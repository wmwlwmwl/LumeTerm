package sshmanager

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// 文件操作取消注册表：给「右键压缩/解压/预览、递归 chmod、删除大目录」这类
// 同步等待的远端长操作提供取消入口。
//
// 背景：这些操作以前用 context.Background() + 30min 超时，用户点错大目录只能干等。
// 前端给每次操作生成 opID，经 Wails binding 传入；取消时调用 AbortFileOperation(opID)。
// 取消是 best-effort：先向远端进程组发 SIGTERM 并最多等 remoteCmdCancelGrace，再关闭
// session。OpenSSH 对非受限会话是用 killpg 终止整组（tar/unzip/rm 会一起停），但
// ForceCommand/subsystem 会话会拒绝 signal，非 OpenSSH 服务端和 Windows 远端可能
// 直接忽略，因此远端仍有可能继续执行到结束（见 docs/TIMEOUT-POLICY.md）。

// fileOperation 表示一个在途的可取消文件操作。
type fileOperation struct {
	id        string
	sessionID string
	cancel    context.CancelFunc
	startedAt time.Time
}

// fileOperationRegistry 同时按 opID 和 sessionID 建索引：
// 按 opID 用于精确取消，按 sessionID 用于连接断开时统一回收。
type fileOperationRegistry struct {
	mu        sync.Mutex
	byID      map[string]*fileOperation
	bySession map[string]map[string]*fileOperation
}

func newFileOperationRegistry() *fileOperationRegistry {
	return &fileOperationRegistry{
		byID:      make(map[string]*fileOperation),
		bySession: make(map[string]map[string]*fileOperation),
	}
}

// begin 注册一个可取消文件操作，返回 ctx 与 done（调用方必须 defer done()）。
func (r *fileOperationRegistry) begin(sessionID string, opID string) (context.Context, func(), error) {
	if sessionID == "" {
		return nil, nil, fmt.Errorf("missing session id")
	}
	if opID == "" {
		return nil, nil, fmt.Errorf("missing file operation id")
	}
	ctx, cancel := context.WithCancel(context.Background())
	op := &fileOperation{
		id:        opID,
		sessionID: sessionID,
		cancel:    cancel,
		startedAt: time.Now(),
	}

	r.mu.Lock()
	if _, exists := r.byID[opID]; exists {
		r.mu.Unlock()
		cancel()
		return nil, nil, fmt.Errorf("file operation %q is already running", opID)
	}
	r.byID[opID] = op
	if r.bySession[sessionID] == nil {
		r.bySession[sessionID] = make(map[string]*fileOperation)
	}
	r.bySession[sessionID][opID] = op
	r.mu.Unlock()

	done := func() { r.finish(opID) }
	return ctx, done, nil
}

// finish 注销操作（不取消 ctx：正常结束时调用方已经持有结果）。
func (r *fileOperationRegistry) finish(opID string) {
	r.mu.Lock()
	op := r.byID[opID]
	if op != nil {
		delete(r.byID, opID)
		if set := r.bySession[op.sessionID]; set != nil {
			delete(set, opID)
			if len(set) == 0 {
				delete(r.bySession, op.sessionID)
			}
		}
	}
	r.mu.Unlock()
	if op != nil {
		op.cancel()
	}
}

// abort 取消指定操作；不存在时返回错误（前端可能重复点取消）。
func (r *fileOperationRegistry) abort(opID string) error {
	r.mu.Lock()
	op := r.byID[opID]
	r.mu.Unlock()
	if op == nil {
		return fmt.Errorf("file operation %q is not running", opID)
	}
	op.cancel()
	return nil
}

// abortSession 取消某个 session 的全部在途文件操作，返回取消数量。
// 用于连接断开/会话清理，避免远端命令继续占用 session。
func (r *fileOperationRegistry) abortSession(sessionID string) int {
	r.mu.Lock()
	ops := make([]*fileOperation, 0, len(r.bySession[sessionID]))
	for _, op := range r.bySession[sessionID] {
		ops = append(ops, op)
	}
	r.mu.Unlock()
	for _, op := range ops {
		op.cancel()
	}
	return len(ops)
}

// activeIDs 返回某 session 在途操作的 id 列表（测试/诊断用）。
func (r *fileOperationRegistry) activeIDs(sessionID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.bySession[sessionID]))
	for id := range r.bySession[sessionID] {
		ids = append(ids, id)
	}
	return ids
}

func (r *fileOperationRegistry) activeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byID)
}

// ─── SSHManager 对外接口 ──────────────────────────────────

// BeginFileOperation 注册一个可取消文件操作；调用方必须 defer done()。
func (m *SSHManager) BeginFileOperation(sessionID string, opID string) (context.Context, func(), error) {
	if m == nil || m.fileOps == nil {
		return nil, nil, fmt.Errorf("file operation registry unavailable")
	}
	return m.fileOps.begin(sessionID, opID)
}

// AbortFileOperation 取消指定 opID 的文件操作（用户点「取消」时调用）。
func (m *SSHManager) AbortFileOperation(opID string) error {
	if m == nil || m.fileOps == nil {
		return fmt.Errorf("file operation registry unavailable")
	}
	return m.fileOps.abort(opID)
}

// abortSessionFileOperations 在连接断开/会话关闭时统一取消。
func (m *SSHManager) abortSessionFileOperations(sessionID string) int {
	if m == nil || m.fileOps == nil {
		return 0
	}
	return m.fileOps.abortSession(sessionID)
}
