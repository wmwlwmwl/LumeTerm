package sshmanager

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"lumeterm/internal/config"
	"lumeterm/internal/localsftp"
	"lumeterm/internal/terminalstream"
	"lumeterm/internal/transfer"

	"github.com/pkg/sftp"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	defaultTermRows = 24
	defaultTermCols = 80
)

// ─── 类型别名：引用 config 包类型 ──────────────────────────
type (
	Connection             = config.Connection
	TransferTuningSettings = config.TransferTuningSettings
	PersistedPortForward   = config.PersistedPortForward
)

// ErrHostKeyChanged 在远程主机密钥发生变化时返回，需要用户确认
var ErrHostKeyChanged = errors.New("host key has changed")

// ErrAuthFailed 在 SSH 认证失败时返回。此时连接本身是通的、主机密钥已校验过，
// 用户补上正确密码即可重试，因此不应连带丢弃「只接受本次」的临时密钥授权。
var ErrAuthFailed = errors.New("认证失败")

var sshHostKeyAlgorithms = []string{
	"ssh-ed25519",
	"ecdsa-sha2-nistp256",
	"ecdsa-sha2-nistp384",
	"ecdsa-sha2-nistp521",
	"rsa-sha2-512",
	"rsa-sha2-256",
}

func hostKeyAlgorithmsForConnection(conn Connection) []string {
	algorithms := append([]string(nil), sshHostKeyAlgorithms...)
	if conn.AllowLegacySSHRSA {
		algorithms = append(algorithms, ssh.KeyAlgoRSA)
	}
	return algorithms
}

const (
	postAuthSlowNoticeTimeout = 10 * time.Second
	postAuthChannelTimeout    = 30 * time.Second
	sftpInitWaitTimeout       = 5 * time.Second
	// ponytail: pkg/sftp 无 per-op deadline,SFTP subsystem 慢时会永久阻塞 getSystemInfo,
	// 致前端递归 setTimeout 链断裂(数据不刷新)。用 goroutine+timer 兜底放弃等待,
	// 残留 goroutine 随 keepalive 关连时退出。15s < 命令超时 30s,先暴露部署问题。
	probeDeployTimeout = 15 * time.Second
	// 保活略松：单次超时不立刻拆线，连续失败达阈值才清理共享连接。
	sshKeepaliveInterval = 15 * time.Second
	sshKeepaliveTimeout  = 20 * time.Second
	sshKeepaliveFailMax  = 3
)

// PendingHostKey 保存等待用户确认的主机密钥变更信息
type PendingHostKey struct {
	Conn           Connection
	Hostname       string
	NewKey         ssh.PublicKey
	NewFingerprint string
	OldKeys        []knownhosts.KnownKey
}

// sshClientEntry 保存单个 SSH 连接共享的 client 和 sftp 实例
// 同一服务器的多个终端复用同一 TCP 连接
type sshClientEntry struct {
	Client        *ssh.Client
	NetConn       net.Conn
	SFTP          *sftp.Client
	SFTPReady     chan struct{}
	SFTPReadyOnce sync.Once
	SFTPInitErr   error
}

type SessionData struct {
	ConnKey             string // 共享客户端查找键: user@host:port
	Session             *ssh.Session
	Stdin               io.WriteCloser
	HistoryStream       *terminalstream.CommandHistoryParser
	RemoteHistoryActive bool
	GroupSessionId      string // 对子终端有效：父会话 sessionId（用于历史事件归组）
	ShellPath           string
	TerminalInitPath    string
	TerminalEncoding    string
	CurrentCwd          string
	PromptReady         bool
	// Local terminal & Serial support
	IsLocal         bool
	IsSerial        bool
	LocalPTYWindows any
	LocalPTYUnix    *os.File
	SerialPort      io.ReadWriteCloser
	Cmd             *exec.Cmd
	WSLDistro       string
	LocalSFTPSrv    *localsftp.Server            // embedded SFTP server; non-nil when file manager is available
	OSCCwdParser    *terminalstream.OSCCWDParser // WSL-only: parses ESC]733;<b64>BEL CWD markers from the ConPTY stream
	// Gen is a per-session-instance generation counter incremented each time a
	// local/serial session reuses the same sessionId (fast reconnect). Background
	// goroutines (the serial read loop, the local cmd-waiter, pipeLocalOutput)
	// capture gen at startup and, on teardown, only clean up if the entry still
	// carries the same gen — otherwise a newer instance has replaced it and the
	// old goroutine must leave the map alone to avoid killing the new session.
	Gen uint64
}

type transferBackend struct {
	manager *SSHManager
}

func (b transferBackend) ClientEntry(sessionID string) (*ssh.Client, *sftp.Client, error) {
	return b.manager.GetClientEntry(sessionID)
}

func (b transferBackend) SFTPClient(sessionID string) (*sftp.Client, error) {
	return b.manager.GetSFTPClient(sessionID)
}

func (b transferBackend) ExecuteCommand(ctx context.Context, client *ssh.Client, command string) (string, error) {
	return b.manager.ExecuteCmdWithClientContext(ctx, client, command)
}

func (b transferBackend) DeleteRemote(ctx context.Context, sessionID string, remotePath string, isDir bool) error {
	return b.manager.DeleteItemContext(ctx, sessionID, remotePath, isDir)
}

func (b transferBackend) MkdirRemote(ctx context.Context, sessionID string, remotePath string) error {
	return b.manager.MkdirContext(ctx, sessionID, remotePath)
}

func (b transferBackend) RenameRemote(ctx context.Context, sessionID string, oldPath string, newPath string) error {
	return b.manager.RenameItemContext(ctx, sessionID, oldPath, newPath)
}

func (b transferBackend) UpdateUploadChannels(sessionID string, delta int) {
	b.manager.trackUploadChannelDelta(sessionID, delta)
}

type transferSink struct {
	manager *SSHManager
}

func (s transferSink) Emit(event string, payload any) {
	switch progress := payload.(type) {
	case transfer.DownloadProgress:
		s.manager.transferService.UpdateMCPTransferFromDownloadEvent(
			progress.SessionID,
			progress.DownloadID,
			progress.Mode,
			progress.Phase,
			progress.Status,
			progress.Progress,
			progress.BytesDone,
			progress.BytesTotal,
			progress.Current,
			progress.Detail,
		)
		payload = map[string]interface{}{
			"downloadId": progress.DownloadID,
			"mode":       progress.Mode,
			"phase":      progress.Phase,
			"status":     progress.Status,
			"progress":   progress.Progress,
			"bytesDone":  progress.BytesDone,
			"bytesTotal": progress.BytesTotal,
			"current":    progress.Current,
			"detail":     progress.Detail,
		}
	case transfer.CompressedUploadProgress:
		s.manager.transferService.UpdateMCPTransferFromCompressedUploadEvent(
			progress.SessionID,
			progress.UploadID,
			progress.Phase,
			progress.Progress,
			progress.BytesDone,
			progress.BytesTotal,
			progress.Current,
			progress.Detail,
		)
		payload = map[string]interface{}{
			"uploadId":      progress.UploadID,
			"phase":         progress.Phase,
			"progress":      progress.Progress,
			"phaseProgress": progress.PhaseProgress,
			"bytesDone":     progress.BytesDone,
			"bytesTotal":    progress.BytesTotal,
			"current":       progress.Current,
			"detail":        progress.Detail,
		}
	}
	if s.manager != nil && s.manager.ctx != nil {
		runtime.EventsEmit(s.manager.ctx, event, payload)
	}
}

// SSHAppBackend 抽象 SSHManager 对 App 的依赖（WebSocket 输出 + 缓冲清理）。
type SSHAppBackend interface {
	WriteWsOutput(sessionId string, data []byte)
	CleanupSession(sessionId string)
}

