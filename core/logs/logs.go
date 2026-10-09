// Package logs reads apps' logs from the systemd journal.
package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xusenlin/berth/core/app"
	"github.com/xusenlin/berth/core/server"
	"github.com/xusenlin/berth/core/ssh"
)

// Entry is one line of an app's log.
type Entry struct {
	// Time is in milliseconds since the Unix epoch.
	Time     int64  `json:"time"`
	Message  string `json:"message"`
	Priority int    `json:"priority"`
	PID      int    `json:"pid,omitempty"`
	// Stream is "stderr" or "stdout" when known; systemd's own lines have
	// none.
	Source string `json:"source,omitempty"`
}

// Parse decodes a line of journalctl -o json.
func Parse(line string) (Entry, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return Entry{}, false
	}
	str := func(k string) string {
		var s string
		if v, ok := raw[k]; ok {
			_ = json.Unmarshal(v, &s)
		}
		return s
	}
	e := Entry{Priority: 6}
	if us, err := strconv.ParseInt(str("__REALTIME_TIMESTAMP"), 10, 64); err == nil {
		e.Time = us / 1000
	}
	if p, err := strconv.Atoi(str("PRIORITY")); err == nil {
		e.Priority = p
	}
	e.PID, _ = strconv.Atoi(str("_PID"))
	// MESSAGE is a string, or an array of bytes when it is not valid UTF-8.
	if v, ok := raw["MESSAGE"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil {
			e.Message = s
		} else {
			var b []byte
			var ints []int
			if json.Unmarshal(v, &ints) == nil {
				b = make([]byte, len(ints))
				for i, n := range ints {
					b[i] = byte(n)
				}
			}
			e.Message = strings.ToValidUTF8(string(b), string(utf8.RuneError))
		}
	}
	if str("_TRANSPORT") == "stdout" {
		e.Source = "stdout"
	} else if str("_COMM") == "systemd" || str("UNIT") != "" || str("USER_UNIT") != "" {
		e.Source = "systemd"
	}
	return e, true
}

func command(env server.Env, name string, extra ...string) string {
	return env.Journalctl(app.UnitName(name)) + " -o json " + strings.Join(extra, " ")
}

// Follow sends the last lines of an app's log, then new lines as they
// come, until ctx ends or send fails.
func Follow(ctx context.Context, c *ssh.Client, env server.Env, name string, lines int, send func(Entry) error) error {
	if lines <= 0 {
		lines = 200
	}
	cmd := command(env, name, "-f", "-n", strconv.Itoa(lines), "2>&1")
	return c.Stream(ctx, cmd, func(line string) error {
		e, ok := Parse(line)
		if !ok {
			// journalctl's own messages, e.g. about permissions.
			if strings.TrimSpace(line) == "" || line == "-- No entries --" {
				return nil
			}
			e = Entry{Time: time.Now().UnixMilli(), Message: line, Priority: 4, Source: "journalctl"}
		}
		return send(e)
	})
}

// Query selects lines of history.
type Query struct {
	// Since and Until are in milliseconds since the epoch; 0 is unbounded.
	Since int64 `json:"since"`
	Until int64 `json:"until"`
	// Keyword keeps lines containing it, ignoring case.
	Keyword string `json:"keyword"`
	// Limit is the most lines returned, the newest; 0 means 2000.
	Limit int `json:"limit"`
}

// History returns past lines of an app's log, oldest first.
func History(ctx context.Context, c *ssh.Client, env server.Env, name string, q Query) ([]Entry, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 2000
	}
	limit = min(limit, 20000)
	var extra []string
	if q.Since > 0 {
		extra = append(extra, "--since", ssh.Quote("@"+strconv.FormatInt(q.Since/1000, 10)))
	}
	if q.Until > 0 {
		extra = append(extra, "--until", ssh.Quote("@"+strconv.FormatInt((q.Until+999)/1000, 10)))
	}
	// The keyword is matched here, not with journalctl --grep, which needs
	// a journalctl built with PCRE: read more lines than asked for.
	n := limit
	if q.Keyword != "" {
		n = 50000
	}
	extra = append(extra, "-n", strconv.Itoa(n))
	out, err := c.Output(ctx, command(env, name, extra...))
	if err != nil {
		return nil, fmt.Errorf("读取日志：%w", err)
	}
	kw := strings.ToLower(q.Keyword)
	var entries []Entry
	for line := range strings.Lines(out) {
		e, ok := Parse(line)
		if !ok {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(e.Message), kw) {
			continue
		}
		entries = append(entries, e)
	}
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}
