package selfupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// Claim is updates/pending.claim-<pid>.json: pending.json taken by the
// process pid while it records the outcome of the update (design.md, Trial
// boot and rollback).
func (p Paths) Claim(pid int) string {
	return filepath.Join(p.Dir(), claimPrefix+strconv.Itoa(pid)+claimSuffix)
}

const (
	claimPrefix = "pending.claim-"
	claimSuffix = ".json"
)

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

// Claim is pending.json taken by one process. Only the process whose rename
// of pending.json succeeded holds it, so a trial boot, its rollback, and the
// update watchdog never record an outcome twice.
type Claim struct {
	Path    string
	PID     int
	Pending Pending
}

// Release deletes the claim file once the outcome is recorded.
func (c Claim) Release() error {
	err := os.Remove(c.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ClaimPending renames pending.json to pending.claim-<pid>.json. ok is false
// when pending.json does not exist: another process claimed it or the update
// already ended. A claim whose content is unreadable is deleted and returned
// as an error.
func ClaimPending(p Paths, pid int) (Claim, bool, error) {
	return claimFrom(p, p.Pending(), pid)
}

// TakeOverClaim renames the claim of fromPID to a claim of pid, for a claim
// left behind by a process that died before it released it. ok is false when
// that claim does not exist.
func TakeOverClaim(p Paths, fromPID, pid int) (Claim, bool, error) {
	if fromPID == pid {
		return Claim{}, false, nil
	}
	return claimFrom(p, p.Claim(fromPID), pid)
}

func claimFrom(p Paths, src string, pid int) (Claim, bool, error) {
	dst := p.Claim(pid)
	if err := os.Rename(src, dst); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Claim{}, false, nil
		}
		return Claim{}, false, err
	}
	claim := Claim{Path: dst, PID: pid}
	if err := readJSON(dst, &claim.Pending); err != nil {
		_ = os.Remove(dst)
		return Claim{}, false, err
	}
	return claim, true, nil
}

// ClaimPIDs returns the pids of the claim files in updates/, sorted.
func ClaimPIDs(p Paths) ([]int, error) {
	entries, err := os.ReadDir(p.Dir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, claimPrefix) || !strings.HasSuffix(name, claimSuffix) {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, claimPrefix), claimSuffix))
		if err != nil || pid <= 0 {
			continue
		}
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids, nil
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

// ReadClaim reads the pending update inside a claim file.
func ReadClaim(path string) (Pending, error) {
	var pending Pending
	err := readJSON(path, &pending)
	return pending, err
}
