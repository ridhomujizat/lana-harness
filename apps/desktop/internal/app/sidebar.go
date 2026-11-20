package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// sidebar is the panel on the left: Spaces over Agents, split by a
// hairline (REQ-DW-030). Its width follows the settings, its right edge
// drags, and a hidden panel draws nothing, so the workspace takes the room
// (spec 58, panel).
func (a *App) sidebar(c *ui.Context, k *palette) {
	if a.settings.PanelHidden {
		return
	}
	ui.Column(c).Key("panel").Width(a.panelWidth()).Shrink(0).MinWidth(0).MinHeight(0).
		Background(k.window).BorderWidth(0, 1, 0, 0).BorderColor(k.hairline).Children(func() {
		a.spacesPanel(c, k)
		ui.Box(c).Height(1).Background(k.hairline)
		a.agentsPanel(c, k)
		a.panelResizer(c, k)
	})
}

// spacesPanel lists the spaces, with the "+" that makes one and the button
// that hides the panel (REQ-DW-030; spec 58, panel).
func (a *App) spacesPanel(c *ui.Context, k *palette) {
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Height(34).Padding(0, 8, 0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Spaces").FontSize(13).TextColor(k.muted).Grow(1)
			if a.iconButton(c, k, "new-space-panel", "plus", "New space").Clicked() {
				a.NewSpace()
			}
			if a.panelButton(c, k, "hide-panel", "Hide panel").Clicked() {
				a.TogglePanel()
			}
		})
		ui.Scroll(c).Grow(1).Children(func() {
			spaces := a.layout.Spaces
			if len(spaces) == 0 {
				ui.Text(c, "No spaces yet").FontSize(12).TextColor(k.muted).Padding(2, 0, 6, 12)
				return
			}
			for i := range spaces {
				a.spaceRow(c, k, &spaces[i])
			}
		})
	})
}

// spaceRow is one space: its colour tile, its label, and "(detached)" when
// nothing in it is live (REQ-DW-030). Its ⋯ button shows on hover, while it
// is active, and while its menu is open, and, with a right-click, opens the
// space's menu (spec 58, spaces). The button is built for every row, even
// while hidden, so that its menu survives the pointer leaving the row for the
// menu, which MyGo drops while the button that owns the menu is not built.
func (a *App) spaceRow(c *ui.Context, k *palette, sp *layout.Space) {
	active := a.layout.ActiveSpace == sp.ID
	detached := a.spaceDetached(sp)
	label := layout.SpaceLabel(sp, layoutSessions(a.sessions))
	e := ui.Row(c.Key("space-"+sp.ID)).Height(34).Gap(9).Padding(0, 10, 0, 8).Radius(8).
		AlignItems(ui.Center).Role(ui.RoleButton).Label(label)
	switch {
	case active:
		e.Background(k.pill).Border(1, k.pillBorder)
	case e.Hovered():
		e.Background(k.hover)
	}
	showActions := active || e.Hovered() || *a.spaceMenuOpen(sp.ID)
	e.Children(func() {
		a.colorTile(c, sp.Color, label)
		ui.Text(c, label).FontSize(13).TextColor(k.text).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if detached {
			ui.Text(c, "(detached)").FontSize(11.5).TextColor(k.muted).SingleLine().Shrink(0)
		}
		a.spaceMoreButton(c, k, sp.ID, label, showActions)
	})
	if e.Clicked() && !active {
		a.SelectSpace(sp.ID)
	}
	e.PopoverMenu(func(m *ui.Menu) { a.spaceMenu(m, sp.ID) })
}

// spaceDetached reports whether a space has no live session (REQ-DW-030).
func (a *App) spaceDetached(sp *layout.Space) bool {
	for _, id := range layout.SpaceSessions(*sp) {
		if _, ok := a.sessions[id]; ok {
			return false
		}
	}
	return true
}

// agentsPanel lists the sessions that are coding agents, grouped as the
// layout says (REQ-DW-030).
func (a *App) agentsPanel(c *ui.Context, k *palette) {
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		bySpace := a.layout.Grouping != "status"
		ui.Row(c).Height(34).Padding(0, 8, 0, 12).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Agents").FontSize(13).TextColor(k.muted).Grow(1)
			if a.toggleButton(c, k, "group-space", "layers", "Group by space", bySpace).Clicked() {
				a.SetGrouping("space")
			}
			if a.toggleButton(c, k, "group-status", "clock", "Group by status", !bySpace).Clicked() {
				a.SetGrouping("status")
			}
		})
		ui.Scroll(c).Grow(1).Children(func() { a.agentRows(c, k) })
	})
}

