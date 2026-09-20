package wailsapp

// ponytail: AI 主题调色实现。纯逻辑已迁移至 internal/ai/theme_tuning_impl.go。
// 本文件仅保留 config 类型依赖部分（draft 状态管理、HandleThemeToolRequest 等）。
// 原因：internal/config import internal/ai，反向 import 会循环依赖。

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	ai "lumeterm/internal/ai"
	"lumeterm/internal/config"
)

type aiThemeToolDraftState struct {
	ConversationID    string
	Slot              string
	DraftID           string
	SourceThemeID     string
	ReadyForPreview   bool
	HasPreviewChanges bool
	Draft             config.ThemePackageFile
}

var aiThemeToolDraftStore = struct {
	mu    sync.Mutex
	items map[string]*aiThemeToolDraftState
}{
	items: map[string]*aiThemeToolDraftState{},
}

func cloneAIThemeToolDraftState(state *aiThemeToolDraftState) *aiThemeToolDraftState {
	if state == nil {
		return nil
	}
	return &aiThemeToolDraftState{
		ConversationID:    state.ConversationID,
		Slot:              state.Slot,
		DraftID:           state.DraftID,
		SourceThemeID:     state.SourceThemeID,
		ReadyForPreview:   state.ReadyForPreview,
		HasPreviewChanges: state.HasPreviewChanges,
		Draft: config.ThemePackageFile{
			SchemaVersion: state.Draft.SchemaVersion,
			ID:            state.Draft.ID,
			Name:          state.Draft.Name,
			Description:   state.Draft.Description,
			ModeHint:      state.Draft.ModeHint,
			Tokens:        config.CloneStringMap(state.Draft.Tokens),
			Components:    config.CloneAnyMap(state.Draft.Components),
			Resources:     config.CloneAnyMap(state.Draft.Resources),
		},
	}
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

func themeToolPackageFileFromSummary(item config.ThemePackageSummary) config.ThemePackageFile {
	return config.ThemePackageFile{
		SchemaVersion: item.SchemaVersion,
		ID:            item.ID,
		Name:          item.Name,
		Description:   item.Description,
		ModeHint:      item.ModeHint,
		Tokens:        config.CloneStringMap(item.Tokens),
		Components:    config.CloneAnyMap(item.Components),
		Resources:     config.CloneAnyMap(item.Resources),
	}
}

func themeToolPackageFileToMap(item config.ThemePackageFile) map[string]interface{} {
	return map[string]interface{}{
		"schemaVersion": item.SchemaVersion,
		"id":            item.ID,
		"name":          item.Name,
		"description":   item.Description,
		"modeHint":      item.ModeHint,
		"tokens":        config.CloneStringMap(item.Tokens),
		"components":     config.CloneAnyMap(item.Components),
		"resources":      config.CloneAnyMap(item.Resources),
	}
}

func themeToolCurrentThemeSummary(items []config.ThemePackageSummary, id string) (config.ThemePackageSummary, bool) {
	normalizedID := strings.TrimSpace(id)
	for _, item := range items {
		if strings.TrimSpace(item.ID) == normalizedID {
			return item, true
		}
	}
	return config.ThemePackageSummary{}, false
}

func themeToolLoadDraft(conversationID string) (*aiThemeToolDraftState, bool) {
	normalizedConversationID := strings.TrimSpace(conversationID)
	if normalizedConversationID == "" {
		return nil, false
	}
	aiThemeToolDraftStore.mu.Lock()
	defer aiThemeToolDraftStore.mu.Unlock()
	state, ok := aiThemeToolDraftStore.items[normalizedConversationID]
	if !ok {
		return nil, false
	}
	return cloneAIThemeToolDraftState(state), true
}

func themeToolSaveDraft(state *aiThemeToolDraftState) {
	if state == nil || strings.TrimSpace(state.ConversationID) == "" {
		return
	}
	aiThemeToolDraftStore.mu.Lock()
	defer aiThemeToolDraftStore.mu.Unlock()
	aiThemeToolDraftStore.items[strings.TrimSpace(state.ConversationID)] = cloneAIThemeToolDraftState(state)
}

func themeToolDeleteDraft(conversationID string) {
	normalizedConversationID := strings.TrimSpace(conversationID)
	if normalizedConversationID == "" {
		return
	}
	aiThemeToolDraftStore.mu.Lock()
	defer aiThemeToolDraftStore.mu.Unlock()
	delete(aiThemeToolDraftStore.items, normalizedConversationID)
}

func themeToolMarkDraftReadyForPreview(conversationID string) {
	normalizedConversationID := strings.TrimSpace(conversationID)
	if normalizedConversationID == "" {
		return
	}
	aiThemeToolDraftStore.mu.Lock()
	defer aiThemeToolDraftStore.mu.Unlock()
	state, ok := aiThemeToolDraftStore.items[normalizedConversationID]
	if !ok || state == nil {
		return
	}
	state.ReadyForPreview = true
	aiThemeToolDraftStore.items[normalizedConversationID] = cloneAIThemeToolDraftState(state)
}

func (a *App) MarkThemeToolConversationUserConfirmed(conversationID string) {
	themeToolMarkDraftReadyForPreview(conversationID)
}

func themeToolApplyPatch(current config.ThemePackageFile, patch map[string]interface{}) (config.ThemePackageFile, []string, []string, error) {
	next := config.ThemePackageFile{
		SchemaVersion: config.ThemePackageSchemaVersion,
		ID:            current.ID,
		Name:          current.Name,
		Description:   current.Description,
		ModeHint:      current.ModeHint,
		Tokens:        config.CloneStringMap(current.Tokens),
		Components:    config.CloneAnyMap(current.Components),
		Resources:     config.CloneAnyMap(current.Resources),
	}
	appliedPaths := make([]string, 0)
	warnings := make([]string, 0)

	allowedTopLevelFields := map[string]struct{}{
		"name":        {},
		"description": {},
		"modeHint":    {},
		"tokens":      {},
		"components":  {},
		"resources":   {},
		"patch":       {},
	}

	for key := range patch {
		if _, ok := allowedTopLevelFields[key]; ok {
			continue
		}
		warnings = append(warnings, "ignored field: "+key)
	}

	if name, ok := patch["name"].(string); ok && strings.TrimSpace(name) != "" {
		next.Name = strings.TrimSpace(name)
		appliedPaths = append(appliedPaths, "name")
	}
	if description, ok := patch["description"].(string); ok {
		next.Description = strings.TrimSpace(description)
		appliedPaths = append(appliedPaths, "description")
	}
	if modeHint, ok := patch["modeHint"].(string); ok {
		if normalizedModeHint := normalizeAIThemeToolSlot(modeHint); normalizedModeHint != "" {
			next.ModeHint = normalizedModeHint
			appliedPaths = append(appliedPaths, "modeHint")
		}
	}
	if tokens, ok := patch["tokens"].(map[string]interface{}); ok {
		if next.Tokens == nil {
			next.Tokens = map[string]string{}
		}
		for key, rawValue := range tokens {
			value, ok := rawValue.(string)
			if !ok {
				warnings = append(warnings, "ignored token: "+key)
				continue
			}
			trimmedValue := strings.TrimSpace(value)
			if trimmedValue == "" {
				continue
			}
			next.Tokens[key] = trimmedValue
			appliedPaths = append(appliedPaths, "tokens."+key)
		}
	}
	if components, ok := patch["components"].(map[string]interface{}); ok {
		next.Components = config.MergeAnyMaps(next.Components, components)
		ai.ThemeToolCollectPatchPaths("components", components, &appliedPaths)
	}
	if resources, ok := patch["resources"].(map[string]interface{}); ok {
		next.Resources = config.MergeAnyMaps(next.Resources, resources)
		ai.ThemeToolCollectPatchPaths("resources", resources, &appliedPaths)
	}

	return next, ai.ThemeToolUniqueStrings(appliedPaths), ai.ThemeToolUniqueStrings(warnings), nil
}

func (a *App) themeToolInitializeDraft(conversationID string, slot string) (*aiThemeToolDraftState, error) {
	normalizedConversationID := strings.TrimSpace(conversationID)
	normalizedSlot := normalizeAIThemeToolSlot(slot)
	if normalizedConversationID == "" {
		return nil, fmt.Errorf("missing conversation id")
	}
	if normalizedSlot == "" {
		return nil, fmt.Errorf("slot must be light or dark")
	}
	if existing, ok := themeToolLoadDraft(normalizedConversationID); ok && existing.Slot == normalizedSlot {
		return existing, nil
	}
	items, err := a.configManager.ListThemePackages()
	if err != nil {
		return nil, err
	}
	settings := a.configManager.GetThemePackageSettings()
	sourceThemeID := settings.DarkThemePackageID
	if normalizedSlot == "light" {
		sourceThemeID = settings.LightThemePackageID
	}
	summary, ok := themeToolCurrentThemeSummary(items, sourceThemeID)
	if !ok {
		return nil, fmt.Errorf("theme package not found")
	}
	draftPackage := themeToolPackageFileFromSummary(summary)
	draftPackage.Name = ai.ThemeToolDefaultDraftName(summary.Name)
	draftPackage.ModeHint = normalizedSlot
	state := &aiThemeToolDraftState{
		ConversationID:    normalizedConversationID,
		Slot:              normalizedSlot,
		DraftID:           ai.ThemeToolBuildDraftID(normalizedSlot),
		SourceThemeID:      summary.ID,
		ReadyForPreview:   false,
		HasPreviewChanges: false,
		Draft:             draftPackage,
	}
	themeToolSaveDraft(state)
	return state, nil
}

func (a *App) themeToolCommitDraft(state *aiThemeToolDraftState, request map[string]interface{}) (config.ThemePackageSummary, config.ThemePackageSettings, error) {
	if a == nil || a.configManager == nil || state == nil {
		return config.ThemePackageSummary{}, config.ThemePackageSettings{}, fmt.Errorf("theme package settings unavailable")
	}
	themePackage := config.ThemePackageFile{
		SchemaVersion: config.ThemePackageSchemaVersion,
		ID:            ai.ThemeToolBuildCommittedID(state.Slot),
		Name:          ai.ThemeToolDefaultCommitName(state.Slot, state.Draft.Name),
		Description:   state.Draft.Description,
		ModeHint:      state.Slot,
		Tokens:        config.CloneStringMap(state.Draft.Tokens),
		Components:    config.CloneAnyMap(state.Draft.Components),
		Resources:     config.CloneAnyMap(state.Draft.Resources),
	}
	if name, ok := request["name"].(string); ok && strings.TrimSpace(name) != "" {
		themePackage.Name = strings.TrimSpace(name)
	}
	if description, ok := request["description"].(string); ok {
		themePackage.Description = strings.TrimSpace(description)
	}
	if err := config.ValidateThemePackageFile(&themePackage); err != nil {
		return config.ThemePackageSummary{}, config.ThemePackageSettings{}, err
	}
	userDirectory, err := a.configManager.EnsureUserThemePackagesDirectory()
	if err != nil {
		return config.ThemePackageSummary{}, config.ThemePackageSettings{}, err
	}
	data, err := json.MarshalIndent(themePackage, "", "  ")
	if err != nil {
		return config.ThemePackageSummary{}, config.ThemePackageSettings{}, err
	}
	targetPath := filepath.Join(userDirectory, themePackage.ID+".json")
	if err := config.AtomicWriteFile(targetPath, data, 0o644); err != nil {
		return config.ThemePackageSummary{}, config.ThemePackageSettings{}, err
	}
	summary, err := config.ReadThemePackageSummaryFromFile(targetPath, "user")
	if err != nil {
		return config.ThemePackageSummary{}, config.ThemePackageSettings{}, err
	}
	settings := a.configManager.GetThemePackageSettings()
	if state.Slot == "light" {
		settings.LightThemePackageID = summary.ID
	} else {
		settings.DarkThemePackageID = summary.ID
	}
	if err := a.configManager.SaveThemePackageSettings(settings); err != nil {
		return config.ThemePackageSummary{}, config.ThemePackageSettings{}, err
	}
	return summary, a.configManager.GetThemePackageSettings(), nil
}

func (a *App) HandleThemeToolRequest(ctx context.Context, request ai.ThemeToolRequest) (ai.ThemeToolResult, error) {
	_ = ctx
	if a == nil || a.configManager == nil {
		return ai.ThemeToolResult{}, fmt.Errorf("theme package settings unavailable")
	}
	conversationID := strings.TrimSpace(request.ConversationID)
	if conversationID == "" {
		return ai.ThemeToolResult{}, fmt.Errorf("missing conversation id")
	}
	action := strings.TrimSpace(strings.ToLower(request.Action))
	switch action {
	case "help":
		state, err := a.themeToolInitializeDraft(conversationID, request.Slot)
		if err != nil {
			return ai.ThemeToolResult{}, err
		}
		return ai.ThemeToolResult{
			Action:        "help",
			Status:        "ready",
			DraftID:       state.DraftID,
			Slot:          state.Slot,
			SourceThemeID: state.SourceThemeID,
			Theme:         themeToolPackageFileToMap(state.Draft),
			FieldMap:      ai.ThemeToolFieldMap(),
			Result:        "help ready",
		}, nil
	case "inspect":
		state, ok := themeToolLoadDraft(conversationID)
		if !ok {
			return ai.ThemeToolResult{}, fmt.Errorf("theme tool must call help first")
		}
		return ai.ThemeToolResult{
			Action:        "inspect",
			Status:        "ready",
			DraftID:       state.DraftID,
			Slot:          state.Slot,
			SourceThemeID: state.SourceThemeID,
			Theme:         themeToolPackageFileToMap(state.Draft),
			Result:        "inspect ready",
		}, nil
	case "preview":
		state, ok := themeToolLoadDraft(conversationID)
		if !ok {
			return ai.ThemeToolResult{}, fmt.Errorf("theme tool must call help first")
		}
		if !state.ReadyForPreview {
			return ai.ThemeToolResult{}, fmt.Errorf("preview blocked until the user answers the follow-up question")
		}
		patch, err := ai.ThemeToolParsePatchObject(request.Request)
		if err != nil {
			return ai.ThemeToolResult{}, err
		}
		invalidPaths, suggestedPaths := ai.ThemeToolValidatePreviewPatch(patch)
		if len(invalidPaths) > 0 {
			return ai.ThemeToolResult{
				Action:         "preview",
				Status:         "blocked",
				DraftID:        state.DraftID,
				Slot:           state.Slot,
				SourceThemeID:  state.SourceThemeID,
				Warnings:       ai.ThemeToolBuildPreviewBlockedWarnings(invalidPaths),
				InvalidPaths:   invalidPaths,
				SuggestedPaths: suggestedPaths,
				FieldMap:       ai.ThemeToolFieldMap(),
				Result:         "preview blocked because the patch contains forbidden or unsupported fields",
			}, nil
		}
		nextDraft, appliedPaths, warnings, err := themeToolApplyPatch(state.Draft, patch)
		if err != nil {
			return ai.ThemeToolResult{}, err
		}
		state.Draft = nextDraft
		state.HasPreviewChanges = state.HasPreviewChanges || len(appliedPaths) > 0
		themeToolSaveDraft(state)
		return ai.ThemeToolResult{
			Action:        "preview",
			Status:        "previewed",
			DraftID:       state.DraftID,
			Slot:          state.Slot,
			SourceThemeID: state.SourceThemeID,
			Theme:         themeToolPackageFileToMap(state.Draft),
			AppliedPaths:  appliedPaths,
			Warnings:      warnings,
			Result:        "preview updated",
		}, nil
	case "commit":
		state, ok := themeToolLoadDraft(conversationID)
		if !ok {
			return ai.ThemeToolResult{}, fmt.Errorf("theme tool must call help first")
		}
		if !state.ReadyForPreview {
			return ai.ThemeToolResult{}, fmt.Errorf("commit blocked until the user answers the follow-up question")
		}
		if !state.HasPreviewChanges {
			return ai.ThemeToolResult{}, fmt.Errorf("commit blocked until at least one preview change is applied")
		}
		requestObject, err := ai.ThemeToolParseRequestObject(request.Request)
		if err != nil {
			return ai.ThemeToolResult{}, err
		}
		committedTheme, settings, err := a.themeToolCommitDraft(state, requestObject)
		if err != nil {
			return ai.ThemeToolResult{}, err
		}
		themeToolDeleteDraft(conversationID)
		return ai.ThemeToolResult{
			Action:         "commit",
			Status:         "committed",
			DraftID:        state.DraftID,
			Slot:           state.Slot,
			SourceThemeID:  state.SourceThemeID,
			CommittedTheme: config.ThemePackageSummaryToMap(committedTheme),
			Settings:       config.ThemePackageSettingsToMap(settings),
			Result:         "committed",
		}, nil
	case "revert":
		state, ok := themeToolLoadDraft(conversationID)
		if !ok {
			return ai.ThemeToolResult{}, fmt.Errorf("theme tool must call help first")
		}
		themeToolDeleteDraft(conversationID)
		return ai.ThemeToolResult{
			Action:        "revert",
			Status:        "reverted",
			DraftID:       state.DraftID,
			Slot:          state.Slot,
			SourceThemeID: state.SourceThemeID,
			Result:        "reverted",
		}, nil
	default:
		return ai.ThemeToolResult{}, fmt.Errorf("unsupported theme tool action: %s", action)
	}
}
