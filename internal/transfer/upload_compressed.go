package transfer

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type localArchiveStats struct {
	TotalBytes int64
	FileCount  int64
	DirCount   int64
}

// compressedUploadCtxKey 用自有类型做 context key，避免与 string 类型撞键。
type compressedUploadCtxKey string

const compressedUploadSessionIDKey compressedUploadCtxKey = "compressedUploadSessionId"

type compressedUploadArchiveEntry struct {
	ArchivePath string
	LocalPath   string
	IsDir       bool
}

type compressedUploadPreflightIssue struct {
	Kind        string
	RemotePath  string
	ArchivePath string
	LocalPath   string
	Detail      string
	Suggestion  string
}

type compressedUploadPreflightReport struct {
	RemoteDir    string
	Issues       []compressedUploadPreflightIssue
	OmittedCount int
}

type compressedUploadRepairSnapshot struct {
	RemotePath   string
	IsDir        bool
	Existed      bool
	OriginalMode os.FileMode
	HadImmutable bool
}

type compressedUploadRepairState struct {
	sessionId string
	order     []string
	items     map[string]compressedUploadRepairSnapshot
}

func newCompressedUploadRepairState(sessionId string) *compressedUploadRepairState {
	return &compressedUploadRepairState{
		sessionId: sessionId,
		order:     make([]string, 0),
		items:     make(map[string]compressedUploadRepairSnapshot),
	}
}

func (state *compressedUploadRepairState) remember(snapshot compressedUploadRepairSnapshot) {
	if state == nil {
		return
	}
	if _, exists := state.items[snapshot.RemotePath]; exists {
		return
	}
	state.order = append(state.order, snapshot.RemotePath)
	state.items[snapshot.RemotePath] = snapshot
}

func (state *compressedUploadRepairState) restore(service *Service) {
	if state == nil || service == nil || state.sessionId == "" {
		return
	}
	sftpClient, err := service.backend.SFTPClient(state.sessionId)
	if err != nil {
		return
	}
	for index := len(state.order) - 1; index >= 0; index-- {
		remotePath := state.order[index]
		snapshot, ok := state.items[remotePath]
		if !ok || !snapshot.Existed {
			continue
		}
		info, err := sftpClient.Stat(remotePath)
		if err != nil || info == nil || info.IsDir() != snapshot.IsDir {
			continue
		}
		_ = service.clearRemoteImmutableAttribute(state.sessionId, remotePath)
		_ = sftpClient.Chmod(remotePath, snapshot.OriginalMode)
		if snapshot.HadImmutable {
			_ = service.setRemoteImmutableAttribute(state.sessionId, remotePath, true)
		}
	}
}

func (report *compressedUploadPreflightReport) Error() string {
	if report == nil {
		return "compressed upload preflight failed"
	}
	var builder strings.Builder
	builder.WriteString("compressed upload preflight failed\n")
	if report.RemoteDir != "" {
		builder.WriteString("remote target: ")
		builder.WriteString(report.RemoteDir)
		builder.WriteByte('\n')
	}
	builder.WriteString("the remote server is unlikely to extract the uploaded archive successfully.\n")
	for index, issue := range report.Issues {
		fmt.Fprintf(&builder, "%d. [%s] %s\n", index+1, issue.Kind, issue.RemotePath)
		if issue.ArchivePath != "" {
			fmt.Fprintf(&builder, "   archive entry: %s\n", issue.ArchivePath)
		}
		if issue.LocalPath != "" {
			fmt.Fprintf(&builder, "   local source: %s\n", issue.LocalPath)
		}
		if issue.Detail != "" {
			fmt.Fprintf(&builder, "   reason: %s\n", issue.Detail)
		}
		if issue.Suggestion != "" {
			fmt.Fprintf(&builder, "   suggestion: %s\n", issue.Suggestion)
		}
	}
	if report.OmittedCount > 0 {
		fmt.Fprintf(&builder, "%d more issue(s) omitted.\n", report.OmittedCount)
	}
	return strings.TrimSpace(builder.String())
}

type compressedUploadTask struct {
	id        string
	sessionId string
	cancel    context.CancelFunc

	mu            sync.Mutex
	tempDir       string
	remoteArchive string
}

type compressedUploadSessionLimiter struct {
	mu     sync.Mutex
	active int
}

func ClampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func (task *compressedUploadTask) setTempDir(tempDir string) {
	task.mu.Lock()
	task.tempDir = tempDir
	task.mu.Unlock()
}

func (task *compressedUploadTask) clearTempDir() {
	task.mu.Lock()
	task.tempDir = ""
	task.mu.Unlock()
}

func (task *compressedUploadTask) setRemoteArchive(remoteArchive string) {
	task.mu.Lock()
	task.remoteArchive = remoteArchive
	task.mu.Unlock()
}

func (task *compressedUploadTask) clearRemoteArchive() {
	task.mu.Lock()
	task.remoteArchive = ""
	task.mu.Unlock()
}

func (task *compressedUploadTask) snapshot() (string, string) {
	task.mu.Lock()
	defer task.mu.Unlock()
	return task.tempDir, task.remoteArchive
}

func (task *compressedUploadTask) cleanupLocal() {
	tempDir, _ := task.snapshot()
	if tempDir != "" {
		_ = os.RemoveAll(tempDir)
		task.clearTempDir()
	}
}

func (task *compressedUploadTask) cleanup(service *Service) {
	task.cleanupLocal()
	_, remoteArchive := task.snapshot()
	if remoteArchive != "" {
		_ = service.backend.DeleteRemote(context.Background(), task.sessionId, remoteArchive, false)
		task.clearRemoteArchive()
	}
}

func (task *compressedUploadTask) detachOnDisconnect() string {
	task.mu.Lock()
	defer task.mu.Unlock()
	tempDir := task.tempDir
	task.tempDir = ""
	task.remoteArchive = ""
	return tempDir
}

