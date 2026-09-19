//go:build windows
// +build windows

package sshmanager

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"

	"luminssh-go/internal/localsftp"
	"luminssh-go/internal/terminalstream"

	"github.com/UserExistsError/conpty"
)

// GetLocalShells lists detected shells on Windows.
func (m *SSHManager) GetLocalShells() ([]string, error) {
	var shells []string
	if path, err := exec.LookPath("pwsh.exe"); err == nil && path != "" {
		shells = append(shells, "pwsh.exe")
	} else if _, err := os.Stat(`C:\Program Files\PowerShell\7\pwsh.exe`); err == nil {
		shells = append(shells, `C:\Program Files\PowerShell\7\pwsh.exe`)
	}
	shells = append(shells, "powershell.exe", "cmd.exe")
	if distros, _ := listWSLDistros(); len(distros) > 0 {
		for _, d := range distros {
			shells = append(shells, "wsl://"+d)
		}
	}
	return shells, nil
}

func listWSLDistros() ([]string, error) {
	cmd := exec.Command("wsl.exe", "-l", "-q")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	return parseWSLDistros(out), nil
}

func parseWSLDistros(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	content := string(raw)
	if len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE {
		u16 := make([]uint16, 0, len(raw)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
		}
		content = string(utf16.Decode(u16))
	}
	var distros []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		line = strings.ReplaceAll(line, "\x00", "")
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "docker-desktop") {
			continue
		}
		if !seen[line] {
			seen[line] = true
			distros = append(distros, line)
		}
	}
	return distros
}

