package lana

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// TestApplyConfigUpdatesPermissionEngine checks that a live config apply
// reaches the permission engine: a changed agent permission or allow rule
// decides the next tool call without a daemon restart.
func TestApplyConfigUpdatesPermissionEngine(t *testing.T) {
	use := "tool_use"
	end := "end_turn"
	text := "done"
	var responses []mock.Response
	for i := 0; i < 3; i++ {
		responses = append(responses,
			mock.Response{Events: []mock.Event{{ToolUse: &mock.ToolUse{ID: "c1", Name: "write_probe", Input: json.RawMessage("{}")}}, {Stop: &use}}},
			mock.Response{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
		)
	}
	base := "[providers.mock]\nkind = \"openai_compat\"\nbase_url = \"mock://local\"\n\n[agents.assistant]\ntoolset = \"custom\"\nprovider = \"mock\"\nmodel = \"m\"\npermission = \"ask\"\ntools = [\"write_probe\"]\n\n[[routes]]\nchannel = \"cli\"\nagent = \"assistant\"\n"
	approved := 0
	rt, err := New(context.Background(), Options{
		Home:      t.TempDir(),
		Layers:    []config.Layer{{Name: "test", TOML: []byte(base)}},
		Tools:     []core.Tool{probeTool{}},
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{Responses: responses})},
	}, ApproveWith(func(context.Context, core.ApprovalRequest) core.ApprovalOutcome {
		approved++
		return core.OutcomeAllowedOnce
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	ctx := context.Background()

	run := func() string {
		t.Helper()
		info, err := rt.CreateSession(ctx, "assistant")
		if err != nil {
			t.Fatal(err)
		}
		result, err := rt.Run(ctx, Request{Agent: "assistant", Prompt: "write", SessionID: info.ID})
		if err != nil {
			t.Fatal(err)
		}
		if result.Text != "done" {
			t.Fatalf("text = %q", result.Text)
		}
		return info.ID
	}
	apply := func(edit func(*core.AgentConfig)) {
		t.Helper()
		rt.cfgMu.RLock()
		cfg := rt.result.Config
		rt.cfgMu.RUnlock()
		agents := make(map[string]core.AgentConfig, len(cfg.Agents))
		for name, agentCfg := range cfg.Agents {
			agents[name] = agentCfg
		}
		agentCfg := agents["assistant"]
		edit(&agentCfg)
		agents["assistant"] = agentCfg
		cfg.Agents = agents
		if err := rt.ApplyConfig(cfg); err != nil {
			t.Fatal(err)
		}
	}

	run()
	if approved != 1 {
		t.Fatalf("ask policy: approvals = %d, want 1", approved)
	}

	apply(func(a *core.AgentConfig) { a.Permission = "trust" })
	sessionID := run()
	if approved != 1 {
		t.Fatalf("trust policy after apply: approvals = %d, want 1", approved)
	}
	policy, err := rt.Policy(ctx, sessionID)
	if err != nil || policy != core.PolicyTrust {
		t.Fatalf("policy = %q err=%v, want trust", policy, err)
	}

	apply(func(a *core.AgentConfig) {
		a.Permission = "ask"
		a.Rules.Allow = []string{"write_probe"}
	})
	run()
	if approved != 1 {
		t.Fatalf("allow rule after apply: approvals = %d, want 1", approved)
	}
}
