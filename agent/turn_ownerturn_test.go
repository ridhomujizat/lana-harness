package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// ownerTurnProbe records the ToolCtx fields a tool call sees.
type ownerTurnProbe struct {
	mu    sync.Mutex
	calls int
	owner bool
	skill string
}

func (p *ownerTurnProbe) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "probe", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
}

func (p *ownerTurnProbe) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (p *ownerTurnProbe) Call(_ context.Context, tc core.ToolCtx, _ json.RawMessage) (core.ToolOutput, error) {
	p.mu.Lock()
	p.calls++
	p.owner = tc.OwnerTurn
	p.skill = tc.SkillWrite
	p.mu.Unlock()
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "ok"}, nil
}

func (p *ownerTurnProbe) seen() (int, bool, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.owner, p.skill
}

// REQ-DKN-021, REQ-DKN-032: a tool sees OwnerTurn true in a root owner turn
// and false in a child turn, and the agent's skill_write config in both.
func TestToolCtxOwnerTurn(t *testing.T) {
	agents := map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"probe"}, SkillWrite: "direct"},
	}
	script := mock.Script{Responses: []mock.Response{
		toolResponse("c1", "probe", `{}`),
		textResponse("done"),
	}}

	probe := func(h *harness) *ownerTurnProbe {
		p := &ownerTurnProbe{}
		if err := h.tools.Register(p); err != nil {
			t.Fatal(err)
		}
		h.hooks.ToolsPreExecute.SetDefault(func(context.Context, core.ToolsPreExecuteInput) (core.ToolDecision, error) {
			return core.ToolDecision{Allowed: true}, nil
		})
		return p
	}

	root := newHarness(t, script, agents)
	rootProbe := probe(root)
	if result := root.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("root result=%+v", result)
	}
	calls, owner, skill := rootProbe.seen()
	if calls != 1 || !owner {
		t.Fatalf("root turn: calls=%d OwnerTurn=%v want 1/true", calls, owner)
	}
	if skill != "direct" {
		t.Fatalf("root turn: SkillWrite=%q want direct", skill)
	}

	childHarness := newHarness(t, script, agents)
	childProbe := probe(childHarness)
	child, err := childHarness.store.Create(context.Background(), core.NewSession{
		Agent: "assistant", Channel: "library", Workspace: childHarness.ws, ParentSessionID: childHarness.info.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	childHarness.info = child
	if result := childHarness.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("child result=%+v", result)
	}
	calls, owner, _ = childProbe.seen()
	if calls != 1 || owner {
		t.Fatalf("child turn: calls=%d OwnerTurn=%v want 1/false", calls, owner)
	}
}
