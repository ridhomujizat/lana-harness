package workbench

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// agentAdapter knows how to find a coding agent's conversation id and how to
// resume it (spec 60 REQ-WR-012).
type agentAdapter struct {
	label string
	// lookup returns what was found for the agent: its conversation id, the
	// directory it runs in, and the claude config dir it was found under when
	// that came from an explicit CLAUDE_CONFIG_DIR.
	lookup  func(q agentQuery) (agentFound, bool)
	validID func(id string) bool
	resume  string // e.g. "claude --resume"
	cont    string // e.g. "claude --continue"
}

// agentFound is what an adapter's lookup returns (spec 60 REQ-WR-010). ConfigDir
// is the explicit CLAUDE_CONFIG_DIR the conversation was found under, and is ""
// for the default <home>/.claude and for every non-claude agent.
type agentFound struct {
	ID        string
	Dir       string
	ConfigDir string
}

// agentQuery is the input an adapter needs to find an agent's conversation.
type agentQuery struct {
	Home    string    // the owner's home dir (os.UserHomeDir in prod; a temp dir in tests)
	Env     []string  // the session's environment (KEY=VALUE), for CLAUDE_CONFIG_DIR
	ProcEnv []string  // the agent process's own environment (KEY=VALUE), for CLAUDE_CONFIG_DIR
	PID     int       // agent pid from agentdetect.Identify
	Cwd     string    // the agent's working directory as the core knows it ("" when unknown)
	Started time.Time // when the core first saw this agent pid; zero = unknown
}

// agentAdapters is the adapter table (spec 60 REQ-WR-012). A later agent is one
// entry and its own lookup.
var agentAdapters = []agentAdapter{
	{
		label:   "claude",
		lookup:  claudeLookup,
		validID: isClaudeID,
		resume:  "claude --resume",
		cont:    "claude --continue",
	},
	{
		label:   "omp",
		lookup:  ompLookup,
		validID: isOmpID,
		resume:  "omp --resume",
		cont:    "omp --continue",
	},
}

// adapterFor returns the adapter for an agent label, or false for an agent that
// has none.
func adapterFor(label string) (agentAdapter, bool) {
	for _, a := range agentAdapters {
		if a.label == label {
			return a, true
		}
	}
	return agentAdapter{}, false
}

// lookupAgentSession returns what an adapter found for an agent, or false when
// there is no adapter or the session was not found (spec 60 REQ-WR-010).
func lookupAgentSession(label string, q agentQuery) (agentFound, bool) {
	a, found := adapterFor(label)
	if !found {
		return agentFound{}, false
	}
	return a.lookup(q)
}

// resumeLine returns the line to type into a restored shell to resume an agent
// conversation, without a trailing newline (spec 60 REQ-WR-022). An empty or
// invalid id falls back to the --continue form, so only a validated id is ever
// typed (spec 60 REQ-WR-023). When the conversation was found under an explicit
// claude config dir, the line runs under that account again (spec 60 addendum:
// claude-swap profiles); env and home are the restored session's environment
// and the owner's home.
func resumeLine(label, id, configDir, home string, env []string) (string, bool) {
	a, ok := adapterFor(label)
	if !ok {
		return "", false
	}
	cmd := a.resume
	if i := strings.IndexByte(cmd, ' '); i >= 0 {
		cmd = cmd[:i]
	}
	tail := "--continue"
	if id != "" && a.validID(id) {
		tail = "--resume " + id
	}
	if label == "claude" && configDir != "" {
		if line, ok := claudeResumeLine(configDir, tail, home, env); ok {
			return line, true
		}
	}
	return cmd + " " + tail, true
}