type SSHManager struct {
	ctx              context.Context
	app              SSHAppBackend                 // reference to App for WebSocket output delivery
	configManager    *config.ConfigManager         // 端口转发持久化等配置管理
	sessions         map[string]*SessionData       // terminalId -> terminal session
	clients          map[string]*sshClientEntry    // connKey -> shared client+SFTP
	connTerminals    map[string][]string           // connKey -> terminal sessionIds
	probeDeployed    map[string]bool               // connKey -> probe.sh deployed
	probeFailed      map[string]int                // connKey -> probe.sh deploy fail count (max 3)
	probeRunFailed   map[string]int                // connKey -> probe script run fail count (reset on success)
	remoteFeatures   map[string]map[string]int     // connKey -> 远端能力探测结果: 1 是 / -1 否（busybox/openwrt）
	pendingHostKeys  map[string]*PendingHostKey    // sessionId -> pending host key info
	tempAcceptedKeys map[string]string             // sessionId -> fingerprint (accept this time only)
	pendingCancels   map[string]context.CancelFunc // sessionId -> cancel func for in-progress Connect
	transferService  *transfer.Service
	portForwards     map[string]*managedPortForward
	// recentDisconnects 整机断开(transport/keepalive)现场记录,parentSessionId 为键,
	// 供 MCP reconnect_server 定位并重连;mcpReconnectFailures 记录同一会话连续重连
	// 失败次数,每达阈值提醒用户。均由 mu 保护,详见 ssh_reconnect.go。
	recentDisconnects    map[string]*DisconnectedSessionRecord
	mcpReconnectFailures map[string]int
	mu                   sync.RWMutex
	pendingMu            sync.Mutex
	// connectLocks 按 connKey 串行化 Connect 的拨号流程:防止前端手动/自动重连与
	// MCP reconnect_server 并发对同一服务器各建一条 transport(重复连接泄漏)。
	// connectLocksMu 保护 connectLocks 表本身;表随服务器数量增长,量级很小(数百以内)。
	connectLocks   map[string]*sync.Mutex
	connectLocksMu sync.Mutex
	// reconnectLocks 按 connKey 串行化 MCP ReconnectDisconnectedSession 的完整恢复
	// 序列(解析配置→拨号→重开子终端→清理记录),避免并发 reconnect_server 拿到同一
	// 条断连记录后重复重开子终端。与 connectLocks 相互独立,不会与 Connect 内的锁死锁。
	reconnectLocks   map[string]*sync.Mutex
	reconnectLocksMu sync.Mutex
	bufPool          sync.Pool
	// nextGen is the monotonic source of SessionData.Gen values, used to tell
	// apart two local/serial sessions that reused the same sessionId (fast
	// reconnect). Guarded by mu.
	nextGen uint64
}

// dialAddr 拼接 host:port，自动处理 IPv6 地址
// 如果 host 本身已带 [] 会先去除，避免 net.JoinHostPort 重复包裹
func dialAddr(host string, port int) string {
	host = strings.TrimSpace(host)
	host = strings.Trim(host, "[]")
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// DialAddr 导出包装器
func DialAddr(host string, port int) string { return dialAddr(host, port) }

// ─── 导出包装器：供 package main 通过 ssh_alias.go 调用的工具函数 ──

func ShellQuotePath(path string) string { return shellQuotePath(path) }

func RunCommandWithSessionContext(ctx context.Context, session *ssh.Session, cmd string, timeout time.Duration) (string, error) {
	return runCommandWithSessionContext(ctx, session, cmd, timeout)
}

func EnsureContextActive(ctx context.Context) error { return ensureContextActive(ctx) }

func WriteStringChunksWithContext(ctx context.Context, writer io.Writer, content string) error {
	return writeStringChunksWithContext(ctx, writer, content)
}

const RemoteCmdLongTimeout = remoteCmdLongTimeout

func NewCommandExecutionToken() string { return newCommandExecutionToken() }

func NewSSHManager() *SSHManager {
	manager := &SSHManager{
		sessions:         make(map[string]*SessionData),
		clients:          make(map[string]*sshClientEntry),
		connTerminals:    make(map[string][]string),
		probeDeployed:    make(map[string]bool),
		probeFailed:      make(map[string]int),
		probeRunFailed:   make(map[string]int),
		remoteFeatures:   make(map[string]map[string]int),
		pendingHostKeys:  make(map[string]*PendingHostKey),
		tempAcceptedKeys: make(map[string]string),
		pendingCancels:   make(map[string]context.CancelFunc),
		connectLocks:     make(map[string]*sync.Mutex),
		reconnectLocks:   make(map[string]*sync.Mutex),
		portForwards:     make(map[string]*managedPortForward),
		bufPool: sync.Pool{
			New: func() any {
				buf := make([]byte, 32768)
				return &buf
			},
		},
	}
	manager.transferService = transfer.NewService(transferBackend{manager: manager}, transferSink{manager: manager})
	return manager
}

// SetApp 注入 App 后端（用于 WebSocket 输出和缓冲清理）
func (m *SSHManager) SetApp(app SSHAppBackend) {
	m.app = app
}

// SetConfigManager 注入配置管理器（用于端口转发持久化等）
func (m *SSHManager) SetConfigManager(cm *config.ConfigManager) {
	m.configManager = cm
}

// SetCtx 注入 Wails 上下文（用于事件发射等）
func (m *SSHManager) SetCtx(ctx context.Context) {
	m.ctx = ctx
}

// GetSession 返回指定会话的 SessionData（只读快照，调用方不得修改内部字段）。
func (m *SSHManager) GetSession(sessionId string) (*SessionData, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[sessionId]
	return s, ok
}

// SnapshotSessionsAndSftpAvailability 返回会话表快照和各 connKey 的 SFTP 可用性。
// 用于 mcp_bridge 列举会话描述符，避免外部直接访问 mu/sessions/clients。
func (m *SSHManager) SnapshotSessionsAndSftpAvailability() (map[string]*SessionData, map[string]bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sessions := make(map[string]*SessionData, len(m.sessions))
	for k, v := range m.sessions {
		sessions[k] = v
	}
	sftpAvail := make(map[string]bool, len(m.clients))
	for k, v := range m.clients {
		if v != nil && v.SFTP != nil {
			sftpAvail[k] = true
		}
	}
	return sessions, sftpAvail
}

// LatestTerminalIDsByConnKey 返回每个连接(connKey)上最新打开且仍存活的终端 id。
// connTerminals 按 setupSession/OpenTerminal 顺序追加、移除时保序，末尾即最新；
// 供外部 MCP 的「终端跟随最新」能力标记与重定向使用。
func (m *SSHManager) LatestTerminalIDsByConnKey() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	latest := make(map[string]string, len(m.connTerminals))
	for connKey, terminals := range m.connTerminals {
		for i := len(terminals) - 1; i >= 0; i-- {
			if id := terminals[i]; id != "" && m.sessions[id] != nil {
				latest[connKey] = id
				break
			}
		}
	}
	return latest
}

func isTransientNetError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "forcibly closed") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "EOF") ||
		strings.Contains(s, "i/o timeout") ||
		strings.Contains(s, "wsarecv") ||
		strings.Contains(s, "wsasend") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "unexpected EOF")
}

