package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lumeterm/internal/aitypes"
)

// Types and normalization/persistence helpers live in internal/aitypes so that
// config/sshmanager can use them without importing this package.
type (
	AISlashCommand              = aitypes.AISlashCommand
	AICollaborationPromptPreset = aitypes.AICollaborationPromptPreset
	AIProxyNode                 = aitypes.AIProxyNode
	AIGlobalSettings            = aitypes.AIGlobalSettings
)

var (
	defaultAIGlobalSettings        = aitypes.DefaultAIGlobalSettings
	normalizeAIStringList          = aitypes.NormalizeAIStringList
	normalizeAIExecuteApprovalMode = aitypes.NormalizeAIExecuteApprovalMode
	normalizeAIProxyType           = aitypes.NormalizeAIProxyType
	normalizeAIProxyNodes          = aitypes.NormalizeAIProxyNodes
	aiProxyNodesPathForConfigDir   = aitypes.AIProxyNodesPathForConfigDir
	loadAIProxyNodesFromPath       = aitypes.LoadAIProxyNodesFromPath
	LoadAIProxyNodes               = aitypes.LoadAIProxyNodes
	normalizeAIGlobalSettings      = aitypes.NormalizeAIGlobalSettings
	aiGlobalSettingsContentEqual   = aitypes.AIGlobalSettingsContentEqual
	LoadAIGlobalSettings           = aitypes.LoadAIGlobalSettings
)

func (c *configBridge) aiGlobalSettingsPath() string {
	return filepath.Join(c.configDir, "ai_global_settings.json")
}

func (c *configBridge) aiProxyNodesPath() string {
	return aiProxyNodesPathForConfigDir(c.configDir)
}

func (c *configBridge) GetAIProxyNodes() []AIProxyNode {
	if c == nil {
		return []AIProxyNode{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if nodes, ok := loadAIProxyNodesFromPath(c.aiProxyNodesPath()); ok {
		return nodes
	}
	return []AIProxyNode{}
}

func (c *configBridge) SaveAIProxyNodes(nodes []AIProxyNode) error {
	if c == nil {
		return nil
	}
	normalized := normalizeAIProxyNodes(nodes)
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return atomicWriteFile(c.aiProxyNodesPath(), data, 0600)
}

func (c *configBridge) GetAIGlobalSettings() AIGlobalSettings {
	settings := defaultAIGlobalSettings()
	if c == nil {
		return settings
	}
	c.mu.RLock()
	data, err := os.ReadFile(c.aiGlobalSettingsPath())
	proxyNodes, ok := loadAIProxyNodesFromPath(c.aiProxyNodesPath())
	c.mu.RUnlock()
	if err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	if ok {
		settings.ProxyNodes = proxyNodes
	} else {
		settings.ProxyNodes = []AIProxyNode{}
	}
	return normalizeAIGlobalSettings(settings)
}

func (c *configBridge) SaveAIGlobalSettings(settings AIGlobalSettings) error {
	if c == nil {
		return nil
	}
	settings.ProxyNodes = LoadAIProxyNodes(c.configDir)
	normalized := normalizeAIGlobalSettings(settings)
	normalized.ProxyNodes = nil

	c.mu.Lock()
	defer c.mu.Unlock()
	existing := defaultAIGlobalSettings()
	if data, err := os.ReadFile(c.aiGlobalSettingsPath()); err == nil {
		_ = json.Unmarshal(data, &existing)
	}
	existing = normalizeAIGlobalSettings(existing)
	existing.ProxyNodes = nil
	if aiGlobalSettingsContentEqual(existing, normalized) {
		return nil
	}

	retentionCountChanged := existing.ConversationAutoBackupRetentionCount != normalized.ConversationAutoBackupRetentionCount
	normalized.UpdatedAt = time.Now().UnixMilli()
	settingsData, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteFile(c.aiGlobalSettingsPath(), settingsData, 0600); err != nil {
		return err
	}
	if retentionCountChanged {
		c.trimAllAIConversationBackupsLocked(normalized.ConversationAutoBackupRetentionCount)
	}
	return nil
}

func (a *Service) GetAIGlobalSettings() AIGlobalSettings {
	if a == nil || a.configManager == nil {
		return defaultAIGlobalSettings()
	}
	return a.configManager.GetAIGlobalSettings()
}

func (a *Service) SaveAIGlobalSettings(jsonStr string) error {
	settings := defaultAIGlobalSettings()
	if strings.TrimSpace(jsonStr) != "" {
		if err := json.Unmarshal([]byte(jsonStr), &settings); err != nil {
			return err
		}
	}
	if a == nil || a.configManager == nil {
		return nil
	}
	if err := a.configManager.SaveAIGlobalSettings(settings); err != nil {
		return err
	}
	// AI 对话日志开关变更后立即生效,无需重启。
	setAIDebugLogEnabled(settings.AIDebugLogEnabled)
	return nil
}
