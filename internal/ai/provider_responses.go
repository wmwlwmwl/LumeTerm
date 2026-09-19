package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	aiprovider "luminssh-go/internal/ai/provider"
)

type aiChatResponsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	CacheReadTokens    int `json:"cache_read_input_tokens,omitempty"`
	InputTokensDetails *struct {
		CachedTokens    int `json:"cached_tokens,omitempty"`
		CacheReadTokens int `json:"cache_read_input_tokens,omitempty"`
	} `json:"input_tokens_details,omitempty"`
}

type aiChatResponsesEvent struct {
	Type        string                `json:"type"`
	Delta       string                `json:"delta,omitempty"`
	Response    *aiChatResponsesState `json:"response,omitempty"`
	Usage       *aiChatResponsesUsage `json:"usage,omitempty"`
	OutputIndex int                   `json:"output_index,omitempty"`
	Item        map[string]any        `json:"item,omitempty"`
}

type aiChatResponsesState struct {
	ID         string                `json:"id,omitempty"`
	OutputText string                `json:"output_text,omitempty"`
	Output     []map[string]any      `json:"output,omitempty"`
	Usage      *aiChatResponsesUsage `json:"usage,omitempty"`
}

func buildAIConversationOpenAIResponsesCacheObject(responseID string, output []map[string]any, includeValues []string, store bool, capturedAt int64) *AIConversationOpenAIResponsesCacheObject {
	trimmedResponseID := strings.TrimSpace(responseID)
	clonedOutput := aiprovider.CloneOpenAIResponsesOutputItems(output)
	normalizedInclude := normalizeAIStringList(includeValues)
	if trimmedResponseID == "" && len(clonedOutput) == 0 && len(normalizedInclude) == 0 && capturedAt == 0 && !store {
		return nil
	}
	return &AIConversationOpenAIResponsesCacheObject{
		ResponseID: trimmedResponseID,
		Output:     clonedOutput,
		Include:    normalizedInclude,
		Store:      store,
		CapturedAt: capturedAt,
	}
}

// buildAIConversationOpenAIResponsesCompactCacheObject 生成紧凑回放缓存对象。
//
// 成功时只保存 provider 原生元数据 (item id, 加密推理内容等), 不保存正文文本;
// 正文由返回的 contentBlocks 承载, 作为下一轮请求的权威文本源。
// baseURL 与 model 构成回放身份, 切换端点或模型时下一轮回放会降级为普通文本。
// output 含本版本无法建模的 item 类型时返回 ok=false, 调用方应退回完整 output 保存。
func buildAIConversationOpenAIResponsesCompactCacheObject(
	responseID string,
	output []map[string]any,
	includeValues []string,
	store bool,
	capturedAt int64,
	baseURL string,
	model string,
) (cacheObject *AIConversationOpenAIResponsesCacheObject, contentBlocks []map[string]any, ok bool) {
	blocks, replayState, built := aiprovider.BuildOpenAIResponsesCompactReplay(
		output,
		baseURL,
		model,
		responseID,
		includeValues,
	)
	if !built {
		return nil, nil, false
	}
	return &AIConversationOpenAIResponsesCacheObject{
		ResponseID:  strings.TrimSpace(responseID),
		ReplayState: replayState,
		Include:     normalizeAIStringList(includeValues),
		Store:       store,
		CapturedAt:  capturedAt,
	}, blocks, true
}

func cloneAIConversationProviderCacheObjects(cacheObjects *AIConversationProviderCacheObjects) *AIConversationProviderCacheObjects {
	if cacheObjects == nil || cacheObjects.OpenAIResponses == nil {
		return nil
	}
	source := cacheObjects.OpenAIResponses
	trimmedResponseID := strings.TrimSpace(source.ResponseID)
	clonedOutput := aiprovider.CloneOpenAIResponsesOutputItems(source.Output)
	clonedReplayState := aiprovider.CloneOpenAIResponsesReplayState(source.ReplayState)
	normalizedInclude := normalizeAIStringList(source.Include)
	if trimmedResponseID == "" && len(clonedOutput) == 0 && len(clonedReplayState) == 0 && len(normalizedInclude) == 0 && source.CapturedAt == 0 && !source.Store {
		return nil
	}
	return &AIConversationProviderCacheObjects{
		OpenAIResponses: &AIConversationOpenAIResponsesCacheObject{
			ResponseID:  trimmedResponseID,
			Output:      clonedOutput,
			ReplayState: clonedReplayState,
			Include:     normalizedInclude,
			Store:       source.Store,
			CapturedAt:  source.CapturedAt,
		},
	}
}

