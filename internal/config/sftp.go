package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// sshHostKeyAlgorithms SSH 主机密钥算法列表。
// ponytail: 与 ssh.go 中的 sshHostKeyAlgorithms 重复；config 簇迁移后 sftp.go 需要独立引用。
// 升级路径：下沉到共享 sshutil 包后两处统一。
var sshHostKeyAlgorithms = []string{
	"ssh-ed25519",
	"ecdsa-sha2-nistp256",
	"ecdsa-sha2-nistp384",
	"ecdsa-sha2-nistp521",
	"rsa-sha2-512",
	"rsa-sha2-256",
}

type SFTPConfig struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	AuthMethod string `json:"authMethod"` // "password" 或 "key"
	Password   string `json:"password"`
	PrivateKey string `json:"privateKey"`
	Passphrase string `json:"passphrase,omitempty"`
	RemoteDir  string `json:"remoteDir"`
	MaxBackups int    `json:"maxBackups"`
}

func (c *ConfigManager) GetSFTPConfig() *SFTPConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getSFTPConfigLocked()
}

// getSFTPConfigLocked 读取 SFTP 配置，调用方需持有 c.mu
func (c *ConfigManager) getSFTPConfigLocked() *SFTPConfig {
	sftpFile := filepath.Join(c.configDir, "sftp.json")
	data, err := os.ReadFile(sftpFile)
	if err != nil {
		return nil
	}
	var conf SFTPConfig
	if err := json.Unmarshal(data, &conf); err != nil {
		return nil
	}
	conf.Username = c.decrypt(conf.Username)
	conf.Password = c.decrypt(conf.Password)
	conf.PrivateKey = c.decrypt(conf.PrivateKey)
	if conf.Port == 0 {
		conf.Port = 22
	}
	if conf.RemoteDir == "" {
		conf.RemoteDir = "/LumeTerm/"
	}
	if conf.RemoteDir[len(conf.RemoteDir)-1] != '/' {
		conf.RemoteDir += "/"
	}
	return &conf
}

func (c *ConfigManager) SaveSFTPConfig(config map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	existing := c.getSFTPConfigLocked()

	username := config["username"]
	password := config["password"]
	privateKey := config["privateKey"]
	if username == "" && existing != nil {
		username = existing.Username
	}
	if password == "" && existing != nil {
		password = existing.Password
	}
	if privateKey == "" && existing != nil {
		privateKey = existing.PrivateKey
	}

	port := 22
	if p, ok := config["port"]; ok && p != "" {
		fmt.Sscanf(p, "%d", &port)
	}

	remoteDir := config["remoteDir"]
	if remoteDir == "" {
		remoteDir = "/LumeTerm/"
	}
	if remoteDir[len(remoteDir)-1] != '/' {
		remoteDir += "/"
	}

	maxBackups := parseIntOrDefault(config["maxBackups"], 0)

	conf := SFTPConfig{
		Host:       config["host"],
		Port:       port,
		AuthMethod: config["authMethod"],
		RemoteDir:  remoteDir,
		MaxBackups: maxBackups,
	}

	encUser, err := c.encrypt(username)
	if err != nil {
		return fmt.Errorf("encrypt username: %w", err)
	}
	encPass, err := c.encrypt(password)
	if err != nil {
		return fmt.Errorf("encrypt password: %w", err)
	}
	encKey, err := c.encrypt(privateKey)
	if err != nil {
		return fmt.Errorf("encrypt private key: %w", err)
	}
	conf.Username = encUser
	conf.Password = encPass
	conf.PrivateKey = encKey
	sftpFile := filepath.Join(c.configDir, "sftp.json")
	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sftp config: %w", err)
	}
	return atomicWriteFile(sftpFile, data, 0600)
}

type SFTPHostKeyMismatch struct {
	Hostname        string   `json:"hostname"`
	NewFingerprint  string   `json:"newFingerprint"`
	OldFingerprints []string `json:"oldFingerprints"`
	KnownHostsPath  string   `json:"knownHostsPath"`
}

type SFTPConnectionTestResult struct {
	Success         bool                 `json:"success"`
	HostKeyMismatch *SFTPHostKeyMismatch `json:"hostKeyMismatch,omitempty"`
}

type syncTrustError struct {
	reason string
	err    error
}

func (e *syncTrustError) Error() string       { return e.err.Error() }
func (e *syncTrustError) Unwrap() error       { return e.err }
func (e *syncTrustError) TrustReason() string { return e.reason }

func wrapSyncTrustError(reason string, err error) error {
	return &syncTrustError{reason: reason, err: err}
}

