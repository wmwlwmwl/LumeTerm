package config

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	aitypes "lumeterm/internal/aitypes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type memoryStorage struct {
	files    map[string][]byte
	readErr  map[string]error
	listErr  error
	writeErr error
	writes   []string
	deletes  []string
}

// 备份文件识别兼容：新 .lumeterm2 与旧 .lumin2 并存（品牌改名过渡期）
func TestIsBackupNameMixedExtensions(t *testing.T) {
	cases := map[string]bool{
		"connections_backup_20260101_000000.000_+0000.lumeterm2": true,
		"connections_backup_20260101_000000.000_+0000.lumin2":    true,
		"connections_backup_20260101_000000.000_+0000.json":      true,
		"connections_backup_note.txt":                            false,
		"lumeterm.png":                                           false,
	}
	for name, want := range cases {
		if got := isBackupName(name); got != want {
			t.Fatalf("isBackupName(%q)=%v want %v", name, got, want)
		}
	}
}

func (s *memoryStorage) ListFiles() ([]RemoteFile, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	files := make([]RemoteFile, 0, len(s.files))
	for name, data := range s.files {
		files = append(files, RemoteFile{Name: name, Size: int64(len(data)), ModTime: time.Now()})
	}
	return files, nil
}
func (s *memoryStorage) ReadFile(name string) ([]byte, error) {
	if err := s.readErr[name]; err != nil {
		return nil, err
	}
	return s.files[name], nil
}
func (s *memoryStorage) WriteFile(name string, data []byte) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	if s.files == nil {
		s.files = map[string][]byte{}
	}
	s.files[name] = append([]byte(nil), data...)
	s.writes = append(s.writes, name)
	return nil
}
func (s *memoryStorage) DeleteFile(name string) error {
	delete(s.files, name)
	s.deletes = append(s.deletes, name)
	return nil
}
func testSyncManager(t *testing.T) *ConfigManager {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	historyDir := filepath.Join(dir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		t.Fatal(err)
	}
	return &ConfigManager{
		configDir:            dir,
		connFile:             filepath.Join(dir, "connections.json"),
		credFile:             filepath.Join(dir, "credentials.json"),
		quickCmdFile:         filepath.Join(dir, "quick_commands.json"),
		syncTimeFile:         filepath.Join(dir, "snapshot_time"),
		lastSyncFile:         filepath.Join(dir, "last_sync_time"),
		tombstoneFile:        filepath.Join(dir, "sync_tombstones.json"),
		recoveryPasswordFile: filepath.Join(dir, "recovery_password"),
		syncModeFile:         filepath.Join(dir, "sync_mode.json"),
		// 必须与生产一致：history 子目录。若 historyDir==configDir，
		// CleanupOrphanedHistory 会把 sync_tombstones.json 等配置文件当孤儿历史删掉。
		historyDir: historyDir,
		key:        key,
		gcm:        gcm,
	}
}

func encryptedSnapshot(t *testing.T, password, id string) []byte {
	t.Helper()
	return encryptedSyncSnapshot(t, password, &SyncSnapshot{Connections: []Connection{{ID: id, Host: id, LastModified: 1}}})
}

func encryptedSyncSnapshot(t *testing.T, password string, snap *SyncSnapshot) []byte {
	t.Helper()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := encryptLUMETERM2(string(data), password)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(encrypted)
}

func TestFetchLatestBackupStrictlyFailsOnNewest(t *testing.T) {
	cm := testSyncManager(t)
	s := &memoryStorage{files: map[string][]byte{
		"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSnapshot(t, "正确", "old"),
		"connections_backup_20260102_000000.000_+0000.lumin2": encryptedSnapshot(t, "其他", "new"),
	}}
	_, err := cm.fetchLatestBackup(s, "正确")
	if !errors.Is(err, errRecoveryPassword) {
		t.Fatalf("最新备份密码错误应严格失败且可分类，得到：%v", err)
	}
}

func TestChangeRecoveryPasswordWithoutProvidersOnlyPersistsLocally(t *testing.T) {
	cm := testSyncManager(t)
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) { return nil, nil }
	if err := cm.ChangeRecoveryPassword("仅本地密码"); err != nil {
		t.Fatal(err)
	}
	if got := cm.GetRecoveryPassword(); got != "仅本地密码" {
		t.Fatalf("无云端时应允许仅设置本地密码，得到：%q", got)
	}
	if err := cm.ChangeRecoveryPassword(""); err != nil {
		t.Fatal(err)
	}
	if got := cm.GetRecoveryPassword(); got != "" {
		t.Fatalf("无云端时应允许清除本地密码，得到：%q", got)
	}
}

func TestChangeRecoveryPasswordWrongPasswordWritesNothing(t *testing.T) {
	cm := testSyncManager(t)
	if err := cm.SetRecoveryPassword("旧密码"); err != nil {
		t.Fatal(err)
	}
	s := &memoryStorage{files: map[string][]byte{
		"connections_backup_20260102_000000.000_+0000.lumin2": encryptedSnapshot(t, "第三个密码", "remote"),
	}}
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: s}}, nil
	}
	err := cm.ChangeRecoveryPassword("新密码")
	if !errors.Is(err, errRecoveryPasswordResetRequired) {
		t.Fatalf("旧、新密码都错误时应返回稳定重置分类，得到：%v", err)
	}
	if errors.Is(err, errRecoveryPassword) {
		t.Fatalf("稳定重置分类不应伪装成普通密码错误：%v", err)
	}
	if len(s.writes) != 0 || len(s.deletes) != 0 {
		t.Fatalf("密码预检失败不得写远端：writes=%v deletes=%v", s.writes, s.deletes)
	}
	if got := cm.GetRecoveryPassword(); got != "旧密码" {
		t.Fatalf("密码预检失败不得改本地密码，得到：%q", got)
	}
	if _, err := os.Stat(cm.connFile); !os.IsNotExist(err) {
		t.Fatalf("密码预检失败不得写本地同步数据：%v", err)
	}
}

func TestChangeRecoveryPasswordDoesNotClassifyNonPasswordError(t *testing.T) {
	cm := testSyncManager(t)
	if err := cm.SetRecoveryPassword("旧密码"); err != nil {
		t.Fatal(err)
	}
	networkErr := errors.New("网络中断")
	s := &memoryStorage{files: map[string][]byte{}, readErr: map[string]error{}, writeErr: nil}
	s.readErr["connections_backup_20260101_000000.000_+0000.lumin2"] = networkErr
	s.files["connections_backup_20260101_000000.000_+0000.lumin2"] = encryptedSnapshot(t, "旧密码", "remote")
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: s}}, nil
	}
	err := cm.ChangeRecoveryPassword("新密码")
	if !errors.Is(err, networkErr) {
		t.Fatalf("网络错误应保持错误链，得到：%v", err)
	}
	if errors.Is(err, errRecoveryPasswordResetRequired) {
		t.Fatalf("网络错误不得分类为强制重置：%v", err)
	}
}

