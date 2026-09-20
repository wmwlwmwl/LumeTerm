package wailsapp

import (
	"fmt"
	"lumeterm/internal/sshmanager"
)

func (a *App) ListGitTerminalCandidates(sessionID string) ([]map[string]interface{}, error) {
	if a == nil || a.sshManager == nil {
		return nil, fmt.Errorf("ssh manager unavailable")
	}
	candidates, err := a.sshManager.ListGitTerminalCandidates(sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, map[string]interface{}{
			"sessionId":   candidate.SessionID,
			"busy":        candidate.Busy,
			"cwd":         candidate.Cwd,
			"current":     candidate.Current,
			"recommended": candidate.Recommended,
		})
	}
	return result, nil
}
func (a *App) AddGitIgnoreEntries(sessionID string, repoPath string, filePaths []string) error {
	if a == nil || a.sshManager == nil {
		return fmt.Errorf("ssh manager unavailable")
	}
	return a.sshManager.AddGitIgnoreEntries(sessionID, repoPath, filePaths)
}
func (a *App) GetGitFileModTime(sessionID string, repoPath string, filePath string) (map[string]interface{}, error) {
	if a == nil || a.sshManager == nil {
		return nil, fmt.Errorf("ssh manager unavailable")
	}
	return a.sshManager.GetGitFileModTime(sessionID, repoPath, filePath)
}
func (a *App) ExecuteGitCommand(sessionID string, repoPath string, args []string, interactive bool) (map[string]interface{}, error) {
	if a == nil || a.sshManager == nil {
		return nil, fmt.Errorf("ssh manager unavailable")
	}
	return a.sshManager.ExecuteGitCommand(sessionID, repoPath, args, interactive)
}

var _ = sshmanager.GitTerminalCandidate{}
