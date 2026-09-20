package config

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/studio-b12/gowebdav"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/text/encoding/ianaindex"

	aitypes "lumeterm/internal/aitypes"
	"lumeterm/internal/apppaths"
	runtimeenv "lumeterm/module/runtimeenv"
)

// parseIntOrDefault 解析字符串为整数，失败时返回默认值
// ponytail: 不区分"0"和解析失败（保持向后兼容），通过日志诊断
func parseIntOrDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		slog.Error("配置整数解析失败", "raw", s, "error", err)
		return def
	}
	return v
}

func normalizeTerminalEncoding(value string) string {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return "utf-8"
	}
	if strings.EqualFold(normalized, "utf8") || strings.EqualFold(normalized, "utf-8") {
		return "utf-8"
	}
	encodingValue, err := ianaindex.IANA.Encoding(normalized)
	if err != nil || encodingValue == nil {
		return "utf-8"
	}
	name, err := ianaindex.IANA.Name(encodingValue)
	if err != nil || strings.TrimSpace(name) == "" {
		return "utf-8"
	}
	return strings.ToLower(strings.TrimSpace(name))
}

// Connection struct
type Connection struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	Username            string `json:"username"`
	Password            string `json:"password,omitempty"`
	AuthMethod          string `json:"authMethod"`
	PrivateKey          string `json:"privateKey,omitempty"`
	Passphrase          string `json:"passphrase,omitempty"`
	Group               string `json:"group,omitempty"` // 服务器分组，空=未分组
	Os                  string `json:"os,omitempty"`
	CredentialID        string `json:"credentialId,omitempty"` // ponytail: 非空时用 Credential 认证，忽略内联字段
	TerminalInitPath    string `json:"terminalInitPath,omitempty"`
	FileManagerInitPath string `json:"fileManagerInitPath,omitempty"`
	TerminalEncoding    string `json:"terminalEncoding,omitempty"`
	AllowLegacySSHRSA   bool   `json:"allowLegacySshRsa,omitempty"`
	// AutoReconnect 自动重连(按服务器粒度,默认关):传输/保活断开后前端自动重连该会话。
	AutoReconnect bool   `json:"autoReconnect,omitempty"`
	ProxyMode     string `json:"proxyMode,omitempty"`
	ProxyNodeID   string `json:"proxyNodeId,omitempty"`
	ProxyType     string `json:"proxyType,omitempty"`
	ProxyHost     string `json:"proxyHost,omitempty"`
	ProxyPort     int    `json:"proxyPort,omitempty"`
	ProxyUsername string `json:"proxyUsername,omitempty"`
	ProxyPassword string `json:"proxyPassword,omitempty"`
	LastModified  int64  `json:"last_modified,omitempty"` // Unix 毫秒时间戳，合并时判断新旧
}

// Credential 可复用的认证凭据，多个 Connection 可引用同一 Credential
type Credential struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	AuthMethod   string `json:"authMethod"` // "password" | "privateKey"
	Username     string `json:"username"`
	Password     string `json:"password,omitempty"`      // 加密存储
	PrivateKey   string `json:"privateKey,omitempty"`    // 加密存储
	Passphrase   string `json:"passphrase,omitempty"`    // 加密存储
	LastModified int64  `json:"last_modified,omitempty"` // Unix 毫秒时间戳
}

type ChmodDialogSettings struct {
	Mode                  string `json:"mode,omitempty"`
	IncludeSubdirectories bool   `json:"includeSubdirectories,omitempty"`
	AutoApplyLastSettings bool   `json:"autoApplyLastSettings,omitempty"`
	LastModified          int64  `json:"last_modified,omitempty"`
}

type TransferTuningSettings struct {
	MaxPacketKiB        int  `json:"maxPacketKiB,omitempty"`
	MaxRequestsPerFile  int  `json:"maxRequestsPerFile,omitempty"`
	ConcurrentWrites    bool `json:"concurrentWrites,omitempty"`
	ApplyToSharedClient bool `json:"applyToSharedClient,omitempty"`
	Configured          bool `json:"configured,omitempty"`
}

type FileManagerSettings struct {
	ChmodDialog                     ChmodDialogSettings    `json:"chmodDialog,omitempty"`
	DoubleClickUncompressArchive    bool                   `json:"doubleClickUncompressArchive,omitempty"`
	SmartUncompressConflictStrategy string                 `json:"smartUncompressConflictStrategy,omitempty"`
	AutoRefreshDisabled             bool                   `json:"autoRefreshDisabled,omitempty"`
	MaxEditSizeMB                   int                    `json:"maxEditSizeMB,omitempty"`
	TransferTuning                  TransferTuningSettings `json:"transferTuning,omitempty"`
}

// 文件编辑大小上限相关常量（单位 MB）。
const (
	fileManagerMaxEditSizeDefaultMB = 5
	fileManagerMaxEditSizeHardCapMB = 50 // 与底层 ReadFileBytes 的内存安全防线对齐，用户配置不可超过
)

// normalizeFileManagerMaxEditSizeMB 将用户配置的编辑大小上限归一化到合法范围 [1, 50]。
// 未配置（0）或越界时返回默认值 5MB，保证老配置文件行为不变。
func normalizeFileManagerMaxEditSizeMB(mb int) int {
	if mb < 1 || mb > fileManagerMaxEditSizeHardCapMB {
		return fileManagerMaxEditSizeDefaultMB
	}
	return mb
}

type AppSettings struct {
	WebviewGpuDisabled bool                 `json:"webviewGpuDisabled,omitempty"`
	RuntimeEnvironment runtimeenv.Settings  `json:"runtimeEnvironment,omitempty"`
	ThemePackages      ThemePackageSettings `json:"themePackages,omitempty"`
	TasksDir           string               `json:"tasksDir,omitempty"` // ponytail: 自定义 AI 对话存储路径, 空则用默认 configDir/tasks
}

type WorkspacePrefs struct {
	RememberWorkspace         bool   `json:"rememberWorkspace"`
	WorkspacePersistenceLevel string `json:"workspacePersistenceLevel,omitempty"`
}

// PersistedPortForward 端口映射持久化记录, 按 serverId(=Connection.ID) 分组存储.
// Enabled=true 表示期望运行态, false 表示已停止. LastModified 供未来同步合并使用.
type PersistedPortForward struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	LocalHost    string `json:"localHost"`
	LocalPort    string `json:"localPort"`
	RemoteHost   string `json:"remoteHost"`
	RemotePort   string `json:"remotePort"`
	Enabled      bool   `json:"enabled"`
	LastModified int64  `json:"lastModified,omitempty"`
}

type ConfigManager struct {
	configDir                 string
	programDir                string
	connFile                  string
	credFile                  string
	davFile                   string
	key                       []byte
	gcm                       cipher.AEAD // ponytail: 仅用于解密旧版密文（明文存储过渡），不再用于加密
	syncModeFile              string
	autoSyncEnabledFile       string
	syncTimeFile              string // 本地快照时间戳文件
	lastSyncFile              string // 上次同步时间戳文件（仅在同步完成时更新）
	tombstoneFile             string // 同步删除墓碑（连接/凭据）
	recoveryPasswordFile      string // 恢复密码（明文存储，0600）
	quickCmdFile              string
	paramHistFile             string
	fileManagerSettingsFile   string
	workspaceStateFile        string
	workspacePrefsFile        string
	workspaceSessionStateFile string
	portForwardsFile          string
	appSettingsFile           string
	historyDir                string
	globalHistFile            string
	mu                        sync.RWMutex
	connCache                 []Connection // 缓存连接列表
	connCacheDirty            bool         // 缓存是否需要刷新
	credCache                 []Credential // 缓存凭据列表
	credCacheDirty            bool         // 凭据缓存是否需要刷新
	syncRunning               atomic.Bool  // AutoSync 并发去重
	wailsCtx                  context.Context
	syncEventForTest          func(string, map[string]interface{})
	syncProvidersForTest      func() ([]providerEntry, []providerFailure)
	onDeleteConnection        func(string) // 删除连接时回调（清理 pingStates 等包级状态）
}

func NewConfigManager() *ConfigManager {
	dir := filepath.Join(apppaths.DataRoot(), "config")

	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Fatalf("无法创建配置目录 %s: %v", dir, err)
	}

	var key []byte

	connFile := filepath.Join(dir, "connections.json")
	credFile := filepath.Join(dir, "credentials.json")
	davFile := filepath.Join(dir, "webdav.json")
	quickCmdFile := filepath.Join(dir, "quick_commands.json")
	paramHistFile := filepath.Join(dir, "param_history.json")
	fileManagerSettingsFile := filepath.Join(dir, "file_manager_settings.json")
	workspaceStateFile := filepath.Join(dir, "workspace_state.json")
	workspacePrefsFile := filepath.Join(dir, "workspace_prefs.json")
	workspaceSessionStateFile := filepath.Join(dir, "workspace_sessions.json")
	portForwardsFile := filepath.Join(dir, "port_forwards.json")
	appSettingsFile := filepath.Join(dir, "app_settings.json")
	historyDir := filepath.Join(dir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		log.Printf("[NewConfigManager] 无法创建历史目录 %s: %v", historyDir, err)
	}

	// 配置明文存储：密钥文件与密文本就同目录，本地加密无真实防护，已退役。
	// 旧版密钥文件（lumin.key）存在时加载，仅用于解密旧密文（读兼容过渡）；全新用户用随机占位 key（不落盘）。
	// 退役计划：v1.4.0 起明文化写入；存量用户任何一次配置保存即完成密文→明文迁移。
	// v1.5.0 起可删除本兼容分支与 decrypt 的旧密文路径（届时活跃用户均已经过 1.4.x 完成迁移；
	// 直接从 1.3.x 跳到删除版的用户需重填凭据密码，属可接受的边缘损失）。
	if data, err := os.ReadFile(filepath.Join(dir, "lumin.key")); err == nil && len(data) == 32 {
		key = data
	} else {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			log.Fatalf("无法生成占位密钥: %v", err)
		}
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		log.Fatalf("无法创建 AES cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		log.Fatalf("无法创建 GCM: %v", err)
	}

	return &ConfigManager{
		configDir:                 dir,
		connFile:                  connFile,
		credFile:                  credFile,
		davFile:                   davFile,
		key:                       key,
		gcm:                       gcm,
		syncModeFile:              filepath.Join(dir, "sync_mode.json"),
		autoSyncEnabledFile:       filepath.Join(dir, "auto_sync_enabled.json"),
		syncTimeFile:              filepath.Join(dir, "snapshot_time"),
		lastSyncFile:              filepath.Join(dir, "last_sync_time"),
		tombstoneFile:             filepath.Join(dir, "sync_tombstones.json"),
		recoveryPasswordFile:      filepath.Join(dir, "recovery_password"),
		quickCmdFile:              quickCmdFile,
		paramHistFile:             paramHistFile,
		fileManagerSettingsFile:   fileManagerSettingsFile,
		workspaceStateFile:        workspaceStateFile,
		workspacePrefsFile:        workspacePrefsFile,
		workspaceSessionStateFile: workspaceSessionStateFile,
		portForwardsFile:          portForwardsFile,
		appSettingsFile:           appSettingsFile,
		historyDir:                historyDir,
		globalHistFile:            filepath.Join(historyDir, "global.json"),
	}
}

