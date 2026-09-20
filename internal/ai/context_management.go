package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	aiprovider "lumeterm/internal/ai/provider"
	"lumeterm/internal/aitypes"
)

type AIConversationContextMetrics struct {
	ContextTokens int `json:"contextTokens"`
}

type AIConversationAPIMessageTokenEntry struct {
	MessageID string `json:"messageId"`
	RawTokens int    `json:"rawTokens"`
}

type AIConversationTokenLedger struct {
	SystemRawTokens int                                  `json:"systemRawTokens"`
	Entries         []AIConversationAPIMessageTokenEntry `json:"entries"`
	ContextTokens   int                                  `json:"contextTokens"`
}

type AIConversationContextCondenseResult struct {
	Snapshot          AIConversationSnapshot `json:"snapshot"`
	Summary           string                 `json:"summary"`
	PrevContextTokens int                    `json:"prevContextTokens"`
	NewContextTokens  int                    `json:"newContextTokens"`
}

const aiConversationFileContentCompressedPlaceholder = "{因节省资源,请重新调用工具获取}"
const aiConversationImageRemovedPlaceholder = "{图片因节省资源已被移除}"
const aiConversationCondenseFollowupPrompt = "<system_message>\ncontext compression caused you to be unable to confirm the working directory or the scope of files or directories to operate on. Please use the ask_followup_question tool to ask or confirm with the user.\n</system_message>"

var aiConversationToolResultPattern = regexp.MustCompile(`(?i)^\[[^\]]+\]\s*Result:`)
var aiConversationEnvironmentDetailsPattern = regexp.MustCompile(`(?s)<environment_details>.*?</environment_details>`)
var aiConversationTerminalOutputPattern = regexp.MustCompile(`(?s)<terminal_output>.*?</terminal_output>`)
var aiConversationFileContentPattern = regexp.MustCompile(`(?s)(<file_content path=["'][^"']*["']>).*?(</file_content>)`)

func calculateAIConversationContextTokensWithProfile(conversationID string, sessionID string, messages []AIConversationAPIMessage, profile AIProviderProfile) (int, error) {
	ledger, err := buildAIConversationTokenLedger(conversationID, sessionID, messages, profile)
	if err != nil {
		return 0, err
	}
	return ledger.ContextTokens, nil
}

func buildAIConversationCondenseUIMessage(summary string, prevContextTokens int, newContextTokens int) AIConversationMessage {
	now := time.Now()
	messageID := fmt.Sprintf("condense-%d", now.UnixNano())
	return AIConversationMessage{
		ID:     messageID,
		TurnID: messageID,
		Kind:   "condense_context",
		Text:   strings.TrimSpace(summary),
		Time:   now.Format("15:04"),
		Extra: map[string]interface{}{
			"contextCondense":   true,
			"prevContextTokens": prevContextTokens,
			"newContextTokens":  newContextTokens,
		},
	}
}

type aiConversationCompressedTextResult struct {
	Text                           string
	RemovedEnvironmentDetailsCount int
	CompressedFileContentCount     int
	CompressedTerminalOutputCount  int
	CompressedSystemNoticeCount    int
	ShouldRemove                   bool
}

func isAIConversationToolResultMessage(message AIConversationAPIMessage) bool {
	if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
		return false
	}
	return aiConversationToolResultPattern.MatchString(strings.TrimSpace(message.Content))
}

func findAILastUserMessageIndex(messages []AIConversationAPIMessage) int {
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.EqualFold(strings.TrimSpace(messages[index].Role), "user") {
			return index
		}
	}
	return -1
}

func findAILastRealUserPromptIndex(messages []AIConversationAPIMessage) int {
	for index := len(messages) - 1; index >= 0; index-- {
		if isAIExplicitUserPromptAPIMessage(messages[index]) {
			return index
		}
	}
	return findAILastUserMessageIndex(messages)
}

