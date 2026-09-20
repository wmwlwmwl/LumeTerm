package ai

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"lumeterm/internal/aitypes"
	"lumeterm/internal/mcpserver"
)

type (
	ToolExecutionAction            = aitypes.ToolExecutionAction
	AIChatCommandTerminalCandidate = aitypes.AIChatCommandTerminalCandidate
)

const (
	ToolExecutionActionNone      = aitypes.ToolExecutionActionNone
	ToolExecutionActionContinue  = aitypes.ToolExecutionActionContinue
	ToolExecutionActionTerminate = aitypes.ToolExecutionActionTerminate

	aiToolExecutionActionNone      = ToolExecutionActionNone
	aiToolExecutionActionContinue  = ToolExecutionActionContinue
	aiToolExecutionActionTerminate = ToolExecutionActionTerminate
)

type aiToolExecutionAction = ToolExecutionAction

type ToolExecutionState struct {
	ExecutionID                     string
	RequestID                       string
	AssistantMessageID              string
	ToolIndex                       int
	ToolMessageID                   string
	RestoreArtifactPath             string
	CopyContent                     string
	ConversationDiffPrimaryPath     string
	ConversationDiffFileCount       int
	ConversationDiffToolName        string
	ConversationDiffHasPreview      bool
	Tool                            aiParsedToolUse
	Batch                           *aiPendingToolBatch
	TargetSessionID                 string
	AllowContinue                   bool
	AllowTerminate                  bool
	AllowTerminalAssignment         bool
	SuppressNextActionRequiredSound bool
	DecisionCh                      chan aiToolExecutionAction
	ReassignCh                      chan string
	ExecutionCtx                    context.Context
	Cancel                          context.CancelFunc
	mu                              sync.Mutex
	terminated                      bool
	snapshotOutputValue             string
}

type aiToolExecutionState = ToolExecutionState

type aiFollowupXMLPayload struct {
	Questions        []aiFollowupXMLQuestion         `xml:"question"`
	Suggestions      []aiFollowupXMLSuggestion       `xml:"suggest"`
	ThemePreferences []aiFollowupXMLThemePreferences `xml:"theme_preferences"`
}

type aiFollowupXMLThemePreferences struct {
	QuestionBlocks []aiFollowupXMLQuestion `xml:"question_block"`
}

type aiFollowupXMLQuestion struct {
	Type      string                `xml:"type,attr"`
	TypeNode  string                `xml:"type"`
	LabelAttr string                `xml:"label,attr"`
	Label     string                `xml:"label"`
	Title     string                `xml:"title"`
	Options   []aiFollowupXMLOption `xml:"option"`
}

type aiFollowupXMLOption struct {
	Mode        string `xml:"mode,attr"`
	Disabled    string `xml:"disabled,attr"`
	Recommended string `xml:"recommended,attr"`
	Text        string `xml:",chardata"`
}

type aiFollowupXMLSuggestion struct {
	Text string `xml:",chardata"`
}

func normalizeAIFollowupQuestionType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "multiple", "multi_select":
		return "multiple"
	case "free_text", "text":
		return "free_text"
	default:
		return "single"
	}
}

func aiAskFollowupQuestionToolDefinition() mcpserver.ToolDefinition {
	return mcpserver.ToolDefinition{
		Name:        "ask_followup_question",
		Description: "Present a multi-question questionnaire to the user and wait for their structured answer before continuing the task. Write one or more <question> blocks directly inside ask_followup_question. Every question must include a label attribute and an explicit type attribute. Allowed types are single, multiple, and free_text. single and multiple questions must place <option> entries inside that same question block. Do not include id attributes on question or option nodes; the system generates stable sequential IDs automatically. Options may use mode, disabled, and recommended attributes. Legacy 2 to 12 <suggest>...</suggest> payloads are also supported.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": true,
		},
	}
}

func parseAIFollowupDisabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func parseAIFollowupRecommended(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func parseAIFollowupPayload(raw string, fallbackQuestion string) ([]AIConversationFollowUpQuestion, []string, error) {
	payload := strings.TrimSpace(raw)
	if payload == "" {
		return nil, nil, fmt.Errorf("缺少追问建议")
	}
	if !strings.HasPrefix(payload, "<follow_up") {
		payload = "<follow_up>" + payload + "</follow_up>"
	}
	var parsed aiFollowupXMLPayload
	if err := xml.Unmarshal([]byte(payload), &parsed); err != nil {
		return nil, nil, fmt.Errorf("追问建议格式无效")
	}
	questionItems := append([]aiFollowupXMLQuestion{}, parsed.Questions...)
	if len(questionItems) == 0 {
		for _, item := range parsed.ThemePreferences {
			questionItems = append(questionItems, item.QuestionBlocks...)
		}
	}
	if len(questionItems) > 0 {
		questions := make([]AIConversationFollowUpQuestion, 0, len(questionItems))
		for questionIndex, item := range questionItems {
			questionID := fmt.Sprintf("question-%d", questionIndex+1)
			rawQuestionType := strings.TrimSpace(item.Type)
			if rawQuestionType == "" {
				return nil, nil, fmt.Errorf("第 %d 个追问问题缺少 type", questionIndex+1)
			}
			questionText := strings.TrimSpace(item.LabelAttr)
			if questionText == "" {
				questionText = strings.TrimSpace(item.Label)
			}
			if questionText == "" {
				questionText = strings.TrimSpace(item.Title)
			}
			if questionText == "" {
				if questionIndex == 0 && strings.TrimSpace(fallbackQuestion) != "" {
					questionText = strings.TrimSpace(fallbackQuestion)
				} else {
					questionText = fmt.Sprintf("Question %d", questionIndex+1)
				}
			}
			questionType := normalizeAIFollowupQuestionType(rawQuestionType)
			if questionType == "free_text" {
				questions = append(questions, AIConversationFollowUpQuestion{
					ID:   questionID,
					Text: questionText,
					Type: questionType,
				})
				continue
			}
			options := make([]AIConversationFollowUpOption, 0, len(item.Options))
			for optionIndex, option := range item.Options {
				answer := strings.TrimSpace(option.Text)
				if answer == "" {
					continue
				}
				optionID := fmt.Sprintf("%s-option-%d", questionID, optionIndex+1)
				options = append(options, AIConversationFollowUpOption{
					ID:          optionID,
					Answer:      answer,
					Mode:        strings.TrimSpace(option.Mode),
					Disabled:    parseAIFollowupDisabled(option.Disabled),
					Recommended: parseAIFollowupRecommended(option.Recommended),
				})
			}
			if len(options) == 0 {
				continue
			}
			questions = append(questions, AIConversationFollowUpQuestion{
				ID:      questionID,
				Text:    questionText,
				Type:    questionType,
				Options: options,
			})
		}
		if len(questions) == 0 {
			return nil, nil, fmt.Errorf("缺少有效追问问题")
		}
		return questions, nil, nil
	}
	suggestions := make([]string, 0, len(parsed.Suggestions))
	for _, item := range parsed.Suggestions {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		suggestions = append(suggestions, text)
	}
	if len(suggestions) < 2 || len(suggestions) > 4 {
		return nil, nil, fmt.Errorf("追问建议数量无效")
	}
	return nil, suggestions, nil
}

func decodeAIFollowupImages(raw string) []string {
	payload := strings.TrimSpace(raw)
	if payload == "" {
		return []string{}
	}
	var images []string
	if err := json.Unmarshal([]byte(payload), &images); err != nil {
		return []string{}
	}
	return normalizeAIStringList(images)
}

func parseAIResolvedFollowupAnswer(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ""
	}
	if strings.HasPrefix(trimmed, "{") {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			readableText, _ := parsed["readableText"].(string)
			readableText = strings.TrimSpace(readableText)
			if readableText == "" {
				readableText = trimmed
			}
			prettyBytes, marshalErr := json.MarshalIndent(parsed, "", "  ")
			if marshalErr == nil && len(prettyBytes) > 0 {
				return readableText, fmt.Sprintf("<survey_response>\n%s\n</survey_response>", string(prettyBytes))
			}
			return readableText, ""
		}
	}
	return trimmed, ""
}

func buildAIFollowupMessage(turnID string, requestID string, tool aiParsedToolUse, index int) (map[string]interface{}, error) {
	question := strings.TrimSpace(tool.Params["question"])
	questions, suggestions, err := parseAIFollowupPayload(tool.Params["follow_up"], question)
	if err != nil {
		return nil, err
	}
	displayQuestion := question
	if displayQuestion == "" && len(questions) > 0 {
		displayQuestion = strings.TrimSpace(questions[0].Text)
	}
	message := map[string]interface{}{
		"id":        buildToolMessageID(turnID, index),
		"turnId":    turnID,
		"kind":      "followup",
		"requestId": requestID,
		"status":    "等待处理",
	}
	if displayQuestion != "" {
		message["question"] = displayQuestion
	}
	if len(questions) > 0 {
		message["questions"] = questions
	}
	if len(suggestions) > 0 {
		message["suggestions"] = suggestions
	}
	return message, nil
}

func (e *aiToolExecutionState) setSnapshotOutput(value string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.snapshotOutputValue = value
	e.mu.Unlock()
}

func (e *aiToolExecutionState) snapshotOutput() string {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotOutputValue
}

func (e *aiToolExecutionState) setTargetSessionID(value string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.TargetSessionID = strings.TrimSpace(value)
	e.mu.Unlock()
}

func (e *aiToolExecutionState) targetSessionID() string {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.TrimSpace(e.TargetSessionID)
}

func (e *aiToolExecutionState) setAllowTerminalAssignment(enabled bool) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.AllowTerminalAssignment = enabled
	e.mu.Unlock()
}

func (e *aiToolExecutionState) allowTerminalAssignment() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.AllowTerminalAssignment
}

func (e *aiToolExecutionState) consumeSuppressNextActionRequiredSound() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	suppressed := e.SuppressNextActionRequiredSound
	e.SuppressNextActionRequiredSound = false
	return suppressed
}

