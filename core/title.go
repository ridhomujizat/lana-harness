package core

import "context"

// Session title sources (spec 40, REQ-TTL-005).
const (
	// TitleSourceAuto is a title written by the background title pass.
	TitleSourceAuto = "auto"
	// TitleSourceOwner is a title the owner gave. The title pass never
	// overwrites it.
	TitleSourceOwner = "owner"
	// TitleSourceSystem is a fixed title given when the session was created,
	// such as "Workflow · <name>".
	TitleSourceSystem = "system"
)

// SessionTitleStore is an optional SessionStore extension that writes the
// session title projection (spec 40). The SQLite and in-memory stores
// implement it.
type SessionTitleStore interface {
	// SetSessionTitle stores title and source. When onlyIfUntitled is true
	// the write applies only while the session has no title source yet, so a
	// background pass never overwrites a title set meanwhile. It reports
	// whether the row changed and returns a not-found error for an unknown
	// session. updated_at is not changed.
	SetSessionTitle(ctx context.Context, id, title, source string, onlyIfUntitled bool) (bool, error)
}

// SessionTitler names a session in the background after a turn (spec 40,
// REQ-TTL-001). TurnEnded returns at once and never delays the turn.
type SessionTitler interface {
	TurnEnded(TitleRequest)
}

// TitleRequest is one title pass request from the end of a root turn that
// stored an owner message.
type TitleRequest struct {
	SessionID string
	AgentName string
	// Targets is the turn's chain (primary first); the pass uses its
	// background chain. Empty means the agent's configured chain.
	Targets []LLMTarget
	// Requested is the combo or address the agent asked for.
	Requested string
}
