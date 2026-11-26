package openaiproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

// fakeStreamer stands in for lana's runtime streamer (spec 78): it answers
// every call from its account's script and records the calls.
type fakeStreamer struct {
	mu        sync.Mutex
	installed map[string]bool
	// errs holds the error of an account's calls; text the answer of the
	// others.
	errs  map[string]*core.ProviderError
	text  []string
	usage core.Usage
	calls []router.RuntimeCall
}

func (f *fakeStreamer) RuntimeInstalled(_, account string, _ core.ProviderConfig) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installed[account]
}

func (f *fakeStreamer) StreamRuntime(_ context.Context, call router.RuntimeCall) (core.ChatStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	if !f.installed[call.Account] {
		return nil, &core.ProviderError{Class: core.ErrorClassNotFound, Message: "the CLI is not installed"}
	}
	if pe := f.errs[call.Account]; pe != nil {
		return nil, pe
	}
	text, usage := append([]string(nil), f.text...), f.usage
	return func(yield func(core.ChatEvent, error) bool) {
		for _, part := range text {
			if !yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: part}, nil) {
				return
			}
		}
		if !yield(core.ChatEvent{Kind: core.ChatUsage, Usage: usage}, nil) {
			return
		}
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

func (f *fakeStreamer) callList() []router.RuntimeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]router.RuntimeCall(nil), f.calls...)
}

