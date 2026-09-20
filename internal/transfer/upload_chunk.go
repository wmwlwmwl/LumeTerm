package transfer

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type sftpUploadPool struct {
	sshClient      *ssh.Client
	maxClients     int
	tuning         Tuning
	mu             sync.Mutex
	cond           *sync.Cond
	idleClients    []*sftp.Client
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
	tuning = NormalizeTuning(tuning)
	client, err := sftp.NewClient(sshClient, buildTunedSFTPOptions(tuning)...)
	if err == nil {
		return client, nil
	}
	if tuning.MaxPacketKiB <= minMaxPacketKiB {
		return nil, err
	}
	return sftp.NewClient(sshClient)
}

func newSFTPUploadPool(sshClient *ssh.Client, maxClients int, tuning Tuning) *sftpUploadPool {
	if maxClients < 1 {
		maxClients = 1
	}
	pool := &sftpUploadPool{
		sshClient:  sshClient,
		maxClients: maxClients,
		tuning:     NormalizeTuning(tuning),
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
		client, err := NewSFTPClient(p.sshClient, p.tuning)
		if err == nil {
			p.mu.Lock()
			closed := p.closed
			p.mu.Unlock()
			if closed {
				_ = client.Close()
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
		_ = client.Close()
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
	releasedCount := p.created
	p.created = 0
	p.mu.Unlock()
	for _, client := range idleClients {
		_ = client.Close()
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
	defer func() {
		if clientBroken {
			task.pool.Discard(client)
			return
		}
		task.pool.Release(client)
	}()
	fileHandle, err := client.OpenFile(fileState.tempPath, os.O_CREATE|os.O_WRONLY)
	if err != nil {
		clientBroken = true
		return err
	}
	written, writeErr := fileHandle.WriteAt(content, offset)
	closeErr := fileHandle.Close()
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
