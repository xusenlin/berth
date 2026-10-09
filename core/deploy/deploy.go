package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/xusenlin/berth/core/app"
	"github.com/xusenlin/berth/core/ssh"
)

// Step is a stage of a deploy.
type Step string

const (
	StepCheck    Step = "check"
	StepUpload   Step = "upload"
	StepLink     Step = "link"
	StepStop     Step = "stop"
	StepBackup   Step = "backup"
	StepSwitch   Step = "switch"
	StepStart    Step = "start"
	StepHealth   Step = "health"
	StepRollback Step = "rollback"
	StepCleanup  Step = "cleanup"
)

// State is how a step went.
type State string

const (
	StateRunning State = "running"
	StateDone    State = "done"
	StateSkipped State = "skipped"
	StateFailed  State = "failed"
	StateWarning State = "warning"
)

// Event reports the progress of a deploy.
type Event struct {
	Step    Step   `json:"step"`
	State   State  `json:"state"`
	Message string `json:"message,omitempty"`
	// Progress is the share of the upload done, from 0 to 1.
	Progress float64 `json:"progress,omitempty"`
}

// Result is the outcome of a deploy or rollback.
type Result struct {
	OK      bool   `json:"ok"`
	Release string `json:"release"`
	// Previous is the release that was current before.
	Previous string `json:"previous"`
	// RolledBack is set when the release failed and the previous one was
	// restored.
	RolledBack bool   `json:"rolledBack"`
	Error      string `json:"error,omitempty"`
	// Logs are the last lines of the journal when the release failed.
	Logs string `json:"logs,omitempty"`
}

// Preflight is what Berth checks before a deploy.
type Preflight struct {
	Package    *Package `json:"package"`
	ServerArch string   `json:"serverArch"`
	DiskFree   int64    `json:"diskFree"`
	Warnings   []string `json:"warnings"`
}

// keepBackups is how many backups of shared/ each app keeps.
const keepBackups = 3

var locks sync.Map // server/app → *sync.Mutex

func lock(key string) (unlock func(), ok bool) {
	v, _ := locks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}

// Deployer deploys apps of one server.
type Deployer struct {
	ServerID string
	M        *app.Manager
	// Now gives release IDs; tests replace it.
	Now func() time.Time
}

func (d *Deployer) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Deployer) sh(ctx context.Context, cmd string) error {
	_, err := d.M.C.Output(ctx, cmd)
	return err
}

func (d *Deployer) systemctl(args ...string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = ssh.Quote(a)
	}
	return d.M.Env.Systemctl() + " " + strings.Join(q, " ")
}

// Check inspects a release file for an app and the server's free space.
func (d *Deployer) Check(ctx context.Context, a app.App, file string) (*Preflight, error) {
	pkg, err := Inspect(file, a.Entry)
	if err != nil {
		return nil, err
	}
	pf := &Preflight{Package: pkg, ServerArch: d.M.Env.Arch, Warnings: []string{}}
	if !ArchMatches(pkg.Arch, d.M.Env.Arch) {
		return nil, fmt.Errorf("架构不匹配：程序是 %s，服务器是 %s", pkg.Arch, d.M.Env.Arch)
	}
	out, err := d.M.C.Output(ctx, "df -Pk "+ssh.Quote(d.M.Env.AppsDir())+" | awk 'NR==2{print $4}'")
	if err == nil {
		var kb int64
		fmt.Sscan(strings.TrimSpace(out), &kb)
		pf.DiskFree = kb * 1024
		need := pkg.Size*2 + 50<<20
		if pf.DiskFree < need {
			pf.Warnings = append(pf.Warnings, fmt.Sprintf("服务器剩余空间仅 %s，发版需要约 %s", humanBytes(pf.DiskFree), humanBytes(need)))
		}
	}
	if pkg.Arch == "" {
		pf.Warnings = append(pf.Warnings, "入口是脚本，未校验架构")
	}
	return pf, nil
}

