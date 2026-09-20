package ai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"lumeterm/internal/mcpserver"
)

type aiToolRestoreFileSnapshot struct {
	Path           string `json:"path"`
	BeforeContent  string `json:"beforeContent"`
	AppliedContent string `json:"appliedContent"`
	ExistedBefore  bool   `json:"existedBefore"`
	ExistsAfter    bool   `json:"existsAfter"`
}

type aiToolDiffPreviewBlock struct {
	Index       int                    `json:"index"`
	Label       string                 `json:"label"`
	LabelParams map[string]interface{} `json:"labelParams,omitempty"`
	Before      string                 `json:"before"`
	After       string                 `json:"after"`
}

type aiToolDiffPreviewState struct {
	Title      string                 `json:"title"`
	Path       string                 `json:"path"`
	PathParams map[string]interface{} `json:"pathParams,omitempty"`
	ToolName   string                 `json:"toolName"`
	Summary    string                 `json:"summary"`
	RawDiff    string                 `json:"rawDiff"`
	Blocks     []aiToolDiffPreviewBlock `json:"blocks"`
}

type aiToolRestoreState struct {
	Version                int                         `json:"version"`
	ReviewID               string                      `json:"reviewId"`
	RequestID              string                      `json:"requestId,omitempty"`
	ConversationID         string                      `json:"conversationId"`
	SessionID              string                      `json:"sessionId"`
	ToolName               string                      `json:"toolName"`
	Summary                string                      `json:"summary"`
	ArtifactPath           string                      `json:"artifactPath,omitempty"`
	PatchPath              string                      `json:"patchPath,omitempty"`
	CreatedAt              int64                       `json:"createdAt"`
	Files                  []aiToolRestoreFileSnapshot `json:"files"`
	ConversationDiffPreview *aiToolDiffPreviewState     `json:"conversationDiffPreview,omitempty"`
}

func isAIRestoreSupportedTool(tool aiParsedToolUse) bool {
	switch strings.TrimSpace(tool.Name) {
	case "apply_diff", "write_to_file", "search_replace", "edit_file", "apply_patch":
		return true
	default:
		return false
	}
}

func isAIRestoreTargetMissing(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	lowerError := strings.ToLower(err.Error())
	return strings.Contains(lowerError, "no such file") || strings.Contains(lowerError, "not exist")
}

func attachAIRestoreArtifactRef(message map[string]interface{}, artifactPath string) map[string]interface{} {
	trimmedArtifactPath := strings.TrimSpace(artifactPath)
	if message == nil || trimmedArtifactPath == "" {
		return message
	}
	existingExtra, _ := message["extra"].(map[string]interface{})
	if existingExtra == nil {
		existingExtra = map[string]interface{}{}
	}
	existingExtra["restoreArtifactPath"] = trimmedArtifactPath
	message["extra"] = existingExtra
	return message
}

func attachAICopyContent(message map[string]interface{}, copyContent string) map[string]interface{} {
	trimmedCopyContent := strings.TrimSpace(copyContent)
	if message == nil || trimmedCopyContent == "" {
		return message
	}
	existingExtra, _ := message["extra"].(map[string]interface{})
	if existingExtra == nil {
		existingExtra = map[string]interface{}{}
	}
	existingExtra["copyContent"] = trimmedCopyContent
	message["extra"] = existingExtra
	return message
}

func attachAIConversationDiffMeta(message map[string]interface{}, primaryPath string, fileCount int, toolName string, hasPreview bool) map[string]interface{} {
	if message == nil {
		return message
	}
	existingExtra, _ := message["extra"].(map[string]interface{})
	if existingExtra == nil {
		existingExtra = map[string]interface{}{}
	}
	if trimmedPrimaryPath := strings.TrimSpace(primaryPath); trimmedPrimaryPath != "" {
		existingExtra["conversationDiffPrimaryPath"] = trimmedPrimaryPath
	}
	if fileCount > 0 {
		existingExtra["conversationDiffFileCount"] = fileCount
	}
	if trimmedToolName := strings.TrimSpace(toolName); trimmedToolName != "" {
		existingExtra["conversationDiffToolName"] = trimmedToolName
	}
	existingExtra["conversationDiffHasPreview"] = hasPreview
	message["extra"] = existingExtra
	return message
}

