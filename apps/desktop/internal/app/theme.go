package app

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// palette holds the window's colours: the light and dark ones of the design
// sheet (spec 58, window design, REQ-DW-035). The view reads the one that
// matches the appearance through App.pal; nothing else names a colour of the
// window directly.
type palette struct {
	window   ui.Color
	hairline ui.Color
	text     ui.Color
	muted    ui.Color
	muted2   ui.Color
	// white is the surface of the chrome's panels: the dialogs, the command
	// palette, the host chip, the palette button and the not-connected card.
	white  ui.Color
	hover  ui.Color
	active ui.Color
	// hoverDark and activeDark are the faces of a button on a pane's dark
	// header.
	hoverDark  ui.Color
	activeDark ui.Color

	// card is an unfocused pane's card, and cardFocused a focused one: the
	// focus shows by brightness, not by a border. shadow and shadowFocused
	// are the soft shadows under them.
	card              ui.Color
	cardFocused       ui.Color
	cardBorder        ui.Color
	cardBorderFocused ui.Color
	// dropBorder outlines a pane the files dragged from Finder would drop on
	// (REQ-DW-043).
	dropBorder    ui.Color
	shadow        ui.Color
	shadowFocused ui.Color
	headerBorder  ui.Color
	headerText    ui.Color
	headerMuted   ui.Color

	green  ui.Color
	orange ui.Color
	red    ui.Color
	grey   ui.Color
	// blue is the "done" status: an agent that finished unseen (spec 58,
	// done status).
	blue ui.Color

	lanaBG   ui.Color
	lanaText ui.Color
	// lanaChipBG and lanaChipText are the Lana run pill of a pane's header
	// (spec 58, window design).
	lanaChipBG   ui.Color
	lanaChipText ui.Color

	divider       ui.Color
	dividerActive ui.Color

	// The raised surfaces of the chrome (spec 58, REQ-DW-035): the selected
	// space row and the active tab pill, and the panels over the window.
	pill        ui.Color
	pillBorder  ui.Color
	panelBorder ui.Color
	// backdrop dims the window behind a dialog or the command palette.
	backdrop ui.Color
	// primary, primaryHover and primaryText are the solid button of a dialog.
	primary      ui.Color
	primaryHover ui.Color
	primaryText  ui.Color
	// quietHover is a ghost button's face under the pointer.
	quietHover ui.Color
	// fieldBorder, fieldPlaceholder and ring are a dialog's text field.
	fieldBorder      ui.Color
	fieldPlaceholder ui.Color
	ring             ui.Color
	// panelShadow and panelShadowSoft are a dialog's or the palette's two
	// soft shadows.
	panelShadow     ui.Color
	panelShadowSoft ui.Color
	// sel, softHover and keycap are a command palette row and its shortcut.
	sel       ui.Color
	softHover ui.Color
	keycap    ui.Color
	// onTint is the text on a coloured tile, as a space's mark (spec 58,
	// spaces).
	onTint ui.Color

	// dark tells that this is the window's dark palette, for the workspace
	// gradient and any other look that is not a plain colour.
	dark bool
}

