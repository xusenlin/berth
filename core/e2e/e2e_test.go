//go:build e2e

// Package e2e tests the core packages against a real server: a container
// with systemd and sshd, built from testdata/e2e.
//
//	docker build -t berth-e2e testdata/e2e
//	docker run -d --name berth-e2e --privileged --cgroupns=host \
//	  -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock -p 2222:22 berth-e2e
//	go test -tags e2e ./core/e2e
package e2e

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xusenlin/berth/core/app"
	"github.com/xusenlin/berth/core/deploy"
	"github.com/xusenlin/berth/core/logs"
	"github.com/xusenlin/berth/core/server"
	"github.com/xusenlin/berth/core/ssh"
)

func addr() (string, int) {
	host, port := "127.0.0.1", 2222
	if v := os.Getenv("BERTH_E2E_HOST"); v != "" {
		host = v
	}
	if v := os.Getenv("BERTH_E2E_PORT"); v != "" {
		port, _ = strconv.Atoi(v)
	}
	return host, port
}

// build compiles testapp for the server's architecture.
func build(t *testing.T, arch, version string, broken bool) string {
	t.Helper()
	goarch := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[arch]
	if goarch == "" {
		t.Skipf("no Go architecture for %s", arch)
	}
	out := filepath.Join(t.TempDir(), "testapp")
	_, file, _, _ := runtime.Caller(0)
	src := filepath.Join(filepath.Dir(file), "..", "..", "testdata", "e2e", "testapp")
	ld := "-X main.version=" + version
	if broken {
		ld += " -X main.broken=true"
	}
	cmd := exec.Command("go", "build", "-ldflags", ld, "-o", out, ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goarch, "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build testapp: %v\n%s", err, b)
	}
	return out
}

// zipOf puts a binary into a zip under dir/name, with a default config.
func zipOf(t *testing.T, bin, dir, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "release.zip")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	h := &zip.FileHeader{Name: dir + "/" + name, Method: zip.Deflate}
	h.SetMode(0o755)
	w, _ := zw.CreateHeader(h)
	src, _ := os.Open(bin)
	io.Copy(w, src)
	src.Close()
	w, _ = zw.Create(dir + "/README.txt")
	io.WriteString(w, "hello")
	zw.Close()
	f.Close()
	return p
}

func get(t *testing.T, c *ssh.Client, port int, path string) string {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, a string) (net.Conn, error) {
		return c.DialRemote(ctx, a)
	}}}
	resp, err := hc.Get("http://127.0.0.1:" + strconv.Itoa(port) + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(b))
}

func TestRoot(t *testing.T) { run(t, "root", server.ModeRoot) }
func TestUser(t *testing.T) { run(t, "deploy", server.ModeUser) }

