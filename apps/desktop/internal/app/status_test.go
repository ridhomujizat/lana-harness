package app

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// TestDisplayStatus is the resolver of spec 58, done status: done stands in
// for idle, and a session that is not idle never shows as done.
func TestDisplayStatus(t *testing.T) {
	cases := []struct {
		st   layout.Status
		done bool
		want layout.Status
	}{
		{layout.StatusIdle, false, layout.StatusIdle},
		{layout.StatusIdle, true, statusDone},
		{layout.StatusWorking, true, layout.StatusWorking},
		{layout.StatusBlocked, true, layout.StatusBlocked},
		{layout.StatusExited, true, layout.StatusExited},
		{statusDone, true, statusDone},
	}
	for _, tc := range cases {
		if got := displayStatus(tc.st, tc.done); got != tc.want {
			t.Errorf("displayStatus(%q, %v) = %q, want %q", tc.st, tc.done, got, tc.want)
		}
	}
}

// TestBecomeDone is the transition of spec 58, done status: an agent that
// moves from working to idle while the user is not looking becomes done.
func TestBecomeDone(t *testing.T) {
	working := lana.Session{ID: "s", Program: "claude", State: "working"}
	idle := lana.Session{ID: "s", Program: "claude", State: "idle"}

	if !becomeDone(working, idle, false) {
		t.Error("an unseen agent that finished should become done")
	}
	if becomeDone(working, idle, true) {
		t.Error("an agent the user is looking at should not become done")
	}
	// A non-agent (a shell) is never done.
	shellWorking := lana.Session{ID: "s", Program: "zsh", State: "working"}
	if becomeDone(shellWorking, lana.Session{ID: "s", Program: "zsh", State: "idle"}, false) {
		t.Error("a shell should never become done")
	}
	// A session that needs the user, or has exited, is not done.
	if becomeDone(working, lana.Session{ID: "s", Program: "claude", NeedsYou: true}, false) {
		t.Error("a session that needs the user should not become done")
	}
	if becomeDone(working, lana.Session{ID: "s", Program: "claude", State: "exited"}, false) {
		t.Error("a session that exited should not become done")
	}
	// An update that is not a working-to-idle move is not a transition.
	if becomeDone(idle, idle, false) {
		t.Error("an already idle session is not a working-to-idle move")
	}
}

// TestMarkDoneMarksAnUnseenAgent is the event half of spec 58, done status:
// a session that finishes while the window is not focused becomes done.
func TestMarkDoneMarksAnUnseenAgent(t *testing.T) {
	a := testApp(t)
	a.sessions["s1"] = lana.Session{ID: "s1", Program: "claude", State: "working"}
	next := lana.Session{ID: "s1", Program: "claude", State: "idle"}
	a.applyEvent(lana.Event{Type: lana.EventSessionUpdated, Session: &next})
	if !a.isDone("s1") {
		t.Fatal("an unseen agent that finished is not done")
	}
}

// TestMarkDoneSkipsTheSeenPane is spec 58, done status: the pane the user is
// looking at does not become done when it finishes.
func TestMarkDoneSkipsTheSeenPane(t *testing.T) {
	a := testApp(t)
	a.focusedWin = true
	work := a.layout.Spaces[0]
	a.layout = layout.FocusPane(a.layout, work.ID, work.Tabs[0].ID, "s1")
	a.sessions["s1"] = lana.Session{ID: "s1", Program: "claude", State: "working"}
	next := lana.Session{ID: "s1", Program: "claude", State: "idle"}
	a.applyEvent(lana.Event{Type: lana.EventSessionUpdated, Session: &next})
	if a.isDone("s1") {
		t.Fatal("the pane the user is looking at became done")
	}
}

// TestMarkDoneSkipsAShell is spec 58, done status: a non-agent is never
// done.
func TestMarkDoneSkipsAShell(t *testing.T) {
	a := testApp(t)
	a.sessions["z"] = lana.Session{ID: "z", Program: "zsh", State: "working"}
	next := lana.Session{ID: "z", Program: "zsh", State: "idle"}
	a.applyEvent(lana.Event{Type: lana.EventSessionUpdated, Session: &next})
	if a.isDone("z") {
		t.Fatal("a shell became done")
	}
}

// TestFocusClearsDone is spec 58, done status: focusing a done pane clears
// its mark.
func TestFocusClearsDone(t *testing.T) {
	a := testApp(t)
	a.focusedWin = true
	a.done["s1"] = true
	a.FocusPane("s1")
	if a.isDone("s1") {
		t.Fatal("focusing the pane did not clear done")
	}
	// A different pane keeps its own mark.
	a.done["s2"] = true
	a.FocusPane("s1")
	if !a.isDone("s2") {
		t.Fatal("focusing another pane cleared a pane that is not focused")
	}
}

// TestWorkingClearsDone is spec 58, done status: a session that starts
// working again stops being done.
func TestWorkingClearsDone(t *testing.T) {
	a := testApp(t)
	a.done["s1"] = true
	next := lana.Session{ID: "s1", Program: "claude", State: "working"}
	a.applyEvent(lana.Event{Type: lana.EventSessionUpdated, Session: &next})
	if a.isDone("s1") {
		t.Fatal("working again did not clear done")
	}
}

// TestStatusGroupsOrder is spec 58, done status: done sits between working
// and idle, with exited last.
func TestStatusGroupsOrder(t *testing.T) {
	want := []layout.Status{
		layout.StatusBlocked, layout.StatusWorking, statusDone, layout.StatusIdle, layout.StatusExited,
	}
	if len(statusGroups) != len(want) {
		t.Fatalf("statusGroups = %v, want %v", statusGroups, want)
	}
	for i := range want {
		if statusGroups[i] != want[i] {
			t.Fatalf("statusGroups[%d] = %q, want %q", i, statusGroups[i], want[i])
		}
	}
	if got := statusGroupName(statusDone); got != "Done" {
		t.Errorf("statusGroupName(done) = %q, want Done", got)
	}
}

// TestDoneColoursAreBlue is spec 58, done status: the done dot is the blue
// in the Agents list and on a tab.
func TestDoneColoursAreBlue(t *testing.T) {
	if want := ui.Hex("#4A8CF7"); light.blue != want {
		t.Fatalf("the done blue is %#v, want %#v", light.blue, want)
	}
	a := testApp(t)
	a.sessions["s1"] = lana.Session{ID: "s1", Program: "claude", State: "idle"}
	a.done["s1"] = true

	// The Agents list dot for the done agent.
	dot := ui.NewTester(func(c *ui.Context) {
		a.statusDot(c, &light, displayStatus(statusOf(a.sessions["s1"]), a.isDone("s1")))
	}, 24, 24)
	if !hasColor(dot.Image(), light.blue) {
		t.Error("the done dot in the Agents list is not blue")
	}

	// The tab dot: its only pane is the done agent.
	a.layout = layout.RemoveSession(a.layout, "s2")
	tab := a.layout.Spaces[0].Tabs[0]
	if got := a.tabStatus(tab); got != statusDone {
		t.Fatalf("a tab whose only pane is done is %q, want done", got)
	}
	td := ui.NewTester(func(c *ui.Context) { a.tabDot(c, &light, tab) }, 24, 24)
	if !hasColor(td.Image(), light.blue) {
		t.Error("the done tab dot is not blue")
	}
}

// hasColor reports whether a rendered image shows a colour anywhere.
func hasColor(img *image.RGBA, want ui.Color) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if sameRGB(img.RGBAAt(x, y), want) {
				return true
			}
		}
	}
	return false
}
