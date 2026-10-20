package services

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/xusenlin/berth/core/config"
	"github.com/xusenlin/berth/core/ssh"
)

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func connectMCP(t *testing.T, url, token string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: &http.Client{Transport: bearer{token}},
		MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// checkObject fails when a result's structuredContent is not a JSON object,
// which MCP requires and Claude Code enforces (the Go client does not).
func checkObject(t *testing.T, name string, res *mcp.CallToolResult) {
	t.Helper()
	if res.StructuredContent == nil {
		return
	}
	if _, ok := res.StructuredContent.(map[string]any); !ok {
		t.Errorf("%s: structuredContent is %T, not an object", name, res.StructuredContent)
	}
}

func toolText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// TestMCP checks the MCP server end to end over HTTP: the token, the tool
// schemas, and that servers the user disconnected stay off limits.
func TestMCP(t *testing.T) {
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMCP(config.MCP{Port: freePort(t)}); err != nil {
		t.Fatal(err)
	}
	srv, err := store.SaveServer(config.Server{Name: "prod", Host: "127.0.0.1", Port: 1, User: "u", Auth: config.AuthPassword, HostKey: "SHA256:x"})
	if err != nil {
		t.Fatal(err)
	}
	b := NewBackend(store, config.MemorySecrets{})
	b.Pool = ssh.NewPool(func(string) (ssh.Target, error) { return ssh.Target{}, errors.New("no server") }, nil)
	s := NewMCPServer(b)
	defer s.Close()
	m := &MCP{S: s}

	st, err := m.SetEnabled(true)
	if err != nil || !st.Running || st.Token == "" || st.Error != "" {
		t.Fatalf("enable: %+v %v", st, err)
	}

	resp, err := http.Post(st.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: status %d", resp.StatusCode)
	}

	ctx := context.Background()
	cs := connectMCP(t, st.URL, st.Token)
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	if len(byName) != 18 {
		t.Errorf("%d tools", len(byName))
	}
	schema, _ := json.Marshal(byName["create_app"].InputSchema)
	for _, field := range []string{`"entry"`, `"port"`, `"health"`, `"env"`, `"required":["server","name"]`} {
		if !strings.Contains(string(schema), field) {
			t.Errorf("create_app schema lacks %s: %s", field, schema)
		}
	}
	if a := byName["delete_app"].Annotations; a.ReadOnlyHint || a.DestructiveHint != nil {
		t.Error("delete_app is not marked destructive")
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_servers", Arguments: map[string]any{}})
	if err != nil || res.IsError || !strings.Contains(toolText(res), `"name":"prod"`) {
		t.Fatalf("list_servers: %v %+v", err, res)
	}
	checkObject(t, "list_servers", res)
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_apps", Arguments: map[string]any{"server": "staging"}})
	if err != nil || !res.IsError || !strings.Contains(toolText(res), "list_servers") {
		t.Errorf("unknown server: %v %+v", err, res)
	}

	if err := (&Servers{B: b}).Disconnect(srv.ID); err != nil {
		t.Fatal(err)
	}
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_apps", Arguments: map[string]any{"server": "prod"}})
	if err != nil || !res.IsError || !strings.Contains(toolText(res), "disconnected") {
		t.Errorf("offline server: %v %+v", err, res)
	}

	if _, err := m.SetCommands(true); err != nil {
		t.Fatal(err)
	}
	if tools, err = cs.ListTools(ctx, nil); err != nil || len(tools.Tools) != 19 {
		t.Errorf("with commands: %v %d tools", err, len(tools.Tools))
	}
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "run_command", Arguments: map[string]any{"server": "prod", "command": "rm -rf /"}})
	if err != nil || !res.IsError || !strings.Contains(toolText(res), "disconnected") {
		t.Errorf("run_command on an offline server: %v %+v", err, res)
	}
	if _, err := m.SetCommands(false); err != nil {
		t.Fatal(err)
	}
	if tools, err = cs.ListTools(ctx, nil); err != nil || len(tools.Tools) != 18 {
		t.Errorf("without commands: %v %d tools", err, len(tools.Tools))
	}

	if _, err := m.ResetToken(); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListTools(ctx, nil); err == nil {
		t.Error("old token still accepted")
	}

	if st, _ := m.SetEnabled(false); st.Running {
		t.Error("still running after disable")
	}
}
