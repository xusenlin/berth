//go:build e2e

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/xusenlin/berth/core/config"
)

// TestMCPEndToEnd drives an app's whole life through the MCP tools, as an
// agent would, against the test server of core/e2e.
func TestMCPEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	host, port := "127.0.0.1", 2222
	if v := os.Getenv("BERTH_E2E_HOST"); v != "" {
		host = v
	}
	if v := os.Getenv("BERTH_E2E_PORT"); v != "" {
		port, _ = strconv.Atoi(v)
	}

	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMCP(config.MCP{Port: freePort(t)}); err != nil {
		t.Fatal(err)
	}
	b := NewBackend(store, config.MemorySecrets{})
	defer b.Pool.CloseAll()
	servers := &Servers{B: b}
	in := ServerInput{Server: config.Server{Name: "e2e", Host: host, Port: port, User: "root", Auth: config.AuthPassword}, Secret: "berth"}
	tr, err := servers.Test(ctx, in)
	if err != nil {
		t.Skipf("no test server at %s:%d: %v", host, port, err)
	}
	in.Server.HostKey = tr.Fingerprint
	if _, err := servers.Save(in); err != nil {
		t.Fatal(err)
	}
	goarch := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[tr.Env.Arch]
	if goarch == "" {
		t.Skipf("no Go architecture for %s", tr.Env.Arch)
	}

	s := NewMCPServer(b)
	defer s.Close()
	st, err := (&MCP{S: s}).SetEnabled(true)
	if err != nil || !st.Running {
		t.Fatalf("enable: %+v %v", st, err)
	}
	cs := connectMCP(t, st.URL, st.Token)

	call := func(name string, args map[string]any, wantOK bool) string {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args, Meta: mcp.Meta{"progressToken": name}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		checkObject(t, name, res)
		text := toolText(res)
		if res.IsError == wantOK {
			t.Fatalf("%s: isError=%v\n%s", name, res.IsError, text)
		}
		return text
	}
	build := func(version string, broken bool) string {
		out := filepath.Join(t.TempDir(), "testapp")
		ld := "-X main.version=" + version
		if broken {
			ld += " -X main.broken=true"
		}
		cmd := exec.Command("go", "build", "-ldflags", ld, "-o", out, ".")
		cmd.Dir = filepath.Join("..", "testdata", "e2e", "testapp")
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goarch, "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build testapp: %v\n%s", err, b)
		}
		return out
	}

	name := "mcp-e2e"
	call("delete_app", map[string]any{"server": "e2e", "app": name}, true) // leftovers of an earlier run
	call("create_app", map[string]any{
		"server": "e2e", "name": name, "entry": "testapp",
		"args":   "-http 127.0.0.1:{{port}} -data {{shared}}/starts.log",
		"health": map[string]any{"type": "http", "path": "/health", "timeout": 10},
		"env":    map[string]string{"GREETING": "hello"},
		"backup": true,
	}, true)
	defer call("delete_app", map[string]any{"server": "e2e", "app": name}, true)

	v1 := build("v1", false)
	if text := call("check_deploy", map[string]any{"server": "e2e", "app": name, "file": v1}, true); !strings.Contains(text, tr.Env.Arch) {
		t.Errorf("check_deploy: %s", text)
	}
	var out deployOutcome
	text := call("deploy", map[string]any{"server": "e2e", "app": name, "file": v1}, true)
	if err := json.Unmarshal([]byte(text), &out); err != nil || !out.OK || len(out.Steps) == 0 {
		t.Fatalf("deploy v1: %v %s", err, text)
	}
	first := out.Release

	if text := call("query_logs", map[string]any{"server": "e2e", "app": name, "since": "5m"}, true); !strings.Contains(text, "testapp v1") || !strings.Contains(text, "GREETING=hello") {
		t.Errorf("logs of v1:\n%s", text)
	}

	text = call("deploy", map[string]any{"server": "e2e", "app": name, "file": build("v2", true)}, false)
	if err := json.Unmarshal([]byte(text), &out); err != nil || !out.RolledBack || !strings.Contains(out.Logs, "broken build") {
		t.Fatalf("broken deploy: %v %s", err, text)
	}

	time.Sleep(time.Second) // release ids have a one-second resolution
	call("deploy", map[string]any{"server": "e2e", "app": name, "file": build("v3", false)}, true)
	if text := call("list_releases", map[string]any{"server": "e2e", "app": name}, true); !strings.Contains(text, first) {
		t.Errorf("releases: %s", text)
	}
	call("rollback", map[string]any{"server": "e2e", "app": name, "release": first}, true)

	if text := call("update_app", map[string]any{"server": "e2e", "app": name, "setEnv": map[string]string{"GREETING": "bonjour"}}, true); strings.Contains(text, "bonjour") {
		t.Errorf("update_app shows an env value: %s", text)
	}
	call("app_action", map[string]any{"server": "e2e", "app": name, "action": "restart"}, true)
	time.Sleep(time.Second)
	if text := call("query_logs", map[string]any{"server": "e2e", "app": name, "keyword": "bonjour"}, true); !strings.Contains(text, "testapp v1") {
		t.Errorf("logs after update:\n%s", text)
	}
	text = call("get_app", map[string]any{"server": "e2e", "app": name}, true)
	var got struct {
		App struct{ Port int } `json:"app"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil || got.App.Port == 0 {
		t.Fatalf("get_app port: %v %s", err, text)
	}
	if !strings.Contains(text, `"current":"`+first+`"`) || !strings.Contains(text, `"GREETING":"(hidden, 7 chars)"`) || !strings.Contains(text, `"backup":true`) {
		t.Errorf("get_app: %s", text)
	}

	var fc struct {
		Content string `json:"content"`
		ModTime int64  `json:"modTime"`
	}
	text = call("read_shared_file", map[string]any{"server": "e2e", "app": name, "path": "starts.log"}, true)
	if err := json.Unmarshal([]byte(text), &fc); err != nil || !strings.Contains(fc.Content, "v1") {
		t.Fatalf("read_shared_file: %v %s", err, text)
	}
	call("read_shared_file", map[string]any{"server": "e2e", "app": name, "path": "./x/../.env"}, false)
	call("write_shared_file", map[string]any{"server": "e2e", "app": name, "path": "starts.log", "content": "x"}, false) // no modTime
	call("write_shared_file", map[string]any{"server": "e2e", "app": name, "path": "starts.log", "content": "x", "modTime": fc.ModTime}, true)

	if text := call("list_apps", map[string]any{"server": "e2e"}, true); !strings.Contains(text, `"name":"`+name+`"`) {
		t.Errorf("list_apps: %s", text)
	}

	if _, err := (&MCP{S: s}).SetCommands(true); err != nil {
		t.Fatal(err)
	}
	run := func(args map[string]any, wantOK bool, want string) {
		t.Helper()
		args["server"] = "e2e"
		if text := call("run_command", args, wantOK); !strings.Contains(text, want) {
			t.Errorf("run_command %v: want %q in\n%s", args["command"], want, text)
		}
	}
	run(map[string]any{"command": "ls -1 && pwd"}, true, name)
	run(map[string]any{"command": "cat starts.log | wc -c", "dir": name + "/shared"}, true, "1")
	run(map[string]any{"command": "for f in ./*; do echo \"f=$f\"; done", "dir": "~"}, true, "f=./senlinApps")
	run(map[string]any{"command": "journalctl -u berth-" + name + " -n 5 --no-pager 2>&1 | tail -n 1 >/dev/null; echo ok"}, true, "ok")
	run(map[string]any{"command": "grep nothing-here /etc/hostname"}, true, "[exit status 1]")
	run(map[string]any{"command": "ls /nowhere"}, true, "No such file")
	run(map[string]any{"command": "seq 100000"}, true, "output cut at 64 KiB")
	run(map[string]any{"command": "while true; do :; done", "timeout": 2}, true, "stopped after 2s")
	run(map[string]any{"command": "sqlite3 -json :memory: 'select 41+1 as n'"}, true, `"n":42`)
	run(map[string]any{"command": "sqlite3 ./new.db 'create table t(x)'; ls -a", "dir": name + "/shared"}, true, "starts.log")
	run(map[string]any{"command": "ls new.db", "dir": name + "/shared"}, true, "No such file")
	run(map[string]any{"command": "sqlite3 :memory: '.shell echo pwned'"}, true, "safe mode")
	health := fmt.Sprintf("http://127.0.0.1:%d/health", got.App.Port)
	run(map[string]any{"command": "curl -s -o /dev/null -w '%{http_code}' " + health}, true, "200")
	run(map[string]any{"command": "p=$(pgrep -o -f 'testapp -http'); grep '^Pid:' /proc/$p/status"}, true, "Pid:")
	run(map[string]any{"command": "cat /proc/1/environ"}, false, "environ")
	run(map[string]any{"command": "cat .env", "dir": name + "/shared"}, false, ".env")
	run(map[string]any{"command": "curl -s https://example.com/"}, false, "server itself")
	run(map[string]any{"command": "rm -rf " + name}, false, "refused")
	run(map[string]any{"command": "echo x > " + name + "/app.json"}, false, "refused")
	run(map[string]any{"command": "ls", "dir": "/nowhere"}, false, "cannot enter")
}