func (a *Service) readAIRestoreTargetState(ctx context.Context, sessionID string, remotePath string) (string, bool, error) {
	if a == nil || a.sshManager == nil {
		return "", false, fmt.Errorf("SSH 管理器不可用")
	}
	sftpClient, err := a.sshManager.getSFTPClient(sessionID)
	if err != nil {
		return "", false, err
	}
	if _, err := sftpClient.Stat(remotePath); err != nil {
		if isAIRestoreTargetMissing(err) {
			return "", false, nil
		}
		return "", false, err
	}
	content, err := a.sshManager.ReadFileContext(ctx, sessionID, remotePath)
	if err != nil {
		return "", false, err
	}
	return content, true, nil
}

func buildAIRestoreReviewPathLabel(files []aiToolRestoreFileSnapshot) string {
	if len(files) == 0 {
		return ""
	}
	if len(files) == 1 {
		return files[0].Path
	}
	return "{count} 个文件"
}

func buildAIRestoreReviewPathParams(files []aiToolRestoreFileSnapshot) map[string]interface{} {
	if len(files) <= 1 {
		return nil
	}
	return map[string]interface{}{
		"count": len(files),
	}
}

func buildAIConversationDiffPrimaryPath(files []aiToolRestoreFileSnapshot) string {
	for _, file := range files {
		if trimmedPath := strings.TrimSpace(file.Path); trimmedPath != "" {
			return trimmedPath
		}
	}
	return ""
}

func buildAIConversationDiffPreviewState(state *aiToolRestoreState) *aiToolDiffPreviewState {
	if state == nil || len(state.Files) == 0 {
		return nil
	}
	blocks := make([]aiToolDiffPreviewBlock, 0, len(state.Files))
	for index, file := range state.Files {
		label := file.Path
		labelParams := map[string]interface{}(nil)
		if strings.TrimSpace(label) == "" {
			label = "文件 #{count}"
			labelParams = map[string]interface{}{
				"count": index + 1,
			}
		}
		blocks = append(blocks, aiToolDiffPreviewBlock{
			Index:       index,
			Label:       label,
			LabelParams: labelParams,
			Before:      file.BeforeContent,
			After:       file.AppliedContent,
		})
	}
	return &aiToolDiffPreviewState{
		Title:      "当前对话文件变更",
		Path:       buildAIRestoreReviewPathLabel(state.Files),
		PathParams: buildAIRestoreReviewPathParams(state.Files),
		ToolName:   strings.TrimSpace(state.ToolName),
		Summary:    strings.TrimSpace(state.Summary),
		RawDiff:    buildAIRestoreUnifiedPatch(state.Files),
		Blocks:     blocks,
	}
}

func (a *Service) buildAIRestoreReviewPayload(state *aiToolRestoreState) map[string]interface{} {
	blocks := make([]map[string]interface{}, 0, len(state.Files))
	for index, file := range state.Files {
		label := file.Path
		labelParams := map[string]interface{}(nil)
		if strings.TrimSpace(label) == "" {
			label = "文件 #{count}"
			labelParams = map[string]interface{}{
				"count": index + 1,
			}
		}
		block := map[string]interface{}{
			"index":  index,
			"label":  label,
			"before": file.BeforeContent,
			"after":  file.AppliedContent,
		}
		if labelParams != nil {
			block["labelParams"] = labelParams
		}
		blocks = append(blocks, block)
	}
	pathLabel := buildAIRestoreReviewPathLabel(state.Files)
	payload := map[string]interface{}{
		"reviewId":            state.ReviewID,
		"title":               "修改",
		"requestId":           state.RequestID,
		"toolMessageId":       state.ReviewID,
		"sessionId":           state.SessionID,
		"path":                pathLabel,
		"toolName":            state.ToolName,
		"summary":             state.Summary,
		"rawDiff":             "",
		"blocks":              blocks,
		"mode":                "preview_restore",
		"restoreArtifactPath": state.ArtifactPath,
	}
	if pathParams := buildAIRestoreReviewPathParams(state.Files); pathParams != nil {
		payload["pathParams"] = pathParams
	}
	return payload
}

