// Package config holds the app's settings and the Lana home they point at.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the settings file, in the app's data directory.
const FileName = "settings.json"

// DefaultTextSize is the size of the terminals' text, in DIPs.
const DefaultTextSize float32 = 13

// DefaultPanelWidth is the width of the left panel (Spaces and Agents), in
// DIPs (spec 58, panel).
const DefaultPanelWidth float32 = 264

// Settings are the user's choices, kept in settings.json in the app's data
// directory.
type Settings struct {
	// LanaHome is the Lana home the app talks to, empty for the default.
	LanaHome string `json:"lanaHome,omitempty"`
	// Appearance is "light" or "dark", empty to follow the system.
	Appearance string `json:"appearance,omitempty"`
	// TextSize is the size of the terminals' text, in DIPs.
	TextSize float32 `json:"textSize,omitempty"`
	// PanelWidth is the width of the left panel, in DIPs (spec 58, panel).
	PanelWidth float32 `json:"panelWidth,omitempty"`
	// PanelHidden hides the left panel (spec 58, panel).
	PanelHidden bool `json:"panelHidden,omitempty"`
	// Notifications shows a desktop notification for a session that needs
	// the user; off by default, because Lana.app already does.
	Notifications bool `json:"notifications,omitempty"`
}

// Defaults are the settings of a fresh install.
func Defaults() Settings {
	return Settings{TextSize: DefaultTextSize, PanelWidth: DefaultPanelWidth}
}

// Load reads settings.json from dir. A file that is missing or invalid
// leaves the defaults, and so does a text size or panel width that was
// never set.
func Load(dir string) Settings {
	s := Defaults()
	if b, err := os.ReadFile(path(dir)); err == nil {
		var read Settings
		if err := json.Unmarshal(b, &read); err == nil {
			s = read
		}
	}
	if s.TextSize <= 0 {
		s.TextSize = DefaultTextSize
	}
	if s.PanelWidth <= 0 {
		s.PanelWidth = DefaultPanelWidth
	}
	return s
}

// Save writes the settings to settings.json in dir: a temporary file
// beside it, renamed over it, so that an interrupted write leaves the old
// file whole.
func Save(dir string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // nothing to remove once the rename has run
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path(dir))
}

// ResolveLanaHome returns the Lana home the app uses: the LANA_HOME
// environment variable, then the setting, then ~/.lana-dev in a
// development build and ~/.lana otherwise. A leading "~/" in either is the
// user's home directory.
func ResolveLanaHome(env func(string) string, s Settings, devBuild bool, userHome string) string {
	p := env("LANA_HOME")
	if p == "" {
		p = s.LanaHome
	}
	if p != "" {
		return expandHome(p, userHome)
	}
	if devBuild {
		return filepath.Join(userHome, ".lana-dev")
	}
	return filepath.Join(userHome, ".lana")
}

// expandHome makes a leading "~" the user's home directory.
func expandHome(p, userHome string) string {
	if p == "~" {
		return userHome
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(userHome, p[2:])
	}
	return p
}

// path is the settings file in dir.
func path(dir string) string { return filepath.Join(dir, FileName) }