func (c *ConfigManager) encrypt(text string) (string, error) {
	// ponytail: 配置明文存储（见 NewConfigManager），恒等返回；保留签名避免改动 30+ 调用点
	return text, nil
}

func (c *ConfigManager) decrypt(hexText string) string {
	if hexText == "" {
		return ""
	}
	// 旧密文（hex+GCM）解开返回明文；否则视为已是明文原样返回。
	// 误判兜底：明文恰好是合法 hex 时 GCM.Open 必失败，同样原样返回。
	ciphertext, err := hex.DecodeString(hexText)
	if err != nil || len(ciphertext) < c.gcm.NonceSize() {
		return hexText
	}
	nonce, ct := ciphertext[:c.gcm.NonceSize()], ciphertext[c.gcm.NonceSize():]
	plaintext, err := c.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return hexText
	}
	return string(plaintext)
}

const (
	// LUMETERM2 为当前密文格式前缀；LUMIN2 为改名前旧前缀，读取兼容。
	// 退役计划（含 .lumin2 扩展名识别、synchelper isBackupName 双后缀、
	// hasBackupPrefix/trimBackupPrefix 的旧分支）：云端旧备份会被滚动清理自然消失，
	// 但用户手里的 .lumin2 导出文件属长期资产，删除兼容即拒收老备份文件。
	// 建议保留至 v2.0 或发布明确的弃用公告后再删。
	lumeterm2Prefix     = "LUMETERM2:"
	legacyLumin2Prefix  = "LUMIN2:"
	lumeterm2Iterations = 210000
	lumeterm2HeaderSize = 1 + 4 + 16 + 12
)

var (
	errRecoveryPassword              = errors.New("恢复密码错误")
	errRecoveryPasswordResetRequired = errors.New("RECOVERY_PASSWORD_RESET_REQUIRED")
)

// hasBackupPrefix 判断是否为受支持的密文（新 LUMETERM2: 或旧 LUMIN2:）
func hasBackupPrefix(text string) bool {
	return strings.HasPrefix(text, lumeterm2Prefix) || strings.HasPrefix(text, legacyLumin2Prefix)
}

// trimBackupPrefix 去掉密文前缀（新旧皆可）
func trimBackupPrefix(text string) string {
	if strings.HasPrefix(text, lumeterm2Prefix) {
		return text[len(lumeterm2Prefix):]
	}
	return strings.TrimPrefix(text, legacyLumin2Prefix)
}

func encryptLUMETERM2(text, password string) (string, error) {
	salt, nonce := make([]byte, 16), make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return encryptLUMETERM2WithSaltNonce(text, password, salt, nonce)
}

func encryptLUMETERM2WithSaltNonce(text, password string, salt, nonce []byte) (string, error) {
	if len(salt) != 16 || len(nonce) != 12 {
		return "", fmt.Errorf("LUMETERM2 salt/nonce 长度无效")
	}
	key := pbkdf2.Key([]byte(password), salt, lumeterm2Iterations, 32, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("cipher.NewGCM: %w", err)
	}
	payload := make([]byte, lumeterm2HeaderSize)
	payload[0] = 2
	binary.BigEndian.PutUint32(payload[1:5], lumeterm2Iterations)
	copy(payload[5:21], salt)
	copy(payload[21:33], nonce)
	payload = gcm.Seal(payload, nonce, []byte(text), nil)
	return lumeterm2Prefix + base64.StdEncoding.EncodeToString(payload), nil
}

func decryptLUMETERM2(text, password string) (string, error) {
	if !hasBackupPrefix(text) {
		return "", fmt.Errorf("缺少 LUMETERM2 前缀")
	}
	payload, err := base64.StdEncoding.DecodeString(trimBackupPrefix(text))
	if err != nil {
		return "", fmt.Errorf("LUMETERM2 Base64 无效: %w", err)
	}
	if len(payload) < lumeterm2HeaderSize+16 {
		return "", fmt.Errorf("LUMETERM2 数据长度不足")
	}
	if payload[0] != 2 {
		return "", fmt.Errorf("不支持的 LUMETERM2 版本: %d", payload[0])
	}
	iterations := binary.BigEndian.Uint32(payload[1:5])
	if iterations < 100000 || iterations > 2000000 {
		return "", fmt.Errorf("LUMETERM2 迭代次数无效: %d", iterations)
	}
	key := pbkdf2.Key([]byte(password), payload[5:21], int(iterations), 32, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("LUMETERM2 密钥无效: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("LUMETERM2 GCM 初始化失败: %w", err)
	}
	plaintext, err := gcm.Open(nil, payload[21:33], payload[33:], nil)
	if err != nil {
		return "", fmt.Errorf("%w，请确认密码是否正确", errRecoveryPassword)
	}
	return string(plaintext), nil
}

func (c *ConfigManager) GetConnections() []Connection {
	c.mu.RLock()
	if !c.connCacheDirty && c.connCache != nil {
		result := make([]Connection, len(c.connCache))
		copy(result, c.connCache)
		c.mu.RUnlock()
		return result
	}
	c.mu.RUnlock()

	// 缓存未命中，升级为写锁刷新缓存
	c.mu.Lock()
	// double-check：另一个 goroutine 可能已经刷新了
	if !c.connCacheDirty && c.connCache != nil {
		result := make([]Connection, len(c.connCache))
		copy(result, c.connCache)
		c.mu.Unlock()
		return result
	}
	conns := c.getConnectionsLocked()
	c.connCache = conns
	c.connCacheDirty = false
	result := make([]Connection, len(conns))
	copy(result, conns)
	c.mu.Unlock()
	return result
}

// getConnectionsLocked 读取连接列表，调用方需持有 c.mu
func (c *ConfigManager) getConnectionsLocked() []Connection {
	data, err := os.ReadFile(c.connFile)
	if err != nil {
		return []Connection{}
	}
	var conns []Connection
	if err := json.Unmarshal(data, &conns); err != nil {
		log.Printf("[getConnectionsLocked] json.Unmarshal failed: %v", err)
		return []Connection{}
	}
	for i := range conns {
		conns[i].Password = c.decrypt(conns[i].Password)
		conns[i].Passphrase = c.decrypt(conns[i].Passphrase)
		conns[i].PrivateKey = c.decryptOrPassthrough(conns[i].PrivateKey)
		conns[i].ProxyPassword = c.decrypt(conns[i].ProxyPassword)
		sanitizeConnectionProxyConfig(&conns[i])
		conns[i].TerminalEncoding = normalizeTerminalEncoding(conns[i].TerminalEncoding)
	}
	return conns
}

// decryptOrPassthrough 尝试解密 PrivateKey，失败则原样返回
// 用于明文私钥到加密私钥的平滑迁移：旧配置里 PrivateKey 是明文 PEM，加密后是 hex
// ponytail: 用 "-----BEGIN" 前缀区分明文与密文，PEM 头固定以此开头，hex 不含
func (c *ConfigManager) decryptOrPassthrough(text string) string {
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "-----BEGIN") {
		return text
	}
	return c.decrypt(text)
}

// GetConnectionByID 按 ID 返回连接的深拷贝。
// 返回值语义避免返回指向内部临时切片的指针，防止调用方修改的是副本而非真实配置。
func (c *ConfigManager) GetConnectionByID(id string) (Connection, bool) {
	conns := c.GetConnections()
	for _, conn := range conns {
		if conn.ID == id {
			return conn, true
		}
	}
	return Connection{}, false
}

// GetConnectionsMasked 返回掩码后的连接列表，用于前端显示
func (c *ConfigManager) GetConnectionsMasked() []Connection {
	conns := c.GetConnections()
	for i := range conns {
		if conns[i].Password != "" {
			conns[i].Password = "****"
		}
		if conns[i].PrivateKey != "" {
			conns[i].PrivateKey = "[key configured]"
		}
		if conns[i].Passphrase != "" {
			conns[i].Passphrase = "****"
		}
		if conns[i].ProxyPassword != "" {
			conns[i].ProxyPassword = "****"
		}
	}
	return conns
}

func (c *ConfigManager) SaveConnection(conn Connection, noSync bool) Connection {
	sanitizeConnectionProxyConfig(&conn)
	conn.TerminalEncoding = normalizeTerminalEncoding(conn.TerminalEncoding)
	c.mu.Lock()
	defer c.mu.Unlock()
	conns := c.getConnectionsLocked()
	if conn.ID == "" {
		// ponytail: 用 crypto/rand 替代 UnixNano，避免 Windows 精度不足导致 ID 冲突
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			conn.ID = strconv.FormatInt(time.Now().UnixNano(), 10)
		} else {
			conn.ID = fmt.Sprintf("%x", b)
		}
		conns = append(conns, conn)
	} else {
		found := false
		for i, existing := range conns {
			if existing.ID == conn.ID {
				// If no new password provided, keep old
				if conn.Password == "" && existing.Password != "" {
					conn.Password = existing.Password
				}
				// If no new private key provided, keep old
				if conn.PrivateKey == "" && existing.PrivateKey != "" {
					conn.PrivateKey = existing.PrivateKey
				}
				// If no new passphrase provided, keep old
				if conn.Passphrase == "" && existing.Passphrase != "" {
					conn.Passphrase = existing.Passphrase
				}
				if conn.ProxyMode == "custom" && conn.ProxyPassword == "" && existing.ProxyPassword != "" {
					conn.ProxyPassword = existing.ProxyPassword
				}
				conns[i] = conn
				found = true
				break
			}
		}
		if !found {
			conns = append(conns, conn)
		}
	}

	conn.LastModified = time.Now().UnixMilli()
	// 更新 conns 中对应条目的时间戳
	for i, s := range conns {
		if s.ID == conn.ID {
			conns[i].LastModified = conn.LastModified
			break
		}
	}

	if err := c.saveConnectionsFile(conns); err != nil {
		log.Printf("[SaveConnection] failed to save connections: %v", err)
	}
	// 同 id 重新保存视为复活：清掉旧墓碑，避免被同步再压回去
	c.clearConnectionTombstonesLocked([]string{conn.ID})
	c.connCacheDirty = true // 标记缓存需要刷新
	if noSync {
		// ponytail: 连接前仅写盘不触发同步，等 OS 更新后一起同步，避免两次上传
	} else {
		c.bumpSnapshotTime()
		go c.AutoSync()
	}
	return conn
}

