// Package services binds Berth's core to the frontend through MyGo. It only
// forwards calls and streams; the logic lives in core.
package services

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/egoist/mygo"

	"github.com/xusenlin/berth/core/app"
	"github.com/xusenlin/berth/core/config"
	"github.com/xusenlin/berth/core/server"
	"github.com/xusenlin/berth/core/ssh"
)

// ServerStatus is sent when a server's connection changes state.
var ServerStatus = mygo.NewEvent[StatusChange]("serverStatus")

// StatusChange is a server's new connection state.
type StatusChange struct {
	ID     string     `json:"id"`
	Status ssh.Status `json:"status"`
	Error  string     `json:"error,omitempty"`
}

// Backend is the state shared by the services.
type Backend struct {
	Store   *config.Store
	Secrets config.Secrets
	Pool    *ssh.Pool

	mu   sync.Mutex
	envs map[string]server.Env
}

// ErrOffline is returned for servers the user disconnected.
var ErrOffline = errors.New("已断开连接，点击“连接”后再操作")

// NewBackend opens the configuration and the connection pool.
func NewBackend(store *config.Store, secrets config.Secrets) *Backend {
	b := &Backend{Store: store, Secrets: secrets, envs: map[string]server.Env{}}
	b.Pool = ssh.NewPool(b.target, func(id string, st ssh.Status, err error) {
		ch := StatusChange{ID: id, Status: st}
		if err != nil {
			ch.Error = err.Error()
		}
		if st == ssh.StatusDisconnected {
			b.forgetEnv(id)
		}
		_ = ServerStatus.Broadcast(ch)
	})
	return b
}

func (b *Backend) target(id string) (ssh.Target, error) {
	srv, ok := b.Store.Server(id)
	if !ok {
		return ssh.Target{}, fmt.Errorf("服务器 %s 不存在", id)
	}
	secret, err := b.Secrets.Get(id)
	if err != nil {
		return ssh.Target{}, fmt.Errorf("读取钥匙串：%w", err)
	}
	return targetOf(srv, secret), nil
}

func targetOf(srv config.Server, secret string) ssh.Target {
	return ssh.Target{
		Host:    srv.Host,
		Port:    srv.Port,
		User:    srv.User,
		Auth:    ssh.Auth(srv.Auth),
		KeyPath: srv.KeyPath,
		Secret:  secret,
		HostKey: srv.HostKey,
	}
}

// client returns the connection to a server and its environment, detecting
// it once per connection.
func (b *Backend) client(ctx context.Context, id string) (*ssh.Client, server.Env, error) {
	if b.isOffline(id) {
		return nil, server.Env{}, ErrOffline
	}
	c, err := b.Pool.Get(ctx, id)
	if err != nil {
		return nil, server.Env{}, err
	}
	if b.isOffline(id) {
		// Disconnected while this call dialed: do not leave it connected.
		b.Pool.Close(id)
		return nil, server.Env{}, ErrOffline
	}
	b.mu.Lock()
	env, ok := b.envs[id]
	b.mu.Unlock()
	if ok {
		return c, env, nil
	}
	env, err = server.Detect(ctx, c)
	if err != nil {
		return nil, env, err
	}
	b.mu.Lock()
	b.envs[id] = env
	b.mu.Unlock()
	return c, env, nil
}

func (b *Backend) manager(ctx context.Context, id string) (*app.Manager, error) {
	c, env, err := b.client(ctx, id)
	if err != nil {
		return nil, err
	}
	if !env.Systemd {
		return nil, fmt.Errorf("服务器没有运行 systemd，无法管理应用")
	}
	return &app.Manager{C: c, Env: env}, nil
}

func (b *Backend) cachedEnv(id string) (server.Env, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	env, ok := b.envs[id]
	return env, ok
}

// isOffline tells whether the user disconnected a server: nothing dials it
// until the user connects again, also after a restart.
func (b *Backend) isOffline(id string) bool { return b.Store.Offline(id) }

func (b *Backend) setOffline(id string, off bool) error { return b.Store.SetOffline(id, off) }

func (b *Backend) forgetEnv(id string) {
	b.mu.Lock()
	delete(b.envs, id)
	b.mu.Unlock()
}
