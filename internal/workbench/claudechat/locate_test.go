package claudechat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	uuidA = "11111111-1111-1111-1111-111111111111"
	uuidB = "22222222-2222-2222-2222-222222222222"
	uuidC = "33333333-3333-3333-3333-333333333333"
)

// resetCaches clears the package caches so tests do not see each other's dirs
// and sessions, and disables caching unless the test sets a window itself.
func resetCaches(t *testing.T) {
	t.Helper()
	dirsMu.Lock()
	dirsCache = nil
	dirsCacheAt = time.Time{}
	dirsMu.Unlock()
	locateMu.Lock()
	locateCache = map[int]locateEntry{}
	locateMu.Unlock()

	oldDir, oldLocate := dirCacheTTL, locateCacheTTL
	dirCacheTTL, locateCacheTTL = 0, 0
	t.Cleanup(func() { dirCacheTTL, locateCacheTTL = oldDir, oldLocate })
}

// stubProcesses replaces the process tree and liveness probes for a test.
func stubProcesses(t *testing.T, parents map[int]int, live map[int]bool) {
	t.Helper()
	oldParent, oldAlive := parentOf, alive
	parentOf = func(pid int) (int, bool) {
		p, ok := parents[pid]
		return p, ok
	}
	alive = func(pid int) bool { return live[pid] }
	t.Cleanup(func() { parentOf, alive = oldParent, oldAlive })
}

// writeSession writes a sessions/<id>.json file and returns its path.
func writeSession(t *testing.T, dir string, pid int, sessionID string, startedAt int64) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body, err := json.Marshal(map[string]any{
		"pid":       pid,
		"sessionId": sessionID,
		"cwd":       "/work",
		"startedAt": startedAt,
		"version":   "2.1.330",
	})
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	name := sessionID + ".json"
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
}

