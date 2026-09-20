package config

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	aitypes "lumeterm/internal/aitypes"
)

// 节点导入/导出功能。
//
// 设计要点：
//   - 导出文件格式为 LumeTerm 自有 JSON（明文，含真实密码/私钥），便于跨机器完整还原。
//   - 导入采用"合并（跳过重复）"策略：按 host+port+username 判重，已存在则跳过，仅新增。
//   - 敏感数据全程在后端处理，不经过前端暴露面。
//   - 导入后走 saveConnectionsFile/saveCredentialsFile 自动加密 + 原子写，并触发云同步。

// connectionsExportFormat 导出 format 字段值（写新读旧：旧文件为 lumin-ssh-connections）
const connectionsExportFormat = "lumeterm-connections"

// legacyConnectionsExportFormat 旧品牌时期的 format 值，导入时兼容识别
const legacyConnectionsExportFormat = "lumin-ssh-connections"

// connectionsExport 节点导出文件的顶层结构
type connectionsExport struct {
	Format      string                `json:"format"` // 固定 lumeterm-connections（兼容旧 lumin-ssh-connections），导入时据此校验来源
	Version     int                   `json:"version"`
	ExportedAt  int64                 `json:"exportedAt"` // Unix 毫秒时间戳
	Connections []Connection          `json:"connections"`
	Credentials []Credential          `json:"credentials"` // 仅含被 connection 引用的凭据
	ProxyNodes  []aitypes.AIProxyNode `json:"proxy_nodes,omitempty"`
}

// skippedItem 记录导入时被跳过（本地已存在）的节点信息
type skippedItem struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
}

// ImportResult 导入操作的统计结果
type ImportResult struct {
	Total    int           `json:"total"`    // 文件中的节点总数
	Imported int           `json:"imported"` // 实际新增数
	Skipped  int           `json:"skipped"`  // 跳过的重复数
	Details  []skippedItem `json:"details"`  // 被跳过的节点明细
}

// newConnectionID 生成一个新的连接/凭据 ID（crypto/rand 16 hex 字符，与 SaveConnection 口径一致）
func newConnectionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// 极少触发；回退到纳秒时间戳避免返回空 ID
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// buildConnectionsExport 组装导出快照：仅保留被 connection 引用的 credential。
func buildConnectionsExport(conns []Connection, creds []Credential) SyncSnapshot {
	return buildConnectionsExportWithProxyNodes(conns, creds, nil)
}

func buildConnectionsExportWithProxyNodes(conns []Connection, creds []Credential, proxyNodes []aitypes.AIProxyNode) SyncSnapshot {
	// 收集所有被引用的 credentialId
	referenced := make(map[string]bool)
	referencedProxyNodes := make(map[string]bool)
	for _, c := range conns {
		if c.CredentialID != "" {
			referenced[c.CredentialID] = true
		}
		if c.ProxyMode == "node" && c.ProxyNodeID != "" {
			referencedProxyNodes[c.ProxyNodeID] = true
		}
	}
	exportedCreds := make([]Credential, 0, len(referenced))
	for _, cr := range creds {
		if referenced[cr.ID] {
			exportedCreds = append(exportedCreds, cr)
		}
	}
	exportedProxyNodes := make([]aitypes.AIProxyNode, 0, len(referencedProxyNodes))
	for _, node := range proxyNodes {
		if referencedProxyNodes[node.ID] {
			exportedProxyNodes = append(exportedProxyNodes, node)
		}
	}
	return SyncSnapshot{
		Connections:  conns,
		Credentials:  exportedCreds,
		ProxyNodes:   exportedProxyNodes,
		SnapshotTime: time.Now().UnixMilli(),
	}
}

// BuildConnectionsExportWithProxyNodes 导出包装器
func BuildConnectionsExportWithProxyNodes(conns []Connection, creds []Credential, proxyNodes []aitypes.AIProxyNode) SyncSnapshot {
	return buildConnectionsExportWithProxyNodes(conns, creds, proxyNodes)
}

