package core

import (
	"context"
	"encoding/json"
)

// Risk adalah kelas risiko tool.
type Risk string

const (
	RiskRead    Risk = "read"
	RiskWrite   Risk = "write"
	RiskExec    Risk = "exec"
	RiskNetwork Risk = "network"
)

// ToolSpec adalah deskripsi tool untuk model.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolCtx adalah konteks satu pemanggilan tool.
type ToolCtx struct {
	SessionID string
	TurnID    string
	CallID    string
	AgentName string
	Workspace string
}

// ToolStatus adalah status hasil tool.
type ToolStatus string

const (
	ToolStatusSuccess  ToolStatus = "success"
	ToolStatusError    ToolStatus = "error"
	ToolStatusRejected ToolStatus = "rejected"
)

// ToolOutput adalah hasil tool.
type ToolOutput struct {
	Status ToolStatus
	Output string
}

// ToolCall adalah satu pemanggilan tool.
type ToolCall struct {
	SessionID string
	TurnID    string
	CallID    string
	Name      string
	Input     json.RawMessage
	Risk      Risk
}

// Tool adalah tool yang bisa dipanggil model.
type Tool interface {
	Spec() ToolSpec
	Risk(input json.RawMessage) Risk
	Call(ctx context.Context, tc ToolCtx, input json.RawMessage) (ToolOutput, error)
}

// ToolRegistry menyimpan tool berdasarkan nama.
type ToolRegistry interface {
	Register(t Tool) error
	Get(name string) (Tool, bool)
	Specs(names []string) ([]ToolSpec, error)
}
