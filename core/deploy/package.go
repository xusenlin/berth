// Package deploy uploads releases of apps, switches to them, checks their
// health and rolls back when they fail.
package deploy

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/xusenlin/berth/core/app"
)

// Kind is the kind of file a release comes from.
type Kind string

const (
	KindBinary Kind = "binary"
	KindZip    Kind = "zip"
	KindTarGz  Kind = "tar.gz"
)

// Package is a release prepared on this computer: its files, checked.
type Package struct {
	Path  string `json:"path"`
	Kind  Kind   `json:"kind"`
	Files int    `json:"files"`
	// Size is the uncompressed size of the files.
	Size int64 `json:"size"`
	// Arch is the architecture of the entry, in uname -m terms, or "" for
	// a script.
	Arch string `json:"arch"`
	// Stripped is the top-level directory removed from the archive's paths.
	Stripped string `json:"stripped,omitempty"`
	// Build is the build info of the entry when it is a Go program.
	Build *app.Build `json:"build,omitempty"`

	entries []entry
	entry   string
}

type entry struct {
	name     string // relative path in the release
	mode     fs.FileMode
	size     int64
	link     string // symlink target
	isDir    bool
	isLink   bool
	open     func() (io.ReadCloser, error)
	headOnly []byte
	data     []byte // the whole file, kept for the build info of a possible entry
}

// KindOf guesses the kind of a release file from its name.
func KindOf(p string) Kind {
	lower := strings.ToLower(p)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return KindZip
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return KindTarGz
	default:
		return KindBinary
	}
}

// Inspect reads a release file: a single executable, which becomes the
// entry, or a zip or tar.gz archive that contains the entry. It rejects
// paths that escape the release and an entry that is not a Linux program.
func Inspect(file, entryName string) (*Package, error) {
	st, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, errors.New("请选择文件而不是目录")
	}
	pkg := &Package{Path: file, Kind: KindOf(file), entry: entryName}
	switch pkg.Kind {
	case KindBinary:
		pkg.entries = []entry{{
			name: entryName,
			mode: 0o755,
			size: st.Size(),
			open: func() (io.ReadCloser, error) { return os.Open(file) },
		}}
	case KindZip:
		err = pkg.readZip()
	case KindTarGz:
		err = pkg.readTarGz()
	}
	if err != nil {
		return nil, err
	}
	pkg.strip()
	var ent *entry
	for i := range pkg.entries {
		e := &pkg.entries[i]
		if !e.isDir {
			pkg.Files++
			pkg.Size += e.size
		}
		if e.name == entryName {
			ent = e
		}
	}
	if ent == nil {
		return nil, fmt.Errorf("压缩包中找不到入口文件 %q", entryName)
	}
	if ent.isDir || ent.isLink {
		return nil, fmt.Errorf("入口 %q 不是普通文件", entryName)
	}
	head, err := pkg.head(ent)
	if err != nil {
		return nil, err
	}
	pkg.Arch, err = programArch(head)
	if err != nil {
		return nil, fmt.Errorf("入口 %q：%w", entryName, err)
	}
	if pkg.Arch != "" {
		pkg.Build = pkg.readBuild(ent)
	}
	return pkg, nil
}

func (p *Package) head(e *entry) ([]byte, error) {
	if e.headOnly != nil {
		return e.headOnly, nil
	}
	rc, err := e.open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	buf := make([]byte, 64)
	n, err := io.ReadFull(rc, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:n], nil
}

// cleanName checks a path from an archive and returns it relative to the
// release, or "" for the archive's root.
func cleanName(name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') {
		return "", fmt.Errorf("压缩包包含绝对路径 %q，已拒绝", name)
	}
	c := path.Clean(name)
	if c == "." {
		return "", nil
	}
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("压缩包包含越界路径 %q，已拒绝", name)
	}
	return c, nil
}

