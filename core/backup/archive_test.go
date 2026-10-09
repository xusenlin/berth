package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type entry struct {
	name, body, link string
	dir              bool
}

func archive(t *testing.T, entries []entry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "b.tar.gz")
	f, _ := os.Create(p)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.dir:
			h.Typeflag, h.Size, h.Mode = tar.TypeDir, 0, 0o755
		case e.link != "":
			h.Typeflag, h.Size, h.Linkname = tar.TypeSymlink, 0, e.link
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		io.WriteString(tw, e.body)
	}
	tw.Close()
	gz.Close()
	f.Close()
	return p
}

var good = []entry{
	{name: "manifest.json", body: `{"format":1,"app":"web","release":"20261009-151800","releases":["20261009-151800"]}`},
	{name: "app.json", body: `{"name":"web","entry":"web"}`},
	{name: "shared/", dir: true},
	{name: "shared/.env", body: "SECRET=1"},
	{name: "shared/data/db.sqlite", body: "sqlite"},
	{name: "releases/20261009-151800/web", body: "\x7fELF"},
	{name: "releases/20261009-151800/data", link: "/root/senlinApps/web/shared/data"},
	{name: "releases/20261009-151800/latest", link: "web"},
	{name: "logs/journal.txt", body: "log"},
}

func TestRead(t *testing.T) {
	info, err := Read(archive(t, good))
	if err != nil {
		t.Fatal(err)
	}
	if info.Manifest.App != "web" || info.Files != 2 || info.DataSize != int64(len("sqlite")+4) || !strings.Contains(string(info.App), `"entry":"web"`) {
		t.Errorf("info = %+v", info)
	}
}

func TestReadRejects(t *testing.T) {
	cases := map[string][]entry{
		"no manifest":       good[1:],
		"traversal":         append(append([]entry{}, good...), entry{name: "shared/../../etc/passwd", body: "x"}),
		"absolute":          append(append([]entry{}, good...), entry{name: "/etc/passwd", body: "x"}),
		"shared link out":   append(append([]entry{}, good...), entry{name: "shared/etc", link: "/etc"}),
		"relative link out": append(append([]entry{}, good...), entry{name: "shared/up", link: "../../.."}),
		"newer format":      append([]entry{{name: "manifest.json", body: `{"format":99}`}}, good[1:]...),
		"bad release id":    append(append([]entry{}, good...), entry{name: "releases/a b/x", body: "x"}),
	}
	for name, entries := range cases {
		if _, err := Read(archive(t, entries)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	notArchive := filepath.Join(t.TempDir(), "x.tar.gz")
	os.WriteFile(notArchive, []byte("hello"), 0o644)
	if _, err := Read(notArchive); err == nil {
		t.Error("plain file accepted")
	}
}

func TestRewrite(t *testing.T) {
	var buf bytes.Buffer
	if err := rewrite(archive(t, good), &buf, 10, func(float64) {}); err != nil {
		t.Fatal(err)
	}
	gz, _ := gzip.NewReader(&buf)
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	sort.Strings(names)
	got := strings.Join(names, ",")
	// No manifest, app.json, logs or .env; no link to the old server's
	// shared/.
	want := "releases/20261009-151800/latest,releases/20261009-151800/web,shared/,shared/data/db.sqlite"
	if got != want {
		t.Errorf("rewritten = %s\nwant        %s", got, want)
	}
}
