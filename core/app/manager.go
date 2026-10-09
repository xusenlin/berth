package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xusenlin/berth/core/server"
	"github.com/xusenlin/berth/core/ssh"
)

// Manager manages the apps of one server.
type Manager struct {
	C   *ssh.Client
	Env server.Env
}

// Status is the runtime state of an app's unit.
type Status struct {
	// Installed is false when systemd does not know the unit.
	Installed bool `json:"installed"`
	// Active is systemd's ActiveState: active, inactive, failed,
	// activating, deactivating.
	Active   string `json:"active"`
	Sub      string `json:"sub"`
	PID      int    `json:"pid"`
	Enabled  bool   `json:"enabled"`
	Restarts int    `json:"restarts"`
	// Uptime is how long, in seconds, the unit has been active.
	Uptime int64 `json:"uptime"`
	// Memory is in bytes, -1 when systemd does not account it.
	Memory int64 `json:"memory"`
	// CPU is the CPU time used, in nanoseconds, -1 when not accounted.
	CPU int64 `json:"cpu"`
}

// Running reports whether the app is up or starting.
func (s Status) Running() bool {
	return s.Active == "active" || s.Active == "activating" || s.Active == "reloading"
}

// Info is an app with its state.
type Info struct {
	App     App    `json:"app"`
	Status  Status `json:"status"`
	Current string `json:"current"`
	// Error is set when app.json could not be read.
	Error string `json:"error,omitempty"`
}

const showProps = "LoadState,ActiveState,SubState,MainPID,ActiveEnterTimestampMonotonic,MemoryCurrent,CPUUsageNSec,UnitFileState,NRestarts"

func (m *Manager) systemctl(args ...string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = ssh.Quote(a)
	}
	return m.Env.Systemctl() + " " + strings.Join(q, " ")
}

// List returns the apps on the server, read from their app.json.
func (m *Manager) List(ctx context.Context) ([]Info, error) {
	script := `cd "$HOME/` + server.AppsDirName + `" 2>/dev/null || exit 0
for d in */; do
  n=${d%/}
  [ -f "$n/app.json" ] || continue
  echo "@@app $n"; cat "$n/app.json"; echo
  echo "@@current"; readlink "$n/current" 2>/dev/null
  echo "@@show"; ` + m.Env.Systemctl() + ` show "` + server.UnitPrefix + `$n.service" -p ` + showProps + ` 2>/dev/null
done
echo "@@uptime"; cat /proc/uptime
`
	out, err := m.C.Output(ctx, script)
	if err != nil {
		return nil, fmt.Errorf("读取应用列表：%w", err)
	}
	return parseList(out), nil
}

func parseList(out string) []Info {
	type part struct{ name, appJSON, current, show string }
	var parts []*part
	var cur *part
	var field *string
	var uptime string
	var b strings.Builder
	flush := func() {
		if field != nil {
			*field = b.String()
		}
		b.Reset()
	}
	for line := range strings.Lines(out) {
		l := strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(l, "@@app "):
			flush()
			cur = &part{name: strings.TrimPrefix(l, "@@app ")}
			parts = append(parts, cur)
			field = &cur.appJSON
			continue
		case l == "@@current" && cur != nil:
			flush()
			field = &cur.current
			continue
		case l == "@@show" && cur != nil:
			flush()
			field = &cur.show
			continue
		case l == "@@uptime":
			flush()
			field = &uptime
			continue
		}
		b.WriteString(line)
	}
	flush()
	now := uptimeMicros(uptime)
	infos := make([]Info, 0, len(parts))
	for _, p := range parts {
		info := Info{Current: releaseOf(p.current), Status: parseShow(p.show, now)}
		if err := json.Unmarshal([]byte(p.appJSON), &info.App); err != nil {
			info.App.Name = p.name
			info.Error = "app.json 无法解析：" + err.Error()
		}
		if info.App.Name != p.name {
			info.App.Name = p.name
		}
		infos = append(infos, info)
	}
	return infos
}

func releaseOf(link string) string {
	link = strings.TrimSpace(link)
	if i := strings.LastIndex(link, "/"); i >= 0 {
		return link[i+1:]
	}
	return link
}

func uptimeMicros(s string) int64 {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return int64(v * 1e6)
}

func parseShow(out string, nowMicros int64) Status {
	st := Status{Memory: -1, CPU: -1}
	props := map[string]string{}
	for line := range strings.Lines(out) {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[k] = v
		}
	}
	st.Installed = props["LoadState"] == "loaded"
	st.Active = props["ActiveState"]
	if st.Active == "" {
		st.Active = "inactive"
	}
	st.Sub = props["SubState"]
	st.PID, _ = strconv.Atoi(props["MainPID"])
	st.Enabled = props["UnitFileState"] == "enabled"
	st.Restarts, _ = strconv.Atoi(props["NRestarts"])
	if v, err := strconv.ParseInt(props["MemoryCurrent"], 10, 64); err == nil && v >= 0 {
		st.Memory = v
	}
	if v, err := strconv.ParseInt(props["CPUUsageNSec"], 10, 64); err == nil && v >= 0 {
		st.CPU = v
	}
	if since, err := strconv.ParseInt(props["ActiveEnterTimestampMonotonic"], 10, 64); err == nil && since > 0 && nowMicros > since && st.Running() {
		st.Uptime = (nowMicros - since) / 1e6
	}
	return st
}

