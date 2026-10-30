package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/web"
)

// writeDaemonSecretJSON writes $LANA_HOME/daemon.secret for a fake daemon.
func writeDaemonSecretJSON(t *testing.T, home, addr, secret string) {
	t.Helper()
	body := fmt.Sprintf("{\"addr\":%q,\"secret\":%q}\n", addr, secret)
	if err := os.WriteFile(daemonSecretPath(home), []byte(body), daemonSecretFileMode); err != nil {
		t.Fatal(err)
	}
}

// TestWebResetLoginWithDaemonCoversACWLG011 covers AC-WLG-011: with a running
// daemon the command prints the link, opens it, and exits 0.
func TestWebResetLoginWithDaemonCoversACWLG011(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	const link = "http://127.0.0.1:18300/#/welcome?code=abc123"
	const secret = "the-daemon-secret"

	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != web.LocalSignInPath {
			http.NotFound(w, r)
			return
		}
		seen <- r.Header.Get(web.LocalSecretHeader)
		_ = json.NewEncoder(w).Encode(map[string]string{"link": link})
	}))
	defer srv.Close()
	writeDaemonSecretJSON(t, home, strings.TrimPrefix(srv.URL, "http://"), secret)

	var opened []string
	stubOpenBrowser(t, func(url string) error {
		opened = append(opened, url)
		return nil
	})

	out, code := captureStdout(t, func() int { return cmdWeb([]string{"reset-login"}) })
	if code != 0 {
		t.Fatalf("exit = %d, output = %q", code, out)
	}
	if got := <-seen; got != secret {
		t.Fatalf("daemon saw secret %q, want %q", got, secret)
	}
	if !strings.Contains(out, "Open this link to sign in: "+link) {
		t.Fatalf("stdout = %q, want the link line", out)
	}
	if len(opened) != 1 || opened[0] != link {
		t.Fatalf("opener called with %v, want [%s]", opened, link)
	}
}

// TestWebResetLoginIgnoresOpenerError covers the rule that a failing opener
// does not change the exit code.
func TestWebResetLoginIgnoresOpenerError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	const link = "http://127.0.0.1:18300/#/welcome?code=xyz"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"link": link})
	}))
	defer srv.Close()
	writeDaemonSecretJSON(t, home, strings.TrimPrefix(srv.URL, "http://"), "secret")

	stubOpenBrowser(t, func(string) error { return fmt.Errorf("no browser available") })

	out, code := captureStdout(t, func() int { return cmdWeb([]string{"reset-login"}) })
	if code != 0 {
		t.Fatalf("exit = %d, output = %q", code, out)
	}
	if !strings.Contains(out, link) {
		t.Fatalf("stdout = %q, want the link", out)
	}
}

// TestWebResetLoginWithoutDaemon covers the no-daemon path: reset_requested is
// set and the restart hint is printed.
func TestWebResetLoginWithoutDaemon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)

	out, code := captureStdout(t, func() int { return cmdWeb([]string{"reset-login"}) })
	if code != 0 {
		t.Fatalf("exit = %d, output = %q", code, out)
	}
	if out != webResetNoDaemonMessage+"\n" {
		t.Fatalf("stdout = %q, want %q", out, webResetNoDaemonMessage+"\n")
	}
	requested, err := web.ResetRequested(home)
	if err != nil {
		t.Fatalf("read web.json: %v", err)
	}
	if !requested {
		t.Fatal("reset_requested is false after lana web reset-login without a daemon")
	}
}

// TestWebResetLoginStaleDaemonSecret covers a stale file: a refused connection
// behaves like no daemon.
func TestWebResetLoginStaleDaemonSecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	// Port 1 is closed, so the POST is refused.
	writeDaemonSecretJSON(t, home, "127.0.0.1:1", "stale-secret")

	out, code := captureStdout(t, func() int { return cmdWeb([]string{"reset-login"}) })
	if code != 0 {
		t.Fatalf("exit = %d, output = %q", code, out)
	}
	if out != webResetNoDaemonMessage+"\n" {
		t.Fatalf("stdout = %q, want the restart hint", out)
	}
	if requested, err := web.ResetRequested(home); err != nil || !requested {
		t.Fatalf("reset_requested = %v, %v; want true", requested, err)
	}
}

// TestWebResetLoginDaemonError covers a non-200 answer: the command reports the
// error and exits 1 without asking for a reset.
func TestWebResetLoginDaemonError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"bad secret"}}`))
	}))
	defer srv.Close()
	writeDaemonSecretJSON(t, home, strings.TrimPrefix(srv.URL, "http://"), "wrong-secret")

	stubOpenBrowser(t, func(string) error { return nil })

	out, code := captureStdout(t, func() int { return cmdWeb([]string{"reset-login"}) })
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stdout = %q", code, out)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	if requested, err := web.ResetRequested(home); err != nil || requested {
		t.Fatalf("reset_requested = %v, %v; want false", requested, err)
	}
}

// TestWebSubcommandUsage covers the exit 2 of `lana web` without a known
// subcommand.
func TestWebSubcommandUsage(t *testing.T) {
	stderr, code := captureStderr(t, func() int { return cmdWeb(nil) })
	if code != 2 || !strings.Contains(stderr, "use lana web reset-login") {
		t.Fatalf("no subcommand: exit = %d, stderr = %q", code, stderr)
	}
	if code := cmdWeb([]string{"bogus"}); code != 2 {
		t.Fatalf("unknown subcommand exit = %d, want 2", code)
	}
	if code := cmdWeb([]string{"reset-login", "extra"}); code != 2 {
		t.Fatalf("extra args exit = %d, want 2", code)
	}
}

// TestDaemonWritesAndRemovesDaemonSecret covers the local control file of
// API-WLG-008: the daemon writes it with mode 0600 and removes it on stop.
func TestDaemonWritesAndRemovesDaemonSecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	writeWebOnlyConfig(t, home, "addr = \"127.0.0.1:0\"\n")
	// Keep the daemon from launching a real browser on first start.
	stubOpenBrowser(t, func(string) error { return nil })

	ctx, cancel := context.WithCancel(context.Background())
	var stderr syncWriter
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, &stderr) }()
	addr := webAddressFromStderr(t, &stderr)

	path := daemonSecretPath(home)
	var stored daemonSecretFile
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && json.Unmarshal(data, &stored) == nil && stored.Addr != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon.secret was not written: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stored.Addr != addr {
		t.Fatalf("daemon.secret addr = %q, want %q", stored.Addr, addr)
	}
	if stored.Secret == "" {
		t.Fatal("daemon.secret has an empty secret")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != daemonSecretFileMode {
		t.Fatalf("daemon.secret mode = %o, want %o", got, daemonSecretFileMode)
	}

	cancel()
	if code := waitDaemon(t, done); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("daemon.secret still exists after stop: %v", err)
	}
}
