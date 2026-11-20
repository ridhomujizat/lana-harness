package app

import (
	"io/fs"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/assets"
)

// TestEmbeddedFontsLoad is REQ-DW-036: the embedded Geist and Geist Mono
// files are there and parse, and the text engine lists them under the
// families the app names.
func TestEmbeddedFontsLoad(t *testing.T) {
	files, err := fs.Glob(assets.Fonts, "fonts/*.ttf")
	if err != nil {
		t.Fatalf("listing the embedded fonts: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no embedded fonts")
	}
	if err := RegisterFonts(); err != nil {
		t.Fatalf("registering the embedded fonts: %v", err)
	}
	families := map[string]ui.FontFamily{}
	for _, f := range ui.FontFamilies() {
		families[f.Name] = f
	}
	for _, want := range []string{uiFontFamily, monoFontFamily} {
		got, ok := families[want]
		if !ok {
			t.Errorf("the font family %q is not listed", want)
			continue
		}
		if !got.Registered {
			t.Errorf("%q is not marked as registered", want)
		}
	}
}
