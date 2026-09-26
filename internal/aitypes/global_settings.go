// Package aitypes holds pure AI-related data types and their normalization /
// persistence helpers. It exists so that packages like config and sshmanager
// can reference these types without depending on the heavyweight ai package.
package aitypes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

type AISlashCommand struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

type AICollaborationPromptPreset struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

type AISystemPromptPreset struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

type AIProxyNode struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

type AIGlobalSettings struct {
	CurrentProviderID                    string                        `json:"currentProviderId"`
	AutoApprovalEnabled                  bool                          `json:"autoApprovalEnabled"`
	AlwaysAllowReadOnly                  bool                          `json:"alwaysAllowReadOnly"`
	AlwaysAllowReadOnlyOutsideWorkspace  bool                          `json:"alwaysAllowReadOnlyOutsideWorkspace"`
	AlwaysAllowWrite                     bool                          `json:"alwaysAllowWrite"`
	AlwaysAllowWriteOutsideWorkspace     bool                          `json:"alwaysAllowWriteOutsideWorkspace"`
	AlwaysAllowWriteProtected            bool                          `json:"alwaysAllowWriteProtected"`
	AlwaysAllowExecute                   bool                          `json:"alwaysAllowExecute"`
	ExecuteApprovalMode                  string                        `json:"executeApprovalMode"`
	AllowedCommands                      []string                      `json:"allowedCommands,omitempty"`
	DeniedCommands                       []string                      `json:"deniedCommands,omitempty"`
	SlashCommands                        []AISlashCommand              `json:"slashCommands,omitempty"`
	CollaborationPromptPresets           []AICollaborationPromptPreset `json:"collaborationPromptPresets,omitempty"`
	SystemPromptPresets                  []AISystemPromptPreset        `json:"systemPromptPresets,omitempty"`
	CollaborationExtraPrompt             string                        `json:"collaborationExtraPrompt,omitempty"`
	AlwaysAllowMcp                       bool                          `json:"alwaysAllowMcp"`
	AlwaysAllowModeSwitch                bool                          `json:"alwaysAllowModeSwitch"`
	AlwaysAllowSubtasks                  bool                          `json:"alwaysAllowSubtasks"`
	AlwaysAllowFollowupQuestions         bool                          `json:"alwaysAllowFollowupQuestions"`
	SoundEnabled                         bool                          `json:"soundEnabled"`
	SoundVolume                          float64                       `json:"soundVolume,omitempty"`
	MCPEnabled                           bool                          `json:"mcpEnabled"`
	MCPAllowBrowserCalls                 bool                          `json:"mcpAllowBrowserCalls"`
	MCPRequireApproval                   bool                          `json:"mcpRequireApproval"`
	MCPActivityVisible                   bool                          `json:"mcpActivityVisible"`
	// MCPTerminalFollowLatest 控制外部 MCP 会话解析是否自动跟随同服务器最新终端。
	// 旧配置缺少该键时,LoadAIGlobalSettings 会保留默认值(true),即默认跟随。
	MCPTerminalFollowLatest              bool                          `json:"mcpTerminalFollowLatest"`
	TerminalIsolation                    bool                          `json:"terminalIsolation"`
	ConfirmDelete                        bool                          `json:"confirmDelete"`
	ContinueAfterToolRejection           bool                          `json:"continueAfterToolRejection"`
	ConversationAutoBackupEnabled        bool                          `json:"conversationAutoBackupEnabled"`
	ConversationAutoBackupRetentionCount int                           `json:"conversationAutoBackupRetentionCount,omitempty"`
	MessageNavEnabled                    bool                          `json:"messageNavEnabled"`
	AIWorkspaceTabNumbersOnly            bool                          `json:"aiWorkspaceTabNumbersOnly"`
	ApprovalButtonOrder                  string                        `json:"approvalButtonOrder"`
	CommandActionButtonOrder             string                        `json:"commandActionButtonOrder"`
	ToolResultTokenThreshold             int                           `json:"toolResultTokenThreshold,omitempty"`
	AIRequestProxyID                     string                        `json:"aiRequestProxyId,omitempty"`
	UpdatedAt                            int64                         `json:"updatedAt,omitempty"`
	ProxyNodes                           []AIProxyNode                 `json:"proxyNodes,omitempty"`
	AutoCondenseEnabled                  bool                          `json:"autoCondenseEnabled"`
	AutoCondenseThresholdRatio           float64                       `json:"autoCondenseThresholdRatio,omitempty"`
	// AIDebugLogEnabled 控制是否把每轮 AI 请求与原始响应流写入 ai.log。
	// 旧配置缺少该键时,LoadAIGlobalSettings 会保留默认值(true),即默认开启。
	AIDebugLogEnabled bool `json:"aiDebugLogEnabled"`
}

