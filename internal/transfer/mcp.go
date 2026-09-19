package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"luminssh-go/internal/mcpserver"
)

const (
	mcpTransferModeUploadFile         = "upload-file"
	mcpTransferModeUploadCompressed   = "upload-compressed"
	mcpTransferModeDownloadFile       = "download-file"
	mcpTransferModeDownloadCompressed = "download-compressed"
	mcpTransferKindFile               = "file"
	mcpTransferKindDirectory          = "directory"
	mcpTransferStatusRunning          = "running"
	mcpTransferStatusCompleted        = "completed"
	mcpTransferStatusFailed           = "failed"
	mcpTransferHistoryLimit           = 100
)

type mcpTransferStore struct {
	mu      sync.Mutex
	active  map[string]mcpserver.TransferTaskSnapshot
	history []mcpserver.TransferTaskSnapshot
}

func newMCPTransferStore() *mcpTransferStore {
	return &mcpTransferStore{active: map[string]mcpserver.TransferTaskSnapshot{}}
}

func currentMCPTransferTimestamp() int64 {
	return time.Now().UnixMilli()
}

func cloneMCPTransferSnapshot(snapshot mcpserver.TransferTaskSnapshot) mcpserver.TransferTaskSnapshot {
	cloned := snapshot
	if len(snapshot.Items) > 0 {
		cloned.Items = append([]mcpserver.TransferBatchItem(nil), snapshot.Items...)
	}
	return cloned
}

func (s *mcpTransferStore) start(snapshot mcpserver.TransferTaskSnapshot) mcpserver.TransferTaskSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := cloneMCPTransferSnapshot(snapshot)
	s.active[cloned.TransferID] = cloned
	return cloned
}

func (s *mcpTransferStore) update(transferID string, mutate func(*mcpserver.TransferTaskSnapshot)) mcpserver.TransferTaskSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.active[transferID]
	if !ok {
		return mcpserver.TransferTaskSnapshot{}
	}
	mutate(&snapshot)
	snapshot.UpdatedAt = currentMCPTransferTimestamp()
	s.active[transferID] = snapshot
	return cloneMCPTransferSnapshot(snapshot)
}

func (s *mcpTransferStore) finish(transferID string, mutate func(*mcpserver.TransferTaskSnapshot)) mcpserver.TransferTaskSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.active[transferID]
	if !ok {
		return mcpserver.TransferTaskSnapshot{}
	}
	mutate(&snapshot)
	snapshot.Active = false
	snapshot.FinishedAt = currentMCPTransferTimestamp()
	snapshot.UpdatedAt = snapshot.FinishedAt
	delete(s.active, transferID)
	nextHistory := make([]mcpserver.TransferTaskSnapshot, 0, len(s.history)+1)
	nextHistory = append(nextHistory, snapshot)
	for _, item := range s.history {
		if item.TransferID == transferID {
			continue
		}
		nextHistory = append(nextHistory, item)
		if len(nextHistory) >= mcpTransferHistoryLimit {
			break
		}
	}
	s.history = nextHistory
	return cloneMCPTransferSnapshot(snapshot)
}

func (s *mcpTransferStore) list(sessionID string) []mcpserver.TransferTaskSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]mcpserver.TransferTaskSnapshot, 0, len(s.active)+len(s.history))
	for _, item := range s.active {
		if item.SessionID != sessionID {
			continue
		}
		result = append(result, cloneMCPTransferSnapshot(item))
	}
	for _, item := range s.history {
		if item.SessionID != sessionID {
			continue
		}
		result = append(result, cloneMCPTransferSnapshot(item))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Active != result[j].Active {
			return result[i].Active
		}
		if result[i].UpdatedAt != result[j].UpdatedAt {
			return result[i].UpdatedAt > result[j].UpdatedAt
		}
		return result[i].TransferID > result[j].TransferID
	})
	return result
}

func (s *mcpTransferStore) cancelSession(sessionID string) {
	s.mu.Lock()
	active := make([]mcpserver.TransferTaskSnapshot, 0)
	for _, snapshot := range s.active {
		if snapshot.SessionID == sessionID {
			active = append(active, snapshot)
		}
	}
	s.mu.Unlock()
	for _, snapshot := range active {
		s.finish(snapshot.TransferID, func(current *mcpserver.TransferTaskSnapshot) {
			current.Status = mcpTransferStatusFailed
			current.Phase = "cancelled"
			current.Detail = "transfer cancelled"
			current.Error = "session disconnected"
		})
	}
}

