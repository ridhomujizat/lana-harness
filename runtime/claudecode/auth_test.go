package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// entriesOf returns the fake's log lines of one kind.
func (h *harness) entriesOf(kind string) []logEntry {
	var out []logEntry
	for _, e := range h.log() {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// envCount returns how often name is set in env.
func envCount(env []string, name string) int {
	n := 0
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && k == name {
			n++
		}
	}
	return n
}

// tokenEnv are daemon variables that must never reach an account's claude.
var tokenEnv = []string{
	"ANTHROPIC_API_KEY=sk-ant-daemon", "ANTHROPIC_AUTH_TOKEN=daemon-token", "CLAUDE_CODE_OAUTH_TOKEN=daemon-oauth",
}

// checkAccountEnv checks the env of a named account's claude: both folder
// variables set once to dir, and no token variable (REQ-CCT-012).
func checkAccountEnv(t *testing.T, what string, env []string, dir string) {
	t.Helper()
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR"} {
		if v, _ := envValue(env, name); v != dir || envCount(env, name) != 1 {
			t.Fatalf("%s: %s = %q (%d times), want %q", what, name, v, envCount(env, name), dir)
		}
	}
	for _, kv := range tokenEnv {
		name, _, _ := strings.Cut(kv, "=")
		if _, ok := envValue(env, name); ok {
			t.Fatalf("%s: %s reached claude", what, name)
		}
	}
}

// REQ-CCT-012: every claude of a named account (turn, status probe,
// sign-in, sign-out) gets CLAUDE_CONFIG_DIR and
// CLAUDE_SECURESTORAGE_CONFIG_DIR = its folder and none of the token
// variables; the built-in account keeps the daemon's folder variables.
func TestAccountEnvPerAccount(t *testing.T) {
	work := t.TempDir()
	env := append([]string{"CLAUDE_CONFIG_DIR=/owner/own", "CLAUDE_SECURESTORAGE_CONFIG_DIR=/owner/secure"}, tokenEnv...)
	h := newHarness(t, "hi.jsonl", Options{}, env...)
	ctx := context.Background()
	turn := h.turn("s1")
	turn.Config.Account, turn.Config.ConfigDir = "claude-work", work
	if _, err := h.run(ctx, turn); err != nil {
		t.Fatal(err)
	}
	if _, err := h.run(ctx, h.turn("s2")); err != nil {
		t.Fatal(err)
	}
	account := core.RuntimeAccount{Name: "claude-work", ConfigDir: work}
	h.rt.AccountStatus(ctx, account)
	if err := h.rt.SignIn(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := h.rt.SignOut(ctx, account); err != nil {
		t.Fatal(err)
	}
	starts := h.starts()
	if len(starts) != 2 {
		t.Fatalf("starts = %d", len(starts))
	}
	checkAccountEnv(t, "turn", starts[0].Env, work)
	status := h.entriesOf("status")
	if len(status) != 1 {
		t.Fatalf("status probes = %d", len(status))
	}
	checkAccountEnv(t, "status", status[0].Env, work)
	auth := h.entriesOf("auth")
	if len(auth) != 2 || strings.Join(auth[0].Argv, " ") != "auth login" || strings.Join(auth[1].Argv, " ") != "auth logout" {
		t.Fatalf("auth runs = %+v", auth)
	}
	checkAccountEnv(t, "sign-in", auth[0].Env, work)
	checkAccountEnv(t, "sign-out", auth[1].Env, work)

	builtin := starts[1].Env
	if v, _ := envValue(builtin, "CLAUDE_CONFIG_DIR"); v != "/owner/own" {
		t.Fatalf("built-in CLAUDE_CONFIG_DIR = %q", v)
	}
	if v, _ := envValue(builtin, "CLAUDE_SECURESTORAGE_CONFIG_DIR"); v != "/owner/secure" {
		t.Fatalf("built-in CLAUDE_SECURESTORAGE_CONFIG_DIR = %q", v)
	}
}

// REQ-CCT-012: a sign-in creates the folder (0700), runs `claude auth
// login`, and drops the cached status, so the account shows its new login.
func TestSignInSucceeds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude", "claude-2")
	h := newHarness(t, "hi.jsonl", Options{StatusTTL: time.Hour}, "FAKECLAUDE_AUTH_EXIT=1", "FAKECLAUDE_LOGIN_EMAIL=two@example.com")
	ctx := context.Background()
	account := core.RuntimeAccount{Name: "claude-2", ConfigDir: dir}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "fakeclaude_auth.json"), `{"loggedIn":false,"authMethod":"none"}`)
	if st := h.rt.AccountStatus(ctx, account); st.LoggedIn {
		t.Fatalf("before = %+v", st)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := h.rt.SignIn(ctx, account); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("folder = %v %v", info, err)
	}
	h.setEnv()
	st := h.rt.AccountStatus(ctx, account)
	if !st.LoggedIn || st.Email != "two@example.com" || st.Plan != "pro" {
		t.Fatalf("after sign-in = %+v", st)
	}
	if err := h.rt.SignOut(ctx, account); err != nil {
		t.Fatal(err)
	}
	if st := h.rt.AccountStatus(ctx, account); st.LoggedIn || st.Email != "" {
		t.Fatalf("after sign-out = %+v", st)
	}
}

