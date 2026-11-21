package ai

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lumeterm/internal/config"

	xproxy "golang.org/x/net/proxy"
)

func cloneDefaultAIHTTPTransport() *http.Transport {
	transport := &http.Transport{}
	if baseTransport, ok := http.DefaultTransport.(*http.Transport); ok && baseTransport != nil {
		transport = baseTransport.Clone()
	}
	return configureAINeverTimeoutHTTPTransport(transport)
}

// Transport 现在按「代理节点 + 超时类别」缓存复用，只在首次或代理配置变更后重建，
// 空闲连接由 IdleConnTimeout 回收；invalidateAIHTTPClients 会显式关闭闲置连接。
func configureAINeverTimeoutHTTPTransport(transport *http.Transport) *http.Transport {
	if transport == nil {
		transport = &http.Transport{}
	}
	transport.DialContext = (&net.Dialer{
		// 流式请求整体不设超时，但连接阶段必须有上限：代理不可达时不能无限挂住。
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.ResponseHeaderTimeout = 0
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ExpectContinueTimeout = 0
	// 空闲超时必须有限，否则连接与 Transport 永久滞留（仅作用于闲置连接，不影响进行中的流式请求）
	transport.IdleConnTimeout = 90 * time.Second
	return transport
}

func resolveAIRequestProxyNode(settings AIGlobalSettings, profile *AIProviderProfile) (*AIProxyNode, error) {
	selectedID := strings.TrimSpace(settings.AIRequestProxyID)
	if profile != nil && profile.DedicatedProxyEnabled {
		selectedID = strings.TrimSpace(profile.DedicatedProxyID)
		if selectedID == "" {
			return nil, nil
		}
	}
	if selectedID == "" {
		return nil, nil
	}
	for _, node := range settings.ProxyNodes {
		if strings.TrimSpace(node.ID) == selectedID {
			resolved := node
			return &resolved, nil
		}
	}
	if profile != nil && profile.DedicatedProxyEnabled {
		return nil, fmt.Errorf("当前供应商指定的代理节点不存在或已被删除")
	}
	return nil, fmt.Errorf("AI 请求代理节点不存在或已被删除")
}

func buildAIHTTPProxyURL(node AIProxyNode) (*url.URL, error) {
	host := strings.TrimSpace(node.Host)
	if host == "" {
		return nil, fmt.Errorf("AI 代理主机地址不能为空")
	}
	port := node.Port
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("AI 代理端口无效")
	}
	proxyURL := &url.URL{
		Scheme: normalizeAIProxyType(node.Type),
		Host:   net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port)),
	}
	username := strings.TrimSpace(node.Username)
	if username != "" || node.Password != "" {
		if node.Password != "" {
			proxyURL.User = url.UserPassword(username, node.Password)
		} else {
			proxyURL.User = url.User(username)
		}
	}
	return proxyURL, nil
}

func buildAISOCKS5DialContext(node AIProxyNode) (func(context.Context, string, string) (net.Conn, error), error) {
	host := strings.TrimSpace(node.Host)
	if host == "" {
		return nil, fmt.Errorf("AI 代理主机地址不能为空")
	}
	port := node.Port
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("AI 代理端口无效")
	}
	address := net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
	var auth *xproxy.Auth
	if strings.TrimSpace(node.Username) != "" || node.Password != "" {
		auth = &xproxy.Auth{
			User:     strings.TrimSpace(node.Username),
			Password: node.Password,
		}
	}
	forward := &net.Dialer{
		Timeout:   0,
		KeepAlive: 30 * time.Second,
	}
	dialer, err := xproxy.SOCKS5("tcp", address, auth, forward)
	if err != nil {
		return nil, err
	}
	if contextDialer, ok := dialer.(xproxy.ContextDialer); ok {
		return contextDialer.DialContext, nil
	}
	return func(ctx context.Context, network string, target string) (net.Conn, error) {
		type dialResult struct {
			conn net.Conn
			err  error
		}
		resultCh := make(chan dialResult, 1)
		go func() {
			conn, dialErr := dialer.Dial(network, target)
			resultCh <- dialResult{conn: conn, err: dialErr}
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
	}, nil
}

// resolveAIProxyNodeIDForProfile 解析本次请求实际使用的代理节点 ID（无代理时返回空串）。
// 只取 ID 不取 host/port/密码，避免敏感值与易变字段进入缓存 key。
func (a *Service) resolveAIProxyNodeIDForProfile(profile *AIProviderProfile) (string, error) {
	if a == nil || a.configManager == nil {
		return "", nil
	}
	selectedProxy, err := resolveAIRequestProxyNode(a.configManager.GetAIGlobalSettings(), profile)
	if err != nil || selectedProxy == nil {
		return "", err
	}
	return strings.TrimSpace(selectedProxy.ID), nil
}

