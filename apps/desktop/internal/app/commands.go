package app

import (
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/config"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// nudgeStep is how far one Move Divider command moves the nearest divider,
// as a fraction of the split (REQ-DW-050).
const nudgeStep = 0.05

// The terminal text size the shortcuts keep between, in DIPs (REQ-DW-036).
const (
	minTextSize float32 = 8
	maxTextSize float32 = 32
)

// shortcut is a key with modifiers, the binding of a command off macOS
// (REQ-DW-051).
type shortcut struct {
	Mods ui.Modifiers
	Key  ui.Key
}

// ctrlShift is Control and Shift with a key, the shape of an app shortcut
// off macOS: Control and a letter belong to the terminal there, so app
// shortcuts take Shift too.
func ctrlShift(k ui.Key) shortcut { return shortcut{ui.Ctrl | ui.Shift, k} }

// showTabKey is the shortcut of the nth tab, 1 to 9, off macOS.
func showTabKey(n int) shortcut { return ctrlShift(ui.Key0 + ui.Key(n)) }

// String writes a shortcut as menus take it and as the palette shows it,
// "Ctrl+Shift+T".
func (s shortcut) String() string {
	var parts []string
	for _, m := range []struct {
		mod  ui.Modifiers
		name string
	}{{ui.Ctrl, "Ctrl"}, {ui.Alt, "Alt"}, {ui.Shift, "Shift"}} {
		if s.Mods&m.mod != 0 {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(append(parts, keyName(s.Key)), "+")
}

// keyName names a key as menus and accelerators take it.
func keyName(k ui.Key) string {
	switch {
	case k >= ui.KeyA && k <= ui.KeyZ:
		return string(rune('A' + k - ui.KeyA))
	case k >= ui.Key0 && k <= ui.Key9:
		return string(rune('0' + k - ui.Key0))
	}
	switch k {
	case ui.KeyEnter:
		return "Enter"
	case ui.KeyTab:
		return "Tab"
	case ui.KeyPageUp:
		return "PageUp"
	case ui.KeyPageDown:
		return "PageDown"
	case ui.KeyLeft:
		return "Left"
	case ui.KeyRight:
		return "Right"
	case ui.KeyUp:
		return "Up"
	case ui.KeyDown:
		return "Down"
	case ui.KeyEqual:
		return "="
	case ui.KeyMinus:
		return "-"
	case ui.KeyBracketLeft:
		return "["
	case ui.KeyBracketRight:
		return "]"
	}
	return "?"
}

// command is an action of the menus and the command palette (REQ-DW-050).
type command struct {
	Title string
	// Accel and Keys are the shortcut and how the palette shows it on
	// macOS; Other is the binding elsewhere, where Control and a letter
	// belong to the terminal, Alt and Control-Alt make characters (AltGr)
	// and Alt and a letter open the menus (REQ-DW-051).
	Accel, Keys string
	Other       shortcut
	Run         func(a *App)
	// Hidden keeps a command out of the command palette.
	Hidden bool
}

// accelFor is the menu accelerator of a command on goos, "" when it has
// none.
func (c *command) accelFor(goos string) string {
	if goos == "darwin" {
		return c.Accel
	}
	if c.Other != (shortcut{}) {
		return c.Other.String()
	}
	return ""
}

// accel is the command's accelerator here.
func (c *command) accel() string { return c.accelFor(runtime.GOOS) }

// keysFor is how the palette shows a command's shortcut on goos.
func (c *command) keysFor(goos string) string {
	if goos == "darwin" {
		return c.Keys
	}
	return c.accelFor(goos)
}

// keys is how the palette shows the command's shortcut here.
func (c *command) keys() string { return c.keysFor(runtime.GOOS) }

// The commands of REQ-DW-050: their macOS accelerator and palette label,
// the "other" binding off macOS, and the action.
var (
	cmdNewTab      = command{Title: "New Tab", Accel: "CmdOrCtrl+T", Keys: "⌘T", Other: ctrlShift(ui.KeyT), Run: (*App).newTabHere}
	cmdNewSpace    = command{Title: "New Space", Accel: "CmdOrCtrl+Alt+T", Keys: "⌥⌘T", Run: (*App).NewSpace}
	cmdSplitR      = command{Title: "Split Right", Accel: "CmdOrCtrl+D", Keys: "⌘D", Other: ctrlShift(ui.KeyD), Run: func(a *App) { a.splitFocused("row") }}
	cmdSplitD      = command{Title: "Split Down", Accel: "CmdOrCtrl+Shift+D", Keys: "⇧⌘D", Other: ctrlShift(ui.KeyE), Run: func(a *App) { a.splitFocused("col") }}
	cmdZoom        = command{Title: "Zoom Pane", Accel: "CmdOrCtrl+Shift+Enter", Keys: "⇧⌘↩", Other: ctrlShift(ui.KeyEnter), Run: (*App).toggleZoomFocused}
	cmdEqualize    = command{Title: "Equalize Panes", Accel: "CmdOrCtrl+Ctrl+=", Keys: "⌃⌘=", Run: (*App).equalizePanes}
	cmdClosePane   = command{Title: "Close Pane", Accel: "CmdOrCtrl+W", Keys: "⌘W", Other: ctrlShift(ui.KeyW), Run: (*App).closeFocusedPane}
	cmdCloseTab    = command{Title: "Close Tab", Accel: "CmdOrCtrl+Shift+W", Keys: "⇧⌘W", Run: (*App).closeActiveTab}
	cmdNextTab     = command{Title: "Next Tab", Accel: "CmdOrCtrl+Shift+]", Keys: "⇧⌘]", Other: shortcut{ui.Ctrl, ui.KeyPageDown}, Run: func(a *App) { a.cycleTab(1) }}
	cmdPrevTab     = command{Title: "Previous Tab", Accel: "CmdOrCtrl+Shift+[", Keys: "⇧⌘[", Other: shortcut{ui.Ctrl, ui.KeyPageUp}, Run: func(a *App) { a.cycleTab(-1) }}
	cmdRenameTab   = command{Title: "Rename Tab…", Accel: "CmdOrCtrl+Shift+R", Keys: "⇧⌘R", Other: ctrlShift(ui.KeyR), Run: (*App).renameActiveTab}
	cmdTogglePanel = command{Title: "Toggle Panel", Accel: "CmdOrCtrl+B", Keys: "⌘B", Other: ctrlShift(ui.KeyB), Run: (*App).TogglePanel}
	cmdPalette     = command{Title: "Command Palette…", Accel: "CmdOrCtrl+Shift+P", Keys: "⇧⌘P", Other: ctrlShift(ui.KeyP), Run: (*App).OpenPalette, Hidden: true}
	cmdPalette2    = command{Title: "Go to Pane…", Accel: "CmdOrCtrl+P", Keys: "⌘P", Run: (*App).OpenPalette, Hidden: true}

	cmdFocusLeft  = command{Title: "Focus Pane Left", Accel: "CmdOrCtrl+Alt+Left", Keys: "⌥⌘←", Other: shortcut{ui.Alt, ui.KeyLeft}, Run: func(a *App) { a.focusPaneIn(-1, 0) }}
	cmdFocusRight = command{Title: "Focus Pane Right", Accel: "CmdOrCtrl+Alt+Right", Keys: "⌥⌘→", Other: shortcut{ui.Alt, ui.KeyRight}, Run: func(a *App) { a.focusPaneIn(1, 0) }}
	cmdFocusUp    = command{Title: "Focus Pane Above", Accel: "CmdOrCtrl+Alt+Up", Keys: "⌥⌘↑", Other: shortcut{ui.Alt, ui.KeyUp}, Run: func(a *App) { a.focusPaneIn(0, -1) }}
	cmdFocusDown  = command{Title: "Focus Pane Below", Accel: "CmdOrCtrl+Alt+Down", Keys: "⌥⌘↓", Other: shortcut{ui.Alt, ui.KeyDown}, Run: func(a *App) { a.focusPaneIn(0, 1) }}

	cmdMoveLeft  = command{Title: "Move Divider Left", Accel: "CmdOrCtrl+Ctrl+Left", Keys: "⌃⌘←", Other: shortcut{ui.Alt | ui.Shift, ui.KeyLeft}, Run: func(a *App) { a.moveDivider(-1, 0) }}
	cmdMoveRight = command{Title: "Move Divider Right", Accel: "CmdOrCtrl+Ctrl+Right", Keys: "⌃⌘→", Other: shortcut{ui.Alt | ui.Shift, ui.KeyRight}, Run: func(a *App) { a.moveDivider(1, 0) }}
	cmdMoveUp    = command{Title: "Move Divider Up", Accel: "CmdOrCtrl+Ctrl+Up", Keys: "⌃⌘↑", Other: shortcut{ui.Alt | ui.Shift, ui.KeyUp}, Run: func(a *App) { a.moveDivider(0, -1) }}
	cmdMoveDown  = command{Title: "Move Divider Down", Accel: "CmdOrCtrl+Ctrl+Down", Keys: "⌃⌘↓", Other: shortcut{ui.Alt | ui.Shift, ui.KeyDown}, Run: func(a *App) { a.moveDivider(0, 1) }}

	cmdClear        = command{Title: "Clear Screen and Scrollback", Accel: "CmdOrCtrl+K", Keys: "⌘K", Other: ctrlShift(ui.KeyK), Run: (*App).clearFocusedPane}
	cmdRestart      = command{Title: "Restart Pane", Run: (*App).restartFocusedPane}
	cmdClosePreview = command{Title: "Close Preview", Run: (*App).ClosePreview}

	cmdBigger     = command{Title: "Bigger Text", Accel: "CmdOrCtrl+=", Keys: "⌘+", Other: shortcut{ui.Ctrl, ui.KeyEqual}, Run: (*App).biggerText}
	cmdSmaller    = command{Title: "Smaller Text", Accel: "CmdOrCtrl+-", Keys: "⌘−", Other: shortcut{ui.Ctrl, ui.KeyMinus}, Run: (*App).smallerText}
	cmdActualSize = command{Title: "Actual Size", Accel: "CmdOrCtrl+0", Keys: "⌘0", Other: shortcut{ui.Ctrl, ui.Key0}, Run: (*App).actualTextSize}

	cmdLight  = command{Title: "Appearance: Light", Run: func(a *App) { a.setAppearance("light") }}
	cmdDark   = command{Title: "Appearance: Dark", Run: func(a *App) { a.setAppearance("dark") }}
	cmdSystem = command{Title: "Appearance: System", Run: func(a *App) { a.setAppearance("") }}

	cmdSettings = command{Title: "Settings…", Accel: "CmdOrCtrl+,", Keys: "⌘,", Run: (*App).OpenSettings}
)

// paletteCommands are the commands the palette lists, in order.
var paletteCommands = []*command{
	&cmdNewTab, &cmdNewSpace, &cmdSplitR, &cmdSplitD, &cmdZoom, &cmdEqualize,
	&cmdClosePane, &cmdCloseTab, &cmdNextTab, &cmdPrevTab, &cmdRenameTab,
	&cmdTogglePanel, &cmdSettings,
	&cmdFocusLeft, &cmdFocusRight, &cmdFocusUp, &cmdFocusDown,
	&cmdMoveLeft, &cmdMoveRight, &cmdMoveUp, &cmdMoveDown,
	&cmdBigger, &cmdSmaller, &cmdActualSize, &cmdClear,
	&cmdRestart, &cmdClosePreview, &cmdLight, &cmdDark, &cmdSystem,
}

// commands are all of them, including the hidden ones the menus still
// carry.
var commands = append(slices.Clone(paletteCommands), &cmdPalette, &cmdPalette2)

// tabAccelFor is the accelerator of the nth tab's item on goos.
func tabAccelFor(goos string, n int) string {
	if goos == "darwin" {
		return fmt.Sprintf("CmdOrCtrl+%d", n)
	}
	return showTabKey(n).String()
}

// spaceAccelFor is the accelerator of the nth space's item on goos, "" off
// macOS where Go to Space has no binding (REQ-DW-050).
func spaceAccelFor(goos string, n int) string {
	if goos == "darwin" {
		return fmt.Sprintf("CmdOrCtrl+Alt+%d", n)
	}
	return ""
}

// namedAccel is an accelerator and what runs it, for the tests that check
// the shortcuts of REQ-DW-050.
type namedAccel struct {
	Accel, Label string
}

// accelerators lists every accelerator the app binds on goos, in a stable
// order (REQ-DW-050, REQ-DW-051).
func accelerators(goos string) []namedAccel {
	var out []namedAccel
	for _, cmd := range commands {
		if accel := cmd.accelFor(goos); accel != "" {
			out = append(out, namedAccel{accel, cmd.Title})
		}
	}
	for i := 1; i <= 9; i++ {
		out = append(out, namedAccel{tabAccelFor(goos, i), fmt.Sprintf("Show Tab %d", i)})
		if accel := spaceAccelFor(goos, i); accel != "" {
			out = append(out, namedAccel{accel, fmt.Sprintf("Show Space %d", i)})
		}
	}
	// Control-Tab cycles the tabs of the active space on every platform,
	// off the menus (REQ-DW-050).
	out = append(out, namedAccel{"Ctrl+Tab", "Next Tab"}, namedAccel{"Ctrl+Shift+Tab", "Previous Tab"})
	return out
}

// Menu builds the app's menu bar: its items carry the accelerators, so the
// shortcuts of REQ-DW-050 work even while a terminal has the keyboard
// focus, and the menus show them.
func (a *App) Menu() *mygo.Menu {
	item := func(cmd *command) *mygo.MenuItem {
		return &mygo.MenuItem{Label: cmd.Title, Accelerator: cmd.accel(), Click: func(*mygo.MenuItem, *mygo.Window) {
			a.runCommand(cmd.Run)
		}}
	}
	tabItems := []*mygo.MenuItem{item(&cmdNextTab), item(&cmdPrevTab), mygo.Separator()}
	for i := 1; i <= 9; i++ {
		i := i
		tabItems = append(tabItems, &mygo.MenuItem{
			Label:       fmt.Sprintf("Show Tab %d", i),
			Accelerator: tabAccelFor(runtime.GOOS, i),
			Click:       func(*mygo.MenuItem, *mygo.Window) { a.runCommand(func(a *App) { a.showTab(i) }) },
		})
	}
	spaceItems := []*mygo.MenuItem{}
	for i := 1; i <= 9; i++ {
		i := i
		spaceItems = append(spaceItems, &mygo.MenuItem{
			Label:       fmt.Sprintf("Show Space %d", i),
			Accelerator: spaceAccelFor(runtime.GOOS, i),
			Click:       func(*mygo.MenuItem, *mygo.Window) { a.runCommand(func(a *App) { a.selectSpaceN(i) }) },
		})
	}
	// The app menu carries Settings… at the home macOS gives it (REQ-DW-060).
	appMenu := &mygo.MenuItem{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
		{Role: mygo.RoleAbout},
		mygo.Separator(),
		item(&cmdSettings),
		mygo.Separator(),
		{Role: mygo.RoleServices},
		mygo.Separator(),
		{Role: mygo.RoleHide},
		{Role: mygo.RoleHideOthers},
		{Role: mygo.RoleUnhide},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	}}
	return mygo.NewMenu([]*mygo.MenuItem{
		appMenu,
		{Label: "Shell", Submenu: []*mygo.MenuItem{
			item(&cmdNewTab),
			mygo.Separator(),
			item(&cmdSplitR),
			item(&cmdSplitD),
			mygo.Separator(),
			item(&cmdClosePane),
			item(&cmdCloseTab),
			mygo.Separator(),
			item(&cmdNewSpace),
		}},
		{Label: "Edit", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleUndo},
			{Role: mygo.RoleRedo},
			mygo.Separator(),
			{Role: mygo.RoleCut},
			{Role: mygo.RoleCopy},
			{Role: mygo.RolePaste},
			{Role: mygo.RoleSelectAll},
			mygo.Separator(),
			item(&cmdClear),
		}},
		{Label: "View", Submenu: []*mygo.MenuItem{
			item(&cmdPalette),
			item(&cmdPalette2),
			mygo.Separator(),
			item(&cmdZoom),
			item(&cmdEqualize),
			mygo.Separator(),
			{Label: "Focus", Submenu: []*mygo.MenuItem{item(&cmdFocusLeft), item(&cmdFocusRight), item(&cmdFocusUp), item(&cmdFocusDown)}},
			{Label: "Resize", Submenu: []*mygo.MenuItem{item(&cmdMoveLeft), item(&cmdMoveRight), item(&cmdMoveUp), item(&cmdMoveDown)}},
			mygo.Separator(),
			item(&cmdBigger),
			item(&cmdSmaller),
			item(&cmdActualSize),
			mygo.Separator(),
			item(&cmdTogglePanel),
			mygo.Separator(),
			{Label: "Go to Space", Submenu: spaceItems},
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
			{Role: mygo.RoleToggleDevTools, Label: "Inspector", Hidden: !mygo.IsDev()},
		}},
		{Label: "Tabs", Submenu: append(tabItems, mygo.Separator(), item(&cmdRenameTab))},
		{Role: mygo.RoleWindowMenu},
	})
}

