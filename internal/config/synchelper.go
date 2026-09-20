package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	aitypes "lumeterm/internal/aitypes"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ─── 通用接口 ─────────────────────────────────────────────

// RemoteFile 远端文件元信息
type RemoteFile struct {
	Name    string
	ModTime time.Time
	IsDir   bool
	Size    int64
}

// RemoteStorage 远端存储接口
type RemoteStorage interface {
	ListFiles() ([]RemoteFile, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte) error
	DeleteFile(name string) error
}

// remoteDirEnsurer 可选：远端根目录被删除时，可主动重建后再同步。
// WebDAV/SFTP/FTP 支持；R2 等对象存储通常不需要。
type remoteDirEnsurer interface {
	EnsureRemoteDir() error
}

// storageCloser 是可选接口：后端若持有需要显式释放的底层连接（如 SFTP/FTP），
// 调用方应在使用完毕后 defer Close() 以避免连接泄漏。webdav/r2 等基于 HTTP 的后端无需实现。
type storageCloser interface {
	Close() error
}

// ─── 同步快照 ─────────────────────────────────────────────

// SyncSnapshot 同步快照，包含连接和快捷命令等所有可同步数据
type SyncSnapshot struct {
	Connections            []Connection           `json:"connections"`
	Credentials            []Credential           `json:"credentials"`
	QuickCommands          string                 `json:"quick_commands"`
	AIProviders            []aitypes.AIProviderProfile `json:"ai_providers"`
	AIGlobalSettings       *aitypes.AIGlobalSettings   `json:"-"` // ponytail: ai_global_settings 不再同步, 字段保留仅供本地读写
	ProxyNodes             []aitypes.AIProxyNode       `json:"proxy_nodes"`
	// PortForwards 端口映射持久化(按 serverId 分组)。不同设备端口占用不同, 不同步。
	PortForwards map[string][]PersistedPortForward `json:"-"` // ponytail: 端口映射不同步, 设备运行时状态
	// deleted_* 始终写出（含 []），与安卓 desktopSnapshotJson 一致，避免「字段缺失 vs 空数组」语义分叉。
	DeletedConnections []SyncTombstone `json:"deleted_connections"`
	DeletedCredentials []SyncTombstone `json:"deleted_credentials"`
	// TombstonePrunedBefore：清理删除记录水位线。合并时远端 deleted_at 早于此值的墓碑丢弃。
	TombstonePrunedBefore int64 `json:"tombstone_pruned_before,omitempty"`
	SnapshotTime          int64 `json:"snapshot_time,omitempty"` // 快照总时间戳（Unix 毫秒），用于判断同步方向
	HasCredentials         bool                   `json:"-"`
	HasQuickCommands       bool                   `json:"-"`
	HasAIProviders         bool                   `json:"-"`
	HasAIGlobalSettings    bool                   `json:"-"`
	HasProxyNodes          bool                   `json:"-"`
	HasPortForwards        bool                   `json:"-"`
	HasDeletedConnections  bool                   `json:"-"`
	HasDeletedCredentials  bool                   `json:"-"`
	HasTombstonePrunedBefore bool                 `json:"-"`
}

func (s *SyncSnapshot) UnmarshalJSON(data []byte) error {
	type alias SyncSnapshot
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var snap alias
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}
	*s = SyncSnapshot(snap)
	_, s.HasCredentials = raw["credentials"]
	_, s.HasQuickCommands = raw["quick_commands"]
	_, s.HasAIProviders = raw["ai_providers"]
	_, s.HasAIGlobalSettings = raw["ai_global_settings"]
	s.HasAIGlobalSettings = false // ponytail: ai_global_settings 不再同步, 强制忽略远端
	_, s.HasProxyNodes = raw["proxy_nodes"]
	_, s.HasPortForwards = raw["port_forwards"]
	s.HasPortForwards = false // ponytail: 端口映射不同步, 强制忽略远端
	_, s.HasDeletedConnections = raw["deleted_connections"]
	_, s.HasDeletedCredentials = raw["deleted_credentials"]
	_, s.HasTombstonePrunedBefore = raw["tombstone_pruned_before"]
	return nil
}

// ─── 共享解密/解析 ─────────────────────────────────────────

// decryptAndParseSnapshot 解析同步备份：先试明文 JSON，失败后用恢复密码解密 LUMETERM2。
func (c *ConfigManager) decryptAndParseSnapshot(data string, password string) (*SyncSnapshot, error) {
	// ponytail: 先试明文（新默认），不行再按密文解密
	var snap SyncSnapshot
	if err := json.Unmarshal([]byte(data), &snap); err == nil && snap.Connections != nil {
		return &snap, nil
	}
	var conns []Connection
	if err := json.Unmarshal([]byte(data), &conns); err == nil && len(conns) > 0 {
		return &SyncSnapshot{Connections: conns}, nil
	}
	trimmed := strings.TrimSpace(data)
	if !hasBackupPrefix(trimmed) {
		return nil, fmt.Errorf("不支持的备份格式：仅支持明文 JSON 与 LUMETERM2 密文（.lumeterm2/.lumin2）")
	}
	if password == "" {
		return nil, fmt.Errorf("%w：LUMETERM2 备份需要恢复密码", errRecoveryPassword)
	}
	decrypted, err := decryptLUMETERM2(trimmed, password)
	if err != nil {
		return nil, fmt.Errorf("LUMETERM2 解密失败：%w", err)
	}
	// 尝试新格式（快照）
	if err := json.Unmarshal([]byte(decrypted), &snap); err == nil && snap.Connections != nil {
		return &snap, nil
	}
	// 回退旧格式（纯连接列表）
	if err := json.Unmarshal([]byte(decrypted), &conns); err != nil {
		return nil, fmt.Errorf("解析备份文件出错：%w", err)
	}
	return &SyncSnapshot{Connections: conns}, nil
}

// ─── 共享合并/比较 ─────────────────────────────────────────

// connsEqual 比较两个连接列表是否内容一致（按 ID 建 map 逐字段比较）
func connsEqual(a, b []Connection) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]Connection, len(a))
	for _, c := range a {
		n := normalizeConnectionForSync(c)
		m[n.ID] = n
	}
	for _, c := range b {
		n := normalizeConnectionForSync(c)
		e, ok := m[n.ID]
		if !ok {
			return false
		}
		if e != n {
			return false
		}
	}
	return true
}

// snapshotEqual 比较本地快照和远端快照是否一致
func snapshotEqual(s1, s2 *SyncSnapshot) bool {
	if s1 == nil || s2 == nil {
		return s1 == nil && s2 == nil
	}
	if !connsEqual(s1.Connections, s2.Connections) {
		return false
	}
	if !credsEqual(s1.Credentials, s2.Credentials) {
		return false
	}
	if !quickCmdsEqual(s1.QuickCommands, s2.QuickCommands) {
		return false
	}
	if !aiProvidersEqual(s1.AIProviders, s2.AIProviders) {
		return false
	}
	if !aiGlobalSettingsEqual(s1.AIGlobalSettings, s2.AIGlobalSettings) {
		return false
	}
	if !aiProxyNodesEqual(s1.ProxyNodes, s2.ProxyNodes) {
		return false
	}
	// 墓碑必须参与比较：否则 all 模式「只合并到新删除记录」会被当成无变化而跳过上传
	if !tombstonesEqual(s1.DeletedConnections, s2.DeletedConnections) {
		return false
	}
	if !tombstonesEqual(s1.DeletedCredentials, s2.DeletedCredentials) {
		return false
	}
	if s1.TombstonePrunedBefore != s2.TombstonePrunedBefore {
		return false
	}
	return true
}

// credsEqual 比较两个凭据列表是否内容一致（规范化后，空 privateKey 等与缺失等价）
func credsEqual(a, b []Credential) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]Credential, len(a))
	for _, c := range a {
		n := normalizeCredentialForSync(c)
		m[n.ID] = n
	}
	for _, c := range b {
		n := normalizeCredentialForSync(c)
		e, ok := m[n.ID]
		if !ok {
			return false
		}
		if e != n {
			return false
		}
	}
	return true
}

func aiProvidersEqual(a, b []aitypes.AIProviderProfile) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]aitypes.AIProviderProfile, len(a))
	for _, p := range a {
		m[p.ID] = p
	}
	for _, p := range b {
		e, ok := m[p.ID]
		if !ok || !reflect.DeepEqual(e, p) {
			return false
		}
	}
	return true
}

// AIProvidersEqual 导出包装器
func AIProvidersEqual(a, b []aitypes.AIProviderProfile) bool {
	return aiProvidersEqual(a, b)
}

func normalizeAIGlobalSettingsForCompare(settings aitypes.AIGlobalSettings) aitypes.AIGlobalSettings {
	settings.CurrentProviderID = strings.TrimSpace(settings.CurrentProviderID)
	settings.AIRequestProxyID = strings.TrimSpace(settings.AIRequestProxyID)
	settings.CollaborationExtraPrompt = strings.TrimSpace(strings.ReplaceAll(settings.CollaborationExtraPrompt, "\r\n", "\n"))
	settings.ProxyNodes = nil
	settings.UpdatedAt = 0
	if settings.AllowedCommands == nil {
		settings.AllowedCommands = []string{}
	}
	if settings.DeniedCommands == nil {
		settings.DeniedCommands = []string{}
	}
	if settings.SlashCommands == nil {
		settings.SlashCommands = []aitypes.AISlashCommand{}
	}
	// omitempty 上传后远端缺失字段解成 nil；本地 Load 会规范成空切片。比较必须等价，否则每次启动都误判 cloudChanged。
	if settings.CollaborationPromptPresets == nil {
		settings.CollaborationPromptPresets = []aitypes.AICollaborationPromptPreset{}
	}
	return settings
}

func aiGlobalSettingsEqual(a, b *aitypes.AIGlobalSettings) bool {
	// ponytail: ai_global_settings 不再同步, 始终返回 true 跳过变更检测
	return true
}

func aiGlobalSettingsDiffSummary(a, b *aitypes.AIGlobalSettings) string {
	if a == nil || b == nil {
		return fmt.Sprintf("nil a=%v b=%v", a == nil, b == nil)
	}
	aa := normalizeAIGlobalSettingsForCompare(*a)
	bb := normalizeAIGlobalSettingsForCompare(*b)
	parts := []string{}
	if aa.UpdatedAt != bb.UpdatedAt {
		parts = append(parts, fmt.Sprintf("updatedAt local=%d remote=%d", aa.UpdatedAt, bb.UpdatedAt))
	}
	if aa.CurrentProviderID != bb.CurrentProviderID {
		parts = append(parts, fmt.Sprintf("provider local=%q remote=%q", aa.CurrentProviderID, bb.CurrentProviderID))
	}
	if aa.AIRequestProxyID != bb.AIRequestProxyID {
		parts = append(parts, fmt.Sprintf("proxyId local=%q remote=%q", aa.AIRequestProxyID, bb.AIRequestProxyID))
	}
	if aa.AutoApprovalEnabled != bb.AutoApprovalEnabled || aa.AlwaysAllowReadOnly != bb.AlwaysAllowReadOnly || aa.AlwaysAllowWrite != bb.AlwaysAllowWrite || aa.AlwaysAllowExecute != bb.AlwaysAllowExecute {
		parts = append(parts, "approval flags differ")
	}
	if !reflect.DeepEqual(aa.AllowedCommands, bb.AllowedCommands) || !reflect.DeepEqual(aa.DeniedCommands, bb.DeniedCommands) || !reflect.DeepEqual(aa.SlashCommands, bb.SlashCommands) {
		parts = append(parts, fmt.Sprintf("commands allowed=%d/%d denied=%d/%d slash=%d/%d", len(aa.AllowedCommands), len(bb.AllowedCommands), len(aa.DeniedCommands), len(bb.DeniedCommands), len(aa.SlashCommands), len(bb.SlashCommands)))
	}
	if !reflect.DeepEqual(aa.CollaborationPromptPresets, bb.CollaborationPromptPresets) || aa.CollaborationExtraPrompt != bb.CollaborationExtraPrompt {
		parts = append(parts, fmt.Sprintf("collaboration presets=%d/%d extra=%q/%q", len(aa.CollaborationPromptPresets), len(bb.CollaborationPromptPresets), aa.CollaborationExtraPrompt, bb.CollaborationExtraPrompt))
	}
	if len(parts) == 0 && !reflect.DeepEqual(aa, bb) {
		parts = append(parts, "other fields differ")
	}
	return strings.Join(parts, "; ")
}

func aiProxyNodesEqual(a, b []aitypes.AIProxyNode) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]aitypes.AIProxyNode, len(a))
	for _, n := range a {
		m[n.ID] = n
	}
	for _, n := range b {
		e, ok := m[n.ID]
		if !ok || !reflect.DeepEqual(e, n) {
			return false
		}
	}
	return true
}

func normalizeSyncAIProviders(providers []aitypes.AIProviderProfile) []aitypes.AIProviderProfile {
	if providers == nil {
		return []aitypes.AIProviderProfile{}
	}
	now := time.Now().UnixMilli()
	normalized := make([]aitypes.AIProviderProfile, 0, len(providers))
	seen := make(map[string]struct{}, len(providers))
	for i, provider := range providers {
		provider.ID = strings.TrimSpace(provider.ID)
		if provider.ID == "" {
			provider.ID = fmt.Sprintf("ai-provider-%d-%d", now, i)
		}
		if _, ok := seen[provider.ID]; ok {
			continue
		}
		seen[provider.ID] = struct{}{}
		provider.Name = strings.TrimSpace(provider.Name)
		if provider.Name == "" {
			provider.Name = "未命名供应商"
		}
		provider.BaseURL = strings.TrimSpace(provider.BaseURL)
		provider.APIKey = strings.TrimSpace(provider.APIKey)
		provider.DedicatedProxyID = strings.TrimSpace(provider.DedicatedProxyID)
		if provider.UpdatedAt <= 0 {
			provider.UpdatedAt = now
		}
		normalized = append(normalized, provider)
	}
	return normalized
}

