package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

func callMemory(t *testing.T, tool core.Tool, tc core.ToolCtx, input string) core.ToolOutput {
	t.Helper()
	out, err := tool.Call(context.Background(), tc, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMemoryTools(t *testing.T) {
	home := t.TempDir()
	tc := core.ToolCtx{AgentName: "worker", Home: home}
	read, write := NewMemoryRead(), NewMemoryWrite()

	out := callMemory(t, write, tc, `{"target":"memory","action":"add","content":"Owner uses fish shell"}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "added to memory (21/4000 characters)" {
		t.Fatalf("add = %+v", out)
	}
	callMemory(t, write, tc, `{"target":"memory","action":"add","content":"Server kenari runs Debian"}`)
	out = callMemory(t, read, tc, `{"target":"memory"}`)
	if !strings.HasPrefix(out.Output, "memory 49/4000 characters, 2 entries\n1. Owner uses fish shell\n2. Server kenari") {
		t.Fatalf("read = %q", out.Output)
	}
	out = callMemory(t, write, tc, `{"target":"memory","action":"replace","content":"x","old_text":"s"}`)
	if out.Status != core.ToolStatusError || out.Output != "old_text matches 2 entries in memory; use more specific text" {
		t.Fatalf("ambiguous = %+v", out)
	}
	out = callMemory(t, write, tc, `{"target":"memory","action":"remove","old_text":"nothing"}`)
	if out.Status != core.ToolStatusError || out.Output != "no entry in memory contains old_text" {
		t.Fatalf("no match = %+v", out)
	}
	out = callMemory(t, write, tc, `{"target":"memory","action":"remove","old_text":"fish"}`)
	if out.Status != core.ToolStatusSuccess || !strings.HasPrefix(out.Output, "removed 1 entry from memory") {
		t.Fatalf("remove = %+v", out)
	}
	out = callMemory(t, write, tc, `{"target":"user","action":"add","content":"`+strings.Repeat("u", 2001)+`"}`)
	if out.Status != core.ToolStatusError || out.Output != "user would be 2001/2000 characters; remove or merge entries first" {
		t.Fatalf("over limit = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "worker", "USER.md")); !os.IsNotExist(err) {
		t.Fatalf("USER.md should not exist after rejected write: %v", err)
	}
	if _, err := os.Stat(agenthome.SharedUser(home)); !os.IsNotExist(err) {
		t.Fatalf("shared USER.md should not exist after rejected write: %v", err)
	}
	out = callMemory(t, write, tc, `{"target":"memory","action":"add"}`)
	if out.Status != core.ToolStatusError || out.Output != "invalid input: content is required" {
		t.Fatalf("missing content = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "worker", "AGENT.md")); !os.IsNotExist(err) {
		t.Fatal("memory tools must not create AGENT.md")
	}
}

// TestMemoryUserWritesSharedFileCoversACMEM009 covers AC-MEM-009: target user
// writes $LANA_HOME/USER.md and never creates a per-agent USER.md.
func TestMemoryUserWritesSharedFileCoversACMEM009(t *testing.T) {
	home := t.TempDir()
	tc := core.ToolCtx{AgentName: "worker", Home: home}
	out := callMemory(t, NewMemoryWrite(), tc, `{"target":"user","action":"add","content":"Name: Ridho"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("add user = %+v", out)
	}
	data, err := os.ReadFile(agenthome.SharedUser(home))
	if err != nil {
		t.Fatalf("read shared user file: %v", err)
	}
	if !strings.Contains(string(data), "Name: Ridho") {
		t.Fatalf("shared USER.md = %q", data)
	}
	out = callMemory(t, NewMemoryRead(), tc, `{"target":"user"}`)
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "1. Name: Ridho") {
		t.Fatalf("read user = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "worker", "USER.md")); !os.IsNotExist(err) {
		t.Fatalf("per-agent USER.md must not be created: %v", err)
	}
}

func TestMemoryUnavailableWithoutHome(t *testing.T) {
	out := callMemory(t, NewMemoryWrite(), core.ToolCtx{AgentName: "worker"}, `{"target":"memory","action":"add","content":"x"}`)
	if out.Status != core.ToolStatusError || out.Output != "memory is not available" {
		t.Fatalf("out = %+v", out)
	}
}
