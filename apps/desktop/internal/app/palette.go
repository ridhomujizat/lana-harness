package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// The command palette's metrics, from the design sheet (spec 58, "MyGo ·
// Workbench · command palette"): a white panel over the dimmed window.
const (
	paletteWidth   float32 = 470
	paletteMaxList         = 360
	paletteRowH    float32 = 32
	paletteHeaderH float32 = 26
	paletteRowPad          = 8
)

// The palette's sections, in the order they show (REQ-DW-052).
const (
	sectionSessions = "SESSIONS"
	sectionSpaces   = "SPACES"
	sectionCommands = "COMMANDS"
)

// paletteItem is a row of the command palette: a pane to go to, a space to
// show, or a command to run (REQ-DW-052).
type paletteItem struct {
	section string
	title   string
	sub     string
	keys    string
	status  string
	tile    tile
	color   string
	run     func()
}

// OpenPalette opens the command palette (REQ-DW-052).
func (a *App) OpenPalette() {
	a.paletteOpen, a.paletteQuery, a.paletteSel = true, "", 0
	a.focusReq = "" // the palette takes the keyboard focus
}

// paletteItems are the palette's rows for the current query: every pane,
// every space and every visible command, fuzzy-matched and ranked
// (REQ-DW-052).
func (a *App) paletteItems() []paletteItem {
	q := strings.TrimSpace(a.paletteQuery)
	var items []paletteItem
	items = append(items, a.paletteSessions(q)...)
	items = append(items, a.paletteSpaces(q)...)
	items = append(items, a.paletteCommands(q)...)
	return items
}

// paletteSessions are the rows of every pane, in layout order (REQ-DW-052).
func (a *App) paletteSessions(q string) []paletteItem {
	var matches []paletteMatch
	for si := range a.layout.Spaces {
		space := &a.layout.Spaces[si]
		label := layout.SpaceLabel(space, layoutSessions(a.sessions))
		for ti := range space.Tabs {
			tab := &space.Tabs[ti]
			for _, id := range layout.TabSessions(*tab) {
				s := a.sessions[id]
				title := sessionTitle(s) + " — " + label
				match := strings.Join([]string{title, paneTitle(s), programName(s), programLabel(s), sessionCwd(s), label, fmt.Sprintf("tab %d", ti+1)}, " ")
				score, ok := fuzzyScore(match, q)
				if !ok {
					continue
				}
				sessionID := id
				matches = append(matches, paletteMatch{score: score, item: paletteItem{
					section: sectionSessions,
					title:   title,
					sub:     sessionSub(sessionCwd(s), a.userHome, ti+1),
					status:  statusText(s, a.isDone(id)),
					tile:    tileOf(s),
					run:     func() { a.jumpToPane(sessionID) },
				}})
			}
		}
	}
	return rankedItems(matches)
}

// paletteSpaces are the rows of every space (REQ-DW-052).
func (a *App) paletteSpaces(q string) []paletteItem {
	var matches []paletteMatch
	for i := range a.layout.Spaces {
		space := &a.layout.Spaces[i]
		label := layout.SpaceLabel(space, layoutSessions(a.sessions))
		score, ok := fuzzyScore(label+" "+space.Dir, q)
		if !ok {
			continue
		}
		spaceID := space.ID
		matches = append(matches, paletteMatch{score: score, item: paletteItem{
			section: sectionSpaces,
			title:   label,
			sub:     tabCount(len(space.Tabs)),
			color:   space.Color,
			run:     func() { a.SelectSpace(spaceID) },
		}})
	}
	return rankedItems(matches)
}

// paletteCommands are the rows of the visible commands (REQ-DW-052).
func (a *App) paletteCommands(q string) []paletteItem {
	var matches []paletteMatch
	for _, cmd := range paletteCommands {
		if cmd.Hidden {
			continue
		}
		score, ok := fuzzyScore(cmd.Title, q)
		if !ok {
			continue
		}
		cmd := cmd
		matches = append(matches, paletteMatch{score: score, item: paletteItem{
			section: sectionCommands,
			title:   cmd.Title,
			keys:    cmd.keys(),
			run:     func() { cmd.Run(a) },
		}})
	}
	return rankedItems(matches)
}