func buildAIPreservedTailMessageIndexSet(messages []AIConversationAPIMessage) map[int]struct{} {
	preserved := make(map[int]struct{})
	anchor := findAILastRealUserPromptIndex(messages)
	if anchor < 0 {
		return preserved
	}
	for index := anchor; index < len(messages); index++ {
		if index > anchor && !strings.EqualFold(strings.TrimSpace(messages[index].Role), "user") {
			break
		}
		preserved[index] = struct{}{}
	}
	lastToolResultEnd := -1
	for index := len(messages) - 1; index >= anchor; index-- {
		if isAIConversationToolResultMessage(messages[index]) {
			lastToolResultEnd = index
			break
		}
	}
	if lastToolResultEnd >= 0 {
		lastToolResultStart := lastToolResultEnd
		for lastToolResultStart-1 >= anchor && isAIConversationToolResultMessage(messages[lastToolResultStart-1]) {
			lastToolResultStart--
		}
		for index := lastToolResultStart; index <= lastToolResultEnd; index++ {
			preserved[index] = struct{}{}
		}
	}
	return preserved
}

func appendAIConversationCondenseFollowupAPIMessage(messages []AIConversationAPIMessage) []AIConversationAPIMessage {
	trimmedPrompt := strings.TrimSpace(aiConversationCondenseFollowupPrompt)
	if trimmedPrompt == "" {
		return messages
	}
	if len(messages) > 0 {
		lastMessage := messages[len(messages)-1]
		if strings.EqualFold(strings.TrimSpace(lastMessage.Role), "user") && strings.TrimSpace(lastMessage.Content) == trimmedPrompt {
			return messages
		}
	}
	now := time.Now()
	nextMessages := append([]AIConversationAPIMessage{}, messages...)
	nextMessages = append(nextMessages, AIConversationAPIMessage{
		Role:      "user",
		Content:   trimmedPrompt,
		MessageID: fmt.Sprintf("condense-followup-%d", now.UnixNano()),
		Ts:        now.UnixMilli(),
	})
	return nextMessages
}

func isAIConversationSystemNoticeText(text string) bool {
	trimmed := strings.TrimLeft(text, "\r\n\t ")
	return strings.HasPrefix(trimmed, "[ERROR] ") || strings.HasPrefix(trimmed, "[TASK RESUMPTION] ")
}

func compressAIConversationTerminalOutputText(text string) (string, int) {
	count := 0
	compressed := aiConversationTerminalOutputPattern.ReplaceAllStringFunc(text, func(match string) string {
		content := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, "<terminal_output>"), "</terminal_output>"))
		if content == "*" {
			return match
		}
		count++
		return "<terminal_output>*</terminal_output>"
	})
	return compressed, count
}

func compressAIConversationFileContentText(text string) (string, int) {
	matches := aiConversationFileContentPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, 0
	}
	return aiConversationFileContentPattern.ReplaceAllString(text, fmt.Sprintf("${1}%s${2}", aiConversationFileContentCompressedPlaceholder)), len(matches)
}

func compressAIConversationUserFacingText(text string, removeEnvironmentDetails bool, removeSystemNotice bool) aiConversationCompressedTextResult {
	nextText := text
	removedEnvironmentDetailsCount := 0
	compressedFileContentCount := 0
	compressedTerminalOutputCount := 0
	compressedSystemNoticeCount := 0
	if removeSystemNotice && isAIConversationSystemNoticeText(nextText) {
		compressedSystemNoticeCount++
		nextText = ""
	}
	if removeEnvironmentDetails {
		textWithoutEnvironmentDetails := aiConversationEnvironmentDetailsPattern.ReplaceAllString(nextText, "")
		if textWithoutEnvironmentDetails != nextText {
			removedEnvironmentDetailsCount++
			nextText = textWithoutEnvironmentDetails
		}
	}
	terminalCompressedText, terminalCompressedCount := compressAIConversationTerminalOutputText(nextText)
	if terminalCompressedCount > 0 {
		compressedTerminalOutputCount += terminalCompressedCount
		nextText = terminalCompressedText
	}
	fileCompressedText, fileCompressedCount := compressAIConversationFileContentText(nextText)
	if fileCompressedCount > 0 {
		compressedFileContentCount += fileCompressedCount
		nextText = fileCompressedText
	}
	return aiConversationCompressedTextResult{
		Text:                           nextText,
		RemovedEnvironmentDetailsCount: removedEnvironmentDetailsCount,
		CompressedFileContentCount:     compressedFileContentCount,
		CompressedTerminalOutputCount:  compressedTerminalOutputCount,
		CompressedSystemNoticeCount:    compressedSystemNoticeCount,
		ShouldRemove:                   strings.TrimSpace(nextText) == "",
	}
}

