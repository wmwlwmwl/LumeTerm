package transfer

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// uploadChunkStallTimeout 是单个分片写入的最长等待时间。
// SFTP 的 WriteAt 没有超时参数，服务端活着但传输卡死时会永久阻塞前端上传请求；
// 超时后关闭该池 client 可让阻塞的写返回错误，并把该 client 标记为丢弃不再复用。
// 取 5min 而不是 120s：默认分片 256KiB、可配置到更大，慢链路（<40KB/s）下
// 单分片也可能超过 2 分钟，避免把「慢但正常」误判成卡死。
const uploadChunkStallTimeout = 5 * time.Minute

type sftpUploadPool struct {
	sshClient   *ssh.Client
	maxClients  int
	tuning      Tuning
	mu          sync.Mutex
	cond        *sync.Cond
	idleClients []*sftp.Client
	// closeFns 保存每个池 client 的「先关 SSH session、再关 sftp client」关闭函数。
	// 上传卡死时只有关闭 session 才能唤醒阻塞的 WriteAt；sftp.Client 自身拿不到
	// 底层 session，所以必须在创建时把 closeFn 一起存下来。
	closeFns       map[*sftp.Client]func()
	created        int
	closed         bool
	onChannelDelta func(int)
}

type chunkedUploadTask struct {
	sessionId     string
	remoteBaseDir string
	pool          *sftpUploadPool
	files         map[string]*chunkedUploadFile
	mu            sync.Mutex
}

type chunkedUploadFile struct {
	id            string
	relativePath  string
	finalPath     string
	tempPath      string
	size          int64
	totalChunks   int
	received      map[int]struct{}
	receivedCount int
	completed     bool
	aborted       bool
	mu            sync.Mutex
}

func buildTunedSFTPOptions(tuning Tuning) []sftp.ClientOption {
	tuning = NormalizeTuning(tuning)
	return []sftp.ClientOption{
		sftp.MaxPacketUnchecked(tuning.MaxPacketKiB * 1024),
		sftp.MaxConcurrentRequestsPerFile(WindowBoundedRequests(tuning)),
		sftp.UseConcurrentWrites(tuning.ConcurrentWrites),
	}
}

func NewSFTPClient(sshClient *ssh.Client, tuning Tuning) (*sftp.Client, error) {
	client, _, err := newPoolSFTPClient(sshClient, tuning)
	return client, err
}

// newPoolSFTPClient 与 NewSFTPClient 行为一致，但额外返回关闭函数（先关 session
// 再关 sftp client）。上传池必须持有它，否则卡死时 sftp.Client.Close() 会阻塞在
// 等待 recv goroutine 上，无法唤醒阻塞中的 WriteAt。
func newPoolSFTPClient(sshClient *ssh.Client, tuning Tuning) (*sftp.Client, func(), error) {
	tuning = NormalizeTuning(tuning)
	client, closeFn, err := openDedicatedSFTPClientWithOptions(sshClient, buildTunedSFTPOptions(tuning))
	if err == nil {
		return client, closeFn, nil
	}
	if tuning.MaxPacketKiB <= minMaxPacketKiB {
		return nil, nil, err
	}
	// 与旧行为一致：服务器不接受调优包长时退回默认选项。
	client, closeFn, fallbackErr := openDedicatedSFTPClientWithOptions(sshClient, nil)
	if fallbackErr != nil {
		return nil, nil, fmt.Errorf("%v; fallback: %w", err, fallbackErr)
	}
	return client, closeFn, nil
}

func newSFTPUploadPool(sshClient *ssh.Client, maxClients int, tuning Tuning) *sftpUploadPool {
	if maxClients < 1 {
		maxClients = 1
	}
	pool := &sftpUploadPool{
		sshClient:  sshClient,
		maxClients: maxClients,
		tuning:     NormalizeTuning(tuning),
		closeFns:   make(map[*sftp.Client]func()),
	}
	pool.cond = sync.NewCond(&pool.mu)
	return pool
}

