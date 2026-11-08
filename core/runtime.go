package core

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// AgentRuntime runs whole turns in an external agent, such as the user's
// installed Claude Code (ADR 0018). lana's own loop, tools, and compaction do
// not run for these turns: the runtime owns its context and reports what
// happens through RuntimeTurn.Emit.
type AgentRuntime interface {
	// RunTurn runs one turn. It emits exactly one RuntimeEnd event as its last
	// event, whatever the outcome, and returns a non-nil error exactly when that
	// end event has IsError set (with the same text). A cancelled turn ends with
	// StopReason RuntimeStopCancelled and returns nil.
	RunTurn(ctx context.Context, turn RuntimeTurn) error
	// Interrupt asks the running turn of a session to stop. It returns nil when
	// the session has no running turn. Cancelling the RunTurn context has the
	// same effect.
	Interrupt(sessionID string) error
	// Close stops the session's process, if any, and forgets the session.
	Close(sessionID string)
	// Shutdown stops every process. It returns when they have exited or ctx ends.
	Shutdown(ctx context.Context) error
	// Status reports the runtime's health for the dashboard (API-CCR-003).
	Status(ctx context.Context) RuntimeStatus
}

// RuntimeDeps are the services a runtime factory receives.
type RuntimeDeps struct {
	Secrets Secrets
	Logger  *slog.Logger
	// OnModels, when set, receives the model list the runtime learned from
	// its binary (for Claude Code, the initialize response). It may be nil.
	OnModels func([]RuntimeModel)
}

// ClaudeCodeProvider is the built-in provider name of Claude Code router
// targets: "claude-code/<model>" runs the turn in the Claude Code runtime
// (ADR 0020, API-CCT-001). It is also the runtime name.
const ClaudeCodeProvider = "claude-code"

// RuntimeModel is one model an external runtime offers, as its binary
// reports it (for Claude Code, an entry of the initialize response's models).
type RuntimeModel struct {
	// Value is the model value passed to --model, for example
	// "claude-opus-5-5" or "opus".
	Value string `json:"value"`
	// DisplayName is the runtime's label, for example "Opus 5.5".
	DisplayName string `json:"display_name"`
	// Description is the runtime's one-line description, when given.
	Description string `json:"description,omitempty"`
}

// RuntimeModelLister is implemented by a runtime that knows the models its
// binary offers. ok is false when no list was captured yet.
type RuntimeModelLister interface {
	Models() (models []RuntimeModel, ok bool)
}

// RuntimeEarlyError is the error AgentRuntime.RunTurn returns when the turn
// failed before its first content event (text, thinking, tool call, or tool
// result): the binary is missing, too old, not logged in, the plan window
// rejected the request, or the process exited (REQ-CCT-004). Nothing was
// shown, so the caller may run the turn on another target.
type RuntimeEarlyError struct {
	// Class is the router outcome class of the failure, for example "auth",
	// "rate_limit", "not_found", "missing_secret", "client", or "runtime".
	Class   string
	Message string
}

func (e *RuntimeEarlyError) Error() string { return e.Message }

// RuntimeToolFunc runs one lana tool for an external runtime turn. callID is
// the runtime's tool call ID when it sent one, else a generated one.
type RuntimeToolFunc func(ctx context.Context, callID, name string, input json.RawMessage) ToolOutput

// RuntimeToolHost serves lana's tools to external runtime turns over a
// loopback MCP server (ADR 0020, REQ-CCT-006).
type RuntimeToolHost interface {
	// Bind sets the tools and the call handler of a session's running turn.
	// It starts the host on first use; an error means it cannot serve.
	Bind(sessionID string, tools []ToolSpec, call RuntimeToolFunc) error
	// Unbind clears the call handler of a session after its turn.
	Unbind(sessionID string)
	// Open issues a bearer token for a session and returns the runtime's MCP
	// config (for Claude Code, the --mcp-config JSON). release revokes the
	// token; the runtime calls it when its process exits.
	Open(sessionID string) (config string, release func(), err error)
	// Close stops the host.
	Close() error
}

// Auth modes of RuntimeConfig.Auth.
const (
	RuntimeAuthSubscription = "subscription"
	RuntimeAuthAPIKey       = "api_key"
)

