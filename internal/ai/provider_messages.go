package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	aiprovider "luminssh-go/internal/ai/provider"
)

const anthropicPromptCachingBetaHeader = "prompt-caching-2024-07-31"

type aiChatMessagesUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadTokens     int `json:"cache_read_input_tokens,omitempty"`
}

type aiChatMessagesEvent struct {
	Type         string               `json:"type"`
	Index        int                  `json:"index,omitempty"`
	Message      *aiChatMessagesState `json:"message,omitempty"`
	Usage        *aiChatMessagesUsage `json:"usage,omitempty"`
	ContentBlock *struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		Thinking string `json:"thinking,omitempty"`
	} `json:"content_block,omitempty"`
	Delta *struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		Thinking string `json:"thinking,omitempty"`
	} `json:"delta,omitempty"`
}

type aiChatMessagesState struct {
	Usage *aiChatMessagesUsage `json:"usage,omitempty"`
}

func normalizeAIMessagesAdaptiveReasoningEffort(value string) string {
	switch normalizeAIProviderReasoningEffort(value) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "minimal":
		return "low"
	case "xhigh", "max":
		return "high"
	default:
		return ""
	}
}

func buildAIMessagesLegacyReasoning(maxOutputTokens int, reasoningEffort string) map[string]any {
	switch normalizeAIProviderReasoningEffort(reasoningEffort) {
	case "", "disable", "none":
		return nil
	}

	maxThinkingBudget := int(float64(maxOutputTokens) * 0.8)
	if maxThinkingBudget < 1024 {
		return nil
	}

	effortRatios := map[string]float64{
		"minimal": 0.25,
		"low":     0.4,
		"medium":  0.6,
		"high":    0.8,
		"xhigh":   1,
	}
	ratio, ok := effortRatios[normalizeAIProviderReasoningEffort(reasoningEffort)]
	if !ok {
		return nil
	}

	budgetTokens := int(float64(maxThinkingBudget) * ratio)
	if budgetTokens < 1024 {
		budgetTokens = 1024
	}
	if budgetTokens > maxThinkingBudget {
		budgetTokens = maxThinkingBudget
	}

	return map[string]any{
		"type":          "enabled",
		"budget_tokens": budgetTokens,
	}
}