// Deploy uploads file as a new release of the app named name and switches
// to it, rolling back when it fails its health check.
func (d *Deployer) Deploy(ctx context.Context, name, file string, report func(Event)) (Result, error) {
	unlock, ok := lock(d.ServerID + "/" + name)
	if !ok {
		return Result{}, fmt.Errorf("%s 正在发版或回滚", name)
	}
	defer unlock()
	if report == nil {
		report = func(Event) {}
	}

	report(Event{Step: StepCheck, State: StateRunning})
	info, err := d.M.Get(ctx, name)
	if err != nil {
		report(Event{Step: StepCheck, State: StateFailed, Message: err.Error()})
		return Result{}, err
	}
	a := info.App
	pf, err := d.Check(ctx, a, file)
	if err != nil {
		report(Event{Step: StepCheck, State: StateFailed, Message: err.Error()})
		return Result{}, err
	}
	msg := fmt.Sprintf("%d 个文件，%s", pf.Package.Files, humanBytes(pf.Package.Size))
	if pf.Package.Arch != "" {
		msg += "，" + pf.Package.Arch
	}
	report(Event{Step: StepCheck, State: StateDone, Message: msg})
	for _, w := range pf.Warnings {
		report(Event{Step: StepCheck, State: StateWarning, Message: w})
	}

	p := app.PathsOf(d.M.Env, name)
	id, err := d.newReleaseID(ctx, p)
	if err != nil {
		return Result{}, err
	}
	res := Result{Release: id, Previous: info.Current}
	rel := p.Releases + "/" + id

	// Upload. Until the switch, a failure only needs the new release gone.
	report(Event{Step: StepUpload, State: StateRunning, Progress: 0})
	if err := d.upload(ctx, pf.Package, rel, a.Entry, func(f float64) {
		report(Event{Step: StepUpload, State: StateRunning, Progress: f})
	}); err != nil {
		_ = d.sh(context.WithoutCancel(ctx), "rm -rf "+ssh.Quote(rel))
		report(Event{Step: StepUpload, State: StateFailed, Message: err.Error()})
		return res, err
	}
	report(Event{Step: StepUpload, State: StateDone, Progress: 1})

	report(Event{Step: StepLink, State: StateRunning})
	if len(a.Shared) == 0 {
		report(Event{Step: StepLink, State: StateSkipped})
	} else if err := d.sh(ctx, linkScript(rel, p.Shared, a.Shared)); err != nil {
		_ = d.sh(context.WithoutCancel(ctx), "rm -rf "+ssh.Quote(rel))
		report(Event{Step: StepLink, State: StateFailed, Message: err.Error()})
		return res, err
	} else {
		report(Event{Step: StepLink, State: StateDone, Message: strings.Join(a.Shared, ", ")})
	}

	// From here on the service is touched: finish even if the caller goes
	// away, so the server is never left half switched.
	ctx = context.WithoutCancel(ctx)

	// Settings may have changed since the unit was written.
	if err := d.M.Apply(ctx, a); err != nil {
		_ = d.sh(ctx, "rm -rf "+ssh.Quote(rel))
		report(Event{Step: StepStop, State: StateFailed, Message: err.Error()})
		return res, err
	}

	report(Event{Step: StepStop, State: StateRunning})
	if info.Status.Running() {
		if err := d.sh(ctx, d.systemctl("stop", a.Unit())); err != nil {
			report(Event{Step: StepStop, State: StateFailed, Message: err.Error()})
			_ = d.sh(ctx, "rm -rf "+ssh.Quote(rel)+"; "+d.systemctl("start", a.Unit()))
			return res, err
		}
		report(Event{Step: StepStop, State: StateDone})
	} else {
		report(Event{Step: StepStop, State: StateSkipped, Message: "未在运行"})
	}

	backup := ""
	report(Event{Step: StepBackup, State: StateRunning})
	if !a.Backup {
		report(Event{Step: StepBackup, State: StateSkipped, Message: "未开启备份"})
	} else {
		backup = p.Backups + "/" + id + ".tar.gz"
		if err := d.sh(ctx, backupScript(p, backup)); err != nil {
			report(Event{Step: StepBackup, State: StateFailed, Message: err.Error()})
			d.restoreAfterFailedBackup(ctx, a, rel, info.Status.Running())
			return res, fmt.Errorf("备份 shared/ 失败：%w", err)
		}
		report(Event{Step: StepBackup, State: StateDone, Message: "backups/" + id + ".tar.gz"})
	}

	report(Event{Step: StepSwitch, State: StateRunning})
	if err := d.sh(ctx, switchScript(p, id)); err != nil {
		report(Event{Step: StepSwitch, State: StateFailed, Message: err.Error()})
		d.restoreAfterFailedBackup(ctx, a, rel, info.Status.Running())
		return res, err
	}
	report(Event{Step: StepSwitch, State: StateDone, Message: id})

	var since string // when the release started, in the server's clock
	failed := func(step Step, err error) (Result, error) {
		report(Event{Step: step, State: StateFailed, Message: err.Error()})
		res.Error = err.Error()
		res.Logs = d.journalTail(ctx, a.Unit(), since)
		d.rollback(ctx, a, res.Previous, id, backup, info.Status.Running(), &res, report)
		return res, err
	}

	report(Event{Step: StepStart, State: StateRunning})
	if since, err = d.start(ctx, a.Unit()); err != nil {
		return failed(StepStart, err)
	}
	report(Event{Step: StepStart, State: StateDone})

	report(Event{Step: StepHealth, State: StateRunning, Message: healthLabel(a)})
	if err := Check(ctx, d.M, a); err != nil {
		return failed(StepHealth, err)
	}
	report(Event{Step: StepHealth, State: StateDone, Message: healthLabel(a)})

	a.Current = id
	if err := d.M.WriteJSON(a); err != nil {
		report(Event{Step: StepCleanup, State: StateWarning, Message: "更新 app.json 失败：" + err.Error()})
	}
	report(Event{Step: StepCleanup, State: StateRunning})
	if removed, err := d.cleanup(ctx, p, a.KeepReleases, id); err != nil {
		report(Event{Step: StepCleanup, State: StateWarning, Message: err.Error()})
	} else {
		msg := "无需清理"
		if removed > 0 {
			msg = fmt.Sprintf("清理了 %d 个旧版本", removed)
		}
		report(Event{Step: StepCleanup, State: StateDone, Message: msg})
	}
	res.OK = true
	return res, nil
}

