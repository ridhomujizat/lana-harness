package titles

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

// fakeRouter is a core.LLMRouter that records the chain, request, and call
// meta of every call. A non-nil gate blocks StreamChain until it is closed.
type fakeRouter struct {
	mu         sync.Mutex
	text       string
	err        error
	gate       chan struct{}
	background string
	calls      int
	chain      []core.LLMTarget
	req        core.ChatRequest
	meta       core.LLMCallMeta
	resolved   map[string][]core.LLMTarget
}

func (r *fakeRouter) Provider(string) (core.Provider, bool) { return nil, false }
func (r *fakeRouter) Names() []string                       { return nil }

func (r *fakeRouter) Resolve(address string) ([]core.LLMTarget, error) {
	if chain, ok := r.resolved[address]; ok {
		return chain, nil
	}
	i := strings.IndexAny(address, "/:")
	if i <= 0 {
		return nil, errors.New("unknown")
	}
	return []core.LLMTarget{{Provider: address[:i], Model: address[i+1:]}}, nil
}

func (r *fakeRouter) StreamChain(ctx context.Context, chain []core.LLMTarget, req core.ChatRequest, meta core.LLMCallMeta, _ func(core.LLMAttempt)) (core.ChatStream, core.LLMTarget, error) {
	r.mu.Lock()
	r.calls++
	r.chain, r.req, r.meta = chain, req, meta
	text, err, gate := r.text, r.err, r.gate
	r.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, core.LLMTarget{}, ctx.Err()
		}
	}
	if err != nil {
		return nil, core.LLMTarget{}, err
	}
	return func(yield func(core.ChatEvent, error) bool) {
		if !yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: text}, nil) {
			return
		}
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, chain[0], nil
}

func (r *fakeRouter) Embed(context.Context, string, []string, core.LLMCallMeta) ([][]float32, error) {
	return nil, errors.New("no embeddings")
}

func (r *fakeRouter) BackgroundModel() string { return r.background }

func (r *fakeRouter) snapshot() (int, []core.LLMTarget, core.ChatRequest, core.LLMCallMeta) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.chain, r.req, r.meta
}

func newSession(t *testing.T, store *memstore.Store, owner, reply string) string {
	t.Helper()
	ctx := context.Background()
	info, err := store.Create(ctx, core.NewSession{Agent: "assistant", Channel: "web", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	drafts := []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t1", Payload: core.TurnStart{V: 1, TurnID: "t1"}},
		{Kind: core.KindUserMessage, TurnID: "t1", Payload: core.UserMessage{V: 1, ContextBlocks: 1, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: "<context>\ntime: now\n</context>"},
			{Kind: core.BlockText, Text: owner},
		}}},
		{Kind: core.KindAssistantMessage, TurnID: "t1", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: reply}}}},
		{Kind: core.KindTurnEnd, TurnID: "t1", Payload: core.TurnEnd{V: 1, TurnID: "t1", Reason: "end_turn"}},
	}
	if owner == "" {
		drafts = []core.EventDraft{drafts[0], drafts[3]}
	}
	if _, err := store.Append(ctx, info.ID, drafts); err != nil {
		t.Fatal(err)
	}
	return info.ID
}

func newService(store core.SessionStore, router *fakeRouter, agents map[string]core.AgentConfig) *Service {
	var llm core.LLMRegistry
	if router != nil {
		llm = router
	}
	svc := New(context.Background(), store, llm)
	svc.LookupAgent = func(name string) (core.AgentConfig, bool) {
		cfg, ok := agents[name]
		return cfg, ok
	}
	return svc
}

