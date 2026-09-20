package transfer

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
)

type downloadTransferTask struct {
	id              string
	sessionId       string
	cancel          context.CancelFunc
	mu              sync.Mutex
	localTempDir    string
	localTargetPath string
	remoteArchive   string
}

func (task *downloadTransferTask) setLocalTempDir(localTempDir string) {
	task.mu.Lock()
	task.localTempDir = localTempDir
	task.mu.Unlock()
}

func (task *downloadTransferTask) clearLocalTempDir() {
	task.mu.Lock()
	task.localTempDir = ""
	task.mu.Unlock()
}

func (task *downloadTransferTask) setLocalTargetPath(localTargetPath string) {
	task.mu.Lock()
	task.localTargetPath = localTargetPath
	task.mu.Unlock()
}

func (task *downloadTransferTask) clearLocalTargetPath() {
	task.mu.Lock()
	task.localTargetPath = ""
	task.mu.Unlock()
}

func (task *downloadTransferTask) setRemoteArchive(remoteArchive string) {
	task.mu.Lock()
	task.remoteArchive = remoteArchive
	task.mu.Unlock()
}

func (task *downloadTransferTask) clearRemoteArchive() {
	task.mu.Lock()
	task.remoteArchive = ""
	task.mu.Unlock()
}

func (task *downloadTransferTask) snapshot() (string, string, string) {
	task.mu.Lock()
	defer task.mu.Unlock()
	return task.localTempDir, task.localTargetPath, task.remoteArchive
}

func (task *downloadTransferTask) cleanupLocal() {
	localTempDir, localTargetPath, _ := task.snapshot()
	if localTempDir != "" {
		_ = os.RemoveAll(localTempDir)
		task.clearLocalTempDir()
	}
	if localTargetPath != "" {
		_ = os.RemoveAll(localTargetPath)
		task.clearLocalTargetPath()
	}
}

func (task *downloadTransferTask) cleanup(service *Service) {
	task.cleanupLocal()
	_, _, remoteArchive := task.snapshot()
	if remoteArchive != "" {
		_ = service.backend.DeleteRemote(context.Background(), task.sessionId, remoteArchive, false)
		task.clearRemoteArchive()
	}
}

func (task *downloadTransferTask) detachOnDisconnect() (string, string) {
	task.mu.Lock()
	defer task.mu.Unlock()
	localTempDir := task.localTempDir
	localTargetPath := task.localTargetPath
	task.localTempDir = ""
	task.localTargetPath = ""
	task.remoteArchive = ""
	return localTempDir, localTargetPath
}

func cleanupDownloadLocalPaths(localTempDir, localTargetPath string) {
	if localTempDir != "" {
		_ = os.RemoveAll(localTempDir)
	}
	if localTargetPath != "" {
		_ = os.RemoveAll(localTargetPath)
	}
}
func (s *Service) registerDownloadTransferTask(downloadID string, task *downloadTransferTask) error {
	if downloadID == "" {
		return fmt.Errorf("missing download id")
	}
	if _, loaded := s.downloadTasks.LoadOrStore(downloadID, task); loaded {
		return fmt.Errorf("download transfer already exists")
	}
	return nil
}

func (s *Service) unregisterDownloadTransferTask(downloadID string, task *downloadTransferTask) {
	if current, ok := s.downloadTasks.Load(downloadID); ok && current == task {
		s.downloadTasks.Delete(downloadID)
	}
}

func (s *Service) abortDownloadTransferTaskByID(downloadID string, task *downloadTransferTask) {
	if task == nil {
		return
	}
	task.cancel()
	task.cleanup(s)
	if downloadID != "" {
		s.downloadTasks.Delete(downloadID)
	}
}

func (s *Service) AbortDownloadTransfer(identifier string) error {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return fmt.Errorf("missing download identifier")
	}
	if current, ok := s.downloadTasks.Load(identifier); ok {
		task, _ := current.(*downloadTransferTask)
		s.abortDownloadTransferTaskByID(identifier, task)
		return nil
	}
	s.downloadTasks.Range(func(key, value any) bool {
		downloadID, _ := key.(string)
		task, _ := value.(*downloadTransferTask)
		if task != nil && task.sessionId == identifier {
			s.abortDownloadTransferTaskByID(downloadID, task)
		}
		return true
	})
	return nil
}

