package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

type CustomHeader struct {
	Name  string
	Value string
}

type Profile struct {
	Provider                               string
	Model                                  string
	BaseURL                                string
	APIKey                                 string
	CustomHeaders                          []CustomHeader
	CacheStrategy                          string
	ReasoningEffort                        string
	EnableReasoningEffort                  bool
	OpenAILegacyReasoningFormatEnabled     bool
	OpenAIResponsesUsePromptCacheRetention bool
	OpenAIResponsesFinishOnCompletedEvent  bool
	SystemPromptAppend                     string
	ModelTemperature                       *float64
	ModelTopP                              *float64
	ModelMaxTokens                         int
	ModelMaxThinkingTokens                 int
}

const defaultAIUserAgentTemplate = "Mozilla/5.0 (${taskHash}_LumeTerm; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4.1 Safari/605.1.15"

func getTaskHash(taskID string) string {
	normalizedTaskID := strings.TrimSpace(taskID)
	if normalizedTaskID == "" {
		return ""
	}
	parts := strings.Split(normalizedTaskID, "-")
	for index := len(parts) - 1; index >= 0; index-- {
		if part := strings.TrimSpace(parts[index]); part != "" {
			return part
		}
	}
	return ""
}

func GetUserAgent(taskID string) string {
	taskHash := getTaskHash(taskID)
	if taskHash == "" {
		return strings.Replace(strings.Replace(defaultAIUserAgentTemplate, "${taskHash}_", "", 1), "${taskHash}", "", 1)
	}
	return strings.Replace(defaultAIUserAgentTemplate, "${taskHash}", taskHash, 1)
}

func ApplyCustomHeaders(headers http.Header, customHeaders []CustomHeader) {
	if headers == nil {
		return
	}
	for _, header := range customHeaders {
		name := strings.TrimSpace(header.Name)
		if name == "" || strings.ContainsAny(name, "\r\n: \t") {
			continue
		}
		// 名称存在即发送,即使值为空也保留该请求头,以兼容只检查字段存在性的网关。
		headers.Set(name, strings.TrimSpace(header.Value))
	}
}

func ApplySamplingParameters(requestBody map[string]any, profile Profile) {
	if profile.ModelTemperature != nil {
		requestBody["temperature"] = *profile.ModelTemperature
	}
	if profile.ModelTopP != nil {
		requestBody["top_p"] = *profile.ModelTopP
	}
}

type OpenAIResponsesCacheObject struct {
	ResponseID  string
	Output      []map[string]any
	ReplayState map[string]any
	Include     []string
	Store       bool
	CapturedAt  int64
}

type ProviderCacheObjects struct {
	OpenAIResponses *OpenAIResponsesCacheObject
}

type ChatMessage struct {
	Role          string
	Content       string
	ContentBlocks []map[string]any
	Images        []string
	CacheObjects  *ProviderCacheObjects
}

func normalizeStringList(values []string) []string {
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

func CloneOpenAIResponsesOutputItems(items []map[string]any) []map[string]any {
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

func CloneOpenAIResponsesReplayState(state map[string]any) map[string]any {
	if len(state) == 0 {
		return nil
	}
	cloned, ok := cloneOpenAIResponsesJSONValue(state)
	if !ok {
		return nil
	}
	clonedState, isRecord := cloned.(map[string]any)
	if !isRecord {
		return nil
	}
	return clonedState
}

func cloneAIProviderCacheObjects(cacheObjects *ProviderCacheObjects) *ProviderCacheObjects {
	if cacheObjects == nil {
		return nil
	}
	if cacheObjects.OpenAIResponses == nil {
		return nil
	}
	return &ProviderCacheObjects{
		OpenAIResponses: &OpenAIResponsesCacheObject{
			ResponseID:  strings.TrimSpace(cacheObjects.OpenAIResponses.ResponseID),
			Output:      CloneOpenAIResponsesOutputItems(cacheObjects.OpenAIResponses.Output),
			ReplayState: CloneOpenAIResponsesReplayState(cacheObjects.OpenAIResponses.ReplayState),
			Include:     normalizeStringList(cacheObjects.OpenAIResponses.Include),
			Store:       cacheObjects.OpenAIResponses.Store,
			CapturedAt:  cacheObjects.OpenAIResponses.CapturedAt,
		},
	}
}

func normalizeCacheStrategy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off":
		return "off"
	case "model":
		return "model"
	case "5m":
		return "5m"
	case "1h":
		return "1h"
	case "30m":
		return "30m"
	case "in_memory":
		return "in_memory"
	case "24h":
		return "24h"
	default:
		return "model"
	}
}