func normalizeSyncAIProxyNodes(nodes []aitypes.AIProxyNode) []aitypes.AIProxyNode {
	if nodes == nil {
		return []aitypes.AIProxyNode{}
	}
	now := time.Now().UnixMilli()
	normalized := make([]aitypes.AIProxyNode, 0, len(nodes))
	seen := make(map[string]struct{}, len(nodes))
	for i, node := range nodes {
		node.Host = strings.TrimSpace(node.Host)
		if node.Host == "" {
			continue
		}
		node.ID = strings.TrimSpace(node.ID)
		if node.ID == "" {
			node.ID = fmt.Sprintf("proxy-%d-%d", now, i)
		}
		if _, ok := seen[node.ID]; ok {
			continue
		}
		seen[node.ID] = struct{}{}
		node.Name = strings.TrimSpace(node.Name)
		node.Type = strings.ToLower(strings.TrimSpace(node.Type))
		if node.Type != "http" {
			node.Type = "socks5"
		}
		if node.Port <= 0 || node.Port > 65535 {
			node.Port = 1080
		}
		node.Username = strings.TrimSpace(node.Username)
		if node.UpdatedAt <= 0 {
			node.UpdatedAt = now
		}
		normalized = append(normalized, node)
	}
	return normalized
}

// ─── 共享远端操作 ─────────────────────────────────────────

// isBackupName 识别远端备份文件（.lumin2 为旧扩展名读兼容，退役计划见 config.go legacyLumin2Prefix 注释）
func isBackupName(name string) bool {
	return strings.HasPrefix(name, "connections_backup_") && (strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".lumeterm2") || strings.HasSuffix(name, ".lumin2"))
}

func isNoBackupError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "云端没有备份文件")
}

// isUnreadableBackupContentError 备份文件已读到但内容损坏/截断（非密码错、非网络错）。
// 改密/关加密时跳过该端远端合并，避免坏文件卡死。
func isUnreadableBackupContentError(err error) bool {
	if err == nil || errors.Is(err, errRecoveryPassword) || errors.Is(err, errRecoveryPasswordResetRequired) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "LUMETERM2 Base64 无效") ||
		strings.Contains(msg, "LUMETERM2 数据长度不足") ||
		strings.Contains(msg, "缺少 LUMETERM2 前缀") ||
		strings.Contains(msg, "不支持的 LUMETERM2 版本") ||
		strings.Contains(msg, "LUMETERM2 迭代次数无效") ||
		strings.Contains(msg, "解析最新备份") && (strings.Contains(msg, "LUMETERM2") || strings.Contains(msg, "invalid") || strings.Contains(msg, "截断"))
}

// fetchLatestBackup 从远端严格下载并解密最新备份，不回退旧文件。
func (c *ConfigManager) fetchLatestBackup(s RemoteStorage, password string) (*SyncSnapshot, error) {
	files, err := s.ListFiles()
	if err != nil {
		return nil, fmt.Errorf("读取远程目录失败：%w", err)
	}

	var backups []string
	for _, f := range files {
		if !f.IsDir && isBackupName(f.Name) {
			backups = append(backups, f.Name)
		}
	}
	if len(backups) == 0 {
		return nil, fmt.Errorf("云端没有备份文件")
	}
	sort.Sort(sort.Reverse(sort.StringSlice(backups)))

	data, err := s.ReadFile(backups[0])
	if err != nil {
		return nil, fmt.Errorf("读取最新备份 %s 失败：%w", backups[0], err)
	}
	snap, err := c.decryptAndParseSnapshot(string(data), password)
	if err != nil {
		return nil, fmt.Errorf("解析最新备份 %s 失败：%w", backups[0], err)
	}
	if snap.Connections == nil {
		return nil, fmt.Errorf("最新备份 %s 缺少连接数据", backups[0])
	}
	return snap, nil
}

func (c *ConfigManager) localAIGlobalSettingsForSync() *aitypes.AIGlobalSettings {
	// ponytail: ai_global_settings 不再同步, 返回 nil 使上传快照不含此字段
	return nil
}

// backupConnections 上传本地所有可同步数据到远端，同时清理超出 maxBackups 的旧备份。
// 加密策略（与导出一致）：设置了恢复密码则上传 LUMETERM2 .lumeterm2；否则明文 JSON 上传 .json。
func (c *ConfigManager) backupConnections(s RemoteStorage, maxBackups int) (map[string]interface{}, error) {
	// 普通备份：上传前并远端墓碑，避免本机空墓碑盖掉云端删除记录
	return c.uploadSnapshot(s, c.localSyncSnapshot(), c.GetRecoveryPassword(), maxBackups, true)
}

func (c *ConfigManager) localSyncSnapshot() *SyncSnapshot {
	store := c.loadTombstoneStore()
	// 应用水位线：清理后的旧墓碑不应再出现在上传包里
	conns := filterTombstonesNotBefore(store.Connections, store.PrunedBefore)
	creds := filterTombstonesNotBefore(store.Credentials, store.PrunedBefore)
	// 上传包用规范化连接/凭据，避免与安卓默认字段形态互相覆盖造成乒乓
	if conns == nil {
		conns = []SyncTombstone{}
	}
	if creds == nil {
		creds = []SyncTombstone{}
	}
	return &SyncSnapshot{
		Connections:              normalizeConnectionsForSync(c.GetConnections()),
		Credentials:              normalizeCredentialsForSync(c.GetCredentials()),
		QuickCommands:            c.loadRawFile(c.quickCmdFile),
		AIProviders:              c.GetAIProviderRegistry().Providers,
		AIGlobalSettings:         c.localAIGlobalSettingsForSync(),
		ProxyNodes:               c.GetAIProxyNodes(),
		DeletedConnections:       conns,
		DeletedCredentials:       creds,
		TombstonePrunedBefore:    store.PrunedBefore,
		SnapshotTime:             c.loadSnapshotTime(),
		HasCredentials:           true,
		HasQuickCommands:         true,
		HasAIProviders:           true,
		HasAIGlobalSettings:      false, // ponytail: ai_global_settings 不再同步
		HasProxyNodes:            true,
		HasDeletedConnections:    true,
		HasDeletedCredentials:    true,
		HasTombstonePrunedBefore: true,
	}
}

// uploadSnapshot 上传快照。
// mergeRemoteTombs=true：先与远端最新包做墓碑并集（防止误覆盖删除）。
// mergeRemoteTombs=false：严格按 snap 内容上传（清理删除记录必须用这个，否则清完又并回来）。
func (c *ConfigManager) uploadSnapshot(s RemoteStorage, snap *SyncSnapshot, password string, maxBackups int, mergeRemoteTombs bool) (map[string]interface{}, error) {
	if snap != nil && mergeRemoteTombs {
		if remote, err := c.fetchLatestBackup(s, password); err == nil && remote != nil {
			// 水位线取本包与远端较大值，避免把已清理的旧墓碑又并回来
			pb := snap.TombstonePrunedBefore
			if remote.TombstonePrunedBefore > pb {
				pb = remote.TombstonePrunedBefore
			}
			snap.TombstonePrunedBefore = pb
			snap.DeletedConnections = filterTombstonesNotBefore(
				pruneConnectionTombstones(
					mergeConnectionTombstones(snap.DeletedConnections, remote.DeletedConnections),
					snap.Connections,
				),
				pb,
			)
			snap.DeletedCredentials = filterTombstonesNotBefore(
				pruneCredentialTombstones(
					mergeCredentialTombstones(snap.DeletedCredentials, remote.DeletedCredentials),
					snap.Credentials,
				),
				pb,
			)
		}
	}
	if snap != nil {
		// 上传线格式稳定：规范化连接/凭据；墓碑 nil → [] 始终写出
		snap.Connections = normalizeConnectionsForSync(snap.Connections)
		snap.Credentials = normalizeCredentialsForSync(snap.Credentials)
		if snap.DeletedConnections == nil {
			snap.DeletedConnections = []SyncTombstone{}
		}
		if snap.DeletedCredentials == nil {
			snap.DeletedCredentials = []SyncTombstone{}
		}
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化同步快照失败：%w", err)
	}

	timestamp := time.Now().Format("20060102_150405.000_-0700")
	var payload []byte
	var fileName string
	if password != "" {
		encrypted, err := encryptLUMETERM2(string(data), password)
		if err != nil {
			return nil, fmt.Errorf("加密同步快照失败：%w", err)
		}
		payload = []byte(encrypted)
		fileName = fmt.Sprintf("connections_backup_%s.lumeterm2", timestamp)
	} else {
		payload = data
		fileName = fmt.Sprintf("connections_backup_%s.json", timestamp)
	}
	result := map[string]interface{}{
		"path":  fileName,
		"time":  time.Now().Format("2006-01-02 15:04:05 -0700"),
		"count": len(snap.Connections),
	}
	if err := s.WriteFile(fileName, payload); err != nil {
		return result, err
	}
	if maxBackups > 0 {
		if err := c.pruneOldBackups(s, maxBackups); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// pruneOldBackups 删除超出数量的最旧备份文件
func (c *ConfigManager) pruneOldBackups(s RemoteStorage, maxBackups int) error {
	files, err := s.ListFiles()
	if err != nil {
		return fmt.Errorf("读取备份列表失败: %w", err)
	}

	type backupEntry struct {
		name string
	}
	var backups []backupEntry
	for _, f := range files {
		if !f.IsDir && isBackupName(f.Name) {
			backups = append(backups, backupEntry{f.Name})
		}
	}
	if len(backups) <= maxBackups {
		return nil
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].name < backups[j].name
	})
	for i := 0; i < len(backups)-maxBackups; i++ {
		if err := s.DeleteFile(backups[i].name); err != nil {
			return fmt.Errorf("删除旧备份 %s 失败: %w", backups[i].name, err)
		}
	}
	return nil
}

// listBackupFiles 列出远端备份文件及其元信息
func (c *ConfigManager) listBackupFiles(s RemoteStorage) ([]map[string]interface{}, error) {
	files, err := s.ListFiles()
	if err != nil {
		return nil, err
	}

	var backups []map[string]interface{}
	for _, f := range files {
		if !f.IsDir && isBackupName(f.Name) {
			// 从文件名解析时间：优先新格式（带时区），fallback 旧格式（无时区用本地时间）
			timeStr := ""
			base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(f.Name, ".lumeterm2"), ".lumin2"), ".json")
			if t, err := time.Parse("connections_backup_20060102_150405.000_-0700", base); err == nil {
				timeStr = t.Local().Format("2006-01-02 15:04:05 -0700")
			} else if t, err := time.ParseInLocation("connections_backup_20060102_150405.000", base, time.Local); err == nil {
				timeStr = t.Format("2006-01-02 15:04:05 -0700")
			} else {
				timeStr = f.ModTime.In(time.Local).Format("2006-01-02 15:04:05 -0700")
			}
			backups = append(backups, map[string]interface{}{
				"name": f.Name,
				"size": f.Size,
				"time": timeStr,
			})
		}
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i]["name"].(string) > backups[j]["name"].(string)
	})
	return backups, nil
}

// ─── 同步入口 ─────────────────────────────────────────────

