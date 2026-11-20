package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/config"
)

// The settings sheet's metrics and limits (REQ-DW-060).
const (
	settingsWidth       float32 = 460
	settingsMinTextSize float32 = 11
	settingsMaxTextSize float32 = 22
)

// appearanceOption is one segment of the Appearance control: the setting it
// writes, its label and its icon (REQ-DW-060).
type appearanceOption struct {
	value string
	label string
	icon  string
}

// appearanceOptions are the Appearance segments, in order.
var appearanceOptions = []appearanceOption{
	{"", "System", "monitor"},
	{"light", "Light", "sun"},
	{"dark", "Dark", "moon"},
}

// OpenSettings opens the settings sheet (REQ-DW-060), opened by ⌘, and the
// palette's Settings command.
func (a *App) OpenSettings() {
	a.settingsOpen = true
	a.settingsHome = a.settings.LanaHome
	a.invalidate()
}

// settingsModal draws the settings sheet (REQ-DW-060) over the window, in the
// style of the rename dialog: a panel with the appearance, text size, Lana
// home and notifications sections, and Done. Escape or a press on the backdrop
// closes it; every change is saved at once.
func (a *App) settingsModal(c *ui.Context) {
	k := a.pal()
	ui.DialogBase(c, &a.settingsOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop)
		panel.Width(settingsWidth).Padding(20).Gap(16).Radius(12).
			Background(k.white).Border(1, k.panelBorder).
			Shadow(0, 12, 32, 0, k.panelShadow).Shadow(0, 1, 2, 0, k.panelShadowSoft)
		entered := ui.Local(panel, "settings-entered", func() bool { return false })
		p := ui.Pose{}
		if !*entered {
			p, *entered = ui.Pose{Y: 8, Scale: 0.97, Fade: 1}, true
			c.Invalidate()
		}
		panel.AnimatePose(p, renameEnter)
		panel.Children(func() {
			ui.Text(c, "Settings").FontSize(16).FontWeight(600).TextColor(k.text).SingleLine()
			a.appearanceSection(c, k)
			a.textSizeSection(c, k)
			a.lanaHomeSection(c, k)
			a.notificationsSection(c, k)
			a.settingsFooter(c, k)
		})
	})
}

// settingsSectionTitle is the heading of one section of the sheet.
func settingsSectionTitle(c *ui.Context, k *palette, title string) {
	ui.Text(c, title).FontSize(12.5).FontWeight(600).TextColor(k.text).SingleLine()
}

// appearanceSection is the System / Light / Dark segmented control
// (REQ-DW-035, REQ-DW-060).
func (a *App) appearanceSection(c *ui.Context, k *palette) {
	ui.Column(c).Gap(8).Children(func() {
		settingsSectionTitle(c, k, "Appearance")
		ui.Row(c).Gap(2).Padding(2).Radius(9).Background(k.hairline).
			Role(ui.RoleRadioGroup).Label("Appearance").Children(func() {
			for _, opt := range appearanceOptions {
				a.appearanceSegment(c, k, opt)
			}
		})
	})
}

// appearanceSegment is one Appearance segment: its icon and label, chosen
// when the setting matches (REQ-DW-060).
func (a *App) appearanceSegment(c *ui.Context, k *palette, opt appearanceOption) {
	selected := a.settings.Appearance == opt.value
	b := ui.Row(c.Key("appearance-"+opt.value)).Height(28).Padding(4, 12, 4, 12).Gap(6).Radius(7).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleRadio).Selected(selected).
		Label(opt.label)
	col := k.muted
	switch {
	case selected:
		b.Background(k.pill).Border(1, k.pillBorder)
		col = k.text
	case b.Hovered():
		b.Background(k.hover)
	}
	b.Children(func() {
		ui.Icon(c, icon(opt.icon)).Size(14, 14).TextColor(col)
		ui.Text(c, opt.label).FontSize(12.5).FontWeight(500).TextColor(col).SingleLine()
	})
	if b.Clicked() {
		a.setAppearance(opt.value)
	}
}

// textSizeSection is the terminals' text size (REQ-DW-036, REQ-DW-060): −
// value +, kept between 11 and 22, and Reset, applied live to the terminals
// the window shows.
func (a *App) textSizeSection(c *ui.Context, k *palette) {
	ui.Column(c).Gap(8).Children(func() {
		settingsSectionTitle(c, k, "Text size")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if a.settingsSmallButton(c, k, "text-smaller", "−").Clicked() {
				a.nudgeTextSize(-1)
			}
			ui.Text(c, fmt.Sprintf("%.0f", a.textSize())).FontSize(13).FontWeight(500).TextColor(k.text).
				Width(28).TextAlign(ui.Center).SingleLine()
			if a.settingsSmallButton(c, k, "text-bigger", "+").Clicked() {
				a.nudgeTextSize(1)
			}
			ui.Spacer(c)
			if a.settingsSmallButton(c, k, "text-reset", "Reset").Clicked() {
				a.actualTextSize()
			}
		})
	})
}

