package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// The panes' metrics (spec 58, window design).
const (
	paneGap     = 8
	cardRadius  = 10
	cardHeaderH = 33
	exitedBarH  = 30
)

// The fade of a pane card (spec 58, window design; REQ-DW-044): in as its
// pane is made, and out as it is closed, both as a pose so the frames that
// move it only paint. closeFadeDelay is how long a closing card is left
// showing, a little longer than paneFadeOut, so its fade is seen before its
// session goes; it is a variable so a test can fire the kill at once.
var (
	paneFadeIn     = ui.Animation{Duration: 180 * time.Millisecond, Ease: ui.EaseOut}
	paneFadeOut    = ui.Animation{Duration: 160 * time.Millisecond, Ease: ui.EaseOut}
	closeFadeDelay = 170 * time.Millisecond
)

// workspace draws the active tab's panes, or the state of no terminals
// (REQ-DW-030). It shows the panes of the active tab and hides the rest,
// which is where a hidden pane's terminal is let go (REQ-DW-041). When the
// preview panel is open it sits at the right, beside the panes, with a
// draggable divider between (REQ-DW-081).
func (a *App) workspace(c *ui.Context, k *palette) {
	a.syncTerms()
	row := ui.Row(c).Key("workspace").Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch)
	bounds := row.Bounds()
	row.Children(func() {
		panesGrow := float32(1)
		if a.preview != nil {
			panesGrow = 1 - a.previewShareClamped()
		}
		ui.Box(c).Grow(panesGrow).Basis(0).MinWidth(0).MinHeight(0).Padding(workspacePad).Children(func() {
			a.workspacePanes(c, k)
		})
		if a.preview != nil {
			a.previewDivider(c, k, bounds)
			a.previewPanel(c, k, a.previewShareClamped())
		}
	})
}

// workspacePanes draws the panes of the active tab, or the state of no
// terminals (REQ-DW-030).
func (a *App) workspacePanes(c *ui.Context, k *palette) {
	space := layout.ActiveSpace(a.layout)
	tab := layout.ActiveTab(space)
	if space == nil || tab == nil {
		a.emptyWorkspace(c, k)
		return
	}
	if tab.Zoom != "" {
		a.paneCard(c, k, space.ID, tab.ID, *tab, tab.Zoom, true).Grow(1)
		return
	}
	a.paneTree(c, k, space.ID, tab.ID, *tab, tab.Root, nil, true)
}

// emptyWorkspace is the state of a window with no terminals: a New
// terminal button (spec 58, empty states).
func (a *App) emptyWorkspace(c *ui.Context, k *palette) {
	ui.Box(c).Grow(1).MinWidth(0).MinHeight(0).Center().Children(func() {
		ui.Column(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "No terminals open").FontSize(14).TextColor(k.muted)
			if ui.Button(c, "New terminal").Clicked() {
				a.NewSpace()
			}
		})
	})
}

// paneTree lays out a node of a tab's tree: a pane card, or a split of
// them with draggable dividers (REQ-DW-032).
func (a *App) paneTree(c *ui.Context, k *palette, spaceID, tabID string, tab layout.Tab, n layout.Node, path []int, root bool) ui.Element {
	if n.Type != "split" || len(n.Children) == 0 {
		e := a.paneCard(c, k, spaceID, tabID, tab, n.Session, tab.Focus == n.Session)
		if root {
			e.Grow(1)
		}
		return e
	}
	var box ui.Element
	if n.Dir == "col" {
		box = ui.Column(c.Key(splitKey(path)))
	} else {
		box = ui.Row(c.Key(splitKey(path)))
	}
	box.MinWidth(0).MinHeight(0).AlignItems(ui.Stretch)
	if root {
		box.Grow(1)
	}
	bounds := box.Bounds()
	sizes := layout.SplitSizes(n)
	children := n.Children
	box.Children(func() {
		for i := range children {
			if i > 0 {
				a.divider(c, k, spaceID, tabID, path, sizes, i-1, n.Dir, bounds)
			}
			child := a.paneTree(c, k, spaceID, tabID, tab, children[i], append(append([]int{}, path...), i), false)
			share := 1 / float32(len(children))
			if i < len(sizes) {
				share = float32(sizes[i])
			}
			child.Grow(share).Basis(0).MinWidth(0).MinHeight(0)
		}
	})
	return box
}

