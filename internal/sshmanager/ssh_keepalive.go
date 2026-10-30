package sshmanager

import (
	"errors"
	"io"
	"log"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/crypto/ssh"
)

func (m *SSHManager) watchClient(connKey string, client *ssh.Client) {
	ticker := time.NewTicker(sshKeepaliveInterval)
	defer ticker.Stop()
	fails := 0
	for range ticker.C {
		tracked, probeOK := m.checkClientKeepalive(connKey, client, sshKeepaliveTimeout)
		var stop bool
		fails, stop = m.handleKeepaliveProbeResult(connKey, client, fails, tracked, probeOK)
		if stop {
			return
		}
	}
}

// handleKeepaliveProbeResult 根据单次探活结果更新连续失败计数。
// 返回 (新失败次数, 是否结束 watch)。达 sshKeepaliveFailMax 才拆共享连接。
func (m *SSHManager) handleKeepaliveProbeResult(connKey string, client *ssh.Client, fails int, tracked, probeOK bool) (int, bool) {
	if !tracked {
		return fails, true
	}
	if probeOK {
		return 0, false
	}
	fails++
	if fails >= sshKeepaliveFailMax {
		log.Printf("[keepalive] 连续探活失败达上限 connKey=%s fails=%d 触发 cleanupClientTransport(reason=keepalive)", connKey, fails)
		m.cleanupClientTransport(connKey, client, "keepalive")
		return fails, true
	}
	return fails, false
}

// checkClientKeepalive 发起一次 SSH 层探活。
// tracked=false：该 client 已不在 map（停止 watch，勿重复 cleanup）。
// tracked=true 且 probeOK=true：通路正常（含服务端拒绝未知 keepalive 名但仍有响应）。
// tracked=true 且 probeOK=false：超时或传输错误——不在此处拆线，由 watch 累计失败。
func (m *SSHManager) checkClientKeepalive(connKey string, client *ssh.Client, timeout time.Duration) (tracked bool, probeOK bool) {
	m.mu.RLock()
	entry, ok := m.clients[connKey]
	if !ok || entry.Client != client || entry.NetConn == nil {
		m.mu.RUnlock()
		return false, false
	}
	m.mu.RUnlock()

	done := make(chan error, 1)
	go func() {
		// 用 OpenSSH 标准保活请求名：绝大多数服务器（含 OpenSSH）会回包，
		// 既能真正维持空闲连接、又能让探活拿到正常响应。自定义名（如
		// keepalive@lumin-ssh）会被部分服务器静默丢弃 → 探活 20s 超时 → 误断。
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		done <- err
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err == nil {
			m.mu.RLock()
			current, currentOK := m.clients[connKey]
			alive := currentOK && current.Client == client
			m.mu.RUnlock()
			if !alive {
				return false, false
			}
			return true, true
		}
		// 有响应但 request 失败（如未知类型被拒）：golang.org/x/crypto/ssh 对 want-reply
		// 被拒通常仍 err==nil 且 reply=false；若将来变成 err，仍视为通路可达。
		m.mu.RLock()
		current, currentOK := m.clients[connKey]
		alive := currentOK && current.Client == client
		m.mu.RUnlock()
		if !alive {
			return false, false
		}
		// 传输层错误（连接已断）→ 计失败；纯协议拒绝在 SendRequest 成功路径已覆盖。
		if isSSHKeepaliveTransportError(err) {
			log.Printf("[keepalive] 探活传输层错误 connKey=%s err=%v", connKey, err)
			return true, false
		}
		log.Printf("[keepalive] 探活 request 被拒（非传输错误）connKey=%s err=%v", connKey, err)
		return true, true
	case <-timer.C:
		log.Printf("[keepalive] 探活超时 connKey=%s timeout=%v", connKey, timeout)
		m.mu.RLock()
		current, currentOK := m.clients[connKey]
		alive := currentOK && current.Client == client
		m.mu.RUnlock()
		if !alive {
			return false, false
		}
		return true, false
	}
}

