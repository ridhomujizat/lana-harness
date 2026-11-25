package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// mustEval resolves a path's symlinks.
func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// waitLink waits for the sign-in link.
func waitLink(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case u := <-ch:
		return u
	case <-time.After(10 * time.Second):
		t.Fatal("no sign-in link")
		return ""
	}
}

// waitErr waits for the sign-in result.
func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("sign-in did not finish")
		return nil
	}
}

// AC-AGY-013: the link reaches onLink, the code is written once to stdin, agy
// exits 0, and the home (0700) holds only the links whose source exists.
func TestSignInSuccess(t *testing.T) {
	owner := t.TempDir()
	if err := os.WriteFile(filepath.Join(owner, ".gitconfig"), []byte("[user]\n\tname = Test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "agy-2")
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "HOME="+owner, "FAKEAGY_LOGIN_CODE=good-code")
	linkCh := make(chan string, 1)
	errCh := make(chan error, 1)
	code := make(chan string, 1)
	go func() {
		errCh <- h.rt.SignIn(context.Background(), core.RuntimeAccount{Name: "agy-2", Home: home},
			func(u string) { linkCh <- u }, code)
	}()

	if u := waitLink(t, linkCh); !strings.HasPrefix(u, "https://accounts.google.com/") {
		t.Fatalf("link = %q", u)
	}
	code <- "good-code"
	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("sign-in: %v", err)
	}

	info, err := os.Stat(home)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("home: %v %v", info, err)
	}
	if link, err := os.Readlink(filepath.Join(home, ".gitconfig")); err != nil || link != filepath.Join(owner, ".gitconfig") {
		t.Fatalf("gitconfig link = %q %v", link, err)
	}
	for _, absent := range []string{".ssh", ".config/gh"} {
		if _, err := os.Lstat(filepath.Join(home, absent)); !os.IsNotExist(err) {
			t.Fatalf("%s should not exist: %v", absent, err)
		}
	}
	// The fake ran acp --login with cwd = the account home, its OWN HOME, and
	// the code on stdin.
	var cwd string
	recorded := false
	for _, e := range h.log() {
		if e.Kind == "start" && len(e.Argv) == 2 && e.Argv[0] == "acp" && e.Argv[1] == "--login" {
			cwd = e.Cwd
			if !hasEnv(e.Env, "HOME="+home) {
				t.Fatalf("login env = %v", e.Env)
			}
		}
		if e.Kind == "login" {
			var line string
			if json.Unmarshal(e.Line, &line) == nil && line == "good-code" {
				recorded = true
			}
		}
	}
	if resolved, want := cwd, mustEval(t, home); mustEval(t, resolved) != want {
		t.Fatalf("login cwd = %q, want %q", cwd, home)
	}
	if !recorded {
		t.Fatal("the code did not reach agy's stdin")
	}
}

// Every link whose owner source exists is created.
func TestSignInLinks(t *testing.T) {
	owner := t.TempDir()
	if err := os.MkdirAll(filepath.Join(owner, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(owner, ".config", "gh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owner, ".gitconfig"), []byte("[user]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "agy-2")
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "HOME="+owner, "FAKEAGY_LOGIN_CODE=c")
	linkCh := make(chan string, 1)
	errCh := make(chan error, 1)
	code := make(chan string, 1)
	go func() {
		errCh <- h.rt.SignIn(context.Background(), core.RuntimeAccount{Name: "agy-2", Home: home},
			func(u string) { linkCh <- u }, code)
	}()
	waitLink(t, linkCh)
	code <- "c"
	if err := waitErr(t, errCh); err != nil {
		t.Fatal(err)
	}
	for _, rel := range loginLinks {
		link, err := os.Readlink(filepath.Join(home, rel))
		if err != nil || link != filepath.Join(owner, rel) {
			t.Fatalf("%s link = %q %v", rel, link, err)
		}
	}
}

// A wrong code fails with agy's line.
func TestSignInBadCode(t *testing.T) {
	owner := t.TempDir()
	home := filepath.Join(t.TempDir(), "agy-2")
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "HOME="+owner, "FAKEAGY_LOGIN_CODE=good-code")
	linkCh := make(chan string, 1)
	errCh := make(chan error, 1)
	code := make(chan string, 1)
	go func() {
		errCh <- h.rt.SignIn(context.Background(), core.RuntimeAccount{Name: "agy-2", Home: home},
			func(u string) { linkCh <- u }, code)
	}()
	waitLink(t, linkCh)
	code <- "wrong"
	err := waitErr(t, errCh)
	if err == nil || !strings.Contains(err.Error(), "login failed: bad code") {
		t.Fatalf("err = %v", err)
	}
}

// Closing the code channel gives agy EOF; agy reports the cancellation.
func TestSignInEOFCancel(t *testing.T) {
	owner := t.TempDir()
	home := filepath.Join(t.TempDir(), "agy-2")
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "HOME="+owner, "FAKEAGY_LOGIN_CODE=good-code")
	linkCh := make(chan string, 1)
	errCh := make(chan error, 1)
	code := make(chan string)
	go func() {
		errCh <- h.rt.SignIn(context.Background(), core.RuntimeAccount{Name: "agy-2", Home: home},
			func(u string) { linkCh <- u }, code)
	}()
	waitLink(t, linkCh)
	close(code)
	err := waitErr(t, errCh)
	if err == nil || !strings.Contains(err.Error(), "login cancelled") {
		t.Fatalf("err = %v", err)
	}
}

// Cancelling ctx kills the process group and returns the context error.
func TestSignInContextCancel(t *testing.T) {
	owner := t.TempDir()
	home := filepath.Join(t.TempDir(), "agy-2")
	h := newHarness(t, "acp_turn1.jsonl", Options{}, "HOME="+owner, "FAKEAGY_LOGIN_CODE=good-code")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	linkCh := make(chan string, 1)
	errCh := make(chan error, 1)
	code := make(chan string, 1)
	go func() {
		errCh <- h.rt.SignIn(ctx, core.RuntimeAccount{Name: "agy-2", Home: home},
			func(u string) { linkCh <- u }, code)
	}()
	waitLink(t, linkCh)
	cancel()
	if err := waitErr(t, errCh); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// The built-in account is never signed in by lana.
func TestSignInBuiltin(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{})
	code := make(chan string)
	for _, name := range []string{"", core.AntigravityProvider} {
		err := h.rt.SignIn(context.Background(), core.RuntimeAccount{Name: name}, nil, code)
		if err == nil || !strings.Contains(err.Error(), "the built-in account signs in with agy in a terminal") {
			t.Fatalf("name %q: err = %v", name, err)
		}
	}
}
