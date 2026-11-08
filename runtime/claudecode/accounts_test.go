package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// envValue returns the value of name in a logged process environment.
func envValue(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// REQ-CCT-010 (AC-CCT-012, runtime side): a named account's process gets
// CLAUDE_CONFIG_DIR = its folder, replacing the daemon's value; the built-in
// account keeps the daemon's environment as it is.
func TestAccountConfigDirEnv(t *testing.T) {
	work := t.TempDir()
	h := newHarness(t, "hi.jsonl", Options{}, "CLAUDE_CONFIG_DIR=/owner/own")
	turn := h.turn("s1")
	turn.Config.Account, turn.Config.ConfigDir = "claude-work", work
	if _, err := h.run(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if _, err := h.run(context.Background(), h.turn("s2")); err != nil {
		t.Fatal(err)
	}
	starts := h.starts()
	if len(starts) != 2 {
		t.Fatalf("starts = %d", len(starts))
	}
	var dirs []string
	for _, kv := range starts[0].Env {
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			dirs = append(dirs, kv)
		}
	}
	if len(dirs) != 1 || dirs[0] != "CLAUDE_CONFIG_DIR="+work {
		t.Fatalf("account env = %v", dirs)
	}
	if v, _ := envValue(starts[1].Env, "CLAUDE_CONFIG_DIR"); v != "/owner/own" {
		t.Fatalf("built-in account CLAUDE_CONFIG_DIR = %q", v)
	}
}

// REQ-CCT-010: a lana session's Claude session is never resumed on another
// account, and a process never serves two accounts.
func TestSessionsNotSharedAcrossAccounts(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{})
	a := h.turn("s1")
	a.Config.Account, a.Config.ConfigDir = "claude-a", t.TempDir()
	b := h.turn("s1")
	b.Config.Account, b.Config.ConfigDir = "claude-b", t.TempDir()
	for _, turn := range []core.RuntimeTurn{a, a, b, a} {
		if _, err := h.run(context.Background(), turn); err != nil {
			t.Fatal(err)
		}
	}
	starts := h.starts()
	if len(starts) != 3 {
		t.Fatalf("starts = %d (the second turn on claude-a reuses its process)", len(starts))
	}
	first := argValue(starts[0].Argv, "--session-id")
	second := argValue(starts[1].Argv, "--session-id")
	third := argValue(starts[2].Argv, "--session-id")
	if first == "" || second == "" || third == "" || second == first || third == first || third == second {
		t.Fatalf("session ids = %q %q %q; argv %v", first, second, third, starts[2].Argv)
	}
	for _, start := range starts {
		if argValue(start.Argv, "--resume") != "" {
			t.Fatalf("an account resumed another account's session: %v", start.Argv)
		}
	}
	// The same account resumes its own session once its process is gone.
	h.rt.mu.Lock()
	p := h.rt.procs["s1"]
	h.rt.mu.Unlock()
	p.stop(0, time.Second)
	<-p.done
	if _, err := h.run(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	starts = h.starts()
	if got := argValue(starts[len(starts)-1].Argv, "--resume"); got != third {
		t.Fatalf("resume = %q, want %q", got, third)
	}
}

// API-CCT-011 (AC-CCT-013, runtime side): each account's status comes from
// `claude auth status` run with its CLAUDE_CONFIG_DIR: login state, e-mail,
// and plan (subscriptionType, else planTier), cached per account.
func TestAccountStatus(t *testing.T) {
	work, home := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(work, "fakeclaude_auth.json"),
		`{"loggedIn":true,"authMethod":"claude.ai","email":"work@example.com","subscriptionType":"max","orgName":"Org"}`)
	writeFile(t, filepath.Join(home, "fakeclaude_auth.json"),
		`{"loggedIn":true,"authMethod":"claude.ai","email":"home@example.com","planTier":"pro"}`)
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "fakeclaude_auth.json"), `{"loggedIn":false,"authMethod":"none"}`)
	h := newHarness(t, "hi.jsonl", Options{StatusTTL: time.Hour}, "FAKECLAUDE_AUTH_EXIT=0")
	ctx := context.Background()

	st := h.rt.AccountStatus(ctx, core.RuntimeAccount{Name: "claude-work", ConfigDir: work})
	if !st.LoggedIn || st.Email != "work@example.com" || st.Plan != "max" || st.AuthMethod != "claude.ai" || !st.VersionOK || st.Binary != fakeBin {
		t.Fatalf("work = %+v", st)
	}
	st = h.rt.AccountStatus(ctx, core.RuntimeAccount{Name: "claude-home", ConfigDir: home})
	if st.Email != "home@example.com" || st.Plan != "pro" {
		t.Fatalf("home = %+v", st)
	}
	h.setEnv("FAKECLAUDE_AUTH_EXIT=1")
	st = h.rt.AccountStatus(ctx, core.RuntimeAccount{Name: "claude-out", ConfigDir: out})
	if st.LoggedIn || st.Email != "" || st.Error != "" || st.AuthMethod != "" {
		t.Fatalf("logged out = %+v", st)
	}
	st = h.rt.AccountStatus(ctx, core.RuntimeAccount{Name: core.ClaudeCodeProvider})
	if !st.LoggedIn || st.Email != "user@example.com" {
		t.Fatalf("built-in = %+v", st)
	}
	// Cached: a new login is seen only after StatusTTL.
	writeFile(t, filepath.Join(work, "fakeclaude_auth.json"), `{"loggedIn":true,"email":"other@example.com"}`)
	if st := h.rt.AccountStatus(ctx, core.RuntimeAccount{Name: "claude-work", ConfigDir: work}); st.Email != "work@example.com" {
		t.Fatalf("cached = %+v", st)
	}
}

