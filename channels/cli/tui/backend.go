package tui

import (
	"context"

	"github.com/ridhomujizat/lana-harness/core"
)

// TurnResult adalah hasil satu turn.
type TurnResult struct {
	Text         string
	StopReason   string
	InputTokens  int64
	OutputTokens int64
}

// Backend adalah port runtime yang dipakai TUI.
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
	AgentNames() []string
	AgentModel(agent string) string
	AgentPolicy(agent string) string
}
