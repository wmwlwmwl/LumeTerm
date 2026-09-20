package externaledit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	externalEditDebounce     = 800 * time.Millisecond
	externalEditStableWait   = 200 * time.Millisecond
	externalEditEventStarted = "external-edit-started"
	externalEditEventSynced  = "external-edit-synced"
	externalEditEventError   = "external-edit-error"
	externalEditEventStopped = "external-edit-stopped"
)

type externalEditSession struct {
	key        string
	sessionID  string
	remotePath string
	localPath  string
	lastHash   string
	readOnly   bool
	maxBytes   int64
	cancel     chan struct{}
	watcher    *fsnotify.Watcher
}

type RemoteFiles interface {
	Size(sessionID string, remotePath string) (int64, error)
	Read(sessionID string, remotePath string) ([]byte, error)
	Write(sessionID string, remotePath string, data []byte) error
}

type EventSink interface {
	Emit(event string, payload map[string]interface{})
}

type Opener interface {
	OpenDefault(localPath string) error
	OpenWith(editorPath string, localPath string) error
}

type Manager struct {
	mu          sync.Mutex
	sessions    map[string]*externalEditSession
	remoteFiles RemoteFiles
	events      EventSink
	opener      Opener
}

func NewManager(remoteFiles RemoteFiles, events EventSink, opener Opener) *Manager {
	manager := &Manager{
		sessions:    make(map[string]*externalEditSession),
		remoteFiles: remoteFiles,
		events:      events,
		opener:      opener,
	}
	manager.pruneOrphanTemp(0)
	return manager
}

// pruneOrphanTemp removes temp dirs that are not tracked by active sessions.
// maxAge=0 means remove all untracked dirs immediately.
func (m *Manager) pruneOrphanTemp(maxAge time.Duration) {
	root, err := m.tempRoot()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	activeDirs := map[string]struct{}{}
	m.mu.Lock()
	for _, sess := range m.sessions {
		activeDirs[filepath.Dir(sess.localPath)] = struct{}{}
	}
	m.mu.Unlock()
	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(root, entry.Name())
		if _, ok := activeDirs[dirPath]; ok {
			continue
		}
		if maxAge > 0 {
			info, statErr := entry.Info()
			if statErr != nil {
				continue
			}
			if now.Sub(info.ModTime()) < maxAge {
				continue
			}
		}
		_ = os.RemoveAll(dirPath)
	}
}

func externalEditKey(sessionID, remotePath string) string {
	return strings.TrimSpace(sessionID) + "\x00" + strings.TrimSpace(remotePath)
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (m *Manager) tempRoot() (string, error) {
	root := filepath.Join(os.TempDir(), "lumeterm-external-edit")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

func (m *Manager) localPathFor(sessionID, remotePath string) (string, error) {
	root, err := m.tempRoot()
	if err != nil {
		return "", err
	}
	base := filepath.Base(strings.TrimSpace(remotePath))
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "untitled.txt"
	}
	// Keep original extension for editor language detection.
	hash := hashBytes([]byte(sessionID + "|" + remotePath))[:16]
	dir := filepath.Join(root, hash)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, base), nil
}

func (m *Manager) emit(event string, payload map[string]interface{}) {
	if m == nil || m.events == nil {
		return
	}
	m.events.Emit(event, payload)
}