// restoreAfterFailedBackup undoes a deploy that failed before the switch.
func (d *Deployer) restoreAfterFailedBackup(ctx context.Context, a app.App, rel string, wasRunning bool) {
	cmd := "rm -rf " + ssh.Quote(rel)
	if wasRunning {
		cmd += "; " + d.systemctl("start", a.Unit())
	}
	_ = d.sh(ctx, cmd)
}

// rollback restores the previous release, and the backup of shared/ taken
// for this deploy, after the new release failed.
func (d *Deployer) rollback(ctx context.Context, a app.App, previous, failedID, backup string, wasRunning bool, res *Result, report func(Event)) {
	p := app.PathsOf(d.M.Env, a.Name)
	report(Event{Step: StepRollback, State: StateRunning})
	if previous == "" {
		_ = d.sh(ctx, d.systemctl("stop", a.Unit()))
		report(Event{Step: StepRollback, State: StateSkipped, Message: "这是第一个版本，没有可回滚的版本；服务已停止，新版本保留以便查看日志"})
		return
	}
	cmd := d.systemctl("stop", a.Unit()) + "; " + switchScript(p, previous)
	if backup != "" {
		cmd += " && " + restoreScript(p, backup)
	}
	cmd += " && rm -rf " + ssh.Quote(p.Releases+"/"+failedID)
	if wasRunning {
		cmd += " && " + d.systemctl("reset-failed", a.Unit()) + " 2>/dev/null; " + d.systemctl("start", a.Unit())
	}
	if err := d.sh(ctx, cmd); err != nil {
		report(Event{Step: StepRollback, State: StateFailed, Message: err.Error()})
		return
	}
	res.RolledBack = true
	msg := "已切回 " + previous
	if backup != "" {
		msg += "，并恢复了 shared/ 备份"
	}
	report(Event{Step: StepRollback, State: StateDone, Message: msg})
	if wasRunning {
		d.checkRestored(ctx, a, report)
	}
}

// checkRestored runs the health check of the release a rollback restored.
func (d *Deployer) checkRestored(ctx context.Context, a app.App, report func(Event)) {
	if err := Check(ctx, d.M, a); err != nil {
		report(Event{Step: StepRollback, State: StateWarning, Message: "旧版本也未通过健康检查：" + err.Error()})
	}
}