// connectionKey 返回用于判重的三元组 key（host+port+username，port 缺省 22）
func connectionKey(c Connection) string {
	port := c.Port
	if port == 0 {
		port = 22
	}
	return fmt.Sprintf("%s|%d|%s", c.Host, port, c.Username)
}

// mergeImport 是纯函数：将导入列表与本地列表合并（跳过重复），返回待新增列表与统计结果。
// 不触碰任何存储，便于单元测试。
//
// 判重口径：host+port+username（port 默认 22），与前端 saveServerConfig 一致。
func mergeImport(localConns, importConns []Connection) (toAdd []Connection, result ImportResult) {
	// 本地已存在的三元组集合
	localKeys := make(map[string]bool, len(localConns))
	for _, c := range localConns {
		localKeys[connectionKey(c)] = true
	}
	// 本地已占用的 ID 集合
	localIDs := make(map[string]bool, len(localConns))
	for _, c := range localConns {
		localIDs[c.ID] = true
	}
	// 本次导入已分配的 ID（避免导入文件内部 ID 重复）
	usedIDs := make(map[string]bool, len(importConns))

	now := time.Now().UnixMilli()
	result.Total = len(importConns)
	result.Details = []skippedItem{}
	toAdd = make([]Connection, 0, len(importConns))

	for _, c := range importConns {
		key := connectionKey(c)
		if localKeys[key] {
			// 本地已存在相同 host+port+username，跳过
			result.Skipped++
			port := c.Port
			if port == 0 {
				port = 22
			}
			result.Details = append(result.Details, skippedItem{
				Name:     c.Name,
				Host:     c.Host,
				Port:     port,
				Username: c.Username,
			})
			continue
		}
		// ID 冲突（被本地或本次导入已占用）则重新生成
		if c.ID == "" || localIDs[c.ID] || usedIDs[c.ID] {
			c.ID = newConnectionID()
		}
		usedIDs[c.ID] = true
		c.LastModified = now
		toAdd = append(toAdd, c)
	}
	result.Imported = len(toAdd)
	return toAdd, result
}

// mergeImportCredentials 合并凭据，并返回原 ID 到保存后 ID 的映射。
// 本地已有同 ID 凭据时直接复用本地凭据，避免导入云端备份时产生重复凭据。
func mergeImportCredentials(localCreds, importCreds []Credential) ([]Credential, map[string]string) {
	localIDs := make(map[string]bool, len(localCreds))
	for _, cr := range localCreds {
		localIDs[cr.ID] = true
	}
	now := time.Now().UnixMilli()
	seenImportIDs := make(map[string]bool, len(importCreds))
	toAdd := make([]Credential, 0, len(importCreds))
	idMap := make(map[string]string, len(importCreds))
	for _, cr := range importCreds {
		oldID := strings.TrimSpace(cr.ID)
		cr.ID = oldID
		if oldID != "" {
			if localIDs[oldID] {
				idMap[oldID] = oldID
				continue
			}
			if seenImportIDs[oldID] {
				continue
			}
			seenImportIDs[oldID] = true
			idMap[oldID] = oldID
		} else {
			cr.ID = newConnectionID()
		}
		cr.LastModified = now
		toAdd = append(toAdd, cr)
	}
	return toAdd, idMap
}

func filterImportCredentialsForConnections(importCreds []Credential, conns []Connection) []Credential {
	referenced := make(map[string]bool, len(conns))
	for _, conn := range conns {
		if conn.CredentialID != "" {
			referenced[conn.CredentialID] = true
		}
	}
	filtered := make([]Credential, 0, len(importCreds))
	for _, cred := range importCreds {
		if referenced[cred.ID] {
			filtered = append(filtered, cred)
		}
	}
	return filtered
}

