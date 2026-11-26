package workbench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// coreStopTimeout bounds how long RestartCore waits for the old core to stop
// (spec 84 REQ-WBR-003).
const coreStopTimeout = 10 * time.Second

// ErrCoreDidNotStop is RestartCore's answer when the old core is still there
// after coreStopTimeout.
var ErrCoreDidNotStop = errors.New("the Workbench core did not stop")

// stopWait is how long RestartCore waits for the old core; tests shorten it.
var stopWait = coreStopTimeout

// RestartCore replaces the running core with a new one and returns a client to
// the new core (spec 84 REQ-WBR-003). The old core gets the "shutdown" op, which
// freezes its persister before it kills its panes, so the new core restores them
// (spec 60). RestartCore waits for the old core to be gone: its socket no longer
// answers, core.pid no longer names it, and its process has exited. It then
// starts a core the way Ensure does and waits for its Hello. With no core
// running it only starts one.
func RestartCore(ctx context.Context, home string) (*Client, error) {
	if old, err := Dial(ctx, home); err == nil {
		pid := 0
		if h, err := old.Hello(ctx); err == nil {
			pid = h.PID
		}
		err := old.Shutdown(ctx)
		_ = old.Close()
		if err != nil {
			return nil, fmt.Errorf("workbench: stopping the core: %w", err)
		}
		if err := waitCoreStopped(ctx, home, pid, stopWait); err != nil {
			return nil, err
		}
	}
	client, err := Ensure(ctx, home)
	if err != nil {
		return nil, fmt.Errorf("the new Workbench core did not start: %w", err)
	}
	return client, nil
}

// waitCoreStopped polls until the core of pid is gone, or fails with
// ErrCoreDidNotStop after timeout. pid 0 (unknown) skips the pid checks.
func waitCoreStopped(ctx context.Context, home string, pid int, timeout time.Duration) error {
	watchExit(pid)
	deadline := time.Now().Add(timeout)
	for {
		if coreStopped(ctx, home, pid) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w within %s", ErrCoreDidNotStop, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Millisecond):
		}
	}
}

// coreStopped reports whether the core of pid is gone. The socket closes
// first; core.pid is removed only after the panes were killed, so a new core
// never restores from a snapshot the old one is still writing.
func coreStopped(ctx context.Context, home string, pid int) bool {
	if c, err := Dial(ctx, home); err == nil {
		_ = c.Close()
		return false
	}
	if pid <= 0 {
		return true
	}
	if b, err := os.ReadFile(filepath.Join(Dir(home), "core.pid")); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n == pid {
			return false
		}
	}
	// A core served inside this process (the tests) has no process of its own.
	if pid == os.Getpid() {
		return true
	}
	return !processAlive(pid)
}

// watchExit reaps pid in the background when it is a child of this process (a
// core the daemon spawned), so its exit is not hidden by a zombie. For any
// other process Wait fails at once and processAlive decides.
func watchExit(pid int) {
	if pid <= 0 || pid == os.Getpid() {
		return
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	go func() { _, _ = p.Wait() }()
}
