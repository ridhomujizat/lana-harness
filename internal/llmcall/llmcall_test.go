package llmcall

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

type stubProvider struct{ calls int }

func (p *stubProvider) Stream(context.Context, core.ChatRequest) (core.ChatStream, error) {
	p.calls++
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: "ok"}, nil)
	}, nil
}

type stubEmbedder struct{ stubProvider }

func (*stubEmbedder) Embed(context.Context, string, []string) ([][]float32, error) {
	return [][]float32{{1}}, nil
}

type stubRegistry map[string]core.Provider

func (r stubRegistry) Provider(name string) (core.Provider, bool) { p, ok := r[name]; return p, ok }
func (r stubRegistry) Names() []string                            { return nil }

// stubRouter resolves combos and records StreamChain and Embed calls.
type stubRouter struct {
	stubRegistry
	combos  map[string][]core.LLMTarget
	chain   []core.LLMTarget
	meta    core.LLMCallMeta
	address string
}

func (r *stubRouter) Resolve(address string) ([]core.LLMTarget, error) {
	if chain, ok := r.combos[address]; ok {
		return chain, nil
	}
	if t, ok := ParseTarget(address); ok && r.stubRegistry[t.Provider] != nil {
		return []core.LLMTarget{t}, nil
	}
	return nil, errors.New("unknown")
}

func (r *stubRouter) StreamChain(_ context.Context, chain []core.LLMTarget, _ core.ChatRequest, meta core.LLMCallMeta, _ func(core.LLMAttempt)) (core.ChatStream, core.LLMTarget, error) {
	r.chain, r.meta = chain, meta
	return func(func(core.ChatEvent, error) bool) {}, chain[len(chain)-1], nil
}

func (r *stubRouter) Embed(_ context.Context, address string, _ []string, meta core.LLMCallMeta) ([][]float32, error) {
	r.address, r.meta = address, meta
	return nil, nil
}

func newStubRouter() *stubRouter {
	return &stubRouter{
		stubRegistry: stubRegistry{"a": &stubProvider{}, "b": &stubProvider{}, "or": &stubEmbedder{}},
		combos:       map[string][]core.LLMTarget{"coding": {{Provider: "a", Model: "x"}, {Provider: "b", Model: "y"}}},
	}
}

// AC-RTR-004 (agent side): a combo is its list, an address one target, and
// the legacy form provider·model; fallback entries follow in both forms.
func TestChainForms(t *testing.T) {
	r := newStubRouter()
	cases := []struct {
		name, provider, model string
		fallback              []string
		want                  []core.LLMTarget
	}{
		{"combo", "", "coding", nil, []core.LLMTarget{{Provider: "a", Model: "x"}, {Provider: "b", Model: "y"}}},
		{"address", "", "or/openai/gpt-5-mini", []string{"b:y", "a/z"}, []core.LLMTarget{{Provider: "or", Model: "openai/gpt-5-mini"}, {Provider: "b", Model: "y"}, {Provider: "a", Model: "z"}}},
		{"legacy", "or", "openai/gpt-5-mini", nil, []core.LLMTarget{{Provider: "or", Model: "openai/gpt-5-mini"}}},
	}
	for _, tc := range cases {
		got, err := Chain(r, tc.provider, tc.model, tc.fallback)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: chain=%v err=%v", tc.name, got, err)
		}
	}
	if _, err := Chain(r, "", "nope/x", nil); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("unknown address err=%v", err)
	}
	if _, err := Chain(r, "a", "x", []string{"broken"}); err == nil {
		t.Fatal("invalid fallback accepted")
	}
	// Without a router, an address still resolves; a combo cannot.
	plain := stubRegistry{"a": &stubProvider{}}
	if got, err := Chain(plain, "", "a/x", nil); err != nil || len(got) != 1 || got[0].Model != "x" {
		t.Fatalf("plain address chain=%v err=%v", got, err)
	}
	if _, err := Chain(plain, "", "coding", nil); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("plain combo err=%v", err)
	}
}

func TestSelectRequestedDisplay(t *testing.T) {
	cases := []struct {
		name               string
		provider, model    string
		sel                core.ModelSelection
		wantP, wantM       string
		requested, display string
	}{
		{"no selection", "a", "x", core.ModelSelection{}, "a", "x", "a/x", "a:x"},
		{"legacy selection", "", "coding", core.ModelSelection{Provider: "b", Model: "y"}, "b", "y", "b/y", "b:y"},
		{"combo selection over legacy agent", "a", "x", core.ModelSelection{Model: "coding"}, "", "coding", "coding", "coding"},
		{"address selection", "a", "x", core.ModelSelection{Model: "b/y"}, "", "b/y", "b/y", "b/y"},
	}
	for _, tc := range cases {
		p, m := Select(tc.provider, tc.model, tc.sel)
		if p != tc.wantP || m != tc.wantM {
			t.Fatalf("%s: select=%q,%q", tc.name, p, m)
		}
		if got := Requested(p, m); got != tc.requested {
			t.Fatalf("%s: requested=%q", tc.name, got)
		}
		if got := Display(p, m); got != tc.display {
			t.Fatalf("%s: display=%q", tc.name, got)
		}
	}
}

func TestContextWindow(t *testing.T) {
	chain := []core.LLMTarget{{Provider: "a", Model: "deepseek-flash"}, {Provider: "b", Model: "deepseek-chat"}}
	if got := ContextWindow(0, chain); got != 128000 {
		t.Fatalf("smallest window=%d", got)
	}
	if got := ContextWindow(64000, chain); got != 64000 {
		t.Fatalf("agent window=%d", got)
	}
	if got := ContextWindow(0, []core.LLMTarget{{Provider: "a", Model: "unknown"}}); got != DefaultContextWindow {
		t.Fatalf("unknown window=%d", got)
	}
	if got := ContextWindow(0, []core.LLMTarget{{Provider: "a", Model: "deepseek-flash"}}); got != 1048576 {
		t.Fatalf("flash window=%d", got)
	}
}

