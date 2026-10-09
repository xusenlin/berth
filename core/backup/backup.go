package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xusenlin/berth/core/app"
	"github.com/xusenlin/berth/core/deploy"
	"github.com/xusenlin/berth/core/ssh"
)

// Stage is a stage of a backup or restore.
type Stage string

const (
	StagePrepare   Stage = "prepare"
	StagePause     Stage = "pause"
	StageArchive   Stage = "archive"
	StageLogs      Stage = "logs"
	StageResume    Stage = "resume"
	StageSave      Stage = "save"
	StageCheck     Stage = "check"
	StageUpload    Stage = "upload"
	StageLink      Stage = "link"
	StageSwitch    Stage = "switch"
	StageConfigure Stage = "configure"
	StageStart     Stage = "start"
	StageHealth    Stage = "health"
)

// Progress reports the progress of a backup or restore, as deploy.Event does.
type Progress struct {
	Stage    Stage        `json:"step"`
	State    deploy.State `json:"state"`
	Message  string       `json:"message,omitempty"`
	Progress float64      `json:"progress,omitempty"`
}

// Options choose what a backup holds besides app.json, shared/ and the
// current release.
type Options struct {
	// AllReleases adds the releases kept on the server, not only the
	// current one.
	AllReleases bool `json:"allReleases"`
	// LogDays adds the last days of the app's log; 0 adds none.
	LogDays int `json:"logDays"`
	// Pause stops the app while its files are read, so that databases are
	// consistent, and starts it again.
	Pause bool `json:"pause"`
}

// Estimate is what a backup of an app would hold.
type Estimate struct {
	Release      string `json:"release"`
	Releases     int    `json:"releases"`
	SharedSize   int64  `json:"sharedSize"`
	ReleaseSize  int64  `json:"releaseSize"`  // the current release
	ReleasesSize int64  `json:"releasesSize"` // every release
	Running      bool   `json:"running"`
}

// EstimateOf measures what a backup of an app would hold.
func EstimateOf(ctx context.Context, m *app.Manager, name string) (Estimate, error) {
	info, err := m.Get(ctx, name)
	if err != nil {
		return Estimate{}, err
	}
	p := app.PathsOf(m.Env, name)
	out, err := m.C.Output(ctx, "cd "+ssh.Quote(p.Dir)+" && du -sk shared 2>/dev/null | cut -f1; du -sk releases 2>/dev/null | cut -f1; ls -1 releases 2>/dev/null | wc -l"+
		releaseSize(info.Current))
	if err != nil {
		return Estimate{}, err
	}
	f := strings.Fields(out)
	num := func(i int) int64 {
		if i >= len(f) {
			return 0
		}
		n, _ := strconv.ParseInt(f[i], 10, 64)
		return n
	}
	e := Estimate{Release: info.Current, Running: info.Status.Running()}
	e.SharedSize = num(0) * 1024
	e.ReleasesSize = num(1) * 1024
	e.Releases = int(num(2))
	e.ReleaseSize = num(3) * 1024
	return e, nil
}

func releaseSize(id string) string {
	if id == "" {
		return "; echo 0"
	}
	return "; du -sk " + ssh.Quote("releases/"+id) + " 2>/dev/null | cut -f1"
}

// Saved is a finished backup.
type Saved struct {
	Path     string   `json:"path"`
	Size     int64    `json:"size"`
	Manifest Manifest `json:"manifest"`
	// Warning is set when tar saw files change while it read them.
	Warning string `json:"warning,omitempty"`
}

