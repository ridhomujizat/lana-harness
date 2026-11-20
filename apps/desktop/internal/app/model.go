package app

import (
	"time"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// layoutDebounce is how long the app waits after a local layout change
// before it writes it, so a burst of changes is one write (REQ-DW-023).
const layoutDebounce = 300 * time.Millisecond

// debounce waits out a quiet period before a write (REQ-DW-023).
type debounce struct {
	dirty bool
	due   time.Time
}

// touch marks the layout changed at now and moves the write due.
func (d *debounce) touch(now time.Time) {
	d.dirty = true
	d.due = now.Add(layoutDebounce)
}

// remaining is how long is left before the write is due, 0 when nothing
// is waiting.
func (d *debounce) remaining(now time.Time) time.Duration {
	if !d.dirty || !now.Before(d.due) {
		return 0
	}
	return d.due.Sub(now)
}

// ready reports whether a pending change is due to be written.
func (d *debounce) ready(now time.Time) bool {
	return d.dirty && !now.Before(d.due)
}

// consume takes the pending change, if any.
func (d *debounce) consume() { d.dirty = false }

// reset forgets a pending change, as after the layout was reloaded.
func (d *debounce) reset() { d.dirty, d.due = false, time.Time{} }

// applyEvent folds one event of the stream into the app's state
// (REQ-DW-023). It runs on the UI thread.
func (a *App) applyEvent(ev lana.Event) {
	switch ev.Type {
	case lana.EventSessionCreated, lana.EventSessionUpdated:
		if ev.Session != nil {
			was, had := a.sessions[ev.Session.ID]
			a.sessions[ev.Session.ID] = *ev.Session
			// An agent that finished unseen is marked done until its pane
			// gets the focus (spec 58, done status).
			a.markDone(was, *ev.Session)
			// A pane that shows the session follows its size, and takes its
			// terminal again when a session that had exited runs
			// (REQ-DW-040).
			if pt := a.terms[ev.Session.ID]; pt != nil && had {
				pt.sessionUpdated(was, *ev.Session)
			}
		}
	case lana.EventSessionRemoved:
		if ev.Session != nil {
			a.dropTerm(ev.Session.ID)
			delete(a.sessions, ev.Session.ID)
			delete(a.done, ev.Session.ID)
			delete(a.entering, ev.Session.ID)
			delete(a.closing, ev.Session.ID)
			// A tab the removal left focuses the pane the user used last, not
			// its first one (spec 58, window design).
			a.layout = a.dropSession(ev.Session.ID)
			a.noteFocus()
		}
	case lana.EventLayoutChanged:
		if len(ev.Layout) > 0 {
			// Another client saved the layout: take its structure, but
			// keep this window's own focus where the pane still exists
			// (REQ-DW-023).
			a.layout = layout.MergeRemote(a.layout, layout.Parse(ev.Layout))
			a.noteFocus()
		}
	}
	a.updateBadge()
	a.invalidate()
}

// shiftShare moves the divider at j of a split by delta, keeping every
// share at least 0.1 (spec 58, dividers). It returns a new slice and
// leaves the one it was given as it was.
func shiftShare(sizes []float64, j int, delta float64) []float64 {
	out := append([]float64{}, sizes...)
	if j < 0 || j+1 >= len(out) {
		return out
	}
	a, b := out[j], out[j+1]
	switch {
	case delta > 0:
		if room := b - 0.1; delta > room {
			delta = room
		}
	case delta < 0:
		if room := a - 0.1; -delta > room {
			delta = -room
		}
	}
	if delta == 0 {
		return out
	}
	out[j], out[j+1] = a+delta, b-delta
	return out
}
