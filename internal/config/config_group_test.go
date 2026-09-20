package config

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

func testConfigManagerForGroup(t *testing.T) *ConfigManager {
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
	return &ConfigManager{
		configDir: dir,
		connFile:  filepath.Join(dir, "connections.json"),
		key:       key,
		gcm:       gcm,
	}
}

// 明文存储策略自检：encrypt 恒等；decrypt 兼容旧密文（hex+GCM）与明文（含 hex 形明文）。
func TestPlaintextEncryptDecryptCompat(t *testing.T) {
	c := testConfigManagerForGroup(t)

	// encrypt 恒等
	for _, s := range []string{"", "pass", "密码🔑", "123456"} {
		got, err := c.encrypt(s)
		if err != nil || got != s {
			t.Fatalf("encrypt(%q)=%q err=%v，应为恒等", s, got, err)
		}
	}

	// decrypt：明文原样返回
	for _, s := range []string{"pass", "密码🔑", "123456", "-----BEGIN OPENSSH PRIVATE KEY-----\nabc"} {
		if got := c.decrypt(s); got != s {
			t.Fatalf("decrypt(明文 %q)=%q，应原样返回", s, got)
		}
	}

	// decrypt：旧密文（手工按旧格式 Seal）解出明文
	plain := "legacy-secret"
	nonce := make([]byte, c.gcm.NonceSize())
	ct := c.gcm.Seal(nonce, nonce, []byte(plain), nil)
	if got := c.decrypt(hex.EncodeToString(ct)); got != plain {
		t.Fatalf("decrypt(旧密文)=%q，应解出 %q", got, plain)
	}
}

func TestRenameConnectionGroup_Success(t *testing.T) {
	cm := testConfigManagerForGroup(t)
	cm.mu.Lock()
	if err := cm.saveConnectionsFile([]Connection{
		{ID: "1", Host: "a", Port: 22, Username: "u", Group: "prod"},
		{ID: "2", Host: "b", Port: 22, Username: "u", Group: "prod"},
		{ID: "3", Host: "c", Port: 22, Username: "u", Group: "dev"},
	}); err != nil {
		cm.mu.Unlock()
		t.Fatal(err)
	}
	cm.connCacheDirty = true
	cm.mu.Unlock()

	if err := cm.RenameConnectionGroup("prod", "production"); err != nil {
		t.Fatal(err)
	}
	got := cm.GetConnections()
	byID := map[string]Connection{}
	for _, c := range got {
		byID[c.ID] = c
	}
	if byID["1"].Group != "production" || byID["2"].Group != "production" {
		t.Fatalf("expected prod servers renamed, got %+v %+v", byID["1"], byID["2"])
	}
	if byID["3"].Group != "dev" {
		t.Fatalf("dev group should be unchanged, got %q", byID["3"].Group)
	}
	if byID["1"].LastModified == 0 {
		t.Fatal("LastModified should be set on renamed connections")
	}
}

func TestRenameConnectionGroup_DuplicateRejected(t *testing.T) {
	cm := testConfigManagerForGroup(t)
	cm.mu.Lock()
	if err := cm.saveConnectionsFile([]Connection{
		{ID: "1", Host: "a", Port: 22, Username: "u", Group: "prod"},
		{ID: "2", Host: "b", Port: 22, Username: "u", Group: "dev"},
	}); err != nil {
		cm.mu.Unlock()
		t.Fatal(err)
	}
	cm.connCacheDirty = true
	cm.mu.Unlock()

	err := cm.RenameConnectionGroup("prod", "dev")
	if err == nil || !strings.Contains(err.Error(), "分组名称已存在") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	got := cm.GetConnections()
	for _, c := range got {
		if c.ID == "1" && c.Group != "prod" {
			t.Fatalf("group should be unchanged on failure, got %q", c.Group)
		}
	}
}

func TestRenameConnectionGroup_EmptyNewRejected(t *testing.T) {
	cm := testConfigManagerForGroup(t)
	err := cm.RenameConnectionGroup("prod", "   ")
	if err == nil || !strings.Contains(err.Error(), "分组名称不能为空") {
		t.Fatalf("expected empty name error, got %v", err)
	}
}

func TestRenameConnectionGroup_EmptyOldRejected(t *testing.T) {
	cm := testConfigManagerForGroup(t)
	err := cm.RenameConnectionGroup("", "prod")
	if err == nil || !strings.Contains(err.Error(), "不能重命名未分组") {
		t.Fatalf("expected ungrouped error, got %v", err)
	}
}

func TestRenameConnectionGroup_SameNameNoop(t *testing.T) {
	cm := testConfigManagerForGroup(t)
	cm.mu.Lock()
	if err := cm.saveConnectionsFile([]Connection{
		{ID: "1", Host: "a", Port: 22, Username: "u", Group: "prod"},
	}); err != nil {
		cm.mu.Unlock()
		t.Fatal(err)
	}
	cm.connCacheDirty = true
	cm.mu.Unlock()

	if err := cm.RenameConnectionGroup("prod", " prod "); err != nil {
		t.Fatal(err)
	}
	got := cm.GetConnections()
	if len(got) != 1 || got[0].Group != "prod" {
		t.Fatalf("same-name rename should leave data unchanged: %+v", got)
	}
}

func TestRenameConnectionGroup_NoMatchingGroupNoop(t *testing.T) {
	cm := testConfigManagerForGroup(t)
	cm.mu.Lock()
	if err := cm.saveConnectionsFile([]Connection{
		{ID: "1", Host: "a", Port: 22, Username: "u", Group: "dev"},
	}); err != nil {
		cm.mu.Unlock()
		t.Fatal(err)
	}
	cm.connCacheDirty = true
	cm.mu.Unlock()

	if err := cm.RenameConnectionGroup("missing", "other"); err != nil {
		t.Fatal(err)
	}
	got := cm.GetConnections()
	if len(got) != 1 || got[0].Group != "dev" {
		t.Fatalf("unexpected change: %+v", got)
	}
}
