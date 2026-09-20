package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"lumeterm/internal/mcpserver"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type AIChatRequestMessage struct {
	Role          string                              `json:"role"`
	Content       string                              `json:"content"`
	ContentBlocks []map[string]any                    `json:"contentBlocks,omitempty"`
	Images        []string                            `json:"images,omitempty"`
	CacheObjects  *AIConversationProviderCacheObjects `json:"cacheObjects,omitempty"`
}

type AIChatRequestPayload struct {
	ConversationID                           string                 `json:"conversationId"`
	SessionID                                string                 `json:"sessionId"`
	AutoApprove                              bool                   `json:"autoApprove"`
	SkipNextAutomaticRequest                 bool                   `json:"skipNextAutomaticRequest"`
	AssistantFirstReplyText                  string                 `json:"assistantFirstReplyText,omitempty"`
	SystemPromptOverride                     string                 `json:"systemPromptOverride,omitempty"`
	SkipSystemPrompt                         bool                   `json:"skipSystemPrompt,omitempty"`
	StreamEventPrefix                        string                 `json:"streamEventPrefix,omitempty"`
	ToolScope                                string                 `json:"toolScope,omitempty"`
	ToolScopeSlot                            string                 `json:"toolScopeSlot,omitempty"`
	AutoRecoverySubtaskHops                  int                    `json:"autoRecoverySubtaskHops,omitempty"`
	Messages                                 []AIChatRequestMessage `json:"messages"`
	AutoRecoveryActive                       bool                   `json:"-"`
	AutoRecoveryProbeCount                   int                    `json:"-"`
	AutoRecoveryCurrentConversationCondensed bool                   `json:"-"`
	AutoRecoveryRequestID                    string                 `json:"-"`
}

type aiParsedToolUse struct {
	Name       string
	Params     map[string]string
	RawXML     string
	StartIndex int
	EndIndex   int
}

type aiToolParamTagCandidate struct {
	ParamName  string
	OpeningTag string
	ClosingTag string
}

type aiToolTagCandidate struct {
	Name       string
	OpeningTag string
	ClosingTag string
}

type PendingToolBatch struct {
	RequestID                      string
	AssistantMessageID             string
	Payload                        AIChatRequestPayload
	Profile                        AIProviderProfile
	RequestMessages                []AIChatRequestMessage
	ParsedTools                    []aiParsedToolUse
	NextToolIndex                  int
	AssistantRetryCount            int
	CollaborationRetryCount        int
	SuppressNextCommandActionSound bool
	DuplicateToolCount             int
	AutoApprovalSettings           AIConversationTaskSettings
	AutoApprovalSettingsRefreshed  bool
	ForceCollaboration             bool
	ForceCollaborationReason       string
	SkippedStandaloneToolNames     []string
}

type aiPendingToolBatch = PendingToolBatch

type aiApprovalDecision string

const (
	aiApprovalDecisionAutoApprove   aiApprovalDecision = "auto_approve"
	aiApprovalDecisionAutoDeny      aiApprovalDecision = "auto_deny"
	aiApprovalDecisionAskUser       aiApprovalDecision = "ask_user"
	aiChatRequestMaxAttempts                           = 2
	aiAssistantRetryMaxAttempts                        = 2
	aiCollaborationRetryMaxAttempts                    = 2
	// aiChatEmptyResponseText 是供应商轮在"流已正常结束但没有任何正文"时回填的兜底文案,
	// 它不是模型的真实回复。展示层可以使用它,但重试/工具协议判断必须先把它当空处理。
	aiChatEmptyResponseText = "未返回内容"
)

var aiSupportedToolNames = []string{
	"list_connected_sessions",
	"get_work_path",
	"list_files",
	"read_file",
	"write_to_file",
	"transfer_batch",
	"transfer_list",
	"execute_command",
	"theme_tool",
	"ask_followup_question",
	"attempt_completion",
	"search_replace",
	"apply_diff",
	"live_search",
	"use_mcp_tool",
	"access_mcp_resource",
}

var aiAlwaysAutoApprovedToolNames = map[string]struct{}{
	"list_connected_sessions": {},
	"get_work_path":           {},
	"live_search":             {},
	"theme_tool":              {},
}

var aiSupportedToolParamNames = []string{
	"session_id",
	"path",
	"content",
	"remaining_file_edits",
	"args",
	"files",
	"items",
	"start_line",
	"end_line",
	"local_parent",
	"remote_parent",
	"local_path",
	"remote_path",
	"operation",
	"wait",
	"command",
	"purpose",
	"is_mutating",
	"cwd",
	"shellType",
	"diff",
	"operations",
	"file_path",
	"old_string",
	"new_string",
	"expected_replacements",
	"patch",
	"recursive",
	"query",
	"question",
	"follow_up",
	"action",
	"request",
	"slot",
	"result",
	"server_name",
	"tool_name",
	"arguments",
	"source",
	"uri",
}

var (
	aiDangerousParameterExpansionPattern           = regexp.MustCompile(`\$\{[^}]*@[PQEAa][^}]*\}`)
	aiParameterAssignmentWithOctalEscapesPattern   = regexp.MustCompile(`\$\{[^}]*[=+\-?][^}]*\\[0-7]{3}[^}]*\}`)
	aiParameterAssignmentWithHexEscapesPattern     = regexp.MustCompile(`\$\{[^}]*[=+\-?][^}]*\\x[0-9a-fA-F]{2}[^}]*\}`)
	aiParameterAssignmentWithUnicodeEscapesPattern = regexp.MustCompile(`\$\{[^}]*[=+\-?][^}]*\\u[0-9a-fA-F]{4}[^}]*\}`)
	aiIndirectExpansionPattern                     = regexp.MustCompile(`\$\{![^}]+\}`)
	aiHereStringWithSubstitutionPattern            = regexp.MustCompile("<<<\\s*(\\$\\(|`)")
	aiZshProcessSubstitutionPattern                = regexp.MustCompile(`(^|[\s;|&(<])=\([^)]+\)`)
	aiZshGlobQualifierPattern                      = regexp.MustCompile(`[*?+@!]\(e:[^:]+:\)`)
	aiRedirectionPattern                           = regexp.MustCompile(`\d*>&\d*`)
	aiSimpleVariablePattern                        = regexp.MustCompile(`\$[a-zA-Z_][a-zA-Z0-9_]*`)
	aiSpecialVariablePattern                       = regexp.MustCompile(`\$[?!#$@*\-0-9]`)
	aiSubshellPlaceholderPattern                   = regexp.MustCompile(`^__SUBSH_(\d+)__$`)
	aiUserMessageBlockPattern                      = regexp.MustCompile(`(?s)<user_message>.*?</user_message>`)
	aiRequestEnvironmentDetailsPattern             = regexp.MustCompile(`(?s)<environment_details>(.*?)</environment_details>`)
	aiRequestConsiderationsPattern                 = regexp.MustCompile(`(?s)<considerations>\s*(.*?)\s*</considerations>`)
	aiGenericXMLTagPattern                         = regexp.MustCompile(`</?[a-zA-Z_][^>]*>`)
	// Models often wrap XML tools or leak stream end tokens; strip before protocol parse.
	aiToolProtocolWrapperTagPattern = regexp.MustCompile(`(?is)</?\s*(?:tool_calls?|function_calls?|tool_reply|tools)\s*>`)
	aiToolProtocolStreamJunkPattern = regexp.MustCompile(`(?is)(?:</\s*assistant\s*>|<\|(?:eos|end|endoftext|im_end|eot_id)\|>|</s>)+`)
	// aiToolProtocolDSMLMarker 是 DeepSeek 原生工具调用(DSML)标记的中间片段,
	// 用于识别模型把原生工具调用泄漏进正文的情况。
	// DSML 标记 = 全角竖线 x2 + "DSML" + 全角竖线 x2 (0xFF5C)。用码点构造,源码保持纯 ASCII。
	aiToolProtocolDSMLMarker = string(rune(0xFF5C)) + string(rune(0xFF5C)) + "DSML" + string(rune(0xFF5C)) + string(rune(0xFF5C))
	aiBareToolOpenTagPattern *regexp.Regexp
)

func init() {
	// Bare open tags like "execute_command>" (missing '<') are a common model slip.
	// Do not match already-correct "</name>" or "<name>".
	quoted := make([]string, 0, len(aiSupportedToolNames))
	for _, name := range aiSupportedToolNames {
		quoted = append(quoted, regexp.QuoteMeta(name))
	}
	// Line-start only — avoids rewriting "echo execute_command>" inside command params.
	aiBareToolOpenTagPattern = regexp.MustCompile(`(?m)^(\s*)(` + strings.Join(quoted, "|") + `)>`)
}

func normalizeAIChatRequestMessages(messages []AIChatRequestMessage) []AIChatRequestMessage {
	normalized := make([]AIChatRequestMessage, 0, len(messages))
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "system" && role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(message.Content)
		// 紧凑回放的权威文本源, 必须与 cacheObjects.replayState 同时保留, 否则块数对不上会强制降级。
		contentBlocks := cloneAIConversationOpenAIResponsesOutputItems(message.ContentBlocks)
		images := normalizeAIStringList(message.Images)
		if role != "user" {
			images = nil
		}
		if content == "" && len(contentBlocks) == 0 && len(images) == 0 {
			continue
		}
		normalized = append(normalized, AIChatRequestMessage{
			Role:          role,
			Content:       content,
			ContentBlocks: contentBlocks,
			Images:        images,
			CacheObjects:  cloneAIConversationProviderCacheObjects(message.CacheObjects),
		})
	}
	return normalized
}

func isAIExplicitUserPromptMessage(message AIChatRequestMessage) bool {
	if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
		return false
	}
	trimmedContent := strings.TrimSpace(message.Content)
	return aiUserMessageBlockPattern.MatchString(trimmedContent)
}

func isAIExplicitUserPromptAPIMessage(message AIConversationAPIMessage) bool {
	if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
		return false
	}
	trimmedContent := strings.TrimSpace(message.Content)
	return aiUserMessageBlockPattern.MatchString(trimmedContent)
}

func findLatestAIExplicitUserPromptMessage(messages []AIChatRequestMessage) (int, AIChatRequestMessage, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		if isAIExplicitUserPromptMessage(messages[index]) {
			return index, messages[index], true
		}
	}
	return -1, AIChatRequestMessage{}, false
}

func findLatestAIReliableConsiderationsMessageBefore(messages []AIChatRequestMessage, beforeIndex int) (int, AIChatRequestMessage, bool) {
	if beforeIndex > len(messages) {
		beforeIndex = len(messages)
	}
	for index := beforeIndex - 1; index >= 0; index-- {
		if !isAIExplicitUserPromptMessage(messages[index]) {
			continue
		}
		if hasReliableAIConsiderationsBlock(messages[index].Content) {
			return index, messages[index], true
		}
	}
	return -1, AIChatRequestMessage{}, false
}

func findLatestAIExplicitUserPromptAPIMessage(messages []AIConversationAPIMessage) int {
	for index := len(messages) - 1; index >= 0; index-- {
		if isAIExplicitUserPromptAPIMessage(messages[index]) {
			return index
		}
	}
	return -1
}

func countAIExplicitUserPromptMessages(messages []AIChatRequestMessage) int {
	count := 0
	for _, message := range messages {
		if isAIExplicitUserPromptMessage(message) {
			count++
		}
	}
	return count
}

func hasReliableAIConsiderationsBlock(text string) bool {
	matches := aiRequestEnvironmentDetailsPattern.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		considerationMatches := aiRequestConsiderationsPattern.FindAllStringSubmatch(match[1], -1)
		for _, considerationMatch := range considerationMatches {
			if len(considerationMatch) < 2 {
				continue
			}
			if strings.TrimSpace(considerationMatch[1]) != "" {
				return true
			}
		}
	}
	return false
}

type aiUserCompensationSignal struct {
	Index                    int
	HasReliableConsideration bool
	Reason                   string
}

type aiUserNodeCompensationResult struct {
	Messages []AIChatRequestMessage
	Applied  bool
	Reason   string
}

func getLatestAIUserCompensationSignal(messages []AIChatRequestMessage) aiUserCompensationSignal {
	latestIndex, latestMessage, ok := findLatestAIExplicitUserPromptMessage(messages)
	if !ok {
		return aiUserCompensationSignal{Index: -1}
	}
	if hasReliableAIConsiderationsBlock(latestMessage.Content) {
		return aiUserCompensationSignal{
			Index:                    latestIndex,
			HasReliableConsideration: true,
			Reason:                   "existing_considerations",
		}
	}
	if _, _, ok := findLatestAIReliableConsiderationsMessageBefore(messages, latestIndex); ok {
		return aiUserCompensationSignal{
			Index:                    latestIndex,
			HasReliableConsideration: true,
			Reason:                   "history_considerations_present",
		}
	}
	reason := "considerations_missing"
	if countAIExplicitUserPromptMessages(messages) <= 1 {
		reason = "first_request"
	}
	return aiUserCompensationSignal{
		Index:                    latestIndex,
		HasReliableConsideration: false,
		Reason:                   reason,
	}
}

