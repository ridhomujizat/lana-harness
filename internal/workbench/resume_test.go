package workbench

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	found, ok := lookupAgentSession("claude", agentQuery{Home: home, PID: 4242})
	if !ok || found.ID != uuid || found.Dir != "/tmp/project" || found.ConfigDir != "" {
		t.Fatalf("lookup = %+v, %v, want {%q /tmp/project } true", found, ok, uuid)
	}

	// The pid in the file must be the one the core saw.
	if _, ok := lookupAgentSession("claude", agentQuery{Home: home, PID: 4243}); ok {
		t.Fatal("lookup with a different pid returned a session")
	}

	// An empty sessionId is not a conversation.
	writeTestFile(t, filepath.Join(home, ".claude", "sessions", "4243.json"),
		`{"pid":4243,"sessionId":"","cwd":"/tmp/empty"}`)
	if _, ok := lookupAgentSession("claude", agentQuery{Home: home, PID: 4243}); ok {
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
	found, ok := lookupAgentSession("claude", q)
	if !ok || found.ID != uuid || found.Dir != "/tmp/env" || found.ConfigDir != config {
		t.Fatalf("lookup = %+v, %v, want {%q /tmp/env %q} true", found, ok, uuid, config)
	}
}

func TestClaudeAdapterProcEnvConfigDir(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(t.TempDir(), "3-owner_example.com")
	const uuid = "0123abcd-0000-0000-0000-000000000000"
	writeTestFile(t, filepath.Join(profile, "sessions", "99.json"),
		`{"pid":99,"sessionId":"`+uuid+`","cwd":"/tmp/swap"}`)
	// A decoy under Home/.claude must be ignored: the agent process's own
	// CLAUDE_CONFIG_DIR wins, which is how a cswap pane is found.
	writeTestFile(t, filepath.Join(home, ".claude", "sessions", "99.json"),
		`{"pid":99,"sessionId":"deadbeef-0000-0000-0000-000000000000","cwd":"/tmp/home"}`)

	q := agentQuery{Home: home, PID: 99, ProcEnv: []string{"CLAUDE_CONFIG_DIR=" + profile}}
	found, ok := lookupAgentSession("claude", q)
	if !ok || found.ID != uuid || found.Dir != "/tmp/swap" || found.ConfigDir != profile {
		t.Fatalf("lookup = %+v, %v, want {%q /tmp/swap %q} true", found, ok, uuid, profile)
	}
}

