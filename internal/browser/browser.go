// Package browser opens a URL in the owner's default browser.
package browser

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// ErrUnsupported reports that this system has no way to open a browser.
var ErrUnsupported = errors.New("browser: opening a browser is not supported on this system")

// Open opens url in the default browser. The daemon calls it once on first
// start or after a requested reset (REQ-WLG-001).
func Open(url string) error {
	return opener(url)
}

// opener is the open implementation. Tests replace it.
var opener = defaultOpen

// launcher starts a command with a URL argument. It is a variable so tests can
// replace it and never start a real browser.
var launcher = start

func defaultOpen(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return launcher("open", url)
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("browser: no display; cannot open the browser")
		}
		return launcher("xdg-open", url)
	default:
		return ErrUnsupported
	}
}

// start launches command with url and reaps it in the background.
func start(command, url string) error {
	// A test binary never launches a real browser: daemon tests start the web
	// server with a fresh home, which would open a sign-in tab on every run.
	if testing.Testing() {
		return nil
	}
	cmd := exec.Command(command, url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
