package app

import (
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// focusApp is an app whose active tab holds three panes, a, b and c, in tree
// order, with the focus history a, then c, then b, as after the user worked in
// a, then c, then b: the focus ends on b (spec 58, window design).
func focusApp(t *testing.T) *App {
	t.Helper()
	a := testApp(t)
	l := layout.Empty()
	l = layout.AddSpace(l, "work", "/tmp/work")
	space := l.Spaces[0].ID
	l = layout.AddTab(l, space, "a", "")
	tabID := l.Spaces[0].Tabs[0].ID
	l = layout.SplitPane(l, space, tabID, "a", "b", "row")
	l = layout.SplitPane(l, space, tabID, "b", "c", "row")
	a.layout = l
	// The focus history is built by focusing panes through the layout, as the
	// view and the shortcuts do.
	a.FocusPane("a")
	a.FocusPane("c")
	a.FocusPane("b")
	a.focusReq = ""
	return a
}

// fadeKills wires the app so a test can fire the close fade by hand and knows
// when the kill round-trip, which posts from a goroutine, has finished.
func fadeKills(a *App) <-chan struct{} {
	posted := make(chan struct{}, 4)
	a.post = func(fn func()) { fn(); posted <- struct{}{} }
	return posted
}

// TestClosingFocusedPaneMovesFocusAtOnce is the pane half of the fix: closing
// the focused pane moves the focus before the fade even fires, to the pane the
// user used before it, and asks for its terminal's keyboard focus; the focus
// stays there once the session is killed (spec 58, window design; REQ-DW-044).
func TestClosingFocusedPaneMovesFocusAtOnce(t *testing.T) {
	a := focusApp(t)
	var fires []func()
	a.after = func(_ time.Duration, fn func()) paneTimer { fires = append(fires, fn); return noopTimer{} }
	posted := fadeKills(a)

	a.ClosePane("b")

	// Right after ClosePane returns, before the fade delay fires, the focus
	// has already moved: from b to c, the pane used just before it.
	if got := a.layout.Spaces[0].Tabs[0].Focus; got != "c" {
		t.Fatalf("focus right after closing = %q, want c", got)
	}
	if a.focusReq != "c" {
		t.Fatalf("focusReq right after closing = %q, want c", a.focusReq)
	}

	// The fade fires the kill; the focus stays where it moved.
	for _, fn := range fires {
		fn()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-posted:
		case <-time.After(2 * time.Second):
			t.Fatal("closing the pane did not kill its session")
		}
	}
	if got := a.layout.Spaces[0].Tabs[0].Focus; got != "c" {
		t.Fatalf("focus after the kill = %q, want c", got)
	}
	if _, _, ok := layout.Locate(a.layout, "b"); ok {
		t.Error("the closed pane is still in the layout")
	}
}

// TestClosingUnfocusedPaneKeepsFocus is the other half of the fix: closing a
// pane that is not its tab's focus leaves the focus, and the keyboard focus,
// where they are (spec 58, window design; REQ-DW-044).
func TestClosingUnfocusedPaneKeepsFocus(t *testing.T) {
	a := focusApp(t)
	var fires []func()
	a.after = func(_ time.Duration, fn func()) paneTimer { fires = append(fires, fn); return noopTimer{} }
	posted := fadeKills(a)

	// a is not the focused pane (b is).
	a.ClosePane("a")

	if got := a.layout.Spaces[0].Tabs[0].Focus; got != "b" {
		t.Fatalf("closing an unfocused pane moved the focus to %q, want b", got)
	}
	if a.focusReq != "" {
		t.Fatalf("closing an unfocused pane asked for the keyboard focus %q", a.focusReq)
	}

	for _, fn := range fires {
		fn()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-posted:
		case <-time.After(2 * time.Second):
			t.Fatal("closing the pane did not kill its session")
		}
	}
	if got := a.layout.Spaces[0].Tabs[0].Focus; got != "b" {
		t.Fatalf("focus after the kill = %q, want b", got)
	}
}

// TestRemovedEventFocusesTheMostRecentPane is REQ-DW-023: a session removed by
// the stream leaves each tab it was in focused on the pane the user used last,
// not on the tab's first pane (spec 58, window design).
func TestRemovedEventFocusesTheMostRecentPane(t *testing.T) {
	a := focusApp(t)

	a.applyEvent(lana.Event{Type: lana.EventSessionRemoved, Session: &lana.Session{ID: "b"}})

	if got := a.layout.Spaces[0].Tabs[0].Focus; got != "c" {
		t.Fatalf("focus after the removal event = %q, want c", got)
	}
	if a.focusReq != "c" {
		t.Fatalf("focusReq after the removal event = %q, want c", a.focusReq)
	}
}

// TestClosingFocusedPaneWithoutHistoryUsesTreeOrder is the fallback of the
// fix: with no focus history the pane before the closed one in tree order
// takes the focus (spec 58, window design).
func TestClosingFocusedPaneWithoutHistoryUsesTreeOrder(t *testing.T) {
	a := testApp(t)
	l := layout.Empty()
	l = layout.AddSpace(l, "work", "/tmp/work")
	space := l.Spaces[0].ID
	l = layout.AddTab(l, space, "a", "")
	tabID := l.Spaces[0].Tabs[0].ID
	l = layout.SplitPane(l, space, tabID, "a", "b", "row")
	l = layout.SplitPane(l, space, tabID, "b", "c", "row")
	l = layout.FocusPane(l, space, tabID, "b")
	a.layout = l
	// No focus history was ever recorded.

	a.ClosePane("b")

	if got := a.layout.Spaces[0].Tabs[0].Focus; got != "a" {
		t.Fatalf("focus right after closing = %q, want the pane before b, a", got)
	}
}