func (p *sftpUploadPool) Acquire() (*sftp.Client, error) {
	for {
		p.mu.Lock()
		for !p.closed && len(p.idleClients) == 0 && p.created >= p.maxClients {
			p.cond.Wait()
		}
		if p.closed {
			p.mu.Unlock()
			return nil, fmt.Errorf("upload pool closed")
		}
		if idleCount := len(p.idleClients); idleCount > 0 {
			client := p.idleClients[idleCount-1]
			p.idleClients = p.idleClients[:idleCount-1]
			p.mu.Unlock()
			return client, nil
		}
		p.created++
		p.mu.Unlock()
		p.notifyChannelDelta(1)
		client, closeFn, err := newPoolSFTPClient(p.sshClient, p.tuning)
		if err == nil {
			p.mu.Lock()
			closed := p.closed
			if !closed {
				p.closeFns[client] = closeFn
			}
			p.mu.Unlock()
			if closed {
				closeFn()
				return nil, fmt.Errorf("upload pool closed")
			}
			return client, nil
		}
		p.mu.Lock()
		released := p.created > 0
		if released {
			p.created--
		}
		closed := p.closed
		p.cond.Broadcast()
		p.mu.Unlock()
		if released {
			p.notifyChannelDelta(-1)
		}
		if closed {
			return nil, fmt.Errorf("upload pool closed")
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "open failed") || strings.Contains(msg, "connect failed") || strings.Contains(msg, "rejected") {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		return nil, err
	}
}

func (p *sftpUploadPool) Release(client *sftp.Client) {
	p.releaseClient(client, false)
}

func (p *sftpUploadPool) Discard(client *sftp.Client) {
	p.releaseClient(client, true)
}

func (p *sftpUploadPool) notifyChannelDelta(delta int) {
	if p.onChannelDelta != nil && delta != 0 {
		p.onChannelDelta(delta)
	}
}

// closeClient 关闭池 client：优先用创建时保存的 closeFn（先关 SSH session，
// 让卡死的 WriteAt/Read 立刻返回），且不阻塞调用方。
func (p *sftpUploadPool) closeClient(client *sftp.Client) {
	if client == nil {
		return
	}
	p.mu.Lock()
	closeFn := p.closeFns[client]
	delete(p.closeFns, client)
	p.mu.Unlock()
	if closeFn != nil {
		closeFn()
		return
	}
	// 理论上不会走到这里；兜底也不要同步 Close，避免卡死调用方。
	go func() { _ = client.Close() }()
}

func (p *sftpUploadPool) releaseClient(client *sftp.Client, broken bool) {
	if client == nil {
		return
	}
	p.mu.Lock()
	if p.closed || broken {
		released := p.created > 0
		if released {
			p.created--
		}
		p.cond.Broadcast()
		p.mu.Unlock()
		p.closeClient(client)
		if released {
			p.notifyChannelDelta(-1)
		}
		return
	}
	p.idleClients = append(p.idleClients, client)
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *sftpUploadPool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	idleClients := append([]*sftp.Client(nil), p.idleClients...)
	p.idleClients = nil
	// 只清理 idle client 的 closeFn；仍被借出的 client 保留映射，
	// 等调用方 Discard/Release 时仍能走「先关 session」的正确路径。
	idleCloseFns := make(map[*sftp.Client]func(), len(idleClients))
	for _, client := range idleClients {
		if closeFn, ok := p.closeFns[client]; ok {
			idleCloseFns[client] = closeFn
			delete(p.closeFns, client)
		}
	}
	releasedCount := p.created
	p.created = 0
	p.mu.Unlock()
	for _, client := range idleClients {
		if closeFn := idleCloseFns[client]; closeFn != nil {
			closeFn()
			continue
		}
		go func(c *sftp.Client) { _ = c.Close() }(client)
	}
	p.cond.Broadcast()
	if releasedCount > 0 {
		p.notifyChannelDelta(-releasedCount)
	}
}

func newUploadObjectID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func normalizeRemoteUploadDir(remoteDir string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(remoteDir), "\\", "/")
	if normalized == "" {
		return "/"
	}
	cleaned := pathpkg.Clean(normalized)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

func sanitizeUploadRelativePath(raw string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if normalized == "" {
		return "", fmt.Errorf("empty relative path")
	}
	if strings.HasPrefix(normalized, "/") {
		return "", fmt.Errorf("absolute path is not allowed")
	}
	parts := strings.Split(normalized, "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid relative path")
		}
		cleaned = append(cleaned, part)
	}
	return strings.Join(cleaned, "/"), nil
}