func appendAIConsiderationsToEnvironmentDetails(content string, considerationsText string) string {
	trimmedContent := strings.TrimSpace(content)
	trimmedConsiderationsText := strings.TrimSpace(considerationsText)
	if trimmedConsiderationsText == "" {
		return trimmedContent
	}
	considerationsBlock := "<considerations>\n" + trimmedConsiderationsText + "\n</considerations>"
	matches := aiRequestEnvironmentDetailsPattern.FindStringSubmatchIndex(trimmedContent)
	if len(matches) >= 4 {
		innerStart := matches[2]
		innerEnd := matches[3]
		innerContent := strings.TrimSpace(trimmedContent[innerStart:innerEnd])
		if innerContent != "" {
			innerContent += "\n\n" + considerationsBlock
		} else {
			innerContent = considerationsBlock
		}
		return strings.TrimSpace(trimmedContent[:innerStart] + "\n" + innerContent + "\n" + trimmedContent[innerEnd:])
	}
	if trimmedContent == "" {
		return "<environment_details>\n" + considerationsBlock + "\n</environment_details>"
	}
	return trimmedContent + "\n\n<environment_details>\n" + considerationsBlock + "\n</environment_details>"
}

func applyAIUserNodeCompensation(messages []AIChatRequestMessage) aiUserNodeCompensationResult {
	signal := getLatestAIUserCompensationSignal(messages)
	if signal.Index < 0 || signal.HasReliableConsideration {
		return aiUserNodeCompensationResult{
			Messages: messages,
			Applied:  false,
			Reason:   signal.Reason,
		}
	}
	compensationText := BuildAIUserCompensation()
	if strings.TrimSpace(compensationText) == "" {
		return aiUserNodeCompensationResult{
			Messages: messages,
			Applied:  false,
			Reason:   signal.Reason,
		}
	}
	cloned := append([]AIChatRequestMessage{}, messages...)
	targetMessage := cloned[signal.Index]
	targetMessage.Content = appendAIConsiderationsToEnvironmentDetails(targetMessage.Content, compensationText)
	cloned[signal.Index] = targetMessage
	return aiUserNodeCompensationResult{
		Messages: cloned,
		Applied:  true,
		Reason:   signal.Reason,
	}
}

func (a *Service) persistAIUserNodeCompensation(requestID string, conversationID string, requestMessages []AIChatRequestMessage) {
	if a == nil || a.configManager == nil || strings.TrimSpace(conversationID) == "" {
		return
	}
	_, latestRequestMessage, ok := findLatestAIExplicitUserPromptMessage(requestMessages)
	if !ok {
		return
	}
	snapshot, err := a.configManager.GetAIConversation(strings.TrimSpace(conversationID))
	if err != nil {
		return
	}
	apiMessageIndex := findLatestAIExplicitUserPromptAPIMessage(snapshot.APIMessages)
	if apiMessageIndex < 0 {
		return
	}
	apiMessage := snapshot.APIMessages[apiMessageIndex]
	updatedContent := strings.TrimSpace(latestRequestMessage.Content)
	updatedImages := normalizeAIStringList(latestRequestMessage.Images)
	updatedCacheObjects := cloneAIConversationProviderCacheObjects(latestRequestMessage.CacheObjects)
	if apiMessage.Content == updatedContent &&
		reflect.DeepEqual(normalizeAIStringList(apiMessage.Images), updatedImages) &&
		reflect.DeepEqual(apiMessage.CacheObjects, updatedCacheObjects) {
		return
	}
	snapshot.APIMessages[apiMessageIndex].Content = updatedContent
	snapshot.APIMessages[apiMessageIndex].Images = updatedImages
	snapshot.APIMessages[apiMessageIndex].CacheObjects = updatedCacheObjects
	snapshot.UpdatedAt = time.Now().UnixMilli()
	savedSnapshot, saveErr := a.configManager.SaveAIConversation(snapshot)
	if saveErr != nil {
		return
	}
	if apiMessageIndex < 0 || apiMessageIndex >= len(savedSnapshot.APIMessages) {
		return
	}
	updatedMessage := savedSnapshot.APIMessages[apiMessageIndex]
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "api_message_append",
		"requestId": requestID,
		"message": map[string]interface{}{
			"messageId":     updatedMessage.MessageID,
			"role":          updatedMessage.Role,
			"content":       updatedMessage.Content,
			"contentBlocks": updatedMessage.ContentBlocks,
			"uiMessageIds":  updatedMessage.UIMessageIDs,
			"images":        updatedMessage.Images,
			"cacheObjects":  updatedMessage.CacheObjects,
			"ts":            updatedMessage.Ts,
		},
	})
}

func getAIAutoApprovalCategoryForTool(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "list_connected_sessions", "list_files", "read_file", "transfer_list", "live_search":
		return "read"
	case "write_to_file", "transfer_batch", "search_replace", "apply_diff", "apply_patch":
		return "write"
	case "execute_command":
		return "execute"
	default:
		return ""
	}
}

func isAIAutoApprovalEffectivelyEnabled(settings AIConversationTaskSettings) bool {
	return settings.AlwaysAllowReadOnly ||
		settings.AlwaysAllowWrite ||
		settings.AlwaysAllowExecute
}

func aiJSStringLength(value string) int {
	return len(utf16.Encode([]rune(value)))
}

const (
	aiMaxToolResultSingleStringLength    = 120000
	aiMaxToolResultAggregateStringLength = 240000
	aiMaxToolResultCollectionItems       = 2048
	aiMaxToolResultInspectDepth          = 8
)

type aiToolResultSafetyState struct {
	TotalStringLength int
	CollectionItems   int
}

func sanitizeAIToolResultText(value string) string {
	return strings.TrimSpace(value)
}

func isAISafeToolResultString(value string, state *aiToolResultSafetyState) bool {
	currentLength := aiJSStringLength(value)
	if currentLength > aiMaxToolResultSingleStringLength {
		return false
	}
	state.TotalStringLength += currentLength
	return state.TotalStringLength <= aiMaxToolResultAggregateStringLength
}

