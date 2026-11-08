package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/router"
	"github.com/ridhomujizat/lana-harness/store/memstore"
	"github.com/ridhomujizat/lana-harness/tools"
)

// newRoutedHarness builds a registry over a router with providers a (always
// auth error) and b (the mock script), and combo duo = [a/x, b/y].
func newRoutedHarness(t *testing.T, script mock.Script, agents map[string]core.AgentConfig) (*harness, *failingProvider) {
	t.Helper()
	a := &failingProvider{failures: 1000, class: "auth", status: 401}
	providers := map[string]core.Provider{"a": a, "b": mock.New(script)}
	combos := map[string]core.ComboConfig{"duo": {Models: []string{"a/x", "b/y"}}}
	rtr, err := router.New(router.Config{Combos: map[string]router.Combo{"duo": {Models: combos["duo"].Models}}, Cooldown: -1},
		router.WithProviders(providers),
		router.WithSleep(func(context.Context, time.Duration) error { return nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := memstore.New()
	ws := t.TempDir()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "library", Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	hooks := core.NewHooks()
	toolReg := tools.NewRegistry()
	cfg := core.Config{
		Providers: map[string]core.ProviderConfig{"a": {Kind: "openai_compat"}, "b": {Kind: "openai_compat"}},
		Combos:    combos,
		Agents:    agents,
	}
	home := t.TempDir()
	reg := NewRegistry(store, rtr, toolReg, hooks, cfg, nil, home, ws)
	tools.NewPipeline(hooks)
	return &harness{reg: reg, store: store, info: info, hooks: hooks, tools: toolReg, home: home, ws: ws}, a
}

func stepTargets(t *testing.T, h *harness, kind string) []core.LLMTarget {
	t.Helper()
	var out []core.LLMTarget
	for _, event := range h.events(t) {
		if event.Kind != kind {
			continue
		}
		var step struct{ Provider, Model string }
		if err := json.Unmarshal(event.Payload, &step); err != nil {
			t.Fatal(err)
		}
		out = append(out, core.LLMTarget{Provider: step.Provider, Model: step.Model})
	}
	return out
}

// AC-RTR-004, AC-RTR-007 (agent side): a combo agent's turn starts on the
// primary, writes the attempt, and step/end names the served target.
func TestRoutedComboTurn(t *testing.T) {
	h, a := newRoutedHarness(t, mock.Script{Responses: []mock.Response{textResponse("from b")}},
		map[string]core.AgentConfig{"assistant": {Model: "duo"}})
	result := h.run(t, "hi")
	if result.Err != nil || result.Text != "from b" {
		t.Fatalf("result=%+v", result)
	}
	if a.calls.Load() != 1 {
		t.Fatalf("a calls=%d", a.calls.Load())
	}
	if got := stepTargets(t, h, core.KindStepStart); len(got) != 1 || got[0] != (core.LLMTarget{Provider: "a", Model: "x"}) {
		t.Fatalf("step/start=%v", got)
	}
	if got := stepTargets(t, h, core.KindStepEnd); len(got) != 1 || got[0] != (core.LLMTarget{Provider: "b", Model: "y"}) {
		t.Fatalf("step/end=%v", got)
	}
	if got := stepTargets(t, h, core.KindAssistantAttempt); len(got) != 1 || got[0] != (core.LLMTarget{Provider: "a", Model: "x"}) {
		t.Fatalf("attempts=%v", got)
	}
}

// API-RTR-003: the session model switch takes a combo, an address, or
// provider:model. provider:model keeps the legacy stored shape; a combo or an
// address is stored as model with no provider.
func TestRoutedSetModelForms(t *testing.T) {
	h, _ := newRoutedHarness(t, mock.Script{Responses: []mock.Response{textResponse("one"), textResponse("two")}},
		map[string]core.AgentConfig{"assistant": {Provider: "b", Model: "z"}})
	ctx := context.Background()

	if err := h.reg.SetModel(ctx, h.info.ID, "duo"); err != nil {
		t.Fatal(err)
	}
	if sel := lastSelection(t, h); sel.Provider != "" || sel.Model != "duo" {
		t.Fatalf("combo selection=%+v", sel)
	}
	h.run(t, "first")
	if got := stepTargets(t, h, core.KindStepStart); got[len(got)-1] != (core.LLMTarget{Provider: "a", Model: "x"}) {
		t.Fatalf("combo step/start=%v", got)
	}

	if err := h.reg.SetModel(ctx, h.info.ID, "b/openai/gpt-5-mini"); err != nil {
		t.Fatal(err)
	}
	if sel := lastSelection(t, h); sel.Provider != "" || sel.Model != "b/openai/gpt-5-mini" {
		t.Fatalf("address selection=%+v", sel)
	}
	h.run(t, "second")
	if got := stepTargets(t, h, core.KindStepEnd); got[len(got)-1] != (core.LLMTarget{Provider: "b", Model: "openai/gpt-5-mini"}) {
		t.Fatalf("address step/end=%v", got)
	}

	if err := h.reg.SetModel(ctx, h.info.ID, "b:m2"); err != nil {
		t.Fatal(err)
	}
	if sel := lastSelection(t, h); sel.Provider != "b" || sel.Model != "m2" {
		t.Fatalf("legacy selection=%+v", sel)
	}

	for _, bad := range []string{"nope", "nope/x", "nope:x"} {
		if err := h.reg.SetModel(ctx, h.info.ID, bad); !errors.Is(err, ErrUnknownProvider) {
			t.Fatalf("SetModel(%q) err=%v", bad, err)
		}
	}
	if err := h.reg.SetModel(ctx, h.info.ID, "b/"); !errors.Is(err, ErrEmptyModel) {
		t.Fatalf("empty model err=%v", err)
	}
}
