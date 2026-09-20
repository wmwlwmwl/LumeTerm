package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"lumeterm/internal/mcpserver"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SessionProviderDelegate interface {
	ListConnectedSessions() ([]mcpserver.SessionDescriptor, error)
	GetWorkspaceState() string
}

type SSHDelegate interface {
	ExecuteCommandInTerminalControlled(sessionID string, command string, purpose string, isMutating bool, cwd string, shellType string, timeout time.Duration, control <-chan aiToolExecutionAction, reassign <-chan string, onCommandQueued func(), onCommandStarted func(), onCommandOutput func(string)) (mcpserver.CommandExecutionResult, aiToolExecutionAction, error)
	ListSiblingTerminalCandidates(sessionID string) ([]AIChatCommandTerminalCandidate, error)
	ListDirContext(ctx context.Context, sessionID string, remotePath string) ([]map[string]interface{}, error)
	ReadFileContext(ctx context.Context, sessionID string, remotePath string) (string, error)
	WriteFileContext(ctx context.Context, sessionID string, remotePath string, content string) error
	DeleteItemContext(ctx context.Context, sessionID string, remotePath string, isDir bool) error
	MkdirContext(ctx context.Context, sessionID string, remotePath string) error
	TransferFileContext(ctx context.Context, sessionID string, request mcpserver.TransferFileRequest) (mcpserver.TransferTaskSnapshot, error)
	ListTransfersContext(ctx context.Context, sessionID string) ([]mcpserver.TransferTaskSnapshot, error)
	BridgeGetClientEntry(sessionID string) (*ssh.Client, *sftp.Client, error)
	BridgeExecuteCmdWithClientContext(ctx context.Context, client *ssh.Client, command string) (string, error)
	BridgeGetSFTPClient(sessionID string) (*sftp.Client, error)
}

type Service struct {
	ctx                       context.Context
	sshManager                *sshBridge
	configManager             *configBridge
	sessionProvider           SessionProviderDelegate
	themeToolDelegate         ThemeToolDelegate
	aiChatReqMu               sync.Mutex
	aiChatReqCancel           map[string]context.CancelFunc
	aiPendingToolMu           sync.Mutex
	aiPendingToolBatches      map[string]*PendingToolBatch
	aiPendingFollowupMu       sync.Mutex
	aiPendingFollowupBatches  map[string]*PendingToolBatch
	aiCollabMu                sync.Mutex
	aiCollaborations          map[string]*aiCollaborationState
	aiToolExecMu              sync.Mutex
	aiToolExecutions          map[string]*ToolExecutionState
	aiSkipNextAutoReqMu       sync.Mutex
	aiSkipNextAutomaticReqMap map[string]bool
}

type configBridge struct {
	configDir string
	mu        sync.RWMutex
}

type sshBridge struct {
	delegate SSHDelegate
}

func NewService(ctx context.Context, configDir string, sessionProvider SessionProviderDelegate, sshDelegate SSHDelegate) *Service {
	// AI 对话完整日志:落盘 <configDir>/ai.log;是否写入由设置 aiDebugLogEnabled 控制(默认开启)。
	initAIDebugLog(configDir)
	service := &Service{
		ctx:                       ctx,
		sshManager:                &sshBridge{delegate: sshDelegate},
		configManager:             &configBridge{configDir: configDir},
		sessionProvider:           sessionProvider,
		aiChatReqCancel:           make(map[string]context.CancelFunc),
		aiPendingToolBatches:      make(map[string]*PendingToolBatch),
		aiPendingFollowupBatches:  make(map[string]*PendingToolBatch),
		aiCollaborations:          make(map[string]*aiCollaborationState),
		aiToolExecutions:          make(map[string]*ToolExecutionState),
		aiSkipNextAutomaticReqMap: make(map[string]bool),
	}
	setAIDebugLogEnabled(service.GetAIGlobalSettings().AIDebugLogEnabled)
	return service
}

func (a *Service) SetContext(ctx context.Context) {
	if a == nil {
		return
	}
	a.ctx = ctx
}

