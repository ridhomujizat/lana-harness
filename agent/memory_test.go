package agent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// relevantMemoryEntries seeds an indexed memory: m4 matches all query tokens,
// m3 matches two, and m1 and m2 match one each. The filler makes the memory
// larger than MemoryInlineLimit.
func relevantMemoryEntries() []string {
	filler := strings.Repeat("note ", 60)
	entries := make([]string, 10)
	for i := range entries {
		entries[i] = "storage " + filler
	}
	entries[0] = "kenari storage " + filler     // m1
	entries[1] = "zeta storage " + filler       // m2
	entries[2] = "kenari zeta " + filler        // m3
	entries[3] = "kenari zeta deploy " + filler // m4
	return entries
}

// REQ-ECX-024: the first user message of a turn carries up to 3 relevant
// entries, and an id already shown in this session is not shown again.
func TestTurnRelevantMemoryContext(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("one"), textResponse("two"), textResponse("three")}},
		map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m"}})
	writeHomeFile(t, h.home, "assistant", "MEMORY.md", agenthome.Join(relevantMemoryEntries()))
	prompt := "kenari zeta deploy"

	h.run(t, prompt)
	first := userText(h.firstUserMessage(t))
	for _, want := range []string{
		"relevant memory:",
		"- m4: kenari zeta deploy ",
		"- m3: kenari zeta ",
		"- m1: kenari storage ",
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("first user message lacks %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "- m2:") {
		t.Fatalf("first user message carries a fourth relevant entry:\n%s", first)
	}

	head := h.systemPrompts(t)[0]
	if !strings.Contains(head, "# Your memory\nm1: ") || !strings.Contains(head, "Full text: memory_read with query or ids.") {
		t.Fatalf("head prompt lacks the memory index:\n%s", head)
	}
	if strings.Contains(head, "relevant memory:") {
		t.Fatalf("relevant memory leaked into the head prompt:\n%s", head)
	}

	// The second turn gets only the remaining matching id.
	h.run(t, prompt)
	second := userText(h.lastUserMessage(t))
	if !strings.Contains(second, "- m2: zeta storage ") || strings.Contains(second, "- m4:") {
		t.Fatalf("second turn relevant memory:\n%s", second)
	}

	// The third turn has no unseen entry at or above the minimum.
	h.run(t, prompt)
	third := userText(h.lastUserMessage(t))
	if strings.Contains(third, "relevant memory:") {
		t.Fatalf("third turn repeated relevant memory:\n%s", third)
	}
}

// REQ-ECX-024: relevant memory rides only the first user message of a turn, so
// the earlier messages stay byte-stable across the later steps (prefix cache).
func TestTurnRelevantMemoryPrefixStable(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "read_file", `{"path":"a.txt"}`),
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	writeHomeFile(t, h.home, "assistant", "MEMORY.md", agenthome.Join(relevantMemoryEntries()))
	reqs := captureRequests(h)

	h.run(t, "kenari zeta deploy")
	if len(*reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(*reqs))
	}
	if (*reqs)[0].System != (*reqs)[1].System {
		t.Fatalf("System changed across steps:\n%q\n---\n%q", (*reqs)[0].System, (*reqs)[1].System)
	}
	if !reflect.DeepEqual((*reqs)[1].Messages[:len((*reqs)[0].Messages)], (*reqs)[0].Messages) {
		t.Fatalf("earlier messages changed across steps:\n%+v\n%+v", (*reqs)[0].Messages, (*reqs)[1].Messages)
	}
	var withMemory int
	for _, message := range (*reqs)[1].Messages {
		if message.Role == core.RoleUser && strings.Contains(fmt.Sprint(message.Content), "relevant memory:") {
			withMemory++
		}
	}
	if withMemory != 1 {
		t.Fatalf("relevant memory appears in %d user messages, want 1", withMemory)
	}
}