func (m *SSHManager) runPostAuthStep(ctx context.Context, cancel context.CancelFunc, sessionId string, client *ssh.Client, closeClientOnStop bool, fn func() error) error {
	done := make(chan error, 1)
	// ponytail: goroutine 无 context 感知，ctx 取消后仍在执行 fn()（可能阻塞 I/O）。
	// 当前 fn() 通常为 session.Shell()/session.RequestPty() 等短期操作，超时后由 closeClientOnStop 关闭 client 间接释放。
	// 若未来 fn 涉及长时网络 I/O，需重构为 context-aware。
	go func() {
		done <- fn()
	}()

	noticeTimer := time.NewTimer(postAuthSlowNoticeTimeout)
	defer noticeTimer.Stop()
	timeoutTimer := time.NewTimer(postAuthChannelTimeout)
	defer timeoutTimer.Stop()

	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			if closeClientOnStop && client != nil {
				client.Close()
			}
			return fmt.Errorf("连接已取消")
		case <-noticeTimer.C:
			if m != nil && m.ctx != nil {
				runtime.EventsEmit(m.ctx, "ssh-status", map[string]interface{}{
					"sessionId": sessionId,
					"status":    "post-auth-slow",
					"message":   "SSH 已认证，但打开终端通道响应较慢，服务器可能正在恢复或负载较高。",
				})
			}
		case <-timeoutTimer.C:
			if cancel != nil {
				cancel()
			}
			if closeClientOnStop && client != nil {
				client.Close()
			}
			return fmt.Errorf("SSH 已认证，但打开终端通道超时。服务器可能暂时无法创建终端会话，请稍后重试")
		}
	}
}

func (m *SSHManager) Connect(sessionId string, conn Connection) error {
	// 去除密码首尾空白（防止复制粘贴带入不可见字符）
	conn.Password = strings.TrimSpace(conn.Password)
	conn.TerminalEncoding = config.NormalizeTerminalEncoding(conn.TerminalEncoding)
	// 诊断：密码为空时记录日志，帮助定位"记住密码后重启密码错误"问题
	if conn.AuthMethod == "password" && conn.Password == "" {
		log.Printf("[Connect] WARNING: password is empty for %s@%s:%d (connId=%s)", conn.Username, conn.Host, conn.Port, conn.ID)
	}
	// ponytail: connKey 包含服务器 ID，防止不同服务器条目共享连接
	connKey := conn.ID
	if connKey == "" {
		connKey = fmt.Sprintf("%s@%s", conn.Username, dialAddr(conn.Host, conn.Port))
	}

	// 按 connKey 串行化拨号:同一服务器的并发 Connect(前端自动/手动重连 vs MCP
	// reconnect_server)只会建一条 transport,后到者复用先建好的 client。
	m.connectLocksMu.Lock()
	connLock := m.connectLocks[connKey]
	if connLock == nil {
		connLock = &sync.Mutex{}
		m.connectLocks[connKey] = connLock
	}
	m.connectLocksMu.Unlock()
	connLock.Lock()
	defer connLock.Unlock()

	m.mu.RLock()
	existingEntry, clientExists := m.clients[connKey]
	m.mu.RUnlock()

	var client *ssh.Client
	var transportConn net.Conn
	clientCreated := false

	if clientExists {
		client = existingEntry.Client
	} else {
		// Setup auth methods
		// keyboard-interactive 优先，因为部分服务器不提供 password 方法
		var authMethods []ssh.AuthMethod
		if conn.AuthMethod == "password" {
			authMethods = append(authMethods, ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) (answers []string, err error) {
				answers = make([]string, len(questions))
				for i := range answers {
					answers[i] = conn.Password
				}
				return answers, nil
			}))
			authMethods = append(authMethods, ssh.Password(conn.Password))
		} else if conn.AuthMethod == "privateKey" {
			var signer ssh.Signer
			var err error
			if conn.Passphrase != "" {
				signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(conn.PrivateKey), []byte(conn.Passphrase))
			} else {
				signer, err = ssh.ParsePrivateKey([]byte(conn.PrivateKey))
			}
			if err != nil {
				return fmt.Errorf("invalid private key: %w", err)
			}
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}

		hostKeyCallback, err := initKnownHostsCallback()
		if err != nil {
			return err
		}

		customHostKeyCallback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			err := hostKeyCallback(hostname, remote, key)
			if err == nil {
				return nil
			}

			var keyErr *knownhosts.KeyError
			if !errors.As(err, &keyErr) {
				return err
			}

			fingerprint := ssh.FingerprintSHA256(key)
			// ponytail: 临时密钥检查统一放在分支前
			m.mu.RLock()
			if fp, ok := m.tempAcceptedKeys[sessionId]; ok && fp == fingerprint {
				m.mu.RUnlock()
				return nil
			}
			m.mu.RUnlock()

			m.mu.Lock()
			m.pendingHostKeys[sessionId] = &PendingHostKey{
				Conn:           conn,
				Hostname:       hostname,
				NewKey:         key,
				NewFingerprint: fingerprint,
				OldKeys:        keyErr.Want, // nil when first connection
			}
			m.mu.Unlock()
			return ErrHostKeyChanged
		}

		sshConfig := &ssh.ClientConfig{
			User:              conn.Username,
			Auth:              authMethods,
			HostKeyCallback:   customHostKeyCallback,
			Timeout:           10 * time.Second,
			HostKeyAlgorithms: hostKeyAlgorithmsForConnection(conn),
		}

		target := dialAddr(conn.Host, conn.Port)

		// 创建可取消 context，支持 Disconnect 中断正在进行的连接
		// 派生自 m.ctx（若存在），确保应用关闭时所有进行中的握手也能被取消
		parent := context.Background()
		if m.ctx != nil {
			parent = m.ctx
		}
		cancelCtx, cancelConnect := context.WithCancel(parent)
		m.pendingMu.Lock()
		m.pendingCancels[sessionId] = cancelConnect
		m.pendingMu.Unlock()
		defer func() {
			m.pendingMu.Lock()
			delete(m.pendingCancels, sessionId)
			m.pendingMu.Unlock()
		}()

		// ponytail: 瞬态网络错误自动重试最多2次
		const maxRetries = 2
		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				time.Sleep(time.Duration(attempt) * time.Second)
				log.Printf("[Connect] 瞬态网络错误重试 %d/%d: %s", attempt, maxRetries, conn.Host)
			}

			netConn, dialErr := config.DialConnectionTargetContext(cancelCtx, conn, target, sshConfig.Timeout)
			if dialErr != nil {
				if errors.Is(dialErr, context.Canceled) || cancelCtx.Err() != nil {
					return fmt.Errorf("连接已取消")
				}
				errStr := dialErr.Error()
				if strings.Contains(errStr, "connection refused") {
					if m.ctx != nil {
						runtime.EventsEmit(m.ctx, "ssh-connection-failed", map[string]interface{}{
							"sessionId": sessionId,
							"connId":    conn.ID,
							"host":      conn.Host,
							"port":      conn.Port,
							"username":  conn.Username,
							"error":     errStr,
						})
					}
					return fmt.Errorf("连接被拒绝")
				}
				if attempt < maxRetries && isTransientNetError(dialErr) {
					continue
				}
				return dialErr
			}

			if cancelCtx.Err() != nil {
				netConn.Close()
				return fmt.Errorf("连接已取消")
			}

			handshakeDone := make(chan struct{})
			go func() {
				select {
				case <-cancelCtx.Done():
					netConn.Close()
				case <-handshakeDone:
				}
			}()

			sshConn, chans, reqs, handshakeErr := ssh.NewClientConn(netConn, target, sshConfig)
			close(handshakeDone)

			if handshakeErr != nil {
				if cancelCtx.Err() != nil {
					netConn.Close()
					return fmt.Errorf("连接已取消")
				}
				if errors.Is(handshakeErr, ErrHostKeyChanged) {
					netConn.Close()
					if m.ctx != nil {
						m.mu.RLock()
						pending, ok := m.pendingHostKeys[sessionId]
						if !ok || pending == nil {
							m.mu.RUnlock()
							return fmt.Errorf("主机密钥已变更，但未找到待确认信息")
						}
						hostname := pending.Hostname
						newFingerprint := pending.NewFingerprint
						oldFingerprints := make([]string, 0, len(pending.OldKeys))
						for _, k := range pending.OldKeys {
							oldFingerprints = append(oldFingerprints, ssh.FingerprintSHA256(k.Key))
						}
						isNew := len(pending.OldKeys) == 0
						m.mu.RUnlock()
						runtime.EventsEmit(m.ctx, "ssh-host-key-changed", map[string]interface{}{
							"sessionId":       sessionId,
							"hostname":        hostname,
							"host":            conn.Host,
							"port":            conn.Port,
							"newFingerprint":  newFingerprint,
							"oldFingerprints": oldFingerprints,
							"isNew":           isNew,
						})
					}
					return fmt.Errorf("主机密钥已变更，请在弹窗中确认")
				}

				errStr := handshakeErr.Error()
				if strings.Contains(errStr, "unable to authenticate") ||
					strings.Contains(errStr, "no supported methods remain") {
					if m.ctx != nil {
						runtime.EventsEmit(m.ctx, "ssh-auth-failed", map[string]interface{}{
							"sessionId": sessionId,
							"connId":    conn.ID,
							"host":      conn.Host,
							"port":      conn.Port,
							"username":  conn.Username,
							"error":     errStr,
						})
					}
					return ErrAuthFailed
				}

				// 瞬态错误关闭连接后重试
				if attempt < maxRetries && isTransientNetError(handshakeErr) {
					netConn.Close()
					continue
				}
				netConn.Close()
				return handshakeErr
			}

			// 握手成功
			client = ssh.NewClient(sshConn, chans, reqs)
			transportConn = netConn
			clientCreated = true
			break
		}

		// 重新检查 connKey 是否已被并发 Connect 写入；若是则丢弃新连接，复用已有连接
		m.mu.Lock()
		if existing, ok := m.clients[connKey]; ok && existing.Client != nil {
			m.mu.Unlock()
			// 关闭刚刚新建的连接，改用已存在的连接
			transportConn.Close()
			client.Close()
			client = existing.Client
			transportConn = existing.NetConn
			clientCreated = false
		} else {
			m.clients[connKey] = &sshClientEntry{Client: client, NetConn: transportConn, SFTPReady: make(chan struct{})}
			m.connTerminals[connKey] = []string{}
			m.mu.Unlock()

			go m.watchClient(connKey, client)
			go func() {
				waitErr := client.Wait()
				log.Printf("[disconnect] 共享 transport client.Wait 返回 connKey=%s err=%v（触发 cleanupClientTransport(reason=transport)）", connKey, waitErr)
				m.cleanupClientTransport(connKey, client, "transport")
			}()
		}
	}

	parent := context.Background()
	if m.ctx != nil {
		parent = m.ctx
	}
	postAuthCtx, cancelPostAuth := context.WithCancel(parent)
	m.pendingMu.Lock()
	m.pendingCancels[sessionId] = cancelPostAuth
	m.pendingMu.Unlock()

	var shellPath string
	err := m.runPostAuthStep(postAuthCtx, cancelPostAuth, sessionId, client, clientCreated, func() error {
		shellPath = detectRemoteShell(client)
		launchCmd, remoteHistoryActive := buildShellLaunchCommand(shellPath, conn.TerminalInitPath)
		if err := m.setupSession(postAuthCtx, client, connKey, sessionId, "", launchCmd, remoteHistoryActive, shellPath, conn.TerminalInitPath, conn.TerminalEncoding); err != nil {
			return err
		}
		return m.waitForCommandChannel(postAuthCtx, client)
	})
	if err != nil {
		// setupSession 失败（如 PTY 请求失败）：仅清理本路径创建的 session；
		// 新建的 client 已被并发复用时不能关闭，否则会级联断开其他终端。
		m.mu.Lock()
		if sd, ok := m.sessions[sessionId]; ok {
			if sd.Stdin != nil {
				sd.Stdin.Close()
			}
			if sd.Session != nil {
				sd.Session.Close()
			}
			delete(m.sessions, sessionId)
		}
		if terminals, ok := m.connTerminals[connKey]; ok {
			next := terminals[:0]
			for _, tid := range terminals {
				if tid != sessionId {
					next = append(next, tid)
				}
			}
			m.connTerminals[connKey] = next
		}
		closeClient := false
		if clientCreated {
			if entry, ok := m.clients[connKey]; ok && entry.Client == client && len(m.connTerminals[connKey]) == 0 {
				if entry.SFTPReady != nil {
					entry.SFTPReadyOnce.Do(func() { close(entry.SFTPReady) })
				}
				delete(m.clients, connKey)
				delete(m.connTerminals, connKey)
				closeClient = true
			}
		}
		m.mu.Unlock()
		if closeClient {
			_ = transportConn.Close()
			_ = client.Close()
		}
		return err
	}
	if clientCreated {
		go m.initSFTPClient(sessionId, connKey, conn, client)
	}
	if m.ctx != nil {
		runtime.EventsEmit(m.ctx, "ssh-command-ready", map[string]interface{}{
			"sessionId": sessionId,
		})
	}
	return nil
}

