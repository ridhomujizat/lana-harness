package selfupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Outcomes of an update (API-UPD-002 last_result.outcome).
const (
	OutcomeUpdated    = "updated"
	OutcomeRolledBack = "rolled_back"
	OutcomeFailed     = "failed"
)

// Paths names the files under $LANA_HOME/updates (API-UPD-011).
type Paths struct {
	Home string
}

// Dir is $LANA_HOME/updates.
func (p Paths) Dir() string { return filepath.Join(p.Home, "updates") }

// State is updates/state.json.
func (p Paths) State() string { return filepath.Join(p.Dir(), "state.json") }

// Pending is updates/pending.json, present only during a trial boot.
func (p Paths) Pending() string { return filepath.Join(p.Dir(), "pending.json") }

// Backups is updates/backups.
func (p Paths) Backups() string { return filepath.Join(p.Dir(), "backups") }

// Download is updates/lana-<version>.download.
func (p Paths) Download(version string) string {
	return filepath.Join(p.Dir(), "lana-"+safeName(version)+".download")
}

// ProbeHome is updates/probe-<version>, the temp home of the probe run.
func (p Paths) ProbeHome(version string) string {
	return filepath.Join(p.Dir(), "probe-"+safeName(version))
}

// Result is the outcome of the last update across restarts.
type Result struct {
	Outcome string `json:"outcome"`
	From    string `json:"from"`
	To      string `json:"to"`
	Reason  string `json:"reason"`
	// At is unix milliseconds.
	At int64 `json:"at"`
}

// State is the content of state.json.
type State struct {
	Skipped    string  `json:"skipped"`
	LastResult *Result `json:"last_result"`
}

// Pending is the content of pending.json.
type Pending struct {
	From      string `json:"from"`
	To        string `json:"to"`
	ExePath   string `json:"exe_path"`
	BackupDir string `json:"backup_dir"`
	Boots     int    `json:"boots"`
	// CreatedAt is unix milliseconds.
	CreatedAt int64 `json:"created_at"`
}

// ReadState reads state.json; a missing file is an empty state.
func ReadState(p Paths) (State, error) {
	var state State
	err := readJSON(p.State(), &state)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	return state, err
}

// WriteState replaces state.json atomically.
func WriteState(p Paths, state State) error {
	return writeJSON(p.State(), state)
}

// UpdateState reads, changes, and writes state.json. A corrupt file is
// replaced by the changed empty state.
func UpdateState(p Paths, change func(*State)) (State, error) {
	state, err := ReadState(p)
	if err != nil {
		state = State{}
	}
	change(&state)
	return state, WriteState(p, state)
}

// ReadPending reads pending.json. It returns nil and no error when the file
// does not exist.
func ReadPending(p Paths) (*Pending, error) {
	var pending Pending
	err := readJSON(p.Pending(), &pending)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pending, nil
}

// WritePending replaces pending.json atomically.
func WritePending(p Paths, pending Pending) error {
	return writeJSON(p.Pending(), pending)
}

// RemovePending deletes pending.json; a missing file is not an error.
func RemovePending(p Paths) error {
	err := os.Remove(p.Pending())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func readJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), 0o600)
}

// atomicWrite writes data through a temp file in the same folder and renames
// it over path.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// safeName keeps a version usable as a file name part.
func safeName(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}