// runCommand runs a command from a menu, on the UI thread.
func (a *App) runCommand(run func(a *App)) {
	if a.win == nil {
		return
	}
	a.post(func() {
		if a.win != nil {
			run(a)
		}
	})
}

// shortcuts handles the keys the menus do not: Control-Tab between the
// tabs of the active space, and off macOS the commands of REQ-DW-051,
// which native-UI windows run no menu shortcuts for. The window's
// shortcuts come before the terminal's keys, so the terminal never
// swallows an app shortcut (REQ-DW-042).
func (a *App) shortcuts(c *ui.Context) {
	// Settings… opens with ⌘, (REQ-DW-060), before the terminal's own keys.
	if c.Shortcut(ui.Super, ui.KeyComma) {
		a.OpenSettings()
	}
	if c.Shortcut(ui.Ctrl, ui.KeyTab) {
		a.cycleTab(1)
	}
	if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyTab) {
		a.cycleTab(-1)
	}
	// Esc closes the preview panel while it is the focused region
	// (REQ-DW-081). Focusing a pane takes that focus off the panel, so Esc
	// still reaches a terminal.
	if a.preview != nil && a.previewFocused && c.Shortcut(0, ui.KeyEscape) {
		a.ClosePreview()
	}
	if runtime.GOOS == "darwin" {
		return
	}
	for _, cmd := range commands {
		if cmd.Other != (shortcut{}) && c.Shortcut(cmd.Other.Mods, cmd.Other.Key) {
			cmd.Run(a)
		}
	}
	for i := 1; i <= 9; i++ {
		if k := showTabKey(i); c.Shortcut(k.Mods, k.Key) {
			a.showTab(i)
		}
	}
}