func (s *Service) getChunkedUploadTask(taskID string) (*chunkedUploadTask, error) {
	s.chunkMu.Lock()
	task := s.chunkTasks[taskID]
	s.chunkMu.Unlock()
	if task == nil {
		return nil, fmt.Errorf("upload task not found")
	}
	return task, nil
}

func (s *Service) getChunkedUploadFile(taskID string, fileID string) (*chunkedUploadTask, *chunkedUploadFile, error) {
	task, err := s.getChunkedUploadTask(taskID)
	if err != nil {
		return nil, nil, err
	}
	task.mu.Lock()
	fileState := task.files[fileID]
	task.mu.Unlock()
	if fileState == nil {
		return nil, nil, fmt.Errorf("upload file not found")
	}
	return task, fileState, nil
}

func (s *Service) BeginChunkedUploadTask(sessionId string, remoteDir string, maxClients int) (string, error) {
	client, _, err := s.backend.ClientEntry(sessionId)
	if err != nil {
		return "", err
	}
	taskID := newUploadObjectID("upload_task")
	pool := newSFTPUploadPool(client, maxClients, s.Tuning())
	pool.onChannelDelta = func(delta int) {
		s.backend.UpdateUploadChannels(sessionId, delta)
	}
	task := &chunkedUploadTask{
		sessionId:     sessionId,
		remoteBaseDir: normalizeRemoteUploadDir(remoteDir),
		pool:          pool,
		files:         make(map[string]*chunkedUploadFile),
	}
	s.chunkMu.Lock()
	s.chunkTasks[taskID] = task
	s.chunkMu.Unlock()
	return taskID, nil
}

func (s *Service) BeginChunkedUploadFile(taskID string, relativePath string, size int64, totalChunks int) (string, error) {
	task, err := s.getChunkedUploadTask(taskID)
	if err != nil {
		return "", err
	}
	safeRelativePath, err := sanitizeUploadRelativePath(relativePath)
	if err != nil {
		return "", err
	}
	if size < 0 {
		return "", fmt.Errorf("invalid file size")
	}
	if totalChunks < 0 {
		return "", fmt.Errorf("invalid total chunks")
	}
	fileID := newUploadObjectID("upload_file")
	finalPath := pathpkg.Join(task.remoteBaseDir, safeRelativePath)
	tempPath := finalPath + ".lumetermpart." + fileID
	client, err := task.pool.Acquire()
	if err != nil {
		return "", err
	}
	defer task.pool.Release(client)
	if err := client.MkdirAll(pathpkg.Dir(finalPath)); err != nil {
		return "", err
	}
	_ = client.Remove(tempPath)
	fileHandle, err := client.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return "", err
	}
	if err := fileHandle.Close(); err != nil {
		return "", err
	}
	fileState := &chunkedUploadFile{
		id:            fileID,
		relativePath:  safeRelativePath,
		finalPath:     finalPath,
		tempPath:      tempPath,
		size:          size,
		totalChunks:   totalChunks,
		received:      make(map[int]struct{}),
		receivedCount: 0,
	}
	task.mu.Lock()
	task.files[fileID] = fileState
	task.mu.Unlock()
	return fileID, nil
}