func syncTrustFailureReason(err error) (string, bool) {
	var trustErr interface{ TrustReason() string }
	if errors.As(err, &trustErr) {
		return trustErr.TrustReason(), true
	}
	return "", false
}

type sftpHostKeyObservation struct {
	hostname string
	key      ssh.PublicKey
	want     []knownhosts.KnownKey
}

var sftpKnownHostsMu sync.Mutex

// sftpHostKeyCallback 返回基于 known_hosts 的 TOFU（首次信任）主机密钥校验回调。
// 首次连接时自动将主机密钥写入 known_hosts；后续连接若密钥不匹配则拒绝。
func sftpHostKeyCallback() ssh.HostKeyCallback {
	return sftpHostKeyCallbackForPath(getKnownHostsPath(), "", nil)
}

func sftpHostKeyCallbackForPath(knownHostsPath, approvedFingerprint string, observation *sftpHostKeyObservation) ssh.HostKeyCallback {
	if err := os.MkdirAll(filepath.Dir(knownHostsPath), 0700); err != nil {
		return func(string, net.Addr, ssh.PublicKey) error { return err }
	}
	if f, err := os.OpenFile(knownHostsPath, os.O_CREATE|os.O_RDONLY, 0600); err != nil {
		return func(string, net.Addr, ssh.PublicKey) error { return err }
	} else {
		f.Close()
	}
	cb, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return func(string, net.Addr, ssh.PublicKey) error { return err }
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := cb(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if !errors.As(err, &keyErr) {
			return err
		}
		if len(keyErr.Want) == 0 {
			log.Printf("[sftpHostKeyCallback] TOFU: 自动接受 %s 的新主机密钥 (fingerprint: %s)", hostname, ssh.FingerprintSHA256(key))
			line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
			sftpKnownHostsMu.Lock()
			defer sftpKnownHostsMu.Unlock()
			f, openErr := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_WRONLY, 0600)
			if openErr != nil {
				return openErr
			}
			if _, writeErr := f.WriteString(line + "\n"); writeErr != nil {
				f.Close()
				return writeErr
			}
			return f.Close()
		}

		if observation != nil {
			observation.hostname = hostname
			observation.key = key
			observation.want = append([]knownhosts.KnownKey(nil), keyErr.Want...)
		}
		if approvedFingerprint != "" && ssh.FingerprintSHA256(key) == approvedFingerprint {
			return nil
		}
		return wrapSyncTrustError("sftp_host_key_changed", err)
	}
}

// buildSSHConfig 构建 SFTP 用的 SSH 配置，复用于 TestSFTPConnection 和 newSFTPClient
func buildSSHConfig(username, password, authMethod, privateKey, passphrase string) (*ssh.ClientConfig, error) {
	return buildSSHConfigWithHostKeyCallback(username, password, authMethod, privateKey, passphrase, sftpHostKeyCallback())
}

func buildSSHConfigWithHostKeyCallback(username, password, authMethod, privateKey, passphrase string, hostKeyCallback ssh.HostKeyCallback) (*ssh.ClientConfig, error) {
	sshConfig := &ssh.ClientConfig{
		User:              username,
		HostKeyCallback:   hostKeyCallback,
		HostKeyAlgorithms: append([]string(nil), sshHostKeyAlgorithms...),
		Timeout:           10 * time.Second,
	}
	if authMethod == "key" {
		var signer ssh.Signer
		var err error
		if passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(privateKey), []byte(passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(privateKey))
		}
		if err != nil {
			return nil, fmt.Errorf("解析私钥失败：%w", err)
		}
		sshConfig.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	} else {
		// keyboard-interactive 优先，因为部分 SFTP 服务器不提供 password 方法。
		sshConfig.Auth = []ssh.AuthMethod{
			ssh.KeyboardInteractive(sftpPasswordChallenge(password)),
			ssh.Password(password),
		}
	}
	return sshConfig, nil
}

func sftpPasswordChallenge(password string) ssh.KeyboardInteractiveChallenge {
	return func(user, instruction string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i := range answers {
			answers[i] = password
		}
		return answers, nil
	}
}

func testSFTPConnection(host string, port int, sshConfig *ssh.ClientConfig) error {
	sshClient, err := ssh.Dial("tcp", dialAddr(host, port), sshConfig)
	if err != nil {
		return fmt.Errorf("SSH 连接失败：%w", err)
	}
	defer sshClient.Close()

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("SFTP 初始化失败：%w", err)
	}
	defer sftpClient.Close()
	if _, err := sftpClient.ReadDir("/"); err != nil {
		return fmt.Errorf("读取根目录失败：%w", err)
	}
	return nil
}