// claudeResumeLine returns the claude resume line for a known config dir, or
// false when the plain `claude <tail>` form is what should be typed. A
// claude-swap profile whose account email can be read, with cswap installed,
// runs under that account: `cswap run <email> -- <tail>`. Any other config dir
// that still exists and is safe to quote is passed as CLAUDE_CONFIG_DIR and
// CLAUDE_SECURESTORAGE_CONFIG_DIR (spec 77 REQ-WRA-006).
// Every failure falls through to the plain form (spec 60 addendum).
func claudeResumeLine(configDir, tail, home string, env []string) (string, bool) {
	if !isDir(configDir) {
		return "", false
	}
	if isCswapProfile(configDir, home) && cswapInstalled(env, home) {
		if email := claudeAccountEmail(configDir); emailPattern.MatchString(email) {
			return "cswap run " + email + " -- " + tail, true
		}
	}
	if safeConfigDir(configDir) {
		// Spec 77 REQ-WRA-006: a Router account keeps its Keychain login under
		// CLAUDE_SECURESTORAGE_CONFIG_DIR, so both are set.
		return "CLAUDE_CONFIG_DIR='" + configDir + "' CLAUDE_SECURESTORAGE_CONFIG_DIR='" + configDir + "' claude " + tail, true
	}
	return "", false
}

// isCswapProfile reports whether dir is a claude-swap profile directory: one
// whose parent is <home>/.claude-swap-backup/sessions.
func isCswapProfile(dir, home string) bool {
	if home == "" {
		return false
	}
	return filepath.Dir(dir) == filepath.Join(home, ".claude-swap-backup", "sessions")
}