// setupSession 创建 shell session 的共享逻辑
func (m *SSHManager) setupSession(ctx context.Context, client *ssh.Client, connKey, sessionId, groupSessionId, launchCmd string, remoteHistoryActive bool, shellPath string, terminalInitPath string, terminalEncoding string) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	if ctx != nil && ctx.Err() != nil {
		session.Close()
		return ctx.Err()
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 115200,
		ssh.TTY_OP_OSPEED: 115200,
	}

	if err := session.RequestPty("xterm-256color", defaultTermRows, defaultTermCols, modes); err != nil {
		session.Close()
		return err
	}
	if ctx != nil && ctx.Err() != nil {
		session.Close()
		return ctx.Err()
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		return err
	}

	if launchCmd != "" {
		err = session.Start(launchCmd)
	} else {
		err = session.Shell()
	}
	if err != nil {
		session.Close()
		return err
	}
	if ctx != nil && ctx.Err() != nil {
		session.Close()
		return ctx.Err()
	}

	var historyStream *terminalstream.CommandHistoryParser
	if remoteHistoryActive {
		encoding := config.NormalizeTerminalEncoding(terminalEncoding)
		historyStream = terminalstream.NewCommandHistoryParser(func(data []byte) string {
			return decodeTerminalText(data, encoding)
		})
	}

	m.mu.Lock()
	if ctx != nil && ctx.Err() != nil {
		m.mu.Unlock()
		session.Close()
		return ctx.Err()
	}
	entry, ok := m.clients[connKey]
	if !ok || entry.Client != client {
		m.mu.Unlock()
		session.Close()
		return fmt.Errorf("SSH 连接已关闭")
	}
	sd := &SessionData{
		ConnKey:             connKey,
		Session:             session,
		Stdin:               stdin,
		HistoryStream:       historyStream,
		RemoteHistoryActive: remoteHistoryActive,
		ShellPath:           strings.TrimSpace(shellPath),
		TerminalInitPath:    strings.TrimSpace(terminalInitPath),
		TerminalEncoding:    config.NormalizeTerminalEncoding(terminalEncoding),
		PromptReady:         !remoteHistoryActive,
	}
	if groupSessionId != "" {
		sd.GroupSessionId = groupSessionId
	}
	// ponytail: 登记幂等化。工作区恢复与用户手动进入并发时，同一 sessionId
	// 可能被两次 setupSession 登记：旧条目必须释放（其 Wait goroutine 因
	// expected 不匹配不会再清理），且 connTerminals 中残留的旧 id 要去重，
	// 否则通道占用虚高且关闭后残留。
	var staleStdin io.WriteCloser
	var staleSession *ssh.Session
	if stale, ok := m.sessions[sessionId]; ok {
		staleStdin = stale.Stdin
		staleSession = stale.Session
		// 防御性：旧条目挂在另一个 connKey 上（同 id 跨连接重登记，正常 id
		// 含时间戳不会发生）时，把旧 key 里的 id 也摘掉，避免旧连接计数残留 +1。
		if stale.ConnKey != "" && stale.ConnKey != connKey {
			if old := m.connTerminals[stale.ConnKey]; len(old) > 0 {
				next := old[:0]
				for _, tid := range old {
					if tid != sessionId {
						next = append(next, tid)
					}
				}
				m.connTerminals[stale.ConnKey] = next
			}
		}
		next := m.connTerminals[connKey][:0]
		for _, tid := range m.connTerminals[connKey] {
			if tid != sessionId {
				next = append(next, tid)
			}
		}
		m.connTerminals[connKey] = next
	}
	m.sessions[sessionId] = sd
	m.connTerminals[connKey] = append(m.connTerminals[connKey], sessionId)
	m.mu.Unlock()
	if staleSession != nil {
		if staleStdin != nil {
			_ = staleStdin.Close()
		}
		_ = staleSession.Close()
	}
	m.emitSSHChannelUsage(connKey)

	go m.pipeOutput(sessionId, stdout, historyStream)
	go m.pipeOutput(sessionId, stderr, nil)
	go func(expected *ssh.Session) {
		waitErr := expected.Wait()
		log.Printf("[disconnect] session.Wait 返回 sessionId=%s err=%v（shell 结束，触发 session_end）", sessionId, waitErr)
		m.disconnectAndNotify(sessionId, expected, "session_end")
	}(session)

	return nil
}

