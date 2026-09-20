package aitypes

import (
	"fmt"
	"strings"
	"time"

	aiprovider "lumeterm/internal/ai/provider"
)

type AIProviderCustomHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type AIProviderProfile struct {
	ID                                     string                   `json:"id"`
	Name                                   string                   `json:"name"`
	Provider                               string                   `json:"provider"`
	Model                                  string                   `json:"model"`
	BaseURL                                string                   `json:"baseUrl"`
	APIKey                                 string                   `json:"apiKey"`
	CustomHeaders                          []AIProviderCustomHeader `json:"customHeaders,omitempty"`
	ModelTemperature                       *float64                 `json:"modelTemperature,omitempty"`
	ModelTopP                              *float64                 `json:"modelTopP,omitempty"`
	CacheStrategy                          string                   `json:"cacheStrategy"`
	OpenAIResponsesUsePromptCacheRetention bool                     `json:"openAiResponsesUsePromptCacheRetention"`
	OpenAIResponsesFinishOnCompletedEvent  bool                     `json:"openAiResponsesFinishOnCompletedEvent"`
	WebSearchEnabled                       bool                     `json:"webSearchEnabled"`
	DedicatedWebSearchEnabled              bool                     `json:"dedicatedWebSearchEnabled"`
	DedicatedWebSearchProviderID           string                   `json:"dedicatedWebSearchProviderId,omitempty"`
	DedicatedProxyEnabled                  bool                     `json:"dedicatedProxyEnabled"`
	DedicatedProxyID                       string                   `json:"dedicatedProxyId,omitempty"`
	ReasoningEffort                        string                   `json:"reasoningEffort"`
	EnableReasoningEffort                  bool                     `json:"enableReasoningEffort"`
	OpenAILegacyReasoningFormatEnabled     bool                     `json:"openAiLegacyReasoningFormatEnabled"`
	SystemPromptAppend                     string                   `json:"systemPromptAppend,omitempty"`
	SystemPromptPresetID                   string                   `json:"systemPromptPresetId,omitempty"`
	ModelMaxTokens                         int                      `json:"modelMaxTokens,omitempty"`
	ModelMaxThinkingTokens                 int                      `json:"modelMaxThinkingTokens,omitempty"`
	Pinned                                 bool                     `json:"pinned"`
	UpdatedAt                              int64                    `json:"updatedAt,omitempty"`
}

type AIProviderRegistry struct {
	Providers []AIProviderProfile `json:"providers"`
}

type AIProviderState struct {
	CurrentProviderID string              `json:"currentProviderId"`
	Providers         []AIProviderProfile `json:"providers"`
}

func NormalizeAIProviderProtocol(value string) string {
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

func NormalizeAIProviderCacheStrategy(value string) string {
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

func NormalizeAIProviderReasoningEffort(value string) string {
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

func NormalizeAIProviderCustomHeaders(headers []AIProviderCustomHeader) []AIProviderCustomHeader {
	if len(headers) == 0 {
		return nil
	}
	normalized := make([]AIProviderCustomHeader, 0, len(headers))
	indexByName := make(map[string]int, len(headers))
	for _, header := range headers {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		normalizedHeader := AIProviderCustomHeader{
			Name:  name,
			Value: strings.TrimSpace(header.Value),
		}
		key := strings.ToLower(name)
		if existingIndex, exists := indexByName[key]; exists {
			normalized[existingIndex] = normalizedHeader
			continue
		}
		indexByName[key] = len(normalized)
		normalized = append(normalized, normalizedHeader)
	}
	return normalized
}

func NormalizeAIProviderProfiles(profiles []AIProviderProfile) []AIProviderProfile {
	if profiles == nil {
		profiles = []AIProviderProfile{}
	}

	now := time.Now().UnixMilli()
	normalized := make([]AIProviderProfile, len(profiles))
	copy(normalized, profiles)

	for index := range normalized {
		profile := &normalized[index]
		if strings.TrimSpace(profile.ID) == "" {
			profile.ID = fmt.Sprintf("ai-provider-%d-%d", now, index)
		}
		if strings.TrimSpace(profile.Name) == "" {
			profile.Name = "未命名供应商"
		}
		profile.Provider = NormalizeAIProviderProtocol(profile.Provider)
		profile.Model = strings.TrimSpace(profile.Model)
		if profile.Model == "" {
			profile.Model = "未选择模型"
		}
		profile.BaseURL = strings.TrimSpace(profile.BaseURL)
		profile.APIKey = strings.TrimSpace(profile.APIKey)
		profile.CustomHeaders = NormalizeAIProviderCustomHeaders(profile.CustomHeaders)
		profile.DedicatedProxyID = strings.TrimSpace(profile.DedicatedProxyID)
		profile.SystemPromptAppend = strings.TrimSpace(strings.ReplaceAll(profile.SystemPromptAppend, "\r\n", "\n"))
		profile.SystemPromptPresetID = strings.TrimSpace(profile.SystemPromptPresetID)
		profile.CacheStrategy = NormalizeAIProviderCacheStrategy(profile.CacheStrategy)
		profile.ReasoningEffort = NormalizeAIProviderReasoningEffort(profile.ReasoningEffort)
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

func NormalizeAIProviderRegistry(registry AIProviderRegistry) AIProviderRegistry {
	registry.Providers = NormalizeAIProviderProfiles(registry.Providers)
	return registry
}

func NormalizeAIProviderState(state AIProviderState) AIProviderState {
	state.CurrentProviderID = strings.TrimSpace(state.CurrentProviderID)
	state.Providers = NormalizeAIProviderProfiles(state.Providers)

	validIDs := make(map[string]struct{}, len(state.Providers))
	for _, profile := range state.Providers {
		validIDs[profile.ID] = struct{}{}
	}

	if _, ok := validIDs[state.CurrentProviderID]; !ok {
		state.CurrentProviderID = ""
	}

	return state
}