func (a *Service) SetThemeToolDelegate(delegate ThemeToolDelegate) {
	if a == nil {
		return
	}
	a.themeToolDelegate = delegate
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmpFile := path + ".tmp"
	file, err := os.OpenFile(tmpFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("open temp file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpFile, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

func ensureContextActive(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func writeStringChunksWithContext(ctx context.Context, file *sftp.File, content string) error {
	const chunkSize = 32768
	for offset := 0; offset < len(content); {
		if err := ensureContextActive(ctx); err != nil {
			return err
		}
		end := offset + chunkSize
		if end > len(content) {
			end = len(content)
		}
		written, err := file.Write([]byte(content[offset:end]))
		if err != nil {
			return err
		}
		offset += written
	}
	return ensureContextActive(ctx)
}

func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

const aiLongTextSegmentThreshold = 10000
const aiLongTextWrapperExtension = ".long_text_wrap"

var aiLongTextSplitPattern = regexp.MustCompile(`((?:\r?\n){2,})`)

func newRuntimeToken() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return hex.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return hex.EncodeToString(buffer)
}

func normalizeAILocalMentionPath(filePath string) string {
	return "/" + strings.ReplaceAll(filepath.ToSlash(filePath), " ", "\\ ")
}

func normalizeAILongTextWrapperLocalPath(value string) string {
	normalized := strings.TrimSpace(value)
	normalized = strings.Trim(normalized, `"'`)
	normalized = strings.TrimPrefix(normalized, "@")
	normalized = strings.ReplaceAll(normalized, "\\ ", " ")
	if len(normalized) >= 4 && normalized[0] == '/' && ((normalized[1] >= 'A' && normalized[1] <= 'Z') || (normalized[1] >= 'a' && normalized[1] <= 'z')) && normalized[2] == ':' && normalized[3] == '/' {
		normalized = normalized[1:]
	}
	return filepath.Clean(filepath.FromSlash(normalized))
}

func splitAILongTextSegmentsPreserveSeparators(text string) []string {
	matches := aiLongTextSplitPattern.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return []string{text}
	}
	parts := make([]string, 0, len(matches)*2+1)
	lastIndex := 0
	for _, match := range matches {
		parts = append(parts, text[lastIndex:match[0]])
		parts = append(parts, text[match[0]:match[1]])
		lastIndex = match[1]
	}
	parts = append(parts, text[lastIndex:])
	return parts
}

func shouldExternalizeAILongTextSegment(segment string) bool {
	trimmed := strings.TrimSpace(segment)
	return utf8.RuneCountInString(trimmed) >= aiLongTextSegmentThreshold && !strings.HasPrefix(trimmed, "@/")
}

func isAIPathWithinBase(basePath string, targetPath string) bool {
	relativePath, err := filepath.Rel(basePath, targetPath)
	if err != nil {
		return false
	}
	return relativePath == "." || (relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)))
}

func (c *configBridge) aiConversationTempDir(conversationID string) string {
	return filepath.Join(c.configDir, "tasks", conversationID, "temp")
}

func (c *configBridge) PreprocessAIConversationLongText(conversationID string, text string) (string, error) {
	if c == nil || strings.TrimSpace(c.configDir) == "" || strings.TrimSpace(text) == "" {
		return text, nil
	}
	trimmedConversationID := strings.TrimSpace(conversationID)
	if trimmedConversationID == "" {
		return text, fmt.Errorf("缺少对话 ID")
	}
	parts := splitAILongTextSegmentsPreserveSeparators(text)
	externalizeFlags := make([]bool, len(parts))
	hasLargeTextSegment := false
	for index, part := range parts {
		if index%2 == 0 && shouldExternalizeAILongTextSegment(part) {
			externalizeFlags[index] = true
			hasLargeTextSegment = true
		}
	}
	if !hasLargeTextSegment {
		return text, nil
	}
	tempDir := c.aiConversationTempDir(trimmedConversationID)
	if err := os.MkdirAll(tempDir, 0700); err != nil {
		return text, err
	}
	rewritten := make([]string, len(parts))
	for index, part := range parts {
		if !externalizeFlags[index] {
			rewritten[index] = part
			continue
		}
		filePath := filepath.Join(tempDir, newRuntimeToken()+aiLongTextWrapperExtension)
		if err := os.WriteFile(filePath, []byte(part), 0600); err != nil {
			return text, err
		}
		rewritten[index] = "@" + normalizeAILocalMentionPath(filePath)
	}
	return strings.Join(rewritten, ""), nil
}