func (a *Service) buildAIConversationDiffPayload(state *aiToolRestoreState) map[string]interface{} {
	preview := state.ConversationDiffPreview
	if preview == nil {
		preview = buildAIConversationDiffPreviewState(state)
	}
	blocks := make([]map[string]interface{}, 0)
	if preview != nil {
		blocks = make([]map[string]interface{}, 0, len(preview.Blocks))
		for _, block := range preview.Blocks {
			nextBlock := map[string]interface{}{
				"index":  block.Index,
				"label":  block.Label,
				"before": block.Before,
				"after":  block.After,
			}
			if len(block.LabelParams) > 0 {
				nextBlock["labelParams"] = block.LabelParams
			}
			blocks = append(blocks, nextBlock)
		}
	}
	payload := map[string]interface{}{
		"reviewId":            state.ReviewID,
		"title":               "当前对话文件变更",
		"requestId":           state.RequestID,
		"toolMessageId":       state.ReviewID,
		"sessionId":           state.SessionID,
		"path":                buildAIRestoreReviewPathLabel(state.Files),
		"toolName":            state.ToolName,
		"summary":             state.Summary,
		"rawDiff":             "",
		"blocks":              blocks,
		"mode":                "conversation_diff",
		"restoreArtifactPath": state.ArtifactPath,
	}
	if preview != nil {
		if strings.TrimSpace(preview.Title) != "" {
			payload["title"] = preview.Title
		}
		if strings.TrimSpace(preview.Path) != "" {
			payload["path"] = preview.Path
		}
		if strings.TrimSpace(preview.ToolName) != "" {
			payload["toolName"] = preview.ToolName
		}
		if strings.TrimSpace(preview.Summary) != "" {
			payload["summary"] = preview.Summary
		}
		payload["rawDiff"] = preview.RawDiff
		if len(preview.PathParams) > 0 {
			payload["pathParams"] = preview.PathParams
		}
	} else if pathParams := buildAIRestoreReviewPathParams(state.Files); pathParams != nil {
		payload["pathParams"] = pathParams
	}
	return payload
}

func (a *Service) verifyAIRestoreState(ctx context.Context, state *aiToolRestoreState) error {
	if state == nil || len(state.Files) == 0 {
		return fmt.Errorf("当前状态不支持还原")
	}
	for _, file := range state.Files {
		currentContent, exists, err := a.readAIRestoreTargetState(ctx, state.SessionID, file.Path)
		if err != nil {
			return err
		}
		if exists != file.ExistsAfter {
			return fmt.Errorf("当前状态不支持还原")
		}
		if exists && currentContent != file.AppliedContent {
			return fmt.Errorf("当前状态不支持还原")
		}
	}
	return nil
}

func (a *Service) verifyAIReapplyState(ctx context.Context, state *aiToolRestoreState) error {
	if state == nil || len(state.Files) == 0 {
		return fmt.Errorf("当前状态不支持重新应用")
	}
	for _, file := range state.Files {
		currentContent, exists, err := a.readAIRestoreTargetState(ctx, state.SessionID, file.Path)
		if err != nil {
			return err
		}
		if exists != file.ExistedBefore {
			return fmt.Errorf("当前状态不支持重新应用")
		}
		if exists && currentContent != file.BeforeContent {
			return fmt.Errorf("当前状态不支持重新应用")
		}
	}
	return nil
}

func (a *Service) buildApplyDiffRestoreState(tool aiParsedToolUse, payload AIChatRequestPayload, reviewID string) (*aiToolRestoreState, error) {
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("缺少终端会话")
	}
	remotePath := strings.TrimSpace(tool.Params["path"])
	if remotePath == "" {
		return nil, fmt.Errorf("缺少路径")
	}
	diffPayload := tool.Params["diff"]
	if strings.TrimSpace(diffPayload) == "" {
		return nil, fmt.Errorf("缺少 diff 内容")
	}
	fileProvider := mcpFileProvider{app: a}
	originalContent, err := fileProvider.ReadTextFileContext(context.Background(), sessionID, remotePath)
	if err != nil {
		return nil, err
	}
	preview, err := mcpserver.BuildApplyDiffPreview(remotePath, originalContent, diffPayload)
	if err != nil {
		return nil, err
	}
	if preview.Failure != nil && !preview.CanApply {
		return nil, errors.New(formatChangeReviewFailure(preview.Failure, preview.FailureBlockStartLine))
	}
	return &aiToolRestoreState{
		Version:   1,
		ReviewID:  reviewID,
		SessionID: sessionID,
		ToolName:  strings.TrimSpace(tool.Name),
		Summary:   summarizeParsedToolUse(tool),
		CreatedAt: time.Now().UnixMilli(),
		Files: []aiToolRestoreFileSnapshot{
			{
				Path:           remotePath,
				BeforeContent:  preview.OriginalContent,
				AppliedContent: preview.PreviewContent,
				ExistedBefore:  true,
				ExistsAfter:    true,
			},
		},
	}, nil
}