// light is the palette of the window: the light colours of the design.
var light = palette{
	window:   ui.Hex("#F7F6F3"),
	hairline: ui.Hex("#E9E7E3"),
	text:     ui.Hex("#1D1C1A"),
	muted:    ui.Hex("#8C8880"),
	muted2:   ui.Hex("#6B6760"),
	white:    ui.Hex("#FFFFFF"),
	hover:    ui.RGBA(0, 0, 0, 0.05),
	active:   ui.RGBA(0, 0, 0, 0.09),

	hoverDark:  ui.RGBA(255, 255, 255, 0.08),
	activeDark: ui.RGBA(255, 255, 255, 0.14),

	card:              ui.Hex("#161514"),
	cardFocused:       ui.Hex("#2A2826"),
	cardBorder:        ui.RGBA(255, 255, 255, 0.04),
	cardBorderFocused: ui.RGBA(255, 255, 255, 0.14),
	dropBorder:        ui.RGBA(255, 255, 255, 0.25),
	shadow:            ui.RGBA(0, 0, 0, 0.25),
	shadowFocused:     ui.RGBA(0, 0, 0, 0.40),
	headerBorder:      ui.Hex("#2E2D2B"),
	headerText:        ui.Hex("#FFFFFF"),
	headerMuted:       ui.Hex("#8C8880"),

	green:  ui.Hex("#2F9E5B"),
	orange: ui.Hex("#E8833A"),
	red:    ui.Hex("#E5484D"),
	grey:   ui.Hex("#6B6760"),
	blue:   ui.Hex("#4A8CF7"),

	lanaBG:   ui.Hex("#7652C2"),
	lanaText: ui.Hex("#FFFFFF"),

	lanaChipBG:   ui.Hex("#2F2D3F"),
	lanaChipText: ui.Hex("#CFCBF5"),

	divider:       ui.Hex("#8C8880"),
	dividerActive: ui.RGBA(0, 0, 0, 0.10),

	pill:             ui.Hex("#FFFFFF"),
	pillBorder:       ui.Hex("#E9E7E3"),
	panelBorder:      ui.Hex("#E9E7E3"),
	backdrop:         ui.RGBA(29, 28, 26, 0.28),
	primary:          ui.Hex("#1D1C1A"),
	primaryHover:     ui.Hex("#33312E"),
	primaryText:      ui.Hex("#FFFFFF"),
	quietHover:       ui.Hex("#F1EFEB"),
	fieldBorder:      ui.Hex("#E2DFD9"),
	fieldPlaceholder: ui.Hex("#A8A49C"),
	ring:             ui.RGBA(29, 28, 26, 0.10),
	panelShadow:      ui.RGBA(0, 0, 0, 0.18),
	panelShadowSoft:  ui.RGBA(0, 0, 0, 0.06),
	sel:              ui.RGBA(0, 0, 0, 0.055),
	softHover:        ui.RGBA(0, 0, 0, 0.035),
	keycap:           ui.RGBA(0, 0, 0, 0.06),
	onTint:           ui.Hex("#FFFFFF"),
}

// dark is the palette of the window: the dark colours of the web Workbench's
// Gr8r tokens (spec 58, REQ-DW-035).
var dark = palette{
	window:   ui.Hex("#181817"),
	hairline: ui.Hex("#2C2B29"),
	text:     ui.Hex("#ECEAE5"),
	muted:    ui.Hex("#9A968F"),
	muted2:   ui.Hex("#B5B2AB"),
	white:    ui.Hex("#1D1D1C"),
	hover:    ui.RGBA(255, 255, 255, 0.06),
	active:   ui.RGBA(255, 255, 255, 0.10),

	hoverDark:  ui.RGBA(255, 255, 255, 0.08),
	activeDark: ui.RGBA(255, 255, 255, 0.14),

	card:              ui.Hex("#1A1918"),
	cardFocused:       ui.Hex("#2A2826"),
	cardBorder:        ui.RGBA(255, 255, 255, 0.05),
	cardBorderFocused: ui.RGBA(255, 255, 255, 0.14),
	dropBorder:        ui.RGBA(255, 255, 255, 0.30),
	shadow:            ui.RGBA(0, 0, 0, 0.35),
	shadowFocused:     ui.RGBA(0, 0, 0, 0.55),
	headerBorder:      ui.Hex("#2C2B29"),
	headerText:        ui.Hex("#ECEAE5"),
	headerMuted:       ui.Hex("#9A968F"),

	green:  ui.Hex("#5CBA86"),
	orange: ui.Hex("#D8A443"),
	red:    ui.Hex("#E57D70"),
	grey:   ui.Hex("#6F6C66"),
	blue:   ui.Hex("#8E9AF3"),

	lanaBG:   ui.Hex("#7652C2"),
	lanaText: ui.Hex("#FFFFFF"),

	lanaChipBG:   ui.Hex("#2F2D3F"),
	lanaChipText: ui.Hex("#CFCBF5"),

	divider:       ui.Hex("#6F6C66"),
	dividerActive: ui.RGBA(255, 255, 255, 0.10),

	pill:             ui.Hex("#252524"),
	pillBorder:       ui.Hex("#3A3936"),
	panelBorder:      ui.Hex("#3A3936"),
	backdrop:         ui.RGBA(0, 0, 0, 0.55),
	primary:          ui.Hex("#ECEAE5"),
	primaryHover:     ui.Hex("#DAD7D0"),
	primaryText:      ui.Hex("#181817"),
	quietHover:       ui.RGBA(255, 255, 255, 0.06),
	fieldBorder:      ui.Hex("#3A3936"),
	fieldPlaceholder: ui.Hex("#6F6C66"),
	ring:             ui.RGBA(255, 255, 255, 0.12),
	panelShadow:      ui.RGBA(0, 0, 0, 0.45),
	panelShadowSoft:  ui.RGBA(0, 0, 0, 0.20),
	sel:              ui.RGBA(255, 255, 255, 0.08),
	softHover:        ui.RGBA(255, 255, 255, 0.05),
	keycap:           ui.RGBA(255, 255, 255, 0.08),
	onTint:           ui.Hex("#FFFFFF"),

	dark: true,
}