// runtimeEnv is a proxy over one API provider "a", the built-in Claude Code
// and Antigravity accounts, Claude Code account cc-2, and the streamer.
func runtimeEnv(t *testing.T, combos map[string]router.Combo) (*env, *fakeProvider, *fakeStreamer) {
	t.Helper()
	p := &fakeProvider{events: textEvents("from ", "a"), models: []router.ModelInfo{{ID: "m"}}}
	e := newEnv(t, map[string]*fakeProvider{"a": p}, nil)
	cfg := e.router.Config()
	providers := cfg.Providers
	providers["cc-2"] = core.ProviderConfig{Kind: core.ClaudeCodeKind}
	providers["cc-off"] = core.ProviderConfig{Kind: core.ClaudeCodeKind, Disabled: true}
	providers["agy-x"] = core.ProviderConfig{Kind: core.AntigravityKind}
	if err := e.router.Apply(router.Config{Providers: providers, Combos: combos, Cooldown: time.Minute}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	f := &fakeStreamer{
		installed: map[string]bool{router.ClaudeCode: true, "cc-2": true, "cc-off": true, router.Antigravity: true},
		errs:      map[string]*core.ProviderError{},
		text:      []string{"Hel", "lo"},
		usage:     core.Usage{InputTokens: 12, OutputTokens: 3, CachedInputTokens: 2},
	}
	e.router.SetRuntimeStreamer(f)
	return e, p, f
}

// AC-RPR-001: GET /models lists the Claude Code and Antigravity accounts'
// models after the API providers, owned by their runtime; a disabled account
// and one whose CLI is not found list nothing.
func TestProxyRuntimeModels(t *testing.T) {
	e, _, _ := runtimeEnv(t, map[string]router.Combo{"fast": {Models: []string{"a/m"}}})
	e.router.SetRuntimeModels(router.Antigravity, []core.RuntimeModel{{Value: "gemini-4-neon-low"}, {Value: "gemini-4-neon-high"}})
	e.router.SetRuntimeModels("agy-x", []core.RuntimeModel{{Value: "gemini-4-neon-high"}})
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/router/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var list modelList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	owner := map[string]string{}
	order := map[string]int{}
	for i, m := range list.Data {
		owner[m.ID] = m.OwnedBy
		if _, ok := order[m.OwnedBy]; !ok {
			order[m.OwnedBy] = i
		}
	}
	for id, want := range map[string]string{
		"fast": "combo", "claude-code/sonnet": "claude-code", "cc-2/opus": "claude-code",
		"antigravity/gemini-4-neon": "antigravity",
	} {
		if owner[id] != want {
			t.Fatalf("%s owned by %q, want %q: %+v", id, owner[id], want, list.Data)
		}
	}
	for id := range owner {
		if strings.HasPrefix(id, "cc-off/") || strings.HasPrefix(id, "agy-x/") {
			t.Fatalf("models list names %q", id)
		}
	}
	if order["combo"] >= order["a"] || order["a"] >= order["claude-code"] || order["claude-code"] >= order["antigravity"] {
		t.Fatalf("order = %+v", order)
	}
}

// AC-RPR-002 (proxy side), REQ-RPR-003, REQ-RPR-004: a streaming and a
// non-streaming call to claude-code/<model> answer with the runtime's text and
// usage; the streamer gets the system text, the transcript prompt, the
// model, and the effort; the call record has source proxy and cost 0.
func TestProxyRuntimeCall(t *testing.T) {
	e, p, f := runtimeEnv(t, nil)
	body := map[string]any{
		"model": "claude-code/sonnet", "reasoning_effort": "high", "temperature": 0.2, "max_tokens": 50,
		"messages": []any{
			map[string]any{"role": "system", "content": "Be brief."},
			map[string]any{"role": "developer", "content": []any{map[string]any{"type": "text", "text": "Use English."}}},
			userMsg("Hi"),
			map[string]any{"role": "assistant", "content": "Hello!"},
			userMsg("Name a color."),
		},
	}
	resp := e.post(t, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	if got := resp.Header.Get(HeaderServedBy); got != "claude-code/sonnet" {
		t.Fatalf("served by %q", got)
	}
	var c completion
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatal(err)
	}
	if c.Model != "claude-code/sonnet" || *c.Choices[0].Message.Content != "Hello" || c.Choices[0].FinishReason != "stop" {
		t.Fatalf("completion = %+v", c)
	}
	if c.Usage.PromptTokens != 12 || c.Usage.CompletionTokens != 3 || c.Usage.TotalTokens != 15 || c.Usage.PromptTokensDetails.CachedTokens != 2 {
		t.Fatalf("usage = %+v", c.Usage)
	}
	calls := f.callList()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	call := calls[0]
	wantPrompt := "User: Hi\n\nAssistant: Hello!\n\nUser: Name a color.\n\nAnswer the last User message."
	if call.Kind != core.ClaudeCodeKind || call.Account != router.ClaudeCode || call.Model != "sonnet" || call.Effort != "high" ||
		call.System != "Be brief.\n\nUse English." || call.Prompt != wantPrompt {
		t.Fatalf("call = %+v", call)
	}
	if p.callCount() != 0 {
		t.Fatalf("API provider called")
	}

	// Streaming with include_usage; a single user message is the prompt as
	// it is.
	resp = e.post(t, map[string]any{"model": "claude-code/sonnet", "stream": true,
		"stream_options": map[string]any{"include_usage": true}, "messages": []any{userMsg("Hi")}})
	data := sseData(t, readBody(t, resp))
	var text strings.Builder
	var finish string
	var u *usage
	for _, d := range data {
		if d == "[DONE]" {
			continue
		}
		var ch chunk
		if err := json.Unmarshal([]byte(d), &ch); err != nil {
			t.Fatalf("chunk %q: %v", d, err)
		}
		if ch.Usage != nil {
			u = ch.Usage
		}
		for _, choice := range ch.Choices {
			if choice.Delta.Content != nil {
				text.WriteString(*choice.Delta.Content)
			}
			if choice.FinishReason != nil {
				finish = *choice.FinishReason
			}
		}
	}
	if text.String() != "Hello" || finish != "stop" || u == nil || u.PromptTokens != 12 || u.CompletionTokens != 3 {
		t.Fatalf("stream text %q finish %q usage %+v", text.String(), finish, u)
	}
	if got := f.callList()[1].Prompt; got != "Hi" {
		t.Fatalf("single prompt = %q", got)
	}
	recs := e.rec.records()
	if len(recs) != 2 {
		t.Fatalf("records = %+v", recs)
	}
	for _, rec := range recs {
		if rec.Source != "proxy" || rec.Provider != router.ClaudeCode || rec.Outcome != "ok" || rec.InputTokens != 12 ||
			rec.OutputTokens != 3 || rec.CostUSD == nil || *rec.CostUSD != 0 || !strings.HasPrefix(rec.Agent, "proxy:") {
			t.Fatalf("record = %+v", rec)
		}
	}
}

// AC-RPR-003: a request with tools to a Claude Code address answers 400 and
// runs nothing; a combo [claude-code/x, a/m] with tools answers from a/m; with
// tool_choice "none" the Claude Code target runs.
func TestProxyRuntimeTools(t *testing.T) {
	combos := map[string]router.Combo{"mix": {Models: []string{"claude-code/sonnet", "a/m"}}}
	e, p, f := runtimeEnv(t, combos)
	tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}}
	resp := e.post(t, map[string]any{"model": "claude-code/sonnet", "tools": tools, "messages": []any{userMsg("hi")}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	if got := decodeError(t, resp); got.Type != "invalid_request_error" || !strings.Contains(got.Message, router.RuntimeInputMessage) {
		t.Fatalf("error = %+v", got)
	}
	if len(f.callList()) != 0 {
		t.Fatalf("runtime called: %+v", f.callList())
	}
	resp = e.post(t, map[string]any{"model": "mix", "tools": tools, "messages": []any{userMsg("hi")}})
	if resp.StatusCode != http.StatusOK || resp.Header.Get(HeaderServedBy) != "a/m" {
		t.Fatalf("combo status = %d served by %q: %s", resp.StatusCode, resp.Header.Get(HeaderServedBy), readBody(t, resp))
	}
	if p.callCount() != 1 || len(f.callList()) != 0 {
		t.Fatalf("a calls %d, runtime calls %+v", p.callCount(), f.callList())
	}
	// An image part fails the same way.
	img := map[string]any{"role": "user", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,iVBORw0KGgo="}}}}
	resp = e.post(t, map[string]any{"model": "claude-code/sonnet", "messages": []any{img}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("image status = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp = e.post(t, map[string]any{"model": "claude-code/sonnet", "tools": tools, "tool_choice": "none", "messages": []any{userMsg("hi")}})
	if resp.StatusCode != http.StatusOK || len(f.callList()) != 1 {
		t.Fatalf("tool_choice none status = %d calls %d: %s", resp.StatusCode, len(f.callList()), readBody(t, resp))
	}
}

// AC-RPR-004: a limit error cools the account until its reset and the pool
// moves on to the next account; the next call skips the cooling account; a
// combo whose Antigravity target hits its limit answers from the next target.
func TestProxyRuntimeLimit(t *testing.T) {
	combos := map[string]router.Combo{"agy-first": {Models: []string{"antigravity/gemini-4-neon", "a/m"}}}
	e, _, f := runtimeEnv(t, combos)
	f.errs[router.ClaudeCode] = &core.ProviderError{Class: "rate_limit", Message: "Claude Code usage limit reached (five_hour)", RetryAfter: time.Hour}
	resp := e.post(t, map[string]any{"model": "claude-code/sonnet", "messages": []any{userMsg("hi")}})
	if resp.StatusCode != http.StatusOK || resp.Header.Get(HeaderServedBy) != "cc-2/sonnet" {
		t.Fatalf("status = %d served by %q: %s", resp.StatusCode, resp.Header.Get(HeaderServedBy), readBody(t, resp))
	}
	until, ok := e.router.Cooling()[router.Target{Provider: router.ClaudeCode, Model: "sonnet"}]
	if !ok || time.Until(until) < 50*time.Minute {
		t.Fatalf("cooling = %+v", e.router.Cooling())
	}
	if n := len(f.callList()); n != 2 {
		t.Fatalf("calls = %+v", f.callList())
	}
	resp = e.post(t, map[string]any{"model": "claude-code/sonnet", "messages": []any{userMsg("hi")}})
	calls := f.callList()
	if resp.StatusCode != http.StatusOK || len(calls) != 3 || calls[2].Account != "cc-2" {
		t.Fatalf("second call status %d calls %+v", resp.StatusCode, calls)
	}

	f.errs[router.Antigravity] = &core.ProviderError{Class: "rate_limit", Message: "Antigravity: quota exceeded"}
	resp = e.post(t, map[string]any{"model": "agy-first", "messages": []any{userMsg("hi")}})
	// The pool's next account, agy-x, has no CLI: the rest of the pool is
	// skipped and the combo moves on.
	if resp.StatusCode != http.StatusOK || resp.Header.Get(HeaderServedBy) != "a/m" {
		t.Fatalf("combo status = %d served by %q: %s", resp.StatusCode, resp.Header.Get(HeaderServedBy), readBody(t, resp))
	}
	if _, ok := e.router.Cooling()[router.Target{Provider: router.Antigravity, Model: "gemini-4-neon"}]; !ok {
		t.Fatalf("antigravity not cooling: %+v", e.router.Cooling())
	}
	// A failure that is not the account's own skips the rest of the pool.
	f.errs = map[string]*core.ProviderError{router.ClaudeCode: {Class: core.ErrorClassNotFound, Message: "Claude Code is not installed or not on PATH"}}
	before := len(f.callList())
	resp = e.post(t, map[string]any{"model": "claude-code/opus", "messages": []any{userMsg("hi")}})
	if resp.StatusCode != http.StatusNotFound || len(f.callList()) != before+1 {
		t.Fatalf("not found status = %d calls %+v: %s", resp.StatusCode, f.callList()[before:], readBody(t, resp))
	}
}