// RuntimeConfig is the per-agent configuration of a runtime turn (API-CCR-001).
type RuntimeConfig struct {
	// Auth is RuntimeAuthSubscription (the default when empty) or
	// RuntimeAuthAPIKey.
	Auth string
	// APIKeyEnv names the secret that holds the API key when Auth is api_key.
	APIKeyEnv string
	// Model is passed to --model. Empty uses the runtime's default.
	Model string
	// Effort is passed to --effort. Empty uses the runtime's default.
	Effort string
	// Binary is the executable path. Empty looks the binary up on PATH.
	Binary string
	// Workspace is the working directory of the process. A leading ~ is
	// expanded. Empty inherits the daemon's working directory.
	Workspace string
	// AppendSystemPrompt is appended to the runtime's system prompt
	// (--append-system-prompt): lana's prompt head for the agent (REQ-CCT-005).
	// A change starts a new process that resumes the runtime session.
	AppendSystemPrompt string
	// MCPKey names the lana tool set served to the process (REQ-CCT-006). A
	// change starts a new process that resumes the runtime session.
	MCPKey string
	// Account is the Claude Code account of the turn (REQ-CCT-010): "" or
	// ClaudeCodeProvider for the built-in one. A process and a runtime
	// session belong to one account and are never reused for another.
	Account string
	// ConfigDir is set as CLAUDE_CONFIG_DIR of the process. Empty keeps the
	// daemon's environment as it is (the built-in account).
	ConfigDir string
}

// RuntimeAccount names one Claude Code account for a status probe
// (API-CCT-011).
type RuntimeAccount struct {
	// Name is the account (provider) name; ClaudeCodeProvider is built in.
	Name string
	// ConfigDir is the account's CLAUDE_CONFIG_DIR; "" keeps the daemon's.
	ConfigDir string
	// Binary is the executable; "" looks claude up on PATH.
	Binary    string
	Auth      string
	APIKeyEnv string
}

// RuntimeAccountStatus is the health of one Claude Code account
// (API-CCT-011). Email and Plan come from `claude auth status` and are never
// stored.
type RuntimeAccountStatus struct {
	Binary     string
	Version    string
	VersionOK  bool
	LoggedIn   bool
	AuthMethod string
	Email      string
	// Plan is subscriptionType, else planTier.
	Plan    string
	Error   string
	Running int
	Queued  int
}

// RuntimeAccountStatuser is implemented by a runtime that reports the status
// of one account (the Claude Code runtime).
type RuntimeAccountStatuser interface {
	AccountStatus(ctx context.Context, account RuntimeAccount) RuntimeAccountStatus
}

// RuntimeTurn is one turn handed to an AgentRuntime.
type RuntimeTurn struct {
	SessionID string
	TurnID    string
	AgentName string
	Config    RuntimeConfig
	// ResumeID is the runtime's own session ID (for Claude Code, the
	// claude_session_id of a RuntimeSessionMeta event) to resume when the
	// runtime has no live process for the session, for example after a daemon
	// restart. Empty starts a new runtime session.
	ResumeID string
	// Input is the user message: text and image blocks.
	Input []ContentBlock
	// Emit receives the turn's events in order. It is called from one
	// goroutine at a time and must not block for long.
	Emit func(RuntimeEvent)
	// Approve decides one tool permission request. It may be called
	// concurrently for parallel tool calls, and its ctx ends when the turn is
	// cancelled or ends. A nil Approve, a panic, or a cancelled ctx denies
	// (fail-closed). message is shown to the agent when the request is denied.
	Approve func(ctx context.Context, req RuntimeApproval) (allow bool, message string)
	// OpenMCP, when set, is called once per new runtime process for the MCP
	// config that serves lana's tools (REQ-CCT-006). release runs when the
	// process exits. An error starts the process without lana's tools.
	OpenMCP func() (config string, release func(), err error)
}

// RuntimeApproval is a permission request from an external runtime.
type RuntimeApproval struct {
	// CallID is the runtime's tool call ID (Claude Code tool_use_id).
	CallID string
	// ToolName is the runtime's tool name, for example "Bash".
	ToolName string
	// Input is the tool input as the runtime sent it.
	Input json.RawMessage
	// Risk is the lana risk class of the tool.
	Risk Risk
	// Rule is the rule-match string, for example "bash(ls -la)" or
	// "read(/etc/hosts)".
	Rule string
	// PermTool and PermInput are the equivalent lana tool name and input for
	// lana's permission engine, so rules such as bash(rm *) or
	// read_file(/etc/*) match a Claude tool call like a native one.
	PermTool  string
	PermInput json.RawMessage
	// Description and Reason are the runtime's own text for the request.
	Description string
	Reason      string
}