func (a *Service) buildWriteToFileRestoreState(tool aiParsedToolUse, payload AIChatRequestPayload, reviewID string) (*aiToolRestoreState, error) {
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("缺少终端会话")
	}
	remotePath := strings.TrimSpace(tool.Params["path"])
	if remotePath == "" {
		return nil, fmt.Errorf("缺少路径")
	}
	finalContent := tool.Params["content"]
	originalContent, existedBefore, err := a.readAIRestoreTargetState(context.Background(), sessionID, remotePath)
	if err != nil {
		return nil, err
	}
	return &aiToolRestoreState{
		Version:   1,
		ReviewID:  reviewID,
		SessionID: sessionID,
		ToolName:  strings.TrimSpace(tool.Name),
		Summary:   summarizeParsedToolUse(tool),
		CreatedAt: time.Now().UnixMilli(),
		Files: []aiToolRestoreFileSnapshot{
			{
				Path:           remotePath,
				BeforeContent:  originalContent,
				AppliedContent: finalContent,
				ExistedBefore:  existedBefore,
				ExistsAfter:    true,
			},
		},
	}, nil
}

func (a *Service) buildSearchReplaceRestoreState(tool aiParsedToolUse, payload AIChatRequestPayload, reviewID string) (*aiToolRestoreState, error) {
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("缺少终端会话")
	}
	remotePath := strings.TrimSpace(tool.Params["path"])
	if remotePath == "" {
		return nil, fmt.Errorf("缺少路径")
	}
	operationsRaw := tool.Params["operations"]
	operations, err := parseSearchReplaceOperations(operationsRaw)
	if err != nil {
		return nil, err
	}
	fileProvider := mcpFileProvider{app: a}
	originalContent, err := fileProvider.ReadTextFileContext(context.Background(), sessionID, remotePath)
	if err != nil {
		return nil, err
	}
	preview, err := mcpserver.BuildSearchReplaceReviewPreview(remotePath, originalContent, operations)
	if err != nil {
		return nil, err
	}
	if preview.Failure != nil {
		return nil, errors.New(formatChangeReviewFailure(preview.Failure, 0))
	}
	return &aiToolRestoreState{
		Version:   1,
		ReviewID:  reviewID,
		SessionID: sessionID,
		ToolName:  strings.TrimSpace(tool.Name),
		Summary:   summarizeParsedToolUse(tool),
		CreatedAt: time.Now().UnixMilli(),
		Files: []aiToolRestoreFileSnapshot{
			{
				Path:           remotePath,
				BeforeContent:  preview.OriginalContent,
				AppliedContent: preview.PreviewContent,
				ExistedBefore:  true,
				ExistsAfter:    true,
			},
		},
	}, nil
}

func (a *Service) buildEditFileRestoreState(tool aiParsedToolUse, payload AIChatRequestPayload, reviewID string) (*aiToolRestoreState, error) {
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("缺少终端会话")
	}
	remotePath := strings.TrimSpace(tool.Params["path"])
	if remotePath == "" {
		return nil, fmt.Errorf("缺少路径")
	}
	expectedReplacements := 1
	if rawExpected := strings.TrimSpace(tool.Params["expected_replacements"]); rawExpected != "" {
		parsedExpected, err := strconv.Atoi(rawExpected)
		if err != nil || parsedExpected < 1 {
			return nil, fmt.Errorf("expected_replacements 无效")
		}
		expectedReplacements = parsedExpected
	}
	fileProvider := mcpFileProvider{app: a}
	originalContent, err := fileProvider.ReadTextFileContext(context.Background(), sessionID, remotePath)
	if err != nil {
		return nil, err
	}
	preview, err := mcpserver.BuildEditFileReviewPreview(remotePath, originalContent, tool.Params["old_string"], tool.Params["new_string"], expectedReplacements)
	if err != nil {
		return nil, err
	}
	if preview.Failure != nil {
		return nil, errors.New(formatChangeReviewFailure(preview.Failure, 0))
	}
	return &aiToolRestoreState{
		Version:   1,
		ReviewID:  reviewID,
		SessionID: sessionID,
		ToolName:  strings.TrimSpace(tool.Name),
		Summary:   summarizeParsedToolUse(tool),
		CreatedAt: time.Now().UnixMilli(),
		Files: []aiToolRestoreFileSnapshot{
			{
				Path:           remotePath,
				BeforeContent:  preview.OriginalContent,
				AppliedContent: preview.PreviewContent,
				ExistedBefore:  true,
				ExistsAfter:    true,
			},
		},
	}, nil
}

