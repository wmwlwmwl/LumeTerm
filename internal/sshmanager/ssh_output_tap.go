package sshmanager

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type sshOutputTapState struct {
	mu        sync.Mutex
	listeners map[string]map[string]chan []byte
}

var sshOutputTapRegistry sync.Map

func getSSHOutputTapState(manager *SSHManager) *sshOutputTapState {
	value, _ := sshOutputTapRegistry.LoadOrStore(manager, &sshOutputTapState{
		listeners: make(map[string]map[string]chan []byte),
	})
	state, ok := value.(*sshOutputTapState)
	if !ok {
		// ponytail: 类型断言失败极为罕见（LoadOrStore 始终存入正确类型），
		// 若发生则说明存在并发内存损坏，panic 并提供诊断信息。
		panic("sshOutputTapState: unexpected type in registry")
	}
	return state
}

func (m *SSHManager) registerSessionOutputTap(sessionID string) (string, <-chan []byte, func()) {
	state := getSSHOutputTapState(m)
	listenerID := newSSHOutputTapID()
	channel := make(chan []byte, 256)
	state.mu.Lock()
	if _, ok := state.listeners[sessionID]; !ok {
		state.listeners[sessionID] = make(map[string]chan []byte)
	}
	state.listeners[sessionID][listenerID] = channel
	state.mu.Unlock()
	cancel := func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		sessionListeners, ok := state.listeners[sessionID]
		if !ok {
			return
		}
		existingChannel, ok := sessionListeners[listenerID]
		if !ok {
			return
		}
		delete(sessionListeners, listenerID)
		if len(sessionListeners) == 0 {
			delete(state.listeners, sessionID)
		}
		close(existingChannel)
	}
	return listenerID, channel, cancel
}

func (m *SSHManager) emitSessionOutput(sessionID string, data []byte) {
	state := getSSHOutputTapState(m)
	payload := append([]byte(nil), data...)
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, channel := range state.listeners[sessionID] {
		select {
		case channel <- payload:
		default:
		}
	}
}

func (m *SSHManager) closeSessionOutputTaps(sessionID string) {
	state := getSSHOutputTapState(m)
	state.mu.Lock()
	defer state.mu.Unlock()
	sessionListeners, ok := state.listeners[sessionID]
	if !ok {
		return
	}
	delete(state.listeners, sessionID)
	for _, channel := range sessionListeners {
		close(channel)
	}
}

func newSSHOutputTapID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))
	}
	return hex.EncodeToString(buffer)
}