// ConnectLocal spawns a local command process via ConPTY on Windows.
func (m *SSHManager) ConnectLocal(sessionId string, name string, shellPath string, cwd string) error {
	workDir := cwd
	if workDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			workDir = home
		}
	}

	var commandLine string
	var cmd *exec.Cmd
	var distroName, isWSL = parseWSLPath(shellPath)
	// cptyEnv is the environment passed to ConPTY. For WSL it carries the
	// PROMPT_COMMAND hook via WSLENV so the shell reports its CWD as an OSC
	// marker in the PTY stream (parsed by pipeLocalOutput). OSC is used because
	// Windows ConPTY strips the \x1f/\x1e control bytes that remote SSH relies on.
	cptyEnv := os.Environ()

	if isWSL {
		// Launch an interactive login bash that first cds to $HOME, so the session
		// starts in the WSL user's home dir rather than the Windows working dir
		// (wsl.exe otherwise inherits /mnt/c/... from the host CWD). The
		// PROMPT_COMMAND hook arrives via WSLENV, unaffected by this command line.
		commandLine = fmt.Sprintf("wsl.exe -d %s -- bash -lc \"cd; exec bash -il\"", distroName)
		cmd = exec.Command("wsl.exe", "-d", distroName, "--", "bash", "-lc", "cd; exec bash -il")
		cptyEnv = append(cptyEnv,
			"PROMPT_COMMAND="+wslPromptCommandHook(),
			// WSLENV forwards the Windows-side PROMPT_COMMAND into WSL ('/u' = path-style unset, plain var).
			"WSLENV=PROMPT_COMMAND",
		)
	} else if isPowerShell(shellPath) {
		// PowerShell: launch with the prompt hook preloaded via -EncodedCommand so
		// the shell reports its CWD as OSC 733 markers on every prompt. -NoExit keeps
		// it interactive. OSCCwdParser (attached below) parses those markers so the
		// file manager follows cd, without relying on the home-dir polling fallback.
		encodedHook := base64.StdEncoding.EncodeToString(utf16Encode(powershellPromptHookScript()))
		commandLine = fmt.Sprintf(`%s -NoLogo -NoExit -EncodedCommand %s`, shellPath, encodedHook)
		cmd = exec.Command(shellPath, "-NoLogo", "-NoExit", "-EncodedCommand", encodedHook)
	} else {
		commandLine = buildCommandLine(shellPath)
		cmd = exec.Command(shellPath)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Dir = workDir

	var cptyHandle *conpty.ConPty
	var stdinPipe io.WriteCloser
	var stdoutPipe io.Reader
	var err error

	if conpty.IsConPtyAvailable() {
		c, err := conpty.Start(commandLine, conpty.ConPtyDimensions(defaultTermCols, defaultTermRows), conpty.ConPtyWorkDir(workDir), conpty.ConPtyEnv(cptyEnv))
		if err == nil {
			cptyHandle = c
		}
	}

	if cptyHandle == nil {
		stdinPipe, err = cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("stdin pipe error: %w", err)
		}
		stdoutPipe, err = cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("stdout pipe error: %w", err)
		}
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("cmd start error: %w", err)
		}
	}

	sd := &SessionData{
		IsLocal:         true,
		LocalPTYWindows: cptyHandle,
		Cmd:             cmd,
		ShellPath:       shellPath,
	}
	if isWSL {
		// The WSL shell emits OSC CWD markers (ESC]733;<b64>BEL) on every prompt.
		// pipeLocalOutput parses OSC CWD markers to drive the file manager.
		sd.WSLDistro = distroName
		sd.RemoteHistoryActive = true
		sd.OSCCwdParser = terminalstream.NewOSCCWDParser()
		sd.PromptReady = false
	} else if isPowerShell(shellPath) {
		// PowerShell emits OSC 733 markers via the injected prompt hook. Parse them
		// the same way as WSL. NOTE: RemoteHistoryActive is deliberately NOT set — it
		// would enable the AI command-execution idle gate (ssh_command_exec.go),
		// which needs a LUMIN_CMD marker stream PowerShell doesn't have, leaving the
		// session stuck "busy". PromptReady stays true so the gate/busy state is
		// unaffected; CWD still flows through the OSCCwdParser path independently.
		sd.OSCCwdParser = terminalstream.NewOSCCWDParser()
		sd.PromptReady = true
	} else {
		// CMD: no shell hook available, keep the home-dir fallback.
		sd.PromptReady = true
	}

	if cptyHandle != nil {
		sd.Stdin = cptyHandle
	} else {
		sd.Stdin = stdinPipe
	}

	m.mu.Lock()
	m.nextGen++
	sd.Gen = m.nextGen
	m.sessions[sessionId] = sd
	m.mu.Unlock()

	// Start an embedded SFTP server so the file manager can work for this session.
	connKey := "local://" + sessionId
	var mapPath func(string) string
	var listRoot func() ([]os.FileInfo, error)
	if isWSL {
		mapPath = localsftp.WSLPathMapper(distroName)
	} else {
		mapPath = localsftp.WindowsPathMapper()
		listRoot = localsftp.ListRoot
	}
	if sftpServer, sshClient, sftpClient, err := localsftp.Start(mapPath, listRoot); err != nil {
		log.Printf("[connectLocal] embedded SFTP server failed (file manager unavailable): %v", err)
	} else {
		entry := &sshClientEntry{Client: sshClient, SFTP: sftpClient}
		m.mu.Lock()
		m.clients[connKey] = entry
		sd.ConnKey = connKey
		sd.LocalSFTPSrv = sftpServer
		m.mu.Unlock()
	}

	// Start background CWD polling monitor for file manager sync.
	// (WSL sessions skip this — they report CWD via the marker stream below.)
	m.StartLocalCwdMonitor(sessionId)

	// Wait and notify exit. Capture gen so a fast reconnect that reused sessionId
	// (a newer entry now sits in the map) doesn't get torn down by this stale waiter.
	go func() {
		if cptyHandle != nil {
			_, _ = cptyHandle.Wait(context.Background())
		} else {
			_ = cmd.Wait()
		}
		m.disconnectCurrentGen(sessionId, sd.Gen)
	}()

	// Pipe output from local pty to WebSocket. For WSL sessions the stream is
	// run through the terminalstream command parser (same as remote SSH) so LUMIN_CWD markers
	// are parsed into CWD changes that drive the file manager follow.
	m.pipeLocalOutput(sessionId, cptyHandle, stdoutPipe)

	return nil
}

func parseWSLPath(path string) (distro string, ok bool) {
	const prefix = "wsl://"
	if !strings.HasPrefix(strings.ToLower(path), prefix) {
		return "", false
	}
	return path[len(prefix):], true
}

func buildCommandLine(shell string) string {
	lower := strings.ToLower(shell)
	quoted := fmt.Sprintf(`"%s"`, shell)
	if strings.Contains(lower, "cmd.exe") {
		return fmt.Sprintf(`"%s" /k`, shell)
	}
	return quoted
}

// wslPromptCommandHook returns the PROMPT_COMMAND script injected into a WSL
// shell so it reports its CWD into the PTY stream on every prompt.
//
// It emits an OSC 733 sequence: ESC ] 733 ; <base64 of pwd> BEL
// OSC is used instead of the \x1fLUMIN_CWD\x1f markers that remote SSH uses,
// because Windows ConPTY strips \x1f/\x1e control bytes, while OSC sequences
// (like the terminal-title OSC 0) pass through intact.
//
// Passed via the WSLENV-forwarded PROMPT_COMMAND environment variable rather
// than a wsl.exe command line, which avoids shell-quoting/interop pitfalls.
// The hook chains to any pre-existing PROMPT_COMMAND the user's profile set.
func wslPromptCommandHook() string {
	// $1 below is the literal PROMPT_COMMAND saved before the user's profile runs;
	// since this is injected as the env PROMPT_COMMAND, $LUMIN_OLD captures it.
	return `LUMIN_CWD="$(pwd 2>/dev/null | tr -d '\r\n' | base64 | tr -d '\r\n')"; ` +
		`[ -n "$LUMIN_CWD" ] && printf '\033]733;%s\007' "$LUMIN_CWD"; ` +
		`[ -n "${LUMIN_OLD_PROMPT_COMMAND:-}" ] && eval "$LUMIN_OLD_PROMPT_COMMAND"`
}