func cleanupCompressedLocalPath(tempDir string) {
	if tempDir != "" {
		_ = os.RemoveAll(tempDir)
	}
}

func (s *Service) getCompressedUploadSessionLimiter(sessionId string) *compressedUploadSessionLimiter {
	limiter, _ := s.compressedSlots.LoadOrStore(sessionId, &compressedUploadSessionLimiter{})
	if typed, ok := limiter.(*compressedUploadSessionLimiter); ok && typed != nil {
		return typed
	}
	fallback := &compressedUploadSessionLimiter{}
	s.compressedSlots.Store(sessionId, fallback)
	return fallback
}

func (s *Service) acquireCompressedUploadSlot(sessionId string, limit int, ctx context.Context) (*compressedUploadSessionLimiter, error) {
	if limit < 1 {
		limit = 1
	}
	for {
		// 每轮从 map 取 limiter：release 空闲删除后，避免继续占用已摘除的旧实例
		limiter := s.getCompressedUploadSessionLimiter(sessionId)
		limiter.mu.Lock()
		if current, ok := s.compressedSlots.Load(sessionId); !ok || current != limiter {
			limiter.mu.Unlock()
			continue
		}
		if limiter.active < limit {
			limiter.active++
			limiter.mu.Unlock()
			return limiter, nil
		}
		limiter.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func (s *Service) releaseCompressedUploadSlot(sessionId string, limiter *compressedUploadSessionLimiter) {
	if limiter == nil {
		return
	}
	limiter.mu.Lock()
	if limiter.active > 0 {
		limiter.active--
	}
	// active==0 时摘除 map 条目，避免 session 维度只增不删
	if limiter.active == 0 && sessionId != "" {
		if current, ok := s.compressedSlots.Load(sessionId); ok && current == limiter {
			s.compressedSlots.Delete(sessionId)
		}
	}
	limiter.mu.Unlock()
}

func (s *Service) registerCompressedUploadTask(uploadID string, task *compressedUploadTask) error {
	if uploadID == "" {
		return fmt.Errorf("missing upload id")
	}
	if _, loaded := s.compressedTasks.LoadOrStore(uploadID, task); loaded {
		return fmt.Errorf("compressed upload already exists")
	}
	return nil
}

func (s *Service) unregisterCompressedUploadTask(uploadID string, task *compressedUploadTask) {
	if current, ok := s.compressedTasks.Load(uploadID); ok && current == task {
		s.compressedTasks.Delete(uploadID)
	}
}

func (s *Service) abortCompressedUploadTaskByID(uploadID string, task *compressedUploadTask) {
	if task == nil {
		return
	}
	task.cancel()
	if uploadID != "" {
		s.compressedTasks.Delete(uploadID)
	}
	go task.cleanup(s)
}

func (s *Service) AbortCompressedUpload(identifier string) error {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return fmt.Errorf("missing upload identifier")
	}

	if current, ok := s.compressedTasks.Load(identifier); ok {
		task, _ := current.(*compressedUploadTask)
		s.abortCompressedUploadTaskByID(identifier, task)
		return nil
	}

	s.compressedTasks.Range(func(key, value any) bool {
		uploadID, _ := key.(string)
		task, _ := value.(*compressedUploadTask)
		if task != nil && task.sessionId == identifier {
			s.abortCompressedUploadTaskByID(uploadID, task)
		}
		return true
	})
	return nil
}

type CompressedUploadProgress struct {
	SessionID     string  `json:"-"`
	UploadID      string  `json:"uploadId"`
	Phase         string  `json:"phase"`
	Progress      float64 `json:"progress"`
	PhaseProgress float64 `json:"phaseProgress"`
	BytesDone     int64   `json:"bytesDone"`
	BytesTotal    int64   `json:"bytesTotal"`
	Current       string  `json:"current"`
	Detail        string  `json:"detail"`
}

func (s *Service) emitCompressedUploadProgress(sessionId string, uploadID string, phase string, progress float64, phaseProgress float64, bytesDone int64, bytesTotal int64, current string, detail string) {
	s.sink.Emit("compressed-upload-progress-"+sessionId, CompressedUploadProgress{
		SessionID:     sessionId,
		UploadID:      uploadID,
		Phase:         phase,
		Progress:      ClampPercent(progress),
		PhaseProgress: ClampPercent(phaseProgress),
		BytesDone:     bytesDone,
		BytesTotal:    bytesTotal,
		Current:       current,
		Detail:        detail,
	})
}

func collectLocalArchiveStats(localPaths []string) (localArchiveStats, error) {
	var stats localArchiveStats
	for _, localPath := range localPaths {
		cleanPath := strings.TrimSpace(localPath)
		if cleanPath == "" {
			continue
		}
		absPath, rootRealPath, info, err := resolveArchiveSourcePath(cleanPath)
		if err != nil {
			return stats, err
		}
		if err := collectLocalArchiveStatsForPath(absPath, rootRealPath, make(map[string]struct{}), &stats); err != nil {
			return stats, err
		}
		if !info.IsDir() {
			continue
		}
	}
	return stats, nil
}

func collectLocalArchiveEntries(localPaths []string) ([]compressedUploadArchiveEntry, error) {
	entries := make([]compressedUploadArchiveEntry, 0)
	for _, localPath := range localPaths {
		cleanPath := strings.TrimSpace(localPath)
		if cleanPath == "" {
			continue
		}
		absPath, rootRealPath, _, err := resolveArchiveSourcePath(cleanPath)
		if err != nil {
			return nil, err
		}
		if err := collectLocalArchiveEntriesForPath(absPath, filepath.Base(filepath.Clean(cleanPath)), rootRealPath, make(map[string]struct{}), &entries); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func collectLocalArchiveEntriesForPath(localPath string, archiveRoot string, rootRealPath string, stack map[string]struct{}, entries *[]compressedUploadArchiveEntry) error {
	_, realPath, info, err := resolveArchiveSourcePath(localPath)
	if err != nil {
		return err
	}
	if !isPathWithinRoot(rootRealPath, realPath) {
		return fmt.Errorf("link target escapes selected root: %s -> %s", localPath, realPath)
	}

	archiveRoot = cleanArchiveName(archiveRoot)
	if info.IsDir() {
		if _, exists := stack[realPath]; exists {
			return fmt.Errorf("cyclic link detected: %s -> %s", localPath, realPath)
		}
		stack[realPath] = struct{}{}
		defer delete(stack, realPath)

		*entries = append(*entries, compressedUploadArchiveEntry{
			ArchivePath: archiveRoot,
			LocalPath:   realPath,
			IsDir:       true,
		})

		dirEntries, err := os.ReadDir(realPath)
		if err != nil {
			return err
		}
		for _, dirEntry := range dirEntries {
			childPath := filepath.Join(realPath, dirEntry.Name())
			childArchiveName := filepath.ToSlash(filepath.Join(archiveRoot, dirEntry.Name()))
			if err := collectLocalArchiveEntriesForPath(childPath, childArchiveName, rootRealPath, stack, entries); err != nil {
				return err
			}
		}
		return nil
	}

	*entries = append(*entries, compressedUploadArchiveEntry{
		ArchivePath: archiveRoot,
		LocalPath:   realPath,
		IsDir:       false,
	})
	return nil
}

func cleanArchiveName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Trim(name, "/")
	if name == "" || name == "." || name == ".." {
		return "upload"
	}
	parts := strings.Split(name, "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			continue
		}
		cleaned = append(cleaned, part)
	}
	if len(cleaned) == 0 {
		return "upload"
	}
	return strings.Join(cleaned, "/")
}

func ensureCompressedUploadContext(ctx context.Context) error {
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

func resolveArchiveSourcePath(localPath string) (string, string, os.FileInfo, error) {
	cleanPath := strings.TrimSpace(localPath)
	if cleanPath == "" {
		return "", "", nil, fmt.Errorf("empty local path")
	}

	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", "", nil, err
	}

	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to resolve link target for %s: %w", absPath, err)
	}
	realPath, err = filepath.Abs(realPath)
	if err != nil {
		return "", "", nil, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "", "", nil, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", "", nil, fmt.Errorf("unsupported file type: %s (%s)", absPath, info.Mode().String())
	}
	return absPath, realPath, info, nil
}

