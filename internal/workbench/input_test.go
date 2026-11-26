//go:build darwin || linux

package workbench

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// A session created with Input types the line into its shell, and the shell
// stays the pane's process when what the line ran ends.
func TestCreateInputTypedIntoShell(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	// The quotes keep the typed line's echo from matching the marker: only
	// the shell running it prints "back-at-prompt".
	s, err := c.Create(ctx, CreateOptions{
		Command: []string{"/bin/sh"},
		Dir:     t.TempDir(),
		Input:   `/bin/sh -c "exit 0"; echo back-at-""prompt`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	stream, err := c.Attach(ctx, s.ID, "view")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	readUntil(t, stream, "back-at-prompt", 5*time.Second)
	g, err := c.Get(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.State == "exited" {
		t.Fatalf("session exited after the typed program ended: %+v", g)
	}
}

// Input is never persisted: the record holds the shell, not the line.
func TestCreateInputNotPersisted(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh"}, Input: "true persist-marker-77"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	waitFor(t, "persisted record", func() bool {
		recs, err := readRecords(home)
		return err == nil && len(recs) == 1
	})
	data, err := os.ReadFile(sessionsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "persist-marker-77") {
		t.Fatalf("sessions.json holds the input: %s", data)
	}
}

// The core refuses an input with a line break or over the limit.
func TestCreateInputRejected(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	for _, in := range []string{"echo a\necho b", "echo a\r", strings.Repeat("a", MaxCreateInput+1)} {
		if _, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh"}, Input: in}); err == nil {
			t.Fatalf("Create with input %.20q succeeded", in)
		}
	}
	list, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("sessions = %+v, want none", list)
	}
}

// The core's hello advertises create_input.
func TestHelloAdvertisesCreateInput(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	h, err := c.Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.Features, FeatureCreateInput) {
		t.Fatalf("hello features = %v, want %q", h.Features, FeatureCreateInput)
	}
}

// fakeCoreClient is a Client on a pipe to a fake core that answers hello with
// features and records each create it gets.
func fakeCoreClient(t *testing.T, features []string) (*Client, func() []CreateOptions) {
	t.Helper()
	client, core := net.Pipe()
	c := &Client{conn: client, pending: map[int64]chan Response{}, closed: make(chan struct{})}
	go c.read()
	var mu sync.Mutex
	var created []CreateOptions
	go func() {
		r := bufio.NewReader(core)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			var req Request
			if json.Unmarshal(line, &req) != nil {
				return
			}
			var data any
			switch req.Op {
			case "hello":
				data = Hello{Version: ProtocolVersion, Features: features}
			case "create":
				mu.Lock()
				created = append(created, *req.Create)
				mu.Unlock()
				data = Session{ID: "s1"}
			}
			raw, _ := json.Marshal(data)
			b, _ := json.Marshal(Response{ID: req.ID, Data: raw})
			if _, err := core.Write(append(b, '\n')); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = c.Close(); _ = core.Close() })
	return c, func() []CreateOptions {
		mu.Lock()
		defer mu.Unlock()
		return append([]CreateOptions(nil), created...)
	}
}

// Against a core without create_input the input runs under sh -c and ends
// in the user's shell; against a new core it goes as input, no command.
func TestCreateInputFallbackForOldCore(t *testing.T) {
	ctx := context.Background()
	line := "env -u CLAUDE_CONFIG_DIR claude"

	old, oldCreated := fakeCoreClient(t, nil)
	if _, err := old.Create(ctx, CreateOptions{Dir: "/w", Input: line}); err != nil {
		t.Fatal(err)
	}
	got := oldCreated()
	if len(got) != 1 || got[0].Input != "" || got[0].Dir != "/w" {
		t.Fatalf("old core create = %+v", got)
	}
	want := []string{"/bin/sh", "-c", line + "\nexec \"${SHELL:-/bin/sh}\""}
	if !slices.Equal(got[0].Command, want) {
		t.Fatalf("old core command = %q, want %q", got[0].Command, want)
	}
	if !isLegacyInputCommand(got[0].Command) {
		t.Fatal("the fallback command is not recognized for restore")
	}

	cur, curCreated := fakeCoreClient(t, []string{FeatureCreateInput})
	if _, err := cur.Create(ctx, CreateOptions{Dir: "/w", Input: line}); err != nil {
		t.Fatal(err)
	}
	got = curCreated()
	if len(got) != 1 || got[0].Input != line || len(got[0].Command) != 0 {
		t.Fatalf("new core create = %+v", got)
	}
}

// The fallback really runs the line and then leaves a working shell.
func TestLegacyInputCommandEndsInShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	o := legacyInputOptions(CreateOptions{Input: `echo first-""ran`})
	s, err := c.Create(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	stream, err := c.Attach(ctx, s.ID, "control")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	readUntil(t, stream, "first-ran", 5*time.Second)
	if _, err := stream.Write([]byte("echo shell-\"\"alive\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, stream, "shell-alive", 5*time.Second)
}

// A record of the old-core fallback restores as the plain user shell, so the
// typed line is not run again.
func TestRestoreLegacyInputAsShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	home := shortHome(t)
	o := legacyInputOptions(CreateOptions{Input: "echo should-not-rerun"})
	rec := sessionRecord{ID: "legacy", Command: o.Command, Dir: t.TempDir(), Cols: 80, Rows: 24, Created: time.Now()}
	if err := writeRecords(home, []sessionRecord{rec}); err != nil {
		t.Fatal(err)
	}
	serveCore(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	var got Session
	waitFor(t, "restored session", func() bool {
		g, err := c.Get(ctx, "legacy")
		got = g
		return err == nil
	})
	defer func() { _ = c.Kill(ctx, "legacy") }()
	if !slices.Equal(got.Command, []string{"/bin/sh"}) || got.State == "exited" {
		t.Fatalf("restored = %+v, want a running /bin/sh", got)
	}
}