// saveConnectionsFile 加密并原子写入连接列表，调用方需持有 c.mu
func (c *ConfigManager) saveConnectionsFile(conns []Connection) error {
	toSave := make([]Connection, len(conns))
	copy(toSave, conns)
	for i := range toSave {
		encPass, err := c.encrypt(toSave[i].Password)
		if err != nil {
			return fmt.Errorf("encrypt password: %w", err)
		}
		encPhrase, err := c.encrypt(toSave[i].Passphrase)
		if err != nil {
			return fmt.Errorf("encrypt passphrase: %w", err)
		}
		encKey, err := c.encrypt(toSave[i].PrivateKey)
		if err != nil {
			return fmt.Errorf("encrypt privateKey: %w", err)
		}
		encProxyPass, err := c.encrypt(toSave[i].ProxyPassword)
		if err != nil {
			return fmt.Errorf("encrypt proxyPassword: %w", err)
		}
		toSave[i].Password = encPass
		toSave[i].Passphrase = encPhrase
		toSave[i].PrivateKey = encKey
		toSave[i].ProxyPassword = encProxyPass
	}
	data, err := json.MarshalIndent(toSave, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal connections: %w", err)
	}
	// 原子写入：先写临时文件，再 rename 覆盖，避免中断损坏原文件
	return atomicWriteFile(c.connFile, data, 0600)
}