type ResponsesPromptCacheSelection struct {
	Format          string
	Duration        string
	UseModelDefault bool
}

func (selection ResponsesPromptCacheSelection) Enabled() bool {
	return selection.UseModelDefault || (selection.Format != "" && selection.Duration != "" && selection.Duration != "off")
}

func ResolveResponsesPromptCacheSelection(profile Profile) ResponsesPromptCacheSelection {
	if normalizeProviderProtocol(profile.Provider) != "Responses" {
		return ResponsesPromptCacheSelection{}
	}
	selectedStrategy := normalizeCacheStrategy(profile.CacheStrategy)
	if selectedStrategy == "off" {
		return ResponsesPromptCacheSelection{}
	}
	if selectedStrategy != "model" {
		format := ResponsesPromptCacheFormatOptions
		if profile.OpenAIResponsesUsePromptCacheRetention {
			format = ResponsesPromptCacheFormatRetention
		}
		return ResponsesPromptCacheSelection{
			Format:   format,
			Duration: selectedStrategy,
		}
	}
	policy := GetResponsesPromptCachePolicy(profile.Model)
	if !policy.Known || policy.Format == "" {
		return ResponsesPromptCacheSelection{}
	}
	if policy.DefaultDuration == "" {
		return ResponsesPromptCacheSelection{UseModelDefault: true}
	}
	return ResponsesPromptCacheSelection{
		Format:   policy.Format,
		Duration: policy.DefaultDuration,
	}
}

func ApplyResponsesPromptCacheSelection(requestBody map[string]any, selection ResponsesPromptCacheSelection) {
	if !selection.Enabled() || selection.UseModelDefault {
		return
	}
	switch selection.Format {
	case ResponsesPromptCacheFormatRetention:
		requestBody["prompt_cache_retention"] = selection.Duration
	case ResponsesPromptCacheFormatOptions:
		requestBody["prompt_cache_options"] = map[string]any{"ttl": selection.Duration}
	}
}

const (
	defaultAIProviderMaxOutputTokens   = 16384
	defaultAIProviderMaxThinkingTokens = 8192
)

func containsAIProviderReasoningEffort(options []string, value string) bool {
	normalizedValue := strings.ToLower(strings.TrimSpace(value))
	if normalizedValue == "" {
		return false
	}
	for _, option := range normalizeAIProviderModelReasoningEffortOptions(options) {
		if option == normalizedValue {
			return true
		}
	}
	return false
}

func supportsAIProviderFallbackReasoningEffort(provider string) bool {
	switch normalizeProviderProtocol(provider) {
	case "Compatible", "Responses", "Messages":
		return true
	default:
		return false
	}
}

func ResolvePromptCacheStrategy(profile Profile, capability AIProviderModelCapability) string {
	if !providerSupportsAIQuickEditPromptCache(profile.Provider) {
		return "off"
	}
	switch normalizeCacheStrategy(profile.CacheStrategy) {
	case "off":
		return "off"
	case "5m":
		return "5m"
	case "1h":
		return "1h"
	default:
		if capability.SupportsPromptCache {
			return "5m"
		}
		return "off"
	}
}