// syncFromProvider 手动合并同步：下载远端 → 合并连接+命令 → 保存本地 → 条件上传
func (c *ConfigManager) syncFromProvider(s RemoteStorage, maxBackups int, password string, provider string) (map[string]interface{}, error) {
	remoteSnap, err := c.fetchLatestBackup(s, password)
	if err != nil {
		return nil, err
	}

	// 合并连接（重叠按 LastModified 取最新，单侧独有按 lastSyncTime 判断删除；墓碑传播真删）
	localConns := c.GetConnections()
	lastSyncTime := c.loadLastSyncTime(provider)
	localTombs := c.loadTombstoneStore()
	mergedConnTombs, mergedCredTombs, mergedPrunedBefore := mergeTombsWithPruneWatermark(
		localTombs.Connections, remoteSnap.DeletedConnections,
		localTombs.Credentials, remoteSnap.DeletedCredentials,
		localTombs.PrunedBefore, remoteSnap.TombstonePrunedBefore,
	)
	deduped, dedupTombs := c.mergeWithDeletionPropagation(localConns, remoteSnap.Connections, lastSyncTime, mergedConnTombs)
	mergedConnTombs = pruneConnectionTombstones(mergeConnectionTombstones(mergedConnTombs, dedupTombs), deduped)
	// 加锁保存并失效缓存（saveConnectionsFile 要求调用方持有 c.mu）
	c.mu.Lock()
	if err := c.saveConnectionsFile(deduped); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("保存连接失败: %w", err)
	}
	c.connCacheDirty = true

	// 合并凭据
	var mergedCreds []Credential
	if remoteSnap.HasCredentials {
		localCreds := c.getCredentialsLocked()
		var credInferred []SyncTombstone
		mergedCreds, credInferred = c.mergeCredentials(localCreds, remoteSnap.Credentials, lastSyncTime, mergedCredTombs)
		mergedCredTombs = pruneCredentialTombstones(mergeCredentialTombstones(mergedCredTombs, credInferred), mergedCreds)
		if err := c.saveCredentialsFile(mergedCreds); err != nil {
			c.mu.Unlock()
			return nil, fmt.Errorf("保存凭据失败: %w", err)
		}
		c.credCacheDirty = true
	} else {
		var credInferred []SyncTombstone
		_, credInferred = c.mergeCredentials(c.getCredentialsLocked(), nil, lastSyncTime, mergedCredTombs)
		mergedCredTombs = pruneCredentialTombstones(mergeCredentialTombstones(mergedCredTombs, credInferred), c.getCredentialsLocked())
	}
	if err := c.saveTombstoneStore(syncTombstoneStore{Connections: mergedConnTombs, Credentials: mergedCredTombs, PrunedBefore: mergedPrunedBefore}); err != nil {
		log.Printf("[syncFromProvider] save tombstones: %v", err)
	}
	c.mu.Unlock()
	c.CleanupOrphanedHistory() // 清理已不存在的连接的历史文件

	// 合并快捷命令（按 last_modified 取最新，单侧独有按 lastSyncTime 判断删除）
	localQuickCmds := c.loadRawFile(c.quickCmdFile)
	mergedQuickCmds := localQuickCmds
	if remoteSnap.HasQuickCommands {
		mergedQuickCmds = c.mergeQuickCommands(localQuickCmds, remoteSnap.QuickCommands, lastSyncTime)
		if err := atomicWriteFile(c.quickCmdFile, []byte(mergedQuickCmds), 0600); err != nil {
			log.Printf("[syncFromProvider] failed to write quick commands: %v", err)
		}
	}

	localAIProviders := c.GetAIProviderRegistry().Providers
	mergedAIProviders := c.mergeAIProviders(localAIProviders, remoteSnap.AIProviders, lastSyncTime)
	if remoteSnap.HasAIProviders {
		if err := c.SaveAIProviderRegistry(aitypes.AIProviderRegistry{Providers: mergedAIProviders}); err != nil {
			log.Printf("[syncFromProvider] failed to save AI providers: %v", err)
		}
	}
	localAIGlobalSettings := c.localAIGlobalSettingsForSync()
	var mergedAIGlobalSettings aitypes.AIGlobalSettings
	if localAIGlobalSettings != nil {
		mergedAIGlobalSettings = *localAIGlobalSettings
	}
	if remoteSnap.HasAIGlobalSettings {
		mergedAIGlobalSettings = mergeAIGlobalSettings(mergedAIGlobalSettings, remoteSnap.AIGlobalSettings)
		if err := c.SaveAIGlobalSettings(mergedAIGlobalSettings); err != nil {
			log.Printf("[syncFromProvider] failed to save AI global settings: %v", err)
		}
	}
	localProxyNodes := c.GetAIProxyNodes()
	mergedProxyNodes := c.mergeAIProxyNodes(localProxyNodes, remoteSnap.ProxyNodes, lastSyncTime)
	if remoteSnap.HasProxyNodes {
		if err := c.SaveAIProxyNodes(mergedProxyNodes); err != nil {
			log.Printf("[syncFromProvider] failed to save AI proxy nodes: %v", err)
		}
	}

	var backupResult interface{}
	changed := !connsEqual(deduped, remoteSnap.Connections) ||
		(remoteSnap.HasQuickCommands && !quickCmdsEqual(mergedQuickCmds, remoteSnap.QuickCommands)) ||
		(mergedCreds != nil && !credsEqual(mergedCreds, remoteSnap.Credentials)) ||
		(remoteSnap.HasAIProviders && !aiProvidersEqual(mergedAIProviders, remoteSnap.AIProviders)) ||
		(remoteSnap.HasAIGlobalSettings && !aiGlobalSettingsEqual(&mergedAIGlobalSettings, remoteSnap.AIGlobalSettings)) ||
		(remoteSnap.HasProxyNodes && !aiProxyNodesEqual(mergedProxyNodes, remoteSnap.ProxyNodes)) ||
		!tombstonesEqual(mergedConnTombs, remoteSnap.DeletedConnections) ||
		!tombstonesEqual(mergedCredTombs, remoteSnap.DeletedCredentials) ||
		mergedPrunedBefore != remoteSnap.TombstonePrunedBefore
	if changed {
		c.bumpSnapshotTime() // 手动同步后更新总时间戳，确保下次自动同步方向正确
		uploadSnap := c.localSyncSnapshot()
		uploadSnap.Connections = deduped
		if mergedCreds != nil {
			uploadSnap.Credentials = mergedCreds
		}
		uploadSnap.QuickCommands = mergedQuickCmds
		uploadSnap.AIProviders = mergedAIProviders
		uploadSnap.ProxyNodes = mergedProxyNodes
		uploadSnap.DeletedConnections = mergedConnTombs
		uploadSnap.DeletedCredentials = mergedCredTombs
		uploadSnap.TombstonePrunedBefore = mergedPrunedBefore
		uploadSnap.SnapshotTime = c.loadSnapshotTime()
		br, berr := c.uploadSnapshot(s, uploadSnap, password, maxBackups, true)
		if berr != nil {
			return nil, fmt.Errorf("上传合并快照失败: %w", berr)
		}
		backupResult = br
	} else if maxBackups > 0 {
		if err := c.pruneOldBackups(s, maxBackups); err != nil {
			return nil, fmt.Errorf("清理旧备份失败: %w", err)
		}
	}
	c.recordSyncCompleted(provider)

	return map[string]interface{}{
		"success":     true,
		"localCount":  len(localConns),
		"remoteCount": len(remoteSnap.Connections),
		"mergedCount": len(deduped),
		"backup":      backupResult,
	}, nil
}

func snapshotHasQuickCommands(snap *SyncSnapshot) bool {
	return snap.HasQuickCommands
}

func latestSnapshotHasItem(itemUpdatedAt int64, snaps []*SyncSnapshot, hasField func(*SyncSnapshot) bool, contains func(*SyncSnapshot) bool) bool {
	if itemUpdatedAt <= 0 {
		return true
	}
	var latest *SyncSnapshot
	for _, snap := range snaps {
		if hasField(snap) && snap.SnapshotTime > itemUpdatedAt && (latest == nil || snap.SnapshotTime > latest.SnapshotTime) {
			latest = snap
		}
	}
	return latest == nil || contains(latest)
}

func filterRemoteDeletedAIProviders(providers []aitypes.AIProviderProfile, snaps []*SyncSnapshot) []aitypes.AIProviderProfile {
	out := providers[:0]
	for _, provider := range providers {
		if latestSnapshotHasItem(provider.UpdatedAt, snaps, func(snap *SyncSnapshot) bool { return snap.HasAIProviders }, func(snap *SyncSnapshot) bool { return aiProviderInSnapshot(snap.AIProviders, provider.ID) }) {
			out = append(out, provider)
		}
	}
	return out
}

func aiProviderInSnapshot(providers []aitypes.AIProviderProfile, id string) bool {
	for _, provider := range providers {
		if provider.ID == id {
			return true
		}
	}
	return false
}

func filterRemoteDeletedAIProxyNodes(nodes []aitypes.AIProxyNode, snaps []*SyncSnapshot) []aitypes.AIProxyNode {
	out := nodes[:0]
	for _, node := range nodes {
		if latestSnapshotHasItem(node.UpdatedAt, snaps, func(snap *SyncSnapshot) bool { return snap.HasProxyNodes }, func(snap *SyncSnapshot) bool { return aiProxyNodeInSnapshot(snap.ProxyNodes, node.ID) }) {
			out = append(out, node)
		}
	}
	return out
}

func aiProxyNodeInSnapshot(nodes []aitypes.AIProxyNode, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

func (c *ConfigManager) filterRemoteDeletedQuickCommands(raw string, snaps []*SyncSnapshot) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	var arr []interface{}
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return raw
	}
	filtered := filterQuickCommandArray(arr, snaps)
	data, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return raw
	}
	return string(data)
}

func filterQuickCommandArray(arr []interface{}, snaps []*SyncSnapshot) []interface{} {
	out := arr[:0]
	for _, item := range arr {
		cmd, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if children, ok := cmd["children"].([]interface{}); ok {
			cmd["children"] = filterQuickCommandArray(children, snaps)
		}
		key := cmdKey(cmd)
		if latestSnapshotHasItem(cmdLastModified(cmd), snaps, snapshotHasQuickCommands, func(snap *SyncSnapshot) bool { return quickCommandInSnapshot(snap.QuickCommands, key) }) {
			out = append(out, cmd)
		}
	}
	return out
}

func quickCommandInSnapshot(raw, key string) bool {
	var arr []interface{}
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return false
	}
	return quickCommandInArray(arr, key)
}

func quickCommandInArray(arr []interface{}, key string) bool {
	for _, item := range arr {
		cmd, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if cmdKey(cmd) == key {
			return true
		}
		if children, ok := cmd["children"].([]interface{}); ok && quickCommandInArray(children, key) {
			return true
		}
	}
	return false
}

// syncAllProviders 合并同步到多个后端。
// flags[0]=strict：任一下载失败整批失败。
// flags[1]=blockFirstContactTombstone：AutoSync 用；首次接触某后端且墓碑会静默删远端项时跳过。
func (c *ConfigManager) syncAllProviders(entries []providerEntry, password string, flags ...bool) (map[string]interface{}, error) {
	if len(entries) == 0 {
		return nil, errors.New("没有可用同步目标")
	}
	_ = len(flags) > 0 && flags[0] // strict 保留 flags 槽位兼容
	blockFirstContactTombstone := len(flags) > 1 && flags[1]
	defer func() {
		for _, p := range entries {
			if cl, ok := p.storage.(storageCloser); ok {
				cl.Close()
			}
		}
	}()

	providerNames := make([]string, 0, len(entries))
	for _, p := range entries {
		providerNames = append(providerNames, p.provider)
	}
	// 多后端合并时取最小 lastSync：任一后端从未同步过则返回 0，避免误删本地新增
	lastSyncTime := c.loadLastSyncTimeMin(providerNames...)
	localConns := c.GetConnections()
	localCreds := c.GetCredentials()
	localQuickCmds := c.loadRawFile(c.quickCmdFile)
	localAIProviders := c.GetAIProviderRegistry().Providers
	localAIGlobalSettings := c.localAIGlobalSettingsForSync()
	localProxyNodes := c.GetAIProxyNodes()

	remoteConns := []Connection{}
	var remoteCreds []Credential
	var remoteConnTombs []SyncTombstone
	var remoteCredTombs []SyncTombstone
	remoteQuickCmds := ""
	var remoteAIProviders []aitypes.AIProviderProfile
	var remoteAIGlobalSettings aitypes.AIGlobalSettings
	var remoteProxyNodes []aitypes.AIProxyNode
	remoteHasCreds := false
	remoteHasQuick := false
	remoteHasAIProviders := false
	remoteHasAIGlobalSettingsValue := false
	remoteHasProxyNodes := false
	type providerSnapshot struct {
		providerEntry
		snapshot *SyncSnapshot
		noBackup bool
	}
	downloaded := 0
	var remoteSnaps []*SyncSnapshot
	providerSnaps := make([]providerSnapshot, 0, len(entries))

	for _, p := range entries {
		remoteSnap, err := c.fetchLatestBackup(p.storage, password)
		if isNoBackupError(err) {
			providerSnaps = append(providerSnaps, providerSnapshot{providerEntry: p, noBackup: true})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s 下载失败：%w", p.provider, err)
		}
		downloaded++
		remoteSnaps = append(remoteSnaps, remoteSnap)
		providerSnaps = append(providerSnaps, providerSnapshot{providerEntry: p, snapshot: remoteSnap})
		// AutoSync 且首次接触该后端：墓碑会静默删远端项时整批跳过，改由手动合并确认
		if blockFirstContactTombstone && c.shouldSkipAutoSyncForTombstoneConflict(p.provider, remoteSnap) {
			log.Printf("[syncAllProviders] skip auto: tombstone conflicts on %s need manual sync", p.provider)
			c.emitSyncEvent("sync-status", map[string]interface{}{
				"action":   "skip",
				"reason":   "tombstone_conflict_needs_manual_sync",
				"provider": p.provider,
			})
			return map[string]interface{}{
				"success":  true,
				"skipped":  true,
				"reason":   "tombstone_conflict_needs_manual_sync",
				"provider": p.provider,
			}, nil
		}
		var remoteDedupTombs []SyncTombstone
		remoteConns, remoteDedupTombs = c.mergeWithDeletionPropagation(remoteConns, remoteSnap.Connections, -1, remoteSnap.DeletedConnections)
		if remoteSnap.HasDeletedConnections {
			remoteConnTombs = mergeConnectionTombstones(remoteConnTombs, remoteSnap.DeletedConnections)
		}
		if len(remoteDedupTombs) > 0 {
			remoteConnTombs = mergeConnectionTombstones(remoteConnTombs, remoteDedupTombs)
		}
		if remoteSnap.HasCredentials {
			remoteHasCreds = true
			var credInferred []SyncTombstone
			remoteCreds, credInferred = c.mergeCredentials(remoteCreds, remoteSnap.Credentials, -1, remoteSnap.DeletedCredentials)
			remoteCredTombs = mergeCredentialTombstones(remoteCredTombs, credInferred)
		}
		if remoteSnap.HasDeletedCredentials {
			remoteCredTombs = mergeCredentialTombstones(remoteCredTombs, remoteSnap.DeletedCredentials)
		}
		if remoteSnap.HasQuickCommands {
			remoteHasQuick = true
			remoteQuickCmds = c.mergeQuickCommands(remoteQuickCmds, remoteSnap.QuickCommands, -1)
		}
		if remoteSnap.HasAIProviders {
			remoteHasAIProviders = true
			remoteAIProviders = c.mergeAIProviders(remoteAIProviders, remoteSnap.AIProviders, -1)
		}
		if remoteSnap.HasAIGlobalSettings {
			if remoteSnap.AIGlobalSettings != nil {
				remoteHasAIGlobalSettingsValue = true
				remoteAIGlobalSettings = mergeAIGlobalSettings(remoteAIGlobalSettings, remoteSnap.AIGlobalSettings)
			}
		}
		if remoteSnap.HasProxyNodes {
			remoteHasProxyNodes = true
			remoteProxyNodes = c.mergeAIProxyNodes(remoteProxyNodes, remoteSnap.ProxyNodes, -1)
		}
	}

	// 连接/凭据删除已由墓碑负责；不再用 snapshot_time 启发式二次过滤，避免与墓碑语义冲突。
	// 快捷命令 / AI / 代理仍无墓碑，保留旧过滤。
	remoteQuickCmds = c.filterRemoteDeletedQuickCommands(remoteQuickCmds, remoteSnaps)
	remoteAIProviders = filterRemoteDeletedAIProviders(remoteAIProviders, remoteSnaps)
	remoteProxyNodes = filterRemoteDeletedAIProxyNodes(remoteProxyNodes, remoteSnaps)

	localTombs := c.loadTombstoneStore()
	var remotePrunedBefore int64
	for _, snap := range remoteSnaps {
		if snap != nil && snap.TombstonePrunedBefore > remotePrunedBefore {
			remotePrunedBefore = snap.TombstonePrunedBefore
		}
	}
	mergedConnTombs, mergedCredTombs, mergedPrunedBefore := mergeTombsWithPruneWatermark(
		localTombs.Connections, remoteConnTombs,
		localTombs.Credentials, remoteCredTombs,
		localTombs.PrunedBefore, remotePrunedBefore,
	)
	var dedupTombs []SyncTombstone
	mergedConns := localConns
	if downloaded > 0 {
		mergedConns, dedupTombs = c.mergeWithDeletionPropagation(localConns, remoteConns, lastSyncTime, mergedConnTombs)
	} else {
		mergedConns, dedupTombs = c.mergeWithDeletionPropagation(localConns, nil, lastSyncTime, mergedConnTombs)
	}
	mergedCreds := localCreds
	var credInferred []SyncTombstone
	if remoteHasCreds {
		mergedCreds, credInferred = c.mergeCredentials(localCreds, remoteCreds, lastSyncTime, mergedCredTombs)
	} else {
		mergedCreds, credInferred = c.mergeCredentials(localCreds, nil, lastSyncTime, mergedCredTombs)
	}
	// dedupTombs 含 host 去重挤掉的 id + 启发式删除补写的墓碑
	mergedConnTombs = pruneConnectionTombstones(mergeConnectionTombstones(mergedConnTombs, dedupTombs), mergedConns)
	mergedCredTombs = pruneCredentialTombstones(mergeCredentialTombstones(mergedCredTombs, credInferred), mergedCreds)
	mergedQuickCmds := localQuickCmds
	if remoteHasQuick {
		mergedQuickCmds = c.mergeQuickCommands(localQuickCmds, remoteQuickCmds, lastSyncTime)
	}
	mergedAIProviders := localAIProviders
	if remoteHasAIProviders {
		mergedAIProviders = c.mergeAIProviders(localAIProviders, remoteAIProviders, lastSyncTime)
	}
	var mergedAIGlobalSettings aitypes.AIGlobalSettings
	if localAIGlobalSettings != nil {
		mergedAIGlobalSettings = *localAIGlobalSettings
	}
	if remoteHasAIGlobalSettingsValue {
		mergedAIGlobalSettings = mergeAIGlobalSettings(mergedAIGlobalSettings, &remoteAIGlobalSettings)
	}
	mergedProxyNodes := localProxyNodes
	if remoteHasProxyNodes {
		mergedProxyNodes = c.mergeAIProxyNodes(localProxyNodes, remoteProxyNodes, lastSyncTime)
	}
	if downloaded > 0 {
		mergedQuickCmds = c.filterRemoteDeletedQuickCommands(mergedQuickCmds, remoteSnaps)
		mergedAIProviders = filterRemoteDeletedAIProviders(mergedAIProviders, remoteSnaps)
		mergedProxyNodes = filterRemoteDeletedAIProxyNodes(mergedProxyNodes, remoteSnaps)
	}

	var finalAIGlobalSettings *aitypes.AIGlobalSettings
	if localAIGlobalSettings != nil || remoteHasAIGlobalSettingsValue {
		finalAIGlobalSettings = &mergedAIGlobalSettings
	}
	final := &SyncSnapshot{
		Connections:              mergedConns,
		Credentials:              mergedCreds,
		QuickCommands:            mergedQuickCmds,
		AIProviders:              mergedAIProviders,
		AIGlobalSettings:         finalAIGlobalSettings,
		ProxyNodes:               mergedProxyNodes,
		DeletedConnections:       mergedConnTombs,
		DeletedCredentials:       mergedCredTombs,
		TombstonePrunedBefore:    mergedPrunedBefore,
		SnapshotTime:             c.loadSnapshotTime(),
		HasCredentials:           true,
		HasQuickCommands:         true,
		HasAIProviders:           true,
		HasAIGlobalSettings:      false, // ponytail: ai_global_settings 不再同步
		HasProxyNodes:            true,
		HasDeletedConnections:    true,
		HasDeletedCredentials:    true,
		HasTombstonePrunedBefore: true,
	}
	local := c.localSyncSnapshot()
	localChanged := !snapshotEqual(final, local)
	uploadTargets := make([]providerSnapshot, 0, len(providerSnaps))
	for _, p := range providerSnaps {
		if p.noBackup || !snapshotEqual(final, p.snapshot) {
			uploadTargets = append(uploadTargets, p)
		}
	}
	cloudChanged := len(uploadTargets) > 0
	if !localChanged && !cloudChanged {
		c.recordSyncCompleted(providerNames...)
		return map[string]interface{}{"success": true, "action": "skip", "localCount": len(localConns), "remoteCount": downloaded, "mergedCount": len(mergedConns), "uploaded": 0, "skipped": true}, nil
	}

	final.SnapshotTime = time.Now().UnixMilli()
	var uploaded []uploadedSnapshot
	for _, p := range uploadTargets {
		result, err := c.uploadSnapshot(p.storage, final, password, 0, true)
		name := snapshotUploadPath(result)
		if err == nil && name != "" {
			uploaded = append(uploaded, uploadedSnapshot{p.storage, name})
			continue
		}
		if name != "" {
			_ = p.storage.DeleteFile(name)
		}
		rollbackUploadedSnapshots(uploaded)
		if err == nil {
			err = errors.New("上传结果缺少备份文件名")
		}
		return nil, fmt.Errorf("%s 上传合并快照失败：%w", p.provider, err)
	}
	if err := c.persistSyncSnapshot(final); err != nil {
		rollbackUploadedSnapshots(uploaded)
		return nil, err
	}
	for _, p := range uploadTargets {
		if p.maxBackups > 0 {
			if err := c.pruneOldBackups(p.storage, p.maxBackups); err != nil {
				return nil, fmt.Errorf("%s 清理旧备份失败：%w", p.provider, err)
			}
		}
	}
	c.CleanupOrphanedHistory()
	c.recordSyncCompleted(providerNames...)
	action := "merge"
	if !localChanged {
		action = "upload"
	} else if !cloudChanged {
		action = "download"
	}
	return map[string]interface{}{
		"success":     true,
		"action":      action,
		"localCount":  len(localConns),
		"remoteCount": downloaded,
		"mergedCount": len(mergedConns),
		"uploaded":    len(uploaded),
	}, nil
}

