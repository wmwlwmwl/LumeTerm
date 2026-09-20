package sshmanager

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"lumeterm/internal/tcpforward"

	"golang.org/x/crypto/ssh"
)

type sshPortForward = tcpforward.Forwarder

type managedPortForward struct {
	id         string
	kind       string
	connKey    string
	serverId   string
	localHost  string
	localPort  string
	remoteHost string
	remotePort string
	enabled    bool
	forwarder  sshPortForward
}

type PortForwardInfo struct {
	ID         string
	Kind       string
	Addr       string
	LocalAddr  string
	RemoteAddr string
	LocalHost  string
	LocalPort  string
	RemoteHost string
	RemotePort string
	Enabled    bool
}

func splitHostPortLoose(addr string) (string, string) {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return "", ""
	}
	idx := strings.LastIndex(trimmed, ":")
	if idx < 0 {
		return trimmed, ""
	}
	host := strings.Trim(trimmed[:idx], "[]")
	return host, trimmed[idx+1:]
}

// serverIdForConnKey 端口映射持久化按 serverId 分组; 当前 connKey 即为 Connection.ID(见 Connect),
// 故直接复用。若未来 connKey 语义变化, 只需改这里的映射。
func (m *SSHManager) serverIdForConnKey(connKey string) string {
	return connKey
}

// PortForwardBelongsToSession 校验某端口映射是否归属给定会话对应的连接, 防止仅凭全局 id 越权操作。
func (m *SSHManager) PortForwardBelongsToSession(sessionId string, id string) bool {
	connKey := m.ConnKeyForSession(sessionId)
	if connKey == "" {
		return false
	}
	m.ensurePersistedPortForwardsLoadedForConnKey(connKey)
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.portForwards[id]
	return ok && entry != nil && entry.connKey == connKey
}

// stopPortForwardsForConnKey 将某连接下所有运行态端口映射转为已停止态(关闭真实监听, 保留记录)。
// 用于 SSH 连接生命周期结束(断连/keepalive 失败/用户关闭最后一个终端)时自动回收监听器,
// 避免本地端口被泄漏占用; 转为已停止态而非删除, 保持"停止不删记录"语义, 重连后可重启。
// 调用方不得持有 m.mu。幂等: 已停止的 entry 会被跳过。
func (m *SSHManager) ensurePersistedPortForwardsLoadedForConnKey(connKey string) {
	if strings.TrimSpace(connKey) == "" || m.app == nil || m.configManager == nil {
		return
	}
	serverId := m.serverIdForConnKey(connKey)
	if strings.TrimSpace(serverId) == "" {
		return
	}
	persisted := m.configManager.GetPortForwards(serverId)
	if len(persisted) == 0 {
		return
	}
	persistNeeded := false
	m.mu.Lock()
	for _, item := range persisted {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		if _, exists := m.portForwards[item.ID]; exists {
			continue
		}
		if item.Enabled {
			persistNeeded = true
		}
		m.portForwards[item.ID] = &managedPortForward{
			id:         item.ID,
			kind:       item.Kind,
			connKey:    connKey,
			serverId:   serverId,
			localHost:  item.LocalHost,
			localPort:  item.LocalPort,
			remoteHost: item.RemoteHost,
			remotePort: item.RemotePort,
			enabled:    false,
			forwarder:  nil,
		}
	}
	m.mu.Unlock()
	if persistNeeded {
		m.persistPortForwardsForServer(serverId)
	}
}

func (m *SSHManager) stopPortForwardsForConnKey(connKey string) {
	if strings.TrimSpace(connKey) == "" {
		return
	}
	m.mu.Lock()
	var toClose []sshPortForward
	serverId := ""
	for _, entry := range m.portForwards {
		if entry == nil || entry.connKey != connKey {
			continue
		}
		serverId = entry.serverId
		if entry.forwarder != nil {
			toClose = append(toClose, entry.forwarder)
			entry.forwarder = nil
		}
		entry.enabled = false
	}
	m.mu.Unlock()

	if len(toClose) == 0 {
		return
	}
	for _, fw := range toClose {
		_ = fw.Close()
	}
	m.persistPortForwardsForServer(serverId)
}