func (s *mcpTransferStore) cancelAll() {
	s.mu.Lock()
	active := make([]mcpserver.TransferTaskSnapshot, 0, len(s.active))
	for _, snapshot := range s.active {
		active = append(active, snapshot)
	}
	s.mu.Unlock()
	for _, snapshot := range active {
		s.finish(snapshot.TransferID, func(current *mcpserver.TransferTaskSnapshot) {
			current.Status = mcpTransferStatusFailed
			current.Phase = "cancelled"
			current.Detail = "transfer cancelled"
			current.Error = "transfer service closed"
		})
	}
}

func normalizeMCPTransferOperation(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case mcpserver.TransferOperationUpload:
		return mcpserver.TransferOperationUpload
	case mcpserver.TransferOperationDownload:
		return mcpserver.TransferOperationDownload
	default:
		return ""
	}
}

func resolveMCPTransferLocalPath(parent string, relative string) (string, error) {
	trimmedParent := strings.TrimSpace(parent)
	if trimmedParent == "" {
		return "", fmt.Errorf("local_parent must not be empty")
	}
	if !filepath.IsAbs(trimmedParent) {
		return "", fmt.Errorf("local_parent must be an absolute path")
	}
	cleanParent := filepath.Clean(trimmedParent)
	trimmedRelative := strings.TrimSpace(relative)
	if trimmedRelative == "" {
		return "", fmt.Errorf("local_path must not be empty")
	}
	if filepath.IsAbs(trimmedRelative) || filepath.VolumeName(trimmedRelative) != "" {
		return "", fmt.Errorf("local_path must be a relative path")
	}
	cleanedRelative := filepath.Clean(trimmedRelative)
	fullPath := filepath.Clean(filepath.Join(cleanParent, cleanedRelative))
	relativeToParent, err := filepath.Rel(cleanParent, fullPath)
	if err != nil {
		return "", err
	}
	if relativeToParent == ".." || strings.HasPrefix(relativeToParent, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("local_path escapes local_parent")
	}
	return fullPath, nil
}

func resolveMCPTransferRemotePath(parent string, relative string) (string, error) {
	trimmedParent := strings.TrimSpace(strings.ReplaceAll(parent, "\\", "/"))
	if trimmedParent == "" {
		return "", fmt.Errorf("remote_parent must not be empty")
	}
	cleanParent := pathpkg.Clean(trimmedParent)
	if !pathpkg.IsAbs(cleanParent) {
		return "", fmt.Errorf("remote_parent must be an absolute path")
	}
	trimmedRelative := strings.TrimSpace(strings.ReplaceAll(relative, "\\", "/"))
	if trimmedRelative == "" {
		return "", fmt.Errorf("remote_path must not be empty")
	}
	if pathpkg.IsAbs(trimmedRelative) {
		return "", fmt.Errorf("remote_path must be a relative path")
	}
	cleanedRelative := pathpkg.Clean(trimmedRelative)
	fullPath := pathpkg.Clean(pathpkg.Join(cleanParent, cleanedRelative))
	if cleanParent == "/" {
		if !strings.HasPrefix(fullPath, "/") {
			fullPath = "/" + strings.TrimPrefix(fullPath, "/")
		}
		return fullPath, nil
	}
	if fullPath == cleanParent {
		return fullPath, nil
	}
	parentPrefix := strings.TrimSuffix(cleanParent, "/") + "/"
	if !strings.HasPrefix(fullPath, parentPrefix) {
		return "", fmt.Errorf("remote_path escapes remote_parent")
	}
	return fullPath, nil
}

func detectMCPTransferLocalKind(localPath string) (string, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return mcpTransferKindDirectory, nil
	}
	if info.Mode().IsRegular() {
		return mcpTransferKindFile, nil
	}
	return "", fmt.Errorf("unsupported local path type: %s", localPath)
}

func (s *Service) detectMCPTransferRemoteKind(sessionID string, remotePath string) (string, error) {
	sftpClient, err := s.backend.SFTPClient(sessionID)
	if err != nil {
		return "", err
	}
	info, err := sftpClient.Stat(remotePath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return mcpTransferKindDirectory, nil
	}
	return mcpTransferKindFile, nil
}

func newMCPTransferID() string {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("mcp_transfer_%d", time.Now().UnixNano())
	}
	return "mcp_transfer_" + hex.EncodeToString(bytes)
}

