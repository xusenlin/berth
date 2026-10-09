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
	"strings"
	"time"

	"github.com/xusenlin/berth/core/app"
	"github.com/xusenlin/berth/core/deploy"
	"github.com/xusenlin/berth/core/ssh"
)

// Preview is what restoring an archive to a server would do.
type Preview struct {
	Info *Contents `json:"info"`
	App  app.App   `json:"app"`
	// Exists is set when the server has an app of the archive's name.
	Exists     bool   `json:"exists"`
	ServerArch string `json:"serverArch"`
	// ArchOK is false when the archive's programs do not run on the server.
	ArchOK bool `json:"archOK"`
	// PortTaken says who holds the app's port on the server, "" when free.
	PortTaken string `json:"portTaken"`
	// Port is the port to restore with: the app's own when free, else a
	// free one of the server's range.
	Port     int      `json:"port"`
	Warnings []string `json:"warnings"`
}

// Inspect reads an archive and checks it against a server.
func Inspect(ctx context.Context, m *app.Manager, file string, portRange [2]int) (*Preview, error) {
	info, err := Read(file)
	if err != nil {
		return nil, err
	}
	pv := &Preview{Info: info, ServerArch: m.Env.Arch, Warnings: []string{}}
	if err := json.Unmarshal(info.App, &pv.App); err != nil {
		return nil, fmt.Errorf("备份中的 app.json 无法解析：%w", err)
	}
	pv.App.Name = info.Manifest.App
	pv.ArchOK = info.Manifest.Release == "" || deploy.ArchMatches(info.Manifest.Server.Arch, m.Env.Arch)
	apps, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range apps {
		if a.App.Name == pv.App.Name {
			pv.Exists = true
		}
	}
	pv.Port = pv.App.Port
	if pv.App.Port > 0 {
		pv.PortTaken, err = portOwner(ctx, m, pv.App.Port, pv.App.Name)
		if err != nil {
			return nil, err
		}
		if pv.PortTaken != "" {
			used, err := m.UsedPorts(ctx, "")
			if err != nil {
				return nil, err
			}
			pv.Port = app.PickPort(portRange[0], portRange[1], used)
		}
	}
	man := info.Manifest
	if man.Release == "" {
		pv.Warnings = append(pv.Warnings, "备份中没有程序，恢复后需要先发版才能启动")
	}
	if !man.Paused && man.Release != "" {
		pv.Warnings = append(pv.Warnings, "备份时应用没有暂停，若程序使用数据库，数据可能不完全一致")
	}
	if man.Server.Home != "" && man.Server.Home != m.Env.Home && strings.Contains(pv.App.Args+envValues(pv.App), man.Server.Home+"/") {
		pv.Warnings = append(pv.Warnings, fmt.Sprintf("启动参数或环境变量中含有原服务器的路径 %s，恢复后请在设置中修改", man.Server.Home))
	}
	return pv, nil
}

func envValues(a app.App) string {
	var b strings.Builder
	for _, v := range a.Env {
		b.WriteString(v)
		b.WriteByte('\n')
	}
	return b.String()
}

