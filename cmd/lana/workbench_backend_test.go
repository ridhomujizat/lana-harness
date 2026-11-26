package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
	"github.com/ridhomujizat/lana-harness/web"
)

// coreHome returns a temp Lana home short enough for the core's Unix socket.
func coreHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wbc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// serveTestCore runs a core in this process on home and returns its stop.
func serveTestCore(t *testing.T, home string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- workbench.Serve(ctx, home) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := workbench.Dial(context.Background(), home)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("core did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("core did not stop")
		}
	}
	t.Cleanup(stop)
	return stop
}

// Spec 84 REQ-WBR-002: the core's build against the daemon's decides
// outdated; a core that sends no version is outdated; no core is not running
// and not outdated.
func TestWorkbenchBackendCoreInfo(t *testing.T) {
	t.Cleanup(func() { workbench.SetBuild("", "") })
	home := coreHome(t)
	b := newWorkbenchBackend(home)
	b.daemonVersion, b.daemonCommit = "0.6.0-beta.14", "abc1234"
	ctx := context.Background()

	info, err := b.CoreInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Running || info.Outdated || info.DaemonVersion != "0.6.0-beta.14" {
		t.Fatalf("no core: %+v, want not running and not outdated", info)
	}
	if _, err := os.Stat(workbench.SocketPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("CoreInfo started a core: %v", err)
	}

	serveTestCore(t, home)
	for _, tc := range []struct {
		name            string
		version, commit string
		outdated        bool
	}{
		{"older core without a version", "", "", true},
		{"older build", "0.6.0-beta.13", "def5678", true},
		{"same version, other commit", "0.6.0-beta.14", "def5678", true},
		{"same build", "0.6.0-beta.14", "abc1234", false},
	} {
		workbench.SetBuild(tc.version, tc.commit)
		info, err := b.CoreInfo(ctx)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !info.Running || info.Outdated != tc.outdated || info.Version != tc.version || info.Commit != tc.commit {
			t.Fatalf("%s: %+v, want running, outdated %v", tc.name, info, tc.outdated)
		}
	}
}

// Spec 84 REQ-WBR-003: RestartCore installs the new core's client, tells the
// notification watcher, and reports the new core; a second restart while one
// runs is refused.
func TestWorkbenchBackendRestartCore(t *testing.T) {
	t.Cleanup(func() { workbench.SetBuild("", "") })
	workbench.SetBuild("1.0.0", "c0ffee")
	home := coreHome(t)
	serveTestCore(t, home)
	b := newWorkbenchBackend(home)
	b.daemonVersion, b.daemonCommit = "1.0.0", "c0ffee"
	ctx := context.Background()

	release := make(chan struct{})
	entered := make(chan struct{})
	b.restartCore = func(ctx context.Context, home string) (*workbench.Client, error) {
		close(entered)
		<-release
		return workbench.Dial(ctx, home)
	}
	var live *workbench.Client
	b.setOnLive(func(c *workbench.Client) { live = c })

	type result struct {
		info web.WorkbenchCore
		err  error
	}
	first := make(chan result, 1)
	go func() {
		info, err := b.RestartCore(ctx)
		first <- result{info, err}
	}()
	<-entered
	if _, err := b.RestartCore(ctx); !errors.Is(err, web.ErrWorkbenchRestartRunning) {
		t.Fatalf("second restart = %v, want ErrWorkbenchRestartRunning", err)
	}
	close(release)
	got := <-first
	if got.err != nil {
		t.Fatal(got.err)
	}
	if !got.info.Running || got.info.Outdated || got.info.Version != "1.0.0" {
		t.Fatalf("restart answer = %+v", got.info)
	}
	if live == nil || b.client != live {
		t.Fatal("the new client was not installed and handed to the watcher")
	}

	// A failed restart answers its error and leaves no client behind.
	b.restartCore = func(context.Context, string) (*workbench.Client, error) {
		return nil, errors.New("the new Workbench core did not start")
	}
	if _, err := b.RestartCore(ctx); err == nil {
		t.Fatal("failed restart returned no error")
	}
	if b.client != nil {
		t.Fatal("failed restart left a client")
	}
}
