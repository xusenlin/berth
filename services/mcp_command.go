package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/xusenlin/berth/core/readonly"
	"github.com/xusenlin/berth/core/ssh"
)

const (
	commandTool = "run_command"
	// commandOutput is the most output returned, enough to read a page of
	// logs without flooding the agent's context.
	commandOutput  = 64 << 10
	commandTimeout = 30
	commandMaxTime = 120
)

// syncCommands offers run_command when the user allowed it.
func (s *MCPServer) syncCommands() {
	if !s.B.Store.MCP().Commands {
		s.tool.RemoveTools(commandTool)
		return
	}
	no := false
	mcp.AddTool(s.tool, &mcp.Tool{Name: commandTool,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &no},
		Description: "Run a read-only shell command on a server to investigate: read logs and files anywhere, list directories, " +
			"look at processes, ports, disks and systemd units. Pipes, &&, ;, if, for and $(…) work, but only programs that " +
			"cannot change the server: cat, grep, tail, head, sed (no -i, w or e), find (no -delete or -exec), " +
			"sort, uniq, wc, jq, sqlite3 (opened -readonly -safe, e.g. on an app's shared/data.db), journalctl, " +
			"systemctl status/show/cat/list-* (add --user on servers whose apps are user services), crontab -l, ss, ps, du, df, " +
			"ip … show, curl for GET requests to the server itself (http://127.0.0.1:PORT/…, e.g. a health check), xargs with one " +
			"of these, and the like. Redirections may only read files or go to /dev/null or 2>&1. Variables may be set on their " +
			"own with lowercase names (p=$(pgrep -f web); cat /proc/$p/status), not before a command. Files holding passwords are " +
			"refused: /proc/*/environ, an app's shared/.env and app.json (get_app shows the settings). The command runs with sh as the login " +
			"user, under nice and a timeout; stderr is merged into the output, which is cut at 64 KiB. A refused command says why.",
	}, s.runCommand)
}

type commandArg struct {
	Server  string `json:"server" jsonschema:"server name or id"`
	Command string `json:"command" jsonschema:"POSIX sh command, e.g. tail -n 200 ~/senlinApps/web/shared/app.log | grep -i error"`
	Dir     string `json:"dir,omitempty" jsonschema:"working directory, absolute or relative to the apps directory ~/senlinApps (the default), where each app has a directory"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"seconds before the command is stopped; default 30, at most 120"`
}

var errOutputFull = errors.New("output full")

func (s *MCPServer) runCommand(ctx context.Context, _ *mcp.CallToolRequest, in commandArg) (*mcp.CallToolResult, any, error) {
	// The user may have turned it off since the client listed the tools.
	if !s.B.Store.MCP().Commands {
		return nil, nil, errors.New("the user turned off commands in Berth's MCP settings")
	}
	srv, err := s.server(in.Server)
	if err != nil {
		return nil, nil, err
	}
	script, err := readonly.Check(in.Command)
	if err != nil {
		return nil, nil, fmt.Errorf("refused: %w", err)
	}
	secs := in.Timeout
	if secs <= 0 {
		secs = commandTimeout
	}
	secs = min(secs, commandMaxTime)

	c, env, err := s.B.client(ctx, srv.ID)
	if err != nil {
		return nil, nil, err
	}
	dir := strings.TrimSpace(in.Dir)
	switch {
	case dir == "":
		dir = env.AppsDir()
	case dir == "~" || strings.HasPrefix(dir, "~/"):
		dir = env.Home + dir[1:]
	case !path.IsAbs(dir):
		dir = path.Join(env.AppsDir(), dir)
	}

	// timeout kills the command's process group on the server, which
	// closing the session would not; the local deadline is a backstop.
	run := "nice -n 10 sh -c " + ssh.Quote(script)
	wrapper := "cd " + ssh.Quote(dir) + " || exit 125\nexec 2>&1 </dev/null\n" +
		"if command -v timeout >/dev/null 2>&1; then exec timeout -s KILL " + strconv.Itoa(secs) + " " + run + "; fi\n" +
		"exec " + run
	ctx, cancel := context.WithTimeout(ctx, time.Duration(secs+10)*time.Second)
	defer cancel()

	var out []byte
	start := time.Now()
	res, err := c.Pipe(ctx, wrapper, nil, func(r io.Reader) error {
		var rerr error
		out, rerr = io.ReadAll(io.LimitReader(r, commandOutput+1))
		if rerr != nil {
			return rerr
		}
		if len(out) > commandOutput {
			out = out[:commandOutput]
			return errOutputFull
		}
		return nil
	})
	elapsed := time.Since(start)

	var b strings.Builder
	b.Write(out)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		b.WriteByte('\n')
	}
	note := func(format string, a ...any) { fmt.Fprintf(&b, "["+format+"]\n", a...) }
	switch {
	case errors.Is(err, errOutputFull):
		note("output cut at %d KiB: narrow it with grep, head, tail or sed -n", commandOutput>>10)
	case errors.Is(err, context.DeadlineExceeded) || elapsed >= time.Duration(secs)*time.Second && res.Status != 0:
		note("stopped after %ds; raise timeout (at most %d) or narrow the command", secs, commandMaxTime)
	case err != nil:
		return nil, nil, err
	case res.Status == 125 && len(out) == 0:
		return nil, nil, fmt.Errorf("cannot enter %s: %s", dir, strings.TrimSpace(res.Stderr))
	case res.Status != 0:
		note("exit status %d", res.Status)
	}
	if b.Len() == 0 {
		b.WriteString("(no output)")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}, nil, nil
}
