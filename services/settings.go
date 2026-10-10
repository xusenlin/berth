package services

import (
	"github.com/egoist/mygo"

	"github.com/xusenlin/berth/core/config"
)

// Settings are the preferences of Berth.
type Settings struct{ B *Backend }

// Get returns the preferences.
func (s *Settings) Get() config.Settings { return s.B.Store.Settings() }

// Set replaces the preferences.
func (s *Settings) Set(st config.Settings) error {
	if err := s.B.Store.SetSettings(st); err != nil {
		return err
	}
	ApplyTheme(st.Theme)
	return nil
}

// ApplyTheme gives the native parts of the windows the page's theme: the
// window controls over the page on Windows and Linux draw in the app's
// appearance, which would otherwise follow the system and vanish against a
// page of the other one.
func ApplyTheme(theme string) { mygo.Theme.SetSource(mygo.ThemeSource(theme)) }

// ConfigPath returns where config.json is.
func (s *Settings) ConfigPath() string { return s.B.Store.Path() }
