package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubOpenBrowser replaces the daemon opener for one test and restores it when
// the test ends. The daemon reads the variable only while starting, before the
// test stops it, so there is no concurrent access.
func stubOpenBrowser(t *testing.T, open func(url string) error) {
	t.Helper()
	previous := openBrowser
	openBrowser = open
	t.Cleanup(func() { openBrowser = previous })
}

// writeResetRequested writes a web.json that only asks for a reset
// (REQ-WLG-005).
func writeResetRequested(t *testing.T, home string) {
	t.Helper()
	body := `{"version":2,"reset_requested":true}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "web.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDaemonOpensBrowserOnStartCoversACWLG001 covers AC-WLG-001: a new
// LANA_HOME with no password opens a sign-in link once and writes it to stderr.
func TestDaemonOpensBrowserOnStartCoversACWLG001(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	writeWebOnlyConfig(t, home, "addr = \"127.0.0.1:0\"\n")

	opened := make(chan string, 1)
	stubOpenBrowser(t, func(url string) error {
		opened <- url
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	var stderr syncWriter
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, &stderr) }()
	addr := webAddressFromStderr(t, &stderr)

	var link string
	select {
	case link = <-opened:
	case <-time.After(5 * time.Second):
		t.Fatalf("opener was not called; stderr = %q", stderr.String())
	}
	want := "http://" + addr + "/#/welcome?code="
	if !strings.HasPrefix(link, want) {
		t.Fatalf("opened url = %q, want prefix %q", link, want)
	}
	waitStderr(t, &stderr, "web: open this link to set your password: "+link)

	select {
	case extra := <-opened:
		t.Fatalf("opener was called more than once: %q", extra)
	default:
	}

	cancel()
	if code := waitDaemon(t, done); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

// TestDaemonOpenBrowserFalseCoversACWLG002 covers AC-WLG-002: with
// open_browser = false the opener is not called and the link is still on
// stderr.
func TestDaemonOpenBrowserFalseCoversACWLG002(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	writeWebOnlyConfig(t, home, "addr = \"127.0.0.1:0\"\nopen_browser = false\n")

	called := make(chan string, 1)
	stubOpenBrowser(t, func(url string) error {
		called <- url
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	var stderr syncWriter
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, &stderr) }()
	webAddressFromStderr(t, &stderr)
	waitStderr(t, &stderr, "web: open this link to set your password: http://")

	select {
	case url := <-called:
		t.Fatalf("opener was called with %q although open_browser = false", url)
	case <-time.After(200 * time.Millisecond):
	}

	cancel()
	if code := waitDaemon(t, done); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

// TestDaemonOpenOnResetRequestedCoversACWLG010 covers AC-WLG-010: a start with
// reset_requested opens a sign-in link.
func TestDaemonOpenOnResetRequestedCoversACWLG010(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	writeWebOnlyConfig(t, home, "addr = \"127.0.0.1:0\"\n")
	writeResetRequested(t, home)

	opened := make(chan string, 1)
	stubOpenBrowser(t, func(url string) error {
		opened <- url
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	var stderr syncWriter
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, &stderr) }()
	addr := webAddressFromStderr(t, &stderr)

	var link string
	select {
	case link = <-opened:
	case <-time.After(5 * time.Second):
		t.Fatalf("opener was not called; stderr = %q", stderr.String())
	}
	if !strings.HasPrefix(link, "http://"+addr+"/#/welcome?code=") {
		t.Fatalf("opened url = %q, want a sign-in link", link)
	}

	cancel()
	if code := waitDaemon(t, done); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}