func inspectAIToolResultSafetyValue(value reflect.Value, depth int, state *aiToolResultSafetyState) bool {
	if !value.IsValid() {
		return true
	}
	if depth > aiMaxToolResultInspectDepth {
		return false
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Ptr:
		if value.IsNil() {
			return true
		}
		return inspectAIToolResultSafetyValue(value.Elem(), depth+1, state)
	case reflect.String:
		return isAISafeToolResultString(value.String(), state)
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			currentLength := value.Len()
			if currentLength > aiMaxToolResultSingleStringLength {
				return false
			}
			state.TotalStringLength += currentLength
			return state.TotalStringLength <= aiMaxToolResultAggregateStringLength
		}
		fallthrough
	case reflect.Array:
		state.CollectionItems += value.Len()
		if state.CollectionItems > aiMaxToolResultCollectionItems {
			return false
		}
		for index := 0; index < value.Len(); index++ {
			if !inspectAIToolResultSafetyValue(value.Index(index), depth+1, state) {
				return false
			}
		}
		return true
	case reflect.Map:
		state.CollectionItems += value.Len()
		if state.CollectionItems > aiMaxToolResultCollectionItems {
			return false
		}
		iter := value.MapRange()
		for iter.Next() {
			if !inspectAIToolResultSafetyValue(iter.Key(), depth+1, state) {
				return false
			}
			if !inspectAIToolResultSafetyValue(iter.Value(), depth+1, state) {
				return false
			}
		}
		return true
	case reflect.Struct:
		state.CollectionItems += value.NumField()
		if state.CollectionItems > aiMaxToolResultCollectionItems {
			return false
		}
		for index := 0; index < value.NumField(); index++ {
			if !value.Type().Field(index).IsExported() {
				continue
			}
			if !inspectAIToolResultSafetyValue(value.Field(index), depth+1, state) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

func isAISafeToolResultValue(value any) bool {
	return inspectAIToolResultSafetyValue(reflect.ValueOf(value), 0, &aiToolResultSafetyState{})
}

func formatAIPlainToolResultContent(result any) string {
	switch value := result.(type) {
	case string:
		return strings.TrimSpace(value)
	case []byte:
		return strings.TrimSpace(string(value))
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", result))
	}
}

func formatAIReadFileLineRange(startLine int, endLine int) string {
	if startLine <= 0 && endLine <= 0 {
		return ""
	}
	if startLine <= 0 {
		startLine = endLine
	}
	if endLine <= 0 {
		endLine = startLine
	}
	if endLine < startLine {
		startLine, endLine = endLine, startLine
	}
	if startLine == endLine {
		return fmt.Sprintf("(%d)", startLine)
	}
	return fmt.Sprintf("(%d-%d)", startLine, endLine)
}

func formatAIReadFilePathLabel(path string, startLine int, endLine int) string {
	trimmedPath := strings.TrimSpace(path)
	lineRange := formatAIReadFileLineRange(startLine, endLine)
	if trimmedPath == "" {
		return lineRange
	}
	if lineRange == "" {
		return trimmedPath
	}
	return trimmedPath + " " + lineRange
}

func formatAIReadFileToolResultContent(result mcpserver.ReadFileResult) string {
	content := strings.TrimSpace(result.Content)
	pathLabel := formatAIReadFilePathLabel(result.Path, result.StartLine, result.EndLine)
	if content == "" {
		if pathLabel != "" {
			return "Path: " + pathLabel
		}
		return ""
	}
	if pathLabel == "" {
		return content
	}
	return "Path: " + pathLabel + "\n" + content
}

func formatAIReadFileBatchEntryContent(result mcpserver.ReadFileResult) string {
	content := strings.TrimSpace(result.Content)
	pathLabel := formatAIReadFilePathLabel(result.Path, result.StartLine, result.EndLine)
	if content == "" {
		if pathLabel != "" {
			return "Path: " + pathLabel
		}
		return ""
	}
	if pathLabel == "" {
		return content
	}
	return "Path: " + pathLabel + "\n" + content
}

func formatAIReadFileBatchToolResultContent(result mcpserver.ReadFileBatchResult) string {
	sections := make([]string, 0, len(result.Files))
	for _, file := range result.Files {
		if content := formatAIReadFileBatchEntryContent(file); content != "" {
			sections = append(sections, content)
		}
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func resolveAIReadFileResults(result any) ([]mcpserver.ReadFileResult, bool) {
	switch value := result.(type) {
	case mcpserver.ReadFileResult:
		return []mcpserver.ReadFileResult{value}, false
	case *mcpserver.ReadFileResult:
		if value == nil {
			return nil, false
		}
		return []mcpserver.ReadFileResult{*value}, false
	case mcpserver.ReadFileBatchResult:
		return value.Files, true
	case *mcpserver.ReadFileBatchResult:
		if value == nil {
			return nil, true
		}
		return value.Files, true
	default:
		return nil, false
	}
}

func buildAIReadFileTokenEstimateMeta(result any, profile AIProviderProfile) []map[string]interface{} {
	files, isBatch := resolveAIReadFileResults(result)
	if len(files) == 0 {
		return nil
	}
	estimates := make([]map[string]interface{}, 0, len(files))
	for _, file := range files {
		content := formatAIReadFileToolResultContent(file)
		if isBatch {
			content = formatAIReadFileBatchEntryContent(file)
		}
		tokenCount, tokenDisplay, ok := estimateAIResultTokens(content, profile)
		if !ok {
			tokenCount = 0
			tokenDisplay = "0.000000M"
		}
		estimates = append(estimates, map[string]interface{}{
			"path":         strings.TrimSpace(file.Path),
			"displayPath":  formatAIReadFilePathLabel(file.Path, file.StartLine, file.EndLine),
			"startLine":    file.StartLine,
			"endLine":      file.EndLine,
			"tokenCount":   tokenCount,
			"tokenDisplay": tokenDisplay,
		})
	}
	return estimates
}

func formatAIRawToolResultContent(result any) string {
	switch value := result.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	case mcpserver.ReadFileResult:
		return formatAIReadFileToolResultContent(value)
	case *mcpserver.ReadFileResult:
		if value == nil {
			return ""
		}
		return formatAIReadFileToolResultContent(*value)
	case mcpserver.ReadFileBatchResult:
		return formatAIReadFileBatchToolResultContent(value)
	case *mcpserver.ReadFileBatchResult:
		if value == nil {
			return ""
		}
		return formatAIReadFileBatchToolResultContent(*value)
	default:
		data, err := json.Marshal(result)
		if err == nil {
			return string(data)
		}
		return fmt.Sprintf("%v", result)
	}
}

func normalizeAICommandList(values []string) []string {
	if values == nil {
		return []string{}
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func findLongestAICommandPatternMatch(command string, patterns []string) string {
	if command == "" || len(patterns) == 0 {
		return ""
	}
	trimmedCommand := strings.ToLower(strings.TrimSpace(command))
	if trimmedCommand == "" {
		return ""
	}
	longestMatch := ""
	for _, pattern := range patterns {
		lowerPattern := strings.ToLower(strings.TrimSpace(pattern))
		if lowerPattern == "" {
			continue
		}
		if strings.HasPrefix(trimmedCommand, lowerPattern) {
			if longestMatch == "" || aiJSStringLength(lowerPattern) > aiJSStringLength(longestMatch) {
				longestMatch = lowerPattern
			}
		}
	}
	return longestMatch
}

func containsAIDangerousSubstitution(source string) bool {
	return aiDangerousParameterExpansionPattern.MatchString(source) ||
		aiParameterAssignmentWithOctalEscapesPattern.MatchString(source) ||
		aiParameterAssignmentWithHexEscapesPattern.MatchString(source) ||
		aiParameterAssignmentWithUnicodeEscapesPattern.MatchString(source) ||
		aiIndirectExpansionPattern.MatchString(source) ||
		aiHereStringWithSubstitutionPattern.MatchString(source) ||
		aiZshProcessSubstitutionPattern.MatchString(source) ||
		aiZshGlobQualifierPattern.MatchString(source)
}

func replaceAIPatternWithPlaceholders(input string, pattern *regexp.Regexp, placeholderPrefix string, values *[]string) string {
	return pattern.ReplaceAllStringFunc(input, func(match string) string {
		*values = append(*values, match)
		return fmt.Sprintf("__%s_%d__", placeholderPrefix, len(*values)-1)
	})
}

func scanAIQuotedSegment(input string, start int, quoteByte byte) (int, bool) {
	if start < 0 || start >= len(input) || input[start] != quoteByte {
		return 0, false
	}
	for i := start + 1; i < len(input); i++ {
		if quoteByte == '"' && input[i] == '\\' && i+1 < len(input) {
			i++
			continue
		}
		if input[i] == quoteByte {
			return i + 1, true
		}
	}
	return 0, false
}

func scanAIDoubleQuotedSegment(input string, start int) (int, bool) {
	return scanAIQuotedSegment(input, start, '"')
}

func scanAISingleQuotedSegment(input string, start int) (int, bool) {
	return scanAIQuotedSegment(input, start, '\'')
}

func scanAIParameterExpansionSegment(input string, start int) (int, bool) {
	if !strings.HasPrefix(input[start:], "${") {
		return 0, false
	}
	if endOffset := strings.IndexByte(input[start+2:], '}'); endOffset != -1 {
		return start + 3 + endOffset, true
	}
	return 0, false
}

func scanAIArithmeticBracketSegment(input string, start int) (int, bool) {
	if !strings.HasPrefix(input[start:], "$[") {
		return 0, false
	}
	if endOffset := strings.IndexByte(input[start+2:], ']'); endOffset != -1 {
		return start + 3 + endOffset, true
	}
	return 0, false
}

func scanAIBalancedParenSegment(input string, start int, prefix string) (int, bool) {
	if !strings.HasPrefix(input[start:], prefix) {
		return 0, false
	}
	depth := 1
	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false
	escaped := false
	for i := start + len(prefix); i < len(input); i++ {
		ch := input[i]
		if escaped {
			escaped = false
			continue
		}
		if inSingleQuote {
			if ch == '\'' {
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inDoubleQuote = false
			}
			continue
		}
		if inBacktick {
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '`' {
				inBacktick = false
			}
			continue
		}

		switch ch {
		case '\'':
			inSingleQuote = true
		case '"':
			inDoubleQuote = true
		case '`':
			inBacktick = true
		case '\\':
			escaped = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

func scanAIArithmeticDoubleParenSegment(input string, start int) (int, bool) {
	if !strings.HasPrefix(input[start:], "$((") {
		return 0, false
	}
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false
	escaped := false
	for i := start + 3; i < len(input); i++ {
		ch := input[i]
		if escaped {
			escaped = false
			continue
		}
		if inSingleQuote {
			if ch == '\'' {
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inDoubleQuote = false
			}
			continue
		}
		if inBacktick {
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '`' {
				inBacktick = false
			}
			continue
		}

		switch ch {
		case '\'':
			inSingleQuote = true
		case '"':
			inDoubleQuote = true
		case '`':
			inBacktick = true
		case '\\':
			escaped = true
		case '(':
			depth++
		case ')':
			if depth == 0 {
				if i+1 < len(input) && input[i+1] == ')' {
					return i + 2, true
				}
			} else {
				depth--
			}
		}
	}
	return 0, false
}

func scanAIBacktickSegment(input string, start int) (int, bool) {
	if start < 0 || start >= len(input) || input[start] != '`' {
		return 0, false
	}
	escaped := false
	for i := start + 1; i < len(input); i++ {
		if escaped {
			escaped = false
			continue
		}
		if input[i] == '\\' {
			escaped = true
			continue
		}
		if input[i] == '`' {
			return i + 1, true
		}
	}
	return 0, false
}

func replaceAIScannedSegments(input string, placeholderPrefix string, values *[]string, scanner func(string, int) (int, bool)) string {
	var builder strings.Builder
	for i := 0; i < len(input); {
		if end, ok := scanner(input, i); ok && end > i {
			*values = append(*values, input[i:end])
			builder.WriteString(fmt.Sprintf("__%s_%d__", placeholderPrefix, len(*values)-1))
			i = end
			continue
		}
		builder.WriteByte(input[i])
		i++
	}
	return builder.String()
}

func replaceAIProcessSubstitutionSegments(input string, subshells *[]string) string {
	var builder strings.Builder
	for i := 0; i < len(input); {
		if strings.HasPrefix(input[i:], "<(") || strings.HasPrefix(input[i:], ">(") {
			if end, ok := scanAIBalancedParenSegment(input, i, input[i:i+2]); ok {
				*subshells = append(*subshells, strings.TrimSpace(input[i+2:end-1]))
				builder.WriteString(fmt.Sprintf("__SUBSH_%d__", len(*subshells)-1))
				i = end
				continue
			}
		}
		builder.WriteByte(input[i])
		i++
	}
	return builder.String()
}

func replaceAISubshellSegments(input string, subshells *[]string) string {
	var builder strings.Builder
	for i := 0; i < len(input); {
		if strings.HasPrefix(input[i:], "$(") {
			if end, ok := scanAIBalancedParenSegment(input, i, "$("); ok {
				*subshells = append(*subshells, strings.TrimSpace(input[i+2:end-1]))
				builder.WriteString(fmt.Sprintf("__SUBSH_%d__", len(*subshells)-1))
				i = end
				continue
			}
		}
		if input[i] == '`' {
			if end, ok := scanAIBacktickSegment(input, i); ok {
				*subshells = append(*subshells, strings.TrimSpace(input[i+1:end-1]))
				builder.WriteString(fmt.Sprintf("__SUBSH_%d__", len(*subshells)-1))
				i = end
				continue
			}
		}
		builder.WriteByte(input[i])
		i++
	}
	return builder.String()
}

func restoreAIPlaceholderValues(input string, placeholderPrefix string, values []string) string {
	restored := input
	for index, value := range values {
		restored = strings.ReplaceAll(restored, fmt.Sprintf("__%s_%d__", placeholderPrefix, index), value)
	}
	return restored
}

func restoreAICommandPlaceholders(command string, quotes []string, redirections []string, arithmeticExpressions []string, parameterExpansions []string, variables []string, subshells []string) string {
	restored := command
	restored = restoreAIPlaceholderValues(restored, "QUOTE", quotes)
	restored = restoreAIPlaceholderValues(restored, "REDIR", redirections)
	restored = restoreAIPlaceholderValues(restored, "ARITH", arithmeticExpressions)
	restored = restoreAIPlaceholderValues(restored, "PARAM", parameterExpansions)
	restored = restoreAIPlaceholderValues(restored, "VAR", variables)
	restored = restoreAIPlaceholderValues(restored, "SUBSH", subshells)
	return restored
}

func tokenizeAICommandLine(command string) []string {
	tokens := make([]string, 0)
	var current strings.Builder
	flushCurrent := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if ch == ' ' || ch == '\t' {
			flushCurrent()
			continue
		}
		if i+1 < len(command) {
			pair := command[i : i+2]
			if pair == "&&" || pair == "||" {
				flushCurrent()
				tokens = append(tokens, pair)
				i++
				continue
			}
		}
		if ch == ';' || ch == '|' || ch == '&' {
			flushCurrent()
			tokens = append(tokens, string(ch))
			continue
		}
		current.WriteByte(ch)
	}

	flushCurrent()
	return tokens
}

func parseAICommandLine(command string) []string {
	if strings.TrimSpace(command) == "" {
		return []string{}
	}

	redirections := make([]string, 0)
	subshells := make([]string, 0)
	quotes := make([]string, 0)
	arithmeticExpressions := make([]string, 0)
	variables := make([]string, 0)
	parameterExpansions := make([]string, 0)

	processedCommand := replaceAIPatternWithPlaceholders(command, aiRedirectionPattern, "REDIR", &redirections)
	processedCommand = replaceAIScannedSegments(processedCommand, "ARITH", &arithmeticExpressions, scanAIArithmeticDoubleParenSegment)
	processedCommand = replaceAIScannedSegments(processedCommand, "ARITH", &arithmeticExpressions, scanAIArithmeticBracketSegment)
	processedCommand = replaceAIScannedSegments(processedCommand, "PARAM", &parameterExpansions, scanAIParameterExpansionSegment)
	processedCommand = replaceAIProcessSubstitutionSegments(processedCommand, &subshells)
	processedCommand = replaceAIPatternWithPlaceholders(processedCommand, aiSimpleVariablePattern, "VAR", &variables)
	processedCommand = replaceAIPatternWithPlaceholders(processedCommand, aiSpecialVariablePattern, "VAR", &variables)
	processedCommand = replaceAISubshellSegments(processedCommand, &subshells)
	processedCommand = replaceAIScannedSegments(processedCommand, "QUOTE", &quotes, scanAIDoubleQuotedSegment)
	processedCommand = replaceAIScannedSegments(processedCommand, "QUOTE", &quotes, scanAISingleQuotedSegment)

	tokens := tokenizeAICommandLine(processedCommand)
	commands := make([]string, 0)
	currentCommand := make([]string, 0)

	flushCommand := func() {
		if len(currentCommand) == 0 {
			return
		}
		commands = append(commands, strings.Join(currentCommand, " "))
		currentCommand = currentCommand[:0]
	}

	for _, token := range tokens {
		switch token {
		case "&&", "||", ";", "|", "&":
			flushCommand()
		default:
			subshellMatch := aiSubshellPlaceholderPattern.FindStringSubmatch(token)
			if len(subshellMatch) == 2 {
				flushCommand()
				index, err := strconv.Atoi(subshellMatch[1])
				if err == nil && index >= 0 && index < len(subshells) {
					commands = append(commands, subshells[index])
				}
				continue
			}
			currentCommand = append(currentCommand, token)
		}
	}
	flushCommand()

	restored := make([]string, 0, len(commands))
	for _, item := range commands {
		commandText := strings.TrimSpace(restoreAICommandPlaceholders(item, quotes, redirections, arithmeticExpressions, parameterExpansions, variables, subshells))
		if commandText == "" {
			continue
		}
		restored = append(restored, commandText)
	}
	return restored
}

func parseAICommand(command string) []string {
	if strings.TrimSpace(command) == "" {
		return []string{}
	}

	lines := strings.FieldsFunc(command, func(r rune) bool {
		return r == '\r' || r == '\n'
	})

	commands := make([]string, 0)
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		commands = append(commands, parseAICommandLine(line)...)
	}
	return commands
}

func stripFirstAIRedirection(command string) string {
	match := aiRedirectionPattern.FindStringIndex(command)
	if match == nil {
		return command
	}
	return command[:match[0]] + command[match[1]:]
}

func getAISingleCommandDecision(command string, allowedCommands []string, deniedCommands []string, allowAll bool) aiApprovalDecision {
	if command == "" {
		return aiApprovalDecisionAutoApprove
	}

	longestAllowedMatch := findLongestAICommandPatternMatch(command, allowedCommands)
	longestDeniedMatch := findLongestAICommandPatternMatch(command, deniedCommands)
	if allowAll {
		if longestDeniedMatch != "" {
			return aiApprovalDecisionAutoDeny
		}
		return aiApprovalDecisionAutoApprove
	}

	if longestAllowedMatch != "" && longestDeniedMatch == "" {
		return aiApprovalDecisionAutoApprove
	}
	if longestAllowedMatch == "" && longestDeniedMatch != "" {
		return aiApprovalDecisionAutoDeny
	}
	if longestAllowedMatch != "" && longestDeniedMatch != "" {
		if aiJSStringLength(longestAllowedMatch) > aiJSStringLength(longestDeniedMatch) {
			return aiApprovalDecisionAutoApprove
		}
		return aiApprovalDecisionAutoDeny
	}
	return aiApprovalDecisionAskUser
}

func getAICommandDecision(command string, allowedCommands []string, deniedCommands []string, allowAll bool) aiApprovalDecision {
	if strings.TrimSpace(command) == "" {
		return aiApprovalDecisionAutoApprove
	}

	subCommands := parseAICommand(command)
	decisions := make([]aiApprovalDecision, 0, len(subCommands))
	for _, subCommand := range subCommands {
		commandWithoutRedirection := strings.TrimSpace(stripFirstAIRedirection(subCommand))
		decisions = append(decisions, getAISingleCommandDecision(commandWithoutRedirection, allowedCommands, deniedCommands, allowAll))
	}

	for _, decision := range decisions {
		if decision == aiApprovalDecisionAutoDeny {
			return aiApprovalDecisionAutoDeny
		}
	}

	if containsAIDangerousSubstitution(command) {
		return aiApprovalDecisionAskUser
	}

	if len(decisions) == 0 {
		return aiApprovalDecisionAutoApprove
	}

	for _, decision := range decisions {
		if decision != aiApprovalDecisionAutoApprove {
			return aiApprovalDecisionAskUser
		}
	}
	return aiApprovalDecisionAutoApprove
}

func getAIExecuteCommandDecision(settings AIConversationTaskSettings, command string, rawIsMutating string) aiApprovalDecision {
	if !isAIAutoApprovalEffectivelyEnabled(settings) {
		return aiApprovalDecisionAskUser
	}
	if !settings.AlwaysAllowExecute {
		return aiApprovalDecisionAskUser
	}

	deniedCommands := normalizeAICommandList(settings.DeniedCommands)
	allowedCommands := normalizeAICommandList(settings.AllowedCommands)
	executeApprovalMode := normalizeAIExecuteApprovalMode(settings.ExecuteApprovalMode)
	allowAll := executeApprovalMode == "all" || (executeApprovalMode == "read_only" && strings.TrimSpace(rawIsMutating) != "1")
	return getAICommandDecision(command, allowedCommands, deniedCommands, allowAll)
}

func getAIParsedToolUseDecision(settings AIConversationTaskSettings, tool aiParsedToolUse) aiApprovalDecision {
	toolName := strings.TrimSpace(tool.Name)
	if _, ok := aiAlwaysAutoApprovedToolNames[toolName]; ok {
		return aiApprovalDecisionAutoApprove
	}
	if isAIMCPClientToolAlwaysAllowed(tool) {
		return aiApprovalDecisionAutoApprove
	}
	if !isAIAutoApprovalEffectivelyEnabled(settings) {
		return aiApprovalDecisionAskUser
	}
	switch getAIAutoApprovalCategoryForTool(toolName) {
	case "read":
		if settings.AlwaysAllowReadOnly {
			return aiApprovalDecisionAutoApprove
		}
	case "write":
		if settings.AlwaysAllowWrite {
			return aiApprovalDecisionAutoApprove
		}
	case "execute":
		return getAIExecuteCommandDecision(settings, tool.Params["command"], tool.Params["is_mutating"])
	}
	return aiApprovalDecisionAskUser
}

func isRawPreserveToolParam(toolName string, paramName string) bool {
	return (toolName == "write_to_file" && paramName == "content") || (toolName == "apply_diff" && (paramName == "diff" || paramName == "args"))
}

func stripOuterToolParamNewlines(value string) string {
	return strings.TrimSuffix(strings.TrimPrefix(value, "\n"), "\n")
}

func skipAIToolXMLWhitespace(value string, startIndex int) int {
	cursor := startIndex
	for cursor < len(value) {
		switch value[cursor] {
		case ' ', '\t', '\r', '\n':
			cursor++
		default:
			return cursor
		}
	}
	return cursor
}

func buildAIToolTagCandidates() []aiToolTagCandidate {
	candidates := make([]aiToolTagCandidate, 0, len(aiSupportedToolNames))
	for _, toolName := range aiSupportedToolNames {
		candidates = append(candidates, aiToolTagCandidate{
			Name:       toolName,
			OpeningTag: fmt.Sprintf("<%s>", toolName),
			ClosingTag: fmt.Sprintf("</%s>", toolName),
		})
	}
	return candidates
}

func buildAIToolParamTagCandidates() []aiToolParamTagCandidate {
	candidates := make([]aiToolParamTagCandidate, 0, len(aiSupportedToolParamNames))
	for _, paramName := range aiSupportedToolParamNames {
		candidates = append(candidates, aiToolParamTagCandidate{
			ParamName:  paramName,
			OpeningTag: fmt.Sprintf("<%s>", paramName),
			ClosingTag: fmt.Sprintf("</%s>", paramName),
		})
	}
	return candidates
}

func findAIImplicitToolParamBoundary(xmlContent string, startIndex int, toolClosingTag string, paramCandidates []aiToolParamTagCandidate) int {
	nextBoundary := len(xmlContent)
	if closingIndex := strings.Index(xmlContent[startIndex:], toolClosingTag); closingIndex != -1 {
		nextBoundary = startIndex + closingIndex
	}
	for _, paramCandidate := range paramCandidates {
		if openingIndex := strings.Index(xmlContent[startIndex:], paramCandidate.OpeningTag); openingIndex != -1 {
			candidateIndex := startIndex + openingIndex
			if candidateIndex < nextBoundary {
				nextBoundary = candidateIndex
			}
		}
	}
	return nextBoundary
}

func parseAINextToolUseFromXML(xmlContent string, startIndex int) (aiParsedToolUse, int, bool) {
	cursor := skipAIToolXMLWhitespace(xmlContent, startIndex)
	toolCandidates := buildAIToolTagCandidates()
	paramCandidates := buildAIToolParamTagCandidates()

	for _, candidate := range toolCandidates {
		if !strings.HasPrefix(xmlContent[cursor:], candidate.OpeningTag) {
			continue
		}

		tool := aiParsedToolUse{
			Name:   candidate.Name,
			Params: map[string]string{},
		}
		toolStartIndex := cursor
		cursor += len(candidate.OpeningTag)
		if candidate.Name == "ask_followup_question" {
			closingIndex := strings.Index(xmlContent[cursor:], candidate.ClosingTag)
			if closingIndex == -1 {
				return aiParsedToolUse{}, 0, false
			}
			tool.Params["follow_up"] = strings.TrimSpace(stripOuterToolParamNewlines(xmlContent[cursor : cursor+closingIndex]))
			tool.RawXML = xmlContent[toolStartIndex : cursor+closingIndex+len(candidate.ClosingTag)]
			tool.StartIndex = toolStartIndex
			tool.EndIndex = cursor + closingIndex + len(candidate.ClosingTag)
			return tool, tool.EndIndex, true
		}
		currentParamName := ""
		currentParamValueStart := 0
		currentParamClosingTag := ""

		for cursor < len(xmlContent) {
			if currentParamName != "" {
				closingIndex := strings.Index(xmlContent[cursor:], currentParamClosingTag)
				if closingIndex == -1 {
					paramValueEnd := findAIImplicitToolParamBoundary(xmlContent, cursor, candidate.ClosingTag, paramCandidates)
					paramValue := xmlContent[currentParamValueStart:paramValueEnd]
					if isRawPreserveToolParam(tool.Name, currentParamName) {
						tool.Params[currentParamName] = stripOuterToolParamNewlines(paramValue)
					} else {
						tool.Params[currentParamName] = normalizeAINonRawToolParamValue(paramValue)
					}
					cursor = paramValueEnd
					currentParamName = ""
					currentParamClosingTag = ""
					continue
				}
				paramValueEnd := cursor + closingIndex
				paramValue := xmlContent[currentParamValueStart:paramValueEnd]
				if isRawPreserveToolParam(tool.Name, currentParamName) {
					tool.Params[currentParamName] = stripOuterToolParamNewlines(paramValue)
				} else {
					tool.Params[currentParamName] = normalizeAINonRawToolParamValue(paramValue)
				}
				cursor = paramValueEnd + len(currentParamClosingTag)
				currentParamName = ""
				currentParamClosingTag = ""
				continue
			}

			if strings.HasPrefix(xmlContent[cursor:], candidate.ClosingTag) {
				tool.RawXML = xmlContent[toolStartIndex : cursor+len(candidate.ClosingTag)]
				tool.StartIndex = toolStartIndex
				tool.EndIndex = cursor + len(candidate.ClosingTag)
				return tool, cursor + len(candidate.ClosingTag), true
			}

			matchedParam := false
			for _, paramCandidate := range paramCandidates {
				if !strings.HasPrefix(xmlContent[cursor:], paramCandidate.OpeningTag) {
					continue
				}
				currentParamName = paramCandidate.ParamName
				cursor += len(paramCandidate.OpeningTag)
				currentParamValueStart = cursor
				currentParamClosingTag = paramCandidate.ClosingTag
				matchedParam = true
				break
			}
			if matchedParam {
				continue
			}

			cursor++
		}

		tool.RawXML = xmlContent[toolStartIndex:cursor]
		tool.StartIndex = toolStartIndex
		tool.EndIndex = cursor
		return tool, cursor, true
	}

	return aiParsedToolUse{}, 0, false
}

func parseToolUsesFromXML(xmlContent string) []aiParsedToolUse {
	parsedUses := make([]aiParsedToolUse, 0)
	for cursor := 0; cursor < len(xmlContent); {
		tool, nextCursor, ok := parseAINextToolUseFromXML(xmlContent, cursor)
		if ok && nextCursor > cursor {
			parsedUses = append(parsedUses, tool)
			cursor = nextCursor
			continue
		}
		cursor++
	}
	return parsedUses
}

func stripParsedToolUsesFromText(content string, tools []aiParsedToolUse) string {
	stripped := content
	for _, tool := range tools {
		if strings.TrimSpace(tool.RawXML) == "" {
			continue
		}
		stripped = strings.Replace(stripped, tool.RawXML, "", 1)
	}
	return strings.TrimSpace(stripped)
}

func fixAIBareToolOpenTags(content string) string {
	if aiBareToolOpenTagPattern == nil || !strings.Contains(content, ">") {
		return content
	}
	// match groups: (indent)(name)>  → indent + "<" + name + ">"
	return aiBareToolOpenTagPattern.ReplaceAllString(content, `${1}<${2}>`)
}

// sanitizeAIAssistantToolProtocolText repairs common model slips so valid tool XML can parse.
// It does not invent tools; empty / non-tool text stays non-tool.
func sanitizeAIAssistantToolProtocolText(content string) string {
	text := strings.TrimSpace(content)
	if text == "" {
		return ""
	}
	// 部分模型(如 DeepSeek)会把原生工具调用以 DSML 标记泄漏进正文。宿主无法执行这种格式,
	// 残缺块(只剩闭合标签、参数内容丢失)更会污染后面的工具协议解析,所以直接丢弃标记及其
	// 之后的整块内容,只保留标记之前的正文。若标记之前也没有正文,就返回空,
	// 交给调用方的空响应重试去重新生成。
	if index := strings.Index(text, aiToolProtocolDSMLMarker); index >= 0 {
		cutIndex := index
		// 标记前的 '<'(以及可能的 '/')属于同一个标签,必须一起丢弃,
		// 否则会剩下一个孤立的尖括号,又变成被包成假答复的垃圾文本。
		if openIndex := strings.LastIndex(text[:cutIndex], "<"); openIndex >= 0 {
			cutIndex = openIndex
		}
		text = strings.TrimSpace(text[:cutIndex])
		if text == "" {
			return ""
		}
	}
	text = aiToolProtocolStreamJunkPattern.ReplaceAllString(text, "")
	text = aiToolProtocolWrapperTagPattern.ReplaceAllString(text, "")
	text = fixAIBareToolOpenTags(text)
	text = aiToolProtocolWrapperTagPattern.ReplaceAllString(text, "")
	text = aiToolProtocolStreamJunkPattern.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func containsAINonToolXMLMarkup(content string) bool {
	return aiGenericXMLTagPattern.MatchString(strings.TrimSpace(content))
}

func containsAIRecognizedToolTag(content string) bool {
	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return false
	}
	for _, candidate := range buildAIToolTagCandidates() {
		if strings.Contains(trimmedContent, candidate.OpeningTag) || strings.Contains(trimmedContent, candidate.ClosingTag) {
			return true
		}
	}
	return false
}

// isAIAssistantToolProtocolIgnorableRemainder allows prose / already-stripped wrappers
// when at least one tool already parsed successfully. Still rejects leftover tool tags
// (malformed extra calls) so the model gets a protocol retry for those.
func isAIAssistantToolProtocolIgnorableRemainder(remaining string) bool {
	cleaned := sanitizeAIAssistantToolProtocolText(remaining)
	if cleaned == "" {
		return true
	}
	if containsAIRecognizedToolTag(cleaned) {
		return false
	}
	return true
}

func parseAssistantToolUses(content string) ([]aiParsedToolUse, error) {
	tools, _, err := parseAssistantToolUsesWithDuplicateCount(content)
	return tools, err
}

func parseAssistantToolUsesWithDuplicateCount(content string) ([]aiParsedToolUse, int, error) {
	trimmedContent := sanitizeAIAssistantToolProtocolText(content)
	if trimmedContent == "" {
		return nil, 0, fmt.Errorf("assistant response did not contain any tool calls")
	}
	allParsedTools := parseToolUsesFromXML(trimmedContent)
	parsedTools, duplicateToolCount := dedupeParsedToolUsesWithCount(allParsedTools)
	if len(parsedTools) == 0 {
		return nil, 0, fmt.Errorf("assistant response did not contain any recognized tool calls")
	}
	remainingText := stripParsedToolUsesFromText(trimmedContent, allParsedTools)
	if remainingText != "" && !isAIAssistantToolProtocolIgnorableRemainder(remainingText) {
		return nil, duplicateToolCount, fmt.Errorf("assistant response must contain only direct tool calls with no extra text or XML container")
	}
	if err := validateAIStandaloneOnlyBatchTools(parsedTools); err != nil {
		return nil, duplicateToolCount, err
	}
	return parsedTools, duplicateToolCount, nil
}

func isAIStandaloneOnlyBatchTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "ask_followup_question", "attempt_completion", aiThemeToolName:
		return true
	default:
		return false
	}
}

// validateAIStandaloneOnlyBatchTools 只在整批都是独占工具且多于一个时报协议错误。
// 混合批次(独占工具 + 普通工具)不再整批作废, 由 filterAIStandaloneOnlyBatchTools 在执行层剔除独占工具。
func validateAIStandaloneOnlyBatchTools(tools []aiParsedToolUse) error {
	if len(tools) <= 1 {
		return nil
	}
	standaloneToolCount := 0
	for _, tool := range tools {
		if isAIStandaloneOnlyBatchTool(tool.Name) {
			standaloneToolCount++
		}
	}
	if standaloneToolCount != len(tools) {
		return nil
	}
	return fmt.Errorf("%s must be the only tool call in the reply", strings.TrimSpace(tools[0].Name))
}

// filterAIStandaloneOnlyBatchTools 仅过滤执行列表, 不改写 assistant 原文与 API 历史,
// 因此下一轮请求前缀与提示缓存亲和性不受影响。
func filterAIStandaloneOnlyBatchTools(tools []aiParsedToolUse) ([]aiParsedToolUse, []string) {
	if len(tools) <= 1 {
		return tools, nil
	}
	filteredTools := make([]aiParsedToolUse, 0, len(tools))
	skippedToolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		if isAIStandaloneOnlyBatchTool(tool.Name) {
			skippedToolNames = append(skippedToolNames, strings.TrimSpace(tool.Name))
			continue
		}
		filteredTools = append(filteredTools, tool)
	}
	if len(filteredTools) == 0 {
		return tools, nil
	}
	return filteredTools, skippedToolNames
}

// rebuildAIAssistantToolOnlyContent keeps only successfully parsed tool XML.
// Used when replaying history so old wrapper/prose slips do not re-poison the model.
func rebuildAIAssistantToolOnlyContent(content string) string {
	tools, err := parseAssistantToolUses(content)
	if err != nil || len(tools) == 0 {
		return sanitizeAIAssistantToolProtocolText(content)
	}
	parts := make([]string, 0, len(tools))
	for _, tool := range tools {
		raw := strings.TrimSpace(tool.RawXML)
		if raw == "" {
			continue
		}
		parts = append(parts, raw)
	}
	if len(parts) == 0 {
		return sanitizeAIAssistantToolProtocolText(content)
	}
	return strings.Join(parts, "\n")
}

// purgeAIProtocolRetryNoiseFromMessages rewrites assistant history to clean tool XML so
// prior protocol failures do not loop.
//
// Protocol-retry user prompts are kept: they are permanent history like any other user
// message, which also keeps the request prefix stable for prompt caching.
func purgeAIProtocolRetryNoiseFromMessages(messages []AIChatRequestMessage) []AIChatRequestMessage {
	if len(messages) == 0 {
		return messages
	}
	cleaned := make([]AIChatRequestMessage, 0, len(messages))
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		content := strings.TrimSpace(message.Content)
		if role == "assistant" {
			rewritten := rebuildAIAssistantToolOnlyContent(content)
			if rewritten == "" {
				continue
			}
			if rewritten != content {
				// 文本被改写后与紧凑回放的内容块不再一致, 丢弃元数据让这条消息降级为纯文本,
				// 避免用旧文本重建出与本地记录不符的原生 item。
				message.ContentBlocks = nil
				message.CacheObjects = nil
			}
			content = rewritten
			message.Content = content
		}
		cleaned = append(cleaned, message)
	}
	return cleaned
}

func buildInvalidToolProtocolRetryMessage(detail string) string {
	trimmedDetail := strings.TrimSpace(detail)
	if trimmedDetail == "" {
		trimmedDetail = "assistant response violated the XML tool protocol"
	}
	return strings.TrimSpace(fmt.Sprintf(`[ERROR] Invalid tool protocol in your previous response: %s

Your next reply must be a minimal valid tool reply.
Output direct tool tags only, with no extra XML wrapper or container.
All opened tool tags and parameter tags must be properly closed.
For example: <attempt_completion><result>...</result></attempt_completion>
If you use the completion tool, it must be the only tool call.
If you use the follow-up tool, it must be the only tool call.
Do not add explanation or recovery commentary outside the tool reply.
Output the tool reply and stop.`, trimmedDetail))
}

func buildAIDuplicateToolProtocolConflictMessage(duplicateToolCount int) string {
	if duplicateToolCount <= 0 {
		return ""
	}
	envelopeLabel := "duplicate top-level tool XML element was"
	if duplicateToolCount != 1 {
		envelopeLabel = "duplicate top-level tool XML elements were"
	}
	return strings.TrimSpace(fmt.Sprintf(`[SYSTEM_TOOL_PROTOCOL_CONFLICT]
The current host uses direct XML tool calls as its only authoritative tool protocol. %d %s detected in the same assistant response because the complete RawXML string was exactly identical to an earlier tool call. Later duplicate tool call(s) were ignored; the first occurrence was retained.
This indicates a protocol compatibility conflict between the current host XML protocol and an additional provider or model response protocol. Do not repeat the ignored tool call(s). Do not emit native tool_calls/function calling, commentary/final_answer copies, extra XML containers, or duplicate XML tool tags. Continue with one direct XML tool tag per logical tool call.`, duplicateToolCount, envelopeLabel))
}

func (a *Service) emitAIDuplicateToolProtocolConflictMessage(requestID string, duplicateToolCount int) AIChatRequestMessage {
	content := buildAIDuplicateToolProtocolConflictMessage(duplicateToolCount)
	if content == "" {
		return AIChatRequestMessage{}
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "api_message_append",
		"requestId": requestID,
		"message": map[string]interface{}{
			"messageId": fmt.Sprintf("api-tool-protocol-conflict-%d", time.Now().UnixNano()),
			"role":      "user",
			"content":   content,
			"ts":        time.Now().UnixMilli(),
		},
	})
	return AIChatRequestMessage{Role: "user", Content: content}
}

func buildAIStandaloneToolSkippedMessage(skippedToolNames []string) string {
	normalizedToolNames := make([]string, 0, len(skippedToolNames))
	for _, name := range skippedToolNames {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			normalizedToolNames = append(normalizedToolNames, trimmed)
		}
	}
	if len(normalizedToolNames) == 0 {
		return ""
	}
	toolLabel := "tool"
	pronounLabel := "it"
	if len(normalizedToolNames) != 1 {
		toolLabel = "tools"
		pronounLabel = "them"
	}
	return strings.TrimSpace(fmt.Sprintf(`[SYSTEM_TOOL_PROTOCOL_CONFLICT]
The following standalone-only %s appeared alongside other tool calls in the same assistant response and was skipped: %s. A standalone-only tool must be the only tool call in the reply.
The remaining tool calls in that response were executed normally, and their results follow the current host protocol. Do not repeat the executed tool calls. If you still need the skipped %s, call %s alone in a separate reply with no other tool call.`, toolLabel, strings.Join(normalizedToolNames, ", "), toolLabel, pronounLabel))
}

