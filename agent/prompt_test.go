package agent

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// captureRequests records every ChatRequest the turn loop sends.
func captureRequests(h *harness) *[]core.ChatRequest {
	var mu sync.Mutex
	reqs := []core.ChatRequest{}
	h.hooks.LLMStream.Use(func(ctx context.Context, in core.LLMStreamInput, next core.Next[core.LLMStreamInput, core.ChatStream]) (core.ChatStream, error) {
		mu.Lock()
		reqs = append(reqs, in.Request)
		mu.Unlock()
		return next(ctx, in)
	})
	return &reqs
}

func specNames(specs []core.ToolSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

// REQ-LTC-001: two turns of one session with unchanged config send a
// byte-identical prefix.
func TestTurnPrefixStableAcrossTurns(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("a"), textResponse("b")}},
		map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"write_file", "read_file", "bash"}}})
	reqs := captureRequests(h)

	h.run(t, "one")
	h.run(t, "two")

	if len(*reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(*reqs))
	}
	if (*reqs)[0].System != (*reqs)[1].System {
		t.Fatalf("System changed between turns:\n%q\n---\n%q", (*reqs)[0].System, (*reqs)[1].System)
	}
	if !reflect.DeepEqual((*reqs)[0].Tools, (*reqs)[1].Tools) {
		t.Fatalf("Tools changed between turns:\n%v\n---\n%v", specNames((*reqs)[0].Tools), specNames((*reqs)[1].Tools))
	}
}

// REQ-LTC-003: tool declarations are sorted by name.
func TestTurnToolsSorted(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}},
		map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"write_file", "read_file", "bash"}}})
	reqs := captureRequests(h)

	h.run(t, "hi")

	if len(*reqs) == 0 {
		t.Fatal("no request recorded")
	}
	got := specNames((*reqs)[0].Tools)
	want := []string{"bash", "read_file", "write_file"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

// REQ-LTC-002: a memory change keeps System unchanged and sends only the
// "# Your memory" section as a <system-update>.
func TestTurnMemoryChangeSendsMemoryUpdateOnly(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("a"), textResponse("b")}}, nil)
	writeHomeFile(t, h.home, "assistant", "MEMORY.md", "Server kenari")
	reqs := captureRequests(h)

	h.run(t, "one")
	writeHomeFile(t, h.home, "assistant", "MEMORY.md", "Server bandung")
	h.run(t, "two")

	if len(*reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(*reqs))
	}
	if (*reqs)[0].System != (*reqs)[1].System {
		t.Fatalf("System changed with only the memory file:\n%q\n---\n%q", (*reqs)[0].System, (*reqs)[1].System)
	}
	update := userText(h.lastUserMessage(t))
	if !strings.Contains(update, "<system-update>") {
		t.Fatalf("second turn lacks a <system-update>:\n%s", update)
	}
	if !strings.Contains(update, "# Your memory") || !strings.Contains(update, "Server bandung") {
		t.Fatalf("<system-update> lacks the new memory section:\n%s", update)
	}
	for _, other := range []string{"# Runtime context", "# When something blocks you", "# About the user", "# Skills", "# Your team"} {
		if strings.Contains(update, other) {
			t.Fatalf("<system-update> must hold only the memory section, found %q:\n%s", other, update)
		}
	}
}

// REQ-LTC-002: a compaction summary after the head rewrites the head on the
// next turn.
func TestTurnNewHeadAfterCompaction(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("a"), textResponse("b")}}, nil)
	h.run(t, "one")
	if got := len(h.systemPrompts(t)); got != 1 {
		t.Fatalf("system messages = %d, want 1", got)
	}

	events := &Events{Store: h.store, SessionID: h.info.ID}
	if _, err := events.Append(context.Background(), core.EventDraft{
		Kind:    core.KindCompactionSummary,
		Payload: core.CompactionSummary{V: core.PayloadVersion, Summary: "older context", UptoSeq: 1},
	}); err != nil {
		t.Fatal(err)
	}

	h.run(t, "two")
	if got := len(h.systemPrompts(t)); got != 2 {
		t.Fatalf("system messages after a compaction = %d, want 2", got)
	}
}
