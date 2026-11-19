// Package workbench is Lana's terminal session core and its client.
//
// The core owns the pseudo-terminals: shells and coding agents keep running
// when the Lana daemon stops or is upgraded, and the next launch attaches to
// them again, with what they printed replayed. Clients talk to it over a Unix
// socket: a control connection of JSON lines (requests and their responses),
// one connection per attached session that carries the terminal's bytes both
// ways, and one connection per subscription that streams session events.
package workbench

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"time"
)

// ProtocolVersion changes whenever the wire protocol does. A client that meets
// a core of another version reports ErrVersionMismatch and does not use it.
const ProtocolVersion = 1

// Tunables of the core and the client.
const (
	// ringSize is how much output a session keeps for a later attach.
	ringSize = 1 << 20
	// attachQueue bounds one attachment's pending output chunks; a client
	// beyond it is dropped so it never holds up the pty.
	attachQueue = 256
	// subscribeQueue bounds one subscription's pending events; a client
	// beyond it is dropped so a slow reader never holds up the hub.
	subscribeQueue = 256
	// updateInterval is how often the hub polls the sessions for changes and
	// the shortest gap between two session.updated events of one session.
	updateInterval = 500 * time.Millisecond
	// layoutMax bounds the opaque layout document.
	layoutMax = 256 << 10
	// workingWindow is how long after its last output a session is "working".
	workingWindow = 2 * time.Second
	// killGrace is how long a hung-up session may take before SIGKILL.
	killGrace = 2 * time.Second
	// coreStartTimeout is how long Ensure waits for a spawned core's socket.
	coreStartTimeout = 3 * time.Second
	// callTimeout bounds one control request.
	callTimeout = 15 * time.Second
	// defaultCols and defaultRows size a session when the client passes none.
	defaultCols = 80
	defaultRows = 24
)

// Attach replay framing. A replay carries, in order, the pty size in effect at
// its first byte, the bytes with a size marker wherever the size changed, and a
// final replay-end marker. A terminal ignores an OSC string whose number it does
// not know, so these markers pass through attach clients that do not understand
// them; the web client (web/ui/src/views/workbench-term.tsx) consumes them to
// replay each byte at the pty size it was produced at.
//
// The size marker is an OSC 7701 string, ";<cols>;<rows>" with decimal sizes,
// and the replay-end marker is an empty OSC 7702 string.
const replayEndMarker = "\x1b]7702\x07"

// sizeMarker returns the OSC 7701 string that sets the pty size at the point it
// appears in a replay.
func sizeMarker(cols, rows int) []byte {
	return []byte("\x1b]7701;" + strconv.Itoa(cols) + ";" + strconv.Itoa(rows) + "\x07")
}

// Attach modes.
const (
	modeControl = "control"
	modeView    = "view"
)

// Event types of a subscription stream.
const (
	evCreated = "session.created"
	evUpdated = "session.updated"
	evRemoved = "session.removed"
	evLayout  = "layout.changed"
)

// Error codes of the control protocol.
const codeNotFound = "not_found"

// ErrNotFound is returned for an unknown session id.
var ErrNotFound = errors.New("workbench: session not found")

// ErrVersionMismatch is returned when the core speaks another protocol
// version.
var ErrVersionMismatch = errors.New("workbench_version_mismatch")

// Session describes a session and what runs in it now.
type Session struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Command    []string          `json:"command"`
	Dir        string            `json:"dir"`
	Labels     map[string]string `json:"labels,omitempty"`
	PID        int               `json:"pid"`
	Created    time.Time         `json:"created"`
	Cols       int               `json:"cols"`
	Rows       int               `json:"rows"`
	Program    string            `json:"program"`
	Cwd        string            `json:"cwd,omitempty"`
	State      string            `json:"state"`
	ExitCode   int               `json:"exit_code"`
	NeedsYou   bool              `json:"needs_you"`
	LastOutput time.Time         `json:"last_output"`
	Attached   int               `json:"attached"`
	// Agent is the coding agent running in the foreground job, "" when none,
	// and AgentState is its screen-derived state (spec 55 A3).
	Agent      string `json:"agent"`
	AgentState string `json:"agent_state"`
	// Restored is when the session was brought back after a core restart,
	// omitted when it was not; ResumedAgent is the agent whose conversation was
	// typed into it at restore, omitted when nothing was typed (spec 60
	// REQ-WR-040). Both are additive to protocol version 1.
	Restored     *time.Time `json:"restored,omitempty"`
	ResumedAgent string     `json:"resumed_agent,omitempty"`
}

// Event is one line of a subscription stream (REQ-WB-030): what happened, and
// the session or layout it happened to.
type Event struct {
	Type    string          `json:"type"`
	Session *Session        `json:"session,omitempty"`
	Layout  json.RawMessage `json:"layout,omitempty"`
}

// CreateOptions starts a session.
type CreateOptions struct {
	Command []string          `json:"command,omitempty"`
	Dir     string            `json:"dir,omitempty"`
	Env     []string          `json:"env,omitempty"`
	Cols    int               `json:"cols,omitempty"`
	Rows    int               `json:"rows,omitempty"`
	Title   string            `json:"title,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
}

// Hello is the core's answer to "hello".
type Hello struct {
	Version int       `json:"version"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// Request is a line of the control connection, from the client.
type Request struct {
	ID     int64           `json:"id"`
	Op     string          `json:"op"`
	SID    string          `json:"sid,omitempty"`
	Mode   string          `json:"mode,omitempty"`
	Cols   int             `json:"cols,omitempty"`
	Rows   int             `json:"rows,omitempty"`
	Create *CreateOptions  `json:"create,omitempty"`
	Layout json.RawMessage `json:"layout,omitempty"`
}

// Response answers the Request with the same ID.
type Response struct {
	ID    int64           `json:"id"`
	Error string          `json:"error,omitempty"`
	Code  string          `json:"code,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// attachResult is the one-line answer to an attach request; what follows on
// the connection is the raw terminal stream.
type attachResult struct {
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
}

// codeError is a server error carrying its kind, so errors.Is finds the
// sentinel the wire code stands for.
type codeError struct {
	msg  string
	kind error
}

func (e *codeError) Error() string { return e.msg }
func (e *codeError) Unwrap() error { return e.kind }

// codeFor maps an error to its wire code, or "" for a plain error.
func codeFor(err error) string {
	if errors.Is(err, ErrNotFound) {
		return codeNotFound
	}
	return ""
}

// kindFor maps a wire code back to the sentinel it stands for.
func kindFor(code string) error {
	switch code {
	case codeNotFound:
		return ErrNotFound
	}
	return nil
}

// errorFor turns a failed Response into the error the client returns.
func errorFor(res Response) error {
	if res.Error == "" {
		return nil
	}
	if kind := kindFor(res.Code); kind != nil {
		return &codeError{msg: res.Error, kind: kind}
	}
	return errors.New(res.Error)
}

// Dir returns the workbench directory inside a Lana home.
func Dir(home string) string { return filepath.Join(home, "workbench") }

// sockPath returns the core's socket path.
func sockPath(home string) string { return filepath.Join(Dir(home), "core.sock") }

// SocketPath returns the core's socket path inside a Lana home, so a caller
// can tell whether a core is running without starting one.
func SocketPath(home string) string { return sockPath(home) }

// newID returns a 12 hex character session id.
func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