func (a *Service) emitAIStandaloneToolSkippedMessage(requestID string, skippedToolNames []string) AIChatRequestMessage {
	content := buildAIStandaloneToolSkippedMessage(skippedToolNames)
	if content == "" {
		return AIChatRequestMessage{}
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "api_message_append",
		"requestId": requestID,
		"message": map[string]interface{}{
			"messageId": fmt.Sprintf("api-standalone-tool-skipped-%d", time.Now().UnixNano()),
			"role":      "user",
			"content":   content,
			"ts":        time.Now().UnixMilli(),
		},
	})
	return AIChatRequestMessage{Role: "user", Content: content}
}

func buildParsedToolUseDedupeKey(tool aiParsedToolUse) string {
	return tool.RawXML
}

func dedupeParsedToolUsesWithCount(tools []aiParsedToolUse) ([]aiParsedToolUse, int) {
	if len(tools) <= 1 {
		return tools, 0
	}
	seen := make(map[string]struct{}, len(tools))
	deduped := make([]aiParsedToolUse, 0, len(tools))
	duplicateToolCount := 0
	for _, tool := range tools {
		key := buildParsedToolUseDedupeKey(tool)
		if key == "" {
			deduped = append(deduped, tool)
			continue
		}
		if _, exists := seen[key]; exists {
			duplicateToolCount++
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, tool)
	}
	return deduped, duplicateToolCount
}