func TestChangeRecoveryPasswordAllFailsClosed(t *testing.T) {
	cm := testSyncManager(t)
	if err := cm.SetRecoveryPassword("旧密码"); err != nil {
		t.Fatal(err)
	}
	first := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSnapshot(t, "旧密码", "one")}}
	second := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSnapshot(t, "错误", "two")}}
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: first}, {provider: "r2", storage: second}}, nil
	}
	if err := cm.ChangeRecoveryPassword("新密码"); err == nil {
		t.Fatal("all 模式任一预检失败应整体失败")
	}
	if len(first.writes)+len(second.writes) != 0 {
		t.Fatalf("all 预检失败应零远端写入：%v %v", first.writes, second.writes)
	}
	if _, err := os.Stat(cm.connFile); !os.IsNotExist(err) {
		t.Fatalf("all 预检失败应零本地写入：%v", err)
	}
}

func TestChangeRecoveryPasswordRollsBackUploadsWhenLocalPersistFails(t *testing.T) {
	cm := testSyncManager(t)
	if err := cm.SetRecoveryPassword("旧密码"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(cm.quickCmdFile, 0700); err != nil {
		t.Fatal(err)
	}
	s := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSnapshot(t, "旧密码", "one")}}
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: s}}, nil
	}
	if err := cm.ChangeRecoveryPassword("新密码"); err == nil {
		t.Fatal("本地持久化失败应返回错误")
	}
	if len(s.writes) != 1 || len(s.deletes) != 1 || s.writes[0] != s.deletes[0] {
		t.Fatalf("本地持久化失败应回滚本轮上传：writes=%v deletes=%v", s.writes, s.deletes)
	}
	if got := cm.GetRecoveryPassword(); got != "旧密码" {
		t.Fatalf("本地持久化失败不得保存新密码，得到：%q", got)
	}
}

func TestChangeRecoveryPasswordRollsBackUploadsWhenPasswordPersistFails(t *testing.T) {
	cm := testSyncManager(t)
	if err := cm.SetRecoveryPassword("旧密码"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cm.recoveryPasswordFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(cm.recoveryPasswordFile, 0700); err != nil {
		t.Fatal(err)
	}
	s := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSnapshot(t, "旧密码", "one")}}
	// 本地密码文件被故意破坏后无法读取旧密码，因此让远端同时接受候选新密码完成纯读取预检。
	s.files["connections_backup_20260101_000000.000_+0000.lumin2"] = encryptedSnapshot(t, "新密码", "one")
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: s}}, nil
	}
	if err := cm.ChangeRecoveryPassword("新密码"); err == nil {
		t.Fatal("密码持久化失败应返回错误")
	}
	if len(s.writes) != 1 || len(s.deletes) != 1 || s.writes[0] != s.deletes[0] {
		t.Fatalf("密码持久化失败应回滚本轮上传：writes=%v deletes=%v", s.writes, s.deletes)
	}
}

func TestPersistSyncSnapshotSavesAllMergedFields(t *testing.T) {
	cm := testSyncManager(t)
	snap := &SyncSnapshot{
		Connections:      []Connection{{ID: "connection", Host: "host"}},
		Credentials:      []Credential{{ID: "credential", Name: "凭据"}},
		QuickCommands:    "[]",
		AIProviders:      []aitypes.AIProviderProfile{{ID: "provider", Name: "供应商", UpdatedAt: 1}},
		ProxyNodes:       []aitypes.AIProxyNode{{ID: "proxy", Name: "代理", Host: "127.0.0.1", Port: 1080, UpdatedAt: 1}},
		SnapshotTime:     123,
	}
	if err := cm.persistSyncSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	if got := cm.GetAIProviderRegistry().Providers; len(got) != 1 || got[0].ID != "provider" {
		t.Fatalf("AI 供应商未完整持久化：%+v", got)
	}
	if got := cm.GetAIProxyNodes(); len(got) != 1 || got[0].ID != "proxy" {
		t.Fatalf("AI 代理节点未完整持久化：%+v", got)
	}
}

func TestSnapshotUploadPathHandlesNilResult(t *testing.T) {
	if got := snapshotUploadPath(nil); got != "" {
		t.Fatalf("nil 上传结果应安全返回空路径，得到：%q", got)
	}
}

func TestSyncWithRecoveryPasswordWrongCandidateIsNotSaved(t *testing.T) {
	cm := testSyncManager(t)
	if err := cm.SetRecoveryPassword("旧密码"); err != nil {
		t.Fatal(err)
	}
	s := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSnapshot(t, "正确候选", "one")}}
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: s}}, nil
	}
	if _, err := cm.SyncWithRecoveryPassword("错误候选"); !errors.Is(err, errRecoveryPassword) {
		t.Fatalf("错误候选应返回密码错误，得到：%v", err)
	}
	if got := cm.GetRecoveryPassword(); got != "旧密码" {
		t.Fatalf("错误候选不得持久化，得到：%q", got)
	}
	if len(s.writes) != 0 {
		t.Fatalf("错误候选纯读取预检不得写远端：%v", s.writes)
	}
}

func TestSyncWithRecoveryPasswordSavesConfirmedCandidate(t *testing.T) {
	cm := testSyncManager(t)
	s := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSnapshot(t, "正确候选", "one")}}
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: s}}, nil
	}
	if _, err := cm.SyncWithRecoveryPassword("正确候选"); err != nil {
		t.Fatal(err)
	}
	if got := cm.GetRecoveryPassword(); got != "正确候选" {
		t.Fatalf("预检成功后应持久化候选密码，得到：%q", got)
	}
}

func TestSyncAllProvidersStrictPreservesPasswordError(t *testing.T) {
	cm := testSyncManager(t)
	s := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSnapshot(t, "正确", "one")}}
	_, err := cm.syncAllProviders([]providerEntry{{provider: "webdav", storage: s}}, "错误", true)
	if !errors.Is(err, errRecoveryPassword) {
		t.Fatalf("all 严格同步应保留密码错误链，得到：%v", err)
	}
}

func TestSnapshotEqualIgnoresTimeListOrderAndQuickJSONFormatting(t *testing.T) {
	first := &SyncSnapshot{
		Connections:   []Connection{{ID: "a", Host: "一"}, {ID: "b", Host: "二"}},
		Credentials:   []Credential{{ID: "x", Name: "甲"}, {ID: "y", Name: "乙"}},
		QuickCommands: `[{"name":"先","command":"one"},{"name":"后","command":"two"}]`,
		SnapshotTime:  1,
	}
	second := &SyncSnapshot{
		Connections:   []Connection{{ID: "b", Host: "二"}, {ID: "a", Host: "一"}},
		Credentials:   []Credential{{ID: "y", Name: "乙"}, {ID: "x", Name: "甲"}},
		QuickCommands: "[ { \"command\": \"one\", \"name\": \"先\" }, { \"command\": \"two\", \"name\": \"后\" } ]",
		SnapshotTime:  999,
	}
	if !snapshotEqual(first, second) {
		t.Fatal("业务比较应忽略快照时间、按 ID 比较列表，并按 JSON 语义比较快捷命令")
	}
	second.QuickCommands = `[{"name":"后","command":"two"},{"name":"先","command":"one"}]`
	if snapshotEqual(first, second) {
		t.Fatal("快捷命令业务顺序变化必须视为不同")
	}
}

