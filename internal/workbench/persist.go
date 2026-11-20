package workbench

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// sessionsVersion is the shape of sessions.json; a file of another version is
// treated as garbage (spec 60 REQ-WR-001, REQ-WR-020).
const sessionsVersion = 1

// sessionsName is the file the core keeps its session records in.
const sessionsName = "sessions.json"

// persistInterval is how often the persister writes sessions.json. It is also
// the bound on how long a change waits to be persisted (spec 60 REQ-WR-002).
const persistInterval = time.Second

// sessionRecord is one session as the core persists it (spec 60 REQ-WR-001).
// The fields and their JSON names are exactly the spec's, except
// agent_config_dir, added for claude-swap profiles (spec 60 addendum); an old
// file without it loads with "".
type sessionRecord struct {
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	Command        []string          `json:"command"`
	Dir            string            `json:"dir"`
	Env            []string          `json:"env,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Created        time.Time         `json:"created"`
	Cols           int               `json:"cols"`
	Rows           int               `json:"rows"`
	LastDir        string            `json:"last_dir,omitempty"`
	Exited         bool              `json:"exited"`
	ExitCode       int               `json:"exit_code"`
	Agent          string            `json:"agent,omitempty"`
	AgentSession   string            `json:"agent_session,omitempty"`
	AgentCwd       string            `json:"agent_cwd,omitempty"`
	AgentConfigDir string            `json:"agent_config_dir,omitempty"`
	AgentSeen      time.Time         `json:"agent_seen"`
}

// sessionsFile is the shape of sessions.json: a version and its records.
type sessionsFile struct {
	Version  int             `json:"version"`
	Sessions []sessionRecord `json:"sessions"`
}

// sessionsPath returns the path of the core's sessions file.
func sessionsPath(home string) string { return filepath.Join(Dir(home), sessionsName) }

// logf writes one core log line to stderr, which the core's launcher appends to
// core.log (spec 60 REQ-WR-024).
func logf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "workbench: "+format+"\n", args...)
}

// readRecords reads the persisted sessions. A missing file is no session and no
// error. A file that cannot be parsed, or that carries another version, is
// renamed to sessions.json.bad-<unix>, and the error is returned so the caller
// can log it (spec 60 REQ-WR-020).
func readRecords(home string) ([]sessionRecord, error) {
	path := sessionsPath(home)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var f sessionsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, quarantine(path, err)
	}
	if f.Version != sessionsVersion {
		return nil, quarantine(path, fmt.Errorf("unsupported version %d", f.Version))
	}
	return f.Sessions, nil
}

// quarantine renames a bad sessions file aside so the core starts clean, and
// reports the cause (spec 60 REQ-WR-020).
func quarantine(path string, cause error) error {
	bad := fmt.Sprintf("%s.bad-%d", path, time.Now().Unix())
	if err := os.Rename(path, bad); err != nil {
		return fmt.Errorf("%s: %w", sessionsName, cause)
	}
	return fmt.Errorf("%s: %w (renamed to %s)", sessionsName, cause, filepath.Base(bad))
}

// writeRecords replaces sessions.json atomically: a temp file in the same
// directory, mode 0600, fsynced, then renamed over the file (spec 60
// REQ-WR-001).
func writeRecords(home string, recs []sessionRecord) error {
	data, err := marshalRecords(recs)
	if err != nil {
		return err
	}
	return writeRecordsData(home, data)
}

// marshalRecords encodes records as the file's bytes.
func marshalRecords(recs []sessionRecord) ([]byte, error) {
	if recs == nil {
		recs = []sessionRecord{}
	}
	return json.Marshal(sessionsFile{Version: sessionsVersion, Sessions: recs})
}

// writeRecordsData writes the encoded records to sessions.json atomically.
func writeRecordsData(home string, data []byte) error {
	dir := Dir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sessions-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, sessionsPath(home))
}

// PendingRestore reports whether a previous core left a session that has to be
// started again (spec 60 REQ-WR-030).
func PendingRestore(home string) bool {
	recs, err := readRecords(home)
	if err != nil {
		return false
	}
	for _, r := range recs {
		if !r.Exited {
			return true
		}
	}
	return false
}

// isDir reports whether path names an existing directory.
func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// record returns the session's persisted form (spec 60 REQ-WR-001). last_dir is
// the session's working directory while it names an existing directory, else
// the directory it was created in (REQ-WR-003).
func (s *session) record() sessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	last := s.cwd
	if !isDir(last) {
		last = s.dir
	}
	return sessionRecord{
		ID:             s.id,
		Title:          s.title,
		Command:        s.command,
		Dir:            s.dir,
		Env:            s.extraEnv,
		Labels:         s.labels,
		Created:        s.created,
		Cols:           s.cols,
		Rows:           s.rows,
		LastDir:        last,
		Exited:         s.exited,
		ExitCode:       s.exitCode,
		Agent:          s.rAgent,
		AgentSession:   s.agentSession,
		AgentCwd:       s.agentCwd,
		AgentConfigDir: s.agentConfigDir,
		AgentSeen:      s.agentSeen,
	}
}

// snapshotRecords builds the persisted form of every session, in order.
func (s *server) snapshotRecords() []sessionRecord {
	s.mu.Lock()
	all := make([]*session, 0, len(s.order))
	for _, id := range s.order {
		if ss := s.sessions[id]; ss != nil {
			all = append(all, ss)
		}
	}
	s.mu.Unlock()
	recs := make([]sessionRecord, 0, len(all))
	for _, ss := range all {
		recs = append(recs, ss.record())
	}
	return recs
}

// runPersister writes sessions.json every second until quit closes (spec 60
// REQ-WR-002). closeAll freezes it, so a shutdown never marks a running session
// as exited (REQ-WR-004).
func (s *server) runPersister(quit <-chan struct{}) {
	t := time.NewTicker(persistInterval)
	defer t.Stop()
	for {
		select {
		case <-quit:
			return
		case <-t.C:
			s.persist()
		}
	}
}

// persist writes the current snapshot when it differs from the last write.
func (s *server) persist() {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.persistLocked()
}

// persistLocked writes the current snapshot when it changed since the last
// write. The persist lock is held.
func (s *server) persistLocked() {
	if s.persistOff {
		return
	}
	recs := s.snapshotRecords()
	data, err := marshalRecords(recs)
	if err != nil {
		return
	}
	if bytes.Equal(data, s.persistLast) {
		return
	}
	if err := writeRecordsData(s.home, data); err != nil {
		logf("writing %s: %v", sessionsName, err)
		return
	}
	s.persistLast = data
}

// freezePersist flushes one final snapshot and then stops writing, so the
// teardown kills that follow never turn a running session into an exited one in
// the file (spec 60 REQ-WR-004).
func (s *server) freezePersist() {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.persistLocked()
	s.persistOff = true
}
