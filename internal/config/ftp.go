package config

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
)

const (
	FTPModeExplicitTLS = "explicit_tls"
	FTPModePlain       = "plain"
)

func normalizeFTPMode(mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return FTPModeExplicitTLS, nil
	}
	if mode != FTPModeExplicitTLS && mode != FTPModePlain {
		return "", fmt.Errorf("不支持的 FTP 连接模式: %s", mode)
	}
	return mode, nil
}

type FTPConfig struct {
	Mode       string `json:"mode,omitempty"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	RemoteDir  string `json:"remoteDir"`
	MaxBackups int    `json:"maxBackups"`
}

func (c *ConfigManager) GetFTPConfig() *FTPConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getFTPConfigLocked()
}

// getFTPConfigLocked 读取 FTP 配置，调用方需持有 c.mu
func (c *ConfigManager) getFTPConfigLocked() *FTPConfig {
	ftpFile := filepath.Join(c.configDir, "ftp.json")
	data, err := os.ReadFile(ftpFile)
	if err != nil {
		return nil
	}
	var conf FTPConfig
	if err := json.Unmarshal(data, &conf); err != nil {
		return nil
	}
	conf.Username = c.decrypt(conf.Username)
	conf.Password = c.decrypt(conf.Password)
	mode, err := normalizeFTPMode(conf.Mode)
	if err != nil {
		return nil
	}
	conf.Mode = mode
	if conf.RemoteDir == "" {
		conf.RemoteDir = "/LumeTerm/"
	}
	if conf.Port == 0 {
		conf.Port = 21
	}
	return &conf
}

func (c *ConfigManager) SaveFTPConfig(config map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	existing := c.getFTPConfigLocked()

	username := config["username"]
	password := config["password"]
	if username == "" && existing != nil {
		username = existing.Username
	}
	if password == "" && existing != nil {
		password = existing.Password
	}

	port := 21
	if config["port"] != "" {
		fmt.Sscanf(config["port"], "%d", &port)
	}

	remoteDir := config["remoteDir"]
	if remoteDir == "" {
		remoteDir = "/LumeTerm/"
	}
	if !strings.HasPrefix(remoteDir, "/") {
		remoteDir = "/" + remoteDir
	}
	if !strings.HasSuffix(remoteDir, "/") {
		remoteDir += "/"
	}

	maxBackups := parseIntOrDefault(config["maxBackups"], 0)
	modeInput := config["mode"]
	if modeInput == "" && existing != nil {
		modeInput = existing.Mode
	}
	mode, err := normalizeFTPMode(modeInput)
	if err != nil {
		return err
	}

	conf := FTPConfig{
		Mode:       mode,
		Host:       config["host"],
		Port:       port,
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
	conf.Username = encUser
	conf.Password = encPass
	ftpFile := filepath.Join(c.configDir, "ftp.json")
	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ftp config: %w", err)
	}
	return atomicWriteFile(ftpFile, data, 0600)
}

type FTPCertificateInfo struct {
	Endpoint          string   `json:"endpoint"`
	Fingerprint       string   `json:"fingerprint"`
	PinnedFingerprint string   `json:"pinnedFingerprint,omitempty"`
	Subject           string   `json:"subject"`
	Issuer            string   `json:"issuer"`
	SerialNumber      string   `json:"serialNumber"`
	DNSNames          []string `json:"dnsNames"`
	IPAddresses       []string `json:"ipAddresses"`
	NotBefore         string   `json:"notBefore"`
	NotAfter          string   `json:"notAfter"`
}

type FTPConnectionTestResult struct {
	Success                     bool                `json:"success"`
	CertificateApprovalRequired *FTPCertificateInfo `json:"certificateApprovalRequired,omitempty"`
}

type ftpTLSPin struct {
	CertificateDER string `json:"certificateDer"`
	Fingerprint    string `json:"fingerprint"`
	ApprovedAt     string `json:"approvedAt"`
}

type ftpTLSPinStore struct {
	Version int                  `json:"version"`
	Pins    map[string]ftpTLSPin `json:"pins"`
}

var ftpTLSPinsMu sync.Mutex

func normalizeFTPEndpoint(host string, port int) (string, string, error) {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" || port <= 0 || port > 65535 {
		return "", "", fmt.Errorf("FTP 主机或端口无效")
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host = strings.TrimSuffix(strings.ToLower(host), ".")
	}
	return host, net.JoinHostPort(host, fmt.Sprintf("%d", port)), nil
}

func ftpCertificateFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func (c *ConfigManager) ftpTLSPinsPath() string {
	return filepath.Join(c.configDir, "ftp_tls_pins.json")
}

func loadFTPTLSPinStore(path string) (*ftpTLSPinStore, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &ftpTLSPinStore{Version: 1, Pins: make(map[string]ftpTLSPin)}, nil
	}
	if err != nil {
		return nil, err
	}
	var store ftpTLSPinStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("解析 FTPS 证书信任存储失败: %w", err)
	}
	if store.Version != 1 || store.Pins == nil {
		return nil, fmt.Errorf("FTPS 证书信任存储格式无效")
	}
	return &store, nil
}

func validateFTPTLSPin(pin ftpTLSPin) (*x509.Certificate, error) {
	der, err := base64.StdEncoding.DecodeString(pin.CertificateDER)
	if err != nil {
		return nil, fmt.Errorf("FTPS 证书 pin 编码无效: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("FTPS 证书 pin 无效: %w", err)
	}
	if ftpCertificateFingerprint(cert) != pin.Fingerprint {
		return nil, fmt.Errorf("FTPS 证书 pin 指纹不一致")
	}
	return cert, nil
}

func (c *ConfigManager) loadFTPTLSPin(endpoint string) (*x509.Certificate, string, error) {
	ftpTLSPinsMu.Lock()
	defer ftpTLSPinsMu.Unlock()
	store, err := loadFTPTLSPinStore(c.ftpTLSPinsPath())
	if err != nil {
		return nil, "", err
	}
	pin, ok := store.Pins[endpoint]
	if !ok {
		return nil, "", nil
	}
	cert, err := validateFTPTLSPin(pin)
	return cert, pin.Fingerprint, err
}

func (c *ConfigManager) saveFTPTLSPin(endpoint, expectedFingerprint string, cert *x509.Certificate) error {
	ftpTLSPinsMu.Lock()
	defer ftpTLSPinsMu.Unlock()
	store, err := loadFTPTLSPinStore(c.ftpTLSPinsPath())
	if err != nil {
		return err
	}
	current := ""
	if pin, ok := store.Pins[endpoint]; ok {
		if _, err := validateFTPTLSPin(pin); err != nil {
			return err
		}
		current = pin.Fingerprint
	}
	if current != expectedFingerprint {
		return fmt.Errorf("FTPS 证书信任状态已变化，请重新测试")
	}
	store.Pins[endpoint] = ftpTLSPin{
		CertificateDER: base64.StdEncoding.EncodeToString(cert.Raw),
		Fingerprint:    ftpCertificateFingerprint(cert), ApprovedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(c.ftpTLSPinsPath(), data, 0600)
}

func buildFTPTLSConfig(serverName string, pinnedCert *x509.Certificate, observed **x509.Certificate) *tls.Config {
	config := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	if pinnedCert != nil {
		// 已明确固定证书时由 VerifyConnection 完成完整验证，以兼容没有 SAN、仅有精确匹配 CN 的旧证书。
		config.InsecureSkipVerify = true //nolint:gosec -- exact DER pin, validity, usage and endpoint identity are verified below.
		config.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 || !bytes.Equal(state.PeerCertificates[0].Raw, pinnedCert.Raw) {
				return wrapSyncTrustError("ftps_certificate_changed", fmt.Errorf("FTPS 服务器证书已变更"))
			}
			return validateApprovableFTPCertificate(state.PeerCertificates[0], serverName)
		}
	}
	if observed != nil {
		previous := config.VerifyConnection
		config.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) > 0 {
				*observed = state.PeerCertificates[0]
			}
			if previous != nil {
				return previous(state)
			}
			return nil
		}
	}
	return config
}

func dialAndLoginFTP(addr, username, password, mode string, tlsConfig *tls.Config) (*ftp.ServerConn, error) {
	mode, err := normalizeFTPMode(mode)
	if err != nil {
		return nil, err
	}
	options := []ftp.DialOption{ftp.DialWithTimeout(10 * time.Second)}
	if mode == FTPModeExplicitTLS {
		options = append(options, ftp.DialWithExplicitTLS(tlsConfig))
	}
	client, err := ftp.Dial(addr, options...)
	if err != nil {
		return nil, err
	}
	if err := client.Login(username, password); err != nil {
		client.Quit()
		return nil, err
	}
	return client, nil
}

func certificateFromVerificationError(err error) (*x509.Certificate, bool) {
	var verifyErr *tls.CertificateVerificationError
	if !errors.As(err, &verifyErr) || len(verifyErr.UnverifiedCertificates) == 0 {
		return nil, false
	}
	var unknownAuthority x509.UnknownAuthorityError
	if !errors.As(verifyErr.Err, &unknownAuthority) {
		return nil, false
	}
	return verifyErr.UnverifiedCertificates[0], true
}

func isUnknownAuthorityError(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return true
	}
	return strings.Contains(err.Error(), "x509: certificate signed by unknown authority")
}

// observeExplicitFTPSCertificate 仅执行 FTP greeting、AUTH TLS 和 TLS 握手，不发送用户名或密码。
func observeExplicitFTPSCertificate(endpoint, serverName string) (*x509.Certificate, error) {
	conn, err := net.DialTimeout("tcp", endpoint, 10*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, err
	}
	textConn := textproto.NewConn(conn)
	if _, _, err := textConn.ReadResponse(220); err != nil {
		return nil, err
	}
	if err := textConn.PrintfLine("AUTH TLS"); err != nil {
		return nil, err
	}
	if _, _, err := textConn.ReadResponse(234); err != nil {
		return nil, err
	}

	tlsConn := tls.Client(conn, &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}) //nolint:gosec -- observation only; certificate is validated before approval and never used to authenticate here.
	if err := tlsConn.Handshake(); err != nil {
		return nil, err
	}
	certificates := tlsConn.ConnectionState().PeerCertificates
	if len(certificates) == 0 {
		return nil, fmt.Errorf("FTPS 服务器未提供证书")
	}
	return certificates[0], nil
}

func validateApprovableFTPCertificate(cert *x509.Certificate, serverName string) error {
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	options := x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if _, err := cert.Verify(options); err != nil {
		return err
	}
	if len(cert.DNSNames) > 0 || len(cert.IPAddresses) > 0 {
		return cert.VerifyHostname(serverName)
	}
	// 兼容宝塔等旧式自签名证书：仅允许无 SAN 且 CN 与端点完全一致的证书。
	commonName := strings.TrimSpace(cert.Subject.CommonName)
	if ip := net.ParseIP(serverName); ip != nil {
		if commonName != ip.String() {
			return fmt.Errorf("证书 CN %q 与服务器 IP %q 不匹配", commonName, serverName)
		}
		return nil
	}
	if !strings.EqualFold(strings.TrimSuffix(commonName, "."), strings.TrimSuffix(serverName, ".")) {
		return fmt.Errorf("证书 CN %q 与服务器名称 %q 不匹配", commonName, serverName)
	}
	return nil
}

func ftpCertificateInfo(endpoint string, cert *x509.Certificate, pinnedFingerprint string) *FTPCertificateInfo {
	ips := make([]string, len(cert.IPAddresses))
	for i, ip := range cert.IPAddresses {
		ips[i] = ip.String()
	}
	return &FTPCertificateInfo{
		Endpoint: endpoint, Fingerprint: ftpCertificateFingerprint(cert), PinnedFingerprint: pinnedFingerprint,
		Subject: cert.Subject.String(), Issuer: cert.Issuer.String(), SerialNumber: cert.SerialNumber.String(),
		DNSNames: append([]string(nil), cert.DNSNames...), IPAddresses: ips,
		NotBefore: cert.NotBefore.Format(time.RFC3339), NotAfter: cert.NotAfter.Format(time.RFC3339),
	}
}

func (c *ConfigManager) TestFTPConnection(host string, port int, username, password, mode string) (*FTPConnectionTestResult, error) {
	serverName, endpoint, err := normalizeFTPEndpoint(host, port)
	if err != nil {
		return nil, err
	}
	mode, err = normalizeFTPMode(mode)
	if err != nil {
		return nil, err
	}
	if mode == FTPModePlain {
		client, err := dialAndLoginFTP(endpoint, username, password, mode, nil)
		if err != nil {
			return nil, fmt.Errorf("FTP 连接失败 %s: %w", endpoint, err)
		}
		client.Quit()
		return &FTPConnectionTestResult{Success: true}, nil
	}
	pin, pinnedFingerprint, err := c.loadFTPTLSPin(endpoint)
	if err != nil {
		return nil, err
	}
	client, err := dialAndLoginFTP(endpoint, username, password, FTPModeExplicitTLS, buildFTPTLSConfig(serverName, pin, nil))
	if err == nil {
		client.Quit()
		return &FTPConnectionTestResult{Success: true}, nil
	}
	connectionErr := err
	cert, ok := certificateFromVerificationError(connectionErr)
	trustReason, _ := syncTrustFailureReason(connectionErr)
	if !ok && (isUnknownAuthorityError(connectionErr) || trustReason == "ftps_certificate_changed") {
		var observeErr error
		cert, observeErr = observeExplicitFTPSCertificate(endpoint, serverName)
		if observeErr != nil {
			return nil, fmt.Errorf("FTP TLS 连接失败 %s: %w", endpoint, connectionErr)
		}
		ok = true
	}
	if !ok || validateApprovableFTPCertificate(cert, serverName) != nil {
		return nil, fmt.Errorf("FTP TLS 连接失败 %s: %w", endpoint, connectionErr)
	}
	return &FTPConnectionTestResult{CertificateApprovalRequired: ftpCertificateInfo(endpoint, cert, pinnedFingerprint)}, nil
}

func (c *ConfigManager) TestFTPConnectionWithCertificateApproval(host string, port int, username, password, mode, approvedFingerprint, expectedPinnedFingerprint string) (*FTPConnectionTestResult, error) {
	if normalizedMode, err := normalizeFTPMode(mode); err != nil || normalizedMode != FTPModeExplicitTLS {
		return nil, fmt.Errorf("只有显式 FTPS 可以确认服务器证书")
	}
	serverName, endpoint, err := normalizeFTPEndpoint(host, port)
	if err != nil {
		return nil, err
	}
	_, currentFingerprint, err := c.loadFTPTLSPin(endpoint)
	if err != nil {
		return nil, err
	}
	if currentFingerprint != expectedPinnedFingerprint {
		return nil, fmt.Errorf("FTPS 证书信任状态已变化，请重新测试")
	}

	var observed *x509.Certificate
	probeClient, err := dialAndLoginFTP(endpoint, username, password, FTPModeExplicitTLS, buildFTPTLSConfig(serverName, nil, &observed))
	if probeClient != nil {
		probeClient.Quit()
	}
	if err != nil {
		cert, ok := certificateFromVerificationError(err)
		if !ok && isUnknownAuthorityError(err) {
			cert, err = observeExplicitFTPSCertificate(endpoint, serverName)
			ok = err == nil
		}
		if !ok {
			return nil, fmt.Errorf("FTP TLS 连接失败 %s: %w", endpoint, err)
		}
		observed = cert
	}
	if observed == nil || ftpCertificateFingerprint(observed) != approvedFingerprint {
		return nil, fmt.Errorf("FTPS 服务器证书在确认后发生变化")
	}
	if err := validateApprovableFTPCertificate(observed, serverName); err != nil {
		return nil, fmt.Errorf("FTPS 服务器证书不可接受: %w", err)
	}
	client, err := dialAndLoginFTP(endpoint, username, password, FTPModeExplicitTLS, buildFTPTLSConfig(serverName, observed, nil))
	if err != nil {
		return nil, fmt.Errorf("FTP TLS 连接失败 %s: %w", endpoint, err)
	}
	client.Quit()
	if err := c.saveFTPTLSPin(endpoint, expectedPinnedFingerprint, observed); err != nil {
		return nil, err
	}
	return &FTPConnectionTestResult{Success: true}, nil
}

func (c *ConfigManager) newFTPClient() (*ftp.ServerConn, error) {
	conf := c.GetFTPConfig()
	if conf == nil {
		return nil, fmt.Errorf("FTP not configured")
	}
	serverName, endpoint, err := normalizeFTPEndpoint(conf.Host, conf.Port)
	if err != nil {
		return nil, err
	}
	var pin *x509.Certificate
	if conf.Mode == FTPModeExplicitTLS {
		pin, _, err = c.loadFTPTLSPin(endpoint)
		if err != nil {
			return nil, err
		}
	}
	client, err := dialAndLoginFTP(endpoint, conf.Username, conf.Password, conf.Mode, buildFTPTLSConfig(serverName, pin, nil))
	if err != nil {
		if conf.Mode == FTPModeExplicitTLS && isUnknownAuthorityError(err) {
			err = wrapSyncTrustError("ftps_certificate_untrusted", err)
		}
		label := "FTP"
		if conf.Mode == FTPModeExplicitTLS {
			label = "FTP TLS"
		}
		return nil, fmt.Errorf("%s 连接失败 %s: %w", label, endpoint, err)
	}
	return client, nil
}

func (c *ConfigManager) ensureFTPDir(client *ftp.ServerConn) error {
	conf := c.GetFTPConfig()
	if conf == nil {
		return fmt.Errorf("FTP not configured")
	}

	// Try to change to the remote directory first
	err := client.ChangeDir(conf.RemoteDir)
	if err == nil {
		return nil
	}

	// Directory doesn't exist, create it level by level
	parts := strings.Split(strings.Trim(conf.RemoteDir, "/"), "/")
	current := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		current += "/" + part
		err := client.ChangeDir(current)
		if err != nil {
			err = client.MakeDir(current)
			if err != nil {
				return fmt.Errorf("failed to create directory %s: %w", current, err)
			}
		}
	}
	// Final change to the target dir
	return client.ChangeDir(conf.RemoteDir)
}

// ─── FTP RemoteStorage 实现 ───────────────────────────────

type ftpStorage struct {
	c          *ConfigManager
	client     *ftp.ServerConn
	remoteDir  string
	maxBackups int
}

// Close 关闭底层 FTP 连接
func (s *ftpStorage) Close() error {
	if s.client != nil {
		return s.client.Quit()
	}
	return nil
}

func (s *ftpStorage) MaxBackups() int { return s.maxBackups }

// EnsureRemoteDir 重建 FTP 同步根目录。
func (s *ftpStorage) EnsureRemoteDir() error {
	if s.c == nil || s.client == nil {
		return fmt.Errorf("FTP 未初始化")
	}
	return s.c.ensureFTPDir(s.client)
}

func (s *ftpStorage) ListFiles() ([]RemoteFile, error) {
	// newFTPStorage 已切换到 remoteDir；列出当前目录可兼容不支持 MLSD/LIST 绝对路径的服务器。
	entries, err := s.client.List("")
	if err != nil {
		return nil, err
	}
	var result []RemoteFile
	for _, e := range entries {
		result = append(result, RemoteFile{
			Name:    e.Name,
			ModTime: e.Time,
			IsDir:   e.Type == ftp.EntryTypeFolder,
			Size:    int64(e.Size),
		})
	}
	return result, nil
}

func (s *ftpStorage) ReadFile(name string) ([]byte, error) {
	path := strings.TrimRight(s.remoteDir, "/") + "/" + name
	resp, err := s.client.Retr(path)
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp)
	return buf.Bytes(), err
}

func (s *ftpStorage) WriteFile(name string, data []byte) error {
	// 目录在 newFTPStorage 时已确保创建，此处无需每次检查
	path := strings.TrimRight(s.remoteDir, "/") + "/" + name
	return s.client.Stor(path, bytes.NewReader(data))
}

func (s *ftpStorage) DeleteFile(name string) error {
	path := strings.TrimRight(s.remoteDir, "/") + "/" + name
	return s.client.Delete(path)
}

func (c *ConfigManager) newFTPStorage() (RemoteStorage, int, error) {
	conf := c.GetFTPConfig()
	if conf == nil {
		return nil, 0, fmt.Errorf("FTP not configured")
	}
	client, err := c.newFTPClient()
	if err != nil {
		return nil, 0, err
	}
	// 创建存储对象后立即确保远程目录存在，避免每次 WriteFile 都检查
	if err := c.ensureFTPDir(client); err != nil {
		client.Quit()
		return nil, 0, err
	}
	return &ftpStorage{
		c:          c,
		client:     client,
		remoteDir:  conf.RemoteDir,
		maxBackups: conf.MaxBackups,
	}, conf.MaxBackups, nil
}

// BackupToFTP 备份到 FTP
func (c *ConfigManager) BackupToFTP() (map[string]interface{}, error) {
	return c.backupTo("ftp", c.newFTPStorage)
}

// ListFTPBackups 列出 FTP 备份
func (c *ConfigManager) ListFTPBackups() ([]map[string]interface{}, error) {
	return c.listBackupsFrom(c.newFTPStorage)
}

// SyncFromFTP 手动合并同步
func (c *ConfigManager) SyncFromFTP() (map[string]interface{}, error) {
	return c.syncFrom("ftp", c.newFTPStorage)
}

func (c *ConfigManager) RestoreFromFTPFile(filename string) (map[string]interface{}, error) {
	return c.restoreFrom("ftp", c.newFTPStorage, filename, c.GetRecoveryPassword())
}

// RestoreFromFTPFileWithPassword 用用户输入的密码恢复（恢复失败时的兜底入口）。
func (c *ConfigManager) RestoreFromFTPFileWithPassword(filename string, password string) (map[string]interface{}, error) {
	return c.restoreFrom("ftp", c.newFTPStorage, filename, password)
}