func stripAssistantToolXML(content string) string {
	trimmedContent := sanitizeAIAssistantToolProtocolText(content)
	if trimmedContent == "" {
		return ""
	}
	parsedTools := parseToolUsesFromXML(trimmedContent)
	if len(parsedTools) == 0 {
		return trimmedContent
	}
	return stripParsedToolUsesFromText(trimmedContent, parsedTools)
}

func normalizeAINonRawToolParamValue(value string) string {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return ""
	}
	return html.UnescapeString(trimmedValue)
}

func buildAIAttemptCompletionToolText(resultText string) string {
	return strings.TrimSpace(fmt.Sprintf("<attempt_completion>\n<result>%s</result>\n</attempt_completion>", html.EscapeString(resultText)))
}

func replaceAILatestAssistantMessageContent(messages []AIChatRequestMessage, content string) []AIChatRequestMessage {
	if len(messages) == 0 {
		return messages
	}
	cloned := append([]AIChatRequestMessage{}, messages...)
	for index := len(cloned) - 1; index >= 0; index-- {
		if strings.ToLower(strings.TrimSpace(cloned[index].Role)) != "assistant" {
			continue
		}
		if strings.TrimSpace(cloned[index].Content) != strings.TrimSpace(content) {
			// 同上: 改写文本后必须丢弃紧凑回放元数据, 否则内容块与元数据错位。
			cloned[index].ContentBlocks = nil
			cloned[index].CacheObjects = nil
		}
		cloned[index].Content = content
		return cloned
	}
	return append(cloned, AIChatRequestMessage{
		Role:    "assistant",
		Content: content,
	})
}

func normalizeAIAssistantRoundResultForToolProtocol(roundResult aiChatRoundResult) aiChatRoundResult {
	trimmedText := sanitizeAIAssistantToolProtocolText(roundResult.Text)
	if trimmedText == "" {
		return roundResult
	}
	// Prefer sanitized text when it actually exposes parseable tools (wrapper/bare-tag repairs).
	if sanitizedTools := parseToolUsesFromXML(trimmedText); len(sanitizedTools) > 0 {
		if trimmedText != strings.TrimSpace(roundResult.Text) {
			roundResult.Text = trimmedText
			if len(roundResult.NextRequestMessages) > 0 {
				roundResult.NextRequestMessages = replaceAILatestAssistantMessageContent(roundResult.NextRequestMessages, trimmedText)
			}
		}
		return roundResult
	}
	if containsAIRecognizedToolTag(trimmedText) || containsAINonToolXMLMarkup(trimmedText) {
		if trimmedText != strings.TrimSpace(roundResult.Text) {
			roundResult.Text = trimmedText
			if len(roundResult.NextRequestMessages) > 0 {
				roundResult.NextRequestMessages = replaceAILatestAssistantMessageContent(roundResult.NextRequestMessages, trimmedText)
			}
		}
		return roundResult
	}
	wrappedText := buildAIAttemptCompletionToolText(trimmedText)
	roundResult.Text = wrappedText
	if len(roundResult.NextRequestMessages) > 0 {
		roundResult.NextRequestMessages = replaceAILatestAssistantMessageContent(roundResult.NextRequestMessages, wrappedText)
	}
	return roundResult
}

func (a *Service) getAIProviderProfileByID(providerID string) (AIProviderProfile, error) {
	if a == nil || a.configManager == nil {
		return AIProviderProfile{}, fmt.Errorf("供应商状态不可用")
	}
	state := a.configManager.GetAIProviderState()
	if len(state.Providers) == 0 {
		return AIProviderProfile{}, fmt.Errorf("尚未配置供应商")
	}
	trimmedProviderID := strings.TrimSpace(providerID)
	if trimmedProviderID != "" {
		for _, profile := range state.Providers {
			if profile.ID == trimmedProviderID {
				return profile, nil
			}
		}
	}
	if strings.TrimSpace(state.CurrentProviderID) != "" {
		for _, profile := range state.Providers {
			if profile.ID == state.CurrentProviderID {
				return profile, nil
			}
		}
	}
	return state.Providers[0], nil
}

func (a *Service) getAIProviderProfileForConversation(conversationID string) (AIProviderProfile, error) {
	if a == nil || a.configManager == nil {
		return AIProviderProfile{}, fmt.Errorf("供应商状态不可用")
	}
	trimmedConversationID := strings.TrimSpace(conversationID)
	if trimmedConversationID != "" {
		snapshot, err := a.configManager.GetAIConversation(trimmedConversationID)
		if err == nil {
			return a.getAIProviderProfileByID(snapshot.Settings.CurrentProviderID)
		}
	}
	globalSettings := a.configManager.GetAIGlobalSettings()
	return a.getAIProviderProfileByID(globalSettings.CurrentProviderID)
}

// isAIChatSupportedProviderProfile 判定供应商快照是否可直接用于发起对话请求。
// StartAIChat 保留自己的分段校验以便向用户返回具体原因, 此处只做可用性判定。
func isAIChatSupportedProviderProfile(profile AIProviderProfile) bool {
	switch profile.Provider {
	case "Compatible", "Responses", "Messages":
	default:
		return false
	}
	return strings.TrimSpace(profile.BaseURL) != "" && strings.TrimSpace(profile.Model) != ""
}

// resolveAIChatContinuationProfile 在自动续跑轮次前重解析供应商快照,
// 使请求进行中切换的供应商, 模型与思考档位从下一轮开始生效, 并写回批次保持后续轮次一致。
// 只有读到会话快照且解析结果可用时才替换; 读不到会话快照时必须沿用批次内已固化的快照,
// 因为临时会话不落在持久化目录, 回落全局设置会让同一任务中途跨到另一个供应商。
func (a *Service) resolveAIChatContinuationProfile(batch *aiPendingToolBatch) AIProviderProfile {
	if a == nil || batch == nil {
		return AIProviderProfile{}
	}
	if a.configManager == nil {
		return batch.Profile
	}
	trimmedConversationID := strings.TrimSpace(batch.Payload.ConversationID)
	if trimmedConversationID == "" {
		return batch.Profile
	}
	snapshot, err := a.configManager.GetAIConversation(trimmedConversationID)
	if err != nil {
		return batch.Profile
	}
	profile, err := a.getAIProviderProfileByID(snapshot.Settings.CurrentProviderID)
	if err != nil || !isAIChatSupportedProviderProfile(profile) {
		return batch.Profile
	}
	batch.Profile = profile
	return profile
}

