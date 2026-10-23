package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/xusenlin/berth/core/app"
	"github.com/xusenlin/berth/core/config"
	"github.com/xusenlin/berth/core/deploy"
	"github.com/xusenlin/berth/core/logs"
)

const mcpInstructions = `Berth deploys Linux programs to servers over SSH and runs each as a systemd service. Nothing runs on the servers besides the apps.

- Servers are configured by the user in the Berth app; refer to one by its name or id from list_servers.
- An app is a program on one server: app.json (entry, args, port, env, health check…), releases/ (one directory per deploy) and shared/ (data kept across releases, e.g. a database).
- To ship code: build it for the server's arch (list_servers shows it, e.g. x86_64 → GOOS=linux GOARCH=amd64), then deploy the local file (an executable, .zip or .tar.gz). check_deploy validates it first. A release that fails its health check is rolled back automatically; the result carries the journal lines.
- Apps with backup on get shared/ archived on the server before each deploy (the latest 20 are kept) and restored when the deploy rolls back.
- New settings (update_app) reach a running app when it restarts.
- Environment variable values are hidden from you, as they often hold passwords: get_app shows names and lengths. Set them with update_app setEnv.`

// Handlers return objects, never bare arrays: MCP requires
// structuredContent to be a JSON object, and clients such as Claude Code
// reject anything else.
func (s *MCPServer) addTools() {
	no := false
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &no}
	write := &mcp.ToolAnnotations{DestructiveHint: &no, OpenWorldHint: &no}
	destructive := &mcp.ToolAnnotations{OpenWorldHint: &no}

	mcp.AddTool(s.tool, &mcp.Tool{Name: "list_servers", Annotations: ro,
		Description: "List the servers configured in Berth, with their connection state and, once connected, their architecture and mode."},
		s.listServers)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "server_overview", Annotations: ro,
		Description: "CPU, memory, load, disks and OS of a server."},
		s.serverOverview)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "list_ports", Annotations: ro,
		Description: "Listening ports of a server with the app owning each, ports reserved by stopped apps, and a free port suggested for a new app."},
		s.listPorts)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "list_apps", Annotations: ro,
		Description: "Apps of a server with their state, port and current release."},
		s.listApps)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "get_app", Annotations: ro,
		Description: "Full settings (app.json) and systemd state of an app."},
		s.getApp)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "list_releases", Annotations: ro,
		Description: "Releases of an app, newest first, with the uploaded file name and the Go build info (version, commit) when known."},
		s.listReleases)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "query_logs", Annotations: ro,
		Description: "Past lines of an app's journal, oldest first."},
		s.queryLogs)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "list_shared_files", Annotations: ro,
		Description: "Files of an app's shared/ directory, the data kept across releases."},
		s.listShared)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "read_shared_file", Annotations: ro,
		Description: "Read a text file of an app's shared/ directory. Keep modTime to write it back."},
		s.readShared)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "write_shared_file", Annotations: destructive,
		Description: "Create or replace a text file (up to 1 MiB) of an app's shared/ directory. To replace a file, pass the modTime read_shared_file returned; the write fails if the file changed since."},
		s.writeShared)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "create_app", Annotations: write,
		Description: "Create an app on a server. It has no release until deploy. Args may use {{port}} and {{shared}} (the absolute path of shared/)."},
		s.createApp)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "update_app", Annotations: destructive,
		Description: "Change settings of an app; omitted fields keep their value. A running app picks the changes up when restarted (app_action restart)."},
		s.updateApp)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "set_note", Annotations: write,
		Description: "Replace the free-text note of an app, shown to the user in Berth."},
		s.setNote)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "app_action", Annotations: destructive,
		Description: "Start, stop or restart an app."},
		s.appAction)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "check_deploy", Annotations: ro,
		Description: "Inspect a local release file before deploying it: its kind, size, architecture against the server's, Go build info, and warnings such as low disk space."},
		s.checkDeploy)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "deploy", Annotations: write,
		Description: "Upload a local file (executable, .zip or .tar.gz) as a new release of an app and switch to it. Stops the app, backs up shared/ when enabled, switches, starts and runs the health check; on failure it switches back and returns the journal lines. Earlier releases stay for rollback."},
		s.deploy)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "rollback", Annotations: write,
		Description: "Switch an app to an earlier release from list_releases and restart it. Data in shared/ is left as it is."},
		s.rollback)
	mcp.AddTool(s.tool, &mcp.Tool{Name: "delete_app", Annotations: destructive,
		Description: "Stop an app and delete it from the server with all its releases, backups and shared/ data. Cannot be undone: only call it when the user asked for it."},
		s.deleteApp)
}

