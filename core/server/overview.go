package server

import (
	"bufio"
	"context"
	"strconv"
	"strings"

	"github.com/xusenlin/berth/core/ssh"
)

// Overview is a snapshot of a server's resources.
type Overview struct {
	OS         string     `json:"os"`
	Kernel     string     `json:"kernel"`
	Arch       string     `json:"arch"`
	Uptime     int64      `json:"uptime"` // seconds
	Load       [3]float64 `json:"load"`
	CPUs       int        `json:"cpus"`
	CPUPercent float64    `json:"cpuPercent"`
	MemTotal   int64      `json:"memTotal"` // bytes
	MemUsed    int64      `json:"memUsed"`
	SwapTotal  int64      `json:"swapTotal"`
	SwapUsed   int64      `json:"swapUsed"`
	Disks      []Disk     `json:"disks"`
	// AppsDisk is the filesystem of the apps directory.
	AppsDisk *Disk `json:"appsDisk"`
}

// Disk is a mounted filesystem.
type Disk struct {
	Filesystem string `json:"filesystem"`
	Mount      string `json:"mount"`
	Total      int64  `json:"total"` // bytes
	Used       int64  `json:"used"`
	Avail      int64  `json:"avail"`
}

const overviewScript = `echo @@os; cat /etc/os-release 2>/dev/null
echo @@uname; uname -srm
echo @@uptime; cat /proc/uptime
echo @@loadavg; cat /proc/loadavg
echo @@nproc; nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo
echo @@stat1; head -n1 /proc/stat
sleep 0.5
echo @@stat2; head -n1 /proc/stat
echo @@meminfo; cat /proc/meminfo
echo @@df; df -Pk 2>/dev/null
echo @@appsdf; df -Pk "$HOME/` + AppsDirName + `" 2>/dev/null | tail -n1
`

// Collect takes a snapshot with one command.
func Collect(ctx context.Context, c *ssh.Client) (Overview, error) {
	out, err := c.Output(ctx, overviewScript)
	if err != nil && out == "" {
		return Overview{}, err
	}
	return parseOverview(out), nil
}

// sections splits the output of a script into the parts after each
// "@@name" line.
func sections(out string) map[string]string {
	m := map[string]string{}
	var name string
	var b strings.Builder
	flush := func() {
		if name != "" {
			m[name] = b.String()
		}
		b.Reset()
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "@@") {
			flush()
			name = strings.TrimPrefix(line, "@@")
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	return m
}

func parseOverview(out string) Overview {
	s := sections(out)
	var o Overview
	for line := range strings.Lines(s["os"]) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "PRETTY_NAME="); ok {
			o.OS = strings.Trim(v, `"`)
		}
	}
	if f := strings.Fields(s["uname"]); len(f) >= 3 {
		o.Kernel = f[0] + " " + f[1]
		o.Arch = f[2]
	}
	if f := strings.Fields(s["uptime"]); len(f) > 0 {
		v, _ := strconv.ParseFloat(f[0], 64)
		o.Uptime = int64(v)
	}
	if f := strings.Fields(s["loadavg"]); len(f) >= 3 {
		for i := range 3 {
			o.Load[i], _ = strconv.ParseFloat(f[i], 64)
		}
	}
	o.CPUs, _ = strconv.Atoi(strings.TrimSpace(s["nproc"]))
	o.CPUPercent = cpuPercent(s["stat1"], s["stat2"])

	mem := map[string]int64{}
	for line := range strings.Lines(s["meminfo"]) {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseInt(f[0], 10, 64)
		mem[k] = n * 1024
	}
	o.MemTotal = mem["MemTotal"]
	avail, ok := mem["MemAvailable"]
	if !ok {
		avail = mem["MemFree"] + mem["Buffers"] + mem["Cached"]
	}
	o.MemUsed = max(o.MemTotal-avail, 0)
	o.SwapTotal = mem["SwapTotal"]
	o.SwapUsed = max(o.SwapTotal-mem["SwapFree"], 0)

	for line := range strings.Lines(s["df"]) {
		if d, ok := parseDF(line); ok && interestingDisk(d) {
			o.Disks = append(o.Disks, d)
		}
	}
	if d, ok := parseDF(s["appsdf"]); ok {
		o.AppsDisk = &d
	}
	return o
}

// cpuPercent is the busy share of CPU time between two "cpu" lines of
// /proc/stat.
func cpuPercent(a, b string) float64 {
	parse := func(line string) (total, idle uint64, ok bool) {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			return 0, 0, false
		}
		for i, v := range f[1:] {
			if i >= 8 { // guest time is already counted in user time
				break
			}
			n, _ := strconv.ParseUint(v, 10, 64)
			total += n
			if i == 3 || i == 4 { // idle, iowait
				idle += n
			}
		}
		return total, idle, true
	}
	t1, i1, ok1 := parse(a)
	t2, i2, ok2 := parse(b)
	if !ok1 || !ok2 || t2 <= t1 {
		return 0
	}
	dt, di := float64(t2-t1), float64(i2-i1)
	return max(0, min(100, (dt-di)/dt*100))
}

func parseDF(line string) (Disk, bool) {
	f := strings.Fields(line)
	if len(f) < 6 || f[0] == "Filesystem" {
		return Disk{}, false
	}
	total, err1 := strconv.ParseInt(f[1], 10, 64)
	used, err2 := strconv.ParseInt(f[2], 10, 64)
	avail, err3 := strconv.ParseInt(f[3], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return Disk{}, false
	}
	return Disk{
		Filesystem: f[0],
		Mount:      strings.Join(f[5:], " "),
		Total:      total * 1024,
		Used:       used * 1024,
		Avail:      avail * 1024,
	}, true
}

// interestingDisk leaves out pseudo and tiny filesystems.
func interestingDisk(d Disk) bool {
	switch d.Filesystem {
	case "tmpfs", "devtmpfs", "udev", "none", "shm", "efivarfs", "cgroup", "cgroup2":
		return false
	}
	for _, p := range []string{"/dev", "/run", "/sys", "/proc", "/boot", "/snap", "/var/lib/docker"} {
		if d.Mount == p || strings.HasPrefix(d.Mount, p+"/") {
			return false
		}
	}
	return d.Total > 0
}

// AppsDiskUsage returns the bytes the apps directory takes. It can be slow
// for large directories.
func AppsDiskUsage(ctx context.Context, c *ssh.Client) (int64, error) {
	out, err := c.Output(ctx, `du -sk "$HOME/`+AppsDirName+`" 2>/dev/null | cut -f1`)
	if err != nil && strings.TrimSpace(out) == "" {
		return 0, err
	}
	kb, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	return kb * 1024, nil
}
