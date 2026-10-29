package tui

import (
	"context"

	"github.com/ridhomujizat/lana-harness/core"
)

// CompactionInfo summarizes the result of a compaction.
type CompactionInfo struct {
	TokensBefore int64
	TokensAfter  int64
}

// TurnResult is the outcome of one turn.
type TurnResult struct {
	Text         string
	StopReason   string
	InputTokens  int64
	OutputTokens int64
}

// Backend is the runtime port used by the TUI.
type Backend interface {
	CreateSession(ctx context.Context, agent string) (core.SessionInfo, error)
	LastSession(ctx context.Context) (core.SessionInfo, error)
	RunTurn(ctx context.Context, sessionID, prompt string) (TurnResult, error)
	Send(ctx context.Context, sessionID, text string) error
	Cancel(ctx context.Context, sessionID string) error
	SubscribeLive(ctx context.Context, sessionID string) (*core.Subscription[core.LiveFrame], error)
	Policy(ctx context.Context, sessionID string) (core.Policy, error)
	SetPolicy(ctx context.Context, sessionID string, policy core.Policy) error
	History(ctx context.Context, sessionID string) ([]core.SessionEvent, error)
	SessionInfo(ctx context.Context, sessionID string) (core.SessionInfo, error)
	ListSessions(ctx context.Context, limit int) ([]core.SessionInfo, error)
	ForkSession(ctx context.Context, sessionID string) (core.SessionInfo, error)
	SetAgent(ctx context.Context, sessionID, agent string) error
	SetModel(ctx context.Context, sessionID, model string) error
	Compact(ctx context.Context, sessionID string) (CompactionInfo, error)
	AgentNames() []string
	AgentModel(agent string) string
	AgentPolicy(agent string) string
}
