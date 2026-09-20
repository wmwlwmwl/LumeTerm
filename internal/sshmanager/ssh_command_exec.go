package sshmanager

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	aitypes "lumeterm/internal/aitypes"
	"lumeterm/internal/mcp"
	"lumeterm/internal/mcpserver"
)

var luminExitCodePattern = regexp.MustCompile(`\[LumeTerm_EXIT_CODE_(\d+)\]`)

const maxInteractiveCapturedOutputBytes = 1 << 20
const interactiveIdlePollInterval = 200 * time.Millisecond
const interactiveIdleGraceDuration = 600 * time.Millisecond

func (m *SSHManager) ExecuteCommandInTerminal(sessionID string, command string, purpose string, isMutating bool, cwd string, shellType string, timeout time.Duration) (mcpserver.CommandExecutionResult, error) {
	result := mcpserver.CommandExecutionResult{
		SessionID:  sessionID,
		Command:    command,
		Purpose:    purpose,
		IsMutating: isMutating,
		CWD:        cwd,
		ShellType:  shellType,
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	m.mu.RLock()
	sessionData, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok || sessionData == nil || sessionData.Stdin == nil {
		return result, fmt.Errorf("session not found")
	}
	startMarker := "[LumeTerm_START_" + newCommandExecutionToken() + "]"
	endMarker := "[LumeTerm_END_" + newCommandExecutionToken() + "]"
	_, outputChannel, cancel := m.registerSessionOutputTap(sessionID)
	defer cancel()
	if _, _, _, err := m.waitForInteractiveSessionIdle(sessionID, nil, nil, outputChannel, nil); err != nil {
		return result, err
	}
	drainInteractiveOutputChannel(outputChannel)
	wrappedCommand, err := m.prepareInteractiveCommandWrapper(sessionID, command, cwd, shellType, startMarker, endMarker)
	if err != nil {
		return result, err
	}
	if !strings.HasSuffix(wrappedCommand, "\n") {
		wrappedCommand += "\n"
	}
	m.WriteBytes(sessionID, []byte(wrappedCommand))
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var captured strings.Builder
	for {
		select {
		case chunk, ok := <-outputChannel:
			raw := captured.String()
			if ok {
				writeLimitedInteractiveOutput(&captured, chunk)
				raw = captured.String()
			}
			completed := strings.Contains(raw, endMarker)
			if completed || !ok {
				if completed {
					m.markInteractiveSessionPromptReady(sessionID)
				}
				exitCode, hasExitCode := extractInteractiveExitCode(raw)
				if hasExitCode {
					result.ExitCode = &exitCode
				}
				result.Output = sanitizeInteractiveCommandOutput(raw, startMarker, endMarker)
				return result, nil
			}
		case <-deadline.C:
			raw := captured.String()
			exitCode, hasExitCode := extractInteractiveExitCode(raw)
			if hasExitCode {
				result.ExitCode = &exitCode
			}
			result.TimedOut = true
			result.Output = sanitizeInteractiveCommandOutput(raw, startMarker, endMarker)
			return result, nil
		}
	}
}

func (m *SSHManager) waitForInteractiveSessionIdle(sessionID string, control <-chan aitypes.ToolExecutionAction, reassign <-chan string, outputChannel <-chan []byte, onWaitStart func()) (aitypes.ToolExecutionAction, bool, string, error) {
	waitingNotified := false
	reassignChannel := reassign
	graceDeadline := time.Time{}

	waitOnce := func(waitDuration time.Duration) (aitypes.ToolExecutionAction, string, error) {
		select {
		case nextSessionID, ok := <-reassignChannel:
			if !ok {
				reassignChannel = nil
				return aitypes.ToolExecutionActionNone, "", nil
			}
			trimmedSessionID := strings.TrimSpace(nextSessionID)
			if trimmedSessionID == "" || trimmedSessionID == sessionID {
				return aitypes.ToolExecutionActionNone, "", nil
			}
			return aitypes.ToolExecutionActionNone, trimmedSessionID, nil
		case action, ok := <-control:
			if !ok {
				return aitypes.ToolExecutionActionNone, "", nil
			}
			if action == aitypes.ToolExecutionActionTerminate {
				return aitypes.ToolExecutionActionTerminate, "", nil
			}
			return aitypes.ToolExecutionActionNone, "", nil
		case _, ok := <-outputChannel:
			if !ok {
				return aitypes.ToolExecutionActionNone, "", fmt.Errorf("session output unavailable")
			}
			return aitypes.ToolExecutionActionNone, "", nil
		case <-time.After(waitDuration):
			return aitypes.ToolExecutionActionNone, "", nil
		}
	}

	for {
		m.mu.RLock()
		sessionData, ok := m.sessions[sessionID]
		shouldWait := ok && sessionData != nil && sessionData.RemoteHistoryActive && !sessionData.PromptReady
		m.mu.RUnlock()
		if !ok || sessionData == nil {
			return aitypes.ToolExecutionActionNone, waitingNotified, "", fmt.Errorf("session not found")
		}
		if !shouldWait {
			return aitypes.ToolExecutionActionNone, waitingNotified, "", nil
		}
		if !waitingNotified {
			if graceDeadline.IsZero() {
				graceDeadline = time.Now().Add(interactiveIdleGraceDuration)
			}
			if remaining := time.Until(graceDeadline); remaining > 0 {
				waitDuration := interactiveIdlePollInterval
				if remaining < waitDuration {
					waitDuration = remaining
				}
				action, reassignedSessionID, err := waitOnce(waitDuration)
				if err != nil {
					return aitypes.ToolExecutionActionNone, waitingNotified, "", err
				}
				if action == aitypes.ToolExecutionActionTerminate {
					return aitypes.ToolExecutionActionTerminate, waitingNotified, "", nil
				}
				if reassignedSessionID != "" {
					return aitypes.ToolExecutionActionNone, waitingNotified, reassignedSessionID, nil
				}
				continue
			}
			waitingNotified = true
			if onWaitStart != nil {
				onWaitStart()
			}
		}
		action, reassignedSessionID, err := waitOnce(interactiveIdlePollInterval)
		if err != nil {
			return aitypes.ToolExecutionActionNone, waitingNotified, "", err
		}
		if action == aitypes.ToolExecutionActionTerminate {
			return aitypes.ToolExecutionActionTerminate, waitingNotified, "", nil
		}
		if reassignedSessionID != "" {
			return aitypes.ToolExecutionActionNone, waitingNotified, reassignedSessionID, nil
		}
	}
}

func (m *SSHManager) markInteractiveSessionPromptReady(sessionID string) {
	m.mu.Lock()
	if sessionData, ok := m.sessions[sessionID]; ok && sessionData != nil && sessionData.RemoteHistoryActive {
		sessionData.PromptReady = true
	}
	m.mu.Unlock()
}

func drainInteractiveOutputChannel(outputChannel <-chan []byte) {
	for {
		select {
		case _, ok := <-outputChannel:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

func (m *SSHManager) ExecuteCommandInTerminalControlled(sessionID string, command string, purpose string, isMutating bool, cwd string, shellType string, timeout time.Duration, control <-chan aitypes.ToolExecutionAction, reassign <-chan string, onCommandQueued func(), onCommandStarted func(), onCommandOutput func(string)) (mcpserver.CommandExecutionResult, aitypes.ToolExecutionAction, error) {
	result := mcpserver.CommandExecutionResult{
		SessionID:  sessionID,
		Command:    command,
		Purpose:    purpose,
		IsMutating: isMutating,
		CWD:        cwd,
		ShellType:  shellType,
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	currentSessionID := sessionID
	startMarker := "[LumeTerm_START_" + newCommandExecutionToken() + "]"
	endMarker := "[LumeTerm_END_" + newCommandExecutionToken() + "]"
	queuedDuringWait := false

	var outputChannel <-chan []byte
	var cancel func()

	for {
		m.mu.RLock()
		sessionData, ok := m.sessions[currentSessionID]
		m.mu.RUnlock()
		if !ok || sessionData == nil || sessionData.Stdin == nil {
			return result, aitypes.ToolExecutionActionNone, fmt.Errorf("session not found")
		}

		result.SessionID = currentSessionID
		_, nextOutputChannel, nextCancel := m.registerSessionOutputTap(currentSessionID)
		waitOutcome, waited, reassignedSessionID, err := m.waitForInteractiveSessionIdle(
			currentSessionID,
			control,
			reassign,
			nextOutputChannel,
			func() {
				if queuedDuringWait {
					return
				}
				if onCommandQueued != nil {
					onCommandQueued()
				}
			},
		)
		if waited {
			queuedDuringWait = true
		}
		if err != nil {
			nextCancel()
			return result, aitypes.ToolExecutionActionNone, err
		}
		if waitOutcome == aitypes.ToolExecutionActionTerminate {
			nextCancel()
			return result, aitypes.ToolExecutionActionTerminate, nil
		}
		if reassignedSessionID != "" && reassignedSessionID != currentSessionID {
			nextCancel()
			currentSessionID = reassignedSessionID
			continue
		}

		outputChannel = nextOutputChannel
		cancel = nextCancel
		break
	}
	if cancel != nil {
		defer cancel()
	}

	drainInteractiveOutputChannel(outputChannel)
	wrappedCommand, err := m.prepareInteractiveCommandWrapper(currentSessionID, command, cwd, shellType, startMarker, endMarker)
	if err != nil {
		return result, aitypes.ToolExecutionActionNone, err
	}
	if !strings.HasSuffix(wrappedCommand, "\n") {
		wrappedCommand += "\n"
	}
	if queuedDuringWait && onCommandStarted != nil {
		onCommandStarted()
	}
	m.WriteBytes(currentSessionID, []byte(wrappedCommand))
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	var terminateTimer *time.Timer
	var terminateDeadline <-chan time.Time
	defer func() {
		if terminateTimer != nil {
			terminateTimer.Stop()
		}
	}()

	var captured strings.Builder
	decisionRequired := false
	terminationRequested := false

	for {
		select {
		case action, ok := <-control:
			if !ok {
				continue
			}
			switch action {
			case aitypes.ToolExecutionActionContinue:
				if decisionRequired {
					result.Output = sanitizeInteractiveCommandOutput(captured.String(), startMarker, endMarker)
					return result, aitypes.ToolExecutionActionContinue, nil
				}
			case aitypes.ToolExecutionActionTerminate:
				terminationRequested = true
				m.WriteBytes(currentSessionID, []byte{3})
				if terminateTimer == nil {
					terminateTimer = time.NewTimer(3 * time.Second)
					terminateDeadline = terminateTimer.C
				}
			}
		case chunk, ok := <-outputChannel:
			raw := captured.String()
			if ok {
				writeLimitedInteractiveOutput(&captured, chunk)
				raw = captured.String()
			}
			snapshot := sanitizeInteractiveCommandOutput(raw, startMarker, endMarker)
			completed := strings.Contains(raw, endMarker)
			if !decisionRequired && strings.TrimSpace(snapshot) != "" && !completed {
				decisionRequired = true
				if onCommandOutput != nil {
					onCommandOutput(snapshot)
				}
			}
			if completed || !ok {
				if completed {
					m.markInteractiveSessionPromptReady(currentSessionID)
				}
				exitCode, hasExitCode := extractInteractiveExitCode(raw)
				if hasExitCode {
					result.ExitCode = &exitCode
				}
				result.Output = snapshot
				if terminationRequested {
					return result, aitypes.ToolExecutionActionTerminate, nil
				}
				return result, aitypes.ToolExecutionActionNone, nil
			}
		case <-terminateDeadline:
			result.Output = sanitizeInteractiveCommandOutput(captured.String(), startMarker, endMarker)
			return result, aitypes.ToolExecutionActionTerminate, nil
		case <-deadline.C:
			raw := captured.String()
			exitCode, hasExitCode := extractInteractiveExitCode(raw)
			if hasExitCode {
				result.ExitCode = &exitCode
			}
			result.TimedOut = true
			result.Output = sanitizeInteractiveCommandOutput(raw, startMarker, endMarker)
			if terminationRequested {
				return result, aitypes.ToolExecutionActionTerminate, nil
			}
			return result, aitypes.ToolExecutionActionNone, nil
		}
	}
}

type unixInteractiveCommandPlan struct {
	scriptPath      string
	scriptContent   string
	terminalCommand string
}

func (m *SSHManager) prepareInteractiveCommandWrapper(sessionID string, command string, cwd string, shellType string, startMarker string, endMarker string) (string, error) {
	normalizedShellType := strings.TrimSpace(shellType)
	if normalizedShellType == "" {
		normalizedShellType = "zsh"
	}
	switch normalizedShellType {
	case "zsh":
		plan := buildUnixInteractiveCommandPlan(command, cwd, startMarker, endMarker)
		if err := m.stageUnixInteractiveCommandScript(sessionID, plan.scriptPath, plan.scriptContent); err != nil {
			return "", err
		}
		return plan.terminalCommand, nil
	case "powershell":
		return buildPowerShellInteractiveCommandWrapper(command, cwd, startMarker, endMarker), nil
	case "cmd":
		return buildCmdInteractiveCommandWrapper(command, cwd, startMarker, endMarker), nil
	default:
		return "", fmt.Errorf("unsupported shellType: %s", normalizedShellType)
	}
}

func buildUnixInteractiveCommandPlan(command string, cwd string, startMarker string, endMarker string) unixInteractiveCommandPlan {
	token := newCommandExecutionToken()
	scriptPath := "/tmp/lumeterm_mcp_" + token + ".sh"
	scriptLines := []string{
		"#!/bin/sh",
		"cleanup() {",
		"  lumeterm_mcp_exit=$?",
		"  rm -f " + quotePOSIX(scriptPath),
		`  printf '%s\n' "[LumeTerm_EXIT_CODE_${lumeterm_mcp_exit}]"`,
		"  printf '%s\\n' " + quotePOSIX(endMarker),
		"  exit 0",
		"}",
		"trap cleanup EXIT",
	}
	if strings.TrimSpace(cwd) != "" {
		scriptLines = append(scriptLines, "cd "+quotePOSIX(cwd))
	}
	scriptLines = append(scriptLines, "printf '%s\\n' "+quotePOSIX(startMarker))
	scriptLines = append(scriptLines, command)
	return unixInteractiveCommandPlan{
		scriptPath:      scriptPath,
		scriptContent:   strings.Join(scriptLines, "\n"),
		terminalCommand: "sh " + quotePOSIX(scriptPath),
	}
}

func (m *SSHManager) stageUnixInteractiveCommandScript(sessionID string, scriptPath string, scriptContent string) error {
	sftpErr := m.stageUnixInteractiveCommandScriptViaSFTP(sessionID, scriptPath, scriptContent)
	if sftpErr == nil {
		return nil
	}
	execErr := m.stageUnixInteractiveCommandScriptViaExec(sessionID, scriptPath, scriptContent)
	if execErr != nil {
		return fmt.Errorf("stage via sftp failed: %v; fallback exec failed: %w", sftpErr, execErr)
	}
	return nil
}

func (m *SSHManager) stageUnixInteractiveCommandScriptViaSFTP(sessionID string, scriptPath string, scriptContent string) error {
	stageCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.WriteFileContext(stageCtx, sessionID, scriptPath, scriptContent); err != nil {
		return err
	}
	sftpClient, err := m.GetSFTPClient(sessionID)
	if err != nil {
		return err
	}
	if err := sftpClient.Chmod(scriptPath, 0o700); err != nil {
		_ = sftpClient.Remove(scriptPath)
		return err
	}
	return nil
}

func (m *SSHManager) stageUnixInteractiveCommandScriptViaExec(sessionID string, scriptPath string, scriptContent string) error {
	client, _, err := m.GetClientEntry(sessionID)
	if err != nil {
		return err
	}
	stageCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = m.ExecuteCmdWithClientContext(stageCtx, client, buildUnixInteractiveCommandStageExecCommand(scriptPath, scriptContent))
	return err
}

func buildUnixInteractiveCommandStageExecCommand(scriptPath string, scriptContent string) string {
	encodedScript := base64.StdEncoding.EncodeToString([]byte(scriptContent))
	pythonCode := fmt.Sprintf("import base64, os; path = %q; content = base64.b64decode(%q); handle = open(path, 'wb'); handle.write(content); handle.close(); os.chmod(path, 0o700)", scriptPath, encodedScript)
	return strings.Join([]string{
		"set -e",
		"rm -f " + quotePOSIX(scriptPath),
		"if command -v python3 >/dev/null 2>&1; then",
		"python3 -c " + quotePOSIX(pythonCode),
		"elif command -v python >/dev/null 2>&1; then",
		"python -c " + quotePOSIX(pythonCode),
		"else",
		"printf '%s' " + quotePOSIX(encodedScript) + " | base64 -d > " + quotePOSIX(scriptPath),
		"chmod 700 " + quotePOSIX(scriptPath),
		"fi",
	}, "\n")
}

func buildPowerShellInteractiveCommandWrapper(command string, cwd string, startMarker string, endMarker string) string {
	token := newCommandExecutionToken()
	scriptLines := []string{
		`$ErrorActionPreference = "Continue"`,
		`try {`,
	}
	if strings.TrimSpace(cwd) != "" {
		scriptLines = append(scriptLines, "Set-Location -LiteralPath '"+escapePowerShellSingleQuoted(cwd)+"'")
	}
	scriptLines = append(scriptLines, "Write-Output '"+escapePowerShellSingleQuoted(startMarker)+"'")
	scriptLines = append(scriptLines, command)
	scriptLines = append(scriptLines,
		`} finally {`,
		`  $lumeterm_mcp_exit = $LASTEXITCODE`,
		`  if ($lumeterm_mcp_exit -eq $null) { $lumeterm_mcp_exit = if ($?) { 0 } else { 1 } }`,
		`  Remove-Item -LiteralPath $MyInvocation.MyCommand.Path -Force -ErrorAction SilentlyContinue`,
		`  Write-Output ("[LumeTerm_EXIT_CODE_" + $lumeterm_mcp_exit + "]")`,
		`  Write-Output '`+escapePowerShellSingleQuoted(endMarker)+`'`,
		`  exit 0`,
		`}`,
	)
	encodedScript := base64.StdEncoding.EncodeToString([]byte(strings.Join(scriptLines, "\r\n")))
	return strings.Join([]string{
		"$__lumeterm_path = Join-Path $env:TEMP 'lumeterm_mcp_" + token + ".ps1'",
		"$__lumin_bytes = [System.Convert]::FromBase64String('" + encodedScript + "')",
		"[System.IO.File]::WriteAllBytes($__lumeterm_path, $__lumin_bytes)",
		"& $__lumeterm_path",
	}, "\r\n")
}

func buildCmdInteractiveCommandWrapper(command string, cwd string, startMarker string, endMarker string) string {
	token := newCommandExecutionToken()
	scriptLines := []string{
		"@echo off",
	}
	if strings.TrimSpace(cwd) != "" {
		scriptLines = append(scriptLines, `cd /d "`+escapeCmdQuoted(cwd)+`"`)
	}
	scriptLines = append(scriptLines,
		"echo "+startMarker,
		command,
		`set "__LUMIN_EXIT=%ERRORLEVEL%"`,
		`echo [LumeTerm_EXIT_CODE_%__LUMIN_EXIT%]`,
		"echo "+endMarker,
		`(goto) 2>nul & del "%~f0"`,
		`exit /b 0`,
	)
	encodedScript := base64.StdEncoding.EncodeToString([]byte(strings.Join(scriptLines, "\r\n")))
	return strings.Join([]string{
		`powershell -NoProfile -Command "$p = Join-Path $env:TEMP 'lumeterm_mcp_` + token + `.cmd'; [System.IO.File]::WriteAllBytes($p, [System.Convert]::FromBase64String('` + encodedScript + `'))"`,
		`call "%TEMP%\lumeterm_mcp_` + token + `.cmd"`,
	}, "\r\n")
}

func writeLimitedInteractiveOutput(captured *strings.Builder, chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	if captured.Len()+len(chunk) <= maxInteractiveCapturedOutputBytes {
		captured.Write(chunk)
		return
	}
	combined := captured.String() + string(chunk)
	if len(combined) > maxInteractiveCapturedOutputBytes {
		combined = combined[len(combined)-maxInteractiveCapturedOutputBytes:]
	}
	captured.Reset()
	captured.WriteString(combined)
}

func sanitizeInteractiveCommandOutput(output string, startMarker string, endMarker string) string {
	if startIndex := strings.Index(output, startMarker); startIndex >= 0 {
		output = output[startIndex+len(startMarker):]
	}
	if endIndex := strings.Index(output, endMarker); endIndex >= 0 {
		output = output[:endIndex]
	}
	output = luminExitCodePattern.ReplaceAllString(output, "")
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.Trim(output, "\n\r\t ")
	if output == "" {
		return ""
	}
	return compressTerminalOutput(output, mcp.CurrentTerminalOutputLineLimit(), mcp.CurrentTerminalOutputCharacterLimit())
}

func extractInteractiveExitCode(output string) (int, bool) {
	match := luminExitCodePattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return 0, false
	}
	exitCode := 0
	for _, ch := range match[1] {
		exitCode = exitCode*10 + int(ch-'0')
	}
	return exitCode, true
}

func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func escapePowerShellSingleQuoted(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func escapeCmdQuoted(value string) string {
	return strings.ReplaceAll(value, `"`, `""`)
}

func newCommandExecutionToken() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return hex.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return hex.EncodeToString(buffer)
}
