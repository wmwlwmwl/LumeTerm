package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"lumeterm/internal/mcp"
	"lumeterm/internal/mcpserver"
)

const aiUseMCPToolName = "use_mcp_tool"
const aiAccessMCPResourceToolName = "access_mcp_resource"

var aiMCPInternalSchemaParamRegex = regexp.MustCompile(`^__.*__$`)

func buildAIMCPClientToolDefinitions() []mcpserver.ToolDefinition {
	hub := mcp.ClientHubInstance()
	if hub == nil {
		return nil
	}
	promptServers := getAIPromptEnabledConnectedServers(hub.GetServers())
	promptTools := hub.ListPromptTools()

	serverNames := make([]string, 0, len(promptServers))
	resourceRefs := make([]string, 0)
	for _, server := range promptServers {
		serverNames = append(serverNames, server.Name)
		for _, resource := range server.Resources {
			if strings.TrimSpace(resource.URI) == "" {
				continue
			}
			resourceRefs = append(resourceRefs, fmt.Sprintf("%s:%s", server.Name, resource.URI))
		}
		for _, template := range server.ResourceTemplates {
			if strings.TrimSpace(template.URITemplate) == "" {
				continue
			}
			resourceRefs = append(resourceRefs, fmt.Sprintf("%s:%s", server.Name, template.URITemplate))
		}
	}

	toolRefs := make([]string, 0, len(promptTools))
	for _, tool := range promptTools {
		toolRefs = append(toolRefs, fmt.Sprintf("%s:%s", tool.ServerName, tool.ToolName))
	}

	definitions := make([]mcpserver.ToolDefinition, 0, 2)
	if len(promptTools) > 0 {
		definitions = append(definitions, mcpserver.ToolDefinition{
			Name: aiUseMCPToolName,
			Description: "Call a configured MCP client tool from the integrated MCP client hub. Consult the MCP SERVERS section below for the exact server instructions, available tools, and each tool's input schema. Required arguments: server_name, tool_name, arguments. Available server/tool pairs: " + strings.Join(toolRefs, ", "),
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server_name": map[string]any{
						"type":        "string",
						"description": "Target MCP server name. Available: " + strings.Join(serverNames, ", "),
					},
					"tool_name": map[string]any{
						"type":        "string",
						"description": "Target MCP tool name. Available pairs: " + strings.Join(toolRefs, ", "),
					},
					"arguments": map[string]any{
						"type":        "string",
						"description": "JSON object string passed as MCP tool arguments. The MCP SERVERS section below lists the exact input schema for each tool. Use {} when no arguments are needed.",
					},
				},
				"required":             []string{"server_name", "tool_name", "arguments"},
				"additionalProperties": false,
			},
		})
	}
	if len(resourceRefs) > 0 {
		definitions = append(definitions, mcpserver.ToolDefinition{
			Name: aiAccessMCPResourceToolName,
			Description: "Read a configured MCP resource from the integrated MCP client hub. Consult the MCP SERVERS section below for server instructions, available resource templates, and direct resources. Required arguments: server_name, uri. Available server/resource pairs: " + strings.Join(resourceRefs, ", "),
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server_name": map[string]any{
						"type":        "string",
						"description": "Target MCP server name. Available: " + strings.Join(serverNames, ", "),
					},
					"uri": map[string]any{
						"type":        "string",
						"description": "Target resource URI or URI template reference. Available pairs: " + strings.Join(resourceRefs, ", "),
					},
				},
				"required":             []string{"server_name", "uri"},
				"additionalProperties": false,
			},
		})
	}
	return definitions
}

func parseAIMCPServerSource(value string) mcp.ServerSource {
	switch strings.TrimSpace(value) {
	case string(mcp.ServerSourceEmbedded):
		return mcp.ServerSourceEmbedded
	default:
		return mcp.ServerSourceGlobal
	}
}

func resolveAIMCPServerSourceByName(serverName string, rawSource string) mcp.ServerSource {
	if strings.TrimSpace(rawSource) != "" {
		return parseAIMCPServerSource(rawSource)
	}
	hub := mcp.ClientHubInstance()
	if hub == nil {
		return mcp.ServerSourceGlobal
	}
	trimmedServerName := strings.TrimSpace(serverName)
	for _, server := range getAIPromptEnabledConnectedServers(hub.GetServers()) {
		if strings.TrimSpace(server.Name) != trimmedServerName {
			continue
		}
		return server.Source
	}
	return mcp.ServerSourceGlobal
}