func TestAutoSyncProviderEmitsStatusAfterUploadingLocalChanges(t *testing.T) {
	cm := testSyncManager(t)
	if err := cm.persistSyncSnapshot(&SyncSnapshot{
		Connections:   []Connection{{ID: "server", Host: "new", LastModified: 20}},
		QuickCommands: "[]",
		SnapshotTime:  20,
	}); err != nil {
		t.Fatal(err)
	}
	cm.saveLastSyncTime("webdav", 15)
	remote := cm.localSyncSnapshot()
	remote.Connections = []Connection{{ID: "server", Host: "old", ProxyMode: "direct", LastModified: 10}}
	if err := cm.SetRecoveryPassword("密码"); err != nil {
		t.Fatal(err)
	}
	storage := &memoryStorage{files: map[string][]byte{
		"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSyncSnapshot(t, "密码", remote),
	}}
	var statusEvents, completedEvents []map[string]interface{}
	cm.syncEventForTest = func(event string, data map[string]interface{}) {
		switch event {
		case "sync-status":
			statusEvents = append(statusEvents, data)
		case "sync-completed":
			completedEvents = append(completedEvents, data)
		}
	}

	if err := cm.autoSyncProvider(storage, 0, "webdav"); err != nil {
		t.Fatal(err)
	}
	if len(statusEvents) != 1 || statusEvents[0]["action"] != "upload" {
		t.Fatalf("本地变更上传成功后必须发送一次状态事件：%v", statusEvents)
	}
	if len(completedEvents) != 1 || completedEvents[0]["timestamp"] != cm.loadLastSyncTime("webdav") {
		t.Fatalf("同步完成时间必须持久化并广播：events=%v persisted=%d", completedEvents, cm.loadLastSyncTime("webdav"))
	}
}

func TestAutoSyncProviderKeepsNoChangeSilent(t *testing.T) {
	cm := testSyncManager(t)
	if err := cm.persistSyncSnapshot(&SyncSnapshot{
		Connections:   []Connection{{ID: "server", Host: "same", LastModified: 20}},
		QuickCommands: "[]",
		SnapshotTime:  20,
	}); err != nil {
		t.Fatal(err)
	}
	remote := cm.localSyncSnapshot()
	if err := cm.SetRecoveryPassword("密码"); err != nil {
		t.Fatal(err)
	}
	storage := &memoryStorage{files: map[string][]byte{
		"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSyncSnapshot(t, "密码", remote),
	}}
	statusCount, completedCount := 0, 0
	cm.syncEventForTest = func(event string, _ map[string]interface{}) {
		switch event {
		case "sync-status":
			statusCount++
		case "sync-completed":
			completedCount++
		}
	}

	if err := cm.autoSyncProvider(storage, 0, "webdav"); err != nil {
		t.Fatal(err)
	}
	if statusCount != 0 {
		t.Fatalf("两端一致时不应发送完成提示，得到 %d 次", statusCount)
	}
	if completedCount != 1 || cm.loadLastSyncTime("webdav") <= 0 {
		t.Fatalf("两端一致也应记录一次同步完成：events=%d persisted=%d", completedCount, cm.loadLastSyncTime("webdav"))
	}
}

func TestAutoSyncProviderRecordsFirstEmptyRemoteUpload(t *testing.T) {
	cm := testSyncManager(t)
	storage := &memoryStorage{files: map[string][]byte{}}
	statusCount, completedCount := 0, 0
	cm.syncEventForTest = func(event string, _ map[string]interface{}) {
		switch event {
		case "sync-status":
			statusCount++
		case "sync-completed":
			completedCount++
		}
	}

	if err := cm.autoSyncProvider(storage, 0, "webdav"); err != nil {
		t.Fatal(err)
	}
	if len(storage.writes) != 1 || statusCount != 1 || completedCount != 1 || cm.loadLastSyncTime("webdav") <= 0 {
		t.Fatalf("首次上传应记录并通知同步完成：writes=%v status=%d completed=%d persisted=%d", storage.writes, statusCount, completedCount, cm.loadLastSyncTime("webdav"))
	}
}

func TestGetLastSyncTimeReturnsPersistedValue(t *testing.T) {
	cm := testSyncManager(t)
	if got := cm.LoadLastSyncTimeMax(); got != 0 {
		t.Fatalf("未同步时应返回 0，得到 %d", got)
	}
	cm.saveLastSyncTime("webdav", 123456789)
	if got := cm.LoadLastSyncTimeMax(); got != 123456789 {
		t.Fatalf("应返回持久化时间，得到 %d", got)
	}
}

func TestSyncAllProvidersUploadsOnlyDifferentAndNoBackup(t *testing.T) {
	cm := testSyncManager(t)
	local := &SyncSnapshot{
		Connections:         []Connection{{ID: "same", Host: "same", LastModified: 10}},
		Credentials:         []Credential{},
		QuickCommands:       "[]",
		AIProviders:         []aitypes.AIProviderProfile{},
		ProxyNodes:          []aitypes.AIProxyNode{},
		SnapshotTime:        10,
		HasCredentials:      true,
		HasQuickCommands:    true,
		HasAIProviders:      true,
		HasProxyNodes:       true,
	}
	if err := cm.persistSyncSnapshot(local); err != nil {
		t.Fatal(err)
	}
	same := *cm.localSyncSnapshot()
	same.SnapshotTime = 999
	different := same
	different.Connections = []Connection{{ID: "same", Host: "old", ProxyMode: "direct", LastModified: 1}}
	matching := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSyncSnapshot(t, "密码", &same)}}
	stale := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSyncSnapshot(t, "密码", &different)}}
	empty := &memoryStorage{files: map[string][]byte{}}
	result, err := cm.syncAllProviders([]providerEntry{{provider: "webdav", storage: matching}, {provider: "r2", storage: stale}, {provider: "ftp", storage: empty}}, "密码")
	if err != nil {
		t.Fatal(err)
	}
	if len(matching.writes) != 0 || len(stale.writes) != 1 || len(empty.writes) != 1 {
		t.Fatalf("应仅上传不同或无备份目标：matching=%v stale=%v empty=%v", matching.writes, stale.writes, empty.writes)
	}
	if result["action"] != "upload" || result["uploaded"] != 2 {
		t.Fatalf("动作和上传数错误：%v", result)
	}
}

func TestSyncAllProvidersRealDownloadFailureFailsClosed(t *testing.T) {
	cm := testSyncManager(t)
	good := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSnapshot(t, "密码", "remote")}}
	bad := &memoryStorage{listErr: errors.New("网络中断")}
	if _, err := cm.syncAllProviders([]providerEntry{{provider: "webdav", storage: good}, {provider: "r2", storage: bad}}, "密码"); err == nil {
		t.Fatal("任一真实下载失败应 fail closed")
	}
	if len(good.writes) != 0 || len(bad.writes) != 0 {
		t.Fatalf("下载失败不得上传：good=%v bad=%v", good.writes, bad.writes)
	}
	if _, err := os.Stat(cm.connFile); !os.IsNotExist(err) {
		t.Fatalf("下载失败不得持久化本地：%v", err)
	}
}

func TestSyncAllProvidersRollsBackUploadsOnFailure(t *testing.T) {
	cm := testSyncManager(t)
	first := &memoryStorage{files: map[string][]byte{}}
	second := &memoryStorage{files: map[string][]byte{}, writeErr: errors.New("上传失败")}
	if _, err := cm.syncAllProviders([]providerEntry{{provider: "webdav", storage: first}, {provider: "r2", storage: second}}, "密码"); err == nil {
		t.Fatal("后续上传失败应整体失败")
	}
	if len(first.writes) != 1 || len(first.deletes) != 1 || first.writes[0] != first.deletes[0] {
		t.Fatalf("后续上传失败应回滚本轮文件：writes=%v deletes=%v", first.writes, first.deletes)
	}
	if _, err := os.Stat(cm.connFile); !os.IsNotExist(err) {
		t.Fatalf("上传失败不得持久化本地：%v", err)
	}
}

