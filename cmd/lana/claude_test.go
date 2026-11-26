package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/claudemcp"
)

// mcpSyncEnv points HOME and LANA_HOME at temp dirs (never the owner's real
// ~/.claude.json or ~/.lana) and writes a config with a claude-code account.
func mcpSyncEnv(t *testing.T) (home, lanaHome string) {
	t.Helper()
	home, lanaHome = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LANA_HOME", lanaHome)
	cfg := "[providers.claude-3]\nkind = \"claude-code\"\n\n[providers.ds]\nkind = \"deepseek\"\n"
	if err := os.WriteFile(filepath.Join(lanaHome, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, lanaHome
}

func runMCPSync(t *testing.T, home string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cmdClaudeMCPSync(args, home, &out, &errOut)
	return code, out.String(), errOut.String()
}

// REQ-SMC-005: `lana claude mcp-sync --account <name>` runs the mirror; it
// exits 0 also when it leaves the account untouched, with a reason on stderr,
// and never prints server entries.
func TestClaudeMCPSync(t *testing.T) {
	home, lanaHome := mcpSyncEnv(t)
	secret := "Bearer do-not-print"
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers": {"hub": {"type": "http", "url": "https://hub.example.invalid/mcp", "headers": {"Authorization": "`+secret+`"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	accountDir := filepath.Join(lanaHome, "claude", "claude-3")

	// No account file yet: untouched, exit 0, reason on stderr.
	code, out, errOut := runMCPSync(t, home, "--account", "claude-3")
	if code != 0 || out != "" || !strings.Contains(errOut, "no .claude.json") {
		t.Fatalf("no account file: %d %q %q", code, out, errOut)
	}

	if err := os.MkdirAll(accountDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(accountDir, ".claude.json"), []byte(`{"numStartups": 3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runMCPSync(t, home, "--account", "claude-3")
	if code != 0 || !strings.Contains(out, "mirrored") || errOut != "" {
		t.Fatalf("mirror: %d %q %q", code, out, errOut)
	}
	data, err := os.ReadFile(filepath.Join(accountDir, ".claude.json"))
	if err != nil || !strings.Contains(string(data), `"hub"`) || !strings.Contains(string(data), `"numStartups": 3`) {
		t.Fatalf("account file = %s, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(accountDir, claudemcp.MarkerName)); err != nil {
		t.Fatalf("marker: %v", err)
	}
	code, out, _ = runMCPSync(t, home, "--account", "claude-3")
	if code != 0 || !strings.Contains(out, "already") {
		t.Fatalf("in sync: %d %q", code, out)
	}

	for _, name := range []string{"ds", "nope", "claude-code"} {
		code, out, errOut = runMCPSync(t, home, "--account", name)
		if code != 0 || out != "" || errOut == "" {
			t.Fatalf("%s: %d %q %q", name, code, out, errOut)
		}
		if strings.Contains(errOut, secret) {
			t.Fatal("secret printed")
		}
	}
	if code, _, _ := runMCPSync(t, home); code != 2 {
		t.Fatalf("no --account: %d, want 2", code)
	}
}
