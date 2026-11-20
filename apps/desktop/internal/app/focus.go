package app

import (
	"slices"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// recentFocusMax bounds a tab's focus history: the most recent panes it
// remembers, so closing one can give the focus back to the pane the user used
// before it (spec 58, window design).
const recentFocusMax = 32

// noteFocus records the focused pane of every tab, so closing a pane gives the
// focus back to the pane the user used before it rather than to the tab's
// first one (spec 58, window design). The history is kept per tab, in memory
// alone.
func (a *App) noteFocus() {
	for si := range a.layout.Spaces {
		for ti := range a.layout.Spaces[si].Tabs {
			t := a.layout.Spaces[si].Tabs[ti]
			a.noteTabFocus(t.ID, t.Focus)
		}
	}
}

// noteTabFocus moves a session to the end of its tab's focus history, dropping
// an earlier entry for it, and keeps the history to its most recent
// recentFocusMax entries.
func (a *App) noteTabFocus(tabID, sessionID string) {
	if tabID == "" || sessionID == "" {
		return
	}
	if a.recent == nil {
		a.recent = map[string][]string{}
	}
	ids := a.recent[tabID]
	ids = slices.DeleteFunc(ids, func(id string) bool { return id == sessionID })
	ids = append(ids, sessionID)
	if len(ids) > recentFocusMax {
		ids = ids[len(ids)-recentFocusMax:]
	}
	a.recent[tabID] = ids
}

// forgetFocus drops a session from every tab's focus history, as when it is
// closed.
func (a *App) forgetFocus(sessionID string) {
	if sessionID == "" {
		return
	}
	for tabID, ids := range a.recent {
		kept := slices.DeleteFunc(ids, func(id string) bool { return id == sessionID })
		if len(kept) == 0 {
			delete(a.recent, tabID)
			continue
		}
		a.recent[tabID] = kept
	}
}

// nextFocus picks the pane a tab's focus goes to when one of its panes is
// closed: the most recent pane of the tab's history that is still open, else
// the pane next to the closed one in tree order, the one before it first (spec
// 58, window design). remaining lists the tab's panes in tree order, the
// closed one included; closing and the panes of a.closing are never chosen.
func (a *App) nextFocus(tabID, closing string, remaining []string) string {
	recent := a.recent[tabID]
	for i := len(recent) - 1; i >= 0; i-- {
		id := recent[i]
		if id == closing || a.closing[id] {
			continue
		}
		if slices.Contains(remaining, id) {
			return id
		}
	}
	at := slices.Index(remaining, closing)
	if at < 0 {
		return ""
	}
	for i := at - 1; i >= 0; i-- {
		if id := remaining[i]; id != closing && !a.closing[id] {
			return id
		}
	}
	for i := at + 1; i < len(remaining); i++ {
		if id := remaining[i]; id != closing && !a.closing[id] {
			return id
		}
	}
	return ""
}

// focusOffClosing moves a tab's focus off a closing pane at once, before its
// card fades and its session is killed, so it goes to the pane the user used
// before it instead of waiting for the kill round-trip and jumping to the
// tab's first pane (spec 58, window design; REQ-DW-044). A pane that is not
// its tab's focus leaves the focus where it is.
func (a *App) focusOffClosing(sessionID string) {
	space, tab, ok := layout.Locate(a.layout, sessionID)
	if !ok || tab.Focus != sessionID {
		return
	}
	next := a.nextFocus(tab.ID, sessionID, layout.TabSessions(*tab))
	if next == "" {
		return
	}
	l := layout.FocusPane(a.layout, space.ID, tab.ID, next)
	if activeTabIs(l, space.ID, tab.ID) {
		a.focusReq = next
	}
	a.markLayout(l)
}

// dropSession returns the layout with a session removed, giving each tab that
// held it as its focus a pane from the tab's history rather than the first
// pane RemoveSession picks, and asks for the keyboard focus when that tab is
// the one on show (spec 58, window design; REQ-DW-044).
func (a *App) dropSession(sessionID string) layout.Layout {
	_, tab, ok := layout.Locate(a.layout, sessionID)
	next := ""
	if ok && tab.Focus == sessionID {
		next = a.nextFocus(tab.ID, sessionID, layout.TabSessions(*tab))
	}
	l := layout.RemoveSession(a.layout, sessionID)
	if next != "" {
		if s, t, found := layout.Locate(l, next); found {
			l = layout.FocusPane(l, s.ID, t.ID, next)
			if activeTabIs(l, s.ID, t.ID) {
				a.focusReq = next
			}
		}
	}
	a.forgetFocus(sessionID)
	return l
}

// activeTabIs reports whether a tab is the one the window shows: the active
// tab of the active space.
func activeTabIs(l layout.Layout, spaceID, tabID string) bool {
	space := layout.ActiveSpace(l)
	if space == nil || space.ID != spaceID {
		return false
	}
	tab := layout.ActiveTab(space)
	return tab != nil && tab.ID == tabID
}