func mergeImportProxyNodes(localNodes, importNodes []aitypes.AIProxyNode) ([]aitypes.AIProxyNode, map[string]string) {
	localIDs := make(map[string]bool, len(localNodes))
	for _, node := range localNodes {
		localIDs[node.ID] = true
	}
	now := time.Now().UnixMilli()
	seenImportIDs := make(map[string]bool, len(importNodes))
	toAdd := make([]aitypes.AIProxyNode, 0, len(importNodes))
	idMap := make(map[string]string, len(importNodes))
	for _, node := range importNodes {
		oldID := strings.TrimSpace(node.ID)
		node.ID = oldID
		if oldID != "" {
			if localIDs[oldID] {
				idMap[oldID] = oldID
				continue
			}
			if seenImportIDs[oldID] {
				continue
			}
			seenImportIDs[oldID] = true
			idMap[oldID] = oldID
		} else {
			node.ID = newConnectionID()
		}
		node.UpdatedAt = now
		toAdd = append(toAdd, node)
	}
	return toAdd, idMap
}

func filterImportProxyNodesForConnections(importNodes []aitypes.AIProxyNode, conns []Connection) []aitypes.AIProxyNode {
	referenced := make(map[string]bool, len(conns))
	for _, conn := range conns {
		if conn.ProxyMode == "node" && conn.ProxyNodeID != "" {
			referenced[conn.ProxyNodeID] = true
		}
	}
	filtered := make([]aitypes.AIProxyNode, 0, len(importNodes))
	for _, node := range importNodes {
		if referenced[node.ID] {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func applyImportReferenceMappings(conns []Connection, credIDMap map[string]string, proxyIDMap map[string]string) {
	for i := range conns {
		if newID, ok := credIDMap[conns[i].CredentialID]; ok {
			conns[i].CredentialID = newID
		}
		if conns[i].ProxyMode == "node" {
			if newID, ok := proxyIDMap[conns[i].ProxyNodeID]; ok {
				conns[i].ProxyNodeID = newID
			}
		}
	}
}

// parseConnectionsExport 解析并校验导入文件内容。
// 容错：port 缺失默认 22，authMethod 缺失按 password。
func parseConnectionsExport(data []byte) (*connectionsExport, error) {
	var exp connectionsExport
	if err := json.Unmarshal(data, &exp); err != nil {
		return nil, fmt.Errorf("解析文件失败：%w", err)
	}
	if exp.Format != connectionsExportFormat && exp.Format != legacyConnectionsExportFormat {
		return nil, fmt.Errorf("无效的导入文件格式（format 应为 %s）", connectionsExportFormat)
	}
	// 字段容错：补默认值
	for i := range exp.Connections {
		if exp.Connections[i].Port == 0 {
			exp.Connections[i].Port = 22
		}
		if exp.Connections[i].AuthMethod == "" {
			exp.Connections[i].AuthMethod = "password"
		}
		exp.Connections[i].TerminalEncoding = normalizeTerminalEncoding(exp.Connections[i].TerminalEncoding)
	}
	return &exp, nil
}

// buildImportTemplate 生成带样例的导入模板，方便用户批量录入。
// 含 2 条样例（密码认证 + 私钥认证），host/密码用占位符，用户照着复制修改。
func buildImportTemplate(lang string) SyncSnapshot {
	name1 := "示例-密码认证"
	name2 := "示例-私钥认证"
	if lang == "en-US" {
		name1 = "Example-Password Auth"
		name2 = "Example-PrivateKey Auth"
	}
	return SyncSnapshot{
		Connections: []Connection{
			{
				ID:               "",
				Name:             name1,
				Host:             "1.2.3.4",
				Port:             22,
				Username:         "root",
				Password:         "your-password-here",
				AuthMethod:       "password",
				Group:            "web-servers",
				TerminalEncoding: "utf-8",
			},
			{
				ID:               "",
				Name:             name2,
				Host:             "5.6.7.8",
				Port:             22,
				Username:         "ubuntu",
				AuthMethod:       "privateKey",
				PrivateKey:       "-----BEGIN OPENSSH PRIVATE KEY-----\nyour-private-key-content-here\n-----END OPENSSH PRIVATE KEY-----",
				Passphrase:       "optional-key-passphrase",
				Group:            "db-servers",
				TerminalEncoding: "utf-8",
			},
		},
		Credentials: []Credential{},
	}
}

// BuildImportTemplate 导出包装器
func BuildImportTemplate(lang string) SyncSnapshot {
	return buildImportTemplate(lang)
}

// ── 密文导入/导出 ──────────────────────────────────────────

// errNeedPassword 表示密文导入时所有候选密钥都解密失败，需要用户输入密码。
var errNeedPassword = errors.New("need password")
var ErrNeedPassword = errNeedPassword // 导出别名供 package main 引用

// encryptExportData 把导出对象序列化为 JSON 并用指定密码加密，返回 LUMETERM2 字符串。
func (c *ConfigManager) encryptExportData(exp SyncSnapshot, password string) (string, error) {
	data, err := json.MarshalIndent(exp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal export: %w", err)
	}
	enc, err := encryptLUMETERM2(string(data), password)
	if err != nil {
		return "", fmt.Errorf("encrypt export: %w", err)
	}
	return enc, nil
}

// EncryptExportData 导出包装器
func (c *ConfigManager) EncryptExportData(exp SyncSnapshot, password string) (string, error) {
	return c.encryptExportData(exp, password)
}

// parseImportData 智能解析导入文件原始字节：先试明文 JSON，失败则用密码解密 LUMETERM2。
//
// 解析优先级：
//  1. 明文 connectionsExport（format 字段匹配）
//  2. 明文 SyncSnapshot
//  3. LUMETERM2 密文（需 password）
//
// password 为空表示用户未提供密码；解密失败时返回 errNeedPassword。
func (c *ConfigManager) parseImportData(data []byte, password string) (*SyncSnapshot, error) {
	// 先尝试明文 JSON：优先 connectionsExport 格式
	if exp, ok := tryParseExportJSON(data); ok {
		return exp, nil
	}
	// 再尝试明文 SyncSnapshot 格式（云端备份解密后的结构，或用户手动构造）
	if exp, ok := tryParseSnapshotJSON(data); ok {
		return exp, nil
	}

	raw := strings.TrimSpace(string(data))
	if !hasBackupPrefix(raw) {
		return nil, fmt.Errorf("不支持的导入格式：仅支持明文 JSON 与 LUMETERM2 密文（.lumeterm2/.lumin2）")
	}
	if password == "" {
		return nil, errNeedPassword
	}
	decrypted, err := decryptLUMETERM2(raw, password)
	if err != nil {
		return nil, errNeedPassword
	}
	if exp, ok := tryParseSnapshotJSON([]byte(decrypted)); ok {
		return exp, nil
	}
	if exp, ok := tryParseExportJSON([]byte(decrypted)); ok {
		return exp, nil
	}
	return nil, fmt.Errorf("LUMETERM2 解密成功但内容不是有效 SyncSnapshot")
}

// ParseImportData 导出包装器
func (c *ConfigManager) ParseImportData(data []byte, password string) (*SyncSnapshot, error) {
	return c.parseImportData(data, password)
}

// tryParseExportJSON 兼容旧版 connectionsExport，转换为 SyncSnapshot。
func tryParseExportJSON(data []byte) (*SyncSnapshot, bool) {
	var exp connectionsExport
	if err := json.Unmarshal(data, &exp); err != nil {
		return nil, false
	}
	if exp.Format != connectionsExportFormat && exp.Format != legacyConnectionsExportFormat {
		return nil, false
	}
	snap := &SyncSnapshot{
		Connections:  exp.Connections,
		Credentials:  exp.Credentials,
		ProxyNodes:   exp.ProxyNodes,
		SnapshotTime: exp.ExportedAt,
	}
	normalizeImportSnapshot(snap)
	return snap, true
}

func tryParseSnapshotJSON(data []byte) (*SyncSnapshot, bool) {
	var snap SyncSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, false
	}
	if snap.Connections == nil {
		return nil, false
	}
	normalizeImportSnapshot(&snap)
	return &snap, true
}

func normalizeImportSnapshot(snap *SyncSnapshot) {
	for i := range snap.Connections {
		if snap.Connections[i].Port == 0 {
			snap.Connections[i].Port = 22
		}
		if snap.Connections[i].AuthMethod == "" {
			snap.Connections[i].AuthMethod = "password"
		}
		snap.Connections[i].TerminalEncoding = normalizeTerminalEncoding(snap.Connections[i].TerminalEncoding)
	}
}
