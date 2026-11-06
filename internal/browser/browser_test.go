package browser

import (
	"errors"
	"runtime"
	"testing"
)

func TestOpenCallsOpener(t *testing.T) {
	previous := opener
	t.Cleanup(func() { opener = previous })

	var got string
	opener = func(url string) error {
		got = url
		return nil
	}
	if err := Open("http://127.0.0.1:18300/#/welcome?code=abc"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != "http://127.0.0.1:18300/#/welcome?code=abc" {
		t.Fatalf("opener got %q", got)
	}
}

func TestOpenPropagatesError(t *testing.T) {
	previous := opener
	t.Cleanup(func() { opener = previous })

	sentinel := errors.New("boom")
	opener = func(string) error { return sentinel }
	if err := Open("http://example.com"); !errors.Is(err, sentinel) {
		t.Fatalf("Open = %v, want sentinel", err)
	}
}

func TestDefaultOpenDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	previous := launcher
	t.Cleanup(func() { launcher = previous })

	var gotCommand, gotURL string
	launcher = func(command, url string) error {
		gotCommand, gotURL = command, url
		return nil
	}
	if err := defaultOpen("http://example.com"); err != nil {
		t.Fatalf("defaultOpen: %v", err)
	}
	if gotCommand != "open" || gotURL != "http://example.com" {
		t.Fatalf("launcher got %q %q, want open http://example.com", gotCommand, gotURL)
	}
}

func TestDefaultOpenLinuxWithDisplay(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	for _, env := range []string{"DISPLAY", "WAYLAND_DISPLAY"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("DISPLAY", "")
			t.Setenv("WAYLAND_DISPLAY", "")
			t.Setenv(env, ":0")

			previous := launcher
			t.Cleanup(func() { launcher = previous })

			var gotCommand, gotURL string
			launcher = func(command, url string) error {
				gotCommand, gotURL = command, url
				return nil
			}
			if err := defaultOpen("http://example.com"); err != nil {
				t.Fatalf("defaultOpen: %v", err)
			}
			if gotCommand != "xdg-open" || gotURL != "http://example.com" {
				t.Fatalf("launcher got %q %q, want xdg-open http://example.com", gotCommand, gotURL)
			}
		})
	}
}

func TestDefaultOpenLinuxWithoutDisplay(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	if err := defaultOpen("http://example.com"); err == nil {
		t.Fatal("defaultOpen succeeded without a display")
	}
}
