package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	aiprovider "lumeterm/internal/ai/provider"
	"lumeterm/internal/aitypes"
)

type AIProviderProfile = aitypes.AIProviderProfile

type AIProviderPromptCachePolicy = aiprovider.ResponsesPromptCachePolicy

type AIProviderRegistry = aitypes.AIProviderRegistry

type AIProviderState = aitypes.AIProviderState

var (
	normalizeAIProviderProtocol        = aitypes.NormalizeAIProviderProtocol
	normalizeAIProviderReasoningEffort = aitypes.NormalizeAIProviderReasoningEffort
	normalizeAIProviderRegistry        = aitypes.NormalizeAIProviderRegistry
	normalizeAIProviderState           = aitypes.NormalizeAIProviderState
)

func (c *configBridge) aiProviderRegistryPath() string {
	return filepath.Join(c.configDir, "ai_providers.json")
}

func (c *configBridge) GetAIProviderRegistry() AIProviderRegistry {
	registry := AIProviderRegistry{
		Providers: []AIProviderProfile{},
	}
	if c == nil {
		return normalizeAIProviderRegistry(registry)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := os.ReadFile(c.aiProviderRegistryPath())
	if err != nil {
		return normalizeAIProviderRegistry(registry)
	}
	_ = json.Unmarshal(data, &registry)
	return normalizeAIProviderRegistry(registry)
}

func (c *configBridge) SaveAIProviderRegistry(registry AIProviderRegistry) error {
	if c == nil {
		return nil
	}
	normalized := normalizeAIProviderRegistry(registry)
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return atomicWriteFile(c.aiProviderRegistryPath(), data, 0600)
}

func (c *configBridge) GetAIProviderState() AIProviderState {
	if c == nil {
		return normalizeAIProviderState(AIProviderState{Providers: []AIProviderProfile{}})
	}
	registry := c.GetAIProviderRegistry()
	globalSettings := c.GetAIGlobalSettings()
	return normalizeAIProviderState(AIProviderState{
		CurrentProviderID: globalSettings.CurrentProviderID,
		Providers:         registry.Providers,
	})
}

func (c *configBridge) SaveAIProviderState(state AIProviderState) error {
	if c == nil {
		return nil
	}
	normalized := normalizeAIProviderState(state)
	if err := c.SaveAIProviderRegistry(AIProviderRegistry{Providers: normalized.Providers}); err != nil {
		return err
	}
	globalSettings := c.GetAIGlobalSettings()
	globalSettings.CurrentProviderID = normalized.CurrentProviderID
	return c.SaveAIGlobalSettings(globalSettings)
}

func (a *Service) GetAIProviderState() AIProviderState {
	if a == nil || a.configManager == nil {
		return normalizeAIProviderState(AIProviderState{Providers: []AIProviderProfile{}})
	}
	return a.configManager.GetAIProviderState()
}

func (a *Service) GetAIProviderPromptCachePolicy(modelID string) AIProviderPromptCachePolicy {
	return aiprovider.GetResponsesPromptCachePolicy(modelID)
}

func (a *Service) SaveAIProviderState(jsonStr string) error {
	state := AIProviderState{
		Providers: []AIProviderProfile{},
	}
	if strings.TrimSpace(jsonStr) != "" {
		if err := json.Unmarshal([]byte(jsonStr), &state); err != nil {
			return err
		}
	}
	if a == nil || a.configManager == nil {
		return nil
	}
	return a.configManager.SaveAIProviderState(state)
}

func toAIProviderRuntimeProfile(profile AIProviderProfile) aiprovider.Profile {
	customHeaders := make([]aiprovider.CustomHeader, 0, len(profile.CustomHeaders))
	for _, header := range profile.CustomHeaders {
		customHeaders = append(customHeaders, aiprovider.CustomHeader{
			Name:  strings.TrimSpace(header.Name),
			Value: strings.TrimSpace(header.Value),
		})
	}
	return aiprovider.Profile{
		Provider:                               profile.Provider,
		Model:                                  profile.Model,
		BaseURL:                                profile.BaseURL,
		APIKey:                                 profile.APIKey,
		CustomHeaders:                          customHeaders,
		ModelTemperature:                       profile.ModelTemperature,
		ModelTopP:                              profile.ModelTopP,
		CacheStrategy:                          profile.CacheStrategy,
		OpenAIResponsesUsePromptCacheRetention: profile.OpenAIResponsesUsePromptCacheRetention,
		OpenAIResponsesFinishOnCompletedEvent:  profile.OpenAIResponsesFinishOnCompletedEvent,
		SystemPromptAppend:                     profile.SystemPromptAppend,
		ReasoningEffort:                        profile.ReasoningEffort,
		EnableReasoningEffort:                  profile.EnableReasoningEffort,
		OpenAILegacyReasoningFormatEnabled:     profile.OpenAILegacyReasoningFormatEnabled,
		ModelMaxTokens:                         profile.ModelMaxTokens,
		ModelMaxThinkingTokens:                 profile.ModelMaxThinkingTokens,
	}
}

func toAIProviderRuntimeCacheObjects(cacheObjects *AIConversationProviderCacheObjects) *aiprovider.ProviderCacheObjects {
	if cacheObjects == nil || cacheObjects.OpenAIResponses == nil {
		return nil
	}
	return &aiprovider.ProviderCacheObjects{
		OpenAIResponses: &aiprovider.OpenAIResponsesCacheObject{
			ResponseID:  strings.TrimSpace(cacheObjects.OpenAIResponses.ResponseID),
			Output:      aiprovider.CloneOpenAIResponsesOutputItems(cacheObjects.OpenAIResponses.Output),
			ReplayState: aiprovider.CloneOpenAIResponsesReplayState(cacheObjects.OpenAIResponses.ReplayState),
			Include:     normalizeAIStringList(cacheObjects.OpenAIResponses.Include),
			Store:       cacheObjects.OpenAIResponses.Store,
			CapturedAt:  cacheObjects.OpenAIResponses.CapturedAt,
		},
	}
}

func toAIProviderRuntimeMessages(messages []AIChatRequestMessage) []aiprovider.ChatMessage {
	converted := make([]aiprovider.ChatMessage, 0, len(messages))
	for _, message := range messages {
		converted = append(converted, aiprovider.ChatMessage{
			Role:          message.Role,
			Content:       message.Content,
			ContentBlocks: aiprovider.CloneOpenAIResponsesOutputItems(message.ContentBlocks),
			Images:        normalizeAIStringList(message.Images),
			CacheObjects:  toAIProviderRuntimeCacheObjects(message.CacheObjects),
		})
	}
	return converted
}
