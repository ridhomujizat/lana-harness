package lana

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
)

// waitClaudeSignIn waits until the account's sign-in is no longer running.
func waitClaudeSignIn(t *testing.T, rt *Runtime, name string) ClaudeSignIn {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if state := rt.ClaudeSignInState(name); state.State != ClaudeSignInRunning {
			return state
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the sign-in of %s is still running", name)
	return ClaudeSignIn{}
}

func claudeAccountByName(t *testing.T, rt *Runtime, name string) ClaudeCodeAccount {
	t.Helper()
	accounts, err := rt.ClaudeCodeAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if account.Name == name {
			return account
		}
	}
	t.Fatalf("no account %s", name)
	return ClaudeCodeAccount{}
}

// REQ-CCT-012 with the fake `claude`: the sign-in state goes running →
// succeeded (and the account then shows its new login), failed (with a
// URL-free error), or cancelled; a second start while one runs is refused;
// a sign-out logs the account out and resets the state.
func TestClaudeSignInStateMachine(t *testing.T) {
	bin := buildFakeClaude(t)
	logPath := filepath.Join(t.TempDir(), "fake.jsonl")
	t.Setenv("FAKECLAUDE_LOG", logPath)
	t.Setenv("FAKECLAUDE_AUTH_EXIT", "1")
	t.Setenv("FAKECLAUDE_LOGIN_MS", "300")
	t.Setenv("FAKECLAUDE_LOGIN_EMAIL", "two@example.com")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	dir := filepath.Join(home, "claude", "claude-2")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "fakeclaude_auth.json"), `{"loggedIn":false,"authMethod":"none"}`)
	cfg := `
[providers.claude-2]
kind = "claude-code"
binary = "` + bin + `"
label = "Second"
`
	rt, err := New(context.Background(), Options{Home: home, Store: openRouterTestStore(t), Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	account := core.RuntimeAccount{Name: "claude-2", ConfigDir: dir, Binary: bin}

	if before := claudeAccountByName(t, rt, "claude-2"); before.Status.LoggedIn {
		t.Fatalf("before = %+v", before.Status)
	}
	if state := rt.ClaudeSignInState("claude-2"); state.State != ClaudeSignInIdle {
		t.Fatalf("idle = %+v", state)
	}
	state, err := rt.StartClaudeSignIn(account)
	if err != nil || state.State != ClaudeSignInRunning || state.StartedAt.IsZero() {
		t.Fatalf("start = %+v %v", state, err)
	}
	if _, err := rt.StartClaudeSignIn(account); !errors.Is(err, ErrClaudeSignInRunning) {
		t.Fatalf("second start = %v", err)
	}
	state = waitClaudeSignIn(t, rt, "claude-2")
	if state.State != ClaudeSignInSucceeded || state.Error != "" || state.FinishedAt.IsZero() {
		t.Fatalf("finished = %+v", state)
	}
	// The status cache was dropped: the account shows its new login at once.
	if after := claudeAccountByName(t, rt, "claude-2"); !after.Status.LoggedIn || after.Status.Email != "two@example.com" {
		t.Fatalf("after = %+v", after.Status)
	}

	t.Setenv("FAKECLAUDE_LOGIN", "fail")
	if _, err := rt.StartClaudeSignIn(account); err != nil {
		t.Fatal(err)
	}
	state = waitClaudeSignIn(t, rt, "claude-2")
	if state.State != ClaudeSignInFailed || state.Error != "Login failed: token exchange refused at sign-in link" {
		t.Fatalf("failed = %+v", state)
	}

	t.Setenv("FAKECLAUDE_LOGIN", "hang")
	if _, err := rt.StartClaudeSignIn(account); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	state = rt.CancelClaudeSignIn("claude-2")
	if state.State != ClaudeSignInCancelled || state.Error != "" {
		t.Fatalf("cancelled = %+v", state)
	}

	t.Setenv("FAKECLAUDE_LOGIN", "")
	if err := rt.ClaudeSignOut(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if state := rt.ClaudeSignInState("claude-2"); state.State != ClaudeSignInIdle {
		t.Fatalf("after sign-out = %+v", state)
	}
	if out := claudeAccountByName(t, rt, "claude-2"); out.Status.LoggedIn {
		t.Fatalf("signed out = %+v", out.Status)
	}

	// Every claude of the account ran with both folder variables; no log line
	// holds a URL.
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	auth := fakeClaudeLog(t, logPath, "auth")
	if len(auth) != 4 {
		t.Fatalf("auth runs = %d", len(auth))
	}
	for _, entry := range auth {
		var secure bool
		for _, kv := range entry["env"].([]any) {
			secure = secure || kv.(string) == "CLAUDE_SECURESTORAGE_CONFIG_DIR="+dir
		}
		if !secure {
			t.Fatalf("auth env = %v", entry["env"])
		}
	}
	if strings.Contains(string(data), "oauth") {
		t.Fatal("the fake log has an OAuth URL; it must only be on the fake's stderr")
	}

	// Close stops a running sign-in.
	t.Setenv("FAKECLAUDE_LOGIN", "hang")
	if _, err := rt.StartClaudeSignIn(account); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if state := waitClaudeSignIn(t, rt, "claude-2"); state.State != ClaudeSignInCancelled {
		t.Fatalf("after close = %+v", state)
	}
}
