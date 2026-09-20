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

	aiprovider "lumeterm/internal/ai/provider"
)

var aiLiveSearchTimezone = time.FixedZone("UTC+8", 8*3600)

func buildAILiveSearchPrompt(query string) string {
	currentTime := time.Now().In(aiLiveSearchTimezone).Format("2006-01-02 15:04:05")
	return "当前时区时间:" + currentTime + "\n" +
		"使用内置联网搜索工具以继续来完成本次任务.\n" +
		"内置联网搜索工具可能如下:\n" +
		"1. `web_search`\n" +
		"2. `web_search_call`\n" +
		"3. `web.run`\n" +
		"内置联网搜索能力的具体标识符,应由模型根据当前供应商协议,接口事件模型与工具编排语义自行判定.\n" +
		"可优先识别的专业术语包括:\n" +
		"1. 内置搜索工具类型标识符\n" +
		"2. 提供商原生搜索工具调用事件\n" +
		"3. 响应流中的搜索调用事件名称\n" +
		"4. 供应商搜索能力注入参数\n" +
		"5. 提供商专有的浏览或检索能力端点语义\n\n" +
		"您被安排的任务一个或多个搜索任务是:\n" + strings.TrimSpace(query)
}

func (a *Service) resolveAIProviderWebSearchRuntimeProfile(profile AIProviderProfile) (AIProviderProfile, error) {
	normalizedProfile := normalizeAIProviderValidationProfile(profile)
	if !normalizedProfile.WebSearchEnabled {
		return AIProviderProfile{}, fmt.Errorf("当前配置未启用可用的联网搜索工具")
	}
	if normalizedProfile.DedicatedWebSearchEnabled {
		resolvedProfile, _, err := a.resolveAIProviderWebSearchValidationProfile(normalizedProfile)
		if err != nil {
			return AIProviderProfile{}, err
		}
		if resolvedProfile.BaseURL == "" || resolvedProfile.APIKey == "" || resolvedProfile.Model == "" {
			return AIProviderProfile{}, fmt.Errorf("请先完整填写基础 URL、API 密钥和模型")
		}
		return resolvedProfile, nil
	}
	if normalizedProfile.BaseURL == "" || normalizedProfile.APIKey == "" || normalizedProfile.Model == "" {
		return AIProviderProfile{}, fmt.Errorf("请先完整填写基础 URL、API 密钥和模型")
	}
	return normalizedProfile, nil
}

func (a *Service) searchAIProviderWeb(ctx context.Context, requestID string, profile AIProviderProfile, query string, onProgress func(string)) (string, error) {
	normalizedQuery := strings.TrimSpace(query)
	if normalizedQuery == "" {
		return "", fmt.Errorf("query 不能为空")
	}

	resolvedProfile, err := a.resolveAIProviderWebSearchRuntimeProfile(profile)
	if err != nil {
		return "", err
	}

	modelCapability := aiprovider.ResolveModelCapability(resolvedProfile.Provider, resolvedProfile.Model)
	runtimeProfile := toAIProviderRuntimeProfile(resolvedProfile)

	requestBody := map[string]any{
		"model":  resolvedProfile.Model,
		"input":  buildAILiveSearchPrompt(normalizedQuery),
		"tools":  []map[string]string{{"type": "web_search"}},
		"store":  false,
		"stream": true,
	}
	aiprovider.ApplySamplingParameters(requestBody, runtimeProfile)

	if reasoningEffort := aiprovider.GetEffectiveReasoningEffort(runtimeProfile, modelCapability); reasoningEffort != "" {
		requestBody["reasoning"] = map[string]any{
			"effort":  reasoningEffort,
			"summary": "auto",
		}
		requestBody["include"] = []string{"reasoning.encrypted_content"}
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}

	endpoint := strings.TrimRight(resolvedProfile.BaseURL, "/") + "/responses"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", aiprovider.GetUserAgent(requestID))
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Authorization", "Bearer "+resolvedProfile.APIKey)
	aiprovider.ApplyCustomHeaders(request.Header, runtimeProfile.CustomHeaders)

	client, err := a.newAINeverTimeoutHTTPClientForProfile(&resolvedProfile)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		bodyStr := readErrorBody(response, 4096)
		errorText := strings.TrimSpace(bodyStr)
		if errorText == "" {
			errorText = response.Status
		}
		return "", fmt.Errorf("搜索失败:%s", errorText)
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var outputBuilder strings.Builder
	var lastProgressAt time.Time
	var lastProgressContent string

	for scanner.Scan() {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var event aiChatResponsesEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}

		switch event.Type {
		case "response.output_text.delta", "response.text.delta":
			if event.Delta != "" {
				outputBuilder.WriteString(event.Delta)
			}
		case "response.completed", "response.done":
			if outputBuilder.Len() == 0 && event.Response != nil && strings.TrimSpace(event.Response.OutputText) != "" {
				outputBuilder.WriteString(event.Response.OutputText)
			}
		}

		if onProgress != nil {
			currentContent := strings.TrimSpace(outputBuilder.String())
			if currentContent != "" && currentContent != lastProgressContent && (lastProgressAt.IsZero() || time.Since(lastProgressAt) >= 80*time.Millisecond) {
				lastProgressContent = currentContent
				lastProgressAt = time.Now()
				onProgress(currentContent)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("搜索失败:%s", err.Error())
	}

	content := strings.TrimSpace(outputBuilder.String())
	if content == "" {
		return "", fmt.Errorf("搜索失败:未返回内容")
	}

	if onProgress != nil && content != lastProgressContent {
		onProgress(content)
	}

	return content, nil
}
