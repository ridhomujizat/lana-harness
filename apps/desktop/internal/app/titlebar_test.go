package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestTitlebarPanelButtonIsTheWayBack is spec 58, panel: a hidden panel
// leaves the title bar a "Show panel" button at the left of the tab strip,
// and clicking it brings the panel back; a shown panel leaves the button
// out, so the toggle shows only where it is needed.
func TestTitlebarPanelButtonIsTheWayBack(t *testing.T) {
	a := testApp(t)
	a.dataDir = t.TempDir()
	tt := ui.NewTester(a.View(), 1440, 900)

	if tt.HasText("Show panel (⌘B)") {
		t.Fatalf("the title bar shows the Show panel button with the panel up; it shows %q", tt.Texts())
	}

	a.TogglePanel()
	tt.Frame()
	if !a.settings.PanelHidden {
		t.Fatal("Toggle Panel did not hide the panel")
	}
	if !tt.HasText("Show panel (⌘B)") {
		t.Fatalf("the hidden panel leaves no way back; the title bar shows %q", tt.Texts())
	}

	if err := tt.Click("Show panel (⌘B)"); err != nil {
		t.Fatalf("clicking Show panel: %v", err)
	}
	if a.settings.PanelHidden {
		t.Error("clicking Show panel did not show the panel")
	}
	tt.Frame()
	if !tt.HasText("Spaces") {
		t.Errorf("the panel did not come back; the window shows %q", tt.Texts())
	}
	if tt.HasText("Show panel (⌘B)") {
		t.Error("the Show panel button stays while the panel shows")
	}
}
