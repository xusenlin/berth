package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/xusenlin/berth/core/app"
)

func TestBuildOf(t *testing.T) {
	vcs := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "01b4f77867b61f5bce0efc65f36a1f7cea512f9a"},
		{Key: "vcs.time", Value: "2026-10-09T08:54:55Z"},
		{Key: "vcs.modified", Value: "false"},
	}
	for _, c := range []struct {
		version string
		want    string
	}{
		{"v1.0.1", "v1.0.1 (01b4f77)"},
		{"v1.0.1+dirty", "v1.0.1 (01b4f77)"},
		{"v0.0.0-20261009085455-01b4f77867b6", "01b4f77"},
		{"v1.0.2-0.20261009085455-01b4f77867b6+dirty", "01b4f77"},
		{"(devel)", "01b4f77"},
	} {
		bi := &debug.BuildInfo{Main: debug.Module{Version: c.version}, Settings: vcs}
		if got := BuildLabel(buildOf(bi)); got != c.want {
			t.Errorf("%s: %q, want %q", c.version, got, c.want)
		}
	}
	if b := buildOf(&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}); b != nil {
		t.Errorf("no vcs = %+v", b)
	}
	if got := BuildLabel(&app.Build{Revision: "abc", Modified: true}); got != "abc，有未提交改动" {
		t.Errorf("modified = %q", got)
	}
}

// TestInspectReadsBuild builds a Go program from a tagged commit and reads
// its version back, alone and in archives.
func TestInspectReadsBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	for _, tool := range []string{"go", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not found")
		}
	}
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "go.mod"), []byte("module example.com/hello\n\ngo 1.24\n"), 0o644)
	os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	run := func(env []string, name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	git := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t"}
	run(nil, "git", "init", "-q")
	run(git, "git", "add", ".")
	run(git, "git", "commit", "-qm", "init")
	run(git, "git", "tag", "v1.2.3")
	bin := filepath.Join(t.TempDir(), "hello")
	run([]string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS="}, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", bin, ".")
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}

	for name, file := range map[string]string{
		"binary": bin,
		"zip":    writeZip(t, []file{{name: "hello-linux/hello", body: data, mode: 0o755}}),
		"tar.gz": writeTarGz(t, []file{{name: "hello-linux/hello", body: data, mode: 0o755}}),
	} {
		pkg, err := Inspect(file, "hello")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b := pkg.Build
		if b == nil || b.Version != "v1.2.3" || len(b.Revision) != 40 || b.Time == "" || b.Modified {
			t.Errorf("%s: build = %+v", name, b)
		}
	}
}