func isPathWithinRoot(rootRealPath string, targetRealPath string) bool {
	rel, err := filepath.Rel(rootRealPath, targetRealPath)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func collectLocalArchiveStatsForPath(localPath string, rootRealPath string, stack map[string]struct{}, stats *localArchiveStats) error {
	_, realPath, info, err := resolveArchiveSourcePath(localPath)
	if err != nil {
		return err
	}
	if !isPathWithinRoot(rootRealPath, realPath) {
		return fmt.Errorf("link target escapes selected root: %s -> %s", localPath, realPath)
	}

	if info.IsDir() {
		if _, exists := stack[realPath]; exists {
			return fmt.Errorf("cyclic link detected: %s -> %s", localPath, realPath)
		}
		stack[realPath] = struct{}{}
		defer delete(stack, realPath)

		stats.DirCount++
		entries, err := os.ReadDir(realPath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := collectLocalArchiveStatsForPath(filepath.Join(realPath, entry.Name()), rootRealPath, stack, stats); err != nil {
				return err
			}
		}
		return nil
	}

	stats.FileCount++
	stats.TotalBytes += info.Size()
	return nil
}

func writeArchiveHeader(tw *tar.Writer, archiveName string, info os.FileInfo) error {
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = cleanArchiveName(archiveName)
	if info.IsDir() && !strings.HasSuffix(header.Name, "/") {
		header.Name += "/"
	}
	return tw.WriteHeader(header)
}

func addRegularFileToTar(ctx context.Context, tw *tar.Writer, realPath string, archiveName string, info os.FileInfo, onProgress func(int64, string)) error {
	if err := ensureCompressedUploadContext(ctx); err != nil {
		return err
	}
	if err := writeArchiveHeader(tw, archiveName, info); err != nil {
		return err
	}

	file, err := os.Open(realPath)
	if err != nil {
		return err
	}
	defer file.Close()

	buf := make([]byte, 1024*1024)
	for {
		if err := ensureCompressedUploadContext(ctx); err != nil {
			return err
		}
		n, readErr := file.Read(buf)
		if n > 0 {
			if _, err := tw.Write(buf[:n]); err != nil {
				return err
			}
			if onProgress != nil {
				onProgress(int64(n), realPath)
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func addPathToTar(ctx context.Context, tw *tar.Writer, localPath string, archiveRoot string, rootRealPath string, stack map[string]struct{}, onProgress func(int64, string)) error {
	if err := ensureCompressedUploadContext(ctx); err != nil {
		return err
	}

	_, realPath, info, err := resolveArchiveSourcePath(localPath)
	if err != nil {
		return err
	}
	if !isPathWithinRoot(rootRealPath, realPath) {
		return fmt.Errorf("link target escapes selected root: %s -> %s", localPath, realPath)
	}

	archiveRoot = cleanArchiveName(archiveRoot)
	if info.IsDir() {
		if _, exists := stack[realPath]; exists {
			return fmt.Errorf("cyclic link detected: %s -> %s", localPath, realPath)
		}
		stack[realPath] = struct{}{}
		defer delete(stack, realPath)

		if err := writeArchiveHeader(tw, archiveRoot, info); err != nil {
			return err
		}

		entries, err := os.ReadDir(realPath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			childPath := filepath.Join(realPath, entry.Name())
			childArchiveName := filepath.ToSlash(filepath.Join(archiveRoot, entry.Name()))
			if err := addPathToTar(ctx, tw, childPath, childArchiveName, rootRealPath, stack, onProgress); err != nil {
				return err
			}
		}
		return nil
	}

	return addRegularFileToTar(ctx, tw, realPath, archiveRoot, info, onProgress)
}

func createLocalTarGz(ctx context.Context, localPaths []string, stats localArchiveStats, onProgress func(int64, int64, string)) (string, string, error) {
	if len(localPaths) == 0 {
		return "", "", fmt.Errorf("no local paths")
	}
	tempDir, err := os.MkdirTemp("", "lumeterm-upload-*")
	if err != nil {
		return "", "", err
	}
	archiveName := fmt.Sprintf("lumeterm_upload_%d.tar.gz", time.Now().UnixNano())
	archivePath := filepath.Join(tempDir, archiveName)
	file, err := os.Create(archivePath)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", err
	}

	var processedBytes int64
	lastEmit := time.Time{}
	emit := func(current string, force bool) {
		if onProgress == nil {
			return
		}
		now := time.Now()
		if force || now.Sub(lastEmit) > 200*time.Millisecond || processedBytes >= stats.TotalBytes {
			onProgress(processedBytes, stats.TotalBytes, current)
			lastEmit = now
		}
	}
	onBytes := func(n int64, current string) {
		processedBytes += n
		if stats.TotalBytes > 0 && processedBytes > stats.TotalBytes {
			processedBytes = stats.TotalBytes
		}
		emit(current, false)
	}

	gw := gzip.NewWriter(file)
	tw := tar.NewWriter(gw)
	emit("", true)
	for _, localPath := range localPaths {
		if err := ensureCompressedUploadContext(ctx); err != nil {
			_ = tw.Close()
			_ = gw.Close()
			_ = file.Close()
			_ = os.RemoveAll(tempDir)
			return "", "", err
		}
		cleanPath := strings.TrimSpace(localPath)
		if cleanPath == "" {
			continue
		}
		absPath, rootRealPath, _, err := resolveArchiveSourcePath(cleanPath)
		if err != nil {
			_ = tw.Close()
			_ = gw.Close()
			_ = file.Close()
			_ = os.RemoveAll(tempDir)
			return "", "", err
		}
		if err := addPathToTar(ctx, tw, absPath, filepath.Base(filepath.Clean(cleanPath)), rootRealPath, make(map[string]struct{}), onBytes); err != nil {
			_ = tw.Close()
			_ = gw.Close()
			_ = file.Close()
			_ = os.RemoveAll(tempDir)
			return "", "", err
		}
		emit(cleanPath, true)
	}
	if stats.TotalBytes == 0 {
		emit("", true)
	}
	if err := tw.Close(); err != nil {
		_ = gw.Close()
		_ = file.Close()
		_ = os.RemoveAll(tempDir)
		return "", "", err
	}
	if err := gw.Close(); err != nil {
		_ = file.Close()
		_ = os.RemoveAll(tempDir)
		return "", "", err
	}
	if err := file.Close(); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", err
	}
	if onProgress != nil {
		onProgress(stats.TotalBytes, stats.TotalBytes, "")
	}
	return archivePath, tempDir, nil
}

func normalizeRemotePathForCompare(remotePath string) string {
	normalized := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(strings.TrimSpace(remotePath), "\\", "/")))
	if normalized == "." {
		return "/"
	}
	if strings.HasPrefix(strings.TrimSpace(remotePath), "/") && !strings.HasPrefix(normalized, "/") {
		return "/" + strings.TrimPrefix(normalized, "/")
	}
	return normalized
}

func joinRemoteUploadPath(remoteDir string, archivePath string) string {
	base := strings.ReplaceAll(strings.TrimSpace(remoteDir), "\\", "/")
	if base == "" {
		base = "/"
	}
	joined := filepath.ToSlash(filepath.Clean(filepath.Join(base, filepath.FromSlash(cleanArchiveName(archivePath)))))
	if strings.HasPrefix(base, "/") && !strings.HasPrefix(joined, "/") {
		return "/" + strings.TrimPrefix(joined, "/")
	}
	return joined
}

func hasFailedRemoteAncestor(failedDirs map[string]struct{}, remotePath string) bool {
	normalizedPath := normalizeRemotePathForCompare(remotePath)
	for dirPath := range failedDirs {
		if normalizedPath == dirPath || strings.HasPrefix(normalizedPath, dirPath+"/") {
			return true
		}
	}
	return false
}

func isRemotePathNotExist(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such file") || strings.Contains(message, "does not exist") || strings.Contains(message, "not found")
}

func bestEffortRemoteChmod(sftpClient *sftp.Client, remotePath string, mode os.FileMode) {
	if sftpClient == nil {
		return
	}
	_ = sftpClient.Chmod(remotePath, mode)
}

func probeRemoteDirectoryWritable(sftpClient *sftp.Client, remotePath string) error {
	probeName := fmt.Sprintf(".lumeterm_write_probe_%d", time.Now().UnixNano())
	probePath := joinRemoteUploadPath(remotePath, probeName)
	probeFile, err := sftpClient.Create(probePath)
	if err != nil {
		return err
	}
	if err := probeFile.Close(); err != nil {
		_ = sftpClient.Remove(probePath)
		return err
	}
	return sftpClient.Remove(probePath)
}

func probeRemoteFileOverwriteable(sftpClient *sftp.Client, remotePath string) error {
	probeFile, err := sftpClient.OpenFile(remotePath, os.O_WRONLY)
	if err != nil {
		return err
	}
	return probeFile.Close()
}

func (s *Service) readRemoteAttrOutput(sessionId string, remotePath string) string {
	client, _, err := s.backend.ClientEntry(sessionId)
	if err != nil {
		return ""
	}
	output, err := s.backend.ExecuteCommand(context.Background(), client, "lsattr -d -- "+shellQuotePath(remotePath)+" 2>/dev/null || true")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func remoteAttrOutputHasImmutable(output string) bool {
	fields := strings.Fields(strings.TrimSpace(output))
	return len(fields) > 0 && strings.Contains(fields[0], "i")
}

func (s *Service) bestEffortDescribeRemoteMutationFailure(sessionId string, remotePath string) string {
	output := s.readRemoteAttrOutput(sessionId, remotePath)
	if output == "" {
		return ""
	}
	if remoteAttrOutputHasImmutable(output) {
		return "immutable attribute detected: " + output
	}
	return "remote attributes: " + output
}

func (s *Service) setRemoteImmutableAttribute(sessionId string, remotePath string, immutable bool) error {
	client, _, err := s.backend.ClientEntry(sessionId)
	if err != nil {
		return err
	}
	flag := "-i"
	if immutable {
		flag = "+i"
	}
	_, err = s.backend.ExecuteCommand(context.Background(), client, "chattr "+flag+" -- "+shellQuotePath(remotePath))
	return err
}

func (s *Service) clearRemoteImmutableAttribute(sessionId string, remotePath string) error {
	return s.setRemoteImmutableAttribute(sessionId, remotePath, false)
}

func (s *Service) snapshotCompressedUploadRepairState(sessionId string, sftpClient *sftp.Client, remotePath string, isDir bool) compressedUploadRepairSnapshot {
	snapshot := compressedUploadRepairSnapshot{
		RemotePath:   remotePath,
		IsDir:        isDir,
		Existed:      false,
		OriginalMode: 0,
		HadImmutable: remoteAttrOutputHasImmutable(s.readRemoteAttrOutput(sessionId, remotePath)),
	}
	info, err := sftpClient.Stat(remotePath)
	if err == nil && info != nil && info.IsDir() == isDir {
		snapshot.Existed = true
		snapshot.OriginalMode = info.Mode()
	}
	return snapshot
}

func (s *Service) bestEffortRepairRemoteDirectory(sessionId string, sftpClient *sftp.Client, remotePath string) {
	normalizedPath := normalizeRemotePathForCompare(remotePath)
	if normalizedPath == "" {
		return
	}
	info, err := sftpClient.Stat(normalizedPath)
	if err != nil && isRemotePathNotExist(err) {
		parentPath := normalizeRemotePathForCompare(filepath.Dir(normalizedPath))
		if parentPath != "" && parentPath != "." && parentPath != normalizedPath {
			_ = s.clearRemoteImmutableAttribute(sessionId, parentPath)
			bestEffortRemoteChmod(sftpClient, parentPath, 0o755)
		}
		_ = sftpClient.MkdirAll(normalizedPath)
		info, err = sftpClient.Stat(normalizedPath)
	}
	if err != nil || info == nil || !info.IsDir() {
		return
	}
	_ = s.clearRemoteImmutableAttribute(sessionId, normalizedPath)
	bestEffortRemoteChmod(sftpClient, normalizedPath, 0o755)
	_ = probeRemoteDirectoryWritable(sftpClient, normalizedPath)
}

func (s *Service) bestEffortRepairRemoteFile(sessionId string, sftpClient *sftp.Client, remotePath string) {
	normalizedPath := normalizeRemotePathForCompare(remotePath)
	if normalizedPath == "" {
		return
	}
	info, err := sftpClient.Stat(normalizedPath)
	if err != nil || info == nil || info.IsDir() {
		return
	}
	_ = s.clearRemoteImmutableAttribute(sessionId, normalizedPath)
	bestEffortRemoteChmod(sftpClient, normalizedPath, 0o644)
	_ = probeRemoteFileOverwriteable(sftpClient, normalizedPath)
}

func (s *Service) autoRepairCompressedUploadTargets(sessionId string, remoteDir string, entries []compressedUploadArchiveEntry) (*compressedUploadRepairState, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	sftpClient, err := s.backend.SFTPClient(sessionId)
	if err != nil {
		return nil, err
	}

	repairState := newCompressedUploadRepairState(sessionId)
	checkedPaths := make(map[string]struct{})
	for _, entry := range entries {
		remotePath := joinRemoteUploadPath(remoteDir, entry.ArchivePath)
		if _, exists := checkedPaths[remotePath]; exists {
			continue
		}
		checkedPaths[remotePath] = struct{}{}
		repairState.remember(s.snapshotCompressedUploadRepairState(sessionId, sftpClient, remotePath, entry.IsDir))
		if entry.IsDir {
			s.bestEffortRepairRemoteDirectory(sessionId, sftpClient, remotePath)
			continue
		}
		s.bestEffortRepairRemoteFile(sessionId, sftpClient, remotePath)
	}

	ctx := context.WithValue(context.Background(), compressedUploadSessionIDKey, sessionId)
	if err := s.preflightCompressedUploadTargets(ctx, sessionId, remoteDir, entries, nil); err != nil {
		repairState.restore(s)
		return nil, err
	}
	return repairState, nil
}

func (s *Service) AutoRepairCompressedUploadTargets(sessionId string, localPaths []string, remoteDir string) error {
	paths := make([]string, 0, len(localPaths))
	for _, localPath := range localPaths {
		localPath = strings.TrimSpace(localPath)
		if localPath != "" {
			paths = append(paths, localPath)
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("no local paths")
	}

	entries, err := collectLocalArchiveEntries(paths)
	if err != nil {
		return err
	}
	repairState, err := s.autoRepairCompressedUploadTargets(sessionId, remoteDir, entries)
	if repairState != nil {
		repairState.restore(s)
	}
	return err
}

func (s *Service) preflightCompressedUploadTargets(ctx context.Context, sessionId string, remoteDir string, entries []compressedUploadArchiveEntry, onProgress func(int, int, string, string)) error {
	if len(entries) == 0 {
		return nil
	}
	sftpClient, err := s.backend.SFTPClient(sessionId)
	if err != nil {
		return err
	}

	report := &compressedUploadPreflightReport{
		RemoteDir: normalizeRemotePathForCompare(remoteDir),
	}
	const maxIssues = 25
	failedDirs := make(map[string]struct{})
	checkedPaths := make(map[string]struct{})
	addIssue := func(entry compressedUploadArchiveEntry, remotePath string, kind string, cause error, suggestion string) {
		detail := ""
		if cause != nil {
			detail = cause.Error()
		}
		if diagnosis := s.bestEffortDescribeRemoteMutationFailure(sessionId, remotePath); diagnosis != "" {
			if detail != "" {
				detail += "; "
			}
			detail += diagnosis
		}
		issue := compressedUploadPreflightIssue{
			Kind:        kind,
			RemotePath:  remotePath,
			ArchivePath: cleanArchiveName(entry.ArchivePath),
			LocalPath:   entry.LocalPath,
			Detail:      detail,
			Suggestion:  suggestion,
		}
		if len(report.Issues) < maxIssues {
			report.Issues = append(report.Issues, issue)
			return
		}
		report.OmittedCount++
	}

	totalEntries := len(entries)
	for index, entry := range entries {
		if err := ensureCompressedUploadContext(ctx); err != nil {
			return err
		}
		remotePath := joinRemoteUploadPath(remoteDir, entry.ArchivePath)
		if _, exists := checkedPaths[remotePath]; exists {
			continue
		}
		checkedPaths[remotePath] = struct{}{}
		if hasFailedRemoteAncestor(failedDirs, remotePath) {
			continue
		}

		progressDetail := "checking directory writability"
		if !entry.IsDir {
			progressDetail = "checking existing file overwriteability"
		}
		if onProgress != nil {
			onProgress(index, totalEntries, remotePath, progressDetail)
		}

		if entry.IsDir {
			info, statErr := sftpClient.Stat(remotePath)
			if statErr != nil {
				if isRemotePathNotExist(statErr) {
					if mkdirErr := sftpClient.MkdirAll(remotePath); mkdirErr != nil {
						addIssue(entry, remotePath, "directory-create", mkdirErr, "ensure the current SSH user can create this directory before retrying.")
						failedDirs[remotePath] = struct{}{}
						continue
					}
					info, statErr = sftpClient.Stat(remotePath)
				}
				if statErr != nil {
					addIssue(entry, remotePath, "directory-stat", statErr, "check whether the directory exists and is accessible to the current SSH user.")
					failedDirs[remotePath] = struct{}{}
					continue
				}
			}
			if !info.IsDir() {
				addIssue(entry, remotePath, "directory-type-conflict", fmt.Errorf("remote path already exists as a file"), "rename or remove the conflicting remote file before retrying.")
				failedDirs[remotePath] = struct{}{}
				continue
			}
			bestEffortRemoteChmod(sftpClient, remotePath, 0o755)
			if probeErr := probeRemoteDirectoryWritable(sftpClient, remotePath); probeErr != nil {
				addIssue(entry, remotePath, "directory-write-probe", probeErr, "ensure the current SSH user can create and remove files in this directory, and remove immutable or readonly protection if present.")
				failedDirs[remotePath] = struct{}{}
			}
			continue
		}

		parentDir := normalizeRemotePathForCompare(filepath.Dir(remotePath))
		if hasFailedRemoteAncestor(failedDirs, parentDir) {
			continue
		}

		info, statErr := sftpClient.Stat(remotePath)
		if statErr != nil {
			if isRemotePathNotExist(statErr) {
				continue
			}
			addIssue(entry, remotePath, "file-stat", statErr, "check whether the existing remote file is accessible to the current SSH user.")
			continue
		}
		if info.IsDir() {
			addIssue(entry, remotePath, "file-type-conflict", fmt.Errorf("remote path already exists as a directory"), "rename or remove the conflicting remote directory before retrying.")
			continue
		}
		bestEffortRemoteChmod(sftpClient, remotePath, 0o644)
		if probeErr := probeRemoteFileOverwriteable(sftpClient, remotePath); probeErr != nil {
			addIssue(entry, remotePath, "file-overwrite-probe", probeErr, "ensure the current SSH user can reopen and overwrite this file, and remove immutable or readonly protection if present.")
		}
	}

	if onProgress != nil {
		onProgress(totalEntries, totalEntries, "", "remote preflight completed")
	}
	if len(report.Issues) > 0 {
		return report
	}
	return nil
}

func (s *Service) uploadLocalFileWithContext(ctx context.Context, sshClient *ssh.Client, maxConcurrent int, localPath string, remoteDir string, onProgress func(int64, int64)) error {
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()

	var totalSize int64
	if stat, statErr := src.Stat(); statErr == nil {
		totalSize = stat.Size()
	}

	destPath := filepath.ToSlash(filepath.Join(remoteDir, filepath.Base(localPath)))
	tempPath := destPath + ".lumetermpart." + newUploadObjectID("upload_file")

	pool := newSFTPUploadPool(sshClient, maxConcurrent, s.Tuning())
	if sessionId, ok := ctx.Value(compressedUploadSessionIDKey).(string); ok && strings.TrimSpace(sessionId) != "" {
		pool.onChannelDelta = func(delta int) {
			s.backend.UpdateUploadChannels(sessionId, delta)
		}
	}
	defer pool.Close()

	// Pre-create remote temp file
	{
		client, err := pool.Acquire()
		if err != nil {
			return err
		}
		handle, openErr := client.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
		pool.Release(client)
		if openErr != nil {
			return openErr
		}
		_ = handle.Close()
	}

	removeTemp := true
	defer func() {
		if !removeTemp {
			return
		}
		if client, err := pool.Acquire(); err == nil {
			_ = client.Remove(tempPath)
			pool.Release(client)
		}
	}()

	chunkSize := int64(256 * 1024) // 256KB chunks (matches frontend UI chunk size for smooth progress updates)
	totalChunks := int64(0)
	if totalSize > 0 {
		totalChunks = (totalSize + chunkSize - 1) / chunkSize
	}

	workerCount := maxConcurrent
	if workerCount < 1 {
		workerCount = 1
	}
	if totalChunks > 0 && workerCount > int(totalChunks) {
		workerCount = int(totalChunks)
	}

	type chunkJob struct {
		offset int64
		length int64
	}

	jobs := make(chan chunkJob, totalChunks)
	for i := int64(0); i < totalChunks; i++ {
		offset := i * chunkSize
		length := chunkSize
		if offset+length > totalSize {
			length = totalSize - offset
		}
		jobs <- chunkJob{offset: offset, length: length}
	}
	close(jobs)

	var uploaded int64
	var firstErr error
	var errMu sync.Mutex
	setErr := func(e error) {
		if e == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = e
		}
		errMu.Unlock()
	}

	var wg sync.WaitGroup
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	lastEmit := time.Now()
	var emitMu sync.Mutex
	maybeEmit := func(done int64, force bool) {
		if onProgress == nil {
			return
		}
		emitMu.Lock()
		defer emitMu.Unlock()
		now := time.Now()
		if force || now.Sub(lastEmit) > 200*time.Millisecond || done >= totalSize {
			lastEmit = now
			onProgress(done, totalSize)
		}
	}

	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			client, err := pool.Acquire()
			if err != nil {
				setErr(err)
				workerCancel()
				return
			}
			defer pool.Release(client)

			handle, openErr := client.OpenFile(tempPath, os.O_WRONLY)
			if openErr != nil {
				setErr(openErr)
				workerCancel()
				return
			}
			defer handle.Close()

			buf := make([]byte, chunkSize)
			for {
				select {
				case <-workerCtx.Done():
					return
				case job, ok := <-jobs:
					if !ok {
						return
					}
					if workerCtx.Err() != nil {
						return
					}
					// Check error
					errMu.Lock()
					hasErr := firstErr != nil
					errMu.Unlock()
					if hasErr {
						return
					}

					// Read chunk
					n, readErr := src.ReadAt(buf[:job.length], job.offset)
					if readErr != nil && readErr != io.EOF {
						setErr(readErr)
						workerCancel()
						return
					}
					if n != int(job.length) {
						setErr(fmt.Errorf("short read at offset %d: %d/%d", job.offset, n, job.length))
						workerCancel()
						return
					}

					// Write chunk using worker's persistent file handle
					written, writeErr := handle.WriteAt(buf[:job.length], job.offset)
					if writeErr != nil {
						setErr(writeErr)
						workerCancel()
						return
					}
					if written != int(job.length) {
						setErr(io.ErrShortWrite)
						workerCancel()
						return
					}

					newUploaded := atomic.AddInt64(&uploaded, job.length)
					maybeEmit(newUploaded, false)
				}
			}
		}()
	}

	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	maybeEmit(totalSize, true)

	// Complete by truncating and renaming
	{
		client, err := pool.Acquire()
		if err != nil {
			return err
		}
		finishErr := func() error {
			handle, openErr := client.OpenFile(tempPath, os.O_WRONLY)
			if openErr != nil {
				return openErr
			}
			truncateErr := handle.Truncate(totalSize)
			closeErr := handle.Close()
			if truncateErr != nil {
				return truncateErr
			}
			if closeErr != nil {
				return closeErr
			}
			if err := client.PosixRename(tempPath, destPath); err != nil {
				return client.Rename(tempPath, destPath)
			}
			return nil
		}()
		pool.Release(client)
		if finishErr != nil {
			return finishErr
		}
	}

	removeTemp = false
	return nil
}

