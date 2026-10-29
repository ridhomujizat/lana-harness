package core

import "context"

// Policy adalah mode approval session.
type Policy string

const (
	PolicyTrust Policy = "trust"
	PolicyAsk   Policy = "ask"
	PolicyNever Policy = "never"
)

// ApprovalOutcome adalah jawaban approval.
type ApprovalOutcome string

const (
	OutcomeAllowedOnce    ApprovalOutcome = "allowed_once"
	OutcomeAllowedSession ApprovalOutcome = "allowed_session"
	OutcomeRejected       ApprovalOutcome = "rejected"
	OutcomeCancelled      ApprovalOutcome = "cancelled"
	OutcomeUnavailable    ApprovalOutcome = "unavailable"
)

// ApprovalRequest adalah permintaan approval untuk satu pemanggilan tool.
type ApprovalRequest struct {
	SessionID string
	TurnID    string
	CallID    string
	AgentName string
	ToolName  string
	Risk      Risk
	Label     string
}

// ApprovalService menjalankan approval lewat hook ApprovalRequest.
type ApprovalService interface {
	Request(ctx context.Context, req ApprovalRequest) (ApprovalOutcome, error)
	SetPolicy(ctx context.Context, sessionID string, p Policy) error
	EffectivePolicy(ctx context.Context, sessionID string) (Policy, error)
}
