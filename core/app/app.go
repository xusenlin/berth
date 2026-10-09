// Package app manages apps on a server: their app.json, systemd units,
// ports and states.
package app

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/xusenlin/berth/core/server"
)

// Restart is the systemd Restart= policy.
type Restart string

const (
	RestartOnFailure Restart = "on-failure"
	RestartAlways    Restart = "always"
	RestartNo        Restart = "no"
)

// HealthType is how a release is judged healthy after it starts.
type HealthType string

const (
	HealthProcess HealthType = "process"
	HealthTCP     HealthType = "tcp"
	HealthHTTP    HealthType = "http"
	HealthNone    HealthType = "none"
)

// Health is the health check of an app.
type Health struct {
	Type HealthType `json:"type"`
	// Path is the HTTP path of HealthHTTP.
	Path string `json:"path,omitempty"`
	// Timeout is how long, in seconds, the check waits.
	Timeout int `json:"timeout,omitempty"`
}

// App is the content of an app's app.json.
type App struct {
	Name         string            `json:"name"`
	Entry        string            `json:"entry"`
	Args         string            `json:"args"`
	Port         int               `json:"port,omitempty"`
	Env          map[string]string `json:"env"`
	Shared       []string          `json:"shared"`
	Autostart    bool              `json:"autostart"`
	Restart      Restart           `json:"restart"`
	Health       Health            `json:"health"`
	Backup       bool              `json:"backup"`
	KeepReleases int               `json:"keepReleases"`
	Current      string            `json:"current,omitempty"`
	Preset       string            `json:"preset,omitempty"`
}

var (
	nameRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Normalize fills in defaults and checks the fields.
func (a *App) Normalize() error {
	a.Name = strings.TrimSpace(a.Name)
	a.Entry = strings.TrimSpace(a.Entry)
	a.Args = strings.TrimSpace(a.Args)
	if !nameRE.MatchString(a.Name) {
		return errors.New("应用名只能包含小写字母、数字、- 和 _，并以字母或数字开头，最长 40 个字符")
	}
	if a.Entry == "" {
		return errors.New("入口文件不能为空")
	}
	if err := checkRelPath(a.Entry); err != nil {
		return fmt.Errorf("入口文件：%w", err)
	}
	if strings.ContainsAny(a.Args, "\n\r") {
		return errors.New("启动参数不能换行")
	}
	if a.Port < 0 || a.Port > 65535 {
		return fmt.Errorf("端口 %d 无效", a.Port)
	}
	if a.Env == nil {
		a.Env = map[string]string{}
	}
	for k, v := range a.Env {
		if !envKeyRE.MatchString(k) {
			return fmt.Errorf("环境变量名 %q 无效", k)
		}
		if strings.ContainsAny(v, "\n\r") {
			return fmt.Errorf("环境变量 %s 的值不能换行", k)
		}
	}
	shared := []string{}
	for _, p := range a.Shared {
		p = strings.Trim(strings.TrimSpace(p), "/")
		if p == "" {
			continue
		}
		if err := checkRelPath(p); err != nil {
			return fmt.Errorf("持久路径 %q：%w", p, err)
		}
		if p == a.Entry {
			return fmt.Errorf("持久路径不能是入口文件 %q", p)
		}
		if !slices.Contains(shared, p) {
			shared = append(shared, p)
		}
	}
	a.Shared = shared
	switch a.Restart {
	case "":
		a.Restart = RestartOnFailure
	case RestartOnFailure, RestartAlways, RestartNo:
	default:
		return fmt.Errorf("未知的重启策略 %q", a.Restart)
	}
	switch a.Health.Type {
	case "":
		a.Health.Type = HealthProcess
	case HealthProcess, HealthNone:
	case HealthTCP:
		if a.Port == 0 {
			return errors.New("TCP 健康检查需要端口")
		}
	case HealthHTTP:
		if a.Port == 0 {
			return errors.New("HTTP 健康检查需要端口")
		}
		if !strings.HasPrefix(a.Health.Path, "/") {
			a.Health.Path = "/" + a.Health.Path
		}
	default:
		return fmt.Errorf("未知的健康检查类型 %q", a.Health.Type)
	}
	if a.Health.Timeout <= 0 {
		a.Health.Timeout = 10
	}
	a.Health.Timeout = min(a.Health.Timeout, 300)
	if a.KeepReleases <= 0 {
		a.KeepReleases = 5
	}
	return nil
}

// checkRelPath accepts a clean relative path inside a directory.
func checkRelPath(p string) error {
	if strings.HasPrefix(p, "/") {
		return errors.New("必须是相对路径")
	}
	if path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return errors.New("路径无效")
	}
	if strings.ContainsAny(p, "\n\r\x00'\"\\$`") {
		return errors.New("包含不允许的字符")
	}
	return nil
}

// Clone returns a deep copy.
func (a App) Clone() App {
	a.Env = maps.Clone(a.Env)
	a.Shared = slices.Clone(a.Shared)
	return a
}

// Unit returns the name of the app's systemd unit.
func (a App) Unit() string { return UnitName(a.Name) }

// UnitName returns the unit of the app named name.
func UnitName(name string) string { return server.UnitPrefix + name + ".service" }

// Paths are the locations of an app on its server.
type Paths struct {
	Dir      string
	Releases string
	Current  string
	Shared   string
	Backups  string
	AppJSON  string
	EnvFile  string
}

// PathsOf returns the locations of the app named name.
func PathsOf(env server.Env, name string) Paths {
	dir := env.AppsDir() + "/" + name
	return Paths{
		Dir:      dir,
		Releases: dir + "/releases",
		Current:  dir + "/current",
		Shared:   dir + "/shared",
		Backups:  dir + "/backups",
		AppJSON:  dir + "/app.json",
		EnvFile:  dir + "/shared/.env",
	}
}

// Preset fills in the fields of a kind of program.
type Preset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	App         App    `json:"app"`
}

// Presets are the starting points of new apps.
func Presets() []Preset {
	return []Preset{
		{
			ID:          "generic",
			Name:        "通用",
			Description: "任意 Linux 可执行文件",
			App: App{
				Args:         "",
				Env:          map[string]string{},
				Shared:       []string{},
				Autostart:    true,
				Restart:      RestartOnFailure,
				Health:       Health{Type: HealthProcess, Timeout: 5},
				Backup:       false,
				KeepReleases: 5,
				Preset:       "generic",
			},
		},
		{
			ID:          "pocketbase",
			Name:        "PocketBase",
			Description: "数据保存在 shared/pb_data，发版前自动备份",
			App: App{
				Entry: "pocketbase",
				Args:  "serve --http=0.0.0.0:{{port}} --dir={{shared}}/pb_data",
				Env:   map[string]string{},
				// pb_migrations, pb_hooks and pb_public ship with each release.
				Shared:       []string{"pb_data"},
				Autostart:    true,
				Restart:      RestartOnFailure,
				Health:       Health{Type: HealthHTTP, Path: "/api/health", Timeout: 15},
				Backup:       true,
				KeepReleases: 5,
				Preset:       "pocketbase",
			},
		},
	}
}
