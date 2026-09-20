package sshmanager

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"lumeterm/internal/mcpserver"
)

type GitTerminalCandidate struct {
	SessionID   string
	Busy        bool
	Cwd         string
	Current     bool
	Recommended bool
}

var gitCommandNames = map[string]struct{}{
	"add":       {},
	"init":      {},
	"branch":    {},
	"checkout":  {},
	"clean":     {},
	"commit":    {},
	"config":    {},
	"diff":      {},
	"log":       {},
	"push":      {},
	"remote":    {},
	"reset":     {},
	"rev-parse": {},
	"rm":        {},
	"show":      {},
	"status":    {},
}

func normalizeGitArguments(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("git command is required")
	}
	normalized := make([]string, len(args))
	for index, arg := range args {
		if strings.IndexByte(arg, 0) >= 0 {
			return nil, fmt.Errorf("git argument contains NUL byte")
		}
		normalized[index] = arg
	}
	name := strings.TrimSpace(normalized[0])
	if _, ok := gitCommandNames[name]; !ok {
		return nil, fmt.Errorf("unsupported git command: %s", name)
	}
	normalized[0] = name
	return normalized, nil
}
func buildGitCommand(repoPath string, args []string) (string, error) {
	normalizedRepoPath, err := normalizeGitRepositoryPath(repoPath)
	if err != nil {
		return "", err
	}
	normalized, err := normalizeGitArguments(args)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(normalized)+7)
	parts = append(parts, "LC_ALL=C", "GIT_PAGER=cat", "git", "-C", shellQuotePath(normalizedRepoPath), "-c", "core.quotePath=false")
	for _, arg := range normalized {
		parts = append(parts, shellQuotePath(arg))
	}
	return strings.Join(parts, " "), nil
}

func normalizeGitBranchName(value string) (string, error) {
	branchName := strings.TrimSpace(value)
	if branchName == "" {
		return "", fmt.Errorf("git branch name is required")
	}
	if strings.IndexByte(branchName, 0) >= 0 || strings.ContainsAny(branchName, "\r\n\t") {
		return "", fmt.Errorf("git branch name contains invalid characters")
	}
	if strings.HasPrefix(branchName, "-") {
		return "", fmt.Errorf("git branch name is invalid")
	}
	return branchName, nil
}

func buildGitPatchScriptCommand(repoPath string, branchName string) (string, error) {
	normalizedRepoPath, err := normalizeGitRepositoryPath(repoPath)
	if err != nil {
		return "", err
	}
	normalizedBranchName, err := normalizeGitBranchName(branchName)
	if err != nil {
		return "", err
	}
	scriptPath := strings.TrimRight(normalizedRepoPath, "/") + "/patch.sh"
	scriptContent := strings.Join([]string{
		"#!/bin/sh",
		"set -eu",
		"repo=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)",
		"cd \"$repo\"",
		"branch=" + quotePOSIX(normalizedBranchName),
		"if [ \"${1:-}\" = \"j\" ]; then",
		"  git rebase --continue",
		"  exit 0",
		"fi",
		"git checkout \"$branch\" 2>/dev/null || git checkout -b \"$branch\"",
		"git add .",
		"git commit -m \"()\"",
		"git fetch origin",
		"git rebase origin/main",
		"",
	}, "\n")
	encodedScript := base64.StdEncoding.EncodeToString([]byte(scriptContent))
	return "printf '%s' " + quotePOSIX(encodedScript) + " | base64 -d > " + quotePOSIX(scriptPath) + " && chmod 700 " + quotePOSIX(scriptPath), nil
}