// Create saves an app to the archive dest on this computer.
func Create(ctx context.Context, m *app.Manager, serverID string, server ServerInfo, name, dest string, opts Options, report func(Progress)) (Saved, error) {
	if report == nil {
		report = func(Progress) {}
	}
	unlock, ok := deploy.LockApp(serverID, name)
	if !ok {
		return Saved{}, fmt.Errorf("%s 正在发版、回滚或备份", name)
	}
	defer unlock()

	report(Progress{Stage: StagePrepare, State: deploy.StateRunning})
	info, err := m.Get(ctx, name)
	if err != nil {
		report(Progress{Stage: StagePrepare, State: deploy.StateFailed, Message: err.Error()})
		return Saved{}, err
	}
	est, err := EstimateOf(ctx, m, name)
	if err != nil {
		report(Progress{Stage: StagePrepare, State: deploy.StateFailed, Message: err.Error()})
		return Saved{}, err
	}
	p := app.PathsOf(m.Env, name)
	paths := []string{"app.json", "shared"}
	var releases []string
	total := est.SharedSize
	if opts.AllReleases {
		rels, _, err := m.Releases(ctx, name)
		if err != nil {
			return Saved{}, err
		}
		for _, r := range rels {
			releases = append(releases, r.ID)
		}
		if len(releases) > 0 {
			paths = append(paths, "releases")
		}
		total += est.ReleasesSize
	} else if info.Current != "" {
		releases = []string{info.Current}
		paths = append(paths, "releases/"+info.Current)
		total += est.ReleaseSize
	}
	if releases == nil {
		releases = []string{}
	}
	man := Manifest{
		Format:    FormatVersion,
		App:       name,
		Release:   info.Current,
		Releases:  releases,
		Server:    server,
		CreatedAt: time.Now(),
		Logs:      opts.LogDays > 0,
	}
	report(Progress{Stage: StagePrepare, State: deploy.StateDone, Message: fmt.Sprintf("约 %s，%d 个版本", humanBytes(total), len(releases))})

	// A paused app starts again as soon as its files are read, and also when
	// anything fails, even if ctx is cancelled.
	resume := func() {}
	if opts.Pause && info.Status.Running() {
		report(Progress{Stage: StagePause, State: deploy.StateRunning})
		if _, err := m.C.Output(ctx, m.Env.Systemctl()+" stop "+ssh.Quote(info.App.Unit())); err != nil {
			report(Progress{Stage: StagePause, State: deploy.StateFailed, Message: err.Error()})
			return Saved{}, err
		}
		man.Paused = true
		report(Progress{Stage: StagePause, State: deploy.StateDone})
		var once sync.Once
		resume = func() {
			once.Do(func() {
				report(Progress{Stage: StageResume, State: deploy.StateRunning})
				if _, err := m.C.Output(context.WithoutCancel(ctx), m.Env.Systemctl()+" start "+ssh.Quote(info.App.Unit())); err != nil {
					report(Progress{Stage: StageResume, State: deploy.StateFailed, Message: "启动失败：" + err.Error()})
					return
				}
				report(Progress{Stage: StageResume, State: deploy.StateDone})
			})
		}
		defer resume()
	}

	part := dest + ".part"
	out, err := os.Create(part)
	if err != nil {
		return Saved{}, err
	}
	keep := false
	defer func() {
		if !keep {
			out.Close()
			os.Remove(part)
		}
	}()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	if err := writeJSON(tw, manifestName, man); err != nil {
		return Saved{}, err
	}

	report(Progress{Stage: StageArchive, State: deploy.StateRunning, Progress: 0})
	q := make([]string, len(paths))
	for i, x := range paths {
		q[i] = ssh.Quote(x)
	}
	cmd := "cd " + ssh.Quote(p.Dir) + " && tar -czf - --exclude=shared/.env " + strings.Join(q, " ")
	var done int64
	var last time.Time
	res, err := m.C.Pipe(ctx, cmd, nil, func(r io.Reader) error {
		gzr, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("读取服务器数据：%w", err)
		}
		tr := tar.NewReader(gzr)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("读取服务器数据：%w", err)
			}
			k, clean, _, err := classify(h.Name)
			if err != nil {
				return err
			}
			if k == kindOther || k == kindManifest || k == kindLogs {
				continue
			}
			h.Name = clean
			if h.Typeflag == tar.TypeDir {
				h.Name += "/"
			}
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			n, err := io.Copy(tw, tr)
			if err != nil {
				return err
			}
			done += n
			if total > 0 && time.Since(last) > 150*time.Millisecond {
				last = time.Now()
				report(Progress{Stage: StageArchive, State: deploy.StateRunning, Progress: min(0.99, float64(done)/float64(total))})
			}
		}
	})
	if err != nil {
		report(Progress{Stage: StageArchive, State: deploy.StateFailed, Message: err.Error()})
		return Saved{}, err
	}
	result := Saved{Path: dest, Manifest: man}
	switch {
	case res.Status == 1: // GNU tar: some files changed while read
		result.Warning = "部分文件在打包时发生了变化，建议勾选“备份时暂停应用”"
		report(Progress{Stage: StageArchive, State: deploy.StateWarning, Message: result.Warning})
	case res.Status != 0:
		err := fmt.Errorf("打包失败：%s", strings.TrimSpace(res.Stderr))
		report(Progress{Stage: StageArchive, State: deploy.StateFailed, Message: err.Error()})
		return Saved{}, err
	}
	report(Progress{Stage: StageArchive, State: deploy.StateDone, Progress: 1, Message: humanBytes(done)})
	resume()

	if opts.LogDays > 0 {
		report(Progress{Stage: StageLogs, State: deploy.StateRunning})
		days := min(opts.LogDays, 365)
		logs, err := m.C.Output(ctx, m.Env.Journalctl(info.App.Unit())+" -o short-iso --since "+ssh.Quote(fmt.Sprintf("-%dd", days))+" 2>&1")
		if err != nil {
			report(Progress{Stage: StageLogs, State: deploy.StateWarning, Message: "导出日志失败：" + err.Error()})
		} else if err := writeFile(tw, logsName, []byte(logs)); err != nil {
			return Saved{}, err
		} else {
			report(Progress{Stage: StageLogs, State: deploy.StateDone, Message: fmt.Sprintf("最近 %d 天，%s", days, humanBytes(int64(len(logs))))})
		}
	}

	report(Progress{Stage: StageSave, State: deploy.StateRunning})
	if err := tw.Close(); err != nil {
		return Saved{}, err
	}
	if err := gz.Close(); err != nil {
		return Saved{}, err
	}
	if err := out.Close(); err != nil {
		return Saved{}, err
	}
	if err := os.Rename(part, dest); err != nil {
		return Saved{}, err
	}
	keep = true
	if st, err := os.Stat(dest); err == nil {
		result.Size = st.Size()
	}
	report(Progress{Stage: StageSave, State: deploy.StateDone, Message: humanBytes(result.Size)})
	return result, nil
}

func writeJSON(tw *tar.Writer, name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(tw, name, append(data, '\n'))
}

func writeFile(tw *tar.Writer, name string, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Now(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