func (e *aiToolExecutionState) requestTerminalAssignment(sessionID string) bool {
	if e == nil {
		return false
	}
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return false
	}
	e.setTargetSessionID(trimmedSessionID)
	if e.ReassignCh == nil {
		return false
	}
	for {
		select {
		case <-e.ReassignCh:
		default:
			select {
			case e.ReassignCh <- trimmedSessionID:
				return true
			default:
				return false
			}
		}
	}
}

func (e *aiToolExecutionState) markTerminated() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.terminated {
		return false
	}
	e.terminated = true
	return true
}

func (e *aiToolExecutionState) isTerminated() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.terminated
}

func (a *Service) setAIChatToolExecution(requestID string, execution *aiToolExecutionState) {
	if a == nil || strings.TrimSpace(requestID) == "" || execution == nil {
		return
	}
	a.aiToolExecMu.Lock()
	a.aiToolExecutions[strings.TrimSpace(requestID)] = execution
	a.aiToolExecMu.Unlock()
}

func (a *Service) getAIChatToolExecution(requestID string) *aiToolExecutionState {
	if a == nil || strings.TrimSpace(requestID) == "" {
		return nil
	}
	a.aiToolExecMu.Lock()
	defer a.aiToolExecMu.Unlock()
	return a.aiToolExecutions[strings.TrimSpace(requestID)]
}

func (a *Service) popAIChatToolExecution(requestID string) *aiToolExecutionState {
	if a == nil || strings.TrimSpace(requestID) == "" {
		return nil
	}
	a.aiToolExecMu.Lock()
	defer a.aiToolExecMu.Unlock()
	execution := a.aiToolExecutions[strings.TrimSpace(requestID)]
	delete(a.aiToolExecutions, strings.TrimSpace(requestID))
	return execution
}

func (a *Service) popAIChatToolExecutionIfMatches(requestID string, executionID string) *aiToolExecutionState {
	if a == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(executionID) == "" {
		return nil
	}
	a.aiToolExecMu.Lock()
	defer a.aiToolExecMu.Unlock()
	execution := a.aiToolExecutions[strings.TrimSpace(requestID)]
	if execution == nil || execution.ExecutionID != strings.TrimSpace(executionID) {
		return nil
	}
	delete(a.aiToolExecutions, strings.TrimSpace(requestID))
	return execution
}

func (a *Service) isAIChatToolExecutionCurrent(requestID string, executionID string) bool {
	if a == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(executionID) == "" {
		return false
	}
	a.aiToolExecMu.Lock()
	defer a.aiToolExecMu.Unlock()
	execution := a.aiToolExecutions[strings.TrimSpace(requestID)]
	return execution != nil && execution.ExecutionID == strings.TrimSpace(executionID)
}

func (a *Service) ListAIChatCommandTerminalCandidates(requestID string) ([]AIChatCommandTerminalCandidate, error) {
	trimmedRequestID := strings.TrimSpace(requestID)
	if a == nil || trimmedRequestID == "" {
		return nil, fmt.Errorf("没有可指派的命令实例")
	}
	if a.sshManager == nil {
		return nil, fmt.Errorf("SSH 管理器不可用")
	}
	execution := a.getAIChatToolExecution(trimmedRequestID)
	if execution == nil || execution.Batch == nil {
		return nil, fmt.Errorf("没有可指派的命令实例")
	}
	if strings.TrimSpace(execution.Tool.Name) != "execute_command" {
		return nil, fmt.Errorf("当前工具不支持指派终端")
	}
	targetSessionID := execution.targetSessionID()
	if targetSessionID == "" {
		targetSessionID = strings.TrimSpace(execution.Batch.Payload.SessionID)
	}
	if targetSessionID == "" {
		return nil, fmt.Errorf("当前工具缺少目标终端")
	}
	return a.sshManager.ListSiblingTerminalCandidates(targetSessionID)
}

func (a *Service) AssignAIChatToolTerminal(requestID string, targetSessionID string) error {
	trimmedRequestID := strings.TrimSpace(requestID)
	trimmedTargetSessionID := strings.TrimSpace(targetSessionID)
	if a == nil || trimmedRequestID == "" {
		return fmt.Errorf("没有可指派的命令实例")
	}
	if trimmedTargetSessionID == "" {
		return fmt.Errorf("目标终端不能为空")
	}
	if a.sshManager == nil {
		return fmt.Errorf("SSH 管理器不可用")
	}
	execution := a.getAIChatToolExecution(trimmedRequestID)
	if execution == nil || execution.Batch == nil {
		return fmt.Errorf("没有可指派的命令实例")
	}
	if strings.TrimSpace(execution.Tool.Name) != "execute_command" {
		return fmt.Errorf("当前工具不支持指派终端")
	}
	if !execution.allowTerminalAssignment() {
		return fmt.Errorf("当前工具未处于可指派状态")
	}
	currentTargetSessionID := execution.targetSessionID()
	if currentTargetSessionID == "" {
		currentTargetSessionID = strings.TrimSpace(execution.Batch.Payload.SessionID)
	}
	candidates, err := a.sshManager.ListSiblingTerminalCandidates(currentTargetSessionID)
	if err != nil {
		return err
	}
	targetCwd := ""
	allowed := false
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.SessionID) != trimmedTargetSessionID {
			continue
		}
		targetCwd = strings.TrimSpace(candidate.Cwd)
		allowed = true
		break
	}
	if !allowed {
		return fmt.Errorf("目标终端不可用")
	}
	if !execution.requestTerminalAssignment(trimmedTargetSessionID) {
		return fmt.Errorf("当前工具无法切换目标终端")
	}
	a.emitAIChatToolExecutionTerminalAssignmentRequired(
		trimmedRequestID,
		execution,
		buildAIChatCommandToolMessage(
			execution,
			execution.Tool.Params["purpose"],
			execution.Tool.Params["command"],
			"",
			"排队中, 等待终端空闲",
			buildAIChatCommandMessageExtra(trimmedTargetSessionID, resolveAIChatCommandDisplayCwd(execution.Tool.Params["cwd"], targetCwd), isAIChatMutatingCommandTool(execution.Tool)),
		),
	)
	return nil
}

func getAIToolRemainingFileEdits(tool aiParsedToolUse) int {
	parsedValue, err := strconv.Atoi(strings.TrimSpace(tool.Params["remaining_file_edits"]))
	if err != nil || parsedValue < 1 {
		return 0
	}
	return parsedValue
}

func isAIChatMutatingCommandTool(tool aiParsedToolUse) bool {
	parsedValue, err := strconv.Atoi(strings.TrimSpace(tool.Params["is_mutating"]))
	return err == nil && parsedValue == 1
}

func buildToolPreviewMessage(turnID string, tool aiParsedToolUse, index int) map[string]interface{} {
	if tool.Name == "execute_command" {
		return map[string]interface{}{
			"id":      buildToolMessageID(turnID, index),
			"turnId":  turnID,
			"kind":    "command",
			"purpose": tool.Params["purpose"],
			"command": tool.Params["command"],
			"output":  "待批准",
			"status":  "待批准",
			"extra":   buildAIChatCommandMessageExtra("", resolveAIChatCommandDisplayCwd(tool.Params["cwd"]), isAIChatMutatingCommandTool(tool)),
		}
	}
	if tool.Name == "ask_followup_question" {
		message, err := buildAIFollowupMessage(turnID, "", tool, index)
		if err == nil {
			return message
		}
	}
	if tool.Name == "attempt_completion" {
		return map[string]interface{}{
			"id":      buildToolMessageID(turnID, index),
			"turnId":  turnID,
			"kind":    "completion",
			"title":   titleForParsedToolUse(tool),
			"summary": "",
			"result":  strings.TrimSpace(tool.Params["result"]),
			"status":  "等待处理",
		}
	}
	if isAIMCPClientToolName(tool.Name) {
		serverName := strings.TrimSpace(tool.Params["server_name"])
		toolName := strings.TrimSpace(tool.Params["tool_name"])
		if tool.Name == aiAccessMCPResourceToolName {
			uri := strings.TrimSpace(tool.Params["uri"])
			if uri != "" {
				toolName = "resource:" + uri
			}
		}
		argsText := strings.TrimSpace(tool.Params["arguments"])
		if argsText == "" {
			if tool.Name == aiAccessMCPResourceToolName {
				argsText = marshalMCPAccessResourceArgs(strings.TrimSpace(tool.Params["uri"]))
			} else {
				argsText = "{}"
			}
		}
		return map[string]interface{}{
			"id":         buildToolMessageID(turnID, index),
			"turnId":     turnID,
			"kind":       "mcp",
			"serverName": serverName,
			"toolName":   toolName,
			"args":       argsText,
			"response":   "",
			"status":     "待批准",
			"extra": map[string]interface{}{
				"source": strings.TrimSpace(tool.Params["source"]),
			},
		}
	}
	return map[string]interface{}{
		"id":                 buildToolMessageID(turnID, index),
		"turnId":             turnID,
		"kind":               "tool",
		"actionLabel":        tool.Name,
		"title":              titleForParsedToolUse(tool),
		"summary":            summarizeParsedToolUse(tool),
		"code":               tool.RawXML,
		"status":             "待批准",
		"remainingFileEdits": getAIToolRemainingFileEdits(tool),
	}
}

func isChangeReviewTool(tool aiParsedToolUse) bool {
	switch strings.TrimSpace(tool.Name) {
	case "apply_diff", "write_to_file", "search_replace", "edit_file", "apply_patch":
		return true
	default:
		return false
	}
}

func formatChangeReviewFailure(failure *mcpserver.EditMatchFailure, startLine int) string {
	if failure == nil {
		return "暂无可预览差异"
	}
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(failure.Reason))
	if startLine > 0 {
		builder.WriteString(fmt.Sprintf("\n起始行: %d", startLine))
	}
	if failure.Occurrences > 0 {
		builder.WriteString(fmt.Sprintf("\n匹配次数: %d", failure.Occurrences))
	}
	if failure.Similarity > 0 || failure.RequiredSimilarity > 0 {
		builder.WriteString(fmt.Sprintf("\n相似度: %.0f%% / 需要 %.0f%%", failure.Similarity*100, failure.RequiredSimilarity*100))
	}
	if strings.TrimSpace(failure.BestMatch) != "" {
		builder.WriteString("\n\n最佳匹配片段:\n")
		builder.WriteString(strings.TrimSpace(failure.BestMatch))
	}
	return builder.String()
}

