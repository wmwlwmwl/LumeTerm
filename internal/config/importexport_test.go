package config

import (
	"encoding/json"
	"errors"
	aitypes "lumeterm/internal/aitypes"
	"strings"
	"testing"
)

func TestMergeImport_AllNew(t *testing.T) {
	local := []Connection{}
	incoming := []Connection{
		{ID: "a", Host: "h1", Port: 22, Username: "root"},
		{ID: "b", Host: "h2", Port: 22, Username: "root"},
	}
	toAdd, result := mergeImport(local, incoming)
	if len(toAdd) != 2 {
		t.Fatalf("expected 2 to add, got %d", len(toAdd))
	}
	if result.Total != 2 || result.Imported != 2 || result.Skipped != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	// ID 保留
	if toAdd[0].ID != "a" || toAdd[1].ID != "b" {
		t.Fatalf("IDs not preserved: %s %s", toAdd[0].ID, toAdd[1].ID)
	}
	// LastModified 应被重置为非零
	if toAdd[0].LastModified == 0 {
		t.Fatal("LastModified not set")
	}
}

func TestMergeImport_AllDuplicates(t *testing.T) {
	local := []Connection{
		{ID: "x", Host: "h1", Port: 22, Username: "root"},
	}
	incoming := []Connection{
		{ID: "a", Host: "h1", Port: 22, Username: "root"}, // 重复
	}
	toAdd, result := mergeImport(local, incoming)
	if len(toAdd) != 0 {
		t.Fatalf("expected 0 to add, got %d", len(toAdd))
	}
	if result.Skipped != 1 || result.Imported != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Details) != 1 || result.Details[0].Host != "h1" {
		t.Fatalf("unexpected details: %+v", result.Details)
	}
}

func TestMergeImport_PartialDuplicate(t *testing.T) {
	local := []Connection{
		{ID: "x", Host: "h1", Port: 22, Username: "root"},
	}
	incoming := []Connection{
		{ID: "a", Host: "h1", Port: 22, Username: "root"}, // 重复
		{ID: "b", Host: "h2", Port: 22, Username: "root"}, // 新增
	}
	toAdd, result := mergeImport(local, incoming)
	if len(toAdd) != 1 {
		t.Fatalf("expected 1 to add, got %d", len(toAdd))
	}
	if toAdd[0].Host != "h2" {
		t.Fatalf("expected h2, got %s", toAdd[0].Host)
	}
	if result.Imported != 1 || result.Skipped != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestMergeImport_PortDefault22(t *testing.T) {
	// 本地 port=22，导入 port=0（缺省），应判为重复
	local := []Connection{
		{ID: "x", Host: "h1", Port: 22, Username: "root"},
	}
	incoming := []Connection{
		{ID: "a", Host: "h1", Port: 0, Username: "root"}, // port 缺省按 22，重复
	}
	toAdd, result := mergeImport(local, incoming)
	if len(toAdd) != 0 {
		t.Fatalf("expected 0 to add (port default 22 dup), got %d", len(toAdd))
	}
	if result.Skipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", result.Skipped)
	}
	if result.Details[0].Port != 22 {
		t.Fatalf("expected detail port 22, got %d", result.Details[0].Port)
	}
}

func TestMergeImport_IDCollisionRegenerated(t *testing.T) {
	local := []Connection{
		{ID: "shared", Host: "h1", Port: 22, Username: "root"},
	}
	incoming := []Connection{
		{ID: "shared", Host: "h2", Port: 22, Username: "admin"}, // host/user 不同，非重复；但 ID 冲突
	}
	toAdd, _ := mergeImport(local, incoming)
	if len(toAdd) != 1 {
		t.Fatalf("expected 1 to add, got %d", len(toAdd))
	}
	if toAdd[0].ID == "shared" {
		t.Fatal("expected ID to be regenerated on collision, still 'shared'")
	}
	if toAdd[0].ID == "" {
		t.Fatal("regenerated ID is empty")
	}
	if toAdd[0].Host != "h2" {
		t.Fatalf("content mismatch: %s", toAdd[0].Host)
	}
}