// BuildResponsesPromptCacheKey 生成 Responses 提示缓存键。
//
// 会话 ID 是唯一权威来源。系统提示词刻意不参与: 它可能包含每轮变化的动态内容,
// 一旦进入哈希就会让缓存键逐轮漂移, 前缀缓存全盘失效。
// bypass 时间戳只在用户或系统显式要求绕过缓存时推进键代次, 属显式意图而非动态噪声。
func BuildResponsesPromptCacheKey(conversationID string, promptCacheBypassTimestamp string) string {
	trimmedConversationID := strings.TrimSpace(conversationID)
	if trimmedConversationID == "" {
		return ""
	}
	bypassSource := strings.TrimSpace(promptCacheBypassTimestamp)
	if bypassSource == "" {
		bypassSource = "stable"
	}
	checksum := sha256.Sum256([]byte(bypassSource))
	bypassHash := hex.EncodeToString(checksum[:])[:12]
	cacheKey := "LumeTerm:resp:v3:" + trimmedConversationID + ":" + bypassHash
	if len(cacheKey) > 64 {
		return cacheKey[len(cacheKey)-64:]
	}
	return cacheKey
}

func getAIProviderOpenAIPromptCacheControl(strategy string) map[string]any {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "1h":
		return map[string]any{
			"type": "ephemeral",
			"ttl":  "1h",
		}
	case "5m":
		return map[string]any{
			"type": "ephemeral",
		}
	default:
		return nil
	}
}

func buildAIProviderOpenAIImageContentParts(images []string) []map[string]any {
	parts := make([]map[string]any, 0, len(images))
	for _, image := range normalizeStringList(images) {
		if image == "" {
			continue
		}
		parts = append(parts, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": image,
			},
		})
	}
	return parts
}

func buildAIProviderOpenAIUserContent(message ChatMessage, cacheControl map[string]any) []map[string]any {
	parts := make([]map[string]any, 0, len(message.Images)+1)
	if text := strings.TrimSpace(message.Content); text != "" {
		textBlock := map[string]any{
			"type": "text",
			"text": text,
		}
		if cacheControl != nil {
			textBlock["cache_control"] = cacheControl
		}
		parts = append(parts, textBlock)
	}
	return append(parts, buildAIProviderOpenAIImageContentParts(message.Images)...)
}

func BuildOpenAIChatMessages(systemPrompt string, requestMessages []ChatMessage, promptCacheStrategy string) []map[string]any {
	cacheControl := getAIProviderOpenAIPromptCacheControl(promptCacheStrategy)
	systemMessage := map[string]any{
		"role":    "system",
		"content": systemPrompt,
	}
	if cacheControl != nil {
		systemMessage["content"] = []map[string]any{
			{
				"type":          "text",
				"text":          systemPrompt,
				"cache_control": cacheControl,
			},
		}
	}

	userIndexes := make([]int, 0, len(requestMessages))
	for index, message := range requestMessages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			userIndexes = append(userIndexes, index)
		}
	}
	if len(userIndexes) > 2 {
		userIndexes = userIndexes[len(userIndexes)-2:]
	}
	cacheIndexes := make(map[int]struct{}, len(userIndexes))
	for _, index := range userIndexes {
		cacheIndexes[index] = struct{}{}
	}

	messages := make([]map[string]any, 0, len(requestMessages)+1)
	messages = append(messages, systemMessage)
	for index, message := range requestMessages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "system" && role != "user" && role != "assistant" {
			continue
		}
		if role == "user" {
			var textCacheControl map[string]any
			if cacheControl != nil {
				if _, ok := cacheIndexes[index]; ok {
					textCacheControl = cacheControl
				}
			}
			messageImages := normalizeStringList(message.Images)
			contentParts := buildAIProviderOpenAIUserContent(ChatMessage{
				Role:    message.Role,
				Content: message.Content,
				Images:  messageImages,
			}, textCacheControl)
			if len(contentParts) == 0 {
				continue
			}
			if len(messageImages) == 0 && textCacheControl == nil {
				messages = append(messages, map[string]any{
					"role":    "user",
					"content": strings.TrimSpace(message.Content),
				})
			} else {
				messages = append(messages, map[string]any{
					"role":    "user",
					"content": contentParts,
				})
			}
			continue
		}
		messages = append(messages, map[string]any{
			"role":    role,
			"content": strings.TrimSpace(message.Content),
		})
	}
	return messages
}

