package core

import "context"

// SessionSearcher is an optional SessionStore extension that searches the
// conversation text index (REQ-SSR-011–015). The SQLite store implements it;
// the in-memory store does not. A tool sees it through ToolCtx.Sessions.
type SessionSearcher interface {
	SearchText(ctx context.Context, q TextQuery) ([]TextHit, error)
	// TextWindow resolves sessionID (full id or unique prefix) and returns the
	// session and up to radius conversation rows on each side of aroundSeq.
	TextWindow(ctx context.Context, sessionID string, aroundSeq int64, radius int) (SessionInfo, []TextRow, error)
}

// TextQuery is one discovery request.
type TextQuery struct {
	Terms         []string // already tokenized, lower case
	Agent         string   // "" = all agents
	CallerSession string   // REQ-SSR-015
	Limit         int      // sessions
	Context       int      // rows on each side of the first hit (REQ-SSR-012)
}

// TextRow is one indexed conversation row.
type TextRow struct {
	SessionID string
	Seq       int64
	Role      string // "user" | "assistant"
	CreatedAt int64
	Text      string
}

// TextHit is one session in the discovery result.
type TextHit struct {
	Session SessionInfo
	Row     TextRow
	Snippet string
	Around  []TextRow // only on the first hit; rows in seq order, the hit included
}

// SessionTextReader is an optional SessionStore extension that reads the
// conversation text index for knowledge upkeep (REQ-KNU-001, REQ-KNU-003). The
// SQLite store implements it; the in-memory store does not.
type SessionTextReader interface {
	// TextHeads lists searchable sessions (spec 29 definition) that have
	// conversation text, with their newest text seq.
	TextHeads(ctx context.Context) ([]TextHead, error)
	// TextRows returns rows of sessionID with seq > afterSeq in seq order,
	// stopping before the total text would pass maxRunes (at least one row).
	TextRows(ctx context.Context, sessionID string, afterSeq int64, maxRunes int) ([]TextRow, error)
}

// TextHead is one searchable session with conversation text, with MaxSeq the
// newest indexed text seq of the session.
type TextHead struct {
	Session SessionInfo
	MaxSeq  int64
}
