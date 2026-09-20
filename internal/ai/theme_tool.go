package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lumeterm/internal/mcpserver"
)

const aiThemeToolName = "theme_tool"
const aiThemeToolScope = "theme_tuning"

type ThemeToolDelegate interface {
	HandleThemeToolRequest(ctx context.Context, request ThemeToolRequest) (ThemeToolResult, error)
	MarkThemeToolConversationUserConfirmed(conversationID string)
}

type ThemeToolRequest struct {
	ConversationID string `json:"conversationId,omitempty"`
	RequestID      string `json:"requestId,omitempty"`
	Action         string `json:"action,omitempty"`
	Slot           string `json:"slot,omitempty"`
	Request        string `json:"request,omitempty"`
}

type ThemeToolResult struct {
	Action         string                 `json:"action,omitempty"`
	Status         string                 `json:"status,omitempty"`
	DraftID        string                 `json:"draftId,omitempty"`
	Slot           string                 `json:"slot,omitempty"`
	SourceThemeID  string                 `json:"sourceThemeId,omitempty"`
	Theme          map[string]interface{} `json:"theme,omitempty"`
	AppliedPaths   []string               `json:"appliedPaths,omitempty"`
	Warnings       []string               `json:"warnings,omitempty"`
	InvalidPaths   []string               `json:"invalidPaths,omitempty"`
	SuggestedPaths []string               `json:"suggestedPaths,omitempty"`
	FieldMap       map[string]interface{} `json:"fieldMap,omitempty"`
	Settings       map[string]interface{} `json:"settings,omitempty"`
	CommittedTheme map[string]interface{} `json:"committedTheme,omitempty"`
	Result         string                 `json:"result,omitempty"`
}

func normalizeAIToolScope(value string) string {
	if strings.TrimSpace(strings.ToLower(value)) == aiThemeToolScope {
		return aiThemeToolScope
	}
	return ""
}

func isAIThemeToolScope(value string) bool {
	return normalizeAIToolScope(value) == aiThemeToolScope
}

func allowedAIToolNamesForScope(scope string) map[string]struct{} {
	if !isAIThemeToolScope(scope) {
		return nil
	}
	return map[string]struct{}{
		aiThemeToolName:        {},
		"ask_followup_question": {},
		"attempt_completion":    {},
	}
}

func validateAIParsedToolScope(scope string, tools []aiParsedToolUse) error {
	allowed := allowedAIToolNamesForScope(scope)
	if len(allowed) == 0 {
		for _, tool := range tools {
			if strings.TrimSpace(tool.Name) == aiThemeToolName {
				return fmt.Errorf("%s is only allowed in the theme tool scope", aiThemeToolName)
			}
		}
		return nil
	}
	for _, tool := range tools {
		toolName := strings.TrimSpace(tool.Name)
		if _, ok := allowed[toolName]; ok {
			continue
		}
		return fmt.Errorf("%s is not allowed in the current tool scope", toolName)
	}
	return nil
}

func normalizeAIThemeToolSlot(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "light":
		return "light"
	case "dark":
		return "dark"
	default:
		return ""
	}
}

func buildAIThemeToolScopedToolDefinitions(slot string) []mcpserver.ToolDefinition {
	return []mcpserver.ToolDefinition{
		themeToolAIChatToolDefinition(slot),
		aiAskFollowupQuestionToolDefinition(),
		mcpserver.AttemptCompletionToolDefinition(),
	}
}