func (s *Service) UploadChunkBase64(taskID string, fileID string, chunkIndex int, offset int64, base64Content string) error {
	task, fileState, err := s.getChunkedUploadFile(taskID, fileID)
	if err != nil {
		return err
	}
	if chunkIndex < 0 {
		return fmt.Errorf("invalid chunk index")
	}
	if fileState.totalChunks > 0 && chunkIndex >= fileState.totalChunks {
		return fmt.Errorf("invalid chunk index %d/%d", chunkIndex+1, fileState.totalChunks)
	}
	if offset < 0 {
		return fmt.Errorf("invalid chunk offset")
	}
	fileState.mu.Lock()
	if fileState.completed {
		fileState.mu.Unlock()
		return fmt.Errorf("upload file already completed")
	}
	if fileState.aborted {
		fileState.mu.Unlock()
		return fmt.Errorf("upload file aborted")
	}
	if _, exists := fileState.received[chunkIndex]; exists {
		fileState.mu.Unlock()
		return nil
	}
	fileState.mu.Unlock()
	content, err := base64.StdEncoding.DecodeString(base64Content)
	if err != nil {
		return fmt.Errorf("base64 decode failed: %w", err)
	}
	client, err := task.pool.Acquire()
	if err != nil {
		return err
	}
	clientBroken := false
	var stalled atomic.Bool
	stallFired := make(chan struct{})
	stallTimer := time.AfterFunc(uploadChunkStallTimeout, func() {
		defer close(stallFired)
		stalled.Store(true)
		// 先关 SSH session 再关 sftp client，唤醒阻塞中的 WriteAt/Close；
		// 该 client 不再复用。
		task.pool.closeClient(client)
	})
	defer func() {
		// Stop 返回 false 说明回调已触发或正在触发；等它把 stalled 置位，
		// 避免把「即将被关闭」的 client 放回 idle 池。
		if !stallTimer.Stop() {
			<-stallFired
		}
		if clientBroken || stalled.Load() {
			task.pool.Discard(client)
			return
		}
		task.pool.Release(client)
	}()
	fileHandle, err := client.OpenFile(fileState.tempPath, os.O_CREATE|os.O_WRONLY)
	if err != nil {
		clientBroken = true
		if stalled.Load() {
			return fmt.Errorf("upload chunk stalled for %s", uploadChunkStallTimeout)
		}
		return err
	}
	written, writeErr := fileHandle.WriteAt(content, offset)
	closeErr := fileHandle.Close()
	if stalled.Load() {
		clientBroken = true
		return fmt.Errorf("upload chunk stalled for %s", uploadChunkStallTimeout)
	}
	if writeErr != nil {
		clientBroken = true
		return writeErr
	}
	if closeErr != nil {
		clientBroken = true
		return closeErr
	}
	if written != len(content) {
		clientBroken = true
		return io.ErrShortWrite
	}
	fileState.mu.Lock()
	if _, exists := fileState.received[chunkIndex]; !exists {
		fileState.received[chunkIndex] = struct{}{}
		fileState.receivedCount++
	}
	fileState.mu.Unlock()
	return nil
}