// splitKey names a split for the element tree.
func splitKey(path []int) string {
	if len(path) == 0 {
		return "split-root"
	}
	return fmt.Sprintf("split-%v", path)
}

// divider is the draggable gap beside a child of a split (REQ-DW-032):
// dragging it moves the divider live, and the write follows at the end of
// the drag (REQ-DW-023).
func (a *App) divider(c *ui.Context, k *palette, spaceID, tabID string, path []int, sizes []float64, j int, dir string, bounds ui.Rect) {
	div := ui.Box(c.Key(fmt.Sprintf("div-%v-%d", path, j))).Role(ui.RoleSplitter).Label("Divider")
	if dir == "col" {
		div.Height(paneGap).Cursor(ui.CursorResizeRow)
	} else {
		div.Width(paneGap).Cursor(ui.CursorResizeColumn)
	}
	if dx, dy, ok := div.Dragged(); ok {
		total := float64(bounds.W) - paneGap*float64(len(sizes)-1)
		delta := float64(dx)
		if dir == "col" {
			total = float64(bounds.H) - paneGap*float64(len(sizes)-1)
			delta = float64(dy)
		}
		if total > 0 {
			a.layout = layout.SetSizes(a.layout, spaceID, tabID, path, shiftShare(sizes, j, delta/total))
			a.invalidate()
		}
	}
	if _, _, ended := div.DragEnded(); ended {
		a.markLayout(a.layout)
	}
	if div.DoubleClicked() {
		a.markLayout(layout.Equalize(a.layout, spaceID, tabID))
	}
	if div.Hovered() || div.Dragging() {
		div.Background(k.dividerActive)
	}
}

// paneCard draws a pane: a card whose header names the program and whose
// body is the session's live terminal (REQ-DW-031, REQ-DW-036). The pane
// with the focus shows by a brighter card, not by a border, and the change
// eases in (REQ-DW-040). A click on it focuses the pane and gives its
// terminal the keyboard focus. A card fades in as its pane is made and out
// as it is closed (spec 58, window design; REQ-DW-044).
func (a *App) paneCard(c *ui.Context, k *palette, spaceID, tabID string, tab layout.Tab, sessionID string, focused bool) ui.Element {
	s := a.sessions[sessionID]
	card := ui.Column(c.Key("card-" + sessionID)).Radius(cardRadius).Clip().MinWidth(0).MinHeight(0)
	// A closing card shows faded out and takes no clicks until its session
	// goes; an entering one shows faded out for one frame, then is posed at
	// rest, which moves it in. A card that was never marked is posed at rest
	// every frame, so it builds no pose and never fades.
	closing := a.closing[sessionID]
	switch {
	case closing:
		card.AnimatePose(ui.Pose{Fade: 1, Scale: 0.98}, paneFadeOut)
		card.IgnoreInput(true)
	case a.entering[sessionID]:
		delete(a.entering, sessionID)
		card.AnimatePose(ui.Pose{Fade: 1, Scale: 0.98}, paneFadeIn)
		c.Invalidate()
	default:
		card.AnimatePose(ui.Pose{}, paneFadeIn)
	}
	bg, border, shadow := k.card, k.cardBorder, k.shadow
	if focused {
		bg, border, shadow = k.cardFocused, k.cardBorderFocused, k.shadowFocused
	}
	card.Background(bg).Border(1, border).
		Shadow(0, 1, 2, 0, shadow).
		Shadow(0, 6, 22, -2, shadow)
	card.Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
	hovered := card.Hovered()
	// A drop lands on the innermost element that takes files: the terminal
	// body takes its own (term.go), and this card takes the ones that fall on
	// its header or its exited bar (REQ-DW-043).
	if files := card.DroppedFiles(); len(files) > 0 {
		a.dropFilesOnPane(sessionID, files)
	}
	overFiles := card.FileDragOver()
	card.Children(func() {
		a.paneHeader(c, k, sessionID, s, tab.Zoom == sessionID, focused, hovered)
		if a.terminalBody(c, k, sessionID, focused) {
			overFiles = true
		}
		if s.State == "exited" {
			a.exitedBar(c, k, sessionID, s)
		}
	})
	// A pane the files dragged from Finder are over shows a subtle outline, so
	// it is clear which one would take them (REQ-DW-043).
	if overFiles {
		card.Border(1, k.dropBorder)
	}
	// A press focuses the pane at once, so the highlight moves on mouse down
	// as GoRex's does; a click keeps focusing it. A closing card takes no
	// clicks at all (REQ-DW-044).
	if !closing {
		if card.Pressed() && !focused {
			a.FocusPane(sessionID)
			a.focusReq = sessionID
		}
		if card.Clicked() {
			a.FocusPane(sessionID)
			a.focusReq = sessionID
		}
	}
	return card
}