// Rollback switches an app to an earlier release, and back again when that
// release fails its health check. shared/ is left as it is.
func (d *Deployer) Rollback(ctx context.Context, name, release string, report func(Event)) (Result, error) {
	unlock, ok := lock(d.ServerID + "/" + name)
	if !ok {
		return Result{}, fmt.Errorf("%s 正在发版或回滚", name)
	}
	defer unlock()
	if report == nil {
		report = func(Event) {}
	}
	info, err := d.M.Get(ctx, name)
	if err != nil {
		return Result{}, err
	}
	a := info.App
	p := app.PathsOf(d.M.Env, name)
	res := Result{Release: release, Previous: info.Current}
	if release == info.Current {
		return res, errors.New("已经是当前版本")
	}
	if strings.ContainsAny(release, "/ ") || release == "" {
		return res, errors.New("版本号无效")
	}
	if err := d.sh(ctx, "test -x "+ssh.Quote(p.Releases+"/"+release+"/"+a.Entry)); err != nil {
		return res, fmt.Errorf("版本 %s 不存在或缺少入口文件 %s", release, a.Entry)
	}
	ctx = context.WithoutCancel(ctx)
	if err := d.M.Apply(ctx, a); err != nil {
		return res, err
	}
	report(Event{Step: StepStop, State: StateRunning})
	if err := d.sh(ctx, d.systemctl("stop", a.Unit())); err != nil {
		report(Event{Step: StepStop, State: StateFailed, Message: err.Error()})
		return res, err
	}
	report(Event{Step: StepStop, State: StateDone})
	report(Event{Step: StepSwitch, State: StateRunning})
	if err := d.sh(ctx, switchScript(p, release)); err != nil {
		report(Event{Step: StepSwitch, State: StateFailed, Message: err.Error()})
		return res, err
	}
	report(Event{Step: StepSwitch, State: StateDone, Message: release})

	var since string
	back := func(step Step, err error) (Result, error) {
		report(Event{Step: step, State: StateFailed, Message: err.Error()})
		res.Error = err.Error()
		res.Logs = d.journalTail(ctx, a.Unit(), since)
		report(Event{Step: StepRollback, State: StateRunning})
		cmd := d.systemctl("stop", a.Unit())
		if info.Current != "" {
			cmd += "; " + switchScript(p, info.Current)
			if info.Status.Running() {
				cmd += " && " + d.systemctl("reset-failed", a.Unit()) + " 2>/dev/null; " + d.systemctl("start", a.Unit())
			}
		}
		if e := d.sh(ctx, cmd); e != nil {
			report(Event{Step: StepRollback, State: StateFailed, Message: e.Error()})
		} else {
			res.RolledBack = true
			report(Event{Step: StepRollback, State: StateDone, Message: "已切回 " + info.Current})
			if info.Current != "" && info.Status.Running() {
				d.checkRestored(ctx, a, report)
			}
		}
		return res, err
	}
	report(Event{Step: StepStart, State: StateRunning})
	if since, err = d.start(ctx, a.Unit()); err != nil {
		return back(StepStart, err)
	}
	report(Event{Step: StepStart, State: StateDone})
	report(Event{Step: StepHealth, State: StateRunning, Message: healthLabel(a)})
	if err := Check(ctx, d.M, a); err != nil {
		return back(StepHealth, err)
	}
	report(Event{Step: StepHealth, State: StateDone, Message: healthLabel(a)})
	a.Current = release
	_ = d.M.WriteJSON(a)
	res.OK = true
	return res, nil
}

func (d *Deployer) newReleaseID(ctx context.Context, p app.Paths) (string, error) {
	base := d.now().Format("20060102-150405")
	for i := 0; i < 100; i++ {
		id := base
		if i > 0 {
			id = fmt.Sprintf("%s-%d", base, i+1)
		}
		res, err := d.M.C.Run(ctx, "test -e "+ssh.Quote(p.Releases+"/"+id), nil)
		if err != nil {
			return "", err
		}
		if res.Status != 0 {
			return id, nil
		}
	}
	return "", errors.New("无法生成版本号")
}

// upload streams the package as a tar.gz into tar on the server.
func (d *Deployer) upload(ctx context.Context, pkg *Package, rel, entry string, progress func(float64)) error {
	pr, pw := io.Pipe()
	var last time.Time
	go func() {
		err := pkg.WriteTarGz(pw, func(done int64) {
			if pkg.Size > 0 && time.Since(last) > 150*time.Millisecond {
				last = time.Now()
				progress(float64(done) / float64(pkg.Size))
			}
		})
		pw.CloseWithError(err)
	}()
	q := ssh.Quote(rel)
	cmd := "mkdir -p " + q + " && tar -xzf - --no-same-owner -C " + q + " && chmod +x " + ssh.Quote(rel+"/"+entry) + " && test -f " + ssh.Quote(rel+"/"+entry)
	res, err := d.M.C.Run(ctx, cmd, pr)
	pr.CloseWithError(errors.New("upload ended"))
	if err != nil {
		return fmt.Errorf("上传失败：%w", err)
	}
	if res.Status != 0 {
		return fmt.Errorf("解压失败：%s", strings.TrimSpace(res.Stderr))
	}
	return nil
}

