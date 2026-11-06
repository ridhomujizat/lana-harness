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
