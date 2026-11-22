package figcraft

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// maxLivePorts is how many live_ports a Settings may hold (REQ-FV-050).
const maxLivePorts = 16

// ErrInvalidCLIPath means a Settings cli_path is neither empty nor an absolute
// path to an existing executable regular file (REQ-FV-050).
var ErrInvalidCLIPath = errors.New("figcraft: cli_path must be an absolute path to an executable file")

// ErrInvalidPorts means a Settings live_ports entry is outside 1..65535 or the
// list holds more than 16 entries (REQ-FV-050).
var ErrInvalidPorts = errors.New("figcraft: live_ports must be between 1 and 65535, at most 16 entries")

// SettingsStore keeps the FigCraft viewer settings of REQ-FV-050 in one JSON
// file: {"cli_path": "", "live": true, "live_ports": []}. It caches the value
// after the first read and is safe for concurrent use; Put writes atomically,
// so a reader never sees a half-written file.
type SettingsStore struct {
	path string

	mu     sync.Mutex
	loaded bool
	cache  Settings
}

// NewSettingsStore opens the settings file. A missing or corrupt file is not an
// error: Get answers DefaultSettings until a Put writes one (REQ-FV-050).
func NewSettingsStore(path string) *SettingsStore {
	return &SettingsStore{path: path}
}

// Get returns the stored settings, reading the file on first use (REQ-FV-050).
// A missing or corrupt file answers DefaultSettings, and a file without a
// "live" key means the default true.
func (s *SettingsStore) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	return s.cache
}

// Put validates settings, writes them atomically with mode 0o600 and returns
// the stored value (REQ-FV-050). A bad cli_path is ErrInvalidCLIPath and a bad
// live_ports list is ErrInvalidPorts.
func (s *SettingsStore) Put(settings Settings) (Settings, error) {
	if err := validateSettings(settings); err != nil {
		return Settings{}, err
	}
	settings = normalizeSettings(settings)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.save(settings); err != nil {
		return Settings{}, err
	}
	s.cache = settings
	s.loaded = true
	return settings, nil
}

// load reads the settings file into the cache once (REQ-FV-050). A missing or
// corrupt file leaves the defaults, and a missing "live" key keeps the default
// true, so the raw keys are decoded one by one.
func (s *SettingsStore) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.cache = DefaultSettings()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	settings := DefaultSettings()
	if v, ok := raw["cli_path"]; ok {
		_ = json.Unmarshal(v, &settings.CLIPath)
	}
	if v, ok := raw["live"]; ok {
		_ = json.Unmarshal(v, &settings.Live)
	}
	if v, ok := raw["live_ports"]; ok {
		var ports []int
		if err := json.Unmarshal(v, &ports); err == nil {
			settings.LivePorts = ports
		}
	}
	s.cache = normalizeSettings(settings)
}

// save writes settings to the file atomically (REQ-FV-050): a temp file in the
// same folder, mode 0o600, renamed into place, so the file is either the old or
// the new contents.
func (s *SettingsStore) save(settings Settings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".workbench-figcraft-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// normalizeSettings replaces a nil live_ports with an empty list, so the file
// always carries the key as [] (REQ-FV-050).
func normalizeSettings(settings Settings) Settings {
	if settings.LivePorts == nil {
		settings.LivePorts = []int{}
	}
	return settings
}

// validateSettings enforces the REQ-FV-050 rules: cli_path is empty or an
// absolute path to an executable regular file, and live_ports holds 1..65535
// with at most 16 entries.
func validateSettings(settings Settings) error {
	if settings.CLIPath != "" {
		if !filepath.IsAbs(settings.CLIPath) {
			return ErrInvalidCLIPath
		}
		info, err := os.Stat(settings.CLIPath)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return ErrInvalidCLIPath
		}
	}
	if len(settings.LivePorts) > maxLivePorts {
		return ErrInvalidPorts
	}
	for _, port := range settings.LivePorts {
		if port < 1 || port > 65535 {
			return ErrInvalidPorts
		}
	}
	return nil
}
