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

	aiprovider "lumeterm/internal/ai/provider"
)

type aiChatCompatibleUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens,omitempty"`
	PromptTokensDetails *struct {
		CachedTokens    int `json:"cached_tokens,omitempty"`
		CacheReadTokens int `json:"cache_read_input_tokens,omitempty"`
	} `json:"prompt_tokens_details,omitempty"`
}

type aiChatCompatibleStreamError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type aiChatCompatibleChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *aiChatCompatibleUsage       `json:"usage,omitempty"`
	Error *aiChatCompatibleStreamError `json:"error,omitempty"`
}

func aiChatCompatibleErrorText(err *aiChatCompatibleStreamError) string {
	if err == nil {
		return ""
	}
	if text := strings.TrimSpace(err.Message); text != "" {
		return text
	}
	if text := strings.TrimSpace(err.Code); text != "" {
		return text
	}
	if text := strings.TrimSpace(err.Type); text != "" {
		return text
	}
	return "upstream stream error"
}

type aiChatRoundResult struct {
	Text                string
	FirstTokenMs        int64
	ElapsedMs           int64
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	TokensPerSecond     float64
	NextRequestMessages []AIChatRequestMessage
}
type aiProviderModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func fetchCompatibleProviderModels(client *http.Client, profile AIProviderProfile) ([]string, error) {
	trimmedBaseURL := strings.TrimSpace(profile.BaseURL)
	if trimmedBaseURL == "" {
		return nil, fmt.Errorf("请先填写 OpenAI 基础 URL")
	}

	endpoint := strings.TrimRight(trimmedBaseURL, "/") + "/models"
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", aiprovider.GetUserAgent(""))
	if key := strings.TrimSpace(profile.APIKey); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	aiprovider.ApplyCustomHeaders(request.Header, toAIProviderRuntimeProfile(profile).CustomHeaders)

	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		bodyStr := readErrorBody(response, 4096)
		errorText := strings.TrimSpace(bodyStr)
		if errorText == "" {
			errorText = response.Status
		}
		return nil, fmt.Errorf("%s", errorText)
	}

	var payload aiProviderModelsResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}

	modelSet := make(map[string]struct{}, len(payload.Data))
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		modelID := strings.TrimSpace(item.ID)
		if modelID == "" {
			continue
		}
		if _, exists := modelSet[modelID]; exists {
			continue
		}
		modelSet[modelID] = struct{}{}
		models = append(models, modelID)
	}

	sort.Strings(models)

	if len(models) == 0 {
		return nil, fmt.Errorf("未获取到任何模型")
	}

	return models, nil
}

func fetchMessagesProviderModels(client *http.Client, profile AIProviderProfile) ([]string, error) {
	trimmedBaseURL := aiprovider.NormalizeMessagesBaseURL(profile.BaseURL)
	if trimmedBaseURL == "" {
		return nil, fmt.Errorf("请先填写 Anthropic 基础 URL")
	}

	endpoint := trimmedBaseURL + "/v1/models"
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	request.Header.Set("Accept", "application/json")
	request.Header.Set("anthropic-version", "2023-06-01")
	if key := strings.TrimSpace(profile.APIKey); key != "" {
		request.Header.Set("x-api-key", key)
	}
	aiprovider.ApplyCustomHeaders(request.Header, toAIProviderRuntimeProfile(profile).CustomHeaders)

	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	response, fetchErr := client.Do(request)

	is404Or405 := false
	if fetchErr == nil && response != nil {
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed {
			is404Or405 = true
			response.Body.Close()
		}
	}

	if (fetchErr != nil || is404Or405) && strings.Contains(trimmedBaseURL, "/anthropic") {
		// Fallback for DeepSeek or similar providers that expose Anthropic-compatible /v1/messages
		// but require OpenAI-compatible /v1/models on their main domain.
		derivedBaseURL := strings.TrimSuffix(strings.TrimRight(trimmedBaseURL, "/"), "/anthropic")
		var fallbackErr error
		fallbackSucceeded := false
		for _, path := range []string{"/v1/models", "/models"} {
			fallbackEndpoint := derivedBaseURL + path
			fallbackReq, err := http.NewRequest(http.MethodGet, fallbackEndpoint, nil)
			if err != nil {
				continue
			}
			fallbackReq.Header.Set("Accept", "application/json")
			fallbackReq.Header.Set("User-Agent", aiprovider.GetUserAgent(""))
			if key := strings.TrimSpace(profile.APIKey); key != "" {
				fallbackReq.Header.Set("Authorization", "Bearer "+key)
			}
			aiprovider.ApplyCustomHeaders(fallbackReq.Header, toAIProviderRuntimeProfile(profile).CustomHeaders)
			fallbackResp, err := client.Do(fallbackReq)
			if err != nil {
				fallbackErr = err
				continue
			}
			if fallbackResp.StatusCode >= 200 && fallbackResp.StatusCode < 300 {
				response = fallbackResp
				fetchErr = nil
				fallbackSucceeded = true
				break
			}
			bodyStr := readErrorBody(fallbackResp, 1024)
			fallbackResp.Body.Close()
			fallbackErr = fmt.Errorf("fallback to %s failed (status %d): %s", path, fallbackResp.StatusCode, strings.TrimSpace(bodyStr))
		}
		if !fallbackSucceeded {
			if fallbackErr != nil {
				return nil, fmt.Errorf("获取 Anthropic 模型列表失败，且尝试备用 OpenAI 兼容接口失败: %v", fallbackErr)
			}
			if fetchErr != nil {
				return nil, fetchErr
			}
			return nil, fmt.Errorf("获取 Anthropic 模型列表失败 (404/405)")
		}
	} else if fetchErr != nil {
		return nil, fetchErr
	} else if is404Or405 {
		return nil, fmt.Errorf("获取 Anthropic 模型列表失败 (404/405)")
	}

	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		bodyStr := readErrorBody(response, 4096)
		errorText := strings.TrimSpace(bodyStr)
		if errorText == "" {
			errorText = response.Status
		}
		return nil, fmt.Errorf("%s", errorText)
	}

	var payload aiProviderModelsResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}

	modelSet := make(map[string]struct{}, len(payload.Data))
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		modelID := strings.TrimSpace(item.ID)
		if modelID == "" {
			continue
		}
		if _, exists := modelSet[modelID]; exists {
			continue
		}
		modelSet[modelID] = struct{}{}
		models = append(models, modelID)
	}

	sort.Strings(models)

	if len(models) == 0 {
		return nil, fmt.Errorf("未获取到任何模型")
	}

	return models, nil
}