func (a *Service) requestMessagesAIChatRound(ctx context.Context, requestID string, payload AIChatRequestPayload, profile AIProviderProfile, requestMessages []AIChatRequestMessage) (aiChatRoundResult, error) {
	result := aiChatRoundResult{}
	startedAt := time.Now()
	firstTokenAt := time.Time{}
	var contentBuilder strings.Builder
	var contentParser aiReasoningTagStreamParser
	finalizeRoundResult := func() {
		finalizeAIChatRoundResult(&result, startedAt, firstTokenAt, &contentBuilder)
	}

	emitReasoningDelta := func(delta string) {
		if delta == "" {
			return
		}
		a.emitAIChatPayloadReasoningDelta(payload, requestID, delta)
	}

	emitContentDelta := func(delta string) {
		if delta == "" {
			return
		}
		if firstTokenAt.IsZero() && strings.TrimSpace(delta) != "" {
			firstTokenAt = time.Now()
		}
		contentBuilder.WriteString(delta)
		a.emitAIChatPayloadContentDelta(payload, requestID, delta)
	}

	systemPrompt := resolveAISystemPromptForPayload(a.ctx, payload, profile)
	modelCapability := aiprovider.ResolveModelCapability(profile.Provider, profile.Model)
	runtimeProfile := toAIProviderRuntimeProfile(profile)
	promptCacheStrategy := aiprovider.ResolvePromptCacheStrategy(runtimeProfile, modelCapability)
	maxOutputTokens := aiprovider.ResolveMaxOutputTokens(runtimeProfile, modelCapability)

	systemBlock := map[string]any{
		"type": "text",
		"text": systemPrompt,
	}
	if cacheControl := aiprovider.GetAnthropicPromptCacheControl(promptCacheStrategy); cacheControl != nil {
		systemBlock["cache_control"] = cacheControl
	}

	requestBody := map[string]any{
		"model":      profile.Model,
		"max_tokens": maxOutputTokens,
		"system":     []map[string]any{systemBlock},
		"messages":   aiprovider.BuildAnthropicMessages(toAIProviderRuntimeMessages(requestMessages), promptCacheStrategy),
		"stream":     true,
	}
	aiprovider.ApplySamplingParameters(requestBody, runtimeProfile)

	if reasoningEffort := aiprovider.GetEffectiveReasoningEffort(runtimeProfile, modelCapability); reasoningEffort != "" {
		if profile.OpenAILegacyReasoningFormatEnabled {
			if legacyReasoning := buildAIMessagesLegacyReasoning(maxOutputTokens, reasoningEffort); legacyReasoning != nil {
				requestBody["thinking"] = legacyReasoning
			}
		} else if adaptiveReasoningEffort := normalizeAIMessagesAdaptiveReasoningEffort(reasoningEffort); adaptiveReasoningEffort != "" {
			requestBody["thinking"] = map[string]any{
				"type": "adaptive",
			}
			requestBody["output_config"] = map[string]any{
				"effort": adaptiveReasoningEffort,
			}
		}
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return result, err
	}

	baseURL := aiprovider.NormalizeMessagesBaseURL(profile.BaseURL)
	if baseURL == "" {
		return result, fmt.Errorf("当前供应商缺少 Base URL")
	}
	endpoint := baseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return result, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", aiprovider.GetUserAgent(requestID))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("anthropic-version", "2023-06-01")
	if apiKey := strings.TrimSpace(profile.APIKey); apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	if promptCacheStrategy != "off" {
		req.Header.Set("anthropic-beta", anthropicPromptCachingBetaHeader)
	}
	aiprovider.ApplyCustomHeaders(req.Header, runtimeProfile.CustomHeaders)

	client, err := a.newAINeverTimeoutHTTPClientForProfile(&profile)
	if err != nil {
		return result, err
	}
	resp, err := traceAIHTTPRound(client, req, aiDebugRoundMeta{
		RequestID:         requestID,
		ConversationID:    payload.ConversationID,
		Protocol:          profile.Provider,
		Model:             profile.Model,
		Endpoint:          endpoint,
		MessageCount:      len(requestMessages),
		SystemPromptChars: len(systemPrompt),
		APIKey:            profile.APIKey,
	}, body)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		bodyStr := readErrorBody(resp, 4096)
		errorText := strings.TrimSpace(bodyStr)
		if errorText == "" {
			errorText = resp.Status
		}
		return result, fmt.Errorf("%s", errorText)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lastOutputTokens := 0

	for scanner.Scan() {
		if ctx.Err() != nil {
			finalizeRoundResult()
			return result, ctx.Err()
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		eventPayload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if eventPayload == "" || eventPayload == "[DONE]" {
			continue
		}

		var event aiChatMessagesEvent
		if err := json.Unmarshal([]byte(eventPayload), &event); err != nil {
			continue
		}

		switch event.Type {
		case "message_start":
			if event.Message != nil && event.Message.Usage != nil {
				result.InputTokens = event.Message.Usage.InputTokens
				lastOutputTokens = event.Message.Usage.OutputTokens
				result.OutputTokens = lastOutputTokens
				result.CacheReadTokens = event.Message.Usage.CacheReadTokens
			}
		case "message_delta":
			if event.Usage != nil {
				currentOutputTokens := event.Usage.OutputTokens
				if currentOutputTokens < lastOutputTokens {
					currentOutputTokens = lastOutputTokens
				}
				result.OutputTokens = currentOutputTokens
				lastOutputTokens = currentOutputTokens
			}
		case "content_block_start":
			if event.ContentBlock == nil {
				continue
			}
			emitReasoningDelta(event.ContentBlock.Thinking)
			bodyDelta, taggedReasoningDelta := contentParser.Feed(event.ContentBlock.Text)
			emitReasoningDelta(taggedReasoningDelta)
			emitContentDelta(bodyDelta)
		case "content_block_delta":
			if event.Delta == nil {
				continue
			}
			emitReasoningDelta(event.Delta.Thinking)
			bodyDelta, taggedReasoningDelta := contentParser.Feed(event.Delta.Text)
			emitReasoningDelta(taggedReasoningDelta)
			emitContentDelta(bodyDelta)
		}
	}

	if err := scanner.Err(); err != nil {
		finalizeRoundResult()
		return result, err
	}
	if ctx.Err() != nil {
		finalizeRoundResult()
		return result, ctx.Err()
	}

	flushedBody, flushedReasoning := contentParser.Flush()
	emitReasoningDelta(flushedReasoning)
	emitContentDelta(flushedBody)

	result.Text = strings.TrimSpace(contentBuilder.String())
	if result.Text == "" {
		result.Text = aiChatEmptyResponseText
	}
	if !firstTokenAt.IsZero() {
		result.FirstTokenMs = firstTokenAt.Sub(startedAt).Milliseconds()
	}
	result.ElapsedMs = time.Since(startedAt).Milliseconds()
	if result.OutputTokens > 0 && result.ElapsedMs > 0 {
		result.TokensPerSecond = float64(result.OutputTokens) / (float64(result.ElapsedMs) / 1000)
	}

	return result, nil
}
