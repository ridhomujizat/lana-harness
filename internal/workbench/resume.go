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
	// lookup returns the conversation id and the directory the agent runs in.
	lookup  func(q agentQuery) (id, dir string, ok bool)
	validID func(id string) bool
	resume  string // e.g. "claude --resume"
	cont    string // e.g. "claude --continue"
}

// agentQuery is the input an adapter needs to find an agent's conversation.
type agentQuery struct {
	Home    string    // the owner's home dir (os.UserHomeDir in prod; a temp dir in tests)
	Env     []string  // the session's environment (KEY=VALUE), for CLAUDE_CONFIG_DIR
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

// lookupAgentSession returns an agent's conversation id and the directory it
// runs in, or false when there is no adapter or the session was not found
// (spec 60 REQ-WR-010).
func lookupAgentSession(label string, q agentQuery) (id, dir string, ok bool) {
	a, found := adapterFor(label)
	if !found {
		return "", "", false
	}
	return a.lookup(q)
}

// resumeLine returns the line to type into a restored shell to resume an agent
// conversation, without a trailing newline (spec 60 REQ-WR-022). An empty or
// invalid id falls back to the --continue form, so only a validated id is ever
// typed (spec 60 REQ-WR-023).
func resumeLine(label, id string) (string, bool) {
	a, ok := adapterFor(label)
	if !ok {
		return "", false
	}
	if id != "" && a.validID(id) {
		return a.resume + " " + id, true
	}
	return a.cont, true
}

// claudeSessionsDir returns the directory holding a claude config's session
// files: CLAUDE_CONFIG_DIR from the environment when set, else <home>/.claude.
func claudeSessionsDir(q agentQuery) string {
	config := envValue(q.Env, "CLAUDE_CONFIG_DIR")
	if config == "" {
		if q.Home == "" {
			return ""
		}
		config = filepath.Join(q.Home, ".claude")
	}
	return filepath.Join(config, "sessions")
}

// claudeLookup reads <pid>.json from claude's session directory; its pid must
// match the agent's and its sessionId must be set (spec 60 REQ-WR-010).
func claudeLookup(q agentQuery) (id, dir string, ok bool) {
	if q.PID <= 0 {
		return "", "", false
	}
	sessions := claudeSessionsDir(q)
	if sessions == "" {
		return "", "", false
	}
	data, err := os.ReadFile(filepath.Join(sessions, strconv.Itoa(q.PID)+".json"))
	if err != nil {
		return "", "", false
	}
	var rec struct {
		PID       int    `json:"pid"`
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
	}
	if json.Unmarshal(data, &rec) != nil || rec.PID != q.PID || rec.SessionID == "" {
		return "", "", false
	}
	return rec.SessionID, rec.Cwd, true
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
func ompLookup(q agentQuery) (id, dir string, ok bool) {
	if q.Home == "" {
		return "", "", false
	}
	root := ompSessionRoot(q.Home)
	for _, path := range openFiles(q.PID) {
		if !strings.HasSuffix(path, ".jsonl") || !underRoot(path, root) {
			continue
		}
		if hID, hCwd, hOK := ompSessionHeader(path); hOK {
			return hID, hCwd, true
		}
		if hID := ompIDFromName(filepath.Base(path)); hID != "" {
			return hID, "", true
		}
	}
	if q.Cwd == "" {
		return "", "", false
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
		return "", "", false
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
		return "", "", false
	}
	return bestID, bestCwd, true
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
