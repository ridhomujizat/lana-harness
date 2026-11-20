package app

import (
	"math"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/layout"
)

// iconButton is a small borderless button of an icon on the window's light
// chrome, with a face on hover. label names it for screen readers and its
// tooltip.
func (a *App) iconButton(c *ui.Context, k *palette, key, name, label string) ui.Element {
	return a.tintedIconButton(c, key, name, label, k.muted2, k.text, k.hover, k.active)
}

// headerIconButton is an icon button on a pane's dark header, where the
// icon lightens to white under the pointer rather than darkening.
func (a *App) headerIconButton(c *ui.Context, k *palette, key, name, label string) ui.Element {
	return a.tintedIconButton(c, key, name, label, k.headerMuted, k.headerText, k.hoverDark, k.activeDark)
}

// tintedIconButton is a borderless icon button in the colours of the
// surface it sits on.
func (a *App) tintedIconButton(c *ui.Context, key, name, label string, idle, lit, hover, pressed ui.Color) ui.Element {
	b := ui.Box(c.Key(key)).Size(24, 24).Center().Radius(6).Shrink(0).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label).Tooltip(label)
	switch {
	case b.Pressed():
		b.Background(pressed)
	case b.Hovered():
		b.Background(hover)
	}
	col := idle
	if b.Hovered() {
		col = lit
	}
	b.Children(func() {
		ui.Icon(c, icon(name)).Size(15, 15).TextColor(col)
	})
	return b
}

// toggleButton is an icon button that shows whether it is the one chosen: a
// button of the segmented pair the web Workbench draws in the Agents
// heading, on the panel's light surface.
func (a *App) toggleButton(c *ui.Context, k *palette, key, name, label string, on bool) ui.Element {
	b := ui.Box(c.Key(key)).Size(26, 24).Center().Radius(6).Shrink(0).
		Cursor(ui.CursorPointer).Role(ui.RoleToggleButton).Label(label).Tooltip(label).Selected(on)
	switch {
	case on:
		b.Background(k.hairline)
	case b.Hovered():
		b.Background(k.quietHover)
	}
	col := k.muted
	if on {
		col = k.text
	}
	b.Children(func() {
		ui.Icon(c, icon(name)).Size(15, 15).TextColor(col)
	})
	return b
}

// pillButton is the dark pill of the exited bar: light text on the card's
// surface.
func (a *App) pillButton(c *ui.Context, k *palette, key, label string) ui.Element {
	b := ui.Box(c.Key(key)).PaddingX(10).PaddingY(4).Radius(6).Shrink(0).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	switch {
	case b.Pressed():
		b.Background(k.cardBorderFocused)
	case b.Hovered():
		b.Background(k.headerBorder)
	default:
		b.Background(k.cardBorder)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(k.headerText).SingleLine()
	})
	return b
}

// dialogButton is a button of a dialog (spec 58, spaces, REQ-DW-060): a ghost
// one with no face of its own until the pointer is on it, or the solid
// primary one, with the key that presses it shown in Geist Mono beside the
// label.
func (a *App) dialogButton(c *ui.Context, label, keycap string, primary bool) ui.Element {
	k := a.pal()
	b := ui.ButtonBase(c).Height(32).Radius(8).Padding(0, 14).Gap(8).
		AlignItems(ui.Center).Justify(ui.Center).Shrink(0).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	text := k.text
	switch {
	case primary:
		text = k.primaryText
		if b.Hovered() || b.Pressed() {
			b.Background(k.primaryHover)
		} else {
			b.Background(k.primary)
		}
	case b.Hovered() || b.Pressed():
		b.Background(k.quietHover)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(text).SingleLine()
		if keycap != "" {
			ui.Text(c, keycap).Font(monoFontFamily).FontSize(11).TextColor(k.muted).SingleLine()
		}
	})
	return b
}

