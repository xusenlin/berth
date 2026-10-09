package services

import "github.com/xusenlin/berth/core/config"

// Settings are the preferences of Berth.
type Settings struct{ B *Backend }

// Get returns the preferences.
func (s *Settings) Get() config.Settings { return s.B.Store.Settings() }

// Set replaces the preferences.
func (s *Settings) Set(st config.Settings) error { return s.B.Store.SetSettings(st) }

// ConfigPath returns where config.json is.
func (s *Settings) ConfigPath() string { return s.B.Store.Path() }