func (a *Service) buildApplyPatchRestoreState(tool aiParsedToolUse, payload AIChatRequestPayload, reviewID string) (*aiToolRestoreState, error) {
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("缺少终端会话")
	}
	patchPayload := tool.Params["patch"]
	if strings.TrimSpace(patchPayload) == "" {
		return nil, fmt.Errorf("缺少 patch 内容")
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
		return nil, err
	}
	if preview.Failure != nil {
		return nil, errors.New(formatChangeReviewFailure(preview.Failure, 0))
	}
	files := make([]aiToolRestoreFileSnapshot, 0, len(preview.Files))
	for _, file := range preview.Files {
		files = append(files, aiToolRestoreFileSnapshot{
			Path:           file.Path,
			BeforeContent:  file.Before,
			AppliedContent: file.After,
			ExistedBefore:  file.ExistedBefore,
			ExistsAfter:    file.ExistsAfter,
		})
	}
	return &aiToolRestoreState{
		Version:   1,
		ReviewID:  reviewID,
		SessionID: sessionID,
		ToolName:  strings.TrimSpace(tool.Name),
		Summary:   summarizeParsedToolUse(tool),
		CreatedAt: time.Now().UnixMilli(),
		Files:     files,
	}, nil
}

func (a *Service) buildAIChatToolRestoreState(tool aiParsedToolUse, payload AIChatRequestPayload, reviewID string) (*aiToolRestoreState, error) {
	conversationID := strings.TrimSpace(payload.ConversationID)
	if conversationID == "" {
		return nil, fmt.Errorf("缺少对话 ID")
	}
	var state *aiToolRestoreState
	var err error
	switch strings.TrimSpace(tool.Name) {
	case "apply_diff":
		state, err = a.buildApplyDiffRestoreState(tool, payload, reviewID)
	case "write_to_file":
		state, err = a.buildWriteToFileRestoreState(tool, payload, reviewID)
	case "search_replace":
		state, err = a.buildSearchReplaceRestoreState(tool, payload, reviewID)
	case "edit_file":
		state, err = a.buildEditFileRestoreState(tool, payload, reviewID)
	case "apply_patch":
		state, err = a.buildApplyPatchRestoreState(tool, payload, reviewID)
	default:
		return nil, fmt.Errorf("当前状态不支持还原")
	}
	if err != nil {
		return nil, err
	}
	state.ConversationID = conversationID
	if state.ConversationDiffPreview == nil {
		state.ConversationDiffPreview = buildAIConversationDiffPreviewState(state)
	}
	return state, nil
}

type aiToolRestoreArtifactResult struct {
	ArtifactPath               string
	CopyContent                string
	ConversationDiffPrimaryPath string
	ConversationDiffFileCount   int
	ConversationDiffToolName    string
	ConversationDiffHasPreview  bool
}

func (a *Service) persistAIChatToolRestoreArtifact(tool aiParsedToolUse, payload AIChatRequestPayload, reviewID string, requestID string) (aiToolRestoreArtifactResult, error) {
	if a == nil || a.configManager == nil {
		return aiToolRestoreArtifactResult{}, fmt.Errorf("配置管理器不可用")
	}
	state, err := a.buildAIChatToolRestoreState(tool, payload, reviewID)
	if err != nil {
		return aiToolRestoreArtifactResult{}, err
	}
	state.RequestID = strings.TrimSpace(requestID)
	artifactPath, err := a.configManager.WriteAIConversationRestoreArtifact(state)
	if err != nil {
		return aiToolRestoreArtifactResult{}, err
	}
	return aiToolRestoreArtifactResult{
		ArtifactPath:                artifactPath,
		CopyContent:                 buildAICopyContentForTool(tool, state),
		ConversationDiffPrimaryPath: buildAIConversationDiffPrimaryPath(state.Files),
		ConversationDiffFileCount:   len(state.Files),
		ConversationDiffToolName:    strings.TrimSpace(state.ToolName),
		ConversationDiffHasPreview:  state.ConversationDiffPreview != nil && len(state.ConversationDiffPreview.Blocks) > 0,
	}, nil
}

