package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
	if out.Status != core.ToolStatusSuccess || out.Output != "added to memory (21/8000 characters)" {
		t.Fatalf("add = %+v", out)
	}
	callMemory(t, write, tc, `{"target":"memory","action":"add","content":"Server kenari runs Debian"}`)
	out = callMemory(t, read, tc, `{"target":"memory"}`)
	if !strings.HasPrefix(out.Output, "memory 49/8000 characters, 2 entries\nm1: Owner uses fish shell\nm2: Server kenari") {
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
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "m1: Name: Ridho") {
		t.Fatalf("read user = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "worker", "USER.md")); !os.IsNotExist(err) {
		t.Fatalf("per-agent USER.md must not be created: %v", err)
	}
}

// REQ-ECX-023: memory_read returns every entry by default, those asked by ids
// in full, and up to 10 ranked entries for a query. REQ-ECX-021: every entry
// carries its m<n> id.
func TestMemoryReadQueryIDsAndDefault(t *testing.T) {
	home := t.TempDir()
	tc := core.ToolCtx{AgentName: "worker", Home: home}
	entries := []string{
		"Kenari alpha", "Beta gamma", "Delta epsilon", "Zeta theta", "Iota kappa",
		"Lambda mu", "Nu xi", "Omicron pi", "Rho sigma", "Tau upsilon",
	}
	if err := agenthome.WriteFile(filepath.Join(home, "agents", "worker", "MEMORY.md"), agenthome.Join(entries)); err != nil {
		t.Fatal(err)
	}
	read := NewMemoryRead()

	out := callMemory(t, read, tc, `{"target":"memory"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("default read = %+v", out)
	}
	for i, entry := range entries {
		if !strings.Contains(out.Output, fmt.Sprintf("\nm%d: %s", i+1, entry)) {
			t.Fatalf("default read lacks m%d (%q):\n%s", i+1, entry, out.Output)
		}
	}

	out = callMemory(t, read, tc, `{"target":"memory","query":"kenari beta"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("query = %+v", out)
	}
	if !strings.Contains(out.Output, "m1: Kenari alpha") || !strings.Contains(out.Output, "m2: Beta gamma") {
		t.Fatalf("query result lacks the matching ids:\n%s", out.Output)
	}
	if strings.Index(out.Output, "m1:") > strings.Index(out.Output, "m2:") {
		t.Fatalf("query result is not best first:\n%s", out.Output)
	}
	if strings.Contains(out.Output, "m3:") {
		t.Fatalf("query returned an entry below the minimum:\n%s", out.Output)
	}

	out = callMemory(t, read, tc, `{"target":"memory","ids":["m4",2]}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("ids = %+v", out)
	}
	if !strings.Contains(out.Output, "m4: Zeta theta") || !strings.Contains(out.Output, "m2: Beta gamma") {
		t.Fatalf("ids result lacks requested entries:\n%s", out.Output)
	}
	if strings.Contains(out.Output, "m1:") || strings.Contains(out.Output, "m3:") {
		t.Fatalf("ids result returned unrequested entries:\n%s", out.Output)
	}

	out = callMemory(t, read, tc, `{"target":"memory","ids":["m99"]}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "m99") {
		t.Fatalf("out-of-range id = %+v", out)
	}
	out = callMemory(t, read, tc, `{"target":"memory","ids":["nope"]}`)
	if out.Status != core.ToolStatusError || !strings.HasPrefix(out.Output, "invalid input:") {
		t.Fatalf("bad id = %+v", out)
	}
}

func TestMemoryReadQueryCapsAtTen(t *testing.T) {
	home := t.TempDir()
	tc := core.ToolCtx{AgentName: "worker", Home: home}
	entries := make([]string, 12)
	query := make([]string, 0, 11)
	for i := range entries {
		entries[i] = fmt.Sprintf("needle%02d first", i+1)
		if i < 11 {
			query = append(query, fmt.Sprintf("needle%02d", i+1))
		}
	}
	if err := agenthome.WriteFile(filepath.Join(home, "agents", "worker", "MEMORY.md"), agenthome.Join(entries)); err != nil {
		t.Fatal(err)
	}
	out := callMemory(t, NewMemoryRead(), tc, `{"target":"memory","query":"`+strings.Join(query, " ")+`"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("query = %+v", out)
	}
	matches := 0
	for _, line := range strings.Split(out.Output, "\n") {
		if strings.HasPrefix(line, "m") && !strings.HasPrefix(line, "memory ") {
			matches++
		}
	}
	if matches != 10 {
		t.Fatalf("query returned %d entries, want 10:\n%s", matches, out.Output)
	}
}

// Every memory_read input carries a property description.
func TestMemoryReadSpecDescribesInputs(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(NewMemoryRead().Spec().InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"target", "query", "ids"} {
		if strings.TrimSpace(schema.Properties[name].Description) == "" {
			t.Fatalf("%s has no property description", name)
		}
	}
}

func TestMemoryUnavailableWithoutHome(t *testing.T) {
	out := callMemory(t, NewMemoryWrite(), core.ToolCtx{AgentName: "worker"}, `{"target":"memory","action":"add","content":"x"}`)
	if out.Status != core.ToolStatusError || out.Output != "memory is not available" {
		t.Fatalf("out = %+v", out)
	}
}