func compressAIConversationToolResultText(text string, removeEnvironmentDetails bool, removeSystemNotice bool) (string, aiConversationCompressedTextResult) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "*", aiConversationCompressedTextResult{}
	}
	header := trimmed
	body := ""
	if newlineIndex := strings.Index(trimmed, "\n"); newlineIndex >= 0 {
		header = strings.TrimSpace(trimmed[:newlineIndex])
		body = strings.TrimSpace(trimmed[newlineIndex+1:])
	}
	compressedBody := compressAIConversationUserFacingText(body, removeEnvironmentDetails, removeSystemNotice)
	if header == "" {
		return "*", compressedBody
	}
	if strings.HasSuffix(header, " *") {
		return header, compressedBody
	}
	return fmt.Sprintf("%s *", header), compressedBody
}

func buildAIConversationCompressionSummary(
	compressedCount int,
	removedAssistantCount int,
	compressedImageCount int,
	removedEnvironmentDetailsCount int,
	compressedFileContentCount int,
	compressedTerminalOutputCount int,
	compressedSystemNoticeCount int,
) string {
	return fmt.Sprintf(
		"已压缩 %d 个 工具调用结果,移除 %d 个空白 assistant 消息,替换 %d 个图片,移除 %d 个 environment_details,压缩 %d 个 file_content,压缩 %d 个 terminal_output,压缩 %d 个系统提示消息",
		compressedCount,
		removedAssistantCount,
		compressedImageCount,
		removedEnvironmentDetailsCount,
		compressedFileContentCount,
		compressedTerminalOutputCount,
		compressedSystemNoticeCount,
	)
}

func (a *Service) CountAIConversationContextTokens(sessionID string, snapshotJSON string) (AIConversationContextMetrics, error) {
	if a == nil || a.configManager == nil {
		return AIConversationContextMetrics{}, fmt.Errorf("配置管理器不可用")
	}
	var snapshot AIConversationSnapshot
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil {
		return AIConversationContextMetrics{}, err
	}
	snapshot = normalizeAIConversationSnapshot(snapshot, defaultAIConversationTaskSettings(a.configManager.GetAIGlobalSettings()))
	profile := AIProviderProfile{}
	if resolvedProfile, err := a.getAIProviderProfileForConversation(snapshot.ID); err == nil {
		profile = resolvedProfile
	}
	contextTokens, err := calculateAIConversationContextTokensWithProfile(snapshot.ID, strings.TrimSpace(sessionID), snapshot.APIMessages, profile)
	if err != nil {
		return AIConversationContextMetrics{}, err
	}
	return AIConversationContextMetrics{
		ContextTokens: contextTokens,
	}, nil
}

func buildAISystemPromptRawTokens(conversationID string, sessionID string, profile AIProviderProfile) (int, error) {
	// ponytail: Wails前端绑定调用链无原生context，context.Background()语义更准确
	return estimateAITextTokensForProfile(BuildChatSystemPromptWithProfile(context.Background(), conversationID, sessionID, false, profile), profile), nil
}

func buildAIConversationAPIMessageTokenBlocks(message AIConversationAPIMessage, profile AIProviderProfile) []TokenCountBlock {
	blocks := make([]TokenCountBlock, 0, 3)
	if role := strings.TrimSpace(message.Role); role != "" {
		blocks = append(blocks, TokenCountBlock{
			Type: "text",
			Text: role,
		})
	}
	if strings.EqualFold(strings.TrimSpace(profile.Provider), "Responses") &&
		strings.EqualFold(strings.TrimSpace(message.Role), "assistant") &&
		message.CacheObjects != nil &&
		message.CacheObjects.OpenAIResponses != nil &&
		len(message.CacheObjects.OpenAIResponses.Output) > 0 {
		responseBlocks := buildAIResponsesOutputTokenCountBlocks(message.CacheObjects.OpenAIResponses.Output)
		if len(responseBlocks) > 0 {
			return append(blocks, responseBlocks...)
		}
	}
	if message.Content != "" {
		blocks = append(blocks, TokenCountBlock{
			Type: "text",
			Text: message.Content,
		})
	}
	for _, image := range normalizeAIStringList(message.Images) {
		blocks = append(blocks, TokenCountBlock{
			Type: "image",
			Data: image,
		})
	}
	return blocks
}

