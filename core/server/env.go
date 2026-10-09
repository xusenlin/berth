// Package server inspects servers: their environment, the mode apps run in,
// and an overview of their resources.
package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/xusenlin/berth/core/ssh"
)

// AppsDirName is the directory under the login user's home that holds the
// apps.
const AppsDirName = "senlinApps"

// UnitPrefix starts the name of every app's systemd unit.
const UnitPrefix = "senlin-"

// Mode is how apps run on a server, decided by the login user.
type Mode string

const (
	// ModeRoot runs apps as system services.
	ModeRoot Mode = "root"
	// ModeUser runs apps as services of the user's systemd instance, which
	// starts at boot only with linger enabled.
	ModeUser Mode = "user"
)

// Env is what Berth needs to know about a server.
type Env struct {
	User           string `json:"user"`
	UID            int    `json:"uid"`
	Home           string `json:"home"`
	Mode           Mode   `json:"mode"`
	Arch           string `json:"arch"`
	OS             string `json:"os"`
	Systemd        bool   `json:"systemd"`
	SystemdVersion string `json:"systemdVersion"`
	Linger         bool   `json:"linger"`
	// Sudo is "nopasswd", "passwd" (sudo exists but asks for a password) or
	// "" (no sudo).
	Sudo string `json:"sudo"`
	// DiskFree is the free space, in bytes, of the apps directory's
	// filesystem.
	DiskFree int64 `json:"diskFree"`
}

// AppsDir returns the absolute path of the apps directory.
func (e Env) AppsDir() string { return e.Home + "/" + AppsDirName }

// UnitDir returns where unit files of apps go.
func (e Env) UnitDir() string {
	if e.Mode == ModeRoot {
		return "/etc/systemd/system"
	}
	return e.Home + "/.config/systemd/user"
}

// userPrefix makes systemctl --user work from an SSH session that pam_systemd
// did not set up.
func (e Env) userPrefix() string {
	if e.Mode == ModeRoot {
		return ""
	}
	run := fmt.Sprintf("/run/user/%d", e.UID)
	return fmt.Sprintf("XDG_RUNTIME_DIR=%s DBUS_SESSION_BUS_ADDRESS=unix:path=%s/bus ", run, run)
}

// Systemctl returns the systemctl command of the mode, ready for
// arguments.
func (e Env) Systemctl() string {
	if e.Mode == ModeRoot {
		return "systemctl"
	}
	return e.userPrefix() + "systemctl --user"
}

// Journalctl returns the journalctl command for an app's unit.
func (e Env) Journalctl(unit string) string {
	if e.Mode == ModeRoot {
		return "journalctl --no-pager -u " + ssh.Quote(unit)
	}
	return e.userPrefix() + "journalctl --no-pager --user -u " + ssh.Quote(unit)
}

// NeedsLinger reports whether apps would not start at boot.
func (e Env) NeedsLinger() bool { return e.Mode == ModeUser && !e.Linger }

// LingerCommand is what an administrator runs to enable linger.
func (e Env) LingerCommand() string { return "sudo loginctl enable-linger " + e.User }

const detectScript = `u=$(id -un)
echo "user=$u"
echo "uid=$(id -u)"
echo "home=$HOME"
echo "arch=$(uname -m)"
if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
  echo systemd=1
  echo "systemdVersion=$(systemctl --version 2>/dev/null | head -n1)"
fi
if [ -f "/var/lib/systemd/linger/$u" ] || [ "$(loginctl show-user "$u" -p Linger --value 2>/dev/null)" = yes ]; then echo linger=1; fi
if [ -r /etc/os-release ]; then . /etc/os-release; echo "os=$PRETTY_NAME"; fi
mkdir -p "$HOME/` + AppsDirName + `" 2>/dev/null
echo "free=$(df -Pk "$HOME/` + AppsDirName + `" 2>/dev/null | awk 'NR==2{print $4}')"
if [ "$(id -u)" != 0 ]; then
  if sudo -n true >/dev/null 2>&1; then echo sudo=nopasswd; elif command -v sudo >/dev/null 2>&1; then echo sudo=passwd; fi
fi
`

// Detect inspects the server and creates the apps directory.
func Detect(ctx context.Context, c *ssh.Client) (Env, error) {
	out, err := c.Output(ctx, detectScript)
	if err != nil {
		return Env{}, fmt.Errorf("环境检测失败：%w", err)
	}
	env := parseEnv(out)
	if env.Home == "" {
		return env, errors.New("环境检测失败：无法确定用户主目录")
	}
	if env.Mode == ModeUser {
		if _, err := c.Output(ctx, "mkdir -p "+ssh.Quote(env.UnitDir())); err != nil {
			return env, fmt.Errorf("创建 %s：%w", env.UnitDir(), err)
		}
	}
	return env, nil
}

func parseEnv(out string) Env {
	var env Env
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "user":
			env.User = v
		case "uid":
			env.UID, _ = strconv.Atoi(v)
		case "home":
			env.Home = strings.TrimRight(v, "/")
			if env.Home == "" {
				env.Home = "/"
			}
		case "arch":
			env.Arch = v
		case "systemd":
			env.Systemd = true
		case "systemdVersion":
			env.SystemdVersion = v
		case "linger":
			env.Linger = true
		case "os":
			env.OS = strings.Trim(v, `"`)
		case "free":
			kb, _ := strconv.ParseInt(v, 10, 64)
			env.DiskFree = kb * 1024
		case "sudo":
			env.Sudo = v
		}
	}
	if env.UID == 0 {
		env.Mode = ModeRoot
	} else {
		env.Mode = ModeUser
	}
	return env
}

// LingerError is returned when Berth cannot enable linger itself.
type LingerError struct {
	Command string
	Reason  string
}

func (e *LingerError) Error() string {
	return fmt.Sprintf("无法开启 linger（%s）。请让管理员在服务器上执行：%s", e.Reason, e.Command)
}

// EnableLinger enables linger for the login user with sudo, giving sudo
// password when it asks for one.
func EnableLinger(ctx context.Context, c *ssh.Client, env Env, password string) error {
	if env.Mode == ModeRoot || env.Linger {
		return nil
	}
	cmd := "loginctl enable-linger " + ssh.Quote(env.User)
	res, err := c.Run(ctx, "sudo -n "+cmd, nil)
	if err != nil {
		return err
	}
	if res.Status == 0 {
		return nil
	}
	if password == "" {
		return &LingerError{Command: env.LingerCommand(), Reason: "sudo 需要密码，或该用户没有 sudo 权限"}
	}
	res, err = c.Run(ctx, "sudo -S -p '' "+cmd, strings.NewReader(password+"\n"))
	if err != nil {
		return err
	}
	if res.Status != 0 {
		reason := strings.TrimSpace(res.Stderr)
		if reason == "" {
			reason = "sudo 执行失败"
		}
		return &LingerError{Command: env.LingerCommand(), Reason: reason}
	}
	return nil
}