// checkLink rejects symlinks that point outside the release.
func checkLink(name, target string) error {
	if strings.HasPrefix(target, "/") {
		return fmt.Errorf("压缩包中的符号链接 %q 指向绝对路径 %q，已拒绝", name, target)
	}
	resolved := path.Clean(path.Join(path.Dir(name), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("压缩包中的符号链接 %q 指向目录之外，已拒绝", name)
	}
	return nil
}

func (p *Package) readZip() error {
	zr, err := zip.OpenReader(p.Path)
	if err != nil {
		return fmt.Errorf("读取 zip：%w", err)
	}
	defer zr.Close() // files reopen the archive when they upload
	for _, f := range zr.File {
		name, err := cleanName(f.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		mode := f.Mode()
		e := entry{name: name, mode: mode.Perm(), size: int64(f.UncompressedSize64)}
		switch {
		case mode.IsDir():
			e.isDir = true
		case mode&fs.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return err
			}
			target, err := io.ReadAll(io.LimitReader(rc, 4096))
			rc.Close()
			if err != nil {
				return err
			}
			if err := checkLink(name, string(target)); err != nil {
				return err
			}
			e.isLink, e.link, e.size = true, string(target), 0
		case mode.IsRegular():
			zipPath, name := p.Path, f.Name
			e.open = func() (io.ReadCloser, error) { return openZipFile(zipPath, name) }
		default:
			return fmt.Errorf("压缩包包含不支持的文件类型 %q", f.Name)
		}
		if e.mode == 0 && !e.isLink {
			e.mode = 0o644
			if e.isDir {
				e.mode = 0o755
			}
		}
		p.entries = append(p.entries, e)
	}
	return nil
}

type zipFileReader struct {
	io.ReadCloser
	zr *zip.ReadCloser
}

func (z zipFileReader) Close() error {
	err := z.ReadCloser.Close()
	z.zr.Close()
	return err
}

func openZipFile(zipPath, name string) (io.ReadCloser, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				zr.Close()
				return nil, err
			}
			return zipFileReader{rc, zr}, nil
		}
	}
	zr.Close()
	return nil, fmt.Errorf("zip 中找不到 %s", name)
}

// readTarGz lists the archive. Its files are read again, in order, when
// the package is written, so only the entry's first bytes are kept.
func (p *Package) readTarGz() error {
	f, err := os.Open(p.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("读取 tar.gz：%w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 tar.gz：%w", err)
		}
		name, err := cleanName(h.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		e := entry{name: name, mode: fs.FileMode(h.Mode).Perm(), size: h.Size}
		switch h.Typeflag {
		case tar.TypeDir:
			e.isDir = true
		case tar.TypeSymlink:
			if err := checkLink(name, h.Linkname); err != nil {
				return err
			}
			e.isLink, e.link, e.size = true, h.Linkname, 0
		case tar.TypeReg, tar.TypeRegA:
			if p.mayBeEntry(name) && h.Size <= maxBuildRead {
				data, err := io.ReadAll(tr)
				if err != nil {
					return fmt.Errorf("读取 tar.gz：%w", err)
				}
				e.headOnly = data[:min(len(data), 64)]
				if bytes.HasPrefix(data, []byte("\x7fELF")) {
					e.data = data
				}
				break
			}
			buf := make([]byte, 64)
			n, _ := io.ReadFull(tr, buf)
			e.headOnly = buf[:n]
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		default:
			return fmt.Errorf("压缩包包含不支持的文件类型 %q（硬链接或设备文件）", h.Name)
		}
		p.entries = append(p.entries, e)
	}
	return nil
}

// mayBeEntry reports whether name is the entry, before or after strip.
func (p *Package) mayBeEntry(name string) bool {
	_, rest, _ := strings.Cut(name, "/")
	return name == p.entry || rest == p.entry
}

// strip removes a single top-level directory holding everything, as in
// myapp-linux-amd64/myapp, when the entry is not at the root.
func (p *Package) strip() {
	if p.Kind == KindBinary {
		return
	}
	var top string
	hasEntry := false
	for _, e := range p.entries {
		if e.name == p.entry {
			hasEntry = true
		}
		first, _, _ := strings.Cut(e.name, "/")
		if top == "" {
			top = first
		} else if first != top {
			return
		}
	}
	if hasEntry || top == "" {
		return
	}
	inside := false
	for _, e := range p.entries {
		if strings.HasPrefix(e.name, top+"/") {
			inside = true
			break
		}
	}
	if !inside {
		return // a single file, not a directory
	}
	kept := p.entries[:0]
	for _, e := range p.entries {
		rest, ok := strings.CutPrefix(e.name, top+"/")
		if !ok {
			continue // the directory itself
		}
		e.name = rest
		kept = append(kept, e)
	}
	p.entries = kept
	p.Stripped = top
}

