package app

import (
	"math"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/config"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// The limits of the panel's width, in DIPs (spec 58, panel): a drag on its
// right edge stays between them.
const (
	minPanelWidth float32 = 200
	maxPanelWidth float32 = 420
	// renameWidth is the rename dialog's width in DIPs, and
	// renameMaxLength the most that can be typed into it (spec 58, spaces).
	renameWidth     float32 = 400
	renameMaxLength         = 60
)

// moreIcon is the row's "more" button, kept here rather than in icons.go.
// It is drawn in the colour of its text, as the chrome's icons are.
var moreIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/><circle cx="5" cy="12" r="1"/></svg>`))

// panelWidth is the panel's width: the setting, its default when it was never
// set, kept between the limits (spec 58, panel).
func (a *App) panelWidth() float32 {
	w := a.settings.PanelWidth
	if w <= 0 {
		w = config.DefaultPanelWidth
	}
	return min(max(w, minPanelWidth), maxPanelWidth)
}

// setPanelWidth keeps the panel's width in the settings, between the limits
// (spec 58, panel). A drag calls it live; the write follows on its end.
func (a *App) setPanelWidth(w float32) {
	w = min(max(w, minPanelWidth), maxPanelWidth)
	if w == a.settings.PanelWidth {
		return
	}
	a.settings.PanelWidth = w
}

// TogglePanel shows or hides the panel, keeping the choice in the settings
// (spec 58, panel).
func (a *App) TogglePanel() {
	a.settings.PanelHidden = !a.settings.PanelHidden
	a.saveSettings()
	a.invalidate()
}

// panelResizer is the grip on the panel's right edge: dragging it sets the
// panel's width, which the settings keep (spec 58, panel).
func (a *App) panelResizer(c *ui.Context, k *palette) {
	r := ui.Box(c.Key("panel-resizer")).Absolute().Top(0).Bottom(0).Right(0).Width(6).
		Cursor(ui.CursorResizeColumn).Role(ui.RoleSplitter).Label("Resize panel")
	if r.Hovered() || r.Dragging() {
		r.Background(k.dividerActive)
	}
	if dx, _, ok := r.Dragged(); ok {
		a.setPanelWidth(a.panelWidth() + dx)
		a.invalidate()
	}
	if _, _, ended := r.DragEnded(); ended {
		a.saveSettings()
	}
}

// actionsIconButton is a small borderless icon button of the actions' own
// icons, with the face widgets.go gives the chrome's buttons. It carries no
// tooltip: the ⋯ button of a space row opens a menu over its own spot, and a
// tooltip there would pop over that menu (spec 58, spaces). Its Label still
// names it for assistive technology.
func (a *App) actionsIconButton(c *ui.Context, k *palette, key string, svg *ui.SVG, label string) ui.Element {
	b := ui.Box(c.Key(key)).Size(22, 22).Center().Radius(6).Shrink(0).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	switch {
	case b.Pressed():
		b.Background(k.active)
	case b.Hovered():
		b.Background(k.hover)
	}
	col := k.muted2
	if b.Hovered() {
		col = k.text
	}
	b.Children(func() { ui.Icon(c, svg).Size(15, 15).TextColor(col) })
	return b
}

// panelButton is the panel's toggle: the Spaces header's "Hide panel" and
// the title bar's "Show panel" draw the same control, at the same size and
// radius, so the two feel like one button that stays put while the panel
// comes and goes (spec 58, panel).
func (a *App) panelButton(c *ui.Context, k *palette, key, label string) ui.Element {
	b := ui.Box(c.Key(key)).Size(28, 28).Center().Radius(6).Shrink(0).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label).Tooltip(label)
	switch {
	case b.Pressed():
		b.Background(k.active)
	case b.Hovered():
		b.Background(k.hover)
	}
	col := k.muted2
	if b.Hovered() {
		col = k.text
	}
	b.Children(func() { ui.Icon(c, icon("panel")).Size(15, 15).TextColor(col) })
	return b
}

// spaceMoreButton is the ⋯ button of a space row: it opens the same menu as
// the row's right-click (spec 58, spaces). The row builds it always, so its
// menu survives the pointer leaving the row for the menu; it shows, and takes
// the pointer, only while show is set (the row is hovered or active, or its
// menu is open), staying built but invisible otherwise.
func (a *App) spaceMoreButton(c *ui.Context, k *palette, spaceID, label string, show bool) ui.Element {
	b := a.actionsIconButton(c, k, "space-more-"+spaceID, moreIcon, "Space actions: "+label)
	if !show {
		// Invisible, and taking no pointer, so a row at rest shows no
		// button and passes a click at its spot through to itself (spec 58,
		// spaces).
		b.Opacity(0).IgnoreInput(true)
	}
	b.PopoverMenuButton(func(m *ui.Menu) { a.spaceMenu(m, spaceID) },
		ui.PopoverMenuButtonOptions{Open: a.spaceMenuOpen(spaceID)})
	return b
}

// spaceMenuOpen is the ⋯ menu-open state of a space row, made on first use so
// that its button holds the same one across frames, as MyGo's
// PopoverMenuButtonOptions.Open needs: MyGo sets it as the user opens and
// closes the menu, and reads it back so the button stays built while the menu
// shows (spec 58, spaces).
func (a *App) spaceMenuOpen(spaceID string) *bool {
	open, ok := a.spaceMenus[spaceID]
	if !ok {
		open = new(bool)
		a.spaceMenus[spaceID] = open
	}
	return open
}

// spaceMenu is a space's menu (spec 58, spaces): rename it, open a terminal
// in it, or close it and every session it holds. The row's right-click and
// its ⋯ button both open it.
func (a *App) spaceMenu(m *ui.Menu, spaceID string) {
	if m.Item("Rename space…").Chosen() {
		a.RenameSpaceDialog(spaceID)
	}
	if m.Item("New terminal").Chosen() {
		a.NewTerminal(spaceID)
	}
	m.Separator()
	if m.Item("Close space").Danger().Chosen() {
		a.CloseSpace(spaceID)
	}
}

// spaceByID finds a space of the layout, nil when it is not there.
func (a *App) spaceByID(id string) *layout.Space {
	for i := range a.layout.Spaces {
		if a.layout.Spaces[i].ID == id {
			return &a.layout.Spaces[i]
		}
	}
	return nil
}

// CloseSpace closes a space: it kills every session it holds at once, with no
// confirmation, and drops the space from the layout. When it was the active
// one, the next space becomes active (spec 58, spaces; REQ-DW-044).
func (a *App) CloseSpace(spaceID string) {
	sp := a.spaceByID(spaceID)
	if sp == nil {
		return
	}
	ids := layout.SpaceSessions(*sp)
	conn := a.conn
	for _, id := range ids {
		if conn != nil {
			a.async(func() {
				ctx, cancel := a.sessionCtx()
				defer cancel()
				_ = conn.KillSession(ctx, id)
			})
		}
		delete(a.sessions, id)
		delete(a.done, id)
		delete(a.entering, id)
		delete(a.closing, id)
		a.forgetFocus(id)
	}
	wasActive := a.layout.ActiveSpace == spaceID
	a.markLayout(layout.RemoveSpace(a.layout, spaceID))
	if wasActive {
		a.focusReq = a.focusedSession()
	}
}

// renameDialog is the modal that renames a space or a tab (spec 58, spaces):
// one dialog at a time, opened from a row's or a tab's menu.
type renameDialog struct {
	open bool
	// kind is "space" or "tab".
	kind string
	// spaceID, and tabID for a tab, are what the dialog renames.
	spaceID string
	tabID   string
	// title is the dialog's heading, and current the label the space or tab
	// shows now, its muted subtitle.
	title   string
	current string
	// placeholder, auto and hint are the dialog's own text: auto is the
	// space's automatic folder label, shown in the preview while the name
	// is empty.
	placeholder string
	auto        string
	hint        string
	// text is what is typed; it starts as the space's label or the tab's
	// title.
	text string
	// color is the space's colour while it is picked in the dialog, and
	// tabTile the mark a tab's focused session shows.
	color   string
	tabTile tile
	// focus asks the field to take the keyboard focus as the dialog opens.
	focus bool
}

// RenameSpaceDialog opens the rename dialog of a space, prefilled with its
// label and its colour (spec 58, spaces).
func (a *App) RenameSpaceDialog(spaceID string) {
	sp := a.spaceByID(spaceID)
	if sp == nil {
		return
	}
	label := layout.SpaceLabel(sp, layoutSessions(a.sessions))
	color := sp.Color
	if color == "" {
		color = layout.DefaultColor
	}
	a.dialog = renameDialog{
		open:        true,
		kind:        "space",
		spaceID:     spaceID,
		title:       "Rename space",
		current:     label,
		placeholder: a.autoSpaceLabel(sp),
		auto:        a.autoSpaceLabel(sp),
		hint:        "Leave empty to name it after the folder of its active terminal.",
		text:        label,
		color:       color,
		focus:       true,
	}
	a.invalidate()
}

// autoSpaceLabel is the folder label a space takes when it is left to name
// itself: the folder of its active terminal, or "~".
func (a *App) autoSpaceLabel(sp *layout.Space) string {
	auto := *sp
	auto.Auto = true
	return layout.SpaceLabel(&auto, layoutSessions(a.sessions))
}

// RenameTabDialog opens the rename dialog of a tab, prefilled with its title
// (spec 58, spaces).
func (a *App) RenameTabDialog(spaceID, tabID string) {
	tab := findTab(a.layout, spaceID, tabID)
	if tab == nil {
		return
	}
	bare := *tab
	bare.Title = ""
	a.dialog = renameDialog{
		open:        true,
		kind:        "tab",
		spaceID:     spaceID,
		tabID:       tabID,
		title:       "Rename tab",
		current:     tabLabel(*tab, a.sessions),
		placeholder: tabLabel(bare, a.sessions),
		hint:        "Leave empty to use the program's title.",
		text:        tab.Title,
		tabTile:     tileOf(a.sessions[tabFocus(*tab)]),
		focus:       true,
	}
	a.invalidate()
}

// tabFocus is the session a tab's tile stands for: its focused pane, else
// its first one, "" for an empty tab.
func tabFocus(tab layout.Tab) string {
	if tab.Focus != "" {
		return tab.Focus
	}
	if ids := layout.TabSessions(tab); len(ids) > 0 {
		return ids[0]
	}
	return ""
}

// SnapshotDialog opens the rename dialog a development snapshot asks for
// (spec 58, spaces): "rename-space" for the active space, "rename-tab" for
// its active tab.
func (a *App) SnapshotDialog(name string) {
	space := layout.ActiveSpace(a.layout)
	if space == nil {
		return
	}
	switch name {
	case "rename-space":
		a.RenameSpaceDialog(space.ID)
	case "rename-tab":
		if tab := layout.ActiveTab(space); tab != nil {
			a.RenameTabDialog(space.ID, tab.ID)
		}
	}
}

// dialogs draws the app's modals over the window (spec 58, spaces;
// REQ-DW-060).
func (a *App) dialogs(c *ui.Context) {
	if a.dialog.open {
		a.renameModal(c)
	}
	if a.settingsOpen {
		a.settingsModal(c)
	}
}

// renameModal draws the rename dialog (spec 58, spaces): a live preview of
// what is being named, the title with the label it shows now, the name
// field, a hint, the space colours, and Cancel and Save. The panel fades and
// rises in; Enter saves, and Escape or a press on the backdrop cancels.
func (a *App) renameModal(c *ui.Context) {
	d := &a.dialog
	k := a.pal()
	ui.DialogBase(c, &d.open, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop)
		panel.Width(renameWidth).Padding(20).Gap(14).Radius(12).
			Background(k.white).Border(1, k.panelBorder).
			Shadow(0, 12, 32, 0, k.panelShadow).Shadow(0, 1, 2, 0, k.panelShadowSoft)
		// The panel comes in from a pose, built unseen once so that the
		// next frame moves it to its place (spec 58, spaces).
		entered := ui.Local(panel, "entered", func() bool { return false })
		p := ui.Pose{}
		if !*entered {
			p, *entered = ui.Pose{Y: 8, Scale: 0.97, Fade: 1}, true
			c.Invalidate()
		}
		panel.AnimatePose(p, renameEnter)
		panel.Children(func() {
			a.renameHeader(c, d, k)
			a.renameNameField(c, d, k)
			ui.Text(c, d.hint).FontSize(12.5).TextColor(k.muted)
			if d.kind == "space" {
				a.renameColorRow(c, d, k)
			}
			a.renameFooter(c, d)
		})
	})
	d.focus = false
}

// renameHeader is the dialog's first row: the preview tile of what is being
// named, and the title with the label the space or tab shows now (spec 58,
// spaces).
func (a *App) renameHeader(c *ui.Context, d *renameDialog, k *palette) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		a.renamePreview(c, d, k)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, d.title).FontSize(15).FontWeight(600).TextColor(k.text).SingleLine()
			ui.Text(c, d.current).FontSize(12.5).TextColor(k.muted).SingleLine().Ellipsis("…")
		})
	})
}

// renamePreview is the header's tile: a space's 28 px colour tile with the
// first letter of the name being typed, or the mark of a tab's focused
// session (spec 58, spaces).
func (a *App) renamePreview(c *ui.Context, d *renameDialog, k *palette) {
	if d.kind != "space" {
		a.programTile(c, d.tabTile, 28)
		return
	}
	ui.Box(c).Size(28, 28).Radius(8).Center().Shrink(0).Background(spaceColor(d.color)).
		Children(func() {
			ui.Text(c, renameInitial(d)).FontSize(14).FontWeight(600).TextColor(k.onTint).SingleLine()
		})
}

// renameInitial is the letter the preview tile shows: the first of the name
// being typed, else the space's automatic label's (spec 58, spaces).
func renameInitial(d *renameDialog) string {
	label := strings.TrimSpace(d.text)
	if label == "" {
		label = d.auto
	}
	r := []rune(label)
	if len(r) == 0 {
		return "~"
	}
	return strings.ToUpper(string(r[0]))
}

// renameNameField is the dialog's name field (spec 58, spaces): a panel
// input with a hairline border that takes the focus, and the text colour and
// a ring while it has it. The current name is selected as the dialog opens,
// so typing replaces it.
func (a *App) renameNameField(c *ui.Context, d *renameDialog, k *palette) {
	in := ui.TextInputBase(c, &d.text).Height(36).Radius(8).Padding(0, 12).
		FontSize(13.5).Background(k.white).Border(1, k.fieldBorder).
		Placeholder(d.placeholder).PlaceholderColor(k.fieldPlaceholder).
		MaxLength(renameMaxLength).FocusRing(false)
	if d.focus {
		in.AutoFocus().SetTextSelection(0, math.MaxInt)
	}
	if d.focus || in.Focused() {
		in.BorderColor(k.text).Shadow(0, 0, 0, 3, k.ring)
	}
	if in.Submitted() {
		a.commitRename()
	}
}

// renameColorRow is the space dialog's colour row: the seven space colours
// as circles, the chosen one ringed (spec 58, spaces).
func (a *App) renameColorRow(c *ui.Context, d *renameDialog, k *palette) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "Colour").FontSize(12).FontWeight(500).TextColor(k.muted).SingleLine()
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			for _, color := range layout.SpaceColors {
				if a.renameSwatch(c, k, color, d.color == color).Clicked() {
					d.color = color
				}
			}
		})
	})
}

// renameSwatch is one of the colour row's circles: an 18 px disc in a 26 px
// target, the chosen one ringed with its colour and a gap between (spec 58,
// spaces).
func (a *App) renameSwatch(c *ui.Context, k *palette, color string, selected bool) ui.Element {
	b := ui.Box(c).Size(26, 26).Center().Radius(13).Shrink(0).
		Cursor(ui.CursorPointer).Role(ui.RoleRadio).Selected(selected).
		Label("Space colour " + color)
	if selected {
		b.Background(k.white).Border(2, spaceColor(color))
	} else if b.Hovered() {
		b.Background(k.hover)
	}
	b.Children(func() {
		ui.Box(c).Size(18, 18).Radius(9).Shrink(0).Background(spaceColor(color))
	})
	return b
}

// renameFooter is the dialog's last row: Cancel and Save, with the keys
// that press them shown inside (spec 58, spaces).
func (a *App) renameFooter(c *ui.Context, d *renameDialog) {
	ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
		if a.dialogButton(c, "Cancel", "esc", false).Clicked() {
			d.open = false
		}
		if a.dialogButton(c, "Save", "↵", true).Clicked() {
			a.commitRename()
		}
	})
}

// commitRename keeps what the rename dialog holds: a space's name and
// colour, or a tab's title. An empty one turns the automatic naming back on,
// or goes back to the program's own title (spec 58, spaces).
func (a *App) commitRename() {
	d := a.dialog
	if !d.open {
		return
	}
	a.dialog.open = false
	switch d.kind {
	case "space":
		l := layout.RenameSpace(a.layout, d.spaceID, d.text)
		a.markLayout(layout.SetSpaceColor(l, d.spaceID, d.color))
	case "tab":
		a.markLayout(layout.RenameTab(a.layout, d.spaceID, d.tabID, d.text))
	}
	a.focusReq = a.focusedSession()
}