// newTabHere opens a terminal in the active space, in the focused pane's
// folder (REQ-DW-050).
func (a *App) newTabHere() {
	space := layout.ActiveSpace(a.layout)
	if space == nil {
		a.NewSpace()
		return
	}
	a.NewTab(space.ID, a.focusedCwd())
}

// splitFocused splits the focused pane, right of it ("row") or below it
// ("col") (REQ-DW-050).
func (a *App) splitFocused(dir string) {
	if id := a.focusedSession(); id != "" {
		a.SplitPane(id, dir)
	}
}

// closeFocusedPane closes the focused pane (REQ-DW-044, REQ-DW-050).
func (a *App) closeFocusedPane() {
	if id := a.focusedSession(); id != "" {
		a.ClosePane(id)
	}
}

// closeActiveTab closes the active tab of the active space (REQ-DW-050).
func (a *App) closeActiveTab() {
	space := layout.ActiveSpace(a.layout)
	if space == nil {
		return
	}
	if tab := layout.ActiveTab(space); tab != nil {
		a.CloseTab(space.ID, tab.ID)
	}
}

// toggleZoomFocused zooms the focused pane, or unzooms it (REQ-DW-050).
func (a *App) toggleZoomFocused() {
	if id := a.focusedSession(); id != "" {
		a.ToggleZoom(id)
	}
}

