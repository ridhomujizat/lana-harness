package workbench

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Spec 84 REQ-WBR-001: the Hello carries the core's Lana build.
func TestHelloReportsBuild(t *testing.T) {
	SetBuild("1.2.3", "abc123")
	t.Cleanup(func() { SetBuild("", "") })
	home := shortHome(t)
	serve(t, home)
	h, err := mustClient(t, home).Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.AppVersion != "1.2.3" || h.Commit != "abc123" || h.Version != ProtocolVersion {
		t.Fatalf("Hello = %+v, want app_version 1.2.3 commit abc123 version %d", h, ProtocolVersion)
	}
}

// inProcessStart makes Ensure start the core as a goroutine of the test, and
// stops every core it started at cleanup.
func inProcessStart(t *testing.T) *int {
	t.Helper()
	var mu sync.Mutex
	starts := 0
	var stops []func()
	old := startCore
	startCore = func(home string) error {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- Serve(ctx, home) }()
		mu.Lock()
		starts++
		stops = append(stops, func() {
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("Serve: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("Serve did not stop")
			}
		})
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() {
		startCore = old
		mu.Lock()
		defer mu.Unlock()
		for _, stop := range stops {
			stop()
		}
	})
	return &starts
}

// Spec 84 REQ-WBR-003: RestartCore stops the core with "shutdown", starts a
// new one, and the new one restores the panes (spec 60).
func TestRestartCoreRestoresSessions(t *testing.T) {
	home := shortHome(t)
	work := t.TempDir()
	starts := inProcessStart(t)
	ctx := context.Background()

	first, err := Ensure(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	h1, err := first.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := first.Create(ctx, CreateOptions{Command: []string{"/bin/sh"}, Dir: work})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "persisted record", func() bool {
		recs, err := readRecords(home)
		return err == nil && len(recs) == 1
	})

	second, err := RestartCore(ctx, home)
	if err != nil {
		t.Fatalf("RestartCore: %v", err)
	}
	defer func() { _ = second.Close() }()
	if *starts != 2 {
		t.Fatalf("cores started = %d, want 2", *starts)
	}
	h2, err := second.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !h2.Started.After(h1.Started) {
		t.Fatalf("new core started %v, old %v: not a new core", h2.Started, h1.Started)
	}
	list, err := second.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSession(list, run.ID) {
		t.Fatalf("session %s not restored: %+v", run.ID, list)
	}
	for _, s := range list {
		if s.ID == run.ID && (s.State == "exited" || s.Restored == nil) {
			t.Fatalf("restored session = %+v, want running and restored", s)
		}
	}
	// The old core's socket is gone: a fresh dial reaches the new core.
	c, err := Dial(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if h, err := c.Hello(ctx); err != nil || !h.Started.Equal(h2.Started) {
		t.Fatalf("dial after restart = %+v, %v; want the new core", h, err)
	}
}

// With no core running, RestartCore only starts one.
func TestRestartCoreStartsWhenNoneRuns(t *testing.T) {
	home := shortHome(t)
	starts := inProcessStart(t)
	c, err := RestartCore(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if *starts != 1 {
		t.Fatalf("cores started = %d, want 1", *starts)
	}
}

// A core that does not go away fails the restart with ErrCoreDidNotStop.
func TestWaitCoreStoppedTimesOut(t *testing.T) {
	home := shortHome(t)
	if err := os.MkdirAll(Dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	// core.pid still names the core: it has not finished killing its panes.
	pid := os.Getpid() + 100000
	if err := os.WriteFile(filepath.Join(Dir(home), "core.pid"), []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := waitCoreStopped(context.Background(), home, pid, 100*time.Millisecond)
	if !errors.Is(err, ErrCoreDidNotStop) {
		t.Fatalf("waitCoreStopped = %v, want ErrCoreDidNotStop", err)
	}
	// Once core.pid is gone and the process does not exist, it has stopped.
	if err := os.Remove(filepath.Join(Dir(home), "core.pid")); err != nil {
		t.Fatal(err)
	}
	if processAlive(pid) {
		t.Skipf("pid %d happens to exist", pid)
	}
	if err := waitCoreStopped(context.Background(), home, pid, 100*time.Millisecond); err != nil {
		t.Fatalf("waitCoreStopped after the pid file went = %v", err)
	}
}