// server resolves a server by id or name and checks it may be used.
func (s *MCPServer) server(ref string) (config.Server, error) {
	ref = strings.TrimSpace(ref)
	servers := s.B.Store.Servers()
	var found []config.Server
	for _, srv := range servers {
		if srv.ID == ref {
			found = []config.Server{srv}
			break
		}
		if strings.EqualFold(srv.Name, ref) || srv.Host == ref {
			found = append(found, srv)
		}
	}
	switch {
	case len(found) == 0:
		return config.Server{}, fmt.Errorf("no server %q: call list_servers", ref)
	case len(found) > 1:
		return config.Server{}, fmt.Errorf("several servers match %q: use the id from list_servers", ref)
	}
	srv := found[0]
	if s.B.isOffline(srv.ID) {
		return srv, fmt.Errorf("the user disconnected server %s in Berth; ask them to connect it again", srv.Name)
	}
	return srv, nil
}

func (s *MCPServer) servers() *Servers { return &Servers{B: s.B} }
func (s *MCPServer) apps() *Apps       { return &Apps{B: s.B} }

type serverArg struct {
	Server string `json:"server" jsonschema:"server name or id"`
}

type appArg struct {
	Server string `json:"server" jsonschema:"server name or id"`
	App    string `json:"app" jsonschema:"app name"`
}

type mcpServer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	User    string `json:"user"`
	Status  string `json:"status"`
	Offline bool   `json:"offline,omitempty"`
	Arch    string `json:"arch,omitempty"`
	// Mode is root (system services) or user (user services).
	Mode    string `json:"mode,omitempty"`
	Systemd *bool  `json:"systemd,omitempty"`
}

func (s *MCPServer) listServers(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	out := []mcpServer{}
	for _, v := range s.servers().List() {
		m := mcpServer{ID: v.Server.ID, Name: v.Server.Name, Host: v.Server.Host, User: v.Server.User, Status: string(v.Status), Offline: v.Offline}
		if v.Env != nil {
			m.Arch, m.Mode, m.Systemd = v.Env.Arch, string(v.Env.Mode), &v.Env.Systemd
		}
		out = append(out, m)
	}
	return nil, map[string]any{"servers": out}, nil
}

func (s *MCPServer) serverOverview(ctx context.Context, _ *mcp.CallToolRequest, in serverArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	ov, err := s.servers().Overview(ctx, srv.ID)
	return nil, ov, err
}

func (s *MCPServer) listPorts(ctx context.Context, _ *mcp.CallToolRequest, in serverArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	p, err := s.servers().Ports(ctx, srv.ID)
	if err != nil {
		return nil, nil, err
	}
	suggested, _ := s.apps().SuggestPort(ctx, srv.ID)
	return nil, struct {
		Ports
		Suggested int `json:"suggested"`
	}{p, suggested}, nil
}