// equalizePanes gives every split of the active tab equal shares
// (REQ-DW-050).
func (a *App) equalizePanes() {
	space := layout.ActiveSpace(a.layout)
	tab := layout.ActiveTab(space)
	if space == nil || tab == nil {
		return
	}
	a.markLayout(layout.Equalize(a.layout, space.ID, tab.ID))
}

// focusPaneIn focuses the pane next to the focused one, toward (dx, dy)
// (REQ-DW-050).
func (a *App) focusPaneIn(dx, dy int) {
	space := layout.ActiveSpace(a.layout)
	tab := layout.ActiveTab(space)
	if space == nil || tab == nil || tab.Focus == "" {
		return
	}
	// The rectangles only need to be in proportion: the box the view lays
	// the tab out in is not known here.
	rects := layout.Rects(tab.Root, 0, 0, 1000, 1000, paneGap)
	if next, ok := layout.Neighbor(rects, tab.Focus, dx, dy); ok {
		a.FocusPane(next)
	}
}

// moveDivider moves the nearest divider of the focused pane one step in
// the direction (dx, dy) (REQ-DW-050).
func (a *App) moveDivider(dx, dy int) {
	space := layout.ActiveSpace(a.layout)
	tab := layout.ActiveTab(space)
	if space == nil || tab == nil || tab.Focus == "" {
		return
	}
	a.markLayout(layout.NudgeDivider(a.layout, space.ID, tab.ID, tab.Focus, dx, dy, nudgeStep))
}

