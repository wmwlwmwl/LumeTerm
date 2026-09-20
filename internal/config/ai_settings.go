package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"lumeterm/internal/aitypes"
)

func (c *ConfigManager) aiProviderRegistryPath() string {
	return filepath.Join(c.configDir, "ai_providers.json")
}

func (c *ConfigManager) aiGlobalSettingsPath() string {
	return filepath.Join(c.configDir, "ai_global_settings.json")
}

func (c *ConfigManager) aiProxyNodesPath() string {
	return filepath.Join(c.configDir, "proxy_nodes.json")
}

func (c *ConfigManager) GetAIProviderRegistry() aitypes.AIProviderRegistry {
	registry := aitypes.AIProviderRegistry{Providers: []aitypes.AIProviderProfile{}}
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := os.ReadFile(c.aiProviderRegistryPath())
	if err == nil {
		_ = json.Unmarshal(data, &registry)
	}
	if registry.Providers == nil {
		registry.Providers = []aitypes.AIProviderProfile{}
	}
	return registry
}

func (c *ConfigManager) SaveAIProviderRegistry(registry aitypes.AIProviderRegistry) error {
	registry.Providers = normalizeSyncAIProviders(registry.Providers)
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return atomicWriteFile(c.aiProviderRegistryPath(), data, 0600)
}

func (c *ConfigManager) GetAIGlobalSettings() aitypes.AIGlobalSettings {
	settings := aitypes.LoadAIGlobalSettings(c.configDir)
	settings.ProxyNodes = nil
	return settings
}

func (c *ConfigManager) SaveAIGlobalSettings(settings aitypes.AIGlobalSettings) error {
	settings.ProxyNodes = nil
	if settings.UpdatedAt <= 0 {
		settings.UpdatedAt = time.Now().UnixMilli()
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return atomicWriteFile(c.aiGlobalSettingsPath(), data, 0600)
}

func (c *ConfigManager) GetAIProxyNodes() []aitypes.AIProxyNode {
	return aitypes.LoadAIProxyNodes(c.configDir)
}

func (c *ConfigManager) SaveAIProxyNodes(nodes []aitypes.AIProxyNode) error {
	nodes = normalizeSyncAIProxyNodes(nodes)
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return atomicWriteFile(c.aiProxyNodesPath(), data, 0600)
}
