package workflow

import (
	"context"
	"encoding/json"
	"time"
)

// Deps is what the engine needs from the runtime. lana.Runtime implements it.
type Deps interface {
	// RunAgent runs one prompt in a fresh session on channel "workflow",
	// address = WorkflowID, and blocks until the turn ends.
	RunAgent(ctx context.Context, req AgentRequest) (AgentResult, error)
	// CallTool runs one tool; the result is the tool's text output.
	CallTool(ctx context.Context, req ToolRequest) (string, error)
	// Bash runs a shell command in the workflow workspace.
	Bash(ctx context.Context, req BashRequest) (BashResult, error)
	// WebFetch fetches a URL and returns its title and markdown.
	WebFetch(ctx context.Context, url string) (title, markdown string, err error)
	// Browse renders a page with the browser engine and returns its output.
	Browse(ctx context.Context, req BrowseRequest) (string, error)
	// SaveKnowledge stores a knowledge document for the workflow.
	SaveKnowledge(ctx context.Context, req KnowledgeRequest) error
	// AgentExists reports whether an agent is registered under name.
	AgentExists(name string) bool
	// ToolRisk returns the risk of a tool (read|write|admin), or "" when unknown.
	ToolRisk(name string) string
}

// AgentRequest is one RunAgent call on the workflow channel.
type AgentRequest struct {
	WorkflowID, RunID, NodeID string
	Agent                     string
	Prompt                    string
	Model                     string   // "" keeps the agent model
	Tools                     []string // nil = agent defaults; non-nil = only these tools may run
	Lock                      []string // tools that run without approval
	MaxSteps                  int      // 0 = agent default; counts tool calls
	// Background marks a guardian's structured call (assistant graph edits,
	// failure decisions): lana background work that runs on the agent's
	// background chain, never in Claude Code (spec 39 revision 5). A
	// guardian with no model outside Claude Code fails with a
	// *GuardianBackgroundError.
	Background bool
}

// AgentResult is the outcome of one RunAgent call.
type AgentResult struct {
	Text       string
	SessionID  string
	Tokens     int64
	StopReason string
}

// ToolRequest is one CallTool call.
type ToolRequest struct {
	WorkflowID, RunID, NodeID string
	Name                      string
	Input                     json.RawMessage
}

// BashRequest is one Bash call.
type BashRequest struct {
	WorkflowID string
	Command    string
	Timeout    time.Duration
}

// BashResult is the outcome of one Bash call.
type BashResult struct {
	ExitCode int
	Output   string // combined, last 64 KiB
}

// BrowseRequest is one Browse call.
type BrowseRequest struct {
	WorkflowID string
	URL        string
	Format     string // text|markdown|html
	Stealth    bool
	Selector   string
	Timeout    time.Duration
}

// KnowledgeRequest is one SaveKnowledge call.
type KnowledgeRequest struct {
	WorkflowID, Agent, Title, Folder, Text string
}

// Notifier tells the owner. cmd/lana sets a Telegram notifier; nil = no-op.
type Notifier interface {
	// Notify sends one notice to the owner.
	Notify(ctx context.Context, n Notice)
	// SendOwner sends a plain text message; used by the message node.
	SendOwner(ctx context.Context, text string) error
}

// Notice is one owner notification, optionally with decision buttons.
type Notice struct {
	WorkflowID   string
	WorkflowName string
	RunNumber    int
	Title        string // e.g. "Riset harian · Run #8 is paused"
	Text         string
	DecisionID   string   // "" = no buttons
	Choices      []string // apply, run_only, skip, stop, approve, deny
}

// AssistEnv is what a workflow can use, told to the guardian assistant so it
// builds with what exists instead of asking the owner (REQ-WF-010 revision):
// the agents, the MCP tools a tool node can call, the names (never values)
// of the owner's secrets, and whether bash nodes see those secrets.
type AssistEnv struct {
	Agents []EnvAgent
	// MCPTools are the tools of the configured MCP servers, named
	// mcp__<server>__<tool>; MCPUnavailable names servers that did not start.
	MCPTools       []EnvTool
	MCPUnavailable map[string]string
	// Secrets are the names in secrets.env and the provider keys lana knows.
	Secrets []string
	// BashSeesSecrets reports whether bash nodes get the secrets as env vars.
	BashSeesSecrets bool
}

// EnvAgent is one agent a bot or worker node can run.
type EnvAgent struct {
	Name        string
	Description string
	Model       string
	MCP         []string
}

// EnvTool is one tool a tool node can call: its name, description and input
// schema (compact JSON).
type EnvTool struct {
	Name        string
	Description string
	Input       string
}

// AssistEnvProvider is an optional Deps extension that describes the
// environment for the assistant prompt. Deps without it give an empty one.
type AssistEnvProvider interface {
	AssistEnv(ctx context.Context, wf Workflow) AssistEnv
}

// AssistSecretsProvider is an optional Deps extension that lists the owner's
// secret names cheaply, so a later guardian note names a secret added since
// the first note (spec 53). Names only, never values.
type AssistSecretsProvider interface {
	AssistSecrets(ctx context.Context) []string
}

// AssistantRuntime is the optional Deps extension behind the guardian chat
// session of spec 51 (REQ-WFC-001, REQ-WFC-009). Deps without it make the
// guardian chat unavailable.
type AssistantRuntime interface {
	// CreateAssistantSession creates the guardian's chat session for wf on
	// channel "workflow", address = the workflow id.
	CreateAssistantSession(ctx context.Context, wf Workflow) (string, error)
	// AssistantSessionExists reports whether the session still exists and
	// belongs to agent (a new guardian gets a new session).
	AssistantSessionExists(ctx context.Context, sessionID, agent string) bool
	// RunAssistantTurn appends note as a hidden runtime note (when not
	// empty), runs text with attachments as an owner turn on the session and
	// blocks until the turn ends. It returns the final assistant text.
	RunAssistantTurn(ctx context.Context, sessionID, note, text string, attachments []string) (AgentResult, error)
}
