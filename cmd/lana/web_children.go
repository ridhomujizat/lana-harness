package main

import (
	"context"

	"github.com/ridhomujizat/lana-harness/core"
)

// ChildSessions lists the direct children of a session. It is the optional
// web.Backend extension used by the SSE and approval fan-in (API-TEAM-011).
func (b *webBackend) ChildSessions(ctx context.Context, parentID string) ([]core.SessionInfo, error) {
	return b.rt.ListAllSessions(ctx, core.SessionFilter{ParentSessionID: parentID})
}

// SessionInfo reports one session without the full detail read. It is the
// optional web.Backend extension used to walk ParentSessionID to the root for
// the approval fan-in (API-TEAM-011).
func (b *webBackend) SessionInfo(ctx context.Context, sessionID string) (core.SessionInfo, error) {
	return b.rt.SessionInfo(ctx, sessionID)
}
