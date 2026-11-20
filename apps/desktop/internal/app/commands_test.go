package app

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/config"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// multiTabApp is an app with n tabs in one space, ready to drive the tab
// commands (REQ-DW-050).
func multiTabApp(t *testing.T, n int) *App {
	t.Helper()
	a := New(Options{Home: "/Users/ada/.lana", UserHome: "/Users/ada", Now: fixedNow})
	a.conn = &fakeConn{}
	a.async = func(fn func()) { fn() }
	a.after = func(time.Duration, func()) paneTimer { return noopTimer{} }
	a.termFor = func(*paneConn, termOptions) (paneEmu, error) { return &fakeEmu{}, nil }
	l := layout.Empty()
	l = layout.AddSpace(l, "work", "/tmp/work")
	work := l.Spaces[0].ID
	l = layout.RenameSpace(l, work, "work")
	a.sessions = map[string]lana.Session{}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("s%d", i+1)
		a.sessions[id] = lana.Session{ID: id, Program: "zsh"}
		l = layout.AddTab(l, work, id, "")
	}
	a.layout = l
	return a
}

// TestAcceleratorsAreUniquePerPlatform is REQ-DW-070: no accelerator is
// used twice on any platform.
func TestAcceleratorsAreUniquePerPlatform(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		seen := map[string]string{}
		for _, na := range accelerators(goos) {
			if prev, dup := seen[na.Accel]; dup {
				t.Errorf("%s: %q is both %q and %q", goos, na.Accel, prev, na.Label)
			}
			seen[na.Accel] = na.Label
		}
	}
}

// TestShortcutsCoverREQDw050 is REQ-DW-070: every shortcut of REQ-DW-050
// is present, on macOS and off it.
func TestShortcutsCoverREQDw050(t *testing.T) {
	cases := map[string][]string{
		"darwin": {
			"CmdOrCtrl+T", "CmdOrCtrl+D", "CmdOrCtrl+Shift+D", "CmdOrCtrl+W", "CmdOrCtrl+Shift+W",
			"CmdOrCtrl+Shift+Enter", "CmdOrCtrl+Ctrl+=",
			"CmdOrCtrl+Alt+Left", "CmdOrCtrl+Alt+Right", "CmdOrCtrl+Alt+Up", "CmdOrCtrl+Alt+Down",
			"CmdOrCtrl+Ctrl+Left", "CmdOrCtrl+Ctrl+Right", "CmdOrCtrl+Ctrl+Up", "CmdOrCtrl+Ctrl+Down",
			"CmdOrCtrl+1", "CmdOrCtrl+2", "CmdOrCtrl+3", "CmdOrCtrl+4", "CmdOrCtrl+5",
			"CmdOrCtrl+6", "CmdOrCtrl+7", "CmdOrCtrl+8", "CmdOrCtrl+9",
			"CmdOrCtrl+Shift+]", "CmdOrCtrl+Shift+[", "Ctrl+Tab", "Ctrl+Shift+Tab",
			"CmdOrCtrl+Shift+R", "CmdOrCtrl+Shift+P", "CmdOrCtrl+P", "CmdOrCtrl+K",
			"CmdOrCtrl+=", "CmdOrCtrl+-", "CmdOrCtrl+0",
			"CmdOrCtrl+Alt+1", "CmdOrCtrl+Alt+2", "CmdOrCtrl+Alt+3", "CmdOrCtrl+Alt+4", "CmdOrCtrl+Alt+5",
			"CmdOrCtrl+Alt+6", "CmdOrCtrl+Alt+7", "CmdOrCtrl+Alt+8", "CmdOrCtrl+Alt+9",
			"CmdOrCtrl+Alt+T",
		},
		"linux": {
			"Ctrl+Shift+T", "Ctrl+Shift+D", "Ctrl+Shift+E", "Ctrl+Shift+W", "Ctrl+Shift+Enter",
			"Alt+Left", "Alt+Right", "Alt+Up", "Alt+Down",
			"Alt+Shift+Left", "Alt+Shift+Right", "Alt+Shift+Up", "Alt+Shift+Down",
			"Ctrl+Shift+1", "Ctrl+Shift+2", "Ctrl+Shift+3", "Ctrl+Shift+4", "Ctrl+Shift+5",
			"Ctrl+Shift+6", "Ctrl+Shift+7", "Ctrl+Shift+8", "Ctrl+Shift+9",
			"Ctrl+PageDown", "Ctrl+PageUp", "Ctrl+Tab", "Ctrl+Shift+Tab",
			"Ctrl+Shift+R", "Ctrl+Shift+P", "Ctrl+Shift+K",
			"Ctrl+=", "Ctrl+-", "Ctrl+0",
		},
	}
	for goos, want := range cases {
		have := map[string]bool{}
		for _, na := range accelerators(goos) {
			have[na.Accel] = true
		}
		for _, accel := range want {
			if !have[accel] {
				t.Errorf("%s: the shortcut %q is missing", goos, accel)
			}
		}
	}
}

// TestCycleTabWraps is REQ-DW-050: Next and Previous Tab wrap around the
// tabs of the active space.
func TestCycleTabWraps(t *testing.T) {
	a := multiTabApp(t, 3)
	first := a.layout.Spaces[0].Tabs[0].ID
	last := a.layout.Spaces[0].Tabs[2].ID
	if got := a.layout.Spaces[0].ActiveTab; got != last {
		t.Fatalf("the setup left tab %q active, want the last", got)
	}
	cmdNextTab.Run(a)
	if got := a.layout.Spaces[0].ActiveTab; got != first {
		t.Errorf("Next Tab from the last went to %q, want the first", got)
	}
	cmdPrevTab.Run(a)
	if got := a.layout.Spaces[0].ActiveTab; got != last {
		t.Errorf("Previous Tab from the first went to %q, want the last", got)
	}
}

