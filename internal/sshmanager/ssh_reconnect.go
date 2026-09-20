package sshmanager

// MCP 断线重连支持:整机断开(transport/keepalive)时记录断连现场,供外部 AI 通过
// MCP reconnect_server 工具定位并重连;连续失败达到阈值时向前端发提醒,让用户介入。
// 记录以 parentSessionId 为键,parentSessionId 重连后复用原 id,前端可无缝收编。

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	// mcpReconnectNotifyEvery 每累计 N 次连续失败,向前端发一次「请手动处理」提醒。
	mcpReconnectNotifyEvery = 3
	// mcpReconnectMaxRecords 断连记录上限(按 parentSessionId 去重,FIFO 淘汰)。
	mcpReconnectMaxRecords = 32
	// mcpReconnectRecordTTL 断连记录保留时长,过期后 AI 无法再一键重连。
	mcpReconnectRecordTTL = 24 * time.Hour
)

// ErrReconnectUnavailable 表示该会话没有可用的断连记录(AI 应改用 list_connected_sessions)。
var ErrReconnectUnavailable = errors.New("no disconnected session record for this session id")

// DisconnectedSessionRecord 一次整机断开的现场快照。
type DisconnectedSessionRecord struct {
	ConnKey         string    `json:"connKey"`
	ParentSessionId string    `json:"parentSessionId"`
	SessionIds      []string  `json:"sessionIds"`
	Reason          string    `json:"reason"`
	ClosedAt        time.Time `json:"closedAt"`
}

// ReconnectOutcome reconnect_server 的执行结果。
type ReconnectOutcome struct {
	SessionId        string            `json:"sessionId"`
	ConnKey          string            `json:"connKey"`
	OldToNew         map[string]string `json:"oldToNew"`
	TerminalCount    int               `json:"terminalCount"`
	AlreadyConnected bool              `json:"alreadyConnected"`
	// FailedTerminals 重连后未能重新打开的旧子终端 id;前端应删除这些终端及其
	// 布局/工作区引用,避免把实际已死、没有通道的终端继续显示为已连接。
	FailedTerminals []string `json:"failedTerminals,omitempty"`
}

