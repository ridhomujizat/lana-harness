// Lana Workbench is the native Workbench of Lana (spec 58): one window of
// terminals drawn by MyGo and the terminal plugin, a client of the daemon's
// HTTP API like the web UI. The window's state and views live in
// internal/app; this file is the app's lifecycle.
//
//	go tool mygo dev    the app, rebuilt as the code changes
//	go tool mygo build  the app bundle under build/
package main

import (
	"log"
	"os"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/app"
	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/config"
)

// workbench is the app's state; win is its single window.
var (
	workbench *app.App
	win       *mygo.Window
)

func main() {
	// The terminal library is loaded first, so that one that is missing is
	// reported before a window shows. The app goes on without it: the
	// window says it is not connected either way.
	if err := terminal.Load(); err != nil {
		log.Printf("terminal: %v", err)
	}
	// The app's fonts, Geist and Geist Mono, are registered before the first
	// frame draws text (spec 58, REQ-DW-036).
	if err := app.RegisterFonts(); err != nil {
		log.Printf("fonts: %v", err)
	}
	// The saved appearance is applied before the app runs, so the window's
	// own parts and MyGo's widgets match from the first frame (REQ-DW-035).
	st := settings()
	mygo.Theme.SetSource(app.AppearanceSource(st.Appearance))
	workbench = app.New(app.Options{
		Home:     lanaHome(),
		Settings: st,
		DataDir:  dataDir(),
		Dev:      mygo.IsDev(),
		// The preview panel shows a file in the window's own web view
		// (REQ-DW-083); the factory makes the first one from the window.
		NewWebView: func(url string) *mygo.WebView {
			if win == nil {
				return nil
			}
			v, err := win.NewWebView(mygo.WebViewOptions{
				URL:             url,
				BackgroundColor: "light-dark(#F7F6F3, #181817)",
			})
			if err != nil {
				log.Printf("preview: web view: %v", err)
				return nil
			}
			return v
		},
	})
	// The menu bar carries the app's shortcuts, so they work even while a
	// terminal has the keyboard focus (REQ-DW-050).
	mygo.App.SetMenu(workbench.Menu())
	mygo.App.WhenReady(open)
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			open()
		}
	})
	mygo.App.OnWindowAllClosed(func() {
		if runtime.GOOS != "darwin" {
			mygo.App.Quit()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// settings reads the app's settings from its data directory.
func settings() config.Settings {
	dir := dataDir()
	if dir == "" {
		return config.Defaults()
	}
	return config.Load(dir)
}

// dataDir is the app's data directory, where settings.json is kept
// (REQ-DW-060).
func dataDir() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Printf("data directory: %v", err)
		return ""
	}
	return dir
}

// lanaHome resolves the Lana home this build talks to: the LANA_HOME
// environment variable, the setting, then ~/.lana-dev under mygo dev and
// ~/.lana otherwise (REQ-DW-012).
func lanaHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("home directory: %v", err)
	}
	return config.ResolveLanaHome(os.Getenv, settings(), mygo.IsDev(), home)
}

// open shows the window, or brings a window that was hidden or minimized
// back. The size and position come from the window state of the last run.
func open() {
	if win != nil {
		win.Restore()
		win.Focus()
		return
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:                "Lana Workbench",
		Width:                1440,
		Height:               900,
		MinWidth:             900,
		MinHeight:            560,
		StateKey:             "main",
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 16, Y: 15},
		// The window's own background follows the appearance, so no light
		// flash shows before the first frame (REQ-DW-035).
		BackgroundColor: "light-dark(#F7F6F3, #181817)",
		Content: ui.View(func(c *ui.Context) {
			// Geist is the window's text family (spec 58, REQ-DW-036).
			app.SetUIFont(c)
			workbench.View()(c)
		}),
	})
	win = w
	workbench.Start(w)
	w.OnClosed(func() {
		workbench.Stop()
		win = nil
	})
	snapshot(w)
}
