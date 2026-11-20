package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/config"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// TestRenameSpaceDialogSaves is the space half of spec 58, spaces: the dialog
// opens prefilled with the label, and Enter (or Save) keeps what is typed and
// turns the automatic naming off.
func TestRenameSpaceDialogSaves(t *testing.T) {
	a := testApp(t)
	space := a.layout.Spaces[1].ID // "play"
	a.RenameSpaceDialog(space)
	if !a.dialog.open || a.dialog.kind != "space" || a.dialog.spaceID != space {
		t.Fatalf("the rename dialog opened as %+v", a.dialog)
	}
	if got := a.dialog.text; got != "play" {
		t.Fatalf("the dialog is prefilled with %q, want the label %q", got, "play")
	}

	tt := ui.NewTester(a.View(), 1440, 900)
	for _, want := range []string{"Rename space", "Leave empty to name it after the folder of its active terminal.", "Save", "Cancel"} {
		if !tt.HasText(want) {
			t.Errorf("the rename dialog does not show %q; it shows %q", want, tt.Texts())
		}
	}

	tt.Type("!")
	typed := a.dialog.text
	if typed == "play" {
		t.Fatal("typing into the dialog changed nothing")
	}
	tt.Key(0, ui.KeyEnter)

	sp := a.spaceByID(space)
	if sp == nil {
		t.Fatal("the space went missing")
	}
	if sp.Name != typed || sp.Auto {
		t.Errorf("Enter saved %q (Auto %v), want %q with Auto off", sp.Name, sp.Auto, typed)
	}
	if a.dialog.open {
		t.Error("Enter left the dialog open")
	}
}

// TestRenameSpaceDialogCancels is spec 58, spaces: Escape and Cancel close the
// dialog without touching the layout.
func TestRenameSpaceDialogCancels(t *testing.T) {
	for _, close := range []string{"escape", "cancel"} {
		t.Run(close, func(t *testing.T) {
			a := testApp(t)
			space := a.layout.Spaces[1].ID
			before := a.spaceByID(space).Name

			a.RenameSpaceDialog(space)
			a.dialog.text = "changed"
			tt := ui.NewTester(a.View(), 1440, 900)
			switch close {
			case "escape":
				tt.Key(0, ui.KeyEscape)
			default:
				if err := tt.Click("Cancel"); err != nil {
					t.Fatalf("Cancel: %v", err)
				}
			}
			if a.dialog.open {
				t.Fatalf("%s left the dialog open", close)
			}
			if got := a.spaceByID(space).Name; got != before {
				t.Errorf("%s saved %q, want the name unchanged at %q", close, got, before)
			}
		})
	}
}

// TestRenameSpaceDialogEmptyRestoresAutoNaming is spec 58, spaces: an empty
// name turns the automatic folder naming back on.
func TestRenameSpaceDialogEmptyRestoresAutoNaming(t *testing.T) {
	a := testApp(t)
	l := layout.RenameSpace(a.layout, a.layout.Spaces[1].ID, "play")
	a.layout = l
	space := a.layout.Spaces[1].ID
	if a.spaceByID(space).Auto {
		t.Fatal("the setup left the space auto-named")
	}

	a.RenameSpaceDialog(space)
	a.dialog.text = "" // the user cleared the field
	tt := ui.NewTester(a.View(), 1440, 900)
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if sp := a.spaceByID(space); sp == nil || !sp.Auto {
		t.Error("an empty name did not turn automatic naming back on")
	}
	if a.dialog.open {
		t.Error("Save left the dialog open")
	}
}

// TestRenameSpaceDialogPrefillsAndSelects is spec 58, spaces: the dialog
// opens with the space's label in the field, the folder of its active
// terminal as the placeholder, and the name selected so that typing replaces
// it.
func TestRenameSpaceDialogPrefillsAndSelects(t *testing.T) {
	a := testApp(t)
	work := a.layout.Spaces[0].ID // "work", its active pane in /tmp/work/web
	a.RenameSpaceDialog(work)

	if got := a.dialog.text; got != "work" {
		t.Fatalf("the dialog is prefilled with %q, want the label %q", got, "work")
	}
	if got := a.dialog.current; got != "work" {
		t.Errorf("the dialog's subtitle is %q, want the label %q", got, "work")
	}
	if got := a.dialog.placeholder; got != "web" {
		t.Errorf("the dialog's placeholder is %q, want the active terminal's folder %q", got, "web")
	}

	tt := ui.NewTester(a.View(), 1440, 900)
	for _, want := range []string{"Rename space", "Colour", "Save", "Cancel", "esc", "↵"} {
		if !tt.HasText(want) {
			t.Errorf("the rename dialog does not show %q; it shows %q", want, tt.Texts())
		}
	}

	// The current name is selected, so a keystroke replaces it rather than
	// appending to it.
	tt.Type("Z")
	if got := a.dialog.text; got != "Z" {
		t.Errorf("typing left the name at %q, want it replaced by %q", got, "Z")
	}
}

