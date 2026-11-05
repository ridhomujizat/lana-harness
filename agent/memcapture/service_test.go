package memcapture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

type fakeLLM struct{ provider core.Provider }

func (f fakeLLM) Provider(string) (core.Provider, bool) { return f.provider, true }
func (f fakeLLM) Names() []string                       { return []string{"mock"} }

// fakeProvider is a scripted provider. A non-nil gate blocks Stream until it is
// closed, so a test can hold a pass in flight.
type fakeProvider struct {
	mu      sync.Mutex
	text    string
	toolUse bool
	err     error
	gate    chan struct{}
	calls   int
	last    core.ChatRequest
}

func (p *fakeProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	p.calls++
	p.last = req
	text, toolUse, err, gate := p.text, p.toolUse, p.err, p.gate
	p.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return func(yield func(core.ChatEvent, error) bool) {
		if text != "" {
			if !yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: text}, nil) {
				return
			}
		}
		if toolUse {
			if !yield(core.ChatEvent{Kind: core.ChatBlock, Block: core.ContentBlock{Kind: core.BlockToolUse, ID: "c1", Name: "x", Input: json.RawMessage(`{}`)}}, nil) {
				return
			}
		}
		yield(core.ChatEvent{Kind: core.ChatUsage, Usage: core.Usage{InputTokens: 10, OutputTokens: 2, CachedInputTokens: 3}}, nil)
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

func (p *fakeProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *fakeProvider) lastRequest() core.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

// newTestService builds a Service over a fresh store and home.
func newTestService(t *testing.T, provider *fakeProvider) (*memstore.Store, *Service, string, string) {
	t.Helper()
	store := memstore.New()
	home := t.TempDir()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(context.Background(), store, fakeLLM{provider: provider})
	return store, svc, info.ID, home
}