// Get returns one app with its state.
func (m *Manager) Get(ctx context.Context, name string) (Info, error) {
	p := PathsOf(m.Env, name)
	script := "echo '@@app " + name + "'; cat " + ssh.Quote(p.AppJSON) + " || exit 3; echo" +
		"; echo @@current; readlink " + ssh.Quote(p.Current) + " 2>/dev/null" +
		"; echo @@show; " + m.systemctl("show", UnitName(name), "-p", showProps) + " 2>/dev/null" +
		"; echo @@uptime; cat /proc/uptime"
	res, err := m.C.Run(ctx, script, nil)
	if err != nil {
		return Info{}, err
	}
	if res.Status == 3 {
		return Info{}, fmt.Errorf("应用 %s 不存在", name)
	}
	infos := parseList(res.Stdout)
	if len(infos) != 1 {
		return Info{}, fmt.Errorf("读取应用 %s 失败", name)
	}
	return infos[0], nil
}

// Status returns the state of an app's unit.
func (m *Manager) Status(ctx context.Context, name string) (Status, error) {
	out, err := m.C.Output(ctx, m.systemctl("show", UnitName(name), "-p", showProps)+"; echo @@uptime; cat /proc/uptime")
	if err != nil {
		return Status{}, err
	}
	show, up, _ := strings.Cut(out, "@@uptime")
	return parseShow(show, uptimeMicros(up)), nil
}

// UsedPorts returns the ports of the server's apps and those listening.
func (m *Manager) UsedPorts(ctx context.Context, except string) (map[int]bool, error) {
	out, err := m.C.Output(ctx, "cat /proc/net/tcp /proc/net/tcp6 2>/dev/null; true")
	if err != nil {
		return nil, err
	}
	used := ParseListening(out)
	apps, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range apps {
		if a.App.Name != except && a.App.Port > 0 {
			used[a.App.Port] = true
		}
	}
	return used, nil
}

// Create sets up a new app: its directories, app.json, environment file
// and unit. It does not start it; it has no release yet.
func (m *Manager) Create(ctx context.Context, a App) (App, error) {
	if err := a.Normalize(); err != nil {
		return App{}, err
	}
	a.Current = ""
	p := PathsOf(m.Env, a.Name)
	res, err := m.C.Run(ctx, "test -e "+ssh.Quote(p.Dir), nil)
	if err != nil {
		return App{}, err
	}
	if res.Status == 0 {
		return App{}, fmt.Errorf("服务器上已存在 %s", p.Dir)
	}
	if a.Port > 0 {
		used, err := m.UsedPorts(ctx, a.Name)
		if err != nil {
			return App{}, err
		}
		if used[a.Port] {
			return App{}, fmt.Errorf("端口 %d 已被占用", a.Port)
		}
	}
	if _, err := m.C.Output(ctx, "mkdir -p "+ssh.Quote(p.Releases)+" "+ssh.Quote(p.Shared)+" "+ssh.Quote(p.Backups)); err != nil {
		return App{}, err
	}
	if err := m.Apply(ctx, a); err != nil {
		return App{}, err
	}
	return a, nil
}

// Update replaces the settings of an app, keeping its current release. A
// running app keeps its old settings until it restarts.
func (m *Manager) Update(ctx context.Context, a App) (App, error) {
	if err := a.Normalize(); err != nil {
		return App{}, err
	}
	old, err := m.Get(ctx, a.Name)
	if err != nil {
		return App{}, err
	}
	a.Current = old.App.Current
	if a.Port > 0 && a.Port != old.App.Port {
		used, err := m.UsedPorts(ctx, a.Name)
		if err != nil {
			return App{}, err
		}
		// The app's own process may hold its old port only.
		if used[a.Port] {
			return App{}, fmt.Errorf("端口 %d 已被占用", a.Port)
		}
	}
	if err := m.Apply(ctx, a); err != nil {
		return App{}, err
	}
	return a, nil
}

// Apply writes app.json, the environment file and the unit, then reloads
// systemd and applies autostart.
func (m *Manager) Apply(ctx context.Context, a App) error {
	p := PathsOf(m.Env, a.Name)
	if err := m.WriteJSON(a); err != nil {
		return err
	}
	if err := m.writeFile(p.EnvFile, []byte(RenderEnvFile(a)), 0o600); err != nil {
		return fmt.Errorf("写入环境变量文件：%w", err)
	}
	unitPath := m.Env.UnitDir() + "/" + a.Unit()
	if err := m.writeFile(unitPath, []byte(RenderUnit(a, m.Env)), 0o644); err != nil {
		return fmt.Errorf("写入 unit 文件：%w", err)
	}
	cmd := m.systemctl("daemon-reload")
	if a.Autostart {
		cmd += " && " + m.systemctl("enable", a.Unit())
	} else {
		cmd += " && " + m.systemctl("disable", a.Unit())
	}
	if _, err := m.C.Output(ctx, cmd); err != nil {
		return fmt.Errorf("systemd：%w", err)
	}
	return nil
}