func buildMCPTransferInitialSnapshot(sessionID string, request mcpserver.TransferFileRequest) mcpserver.TransferTaskSnapshot {
	now := currentMCPTransferTimestamp()
	items := append([]mcpserver.TransferBatchItem(nil), request.Items...)
	return mcpserver.TransferTaskSnapshot{
		TransferID:     newMCPTransferID(),
		SessionID:      sessionID,
		Operation:      request.Operation,
		Status:         mcpTransferStatusRunning,
		Phase:          "preparing",
		Progress:       0,
		Wait:           request.Wait,
		Active:         true,
		LocalParent:    request.LocalParent,
		RemoteParent:   request.RemoteParent,
		Items:          items,
		ItemCount:      len(items),
		CompletedItems: 0,
		StartedAt:      now,
		UpdatedAt:      now,
	}
}

func (s *Service) updateMCPTransferProgress(transferID string, phase string, progress float64, bytesDone int64, bytesTotal int64, current string, detail string) mcpserver.TransferTaskSnapshot {
	return s.mcpStore.update(transferID, func(snapshot *mcpserver.TransferTaskSnapshot) {
		snapshot.Phase = phase
		snapshot.Progress = ClampPercent(progress)
		snapshot.BytesDone = bytesDone
		snapshot.BytesTotal = bytesTotal
		snapshot.Current = current
		snapshot.Detail = detail
		snapshot.Status = mcpTransferStatusRunning
		snapshot.Error = ""
	})
}

func (s *Service) completeMCPTransfer(transferID string, phase string, bytesDone int64, bytesTotal int64, current string, detail string) mcpserver.TransferTaskSnapshot {
	return s.mcpStore.finish(transferID, func(snapshot *mcpserver.TransferTaskSnapshot) {
		snapshot.Phase = phase
		snapshot.Progress = 100
		snapshot.BytesDone = bytesDone
		snapshot.BytesTotal = bytesTotal
		snapshot.Current = current
		snapshot.Detail = detail
		snapshot.Status = mcpTransferStatusCompleted
		snapshot.Error = ""
	})
}

func (s *Service) failMCPTransfer(transferID string, phase string, detail string, err error) mcpserver.TransferTaskSnapshot {
	return s.mcpStore.finish(transferID, func(snapshot *mcpserver.TransferTaskSnapshot) {
		snapshot.Phase = phase
		snapshot.Status = mcpTransferStatusFailed
		snapshot.Detail = detail
		if err != nil {
			snapshot.Error = err.Error()
		}
	})
}

func (s *Service) UpdateMCPTransferFromCompressedUploadEvent(sessionID string, uploadID string, phase string, progress float64, bytesDone int64, bytesTotal int64, current string, detail string) {
	trimmedUploadID := strings.TrimSpace(uploadID)
	if trimmedUploadID == "" {
		return
	}
	s.mcpStore.update(trimmedUploadID, func(snapshot *mcpserver.TransferTaskSnapshot) {
		if snapshot.SessionID != strings.TrimSpace(sessionID) {
			return
		}
		snapshot.Phase = strings.TrimSpace(phase)
		snapshot.Progress = ClampPercent(progress)
		snapshot.BytesDone = bytesDone
		snapshot.BytesTotal = bytesTotal
		snapshot.Current = strings.TrimSpace(current)
		snapshot.Detail = strings.TrimSpace(detail)
		snapshot.Status = mcpTransferStatusRunning
		snapshot.Error = ""
	})
}

func (s *Service) UpdateMCPTransferFromDownloadEvent(sessionID string, downloadID string, mode string, phase string, status string, progress float64, bytesDone int64, bytesTotal int64, current string, detail string) {
	trimmedDownloadID := strings.TrimSpace(downloadID)
	if trimmedDownloadID == "" {
		return
	}
	s.mcpStore.update(trimmedDownloadID, func(snapshot *mcpserver.TransferTaskSnapshot) {
		if snapshot.SessionID != strings.TrimSpace(sessionID) {
			return
		}
		if strings.TrimSpace(mode) != "" {
			snapshot.Mode = strings.TrimSpace(mode)
		}
		snapshot.Phase = strings.TrimSpace(phase)
		snapshot.Progress = ClampPercent(progress)
		snapshot.BytesDone = bytesDone
		snapshot.BytesTotal = bytesTotal
		snapshot.Current = strings.TrimSpace(current)
		snapshot.Detail = strings.TrimSpace(detail)
		if strings.TrimSpace(status) != "" {
			snapshot.Status = strings.TrimSpace(status)
		}
		snapshot.Error = ""
	})
}

