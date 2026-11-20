package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// TestLanaChipShowsTheAgentAndTask is spec 58: a session Lana runs shows its
// agent and task in its pane's header.
func TestLanaChipShowsTheAgentAndTask(t *testing.T) {
	a := testApp(t)
	s := a.sessions["s1"]
	s.Labels = map[string]string{"lana.agent": "ada", "lana.task": "deploy"}
	a.sessions["s1"] = s
	tt := ui.NewTester(func(c *ui.Context) { a.workspace(c, &light) }, 1200, 700)
	if !tt.HasText("ada · deploy") {
		t.Errorf("the pane header does not show the Lana chip; it shows %q", tt.Texts())
	}
}

// TestLanaChipNeedsAnAgent is spec 58: a task without a Lana agent shows no
// chip.
func TestLanaChipNeedsAnAgent(t *testing.T) {
	a := testApp(t)
	s := a.sessions["s1"]
	s.Labels = map[string]string{"lana.task": "deploy"}
	a.sessions["s1"] = s
	tt := ui.NewTester(func(c *ui.Context) { a.workspace(c, &light) }, 1200, 700)
	if tt.HasText("deploy") {
		t.Errorf("a task without an agent shows a Lana chip; it shows %q", tt.Texts())
	}
}

// focusedEmu is a pane's terminal whose view takes the keyboard focus as it
// is built, standing in for a terminal the user clicked into.
type focusedEmu struct{ fakeEmu }

func (e *focusedEmu) View(c *ui.Context) ui.Element { return ui.Box(c).Focus() }

// TestTerminalFocusFocusesItsPane is REQ-DW-040: a pane whose terminal view
// reports the keyboard focus becomes the focused pane, so the pane follows
// its terminal as GoRex's does.
func TestTerminalFocusFocusesItsPane(t *testing.T) {
	a := testApp(t)
	// The panes need a terminal to draw, so the attach of the fake
	// connection, which refuses, is replaced (see term_test.go).
	a.attach = func(context.Context, string) (paneAttachment, error) { return &fakeAttachment{}, nil }
	space := a.layout.Spaces[0]
	tab := space.Tabs[0]
	// Start with s1 focused, and let s2's terminal report the focus.
	a.layout = layout.FocusPane(a.layout, space.ID, tab.ID, "s1")
	a.termFor = func(conn *paneConn, _ termOptions) (paneEmu, error) {
		if conn.pt.sessionID == "s2" {
			return &focusedEmu{}, nil
		}
		return &fakeEmu{}, nil
	}
	ui.NewTester(func(c *ui.Context) { a.workspace(c, &light) }, 1200, 700)
	if got := layout.ActiveTab(layout.ActiveSpace(a.layout)).Focus; got != "s2" {
		t.Fatalf("the pane whose terminal took the focus is %q, want s2", got)
	}
}

// TestWorkspaceShowsThePreviewPanel is REQ-DW-081: the workspace lays the
// preview panel beside the panes when a preview is set. There is no web view
// factory in a headless test, so the body draws the placeholder naming the
// file.
func TestWorkspaceShowsThePreviewPanel(t *testing.T) {
	a, d := previewApp(t)
	d.answer(http.StatusOK, `{"path":"/tmp/work/notes.md","name":"notes.md","dir":"/tmp/work","kind":"markdown","size":5,"url":"/v1/workbench/preview/t/notes.md?view=1","raw_url":"/v1/workbench/preview/t/notes.md"}`)
	openPreview(t, a, "s1", "notes.md", "/tmp/work")
	if a.preview == nil {
		t.Fatal("the preview did not open")
	}
	tt := ui.NewTester(func(c *ui.Context) { a.workspace(c, &light) }, 1200, 700)
	if !tt.HasText("Preview") {
		t.Errorf("the workspace does not show the preview placeholder; it shows %q", tt.Texts())
	}
	if !tt.HasText("notes.md") {
		t.Errorf("the panel does not name the file; it shows %q", tt.Texts())
	}
}