// terminalBody draws a pane's terminal over its inner padding (REQ-DW-036),
// with a "reconnecting…" line while its stream is away (REQ-DW-041). An
// unfocused pane's terminal is a little dimmer than a focused one, which
// helps the focused card stand out (REQ-DW-040). It reports whether files
// dragged from Finder are over the terminal, which takes its own drops and
// which the card outlines (REQ-DW-043).
func (a *App) terminalBody(c *ui.Context, k *palette, sessionID string, focused bool) bool {
	pt := a.termOf(sessionID)
	pt.show()
	body := ui.Box(c).Grow(1).MinHeight(0).Padding(termPadTop, termPadSide, termPadBottom, termPadSide)
	over := false
	body.Children(func() {
		if term := pt.emulator(); term != nil {
			tv := term.View(c).Fill()
			over = tv.FileDragOver()
			if !focused {
				tv = tv.Opacity(0.62)
			}
			// A closing pane's terminal must not take the keyboard focus
			// while its card fades out (REQ-DW-044).
			if !a.closing[sessionID] {
				if a.focusReq == sessionID {
					tv.Focus()
					a.focusReq = ""
				}
				// The pane follows its terminal's keyboard focus, so a pane
				// focused by the terminal itself (a click in it, say) becomes
				// the focused pane, as GoRex's does (REQ-DW-040).
				// A pending focus request wins: the old pane's terminal still
				// holds the keyboard for a frame after another pane is pressed.
				if tv.Focused() && !focused && a.focusReq == "" {
					a.FocusPane(sessionID)
				}
			}
			return
		}
		if pt.isReconnecting() {
			ui.Box(c).Fill().Center().Children(func() {
				ui.Text(c, "reconnecting…").FontSize(11).TextColor(k.headerMuted).SingleLine()
			})
		}
	})
	return over
}

// paneHeader is a pane's program tile, its program or agent name, the
// session's title, its directory, its Lana chip and its status and buttons
// (REQ-DW-031). The program name is bold, the session title and the directory
// follow in the header's dimmer text; the title is the first to ellipsize
// when the room runs short. It sits on the card's own colour: no bar of its
// own and no divider under it, as GoRex's header. A double click toggles the
// zoom.
func (a *App) paneHeader(c *ui.Context, k *palette, sessionID string, s lana.Session, zoomed, focused, hovered bool) {
	h := ui.Row(c).Height(cardHeaderH).Padding(0, 8, 0, 12).Gap(7).AlignItems(ui.Center).MinWidth(0)
	if h.DoubleClicked() {
		a.ToggleZoom(sessionID)
	}
	h.Children(func() {
		a.programTile(c, tileOf(s), 18)
		program := programName(s)
		ui.Text(c, program).FontSize(12.5).FontWeight(600).TextColor(k.headerText).SingleLine().Shrink(0)
		if title := sessionTitle(s); title != program {
			ui.Text(c, title).FontSize(12.5).TextColor(k.headerText.Alpha(0.86)).
				SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		}
		if cwd := displayCwd(sessionCwd(s), a.userHome); cwd != "" {
			ui.Text(c, cwd).FontSize(12.5).TextColor(k.headerMuted).
				SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		}
		a.lanaChip(c, k, s)
		ui.Spacer(c)
		a.statusBadge(c, k, s)
		a.headerButtons(c, k, sessionID, zoomed, focused || hovered)
	})
}