func buildChangeReviewMessage(turnID string, tool aiParsedToolUse, index int) map[string]interface{} {
	message := buildToolPreviewMessage(turnID, tool, index)
	message["status"] = "待审阅"
	message["result"] = "待审阅"
	return message
}

func buildChangeReviewEnvelope(turnID string, index int, sessionID string, remotePath string, tool aiParsedToolUse, rawPayload string, pathParams map[string]interface{}, blocks []map[string]interface{}) map[string]interface{} {
	envelope := map[string]interface{}{
		"reviewId":      buildToolMessageID(turnID, index),
		"title":         "修改",
		"requestId":     "",
		"toolMessageId": buildToolMessageID(turnID, index),
		"sessionId":     sessionID,
		"path":          remotePath,
		"toolName":      strings.TrimSpace(tool.Name),
		"summary":       summarizeParsedToolUse(tool),
		"rawDiff":       rawPayload,
		"blocks":        blocks,
	}
	if len(pathParams) > 0 {
		envelope["pathParams"] = pathParams
	}
	return envelope
}

func (a *Service) buildApplyDiffChangeReview(tool aiParsedToolUse, payload AIChatRequestPayload, turnID string, index int) (map[string]interface{}, map[string]interface{}, error) {
	if a == nil {
		return nil, nil, fmt.Errorf("应用不可用")
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return nil, nil, fmt.Errorf("缺少终端会话")
	}
	remotePath := strings.TrimSpace(tool.Params["path"])
	if remotePath == "" {
		return nil, nil, fmt.Errorf("缺少路径")
	}
	diffPayload := tool.Params["diff"]
	if strings.TrimSpace(diffPayload) == "" {
		return nil, nil, fmt.Errorf("缺少 diff 内容")
	}
	fileProvider := mcpFileProvider{app: a}
	originalContent, err := fileProvider.ReadTextFileContext(context.Background(), sessionID, remotePath)
	if err != nil {
		return nil, nil, err
	}
	preview, err := mcpserver.BuildApplyDiffPreview(remotePath, originalContent, diffPayload)
	if err != nil {
		return nil, nil, err
	}
	if preview.Failure != nil && !preview.CanApply {
		return nil, nil, errors.New(formatChangeReviewFailure(preview.Failure, preview.FailureBlockStartLine))
	}
	label := "整文件预览（共 {count} 个 SEARCH/REPLACE）"
	reviewBlocks := []map[string]interface{}{
		{
			"index":       0,
			"label":       label,
			"labelParams": map[string]interface{}{"count": len(preview.Blocks)},
			"before":      preview.OriginalContent,
			"after":       preview.PreviewContent,
		},
	}
	return buildChangeReviewMessage(turnID, tool, index), buildChangeReviewEnvelope(turnID, index, sessionID, remotePath, tool, diffPayload, nil, reviewBlocks), nil
}

func (a *Service) buildWriteToFileChangeReview(tool aiParsedToolUse, payload AIChatRequestPayload, turnID string, index int) (map[string]interface{}, map[string]interface{}, error) {
	if a == nil {
		return nil, nil, fmt.Errorf("应用不可用")
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return nil, nil, fmt.Errorf("缺少终端会话")
	}
	remotePath := strings.TrimSpace(tool.Params["path"])
	if remotePath == "" {
		return nil, nil, fmt.Errorf("缺少路径")
	}
	finalContent := tool.Params["content"]
	fileProvider := mcpFileProvider{app: a}
	originalContent, readErr := fileProvider.ReadTextFileContext(context.Background(), sessionID, remotePath)
	isNewFile := false
	if readErr != nil {
		originalContent = ""
		isNewFile = true
	}
	label := "整文件覆盖预览"
	if isNewFile {
		label = "新建文件预览"
	}
	blocks := []map[string]interface{}{
		{
			"index":  0,
			"label":  label,
			"before": originalContent,
			"after":  finalContent,
		},
	}
	return buildChangeReviewMessage(turnID, tool, index), buildChangeReviewEnvelope(turnID, index, sessionID, remotePath, tool, finalContent, nil, blocks), nil
}

func parseSearchReplaceOperations(raw string) ([]mcpserver.SearchReplaceOperation, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("缺少操作列表")
	}
	var operations []mcpserver.SearchReplaceOperation
	if err := json.Unmarshal([]byte(trimmed), &operations); err != nil {
		return nil, fmt.Errorf("操作列表 JSON 无效")
	}
	if len(operations) == 0 {
		return nil, fmt.Errorf("操作列表为空")
	}
	return operations, nil
}

func (a *Service) buildSearchReplaceChangeReview(tool aiParsedToolUse, payload AIChatRequestPayload, turnID string, index int) (map[string]interface{}, map[string]interface{}, error) {
	if a == nil {
		return nil, nil, fmt.Errorf("应用不可用")
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return nil, nil, fmt.Errorf("缺少终端会话")
	}
	remotePath := strings.TrimSpace(tool.Params["path"])
	if remotePath == "" {
		return nil, nil, fmt.Errorf("缺少路径")
	}
	operationsRaw := tool.Params["operations"]
	operations, err := parseSearchReplaceOperations(operationsRaw)
	if err != nil {
		return nil, nil, err
	}
	fileProvider := mcpFileProvider{app: a}
	originalContent, err := fileProvider.ReadTextFileContext(context.Background(), sessionID, remotePath)
	if err != nil {
		return nil, nil, err
	}
	preview, err := mcpserver.BuildSearchReplaceReviewPreview(remotePath, originalContent, operations)
	if err != nil {
		return nil, nil, err
	}
	if preview.Failure != nil {
		return nil, nil, errors.New(formatChangeReviewFailure(preview.Failure, 0))
	}
	label := "整文件预览（共 {count} 个 Search/Replace）"
	blocks := []map[string]interface{}{
		{
			"index":       0,
			"label":       label,
			"labelParams": map[string]interface{}{"count": len(preview.Operations)},
			"before":      preview.OriginalContent,
			"after":       preview.PreviewContent,
		},
	}
	return buildChangeReviewMessage(turnID, tool, index), buildChangeReviewEnvelope(turnID, index, sessionID, remotePath, tool, operationsRaw, nil, blocks), nil
}

func (a *Service) buildEditFileChangeReview(tool aiParsedToolUse, payload AIChatRequestPayload, turnID string, index int) (map[string]interface{}, map[string]interface{}, error) {
	if a == nil {
		return nil, nil, fmt.Errorf("应用不可用")
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return nil, nil, fmt.Errorf("缺少终端会话")
	}
	remotePath := strings.TrimSpace(tool.Params["path"])
	if remotePath == "" {
		return nil, nil, fmt.Errorf("缺少路径")
	}
	expectedReplacements := 1
	if rawExpected := strings.TrimSpace(tool.Params["expected_replacements"]); rawExpected != "" {
		parsedExpected, err := strconv.Atoi(rawExpected)
		if err != nil || parsedExpected < 1 {
			return nil, nil, fmt.Errorf("expected_replacements 无效")
		}
		expectedReplacements = parsedExpected
	}
	fileProvider := mcpFileProvider{app: a}
	originalContent, err := fileProvider.ReadTextFileContext(context.Background(), sessionID, remotePath)
	if err != nil {
		return nil, nil, err
	}
	preview, err := mcpserver.BuildEditFileReviewPreview(remotePath, originalContent, tool.Params["old_string"], tool.Params["new_string"], expectedReplacements)
	if err != nil {
		return nil, nil, err
	}
	if preview.Failure != nil {
		return nil, nil, errors.New(formatChangeReviewFailure(preview.Failure, 0))
	}
	label := "整文件预览（共 {count} 处替换）"
	blocks := []map[string]interface{}{
		{
			"index":       0,
			"label":       label,
			"labelParams": map[string]interface{}{"count": preview.Occurrences},
			"before":      preview.OriginalContent,
			"after":       preview.PreviewContent,
		},
	}
	rawPayload := fmt.Sprintf("{\"old_string\":%q,\"new_string\":%q,\"expected_replacements\":%d}", preview.Search, preview.Replace, preview.ExpectedReplacements)
	return buildChangeReviewMessage(turnID, tool, index), buildChangeReviewEnvelope(turnID, index, sessionID, remotePath, tool, rawPayload, nil, blocks), nil
}

func (a *Service) buildApplyPatchChangeReview(tool aiParsedToolUse, payload AIChatRequestPayload, turnID string, index int) (map[string]interface{}, map[string]interface{}, error) {
	if a == nil {
		return nil, nil, fmt.Errorf("应用不可用")
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return nil, nil, fmt.Errorf("缺少终端会话")
	}
	patchPayload := tool.Params["patch"]
	if strings.TrimSpace(patchPayload) == "" {
		return nil, nil, fmt.Errorf("缺少 patch 内容")
	}
	preview, err := mcpserver.BuildApplyPatchReviewPreview(patchPayload, func(remotePath string) (string, error) {
		content, exists, readErr := a.readAIRestoreTargetState(context.Background(), sessionID, remotePath)
		if readErr != nil {
			return "", readErr
		}
		if !exists {
			return "", os.ErrNotExist
		}
		return content, nil
	})
	if err != nil {
		return nil, nil, err
	}
	if preview.Failure != nil {
		return nil, nil, errors.New(formatChangeReviewFailure(preview.Failure, 0))
	}
	reviewBlocks := make([]map[string]interface{}, 0, len(preview.Files))
	for _, file := range preview.Files {
		label := strings.TrimSpace(file.Path)
		labelParams := map[string]interface{}(nil)
		if label == "" {
			label = "文件 #{count}"
			labelParams = map[string]interface{}{"count": file.Index + 1}
		}
		block := map[string]interface{}{
			"index":  file.Index,
			"label":  label,
			"before": file.Before,
			"after":  file.After,
		}
		if labelParams != nil {
			block["labelParams"] = labelParams
		}
		reviewBlocks = append(reviewBlocks, block)
	}
	reviewPath := ""
	pathParams := map[string]interface{}(nil)
	if len(preview.Files) == 1 {
		reviewPath = preview.Files[0].Path
	} else {
		reviewPath = "{count} 个文件"
		pathParams = map[string]interface{}{"count": len(preview.Files)}
	}
	return buildChangeReviewMessage(turnID, tool, index), buildChangeReviewEnvelope(turnID, index, sessionID, reviewPath, tool, patchPayload, pathParams, reviewBlocks), nil
}