// emitSyncEvent 向前端发送同步状态事件（ponytail: wailsCtx 可能为 nil，静默跳过）
func (c *ConfigManager) emitSyncEvent(event string, data map[string]interface{}) {
	if c.syncEventForTest != nil {
		c.syncEventForTest(event, data)
		return
	}
	if c.wailsCtx != nil {
		runtime.EventsEmit(c.wailsCtx, event, data)
	}
}

// recordSyncCompleted 记录指定后端的同步完成时间；多后端时批量写入同一时间戳。
func (c *ConfigManager) recordSyncCompleted(providers ...string) int64 {
	timestamp := time.Now().UnixMilli()
	if len(providers) == 1 {
		c.saveLastSyncTime(providers[0], timestamp)
	} else if len(providers) > 1 {
		c.saveLastSyncTimes(providers, timestamp)
	}
	c.emitSyncEvent("sync-completed", map[string]interface{}{"timestamp": timestamp})
	return timestamp
}

// autoSyncProvider 自动同步：
// - 所有方向：重叠连接/快捷命令按 per-item last_modified 取最新，单侧独有按 lastSyncTime 判断删除
// - 无变化 → 静默跳过
func (c *ConfigManager) autoSyncProvider(s RemoteStorage, maxBackups int, provider string) error {
	remoteSnap, err := c.fetchLatestBackup(s, c.GetRecoveryPassword())
	if err != nil {
		if !isNoBackupError(err) {
			return err
		}
		if _, berr := c.backupConnections(s, maxBackups); berr != nil {
			return fmt.Errorf("云端访问失败: %w", berr)
		}
		c.recordSyncCompleted(provider)
		c.emitSyncEvent("sync-status", map[string]interface{}{
			"action": "upload",
			"reason": "no_remote_backup",
		})
		return nil
	}

	localSnapTime := c.loadSnapshotTime()
	remoteSnapTime := remoteSnap.SnapshotTime
	lastSyncTime := c.loadLastSyncTime(provider)

	// 首次接触该后端且本地墓碑会静默删掉远端项：跳过 AutoSync，请用户手动合并同步并确认
	if c.shouldSkipAutoSyncForTombstoneConflict(provider, remoteSnap) {
		log.Printf("[autoSyncProvider] skip %s: tombstone conflicts need manual sync confirmation", provider)
		c.emitSyncEvent("sync-status", map[string]interface{}{
			"action":   "skip",
			"reason":   "tombstone_conflict_needs_manual_sync",
			"provider": provider,
		})
		return nil
	}

	localConns := c.GetConnections()

	// 连接合并：重叠按 LastModified 取最新，单侧独有按 lastSyncTime 判断删除；墓碑传播真删
	localTombs := c.loadTombstoneStore()
	mergedConnTombs, mergedCredTombs, mergedPrunedBefore := mergeTombsWithPruneWatermark(
		localTombs.Connections, remoteSnap.DeletedConnections,
		localTombs.Credentials, remoteSnap.DeletedCredentials,
		localTombs.PrunedBefore, remoteSnap.TombstonePrunedBefore,
	)
	merged, dedupTombs := c.mergeWithDeletionPropagation(localConns, remoteSnap.Connections, lastSyncTime, mergedConnTombs)
	mergedConnTombs = pruneConnectionTombstones(mergeConnectionTombstones(mergedConnTombs, dedupTombs), merged)

	// 凭据合并
	var mergedCreds []Credential
	localCreds := c.GetCredentials()
	var credInferred []SyncTombstone
	if remoteSnap.HasCredentials {
		mergedCreds, credInferred = c.mergeCredentials(localCreds, remoteSnap.Credentials, lastSyncTime, mergedCredTombs)
	} else {
		mergedCreds, credInferred = c.mergeCredentials(localCreds, nil, lastSyncTime, mergedCredTombs)
	}
	mergedCredTombs = pruneCredentialTombstones(mergeCredentialTombstones(mergedCredTombs, credInferred), mergedCreds)

	// 快捷命令合并：重叠按 last_modified 取最新，单侧独有按 lastSyncTime 判断删除
	localQuickCmds := c.loadRawFile(c.quickCmdFile)
	mergedQuickCmds := localQuickCmds
	if remoteSnap.HasQuickCommands {
		mergedQuickCmds = c.mergeQuickCommands(localQuickCmds, remoteSnap.QuickCommands, lastSyncTime)
	}

	localAIProviders := c.GetAIProviderRegistry().Providers
	mergedAIProviders := localAIProviders
	if remoteSnap.HasAIProviders {
		mergedAIProviders = c.mergeAIProviders(localAIProviders, remoteSnap.AIProviders, lastSyncTime)
	}
	localAIGlobalSettings := c.localAIGlobalSettingsForSync()
	var mergedAIGlobalSettings aitypes.AIGlobalSettings
	var mergedAIGlobalSettingsPtr *aitypes.AIGlobalSettings
	if localAIGlobalSettings != nil {
		mergedAIGlobalSettings = *localAIGlobalSettings
		mergedAIGlobalSettingsPtr = &mergedAIGlobalSettings
	}
	if remoteSnap.HasAIGlobalSettings && (localAIGlobalSettings != nil || remoteSnap.AIGlobalSettings != nil) {
		mergedAIGlobalSettings = mergeAIGlobalSettings(mergedAIGlobalSettings, remoteSnap.AIGlobalSettings)
		mergedAIGlobalSettingsPtr = &mergedAIGlobalSettings
	}
	localProxyNodes := c.GetAIProxyNodes()
	mergedProxyNodes := localProxyNodes
	if remoteSnap.HasProxyNodes {
		mergedProxyNodes = c.mergeAIProxyNodes(localProxyNodes, remoteSnap.ProxyNodes, lastSyncTime)
	}

	// 本地有变化 → 保存
	quickChanged := remoteSnap.HasQuickCommands && !quickCmdsEqual(mergedQuickCmds, localQuickCmds)
	credsChanged := remoteSnap.HasCredentials && !credsEqual(mergedCreds, localCreds)
	aiProvidersChanged := remoteSnap.HasAIProviders && !aiProvidersEqual(mergedAIProviders, localAIProviders)
	aiGlobalSettingsChanged := remoteSnap.HasAIGlobalSettings && !aiGlobalSettingsEqual(mergedAIGlobalSettingsPtr, localAIGlobalSettings)
	proxyNodesChanged := remoteSnap.HasProxyNodes && !aiProxyNodesEqual(mergedProxyNodes, localProxyNodes)
	tombLocalChanged := !tombstonesEqual(mergedConnTombs, localTombs.Connections) ||
		!tombstonesEqual(mergedCredTombs, localTombs.Credentials) ||
		mergedPrunedBefore != localTombs.PrunedBefore
	localChanged := !connsEqual(merged, localConns) || quickChanged || credsChanged || aiProvidersChanged || aiGlobalSettingsChanged || proxyNodesChanged || tombLocalChanged

	// 云端有变化 → 需要上传
	cloudQuickChanged := remoteSnap.HasQuickCommands && !quickCmdsEqual(mergedQuickCmds, remoteSnap.QuickCommands)
	cloudCredsChanged := remoteSnap.HasCredentials && !credsEqual(mergedCreds, remoteSnap.Credentials)
	cloudAIProvidersChanged := remoteSnap.HasAIProviders && !aiProvidersEqual(mergedAIProviders, remoteSnap.AIProviders)
	cloudAIGlobalSettingsChanged := remoteSnap.HasAIGlobalSettings && !aiGlobalSettingsEqual(mergedAIGlobalSettingsPtr, remoteSnap.AIGlobalSettings)
	if cloudAIGlobalSettingsChanged {
		log.Printf("[autoSyncProvider] aiGlobal diff %s", aiGlobalSettingsDiffSummary(mergedAIGlobalSettingsPtr, remoteSnap.AIGlobalSettings))
	}
	cloudProxyNodesChanged := remoteSnap.HasProxyNodes && !aiProxyNodesEqual(mergedProxyNodes, remoteSnap.ProxyNodes)
	cloudConnsChanged := !connsEqual(merged, remoteSnap.Connections)
	cloudTombChanged := !tombstonesEqual(mergedConnTombs, remoteSnap.DeletedConnections) ||
		!tombstonesEqual(mergedCredTombs, remoteSnap.DeletedCredentials) ||
		mergedPrunedBefore != remoteSnap.TombstonePrunedBefore
	cloudChanged := cloudConnsChanged || cloudQuickChanged || cloudCredsChanged || cloudAIProvidersChanged || cloudAIGlobalSettingsChanged || cloudProxyNodesChanged || cloudTombChanged

	// 无变化 → 静默跳过
	if !localChanged && !cloudChanged {
		c.recordSyncCompleted(provider)
		return nil
	}

	// 确定同步方向（用于前端通知）
	var action string
	if cloudChanged && localChanged {
		action = "merge"
	} else if cloudChanged {
		action = "upload"
	} else {
		action = "download"
	}
	if localChanged {
		c.mu.Lock()
		if err := c.saveConnectionsFile(merged); err != nil {
			log.Printf("[autoSyncProvider] save: %v", err)
		}
		c.connCacheDirty = true
		if err := c.saveTombstoneStore(syncTombstoneStore{Connections: mergedConnTombs, Credentials: mergedCredTombs, PrunedBefore: mergedPrunedBefore}); err != nil {
			log.Printf("[autoSyncProvider] save tombstones: %v", err)
		}
		if credsChanged || tombLocalChanged {
			if err := c.saveCredentialsFile(mergedCreds); err != nil {
				log.Printf("[autoSyncProvider] save creds: %v", err)
			}
			c.credCacheDirty = true
		}
		if quickChanged {
			// staleness check: 重读文件确认没有被并发 SaveQuickCommands 覆盖
			// ponytail: 已持写锁，不能再调 loadRawFile（它会 RLock 自死锁），直接 os.ReadFile
			data, _ := os.ReadFile(c.quickCmdFile)
			if quickCmdsEqual(string(data), localQuickCmds) {
				atomicWriteFile(c.quickCmdFile, []byte(mergedQuickCmds), 0600)
			}
		}
		c.mu.Unlock()
		if aiProvidersChanged {
			if err := c.SaveAIProviderRegistry(aitypes.AIProviderRegistry{Providers: mergedAIProviders}); err != nil {
				log.Printf("[autoSyncProvider] save AI providers: %v", err)
			}
		}
		if aiGlobalSettingsChanged {
			if err := c.SaveAIGlobalSettings(mergedAIGlobalSettings); err != nil {
				log.Printf("[autoSyncProvider] save AI global settings: %v", err)
			}
		}
		if proxyNodesChanged {
			if err := c.SaveAIProxyNodes(mergedProxyNodes); err != nil {
				log.Printf("[autoSyncProvider] save AI proxy nodes: %v", err)
			}
		}
		c.CleanupOrphanedHistory()
	}

	// 云端有变化 → 上传（显式带合并墓碑，避免仅读本地文件时漏掉）
	syncTimeUpdated := false
	if cloudChanged {
		c.bumpSnapshotTime()
		syncTimeUpdated = true
		uploadSnap := c.localSyncSnapshot()
		uploadSnap.Connections = merged
		uploadSnap.Credentials = mergedCreds
		uploadSnap.QuickCommands = mergedQuickCmds
		uploadSnap.AIProviders = mergedAIProviders
		if mergedAIGlobalSettingsPtr != nil {
			uploadSnap.AIGlobalSettings = mergedAIGlobalSettingsPtr
		}
		uploadSnap.ProxyNodes = mergedProxyNodes
		uploadSnap.DeletedConnections = mergedConnTombs
		uploadSnap.DeletedCredentials = mergedCredTombs
		uploadSnap.TombstonePrunedBefore = mergedPrunedBefore
		uploadSnap.SnapshotTime = c.loadSnapshotTime()
		if _, berr := c.uploadSnapshot(s, uploadSnap, c.GetRecoveryPassword(), maxBackups, true); berr != nil {
			return fmt.Errorf("上传合并快照失败: %w", berr)
		}
	} else if localChanged {
		c.bumpSnapshotTime()
		syncTimeUpdated = true
	}

	// 仅在本轮没有生成新本地快照时，才用较新的远端时间校准本地时间戳。
	if !syncTimeUpdated && remoteSnapTime > localSnapTime {
		atomicWriteFile(c.syncTimeFile, []byte(fmt.Sprintf("%d", remoteSnapTime)), 0600)
	}
	c.recordSyncCompleted(provider)

	c.emitSyncEvent("sync-status", map[string]interface{}{
		"action":       action,
		"localCount":   len(localConns),
		"remoteCount":  len(remoteSnap.Connections),
		"mergedCount":  len(merged),
		"localChanged": localChanged,
		"cloudChanged": cloudChanged,
	})

	return nil
}

