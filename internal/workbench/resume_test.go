package workbench

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestFile writes content to path, creating its parent directories.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// chtimes sets a file's modification time, so session scanning can be ordered.
func chtimes(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// writeSession writes an omp session file: a title line then the session header
// carrying id and cwd.
func writeSession(t *testing.T, path, id, cwd string) {
	t.Helper()
	body := "{\"type\":\"title\",\"title\":\"x\"}\n" +
		"{\"type\":\"session\",\"id\":\"" + id + "\",\"cwd\":\"" + cwd + "\"}\n"
	writeTestFile(t, path, body)
}

func TestClaudeAdapterLookup(t *testing.T) {
	home := t.TempDir()
	const uuid = "0123abcd-0000-0000-0000-000000000000"
	writeTestFile(t, filepath.Join(home, ".claude", "sessions", "4242.json"),
		`{"pid":4242,"sessionId":"`+uuid+`","cwd":"/tmp/project"}`)

	id, dir, ok := lookupAgentSession("claude", agentQuery{Home: home, PID: 4242})
	if !ok || id != uuid || dir != "/tmp/project" {
		t.Fatalf("lookup = (%q, %q, %v), want (%q, /tmp/project, true)", id, dir, ok, uuid)
	}

	// The pid in the file must be the one the core saw.
	if _, _, ok := lookupAgentSession("claude", agentQuery{Home: home, PID: 4243}); ok {
		t.Fatal("lookup with a different pid returned a session")
	}

	// An empty sessionId is not a conversation.
	writeTestFile(t, filepath.Join(home, ".claude", "sessions", "4243.json"),
		`{"pid":4243,"sessionId":"","cwd":"/tmp/empty"}`)
	if _, _, ok := lookupAgentSession("claude", agentQuery{Home: home, PID: 4243}); ok {
		t.Fatal("lookup with an empty sessionId returned a session")
	}
}

func TestClaudeAdapterConfigDirOverride(t *testing.T) {
	home := t.TempDir()
	config := t.TempDir()
	const uuid = "0123abcd-0000-0000-0000-000000000000"
	writeTestFile(t, filepath.Join(config, "sessions", "77.json"),
		`{"pid":77,"sessionId":"`+uuid+`","cwd":"/tmp/env"}`)
	// A file under Home/.claude must be ignored when the environment points
	// somewhere else.
	writeTestFile(t, filepath.Join(home, ".claude", "sessions", "77.json"),
		`{"pid":77,"sessionId":"deadbeef-0000-0000-0000-000000000000","cwd":"/tmp/home"}`)

	q := agentQuery{Home: home, PID: 77, Env: []string{"CLAUDE_CONFIG_DIR=" + config}}
	id, dir, ok := lookupAgentSession("claude", q)
	if !ok || id != uuid || dir != "/tmp/env" {
		t.Fatalf("lookup = (%q, %q, %v), want (%q, /tmp/env, true)", id, dir, ok, uuid)
	}
}

func TestOmpAdapterLookup(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".omp", "agent", "sessions")
	const (
		idA = "0123abcd-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		idB = "0123abcd-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
		idC = "0123abcd-cccc-cccc-cccc-cccccccccccc"
	)
	fileA := filepath.Join(root, "a", "100_"+idA+".jsonl")
	fileB := filepath.Join(root, "b", "200_"+idB+".jsonl")
	fileC := filepath.Join(root, "c", "300_"+idC+".jsonl")
	writeSession(t, fileA, idA, "/tmp/one")
	writeSession(t, fileB, idB, "/tmp/two")
	writeSession(t, fileC, idC, "/tmp/one")

	base := time.Now().Add(-time.Hour)
	chtimes(t, fileA, base)
	chtimes(t, fileB, base.Add(time.Minute))
	chtimes(t, fileC, base.Add(2*time.Minute))

	// The newest file whose header cwd matches the agent's directory wins.
	id, dir, ok := lookupAgentSession("omp", agentQuery{Home: home, PID: 999999, Cwd: "/tmp/one"})
	if !ok || id != idC || dir != "/tmp/one" {
		t.Fatalf("lookup = (%q, %q, %v), want (%q, /tmp/one, true)", id, dir, ok, idC)
	}

	// Only the other directory matches here.
	id, dir, ok = lookupAgentSession("omp", agentQuery{Home: home, PID: 999999, Cwd: "/tmp/two"})
	if !ok || id != idB || dir != "/tmp/two" {
		t.Fatalf("lookup = (%q, %q, %v), want (%q, /tmp/two, true)", id, dir, ok, idB)
	}

	// With no known directory nothing matches.
	if _, _, ok := lookupAgentSession("omp", agentQuery{Home: home, PID: 999999}); ok {
		t.Fatal("lookup with no cwd returned a session")
	}

	// A start time after every file leaves nothing fresh enough.
	if _, _, ok := lookupAgentSession("omp", agentQuery{
		Home: home, PID: 999999, Cwd: "/tmp/one", Started: base.Add(3 * time.Minute),
	}); ok {
		t.Fatal("lookup with a late start returned a session")
	}
}

func TestResumeLine(t *testing.T) {
	const uuid = "0123abcd-0000-0000-0000-000000000000"
	cases := []struct {
		label string
		id    string
		want  string
		ok    bool
	}{
		{"claude", uuid, "claude --resume " + uuid, true},
		{"claude", "x; rm -rf ~", "claude --continue", true},
		{"claude", "", "claude --continue", true},
		{"omp", "", "omp --continue", true},
		{"omp", uuid, "omp --resume " + uuid, true},
		{"cursor-agent", uuid, "", false},
	}
	for _, c := range cases {
		got, ok := resumeLine(c.label, c.id)
		if got != c.want || ok != c.ok {
			t.Errorf("resumeLine(%q, %q) = (%q, %v), want (%q, %v)", c.label, c.id, got, ok, c.want, c.ok)
		}
	}
}
