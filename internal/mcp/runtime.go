package mcp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"lumeterm/internal/mcpserver"
)

const mcpListenAddr = "127.0.0.1:5779"

var mcpServerRegistry sync.Map
var mcpLogState = struct {
	mu    sync.Mutex
	lines []string
}{}

type serverInfo struct {
	URL          string                   `json:"url"`
	Transport    string                   `json:"transport"`
	Endpoint     string                   `json:"endpoint"`
	Instructions string                   `json:"instructions"`
	Logs         string                   `json:"logs"`
	Tools        []map[string]interface{} `json:"tools"`
}

/*
ServiceSettings defines exposure controls for the local loopback MCP endpoint.
These controls are deliberately scoped to attack-surface reduction and misuse
prevention; they are not intended to establish a strong security boundary once
the workstation, or the current user context on that workstation, is already
compromised.

Threat model and non-goals:
1. The primary objective is to reduce exposure of the 127.0.0.1 MCP listener to:
   - browser-originated requests, including cross-origin web content and
     browser-mediated access paths to the local loopback service;
   - accidental or overly-permissive local integrations;
   - low-friction local invocation paths that were never meant to reach the
     SSH-backed MCP toolchain.
2. This configuration does not attempt to "re-trust" an already-compromised
   host. In particular, it does not claim to defend against an attacker who has
   already obtained arbitrary code execution in the same user context as this
   desktop application.
3. In that same-user post-compromise model, a local adversary can typically:
   - send direct HTTP requests to the loopback MCP listener;
   - inspect, replay, or synthesize user-space configuration and process state;
   - emulate a legitimate MCP client at the protocol layer;
   - operate with substantially the same ambient authority as the application.
4. Therefore, loopback-only binding, Origin-based gating, and static software
   toggles should be understood as friction controls. They materially reduce
   accidental exposure and low-complexity abuse, but they do not restore trust
   to a compromised workstation and should not be described as containment for
   same-user malware.
5. If future product requirements include security claims in a hostile local
   process model, stronger controls are required outside the scope of this
   implementation, such as explicit user approval, brokered authorization, a
   separate trust root, or stronger process / OS isolation.

Operational interpretation:
- Enabled controls whether the loopback MCP listener is exposed at all.
- AllowBrowserCalls controls whether requests carrying an Origin header are
  accepted. This is primarily a browser-exposure decision, not a defense
  against local code execution by an attacker already running as the same user.
*/
type ServiceSettings struct {
	Enabled           bool
	AllowBrowserCalls bool
}

func StartServer(host Host, settings ServiceSettings) {
	key := registryKey(host)
	if key == nil {
		return
	}
	if _, loaded := mcpServerRegistry.Load(key); loaded {
		return
	}
	if !settings.Enabled {
		appendMCPLog("MCP server disabled by settings")
		return
	}
	appendMCPLog("starting MCP server")
	service := mcpserver.NewService(NewSessionProvider(host))
	catalog := mcpserver.NewCatalog(service, NewFileProvider(host), NewCommandProvider(host), NewRemoteEditExecutor(host), NewTransferProvider(host))
	if carrier, ok := host.(ActivityReporterCarrier); ok {
		catalog.SetReporter(carrier.MCPActivityReporter())
	}
	if carrier, ok := host.(ReconnectCarrier); ok {
		catalog.SetDisconnectTracker(carrier.MCPDisconnectTracker())
		catalog.SetReconnectProvider(carrier.MCPReconnectProvider())
	}
	allowedOrigins := []string{mcpserver.BrowserCallsDisabledOriginSentinel}
	if settings.AllowBrowserCalls {
		allowedOrigins = nil
	}
	server := mcpserver.NewServer(
		mcpserver.ServerConfig{
			Addr:           mcpListenAddr,
			Endpoint:       "/mcp",
			AllowedOrigins: allowedOrigins,
			ServerInfo: mcpserver.Implementation{
			Name:        "lumeterm",
			Title:       "LumeTerm MCP Server",
			Version:     "0.1.0",
			Description: "MCP server for connected LumeTerm terminal sessions",
		},
		Instructions: "Call list_connected_sessions first and use the returned session_id for subsequent SSH-scoped tools. If a tool reports that the session's server is disconnected, call reconnect_server with that session_id to restore the connection, then retry.",
			Logger:       appendMCPLog,
		},
		catalog,
	)
	if err := server.Start(); err != nil {
		appendMCPLog(fmt.Sprintf("MCP server start failed: %v", err))
		log.Printf("mcp server start failed: %v", err)
		return
	}
	mcpServerRegistry.Store(key, server)
	appendMCPLog(fmt.Sprintf("MCP server listening on %s", server.URL()))
	log.Printf("mcp server listening on %s", server.URL())
}

