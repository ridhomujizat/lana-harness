package claudechat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"
)

// Conversation is the Claude Code conversation running under a Workbench
// session's shell pid (spec 71 REQ-WC-001): the process and the transcript file
// it appends to. Path is empty until the transcript exists, which the caller
// reports as "waiting" (REQ-WC-003).
type Conversation struct {
	PID       int
	SessionID string
	Cwd       string
	ConfigDir string
	Path      string
	Started   time.Time
	Version   string
}

// maxAncestorSteps bounds the parent walk so a cycle or a very deep tree can
// never loop forever (REQ-WC-001).
const maxAncestorSteps = 16

// claudeIDPattern is a canonical UUID: 8-4-4-4-12 hex digits, case-insensitive.
var claudeIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Cache windows; package vars so tests can shorten them.
var (
	dirCacheTTL    = 30 * time.Second
	locateCacheTTL = 2 * time.Second
)

// parentOf and alive are indirected through package vars so tests can stub the
// process tree and liveness without spawning processes.
var (
	parentOf = parentPID
	alive    = processAlive
)

var (
	dirsMu      sync.Mutex
	dirsCache   []string
	dirsCacheAt time.Time

	locateMu    sync.Mutex
	locateCache = map[int]locateEntry{}
)

type locateEntry struct {
	conv Conversation
	ok   bool
	at   time.Time
}

// sessionFile is the part of a sessions/*.json file Locate reads.
type sessionFile struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	StartedAt int64  `json:"startedAt"`
	Version   string `json:"version"`
}

// Locate finds the Claude Code conversation whose process runs under shellPID.
// It scans the known config dirs, keeps only sessions whose pid is alive and
// whose parent chain reaches shellPID, and returns the newest match. The result
// is cached per shellPID for locateCacheTTL so the 300 ms poll does not rescan;
// /clear (a new session id in the same pid file) shows up after that.
func Locate(shellPID int, home string) (Conversation, bool) {
	if shellPID <= 0 {
		return Conversation{}, false
	}

	locateMu.Lock()
	if e, ok := locateCache[shellPID]; ok && time.Since(e.at) < locateCacheTTL {
		locateMu.Unlock()
		return e.conv, e.ok
	}
	locateMu.Unlock()

	conv, ok := locate(shellPID, home)

	locateMu.Lock()
	locateCache[shellPID] = locateEntry{conv: conv, ok: ok, at: time.Now()}
	locateMu.Unlock()
	return conv, ok
}

// locate does the uncached scan.
func locate(shellPID int, home string) (Conversation, bool) {
	var best Conversation
	found := false
	for _, dir := range configDirs(home) {
		files, _ := filepath.Glob(filepath.Join(dir, "sessions", "*.json"))
		for _, file := range files {
			conv, ok := readSession(file)
			if !ok {
				continue
			}
			if !alive(conv.PID) || !isDescendant(shellPID, conv.PID) {
				continue
			}
			if !found || conv.Started.After(best.Started) {
				conv.ConfigDir = dir
				best = conv
				found = true
			}
		}
	}
	if !found {
		return Conversation{}, false
	}
	best.Path = transcriptPath(best.ConfigDir, best.SessionID)
	return best, true
}

// configDirs returns the Claude config dirs that exist: ~/.claude, every
// ~/.claude-swap-backup/sessions/* profile, and CLAUDE_CONFIG_DIR when set. The
// list is cached for dirCacheTTL.
func configDirs(home string) []string {
	dirsMu.Lock()
	defer dirsMu.Unlock()

	if dirsCache != nil && time.Since(dirsCacheAt) < dirCacheTTL {
		return dirsCache
	}

	var dirs []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		if seen[clean] {
			return
		}
		if st, err := os.Stat(clean); err != nil || !st.IsDir() {
			return
		}
		seen[clean] = true
		dirs = append(dirs, clean)
	}

	if home != "" {
		add(filepath.Join(home, ".claude"))
		backups, _ := filepath.Glob(filepath.Join(home, ".claude-swap-backup", "sessions", "*"))
		for _, b := range backups {
			add(b)
		}
	}
	add(os.Getenv("CLAUDE_CONFIG_DIR"))

	dirsCache = dirs
	dirsCacheAt = time.Now()
	return dirs
}

// readSession parses a sessions/*.json file. A file that fails to parse, has no
// pid, or has a session id that is not a canonical UUID is skipped.
func readSession(file string) (Conversation, bool) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return Conversation{}, false
	}
	var s sessionFile
	if json.Unmarshal(raw, &s) != nil {
		return Conversation{}, false
	}
	if s.PID <= 0 || !claudeIDPattern.MatchString(s.SessionID) {
		return Conversation{}, false
	}
	return Conversation{
		PID:       s.PID,
		SessionID: s.SessionID,
		Cwd:       s.Cwd,
		Started:   time.UnixMilli(s.StartedAt),
		Version:   s.Version,
	}, true
}

// isDescendant reports whether pid's parent chain reaches ancestor within
// maxAncestorSteps. pid == ancestor counts.
func isDescendant(ancestor, pid int) bool {
	if pid <= 0 || ancestor <= 0 {
		return false
	}
	if pid == ancestor {
		return true
	}
	cur := pid
	for i := 0; i < maxAncestorSteps; i++ {
		parent, ok := parentOf(cur)
		if !ok || parent <= 0 {
			return false
		}
		if parent == ancestor {
			return true
		}
		cur = parent
	}
	return false
}

// transcriptPath globs <configDir>/projects/*/<sessionID>.jsonl and returns the
// first match, or "" when the transcript does not exist yet.
func transcriptPath(configDir, sessionID string) string {
	matches, _ := filepath.Glob(filepath.Join(configDir, "projects", "*", sessionID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// processAlive reports whether pid exists: kill(pid, 0) succeeds or is refused
// with EPERM (a process we may not signal is still alive).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
