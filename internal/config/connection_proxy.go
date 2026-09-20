package config

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	aitypes "lumeterm/internal/aitypes"

	xproxy "golang.org/x/net/proxy"
)

type bufferedNetConn struct {
	net.Conn
	reader io.Reader
}

func (c *bufferedNetConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func normalizeConnectionProxyMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "node":
		return "node"
	case "custom":
		return "custom"
	default:
		return "direct"
	}
}

func normalizeConnectionProxyType(value string) string {
	if strings.ToLower(strings.TrimSpace(value)) == "http" {
		return "http"
	}
	return "socks5"
}

func normalizeConnectionProxyPort(port int) int {
	if port > 0 && port <= 65535 {
		return port
	}
	return 1080
}

func sanitizeConnectionProxyConfig(conn *Connection) {
	if conn == nil {
		return
	}
	conn.ProxyMode = normalizeConnectionProxyMode(conn.ProxyMode)
	conn.ProxyNodeID = strings.TrimSpace(conn.ProxyNodeID)
	conn.ProxyType = normalizeConnectionProxyType(conn.ProxyType)
	conn.ProxyHost = strings.TrimSpace(conn.ProxyHost)
	conn.ProxyUsername = strings.TrimSpace(conn.ProxyUsername)

	switch conn.ProxyMode {
	case "direct":
		conn.ProxyNodeID = ""
		conn.ProxyType = ""
		conn.ProxyHost = ""
		conn.ProxyPort = 0
		conn.ProxyUsername = ""
		conn.ProxyPassword = ""
	case "node":
		conn.ProxyType = ""
		conn.ProxyHost = ""
		conn.ProxyPort = 0
		conn.ProxyUsername = ""
		conn.ProxyPassword = ""
	case "custom":
		conn.ProxyNodeID = ""
		conn.ProxyPort = normalizeConnectionProxyPort(conn.ProxyPort)
	}
}

// normalizeConnectionForSync 同步比较/上传用的连接规范化。
// 与安卓 Connection.normalizedForSync 对齐：trim 空字段、proxyMode=direct 清掉无意义默认 proxy*，
// 避免「PC omitempty vs 安卓写 socks5/1080 空串」被 connsEqual 当成真变更。
func normalizeConnectionForSync(conn Connection) Connection {
	conn.ID = strings.TrimSpace(conn.ID)
	conn.Name = strings.TrimSpace(conn.Name)
	conn.Host = strings.TrimSpace(conn.Host)
	conn.Username = strings.TrimSpace(conn.Username)
	conn.Password = strings.TrimSpace(conn.Password)
	conn.AuthMethod = strings.TrimSpace(conn.AuthMethod)
	conn.PrivateKey = strings.TrimSpace(conn.PrivateKey)
	conn.Passphrase = strings.TrimSpace(conn.Passphrase)
	conn.Group = strings.TrimSpace(conn.Group)
	conn.Os = strings.TrimSpace(conn.Os)
	conn.CredentialID = strings.TrimSpace(conn.CredentialID)
	conn.TerminalInitPath = strings.TrimSpace(conn.TerminalInitPath)
	conn.FileManagerInitPath = strings.TrimSpace(conn.FileManagerInitPath)
	// 空值归一到 utf-8，与本地读取路径一致；否则远端缺字段会被判成真变更，反复上传
	conn.TerminalEncoding = normalizeTerminalEncoding(conn.TerminalEncoding)
	sanitizeConnectionProxyConfig(&conn)
	return conn
}

// normalizeCredentialForSync 同步比较/上传用的凭据规范化（与安卓 Credential.normalizedForSync 对齐）。
func normalizeCredentialForSync(cred Credential) Credential {
	cred.ID = strings.TrimSpace(cred.ID)
	cred.Name = strings.TrimSpace(cred.Name)
	cred.AuthMethod = strings.TrimSpace(cred.AuthMethod)
	cred.Username = strings.TrimSpace(cred.Username)
	cred.Password = strings.TrimSpace(cred.Password)
	cred.PrivateKey = strings.TrimSpace(cred.PrivateKey)
	cred.Passphrase = strings.TrimSpace(cred.Passphrase)
	return cred
}

func normalizeConnectionsForSync(conns []Connection) []Connection {
	if conns == nil {
		return []Connection{}
	}
	out := make([]Connection, len(conns))
	for i := range conns {
		out[i] = normalizeConnectionForSync(conns[i])
	}
	return out
}

func normalizeCredentialsForSync(creds []Credential) []Credential {
	if creds == nil {
		return []Credential{}
	}
	out := make([]Credential, len(creds))
	for i := range creds {
		out[i] = normalizeCredentialForSync(creds[i])
	}
	return out
}

func (c *ConfigManager) GetProxyNodes() []aitypes.AIProxyNode {
	if c == nil {
		return nil
	}
	return aitypes.LoadAIProxyNodes(c.configDir)
}

func (c *ConfigManager) ResolveConnectionRuntime(conn Connection) (Connection, error) {
	conn = c.ResolveConnectionAuth(conn)
	return c.ResolveConnectionProxy(conn)
}