// darkWindowGradient is the dark Workbench's subtle workspace gradient
// (spec 58, window design, REQ-DW-035).
var darkWindowGradient = ui.LinearGradient{Angle: 135, Stops: []ui.GradientStop{
	{Color: ui.Hex("#1F1C26"), At: 0},
	{Color: ui.Hex("#1A1918"), At: 0.5},
	{Color: ui.Hex("#201D18"), At: 1},
}}

// pal is the palette the window draws in right now (REQ-DW-035): the light or
// dark one the settings chose, else the one the system's appearance asks for.
func (a *App) pal() *palette {
	switch a.settings.Appearance {
	case "light":
		return &light
	case "dark":
		return &dark
	}
	if a.systemDarkAppearance() {
		return &dark
	}
	return &light
}

// systemDarkAppearance reports whether the system is in its dark appearance,
// for a window that follows it (REQ-DW-035). It is a seam: a test sets
// systemDark, and before there is a window the system is not asked.
func (a *App) systemDarkAppearance() bool {
	if a.systemDark != nil {
		return a.systemDark()
	}
	if a.win == nil {
		return false
	}
	return mygo.Theme.IsDark()
}

// AppearanceSource maps the appearance setting to the appearance MyGo
// applies to the window and its widgets (REQ-DW-035): empty follows the
// system.
func AppearanceSource(appearance string) mygo.ThemeSource {
	switch appearance {
	case "light":
		return mygo.ThemeLight
	case "dark":
		return mygo.ThemeDark
	}
	return mygo.ThemeSystem
}

// applyAppearance tells MyGo the appearance the window draws its own parts
// in, so the title bar, the traffic lights and MyGo's widgets match the
// palette (REQ-DW-035). It does nothing before there is a window.
func (a *App) applyAppearance() {
	if a.win == nil {
		return
	}
	mygo.Theme.SetSource(AppearanceSource(a.settings.Appearance))
}

// windowBackground fills the window's root: the palette's solid colour, or
// the dark Workbench's gradient (spec 58, window design).
func windowBackground(e ui.Element, k *palette) {
	if k.dark {
		e.LinearGradient(darkWindowGradient)
		return
	}
	e.Background(k.window)
}

// SetUIFont makes Geist the window's text family (spec 58, REQ-DW-036). MyGo
// takes the family from the theme, so the view sets it on the context's own
// theme each time the appearance changes; once it is Geist this does nothing.
func SetUIFont(c *ui.Context) {
	theme := c.Theme()
	if theme == nil || theme.Font == uiFontFamily {
		return
	}
	t := *theme
	t.Font = uiFontFamily
	c.SetTheme(&t)
}

// renameEnter is how a dialog's panel appears: a 140 ms fade with a slight
// rise and scale (spec 58, spaces). It is a pose, so the frames that move it
// only paint; with less motion only its fade plays.
var renameEnter = ui.Animation{Duration: 140 * time.Millisecond, Ease: ui.EaseOut}
