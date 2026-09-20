package ai

// ponytail: 从 package main (ai_wails_provider_bindings.go) 提取的 provider 归一化函数。
// 仅依赖 internal/ai 自身类型 + internal/ai/provider 子包。
// AIProviderBindings 结构体（含 ConfigManager 依赖）仍留在 package main。

import (
	"strings"
	"time"

	aiprovider "lumeterm/internal/ai/provider"
)

func NormalizeAIProviderProtocolForBinding(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "compatible":
		return "Compatible"
	case "responses":
		return "Responses"
	case "messages":
		return "Messages"
	default:
		return "Compatible"
	}
}

func NormalizeAIProviderCacheStrategyForBinding(value string) string {
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

func NormalizeAIProviderReasoningEffortForBinding(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "disable":
		return "disable"
	case "none":
		return "none"
	case "minimal":
		return "minimal"
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "xhigh"
	case "max":
		return "max"
	default:
		return "disable"
	}
}

func NormalizeAIProviderProfilesForBinding(profiles []AIProviderProfile) []AIProviderProfile {
	if profiles == nil {
		profiles = []AIProviderProfile{}
	}
	now := time.Now().UnixMilli()
	normalized := make([]AIProviderProfile, len(profiles))
	copy(normalized, profiles)
	for index := range normalized {
		profile := &normalized[index]
		if strings.TrimSpace(profile.ID) == "" {
			profile.ID = "ai-provider-" + strings.TrimSpace(time.UnixMilli(now).Format("20060102150405")) + "-" + strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(time.UnixMilli(now+int64(index)).Format("15:04:05.000"), ":", ""), ".", ""), " ", ""))
		}
		if strings.TrimSpace(profile.Name) == "" {
			profile.Name = "未命名供应商"
		}
		profile.Provider = NormalizeAIProviderProtocolForBinding(profile.Provider)
		profile.Model = strings.TrimSpace(profile.Model)
		if profile.Model == "" {
			profile.Model = "未选择模型"
		}
		profile.BaseURL = strings.TrimSpace(profile.BaseURL)
		profile.APIKey = strings.TrimSpace(profile.APIKey)
		profile.DedicatedProxyID = strings.TrimSpace(profile.DedicatedProxyID)
		profile.SystemPromptAppend = strings.TrimSpace(strings.ReplaceAll(profile.SystemPromptAppend, "\r\n", "\n"))
		profile.SystemPromptPresetID = strings.TrimSpace(profile.SystemPromptPresetID)
		profile.CacheStrategy = NormalizeAIProviderCacheStrategyForBinding(profile.CacheStrategy)
		profile.ReasoningEffort = NormalizeAIProviderReasoningEffortForBinding(profile.ReasoningEffort)
		profile.EnableReasoningEffort = profile.EnableReasoningEffort || (profile.ReasoningEffort != "" && profile.ReasoningEffort != "disable") || profile.ModelMaxTokens > 0 || profile.ModelMaxThinkingTokens > 0
		if profile.ModelMaxTokens < 0 {
			profile.ModelMaxTokens = 0
		}
		if profile.ModelMaxThinkingTokens < 0 {
			profile.ModelMaxThinkingTokens = 0
		}
		if profile.ModelMaxTokens > 0 && profile.ModelMaxThinkingTokens > 0 {
			maxThinkingTokens := int(float64(profile.ModelMaxTokens) * 0.8)
			if maxThinkingTokens > 0 && profile.ModelMaxThinkingTokens > maxThinkingTokens {
				profile.ModelMaxThinkingTokens = maxThinkingTokens
			}
		}
		if profile.UpdatedAt == 0 {
			profile.UpdatedAt = now
		}
	}
	dedicatedCandidateIDs := make(map[string]struct{}, len(normalized))
	for _, profile := range normalized {
		if aiprovider.CanBeDedicatedWebSearchCandidate(profile.Provider) {
			dedicatedCandidateIDs[profile.ID] = struct{}{}
		}
	}
	for index := range normalized {
		profile := &normalized[index]
		if profile.DedicatedWebSearchProviderID == profile.ID {
			profile.DedicatedWebSearchProviderID = ""
		}
		if profile.DedicatedWebSearchEnabled {
			if _, ok := dedicatedCandidateIDs[profile.DedicatedWebSearchProviderID]; !ok || profile.DedicatedWebSearchProviderID == "" {
				replacement := ""
				for otherIndex := range normalized {
					if normalized[otherIndex].ID != profile.ID && aiprovider.CanBeDedicatedWebSearchCandidate(normalized[otherIndex].Provider) {
						replacement = normalized[otherIndex].ID
						break
					}
				}
				profile.DedicatedWebSearchProviderID = replacement
				profile.DedicatedWebSearchEnabled = replacement != ""
			}
		} else if profile.DedicatedWebSearchProviderID != "" {
			if _, ok := dedicatedCandidateIDs[profile.DedicatedWebSearchProviderID]; !ok {
				profile.DedicatedWebSearchProviderID = ""
			}
		}
	}
	return normalized
}

func NormalizeAIProviderStateForBinding(state AIProviderState) AIProviderState {
	state.CurrentProviderID = strings.TrimSpace(state.CurrentProviderID)
	state.Providers = NormalizeAIProviderProfilesForBinding(state.Providers)
	validIDs := make(map[string]struct{}, len(state.Providers))
	for _, profile := range state.Providers {
		validIDs[profile.ID] = struct{}{}
	}
	if _, ok := validIDs[state.CurrentProviderID]; !ok {
		state.CurrentProviderID = ""
	}
	return state
}
