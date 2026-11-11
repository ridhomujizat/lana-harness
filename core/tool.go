package core

import (
	"context"
	"encoding/json"
	"time"
)

// Risk is the risk class of a tool.
type Risk string

const (
	RiskRead    Risk = "read"
	RiskWrite   Risk = "write"
	RiskExec    Risk = "exec"
	RiskNetwork Risk = "network"
	// RiskMemory writes the calling agent's memory files.
	RiskMemory Risk = "memory"
	// RiskAdmin changes lana config or processes. It always needs a fresh owner approval.
	RiskAdmin Risk = "admin"
)

// ToolSpec is the tool description for the model.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolCtx is the context of one tool call.
type ToolCtx struct {
	SessionID string
	TurnID    string
	CallID    string
	AgentName string
	Workspace string
	// WorkspaceOnly keeps the file tools inside the workspace. False (the
	// default) lets them reach any path.
	WorkspaceOnly bool
	// Home is $LANA_HOME. Empty when the runtime has no home folder.
	Home string
	// Skills are the global skills listed in the agent's config.
	Skills []string
	// SkillWrite is the calling agent's skill_write config: "approve" (the
	// default) stages a skill proposal for the owner's review, "direct"
	// writes the skill at once (spec 46 REQ-DKN-050).
	SkillWrite string
	// OwnerTurn is true when the turn is a root turn started by an owner
	// message (spec 46 REQ-DKN-021, REQ-DKN-032); child and non-owner turns
	// are false.
	OwnerTurn bool
	// Questions asks the owner through the session's channel.
	Questions QuestionService
	// Files records and delivers the files the send_file tool sends
	// (REQ-AGF-003, REQ-AGF-004). Nil when the runtime has no file service;
	// the tool then reports that sending is not available.
	Files FileSender
	// Secrets opens the secure secret input card of the secret_request tool
	// (spec 53). Nil when the runtime has no secret request service; the tool
	// then reports that it is not available.
	Secrets SecretRequester
	// Subagents runs child sessions for the task tools.
	Subagents SubagentRunner
	// Discussions runs the room discussion of a team lead's root turn for the
	// discuss_* tools (spec 47). Nil elsewhere.
	Discussions DiscussionService
	// MCPAdmin changes MCP servers for the mcp_manage tool. Nil when the
	// runtime has no MCP registry.
	MCPAdmin MCPAdmin
	// MCPStatus is the read-only status source for the mcp_manage list
	// action. It is set for every turn, including child turns without
	// MCPAdmin. Nil when the runtime has no MCP registry.
	MCPStatus func(name string) (state string, tools int, reason string, known bool)
	// AllowedAgents is the delegation list of the calling agent.
	AllowedAgents []string
	// Depth is the session depth. A root session has depth 0.
	Depth int
	// Jobs runs and tracks the session's background jobs. Nil when the runtime
	// has no job registry (REQ-JOB-010).
	Jobs JobService
	// Sessions searches past conversations for the session_search tool
	// (REQ-SSR-010). Nil when the store has no text index.
	Sessions SessionSearcher
	// Knowledge reads and changes the knowledge vault for the knowledge tools
	// (REQ-KNW-040..043). Nil when the runtime has no knowledge service.
	Knowledge Knowledge
}

// Background-job status values (REQ-JOB-010).
const (
	JobStatusRunning = "running"
	JobStatusExited  = "exited"
	JobStatusKilled  = "killed"
)

// JobView is a snapshot of one background job. Status is "running", "exited",
// or "killed" (REQ-JOB-010).
type JobView struct {
	ID        string
	Command   string
	LogPath   string
	PID       int
	StartedAt time.Time
	EndedAt   time.Time
	Status    string
	ExitCode  int
}

