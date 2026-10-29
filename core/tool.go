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

// ToolRegistry stores tools by name.
type ToolRegistry interface {
	Register(t Tool) error
	Get(name string) (Tool, bool)
	Specs(names []string) ([]ToolSpec, error)
}