// writeTranscript creates a projects/<enc>/<id>.jsonl file under a config dir.
func writeTranscript(t *testing.T, configDir, sessionID string) {
	t.Helper()
	dir := filepath.Join(configDir, "projects", "-x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func TestLocateAncestorChain(t *testing.T) {
	resetCaches(t)
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	sessions := filepath.Join(configDir, "sessions")
	writeSession(t, sessions, 103, uuidA, 1000)
	writeTranscript(t, configDir, uuidA)

	stubProcesses(t,
		map[int]int{103: 102, 102: 101, 101: 100},
		map[int]bool{103: true})

	conv, ok := Locate(100, home)
	if !ok {
		t.Fatalf("Locate: not found")
	}
	if conv.PID != 103 || conv.SessionID != uuidA || conv.Cwd != "/work" {
		t.Fatalf("conversation = %+v", conv)
	}
	if conv.ConfigDir != filepath.Clean(configDir) {
		t.Errorf("config dir = %q, want %q", conv.ConfigDir, configDir)
	}
	if conv.Path != filepath.Join(configDir, "projects", "-x", uuidA+".jsonl") {
		t.Errorf("path = %q", conv.Path)
	}
	if !conv.Started.Equal(time.UnixMilli(1000)) {
		t.Errorf("started = %v", conv.Started)
	}
	if conv.Version != "2.1.330" {
		t.Errorf("version = %q", conv.Version)
	}
}

func TestLocateSelfMatches(t *testing.T) {
	resetCaches(t)
	home := t.TempDir()
	writeSession(t, filepath.Join(home, ".claude", "sessions"), 100, uuidA, 1000)
	stubProcesses(t, map[int]int{}, map[int]bool{100: true})

	if conv, ok := Locate(100, home); !ok || conv.PID != 100 {
		t.Fatalf("Locate = %+v, %v; want the session itself", conv, ok)
	}
}

func TestLocateNonDescendantIgnored(t *testing.T) {
	resetCaches(t)
	home := t.TempDir()
	writeSession(t, filepath.Join(home, ".claude", "sessions"), 200, uuidA, 1000)
	stubProcesses(t,
		map[int]int{200: 1, 1: 0},
		map[int]bool{200: true})

	if _, ok := Locate(100, home); ok {
		t.Fatalf("Locate found a non-descendant session")
	}
}

func TestLocateDeadPIDIgnored(t *testing.T) {
	resetCaches(t)
	home := t.TempDir()
	writeSession(t, filepath.Join(home, ".claude", "sessions"), 103, uuidA, 1000)
	stubProcesses(t,
		map[int]int{103: 102, 102: 100},
		map[int]bool{103: false})

	if _, ok := Locate(100, home); ok {
		t.Fatalf("Locate found a dead session")
	}
}

func TestLocateBadUUIDIgnored(t *testing.T) {
	resetCaches(t)
	home := t.TempDir()
	writeSession(t, filepath.Join(home, ".claude", "sessions"), 103, "not-a-uuid", 1000)
	stubProcesses(t,
		map[int]int{103: 100},
		map[int]bool{103: true})

	if _, ok := Locate(100, home); ok {
		t.Fatalf("Locate accepted a non-UUID session id")
	}
}

func TestLocateNewestStartedWins(t *testing.T) {
	resetCaches(t)
	home := t.TempDir()
	sessions := filepath.Join(home, ".claude", "sessions")
	writeSession(t, sessions, 101, uuidA, 1000)
	writeSession(t, sessions, 102, uuidB, 3000)
	stubProcesses(t,
		map[int]int{101: 100, 102: 100},
		map[int]bool{101: true, 102: true})

	conv, ok := Locate(100, home)
	if !ok {
		t.Fatalf("Locate: not found")
	}
	if conv.SessionID != uuidB {
		t.Fatalf("conversation = %+v, want the newest (uuidB)", conv)
	}
}

func TestLocateMissingTranscript(t *testing.T) {
	resetCaches(t)
	home := t.TempDir()
	writeSession(t, filepath.Join(home, ".claude", "sessions"), 103, uuidA, 1000)
	stubProcesses(t, map[int]int{103: 100}, map[int]bool{103: true})

	conv, ok := Locate(100, home)
	if !ok {
		t.Fatalf("Locate: not found")
	}
	if conv.Path != "" {
		t.Fatalf("path = %q, want empty (waiting)", conv.Path)
	}
}

func TestLocateSwapBackupDir(t *testing.T) {
	resetCaches(t)
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude-swap-backup", "sessions", "p1")
	writeSession(t, filepath.Join(configDir, "sessions"), 103, uuidC, 1000)
	writeTranscript(t, configDir, uuidC)
	stubProcesses(t, map[int]int{103: 100}, map[int]bool{103: true})

	conv, ok := Locate(100, home)
	if !ok || conv.SessionID != uuidC {
		t.Fatalf("Locate = %+v, %v; want the swap-backup session", conv, ok)
	}
}

func TestLocateLanaAccountDir(t *testing.T) {
	resetCaches(t)
	home := t.TempDir()
	lana := filepath.Join(t.TempDir(), "lana")
	t.Setenv("LANA_HOME", lana)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	// The account's projects is a symlink to ~/.claude/projects (spec 77).
	shared := filepath.Join(home, ".claude")
	writeTranscript(t, shared, uuidC)
	account := filepath.Join(lana, "claude", "claude-3")
	if err := os.MkdirAll(account, 0o755); err != nil {
		t.Fatalf("mkdir account: %v", err)
	}
	if err := os.Symlink(filepath.Join(shared, "projects"), filepath.Join(account, "projects")); err != nil {
		t.Fatalf("symlink projects: %v", err)
	}
	writeSession(t, filepath.Join(account, "sessions"), 103, uuidC, 1000)
	// The shared mcp dir is not an account and is never scanned.
	writeSession(t, filepath.Join(lana, "claude", "mcp", "sessions"), 104, uuidB, 2000)
	stubProcesses(t, map[int]int{103: 100, 104: 100}, map[int]bool{103: true, 104: true})

	conv, ok := Locate(100, home)
	if !ok || conv.SessionID != uuidC {
		t.Fatalf("Locate = %+v, %v; want the Lana account session", conv, ok)
	}
	if conv.ConfigDir != account {
		t.Fatalf("ConfigDir = %q, want %q", conv.ConfigDir, account)
	}
	want := filepath.Join(account, "projects", "-x", uuidC+".jsonl")
	if conv.Path != want {
		t.Fatalf("Path = %q, want %q", conv.Path, want)
	}
}

func TestLocateCacheExpiry(t *testing.T) {
	resetCaches(t)
	home := t.TempDir()
	sessions := filepath.Join(home, ".claude", "sessions")
	writeSession(t, sessions, 103, uuidA, 1000)
	stubProcesses(t, map[int]int{103: 100}, map[int]bool{103: true})

	locateCacheTTL = 20 * time.Millisecond

	conv, ok := Locate(100, home)
	if !ok || conv.SessionID != uuidA {
		t.Fatalf("first Locate = %+v, %v", conv, ok)
	}

	// /clear rewrites the same pid file with a new session id.
	writeSession(t, sessions, 103, uuidB, 2000)

	// Within the window the cached answer is returned.
	if conv, _ := Locate(100, home); conv.SessionID != uuidA {
		t.Fatalf("cached Locate = %+v, want uuidA", conv)
	}

	time.Sleep(30 * time.Millisecond)
	conv, ok = Locate(100, home)
	if !ok || conv.SessionID != uuidB {
		t.Fatalf("expired Locate = %+v, %v; want uuidB", conv, ok)
	}
}