func normalizeGitRepositoryPath(repoPath string) (string, error) {
	normalized := strings.TrimSpace(repoPath)
	if normalized == "" {
		return "", fmt.Errorf("git repository path is required")
	}
	if !strings.HasPrefix(normalized, "/") {
		return "", fmt.Errorf("git repository path must be absolute")
	}
	normalized = strings.TrimRight(normalized, "/")
	if normalized == "" {
		normalized = "/"
	}
	if strings.IndexByte(normalized, 0) >= 0 {
		return "", fmt.Errorf("git repository path contains NUL byte")
	}
	return normalized, nil
}
func gitCommandIsMutating(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch strings.TrimSpace(args[0]) {
	case "add", "checkout", "clean", "commit", "push", "reset", "rm":
		return true
	default:
		return false
	}
}
func betterGitTerminalCandidate(left GitTerminalCandidate, right GitTerminalCandidate, currentCwd string) bool {
	if left.Busy != right.Busy {
		return !left.Busy
	}
	leftMatches := currentCwd != "" && strings.TrimSpace(left.Cwd) == currentCwd
	rightMatches := currentCwd != "" && strings.TrimSpace(right.Cwd) == currentCwd
	if leftMatches != rightMatches {
		return leftMatches
	}
	leftHasCwd := strings.TrimSpace(left.Cwd) != ""
	rightHasCwd := strings.TrimSpace(right.Cwd) != ""
	if leftHasCwd != rightHasCwd {
		return leftHasCwd
	}
	return strings.Compare(left.SessionID, right.SessionID) < 0
}
func (m *SSHManager) ListGitTerminalCandidates(sessionID string) ([]GitTerminalCandidate, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if m == nil || trimmedSessionID == "" {
		return nil, fmt.Errorf("session not found")
	}
	m.mu.RLock()
	currentSession, ok := m.sessions[trimmedSessionID]
	if !ok || currentSession == nil {
		m.mu.RUnlock()
		return nil, fmt.Errorf("session not found")
	}
	connKey := currentSession.ConnKey
	currentCwd := strings.TrimSpace(currentSession.CurrentCwd)
	if currentCwd == "" {
		currentCwd = strings.TrimSpace(currentSession.TerminalInitPath)
	}
	sessionIDs := append([]string{}, m.connTerminals[connKey]...)
	candidates := make([]GitTerminalCandidate, 0, len(sessionIDs))
	for _, candidateID := range sessionIDs {
		candidateSession := m.sessions[candidateID]
		if candidateSession == nil || candidateSession.Session == nil || candidateSession.Stdin == nil || candidateSession.IsLocal || candidateSession.IsSerial {
			continue
		}
		candidateCwd := strings.TrimSpace(candidateSession.CurrentCwd)
		if candidateCwd == "" {
			candidateCwd = strings.TrimSpace(candidateSession.TerminalInitPath)
		}
		candidates = append(candidates, GitTerminalCandidate{
			SessionID: strings.TrimSpace(candidateID),
			Busy:      candidateSession.RemoteHistoryActive && !candidateSession.PromptReady,
			Cwd:       candidateCwd,
			Current:   candidateID == trimmedSessionID,
		})
	}
	m.mu.RUnlock()
	if len(candidates) == 0 {
		return []GitTerminalCandidate{}, nil
	}
	currentIndex := -1
	for index := range candidates {
		if candidates[index].Current {
			currentIndex = index
			break
		}
	}
	recommendedIndex := currentIndex
	if currentIndex < 0 || candidates[currentIndex].Busy {
		recommendedIndex = -1
		for index := range candidates {
			if candidates[index].Busy {
				continue
			}
			if recommendedIndex < 0 || betterGitTerminalCandidate(candidates[index], candidates[recommendedIndex], currentCwd) {
				recommendedIndex = index
			}
		}
		if recommendedIndex < 0 {
			recommendedIndex = currentIndex
		}
		if recommendedIndex < 0 {
			recommendedIndex = 0
		}
	}
	for index := range candidates {
		candidates[index].Recommended = index == recommendedIndex
	}
	sort.SliceStable(candidates, func(left int, right int) bool {
		if candidates[left].Recommended != candidates[right].Recommended {
			return candidates[left].Recommended
		}
		if candidates[left].Busy != candidates[right].Busy {
			return !candidates[left].Busy
		}
		if candidates[left].Current != candidates[right].Current {
			return candidates[left].Current
		}
		return strings.Compare(candidates[left].SessionID, candidates[right].SessionID) < 0
	})
	return candidates, nil
}
func normalizeGitRelativePath(filePath string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(filePath), "\\", "/")
	if normalized == "" || strings.HasPrefix(normalized, "/") {
		return "", fmt.Errorf("git file path must be relative")
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("git file path escapes repository")
	}
	return cleaned, nil
}

func (m *SSHManager) GetGitFileModTime(sessionID string, repoPath string, filePath string) (map[string]interface{}, error) {
	normalizedRepoPath, err := normalizeGitRepositoryPath(repoPath)
	if err != nil {
		return nil, err
	}
	normalizedFilePath, err := normalizeGitRelativePath(filePath)
	if err != nil {
		return nil, err
	}
	sftpClient, err := m.GetSFTPClient(sessionID)
	if err != nil {
		return nil, err
	}
	fullPath := strings.TrimRight(normalizedRepoPath, "/") + "/" + normalizedFilePath
	info, err := sftpClient.Stat(fullPath)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"mtime": info.ModTime().Unix(),
		"size":  info.Size(),
	}, nil
}