const (
	DefaultAIConversationAutoBackupRetentionCount = 30
	MaxAIConversationAutoBackupRetentionCount     = 200
	// DefaultAICondenseThresholdRatio 是自动压缩的默认触发阈值（上下文占模型窗口的比例）。
	DefaultAICondenseThresholdRatio = 0.8
	MinAICondenseThresholdRatio     = 0.3
	MaxAICondenseThresholdRatio     = 0.95
)

func DefaultAIGlobalSettings() AIGlobalSettings {
	return AIGlobalSettings{
		SoundEnabled:                         true,
		SoundVolume:                          0.06,
		MCPEnabled:                           true,
		MCPAllowBrowserCalls:                 false,
		MCPRequireApproval:                   false,
		MCPActivityVisible:                   false,
		MCPTerminalFollowLatest:              true,
		TerminalIsolation:                    true,
		ConfirmDelete:                        true,
		ContinueAfterToolRejection:           true,
		ConversationAutoBackupEnabled:        true,
		ConversationAutoBackupRetentionCount: DefaultAIConversationAutoBackupRetentionCount,
		MessageNavEnabled:                    true,
		AIWorkspaceTabNumbersOnly:            false,
		ApprovalButtonOrder:                  "reject-approve",
		CommandActionButtonOrder:             "terminate-continue",
		ToolResultTokenThreshold:             350000,
		AutoCondenseEnabled:                  false,
		AutoCondenseThresholdRatio:           DefaultAICondenseThresholdRatio,
		AIDebugLogEnabled:                    true,
	}
}

func IsValidAISlashCommandName(value string) bool {
	if value == "" {
		return false
	}
	for _, currentRune := range value {
		if currentRune >= 'a' && currentRune <= 'z' {
			continue
		}
		if currentRune >= 'A' && currentRune <= 'Z' {
			continue
		}
		if currentRune >= '0' && currentRune <= '9' {
			continue
		}
		if currentRune == '.' || currentRune == '_' || currentRune == '-' {
			continue
		}
		return false
	}
	return true
}