func (m *SSHManager) ApplyTransferTuning(settings TransferTuningSettings) {
	m.transferService.SetTuning(transfer.Tuning{
		MaxPacketKiB:        settings.MaxPacketKiB,
		MaxRequestsPerFile:  settings.MaxRequestsPerFile,
		ConcurrentWrites:    settings.ConcurrentWrites,
		ApplyToSharedClient: settings.ApplyToSharedClient,
		Configured:          settings.Configured,
	})
}

func (m *SSHManager) newSharedSFTPClient(client *ssh.Client) (*sftp.Client, error) {
	if m.transferService.Tuning().ApplyToSharedClient {
		// 共享 client 读方向固定 32KB 互操作安全长度,避免部分服务器对超长
		// READ 返回短读/提前 EOF 造成下载静默截断(issue #334)。
		return m.transferService.NewSharedSFTPClient(client)
	}
	return sftp.NewClient(client)
}

func (m *SSHManager) initSFTPClient(sessionId string, connKey string, conn Connection, client *ssh.Client) {
	sftpClient, err := m.newSharedSFTPClient(client)
	m.mu.Lock()
	entry, ok := m.clients[connKey]
	if !ok || entry.Client != client {
		m.mu.Unlock()
		if sftpClient != nil {
			sftpClient.Close()
		}
		return
	}
	entry.SFTP = sftpClient
	entry.SFTPInitErr = err
	if entry.SFTPReady != nil {
		entry.SFTPReadyOnce.Do(func() { close(entry.SFTPReady) })
	}
	m.mu.Unlock()
	m.emitSSHChannelUsage(connKey)
	if err == nil {
		go m.probeSSHMaxSessions(connKey)
	}

	if err != nil && m.ctx != nil {
		event := map[string]interface{}{
			"sessionId": sessionId,
			"status":    "sftp-unavailable",
			"host":      conn.Host,
			"port":      conn.Port,
			"username":  conn.Username,
			"error":     err.Error(),
		}
		// OpenWrt/Dropbear 缺省无 SFTP 子系统,附上可复制的安装命令
		if m.remoteFeatureIs(client, connKey, featureOpenWrt) {
			event["openwrt"] = true
			event["installCmd"] = sftpInstallCmd
		}
		runtime.EventsEmit(m.ctx, "ssh-status", event)
	}
}

// sftpInstallCmd OpenWrt 上安装 SFTP 子系统的命令(Dropbear 需 openssh-sftp-server)。
const sftpInstallCmd = "opkg update && opkg install openssh-sftp-server"

func (m *SSHManager) disconnectAndNotify(sessionId string, expectedSession *ssh.Session, reason string) {
	if reason == "" {
		reason = "session_end"
	}
	m.mu.RLock()
	expected := m.sessions[sessionId]
	if expected == nil || (expectedSession != nil && expected.Session != expectedSession) {
		m.mu.RUnlock()
		return
	}
	parentSessionId := sessionId
	if expected.GroupSessionId != "" {
		parentSessionId = expected.GroupSessionId
	}
	connKey := expected.ConnKey
	terminalsBefore := len(m.connTerminals[connKey])
	m.mu.RUnlock()

	if !m.disconnect(sessionId, expected) {
		return
	}
	if m.ctx == nil {
		return
	}

	connectionClosed := false
	if connKey != "" {
		m.mu.RLock()
		_, clientAlive := m.clients[connKey]
		terminalsAfter := len(m.connTerminals[connKey])
		m.mu.RUnlock()
		// 断开前是该连接上最后一个终端，或 client 已不在
		connectionClosed = !clientAlive || (terminalsBefore > 0 && terminalsAfter == 0)
	}

	runtime.EventsEmit(m.ctx, "ssh-disconnected", map[string]interface{}{
		"sessionId":        sessionId,
		"parentSessionId":  parentSessionId,
		"terminalIds":      []string{sessionId},
		"reason":           reason,
		"connectionClosed": connectionClosed,
	})
}

// disconnectCurrentGen tears down the session for sessionId, but only if the
// entry currently in the map is still the same generation (gen) the caller
// started with. Local/serial sessions reuse the same sessionId on fast
// reconnect, so a stale background goroutine (e.g. the previous serial read
// loop) would otherwise find the *new* session under that id and kill it.
// If a newer instance has taken over, this is a no-op.
//
// 事件载荷与 disconnectAndNotify 对齐（对象而非纯 string）：本地/串口单会话，
// connectionClosed 恒 true；reason=session_end，避免前端把 string 兼容路径
// 当成 transport 误报「SSH 连接已意外断开」。
func (m *SSHManager) disconnectCurrentGen(sessionId string, gen uint64) {
	m.mu.RLock()
	cur, ok := m.sessions[sessionId]
	if !ok || cur.Gen != gen {
		m.mu.RUnlock()
		return
	}
	parentSessionId := sessionId
	if cur.GroupSessionId != "" {
		parentSessionId = cur.GroupSessionId
	}
	m.mu.RUnlock()

	if !m.Disconnect(sessionId) || m.ctx == nil {
		return
	}
	runtime.EventsEmit(m.ctx, "ssh-disconnected", map[string]interface{}{
		"sessionId":        sessionId,
		"parentSessionId":  parentSessionId,
		"terminalIds":      []string{sessionId},
		"reason":           "session_end",
		"connectionClosed": true,
	})
}

func (m *SSHManager) GetClientEntry(sessionId string) (*ssh.Client, *sftp.Client, error) {
	m.mu.RLock()
	s, ok := m.sessions[sessionId]
	if !ok {
		m.mu.RUnlock()
		return nil, nil, fmt.Errorf("session not found")
	}
	entry, ok := m.clients[s.ConnKey]
	m.mu.RUnlock()
	if !ok {
		return nil, nil, fmt.Errorf("client not found for session")
	}
	return entry.Client, entry.SFTP, nil
}

