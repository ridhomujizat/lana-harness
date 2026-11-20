package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestClosePaneTwiceKillsOnce is REQ-DW-044: a second close while a pane
// fades out does nothing, so the session is killed once.
func TestClosePaneTwiceKillsOnce(t *testing.T) {
	a := testApp(t)
	fake := a.conn.(*fakeConn)
	var fires []func()
	a.after = func(_ time.Duration, fn func()) paneTimer {
		fires = append(fires, fn)
		return noopTimer{}
	}
	posted := make(chan struct{}, 2)
	a.post = func(fn func()) { fn(); posted <- struct{}{} }

	a.ClosePane("s1")
	a.ClosePane("s1")

	if len(fires) != 1 {
		t.Fatalf("closing twice scheduled %d kills, want 1", len(fires))
	}
	for _, fn := range fires {
		fn()
	}
	// The kill goes off; its layout update follows on the UI thread.
	for i := 0; i < 2; i++ {
		select {
		case <-posted:
		case <-time.After(2 * time.Second):
			t.Fatal("closing the pane did not kill its session")
		}
	}

	fake.mu.Lock()
	killed := append([]string{}, fake.killed...)
	fake.mu.Unlock()
	if len(killed) != 1 || killed[0] != "s1" {
		t.Fatalf("closing twice killed %v, want [s1] once", killed)
	}
}

// TestNewTabMarksPanesEntering is spec 58: a pane a new tab or a split opens
// is marked to fade in, while a pane from the initial layout is not.
func TestNewTabMarksPanesEntering(t *testing.T) {
	a := testApp(t)
	posted := make(chan struct{}, 2)
	a.post = func(fn func()) { fn(); posted <- struct{}{} }

	// A pane from the initial layout was never marked: it does not fade.
	if a.entering["s1"] {
		t.Fatal("a pane from the initial layout is marked entering")
	}

	a.NewTab(a.layout.Spaces[0].ID, "/tmp/work")
	select {
	case <-posted:
	case <-time.After(2 * time.Second):
		t.Fatal("the new terminal was never created")
	}
	if !a.entering["new"] {
		t.Error("a pane a new tab opened is not marked entering")
	}
}

// TestClosingAndEnteringPanesLayOut is spec 58: a closing pane keeps its card
// built while it fades out, and a freshly entered one builds without panic
// and drops its mark once posed.
func TestClosingAndEnteringPanesLayOut(t *testing.T) {
	a := testApp(t)
	a.closing["s2"] = true
	a.entering["s1"] = true
	tt := ui.NewTester(func(c *ui.Context) { a.workspace(c, &light) }, 1200, 700)
	if !tt.HasText("api") || !tt.HasText("web") {
		t.Errorf("the fading panes do not lay out; the workspace shows %q", tt.Texts())
	}
	if a.entering["s1"] {
		t.Error("the entering pane keeps its mark past the frame it was posed in")
	}
}