func StopServer(host Host) {
	key := registryKey(host)
	if key == nil {
		return
	}
	value, ok := mcpServerRegistry.LoadAndDelete(key)
	if !ok {
		return
	}
	server, ok := value.(*mcpserver.Server)
	if !ok || server == nil {
		return
	}
	appendMCPLog("stopping MCP server")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Close(ctx); err != nil {
		appendMCPLog(fmt.Sprintf("MCP server stop failed: %v", err))
		log.Printf("mcp server stop failed: %v", err)
		return
	}
	appendMCPLog("MCP server stopped")
}

func GetServerInfo(host Host, settings ServiceSettings) map[string]interface{} {
	server := getMCPServer(host)
	tools := buildMCPToolDefinitions(host)
	if !settings.Enabled || server == nil {
		return map[string]interface{}{
			"url":          "",
			"transport":    "streamable-http",
			"endpoint":     "/mcp",
			"instructions": "Call list_connected_sessions first; if a tool reports the session's server is disconnected, call reconnect_server to restore it.",
			"logs":         getMCPLogText(),
			"tools":        tools,
		}
	}
	info := serverInfo{
		URL:          server.URL(),
		Transport:    "streamable-http",
		Endpoint:     "/mcp",
		Instructions: "Call list_connected_sessions first, then use the returned session_id for subsequent tools. If a tool reports that the session's server is disconnected, call reconnect_server with that session_id, then retry.",
		Logs:         getMCPLogText(),
		Tools:        tools,
	}
	return map[string]interface{}{
		"url":          info.URL,
		"transport":    info.Transport,
		"endpoint":     info.Endpoint,
		"instructions": info.Instructions,
		"logs":         info.Logs,
		"tools":        info.Tools,
	}
}

func registryKey(host Host) any {
	if host == nil {
		return nil
	}
	return host.RegistryKey()
}

func getMCPServer(host Host) *mcpserver.Server {
	key := registryKey(host)
	if key == nil {
		return nil
	}
	value, ok := mcpServerRegistry.Load(key)
	if !ok {
		return nil
	}
	server, ok := value.(*mcpserver.Server)
	if !ok {
		return nil
	}
	return server
}

func buildMCPToolDefinitions(host Host) []map[string]interface{} {
	if host == nil {
		return []map[string]interface{}{}
	}
	service := mcpserver.NewService(NewSessionProvider(host))
	catalog := mcpserver.NewCatalog(service, NewFileProvider(host), NewCommandProvider(host), NewRemoteEditExecutor(host), NewTransferProvider(host))
	// 与 StartServer 一致地注入重连能力,保证工具列表展示与服务端实际下发一致。
	if carrier, ok := host.(ReconnectCarrier); ok {
		catalog.SetReconnectProvider(carrier.MCPReconnectProvider())
	}
	definitions := catalog.List()
	result := make([]map[string]interface{}, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, map[string]interface{}{
			"name":        definition.Name,
			"description": definition.Description,
		})
	}
	return result
}

func appendMCPLog(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	mcpLogState.mu.Lock()
	defer mcpLogState.mu.Unlock()
	line := time.Now().Format("2006-01-02 15:04:05") + " " + message
	mcpLogState.lines = append(mcpLogState.lines, line)
	if len(mcpLogState.lines) > 200 {
		mcpLogState.lines = append([]string(nil), mcpLogState.lines[len(mcpLogState.lines)-200:]...)
	}
}

func getMCPLogText() string {
	mcpLogState.mu.Lock()
	defer mcpLogState.mu.Unlock()
	return strings.Join(mcpLogState.lines, "\n")
}