func NormalizeAICollaborationPromptPresets(presets []AICollaborationPromptPreset) []AICollaborationPromptPreset {
	if presets == nil {
		return []AICollaborationPromptPreset{}
	}
	normalized := make([]AICollaborationPromptPreset, 0, len(presets))
	seen := make(map[string]struct{}, len(presets))
	for index, preset := range presets {
		text := strings.TrimSpace(strings.ReplaceAll(preset.Text, "\r\n", "\n"))
		if text == "" {
			continue
		}
		id := strings.TrimSpace(preset.ID)
		if id == "" {
			id = fmt.Sprintf("collab-preset-%d-%d", time.Now().UnixMilli(), index+1)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		title := strings.TrimSpace(preset.Title)
		if title == "" {
			title = text
		}
		seen[id] = struct{}{}
		normalized = append(normalized, AICollaborationPromptPreset{
			ID:    id,
			Title: title,
			Text:  text,
		})
	}
	return normalized
}

func NormalizeAISystemPromptPresets(presets []AISystemPromptPreset) []AISystemPromptPreset {
	if presets == nil {
		return []AISystemPromptPreset{}
	}
	normalized := make([]AISystemPromptPreset, 0, len(presets))
	seen := make(map[string]struct{}, len(presets))
	for index, preset := range presets {
		text := strings.TrimSpace(strings.ReplaceAll(preset.Text, "\r\n", "\n"))
		if text == "" {
			continue
		}
		id := strings.TrimSpace(preset.ID)
		if id == "" {
			id = fmt.Sprintf("system-prompt-preset-%d-%d", time.Now().UnixMilli(), index+1)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		title := strings.TrimSpace(preset.Title)
		if title == "" {
			title = text
		}
		seen[id] = struct{}{}
		normalized = append(normalized, AISystemPromptPreset{
			ID:    id,
			Title: title,
			Text:  text,
		})
	}
	return normalized
}

func NormalizeAISlashCommands(commands []AISlashCommand) []AISlashCommand {
	if commands == nil {
		return []AISlashCommand{}
	}
	normalized := make([]AISlashCommand, 0, len(commands))
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		name := strings.TrimSpace(strings.TrimPrefix(command.Name, "/"))
		prompt := strings.TrimSpace(strings.ReplaceAll(command.Prompt, "\r\n", "\n"))
		if !IsValidAISlashCommandName(name) || prompt == "" {
			continue
		}
		dedupeKey := strings.ToLower(name)
		if _, exists := seen[dedupeKey]; exists {
			continue
		}
		seen[dedupeKey] = struct{}{}
		normalized = append(normalized, AISlashCommand{
			Name:   name,
			Prompt: prompt,
		})
	}
	return normalized
}

func NormalizeAIStringList(values []string) []string {
	if values == nil {
		return []string{}
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func NormalizeAIExecuteApprovalMode(value string) string {
	switch strings.TrimSpace(value) {
	case "read_only":
		return "read_only"
	case "all":
		return "all"
	default:
		return "basic"
	}
}

func NormalizeAIApprovalButtonOrder(value string) string {
	switch strings.TrimSpace(value) {
	case "approve-reject":
		return "approve-reject"
	default:
		return "reject-approve"
	}
}

func NormalizeAICommandActionButtonOrder(value string) string {
	switch strings.TrimSpace(value) {
	case "continue-terminate":
		return "continue-terminate"
	default:
		return "terminate-continue"
	}
}

func NormalizeAIProxyType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "http":
		return "http"
	default:
		return "socks5"
	}
}

func NormalizeAISoundVolume(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func BuildDefaultAIProxyNodeID(proxyType string, host string, port int, index int) string {
	sanitizedHost := strings.ToLower(strings.TrimSpace(host))
	sanitizedHost = strings.NewReplacer(":", "-", ".", "-", "[", "", "]", "", "/", "-", "\\", "-").Replace(sanitizedHost)
	if sanitizedHost == "" {
		sanitizedHost = fmt.Sprintf("node-%d", index+1)
	}
	return fmt.Sprintf("proxy-%s-%s-%d-%d", proxyType, sanitizedHost, port, index+1)
}

func NormalizeAIProxyNodes(nodes []AIProxyNode) []AIProxyNode {
	if nodes == nil {
		return []AIProxyNode{}
	}
	normalized := make([]AIProxyNode, 0, len(nodes))
	seen := make(map[string]struct{}, len(nodes))
	for index, node := range nodes {
		host := strings.TrimSpace(node.Host)
		if host == "" {
			continue
		}
		proxyType := NormalizeAIProxyType(node.Type)
		port := node.Port
		if port <= 0 || port > 65535 {
			port = 1080
		}
		id := strings.TrimSpace(node.ID)
		if id == "" {
			id = BuildDefaultAIProxyNodeID(proxyType, host, port, index)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		updatedAt := node.UpdatedAt
		if updatedAt <= 0 {
			updatedAt = time.Now().UnixMilli()
		}
		seen[id] = struct{}{}
		normalized = append(normalized, AIProxyNode{
			ID:        id,
			Name:      strings.TrimSpace(node.Name),
			Type:      proxyType,
			Host:      host,
			Port:      port,
			Username:  strings.TrimSpace(node.Username),
			Password:  node.Password,
			UpdatedAt: updatedAt,
		})
	}
	return normalized
}

func NormalizeAIRequestProxyID(selectedID string, nodes []AIProxyNode) string {
	trimmedID := strings.TrimSpace(selectedID)
	if trimmedID == "" {
		return ""
	}
	for _, node := range nodes {
		if node.ID == trimmedID {
			return trimmedID
		}
	}
	return ""
}

func AIProxyNodesPathForConfigDir(configDir string) string {
	return filepath.Join(configDir, "proxy_nodes.json")
}

func LoadAIProxyNodesFromPath(path string) ([]AIProxyNode, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var nodes []AIProxyNode
	if err := json.Unmarshal(data, &nodes); err != nil {
		return []AIProxyNode{}, true
	}
	return NormalizeAIProxyNodes(nodes), true
}

func LoadAIProxyNodes(configDir string) []AIProxyNode {
	if strings.TrimSpace(configDir) == "" {
		return []AIProxyNode{}
	}
	if nodes, ok := LoadAIProxyNodesFromPath(AIProxyNodesPathForConfigDir(configDir)); ok {
		return nodes
	}
	return []AIProxyNode{}
}

func NormalizeAIGlobalSettings(settings AIGlobalSettings) AIGlobalSettings {
	settings.CurrentProviderID = strings.TrimSpace(settings.CurrentProviderID)
	settings.SlashCommands = NormalizeAISlashCommands(settings.SlashCommands)
	settings.CollaborationPromptPresets = NormalizeAICollaborationPromptPresets(settings.CollaborationPromptPresets)
	settings.SystemPromptPresets = NormalizeAISystemPromptPresets(settings.SystemPromptPresets)
	settings.CollaborationExtraPrompt = strings.TrimSpace(strings.ReplaceAll(settings.CollaborationExtraPrompt, "\r\n", "\n"))
	settings.AllowedCommands = NormalizeAIStringList(settings.AllowedCommands)
	settings.DeniedCommands = NormalizeAIStringList(settings.DeniedCommands)
	settings.ExecuteApprovalMode = NormalizeAIExecuteApprovalMode(settings.ExecuteApprovalMode)
	settings.AutoApprovalEnabled = settings.AlwaysAllowReadOnly || settings.AlwaysAllowWrite || settings.AlwaysAllowExecute
	settings.ApprovalButtonOrder = NormalizeAIApprovalButtonOrder(settings.ApprovalButtonOrder)
	settings.CommandActionButtonOrder = NormalizeAICommandActionButtonOrder(settings.CommandActionButtonOrder)
	settings.SoundVolume = NormalizeAISoundVolume(settings.SoundVolume)
	if settings.UpdatedAt <= 0 {
		settings.UpdatedAt = time.Now().UnixMilli()
	}
	settings.ProxyNodes = NormalizeAIProxyNodes(settings.ProxyNodes)
	settings.AIRequestProxyID = NormalizeAIRequestProxyID(settings.AIRequestProxyID, settings.ProxyNodes)
	if settings.ToolResultTokenThreshold <= 0 {
		settings.ToolResultTokenThreshold = DefaultAIGlobalSettings().ToolResultTokenThreshold
	}
	if settings.AutoCondenseThresholdRatio <= 0 {
		settings.AutoCondenseThresholdRatio = DefaultAICondenseThresholdRatio
	}
	if settings.AutoCondenseThresholdRatio < MinAICondenseThresholdRatio {
		settings.AutoCondenseThresholdRatio = MinAICondenseThresholdRatio
	}
	if settings.AutoCondenseThresholdRatio > MaxAICondenseThresholdRatio {
		settings.AutoCondenseThresholdRatio = MaxAICondenseThresholdRatio
	}
	if settings.ConversationAutoBackupRetentionCount <= 0 {
		settings.ConversationAutoBackupRetentionCount = DefaultAIConversationAutoBackupRetentionCount
	}
	if settings.ConversationAutoBackupRetentionCount > MaxAIConversationAutoBackupRetentionCount {
		settings.ConversationAutoBackupRetentionCount = MaxAIConversationAutoBackupRetentionCount
	}
	// 审批依赖活动弹窗（审批按钮位于弹窗内），开启审批时强制开启弹窗，避免审批静默超时
	if settings.MCPRequireApproval && !settings.MCPActivityVisible {
		settings.MCPActivityVisible = true
	}
	return settings
}

func AIGlobalSettingsContentEqual(a, b AIGlobalSettings) bool {
	a.ProxyNodes = nil
	b.ProxyNodes = nil
	a.UpdatedAt = 0
	b.UpdatedAt = 0
	return reflect.DeepEqual(a, b)
}

func LoadAIGlobalSettings(configDir string) AIGlobalSettings {
	settings := DefaultAIGlobalSettings()
	if strings.TrimSpace(configDir) == "" {
		return settings
	}
	data, err := os.ReadFile(filepath.Join(configDir, "ai_global_settings.json"))
	if err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	settings.ProxyNodes = LoadAIProxyNodes(configDir)
	return NormalizeAIGlobalSettings(settings)
}
