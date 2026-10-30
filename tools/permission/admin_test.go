package permission

import (
	"context"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// adminCall builds an mcp_manage add call with the admin risk class.
func adminCall(s *testSetup) core.ToolsPreExecuteInput {
	in := callInput(s.session.ID, "mcp_manage", `{"action":"add","name":"fs"}`)
	in.Call.Risk = core.RiskAdmin
	return in
}

// AC-AMCP-012: trust and an allow rule do not skip the question for admin.
func TestAdminAsksDespiteTrustAndAllowRule(t *testing.T) {
	s := setup(t, core.AgentConfig{
		Permission: "trust",
		Rules:      core.AgentRules{Allow: []string{"mcp_manage"}},
	}, false)

	t.Run("allowed_once", func(t *testing.T) {
		approval := &fakeApproval{outcome: core.OutcomeAllowedOnce}
		engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
		got, _ := engine.Handle(context.Background(), adminCall(s), nil)
		if !got.Allowed || got.Outcome != core.OutcomeAllowedOnce {
			t.Fatalf("got=%+v, want allowed_once", got)
		}
		if calls := approval.calls.Load(); calls != 1 {
			t.Fatalf("approval calls = %d, want 1", calls)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		approval := &fakeApproval{outcome: core.OutcomeRejected}
		engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
		got, _ := engine.Handle(context.Background(), adminCall(s), nil)
		if got.Allowed || !strings.Contains(got.Reason, "approval rejected") {
			t.Fatalf("got=%+v, want an approval rejection", got)
		}
		if calls := approval.calls.Load(); calls != 1 {
			t.Fatalf("approval calls = %d, want 1", calls)
		}
	})
}

// AC-AMCP-013: allowed_session allows the call once and creates no grant that
// a later admin call could reuse.
func TestAdminAllowedSessionDoesNotGrant(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "trust"}, false)
	approval := &fakeApproval{outcome: core.OutcomeAllowedSession}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}

	first := adminCall(s)
	if got, _ := engine.Handle(context.Background(), first, nil); !got.Allowed || got.Outcome != core.OutcomeAllowedSession {
		t.Fatalf("first call got=%+v, want allowed_session", got)
	}
	if calls := approval.calls.Load(); calls != 1 {
		t.Fatalf("approval calls after first = %d, want 1", calls)
	}

	// The fake approval writes no log events, so record the allowed_session
	// decision the real service would have written (see TestGrantReused).
	if _, err := s.store.Append(context.Background(), s.session.ID, []core.EventDraft{
		{Kind: core.KindToolCall, Payload: core.ToolCallPayload{V: 1, CallID: first.Call.CallID, Name: first.Call.Name, Input: first.Call.Input}},
		{Kind: core.KindApprovalDecided, Payload: core.ApprovalDecided{V: 1, RequestID: "r1", CallID: first.Call.CallID, Outcome: core.OutcomeAllowedSession}},
	}); err != nil {
		t.Fatal(err)
	}

	second := adminCall(s)
	if got, _ := engine.Handle(context.Background(), second, nil); !got.Allowed {
		t.Fatalf("second call got=%+v, want a fresh approval", got)
	}
	if calls := approval.calls.Load(); calls != 2 {
		t.Fatalf("approval calls after second = %d, want 2", calls)
	}
}

// AC-AMCP-014: policy never and deny rules reject admin without a question.
func TestAdminRejectedWithoutQuestion(t *testing.T) {
	t.Run("policy never", func(t *testing.T) {
		s := setup(t, core.AgentConfig{Permission: "never"}, false)
		approval := &fakeApproval{outcome: core.OutcomeAllowedOnce}
		engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
		got, _ := engine.Handle(context.Background(), adminCall(s), nil)
		if got.Allowed || !strings.Contains(got.Reason, "policy never") {
			t.Fatalf("got=%+v, want policy never", got)
		}
		if calls := approval.calls.Load(); calls != 0 {
			t.Fatalf("approval calls = %d, want 0", calls)
		}
	})

	t.Run("deny rule", func(t *testing.T) {
		s := setup(t, core.AgentConfig{
			Permission: "trust",
			Rules:      core.AgentRules{Deny: []string{"mcp_manage"}},
		}, false)
		approval := &fakeApproval{outcome: core.OutcomeAllowedOnce}
		engine := &Engine{Store: s.store, Config: s.config, Approval: approval}
		got, _ := engine.Handle(context.Background(), adminCall(s), nil)
		if got.Allowed || !strings.Contains(got.Reason, "deny rule") {
			t.Fatalf("got=%+v, want a deny rule rejection", got)
		}
		if calls := approval.calls.Load(); calls != 0 {
			t.Fatalf("approval calls = %d, want 0", calls)
		}
	})
}

// AC-AMCP-015: child sessions may never make an admin call.
func TestAdminChildRejected(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "trust"}, false)
	child := newChildSession(t, s, "coder")
	approval := &fakeApproval{outcome: core.OutcomeAllowedOnce}
	engine := &Engine{Store: s.store, Config: s.config, Approval: approval}

	in := callInputFor(child.ID, "coder", "mcp_manage", `{"action":"add","name":"fs"}`)
	in.Call.Risk = core.RiskAdmin
	got, _ := engine.Handle(context.Background(), in, nil)
	if got.Allowed || !strings.Contains(got.Reason, "child sessions may not use admin tools") {
		t.Fatalf("got=%+v, want a child session rejection", got)
	}
	if calls := approval.calls.Load(); calls != 0 {
		t.Fatalf("approval calls = %d, want 0", calls)
	}
}

func TestAdminWithoutApprovalService(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask"}, false)
	engine := &Engine{Store: s.store, Config: s.config}
	got, _ := engine.Handle(context.Background(), adminCall(s), nil)
	if got.Allowed || !strings.Contains(got.Reason, "approval is not available") {
		t.Fatalf("got=%+v, want approval is not available", got)
	}
}