// RuntimeEventKind is the kind of a RuntimeEvent.
type RuntimeEventKind string

const (
	RuntimeTextDelta        RuntimeEventKind = "text_delta"
	RuntimeThinkingDelta    RuntimeEventKind = "thinking_delta"
	RuntimeAssistantMessage RuntimeEventKind = "assistant_message"
	RuntimeToolCall         RuntimeEventKind = "tool_call"
	RuntimeToolResult       RuntimeEventKind = "tool_result"
	RuntimeSessionMeta      RuntimeEventKind = "session_meta"
	RuntimeUsage            RuntimeEventKind = "usage"
	RuntimeLimits           RuntimeEventKind = "limits"
	RuntimeEnd              RuntimeEventKind = "end"
)

// Stop reasons of a RuntimeEnd event besides the runtime's own (for example
// "end_turn").
const (
	RuntimeStopEndTurn   = "end_turn"
	RuntimeStopCancelled = "cancelled"
	RuntimeStopError     = "error"
)

// RuntimeEvent is one event of a runtime turn. Which fields are set depends on
// Kind:
//
//   - text_delta, thinking_delta, assistant_message: Text.
//   - tool_call: ID, Name, Input, Risk. The runtime executes the tool itself.
//   - tool_result: ID, Output, IsError.
//   - session_meta: ClaudeSessionID, Model, Tools, Version. Sent once per
//     runtime process, on its first init.
//   - usage: Usage, CostUSD, Model, DurationMS. Sent once per turn, before end.
//   - limits: Windows.
//   - end: StopReason, IsError, ErrorText. Always the last event.
type RuntimeEvent struct {
	Kind RuntimeEventKind

	Text string

	ID     string
	Name   string
	Input  json.RawMessage
	Risk   Risk
	Output string

	ClaudeSessionID string
	Model           string
	Tools           []string
	Version         string

	Usage      Usage
	CostUSD    float64
	DurationMS int64

	Windows []RuntimeLimitWindow

	StopReason string
	IsError    bool
	ErrorText  string
}

// RuntimeLimitWindow is one plan usage window the runtime reports, for example
// Claude Code's five-hour or seven-day window.
type RuntimeLimitWindow struct {
	// Type is the window name as reported, for example "five_hour" or
	// "seven_day".
	Type string `json:"type"`
	// Status is the window status as reported, for example "allowed".
	Status string `json:"status"`
	// Utilization is the used fraction, 0..1 (0.42 = 42 %), when reported.
	Utilization *float64 `json:"utilization,omitempty"`
	// ResetsAt is when the window resets; zero when not reported.
	ResetsAt time.Time `json:"resets_at"`
}

// RuntimeStatus is the health of a runtime (API-CCR-003).
type RuntimeStatus struct {
	// ConfiguredAgents is filled by the caller; the runtime leaves it empty.
	ConfiguredAgents []string `json:"configured_agents"`
	Binary           string   `json:"binary"`
	Version          string   `json:"version"`
	VersionOK        bool     `json:"version_ok"`
	LoggedIn         bool     `json:"logged_in"`
	AuthMethod       string   `json:"auth_method"`
	Error            string   `json:"error"`
	Running          int      `json:"running"`
	Queued           int      `json:"queued"`
	MaxConcurrent    int      `json:"max_concurrent"`
}

// RuntimeFactory builds a runtime.
type RuntimeFactory func(RuntimeDeps) (AgentRuntime, error)

var (
	runtimeMu       sync.Mutex
	runtimeRegistry = map[string]RuntimeFactory{}
)

// RegisterRuntime registers a runtime factory under the name an agent's
// runtime key uses, for example "claude-code". A duplicate name panics.
func RegisterRuntime(name string, factory func(RuntimeDeps) (AgentRuntime, error)) {
	if name == "" || factory == nil {
		panic("core: RegisterRuntime requires a name and a factory")
	}
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if _, ok := runtimeRegistry[name]; ok {
		panic("core: runtime '" + name + "' is already registered")
	}
	runtimeRegistry[name] = factory
}

// LookupRuntime returns the factory registered under name.
func LookupRuntime(name string) (RuntimeFactory, bool) {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	f, ok := runtimeRegistry[name]
	return f, ok
}

// RuntimeNames returns the registered runtime names, sorted alphabetically.
func RuntimeNames() []string {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	names := make([]string, 0, len(runtimeRegistry))
	for name := range runtimeRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