// lanaChip is the pill a pane's header shows when Lana runs the session
// (spec 58, window design): the agent's avatar circle and "agent · task", as
// the web Workbench's LanaChip.
func (a *App) lanaChip(c *ui.Context, k *palette, s lana.Session) {
	agent := agentLabel(s)
	if agent == "" {
		return
	}
	label := agent
	if task := strings.TrimSpace(s.Labels["lana.task"]); task != "" {
		label = agent + " · " + task
	}
	initial := "?"
	if r := []rune(agent); len(r) > 0 {
		initial = strings.ToUpper(string(r[0]))
	}
	ui.Row(c).Gap(5).Padding(2, 8, 2, 3).Radius(999).AlignItems(ui.Center).Shrink(0).
		Background(k.lanaChipBG).Children(func() {
		ui.Box(c).Size(16, 16).Radius(8).Center().Shrink(0).Background(hashColor(agent)).Children(func() {
			ui.Text(c, initial).FontSize(9.5).FontWeight(600).TextColor(k.lanaText)
		})
		ui.Text(c, label).FontSize(11.5).FontWeight(500).TextColor(k.lanaChipText).SingleLine()
	})
}

// statusBadge is what a session is doing, with its dot (REQ-DW-031). A done
// agent shows "done" in blue (spec 58, done status).
func (a *App) statusBadge(c *ui.Context, k *palette, s lana.Session) {
	done := a.isDone(s.ID)
	col := k.grey
	switch displayStatus(statusOf(s), done) {
	case layout.StatusWorking:
		col = k.green
	case statusDone:
		col = k.blue
	case layout.StatusBlocked, layout.StatusExited:
		col = k.orange
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Box(c).Size(7, 7).Radius(4).Shrink(0).Background(col)
		ui.Text(c, statusText(s, done)).FontSize(12).TextColor(col).SingleLine()
	})
}

// headerButtons are a pane's split, zoom and close buttons (REQ-DW-031).
// They show only while the pane is focused or hovered, and stay hidden,
// faded out, otherwise, as GoRex's do.
func (a *App) headerButtons(c *ui.Context, k *palette, sessionID string, zoomed, show bool) {
	opacity := float32(0)
	if show {
		opacity = 1
	}
	ui.Row(c).Gap(1).AlignItems(ui.Center).Shrink(0).Opacity(opacity).Children(func() {
		if !show {
			return
		}
		if a.headerIconButton(c, k, "split-right-"+sessionID, "splitR", "Split Right").Clicked() {
			a.SplitPane(sessionID, "row")
		}
		if a.headerIconButton(c, k, "split-down-"+sessionID, "splitD", "Split Down").Clicked() {
			a.SplitPane(sessionID, "col")
		}
		name, label := "zoom", "Zoom"
		if zoomed {
			name, label = "unzoom", "Unzoom"
		}
		if a.headerIconButton(c, k, "zoom-"+sessionID, name, label).Clicked() {
			a.ToggleZoom(sessionID)
		}
		if a.headerIconButton(c, k, "close-pane-"+sessionID, "close", "Close Pane").Clicked() {
			a.ClosePane(sessionID)
		}
	})
}

// exitedBar is the thin row under a session that has ended: what it exited
// with, and Restart and Close (REQ-DW-033).
func (a *App) exitedBar(c *ui.Context, k *palette, sessionID string, s lana.Session) {
	ui.Row(c).Height(exitedBarH).Padding(0, 12, 8, 12).Gap(7).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(7, 7).Radius(4).Shrink(0).Background(k.orange)
		ui.Textf(c, "exited %d · %s ago", s.ExitCode, ageFormat(a.now(), exitTime(s))).
			FontSize(12).TextColor(k.orange).SingleLine().Grow(1).MinWidth(0)
		if a.ghostButton(c, k, "restart-"+sessionID, "Restart").Clicked() {
			a.Restart(sessionID)
		}
		if a.ghostButton(c, k, "close-exited-"+sessionID, "Close").Clicked() {
			a.ClosePane(sessionID)
		}
	})
}

// ghostButton is a small, quiet button of the exited bar.
func (a *App) ghostButton(c *ui.Context, k *palette, key, label string) ui.Element {
	b := ui.Box(c.Key(key)).PaddingX(8).PaddingY(3).Radius(6).Shrink(0).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	if b.Hovered() {
		b.Background(k.hoverDark)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(500).TextColor(k.headerMuted).SingleLine()
	})
	return b
}

// exitTime is when a session ended, as far as its output tells: the last
// output it made, else when it started.
func exitTime(s lana.Session) time.Time {
	if !s.LastOutput.IsZero() {
		return s.LastOutput
	}
	return s.Created
}