// getSFTPClient 查找 session 对应的 SFTP 客户端；初始化中时短暂等待。
func (m *SSHManager) GetSFTPClient(sessionId string) (*sftp.Client, error) {
	m.mu.RLock()
	s, ok := m.sessions[sessionId]
	if !ok {
		m.mu.RUnlock()
		return nil, fmt.Errorf("session not found")
	}
	entry, ok := m.clients[s.ConnKey]
	if !ok {
		m.mu.RUnlock()
		return nil, fmt.Errorf("client not found for session")
	}
	if entry.SFTP != nil {
		sftpClient := entry.SFTP
		m.mu.RUnlock()
		return sftpClient, nil
	}
	ready := entry.SFTPReady
	m.mu.RUnlock()

	if ready == nil {
		return nil, fmt.Errorf("SFTP not available")
	}
	select {
	case <-ready:
	case <-time.After(sftpInitWaitTimeout):
		return nil, fmt.Errorf("SFTP initialization timed out")
	}

	// 等待后的二次读取同样必须在 RLock 内:等待超时路径与 initSFTPClient
	// 对 entry.SFTP 的写入并发时,锁外读属于数据竞态(-race 检出)。
	m.mu.RLock()
	entry, ok = m.clients[s.ConnKey]
	var initErr error
	var sftpClient *sftp.Client
	var client *ssh.Client
	if ok {
		initErr = entry.SFTPInitErr
		sftpClient = entry.SFTP
		client = entry.Client
	}
	connKey := s.ConnKey
	m.mu.RUnlock()

	if !ok || sftpClient == nil {
		if initErr != nil {
			// OpenWrt/Dropbear 缺省无 SFTP 子系统,给出可执行的安装提示;
			// 探测在锁外进行,失败时按非 OpenWrt 处理,保持原错误透传。
			if m.remoteFeatureIs(client, connKey, featureOpenWrt) {
				return nil, fmt.Errorf("OpenWrt device detected: file manager requires the SFTP subsystem. Install with: %s (original error: %w)", sftpInstallCmd, initErr)
			}
			return nil, fmt.Errorf("SFTP not available: %w", initErr)
		}
		return nil, fmt.Errorf("SFTP not available")
	}
	return sftpClient, nil
}

// DisconnectConnection 关闭 sessionId 所属共享连接的全部终端。
// terminalIds 由前端提供（会话已知的全部终端 id），用于兜底：
// 根终端已关闭（如根标签被单独关闭、根 shell 自然退出）而子终端仍存活时，
// m.sessions[sessionId] 已不存在，仅凭根 id 无法定位连接，会整体 no-op，
// 导致子终端与共享 client 永久泄漏。逐个断开传入 id 使会话级关闭不依赖根终端存活。
func (m *SSHManager) DisconnectConnection(sessionId string, terminalIds []string) {
	m.mu.RLock()
	session := m.sessions[sessionId]
	if session == nil || session.ConnKey == "" {
		m.mu.RUnlock()
		// 会话尚未登记：Connect 正在进行 dial/握手/认证（如等待密码输入），
		// 此时仅凭 m.sessions 收集 targets 会整体 no-op，在途 Connect 完成后
		// 该 session 与共享 client 永久泄漏（通道占用虚高）。Disconnect 的
		// expected=nil 会取消 pendingCancels[sessionId]，使 Connect 在检查点
		// 提前退出、不再登记。terminalIds 里的子终端仍逐个兜底清理。
		m.Disconnect(sessionId)
		for _, id := range terminalIds {
			if id != "" && id != sessionId {
				m.Disconnect(id)
			}
		}
		return
	}
	ids := append([]string{sessionId}, m.connTerminals[session.ConnKey]...)
	for _, id := range terminalIds {
		if id != "" {
			ids = append(ids, id)
		}
	}
	targets := make(map[string]*SessionData, len(ids))
	for _, id := range ids {
		if current := m.sessions[id]; current != nil {
			targets[id] = current
		}
	}
	m.mu.RUnlock()
	for id, expected := range targets {
		m.disconnect(id, expected)
	}
}

func (m *SSHManager) Disconnect(sessionId string) bool {
	// 主动断开(用户关闭/换密码重连等)时清除断连记录与连续失败计数,
	// 避免 MCP reconnect_server 之后误重连一个用户已放弃的会话。
	m.clearDisconnectedRecordsForSession(sessionId)
	return m.disconnect(sessionId, nil)
}

func (m *SSHManager) disconnect(sessionId string, expected *SessionData) bool {
	disconnected := false
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Disconnect] panic recovered: %v\n%s", r, debug.Stack())
		}
	}()

	// 先取消正在进行的连接（Connect 还没完成的情况）。迟到的旧 Session.Wait
	// 带 expected 身份，不得取消同 ID 的新连接。
	if expected == nil {
		m.pendingMu.Lock()
		if cancel, ok := m.pendingCancels[sessionId]; ok {
			cancel()
			delete(m.pendingCancels, sessionId)
		}
		m.pendingMu.Unlock()
	}

	// 1. 在锁内完成 map 清理，收集需要关闭的资源
	m.mu.Lock()
	s, ok := m.sessions[sessionId]
	if expected != nil && (!ok || s != expected) {
		m.mu.Unlock()
		return false
	}
	delete(m.tempAcceptedKeys, sessionId)
	delete(m.pendingHostKeys, sessionId)
	if !ok {
		m.mu.Unlock()
		return false
	}
	disconnected = true
	connKey := s.ConnKey
	delete(m.sessions, sessionId)
	isLocal := s.IsLocal
	isSerial := s.IsSerial

	// 收集需要关闭的资源（避免在锁内执行可能阻塞的 Close 操作）
	stdin := s.Stdin
	sshSess := s.Session

	// 从 connTerminals 中移除（去掉全部同 id 条目：并发重入可能残留重复登记）
	terminals := m.connTerminals[connKey]
	next := terminals[:0]
	for _, t := range terminals {
		if t != sessionId {
			next = append(next, t)
		}
	}
	m.connTerminals[connKey] = next
	defer m.emitSSHChannelUsage(connKey)

	var netConnToClose net.Conn
	var sftpToClose *sftp.Client
	var clientToClose *ssh.Client
	stopForwardsConnKey := ""

	if len(m.connTerminals[connKey]) == 0 {
		if entry, ok := m.clients[connKey]; ok {
			netConnToClose = entry.NetConn
			sftpToClose = entry.SFTP
			clientToClose = entry.Client
			if entry.SFTPReady != nil {
				entry.SFTPReadyOnce.Do(func() { close(entry.SFTPReady) })
			}
			delete(m.clients, connKey)
		}
		delete(m.connTerminals, connKey)
		delete(m.probeDeployed, connKey)
		delete(m.probeFailed, connKey)
		delete(m.probeRunFailed, connKey)
		delete(m.remoteFeatures, connKey)
		// 即使 transport 清理已先删掉 client，也要回收连接索引和转发。
		// stopPortForwardsForConnKey 幂等，和 cleanupClientTransport 重复调用无害。
		stopForwardsConnKey = connKey
	}
	m.mu.Unlock() // 尽早释放锁，避免 Close 阻塞影响其他操作
	// 只有成功摘除目标 session 后才取消其传输；旧 Wait 不能误取消重连后的任务。
	m.transferService.DisconnectSession(sessionId)
	// 最后一个终端先关闭底层 transport，解除半死连接上的 SFTP、远程转发
	// cancel 请求等阻塞；共享连接还有其它终端时 netConnToClose 为 nil。
	if netConnToClose != nil {
		_ = netConnToClose.Close()
	}
	if stopForwardsConnKey != "" {
		m.stopPortForwardsForConnKey(stopForwardsConnKey)
	}
	// 会话彻底销毁：同步清理 WebSocket、外部编辑等旁路资源。
	if m.app != nil {
		m.app.CleanupSession(sessionId)
	}

	// 2. 在锁外关闭资源（服务器挂了时这些操作可能阻塞，但不会锁住其他 goroutine）
	if isLocal {
		// Close the embedded SFTP server and remove its client entry from the map.
		// The sshClientEntry's SFTP client and underlying ssh.Client were dialed
		// into the in-process server; LocalSFTPSrv.Close only stops the listener,
		// so we must also close them or the per-session TCP conn + goroutines leak.
		if s.ConnKey != "" {
			m.mu.Lock()
			localEntry, entryOk := m.clients[s.ConnKey]
			if entryOk {
				delete(m.clients, s.ConnKey)
			}
			m.mu.Unlock()
			if entryOk {
				if localEntry.SFTP != nil {
					closeWithTimeout(localEntry.SFTP, 3*time.Second)
				}
				if localEntry.Client != nil {
					closeWithTimeout(localEntry.Client, 3*time.Second)
				}
			}
		}
		if s.LocalSFTPSrv != nil {
			_ = s.LocalSFTPSrv.Close()
		}
		m.CloseLocal(s)
	} else if isSerial {
		if s.SerialPort != nil {
			_ = s.SerialPort.Close()
			s.SerialPort = nil
		}
	}

	if stdin != nil && !isLocal && !isSerial {
		stdin.Close()
	}
	if sshSess != nil {
		sshSess.Close()
	}
	if sftpToClose != nil {
		closeWithTimeout(sftpToClose, 3*time.Second)
	}
	if clientToClose != nil {
		closeWithTimeout(clientToClose, 3*time.Second)
	}
	m.closeSessionOutputTaps(sessionId)
	return disconnected
}

