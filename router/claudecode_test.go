package router

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// AC-CCT-001, AC-CCT-002: claude-code addresses resolve without a provider
// entry and combos may list them; [providers.claude-code] is rejected.
func TestClaudeCodeResolveAndCombos(t *testing.T) {
	h := newHarness(t, Config{
		Providers: map[string]core.ProviderConfig{"ds": fakeProvider(t, "ds", &fakeBehavior{})},
		Combos:    map[string]Combo{"c": {Models: []string{"claude-code/sonnet", "ds/deepseek-flash"}}},
	})
	chain, err := h.r.Resolve("claude-code/opus")
	if err != nil || len(chain) != 1 || chain[0] != (Target{Provider: ClaudeCode, Model: "opus"}) {
		t.Fatalf("resolve = %v, %v", chain, err)
	}
	chain, err = h.r.Resolve("c")
	if err != nil || len(chain) != 2 || chain[0].Provider != ClaudeCode {
		t.Fatalf("combo = %v, %v", chain, err)
	}
	if _, err := h.r.Resolve("claude-code/-x"); !errors.Is(err, ErrUnknownAddress) {
		t.Fatalf("flag model: err = %v", err)
	}
	if _, err := New(Config{Providers: map[string]core.ProviderConfig{ClaudeCode: {Kind: "anthropic"}}}); err == nil || !strings.Contains(err.Error(), "built in") {
		t.Fatalf("reserved provider: err = %v", err)
	}
	// The router never streams Claude Code: a chain that reaches it moves on.
	stream, served, err := h.r.StreamChain(context.Background(), chain, core.ChatRequest{}, CallMeta{Source: "turn"}, nil)
	if err != nil || served.Provider != "ds" {
		t.Fatalf("stream chain: served=%v err=%v", served, err)
	}
	_, _ = drain(stream)
	if h.r.BackgroundModel() != "" {
		t.Fatal("background model set")
	}
	bg := newHarness(t, Config{BackgroundModel: "ds/deepseek-flash"})
	if bg.r.BackgroundModel() != "ds/deepseek-flash" {
		t.Fatalf("background model = %q", bg.r.BackgroundModel())
	}
}

// API-CCT-004 and the owner's scope addition: Models("claude-code") lists the
// aliases, then the models Claude Code reported, else the catalog.
func TestClaudeCodeModels(t *testing.T) {
	h := newHarness(t, Config{ClaudeCodeModels: []string{"claude-opus-5-5", "my-model"}})
	list, err := h.r.Models(context.Background(), ClaudeCode, false)
	if err != nil || len(list) < 4 {
		t.Fatalf("models = %v, %v", list, err)
	}
	if list[0].Address != "claude-code/opus" || list[0].Name != "Claude Code · Opus (latest)" || list[0].ContextWindow == 0 {
		t.Fatalf("alias = %+v", list[0])
	}
	if list[1].Name != "Claude Code · Sonnet (latest)" || list[2].Name != "Claude Code · Haiku (latest)" {
		t.Fatalf("aliases = %+v", list[:3])
	}
	if list[3].Address != "claude-code/claude-opus-5-5" || list[3].Name != "Claude Code · Opus 5.5" || list[3].Source != ClaudeSourceCatalog || list[0].Source != ClaudeSourceAlias {
		t.Fatalf("catalog fallback = %+v", list[3])
	}
	last := list[len(list)-1]
	if last.Address != "claude-code/my-model" || last.Name != "Claude Code · my-model" || last.Source != ClaudeSourceCustom {
		t.Fatalf("custom = %+v", last)
	}

	h.r.SetClaudeCodeModels([]core.RuntimeModel{
		{Value: "default", DisplayName: "Default (recommended)"},
		{Value: "opus", DisplayName: "Opus"},
		{Value: "claude-opus-5-5", DisplayName: "Opus 5.5"},
		{Value: "claude-sonnet-5-5[1m]", DisplayName: "Sonnet 5.5 (1M context)"},
		{Value: "-bad", DisplayName: "bad"},
	})
	list, _ = h.r.Models(context.Background(), ClaudeCode, false)
	var got []string
	for _, m := range list {
		got = append(got, m.Address+"="+m.Name+"="+m.Source)
	}
	want := []string{
		"claude-code/opus=Claude Code · Opus (latest)=alias",
		"claude-code/sonnet=Claude Code · Sonnet (latest)=alias",
		"claude-code/haiku=Claude Code · Haiku (latest)=alias",
		"claude-code/default=Claude Code · Default (recommended)=claude_code",
		"claude-code/claude-opus-5-5=Claude Code · Opus 5.5=claude_code",
		"claude-code/claude-sonnet-5-5[1m]=Claude Code · Sonnet 5.5 (1M context)=claude_code",
		"claude-code/my-model=Claude Code · my-model=custom",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("models:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if list[5].ContextWindow == 0 || !list[5].Vision {
		t.Fatalf("catalog facts by ID missing: %+v", list[5])
	}
	// The proxy model list never names Claude Code.
	for _, name := range h.r.Names() {
		if name == ClaudeCode {
			t.Fatal("Names lists claude-code")
		}
	}
}

// API-CCT-003: [router] background_model reaches the router config.
func TestConfigFromCoreBackgroundModel(t *testing.T) {
	cfg, err := ConfigFromCore(core.Config{Router: core.RouterConfig{BackgroundModel: " ds/x ", ClaudeCodeModels: []string{"m1"}}})
	if err != nil || cfg.BackgroundModel != "ds/x" || len(cfg.ClaudeCodeModels) != 1 {
		t.Fatalf("cfg = %+v err = %v", cfg, err)
	}
	used := usedProviders(core.Config{Agents: map[string]core.AgentConfig{
		"a": {Model: "claude-code/opus", Fallback: []string{"claude-code/haiku"}},
		"b": {Runtime: "claude-code", Model: "opus"},
	}})
	if used[ClaudeCode] {
		t.Fatalf("claude-code marked as a used provider: %v", used)
	}
}