func resolveAIMCPToolSource(serverName string, toolName string, rawSource string) mcp.ServerSource {
	if strings.TrimSpace(rawSource) != "" {
		return parseAIMCPServerSource(rawSource)
	}
	hub := mcp.ClientHubInstance()
	if hub == nil {
		return mcp.ServerSourceGlobal
	}
	trimmedServerName := strings.TrimSpace(serverName)
	trimmedToolName := strings.TrimSpace(toolName)
	for _, promptTool := range hub.ListPromptTools() {
		if strings.TrimSpace(promptTool.ServerName) != trimmedServerName {
			continue
		}
		if strings.TrimSpace(promptTool.ToolName) != trimmedToolName {
			continue
		}
		return promptTool.ServerSource
	}
	return resolveAIMCPServerSourceByName(serverName, rawSource)
}

func resolveAIMCPResourceSource(serverName string, uri string, rawSource string) mcp.ServerSource {
	if strings.TrimSpace(rawSource) != "" {
		return parseAIMCPServerSource(rawSource)
	}
	hub := mcp.ClientHubInstance()
	if hub == nil {
		return mcp.ServerSourceGlobal
	}
	trimmedServerName := strings.TrimSpace(serverName)
	trimmedURI := strings.TrimSpace(uri)
	for _, server := range getAIPromptEnabledConnectedServers(hub.GetServers()) {
		if strings.TrimSpace(server.Name) != trimmedServerName {
			continue
		}
		for _, resource := range server.Resources {
			if strings.TrimSpace(resource.URI) == trimmedURI {
				return server.Source
			}
		}
		for _, template := range server.ResourceTemplates {
			if strings.TrimSpace(template.URITemplate) == trimmedURI {
				return server.Source
			}
		}
	}
	return resolveAIMCPServerSourceByName(serverName, rawSource)
}

func isAIMCPClientToolAlwaysAllowed(tool aiParsedToolUse) bool {
	if strings.TrimSpace(tool.Name) != aiUseMCPToolName {
		return false
	}
	hub := mcp.ClientHubInstance()
	if hub == nil {
		return false
	}
	serverName := strings.TrimSpace(tool.Params["server_name"])
	toolName := strings.TrimSpace(tool.Params["tool_name"])
	if serverName == "" || toolName == "" {
		return false
	}
	source := resolveAIMCPToolSource(serverName, toolName, tool.Params["source"])
	for _, promptTool := range hub.ListPromptTools() {
		if strings.TrimSpace(promptTool.ServerName) != serverName {
			continue
		}
		if promptTool.ServerSource != source {
			continue
		}
		if strings.TrimSpace(promptTool.ToolName) != toolName {
			continue
		}
		return promptTool.AlwaysAllow
	}
	return false
}

func parseAIMCPToolArguments(raw string) (map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}, nil
	}
	arguments := map[string]any{}
	if err := json.Unmarshal([]byte(trimmed), &arguments); err != nil {
		return nil, err
	}
	return arguments, nil
}

func buildAIMCPToolMessage(execution *aiToolExecutionState, serverName string, toolName string, args string, response string, status string, source mcp.ServerSource, profile AIProviderProfile, extra map[string]interface{}) map[string]interface{} {
	mergedExtra := map[string]interface{}{
		"source": string(source),
	}
	for key, value := range extra {
		mergedExtra[key] = value
	}
	message := map[string]interface{}{
		"id":         execution.ToolMessageID,
		"turnId":     execution.AssistantMessageID,
		"kind":       "mcp",
		"serverName": serverName,
		"toolName":   toolName,
		"args":       args,
		"response":   response,
		"status":     status,
		"extra":      mergedExtra,
	}
	if _, exists := mergedExtra["resultTokenEstimateDisplay"]; exists {
		return message
	}
	return attachAIResultTokenEstimateMeta(message, buildAIMCPToolResultContent(serverName, toolName, response), profile)
}