func (a *Service) buildAIHTTPTransportForProfile(profile *AIProviderProfile) (*http.Transport, error) {
	transport := cloneDefaultAIHTTPTransport()
	transport.Proxy = nil
	if a == nil || a.configManager == nil {
		return transport, nil
	}
	settings := a.configManager.GetAIGlobalSettings()
	selectedProxy, err := resolveAIRequestProxyNode(settings, profile)
	if err != nil {
		return nil, err
	}
	if selectedProxy == nil {
		return transport, nil
	}
	switch normalizeAIProxyType(selectedProxy.Type) {
	case "http", "https":
		// 自签证书场景改走自建隧道：Transport 的 TLSClientConfig 会同时作用于代理和目标 API，
		// 只有自己完成代理 TLS + CONNECT 才能做到只跳过代理证书校验。
		if normalizeAIProxyType(selectedProxy.Type) == "https" && selectedProxy.AllowInsecureTLS {
			dialContext, err := buildAIInsecureHTTPSProxyDialContext(*selectedProxy)
			if err != nil {
				return nil, err
			}
			transport.Proxy = nil
			transport.DialContext = dialContext
			return transport, nil
		}
		proxyURL, err := buildAIHTTPProxyURL(*selectedProxy)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	default:
		dialContext, err := buildAISOCKS5DialContext(*selectedProxy)
		if err != nil {
			return nil, err
		}
		transport.Proxy = nil
		transport.DialContext = dialContext
	}
	return transport, nil
}

// buildAIInsecureHTTPSProxyDialContext 用自建 TLS 隧道连接 HTTPS 代理，仅跳过代理证书校验；
// 目标 API 的 TLS 仍由 Transport 走默认校验，不受该开关影响。
func buildAIInsecureHTTPSProxyDialContext(node AIProxyNode) (func(context.Context, string, string) (net.Conn, error), error) {
	conn := config.Connection{
		ProxyMode:             "custom",
		ProxyType:             "https",
		ProxyHost:             strings.TrimSpace(node.Host),
		ProxyPort:             node.Port,
		ProxyUsername:         strings.TrimSpace(node.Username),
		ProxyPassword:         node.Password,
		ProxyAllowInsecureTLS: true,
	}
	if conn.ProxyHost == "" {
		return nil, fmt.Errorf("AI 代理主机地址不能为空")
	}
	if conn.ProxyPort <= 0 || conn.ProxyPort > 65535 {
		return nil, fmt.Errorf("AI 代理端口无效")
	}
	return func(ctx context.Context, network string, addr string) (net.Conn, error) {
		return config.DialConnectionTargetContext(ctx, conn, addr, 10*time.Second)
	}, nil
}

func aiHTTPClientCacheKey(proxyNodeID string, timeout time.Duration) string {
	return strings.TrimSpace(proxyNodeID) + "|" + timeout.String()
}

// newAIHTTPClientForProfile 按「代理节点 + 超时类别」缓存 http.Client。
// 之前每次请求都新建 Transport，TCP/TLS 无法复用，每轮对话都要重新握手。
func (a *Service) newAIHTTPClientForProfile(profile *AIProviderProfile, timeout time.Duration) (*http.Client, error) {
	if a == nil {
		return nil, fmt.Errorf("AI 服务不可用")
	}
	proxyNodeID, err := a.resolveAIProxyNodeIDForProfile(profile)
	if err != nil {
		return nil, err
	}
	cacheKey := aiHTTPClientCacheKey(proxyNodeID, timeout)
	a.aiHTTPClientMu.Lock()
	defer a.aiHTTPClientMu.Unlock()
	if cached, ok := a.aiHTTPClients[cacheKey]; ok && cached != nil {
		return cached, nil
	}
	transport, err := a.buildAIHTTPTransportForProfile(profile)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	if a.aiHTTPClients == nil {
		a.aiHTTPClients = make(map[string]*http.Client)
	}
	a.aiHTTPClients[cacheKey] = client
	return client, nil
}

// invalidateAIHTTPClients 清空客户端缓存并关闭闲置连接。
// 代理配置变更后必须调用，否则会继续复用旧代理建连。
func (a *Service) invalidateAIHTTPClients() {
	if a == nil {
		return
	}
	a.aiHTTPClientMu.Lock()
	defer a.aiHTTPClientMu.Unlock()
	for _, client := range a.aiHTTPClients {
		if client == nil {
			continue
		}
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
	}
	a.aiHTTPClients = make(map[string]*http.Client)
}

func (a *Service) newAINeverTimeoutHTTPClientForProfile(profile *AIProviderProfile) (*http.Client, error) {
	return a.newAIHTTPClientForProfile(profile, 0)
}

func (a *Service) newAIHTTPClient(timeout time.Duration) (*http.Client, error) {
	return a.newAIHTTPClientForProfile(nil, timeout)
}