func run(t *testing.T, user string, mode server.Mode) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	host, port := addr()
	target := ssh.Target{Host: host, Port: port, User: user, Auth: ssh.AuthPassword, Secret: "berth"}

	// Host keys: the first connection learns it, a wrong one is refused.
	c, err := ssh.Dial(ctx, target)
	if err != nil {
		t.Skipf("no test server at %s:%d: %v", host, port, err)
	}
	fp := c.Fingerprint()
	c.Close()
	if !strings.HasPrefix(fp, "SHA256:") {
		t.Fatalf("fingerprint = %q", fp)
	}
	bad := target
	bad.HostKey = "SHA256:nope"
	var hk *ssh.HostKeyError
	if _, err := ssh.Dial(ctx, bad); !errors.As(err, &hk) || hk.Got != fp {
		t.Fatalf("wrong host key: %v", err)
	}
	wrongPw := target
	wrongPw.Secret = "wrong"
	wrongPw.HostKey = fp
	if _, err := ssh.Dial(ctx, wrongPw); err == nil {
		t.Fatal("wrong password accepted")
	}

	target.HostKey = fp
	pool := ssh.NewPool(func(string) (ssh.Target, error) { return target, nil }, nil)
	defer pool.CloseAll()
	c, err = pool.Get(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := pool.Get(ctx, "s1"); again != c {
		t.Error("pool dialed twice")
	}

	env, err := server.Detect(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("env: %+v", env)
	if env.Mode != mode || !env.Systemd || env.Arch == "" {
		t.Fatalf("env = %+v", env)
	}
	if mode == server.ModeUser {
		if env.Sudo != "passwd" {
			t.Errorf("sudo = %q", env.Sudo)
		}
		if err := server.EnableLinger(ctx, c, env, "berth"); err != nil {
			t.Fatal(err)
		}
		if env, err = server.Detect(ctx, c); err != nil || !env.Linger {
			t.Fatalf("linger not enabled: %+v %v", env, err)
		}
	}

	ov, err := server.Collect(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if ov.CPUs < 1 || ov.MemTotal <= 0 || ov.AppsDisk == nil || ov.OS == "" {
		t.Errorf("overview = %+v", ov)
	}

	m := &app.Manager{C: c, Env: env}
	_ = m.Delete(ctx, "e2e")
	used, err := m.UsedPorts(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !used[22] {
		t.Error("sshd's port 22 not seen as listening")
	}
	p := app.PickPort(18000, 18999, used)
	if mode == server.ModeUser {
		p = app.PickPort(18500, 18999, used) // apart from the root test's app
	}
	a, err := m.Create(ctx, app.App{
		Name:   "e2e",
		Entry:  "testapp",
		Args:   "--http=0.0.0.0:{{port}} --data={{shared}}/data/starts.log",
		Port:   p,
		Env:    map[string]string{"GREETING": `hi "there" $HOME`},
		Shared: []string{"data", "README.txt"},
		Health: app.Health{Type: app.HealthHTTP, Path: "/health", Timeout: 10},
		Backup: true,
		// Autostart stays on to check enable.
		Autostart:    true,
		KeepReleases: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, a); err == nil {
		t.Error("created the same app twice")
	}
	if err := m.Do(ctx, "e2e", app.ActionStart); err == nil {
		t.Error("started an app without a release")
	}

	d := &deploy.Deployer{ServerID: "s1", M: m}
	tick := time.Date(2026, 10, 9, 15, 0, 0, 0, time.Local)
	d.Now = func() time.Time { tick = tick.Add(time.Minute); return tick }
	var events []deploy.Event
	report := func(e deploy.Event) { events = append(events, e) }

	// v1: a zip with a top-level directory, which is stripped.
	v1 := zipOf(t, build(t, env.Arch, "v1", false), "testapp-linux", "testapp")
	res, err := d.Deploy(ctx, "e2e", v1, report)
	if err != nil || !res.OK {
		t.Fatalf("deploy v1: %v %+v\n%v", err, res, events)
	}
	if got := get(t, c, p, "/health"); got != "ok v1" {
		t.Fatalf("v1 health = %q", got)
	}
	rel1 := res.Release
	ls, err := server.Listeners(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range ls {
		if l.Port == p && l.Proto == "tcp" {
			found = l.App == "e2e" && l.Process == "testapp" && !l.Local
		}
	}
	if !found {
		t.Errorf("app's port not attributed: %+v", ls)
	}

	// The shared README was seeded from the release, the env file quoted.
	out, err := c.Output(ctx, "cat ~/senlinApps/e2e/shared/README.txt; readlink ~/senlinApps/e2e/current/README.txt")
	if err != nil || !strings.HasPrefix(out, "hello") || !strings.Contains(out, "/shared/README.txt") {
		t.Errorf("shared README: %q %v", out, err)
	}

	// v2: a bare binary. The shared data file keeps v1's line.
	v2 := build(t, env.Arch, "v2", false)
	if res, err = d.Deploy(ctx, "e2e", v2, report); err != nil || !res.OK || res.Previous != rel1 {
		t.Fatalf("deploy v2: %v %+v", err, res)
	}
	rel2 := res.Release
	if got := get(t, c, p, "/health"); got != "ok v2" {
		t.Fatalf("v2 health = %q", got)
	}
	out, _ = c.Output(ctx, "cat ~/senlinApps/e2e/shared/data/starts.log")
	if !strings.Contains(out, "v1 ") || !strings.Contains(out, "v2 ") {
		t.Errorf("starts.log = %q", out)
	}

	// v3 is broken: the deploy fails and v2 comes back, with its data.
	events = nil
	res, err = d.Deploy(ctx, "e2e", build(t, env.Arch, "v3", true), report)
	if err == nil || res.OK || !res.RolledBack {
		t.Fatalf("deploy v3 should fail and roll back: %v %+v", err, res)
	}
	if !strings.Contains(res.Logs, "broken build") {
		t.Errorf("logs of the failure = %q", res.Logs)
	}
	if got := get(t, c, p, "/health"); got != "ok v2" {
		t.Fatalf("after rollback health = %q", got)
	}
	info, err := m.Get(ctx, "e2e")
	if err != nil || info.Current != rel2 || info.App.Current != rel2 || !info.Status.Running() || !info.Status.Enabled {
		t.Fatalf("after rollback: %+v %v", info, err)
	}
	rels, cur, err := m.Releases(ctx, "e2e")
	if err != nil || len(rels) != 2 || cur != rel2 {
		t.Fatalf("releases = %+v %q %v", rels, cur, err)
	}
	if rels[0].Source != "testapp" || rels[1].Source != "release.zip" {
		t.Errorf("release sources = %+v", rels)
	}

	// Editing shared files: create, read, update, refuse stale writes and
	// escapes through symlinks.
	fc, err := m.WriteShared(ctx, "e2e", "conf/app.yaml", "a: 1\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.WriteShared(ctx, "e2e", "conf/app.yaml", "x", 0); err == nil {
		t.Error("created an existing file")
	}
	read, err := m.ReadShared(ctx, "e2e", "conf/app.yaml")
	if err != nil || read.Content != "a: 1\n" || !read.Exists {
		t.Fatalf("read: %+v %v", read, err)
	}
	if _, err := m.WriteShared(ctx, "e2e", "conf/app.yaml", "a: 2\n", fc.ModTime-5000); !errors.Is(err, app.ErrModified) {
		t.Errorf("stale write: %v", err)
	}
	if _, err := m.WriteShared(ctx, "e2e", "conf/app.yaml", "a: 2\n", read.ModTime); err != nil {
		t.Fatal(err)
	}
	if out, _ := c.Output(ctx, "cat ~/senlinApps/e2e/shared/conf/app.yaml"); out != "a: 2\n" {
		t.Errorf("on server: %q", out)
	}
	if _, err := c.Output(ctx, "ln -sfn /etc ~/senlinApps/e2e/shared/etc"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"etc/passwd", "../app.json", ".env", "/etc/passwd"} {
		if _, err := m.ReadShared(ctx, "e2e", bad); err == nil {
			t.Errorf("read %s: expected refusal", bad)
		}
		if _, err := m.WriteShared(ctx, "e2e", bad, "x", 0); err == nil {
			t.Errorf("write %s: expected refusal", bad)
		}
	}
	files, _, err := m.SharedFiles(ctx, "e2e")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Path)
	}
	if !slices.Contains(names, "conf/app.yaml") || !slices.Contains(names, "data/starts.log") || slices.Contains(names, ".env") {
		t.Errorf("shared files = %v", names)
	}

	// Manual rollback to v1.
	if res, err = d.Rollback(ctx, "e2e", rel1, nil); err != nil || !res.OK {
		t.Fatalf("rollback: %v %+v", err, res)
	}
	if got := get(t, c, p, "/health"); got != "ok v1" {
		t.Fatalf("after manual rollback health = %q", got)
	}

	// A wrong architecture is refused before anything is uploaded.
	other := map[string]string{"x86_64": "aarch64", "aarch64": "x86_64"}[env.Arch]
	if _, err := d.Deploy(ctx, "e2e", build(t, other, "vx", false), nil); err == nil || !strings.Contains(err.Error(), "架构不匹配") {
		t.Errorf("wrong arch: %v", err)
	}

	// Logs: history has the start line with its environment, follow gets
	// new ticks.
	hist, err := logs.History(ctx, c, env, "e2e", logs.Query{Keyword: "starting"})
	if err != nil || len(hist) == 0 {
		t.Fatalf("history: %d %v", len(hist), err)
	}
	if last := hist[len(hist)-1].Message; !strings.Contains(last, `GREETING=hi "there" $HOME`) {
		t.Errorf("start line = %q", last)
	}
	fctx, fcancel := context.WithTimeout(ctx, 10*time.Second)
	got := 0
	err = logs.Follow(fctx, c, env, "e2e", 5, func(e logs.Entry) error {
		if strings.HasPrefix(e.Message, "tick v1") {
			got++
			if got == 7 {
				return io.EOF
			}
		}
		return nil
	})
	fcancel()
	if !errors.Is(err, io.EOF) {
		t.Errorf("follow: %v after %d ticks", err, got)
	}

	// Lifecycle.
	if err := m.Do(ctx, "e2e", app.ActionStop); err != nil {
		t.Fatal(err)
	}
	if st, _ := m.Status(ctx, "e2e"); st.Running() {
		t.Errorf("still running after stop: %+v", st)
	}
	if err := m.Do(ctx, "e2e", app.ActionStart); err != nil {
		t.Fatal(err)
	}
	if err := m.SetAutostart(ctx, "e2e", false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	st, _ := m.Status(ctx, "e2e")
	if !st.Running() || st.Enabled || st.PID == 0 {
		t.Errorf("status = %+v", st)
	}
	list, err := m.List(ctx)
	if err != nil || len(list) == 0 {
		t.Fatalf("list: %v %v", list, err)
	}

	// Connection loss: the pool dials again.
	c.Close()
	c2, err := pool.Get(ctx, "s1")
	if err != nil || c2 == c {
		t.Fatalf("reconnect: %v", err)
	}
	m.C = c2
	if err := m.Delete(ctx, "e2e"); err != nil {
		t.Fatal(err)
	}
	if res, _ := c2.Run(ctx, "test -e ~/senlinApps/e2e", nil); res.Status == 0 {
		t.Error("app directory left behind")
	}
}
