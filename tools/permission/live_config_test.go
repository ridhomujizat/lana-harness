package permission

import (
	"context"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// TestEngineReadsLiveConfig checks that an engine wired to the approval
// service's config sees ApplyConfig on the next call: a switch to trust and an
// added allow rule both skip the approval request.
func TestEngineReadsLiveConfig(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask"}, false)
	service := NewApprovalService(s.store, s.hooks, s.config)
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := &Engine{Store: s.store, Config: s.config, ConfigFn: service.CurrentConfig, Approval: approval}
	input := `{"path":"a.txt","content":"x"}`
	ctx := context.Background()

	if got, _ := engine.Handle(ctx, callInput(s.session.ID, "write_file", input), nil); got.Allowed {
		t.Fatal("ask policy allowed without approval")
	}
	if approval.calls.Load() != 1 {
		t.Fatalf("approval calls = %d, want 1", approval.calls.Load())
	}

	service.ApplyConfig(core.Config{Agents: map[string]core.AgentConfig{"coder": {Permission: "trust"}}})
	if got, _ := engine.Handle(ctx, callInput(s.session.ID, "write_file", input), nil); !got.Allowed {
		t.Fatalf("trust after apply rejected: %+v", got)
	}
	if policy, err := service.EffectivePolicy(ctx, s.session.ID); err != nil || policy != core.PolicyTrust {
		t.Fatalf("effective policy = %q err=%v, want trust", policy, err)
	}

	service.ApplyConfig(core.Config{Agents: map[string]core.AgentConfig{"coder": {
		Permission: "ask", Rules: core.AgentRules{Allow: []string{"write_file"}},
	}}})
	if got, _ := engine.Handle(ctx, callInput(s.session.ID, "write_file", input), nil); !got.Allowed {
		t.Fatalf("allow rule after apply rejected: %+v", got)
	}
	if approval.calls.Load() != 1 {
		t.Fatalf("approval calls = %d, want 1", approval.calls.Load())
	}
}