// REQ-CCT-012: a failed sign-in reports only the last stderr line, with the
// URL replaced and ANSI escapes removed.
func TestSignInFailureIsSanitized(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{}, "FAKECLAUDE_LOGIN=fail")
	err := h.rt.SignIn(context.Background(), core.RuntimeAccount{Name: "claude-2", ConfigDir: t.TempDir()})
	if err == nil {
		t.Fatal("sign-in succeeded")
	}
	if got := err.Error(); got != "Login failed: token exchange refused at sign-in link" {
		t.Fatalf("error = %q", got)
	}
}

// REQ-CCT-012: one sign-in per account at a time; a cancel stops the
// command's process group; another account is not blocked.
func TestSignInCancelAndBusy(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{KillGrace: 500 * time.Millisecond}, "FAKECLAUDE_LOGIN=hang")
	account := core.RuntimeAccount{Name: "claude-2", ConfigDir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.rt.SignIn(ctx, account) }()
	deadline := time.Now().Add(10 * time.Second)
	for len(h.entriesOf("auth")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	auth := h.entriesOf("auth")
	if len(auth) != 1 {
		t.Fatal("the sign-in did not start")
	}
	if err := h.rt.SignIn(context.Background(), account); !errors.Is(err, core.ErrRuntimeSignInRunning) {
		t.Fatalf("second sign-in = %v", err)
	}
	if err := h.rt.SignOut(context.Background(), account); !errors.Is(err, core.ErrRuntimeSignInRunning) {
		t.Fatalf("sign-out during sign-in = %v", err)
	}
	h.setEnv()
	if err := h.rt.SignIn(context.Background(), core.RuntimeAccount{Name: "claude-3", ConfigDir: t.TempDir()}); err != nil {
		t.Fatalf("other account: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled sign-in = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled sign-in did not return")
	}
	if err := syscall.Kill(auth[0].Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("the sign-in process is still alive: %v", err)
	}
	// The account is free again.
	if err := h.rt.SignIn(context.Background(), account); err != nil {
		t.Fatalf("sign-in after cancel: %v", err)
	}
}

// REQ-CCT-012: a sign-in that does not finish in SignInTimeout is stopped.
func TestSignInTimeout(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{SignInTimeout: 300 * time.Millisecond, KillGrace: 300 * time.Millisecond}, "FAKECLAUDE_LOGIN=hang")
	start := time.Now()
	err := h.rt.SignIn(context.Background(), core.RuntimeAccount{Name: "claude-2", ConfigDir: t.TempDir()})
	if err == nil || err.Error() != "Claude Code sign-in timed out. Please try again." {
		t.Fatalf("error = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	auth := h.entriesOf("auth")
	if len(auth) != 1 {
		t.Fatalf("auth runs = %d", len(auth))
	}
	if err := syscall.Kill(auth[0].Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("the sign-in process is still alive: %v", err)
	}
}

// Shutdown cancels a running sign-in.
func TestShutdownCancelsSignIn(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{KillGrace: 300 * time.Millisecond}, "FAKECLAUDE_LOGIN=hang")
	done := make(chan error, 1)
	go func() {
		done <- h.rt.SignIn(context.Background(), core.RuntimeAccount{Name: "claude-2", ConfigDir: t.TempDir()})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for len(h.entriesOf("auth")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.rt.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("sign-in = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the sign-in did not stop")
	}
}

func TestSignInDetail(t *testing.T) {
	cases := map[string]string{
		"first line\n Complete at https://example.test/?code=private\n\n": "Complete at sign-in link",
		"\x1b[31merror:\x1b[0m\tbad   state\r\n":                          "error: bad state",
		"":                                                                "",
		"only\n  \n":                                                      "only",
	}
	for in, want := range cases {
		if got := signInDetail(in); got != want {
			t.Errorf("signInDetail(%q) = %q, want %q", in, got, want)
		}
	}
	if got := signInDetail(strings.Repeat("a", 300)); len([]rune(got)) != 240 {
		t.Fatalf("long line = %d runes", len([]rune(got)))
	}
}