func sftpMismatchResult(observation *sftpHostKeyObservation, knownHostsPath string) *SFTPConnectionTestResult {
	oldFingerprints := make([]string, 0, len(observation.want))
	for _, knownKey := range observation.want {
		oldFingerprints = append(oldFingerprints, ssh.FingerprintSHA256(knownKey.Key))
	}
	return &SFTPConnectionTestResult{HostKeyMismatch: &SFTPHostKeyMismatch{
		Hostname: observation.hostname, NewFingerprint: ssh.FingerprintSHA256(observation.key),
		OldFingerprints: oldFingerprints, KnownHostsPath: knownHostsPath,
	}}
}

func (c *ConfigManager) TestSFTPConnection(host string, port int, username, password, authMethod, privateKey, passphrase string) (*SFTPConnectionTestResult, error) {
	knownHostsPath := getKnownHostsPath()
	observation := &sftpHostKeyObservation{}
	callback := sftpHostKeyCallbackForPath(knownHostsPath, "", observation)
	sshConfig, err := buildSSHConfigWithHostKeyCallback(username, password, authMethod, privateKey, passphrase, callback)
	if err != nil {
		return nil, err
	}
	if err := testSFTPConnection(host, port, sshConfig); err != nil {
		if observation.key != nil && len(observation.want) > 0 {
			return sftpMismatchResult(observation, knownHostsPath), nil
		}
		return nil, err
	}
	return &SFTPConnectionTestResult{Success: true}, nil
}

func (c *ConfigManager) TestSFTPConnectionWithHostKeyApproval(host string, port int, username, password, authMethod, privateKey, passphrase, approvedFingerprint string) (*SFTPConnectionTestResult, error) {
	knownHostsPath := getKnownHostsPath()
	observation := &sftpHostKeyObservation{}
	callback := sftpHostKeyCallbackForPath(knownHostsPath, approvedFingerprint, observation)
	sshConfig, err := buildSSHConfigWithHostKeyCallback(username, password, authMethod, privateKey, passphrase, callback)
	if err != nil {
		return nil, err
	}
	if err := testSFTPConnection(host, port, sshConfig); err != nil {
		if observation.key != nil && ssh.FingerprintSHA256(observation.key) != approvedFingerprint {
			return nil, fmt.Errorf("服务器主机密钥在确认后再次发生变化")
		}
		return nil, err
	}
	if observation.key != nil {
		if ssh.FingerprintSHA256(observation.key) != approvedFingerprint {
			return nil, fmt.Errorf("服务器主机密钥在确认后再次发生变化")
		}
		if err := replaceSFTPKnownHostKey(knownHostsPath, observation); err != nil {
			return nil, err
		}
	}
	return &SFTPConnectionTestResult{Success: true}, nil
}

func replaceSFTPKnownHostKey(knownHostsPath string, observation *sftpHostKeyObservation) error {
	sftpKnownHostsMu.Lock()
	defer sftpKnownHostsMu.Unlock()

	data, err := os.ReadFile(knownHostsPath)
	if err != nil {
		return fmt.Errorf("无法读取 known_hosts: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	normalizedHostname := knownhosts.Normalize(observation.hostname)
	removeLines := make(map[int]bool)
	for _, knownKey := range observation.want {
		if knownKey.Filename != knownHostsPath || knownKey.Line <= 0 || knownKey.Line > len(lines) {
			return fmt.Errorf("known_hosts 条目无法安全更新，请手动处理 %s", knownHostsPath)
		}
		fields := strings.Fields(lines[knownKey.Line-1])
		if len(fields) < 3 || strings.HasPrefix(fields[0], "@") || fields[0] != normalizedHostname {
			return fmt.Errorf("known_hosts 条目无法安全更新，请手动处理 %s", knownHostsPath)
		}
		expectedKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(knownKey.Key)))
		if !strings.Contains(strings.Join(fields[1:], " "), expectedKey) {
			return fmt.Errorf("known_hosts 已发生变化，请重新测试连接")
		}
		removeLines[knownKey.Line-1] = true
	}

	updated := make([]string, 0, len(lines)+1)
	for i, line := range lines {
		if !removeLines[i] && (i < len(lines)-1 || line != "") {
			updated = append(updated, line)
		}
	}
	updated = append(updated, knownhosts.Line([]string{normalizedHostname}, observation.key))
	if err := atomicWriteFile(knownHostsPath, []byte(strings.Join(updated, "\n")+"\n"), 0600); err != nil {
		return fmt.Errorf("无法写入 known_hosts: %w", err)
	}
	return nil
}

