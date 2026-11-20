package app

import (
	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// tabDrag is what a tab carries while it is dragged to another place
// (REQ-DW-034).
type tabDrag struct {
	Space string
	Tab   string
}

// The tab strip's metrics (spec 58, window design): every tab is the same
// width, and a tab's program tiles are landscape, the ones behind peeking
// out tileStep to the right of the one in front.
const (
	tabWidth = 176
	tileSize = 16
	tileStep = 4
)

// titleBar draws the window's title bar under the traffic lights: the tab
// strip of the active space, the host chip and the palette button
// (spec 58, window design).
func (a *App) titleBar(c *ui.Context, k *palette) {
	bar := c.TitleBar()
	left := bar.Left
	if left == 0 {
		left = 12
	} else {
		left += 14
	}
	ui.Row(c).Height(titleBarHeight).Padding(0, 12, 0, left).Gap(10).AlignItems(ui.Center).
		Background(k.window).DragWindow().Children(func() {
		a.showPanelButton(c, k)
		a.tabStrip(c, k)
		ui.Spacer(c)
		a.hostChip(c, k)
		a.paletteButton(c, k)
	})
	ui.Box(c).Height(1).Background(k.hairline)
}

// showPanelButton is the way back to a hidden panel: it sits at the left of
// the tab strip and shows only while the panel is hidden, so nothing else
// on screen leaves the panel unreachable (spec 58, panel).
func (a *App) showPanelButton(c *ui.Context, k *palette) {
	if !a.settings.PanelHidden {
		return
	}
	if a.panelButton(c, k, "show-panel", "Show panel (⌘B)").Clicked() {
		a.TogglePanel()
	}
}

// tabStrip is the tabs of the active space and the "+" that opens one.
func (a *App) tabStrip(c *ui.Context, k *palette) {
	space := layout.ActiveSpace(a.layout)
	ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(1).MinWidth(0).Role(ui.RoleTabList).Label("Tabs").Children(func() {
		if space != nil {
			tabs := space.Tabs
			for i := range tabs {
				a.tabPill(c, k, space.ID, i, tabs[i])
			}
		}
		a.newTabButton(c, k, space)
	})
}

// newTabButton is the "+" after the tabs: a new terminal in the active
// space (REQ-DW-030), or a new space when there is none.
func (a *App) newTabButton(c *ui.Context, k *palette, space *layout.Space) {
	if space == nil {
		if a.iconButton(c, k, "new-space", "plus", "New terminal").Clicked() {
			a.NewSpace()
		}
		return
	}
	if a.iconButton(c, k, "new-tab-"+space.ID, "plus", "New Tab").Clicked() {
		a.NewTerminal(space.ID)
	}
}

// tabPill draws a tab: the tiles of its panes' programs, its title, and
// its status dot (REQ-DW-034).
func (a *App) tabPill(c *ui.Context, k *palette, spaceID string, index int, tab layout.Tab) {
	active := false
	if space := layout.ActiveSpace(a.layout); space != nil {
		active = space.ActiveTab == tab.ID
	}
	e := ui.Row(c.Key("tab-"+tab.ID)).Height(30).Width(tabWidth).Padding(6, 12, 6, 9).Gap(7).Radius(8).
		AlignItems(ui.Center).Shrink(1).MinWidth(96).Role(ui.RoleTab).Selected(active).
		Label(tabLabel(tab, a.sessions))
	switch {
	case active:
		e.Background(k.pill).Border(1, k.pillBorder)
	case e.Hovered():
		e.Background(k.hover)
	}
	e.Children(func() {
		a.tabTiles(c, tab)
		if a.renamingTab == tab.ID {
			a.renameField(c, spaceID, tab)
			return
		}
		ui.Text(c, tabLabel(tab, a.sessions)).FontSize(13).FontWeight(500).TextColor(k.text).
			SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		a.tabDot(c, k, tab)
	})
	if e.Clicked() && !active {
		a.SelectTab(spaceID, tab.ID)
	}
	if e.DoubleClicked() {
		a.SelectTab(spaceID, tab.ID)
		a.StartRename(spaceID, tab.ID)
	}
	e.ContextMenu(func(m *ui.Menu) { a.tabMenu(m, spaceID, tab) })
	e.Drag(tabDrag{Space: spaceID, Tab: tab.ID})
	if dragged, ok := ui.Drop[tabDrag](e); ok && dragged.Tab != tab.ID {
		if dragged.Space == spaceID {
			a.MoveTab(spaceID, dragged.Tab, index)
		} else {
			a.MoveTabToSpace(dragged.Space, dragged.Tab, spaceID)
		}
	}
}

// tabTiles draws the program tiles of a tab's panes, the focused pane's in
// front and up to two others peeking out behind it, as GoRex draws them
// (spec 58, window design). The block is as wide as the front tile plus what
// each tile behind peeks out, and no wider.
func (a *App) tabTiles(c *ui.Context, tab layout.Tab) {
	ids := layout.TabSessions(tab)
	if len(ids) == 0 {
		return
	}
	front := tab.Focus
	if front == "" {
		front = ids[0]
	}
	back := []string{}
	for _, id := range ids {
		if id != front {
			back = append(back, id)
		}
	}
	if len(back) > 2 {
		back = back[:2]
	}
	block := ui.Box(c).Size(tileWidth(tileSize)+tileStep*float32(len(back))+2, tileSize).Shrink(0)
	block.Children(func() {
		// Each tile behind peeks out to the right of the one in front, and
		// is inset from the top and bottom the further back it sits.
		for i := len(back) - 1; i >= 0; i-- {
			inset := 0.8 * float32(i+1)
			programTileBack(c, tileOf(a.sessions[back[i]]), tileSize, inset).
				Absolute().Left(1 + tileStep*float32(i+1)).Top(inset).ZIndex(len(back) - i)
		}
		a.programTile(c, tileOf(a.sessions[front]), tileSize).
			Absolute().Left(1).Top(0).ZIndex(len(back) + 1)
	})
}

// tabDot is the tab's status dot: green while a pane prints, blue when an
// agent finished unseen, orange when one needs the user, and nothing when
// they idle (spec 58, done status).
func (a *App) tabDot(c *ui.Context, k *palette, tab layout.Tab) {
	switch a.tabStatus(tab) {
	case layout.StatusWorking:
		ui.Box(c).Size(7, 7).Radius(4).Shrink(0).Background(k.green)
	case statusDone:
		ui.Box(c).Size(7, 7).Radius(4).Shrink(0).Background(k.blue)
	case layout.StatusBlocked:
		ui.Box(c).Size(7, 7).Radius(4).Shrink(0).Background(k.orange)
	}
}

// tabStatus is what the panes of a tab are doing together: needs you beats
// working beats done beats idle (spec 58, done status).
func (a *App) tabStatus(tab layout.Tab) layout.Status {
	best := layout.StatusIdle
	for _, id := range layout.TabSessions(tab) {
		switch displayStatus(statusOf(a.sessions[id]), a.isDone(id)) {
		case layout.StatusBlocked:
			return layout.StatusBlocked
		case layout.StatusWorking:
			best = layout.StatusWorking
		case statusDone:
			if best != layout.StatusWorking {
				best = statusDone
			}
		}
	}
	return best
}

// renameField edits a tab's title in place: Enter and moving the focus
// away keep it, Escape goes back, and an empty title follows the program
// again (REQ-DW-034).
func (a *App) renameField(c *ui.Context, spaceID string, tab layout.Tab) {
	in := ui.TextInput(c, a.RenameText()).Placeholder(tabLabel(tab, a.sessions)).
		Grow(1).MinWidth(0).Height(22).FontSize(13).AutoFocus()
	switch {
	case in.Shortcut(0, ui.KeyEscape):
		a.EndRename()
	case in.Submitted():
		a.CommitRename(spaceID, tab.ID)
	case in.Focused():
		a.renameFocus = true
	case a.renameFocus:
		a.CommitRename(spaceID, tab.ID) // the focus went elsewhere
	}
}

// tabMenu is a tab's context menu (REQ-DW-034). Renaming opens the same
// dialog the space rows use (spec 58, spaces); a double-click still edits the
// title in place.
func (a *App) tabMenu(m *ui.Menu, spaceID string, tab layout.Tab) {
	if m.Item("Rename tab…").Chosen() {
		a.SelectTab(spaceID, tab.ID)
		a.RenameTabDialog(spaceID, tab.ID)
	}
	others := []layout.Space{}
	for _, sp := range a.layout.Spaces {
		if sp.ID != spaceID {
			others = append(others, sp)
		}
	}
	if len(others) > 0 {
		m.Submenu("Move to space", func(sub *ui.Menu) {
			for _, sp := range others {
				if sub.Item(layout.SpaceLabel(&sp, layoutSessions(a.sessions))).Chosen() {
					a.MoveTabToSpace(spaceID, tab.ID, sp.ID)
				}
			}
		})
	}
	m.Separator()
	if m.Item("Close tab").Chosen() {
		a.CloseTab(spaceID, tab.ID)
	}
}

// hostChip shows the machine the sessions run on: its name and, where the
// system tells it, its chip and memory (spec 58, window design).
func (a *App) hostChip(c *ui.Context, k *palette) {
	name := a.host.Name()
	if name == "" {
		name = "this machine"
	}
	detail := hostDetail(a.host.Chip(), a.host.MemoryGB())
	ui.Row(c).Gap(7).Padding(4, 11, 4, 9).Radius(999).AlignItems(ui.Center).Shrink(0).
		Background(k.white).Border(1, k.panelBorder).Children(func() {
		ui.Box(c).Size(7, 7).Radius(4).Shrink(0).Background(k.green)
		ui.Text(c, name).FontSize(12.5).FontWeight(500).TextColor(k.text).SingleLine()
		if detail != "" {
			ui.Text(c, detail).FontSize(12.5).TextColor(k.muted).SingleLine()
		}
	})
}

// paletteButton opens the command palette (REQ-DW-052). It is a
// placeholder until T5 builds the palette.
func (a *App) paletteButton(c *ui.Context, k *palette) {
	b := ui.Row(c.Key("palette")).Gap(8).Padding(5, 10, 5, 10).Radius(999).AlignItems(ui.Center).Shrink(0).
		Background(k.white).Border(1, k.panelBorder).Cursor(ui.CursorPointer).
		Role(ui.RoleButton).Label("Search sessions, agents, commands")
	b.Children(func() {
		ui.Icon(c, icon("search")).Size(13, 13).TextColor(k.muted)
		ui.Text(c, "Search sessions, agents, commands").FontSize(12.5).TextColor(k.muted).SingleLine()
		ui.Text(c, "⇧⌘P").Font("monospace").FontSize(11.5).TextColor(k.muted2).SingleLine()
	})
	if b.Clicked() {
		a.OpenPalette()
	}
}