// dedupTombstoneAt 给 host 去重挤掉的 id 生成 deleted_at：必须严格大于双方 LastModified，
// 否则 shouldDrop / 对端旧节点可能再次把被挤掉的 id 当新增加回来。
func dedupTombstoneAt(a, b int64) int64 {
	at := time.Now().UnixMilli()
	if a > b {
		b = a
	}
	if at <= b {
		return b + 1
	}
	return at
}

// inferredDeleteTombstoneAt 为「按 lastSync 判删」补墓碑时间：必须 > LastModified，
// 否则 shouldDrop 不成立，下一轮仍可能把该项当新增加回来。
func inferredDeleteTombstoneAt(lastModified, lastSyncTime int64) int64 {
	at := time.Now().UnixMilli()
	floor := lastModified
	if lastSyncTime > floor {
		floor = lastSyncTime
	}
	if at <= floor {
		return floor + 1
	}
	return at
}

// mergeWithDeletionPropagation 合并本地和云端连接：
// 1. 重叠连接（两边都有）→ 按 per-connection LastModified 取最新
// 2. 单侧独有 → LastModified > lastSyncTime 则保留（新增），否则删除并写墓碑
// 3. 若 tombstones 中 deleted_at 新于节点 LastModified，则删除（跨后端/设备真删）
// 4. host:port+username 去重挤掉的 id → 写入 dedupTombs，避免对端再复活
// 返回值 dedupTombs 需并入调用方的连接墓碑列表。
func (c *ConfigManager) mergeWithDeletionPropagation(localConns, remoteConns []Connection, lastSyncTime int64, tombstones ...[]SyncTombstone) (mergedOut []Connection, dedupTombs []SyncTombstone) {
	deleted := map[string]int64{}
	for _, list := range tombstones {
		deleted = mergeTombstoneMaps(deleted, tombstoneMap(list))
	}
	shouldDrop := func(id string, lm int64) bool {
		at, ok := deleted[id]
		// 墓碑时间 >= 节点时间即删除；相等时也算删除，避免同毫秒/整表 touch 后复活
		return ok && at >= lm
	}
	// 启发式删除（单侧独有且 LM<=lastSync）必须落墓碑，否则会上传「人没了、墓碑也空」的包，对端又复活。
	noteInferredDelete := func(id string, lm int64) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		at := inferredDeleteTombstoneAt(lm, lastSyncTime)
		if prev, ok := deleted[id]; ok && prev >= at {
			return
		}
		deleted[id] = at
		dedupTombs = append(dedupTombs, SyncTombstone{ID: id, DeletedAt: at})
	}

	remoteMap := make(map[string]Connection, len(remoteConns))
	for _, rc := range remoteConns {
		remoteMap[rc.ID] = rc
	}

	merged := make([]Connection, 0, len(localConns)+len(remoteConns))
	added := make(map[string]bool)

	// 按本地原始顺序遍历
	for _, lc := range localConns {
		if added[lc.ID] {
			continue
		}
		if rc, hasRemote := remoteMap[lc.ID]; hasRemote {
			chosen := lc
			if rc.LastModified > lc.LastModified {
				chosen = rc
			}
			if !shouldDrop(chosen.ID, chosen.LastModified) {
				merged = append(merged, chosen)
			}
			added[lc.ID] = true
		} else {
			if lc.LastModified > lastSyncTime && !shouldDrop(lc.ID, lc.LastModified) {
				merged = append(merged, lc)
			} else if !shouldDrop(lc.ID, lc.LastModified) {
				// 本地独有且视为已删除：补墓碑，防止上传空墓碑包
				noteInferredDelete(lc.ID, lc.LastModified)
			}
			added[lc.ID] = true
		}
	}
	// 远程独有（按远程原始顺序）
	for _, rc := range remoteConns {
		if !added[rc.ID] {
			if rc.LastModified > lastSyncTime && !shouldDrop(rc.ID, rc.LastModified) {
				merged = append(merged, rc)
			} else if !shouldDrop(rc.ID, rc.LastModified) {
				// 远端独有且视为已删除：补墓碑，防止其它端再合回来
				noteInferredDelete(rc.ID, rc.LastModified)
			}
			added[rc.ID] = true
		}
	}

	// host:port+username 去重（按 LastModified 保留最新的）。
	// 业务上同一账号同一入口视为同一节点，避免多端新增产生重复节点。
	// 被挤掉的 id 记墓碑，否则对端仍带着旧 id 时会在下一轮合并中复活。
	type hpKey struct {
		host string
		port int
		user string
	}
	hostPortMap := make(map[hpKey]int)
	var deduped []Connection
	for _, v := range merged {
		key := hpKey{v.Host, v.Port, v.Username}
		if idx, ok := hostPortMap[key]; ok {
			kept := deduped[idx]
			if v.LastModified > kept.LastModified {
				if kept.ID != "" && kept.ID != v.ID {
					dedupTombs = append(dedupTombs, SyncTombstone{ID: kept.ID, DeletedAt: dedupTombstoneAt(kept.LastModified, v.LastModified)})
				}
				deduped[idx] = v
			} else if v.ID != "" && v.ID != kept.ID {
				dedupTombs = append(dedupTombs, SyncTombstone{ID: v.ID, DeletedAt: dedupTombstoneAt(v.LastModified, kept.LastModified)})
			}
		} else {
			hostPortMap[key] = len(deduped)
			deduped = append(deduped, v)
		}
	}
	return deduped, dedupTombs
}

// mergeCredentials 合并本地和云端凭据；启发式删除同样补墓碑（第二返回值）。
func (c *ConfigManager) mergeCredentials(localCreds, remoteCreds []Credential, lastSyncTime int64, tombstones ...[]SyncTombstone) (mergedOut []Credential, inferredTombs []SyncTombstone) {
	deleted := map[string]int64{}
	for _, list := range tombstones {
		deleted = mergeTombstoneMaps(deleted, tombstoneMap(list))
	}
	shouldDrop := func(id string, lm int64) bool {
		at, ok := deleted[id]
		// 墓碑时间 >= 节点时间即删除；相等时也算删除，避免同毫秒/整表 touch 后复活
		return ok && at >= lm
	}
	noteInferredDelete := func(id string, lm int64) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		at := inferredDeleteTombstoneAt(lm, lastSyncTime)
		if prev, ok := deleted[id]; ok && prev >= at {
			return
		}
		deleted[id] = at
		inferredTombs = append(inferredTombs, SyncTombstone{ID: id, DeletedAt: at})
	}

	remoteMap := make(map[string]Credential, len(remoteCreds))
	for _, rc := range remoteCreds {
		remoteMap[rc.ID] = rc
	}

	merged := make([]Credential, 0, len(localCreds)+len(remoteCreds))
	added := make(map[string]bool)

	for _, lc := range localCreds {
		if added[lc.ID] {
			continue
		}
		if rc, hasRemote := remoteMap[lc.ID]; hasRemote {
			chosen := lc
			if rc.LastModified > lc.LastModified {
				chosen = rc
			}
			if !shouldDrop(chosen.ID, chosen.LastModified) {
				merged = append(merged, chosen)
			}
			added[lc.ID] = true
		} else {
			if lc.LastModified > lastSyncTime && !shouldDrop(lc.ID, lc.LastModified) {
				merged = append(merged, lc)
			} else if !shouldDrop(lc.ID, lc.LastModified) {
				noteInferredDelete(lc.ID, lc.LastModified)
			}
			added[lc.ID] = true
		}
	}
	for _, rc := range remoteCreds {
		if !added[rc.ID] {
			if rc.LastModified > lastSyncTime && !shouldDrop(rc.ID, rc.LastModified) {
				merged = append(merged, rc)
			} else if !shouldDrop(rc.ID, rc.LastModified) {
				noteInferredDelete(rc.ID, rc.LastModified)
			}
			added[rc.ID] = true
		}
	}
	return merged, inferredTombs
}

func tombstonesEqual(a, b []SyncTombstone) bool {
	return reflect.DeepEqual(tombstoneMap(a), tombstoneMap(b))
}

func mergeTombstoneLists(local, remote []SyncTombstone) []SyncTombstone {
	return tombstonesFromMap(mergeTombstoneMaps(tombstoneMap(local), tombstoneMap(remote)))
}

func mergeConnectionTombstones(local, remote []SyncTombstone) []SyncTombstone {
	return mergeTombstoneLists(local, remote)
}

func mergeCredentialTombstones(local, remote []SyncTombstone) []SyncTombstone {
	return mergeTombstoneLists(local, remote)
}

// mergeTombsWithPruneWatermark 合并墓碑并应用清理水位线：
// prunedBefore = max(local, remote)；丢弃 deleted_at < prunedBefore 的项。
// 这样「清理删除记录」后，对端旧墓碑不会再被并回来。
func mergeTombsWithPruneWatermark(localConn, remoteConn, localCred, remoteCred []SyncTombstone, localPB, remotePB int64) (conn, cred []SyncTombstone, prunedBefore int64) {
	prunedBefore = localPB
	if remotePB > prunedBefore {
		prunedBefore = remotePB
	}
	conn = filterTombstonesNotBefore(mergeConnectionTombstones(localConn, remoteConn), prunedBefore)
	cred = filterTombstonesNotBefore(mergeCredentialTombstones(localCred, remoteCred), prunedBefore)
	return conn, cred, prunedBefore
}

// pruneTombstonesByAlive 去掉「仍存在且 LastModified > deleted_at」对应的墓碑。
// 仅当节点严格新于删除时间才视为重建；相等时间戳不能清墓碑，否则会复活。
func pruneTombstonesByAlive(tombs []SyncTombstone, alive map[string]int64) []SyncTombstone {
	m := tombstoneMap(tombs)
	for id, at := range m {
		if lm, ok := alive[id]; ok && lm > at {
			delete(m, id)
		}
	}
	return tombstonesFromMap(m)
}