// nudgeTextSize moves the terminals' text size by delta, kept between the
// sheet's own limits (REQ-DW-060).
func (a *App) nudgeTextSize(delta float32) {
	size := min(max(a.textSize()+delta, settingsMinTextSize), settingsMaxTextSize)
	a.setTextSize(size)
}

// lanaHomeSection is the Lana home (REQ-DW-012, REQ-DW-060): the resolved
// path and where it comes from, a field to set the setting, and Reconnect,
// which saves it and starts the connection over.
func (a *App) lanaHomeSection(c *ui.Context, k *palette) {
	ui.Column(c).Gap(8).Children(func() {
		settingsSectionTitle(c, k, "Lana home")
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, a.home).FontSize(12.5).TextColor(k.text).SingleLine().Ellipsis("…")
			ui.Text(c, "From "+a.lanaHomeSource()).FontSize(11.5).TextColor(k.muted).SingleLine()
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			in := ui.TextInputBase(c, &a.settingsHome).Height(32).Radius(8).Padding(0, 10).
				Grow(1).FontSize(13).Background(k.white).Border(1, k.fieldBorder).
				Placeholder("~/.lana").PlaceholderColor(k.fieldPlaceholder).FocusRing(false)
			if in.Focused() {
				in.BorderColor(k.text).Shadow(0, 0, 0, 3, k.ring)
			}
			if a.settingsSmallButton(c, k, "lana-home-reconnect", "Reconnect").Clicked() {
				a.applyLanaHome()
			}
		})
	})
}

// lanaHomeSource names where the Lana home in use comes from (REQ-DW-012).
func (a *App) lanaHomeSource() string {
	if os.Getenv("LANA_HOME") != "" {
		return "the LANA_HOME environment variable"
	}
	if a.settings.LanaHome != "" {
		return "the Lana home setting"
	}
	if a.dev {
		return "the default (~/.lana-dev)"
	}
	return "the default (~/.lana)"
}

// applyLanaHome keeps the typed Lana home in the settings, points the app at
// it, and starts the connection over, so the new home is used at once
// (REQ-DW-012, REQ-DW-060).
func (a *App) applyLanaHome() {
	a.settings.LanaHome = strings.TrimSpace(a.settingsHome)
	a.saveSettings()
	a.home = config.ResolveLanaHome(os.Getenv, a.settings, a.dev, a.userHome)
	a.reconnect()
}

// reconnect starts the connection loop over with the app's Lana home, or
// wakes it when there is no loop yet, and draws a frame (REQ-DW-060).
func (a *App) reconnect() {
	if a.connCancel != nil {
		a.connCancel()
		a.startConn()
	} else {
		a.Retry()
	}
	a.invalidate()
}

// notificationsSection is the notifications toggle (REQ-DW-060).
func (a *App) notificationsSection(c *ui.Context, k *palette) {
	ui.Column(c).Gap(8).Children(func() {
		settingsSectionTitle(c, k, "Notifications")
		if a.settingsSwitch(c, k, "settings-notifications", "Show a desktop notification when a session needs you.", &a.settings.Notifications).Clicked() {
			a.saveSettings()
		}
		ui.Text(c, "Lana.app already posts Workbench notifications.").FontSize(11.5).TextColor(k.muted).SingleLine()
	})
}

// settingsFooter is the sheet's last row: Done, which closes it (Escape
// closes it too) (REQ-DW-060).
func (a *App) settingsFooter(c *ui.Context, k *palette) {
	ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
		if a.dialogButton(c, "Done", "esc", true).Clicked() {
			a.settingsOpen = false
		}
	})
}

// settingsSmallButton is a small bordered button of the settings sheet.
func (a *App) settingsSmallButton(c *ui.Context, k *palette, key, label string) ui.Element {
	b := ui.Row(c.Key(key)).Height(28).MinWidth(28).Padding(0, 9).Radius(7).Shrink(0).
		AlignItems(ui.Center).Justify(ui.Center).Cursor(ui.CursorPointer).
		Role(ui.RoleButton).Label(label).Background(k.white).Border(1, k.panelBorder)
	if b.Hovered() {
		b.Background(k.quietHover)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(13).TextColor(k.text).SingleLine()
	})
	return b
}

// settingsSwitch is a small switch of the settings sheet: a track with a
// knob, which flips *on over a click.
func (a *App) settingsSwitch(c *ui.Context, k *palette, key, label string, on *bool) ui.Element {
	b := ui.Row(c.Key(key)).Width(38).Height(22).Padding(2).Radius(11).Shrink(0).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleSwitch).
		Selected(*on).Label(label)
	if *on {
		b.Background(k.green).Justify(ui.End)
	} else {
		b.Background(k.hairline).Justify(ui.Start)
	}
	if b.Clicked() {
		*on = !*on
	}
	b.Children(func() {
		ui.Box(c).Size(18, 18).Radius(9).Shrink(0).Background(k.white)
	})
	return b
}