func TestMergeImport_InternalIDDuplicate(t *testing.T) {
	// 导入文件内部两条记录 ID 相同（数据异常），应重新生成第二条的 ID
	local := []Connection{}
	incoming := []Connection{
		{ID: "dup", Host: "h1", Port: 22, Username: "root"},
		{ID: "dup", Host: "h2", Port: 22, Username: "root"},
	}
	toAdd, result := mergeImport(local, incoming)
	if len(toAdd) != 2 {
		t.Fatalf("expected 2 to add, got %d", len(toAdd))
	}
	if result.Imported != 2 {
		t.Fatalf("expected imported=2, got %d", result.Imported)
	}
	// 两条 ID 必须不同
	if toAdd[0].ID == toAdd[1].ID {
		t.Fatal("internal duplicate IDs not regenerated")
	}
}

func TestParseConnectionsExport_Valid(t *testing.T) {
	raw := `{"format":"lumeterm-connections","version":1,"exportedAt":100,"connections":[{"id":"a","host":"h","username":"u"}],"credentials":[]}`
	exp, err := parseConnectionsExport([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp.Format != connectionsExportFormat {
		t.Fatalf("wrong format: %s", exp.Format)
	}
	// port 缺省应补 22，authMethod 缺省应补 password
	if exp.Connections[0].Port != 22 {
		t.Fatalf("expected default port 22, got %d", exp.Connections[0].Port)
	}
	if exp.Connections[0].AuthMethod != "password" {
		t.Fatalf("expected default authMethod password, got %s", exp.Connections[0].AuthMethod)
	}
}

// 旧品牌时期的 format 值（lumin-ssh-connections）仍应可导入（写新读旧）
func TestParseConnectionsExport_LegacyFormat(t *testing.T) {
	raw := `{"format":"lumin-ssh-connections","version":1,"exportedAt":100,"connections":[{"id":"a","host":"h","username":"u"}],"credentials":[]}`
	if _, err := parseConnectionsExport([]byte(raw)); err != nil {
		t.Fatalf("legacy format should be accepted: %v", err)
	}
}

func TestParseConnectionsExport_WrongFormat(t *testing.T) {
	raw := `{"format":"something-else","version":1,"connections":[]}`
	_, err := parseConnectionsExport([]byte(raw))
	if err == nil {
		t.Fatal("expected error for wrong format, got nil")
	}
}

func TestParseConnectionsExport_InvalidJSON(t *testing.T) {
	_, err := parseConnectionsExport([]byte("{not json"))
	if err == nil {
		t.Fatal("expected error for invalid json, got nil")
	}
}

func TestBuildConnectionsExport_OnlyReferencedCreds(t *testing.T) {
	conns := []Connection{
		{ID: "c1", Host: "h1", Username: "u", CredentialID: "cred1"},
		{ID: "c2", Host: "h2", Username: "u"}, // 无凭据引用
	}
	creds := []Credential{
		{ID: "cred1", Name: "one"},
		{ID: "cred2", Name: "two"}, // 未被引用，不应导出
	}
	exp := buildConnectionsExport(conns, creds)
	if len(exp.Credentials) != 1 {
		t.Fatalf("expected 1 exported credential, got %d", len(exp.Credentials))
	}
	if exp.Credentials[0].ID != "cred1" {
		t.Fatalf("expected cred1, got %s", exp.Credentials[0].ID)
	}
	if exp.SnapshotTime == 0 {
		t.Fatal("expected snapshot_time to be set")
	}
}

// 导出 → 导入 往返测试：导出对象序列化后能被 parseImportData 正确解析
func TestExportImportRoundTrip(t *testing.T) {
	conns := []Connection{
		{ID: "c1", Host: "h1", Port: 22, Username: "root", Password: "secret", AuthMethod: "password"},
	}
	creds := []Credential{}
	exp := buildConnectionsExport(conns, creds)
	data, err := json.Marshal(exp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	cm := newTestConfigManager(t)
	parsed, err := cm.parseImportData(data, "")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(parsed.Connections) != 1 || parsed.Connections[0].Password != "secret" {
		t.Fatalf("roundtrip lost data: %+v", parsed)
	}
}

func TestMergeImportCredentials_SkipExisting(t *testing.T) {
	local := []Credential{
		{ID: "cred1", Name: "one"},
	}
	incoming := []Credential{
		{ID: "cred1", Name: "one-dup"}, // 本地已有同 ID，导入时复用本地凭据，不新增重复项
		{ID: "cred2", Name: "two"},     // 新增
	}
	toAdd, idMap := mergeImportCredentials(local, incoming)
	if len(toAdd) != 1 || toAdd[0].ID != "cred2" {
		t.Fatalf("expected only cred2 to add, got %+v", toAdd)
	}
	if idMap["cred1"] != "cred1" || idMap["cred2"] != "cred2" {
		t.Fatalf("unexpected id map: %+v", idMap)
	}
}

func TestFilterImportCredentialsForConnections(t *testing.T) {
	creds := []Credential{{ID: "cred1"}, {ID: "cred2"}}
	conns := []Connection{{CredentialID: "cred2"}}
	filtered := filterImportCredentialsForConnections(creds, conns)
	if len(filtered) != 1 || filtered[0].ID != "cred2" {
		t.Fatalf("expected only referenced cred2, got %+v", filtered)
	}
}

// ── 密文导入/导出测试 ──────────────────────────────────────────

const lumeterm2TestVector = "LUMETERM2:AgADNFAAAQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobF9JHcFJkz4QPNXc0pWTEDpoMJ0Nym0pLUA/Ky8DM1AfpbnyW3TqAZ7fkFSbkZiyXk76SFCMLtTyEajS/guHS/eZCMqZwId/HJj4W8SAGmgyg4VY6cb/wYOoXWNDHyyDK1UnV8A0jEU19FbZAFmBsZx/EfFopbXnqw5ec87NowYY="

func TestLUMETERM2FixedVector(t *testing.T) {
	password := "跨端-password"
	payload := `{"connections":[{"id":"vector","host":"example.com","port":22,"username":"root"}],"snapshot_time":1700000000000}`
	salt := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	nonce := []byte{16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27}
	got, err := encryptLUMETERM2WithSaltNonce(payload, password, salt, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if got != lumeterm2TestVector {
		t.Fatalf("vector mismatch:\n%s", got)
	}
	decrypted, err := decryptLUMETERM2(got, password)
	if err != nil || decrypted != payload {
		t.Fatalf("decrypt mismatch: %v", err)
	}
	// 旧前缀（LUMIN2:，改名前存量密文）应同样可解：仅前缀不同、容器结构一致
	legacy := "LUMIN2:" + strings.TrimPrefix(lumeterm2TestVector, "LUMETERM2:")
	legacyDecrypted, err := decryptLUMETERM2(legacy, password)
	if err != nil || legacyDecrypted != payload {
		t.Fatalf("legacy prefix decrypt mismatch: %v", err)
	}
}

// newTestConfigManager 创建一个临时 ConfigManager 用于加解密测试（会生成临时配置目录）。
func newTestConfigManager(t *testing.T) *ConfigManager {
	t.Helper()
	return NewConfigManager()
}

// TestEncryptedRoundTrip_PasswordKey 密文往返：用密码加密 → parseImportData 解密 → 数据一致
func TestEncryptedRoundTrip_PasswordKey(t *testing.T) {
	cm := newTestConfigManager(t)
	exp := buildConnectionsExport([]Connection{
		{ID: "c1", Host: "h1", Port: 22, Username: "root", Password: "secret", AuthMethod: "password"},
	}, []Credential{})
	password := "myPassword123"

	encrypted, err := cm.encryptExportData(exp, password)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	// 用错误密码解密应返回 errNeedPassword
	_, err = cm.parseImportData([]byte(encrypted), "wrong")
	if !errors.Is(err, errNeedPassword) {
		t.Fatalf("expected errNeedPassword with wrong key, got %v", err)
	}

	// 用正确密码解密
	parsed, err := cm.parseImportData([]byte(encrypted), password)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(parsed.Connections) != 1 || parsed.Connections[0].Password != "secret" {
		t.Fatalf("roundtrip lost data: %+v", parsed)
	}
}

// TestParseImportData_Plaintext 明文 JSON 应直接解析，不走解密
func TestParseImportData_Plaintext(t *testing.T) {
	cm := newTestConfigManager(t)
	plainExp := buildConnectionsExport([]Connection{
		{Host: "h1", Port: 22, Username: "root"},
	}, []Credential{})
	data, _ := json.Marshal(plainExp)

	parsed, err := cm.parseImportData(data, "")
	if err != nil {
		t.Fatalf("plaintext parse failed: %v", err)
	}
	if len(parsed.Connections) != 1 || parsed.Connections[0].Host != "h1" {
		t.Fatalf("plaintext data mismatch: %+v", parsed)
	}
}

// TestParseImportData_SyncSnapshotFormat 云端 SyncSnapshot 格式（明文）应能解析
func TestParseImportData_SyncSnapshotFormat(t *testing.T) {
	cm := newTestConfigManager(t)
	snap := SyncSnapshot{
		Connections: []Connection{{Host: "snap-host", Port: 22, Username: "u"}},
	}
	data, _ := json.Marshal(snap)
	// SyncSnapshot 没有 format 字段，tryParseExportJSON 会失败，应回退到 tryParseSnapshotJSON
	parsed, err := cm.parseImportData(data, "")
	if err != nil {
		t.Fatalf("snapshot parse failed: %v", err)
	}
	if len(parsed.Connections) != 1 || parsed.Connections[0].Host != "snap-host" {
		t.Fatalf("snapshot data mismatch: %+v", parsed)
	}
}

func TestParseImportData_SyncSnapshotKeepsProxyNodes(t *testing.T) {
	cm := newTestConfigManager(t)
	snap := SyncSnapshot{
		Connections: []Connection{{Host: "snap-host", Port: 22, Username: "u", ProxyMode: "node", ProxyNodeID: "proxy-1"}},
		ProxyNodes:  []aitypes.AIProxyNode{{ID: "proxy-1", Name: "Proxy", Type: "socks5", Host: "127.0.0.1", Port: 1080}},
	}
	data, _ := json.Marshal(snap)
	parsed, err := cm.parseImportData(data, "")
	if err != nil {
		t.Fatalf("snapshot parse failed: %v", err)
	}
	if len(parsed.ProxyNodes) != 1 || parsed.ProxyNodes[0].ID != "proxy-1" {
		t.Fatalf("snapshot proxy nodes lost: %+v", parsed.ProxyNodes)
	}
}

func TestApplyImportReferenceMappings(t *testing.T) {
	conns := []Connection{{Host: "h", CredentialID: "cred-old", ProxyMode: "node", ProxyNodeID: "proxy-old"}}
	applyImportReferenceMappings(conns, map[string]string{"cred-old": "cred-new"}, map[string]string{"proxy-old": "proxy-new"})
	if conns[0].CredentialID != "cred-new" {
		t.Fatalf("credential id not remapped: %+v", conns[0])
	}
	if conns[0].ProxyNodeID != "proxy-new" {
		t.Fatalf("proxy node id not remapped: %+v", conns[0])
	}
}

// TestParseImportData_NeedPassword 无正确密码时返回 errNeedPassword
func TestParseImportData_NeedPassword(t *testing.T) {
	cm := newTestConfigManager(t)
	exp := buildConnectionsExport([]Connection{{Host: "h1"}}, []Credential{})
	encrypted, _ := cm.encryptExportData(exp, "correct")

	_, err := cm.parseImportData([]byte(encrypted), "")
	if !errors.Is(err, errNeedPassword) {
		t.Fatalf("expected errNeedPassword, got %v", err)
	}
	_, err = cm.parseImportData([]byte(encrypted), "wrong")
	if !errors.Is(err, errNeedPassword) {
		t.Fatalf("expected errNeedPassword with wrong password, got %v", err)
	}
}

// TestBuildImportTemplate 模板应包含 2 条样例且格式合法
func TestBuildImportTemplate(t *testing.T) {
	tmpl := buildImportTemplate("zh-CN")
	if len(tmpl.Connections) != 2 {
		t.Fatalf("expected 2 sample connections, got %d", len(tmpl.Connections))
	}
	// 应能被 tryParseSnapshotJSON 正确解析
	data, _ := json.Marshal(tmpl)
	parsed, ok := tryParseSnapshotJSON(data)
	if !ok {
		t.Fatal("template should parse as SyncSnapshot")
	}
	if len(parsed.Connections) != 2 {
		t.Fatalf("parsed template has %d connections", len(parsed.Connections))
	}
}