// programTile is the mark a program shows everywhere: the landscape tile of
// the table, with the program's glyph on it (REQ-WP-030, REQ-WP-032). A mono
// logo, the shell prompt icon and a text glyph are drawn in the tile's mark
// colour; a colour logo keeps its own colours. omp's small accent dot sits at
// the tile's bottom right.
func (a *App) programTile(c *ui.Context, t tile, size float32) ui.Element {
	e := landscapeTile(c, t.BG, size, 0)
	if t.Border.A != 0 {
		e.Border(1, t.Border)
	}
	e.Children(func() {
		switch {
		case t.Prompt:
			ui.Icon(c, icon("prompt")).Size(size*0.62, size*0.62).TextColor(t.FG).ZIndex(1)
		case t.Logo != nil && t.Mono:
			ui.Icon(c, t.Logo).Size(size*0.66, size*0.66).TextColor(t.FG).ZIndex(1)
		case t.Logo != nil:
			ui.Image(c, t.Logo).Size(size*0.66, size*0.66).TextColor(t.FG).ZIndex(1)
		case t.Glyph != "":
			ui.Text(c, t.Glyph).FontSize(size * 0.5).FontWeight(600).TextColor(t.FG).ZIndex(1)
		}
		if t.Accent.A != 0 {
			ui.Box(c).Absolute().Right(2).Bottom(2).Size(3, 3).Radius(999).
				Background(t.Accent).ZIndex(2)
		}
	})
	return e
}

// landscapeTile is a program tile's landscape face: a rounded rectangle of
// colour, a little wider than it is tall, with a faint top sheen and a soft
// shadow, inset from its height when it is drawn behind another tile
// (spec 58, window design). It carries no glyph; the caller adds one.
func landscapeTile(c *ui.Context, bg ui.Color, size, inset float32) ui.Element {
	w, h, rad := tileWidth(size), size-2*inset, size*0.28
	e := ui.Box(c).Size(w, h).Radius(rad).Center().Shrink(0).Background(bg).
		Shadow(0, 0.5, 1.5, 0, ui.RGBA(0, 0, 0, 0.18))
	e.Children(func() {
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Radius(rad).
			LinearGradient(ui.LinearGradient{
				From: ui.RGBA(255, 255, 255, 0.18), To: ui.RGBA(255, 255, 255, 0),
				Angle: 180, End: 0.6,
			})
	})
	return e
}

// programTileBack is a program tile drawn behind the front one of a tab's
// tiles: the same landscape face in the tile's own colour, with no glyph, its
// height reduced by 2*inset (REQ-WP-031).
func programTileBack(c *ui.Context, t tile, size, inset float32) ui.Element {
	return landscapeTile(c, t.BG, size, inset)
}

// tileWidth is the landscape width of a program tile of the given height:
// a little wider than it is tall, as GoRex draws them (spec 58, window
// design).
func tileWidth(size float32) float32 {
	return float32(math.Round(float64(size) * 1.32))
}

// statusDot is what a session is doing: a colour, a hollow ring for idle
// (spec 58, window design).
func (a *App) statusDot(c *ui.Context, k *palette, s layout.Status) ui.Element {
	e := ui.Box(c).Size(8, 8).Radius(4).Shrink(0)
	switch s {
	case layout.StatusWorking:
		e.Background(k.green)
	case statusDone:
		e.Background(k.blue)
	case layout.StatusBlocked:
		e.Background(k.orange)
	case layout.StatusExited:
		e.Background(k.red)
	default:
		e.Border(1.5, k.grey)
	}
	return e
}

// colorTile is the square of a space: its colour and the first letter of
// its label.
func (a *App) colorTile(c *ui.Context, hexColor, label string) ui.Element {
	initial := "?"
	if r := []rune(label); len(r) > 0 {
		initial = strings.ToUpper(string(r[0]))
	}
	e := ui.Box(c).Size(22, 22).Radius(6).Center().Shrink(0).Background(spaceColor(hexColor))
	e.Children(func() {
		ui.Text(c, initial).FontSize(12).FontWeight(600).TextColor(ui.Hex("#FFFFFF"))
	})
	return e
}

// spaceColor is a space's colour, the default one when it has none the app
// can draw.
func spaceColor(hexColor string) ui.Color {
	if len(hexColor) == 7 && hexColor[0] == '#' {
		return ui.Hex(hexColor)
	}
	return ui.Hex(layout.DefaultColor)
}
