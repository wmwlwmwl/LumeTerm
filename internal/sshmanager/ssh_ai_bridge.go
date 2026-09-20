package sshmanager

import (
	"fmt"
	"sort"
	"strings"

	aitypes "lumeterm/internal/aitypes"
)

func isBetterAIChatTerminalCandidate(left aitypes.AIChatCommandTerminalCandidate, right aitypes.AIChatCommandTerminalCandidate, currentCwd string) bool {
	leftMatchesCurrentCwd := currentCwd != "" && strings.TrimSpace(left.Cwd) == currentCwd
	rightMatchesCurrentCwd := currentCwd != "" && strings.TrimSpace(right.Cwd) == currentCwd
	if left.Busy != right.Busy {
		return !left.Busy
	}
	if leftMatchesCurrentCwd != rightMatchesCurrentCwd {
		return leftMatchesCurrentCwd
	}
	leftHasCwd := strings.TrimSpace(left.Cwd) != ""
	rightHasCwd := strings.TrimSpace(right.Cwd) != ""
	if leftHasCwd != rightHasCwd {
		return leftHasCwd
	}
	return strings.Compare(left.SessionID, right.SessionID) < 0
}

func (m *SSHManager) ListSiblingTerminalCandidates(sessionId string) ([]aitypes.AIChatCommandTerminalCandidate, error) {
	trimmedSessionID := strings.TrimSpace(sessionId)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session not found")
	}

	m.mu.RLock()
	sessionData, ok := m.sessions[trimmedSessionID]
	if !ok || sessionData == nil {
		m.mu.RUnlock()
		return nil, fmt.Errorf("session not found")
	}

	connKey := sessionData.ConnKey
	currentCwd := strings.TrimSpace(sessionData.CurrentCwd)
	if currentCwd == "" {
		currentCwd = strings.TrimSpace(sessionData.TerminalInitPath)
	}

	siblingSessionIDs := append([]string{}, m.connTerminals[connKey]...)
	candidates := make([]aitypes.AIChatCommandTerminalCandidate, 0, len(siblingSessionIDs))
	for _, siblingSessionID := range siblingSessionIDs {
		if siblingSessionID == trimmedSessionID {
			continue
		}
		siblingSession := m.sessions[siblingSessionID]
		if siblingSession == nil || siblingSession.Session == nil || siblingSession.Stdin == nil {
			continue
		}

		candidateCwd := strings.TrimSpace(siblingSession.CurrentCwd)
		if candidateCwd == "" {
			candidateCwd = strings.TrimSpace(siblingSession.TerminalInitPath)
		}

		candidates = append(candidates, aitypes.AIChatCommandTerminalCandidate{
			SessionID: strings.TrimSpace(siblingSessionID),
			Busy:      siblingSession.RemoteHistoryActive && !siblingSession.PromptReady,
			Cwd:       candidateCwd,
		})
	}
	m.mu.RUnlock()

	if len(candidates) == 0 {
		return []aitypes.AIChatCommandTerminalCandidate{}, nil
	}

	recommendedIndex := 0
	for index := 1; index < len(candidates); index++ {
		if isBetterAIChatTerminalCandidate(candidates[index], candidates[recommendedIndex], currentCwd) {
			recommendedIndex = index
		}
	}
	for index := range candidates {
		candidates[index].Recommended = index == recommendedIndex
	}

	sort.SliceStable(candidates, func(i int, j int) bool {
		if candidates[i].Recommended != candidates[j].Recommended {
			return candidates[i].Recommended
		}
		if candidates[i].Busy != candidates[j].Busy {
			return !candidates[i].Busy
		}
		leftMatchesCurrentCwd := currentCwd != "" && strings.TrimSpace(candidates[i].Cwd) == currentCwd
		rightMatchesCurrentCwd := currentCwd != "" && strings.TrimSpace(candidates[j].Cwd) == currentCwd
		if leftMatchesCurrentCwd != rightMatchesCurrentCwd {
			return leftMatchesCurrentCwd
		}
		return strings.Compare(candidates[i].SessionID, candidates[j].SessionID) < 0
	})
	return candidates, nil
}