// closeWithTimeout 关闭资源，最多等待 timeout，超时放弃避免半死服务端卡住调用方
// ponytail: 超时后底层 goroutine 仍在 Close 上阻塞，等连接真正断开或进程退出才回收；
// SSH client 无 CloseWithDeadline，这是唯一能保证调用方不卡死的轻量手段
func closeWithTimeout(c io.Closer, timeout time.Duration) {
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// DisconnectAll 断开所有 SSH 连接，用于应用退出时清理资源
func (m *SSHManager) DisconnectAll() {
	// 先取消所有正在进行的连接
	m.pendingMu.Lock()
	for id, cancel := range m.pendingCancels {
		cancel()
		delete(m.pendingCancels, id)
	}
	m.pendingMu.Unlock()

	m.mu.RLock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		m.Disconnect(id)
	}
	m.transferService.Close()
	// ponytail: 清理输出监听注册表，防止长期运行的桌面应用累积内存
	sshOutputTapRegistry.Delete(m)
}

// OpenTerminal 为已有连接创建新的终端通道
// 复用同一个 SSH 客户端，创建新的 shell session
func (m *SSHManager) OpenTerminal(sessionId string) (string, error) {
	m.mu.RLock()
	existing, ok := m.sessions[sessionId]
	if !ok {
		m.mu.RUnlock()
		return "", fmt.Errorf("session not found")
	}
	entry, ok := m.clients[existing.ConnKey]
	if !ok {
		m.mu.RUnlock()
		return "", fmt.Errorf("client not found for session")
	}
	connKey := existing.ConnKey
	terminalEncoding := existing.TerminalEncoding
	m.mu.RUnlock()

	// 生成新 session ID
	randomId := make([]byte, 8)
	if _, err := rand.Read(randomId); err != nil {
		return "", fmt.Errorf("生成 session ID 失败: %w", err)
	}
	newId := fmt.Sprintf("term_%x", randomId)
	launchCmd, remoteHistoryActive := buildShellLaunchCommand(existing.ShellPath, existing.TerminalInitPath)

	err := m.setupSession(context.Background(), entry.Client, connKey, newId, sessionId, launchCmd, remoteHistoryActive, existing.ShellPath, existing.TerminalInitPath, terminalEncoding)
	if err != nil {
		return "", err
	}

	return newId, nil
}

func (m *SSHManager) executeCmdWithClient(client *ssh.Client, cmd string) (string, error) {
	return m.ExecuteCmdWithClientContext(context.Background(), client, cmd)
}

func (m *SSHManager) waitForCommandChannel(ctx context.Context, client *ssh.Client) error {
	if ctx == nil {
		ctx = context.Background()
	}
	readyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var lastErr error
	for {
		_, err := m.ExecuteCmdWithClientContext(readyCtx, client, "true")
		if err == nil {
			return nil
		}
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		lastErr = err
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-readyCtx.Done():
			timer.Stop()
			if lastErr != nil {
				return fmt.Errorf("SSH 命令通道未就绪: %w", lastErr)
			}
			return readyCtx.Err()
		case <-timer.C:
		}
	}
}

func (m *SSHManager) ExecuteCmdWithClientContext(ctx context.Context, client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	return runCommandWithSessionContext(ctx, session, cmd, 30*time.Second)
}

// runCommandWithSession 在 session 上执行命令，带超时控制
func runCommandWithSession(session *ssh.Session, cmd string, timeout time.Duration) (string, error) {
	return runCommandWithSessionContext(context.Background(), session, cmd, timeout)
}

func runCommandWithSessionContext(ctx context.Context, session *ssh.Session, cmd string, timeout time.Duration) (string, error) {
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				errCh <- fmt.Errorf("panic in session.Run: %v", r)
			}
		}()
		errCh <- session.Run(cmd)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var ctxDone <-chan struct{}
	if ctx != nil {
		ctxDone = ctx.Done()
	}

	select {
	case err := <-errCh:
		stdout := stdoutBuf.String()
		stderr := strings.TrimSpace(stderrBuf.String())
		if err != nil && stderr != "" {
			return stdout, fmt.Errorf("%w: %s", err, stderr)
		}
		return stdout, err
	case <-ctxDone:
		go session.Close()
		return "", ctx.Err()
	case <-timer.C:
		go session.Close()
		return "", fmt.Errorf("command timed out after %v", timeout)
	}
}