func (m *Manager) Open(sessionID, remotePath, content, editorPath string, readOnly bool, maxBytes int64) (map[string]interface{}, error) {
	sessionID = strings.TrimSpace(sessionID)
	remotePath = strings.TrimSpace(remotePath)
	if sessionID == "" || remotePath == "" {
		return nil, fmt.Errorf("missing session or remote path")
	}
	if m.remoteFiles == nil || m.opener == nil {
		return nil, fmt.Errorf("external editor not ready")
	}
	// 兜底：调用方未传上限时用默认 5MB，避免 0 导致所有文件被拒。
	if maxBytes <= 0 {
		maxBytes = 5 * 1024 * 1024
	}

	var data []byte
	if content != "" {
		data = []byte(content)
	} else {
		// 下载前先 stat 拦截大文件，避免把整个大文件（如几 GB 的 mp4）读进内存后才报错。
		if size, err := m.remoteSize(sessionID, remotePath); err == nil && size > maxBytes {
			return nil, fmt.Errorf("文件过大 (%.1f MB)，最大支持 %.0f MB", float64(size)/(1024*1024), float64(maxBytes)/(1024*1024))
		}
		// 用原始字节而非 ReadFile：ReadFile 会经 b.String() 把非 UTF-8 字节（如 GBK
		// 编码的中文 lua/配置文件）强解为乱码，写入本地临时文件后编辑器打开即乱码。
		// 直接读原始字节，本地文件与远程字节一致，编辑器自己会做编码检测。
		rawBytes, err := m.remoteFiles.Read(sessionID, remotePath)
		if err != nil {
			return nil, err
		}
		data = rawBytes
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("文件过大 (%.1f MB)，最大支持 %.0f MB", float64(len(data))/(1024*1024), float64(maxBytes)/(1024*1024))
	}

	localPath, err := m.localPathFor(sessionID, remotePath)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(localPath, data, 0o600); err != nil {
		return nil, err
	}

	key := externalEditKey(sessionID, remotePath)
	m.mu.Lock()
	if existing, ok := m.sessions[key]; ok {
		m.stopSessionLocked(existing, false, true)
	}

	// readOnly（媒体类：只下载用系统程序查看，不监听修改、不回写远程）：
	// 不创建 watcher、不启动监听协程。会话仍登记到 sessions 以便统一 Stop/清理临时文件。
	var watcher *fsnotify.Watcher
	if !readOnly {
		w, werr := fsnotify.NewWatcher()
		if werr != nil {
			m.mu.Unlock()
			return nil, werr
		}
		watchDir := filepath.Dir(localPath)
		if err := w.Add(watchDir); err != nil {
			_ = w.Close()
			m.mu.Unlock()
			return nil, err
		}
		watcher = w
	}

	sess := &externalEditSession{
		key:        key,
		sessionID:  sessionID,
		remotePath: remotePath,
		localPath:  localPath,
		lastHash:   hashBytes(data),
		readOnly:   readOnly,
		maxBytes:   maxBytes,
		cancel:     make(chan struct{}),
		watcher:    watcher,
	}
	m.sessions[key] = sess
	m.mu.Unlock()

	if !readOnly {
		go m.watchSession(sess)
	}

	if strings.TrimSpace(editorPath) != "" {
		err = m.opener.OpenWith(editorPath, localPath)
	} else {
		err = m.opener.OpenDefault(localPath)
	}
	if err != nil {
		_ = m.Stop(sessionID, remotePath)
		return nil, fmt.Errorf("打开编辑器失败: %w", err)
	}

	payload := map[string]interface{}{
		"sessionId":  sessionID,
		"remotePath": remotePath,
		"localPath":  localPath,
		"readOnly":   readOnly,
	}
	m.emit(externalEditEventStarted, payload)
	return payload, nil
}

// remoteSize 返回远程文件大小（字节）；stat 失败时返回 0 和错误，调用方可忽略错误走原下载流程。
func (m *Manager) remoteSize(sessionID, remotePath string) (int64, error) {
	return m.remoteFiles.Size(sessionID, remotePath)
}

func (m *Manager) watchSession(sess *externalEditSession) {
	baseName := filepath.Base(sess.localPath)
	var (
		timerMu sync.Mutex
		timer   *time.Timer
	)
	schedule := func() {
		timerMu.Lock()
		defer timerMu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(externalEditDebounce, func() {
			m.trySync(sess)
		})
	}

	for {
		select {
		case <-sess.cancel:
			timerMu.Lock()
			if timer != nil {
				timer.Stop()
			}
			timerMu.Unlock()
			return
		case err, ok := <-sess.watcher.Errors:
			if !ok {
				return
			}
			if err != nil {
				m.emit(externalEditEventError, map[string]interface{}{
					"sessionId":  sess.sessionID,
					"remotePath": sess.remotePath,
					"error":      err.Error(),
				})
			}
		case event, ok := <-sess.watcher.Events:
			if !ok {
				return
			}
			name := filepath.Base(event.Name)
			// Match target file and common atomic-save side files that end up as the target.
			if name != baseName && !strings.HasPrefix(name, baseName) {
				// Some editors write "file.tmp" then rename to file; also watch rename targets.
				if event.Op&(fsnotify.Rename|fsnotify.Create|fsnotify.Write) == 0 {
					continue
				}
				// If rename creates our target path, still handle.
				if filepath.Clean(event.Name) != filepath.Clean(sess.localPath) {
					continue
				}
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
				schedule()
			}
		}
	}
}

