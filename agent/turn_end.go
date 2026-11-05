package agent

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// The turn/end event is the only write that moves a session's status off
// "running": the status is a projection of the event log. A turn/end that is
// lost to a busy store leaves the session looking busy forever, and every
// later turn/start is refused. writeTurnEnd therefore retries a busy failure.
var (
	// turnEndDelays are the waits before each in-line retry.
	turnEndDelays = []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
	// turnEndLateDelay and turnEndLateTries bound the background retries that
	// follow when the in-line retries are spent.
	turnEndLateDelay = 5 * time.Second
	turnEndLateTries = 24
	// turnEndWait performs one wait. It is a variable so tests skip the time.
	turnEndWait = time.Sleep
)

// writeTurnEnd appends the turn/end draft. A busy store error is retried in
// line with a short backoff. When those retries are spent the write continues
// in the background, so the session leaves "running" as soon as the store
// accepts a write again, without holding up the turn result. It returns the
// error of the last in-line attempt.
func writeTurnEnd(ctx context.Context, events *Events, draft core.EventDraft) error {
	_, err := events.Append(ctx, draft)
	for _, delay := range turnEndDelays {
		if !isStoreBusy(err) {
			return err
		}
		turnEndWait(delay)
		_, err = events.Append(ctx, draft)
	}
	if isStoreBusy(err) {
		go writeTurnEndLate(ctx, events, draft)
	}
	return err
}

// writeTurnEndLate keeps retrying a busy turn/end write with a longer delay.
// It stops at the first success or at an error that is not busy.
func writeTurnEndLate(ctx context.Context, events *Events, draft core.EventDraft) {
	for try := 1; try <= turnEndLateTries; try++ {
		turnEndWait(turnEndLateDelay)
		_, err := events.Append(ctx, draft)
		if err == nil {
			slog.Info("wrote turn/end after retries", "session_id", events.SessionID, "turn_id", draft.TurnID, "tries", try)
			return
		}
		if !isStoreBusy(err) {
			slog.Error("failed to write turn/end", "session_id", events.SessionID, "turn_id", draft.TurnID, "error", err)
			return
		}
	}
	slog.Error("gave up writing turn/end; the session stays running", "session_id", events.SessionID, "turn_id", draft.TurnID)
}

// isStoreBusy reports whether err is SQLite's SQLITE_BUSY or SQLITE_LOCKED,
// including extended codes such as SQLITE_BUSY_SNAPSHOT (517). The agent
// package sees the store only through core.SessionStore, so it matches the
// driver's message rather than its error type.
func isStoreBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked") || strings.Contains(msg, "SQLITE_BUSY")
}
