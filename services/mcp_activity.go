package services

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AgentActivity is sent when an agent starts and ends a tool call, so the
// window shows what the agent is doing.
var AgentActivity = mygo.NewEvent[Activity]("agentActivity")

// ActivityState is where a tool call is.
type ActivityState string

const (
	ActivityRunning ActivityState = "running"
	ActivityDone    ActivityState = "done"
	ActivityFailed  ActivityState = "failed"
)

// Activity is a tool call of an agent. The start and the end of a call
// share its ID.
type Activity struct {
	ID   int64  `json:"id"`
	Tool string `json:"tool"`
	// Client is the agent's name, such as claude-code.
	Client string `json:"client,omitempty"`
	// Server is the id of the server the call is about.
	Server string `json:"server,omitempty"`
	App    string `json:"app,omitempty"`
	// Detail is what the call works on: a command, a file, an action…
	Detail string        `json:"detail,omitempty"`
	State  ActivityState `json:"state"`
	Error  string        `json:"error,omitempty"`
	// Millis is how long the call took, once ended.
	Millis int64 `json:"millis,omitempty"`
}

var activityID atomic.Int64

// activityArgs holds the arguments of every tool that tell what a call is
// about.
type activityArgs struct {
	Server  string `json:"server"`
	App     string `json:"app"`
	Name    string `json:"name"` // create_app
	Command string `json:"command"`
	Path    string `json:"path"`
	Action  string `json:"action"`
	File    string `json:"file"`
	Release string `json:"release"`
	Keyword string `json:"keyword"`
}

func (a activityArgs) detail() string {
	switch {
	case a.Command != "":
		return a.Command
	case a.Path != "":
		return a.Path
	case a.Action != "":
		return a.Action
	case a.File != "":
		return filepath.Base(a.File)
	case a.Release != "":
		return a.Release
	}
	return a.Keyword
}

// activity reports tool calls to the window.
func (s *MCPServer) activity(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		call, ok := req.(*mcp.CallToolRequest)
		if method != "tools/call" || !ok || call.Params == nil {
			return next(ctx, method, req)
		}
		var args activityArgs
		_ = json.Unmarshal(call.Params.Arguments, &args)
		a := Activity{
			ID:     activityID.Add(1),
			Tool:   call.Params.Name,
			Client: clientName(call.Session),
			App:    args.App,
			Detail: clip(args.detail(), 160),
			State:  ActivityRunning,
		}
		if a.App == "" {
			a.App = args.Name
		}
		if srv, err := s.lookup(args.Server); err == nil {
			a.Server = srv.ID
		}
		_ = AgentActivity.Broadcast(a)

		start := time.Now()
		res, err := next(ctx, method, req)
		a.Millis = time.Since(start).Milliseconds()
		a.State = ActivityDone
		if err != nil {
			a.State, a.Error = ActivityFailed, err.Error()
		} else if r, ok := res.(*mcp.CallToolResult); ok && r.IsError {
			a.State = ActivityFailed
			for _, c := range r.Content {
				if t, ok := c.(*mcp.TextContent); ok {
					a.Error = t.Text
					break
				}
			}
		}
		a.Error = clip(a.Error, 300)
		_ = AgentActivity.Broadcast(a)
		return res, err
	}
}

func clientName(ss *mcp.ServerSession) string {
	if ss == nil {
		return ""
	}
	if p := ss.InitializeParams(); p != nil && p.ClientInfo != nil {
		if p.ClientInfo.Title != "" {
			return p.ClientInfo.Title
		}
		return p.ClientInfo.Name
	}
	return ""
}

// clip shortens s to its first line and at most n runes.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n]) + "…"
	}
	return s
}
