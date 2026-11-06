package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ridhomujizat/lana-harness/core"
)

// TestCallUnreachableAfterDrop covers AC-TFB-020: a dropped connection turns
// into a clear "not reachable" tool error instead of the raw transport error.
func TestCallUnreachableAfterDrop(t *testing.T) {
	server := newInMemoryServer()
	manager, registry, _ := newTestManager(t, server, map[string]core.MCPConfig{"dbx": {}})
	if _, unavailable := manager.Ensure(context.Background(), []string{"dbx"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}

	s, ok := manager.lookupServer("dbx")
	if !ok {
		t.Fatal("dbx is not configured")
	}
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if session == nil {
		t.Fatal("dbx has no session")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}

	out := callTool(t, registry, "mcp__dbx__echo", `{"text":"hello"}`)
	const prefix = "MCP server dbx is not reachable: "
	if out.Status != core.ToolStatusError {
		t.Fatalf("status=%q wanted error (output=%q)", out.Status, out.Output)
	}
	if !strings.HasPrefix(out.Output, prefix) {
		t.Fatalf("output=%q wanted prefix %q", out.Output, prefix)
	}
	if strings.HasPrefix(out.Output, "mcp: ") {
		t.Fatalf("output=%q kept the old mcp prefix", out.Output)
	}
	if strings.TrimPrefix(out.Output, prefix) == "" {
		t.Fatalf("output=%q has no short reason", out.Output)
	}

	// The rest of the output is shortReason of the transport error.
	if _, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "echo", Arguments: map[string]any{}}); err != nil {
		if want := prefix + shortReason(err); out.Output != want {
			t.Fatalf("output=%q want %q", out.Output, want)
		}
	} else {
		t.Fatal("closed session did not fail a call")
	}
}

func TestCallToolErrorIsNotUnreachable(t *testing.T) {
	server := newInMemoryServer()
	manager, registry, _ := newTestManager(t, server, map[string]core.MCPConfig{"dbx": {}})
	if _, unavailable := manager.Ensure(context.Background(), []string{"dbx"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}

	out := callTool(t, registry, "mcp__dbx__boom", `{}`)
	if out.Status != core.ToolStatusError || out.Output != "bad" {
		t.Fatalf("boom=%+v", out)
	}
	if strings.Contains(out.Output, "not reachable") {
		t.Fatalf("tool error was reported as unreachable: %q", out.Output)
	}
}

func TestCallNotRunningServer(t *testing.T) {
	server := newInMemoryServer()
	manager, registry, _ := newTestManager(t, server, map[string]core.MCPConfig{"dbx": {}})
	if _, unavailable := manager.Ensure(context.Background(), []string{"dbx"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}

	want := "MCP server dbx is not reachable: not running"
	manager.Put("dbx", core.MCPConfig{Command: "replacement"})
	out := callTool(t, registry, "mcp__dbx__echo", `{"text":"old"}`)
	if out.Status != core.ToolStatusError || out.Output != want {
		t.Fatalf("stopped server call=%+v want %q", out, want)
	}

	manager.Remove("dbx")
	out = callTool(t, registry, "mcp__dbx__echo", `{"text":"gone"}`)
	if out.Status != core.ToolStatusError || out.Output != want {
		t.Fatalf("removed server call=%+v want %q", out, want)
	}
}
