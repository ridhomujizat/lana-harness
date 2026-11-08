package agent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// accountsLLM is a test registry that knows Claude Code accounts, as the
// router does (llmcall.ClaudeCodeAccounts).
type accountsLLM struct {
	*testLLM
	accounts map[string]core.ProviderConfig
}

func (a accountsLLM) ClaudeCodeAccount(name string) (core.ProviderConfig, bool) {
	if name == "claude-code" {
		return core.ProviderConfig{Kind: core.ClaudeCodeKind}, true
	}
	pc, ok := a.accounts[name]
	return pc, ok
}

// REQ-CCT-010 (agent side): a turn on "<account>/<model>" runs in Claude Code
// with the account's folder and keys; the record and the limits name the
// account; a switch to another account starts a new Claude session; the
// built-in account keeps the agent's keys and no folder.
func TestClaudeAccountTurns(t *testing.T) {
	h, rt := claudeHarness(t, mock.Script{}, map[string]core.AgentConfig{
		"assistant": {Model: "claude-a/opus", Binary: "/agent/claude", Auth: core.RuntimeAuthSubscription},
	}, func(_ context.Context, _ *fakeRuntime, turn core.RuntimeTurn) error {
		turn.Emit(core.RuntimeEvent{Kind: core.RuntimeSessionMeta, ClaudeSessionID: fakeClaudeID, Model: "claude-opus-5-5"})
		turn.Emit(core.RuntimeEvent{Kind: core.RuntimeLimits, Windows: []core.RuntimeLimitWindow{{Type: "five_hour", Status: "allowed"}}})
		turn.Emit(core.RuntimeEvent{Kind: core.RuntimeAssistantMessage, Text: "hi"})
		return end(turn, core.RuntimeEvent{StopReason: core.RuntimeStopEndTurn})
	})
	h.reg.llm = accountsLLM{testLLM: h.llm, accounts: map[string]core.ProviderConfig{
		"claude-a": {Kind: core.ClaudeCodeKind, Binary: "/a/claude", Auth: core.RuntimeAuthAPIKey, APIKeyEnv: "A_KEY"},
		"claude-b": {Kind: core.ClaudeCodeKind, ConfigDir: "/b/folder"},
	}}
	var mu sync.Mutex
	var usages []ExternalUsage
	var limits []string
	h.reg.SetExternalHooks(ExternalHooks{
		Usage: func(u ExternalUsage) { mu.Lock(); usages = append(usages, u); mu.Unlock() },
		Limits: func(provider, _ string, _ []core.RuntimeLimitWindow) {
			mu.Lock()
			limits = append(limits, provider)
			mu.Unlock()
		},
	})
	ctx := context.Background()
	h.run(t, "one")
	h.run(t, "two")
	if err := h.reg.SetModel(ctx, h.info.ID, "claude-b/sonnet"); err != nil {
		t.Fatal(err)
	}
	h.run(t, "three")
	if err := h.reg.SetModel(ctx, h.info.ID, "claude-code/haiku"); err != nil {
		t.Fatal(err)
	}
	h.run(t, "four")

	turns := rt.recorded()
	if len(turns) != 4 {
		t.Fatalf("turns = %d", len(turns))
	}
	a := turns[0].Config
	if a.Account != "claude-a" || a.ConfigDir != filepath.Join(h.reg.home, "claude", "claude-a") || a.Binary != "/a/claude" ||
		a.Auth != core.RuntimeAuthAPIKey || a.APIKeyEnv != "A_KEY" || a.Model != "opus" {
		t.Fatalf("claude-a config = %+v", a)
	}
	if turns[1].ResumeID != fakeClaudeID {
		t.Fatalf("the same account did not resume: %q", turns[1].ResumeID)
	}
	if b := turns[2].Config; b.Account != "claude-b" || b.ConfigDir != "/b/folder" || b.Binary != "" || turns[2].ResumeID != "" {
		t.Fatalf("claude-b turn = %+v (resume %q)", b, turns[2].ResumeID)
	}
	if c := turns[3].Config; c.Account != "claude-code" || c.ConfigDir != "" || c.Binary != "/agent/claude" || turns[3].ResumeID != "" {
		t.Fatalf("built-in turn = %+v (resume %q)", c, turns[3].ResumeID)
	}
	if closes := rt.closedSessions(); len(closes) != 2 {
		t.Fatalf("closes = %v (each account switch starts a new Claude session)", closes)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(usages) != 4 || usages[0].Provider != "claude-a" || usages[0].Requested != "claude-a/opus" ||
		usages[2].Provider != "claude-b" || usages[3].Provider != "claude-code" {
		t.Fatalf("usages = %+v", usages)
	}
	if len(limits) != 4 || limits[0] != "claude-a" || limits[2] != "claude-b" || limits[3] != "claude-code" {
		t.Fatalf("limits = %v", limits)
	}
}

// A disabled account fails before output, so the turn falls back to the next
// target like a disabled provider.
func TestDisabledClaudeAccountFallsBack(t *testing.T) {
	h, rt := claudeHarness(t, mock.Script{Responses: []mock.Response{textResponse("native answer")}},
		map[string]core.AgentConfig{"assistant": {Model: "claude-off/opus", Fallback: []string{"mock:m"}}}, helloScript)
	h.reg.llm = accountsLLM{testLLM: h.llm, accounts: map[string]core.ProviderConfig{
		"claude-off": {Kind: core.ClaudeCodeKind, Disabled: true},
	}}
	result := h.run(t, "hi")
	if result.Text != "native answer" || len(rt.recorded()) != 0 {
		t.Fatalf("result = %+v, runtime turns %d", result, len(rt.recorded()))
	}
}
