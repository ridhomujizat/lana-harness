package router

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// REQ-AGY-001, API-AGY-026: antigravity addresses resolve and combos may list
// them; the accounts are the built-in "antigravity" and every provider of
// kind "antigravity".
func TestAntigravityAccounts(t *testing.T) {
	h := newHarness(t, Config{
		Providers: map[string]core.ProviderConfig{
			"ds":       fakeProvider(t, "ds", &fakeBehavior{}),
			"agy-work": {Kind: core.AntigravityKind, Home: "/w", Label: "work"},
			"agy-off":  {Kind: core.AntigravityKind, Disabled: true},
		},
		Combos: map[string]Combo{"c": {Models: []string{"antigravity/gemini-4-neon-high", "ds/deepseek-flash"}}},
	})
	// The built-in account is first, then the providers of kind antigravity,
	// sorted.
	if got := h.r.AntigravityAccounts(); len(got) != 3 || got[0] != Antigravity || got[1] != "agy-off" || got[2] != "agy-work" {
		t.Fatalf("accounts = %v", got)
	}
	chain, err := h.r.Resolve("antigravity/gemini-3.8-flash-low")
	if err != nil || len(chain) != 1 || chain[0] != (Target{Provider: Antigravity, Model: "gemini-3.8-flash-low"}) {
		t.Fatalf("resolve = %v, %v", chain, err)
	}
	if chain, err := h.r.Resolve("agy-work/default"); err != nil || len(chain) != 1 || chain[0].Provider != "agy-work" {
		t.Fatalf("named resolve = %v, %v", chain, err)
	}
	if _, err := h.r.Resolve("agy-work/-x"); !errors.Is(err, ErrUnknownAddress) {
		t.Fatalf("flag model: err = %v", err)
	}
	// A combo may list an Antigravity target next to a normal provider.
	if _, err := h.r.Resolve("c"); err != nil {
		t.Fatalf("combo = %v", err)
	}
	if _, err := New(Config{Combos: map[string]Combo{"c": {Models: []string{"antigravity/-x"}}}}); err == nil || !strings.Contains(err.Error(), "invalid antigravity model") {
		t.Fatalf("invalid combo model: err = %v", err)
	}
	pc, ok := h.r.AntigravityAccount("agy-work")
	if !ok || pc.Kind != core.AntigravityKind || pc.Home != "/w" || pc.Label != "work" {
		t.Fatalf("account = %+v %v", pc, ok)
	}
	if builtin, ok := h.r.AntigravityAccount(Antigravity); !ok || builtin.Kind != core.AntigravityKind {
		t.Fatalf("built-in = %+v %v", builtin, ok)
	}
	if _, ok := h.r.AntigravityAccount("ds"); ok {
		t.Fatal("ds is an Antigravity account")
	}
	if kind, ok := h.r.Kind("agy-work"); !ok || kind != core.AntigravityKind || !h.r.Disabled("agy-off") || h.r.Disabled("agy-work") {
		t.Fatalf("kind = %q %v", kind, ok)
	}
	if kind, ok := h.r.Kind(Antigravity); !ok || kind != core.AntigravityKind {
		t.Fatalf("built-in kind = %q %v", kind, ok)
	}
	for _, name := range h.r.Names() {
		if name == Antigravity || strings.HasPrefix(name, "agy") {
			t.Fatalf("names = %v", h.r.Names())
		}
	}
	// The router never streams Antigravity: a chain that reaches it moves on.
	combo, err := h.r.Resolve("c")
	if err != nil || len(combo) != 2 {
		t.Fatalf("combo = %v, %v", combo, err)
	}
	stream, served, err := h.r.StreamChain(context.Background(), combo, core.ChatRequest{}, CallMeta{Source: "turn"}, nil)
	if err != nil || served.Provider != "ds" {
		t.Fatalf("stream chain: served=%v err=%v", served, err)
	}
	_, _ = drain(stream)
}

// REQ-AGY-008, AC-AGY-016: a chain whose only target is an Antigravity account
// is not_found, is never called, and does not cool the target down.
func TestAntigravityNotStreamed(t *testing.T) {
	h := newHarness(t, Config{})
	_, _, err := h.r.StreamChain(context.Background(), chain("antigravity/gemini-4-neon-high"), core.ChatRequest{}, CallMeta{}, nil)
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != core.ErrorClassNotFound || !strings.Contains(pe.Message, "antigravity targets run") {
		t.Fatalf("err = %v", err)
	}
	if len(h.r.Cooling()) != 0 {
		t.Fatalf("cooling = %v", h.r.Cooling())
	}
	// The failed attempt is recorded as not_found, at "turn" source.
	recs := h.rec.all()
	if len(recs) != 1 || recs[0].Outcome != core.ErrorClassNotFound || recs[0].Provider != Antigravity {
		t.Fatalf("records = %+v", recs)
	}
}

