package router

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// stubStreamer answers every runtime call with "from <account>" and records
// the calls.
type stubStreamer struct {
	mu    sync.Mutex
	calls []RuntimeCall
}

func (s *stubStreamer) RuntimeInstalled(string, string, core.ProviderConfig) bool { return true }

func (s *stubStreamer) StreamRuntime(_ context.Context, call RuntimeCall) (core.ChatStream, error) {
	s.mu.Lock()
	s.calls = append(s.calls, call)
	s.mu.Unlock()
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: "from " + call.Account}, nil)
	}, nil
}

func (s *stubStreamer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func userText(text string) core.Message {
	return core.Message{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: text}}}
}

// REQ-RPR-003: Antigravity gets the system text as a leading block of the
// prompt; Claude Code gets it apart. Thinking is left out; tools, tool
// results, and images fail with RuntimeInputMessage.
func TestRuntimePrompt(t *testing.T) {
	req := core.ChatRequest{System: "Be brief.", Messages: []core.Message{
		userText("Hi"),
		{Role: core.RoleAssistant, Content: []core.ContentBlock{{Kind: core.BlockThinking, Text: "hmm"}, {Kind: core.BlockText, Text: "Hello!"}}},
		userText("More?"),
	}}
	system, prompt, perr := runtimePrompt(core.AntigravityKind, req)
	want := "System instructions:\nBe brief.\n\nUser: Hi\n\nAssistant: Hello!\n\nUser: More?\n\nAnswer the last User message."
	if perr != nil || system != "" || prompt != want {
		t.Fatalf("antigravity = %q %q %v", system, prompt, perr)
	}
	system, prompt, perr = runtimePrompt(core.ClaudeCodeKind, core.ChatRequest{System: "Be brief.", Messages: []core.Message{userText("Hi")}})
	if perr != nil || system != "Be brief." || prompt != "Hi" {
		t.Fatalf("claude = %q %q %v", system, prompt, perr)
	}
	for name, bad := range map[string]core.ChatRequest{
		"tools":       {Tools: []core.ToolSpec{{Name: "x"}}, Messages: []core.Message{userText("Hi")}},
		"required":    {ToolChoice: "required", Messages: []core.Message{userText("Hi")}},
		"image":       {Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockImage, Data: []byte{1}}}}}},
		"tool result": {Messages: []core.Message{userText("Hi"), {Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockToolResult, ID: "1"}}}}},
	} {
		if _, _, perr := runtimePrompt(core.ClaudeCodeKind, bad); perr == nil || !runtimeInputError(perr) {
			t.Fatalf("%s = %v", name, perr)
		}
	}
	if _, prompt, perr := runtimePrompt(core.ClaudeCodeKind, core.ChatRequest{ToolChoice: "none", Tools: []core.ToolSpec{{Name: "x"}}, Messages: []core.Message{userText("Hi")}}); perr != nil || prompt != "Hi" {
		t.Fatalf("tool_choice none = %q %v", prompt, perr)
	}
}

// AC-RPR-006 (revised by REQ-RPR-009): with a streamer, a call of any source
// on a Claude Code target runs its CLI when the request is text only; a call
// with tools fails that target with client and the chain moves on. Without a
// streamer the target fails with not_found as before.
func TestRuntimeTargetSources(t *testing.T) {
	b := &fakeBehavior{}
	cfg := Config{Providers: map[string]core.ProviderConfig{"a": fakeProvider(t, "a", b)}}
	h := newHarness(t, cfg)
	plain := core.ChatRequest{Messages: []core.Message{userText("hi")}}
	_, _, err := h.r.StreamChain(context.Background(), chain("claude-code/sonnet"), plain, CallMeta{Source: "turn"}, nil)
	var perr *core.ProviderError
	if !errors.As(err, &perr) || perr.Class != core.ErrorClassNotFound || !strings.Contains(perr.Message, "claude-code targets run") {
		t.Fatalf("without streamer = %v", err)
	}

	s := &stubStreamer{}
	h.r.SetRuntimeStreamer(s)
	for _, source := range []string{"turn", "compaction", "avatar", "proxy"} {
		stream, target, err := h.r.StreamChain(context.Background(), chain("claude-code/sonnet", "a/m"), plain, CallMeta{Source: source}, nil)
		if err != nil || target.Provider != ClaudeCode {
			t.Fatalf("%s: target %v err %v", source, target, err)
		}
		if text, err := drain(stream); err != nil || text != "from claude-code" {
			t.Fatalf("%s: text %q err %v", source, text, err)
		}
	}
	withTools := plain
	withTools.Tools = []core.ToolSpec{{Name: "memory_write"}}
	before := s.count()
	stream, target, err := h.r.StreamChain(context.Background(), chain("claude-code/sonnet", "a/m"), withTools, CallMeta{Source: "turn"}, nil)
	if err != nil || target.Provider != "a" {
		t.Fatalf("tools: target %v err %v", target, err)
	}
	_, _ = drain(stream)
	if s.count() != before {
		t.Fatalf("runtime called for a request with tools")
	}
	recs := h.rec.all()
	last := recs[len(recs)-2]
	if last.Provider != ClaudeCode || last.Outcome != "client" || last.CostUSD == nil || *last.CostUSD != 0 {
		t.Fatalf("failure record = %+v", last)
	}
}