func buildAIConversationTokenLedger(conversationID string, sessionID string, messages []AIConversationAPIMessage, profile AIProviderProfile) (AIConversationTokenLedger, error) {
	systemRawTokens, err := buildAISystemPromptRawTokens(conversationID, sessionID, profile)
	if err != nil {
		return AIConversationTokenLedger{}, err
	}
	normalizedMessages := normalizeAIConversationAPIMessages(messages)
	entries := make([]AIConversationAPIMessageTokenEntry, 0, len(normalizedMessages))
	totalRawTokens := systemRawTokens
	for _, message := range normalizedMessages {
		rawTokens := estimateAIConversationMessageTokens(message, profile)
		entries = append(entries, AIConversationAPIMessageTokenEntry{
			MessageID: strings.TrimSpace(message.MessageID),
			RawTokens: rawTokens,
		})
		totalRawTokens += rawTokens
	}
	return AIConversationTokenLedger{
		SystemRawTokens: systemRawTokens,
		Entries:         entries,
		ContextTokens:   totalRawTokens,
	}, nil
}

func (a *Service) BuildAIConversationTokenLedger(sessionID string, snapshotJSON string) (AIConversationTokenLedger, error) {
	if a == nil || a.configManager == nil {
		return AIConversationTokenLedger{}, fmt.Errorf("配置管理器不可用")
	}
	var snapshot AIConversationSnapshot
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil {
		return AIConversationTokenLedger{}, err
	}
	snapshot = normalizeAIConversationSnapshot(snapshot, defaultAIConversationTaskSettings(a.configManager.GetAIGlobalSettings()))
	profile := AIProviderProfile{}
	if resolvedProfile, err := a.getAIProviderProfileForConversation(snapshot.ID); err == nil {
		profile = resolvedProfile
	}
	return buildAIConversationTokenLedger(snapshot.ID, strings.TrimSpace(sessionID), snapshot.APIMessages, profile)
}

func (a *Service) CountAIConversationAPIMessageRawTokens(sessionID string, conversationID string, messagesJSON string) ([]AIConversationAPIMessageTokenEntry, error) {
	if a == nil || a.configManager == nil {
		return nil, fmt.Errorf("配置管理器不可用")
	}
	var messages []AIConversationAPIMessage
	if strings.TrimSpace(messagesJSON) != "" {
		if err := json.Unmarshal([]byte(messagesJSON), &messages); err != nil {
			return nil, err
		}
	}
	profile := AIProviderProfile{}
	if resolvedProfile, err := a.getAIProviderProfileForConversation(strings.TrimSpace(conversationID)); err == nil {
		profile = resolvedProfile
	}
	normalizedMessages := normalizeAIConversationAPIMessages(messages)
	entries := make([]AIConversationAPIMessageTokenEntry, 0, len(normalizedMessages))
	for _, message := range normalizedMessages {
		entries = append(entries, AIConversationAPIMessageTokenEntry{
			MessageID: strings.TrimSpace(message.MessageID),
			RawTokens: estimateAIConversationMessageTokens(message, profile),
		})
	}
	return entries, nil
}

