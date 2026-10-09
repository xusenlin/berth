package server

import (
	"bufio"
	"context"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/xusenlin/berth/core/ssh"
)

// Listener is a socket listening on a server.
type Listener struct {
	Proto   string `json:"proto"`   // tcp or udp
	Address string `json:"address"` // e.g. 0.0.0.0, 127.0.0.1, ::
	Port    int    `json:"port"`
	// Process and PID are known when the login user may see the process:
	// every process as root, the user's own otherwise.
	Process string `json:"process,omitempty"`
	PID     int    `json:"pid,omitempty"`
	// App is the Berth app whose unit runs the process.
	App string `json:"app,omitempty"`
	// Local is set when only the server itself can connect.
	Local bool `json:"local"`
}

// listenersScript prints the sockets of /proc/net, then the processes
// holding the listening ones: pid, inode, unit and command, tab separated.
const listenersScript = `for f in tcp tcp6 udp udp6; do echo "@@proc-$f"; cat /proc/net/$f 2>/dev/null; done
echo @@owners
inodes=$(awk 'FNR>1 && ($4=="0A" || $4=="07") {print $10}' /proc/net/tcp /proc/net/tcp6 /proc/net/udp /proc/net/udp6 2>/dev/null | sort -u | tr '\n' ' ')
[ -n "$inodes" ] && find /proc/[0-9]*/fd -lname 'socket:*' -printf '%h %l\n' 2>/dev/null |
  awk -v list="$inodes" 'BEGIN { n = split(list, a, " "); for (i = 1; i <= n; i++) want["socket:[" a[i] "]"] = 1 }
    ($2 in want) { split($1, p, "/"); print p[3], substr($2, 9, length($2) - 9) }' |
  sort -u | while read pid inode; do
    printf '%s\t%s\t%s\t%s\n' "$pid" "$inode" "$(grep -o '` + UnitPrefix + `[^/]*\.service' /proc/$pid/cgroup 2>/dev/null | head -n1)" "$(cat /proc/$pid/comm 2>/dev/null)"
  done
true
`

// Listeners returns the TCP and UDP sockets listening on the server,
// sorted by port.
func Listeners(ctx context.Context, c *ssh.Client) ([]Listener, error) {
	out, err := c.Output(ctx, listenersScript)
	if err != nil && out == "" {
		return nil, err
	}
	return parseListeners(out), nil
}

type owner struct {
	pid  int
	app  string
	comm string
}

func parseListeners(out string) []Listener {
	s := sections(out)
	owners := map[string]owner{}
	for line := range strings.Lines(s["owners"]) {
		f := strings.Split(strings.TrimRight(line, "\n"), "\t")
		if len(f) != 4 {
			continue
		}
		if _, seen := owners[f[1]]; seen {
			continue // a socket shared by forked processes: keep the parent
		}
		pid, _ := strconv.Atoi(f[0])
		owners[f[1]] = owner{pid: pid, app: strings.TrimSuffix(strings.TrimPrefix(f[2], UnitPrefix), ".service"), comm: f[3]}
	}
	var ls []Listener
	for _, f := range []string{"tcp", "tcp6", "udp", "udp6"} {
		for _, sock := range parseProcNet(s["proc-"+f], strings.TrimSuffix(f, "6")) {
			l := sock.Listener
			if o, ok := owners[sock.inode]; ok {
				l.PID, l.App, l.Process = o.pid, o.app, o.comm
			}
			ls = append(ls, l)
		}
	}
	return dedupe(ls)
}

type socket struct {
	Listener
	inode string
}

// parseProcNet reads /proc/net/{tcp,udp}{,6}: sockets in LISTEN (TCP) or
// unconnected (UDP) state.
func parseProcNet(content, proto string) []socket {
	want := "0A"
	if proto == "udp" {
		want = "07"
	}
	var ls []socket
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[3] != want {
			continue
		}
		hexAddr, hexPort, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(hexPort, 16, 16)
		if err != nil {
			continue
		}
		addr := procAddr(hexAddr)
		sk := socket{Listener: Listener{Proto: proto, Address: addr, Port: int(port), Local: isLoopback(addr)}}
		if len(f) > 9 {
			sk.inode = f[9]
		}
		ls = append(ls, sk)
	}
	return ls
}

// procAddr decodes an address of /proc/net/tcp: 4 or 16 bytes in hex, each
// 32-bit word in host (little endian) order.
func procAddr(h string) string {
	if len(h) != 8 && len(h) != 32 {
		return h
	}
	b := make(net.IP, len(h)/2)
	for w := 0; w < len(h)/8; w++ {
		for i := range 4 {
			v, _ := strconv.ParseUint(h[w*8+i*2:w*8+i*2+2], 16, 8)
			b[w*4+3-i] = byte(v)
		}
	}
	if len(b) == 16 {
		if v4 := b.To4(); v4 != nil && !b.Equal(net.IPv6zero) {
			return v4.String()
		}
	}
	return b.String()
}

func isLoopback(addr string) bool {
	if addr == "localhost" {
		return true
	}
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
}

// dedupe merges the IPv4 and IPv6 sockets of one process on one port, and
// sorts by port.
func dedupe(ls []Listener) []Listener {
	slices.SortStableFunc(ls, func(a, b Listener) int {
		if a.Port != b.Port {
			return a.Port - b.Port
		}
		return strings.Compare(a.Proto, b.Proto)
	})
	var out []Listener
	for _, l := range ls {
		if n := len(out); n > 0 {
			p := &out[n-1]
			if p.Port == l.Port && p.Proto == l.Proto && p.PID == l.PID && p.Local == l.Local {
				if !l.Local && (l.Address == "::" || l.Address == "*") {
					p.Address = l.Address // shown as "all addresses" either way
				}
				continue
			}
		}
		out = append(out, l)
	}
	return out
}