// REQ-AGY-009, API-AGY-026: Models(account) is the reported list with the
// account's addresses; it never returns a catalog, and an unknown account
// lists nothing.
func TestAntigravityRuntimeModels(t *testing.T) {
	h := newHarness(t, Config{Providers: map[string]core.ProviderConfig{"agy-work": {Kind: core.AntigravityKind}}})
	if got := h.r.RuntimeModels("agy-work"); len(got) != 0 || got == nil {
		t.Fatalf("empty = %v (nil=%v)", got, got == nil)
	}
	if got := h.r.RuntimeModels("nope"); len(got) != 0 || got == nil {
		t.Fatalf("unknown = %v (nil=%v)", got, got == nil)
	}
	h.r.SetRuntimeModels("agy-work", []core.RuntimeModel{
		{Value: "gemini-4-neon-high", DisplayName: "Gemini 4 Neon (high)"},
		{Value: "default"},
		{Value: ""},
		{Value: "-bad"},
	})
	got := h.r.RuntimeModels("agy-work")
	if len(got) != 4 || got[0].Value != "gemini-4-neon-high" {
		t.Fatalf("runtime models = %+v", got)
	}
	// RuntimeModels returns a copy.
	got[0].Value = "mutated"
	if h.r.RuntimeModels("agy-work")[0].Value != "gemini-4-neon-high" {
		t.Fatal("RuntimeModels did not return a copy")
	}
	list, err := h.r.Models(context.Background(), "agy-work", false)
	if err != nil || len(list) != 2 {
		t.Fatalf("models = %+v, %v", list, err)
	}
	if list[0].Address != "agy-work/gemini-4-neon-high" || list[0].ID != "gemini-4-neon-high" ||
		list[0].Name != "Antigravity · Gemini 4 Neon (high)" || list[0].Source != AntigravitySourceRuntime {
		t.Fatalf("row 0 = %+v", list[0])
	}
	if list[1].Address != "agy-work/default" || list[1].Name != "Antigravity · default" {
		t.Fatalf("row 1 = %+v", list[1])
	}
	// The built-in account is an Antigravity account too and lists nothing
	// until a list is set.
	if list, err := h.r.Models(context.Background(), Antigravity, false); err != nil || len(list) != 0 {
		t.Fatalf("built-in models = %+v, %v", list, err)
	}
	// An unknown account lists nothing and is never a catalog.
	if list, err := h.r.Models(context.Background(), "nope", false); err == nil || list != nil {
		t.Fatalf("unknown models = %+v, %v", list, err)
	}
	// A nil list clears nothing but reads back empty.
	h.r.SetRuntimeModels("agy-work", nil)
	if got := h.r.RuntimeModels("agy-work"); len(got) != 0 {
		t.Fatalf("after nil set = %+v", got)
	}
}

// API-AGY-003: a [providers.antigravity] table of kind "antigravity" holds the
// built-in account's settings; it never takes a home, cannot change the kind,
// and Apply updates it.
func TestBuiltinAntigravityAccountSettings(t *testing.T) {
	r, err := New(Config{Providers: map[string]core.ProviderConfig{
		Antigravity: {Kind: core.AntigravityKind, AllowScheduled: true, Binary: "/opt/agy", Label: "ignored"},
	}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	account, ok := r.AntigravityAccount(Antigravity)
	if !ok || account.Kind != core.AntigravityKind || !account.AllowScheduled || account.Binary != "/opt/agy" || account.Label != "" {
		t.Fatalf("built-in = %+v %v", account, ok)
	}
	if len(r.Names()) != 0 || r.Disabled(Antigravity) {
		t.Fatalf("names %v disabled %v", r.Names(), r.Disabled(Antigravity))
	}
	if err := r.Apply(Config{Providers: map[string]core.ProviderConfig{Antigravity: {Kind: core.AntigravityKind, Disabled: true}}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if account, _ := r.AntigravityAccount(Antigravity); account.AllowScheduled || !r.Disabled(Antigravity) {
		t.Fatalf("after apply = %+v disabled %v", account, r.Disabled(Antigravity))
	}
	if _, err := New(Config{Providers: map[string]core.ProviderConfig{Antigravity: {Kind: core.AntigravityKind, Home: "/x"}}}); err == nil || !strings.Contains(err.Error(), "home") {
		t.Fatalf("home: err = %v", err)
	}
	if _, err := New(Config{Providers: map[string]core.ProviderConfig{Antigravity: {Kind: "openai"}}}); err == nil || !strings.Contains(err.Error(), "built in") {
		t.Fatalf("kind: err = %v", err)
	}
	if _, err := New(Config{
		Providers: map[string]core.ProviderConfig{"agy": {Kind: core.AntigravityKind}},
		Combos:    map[string]Combo{"agy": {Models: []string{"agy/default"}}},
	}); err == nil {
		t.Fatal("a combo named like an Antigravity account was accepted")
	}
}

// REQ-AGY-004: usedProviders and missingSecrets skip Antigravity accounts the
// same way they skip Claude Code accounts.
func TestAntigravityUsedProviders(t *testing.T) {
	used := usedProviders(core.Config{
		Providers: map[string]core.ProviderConfig{
			"agy-work": {Kind: core.AntigravityKind},
			"claude-w": {Kind: core.ClaudeCodeKind},
		},
		Agents: map[string]core.AgentConfig{
			"a": {Model: "antigravity/gemini-4-neon-high", Fallback: []string{"agy-work/default"}},
		},
	})
	if used[Antigravity] || used["agy-work"] {
		t.Fatalf("antigravity marked as a used provider: %v", used)
	}
	// Even when an account is in use (here as the embedding provider), its key
	// is not checked at boot: the runtime checks it on its turn.
	if misses := missingSecrets(core.Config{
		Providers: map[string]core.ProviderConfig{"agy-work": {Kind: core.AntigravityKind, APIKeyEnv: "AGY_KEY"}},
		Knowledge: core.KnowledgeConfig{EmbeddingProvider: "agy-work"},
	}, nil); len(misses) != 0 {
		t.Fatalf("missingSecrets = %+v", misses)
	}
}
