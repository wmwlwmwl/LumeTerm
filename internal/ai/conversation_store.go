package ai

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type AIConversationTaskSettings struct {
	CurrentProviderID                   string   `json:"currentProviderId"`
	AutoApprovalEnabled                 bool     `json:"autoApprovalEnabled"`
	AlwaysAllowReadOnly                 bool     `json:"alwaysAllowReadOnly"`
	AlwaysAllowReadOnlyOutsideWorkspace bool     `json:"alwaysAllowReadOnlyOutsideWorkspace"`
	AlwaysAllowWrite                    bool     `json:"alwaysAllowWrite"`
	AlwaysAllowWriteOutsideWorkspace    bool     `json:"alwaysAllowWriteOutsideWorkspace"`
	AlwaysAllowWriteProtected           bool     `json:"alwaysAllowWriteProtected"`
	AlwaysAllowExecute                  bool     `json:"alwaysAllowExecute"`
	ExecuteApprovalMode                 string   `json:"executeApprovalMode"`
	AllowedCommands                     []string `json:"allowedCommands,omitempty"`
	DeniedCommands                      []string `json:"deniedCommands,omitempty"`
	AlwaysAllowMcp                      bool     `json:"alwaysAllowMcp"`
	AlwaysAllowModeSwitch               bool     `json:"alwaysAllowModeSwitch"`
	AlwaysAllowSubtasks                 bool     `json:"alwaysAllowSubtasks"`
	AlwaysAllowFollowupQuestions        bool     `json:"alwaysAllowFollowupQuestions"`
	CollaborationExtraPrompt            string   `json:"collaborationExtraPrompt,omitempty"`
}