func shellQuotePath(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (s *Service) uncompressUploadedArchive(sessionId string, remotePath string) error {
	client, _, err := s.backend.ClientEntry(sessionId)
	if err != nil {
		return err
	}
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return fmt.Errorf("missing remote path")
	}
	remoteDir := filepath.ToSlash(filepath.Dir(remotePath))
	base := filepath.Base(remotePath)
	safeDir := shellQuotePath(remoteDir)
	safeBase := shellQuotePath(base)
	var command string
	lowerBase := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lowerBase, ".zip"):
		command = fmt.Sprintf("cd %s && unzip -o %s", safeDir, safeBase)
	case strings.HasSuffix(lowerBase, ".tar.gz") || strings.HasSuffix(lowerBase, ".tgz"):
		command = fmt.Sprintf("cd %s && tar -xzf %s", safeDir, safeBase)
	case strings.HasSuffix(lowerBase, ".tar"):
		command = fmt.Sprintf("cd %s && tar -xf %s", safeDir, safeBase)
	case strings.HasSuffix(lowerBase, ".tar.bz2") || strings.HasSuffix(lowerBase, ".tbz2"):
		command = fmt.Sprintf("cd %s && tar -xjf %s", safeDir, safeBase)
	case strings.HasSuffix(lowerBase, ".gz"):
		command = fmt.Sprintf("cd %s && gunzip -f -k %s", safeDir, safeBase)
	default:
		return fmt.Errorf("unsupported archive format")
	}
	output, err := s.backend.ExecuteCommand(context.Background(), client, command)
	if err != nil {
		return fmt.Errorf("uncompress uploaded archive failed: %w, output: %s", err, output)
	}
	return nil
}