func pruneConnectionTombstones(tombs []SyncTombstone, conns []Connection) []SyncTombstone {
	alive := make(map[string]int64, len(conns))
	for _, c := range conns {
		alive[c.ID] = c.LastModified
	}
	return pruneTombstonesByAlive(tombs, alive)
}

func pruneCredentialTombstones(tombs []SyncTombstone, creds []Credential) []SyncTombstone {
	alive := make(map[string]int64, len(creds))
	for _, c := range creds {
		alive[c.ID] = c.LastModified
	}
	return pruneTombstonesByAlive(tombs, alive)
}

func (c *ConfigManager) mergeAIProviders(localProviders, remoteProviders []aitypes.AIProviderProfile, lastSyncTime int64) []aitypes.AIProviderProfile {
	if remoteProviders == nil {
		return localProviders
	}
	remoteMap := make(map[string]aitypes.AIProviderProfile, len(remoteProviders))
	for _, p := range remoteProviders {
		remoteMap[p.ID] = p
	}
	merged := make([]aitypes.AIProviderProfile, 0, len(localProviders)+len(remoteProviders))
	added := make(map[string]bool)
	for _, lp := range localProviders {
		if added[lp.ID] {
			continue
		}
		if rp, hasRemote := remoteMap[lp.ID]; hasRemote {
			if lp.UpdatedAt >= rp.UpdatedAt {
				merged = append(merged, lp)
			} else {
				merged = append(merged, rp)
			}
		} else if lp.UpdatedAt > lastSyncTime {
			merged = append(merged, lp)
		}
		added[lp.ID] = true
	}
	for _, rp := range remoteProviders {
		if !added[rp.ID] {
			if rp.UpdatedAt > lastSyncTime {
				merged = append(merged, rp)
			}
			added[rp.ID] = true
		}
	}
	return merged
}

func (c *ConfigManager) mergeAIProxyNodes(localNodes, remoteNodes []aitypes.AIProxyNode, lastSyncTime int64) []aitypes.AIProxyNode {
	if remoteNodes == nil {
		return localNodes
	}
	remoteMap := make(map[string]aitypes.AIProxyNode, len(remoteNodes))
	for _, n := range remoteNodes {
		remoteMap[n.ID] = n
	}
	merged := make([]aitypes.AIProxyNode, 0, len(localNodes)+len(remoteNodes))
	added := make(map[string]bool)
	for _, ln := range localNodes {
		if added[ln.ID] {
			continue
		}
		if rn, hasRemote := remoteMap[ln.ID]; hasRemote {
			if ln.UpdatedAt >= rn.UpdatedAt {
				merged = append(merged, ln)
			} else {
				merged = append(merged, rn)
			}
		} else if ln.UpdatedAt > lastSyncTime {
			merged = append(merged, ln)
		}
		added[ln.ID] = true
	}
	for _, rn := range remoteNodes {
		if !added[rn.ID] {
			if rn.UpdatedAt > lastSyncTime {
				merged = append(merged, rn)
			}
			added[rn.ID] = true
		}
	}
	return merged
}

// TombstoneConflictItem 预检：本地墓碑会从目标云删掉的项。
type TombstoneConflictItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host,omitempty"`
}

// TombstoneConflictPreview 合并同步前预检结果：先读远端再算「墓碑 ∩ 远端仍在」。
type TombstoneConflictPreview struct {
	Providers             []string                `json:"providers"`
	WouldDeleteConnections []TombstoneConflictItem `json:"wouldDeleteConnections"`
	WouldDeleteCredentials []TombstoneConflictItem `json:"wouldDeleteCredentials"`
}

// PreviewTombstoneConflicts 先 fetch 当前同步模式对应后端，再列出本地墓碑会删掉的远端项。
// 无远端备份或无冲突时返回空列表（不弹窗）。
func (c *ConfigManager) PreviewTombstoneConflicts() (TombstoneConflictPreview, error) {
	var out TombstoneConflictPreview
	providers, failures := c.getSyncProviders()
	defer closeProviders(providers)
	if len(failures) > 0 && len(providers) == 0 {
		return out, providerFailuresError(failures)
	}
	store := c.loadTombstoneStore()
	connTombs := filterTombstonesNotBefore(store.Connections, store.PrunedBefore)
	credTombs := filterTombstonesNotBefore(store.Credentials, store.PrunedBefore)
	if len(connTombs) == 0 && len(credTombs) == 0 {
		return out, nil
	}
	connMap := tombstoneMap(connTombs)
	credMap := tombstoneMap(credTombs)
	seenConn := map[string]bool{}
	seenCred := map[string]bool{}
	password := c.GetRecoveryPassword()

	for _, p := range providers {
		out.Providers = append(out.Providers, p.provider)
		remote, err := c.fetchLatestBackup(p.storage, password)
		if err != nil {
			if isNoBackupError(err) {
				continue
			}
			return out, fmt.Errorf("%s 读取失败：%w", p.provider, err)
		}
		if remote == nil {
			continue
		}
		for _, rc := range remote.Connections {
			at, ok := connMap[rc.ID]
			if !ok || seenConn[rc.ID] {
				continue
			}
			// 与 shouldDrop 一致：deleted_at >= last_modified 才会被墓碑删掉
			if at < rc.LastModified {
				continue
			}
			seenConn[rc.ID] = true
			name := strings.TrimSpace(rc.Name)
			if name == "" {
				name = rc.Host
			}
			out.WouldDeleteConnections = append(out.WouldDeleteConnections, TombstoneConflictItem{
				ID: rc.ID, Name: name, Host: rc.Host,
			})
		}
		if remote.HasCredentials || len(remote.Credentials) > 0 {
			for _, rc := range remote.Credentials {
				at, ok := credMap[rc.ID]
				if !ok || seenCred[rc.ID] {
					continue
				}
				if at < rc.LastModified {
					continue
				}
				seenCred[rc.ID] = true
				name := strings.TrimSpace(rc.Name)
				if name == "" {
					name = rc.ID
				}
				out.WouldDeleteCredentials = append(out.WouldDeleteCredentials, TombstoneConflictItem{
					ID: rc.ID, Name: name,
				})
			}
		}
	}
	return out, nil
}

// ClearTombstoneConflicts 放弃对指定 id 的删除意图（保留云端项时调用），再同步即可留下远端数据。
func (c *ConfigManager) ClearTombstoneConflicts(connectionIDs, credentialIDs []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clearConnectionTombstonesLocked(connectionIDs)
	c.clearCredentialTombstonesLocked(credentialIDs)
}

// remoteHasTombstoneConflicts 本地墓碑是否会删掉远端仍存在的连接/凭据（与 shouldDrop 一致）。
func remoteHasTombstoneConflicts(remote *SyncSnapshot, connTombs, credTombs []SyncTombstone) bool {
	if remote == nil {
		return false
	}
	connMap := tombstoneMap(connTombs)
	credMap := tombstoneMap(credTombs)
	if len(connMap) == 0 && len(credMap) == 0 {
		return false
	}
	for _, rc := range remote.Connections {
		if at, ok := connMap[rc.ID]; ok && at >= rc.LastModified {
			return true
		}
	}
	if remote.HasCredentials || len(remote.Credentials) > 0 {
		for _, rc := range remote.Credentials {
			if at, ok := credMap[rc.ID]; ok && at >= rc.LastModified {
				return true
			}
		}
	}
	return false
}

// shouldSkipAutoSyncForTombstoneConflict 首次接触该后端且墓碑会静默删远端项时，跳过 AutoSync，改由手动同步确认。
func (c *ConfigManager) shouldSkipAutoSyncForTombstoneConflict(provider string, remote *SyncSnapshot) bool {
	if c.loadLastSyncTime(provider) != 0 {
		return false
	}
	store := c.loadTombstoneStore()
	connTombs := filterTombstonesNotBefore(store.Connections, store.PrunedBefore)
	credTombs := filterTombstonesNotBefore(store.Credentials, store.PrunedBefore)
	return remoteHasTombstoneConflicts(remote, connTombs, credTombs)
}

// PruneSyncTombstones 按天数清理本地删除记录，并上传到当前同步模式对应的后端，避免下次同步又从云端并回来。
// days <= 0：清理全部；days > 0：清理 deleted_at 早于「现在 - days 天」的记录。
// 自动同步模式为 all 时推全部已配置后端；为 r2/webdav 等时只推该后端。
func (c *ConfigManager) PruneSyncTombstones(days int) (SyncTombstonePruneResult, error) {
	var result SyncTombstonePruneResult
	store := c.loadTombstoneStore()
	beforeConn := len(tombstoneMap(store.Connections))
	beforeCred := len(tombstoneMap(store.Credentials))

	clearAll := days <= 0
	var cutoff int64
	if !clearAll {
		cutoff = time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	}
	var removedConn, removedCred int
	beforeConnList := append([]SyncTombstone(nil), store.Connections...)
	beforeCredList := append([]SyncTombstone(nil), store.Credentials...)
	store.Connections, removedConn = pruneTombstonesOlderThan(store.Connections, cutoff, clearAll)
	store.Credentials, removedCred = pruneTombstonesOlderThan(store.Credentials, cutoff, clearAll)
	// 推进清理水位线：之后合并时丢弃更早的远端墓碑，避免「清理后又被对端并回来」
	if removedConn > 0 || removedCred > 0 {
		var pb int64
		if clearAll {
			pb = time.Now().UnixMilli()
			if m := maxTombstoneDeletedAt(beforeConnList, beforeCredList); m >= pb {
				pb = m + 1
			}
		} else {
			pb = cutoff
		}
		if pb > store.PrunedBefore {
			store.PrunedBefore = pb
		}
	}
	result.RemovedConnections = removedConn
	result.RemovedCredentials = removedCred
	result.RemainingConnections = len(tombstoneMap(store.Connections))
	result.RemainingCredentials = len(tombstoneMap(store.Credentials))

	if removedConn == 0 && removedCred == 0 {
		// 没有变化时仍返回当前条数，方便前端刷新
		if result.RemainingConnections == 0 {
			result.RemainingConnections = beforeConn
		}
		if result.RemainingCredentials == 0 {
			result.RemainingCredentials = beforeCred
		}
		return result, nil
	}
	if err := c.saveTombstoneStore(store); err != nil {
		return result, fmt.Errorf("保存删除记录失败：%w", err)
	}

	// 只推当前同步模式对应的后端：选 R2 就只上 R2，不会去碰已配置但未选用的 FTP/WebDAV。
	// 模式为 all 时再推全部；单个后端失败不整单回滚（本地已清完），汇总错误返回。
	providers, failures := c.getSyncProviders()
	defer closeProviders(providers)
	password := c.GetRecoveryPassword()
	c.bumpSnapshotTime()
	var uploadErrs []error
	for _, failure := range failures {
		uploadErrs = append(uploadErrs, fmt.Errorf("%s: %w", failure.provider, failure.err))
	}
	for _, p := range providers {
		// 清理删除记录：严格按本机已裁剪的墓碑上传，禁止再与远端并集
			if _, err := c.uploadSnapshot(p.storage, c.localSyncSnapshot(), password, p.maxBackups, false); err != nil {
			uploadErrs = append(uploadErrs, fmt.Errorf("%s 上传失败：%w", p.provider, err))
			continue
		}
		result.Uploaded++
		c.recordSyncCompleted(p.provider)
	}
	if len(uploadErrs) > 0 {
		if result.Uploaded == 0 {
			return result, fmt.Errorf("清理后上传失败：%w", errors.Join(uploadErrs...))
		}
		return result, fmt.Errorf("已清理并上传 %d 个目标，部分失败：%w", result.Uploaded, errors.Join(uploadErrs...))
	}
	return result, nil
}

func mergeAIGlobalSettings(localSettings aitypes.AIGlobalSettings, remoteSettings *aitypes.AIGlobalSettings) aitypes.AIGlobalSettings {
	// ponytail: ai_global_settings 不再同步, 保留本地设置不变
	return localSettings
}

// ─── 同步模式分发 ─────────────────────────────────────────

// getSyncProviders 返回当前同步模式下所有已配置的提供商及初始化失败。
func (c *ConfigManager) getSyncProviders() ([]providerEntry, []providerFailure) {
	if c.syncProvidersForTest != nil {
		return c.syncProvidersForTest()
	}
	return c.configuredSyncProviders(c.GetSyncMode())
}

func (c *ConfigManager) configuredSyncProviders(mode string) ([]providerEntry, []providerFailure) {
	var entries []providerEntry
	var failures []providerFailure
	add := func(id string, configured bool, storageFn func() (RemoteStorage, int, error)) {
		if !configured || (mode != id && mode != "all") {
			return
		}
		s, max, err := storageFn()
		if err != nil {
			failures = append(failures, providerFailure{provider: id, err: err})
			return
		}
		entries = append(entries, providerEntry{provider: id, storage: s, maxBackups: max})
	}

	webdav := c.GetWebdavConfig()
	r2 := c.GetR2Config()
	ftpConfig := c.GetFTPConfig()
	sftpConfig := c.GetSFTPConfig()
	add("webdav", webdav != nil && webdav["url"] != "", c.newWebdavStorage)
	add("r2", r2 != nil && r2.Bucket != "" && r2.Endpoint != "", c.newR2Storage)
	add("ftp", ftpConfig != nil && ftpConfig.Host != "", c.newFTPStorage)
	add("sftp", sftpConfig != nil && sftpConfig.Host != "", c.newSFTPStorage)
	return entries, failures
}

type providerEntry struct {
	provider   string
	storage    RemoteStorage
	maxBackups int
}

type providerFailure struct {
	provider string
	err      error
}

func providerFailuresError(failures []providerFailure) error {
	if len(failures) == 0 {
		return nil
	}
	errs := make([]error, 0, len(failures))
	for _, failure := range failures {
		errs = append(errs, fmt.Errorf("%s: %w", failure.provider, failure.err))
	}
	return errors.Join(errs...)
}

