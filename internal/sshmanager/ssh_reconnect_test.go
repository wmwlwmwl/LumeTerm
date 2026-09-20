package sshmanager

import (
	"errors"
	"sync"
	"testing"
	"time"

	"lumeterm/internal/config"
)

// TestRecordAndLookupDisconnectedSession 验证整机断开后记录可按父/子会话 id 查到。
func TestRecordAndLookupDisconnectedSession(t *testing.T) {
	manager := NewSSHManager()
	manager.recordDisconnectedConn("srv-1", []string{"root", "term_a", "term_b"}, "root", "keepalive")

	record, ok := manager.LookupDisconnectedSession("root")
	if !ok || record.ParentSessionId != "root" || record.ConnKey != "srv-1" || record.Reason != "keepalive" {
		t.Fatalf("按父会话 id 查询断连记录失败: ok=%v record=%+v", ok, record)
	}
	if record, ok = manager.LookupDisconnectedSession("term_b"); !ok || record.ParentSessionId != "root" {
		t.Fatalf("按子终端 id 查询断连记录失败: ok=%v record=%+v", ok, record)
	}
	if _, ok = manager.LookupDisconnectedSession("unknown"); ok {
		t.Fatal("未知会话不应命中断连记录")
	}
}

// TestLookupStaleRecordWhenReconnected 会话重连(如由前端完成)后,断连记录应视为失效。
func TestLookupStaleRecordWhenReconnected(t *testing.T) {
	manager := NewSSHManager()
	manager.recordDisconnectedConn("srv-1", []string{"root"}, "root", "transport")
	manager.mu.Lock()
	manager.sessions["root"] = &SessionData{ConnKey: "srv-1"}
	manager.mu.Unlock()

	if _, ok := manager.LookupDisconnectedSession("root"); ok {
		t.Fatal("会话已重连后不应再命中断连记录")
	}
	manager.mu.RLock()
	_, remains := manager.recentDisconnects["root"]
	manager.mu.RUnlock()
	if remains {
		t.Fatal("失效记录应被清理")
	}
}

// TestReconnectIdempotentForAliveSession 对仍在线的会话调用重连应幂等成功。
func TestReconnectIdempotentForAliveSession(t *testing.T) {
	manager := NewSSHManager()
	manager.mu.Lock()
	manager.sessions["root"] = &SessionData{ConnKey: "srv-1"}
	manager.clients["srv-1"] = &sshClientEntry{}
	manager.mu.Unlock()

	outcome, err := manager.ReconnectDisconnectedSession("root")
	if err != nil {
		t.Fatalf("在线会话重连应幂等成功: %v", err)
	}
	if !outcome.AlreadyConnected || outcome.SessionId != "root" {
		t.Fatalf("意外的幂等结果: %+v", outcome)
	}
}

// TestReconnectUnavailableWithoutRecord 无断连记录时返回哨兵错误。
func TestReconnectUnavailableWithoutRecord(t *testing.T) {
	manager := NewSSHManager()
	if _, err := manager.ReconnectDisconnectedSession("ghost"); !errors.Is(err, ErrReconnectUnavailable) {
		t.Fatalf("应返回 ErrReconnectUnavailable, 实际: %v", err)
	}
}

// TestDisconnectClearsRecord 用户主动断开后,断连记录与失败计数应被清除。
func TestDisconnectClearsRecord(t *testing.T) {
	manager := NewSSHManager()
	manager.recordDisconnectedConn("srv-1", []string{"root", "term_a"}, "root", "transport")
	manager.mu.Lock()
	manager.mcpReconnectFailures["root"] = 2
	manager.mu.Unlock()

	manager.Disconnect("term_a")

	if _, ok := manager.LookupDisconnectedSession("root"); ok {
		t.Fatal("主动断开后断连记录应被清除")
	}
	manager.mu.RLock()
	fails := manager.mcpReconnectFailures["root"]
	manager.mu.RUnlock()
	if fails != 0 {
		t.Fatalf("主动断开后失败计数应被清除, 实际: %d", fails)
	}
}

// TestRecordReplacesStaleRecordForSameSession 同一会话反复断开时只保留最新记录。
func TestRecordReplacesStaleRecordForSameSession(t *testing.T) {
	manager := NewSSHManager()
	manager.recordDisconnectedConn("srv-1", []string{"root", "term_a"}, "root", "transport")
	manager.recordDisconnectedConn("srv-1", []string{"root", "term_a", "term_b"}, "root", "keepalive")

	manager.mu.RLock()
	count := len(manager.recentDisconnects)
	record := manager.recentDisconnects["root"]
	manager.mu.RUnlock()
	if count != 1 {
		t.Fatalf("同一会话重复断开应只保留一条记录, 实际: %d", count)
	}
	if record == nil || len(record.SessionIds) != 3 || record.Reason != "keepalive" {
		t.Fatalf("应保留最新现场: %+v", record)
	}
}