func (a *Service) runAIChatMCPClientToolExecution(execution *aiToolExecutionState) {
	if a == nil || execution == nil || execution.Batch == nil {
		return
	}
	hub := mcp.ClientHubInstance()
	if hub == nil {
		a.failAIChatToolPreview(execution.RequestID, execution.Batch, execution.Tool, "MCP 客户端中心不可用")
		return
	}
	serverName := strings.TrimSpace(execution.Tool.Params["server_name"])
	if serverName == "" {
		a.failAIChatToolPreview(execution.RequestID, execution.Batch, execution.Tool, "缺少服务名")
		return
	}
	var source mcp.ServerSource
	profile := resolveAIExecutionProfile(execution)
	statusText := "已执行"
	uiResultText := ""
	rawResultText := ""
	argsText := "{}"
	toolLabel := strings.TrimSpace(execution.Tool.Params["tool_name"])
	rawResultSource := ""
	switch execution.Tool.Name {
	case aiUseMCPToolName:
		if toolLabel == "" {
			a.failAIChatToolPreview(execution.RequestID, execution.Batch, execution.Tool, "缺少工具名")
			return
		}
		parsedArguments, err := parseAIMCPToolArguments(execution.Tool.Params["arguments"])
		if err != nil {
			a.failAIChatToolPreview(execution.RequestID, execution.Batch, execution.Tool, "参数 JSON 无效")
			return
		}
		source = resolveAIMCPToolSource(serverName, toolLabel, execution.Tool.Params["source"])
		callResult, err := hub.CallTool(serverName, source, toolLabel, parsedArguments)
		if err != nil {
			statusText = "错误"
			uiResultText = err.Error()
			rawResultText = err.Error()
			argsText = marshalMCPToolArgs(parsedArguments)
		} else {
			uiResultText = strings.TrimSpace(callResult.Response)
			rawResultText = callResult.Response
			rawResultSource = callResult.Response
			argsText = callResult.Args
			if callResult.IsError {
				statusText = "错误"
			} else if strings.TrimSpace(rawResultSource) != "" {
				thresholdedResult := buildAIResultContentWithThreshold(rawResultSource, profile, a.GetAIGlobalSettings().ToolResultTokenThreshold)
				if thresholdedResult.Oversized {
					uiResultText = thresholdedResult.Content
					rawResultText = thresholdedResult.Content
				}
			}
		}
	case aiAccessMCPResourceToolName:
		uri := strings.TrimSpace(execution.Tool.Params["uri"])
		if uri == "" {
			a.failAIChatToolPreview(execution.RequestID, execution.Batch, execution.Tool, "缺少资源 URI")
			return
		}
		toolLabel = "resource:" + uri
		source = resolveAIMCPResourceSource(serverName, uri, execution.Tool.Params["source"])
		readResult, err := hub.ReadResource(serverName, source, uri)
		if err != nil {
			statusText = "错误"
			uiResultText = err.Error()
			rawResultText = err.Error()
			argsText = marshalMCPAccessResourceArgs(uri)
		} else {
			uiResultText = strings.TrimSpace(readResult.Response)
			rawResultText = readResult.Response
			rawResultSource = readResult.Response
			argsText = marshalMCPAccessResourceArgs(uri)
			if strings.TrimSpace(rawResultSource) != "" {
				thresholdedResult := buildAIResultContentWithThreshold(rawResultSource, profile, a.GetAIGlobalSettings().ToolResultTokenThreshold)
				if thresholdedResult.Oversized {
					uiResultText = thresholdedResult.Content
					rawResultText = thresholdedResult.Content
				}
			}
		}
	default:
		a.failAIChatToolPreview(execution.RequestID, execution.Batch, execution.Tool, "当前工具不支持 MCP 客户端调用")
		return
	}
	if !a.isAIChatToolExecutionCurrent(execution.RequestID, execution.ExecutionID) {
		return
	}
	a.popAIChatToolExecutionIfMatches(execution.RequestID, execution.ExecutionID)
	if execution.Cancel != nil {
		execution.Cancel()
	}
	mcpExtra := map[string]interface{}{}
	if resultTokenEstimateMeta := buildAIResultTokenEstimateMeta(buildAIMCPToolResultContent(serverName, toolLabel, rawResultSource), profile); len(resultTokenEstimateMeta) > 0 {
		for key, value := range resultTokenEstimateMeta {
			mcpExtra[key] = value
		}
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "upsert_message",
		"requestId": execution.RequestID,
		"message":   buildAIMCPToolMessage(execution, serverName, toolLabel, argsText, uiResultText, statusText, source, profile, mcpExtra),
	})
	a.emitAIMCPToolResultMessage(execution.RequestID, execution, serverName, toolLabel, rawResultText)
	a.emitAIChatToolExecutionPersistRequested(execution.RequestID)
	if statusText != "已执行" {
		execution.Batch.NextToolIndex = len(execution.Batch.ParsedTools)
		a.resumeAIChatAfterToolBatch(execution.RequestID, execution.Batch)
		return
	}
	execution.Batch.NextToolIndex++
	a.advanceAIChatToolBatch(execution.RequestID, execution.Batch)
}