func (c *ConfigManager) emitSyncFailure(provider string, err error) {
	if err == nil {
		return
	}
	payload := map[string]interface{}{"provider": provider, "error": err.Error(), "category": "sync"}
	if errors.Is(err, errRecoveryPassword) {
		payload["category"] = "password"
		payload["reason"] = "recovery_password_incorrect"
	} else if reason, ok := syncTrustFailureReason(err); ok {
		payload["category"] = "trust"
		payload["reason"] = reason
	}
	c.emitSyncEvent("sync-failed", payload)
}

func (c *ConfigManager) SyncAllProviders() (map[string]interface{}, error) {
	providers, failures := c.getSyncProviders()
	if err := providerFailuresError(failures); err != nil {
		closeProviders(providers)
		return nil, err
	}
	return c.syncAllProviders(providers, c.GetRecoveryPassword())
}

// SyncWithRecoveryPassword 先用候选密码纯读取预检；确认可解最新备份后持久化，再进入同次严格同步。
func (c *ConfigManager) SyncWithRecoveryPassword(password string) (map[string]interface{}, error) {
	providers, failures := c.getSyncProviders()
	if err := providerFailuresError(failures); err != nil {
		closeProviders(providers)
		return nil, err
	}
	if len(providers) == 0 {
		return nil, errors.New("没有可用同步目标")
	}
	for _, p := range providers {
		_, err := c.fetchLatestBackup(p.storage, password)
		if isNoBackupError(err) {
			continue
		}
		if err != nil {
			closeProviders(providers)
			return nil, fmt.Errorf("%s 密码预检失败：%w", p.provider, err)
		}
	}
	if err := c.SetRecoveryPassword(password); err != nil {
		closeProviders(providers)
		return nil, fmt.Errorf("保存恢复密码失败：%w", err)
	}
	if c.GetSyncMode() == "all" {
		return c.syncAllProviders(providers, password, true)
	}
	defer closeProviders(providers)
	return c.syncFromProvider(providers[0].storage, providers[0].maxBackups, password, providers[0].provider)
}

// ChangeRecoveryPassword 对当前同步模式目标预检成功后，以新密码各上传一次合并快照，最后持久化密码。
// 空密码表示关闭加密（改明文）。未选用的后端（如配了 FTP 但模式是 WebDAV）不会参与。
// 某后端最新备份损坏/不可读时跳过该端合并，仍尽量用本机+可读端合并后回写，避免单端坏文件卡死关加密。
func (c *ConfigManager) ChangeRecoveryPassword(newPassword string) error {
	newPassword = normalizeRecoveryPassword(newPassword)
	providers, failures := c.getSyncProviders()
	if err := providerFailuresError(failures); err != nil {
		closeProviders(providers)
		return err
	}
	if len(providers) == 0 {
		return c.SetRecoveryPassword(newPassword)
	}
	defer closeProviders(providers)

	oldPassword := c.GetRecoveryPassword()
	remoteSnaps := make([]*SyncSnapshot, 0, len(providers))
	for _, p := range providers {
		snap, oldErr := c.fetchLatestBackup(p.storage, oldPassword)
		if errors.Is(oldErr, errRecoveryPassword) {
			var newErr error
			snap, newErr = c.fetchLatestBackup(p.storage, newPassword)
			if errors.Is(newErr, errRecoveryPassword) {
				return fmt.Errorf("%w：%s 的云端备份无法使用旧密码或新密码解密", errRecoveryPasswordResetRequired, p.provider)
			}
			oldErr = newErr
		}
		if isNoBackupError(oldErr) {
			continue
		}
		if oldErr != nil {
			// 传输/网络错误仍失败；备份内容损坏（截断、Base64 坏）跳过该端，避免关加密被坏文件卡住
			if isUnreadableBackupContentError(oldErr) {
				log.Printf("[ChangeRecoveryPassword] %s 预检跳过损坏备份：%v", p.provider, oldErr)
				continue
			}
			return fmt.Errorf("%s 预检失败：%w", p.provider, oldErr)
		}
		remoteSnaps = append(remoteSnaps, snap)
	}

	merged := c.localSyncSnapshot()
	for _, snap := range remoteSnaps {
		mergeSnapshot(c, merged, snap)
	}
	merged.SnapshotTime = time.Now().UnixMilli()
	return c.uploadRecoveryPasswordSnapshot(providers, merged, newPassword, true)
}

// ResetRecoveryPassword 放弃读取云端，以本机快照覆盖当前同步模式目标。
func (c *ConfigManager) ResetRecoveryPassword(newPassword string) error {
	newPassword = normalizeRecoveryPassword(newPassword)
	providers, failures := c.getSyncProviders()
	if err := providerFailuresError(failures); err != nil {
		closeProviders(providers)
		return err
	}
	if len(providers) == 0 {
		return c.SetRecoveryPassword(newPassword)
	}
	defer closeProviders(providers)

	snap := c.localSyncSnapshot()
	snap.SnapshotTime = time.Now().UnixMilli()
	return c.uploadRecoveryPasswordSnapshot(providers, snap, newPassword, false)
}

func (c *ConfigManager) uploadRecoveryPasswordSnapshot(providers []providerEntry, snap *SyncSnapshot, password string, persistSnapshot bool) error {
	var uploaded []uploadedSnapshot
	for _, p := range providers {
		result, err := c.uploadSnapshot(p.storage, snap, password, 0, true)
		name := snapshotUploadPath(result)
		if err == nil && name != "" {
			uploaded = append(uploaded, uploadedSnapshot{p.storage, name})
			continue
		}
		if name != "" {
			_ = p.storage.DeleteFile(name)
		}
		rollbackUploadedSnapshots(uploaded)
		if err == nil {
			err = errors.New("上传结果缺少备份文件名")
		}
		return fmt.Errorf("%s 上传新密码快照失败：%w", p.provider, err)
	}
	if persistSnapshot {
		if err := c.persistSyncSnapshot(snap); err != nil {
			rollbackUploadedSnapshots(uploaded)
			return err
		}
	}
	if err := c.SetRecoveryPassword(password); err != nil {
		rollbackUploadedSnapshots(uploaded)
		return fmt.Errorf("保存恢复密码失败：%w", err)
	}
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, p.provider)
	}
	c.recordSyncCompleted(names...)
	return nil
}

type uploadedSnapshot struct {
	storage RemoteStorage
	name    string
}

func snapshotUploadPath(result map[string]interface{}) string {
	if result == nil {
		return ""
	}
	name, _ := result["path"].(string)
	return name
}

func rollbackUploadedSnapshots(uploaded []uploadedSnapshot) {
	for _, file := range uploaded {
		_ = file.storage.DeleteFile(file.name)
	}
}

func closeProviders(providers []providerEntry) {
	for _, p := range providers {
		if closer, ok := p.storage.(storageCloser); ok {
			_ = closer.Close()
		}
	}
}

func mergeSnapshot(c *ConfigManager, dst, src *SyncSnapshot) {
	dst.DeletedConnections = mergeConnectionTombstones(dst.DeletedConnections, src.DeletedConnections)
	dst.DeletedCredentials = mergeCredentialTombstones(dst.DeletedCredentials, src.DeletedCredentials)
	var dedupTombs []SyncTombstone
	dst.Connections, dedupTombs = c.mergeWithDeletionPropagation(dst.Connections, src.Connections, -1, dst.DeletedConnections)
	dst.DeletedConnections = pruneConnectionTombstones(mergeConnectionTombstones(dst.DeletedConnections, dedupTombs), dst.Connections)
	if src.HasCredentials {
		var credInferred []SyncTombstone
		dst.Credentials, credInferred = c.mergeCredentials(dst.Credentials, src.Credentials, -1, dst.DeletedCredentials)
		dst.DeletedCredentials = pruneCredentialTombstones(mergeCredentialTombstones(dst.DeletedCredentials, credInferred), dst.Credentials)
	}
	if src.HasQuickCommands {
		dst.QuickCommands = c.mergeQuickCommands(dst.QuickCommands, src.QuickCommands, -1)
	}
	if src.HasAIProviders {
		dst.AIProviders = c.mergeAIProviders(dst.AIProviders, src.AIProviders, -1)
	}
	if src.HasAIGlobalSettings {
		var local aitypes.AIGlobalSettings
		if dst.AIGlobalSettings != nil {
			local = *dst.AIGlobalSettings
		}
		settings := mergeAIGlobalSettings(local, src.AIGlobalSettings)
		dst.AIGlobalSettings = &settings
	}
	if src.HasProxyNodes {
		dst.ProxyNodes = c.mergeAIProxyNodes(dst.ProxyNodes, src.ProxyNodes, -1)
	}
}

func (c *ConfigManager) persistSyncSnapshot(snap *SyncSnapshot) error {
	c.mu.Lock()
	if err := c.saveConnectionsFile(snap.Connections); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("保存连接失败：%w", err)
	}
	if err := c.saveCredentialsFile(snap.Credentials); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("保存凭据失败：%w", err)
	}
	if err := atomicWriteFile(c.quickCmdFile, []byte(snap.QuickCommands), 0600); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("保存快捷命令失败：%w", err)
	}
	if err := c.saveTombstoneStore(syncTombstoneStore{
		Connections:  pruneConnectionTombstones(snap.DeletedConnections, snap.Connections),
		Credentials:  pruneCredentialTombstones(snap.DeletedCredentials, snap.Credentials),
		PrunedBefore: snap.TombstonePrunedBefore,
	}); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("保存同步墓碑失败：%w", err)
	}
	c.connCacheDirty, c.credCacheDirty = true, true
	c.mu.Unlock()

	if err := c.SaveAIProviderRegistry(aitypes.AIProviderRegistry{Providers: snap.AIProviders}); err != nil {
		return fmt.Errorf("保存 AI 供应商失败：%w", err)
	}
	// ponytail: ai_global_settings 不再同步, 不在 persistSyncSnapshot 中保存/清除
	if err := c.SaveAIProxyNodes(snap.ProxyNodes); err != nil {
		return fmt.Errorf("保存 AI 代理节点失败：%w", err)
	}
	if err := atomicWriteFile(c.syncTimeFile, []byte(fmt.Sprintf("%d", snap.SnapshotTime)), 0600); err != nil {
		return fmt.Errorf("保存同步时间失败：%w", err)
	}
	return nil
}

// AutoSync 自动同步：下载云端 → 双向合并(本地优先) → 上传到所有已配置的云端。
// 启动时也会调用，确保多设备间数据一致。
// 失败时最多重试 3 次（间隔 2s/4s/8s），仍失败则通过 Wails 事件通知前端。
func (c *ConfigManager) AutoSync() {
	if !c.GetAutoSyncEnabled() {
		return
	}
	// ponytail: 并发去重，避免多入口同时触发浪费网络资源
	if !c.syncRunning.CompareAndSwap(false, true) {
		return
	}
	defer c.syncRunning.Store(false)

	providers, failures := c.getSyncProviders()
	for _, failure := range failures {
		log.Printf("autoSync provider %s initialization failed: %v", failure.provider, failure.err)
		c.emitSyncFailure(failure.provider, failure.err)
	}
	if c.GetSyncMode() == "all" {
		if len(failures) > 0 {
			closeProviders(providers)
			return
		}
		if _, err := c.syncAllProviders(providers, c.GetRecoveryPassword(), false, true); err != nil {
			log.Printf("autoSync all failed: %v", err)
			c.emitSyncFailure("all", err)
		}
		return
	}
	const maxRetries = 3

	var wg sync.WaitGroup
	for _, p := range providers {
		wg.Add(1)
		go func(p providerEntry) {
			defer wg.Done()
			if cl, ok := p.storage.(storageCloser); ok {
				defer cl.Close()
			}

			var lastErr error
			for attempt := 0; attempt < maxRetries; attempt++ {
				if attempt > 0 {
					time.Sleep(time.Duration(1<<uint(attempt)) * time.Second) // 2s, 4s, 8s
				}
				lastErr = c.autoSyncProvider(p.storage, p.maxBackups, p.provider)
				if lastErr == nil {
					return
				}
				log.Printf("autoSync attempt %d/%d failed: %v", attempt+1, maxRetries, lastErr)
				if errors.Is(lastErr, errRecoveryPassword) {
					break
				}
			}

			// 全部重试失败，通知前端
			if lastErr != nil {
				log.Printf("autoSync all %d attempts failed for %s: %v", maxRetries, p.provider, lastErr)
				c.emitSyncFailure(p.provider, lastErr)
			}
		}(p)
	}
	wg.Wait()
}

// RetrySync 前端手动重试同步，返回错误信息供前端展示
func (c *ConfigManager) RetrySync() string {
	return c.retrySync(false)
}

// EnsureRemoteDirAndRetrySync 先尝试重建远端同步目录，再重试同步。
// 用于「远程目录 404/被删除」场景；返回空字符串表示成功。
func (c *ConfigManager) EnsureRemoteDirAndRetrySync() string {
	return c.retrySync(true)
}

func (c *ConfigManager) retrySync(ensureDir bool) string {
	providers, failures := c.getSyncProviders()
	if c.GetSyncMode() == "all" {
		if err := providerFailuresError(failures); err != nil {
			closeProviders(providers)
			return err.Error()
		}
		if ensureDir {
			if err := ensureProvidersRemoteDirs(providers); err != nil {
				closeProviders(providers)
				return err.Error()
			}
		}
		_, err := c.syncAllProviders(providers, c.GetRecoveryPassword())
		if err != nil {
			return err.Error()
		}
		return ""
	}
	var errs []string
	for _, failure := range failures {
		errs = append(errs, fmt.Sprintf("%s: %v", failure.provider, failure.err))
	}
	for _, p := range providers {
		func() {
			if cl, ok := p.storage.(storageCloser); ok {
				defer cl.Close()
			}
			if ensureDir {
				if ensurer, ok := p.storage.(remoteDirEnsurer); ok {
					if err := ensurer.EnsureRemoteDir(); err != nil {
						errs = append(errs, fmt.Sprintf("%s: 重建远程目录失败: %v", p.provider, err))
						return
					}
				}
			}
			if err := c.autoSyncProvider(p.storage, p.maxBackups, p.provider); err != nil {
				errs = append(errs, fmt.Sprintf("%T: %v", p.storage, err))
			}
		}()
	}
	if len(errs) > 0 {
		return strings.Join(errs, "; ")
	}
	return ""
}

