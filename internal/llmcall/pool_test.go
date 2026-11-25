package llmcall

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// poolRegistry is a stub registry that knows the subscription pools.
type poolRegistry struct {
	stubRegistry
	pools map[string][]string
}

func (r poolRegistry) PoolAccounts(runtime string) []string { return r.pools[runtime] }

// Spec 75 REQ-PAF-001: a pooled address (the built-in account) expands to one
// target per pooled account with the same model; a pinned account, a native
// target, an empty pool, and a registry without pools stay as they are.
func TestPool(t *testing.T) {
	reg := poolRegistry{pools: map[string][]string{
		Antigravity: {"agy-3", "antigravity", "agy-2", "agy-3"},
		ClaudeCode:  {"claude-code", "claude-b"},
	}}
	got := Pool(reg, core.LLMTarget{Provider: Antigravity, Model: "gemini-4-neon-high"})
	want := []core.LLMTarget{{Provider: "agy-3", Model: "gemini-4-neon-high"}, {Provider: "antigravity", Model: "gemini-4-neon-high"}, {Provider: "agy-2", Model: "gemini-4-neon-high"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("antigravity pool = %+v", got)
	}
	if got := Pool(reg, core.LLMTarget{Provider: ClaudeCode, Model: "opus"}); len(got) != 2 || got[1] != (core.LLMTarget{Provider: "claude-b", Model: "opus"}) {
		t.Fatalf("claude pool = %+v", got)
	}
	for _, target := range []core.LLMTarget{{Provider: "agy-2", Model: "m"}, {Provider: "ds", Model: "deepseek-flash"}} {
		if got := Pool(reg, target); len(got) != 1 || got[0] != target {
			t.Fatalf("Pool(%+v) = %+v", target, got)
		}
	}
	empty := poolRegistry{}
	if got := Pool(empty, core.LLMTarget{Provider: Antigravity, Model: "m"}); len(got) != 1 || got[0].Provider != Antigravity {
		t.Fatalf("empty pool = %+v", got)
	}
	if got := Pool(stubRegistry{}, core.LLMTarget{Provider: ClaudeCode, Model: "m"}); len(got) != 1 || got[0].Provider != ClaudeCode {
		t.Fatalf("no pools = %+v", got)
	}
}

// REQ-AGY-001: a named account's HOME is home with ~ and $LANA_HOME expanded,
// else $LANA_HOME/antigravity/<name>; the built-in account keeps the daemon's.
func TestAntigravityHomeDir(t *testing.T) {
	home := t.TempDir()
	if got := AntigravityHomeDir(home, Antigravity, core.ProviderConfig{Home: "/x"}); got != "" {
		t.Fatalf("built-in = %q", got)
	}
	if got := AntigravityHomeDir(home, "agy-2", core.ProviderConfig{}); got != filepath.Join(home, "antigravity", "agy-2") {
		t.Fatalf("default = %q", got)
	}
	if got := AntigravityHomeDir(home, "agy-2", core.ProviderConfig{Home: "$LANA_HOME/h/2"}); got != filepath.Join(home, "h", "2") {
		t.Fatalf("lana home = %q", got)
	}
	if got := AntigravityHomeDir(home, "agy-2", core.ProviderConfig{Home: "rel"}); got != filepath.Join(home, "rel") {
		t.Fatalf("relative = %q", got)
	}
	if got := AntigravityHomeDir(home, "agy-2", core.ProviderConfig{Home: "/abs/agy"}); got != "/abs/agy" {
		t.Fatalf("absolute = %q", got)
	}
}