func (a *Service) CondenseAIConversationContext(conversationID string, sessionID string) (AIConversationContextCondenseResult, error) {
	if a == nil || a.configManager == nil {
		return AIConversationContextCondenseResult{}, fmt.Errorf("配置管理器不可用")
	}
	snapshot, err := a.configManager.GetAIConversation(strings.TrimSpace(conversationID))
	if err != nil {
		return AIConversationContextCondenseResult{}, err
	}
	apiMessages := normalizeAIConversationAPIMessages(snapshot.APIMessages)
	if len(apiMessages) <= 2 {
		return AIConversationContextCondenseResult{}, fmt.Errorf("当前消息不足，无法压缩上下文")
	}
	profile := AIProviderProfile{}
	if resolvedProfile, err := a.getAIProviderProfileForConversation(snapshot.ID); err == nil {
		profile = resolvedProfile
	}
	prevContextTokens, err := calculateAIConversationContextTokensWithProfile(snapshot.ID, strings.TrimSpace(sessionID), apiMessages, profile)
	if err != nil {
		return AIConversationContextCondenseResult{}, err
	}
	preservedIndices := buildAIPreservedTailMessageIndexSet(apiMessages)
	newMessages := make([]AIConversationAPIMessage, 0, len(apiMessages))
	compressedCount := 0
	removedAssistantCount := 0
	compressedImageCount := 0
	removedEnvironmentDetailsCount := 0
	compressedFileContentCount := 0
	compressedTerminalOutputCount := 0
	compressedSystemNoticeCount := 0
	for index, message := range apiMessages {
		nextMessage := message
		if _, shouldPreserve := preservedIndices[index]; shouldPreserve {
			newMessages = append(newMessages, nextMessage)
			continue
		}
		if isAIConversationToolResultMessage(nextMessage) {
			compressedCount++
			compressedText, compressedBody := compressAIConversationToolResultText(nextMessage.Content, true, true)
			nextMessage.Content = compressedText
			removedEnvironmentDetailsCount += compressedBody.RemovedEnvironmentDetailsCount
			compressedFileContentCount += compressedBody.CompressedFileContentCount
			compressedTerminalOutputCount += compressedBody.CompressedTerminalOutputCount
			compressedSystemNoticeCount += compressedBody.CompressedSystemNoticeCount
			if len(normalizeAIStringList(nextMessage.Images)) > 0 {
				compressedImageCount += len(normalizeAIStringList(nextMessage.Images))
				nextMessage.Images = nil
			}
			newMessages = append(newMessages, nextMessage)
			continue
		}
		compressedText := compressAIConversationUserFacingText(nextMessage.Content, true, true)
		nextMessage.Content = compressedText.Text
		removedEnvironmentDetailsCount += compressedText.RemovedEnvironmentDetailsCount
		compressedFileContentCount += compressedText.CompressedFileContentCount
		compressedTerminalOutputCount += compressedText.CompressedTerminalOutputCount
		compressedSystemNoticeCount += compressedText.CompressedSystemNoticeCount
		if compressedText.ShouldRemove {
			nextMessage.Content = ""
		}
		images := normalizeAIStringList(nextMessage.Images)
		if len(images) > 0 {
			compressedImageCount += len(images)
			nextMessage.Images = nil
			if strings.TrimSpace(nextMessage.Content) == "" {
				nextMessage.Content = aiConversationImageRemovedPlaceholder
			} else if !strings.Contains(nextMessage.Content, aiConversationImageRemovedPlaceholder) {
				nextMessage.Content = strings.TrimSpace(nextMessage.Content + "\n" + aiConversationImageRemovedPlaceholder)
			}
		}
		if strings.TrimSpace(nextMessage.Content) == "" && len(normalizeAIStringList(nextMessage.Images)) == 0 {
			if strings.EqualFold(strings.TrimSpace(nextMessage.Role), "assistant") {
				removedAssistantCount++
			}
			continue
		}
		newMessages = append(newMessages, nextMessage)
	}
	newMessages = appendAIConversationCondenseFollowupAPIMessage(newMessages)
	newContextTokens, err := calculateAIConversationContextTokensWithProfile(snapshot.ID, strings.TrimSpace(sessionID), newMessages, profile)
	if err != nil {
		return AIConversationContextCondenseResult{}, err
	}
	if newContextTokens >= prevContextTokens {
		return AIConversationContextCondenseResult{}, fmt.Errorf("压缩后上下文未减少")
	}
	compressionSummary := buildAIConversationCompressionSummary(
		compressedCount,
		removedAssistantCount,
		compressedImageCount,
		removedEnvironmentDetailsCount,
		compressedFileContentCount,
		compressedTerminalOutputCount,
		compressedSystemNoticeCount,
	)
	nextSnapshot := snapshot
	nextSnapshot.UpdatedAt = time.Now().UnixMilli()
	nextSnapshot.Status = "idle"
	nextSnapshot.APIMessages = newMessages
	nextSnapshot.Messages = append(
		append([]AIConversationMessage{}, snapshot.Messages...),
		buildAIConversationCondenseUIMessage(compressionSummary, prevContextTokens, newContextTokens),
	)
	savedSnapshot, err := a.configManager.SaveAIConversation(nextSnapshot)
	if err != nil {
		return AIConversationContextCondenseResult{}, err
	}
	return AIConversationContextCondenseResult{
		Snapshot:          savedSnapshot,
		Summary:           compressionSummary,
		PrevContextTokens: prevContextTokens,
		NewContextTokens:  newContextTokens,
	}, nil
}