// loadRawFile 读取配置文件的原始 JSON 字符串（用于同步快照）
func (c *ConfigManager) loadRawFile(path string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// atomicWriteFile 原子写入文件：先写临时文件 + fsync，再 rename 覆盖。
// fsync 防止进程快速退出时 OS 缓存未刷盘导致数据丢失。
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmpFile := path + ".tmp"
	f, err := os.OpenFile(tmpFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("open temp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	f.Close()
	if err := os.Rename(tmpFile, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// loadSnapshotTime 读取本地快照时间戳
func (c *ConfigManager) loadSnapshotTime() int64 {
	data, err := os.ReadFile(c.syncTimeFile)
	if err != nil {
		return 0
	}
	var t int64
	fmt.Sscanf(string(data), "%d", &t)
	return t
}

// bumpSnapshotTime 更新本地快照时间戳为当前时间
func (c *ConfigManager) bumpSnapshotTime() int64 {
	now := time.Now().UnixMilli()
	atomicWriteFile(c.syncTimeFile, []byte(fmt.Sprintf("%d", now)), 0600)
	return now
}

// loadLastSyncTimeMap 读取各同步后端的上次同步时间。
// 兼容旧版纯数字文件：仅归属到当前同步模式对应后端，避免跨后端误判删除。
func (c *ConfigManager) loadLastSyncTimeMap() map[string]int64 {
	data, err := os.ReadFile(c.lastSyncFile)
	if err != nil {
		return map[string]int64{}
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return map[string]int64{}
	}
	var asMap map[string]int64
	if err := json.Unmarshal([]byte(raw), &asMap); err == nil && asMap != nil {
		return asMap
	}
	// 旧版全局数字时间戳无法区分后端；直接丢弃，避免 R2 的 lastSync 污染 WebDAV 删除判定。
	// 首次同步后会改写为 {"provider": ts} 格式。
	return map[string]int64{}
}

// loadLastSyncTime 读取指定后端的上次同步时间；从未同步过该后端则返回 0。
func (c *ConfigManager) loadLastSyncTime(provider string) int64 {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return 0
	}
	return c.loadLastSyncTimeMap()[provider]
}

// loadLastSyncTimeMin 取多个后端中最小的上次同步时间；任一为 0 则返回 0（更保守，少误删）。
func (c *ConfigManager) loadLastSyncTimeMin(providers ...string) int64 {
	if len(providers) == 0 {
		return 0
	}
	m := c.loadLastSyncTimeMap()
	var min int64
	for i, p := range providers {
		p = strings.TrimSpace(p)
		t := m[p]
		if t <= 0 {
			return 0
		}
		if i == 0 || t < min {
			min = t
		}
	}
	return min
}

// loadLastSyncTimeMax 取所有已记录后端中最大的上次同步时间，供 UI 展示。
func (c *ConfigManager) loadLastSyncTimeMax() int64 {
	m := c.loadLastSyncTimeMap()
	var max int64
	for _, t := range m {
		if t > max {
			max = t
		}
	}
	return max
}

// saveLastSyncTime 保存指定后端的上次同步时间戳。
func (c *ConfigManager) saveLastSyncTime(provider string, t int64) {
	provider = strings.TrimSpace(provider)
	if provider == "" || t <= 0 {
		return
	}
	m := c.loadLastSyncTimeMap()
	m[provider] = t
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	atomicWriteFile(c.lastSyncFile, data, 0600)
}

// saveLastSyncTimes 批量写入多个后端的同一同步完成时间。
func (c *ConfigManager) saveLastSyncTimes(providers []string, t int64) {
	if t <= 0 || len(providers) == 0 {
		return
	}
	m := c.loadLastSyncTimeMap()
	changed := false
	for _, p := range providers {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		m[p] = t
		changed = true
	}
	if !changed {
		return
	}
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	atomicWriteFile(c.lastSyncFile, data, 0600)
}

// SyncTombstone 同步删除墓碑：明确记录「某 id 在 deleted_at 被删」，跨后端/设备传播删除意图。
type SyncTombstone struct {
	ID        string `json:"id"`
	DeletedAt int64  `json:"deleted_at"`
}

type syncTombstoneStore struct {
	Connections []SyncTombstone `json:"connections,omitempty"`
	Credentials []SyncTombstone `json:"credentials,omitempty"`
	// PrunedBefore：清理删除记录时推进。合并远端墓碑时丢弃 deleted_at < PrunedBefore 的项，
	// 防止「本机已清理」又被另一端旧墓碑并回来。
	PrunedBefore int64 `json:"pruned_before,omitempty"`
}

func (c *ConfigManager) loadTombstoneStore() syncTombstoneStore {
	var store syncTombstoneStore
	if c == nil || c.tombstoneFile == "" {
		return store
	}
	data, err := os.ReadFile(c.tombstoneFile)
	if err != nil {
		return store
	}
	if err := json.Unmarshal(data, &store); err != nil {
		slog.Error("墓碑存储反序列化失败", "error", err)
	}
	if store.Connections == nil {
		store.Connections = []SyncTombstone{}
	}
	if store.Credentials == nil {
		store.Credentials = []SyncTombstone{}
	}
	return store
}

func (c *ConfigManager) saveTombstoneStore(store syncTombstoneStore) error {
	if c == nil || c.tombstoneFile == "" {
		return nil
	}
	if store.Connections == nil {
		store.Connections = []SyncTombstone{}
	}
	if store.Credentials == nil {
		store.Credentials = []SyncTombstone{}
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(c.tombstoneFile, data, 0600)
}

func tombstoneMap(list []SyncTombstone) map[string]int64 {
	m := make(map[string]int64, len(list))
	for _, t := range list {
		id := strings.TrimSpace(t.ID)
		if id == "" || t.DeletedAt <= 0 {
			continue
		}
		if prev, ok := m[id]; !ok || t.DeletedAt > prev {
			m[id] = t.DeletedAt
		}
	}
	return m
}

func tombstonesFromMap(m map[string]int64) []SyncTombstone {
	if len(m) == 0 {
		return []SyncTombstone{}
	}
	out := make([]SyncTombstone, 0, len(m))
	for id, at := range m {
		out = append(out, SyncTombstone{ID: id, DeletedAt: at})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeletedAt != out[j].DeletedAt {
			return out[i].DeletedAt > out[j].DeletedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func mergeTombstoneMaps(a, b map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(a)+len(b))
	for id, at := range a {
		out[id] = at
	}
	for id, at := range b {
		if prev, ok := out[id]; !ok || at > prev {
			out[id] = at
		}
	}
	return out
}

// filterTombstonesNotBefore 丢弃 deleted_at < prunedBefore 的墓碑（清理水位线）。
func filterTombstonesNotBefore(list []SyncTombstone, prunedBefore int64) []SyncTombstone {
	if prunedBefore <= 0 || len(list) == 0 {
		return list
	}
	m := tombstoneMap(list)
	for id, at := range m {
		if at < prunedBefore {
			delete(m, id)
		}
	}
	return tombstonesFromMap(m)
}

// maxTombstoneDeletedAt 返回列表中最大 deleted_at。
func maxTombstoneDeletedAt(lists ...[]SyncTombstone) int64 {
	var max int64
	for _, list := range lists {
		for _, t := range list {
			if t.DeletedAt > max {
				max = t.DeletedAt
			}
		}
	}
	return max
}

func (c *ConfigManager) addConnectionTombstonesLocked(ids []string, deletedAt int64) {
	if deletedAt <= 0 || len(ids) == 0 {
		return
	}
	store := c.loadTombstoneStore()
	// 新删除必须越过清理水位线，否则会被 filter 掉
	if deletedAt <= store.PrunedBefore {
		deletedAt = store.PrunedBefore + 1
	}
	m := tombstoneMap(store.Connections)
	changed := false
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if prev, ok := m[id]; !ok || deletedAt > prev {
			m[id] = deletedAt
			changed = true
		}
	}
	if !changed {
		return
	}
	store.Connections = tombstonesFromMap(m)
	if err := c.saveTombstoneStore(store); err != nil {
		log.Printf("[tombstone] save connections failed: %v", err)
	}
}

func (c *ConfigManager) addCredentialTombstonesLocked(ids []string, deletedAt int64) {
	if deletedAt <= 0 || len(ids) == 0 {
		return
	}
	store := c.loadTombstoneStore()
	if deletedAt <= store.PrunedBefore {
		deletedAt = store.PrunedBefore + 1
	}
	m := tombstoneMap(store.Credentials)
	changed := false
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if prev, ok := m[id]; !ok || deletedAt > prev {
			m[id] = deletedAt
			changed = true
		}
	}
	if !changed {
		return
	}
	store.Credentials = tombstonesFromMap(m)
	if err := c.saveTombstoneStore(store); err != nil {
		log.Printf("[tombstone] save credentials failed: %v", err)
	}
}

func (c *ConfigManager) clearConnectionTombstonesLocked(ids []string) {
	if len(ids) == 0 {
		return
	}
	store := c.loadTombstoneStore()
	m := tombstoneMap(store.Connections)
	changed := false
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if _, ok := m[id]; ok {
			delete(m, id)
			changed = true
		}
	}
	if !changed {
		return
	}
	store.Connections = tombstonesFromMap(m)
	if err := c.saveTombstoneStore(store); err != nil {
		slog.Error("墓碑连接记录保存失败", "error", err)
	}
}

func (c *ConfigManager) clearCredentialTombstonesLocked(ids []string) {
	if len(ids) == 0 {
		return
	}
	store := c.loadTombstoneStore()
	m := tombstoneMap(store.Credentials)
	changed := false
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if _, ok := m[id]; ok {
			delete(m, id)
			changed = true
		}
	}
	if !changed {
		return
	}
	store.Credentials = tombstonesFromMap(m)
	if err := c.saveTombstoneStore(store); err != nil {
		slog.Error("墓碑凭证记录保存失败", "error", err)
	}
}

// SyncTombstoneStats 同步删除记录条数（供设置页展示）。
type SyncTombstoneStats struct {
	Connections int `json:"connections"`
	Credentials int `json:"credentials"`
}

// SyncTombstonePruneResult 按天数清理墓碑的结果。
type SyncTombstonePruneResult struct {
	RemovedConnections   int `json:"removedConnections"`
	RemovedCredentials   int `json:"removedCredentials"`
	RemainingConnections int `json:"remainingConnections"`
	RemainingCredentials int `json:"remainingCredentials"`
	Uploaded             int `json:"uploaded"`
}

// GetSyncTombstoneStats 返回本地墓碑条数。
func (c *ConfigManager) GetSyncTombstoneStats() SyncTombstoneStats {
	store := c.loadTombstoneStore()
	return SyncTombstoneStats{
		Connections: len(tombstoneMap(store.Connections)),
		Credentials: len(tombstoneMap(store.Credentials)),
	}
}

// pruneTombstonesOlderThan 去掉 deleted_at 早于 cutoff 的墓碑；days<=0 表示全部清理。
func pruneTombstonesOlderThan(list []SyncTombstone, cutoff int64, clearAll bool) (kept []SyncTombstone, removed int) {
	if len(list) == 0 {
		return []SyncTombstone{}, 0
	}
	kept = make([]SyncTombstone, 0, len(list))
	for _, t := range list {
		id := strings.TrimSpace(t.ID)
		if id == "" || t.DeletedAt <= 0 {
			continue
		}
		if clearAll || t.DeletedAt < cutoff {
			removed++
			continue
		}
		kept = append(kept, SyncTombstone{ID: id, DeletedAt: t.DeletedAt})
	}
	return tombstonesFromMap(tombstoneMap(kept)), removed
}

// SetConnectionGroup 仅更新服务器的分组字段，不影响密码等敏感数据
func (c *ConfigManager) SetConnectionGroup(id string, group string) error {
	return c.updateConnectionField(id, func(conn *Connection) { conn.Group = group })
}

// BatchSetConnectionGroup 批量更新服务器的分组字段，仅触发一次写入和云同步
func (c *ConfigManager) BatchSetConnectionGroup(ids []string, group string) error {
	if len(ids) == 0 {
		return nil
	}
	idMap := make(map[string]bool, len(ids))
	for _, id := range ids {
		idMap[id] = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	conns := c.getConnectionsLocked()
	modified := false
	now := time.Now().UnixMilli()
	for i, conn := range conns {
		if idMap[conn.ID] {
			conns[i].Group = group
			conns[i].LastModified = now
			modified = true
		}
	}
	if !modified {
		return nil
	}
	if err := c.saveConnectionsFile(conns); err != nil {
		return err
	}
	c.bumpSnapshotTime()
	c.connCacheDirty = true
	go c.AutoSync()
	return nil
}

// RenameConnectionGroup 将 oldName 分组下所有服务器的 group 改为 newName。
// 与已有分组重名时拒绝；未分组（空名）不可作为 oldName；同名 no-op。
func (c *ConfigManager) RenameConnectionGroup(oldName, newName string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" {
		return fmt.Errorf("不能重命名未分组")
	}
	if newName == "" {
		return fmt.Errorf("分组名称不能为空")
	}
	if oldName == newName {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	conns := c.getConnectionsLocked()

	for _, conn := range conns {
		if conn.Group == newName {
			return fmt.Errorf("分组名称已存在")
		}
	}

	now := time.Now().UnixMilli()
	modified := false
	for i, conn := range conns {
		if conn.Group == oldName {
			conns[i].Group = newName
			conns[i].LastModified = now
			modified = true
		}
	}
	if !modified {
		return nil
	}
	if err := c.saveConnectionsFile(conns); err != nil {
		return err
	}
	c.bumpSnapshotTime()
	c.connCacheDirty = true
	go c.AutoSync()
	return nil
}

// SetConnectionOS 更新服务器的操作系统字段；即使值未变化，也会触发一次同步。
func (c *ConfigManager) SetConnectionOS(id string, osValue string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	conns := c.getConnectionsLocked()
	for i, conn := range conns {
		if conn.ID == id {
			if conn.Os != osValue {
				conns[i].Os = osValue
				conns[i].LastModified = time.Now().UnixMilli()
				if err := c.saveConnectionsFile(conns); err != nil {
					return err
				}
				c.bumpSnapshotTime()
				c.connCacheDirty = true
			}
			go c.AutoSync()
			return nil
		}
	}
	return fmt.Errorf("connection not found: %s", id)
}

// updateConnectionField 通用：按 ID 查找并更新单个字段
func (c *ConfigManager) updateConnectionField(id string, apply func(*Connection)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	conns := c.getConnectionsLocked()
	for i, conn := range conns {
		if conn.ID == id {
			apply(&conns[i])
			conns[i].LastModified = time.Now().UnixMilli()
			if err := c.saveConnectionsFile(conns); err != nil {
				return err
			}
			c.bumpSnapshotTime()
			c.connCacheDirty = true
			go c.AutoSync()
			return nil
		}
	}
	return fmt.Errorf("connection not found: %s", id)
}

func (c *ConfigManager) DeleteConnection(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	conns := c.getConnectionsLocked()
	filtered := make([]Connection, 0, len(conns))
	found := false
	for _, conn := range conns {
		if conn.ID != id {
			filtered = append(filtered, conn)
			continue
		}
		found = true
	}
	if !found {
		return false
	}
	if err := c.saveConnectionsFile(filtered); err != nil {
		log.Printf("[DeleteConnection] failed to save connections: %v", err)
	}
	c.addConnectionTombstonesLocked([]string{id}, time.Now().UnixMilli())
	if err := c.deleteWorkspaceSessionStateLocked(id); err != nil {
		log.Printf("[DeleteConnection] failed to delete workspace session state: %v", err)
	}
	if err := c.deletePortForwardsLocked(id); err != nil {
		log.Printf("[DeleteConnection] failed to delete port forwards: %v", err)
	}
	c.bumpSnapshotTime()
	c.connCacheDirty = true // 标记缓存需要刷新

	// 清理该连接的延迟检测 Banner 状态，避免 pingStates 无限增长。
	if c.onDeleteConnection != nil {
		c.onDeleteConnection(id)
	}

	// 清理该服务器的历史文件
	histPath := filepath.Join(c.historyDir, id+".json")
	os.Remove(histPath)

	go c.AutoSync()
	return true
}

// BatchDeleteConnections 批量删除多个连接，清理历史文件，触发同步
func (c *ConfigManager) BatchDeleteConnections(ids []string) {
	if len(ids) == 0 {
		return
	}
	deleteSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		deleteSet[id] = true
	}
	c.mu.Lock()
	conns := c.getConnectionsLocked()
	filtered := make([]Connection, 0, len(conns))
	for _, conn := range conns {
		if !deleteSet[conn.ID] {
			filtered = append(filtered, conn)
		}
	}
	if len(filtered) == len(conns) {
		c.mu.Unlock()
		return
	}
	if err := c.saveConnectionsFile(filtered); err != nil {
		log.Printf("[BatchDeleteConnections] failed to save connections: %v", err)
	}
	removed := make([]string, 0, len(ids))
	for _, id := range ids {
		for _, conn := range conns {
			if conn.ID == id {
				removed = append(removed, id)
				break
			}
		}
	}
	c.addConnectionTombstonesLocked(removed, time.Now().UnixMilli())
	states := c.getWorkspaceSessionStatesLocked()
	if len(states) > 0 {
		changed := false
		for _, id := range ids {
			if _, ok := states[id]; ok {
				delete(states, id)
				changed = true
			}
		}
		if changed {
			if err := c.saveWorkspaceSessionStatesLocked(states); err != nil {
				log.Printf("[BatchDeleteConnections] failed to delete workspace session states: %v", err)
			}
		}
	}
	pfStates := c.getPortForwardsLocked()
	if len(pfStates) > 0 {
		pfChanged := false
		for _, id := range ids {
			if _, ok := pfStates[id]; ok {
				delete(pfStates, id)
				pfChanged = true
			}
		}
		if pfChanged {
			if err := c.savePortForwardsLocked(pfStates); err != nil {
				log.Printf("[BatchDeleteConnections] failed to delete port forwards: %v", err)
			}
		}
	}
	c.bumpSnapshotTime()
	c.connCacheDirty = true
	c.mu.Unlock()

	for _, id := range ids {
		os.Remove(filepath.Join(c.historyDir, id+".json"))
		if c.onDeleteConnection != nil {
			c.onDeleteConnection(id) // 清理延迟检测 Banner 状态，避免 pingStates 无限增长。
		}
	}
	go c.AutoSync()
}

// ── Credential 凭据管理 ──────────────────────────────────────────

func (c *ConfigManager) GetCredentials() []Credential {
	c.mu.RLock()
	if !c.credCacheDirty && c.credCache != nil {
		result := make([]Credential, len(c.credCache))
		copy(result, c.credCache)
		c.mu.RUnlock()
		return result
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.credCacheDirty && c.credCache != nil {
		result := make([]Credential, len(c.credCache))
		copy(result, c.credCache)
		return result
	}
	creds := c.getCredentialsLocked()
	c.credCache = creds
	c.credCacheDirty = false
	result := make([]Credential, len(creds))
	copy(result, creds)
	return result
}

// getCredentialsLocked 读取凭据列表，调用方需持有 c.mu
func (c *ConfigManager) getCredentialsLocked() []Credential {
	data, err := os.ReadFile(c.credFile)
	if err != nil {
		return []Credential{}
	}
	var creds []Credential
	if err := json.Unmarshal(data, &creds); err != nil {
		log.Printf("[getCredentialsLocked] json.Unmarshal failed: %v", err)
		return []Credential{}
	}
	for i := range creds {
		creds[i].Password = c.decrypt(creds[i].Password)
		creds[i].Passphrase = c.decrypt(creds[i].Passphrase)
		creds[i].PrivateKey = c.decryptOrPassthrough(creds[i].PrivateKey)
	}
	return creds
}

func (c *ConfigManager) GetCredentialByID(id string) (Credential, bool) {
	creds := c.GetCredentials()
	for _, cr := range creds {
		if cr.ID == id {
			return cr, true
		}
	}
	return Credential{}, false
}

func (c *ConfigManager) GetCredentialsMasked() []Credential {
	creds := c.GetCredentials()
	for i := range creds {
		if creds[i].Password != "" {
			creds[i].Password = "****"
		}
		if creds[i].PrivateKey != "" {
			creds[i].PrivateKey = "[key configured]"
		}
		if creds[i].Passphrase != "" {
			creds[i].Passphrase = "****"
		}
	}
	return creds
}

func (c *ConfigManager) SaveCredential(cred Credential) Credential {
	return c.saveCredential(cred, false)
}

func (c *ConfigManager) SaveCredentialNoSync(cred Credential) Credential {
	return c.saveCredential(cred, true)
}

func (c *ConfigManager) saveCredential(cred Credential, noSync bool) Credential {
	c.mu.Lock()
	defer c.mu.Unlock()
	creds := c.getCredentialsLocked()

	if cred.ID == "" {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			cred.ID = strconv.FormatInt(time.Now().UnixNano(), 10)
		} else {
			cred.ID = fmt.Sprintf("%x", b)
		}
		creds = append(creds, cred)
	} else {
		found := false
		for i, existing := range creds {
			if existing.ID == cred.ID {
				if cred.Password == "" && existing.Password != "" {
					cred.Password = existing.Password
				}
				if cred.PrivateKey == "" && existing.PrivateKey != "" {
					cred.PrivateKey = existing.PrivateKey
				}
				if cred.Passphrase == "" && existing.Passphrase != "" {
					cred.Passphrase = existing.Passphrase
				}
				creds[i] = cred
				found = true
				break
			}
		}
		if !found {
			creds = append(creds, cred)
		}
	}

	cred.LastModified = time.Now().UnixMilli()
	for i, s := range creds {
		if s.ID == cred.ID {
			creds[i].LastModified = cred.LastModified
			break
		}
	}

	if err := c.saveCredentialsFile(creds); err != nil {
		log.Printf("[SaveCredential] failed to save credentials: %v", err)
	}
	c.clearCredentialTombstonesLocked([]string{cred.ID})
	c.credCacheDirty = true
	c.bumpSnapshotTime()
	if !noSync {
		go c.AutoSync()
	}
	return cred
}

func (c *ConfigManager) DeleteCredential(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	conns := c.getConnectionsLocked()
	// 检查是否有连接引用此凭据
	for _, conn := range conns {
		if conn.CredentialID == id {
			refName := conn.Name
			if refName == "" {
				refName = conn.Host
			}
			return fmt.Errorf("该凭据仍被服务器「%s」引用，无法删除", refName)
		}
	}

	creds := c.getCredentialsLocked()
	filtered := make([]Credential, 0, len(creds))
	for _, cr := range creds {
		if cr.ID != id {
			filtered = append(filtered, cr)
		}
	}
	if err := c.saveCredentialsFile(filtered); err != nil {
		log.Printf("[DeleteCredential] failed to save credentials: %v", err)
	}
	c.addCredentialTombstonesLocked([]string{id}, time.Now().UnixMilli())
	c.credCacheDirty = true
	c.bumpSnapshotTime()
	go c.AutoSync()
	return nil
}

// saveCredentialsFile 加密并原子写入凭据列表，调用方需持有 c.mu
func (c *ConfigManager) saveCredentialsFile(creds []Credential) error {
	toSave := make([]Credential, len(creds))
	copy(toSave, creds)
	for i := range toSave {
		encPass, err := c.encrypt(toSave[i].Password)
		if err != nil {
			return fmt.Errorf("encrypt password: %w", err)
		}
		encPhrase, err := c.encrypt(toSave[i].Passphrase)
		if err != nil {
			return fmt.Errorf("encrypt passphrase: %w", err)
		}
		encKey, err := c.encrypt(toSave[i].PrivateKey)
		if err != nil {
			return fmt.Errorf("encrypt privateKey: %w", err)
		}
		toSave[i].Password = encPass
		toSave[i].Passphrase = encPhrase
		toSave[i].PrivateKey = encKey
	}
	data, err := json.MarshalIndent(toSave, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	return atomicWriteFile(c.credFile, data, 0600)
}

// ImportConnections 合并导入节点：按 host+port+username 判重，本地已存在则跳过，仅新增。
// incoming 为待导入的明文节点列表，incomingCreds 为待导入的明文凭据列表。
// 走 saveConnectionsFile/saveCredentialsFile 自动加密 + 原子写，并触发云同步。
func (c *ConfigManager) ImportConnections(incoming []Connection, incomingCreds []Credential, incomingProxyNodes ...[]aitypes.AIProxyNode) (ImportResult, error) {
	c.mu.Lock()
	localConns := c.getConnectionsLocked()
	localCreds := c.getCredentialsLocked()
	localProxyNodes := c.GetAIProxyNodes()

	toAdd, result := mergeImport(localConns, incoming)
	incomingCreds = filterImportCredentialsForConnections(incomingCreds, toAdd)
	var importProxyNodes []aitypes.AIProxyNode
	if len(incomingProxyNodes) > 0 {
		importProxyNodes = filterImportProxyNodesForConnections(incomingProxyNodes[0], toAdd)
	}
	if len(toAdd) == 0 && len(incomingCreds) == 0 && len(importProxyNodes) == 0 {
		c.mu.Unlock()
		// 无需新增任何数据，直接返回统计（仍可能全是重复）
		return result, nil
	}

	toAddCreds, credIDMap := mergeImportCredentials(localCreds, incomingCreds)
	var proxyIDMap map[string]string
	var toAddProxyNodes []aitypes.AIProxyNode
	if len(importProxyNodes) > 0 {
		toAddProxyNodes, proxyIDMap = mergeImportProxyNodes(localProxyNodes, importProxyNodes)
	}
	applyImportReferenceMappings(toAdd, credIDMap, proxyIDMap)

	// 合并节点
	if len(toAdd) > 0 {
		mergedConns := make([]Connection, 0, len(localConns)+len(toAdd))
		mergedConns = append(mergedConns, localConns...)
		mergedConns = append(mergedConns, toAdd...)
		if err := c.saveConnectionsFile(mergedConns); err != nil {
			c.mu.Unlock()
			return result, fmt.Errorf("保存导入的节点失败: %w", err)
		}
		c.connCacheDirty = true
	}

	// 合并凭据（按 ID 去重）
	if len(toAddCreds) > 0 {
		mergedCreds := make([]Credential, 0, len(localCreds)+len(toAddCreds))
		mergedCreds = append(mergedCreds, localCreds...)
		mergedCreds = append(mergedCreds, toAddCreds...)
		if err := c.saveCredentialsFile(mergedCreds); err != nil {
			c.mu.Unlock()
			return result, fmt.Errorf("保存导入的凭据失败: %w", err)
		}
		c.credCacheDirty = true
	}
	c.mu.Unlock()

	if len(toAddProxyNodes) > 0 {
		mergedProxyNodes := make([]aitypes.AIProxyNode, 0, len(localProxyNodes)+len(toAddProxyNodes))
		mergedProxyNodes = append(mergedProxyNodes, localProxyNodes...)
		mergedProxyNodes = append(mergedProxyNodes, toAddProxyNodes...)
		if err := c.SaveAIProxyNodes(mergedProxyNodes); err != nil {
			return result, fmt.Errorf("保存导入的代理节点失败: %w", err)
		}
	}

	c.bumpSnapshotTime()
	go c.AutoSync()
	return result, nil
}

// ResolveConnectionAuth 如果 Connection 引用了 Credential，返回用凭据填充认证字段的副本
func (c *ConfigManager) ResolveConnectionAuth(conn Connection) Connection {
	if conn.CredentialID == "" {
		return conn
	}
	cred, ok := c.GetCredentialByID(conn.CredentialID)
	if !ok {
		log.Printf("[ResolveConnectionAuth] credential %s not found for connection %s, using inline fields", conn.CredentialID, conn.ID)
		return conn
	}
	conn.AuthMethod = cred.AuthMethod
	conn.Username = cred.Username
	conn.Password = cred.Password
	conn.PrivateKey = cred.PrivateKey
	conn.Passphrase = cred.Passphrase
	return conn
}

// WEBDAV
type WebdavConfig struct {
	Url        string `json:"url"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	RemotePath string `json:"remotePath"`
	MaxBackups int    `json:"maxBackups"`
}

func (c *ConfigManager) GetWebdavConfig() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getWebdavConfigLocked()
}

// getWebdavConfigLocked 读取 WebDAV 配置，调用方需持有 c.mu
func (c *ConfigManager) getWebdavConfigLocked() map[string]string {
	data, err := os.ReadFile(c.davFile)
	if err != nil {
		return nil
	}
	var conf WebdavConfig
	if err := json.Unmarshal(data, &conf); err != nil {
		log.Printf("[getWebdavConfigLocked] json.Unmarshal failed: %v", err)
		return nil
	}
	return map[string]string{
		"url":        conf.Url,
		"username":   c.decrypt(conf.Username),
		"password":   c.decrypt(conf.Password),
		"remotePath": conf.RemotePath,
		"maxBackups": fmt.Sprintf("%d", conf.MaxBackups),
	}
}

func (c *ConfigManager) SaveWebdavConfig(config map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	pass := config["password"]
	if pass == "" {
		existing := c.getWebdavConfigLocked()
		if existing != nil && existing["password"] != "" {
			pass = existing["password"]
		}
	}

	maxBackups := parseIntOrDefault(config["maxBackups"], 0)

	encUser, err := c.encrypt(config["username"])
	if err != nil {
		return fmt.Errorf("encrypt username: %w", err)
	}
	encPass, err := c.encrypt(pass)
	if err != nil {
		return fmt.Errorf("encrypt password: %w", err)
	}

	conf := WebdavConfig{
		Url:        config["url"],
		Username:   encUser,
		Password:   encPass,
		RemotePath: config["remotePath"],
		MaxBackups: maxBackups,
	}
	if conf.RemotePath == "" {
		conf.RemotePath = "/LumeTerm/"
	}
	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal webdav config: %w", err)
	}
	return atomicWriteFile(c.davFile, data, 0600)
}

func (c *ConfigManager) TestWebdavConnection(url, username, password string) error {
	client := gowebdav.NewClient(url, username, password)
	_, err := client.ReadDir("/")
	return err
}

// ─── WebDAV RemoteStorage 实现 ─────────────────────────────

type webdavStorage struct {
	client     *gowebdav.Client
	remotePath string
}

func (s *webdavStorage) ListFiles() ([]RemoteFile, error) {
	files, err := s.client.ReadDir(s.remotePath)
	if err != nil {
		return nil, err
	}
	result := make([]RemoteFile, 0, len(files))
	for _, f := range files {
		result = append(result, RemoteFile{
			Name:    f.Name(),
			ModTime: f.ModTime(),
			IsDir:   f.IsDir(),
			Size:    f.Size(),
		})
	}
	return result, nil
}

// EnsureRemoteDir 重建 WebDAV 同步根目录（目录被删/404 时由前端「重新创建并重试」调用）。
func (s *webdavStorage) EnsureRemoteDir() error {
	path := strings.TrimSpace(s.remotePath)
	if path == "" || path == "/" || path == "." {
		return nil
	}
	if _, err := s.client.ReadDir(path); err == nil {
		return nil
	}
	if err := s.client.MkdirAll(path, 0755); err != nil {
		// 并发创建时可能已存在，再读一次确认
		if _, rerr := s.client.ReadDir(path); rerr == nil {
			return nil
		}
		return fmt.Errorf("创建远程目录 %s 失败: %w", path, err)
	}
	return nil
}

func (s *webdavStorage) ReadFile(name string) ([]byte, error) {
	path := filepath.ToSlash(filepath.Join(s.remotePath, name))
	return s.client.Read(path)
}

func (s *webdavStorage) WriteFile(name string, data []byte) error {
	path := filepath.ToSlash(filepath.Join(s.remotePath, name))
	// ensure dir exists
	if _, err := s.client.ReadDir(s.remotePath); err != nil {
		if mkErr := s.client.MkdirAll(s.remotePath, 0755); mkErr != nil {
			return fmt.Errorf("创建远程目录 %s 失败: %w", s.remotePath, mkErr)
		}
	}
	return s.client.Write(path, data, 0644)
}

func (s *webdavStorage) DeleteFile(name string) error {
	path := filepath.ToSlash(filepath.Join(s.remotePath, name))
	return s.client.Remove(path)
}

func (c *ConfigManager) newWebdavStorage() (RemoteStorage, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	conf := c.getWebdavConfigLocked()
	if conf == nil {
		return nil, 0, fmt.Errorf("WebDAV not configured")
	}
	client := gowebdav.NewClient(conf["url"], conf["username"], conf["password"])
	maxBackups := parseIntOrDefault(conf["maxBackups"], 0)
	return &webdavStorage{
		client:     client,
		remotePath: conf["remotePath"],
	}, maxBackups, nil
}

// BackupToWebdav 备份到 WebDAV
func (c *ConfigManager) BackupToWebdav() (map[string]interface{}, error) {
	return c.backupTo("webdav", c.newWebdavStorage)
}

// ListWebdavBackups 列出 WebDAV 备份
func (c *ConfigManager) ListWebdavBackups() ([]map[string]interface{}, error) {
	return c.listBackupsFrom(c.newWebdavStorage)
}

// SyncFromWebdav 手动合并同步
func (c *ConfigManager) SyncFromWebdav() (map[string]interface{}, error) {
	return c.syncFrom("webdav", c.newWebdavStorage)
}

func (c *ConfigManager) RestoreFromWebdavFile(filename string) (map[string]interface{}, error) {
	return c.restoreFrom("webdav", c.newWebdavStorage, filename, c.GetRecoveryPassword())
}

// RestoreFromWebdavFileWithPassword 用用户输入的密码恢复（恢复失败时的兜底入口）。
func (c *ConfigManager) RestoreFromWebdavFileWithPassword(filename string, password string) (map[string]interface{}, error) {
	return c.restoreFrom("webdav", c.newWebdavStorage, filename, password)
}

// ─── 同步模式配置 ─────────────────────────────────────────

// GetSyncMode 获取自动同步模式：webdav / r2 / ftp / sftp / all
func (c *ConfigManager) GetSyncMode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := os.ReadFile(c.syncModeFile)
	if err != nil {
		return "webdav"
	}
	var mode string
	if json.Unmarshal(data, &mode) != nil || mode == "" {
		return "webdav"
	}
	return mode
}

// SetSyncMode 设置自动同步模式
func (c *ConfigManager) SetSyncMode(mode string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, _ := json.Marshal(mode)
	return atomicWriteFile(c.syncModeFile, data, 0600)
}

func (c *ConfigManager) GetAutoSyncEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := os.ReadFile(c.autoSyncEnabledFile)
	if err != nil {
		return false
	}
	var enabled bool
	if json.Unmarshal(data, &enabled) != nil {
		return false
	}
	return enabled
}

func (c *ConfigManager) SetAutoSyncEnabled(enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, _ := json.Marshal(enabled)
	return atomicWriteFile(c.autoSyncEnabledFile, data, 0600)
}

// ─── 恢复密码 ─────────────────────────────────────────────

// GetRecoveryPassword 获取恢复密码（明文，供前端回显"已设置"状态）；未设置返回空串。
func (c *ConfigManager) GetRecoveryPassword() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := os.ReadFile(c.recoveryPasswordFile)
	if err != nil {
		return ""
	}
	return c.decrypt(string(data))
}

func normalizeRecoveryPassword(password string) string {
	if strings.TrimSpace(password) == "" {
		return ""
	}
	return password
}

// SetRecoveryPassword 设置恢复密码；空串或纯空白则清除。
// 密码用主密钥加密后原子写入。
func (c *ConfigManager) SetRecoveryPassword(password string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	password = normalizeRecoveryPassword(password)
	if password == "" {
		if err := os.Remove(c.recoveryPasswordFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("清除恢复密码失败: %w", err)
		}
		return nil
	}
	enc, err := c.encrypt(password)
	if err != nil {
		return fmt.Errorf("加密恢复密码失败: %w", err)
	}
	return atomicWriteFile(c.recoveryPasswordFile, []byte(enc), 0600)
}

func sanitizeChmodDialogMode(mode string) string {
	filtered := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '7' {
			return r
		}
		return -1
	}, strings.TrimSpace(mode))
	if len(filtered) == 4 && filtered[0] == '0' {
		filtered = filtered[1:]
	}
	if len(filtered) != 3 {
		return ""
	}
	return filtered
}

const (
	defaultTransferMaxPacketKiB       = 128
	defaultTransferMaxRequestsPerFile = 16
	minTransferMaxPacketKiB           = 32
	maxTransferMaxPacketKiB           = 512
	minTransferMaxRequestsPerFile     = 1
	maxTransferMaxRequestsPerFile     = 1024
)

func clampIntSetting(value int, minValue int, maxValue int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func normalizeTransferTuningSettings(settings TransferTuningSettings) TransferTuningSettings {
	normalized := settings
	normalized.MaxPacketKiB = clampIntSetting(settings.MaxPacketKiB, minTransferMaxPacketKiB, maxTransferMaxPacketKiB, defaultTransferMaxPacketKiB)
	normalized.MaxRequestsPerFile = clampIntSetting(settings.MaxRequestsPerFile, minTransferMaxRequestsPerFile, maxTransferMaxRequestsPerFile, defaultTransferMaxRequestsPerFile)
	if !settings.Configured {
		normalized.ConcurrentWrites = true
		normalized.ApplyToSharedClient = true
	}
	return normalized
}

func normalizeFileManagerSmartUncompressConflictStrategy(value string) string {
	switch strings.TrimSpace(value) {
	case "overwrite":
		return "overwrite"
	case "prompt":
		return "prompt"
	default:
		return "auto_rename"
	}
}

// NormalizeFileManagerSmartUncompressConflictStrategy 导出包装器
func NormalizeFileManagerSmartUncompressConflictStrategy(value string) string {
	return normalizeFileManagerSmartUncompressConflictStrategy(value)
}

func (c *ConfigManager) getFileManagerSettingsLocked() FileManagerSettings {
	data, err := os.ReadFile(c.fileManagerSettingsFile)
	if err != nil {
		return FileManagerSettings{}
	}
	var settings FileManagerSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		log.Printf("[getFileManagerSettingsLocked] json.Unmarshal failed: %v", err)
		return FileManagerSettings{}
	}
	settings.ChmodDialog.Mode = sanitizeChmodDialogMode(settings.ChmodDialog.Mode)
	settings.SmartUncompressConflictStrategy = normalizeFileManagerSmartUncompressConflictStrategy(settings.SmartUncompressConflictStrategy)
	settings.MaxEditSizeMB = normalizeFileManagerMaxEditSizeMB(settings.MaxEditSizeMB)
	settings.TransferTuning = normalizeTransferTuningSettings(settings.TransferTuning)
	return settings
}

func (c *ConfigManager) saveFileManagerSettingsLocked(settings FileManagerSettings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal file manager settings: %w", err)
	}
	return atomicWriteFile(c.fileManagerSettingsFile, data, 0600)
}

func (c *ConfigManager) getAppSettingsLocked() AppSettings {
	data, err := os.ReadFile(c.appSettingsFile)
	if err != nil {
		return AppSettings{}
	}
	var settings AppSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		log.Printf("[getAppSettingsLocked] json.Unmarshal failed: %v", err)
		return AppSettings{}
	}
	return settings
}

func (c *ConfigManager) saveAppSettingsLocked(settings AppSettings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal app settings: %w", err)
	}
	return atomicWriteFile(c.appSettingsFile, data, 0600)
}

func (c *ConfigManager) GetWebviewGpuDisabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getAppSettingsLocked().WebviewGpuDisabled
}

func (c *ConfigManager) SetWebviewGpuDisabled(enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	settings := c.getAppSettingsLocked()
	settings.WebviewGpuDisabled = enabled
	return c.saveAppSettingsLocked(settings)
}

func (c *ConfigManager) GetRuntimeEnvironmentSettings() runtimeenv.Settings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return runtimeenv.NormalizeSettings(c.getAppSettingsLocked().RuntimeEnvironment)
}

func (c *ConfigManager) SaveRuntimeEnvironmentSettings(settings runtimeenv.Settings) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	appSettings := c.getAppSettingsLocked()
	appSettings.RuntimeEnvironment = runtimeenv.NormalizeSettings(settings)
	return c.saveAppSettingsLocked(appSettings)
}

func (c *ConfigManager) GetTasksDir() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return strings.TrimSpace(c.getAppSettingsLocked().TasksDir)
}

func (c *ConfigManager) SetTasksDir(dir string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	appSettings := c.getAppSettingsLocked()
	appSettings.TasksDir = strings.TrimSpace(dir)
	return c.saveAppSettingsLocked(appSettings)
}

// MigrateAITasksDir 将 AI 对话数据从当前目录迁移到目标目录。
// 成功后更新 TasksDir 设置并删除旧目录数据。
func (c *ConfigManager) MigrateAITasksDir(targetDir string) error {
	targetDir = strings.TrimSpace(targetDir)
	if targetDir == "" {
		return fmt.Errorf("目标目录不能为空")
	}
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("无法解析目标路径: %w", err)
	}
	// 获取当前 tasks 目录（自定义路径或默认 configDir/tasks）
	currentTasksDir := c.GetTasksDir()
	var sourceDir string
	if currentTasksDir != "" {
		sourceDir = currentTasksDir
	} else {
		sourceDir = filepath.Join(c.configDir, "tasks")
	}
	if absTarget == sourceDir {
		return fmt.Errorf("目标目录与当前目录相同")
	}
	// 确保目标目录存在
	if err := os.MkdirAll(absTarget, 0755); err != nil {
		return fmt.Errorf("无法创建目标目录: %w", err)
	}
	// 目标目录必须为空或仅含隐藏文件, 避免覆盖已有数据
	entries, err := os.ReadDir(absTarget)
	if err != nil {
		return fmt.Errorf("无法读取目标目录: %w", err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			return fmt.Errorf("目标目录不为空, 请选择一个空目录")
		}
	}
	// 源目录不存在或为空时直接切换路径
	sourceEntries, err := os.ReadDir(sourceDir)
	if err != nil || len(sourceEntries) == 0 {
		return c.SetTasksDir(absTarget)
	}
	// 复制全部内容
	if err := copyDir(sourceDir, absTarget); err != nil {
		return fmt.Errorf("迁移失败: %w", err)
	}
	// 验证: 对比新旧目录条目数一致
	newEntries, err := os.ReadDir(absTarget)
	if err != nil {
		return fmt.Errorf("迁移后验证失败: %w", err)
	}
	if len(newEntries) != len(sourceEntries) {
		return fmt.Errorf("迁移后验证失败: 文件数量不一致 (%d → %d)", len(sourceEntries), len(newEntries))
	}
	// 更新设置
	if err := c.SetTasksDir(absTarget); err != nil {
		return err
	}
	// 删除旧目录数据, 失败仅记录不阻断（数据已在新目录）
	if err := os.RemoveAll(sourceDir); err != nil {
		log.Printf("[MigrateAITasksDir] 清理旧目录失败: %v (数据已迁移到 %s)", err, absTarget)
	}
	return nil
}

func copyDir(source, target string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(target, rel)
		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}
		return copyFile(path, targetPath, info.Mode())
	})
}

func copyFile(source, target string, mode os.FileMode) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func (c *ConfigManager) GetChmodDialogSettings() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	settings := c.getFileManagerSettingsLocked()
	return map[string]interface{}{
		"mode":                  settings.ChmodDialog.Mode,
		"includeSubdirectories": settings.ChmodDialog.IncludeSubdirectories,
		"autoApplyLastSettings": settings.ChmodDialog.AutoApplyLastSettings,
	}
}

func (c *ConfigManager) GetFileManagerSettings() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	settings := c.getFileManagerSettingsLocked()
	return map[string]interface{}{
		"doubleClickUncompressArchive":    settings.DoubleClickUncompressArchive,
		"smartUncompressConflictStrategy": settings.SmartUncompressConflictStrategy,
		"autoRefreshDisabled":             settings.AutoRefreshDisabled,
		"maxEditSizeMB":                   settings.MaxEditSizeMB,
		"transferMaxPacketKiB":            settings.TransferTuning.MaxPacketKiB,
		"transferMaxRequestsPerFile":      settings.TransferTuning.MaxRequestsPerFile,
		"transferConcurrentWrites":        settings.TransferTuning.ConcurrentWrites,
		"transferApplyToSharedClient":     settings.TransferTuning.ApplyToSharedClient,
	}
}

func (c *ConfigManager) GetTransferTuningSettings() TransferTuningSettings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getFileManagerSettingsLocked().TransferTuning
}

func (c *ConfigManager) SaveTransferTuningSettings(maxPacketKiB int, maxRequestsPerFile int, concurrentWrites bool, applyToSharedClient bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	settings := c.getFileManagerSettingsLocked()
	settings.TransferTuning = normalizeTransferTuningSettings(TransferTuningSettings{
		MaxPacketKiB:        maxPacketKiB,
		MaxRequestsPerFile:  maxRequestsPerFile,
		ConcurrentWrites:    concurrentWrites,
		ApplyToSharedClient: applyToSharedClient,
		Configured:          true,
	})
	err := c.saveFileManagerSettingsLocked(settings)
	if err == nil {
		c.bumpSnapshotTime()
		go c.AutoSync()
	}
	return err
}

func (c *ConfigManager) SaveChmodDialogSettings(mode string, includeSubdirectories bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	sanitizedMode := sanitizeChmodDialogMode(mode)
	if sanitizedMode == "" {
		sanitizedMode = "644"
	}
	settings := c.getFileManagerSettingsLocked()
	settings.ChmodDialog.Mode = sanitizedMode
	settings.ChmodDialog.IncludeSubdirectories = includeSubdirectories
	settings.ChmodDialog.LastModified = time.Now().UnixMilli()
	err := c.saveFileManagerSettingsLocked(settings)
	if err == nil {
		c.bumpSnapshotTime()
		go c.AutoSync()
	}
	return err
}

func (c *ConfigManager) SetChmodAutoApplyLastSettings(enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	settings := c.getFileManagerSettingsLocked()
	settings.ChmodDialog.AutoApplyLastSettings = enabled
	settings.ChmodDialog.LastModified = time.Now().UnixMilli()
	err := c.saveFileManagerSettingsLocked(settings)
	if err == nil {
		c.bumpSnapshotTime()
		go c.AutoSync()
	}
	return err
}

func (c *ConfigManager) SetFileManagerDoubleClickUncompressArchive(enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	settings := c.getFileManagerSettingsLocked()
	settings.DoubleClickUncompressArchive = enabled
	err := c.saveFileManagerSettingsLocked(settings)
	if err == nil {
		c.bumpSnapshotTime()
		go c.AutoSync()
	}
	return err
}

func (c *ConfigManager) SetFileManagerAutoRefreshDisabled(disabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	settings := c.getFileManagerSettingsLocked()
	settings.AutoRefreshDisabled = disabled
	err := c.saveFileManagerSettingsLocked(settings)
	if err == nil {
		c.bumpSnapshotTime()
		go c.AutoSync()
	}
	return err
}

func (c *ConfigManager) SetFileManagerSmartUncompressConflictStrategy(strategy string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	settings := c.getFileManagerSettingsLocked()
	settings.SmartUncompressConflictStrategy = normalizeFileManagerSmartUncompressConflictStrategy(strategy)
	err := c.saveFileManagerSettingsLocked(settings)
	if err == nil {
		c.bumpSnapshotTime()
		go c.AutoSync()
	}
	return err
}

// SetFileManagerMaxEditSize 设置文件编辑大小上限（MB），自动归一化到 [1, 50]。
func (c *ConfigManager) SetFileManagerMaxEditSize(mb int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	settings := c.getFileManagerSettingsLocked()
	settings.MaxEditSizeMB = normalizeFileManagerMaxEditSizeMB(mb)
	err := c.saveFileManagerSettingsLocked(settings)
	if err == nil {
		c.bumpSnapshotTime()
		go c.AutoSync()
	}
	return err
}

// GetFileManagerMaxEditSizeBytes 返回当前编辑大小上限（字节），供 external_edit 校验使用。
func (c *ConfigManager) GetFileManagerMaxEditSizeBytes() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	mb := normalizeFileManagerMaxEditSizeMB(c.getFileManagerSettingsLocked().MaxEditSizeMB)
	return int64(mb) * 1024 * 1024
}

// ─── 快捷命令 ──────────────────────────────────────

// GetQuickCommands 读取快捷命令列表
func (c *ConfigManager) GetQuickCommands() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := os.ReadFile(c.quickCmdFile)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// SaveQuickCommands 保存快捷命令列表（JSON 字符串），触发云端同步
func (c *ConfigManager) SaveQuickCommands(jsonStr string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := atomicWriteFile(c.quickCmdFile, []byte(jsonStr), 0600)
	if err == nil {
		c.bumpSnapshotTime()
		go c.AutoSync()
	}
	return err
}

// SaveQuickCommandsLocal 保存快捷命令列表到本地，不触发云端同步
func (c *ConfigManager) SaveQuickCommandsLocal(jsonStr string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return atomicWriteFile(c.quickCmdFile, []byte(jsonStr), 0600)
}

// GetParamHistory 读取参数历史
func (c *ConfigManager) GetParamHistory() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := os.ReadFile(c.paramHistFile)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// SaveParamHistory 保存参数历史
func (c *ConfigManager) SaveParamHistory(jsonStr string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return atomicWriteFile(c.paramHistFile, []byte(jsonStr), 0600)
}

func normalizeWorkspacePersistenceLevel(level string) string {
	if strings.TrimSpace(level) == "session" {
		return "session"
	}
	return "program"
}

func (c *ConfigManager) getWorkspacePrefsLocked() WorkspacePrefs {
	prefs := WorkspacePrefs{WorkspacePersistenceLevel: "program"}
	data, err := os.ReadFile(c.workspacePrefsFile)
	if err != nil {
		return prefs
	}
	var enabled bool
	if err := json.Unmarshal(data, &enabled); err == nil {
		prefs.RememberWorkspace = enabled
		return prefs
	}
	var payload WorkspacePrefs
	if err := json.Unmarshal(data, &payload); err == nil {
		prefs.RememberWorkspace = payload.RememberWorkspace
		prefs.WorkspacePersistenceLevel = normalizeWorkspacePersistenceLevel(payload.WorkspacePersistenceLevel)
		return prefs
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err == nil {
		if enabledValue, ok := raw["rememberWorkspace"].(bool); ok {
			prefs.RememberWorkspace = enabledValue
		}
		if levelValue, ok := raw["workspacePersistenceLevel"].(string); ok {
			prefs.WorkspacePersistenceLevel = normalizeWorkspacePersistenceLevel(levelValue)
		}
	}
	return prefs
}

func (c *ConfigManager) saveWorkspacePrefsLocked(prefs WorkspacePrefs) error {
	prefs.WorkspacePersistenceLevel = normalizeWorkspacePersistenceLevel(prefs.WorkspacePersistenceLevel)
	data, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	return atomicWriteFile(c.workspacePrefsFile, data, 0600)
}

func (c *ConfigManager) getWorkspaceSessionStatesLocked() map[string]json.RawMessage {
	data, err := os.ReadFile(c.workspaceSessionStateFile)
	if err != nil {
		return map[string]json.RawMessage{}
	}
	var states map[string]json.RawMessage
	if err := json.Unmarshal(data, &states); err != nil || states == nil {
		return map[string]json.RawMessage{}
	}
	return states
}

func (c *ConfigManager) saveWorkspaceSessionStatesLocked(states map[string]json.RawMessage) error {
	if len(states) == 0 {
		if err := os.Remove(c.workspaceSessionStateFile); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(c.workspaceSessionStateFile, data, 0600)
}

func (c *ConfigManager) deleteWorkspaceSessionStateLocked(serverId string) error {
	serverId = strings.TrimSpace(serverId)
	if serverId == "" {
		return nil
	}
	states := c.getWorkspaceSessionStatesLocked()
	if len(states) == 0 {
		return nil
	}
	if _, ok := states[serverId]; !ok {
		return nil
	}
	delete(states, serverId)
	return c.saveWorkspaceSessionStatesLocked(states)
}

func (c *ConfigManager) getPortForwardsLocked() map[string][]PersistedPortForward {
	data, err := os.ReadFile(c.portForwardsFile)
	if err != nil {
		return map[string][]PersistedPortForward{}
	}
	var states map[string][]PersistedPortForward
	if err := json.Unmarshal(data, &states); err != nil || states == nil {
		return map[string][]PersistedPortForward{}
	}
	return states
}

func (c *ConfigManager) savePortForwardsLocked(states map[string][]PersistedPortForward) error {
	cleaned := make(map[string][]PersistedPortForward, len(states))
	for serverId, list := range states {
		if strings.TrimSpace(serverId) == "" || len(list) == 0 {
			continue
		}
		cleaned[serverId] = list
	}
	if len(cleaned) == 0 {
		if err := os.Remove(c.portForwardsFile); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(cleaned, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(c.portForwardsFile, data, 0600)
}

func (c *ConfigManager) GetPortForwards(serverId string) []PersistedPortForward {
	c.mu.RLock()
	defer c.mu.RUnlock()
	states := c.getPortForwardsLocked()
	list, ok := states[strings.TrimSpace(serverId)]
	if !ok {
		return []PersistedPortForward{}
	}
	result := make([]PersistedPortForward, len(list))
	copy(result, list)
	return result
}

func (c *ConfigManager) SavePortForwards(serverId string, list []PersistedPortForward) error {
	serverId = strings.TrimSpace(serverId)
	if serverId == "" {
		return fmt.Errorf("missing serverId")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	states := c.getPortForwardsLocked()
	if len(list) == 0 {
		delete(states, serverId)
	} else {
		states[serverId] = list
	}
	if err := c.savePortForwardsLocked(states); err != nil {
		return err
	}
	c.bumpSnapshotTime()
	return nil
}

func (c *ConfigManager) deletePortForwardsLocked(serverId string) error {
	serverId = strings.TrimSpace(serverId)
	if serverId == "" {
		return nil
	}
	states := c.getPortForwardsLocked()
	if _, ok := states[serverId]; !ok {
		return nil
	}
	delete(states, serverId)
	return c.savePortForwardsLocked(states)
}

func (c *ConfigManager) GetRememberWorkspace() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getWorkspacePrefsLocked().RememberWorkspace
}

func (c *ConfigManager) GetWorkspacePersistenceLevel() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getWorkspacePrefsLocked().WorkspacePersistenceLevel
}

func (c *ConfigManager) SetRememberWorkspace(enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefs := c.getWorkspacePrefsLocked()
	prefs.RememberWorkspace = enabled
	if err := c.saveWorkspacePrefsLocked(prefs); err != nil {
		return err
	}
	if !enabled {
		if err := os.Remove(c.workspaceStateFile); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Remove(c.workspaceSessionStateFile); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (c *ConfigManager) SetWorkspacePersistenceLevel(level string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefs := c.getWorkspacePrefsLocked()
	prefs.WorkspacePersistenceLevel = normalizeWorkspacePersistenceLevel(level)
	return c.saveWorkspacePrefsLocked(prefs)
}

func (c *ConfigManager) GetWorkspaceState() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := os.ReadFile(c.workspaceStateFile)
	if err != nil {
		return ""
	}
	return string(data)
}

func (c *ConfigManager) SaveWorkspaceState(jsonStr string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	trimmed := strings.TrimSpace(jsonStr)
	if trimmed == "" {
		if err := os.Remove(c.workspaceStateFile); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if !json.Valid([]byte(trimmed)) {
		return fmt.Errorf("invalid workspace state")
	}
	return atomicWriteFile(c.workspaceStateFile, []byte(trimmed), 0600)
}

func (c *ConfigManager) GetWorkspaceSessionState(serverId string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	states := c.getWorkspaceSessionStatesLocked()
	raw, ok := states[strings.TrimSpace(serverId)]
	if !ok {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func (c *ConfigManager) SaveWorkspaceSessionState(serverId string, jsonStr string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	serverId = strings.TrimSpace(serverId)
	if serverId == "" {
		return fmt.Errorf("missing serverId")
	}
	trimmed := strings.TrimSpace(jsonStr)
	if trimmed == "" {
		return c.deleteWorkspaceSessionStateLocked(serverId)
	}
	if !json.Valid([]byte(trimmed)) {
		return fmt.Errorf("invalid workspace session state")
	}
	states := c.getWorkspaceSessionStatesLocked()
	states[serverId] = json.RawMessage(trimmed)
	return c.saveWorkspaceSessionStatesLocked(states)
}

func (c *ConfigManager) ClearWorkspaceState() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.Remove(c.workspaceStateFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ─── 命令历史 ──────────────────────────────────────

// historyFileName 把会话 id 规整为历史文件名（不含扩展名），同时防止路径穿越。
//
// 不能用 filepath.Base：它在 Linux 上会把 "local_/bin/bash" 裁成 "bash"，
// 丢掉 local_/serial_ 前缀，导致 CleanupOrphanedHistory 把它当孤儿删掉
// （本地/串口终端的历史在每次启动后被清空，重启即丢失）。Windows 的 shell
// 名（pwsh.exe / wsl://Ubuntu）不含路径分隔符，filepath.Base 不会改它们，
// 这正是 PowerShell/WSL 正常、Linux bash 丢失的根因。
//
// 这里直接把所有平台上的路径分隔符与路径相关字符（/ \ :）统一替换为下划线：
// 替换后不再含任何分隔符，天然杜绝路径穿越（"../../etc/passwd" 也会被改成
// 形如 "______etc_passwd" 的扁平名字），同时完整保留 local_/serial_ 前缀契约，
// 使清理孤儿逻辑能正确豁免本地/串口终端历史。
// 对既有 id（SSH 连接的 hex ID、Windows shell 名等不含分隔符的 id）结果不变，
// 向后兼容。
func historyFileName(id string) string {
	if id == "" {
		return ""
	}
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(id)
}

// GetCommandHistory 读取指定会话的命令历史
func (c *ConfigManager) GetCommandHistory(sessionId string) string {
	// 防止路径穿越：保留 local_/serial_ 前缀（见 historyFileName）
	sessionId = historyFileName(sessionId)
	path := filepath.Join(c.historyDir, sessionId+".json")
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// SaveCommandHistory 保存指定会话的命令历史
func (c *ConfigManager) SaveCommandHistory(sessionId, jsonStr string) error {
	// 防止路径穿越：保留 local_/serial_ 前缀（见 historyFileName）
	sessionId = historyFileName(sessionId)
	path := filepath.Join(c.historyDir, sessionId+".json")
	c.mu.Lock()
	defer c.mu.Unlock()
	return atomicWriteFile(path, []byte(jsonStr), 0600)
}

// GetGlobalCommandHistory 读取全局命令历史
func (c *ConfigManager) GetGlobalCommandHistory() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := os.ReadFile(c.globalHistFile)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// SaveGlobalCommandHistory 保存全局命令历史
func (c *ConfigManager) SaveGlobalCommandHistory(jsonStr string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return atomicWriteFile(c.globalHistFile, []byte(jsonStr), 0600)
}

// CleanupOrphanedHistory 清理已不存在的连接的历史文件
func (c *ConfigManager) CleanupOrphanedHistory() {
	// 安全阀：historyDir 必须是独立子目录。测试/误配若指向 configDir，
	// 会把 sync_tombstones.json 等配置文件当成「孤儿历史」删掉。
	if c == nil || c.historyDir == "" || c.configDir == "" || filepath.Clean(c.historyDir) == filepath.Clean(c.configDir) {
		return
	}
	conns := c.GetConnections()
	active := make(map[string]bool)
	for _, conn := range conns {
		active[conn.ID] = true
	}

	entries, err := os.ReadDir(c.historyDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// 跳过 global.json
		if e.Name() == "global.json" {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if strings.HasPrefix(id, "local_") || strings.HasPrefix(id, "serial_") {
			continue
		}
		if !active[id] {
			path := filepath.Join(c.historyDir, e.Name())
			os.Remove(path)
		}
	}
}

// ─── 对外依赖注入 ──────────────────────────────────────────

// SetProgramDir 注入程序目录路径（用于内置主题包等需要程序路径的场景）
func (c *ConfigManager) SetProgramDir(dir string) {
	c.programDir = dir
}

// SetWailsCtx 注入 Wails 上下文（用于向前端发送同步事件）
func (c *ConfigManager) SetWailsCtx(ctx context.Context) {
	c.wailsCtx = ctx
}

// SetOnDeleteConnection 注入删除连接回调（清理 pingStates 等包级状态）
func (c *ConfigManager) SetOnDeleteConnection(fn func(string)) {
	c.onDeleteConnection = fn
}

// GetConfigDir 返回配置目录路径
func (c *ConfigManager) GetConfigDir() string {
	return c.configDir
}

// BumpSnapshotTime 导出包装器：更新本地快照时间戳
func (c *ConfigManager) BumpSnapshotTime() int64 { return c.bumpSnapshotTime() }

// LoadLastSyncTimeMax 导出包装器：取所有后端最大同步时间
func (c *ConfigManager) LoadLastSyncTimeMax() int64 { return c.loadLastSyncTimeMax() }

// ─── 从 package main 复制的辅助函数 ─────────────────────────

// dialAddr 拼接 host:port，自动处理 IPv6 地址。
// ponytail: 与 ssh.go 中的 dialAddr 重复；config 簇迁移到 internal/config 后无法直接调用 package main 的版本。
// 升级路径：未来下沉到共享 netutil 包后两处统一引用。
func dialAddr(host string, port int) string {
	host = strings.TrimSpace(host)
	host = strings.Trim(host, "[]")
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// getKnownHostsPath 返回跨平台的 known_hosts 文件路径。
// ponytail: 与 ssh.go 中的 getKnownHostsPath 重复；config 簇迁移后 sftp.go 需要独立调用。
// 升级路径：同 dialAddr，下沉到共享包后统一。
func getKnownHostsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "known_hosts")
}

// ─── 导出包装器（供 package main 通过 type alias + wrapper 调用）───

func NormalizeTerminalEncoding(value string) string {
	return normalizeTerminalEncoding(value)
}

func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	return atomicWriteFile(path, data, perm)
}