// TestReconnectRecordsCapped 断连记录数量应受上限约束(FIFO 淘汰)。
func TestReconnectRecordsCapped(t *testing.T) {
	manager := NewSSHManager()
	for i := 0; i < mcpReconnectMaxRecords+5; i++ {
		parent := "parent-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		manager.recordDisconnectedConn("srv", []string{parent}, parent, "transport")
	}
	manager.mu.RLock()
	count := len(manager.recentDisconnects)
	manager.mu.RUnlock()
	if count > mcpReconnectMaxRecords {
		t.Fatalf("断连记录应不超过 %d 条, 实际: %d", mcpReconnectMaxRecords, count)
	}
}

// TestEvictionClearsFailureCounts 记录被覆盖或 FIFO 淘汰的会话,其连续失败计数应一并清除,
// 防止残留计数使该会话后续再次断连时提前触发用户提醒。
func TestEvictionClearsFailureCounts(t *testing.T) {
	manager := NewSSHManager()
	// 填满上限,最早的记录将被后续新记录淘汰
	for i := 0; i < mcpReconnectMaxRecords; i++ {
		parent := "parent-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		manager.recordDisconnectedConn("srv", []string{parent}, parent, "transport")
	}
	manager.mu.Lock()
	// 显式把最早插入的记录时间调早,保证 FIFO 淘汰的一定是它(同一周期内时间戳可能相等)
	if rec := manager.recentDisconnects["parent-a0"]; rec != nil {
		rec.ClosedAt = time.Now().Add(-time.Hour)
	}
	manager.mcpReconnectFailures["parent-a0"] = 3
	manager.mu.Unlock()
	// 再登记一条,触发 FIFO 淘汰最早的记录
	manager.recordDisconnectedConn("srv", []string{"newest"}, "newest", "transport")

	manager.mu.RLock()
	fails, hasFail := manager.mcpReconnectFailures["parent-a0"]
	_, hasRecord := manager.recentDisconnects["parent-a0"]
	manager.mu.RUnlock()
	if hasRecord {
		t.Fatal("最旧记录应已被 FIFO 淘汰")
	}
	if hasFail || fails != 0 {
		t.Fatalf("被淘汰会话的失败计数应被清除, 残留: %d", fails)
	}

	// 同 parent 再次断开:旧记录被替换(overlap 清理),失败计数同样应被清掉
	manager.recordDisconnectedConn("srv", []string{"root", "term_a"}, "root", "transport")
	manager.mu.Lock()
	manager.mcpReconnectFailures["root"] = 2
	manager.mu.Unlock()
	manager.recordDisconnectedConn("srv", []string{"root", "term_a"}, "root", "keepalive")

	manager.mu.RLock()
	fails, hasFail = manager.mcpReconnectFailures["root"]
	if _, ok := manager.recentDisconnects["root"]; !ok {
		manager.mu.RUnlock()
		t.Fatal("同 parent 再次断开应保留最新记录")
	}
	manager.mu.RUnlock()
	if hasFail || fails != 0 {
		t.Fatalf("记录被替换后失败计数应被清除, 残留: %d", fails)
	}
}

// TestConcurrentReconnectSingleRecovery 并发 reconnect_server 同一断连会话时,
// 只执行一次完整恢复(单次拨号/重开子终端),其余调用幂等命中 AlreadyConnected,
// 不重复创建子终端(回归:per-connKey 序列锁覆盖整个恢复序列)。
func TestConcurrentReconnectSingleRecovery(t *testing.T) {
	host, port, hostKeyLine, cleanup := newCycleTestServer(t)
	defer cleanup()
	manager := setupCycleTestManager(t, host, port, hostKeyLine)
	// 隔离配置目录,避免测试写入真实用户配置
	tmp := t.TempDir()
	t.Setenv("APPDATA", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	cm := config.NewConfigManager()
	cm.SaveConnection(Connection{ID: "srv-1", Username: "test", Host: host, Port: port}, true)
	manager.SetConfigManager(cm)

	// 整机断开现场:parent=root,子终端 term_a/term_b
	manager.recordDisconnectedConn("srv-1", []string{"root", "term_a", "term_b"}, "root", "transport")

	const n = 8
	var wg sync.WaitGroup
	results := make([]ReconnectOutcome, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = manager.ReconnectDisconnectedSession("root")
		}(i)
	}
	wg.Wait()

	// 恰好一次完整恢复(非 AlreadyConnected),其余全部幂等成功
	recovered := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("调用 %d 不应失败: %v", i, errs[i])
		}
		if !results[i].AlreadyConnected {
			recovered++
		}
	}
	if recovered != 1 {
		t.Fatalf("完整恢复应恰好执行 1 次, 实际: %d", recovered)
	}
	// 终端不重复:parent(1) + 重开的 2 个子终端 = 3;若无序列锁会翻倍
	manager.mu.RLock()
	terms := append([]string(nil), manager.connTerminals["srv-1"]...)
	fails := manager.mcpReconnectFailures["root"]
	manager.mu.RUnlock()
	if len(terms) != 3 {
		t.Fatalf("恢复后终端数应 = 3(parent+2 子), 实际: %d (%v) —— 疑似重复重开子终端", len(terms), terms)
	}
	if fails != 0 {
		t.Fatalf("成功重连后失败计数应为 0, 实际: %d", fails)
	}
}