// agentRows draws the agents grouped by space, or by status.
func (a *App) agentRows(c *ui.Context, k *palette) {
	agents := a.agents()
	if len(agents) == 0 {
		ui.Text(c, "No agents running").FontSize(12).TextColor(k.muted).Padding(2, 0, 6, 12)
		return
	}
	if a.layout.Grouping == "status" {
		for _, st := range statusGroups {
			group := a.agentsWithStatus(agents, st)
			if len(group) == 0 {
				continue
			}
			a.groupHeader(c, k, statusGroupName(st), len(group))
			for _, s := range group {
				a.agentRow(c, k, s)
			}
		}
		return
	}
	spaces := a.layout.Spaces
	for i := range spaces {
		group := agentsInSpace(spaces[i], agents)
		if len(group) == 0 {
			continue
		}
		a.groupHeader(c, k, layout.SpaceLabel(&spaces[i], layoutSessions(a.sessions)), len(group))
		for _, s := range group {
			a.agentRow(c, k, s)
		}
	}
}

// groupHeader names a group of agents and counts it.
func (a *App) groupHeader(c *ui.Context, k *palette, name string, count int) {
	ui.Row(c).Gap(6).Padding(8, 8, 4, 10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, name).FontSize(11.5).FontWeight(600).TextColor(k.muted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		ui.Text(c, fmt.Sprintf("%d", count)).FontSize(11.5).TextColor(k.muted).Shrink(0)
	})
}

// agentRow is one agent: its tile, its title, its Lana bubble, its age and
// its status dot (REQ-DW-030). The row of the pane with the focus wears the
// same pill as an active space row, so the panel points at what the keyboard
// is on.
func (a *App) agentRow(c *ui.Context, k *palette, s lana.Session) {
	active := a.focusedSession() == s.ID
	e := ui.Row(c.Key("agent-"+s.ID)).Height(30).Gap(8).Padding(0, 8, 0, 8).Radius(7).
		AlignItems(ui.Center).Role(ui.RoleButton).Selected(active).Label(paneTitle(s))
	// The row always keeps a 1px border, transparent unless the row is
	// active, so the pill of the focused row does not shift its content.
	e.Border(1, ui.Transparent)
	switch {
	case active:
		e.Background(k.pill).BorderColor(k.pillBorder)
	case e.Hovered():
		e.Background(k.hover)
	}
	e.Children(func() {
		a.programTile(c, tileOf(s), 18)
		ui.Text(c, paneTitle(s)).FontSize(12.5).TextColor(k.text).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if name := agentLabel(s); name != "" {
			a.lanaBubble(c, k, name)
		}
		ui.Text(c, ageFormat(a.now(), s.Created)).FontSize(11.5).TextColor(k.muted).Shrink(0)
		a.statusDot(c, k, displayStatus(statusOf(s), a.isDone(s.ID)))
	})
	if e.Clicked() {
		a.jumpTo(s.ID)
	}
}

// lanaBubble is the initial of the Lana agent a session carries
// (REQ-DW-030).
func (a *App) lanaBubble(c *ui.Context, k *palette, name string) {
	initial := "?"
	if r := []rune(name); len(r) > 0 {
		initial = strings.ToUpper(string(r[0]))
	}
	ui.Box(c).Size(16, 16).Radius(8).Center().Shrink(0).Background(k.lanaBG).Children(func() {
		ui.Text(c, initial).FontSize(9.5).FontWeight(600).TextColor(k.lanaText)
	})
}

// agents are the agent sessions, in the order the panel shows them.
func (a *App) agents() []lana.Session {
	out := []lana.Session{}
	for _, s := range a.sessions {
		if isAgent(s) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// statusGroups is the order the Agents panel groups agents by status: needs
// you, working, done, idle, then exited (spec 58, done status).
var statusGroups = []layout.Status{
	layout.StatusBlocked, layout.StatusWorking, statusDone, layout.StatusIdle, layout.StatusExited,
}

// agentsWithStatus are the agents doing one thing, the done mark included.
func (a *App) agentsWithStatus(agents []lana.Session, st layout.Status) []lana.Session {
	out := []lana.Session{}
	for _, s := range agents {
		if displayStatus(statusOf(s), a.isDone(s.ID)) == st {
			out = append(out, s)
		}
	}
	return out
}

// agentsInSpace are the agents whose session sits in a space.
func agentsInSpace(sp layout.Space, agents []lana.Session) []lana.Session {
	ids := map[string]bool{}
	for _, id := range layout.SpaceSessions(sp) {
		ids[id] = true
	}
	out := []lana.Session{}
	for _, s := range agents {
		if ids[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

// statusGroupName is a status group's heading.
func statusGroupName(st layout.Status) string {
	switch st {
	case layout.StatusBlocked:
		return "Needs you"
	case layout.StatusWorking:
		return "Working"
	case statusDone:
		return "Done"
	case layout.StatusExited:
		return "Exited"
	}
	return "Idle"
}

// jumpTo shows a session: its space, its tab and its pane (REQ-DW-030).
func (a *App) jumpTo(sessionID string) {
	space, tab, ok := layout.Locate(a.layout, sessionID)
	if !ok {
		return
	}
	l := layout.SelectSpace(a.layout, space.ID)
	l = layout.SelectTab(l, space.ID, tab.ID)
	l = layout.FocusPane(l, space.ID, tab.ID, sessionID)
	a.markLayout(l)
	// Jumping to a done agent's pane focuses it, so it stops being done
	// (spec 58, done status).
	a.clearFocusedDone()
}