func TestStreamRoutesOrCallsDirectly(t *testing.T) {
	r := newStubRouter()
	chain := []core.LLMTarget{{Provider: "a", Model: "x"}, {Provider: "b", Model: "y"}}
	_, served, err := Stream(context.Background(), r, chain, core.ChatRequest{}, core.LLMCallMeta{Source: SourceCompaction}, nil)
	if err != nil || served.Provider != "b" || r.meta.Source != SourceCompaction || len(r.chain) != 2 {
		t.Fatalf("routed served=%v meta=%+v err=%v", served, r.meta, err)
	}
	p := &stubProvider{}
	_, served, err = Stream(context.Background(), stubRegistry{"a": p}, chain, core.ChatRequest{}, core.LLMCallMeta{}, nil)
	if err != nil || served.Provider != "a" || p.calls != 1 {
		t.Fatalf("direct served=%v calls=%d err=%v", served, p.calls, err)
	}
	if _, _, err := Stream(context.Background(), r, nil, core.ChatRequest{}, core.LLMCallMeta{}, nil); err == nil {
		t.Fatal("empty chain accepted")
	}
}

// REQ-RTR-007: embeddings go through LLMRouter.Embed with source embedding.
func TestEmbedderRoutes(t *testing.T) {
	r := newStubRouter()
	e, ok := Embedder(r, "or")
	if !ok {
		t.Fatal("embedder not found")
	}
	if _, err := e.Embed(context.Background(), "openai/text-embedding-3-small", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if r.address != "or/openai/text-embedding-3-small" || r.meta.Source != SourceEmbedding {
		t.Fatalf("address=%q meta=%+v", r.address, r.meta)
	}
	if _, ok := Embedder(r, "a"); ok {
		t.Fatal("non-embedding provider returned an embedder")
	}
	if _, ok := Embedder(stubRegistry{"or": &stubEmbedder{}}, "or"); !ok {
		t.Fatal("plain registry embedder not found")
	}
}

// bgRouter is a stubRouter with a [router] background_model.
type bgRouter struct {
	*stubRouter
	background string
}

func (r bgRouter) BackgroundModel() string { return r.background }

// REQ-CCT-007: background passes never use claude-code targets: the rest of
// the chain, else [router] background_model, else ErrNoBackgroundModel.
func TestBackgroundChain(t *testing.T) {
	r := newStubRouter()
	r.combos["claude"] = []core.LLMTarget{{Provider: ClaudeCode, Model: "opus"}, {Provider: "a", Model: "x"}}
	chain, requested, err := BackgroundChain(r, []core.LLMTarget{{Provider: ClaudeCode, Model: "opus"}, {Provider: "a", Model: "x"}}, "claude")
	if err != nil || len(chain) != 1 || chain[0].Provider != "a" || requested != "claude" {
		t.Fatalf("rest: chain=%v requested=%q err=%v", chain, requested, err)
	}
	only := []core.LLMTarget{{Provider: ClaudeCode, Model: "opus"}}
	if _, _, err := BackgroundChain(r, only, "claude-code/opus"); !errors.Is(err, ErrNoBackgroundModel) {
		t.Fatalf("no background model: err = %v", err)
	}
	chain, requested, err = BackgroundChain(bgRouter{r, "b/y"}, only, "claude-code/opus")
	if err != nil || len(chain) != 1 || chain[0] != (core.LLMTarget{Provider: "b", Model: "y"}) || requested != "b/y" {
		t.Fatalf("background model: chain=%v requested=%q err=%v", chain, requested, err)
	}
	plain := []core.LLMTarget{{Provider: "a", Model: "x"}}
	if chain, _, _ := BackgroundChain(r, plain, "a/x"); !reflect.DeepEqual(chain, plain) {
		t.Fatalf("plain chain changed: %v", chain)
	}
}

// API-CCT-002: a legacy runtime agent is its claude-code address.
func TestNormalizeAgent(t *testing.T) {
	cases := map[string]string{"haiku": "claude-code/haiku", "": "claude-code/default", "claude-code/opus": "claude-code/opus"}
	for model, want := range cases {
		got := NormalizeAgent(core.AgentConfig{Runtime: "claude-code", Model: model, Provider: "x"})
		if got.Model != want || got.Runtime != "" || got.Provider != "" {
			t.Fatalf("model %q: %+v", model, got)
		}
	}
	if got := NormalizeAgent(core.AgentConfig{Model: "a/x"}); got.Model != "a/x" {
		t.Fatalf("native agent changed: %+v", got)
	}
	chain, requested, err := AgentChain(newStubRouter(), core.AgentConfig{Runtime: "claude-code", Model: "haiku"})
	if err == nil {
		t.Fatalf("stub router resolved claude-code without knowing it: %v %q", chain, requested)
	}
	if !IsClaudeCodeAddress("claude-code/opus") || IsClaudeCodeAddress("claude-code:opus") || IsClaudeCodeAddress("ds/x") {
		t.Fatal("IsClaudeCodeAddress")
	}
	if got := RuntimeProvider("claude-code"); got != "claude-code" {
		t.Fatalf("RuntimeProvider = %q", got)
	}
	if !WarnNoBackground("agent-x", "test") || WarnNoBackground("agent-x", "test") {
		t.Fatal("WarnNoBackground is not rate limited")
	}
}