func (a *Service) buildToolChangeReview(tool aiParsedToolUse, payload AIChatRequestPayload, turnID string, index int) (map[string]interface{}, map[string]interface{}, error) {
	switch strings.TrimSpace(tool.Name) {
	case "apply_diff":
		return a.buildApplyDiffChangeReview(tool, payload, turnID, index)
	case "write_to_file":
		return a.buildWriteToFileChangeReview(tool, payload, turnID, index)
	case "search_replace":
		return a.buildSearchReplaceChangeReview(tool, payload, turnID, index)
	case "edit_file":
		return a.buildEditFileChangeReview(tool, payload, turnID, index)
	case "apply_patch":
		return a.buildApplyPatchChangeReview(tool, payload, turnID, index)
	default:
		return nil, nil, fmt.Errorf("当前状态不支持重新应用")
	}
}

func (a *Service) failAIChatToolPreview(requestID string, batch *aiPendingToolBatch, tool aiParsedToolUse, resultText string) {
	if a == nil || batch == nil {
		return
	}
	resolvedResultText := strings.TrimSpace(resultText)
	if resolvedResultText == "" {
		resolvedResultText = "错误"
	}
	message := buildToolPreviewMessage(batch.AssistantMessageID, tool, batch.NextToolIndex)
	message["status"] = "错误"
	if tool.Name == "execute_command" {
		message["output"] = resolvedResultText
	} else {
		message["result"] = resolvedResultText
	}
	attachAIResultTokenEstimateMeta(message, buildAIChatToolResultContent(tool.Name, resolvedResultText), batch.Profile)
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "upsert_message",
		"requestId": requestID,
		"message":   message,
	})
	execution := &aiToolExecutionState{
		RequestID:          requestID,
		AssistantMessageID: batch.AssistantMessageID,
		ToolIndex:          batch.NextToolIndex,
		ToolMessageID:      buildToolMessageID(batch.AssistantMessageID, batch.NextToolIndex),
		Tool:               tool,
		Batch:              batch,
	}
	a.emitAIChatToolResultMessage(requestID, execution, resolvedResultText)
	a.emitAIChatToolExecutionPersistRequested(requestID)
	batch.NextToolIndex = len(batch.ParsedTools)
	a.resumeAIChatAfterToolBatch(requestID, batch)
}

const aiDefaultToolResultTokenThreshold = 350000

type aiToolResultThresholdResult struct {
	Content              string
	TokenCount           int
	TokenEstimateDisplay string
	Oversized            bool
}

func buildAIChatToolResultContent(toolName string, resultText string) string {
	return fmt.Sprintf("[%s] Result:\n%s", toolName, resultText)
}

func buildAIMCPToolResultContent(serverName string, toolName string, resultText string) string {
	return fmt.Sprintf("[%s:%s] Result:\n%s", serverName, toolName, resultText)
}

func resolveAIExecutionProfile(execution *aiToolExecutionState) AIProviderProfile {
	if execution == nil || execution.Batch == nil {
		return AIProviderProfile{}
	}
	return execution.Batch.Profile
}

func buildAIResultTokenEstimateDisplay(tokenCount int) string {
	if tokenCount <= 0 {
		return ""
	}
	return fmt.Sprintf("%.6fM", float64(tokenCount)/1000000)
}

func estimateAIResultTokens(rawResultContent string, profile AIProviderProfile) (int, string, bool) {
	if strings.TrimSpace(rawResultContent) == "" {
		return 0, "", false
	}
	tokenCount := estimateAIResultTextTokens(rawResultContent, profile)
	if tokenCount <= 0 {
		return 0, "", false
	}
	return tokenCount, buildAIResultTokenEstimateDisplay(tokenCount), true
}

func buildAIOversizedToolResultMessage(tokenCount int, threshold int) string {
	return fmt.Sprintf(
		"[ERROR] [Token Limit] Tool result returned too much data, so the raw output was omitted. Estimated size is about %s tokens, exceeding the threshold of %s. Do not assume omitted content; if you need details, call the tool again with a narrower scope, output limits, or a summary request.",
		buildAIResultTokenEstimateDisplay(tokenCount),
		buildAIResultTokenEstimateDisplay(threshold),
	)
}

func buildAIResultContentWithThreshold(rawResultContent string, profile AIProviderProfile, threshold int) aiToolResultThresholdResult {
	trimmedContent := strings.TrimSpace(rawResultContent)
	if trimmedContent == "" {
		return aiToolResultThresholdResult{}
	}
	tokenCount, tokenDisplay, ok := estimateAIResultTokens(trimmedContent, profile)
	if !ok {
		return aiToolResultThresholdResult{Content: trimmedContent}
	}
	normalizedThreshold := threshold
	if normalizedThreshold <= 0 {
		normalizedThreshold = aiDefaultToolResultTokenThreshold
	}
	if tokenCount > normalizedThreshold {
		return aiToolResultThresholdResult{
			Content:              buildAIOversizedToolResultMessage(tokenCount, normalizedThreshold),
			TokenCount:           tokenCount,
			TokenEstimateDisplay: tokenDisplay,
			Oversized:            true,
		}
	}
	return aiToolResultThresholdResult{
		Content:              trimmedContent,
		TokenCount:           tokenCount,
		TokenEstimateDisplay: tokenDisplay,
	}
}

func buildAIResultTokenEstimateMeta(rawResultContent string, profile AIProviderProfile) map[string]interface{} {
	tokenCount, tokenDisplay, ok := estimateAIResultTokens(rawResultContent, profile)
	if !ok || tokenCount <= 0 {
		return nil
	}
	return map[string]interface{}{
		"resultTokenEstimate":        tokenCount,
		"resultTokenEstimateDisplay": tokenDisplay,
	}
}

func attachAIResultTokenEstimateMeta(message map[string]interface{}, rawResultContent string, profile AIProviderProfile) map[string]interface{} {
	meta := buildAIResultTokenEstimateMeta(rawResultContent, profile)
	if message == nil || len(meta) == 0 {
		return message
	}
	existingExtra, _ := message["extra"].(map[string]interface{})
	if existingExtra == nil {
		existingExtra = map[string]interface{}{}
	}
	for key, value := range meta {
		existingExtra[key] = value
	}
	message["extra"] = existingExtra
	return message
}

func attachAIReadFileTokenEstimateMeta(message map[string]interface{}, result any, profile AIProviderProfile) map[string]interface{} {
	estimates := buildAIReadFileTokenEstimateMeta(result, profile)
	if message == nil || len(estimates) == 0 {
		return message
	}
	existingExtra, _ := message["extra"].(map[string]interface{})
	if existingExtra == nil {
		existingExtra = map[string]interface{}{}
	}
	existingExtra["readFileTokenEstimates"] = estimates
	message["extra"] = existingExtra
	return message
}

func buildAIChatToolResultMessage(toolName string, resultText string) AIChatRequestMessage {
	return AIChatRequestMessage{
		Role:    "user",
		Content: buildAIChatToolResultContent(toolName, resultText),
	}
}

func shouldSuppressAIChatToolResultUserMessage(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "ask_followup_question", "attempt_completion":
		return true
	default:
		return false
	}
}

func (a *Service) emitAIChatToolResultMessage(requestID string, execution *aiToolExecutionState, resultText string) {
	if a == nil || execution == nil || execution.Batch == nil {
		return
	}
	if shouldSuppressAIChatToolResultUserMessage(execution.Tool.Name) {
		return
	}
	thresholdedResult := buildAIResultContentWithThreshold(resultText, resolveAIExecutionProfile(execution), a.GetAIGlobalSettings().ToolResultTokenThreshold)
	resultContent := thresholdedResult.Content
	if strings.TrimSpace(resultContent) == "" {
		resultContent = strings.TrimSpace(resultText)
	}
	execution.Batch.RequestMessages = append(execution.Batch.RequestMessages, buildAIChatToolResultMessage(execution.Tool.Name, resultContent))
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "api_message_append",
		"requestId": requestID,
		"message": map[string]interface{}{
			"messageId":    fmt.Sprintf("api-tool-result-%d", time.Now().UnixNano()),
			"role":         "user",
			"content":      buildAIChatToolResultContent(execution.Tool.Name, resultContent),
			"uiMessageIds": []string{execution.ToolMessageID},
			"ts":           time.Now().UnixMilli(),
		},
	})
}

func (a *Service) emitAIChatToolExecutionStarted(requestID string, execution *aiToolExecutionState, message map[string]interface{}) {
	if a == nil || execution == nil {
		return
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":           "tool_execution_started",
		"requestId":      requestID,
		"executionId":    execution.ExecutionID,
		"allowContinue":  execution.AllowContinue,
		"allowTerminate": execution.AllowTerminate,
		"message":        message,
	})
}

func (a *Service) emitAIChatToolExecutionActionRequired(requestID string, execution *aiToolExecutionState, message map[string]interface{}) {
	if a == nil || execution == nil {
		return
	}
	payload := map[string]interface{}{
		"kind":           "tool_execution_action_required",
		"requestId":      requestID,
		"executionId":    execution.ExecutionID,
		"allowContinue":  execution.AllowContinue,
		"allowTerminate": execution.AllowTerminate,
		"message":        message,
		"sound":          "progress",
	}
	if execution.consumeSuppressNextActionRequiredSound() {
		payload["sound"] = ""
	}
	a.emitAIChatEvent(payload)
}

func (a *Service) emitAIChatToolExecutionPersistRequested(requestID string) {
	if a == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "tool_execution_persist_requested",
		"requestId": strings.TrimSpace(requestID),
	})
}