type AIConversationFollowUpOption struct {
	ID          string `json:"id,omitempty"`
	Answer      string `json:"answer,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

type AIConversationFollowUpQuestion struct {
	ID      string                         `json:"id,omitempty"`
	Text    string                         `json:"text,omitempty"`
	Type    string                         `json:"type,omitempty"`
	Options []AIConversationFollowUpOption `json:"options,omitempty"`
}

type AIConversationMessage struct {
	ID                 string                           `json:"id,omitempty"`
	TurnID             string                           `json:"turnId,omitempty"`
	Kind               string                           `json:"kind"`
	Text               string                           `json:"text,omitempty"`
	Time               string                           `json:"time,omitempty"`
	Metrics            []string                         `json:"metrics,omitempty"`
	Streaming          bool                             `json:"streaming,omitempty"`
	Duration           string                           `json:"duration,omitempty"`
	ActionLabel        string                           `json:"actionLabel,omitempty"`
	Title              string                           `json:"title,omitempty"`
	Summary            string                           `json:"summary,omitempty"`
	Code               string                           `json:"code,omitempty"`
	Status             string                           `json:"status,omitempty"`
	Result             string                           `json:"result,omitempty"`
	RemainingFileEdits int                              `json:"remainingFileEdits,omitempty"`
	Purpose            string                           `json:"purpose,omitempty"`
	Command            string                           `json:"command,omitempty"`
	Output             string                           `json:"output,omitempty"`
	Images             []string                         `json:"images,omitempty"`
	ServerName         string                           `json:"serverName,omitempty"`
	ToolName           string                           `json:"toolName,omitempty"`
	Args               string                           `json:"args,omitempty"`
	Response           string                           `json:"response,omitempty"`
	RequestID          string                           `json:"requestId,omitempty"`
	Question           string                           `json:"question,omitempty"`
	Questions          []AIConversationFollowUpQuestion `json:"questions,omitempty"`
	Suggestions        []string                         `json:"suggestions,omitempty"`
	Extra              map[string]interface{}           `json:"extra,omitempty"`
}

type AIConversationOpenAIResponsesCacheObject struct {
	ResponseID  string           `json:"responseId,omitempty"`
	Output      []map[string]any `json:"output,omitempty"`
	ReplayState map[string]any   `json:"replayState,omitempty"`
	Include     []string         `json:"include,omitempty"`
	Store       bool             `json:"store,omitempty"`
	CapturedAt  int64            `json:"capturedAt,omitempty"`
}

type AIConversationProviderCacheObjects struct {
	OpenAIResponses *AIConversationOpenAIResponsesCacheObject `json:"openaiResponses,omitempty"`
}

type AIConversationAPIMessage struct {
	Role          string                              `json:"role"`
	Content       string                              `json:"content"`
	ContentBlocks []map[string]any                    `json:"contentBlocks,omitempty"`
	MessageID     string                              `json:"messageId,omitempty"`
	UIMessageIDs  []string                            `json:"uiMessageIds,omitempty"`
	Images        []string                            `json:"images,omitempty"`
	CacheObjects  *AIConversationProviderCacheObjects `json:"cacheObjects,omitempty"`
	Ts            int64                               `json:"ts,omitempty"`
}

type AIConversationSummary struct {
	ID                         string `json:"id"`
	Title                      string `json:"title"`
	CreatedAt                  int64  `json:"createdAt"`
	UpdatedAt                  int64  `json:"updatedAt"`
	Status                     string `json:"status"`
	ToolProtocol               string `json:"toolProtocol"`
	MessageCount               int    `json:"messageCount"`
	PromptCacheBypassTimestamp string `json:"promptCacheBypassTimestamp,omitempty"`
	ParentConversationID       string `json:"parentConversationId,omitempty"`
	RootConversationID         string `json:"rootConversationId,omitempty"`
	RelationType               string `json:"relationType,omitempty"`
	RelationSource             string `json:"relationSource,omitempty"`
	ParentTitleSnapshot        string `json:"parentTitleSnapshot,omitempty"`
	Archived                   bool   `json:"archived,omitempty"`
	Transient                  bool   `json:"transient,omitempty"`
	UpstreamInputTokens        int    `json:"upstreamInputTokens,omitempty"`
	UpstreamOutputTokens       int    `json:"upstreamOutputTokens,omitempty"`
}

type AIConversationSnapshot struct {
	ID                         string                     `json:"id"`
	Title                      string                     `json:"title"`
	CreatedAt                  int64                      `json:"createdAt"`
	UpdatedAt                  int64                      `json:"updatedAt"`
	Status                     string                     `json:"status"`
	ToolProtocol               string                     `json:"toolProtocol"`
	PromptCacheBypassTimestamp string                     `json:"promptCacheBypassTimestamp,omitempty"`
	ParentConversationID       string                     `json:"parentConversationId,omitempty"`
	RootConversationID         string                     `json:"rootConversationId,omitempty"`
	RelationType               string                     `json:"relationType,omitempty"`
	RelationSource             string                     `json:"relationSource,omitempty"`
	ParentTitleSnapshot        string                     `json:"parentTitleSnapshot,omitempty"`
	Archived                   bool                       `json:"archived,omitempty"`
	Transient                  bool                       `json:"transient,omitempty"`
	UpstreamInputTokens        int                        `json:"upstreamInputTokens,omitempty"`
	UpstreamOutputTokens       int                        `json:"upstreamOutputTokens,omitempty"`
	Messages                   []AIConversationMessage    `json:"messages"`
	APIMessages                []AIConversationAPIMessage `json:"apiMessages"`
	Settings                   AIConversationTaskSettings `json:"settings"`
}

func defaultAIConversationTaskSettings(globalSettings AIGlobalSettings) AIConversationTaskSettings {
	return AIConversationTaskSettings{
		CurrentProviderID:                   strings.TrimSpace(globalSettings.CurrentProviderID),
		AutoApprovalEnabled:                 globalSettings.AutoApprovalEnabled,
		AlwaysAllowReadOnly:                 globalSettings.AlwaysAllowReadOnly,
		AlwaysAllowReadOnlyOutsideWorkspace: globalSettings.AlwaysAllowReadOnlyOutsideWorkspace,
		AlwaysAllowWrite:                    globalSettings.AlwaysAllowWrite,
		AlwaysAllowWriteOutsideWorkspace:    globalSettings.AlwaysAllowWriteOutsideWorkspace,
		AlwaysAllowWriteProtected:           globalSettings.AlwaysAllowWriteProtected,
		AlwaysAllowExecute:                  globalSettings.AlwaysAllowExecute,
		ExecuteApprovalMode:                 globalSettings.ExecuteApprovalMode,
		AlwaysAllowMcp:                      globalSettings.AlwaysAllowMcp,
		AlwaysAllowModeSwitch:               globalSettings.AlwaysAllowModeSwitch,
		AlwaysAllowSubtasks:                 globalSettings.AlwaysAllowSubtasks,
		AlwaysAllowFollowupQuestions:        globalSettings.AlwaysAllowFollowupQuestions,
		CollaborationExtraPrompt:            strings.TrimSpace(globalSettings.CollaborationExtraPrompt),
	}
}

func normalizeAIConversationTaskSettings(settings AIConversationTaskSettings) AIConversationTaskSettings {
	settings.CurrentProviderID = strings.TrimSpace(settings.CurrentProviderID)
	settings.CollaborationExtraPrompt = strings.TrimSpace(strings.ReplaceAll(settings.CollaborationExtraPrompt, "\r\n", "\n"))
	settings.AllowedCommands = normalizeAIStringList(settings.AllowedCommands)
	settings.DeniedCommands = normalizeAIStringList(settings.DeniedCommands)
	settings.ExecuteApprovalMode = normalizeAIExecuteApprovalMode(settings.ExecuteApprovalMode)
	settings.AutoApprovalEnabled = settings.AlwaysAllowReadOnly || settings.AlwaysAllowWrite || settings.AlwaysAllowExecute
	return settings
}

func normalizeAIConversationFollowUpOptions(options []AIConversationFollowUpOption, questionID string) []AIConversationFollowUpOption {
	if options == nil {
		return []AIConversationFollowUpOption{}
	}
	normalized := make([]AIConversationFollowUpOption, 0, len(options))
	for index, option := range options {
		answer := strings.TrimSpace(option.Answer)
		if answer == "" {
			continue
		}
		optionID := fmt.Sprintf("%s-option-%d", questionID, index+1)
		normalized = append(normalized, AIConversationFollowUpOption{
			ID:          optionID,
			Answer:      answer,
			Mode:        strings.TrimSpace(option.Mode),
			Disabled:    option.Disabled,
			Recommended: option.Recommended,
		})
	}
	return normalized
}

func normalizeAIConversationFollowUpQuestions(questions []AIConversationFollowUpQuestion, fallbackQuestion string) []AIConversationFollowUpQuestion {
	if questions == nil {
		return []AIConversationFollowUpQuestion{}
	}
	normalized := make([]AIConversationFollowUpQuestion, 0, len(questions))
	for index, question := range questions {
		questionID := fmt.Sprintf("question-%d", index+1)
		questionText := strings.TrimSpace(question.Text)
		if questionText == "" {
			if index == 0 && strings.TrimSpace(fallbackQuestion) != "" {
				questionText = strings.TrimSpace(fallbackQuestion)
			} else {
				questionText = fmt.Sprintf("Question %d", index+1)
			}
		}
		questionType := strings.ToLower(strings.TrimSpace(question.Type))
		switch questionType {
		case "multiple", "multi_select":
			questionType = "multiple"
		case "free_text", "text":
			questionType = "free_text"
		default:
			questionType = "single"
		}
		options := normalizeAIConversationFollowUpOptions(question.Options, questionID)
		if questionType != "free_text" && len(options) == 0 {
			continue
		}
		normalized = append(normalized, AIConversationFollowUpQuestion{
			ID:      questionID,
			Text:    questionText,
			Type:    questionType,
			Options: options,
		})
	}
	return normalized
}

func normalizeAIConversationMessages(messages []AIConversationMessage) []AIConversationMessage {
	if messages == nil {
		return []AIConversationMessage{}
	}
	normalized := make([]AIConversationMessage, 0, len(messages))
	for _, message := range messages {
		if strings.TrimSpace(message.Kind) == "" {
			continue
		}
		message.ID = strings.TrimSpace(message.ID)
		message.TurnID = strings.TrimSpace(message.TurnID)
		message.Kind = strings.TrimSpace(message.Kind)
		message.Text = strings.TrimSpace(message.Text)
		message.Time = strings.TrimSpace(message.Time)
		message.Duration = strings.TrimSpace(message.Duration)
		message.ActionLabel = strings.TrimSpace(message.ActionLabel)
		message.Title = strings.TrimSpace(message.Title)
		message.Summary = strings.TrimSpace(message.Summary)
		message.Code = strings.TrimSpace(message.Code)
		message.Status = strings.TrimSpace(message.Status)
		message.Result = strings.TrimSpace(message.Result)
		if message.RemainingFileEdits < 0 {
			message.RemainingFileEdits = 0
		}
		message.Purpose = strings.TrimSpace(message.Purpose)
		message.Command = strings.TrimSpace(message.Command)
		message.Output = strings.TrimSpace(message.Output)
		message.Images = normalizeAIStringList(message.Images)
		message.ServerName = strings.TrimSpace(message.ServerName)
		message.ToolName = strings.TrimSpace(message.ToolName)
		message.Args = strings.TrimSpace(message.Args)
		message.Response = strings.TrimSpace(message.Response)
		message.RequestID = strings.TrimSpace(message.RequestID)
		message.Question = strings.TrimSpace(message.Question)
		message.Questions = normalizeAIConversationFollowUpQuestions(message.Questions, message.Question)
		if message.Suggestions == nil {
			message.Suggestions = []string{}
		} else {
			suggestions := make([]string, 0, len(message.Suggestions))
			for _, item := range message.Suggestions {
				trimmedSuggestion := strings.TrimSpace(item)
				if trimmedSuggestion == "" {
					continue
				}
				suggestions = append(suggestions, trimmedSuggestion)
			}
			message.Suggestions = suggestions
		}
		normalized = append(normalized, message)
	}
	return normalized
}

func cloneAIConversationOpenAIResponsesOutputItems(items []map[string]any) []map[string]any {
	if len(items) == 0 {
		return []map[string]any{}
	}
	data, err := json.Marshal(items)
	if err != nil {
		cloned := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if item == nil {
				continue
			}
			copied := make(map[string]any, len(item))
			for key, value := range item {
				copied[key] = value
			}
			cloned = append(cloned, copied)
		}
		return cloned
	}
	var cloned []map[string]any
	if err := json.Unmarshal(data, &cloned); err != nil {
		return []map[string]any{}
	}
	if cloned == nil {
		return []map[string]any{}
	}
	return cloned
}

func cloneAIConversationOpenAIResponsesReplayState(state map[string]any) map[string]any {
	if len(state) == 0 {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil
	}
	var cloned map[string]any
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func normalizeAIConversationOpenAIResponsesCacheObject(cache *AIConversationOpenAIResponsesCacheObject) *AIConversationOpenAIResponsesCacheObject {
	if cache == nil {
		return nil
	}
	normalizedOutput := cloneAIConversationOpenAIResponsesOutputItems(cache.Output)
	normalizedReplayState := cloneAIConversationOpenAIResponsesReplayState(cache.ReplayState)
	normalizedInclude := normalizeAIStringList(cache.Include)
	if strings.TrimSpace(cache.ResponseID) == "" && len(normalizedOutput) == 0 && len(normalizedReplayState) == 0 && len(normalizedInclude) == 0 && cache.CapturedAt == 0 && !cache.Store {
		return nil
	}
	return &AIConversationOpenAIResponsesCacheObject{
		ResponseID:  strings.TrimSpace(cache.ResponseID),
		Output:      normalizedOutput,
		ReplayState: normalizedReplayState,
		Include:     normalizedInclude,
		Store:       cache.Store,
		CapturedAt:  cache.CapturedAt,
	}
}

func normalizeAIConversationProviderCacheObjects(cacheObjects *AIConversationProviderCacheObjects) *AIConversationProviderCacheObjects {
	if cacheObjects == nil {
		return nil
	}
	normalized := &AIConversationProviderCacheObjects{
		OpenAIResponses: normalizeAIConversationOpenAIResponsesCacheObject(cacheObjects.OpenAIResponses),
	}
	if normalized.OpenAIResponses == nil {
		return nil
	}
	return normalized
}

func normalizeAIConversationAPIMessages(messages []AIConversationAPIMessage) []AIConversationAPIMessage {
	if messages == nil {
		return []AIConversationAPIMessage{}
	}
	normalized := make([]AIConversationAPIMessage, 0, len(messages))
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "system" && role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(message.Content)
		contentBlocks := cloneAIConversationOpenAIResponsesOutputItems(message.ContentBlocks)
		images := normalizeAIStringList(message.Images)
		cacheObjects := normalizeAIConversationProviderCacheObjects(message.CacheObjects)
		if content == "" && len(contentBlocks) == 0 && len(images) == 0 && cacheObjects == nil {
			continue
		}
		uiMessageIDs := make([]string, 0, len(message.UIMessageIDs))
		seen := make(map[string]struct{}, len(message.UIMessageIDs))
		for _, uiMessageID := range message.UIMessageIDs {
			trimmedUIMessageID := strings.TrimSpace(uiMessageID)
			if trimmedUIMessageID == "" {
				continue
			}
			if _, exists := seen[trimmedUIMessageID]; exists {
				continue
			}
			seen[trimmedUIMessageID] = struct{}{}
			uiMessageIDs = append(uiMessageIDs, trimmedUIMessageID)
		}
		normalized = append(normalized, AIConversationAPIMessage{
			Role:          role,
			Content:       content,
			ContentBlocks: contentBlocks,
			MessageID:     strings.TrimSpace(message.MessageID),
			UIMessageIDs:  uiMessageIDs,
			Images:        images,
			CacheObjects:  cacheObjects,
			Ts:            message.Ts,
		})
	}
	return normalized
}

func formatAIPromptCacheBypassTimestamp(ts time.Time) string {
	return ts.Format("2006-01-02 15:04:05")
}

func normalizeAIPromptCacheBypassTimestamp(value string) string {
	return strings.TrimSpace(value)
}

func normalizeAIConversationSummary(summary AIConversationSummary) AIConversationSummary {
	summary.ID = strings.TrimSpace(summary.ID)
	summary.Title = strings.TrimSpace(summary.Title)
	if summary.Title == "" {
		summary.Title = "新对话"
	}
	if summary.Status == "" {
		summary.Status = "idle"
	}
	if summary.ToolProtocol == "" {
		summary.ToolProtocol = "xml"
	}
	summary.PromptCacheBypassTimestamp = normalizeAIPromptCacheBypassTimestamp(summary.PromptCacheBypassTimestamp)
	summary.ParentConversationID = strings.TrimSpace(summary.ParentConversationID)
	summary.RootConversationID = strings.TrimSpace(summary.RootConversationID)
	summary.RelationType = normalizeAIConversationRelationType(summary.RelationType)
	summary.RelationSource = normalizeAIConversationRelationSource(summary.RelationSource)
	summary.ParentTitleSnapshot = strings.TrimSpace(summary.ParentTitleSnapshot)
	return summary
}

func normalizeAIConversationSnapshot(snapshot AIConversationSnapshot, fallbackSettings AIConversationTaskSettings) AIConversationSnapshot {
	snapshot.ID = strings.TrimSpace(snapshot.ID)
	if snapshot.ID == "" {
		snapshot.ID = aiConversationID()
	}
	snapshot.Title = strings.TrimSpace(snapshot.Title)
	if snapshot.Title == "" {
		snapshot.Title = "新对话"
	}
	if snapshot.Status == "" {
		snapshot.Status = "idle"
	}
	if snapshot.ToolProtocol == "" {
		snapshot.ToolProtocol = "xml"
	}
	snapshot.PromptCacheBypassTimestamp = normalizeAIPromptCacheBypassTimestamp(snapshot.PromptCacheBypassTimestamp)
	snapshot.ParentConversationID = strings.TrimSpace(snapshot.ParentConversationID)
	snapshot.RootConversationID = strings.TrimSpace(snapshot.RootConversationID)
	snapshot.RelationType = normalizeAIConversationRelationType(snapshot.RelationType)
	snapshot.RelationSource = normalizeAIConversationRelationSource(snapshot.RelationSource)
	snapshot.ParentTitleSnapshot = strings.TrimSpace(snapshot.ParentTitleSnapshot)
	if snapshot.UpstreamInputTokens < 0 {
		snapshot.UpstreamInputTokens = 0
	}
	if snapshot.UpstreamOutputTokens < 0 {
		snapshot.UpstreamOutputTokens = 0
	}
	if snapshot.Messages == nil {
		snapshot.Messages = []AIConversationMessage{}
	}
	snapshot.Messages = normalizeAIConversationMessages(snapshot.Messages)
	if snapshot.APIMessages == nil {
		snapshot.APIMessages = []AIConversationAPIMessage{}
	}
	snapshot.APIMessages = normalizeAIConversationAPIMessages(snapshot.APIMessages)
	if snapshot.CreatedAt == 0 {
		snapshot.CreatedAt = time.Now().UnixMilli()
	}
	if snapshot.UpdatedAt == 0 {
		snapshot.UpdatedAt = snapshot.CreatedAt
	}
	snapshot.Settings = normalizeAIConversationTaskSettings(snapshot.Settings)
	return snapshot
}

func aiConversationID() string {
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		return fmt.Sprintf("conv-%d", time.Now().UnixMilli())
	}
	return "conv-" + hex.EncodeToString(randomBytes)
}

func (c *configBridge) aiConversationsRootDir() string {
	// 读取 app_settings.json 获取自定义 tasks 目录, 失败则用默认 configDir/tasks
	settingsPath := filepath.Join(c.configDir, "app_settings.json")
	if data, err := os.ReadFile(settingsPath); err == nil {
		var s struct {
			TasksDir string `json:"tasksDir"`
		}
		if json.Unmarshal(data, &s) == nil {
			if dir := strings.TrimSpace(s.TasksDir); dir != "" {
				return dir
			}
		}
	}
	return filepath.Join(c.configDir, "tasks")
}

func (c *configBridge) aiConversationDir(conversationID string) string {
	return filepath.Join(c.aiConversationsRootDir(), conversationID)
}

func (c *configBridge) aiConversationMetadataPath(conversationID string) string {
	return filepath.Join(c.aiConversationDir(conversationID), "task_metadata.json")
}

func (c *configBridge) aiConversationMessagesPath(conversationID string) string {
	return filepath.Join(c.aiConversationDir(conversationID), "ui_messages.json")
}

func (c *configBridge) aiConversationAPIMessagesPath(conversationID string) string {
	return filepath.Join(c.aiConversationDir(conversationID), "api_conversation_history.json")
}

func (c *configBridge) aiConversationSettingsPath(conversationID string) string {
	return filepath.Join(c.aiConversationDir(conversationID), "setting.json")
}

func (c *configBridge) readAIConversationSummary(conversationID string) (AIConversationSummary, error) {
	data, err := os.ReadFile(c.aiConversationMetadataPath(conversationID))
	if err != nil {
		return AIConversationSummary{}, err
	}
	var summary AIConversationSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return AIConversationSummary{}, err
	}
	return normalizeAIConversationSummary(summary), nil
}

func (c *configBridge) readAIConversationMessages(conversationID string) []AIConversationMessage {
	data, err := os.ReadFile(c.aiConversationMessagesPath(conversationID))
	if err != nil {
		return []AIConversationMessage{}
	}
	var messages []AIConversationMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return []AIConversationMessage{}
	}
	if messages == nil {
		return []AIConversationMessage{}
	}
	return messages
}

func (c *configBridge) readAIConversationAPIMessages(conversationID string) []AIConversationAPIMessage {
	data, err := os.ReadFile(c.aiConversationAPIMessagesPath(conversationID))
	if err != nil {
		return []AIConversationAPIMessage{}
	}
	var messages []AIConversationAPIMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return []AIConversationAPIMessage{}
	}
	if messages == nil {
		return []AIConversationAPIMessage{}
	}
	return normalizeAIConversationAPIMessages(messages)
}

func (c *configBridge) readAIConversationSettings(conversationID string, fallback AIConversationTaskSettings) AIConversationTaskSettings {
	data, err := os.ReadFile(c.aiConversationSettingsPath(conversationID))
	if err != nil {
		return fallback
	}
	var settings AIConversationTaskSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return fallback
	}
	return normalizeAIConversationTaskSettings(settings)
}

func (c *configBridge) GetAIConversationTaskSettings(conversationID string) AIConversationTaskSettings {
	if c == nil {
		return AIConversationTaskSettings{}
	}
	fallback := defaultAIConversationTaskSettings(c.GetAIGlobalSettings())
	trimmedConversationID := strings.TrimSpace(conversationID)
	if trimmedConversationID == "" {
		return fallback
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.readAIConversationSettings(trimmedConversationID, fallback)
}

func (c *configBridge) GetAIConversationPromptCacheBypassTimestamp(conversationID string) string {
	if c == nil {
		return ""
	}
	trimmedConversationID := strings.TrimSpace(conversationID)
	if trimmedConversationID == "" {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	summary, err := c.readAIConversationSummary(trimmedConversationID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(summary.PromptCacheBypassTimestamp)
}

func marshalAIConversationJSON(value interface{}) ([]byte, error) {
	var builder strings.Builder
	encoder := json.NewEncoder(&builder)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSuffix(builder.String(), "\n")), nil
}

func (c *configBridge) writeAIConversationSnapshot(snapshot AIConversationSnapshot) error {
	if err := os.MkdirAll(c.aiConversationDir(snapshot.ID), 0700); err != nil {
		return err
	}

	summary := normalizeAIConversationSummary(AIConversationSummary{
		ID:                         snapshot.ID,
		Title:                      snapshot.Title,
		CreatedAt:                  snapshot.CreatedAt,
		UpdatedAt:                  snapshot.UpdatedAt,
		Status:                     snapshot.Status,
		ToolProtocol:               snapshot.ToolProtocol,
		MessageCount:               len(snapshot.Messages),
		PromptCacheBypassTimestamp: snapshot.PromptCacheBypassTimestamp,
		ParentConversationID:       snapshot.ParentConversationID,
		RootConversationID:         snapshot.RootConversationID,
		RelationType:               snapshot.RelationType,
		RelationSource:             snapshot.RelationSource,
		ParentTitleSnapshot:        snapshot.ParentTitleSnapshot,
		Archived:                   snapshot.Archived,
		Transient:                  snapshot.Transient,
		UpstreamInputTokens:        snapshot.UpstreamInputTokens,
		UpstreamOutputTokens:       snapshot.UpstreamOutputTokens,
	})

	metadataBytes, err := marshalAIConversationJSON(summary)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(c.aiConversationMetadataPath(snapshot.ID), metadataBytes, 0600); err != nil {
		return err
	}

	messageBytes, err := marshalAIConversationJSON(snapshot.Messages)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(c.aiConversationMessagesPath(snapshot.ID), messageBytes, 0600); err != nil {
		return err
	}

	apiMessageBytes, err := marshalAIConversationJSON(normalizeAIConversationAPIMessages(snapshot.APIMessages))
	if err != nil {
		return err
	}
	if err := atomicWriteFile(c.aiConversationAPIMessagesPath(snapshot.ID), apiMessageBytes, 0600); err != nil {
		return err
	}

	settingsBytes, err := marshalAIConversationJSON(normalizeAIConversationTaskSettings(snapshot.Settings))
	if err != nil {
		return err
	}
	return atomicWriteFile(c.aiConversationSettingsPath(snapshot.ID), settingsBytes, 0600)
}

func (c *configBridge) ListAIConversations() []AIConversationSummary {
	if c == nil {
		return []AIConversationSummary{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	entries, err := os.ReadDir(c.aiConversationsRootDir())
	if err != nil {
		return []AIConversationSummary{}
	}

	summaries := make([]AIConversationSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		summary, err := c.readAIConversationSummary(entry.Name())
		if err != nil {
			continue
		}
		summaries = append(summaries, summary)
	}

	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].UpdatedAt != summaries[j].UpdatedAt {
			return summaries[i].UpdatedAt > summaries[j].UpdatedAt
		}
		return summaries[i].ID > summaries[j].ID
	})

	return summaries
}

func (c *configBridge) CreateAIConversation(title string) (AIConversationSnapshot, error) {
	if c == nil {
		return AIConversationSnapshot{}, fmt.Errorf("配置管理器不可用")
	}

	globalSettings := c.GetAIGlobalSettings()
	snapshot := normalizeAIConversationSnapshot(AIConversationSnapshot{
		ID:                         aiConversationID(),
		Title:                      strings.TrimSpace(title),
		CreatedAt:                  time.Now().UnixMilli(),
		UpdatedAt:                  time.Now().UnixMilli(),
		Status:                     "idle",
		ToolProtocol:               "xml",
		PromptCacheBypassTimestamp: formatAIPromptCacheBypassTimestamp(time.Now()),
		Messages:                   []AIConversationMessage{},
		APIMessages:                []AIConversationAPIMessage{},
		Settings:                   defaultAIConversationTaskSettings(globalSettings),
	}, defaultAIConversationTaskSettings(globalSettings))

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.writeAIConversationSnapshot(snapshot); err != nil {
		return AIConversationSnapshot{}, err
	}
	return snapshot, nil
}

func (c *configBridge) GetAIConversation(conversationID string) (AIConversationSnapshot, error) {
	if c == nil {
		return AIConversationSnapshot{}, fmt.Errorf("配置管理器不可用")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return AIConversationSnapshot{}, fmt.Errorf("缺少对话 ID")
	}

	fallbackSettings := defaultAIConversationTaskSettings(c.GetAIGlobalSettings())

	c.mu.RLock()
	defer c.mu.RUnlock()

	summary, err := c.readAIConversationSummary(conversationID)
	if err != nil {
		return AIConversationSnapshot{}, err
	}

	snapshot := AIConversationSnapshot{
		ID:                         summary.ID,
		Title:                      summary.Title,
		CreatedAt:                  summary.CreatedAt,
		UpdatedAt:                  summary.UpdatedAt,
		Status:                     summary.Status,
		ToolProtocol:               summary.ToolProtocol,
		PromptCacheBypassTimestamp: summary.PromptCacheBypassTimestamp,
		ParentConversationID:       summary.ParentConversationID,
		RootConversationID:         summary.RootConversationID,
		RelationType:               summary.RelationType,
		RelationSource:             summary.RelationSource,
		ParentTitleSnapshot:        summary.ParentTitleSnapshot,
		Archived:                   summary.Archived,
		Transient:                  summary.Transient,
		UpstreamInputTokens:        summary.UpstreamInputTokens,
		UpstreamOutputTokens:       summary.UpstreamOutputTokens,
		Messages:                   c.readAIConversationMessages(conversationID),
		APIMessages:                c.readAIConversationAPIMessages(conversationID),
		Settings:                   c.readAIConversationSettings(conversationID, fallbackSettings),
	}

	return normalizeAIConversationSnapshot(snapshot, fallbackSettings), nil
}

func (c *configBridge) SaveAIConversation(snapshot AIConversationSnapshot) (AIConversationSnapshot, error) {
	if c == nil {
		return AIConversationSnapshot{}, fmt.Errorf("配置管理器不可用")
	}
	fallbackSettings := defaultAIConversationTaskSettings(c.GetAIGlobalSettings())
	if strings.TrimSpace(snapshot.PromptCacheBypassTimestamp) == "" && strings.TrimSpace(snapshot.ID) != "" {
		if existingSummary, err := c.readAIConversationSummary(strings.TrimSpace(snapshot.ID)); err == nil {
			snapshot.PromptCacheBypassTimestamp = existingSummary.PromptCacheBypassTimestamp
		}
	}
	normalized := normalizeAIConversationSnapshot(snapshot, fallbackSettings)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.writeAIConversationSnapshot(normalized); err != nil {
		return AIConversationSnapshot{}, err
	}
	c.withSearchDBTx("保存对话搜索索引", func(tx *sql.Tx) error {
		return c.replaceAIConversationSearchRowsLocked(tx, normalized)
	})
	return normalized, nil
}

func (c *configBridge) DeleteAIConversation(conversationID string) error {
	if c == nil {
		return fmt.Errorf("配置管理器不可用")
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return fmt.Errorf("缺少对话 ID")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	targetSummary, err := c.readAIConversationSummary(conversationID)
	if err == nil && strings.TrimSpace(targetSummary.ParentConversationID) == "" {
		if reorganizeErr := c.reorganizeAIRootConversationDescendantsLocked(targetSummary); reorganizeErr != nil {
			return reorganizeErr
		}
	}
	if err := os.RemoveAll(c.aiConversationDir(conversationID)); err != nil {
		return err
	}
	c.withSearchDBTx("删除对话搜索索引", func(tx *sql.Tx) error {
		return c.deleteAIConversationSearchRowsLocked(tx, conversationID)
	})
	return nil
}

// withSearchDBTx 在搜索数据库事务中执行操作，失败时记录日志不中断主流程
func (c *configBridge) withSearchDBTx(operation string, fn func(*sql.Tx) error) {
	db, err := c.getAIConversationSearchDB()
	if err != nil {
		slog.Error("搜索数据库不可用", "operation", operation, "error", err)
		return
	}
	tx, err := db.Begin()
	if err != nil {
		slog.Error("搜索数据库事务开始失败", "operation", operation, "error", err)
		return
	}
	if err := c.ensureAIConversationSearchSchemaLocked(tx); err != nil {
		_ = tx.Rollback()
		slog.Error("搜索数据库 schema 检查失败", "operation", operation, "error", err)
		return
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		slog.Error("搜索数据库事务执行失败", "operation", operation, "error", err)
		return
	}
	if err := tx.Commit(); err != nil {
		slog.Error("搜索数据库事务提交失败", "operation", operation, "error", err)
		return
	}
}

func (a *Service) ListAIConversations() []AIConversationSummary {
	if a == nil || a.configManager == nil {
		return []AIConversationSummary{}
	}
	return a.configManager.ListAIConversations()
}

func (a *Service) CreateAIConversation(title string) (AIConversationSnapshot, error) {
	if a == nil || a.configManager == nil {
		return AIConversationSnapshot{}, fmt.Errorf("配置管理器不可用")
	}
	return a.configManager.CreateAIConversation(title)
}

func (a *Service) GetAIConversation(conversationID string) (AIConversationSnapshot, error) {
	if a == nil || a.configManager == nil {
		return AIConversationSnapshot{}, fmt.Errorf("配置管理器不可用")
	}
	return a.configManager.GetAIConversation(conversationID)
}

func (a *Service) SaveAIConversation(jsonStr string) (AIConversationSnapshot, error) {
	if a == nil || a.configManager == nil {
		return AIConversationSnapshot{}, fmt.Errorf("配置管理器不可用")
	}
	var snapshot AIConversationSnapshot
	if strings.TrimSpace(jsonStr) != "" {
		if err := json.Unmarshal([]byte(jsonStr), &snapshot); err != nil {
			return AIConversationSnapshot{}, err
		}
	}
	return a.configManager.SaveAIConversation(snapshot)
}

func (a *Service) DeleteAIConversation(conversationID string) error {
	if a == nil || a.configManager == nil {
		return fmt.Errorf("配置管理器不可用")
	}
	return a.configManager.DeleteAIConversation(conversationID)
}