func (a *Service) getAIAutoApprovalSettingsForConversation(conversationID string) AIConversationTaskSettings {
	if a == nil || a.configManager == nil {
		return AIConversationTaskSettings{}
	}
	globalSettings := a.configManager.GetAIGlobalSettings()
	settings := a.configManager.GetAIConversationTaskSettings(conversationID)
	settings.AllowedCommands = normalizeAIStringList(globalSettings.AllowedCommands)
	settings.DeniedCommands = normalizeAIStringList(globalSettings.DeniedCommands)
	return settings
}

func (a *Service) setAIChatRequestCancel(requestID string, cancel context.CancelFunc) {
	if a == nil || requestID == "" || cancel == nil {
		return
	}
	a.aiChatReqMu.Lock()
	if existing := a.aiChatReqCancel[requestID]; existing != nil {
		existing()
	}
	a.aiChatReqCancel[requestID] = cancel
	a.aiChatReqMu.Unlock()
}

func (a *Service) popAIChatRequestCancel(requestID string) context.CancelFunc {
	if a == nil || requestID == "" {
		return nil
	}
	a.aiChatReqMu.Lock()
	cancel := a.aiChatReqCancel[requestID]
	delete(a.aiChatReqCancel, requestID)
	a.aiChatReqMu.Unlock()
	return cancel
}

func (a *Service) setAIChatPendingToolBatch(requestID string, batch *aiPendingToolBatch) {
	if a == nil || requestID == "" || batch == nil {
		return
	}
	a.aiPendingToolMu.Lock()
	a.aiPendingToolBatches[requestID] = batch
	a.aiPendingToolMu.Unlock()
}

func (a *Service) popAIChatPendingToolBatch(requestID string) *aiPendingToolBatch {
	if a == nil || requestID == "" {
		return nil
	}
	a.aiPendingToolMu.Lock()
	batch := a.aiPendingToolBatches[requestID]
	delete(a.aiPendingToolBatches, requestID)
	a.aiPendingToolMu.Unlock()
	return batch
}

func (a *Service) setAIChatPendingFollowupBatch(requestID string, batch *aiPendingToolBatch) {
	if a == nil || requestID == "" || batch == nil {
		return
	}
	a.aiPendingFollowupMu.Lock()
	a.aiPendingFollowupBatches[requestID] = batch
	a.aiPendingFollowupMu.Unlock()
}

func (a *Service) popAIChatPendingFollowupBatch(requestID string) *aiPendingToolBatch {
	if a == nil || requestID == "" {
		return nil
	}
	a.aiPendingFollowupMu.Lock()
	batch := a.aiPendingFollowupBatches[requestID]
	delete(a.aiPendingFollowupBatches, requestID)
	a.aiPendingFollowupMu.Unlock()
	return batch
}

func (a *Service) finishAIChatRequest(requestID string) {
	if a == nil || requestID == "" {
		return
	}
	a.popAIChatPendingToolBatch(requestID)
	a.popAIChatPendingFollowupBatch(requestID)
	collaborationState := a.popAIChatCollaborationState(requestID)
	if collaborationState != nil && collaborationState.Cancel != nil {
		collaborationState.Cancel()
	}
	a.popAIChatRequestCancel(requestID)
	a.setAIChatSkipNextAutomaticRequest(requestID, false)
}

func (a *Service) setAIChatSkipNextAutomaticRequest(requestID string, enabled bool) {
	trimmedRequestID := strings.TrimSpace(requestID)
	if a == nil || trimmedRequestID == "" {
		return
	}
	a.aiSkipNextAutoReqMu.Lock()
	if enabled {
		a.aiSkipNextAutomaticReqMap[trimmedRequestID] = true
	} else {
		delete(a.aiSkipNextAutomaticReqMap, trimmedRequestID)
	}
	a.aiSkipNextAutoReqMu.Unlock()
}

func (a *Service) consumeAIChatSkipNextAutomaticRequest(requestID string) bool {
	trimmedRequestID := strings.TrimSpace(requestID)
	if a == nil || trimmedRequestID == "" {
		return false
	}
	a.aiSkipNextAutoReqMu.Lock()
	enabled := a.aiSkipNextAutomaticReqMap[trimmedRequestID]
	delete(a.aiSkipNextAutomaticReqMap, trimmedRequestID)
	a.aiSkipNextAutoReqMu.Unlock()
	return enabled
}

func (a *Service) SetAIChatSkipNextAutomaticRequest(requestID string, enabled bool) {
	a.setAIChatSkipNextAutomaticRequest(requestID, enabled)
}

func (a *Service) emitAIChatEvent(payload map[string]interface{}) {
	if a == nil || a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "ai-chat-stream", payload)
}

func (a *Service) emitAIChatRuntimePhase(requestID string, phase string) {
	trimmedRequestID := strings.TrimSpace(requestID)
	trimmedPhase := strings.TrimSpace(phase)
	if a == nil || trimmedRequestID == "" || trimmedPhase == "" {
		return
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "runtime_phase",
		"requestId": trimmedRequestID,
		"phase":     trimmedPhase,
	})
}

func decodeAIChatRequestPayload(raw string) (AIChatRequestPayload, error) {
	payload := AIChatRequestPayload{}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return payload, fmt.Errorf("消息内容为空")
	}

	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			return payload, err
		}
		payload.Messages = normalizeAIChatRequestMessages(payload.Messages)
		return payload, nil
	}

	var messages []AIChatRequestMessage
	if err := json.Unmarshal([]byte(trimmed), &messages); err != nil {
		return payload, err
	}
	payload.Messages = normalizeAIChatRequestMessages(messages)
	return payload, nil
}

func shouldUseAIAssistantFirstReply(payload AIChatRequestPayload, requestMessages []AIChatRequestMessage) bool {
	if strings.TrimSpace(payload.AssistantFirstReplyText) == "" {
		return false
	}
	if countAIExplicitUserPromptMessages(requestMessages) > 1 {
		return false
	}
	for _, message := range requestMessages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "assistant") {
			return false
		}
	}
	return true
}

func buildAIAssistantFirstReplyStreamChunks(text string) []string {
	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return nil
	}
	runes := []rune(trimmedText)
	chunks := make([]string, 0, (len(runes)+23)/24)
	for start := 0; start < len(runes); {
		end := start + 24
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
		start = end
	}
	return chunks
}

func waitForAIAssistantFirstReplyChunk(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return ctx == nil || ctx.Err() == nil
	}
	if ctx == nil {
		time.Sleep(duration)
		return true
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (a *Service) requestAIAssistantFirstReplyRound(ctx context.Context, requestID string, payload AIChatRequestPayload, requestMessages []AIChatRequestMessage) (aiChatRoundResult, error) {
	result := aiChatRoundResult{}
	assistantFirstReplyText := strings.TrimSpace(payload.AssistantFirstReplyText)
	if assistantFirstReplyText == "" {
		return result, fmt.Errorf("missing assistant first reply")
	}
	startedAt := time.Now()
	firstTokenAt := time.Time{}
	var contentBuilder strings.Builder
	chunks := buildAIAssistantFirstReplyStreamChunks(assistantFirstReplyText)
	for index, chunk := range chunks {
		if ctx != nil && ctx.Err() != nil {
			return result, ctx.Err()
		}
		if firstTokenAt.IsZero() && strings.TrimSpace(chunk) != "" {
			firstTokenAt = time.Now()
		}
		contentBuilder.WriteString(chunk)
		a.emitAIChatEvent(map[string]interface{}{
			"kind":      "delta",
			"requestId": requestID,
			"delta":     chunk,
		})
		if index < len(chunks)-1 && !waitForAIAssistantFirstReplyChunk(ctx, 24*time.Millisecond) {
			return result, context.Canceled
		}
	}
	result.Text = strings.TrimSpace(contentBuilder.String())
	if result.Text == "" {
		result.Text = assistantFirstReplyText
	}
	if !firstTokenAt.IsZero() {
		result.FirstTokenMs = firstTokenAt.Sub(startedAt).Milliseconds()
	}
	result.ElapsedMs = time.Since(startedAt).Milliseconds()
	result.NextRequestMessages = append([]AIChatRequestMessage{}, requestMessages...)
	result.NextRequestMessages = append(result.NextRequestMessages, AIChatRequestMessage{
		Role:    "assistant",
		Content: result.Text,
	})
	return result, nil
}

func (a *Service) StartAIChat(requestID string, messagesJSON string) error {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("缺少请求 ID")
	}
	payload, err := decodeAIChatRequestPayload(messagesJSON)
	if err != nil {
		return err
	}
	if len(payload.Messages) == 0 {
		return fmt.Errorf("消息内容为空")
	}
	payload.ToolScope = normalizeAIToolScope(payload.ToolScope)
	payload.ToolScopeSlot = normalizeAIThemeToolSlot(payload.ToolScopeSlot)
	if payload.SystemPromptOverride == "" && payload.ToolScope == aiThemeToolScope {
		payload.SystemPromptOverride = buildAIThemeToolSystemPrompt(payload)
	}

	profile, err := a.getAIProviderProfileForConversation(payload.ConversationID)
	if err != nil {
		return err
	}
	switch profile.Provider {
	case "Compatible", "Responses", "Messages":
	default:
		return fmt.Errorf("当前阶段暂不支持该供应商协议: %s", profile.Provider)
	}
	if strings.TrimSpace(profile.BaseURL) == "" {
		return fmt.Errorf("当前供应商缺少基础 URL")
	}
	if strings.TrimSpace(profile.Model) == "" {
		return fmt.Errorf("当前供应商缺少模型")
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.setAIChatRequestCancel(requestID, cancel)
	a.setAIChatSkipNextAutomaticRequest(requestID, payload.SkipNextAutomaticRequest)
	go a.runCompatibleAIChat(ctx, requestID, payload, profile)
	return nil
}

func (a *Service) CancelAIChat(requestID string) {
	trimmedRequestID := strings.TrimSpace(requestID)
	cancel := a.popAIChatRequestCancel(trimmedRequestID)
	if cancel != nil {
		cancel()
	}
	if execution := a.popAIChatToolExecution(trimmedRequestID); execution != nil {
		execution.markTerminated()
		if execution.Cancel != nil {
			execution.Cancel()
		}
	}
	collaborationState := a.popAIChatCollaborationState(trimmedRequestID)
	if collaborationState != nil {
		collaborationState.markFinished()
		if collaborationState.Cancel != nil {
			collaborationState.Cancel()
		}
	}
	// 待批准/待追问批次一并清理；它们存在与否不再决定是否回事件。
	a.popAIChatPendingToolBatch(trimmedRequestID)
	a.popAIChatPendingFollowupBatch(trimmedRequestID)
	// 无条件回终态事件：前端以 activeRequestId 匹配面板，只要面板还挂着这个 requestId
	// 就一定能复位回 idle。原先仅在「存在待批准/待追问/协同态」时才发 cancelled，
	// 留下两个死锁口子：
	//  1) 纯流式取消（无 pending 批次）只能指望请求循环自己补发 cancelled；
	//  2) 后端已自行收尾（finishAIChatRequest，如恢复流程切全量摘要）却漏发终态事件时，
	//     面板会永久停在「生成中」，停止按钮再点也不会有任何反应。
	a.emitAIChatRuntimePhase(trimmedRequestID, "ready")
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "cancelled",
		"requestId": trimmedRequestID,
	})
}

func (a *Service) ApproveAIChatTools(requestID string) error {
	trimmedRequestID := strings.TrimSpace(requestID)
	pendingBatch := a.popAIChatPendingToolBatch(trimmedRequestID)
	if pendingBatch == nil {
		return fmt.Errorf("没有待批准的工具调用")
	}
	pendingBatch.SuppressNextCommandActionSound = true
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "tool_approval_resolved",
		"requestId": trimmedRequestID,
		"approved":  true,
	})
	a.startAIChatToolExecution(trimmedRequestID, pendingBatch)
	return nil
}

func (a *Service) RejectAIChatTools(requestID string) error {
	trimmedRequestID := strings.TrimSpace(requestID)
	pendingBatch := a.popAIChatPendingToolBatch(trimmedRequestID)
	if pendingBatch == nil {
		return fmt.Errorf("没有待批准的工具调用")
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "tool_approval_resolved",
		"requestId": trimmedRequestID,
		"approved":  false,
	})
	if pendingBatch.NextToolIndex < len(pendingBatch.ParsedTools) {
		tool := pendingBatch.ParsedTools[pendingBatch.NextToolIndex]
		message := buildToolPreviewMessage(pendingBatch.AssistantMessageID, tool, pendingBatch.NextToolIndex)
		message["status"] = "已拒绝"
		if tool.Name == "execute_command" {
			message["output"] = "已拒绝执行工具调用"
		} else {
			message["result"] = "已拒绝执行工具调用"
		}
		a.emitAIChatEvent(map[string]interface{}{
			"kind":      "upsert_message",
			"requestId": trimmedRequestID,
			"message":   message,
		})
		execution := &aiToolExecutionState{
			RequestID:          trimmedRequestID,
			AssistantMessageID: pendingBatch.AssistantMessageID,
			ToolIndex:          pendingBatch.NextToolIndex,
			ToolMessageID:      buildToolMessageID(pendingBatch.AssistantMessageID, pendingBatch.NextToolIndex),
			Tool:               tool,
			Batch:              pendingBatch,
		}
		a.emitAIChatToolResultMessage(trimmedRequestID, execution, "已拒绝执行工具调用")
		a.emitAIChatToolExecutionPersistRequested(trimmedRequestID)
		pendingBatch.NextToolIndex++
	}
	a.advanceAIChatToolBatch(trimmedRequestID, pendingBatch)
	return nil
}

