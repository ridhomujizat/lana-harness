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

// REQ-CCT-010, API-CCT-012: a provider of kind claude-code is a Claude Code
// account: its addresses resolve, combos may list it, Models(account) is the
// Claude Code list with the account's addresses, and the router never
// streams to it.
func TestClaudeCodeAccounts(t *testing.T) {
	h := newHarness(t, Config{
		Providers: map[string]core.ProviderConfig{
			"ds":          fakeProvider(t, "ds", &fakeBehavior{}),
			"claude-work": {Kind: core.ClaudeCodeKind, ConfigDir: "/w", AllowScheduled: true},
			"claude-off":  {Kind: core.ClaudeCodeKind, Disabled: true},
		},
		Combos: map[string]Combo{"c": {Models: []string{"claude-work/opus", "claude-code/opus", "ds/deepseek-flash"}}},
	})
	chain, err := h.r.Resolve("claude-work/haiku")
	if err != nil || len(chain) != 1 || chain[0] != (Target{Provider: "claude-work", Model: "haiku"}) {
		t.Fatalf("resolve = %v, %v", chain, err)
	}
	if _, err := h.r.Resolve("claude-work/-x"); !errors.Is(err, ErrUnknownAddress) {
		t.Fatalf("flag model: err = %v", err)
	}
	pc, ok := h.r.ClaudeCodeAccount("claude-work")
	if !ok || pc.ConfigDir != "/w" || !pc.AllowScheduled {
		t.Fatalf("account = %+v %v", pc, ok)
	}
	if _, ok := h.r.ClaudeCodeAccount("ds"); ok {
		t.Fatal("ds is an account")
	}
	if builtin, ok := h.r.ClaudeCodeAccount(ClaudeCode); !ok || builtin.Kind != core.ClaudeCodeKind {
		t.Fatalf("built-in = %+v %v", builtin, ok)
	}
	if got := strings.Join(h.r.ClaudeCodeAccounts(), ","); got != "claude-off,claude-work" {
		t.Fatalf("accounts = %q", got)
	}
	if kind, ok := h.r.Kind("claude-work"); !ok || kind != core.ClaudeCodeKind || !h.r.Disabled("claude-off") || h.r.Disabled("claude-work") {
		t.Fatalf("kind = %q %v", kind, ok)
	}
	for _, name := range h.r.Names() {
		if strings.HasPrefix(name, "claude") {
			t.Fatalf("names = %v", h.r.Names())
		}
	}
	list, err := h.r.Models(context.Background(), "claude-work", false)
	builtin, _ := h.r.Models(context.Background(), ClaudeCode, false)
	if err != nil || len(list) != len(builtin) || list[0].Address != "claude-work/opus" || list[0].ID != builtin[0].ID || list[0].Name != builtin[0].Name {
		t.Fatalf("models = %+v, %v", list, err)
	}
	combo, err := h.r.Resolve("c")
	if err != nil || len(combo) != 3 {
		t.Fatalf("combo = %v, %v", combo, err)
	}
	stream, served, err := h.r.StreamChain(context.Background(), combo, core.ChatRequest{}, CallMeta{Source: "turn"}, nil)
	if err != nil || served.Provider != "ds" {
		t.Fatalf("stream chain: served=%v err=%v", served, err)
	}
	_, _ = drain(stream)
	// API-CCT-022: a claude-code table of another kind is refused; one of
	// kind claude-code holds the built-in settings (TestBuiltinClaudeAccountSettings).
	if _, err := New(Config{Providers: map[string]core.ProviderConfig{ClaudeCode: {Kind: "openai"}}}); err == nil {
		t.Fatal("a claude-code table of another kind was accepted")
	}
	if _, err := New(Config{
		Providers: map[string]core.ProviderConfig{"cw": {Kind: core.ClaudeCodeKind}},
		Combos:    map[string]Combo{"cw": {Models: []string{"cw/opus"}}},
	}); err == nil {
		t.Fatal("a combo named like an account was accepted")
	}
}

// API-CCT-022 (spec 39 revision 5): a [providers.claude-code] table of kind
// "claude-code" holds the built-in account's settings. It is not a
// configured account, it never takes a folder, and Apply updates it.
func TestBuiltinClaudeAccountSettings(t *testing.T) {
	r, err := New(Config{Providers: map[string]core.ProviderConfig{
		ClaudeCode: {Kind: core.ClaudeCodeKind, AllowScheduled: true, Binary: "/opt/claude", Label: "ignored"},
	}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	account, ok := r.ClaudeCodeAccount(ClaudeCode)
	if !ok || account.Kind != core.ClaudeCodeKind || !account.AllowScheduled || account.Binary != "/opt/claude" || account.Label != "" {
		t.Fatalf("built-in = %+v %v", account, ok)
	}
	if len(r.ClaudeCodeAccounts()) != 0 || len(r.Names()) != 0 || r.Disabled(ClaudeCode) {
		t.Fatalf("accounts %v names %v disabled %v", r.ClaudeCodeAccounts(), r.Names(), r.Disabled(ClaudeCode))
	}
	if err := r.Apply(Config{Providers: map[string]core.ProviderConfig{ClaudeCode: {Kind: core.ClaudeCodeKind, Disabled: true}}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if account, _ := r.ClaudeCodeAccount(ClaudeCode); account.AllowScheduled || !r.Disabled(ClaudeCode) {
		t.Fatalf("after apply = %+v disabled %v", account, r.Disabled(ClaudeCode))
	}
	if err := r.Apply(Config{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if account, _ := r.ClaudeCodeAccount(ClaudeCode); account.Kind != core.ClaudeCodeKind || account.AllowScheduled || account.Binary != "" || r.Disabled(ClaudeCode) {
		t.Fatalf("without the table = %+v", account)
	}
	if _, err := New(Config{Providers: map[string]core.ProviderConfig{ClaudeCode: {Kind: core.ClaudeCodeKind, ConfigDir: "/x"}}}); err == nil || !strings.Contains(err.Error(), "config_dir") {
		t.Fatalf("config_dir: err = %v", err)
	}
}