func TestSyncAllProvidersRollsBackWhenLocalPersistFails(t *testing.T) {
	cm := testSyncManager(t)
	if err := os.Mkdir(cm.quickCmdFile, 0700); err != nil {
		t.Fatal(err)
	}
	storage := &memoryStorage{files: map[string][]byte{}}
	if _, err := cm.syncAllProviders([]providerEntry{{provider: "webdav", storage: storage}}, "密码"); err == nil {
		t.Fatal("本地持久化失败应整体失败")
	}
	if len(storage.writes) != 1 || len(storage.deletes) != 1 || storage.writes[0] != storage.deletes[0] {
		t.Fatalf("本地持久化失败应回滚本轮文件：writes=%v deletes=%v", storage.writes, storage.deletes)
	}
}

func TestSyncAllProvidersPropagatesDeletionIntoFinal(t *testing.T) {
	cm := testSyncManager(t)
	if err := cm.persistSyncSnapshot(&SyncSnapshot{
		Connections:   []Connection{{ID: "deleted", Host: "deleted", LastModified: 100}},
		QuickCommands: "[]",
		SnapshotTime:  100,
	}); err != nil {
		t.Fatal(err)
	}
	cm.saveLastSyncTime("webdav", 200)
	remote := &SyncSnapshot{Connections: []Connection{}, SnapshotTime: 300}
	storage := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSyncSnapshot(t, "密码", remote)}}
	result, err := cm.syncAllProviders([]providerEntry{{provider: "webdav", storage: storage}}, "密码")
	if err != nil {
		t.Fatal(err)
	}
	if got := cm.GetConnections(); len(got) != 0 {
		t.Fatalf("远端删除应传播到 FINAL 和本地：%+v", got)
	}
	if len(storage.writes) != 1 || result["action"] != "merge" {
		t.Fatalf("删除传播后的完整 FINAL 应回写旧格式远端：writes=%v result=%v", storage.writes, result)
	}
	decrypted, err := decryptLUMETERM2(string(storage.files[storage.writes[0]]), "密码")
	if err != nil {
		t.Fatal(err)
	}
	var uploaded SyncSnapshot
	if err := json.Unmarshal([]byte(decrypted), &uploaded); err != nil {
		t.Fatal(err)
	}
	if len(uploaded.Connections) != 0 {
		t.Fatalf("上传 FINAL 不得复活已删除连接：%+v", uploaded.Connections)
	}
}

func TestSyncAllProvidersInitializationFailureStopsBeforeSync(t *testing.T) {
	cm := testSyncManager(t)
	storage := &memoryStorage{files: map[string][]byte{}}
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: storage}}, []providerFailure{{provider: "r2", err: errors.New("初始化失败")}}
	}
	if _, err := cm.SyncAllProviders(); err == nil {
		t.Fatal("all 入口初始化失败应立即停止")
	}
	if len(storage.writes) != 0 {
		t.Fatalf("初始化失败不得继续同步：%v", storage.writes)
	}
}

func TestResetRecoveryPasswordUsesLocalSnapshotAndPreservesOldBackups(t *testing.T) {
	cm := testSyncManager(t)
	if err := cm.persistSyncSnapshot(&SyncSnapshot{
		Connections:   []Connection{{ID: "local", Host: "local", LastModified: 2}},
		QuickCommands: "[]",
		SnapshotTime:  2,
	}); err != nil {
		t.Fatal(err)
	}
	oldName := "connections_backup_20260101_000000.000_+0000.lumin2"
	s := &memoryStorage{files: map[string][]byte{oldName: encryptedSnapshot(t, "未知密码", "remote")}}
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: s}}, nil
	}
	if err := cm.ResetRecoveryPassword("新密码"); err != nil {
		t.Fatal(err)
	}
	if len(s.writes) != 1 {
		t.Fatalf("每个后端应只上传一次：%v", s.writes)
	}
	if _, ok := s.files[oldName]; !ok {
		t.Fatal("强制重置必须保留旧备份")
	}
	snap, err := cm.decryptAndParseSnapshot(string(s.files[s.writes[0]]), "新密码")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Connections) != 1 || snap.Connections[0].ID != "local" {
		t.Fatalf("强制重置必须以本机快照为准，得到：%+v", snap.Connections)
	}
}

func TestResetRecoveryPasswordRollsBackAllUploadsOnLaterFailure(t *testing.T) {
	cm := testSyncManager(t)
	first := &memoryStorage{files: map[string][]byte{}}
	second := &memoryStorage{files: map[string][]byte{}, writeErr: errors.New("上传失败")}
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: first}, {provider: "r2", storage: second}}, nil
	}
	if err := cm.ResetRecoveryPassword("新密码"); err == nil {
		t.Fatal("后端上传失败应返回错误")
	}
	if len(first.writes) != 1 || len(first.deletes) != 1 || first.writes[0] != first.deletes[0] {
		t.Fatalf("后续上传失败应回滚已上传文件：writes=%v deletes=%v", first.writes, first.deletes)
	}
	if got := cm.GetRecoveryPassword(); got != "" {
		t.Fatalf("上传失败不得保存本地密码，得到：%q", got)
	}
}

func TestResetRecoveryPasswordRollsBackWhenPasswordPersistFails(t *testing.T) {
	cm := testSyncManager(t)
	if err := os.Mkdir(cm.recoveryPasswordFile, 0700); err != nil {
		t.Fatal(err)
	}
	s := &memoryStorage{files: map[string][]byte{}}
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: s}}, nil
	}
	if err := cm.ResetRecoveryPassword("新密码"); err == nil {
		t.Fatal("本地密码保存失败应返回错误")
	}
	if len(s.writes) != 1 || len(s.deletes) != 1 || s.writes[0] != s.deletes[0] {
		t.Fatalf("密码保存失败应回滚上传：writes=%v deletes=%v", s.writes, s.deletes)
	}
}

func TestResetRecoveryPasswordWithoutProvidersAndWhitespace(t *testing.T) {
	cm := testSyncManager(t)
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) { return nil, nil }
	if err := cm.ResetRecoveryPassword("仅本地密码"); err != nil {
		t.Fatal(err)
	}
	if got := cm.GetRecoveryPassword(); got != "仅本地密码" {
		t.Fatalf("无后端时应仅保存本地密码，得到：%q", got)
	}
	if err := cm.ResetRecoveryPassword(" \t\r\n "); err != nil {
		t.Fatal(err)
	}
	if got := cm.GetRecoveryPassword(); got != "" {
		t.Fatalf("纯空白应规范为空密码，得到：%q", got)
	}
}