func (c *ConfigManager) ResolveConnectionProxy(conn Connection) (Connection, error) {
	sanitizeConnectionProxyConfig(&conn)

	switch conn.ProxyMode {
	case "direct":
		return conn, nil
	case "custom":
		if conn.ProxyHost == "" {
			return conn, fmt.Errorf("代理主机地址不能为空")
		}
		conn.ProxyType = normalizeConnectionProxyType(conn.ProxyType)
		conn.ProxyPort = normalizeConnectionProxyPort(conn.ProxyPort)
		return conn, nil
	case "node":
		if c == nil {
			return conn, fmt.Errorf("当前环境无法解析代理节点")
		}
		if conn.ProxyNodeID == "" {
			return conn, fmt.Errorf("未选择代理节点")
		}
		for _, node := range c.GetProxyNodes() {
			if strings.TrimSpace(node.ID) != conn.ProxyNodeID {
				continue
			}
			conn.ProxyMode = "custom"
			conn.ProxyType = normalizeConnectionProxyType(node.Type)
			conn.ProxyHost = strings.TrimSpace(node.Host)
			conn.ProxyPort = normalizeConnectionProxyPort(node.Port)
			conn.ProxyUsername = strings.TrimSpace(node.Username)
			conn.ProxyPassword = node.Password
			return conn, nil
		}
		return conn, fmt.Errorf("所选代理节点不存在或已被删除")
	default:
		return conn, nil
	}
}

func connectionUsesProxy(conn Connection) bool {
	return normalizeConnectionProxyMode(conn.ProxyMode) == "custom" && strings.TrimSpace(conn.ProxyHost) != ""
}

func dialConnectionTargetContext(ctx context.Context, conn Connection, target string, timeout time.Duration) (net.Conn, error) {
	if !connectionUsesProxy(conn) {
		// 显式 TCP KeepAlive，减轻空闲被 NAT/中间设备静默丢连接。
		d := net.Dialer{
			Timeout:   timeout,
			KeepAlive: 30 * time.Second,
		}
		return d.DialContext(ctx, "tcp", target)
	}

	switch normalizeConnectionProxyType(conn.ProxyType) {
	case "http":
		return dialHTTPProxyContext(ctx, conn, target, timeout)
	default:
		return dialSOCKS5ProxyContext(ctx, conn, target, timeout)
	}
}

func dialHTTPProxyContext(ctx context.Context, conn Connection, target string, timeout time.Duration) (net.Conn, error) {
	d := net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	}
	proxyTarget := dialAddr(conn.ProxyHost, normalizeConnectionProxyPort(conn.ProxyPort))
	netConn, err := d.DialContext(ctx, "tcp", proxyTarget)
	if err != nil {
		return nil, err
	}

	var builder strings.Builder
	builder.WriteString("CONNECT ")
	builder.WriteString(target)
	builder.WriteString(" HTTP/1.1\r\nHost: ")
	builder.WriteString(target)
	builder.WriteString("\r\n")
	if conn.ProxyUsername != "" || conn.ProxyPassword != "" {
		token := base64.StdEncoding.EncodeToString([]byte(conn.ProxyUsername + ":" + conn.ProxyPassword))
		builder.WriteString("Proxy-Authorization: Basic ")
		builder.WriteString(token)
		builder.WriteString("\r\n")
	}
	builder.WriteString("\r\n")

	if _, err := io.WriteString(netConn, builder.String()); err != nil {
		netConn.Close()
		return nil, err
	}

	reader := bufio.NewReader(netConn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		netConn.Close()
		return nil, err
	}
	statusLine = strings.TrimSpace(statusLine)
	statusParts := strings.SplitN(statusLine, " ", 3)
	if len(statusParts) < 2 {
		netConn.Close()
		return nil, fmt.Errorf("invalid proxy response")
	}
	statusCode, err := strconv.Atoi(statusParts[1])
	if err != nil {
		netConn.Close()
		return nil, fmt.Errorf("invalid proxy status: %s", statusLine)
	}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			netConn.Close()
			return nil, readErr
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	if statusCode < 200 || statusCode >= 300 {
		netConn.Close()
		if len(statusParts) == 3 && strings.TrimSpace(statusParts[2]) != "" {
			return nil, fmt.Errorf("proxy connect failed: %s", strings.TrimSpace(statusParts[2]))
		}
		return nil, fmt.Errorf("proxy connect failed: %d", statusCode)
	}
	return &bufferedNetConn{
		Conn:   netConn,
		reader: io.MultiReader(reader, netConn),
	}, nil
}

func dialSOCKS5ProxyContext(ctx context.Context, conn Connection, target string, timeout time.Duration) (net.Conn, error) {
	proxyTarget := dialAddr(conn.ProxyHost, normalizeConnectionProxyPort(conn.ProxyPort))
	var auth *xproxy.Auth
	if conn.ProxyUsername != "" || conn.ProxyPassword != "" {
		auth = &xproxy.Auth{
			User:     conn.ProxyUsername,
			Password: conn.ProxyPassword,
		}
	}
	forward := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	}
	dialer, err := xproxy.SOCKS5("tcp", proxyTarget, auth, forward)
	if err != nil {
		return nil, err
	}
	if contextDialer, ok := dialer.(xproxy.ContextDialer); ok {
		return contextDialer.DialContext(ctx, "tcp", target)
	}

	type dialResult struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan dialResult, 1)
	go func() {
		netConn, dialErr := dialer.Dial("tcp", target)
		resultCh <- dialResult{conn: netConn, err: dialErr}
	}()
	select {
	case <-ctx.Done():
		// Dial 可能在取消后才成功：异步收结果并关闭，避免 fd 泄漏
		go func() {
			if result := <-resultCh; result.conn != nil {
				_ = result.conn.Close()
			}
		}()
		return nil, ctx.Err()
	case result := <-resultCh:
		return result.conn, result.err
	}
}

// ─── 导出包装器（供 package main wrapper 调用）───

func DialConnectionTargetContext(ctx context.Context, conn Connection, target string, timeout time.Duration) (net.Conn, error) {
	return dialConnectionTargetContext(ctx, conn, target, timeout)
}

func ConnectionUsesProxy(conn Connection) bool {
	return connectionUsesProxy(conn)
}
