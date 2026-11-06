package mcp

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// TestPutNewServerConnectsOnEnsure covers the add half of hot apply: Put stores
// a lazily stopped definition and the next Ensure connects it.
func TestPutNewServerConnectsOnEnsure(t *testing.T) {
	server := newInMemoryServer()
	manager, _, connects := newTestManager(t, server, nil)

	manager.Put("added", core.MCPConfig{Command: "fake"})
	status := manager.Status([]string{"added"})[0]
	if status.State != stateStopped || status.Tools != 0 {
		t.Fatalf("status after Put=%+v, want stopped with no tools", status)
	}

	names, unavailable := manager.Ensure(context.Background(), []string{"added"})
	if len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}
	if len(names) != 4 {
		t.Fatalf("tools=%v, want 4", names)
	}
	if got := connects.Load(); got != 1 {
		t.Fatalf("connects=%d want 1", got)
	}
}

// TestPutStopsRunningServer covers the replace half: Put closes the old session
// and resets its tools before the next Ensure starts the new definition.
func TestPutStopsRunningServer(t *testing.T) {
	server := newInMemoryServer()
	manager, registry, connects := newTestManager(t, server, map[string]core.MCPConfig{"test": {}})

	if _, unavailable := manager.Ensure(context.Background(), []string{"test"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}

	manager.Put("test", core.MCPConfig{Command: "replacement"})
	status := manager.Status([]string{"test"})[0]
	if status.State != stateStopped || status.Tools != 0 {
		t.Fatalf("status after Put=%+v, want stopped with no tools", status)
	}
	out := callTool(t, registry, "mcp__test__echo", `{"text":"old"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "not running") {
		t.Fatalf("old tool call=%+v, want not running", out)
	}

	if _, unavailable := manager.Ensure(context.Background(), []string{"test"}); len(unavailable) != 0 {
		t.Fatalf("unavailable after Put=%v", unavailable)
	}
	if got := connects.Load(); got != 2 {
		t.Fatalf("connects=%d want 2", got)
	}
	if status := manager.Status([]string{"test"})[0]; status.State != stateRunning || status.Tools != 4 {
		t.Fatalf("status after re-Ensure=%+v", status)
	}
}

// TestRemoveStopsAndForgets covers removing a running server: the session is
// closed, the map entry is gone, and Ensure reports it as not configured.
func TestRemoveStopsAndForgets(t *testing.T) {
	server := newInMemoryServer()
	manager, _, connects := newTestManager(t, server, map[string]core.MCPConfig{"test": {}})

	if _, unavailable := manager.Ensure(context.Background(), []string{"test"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}

	manager.Remove("test")
	status := manager.Status([]string{"test"})[0]
	if status.State != stateUnavailable || status.Reason != "not configured" {
		t.Fatalf("status after Remove=%+v", status)
	}
	if _, ok := manager.lookupServer("test"); ok {
		t.Fatal("server is still in the map after Remove")
	}
	_, unavailable := manager.Ensure(context.Background(), []string{"test"})
	if unavailable["test"] != "not configured" {
		t.Fatalf("unavailable=%v", unavailable)
	}
	if got := connects.Load(); got != 1 {
		t.Fatalf("connects=%d want 1 (Remove must not reconnect)", got)
	}

	// Removing an unknown name is a no-op.
	manager.Remove("test")
}

// TestConcurrentEnsureAndPut exercises the map lock under -race: Ensure may
// connect while Put replaces the definition.
func TestConcurrentEnsureAndPut(t *testing.T) {
	server := newInMemoryServer()
	manager, _, _ := newTestManager(t, server, map[string]core.MCPConfig{"test": {}})
	ctx := context.Background()

	const workers = 16
	var wg sync.WaitGroup
	for range workers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				manager.Ensure(ctx, []string{"test"})
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				manager.Put("test", core.MCPConfig{Command: "fake"})
			}
		}()
	}
	wg.Wait()

	names, unavailable := manager.Ensure(ctx, []string{"test"})
	if len(unavailable) != 0 {
		t.Fatalf("unavailable after concurrent Put/Ensure=%v", unavailable)
	}
	if len(names) != 4 {
		t.Fatalf("tools=%v, want 4", names)
	}
}