type DownloadProgress struct {
	SessionID  string
	DownloadID string
	Mode       string
	Phase      string
	Status     string
	Progress   float64
	BytesDone  int64
	BytesTotal int64
	Current    string
	Detail     string
}

func (s *Service) emitDownloadTransferProgress(sessionId string, downloadID string, mode string, phase string, status string, progress float64, bytesDone int64, bytesTotal int64, current string, detail string) {
	s.sink.Emit("download-transfer-progress-"+sessionId, DownloadProgress{
		SessionID: sessionId, DownloadID: downloadID, Mode: mode, Phase: phase, Status: status,
		Progress: ClampPercent(progress), BytesDone: bytesDone, BytesTotal: bytesTotal,
		Current: current, Detail: detail,
	})
}

// downloadProgressWriter 只做磁盘写入与原子计数，绝不在 Write 内触发进度上报。
// pkg/sftp 的 File.WriteTo 采用严格按 offset 串行的 Reduce 阶段，w.Write 是整条下载
// 流水线的唯一出口；一旦在这里调用 runtime.EventsEmit（同步 IPC 到 WebView）或抢占
// 带锁的 MCP 传输存储，所有并发读 worker 都会堵在投递上，调度协程随之停止派发，
// 表现为进度长时间停在 0 之后极慢推进。上传路径之所以不受影响，是因为它的进度上报
// 发生在多个并行 worker 内部，单个 worker 被阻塞不会冻结整条流水线。
type downloadProgressWriter struct {
	ctx       context.Context
	dst       io.Writer
	totalSize int64
	copied    atomic.Int64
}

func (w *downloadProgressWriter) Write(p []byte) (int, error) {
	if err := ensureContextActive(w.ctx); err != nil {
		return 0, err
	}
	written, err := w.dst.Write(p)
	if written > 0 {
		w.copied.Add(int64(written))
	}
	return written, err
}

