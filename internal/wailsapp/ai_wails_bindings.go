package wailsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	ai "lumeterm/internal/ai"
	"lumeterm/internal/config"
	"lumeterm/internal/localopen"
	"lumeterm/internal/mcp"
	"lumeterm/internal/mcpbridge"
	"lumeterm/internal/mcpserver"
	"lumeterm/internal/sshmanager"

	"github.com/pkg/sftp"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/crypto/ssh"
)

type AIBindings struct {
	app                       *App
	runtimeApp                *ai.Service
	temporaryConversationsDir string
	temporaryConversationsMu  sync.RWMutex
}

func NewAIBindings(app *App) *AIBindings {
	root := os.TempDir()
	// 清理时同时匹配旧前缀（改名前的遗留目录）与新前缀
	for _, pattern := range []string{"lumeterm-ai-temporary-conversations-*", "lumin-ai-temporary-conversations-*"} {
		if matches, err := filepath.Glob(filepath.Join(root, pattern)); err == nil {
			for _, match := range matches {
				_ = os.RemoveAll(match)
			}
		}
	}
	tempDir, _ := os.MkdirTemp(root, "lumeterm-ai-temporary-conversations-*")
	return &AIBindings{app: app, temporaryConversationsDir: tempDir}
}

func (b *AIBindings) temporaryConversationPath(conversationID string) (string, error) {
	id := strings.TrimSpace(conversationID)
	if id == "" || filepath.Base(id) != id || !strings.HasPrefix(id, "temporary-") {
		return "", fmt.Errorf("invalid temporary conversation id")
	}
	if b.temporaryConversationsDir == "" {
		return "", fmt.Errorf("temporary conversation storage unavailable")
	}
	return filepath.Join(b.temporaryConversationsDir, id+".json"), nil
}

func temporaryConversationSummary(snapshot ai.AIConversationSnapshot) ai.AIConversationSummary {
	return ai.AIConversationSummary{ID: snapshot.ID, Title: snapshot.Title, CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt, Status: snapshot.Status, ToolProtocol: snapshot.ToolProtocol, MessageCount: len(snapshot.Messages), PromptCacheBypassTimestamp: snapshot.PromptCacheBypassTimestamp, ParentConversationID: snapshot.ParentConversationID, RootConversationID: snapshot.RootConversationID, RelationType: snapshot.RelationType, RelationSource: snapshot.RelationSource, ParentTitleSnapshot: snapshot.ParentTitleSnapshot, Archived: snapshot.Archived, Transient: true}
}

func (b *AIBindings) ListTemporaryAIConversations() []ai.AIConversationSummary {
	b.temporaryConversationsMu.RLock()
	defer b.temporaryConversationsMu.RUnlock()
	entries, err := os.ReadDir(b.temporaryConversationsDir)
	if err != nil {
		return []ai.AIConversationSummary{}
	}
	summaries := make([]ai.AIConversationSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(b.temporaryConversationsDir, entry.Name()))
		if err != nil {
			continue
		}
		var snapshot ai.AIConversationSnapshot
		if json.Unmarshal(data, &snapshot) != nil {
			continue
		}
		summaries = append(summaries, temporaryConversationSummary(snapshot))
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].UpdatedAt > summaries[j].UpdatedAt })
	return summaries
}

func (b *AIBindings) GetTemporaryAIConversation(conversationID string) (ai.AIConversationSnapshot, error) {
	path, err := b.temporaryConversationPath(conversationID)
	if err != nil {
		return ai.AIConversationSnapshot{}, err
	}
	b.temporaryConversationsMu.RLock()
	defer b.temporaryConversationsMu.RUnlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return ai.AIConversationSnapshot{}, err
	}
	var snapshot ai.AIConversationSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return ai.AIConversationSnapshot{}, err
	}
	snapshot.Transient = true
	return snapshot, nil
}

func (b *AIBindings) SaveTemporaryAIConversation(jsonStr string) (ai.AIConversationSnapshot, error) {
	var snapshot ai.AIConversationSnapshot
	if err := json.Unmarshal([]byte(jsonStr), &snapshot); err != nil {
		return snapshot, err
	}
	path, err := b.temporaryConversationPath(snapshot.ID)
	if err != nil {
		return snapshot, err
	}
	snapshot.Transient = true
	if snapshot.UpdatedAt == 0 {
		snapshot.UpdatedAt = time.Now().UnixMilli()
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return snapshot, err
	}
	b.temporaryConversationsMu.Lock()
	defer b.temporaryConversationsMu.Unlock()
	temporaryPath := path + ".new"
	if err := os.WriteFile(temporaryPath, data, 0600); err != nil {
		return snapshot, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return snapshot, err
	}
	return snapshot, nil
}

