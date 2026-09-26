package mcpserver

import "fmt"

func listConnectedSessionsToolDefinition() ToolDefinition {
	return ToolDefinition{
		Name: "list_connected_sessions",
		Description: "List currently connected SSH terminal sessions and return the session_id required by subsequent MCP tools. Each session includes an address field (user@host:port) that distinguishes servers sharing the same display name. When a server has several terminal tabs, the entry marked is_latest_terminal is the most recently opened one; prefer it, and note that requests targeting an older terminal of the same server automatically run in the latest one.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (c *Catalog) callListConnectedSessions(arguments map[string]any) (any, error) {
	if c == nil || c.service == nil {
		return nil, ErrSessionProviderUnavailable
	}
	if err := validateNoArguments(arguments); err != nil {
		return nil, err
	}
	return c.service.ListConnectedSessions()
}

func validateNoArguments(arguments map[string]any) error {
	if len(arguments) == 0 {
		return nil
	}
	return fmt.Errorf("tool does not accept arguments")
}