func ParseBase64DataURL(dataURL string) (string, string, bool) {
	trimmed := strings.TrimSpace(dataURL)
	if !strings.HasPrefix(trimmed, "data:") {
		return "", "", false
	}
	body := strings.TrimPrefix(trimmed, "data:")
	parts := strings.SplitN(body, ";base64,", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	mediaType := strings.TrimSpace(parts[0])
	base64Data := strings.TrimSpace(parts[1])
	if mediaType == "" || base64Data == "" {
		return "", "", false
	}
	return mediaType, base64Data, true
}

func buildAIProviderResponsesImageContentParts(images []string) []map[string]any {
	parts := make([]map[string]any, 0, len(images))
	for _, image := range normalizeStringList(images) {
		if image == "" {
			continue
		}
		parts = append(parts, map[string]any{
			"type":      "input_image",
			"image_url": image,
		})
	}
	return parts
}

func BuildResponsesInputMessages(requestMessages []ChatMessage, baseURL string, model string) []map[string]any {
	input := make([]map[string]any, 0, len(requestMessages))
	for _, message := range requestMessages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "assistant":
			cacheObjects := cloneAIProviderCacheObjects(message.CacheObjects)
			if cacheObjects != nil && cacheObjects.OpenAIResponses != nil {
				// 紧凑回放优先: 元数据与内容块重建原生 item。
				if len(cacheObjects.OpenAIResponses.ReplayState) > 0 {
					if replayed, ok := ReplayOpenAIResponsesCompact(
						cacheObjects.OpenAIResponses.ReplayState,
						message.ContentBlocks,
						baseURL,
						model,
					); ok {
						input = append(input, replayed...)
						continue
					}
				}
				// 旧数据兼容: 完整原生 output 原样回放。
				if len(cacheObjects.OpenAIResponses.Output) > 0 {
					input = append(input, cacheObjects.OpenAIResponses.Output...)
					continue
				}
			}
			input = append(input, map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type": "output_text",
						"text": message.Content,
					},
				},
			})
		case "user":
			contentParts := make([]map[string]any, 0, len(message.Images)+1)
			if text := strings.TrimSpace(message.Content); text != "" {
				contentParts = append(contentParts, map[string]any{
					"type": "input_text",
					"text": text,
				})
			}
			contentParts = append(contentParts, buildAIProviderResponsesImageContentParts(message.Images)...)
			if len(contentParts) == 0 {
				continue
			}
			input = append(input, map[string]any{
				"role":    "user",
				"content": contentParts,
			})
		}
	}
	return input
}

func NormalizeMessagesBaseURL(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimRight(trimmed, "/")
	trimmed = strings.TrimSuffix(trimmed, "/v1/messages")
	trimmed = strings.TrimSuffix(trimmed, "/v1")
	return strings.TrimRight(trimmed, "/")
}

func GetAnthropicPromptCacheControl(strategy string) map[string]any {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "1h":
		return map[string]any{
			"type": "ephemeral",
			"ttl":  "1h",
		}
	case "5m":
		return map[string]any{
			"type": "ephemeral",
		}
	default:
		return nil
	}
}

func buildAIProviderAnthropicImageContentParts(images []string) []map[string]any {
	parts := make([]map[string]any, 0, len(images))
	for _, image := range normalizeStringList(images) {
		mediaType, base64Data, ok := ParseBase64DataURL(image)
		if !ok {
			continue
		}
		parts = append(parts, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": mediaType,
				"data":       base64Data,
			},
		})
	}
	return parts
}

