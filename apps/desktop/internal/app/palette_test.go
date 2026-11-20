package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// TestFuzzyScoreRanks is REQ-DW-052: the fuzzy match ranks a prefix above
// a word start, that above a match inside, and that above the letters
// found in order.
func TestFuzzyScoreRanks(t *testing.T) {
	prefix, ok := fuzzyScore("Command Palette", "command")
	if !ok {
		t.Fatal(`"command" did not match "Command Palette"`)
	}
	word, _ := fuzzyScore("Command Palette", "palette")
	inside, _ := fuzzyScore("Command Palette", "mand")
	loose, _ := fuzzyScore("Command Palette", "cmdp")
	if !(prefix > word && word > inside && inside > loose) {
		t.Errorf("the ranks are not prefix > word > inside > loose: %d, %d, %d, %d", prefix, word, inside, loose)
	}
	if _, ok := fuzzyScore("Command Palette", "zzz"); ok {
		t.Error(`"zzz" matched "Command Palette"`)
	}
	if _, ok := fuzzyScore("anything", ""); !ok {
		t.Error("an empty query did not match")
	}
	// A word start is told from a match inside by the letters before it.
	if _, ok := fuzzyScore("Rename Tab", "tab"); !ok {
		t.Error(`"tab" did not match "Rename Tab"`)
	}
}

// TestPaletteFilters is REQ-DW-052: the palette lists sessions, spaces and
// commands, and the query filters them.
func TestPaletteFilters(t *testing.T) {
	a := testApp(t)
	a.paletteQuery = ""
	all := a.paletteItems()
	if len(all) == 0 {
		t.Fatal("the palette lists nothing with no query")
	}
	if all[0].section != sectionSessions {
		t.Errorf("the palette starts with %q, want the sessions", all[0].section)
	}
	if !hasTitle(all, "Equalize Panes") {
		t.Error("the commands are missing with no query")
	}
	if !hasTitle(all, "work") {
		t.Error("the spaces are missing with no query")
	}

	a.paletteQuery = "equalize"
	got := a.paletteItems()
	if len(got) != 1 || got[0].title != "Equalize Panes" || got[0].section != sectionCommands {
		t.Errorf("filtering by equalize got %+v", got)
	}

	a.paletteQuery = "web"
	got = a.paletteItems()
	if len(got) == 0 || got[0].section != sectionSessions {
		t.Errorf("filtering by web got %+v", got)
	}

	a.paletteQuery = "zzzz"
	if got := a.paletteItems(); len(got) != 0 {
		t.Errorf("zzzz matched %d rows", len(got))
	}
}

// TestPaletteButtonOpens is REQ-DW-052: the title bar's palette button
// opens the palette.
func TestPaletteButtonOpens(t *testing.T) {
	a := testApp(t)
	tt := ui.NewTester(a.View(), 1440, 900)
	if err := tt.Click("Search sessions, agents, commands"); err != nil {
		t.Fatalf("clicking the palette button: %v", err)
	}
	if !a.paletteOpen {
		t.Fatal("the palette button did not open the palette")
	}
}

// TestPaletteEnterJumpsToSession is REQ-DW-052: Enter on a session row
// selects its space and tab and focuses its pane.
func TestPaletteEnterJumpsToSession(t *testing.T) {
	a := testApp(t)
	a.FocusPane("s1")
	tt := ui.NewTester(a.View(), 1440, 900)

	a.OpenPalette()
	tt.Frame()
	tt.Type("web")
	tt.Key(0, ui.KeyEnter)

	if a.paletteOpen {
		t.Fatal("Enter did not close the palette")
	}
	work := a.layout.Spaces[0]
	if a.layout.ActiveSpace != work.ID {
		t.Errorf("the jump selected space %q, want %q", a.layout.ActiveSpace, work.ID)
	}
	tab := layout.ActiveTab(&work)
	if tab == nil || tab.Focus != "s2" {
		t.Fatalf("the jump left the tab's focus at %v, want s2", tab)
	}
	if a.focusReq != "s2" {
		t.Errorf("the jump asked the focus for %q, want s2", a.focusReq)
	}
}

// hasTitle reports whether a palette row with the given title is there.
func hasTitle(items []paletteItem, title string) bool {
	for _, it := range items {
		if it.title == title {
			return true
		}
	}
	return false
}