func (m *SSHManager) AddGitIgnoreEntries(sessionID string, repoPath string, filePaths []string) error {
	normalizedRepoPath, err := normalizeGitRepositoryPath(repoPath)
	if err != nil {
		return err
	}
	entries := make([]string, 0, len(filePaths))
	seen := make(map[string]struct{}, len(filePaths))
	for _, filePath := range filePaths {
		normalizedFilePath, normalizeErr := normalizeGitRelativePath(filePath)
		if normalizeErr != nil {
			return normalizeErr
		}
		if _, exists := seen[normalizedFilePath]; exists {
			continue
		}
		seen[normalizedFilePath] = struct{}{}
		entries = append(entries, normalizedFilePath)
	}
	if len(entries) == 0 {
		return nil
	}
	ignorePath := strings.TrimRight(normalizedRepoPath, "/") + "/.gitignore"
	sftpClient, err := m.GetSFTPClient(sessionID)
	if err != nil {
		return err
	}
	existingContent := ""
	if _, statErr := sftpClient.Stat(ignorePath); statErr == nil {
		existingContent, err = m.ReadFile(sessionID, ignorePath)
		if err != nil {
			return err
		}
	} else if !isRemotePathNotFound(statErr) {
		return statErr
	}
	existingEntries := make(map[string]struct{})
	for _, line := range strings.Split(existingContent, "\n") {
		entry := strings.TrimSpace(line)
		if entry != "" && !strings.HasPrefix(entry, "#") {
			existingEntries[entry] = struct{}{}
		}
	}
	additions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if _, exists := existingEntries[entry]; !exists {
			additions = append(additions, entry)
		}
	}
	if len(additions) == 0 {
		return nil
	}
	if existingContent != "" && !strings.HasSuffix(existingContent, "\n") {
		existingContent += "\n"
	}
	return m.WriteFile(sessionID, ignorePath, existingContent+strings.Join(additions, "\n")+"\n")
}

func gitExecutionPayload(sessionID string, repoPath string, command string, interactive bool, result mcpserver.CommandExecutionResult, commandError error) map[string]interface{} {
	success := commandError == nil && !result.TimedOut && (result.ExitCode == nil || *result.ExitCode == 0)
	payload := map[string]interface{}{
		"success":     success,
		"sessionId":   sessionID,
		"repoPath":    repoPath,
		"command":     command,
		"interactive": interactive,
		"output":      result.Output,
		"timedOut":    result.TimedOut,
	}
	if result.ExitCode != nil {
		payload["exitCode"] = *result.ExitCode
	}
	if commandError != nil {
		payload["error"] = commandError.Error()
	}
	return payload
}
func isRetryableGitChannelOpenError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "rejected: connect failed") || strings.Contains(message, "open failed")
}

func (m *SSHManager) ExecuteGitCommand(sessionID string, repoPath string, args []string, interactive bool) (map[string]interface{}, error) {
	normalizedRepoPath, err := normalizeGitRepositoryPath(repoPath)
	if err != nil {
		return nil, err
	}
	gitCommand, err := buildGitCommand(normalizedRepoPath, args)
	if err != nil {
		return nil, err
	}
	if interactive && len(args) >= 3 && strings.TrimSpace(args[0]) == "checkout" && strings.TrimSpace(args[1]) == "-b" && strings.TrimSpace(args[2]) == "patch" {
		patchScriptCommand, scriptErr := buildGitPatchScriptCommand(normalizedRepoPath, args[2])
		if scriptErr != nil {
			return nil, scriptErr
		}
		gitCommand += " && " + patchScriptCommand
	}
	isMutating := gitCommandIsMutating(args)
	if interactive {
		result, executionErr := m.ExecuteCommandInTerminal(sessionID, gitCommand, "Git repository operation", isMutating, "", "zsh", 5*time.Minute)
		return gitExecutionPayload(sessionID, normalizedRepoPath, gitCommand, true, result, executionErr), executionErr
	}
	client, _, clientErr := m.GetClientEntry(sessionID)
	if clientErr != nil {
		return nil, clientErr
	}
	command := gitCommand
	var output string
	var commandErr error
	for attempt := 0; attempt < 3; attempt++ {
		output, commandErr = m.ExecuteCmdWithClientContext(context.Background(), client, command)
		if commandErr == nil || !isRetryableGitChannelOpenError(commandErr) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 180 * time.Millisecond)
	}
	result := mcpserver.CommandExecutionResult{
		SessionID:  sessionID,
		Command:    gitCommand,
		Purpose:    "Git repository operation",
		IsMutating: isMutating,
		CWD:        normalizedRepoPath,
		ShellType:  "sh",
		Output:     output,
	}
	return gitExecutionPayload(sessionID, normalizedRepoPath, gitCommand, false, result, commandErr), nil
}
