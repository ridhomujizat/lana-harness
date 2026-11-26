package workbench

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNamedClaudeAccountDir(t *testing.T) {
	for _, tc := range []struct {
		dir  string
		want bool
	}{
		{"/l/claude/claude-3", true},
		{"/l/claude/claude-3/", true},
		{"/l/claude", false},
		{"/l/claude/a/b", false},
		{"/other/claude/claude-3", false},
		{"claude/claude-3", false},
		{"", false},
		{"/home/o/.claude-swap-backup/sessions/3-o_example.com", false},
	} {
		if got := namedClaudeAccountDir(tc.dir, "/l"); got != tc.want {
			t.Errorf("namedClaudeAccountDir(%q) = %v, want %v", tc.dir, got, tc.want)
		}
	}
	if mirrorRestoredClaude("codex", "/l/claude/claude-3", "/l", "/h") {
		t.Error("a codex pane was mirrored")
	}
}

// AC-SMC-008: a restored claude pane of a named account is mirrored before
// its resume line is typed; a pane of another config dir is not.
func TestRestoreMirrorsNamedClaudeAccount(t *testing.T) {
	home := shortHome(t) // the Lana home
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("SHELL", catScript(t))
	if err := os.WriteFile(filepath.Join(userHome, ".claude.json"), []byte(`{"mcpServers": {"hub": {"type": "http", "url": "https://hub.example.invalid/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(home, "claude", "claude-3")
	other := filepath.Join(t.TempDir(), "elsewhere")
	for _, d := range []string{named, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, ".claude.json"), []byte(`{"numStartups": 2}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	type call struct{ dir string }
	calls := make(chan call, 4)
	orig := mirrorClaudeMCP
	t.Cleanup(func() { mirrorClaudeMCP = orig })
	mirrorClaudeMCP = func(h, dir string) (bool, error) {
		calls <- call{dir}
		return orig(h, dir)
	}

	const uuid = "0123abcd-0000-0000-0000-000000000000"
	dir := t.TempDir()
	now := time.Now()
	recs := []sessionRecord{
		{
			ID: "aaaaaaaaaaaa", Title: "named", Command: []string{"claude"}, Dir: dir, Created: now,
			Cols: 80, Rows: 24, LastDir: dir, Agent: "claude", AgentSession: uuid, AgentCwd: dir,
			AgentSeen: now, AgentConfigDir: named,
		},
		{
			ID: "bbbbbbbbbbbb", Title: "other", Command: []string{"claude"}, Dir: dir, Created: now,
			Cols: 80, Rows: 24, LastDir: dir, Agent: "claude", AgentSession: uuid, AgentCwd: dir,
			AgentSeen: now, AgentConfigDir: other,
		},
	}
	if err := writeRecords(home, recs); err != nil {
		t.Fatal(err)
	}
	serveCore(t, home)
	c := mustClient(t, home)

	stream, err := c.Attach(context.Background(), "aaaaaaaaaaaa", "view")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	want := "claude --resume " + uuid
	if got := readUntil(t, stream, want, 5*time.Second); !bytes.Contains([]byte(got), []byte(want)) || strings.Contains(got, "--mcp-config") {
		t.Fatalf("replay = %q", got)
	}
	// The mirror ran before the line was typed, so the account has it now.
	data, err := os.ReadFile(filepath.Join(named, ".claude.json"))
	if err != nil || !bytes.Contains(data, []byte(`"hub"`)) || !bytes.Contains(data, []byte(`"numStartups": 2`)) {
		t.Fatalf("named account = %s, %v", data, err)
	}
	select {
	case got := <-calls:
		if got.dir != named {
			t.Fatalf("mirrored %q, want %q", got.dir, named)
		}
	default:
		t.Fatal("mirror not called")
	}

	other2 := mustClient(t, home)
	stream2, err := other2.Attach(context.Background(), "bbbbbbbbbbbb", "view")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream2.Close() }()
	_ = readUntil(t, stream2, want, 5*time.Second)
	if data, _ := os.ReadFile(filepath.Join(other, ".claude.json")); string(data) != `{"numStartups": 2}` {
		t.Fatalf("other config dir changed: %s", data)
	}
	select {
	case got := <-calls:
		t.Fatalf("mirror called for %q", got.dir)
	default:
	}
}
