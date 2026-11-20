package app

import (
	"fmt"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/assets"
)

// The app's fonts (spec 58, REQ-DW-036): Geist for the interface and Geist
// Mono for the terminals, embedded (OFL, see assets/fonts/README.md).
const (
	uiFontFamily   = "Geist"
	monoFontFamily = "Geist Mono"
)

// uiFontFiles are the Geist files the interface uses, in the weights the
// window's text draws in.
var uiFontFiles = []string{
	"Geist-Regular.ttf",
	"Geist-Medium.ttf",
	"Geist-SemiBold.ttf",
	"Geist-Bold.ttf",
}

// monoFontFiles are the Geist Mono files the terminals use.
var monoFontFiles = []string{
	"GeistMono-Regular.ttf",
	"GeistMono-Medium.ttf",
}

var (
	fontsOnce sync.Once
	fontsErr  error
)

// RegisterFonts registers the embedded fonts with the text engine, once. It
// is safe to call more than once, so the app and a test can both call it. An
// error names the file that did not parse.
func RegisterFonts() error {
	fontsOnce.Do(func() {
		register := func(files []string, family string) error {
			for _, name := range files {
				data, err := assets.Fonts.ReadFile("fonts/" + name)
				if err != nil {
					return err
				}
				if err := ui.RegisterFont(data, family); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
			return nil
		}
		if err := register(uiFontFiles, uiFontFamily); err != nil {
			fontsErr = err
			return
		}
		fontsErr = register(monoFontFiles, monoFontFamily)
	})
	return fontsErr
}
