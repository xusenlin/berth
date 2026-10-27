package services

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPServer serves Berth's tools to agents over MCP (Streamable HTTP) on
// 127.0.0.1, inside the app: it shares the configuration and the
// connections of the window, so agents see what the user sees.
type MCPServer struct {
	B *Backend

	mu   sync.Mutex
	http *http.Server
	err  string // why the last start failed
	tool *mcp.Server
}

// NewMCPServer returns the MCP server of a backend, not yet listening.
func NewMCPServer(b *Backend) *MCPServer {
	s := &MCPServer{B: b}
	s.tool = mcp.NewServer(&mcp.Implementation{Name: "berth", Title: "Berth", Version: "1"}, &mcp.ServerOptions{
		Instructions: mcpInstructions,
	})
	s.tool.AddReceivingMiddleware(s.activity)
	s.addTools()
	s.syncCommands()
	return s
}

// Start listens when the user enabled the server. Errors, such as a port in
// use, show in the MCP dialog.
func (s *MCPServer) Start() {
	if s.B.Store.MCP().Enabled {
		_ = s.restart()
	}
}

// Close stops listening.
func (s *MCPServer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

func (s *MCPServer) stopLocked() {
	if s.http != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.http.Shutdown(ctx)
		s.http = nil
	}
}

// restart stops the server, then listens again if it is enabled.
func (s *MCPServer) restart() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
	s.err = ""
	cfg := s.B.Store.MCP()
	if !cfg.Enabled {
		return nil
	}
	ln, err := net.Listen("tcp", mcpAddr(cfg.Port))
	if err != nil {
		s.err = fmt.Sprintf("无法监听端口 %d：%v", cfg.Port, err)
		return errors.New(s.err)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", s.authorize(http.NewCrossOriginProtection().Handler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.tool }, &mcp.StreamableHTTPOptions{
			SessionTimeout: time.Hour,
		}),
	)))
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func(srv *http.Server) { _ = srv.Serve(ln) }(s.http)
	return nil
}

// authorize lets through requests with the current token, read on every
// request so that a new token takes effect at once.
func (s *MCPServer) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.B.Store.MCP().Token
		got := r.Header.Get("Authorization")
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *MCPServer) running() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.http != nil, s.err
}

func mcpAddr(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// MCP is the MCP server's settings for the page.
type MCP struct{ S *MCPServer }

// MCPStatus is the state of the MCP server.
type MCPStatus struct {
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
	URL     string `json:"url"`
	Token   string `json:"token"`
	// Commands is whether agents may run read-only commands on servers.
	Commands bool `json:"commands"`
	// Error is why the server is enabled but not running.
	Error string `json:"error,omitempty"`
}

// Status returns the state of the MCP server.
func (m *MCP) Status() MCPStatus {
	cfg := m.S.B.Store.MCP()
	running, err := m.S.running()
	return MCPStatus{
		Enabled:  cfg.Enabled,
		Running:  running,
		URL:      "http://" + mcpAddr(cfg.Port) + "/mcp",
		Token:    cfg.Token,
		Commands: cfg.Commands,
		Error:    err,
	}
}

// SetEnabled turns the MCP server on or off. The first start makes a
// token.
func (m *MCP) SetEnabled(on bool) (MCPStatus, error) {
	cfg := m.S.B.Store.MCP()
	cfg.Enabled = on
	if on && cfg.Token == "" {
		cfg.Token = newToken()
	}
	if err := m.S.B.Store.SetMCP(cfg); err != nil {
		return m.Status(), err
	}
	_ = m.S.restart() // the error shows in the status
	return m.Status(), nil
}

// SetCommands lets agents run read-only commands on servers, or stops
// them. Connected clients see the tool appear or go.
func (m *MCP) SetCommands(on bool) (MCPStatus, error) {
	cfg := m.S.B.Store.MCP()
	cfg.Commands = on
	if err := m.S.B.Store.SetMCP(cfg); err != nil {
		return m.Status(), err
	}
	m.S.syncCommands()
	return m.Status(), nil
}

// ResetToken replaces the token: clients holding the old one are refused.
func (m *MCP) ResetToken() (MCPStatus, error) {
	cfg := m.S.B.Store.MCP()
	cfg.Token = newToken()
	if err := m.S.B.Store.SetMCP(cfg); err != nil {
		return m.Status(), err
	}
	return m.Status(), nil
}
