package deploy

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fakeELF is the start of an x86-64 ELF executable.
func fakeELF(machine uint16) []byte {
	b := make([]byte, 128)
	copy(b, "\x7fELF")
	b[4], b[5] = 2, 1 // 64-bit, little endian
	b[18], b[19] = byte(machine), byte(machine>>8)
	return b
}

type file struct {
	name string
	body []byte
	mode int64
	link string
	dir  bool
}

func writeZip(t *testing.T, files []file) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "r.zip")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	for _, fl := range files {
		h := &zip.FileHeader{Name: fl.name, Method: zip.Deflate}
		mode := os.FileMode(fl.mode)
		if mode == 0 {
			mode = 0o644
		}
		if fl.dir {
			mode |= os.ModeDir
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(fl.body)
	}
	zw.Close()
	f.Close()
	return p
}

func writeTarGz(t *testing.T, files []file) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "r.tar.gz")
	f, _ := os.Create(p)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, fl := range files {
		h := &tar.Header{Name: fl.name, Mode: fl.mode, Size: int64(len(fl.body)), Typeflag: tar.TypeReg}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if fl.dir {
			h.Typeflag, h.Size = tar.TypeDir, 0
		}
		if fl.link != "" {
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, fl.link, 0
		}
		tw.WriteHeader(h)
		tw.Write(fl.body)
	}
	tw.Close()
	gz.Close()
	f.Close()
	return p
}

// untar lists a tar.gz as name → content (or "->target" for links, "/"
// for directories).
func untar(t *testing.T, data []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	m := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return m
		}
		if err != nil {
			t.Fatal(err)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			m[h.Name] = "/"
		case tar.TypeSymlink:
			m[h.Name] = "->" + h.Linkname
		default:
			b, _ := io.ReadAll(tr)
			if h.Mode&0o111 != 0 {
				m[h.Name] = "x:" + string(b[:min(len(b), 4)])
			} else {
				m[h.Name] = string(b)
			}
		}
	}
}

func keys(m map[string]string) string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return strings.Join(k, ",")
}

func TestInspectBinary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "myapp-linux-amd64")
	os.WriteFile(p, fakeELF(62), 0o644)
	pkg, err := Inspect(p, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Kind != KindBinary || pkg.Arch != "x86_64" || pkg.Files != 1 || pkg.Size != 128 {
		t.Fatalf("pkg = %+v", pkg)
	}
	var buf bytes.Buffer
	if err := pkg.WriteTarGz(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if got := untar(t, buf.Bytes()); got["myapp"] != "x:\x7fELF" || len(got) != 1 {
		t.Errorf("tar = %q", got)
	}
}

func TestInspectRejectsOtherPrograms(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"macho": {0xcf, 0xfa, 0xed, 0xfe, 0, 0, 0, 0},
		"pe":    []byte("MZ\x90\x00"),
		"text":  []byte("hello"),
	}
	for name, body := range cases {
		p := filepath.Join(dir, name)
		os.WriteFile(p, body, 0o755)
		if _, err := Inspect(p, "app"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	script := filepath.Join(dir, "run.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o755)
	pkg, err := Inspect(script, "run.sh")
	if err != nil || pkg.Arch != "" {
		t.Errorf("script: %+v %v", pkg, err)
	}
}

func TestInspectZipStripsTopDir(t *testing.T) {
	p := writeZip(t, []file{
		{name: "myapp-v1/", dir: true},
		{name: "myapp-v1/myapp", body: fakeELF(183), mode: 0o755},
		{name: "myapp-v1/config.yaml", body: []byte("a: 1")},
		{name: "myapp-v1/static/index.html", body: []byte("<h1>")},
	})
	pkg, err := Inspect(p, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Stripped != "myapp-v1" || pkg.Arch != "aarch64" || pkg.Files != 3 {
		t.Fatalf("pkg = %+v", pkg)
	}
	var buf bytes.Buffer
	if err := pkg.WriteTarGz(&buf, nil); err != nil {
		t.Fatal(err)
	}
	got := untar(t, buf.Bytes())
	if keys(got) != "config.yaml,myapp,static/index.html" || got["config.yaml"] != "a: 1" {
		t.Errorf("tar = %q", got)
	}
}

func TestInspectTarGz(t *testing.T) {
	p := writeTarGz(t, []file{
		{name: "./", dir: true},
		{name: "./pocketbase", body: fakeELF(62), mode: 0o755},
		{name: "./pb_migrations/", dir: true},
		{name: "./pb_migrations/1_init.js", body: []byte("migrate()")},
		{name: "./latest", link: "pocketbase"},
	})
	pkg, err := Inspect(p, "pocketbase")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Stripped != "" || pkg.Arch != "x86_64" {
		t.Fatalf("pkg = %+v", pkg)
	}
	var buf bytes.Buffer
	var last int64
	if err := pkg.WriteTarGz(&buf, func(n int64) { last = n }); err != nil {
		t.Fatal(err)
	}
	if last != pkg.Size {
		t.Errorf("progress ended at %d of %d", last, pkg.Size)
	}
	got := untar(t, buf.Bytes())
	if keys(got) != "latest,pb_migrations/,pb_migrations/1_init.js,pocketbase" || got["latest"] != "->pocketbase" || got["pb_migrations/1_init.js"] != "migrate()" {
		t.Errorf("tar = %q", got)
	}
}

func TestInspectRejectsTraversal(t *testing.T) {
	cases := [][]file{
		{{name: "app", body: fakeELF(62)}, {name: "../evil", body: []byte("x")}},
		{{name: "app", body: fakeELF(62)}, {name: "/etc/passwd", body: []byte("x")}},
		{{name: "app", body: fakeELF(62)}, {name: "a/../../evil", body: []byte("x")}},
	}
	for i, files := range cases {
		if _, err := Inspect(writeZip(t, files), "app"); err == nil {
			t.Errorf("zip case %d: expected error", i)
		}
		if _, err := Inspect(writeTarGz(t, files), "app"); err == nil {
			t.Errorf("tar case %d: expected error", i)
		}
	}
	links := [][]file{
		{{name: "app", body: fakeELF(62)}, {name: "l", link: "/etc"}},
		{{name: "app", body: fakeELF(62)}, {name: "d/l", link: "../../etc"}},
	}
	for i, files := range links {
		if _, err := Inspect(writeTarGz(t, files), "app"); err == nil {
			t.Errorf("link case %d: expected error", i)
		}
	}
	if _, err := Inspect(writeZip(t, []file{{name: "other", body: fakeELF(62)}}), "app"); err == nil {
		t.Error("missing entry: expected error")
	}
}

func TestArchMatches(t *testing.T) {
	ok := [][2]string{{"x86_64", "x86_64"}, {"aarch64", "arm64"}, {"arm", "armv7l"}, {"", "x86_64"}, {"i686", "i386"}}
	for _, c := range ok {
		if !ArchMatches(c[0], c[1]) {
			t.Errorf("%v should match", c)
		}
	}
	if ArchMatches("x86_64", "aarch64") {
		t.Error("x86_64 should not run on aarch64")
	}
}