// TestRenameSpacePreviewFollowsTyping is spec 58, spaces: the header's tile
// shows the first letter of the name being typed, updating as it changes.
func TestRenameSpacePreviewFollowsTyping(t *testing.T) {
	a := testApp(t)
	work := a.layout.Spaces[0].ID
	a.RenameSpaceDialog(work)
	tt := ui.NewTester(a.View(), 1440, 900)

	if _, ok := tt.Find("W"); !ok {
		t.Fatalf("the preview does not show the label's first letter; it shows %q", tt.Texts())
	}
	tt.Type("Q") // replaces the selected "work"
	if _, ok := tt.Find("Q"); !ok {
		t.Errorf("the preview does not follow the name being typed; it shows %q", tt.Texts())
	}

	// An empty name falls back to the automatic folder label ("web").
	a.dialog.text = ""
	tt.Frame()
	if _, ok := tt.Find("W"); !ok {
		t.Errorf("an empty name does not preview the automatic label; it shows %q", tt.Texts())
	}
}

// TestRenameTabDialogShowsFocusedSession is spec 58, spaces: the tab dialog's
// subtitle is the tab's label, its placeholder the focused session's title,
// and its preview tile that session's mark.
func TestRenameTabDialogShowsFocusedSession(t *testing.T) {
	a := testApp(t)
	space, tab := a.layout.Spaces[0].ID, a.layout.Spaces[0].Tabs[0].ID
	a.RenameTabDialog(space, tab)

	if a.dialog.current != "web" {
		t.Errorf("the subtitle is %q, want the focused session's title %q", a.dialog.current, "web")
	}
	if a.dialog.placeholder != "web" {
		t.Errorf("the placeholder is %q, want the focused session's title %q", a.dialog.placeholder, "web")
	}
	want := tileOf(a.sessions["s2"])
	if a.dialog.tabTile.BG != want.BG || a.dialog.tabTile.Glyph != want.Glyph ||
		a.dialog.tabTile.Logo != want.Logo || a.dialog.tabTile.Mono != want.Mono {
		t.Errorf("the preview tile is %+v, want the focused session's %+v", a.dialog.tabTile, want)
	}
}

// TestRenameSpaceDialogPicksColourAndSaves is spec 58, spaces: picking a
// colour in the dialog's row stores it with the name Save keeps.
func TestRenameSpaceDialogPicksColourAndSaves(t *testing.T) {
	a := testApp(t)
	work := a.layout.Spaces[0].ID
	a.RenameSpaceDialog(work)
	tt := ui.NewTester(a.View(), 1440, 900)

	const pick = "#5B7BE0"
	if err := tt.Click("Space colour " + pick); err != nil {
		t.Fatalf("clicking the %s swatch: %v", pick, err)
	}
	if a.dialog.color != pick {
		t.Fatalf("picking %s left the dialog's colour at %q", pick, a.dialog.color)
	}

	a.dialog.text = "renamed"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	sp := a.spaceByID(work)
	if sp == nil {
		t.Fatal("the space went missing")
	}
	if sp.Name != "renamed" || sp.Auto {
		t.Errorf("Save left the name %q (Auto %v), want %q with Auto off", sp.Name, sp.Auto, "renamed")
	}
	if sp.Color != pick {
		t.Errorf("Save left the colour %q, want the picked %q", sp.Color, pick)
	}
	if a.dialog.open {
		t.Error("Save left the dialog open")
	}
}

