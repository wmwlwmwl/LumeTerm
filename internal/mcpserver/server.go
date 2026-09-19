package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

type ServerConfig struct {
	Addr string
	Endpoint string
	AllowedOrigins []string
	ServerInfo Implementation
	Instructions string
	Logger func(string)
}

type Server struct {
	config ServerConfig
	catalog *Catalog
	listener net.Listener
	httpServer *http.Server
}

func NewServer(config ServerConfig, catalog *Catalog) *Server {
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		endpoint = "/mcp"
	}
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	config.Endpoint = endpoint
	return &Server{
		config: config,
		catalog: catalog,
	}
}

func (s *Server) Start() error {
	if s.httpServer != nil {
		return nil
	}
	addr := strings.TrimSpace(s.config.Addr)
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	mux := http.NewServeMux()
	mux.Handle(s.config.Endpoint, NewHTTPHandler(s.catalog, s.config.ServerInfo, s.config.Instructions, s.config.AllowedOrigins, s.config.Logger))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.listener = listener
	s.httpServer = &http.Server{Handler: mux}
	go func(server *http.Server, currentListener net.Listener) {
		if err := server.Serve(currentListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("MCP HTTP 服务器异常退出", "error", err)
		}
	}(s.httpServer, listener)
	return nil
}

func (s *Server) Close(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	server := s.httpServer
	listener := s.listener
	s.httpServer = nil
	s.listener = nil
	var firstErr error
	if err := server.Shutdown(ctx); err != nil && err != http.ErrServerClosed {
		firstErr = err
	}
	if listener != nil {
		if err := listener.Close(); err != nil && err != net.ErrClosed && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Server) Address() string {
	if s == nil || s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) URL() string {
	address := s.Address()
	if address == "" {
		return ""
	}
	return fmt.Sprintf("http://%s%s", address, s.config.Endpoint)
}