// startDownloadProgressReporter 用独立协程按固定间隔上报进度，把 IPC 开销从数据
// 流水线的关键路径上彻底移出。返回的 stop 函数会结束协程。
func startDownloadProgressReporter(ctx context.Context, writer *downloadProgressWriter, totalSize int64, onProgress func(int64, int64)) func() {
	if onProgress == nil {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		lastReported := int64(-1)
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := writer.copied.Load()
				if current == lastReported {
					continue
				}
				lastReported = current
				onProgress(current, totalSize)
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

func copyReaderWithProgressContext(ctx context.Context, dst io.Writer, src io.Reader, totalSize int64, onProgress func(int64, int64)) error {
	writer := &downloadProgressWriter{
		ctx:       ctx,
		dst:       dst,
		totalSize: totalSize,
	}
	stopReporter := startDownloadProgressReporter(ctx, writer, totalSize, onProgress)
	_, copyErr := io.Copy(writer, src)
	stopReporter()
	if copyErr != nil {
		return copyErr
	}
	if err := ensureContextActive(ctx); err != nil {
		return err
	}
	// 部分服务器对超长 READ 只回短数据甚至提前 EOF,pkg/sftp 的并发读会把它当
	// 正常结束(io.Copy 返回 nil)。这里校验实际字节数,把静默截断变成显式失败,
	// 让上层 cleanup 删除半成品而不是留下损坏文件(issue #334)。
	if totalSize > 0 {
		if copied := writer.copied.Load(); copied < totalSize {
			return fmt.Errorf("download incomplete: received %d of %d bytes (remote returned short reads or early EOF)", copied, totalSize)
		}
	}
	if onProgress != nil {
		onProgress(writer.copied.Load(), totalSize)
	}
	return nil
}

func sanitizeDownloadArchiveName(name string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		" ", "_",
	)
	cleaned := replacer.Replace(strings.TrimSpace(name))
	if cleaned == "" {
		return "download"
	}
	return cleaned
}

func collectRemoteDownloadStats(sftpClient *sftp.Client, remotePath string) (int64, int64, error) {
	var totalBytes int64
	var fileCount int64
	walker := sftpClient.Walk(remotePath)
	for walker.Step() {
		if err := walker.Err(); err != nil {
			return 0, 0, err
		}
		stat := walker.Stat()
		if stat == nil || stat.IsDir() {
			continue
		}
		totalBytes += stat.Size()
		fileCount++
	}
	return totalBytes, fileCount, nil
}

func ensureLocalDirectoryAvailable(localDir string) error {
	cleaned := strings.TrimSpace(localDir)
	if cleaned == "" {
		return fmt.Errorf("missing local directory")
	}
	return os.MkdirAll(cleaned, 0o755)
}

func extractTarGzArchive(ctx context.Context, archivePath string, destinationDir string) error {
	if err := ensureLocalDirectoryAvailable(destinationDir); err != nil {
		return err
	}
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archiveFile.Close()

	gzipReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	cleanDestination := filepath.Clean(destinationDir)
	for {
		if err := ensureContextActive(ctx); err != nil {
			return err
		}
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		targetPath := filepath.Join(cleanDestination, filepath.FromSlash(header.Name))
		cleanTargetPath := filepath.Clean(targetPath)
		if cleanTargetPath != cleanDestination && !strings.HasPrefix(cleanTargetPath, cleanDestination+string(os.PathSeparator)) {
			return fmt.Errorf("archive entry escapes destination: %s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(cleanTargetPath, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(cleanTargetPath), 0o755); err != nil {
				return err
			}
			targetFile, err := os.OpenFile(cleanTargetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if err := copyReaderWithProgressContext(ctx, targetFile, tarReader, header.Size, nil); err != nil {
				targetFile.Close()
				return err
			}
			if err := targetFile.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// 校验符号链接目标解析后仍落在解压目录内，防止恶意包借链接写出界
			linkDest := filepath.Clean(filepath.Join(filepath.Dir(cleanTargetPath), filepath.FromSlash(header.Linkname)))
			if linkDest != cleanDestination && !strings.HasPrefix(linkDest, cleanDestination+string(os.PathSeparator)) {
				return fmt.Errorf("archive symlink escapes destination: %s", header.Name)
			}
			if err := os.MkdirAll(filepath.Dir(cleanTargetPath), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, cleanTargetPath); err != nil && !os.IsExist(err) {
				return err
			}
		default:
			return fmt.Errorf("unsupported archive entry type: %s", header.Name)
		}
	}
}

func (s *Service) DownloadFileToLocal(sessionId string, downloadID string, remotePath string, localPath string, optionsJSON string) error {
	options := ParseDownloadConflictOptions(optionsJSON)
	localPath = filepath.Clean(strings.TrimSpace(localPath))
	if localPath == "" {
		return fmt.Errorf("missing local path")
	}
	localTargetExisted := false
	if _, err := os.Stat(localPath); err == nil {
		localTargetExisted = true
		if options.StrategyFor(".") == DownloadConflictStrategyAutoRename {
			renamedPath, renameErr := BuildDownloadRenamedPath(localPath, options.RenameSuffixMode, false)
			if renameErr != nil {
				return renameErr
			}
			localPath = renamedPath
			localTargetExisted = false
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	task := &downloadTransferTask{
		id:        downloadID,
		sessionId: sessionId,
		cancel:    cancel,
	}
	if err := s.registerDownloadTransferTask(downloadID, task); err != nil {
		cancel()
		return err
	}
	defer func() {
		cancel()
		task.cleanup(s)
		s.unregisterDownloadTransferTask(downloadID, task)
	}()
	if !localTargetExisted {
		task.setLocalTargetPath(localPath)
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	sftpClient, err := s.backend.SFTPClient(sessionId)
	if err != nil {
		return err
	}
	src, err := sftpClient.Open(remotePath)
	if err != nil {
		return err
	}
	defer src.Close()

	remoteInfo, err := src.Stat()
	if err != nil {
		return err
	}
	totalSize := remoteInfo.Size()

	if existingInfo, err := os.Stat(localPath); err == nil {
		if existingInfo.IsDir() {
			if err := os.RemoveAll(localPath); err != nil {
				return err
			}
		} else if options.StrategyFor(".") == DownloadConflictStrategyDiffOverwrite && !AreDownloadFilesDifferent(existingInfo, remoteInfo, options) {
			task.clearLocalTargetPath()
			s.emitDownloadTransferProgress(sessionId, downloadID, "download-file", "completed", "completed", 100, totalSize, totalSize, filepath.Base(remotePath), "skipped")
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	dst, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	fileName := filepath.Base(remotePath)
	s.emitDownloadTransferProgress(sessionId, downloadID, "download-file", "downloading", "uploading", 0, 0, totalSize, fileName, "")
	if err := copyReaderWithProgressContext(ctx, dst, src, totalSize, func(done int64, total int64) {
		progress := float64(100)
		if total > 0 {
			progress = float64(done) / float64(total) * 100
		}
		s.emitDownloadTransferProgress(sessionId, downloadID, "download-file", "downloading", "uploading", progress, done, total, fileName, "")
	}); err != nil {
		return err
	}
	if err := SyncPathTimestamps(localPath, remoteInfo.ModTime()); err != nil {
		return err
	}
	task.clearLocalTargetPath()
	s.emitDownloadTransferProgress(sessionId, downloadID, "download-file", "completed", "completed", 100, totalSize, totalSize, fileName, "")
	return nil
}

func (s *Service) DownloadDirectoryToLocal(sessionId string, downloadID string, remotePath string, localRoot string, optionsJSON string) error {
	options := ParseDownloadConflictOptions(optionsJSON)
	normalizedRemotePath := NormalizeRemoteDownloadPath(remotePath)
	if normalizedRemotePath == "" || normalizedRemotePath == "/" {
		return fmt.Errorf("invalid remote directory")
	}
	localRoot = filepath.Clean(strings.TrimSpace(localRoot))
	if localRoot == "" {
		return fmt.Errorf("missing local directory")
	}
	localTargetExisted := false
	if _, err := os.Stat(localRoot); err == nil {
		localTargetExisted = true
		if options.StrategyFor(".") == DownloadConflictStrategyAutoRename {
			renamedPath, renameErr := BuildDownloadRenamedPath(localRoot, options.RenameSuffixMode, true)
			if renameErr != nil {
				return renameErr
			}
			localRoot = renamedPath
			localTargetExisted = false
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := ensureLocalDirectoryAvailable(filepath.Dir(localRoot)); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	task := &downloadTransferTask{
		id:        downloadID,
		sessionId: sessionId,
		cancel:    cancel,
	}
	if err := s.registerDownloadTransferTask(downloadID, task); err != nil {
		cancel()
		return err
	}
	defer func() {
		cancel()
		task.cleanup(s)
		s.unregisterDownloadTransferTask(downloadID, task)
	}()

	if !localTargetExisted {
		task.setLocalTargetPath(localRoot)
	}
	tempDir, err := os.MkdirTemp("", "lumeterm-download-dir-*")
	if err != nil {
		return err
	}
	task.setLocalTempDir(tempDir)

	sftpClient, err := s.backend.SFTPClient(sessionId)
	if err != nil {
		return err
	}
	totalBytes, fileCount, err := collectRemoteDownloadStats(sftpClient, normalizedRemotePath)
	if err != nil {
		return err
	}
	s.emitDownloadTransferProgress(sessionId, downloadID, "download-directory", "downloading", "uploading", 0, 0, totalBytes, "", fmt.Sprintf("%d files", fileCount))

	tempRoot := filepath.Join(tempDir, RemoteDownloadBaseName(normalizedRemotePath))
	walker := sftpClient.Walk(normalizedRemotePath)
	var downloaded int64
	for walker.Step() {
		if err := ensureContextActive(ctx); err != nil {
			return err
		}
		if err := walker.Err(); err != nil {
			return err
		}
		currentRemotePath := filepath.ToSlash(walker.Path())
		stat := walker.Stat()
		if stat == nil {
			continue
		}
		relativePath := strings.TrimPrefix(currentRemotePath, normalizedRemotePath)
		relativePath = strings.TrimPrefix(relativePath, "/")
		localPath := tempRoot
		if relativePath != "" {
			localPath = filepath.Join(tempRoot, filepath.FromSlash(relativePath))
		}
		if stat.IsDir() {
			if err := os.MkdirAll(localPath, 0o755); err != nil {
				return err
			}
			if err := SyncPathTimestamps(localPath, stat.ModTime()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return err
		}
		src, err := sftpClient.Open(currentRemotePath)
		if err != nil {
			return err
		}
		dst, err := os.Create(localPath)
		if err != nil {
			src.Close()
			return err
		}
		totalSize := stat.Size()
		copyErr := copyReaderWithProgressContext(ctx, dst, src, totalSize, func(done int64, total int64) {
			progress := float64(100)
			if totalBytes > 0 {
				progress = float64(downloaded+done) / float64(totalBytes) * 100
			}
			s.emitDownloadTransferProgress(sessionId, downloadID, "download-directory", "downloading", "uploading", progress, downloaded+done, totalBytes, currentRemotePath, "")
		})
		closeDstErr := dst.Close()
		closeSrcErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeDstErr != nil {
			return closeDstErr
		}
		if closeSrcErr != nil {
			return closeSrcErr
		}
		if err := SyncPathTimestamps(localPath, stat.ModTime()); err != nil {
			return err
		}
		downloaded += totalSize
	}
	finalPath, err := ApplyDownloadedDirectoryFromSource(tempRoot, localRoot, options)
	if err != nil {
		return err
	}
	localRoot = finalPath
	task.clearLocalTargetPath()
	s.emitDownloadTransferProgress(sessionId, downloadID, "download-directory", "completed", "completed", 100, totalBytes, totalBytes, localRoot, fmt.Sprintf("%d files", fileCount))
	return nil
}

func (s *Service) DownloadDirectoryCompressed(sessionId string, downloadID string, remotePath string, localRoot string, optionsJSON string) error {
	options := ParseDownloadConflictOptions(optionsJSON)
	normalizedRemotePath := NormalizeRemoteDownloadPath(remotePath)
	if normalizedRemotePath == "" || normalizedRemotePath == "/" {
		return fmt.Errorf("invalid remote directory")
	}
	localRoot = filepath.Clean(strings.TrimSpace(localRoot))
	if localRoot == "" {
		return fmt.Errorf("missing local directory")
	}
	localTargetExisted := false
	if _, err := os.Stat(localRoot); err == nil {
		localTargetExisted = true
		if options.StrategyFor(".") == DownloadConflictStrategyAutoRename {
			renamedPath, renameErr := BuildDownloadRenamedPath(localRoot, options.RenameSuffixMode, true)
			if renameErr != nil {
				return renameErr
			}
			localRoot = renamedPath
			localTargetExisted = false
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := ensureLocalDirectoryAvailable(filepath.Dir(localRoot)); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	task := &downloadTransferTask{
		id:        downloadID,
		sessionId: sessionId,
		cancel:    cancel,
	}
	if err := s.registerDownloadTransferTask(downloadID, task); err != nil {
		cancel()
		return err
	}
	defer func() {
		cancel()
		task.cleanup(s)
		s.unregisterDownloadTransferTask(downloadID, task)
	}()

	if !localTargetExisted {
		task.setLocalTargetPath(localRoot)
	}
	tempDir, err := os.MkdirTemp("", "lumeterm-download-*")
	if err != nil {
		return err
	}
	task.setLocalTempDir(tempDir)

	client, _, err := s.backend.ClientEntry(sessionId)
	if err != nil {
		return err
	}
	sftpClient, err := s.backend.SFTPClient(sessionId)
	if err != nil {
		return err
	}

	remoteParentDir := pathpkg.Dir(normalizedRemotePath)
	remoteBaseName := pathpkg.Base(normalizedRemotePath)
	remoteArchiveName := fmt.Sprintf(".lumeterm_download_%d_%s.tar.gz", time.Now().UnixNano(), sanitizeDownloadArchiveName(remoteBaseName))
	remoteArchivePath := pathpkg.Join(remoteParentDir, remoteArchiveName)
	localArchivePath := filepath.Join(tempDir, remoteArchiveName)
	task.setRemoteArchive(remoteArchivePath)

	s.emitDownloadTransferProgress(sessionId, downloadID, "download-compressed", "preparing", "uploading", 0, 0, 0, "", "")
	s.emitDownloadTransferProgress(sessionId, downloadID, "download-compressed", "compressing", "uploading", 0, 0, 0, remoteBaseName, "")
	compressCommand := fmt.Sprintf("cd %s && tar -czf %s %s", shellQuotePath(remoteParentDir), shellQuotePath(remoteArchiveName), shellQuotePath(remoteBaseName))
	if _, err := s.backend.ExecuteCommand(ctx, client, compressCommand); err != nil {
		return err
	}

	remoteArchiveFile, err := sftpClient.Open(remoteArchivePath)
	if err != nil {
		return err
	}
	defer remoteArchiveFile.Close()

	archiveStat, err := remoteArchiveFile.Stat()
	if err != nil {
		return err
	}
	archiveSize := archiveStat.Size()

	localArchiveFile, err := os.Create(localArchivePath)
	if err != nil {
		return err
	}
	defer localArchiveFile.Close()

	s.emitDownloadTransferProgress(sessionId, downloadID, "download-compressed", "downloading", "uploading", 0, 0, archiveSize, remoteArchiveName, "")
	if err := copyReaderWithProgressContext(ctx, localArchiveFile, remoteArchiveFile, archiveSize, func(done int64, total int64) {
		progress := float64(100)
		if total > 0 {
			progress = float64(done) / float64(total) * 100
		}
		s.emitDownloadTransferProgress(sessionId, downloadID, "download-compressed", "downloading", "uploading", progress, done, total, remoteArchiveName, "")
	}); err != nil {
		return err
	}

	s.emitDownloadTransferProgress(sessionId, downloadID, "download-compressed", "extracting", "uploading", 99, 0, 0, remoteArchiveName, "")
	extractBaseDir := filepath.Join(tempDir, "extract")
	if err := os.MkdirAll(extractBaseDir, 0o755); err != nil {
		return err
	}
	if err := extractTarGzArchive(ctx, localArchivePath, extractBaseDir); err != nil {
		return err
	}
	extractedRoot := filepath.Join(extractBaseDir, remoteBaseName)
	if _, err := os.Stat(extractedRoot); err != nil {
		return err
	}
	finalPath, err := ApplyDownloadedDirectoryFromSource(extractedRoot, localRoot, options)
	if err != nil {
		return err
	}
	localRoot = finalPath
	task.clearLocalTargetPath()

	s.emitDownloadTransferProgress(sessionId, downloadID, "download-compressed", "cleanup-local", "uploading", 99, 0, 0, remoteArchiveName, "")
	_ = os.RemoveAll(tempDir)
	task.clearLocalTempDir()

	s.emitDownloadTransferProgress(sessionId, downloadID, "download-compressed", "cleanup-remote", "uploading", 99, 0, 0, remoteArchiveName, "")
	if err := s.backend.DeleteRemote(context.Background(), sessionId, remoteArchivePath, false); err != nil {
		return err
	}
	task.clearRemoteArchive()

	s.emitDownloadTransferProgress(sessionId, downloadID, "download-compressed", "completed", "completed", 100, archiveSize, archiveSize, localRoot, "")
	return nil
}
