package app

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// TestShellQuotePaths is the escaping of files dropped on a pane: a plain
// path stands as it is, one with a character a shell reads otherwise is
// single-quoted with its own single quotes escaped, and the paths are joined
// by spaces and followed by one trailing space (REQ-DW-043).
func TestShellQuotePaths(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		want  string
	}{
		{"none", nil, ""},
		{"plain", []string{"/tmp/a.go"}, "/tmp/a.go "},
		{"plain punctuation", []string{"a@b:c%d+e=f,g-h_i.j/k"}, "a@b:c%d+e=f,g-h_i.j/k "},
		{"spaces", []string{"/tmp/a b.go"}, "'/tmp/a b.go' "},
		{"single quote", []string{"/tmp/it's.go"}, `'/tmp/it'\''s.go' `},
		{"unicode", []string{"/tmp/日本語"}, "'/tmp/日本語' "},
		{"several", []string{"/tmp/a", "/tmp/b c"}, "/tmp/a '/tmp/b c' "},
		{"tilde", []string{"~/notes.md"}, "'~/notes.md' "},
	}
	for _, tc := range cases {
		if got := shellQuotePaths(tc.paths); got != tc.want {
			t.Errorf("%s: shellQuotePaths(%q) = %q, want %q", tc.name, tc.paths, got, tc.want)
		}
	}
}

// labeledEmu is a fake pane terminal whose view is a focusable element named
// for its pane, so a test can read whether that pane's terminal took the
// keyboard focus (REQ-DW-043).
type labeledEmu struct {
	*fakeEmu
	label string
}

func (e labeledEmu) View(c *ui.Context) ui.Element {
	return ui.Box(c).Fill().Focusable().Label(e.label)
}

// dropApp is an app whose panes have labeled fake terminals, wired so the
// headless view lets a test see the focus and the pastes of each pane.
func dropApp(t *testing.T) (*App, map[string]*fakeEmu) {
	t.Helper()
	a := testApp(t)
	a.attach = func(context.Context, string) (paneAttachment, error) { return &fakeAttachment{}, nil }
	emus := map[string]*fakeEmu{}
	a.termFor = func(conn *paneConn, _ termOptions) (paneEmu, error) {
		id := conn.pt.sessionID
		e := &fakeEmu{}
		emus[id] = e
		return labeledEmu{fakeEmu: e, label: "term-" + id}, nil
	}
	return a, emus
}

// TestDroppedFilesTypeQuotedPathsIntoThePane is the pane half of REQ-DW-043:
// dropping files on a pane focuses it and its terminal, and types their
// quoted paths into its session as a paste, with one trailing space and no
// newline.
func TestDroppedFilesTypeQuotedPathsIntoThePane(t *testing.T) {
	a, emus := dropApp(t)
	tt := ui.NewTester(func(c *ui.Context) { a.workspace(c, &light) }, 1200, 700)

	// The tab's focus is s2 ("web"), so a drop on s1 ("api") must move it.
	api, ok := tt.Find("api")
	if !ok {
		t.Fatal("the pane does not show its title")
	}
	tt.DropFiles(api.X+api.W/2, api.Y+api.H/2, "/tmp/a b", "/tmp/c")

	if got := a.layout.Spaces[0].Tabs[0].Focus; got != "s1" {
		t.Fatalf("the drop left the focus on %q, want s1", got)
	}
	if !tt.Focused("term-s1") {
		t.Error("the pane's terminal did not take the keyboard focus")
	}
	emu := emus["s1"]
	if emu == nil {
		t.Fatal("no terminal was made for the pane")
	}
	if got, want := emu.lastPaste(), "'/tmp/a b' /tmp/c "; got != want {
		t.Fatalf("the pane's session got %q, want %q", got, want)
	}
}

// TestDroppedFilesDoNothingOnAnExitedPane is REQ-DW-043: a drop on a session
// that has exited neither focuses the pane nor types anything into it.
func TestDroppedFilesDoNothingOnAnExitedPane(t *testing.T) {
	a, emus := dropApp(t)
	// Focus the live pane, so a drop on the exited one would show.
	space, tab := a.layout.Spaces[0].ID, a.layout.Spaces[0].Tabs[0].ID
	a.layout = layout.FocusPane(a.layout, space, tab, "s1")

	tt := ui.NewTester(func(c *ui.Context) { a.workspace(c, &light) }, 1200, 700)
	web, ok := tt.Find("web")
	if !ok {
		t.Fatal("the exited pane does not show its title")
	}
	tt.DropFiles(web.X+web.W/2, web.Y+web.H/2, "/tmp/a")

	if got := a.layout.Spaces[0].Tabs[0].Focus; got != "s1" {
		t.Fatalf("a drop on the exited pane moved the focus to %q, want s1", got)
	}
	if got := emus["s2"].lastPaste(); got != "" {
		t.Fatalf("a drop on the exited pane typed %q into it", got)
	}
}
