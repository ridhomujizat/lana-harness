package workbench

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// nextEvent returns the next event, failing the test on a timeout.
func nextEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("event stream closed")
		}
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for an event")
		return Event{}
	}
}

// waitEvent skips events until one matches want, failing the test on a
// timeout.
func waitEvent(t *testing.T, events <-chan Event, want func(Event) bool) Event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed")
			}
			if want(ev) {
				return ev
			}
		case <-deadline:
			t.Fatal("timed out waiting for an event")
			return Event{}
		}
	}
}

func TestSubscribeEvents(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", "printf hi; sleep 3"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()

	ev := nextEvent(t, events)
	if ev.Type != evCreated || ev.Session == nil || ev.Session.ID != s.ID {
		t.Fatalf("first event = %+v, want session.created for %s", ev, s.ID)
	}

	ev = waitEvent(t, events, func(ev Event) bool {
		return ev.Type == evUpdated && ev.Session != nil && ev.Session.ID == s.ID
	})
	if ev.Session.State != "working" {
		t.Fatalf("first update state = %q, want working", ev.Session.State)
	}

	ev = waitEvent(t, events, func(ev Event) bool {
		return ev.Type == evUpdated && ev.Session != nil && ev.Session.ID == s.ID && ev.Session.State == "idle"
	})

	doc := json.RawMessage(`{"spaces":[{"id":"a"}]}`)
	if err := c.SetLayout(ctx, doc); err != nil {
		t.Fatal(err)
	}
	ev = waitEvent(t, events, func(ev Event) bool { return ev.Type == evLayout })
	if string(ev.Layout) != string(doc) {
		t.Fatalf("layout.changed = %s, want %s", ev.Layout, doc)
	}

	if err := c.Kill(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	ev = waitEvent(t, events, func(ev Event) bool {
		return ev.Type == evRemoved && ev.Session != nil && ev.Session.ID == s.ID
	})
}

func TestBellNeedsYou(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", `printf "\a"; sleep 3`}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	waitFor(t, "bell", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && g.NeedsYou
	})
}

func TestOSCTitleIsNotABell(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", `printf "\033]0;title\a"; sleep 3`}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		g, err := c.Get(ctx, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if g.NeedsYou {
			t.Fatal("an OSC terminator was treated as a bell")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestForegroundProgram checks that a session reports the pty's foreground
// process. macOS names a process after its resolved binary, so a symlinked
// sleep (as in a nix shell) reports its target, and the expected name is read
// the same way.
func TestForegroundProgram(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("foreground program is read on macOS and Linux only")
	}
	want := "sleep"
	if path, err := exec.LookPath("sleep"); err == nil {
		if runtime.GOOS == "darwin" {
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				path = resolved
			}
		}
		want = filepath.Base(path)
	}
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx := context.Background()
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", "exec sleep 5"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Kill(ctx, s.ID) }()
	waitFor(t, "foreground program", func() bool {
		g, err := c.Get(ctx, s.ID)
		return err == nil && g.Program == want
	})
}

func TestUpdatesEndAtExit(t *testing.T) {
	home := shortHome(t)
	serve(t, home)
	c := mustClient(t, home)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Create(ctx, CreateOptions{Command: []string{"/bin/sh", "-c", "printf hi; exit 2"}})
	if err != nil {
		t.Fatal(err)
	}
	var last Session
	seen := false
	deadline := time.After(3 * time.Second)
loop:
	for {
		select {
		case ev := <-events:
			if ev.Type == evUpdated && ev.Session != nil && ev.Session.ID == s.ID {
				last, seen = *ev.Session, true
			}
		case <-deadline:
			break loop
		}
	}
	if !seen {
		t.Fatal("no session.updated event")
	}
	if last.State != "exited" || last.ExitCode != 2 {
		t.Fatalf("last update = state %q, code %d; want exited, 2", last.State, last.ExitCode)
	}
}

func TestShouldSend(t *testing.T) {
	now := time.Now()
	if !shouldSend(true, now.Add(-updateInterval), now) {
		t.Fatal("a dirty session past the interval was not sent")
	}
	if shouldSend(true, now.Add(-updateInterval/2), now) {
		t.Fatal("a dirty session inside the interval was sent")
	}
	if shouldSend(false, now.Add(-2*updateInterval), now) {
		t.Fatal("a clean session was sent")
	}
}
