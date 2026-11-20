package app

import (
	"encoding/json"
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// testApp is an app with two spaces of terminals, a third with none, and
// an agent that only the Agents panel shows: the state of the headless
// view test.
func testApp(t *testing.T) *App {
	t.Helper()
	now := fixedNow()
	a := New(Options{
		Home: "/Users/ada/.lana",
		Host: fakeHost{name: "studio", chip: "M4 Pro", memory: 48},
		Now:  func() time.Time { return now },
	})
	// The window test asserts the light colours, so it fixes the appearance
	// rather than following the system (REQ-DW-035).
	a.settings.Appearance = "light"
	a.conn = &fakeConn{}
	a.online = true
	// The terminal seams run at once, so the headless view test needs no
	// daemon, no native library and no real timers (see term_test.go).
	a.async = func(fn func()) { fn() }
	a.after = func(time.Duration, func()) paneTimer { return noopTimer{} }
	a.termFor = func(*paneConn, termOptions) (paneEmu, error) { return &fakeEmu{}, nil }

	l := layout.Empty()
	l = layout.AddSpace(l, "work", "/tmp/work")
	work := l.Spaces[0].ID
	l = layout.RenameSpace(l, work, "work")
	l = layout.AddTab(l, work, "s1", "")
	tabID := l.Spaces[0].Tabs[0].ID
	l = layout.SplitPane(l, work, tabID, "s1", "s2", "row")

	l = layout.AddSpace(l, "play", "/tmp/play")
	play := l.Spaces[1].ID
	l = layout.RenameSpace(l, play, "play")
	l = layout.AddTab(l, play, "s3", "")

	l = layout.AddSpace(l, "empty", "/tmp/empty")
	l = layout.RenameSpace(l, l.Spaces[2].ID, "empty")
	l = layout.SelectSpace(l, work)
	a.layout = l

	a.sessions = map[string]lana.Session{
		"s1": {ID: "s1", Title: "api", Program: "claude", State: "working", Cwd: "/tmp/work/api", Created: now.Add(-2 * time.Minute)},
		"s2": {ID: "s2", Title: "web", Program: "codex", State: "exited", ExitCode: 1, Cwd: "/tmp/work/web", Created: now.Add(-3 * time.Minute), LastOutput: now.Add(-30 * time.Second)},
		"s3": {ID: "s3", Title: "helper", Program: "claude", State: "idle", Created: now.Add(-90 * time.Second), Labels: map[string]string{"lana.agent": "Ada"}},
	}
	return a
}

// TestWindowRendersPanesExitedBarAndAgents builds the whole window with a
// fake connection and no daemon (REQ-DW-030, REQ-DW-033).
func TestWindowRendersPanesExitedBarAndAgents(t *testing.T) {
	a := testApp(t)
	tt := ui.NewTester(a.view, 1440, 900)

	for _, want := range []string{
		// The tab strip, the host chip and the palette button.
		"Search sessions, agents, commands",
		"studio",
		"M4 Pro · 48 GB",
		// The panel.
		"Spaces",
		"Agents",
		"work",
		"play",
		"empty",
		"(detached)",
		// The two panes of the open tab, and their headers.
		"api",
		"web",
		"printing",
		"exited 1",
		// The exited bar.
		"exited 1 · <1m ago",
		"Restart",
		"Close",
		// An agent only the Agents panel lists.
		"helper",
	} {
		if !tt.HasText(want) {
			t.Errorf("the window does not show %q; it shows %q", want, tt.Texts())
		}
	}
	if tt.FramePending() {
		t.Error("the view keeps asking for frames")
	}
}

// TestWindowShowsNotConnected is REQ-DW-013: the reason and a Retry
// button.
func TestWindowShowsNotConnected(t *testing.T) {
	a := New(Options{Home: "/Users/ada/.lana", Host: fakeHost{name: "studio"}, Now: fixedNow})
	a.connErr = "no daemon.secret in /Users/ada/.lana"
	tt := ui.NewTester(a.view, 1440, 900)
	for _, want := range []string{"Not connected", "no daemon.secret in /Users/ada/.lana", "Retry"} {
		if !tt.HasText(want) {
			t.Errorf("the not-connected state does not show %q; it shows %q", want, tt.Texts())
		}
	}
}

// TestClosePaneFadesOutThenKills is REQ-DW-044: closing a pane fades its card
// out for closeFadeDelay first, then kills its session at once, with no
// confirmation, whatever runs in it.
func TestClosePaneFadesOutThenKills(t *testing.T) {
	a := testApp(t)
	fake := a.conn.(*fakeConn)
	// The kill waits out the fade; the test fires it by hand.
	var kill func()
	a.after = func(_ time.Duration, fn func()) paneTimer { kill = fn; return noopTimer{} }
	// The posted work runs at once, and signals so the test can wait for it
	// without a sleep (createSession and killSession post from goroutines).
	posted := make(chan struct{}, 2)
	a.post = func(fn func()) { fn(); posted <- struct{}{} }

	a.ClosePane("s1")

	// The card fades out first: the session is not killed and the layout is
	// unchanged until the delay passes.
	if !a.closing["s1"] {
		t.Fatal("closing did not mark the session for the fade out")
	}
	if _, _, ok := layout.Locate(a.layout, "s1"); !ok {
		t.Fatal("closing removed the pane before the fade out")
	}
	fake.mu.Lock()
	early := append([]string{}, fake.killed...)
	fake.mu.Unlock()
	if len(early) != 0 {
		t.Fatalf("closing killed %v at once, want none before the fade out", early)
	}
	if kill == nil {
		t.Fatal("closing did not schedule the kill")
	}

	// The delay passes: the kill goes off; its layout update follows on the
	// UI thread.
	kill()
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
		t.Fatalf("closing the pane killed %v, want [s1]", killed)
	}
	if _, ok := a.sessions["s1"]; ok {
		t.Error("the closed session is still listed")
	}
	if a.closing["s1"] {
		t.Error("the closed session still shows as closing")
	}
	if _, _, ok := layout.Locate(a.layout, "s1"); ok {
		t.Error("the closed pane is still in the layout")
	}
	if got := layout.TabSessions(a.layout.Spaces[0].Tabs[0]); len(got) != 1 || got[0] != "s2" {
		t.Errorf("the tab still shows %v after closing s1, want [s2]", got)
	}
}

