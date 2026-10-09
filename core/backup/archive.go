// Package backup saves an app to a local archive and restores it, on the
// same server or another one.
//
// An archive is a tar.gz laid out as the app's directory on its server:
//
//	manifest.json          what the archive holds, written first
//	app.json               the app's settings
//	shared/...             its persistent files, without .env
//	releases/<id>/...      the current release, or every release
//	logs/journal.txt       optionally, the app's recent log
package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

// FormatVersion is the version of the archive layout.
const FormatVersion = 1

// Manifest describes an archive.
type Manifest struct {
	Format int    `json:"format"`
	App    string `json:"app"`
	// Release is the release that was current, "" when there was none.
	Release string `json:"release"`
	// Releases are the releases in the archive.
	Releases  []string   `json:"releases"`
	Server    ServerInfo `json:"server"`
	CreatedAt time.Time  `json:"createdAt"`
	// Paused is set when the app was stopped while archived, so that its
	// data is consistent.
	Paused bool `json:"paused"`
	Logs   bool `json:"logs"`
}

// ServerInfo is where an archive comes from.
type ServerInfo struct {
	Name string `json:"name"`
	Host string `json:"host"`
	User string `json:"user"`
	Home string `json:"home"`
	Mode string `json:"mode"`
	// Arch is the server's uname -m: the releases' programs run on it.
	Arch string `json:"arch"`
}

const (
	manifestName = "manifest.json"
	appJSONName  = "app.json"
	logsName     = "logs/journal.txt"
)

// kind classifies a path of an archive.
type kind int

const (
	kindOther kind = iota
	kindManifest
	kindAppJSON
	kindShared
	kindRelease
	kindLogs
)

// classify checks a path of an archive and says what it is. release is the
// release ID of paths under releases/.
func classify(name string) (k kind, clean, release string, err error) {
	if strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return 0, "", "", fmt.Errorf("备份包含无效路径 %q", name)
	}
	c := path.Clean(strings.TrimPrefix(name, "./"))
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return 0, "", "", fmt.Errorf("备份包含越界路径 %q", name)
	}
	switch {
	case c == manifestName:
		return kindManifest, c, "", nil
	case c == appJSONName:
		return kindAppJSON, c, "", nil
	case c == "logs" || strings.HasPrefix(c, "logs/"):
		return kindLogs, c, "", nil
	case c == "shared" || strings.HasPrefix(c, "shared/"):
		if c == "shared/.env" {
			return kindOther, c, "", nil
		}
		return kindShared, c, "", nil
	case c == "releases":
		return kindRelease, c, "", nil
	case strings.HasPrefix(c, "releases/"):
		id, _, _ := strings.Cut(strings.TrimPrefix(c, "releases/"), "/")
		if id == "" || strings.ContainsAny(id, " '\"$`\\") {
			return 0, "", "", fmt.Errorf("备份包含无效的版本目录 %q", name)
		}
		return kindRelease, c, id, nil
	}
	return kindOther, c, "", nil
}

// linkInside reports whether a relative symlink at name stays inside the
// archive's own tree (shared/ or its release).
func linkInside(name, target string) bool {
	if strings.HasPrefix(target, "/") {
		return false
	}
	r := path.Clean(path.Join(path.Dir(name), target))
	return r != ".." && !strings.HasPrefix(r, "../")
}

// Info is what reading an archive found.
type Contents struct {
	Manifest Manifest `json:"manifest"`
	// App is app.json as the archive has it.
	App json.RawMessage `json:"-"`
	// Size is the archive's size, DataSize the size of its files.
	Size     int64 `json:"size"`
	DataSize int64 `json:"dataSize"`
	Files    int   `json:"files"`
}

// Read checks a whole archive and returns its manifest and app.json.
func Read(file string) (*Contents, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, errors.New("不是 Berth 备份文件（无法解压）")
	}
	tr := tar.NewReader(gz)
	info := &Contents{Size: st.Size()}
	seen := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("备份文件已损坏：%w", err)
		}
		k, name, _, err := classify(h.Name)
		if err != nil {
			return nil, err
		}
		if err := checkEntry(h, k, name); err != nil {
			return nil, err
		}
		switch k {
		case kindManifest:
			if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&info.Manifest); err != nil {
				return nil, fmt.Errorf("manifest.json 无法解析：%w", err)
			}
			seen = true
		case kindAppJSON:
			b, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return nil, err
			}
			info.App = b
		case kindShared, kindRelease:
			if h.Typeflag == tar.TypeReg {
				info.Files++
				info.DataSize += h.Size
			}
		}
	}
	if !seen {
		return nil, errors.New("不是 Berth 备份文件（缺少 manifest.json）")
	}
	if info.Manifest.Format > FormatVersion {
		return nil, fmt.Errorf("备份格式版本 %d 比当前 Berth 支持的新，请升级 Berth", info.Manifest.Format)
	}
	if info.App == nil {
		return nil, errors.New("备份缺少 app.json")
	}
	return info, nil
}

// checkEntry accepts the kinds of entries Berth writes: directories,
// regular files, and symlinks that stay inside the tree. Absolute symlinks
// in releases are the links to shared/ of the old server, which restores
// make again; they are dropped, not refused.
func checkEntry(h *tar.Header, k kind, name string) error {
	switch h.Typeflag {
	case tar.TypeDir, tar.TypeReg, tar.TypeRegA, tar.TypeXGlobalHeader, tar.TypeXHeader:
		return nil
	case tar.TypeSymlink:
		if linkInside(name, h.Linkname) {
			return nil
		}
		if k == kindRelease && strings.HasPrefix(h.Linkname, "/") {
			return nil
		}
		return fmt.Errorf("备份中的符号链接 %s 指向 %s，超出应用目录，已拒绝", name, h.Linkname)
	default:
		return fmt.Errorf("备份包含不支持的文件类型 %s", name)
	}
}