// API-CCT-011: running and queued turns are counted per account.
func TestAccountTurnCounts(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	h := newHarness(t, "gate.jsonl", Options{MaxConcurrent: 1}, "FAKECLAUDE_GATE="+gate)
	a := h.turn("s1")
	a.Config.Account, a.Config.ConfigDir = "claude-a", t.TempDir()
	b := h.turn("s2")
	b.Config.Account, b.Config.ConfigDir = "claude-b", t.TempDir()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errs[0] = h.run(context.Background(), a)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for len(h.starts()) < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errs[1] = h.run(context.Background(), b)
	}()
	for time.Now().Before(deadline) {
		if _, queued := h.rt.accountTurns("claude-b"); queued == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx := context.Background()
	stA := h.rt.AccountStatus(ctx, core.RuntimeAccount{Name: "claude-a", ConfigDir: a.Config.ConfigDir})
	stB := h.rt.AccountStatus(ctx, core.RuntimeAccount{Name: "claude-b", ConfigDir: b.Config.ConfigDir})
	if stA.Running != 1 || stA.Queued != 0 || stB.Running != 0 || stB.Queued != 1 {
		t.Fatalf("a = %d/%d, b = %d/%d", stA.Running, stA.Queued, stB.Running, stB.Queued)
	}
	writeFile(t, gate, "")
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	if r, q := h.rt.accountTurns("claude-a"); r != 0 || q != 0 {
		t.Fatalf("after: a = %d/%d", r, q)
	}
}

// API-CCT-014: Shutdown removes the --mcp-config files of live processes.
func TestShutdownRemovesMCPConfig(t *testing.T) {
	tmp := t.TempDir()
	h := newHarness(t, "hi.jsonl", Options{TempDir: tmp})
	turn := h.turn("s1")
	turn.OpenMCP = func() (string, func(), error) {
		return `{"mcpServers":{"lana":{"type":"http","url":"http://127.0.0.1:1/mcp","headers":{"Authorization":"Bearer t"}}}}`, nil, nil
	}
	if _, err := h.run(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	path := argValue(h.starts()[0].Argv, "--mcp-config")
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file: %v %v", info, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.rt.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config file after shutdown: %v", err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("temp dir = %v", entries)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