// linkScript makes each shared path of the release a symlink into
// shared/. A path missing from shared/ is seeded from the release when it
// has one, or created as a directory when its name has no extension.
func linkScript(rel, shared string, paths []string) string {
	var b strings.Builder
	b.WriteString("set -e\ncd " + ssh.Quote(rel) + "\n")
	for _, p := range paths {
		src, dst := ssh.Quote(shared+"/"+p), ssh.Quote(p)
		b.WriteString("if [ ! -e " + src + " ] && [ ! -L " + src + " ]; then\n")
		b.WriteString("  mkdir -p \"$(dirname " + src + ")\"\n")
		b.WriteString("  if [ -e " + dst + " ]; then mv " + dst + " " + src + "\n")
		if !strings.Contains(baseOf(p), ".") {
			b.WriteString("  else mkdir -p " + src + "\n")
		}
		b.WriteString("  fi\nfi\n")
		b.WriteString("rm -rf " + dst + "\nmkdir -p \"$(dirname " + dst + ")\"\nln -s " + src + " " + dst + "\n")
	}
	return b.String()
}

func baseOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func backupScript(p app.Paths, backup string) string {
	return "set -e; mkdir -p " + ssh.Quote(p.Backups) +
		"; tar -czf " + ssh.Quote(backup+".tmp") + " --exclude=./.env -C " + ssh.Quote(p.Shared) + " ." +
		"; mv " + ssh.Quote(backup+".tmp") + " " + ssh.Quote(backup) +
		fmt.Sprintf("; ls -1t %s/*.tar.gz 2>/dev/null | tail -n +%d | xargs -r rm -f", ssh.Quote(p.Backups), keepBackups+1)
}

// restoreScript puts shared/ back as the backup has it. .env is written
// from app.json, so it is neither backed up nor restored.
func restoreScript(p app.Paths, backup string) string {
	s := ssh.Quote(p.Shared)
	return "find " + s + " -mindepth 1 -maxdepth 1 ! -name .env -exec rm -rf {} + && tar -xzf " + ssh.Quote(backup) + " -C " + s
}

// switchScript points current at a release atomically.
func switchScript(p app.Paths, id string) string {
	tmp := ssh.Quote(p.Current + ".berth-tmp")
	return "ln -sfn " + ssh.Quote("releases/"+id) + " " + tmp + " && mv -Tf " + tmp + " " + ssh.Quote(p.Current)
}

// cleanup removes the oldest releases beyond keep, never the current one.
func (d *Deployer) cleanup(ctx context.Context, p app.Paths, keep int, current string) (int, error) {
	rels, _, err := d.M.Releases(ctx, baseOf(p.Dir))
	if err != nil {
		return 0, err
	}
	var rm []string
	kept := 0
	for _, r := range rels { // newest first
		if r.ID == current || kept < keep {
			kept++
			continue
		}
		rm = append(rm, ssh.Quote(p.Releases+"/"+r.ID))
	}
	if len(rm) == 0 {
		return 0, nil
	}
	return len(rm), d.sh(ctx, "rm -rf "+strings.Join(rm, " "))
}

// start starts a unit and returns when it did, as a journalctl --since
// value in the server's clock.
func (d *Deployer) start(ctx context.Context, unit string) (string, error) {
	out, err := d.M.C.Output(ctx, "date +%s; "+d.systemctl("reset-failed", unit)+" 2>/dev/null; "+d.systemctl("start", unit))
	since := ""
	if f := strings.Fields(out); len(f) > 0 {
		since = "@" + f[0]
	}
	return since, err
}

// journalTail returns the last lines of a unit's log, since a time when
// known, so the failure is not buried under the previous release's lines.
func (d *Deployer) journalTail(ctx context.Context, unit, since string) string {
	cmd := d.M.Env.Journalctl(unit) + " -n 60 -o cat"
	if since != "" {
		cmd += " --since " + ssh.Quote(since)
	}
	out, _ := d.M.C.Output(ctx, cmd+" 2>&1")
	return strings.TrimSpace(out)
}

func healthLabel(a app.App) string {
	switch a.Health.Type {
	case app.HealthTCP:
		return fmt.Sprintf("TCP 127.0.0.1:%d", a.Port)
	case app.HealthHTTP:
		return fmt.Sprintf("GET http://127.0.0.1:%d%s", a.Port, a.Health.Path)
	case app.HealthNone:
		return "不检查"
	default:
		return fmt.Sprintf("进程保持运行 %d 秒", a.Health.Timeout)
	}
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