func themeToolAIChatToolDefinition(slot string) mcpserver.ToolDefinition {
	slotValue := normalizeAIThemeToolSlot(slot)
	slotHint := ""
	if slotValue != "" {
		slotHint = " Current tuning slot: " + slotValue + "."
	}
	return mcpserver.ToolDefinition{
		Name: "theme_tool",
		Description: "Tune the current theme package inside a scoped live-preview workflow." + slotHint + " The first call must use action=help. Help returns the full current draft JSON and the private component field map. Supported actions: help, inspect, preview, commit, revert. preview only accepts connected private component fields. Global tokens and unsupported fields are blocked. commit accepts optional request JSON with name and description. revert discards the temporary preview without writing files.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Required action name. Use help first, then inspect, preview, commit, or revert.",
					"enum":        []string{"help", "inspect", "preview", "commit", "revert"},
				},
				"slot": map[string]any{
					"type":        "string",
					"description": "Theme slot for the first help call. Use light or dark.",
					"enum":        []string{"light", "dark"},
				},
				"request": map[string]any{
					"type":        "string",
					"description": "Optional JSON payload. preview uses a private-component patch JSON. commit may use name and description.",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func compactAINonEmptyLines(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

func buildAIThemeToolPromptSection(slot string) string {
	definitions := buildAIThemeToolScopedToolDefinitions(slot)
	sections := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		sections = append(sections, formatAIChatToolDefinition(definition, ""))
	}
	return "# Tools\n\n" + strings.Join(sections, "\n\n")
}

func buildAIThemeToolSystemPrompt(payload AIChatRequestPayload) string {
	slot := normalizeAIThemeToolSlot(payload.ToolScopeSlot)
	if slot == "" {
		slot = "dark"
	}
	lines := compactAINonEmptyLines([]string{
		"You are the AI theme tuning assistant.",
		"You are operating inside a scoped tool mode for live theme tuning.",
		"Only the tools listed below are allowed.",
		"Your first reply must be a direct XML theme_tool help call for the current slot.",
		"Help returns the full current draft JSON and a private component field map.",
		"Global tokens are forbidden in theme tuning. Only listed private component fields may be previewed.",
		"After the help result arrives, do not modify the theme yet.",
		"You must ask the user how they want the colors to change by using ask_followup_question before any preview call.",
		"Do not call preview or commit until the user has answered that follow-up question.",
		"Preview calls with unsupported fields or global tokens will be blocked and will return suggested private field paths.",
		"After the user answers, use inspect if you need the full draft again, use preview for temporary live updates, commit to write a new user theme package and bind the current slot, or revert to discard the temporary preview.",
		"Do not use any tool that is not listed below.",
		"Do not output any extra text outside direct XML tool replies.",
		"Current theme slot: " + slot + ".",
		buildPromptBuilderLanguagePreferenceInstruction(),
		buildAIThemeToolPromptSection(slot),
	})
	return strings.TrimSpace(strings.Join(lines, "\n\n"))
}

func buildAIThemeToolScopedHelpPayload(themeResult ThemeToolResult) map[string]interface{} {
	payload := map[string]interface{}{
		"scope":          aiThemeToolScope,
		"slot":           themeResult.Slot,
		"draftId":        themeResult.DraftID,
		"sourceThemeId":  themeResult.SourceThemeID,
		"allowedActions": []string{"help", "inspect", "preview", "commit", "revert"},
		"tools":          buildAIThemeToolScopedToolDefinitions(themeResult.Slot),
	}
	if len(themeResult.Theme) > 0 {
		payload["theme"] = themeResult.Theme
	}
	if len(themeResult.FieldMap) > 0 {
		payload["fieldMap"] = themeResult.FieldMap
	}
	if strings.TrimSpace(themeResult.Result) != "" {
		payload["message"] = strings.TrimSpace(themeResult.Result)
	}
	return payload
}

func buildAIThemeToolResultPayload(themeResult ThemeToolResult) map[string]interface{} {
	payload := map[string]interface{}{
		"action": themeResult.Action,
		"status": themeResult.Status,
	}
	if strings.TrimSpace(themeResult.DraftID) != "" {
		payload["draftId"] = themeResult.DraftID
	}
	if strings.TrimSpace(themeResult.Slot) != "" {
		payload["slot"] = themeResult.Slot
	}
	if strings.TrimSpace(themeResult.SourceThemeID) != "" {
		payload["sourceThemeId"] = themeResult.SourceThemeID
	}
	if strings.TrimSpace(themeResult.Result) != "" {
		payload["message"] = strings.TrimSpace(themeResult.Result)
	}
	switch strings.TrimSpace(themeResult.Action) {
	case "help":
		payload = buildAIThemeToolScopedHelpPayload(themeResult)
		payload["action"] = themeResult.Action
		payload["status"] = themeResult.Status
	case "inspect":
		if len(themeResult.Theme) > 0 {
			payload["theme"] = themeResult.Theme
		}
	case "preview":
		if len(themeResult.AppliedPaths) > 0 {
			payload["appliedPaths"] = themeResult.AppliedPaths
		}
		if len(themeResult.Warnings) > 0 {
			payload["warnings"] = themeResult.Warnings
		}
		if len(themeResult.InvalidPaths) > 0 {
			payload["invalidPaths"] = themeResult.InvalidPaths
		}
		if len(themeResult.SuggestedPaths) > 0 {
			payload["suggestedPaths"] = themeResult.SuggestedPaths
		}
		if len(themeResult.FieldMap) > 0 {
			payload["fieldMap"] = themeResult.FieldMap
		}
	case "commit":
		if len(themeResult.Settings) > 0 {
			payload["settings"] = themeResult.Settings
		}
		if len(themeResult.CommittedTheme) > 0 {
			payload["committedTheme"] = themeResult.CommittedTheme
		}
	}
	return payload
}

func (a *Service) runAIChatThemeToolExecution(execution *aiToolExecutionState) {
	if a == nil || execution == nil || execution.Batch == nil {
		return
	}
	if !a.isAIChatToolExecutionCurrent(execution.RequestID, execution.ExecutionID) {
		return
	}

	profile := resolveAIExecutionProfile(execution)
	statusText := "已执行"
	rawResultText := ""
	uiResultText := ""
	stopAfterThisTool := false
	var resultPayload map[string]interface{}

	if a.themeToolDelegate == nil {
		statusText = "错误"
		rawResultText = "主题调色能力未就绪"
		uiResultText = rawResultText
		stopAfterThisTool = true
	} else {
		themeResult, err := a.themeToolDelegate.HandleThemeToolRequest(execution.ExecutionCtx, ThemeToolRequest{
			ConversationID: execution.Batch.Payload.ConversationID,
			RequestID:      execution.RequestID,
			Action:         execution.Tool.Params["action"],
			Slot:           execution.Tool.Params["slot"],
			Request:        execution.Tool.Params["request"],
		})
		if err != nil {
			if execution.isTerminated() || errors.Is(err, context.Canceled) {
				statusText = "已终止"
				rawResultText = "已终止"
				uiResultText = rawResultText
			} else {
				statusText = "错误"
				rawResultText = err.Error()
				uiResultText = rawResultText
			}
			stopAfterThisTool = true
		} else {
			resultPayload = buildAIThemeToolResultPayload(themeResult)
			rawResultText = formatAIRawToolResultContent(resultPayload)
			uiResultText = formatToolResultContent(resultPayload)
			thresholdedResult := buildAIResultContentWithThreshold(rawResultText, profile, a.GetAIGlobalSettings().ToolResultTokenThreshold)
			if thresholdedResult.Oversized {
				uiResultText = thresholdedResult.Content
				rawResultText = thresholdedResult.Content
			}
			switch strings.TrimSpace(themeResult.Action) {
			case "preview":
				if len(themeResult.Theme) > 0 {
					a.emitAIChatEvent(map[string]interface{}{
						"kind":      "theme_tool_preview",
						"requestId": execution.RequestID,
						"draftId":   themeResult.DraftID,
						"slot":      themeResult.Slot,
						"theme":     themeResult.Theme,
					})
				}
			case "revert":
				a.emitAIChatEvent(map[string]interface{}{
					"kind":      "theme_tool_reverted",
					"requestId": execution.RequestID,
					"draftId":   themeResult.DraftID,
				})
			case "commit":
				a.emitAIChatEvent(map[string]interface{}{
					"kind":      "theme_tool_committed",
					"requestId": execution.RequestID,
					"draftId":   themeResult.DraftID,
					"slot":      themeResult.Slot,
				})
			}
		}
	}

	if !a.isAIChatToolExecutionCurrent(execution.RequestID, execution.ExecutionID) {
		return
	}

	a.popAIChatToolExecutionIfMatches(execution.RequestID, execution.ExecutionID)
	if execution.Cancel != nil {
		execution.Cancel()
	}

	message := map[string]interface{}{
		"id":                 execution.ToolMessageID,
		"turnId":             execution.AssistantMessageID,
		"kind":               "tool",
		"actionLabel":        execution.Tool.Name,
		"title":              titleForParsedToolUse(execution.Tool),
		"summary":            summarizeParsedToolUse(execution.Tool),
		"code":               execution.Tool.RawXML,
		"status":             statusText,
		"result":             sanitizeAIToolResultText(uiResultText),
		"remainingFileEdits": getAIToolRemainingFileEdits(execution.Tool),
	}
	attachAIResultTokenEstimateMeta(message, buildAIChatToolResultContent(execution.Tool.Name, rawResultText), profile)
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "upsert_message",
		"requestId": execution.RequestID,
		"message":   message,
	})

	a.emitAIChatToolResultMessage(execution.RequestID, execution, rawResultText)
	a.emitAIChatToolExecutionPersistRequested(execution.RequestID)

	if stopAfterThisTool {
		execution.Batch.NextToolIndex = len(execution.Batch.ParsedTools)
		a.resumeAIChatAfterToolBatch(execution.RequestID, execution.Batch)
		return
	}

	execution.Batch.NextToolIndex++
	a.advanceAIChatToolBatch(execution.RequestID, execution.Batch)
}