// TestCloseTabKillsEverySessionAtOnce is REQ-DW-044: closing a tab drops it
// and kills every session in it at once, with no confirmation.
func TestCloseTabKillsEverySessionAtOnce(t *testing.T) {
	a := testApp(t)
	fake := a.conn.(*fakeConn)
	space := a.layout.Spaces[1].ID
	tab := a.layout.Spaces[1].Tabs[0].ID

	a.CloseTab(space, tab)

	if _, ok := a.sessions["s3"]; ok {
		t.Error("the closed tab's session is still listed")
	}
	if findTab(a.layout, space, tab) != nil {
		t.Error("the tab is still in the layout")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		fake.mu.Lock()
		killed := append([]string{}, fake.killed...)
		fake.mu.Unlock()
		if len(killed) == 1 && killed[0] == "s3" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("closing the tab killed %v, want [s3]", killed)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestFocusAndZoomGoThroughTheLayout are the layout actions of the pane
// buttons.
func TestFocusAndZoomGoThroughTheLayout(t *testing.T) {
	a := testApp(t)
	a.FocusPane("s2")
	tab := a.layout.Spaces[0].Tabs[0]
	if tab.Focus != "s2" {
		t.Fatalf("focus = %q, want s2", tab.Focus)
	}
	a.ToggleZoom("s2")
	if got := a.layout.Spaces[0].Tabs[0].Zoom; got != "s2" {
		t.Fatalf("zoom = %q, want s2", got)
	}
	a.ToggleZoom("s2")
	if got := a.layout.Spaces[0].Tabs[0].Zoom; got != "" {
		t.Fatalf("zoom after unzoom = %q, want empty", got)
	}
}

// TestNewTerminalTakesTheFocus is REQ-DW-050: the terminal a new tab or a
// split opens becomes the focused pane and gets the keyboard focus.
func TestNewTerminalTakesTheFocus(t *testing.T) {
	run := func(t *testing.T, act func(a *App)) {
		a := testApp(t)
		posted := make(chan func(), 1)
		a.post = func(fn func()) { posted <- fn }
		act(a)
		select {
		case fn := <-posted:
			fn()
		case <-time.After(2 * time.Second):
			t.Fatal("the terminal was never created")
		}
		tab := layout.ActiveTab(layout.ActiveSpace(a.layout))
		if tab == nil || tab.Focus != "new" {
			t.Fatalf("the focused pane is %v, want the new session", tab)
		}
		if a.focusReq != "new" {
			t.Errorf("focusReq = %q, want the new session", a.focusReq)
		}
	}
	t.Run("NewTab", func(t *testing.T) {
		run(t, func(a *App) { a.NewTab(a.layout.Spaces[0].ID, "/tmp/work") })
	})
	t.Run("SplitPane", func(t *testing.T) {
		run(t, func(a *App) { a.SplitPane("s1", "row") })
	})
}

// TestFlushLayoutWritesTheDocument is the write half of REQ-DW-023.
func TestFlushLayoutWritesTheDocument(t *testing.T) {
	a := New(Options{Now: fixedNow})
	fake := &fakeConn{}
	a.conn = fake
	a.online = true
	a.markLayout(layout.AddSpace(a.layout, "work", "/tmp/work"))
	if !a.save.ready(fixedNow().Add(layoutDebounce)) {
		t.Fatal("the change is not due after the debounce")
	}
	a.flushLayout()

	deadline := time.Now().Add(2 * time.Second)
	for {
		fake.mu.Lock()
		puts := append([]json.RawMessage{}, fake.puts...)
		fake.mu.Unlock()
		if len(puts) > 0 {
			if !strings.Contains(string(puts[0]), "/tmp/work") {
				t.Fatalf("the written layout is %s", puts[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the layout was never written")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestPaneCardsShowFocusByBrightness is REQ-DW-031: the pane with the focus
// shows by a brighter card, the other by a dimmer one, and neither by a
// border. The empty terminal bodies leave the cards' colours showing.
func TestPaneCardsShowFocusByBrightness(t *testing.T) {
	a := testApp(t)
	tt := ui.NewTester(func(c *ui.Context) { a.workspace(c, &light) }, 1200, 700)
	img := tt.Image()

	// The tab's focus is s2 ("web"), so s1 ("api") is the unfocused pane.
	api, ok := tt.Find("api")
	if !ok {
		t.Fatal("the unfocused pane does not show its title")
	}
	web, ok := tt.Find("web")
	if !ok {
		t.Fatal("the focused pane does not show its title")
	}
	// Sample the card below the header, where the empty body leaves the
	// card's colour showing through.
	unfocused := img.RGBAAt(int(api.X+api.W/2), 300)
	focused := img.RGBAAt(int(web.X+web.W/2), 300)
	if !sameRGB(unfocused, light.card) {
		t.Errorf("the unfocused pane's card is %v, want %v", unfocused, light.card)
	}
	if !sameRGB(focused, light.cardFocused) {
		t.Errorf("the focused pane's card is %v, want %v", focused, light.cardFocused)
	}
	if light.card == light.cardFocused {
		t.Fatal("the focused and unfocused card colours are the same")
	}
}

// TestAgentRowsShowFocusByPill is REQ-DW-030: the agent row of the pane with
// the focus wears the pill the active space row wears, and the others do not.
func TestAgentRowsShowFocusByPill(t *testing.T) {
	a := testApp(t)
	// The body draws the panel before the workspace, so Find returns the
	// panel's row for a title, not the pane header that shows the same one,
	// and leaving out the title bar keeps the tab strip from clashing too.
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { a.body(c, &light) })
	}, 1200, 700)
	img := tt.Image()

	// The active tab's focus is s2 ("web"), so s1 ("api") is the other row.
	other, ok := tt.Find("api")
	if !ok {
		t.Fatal("the unfocused agent row does not show its title")
	}
	focused, ok := tt.Find("web")
	if !ok {
		t.Fatal("the focused agent row does not show its title")
	}
	// Find returns each row's own box (its Label names it), so sample the
	// row in its left padding, before the program tile, where only the
	// row's background shows.
	otherPx := img.RGBAAt(int(other.X+3), int(other.Y+other.H/2))
	focusedPx := img.RGBAAt(int(focused.X+3), int(focused.Y+focused.H/2))
	if !sameRGB(focusedPx, light.pill) {
		t.Errorf("the focused agent row's background is %v, want the pill %v", focusedPx, light.pill)
	}
	if sameRGB(otherPx, light.pill) {
		t.Errorf("the unfocused agent row's background is the pill %v", otherPx)
	}
}

// sameRGB reports whether a rendered pixel is a colour, its alpha aside.
func sameRGB(got color.RGBA, want ui.Color) bool {
	return got.R == want.R && got.G == want.G && got.B == want.B
}

// TestPaneHeaderButtonsShowOnlyForTheFocusedPane is REQ-DW-031: a pane's
// split, zoom and close buttons show only while the pane is focused (or
// hovered), so adding an unfocused pane adds no set of buttons.
func TestPaneHeaderButtonsShowOnlyForTheFocusedPane(t *testing.T) {
	one := testApp(t)
	one.layout = layout.RemoveSession(one.layout, "s2")
	ttOne := ui.NewTester(func(c *ui.Context) { one.workspace(c, &light) }, 1200, 700)
	single := countText(ttOne.Texts(), "Split Right")
	if single == 0 {
		t.Fatal("the focused pane does not show its split button")
	}

	two := testApp(t)
	ttTwo := ui.NewTester(func(c *ui.Context) { two.workspace(c, &light) }, 1200, 700)
	if got := countText(ttTwo.Texts(), "Split Right"); got != single {
		t.Fatalf("the workspace shows %d split buttons with two panes and %d with one: the unfocused pane's buttons are not hidden", got, single)
	}
}

// countText counts the elements of a frame showing exactly a text.
func countText(texts []string, want string) int {
	n := 0
	for _, s := range texts {
		if s == want {
			n++
		}
	}
	return n
}