func (m *Manager) trySync(sess *externalEditSession) {
	select {
	case <-sess.cancel:
		return
	default:
	}

	// Require two stable reads to avoid half-written files.
	data1, err := os.ReadFile(sess.localPath)
	if err != nil {
		return
	}
	time.Sleep(externalEditStableWait)
	select {
	case <-sess.cancel:
		return
	default:
	}
	data2, err := os.ReadFile(sess.localPath)
	if err != nil {
		return
	}
	if hashBytes(data1) != hashBytes(data2) {
		// Still changing; wait for next event / debounce.
		return
	}
	if int64(len(data2)) > sess.maxBytes {
		m.emit(externalEditEventError, map[string]interface{}{
			"sessionId":  sess.sessionID,
			"remotePath": sess.remotePath,
			"error":      fmt.Sprintf("本地文件过大 (%.1f MB)，跳过回传", float64(len(data2))/(1024*1024)),
		})
		return
	}

	nextHash := hashBytes(data2)
	m.mu.Lock()
	current, ok := m.sessions[sess.key]
	if !ok || current != sess {
		m.mu.Unlock()
		return
	}
	if current.lastHash == nextHash {
		m.mu.Unlock()
		return
	}
	current.lastHash = nextHash
	sessionID := current.sessionID
	remotePath := current.remotePath
	m.mu.Unlock()

	if err := m.remoteFiles.Write(sessionID, remotePath, data2); err != nil {
		m.emit(externalEditEventError, map[string]interface{}{
			"sessionId":  sessionID,
			"remotePath": remotePath,
			"error":      err.Error(),
		})
		return
	}
	m.emit(externalEditEventSynced, map[string]interface{}{
		"sessionId":  sessionID,
		"remotePath": remotePath,
		"bytes":      len(data2),
	})
}

func (m *Manager) stopSessionLocked(sess *externalEditSession, removeFiles bool, emit bool) {
	if sess == nil {
		return
	}
	select {
	case <-sess.cancel:
	default:
		close(sess.cancel)
	}
	if sess.watcher != nil {
		_ = sess.watcher.Close()
	}
	delete(m.sessions, sess.key)
	if removeFiles {
		_ = os.Remove(sess.localPath)
		_ = os.Remove(filepath.Dir(sess.localPath))
	}
	if emit {
		m.emit(externalEditEventStopped, map[string]interface{}{
			"sessionId":  sess.sessionID,
			"remotePath": sess.remotePath,
			"localPath":  sess.localPath,
		})
	}
}

func (m *Manager) Stop(sessionID, remotePath string) error {
	key := externalEditKey(sessionID, remotePath)
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[key]
	if !ok {
		return nil
	}
	m.stopSessionLocked(sess, true, true)
	return nil
}

func (m *Manager) StopSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, sess := range m.sessions {
		if sess.sessionID == sessionID {
			m.stopSessionLocked(sess, true, true)
			_ = key
		}
	}
}

func (m *Manager) StopSessionSilently(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sess := range m.sessions {
		if sess.sessionID == sessionID {
			m.stopSessionLocked(sess, true, false)
		}
	}
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	for _, sess := range m.sessions {
		m.stopSessionLocked(sess, true, true)
	}
	m.mu.Unlock()
	m.pruneOrphanTemp(0)
}

func (m *Manager) List() []map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]map[string]interface{}, 0, len(m.sessions))
	for _, sess := range m.sessions {
		out = append(out, map[string]interface{}{
			"sessionId":  sess.sessionID,
			"remotePath": sess.remotePath,
			"localPath":  sess.localPath,
		})
	}
	return out
}