// WriteTarGz writes the package's files as a tar.gz to w, calling progress
// with the bytes of files written so far.
func (p *Package) WriteTarGz(w io.Writer, progress func(done int64)) error {
	gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	tw := tar.NewWriter(gz)
	var done int64
	write := func(e entry, r io.Reader) error {
		h := &tar.Header{Name: e.name, Mode: int64(e.mode), Size: e.size, Typeflag: tar.TypeReg}
		if e.name == p.entry {
			h.Mode |= 0o755
		}
		switch {
		case e.isDir:
			h.Typeflag, h.Name, h.Size = tar.TypeDir, e.name+"/", 0
		case e.isLink:
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, e.link, 0
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if r == nil {
			return nil
		}
		n, err := io.Copy(tw, &countingReader{r: io.LimitReader(r, e.size), n: &done, report: progress})
		if err != nil {
			return err
		}
		if n != e.size {
			return fmt.Errorf("%s 在读取时改变了大小", e.name)
		}
		return nil
	}

	if p.Kind == KindTarGz {
		if err := p.copyTarGz(write); err != nil {
			return err
		}
	} else {
		for _, e := range p.entries {
			var r io.ReadCloser
			if e.open != nil && !e.isDir && !e.isLink {
				var err error
				if r, err = e.open(); err != nil {
					return err
				}
			}
			var err error
			if r != nil {
				err = write(e, r)
				r.Close()
			} else {
				err = write(e, nil)
			}
			if err != nil {
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// copyTarGz reads the archive again, writing the entries Inspect kept.
func (p *Package) copyTarGz(write func(entry, io.Reader) error) error {
	byName := map[string]entry{}
	for _, e := range p.entries {
		byName[e.name] = e
	}
	f, err := os.Open(p.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := cleanName(h.Name)
		if err != nil {
			return err
		}
		if p.Stripped != "" {
			rest, ok := strings.CutPrefix(name, p.Stripped+"/")
			if !ok {
				continue
			}
			name = rest
		}
		e, ok := byName[name]
		if !ok {
			continue
		}
		var r io.Reader
		if !e.isDir && !e.isLink {
			r = tr
		}
		if err := write(e, r); err != nil {
			return err
		}
	}
}

type countingReader struct {
	r      io.Reader
	n      *int64
	report func(int64)
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	*c.n += int64(n)
	if c.report != nil && n > 0 {
		c.report(*c.n)
	}
	return n, err
}

// programArch returns the architecture of an ELF executable in uname -m
// terms, "" for a script, or an error for anything else.
func programArch(head []byte) (string, error) {
	switch {
	case bytes.HasPrefix(head, []byte("#!")):
		return "", nil
	case len(head) >= 20 && bytes.HasPrefix(head, []byte("\x7fELF")):
		var order binary.ByteOrder = binary.LittleEndian
		if head[5] == 2 {
			order = binary.BigEndian
		}
		machine := order.Uint16(head[18:20])
		if a, ok := elfArch[machine]; ok {
			return a, nil
		}
		return fmt.Sprintf("elf-%d", machine), nil
	case len(head) >= 4 && (bytes.Equal(head[:4], []byte{0xcf, 0xfa, 0xed, 0xfe}) || bytes.Equal(head[:4], []byte{0xca, 0xfe, 0xba, 0xbe}) || bytes.Equal(head[:4], []byte{0xce, 0xfa, 0xed, 0xfe})):
		return "", errors.New("这是 macOS 程序，请交叉编译 Linux 版本（如 GOOS=linux GOARCH=amd64 go build）")
	case bytes.HasPrefix(head, []byte("MZ")):
		return "", errors.New("这是 Windows 程序，请交叉编译 Linux 版本")
	default:
		return "", errors.New("不是 Linux 可执行文件（ELF）或脚本")
	}
}

var elfArch = map[uint16]string{
	3:   "i686",
	8:   "mips",
	20:  "ppc",
	21:  "ppc64",
	22:  "s390x",
	40:  "arm",
	62:  "x86_64",
	183: "aarch64",
	243: "riscv64",
	258: "loongarch64",
}

// ArchMatches reports whether a program of arch runs on a server whose
// uname -m is server.
func ArchMatches(arch, server string) bool {
	if arch == "" {
		return true
	}
	norm := func(a string) string {
		switch {
		case a == "amd64":
			return "x86_64"
		case a == "arm64":
			return "aarch64"
		case strings.HasPrefix(a, "armv"), a == "armhf", a == "armel":
			return "arm"
		case a == "i386", a == "i486", a == "i586", a == "x86":
			return "i686"
		case a == "ppc64le":
			return "ppc64"
		case strings.HasPrefix(a, "mips"):
			return "mips"
		}
		return a
	}
	return norm(arch) == norm(server)
}
