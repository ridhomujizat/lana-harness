package app

import (
	"context"
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/config"
)

// TestPaletteFollowsTheAppearance is REQ-DW-035: the window chooses the light
// or dark palette from the appearance setting, and follows the system's own
// appearance when the setting is empty.
func TestPaletteFollowsTheAppearance(t *testing.T) {
	cases := []struct {
		name       string
		appearance string
		systemDark bool
		want       *palette
	}{
		{"the light setting", "light", true, &light},
		{"the dark setting", "dark", false, &dark},
		{"the system when it is dark", "", true, &dark},
		{"the system when it is light", "", false, &light},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New(Options{SystemDark: func() bool { return tc.systemDark }})
			a.settings.Appearance = tc.appearance
			if got := a.pal(); got != tc.want {
				t.Errorf("appearance %q with the system dark %v chose %+v, want %+v", tc.appearance, tc.systemDark, got, tc.want)
			}
		})
	}
}

// TestDarkPaletteColorsAreTheWebs is REQ-DW-035: the dark palette takes the
// web Workbench's dark tokens, and the light one keeps its own.
func TestDarkPaletteColorsAreTheWebs(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  ui.Color
		want ui.Color
	}{
		{"the window", dark.window, ui.Hex("#181817")},
		{"an unfocused card", dark.card, ui.Hex("#1A1918")},
		{"a focused card", dark.cardFocused, ui.Hex("#2A2826")},
		{"the text", dark.text, ui.Hex("#ECEAE5")},
		{"the done blue", dark.blue, ui.Hex("#8E9AF3")},
		{"the needs-you orange", dark.orange, ui.Hex("#D8A443")},
		{"the light window", light.window, ui.Hex("#F7F6F3")},
		{"the light needs-you orange", light.orange, ui.Hex("#E8833A")},
	} {
		if tc.got != tc.want {
			t.Errorf("%s is %#v, want %#v", tc.name, tc.got, tc.want)
		}
	}
}

// TestAppearanceChangesTheWindowColor is REQ-DW-035: switching the appearance
// redraws the window in the other palette at once, with no restart.
func TestAppearanceChangesTheWindowColor(t *testing.T) {
	a := testApp(t)
	before := windowPixel(a)
	if !sameRGB(before, light.window) {
		t.Fatalf("the light window's background is %v, want %v", before, light.window)
	}

	a.settings.Appearance = "dark"
	after := windowPixel(a)
	if !sameRGB(after, dark.window) {
		t.Fatalf("the dark window's background is %v, want %v", after, dark.window)
	}
	if before == after {
		t.Fatal("switching the appearance left the window's background unchanged")
	}
}

// windowPixel renders the window and samples the sidebar, which shows the
// window's own colour.
func windowPixel(a *App) color.RGBA {
	tt := ui.NewTester(a.View(), 1440, 900)
	return tt.Image().RGBAAt(10, 820)
}

// TestSettingsOpensWithCommandComma is REQ-DW-060: ⌘, opens the settings
// sheet, the segmented control writes the appearance, and Done closes it.
func TestSettingsOpensWithCommandComma(t *testing.T) {
	a := testApp(t)
	tt := ui.NewTester(a.View(), 1440, 900)
	tt.Key(ui.Super, ui.KeyComma)
	tt.Frame()
	if !a.settingsOpen {
		t.Fatal("⌘, did not open the settings sheet")
	}
	if !tt.HasText("Settings") {
		t.Fatalf("the settings sheet does not show its header; it shows %q", tt.Texts())
	}
	if !tt.HasText("Appearance") || !tt.HasText("Lana home") || !tt.HasText("Text size") {
		t.Fatalf("the settings sheet is missing a section; it shows %q", tt.Texts())
	}

	if err := tt.Click("Dark"); err != nil {
		t.Fatalf("clicking the Dark segment: %v", err)
	}
	tt.Frame()
	if a.settings.Appearance != "dark" {
		t.Fatalf("the segmented control wrote appearance %q, want dark", a.settings.Appearance)
	}
	if a.pal() != &dark {
		t.Fatal("choosing Dark did not switch the palette")
	}

	if err := tt.Click("Done"); err != nil {
		t.Fatalf("clicking Done: %v", err)
	}
	tt.Frame()
	if a.settingsOpen {
		t.Error("Done did not close the settings sheet")
	}
}

// TestSettingsTextSizeClamps is REQ-DW-060: the sheet's text size buttons keep
// the size between 11 and 22, and write it to the settings.
func TestSettingsTextSizeClamps(t *testing.T) {
	dir := t.TempDir()
	a := New(Options{Now: fixedNow, DataDir: dir})

	for i := 0; i < 30; i++ {
		a.nudgeTextSize(-1)
	}
	if got := a.textSize(); got != settingsMinTextSize {
		t.Errorf("nudging down left the text size %v, want %v", got, settingsMinTextSize)
	}
	for i := 0; i < 30; i++ {
		a.nudgeTextSize(1)
	}
	if got := a.textSize(); got != settingsMaxTextSize {
		t.Errorf("nudging up left the text size %v, want %v", got, settingsMaxTextSize)
	}
	if got := config.Load(dir).TextSize; got != settingsMaxTextSize {
		t.Errorf("the settings hold text size %v, want %v", got, settingsMaxTextSize)
	}
}

// TestSettingsLanaHomeReconnect is REQ-DW-012 and REQ-DW-060: the Lana home
// field and Reconnect save the setting and start the connection over.
func TestSettingsLanaHomeReconnect(t *testing.T) {
	t.Setenv("LANA_HOME", "")
	dir := t.TempDir()
	a := New(Options{Now: fixedNow, DataDir: dir, UserHome: "/Users/ada"})
	a.OpenSettings()

	a.settingsHome = "~/lana-test"
	a.applyLanaHome()

	if got := config.Load(dir).LanaHome; got != "~/lana-test" {
		t.Errorf("the settings hold Lana home %q, want %q", got, "~/lana-test")
	}
	if a.home != "/Users/ada/lana-test" {
		t.Errorf("the app points at %q, want %q", a.home, "/Users/ada/lana-test")
	}
	select {
	case <-a.wake:
	default:
		t.Error("Reconnect did not ask the connection loop to try again")
	}
}

// TestSettingsReconnectDialsTheNewHome is REQ-DW-060: when there is a
// connection loop, Reconnect ends it and starts a new one that dials the new
// Lana home.
func TestSettingsReconnectDialsTheNewHome(t *testing.T) {
	t.Setenv("LANA_HOME", "")
	dialed := make(chan string, 1)
	a := New(Options{
		Now:      fixedNow,
		UserHome: "/Users/ada",
		Dial: func(_ context.Context, home string) (Conn, error) {
			select {
			case dialed <- home:
			default:
			}
			return nil, errNotAttached
		},
	})
	a.ctx, a.cancel = context.WithCancel(context.Background())
	defer a.cancel()
	// A loop is already running; Reconnect ends it and starts another.
	a.connCancel = func() {}

	a.settingsHome = "~/lana-test"
	a.applyLanaHome()

	select {
	case got := <-dialed:
		if got != "/Users/ada/lana-test" {
			t.Errorf("the new connection loop dialed %q, want %q", got, "/Users/ada/lana-test")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Reconnect did not start a connection loop at the new home")
	}
}