func TestSetRecoveryPasswordReturnsRemoveError(t *testing.T) {
	cm := testSyncManager(t)
	if err := os.Mkdir(cm.recoveryPasswordFile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cm.recoveryPasswordFile, "占位"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cm.SetRecoveryPassword(""); err == nil {
		t.Fatal("清除不可删除的密码路径必须返回删除错误")
	}
}

func TestChangeRecoveryPasswordPersistsAfterSuccessfulUploads(t *testing.T) {
	cm := testSyncManager(t)
	if err := cm.SetRecoveryPassword("旧密码"); err != nil {
		t.Fatal(err)
	}
	first := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSnapshot(t, "旧密码", "one")}}
	second := &memoryStorage{files: map[string][]byte{"connections_backup_20260101_000000.000_+0000.lumin2": encryptedSnapshot(t, "旧密码", "two")}}
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: first}, {provider: "r2", storage: second}}, nil
	}
	if err := cm.ChangeRecoveryPassword("新密码"); err != nil {
		t.Fatal(err)
	}
	if len(first.writes) != 1 || len(second.writes) != 1 {
		t.Fatalf("每个目标应只上传一次：%v %v", first.writes, second.writes)
	}
	if got := cm.GetRecoveryPassword(); got != "新密码" {
		t.Fatalf("全部成功后应持久化新密码，得到：%q", got)
	}
	for _, s := range []*memoryStorage{first, second} {
		if _, err := cm.decryptAndParseSnapshot(string(s.files[s.writes[0]]), "新密码"); err != nil {
			t.Fatalf("新快照应使用新密码：%v", err)
		}
	}
}

