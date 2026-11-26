package workbench

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/claudeshare"
)

// Spec 83: Antigravity (agy) panes resume and switch accounts like Claude Code.
// agy hands its hooks the conversation id; Lana's hooks record it per pane
// (REQ-WAG-001) and the core's agy adapter reads that record.

// envWorkbenchHome names the core's Lana home in every pane's environment, so
// a hook running under an Antigravity account HOME finds the core's folder.
const envWorkbenchHome = "LANA_WORKBENCH_HOME"

// EnvWorkbenchHome is envWorkbenchHome for the hooks.
const EnvWorkbenchHome = envWorkbenchHome

// AgentAgy is the agent label of Antigravity panes.
const AgentAgy = "agy"

// agyFileTokenEnv is the environment the popup sets for agy of an added
// account (spec 77 revision 4): agy keeps its login in file token storage
// under HOME instead of the Keychain.
const agyFileTokenEnv = "SSH_CONNECTION='127.0.0.1 0 127.0.0.1 0'"

// agentRecordMax bounds the bytes read from a pane's agent record.
const agentRecordMax = 16 << 10

var (
	coreHomeMu  sync.Mutex
	coreHomeDir string
)

// setCoreHome records the Lana home of the core running in this process.
func setCoreHome(home string) {
	coreHomeMu.Lock()
	coreHomeDir = home
	coreHomeMu.Unlock()
}

// coreHome returns the Lana home of the core running in this process, "" when
// none runs here.
func coreHome() string {
	coreHomeMu.Lock()
	defer coreHomeMu.Unlock()
	return coreHomeDir
}

// agyIDPattern is the shape an agy conversation id must have to be recorded
// or typed: shell-safe, no path separator.
var agyIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,127}$`)

// isAgyID reports whether id can be an agy conversation id.
func isAgyID(id string) bool { return agyIDPattern.MatchString(id) }

// paneIDPattern is the shape of a pane id a record may be named after.
var paneIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// AgentRecord is what a hook records about the agent conversation of a pane
// (spec 83 REQ-WAG-001).
type AgentRecord struct {
	Agent          string    `json:"agent"`
	ConversationID string    `json:"conversation_id"`
	Home           string    `json:"home,omitempty"`
	At             time.Time `json:"at"`
}

// agentRecordPath is <lanaHome>/workbench/agents/<pane>.json.
func agentRecordPath(lanaHome, pane string) string {
	return filepath.Join(Dir(lanaHome), "agents", pane+".json")
}

// WriteAgentRecord records rec for pane under lanaHome: 0600, written
// atomically. A pane id or conversation id of another shape is refused.
func WriteAgentRecord(lanaHome, pane string, rec AgentRecord) error {
	if lanaHome == "" || !paneIDPattern.MatchString(pane) {
		return errors.New("workbench: no pane to record")
	}
	if rec.Agent != AgentAgy || !isAgyID(rec.ConversationID) {
		return errors.New("workbench: not an agy conversation id")
	}
	path := agentRecordPath(lanaHome, pane)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent-*")
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
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// readAgentRecord reads the record of a pane.
func readAgentRecord(lanaHome, pane string) (AgentRecord, bool) {
	if lanaHome == "" || !paneIDPattern.MatchString(pane) {
		return AgentRecord{}, false
	}
	f, err := os.Open(agentRecordPath(lanaHome, pane))
	if err != nil {
		return AgentRecord{}, false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, agentRecordMax))
	if err != nil {
		return AgentRecord{}, false
	}
	var rec AgentRecord
	if json.Unmarshal(data, &rec) != nil {
		return AgentRecord{}, false
	}
	return rec, true
}

// removeAgentRecord drops a pane's record, so a later agy in the pane is not
// matched to it.
func removeAgentRecord(lanaHome, pane string) {
	if lanaHome == "" || !paneIDPattern.MatchString(pane) {
		return
	}
	_ = os.Remove(agentRecordPath(lanaHome, pane))
}

// agyLookup is the agy adapter's lookup: the pane's record, when it is newer
// than the moment the core first saw this agy. Its HOME is the account the
// conversation runs on; the owner's HOME is the built-in account ("").
func agyLookup(q agentQuery) (agentFound, bool) {
	rec, ok := readAgentRecord(q.LanaHome, q.SessionID)
	if !ok || rec.Agent != AgentAgy || !isAgyID(rec.ConversationID) {
		return agentFound{}, false
	}
	if !q.Started.IsZero() && rec.At.Before(q.Started) {
		return agentFound{}, false
	}
	found := agentFound{ID: rec.ConversationID}
	if rec.Home != "" && filepath.IsAbs(rec.Home) && !sameDir(rec.Home, q.Home) {
		found.ConfigDir = filepath.Clean(rec.Home)
	}
	return found, true
}

// sameDir reports whether two paths name the same folder.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// agyResumeLine is the agy line for an account HOME, as the Accounts popup
// launches it: `env HOME='<home>' SSH_CONNECTION='…' agy <tail>`; home "" is
// the built-in account, `agy <tail>`. A home that does not exist or cannot be
// single-quoted gives false.
func agyResumeLine(home, tail string) (string, bool) {
	if home == "" {
		return "agy " + tail, true
	}
	if !safeConfigDir(home) || !isDir(home) {
		return "", false
	}
	return "env HOME='" + home + "' " + agyFileTokenEnv + " agy " + tail, true
}

// relinkAgyMCP is the spec 83 REQ-WAG-004 relink; tests may replace it.
var relinkAgyMCP = claudeshare.RelinkAgyMCPConfig

// relinkRestoredAgy relinks a restored agy pane's mcp_config.json to the
// owner's before its resume line is typed (spec 83 REQ-WAG-004), when the
// pane's HOME is a named account's: directly under <lanaHome>/antigravity.
// It reports whether the relink ran; a failure never stops the resume.
func relinkRestoredAgy(agent, configDir, lanaHome, home string) bool {
	if agent != AgentAgy || !namedAccountDir(configDir, lanaHome, "antigravity") || home == "" {
		return false
	}
	if _, err := relinkAgyMCP(configDir, home, time.Now()); err != nil {
		logf("relink agy mcp_config.json: %v", err)
	}
	return true
}