type mcpApp struct {
	Name      string `json:"name"`
	Active    string `json:"active"`
	Sub       string `json:"sub,omitempty"`
	Port      int    `json:"port,omitempty"`
	Current   string `json:"current,omitempty"`
	Autostart bool   `json:"autostart"`
	Note      string `json:"note,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (s *MCPServer) listApps(ctx context.Context, _ *mcp.CallToolRequest, in serverArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	infos, err := s.apps().List(ctx, srv.ID)
	if err != nil {
		return nil, nil, err
	}
	out := []mcpApp{}
	for _, i := range infos {
		out = append(out, mcpApp{Name: i.App.Name, Active: i.Status.Active, Sub: i.Status.Sub, Port: i.App.Port,
			Current: i.Current, Autostart: i.App.Autostart, Note: i.App.Note, Error: i.Error})
	}
	return nil, map[string]any{"apps": out}, nil
}

func (s *MCPServer) getApp(ctx context.Context, _ *mcp.CallToolRequest, in appArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	info, err := s.apps().Get(ctx, srv.ID, in.App)
	info.App = masked(info.App)
	return nil, info, err
}

// masked hides the values of an app's environment variables, which often
// hold passwords, so that they stay out of the agent's context: it sees the
// names and lengths, and sets values with update_app.
func masked(a app.App) app.App {
	env := make(map[string]string, len(a.Env))
	for k, v := range a.Env {
		env[k] = fmt.Sprintf("(hidden, %d chars)", utf8.RuneCountInString(v))
	}
	a.Env = env
	return a
}

func (s *MCPServer) listReleases(ctx context.Context, _ *mcp.CallToolRequest, in appArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	rels, err := s.apps().Releases(ctx, srv.ID, in.App)
	if rels == nil {
		rels = []app.Release{}
	}
	return nil, map[string]any{"releases": rels}, err
}

type logsArg struct {
	Server  string `json:"server" jsonschema:"server name or id"`
	App     string `json:"app" jsonschema:"app name"`
	Lines   int    `json:"lines,omitempty" jsonschema:"most lines returned, the newest; default 200, at most 2000"`
	Since   string `json:"since,omitempty" jsonschema:"start: a duration back from now such as 30m or 2h, or an RFC 3339 time"`
	Until   string `json:"until,omitempty" jsonschema:"end, same forms as since"`
	Keyword string `json:"keyword,omitempty" jsonschema:"keep lines containing it, ignoring case"`
}

func (s *MCPServer) queryLogs(ctx context.Context, _ *mcp.CallToolRequest, in logsArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	q := logs.Query{Keyword: in.Keyword, Limit: in.Lines}
	if q.Limit <= 0 {
		q.Limit = 200
	}
	q.Limit = min(q.Limit, 2000)
	if q.Since, err = logTime(in.Since); err != nil {
		return nil, nil, fmt.Errorf("since: %w", err)
	}
	if q.Until, err = logTime(in.Until); err != nil {
		return nil, nil, fmt.Errorf("until: %w", err)
	}
	entries, err := s.apps().QueryLogs(ctx, srv.ID, in.App, q)
	if err != nil {
		return nil, nil, err
	}
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(time.UnixMilli(e.Time).Format("2006-01-02 15:04:05"))
		if e.Source == "stderr" {
			b.WriteString(" [stderr]")
		}
		b.WriteString(" ")
		b.WriteString(e.Message)
		b.WriteString("\n")
	}
	if len(entries) == 0 {
		b.WriteString("(no log lines)")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}, nil, nil
}

// logTime reads a duration back from now or an RFC 3339 time, in
// milliseconds since the epoch; "" is 0, unbounded.
func logTime(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d.Abs()).UnixMilli(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, errors.New("want a duration such as 30m or an RFC 3339 time")
	}
	return t.UnixMilli(), nil
}

func (s *MCPServer) listShared(ctx context.Context, _ *mcp.CallToolRequest, in appArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	files, err := s.apps().ListShared(ctx, srv.ID, in.App)
	return nil, files, err
}

type sharedArg struct {
	Server string `json:"server" jsonschema:"server name or id"`
	App    string `json:"app" jsonschema:"app name"`
	Path   string `json:"path" jsonschema:"path relative to shared/"`
}

func (s *MCPServer) readShared(ctx context.Context, _ *mcp.CallToolRequest, in sharedArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	// shared/.env holds the environment variables, which get_app hides.
	if path.Clean("/"+in.Path) == "/.env" {
		return nil, nil, errors.New(".env holds the app's environment variables, whose values are hidden: get_app lists their names")
	}
	fc, err := s.apps().ReadShared(ctx, srv.ID, in.App, in.Path)
	return nil, fc, err
}

type writeSharedArg struct {
	Server  string `json:"server" jsonschema:"server name or id"`
	App     string `json:"app" jsonschema:"app name"`
	Path    string `json:"path" jsonschema:"path relative to shared/"`
	Content string `json:"content" jsonschema:"the whole new content"`
	ModTime int64  `json:"modTime,omitempty" jsonschema:"modTime from read_shared_file; omit for a new file"`
}

func (s *MCPServer) writeShared(ctx context.Context, _ *mcp.CallToolRequest, in writeSharedArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	fc, err := s.apps().WriteShared(ctx, srv.ID, in.App, in.Path, in.Content, in.ModTime)
	if err != nil {
		return nil, nil, err
	}
	fc.Content = "" // the caller has it
	return nil, fc, nil
}

// appFields are the settings create_app and update_app take; nil keeps the
// value of the preset or of the app.
type appFields struct {
	Entry        *string     `json:"entry,omitempty" jsonschema:"executable to run, relative to the release directory, e.g. myapp or bin/server"`
	Args         *string     `json:"args,omitempty" jsonschema:"command-line arguments; {{port}} and {{shared}} are replaced"`
	Port         *int        `json:"port,omitempty" jsonschema:"port the app listens on, 0 for none"`
	Shared       []string    `json:"shared,omitempty" jsonschema:"paths relative to the release directory that link into shared/ and so persist across releases, e.g. data or config.yaml"`
	Autostart    *bool       `json:"autostart,omitempty" jsonschema:"start at boot"`
	Restart      *string     `json:"restart,omitempty" jsonschema:"systemd restart policy: on-failure, always or no"`
	Health       *app.Health `json:"health,omitempty" jsonschema:"health check after start: type process, tcp (needs port), http (needs port; path such as /health) or none; timeout in seconds"`
	Backup       *bool       `json:"backup,omitempty" jsonschema:"archive shared/ on the server before each deploy and restore it if the deploy rolls back"`
	KeepReleases *int        `json:"keepReleases,omitempty" jsonschema:"releases kept for rollback"`
}

func (f appFields) apply(a *app.App) {
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&a.Entry, f.Entry)
	set(&a.Args, f.Args)
	if f.Restart != nil {
		a.Restart = app.Restart(*f.Restart)
	}
	if f.Port != nil {
		a.Port = *f.Port
	}
	if f.Shared != nil {
		a.Shared = f.Shared
	}
	if f.Autostart != nil {
		a.Autostart = *f.Autostart
	}
	if f.Health != nil {
		a.Health = *f.Health
	}
	if f.Backup != nil {
		a.Backup = *f.Backup
	}
	if f.KeepReleases != nil {
		a.KeepReleases = *f.KeepReleases
	}
}

type createArg struct {
	Server string `json:"server" jsonschema:"server name or id"`
	Name   string `json:"name" jsonschema:"app name: lowercase letters, digits, - and _, at most 40"`
	Preset string `json:"preset,omitempty" jsonschema:"starting settings: generic (default; autostart, restart on-failure, process health check, keeps 5 releases) or pocketbase (entry pocketbase, shared pb_data, HTTP health check on /api/health, backup on)"`
	appFields
	Env  map[string]string `json:"env,omitempty" jsonschema:"environment variables"`
	Note string            `json:"note,omitempty" jsonschema:"free text about the app, shown in Berth"`
}

func (s *MCPServer) createApp(ctx context.Context, _ *mcp.CallToolRequest, in createArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	preset := in.Preset
	if preset == "" {
		preset = "generic"
	}
	var a app.App
	found := false
	for _, p := range app.Presets() {
		if p.ID == preset {
			a, found = p.App, true
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("unknown preset %q: use generic or pocketbase", preset)
	}
	a.Name = in.Name
	in.appFields.apply(&a)
	if in.Port == nil {
		if a.Port, err = s.apps().SuggestPort(ctx, srv.ID); err != nil {
			return nil, nil, err
		}
	}
	if in.Env != nil {
		a.Env = in.Env
	}
	a.Note = in.Note
	created, err := s.apps().Create(context.WithoutCancel(ctx), srv.ID, a)
	return nil, masked(created), err
}

type updateArg struct {
	Server string `json:"server" jsonschema:"server name or id"`
	App    string `json:"app" jsonschema:"app name"`
	appFields
	SetEnv   map[string]string `json:"setEnv,omitempty" jsonschema:"environment variables to add or change; others are kept"`
	UnsetEnv []string          `json:"unsetEnv,omitempty" jsonschema:"environment variables to remove"`
}

func (s *MCPServer) updateApp(ctx context.Context, _ *mcp.CallToolRequest, in updateArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	info, err := s.apps().Get(ctx, srv.ID, in.App)
	if err != nil {
		return nil, nil, err
	}
	if info.Error != "" {
		return nil, nil, errors.New(info.Error)
	}
	a := info.App
	in.appFields.apply(&a)
	if a.Env == nil {
		a.Env = map[string]string{}
	}
	for k, v := range in.SetEnv {
		a.Env[k] = v
	}
	for _, k := range in.UnsetEnv {
		delete(a.Env, k)
	}
	updated, err := s.apps().Update(context.WithoutCancel(ctx), srv.ID, a)
	return nil, masked(updated), err
}

type noteArg struct {
	Server string `json:"server" jsonschema:"server name or id"`
	App    string `json:"app" jsonschema:"app name"`
	Note   string `json:"note" jsonschema:"the new note; empty clears it"`
}

func (s *MCPServer) setNote(ctx context.Context, _ *mcp.CallToolRequest, in noteArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	note, err := s.apps().SetNote(ctx, srv.ID, in.App, in.Note)
	return nil, map[string]string{"note": note}, err
}

type actionArg struct {
	Server string `json:"server" jsonschema:"server name or id"`
	App    string `json:"app" jsonschema:"app name"`
	Action string `json:"action" jsonschema:"start, stop or restart"`
}

func (s *MCPServer) appAction(ctx context.Context, _ *mcp.CallToolRequest, in actionArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	if err := s.apps().Do(context.WithoutCancel(ctx), srv.ID, in.App, app.Action(in.Action)); err != nil {
		return nil, nil, err
	}
	info, err := s.apps().Get(ctx, srv.ID, in.App)
	return nil, info.Status, err
}

type fileArg struct {
	Server string `json:"server" jsonschema:"server name or id"`
	App    string `json:"app" jsonschema:"app name"`
	File   string `json:"file" jsonschema:"absolute path on this computer of an executable, .zip or .tar.gz"`
}

func (in fileArg) path() (string, error) {
	if !filepath.IsAbs(in.File) {
		return "", fmt.Errorf("file must be an absolute path, got %q", in.File)
	}
	if _, err := os.Stat(in.File); err != nil {
		return "", err
	}
	return in.File, nil
}

func (s *MCPServer) checkDeploy(ctx context.Context, _ *mcp.CallToolRequest, in fileArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	file, err := in.path()
	if err != nil {
		return nil, nil, err
	}
	pf, err := s.apps().Check(ctx, srv.ID, in.App, file)
	return nil, pf, err
}

// deployOutcome is a deploy or rollback with the steps it went through.
type deployOutcome struct {
	deploy.Result
	Steps []string `json:"steps"`
}

// track reports the progress of a deploy to the client when it asked for
// it, and records the finished steps.
func track(ctx context.Context, req *mcp.CallToolRequest, out *deployOutcome) func(deploy.Event) {
	token := req.Params.GetProgressToken()
	n := 0
	return func(e deploy.Event) {
		if e.State != deploy.StateRunning {
			line := string(e.Step) + ": " + string(e.State)
			if e.Message != "" {
				line += " " + e.Message
			}
			out.Steps = append(out.Steps, line)
		}
		if token == nil {
			return
		}
		n++
		msg := string(e.Step)
		if e.Progress > 0 {
			msg += fmt.Sprintf(" %.0f%%", e.Progress*100)
		} else if e.Message != "" {
			msg += " " + e.Message
		}
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Progress: float64(n), Message: msg})
	}
}

func (s *MCPServer) deploy(ctx context.Context, req *mcp.CallToolRequest, in fileArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	file, err := in.path()
	if err != nil {
		return nil, nil, err
	}
	m, err := s.B.manager(ctx, srv.ID)
	if err != nil {
		return nil, nil, err
	}
	out := deployOutcome{Steps: []string{}}
	d := &deploy.Deployer{ServerID: srv.ID, M: m}
	// An agent that gives up waiting must not stop the deploy midway.
	out.Result, _ = outcome(d.Deploy(context.WithoutCancel(ctx), in.App, file, track(ctx, req, &out)))
	return failedIf(out)
}

type rollbackArg struct {
	Server  string `json:"server" jsonschema:"server name or id"`
	App     string `json:"app" jsonschema:"app name"`
	Release string `json:"release" jsonschema:"release id from list_releases"`
}

func (s *MCPServer) rollback(ctx context.Context, req *mcp.CallToolRequest, in rollbackArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	m, err := s.B.manager(ctx, srv.ID)
	if err != nil {
		return nil, nil, err
	}
	out := deployOutcome{Steps: []string{}}
	d := &deploy.Deployer{ServerID: srv.ID, M: m}
	out.Result, _ = outcome(d.Rollback(context.WithoutCancel(ctx), in.App, in.Release, track(ctx, req, &out)))
	return failedIf(out)
}

// failedIf marks the result of a failed deploy or rollback as an error,
// keeping its steps and logs.
func failedIf(out deployOutcome) (*mcp.CallToolResult, any, error) {
	if out.OK {
		return nil, out, nil
	}
	res := &mcp.CallToolResult{IsError: true}
	return res, out, nil
}

func (s *MCPServer) deleteApp(ctx context.Context, _ *mcp.CallToolRequest, in appArg) (*mcp.CallToolResult, any, error) {
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	unlock, ok := deploy.LockApp(srv.ID, in.App)
	if !ok {
		return nil, nil, fmt.Errorf("%s is being deployed, rolled back, backed up or restored", in.App)
	}
	defer unlock()
	if err := s.apps().Delete(context.WithoutCancel(ctx), srv.ID, in.App); err != nil {
		return nil, nil, err
	}
	return nil, map[string]bool{"deleted": true}, nil
}