func TestSwitchProviderDoesNotDeleteLocalOnlyByForeignLastSync(t *testing.T) {
	cm := testSyncManager(t)
	// 模拟长期用 R2 同步后 lastSync 已很高；本地有 R2 上新增的节点。
	local := &SyncSnapshot{
		Connections: []Connection{
			{ID: "shared", Host: "shared.example", Port: 22, Username: "root", LastModified: 100},
			{ID: "r2-only-a", Host: "a.example", Port: 22, Username: "root", LastModified: 150},
			{ID: "r2-only-b", Host: "b.example", Port: 22, Username: "root", LastModified: 160},
		},
		Credentials: []Credential{
			{ID: "cred-shared", Name: "shared", LastModified: 100},
			{ID: "cred-r2", Name: "r2", LastModified: 160},
		},
		QuickCommands: "[]",
		SnapshotTime:  200,
	}
	if err := cm.persistSyncSnapshot(local); err != nil {
		t.Fatal(err)
	}
	cm.saveLastSyncTime("r2", 1000) // 仅 R2 同步过；WebDAV 从未同步

	// WebDAV 仍是旧快照：缺 r2-only 节点
	webdavRemote := &SyncSnapshot{
		Connections: []Connection{
			{ID: "shared", Host: "shared.example", Port: 22, Username: "root", LastModified: 100},
		},
		Credentials: []Credential{
			{ID: "cred-shared", Name: "shared", LastModified: 100},
		},
		HasCredentials: true,
		SnapshotTime:   100,
	}
	storage := &memoryStorage{files: map[string][]byte{
		"connections_backup_20260101_000000.000_+0000.json": mustJSON(t, webdavRemote),
	}}

	if _, err := cm.syncFromProvider(storage, 0, "", "webdav"); err != nil {
		t.Fatal(err)
	}
	got := cm.GetConnections()
	ids := map[string]bool{}
	for _, c := range got {
		ids[c.ID] = true
	}
	for _, id := range []string{"shared", "r2-only-a", "r2-only-b"} {
		if !ids[id] {
			t.Fatalf("切回 WebDAV 不得删除仅存在于 R2/本地的节点 %s；got=%+v", id, got)
		}
	}
	if cm.loadLastSyncTime("webdav") <= 0 {
		t.Fatal("WebDAV 同步完成后应记录本后端 lastSync")
	}
	if cm.loadLastSyncTime("r2") != 1000 {
		t.Fatalf("不得覆盖其他后端 lastSync：%d", cm.loadLastSyncTime("r2"))
	}
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLegacyGlobalLastSyncTimeIsIgnored(t *testing.T) {
	cm := testSyncManager(t)
	if err := os.WriteFile(cm.lastSyncFile, []byte("999999"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := cm.loadLastSyncTime("webdav"); got != 0 {
		t.Fatalf("旧版全局 lastSync 不得归属到任意后端，got=%d", got)
	}
	if got := cm.loadLastSyncTime("r2"); got != 0 {
		t.Fatalf("旧版全局 lastSync 不得归属到任意后端，got=%d", got)
	}
	cm.saveLastSyncTime("webdav", 42)
	if got := cm.loadLastSyncTime("webdav"); got != 42 {
		t.Fatalf("got %d", got)
	}
	if got := cm.loadLastSyncTime("r2"); got != 0 {
		t.Fatalf("r2 should stay 0, got %d", got)
	}
}

func TestTombstonePropagatesExplicitDeleteAcrossProviders(t *testing.T) {
	// 设备1 用 WebDAV 删了 server1；设备2 本地仍有 server1。
	// 设备2 首次合 WebDAV 时若没有墓碑会把 server1 救回去；
	// 有墓碑后应删除并上传带 tombstone 的快照。
	cm := testSyncManager(t)
	local := &SyncSnapshot{
		Connections: []Connection{
			{ID: "server1", Host: "s1.example", Port: 22, Username: "root", LastModified: 100},
			{ID: "server2", Host: "s2.example", Port: 22, Username: "root", LastModified: 100},
		},
		Credentials:   []Credential{},
		QuickCommands: "[]",
		SnapshotTime:  100,
	}
	if err := cm.persistSyncSnapshot(local); err != nil {
		t.Fatal(err)
	}
	// WebDAV 远端：已删 server1，并带墓碑
	remote := &SyncSnapshot{
		Connections: []Connection{
			{ID: "server2", Host: "s2.example", Port: 22, Username: "root", LastModified: 100},
		},
		DeletedConnections: []SyncTombstone{
			{ID: "server1", DeletedAt: 500},
		},
		HasCredentials:        true,
		HasDeletedConnections: true,
		SnapshotTime:          500,
	}
	storage := &memoryStorage{files: map[string][]byte{
		"connections_backup_20260101_000000.000_+0000.json": mustJSON(t, remote),
	}}
	if _, err := cm.syncFromProvider(storage, 0, "", "webdav"); err != nil {
		t.Fatal(err)
	}
	got := cm.GetConnections()
	for _, c := range got {
		if c.ID == "server1" {
			t.Fatalf("远端墓碑应删除本地 server1：%+v", got)
		}
	}
	if len(got) != 1 || got[0].ID != "server2" {
		t.Fatalf("应只剩 server2：%+v", got)
	}
	store := cm.loadTombstoneStore()
	if tombstoneMap(store.Connections)["server1"] != 500 {
		t.Fatalf("本地应保留 server1 墓碑：%+v", store.Connections)
	}
	// 上传的快照也应带墓碑
	if len(storage.writes) == 0 {
		t.Fatal("合并结果与远端不同应上传")
	}
	var uploaded SyncSnapshot
	if err := json.Unmarshal(storage.files[storage.writes[0]], &uploaded); err != nil {
		t.Fatal(err)
	}
	if tombstoneMap(uploaded.DeletedConnections)["server1"] != 500 {
		t.Fatalf("上传快照必须携带 server1 墓碑：%+v", uploaded.DeletedConnections)
	}
}

func TestLocalDeleteWritesTombstoneAndPushesDelete(t *testing.T) {
	cm := testSyncManager(t)
	if err := cm.persistSyncSnapshot(&SyncSnapshot{
		Connections: []Connection{
			{ID: "keep", Host: "k.example", Port: 22, Username: "root", LastModified: 10},
			{ID: "drop", Host: "d.example", Port: 22, Username: "root", LastModified: 10},
		},
		QuickCommands: "[]",
		SnapshotTime:  10,
	}); err != nil {
		t.Fatal(err)
	}
	if !cm.DeleteConnection("drop") {
		t.Fatal("delete failed")
	}
	store := cm.loadTombstoneStore()
	if _, ok := tombstoneMap(store.Connections)["drop"]; !ok {
		t.Fatalf("删除后应写入墓碑：%+v", store.Connections)
	}
	// 远端仍有 drop；同步后应删掉远端并上传墓碑
	remote := &SyncSnapshot{
		Connections: []Connection{
			{ID: "keep", Host: "k.example", Port: 22, Username: "root", LastModified: 10},
			{ID: "drop", Host: "d.example", Port: 22, Username: "root", LastModified: 10},
		},
		SnapshotTime: 10,
	}
	storage := &memoryStorage{files: map[string][]byte{
		"connections_backup_20260101_000000.000_+0000.json": mustJSON(t, remote),
	}}
	if _, err := cm.syncFromProvider(storage, 0, "", "webdav"); err != nil {
		t.Fatal(err)
	}
	if ids := cm.GetConnections(); len(ids) != 1 || ids[0].ID != "keep" {
		t.Fatalf("本地 tombstone 应继续压制远端 drop：%+v", ids)
	}
	if len(storage.writes) == 0 {
		t.Fatal("应上传带 tombstone 的合并结果")
	}
	var uploaded SyncSnapshot
	if err := json.Unmarshal(storage.files[storage.writes[0]], &uploaded); err != nil {
		t.Fatal(err)
	}
	if _, ok := tombstoneMap(uploaded.DeletedConnections)["drop"]; !ok {
		t.Fatalf("上传应包含 drop 墓碑：%+v", uploaded.DeletedConnections)
	}
	for _, c := range uploaded.Connections {
		if c.ID == "drop" {
			t.Fatalf("上传不得复活 drop：%+v", uploaded.Connections)
		}
	}
}

func TestRecreateAfterDeleteClearsTombstone(t *testing.T) {
	cm := testSyncManager(t)
	// 本地墓碑 drop@100；远端又有 LastModified=200 的同 id → 应复活并清墓碑
	if err := cm.persistSyncSnapshot(&SyncSnapshot{
		Connections:        []Connection{},
		DeletedConnections: []SyncTombstone{{ID: "drop", DeletedAt: 100}},
		QuickCommands:      "[]",
		SnapshotTime:       100,
	}); err != nil {
		t.Fatal(err)
	}
	remote := &SyncSnapshot{
		Connections: []Connection{
			{ID: "drop", Host: "new.example", Port: 22, Username: "root", LastModified: 200},
		},
		SnapshotTime: 200,
	}
	storage := &memoryStorage{files: map[string][]byte{
		"connections_backup_20260101_000000.000_+0000.json": mustJSON(t, remote),
	}}
	if _, err := cm.syncFromProvider(storage, 0, "", "webdav"); err != nil {
		t.Fatal(err)
	}
	got := cm.GetConnections()
	if len(got) != 1 || got[0].ID != "drop" || got[0].LastModified != 200 {
		t.Fatalf("新于墓碑的节点应复活：%+v", got)
	}
	if _, ok := tombstoneMap(cm.loadTombstoneStore().Connections)["drop"]; ok {
		t.Fatalf("复活后应清除墓碑：%+v", cm.loadTombstoneStore().Connections)
	}
}

func TestHostPortDedupWritesTombstoneForDroppedID(t *testing.T) {
	cm := testSyncManager(t)
	// 本地新 id-B；远端旧 id-A 同一 host:port:user → 去重保留 B，A 必须进墓碑
	local := &SyncSnapshot{
		Connections: []Connection{
			{ID: "id-B", Host: "1.2.3.4", Port: 22, Username: "root", LastModified: 200},
		},
		QuickCommands: "[]",
		SnapshotTime:  200,
	}
	if err := cm.persistSyncSnapshot(local); err != nil {
		t.Fatal(err)
	}
	remote := &SyncSnapshot{
		Connections: []Connection{
			{ID: "id-A", Host: "1.2.3.4", Port: 22, Username: "root", LastModified: 100},
		},
		SnapshotTime: 100,
	}
	storage := &memoryStorage{files: map[string][]byte{
		"connections_backup_20260101_000000.000_+0000.json": mustJSON(t, remote),
	}}
	if _, err := cm.syncFromProvider(storage, 0, "", "webdav"); err != nil {
		t.Fatal(err)
	}
	got := cm.GetConnections()
	if len(got) != 1 || got[0].ID != "id-B" {
		t.Fatalf("应只保留 id-B：%+v", got)
	}
	tombs := tombstoneMap(cm.loadTombstoneStore().Connections)
	if tombs["id-A"] <= 100 {
		t.Fatalf("被挤掉的 id-A 应有 deleted_at>100 的墓碑：%+v", cm.loadTombstoneStore().Connections)
	}
	if len(storage.writes) == 0 {
		t.Fatal("去重后应上传带墓碑的合并结果")
	}
	// 再同步：用上一轮上传的完整快照（含 id-B + id-A 墓碑），id-A 不得复活
	latest := storage.writes[len(storage.writes)-1]
	storage2 := &memoryStorage{files: map[string][]byte{
		latest: append([]byte(nil), storage.files[latest]...),
	}}
	if _, err := cm.syncFromProvider(storage2, 0, "", "webdav"); err != nil {
		t.Fatal(err)
	}
	got = cm.GetConnections()
	for _, c := range got {
		if c.ID == "id-A" {
			t.Fatalf("有墓碑后 id-A 不得复活：%+v", got)
		}
	}
	if len(got) != 1 || got[0].ID != "id-B" {
		t.Fatalf("第二次同步仍应只有 id-B：%+v", got)
	}
}

func TestPruneSyncTombstonesByDaysUploadsRemaining(t *testing.T) {
	cm := testSyncManager(t)
	now := time.Now().UnixMilli()
	oldAt := now - 40*24*60*60*1000
	newAt := now - 5*24*60*60*1000
	// 先落连接快照，再写墓碑，避免 persistSyncSnapshot 用空墓碑覆盖
	if err := cm.persistSyncSnapshot(&SyncSnapshot{
		Connections:   []Connection{{ID: "keep", Host: "k", Port: 22, Username: "root", LastModified: now}},
		QuickCommands: "[]",
		SnapshotTime:  now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := cm.saveTombstoneStore(syncTombstoneStore{
		Connections: []SyncTombstone{
			{ID: "old", DeletedAt: oldAt},
			{ID: "new", DeletedAt: newAt},
		},
		Credentials: []SyncTombstone{
			{ID: "cred-old", DeletedAt: oldAt},
		},
	}); err != nil {
		t.Fatal(err)
	}
	storage := &memoryStorage{files: map[string][]byte{}}
	// 清理上传跟当前同步模式走（getSyncProviders），不是全部已配置后端
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: storage}}, nil
	}
	res, err := cm.PruneSyncTombstones(30)
	if err != nil {
		t.Fatal(err)
	}
	if res.RemovedConnections != 1 || res.RemovedCredentials != 1 {
		t.Fatalf("应清理 1 连接 + 1 凭据：%+v", res)
	}
	if res.RemainingConnections != 1 || res.RemainingCredentials != 0 {
		t.Fatalf("应保留 5 天内的 new：%+v", res)
	}
	if res.Uploaded != 1 || len(storage.writes) != 1 {
		t.Fatalf("清理后应上传到云端：uploaded=%d writes=%v", res.Uploaded, storage.writes)
	}
	store := cm.loadTombstoneStore()
	if _, ok := tombstoneMap(store.Connections)["old"]; ok {
		t.Fatal("old 墓碑应已清理")
	}
	if tombstoneMap(store.Connections)["new"] != newAt {
		t.Fatalf("new 墓碑应保留：%+v", store.Connections)
	}
	var uploaded SyncSnapshot
	if err := json.Unmarshal(storage.files[storage.writes[0]], &uploaded); err != nil {
		t.Fatal(err)
	}
	if _, ok := tombstoneMap(uploaded.DeletedConnections)["old"]; ok {
		t.Fatalf("上传快照不得再带 old 墓碑：%+v", uploaded.DeletedConnections)
	}
	if tombstoneMap(uploaded.DeletedConnections)["new"] != newAt {
		t.Fatalf("上传快照应保留 new 墓碑：%+v", uploaded.DeletedConnections)
	}
	// 清理后必须推进水位线，否则对端旧墓碑还会被并回来
	if pb := cm.loadTombstoneStore().PrunedBefore; pb <= 0 {
		t.Fatalf("清理后 PrunedBefore 应推进，got=%d", pb)
	}
	if uploaded.TombstonePrunedBefore <= 0 {
		t.Fatalf("上传快照应带 tombstone_pruned_before，got=%d", uploaded.TombstonePrunedBefore)
	}
}

func TestPruneWatermarkBlocksRemoteTombstoneRestore(t *testing.T) {
	// 清理删除记录后，即使远端仍有旧墓碑，合并也不得恢复
	cm := testSyncManager(t)
	now := time.Now().UnixMilli()
	oldAt := now - 40*24*60*60*1000
	if err := cm.persistSyncSnapshot(&SyncSnapshot{
		Connections:   []Connection{{ID: "keep", Host: "k", Port: 22, Username: "root", LastModified: now}},
		QuickCommands: "[]",
		SnapshotTime:  now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := cm.saveTombstoneStore(syncTombstoneStore{
		Connections: []SyncTombstone{{ID: "old", DeletedAt: oldAt}},
	}); err != nil {
		t.Fatal(err)
	}
	storage := &memoryStorage{files: map[string][]byte{}}
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: storage}}, nil
	}
	if _, err := cm.PruneSyncTombstones(30); err != nil {
		t.Fatal(err)
	}
	// 模拟对端仍带着被清掉的 old 墓碑
	remote := &SyncSnapshot{
		Connections: []Connection{{ID: "keep", Host: "k", Port: 22, Username: "root", LastModified: now}},
		DeletedConnections: []SyncTombstone{
			{ID: "old", DeletedAt: oldAt},
		},
		HasCredentials: true,
		SnapshotTime:   now + 1,
	}
	storage2 := &memoryStorage{files: map[string][]byte{
		"connections_backup_20260102_000000.000_+0000.json": mustJSON(t, remote),
	}}
	if _, err := cm.syncFromProvider(storage2, 0, "", "webdav"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tombstoneMap(cm.loadTombstoneStore().Connections)["old"]; ok {
		t.Fatalf("水位线应挡住远端旧墓碑恢复：%+v", cm.loadTombstoneStore())
	}
	// 上传包也不得再带 old
	if len(storage2.writes) > 0 {
		var uploaded SyncSnapshot
		if err := json.Unmarshal(storage2.files[storage2.writes[len(storage2.writes)-1]], &uploaded); err != nil {
			t.Fatal(err)
		}
		if _, ok := tombstoneMap(uploaded.DeletedConnections)["old"]; ok {
			t.Fatalf("上传不得恢复 old 墓碑：%+v", uploaded.DeletedConnections)
		}
	}
}


func TestAutoSyncSkipsFirstContactTombstoneConflict(t *testing.T) {
	cm := testSyncManager(t)
	// 本地有 drop 墓碑，从未同步过 webdav；远端仍有 drop → AutoSync 应跳过
	if err := cm.persistSyncSnapshot(&SyncSnapshot{
		Connections: []Connection{
			{ID: "keep", Host: "keep.example", Port: 22, Username: "root", LastModified: 100},
		},
		DeletedConnections: []SyncTombstone{{ID: "drop", DeletedAt: 500}},
		QuickCommands:      "[]",
		SnapshotTime:       200,
	}); err != nil {
		t.Fatal(err)
	}
	remote := &SyncSnapshot{
		Connections: []Connection{
			{ID: "keep", Host: "keep.example", Port: 22, Username: "root", LastModified: 100},
			{ID: "drop", Host: "drop.example", Port: 22, Username: "root", LastModified: 100},
		},
		SnapshotTime: 150,
	}
	if !cm.shouldSkipAutoSyncForTombstoneConflict("webdav", remote) {
		t.Fatal("首次接触 + 冲突应跳过 AutoSync")
	}
	// 已同步过该后端则不跳过
	cm.saveLastSyncTime("webdav", 999)
	if cm.shouldSkipAutoSyncForTombstoneConflict("webdav", remote) {
		t.Fatal("已同步过后端不应再因冲突跳过 AutoSync")
	}
}

func TestPreviewTombstoneConflictsListsRemoteItems(t *testing.T) {
	cm := testSyncManager(t)
	// 本地已删 drop，有墓碑；远端仍有 drop → 预检应列出
	if err := cm.persistSyncSnapshot(&SyncSnapshot{
		Connections: []Connection{
			{ID: "keep", Host: "keep.example", Port: 22, Username: "root", LastModified: 100},
		},
		DeletedConnections: []SyncTombstone{{ID: "drop", DeletedAt: 500}},
		QuickCommands:      "[]",
		SnapshotTime:       200,
	}); err != nil {
		t.Fatal(err)
	}
	remote := &SyncSnapshot{
		Connections: []Connection{
			{ID: "keep", Host: "keep.example", Port: 22, Username: "root", LastModified: 100},
			{ID: "drop", Name: "被删", Host: "drop.example", Port: 22, Username: "root", LastModified: 100},
		},
		SnapshotTime: 150,
	}
	storage := &memoryStorage{files: map[string][]byte{
		"connections_backup_20260101_000000.000_+0000.json": mustJSON(t, remote),
	}}
	cm.syncProvidersForTest = func() ([]providerEntry, []providerFailure) {
		return []providerEntry{{provider: "webdav", storage: storage}}, nil
	}
	prev, err := cm.PreviewTombstoneConflicts()
	if err != nil {
		t.Fatal(err)
	}
	if len(prev.WouldDeleteConnections) != 1 || prev.WouldDeleteConnections[0].ID != "drop" {
		t.Fatalf("应预检到 drop：%+v", prev)
	}
	// 清除冲突墓碑后预检为空
	cm.ClearTombstoneConflicts([]string{"drop"}, nil)
	prev2, err := cm.PreviewTombstoneConflicts()
	if err != nil {
		t.Fatal(err)
	}
	if len(prev2.WouldDeleteConnections) != 0 {
		t.Fatalf("清除后应无冲突：%+v", prev2)
	}
}

func TestInferredDeleteWritesTombstoneSoUploadIsNotEmpty(t *testing.T) {
	// 复现 1.txt：本地按 lastSync 启发式删掉 drop，上传不得是「人没了、墓碑也空」
	cm := testSyncManager(t)
	if err := cm.persistSyncSnapshot(&SyncSnapshot{
		Connections: []Connection{
			{ID: "keep", Host: "keep.example", Port: 22, Username: "root", LastModified: 100},
			{ID: "drop", Host: "drop.example", Port: 22, Username: "root", LastModified: 100},
		},
		QuickCommands: "[]",
		SnapshotTime:  200,
	}); err != nil {
		t.Fatal(err)
	}
	// PC 本地 lastSync 已经很高，远程无 drop、也无墓碑 → 旧逻辑会删人但不写墓碑
	cm.saveLastSyncTime("webdav", 150)
	remote := &SyncSnapshot{
		Connections: []Connection{
			{ID: "keep", Host: "keep.example", Port: 22, Username: "root", LastModified: 100},
		},
		HasCredentials: true,
		SnapshotTime:   150,
	}
	storage := &memoryStorage{files: map[string][]byte{
		"connections_backup_20260101_000000.000_+0000.json": mustJSON(t, remote),
	}}
	if _, err := cm.syncFromProvider(storage, 0, "", "webdav"); err != nil {
		t.Fatal(err)
	}
	got := cm.GetConnections()
	for _, c := range got {
		if c.ID == "drop" {
			t.Fatalf("drop 应被删除：%+v", got)
		}
	}
	tombs := tombstoneMap(cm.loadTombstoneStore().Connections)
	if tombs["drop"] <= 100 {
		t.Fatalf("启发式删除必须写墓碑：%+v", cm.loadTombstoneStore().Connections)
	}
	if len(storage.writes) == 0 {
		t.Fatal("应上传带墓碑的合并结果")
	}
	var uploaded SyncSnapshot
	if err := json.Unmarshal(storage.files[storage.writes[len(storage.writes)-1]], &uploaded); err != nil {
		t.Fatal(err)
	}
	if _, ok := tombstoneMap(uploaded.DeletedConnections)["drop"]; !ok {
		t.Fatalf("上传快照不得是空墓碑：%+v", uploaded.DeletedConnections)
	}
	for _, c := range uploaded.Connections {
		if c.ID == "drop" {
			t.Fatalf("上传不得复活 drop：%+v", uploaded.Connections)
		}
	}
}

func TestConnsEqualNormalizesAndroidDefaultProxyFields(t *testing.T) {
	// PC omitempty 形态 vs 安卓 historically 写 socks5/1080 空串
	pc := []Connection{{
		ID: "1", Name: "s", Host: "h", Port: 22, Username: "u",
		Password: "p", AuthMethod: "password", ProxyMode: "direct",
		LastModified: 100,
	}}
	android := []Connection{{
		ID: "1", Name: "s", Host: "h", Port: 22, Username: "u",
		Password: "p", AuthMethod: "password",
		PrivateKey: "", Passphrase: "", Group: "", CredentialID: "",
		ProxyMode: "direct", ProxyNodeID: "", ProxyType: "socks5",
		ProxyHost: "", ProxyPort: 1080, ProxyUsername: "", ProxyPassword: "",
		LastModified: 100,
	}}
	if !connsEqual(pc, android) {
		t.Fatal("规范化后 direct 模式下默认 proxy* 不应被视为差异")
	}
}

func TestAIGlobalSettingsEqualTreatsNilCollaborationPresetsAsEmpty(t *testing.T) {
	// ponytail: ai_global_settings 不再同步, aiGlobalSettingsEqual 始终返回 true
	local := &aitypes.AIGlobalSettings{
		CurrentProviderID:          "p1",
		AlwaysAllowExecute:         true,
		CollaborationPromptPresets: []aitypes.AICollaborationPromptPreset{},
		CollaborationExtraPrompt:   "  extra\r\n ",
		UpdatedAt:                  100,
	}
	remote := &aitypes.AIGlobalSettings{
		CurrentProviderID:          "p1",
		AlwaysAllowExecute:         true,
		CollaborationPromptPresets: nil,
		CollaborationExtraPrompt:   "extra",
		UpdatedAt:                  200,
	}
	if !aiGlobalSettingsEqual(local, remote) {
		t.Fatal("ai_global_settings 不再同步, aiGlobalSettingsEqual 应始终返回 true")
	}
}

func TestCredsEqualNormalizesEmptyPrivateKey(t *testing.T) {
	a := []Credential{{ID: "c", Name: "n", AuthMethod: "password", Username: "u", Password: "p", LastModified: 1}}
	b := []Credential{{ID: "c", Name: "n", AuthMethod: "password", Username: "u", Password: "p", PrivateKey: "", Passphrase: "", LastModified: 1}}
	if !credsEqual(a, b) {
		t.Fatal("空 privateKey/passphrase 应与缺失等价")
	}
}

func TestQuickCmdsEqualIgnoresExpanded(t *testing.T) {
	pc := `[{"type":"group","name":"面板安装","expanded":true,"children":[{"name":"宝塔","command":"x","last_modified":1}],"last_modified":1}]`
	android := `[{"type":"group","name":"面板安装","expanded":false,"children":[{"name":"宝塔","command":"x","last_modified":1}],"last_modified":1}]`
	if !quickCmdsEqual(pc, android) {
		t.Fatal("仅 expanded 不同不应视为快捷命令业务变化")
	}
	androidDiff := `[{"type":"group","name":"面板安装","expanded":false,"children":[{"name":"宝塔","command":"y","last_modified":1}],"last_modified":1}]`
	if quickCmdsEqual(pc, androidDiff) {
		t.Fatal("command 真变更必须检测为不同")
	}
}

func TestNormalizeConnectionForSyncClearsDirectProxyDefaults(t *testing.T) {
	c := normalizeConnectionForSync(Connection{
		ID: "1", ProxyMode: "direct", ProxyType: "socks5", ProxyPort: 1080,
	})
	if c.ProxyType != "" || c.ProxyPort != 0 {
		t.Fatalf("direct 应清空 proxyType/port, got type=%q port=%d", c.ProxyType, c.ProxyPort)
	}
}

// 远端缺 terminalEncoding（安卓旧包）与本地 utf-8 必须判等，否则每轮同步都白传一次
func TestConnsEqualIgnoresMissingTerminalEncoding(t *testing.T) {
	local := []Connection{{ID: "1", Host: "h", Username: "u", TerminalEncoding: "utf-8"}}
	remote := []Connection{{ID: "1", Host: "h", Username: "u"}}
	if !connsEqual(local, remote) {
		t.Fatal("缺 terminalEncoding 应与 utf-8 判等")
	}
	if c := normalizeConnectionForSync(Connection{ID: "1", TerminalEncoding: " GBK "}); c.TerminalEncoding != "gbk" {
		t.Fatalf("terminalEncoding 应归一为 gbk, got %q", c.TerminalEncoding)
	}
	if connsEqual(local, []Connection{{ID: "1", Host: "h", Username: "u", TerminalEncoding: "gbk"}}) {
		t.Fatal("terminalEncoding 真变更必须检测为不同")
	}
}
