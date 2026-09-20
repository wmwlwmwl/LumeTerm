// Package wslocal runs the loopback WebSocket terminal server. It bypasses
// the Wails IPC channel for minimal latency and authenticates clients with a
// random token to prevent command injection by local malicious processes.
package wslocal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"lumeterm/internal/wsbuffer"

	"github.com/gorilla/websocket"
)

type InputSink interface {
	// WriteBytes forwards a WebSocket message to the session's stdin.
	WriteBytes(sessionId string, data []byte)
}

type Server struct {
	token    string
	port     int
	manager  *wsbuffer.Manager
	sink     InputSink
	server   *http.Server
	listener net.Listener
}

func NewServer(manager *wsbuffer.Manager, sink InputSink) *Server {
	return &Server{
		manager: manager,
		sink:    sink,
	}
}

func (s *Server) Port() int     { return s.port }
func (s *Server) Token() string { return s.token }

func allowedOrigin(origin string) bool {
	// Wails WebView 自定义协议 Origin（不同平台 / Wails 版本表现不一）：
	//   wails://wails                       —— Windows WebView2（旧版）
	//   wails://wails.localhost[:port]      —— Linux/macOS WebKit dev 模式实测
	//                                          （页面 origin=wails://wails.localhost:<vitePort>）
	//   wails://localhost[:port]            —— 部分环境 WebKit baseURL
	//   http(s)://wails.localhost[:port]    —— Windows WebView2
	// dev 模式下 host 后会带 vite/dev 端口。每个 host 用「精确匹配 + 带冒号前缀」，
	// 避免误匹配 wails.localhost.attacker.com 之类的子域（DNS-rebinding 防护）。
	if origin == "wails://wails" ||
		origin == "wails://wails.localhost" ||
		strings.HasPrefix(origin, "wails://wails.localhost:") ||
		origin == "wails://localhost" ||
		strings.HasPrefix(origin, "wails://localhost:") ||
		origin == "http://wails.localhost" ||
		strings.HasPrefix(origin, "http://wails.localhost:") ||
		origin == "https://wails.localhost" ||
		strings.HasPrefix(origin, "https://wails.localhost:") {
		return true
	}
	return strings.HasPrefix(origin, "http://localhost:") ||
		strings.HasPrefix(origin, "http://127.0.0.1:") ||
		strings.HasPrefix(origin, "http://[::1]:")
}

// Start generates the auth token, listens on a random loopback port and
// serves the /ws/ endpoint in the background.
func (s *Server) Start() error {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return err
	}
	s.token = hex.EncodeToString(tokenBytes)

	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return allowedOrigin(r.Header.Get("Origin"))
		},
		ReadBufferSize:  4096,
		WriteBufferSize: 32768,
	}
	mux.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {
		// 校验 token，拒绝未携带正确 token 的连接
		if r.URL.Query().Get("token") != s.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sessionId := strings.TrimPrefix(r.URL.Path, "/ws/")
		if sessionId == "" {
			http.Error(w, "missing sessionId", http.StatusBadRequest)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// 注册当前 WebSocket 连接（同 session 重连时自动关闭旧连接），并 flush 注册前缓冲
		entry := s.manager.Register(sessionId, conn)
		s.manager.FlushPending(sessionId)
		defer s.manager.Unregister(sessionId, entry)

		// 读取 WebSocket 消息，直通 SSH stdin
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("[ws] 读取 WebSocket 消息失败 sessionId=%s err=%v（前端连接断开或异常）", sessionId, err)
				break
			}
			s.sink.WriteBytes(sessionId, msg)
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.listener = listener
	s.port = listener.Addr().(*net.TCPAddr).Port
	s.server = &http.Server{Handler: mux}
	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("WebSocket server stopped: %v", err)
		}
	}()
	return nil
}

// Stop shuts the server down and releases the listener.
func (s *Server) Stop() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.server.Shutdown(ctx)
		s.server = nil
	}
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
}