// aiConversationAutoCondenseMinRate 是自动压缩的最低收益要求：预览压缩率低于该值时不压缩，
// 避免为一点点空间收益重写整个消息前缀（前缀重写会让 provider 端提示缓存全部失效）。
const aiConversationAutoCondenseMinRate = 0.15

// maybeAutoCondenseAIChatContext 在请求发出前做上下文压力检查：开启设置且上下文占用达到
// 模型窗口阈值时，复用无损压缩管线在轮次边界静默压缩历史（主动式，对应失败恢复的被动式兜底）。
// 任何前置条件不满足或中途出错都静默回退为原消息；返回压缩后的请求消息与是否发生了压缩。
func (a *Service) maybeAutoCondenseAIChatContext(ctx context.Context, requestID string, payload AIChatRequestPayload, profile AIProviderProfile) ([]AIChatRequestMessage, bool) {
	if a == nil || a.configManager == nil {
		return nil, false
	}
	select {
	case <-ctx.Done():
		return nil, false
	default:
	}
	settings := a.configManager.GetAIGlobalSettings()
	if !settings.AutoCondenseEnabled {
		return nil, false
	}
	conversationID := strings.TrimSpace(payload.ConversationID)
	if conversationID == "" {
		return nil, false
	}
	switch strings.TrimSpace(profile.Provider) {
	case "Compatible", "Responses", "Messages":
	default:
		return nil, false
	}
	snapshot, err := a.configManager.GetAIConversation(conversationID)
	if err != nil || len(snapshot.APIMessages) == 0 {
		return nil, false
	}
	select {
	case <-ctx.Done():
		return nil, false
	default:
	}
	// 前端先落库再发起请求；快照消息数必须与本次请求严格一致。
	if len(snapshot.APIMessages) != len(normalizeAIChatRequestMessages(payload.Messages)) {
		return nil, false
	}
	contextWindow := aiprovider.GetModelContextWindow(profile.Provider, profile.Model)
	if contextWindow <= 0 {
		return nil, false
	}
	thresholdRatio := settings.AutoCondenseThresholdRatio
	if thresholdRatio <= 0 {
		thresholdRatio = aitypes.DefaultAICondenseThresholdRatio
	}
	contextTokens, err := calculateAIConversationContextTokensWithProfile(conversationID, strings.TrimSpace(payload.SessionID), snapshot.APIMessages, profile)
	if err != nil || contextTokens < int(float64(contextWindow)*thresholdRatio) {
		return nil, false
	}
	preview, err := a.previewAIConversationContextCondenseFromSnapshot(snapshot, strings.TrimSpace(payload.SessionID))
	if err != nil {
		return nil, false
	}
	if calculateAIConversationCondenseRate(preview.PrevContextTokens, preview.NewContextTokens) < aiConversationAutoCondenseMinRate {
		return nil, false
	}
	select {
	case <-ctx.Done():
		return nil, false
	default:
	}
	result, err := a.CondenseAIConversationContext(conversationID, strings.TrimSpace(payload.SessionID))
	if err != nil {
		return nil, false
	}
	requestMessages := buildAIChatRequestMessagesFromConversationAPI(result.Snapshot.APIMessages)
	if len(requestMessages) == 0 {
		return nil, false
	}
	select {
	case <-ctx.Done():
		return nil, false
	default:
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":              "context_auto_condensed",
		"requestId":         strings.TrimSpace(requestID),
		"snapshot":          result.Snapshot,
		"summary":           result.Summary,
		"prevContextTokens": result.PrevContextTokens,
		"newContextTokens":  result.NewContextTokens,
	})
	return requestMessages, true
}