func (b *AIBindings) DeleteTemporaryAIConversation(conversationID string) error {
	path, err := b.temporaryConversationPath(conversationID)
	if err != nil {
		return err
	}
	b.temporaryConversationsMu.Lock()
	defer b.temporaryConversationsMu.Unlock()
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (b *AIBindings) runtime() *ai.Service {
	if b == nil {
		return nil
	}
	if b.runtimeApp == nil {
		configDir := ""
		if b.app != nil && b.app.configManager != nil {
			configDir = b.app.configManager.GetConfigDir()
		}
		var sessionProvider ai.SessionProviderDelegate
		var sshDelegate ai.SSHDelegate
		if b.app != nil {
			sessionProvider = mcpbridge.SessionProvider{Host: newMCPHost(b.app)}
			sshDelegate = aiSSHDelegate{manager: b.app.sshManager}
		}
		b.runtimeApp = ai.NewService(context.Background(), configDir, sessionProvider, sshDelegate)
	}
	if b.app != nil {
		b.runtimeApp.SetContext(b.app.ctx)
		b.runtimeApp.SetThemeToolDelegate(b.app)
	}
	return b.runtimeApp
}

func (b *AIBindings) StartAIChat(requestID string, messagesJSON string) error {
	return b.runtime().StartAIChat(requestID, messagesJSON)
}

func (b *AIBindings) GetAIAssistantFirstReply(language string) string {
	return ai.BuildAIAssistantFirstReply(language)
}

func (b *AIBindings) CancelAIChat(requestID string) {
	b.runtime().CancelAIChat(requestID)
}

func (b *AIBindings) ApproveAIChatTools(requestID string) error {
	return b.runtime().ApproveAIChatTools(requestID)
}

func (b *AIBindings) RejectAIChatTools(requestID string) error {
	return b.runtime().RejectAIChatTools(requestID)
}

func (b *AIBindings) RejectAIChatToolsForQueuedSubmission(requestID string) error {
	return b.runtime().RejectAIChatToolsForQueuedSubmission(requestID)
}

func (b *AIBindings) ResolveAIChatFollowup(requestID string, answer string, imagesJSON string) error {
	return b.runtime().ResolveAIChatFollowup(requestID, answer, imagesJSON)
}

func (b *AIBindings) StartAIChatCollaboration(requestID string) error {
	return b.runtime().StartAIChatCollaboration(requestID)
}

func (b *AIBindings) DisableAIChatCollaboration(requestID string) error {
	return b.runtime().DisableAIChatCollaboration(requestID)
}

func (b *AIBindings) SetAIChatSkipNextAutomaticRequest(requestID string, enabled bool) {
	b.runtime().SetAIChatSkipNextAutomaticRequest(requestID, enabled)
}

func (b *AIBindings) ContinueAIChatTool(requestID string) error {
	return b.runtime().ContinueAIChatTool(requestID)
}

func (b *AIBindings) TerminateAIChatTool(requestID string) error {
	return b.runtime().TerminateAIChatTool(requestID)
}

func (b *AIBindings) PreviewAIChatToolRestore(reviewID string, sessionID string) (map[string]interface{}, error) {
	return b.runtime().PreviewAIChatToolRestore(reviewID, sessionID)
}

func (b *AIBindings) PreviewAIChatToolDiff(reviewID string, sessionID string) (map[string]interface{}, error) {
	return b.runtime().PreviewAIChatToolDiff(reviewID)
}

func (b *AIBindings) ReapplyAIChatTool(reviewID string, sessionID string) error {
	return b.runtime().ReapplyAIChatTool(reviewID, sessionID)
}

func (b *AIBindings) RestoreAIChatTool(reviewID string, sessionID string) error {
	return b.runtime().RestoreAIChatTool(reviewID, sessionID)
}

func (b *AIBindings) ListAIChatCommandTerminalCandidates(requestID string) ([]ai.AIChatCommandTerminalCandidate, error) {
	return b.runtime().ListAIChatCommandTerminalCandidates(requestID)
}

func (b *AIBindings) AssignAIChatToolTerminal(requestID string, targetSessionID string) error {
	return b.runtime().AssignAIChatToolTerminal(requestID, targetSessionID)
}

func (b *AIBindings) ListAIConversations() []ai.AIConversationSummary {
	return b.runtime().ListAIConversations()
}

func (b *AIBindings) CreateAIConversation(title string) (ai.AIConversationSnapshot, error) {
	return b.runtime().CreateAIConversation(title)
}

func (b *AIBindings) GetAIConversation(conversationID string) (ai.AIConversationSnapshot, error) {
	return b.runtime().GetAIConversation(conversationID)
}

func (b *AIBindings) SaveAIConversation(jsonStr string) (ai.AIConversationSnapshot, error) {
	return b.runtime().SaveAIConversation(jsonStr)
}

func (b *AIBindings) DeleteAIConversation(conversationID string) error {
	return b.runtime().DeleteAIConversation(conversationID)
}

func (b *AIBindings) SearchAIConversationMessages(query string, conversationID string, limit int) ([]ai.AIConversationMessageSearchResult, error) {
	return b.runtime().SearchAIConversationMessages(query, conversationID, limit)
}

func (b *AIBindings) OpenAIConversationFolder(conversationID string) error {
	trimmedConversationID := strings.TrimSpace(conversationID)
	if trimmedConversationID == "" {
		return fmt.Errorf("conversation id is required")
	}
	if b == nil || b.app == nil || b.app.configManager == nil {
		return fmt.Errorf("config manager unavailable")
	}
	tasksDir := b.app.configManager.GetTasksDir()
	if tasksDir == "" {
		tasksDir = filepath.Join(b.app.configManager.GetConfigDir(), "tasks")
	}
	return localopen.Reveal(filepath.Join(tasksDir, trimmedConversationID), true)
}

// RevealAIDebugLog 在文件管理器中定位 AI 对话完整日志(ai.log),
// 便于用户在 AI 面板出现异常时直接把该文件提供出来排查。
func (b *AIBindings) RevealAIDebugLog() error {
	if b == nil || b.app == nil || b.app.configManager == nil {
		return fmt.Errorf("config manager unavailable")
	}
	logPath := strings.TrimSpace(ai.AIDebugLogPath())
	if logPath == "" {
		configDir := b.app.configManager.GetConfigDir()
		if configDir == "" {
			return fmt.Errorf("config dir unavailable")
		}
		logPath = filepath.Join(configDir, "ai.log")
	}
	if _, err := os.Stat(logPath); err != nil {
		return fmt.Errorf("AI 日志尚未生成: %w", err)
	}
	return localopen.Reveal(logPath, false)
}

func (b *AIBindings) PreprocessAIConversationLongText(conversationID string, text string) (string, error) {
	return b.runtime().PreprocessAIConversationLongText(conversationID, text)
}

func (b *AIBindings) ReadAIConversationWrappedFile(conversationID string, localPath string) (string, error) {
	return b.runtime().ReadAIConversationWrappedFile(conversationID, localPath)
}

func (b *AIBindings) ListAIConversationBackups(conversationID string) []ai.AIConversationBackup {
	return b.runtime().ListAIConversationBackups(conversationID)
}

func (b *AIBindings) GetAIConversationBackupHistory(conversationID string, backupID string) []ai.AIConversationAPIMessage {
	return b.runtime().GetAIConversationBackupHistory(conversationID, backupID)
}

func (b *AIBindings) RestoreAIConversationBackup(conversationID string, backupID string) (ai.AIConversationSnapshot, error) {
	return b.runtime().RestoreAIConversationBackup(conversationID, backupID)
}

func (b *AIBindings) DeleteAIConversationBackup(conversationID string, backupID string) error {
	return b.runtime().DeleteAIConversationBackup(conversationID, backupID)
}

func (b *AIBindings) CountAIConversationContextTokens(sessionID string, snapshotJSON string) (ai.AIConversationContextMetrics, error) {
	return b.runtime().CountAIConversationContextTokens(sessionID, snapshotJSON)
}

func (b *AIBindings) CondenseAIConversationContext(conversationID string, sessionID string) (ai.AIConversationContextCondenseResult, error) {
	return b.runtime().CondenseAIConversationContext(conversationID, sessionID)
}

func (b *AIBindings) PreviewAIConversationContextCondense(conversationID string, sessionID string) (ai.AIConversationContextCondenseResult, error) {
	return b.runtime().PreviewAIConversationContextCondense(conversationID, sessionID)
}

func (b *AIBindings) ProbeAIProviderLiveness(conversationID string, sessionID string, requestID string) (bool, error) {
	return b.runtime().ProbeAIProviderLiveness(conversationID, sessionID, requestID)
}

func (b *AIBindings) CreateAIConversationSummarySubtask(conversationID string, sessionID string, requestID string) (ai.AIConversationSummarySubtaskResult, error) {
	return b.runtime().CreateAIConversationSummarySubtask(conversationID, sessionID, requestID)
}

func (b *AIBindings) BuildAIConversationTokenLedger(sessionID string, snapshotJSON string) (ai.AIConversationTokenLedger, error) {
	return b.runtime().BuildAIConversationTokenLedger(sessionID, snapshotJSON)
}

func (b *AIBindings) CountAIConversationAPIMessageRawTokens(sessionID string, conversationID string, messagesJSON string) ([]ai.AIConversationAPIMessageTokenEntry, error) {
	return b.runtime().CountAIConversationAPIMessageRawTokens(sessionID, conversationID, messagesJSON)
}

func (b *AIBindings) GetAIGlobalSettings() ai.AIGlobalSettings {
	return b.runtime().GetAIGlobalSettings()
}

func (b *AIBindings) SaveAIGlobalSettings(jsonStr string) error {
	previous := b.runtime().GetAIGlobalSettings()
	if err := b.runtime().SaveAIGlobalSettings(jsonStr); err != nil {
		return err
	}
	current := b.runtime().GetAIGlobalSettings()
	if previous.MCPEnabled != current.MCPEnabled || previous.MCPAllowBrowserCalls != current.MCPAllowBrowserCalls {
		mcpbridge.ApplyServiceState(b.app.configManager.GetConfigDir(), newMCPHost(b.app))
	}
	if previous.MCPActivityVisible != current.MCPActivityVisible {
		if b != nil && b.app != nil && b.app.ctx != nil {
			runtime.EventsEmit(b.app.ctx, "mcp-activity-visibility-changed", current.MCPActivityVisible)
		}
	}
	previous.CurrentProviderID = ""
	current.CurrentProviderID = ""
	previous.UpdatedAt = 0
	current.UpdatedAt = 0
	previous.ProxyNodes = nil
	current.ProxyNodes = nil
	if b != nil && b.app != nil && b.app.configManager != nil && !reflect.DeepEqual(previous, current) {
		mcp.InitializeClientHub(b.app.configManager.GetConfigDir())
		b.app.configManager.BumpSnapshotTime()
		go b.app.configManager.AutoSync()
	}
	return nil
}

func (b *AIBindings) GetAIProviderState() ai.AIProviderState {
	return b.runtime().GetAIProviderState()
}

func (b *AIBindings) GetAIProviderPromptCachePolicy(modelID string) ai.AIProviderPromptCachePolicy {
	return b.runtime().GetAIProviderPromptCachePolicy(modelID)
}

func (b *AIBindings) SaveAIProviderState(jsonStr string) error {
	previous := b.runtime().GetAIProviderState()
	if err := b.runtime().SaveAIProviderState(jsonStr); err != nil {
		return err
	}
	current := b.runtime().GetAIProviderState()
	previousProviders := append([]ai.AIProviderProfile(nil), previous.Providers...)
	currentProviders := append([]ai.AIProviderProfile(nil), current.Providers...)
	for i := range previousProviders {
		previousProviders[i].UpdatedAt = 0
	}
	for i := range currentProviders {
		currentProviders[i].UpdatedAt = 0
	}
	if b != nil && b.app != nil && b.app.configManager != nil && !config.AIProvidersEqual(previousProviders, currentProviders) {
		b.app.configManager.BumpSnapshotTime()
		go b.app.configManager.AutoSync()
	}
	return nil
}

func (b *AIBindings) ValidateAIProviderWebSearch(jsonStr string) ai.AIProviderWebSearchValidationResult {
	return b.runtime().ValidateAIProviderWebSearch(jsonStr)
}

func (b *AIBindings) RequestAIProviderModels(baseURL string, apiKey string) ([]string, error) {
	return b.runtime().RequestAIProviderModels(baseURL, apiKey)
}

func (b *AIBindings) RequestAIProviderModelsWithProfile(jsonStr string) ([]string, error) {
	return b.runtime().RequestAIProviderModelsWithProfile(jsonStr)
}

type aiSSHDelegate struct {
	manager *sshmanager.SSHManager
}

func (d aiSSHDelegate) ExecuteCommandInTerminalControlled(sessionID string, command string, purpose string, isMutating bool, cwd string, shellType string, timeout time.Duration, control <-chan ai.ToolExecutionAction, reassign <-chan string, onCommandQueued func(), onCommandStarted func(), onCommandOutput func(string)) (mcpserver.CommandExecutionResult, ai.ToolExecutionAction, error) {
	if d.manager == nil {
		return mcpserver.CommandExecutionResult{}, ai.ToolExecutionActionNone, fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.ExecuteCommandInTerminalControlled(sessionID, command, purpose, isMutating, cwd, shellType, timeout, control, reassign, onCommandQueued, onCommandStarted, onCommandOutput)
}

func (d aiSSHDelegate) ListSiblingTerminalCandidates(sessionID string) ([]ai.AIChatCommandTerminalCandidate, error) {
	if d.manager == nil {
		return nil, fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.ListSiblingTerminalCandidates(sessionID)
}

func (d aiSSHDelegate) ListDirContext(ctx context.Context, sessionID string, remotePath string) ([]map[string]interface{}, error) {
	if d.manager == nil {
		return nil, fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.ListDirContext(ctx, sessionID, remotePath)
}

func (d aiSSHDelegate) ReadFileContext(ctx context.Context, sessionID string, remotePath string) (string, error) {
	if d.manager == nil {
		return "", fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.ReadFileContext(ctx, sessionID, remotePath)
}

func (d aiSSHDelegate) WriteFileContext(ctx context.Context, sessionID string, remotePath string, content string) error {
	if d.manager == nil {
		return fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.WriteFileContext(ctx, sessionID, remotePath, content)
}

func (d aiSSHDelegate) DeleteItemContext(ctx context.Context, sessionID string, remotePath string, isDir bool) error {
	if d.manager == nil {
		return fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.DeleteItemContext(ctx, sessionID, remotePath, isDir)
}

func (d aiSSHDelegate) CopyItemContext(ctx context.Context, sessionID string, srcPath string, dstPath string) error {
	if d.manager == nil {
		return fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.CopyItemContext(ctx, sessionID, srcPath, dstPath)
}

func (d aiSSHDelegate) MoveItemContext(ctx context.Context, sessionID string, srcPath string, dstPath string) error {
	if d.manager == nil {
		return fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.MoveItemContext(ctx, sessionID, srcPath, dstPath)
}

func (d aiSSHDelegate) MkdirContext(ctx context.Context, sessionID string, remotePath string) error {
	if d.manager == nil {
		return fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.MkdirContext(ctx, sessionID, remotePath)
}

func (d aiSSHDelegate) TransferFileContext(ctx context.Context, sessionID string, request mcpserver.TransferFileRequest) (mcpserver.TransferTaskSnapshot, error) {
	if d.manager == nil {
		return mcpserver.TransferTaskSnapshot{}, fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.TransferFileContext(ctx, sessionID, request)
}

func (d aiSSHDelegate) ListTransfersContext(ctx context.Context, sessionID string) ([]mcpserver.TransferTaskSnapshot, error) {
	if d.manager == nil {
		return nil, fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.ListTransfersContext(ctx, sessionID)
}

func (d aiSSHDelegate) BridgeGetClientEntry(sessionID string) (*ssh.Client, *sftp.Client, error) {
	if d.manager == nil {
		return nil, nil, fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.GetClientEntry(sessionID)
}

func (d aiSSHDelegate) BridgeExecuteCmdWithClientContext(ctx context.Context, client *ssh.Client, command string) (string, error) {
	if d.manager == nil {
		return "", fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.ExecuteCmdWithClientContext(ctx, client, command)
}

func (d aiSSHDelegate) BridgeGetSFTPClient(sessionID string) (*sftp.Client, error) {
	if d.manager == nil {
		return nil, fmt.Errorf("ssh manager unavailable")
	}
	return d.manager.GetSFTPClient(sessionID)
}