// recordDisconnectedConn 在整机断开(cleanupClientTransport)时登记断连现场。
// 同一会话的旧记录被替换;超过上限时淘汰最旧的。
func (m *SSHManager) recordDisconnectedConn(connKey string, terminalIds []string, parentSessionId string, reason string) {
	if connKey == "" || len(terminalIds) == 0 {
		return
	}
	if parentSessionId == "" {
		parentSessionId = terminalIds[0]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recentDisconnects == nil {
		m.recentDisconnects = make(map[string]*DisconnectedSessionRecord)
	}
	if m.mcpReconnectFailures == nil {
		m.mcpReconnectFailures = make(map[string]int)
	}
	now := time.Now()
	// 清理过期记录,并移除与本批终端重叠的旧记录(同一会话再次断开时保留最新现场)
	for parent, record := range m.recentDisconnects {
		if now.Sub(record.ClosedAt) > mcpReconnectRecordTTL {
			delete(m.recentDisconnects, parent)
			delete(m.mcpReconnectFailures, parent)
			continue
		}
		if parent == parentSessionId || overlapsSessionId(record.SessionIds, terminalIds) {
			delete(m.recentDisconnects, parent)
			// 记录被替换/移除的会话不再可重连,失败计数一并清掉,防止残留计数
			// 使后续再次断连的会话在累计失败时提前触发用户提醒。
			delete(m.mcpReconnectFailures, parent)
		}
	}
	ids := append([]string(nil), terminalIds...)
	m.recentDisconnects[parentSessionId] = &DisconnectedSessionRecord{
		ConnKey:         connKey,
		ParentSessionId: parentSessionId,
		SessionIds:      ids,
		Reason:          reason,
		ClosedAt:        now,
	}
	// FIFO 淘汰最旧:被淘汰的会话不再可重连,同步清理其失败计数
	for len(m.recentDisconnects) > mcpReconnectMaxRecords {
		oldestParent := ""
		var oldestTime time.Time
		for parent, record := range m.recentDisconnects {
			if oldestParent == "" || record.ClosedAt.Before(oldestTime) {
				oldestParent = parent
				oldestTime = record.ClosedAt
			}
		}
		if oldestParent == "" {
			break
		}
		delete(m.recentDisconnects, oldestParent)
		delete(m.mcpReconnectFailures, oldestParent)
	}
}

func overlapsSessionId(existing []string, incoming []string) bool {
	for _, a := range existing {
		for _, b := range incoming {
			if a == b {
				return true
			}
		}
	}
	return false
}

// clearDisconnectedRecordsForSession 用户/前端主动断开或重连时,移除相关断连记录与失败计数。
func (m *SSHManager) clearDisconnectedRecordsForSession(sessionId string) {
	if sessionId == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for parent, record := range m.recentDisconnects {
		if parent == sessionId || overlapsSessionId(record.SessionIds, []string{sessionId}) {
			delete(m.recentDisconnects, parent)
			delete(m.mcpReconnectFailures, parent)
		}
	}
}

// LookupDisconnectedSession 查询会话是否处于「已断开且可一键重连」状态。
// 会话已被用户/前端重连(parent 已重新登记)时视为未断开,并顺手清理过期记录。
func (m *SSHManager) LookupDisconnectedSession(sessionId string) (DisconnectedSessionRecord, bool) {
	sessionId = strings.TrimSpace(sessionId)
	if sessionId == "" {
		return DisconnectedSessionRecord{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for parent, record := range m.recentDisconnects {
		if now.Sub(record.ClosedAt) > mcpReconnectRecordTTL {
			delete(m.recentDisconnects, parent)
			delete(m.mcpReconnectFailures, parent)
		}
	}
	for _, record := range m.recentDisconnects {
		if record.ParentSessionId != sessionId && !containsSessionId(record.SessionIds, sessionId) {
			continue
		}
		if _, alive := m.sessions[record.ParentSessionId]; alive {
			// 已重连但记录未清理(如由前端自动重连完成):视为未断开
			delete(m.recentDisconnects, record.ParentSessionId)
			delete(m.mcpReconnectFailures, record.ParentSessionId)
			return DisconnectedSessionRecord{}, false
		}
		return *record, true
	}
	return DisconnectedSessionRecord{}, false
}

func containsSessionId(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

// parentForSessionIdLocked 返回会话所属的父会话 id(子终端取 GroupSessionId)。需持有 m.mu。
func (m *SSHManager) parentForSessionIdLocked(sessionId string) string {
	session, ok := m.sessions[sessionId]
	if !ok || session == nil {
		return ""
	}
	if session.GroupSessionId != "" {
		return session.GroupSessionId
	}
	return sessionId
}

// ReconnectDisconnectedSession 重连已断开的会话:按断连记录找回服务器配置重新拨号,
// 复用原 parentSessionId,并按原终端数量重开子终端。成功后广播 ssh-mcp-reconnected,
// 由前端收编(重映射终端/布局/工作区)。
func (m *SSHManager) ReconnectDisconnectedSession(sessionId string) (ReconnectOutcome, error) {
	sessionId = strings.TrimSpace(sessionId)
	if sessionId == "" {
		return ReconnectOutcome{}, ErrReconnectUnavailable
	}

	record, ok := m.LookupDisconnectedSession(sessionId)
	if !ok {
		// 会话可能仍然在线(如仅子终端 id 变化):幂等返回成功
		m.mu.RLock()
		parent := m.parentForSessionIdLocked(sessionId)
		_, alive := m.sessions[sessionId]
		m.mu.RUnlock()
		if alive && parent != "" {
			return ReconnectOutcome{
				SessionId:        parent,
				ConnKey:          m.ConnKeyForSession(parent),
				OldToNew:         map[string]string{parent: parent},
				TerminalCount:    1,
				AlreadyConnected: true,
			}, nil
		}
		return ReconnectOutcome{}, ErrReconnectUnavailable
	}

	conn, err := m.resolveDisconnectedConn(record.ConnKey)
	if err != nil {
		return ReconnectOutcome{}, m.recordMCPReconnectFailure(record.ParentSessionId, record.ConnKey, err)
	}
	// 按 connKey 串行化整个恢复序列(解析→拨号→重开子终端→清理记录):
	// 并发 reconnect_server 对同一服务器只执行一次恢复,后续调用在锁释放后
	// 因记录已清,会走幂等路径(会话在线→AlreadyConnected),不会重复重开子终端。
	m.reconnectLocksMu.Lock()
	reconnectLock := m.reconnectLocks[record.ConnKey]
	if reconnectLock == nil {
		reconnectLock = &sync.Mutex{}
		m.reconnectLocks[record.ConnKey] = reconnectLock
	}
	m.reconnectLocksMu.Unlock()
	reconnectLock.Lock()
	defer reconnectLock.Unlock()

	// 拿锁后复查:前一个并发调用可能已完成恢复并清掉记录,此时走幂等路径,
	// 不重复拨号,更不重复重开子终端。
	if _, stillDead := m.LookupDisconnectedSession(record.ParentSessionId); !stillDead {
		m.mu.RLock()
		parent := m.parentForSessionIdLocked(record.ParentSessionId)
		_, alive := m.sessions[record.ParentSessionId]
		m.mu.RUnlock()
		if alive && parent != "" {
			return ReconnectOutcome{
				SessionId:        parent,
				ConnKey:          record.ConnKey,
				OldToNew:         map[string]string{parent: parent},
				TerminalCount:    1,
				AlreadyConnected: true,
			}, nil
		}
		// 记录已消费但会话不在线(如过期),不再重复恢复
		return ReconnectOutcome{}, ErrReconnectUnavailable
	}

	// 是否允许外部 AI 重连由 MCP 全局授权控制(MCP 服务器启用即放行);
	// MCP 服务器关闭时不注入 reconnect 能力,MCP 工具目录里本就没有 reconnect_server。
	// 复用原 parentSessionId 重新拨号;主机密钥变更时 Connect 会挂起待用户确认并返回错误,
	// 该错误计入连续失败,达到阈值即提醒用户(与整体流程一致)。
	if err := m.Connect(record.ParentSessionId, conn); err != nil {
		wrapped := fmt.Errorf("reconnect 失败: %w", err)
		if errors.Is(err, ErrHostKeyChanged) {
			wrapped = fmt.Errorf("reconnect 失败: 主机密钥已变更, 等待用户在 LumeTerm 界面确认后重试")
		} else if errors.Is(err, ErrAuthFailed) {
			wrapped = fmt.Errorf("reconnect 失败: 认证失败, 需要用户更新凭据: %w", err)
		}
		return ReconnectOutcome{}, m.recordMCPReconnectFailure(record.ParentSessionId, record.ConnKey, wrapped)
	}

	// 重开子终端,保持终端数量一致;失败的逐个跳过并明确上报,
	// 前端据此删除对应旧终端(避免残留无通道的死终端)。
	oldToNew := map[string]string{record.ParentSessionId: record.ParentSessionId}
	terminalCount := 1
	var failedTerminals []string
	for _, oldId := range record.SessionIds {
		if oldId == record.ParentSessionId {
			continue
		}
		newId, termErr := m.OpenTerminal(record.ParentSessionId)
		if termErr != nil {
			log.Printf("[mcp-reconnect] 重开子终端失败 parent=%s old=%s err=%v", record.ParentSessionId, oldId, termErr)
			failedTerminals = append(failedTerminals, oldId)
			continue
		}
		oldToNew[oldId] = newId
		terminalCount++
	}

	m.mu.Lock()
	delete(m.mcpReconnectFailures, record.ParentSessionId)
	delete(m.recentDisconnects, record.ParentSessionId)
	m.mu.Unlock()

	if m.ctx != nil {
		runtime.EventsEmit(m.ctx, "ssh-mcp-reconnected", map[string]interface{}{
			"sessionId":       record.ParentSessionId,
			"connKey":         record.ConnKey,
			"oldToNew":        oldToNew,
			"failedTerminals": failedTerminals,
		})
	}
	log.Printf("[mcp-reconnect] 会话已重连 parent=%s connKey=%s terminals=%d failed=%d", record.ParentSessionId, record.ConnKey, terminalCount, len(failedTerminals))
	return ReconnectOutcome{
		SessionId:        record.ParentSessionId,
		ConnKey:          record.ConnKey,
		OldToNew:         oldToNew,
		TerminalCount:    terminalCount,
		FailedTerminals:  failedTerminals,
	}, nil
}

// resolveDisconnectedConn 把断连记录里的 ConnKey 还原成可拨号的服务器配置。
func (m *SSHManager) resolveDisconnectedConn(connKey string) (Connection, error) {
	if m.configManager == nil {
		return Connection{}, fmt.Errorf("配置管理器不可用, 无法解析断开连接")
	}
	if conn, ok := m.configManager.GetConnectionByID(connKey); ok {
		return m.configManager.ResolveConnectionRuntime(conn)
	}
	for _, conn := range m.configManager.GetConnections() {
		if conn.Username+"@"+dialAddr(conn.Host, conn.Port) == connKey {
			return m.configManager.ResolveConnectionRuntime(conn)
		}
	}
	return Connection{}, fmt.Errorf("未找到断开连接对应的服务器配置 (%s)", connKey)
}

// recordMCPReconnectFailure 累计同一会话的连续重连失败;每达到阈值倍数时提醒用户。
// 返回包装后的错误供调用方返回给 AI。
func (m *SSHManager) recordMCPReconnectFailure(parentSessionId string, connKey string, cause error) error {
	fails := 0
	m.mu.Lock()
	if m.mcpReconnectFailures == nil {
		m.mcpReconnectFailures = make(map[string]int)
	}
	m.mcpReconnectFailures[parentSessionId]++
	fails = m.mcpReconnectFailures[parentSessionId]
	m.mu.Unlock()

	log.Printf("[mcp-reconnect] 重连失败 parent=%s connKey=%s fails=%d err=%v", parentSessionId, connKey, fails, cause)
	if fails%mcpReconnectNotifyEvery == 0 && m.ctx != nil {
		runtime.EventsEmit(m.ctx, "mcp-reconnect-failed", map[string]interface{}{
			"sessionId": parentSessionId,
			"connKey":   connKey,
			"attempts":  fails,
			"error":     cause.Error(),
		})
	}
	return cause
}