// showTab makes the nth tab of the active space active, from 1; 9 is the
// last one. A tab out of range leaves the layout as it is (REQ-DW-050).
func (a *App) showTab(n int) {
	space := layout.ActiveSpace(a.layout)
	if space == nil || len(space.Tabs) == 0 {
		return
	}
	i := n - 1
	if n == 9 {
		i = len(space.Tabs) - 1
	}
	if i < 0 || i >= len(space.Tabs) {
		return
	}
	a.SelectTab(space.ID, space.Tabs[i].ID)
}

// cycleTab moves d tabs along the active space, wrapping around
// (REQ-DW-050).
func (a *App) cycleTab(d int) {
	space := layout.ActiveSpace(a.layout)
	if space == nil || len(space.Tabs) < 2 {
		return
	}
	idx := 0
	for i := range space.Tabs {
		if space.Tabs[i].ID == space.ActiveTab {
			idx = i
			break
		}
	}
	n := len(space.Tabs)
	idx = ((idx+d)%n + n) % n
	a.SelectTab(space.ID, space.Tabs[idx].ID)
}

// renameActiveTab begins editing the active tab's title (REQ-DW-050).
func (a *App) renameActiveTab() {
	space := layout.ActiveSpace(a.layout)
	if space == nil {
		return
	}
	if tab := layout.ActiveTab(space); tab != nil {
		a.StartRename(space.ID, tab.ID)
	}
}

