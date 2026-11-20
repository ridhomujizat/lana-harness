package app

import (
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// statusDone is a client-only status: an agent that finished its work while
// the user was not looking at its pane (spec 58, done status). It lives in
// the window, not in the shared layout document.
const statusDone layout.Status = "done"

// displayStatus folds a session's base status and its done mark into what
// the window shows. The order of precedence is needs you, working, done,
// idle (spec 58, done status): a session that is not idle never shows as
// done.
func displayStatus(st layout.Status, done bool) layout.Status {
	if done && st == layout.StatusIdle {
		return statusDone
	}
	return st
}

// becomeDone reports whether an update moves a session from working to idle
// while the user is not looking at it, so it shows as done until its pane
// gets the focus (spec 58, done status). Only agents are done, and a
// session that needs the user or has exited is not.
func becomeDone(prev, next lana.Session, looking bool) bool {
	if looking || !isAgent(next) {
		return false
	}
	return statusOf(prev) == layout.StatusWorking && statusOf(next) == layout.StatusIdle
}

// lookingAt reports whether the user is looking at a session's pane right
// now: the window has the focus, that pane is the focused pane of the
// active tab of the active space, and it is visible (not hidden behind
// another zoomed pane) (spec 58, done status).
func (a *App) lookingAt(sessionID string) bool {
	if !a.focusedWin {
		return false
	}
	space := layout.ActiveSpace(a.layout)
	tab := layout.ActiveTab(space)
	if tab == nil {
		return false
	}
	if tab.Zoom != "" && tab.Zoom != sessionID {
		return false
	}
	return a.focusedSession() == sessionID
}

// markDone folds one session update into the done set (spec 58, done
// status): an agent that finished unseen becomes done, and one that starts
// working, needs the user or exits stops being done.
func (a *App) markDone(prev, next lana.Session) {
	id := next.ID
	if id == "" {
		return
	}
	if becomeDone(prev, next, a.lookingAt(id)) {
		a.done[id] = true
		return
	}
	switch statusOf(next) {
	case layout.StatusWorking, layout.StatusBlocked, layout.StatusExited:
		delete(a.done, id)
	}
}

// clearFocusedDone forgets the done mark of the pane the user is looking at,
// now that it has the focus (spec 58, done status).
func (a *App) clearFocusedDone() {
	id := a.focusedSession()
	if id != "" && a.lookingAt(id) {
		delete(a.done, id)
	}
}

// isDone reports whether a session shows as done.
func (a *App) isDone(sessionID string) bool { return a.done[sessionID] }