// persistPortForwardsForServer 将某 server 当前所有映射(运行态+已停止态)写盘。调用方不得持有 m.mu。
func (m *SSHManager) persistPortForwardsForServer(serverId string) {
	if m.app == nil || m.configManager == nil || strings.TrimSpace(serverId) == "" {
		return
	}
	m.mu.RLock()
	list := make([]PersistedPortForward, 0)
	now := time.Now().UnixMilli()
	for _, entry := range m.portForwards {
		if entry == nil || entry.serverId != serverId {
			continue
		}
		list = append(list, PersistedPortForward{
			ID:           entry.id,
			Kind:         entry.kind,
			LocalHost:    entry.localHost,
			LocalPort:    entry.localPort,
			RemoteHost:   entry.remoteHost,
			RemotePort:   entry.remotePort,
			Enabled:      entry.enabled,
			LastModified: now,
		})
	}
	m.mu.RUnlock()
	if err := m.configManager.SavePortForwards(serverId, list); err != nil {
		log.Printf("[persistPortForwardsForServer] save failed for %s: %v", serverId, err)
	}
}

func (m *SSHManager) startPortForward(connKey, kind, localAddr, remoteAddr string, start func(*ssh.Client) (sshPortForward, error)) (string, error) {
	m.mu.RLock()
	entry, ok := m.clients[connKey]
	if !ok || entry == nil || entry.Client == nil {
		m.mu.RUnlock()
		return "", errors.New("SSH 连接不存在或已关闭")
	}
	client := entry.Client
	m.mu.RUnlock()

	// 创建远程转发时会等待服务器响应，必须在全局锁外执行，避免半断开的
	// 服务器拖住其它会话和断线清理。
	forwarder, err := start(client)
	if err != nil {
		return "", err
	}

	idPrefix := "lf"
	if kind == "remote" {
		idPrefix = "rf"
	}
	id := fmt.Sprintf("%s-%d", idPrefix, time.Now().UnixNano())
	serverId := m.serverIdForConnKey(connKey)
	localHost, localPort := splitHostPortLoose(localAddr)
	remoteHost, remotePort := splitHostPortLoose(remoteAddr)

	m.mu.Lock()
	current, alive := m.clients[connKey]
	if !alive || current == nil || current.Client != client {
		m.mu.Unlock()
		_ = forwarder.Close()
		return "", errors.New("SSH 连接已关闭")
	}
	m.portForwards[id] = &managedPortForward{
		id: id, kind: kind, connKey: connKey, serverId: serverId,
		localHost: localHost, localPort: localPort, remoteHost: remoteHost, remotePort: remotePort,
		enabled: true, forwarder: forwarder,
	}
	m.mu.Unlock()

	m.persistPortForwardsForServer(serverId)
	return id, nil
}

func (m *SSHManager) StartLocalPortForward(connKey, localAddr, remoteAddr string) (string, error) {
	m.ensurePersistedPortForwardsLoadedForConnKey(connKey)
	return m.startPortForward(connKey, "local", localAddr, remoteAddr, func(client *ssh.Client) (sshPortForward, error) {
		return tcpforward.StartLocal(context.Background(), client, localAddr, remoteAddr)
	})
}

func (m *SSHManager) StartRemotePortForward(connKey, remoteAddr, localAddr string) (string, error) {
	m.ensurePersistedPortForwardsLoadedForConnKey(connKey)
	return m.startPortForward(connKey, "remote", localAddr, remoteAddr, func(client *ssh.Client) (sshPortForward, error) {
		return tcpforward.StartRemote(context.Background(), client, remoteAddr, localAddr)
	})
}

// StopPortForward 停止转发但保留记录(标记为已停止), 关闭真实监听, 不从 map 移除。
func (m *SSHManager) StopPortForward(id string) error {
	m.mu.RLock()
	existing, ok := m.portForwards[id]
	connKey := ""
	if ok && existing != nil {
		connKey = existing.connKey
	}
	m.mu.RUnlock()
	m.ensurePersistedPortForwardsLoadedForConnKey(connKey)
	m.mu.Lock()
	entry, ok := m.portForwards[id]
	if !ok || entry == nil {
		m.mu.Unlock()
		return errors.New("port forward not found")
	}
	forwarder := entry.forwarder
	entry.forwarder = nil
	entry.enabled = false
	serverId := entry.serverId
	m.mu.Unlock()

	if forwarder != nil {
		_ = forwarder.Close()
	}
	m.persistPortForwardsForServer(serverId)
	return nil
}

