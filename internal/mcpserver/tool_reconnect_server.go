package mcpserver

import "fmt"

func reconnectServerToolDefinition() ToolDefinition {
	return ToolDefinition{
		Name: "reconnect_server",
		Description: "Reconnect a disconnected SSH session in LumeTerm. Use this when another tool reports that the session's server is disconnected, or the session disappeared from list_connected_sessions. Pass the previously used session_id (the parent session id, or a child terminal id of that session). The parent session id survives the reconnect; if child terminal ids changed, the old_to_new mapping in the result lists the replacements. If the reconnect keeps failing, the user is notified automatically.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Previously returned session id (parent session or one of its child terminal ids) whose server is disconnected.",
				},
			},
			"required":              []string{"session_id"},
			"additionalProperties": false,
		},
	}
}

func (c *Catalog) callReconnectServer(arguments map[string]any) (any, error) {
	if c == nil || c.reconnectProvider == nil {
		return nil, fmt.Errorf("reconnect provider unavailable")
	}
	if err := validateAllowedArguments(arguments, "session_id"); err != nil {
		return nil, err
	}
	sessionID, err := requireStringArgument(arguments, "session_id")
	if err != nil {
		return nil, err
	}
	return c.reconnectProvider.ReconnectDisconnectedSession(sessionID)
}