// JobService is the background-job port the bash and job tools read through
// ToolCtx.Jobs (REQ-JOB-010..014). It is implemented by an adapter over the job
// registry, so core never imports the registry.
type JobService interface {
	// Start runs command in workspace and returns the new job.
	Start(sessionID, workspace, command string) (JobView, error)
	// List returns the session's jobs, oldest first.
	List(sessionID string) []JobView
	// ReadNew returns the output since the previous ReadNew of the job, at
	// most max bytes. dropped is the number of skipped bytes.
	ReadNew(sessionID, id string, max int) (text string, dropped int64, job JobView, err error)
	// Tail returns the last max bytes of the log without moving the cursor.
	Tail(sessionID, id string, max int) (string, error)
	// Wait waits until the job ends, d elapses, or ctx is done.
	Wait(ctx context.Context, sessionID, id string, d time.Duration) JobView
	// Kill stops the job and returns its final state.
	Kill(sessionID, id string) (JobView, error)
}

// ToolStatus is the status of a tool result.
type ToolStatus string

const (
	ToolStatusSuccess  ToolStatus = "success"
	ToolStatusError    ToolStatus = "error"
	ToolStatusRejected ToolStatus = "rejected"
)

// ToolOutput is the result of a tool.
type ToolOutput struct {
	Status ToolStatus
	Output string
	// Images are the image blocks of the result. Unlike Usage they are
	// persisted: the turn copies them into the tool/result event.
	Images []ContentBlock
	// Digest is the hex SHA-256 of the status and the output text, taken
	// before the output is spilled. The loop guard compares it to spot an
	// unchanged result (REQ-LPG-003).
	Digest string `json:"-"`
	// UnknownName is true when the call named a tool that was not offered
	// (REQ-LPG-021), even after name repair. The turn counts these calls for
	// the invalid-name strikes.
	UnknownName bool `json:"-"`
	// Usage is the token usage of child turns. It is added to the parent step
	// usage and never serialized into the tool result.
	Usage *Usage `json:"-"`
}

// ToolCall is one tool call.
type ToolCall struct {
	SessionID string
	TurnID    string
	CallID    string
	Name      string
	Input     json.RawMessage
	Risk      Risk
}

// Tool is a tool the model can call.
type Tool interface {
	Spec() ToolSpec
	Risk(input json.RawMessage) Risk
	Call(ctx context.Context, tc ToolCtx, input json.RawMessage) (ToolOutput, error)
}

// ParallelTool is an optional Tool extension. A tool that reports true may run
// in a batch with other ready calls of the same step.
type ParallelTool interface {
	ParallelSafe() bool
}

// ToolGuidance is implemented by a tool that adds a short usage note to the
// head prompt when it is offered (REQ-ECX-010).
type ToolGuidance interface{ PromptGuidance() string }

// SpawnRequest is one task delegation to a child agent.
type SpawnRequest struct {
	// ParentSessionID is the calling session.
	ParentSessionID string
	// ParentCallID is the tool call that delegates the task.
	ParentCallID string
	// Depth is the calling session's depth. The child runs at Depth+1.
	Depth int
	// Agent is the child's agent name. It must be in the caller's delegation
	// list.
	Agent string
	// Prompt is the child's only user message.
	Prompt string
	// Title is the child session title. Empty means the agent name.
	Title string
	// Context lists earlier child sessions of the same parent whose last
	// answers the child receives with the prompt (spec 47 team work).
	Context []string
	// WithNotes adds the agent's knowledge pages that match the prompt.
	WithNotes bool
}

// ContinueRequest is one follow-up to an existing child session.
type ContinueRequest struct {
	// ParentSessionID is the calling session.
	ParentSessionID string
	// SessionID is the child session to continue.
	SessionID string
	// Prompt is the follow-up user message.
	Prompt string
}

// SubagentRunner runs child sessions for the task tools.
type SubagentRunner interface {
	Spawn(ctx context.Context, req SpawnRequest) (ToolOutput, error)
	Continue(ctx context.Context, req ContinueRequest) (ToolOutput, error)
}

// ToolRegistry stores tools by name.
type ToolRegistry interface {
	Register(t Tool) error
	Get(name string) (Tool, bool)
	Specs(names []string) ([]ToolSpec, error)
}