// DeletePortForward 彻底移除记录; 若仍在运行则先关闭监听。
func (m *SSHManager) DeletePortForward(id string) error {
	m.mu.RLock()
	existing, ok := m.portForwards[id]
	connKey := ""
	if ok && existing != nil {
		connKey = existing.connKey
	}
	m.mu.RUnlock()
	m.ensurePersistedPortForwardsLoadedForConnKey(connKey)
	m.mu.Lock()
	entry, ok := m.portForwards[id]
	if !ok || entry == nil {
		m.mu.Unlock()
		return errors.New("port forward not found")
	}
	forwarder := entry.forwarder
	serverId := entry.serverId
	delete(m.portForwards, id)
	m.mu.Unlock()

	if forwarder != nil {
		_ = forwarder.Close()
	}
	m.persistPortForwardsForServer(serverId)
	return nil
}

// RestartPortForward 重新拉起一个已停止的映射, 复用原配置, 分配新 id。
func (m *SSHManager) RestartPortForward(id string) (string, error) {
	m.mu.RLock()
	existing, ok := m.portForwards[id]
	connKeyForLoad := ""
	if ok && existing != nil {
		connKeyForLoad = existing.connKey
	}
	m.mu.RUnlock()
	m.ensurePersistedPortForwardsLoadedForConnKey(connKeyForLoad)
	m.mu.RLock()
	entry, ok := m.portForwards[id]
	if !ok || entry == nil {
		m.mu.RUnlock()
		return "", errors.New("port forward not found")
	}
	if entry.enabled && entry.forwarder != nil {
		m.mu.RUnlock()
		return id, nil
	}
	kind := entry.kind
	connKey := entry.connKey
	localAddr := net.JoinHostPort(entry.localHost, entry.localPort)
	remoteAddr := net.JoinHostPort(entry.remoteHost, entry.remotePort)
	m.mu.RUnlock()

	m.mu.Lock()
	delete(m.portForwards, id)
	m.mu.Unlock()

	if kind == "local" {
		return m.StartLocalPortForward(connKey, localAddr, remoteAddr)
	}
	return m.StartRemotePortForward(connKey, remoteAddr, localAddr)
}

func (m *SSHManager) ListPortForwards() []PortForwardInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]PortForwardInfo, 0, len(m.portForwards))
	for _, entry := range m.portForwards {
		info := PortForwardInfo{
			ID:         entry.id,
			Kind:       entry.kind,
			Enabled:    entry.enabled,
			LocalHost:  entry.localHost,
			LocalPort:  entry.localPort,
			RemoteHost: entry.remoteHost,
			RemotePort: entry.remotePort,
			LocalAddr:  net.JoinHostPort(entry.localHost, entry.localPort),
			RemoteAddr: net.JoinHostPort(entry.remoteHost, entry.remotePort),
		}
		if entry.forwarder != nil {
			if addr := entry.forwarder.Addr(); addr != nil {
				info.Addr = addr.String()
			}
		}
		infos = append(infos, info)
	}
	return infos
}

func (m *SSHManager) ListPortForwardsForSession(sessionId string) ([]PortForwardInfo, error) {
	m.mu.RLock()
	s, ok := m.sessions[sessionId]
	if !ok {
		m.mu.RUnlock()
		return nil, fmt.Errorf("session not found")
	}
	connKey := s.ConnKey
	m.mu.RUnlock()

	m.ensurePersistedPortForwardsLoadedForConnKey(connKey)

	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]PortForwardInfo, 0, len(m.portForwards))
	for _, entry := range m.portForwards {
		if entry == nil || entry.connKey != connKey {
			continue
		}
		info := PortForwardInfo{
			ID:         entry.id,
			Kind:       entry.kind,
			Enabled:    entry.enabled,
			LocalHost:  entry.localHost,
			LocalPort:  entry.localPort,
			RemoteHost: entry.remoteHost,
			RemotePort: entry.remotePort,
		}
		if entry.forwarder != nil {
			if addr := entry.forwarder.Addr(); addr != nil {
				info.Addr = addr.String()
			}
		}
		switch entry.kind {
		case "local":
			info.LocalAddr = net.JoinHostPort(entry.localHost, entry.localPort)
			info.RemoteAddr = net.JoinHostPort(entry.remoteHost, entry.remotePort)
		case "remote":
			info.LocalAddr = net.JoinHostPort(entry.localHost, entry.localPort)
			info.RemoteAddr = net.JoinHostPort(entry.remoteHost, entry.remotePort)
		}
		infos = append(infos, info)
	}
	return infos, nil
}