func (a *Service) RequestAIProviderModels(baseURL string, apiKey string) ([]string, error) {
	client, err := a.newAIHTTPClient(20 * time.Second)
	if err != nil {
		return nil, err
	}
	return fetchCompatibleProviderModels(client, AIProviderProfile{BaseURL: baseURL, APIKey: apiKey})
}

func (a *Service) RequestAIProviderModelsWithProfile(jsonStr string) ([]string, error) {
	profile := AIProviderProfile{}
	if strings.TrimSpace(jsonStr) != "" {
		if err := json.Unmarshal([]byte(jsonStr), &profile); err != nil {
			return nil, err
		}
	}
	profile.BaseURL = strings.TrimSpace(profile.BaseURL)
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	client, err := a.newAIHTTPClientForProfile(&profile, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if profile.Provider == "Messages" {
		return fetchMessagesProviderModels(client, profile)
	}
	return fetchCompatibleProviderModels(client, profile)
}

func (a *Service) requestCompatibleAIChatRound(ctx context.Context, requestID string, payload AIChatRequestPayload, profile AIProviderProfile, requestMessages []AIChatRequestMessage) (aiChatRoundResult, error) {
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
	requestBody := map[string]any{
		"model":    profile.Model,
		"stream":   true,
		"messages": aiprovider.BuildOpenAIChatMessages(systemPrompt, toAIProviderRuntimeMessages(requestMessages), aiprovider.ResolvePromptCacheStrategy(runtimeProfile, modelCapability)),
	}
	aiprovider.ApplySamplingParameters(requestBody, runtimeProfile)

	if reasoningEffort := aiprovider.GetEffectiveReasoningEffort(runtimeProfile, modelCapability); reasoningEffort != "" {
		requestBody["reasoning_effort"] = reasoningEffort
	} else if aiprovider.ShouldUseBinaryReasoning(runtimeProfile, modelCapability) {
		requestBody["thinking"] = map[string]any{
			"type": "enabled",
		}
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return result, err
	}
	endpoint := strings.TrimRight(profile.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
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

	for scanner.Scan() {
		if ctx.Err() != nil {
			finalizeRoundResult()
			return result, ctx.Err()
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, "data:") {
			// SSE 注释/控制行(如 ": keep-alive")可忽略。部分 OpenAI 兼容网关在其上游失败时,
			// 会先把流打开(HTTP 200),随后把一段不带 "data:" 前缀的原始 JSON 错误对象
			// 直接追加进流体再关闭。若不识别,该轮会以"空内容"结束并回落到兜底文案,
			// 上层既看不到错误也无法自动重试。此处将其识别为错误并返回。
			if strings.HasPrefix(line, "{") {
				var rawChunk aiChatCompatibleChunk
				if json.Unmarshal([]byte(line), &rawChunk) == nil && rawChunk.Error != nil {
					finalizeRoundResult()
					return result, fmt.Errorf("%s", aiChatCompatibleErrorText(rawChunk.Error))
				}
			}
			continue
		}

		chunkPayload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if chunkPayload == "" {
			continue
		}
		if chunkPayload == "[DONE]" {
			break
		}

		var chunk aiChatCompatibleChunk
		if err := json.Unmarshal([]byte(chunkPayload), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			finalizeRoundResult()
			return result, fmt.Errorf("%s", aiChatCompatibleErrorText(chunk.Error))
		}

		if chunk.Usage != nil {
			result.InputTokens = chunk.Usage.PromptTokens
			result.OutputTokens = chunk.Usage.CompletionTokens
			result.CacheReadTokens = chunk.Usage.CacheReadTokens
			if result.CacheReadTokens == 0 && chunk.Usage.PromptTokensDetails != nil {
				result.CacheReadTokens = chunk.Usage.PromptTokensDetails.CacheReadTokens
				if result.CacheReadTokens == 0 {
					result.CacheReadTokens = chunk.Usage.PromptTokensDetails.CachedTokens
				}
			}
		}
		for _, choice := range chunk.Choices {
			reasoningDelta := choice.Delta.ReasoningContent
			if reasoningDelta == "" {
				reasoningDelta = choice.Delta.Reasoning
			}
			emitReasoningDelta(reasoningDelta)

			bodyDelta, taggedReasoningDelta := contentParser.Feed(choice.Delta.Content)
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
