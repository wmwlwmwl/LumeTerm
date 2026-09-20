package mcp

import "lumeterm/internal/mcpserver"

type activeFileManagerWorkspaceStateProvider interface {
	GetWorkspaceState() string
}

type SessionProvider struct {
	host Host
}

func NewSessionProvider(host Host) SessionProvider {
	return SessionProvider{host: host}
}

func (p SessionProvider) ListConnectedSessions() ([]mcpserver.SessionDescriptor, error) {
	if p.host == nil {
		return []mcpserver.SessionDescriptor{}, nil
	}
	return p.host.ListSessionDescriptors()
}

func (p SessionProvider) GetWorkspaceState() string {
	if provider, ok := p.host.(activeFileManagerWorkspaceStateProvider); ok {
		return provider.GetWorkspaceState()
	}
	return ""
}