package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

func earlyClass(t *testing.T, err error) string {
	t.Helper()
	var early *EarlyError
	if !errors.As(err, &early) {
		t.Fatalf("err = %T %v, want an EarlyError", err, err)
	}
	return early.Class
}

// REQ-CCT-004: a failure before the first content event is an EarlyError with
// its class; a failure after output is not.
func TestEarlyErrors(t *testing.T) {
	missing := newHarness(t, "hi.jsonl", Options{LookPath: func(string) (string, error) { return "", exec.ErrNotFound }})
	_, err := missing.run(context.Background(), missing.turn("s1"))
	if c := earlyClass(t, err); c != core.ErrorClassNotFound || err.Error() != errNotInstalled {
		t.Fatalf("not installed: class %q err %v", c, err)
	}

	old := newHarness(t, "hi.jsonl", Options{}, "FAKECLAUDE_VERSION=2.0.9 (Claude Code)")
	if _, err := old.run(context.Background(), old.turn("s1")); earlyClass(t, err) != "client" {
		t.Fatalf("old version: %v", err)
	}

	login := newHarness(t, "notloggedin.jsonl", Options{})
	_, err = login.run(context.Background(), login.turn("s1"))
	if c := earlyClass(t, err); c != "auth" || err.Error() != notLoggedInMessage {
		t.Fatalf("not logged in: class %q err %v", c, err)
	}

	crash := newHarness(t, "crash_early.jsonl", Options{})
	_, err = crash.run(context.Background(), crash.turn("s1"))
	if c := earlyClass(t, err); c != "runtime" || !strings.Contains(err.Error(), "upstream connection reset") {
		t.Fatalf("crash before output: class %q err %v", c, err)
	}

	late := newHarness(t, "crash.jsonl", Options{})
	_, err = late.run(context.Background(), late.turn("s1"))
	var early *EarlyError
	if err == nil || errors.As(err, &early) {
		t.Fatalf("crash after output: err = %T %v", err, err)
	}

	key := newHarness(t, "hi.jsonl", Options{})
	turn := key.turn("s1")
	turn.Config.Auth, turn.Config.APIKeyEnv = core.RuntimeAuthAPIKey, "NOT_SET"
	if _, err := key.run(context.Background(), turn); earlyClass(t, err) != "missing_secret" {
		t.Fatalf("missing key: %v", err)
	}
}

