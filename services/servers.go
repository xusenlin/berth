package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"

	"github.com/xusenlin/berth/core/app"
	"github.com/xusenlin/berth/core/config"
	"github.com/xusenlin/berth/core/server"
	"github.com/xusenlin/berth/core/ssh"
)

// Servers manages the servers Berth knows.
type Servers struct{ B *Backend }

// ServerInput is a server being added or edited.
type ServerInput struct {
	Server config.Server `json:"server"`
	// Secret is the password, or the private key's passphrase. Empty when
	// editing keeps the stored one.
	Secret string `json:"secret"`
}

// ServerView is a server with its connection state.
type ServerView struct {
	Server    config.Server `json:"server"`
	Status    ssh.Status    `json:"status"`
	Env       *server.Env   `json:"env"`
	HasSecret bool          `json:"hasSecret"`
}

// TestResult is what a test connection found.
type TestResult struct {
	Fingerprint string     `json:"fingerprint"`
	KeyType     string     `json:"keyType"`
	Env         server.Env `json:"env"`
	// Changed is set when the server's host key differs from the recorded
	// one.
	Changed bool `json:"changed"`
}

func (s *Servers) view(srv config.Server) ServerView {
	v := ServerView{Server: srv, Status: s.B.Pool.Status(srv.ID)}
	if env, ok := s.B.cachedEnv(srv.ID); ok {
		v.Env = &env
	}
	secret, _ := s.B.Secrets.Get(srv.ID)
	v.HasSecret = secret != ""
	return v
}

// List returns all servers.
func (s *Servers) List() []ServerView {
	servers := s.B.Store.Servers()
	views := make([]ServerView, len(servers))
	for i, srv := range servers {
		views[i] = s.view(srv)
	}
	return views
}

func (s *Servers) secretFor(in ServerInput) (string, error) {
	if in.Secret != "" || in.Server.ID == "" {
		return in.Secret, nil
	}
	return s.B.Secrets.Get(in.Server.ID)
}

// Test connects with the given settings, without saving them, and returns
// the server's host key and environment. The recorded host key is not
// enforced, so a changed key is reported rather than refused.
func (s *Servers) Test(ctx context.Context, in ServerInput) (TestResult, error) {
	if err := in.Server.Validate(); err != nil {
		return TestResult{}, err
	}
	secret, err := s.secretFor(in)
	if err != nil {
		return TestResult{}, err
	}
	t := targetOf(in.Server, secret)
	t.HostKey = ""
	c, err := ssh.Dial(ctx, t)
	if err != nil {
		return TestResult{}, err
	}
	defer c.Close()
	env, err := server.Detect(ctx, c)
	if err != nil {
		return TestResult{}, err
	}
	return TestResult{
		Fingerprint: c.Fingerprint(),
		KeyType:     c.KeyType(),
		Env:         env,
		Changed:     in.Server.HostKey != "" && in.Server.HostKey != c.Fingerprint(),
	}, nil
}

// Save adds or updates a server. Its HostKey must be the fingerprint the
// user confirmed after Test.
func (s *Servers) Save(in ServerInput) (ServerView, error) {
	if in.Server.HostKey == "" {
		return ServerView{}, errors.New("请先测试连接并确认主机密钥")
	}
	if in.Server.Auth == config.AuthAgent {
		in.Secret = ""
	}
	isNew := in.Server.ID == ""
	srv, err := s.B.Store.SaveServer(in.Server)
	if err != nil {
		return ServerView{}, err
	}
	if in.Secret != "" || in.Server.Auth == config.AuthAgent || isNew {
		if err := s.B.Secrets.Set(srv.ID, in.Secret); err != nil {
			return ServerView{}, err
		}
	}
	s.B.Pool.Close(srv.ID)
	s.B.forgetEnv(srv.ID)
	return s.view(srv), nil
}

// Delete forgets a server. Its apps keep running.
func (s *Servers) Delete(id string) error {
	s.B.Pool.Close(id)
	s.B.forgetEnv(id)
	if err := s.B.Store.DeleteServer(id); err != nil {
		return err
	}
	return s.B.Secrets.Delete(id)
}

