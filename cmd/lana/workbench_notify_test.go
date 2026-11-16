package main

import (
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/notify"
	"github.com/ridhomujizat/lana-harness/internal/workbench"
)

func TestWorkbenchTransitionFinished(t *testing.T) {
	prev := workbench.Session{ID: "s1", Agent: "claude", State: "working", Title: "✳ Fix the login bug"}
	next := prev
	next.State = "idle"

	e, ok := workbenchTransition(prev, next, time.Unix(0, 42))
	if !ok || e.Kind != notify.KindAgentFinished {
		t.Fatalf("transition = %+v, %v; want agent_finished", e, ok)
	}
	if e.Title != "Claude Code finished" || e.Agent != "Claude Code" || e.Body != "Fix the login bug" {
		t.Fatalf("event = %+v", e)
	}
	if e.SessionID != "s1" || e.Thread != "s1" || e.URL != "#/workbench" {
		t.Fatalf("event targets = %+v", e)
	}
	if e.Key != "agent_finished:s1:42" {
		t.Fatalf("key = %q", e.Key)
	}
}

func TestWorkbenchTransitionExitedIsNotFinished(t *testing.T) {
	prev := workbench.Session{ID: "s1", Agent: "claude", State: "working", Title: "Fix"}
	next := prev
	next.State = "exited"
	if _, ok := workbenchTransition(prev, next, time.Now()); ok {
		t.Fatal("an exited session notified as finished")
	}
}

func TestWorkbenchTransitionNeedsYou(t *testing.T) {
	prev := workbench.Session{ID: "s1", Agent: "codex", State: "idle", NeedsYou: false, Title: "⠂ Writing tests"}
	next := prev
	next.NeedsYou = true
	e, ok := workbenchTransition(prev, next, time.Now())
	if !ok || e.Kind != notify.KindAgentNeedsYou {
		t.Fatalf("transition = %+v, %v; want agent_needs_you", e, ok)
	}
	if e.Title != "Codex needs you" || e.Agent != "Codex" || e.Body != "Writing tests" {
		t.Fatalf("event = %+v", e)
	}
	if _, ok := workbenchTransition(next, next, time.Now()); ok {
		t.Fatal("a lasting needs-you notified again")
	}
}

func TestWorkbenchTransitionRequiresAgent(t *testing.T) {
	prev := workbench.Session{ID: "s1", State: "working", Title: "Fix"}
	next := prev
	next.State = "idle"
	if _, ok := workbenchTransition(prev, next, time.Now()); ok {
		t.Fatal("a session without an agent notified")
	}
	next = prev
	next.NeedsYou = true
	if _, ok := workbenchTransition(prev, next, time.Now()); ok {
		t.Fatal("a needs-you without an agent notified")
	}
}

func TestWorkbenchAgentName(t *testing.T) {
	for agent, want := range map[string]string{
		"claude":   "Claude Code",
		" codex ":  "Codex",
		"jcode":    "jcode",
		"opencode": "opencode",
	} {
		if got := workbenchAgentName(agent); got != want {
			t.Errorf("workbenchAgentName(%q) = %q, want %q", agent, got, want)
		}
	}
}

func TestWorkbenchAgentBody(t *testing.T) {
	folder := func(title, cwd, dir string) string {
		return workbenchAgentBody(workbench.Session{Title: title, Cwd: cwd, Dir: dir})
	}
	if got := folder("✳ Fix the login bug", "", ""); got != "Fix the login bug" {
		t.Errorf("glyph strip = %q", got)
	}
	if got := folder("⠂ Writing tests", "", ""); got != "Writing tests" {
		t.Errorf("braille strip = %q", got)
	}
	if got := folder("✳", "/Users/x/myproj", "/tmp"); got != "myproj" {
		t.Errorf("cwd fallback = %q, want myproj", got)
	}
	if got := folder("", "", "/tmp/plain dir"); got != "plain dir" {
		t.Errorf("dir fallback = %q, want plain dir", got)
	}
	if got := folder("", "", ""); got != "" {
		t.Errorf("empty body = %q, want empty", got)
	}
	if got := folder("Summary: "+strings.Repeat("word ", 60), "", ""); len([]rune(got)) > notify.AgentMax {
		t.Errorf("body is %d runes, want at most %d", len([]rune(got)), notify.AgentMax)
	}
}

// drainEvents returns the events already published to a buffered subscriber.
// A publish into a subscriber that is not full is synchronous, so this does not
// race a producer.
func drainEvents(ch <-chan notify.Event) []notify.Event {
	var out []notify.Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func TestWorkbenchWatcherDebounce(t *testing.T) {
	bus := notify.NewBus()
	w := newWorkbenchWatcher(bus)
	now := time.UnixMilli(1_700_000_000_000)
	w.now = func() time.Time { return now }

	_, events, cancel := bus.Subscribe("")
	defer cancel()

	agent := func(id, state string) workbench.Session {
		return workbench.Session{ID: id, Agent: "claude", State: state, Title: "✳ Fix"}
	}

	w.note(agent("s1", "idle"))    // baseline
	w.note(agent("s1", "working")) // no edge
	w.note(agent("s1", "idle"))    // agent_finished, published

	w.note(agent("s1", "working"))
	w.note(agent("s1", "idle")) // within the debounce, dropped

	now = now.Add(workbenchNotifyDebounce)
	w.note(agent("s1", "working"))
	w.note(agent("s1", "idle")) // past the debounce, published

	w.note(agent("s2", "idle"))
	w.note(agent("s2", "working"))
	w.note(agent("s2", "idle")) // another session, not debounced

	got := drainEvents(events)
	if len(got) != 3 {
		t.Fatalf("published %d events, want 3: %+v", len(got), got)
	}
	for i, want := range []string{"s1", "s1", "s2"} {
		if got[i].Kind != notify.KindAgentFinished || got[i].SessionID != want {
			t.Fatalf("event %d = %+v, want agent_finished for %s", i, got[i], want)
		}
	}
	if got[0].Key == got[1].Key {
		t.Fatalf("two real repeats share the dedupe key %q", got[0].Key)
	}
}

func TestWorkbenchWatcherFirstSnapshotIsBaseline(t *testing.T) {
	bus := notify.NewBus()
	w := newWorkbenchWatcher(bus)
	_, events, cancel := bus.Subscribe("")
	defer cancel()
	// A session first seen already waiting for the owner is a baseline: without
	// that, the false-to-true edge against an empty snapshot would notify.
	waiting := workbench.Session{ID: "s1", Agent: "claude", State: "idle", NeedsYou: true, Title: "Fix"}
	w.note(waiting)
	w.note(waiting)
	if got := drainEvents(events); len(got) != 0 {
		t.Fatalf("baseline published %+v", got)
	}
}
