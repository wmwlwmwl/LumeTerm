package mcpserver

import (
	"errors"
	"sort"
	"strings"
)

var ErrSessionProviderUnavailable = errors.New("session provider unavailable")
var ErrSessionNotFound = errors.New("session not found")

type Service struct {
	sessionProvider SessionProvider
	// followLatestTerminal 开启后,外部 AI 对某服务器的操作自动解析到该服务器
	// 最新打开的终端:既覆盖「同服务器另开了新终端」的场景,也在传入的旧 id
	// 已失效但同组仍有存活终端时兜底,避免 AI 卡在过期的 session_id 上。
	followLatestTerminal bool
}

func NewService(sessionProvider SessionProvider) *Service {
	return &Service{sessionProvider: sessionProvider}
}

// SetFollowLatestTerminal 控制是否把会话解析重定向到同服务器最新终端(默认关闭)。
func (s *Service) SetFollowLatestTerminal(enabled bool) {
	if s == nil {
		return
	}
	s.followLatestTerminal = enabled
}

func (s *Service) ListConnectedSessions() ([]ConnectedSession, error) {
	if s == nil || s.sessionProvider == nil {
		return nil, ErrSessionProviderUnavailable
	}
	descriptors, err := s.sessionProvider.ListConnectedSessions()
	if err != nil {
		return nil, err
	}
	result := make([]ConnectedSession, 0, len(descriptors))
	for _, descriptor := range descriptors {
		groupSessionID := descriptor.GroupSessionID
		if groupSessionID == "" {
			groupSessionID = descriptor.SessionID
		}
		result = append(result, ConnectedSession{
			SessionID: descriptor.SessionID,
			GroupSessionID: groupSessionID,
			ConnectionRef: descriptor.ConnectionRef,
			ConnectionID: descriptor.ConnectionID,
			Address: descriptor.Address,
			Tags: append([]string(nil), descriptor.Tags...),
			SFTPAvailable: descriptor.SFTPAvailable,
			IsChildTerminal: descriptor.GroupSessionID != "",
			IsLatestTerminal: descriptor.IsLatestTerminal,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ConnectionRef != result[j].ConnectionRef {
			return result[i].ConnectionRef < result[j].ConnectionRef
		}
		if result[i].GroupSessionID != result[j].GroupSessionID {
			return result[i].GroupSessionID < result[j].GroupSessionID
		}
		return result[i].SessionID < result[j].SessionID
	})
	return result, nil
}

func (s *Service) GetConnectedSession(sessionID string) (ConnectedSession, error) {
	sessions, err := s.ListConnectedSessions()
	if err != nil {
		return ConnectedSession{}, err
	}
	trimmedSessionID := strings.TrimSpace(sessionID)
	for _, session := range sessions {
		if session.SessionID == trimmedSessionID {
			return s.followLatestSession(sessions, session), nil
		}
	}
	// 传入的 id 已不在存活列表(例如其终端标签被关闭):跟随模式下若它曾是
	// 某组的父会话且同组仍有存活终端,则解析到该组最新终端而不是直接报错。
	if s.followLatestTerminal {
		for _, session := range sessions {
			if session.GroupSessionID != trimmedSessionID {
				continue
			}
			if target, ok := latestSessionInGroup(sessions, groupKey(session), trimmedSessionID); ok {
				return target, nil
			}
		}
	}
	return ConnectedSession{}, ErrSessionNotFound
}

// followLatestSession 在跟随模式下把解析结果重定向到同服务器最新终端。
func (s *Service) followLatestSession(sessions []ConnectedSession, session ConnectedSession) ConnectedSession {
	if !s.followLatestTerminal || session.IsLatestTerminal {
		return session
	}
	if target, ok := latestSessionInGroup(sessions, groupKey(session), session.SessionID); ok {
		return target
	}
	return session
}

// groupKey 是终端分组键:优先共享连接(connKey),回退到父会话 id。
func groupKey(session ConnectedSession) string {
	if session.ConnectionRef != "" {
		return session.ConnectionRef
	}
	if session.GroupSessionID != "" {
		return session.GroupSessionID
	}
	return session.SessionID
}

// latestSessionInGroup 返回分组内标记为最新的终端;excludeID 用于避免把请求
// 的已失效 id 自己当成候选。
func latestSessionInGroup(sessions []ConnectedSession, key string, excludeID string) (ConnectedSession, bool) {
	for _, session := range sessions {
		if !session.IsLatestTerminal || session.SessionID == excludeID {
			continue
		}
		if groupKey(session) == key {
			return session, true
		}
	}
	return ConnectedSession{}, false
}
