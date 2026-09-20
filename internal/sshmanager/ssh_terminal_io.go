package sshmanager

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"lumeterm/internal/config"
	"lumeterm/internal/localsftp"
	"lumeterm/internal/localsysinfo"
	"lumeterm/internal/terminalstream"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/transform"
)

func terminalEncodingCodec(terminalEncoding string) encoding.Encoding {
	normalized := config.NormalizeTerminalEncoding(terminalEncoding)
	if normalized == "utf-8" {
		return nil
	}
	codec, err := ianaindex.IANA.Encoding(normalized)
	if err != nil || codec == nil {
		return nil
	}
	return codec
}

func wrapTerminalOutputReader(reader io.Reader, terminalEncoding string) io.Reader {
	codec := terminalEncodingCodec(terminalEncoding)
	if reader == nil || codec == nil {
		return reader
	}
	return transform.NewReader(reader, codec.NewDecoder())
}

func decodeTerminalBytesToUTF8(data []byte, terminalEncoding string) ([]byte, error) {
	codec := terminalEncodingCodec(terminalEncoding)
	if len(data) == 0 || codec == nil {
		return data, nil
	}
	decoded, _, err := transform.Bytes(codec.NewDecoder(), data)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func decodeTerminalText(data []byte, terminalEncoding string) string {
	decoded, err := decodeTerminalBytesToUTF8(data, terminalEncoding)
	if err != nil {
		return ""
	}
	return string(decoded)
}

func encodeTerminalInputBytes(data []byte, terminalEncoding string) ([]byte, error) {
	codec := terminalEncodingCodec(terminalEncoding)
	if len(data) == 0 || codec == nil {
		return data, nil
	}
	encoded, _, err := transform.Bytes(codec.NewEncoder(), data)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// ponytail: 判断是否为瞬态网络错误（连接重置、EOF、超时等），这类错误可重试
func (m *SSHManager) pipeOutput(sessionId string, r io.Reader, historyStream *terminalstream.CommandHistoryParser) {
	bufPtr := m.bufPool.Get().(*[]byte)
	defer m.bufPool.Put(bufPtr)
	buf := *bufPtr

	eventSessionId := sessionId
	terminalEncoding := "utf-8"
	m.mu.RLock()
	if s, ok := m.sessions[sessionId]; ok {
		if s.GroupSessionId != "" {
			eventSessionId = s.GroupSessionId
		}
		terminalEncoding = config.NormalizeTerminalEncoding(s.TerminalEncoding)
	}
	m.mu.RUnlock()

	reader := wrapTerminalOutputReader(r, terminalEncoding)

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			var data []byte
			if historyStream != nil {
				visible, commands, cwd, promptSeen := historyStream.Process(buf[:n])
				data = visible
				if cwd != "" || promptSeen {
					shouldEmitCwd := false
					m.mu.Lock()
					if s, ok := m.sessions[sessionId]; ok {
						if cwd != "" && s.CurrentCwd != cwd {
							s.CurrentCwd = cwd
							shouldEmitCwd = true
						}
						if promptSeen && s.RemoteHistoryActive {
							s.PromptReady = true
						}
					}
					m.mu.Unlock()
					if shouldEmitCwd && m.ctx != nil {
						runtime.EventsEmit(m.ctx, "ssh-terminal-cwd-"+sessionId, cwd)
					}
				}
				for _, command := range commands {
					if command == "" || m.ctx == nil {
						continue
					}
					runtime.EventsEmit(m.ctx, "ssh-command-executed", map[string]string{
						"sessionId": eventSessionId,
						"command":   command,
						"time":      time.Now().Format(time.RFC3339),
						"source":    "remote",
					})
				}
			} else {
				data = buf[:n]
			}
			if len(data) == 0 {
				if err != nil {
					return
				}
				continue
			}
			m.emitSessionOutput(sessionId, data)
			if m.app != nil {
				m.app.WriteWsOutput(sessionId, data)
			} else if m.ctx != nil {
				runtime.EventsEmit(m.ctx, "terminal-data-"+sessionId, string(data))
			}
		}
		if err != nil {
			return
		}
	}
}

