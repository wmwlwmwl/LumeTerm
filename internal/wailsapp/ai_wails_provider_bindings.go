package wailsapp

// ponytail: provider 归一化函数已迁移至 internal/ai/provider_binding.go。
// 本文件仅保留 AIProviderBindings 结构体（含 config.ConfigManager 文件 I/O 依赖）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	ai "lumeterm/internal/ai"
	"lumeterm/internal/config"
)

type AIProviderBindings struct {
	configManager *config.ConfigManager
}

func NewAIProviderBindings(configManager *config.ConfigManager) *AIProviderBindings {
	return &AIProviderBindings{configManager: configManager}
}

func (b *AIProviderBindings) GetAIProviderState() ai.AIProviderState {
	if b == nil || b.configManager == nil {
		return ai.NormalizeAIProviderStateForBinding(ai.AIProviderState{Providers: []ai.AIProviderProfile{}})
	}
	registry := b.getAIProviderRegistry()
	return ai.NormalizeAIProviderStateForBinding(ai.AIProviderState{
		CurrentProviderID: b.readAICurrentProviderID(),
		Providers:         registry.Providers,
	})
}

func (b *AIProviderBindings) SaveAIProviderState(jsonStr string) error {
	state := ai.AIProviderState{
		Providers: []ai.AIProviderProfile{},
	}
	if strings.TrimSpace(jsonStr) != "" {
		if err := json.Unmarshal([]byte(jsonStr), &state); err != nil {
			return err
		}
	}
	normalized := ai.NormalizeAIProviderStateForBinding(state)
	if err := b.saveAIProviderRegistry(ai.AIProviderRegistry{Providers: normalized.Providers}); err != nil {
		return err
	}
	return b.saveAICurrentProviderID(normalized.CurrentProviderID)
}

func (b *AIProviderBindings) aiProviderRegistryPath() string {
	return filepath.Join(b.configManager.GetConfigDir(), "ai_providers.json")
}

func (b *AIProviderBindings) aiGlobalSettingsPath() string {
	return filepath.Join(b.configManager.GetConfigDir(), "ai_global_settings.json")
}

func (b *AIProviderBindings) getAIProviderRegistry() ai.AIProviderRegistry {
	registry := ai.AIProviderRegistry{
		Providers: []ai.AIProviderProfile{},
	}
	if b == nil || b.configManager == nil {
		registry.Providers = ai.NormalizeAIProviderProfilesForBinding(registry.Providers)
		return registry
	}
	data, err := os.ReadFile(b.aiProviderRegistryPath())
	if err == nil {
		_ = json.Unmarshal(data, &registry)
	}
	registry.Providers = ai.NormalizeAIProviderProfilesForBinding(registry.Providers)
	return registry
}

func (b *AIProviderBindings) saveAIProviderRegistry(registry ai.AIProviderRegistry) error {
	if b == nil || b.configManager == nil {
		return nil
	}
	normalized := ai.AIProviderRegistry{
		Providers: ai.NormalizeAIProviderProfilesForBinding(registry.Providers),
	}
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(b.aiProviderRegistryPath(), data, 0600)
}

func (b *AIProviderBindings) readAICurrentProviderID() string {
	if b == nil || b.configManager == nil {
		return ""
	}
	payload := map[string]interface{}{}
	data, err := os.ReadFile(b.aiGlobalSettingsPath())
	if err != nil {
		return ""
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	value, _ := payload["currentProviderId"].(string)
	return strings.TrimSpace(value)
}

func (b *AIProviderBindings) saveAICurrentProviderID(currentProviderID string) error {
	if b == nil || b.configManager == nil {
		return nil
	}
	payload := map[string]interface{}{}
	data, err := os.ReadFile(b.aiGlobalSettingsPath())
	if err == nil {
		_ = json.Unmarshal(data, &payload)
	}
	payload["currentProviderId"] = strings.TrimSpace(currentProviderID)
	nextData, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(b.aiGlobalSettingsPath(), nextData, 0600)
}