// powershellPromptHookScript returns the PowerShell script that overrides the
// `prompt` function so every prompt reports the current directory as an OSC 733
// marker (ESC]733;<base64>SFTP-path>BEL). This is the PowerShell equivalent of
// wslPromptCommandHook: it lets pipeLocalOutput track CWD and
// drive the file manager's follow, since PowerShell has no /proc/<pid>/cwd to poll.
//
// The new prompt chains to the user's pre-existing prompt ScriptBlock (their
// $PROFILE may customize it), only prepending the marker — so the visible prompt
// is unchanged. If there is no prior prompt, it falls back to the PS default.
//
// The marker payload is the SFTP-style path produced by an in-script equivalent
// of windowsPathToSFTP (C:\Users\foo -> /c/Users/foo), so it lines up with
// winPathMapper without any backend-side remapping.
func powershellPromptHookScript() string {
	return strings.Join([]string{
		// Capture the user's prompt (set by their $PROFILE, which runs because we
		// don't pass -NoProfile). Get-Command returns the default prompt too, so
		// $__LuminPrevPrompt is normally non-null.
		`try { $__LuminPrevPrompt = (Get-Command prompt -ErrorAction Stop).ScriptBlock } catch { $__LuminPrevPrompt = $null }`,
		``,
		`function prompt {`,
		`  try {`,
		`    $loc = (Get-Location).Path`,
		// Convert drive-letter path to SFTP style (mirrors windowsPathToSFTP).
		`    if ($loc.Length -ge 2 -and $loc[1] -eq ':') {`,
		`      $sftp = '/' + $loc[0].ToString().ToLower() + $loc.Substring(2).Replace('\','/')`,
		`    } else { $sftp = '/' + ($loc -replace '^/','').Replace('\','/') }`,
		`    $b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($sftp))`,
		// Emit OSC 733 marker; [Console]::Write avoids the trailing newline that
		// Write-Output would add. ConPTY passes OSC sequences through intact.
		`    [Console]::Write([char]27 + "]733;" + $b64 + [char]7)`,
		`  } catch {}`,
		`  if ($__LuminPrevPrompt) { & $__LuminPrevPrompt } else { "PS $(Get-Location)> " }`,
		`}`,
	}, "\r\n")
}

// utf16Encode converts a UTF-8 Go string to its UTF-16LE little-endian byte
// representation, the format PowerShell's -EncodedCommand expects.
func utf16Encode(s string) []byte {
	runes := []rune(s)
	codes := utf16.Encode(runes)
	buf := make([]byte, len(codes)*2)
	for i, c := range codes {
		buf[i*2] = byte(c)
		buf[i*2+1] = byte(c >> 8)
	}
	return buf
}

// isPowerShell reports whether shellPath names a PowerShell executable.
func isPowerShell(shellPath string) bool {
	lower := strings.ToLower(shellPath)
	return strings.Contains(lower, "powershell") || strings.Contains(lower, "pwsh")
}

// ResizeLocal handles Windows ConPTY resizing.
func (m *SSHManager) ResizeLocal(s *SessionData, cols, rows int) {
	// Snapshot under the lock: CloseLocal may nil the field concurrently.
	m.mu.RLock()
	ptyAny := s.LocalPTYWindows
	m.mu.RUnlock()
	// Guard against the typed-nil case: when ConPTY is unavailable connectLocal
	// stores a nil *conpty.ConPty into the any field. The type assertion succeeds
	// (concrete type matches) but the value is nil, and c.Resize would dereference
	// a nil receiver and panic.
	if c, ok := ptyAny.(*conpty.ConPty); ok && c != nil {
		_ = c.Resize(cols, rows)
	}
}

// CloseLocal kills processes and closes PTY on Windows.
// Field mutation happens under m.mu so concurrent readers (ResizeLocal, the
// CWD monitor) never observe a half-nilled session. The actual Close/Kill calls
// run outside the lock since they may block.
func (m *SSHManager) CloseLocal(s *SessionData) {
	m.mu.Lock()
	ptyAny := s.LocalPTYWindows
	s.LocalPTYWindows = nil
	cmd := s.Cmd
	s.Cmd = nil
	m.mu.Unlock()

	if c, ok := ptyAny.(*conpty.ConPty); ok {
		_ = c.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
