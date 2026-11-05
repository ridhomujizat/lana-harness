package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// TestWithoutTurnSkipsLiveTurn covers the guard of the orphaned-turn repair:
// fn runs only when no turn for the session runs in this process, and a turn
// cannot start while fn runs.
func TestWithoutTurnSkipsLiveTurn(t *testing.T) {
	reg := NewRegistry(nil, nil, nil, nil, core.Config{}, nil, "", "")

	called := false
	ran, err := reg.WithoutTurn("s1", func() error { called = true; return nil })
	if err != nil || !ran || !called {
		t.Fatalf("idle session: ran=%v called=%v err=%v", ran, called, err)
	}

	reg.mu.Lock()
	runner := reg.runnerLocked("s1")
	reg.mu.Unlock()
	runner.mu.Lock()
	runner.running = true
	runner.mu.Unlock()
	called = false
	ran, err = reg.WithoutTurn("s1", func() error { called = true; return nil })
	if err != nil || ran || called {
		t.Fatalf("live turn: ran=%v called=%v err=%v", ran, called, err)
	}
	runner.mu.Lock()
	runner.running = false
	runner.mu.Unlock()

	// While fn runs, the runner lock is held, so a turn start waits.
	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_, _ = reg.WithoutTurn("s1", func() error { close(started); <-release; return nil })
	}()
	<-started
	locked := make(chan struct{})
	go func() {
		runner.mu.Lock()
		runner.mu.Unlock() //nolint:staticcheck // only waits for the lock
		close(locked)
	}()
	select {
	case <-locked:
		t.Fatal("runner lock was free while fn ran")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-locked

	sentinel := errors.New("boom")
	if _, err := reg.WithoutTurn("s1", func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("fn error not returned: %v", err)
	}
}