// pipeLocalOutput pumps bytes from a local PTY/pipe to the frontend. For WSL
// sessions it runs the bytes through the terminalstream OSC CWD parser (which strips the OSC 733
// CWD markers emitted by the PROMPT_COMMAND hook and decodes the CWD), so the
// file manager can follow the shell's working directory. cptyHandle is the
// Windows ConPTY handle (opaque); stdoutPipe is the fallback non-ConPTY reader.
// Exactly one of them is non-nil.
func (m *SSHManager) pipeLocalOutput(sessionId string, cptyHandle any, stdoutPipe io.Reader) {
	go func() {
		bufPtr := m.bufPool.Get().(*[]byte)
		defer m.bufPool.Put(bufPtr)
		buf := *bufPtr

		for {
			var n int
			var err error
			if c, ok := cptyHandle.(interface{ Read([]byte) (int, error) }); ok && c != nil {
				n, err = c.Read(buf)
			} else {
				if stdoutPipe == nil {
					return
				}
				n, err = stdoutPipe.Read(buf)
			}
			if n <= 0 {
				if err != nil {
					return
				}
				continue
			}

			m.mu.RLock()
			// Disconnect removes the session from the map; guard against the
			// pipe goroutine still reading after teardown (nil map value panic).
			curSd, hasSd := m.sessions[sessionId]
			m.mu.RUnlock()
			if !hasSd {
				return
			}
			oscParser := curSd.OSCCwdParser

			var data []byte
			if oscParser != nil {
				visible, cwd, promptSeen := oscParser.Process(buf[:n])
				data = visible
				if cwd != "" || promptSeen {
					shouldEmitCwd := false
					m.mu.Lock()
					if s, ok := m.sessions[sessionId]; ok {
						if cwd != "" && s.CurrentCwd != cwd {
							s.CurrentCwd = cwd
							shouldEmitCwd = true
						}
						if promptSeen && s.RemoteHistoryActive {
							s.PromptReady = true
						}
					}
					m.mu.Unlock()
					if shouldEmitCwd && m.ctx != nil {
						runtime.EventsEmit(m.ctx, "ssh-terminal-cwd-"+sessionId, cwd)
					}
				}
			} else {
				data = make([]byte, n)
				copy(data, buf[:n])
			}

			if len(data) == 0 {
				if err != nil {
					return
				}
				continue
			}
			m.emitSessionOutput(sessionId, data)
			if m.app != nil {
				m.app.WriteWsOutput(sessionId, data)
			} else if m.ctx != nil {
				runtime.EventsEmit(m.ctx, "terminal-data-"+sessionId, string(data))
			}
			if err != nil {
				return
			}
		}
	}()
}