// claudeAccountEmail reads oauthAccount.emailAddress from a claude config's
// .claude.json, or "" when it is missing or unreadable.
func claudeAccountEmail(configDir string) string {
	data, err := os.ReadFile(filepath.Join(configDir, ".claude.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		OAuthAccount struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	return cfg.OAuthAccount.EmailAddress
}

// emailPattern matches a plain email address, which is all that is ever typed
// after "cswap run".
var emailPattern = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+$`)

// cswapInstalled reports whether the cswap launcher is installed: cswap in the
// PATH of the restored session's environment, or in <home>/.local/bin (the uv
// tool default; the core runs under launchd with a short PATH). The file must
// be a regular executable file. It is a variable so tests can override it.
var cswapInstalled = func(env []string, home string) bool {
	dirs := filepath.SplitList(envValueLast(env, "PATH"))
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if isExecutableFile(filepath.Join(d, "cswap")) {
			return true
		}
	}
	return false
}

// isExecutableFile reports whether path names a regular executable file.
func isExecutableFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// safeConfigDir reports whether dir can be typed as a single-quoted
// CLAUDE_CONFIG_DIR value: an absolute path with no single quote and no control
// character.
func safeConfigDir(dir string) bool {
	if !filepath.IsAbs(dir) {
		return false
	}
	for i := 0; i < len(dir); i++ {
		if dir[i] == '\'' || dir[i] < 0x20 || dir[i] == 0x7f {
			return false
		}
	}
	return true
}

// claudeConfigDir returns the claude config directory and whether it came from
// an explicit CLAUDE_CONFIG_DIR: the agent process's own environment first, then
// the session's environment, else <home>/.claude (spec 60 REQ-WR-011).
func claudeConfigDir(q agentQuery) (dir string, explicit bool) {
	if v := envValue(q.ProcEnv, "CLAUDE_CONFIG_DIR"); v != "" {
		return v, true
	}
	if v := envValue(q.Env, "CLAUDE_CONFIG_DIR"); v != "" {
		return v, true
	}
	if q.Home == "" {
		return "", false
	}
	return filepath.Join(q.Home, ".claude"), false
}

// claudeLookup reads <pid>.json from claude's session directory; its pid must
// match the agent's and its sessionId must be set (spec 60 REQ-WR-010).
func claudeLookup(q agentQuery) (agentFound, bool) {
	if q.PID <= 0 {
		return agentFound{}, false
	}
	config, explicit := claudeConfigDir(q)
	if config == "" {
		return agentFound{}, false
	}
	data, err := os.ReadFile(filepath.Join(config, "sessions", strconv.Itoa(q.PID)+".json"))
	if err != nil {
		return agentFound{}, false
	}
	var rec struct {
		PID       int    `json:"pid"`
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
	}
	if json.Unmarshal(data, &rec) != nil || rec.PID != q.PID || rec.SessionID == "" {
		return agentFound{}, false
	}
	found := agentFound{ID: rec.SessionID, Dir: rec.Cwd}
	if explicit {
		found.ConfigDir = config
	}
	return found, true
}

// claudeIDPattern is a canonical UUID: 8-4-4-4-12 hex digits, case-insensitive.
var claudeIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isClaudeID reports whether id is a canonical UUID.
func isClaudeID(id string) bool { return claudeIDPattern.MatchString(id) }

// ompIDPattern is the shape an omp session id may have.
var ompIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

// isOmpID reports whether id looks like an omp session id.
func isOmpID(id string) bool { return ompIDPattern.MatchString(id) }

// ompHeaderMax bounds the bytes read from a session file for its header line.
const ompHeaderMax = 64 << 10

// ompSessionRoot returns the directory holding omp's session files.
func ompSessionRoot(home string) string {
	return filepath.Join(home, ".omp", "agent", "sessions")
}

// ompLookup finds an omp conversation: first from a session file the agent has
// open, then from the newest session file under the sessions root whose header
// cwd matches the agent's directory (spec 60 REQ-WR-010).
func ompLookup(q agentQuery) (agentFound, bool) {
	if q.Home == "" {
		return agentFound{}, false
	}
	root := ompSessionRoot(q.Home)
	for _, path := range openFiles(q.PID) {
		if !strings.HasSuffix(path, ".jsonl") || !underRoot(path, root) {
			continue
		}
		if hID, hCwd, hOK := ompSessionHeader(path); hOK {
			return agentFound{ID: hID, Dir: hCwd}, true
		}
		if hID := ompIDFromName(filepath.Base(path)); hID != "" {
			return agentFound{ID: hID}, true
		}
	}
	if q.Cwd == "" {
		return agentFound{}, false
	}
	want := physicalDir(q.Cwd)
	var (
		bestID  string
		bestCwd string
		bestMod time.Time
		found   bool
	)
	subs, err := os.ReadDir(root)
	if err != nil {
		return agentFound{}, false
	}
	for _, sub := range subs {
		if !sub.IsDir() {
			continue
		}
		subdir := filepath.Join(root, sub.Name())
		files, err := os.ReadDir(subdir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			if !q.Started.IsZero() && info.ModTime().Before(q.Started) {
				continue
			}
			hID, hCwd, hOK := ompSessionHeader(filepath.Join(subdir, f.Name()))
			if !hOK || physicalDir(hCwd) != want {
				continue
			}
			if found && !info.ModTime().After(bestMod) {
				continue
			}
			bestID, bestCwd, bestMod, found = hID, hCwd, info.ModTime(), true
		}
	}
	if !found {
		return agentFound{}, false
	}
	return agentFound{ID: bestID, Dir: bestCwd}, true
}

// ompSessionHeader reads the "session" header line from a session file, looking
// at no more than the first 5 lines and the first 64 KiB. A missing or garbled
// header is not an error.
func ompSessionHeader(path string) (id, cwd string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, ompHeaderMax))
	if err != nil {
		return "", "", false
	}
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines) && i < 5; i++ {
		var rec struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Cwd  string `json:"cwd"`
		}
		if json.Unmarshal([]byte(lines[i]), &rec) != nil {
			continue
		}
		if rec.Type == "session" && rec.ID != "" {
			return rec.ID, rec.Cwd, true
		}
	}
	return "", "", false
}

// ompIDFromName returns the id embedded in a session file name: the part after
// the last "_", with the ".jsonl" suffix removed.
func ompIDFromName(name string) string {
	base := strings.TrimSuffix(name, ".jsonl")
	i := strings.LastIndexByte(base, '_')
	if i < 0 {
		return ""
	}
	return base[i+1:]
}

// underRoot reports whether path is a file below root.
func underRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// envValue returns the value of key in a KEY=VALUE environment, or "".
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, prefix); ok {
			return v
		}
	}
	return ""
}

// envValueLast returns the value of key in a KEY=VALUE environment, a later
// entry winning over an earlier one the way the OS reads the list, or "".
func envValueLast(env []string, key string) string {
	prefix := key + "="
	value := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, prefix); ok {
			value = v
		}
	}
	return value
}

// splitNul splits a NUL-separated byte string, dropping empty entries, the form
// the environment of a process is read in on both macOS and Linux.
func splitNul(b []byte) []string {
	var out []string
	for _, part := range strings.Split(string(b), "\x00") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