func (a *Service) RejectAIChatToolsForQueuedSubmission(requestID string) error {
	trimmedRequestID := strings.TrimSpace(requestID)
	pendingBatch := a.popAIChatPendingToolBatch(trimmedRequestID)
	if pendingBatch == nil {
		return fmt.Errorf("没有待批准的工具调用")
	}
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "tool_approval_resolved",
		"requestId": trimmedRequestID,
		"approved":  false,
	})
	if pendingBatch.NextToolIndex < len(pendingBatch.ParsedTools) {
		tool := pendingBatch.ParsedTools[pendingBatch.NextToolIndex]
		message := buildToolPreviewMessage(pendingBatch.AssistantMessageID, tool, pendingBatch.NextToolIndex)
		message["status"] = "已拒绝"
		if tool.Name == "execute_command" {
			message["output"] = "已拒绝执行工具调用"
		} else {
			message["result"] = "已拒绝执行工具调用"
		}
		a.emitAIChatEvent(map[string]interface{}{
			"kind":      "upsert_message",
			"requestId": trimmedRequestID,
			"message":   message,
		})
		execution := &aiToolExecutionState{
			RequestID:          trimmedRequestID,
			AssistantMessageID: pendingBatch.AssistantMessageID,
			ToolIndex:          pendingBatch.NextToolIndex,
			ToolMessageID:      buildToolMessageID(pendingBatch.AssistantMessageID, pendingBatch.NextToolIndex),
			Tool:               tool,
			Batch:              pendingBatch,
		}
		a.emitAIChatToolResultMessage(trimmedRequestID, execution, "已拒绝执行工具调用")
		a.emitAIChatToolExecutionPersistRequested(trimmedRequestID)
	}
	a.emitAIChatRuntimePhase(trimmedRequestID, "ready")
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "tool_rejected",
		"requestId": trimmedRequestID,
		"text":      "",
	})
	a.finishAIChatRequest(trimmedRequestID)
	return nil
}

func convertToolArguments(tool aiParsedToolUse, sessionID string) map[string]any {
	arguments := make(map[string]any, len(tool.Params)+1)
	for key, value := range tool.Params {
		switch key {
		case "remaining_file_edits", "start_line", "end_line", "is_mutating":
			if parsedValue, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				arguments[key] = parsedValue
			} else {
				arguments[key] = strings.TrimSpace(value)
			}
		case "operations", "items":
			trimmedValue := strings.TrimSpace(value)
			if trimmedValue == "" {
				arguments[key] = []map[string]any{}
				break
			}
			var parsedValue any
			if err := json.Unmarshal([]byte(trimmedValue), &parsedValue); err == nil {
				arguments[key] = parsedValue
			} else {
				arguments[key] = trimmedValue
			}
		case "recursive", "wait":
			lowerValue := strings.ToLower(strings.TrimSpace(value))
			if lowerValue == "true" || lowerValue == "false" {
				arguments[key] = lowerValue == "true"
			} else {
				arguments[key] = strings.TrimSpace(value)
			}
		default:
			arguments[key] = value
		}
	}
	if _, ok := arguments["session_id"]; !ok && strings.TrimSpace(sessionID) != "" && tool.Name != "list_connected_sessions" {
		arguments["session_id"] = strings.TrimSpace(sessionID)
	}
	return arguments
}

func summarizeParsedToolUse(tool aiParsedToolUse) string {
	if strings.TrimSpace(tool.Name) == aiThemeToolName {
		if action := strings.TrimSpace(tool.Params["action"]); action != "" {
			return action
		}
	}
	if strings.TrimSpace(tool.Name) == "read_file" {
		startLine, _ := strconv.Atoi(strings.TrimSpace(tool.Params["start_line"]))
		endLine, _ := strconv.Atoi(strings.TrimSpace(tool.Params["end_line"]))
		if value := formatAIReadFilePathLabel(tool.Params["path"], startLine, endLine); strings.TrimSpace(value) != "" {
			return value
		}
	}
	for _, key := range []string{"path", "file_path", "command", "query", "purpose", "result"} {
		if value := strings.TrimSpace(tool.Params[key]); value != "" {
			return value
		}
	}
	return tool.Name
}

func titleForParsedToolUse(tool aiParsedToolUse) string {
	switch tool.Name {
	case "read_file":
		return "读取文件"
	case "write_to_file":
		return "写入文件"
	case "transfer_batch":
		return "批量传输"
	case "transfer_list":
		return "列出传输任务"
	case "apply_diff":
		return "应用差异"
	case "execute_command":
		return "执行命令"
	case aiThemeToolName:
		return "主题调色"
	case "ask_followup_question":
		return "追问"
	case "attempt_completion":
		return "任务完成"
	case "list_files":
		return "列出文件"
	case "list_connected_sessions":
		return "列出已连接会话"
	case "live_search":
		return "联网搜索"
	default:
		return tool.Name
	}
}

func formatToolResultContent(result any) string {
	if !isAISafeToolResultValue(result) {
		return formatAIPlainToolResultContent(result)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return formatAIPlainToolResultContent(result)
	}
	return sanitizeAIToolResultText(string(data))
}

func buildToolMessageID(turnID string, index int) string {
	return fmt.Sprintf("%s-tool-%d", turnID, index)
}

func buildAINextRequestMessagesWithAssistant(requestMessages []AIChatRequestMessage, roundResult aiChatRoundResult) []AIChatRequestMessage {
	if len(roundResult.NextRequestMessages) > 0 {
		return append([]AIChatRequestMessage{}, roundResult.NextRequestMessages...)
	}
	nextMessages := append([]AIChatRequestMessage{}, requestMessages...)
	nextMessages = append(nextMessages, AIChatRequestMessage{
		Role:    "assistant",
		Content: roundResult.Text,
	})
	return nextMessages
}

func extractAILatestAssistantCacheObjects(messages []AIChatRequestMessage) *AIConversationProviderCacheObjects {
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.ToLower(strings.TrimSpace(messages[index].Role)) != "assistant" {
			continue
		}
		return cloneAIConversationProviderCacheObjects(messages[index].CacheObjects)
	}
	return nil
}

// extractAILatestAssistantContentBlocks 取出最近一条 assistant 的有序内容块。
// 紧凑回放依赖它作为权威文本源, 必须与 cacheObjects.replayState 一起往返到前端。
func extractAILatestAssistantContentBlocks(messages []AIChatRequestMessage) []map[string]any {
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.ToLower(strings.TrimSpace(messages[index].Role)) != "assistant" {
			continue
		}
		return cloneAIConversationOpenAIResponsesOutputItems(messages[index].ContentBlocks)
	}
	return nil
}

func getAILatestComparableAssistantMessageContent(messages []AIChatRequestMessage) (string, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.ToLower(strings.TrimSpace(messages[index].Role)) != "assistant" {
			continue
		}
		for nextIndex := index + 1; nextIndex < len(messages); nextIndex++ {
			if !strings.EqualFold(strings.TrimSpace(messages[nextIndex].Role), "user") {
				continue
			}
			if isAIExplicitUserPromptMessage(messages[nextIndex]) {
				return "", false
			}
		}
		content := strings.TrimSpace(sanitizeAIAssistantToolProtocolText(messages[index].Content))
		if content == "" {
			return "", false
		}
		return content, true
	}
	return "", false
}

func isAIEmptyAttemptCompletionResult(tool aiParsedToolUse) bool {
	if strings.TrimSpace(tool.Name) != "attempt_completion" {
		return false
	}
	return sanitizeAIToolResultText(strings.TrimSpace(tool.Params["result"])) == ""
}

func buildAIForcedCollaborationFlags(requestMessages []AIChatRequestMessage, roundText string, parsedTools []aiParsedToolUse) (bool, string) {
	if len(parsedTools) == 0 {
		return false, ""
	}
	if len(parsedTools) == 1 && isAIEmptyAttemptCompletionResult(parsedTools[0]) {
		return true, "attempt_completion_empty_result"
	}
	currentAssistantContent := strings.TrimSpace(sanitizeAIAssistantToolProtocolText(roundText))
	if currentAssistantContent == "" {
		return false, ""
	}
	previousAssistantContent, comparable := getAILatestComparableAssistantMessageContent(requestMessages)
	if !comparable {
		return false, ""
	}
	if currentAssistantContent != previousAssistantContent {
		return false, ""
	}
	return true, "assistant_same_output_twice"
}

func (a *Service) requestAIProviderChatRound(ctx context.Context, requestID string, payload AIChatRequestPayload, profile AIProviderProfile, requestMessages []AIChatRequestMessage) (aiChatRoundResult, error) {
	if shouldUseAIAssistantFirstReply(payload, requestMessages) {
		return a.requestAIAssistantFirstReplyRound(ctx, requestID, payload, requestMessages)
	}
	switch profile.Provider {
	case "Responses":
		return a.requestResponsesAIChatRound(ctx, requestID, payload, profile, requestMessages)
	case "Messages":
		return a.requestMessagesAIChatRound(ctx, requestID, payload, profile, requestMessages)
	default:
		return a.requestCompatibleAIChatRound(ctx, requestID, payload, profile, requestMessages)
	}
}

