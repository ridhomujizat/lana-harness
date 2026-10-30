package core

import (
	"context"
	"encoding/json"
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
	// Home is $LANA_HOME. Empty when the runtime has no home folder.
	Home string
	// Skills are the global skills listed in the agent's config.
	Skills []string
	// Questions asks the owner through the session's channel.
	Questions QuestionService
	// Subagents runs child sessions for the task tools.
	Subagents SubagentRunner
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