func resolveAIChatCommandDisplayCwd(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func buildAIChatCommandMessageExtra(targetSessionID string, targetCwd string, isMutating bool) map[string]interface{} {
	extra := map[string]interface{}{
		"isMutating": isMutating,
	}
	if trimmedTargetSessionID := strings.TrimSpace(targetSessionID); trimmedTargetSessionID != "" {
		extra["targetSessionId"] = trimmedTargetSessionID
	}
	if trimmedTargetCwd := strings.TrimSpace(targetCwd); trimmedTargetCwd != "" {
		extra["targetCwd"] = trimmedTargetCwd
	}
	return extra
}

func buildAIChatCommandToolMessage(execution *aiToolExecutionState, purpose string, command string, output string, status string, extra map[string]interface{}) map[string]interface{} {
	if execution == nil {
		return map[string]interface{}{}
	}
	message := map[string]interface{}{
		"id":      execution.ToolMessageID,
		"turnId":  execution.AssistantMessageID,
		"kind":    "command",
		"purpose": purpose,
		"command": command,
		"output":  output,
		"status":  status,
	}
	if len(extra) > 0 {
		message["extra"] = extra
		if _, exists := extra["resultTokenEstimateDisplay"]; exists {
			return message
		}
	}
	return attachAIResultTokenEstimateMeta(message, buildAIChatToolResultContent(execution.Tool.Name, output), resolveAIExecutionProfile(execution))
}

func (a *Service) emitAIChatCommandToolMessage(requestID string, execution *aiToolExecutionState, purpose string, command string, output string, status string, extra map[string]interface{}) {
	if a == nil || execution == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "upsert_message",
		"requestId": strings.TrimSpace(requestID),
		"message":   buildAIChatCommandToolMessage(execution, purpose, command, output, status, extra),
	})
}

func (a *Service) emitAIChatToolExecutionTerminalAssignmentRequired(requestID string, execution *aiToolExecutionState, message map[string]interface{}) {
	if a == nil || execution == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	payload := map[string]interface{}{
		"kind":           "tool_execution_terminal_assignment_required",
		"requestId":      strings.TrimSpace(requestID),
		"executionId":    execution.ExecutionID,
		"allowTerminate": execution.AllowTerminate,
		"message":        message,
		"sound":          "progress",
	}
	if execution.consumeSuppressNextActionRequiredSound() {
		payload["sound"] = ""
	}
	a.emitAIChatEvent(payload)
}

func (a *Service) skipCompatibleAIChatAfterResolvedTools(requestID string) {
	if a == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	a.emitAIChatRuntimePhase(requestID, "ready")
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "automatic_request_skipped",
		"requestId": strings.TrimSpace(requestID),
	})
	a.finishAIChatRequest(strings.TrimSpace(requestID))
}

func (a *Service) finishAIChatAfterTerminatedTool(requestID string) {
	trimmedRequestID := strings.TrimSpace(requestID)
	if a == nil || trimmedRequestID == "" {
		return
	}
	a.emitAIChatRuntimePhase(trimmedRequestID, "ready")
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "tool_execution_terminated",
		"requestId": trimmedRequestID,
		"text":      "已终止当前工具，等待恢复任务",
	})
	a.finishAIChatRequest(trimmedRequestID)
}

func (a *Service) continueCompatibleAIChatAfterResolvedTools(ctx context.Context, requestID string, batch *aiPendingToolBatch) {
	if a == nil || batch == nil {
		return
	}
	if a.consumeAIChatSkipNextAutomaticRequest(requestID) {
		a.skipCompatibleAIChatAfterResolvedTools(requestID)
		return
	}
	requestMessages := append([]AIChatRequestMessage{}, batch.RequestMessages...)
	if batch.DuplicateToolCount > 0 {
		requestMessages = append(requestMessages, a.emitAIDuplicateToolProtocolConflictMessage(requestID, batch.DuplicateToolCount))
	}
	if len(batch.SkippedStandaloneToolNames) > 0 {
		requestMessages = append(requestMessages, a.emitAIStandaloneToolSkippedMessage(requestID, batch.SkippedStandaloneToolNames))
	}
	nextAssistantMessageID := fmt.Sprintf("%s-cont-%d", requestID, time.Now().UnixNano())
	a.emitAIChatRuntimePhase(requestID, "api_request")
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "assistant_continue",
		"requestId": requestID,
		"messageId": nextAssistantMessageID,
	})
	continuationProfile := a.resolveAIChatContinuationProfile(batch)
	a.runCompatibleAIChatLoop(ctx, requestID, batch.Payload, continuationProfile, requestMessages, batch.AutoApprovalSettings, nextAssistantMessageID, batch.AssistantRetryCount, batch.CollaborationRetryCount)
}

func (a *Service) resumeAIChatAfterToolBatch(requestID string, batch *aiPendingToolBatch) {
	if a == nil || batch == nil {
		return
	}
	a.emitAIChatRuntimePhase(requestID, "between_tool_and_next_api")
	ctx, cancel := context.WithCancel(context.Background())
	a.setAIChatRequestCancel(requestID, cancel)
	go a.continueCompatibleAIChatAfterResolvedTools(ctx, requestID, batch)
}

// forceRejectAIChatToolBatchForCollaboration rejects every remaining tool in the batch
// regardless of approval settings, then hands control to the forced collaboration layer.
func (a *Service) forceRejectAIChatToolBatchForCollaboration(requestID string, batch *aiPendingToolBatch) {
	if a == nil || batch == nil {
		return
	}
	forcedRejectText := "⚠️助理察觉到异常,拒绝本次回复并强制介入"
	for batch.NextToolIndex < len(batch.ParsedTools) {
		tool := batch.ParsedTools[batch.NextToolIndex]
		message := buildToolPreviewMessage(batch.AssistantMessageID, tool, batch.NextToolIndex)
		message["status"] = "已拒绝"
		if tool.Name == "execute_command" {
			message["output"] = forcedRejectText
		} else {
			message["result"] = forcedRejectText
		}
		a.emitAIChatEvent(map[string]interface{}{
			"kind":      "upsert_message",
			"requestId": requestID,
			"message":   message,
		})
		execution := &aiToolExecutionState{
			RequestID:          requestID,
			AssistantMessageID: batch.AssistantMessageID,
			ToolIndex:          batch.NextToolIndex,
			ToolMessageID:      buildToolMessageID(batch.AssistantMessageID, batch.NextToolIndex),
			Tool:               tool,
			Batch:              batch,
		}
		a.emitAIChatToolResultMessage(requestID, execution, forcedRejectText)
		batch.NextToolIndex++
	}
	a.emitAIChatToolExecutionPersistRequested(requestID)
	a.startAIForcedCollaboration(requestID, batch)
}

func (a *Service) advanceAIChatToolBatch(requestID string, batch *aiPendingToolBatch) {
	if a == nil || batch == nil {
		return
	}
	if batch.ForceCollaboration {
		a.emitAIChatRuntimePhase(requestID, "tool_session")
		a.forceRejectAIChatToolBatchForCollaboration(requestID, batch)
		return
	}
	if batch.NextToolIndex >= len(batch.ParsedTools) {
		a.resumeAIChatAfterToolBatch(requestID, batch)
		return
	}

	a.emitAIChatRuntimePhase(requestID, "tool_session")
	tool := batch.ParsedTools[batch.NextToolIndex]
	if tool.Name == "ask_followup_question" {
		a.startAIChatFollowup(requestID, batch)
		return
	}
	if tool.Name == "attempt_completion" {
		a.startAIChatToolExecution(requestID, batch)
		return
	}
	decision := getAIParsedToolUseDecision(batch.AutoApprovalSettings, tool)

	// 只在本批次第一次遇到需要用户审批的工具之前, 拉取一次最新的审批配置。
	// 若本批次所有工具都是自动批准或自动拒绝, 则不会拉取; 多个工具也只在第一个进入审批前拉取一次。
	if decision == aiApprovalDecisionAskUser && !batch.AutoApprovalSettingsRefreshed {
		batch.AutoApprovalSettingsRefreshed = true
		if a != nil && a.configManager != nil {
			batch.AutoApprovalSettings = a.getAIAutoApprovalSettingsForConversation(batch.Payload.ConversationID)
			decision = getAIParsedToolUseDecision(batch.AutoApprovalSettings, tool)
		}
	}

	if decision == aiApprovalDecisionAutoDeny {
		message := buildToolPreviewMessage(batch.AssistantMessageID, tool, batch.NextToolIndex)
		if tool.Name == "execute_command" {
			message["status"] = "已拒绝"
			message["output"] = "已拒绝执行工具调用"
		} else {
			message["status"] = "已拒绝"
			message["result"] = "已拒绝执行工具调用"
		}
		a.emitAIChatEvent(map[string]interface{}{
			"kind":      "upsert_message",
			"requestId": requestID,
			"message":   message,
		})
		execution := &aiToolExecutionState{
			RequestID:          requestID,
			AssistantMessageID: batch.AssistantMessageID,
			ToolIndex:          batch.NextToolIndex,
			ToolMessageID:      buildToolMessageID(batch.AssistantMessageID, batch.NextToolIndex),
			Tool:               tool,
			Batch:              batch,
		}
		a.emitAIChatToolResultMessage(requestID, execution, "已拒绝执行工具调用")
		a.emitAIChatToolExecutionPersistRequested(requestID)
		batch.NextToolIndex++
		a.advanceAIChatToolBatch(requestID, batch)
		return
	}

	if decision == aiApprovalDecisionAskUser {
		if isChangeReviewTool(tool) {
			message, review, err := a.buildToolChangeReview(tool, batch.Payload, batch.AssistantMessageID, batch.NextToolIndex)
			if err != nil {
				a.failAIChatToolPreview(requestID, batch, tool, err.Error())
				return
			}
			review["requestId"] = requestID
			reviewID := buildToolMessageID(batch.AssistantMessageID, batch.NextToolIndex)
			restoreArtifact, restoreErr := a.persistAIChatToolRestoreArtifact(tool, batch.Payload, reviewID, requestID)
			if restoreErr != nil {
				a.failAIChatToolPreview(requestID, batch, tool, restoreErr.Error())
				return
			}
			attachAIRestoreArtifactRef(message, restoreArtifact.ArtifactPath)
			attachAICopyContent(message, restoreArtifact.CopyContent)
			attachAIConversationDiffMeta(message, restoreArtifact.ConversationDiffPrimaryPath, restoreArtifact.ConversationDiffFileCount, restoreArtifact.ConversationDiffToolName, restoreArtifact.ConversationDiffHasPreview)
			review["restoreArtifactPath"] = restoreArtifact.ArtifactPath
			a.setAIChatPendingToolBatch(requestID, batch)
			a.emitAIChatEvent(map[string]interface{}{
				"kind":         "tool_approval_required",
				"requestId":    requestID,
				"approvalMode": "change_review",
				"messages":     []map[string]interface{}{message},
				"sound":        "progress",
			})
			a.emitAIChatEvent(map[string]interface{}{
				"kind":      "change_review_required",
				"requestId": requestID,
				"review":    review,
			})
			return
		}
		a.setAIChatPendingToolBatch(requestID, batch)
		a.emitAIChatEvent(map[string]interface{}{
			"kind":         "tool_approval_required",
			"requestId":    requestID,
			"approvalMode": "inline",
			"messages":     []map[string]interface{}{buildToolPreviewMessage(batch.AssistantMessageID, tool, batch.NextToolIndex)},
			"sound":        "progress",
		})
		return
	}

	if decision == aiApprovalDecisionAutoApprove && strings.TrimSpace(tool.Name) == "execute_command" {
		batch.SuppressNextCommandActionSound = true
	}
	a.startAIChatToolExecution(requestID, batch)
}