func captureAIResponsesOutputItem(items map[int]map[string]any, outputIndex int, item map[string]any) {
	if items == nil || outputIndex < 0 || item == nil {
		return
	}
	clonedItems := aiprovider.CloneOpenAIResponsesOutputItems([]map[string]any{item})
	if len(clonedItems) == 0 {
		return
	}
	items[outputIndex] = clonedItems[0]
}

func collectAIResponsesOutputItems(items map[int]map[string]any) []map[string]any {
	if len(items) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(items))
	for index := range items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	collected := make([]map[string]any, 0, len(indexes))
	for _, index := range indexes {
		item := items[index]
		if item == nil {
			continue
		}
		clonedItems := aiprovider.CloneOpenAIResponsesOutputItems([]map[string]any{item})
		if len(clonedItems) == 0 {
			continue
		}
		collected = append(collected, clonedItems[0])
	}
	if len(collected) == 0 {
		return nil
	}
	return collected
}

func buildAIResponsesAssistantMessageWithCache(content string, cacheObject *AIConversationOpenAIResponsesCacheObject, contentBlocks []map[string]any) AIChatRequestMessage {
	return AIChatRequestMessage{
		Role:          "assistant",
		Content:       content,
		ContentBlocks: aiprovider.CloneOpenAIResponsesOutputItems(contentBlocks),
		CacheObjects: cloneAIConversationProviderCacheObjects(&AIConversationProviderCacheObjects{
			OpenAIResponses: cacheObject,
		}),
	}
}