func buildMCPTransferForceOverwriteOptionsJSON() string {
	return `{"strategy":"force_overwrite"}`
}

func runMCPTransferCopy(ctx context.Context, dst io.Writer, src io.Reader, totalSize int64, onProgress func(int64, int64)) error {
	buf := make([]byte, 2*1024*1024)
	var copied int64
	lastEmit := time.Time{}
	for {
		if err := ensureContextActive(ctx); err != nil {
			return err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			if writeErr != nil {
				return writeErr
			}
			copied += int64(written)
			now := time.Now()
			if onProgress != nil && (now.Sub(lastEmit) > 200*time.Millisecond || copied >= totalSize) {
				onProgress(copied, totalSize)
				lastEmit = now
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if err := ensureContextActive(ctx); err != nil {
		return err
	}
	// 与 copyReaderWithProgressContext 相同的截断守卫:远端流提前结束(io.EOF)
	// 不代表传完了声明的大小,必须比对实际字节数(issue #334)。
	if totalSize > 0 && copied < totalSize {
		return fmt.Errorf("transfer incomplete: received %d of %d bytes (remote returned short reads or early EOF)", copied, totalSize)
	}
	if onProgress != nil {
		onProgress(copied, totalSize)
	}
	return nil
}

func (s *Service) transferUploadFileContext(ctx context.Context, transferID string, sessionID string, localFullPath string, remoteFullPath string) (int64, error) {
	s.updateMCPTransferProgress(transferID, "uploading", 0, 0, 0, filepath.Base(localFullPath), "uploading local file")
	src, err := os.Open(localFullPath)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return 0, err
	}
	remoteParentDir := pathpkg.Dir(remoteFullPath)
	if remoteParentDir != "" && remoteParentDir != "." && remoteParentDir != "/" {
		if err := s.backend.MkdirRemote(ctx, sessionID, remoteParentDir); err != nil {
			return 0, err
		}
	}
	sftpClient, err := s.backend.SFTPClient(sessionID)
	if err != nil {
		return 0, err
	}
	if existingInfo, statErr := sftpClient.Stat(remoteFullPath); statErr == nil && existingInfo != nil && existingInfo.IsDir() {
		if err := s.backend.DeleteRemote(ctx, sessionID, remoteFullPath, true); err != nil {
			return 0, err
		}
	}
	dst, err := sftpClient.Create(remoteFullPath)
	if err != nil {
		return 0, err
	}
	defer dst.Close()
	if err := runMCPTransferCopy(ctx, dst, src, info.Size(), func(done int64, total int64) {
		progress := float64(100)
		if total > 0 {
			progress = float64(done) / float64(total) * 100
		}
		s.updateMCPTransferProgress(transferID, "uploading", progress, done, total, filepath.Base(localFullPath), "uploading local file")
	}); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *Service) transferDownloadFileContext(ctx context.Context, transferID string, sessionID string, remoteFullPath string, localFullPath string) (int64, error) {
	s.updateMCPTransferProgress(transferID, "downloading", 0, 0, 0, pathpkg.Base(remoteFullPath), "downloading remote file")
	sftpClient, err := s.backend.SFTPClient(sessionID)
	if err != nil {
		return 0, err
	}
	src, err := sftpClient.Open(remoteFullPath)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	remoteInfo, err := src.Stat()
	if err != nil {
		return 0, err
	}
	// 本地同名路径若已是目录则直接失败,不做删除:目录可能承载用户数据,
	// 静默递归删除属于不可逆破坏,交由调用方换名或先清理后再重试。
	if existingInfo, statErr := os.Stat(localFullPath); statErr == nil && existingInfo != nil && existingInfo.IsDir() {
		return 0, fmt.Errorf("local path already exists as a directory, refusing to replace it: %s", localFullPath)
	}
	if err := os.MkdirAll(filepath.Dir(localFullPath), 0o755); err != nil {
		return 0, err
	}
	dst, err := os.Create(localFullPath)
	if err != nil {
		return 0, err
	}
	defer dst.Close()
	if err := runMCPTransferCopy(ctx, dst, src, remoteInfo.Size(), func(done int64, total int64) {
		progress := float64(100)
		if total > 0 {
			progress = float64(done) / float64(total) * 100
		}
		s.updateMCPTransferProgress(transferID, "downloading", progress, done, total, pathpkg.Base(remoteFullPath), "downloading remote file")
	}); err != nil {
		return 0, err
	}
	if err := SyncPathTimestamps(localFullPath, remoteInfo.ModTime()); err != nil {
		return 0, err
	}
	return remoteInfo.Size(), nil
}

func (s *Service) transferUploadDirectoryContext(ctx context.Context, transferID string, sessionID string, localFullPath string, remoteFullPath string) error {
	s.updateMCPTransferProgress(transferID, "preparing", 0, 0, 0, filepath.Base(localFullPath), "preparing compressed directory upload")
	targetParentDir := pathpkg.Dir(remoteFullPath)
	if targetParentDir == "." || targetParentDir == "" {
		targetParentDir = "/"
	}
	if err := s.backend.MkdirRemote(ctx, sessionID, targetParentDir); err != nil {
		return err
	}
	sourceBaseName := filepath.Base(filepath.Clean(localFullPath))
	targetBaseName := pathpkg.Base(pathpkg.Clean(remoteFullPath))
	if err := s.UploadLocalPathsCompressed(sessionID, transferID, 1, []string{localFullPath}, targetParentDir); err != nil {
		return err
	}
	uploadedPath := pathpkg.Join(targetParentDir, sourceBaseName)
	if uploadedPath != remoteFullPath && targetBaseName != "" {
		sftpClient, err := s.backend.SFTPClient(sessionID)
		if err != nil {
			return err
		}
		if existingInfo, statErr := sftpClient.Stat(remoteFullPath); statErr == nil && existingInfo != nil {
			if err := s.backend.DeleteRemote(ctx, sessionID, remoteFullPath, existingInfo.IsDir()); err != nil {
				return err
			}
		}
		if err := s.backend.RenameRemote(ctx, sessionID, uploadedPath, remoteFullPath); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) transferDownloadDirectoryContext(ctx context.Context, transferID string, sessionID string, remoteFullPath string, localFullPath string) error {
	s.updateMCPTransferProgress(transferID, "preparing", 0, 0, 0, pathpkg.Base(remoteFullPath), "preparing compressed directory download")
	return s.DownloadDirectoryCompressed(sessionID, transferID, remoteFullPath, localFullPath, buildMCPTransferForceOverwriteOptionsJSON())
}

func buildMCPTransferMode(operation string, detectedKind string) string {
	switch operation {
	case mcpserver.TransferOperationUpload:
		if detectedKind == mcpTransferKindDirectory {
			return mcpTransferModeUploadCompressed
		}
		return mcpTransferModeUploadFile
	case mcpserver.TransferOperationDownload:
		if detectedKind == mcpTransferKindDirectory {
			return mcpTransferModeDownloadCompressed
		}
		return mcpTransferModeDownloadFile
	default:
		return ""
	}
}

func (s *Service) runMCPTransferItemContext(ctx context.Context, snapshot mcpserver.TransferTaskSnapshot, item mcpserver.TransferBatchItem, itemIndex int) error {
	localFullPath, err := resolveMCPTransferLocalPath(snapshot.LocalParent, item.LocalPath)
	if err != nil {
		return err
	}
	remoteFullPath, err := resolveMCPTransferRemotePath(snapshot.RemoteParent, item.RemotePath)
	if err != nil {
		return err
	}
	var detectedKind string
	switch snapshot.Operation {
	case mcpserver.TransferOperationUpload:
		detectedKind, err = detectMCPTransferLocalKind(localFullPath)
	case mcpserver.TransferOperationDownload:
		detectedKind, err = s.detectMCPTransferRemoteKind(snapshot.SessionID, remoteFullPath)
	default:
		err = fmt.Errorf("unsupported transfer operation: %s", snapshot.Operation)
	}
	if err != nil {
		return err
	}
	mode := buildMCPTransferMode(snapshot.Operation, detectedKind)
	if mode == "" {
		return fmt.Errorf("unsupported transfer mode")
	}
	s.mcpStore.update(snapshot.TransferID, func(current *mcpserver.TransferTaskSnapshot) {
		current.Mode = mode
		current.DetectedKind = detectedKind
		current.Current = fmt.Sprintf("%s -> %s", item.LocalPath, item.RemotePath)
		current.Detail = fmt.Sprintf("item %d/%d", itemIndex+1, current.ItemCount)
	})
	switch mode {
	case mcpTransferModeUploadFile:
		_, err = s.transferUploadFileContext(ctx, snapshot.TransferID, snapshot.SessionID, localFullPath, remoteFullPath)
	case mcpTransferModeDownloadFile:
		_, err = s.transferDownloadFileContext(ctx, snapshot.TransferID, snapshot.SessionID, remoteFullPath, localFullPath)
	case mcpTransferModeUploadCompressed:
		err = s.transferUploadDirectoryContext(ctx, snapshot.TransferID, snapshot.SessionID, localFullPath, remoteFullPath)
	case mcpTransferModeDownloadCompressed:
		err = s.transferDownloadDirectoryContext(ctx, snapshot.TransferID, snapshot.SessionID, remoteFullPath, localFullPath)
	default:
		err = fmt.Errorf("unsupported transfer mode: %s", mode)
	}
	if err != nil {
		return err
	}
	s.mcpStore.update(snapshot.TransferID, func(current *mcpserver.TransferTaskSnapshot) {
		current.CompletedItems = itemIndex + 1
		current.Progress = ClampPercent(float64(current.CompletedItems) / float64(current.ItemCount) * 100)
		current.Current = fmt.Sprintf("%s -> %s", item.LocalPath, item.RemotePath)
		current.Detail = fmt.Sprintf("completed item %d/%d", itemIndex+1, current.ItemCount)
	})
	return nil
}

func (s *Service) runMCPTransferContext(ctx context.Context, snapshot mcpserver.TransferTaskSnapshot) (mcpserver.TransferTaskSnapshot, error) {
	for index, item := range snapshot.Items {
		if err := ensureContextActive(ctx); err != nil {
			failed := s.failMCPTransfer(snapshot.TransferID, "failed", "transfer cancelled", err)
			return failed, err
		}
		if err := s.runMCPTransferItemContext(ctx, snapshot, item, index); err != nil {
			failed := s.failMCPTransfer(snapshot.TransferID, "failed", fmt.Sprintf("failed at item %d/%d", index+1, snapshot.ItemCount), err)
			return failed, err
		}
	}
	completed := s.completeMCPTransfer(snapshot.TransferID, "completed", 0, 0, "", fmt.Sprintf("completed %d item(s)", snapshot.ItemCount))
	return completed, nil
}

func (s *Service) TransferFileContext(ctx context.Context, sessionID string, request mcpserver.TransferFileRequest) (mcpserver.TransferTaskSnapshot, error) {
	if s == nil {
		return mcpserver.TransferTaskSnapshot{}, fmt.Errorf("transfer service unavailable")
	}
	if err := ensureContextActive(ctx); err != nil {
		return mcpserver.TransferTaskSnapshot{}, err
	}
	if strings.TrimSpace(sessionID) == "" {
		return mcpserver.TransferTaskSnapshot{}, fmt.Errorf("missing session id")
	}
	normalizedOperation := normalizeMCPTransferOperation(request.Operation)
	if normalizedOperation == "" {
		return mcpserver.TransferTaskSnapshot{}, fmt.Errorf("unsupported transfer operation: %s", request.Operation)
	}
	if len(request.Items) == 0 {
		return mcpserver.TransferTaskSnapshot{}, fmt.Errorf("no transfer items")
	}
	request.SessionID = strings.TrimSpace(sessionID)
	request.Operation = normalizedOperation
	snapshot := buildMCPTransferInitialSnapshot(sessionID, request)
	var taskCtx context.Context
	var cancel context.CancelFunc
	if request.Wait {
		taskCtx, cancel = context.WithCancel(ctx)
	} else {
		taskCtx, cancel = context.WithCancel(context.Background())
	}
	if err := s.registry.register(snapshot.TransferID, snapshot.SessionID, "mcp", cancel); err != nil {
		cancel()
		return mcpserver.TransferTaskSnapshot{}, err
	}
	s.mcpStore.start(snapshot)
	if !request.Wait {
		go func(taskSnapshot mcpserver.TransferTaskSnapshot) {
			defer cancel()
			defer s.registry.finish(taskSnapshot.TransferID)
			_, _ = s.runMCPTransferContext(taskCtx, taskSnapshot)
		}(snapshot)
		return snapshot, nil
	}
	defer cancel()
	defer s.registry.finish(snapshot.TransferID)
	finalSnapshot, err := s.runMCPTransferContext(taskCtx, snapshot)
	if err != nil {
		return finalSnapshot, err
	}
	return finalSnapshot, nil
}

func (s *Service) ListTransfersContext(ctx context.Context, sessionID string) ([]mcpserver.TransferTaskSnapshot, error) {
	if s == nil {
		return nil, fmt.Errorf("transfer service unavailable")
	}
	if err := ensureContextActive(ctx); err != nil {
		return nil, err
	}
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("missing session id")
	}
	return s.mcpStore.list(trimmedSessionID), nil
}
