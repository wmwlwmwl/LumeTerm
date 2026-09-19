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

const (
	aiProviderWebSearchValidationURL           = "https://www.baidu.com"
	aiProviderWebSearchValidationExpectedTitle = "百度"
	aiProviderWebSearchValidationPrompt        = "请访问 https://www.baidu.com,然后只回答这个网站的名称,不要输出任何解释."
	aiProviderWebSearchSupportedMessage        = "支持"
	aiProviderWebSearchUnsupportedMessage      = "不支持"
)

type AIProviderWebSearchValidationResult struct {
	Success       bool   `json:"success"`
	Message       string `json:"message"`
	ActualTitle   string `json:"actualTitle,omitempty"`
	RawOutput     string `json:"rawOutput,omitempty"`
	ExpectedTitle string `json:"expectedTitle"`
}

func normalizeAIProviderValidationProfile(profile AIProviderProfile) AIProviderProfile {
	profile.Provider = normalizeAIProviderProtocol(profile.Provider)
	profile.BaseURL = strings.TrimSpace(profile.BaseURL)
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	profile.Model = strings.TrimSpace(profile.Model)
	profile.DedicatedWebSearchProviderID = strings.TrimSpace(profile.DedicatedWebSearchProviderID)
	return profile
}

func (a *Service) resolveAIProviderWebSearchValidationProfile(profile AIProviderProfile) (AIProviderProfile, bool, error) {
	normalizedProfile := normalizeAIProviderValidationProfile(profile)
	if !normalizedProfile.DedicatedWebSearchEnabled {
		return normalizedProfile, false, nil
	}
	if normalizedProfile.DedicatedWebSearchProviderID == "" {
		return AIProviderProfile{}, false, fmt.Errorf("请先选择联网搜索供应商")
	}
	if a == nil || a.configManager == nil {
		return AIProviderProfile{}, false, fmt.Errorf("当前环境无法解析联网搜索供应商")
	}
	state := a.configManager.GetAIProviderState()
	for _, candidate := range state.Providers {
		if candidate.ID != normalizedProfile.DedicatedWebSearchProviderID {
			continue
		}
		if !aiprovider.CanBeDedicatedWebSearchCandidate(candidate.Provider) {
			return AIProviderProfile{}, false, fmt.Errorf("联网搜索供应商仅允许 Compatible 或 Responses")
		}
		return normalizeAIProviderValidationProfile(candidate), true, nil
	}
	return AIProviderProfile{}, false, fmt.Errorf("所选联网专用供应商不存在或已被删除")
}

func (a *Service) ValidateAIProviderWebSearch(jsonStr string) AIProviderWebSearchValidationResult {
	result := AIProviderWebSearchValidationResult{
		Success:       false,
		ExpectedTitle: aiProviderWebSearchValidationExpectedTitle,
	}
	profile := AIProviderProfile{}
	if strings.TrimSpace(jsonStr) != "" {
		if err := json.Unmarshal([]byte(jsonStr), &profile); err != nil {
			result.Message = aiProviderWebSearchUnsupportedMessage
			result.RawOutput = err.Error()
			return result
		}
	}
	resolvedProfile, _, err := a.resolveAIProviderWebSearchValidationProfile(profile)
	if err != nil {
		result.Message = aiProviderWebSearchUnsupportedMessage
		result.RawOutput = err.Error()
		return result
	}
	if resolvedProfile.BaseURL == "" || resolvedProfile.APIKey == "" || resolvedProfile.Model == "" {
		result.Message = aiProviderWebSearchUnsupportedMessage
		result.RawOutput = "请先完整填写基础 URL、API 密钥和模型"
		return result
	}

	requestBody := map[string]any{
		"model":  resolvedProfile.Model,
		"input":  aiProviderWebSearchValidationPrompt,
		"tools":  []map[string]string{{"type": "web_search"}},
		"store":  false,
		"stream": true,
	}

	requestBytes, err := json.Marshal(requestBody)
	if err != nil {
		result.Message = aiProviderWebSearchUnsupportedMessage
		result.RawOutput = err.Error()
		return result
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	endpoint := strings.TrimRight(resolvedProfile.BaseURL, "/") + "/responses"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBytes))
	if err != nil {
		result.Message = aiProviderWebSearchUnsupportedMessage
		result.RawOutput = err.Error()
		return result
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", aiprovider.GetUserAgent(""))
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Authorization", "Bearer "+resolvedProfile.APIKey)
	aiprovider.ApplyCustomHeaders(request.Header, toAIProviderRuntimeProfile(resolvedProfile).CustomHeaders)

	client, err := a.newAIHTTPClientForProfile(&resolvedProfile, 0)
	if err != nil {
		result.Message = aiProviderWebSearchUnsupportedMessage
		result.RawOutput = err.Error()
		return result
	}
	response, err := client.Do(request)
	if err != nil {
		result.Message = aiProviderWebSearchUnsupportedMessage
		result.RawOutput = err.Error()
		return result
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		bodyStr := readErrorBody(response, 4096)
		errorText := strings.TrimSpace(bodyStr)
		if errorText == "" {
			errorText = response.Status
		}
		result.Message = aiProviderWebSearchUnsupportedMessage
		result.RawOutput = errorText
		return result
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var outputBuilder strings.Builder
	for scanner.Scan() {
		if ctx.Err() != nil {
			result.Message = aiProviderWebSearchUnsupportedMessage
			result.RawOutput = ctx.Err().Error()
			return result
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var event struct {
			Type     string `json:"type"`
			Delta    string `json:"delta,omitempty"`
			Response *struct {
				OutputText string `json:"output_text,omitempty"`
			} `json:"response,omitempty"`
		}
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
	}

	if err := scanner.Err(); err != nil {
		result.Message = aiProviderWebSearchUnsupportedMessage
		result.RawOutput = err.Error()
		return result
	}

	actualTitle := strings.TrimSpace(outputBuilder.String())
	actualTitle = strings.TrimSpace(strings.Trim(actualTitle, "\"'`"))

	result.Success = actualTitle == aiProviderWebSearchValidationExpectedTitle
	result.ActualTitle = actualTitle
	if actualTitle != "" {
		result.RawOutput = actualTitle
	} else {
		result.RawOutput = "未返回内容"
	}
	if result.Success {
		result.Message = aiProviderWebSearchSupportedMessage
	} else {
		result.Message = aiProviderWebSearchUnsupportedMessage
	}
	return result
}