// Connect connects to a server, if it is not already, and returns it with
// its environment.
func (s *Servers) Connect(ctx context.Context, id string) (ServerView, error) {
	srv, ok := s.B.Store.Server(id)
	if !ok {
		return ServerView{}, errors.New("服务器不存在")
	}
	if _, _, err := s.B.client(ctx, id); err != nil {
		return s.view(srv), err
	}
	return s.view(srv), nil
}

// Disconnect closes the connection to a server.
func (s *Servers) Disconnect(id string) { s.B.Pool.Close(id) }

// EnableLinger enables linger for the login user, so user services start
// at boot.
func (s *Servers) EnableLinger(ctx context.Context, id string) (server.Env, error) {
	c, env, err := s.B.client(ctx, id)
	if err != nil {
		return env, err
	}
	srv, _ := s.B.Store.Server(id)
	pw := ""
	if srv.Auth == config.AuthPassword {
		pw, _ = s.B.Secrets.Get(id)
	}
	if err := server.EnableLinger(ctx, c, env, pw); err != nil {
		return env, err
	}
	s.B.forgetEnv(id)
	_, env, err = s.B.client(ctx, id)
	return env, err
}

// Overview returns a snapshot of a server's resources.
func (s *Servers) Overview(ctx context.Context, id string) (server.Overview, error) {
	c, _, err := s.B.client(ctx, id)
	if err != nil {
		return server.Overview{}, err
	}
	return server.Collect(ctx, c)
}

// AppsDiskUsage returns the bytes the apps directory takes.
func (s *Servers) AppsDiskUsage(ctx context.Context, id string) (int64, error) {
	c, _, err := s.B.client(ctx, id)
	if err != nil {
		return 0, err
	}
	return server.AppsDiskUsage(ctx, c)
}

// PickKeyFile asks for a private key file, starting in ~/.ssh. It returns
// "" when canceled.
func (s *Servers) PickKeyFile(ctx context.Context) (string, error) {
	dir := ""
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".ssh")
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:          mygo.CallerWindow(ctx),
		Title:           "选择私钥文件",
		DefaultPath:     dir,
		ShowHiddenFiles: true,
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// Ports is what listens on a server, and which ports its apps reserve.
type Ports struct {
	Listeners []server.Listener `json:"listeners"`
	// Reserved are ports of apps that nothing listens on, e.g. stopped
	// apps.
	Reserved []ReservedPort `json:"reserved"`
	// PortRange is where ports of new apps are picked from.
	PortRange [2]int `json:"portRange"`
	// AllProcesses is set when the login user can see every process
	// (root); otherwise other users' processes have no name.
	AllProcesses bool `json:"allProcesses"`
}

// ReservedPort is the port of an app.
type ReservedPort struct {
	Port    int    `json:"port"`
	App     string `json:"app"`
	Running bool   `json:"running"`
}

// Ports lists the listening sockets of a server, with the Berth app each
// belongs to.
func (s *Servers) Ports(ctx context.Context, id string) (Ports, error) {
	c, env, err := s.B.client(ctx, id)
	if err != nil {
		return Ports{}, err
	}
	srv, _ := s.B.Store.Server(id)
	ls, err := server.Listeners(ctx, c)
	if err != nil {
		return Ports{}, err
	}
	p := Ports{Listeners: ls, Reserved: []ReservedPort{}, PortRange: srv.PortRange, AllProcesses: env.Mode == server.ModeRoot}
	if !env.Systemd {
		return p, nil
	}
	apps, err := (&app.Manager{C: c, Env: env}).List(ctx)
	if err != nil {
		return p, nil // the listeners alone are still useful
	}
	listening := map[int]bool{}
	for i := range p.Listeners {
		l := &p.Listeners[i]
		listening[l.Port] = true
		if l.App != "" || l.PID != 0 {
			continue
		}
		// The process is hidden from this user: attribute the socket to
		// the running app registered on its port.
		for _, a := range apps {
			if a.App.Port == l.Port && a.Status.Running() {
				l.App = a.App.Name
			}
		}
	}
	for _, a := range apps {
		if a.App.Port > 0 && !listening[a.App.Port] {
			p.Reserved = append(p.Reserved, ReservedPort{Port: a.App.Port, App: a.App.Name, Running: a.Status.Running()})
		}
	}
	return p, nil
}
