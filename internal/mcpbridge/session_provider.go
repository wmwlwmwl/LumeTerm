package mcpbridge

import "lumeterm/internal/mcpserver"

// SessionProvider 适配 Host 到 mcpserver.SessionProvider 和 ai.SessionProviderDelegate 接口。
type SessionProvider struct {
	Host Host
}

func (p SessionProvider) ListConnectedSessions() ([]mcpserver.SessionDescriptor, error) {
	return p.Host.ListSessionDescriptors()
}

func (p SessionProvider) GetWorkspaceState() string {
	return p.Host.GetWorkspaceState()
}
