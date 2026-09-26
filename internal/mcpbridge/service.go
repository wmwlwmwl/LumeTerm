package mcpbridge

import (
	ai "lumeterm/internal/ai"
	mcp "lumeterm/internal/mcp"
)

// LoadServiceSettings 从 AI 全局设置中提取 MCP 服务配置。
func LoadServiceSettings(configDir string) mcp.ServiceSettings {
	settings := ai.LoadAIGlobalSettings(configDir)
	return mcp.ServiceSettings{
		Enabled:              settings.MCPEnabled,
		AllowBrowserCalls:    settings.MCPAllowBrowserCalls,
		FollowLatestTerminal: settings.MCPTerminalFollowLatest,
	}
}

// ApplyServiceState 根据 AI 设置重启/停止 MCP 服务。
func ApplyServiceState(configDir string, host Host) {
	settings := LoadServiceSettings(configDir)
	mcp.StopServer(host)
	if !settings.Enabled {
		return
	}
	mcp.StartServer(host, settings)
}

// StartServer 初始化客户端 hub 并启动 MCP 服务。
func StartServer(configDir string, host Host) {
	mcp.InitializeClientHub(configDir)
	ApplyServiceState(configDir, host)
}

// StopServer 停止 MCP 服务。
func StopServer(host Host) {
	mcp.StopServer(host)
}

// InitOutputCompression 从配置加载并应用 MCP 输出压缩设置。
func InitOutputCompression(configDir string) {
	mcp.ApplyOutputCompressionSettings(mcp.LoadOutputCompressionSettings(configDir))
}
