package services

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/xusenlin/berth/core/app"
	"github.com/xusenlin/berth/core/backup"
	"github.com/xusenlin/berth/core/deploy"
	"github.com/xusenlin/berth/core/logs"
)

// DebugPick, set only by debug builds, answers file dialogs so that tests
// can drive the window.
var DebugPick func() string

// Apps manages the apps of servers.
type Apps struct{ B *Backend }

// List returns the apps of a server with their states.
func (a *Apps) List(ctx context.Context, serverID string) ([]app.Info, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return nil, err
	}
	return m.List(ctx)
}

// Get returns one app with its state.
func (a *Apps) Get(ctx context.Context, serverID, name string) (app.Info, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return app.Info{}, err
	}
	return m.Get(ctx, name)
}

// Presets returns the templates of new apps.
func (a *Apps) Presets() []app.Preset { return app.Presets() }

// SuggestPort returns a free port in the server's range, 0 when none is.
func (a *Apps) SuggestPort(ctx context.Context, serverID string) (int, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return 0, err
	}
	srv, _ := a.B.Store.Server(serverID)
	used, err := m.UsedPorts(ctx, "")
	if err != nil {
		return 0, err
	}
	return app.PickPort(srv.PortRange[0], srv.PortRange[1], used), nil
}

// Create sets up a new app on a server.
func (a *Apps) Create(ctx context.Context, serverID string, spec app.App) (app.App, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return app.App{}, err
	}
	return m.Create(ctx, spec)
}

// Update replaces an app's settings. A running app picks them up when it
// restarts.
func (a *Apps) Update(ctx context.Context, serverID string, spec app.App) (app.App, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return app.App{}, err
	}
	return m.Update(ctx, spec)
}

// SetNote replaces an app's note and returns it as saved.
func (a *Apps) SetNote(ctx context.Context, serverID, name, note string) (string, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return "", err
	}
	return m.SetNote(ctx, name, note)
}

// Delete stops an app and removes it, with its releases and data.
func (a *Apps) Delete(ctx context.Context, serverID, name string) error {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return err
	}
	return m.Delete(ctx, name)
}

// Do starts, stops or restarts an app.
func (a *Apps) Do(ctx context.Context, serverID, name string, action app.Action) error {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return err
	}
	return m.Do(ctx, name, action)
}

// SetAutostart turns starting at boot on or off.
func (a *Apps) SetAutostart(ctx context.Context, serverID, name string, on bool) error {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return err
	}
	return m.SetAutostart(ctx, name, on)
}

// Releases returns an app's releases, newest first.
func (a *Apps) Releases(ctx context.Context, serverID, name string) ([]app.Release, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return nil, err
	}
	rels, _, err := m.Releases(ctx, name)
	return rels, err
}

// PickRelease asks for a release file: an executable, a zip or a tar.gz.
// It returns "" when canceled.
func (a *Apps) PickRelease(ctx context.Context) (string, error) {
	if DebugPick != nil {
		return DebugPick(), nil
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: mygo.CallerWindow(ctx),
		Title:  "选择要发布的文件（可执行文件、zip 或 tar.gz）",
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// Check inspects a release file before it is deployed.
func (a *Apps) Check(ctx context.Context, serverID, name, file string) (*deploy.Preflight, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return nil, err
	}
	info, err := m.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	d := &deploy.Deployer{ServerID: serverID, M: m}
	return d.Check(ctx, info.App, file)
}

// Deploy uploads a release and switches to it, streaming its progress.
func (a *Apps) Deploy(ctx context.Context, serverID, name, file string, progress *mygo.Channel[deploy.Event]) (deploy.Result, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return deploy.Result{}, err
	}
	d := &deploy.Deployer{ServerID: serverID, M: m}
	// A closed page must not stop the deploy midway: ignore send errors.
	return outcome(d.Deploy(ctx, name, file, func(e deploy.Event) { _ = progress.Send(e) }))
}

// outcome keeps the result of a failed deploy, with its logs and whether it
// rolled back, rather than only the error.
func outcome(res deploy.Result, err error) (deploy.Result, error) {
	if err != nil {
		res.OK = false
		res.Error = err.Error()
	}
	return res, nil
}

// Rollback switches an app to an earlier release, streaming the progress.
func (a *Apps) Rollback(ctx context.Context, serverID, name, release string, progress *mygo.Channel[deploy.Event]) (deploy.Result, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return deploy.Result{}, err
	}
	d := &deploy.Deployer{ServerID: serverID, M: m}
	return outcome(d.Rollback(ctx, name, release, func(e deploy.Event) { _ = progress.Send(e) }))
}

// FollowLogs streams the last lines of an app's log, then new ones, until
// the page closes the channel.
func (a *Apps) FollowLogs(ctx context.Context, serverID, name string, lines int, entries *mygo.Channel[logs.Entry]) error {
	c, env, err := a.B.client(ctx, serverID)
	if err != nil {
		return err
	}
	err = logs.Follow(ctx, c, env, name, lines, entries.Send)
	if err == mygo.ErrChannelClosed || ctx.Err() != nil {
		return nil
	}
	return err
}