// getClientEntry 查找 session 对应的共享客户端
func (m *SSHManager) GetTerminalCwd(sessionId string) (string, error) {
	m.mu.RLock()
	sessionData, ok := m.sessions[sessionId]
	if !ok {
		m.mu.RUnlock()
		return "", fmt.Errorf("session not found")
	}
	if strings.TrimSpace(sessionData.CurrentCwd) != "" {
		cwd := strings.TrimSpace(sessionData.CurrentCwd)
		m.mu.RUnlock()
		return cwd, nil
	}
	// For local sessions, query OS process tree instead of SSH.
	if sessionData.IsLocal {
		m.mu.RUnlock()
		return m.getLocalCwdForSession(sessionData)
	}
	m.mu.RUnlock()

	client, _, err := m.GetClientEntry(sessionId)
	if err != nil {
		return "", err
	}

	localAddr := client.LocalAddr().String()
	_, portStr, err := net.SplitHostPort(localAddr)
	if err != nil || portStr == "" {
		return "", fmt.Errorf("invalid local address format")
	}
	if _, err := strconv.Atoi(portStr); err != nil {
		return "", fmt.Errorf("invalid local port: %s", portStr)
	}

	cmd := fmt.Sprintf(`gp(){ awk '/^PPid:/{print $2}' /proc/$1/status 2>/dev/null; }; gn(){ cat /proc/$1/comm 2>/dev/null | tr -d '\n'; }; gc(){ for f in /proc/[0-9]*/status; do p=${f#/proc/}; p=${p%%/*}; awk -v t="$1" '/^PPid:/{if($2==t)f=1} END{exit f?0:1}' "$f" 2>/dev/null && echo "$p"; done; }; CUR_PID=$$; SSHD_PID=""; while [ -n "$CUR_PID" ] && [ "$CUR_PID" -gt 1 ]; do CUR_PID=$(gp $CUR_PID); [ -z "$CUR_PID" ] && break; [ "$(gn $CUR_PID)" = "sshd" ] && SSHD_PID=$CUR_PID && break; done; SHELL_PID=""; MY_MNT=$(readlink /proc/$$/ns/mnt 2>/dev/null); ISS(){ echo "$1" | grep -qE '^(sh|bash|zsh|dash|ash|ksh)$'; }; FCS(){ for child in $(gc "$1"); do [ "$child" = "$$" ] || [ "$child" = "$PPID" ] && continue; ISS "$(gn $child)" || continue; PID_MNT=$(readlink /proc/$child/ns/mnt 2>/dev/null); if [ -z "$MY_MNT" ] || [ -z "$PID_MNT" ] || [ "$MY_MNT" = "$PID_MNT" ]; then echo "$child"; return; fi; done; }; if [ -n "$SSHD_PID" ]; then SHELL_PID=$(FCS "$SSHD_PID"); fi; if [ -z "$SHELL_PID" ]; then PORT=%s; SSHD_PID_PORT=$(ss -ntp 2>/dev/null | grep ":$PORT " | grep -oE 'pid=[0-9]+' | cut -d= -f2 | head -n1); [ -z "$SSHD_PID_PORT" ] && SSHD_PID_PORT=$(netstat -ntp 2>/dev/null | grep ":$PORT " | grep -oE '[0-9]+/sshd' | cut -d/ -f1 | head -n1); if [ -n "$SSHD_PID_PORT" ]; then SHELL_PID=$(FCS "$SSHD_PID_PORT"); fi; fi; if [ -z "$SHELL_PID" ]; then MY_UID=$(id -u 2>/dev/null); SHELL_PID=$(for f in /proc/[0-9]*/status; do p=${f#/proc/}; p=${p%%/*}; [ "$p" = "$$" ] || [ "$p" = "$PPID" ] && continue; awk -v u="$MY_UID" '/^Uid:/{if($2==u)f=1} END{exit f?0:1}' "$f" 2>/dev/null || continue; ISS "$(gn $p)" || continue; PID_MNT=$(readlink /proc/$p/ns/mnt 2>/dev/null); if [ -z "$MY_MNT" ] || [ -z "$PID_MNT" ] || [ "$MY_MNT" = "$PID_MNT" ]; then echo "$p"; fi; done | tail -n1); fi; if [ -n "$SHELL_PID" ]; then readlink /proc/$SHELL_PID/cwd 2>/dev/null || echo "/"; else echo "/"; fi`, portStr)

	out, err := m.executeCmdWithClient(client, cmd)
	if err != nil {
		return "", err
	}
	cwd := strings.TrimSpace(out)
	if cwd == "" || cwd == "/" {
		homeOut, homeErr := m.executeCmdWithClient(client, "echo $HOME")
		if homeErr == nil {
			homeDir := strings.TrimSpace(homeOut)
			if homeDir != "" && homeDir != "/" {
				return homeDir, nil
			}
		}
	}
	if cwd == "" {
		cwd = "/"
	}
	return cwd, nil
}

// getLocalCwdForSession returns the CWD for a local terminal session by
// querying the OS process tree (platform-specific implementation).
func (m *SSHManager) getLocalCwdForSession(s *SessionData) (string, error) {
	if s == nil {
		home, _ := os.UserHomeDir()
		return home, nil
	}
	m.mu.RLock()
	wslDistro := s.WSLDistro
	pid := 0
	if s.Cmd != nil && s.Cmd.Process != nil {
		pid = s.Cmd.Process.Pid
	}
	m.mu.RUnlock()
	return localsftp.CurrentWorkingDirectory(wslDistro, pid)
}