func marshalMCPToolArgs(arguments map[string]any) string {
	if len(arguments) == 0 {
		return "{}"
	}
	data, err := json.MarshalIndent(arguments, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func marshalMCPAccessResourceArgs(uri string) string {
	data, err := json.MarshalIndent(map[string]string{"uri": uri}, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func buildAIMCPPromptSections(existing []mcpserver.ToolDefinition) []mcpserver.ToolDefinition {
	clientDefinitions := buildAIMCPClientToolDefinitions()
	if len(clientDefinitions) == 0 {
		return existing
	}
	result := make([]mcpserver.ToolDefinition, 0, len(existing)+len(clientDefinitions))
	result = append(result, clientDefinitions...)
	result = append(result, existing...)
	return result
}

func isAIMCPClientToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case aiUseMCPToolName, aiAccessMCPResourceToolName:
		return true
	default:
		return false
	}
}

func getAIMCPClientPromptContext() string {
	hub := mcp.ClientHubInstance()
	if hub == nil {
		return ""
	}
	servers := getAIPromptEnabledConnectedServers(hub.GetServers())
	serverEntries := make([]string, 0, len(servers))
	for _, server := range servers {
		serverEntries = append(serverEntries, getAIMCPServerEntry(server, true))
	}
	connectedServersText := "(No MCP servers currently connected)"
	if len(serverEntries) > 0 {
		connectedServersText = strings.Join(serverEntries, "\n\n")
	}
	return strings.TrimSpace(getAIMCPServersSectionBasePrefix() + connectedServersText)
}

func getAIPromptEnabledConnectedServers(servers []mcp.ServerRuntime) []mcp.ServerRuntime {
	result := make([]mcp.ServerRuntime, 0, len(servers))
	for _, server := range servers {
		if server.Disabled || server.DisabledForPrompts || server.Status != "connected" {
			continue
		}
		result = append(result, server)
	}
	return result
}

func getAIMCPServersSectionBasePrefix() string {
	return `MCP SERVERS

The Model Context Protocol (MCP) enables communication between the system and MCP servers that provide additional tools and resources to extend your capabilities. MCP servers can be one of two types:

1. Local (Stdio-based) servers: These run locally on the user's machine and communicate via standard input/output
2. Remote (SSE-based) servers: These run on remote machines and communicate via Server-Sent Events (SSE) or Streamable HTTP over HTTP/HTTPS

# Connected MCP Servers

To use an MCP tool, use the use_mcp_tool tool.
To read an MCP resource, use the access_mcp_resource tool.
The connected servers below list each server's instructions, available tools, and exact input schemas when available. When an MCP tool lists an Input Schema below, you must satisfy that schema exactly when constructing the arguments JSON.

`
}

func getAIMCPServerEntry(server mcp.ServerRuntime, includeToolDescriptions bool) string {
	toolEntries := make([]string, 0)
	if includeToolDescriptions {
		for _, tool := range server.Tools {
			if !tool.EnabledForPrompt {
				continue
			}
			toolEntries = append(toolEntries, fmt.Sprintf("- %s: %s%s", tool.Name, strings.TrimSpace(tool.Description), formatAIMCPToolInputSchema(tool.InputSchema)))
		}
	}

	templateEntries := make([]string, 0, len(server.ResourceTemplates))
	for _, template := range server.ResourceTemplates {
		templateEntries = append(templateEntries, fmt.Sprintf("- %s (%s): %s", template.URITemplate, template.Name, strings.TrimSpace(template.Description)))
	}

	resourceEntries := make([]string, 0, len(server.Resources))
	for _, resource := range server.Resources {
		resourceEntries = append(resourceEntries, fmt.Sprintf("- %s (%s): %s", resource.URI, resource.Name, strings.TrimSpace(resource.Description)))
	}

	entry := getAIMCPServerHeading(server)
	if strings.TrimSpace(server.Instructions) != "" {
		entry += "\n\n### Instructions\n" + strings.TrimSpace(server.Instructions)
	}
	if len(toolEntries) > 0 {
		entry += "\n\n### Available Tools\n" + strings.Join(toolEntries, "\n\n")
	}
	if len(templateEntries) > 0 {
		entry += "\n\n### Resource Templates\n" + strings.Join(templateEntries, "\n")
	}
	if len(resourceEntries) > 0 {
		entry += "\n\n### Direct Resources\n" + strings.Join(resourceEntries, "\n")
	}
	return entry
}

func getAIMCPServerHeading(server mcp.ServerRuntime) string {
	config := mcp.ServerConfig{}
	if err := json.Unmarshal([]byte(server.Config), &config); err != nil {
		return "## " + server.Name
	}
	command := strings.TrimSpace(config.Command)
	if command == "" {
		return "## " + server.Name
	}
	argsText := ""
	if len(config.Args) > 0 {
		argsText = " " + strings.Join(config.Args, " ")
	}
	return fmt.Sprintf("## %s (`%s%s`)", server.Name, command, argsText)
}

func formatAIMCPToolInputSchema(schema map[string]any) string {
	if len(schema) == 0 {
		return ""
	}
	displaySchema := stripAIMCPInternalSchemaParams(schema)
	if len(displaySchema) == 0 {
		return ""
	}
	data, err := json.MarshalIndent(displaySchema, "", "  ")
	if err != nil {
		return ""
	}
	return "\n    Input Schema:\n" + indentAIMCPMultiline(string(data), "        ")
}

func stripAIMCPInternalSchemaParams(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return nil
	}
	stripped, ok := stripAIMCPInternalSchemaParamsNode(schema).(map[string]any)
	if !ok || len(stripped) == 0 {
		return nil
	}
	return stripped
}

func stripAIMCPInternalSchemaParamsNode(node any) any {
	switch typed := node.(type) {
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, stripAIMCPInternalSchemaParamsNode(item))
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, value := range typed {
			result[key] = value
		}
		if propertiesRaw, ok := typed["properties"]; ok {
			if properties, ok := propertiesRaw.(map[string]any); ok {
				nextProperties := make(map[string]any, len(properties))
				for propertyName, propertySchema := range properties {
					if aiMCPInternalSchemaParamRegex.MatchString(propertyName) {
						continue
					}
					nextProperties[propertyName] = stripAIMCPInternalSchemaParamsNode(propertySchema)
				}
				result["properties"] = nextProperties
			}
		}
		if requiredRaw, ok := typed["required"]; ok {
			if required, ok := requiredRaw.([]any); ok {
				nextRequired := make([]any, 0, len(required))
				for _, item := range required {
					name, ok := item.(string)
					if ok && aiMCPInternalSchemaParamRegex.MatchString(name) {
						continue
					}
					nextRequired = append(nextRequired, item)
				}
				result["required"] = nextRequired
			}
		}
		for _, key := range []string{"items", "anyOf", "oneOf", "allOf", "additionalProperties"} {
			if value, ok := typed[key]; ok {
				result[key] = stripAIMCPInternalSchemaParamsNode(value)
			}
		}
		return result
	default:
		return node
	}
}

