//go:build !windows
// +build !windows

package sshmanager

import (
	"fmt"
	"log"
	"os"
	"os/exec"

	"lumeterm/internal/localsftp"

	"github.com/creack/pty"
)

// GetLocalShells lists detected shells on UNIX-like systems.
func (m *SSHManager) GetLocalShells() ([]string, error) {
	shells := []string{}
	if sh := os.Getenv("SHELL"); sh != "" {
		shells = append(shells, sh)
	}
	// Fallback/standard shells
	for _, path := range []string{"/bin/zsh", "/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(path); err == nil {
			exists := false
			for _, s := range shells {
				if s == path {
					exists = true
					break
				}
			}
			if !exists {
				shells = append(shells, path)
			}
		}
	}
	if len(shells) == 0 {
		shells = append(shells, "/bin/sh")
	}
	return shells, nil
}

// ConnectLocal spawns a local process using creack/pty.
func (m *SSHManager) ConnectLocal(sessionId string, name string, shellPath string, cwd string) error {
	workDir := cwd
	if workDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			workDir = home
		}
	}

	cmd := exec.Command(shellPath)
	cmd.Dir = workDir
	cmd.Env = os.Environ()

	// 传初始 Winsize，避免 PTY 以 0x0 出生：部分 shell 在 0 列下 readline
	// 不绘制提示符/回显，且首屏尺寸与前端不同步。前端会在 WS onopen 后
	// 主动发一次实际 fit 尺寸，这里只需一个合理的出生尺寸。
	initialSize := &pty.Winsize{Rows: defaultTermRows, Cols: defaultTermCols}
	ptyFile, err := pty.StartWithSize(cmd, initialSize)
	if err != nil {
		return fmt.Errorf("pty start error: %w", err)
	}

	sd := &SessionData{
		IsLocal:      true,
		LocalPTYUnix: ptyFile,
		Stdin:        ptyFile,
		Cmd:          cmd,
		ShellPath:    shellPath,
		PromptReady:  true,
	}

	m.mu.Lock()
	m.nextGen++
	sd.Gen = m.nextGen
	m.sessions[sessionId] = sd
	m.mu.Unlock()

	// Start an embedded SFTP server so the file manager can work for this session.
	connKey := "local://" + sessionId
	mapPath := localsftp.UnixPathMapper("/")
	if sftpServer, sshClient, sftpClient, err := localsftp.Start(mapPath, nil); err != nil {
		log.Printf("[connectLocal] embedded SFTP server failed (file manager unavailable): %v", err)
	} else {
		entry := &sshClientEntry{Client: sshClient, SFTP: sftpClient}
		m.mu.Lock()
		m.clients[connKey] = entry
		sd.ConnKey = connKey
		sd.LocalSFTPSrv = sftpServer
		m.mu.Unlock()
	}

	// Start background CWD polling monitor for file manager sync
	m.StartLocalCwdMonitor(sessionId)

	// Wait and notify exit. Capture gen so a fast reconnect that reused sessionId
	// (a newer entry now sits in the map) doesn't get torn down by this stale waiter.
	go func() {
		_ = cmd.Wait()
		m.disconnectCurrentGen(sessionId, sd.Gen)
	}()

	// Pipe output from local pty file to WebSocket. Reuses pipeLocalOutput (the
	// same path as Windows) so the per-session output taps stay fed (AI command
	// execution captures output via these taps) and teardown is guarded against
	// a trailing read after Disconnect. Unix sessions set no OSCCwdParser, so the
	// parser branch is skipped and this behaves as a plain passthrough.
	m.pipeLocalOutput(sessionId, ptyFile, nil)

	return nil
}

// ResizeLocal handles resizing on UNIX platforms.
func (m *SSHManager) ResizeLocal(s *SessionData, cols, rows int) {
	// Snapshot under the lock: CloseLocal may nil the field concurrently.
	m.mu.RLock()
	ptyFile := s.LocalPTYUnix
	m.mu.RUnlock()
	if ptyFile != nil {
		_ = pty.Setsize(ptyFile, &pty.Winsize{
			Cols: uint16(cols),
			Rows: uint16(rows),
		})
	}
}

// CloseLocal closes the UNIX PTY handle and kills the process.
// Field mutation happens under m.mu so concurrent readers (ResizeLocal, the
// CWD monitor and localsftp.CurrentWorkingDirectory never observe a half-nilled session. The actual
// Close/Kill calls run outside the lock since they may block.
func (m *SSHManager) CloseLocal(s *SessionData) {
	m.mu.Lock()
	ptyFile := s.LocalPTYUnix
	s.LocalPTYUnix = nil
	cmd := s.Cmd
	s.Cmd = nil
	m.mu.Unlock()

	if ptyFile != nil {
		_ = ptyFile.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