func waitTitle(t *testing.T, store *memstore.Store, svc *Service, id string) core.SessionInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !svc.Pending(id) {
			info, err := store.Info(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			return info
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("title pass did not finish")
	return core.SessionInfo{}
}

// REQ-TTL-003: the reply is cleaned to one short title line.
func TestClean(t *testing.T) {
	cases := map[string]string{
		`"Fix the Telegram webhook"`:                       "Fix the Telegram webhook",
		"Title: Deploy lana to the VPS.":                   "Deploy lana to the VPS",
		"**Rencana liburan ke Bali**\n\nExtra line here":   "Rencana liburan ke Bali",
		"\n\n  “Plan the week”  \n":                        "Plan the week",
		"<think>let me think</think>\nRefactor the router": "Refactor the router",
		"# Session title: 'Budget review'":                 "Budget review",
		"Debug   the\tflaky   test.":                       "Debug the flaky test",
		"   ":                                              "",
		`""`:                                               "",
	}
	for raw, want := range cases {
		if got := Clean(raw); got != want {
			t.Errorf("Clean(%q) = %q, want %q", raw, got, want)
		}
	}
	long := Clean(strings.Repeat("word ", 30))
	if n := utf8.RuneCountInString(long); n > MaxAutoRunes || strings.HasSuffix(long, " ") || strings.Contains(long, "wor ") {
		t.Fatalf("long title %q (%d runes)", long, n)
	}
}

// REQ-TTL-004: the fallback is the first owner message on one line, cut at a
// word boundary to at most 50 characters with "…".
func TestFallback(t *testing.T) {
	if got := Fallback("  Short\nquestion  "); got != "Short question" {
		t.Fatalf("short = %q", got)
	}
	got := Fallback("Please help me write a migration plan for the billing database next week")
	if utf8.RuneCountInString(got) > MaxFallbackRunes || !strings.HasSuffix(got, "…") {
		t.Fatalf("long = %q", got)
	}
	if got != "Please help me write a migration plan for the…" {
		t.Fatalf("long = %q", got)
	}
	hard := Fallback(strings.Repeat("x", 80))
	if utf8.RuneCountInString(hard) != MaxFallbackRunes || !strings.HasSuffix(hard, "…") {
		t.Fatalf("hard = %q", hard)
	}
}

// REQ-TTL-002: the prompt carries the owner message and the reply, each cut
// to 2,000 characters.
func TestPrompt(t *testing.T) {
	p := Prompt(strings.Repeat("a", 2500), strings.Repeat("b", 2500))
	owner, reply, ok := strings.Cut(strings.TrimPrefix(p, "Owner message:\n"), "\n\nAssistant reply:\n")
	if !ok || owner != strings.Repeat("a", 2000)+"…" {
		t.Fatalf("owner section: %d runes", utf8.RuneCountInString(owner))
	}
	if !strings.HasPrefix(reply, strings.Repeat("b", 2000)+"…\n\n") {
		t.Fatalf("reply section = %q", reply[1990:])
	}
	if strings.Contains(Prompt("hi", ""), "Assistant reply") {
		t.Fatal("empty reply section")
	}
}

func TestOwnerTitleAndFixed(t *testing.T) {
	if got, ok := OwnerTitle("  My\n  plan "); !ok || got != "My plan" {
		t.Fatalf("OwnerTitle = %q %v", got, ok)
	}
	if _, ok := OwnerTitle(strings.Repeat("x", 81)); ok {
		t.Fatal("81 characters accepted")
	}
	if got := Fixed("Workflow", "Daily digest"); got != "Workflow · Daily digest" {
		t.Fatalf("Fixed = %q", got)
	}
	if got := Fixed("Calendar", strings.Repeat("long ", 40)); utf8.RuneCountInString(got) > MaxOwnerRunes {
		t.Fatalf("Fixed long = %q", got)
	}
}

// REQ-TTL-001, REQ-TTL-002: the pass uses the background chain through the
// router with source "title", low max tokens, and reasoning off, skipping
// Claude Code targets.
func TestTurnEndedUsesBackgroundChain(t *testing.T) {
	store := memstore.New()
	router := &fakeRouter{text: `"Deploy lana to the VPS."`}
	svc := newService(store, router, nil)
	id := newSession(t, store, "How do I deploy lana on my VPS?", "Use the install script, then systemd.")
	svc.TurnEnded(core.TitleRequest{SessionID: id, AgentName: "assistant", Requested: "combo",
		Targets: []core.LLMTarget{{Provider: "claude-code", Model: "opus"}, {Provider: "ds", Model: "chat"}}})
	info := waitTitle(t, store, svc, id)
	if info.Title != "Deploy lana to the VPS" || info.TitleSource != core.TitleSourceAuto {
		t.Fatalf("title = %q source = %q", info.Title, info.TitleSource)
	}
	calls, chain, req, meta := router.snapshot()
	if calls != 1 || len(chain) != 1 || chain[0].Provider != "ds" {
		t.Fatalf("calls=%d chain=%v", calls, chain)
	}
	if meta.Source != "title" || meta.SessionID != id || meta.Agent != "assistant" || meta.Requested != "combo" {
		t.Fatalf("meta = %+v", meta)
	}
	if req.MaxTokens > 64 || req.ReasoningEffort != "off" || len(req.Tools) != 0 {
		t.Fatalf("request = %+v", req)
	}
	if got := req.Messages[0].Content[0].Text; !strings.Contains(got, "How do I deploy lana") || strings.Contains(got, "<context>") || !strings.Contains(got, "install script") {
		t.Fatalf("prompt = %q", got)
	}
}

// REQ-CCT-007, REQ-TTL-004: an agent with only Claude Code and no
// background model gets the fallback title without a model call.
func TestClaudeCodeOnlyFallsBack(t *testing.T) {
	store := memstore.New()
	router := &fakeRouter{text: "never"}
	svc := newService(store, router, map[string]core.AgentConfig{"assistant": {Model: "claude-code/opus"}})
	id := newSession(t, store, "Summarize the meeting notes from Monday and list the follow-ups", "Here they are.")
	svc.TurnEnded(core.TitleRequest{SessionID: id, AgentName: "assistant"})
	info := waitTitle(t, store, svc, id)
	if info.Title != "Summarize the meeting notes from Monday and list…" || info.TitleSource != core.TitleSourceAuto {
		t.Fatalf("title = %q source = %q", info.Title, info.TitleSource)
	}
	if calls, _, _, _ := router.snapshot(); calls != 0 {
		t.Fatalf("model called %d times", calls)
	}
	// With a background model the same agent gets a generated title.
	router.background = "ds/chat"
	if _, err := svc.Regenerate(id, "assistant"); err != nil {
		t.Fatal(err)
	}
	calls, chain, _, meta := router.snapshot()
	if calls != 1 || chain[0].Provider != "ds" || meta.Source != "title" {
		t.Fatalf("calls=%d chain=%v meta=%+v", calls, chain, meta)
	}
}

// REQ-TTL-004: a failed call falls back to the owner message.
func TestFailedCallFallsBack(t *testing.T) {
	store := memstore.New()
	router := &fakeRouter{err: &core.ProviderError{Class: "server", Message: "down"}}
	svc := newService(store, router, map[string]core.AgentConfig{"assistant": {Model: "ds/chat"}})
	id := newSession(t, store, "Plan my trip", "Sure.")
	svc.TurnEnded(core.TitleRequest{SessionID: id, AgentName: "assistant"})
	if info := waitTitle(t, store, svc, id); info.Title != "Plan my trip" {
		t.Fatalf("title = %q", info.Title)
	}
	// An empty reply falls back too.
	router.err, router.text = nil, "  \n"
	if title, err := svc.Regenerate(id, "assistant"); err != nil || title != "Plan my trip" {
		t.Fatalf("regenerate = %q %v", title, err)
	}
}

// REQ-TTL-006: the pass never overwrites a title that exists, the owner's
// included, even one set while the call runs.
func TestOwnerTitleNeverOverwritten(t *testing.T) {
	store := memstore.New()
	router := &fakeRouter{text: "Model title", gate: make(chan struct{})}
	svc := newService(store, router, map[string]core.AgentConfig{"assistant": {Model: "ds/chat"}})
	id := newSession(t, store, "hello there", "hi")
	svc.TurnEnded(core.TitleRequest{SessionID: id, AgentName: "assistant"})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if calls, _, _, _ := router.snapshot(); calls == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pass did not call the model")
		}
		time.Sleep(time.Millisecond)
	}
	// A second turn end while the pass runs is a no-op (one pass per session).
	svc.TurnEnded(core.TitleRequest{SessionID: id, AgentName: "assistant"})
	if _, err := svc.Regenerate(id, "assistant"); !errors.Is(err, ErrBusy) {
		t.Fatalf("regenerate while busy: %v", err)
	}
	if _, err := store.SetSessionTitle(context.Background(), id, "Mine", core.TitleSourceOwner, false); err != nil {
		t.Fatal(err)
	}
	close(router.gate)
	info := waitTitle(t, store, svc, id)
	if info.Title != "Mine" || info.TitleSource != core.TitleSourceOwner {
		t.Fatalf("owner title overwritten: %q %q", info.Title, info.TitleSource)
	}
	// A later turn end leaves the owner title alone and calls no model.
	router.gate = nil
	svc.TurnEnded(core.TitleRequest{SessionID: id, AgentName: "assistant"})
	waitTitle(t, store, svc, id)
	if calls, _, _, _ := router.snapshot(); calls != 1 {
		t.Fatalf("model called again: %d", calls)
	}
	// Regenerate replaces it, since the owner asked.
	title, err := svc.Regenerate(id, "assistant")
	if err != nil || title != "Model title" {
		t.Fatalf("regenerate = %q %v", title, err)
	}
	if info, _ := store.Info(context.Background(), id); info.TitleSource != core.TitleSourceAuto {
		t.Fatalf("source after regenerate = %q", info.TitleSource)
	}
}

// A session without an owner message is not named.
func TestNothingToTitle(t *testing.T) {
	store := memstore.New()
	svc := newService(store, &fakeRouter{text: "x"}, map[string]core.AgentConfig{"assistant": {Model: "ds/chat"}})
	id := newSession(t, store, "", "")
	if _, err := svc.Regenerate(id, "assistant"); !errors.Is(err, ErrNothingToTitle) {
		t.Fatalf("err = %v", err)
	}
}

// The session's model selection decides the chain when the request has none.
func TestSelectionChain(t *testing.T) {
	store := memstore.New()
	router := &fakeRouter{text: "Chosen"}
	svc := newService(store, router, map[string]core.AgentConfig{"assistant": {Model: "ds/chat"}, "coder": {Model: "or/code"}})
	id := newSession(t, store, "hi", "hello")
	if _, err := store.Append(context.Background(), id, []core.EventDraft{{Kind: core.KindModelSelection, Payload: core.ModelSelection{V: 1, Agent: "coder"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Regenerate(id, "assistant"); err != nil {
		t.Fatal(err)
	}
	if _, chain, _, _ := router.snapshot(); len(chain) != 1 || chain[0].Provider != "or" {
		t.Fatalf("chain = %v", chain)
	}
}
