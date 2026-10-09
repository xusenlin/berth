// Package config reads and writes Berth's local configuration: how to reach
// each server. Everything else (apps, releases, settings of apps) lives on
// the servers themselves.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// AuthType is how Berth logs in to a server.
type AuthType string

const (
	AuthPassword AuthType = "password"
	AuthKey      AuthType = "key"
	AuthAgent    AuthType = "agent"
)

// DefaultPortRange is where ports of new apps are picked from.
var DefaultPortRange = [2]int{18000, 18999}

// Server is how to reach one server.
type Server struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	User      string   `json:"user"`
	Auth      AuthType `json:"auth"`
	KeyPath   string   `json:"keyPath"`
	HostKey   string   `json:"hostKey"`
	PortRange [2]int   `json:"portRange"`
}

// Validate checks the fields a connection needs.
func (s *Server) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	s.Host = strings.TrimSpace(s.Host)
	s.User = strings.TrimSpace(s.User)
	s.KeyPath = strings.TrimSpace(s.KeyPath)
	if s.Host == "" {
		return errors.New("主机地址不能为空")
	}
	if s.User == "" {
		return errors.New("用户名不能为空")
	}
	if s.Port == 0 {
		s.Port = 22
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("端口 %d 无效", s.Port)
	}
	switch s.Auth {
	case AuthPassword, AuthAgent:
	case AuthKey:
		if s.KeyPath == "" {
			return errors.New("请选择私钥文件")
		}
	default:
		return fmt.Errorf("未知的认证方式 %q", s.Auth)
	}
	if s.PortRange == [2]int{} {
		s.PortRange = DefaultPortRange
	}
	if s.PortRange[0] < 1 || s.PortRange[1] > 65535 || s.PortRange[0] > s.PortRange[1] {
		return fmt.Errorf("端口区间 %d-%d 无效", s.PortRange[0], s.PortRange[1])
	}
	if s.Name == "" {
		s.Name = s.Host
	}
	return nil
}

// Settings are preferences of the app.
type Settings struct {
	Theme string `json:"theme"` // system, light or dark
}

// Config is the content of config.json.
type Config struct {
	Servers  []Server `json:"servers"`
	Settings Settings `json:"settings"`
}

// DefaultPath returns where config.json lives: the user's configuration
// directory, e.g. ~/Library/Application Support/Berth on macOS.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Berth", "config.json"), nil
}

// Store is config.json, loaded in memory and written back on every change.
type Store struct {
	path string
	mu   sync.Mutex
	cfg  Config
}

// Open loads the configuration at path. A missing file is an empty
// configuration.
func Open(path string) (*Store, error) {
	s := &Store{path: path, cfg: Config{Settings: Settings{Theme: "system"}}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.cfg); err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	if s.cfg.Settings.Theme == "" {
		s.cfg.Settings.Theme = "system"
	}
	return s, nil
}

// Path returns the file of the store.
func (s *Store) Path() string { return s.path }

// Servers returns all servers, in the order they were added.
func (s *Store) Servers() []Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.cfg.Servers)
}

// Server returns the server with id.
func (s *Store) Server(id string) (Server, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(id)
	if i < 0 {
		return Server{}, false
	}
	return s.cfg.Servers[i], true
}

// SaveServer adds a server, giving it an ID when it has none, or replaces
// the one with its ID.
func (s *Store) SaveServer(srv Server) (Server, error) {
	if err := srv.Validate(); err != nil {
		return Server{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if srv.ID == "" {
		srv.ID = newID()
		s.cfg.Servers = append(s.cfg.Servers, srv)
	} else if i := s.index(srv.ID); i >= 0 {
		s.cfg.Servers[i] = srv
	} else {
		s.cfg.Servers = append(s.cfg.Servers, srv)
	}
	return srv, s.write()
}

// DeleteServer removes the server with id.
func (s *Store) DeleteServer(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(id)
	if i < 0 {
		return nil
	}
	s.cfg.Servers = slices.Delete(s.cfg.Servers, i, i+1)
	return s.write()
}

// Settings returns the preferences.
func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Settings
}

// SetSettings replaces the preferences.
func (s *Store) SetSettings(st Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Settings = st
	return s.write()
}

func (s *Store) index(id string) int {
	return slices.IndexFunc(s.cfg.Servers, func(srv Server) bool { return srv.ID == id })
}

// write replaces the file atomically. The caller holds mu.
func (s *Store) write() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	if s.cfg.Servers == nil {
		s.cfg.Servers = []Server{}
	}
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
