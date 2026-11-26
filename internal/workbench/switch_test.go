//go:build darwin || linux

package workbench

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// switchTestID is a conversation id of the canonical claude shape.
const switchTestID = "0b1f4031-611b-4c77-8b94-2b941e4634f0"

// fakeClaudeBin writes the fake programs a switch test runs, never a real
// claude: "fakeclaude" is the conversation running in the pane's foreground
// (trap is its INT handling), and "claude" is what the typed resume line
// starts, which only prints what it got.
func fakeClaudeBin(t *testing.T, trap string) string {
	t.Helper()
	bin := t.TempDir()
	fake := "#!/bin/sh\n" + trap + "\necho fake-claude-\"\"ready\n" +
		"i=0\nwhile [ $i -lt 400 ]; do sleep 0.05; i=$((i+1)); done\n"
	resumed := "#!/bin/sh\necho \"resumed-\"\"on config=[$CLAUDE_CONFIG_DIR] store=[$CLAUDE_SECURESTORAGE_CONFIG_DIR] args=[$*]\"\n"
	for name, body := range map[string]string{"fakeclaude": fake, "claude": resumed} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// exitOnSecondInt is a claude that exits on the second Ctrl+C, like Claude
// Code at its prompt.
const exitOnSecondInt = `n=0
trap 'n=$((n+1)); if [ $n -ge 2 ]; then echo fake-claude-exit; exit 0; fi' INT`

// ignoreInt is a claude that never exits on Ctrl+C.
const ignoreInt = `trap '' INT`

// switchSession starts an interactive /bin/sh pane with bin first on its PATH.
func switchSession(t *testing.T, bin string, command []string) *session {
	t.Helper()
	ss, err := newSession(newID(), CreateOptions{
		Command: command,
		Dir:     t.TempDir(),
		// Wide enough that the echo of a typed line never wraps.
		Cols: 1000,
		Env:  []string{"PATH=" + bin + ":/usr/bin:/bin", "PS1=$ ", "ENV=", "CLAUDE_CONFIG_DIR=/pane/own", "CLAUDE_SECURESTORAGE_CONFIG_DIR=/pane/own"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ss.kill)
	return ss
}

// output returns everything the session printed so far.
func (s *session) output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.ring.snapshot())
}

// waitOutput waits until the session printed want.
func waitOutput(t *testing.T, ss *session, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(ss.output(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q; output:\n%s", want, ss.output())
}

// runFakeClaude types fakeclaude into the pane's shell and waits for it, then
// records it as the pane's claude conversation the way the lookup would.
func runFakeClaude(t *testing.T, ss *session, configDir string) {
	t.Helper()
	waitOutput(t, ss, "$ ")
	if _, err := ss.p.write([]byte("fakeclaude\r")); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, ss, "fake-claude-ready")
	ss.mu.Lock()
	ss.rAgent, ss.agentSession, ss.agentConfigDir, ss.agentSeen = "claude", switchTestID, configDir, time.Now()
	ss.mu.Unlock()
}

// AC-WAS-001: a claude that exits on the second Ctrl+C is stopped, the named
// account's resume line is typed into the shell once it exited, and the record
// carries the new config dir.
func TestSwitchAccountNamed(t *testing.T) {
	bin := fakeClaudeBin(t, exitOnSecondInt)
	ss := switchSession(t, bin, []string{"/bin/sh"})
	runFakeClaude(t, ss, "")
	if info := ss.info(); info.AccountSwitch != SwitchReady || info.ConversationID != switchTestID || info.AgentConfigDir != "" {
		t.Fatalf("before switch: %+v", info)
	}
	account := t.TempDir()
	if err := ss.switchAccount(SwitchOptions{ConfigDir: account}); err != nil {
		t.Fatalf("switch: %v\n%s", err, ss.output())
	}
	waitOutput(t, ss, "resumed-on config=["+account+"] store=["+account+"] args=[--resume "+switchTestID+"]")
	out := ss.output()
	if !strings.Contains(out, "fake-claude-exit") {
		t.Fatalf("claude did not get the second Ctrl+C:\n%s", out)
	}
	line := "CLAUDE_CONFIG_DIR='" + account + "' CLAUDE_SECURESTORAGE_CONFIG_DIR='" + account + "' claude --resume " + switchTestID
	if strings.Count(out, line) != 1 {
		t.Fatalf("typed line %q not echoed once:\n%s", line, out)
	}
	// It was typed after claude exited, not into it.
	if strings.Index(out, line) < strings.Index(out, "fake-claude-exit") {
		t.Fatalf("line typed before claude exited:\n%s", out)
	}
	info := ss.info()
	if info.AgentConfigDir != account || info.ConversationID != switchTestID {
		t.Fatalf("after switch: %+v", info)
	}
	rec := ss.record()
	if rec.AgentConfigDir != account || rec.AgentSession != switchTestID {
		t.Fatalf("record after switch: %+v", rec)
	}
}

// A pane whose claude already exited (the shell owns the terminal) switches
// to the built-in account with the env -u form, which drops the pane's own
// config variables.
func TestSwitchAccountBuiltinAtPrompt(t *testing.T) {
	bin := fakeClaudeBin(t, exitOnSecondInt)
	ss := switchSession(t, bin, []string{"/bin/sh"})
	waitOutput(t, ss, "$ ")
	ss.mu.Lock()
	ss.rAgent, ss.agentSession, ss.agentConfigDir, ss.agentSeen = "claude", switchTestID, "/some/account", time.Now()
	ss.mu.Unlock()
	if err := ss.switchAccount(SwitchOptions{}); err != nil {
		t.Fatalf("switch: %v\n%s", err, ss.output())
	}
	waitOutput(t, ss, "resumed-on config=[] store=[] args=[--resume "+switchTestID+"]")
	if !strings.Contains(ss.output(), "env -u CLAUDE_CONFIG_DIR -u CLAUDE_SECURESTORAGE_CONFIG_DIR claude --resume "+switchTestID) {
		t.Fatalf("built-in line not typed:\n%s", ss.output())
	}
	if info := ss.info(); info.AgentConfigDir != "" {
		t.Fatalf("config dir after built-in switch = %q", info.AgentConfigDir)
	}
}

// AC-WAS-002: a claude that ignores Ctrl+C keeps the terminal: still_running
// within the bound, and no resume line is typed.
func TestSwitchAccountStillRunning(t *testing.T) {
	bin := fakeClaudeBin(t, ignoreInt)
	ss := switchSession(t, bin, []string{"/bin/sh"})
	runFakeClaude(t, ss, "")
	start := time.Now()
	err := ss.switchAccount(SwitchOptions{ConfigDir: t.TempDir()})
	elapsed := time.Since(start)
	if !errors.Is(err, ErrStillRunning) {
		t.Fatalf("switch = %v, want ErrStillRunning", err)
	}
	if elapsed > switchTimeout+time.Second {
		t.Fatalf("switch took %v", elapsed)
	}
	time.Sleep(200 * time.Millisecond)
	if out := ss.output(); strings.Contains(out, "--resume") {
		t.Fatalf("a line was typed:\n%s", out)
	}
	if info := ss.info(); info.AgentConfigDir != "" {
		t.Fatalf("config dir changed: %q", info.AgentConfigDir)
	}
}

// AC-WAS-003: a pane without a conversation id is no_conversation; a pane
// whose own command is claude is not_switchable; neither is touched.
func TestSwitchAccountRefusals(t *testing.T) {
	bin := fakeClaudeBin(t, exitOnSecondInt)

	shell := switchSession(t, bin, []string{"/bin/sh"})
	waitOutput(t, shell, "$ ")
	for _, rec := range []struct{ agent, id string }{{"", ""}, {"claude", ""}, {"claude", "not-a-uuid"}, {"omp", switchTestID}} {
		shell.mu.Lock()
		shell.rAgent, shell.agentSession = rec.agent, rec.id
		shell.mu.Unlock()
		if info := shell.info(); info.AccountSwitch != CodeNoConversation || info.ConversationID != "" {
			t.Fatalf("%+v: info = %+v", rec, info)
		}
		if err := shell.switchAccount(SwitchOptions{}); !errors.Is(err, ErrNoConversation) || err.Error() != "No Claude conversation in this pane" {
			t.Fatalf("%+v: switch = %v", rec, err)
		}
	}

	own := switchSession(t, bin, []string{filepath.Join(bin, "claude")})
	own.mu.Lock()
	own.rAgent, own.agentSession = "claude", switchTestID
	own.mu.Unlock()
	if info := own.info(); info.AccountSwitch != CodeNotSwitchable {
		t.Fatalf("own claude info = %+v", info)
	}
	err := own.switchAccount(SwitchOptions{})
	if !errors.Is(err, ErrNotSwitchable) || err.Error() != "This pane was opened before account switching; use New tab" {
		t.Fatalf("own claude switch = %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	if out := shell.output(); strings.Contains(out, "^C") || strings.Contains(out, "--resume") {
		t.Fatalf("refused pane was touched:\n%s", out)
	}
}

// Only an interactive shell is switchable.
func TestSwitchableCommand(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want bool
	}{
		{[]string{"/bin/zsh"}, true},
		{[]string{"-zsh"}, true},
		{[]string{"/bin/bash", "-l"}, true},
		{[]string{"/bin/bash", "--login", "--norc"}, true},
		{[]string{"/usr/local/bin/claude"}, false},
		{[]string{"env", "CLAUDE_CONFIG_DIR=/x", "claude"}, false},
		{[]string{"/bin/sh", "-c", "claude"}, false},
		{[]string{"/bin/zsh", "-lc", "claude"}, false},
		{[]string{"/bin/sh", "script.sh"}, false},
		{legacyInputOptions(CreateOptions{Input: "claude"}).Command, false},
		{nil, false},
	} {
		if got := switchableCommand(tc.argv); got != tc.want {
			t.Errorf("switchableCommand(%q) = %v, want %v", tc.argv, got, tc.want)
		}
	}
}

// The resume line: the spec 60 form with the account's dir for a named
// account, env -u for the built-in one, and nothing for a dir that cannot be
// typed or does not exist.
func TestSwitchResumeLine(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	got, err := switchResumeLine("claude", switchTestID, dir, home, nil)
	want := "CLAUDE_CONFIG_DIR='" + dir + "' CLAUDE_SECURESTORAGE_CONFIG_DIR='" + dir + "' claude --resume " + switchTestID
	if err != nil || got != want {
		t.Fatalf("named = %q, %v; want %q", got, err, want)
	}
	got, err = switchResumeLine("claude", switchTestID, "", home, nil)
	if err != nil || got != "env -u CLAUDE_CONFIG_DIR -u CLAUDE_SECURESTORAGE_CONFIG_DIR claude --resume "+switchTestID {
		t.Fatalf("built-in = %q, %v", got, err)
	}
	quoted := filepath.Join(t.TempDir(), "it's")
	if err := os.Mkdir(quoted, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{quoted, filepath.Join(dir, "missing"), "relative/dir"} {
		if got, err := switchResumeLine("claude", switchTestID, bad, home, nil); err == nil {
			t.Fatalf("%q: line %q, want an error", bad, got)
		}
	}
}

// Over the wire: the core advertises switch_account, its refusals keep their
// code, and a client facing a core without the feature answers core_outdated
// without asking it.
func TestSwitchAccountWire(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	h, err := c.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.Features, FeatureSwitchAccount) {
		t.Fatalf("hello features = %v", h.Features)
	}
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	if s.AccountSwitch != CodeNoConversation {
		t.Fatalf("created session account_switch = %q", s.AccountSwitch)
	}
	_, err = c.SwitchAccount(ctx, s.ID, "claude", "")
	if !errors.Is(err, ErrNoConversation) || err.Error() != ErrNoConversation.Error() {
		t.Fatalf("switch = %v, want no_conversation", err)
	}
	if _, err := c.SwitchAccount(ctx, "nope", "claude", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("switch unknown = %v", err)
	}

	old, _ := fakeCoreClient(t, []string{FeatureCreateInput})
	if _, err := old.SwitchAccount(ctx, "s1", "claude", ""); !errors.Is(err, ErrCoreOutdated) {
		t.Fatalf("old core switch = %v, want ErrCoreOutdated", err)
	}
}
