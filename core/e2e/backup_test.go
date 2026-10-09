//go:build e2e

package e2e

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xusenlin/berth/core/app"
	"github.com/xusenlin/berth/core/backup"
	"github.com/xusenlin/berth/core/deploy"
	"github.com/xusenlin/berth/core/server"
	"github.com/xusenlin/berth/core/ssh"
)

// login connects as user and returns the app manager of that "server".
func login(t *testing.T, ctx context.Context, user string) *app.Manager {
	t.Helper()
	host, port := addr()
	c, err := ssh.Dial(ctx, ssh.Target{Host: host, Port: port, User: user, Auth: ssh.AuthPassword, Secret: "berth"})
	if err != nil {
		t.Skipf("no test server: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	env, err := server.Detect(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return &app.Manager{C: c, Env: env}
}

// TestBackupRestore backs up an app as root and restores it as the deploy
// user, which stands for another server: another home, user services and
// another port. Then it restores over the original app.
func TestBackupRestore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	src := login(t, ctx, "root")
	dst := login(t, ctx, "deploy")
	if err := server.EnableLinger(ctx, dst.C, dst.Env, "berth"); err != nil {
		t.Fatal(err)
	}
	_ = src.Delete(ctx, "bk")
	_ = dst.Delete(ctx, "bk2")

	used, _ := src.UsedPorts(ctx, "")
	port := app.PickPort(18200, 18299, used)
	if _, err := src.Create(ctx, app.App{
		Name: "bk", Entry: "testapp",
		Args:   "--http=0.0.0.0:{{port}} --data={{shared}}/data/starts.log",
		Port:   port,
		Env:    map[string]string{"GREETING": "hello"},
		Shared: []string{"data"},
		Health: app.Health{Type: app.HealthHTTP, Path: "/health", Timeout: 10},
		Note:   "备份测试",
	}); err != nil {
		t.Fatal(err)
	}
	d := &deploy.Deployer{ServerID: "src", M: src}
	if res, err := d.Deploy(ctx, "bk", build(t, src.Env.Arch, "v1", false), nil); err != nil || !res.OK {
		t.Fatalf("deploy: %v %+v", err, res)
	}
	if _, err := src.WriteShared(ctx, "bk", "config.yaml", "mode: original\n", 0); err != nil {
		t.Fatal(err)
	}

	// Back up, paused, with logs.
	file := filepath.Join(t.TempDir(), "bk.tar.gz")
	var steps []string
	res, err := backup.Create(ctx, src, "src", backup.ServerInfo{Name: "src", Host: "h", User: "root", Home: src.Env.Home, Mode: string(src.Env.Mode), Arch: src.Env.Arch},
		"bk", file, backup.Options{Pause: true, LogDays: 1}, func(e backup.Progress) { steps = append(steps, string(e.Stage)+":"+string(e.State)) })
	if err != nil {
		t.Fatalf("backup: %v\n%v", err, steps)
	}
	if !res.Manifest.Paused || res.Manifest.Release == "" || res.Size == 0 {
		t.Errorf("result = %+v", res)
	}
	if !strings.Contains(strings.Join(steps, " "), "resume:done") {
		t.Errorf("app not resumed: %v", steps)
	}
	if st, _ := src.Status(ctx, "bk"); !st.Running() {
		t.Errorf("source app not running after backup: %+v", st)
	}

	// Restore on the other "server" under another name and port.
	pv, err := backup.Inspect(ctx, dst, file, [2]int{18300, 18399})
	if err != nil {
		t.Fatal(err)
	}
	if !pv.ArchOK || pv.Exists || pv.App.Note != "备份测试" || pv.Info.Manifest.App != "bk" {
		t.Errorf("preview = %+v", pv)
	}
	used, _ = dst.UsedPorts(ctx, "")
	port2 := app.PickPort(18300, 18399, used)
	rr, err := backup.Restore(ctx, dst, "dst", file, backup.RestoreOptions{Name: "bk2", Port: port2, Start: true}, nil)
	if err != nil || !rr.OK || !rr.Started {
		t.Fatalf("restore: %v %+v", err, rr)
	}
	if got := get(t, dst.C, port2, "/health"); got != "ok v1" {
		t.Errorf("restored health = %q", got)
	}
	info, err := dst.Get(ctx, "bk2")
	if err != nil || info.App.Port != port2 || info.App.Note != "备份测试" || info.App.Env["GREETING"] != "hello" || !info.Status.Running() {
		t.Fatalf("restored app = %+v %v", info, err)
	}
	out, _ := dst.C.Output(ctx, "cat ~/senlinApps/bk2/shared/config.yaml; cat ~/senlinApps/bk2/shared/data/starts.log | wc -l; readlink ~/senlinApps/bk2/current/data")
	if !strings.Contains(out, "mode: original") || !strings.Contains(out, "/home/deploy/senlinApps/bk2/shared/data") {
		t.Errorf("restored files:\n%s", out)
	}

	// Restoring onto an existing app needs Overwrite, and keeps its shared/.
	if _, err := backup.Restore(ctx, dst, "dst", file, backup.RestoreOptions{Name: "bk2", Port: port2}, nil); err == nil {
		t.Error("restored over an existing app without Overwrite")
	}
	if _, err := src.WriteShared(ctx, "bk", "config.yaml", "mode: changed\n", mustModTime(t, ctx, src, "bk", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	rr, err = backup.Restore(ctx, src, "src", file, backup.RestoreOptions{Name: "bk", Port: port, Overwrite: true, Start: true}, nil)
	if err != nil || !rr.OK || rr.Kept == "" {
		t.Fatalf("overwrite: %v %+v", err, rr)
	}
	out, _ = src.C.Output(ctx, "cat ~/senlinApps/bk/shared/config.yaml; cat "+ssh.Quote(rr.Kept)+"/config.yaml")
	if out != "mode: original\nmode: changed\n" {
		t.Errorf("after overwrite: %q", out)
	}
	if got := get(t, src.C, port, "/health"); got != "ok v1" {
		t.Errorf("health after overwrite = %q", got)
	}

	// A port taken on the target is refused.
	if _, err := backup.Restore(ctx, dst, "dst", file, backup.RestoreOptions{Name: "bk3", Port: 22}, nil); err == nil || !strings.Contains(err.Error(), "占用") {
		t.Errorf("taken port: %v", err)
	}
	_ = src.Delete(ctx, "bk")
	_ = dst.Delete(ctx, "bk2")
}

func mustModTime(t *testing.T, ctx context.Context, m *app.Manager, name, path string) int64 {
	t.Helper()
	fc, err := m.ReadShared(ctx, name, path)
	if err != nil {
		t.Fatal(err)
	}
	return fc.ModTime
}
