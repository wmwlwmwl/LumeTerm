package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type rpcTransport interface {
	Start(context.Context) error
	Close() error
	Request(context.Context, string, map[string]any, any) error
}

func newRPCTransport(config ServerConfig, appendLog func(string)) (rpcTransport, error) {
	switch config.Type {
	case ServerTransportStdio:
		return newStdioTransport(config, appendLog), nil
	case ServerTransportSSE, ServerTransportStreamableHTTP:
		return newHTTPTransport(config, appendLog), nil
	default:
		return nil, fmt.Errorf("unsupported server transport type: %s", config.Type)
	}
}

func initializeRPCTransport(ctx context.Context, transport rpcTransport) (clientInitializeResult, error) {
	if err := transport.Start(ctx); err != nil {
		return clientInitializeResult{}, err
	}
	result := clientInitializeResult{}
	if err := transport.Request(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "LumeTerm",
			"version": "0.1.0",
		},
	}, &result); err != nil {
		_ = transport.Close()
		return clientInitializeResult{}, err
	}
	if err := transport.Request(ctx, "notifications/initialized", map[string]any{}, nil); err != nil {
		_ = transport.Close()
		return clientInitializeResult{}, err
	}
	return result, nil
}

func listServerTools(ctx context.Context, transport rpcTransport, config ServerConfig) ([]ServerTool, error) {
	result := clientListToolsResult{}
	if err := transport.Request(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	tools := make([]ServerTool, 0, len(result.Tools))
	alwaysAllowSet := make(map[string]struct{}, len(config.AlwaysAllow))
	for _, name := range config.AlwaysAllow {
		alwaysAllowSet[strings.TrimSpace(name)] = struct{}{}
	}
	disabledForPromptSet := make(map[string]struct{}, len(config.DisabledTools))
	for _, name := range config.DisabledTools {
		disabledForPromptSet[strings.TrimSpace(name)] = struct{}{}
	}
	for _, tool := range result.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		_, alwaysAllow := alwaysAllowSet[name]
		if !alwaysAllow {
			_, alwaysAllow = alwaysAllowSet["*"]
		}
		_, disabledForPrompt := disabledForPromptSet[name]
		tools = append(tools, ServerTool{
			Name:             name,
			Description:      strings.TrimSpace(tool.Description),
			InputSchema:      tool.InputSchema,
			AlwaysAllow:      alwaysAllow,
			EnabledForPrompt: !disabledForPrompt,
		})
	}
	return tools, nil
}

func listServerResources(ctx context.Context, transport rpcTransport) ([]ServerResource, error) {
	result := clientListResourcesResult{}
	if err := transport.Request(ctx, "resources/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	resources := make([]ServerResource, 0, len(result.Resources))
	for _, item := range result.Resources {
		uri := strings.TrimSpace(item.URI)
		name := strings.TrimSpace(item.Name)
		if uri == "" || name == "" {
			continue
		}
		resources = append(resources, ServerResource{
			URI:         uri,
			Name:        name,
			MimeType:    strings.TrimSpace(item.MimeType),
			Description: strings.TrimSpace(item.Description),
		})
	}
	return resources, nil
}

func listServerResourceTemplates(ctx context.Context, transport rpcTransport) ([]ServerResourceTemplate, error) {
	result := clientListResourceTemplatesResult{}
	if err := transport.Request(ctx, "resources/templates/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	templates := make([]ServerResourceTemplate, 0, len(result.ResourceTemplates))
	for _, item := range result.ResourceTemplates {
		uriTemplate := strings.TrimSpace(item.URITemplate)
		name := strings.TrimSpace(item.Name)
		if uriTemplate == "" || name == "" {
			continue
		}
		templates = append(templates, ServerResourceTemplate{
			URITemplate: uriTemplate,
			Name:        name,
			Description: strings.TrimSpace(item.Description),
			MimeType:    strings.TrimSpace(item.MimeType),
		})
	}
	return templates, nil
}

func callServerTool(ctx context.Context, transport rpcTransport, serverName string, toolName string, arguments map[string]any) (clientToolCallResult, error) {
	result := clientToolCallResult{}
	if err := transport.Request(ctx, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": arguments,
	}, &result); err != nil {
		return clientToolCallResult{}, err
	}
	return result, nil
}

func readServerResource(ctx context.Context, transport rpcTransport, uri string) (clientReadResourceResult, error) {
	result := clientReadResourceResult{}
	if err := transport.Request(ctx, "resources/read", map[string]any{
		"uri": uri,
	}, &result); err != nil {
		return clientReadResourceResult{}, err
	}
	return result, nil
}

func marshalCallArguments(arguments map[string]any) string {
	if len(arguments) == 0 {
		return "{}"
	}
	data, err := json.MarshalIndent(arguments, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func formatToolCallResponse(result clientToolCallResult) string {
	if len(result.Content) > 0 {
		parts := make([]string, 0, len(result.Content))
		for _, item := range result.Content {
			switch strings.TrimSpace(item.Type) {
			case "text":
				if strings.TrimSpace(item.Text) != "" {
					parts = append(parts, item.Text)
				}
			case "resource":
				if item.Resource != nil {
					data, err := json.MarshalIndent(item.Resource, "", "  ")
					if err == nil {
						parts = append(parts, string(data))
					}
				}
			case "resource_link":
				if strings.TrimSpace(item.URI) != "" {
					parts = append(parts, item.URI)
				}
			default:
				data, err := json.MarshalIndent(item, "", "  ")
				if err == nil {
					parts = append(parts, string(data))
				}
			}
		}
		if len(parts) > 0 {
			return strings.TrimSpace(strings.Join(parts, "\n\n"))
		}
	}
	if len(result.StructuredContent) > 0 {
		data, err := json.MarshalIndent(result.StructuredContent, "", "  ")
		if err == nil {
			return string(data)
		}
	}
	return ""
}

func formatResourceReadResponse(result clientReadResourceResult) string {
	if len(result.Contents) == 0 {
		return ""
	}
	data, err := json.MarshalIndent(result.Contents, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}