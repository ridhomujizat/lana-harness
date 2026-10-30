package core

import (
	"context"
	"encoding/json"
)

// Policy is the approval mode of a session.
type Policy string

const (
	PolicyTrust Policy = "trust"
	PolicyAsk   Policy = "ask"
	PolicyNever Policy = "never"
)

// ApprovalOutcome is the answer to an approval.
type ApprovalOutcome string

const (
	OutcomeAllowedOnce    ApprovalOutcome = "allowed_once"
	OutcomeAllowedSession ApprovalOutcome = "allowed_session"
	OutcomeRejected       ApprovalOutcome = "rejected"
	OutcomeCancelled      ApprovalOutcome = "cancelled"
	OutcomeUnavailable    ApprovalOutcome = "unavailable"
)

// ApprovalRequest is a request for approval of one tool call.
type ApprovalRequest struct {
	SessionID string
	TurnID    string
	CallID    string
	AgentName string
	ToolName  string
	Input     json.RawMessage
	Risk      Risk
	Label     string
}

// ApprovalService runs approvals through the ApprovalRequest hook.
type ApprovalService interface {
	Request(ctx context.Context, req ApprovalRequest) (ApprovalOutcome, error)
	SetPolicy(ctx context.Context, sessionID string, p Policy) error
	EffectivePolicy(ctx context.Context, sessionID string) (Policy, error)
}