func BuildAnthropicMessages(requestMessages []ChatMessage, promptCacheStrategy string) []map[string]any {
	cacheControl := GetAnthropicPromptCacheControl(promptCacheStrategy)
	userIndexes := make([]int, 0, len(requestMessages))
	for index, message := range requestMessages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			userIndexes = append(userIndexes, index)
		}
	}
	if len(userIndexes) > 2 {
		userIndexes = userIndexes[len(userIndexes)-2:]
	}
	cacheIndexes := make(map[int]struct{}, len(userIndexes))
	for _, index := range userIndexes {
		cacheIndexes[index] = struct{}{}
	}

	messages := make([]map[string]any, 0, len(requestMessages))
	for index, message := range requestMessages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		if role == "assistant" {
			messages = append(messages, map[string]any{
				"role":    "assistant",
				"content": strings.TrimSpace(message.Content),
			})
			continue
		}
		var textCacheControl map[string]any
		if cacheControl != nil {
			if _, ok := cacheIndexes[index]; ok {
				textCacheControl = cacheControl
			}
		}
		messageImages := normalizeStringList(message.Images)
		contentParts := make([]map[string]any, 0, len(messageImages)+1)
		if text := strings.TrimSpace(message.Content); text != "" {
			textBlock := map[string]any{
				"type": "text",
				"text": text,
			}
			if textCacheControl != nil {
				textBlock["cache_control"] = textCacheControl
			}
			contentParts = append(contentParts, textBlock)
		}
		contentParts = append(contentParts, buildAIProviderAnthropicImageContentParts(messageImages)...)
		if len(contentParts) == 0 {
			continue
		}
		if len(messageImages) == 0 && textCacheControl == nil {
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": strings.TrimSpace(message.Content),
			})
		} else {
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": contentParts,
			})
		}
	}
	return messages
}

func GetEffectiveReasoningEffort(profile Profile, capability AIProviderModelCapability) string {
	defaultEffort := normalizeReasoningEffort(capability.ReasoningEffort)
	requestedEffort := normalizeReasoningEffort(profile.ReasoningEffort)

	if capability.ReasoningMode != AIProviderReasoningModeEffort {
		if !supportsAIProviderFallbackReasoningEffort(profile.Provider) || !profile.EnableReasoningEffort {
			return ""
		}
		if requestedEffort == "" {
			requestedEffort = defaultEffort
		}
		if requestedEffort == "" || requestedEffort == "disable" {
			return ""
		}
		return requestedEffort
	}

	supportedEfforts := normalizeAIProviderModelReasoningEffortOptions(capability.SupportsReasoningEffort)

	if capability.RequiredReasoningEffort {
		if requestedEffort != "" && requestedEffort != "disable" && (len(supportedEfforts) == 0 || containsAIProviderReasoningEffort(supportedEfforts, requestedEffort)) {
			return requestedEffort
		}
		if defaultEffort != "" && defaultEffort != "disable" && (len(supportedEfforts) == 0 || containsAIProviderReasoningEffort(supportedEfforts, defaultEffort)) {
			return defaultEffort
		}
		if len(supportedEfforts) > 0 {
			return supportedEfforts[0]
		}
		return ""
	}

	if !profile.EnableReasoningEffort {
		return ""
	}

	if requestedEffort == "" {
		requestedEffort = defaultEffort
	}
	if requestedEffort == "" || requestedEffort == "disable" {
		return ""
	}
	if len(supportedEfforts) == 0 || containsAIProviderReasoningEffort(supportedEfforts, requestedEffort) {
		return requestedEffort
	}
	if defaultEffort != "" && defaultEffort != "disable" {
		return defaultEffort
	}
	return ""
}

func ShouldUseBinaryReasoning(profile Profile, capability AIProviderModelCapability) bool {
	if capability.ReasoningMode != AIProviderReasoningModeBinary {
		return false
	}
	if capability.RequiredReasoningEffort {
		return true
	}
	return profile.EnableReasoningEffort
}

func ResolveMaxOutputTokens(profile Profile, capability AIProviderModelCapability) int {
	if profile.ModelMaxTokens > 0 {
		return profile.ModelMaxTokens
	}
	if capability.MaxTokens > 0 {
		return capability.MaxTokens
	}
	return defaultAIProviderMaxOutputTokens
}
