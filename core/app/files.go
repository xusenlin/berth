package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/xusenlin/berth/core/ssh"
)

// MaxEditSize is the largest shared file Berth edits.
const MaxEditSize = 1 << 20

// maxListed bounds the files listed, for shared directories holding data.
const maxListed = 2000

// SharedFile is a file or directory under an app's shared/.
type SharedFile struct {
	// Path is relative to shared/.
	Path string `json:"path"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
	// ModTime is in milliseconds since the epoch.
	ModTime int64 `json:"modTime"`
}

// FileContent is a shared file read for editing.
type FileContent struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	// Exists is false for a new file.
	Exists bool `json:"exists"`
}

// ErrModified is returned when a file changed on the server since it was
// read.
var ErrModified = errors.New("文件在你打开后已在服务器上被修改，请重新加载后再保存")

// SharedFiles lists an app's shared/ directory, leaving out .env, which
// Berth writes from app.json. Truncated is set when there were more files
// than listed.
func (m *Manager) SharedFiles(ctx context.Context, name string) (files []SharedFile, truncated bool, err error) {
	p := PathsOf(m.Env, name)
	cmd := "cd " + ssh.Quote(p.Shared) + " 2>/dev/null || exit 0; " +
		"find . -mindepth 1 -maxdepth 6 \\( -name .env -prune \\) -o \\( -type f -o -type d -o -type l \\) -printf '%y\\t%s\\t%T@\\t%P\\n' 2>/dev/null | head -n " + strconv.Itoa(maxListed+1)
	out, err := m.C.Output(ctx, cmd)
	if err != nil {
		return nil, false, err
	}
	for line := range strings.Lines(out) {
		f := strings.SplitN(strings.TrimRight(line, "\n"), "\t", 4)
		if len(f) != 4 || f[3] == "" {
			continue
		}
		size, _ := strconv.ParseInt(f[1], 10, 64)
		mt, _ := strconv.ParseFloat(f[2], 64)
		files = append(files, SharedFile{Path: f[3], Dir: f[0] == "d", Size: size, ModTime: int64(mt * 1000)})
	}
	if len(files) > maxListed {
		return files[:maxListed], true, nil
	}
	return files, false, nil
}

// cleanShared checks a path relative to shared/.
func cleanShared(rel string) (string, error) {
	rel = strings.TrimPrefix(strings.TrimSpace(rel), "./")
	if rel == "" || strings.HasPrefix(rel, "/") {
		return "", errors.New("路径无效")
	}
	c := path.Clean(rel)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") || strings.ContainsAny(c, "\x00\n") {
		return "", errors.New("路径必须在 shared/ 之内")
	}
	if c == ".env" {
		return "", errors.New(".env 由应用设置中的环境变量生成，请在设置中编辑")
	}
	return c, nil
}

// resolveShared returns the absolute path of a shared file, refusing paths
// that leave shared/ through symlinks. A missing file resolves through its
// directory.
func (m *Manager) resolveShared(name, rel string) (string, error) {
	rel, err := cleanShared(rel)
	if err != nil {
		return "", err
	}
	sc, err := m.C.SFTP()
	if err != nil {
		return "", err
	}
	root, err := sc.RealPath(PathsOf(m.Env, name).Shared)
	if err != nil {
		return "", fmt.Errorf("shared/：%w", err)
	}
	abs := root + "/" + rel
	real, err := sc.RealPath(abs)
	if err != nil {
		// A new file: its directory must resolve inside shared/.
		dir, derr := sc.RealPath(path.Dir(abs))
		if derr != nil {
			return abs, nil // the directory will be created inside root
		}
		real = dir + "/" + path.Base(abs)
	}
	if real != root && !strings.HasPrefix(real, root+"/") {
		return "", fmt.Errorf("%s 指向 shared/ 之外，不能在这里编辑", rel)
	}
	return real, nil
}

// ReadShared reads a text file of an app's shared/ for editing. A missing
// file reads as empty, for creating it.
func (m *Manager) ReadShared(ctx context.Context, name, rel string) (FileContent, error) {
	abs, err := m.resolveShared(name, rel)
	if err != nil {
		return FileContent{}, err
	}
	rel, _ = cleanShared(rel)
	sc, err := m.C.SFTP()
	if err != nil {
		return FileContent{}, err
	}
	fc := FileContent{Path: rel}
	st, err := sc.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return fc, nil
	}
	if err != nil {
		return fc, err
	}
	if st.IsDir() {
		return fc, errors.New("这是目录")
	}
	if st.Size() > MaxEditSize {
		return fc, fmt.Errorf("文件有 %d KiB，超过 1 MiB，不能在界面中编辑", st.Size()>>10)
	}
	f, err := sc.Open(abs)
	if err != nil {
		return fc, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxEditSize+1))
	if err != nil {
		return fc, err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return fc, errors.New("这是二进制文件，不能在界面中编辑")
	}
	fc.Content = string(data)
	fc.Size = st.Size()
	fc.ModTime = st.ModTime().UnixMilli()
	fc.Exists = true
	return fc, nil
}

// WriteShared saves a file of an app's shared/, creating it and its
// directories when missing. modTime is the file's ModTime when it was read,
// 0 for a new file: a different one on the server fails with ErrModified.
func (m *Manager) WriteShared(ctx context.Context, name, rel, content string, modTime int64) (FileContent, error) {
	if len(content) > MaxEditSize {
		return FileContent{}, errors.New("内容超过 1 MiB")
	}
	abs, err := m.resolveShared(name, rel)
	if err != nil {
		return FileContent{}, err
	}
	rel, _ = cleanShared(rel)
	sc, err := m.C.SFTP()
	if err != nil {
		return FileContent{}, err
	}
	mode := os.FileMode(0o644)
	st, err := sc.Stat(abs)
	switch {
	case err == nil:
		if st.IsDir() {
			return FileContent{}, errors.New("这是目录")
		}
		if modTime == 0 {
			return FileContent{}, fmt.Errorf("%s 已存在", rel)
		}
		if st.ModTime().UnixMilli()/1000 != modTime/1000 {
			return FileContent{}, ErrModified
		}
		mode = st.Mode().Perm()
	case errors.Is(err, os.ErrNotExist):
		if modTime != 0 {
			return FileContent{}, errors.New("文件已在服务器上被删除")
		}
		if err := sc.MkdirAll(path.Dir(abs)); err != nil {
			return FileContent{}, err
		}
	default:
		return FileContent{}, err
	}
	if err := WriteFile(m.C, abs, []byte(content), mode); err != nil {
		return FileContent{}, err
	}
	fc := FileContent{Path: rel, Content: content, Size: int64(len(content)), Exists: true}
	if st, err := sc.Stat(abs); err == nil {
		fc.ModTime = st.ModTime().UnixMilli()
	} else {
		fc.ModTime = time.Now().UnixMilli()
	}
	return fc, nil
}