func TestClaudeConfigDirPrecedence(t *testing.T) {
	home := t.TempDir()
	procDir := filepath.Join(t.TempDir(), "proc")
	envDir := filepath.Join(t.TempDir(), "env")
	const uuid = "0123abcd-0000-0000-0000-000000000000"
	writeTestFile(t, filepath.Join(procDir, "sessions", "1.json"),
		`{"pid":1,"sessionId":"`+uuid+`","cwd":"/p"}`)
	writeTestFile(t, filepath.Join(envDir, "sessions", "1.json"),
		`{"pid":1,"sessionId":"deadbeef-0000-0000-0000-000000000000","cwd":"/e"}`)

	// The process environment wins over the session environment.
	q := agentQuery{Home: home, PID: 1,
		ProcEnv: []string{"CLAUDE_CONFIG_DIR=" + procDir},
		Env:     []string{"CLAUDE_CONFIG_DIR=" + envDir}}
	found, ok := lookupAgentSession("claude", q)
	if !ok || found.ID != uuid || found.ConfigDir != procDir {
		t.Fatalf("proc env did not win: %+v, %v", found, ok)
	}

	// With only the session environment set, it is used.
	q = agentQuery{Home: home, PID: 1, Env: []string{"CLAUDE_CONFIG_DIR=" + envDir}}
	if found, ok := lookupAgentSession("claude", q); !ok || found.ConfigDir != envDir {
		t.Fatalf("session env not used: %+v, %v", found, ok)
	}

	// Neither set: <home>/.claude, and no explicit config dir.
	writeTestFile(t, filepath.Join(home, ".claude", "sessions", "1.json"),
		`{"pid":1,"sessionId":"`+uuid+`","cwd":"/h"}`)
	found, ok = lookupAgentSession("claude", agentQuery{Home: home, PID: 1})
	if !ok || found.ID != uuid || found.ConfigDir != "" {
		t.Fatalf("default config dir = %+v, %v, want ConfigDir \"\"", found, ok)
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
	found, ok := lookupAgentSession("omp", agentQuery{Home: home, PID: 999999, Cwd: "/tmp/one"})
	if !ok || found.ID != idC || found.Dir != "/tmp/one" || found.ConfigDir != "" {
		t.Fatalf("lookup = %+v, %v, want {%q /tmp/one } true", found, ok, idC)
	}

	// Only the other directory matches here.
	found, ok = lookupAgentSession("omp", agentQuery{Home: home, PID: 999999, Cwd: "/tmp/two"})
	if !ok || found.ID != idB || found.Dir != "/tmp/two" {
		t.Fatalf("lookup = %+v, %v, want {%q /tmp/two } true", found, ok, idB)
	}

	// With no known directory nothing matches.
	if _, ok := lookupAgentSession("omp", agentQuery{Home: home, PID: 999999}); ok {
		t.Fatal("lookup with no cwd returned a session")
	}

	// A start time after every file leaves nothing fresh enough.
	if _, ok := lookupAgentSession("omp", agentQuery{
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
		got, ok := resumeLine(c.label, c.id, "", "", nil)
		if got != c.want || ok != c.ok {
			t.Errorf("resumeLine(%q, %q) = (%q, %v), want (%q, %v)", c.label, c.id, got, ok, c.want, c.ok)
		}
	}
}

func TestResumeLineClaudeConfigDir(t *testing.T) {
	home := t.TempDir()
	const uuid = "0123abcd-0000-0000-0000-000000000000"

	// A claude-swap profile whose account email can be read.
	profile := filepath.Join(home, ".claude-swap-backup", "sessions", "3-owner_example.com")
	writeTestFile(t, filepath.Join(profile, ".claude.json"),
		`{"oauthAccount":{"emailAddress":"a@b.c"}}`)

	// A profile with no .claude.json, and one with a bad email.
	noEmail := filepath.Join(home, ".claude-swap-backup", "sessions", "4-owner_example.com")
	writeTestFile(t, filepath.Join(noEmail, "sessions", "keep"), "")
	badEmail := filepath.Join(home, ".claude-swap-backup", "sessions", "5-owner_example.com")
	writeTestFile(t, filepath.Join(badEmail, ".claude.json"),
		`{"oauthAccount":{"emailAddress":"not an email"}}`)

	// A non-cswap custom config dir that exists, and one with a quote.
	custom := t.TempDir()
	quoted := filepath.Join(t.TempDir(), "we'ird")
	writeTestFile(t, filepath.Join(quoted, "x"), "")

	orig := cswapInstalled
	t.Cleanup(func() { cswapInstalled = orig })
	install := func(v bool) { cswapInstalled = func([]string, string) bool { return v } }

	cases := []struct {
		name      string
		id        string
		configDir string
		installed bool
		want      string
	}{
		{"no config dir", uuid, "", true, "claude --resume " + uuid},
		{"no config dir, no id", "", "", true, "claude --continue"},
		{"cswap profile", uuid, profile, true, "cswap run a@b.c -- --resume " + uuid},
		{"cswap profile, no id", "", profile, true, "cswap run a@b.c -- --continue"},
		{"cswap profile not installed", uuid, profile, false, "CLAUDE_CONFIG_DIR='" + profile + "' claude --resume " + uuid},
		{"profile without email", uuid, noEmail, true, "CLAUDE_CONFIG_DIR='" + noEmail + "' claude --resume " + uuid},
		{"profile with bad email", uuid, badEmail, true, "CLAUDE_CONFIG_DIR='" + badEmail + "' claude --resume " + uuid},
		{"custom config dir", uuid, custom, true, "CLAUDE_CONFIG_DIR='" + custom + "' claude --resume " + uuid},
		{"custom config dir, no id", "", custom, true, "CLAUDE_CONFIG_DIR='" + custom + "' claude --continue"},
		{"config dir with a quote", uuid, quoted, true, "claude --resume " + uuid},
		{"config dir gone", uuid, filepath.Join(home, "gone"), true, "claude --resume " + uuid},
	}
	for _, c := range cases {
		install(c.installed)
		got, ok := resumeLine("claude", c.id, c.configDir, home, nil)
		if !ok || got != c.want {
			t.Errorf("%s: resumeLine = (%q, %v), want (%q, true)", c.name, got, ok, c.want)
		}
	}

	// The omp resume line is unchanged even when a config dir is passed.
	cswapInstalled = func([]string, string) bool { return true }
	if got, ok := resumeLine("omp", uuid, profile, home, nil); !ok || got != "omp --resume "+uuid {
		t.Errorf("omp resumeLine = (%q, %v), want unchanged", got, ok)
	}
}

func TestCswapInstalled(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// Nothing installed yet.
	if cswapInstalled(nil, home) {
		t.Fatal("cswapInstalled found cswap in an empty home")
	}
	// A non-executable file does not count.
	if err := os.WriteFile(filepath.Join(bin, "cswap"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cswapInstalled(nil, home) {
		t.Fatal("cswapInstalled accepted a non-executable file")
	}
	// An executable in <home>/.local/bin counts.
	if err := os.Chmod(filepath.Join(bin, "cswap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !cswapInstalled(nil, home) {
		t.Fatal("cswapInstalled missed <home>/.local/bin/cswap")
	}
	// An executable on the PATH counts too.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "cswap"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + other}
	if !cswapInstalled(env, t.TempDir()) {
		t.Fatal("cswapInstalled missed cswap on the PATH")
	}
}

func TestProcessEnv(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("processEnv is only available on macOS and Linux")
	}
	cmd := exec.Command("sleep", "5")
	cmd.Env = append(os.Environ(), "LANA_TEST_ENV=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	env := processEnv(cmd.Process.Pid)
	if got := envValue(env, "LANA_TEST_ENV"); got != "1" {
		t.Fatalf("processEnv did not report LANA_TEST_ENV=1 (got %q)", got)
	}
}
