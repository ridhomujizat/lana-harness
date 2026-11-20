package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// serveCore starts a core on home and returns a function that stops it and
// waits for Serve to return. The stop is idempotent and also runs at cleanup,
// so a test can stop one core before starting the next on the same home.
func serveCore(t *testing.T, home string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, home) }()
	waitFor(t, "core socket", func() bool {
		c, err := Dial(context.Background(), home)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Serve: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("Serve did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// catScript writes an executable shell script that runs cat, so a restored
// session echoes whatever is typed into it; it stands in for the login shell.
func catScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shell")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec cat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRecordsRoundTrip(t *testing.T) {
	home := shortHome(t)
	now := time.Now().UTC().Truncate(time.Second)
	in := []sessionRecord{
		{
			ID: "one", Title: "term", Command: []string{"/bin/sh"}, Dir: "/tmp/one",
			Env: []string{"A=1"}, Labels: map[string]string{"k": "v"}, Created: now,
			Cols: 100, Rows: 40, LastDir: "/tmp/one",
			Agent: "claude", AgentSession: "abc", AgentCwd: "/tmp/one", AgentSeen: now,
		},
		{
			ID: "two", Title: "run", Command: []string{"/bin/sh", "-c", "exit 3"}, Dir: "/tmp/two",
			Env: []string{"B=2"}, Labels: map[string]string{"z": "y"}, Created: now,
			Cols: 80, Rows: 24, LastDir: "/tmp/two", Exited: true, ExitCode: 3,
			Agent: "omp", AgentSession: "def", AgentCwd: "/tmp/two", AgentSeen: now,
		},
	}
	if err := writeRecords(home, in); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(sessionsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o, want 600", got)
	}
	data, err := os.ReadFile(sessionsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	out, err := readRecords(home)
	if err != nil {
		t.Fatal(err)
	}
	again, err := json.Marshal(sessionsFile{Version: sessionsVersion, Sessions: out})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, again) {
		t.Fatalf("round trip changed the file:\n got %s\nwant %s", again, data)
	}
	if len(out) != 2 || out[0].Agent != "claude" || out[1].AgentSession != "def" {
		t.Fatalf("round trip records = %+v", out)
	}
}

func TestAgentConfigDirRoundTrip(t *testing.T) {
	home := shortHome(t)
	dir := "/home/o/.claude-swap-backup/sessions/3-o_example.com"
	if err := writeRecords(home, []sessionRecord{
		{ID: "a", Agent: "claude", AgentSession: "s", AgentConfigDir: dir},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := readRecords(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].AgentConfigDir != dir {
		t.Fatalf("round trip agent_config_dir = %+v, want %q", out, dir)
	}

	// A file written before the field existed loads with "".
	raw := `{"version":1,"sessions":[{"id":"b","agent":"claude","agent_session":"s"}]}`
	if err := os.WriteFile(sessionsPath(home), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = readRecords(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].AgentConfigDir != "" || out[0].Agent != "claude" {
		t.Fatalf("old record without agent_config_dir = %+v, want ConfigDir \"\"", out)
	}
}

func TestReadRecordsQuarantine(t *testing.T) {
	home := shortHome(t)
	if err := os.MkdirAll(Dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionsPath(home), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	recs, err := readRecords(home)
	if err == nil {
		t.Fatal("readRecords accepted garbage")
	}
	if recs != nil {
		t.Fatalf("garbage records = %+v, want nil", recs)
	}
	if _, err := os.Stat(sessionsPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("garbage file not renamed: %v", err)
	}
	if matches, _ := filepath.Glob(sessionsPath(home) + ".bad-*"); len(matches) != 1 {
		t.Fatalf("quarantine files = %v, want 1", matches)
	}

	// A version the core does not know is quarantined the same way.
	home2 := shortHome(t)
	if err := os.MkdirAll(Dir(home2), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionsPath(home2), []byte(`{"version":99,"sessions":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecords(home2); err == nil {
		t.Fatal("readRecords accepted an unknown version")
	}
	if _, err := os.Stat(sessionsPath(home2)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown version not renamed: %v", err)
	}
}

func TestPendingRestore(t *testing.T) {
	home := shortHome(t)
	if PendingRestore(home) {
		t.Fatal("PendingRestore on a missing file = true")
	}
	if err := writeRecords(home, []sessionRecord{{ID: "a", Exited: true, ExitCode: 0}}); err != nil {
		t.Fatal(err)
	}
	if PendingRestore(home) {
		t.Fatal("PendingRestore with only exited sessions = true")
	}
	if err := writeRecords(home, []sessionRecord{{ID: "a", Exited: false}, {ID: "b", Exited: true}}); err != nil {
		t.Fatal(err)
	}
	if !PendingRestore(home) {
		t.Fatal("PendingRestore with a running session = false")
	}
}

func TestRestoreRunningAndExited(t *testing.T) {
	home := shortHome(t)
	work := t.TempDir()
	stopA := serveCore(t, home)
	c := mustClient(t, home)
	ctx := context.Background()

	run, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh"}, Dir: work})
	if err != nil {
		t.Fatal(err)
	}
	ex, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", "exit 3"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "exit", func() bool {
		g, err := c.Get(ctx, ex.ID)
		return err == nil && g.State == "exited" && g.ExitCode == 3
	})
	waitFor(t, "persisted records", func() bool {
		recs, err := readRecords(home)
		return err == nil && len(recs) == 2
	})
	stopA()

	recs, err := readRecords(home)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]sessionRecord{}
	for _, r := range recs {
		byID[r.ID] = r
	}
	r1, ok := byID[run.ID]
	if !ok {
		t.Fatalf("running session %s missing after the core stopped", run.ID)
	}
	if r1.Exited {
		t.Fatalf("running session was persisted exited: %+v", r1)
	}
	if r2, ok := byID[ex.ID]; !ok || !r2.Exited || r2.ExitCode != 3 {
		t.Fatalf("exited session record = %+v, ok = %v", r2, ok)
	}

	serveCore(t, home)
	c2 := mustClient(t, home)
	var list []Session
	waitFor(t, "restored sessions", func() bool {
		l, err := c2.List(ctx)
		if err != nil {
			return false
		}
		list = l
		return hasSession(l, run.ID) && hasSession(l, ex.ID)
	})
	var gotRun, gotEx Session
	for _, s := range list {
		switch s.ID {
		case run.ID:
			gotRun = s
		case ex.ID:
			gotEx = s
		}
	}
	if gotRun.State == "exited" {
		t.Fatalf("restored running session is exited: %+v", gotRun)
	}
	if gotRun.Restored == nil {
		t.Fatalf("restored running session has no restored time: %+v", gotRun)
	}
	if gotEx.State != "exited" || gotEx.ExitCode != 3 {
		t.Fatalf("restored exited session = %+v", gotEx)
	}

	// An exited restored session replays no output: the stream carries only the
	// replay framing and ends at once.
	stream, err := c2.Attach(ctx, ex.ID, "view")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(stream)
	_ = stream.Close()
	if err != nil {
		t.Fatalf("attach to an exited restored session: %v", err)
	}
	want := string(sizeMarker(80, 24)) + replayEndMarker
	if string(data) != want {
		t.Fatalf("exited attach replay = %q, want %q", data, want)
	}
}

func TestRestoreAgentResume(t *testing.T) {
	home := shortHome(t)
	dir := t.TempDir()
	t.Setenv("SHELL", catScript(t))
	const uuid = "0123abcd-0000-0000-0000-000000000000"
	const (
		idOne = "aaaaaaaaaaaa"
		idTwo = "bbbbbbbbbbbb"
	)
	now := time.Now()
	recs := []sessionRecord{
		{
			ID: idOne, Title: "one", Command: []string{"claude"}, Dir: dir,
			Created: now, Cols: 80, Rows: 24, LastDir: dir,
			Agent: "claude", AgentSession: uuid, AgentCwd: dir, AgentSeen: now,
		},
		{
			ID: idTwo, Title: "two", Command: []string{"claude"}, Dir: dir,
			Created: now, Cols: 80, Rows: 24, LastDir: dir,
			Agent: "claude", AgentSession: "bad;id", AgentCwd: dir, AgentSeen: now,
		},
	}
	if err := writeRecords(home, recs); err != nil {
		t.Fatal(err)
	}
	serveCore(t, home)
	c := mustClient(t, home)
	ctx := context.Background()

	for _, id := range []string{idOne, idTwo} {
		g, err := c.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if g.Restored == nil || g.ResumedAgent != "claude" {
			t.Fatalf("session %s restored = %+v", id, g)
		}
	}

	// A valid id resumes the conversation; a bad one falls back to --continue.
	check := func(id, want string) {
		t.Helper()
		stream, err := c.Attach(ctx, id, "view")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stream.Close() }()
		if got := readUntil(t, stream, want, 5*time.Second); !bytes.Contains([]byte(got), []byte(want)) {
			t.Fatalf("session %s replay = %q, want %q", id, got, want)
		}
	}
	check(idOne, "claude --resume "+uuid)
	check(idTwo, "claude --continue")
}

func TestRestoreNonShellBecomesExited(t *testing.T) {
	home := shortHome(t)
	rec := sessionRecord{
		ID: "cccccccccccc", Title: "dev", Command: []string{"/bin/sleep", "100"},
		Dir: t.TempDir(), Cols: 80, Rows: 24, Exited: false,
	}
	if err := writeRecords(home, []sessionRecord{rec}); err != nil {
		t.Fatal(err)
	}
	serveCore(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	g, err := c.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.State != "exited" || g.ExitCode != -1 {
		t.Fatalf("non-shell restore = %+v, want exited -1", g)
	}
	if g.PID != 0 {
		t.Fatalf("non-shell restore started a process: pid = %d", g.PID)
	}

	// The owner can restart it, which starts the command fresh.
	rs, err := c.Restart(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, rec.ID) }()
	if rs.PID <= 0 || rs.State == "exited" {
		t.Fatalf("restart of a non-shell session = %+v", rs)
	}
}

func TestKillDropsRecord(t *testing.T) {
	home := shortHome(t)
	serveCore(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", "sleep 30"}, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "record written", func() bool {
		recs, err := readRecords(home)
		return err == nil && len(recs) == 1
	})
	if err := c.Kill(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "record dropped", func() bool {
		recs, err := readRecords(home)
		return err == nil && len(recs) == 0
	})
}

func TestAgentExpiry(t *testing.T) {
	stale := &session{
		rAgent: "claude", agentSession: "id", agentCwd: "/tmp",
		agentSeen: time.Now().Add(-11 * time.Minute),
		agentPID:  42, agentStarted: time.Now(),
	}
	stale.mu.Lock()
	stale.expireAgentLocked(time.Now())
	stale.mu.Unlock()
	if stale.rAgent != "" || stale.agentSession != "" || stale.agentCwd != "" ||
		!stale.agentSeen.IsZero() || stale.agentPID != 0 {
		t.Fatalf("stale session kept agent fields: %+v", stale)
	}

	fresh := &session{rAgent: "claude", agentSession: "id", agentSeen: time.Now()}
	fresh.mu.Lock()
	fresh.expireAgentLocked(time.Now())
	fresh.mu.Unlock()
	if fresh.rAgent != "claude" || fresh.agentSession != "id" {
		t.Fatalf("fresh session lost its agent: %+v", fresh)
	}
}
