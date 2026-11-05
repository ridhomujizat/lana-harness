package calendar

// This file holds the outside dependencies of the Service (design section
// "Feature-local view"). The calendar never imports internal/workflow or the
// runtime: the runtime glue adapts them to these interfaces.

import (
	"context"
	"encoding/json"
	"time"
)

// WorkflowSource is the workflow side the calendar reads slots and runs from
// and starts runs through (REQ-CAL-004, REQ-CAL-006, REQ-CAL-008).
type WorkflowSource interface {
	// Slots returns the future fire times in [from, to); past slots are
	// omitted by the workflow side.
	Slots(ctx context.Context, from, to time.Time) ([]WorkflowSlot, error)
	// Runs returns the runs started in [from, to).
	Runs(ctx context.Context, from, to time.Time) ([]WorkflowRun, error)
	// StartRun starts one run with trigger "calendar" (API-CAL-018).
	StartRun(ctx context.Context, workflowID string, input json.RawMessage) (runID string, number int, err error)
	// Workflow returns a workflow's name and status ("active", "paused", …);
	// ok is false when it does not exist.
	Workflow(ctx context.Context, id string) (name string, status string, ok bool)
}

// Actions runs the agent and message actions of items (REQ-CAL-006).
type Actions interface {
	// AgentPrompt runs one turn of agent in its room and returns the session id.
	AgentPrompt(ctx context.Context, agent, prompt, title string) (sessionID string, err error)
	// SendMessage sends text to the owner; target is "telegram" or "web".
	SendMessage(ctx context.Context, target, text string) error
	// AgentExists reports whether an agent with that name exists.
	AgentExists(name string) bool
}

// Notifier tells the owner about missed and failed firings and sends the
// daily agenda in the Telegram owner chat (REQ-CAL-007, REQ-CAL-011).
type Notifier interface {
	NotifyOwner(ctx context.Context, text string) error
}