func (c *ConfigManager) newSFTPClient() (*sftp.Client, *ssh.Client, error) {
	conf := c.GetSFTPConfig()
	if conf == nil {
		return nil, nil, fmt.Errorf("SFTP not configured")
	}

	sshConfig, err := buildSSHConfig(conf.Username, conf.Password, conf.AuthMethod, conf.PrivateKey, conf.Passphrase)
	if err != nil {
		return nil, nil, err
	}

	sshClient, err := ssh.Dial("tcp", dialAddr(conf.Host, conf.Port), sshConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH 连接失败：%w", err)
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, nil, fmt.Errorf("SFTP 初始化失败：%w", err)
	}

	return sftpClient, sshClient, nil
}

func ensureSFTPDir(client *sftp.Client, remoteDir string) error {
	_, err := client.Stat(remoteDir)
	if err != nil {
		err = client.MkdirAll(remoteDir)
		if err != nil {
			return fmt.Errorf("创建远程目录失败：%w", err)
		}
	}
	return nil
}

// ─── SFTP RemoteStorage 实现 ──────────────────────────────

type sftpStorage struct {
	c          *ConfigManager
	client     *sftp.Client
	sshClient  *ssh.Client
	remoteDir  string
	maxBackups int
}

// Close 关闭底层 SFTP 与 SSH 连接
func (s *sftpStorage) Close() error {
	var err1, err2 error
	if s.client != nil {
		err1 = s.client.Close()
	}
	if s.sshClient != nil {
		err2 = s.sshClient.Close()
	}
	if err1 != nil {
		return err1
	}
	return err2
}

func (s *sftpStorage) MaxBackups() int { return s.maxBackups }

// EnsureRemoteDir 重建 SFTP 同步根目录。
func (s *sftpStorage) EnsureRemoteDir() error {
	return ensureSFTPDir(s.client, s.remoteDir)
}

func (s *sftpStorage) ListFiles() ([]RemoteFile, error) {
	files, err := s.client.ReadDir(s.remoteDir)
	if err != nil {
		return nil, err
	}
	var result []RemoteFile
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

func (s *sftpStorage) ReadFile(name string) ([]byte, error) {
	path := strings.TrimSuffix(s.remoteDir, "/") + "/" + name
	f, err := s.client.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(f)
	return buf.Bytes(), err
}

func (s *sftpStorage) WriteFile(name string, data []byte) error {
	if err := ensureSFTPDir(s.client, s.remoteDir); err != nil {
		return err
	}

	path := strings.TrimSuffix(s.remoteDir, "/") + "/" + name
	f, err := s.client.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(data)
	if err != nil {
		return err
	}
	return f.Close()
}

func (s *sftpStorage) DeleteFile(name string) error {
	path := strings.TrimSuffix(s.remoteDir, "/") + "/" + name
	return s.client.Remove(path)
}

func (c *ConfigManager) newSFTPStorage() (RemoteStorage, int, error) {
	conf := c.GetSFTPConfig()
	if conf == nil {
		return nil, 0, fmt.Errorf("SFTP not configured")
	}
	client, sshClient, err := c.newSFTPClient()
	if err != nil {
		return nil, 0, err
	}
	return &sftpStorage{
		c:          c,
		client:     client,
		sshClient:  sshClient,
		remoteDir:  conf.RemoteDir,
		maxBackups: conf.MaxBackups,
	}, conf.MaxBackups, nil
}

// BackupToSFTP 备份到 SFTP
func (c *ConfigManager) BackupToSFTP() (map[string]interface{}, error) {
	return c.backupTo("sftp", c.newSFTPStorage)
}

// ListSFTPBackups 列出 SFTP 备份
func (c *ConfigManager) ListSFTPBackups() ([]map[string]interface{}, error) {
	return c.listBackupsFrom(c.newSFTPStorage)
}

// SyncFromSFTP 手动合并同步
func (c *ConfigManager) SyncFromSFTP() (map[string]interface{}, error) {
	return c.syncFrom("sftp", c.newSFTPStorage)
}

func (c *ConfigManager) RestoreFromSFTPFile(filename string) (map[string]interface{}, error) {
	return c.restoreFrom("sftp", c.newSFTPStorage, filename, c.GetRecoveryPassword())
}

// RestoreFromSFTPFileWithPassword 用用户输入的密码恢复（恢复失败时的兜底入口）。
func (c *ConfigManager) RestoreFromSFTPFileWithPassword(filename string, password string) (map[string]interface{}, error) {
	return c.restoreFrom("sftp", c.newSFTPStorage, filename, password)
}