func (c *configBridge) ReadAIConversationWrappedFile(conversationID string, localPath string) (string, error) {
	if c == nil || strings.TrimSpace(c.configDir) == "" {
		return "", fmt.Errorf("配置管理器不可用")
	}
	trimmedConversationID := strings.TrimSpace(conversationID)
	if trimmedConversationID == "" {
		return "", fmt.Errorf("缺少对话 ID")
	}
	tempDir := filepath.Clean(c.aiConversationTempDir(trimmedConversationID))
	normalizedPath := normalizeAILongTextWrapperLocalPath(localPath)
	if normalizedPath == "" {
		return "", fmt.Errorf("长文本包装文件路径不能为空")
	}
	if !strings.EqualFold(filepath.Ext(normalizedPath), aiLongTextWrapperExtension) {
		return "", fmt.Errorf("不支持的长文本包装文件扩展名")
	}
	if !filepath.IsAbs(normalizedPath) {
		normalizedPath = filepath.Join(tempDir, normalizedPath)
	}
	normalizedPath = filepath.Clean(normalizedPath)
	if !isAIPathWithinBase(tempDir, normalizedPath) {
		return "", fmt.Errorf("长文本包装文件路径超出对话临时目录")
	}
	data, err := os.ReadFile(normalizedPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (a *Service) PreprocessAIConversationLongText(conversationID string, text string) (string, error) {
	if a == nil || a.configManager == nil {
		return "", fmt.Errorf("配置管理器不可用")
	}
	return a.configManager.PreprocessAIConversationLongText(conversationID, text)
}

func (a *Service) ReadAIConversationWrappedFile(conversationID string, localPath string) (string, error) {
	if a == nil || a.configManager == nil {
		return "", fmt.Errorf("配置管理器不可用")
	}
	return a.configManager.ReadAIConversationWrappedFile(conversationID, localPath)
}

func (m *sshBridge) ExecuteCommandInTerminalControlled(sessionID string, command string, purpose string, isMutating bool, cwd string, shellType string, timeout time.Duration, control <-chan aiToolExecutionAction, reassign <-chan string, onCommandQueued func(), onCommandStarted func(), onCommandOutput func(string)) (mcpserver.CommandExecutionResult, aiToolExecutionAction, error) {
	if m == nil || m.delegate == nil {
		result := mcpserver.CommandExecutionResult{
			SessionID:  sessionID,
			Command:    command,
			Purpose:    purpose,
			IsMutating: isMutating,
			CWD:        cwd,
			ShellType:  shellType,
		}
		return result, aiToolExecutionActionNone, fmt.Errorf("SSH 管理器桥接不可用")
	}
	return m.delegate.ExecuteCommandInTerminalControlled(sessionID, command, purpose, isMutating, cwd, shellType, timeout, control, reassign, onCommandQueued, onCommandStarted, onCommandOutput)
}

func (m *sshBridge) ListSiblingTerminalCandidates(sessionID string) ([]AIChatCommandTerminalCandidate, error) {
	if m == nil || m.delegate == nil {
		return nil, fmt.Errorf("SSH 管理器桥接不可用")
	}
	return m.delegate.ListSiblingTerminalCandidates(sessionID)
}

func (m *sshBridge) ListDirContext(ctx context.Context, sessionID string, remotePath string) ([]map[string]interface{}, error) {
	if m == nil || m.delegate == nil {
		return nil, fmt.Errorf("SSH 管理器桥接不可用")
	}
	return m.delegate.ListDirContext(ctx, sessionID, remotePath)
}

func (m *sshBridge) ReadFileContext(ctx context.Context, sessionID string, remotePath string) (string, error) {
	if m == nil || m.delegate == nil {
		return "", fmt.Errorf("SSH 管理器桥接不可用")
	}
	return m.delegate.ReadFileContext(ctx, sessionID, remotePath)
}

func (m *sshBridge) WriteFileContext(ctx context.Context, sessionID string, remotePath string, content string) error {
	if m == nil || m.delegate == nil {
		return fmt.Errorf("SSH 管理器桥接不可用")
	}
	return m.delegate.WriteFileContext(ctx, sessionID, remotePath, content)
}

func (m *sshBridge) DeleteItemContext(ctx context.Context, sessionID string, remotePath string, isDir bool) error {
	if m == nil || m.delegate == nil {
		return fmt.Errorf("SSH 管理器桥接不可用")
	}
	return m.delegate.DeleteItemContext(ctx, sessionID, remotePath, isDir)
}

func (m *sshBridge) MkdirContext(ctx context.Context, sessionID string, remotePath string) error {
	if m == nil || m.delegate == nil {
		return fmt.Errorf("SSH 管理器桥接不可用")
	}
	return m.delegate.MkdirContext(ctx, sessionID, remotePath)
}

func (m *sshBridge) getClientEntry(sessionID string) (*ssh.Client, *sftp.Client, error) {
	if m == nil || m.delegate == nil {
		return nil, nil, fmt.Errorf("SSH 管理器桥接不可用")
	}
	return m.delegate.BridgeGetClientEntry(sessionID)
}

func (m *sshBridge) executeCmdWithClientContext(ctx context.Context, client *ssh.Client, command string) (string, error) {
	if m == nil || m.delegate == nil {
		return "", fmt.Errorf("SSH 管理器桥接不可用")
	}
	return m.delegate.BridgeExecuteCmdWithClientContext(ctx, client, command)
}

func (m *sshBridge) getSFTPClient(sessionID string) (*sftp.Client, error) {
	if m == nil || m.delegate == nil {
		return nil, fmt.Errorf("SSH 管理器桥接不可用")
	}
	return m.delegate.BridgeGetSFTPClient(sessionID)
}

type mcpSessionProvider struct {
	app *Service
}

func (p mcpSessionProvider) ListConnectedSessions() ([]mcpserver.SessionDescriptor, error) {
	if p.app == nil || p.app.sessionProvider == nil {
		return []mcpserver.SessionDescriptor{}, nil
	}
	return p.app.sessionProvider.ListConnectedSessions()
}

func (p mcpSessionProvider) GetWorkspaceState() string {
	if p.app == nil || p.app.sessionProvider == nil {
		return ""
	}
	return p.app.sessionProvider.GetWorkspaceState()
}

type mcpCommandProvider struct {
	app *Service
}

func (p mcpCommandProvider) ExecuteCommand(sessionID string, command string, purpose string, isMutating bool, cwd string, shellType string, timeout time.Duration) (mcpserver.CommandExecutionResult, error) {
	return p.ExecuteCommandContext(context.Background(), sessionID, command, purpose, isMutating, cwd, shellType, timeout)
}

func (p mcpCommandProvider) ExecuteCommandContext(ctx context.Context, sessionID string, command string, purpose string, isMutating bool, cwd string, shellType string, timeout time.Duration) (mcpserver.CommandExecutionResult, error) {
	if p.app == nil || p.app.sshManager == nil {
		return mcpserver.CommandExecutionResult{}, fmt.Errorf("SSH 管理器不可用")
	}
	result, _, err := p.app.sshManager.ExecuteCommandInTerminalControlled(sessionID, command, purpose, isMutating, cwd, shellType, timeout, nil, nil, nil, nil, nil)
	return result, err
}

type mcpTransferProvider struct {
	app *Service
}

func (p mcpTransferProvider) TransferFile(sessionID string, request mcpserver.TransferFileRequest) (mcpserver.TransferTaskSnapshot, error) {
	return p.TransferFileContext(context.Background(), sessionID, request)
}

func (p mcpTransferProvider) TransferFileContext(ctx context.Context, sessionID string, request mcpserver.TransferFileRequest) (mcpserver.TransferTaskSnapshot, error) {
	if p.app == nil || p.app.sshManager == nil || p.app.sshManager.delegate == nil {
		return mcpserver.TransferTaskSnapshot{}, fmt.Errorf("SSH 管理器不可用")
	}
	return p.app.sshManager.delegate.TransferFileContext(ctx, sessionID, request)
}

func (p mcpTransferProvider) ListTransfers(sessionID string) ([]mcpserver.TransferTaskSnapshot, error) {
	return p.ListTransfersContext(context.Background(), sessionID)
}

func (p mcpTransferProvider) ListTransfersContext(ctx context.Context, sessionID string) ([]mcpserver.TransferTaskSnapshot, error) {
	if p.app == nil || p.app.sshManager == nil || p.app.sshManager.delegate == nil {
		return nil, fmt.Errorf("SSH 管理器不可用")
	}
	return p.app.sshManager.delegate.ListTransfersContext(ctx, sessionID)
}

type mcpFileProvider struct {
	app *Service
}

func (p mcpFileProvider) ListDirectory(sessionID string, remotePath string) ([]mcpserver.DirectoryEntry, error) {
	return p.ListDirectoryContext(context.Background(), sessionID, remotePath)
}

func (p mcpFileProvider) ListDirectoryContext(ctx context.Context, sessionID string, remotePath string) ([]mcpserver.DirectoryEntry, error) {
	if p.app == nil || p.app.sshManager == nil {
		return nil, fmt.Errorf("SSH 管理器不可用")
	}
	items, err := p.app.sshManager.ListDirContext(ctx, sessionID, remotePath)
	if err != nil {
		return nil, err
	}
	result := make([]mcpserver.DirectoryEntry, 0, len(items))
	for _, item := range items {
		result = append(result, mcpserver.DirectoryEntry{
			Name:        readString(item, "name"),
			IsDirectory: readBool(item, "isDirectory"),
			Size:        readInt64(item, "size"),
			ModifyTime:  readString(item, "modifyTime"),
			Permission:  readString(item, "permission"),
			Mode:        readString(item, "mode"),
			UID:         readString(item, "uid"),
			GID:         readString(item, "gid"),
		})
	}
	return result, nil
}

func (p mcpFileProvider) ReadTextFile(sessionID string, remotePath string) (string, error) {
	return p.ReadTextFileContext(context.Background(), sessionID, remotePath)
}

func (p mcpFileProvider) ReadTextFileContext(ctx context.Context, sessionID string, remotePath string) (string, error) {
	if p.app == nil || p.app.sshManager == nil {
		return "", fmt.Errorf("SSH 管理器不可用")
	}
	return p.app.sshManager.ReadFileContext(ctx, sessionID, remotePath)
}

func (p mcpFileProvider) WriteTextFile(sessionID string, remotePath string, content string) error {
	return p.WriteTextFileContext(context.Background(), sessionID, remotePath, content)
}

func (p mcpFileProvider) WriteTextFileContext(ctx context.Context, sessionID string, remotePath string, content string) error {
	if p.app == nil || p.app.sshManager == nil {
		return fmt.Errorf("SSH 管理器不可用")
	}
	parentDir := pathpkg.Dir(remotePath)
	if parentDir != "" && parentDir != "." && parentDir != "/" {
		if err := p.app.sshManager.MkdirContext(ctx, sessionID, parentDir); err != nil {
			return err
		}
	}
	return p.app.sshManager.WriteFileContext(ctx, sessionID, remotePath, content)
}

func (p mcpFileProvider) DeleteFile(sessionID string, remotePath string) error {
	return p.DeleteFileContext(context.Background(), sessionID, remotePath)
}

func (p mcpFileProvider) DeleteFileContext(ctx context.Context, sessionID string, remotePath string) error {
	if p.app == nil || p.app.sshManager == nil {
		return fmt.Errorf("SSH 管理器不可用")
	}
	return p.app.sshManager.DeleteItemContext(ctx, sessionID, remotePath, false)
}

const remotePatchPythonScript = `#!/usr/bin/env python3
import json
import os
import shutil
import stat
import sys
import tempfile

def ensure_parent(target_path):
    parent = os.path.dirname(target_path)
    if parent:
        os.makedirs(parent, exist_ok=True)

def acquire_lock(target_path):
    ensure_parent(target_path)
    lock_path = target_path + ".lumeterm.lock"
    os.mkdir(lock_path)
    return lock_path

def release_lock(lock_path):
    if not lock_path:
        return
    try:
        os.rmdir(lock_path)
    except OSError:
        shutil.rmtree(lock_path, ignore_errors=True)

def preserve_metadata(target_path):
    if not os.path.exists(target_path):
        return None
    info = os.stat(target_path)
    return {
        "mode": stat.S_IMODE(info.st_mode),
        "uid": info.st_uid,
        "gid": info.st_gid,
    }

def restore_metadata(target_path, metadata):
    if not metadata:
        return
    try:
        os.chmod(target_path, metadata["mode"])
    except OSError:
        pass
    try:
        os.chown(target_path, metadata["uid"], metadata["gid"])
    except OSError:
        pass

def atomic_write(target_path, content):
    ensure_parent(target_path)
    parent = os.path.dirname(target_path) or "."
    metadata = preserve_metadata(target_path)
    temp_path = ""
    fd, temp_path = tempfile.mkstemp(prefix=".lumeterm_patch_", dir=parent, text=True)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        if metadata is not None:
            try:
                os.chmod(temp_path, metadata["mode"])
            except OSError:
                pass
        os.replace(temp_path, target_path)
        restore_metadata(target_path, metadata)
    finally:
        if temp_path and os.path.exists(temp_path):
            os.remove(temp_path)

def read_text(target_path):
    with open(target_path, "r", encoding="utf-8", newline="") as handle:
        return handle.read()

def apply_update_by_expected_content(target_path, expected_content, next_content):
    current_content = read_text(target_path)
    if current_content != expected_content:
        return None, {"reason": "remote content mismatch before atomic write"}
    atomic_write(target_path, next_content)
    return True, None

def apply_delete_by_expected_content(target_path, expected_content):
    current_content = read_text(target_path)
    if current_content != expected_content:
        return None, {"reason": "remote content mismatch before delete"}
    os.remove(target_path)
    return True, None

def apply_update_by_hunks(target_path, hunks):
    content = read_text(target_path)
    for hunk in hunks:
        search = hunk.get("Search") or hunk.get("search") or ""
        replace = hunk.get("Replace") or hunk.get("replace") or ""
        occurrences = content.count(search) if search else 0
        if occurrences != 1:
            return None, occurrences
        content = content.replace(search, replace, 1)
    atomic_write(target_path, content)
    return True, None

def main():
    payload_path = sys.argv[1]
    with open(payload_path, "r", encoding="utf-8") as handle:
        operations = json.load(handle)
    result = {
        "session_id": "",
        "files_changed": 0,
        "applied": False,
        "changes": []
    }
    for operation in operations:
        action = operation.get("Action") or operation.get("action") or ""
        target_path = operation.get("Path") or operation.get("path") or ""
        content = operation.get("Content") or operation.get("content") or ""
        hunks = operation.get("Hunks") or operation.get("hunks") or []
        change = {
            "action": action,
            "path": target_path,
            "hunks": len(hunks),
            "applied": False
        }
        lock_path = ""
        try:
            lock_path = acquire_lock(target_path)
            if action == "add":
                atomic_write(target_path, content)
                change["applied"] = True
                result["files_changed"] += 1
            elif action == "delete":
                if "ExpectedContent" in operation or "expectedContent" in operation or "expected_content" in operation:
                    expected_content = operation.get("ExpectedContent")
                    if expected_content is None:
                        expected_content = operation.get("expectedContent")
                    if expected_content is None:
                        expected_content = operation.get("expected_content")
                    applied, failure = apply_delete_by_expected_content(target_path, expected_content if expected_content is not None else "")
                    if not applied:
                        change["failure"] = failure
                        result["changes"].append(change)
                        result["failure"] = failure
                        print(json.dumps(result, ensure_ascii=False))
                        return 0
                else:
                    os.remove(target_path)
                change["applied"] = True
                result["files_changed"] += 1
            elif action == "update":
                if "ExpectedContent" in operation or "expectedContent" in operation or "expected_content" in operation:
                    expected_content = operation.get("ExpectedContent")
                    if expected_content is None:
                        expected_content = operation.get("expectedContent")
                    if expected_content is None:
                        expected_content = operation.get("expected_content")
                    next_content = operation.get("Content")
                    if next_content is None:
                        next_content = operation.get("content")
                    applied, failure = apply_update_by_expected_content(
                        target_path,
                        expected_content if expected_content is not None else "",
                        next_content if next_content is not None else "",
                    )
                    if not applied:
                        change["failure"] = failure
                        result["changes"].append(change)
                        result["failure"] = failure
                        print(json.dumps(result, ensure_ascii=False))
                        return 0
                else:
                    applied, occurrences = apply_update_by_hunks(target_path, hunks)
                    if not applied:
                        failure = {
                            "reason": "patch hunk matched zero or multiple locations",
                            "occurrences": occurrences
                        }
                        change["failure"] = failure
                        result["changes"].append(change)
                        result["failure"] = failure
                        print(json.dumps(result, ensure_ascii=False))
                        return 0
                change["applied"] = True
                result["files_changed"] += 1
            else:
                failure = {"reason": "unsupported patch action: " + action}
                change["failure"] = failure
                result["changes"].append(change)
                result["failure"] = failure
                print(json.dumps(result, ensure_ascii=False))
                return 0
        except Exception as exc:
            failure = {"reason": str(exc)}
            change["failure"] = failure
            result["changes"].append(change)
            result["failure"] = failure
            print(json.dumps(result, ensure_ascii=False))
            return 0
        finally:
            release_lock(lock_path)
        result["changes"].append(change)
    result["applied"] = True
    print(json.dumps(result, ensure_ascii=False))
    return 0

if __name__ == "__main__":
    sys.exit(main())
`

type mcpRemoteEditExecutor struct {
	app *Service
}

func (e mcpRemoteEditExecutor) GetCapabilities(sessionID string) (mcpserver.RemoteEditCapabilities, error) {
	return e.GetCapabilitiesContext(context.Background(), sessionID)
}

func (e mcpRemoteEditExecutor) GetCapabilitiesContext(ctx context.Context, sessionID string) (mcpserver.RemoteEditCapabilities, error) {
	capabilities := mcpserver.RemoteEditCapabilities{}
	output, err := e.runCommandContext(ctx, sessionID, "sh -lc 'command -v python3 >/dev/null 2>&1 && echo python3=1 || echo python3=0; command -v perl >/dev/null 2>&1 && echo perl=1 || echo perl=0; command -v patch >/dev/null 2>&1 && echo patch=1 || echo patch=0; command -v flock >/dev/null 2>&1 && echo flock=1 || echo flock=0'")
	if err != nil {
		return capabilities, err
	}
	lines := stringsSplit(output)
	for _, line := range lines {
		switch trimSpace(line) {
		case "python3=1":
			capabilities.Python3 = true
		case "perl=1":
			capabilities.Perl = true
		case "patch=1":
			capabilities.Patch = true
		case "flock=1":
			capabilities.Flock = true
		}
	}
	return capabilities, nil
}

func (e mcpRemoteEditExecutor) ApplyPatchAtomic(sessionID string, operations []mcpserver.ApplyPatchFileOperation) (mcpserver.ApplyPatchResult, error) {
	return e.ApplyPatchAtomicContext(context.Background(), sessionID, operations)
}

func (e mcpRemoteEditExecutor) ApplyPatchAtomicContext(ctx context.Context, sessionID string, operations []mcpserver.ApplyPatchFileOperation) (mcpserver.ApplyPatchResult, error) {
	result := mcpserver.ApplyPatchResult{SessionID: sessionID}
	capabilities, err := e.GetCapabilitiesContext(ctx, sessionID)
	if err != nil {
		return result, err
	}
	result.Capabilities = capabilities
	if !capabilities.Python3 {
		return result, mcpserver.ErrRemoteEditUnsupported
	}
	result.Handler = mcpserver.EditHandlerPython3AtomicPatch
	payload, err := json.Marshal(operations)
	if err != nil {
		return result, err
	}
	scriptPath, err := e.uploadTempTextContext(ctx, sessionID, ".py", remotePatchPythonScript, 0700)
	if err != nil {
		return result, err
	}
	defer e.removeTempFile(sessionID, scriptPath)
	payloadPath, err := e.uploadTempTextContext(ctx, sessionID, ".json", string(payload), 0600)
	if err != nil {
		return result, err
	}
	defer e.removeTempFile(sessionID, payloadPath)
	output, err := e.runCommandContext(ctx, sessionID, "python3 "+quotePOSIX(scriptPath)+" "+quotePOSIX(payloadPath)+" 2>&1")
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(trimSpace(output)), &result); err != nil {
		return result, fmt.Errorf("invalid remote patch result: %w", err)
	}
	if result.SessionID == "" {
		result.SessionID = sessionID
	}
	result.Handler = mcpserver.EditHandlerPython3AtomicPatch
	result.Capabilities = capabilities
	return result, nil
}

func (e mcpRemoteEditExecutor) runCommandContext(ctx context.Context, sessionID string, command string) (string, error) {
	if e.app == nil || e.app.sshManager == nil {
		return "", fmt.Errorf("SSH 管理器不可用")
	}
	client, _, err := e.app.sshManager.getClientEntry(sessionID)
	if err != nil {
		return "", err
	}
	return e.app.sshManager.executeCmdWithClientContext(ctx, client, command)
}

func (e mcpRemoteEditExecutor) uploadTempTextContext(ctx context.Context, sessionID string, suffix string, content string, mode os.FileMode) (string, error) {
	if e.app == nil || e.app.sshManager == nil {
		return "", fmt.Errorf("SSH 管理器不可用")
	}
	sftpClient, err := e.app.sshManager.getSFTPClient(sessionID)
	if err != nil {
		return "", err
	}
	path := "/tmp/lumeterm_mcp_" + newRuntimeToken() + suffix
	file, err := sftpClient.Create(path)
	if err != nil {
		return "", err
	}
	if err := writeStringChunksWithContext(ctx, file, content); err != nil {
		file.Close()
		_ = sftpClient.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = sftpClient.Remove(path)
		return "", err
	}
	if err := ensureContextActive(ctx); err != nil {
		_ = sftpClient.Remove(path)
		return "", err
	}
	if err := sftpClient.Chmod(path, mode); err != nil {
		_ = sftpClient.Remove(path)
		return "", err
	}
	return path, nil
}

func (e mcpRemoteEditExecutor) removeTempFile(sessionID string, path string) {
	if e.app == nil || e.app.sshManager == nil || trimSpace(path) == "" {
		return
	}
	sftpClient, err := e.app.sshManager.getSFTPClient(sessionID)
	if err != nil {
		return
	}
	_ = sftpClient.Remove(path)
}

func readString(item map[string]interface{}, key string) string {
	value, ok := item[key]
	if !ok || value == nil {
		return ""
	}
	result, ok := value.(string)
	if ok {
		return result
	}
	return ""
}

func readBool(item map[string]interface{}, key string) bool {
	value, ok := item[key]
	if !ok || value == nil {
		return false
	}
	result, ok := value.(bool)
	if ok {
		return result
	}
	return false
}

func readInt64(item map[string]interface{}, key string) int64 {
	value, ok := item[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	default:
		return 0
	}
}

func trimSpace(value string) string {
	return strings.TrimSpace(value)
}

func stringsSplit(value string) []string {
	return strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
}