// REQ-CCT-004: a rate_limit_event with status rejected before output ends
// the turn at once as an early rate_limit failure, and the next turn starts a
// new process that resumes the Claude session.
func TestRejectedPlanWindowIsEarly(t *testing.T) {
	h := newHarness(t, "rejected.jsonl", Options{})
	start := time.Now()
	events, err := h.run(context.Background(), h.turn("s1"))
	if c := earlyClass(t, err); c != "rate_limit" || !strings.Contains(err.Error(), "five_hour") {
		t.Fatalf("class %q err %v", c, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("rejected turn took %v", time.Since(start))
	}
	limits := false
	for _, ev := range events {
		limits = limits || ev.Kind == core.RuntimeLimits
	}
	if !limits {
		t.Fatalf("no limits event: %v", kinds(events))
	}
	first := argValue(h.starts()[0].Argv, "--session-id")
	h.setFixture("hi.jsonl")
	if _, err := h.run(context.Background(), h.turn("s1")); err != nil {
		t.Fatal(err)
	}
	if starts := h.starts(); len(starts) != 2 || argValue(starts[1].Argv, "--resume") != first {
		t.Fatalf("starts = %+v", starts)
	}
}

// REQ-CCT-005: the prompt head goes to --append-system-prompt; a change of it
// (or of the lana tool set) starts a new process that resumes the session.
func TestPromptHeadRestartsWithResume(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{})
	turn := h.turn("s1")
	turn.Config.AppendSystemPrompt = "You are Bima"
	if _, err := h.run(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if _, err := h.run(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	starts := h.starts()
	if len(starts) != 1 || argValue(starts[0].Argv, "--append-system-prompt") != "You are Bima" {
		t.Fatalf("same head: starts = %+v", starts)
	}
	id := argValue(starts[0].Argv, "--session-id")
	turn.Config.AppendSystemPrompt = "You are Bima.\nThe owner likes tea."
	if _, err := h.run(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	turn.Config.MCPKey = "memory_write"
	if _, err := h.run(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	starts = h.starts()
	if len(starts) != 3 || argValue(starts[1].Argv, "--resume") != id || !strings.Contains(argValue(starts[1].Argv, "--append-system-prompt"), "tea") ||
		argValue(starts[2].Argv, "--resume") != id {
		t.Fatalf("changed head: starts = %+v", starts)
	}
}

// REQ-CCT-006, API-CCT-014: OpenMCP is called once per process; its config
// goes to a 0600 file whose path is --mcp-config, and release runs and the
// file is removed when the process exits.
func TestOpenMCPPerProcess(t *testing.T) {
	tmp := t.TempDir()
	h := newHarness(t, "hi.jsonl", Options{TempDir: tmp})
	var mu sync.Mutex
	opened, released := 0, 0
	turn := h.turn("s1")
	turn.OpenMCP = func() (string, func(), error) {
		mu.Lock()
		defer mu.Unlock()
		opened++
		return `{"mcpServers":{"lana":{"type":"http","url":"http://127.0.0.1:1/mcp"}}}`, func() {
			mu.Lock()
			released++
			mu.Unlock()
		}, nil
	}
	for range 2 {
		if _, err := h.run(context.Background(), turn); err != nil {
			t.Fatal(err)
		}
	}
	start := h.starts()[0]
	path := argValue(start.Argv, "--mcp-config")
	if filepath.Dir(path) != tmp || !strings.Contains(start.MCPConfig, `"lana"`) || start.MCPConfigMode != "0600" {
		t.Fatalf("--mcp-config = %q, file = %q mode %q", path, start.MCPConfig, start.MCPConfigMode)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file of a live process: %v", err)
	}
	h.rt.Close("s1")
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		o, r := opened, released
		mu.Unlock()
		_, statErr := os.Stat(path)
		if o == 1 && r == 1 && errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("opened %d released %d", o, r)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A failed OpenMCP starts the process without --mcp-config.
	turn.OpenMCP = func() (string, func(), error) { return "", nil, errors.New("no host") }
	if _, err := h.run(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	starts := h.starts()
	if argValue(starts[len(starts)-1].Argv, "--mcp-config") != "" {
		t.Fatalf("argv = %v", starts[len(starts)-1].Argv)
	}
}

// Scope addition: the initialize response's models list (never its account
// part) is kept and reported through RuntimeDeps.OnModels.
func TestInitializeModels(t *testing.T) {
	h := newHarness(t, "hi.jsonl", Options{})
	var mu sync.Mutex
	var reported [][]core.RuntimeModel
	h.rt.deps.OnModels = func(list []core.RuntimeModel) {
		mu.Lock()
		reported = append(reported, list)
		mu.Unlock()
	}
	if _, ok := h.rt.Models(); ok {
		t.Fatal("models before any process")
	}
	for range 2 {
		h.rt.Close("s1")
		if _, err := h.run(context.Background(), h.turn("s1")); err != nil {
			t.Fatal(err)
		}
	}
	models, ok := h.rt.Models()
	if !ok || len(models) != 5 || models[2] != (core.RuntimeModel{Value: "claude-opus-5-5", DisplayName: "Opus 5.5", Description: "Claude Opus 5.5"}) {
		t.Fatalf("models = %+v", models)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 1 {
		t.Fatalf("OnModels calls = %d (an unchanged list is not reported again)", len(reported))
	}
}

func TestParseInitModels(t *testing.T) {
	raw := json.RawMessage(`{"commands":[],"account":{"email":"user@example.com"},"models":[` +
		`{"value":"opus","displayName":"Opus","description":"x"},{"id":"claude-x","name":"X"},{"value":"-bad"},{"value":""}]}`)
	got := parseInitModels(raw)
	if len(got) != 2 || got[0].Value != "opus" || got[0].DisplayName != "Opus" || got[1] != (core.RuntimeModel{Value: "claude-x", DisplayName: "X"}) {
		t.Fatalf("models = %+v", got)
	}
	if parseInitModels(json.RawMessage(`{"commands":[]}`)) != nil || parseInitModels(nil) != nil {
		t.Fatal("a response without models gave a list")
	}
}