// TestShowTabOutOfRangeIsNoop is REQ-DW-050: Go to Tab N out of range
// leaves the layout alone, and 9 is the last tab.
func TestShowTabOutOfRangeIsNoop(t *testing.T) {
	a := multiTabApp(t, 3)
	before := a.layout.Spaces[0].ActiveTab
	a.showTab(5)
	if got := a.layout.Spaces[0].ActiveTab; got != before {
		t.Errorf("Go to Tab 5 of 3 changed the tab to %q", got)
	}
	a.showTab(9)
	if got := a.layout.Spaces[0].ActiveTab; got != a.layout.Spaces[0].Tabs[2].ID {
		t.Errorf("Go to Tab 9 went to %q, want the last", got)
	}
	a.showTab(1)
	if got := a.layout.Spaces[0].ActiveTab; got != a.layout.Spaces[0].Tabs[0].ID {
		t.Errorf("Go to Tab 1 went to %q, want the first", got)
	}
}

// TestCtrlTabCyclesTabs is REQ-DW-050: Control-Tab cycles the tabs of the
// active space, through the window's shortcuts so the terminal does not
// swallow it.
func TestCtrlTabCyclesTabs(t *testing.T) {
	a := multiTabApp(t, 3)
	before := a.layout.Spaces[0].ActiveTab
	tt := ui.NewTester(a.View(), 1440, 900)
	tt.Key(ui.Ctrl, ui.KeyTab)
	if got := a.layout.Spaces[0].ActiveTab; got == before {
		t.Fatal("Control-Tab did not change the tab")
	}
}

// TestEqualizeAndMoveDividerChangeTheLayout is REQ-DW-050: the Equalize
// and Move Divider commands reach the layout.
func TestEqualizeAndMoveDividerChangeTheLayout(t *testing.T) {
	a := testApp(t)
	space := a.layout.Spaces[0]
	tab := space.Tabs[0]
	a.layout = layout.SetSizes(a.layout, space.ID, tab.ID, nil, []float64{0.7, 0.3})
	if sizes := layout.SplitSizes(a.layout.Spaces[0].Tabs[0].Root); sizes[0] <= 0.6 {
		t.Fatalf("the setup did not set the sizes: %v", sizes)
	}

	cmdEqualize.Run(a)
	sizes := layout.SplitSizes(a.layout.Spaces[0].Tabs[0].Root)
	if math.Abs(sizes[0]-0.5) > 1e-9 || math.Abs(sizes[1]-0.5) > 1e-9 {
		t.Errorf("Equalize left the sizes %v, want 0.5/0.5", sizes)
	}

	a.FocusPane("s1")
	cmdMoveRight.Run(a)
	sizes = layout.SplitSizes(a.layout.Spaces[0].Tabs[0].Root)
	if sizes[0] <= 0.5 {
		t.Errorf("Move Divider Right left the sizes %v, want the first bigger", sizes)
	}
}

// TestTextSizePersists is REQ-DW-036: the text size commands keep the
// size in settings.json.
func TestTextSizePersists(t *testing.T) {
	dir := t.TempDir()
	a := New(Options{Now: fixedNow, DataDir: dir})

	a.biggerText()
	if a.settings.TextSize != config.DefaultTextSize+1 {
		t.Fatalf("Bigger Text set %v, want %v", a.settings.TextSize, config.DefaultTextSize+1)
	}
	if got := config.Load(dir); got.TextSize != config.DefaultTextSize+1 {
		t.Errorf("the file holds %v, want %v", got.TextSize, config.DefaultTextSize+1)
	}

	a.smallerText()
	a.smallerText()
	if a.settings.TextSize != config.DefaultTextSize-1 {
		t.Errorf("two Smaller Text left %v, want %v", a.settings.TextSize, config.DefaultTextSize-1)
	}

	a.actualTextSize()
	if got := config.Load(dir); got.TextSize != config.DefaultTextSize {
		t.Errorf("Actual Size wrote %v, want %v", got.TextSize, config.DefaultTextSize)
	}
}

// TestTextSizeAppliesToLiveTerminals is REQ-DW-036: the text size command
// gives the new size to the terminals the window shows.
func TestTextSizeAppliesToLiveTerminals(t *testing.T) {
	h := newTermHarness(t)
	h.session("s1", 80, 24)
	pt := h.pane("s1")
	pt.show()
	emu := h.lastEmu()

	h.app.setTextSize(18)
	emu.mu.Lock()
	fonts := append([]float32{}, emu.fonts...)
	emu.mu.Unlock()
	if len(fonts) == 0 || fonts[len(fonts)-1] != 18 {
		t.Errorf("the terminal got font sizes %v, want 18", fonts)
	}
}

// TestClearFocusedPaneReachesTheTerminal is REQ-DW-050: Clear reaches the
// focused pane's terminal.
func TestClearFocusedPaneReachesTheTerminal(t *testing.T) {
	h := newTermHarness(t)
	h.session("s1", 80, 24)
	pt := h.pane("s1")
	pt.show()
	emu := h.lastEmu()

	h.app.layout = layout.Empty()
	h.app.layout = layout.AddSpace(h.app.layout, "work", "/tmp/work")
	h.app.layout = layout.AddTab(h.app.layout, h.app.layout.Spaces[0].ID, "s1", "")

	h.app.clearFocusedPane()
	emu.mu.Lock()
	cleared := emu.cleared
	emu.mu.Unlock()
	if !cleared {
		t.Error("Clear did not reach the focused terminal")
	}
}