// portOwner says who other than the app named name holds a TCP port, ""
// when it is free or the app's own.
func portOwner(ctx context.Context, m *app.Manager, port int, name string) (string, error) {
	apps, err := m.List(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range apps {
		if a.App.Port != port {
			continue
		}
		if a.App.Name != name {
			return "应用 " + a.App.Name, nil
		}
		// The app being overwritten holds its own port: it stops first.
		return "", nil
	}
	used, err := m.UsedPorts(ctx, name)
	if err != nil {
		return "", err
	}
	if used[port] {
		return "其他程序", nil
	}
	return "", nil
}

// RestoreOptions choose how an archive is restored.
type RestoreOptions struct {
	// Name is the app's name on the server, by default the archive's.
	Name string `json:"name"`
	// Port replaces the app's port; 0 keeps none.
	Port int `json:"port"`
	// Overwrite allows replacing an existing app of that name. Its shared/
	// is kept in backups/, not deleted.
	Overwrite bool `json:"overwrite"`
	// Start starts the app once restored.
	Start bool `json:"start"`
}

// RestoreResult is how a restore went.
type RestoreResult struct {
	OK      bool   `json:"ok"`
	Name    string `json:"name"`
	Release string `json:"release"`
	// Kept is where an overwritten app's shared/ was moved.
	Kept    string `json:"kept,omitempty"`
	Started bool   `json:"started"`
	Error   string `json:"error,omitempty"`
	Logs    string `json:"logs,omitempty"`
}

// Restore puts an archive on a server as an app.
func Restore(ctx context.Context, m *app.Manager, serverID, file string, opts RestoreOptions, report func(Progress)) (RestoreResult, error) {
	if report == nil {
		report = func(Progress) {}
	}
	fail := func(step Stage, err error) (RestoreResult, error) {
		report(Progress{Stage: step, State: deploy.StateFailed, Message: err.Error()})
		return RestoreResult{Name: opts.Name, Error: err.Error()}, err
	}

	report(Progress{Stage: StageCheck, State: deploy.StateRunning})
	info, err := Read(file)
	if err != nil {
		return fail(StageCheck, err)
	}
	man := info.Manifest
	var a app.App
	if err := json.Unmarshal(info.App, &a); err != nil {
		return fail(StageCheck, fmt.Errorf("备份中的 app.json 无法解析：%w", err))
	}
	if opts.Name == "" {
		opts.Name = man.App
	}
	a.Name, a.Port, a.Current = opts.Name, opts.Port, man.Release
	if err := a.Normalize(); err != nil {
		return fail(StageCheck, err)
	}
	if man.Release != "" && !deploy.ArchMatches(man.Server.Arch, m.Env.Arch) {
		return fail(StageCheck, fmt.Errorf("备份中的程序是 %s 架构，这台服务器是 %s，无法运行", man.Server.Arch, m.Env.Arch))
	}
	unlock, ok := deploy.LockApp(serverID, a.Name)
	if !ok {
		return fail(StageCheck, fmt.Errorf("%s 正在发版、回滚或备份", a.Name))
	}
	defer unlock()
	_, getErr := m.Get(ctx, a.Name)
	exists := getErr == nil
	if !exists {
		if res, err := m.C.Run(ctx, "test -e "+ssh.Quote(app.PathsOf(m.Env, a.Name).Dir), nil); err == nil && res.Status == 0 {
			exists = true // a directory without a readable app.json
		}
	}
	if exists && !opts.Overwrite {
		return fail(StageCheck, fmt.Errorf("服务器上已有应用 %s，请换一个名称或选择覆盖", a.Name))
	}
	if a.Port > 0 {
		owner, err := portOwner(ctx, m, a.Port, a.Name)
		if err != nil {
			return fail(StageCheck, err)
		}
		if owner != "" {
			return fail(StageCheck, fmt.Errorf("端口 %d 已被%s占用", a.Port, owner))
		}
	}
	report(Progress{Stage: StageCheck, State: deploy.StateDone, Message: fmt.Sprintf("%d 个文件，%s", info.Files, humanBytes(info.DataSize))})

	// From here the server changes: finish even if the caller goes away.
	ctx = context.WithoutCancel(ctx)
	p := app.PathsOf(m.Env, a.Name)
	result := RestoreResult{Name: a.Name, Release: man.Release}

	report(Progress{Stage: StagePrepare, State: deploy.StateRunning})
	prep := "mkdir -p " + ssh.Quote(p.Releases) + " " + ssh.Quote(p.Backups)
	if exists {
		result.Kept = p.Backups + "/shared-before-restore-" + time.Now().Format("20060102-150405")
		prep = m.Env.Systemctl() + " stop " + ssh.Quote(a.Unit()) + " 2>/dev/null; " + prep +
			" && if [ -e " + ssh.Quote(p.Shared) + " ]; then mv " + ssh.Quote(p.Shared) + " " + ssh.Quote(result.Kept) + "; fi"
	}
	prep += " && mkdir -p " + ssh.Quote(p.Shared)
	if _, err := m.C.Output(ctx, prep); err != nil {
		r, e := fail(StagePrepare, err)
		r.Kept = result.Kept
		return r, e
	}
	msg := "创建应用目录"
	if exists {
		msg = "已停止原应用，原 shared/ 移到 " + result.Kept
	}
	report(Progress{Stage: StagePrepare, State: deploy.StateDone, Message: msg})

	report(Progress{Stage: StageUpload, State: deploy.StateRunning, Progress: 0})
	if err := upload(ctx, m, file, p.Dir, info.DataSize, func(f float64) {
		report(Progress{Stage: StageUpload, State: deploy.StateRunning, Progress: f})
	}); err != nil {
		r, e := fail(StageUpload, err)
		r.Kept = result.Kept
		return r, e
	}
	report(Progress{Stage: StageUpload, State: deploy.StateDone, Progress: 1})

	// Links into shared/ are made again: the old server's pointed at its
	// own home.
	report(Progress{Stage: StageLink, State: deploy.StateRunning})
	if len(a.Shared) == 0 || len(man.Releases) == 0 {
		report(Progress{Stage: StageLink, State: deploy.StateSkipped})
	} else {
		var script strings.Builder
		for _, id := range man.Releases {
			script.WriteString("( " + deploy.LinkScript(p.Releases+"/"+id, p.Shared, a.Shared) + ") || exit 1\n")
		}
		if _, err := m.C.Output(ctx, script.String()); err != nil {
			r, e := fail(StageLink, err)
			r.Kept = result.Kept
			return r, e
		}
		report(Progress{Stage: StageLink, State: deploy.StateDone, Message: strings.Join(a.Shared, ", ")})
	}

	report(Progress{Stage: StageSwitch, State: deploy.StateRunning})
	if man.Release == "" {
		_, _ = m.C.Output(ctx, "rm -f "+ssh.Quote(p.Current))
		report(Progress{Stage: StageSwitch, State: deploy.StateSkipped, Message: "备份中没有程序"})
	} else if _, err := m.C.Output(ctx, deploy.SwitchScript(p, man.Release)); err != nil {
		r, e := fail(StageSwitch, err)
		r.Kept = result.Kept
		return r, e
	} else {
		report(Progress{Stage: StageSwitch, State: deploy.StateDone, Message: man.Release})
	}

	report(Progress{Stage: StageConfigure, State: deploy.StateRunning})
	if err := m.Apply(ctx, a); err != nil {
		r, e := fail(StageConfigure, err)
		r.Kept = result.Kept
		return r, e
	}
	report(Progress{Stage: StageConfigure, State: deploy.StateDone, Message: "app.json、.env 与 systemd unit"})

	if !opts.Start || man.Release == "" {
		report(Progress{Stage: StageStart, State: deploy.StateSkipped})
		result.OK = true
		return result, nil
	}
	d := &deploy.Deployer{ServerID: serverID, M: m}
	report(Progress{Stage: StageStart, State: deploy.StateRunning})
	since, err := d.Start(ctx, a.Unit())
	if err != nil {
		report(Progress{Stage: StageStart, State: deploy.StateFailed, Message: err.Error()})
		result.Error, result.Logs = err.Error(), d.JournalTail(ctx, a.Unit(), since)
		return result, err
	}
	report(Progress{Stage: StageStart, State: deploy.StateDone})
	result.Started = true
	report(Progress{Stage: StageHealth, State: deploy.StateRunning})
	if err := deploy.Check(ctx, m, a); err != nil {
		report(Progress{Stage: StageHealth, State: deploy.StateFailed, Message: err.Error()})
		result.Error, result.Logs = err.Error(), d.JournalTail(ctx, a.Unit(), since)
		return result, err
	}
	report(Progress{Stage: StageHealth, State: deploy.StateDone})
	result.OK = true
	return result, nil
}

// upload streams the archive's shared/ and releases/ into dir on the
// server. Absolute symlinks of releases, the old server's links into
// shared/, are left out: the link step makes them again.
func upload(ctx context.Context, m *app.Manager, file, dir string, total int64, progress func(float64)) error {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(rewrite(file, pw, total, progress))
	}()
	q := ssh.Quote(dir)
	res, err := m.C.Run(ctx, "tar -xzf - --no-same-owner -C "+q, pr)
	pr.CloseWithError(errors.New("upload ended"))
	if err != nil {
		return fmt.Errorf("上传失败：%w", err)
	}
	if res.Status != 0 {
		return fmt.Errorf("解压失败：%s", strings.TrimSpace(res.Stderr))
	}
	return nil
}

func rewrite(file string, w io.Writer, total int64, progress func(float64)) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gzr)
	gzw, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	tw := tar.NewWriter(gzw)
	var done int64
	var last time.Time
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		k, clean, _, err := classify(h.Name)
		if err != nil {
			return err
		}
		if err := checkEntry(h, k, clean); err != nil {
			return err
		}
		if k != kindShared && k != kindRelease {
			continue
		}
		if h.Typeflag == tar.TypeSymlink && !linkInside(clean, h.Linkname) {
			continue
		}
		if h.Typeflag == tar.TypeXGlobalHeader || h.Typeflag == tar.TypeXHeader {
			continue
		}
		h.Name = clean
		if h.Typeflag == tar.TypeDir {
			h.Name += "/"
		}
		// Owners mean nothing on another server.
		h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
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
			progress(min(0.99, float64(done)/float64(total)))
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gzw.Close()
}