func (a *Service) runCompatibleAIChatLoop(ctx context.Context, requestID string, payload AIChatRequestPayload, profile AIProviderProfile, requestMessages []AIChatRequestMessage, autoApprovalSettings AIConversationTaskSettings, assistantMessageID string, assistantRetryCount int, collaborationRetryCount int) {
	// Drop historical protocol-retry noise before talking to the model.
	requestMessages = purgeAIProtocolRetryNoiseFromMessages(normalizeAIChatRequestMessages(requestMessages))
	consecutiveNoToolCount := 0
	for round := 0; round < 6; round++ {
		var roundResult aiChatRoundResult
		var err error

		if payload.ConversationID != "" {
			globalSettings := a.GetAIGlobalSettings()
			if globalSettings.ConversationAutoBackupEnabled {
				if _, err := a.CreateAIConversationAutoBackup(payload.ConversationID); err != nil {
					slog.Warn("自动备份创建失败", "conversationID", payload.ConversationID, "error", err)
				}
			}
		}

		compensationResult := applyAIUserNodeCompensation(requestMessages)
		requestMessages = compensationResult.Messages
		if compensationResult.Applied {
			a.persistAIUserNodeCompensation(requestID, payload.ConversationID, requestMessages)
		}
		for attempt := 1; attempt <= aiChatRequestMaxAttempts; attempt++ {
			roundStartedAt := time.Now()
			roundResult, err = a.requestAIProviderChatRound(ctx, requestID, payload, profile, requestMessages)
			aiDebugLogRoundResult(payload.ConversationID, requestID, round, attempt, profile, time.Since(roundStartedAt), roundResult, err)
			if err == nil {
				break
			}

			if ctx.Err() != nil {
				a.emitAIChatRuntimePhase(requestID, "ready")
				a.emitAIChatEvent(map[string]interface{}{
					"kind":      "cancelled",
					"requestId": requestID,
				})
				a.finishAIChatRequest(requestID)
				return
			}

			if attempt < aiChatRequestMaxAttempts {
				a.emitAIChatEvent(map[string]interface{}{
					"kind":        "assistant_retry_reset",
					"requestId":   requestID,
					"messageId":   assistantMessageID,
					"attempt":     attempt + 1,
					"maxAttempts": aiChatRequestMaxAttempts,
				})
				continue
			}

			if !hasAIChatRoundProducedOutput(roundResult) {
				if a.recoverAIChatAfterRequestFailure(ctx, requestID, payload, profile, autoApprovalSettings, assistantMessageID, assistantRetryCount, collaborationRetryCount, err) {
					return
				}
			}
			a.emitAIChatRuntimePhase(requestID, "ready")
			a.emitAIChatEvent(map[string]interface{}{
				"kind":      "error",
				"requestId": requestID,
				"error":     err.Error(),
			})
			a.finishAIChatRequest(requestID)
			return
		}

		// 供应商轮在"流正常结束但没有正文"时会回填兜底文案(aiChatEmptyResponseText),
		// 它不是模型回复。必须在工具协议包装之前归一化为空:否则该文案会被包成
		// attempt_completion 之类的"完成任务"结果,既把它伪装成了正常回复,
		// 也让下方的空响应重试(只认空字符串)永远无法触发。
		if strings.TrimSpace(roundResult.Text) == aiChatEmptyResponseText {
			roundResult.Text = ""
		}

		isAssistantFirstReplyRound := shouldUseAIAssistantFirstReply(payload, requestMessages)

		// Always sanitize known tool-protocol slips; pure-text→attempt_completion wrap stays first-round only.
		// 注意:清洗结果为空时同样要生效 —— 整段都是协议垃圾(例如泄漏的 DSML 工具块)必须把正文清空,
		// 否则垃圾会被包成 attempt_completion 之类的假答复,并让下方的空响应重试失效。
		if sanitized := sanitizeAIAssistantToolProtocolText(roundResult.Text); sanitized != strings.TrimSpace(roundResult.Text) {
			roundResult.Text = sanitized
			if len(roundResult.NextRequestMessages) > 0 {
				roundResult.NextRequestMessages = replaceAILatestAssistantMessageContent(roundResult.NextRequestMessages, sanitized)
			}
		}
		if round == 0 && !isAssistantFirstReplyRound {
			roundResult = normalizeAIAssistantRoundResultForToolProtocol(roundResult)
		}

		trimmedText := strings.TrimSpace(roundResult.Text)
		if trimmedText == "" {
			assistantRetryCount++
			consecutiveNoToolCount = 0
			if assistantRetryCount > aiAssistantRetryMaxAttempts {
				a.emitAIChatRuntimePhase(requestID, "ready")
				a.emitAIChatEvent(map[string]interface{}{
					"kind":      "error",
					"requestId": requestID,
					"error":     "AI 连续返回空响应",
				})
				a.finishAIChatRequest(requestID)
				return
			}
			requestMessages = append(requestMessages,
				AIChatRequestMessage{
					Role:    "assistant",
					Content: "",
				},
				AIChatRequestMessage{
					Role:    "user",
					Content: "[ERROR] 你的上一次响应为空，请重新生成完整响应。",
				},
			)
			a.emitAIChatEvent(map[string]interface{}{
				"kind":        "assistant_retry_reset",
				"requestId":   requestID,
				"messageId":   assistantMessageID,
				"attempt":     assistantRetryCount + 1,
				"maxAttempts": aiAssistantRetryMaxAttempts + 1,
			})
			continue
		}

		parsedTools, duplicateToolCount, parseErr := parseAssistantToolUsesWithDuplicateCount(roundResult.Text)
		if parseErr == nil {
			parseErr = validateAIParsedToolScope(payload.ToolScope, parsedTools)
		}
		if parseErr != nil {
			visibleText := strings.TrimSpace(roundResult.Text)

			if isAssistantFirstReplyRound {
				nextRequestMessages := buildAINextRequestMessagesWithAssistant(requestMessages, roundResult)
				assistantCacheObjects := extractAILatestAssistantCacheObjects(nextRequestMessages)
				assistantContentBlocks := extractAILatestAssistantContentBlocks(nextRequestMessages)
				a.emitAIChatEvent(map[string]interface{}{
					"kind":      "api_message_append",
					"requestId": requestID,
					"message": map[string]interface{}{
						// 与前端 done 事件同格式: 同一轮内多次 append 走 upsert 覆盖而非追加。
						"messageId":     fmt.Sprintf("api-%s", assistantMessageID),
						"turnId":        assistantMessageID,
						"role":          "assistant",
						"content":       roundResult.Text,
						"contentBlocks": assistantContentBlocks,
						"cacheObjects":  assistantCacheObjects,
						"ts":            time.Now().UnixMilli(),
					},
				})
				a.emitAIChatEvent(map[string]interface{}{
					"kind":            "assistant_replace",
					"requestId":       requestID,
					"text":            visibleText,
					"streaming":       false,
					"firstTokenMs":    roundResult.FirstTokenMs,
					"elapsedMs":       roundResult.ElapsedMs,
					"inputTokens":     roundResult.InputTokens,
					"outputTokens":    roundResult.OutputTokens,
					"cacheReadTokens": roundResult.CacheReadTokens,
					"tokensPerSecond": roundResult.TokensPerSecond,
				})
				requestMessages = nextRequestMessages
			}

			consecutiveNoToolCount++
			protocolRetryPrompt := buildInvalidToolProtocolRetryMessage(parseErr.Error())
			if consecutiveNoToolCount == 1 {
				nextRequestMessages := buildAINextRequestMessagesWithAssistant(requestMessages, roundResult)
				assistantCacheObjects := extractAILatestAssistantCacheObjects(nextRequestMessages)
				assistantContentBlocks := extractAILatestAssistantContentBlocks(nextRequestMessages)
				a.emitAIChatEvent(map[string]interface{}{
					"kind":      "api_message_append",
					"requestId": requestID,
					"message": map[string]interface{}{
						// 同上: 预设回复轮次会先命中 isAssistantFirstReplyRound 分支, 此处必须复用同一 ID。
						"messageId":     fmt.Sprintf("api-%s", assistantMessageID),
						"turnId":        assistantMessageID,
						"role":          "assistant",
						"content":       roundResult.Text,
						"contentBlocks": assistantContentBlocks,
						"cacheObjects":  assistantCacheObjects,
						"ts":            time.Now().UnixMilli(),
					},
				})
				a.emitAIChatEvent(map[string]interface{}{
					"kind":            "assistant_replace",
					"requestId":       requestID,
					"text":            visibleText,
					"streaming":       false,
					"firstTokenMs":    roundResult.FirstTokenMs,
					"elapsedMs":       roundResult.ElapsedMs,
					"inputTokens":     roundResult.InputTokens,
					"outputTokens":    roundResult.OutputTokens,
					"cacheReadTokens": roundResult.CacheReadTokens,
					"tokensPerSecond": roundResult.TokensPerSecond,
				})
				if a.consumeAIChatSkipNextAutomaticRequest(requestID) {
					a.skipCompatibleAIChatAfterResolvedTools(requestID)
					return
				}
				requestMessages = append(nextRequestMessages,
					AIChatRequestMessage{
						Role:    "user",
						Content: protocolRetryPrompt,
					},
				)
				a.emitAIChatEvent(map[string]interface{}{
					"kind":      "api_message_append",
					"requestId": requestID,
					"message": map[string]interface{}{
						"messageId": fmt.Sprintf("api-user-protocol-retry-%d", time.Now().UnixNano()),
						"role":      "user",
						"content":   protocolRetryPrompt,
						"ts":        time.Now().UnixMilli(),
					},
				})
				nextAssistantMessageID := fmt.Sprintf("%s-cont-%d", requestID, time.Now().UnixNano())
				a.emitAIChatEvent(map[string]interface{}{
					"kind":      "assistant_continue",
					"requestId": requestID,
					"messageId": nextAssistantMessageID,
				})
				assistantMessageID = nextAssistantMessageID
				continue
			}
			errorText := "AI 回复未满足工具协议要求"
			nextRequestMessages := buildAINextRequestMessagesWithAssistant(requestMessages, roundResult)
			assistantCacheObjects := extractAILatestAssistantCacheObjects(nextRequestMessages)
			assistantContentBlocks := extractAILatestAssistantContentBlocks(nextRequestMessages)
			a.emitAIChatEvent(map[string]interface{}{
				"kind":      "api_message_append",
				"requestId": requestID,
				"message": map[string]interface{}{
					// 与前端 done 事件同格式, 避免同一轮被 upsert 当作两条不同记录。
					"messageId":     fmt.Sprintf("api-%s", assistantMessageID),
					"turnId":        assistantMessageID,
					"role":          "assistant",
					"content":       roundResult.Text,
					"contentBlocks": assistantContentBlocks,
					"cacheObjects":  assistantCacheObjects,
					"ts":            time.Now().UnixMilli(),
				},
			})
			a.emitAIChatEvent(map[string]interface{}{
				"kind":            "assistant_replace",
				"requestId":       requestID,
				"text":            visibleText,
				"streaming":       false,
				"firstTokenMs":    roundResult.FirstTokenMs,
				"elapsedMs":       roundResult.ElapsedMs,
				"inputTokens":     roundResult.InputTokens,
				"outputTokens":    roundResult.OutputTokens,
				"cacheReadTokens": roundResult.CacheReadTokens,
				"tokensPerSecond": roundResult.TokensPerSecond,
				"extra": map[string]interface{}{
					"errorText": errorText,
				},
			})
			if a.consumeAIChatSkipNextAutomaticRequest(requestID) {
				a.skipCompatibleAIChatAfterResolvedTools(requestID)
				return
			}
			if round < 5 {
				requestMessages = append(nextRequestMessages,
					AIChatRequestMessage{
						Role:    "user",
						Content: protocolRetryPrompt,
					},
				)
				a.emitAIChatEvent(map[string]interface{}{
					"kind":      "api_message_append",
					"requestId": requestID,
					"message": map[string]interface{}{
						"messageId": fmt.Sprintf("api-user-protocol-retry-%d", time.Now().UnixNano()),
						"role":      "user",
						"content":   protocolRetryPrompt,
						"ts":        time.Now().UnixMilli(),
					},
				})
				nextAssistantMessageID := fmt.Sprintf("%s-cont-%d", requestID, time.Now().UnixNano())
				a.emitAIChatEvent(map[string]interface{}{
					"kind":      "assistant_continue",
					"requestId": requestID,
					"messageId": nextAssistantMessageID,
				})
				assistantMessageID = nextAssistantMessageID
				continue
			}
			a.emitAIChatRuntimePhase(requestID, "ready")
			a.emitAIChatEvent(map[string]interface{}{
				"kind":      "error",
				"requestId": requestID,
				"error":     "AI 回复未满足工具协议，重试后仍未满足协议要求",
			})
			a.finishAIChatRequest(requestID)
			return
		}

		consecutiveNoToolCount = 0

		visibleText := stripAssistantToolXML(roundResult.Text)
		assistantCacheObjects := extractAILatestAssistantCacheObjects(roundResult.NextRequestMessages)
		assistantContentBlocks := extractAILatestAssistantContentBlocks(roundResult.NextRequestMessages)
		a.emitAIChatEvent(map[string]interface{}{
			"kind":      "api_message_append",
			"requestId": requestID,
			"message": map[string]interface{}{
				// 与前端 done 事件同格式, 避免同一轮被 upsert 当作两条不同记录。
				"messageId":     fmt.Sprintf("api-%s", assistantMessageID),
				"turnId":        assistantMessageID,
				"role":          "assistant",
				"content":       roundResult.Text,
				"contentBlocks": assistantContentBlocks,
				"cacheObjects":  assistantCacheObjects,
				"ts":            time.Now().UnixMilli(),
			},
		})
		a.emitAIChatEvent(map[string]interface{}{
			"kind":            "assistant_replace",
			"requestId":       requestID,
			"text":            visibleText,
			"streaming":       false,
			"firstTokenMs":    roundResult.FirstTokenMs,
			"elapsedMs":       roundResult.ElapsedMs,
			"inputTokens":     roundResult.InputTokens,
			"outputTokens":    roundResult.OutputTokens,
			"cacheReadTokens": roundResult.CacheReadTokens,
			"tokensPerSecond": roundResult.TokensPerSecond,
		})

		forceCollaboration, forceCollaborationReason := buildAIForcedCollaborationFlags(requestMessages, roundResult.Text, parsedTools)

		// 混合批次只剔除独占工具的执行资格, 普通工具照原顺序继续执行。
		// 这里必须在 assistant 原文与 api_message_append 落库之后进行, 避免改写请求前缀破坏提示缓存。
		executableTools, skippedStandaloneToolNames := filterAIStandaloneOnlyBatchTools(parsedTools)

		nextRequestMessages := roundResult.NextRequestMessages
		if len(nextRequestMessages) == 0 {
			nextRequestMessages = append([]AIChatRequestMessage{}, requestMessages...)
			nextRequestMessages = append(nextRequestMessages, AIChatRequestMessage{
				Role:    "assistant",
				Content: roundResult.Text,
			})
		}
		requestMessages = nextRequestMessages

		batch := &aiPendingToolBatch{
			RequestID:                  requestID,
			AssistantMessageID:         assistantMessageID,
			Payload:                    payload,
			Profile:                    profile,
			RequestMessages:            requestMessages,
			ParsedTools:                executableTools,
			NextToolIndex:              0,
			DuplicateToolCount:         duplicateToolCount,
			AssistantRetryCount:        assistantRetryCount,
			CollaborationRetryCount:    collaborationRetryCount,
			AutoApprovalSettings:       autoApprovalSettings,
			ForceCollaboration:         forceCollaboration,
			ForceCollaborationReason:   forceCollaborationReason,
			SkippedStandaloneToolNames: skippedStandaloneToolNames,
		}
		a.advanceAIChatToolBatch(requestID, batch)
		return
	}

	a.emitAIChatRuntimePhase(requestID, "ready")
	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "error",
		"requestId": requestID,
		"error":     "工具调用轮次过多，已中止本次对话",
	})
	a.finishAIChatRequest(requestID)
}

func (a *Service) runCompatibleAIChat(ctx context.Context, requestID string, payload AIChatRequestPayload, profile AIProviderProfile) {
	requestMessages := normalizeAIChatRequestMessages(payload.Messages)
	if len(requestMessages) == 0 {
		a.emitAIChatEvent(map[string]interface{}{
			"kind":      "error",
			"requestId": requestID,
			"error":     "消息内容为空",
		})
		a.finishAIChatRequest(requestID)
		return
	}

	// 主动式上下文压缩：在首个请求发出前检查压力，超阈值时静默压缩历史并改用压缩后的消息。
	if condensedMessages, condensed := a.maybeAutoCondenseAIChatContext(ctx, requestID, payload, profile); condensed {
		payload.Messages = condensedMessages
		requestMessages = condensedMessages
	}

	autoApprovalSettings := a.getAIAutoApprovalSettingsForConversation(payload.ConversationID)

	a.emitAIChatEvent(map[string]interface{}{
		"kind":      "start",
		"requestId": requestID,
		"provider":  profile.Provider,
		"model":     profile.Model,
	})
	a.emitAIChatRuntimePhase(requestID, "api_request")

	a.runCompatibleAIChatLoop(ctx, requestID, payload, profile, requestMessages, autoApprovalSettings, requestID, 0, 0)
}
