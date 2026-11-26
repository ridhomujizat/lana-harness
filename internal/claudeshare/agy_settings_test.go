package claudeshare

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// agyLoginState lists agy's login and state files of an account HOME (spec 77
// REQ-WRA-011): never read, linked or moved. cache/onboarding.json is listed
// because these tests give the owner none, so REQ-WRA-015 leaves it alone.
var agyLoginState = []string{
	".gemini/antigravity-cli/antigravity-oauth-token",
	".gemini/antigravity-cli/cache/acp-auth-state.json",
	".gemini/antigravity-cli/cache/default_project_id.txt",
	".gemini/antigravity-cli/cache/onboarding.json",
	".gemini/antigravity-cli/installation_id",
	".gemini/antigravity-cli/jetski_state.pbtxt",
	".gemini/config/hooks.json",
}

// Spec 77 REQ-WRA-011: a new account HOME (before its first sign-in) gets the
// owner's agy settings linked, only those the owner has.
func TestLinkAntigravitySettingsNewAccount(t *testing.T) {
	owner, account := t.TempDir(), t.TempDir()
	write(t, filepath.Join(owner, ".gemini", "antigravity-cli", "settings.json"), `{"model":"x"}`)
	write(t, filepath.Join(owner, ".gemini", "config", "config.json"), `{"plugins":[]}`)
	write(t, filepath.Join(owner, ".gemini", "config", "skills", "a", "SKILL.md"), "skill")
	// The owner has no mcp_config.json and a hooks.json that must not be linked.
	write(t, filepath.Join(owner, ".gemini", "config", "hooks.json"), "{}")

	report, err := LinkAntigravity(account, owner, testNow)
	if err != nil {
		t.Fatal(err)
	}
	got := actions(report)
	for _, name := range []string{".gemini/antigravity-cli/settings.json", ".gemini/config/config.json", ".gemini/config/skills"} {
		if got[name] != Linked {
			t.Fatalf("%s action = %s", name, got[name])
		}
		if target := linkTarget(t, filepath.Join(account, filepath.FromSlash(name))); target != filepath.Join(owner, filepath.FromSlash(name)) {
			t.Fatalf("%s -> %s", name, target)
		}
	}
	if got[".gemini/config/mcp_config.json"] != Skipped {
		t.Fatalf("mcp_config action = %s", got[".gemini/config/mcp_config.json"])
	}
	notExists(t, filepath.Join(account, ".gemini", "config", "mcp_config.json"))
	notExists(t, filepath.Join(account, ".gemini", "config", "hooks.json"))
	info, err := os.Stat(filepath.Join(account, ".gemini", "config"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("account config folder: %v %v", info, err)
	}

	// A second run keeps every link.
	again, err := LinkAntigravity(account, owner, testNow)
	if err != nil || again.Changed() {
		t.Fatalf("second run changed = %v, err %v", again.Changed(), err)
	}
}

// Spec 77 REQ-WRA-011: the defaults agy wrote at the first sign-in are moved
// aside and replaced by links to the owner's; the login and state files and
// hooks.json stay exactly as they are.
func TestLinkAntigravitySettingsAdoptsDefaults(t *testing.T) {
	owner, account := t.TempDir(), t.TempDir()
	for _, name := range agySettingsNames {
		path := filepath.Join(owner, filepath.FromSlash(name))
		if filepath.Base(name) == "skills" {
			path = filepath.Join(path, "s", "SKILL.md")
		}
		write(t, path, "owner")
	}
	for _, name := range agySettingsNames {
		path := filepath.Join(account, filepath.FromSlash(name))
		if filepath.Base(name) == "skills" {
			path = filepath.Join(path, "d", "SKILL.md")
		}
		write(t, path, "default")
	}
	for _, name := range agyLoginState {
		write(t, filepath.Join(account, filepath.FromSlash(name)), "state:"+name)
	}

	report, err := LinkAntigravity(account, owner, testNow)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Item{}
	for _, item := range report.Items {
		byName[item.Name] = item
	}
	for _, name := range agySettingsNames {
		item := byName[name]
		src := filepath.Join(account, filepath.FromSlash(name))
		if item.Action != Adopted || item.Backup != src+".before-share-1700000000" {
			t.Fatalf("%s item = %+v", name, item)
		}
		if target := linkTarget(t, src); target != filepath.Join(owner, filepath.FromSlash(name)) {
			t.Fatalf("%s -> %s", name, target)
		}
		if _, err := os.Lstat(item.Backup); err != nil {
			t.Fatalf("%s backup: %v", name, err)
		}
	}
	if read(t, filepath.Join(account, ".gemini", "config", "config.json.before-share-1700000000")) != "default" {
		t.Fatal("backup content changed")
	}
	if read(t, filepath.Join(owner, ".gemini", "config", "config.json")) != "owner" {
		t.Fatal("owner settings changed")
	}
	for _, name := range agyLoginState {
		path := filepath.Join(account, filepath.FromSlash(name))
		if isLink(path) || read(t, path) != "state:"+name {
			t.Fatalf("%s touched", name)
		}
	}
}

// Spec 77 REQ-WRA-011: an account link (any target) is kept, and an account
// item stays when the owner has none.
func TestLinkAntigravitySettingsKeeps(t *testing.T) {
	owner, account, other := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, filepath.Join(owner, ".gemini", "config", "config.json"), "owner")
	write(t, filepath.Join(other, "config.json"), "other")
	if err := os.MkdirAll(filepath.Join(account, ".gemini", "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, "config.json"), filepath.Join(account, ".gemini", "config", "config.json")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(account, ".gemini", "antigravity-cli", "settings.json"), "own")

	report, err := LinkAntigravity(account, owner, testNow)
	if err != nil {
		t.Fatal(err)
	}
	got := actions(report)
	if got[".gemini/config/config.json"] != Kept || got[".gemini/antigravity-cli/settings.json"] != Skipped {
		t.Fatalf("actions = %v", got)
	}
	if linkTarget(t, filepath.Join(account, ".gemini", "config", "config.json")) != filepath.Join(other, "config.json") {
		t.Fatal("existing link replaced")
	}
	if read(t, filepath.Join(account, ".gemini", "antigravity-cli", "settings.json")) != "own" {
		t.Fatal("account settings changed without an owner item")
	}
}

// Spec 83 REQ-WAG-004: an account mcp_config.json that `agy mcp add` turned
// into a file is renamed and linked again; a link is kept; the built-in
// account and a missing owner file are left alone.
func TestRelinkAgyMCPConfig(t *testing.T) {
	owner, account := t.TempDir(), t.TempDir()
	now := time.Unix(1700000000, 0)
	src := filepath.Join(account, ".gemini", "config", "mcp_config.json")
	dst := filepath.Join(owner, ".gemini", "config", "mcp_config.json")

	// No owner file: nothing happens.
	if item, err := RelinkAgyMCPConfig(account, owner, now); err != nil || item.Action != Skipped {
		t.Fatalf("no owner file = %+v, %v", item, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte(`{"mcpServers":{"hub":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte(`{"mcpServers":{"hub":{},"new":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	item, err := RelinkAgyMCPConfig(account, owner, now)
	if err != nil || item.Action != Adopted || item.Backup != src+".before-share-1700000000" {
		t.Fatalf("relink = %+v, %v", item, err)
	}
	if target, err := os.Readlink(src); err != nil || target != dst {
		t.Fatalf("link = %q, %v", target, err)
	}
	if data, err := os.ReadFile(item.Backup); err != nil || !strings.Contains(string(data), `"new"`) {
		t.Fatalf("backup = %s, %v", data, err)
	}
	if item, err := RelinkAgyMCPConfig(account, owner, now); err != nil || item.Action != Kept {
		t.Fatalf("second relink = %+v, %v", item, err)
	}
	if item, err := RelinkAgyMCPConfig("", owner, now); err != nil || item.Action != Skipped {
		t.Fatalf("built-in = %+v, %v", item, err)
	}
	if item, err := RelinkAgyMCPConfig(owner, owner, now); err != nil || item.Action != Skipped {
		t.Fatalf("owner HOME = %+v, %v", item, err)
	}
}