func (s *Service) CompleteChunkedUploadFile(taskID string, fileID string) error {
	task, fileState, err := s.getChunkedUploadFile(taskID, fileID)
	if err != nil {
		return err
	}
	fileState.mu.Lock()
	if fileState.aborted {
		fileState.mu.Unlock()
		return fmt.Errorf("upload file aborted")
	}
	if fileState.completed {
		fileState.mu.Unlock()
		return nil
	}
	if !(fileState.size == 0 && fileState.totalChunks == 0) {
		missing := make([]string, 0, 8)
		for i := 0; i < fileState.totalChunks && len(missing) < 8; i++ {
			if _, ok := fileState.received[i]; !ok {
				missing = append(missing, fmt.Sprintf("%d", i+1))
			}
		}
		if len(missing) > 0 || len(fileState.received) < fileState.totalChunks {
			receivedUnique := len(fileState.received)
			fileState.mu.Unlock()
			return fmt.Errorf("upload file incomplete: received %d/%d chunks, missing [%s]", receivedUnique, fileState.totalChunks, strings.Join(missing, ", "))
		}
	}
	fileState.completed = true
	fileState.mu.Unlock()
	client, err := task.pool.Acquire()
	if err != nil {
		fileState.mu.Lock()
		fileState.completed = false
		fileState.mu.Unlock()
		return err
	}
	clientBroken := false
	defer func() {
		if clientBroken {
			task.pool.Discard(client)
			return
		}
		task.pool.Release(client)
	}()
	fileHandle, err := client.OpenFile(fileState.tempPath, os.O_WRONLY)
	if err != nil {
		clientBroken = true
		fileState.mu.Lock()
		fileState.completed = false
		fileState.mu.Unlock()
		return err
	}
	truncateErr := fileHandle.Truncate(fileState.size)
	closeErr := fileHandle.Close()
	if truncateErr != nil {
		clientBroken = true
		fileState.mu.Lock()
		fileState.completed = false
		fileState.mu.Unlock()
		return truncateErr
	}
	if closeErr != nil {
		clientBroken = true
		fileState.mu.Lock()
		fileState.completed = false
		fileState.mu.Unlock()
		return closeErr
	}
	if err := client.PosixRename(fileState.tempPath, fileState.finalPath); err != nil {
		if renameErr := client.Rename(fileState.tempPath, fileState.finalPath); renameErr != nil {
			fileState.mu.Lock()
			fileState.completed = false
			fileState.mu.Unlock()
			return renameErr
		}
	}
	task.mu.Lock()
	delete(task.files, fileID)
	task.mu.Unlock()
	return nil
}

func (s *Service) AbortChunkedUploadFile(taskID string, fileID string) error {
	task, fileState, err := s.getChunkedUploadFile(taskID, fileID)
	if err != nil {
		return err
	}
	fileState.mu.Lock()
	fileState.aborted = true
	fileState.mu.Unlock()
	client, acquireErr := task.pool.Acquire()
	if acquireErr == nil {
		_ = client.Remove(fileState.tempPath)
		task.pool.Release(client)
	}
	task.mu.Lock()
	delete(task.files, fileID)
	task.mu.Unlock()
	if acquireErr != nil {
		return acquireErr
	}
	return nil
}

func (s *Service) FinishChunkedUploadTask(taskID string) error {
	s.chunkMu.Lock()
	task := s.chunkTasks[taskID]
	delete(s.chunkTasks, taskID)
	s.chunkMu.Unlock()
	if task == nil {
		return nil
	}
	task.pool.Close()
	return nil
}

func (s *Service) abortChunkedUploadTaskOnDisconnect(taskID string) {
	s.chunkMu.Lock()
	task := s.chunkTasks[taskID]
	delete(s.chunkTasks, taskID)
	s.chunkMu.Unlock()
	if task == nil {
		return
	}
	task.mu.Lock()
	for _, fileState := range task.files {
		fileState.mu.Lock()
		fileState.aborted = true
		fileState.mu.Unlock()
	}
	task.files = make(map[string]*chunkedUploadFile)
	task.mu.Unlock()
	// 断线时不能等待 Acquire 后再删远端临时文件；先关 pool 唤醒等待者，
	// 活跃 client 会由随后关闭的 SSH transport 中断。
	task.pool.Close()
}

func (s *Service) AbortChunkedUploadTask(taskID string) error {
	s.chunkMu.Lock()
	task := s.chunkTasks[taskID]
	delete(s.chunkTasks, taskID)
	s.chunkMu.Unlock()
	if task == nil {
		return nil
	}
	task.mu.Lock()
	tempPaths := make([]string, 0, len(task.files))
	for _, fileState := range task.files {
		fileState.mu.Lock()
		fileState.aborted = true
		fileState.mu.Unlock()
		tempPaths = append(tempPaths, fileState.tempPath)
	}
	task.files = make(map[string]*chunkedUploadFile)
	task.mu.Unlock()
	client, err := task.pool.Acquire()
	if err == nil {
		for _, tempPath := range tempPaths {
			_ = client.Remove(tempPath)
		}
		task.pool.Release(client)
	}
	task.pool.Close()
	return err
}