// sessionSub is a session row's second line: the folder and the tab's
// number.
func sessionSub(cwd, userHome string, tab int) string {
	parts := []string{}
	if dir := displayCwd(cwd, userHome); dir != "" {
		parts = append(parts, dir)
	}
	return strings.Join(append(parts, fmt.Sprintf("tab %d", tab)), " · ")
}

// tabCount names how many tabs a space has.
func tabCount(n int) string {
	if n == 1 {
		return "1 tab"
	}
	return fmt.Sprintf("%d tabs", n)
}

// jumpToPane shows a pane: its space, its tab, and its pane focused
// (REQ-DW-052).
func (a *App) jumpToPane(sessionID string) {
	space, tab, ok := layout.Locate(a.layout, sessionID)
	if !ok {
		return
	}
	l := layout.SelectSpace(a.layout, space.ID)
	l = layout.SelectTab(l, space.ID, tab.ID)
	l = layout.FocusPane(l, space.ID, tab.ID, sessionID)
	a.markLayout(l)
	a.focusReq = sessionID
	// A palette jump focuses the pane, so it stops being done (spec 58, done
	// status).
	a.clearFocusedDone()
}

// paletteMatch is a palette row and its fuzzy score, before ranking.
type paletteMatch struct {
	score int
	item  paletteItem
}

// rankedItems sorts a section's matches best first, keeping the layout
// order of equal scores.
func rankedItems(matches []paletteMatch) []paletteItem {
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	out := make([]paletteItem, len(matches))
	for i := range matches {
		out[i] = matches[i].item
	}
	return out
}

// fuzzyScore ranks how well query matches text (REQ-DW-052): a prefix
// ranks above a match at the start of a word, that above one anywhere
// inside, and that above the letters only found in order. A higher score
// is a better match; ok is false when not every letter is there.
func fuzzyScore(text, query string) (score int, ok bool) {
	t := strings.ToLower(text)
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 0, true
	}
	if !subsequence(t, q) {
		return 0, false
	}
	if strings.HasPrefix(t, q) {
		return 4000 - len(t), true
	}
	if wordStart(t, q) >= 0 {
		return 3000 - len(t), true
	}
	if strings.Contains(t, q) {
		return 2000 - len(t), true
	}
	return 1000 - len(t), true
}

// wordStart is where q first stands at the start of a word in t, -1 when
// it never does.
func wordStart(t, q string) int {
	from := 0
	for from <= len(t)-len(q) {
		i := strings.Index(t[from:], q)
		if i < 0 {
			return -1
		}
		i += from
		if i == 0 || !isWordByte(t[i-1]) {
			return i
		}
		from = i + 1
	}
	return -1
}

// isWordByte reports whether a byte is part of a word, as the letters and
// digits of a title are.
func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// subsequence reports whether the bytes of q appear in t, in order.
func subsequence(t, q string) bool {
	i := 0
	for j := 0; j < len(t) && i < len(q); j++ {
		if t[j] == q[i] {
			i++
		}
	}
	return i == len(q)
}