func isSSHKeepaliveTransportError(err error) bool {
	if err == nil {
		return false
	}
	// 连接已死、reset、EOF 等：算探活失败。其它错误偏协议层，保守当通路仍在。
	return isTransientNetError(err) ||
		errors.Is(err, io.EOF) ||
		strings.Contains(err.Error(), "connection lost") ||
		strings.Contains(err.Error(), "use of closed network connection")
}

func (m *SSHManager) cleanupClientTransport(connKey string, client *ssh.Client, reason string) {
	m.mu.Lock()
	entry, ok := m.clients[connKey]
	if !ok || entry.Client != client {
		m.mu.Unlock()
		return
	}
	terminalIds := append([]string(nil), m.connTerminals[connKey]...)
	terminalSessions := make(map[string]*SessionData, len(terminalIds))
	parentSessionId := ""
	for _, terminalId := range terminalIds {
		if session := m.sessions[terminalId]; session != nil {
			terminalSessions[terminalId] = session
			if parentSessionId == "" {
				parentSessionId = terminalId
				if session.GroupSessionId != "" {
					parentSessionId = session.GroupSessionId
				}
			}
		}
	}
	netConn := entry.NetConn
	sftpClient := entry.SFTP
	if entry.SFTPReady != nil {
		entry.SFTPReadyOnce.Do(func() { close(entry.SFTPReady) })
	}
	delete(m.clients, connKey)
	delete(m.connTerminals, connKey)
	delete(m.probeDeployed, connKey)
	delete(m.probeFailed, connKey)
	delete(m.probeRunFailed, connKey)
	delete(m.remoteFeatures, connKey)
	m.mu.Unlock()
	// 连接级清理：取消该连接下所有终端的在途文件操作（压缩/解压/删除等）。
	for _, terminalId := range terminalIds {
		m.abortSessionFileOperations(terminalId)
	}
	globalSSHChannelUsage.forget(connKey)
	if netConn != nil {
		_ = netConn.Close()
	}
	// transport 已关闭后再回收端口转发，避免远程 listener.Close 等待
	// cancel-tcpip-forward 回复而阻塞整个断线流程。
	m.stopPortForwardsForConnKey(connKey)

	if reason == "" {
		reason = "transport"
	}
	remoteAddr := ""
	if netConn != nil {
		remoteAddr = netConn.RemoteAddr().String()
	}
	log.Printf("[disconnect] cleanupClientTransport connKey=%s reason=%s terminalCount=%d remoteAddr=%s netConn!=nil=%v sftpClient!=nil=%v",
		connKey, reason, len(terminalIds), remoteAddr, netConn != nil, sftpClient != nil)
	// 静默拆各终端 session，再发一次「整机连接断开」事件，避免 N 次误报。
	// expected 指针确保旧 transport 的迟到清理不会误删同 ID 的新 session。
	for _, terminalId := range terminalIds {
		_ = m.disconnect(terminalId, terminalSessions[terminalId])
	}
	// 登记断连现场:外部 AI 可通过 MCP reconnect_server 一键重连该会话。
	m.recordDisconnectedConn(connKey, terminalIds, parentSessionId, reason)
	if sftpClient != nil {
		closeWithTimeout(sftpClient, 3*time.Second)
	}
	closeWithTimeout(client, 3*time.Second)

	if m.ctx != nil && len(terminalIds) > 0 {
		if parentSessionId == "" {
			parentSessionId = terminalIds[0]
		}
		runtime.EventsEmit(m.ctx, "ssh-disconnected", map[string]interface{}{
			"sessionId":        terminalIds[0],
			"parentSessionId":  parentSessionId,
			"terminalIds":      terminalIds,
			"reason":           reason,
			"connectionClosed": true,
		})
	}
}

// disconnectAndNotify 结束单个 terminal 并通知前端。
// reason=session_end：shell 正常/异常退出；connectionClosed 表示是否同时拆掉了共享 TCP。
