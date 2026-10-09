package services

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/xusenlin/berth/core/config"
	"github.com/xusenlin/berth/core/ssh"
)

// TestDisconnectStaysOffline checks that a server the user disconnected is
// not dialed again by the page's polling, only by Reconnect.
func TestDisconnectStaysOffline(t *testing.T) {
	store, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := store.SaveServer(config.Server{Host: "127.0.0.1", Port: 1, User: "u", Auth: config.AuthPassword, HostKey: "SHA256:x"})
	if err != nil {
		t.Fatal(err)
	}
	b := NewBackend(store, config.MemorySecrets{})
	var dials atomic.Int32
	b.Pool = ssh.NewPool(func(string) (ssh.Target, error) {
		dials.Add(1)
		return ssh.Target{}, errors.New("no server")
	}, nil)
	s := &Servers{B: b}
	a := &Apps{B: b}
	ctx := context.Background()

	if _, err := s.Overview(ctx, srv.ID); err == nil || dials.Load() != 1 {
		t.Fatalf("first call: %v, %d dials", err, dials.Load())
	}
	if err := s.Disconnect(srv.ID); err != nil {
		t.Fatal(err)
	}
	if !s.List()[0].Offline {
		t.Error("not reported offline")
	}
	for range 3 {
		if _, err := s.Overview(ctx, srv.ID); !errors.Is(err, ErrOffline) {
			t.Errorf("overview while offline: %v", err)
		}
		if _, err := a.List(ctx, srv.ID); !errors.Is(err, ErrOffline) {
			t.Errorf("apps while offline: %v", err)
		}
		if v, err := s.Connect(ctx, srv.ID); err != nil || !v.Offline {
			t.Errorf("connect while offline: %+v %v", v, err)
		}
	}
	if n := dials.Load(); n != 1 {
		t.Fatalf("dialed %d times while offline", n-1)
	}
	if _, err := s.Reconnect(ctx, srv.ID); err == nil || dials.Load() != 2 {
		t.Errorf("reconnect: %v, %d dials", err, dials.Load())
	}
	if s.List()[0].Offline {
		t.Error("still offline after reconnect")
	}
}
