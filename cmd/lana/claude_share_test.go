package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/web"
)

// Spec 77 REQ-WRA-001: at daemon start every configured Claude Code account
// folder is linked to the default folder; the built-in account and other
// kinds are left alone, and a missing account folder is skipped.
func TestShareClaudeAccountsAtStart(t *testing.T) {
	home := t.TempDir()
	defaultDir := t.TempDir()
	work := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	writeLiveConfig(t, home, routerWebConfig+`
[providers.claude-work]
kind = "claude-code"
config_dir = "`+work+`"

[providers.claude-gone]
kind = "claude-code"
`)
	folders := claudeAccountFolders(home)
	if len(folders) != 2 || folders[0].Account != "claude-gone" || folders[1].Account != "claude-work" || folders[1].Dir != work {
		t.Fatalf("claudeAccountFolders = %+v", folders)
	}

	defaultJSON := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(defaultJSON, []byte(`{"theme":"dark","oauthAccount":{"emailAddress":"o@example.com"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	shareClaudeAccounts(home, defaultDir, defaultJSON)

	for _, name := range []string{"projects", "history.jsonl"} {
		target, err := os.Readlink(filepath.Join(work, name))
		if err != nil || target != filepath.Join(defaultDir, name) {
			t.Fatalf("%s -> %q (%v)", name, target, err)
		}
	}
	// Spec 77 REQ-WRA-012: the never-signed-in account got the default's
	// settings without the login keys.
	seeded, err := os.ReadFile(filepath.Join(work, ".claude.json"))
	if err != nil || !strings.Contains(string(seeded), `"theme"`) || strings.Contains(string(seeded), "oauthAccount") {
		t.Fatalf(".claude.json = %s (%v)", seeded, err)
	}
	if _, err := os.Stat(filepath.Join(home, "claude", "claude-gone")); !os.IsNotExist(err) {
		t.Fatalf("missing account folder was created: %v", err)
	}
}

// Spec 77 REQ-WRA-001, REQ-WRA-010: only the main daemon, or one started with
// LANA_SHARE_HISTORY=1, links account history into the owner's folders.
func TestShareHistoryGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("LANA_SHARE_HISTORY", "")
	if claudeShareAllowed(home) || agyShareAllowed(home) {
		t.Fatal("a scratch daemon must not share without the opt-in")
	}
	t.Setenv("LANA_SHARE_HISTORY", "yes")
	if claudeShareAllowed(home) || agyShareAllowed(home) {
		t.Fatal("only LANA_SHARE_HISTORY=1 opts in")
	}
	t.Setenv("LANA_SHARE_HISTORY", "1")
	if !claudeShareAllowed(home) || !agyShareAllowed(home) {
		t.Fatal("LANA_SHARE_HISTORY=1 must allow sharing")
	}
}

// Spec 77 REQ-WRA-009, REQ-WRA-010: the hook switch lists the agy config
// folder of every added Antigravity account only while agy is installed, and
// at daemon start every added account HOME that exists shares its
// conversations; the built-in account and a missing HOME are left alone.
func TestAntigravityHookFoldersAndShareAtStart(t *testing.T) {
	home := t.TempDir()
	owner := t.TempDir()
	work := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	writeLiveConfig(t, home, routerWebConfig+`
[providers.antigravity]
kind = "antigravity"

[providers.agy-work]
kind = "antigravity"
home = "`+work+`"

[providers.agy-gone]
kind = "antigravity"
`)
	countAgy := func(folders []web.AgentHookFolder) []web.AgentHookFolder {
		var out []web.AgentHookFolder
		for _, f := range folders {
			if f.Kind == web.AgentHookAntigravity {
				out = append(out, f)
			}
		}
		return out
	}
	if got := countAgy(workbenchHookFolders(home, func() bool { return false })()); len(got) != 0 {
		t.Fatalf("agy not installed: agy folders = %+v", got)
	}
	got := countAgy(workbenchHookFolders(home, func() bool { return true })())
	gone := filepath.Join(home, "antigravity", "agy-gone", ".gemini", "config")
	if len(got) != 2 || got[0].Account != "agy-gone" || got[0].Dir != gone ||
		got[1].Account != "agy-work" || got[1].Dir != filepath.Join(work, ".gemini", "config") {
		t.Fatalf("agy folders = %+v", got)
	}

	// Spec 77 REQ-WRA-011: the owner's agy settings are linked too.
	if err := os.MkdirAll(filepath.Join(owner, ".gemini", "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owner, ".gemini", "config", "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	shareAntigravityAccounts(home, owner)
	if target, err := os.Readlink(filepath.Join(work, ".gemini", "config", "config.json")); err != nil || target != filepath.Join(owner, ".gemini", "config", "config.json") {
		t.Fatalf("config.json -> %q (%v)", target, err)
	}
	for _, name := range []string{"conversations", "brain", "history.jsonl"} {
		target, err := os.Readlink(filepath.Join(work, ".gemini", "antigravity-cli", name))
		if err != nil || target != filepath.Join(owner, ".gemini", "antigravity-cli", name) {
			t.Fatalf("%s -> %q (%v)", name, target, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "antigravity", "agy-gone")); !os.IsNotExist(err) {
		t.Fatalf("missing account HOME was created: %v", err)
	}
}
