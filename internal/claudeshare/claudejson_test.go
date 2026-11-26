package claudeshare

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const defaultClaudeJSON = `{
  "theme": "dark",
  "hasCompletedOnboarding": true,
  "projects": {"/repo": {"allowedTools": []}},
  "mcpServers": {"x": {"command": "x"}},
  "unknownKey": [1, 2],
  "oauthAccount": {"emailAddress": "owner@example.com"},
  "primaryApiKey": "sk-secret",
  "customApiKeyResponses": {"approved": ["a"]}
}`

func readObject(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(read(t, path)), &object); err != nil {
		t.Fatal(err)
	}
	return object
}

// checkSeeded checks the account copy: every default key but the login ones,
// mode 0600.
func checkSeeded(t *testing.T, path string) {
	t.Helper()
	got := readObject(t, path)
	for _, key := range []string{"oauthAccount", "primaryApiKey", "customApiKeyResponses"} {
		if _, ok := got[key]; ok {
			t.Fatalf("%s kept in %v", key, got)
		}
	}
	for _, key := range []string{"theme", "hasCompletedOnboarding", "projects", "mcpServers", "unknownKey"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("%s missing in %v", key, got)
		}
	}
	if string(got["theme"]) != `"dark"` {
		t.Fatalf("theme = %s", got["theme"])
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v %v", info, err)
	}
}

// Spec 77 REQ-WRA-012: an account folder without .claude.json gets the
// default's without the login keys.
func TestSeedClaudeJSONAbsent(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), defaultClaudeJSON)
	account := filepath.Join(t.TempDir(), "acct")
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	wrote, err := SeedClaudeJSON(account, ClaudeJSONPath(filepath.Join(home, ".claude"), false))
	if err != nil || !wrote {
		t.Fatalf("wrote = %v, err %v", wrote, err)
	}
	checkSeeded(t, filepath.Join(account, ".claude.json"))
	if read(t, filepath.Join(home, ".claude.json")) != defaultClaudeJSON {
		t.Fatal("default file changed")
	}
}

// Spec 77 REQ-WRA-012: a .claude.json without oauthAccount (never signed in)
// is replaced by the copy.
func TestSeedClaudeJSONWithoutOAuthAccount(t *testing.T) {
	defaultDir := t.TempDir()
	write(t, filepath.Join(defaultDir, ".claude.json"), defaultClaudeJSON)
	account := t.TempDir()
	write(t, filepath.Join(account, ".claude.json"), `{"firstStartTime":"x","numStartups":1}`)
	wrote, err := SeedClaudeJSON(account, ClaudeJSONPath(defaultDir, true))
	if err != nil || !wrote {
		t.Fatalf("wrote = %v, err %v", wrote, err)
	}
	checkSeeded(t, filepath.Join(account, ".claude.json"))
}

// Spec 77 REQ-WRA-012: a signed-in account's .claude.json is never touched,
// nor a symlink, nor anything when the default file or the account folder is
// missing.
func TestSeedClaudeJSONLeavesAlone(t *testing.T) {
	defaultDir := t.TempDir()
	defaultJSON := filepath.Join(defaultDir, ".claude.json")
	write(t, defaultJSON, defaultClaudeJSON)

	signedIn := t.TempDir()
	own := `{"oauthAccount":{"emailAddress":"b@example.com"},"theme":"light"}`
	write(t, filepath.Join(signedIn, ".claude.json"), own)
	if wrote, err := SeedClaudeJSON(signedIn, defaultJSON); err != nil || wrote {
		t.Fatalf("signed in: wrote = %v, err %v", wrote, err)
	}
	if read(t, filepath.Join(signedIn, ".claude.json")) != own {
		t.Fatal("signed-in file changed")
	}

	linked := t.TempDir()
	if err := os.Symlink(defaultJSON, filepath.Join(linked, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if wrote, err := SeedClaudeJSON(linked, defaultJSON); err != nil || wrote {
		t.Fatalf("symlink: wrote = %v, err %v", wrote, err)
	}

	empty := t.TempDir()
	if wrote, err := SeedClaudeJSON(empty, filepath.Join(t.TempDir(), ".claude.json")); err != nil || wrote {
		t.Fatalf("no default: wrote = %v, err %v", wrote, err)
	}
	notExists(t, filepath.Join(empty, ".claude.json"))

	if wrote, err := SeedClaudeJSON(filepath.Join(t.TempDir(), "missing"), defaultJSON); err != nil || wrote {
		t.Fatalf("missing folder: wrote = %v, err %v", wrote, err)
	}
	// The default folder itself (CLAUDE_CONFIG_DIR = the account) is a no-op.
	if wrote, err := SeedClaudeJSON(defaultDir, defaultJSON); err != nil || wrote {
		t.Fatalf("same file: wrote = %v, err %v", wrote, err)
	}

	broken := t.TempDir()
	write(t, filepath.Join(broken, ".claude.json"), "not json")
	if wrote, err := SeedClaudeJSON(broken, defaultJSON); err == nil || wrote {
		t.Fatalf("broken account file: wrote = %v, err %v", wrote, err)
	}
	if read(t, filepath.Join(broken, ".claude.json")) != "not json" {
		t.Fatal("broken file changed")
	}
}

func TestClaudeJSONPath(t *testing.T) {
	if got := ClaudeJSONPath("/h/.claude", false); got != "/h/.claude.json" {
		t.Fatalf("implicit = %s", got)
	}
	if got := ClaudeJSONPath("/cfg", true); got != "/cfg/.claude.json" {
		t.Fatalf("explicit = %s", got)
	}
	if got := ClaudeJSONPath("", false); got != "" {
		t.Fatalf("empty = %s", got)
	}
}