func (s *Service) UploadLocalPathsCompressed(sessionId string, uploadID string, maxConcurrent int, localPaths []string, remoteDir string) error {
	paths := make([]string, 0, len(localPaths))
	for _, localPath := range localPaths {
		localPath = strings.TrimSpace(localPath)
		if localPath != "" {
			paths = append(paths, localPath)
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("no local paths")
	}

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), compressedUploadSessionIDKey, sessionId))
	task := &compressedUploadTask{
		id:        uploadID,
		sessionId: sessionId,
		cancel:    cancel,
	}
	var repairState *compressedUploadRepairState
	if err := s.registerCompressedUploadTask(uploadID, task); err != nil {
		cancel()
		return err
	}
	defer func() {
		cancel()
		task.cleanup(s)
		if repairState != nil {
			repairState.restore(s)
		}
		s.unregisterCompressedUploadTask(uploadID, task)
	}()

	limiter, err := s.acquireCompressedUploadSlot(sessionId, maxConcurrent, ctx)
	if err != nil {
		return err
	}
	defer s.releaseCompressedUploadSlot(sessionId, limiter)

	sshClient, _, err := s.backend.ClientEntry(sessionId)
	if err != nil {
		return err
	}

	s.emitCompressedUploadProgress(sessionId, uploadID, "preparing", 0, 0, 0, 0, "", fmt.Sprintf("%d paths", len(paths)))

	if len(paths) == 1 {
		info, err := os.Stat(paths[0])
		if err != nil {
			return err
		}
		if !info.IsDir() {
			fileName := filepath.Base(paths[0])
			s.emitCompressedUploadProgress(sessionId, uploadID, "uploading-file", 0, 0, 0, info.Size(), fileName, "")
			if err := s.uploadLocalFileWithContext(ctx, sshClient, maxConcurrent, paths[0], remoteDir, func(done int64, total int64) {
				phaseProgress := float64(100)
				if total > 0 {
					phaseProgress = float64(done) / float64(total) * 100
				}
				s.emitCompressedUploadProgress(sessionId, uploadID, "uploading-file", phaseProgress, phaseProgress, done, total, fileName, "")
			}); err != nil {
				return err
			}
			s.emitCompressedUploadProgress(sessionId, uploadID, "completed", 100, 100, info.Size(), info.Size(), fileName, "")
			return nil
		}
	}

	stats, err := collectLocalArchiveStats(paths)
	if err != nil {
		return err
	}
	archiveEntries, err := collectLocalArchiveEntries(paths)
	if err != nil {
		return err
	}
	s.emitCompressedUploadProgress(
		sessionId,
		uploadID,
		"scanning",
		0,
		100,
		0,
		stats.TotalBytes,
		"",
		fmt.Sprintf("%d files, %d directories", stats.FileCount, stats.DirCount),
	)

	archivePath, tempDir, err := createLocalTarGz(ctx, paths, stats, func(done int64, total int64, current string) {
		phaseProgress := float64(100)
		if total > 0 {
			phaseProgress = float64(done) / float64(total) * 100
		}
		s.emitCompressedUploadProgress(sessionId, uploadID, "compressing", phaseProgress*0.5, phaseProgress, done, total, current, "creating local tar.gz")
	})
	if err != nil {
		return err
	}
	task.setTempDir(tempDir)

	archiveSize := int64(0)
	if archiveInfo, statErr := os.Stat(archivePath); statErr == nil {
		archiveSize = archiveInfo.Size()
	}

	fileName := filepath.Base(archivePath)
	s.emitCompressedUploadProgress(sessionId, uploadID, "uploading", 50, 0, 0, archiveSize, fileName, "uploading local tar.gz")
	remoteArchive := filepath.ToSlash(filepath.Join(remoteDir, fileName))
	task.setRemoteArchive(remoteArchive)
	if err := s.uploadLocalFileWithContext(ctx, sshClient, maxConcurrent, archivePath, remoteDir, func(done int64, total int64) {
		phaseProgress := float64(100)
		if total > 0 {
			phaseProgress = float64(done) / float64(total) * 100
		}
		overall := 50 + phaseProgress*0.49
		s.emitCompressedUploadProgress(sessionId, uploadID, "uploading", overall, phaseProgress, done, total, fileName, "uploading local tar.gz")
	}); err != nil {
		return err
	}

	s.emitCompressedUploadProgress(sessionId, uploadID, "cleanup-local", 99, 0, 0, 0, fileName, "removing local temporary archive")
	_ = os.RemoveAll(tempDir)
	task.clearTempDir()

	if err := ensureCompressedUploadContext(ctx); err != nil {
		return err
	}
	s.emitCompressedUploadProgress(sessionId, uploadID, "verifying", 99, 0, 0, 0, "", "checking remote directories and existing files before extract")
	if err := s.preflightCompressedUploadTargets(ctx, sessionId, remoteDir, archiveEntries, func(done int, total int, current string, detail string) {
		phaseProgress := float64(0)
		if total > 0 {
			phaseProgress = float64(done) / float64(total) * 100
		}
		s.emitCompressedUploadProgress(sessionId, uploadID, "verifying", 99, phaseProgress, 0, 0, current, detail)
	}); err != nil {
		s.emitCompressedUploadProgress(sessionId, uploadID, "verifying", 99, 0, 0, 0, "", "preflight blocked extract, attempting automatic remote repair")
		repairedState, repairErr := s.autoRepairCompressedUploadTargets(sessionId, remoteDir, archiveEntries)
		if repairErr != nil {
			return fmt.Errorf("automatic remote repair failed\n%w", repairErr)
		}
		repairState = repairedState
		s.emitCompressedUploadProgress(sessionId, uploadID, "verifying", 99, 100, 0, 0, "", "automatic remote repair completed, continuing extract")
	}
	s.emitCompressedUploadProgress(sessionId, uploadID, "extracting", 99, 0, 0, 0, fileName, "extracting archive on remote server")
	if err := s.uncompressUploadedArchive(sessionId, remoteArchive); err != nil {
		_ = s.backend.DeleteRemote(context.Background(), sessionId, remoteArchive, false)
		task.clearRemoteArchive()
		return fmt.Errorf("remote extract failed after automatic repair\narchive: %s\nremote target: %s\nreason: %w", remoteArchive, remoteDir, err)
	}

	if err := ensureCompressedUploadContext(ctx); err != nil {
		return err
	}
	s.emitCompressedUploadProgress(sessionId, uploadID, "cleanup-remote", 99, 0, 0, 0, fileName, "removing remote archive")
	if err := s.backend.DeleteRemote(context.Background(), sessionId, remoteArchive, false); err != nil {
		return err
	}
	task.clearRemoteArchive()
	s.emitCompressedUploadProgress(sessionId, uploadID, "completed", 100, 100, 0, 0, "", "completed")
	return nil
}