// commandPalette draws the command palette over the window (REQ-DW-052).
func (a *App) commandPalette(c *ui.Context) {
	if !a.paletteOpen {
		return
	}
	items := a.paletteItems()
	a.paletteSel = min(max(a.paletteSel, 0), max(len(items)-1, 0))
	was := a.paletteOpen
	k := a.pal()
	// reveal scrolls the selection into view in the frame the query or the
	// arrows move it, and only then, so the wheel scrolls the list.
	reveal := false
	ui.DialogBase(c, &a.paletteOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop)
		panel.Width(paletteWidth).Radius(12).Background(k.white).Border(1, k.panelBorder).
			Shadow(0, 24, 60, -8, k.panelShadow).Shadow(0, 2, 6, 0, k.panelShadowSoft).Clip()
		ui.Row(c).Padding(11, 14).Gap(10).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(k.panelBorder).Children(func() {
			ui.Icon(c, icon("search")).Size(15, 15).TextColor(k.muted)
			in := ui.TextInputBase(c, &a.paletteQuery).Grow(1).FontSize(14).
				Placeholder("Search sessions, agents, commands").AutoFocus()
			if in.Changed() {
				a.paletteSel, reveal = 0, true
			}
			if in.Shortcut(0, ui.KeyDown) {
				a.paletteSel, reveal = min(a.paletteSel+1, max(len(items)-1, 0)), true
			}
			if in.Shortcut(0, ui.KeyUp) {
				a.paletteSel, reveal = max(a.paletteSel-1, 0), true
			}
			if in.Submitted() && len(items) > 0 {
				a.paletteOpen = false
				items[a.paletteSel].run()
			}
		})
		ui.Scroll(c).Height(paletteListHeight(items)).Padding(6).Children(func() {
			if len(items) == 0 {
				ui.Text(c, "No matches").FontSize(13).TextColor(k.muted).Padding(14)
				return
			}
			section := ""
			for i, it := range items {
				if it.section != section {
					section = it.section
					ui.Text(c, it.section).FontSize(11).FontWeight(600).TextColor(k.muted).
						Padding(9, 10, 3, 10).SingleLine()
				}
				a.paletteRow(c, k, i, it, i == a.paletteSel, reveal)
			}
		})
		ui.Row(c).Height(30).Padding(0, 14).AlignItems(ui.Center).
			BorderWidth(1, 0, 0, 0).BorderColor(k.panelBorder).Children(func() {
			ui.Text(c, "↑↓ move · enter open").FontSize(11.5).TextColor(k.muted).SingleLine()
		})
	})
	if was && !a.paletteOpen && a.focusReq == "" {
		a.focusReq = a.focusedSession()
	}
}

// paletteListHeight is the room the list takes: its rows and headers, up
// to the design's cap.
func paletteListHeight(items []paletteItem) float32 {
	h := 12
	section := ""
	for _, it := range items {
		h += int(paletteRowH)
		if it.section != section {
			section = it.section
			h += int(paletteHeaderH)
		}
	}
	return float32(min(h, paletteMaxList))
}

// paletteRow draws one palette row.
func (a *App) paletteRow(c *ui.Context, k *palette, i int, it paletteItem, selected, reveal bool) {
	row := ui.Row(c.Key(fmt.Sprintf("palette-%d", i))).Height(paletteRowH).
		Padding(0, paletteRowPad).Gap(9).Radius(7).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if selected {
		row.Background(k.sel)
		if reveal {
			row.ScrollIntoView()
		}
	} else if row.Hovered() {
		row.Background(k.softHover)
	}
	if row.Clicked() {
		a.paletteOpen = false
		it.run()
	}
	row.Children(func() {
		switch it.section {
		case sectionSessions:
			a.programTile(c, it.tile, 18)
		case sectionSpaces:
			a.colorTile(c, it.color, it.title)
		default:
			ui.Box(c).Size(18, 18).Center().Shrink(0).Children(func() {
				ui.Icon(c, icon("command")).Size(14, 14).TextColor(k.muted)
			})
		}
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, it.title).FontSize(13).FontWeight(500).TextColor(k.text).
				SingleLine().Ellipsis("…")
			if it.sub != "" {
				ui.Text(c, it.sub).FontSize(11.5).TextColor(k.muted).SingleLine().Ellipsis("…")
			}
		})
		if it.status != "" {
			ui.Text(c, it.status).FontSize(11.5).TextColor(k.muted).Shrink(0).SingleLine()
		}
		if it.keys != "" {
			ui.Text(c, it.keys).FontSize(11.5).TextColor(k.muted2).Padding(2, 6).Radius(5).
				Background(k.keycap).Shrink(0).SingleLine()
		}
		if selected && it.section == sectionSessions {
			ui.Text(c, "↵").FontSize(12).TextColor(k.muted).Shrink(0).SingleLine()
		}
	})
}
