package app

import (
	"github.com/egoist/mygo/ui"
)

// previewShareClamped is the panel's width share, its default when it was
// never set and kept between the limits (REQ-DW-081).
func (a *App) previewShareClamped() float32 {
	s := a.previewShare
	if s <= 0 {
		s = defaultPreviewShare
	}
	return min(max(s, minPreviewShare), maxPreviewShare)
}

// previewDivider is the draggable gap between the panes and the panel
// (REQ-DW-081): dragging it sets the panel's width share. It reuses the look
// of the split dividers.
func (a *App) previewDivider(c *ui.Context, k *palette, bounds ui.Rect) {
	div := ui.Box(c.Key("preview-divider")).Width(paneGap).Shrink(0).
		Cursor(ui.CursorResizeColumn).Role(ui.RoleSplitter).Label("Preview divider")
	if dx, _, ok := div.Dragged(); ok {
		total := float64(bounds.W) - paneGap
		if total > 0 {
			a.previewShare = min(max(a.previewShare-float32(float64(dx)/total), minPreviewShare), maxPreviewShare)
			a.invalidate()
		}
	}
	if div.Hovered() || div.Dragging() {
		div.Background(k.dividerActive)
	}
}

// previewPanel draws the file preview panel at the right of the workspace
// (REQ-DW-081): a card like a pane's, with a header naming the file and its
// folder and the body showing the file (REQ-DW-082, REQ-DW-083). A click on
// it makes it the focused region, so Esc closes it.
func (a *App) previewPanel(c *ui.Context, k *palette, share float32) {
	col := ui.Column(c.Key("preview-panel")).Grow(share).Basis(0).MinWidth(0).MinHeight(0).
		Radius(cardRadius).Clip().Background(k.cardFocused).Border(1, k.cardBorderFocused).
		Shadow(0, 1, 2, 0, k.shadow).Shadow(0, 6, 22, -2, k.shadow)
	if col.Clicked() {
		a.previewFocused = true
	}
	col.Children(func() {
		a.previewHeader(c, k)
		a.previewBody(c, k)
	})
}

// previewHeader is the panel's header: the file name in bold, its folder
// shortened with "~", and the Open, Reveal and Close buttons (REQ-DW-082).
// Open and Reveal act on a local path, so a remote daemon hides them
// (REQ-WP-040).
func (a *App) previewHeader(c *ui.Context, k *palette) {
	p := a.preview
	h := ui.Row(c).Height(cardHeaderH).Padding(0, 12, 0, 12).Gap(7).AlignItems(ui.Center).MinWidth(0)
	h.Children(func() {
		ui.Text(c, p.fileName()).FontSize(12.5).FontWeight(600).TextColor(k.headerText).
			SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		if dir := displayCwd(p.folder(), a.userHome); dir != "" {
			ui.Text(c, dir).FontSize(11.5).TextColor(k.headerMuted).
				SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		}
		ui.Spacer(c)
		if a.daemonLocal() {
			if a.ghostButton(c, k, "preview-open", "Open in default app").Clicked() {
				a.openPreviewInApp(p.path)
			}
			if a.ghostButton(c, k, "preview-reveal", "Reveal in Finder").Clicked() {
				a.revealPreview(p.path)
			}
		}
		if a.ghostButton(c, k, "preview-close", "×").Clicked() {
			a.ClosePreview()
		}
	})
}

// previewBody draws the file itself (REQ-DW-083): a web view for HTML,
// Markdown, images, PDFs and text, and a native message for anything else.
func (a *App) previewBody(c *ui.Context, k *palette) {
	p := a.preview
	switch p.kind {
	case previewKindHTML, previewKindMarkdown, previewKindImage, previewKindPDF, previewKindText:
		if a.previewView != nil {
			ui.WebView(c, a.previewView).Grow(1).MinHeight(0)
			return
		}
		// No web view factory (a headless test): a placeholder naming the
		// file stands in for the page (REQ-DW-070).
		a.previewPlaceholder(c, k)
	default:
		a.previewBinary(c, k)
	}
}

// previewPlaceholder is the body a test sees when there is no web view.
func (a *App) previewPlaceholder(c *ui.Context, k *palette) {
	ui.Box(c).Grow(1).MinHeight(0).Center().Children(func() {
		ui.Column(c).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Preview").FontSize(13).TextColor(k.headerMuted)
			ui.Text(c, a.preview.fileName()).FontSize(12).TextColor(k.headerText).
				SingleLine().Ellipsis("…")
		})
	})
}

// previewBinary is the body of a file the panel cannot show (REQ-DW-083):
// its name, its size and an Open in default app button, which a local daemon
// alone offers (REQ-WP-040).
func (a *App) previewBinary(c *ui.Context, k *palette) {
	p := a.preview
	ui.Box(c).Grow(1).MinHeight(0).Center().Padding(16).Children(func() {
		ui.Column(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, p.fileName()).FontSize(13).FontWeight(600).TextColor(k.headerText).
				TextAlign(ui.Center)
			ui.Text(c, humanSize(p.size)).FontSize(12).TextColor(k.headerMuted)
			if a.daemonLocal() {
				if a.ghostButton(c, k, "preview-open-binary", "Open in default app").Clicked() {
					a.openPreviewInApp(p.path)
				}
			}
		})
	})
}