const dynamicProbeScript = `#!/bin/sh
# LumeTerm Dynamic Probe - auto generated
# Collects dynamic metrics via /proc

# ── 进程双采样 + 远端选 top6(只传 6 条,流量与 1.2.7 ps|head -6 持平)──
# sample_procs 逐进程 read /proc/[pid]/stat(不逐 PID fork),输出 6 字段
# (pid comm utime stime starttime rss,\x1f 分隔)。供 pass1 落盘与 pass2 流式输入。
# sample_procs_select 用 awk 按 pid 配对双采样,算 CPU delta,按 delta 降序取前 6。
# emit_procs_pass 把选出的 6 条转成 Go 端 parseProcStatSample 的 6 字段格式,
# 分 PROC1(pass1)/PROC2(pass2)两段输出——Go 端 parseProbeProcSections 照旧
# 算 delta 并校验 starttime(PID 复用);远端只负责「选哪 6 个」,不越权算 cpu%。
sample_procs() {
  IFS= read -r selfstat < /proc/self/stat
  selfpid=${selfstat%% *}
  for f in /proc/[0-9]*/stat; do
    [ -r "$f" ] || continue
    IFS= read -r s < "$f" || continue
    pid=${s%% *}
    [ "$pid" = "$selfpid" ] || [ "$pid" = "$$" ] && continue
    comm=${s#*\(}; comm=${comm%)*}
    rest=${s##*)}
    set -- $rest
    # $1=state $12=utime $13=stime $20=starttime $22=rss (1-based in rest)
    # ${12} 而非 $12: POSIX sh 中 $12 等价于 ${1}2,必须用 ${} 引用两位数参数
    printf '%s\037%s\037%s\037%s\037%s\037%s\n' "$pid" "$comm" "${12}" "${13}" "${20}" "${22}"
  done
}

# sample_procs_select: pass1 文件($1)载入 awk 关联数组,pass2(stdin)逐条按
# pid 配对,算 CPU delta,按 delta 降序取前 6。
# 出参(stdout): 至多 6 行,每行 10 个 \x1f 字段:
#   delta pid comm ut1 st1 start1 ut2 st2 start2 rss2
# 仅双侧均有的 pid 配对(等价丢弃单侧样本,即采样窗口内创建/退出);
# starttime 不一致(PID 复用)剔除。
# ponytail: 配对用 BusyBox awk(OpenWrt 默认编入,BUSYBOX_DEFAULT_AWK=y),
# 不得依赖 join——OpenWrt 官方 BusyBox 配置树(main/24.10/23.05)不含 join
# applet,stock OpenWrt 上 join 缺失会让进程段静默为空(系统监控
# top6 不显示任何进程且不报错)。awk 缺失同理降级为空,与无 /proc 的非
# Linux 同路径(进程列表空,不报错)。BEGIN+getline 读 pass1 而非 FILENAME
# 判别输入流:对 "-" 的 FILENAME 取值各 awk 实现有差异,getline 语义无歧义。
sample_procs_select() {
  sep=$(printf '\037')
  awk -F"$sep" -v SEP="$sep" -v F1="$1" '
    BEGIN {
      while ((getline line < F1) > 0) {
        split(line, a, SEP)
        ut1[a[1]] = a[3]; st1[a[1]] = a[4]; start1[a[1]] = a[5]
      }
      close(F1)
    }
    ($1 in ut1) && start1[$1] == $5 {
      d = $3 + $4 - ut1[$1] - st1[$1]
      if (d < 0) d = 0
      printf "%d%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s\n", d, SEP, $1, SEP, $2, SEP, ut1[$1], SEP, st1[$1], SEP, start1[$1], SEP, $3, SEP, $4, SEP, $5, SEP, $6
    }
  ' - | sort -rn | head -6
}

# emit_procs_pass: 将 sample_procs_select 输出($1 文件)按采样($2=1|2)转成
# Go 端 parseProcStatSample 的 6 字段格式: pid comm utime stime starttime rss。
# pass1 的 comm/rss Go 端不取(parseProbeProcSections 只用 p2 的 comm/rss),
# 借 pass2 的值保证 6 字段齐全;utime/stime/starttime 用各自采样值
# (Go 端 delta + PID 复用检测依赖这两项,必须正确)。
# pass2 的 comm 字段替换为 argv[0] basename:Linux 内核 comm 截断为 15 字符
# (TASK_COMM_LEN=16 含 \0),/proc/$pid/cmdline 含完整 argv[0] 及参数,取其
# argv[0](第一个空格前)再取 basename(最后 / 后),既不截断(ps、openclaw-gateway)
# 又显示短进程名(与进程管理 name 列一致);内核线程 cmdline 为空回退 comm。
# 仅 top6 fork tr,basename 用参数展开(${%%}、${##})无 fork,流量与 1.2.7 持平。
emit_procs_pass() {
  sep=$(printf '\037')
  while IFS="$sep" read -r d pid comm ut1 st1 start1 ut2 st2 start2 rss2; do
    if [ "$2" = "1" ]; then
      printf '%s\037%s\037%s\037%s\037%s\037%s\n' "$pid" "$comm" "$ut1" "$st1" "$start1" "$rss2"
    else
      cmd=$(tr '\0\n' '  ' < /proc/$pid/cmdline 2>/dev/null)
      if [ -n "$cmd" ]; then
        case $cmd in *' '*) cmd=${cmd%% *};; esac
        case $cmd in *'/'*) cmd=${cmd##*/};; esac
      else
        cmd=$comm
      fi
      printf '%s\037%s\037%s\037%s\037%s\037%s\n' "$pid" "$cmd" "$ut2" "$st2" "$start2" "$rss2"
    fi
  done < "$1"
}

cat /proc/uptime
echo ---LOAD---
cat /proc/loadavg 2>/dev/null
echo ---MEM---
grep -E '^MemTotal:|^MemFree:|^MemAvailable:|^Buffers:|^Cached:|^SReclaimable:|^SwapTotal:|^SwapFree:' /proc/meminfo
echo ---DF---
LC_ALL=C df -k | grep -vE '^tmpfs|^udev|^devtmpfs|^Filesystem'
echo ---CPU1---
grep '^cpu' /proc/stat
echo ---NET1---
if [ -r /proc/net/dev ]; then cat /proc/net/dev; elif command -v ifconfig >/dev/null 2>&1; then ifconfig -a; elif command -v ip >/dev/null 2>&1; then ip -s link; fi
echo ---NETCONN1---
if [ "$1" = "network" ]; then if command -v ss >/dev/null 2>&1; then out=$(ss -H -tnapni 2>/dev/null); if [ -n "$out" ]; then printf '%s\n' "$out"; elif command -v netstat >/dev/null 2>&1; then netstat -tnapn 2>/dev/null | tail -n +3; fi; elif command -v netstat >/dev/null 2>&1; then netstat -tnapn 2>/dev/null | tail -n +3; fi; fi
echo ---DISKIO1---
cat /proc/diskstats
if [ "$1" = "procs" ]; then
mkdir -p /tmp/.lumeterm 2>/dev/null
proctmp=/tmp/.lumeterm/.ptop.$$
ts1p=$(cut -d' ' -f1 /proc/uptime 2>/dev/null || date +%s)
sample_procs > "$proctmp"
fi
sleep 1
echo ---CPU2---
grep '^cpu' /proc/stat
echo ---NET2---
if [ -r /proc/net/dev ]; then cat /proc/net/dev; elif command -v ifconfig >/dev/null 2>&1; then ifconfig -a; elif command -v ip >/dev/null 2>&1; then ip -s link; fi
echo ---NETCONN2---
if [ "$1" = "network" ]; then if command -v ss >/dev/null 2>&1; then out=$(ss -H -tnapni 2>/dev/null); if [ -n "$out" ]; then printf '%s\n' "$out"; elif command -v netstat >/dev/null 2>&1; then netstat -tnapn 2>/dev/null | tail -n +3; fi; elif command -v netstat >/dev/null 2>&1; then netstat -tnapn 2>/dev/null | tail -n +3; fi; fi
echo ---DISKIO2---
cat /proc/diskstats
if [ "$1" = "procs" ]; then
ts2p=$(cut -d' ' -f1 /proc/uptime 2>/dev/null || date +%s)
proctop=/tmp/.lumeterm/.ptop6.$$
sample_procs | sample_procs_select "$proctmp" > "$proctop"
rm -f "$proctmp"
echo ---PROC1---
printf '%s\n' "$ts1p"
emit_procs_pass "$proctop" 1
echo ---PROC2---
printf '%s\n' "$ts2p"
emit_procs_pass "$proctop" 2
rm -f "$proctop"
fi
echo ---DONE---
`

func ensureContextActive(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func writeStringChunksWithContext(ctx context.Context, writer io.Writer, content string) error {
	const chunkSize = 32768
	for offset := 0; offset < len(content); {
		if err := ensureContextActive(ctx); err != nil {
			return err
		}
		end := offset + chunkSize
		if end > len(content) {
			end = len(content)
		}
		written, err := writer.Write([]byte(content[offset:end]))
		if err != nil {
			return err
		}
		offset += written
	}
	return ensureContextActive(ctx)
}