func ensureProvidersRemoteDirs(providers []providerEntry) error {
	var errs []string
	for _, p := range providers {
		ensurer, ok := p.storage.(remoteDirEnsurer)
		if !ok {
			continue
		}
		if err := ensurer.EnsureRemoteDir(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: 重建远程目录失败: %v", p.provider, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// cmdKey 生成去重键：名称+命令（命令相同的项视为重复）
func cmdKey(m map[string]interface{}) string {
	name, _ := m["name"].(string)
	cmd, _ := m["command"].(string)
	return name + "|||" + cmd
}

// cmdLastModified 从 map 中读取 last_modified（JSON 数字是 float64）
func cmdLastModified(m map[string]interface{}) int64 {
	v, _ := m["last_modified"].(float64)
	return int64(v)
}

// stripQuickExpanded 递归去掉快捷命令树里的 expanded（纯 UI 折叠态，不应驱动云同步乒乓）。
func stripQuickExpanded(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			if k == "expanded" {
				continue
			}
			out[k] = stripQuickExpanded(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			out[i] = stripQuickExpanded(val)
		}
		return out
	default:
		return v
	}
}

// quickCmdsEqual JSON 语义比较（忽略 key 顺序与 expanded），避免前端/安卓序列化差异导致误判
func quickCmdsEqual(a, b string) bool {
	if a == b {
		return true
	}
	var va, vb interface{}
	if err := json.Unmarshal([]byte(a), &va); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &vb); err != nil {
		return false
	}
	va = stripQuickExpanded(va)
	vb = stripQuickExpanded(vb)
	da, _ := json.Marshal(va)
	db, _ := json.Marshal(vb)
	return string(da) == string(db)
}

// mergeQuickCommands 合并本地和远端的快捷命令列表：
// - 顺序跟随 last_modified 较新的一边（移动后该边 max 更大）
// - 重叠项（同 name+command）：按 last_modified 取最新
// - 单侧独有：last_modified > lastSyncTime → 保留，否则视为已删除
// - 组内 children 同样逻辑
func (c *ConfigManager) mergeQuickCommands(localStr, remoteStr string, lastSyncTime int64) string {
	parseQuick := func(raw string) ([]interface{}, bool) {
		if strings.TrimSpace(raw) == "" {
			return []interface{}{}, true
		}
		var arr []interface{}
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			return nil, false
		}
		return arr, true
	}

	local, ok := parseQuick(localStr)
	if !ok {
		return localStr
	}
	remote, ok := parseQuick(remoteStr)
	if !ok {
		return localStr
	}

	// build key-indexed maps
	type cmdEntry struct {
		item map[string]interface{}
		key  string
	}
	localMap := make(map[string]cmdEntry)
	for _, item := range local {
		if m, ok := item.(map[string]interface{}); ok {
			key := cmdKey(m)
			localMap[key] = cmdEntry{m, key}
		}
	}
	remoteMap := make(map[string]cmdEntry)
	for _, item := range remote {
		if m, ok := item.(map[string]interface{}); ok {
			key := cmdKey(m)
			remoteMap[key] = cmdEntry{m, key}
		}
	}

	// 顺序跟随 last_modified 较新的一边（移动后该边 max 更大）
	baseIsRemote := maxQuickLastModified(remote) > maxQuickLastModified(local)
	var base, other []interface{}
	var otherMap map[string]cmdEntry
	if baseIsRemote {
		base, other, otherMap = remote, local, localMap
	} else {
		base, other, otherMap = local, remote, remoteMap
	}

	result := make([]interface{}, 0)
	added := make(map[string]bool)

	for _, item := range base {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		key := cmdKey(m)
		if added[key] {
			continue
		}
		if re, inOther := otherMap[key]; inOther {
			// 重叠：按 last_modified 取最新
			if cmdLastModified(re.item) > cmdLastModified(m) {
				if lCh, ok := m["children"]; ok {
					if rCh, ok := re.item["children"]; ok {
						re.item["children"] = c.mergeCmdChildren(lCh, rCh, lastSyncTime)
					}
				}
				result = append(result, re.item)
			} else {
				if lCh, ok := m["children"]; ok {
					if rCh, ok := re.item["children"]; ok {
						m["children"] = c.mergeCmdChildren(lCh, rCh, lastSyncTime)
					}
				}
				result = append(result, m)
			}
			added[key] = true
		} else {
			// 独有：last_modified > lastSyncTime → 保留（新增），否则删除
			if cmdLastModified(m) > lastSyncTime {
				result = append(result, m)
			}
			added[key] = true
		}
	}

	// 另一边独有（按其原始顺序）：last_modified > lastSyncTime → 保留
	for _, item := range other {
		if m, ok := item.(map[string]interface{}); ok {
			key := cmdKey(m)
			if !added[key] && cmdLastModified(m) > lastSyncTime {
				result = append(result, m)
			}
		}
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data)
}

// mergeCmdChildren 合并两个 children 数组（同 mergeQuickCommands 逻辑）
func (c *ConfigManager) mergeCmdChildren(localCh, remoteCh interface{}, lastSyncTime int64) interface{} {
	lArr, lok := localCh.([]interface{})
	rArr, rok := remoteCh.([]interface{})
	if !lok || !rok {
		return remoteCh
	}

	lMap := make(map[string]map[string]interface{})
	for _, item := range lArr {
		if m, ok := item.(map[string]interface{}); ok {
			lMap[cmdKey(m)] = m
		}
	}
	rMap := make(map[string]map[string]interface{})
	for _, item := range rArr {
		if m, ok := item.(map[string]interface{}); ok {
			rMap[cmdKey(m)] = m
		}
	}

	// 顺序跟随 last_modified 较新的一边
	baseIsRemote := maxQuickLastModified(rArr) > maxQuickLastModified(lArr)
	var base, other []interface{}
	var otherMap map[string]map[string]interface{}
	if baseIsRemote {
		base, other, otherMap = rArr, lArr, lMap
	} else {
		base, other, otherMap = lArr, rArr, rMap
	}

	var result []interface{}
	added := make(map[string]bool)
	for _, item := range base {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		key := cmdKey(m)
		if added[key] {
			continue
		}
		if rm, inOther := otherMap[key]; inOther {
			if cmdLastModified(rm) > cmdLastModified(m) {
				result = append(result, rm)
			} else {
				result = append(result, m)
			}
			added[key] = true
		} else {
			if cmdLastModified(m) > lastSyncTime {
				result = append(result, m)
			}
			added[key] = true
		}
	}
	for _, item := range other {
		if m, ok := item.(map[string]interface{}); ok {
			key := cmdKey(m)
			if !added[key] && cmdLastModified(m) > lastSyncTime {
				result = append(result, m)
			}
		}
	}
	return result
}

// maxQuickLastModified 递归计算数组中最大的 last_modified（含 children）
func maxQuickLastModified(arr []interface{}) int64 {
	var max int64
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if lm := cmdLastModified(m); lm > max {
			max = lm
		}
		if ch, ok := m["children"].([]interface{}); ok {
			if childMax := maxQuickLastModified(ch); childMax > max {
				max = childMax
			}
		}
	}
	return max
}

func markSnapshotRestored(snap *SyncSnapshot, t int64) {
	if snap == nil {
		return
	}
	for i := range snap.Connections {
		snap.Connections[i].LastModified = t
	}
	for i := range snap.Credentials {
		snap.Credentials[i].LastModified = t
	}
	if snap.QuickCommands != "" {
		snap.QuickCommands = touchQuickCommands(snap.QuickCommands, t)
	}
	for i := range snap.AIProviders {
		snap.AIProviders[i].UpdatedAt = t
	}
	if snap.AIGlobalSettings != nil {
		snap.AIGlobalSettings.UpdatedAt = t
		snap.AIGlobalSettings.ProxyNodes = nil
	}
	for i := range snap.ProxyNodes {
		snap.ProxyNodes[i].UpdatedAt = t
	}
	snap.SnapshotTime = t
}

func touchQuickCommands(raw string, t int64) string {
	var arr []interface{}
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return raw
	}
	touchQuickArray(arr, t)
	data, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return raw
	}
	return string(data)
}

func touchQuickArray(arr []interface{}, t int64) {
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		m["last_modified"] = t
		if children, ok := m["children"].([]interface{}); ok {
			touchQuickArray(children, t)
		}
	}
}

// restoreSnapshotToLocal 将快照中的所有数据恢复到本地文件
func (c *ConfigManager) restoreSnapshotToLocal(snap *SyncSnapshot) {
	if snap == nil {
		return
	}
	c.mu.Lock()
	if err := c.saveConnectionsFile(snap.Connections); err != nil {
		log.Printf("[restoreSnapshotToLocal] failed to save connections: %v", err)
	}
	c.connCacheDirty = true
	if snap.Credentials != nil {
		if err := c.saveCredentialsFile(snap.Credentials); err != nil {
			log.Printf("[restoreSnapshotToLocal] failed to save credentials: %v", err)
		}
		c.credCacheDirty = true
	}
	if err := c.saveTombstoneStore(syncTombstoneStore{
		Connections:  pruneConnectionTombstones(snap.DeletedConnections, snap.Connections),
		Credentials:  pruneCredentialTombstones(snap.DeletedCredentials, snap.Credentials),
		PrunedBefore: snap.TombstonePrunedBefore,
	}); err != nil {
		log.Printf("[restoreSnapshotToLocal] failed to save tombstones: %v", err)
	}
	c.mu.Unlock()
	if snap.HasQuickCommands {
		if err := atomicWriteFile(c.quickCmdFile, []byte(snap.QuickCommands), 0600); err != nil {
			log.Printf("[restoreSnapshotToLocal] failed to write quick commands: %v", err)
		}
	}
	if snap.AIProviders != nil {
		if err := c.SaveAIProviderRegistry(aitypes.AIProviderRegistry{Providers: snap.AIProviders}); err != nil {
			log.Printf("[restoreSnapshotToLocal] failed to write AI providers: %v", err)
		}
	}
	if snap.AIGlobalSettings != nil {
		if err := c.SaveAIGlobalSettings(*snap.AIGlobalSettings); err != nil {
			log.Printf("[restoreSnapshotToLocal] failed to write AI global settings: %v", err)
		}
	}
	if snap.ProxyNodes != nil {
		if err := c.SaveAIProxyNodes(snap.ProxyNodes); err != nil {
			log.Printf("[restoreSnapshotToLocal] failed to write AI proxy nodes: %v", err)
		}
	}
}

// restoreFromProvider 是 RestoreFromXxxFile 的共享实现：
// 读取远端文件 → 解密解析快照 → 写回本地。
// 统一用 filepath.Base 防止路径穿越。
func (c *ConfigManager) restoreFromProvider(s RemoteStorage, filename string, maxBackups int, password string, provider string) error {
	filename = filepath.Base(filename) // 防止路径穿越
	data, err := s.ReadFile(filename)
	if err != nil {
		return err
	}
	snap, err := c.decryptAndParseSnapshot(string(data), password)
	if err != nil {
		return err
	}
	restoreTime := time.Now().UnixMilli()
	markSnapshotRestored(snap, restoreTime)
	c.restoreSnapshotToLocal(snap)
	atomicWriteFile(c.syncTimeFile, []byte(fmt.Sprintf("%d", restoreTime)), 0600)
	c.saveLastSyncTime(provider, restoreTime-1)
	if _, err := c.backupConnections(s, maxBackups); err != nil {
		return err
	}
	c.recordSyncCompleted(provider)
	return nil
}

// restoreResult 将 restoreFromProvider 的 error 结果包装为各 Restore 方法统一的返回值
func restoreResult(err error) (map[string]interface{}, error) {
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"success": true}, nil
}

// ─── 备份提供者共享包装方法 ─────────────────────────────────
// 以下 backupTo/listBackupsFrom/syncFrom/restoreFrom 方法消除了 4 个提供者
// （webdav/r2/ftp/sftp）各自的 BackupToXxx/ListXxxBackups/SyncFromXxx/RestoreFromXxxFile
// 样板代码，统一处理 storageCloser 释放。

// backupTo 创建存储、备份、关闭连接
func (c *ConfigManager) backupTo(provider string, storageFn func() (RemoteStorage, int, error)) (map[string]interface{}, error) {
	s, max, err := storageFn()
	if err != nil {
		return nil, err
	}
	if cl, ok := s.(storageCloser); ok {
		defer cl.Close()
	}
	result, err := c.backupConnections(s, max)
	if err != nil {
		return nil, err
	}
	c.recordSyncCompleted(provider)
	return result, nil
}

// listBackupsFrom 创建存储、列出备份、关闭连接
func (c *ConfigManager) listBackupsFrom(storageFn func() (RemoteStorage, int, error)) ([]map[string]interface{}, error) {
	s, _, err := storageFn()
	if err != nil {
		return nil, err
	}
	if cl, ok := s.(storageCloser); ok {
		defer cl.Close()
	}
	return c.listBackupFiles(s)
}

// syncFrom 创建存储、同步、关闭连接
func (c *ConfigManager) syncFrom(provider string, storageFn func() (RemoteStorage, int, error)) (map[string]interface{}, error) {
	s, max, err := storageFn()
	if err != nil {
		return nil, err
	}
	if cl, ok := s.(storageCloser); ok {
		defer cl.Close()
	}
	return c.syncFromProvider(s, max, c.GetRecoveryPassword(), provider)
}

// restoreFrom 创建存储、恢复、关闭连接。
// password 为空时仅尝试明文和旧版云端派生密钥；非空时用于 .lumin2 或旧 hex 解密。
func (c *ConfigManager) restoreFrom(provider string, storageFn func() (RemoteStorage, int, error), filename string, password string) (map[string]interface{}, error) {
	s, max, err := storageFn()
	if err != nil {
		return nil, err
	}
	if cl, ok := s.(storageCloser); ok {
		defer cl.Close()
	}
	return restoreResult(c.restoreFromProvider(s, filename, max, password, provider))
}