func localSysinfoDependencies() localsysinfo.Dependencies {
	return localsysinfo.Dependencies{
		ProbeScript:      dynamicProbeScript,
		ParseProbe:       parseProbeOutput,
		ParseProcessList: parseFullProcessListOutput,
		ParseStaticInfo:  parseServerStaticInfoOutput,
	}
}

func localSysinfoSession(session *SessionData) localsysinfo.Session {
	if session == nil {
		return localsysinfo.Session{}
	}
	return localsysinfo.Session{WSLDistro: session.WSLDistro}
}

// On Unix/WSL it runs ps; on Windows-native shells it is not yet supported.
func getLocalFullProcessList(s *SessionData) ([]map[string]interface{}, error) {
	return localsysinfo.FullProcessList(localSysinfoSession(s), localSysinfoDependencies())
}

// StartLocalCwdMonitor starts a background polling loop to track the CWD of local sessions
// (WSL and Unix shells) and notify the frontend of updates.
func (m *SSHManager) StartLocalCwdMonitor(sessionId string) {
	go func() {
		ticker := time.NewTicker(1200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.mu.RLock()
				s, ok := m.sessions[sessionId]
				m.mu.RUnlock()
				// Sessions that report CWD via a marker stream (WSL via
				// RemoteHistoryActive, PowerShell via an OSCCwdParser) are driven by
				// pipeLocalOutput instead of this poll loop. Without this guard the
				// poller would overwrite their CurrentCwd with the home-dir fallback.
				// CMD/Unix-local still rely on this poll loop.
				if !ok || !s.IsLocal || s.RemoteHistoryActive || s.OSCCwdParser != nil {
					return
				}
				cwd, err := m.getLocalCwdForSession(s)
				if err != nil {
					continue
				}
				cwd = strings.TrimSpace(cwd)
				if cwd == "" {
					continue
				}
				m.mu.Lock()
				// Re-verify session is still active
				s, ok = m.sessions[sessionId]
				if !ok {
					m.mu.Unlock()
					return
				}
				changed := s.CurrentCwd != cwd
				if changed {
					s.CurrentCwd = cwd
				}
				m.mu.Unlock()
				if changed && m.ctx != nil {
					runtime.EventsEmit(m.ctx, "ssh-terminal-cwd-"+sessionId, cwd)
				}
			}
		}
	}()
}

// WriteBytes sends raw bytes to the SSH PTY stdin (used by WebSocket handler)
func (m *SSHManager) WriteBytes(sessionId string, data []byte) {
	m.mu.Lock()
	s, ok := m.sessions[sessionId]
	var stdin io.WriteCloser
	terminalEncoding := "utf-8"
	if ok && s != nil {
		if s.RemoteHistoryActive && len(data) > 0 {
			s.PromptReady = false
		}
		stdin = s.Stdin
		terminalEncoding = config.NormalizeTerminalEncoding(s.TerminalEncoding)
	}
	m.mu.Unlock()
	if stdin != nil {
		payload := data
		encoded, err := encodeTerminalInputBytes(data, terminalEncoding)
		if err != nil {
			log.Printf("[WriteBytes] encode terminal input failed for %s: %v", sessionId, err)
		} else {
			payload = encoded
		}
		_, _ = stdin.Write(payload)
	}
}

func (m *SSHManager) Resize(sessionId string, cols, rows int) {
	m.mu.RLock()
	s, ok := m.sessions[sessionId]
	m.mu.RUnlock()
	if ok {
		if s.IsLocal {
			m.ResizeLocal(s, cols, rows)
		} else if s.IsSerial {
			// No resize for serial port
		} else if s.Session != nil {
			if err := s.Session.WindowChange(rows, cols); err != nil {
				log.Printf("[Resize] WindowChange failed for %s: %v", sessionId, err)
			}
		}
	}
}

// executeCmdWithClient executes a command on a separate temporary session using the given client