func (a *Service) startAIChatFollowup(requestID string, batch *aiPendingToolBatch) {
	if a == nil || batch == nil || batch.NextToolIndex >= len(batch.ParsedTools) {
		return
	}
	tool := batch.ParsedTools[batch.NextToolIndex]
	message, err := buildAIFollowupMessage(batch.AssistantMessageID, requestID, tool, batch.NextToolIndex)
	if err != nil {
		a.failAIChatToolPreview(requestID, batch, tool, err.Error())
		return
	}
	a.setAIChatPendingFollowupBatch(requestID, batch)
	a.emitAIChatRuntimePhase(requestID, "ready")
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "followup_required",
		"requestId": requestID,
		"message":   message,
		"sound":     "notification",
	})
	if a.shouldUseAIChatCollaboration(batch) {
		a.queueAIChatCollaboration(requestID, batch)
	}
}

func (a *Service) startAIChatToolExecution(requestID string, batch *aiPendingToolBatch) {
	if a == nil || batch == nil || batch.NextToolIndex >= len(batch.ParsedTools) {
		return
	}
	tool := batch.ParsedTools[batch.NextToolIndex]
	suppressNextCommandActionSound := batch.SuppressNextCommandActionSound && strings.TrimSpace(tool.Name) == "execute_command"
	batch.SuppressNextCommandActionSound = false
	restoreArtifact := aiToolRestoreArtifactResult{}
	if isAIRestoreSupportedTool(tool) {
		var err error
		restoreArtifact, err = a.persistAIChatToolRestoreArtifact(tool, batch.Payload, buildToolMessageID(batch.AssistantMessageID, batch.NextToolIndex), requestID)
		if err != nil {
			a.failAIChatToolPreview(requestID, batch, tool, err.Error())
			return
		}
	}
	executionID := fmt.Sprintf("%s-tool-exec-%d-%d", requestID, batch.NextToolIndex, time.Now().UnixNano())
	executionCtx, cancel := context.WithCancel(context.Background())
	execution := &aiToolExecutionState{
		ExecutionID:                     executionID,
		RequestID:                       requestID,
		AssistantMessageID:              batch.AssistantMessageID,
		ToolIndex:                       batch.NextToolIndex,
		ToolMessageID:                   buildToolMessageID(batch.AssistantMessageID, batch.NextToolIndex),
		RestoreArtifactPath:             restoreArtifact.ArtifactPath,
		CopyContent:                     restoreArtifact.CopyContent,
		ConversationDiffPrimaryPath:     restoreArtifact.ConversationDiffPrimaryPath,
		ConversationDiffFileCount:       restoreArtifact.ConversationDiffFileCount,
		ConversationDiffToolName:        restoreArtifact.ConversationDiffToolName,
		ConversationDiffHasPreview:      restoreArtifact.ConversationDiffHasPreview,
		Tool:                            tool,
		Batch:                           batch,
		TargetSessionID:                 strings.TrimSpace(batch.Payload.SessionID),
		AllowContinue:                   false,
		AllowTerminate:                  true,
		AllowTerminalAssignment:         false,
		SuppressNextActionRequiredSound: suppressNextCommandActionSound,
		DecisionCh:                      make(chan aiToolExecutionAction, 1),
		ReassignCh:                      make(chan string, 1),
		ExecutionCtx:                    executionCtx,
		Cancel:                          cancel,
	}
	a.setAIChatToolExecution(requestID, execution)
	message := buildToolPreviewMessage(batch.AssistantMessageID, tool, batch.NextToolIndex)
	message["status"] = "执行中"
	if tool.Name == "execute_command" {
		message = buildAIChatCommandToolMessage(
			execution,
			tool.Params["purpose"],
			tool.Params["command"],
			"",
			"执行中",
			buildAIChatCommandMessageExtra(execution.TargetSessionID, resolveAIChatCommandDisplayCwd(tool.Params["cwd"]), isAIChatMutatingCommandTool(tool)),
		)
	} else {
		attachAIRestoreArtifactRef(message, execution.RestoreArtifactPath)
		attachAICopyContent(message, execution.CopyContent)
		attachAIConversationDiffMeta(message, execution.ConversationDiffPrimaryPath, execution.ConversationDiffFileCount, execution.ConversationDiffToolName, execution.ConversationDiffHasPreview)
	}
	a.emitAIChatToolExecutionStarted(requestID, execution, message)

	if tool.Name == aiThemeToolName {
		go a.runAIChatThemeToolExecution(execution)
		return
	}
	if tool.Name == "attempt_completion" {
		go a.runAIChatAttemptCompletionExecution(execution)
		return
	}
	if tool.Name == "execute_command" {
		go a.runAIChatCommandToolExecution(execution)
		return
	}
	if tool.Name == "live_search" {
		go a.runAIChatLiveSearchToolExecution(execution)
		return
	}
	if isAIMCPClientToolName(tool.Name) {
		go a.runAIChatMCPClientToolExecution(execution)
		return
	}
	go a.runAIChatGenericToolExecution(execution)
}

func (a *Service) runAIChatAttemptCompletionExecution(execution *aiToolExecutionState) {
	if a == nil || execution == nil || execution.Batch == nil {
		return
	}
	if !a.isAIChatToolExecutionCurrent(execution.RequestID, execution.ExecutionID) {
		return
	}
	a.popAIChatToolExecutionIfMatches(execution.RequestID, execution.ExecutionID)
	if execution.Cancel != nil {
		execution.Cancel()
	}
	if execution.isTerminated() {
		a.emitAICollaborationCompletionCard(execution.RequestID, execution.Batch, "已终止", "")
		a.emitAIChatToolExecutionPersistRequested(execution.RequestID)
		a.emitAIChatRuntimePhase(execution.RequestID, "ready")
		a.emitAIChatEvent(map[string]interface{}{
			"kind":      "automatic_request_skipped",
			"requestId": execution.RequestID,
		})
		a.finishAIChatRequest(execution.RequestID)
		return
	}
	if a.shouldUseAIChatCollaboration(execution.Batch) {
		a.emitAICollaborationCompletionCard(execution.RequestID, execution.Batch, "等待处理", "")
		a.emitAIChatToolExecutionPersistRequested(execution.RequestID)
		a.queueAIChatCollaboration(execution.RequestID, execution.Batch)
		return
	}
	resultText := resolveAICollaborationCompletionResult(execution.Tool)
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "upsert_message",
		"requestId": execution.RequestID,
		"sound":     "completion",
		"message": map[string]interface{}{
			"id":      execution.ToolMessageID,
			"turnId":  execution.AssistantMessageID,
			"kind":    "completion",
			"title":   titleForParsedToolUse(execution.Tool),
			"summary": "",
			"result":  resultText,
			"status":  "已完成",
		},
	})
	a.emitAIChatToolExecutionPersistRequested(execution.RequestID)
	a.emitAIChatRuntimePhase(execution.RequestID, "ready")
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "automatic_request_skipped",
		"requestId": execution.RequestID,
	})
	a.finishAIChatRequest(execution.RequestID)
}

