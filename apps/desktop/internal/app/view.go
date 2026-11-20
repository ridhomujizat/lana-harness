package app

import "github.com/egoist/mygo/ui"

// The window's metrics (spec 58, window design).
const (
	titleBarHeight = 46
	sidebarWidth   = 264
	workspacePad   = 8
)

// view draws the window: the title bar with the tab strip of the active
// space, the panel on the left, and the active tab's panes (REQ-DW-030).
func (a *App) view(c *ui.Context) {
	if a.win != nil {
		// Size ownership follows the window's focus (REQ-DW-040).
		a.focusedWin = a.win.IsFocused()
	}
	k := a.pal()
	windowBackground(c.Root(), k)
	ui.Column(c).Fill().Children(func() {
		a.titleBar(c, k)
		if a.online {
			a.body(c, k)
		} else {
			a.notConnected(c, k)
		}
	})
	a.tick(c)
	if a.notice != "" {
		a.noticeBanner(c, k)
	}
}

// body is the panel and the workspace beside it.
func (a *App) body(c *ui.Context, k *palette) {
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		a.sidebar(c, k)
		a.workspace(c, k)
	})
}

// notConnected is the state of REQ-DW-013: what is wrong, and a Retry
// button. The app retries on its own with backoff as well.
func (a *App) notConnected(c *ui.Context, k *palette) {
	reason := a.connErr
	if reason == "" {
		reason = "connecting…"
	}
	ui.Box(c).Grow(1).MinHeight(0).Center().Children(func() {
		ui.Column(c).Width(420).Padding(22).Gap(12).Radius(12).
			Background(k.white).Border(1, k.panelBorder).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Not connected").FontSize(16).FontWeight(600).TextColor(k.text)
			ui.Text(c, reason).FontSize(13).TextColor(k.muted).TextAlign(ui.Center)
			if ui.Button(c, "Retry").Clicked() {
				a.Retry()
			}
		})
	})
}

// noticeBanner shows a transient error over the window.
func (a *App) noticeBanner(c *ui.Context, k *palette) {
	ui.Overlay(c, func() {
		ui.Row(c).Absolute().Bottom(18).Left(0).Right(0).Justify(ui.Center).PassThrough().Children(func() {
			ui.Row(c).Gap(10).Padding(8, 8, 8, 14).Radius(12).AlignItems(ui.Center).
				Background(k.card).Border(1, k.cardBorder).Children(func() {
				ui.Box(c).Size(8, 8).Radius(4).Shrink(0).Background(k.orange)
				ui.Text(c, a.notice).FontSize(12.5).TextColor(k.headerText).MaxWidth(520)
				if a.iconButton(c, k, "notice-close", "close", "Dismiss").Clicked() {
					a.DismissNotice()
				}
			})
		})
	})
}