// WriteJSON writes the app's app.json.
func (m *Manager) WriteJSON(a App) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	// app.json holds the environment variables, which may be secrets.
	return m.writeFile(PathsOf(m.Env, a.Name).AppJSON, append(data, '\n'), 0o600)
}

// writeFile replaces a file atomically through SFTP.
func (m *Manager) writeFile(path string, data []byte, mode os.FileMode) error {
	return WriteFile(m.C, path, data, mode)
}

// WriteFile replaces a remote file atomically through SFTP.
func WriteFile(c *ssh.Client, path string, data []byte, mode os.FileMode) error {
	sc, err := c.SFTP()
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.berth-%d.tmp", path, time.Now().UnixNano())
	f, err := sc.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("%s：%w", tmp, err)
	}
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		f.Close()
		sc.Remove(tmp)
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		sc.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		sc.Remove(tmp)
		return err
	}
	if err := sc.PosixRename(tmp, path); err != nil {
		sc.Remove(tmp)
		return fmt.Errorf("%s：%w", path, err)
	}
	return nil
}

// Delete stops an app and removes its unit and directory.
func (m *Manager) Delete(ctx context.Context, name string) error {
	if !nameRE.MatchString(name) {
		return errors.New("应用名无效")
	}
	p := PathsOf(m.Env, name)
	unit := UnitName(name)
	cmd := m.systemctl("disable", "--now", unit) + " 2>/dev/null; " +
		"rm -f " + ssh.Quote(m.Env.UnitDir()+"/"+unit) + "; " +
		m.systemctl("daemon-reload") + "; " +
		m.systemctl("reset-failed", unit) + " 2>/dev/null; " +
		"rm -rf " + ssh.Quote(p.Dir)
	if _, err := m.C.Output(ctx, cmd); err != nil {
		return fmt.Errorf("删除应用：%w", err)
	}
	return nil
}

// Action is a lifecycle command.
type Action string

const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
)

// Do starts, stops or restarts an app.
func (m *Manager) Do(ctx context.Context, name string, act Action) error {
	switch act {
	case ActionStart, ActionRestart:
		info, err := m.Get(ctx, name)
		if err != nil {
			return err
		}
		if info.Current == "" {
			return errors.New("还没有发布过版本，请先发版")
		}
	case ActionStop:
	default:
		return fmt.Errorf("未知操作 %q", act)
	}
	if _, err := m.C.Output(ctx, m.systemctl(string(act), UnitName(name))); err != nil {
		return fmt.Errorf("%s 失败：%w", act, err)
	}
	return nil
}

// SetAutostart enables or disables starting an app at boot.
func (m *Manager) SetAutostart(ctx context.Context, name string, on bool) error {
	info, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	a := info.App
	a.Autostart = on
	if err := m.WriteJSON(a); err != nil {
		return err
	}
	verb := "disable"
	if on {
		verb = "enable"
	}
	_, err = m.C.Output(ctx, m.systemctl(verb, UnitName(name)))
	return err
}

// ReleaseInfoFile, in a release directory, holds the name of the file the
// release was uploaded from.
const ReleaseInfoFile = ".berth-release"

// Releases returns the release directories of an app, newest first.
func (m *Manager) Releases(ctx context.Context, name string) ([]Release, string, error) {
	p := PathsOf(m.Env, name)
	out, err := m.C.Output(ctx, "cd "+ssh.Quote(p.Releases)+" 2>/dev/null || exit 0; for d in */; do d=${d%/}; [ -d \"$d\" ] || continue; "+
		"printf '%s\\t%s\\t%s\\n' \"$d\" \"$(du -sk \"$d\" | cut -f1)\" \"$(head -c 255 \"$d/"+ReleaseInfoFile+"\" 2>/dev/null | tr -d '\\n\\t')\"; done; "+
		"echo @@current; readlink ../current 2>/dev/null")
	if err != nil {
		return nil, "", err
	}
	list, cur, _ := strings.Cut(out, "@@current")
	return parseReleases(list, releaseOf(cur)), releaseOf(cur), nil
}

func parseReleases(list, current string) []Release {
	var rels []Release
	for line := range strings.Lines(list) {
		f := strings.SplitN(strings.TrimRight(line, "\n"), "\t", 3)
		if len(f) < 2 || f[0] == "" {
			continue
		}
		kb, _ := strconv.ParseInt(f[1], 10, 64)
		r := Release{ID: f[0], Size: kb * 1024, Current: f[0] == current}
		if len(f) == 3 {
			r.Source = f[2]
		}
		rels = append(rels, r)
	}
	slices.SortFunc(rels, func(a, b Release) int { return strings.Compare(b.ID, a.ID) })
	return rels
}

// Release is one release directory.
type Release struct {
	ID      string `json:"id"`
	Size    int64  `json:"size"`
	Current bool   `json:"current"`
	// Source is the name of the uploaded file, "" for releases Berth did
	// not record it for.
	Source string `json:"source"`
}