func (a *Service) requestResponsesAIChatRound(ctx context.Context, requestID string, payload AIChatRequestPayload, profile AIProviderProfile, requestMessages []AIChatRequestMessage) (aiChatRoundResult, error) {
	result := aiChatRoundResult{}
	startedAt := time.Now()
	firstTokenAt := time.Time{}
	var contentBuilder strings.Builder
	var contentParser aiReasoningTagStreamParser
	var latestCacheObject *AIConversationOpenAIResponsesCacheObject
	var latestContentBlocks []map[string]any
	finalizeRoundResult := func() {
		finalizeAIChatRoundResult(&result, startedAt, firstTokenAt, &contentBuilder)
	}
	requestCtx := ctx
	if requestCtx == nil {
		requestCtx = context.Background()
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
	promptCacheSelection := aiprovider.ResolveResponsesPromptCacheSelection(runtimeProfile)
	promptCacheBypassTimestamp := ""
	if a != nil && a.configManager != nil && strings.TrimSpace(payload.ConversationID) != "" {
		promptCacheBypassTimestamp = a.configManager.GetAIConversationPromptCacheBypassTimestamp(payload.ConversationID)
	}

	requestBody := map[string]any{
		"model":        profile.Model,
		"input":        aiprovider.BuildResponsesInputMessages(toAIProviderRuntimeMessages(requestMessages), profile.BaseURL, profile.Model),
		"instructions": systemPrompt,
		"stream":       true,
		"store":        false,
	}
	aiprovider.ApplySamplingParameters(requestBody, runtimeProfile)
	if promptCacheSelection.Enabled() {
		if promptCacheKey := aiprovider.BuildResponsesPromptCacheKey(payload.ConversationID, promptCacheBypassTimestamp); promptCacheKey != "" {
			requestBody["prompt_cache_key"] = promptCacheKey
		}
	}
	aiprovider.ApplyResponsesPromptCacheSelection(requestBody, promptCacheSelection)

	if reasoningEffort := aiprovider.GetEffectiveReasoningEffort(runtimeProfile, modelCapability); reasoningEffort != "" {
		requestBody["reasoning"] = map[string]any{
			"effort":  reasoningEffort,
			"summary": "auto",
		}
		requestBody["include"] = []string{"reasoning.encrypted_content"}
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return result, err
	}

	endpoint := strings.TrimRight(profile.BaseURL, "/") + "/responses"
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return result, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", aiprovider.GetUserAgent(requestID))
	req.Header.Set("Accept", "text/event-stream")
	if apiKey := strings.TrimSpace(profile.APIKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
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
	finishedOnCompletedEvent := false
	trackedOutputItems := make(map[int]map[string]any)
	includeValues := []string{}
	if requestBodyInclude, ok := requestBody["include"].([]string); ok {
		includeValues = append([]string{}, requestBodyInclude...)
	}

	for scanner.Scan() {
		if requestCtx.Err() != nil {
			finalizeRoundResult()
			return result, requestCtx.Err()
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		eventPayload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if eventPayload == "" || eventPayload == "[DONE]" {
			continue
		}

		var event aiChatResponsesEvent
		if err := json.Unmarshal([]byte(eventPayload), &event); err != nil {
			continue
		}

		switch event.Type {
		case "response.output_item.added", "response.output_item.done":
			captureAIResponsesOutputItem(trackedOutputItems, event.OutputIndex, event.Item)
		case "response.output_text.delta", "response.text.delta":
			if event.Delta == "" {
				continue
			}
			bodyDelta, taggedReasoningDelta := contentParser.Feed(event.Delta)
			emitReasoningDelta(taggedReasoningDelta)
			emitContentDelta(bodyDelta)
		case "response.reasoning.delta", "response.reasoning_text.delta", "response.reasoning_summary.delta", "response.reasoning_summary_text.delta":
			if event.Delta == "" {
				continue
			}
			emitReasoningDelta(event.Delta)
		case "response.completed", "response.done":
			if event.Response != nil {
				if result.InputTokens == 0 && event.Response.Usage != nil {
					result.InputTokens = event.Response.Usage.InputTokens
					result.OutputTokens = event.Response.Usage.OutputTokens
					result.CacheReadTokens = event.Response.Usage.CacheReadTokens
					if result.CacheReadTokens == 0 && event.Response.Usage.InputTokensDetails != nil {
						result.CacheReadTokens = event.Response.Usage.InputTokensDetails.CacheReadTokens
						if result.CacheReadTokens == 0 {
							result.CacheReadTokens = event.Response.Usage.InputTokensDetails.CachedTokens
						}
					}
				}
				if contentBuilder.Len() == 0 && event.Response.OutputText != "" {
					bodyDelta, taggedReasoningDelta := contentParser.Feed(event.Response.OutputText)
					emitReasoningDelta(taggedReasoningDelta)
					emitContentDelta(bodyDelta)
				}
				finalOutput := event.Response.Output
				if len(finalOutput) == 0 {
					finalOutput = collectAIResponsesOutputItems(trackedOutputItems)
				}
				capturedAt := time.Now().UnixMilli()
				storeEnabled := requestBody["store"] == true
				// 紧凑回放优先; 无法建模的原生 item 退回完整 output 保存。
				if compactCacheObject, compactBlocks, compactOK := buildAIConversationOpenAIResponsesCompactCacheObject(
					event.Response.ID,
					finalOutput,
					includeValues,
					storeEnabled,
					capturedAt,
					profile.BaseURL,
					profile.Model,
				); compactOK {
					latestCacheObject = compactCacheObject
					latestContentBlocks = compactBlocks
				} else if cacheObject := buildAIConversationOpenAIResponsesCacheObject(
					event.Response.ID,
					finalOutput,
					includeValues,
					storeEnabled,
					capturedAt,
				); cacheObject != nil {
					latestCacheObject = cacheObject
					latestContentBlocks = nil
				}
			}
			if event.Usage != nil {
				result.InputTokens = event.Usage.InputTokens
				result.OutputTokens = event.Usage.OutputTokens
				result.CacheReadTokens = event.Usage.CacheReadTokens
				if result.CacheReadTokens == 0 && event.Usage.InputTokensDetails != nil {
					result.CacheReadTokens = event.Usage.InputTokensDetails.CacheReadTokens
					if result.CacheReadTokens == 0 {
						result.CacheReadTokens = event.Usage.InputTokensDetails.CachedTokens
					}
				}
			}
			// 部分中转站在发送终态事件后既不发送 [DONE] 也不关闭连接, scanner.Scan() 会一直阻塞
			// 到读超时。终态事件已携带完整正文, 用量与缓存对象, 且上面已全部落到 result 与
			// latestCacheObject, 因此此处跳出只是停止等待冗余的传输层终止标记, 不丢弃任何已接收数据。
			if profile.OpenAIResponsesFinishOnCompletedEvent {
				finishedOnCompletedEvent = true
			}
		}
		if finishedOnCompletedEvent {
			break
		}
	}

	if err := scanner.Err(); err != nil && !finishedOnCompletedEvent {
		finalizeRoundResult()
		return result, err
	}
	if requestCtx.Err() != nil {
		finalizeRoundResult()
		return result, requestCtx.Err()
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
	if latestCacheObject != nil {
		result.NextRequestMessages = append([]AIChatRequestMessage{}, requestMessages...)
		result.NextRequestMessages = append(result.NextRequestMessages, buildAIResponsesAssistantMessageWithCache(result.Text, latestCacheObject, latestContentBlocks))
	}
	if len(result.NextRequestMessages) == 0 {
		result.NextRequestMessages = append([]AIChatRequestMessage{}, requestMessages...)
	}

	return result, nil
}
