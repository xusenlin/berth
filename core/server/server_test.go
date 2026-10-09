package server

import (
	"math"
	"testing"
)

func TestParseEnv(t *testing.T) {
	env := parseEnv(`user=deploy
uid=1000
home=/home/deploy
arch=x86_64
systemd=1
systemdVersion=systemd 255 (255.4-1ubuntu8)
os=Ubuntu 24.04.1 LTS
free=1048576
sudo=passwd
`)
	if env.Mode != ModeUser || env.User != "deploy" || env.UID != 1000 || !env.Systemd || env.Linger {
		t.Fatalf("env = %+v", env)
	}
	if env.DiskFree != 1<<30 || env.OS != "Ubuntu 24.04.1 LTS" || env.Sudo != "passwd" {
		t.Fatalf("env = %+v", env)
	}
	if !env.NeedsLinger() {
		t.Error("user mode without linger needs linger")
	}
	if got := env.UnitDir(); got != "/home/deploy/.config/systemd/user" {
		t.Errorf("UnitDir = %q", got)
	}
	if got := env.Systemctl(); got != "XDG_RUNTIME_DIR=/run/user/1000 DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus systemctl --user" {
		t.Errorf("Systemctl = %q", got)
	}

	root := parseEnv("user=root\nuid=0\nhome=/root\nsystemd=1\n")
	if root.Mode != ModeRoot || root.NeedsLinger() || root.UnitDir() != "/etc/systemd/system" || root.Systemctl() != "systemctl" {
		t.Fatalf("root env = %+v", root)
	}
}

func TestParseOverview(t *testing.T) {
	out := `@@os
NAME="Ubuntu"
PRETTY_NAME="Ubuntu 24.04 LTS"
@@uname
Linux 6.8.0-45-generic x86_64
@@uptime
2764800.12 5000000.00
@@loadavg
0.10 0.20 0.30 1/200 12345
@@nproc
2
@@stat1
cpu  100 0 100 800 0 0 0 0 0 0
@@stat2
cpu  130 0 120 850 0 0 0 0 0 0
@@meminfo
MemTotal:        2048000 kB
MemFree:          100000 kB
MemAvailable:     900000 kB
SwapTotal:       1024000 kB
SwapFree:        1000000 kB
@@df
Filesystem     1024-blocks     Used Available Capacity Mounted on
tmpfs               200000     1000    199000       1% /run
/dev/vda1         40000000 18000000  20000000      48% /
/dev/vda15          100000    10000     90000      10% /boot/efi
@@appsdf
/dev/vda1         40000000 18000000  20000000      48% /
`
	o := parseOverview(out)
	if o.OS != "Ubuntu 24.04 LTS" || o.Kernel != "Linux 6.8.0-45-generic" || o.Arch != "x86_64" {
		t.Fatalf("system: %+v", o)
	}
	if o.Uptime != 2764800 || o.CPUs != 2 || o.Load != [3]float64{0.1, 0.2, 0.3} {
		t.Fatalf("load: %+v", o)
	}
	// busy 50 of 100 ticks
	if math.Abs(o.CPUPercent-50) > 0.01 {
		t.Errorf("cpu = %v", o.CPUPercent)
	}
	if o.MemTotal != 2048000*1024 || o.MemUsed != (2048000-900000)*1024 || o.SwapUsed != 24000*1024 {
		t.Errorf("mem: %+v", o)
	}
	if len(o.Disks) != 1 || o.Disks[0].Mount != "/" || o.AppsDisk == nil || o.AppsDisk.Avail != 20000000*1024 {
		t.Errorf("disks: %+v %+v", o.Disks, o.AppsDisk)
	}
}

func TestParseListenersProc(t *testing.T) {
	out := `@@proc-tcp
  sl  local_address rem_address   st
   0: 0100007F:18EB 00000000:0000 0A 00000000:00000000 00:00000000 00000000   999        0 3001 1
   1: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3002 1
   2: 0200000A:0016 0900000A:1388 01 00000000:00000000 00:00000000 00000000     0        0 3003 1
   3: 00000000:4651 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3005 1
@@proc-tcp6
   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3004 1
   1: 00000000000000000000000001000000:1F90 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3006 1
@@proc-udp
   0: 3500007F:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000   101        0 3007 2
@@proc-udp6
@@owners
70	3002		sshd
70	3004		sshd
4242	3005	senlin-pb.service	pocketbase
4243	3005	senlin-pb.service	pocketbase
`
	ls := parseListeners(out)
	if len(ls) != 5 {
		t.Fatalf("got %+v", ls)
	}
	if l := ls[0]; l.Port != 22 || l.Local || l.Address != "::" || l.Process != "sshd" || l.PID != 70 {
		t.Errorf("ssh = %+v", l)
	}
	if l := ls[1]; l.Port != 53 || l.Proto != "udp" || !l.Local || l.Address != "127.0.0.53" || l.Process != "" {
		t.Errorf("resolver = %+v", l)
	}
	if l := ls[2]; l.Port != 6379 || !l.Local || l.Address != "127.0.0.1" || l.PID != 0 {
		t.Errorf("hidden process = %+v", l)
	}
	if l := ls[3]; l.Port != 8080 || !l.Local || l.Address != "::1" {
		t.Errorf("v6 local = %+v", l)
	}
	if l := ls[4]; l.Port != 18001 || l.App != "pb" || l.PID != 4242 || l.Process != "pocketbase" {
		t.Errorf("app = %+v", l)
	}
}