func (a *Service) runAIChatGenericToolExecution(execution *aiToolExecutionState) {
	if a == nil || execution == nil || execution.Batch == nil {
		return
	}
	service := mcpserver.NewService(mcpSessionProvider{app: a})
	catalog := mcpserver.NewCatalog(service, mcpFileProvider{app: a}, mcpCommandProvider{app: a}, mcpRemoteEditExecutor{app: a}, mcpTransferProvider{app: a})
	arguments := convertToolArguments(execution.Tool, execution.Batch.Payload.SessionID)
	callResult, callErr := catalog.CallWithContext(execution.ExecutionCtx, execution.Tool.Name, arguments)

	if !a.isAIChatToolExecutionCurrent(execution.RequestID, execution.ExecutionID) {
		return
	}

	a.popAIChatToolExecutionIfMatches(execution.RequestID, execution.ExecutionID)
	if execution.Cancel != nil {
		execution.Cancel()
	}

	statusText := "已执行"
	uiResultText := ""
	rawResultText := ""
	stopAfterThisTool := false

	if callErr != nil {
		if execution.isTerminated() || errors.Is(callErr, context.Canceled) {
			statusText = "已终止"
			uiResultText = execution.snapshotOutput()
			rawResultText = execution.snapshotOutput()
			if strings.TrimSpace(uiResultText) == "" {
				uiResultText = "已终止"
			}
			if strings.TrimSpace(rawResultText) == "" {
				rawResultText = "已终止"
			}
		} else {
			statusText = "错误"
			uiResultText = callErr.Error()
			rawResultText = callErr.Error()
		}
		stopAfterThisTool = true
	} else {
		uiResultText = formatToolResultContent(callResult)
		rawResultText = formatAIRawToolResultContent(callResult)
		thresholdedResult := buildAIResultContentWithThreshold(rawResultText, resolveAIExecutionProfile(execution), a.GetAIGlobalSettings().ToolResultTokenThreshold)
		if thresholdedResult.Oversized {
			uiResultText = thresholdedResult.Content
			rawResultText = thresholdedResult.Content
		}
	}
	uiResultText = sanitizeAIToolResultText(uiResultText)

	message := map[string]interface{}{
		"id":                 execution.ToolMessageID,
		"turnId":             execution.AssistantMessageID,
		"kind":               "tool",
		"actionLabel":        execution.Tool.Name,
		"title":              titleForParsedToolUse(execution.Tool),
		"summary":            summarizeParsedToolUse(execution.Tool),
		"code":               execution.Tool.RawXML,
		"status":             statusText,
		"result":             uiResultText,
		"remainingFileEdits": getAIToolRemainingFileEdits(execution.Tool),
	}
	attachAIRestoreArtifactRef(message, execution.RestoreArtifactPath)
	attachAICopyContent(message, execution.CopyContent)
	attachAIConversationDiffMeta(message, execution.ConversationDiffPrimaryPath, execution.ConversationDiffFileCount, execution.ConversationDiffToolName, execution.ConversationDiffHasPreview)
	profile := resolveAIExecutionProfile(execution)
	if execution.Tool.Name == "read_file" {
		attachAIReadFileTokenEstimateMeta(message, callResult, profile)
	}
	attachAIResultTokenEstimateMeta(message, buildAIChatToolResultContent(execution.Tool.Name, formatAIRawToolResultContent(callResult)), profile)
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "upsert_message",
		"requestId": execution.RequestID,
		"message":   message,
	})

	if execution.isTerminated() {
		if strings.TrimSpace(uiResultText) == "" {
			uiResultText = "已终止"
		}
		if strings.TrimSpace(rawResultText) == "" {
			rawResultText = "已终止"
		}
		stopAfterThisTool = true
	}

	a.emitAIChatToolResultMessage(execution.RequestID, execution, rawResultText)
	a.emitAIChatToolExecutionPersistRequested(execution.RequestID)

	if stopAfterThisTool {
		execution.Batch.NextToolIndex = len(execution.Batch.ParsedTools)
		if execution.isTerminated() {
			a.finishAIChatAfterTerminatedTool(execution.RequestID)
			return
		}
		a.resumeAIChatAfterToolBatch(execution.RequestID, execution.Batch)
		return
	}

	execution.Batch.NextToolIndex++
	a.advanceAIChatToolBatch(execution.RequestID, execution.Batch)
}

func (a *Service) runAIChatLiveSearchToolExecution(execution *aiToolExecutionState) {
	if a == nil || execution == nil || execution.Batch == nil {
		return
	}

	query := strings.TrimSpace(execution.Tool.Params["query"])
	statusText := "已执行"
	uiResultText := ""
	rawResultText := ""
	stopAfterThisTool := false

	if query == "" {
		statusText = "错误"
		uiResultText = "缺少搜索内容"
		rawResultText = uiResultText
		stopAfterThisTool = true
	} else {
		resultText, err := a.searchAIProviderWeb(execution.ExecutionCtx, execution.RequestID, execution.Batch.Profile, query, func(content string) {
			if !a.isAIChatToolExecutionCurrent(execution.RequestID, execution.ExecutionID) {
				return
			}
			safeContent := sanitizeAIToolResultText(content)
			if strings.TrimSpace(safeContent) == "" {
				return
			}
			execution.setSnapshotOutput(safeContent)
			a.emitAIChatEvent(map[string]interface{}{
				"kind":      "upsert_message",
				"requestId": execution.RequestID,
				"message": map[string]interface{}{
					"id":                 execution.ToolMessageID,
					"turnId":             execution.AssistantMessageID,
					"kind":               "tool",
					"actionLabel":        execution.Tool.Name,
					"title":              titleForParsedToolUse(execution.Tool),
					"summary":            summarizeParsedToolUse(execution.Tool),
					"code":               execution.Tool.RawXML,
					"status":             "执行中",
					"result":             safeContent,
					"remainingFileEdits": getAIToolRemainingFileEdits(execution.Tool),
					"extra":              buildAIResultTokenEstimateMeta(buildAIChatToolResultContent(execution.Tool.Name, safeContent), resolveAIExecutionProfile(execution)),
				},
			})
		})
		if err != nil {
			if execution.isTerminated() || errors.Is(err, context.Canceled) {
				statusText = "已终止"
				uiResultText = execution.snapshotOutput()
				if strings.TrimSpace(uiResultText) == "" {
					uiResultText = "已终止"
				}
				rawResultText = uiResultText
			} else {
				statusText = "错误"
				uiResultText = err.Error()
				rawResultText = err.Error()
			}
			stopAfterThisTool = true
		} else {
			uiResultText = sanitizeAIToolResultText(resultText)
			rawResultText = resultText
			if strings.TrimSpace(uiResultText) == "" {
				uiResultText = "无内容"
				rawResultText = uiResultText
			} else {
				thresholded := buildAIResultContentWithThreshold(rawResultText, resolveAIExecutionProfile(execution), a.GetAIGlobalSettings().ToolResultTokenThreshold)
				if thresholded.Oversized {
					uiResultText = thresholded.Content
					rawResultText = thresholded.Content
				}
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

	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "upsert_message",
		"requestId": execution.RequestID,
		"message": map[string]interface{}{
			"id":                 execution.ToolMessageID,
			"turnId":             execution.AssistantMessageID,
			"kind":               "tool",
			"actionLabel":        execution.Tool.Name,
			"title":              titleForParsedToolUse(execution.Tool),
			"summary":            summarizeParsedToolUse(execution.Tool),
			"code":               execution.Tool.RawXML,
			"status":             statusText,
			"result":             uiResultText,
			"remainingFileEdits": getAIToolRemainingFileEdits(execution.Tool),
			"extra":              buildAIResultTokenEstimateMeta(buildAIChatToolResultContent(execution.Tool.Name, rawResultText), resolveAIExecutionProfile(execution)),
		},
	})

	a.emitAIChatToolResultMessage(execution.RequestID, execution, rawResultText)
	a.emitAIChatToolExecutionPersistRequested(execution.RequestID)

	if stopAfterThisTool {
		execution.Batch.NextToolIndex = len(execution.Batch.ParsedTools)
		if execution.isTerminated() {
			a.finishAIChatAfterTerminatedTool(execution.RequestID)
			return
		}
		a.resumeAIChatAfterToolBatch(execution.RequestID, execution.Batch)
		return
	}

	execution.Batch.NextToolIndex++
	a.advanceAIChatToolBatch(execution.RequestID, execution.Batch)
}

func (a *Service) runAIChatCommandToolExecution(execution *aiToolExecutionState) {
	if a == nil || execution == nil || execution.Batch == nil || a.sshManager == nil {
		return
	}

	arguments := convertToolArguments(execution.Tool, execution.Batch.Payload.SessionID)
	command, _ := arguments["command"].(string)
	purpose, _ := arguments["purpose"].(string)
	cwd, _ := arguments["cwd"].(string)
	cwd = strings.TrimSpace(cwd)
	shellType, _ := arguments["shellType"].(string)
	isMutating := false
	switch value := arguments["is_mutating"].(type) {
	case int:
		isMutating = value == 1
	case float64:
		isMutating = int(value) == 1
	}

	result, outcome, execErr := func() (mcpserver.CommandExecutionResult, aiToolExecutionAction, error) {
		if cwd == "" {
			return mcpserver.CommandExecutionResult{}, aiToolExecutionActionNone, fmt.Errorf("missing required argument: cwd")
		}
		return a.sshManager.ExecuteCommandInTerminalControlled(
			execution.targetSessionID(),
			command,
			purpose,
			isMutating,
			cwd,
			shellType,
			5*time.Minute,
			execution.DecisionCh,
			execution.ReassignCh,
			func() {
				execution.setAllowTerminalAssignment(true)
				if !a.isAIChatToolExecutionCurrent(execution.RequestID, execution.ExecutionID) {
					return
				}
				a.emitAIChatToolExecutionTerminalAssignmentRequired(
					execution.RequestID,
					execution,
					buildAIChatCommandToolMessage(
						execution,
						purpose,
						command,
						"",
						"排队中, 等待终端空闲",
						buildAIChatCommandMessageExtra(execution.targetSessionID(), resolveAIChatCommandDisplayCwd(cwd), isMutating),
					),
				)
			},
			func() {
				execution.setAllowTerminalAssignment(false)
				if !a.isAIChatToolExecutionCurrent(execution.RequestID, execution.ExecutionID) {
					return
				}
				a.emitAIChatToolExecutionStarted(
					execution.RequestID,
					execution,
					buildAIChatCommandToolMessage(
						execution,
						purpose,
						command,
						"",
						"执行中",
						buildAIChatCommandMessageExtra(execution.targetSessionID(), resolveAIChatCommandDisplayCwd(cwd), isMutating),
					),
				)
			},
			func(snapshot string) {
				execution.setAllowTerminalAssignment(false)
				if !a.isAIChatToolExecutionCurrent(execution.RequestID, execution.ExecutionID) {
					return
				}
				safeSnapshot := sanitizeAIToolResultText(snapshot)
				execution.setSnapshotOutput(safeSnapshot)
				execution.AllowContinue = true
				a.emitAIChatToolExecutionActionRequired(
					execution.RequestID,
					execution,
					buildAIChatCommandToolMessage(
						execution,
						purpose,
						command,
						safeSnapshot,
						"等待处理",
						buildAIChatCommandMessageExtra(execution.targetSessionID(), resolveAIChatCommandDisplayCwd(cwd), isMutating),
					),
				)
			},
		)
	}()

	if !a.isAIChatToolExecutionCurrent(execution.RequestID, execution.ExecutionID) {
		return
	}

	a.popAIChatToolExecutionIfMatches(execution.RequestID, execution.ExecutionID)
	if execution.Cancel != nil {
		execution.Cancel()
	}

	statusText := "已执行"
	rawResultText := result.Output
	rawResultSource := rawResultText
	uiResultText := sanitizeAIToolResultText(strings.TrimSpace(result.Output))
	stopAfterThisTool := false
	exitCodeText := ""
	if execErr == nil && result.ExitCode != nil {
		exitCodeText = fmt.Sprintf("[LumeTerm_EXIT_CODE_%d]", *result.ExitCode)
	}

	if execErr != nil {
		statusText = "错误"
		rawResultText = execErr.Error()
		uiResultText = execErr.Error()
		stopAfterThisTool = true
	} else if outcome == aiToolExecutionActionContinue {
		statusText = "后台继续"
		stopAfterThisTool = true
	} else if outcome == aiToolExecutionActionTerminate || execution.isTerminated() {
		statusText = "已终止"
		if uiResultText == "" {
			uiResultText = "已终止"
		}
		if strings.TrimSpace(rawResultText) == "" {
			rawResultText = "已终止"
		}
		stopAfterThisTool = true
	} else if result.ExitCode != nil && *result.ExitCode != 0 {
		statusText = "错误"
		stopAfterThisTool = true
	}

	if execErr == nil && strings.TrimSpace(rawResultSource) != "" {
		thresholdedResult := buildAIResultContentWithThreshold(rawResultSource, resolveAIExecutionProfile(execution), a.GetAIGlobalSettings().ToolResultTokenThreshold)
		if thresholdedResult.Oversized {
			uiResultText = thresholdedResult.Content
			rawResultText = thresholdedResult.Content
		}
	}

	commandExtra := buildAIChatCommandMessageExtra(execution.targetSessionID(), resolveAIChatCommandDisplayCwd(result.CWD, cwd), isMutating)
	if result.ExitCode != nil && execErr == nil {
		commandExtra["exitCode"] = *result.ExitCode
	}
	if resultTokenEstimateMeta := buildAIResultTokenEstimateMeta(buildAIChatToolResultContent(execution.Tool.Name, rawResultSource), resolveAIExecutionProfile(execution)); len(resultTokenEstimateMeta) > 0 {
		for key, value := range resultTokenEstimateMeta {
			commandExtra[key] = value
		}
	}

	a.emitAIChatCommandToolMessage(
		execution.RequestID,
		execution,
		purpose,
		command,
		uiResultText,
		statusText,
		commandExtra,
	)

	if strings.TrimSpace(uiResultText) == "" {
		switch statusText {
		case "后台继续":
			uiResultText = "后台继续"
		case "已终止":
			uiResultText = "已终止"
		default:
			uiResultText = "(命令已执行未产生输出)"
		}
	}
	if strings.TrimSpace(rawResultText) == "" {
		rawResultText = uiResultText
	}
	if exitCodeText != "" {
		if strings.TrimSpace(rawResultText) == "" {
			rawResultText = exitCodeText
		} else {
			rawResultText = strings.TrimSpace(rawResultText) + "\n" + exitCodeText
		}
	}

	a.emitAIChatToolResultMessage(execution.RequestID, execution, rawResultText)
	a.emitAIChatToolExecutionPersistRequested(execution.RequestID)

	if stopAfterThisTool {
		execution.Batch.NextToolIndex = len(execution.Batch.ParsedTools)
		if execution.isTerminated() {
			a.finishAIChatAfterTerminatedTool(execution.RequestID)
			return
		}
		a.resumeAIChatAfterToolBatch(execution.RequestID, execution.Batch)
		return
	}

	execution.Batch.NextToolIndex++
	a.advanceAIChatToolBatch(execution.RequestID, execution.Batch)
}

func (a *Service) ResolveAIChatFollowup(requestID string, answer string, imagesJSON string) error {
	trimmedRequestID := strings.TrimSpace(requestID)
	answerText := strings.TrimSpace(answer)
	followupImages := decodeAIFollowupImages(imagesJSON)
	if trimmedRequestID == "" {
		return fmt.Errorf("缺少请求 ID")
	}
	if answerText == "" && len(followupImages) == 0 {
		return fmt.Errorf("缺少追问回复")
	}
	batch := a.popAIChatPendingFollowupBatch(trimmedRequestID)
	if batch == nil {
		return fmt.Errorf("没有待处理的追问回复")
	}
	if batch.NextToolIndex >= len(batch.ParsedTools) {
		return fmt.Errorf("追问批次状态无效")
	}
	tool := batch.ParsedTools[batch.NextToolIndex]
	if tool.Name != "ask_followup_question" {
		return fmt.Errorf("当前待处理工具不是追问")
	}
	now := time.Now()
	readableText, surveyResponseBlock := parseAIResolvedFollowupAnswer(answerText)
	if readableText == "" && len(followupImages) == 0 {
		return fmt.Errorf("缺少追问回复")
	}
	userMessageID := fmt.Sprintf("%s-followup-answer-%d", buildToolMessageID(batch.AssistantMessageID, batch.NextToolIndex), now.UnixNano())
	followupContent := fmt.Sprintf("<user_message>\n%s\n</user_message>", readableText)
	if strings.TrimSpace(surveyResponseBlock) != "" {
		followupContent += "\n" + surveyResponseBlock
	}
	resolvedMessage, err := buildAIFollowupMessage(batch.AssistantMessageID, trimmedRequestID, tool, batch.NextToolIndex)
	if err == nil {
		resolvedMessage["status"] = "已完成"
		a.emitAIChatEvent(map[string]interface{}{
			"kind":      "upsert_message",
			"requestId": trimmedRequestID,
			"message":   resolvedMessage,
		})
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "append_message",
		"requestId": trimmedRequestID,
		"message": map[string]interface{}{
			"id":     userMessageID,
			"kind":   "user",
			"text":   readableText,
			"time":   now.Format("15:04"),
			"turnId": "",
			"images": followupImages,
		},
	})
	batch.RequestMessages = append(batch.RequestMessages, AIChatRequestMessage{
		Role:    "user",
		Content: followupContent,
		Images:  followupImages,
	})
	if isAIThemeToolScope(batch.Payload.ToolScope) && a.themeToolDelegate != nil {
		a.themeToolDelegate.MarkThemeToolConversationUserConfirmed(batch.Payload.ConversationID)
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "api_message_append",
		"requestId": trimmedRequestID,
		"message": map[string]interface{}{
			"messageId":    fmt.Sprintf("api-user-followup-%d", now.UnixNano()),
			"role":         "user",
			"content":      followupContent,
			"uiMessageIds": []string{userMessageID},
			"images":       followupImages,
			"ts":           now.UnixMilli(),
		},
	})
	a.emitAIChatToolExecutionPersistRequested(trimmedRequestID)
	batch.NextToolIndex++
	a.advanceAIChatToolBatch(trimmedRequestID, batch)
	return nil
}

