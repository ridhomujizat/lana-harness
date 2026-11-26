package llmcall

import (
	"reflect"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// accountRegistry is a stub registry that knows Claude Code and Antigravity
// accounts.
type accountRegistry struct {
	stubRegistry
	claude map[string]core.ProviderConfig
	agy    map[string]core.ProviderConfig
}

func (r accountRegistry) ClaudeCodeAccount(name string) (core.ProviderConfig, bool) {
	if cfg, ok := r.claude[name]; ok {
		return cfg, true
	}
	if name == ClaudeCode {
		return core.ProviderConfig{Kind: core.ClaudeCodeKind}, true
	}
	return core.ProviderConfig{}, false
}

func (r accountRegistry) AntigravityAccount(name string) (core.ProviderConfig, bool) {
	if cfg, ok := r.agy[name]; ok {
		return cfg, true
	}
	if name == Antigravity {
		return core.ProviderConfig{Kind: core.AntigravityKind}, true
	}
	return core.ProviderConfig{}, false
}

// REQ-AGY-001, ADR 0022: ExternalRuntime names the runtime of a Claude Code or
// Antigravity provider, over a registry and over a config's providers.
func TestExternalRuntime(t *testing.T) {
	r := accountRegistry{
		stubRegistry: stubRegistry{"a": &stubProvider{}},
		claude:       map[string]core.ProviderConfig{"claude-work": {Kind: core.ClaudeCodeKind}},
		agy:          map[string]core.ProviderConfig{"agy-work": {Kind: core.AntigravityKind}},
	}
	cases := []struct {
		provider, want string
		ok             bool
	}{
		{"claude-code", ClaudeCode, true},
		{"claude-work", ClaudeCode, true},
		{"antigravity", Antigravity, true},
		{"agy-work", Antigravity, true},
		{"a", "", false},
	}
	for _, tc := range cases {
		if got, ok := ExternalRuntime(r, tc.provider); got != tc.want || ok != tc.ok {
			t.Fatalf("ExternalRuntime(%q) = %q,%v want %q,%v", tc.provider, got, ok, tc.want, tc.ok)
		}
	}
	providers := map[string]core.ProviderConfig{
		"claude-work": {Kind: core.ClaudeCodeKind},
		"agy-work":    {Kind: core.AntigravityKind},
		"ds":          {Kind: "deepseek"},
	}
	for _, tc := range cases {
		if got, ok := ExternalRuntimeIn(providers, tc.provider); got != tc.want || ok != tc.ok {
			t.Fatalf("ExternalRuntimeIn(%q) = %q,%v want %q,%v", tc.provider, got, ok, tc.want, tc.ok)
		}
	}
}

// REQ-AGY-001: WithoutExternal drops both Claude Code and Antigravity targets;
// the built-in names count without a registry.
func TestWithoutExternal(t *testing.T) {
	r := accountRegistry{
		claude: map[string]core.ProviderConfig{"claude-work": {Kind: core.ClaudeCodeKind}},
		agy:    map[string]core.ProviderConfig{"agy-work": {Kind: core.AntigravityKind}},
	}
	chain := []core.LLMTarget{
		{Provider: "antigravity", Model: "x"},
		{Provider: "claude-code", Model: "opus"},
		{Provider: "ds", Model: "y"},
	}
	want := []core.LLMTarget{{Provider: "ds", Model: "y"}}
	if got := WithoutExternal(r, chain); !reflect.DeepEqual(got, want) {
		t.Fatalf("WithoutExternal = %v", got)
	}
	if !HasExternal(r, chain) || HasExternal(r, want) {
		t.Fatal("HasExternal")
	}
	if !IsExternalAddress(r, "antigravity/x") || !IsExternalAddress(r, "claude-code/opus") || IsExternalAddress(r, "ds/y") {
		t.Fatal("IsExternalAddress")
	}
	if !IsExternalAddress(nil, "antigravity/x") || !IsExternalAddress(nil, "claude-code/opus") {
		t.Fatal("IsExternalAddress without a registry")
	}
	in := map[string]core.ProviderConfig{"agy-work": {Kind: core.AntigravityKind}, "ds": {}}
	if !IsExternalAddressIn(in, "agy-work/x") || !IsExternalAddressIn(in, "antigravity/x") || IsExternalAddressIn(in, "ds/y") {
		t.Fatal("IsExternalAddressIn")
	}
}

// Spec 78 REQ-RPR-009: a background pass keeps an Antigravity target; the
// router runs it through agy.
func TestBackgroundChainKeepsAntigravity(t *testing.T) {
	r := newStubRouter()
	chain := []core.LLMTarget{{Provider: Antigravity, Model: "x"}, {Provider: "a", Model: "y"}}
	got, requested, err := BackgroundChain(r, chain, "antigravity/x")
	if err != nil || !reflect.DeepEqual(got, chain) || requested != "antigravity/x" {
		t.Fatalf("chain=%v requested=%q err=%v", got, requested, err)
	}
	only := []core.LLMTarget{{Provider: Antigravity, Model: "x"}}
	if got, _, err := BackgroundChain(bgRouter{r, "b/y"}, only, "antigravity/x"); err != nil || !reflect.DeepEqual(got, only) {
		t.Fatalf("only: chain=%v err=%v", got, err)
	}
}

// REQ-AGY-001: the account home is $LANA_HOME/antigravity/<name>; the built-in
// account has none.
func TestAntigravityHome(t *testing.T) {
	t.Setenv("LANA_HOME", "/env/lana")
	cases := []struct{ name, want string }{
		{Antigravity, ""},
		{"", ""},
		{"agy-work", "/env/lana/antigravity/agy-work"},
		{"agy-2", "/env/lana/antigravity/agy-2"},
	}
	for _, tc := range cases {
		if got := AntigravityHome(tc.name); got != tc.want {
			t.Errorf("AntigravityHome(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// REQ-AGY-001: an Antigravity model is non-empty, does not start with "-", and
// has no whitespace anywhere.
func TestValidAntigravityModel(t *testing.T) {
	for _, model := range []string{"gemini-3.8-flash-low", "default", "gemini-4-neon-high"} {
		if !ValidAntigravityModel(model) {
			t.Errorf("ValidAntigravityModel(%q) = false", model)
		}
	}
	for _, model := range []string{"", "-x", "a b", "a\tb", "x\ny"} {
		if ValidAntigravityModel(model) {
			t.Errorf("ValidAntigravityModel(%q) = true", model)
		}
	}
}
