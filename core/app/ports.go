package app

import (
	"bufio"
	"strconv"
	"strings"
)

// PickPort returns the first port of [lo, hi] in neither used nor
// listening, or 0 when all are taken.
func PickPort(lo, hi int, used map[int]bool) int {
	for p := lo; p <= hi; p++ {
		if !used[p] {
			return p
		}
	}
	return 0
}

// ParseListening returns the TCP ports in the LISTEN state of
// /proc/net/tcp and /proc/net/tcp6 content.
func ParseListening(procNetTCP string) map[int]bool {
	ports := map[int]bool{}
	sc := bufio.NewScanner(strings.NewReader(procNetTCP))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// sl local_address rem_address st ...
		if len(f) < 4 || f[3] != "0A" {
			continue
		}
		_, port, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		if n, err := strconv.ParseUint(port, 16, 16); err == nil {
			ports[int(n)] = true
		}
	}
	return ports
}