func (a *Service) ContinueAIChatTool(requestID string) error {
	execution := a.getAIChatToolExecution(requestID)
	if execution == nil {
		return fmt.Errorf("没有可继续的工具实例")
	}
	if !execution.AllowContinue {
		return fmt.Errorf("当前工具不支持继续")
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "tool_execution_action_resolved",
		"requestId": execution.RequestID,
		"action":    "continue",
	})
	select {
	case execution.DecisionCh <- aiToolExecutionActionContinue:
		return nil
	default:
		return fmt.Errorf("当前工具不在等待继续状态")
	}
}

func (a *Service) terminateAIChatToolExecutionImmediately(execution *aiToolExecutionState, fallbackResult string) error {
	if a == nil || execution == nil || execution.Batch == nil {
		return fmt.Errorf("没有可终止的工具实例")
	}
	if !execution.markTerminated() {
		return nil
	}
	if execution.Cancel != nil {
		execution.Cancel()
	}
	if a.popAIChatToolExecutionIfMatches(execution.RequestID, execution.ExecutionID) == nil {
		return nil
	}

	resultText := sanitizeAIToolResultText(strings.TrimSpace(execution.snapshotOutput()))
	if resultText == "" {
		resultText = sanitizeAIToolResultText(strings.TrimSpace(fallbackResult))
	}
	if resultText == "" {
		resultText = "已终止"
	}

	message := buildToolPreviewMessage(execution.AssistantMessageID, execution.Tool, execution.ToolIndex)
	message["status"] = "已终止"
	if execution.Tool.Name == "execute_command" {
		message["output"] = resultText
	} else {
		message["result"] = resultText
	}
	attachAIRestoreArtifactRef(message, execution.RestoreArtifactPath)
	attachAICopyContent(message, execution.CopyContent)
	attachAIConversationDiffMeta(message, execution.ConversationDiffPrimaryPath, execution.ConversationDiffFileCount, execution.ConversationDiffToolName, execution.ConversationDiffHasPreview)
	attachAIResultTokenEstimateMeta(message, buildAIChatToolResultContent(execution.Tool.Name, resultText), resolveAIExecutionProfile(execution))
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "upsert_message",
		"requestId": execution.RequestID,
		"message":   message,
	})

	a.emitAIChatToolResultMessage(execution.RequestID, execution, resultText)
	a.emitAIChatToolExecutionPersistRequested(execution.RequestID)
	a.finishAIChatAfterTerminatedTool(execution.RequestID)
	return nil
}

func (a *Service) TerminateAIChatTool(requestID string) error {
	execution := a.getAIChatToolExecution(requestID)
	if execution == nil {
		return fmt.Errorf("没有可终止的工具实例")
	}
	if !execution.AllowTerminate {
		return fmt.Errorf("当前工具不支持终止")
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "tool_execution_action_resolved",
		"requestId": execution.RequestID,
		"action":    "terminate",
	})
	if execution.Tool.Name == "execute_command" {
		execution.markTerminated()
		select {
		case execution.DecisionCh <- aiToolExecutionActionTerminate:
			return nil
		default:
			return a.terminateAIChatToolExecutionImmediately(execution, "已终止")
		}
	}
	return a.terminateAIChatToolExecutionImmediately(execution, "已终止")
}