// QueryLogs returns past lines of an app's log.
func (a *Apps) QueryLogs(ctx context.Context, serverID, name string, q logs.Query) ([]logs.Entry, error) {
	c, env, err := a.B.client(ctx, serverID)
	if err != nil {
		return nil, err
	}
	return logs.History(ctx, c, env, name, q)
}

// Open opens http://host:port of an app in the default browser.
func (a *Apps) Open(serverID string, port int) error {
	srv, ok := a.B.Store.Server(serverID)
	if !ok {
		return fmt.Errorf("服务器不存在")
	}
	if port <= 0 {
		return fmt.Errorf("应用没有端口")
	}
	return mygo.Shell.OpenExternal("http://" + net.JoinHostPort(srv.Host, strconv.Itoa(port)))
}

// SharedFiles is the listing of an app's shared/ directory.
type SharedFiles struct {
	Files []app.SharedFile `json:"files"`
	// Truncated is set when the directory has more files than listed.
	Truncated bool `json:"truncated"`
}

// ListShared lists the files of an app's shared/ directory.
func (a *Apps) ListShared(ctx context.Context, serverID, name string) (SharedFiles, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return SharedFiles{}, err
	}
	files, truncated, err := m.SharedFiles(ctx, name)
	if files == nil {
		files = []app.SharedFile{}
	}
	return SharedFiles{Files: files, Truncated: truncated}, err
}

// ReadShared reads a text file of an app's shared/ directory.
func (a *Apps) ReadShared(ctx context.Context, serverID, name, path string) (app.FileContent, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return app.FileContent{}, err
	}
	return m.ReadShared(ctx, name, path)
}

// WriteShared saves a text file of an app's shared/ directory. modTime is
// the one ReadShared returned, 0 to create the file.
func (a *Apps) WriteShared(ctx context.Context, serverID, name, path, content string, modTime int64) (app.FileContent, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return app.FileContent{}, err
	}
	return m.WriteShared(ctx, name, path, content, modTime)
}

// BackupEstimate measures what a backup of an app would hold.
func (a *Apps) BackupEstimate(ctx context.Context, serverID, name string) (backup.Estimate, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return backup.Estimate{}, err
	}
	return backup.EstimateOf(ctx, m, name)
}

// PickBackupDest asks where to save a backup of an app. It returns "" when
// canceled.
func (a *Apps) PickBackupDest(ctx context.Context, serverID, name string) (string, error) {
	srv, _ := a.B.Store.Server(serverID)
	file := safeFileName(srv.Name) + "-" + name + "-" + time.Now().Format("20060102-1504") + ".tar.gz"
	if DebugPick != nil {
		return DebugPick(), nil
	}
	dir, _ := mygo.App.Path(mygo.PathDownloads)
	return mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       "保存备份",
		DefaultPath: filepath.Join(dir, file),
		Filters:     []mygo.FileFilter{{Name: "Berth 备份", Extensions: []string{"gz"}}},
	})
}

func safeFileName(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>| `, r) {
			return '-'
		}
		return r
	}, s)
}

// Backup saves an app to dest on this computer, streaming the progress.
func (a *Apps) Backup(ctx context.Context, serverID, name, dest string, opts backup.Options, progress *mygo.Channel[backup.Progress]) (backup.Saved, error) {
	c, env, err := a.B.client(ctx, serverID)
	if err != nil {
		return backup.Saved{}, err
	}
	srv, _ := a.B.Store.Server(serverID)
	info := backup.ServerInfo{Name: srv.Name, Host: srv.Host, User: env.User, Home: env.Home, Mode: string(env.Mode), Arch: env.Arch}
	return backup.Create(ctx, &app.Manager{C: c, Env: env}, serverID, info, name, dest, opts, func(e backup.Progress) { _ = progress.Send(e) })
}

// PickBackupFile asks for a backup to restore. It returns "" when canceled.
func (a *Apps) PickBackupFile(ctx context.Context) (string, error) {
	if DebugPick != nil {
		return DebugPick(), nil
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:  mygo.CallerWindow(ctx),
		Title:   "选择 Berth 备份",
		Filters: []mygo.FileFilter{{Name: "Berth 备份", Extensions: []string{"gz", "tgz"}}},
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// InspectBackup reads a backup and checks it against a server.
func (a *Apps) InspectBackup(ctx context.Context, serverID, file string) (*backup.Preview, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return nil, err
	}
	srv, _ := a.B.Store.Server(serverID)
	return backup.Inspect(ctx, m, file, srv.PortRange)
}

// Restore puts a backup on a server as an app, streaming the progress.
func (a *Apps) Restore(ctx context.Context, serverID, file string, opts backup.RestoreOptions, progress *mygo.Channel[backup.Progress]) (backup.RestoreResult, error) {
	m, err := a.B.manager(ctx, serverID)
	if err != nil {
		return backup.RestoreResult{}, err
	}
	res, err := backup.Restore(ctx, m, serverID, file, opts, func(e backup.Progress) { _ = progress.Send(e) })
	if err != nil {
		// The result tells what was done before the failure.
		res.OK, res.Error = false, err.Error()
	}
	return res, nil
}

// Reveal shows a file in Finder or Explorer.
func (a *Apps) Reveal(path string) { mygo.Shell.ShowItemInFolder(path) }