// TestRenameDialogBackdropCancels is spec 58, spaces: a press on the dimmed
// window, away from the panel, closes the dialog and touches no layout.
func TestRenameDialogBackdropCancels(t *testing.T) {
	a := testApp(t)
	work := a.layout.Spaces[0].ID
	before := a.spaceByID(work).Name
	a.RenameSpaceDialog(work)
	a.dialog.text = "changed"
	tt := ui.NewTester(a.View(), 1440, 900)

	tt.ClickAt(20, 20)
	if a.dialog.open {
		t.Fatal("a press on the backdrop left the dialog open")
	}
	if got := a.spaceByID(work).Name; got != before {
		t.Errorf("the backdrop saved %q, want the name unchanged at %q", got, before)
	}
}

// TestRenameTabDialogSavesAndClears is spec 58, spaces: the tab dialog keeps a
// typed title, and an empty one goes back to the program's own title.
func TestRenameTabDialogSavesAndClears(t *testing.T) {
	a := testApp(t)
	space, tab := a.layout.Spaces[0].ID, a.layout.Spaces[0].Tabs[0].ID

	a.RenameTabDialog(space, tab)
	if !a.dialog.open || a.dialog.kind != "tab" || a.dialog.title != "Rename tab" {
		t.Fatalf("the tab dialog opened as %+v", a.dialog)
	}
	if !strings.Contains(a.dialog.hint, "Leave empty to use the program's title.") {
		t.Errorf("the tab dialog's hint is %q", a.dialog.hint)
	}
	a.dialog.text = "build"
	tt := ui.NewTester(a.View(), 1440, 900)
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := findTab(a.layout, space, tab).Title; got != "build" {
		t.Errorf("the tab title is %q, want %q", got, "build")
	}

	a.RenameTabDialog(space, tab)
	a.dialog.text = ""
	tt2 := ui.NewTester(a.View(), 1440, 900)
	if err := tt2.Click("Save"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := findTab(a.layout, space, tab).Title; got != "" {
		t.Errorf("an empty title left %q, want the program's own title", got)
	}
}

// TestSpaceMenuOpensFromRightClickAndMoreButton is spec 58, spaces: a
// right-click on a row and the row's ⋯ button open the same menu, whose items
// rename, open a terminal and close the space.
func TestSpaceMenuOpensFromRightClickAndMoreButton(t *testing.T) {
	a := testApp(t)
	play := a.layout.Spaces[1].ID
	tt := ui.NewTester(a.View(), 1440, 900)

	if err := tt.RightClick("play"); err != nil {
		t.Fatalf("right-clicking the row: %v", err)
	}
	got := tt.Menu()
	for _, want := range []string{"Rename space…", "New terminal", "Close space"} {
		if !slices.Contains(got, want) {
			t.Fatalf("the space menu is %v, missing %q", got, want)
		}
	}
	if err := tt.ChooseMenuItem("Rename space…"); err != nil {
		t.Fatalf("choosing Rename space…: %v", err)
	}
	if !a.dialog.open || a.dialog.spaceID != play {
		t.Errorf("the menu's Rename opened %+v, want the dialog for %s", a.dialog, play)
	}
}

// TestSpaceMoreButtonAppearsOnHover is spec 58, spaces: the ⋯ button takes a
// click on the active row and on a row the pointer is over, and stays hidden,
// taking no click, on a row at rest.
func TestSpaceMoreButtonAppearsOnHover(t *testing.T) {
	// The active space ("work") shows an interactive ⋯ button.
	active := testApp(t)
	ttActive := ui.NewTester(active.View(), 1440, 900)
	if err := ttActive.Click("Space actions: work"); err != nil {
		t.Fatalf("the active space's ⋯ button: %v", err)
	}
	if !slices.Contains(ttActive.Menu(), "Close space") {
		t.Errorf("the active ⋯ button's menu is %v", ttActive.Menu())
	}

	a := testApp(t)
	tt := ui.NewTester(a.View(), 1440, 900)

	// An inactive row at rest keeps its button invisible: it is built, but
	// takes no click (MyGo's Tester refuses to click an element that ignores
	// input, so the press reaches the row instead).
	if err := tt.Click("Space actions: play"); err == nil {
		t.Error("an inactive space's ⋯ button takes a click without a hover")
	}

	// Hovering the row shows the button, which opens the same menu.
	row, ok := tt.Find("play")
	if !ok {
		t.Fatal("the play row does not show")
	}
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	if err := tt.Click("Space actions: play"); err != nil {
		t.Fatalf("hovering the row, clicking its ⋯ button: %v", err)
	}
	if !slices.Contains(tt.Menu(), "Close space") {
		t.Errorf("the hovered ⋯ button's menu is %v", tt.Menu())
	}
}

// TestSpaceMenuSurvivesPointerMovingToItsItems is spec 58, spaces: the menu
// opened from a row that is not active stays open as the pointer leaves the
// row for its items, so Rename space… can be chosen. Both the ⋯ button and
// the row's right-click open such a menu.
func TestSpaceMenuSurvivesPointerMovingToItsItems(t *testing.T) {
	for _, opened := range []string{"more", "right-click"} {
		t.Run(opened, func(t *testing.T) {
			a := testApp(t)
			play := a.layout.Spaces[1].ID // "play", not the active space
			tt := ui.NewTester(a.View(), 1440, 900)

			switch opened {
			case "more":
				row, ok := tt.Find("play")
				if !ok {
					t.Fatal("the play row does not show")
				}
				tt.Move(row.X+row.W/2, row.Y+row.H/2)
				if err := tt.Click("Space actions: play"); err != nil {
					t.Fatalf("clicking the ⋯ button: %v", err)
				}
			default:
				if err := tt.RightClick("play"); err != nil {
					t.Fatalf("right-clicking the row: %v", err)
				}
			}

			// The pointer leaves the row for the menu item: the menu stays.
			item, ok := tt.Find("Rename space…")
			if !ok {
				t.Fatalf("the menu closed before the pointer reached it; the window shows %q", tt.Texts())
			}
			tt.Move(item.X+item.W/2, item.Y+item.H/2)
			if err := tt.Click("Rename space…"); err != nil {
				t.Fatalf("clicking Rename space…: %v", err)
			}
			if !a.dialog.open || a.dialog.kind != "space" || a.dialog.spaceID != play {
				t.Errorf("the menu's Rename opened %+v, want the dialog for %s", a.dialog, play)
			}
		})
	}
}

// TestCloseSpaceKillsEverySessionAndRemovesIt is spec 58, spaces (REQ-DW-044):
// closing a space kills every session it holds at once, with no confirmation,
// and the next space becomes active.
func TestCloseSpaceKillsEverySessionAndRemovesIt(t *testing.T) {
	a := testApp(t)
	fake := a.conn.(*fakeConn)
	work := a.layout.Spaces[0].ID
	play := a.layout.Spaces[1].ID

	a.CloseSpace(work)

	killed := append([]string{}, fake.killed...)
	slices.Sort(killed)
	if !slices.Equal(killed, []string{"s1", "s2"}) {
		t.Errorf("closing the space killed %v, want the space's sessions [s1 s2]", killed)
	}
	for _, id := range []string{"s1", "s2"} {
		if _, ok := a.sessions[id]; ok {
			t.Errorf("the closed space's session %s is still listed", id)
		}
	}
	if a.spaceByID(work) != nil {
		t.Error("the closed space is still in the layout")
	}
	if got := a.layout.ActiveSpace; got != play {
		t.Errorf("the active space is %q, want the next one %q", got, play)
	}
	if _, ok := a.sessions["s3"]; !ok {
		t.Error("closing a space dropped another space's session")
	}
}

// TestCloseInactiveSpaceKeepsTheActiveOne is spec 58, spaces: closing a space
// that is not active leaves the active one alone.
func TestCloseInactiveSpaceKeepsTheActiveOne(t *testing.T) {
	a := testApp(t)
	work := a.layout.Spaces[0].ID
	a.CloseSpace(a.layout.Spaces[1].ID)
	if got := a.layout.ActiveSpace; got != work {
		t.Errorf("closing an inactive space moved the active one to %q, want %q", got, work)
	}
}

// TestPanelToggleHidesThePanel is spec 58, panel: Toggle Panel drops the panel
// from the window, and the command table carries it.
func TestPanelToggleHidesThePanel(t *testing.T) {
	dir := t.TempDir()
	a := testApp(t)
	a.dataDir = dir

	tt := ui.NewTester(a.View(), 1440, 900)
	if !tt.HasText("Spaces") || !tt.HasText("Resize panel") {
		t.Fatalf("the shown panel does not show its header and grip; it shows %q", tt.Texts())
	}

	cmdTogglePanel.Run(a)
	tt.Frame()
	if a.settings.PanelHidden != true {
		t.Error("Toggle Panel did not hide the panel")
	}
	if tt.HasText("Spaces") {
		t.Error("the hidden panel still shows its header")
	}
	if !config.Load(dir).PanelHidden {
		t.Error("the hidden panel was not written to the settings")
	}

	cmdTogglePanel.Run(a)
	tt.Frame()
	if a.settings.PanelHidden || !tt.HasText("Spaces") {
		t.Error("Toggle Panel did not show the panel again")
	}
}

// TestPanelWidthClampsAndPersists is spec 58, panel: the width stays between
// its limits and is kept in the settings.
func TestPanelWidthClampsAndPersists(t *testing.T) {
	dir := t.TempDir()
	a := testApp(t)
	a.dataDir = dir

	if got := a.panelWidth(); got != config.DefaultPanelWidth {
		t.Fatalf("a fresh panel is %v wide, want the default %v", got, config.DefaultPanelWidth)
	}

	a.setPanelWidth(10)
	if got := a.panelWidth(); got != minPanelWidth {
		t.Errorf("a drag below the minimum left %v, want %v", got, minPanelWidth)
	}
	a.setPanelWidth(1000)
	if got := a.panelWidth(); got != maxPanelWidth {
		t.Errorf("a drag above the maximum left %v, want %v", got, maxPanelWidth)
	}

	a.setPanelWidth(300)
	a.saveSettings()
	if got := config.Load(dir).PanelWidth; got != 300 {
		t.Errorf("the settings hold %v, want the dragged width 300", got)
	}
}

// TestPanelWidthDragsOnItsEdge is spec 58, panel: dragging the grip on the
// panel's right edge sets the width, and the write follows once the drag ends.
func TestPanelWidthDragsOnItsEdge(t *testing.T) {
	dir := t.TempDir()
	a := testApp(t)
	a.dataDir = dir
	tt := ui.NewTester(a.View(), 1440, 900)

	grip, ok := tt.Find("Resize panel")
	if !ok {
		t.Fatal("the panel has no grip on its edge")
	}
	x, y := grip.X+grip.W/2, grip.Y+grip.H/2
	tt.Press(x, y)
	tt.Move(x+40, y)
	tt.Release(x+40, y)

	if got := a.panelWidth(); got <= config.DefaultPanelWidth {
		t.Errorf("dragging the grip right left the panel %v wide, want more than %v", got, config.DefaultPanelWidth)
	}
	if got := config.Load(dir).PanelWidth; got != a.panelWidth() {
		t.Errorf("the settings hold %v after the drag, want %v", got, a.panelWidth())
	}
}

// TestAgentsGroupingToggle is REQ-DW-030 and spec 58, window design: the
// Agents header shows the layers and clock toggle buttons, and the one the
// layout's grouping chooses is pressed.
func TestAgentsGroupingToggle(t *testing.T) {
	for _, name := range []string{"layers", "clock"} {
		if icon(name) == nil {
			t.Fatalf("the grouping toggle's %q icon is not in the set", name)
		}
	}

	a := testApp(t)
	tt := ui.NewTester(a.View(), 1440, 900)

	space, ok := tt.AccessByRole(ui.RoleToggleButton, "Group by space")
	if !ok {
		t.Fatalf("the Agents header has no 'Group by space' toggle; it shows %q", tt.Texts())
	}
	status, ok := tt.AccessByRole(ui.RoleToggleButton, "Group by status")
	if !ok {
		t.Fatalf("the Agents header has no 'Group by status' toggle; it shows %q", tt.Texts())
	}
	if !space.Checked || status.Checked {
		t.Errorf("a fresh layout groups by space, but the toggles are space=%v status=%v", space.Checked, status.Checked)
	}

	if err := tt.Click("Group by status"); err != nil {
		t.Fatalf("clicking 'Group by status': %v", err)
	}
	if a.layout.Grouping != "status" {
		t.Fatalf("the grouping is %q, want status", a.layout.Grouping)
	}
	space, _ = tt.AccessByRole(ui.RoleToggleButton, "Group by space")
	status, _ = tt.AccessByRole(ui.RoleToggleButton, "Group by status")
	if space.Checked || !status.Checked {
		t.Errorf("grouping by status left the toggles space=%v status=%v", space.Checked, status.Checked)
	}
}