func indentAIMCPMultiline(text string, indent string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = indent + line
	}
	return strings.Join(lines, "\n")
}

func (a *Service) emitAIMCPToolResultMessage(requestID string, execution *aiToolExecutionState, serverName string, toolName string, resultText string) {
	if a == nil || strings.TrimSpace(resultText) == "" {
		return
	}
	thresholdedResult := buildAIResultContentWithThreshold(resultText, resolveAIExecutionProfile(execution), a.GetAIGlobalSettings().ToolResultTokenThreshold)
	resultContent := thresholdedResult.Content
	if strings.TrimSpace(resultContent) == "" {
		resultContent = strings.TrimSpace(resultText)
	}
	if execution != nil && execution.Batch != nil {
		execution.Batch.RequestMessages = append(execution.Batch.RequestMessages, AIChatRequestMessage{
			Role:    "user",
			Content: buildAIMCPToolResultContent(serverName, toolName, resultContent),
		})
	}
	toolMessageID := ""
	if execution != nil {
		toolMessageID = execution.ToolMessageID
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "api_message_append",
		"requestId": requestID,
		"message": map[string]interface{}{
			"messageId":    fmt.Sprintf("api-mcp-result-%d", time.Now().UnixNano()),
			"role":         "user",
			"content":      buildAIMCPToolResultContent(serverName, toolName, resultContent),
			"uiMessageIds": []string{toolMessageID},
			"ts":           time.Now().UnixMilli(),
		},
	})
}