// selectSpaceN makes the nth space active, from 1 (REQ-DW-050).
func (a *App) selectSpaceN(n int) {
	if n < 1 || n > len(a.layout.Spaces) {
		return
	}
	a.SelectSpace(a.layout.Spaces[n-1].ID)
}

// clearFocusedPane clears the focused terminal's screen and scrollback,
// locally: the session keeps them (REQ-DW-050).
func (a *App) clearFocusedPane() {
	id := a.focusedSession()
	if id == "" {
		return
	}
	if pt := a.terms[id]; pt != nil {
		if emu := pt.emulator(); emu != nil {
			emu.Clear()
		}
	}
}

// restartFocusedPane restarts the focused pane when its session has exited
// (REQ-DW-033, REQ-DW-050).
func (a *App) restartFocusedPane() {
	id := a.focusedSession()
	if id == "" {
		return
	}
	if s, ok := a.sessions[id]; ok && s.State == "exited" {
		a.Restart(id)
	}
}

// biggerText, smallerText and actualTextSize are the text size commands
// (REQ-DW-036, REQ-DW-050).
func (a *App) biggerText()     { a.setTextSize(a.textSize() + 1) }
func (a *App) smallerText()    { a.setTextSize(a.textSize() - 1) }
func (a *App) actualTextSize() { a.setTextSize(config.DefaultTextSize) }

// setTextSize keeps the terminals' text size, in settings.json, and gives
// it to the terminals the window shows (REQ-DW-036).
func (a *App) setTextSize(size float32) {
	size = min(max(size, minTextSize), maxTextSize)
	if size == a.textSize() {
		return
	}
	a.settings.TextSize = size
	a.saveSettings()
	for _, pt := range a.terms {
		if emu := pt.emulator(); emu != nil {
			emu.SetFontSize(size)
		}
	}
	a.invalidate()
}

// setAppearance keeps the light or dark choice, empty to follow the system
// (REQ-DW-035). The window redraws in the new palette at once, and MyGo's
// own parts follow it.
func (a *App) setAppearance(v string) {
	if a.settings.Appearance == v {
		return
	}
	a.settings.Appearance = v
	a.saveSettings()
	a.applyAppearance()
	a.invalidate()
}

// saveSettings writes the settings to the app's data directory.
func (a *App) saveSettings() {
	if a.dataDir == "" {
		return
	}
	if err := config.Save(a.dataDir, a.settings); err != nil {
		a.setNotice("saving settings: " + err.Error())
	}
}