func seedHistory(t *testing.T, store *memstore.Store, sessionID string) {
	t.Helper()
	_, err := store.Append(context.Background(), sessionID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hello"}}}},
		{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hi"}}}},
		{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// waitIdle waits for the session's pass to finish.
func waitIdle(t *testing.T, svc *Service, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		svc.mu.Lock()
		busy := svc.inFlight[sessionID]
		svc.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("capture pass did not finish")
}

// waitCalls waits until the provider has been called at least n times.
func waitCalls(t *testing.T, provider *fakeProvider, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if provider.count() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("provider calls = %d, want %d", provider.count(), n)
}

func capturedEvents(t *testing.T, store *memstore.Store, sessionID string) []core.MemoryCaptured {
	t.Helper()
	events, err := store.ChainEvents(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var out []core.MemoryCaptured
	for _, event := range events {
		if event.Kind != core.KindMemoryCaptured {
			continue
		}
		var payload core.MemoryCaptured
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, payload)
	}
	return out
}

func writeUserFile(t *testing.T, home, text string) {
	t.Helper()
	if err := os.WriteFile(agenthome.SharedUser(home), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func userEntries(t *testing.T, home string) []string {
	t.Helper()
	entries, err := agenthome.ReadEntries(agenthome.SharedUser(home))
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestCaptureReplaceOp(t *testing.T) {
	provider := &fakeProvider{text: `[{"target":"user","action":"replace","old_text":"Dipanggil \"Pak Ridho\"","content":"Dipanggil \"boss\""}]`}
	store, svc, id, home := newTestService(t, provider)
	writeUserFile(t, home, "Dipanggil \"Pak Ridho\"\n")
	seedHistory(t, store, id)

	svc.Capture(core.CaptureRequest{SessionID: id, TurnID: "t", AgentName: "assistant", Home: home, Provider: "mock", Model: "m", System: "head"})
	waitIdle(t, svc, id)

	entries := userEntries(t, home)
	if len(entries) != 1 || entries[0] != `Dipanggil "boss"` {
		t.Fatalf("user entries = %v", entries)
	}
	captured := capturedEvents(t, store, id)
	if len(captured) != 1 {
		t.Fatalf("captured events = %d, want 1", len(captured))
	}
	if captured[0].TurnID != "t" || len(captured[0].Ops) != 1 || captured[0].Ops[0].Action != "replace" {
		t.Fatalf("captured payload = %+v", captured[0])
	}
	if captured[0].Usage.InputTokens != 10 || captured[0].Usage.OutputTokens != 2 {
		t.Fatalf("captured usage = %+v", captured[0].Usage)
	}
}

func TestCaptureEmptyArrayWritesNothing(t *testing.T) {
	provider := &fakeProvider{text: "[]"}
	store, svc, id, home := newTestService(t, provider)
	writeUserFile(t, home, "keep me\n")
	seedHistory(t, store, id)

	svc.Capture(core.CaptureRequest{SessionID: id, TurnID: "t", AgentName: "assistant", Home: home, Provider: "mock", Model: "m"})
	waitIdle(t, svc, id)

	if entries := userEntries(t, home); len(entries) != 1 || entries[0] != "keep me" {
		t.Fatalf("user entries = %v", entries)
	}
	if captured := capturedEvents(t, store, id); len(captured) != 0 {
		t.Fatalf("captured events = %d, want 0", len(captured))
	}
}

func TestCaptureDropsBadReplies(t *testing.T) {
	cases := []struct {
		name     string
		provider *fakeProvider
	}{
		{name: "tool use", provider: &fakeProvider{text: `[{"target":"user","action":"add","content":"x"}]`, toolUse: true}},
		{name: "bad json", provider: &fakeProvider{text: "not a json array"}},
		{name: "json object", provider: &fakeProvider{text: `{"target":"user"}`}},
		{name: "secret content", provider: &fakeProvider{text: `[{"target":"user","action":"add","content":"token: abcdef123456"}]`}},
		{name: "provider error", provider: &fakeProvider{err: errors.New("boom")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, svc, id, home := newTestService(t, tc.provider)
			writeUserFile(t, home, "keep me\n")
			seedHistory(t, store, id)

			svc.Capture(core.CaptureRequest{SessionID: id, TurnID: "t", AgentName: "assistant", Home: home, Provider: "mock", Model: "m"})
			waitIdle(t, svc, id)

			if entries := userEntries(t, home); len(entries) != 1 || entries[0] != "keep me" {
				t.Fatalf("user entries = %v", entries)
			}
			if captured := capturedEvents(t, store, id); len(captured) != 0 {
				t.Fatalf("captured events = %d, want 0", len(captured))
			}
		})
	}
}

func TestCaptureTakesFirstFiveOps(t *testing.T) {
	provider := &fakeProvider{text: `[
		{"target":"user","action":"add","content":"one"},
		{"target":"user","action":"add","content":"two"},
		{"target":"user","action":"add","content":"three"},
		{"target":"user","action":"add","content":"four"},
		{"target":"user","action":"add","content":"five"},
		{"target":"user","action":"add","content":"six"}
	]`}
	store, svc, id, home := newTestService(t, provider)
	seedHistory(t, store, id)

	svc.Capture(core.CaptureRequest{SessionID: id, TurnID: "t", AgentName: "assistant", Home: home, Provider: "mock", Model: "m"})
	waitIdle(t, svc, id)

	entries := userEntries(t, home)
	if len(entries) != 5 {
		t.Fatalf("user entries = %v, want 5", entries)
	}
	for _, entry := range entries {
		if entry == "six" {
			t.Fatalf("the sixth op was applied: %v", entries)
		}
	}
	captured := capturedEvents(t, store, id)
	if len(captured) != 1 || len(captured[0].Ops) != 5 {
		t.Fatalf("captured = %+v", captured)
	}
}

// Invalid operations must not consume the maxOps quota: a valid op that follows
// five invalid ones is still applied (REQ-MEM-012, REQ-MEM-013).
func TestCaptureCountsOnlyValidOpsAgainstQuota(t *testing.T) {
	invalid := `{"target":"bogus","action":"add","content":"x"}`
	reply := "[" + strings.Join([]string{invalid, invalid, invalid, invalid, invalid, `{"target":"user","action":"add","content":"kept"}`}, ",") + "]"
	provider := &fakeProvider{text: reply}
	store, svc, id, home := newTestService(t, provider)
	seedHistory(t, store, id)

	svc.Capture(core.CaptureRequest{SessionID: id, TurnID: "t", AgentName: "assistant", Home: home, Provider: "mock", Model: "m"})
	waitIdle(t, svc, id)

	entries := userEntries(t, home)
	if len(entries) != 1 || entries[0] != "kept" {
		t.Fatalf("user entries = %v, want [kept]", entries)
	}
	if captured := capturedEvents(t, store, id); len(captured) != 1 || len(captured[0].Ops) != 1 {
		t.Fatalf("captured = %+v, want one applied op", captured)
	}
}

func TestCaptureSecondPassIgnoredWhileInFlight(t *testing.T) {
	gate := make(chan struct{})
	provider := &fakeProvider{text: "[]", gate: gate}
	store, svc, id, home := newTestService(t, provider)
	seedHistory(t, store, id)

	req := core.CaptureRequest{SessionID: id, TurnID: "t", AgentName: "assistant", Home: home, Provider: "mock", Model: "m"}
	svc.Capture(req)
	waitCalls(t, provider, 1)
	svc.Capture(req)
	close(gate)
	waitIdle(t, svc, id)

	if calls := provider.count(); calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
}

func TestCaptureRequestShape(t *testing.T) {
	provider := &fakeProvider{text: "[]"}
	store, svc, id, home := newTestService(t, provider)
	seedHistory(t, store, id)
	before, err := store.DeriveMessages(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	tools := []core.ToolSpec{{Name: "memory_write", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)}}

	svc.Capture(core.CaptureRequest{SessionID: id, TurnID: "t", AgentName: "assistant", Home: home, Provider: "mock", Model: "m", System: "the head", Tools: tools})
	waitIdle(t, svc, id)

	req := provider.lastRequest()
	if req.System != "the head" || !reflect.DeepEqual(req.Tools, tools) {
		t.Fatalf("request prefix changed: system=%q tools=%v", req.System, req.Tools)
	}
	if req.MaxTokens != captureMaxTokens {
		t.Fatalf("MaxTokens = %d", req.MaxTokens)
	}
	if len(req.Messages) != len(before)+1 {
		t.Fatalf("messages = %d, want %d", len(req.Messages), len(before)+1)
	}
	if !reflect.DeepEqual(req.Messages[:len(before)], before) {
		t.Fatalf("messages do not start with the derived history: %+v", req.Messages)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != core.RoleUser || !strings.HasPrefix(last.Content[0].Text, "<memory-capture>") {
		t.Fatalf("final message = %+v", last)
	}
	if !strings.Contains(last.Content[0].Text, "Current owner file (target=user):") {
		t.Fatalf("instruction is missing the owner file list")
	}
}

func TestInstructionListsEntries(t *testing.T) {
	text := captureInstruction([]string{"Dipanggil \"boss\""}, nil)
	if !strings.Contains(text, `Dipanggil "boss"`) {
		t.Fatalf("instruction misses the user entry:\n%s", text)
	}
	if !strings.Contains(text, "Current agent memory (target=memory):\n(empty)") {
		t.Fatalf("instruction misses the empty memory list:\n%s", text)
	}
}

// REQ-ECX-026: the capture instruction keeps memory hygienic and lets the turn
// prove an entry wrong, not only the owner's words.
func TestInstructionMemoryHygiene(t *testing.T) {
	text := captureInstruction(nil, []string{"nohup server & disown"})
	for _, want := range []string{
		"Look at what the latest turn proved, not only at what the owner said",
		"replace or remove that entry instead of adding a second one",
		"including a workaround that a tool now covers",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("instruction lacks %q:\n%s", want, text)
		}
	}
	for _, word := range []string{"MUST", "ALWAYS", "NEVER", "CRITICAL", "IMPORTANT"} {
		if strings.Contains(text, word) {
			t.Fatalf("instruction uses %q:\n%s", word, text)
		}
	}
}

// In index mode the capture instruction still lists every entry, because a
// replace or remove needs an exact old_text.
func TestInstructionListsAllEntries(t *testing.T) {
	entries := make([]string, 100)
	for i := range entries {
		entries[i] = fmt.Sprintf("entry %03d", i+1)
	}
	text := captureInstruction(nil, entries)
	for _, entry := range entries {
		if !strings.Contains(text, entry) {
			t.Fatalf("instruction misses %q", entry)
		}
	}
}
