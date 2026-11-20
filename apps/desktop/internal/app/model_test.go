package app

import (
	"math"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// fixedNow is the clock tests use.
func fixedNow() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) }

func closeTo(t *testing.T, what string, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range got {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

// TestDebounceSchedulesOneWrite is the 300 ms wait of REQ-DW-023.
func TestDebounceSchedulesOneWrite(t *testing.T) {
	var d debounce
	start := fixedNow()
	d.touch(start)
	if d.ready(start) {
		t.Fatal("a change is due the moment it happens")
	}
	if got := d.remaining(start.Add(100 * time.Millisecond)); got != 200*time.Millisecond {
		t.Fatalf("remaining after 100 ms = %v, want 200ms", got)
	}
	if d.ready(start.Add(layoutDebounce - time.Millisecond)) {
		t.Fatal("a change is due before the debounce is up")
	}
	if !d.ready(start.Add(layoutDebounce)) {
		t.Fatal("a change is not due when the debounce is up")
	}
	// A later change moves the due on, so a burst is one write.
	d.touch(start.Add(200 * time.Millisecond))
	if d.ready(start.Add(layoutDebounce)) {
		t.Fatal("the earlier due was not moved on by a later change")
	}
	d.consume()
	if d.ready(start.Add(time.Hour)) {
		t.Fatal("a written change is still due")
	}
}

// TestShiftShareKeepsAFloor is the divider arithmetic of REQ-DW-032.
func TestShiftShareKeepsAFloor(t *testing.T) {
	closeTo(t, "one step", shiftShare([]float64{0.5, 0.5}, 0, 0.2), []float64{0.7, 0.3})
	// Neither share may drop below 0.1.
	closeTo(t, "over the right edge", shiftShare([]float64{0.5, 0.5}, 0, 0.9), []float64{0.9, 0.1})
	closeTo(t, "over the left edge", shiftShare([]float64{0.5, 0.5}, 0, -0.9), []float64{0.1, 0.9})
	// Three children: the divider moves only its two neighbours.
	closeTo(t, "middle divider", shiftShare([]float64{0.3, 0.4, 0.3}, 1, 0.1), []float64{0.3, 0.5, 0.2})
	// A divider that is not there changes nothing.
	closeTo(t, "out of range", shiftShare([]float64{0.5, 0.5}, 4, 0.2), []float64{0.5, 0.5})
	// The slice given is left as it was.
	in := []float64{0.5, 0.5}
	_ = shiftShare(in, 0, 0.2)
	closeTo(t, "input untouched", in, []float64{0.5, 0.5})
}

// TestApplyEventSessions is the session half of the stream reducer.
func TestApplyEventSessions(t *testing.T) {
	a := New(Options{Now: fixedNow})

	created := lana.Session{ID: "s1", Program: "claude", NeedsYou: true}
	a.applyEvent(lana.Event{Type: lana.EventSessionCreated, Session: &created})
	if got := a.sessions["s1"]; got.Program != "claude" {
		t.Fatalf("session after created = %+v", got)
	}
	if a.badge != 1 {
		t.Fatalf("badge = %d, want 1", a.badge)
	}

	updated := created
	updated.NeedsYou = false
	updated.State = "exited"
	a.applyEvent(lana.Event{Type: lana.EventSessionUpdated, Session: &updated})
	if got := a.sessions["s1"]; got.State != "exited" {
		t.Fatalf("session after updated = %+v", got)
	}

	// A tab holds the session; removing it drops the pane and the session.
	l := layout.AddSpace(a.layout, "one", "/tmp/one")
	space := l.Spaces[0]
	l = layout.AddTab(l, space.ID, "s1", "")
	a.layout = l
	a.applyEvent(lana.Event{Type: lana.EventSessionRemoved, Session: &updated})
	if _, ok := a.sessions["s1"]; ok {
		t.Fatal("the removed session is still here")
	}
	if got := len(a.layout.Spaces[0].Tabs); got != 0 {
		t.Fatalf("tabs after removal = %d, want 0", got)
	}
}

// TestApplyEventLayoutChangedMerges is REQ-DW-023: another client's layout
// replaces the structure, and this window keeps its focus.
func TestApplyEventLayoutChangedMerges(t *testing.T) {
	a := New(Options{Now: fixedNow})

	base := layout.Empty()
	base = layout.AddSpace(base, "one", "/tmp/one")
	one := base.Spaces[0].ID
	base = layout.AddSpace(base, "two", "/tmp/two")
	two := base.Spaces[1].ID
	base = layout.AddTab(base, one, "s1", "")
	tabID := base.Spaces[0].Tabs[0].ID
	base = layout.SplitPane(base, one, tabID, "s1", "s2", "row")

	// This window looks at space two, with s2 focused in space one.
	local := base
	local.ActiveSpace = two
	local = layout.FocusPane(local, one, tabID, "s2")
	a.layout = local
	a.sessions = map[string]lana.Session{"s1": {}, "s2": {}, "s3": {}}

	// Another client added a pane to the tab and looks at space one.
	remote := layout.SplitPane(base, one, tabID, "s2", "s3", "col")
	remote.ActiveSpace = one
	raw, err := layout.Marshal(remote)
	if err != nil {
		t.Fatal(err)
	}

	a.applyEvent(lana.Event{Type: lana.EventLayoutChanged, Layout: raw})

	if len(a.layout.Spaces) != 2 {
		t.Fatalf("spaces = %d, want 2", len(a.layout.Spaces))
	}
	if a.layout.ActiveSpace != two {
		t.Fatalf("activeSpace = %q, want %q", a.layout.ActiveSpace, two)
	}
	tab := a.layout.Spaces[0].Tabs[0]
	if sessions := layout.TabSessions(tab); len(sessions) != 3 {
		t.Fatalf("the remote tab has %v, want three panes", sessions)
	}
	if tab.Focus != "s2" {
		t.Fatalf("focus = %q, want s2", tab.Focus)
	}
}