func (a *Service) PreviewAIChatToolRestore(artifactPath string, sessionID string) (map[string]interface{}, error) {
	trimmedArtifactPath := strings.TrimSpace(artifactPath)
	trimmedSessionID := strings.TrimSpace(sessionID)
	if a == nil || a.configManager == nil || trimmedArtifactPath == "" {
		return nil, fmt.Errorf("当前状态不支持还原")
	}
	state, err := a.configManager.ReadAIConversationRestoreArtifact(trimmedArtifactPath)
	if err != nil || state == nil {
		return nil, fmt.Errorf("当前状态不支持还原")
	}
	if trimmedSessionID != "" && strings.TrimSpace(state.SessionID) != "" && trimmedSessionID != strings.TrimSpace(state.SessionID) {
		return nil, fmt.Errorf("当前状态不支持还原")
	}
	if err := a.verifyAIRestoreState(context.Background(), state); err != nil {
		return nil, fmt.Errorf("当前状态不支持还原")
	}
	return a.buildAIRestoreReviewPayload(state), nil
}

func (a *Service) PreviewAIChatToolDiff(artifactPath string) (map[string]interface{}, error) {
	trimmedArtifactPath := strings.TrimSpace(artifactPath)
	if a == nil || a.configManager == nil || trimmedArtifactPath == "" {
		return nil, fmt.Errorf("差异预览失败")
	}
	state, err := a.configManager.ReadAIConversationRestoreArtifact(trimmedArtifactPath)
	if err != nil || state == nil {
		return nil, fmt.Errorf("差异预览失败")
	}
	if state.ConversationDiffPreview == nil {
		state.ConversationDiffPreview = buildAIConversationDiffPreviewState(state)
	}
	return a.buildAIConversationDiffPayload(state), nil
}

func (a *Service) ReapplyAIChatTool(artifactPath string, sessionID string) error {
	trimmedArtifactPath := strings.TrimSpace(artifactPath)
	trimmedSessionID := strings.TrimSpace(sessionID)
	if a == nil || a.configManager == nil || trimmedArtifactPath == "" {
		return fmt.Errorf("当前状态不支持重新应用")
	}
	state, err := a.configManager.ReadAIConversationRestoreArtifact(trimmedArtifactPath)
	if err != nil || state == nil {
		return fmt.Errorf("当前状态不支持重新应用")
	}
	if trimmedSessionID != "" && strings.TrimSpace(state.SessionID) != "" && trimmedSessionID != strings.TrimSpace(state.SessionID) {
		return fmt.Errorf("当前状态不支持重新应用")
	}
	if err := a.verifyAIReapplyState(context.Background(), state); err != nil {
		return fmt.Errorf("当前状态不支持重新应用")
	}
	fileProvider := mcpFileProvider{app: a}
	for _, file := range state.Files {
		if file.ExistsAfter {
			if err := fileProvider.WriteTextFileContext(context.Background(), state.SessionID, file.Path, file.AppliedContent); err != nil {
				return err
			}
			continue
		}
		if err := fileProvider.DeleteFileContext(context.Background(), state.SessionID, file.Path); err != nil && !isAIRestoreTargetMissing(err) {
			return err
		}
	}
	return nil
}

func (a *Service) RestoreAIChatTool(artifactPath string, sessionID string) error {
	trimmedArtifactPath := strings.TrimSpace(artifactPath)
	trimmedSessionID := strings.TrimSpace(sessionID)
	if a == nil || a.configManager == nil || trimmedArtifactPath == "" {
		return fmt.Errorf("当前状态不支持还原")
	}
	state, err := a.configManager.ReadAIConversationRestoreArtifact(trimmedArtifactPath)
	if err != nil || state == nil {
		return fmt.Errorf("当前状态不支持还原")
	}
	if trimmedSessionID != "" && strings.TrimSpace(state.SessionID) != "" && trimmedSessionID != strings.TrimSpace(state.SessionID) {
		return fmt.Errorf("当前状态不支持还原")
	}
	if err := a.verifyAIRestoreState(context.Background(), state); err != nil {
		return fmt.Errorf("当前状态不支持还原")
	}
	fileProvider := mcpFileProvider{app: a}
	for _, file := range state.Files {
		if file.ExistedBefore {
			if err := fileProvider.WriteTextFileContext(context.Background(), state.SessionID, file.Path, file.BeforeContent); err != nil {
				return err
			}
			continue
		}
		if err := fileProvider.DeleteFileContext(context.Background(), state.SessionID, file.Path); err != nil && !isAIRestoreTargetMissing(err) {
			return err
		}
	}
	return nil
}