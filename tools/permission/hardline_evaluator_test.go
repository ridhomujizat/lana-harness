package permission

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

// hardlineWant is the exact refusal of REQ-HLF-010.
func hardlineWant(rule, reason string) string {
	return "blocked by hardline rule " + rule + " (" + reason + "). Do not retry or work around it; ask the owner to run it themselves or to approve it."
}

// homeEngine builds an engine whose home is a fresh temp dir, so home-based
// hardline rules match under test.
func homeEngine(t *testing.T, s *testSetup, approval core.ApprovalService) *Engine {
	t.Helper()
	home := t.TempDir()
	return &Engine{Store: s.store, Config: s.config, Approval: approval, Home: func() string { return home }}
}

// TestHardlineBlockNeedsVerified covers AC-HLF-001: an allow rule and the trust
// policy cannot let a Block call run; allowed_once is rejected, and only
// allowed_verified allows.
func TestHardlineBlockNeedsVerified(t *testing.T) {
	t.Run("allowed_once is rejected", func(t *testing.T) {
		s := setup(t, core.AgentConfig{Permission: "trust", Rules: core.AgentRules{Allow: []string{"bash(rm *)"}}}, false)
		approval := &fakeApproval{outcome: core.OutcomeAllowedOnce}
		engine := homeEngine(t, s, approval)
		got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"rm -rf ~"}`), nil)
		if got.Allowed {
			t.Fatalf("block-tier call allowed without a step-up: %+v", got)
		}
		if approval.calls.Load() != 1 {
			t.Fatalf("approval calls=%d, want 1", approval.calls.Load())
		}
		if got.Reason != hardlineWant("HL-B-001", reasonB001) {
			t.Fatalf("reason=%q", got.Reason)
		}
	})

	t.Run("allowed_verified is allowed", func(t *testing.T) {
		s := setup(t, core.AgentConfig{Permission: "trust", Rules: core.AgentRules{Allow: []string{"bash(rm *)"}}}, false)
		approval := &fakeApproval{outcome: core.OutcomeAllowedVerified}
		engine := homeEngine(t, s, approval)
		got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"rm -rf ~"}`), nil)
		if !got.Allowed || got.Outcome != core.OutcomeAllowedOnce {
			t.Fatalf("verified allow: %+v", got)
		}
	})
}

// TestHardlineEscalateIgnoresGrant covers AC-HLF-002: a session grant for bash
// does not cover an Escalate call; the owner is asked again.
func TestHardlineEscalateIgnoresGrant(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask"}, false)
	if _, err := s.store.Append(context.Background(), s.session.ID, []core.EventDraft{
		{Kind: core.KindToolCall, Payload: core.ToolCallPayload{V: 1, CallID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"cargo test"}`)}},
		{Kind: core.KindApprovalDecided, Payload: core.ApprovalDecided{V: 1, RequestID: "r1", CallID: "c1", Outcome: core.OutcomeAllowedSession}},
	}); err != nil {
		t.Fatal(err)
	}
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := homeEngine(t, s, approval)
	got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"sudo ls"}`), nil)
	if got.Allowed {
		t.Fatalf("grant bypassed the hardline floor: %+v", got)
	}
	if approval.calls.Load() != 1 {
		t.Fatalf("approval calls=%d, want 1 (owner asked again)", approval.calls.Load())
	}
}

// TestHardlineForcePushEscalates covers AC-HLF-006: under trust, git push
// --force reaches the owner with an Escalate match.
func TestHardlineForcePushEscalates(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "trust"}, false)
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := homeEngine(t, s, approval)
	got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"git push --force"}`), nil)
	if got.Allowed {
		t.Fatalf("force push allowed: %+v", got)
	}
	if approval.calls.Load() != 1 {
		t.Fatalf("approval calls=%d, want 1", approval.calls.Load())
	}
	if approval.last.Hardline == nil || approval.last.Hardline.Rule != "HL-E-003" || approval.last.Hardline.Tier != core.HardlineEscalate {
		t.Fatalf("hardline=%+v", approval.last.Hardline)
	}
}

// TestHardlineRunsUnderEveryPolicy covers AC-HLF-012: hardline evaluation runs
// under trust, auto, and ask, and never rejects without asking.
func TestHardlineRunsUnderEveryPolicy(t *testing.T) {
	for _, policy := range []string{"trust", "auto", "ask"} {
		t.Run(policy, func(t *testing.T) {
			s := setup(t, core.AgentConfig{Permission: policy}, false)
			approval := &fakeApproval{outcome: core.OutcomeRejected}
			engine := homeEngine(t, s, approval)
			got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"rm -rf ~"}`), nil)
			if got.Allowed {
				t.Fatalf("%s: hardline not applied: %+v", policy, got)
			}
			if approval.calls.Load() != 1 {
				t.Fatalf("%s: approval calls=%d, want 1", policy, approval.calls.Load())
			}
			if approval.last.Hardline == nil || approval.last.Hardline.Tier != core.HardlineBlock {
				t.Fatalf("%s: hardline=%+v", policy, approval.last.Hardline)
			}
		})
	}

	t.Run("never rejects without asking", func(t *testing.T) {
		s := setup(t, core.AgentConfig{Permission: "never"}, false)
		approval := &fakeApproval{outcome: core.OutcomeAllowedVerified}
		engine := homeEngine(t, s, approval)
		got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"rm -rf ~"}`), nil)
		if got.Allowed || !strings.Contains(got.Reason, "blocked by hardline rule HL-B-001") {
			t.Fatalf("never: got=%+v", got)
		}
		if approval.calls.Load() != 0 {
			t.Fatalf("never: approval calls=%d, want 0", approval.calls.Load())
		}
	})

	// A session's own never policy lets its allow rules run other calls
	// (AC-PERM-007), but never a hardline call.
	t.Run("never with an allow rule still rejects", func(t *testing.T) {
		s := setup(t, core.AgentConfig{Permission: "never", Rules: core.AgentRules{Allow: []string{"bash(rm *)"}}}, false)
		approval := &fakeApproval{outcome: core.OutcomeAllowedVerified}
		engine := homeEngine(t, s, approval)
		got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"rm -rf ~"}`), nil)
		if got.Allowed || !strings.Contains(got.Reason, "blocked by hardline rule HL-B-001") {
			t.Fatalf("never+allow: got=%+v", got)
		}
		if approval.calls.Load() != 0 {
			t.Fatalf("never+allow: approval calls=%d, want 0", approval.calls.Load())
		}
	})
}

// TestHardlineAllowSessionBecomesOnceNoGrant covers AC-HLF-015 and AC-HLF-017:
// a hardline approval answered allowed_session is recorded as allowed_once and
// leaves no session grant.
func TestHardlineAllowSessionBecomesOnceNoGrant(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "trust"}, false)
	// A tool/call event for the same call id, so a session grant would be
	// visible to Grants if the service wrote one.
	if _, err := s.store.Append(context.Background(), s.session.ID, []core.EventDraft{
		{Kind: core.KindToolCall, Payload: core.ToolCallPayload{V: 1, CallID: "c", Name: "bash", Input: json.RawMessage(`{"command":"sudo ls"}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	s.hooks.ApprovalRequest.SetDefault(func(context.Context, core.ApprovalRequest) (core.ApprovalOutcome, error) {
		return core.OutcomeAllowedSession, nil
	})
	service := NewApprovalService(s.store, s.hooks, s.config)
	engine := homeEngine(t, s, service)

	got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"sudo ls"}`), nil)
	if !got.Allowed || got.Outcome != core.OutcomeAllowedOnce {
		t.Fatalf("decision=%+v", got)
	}

	events, err := s.store.ChainEvents(context.Background(), s.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var decided []core.ApprovalDecided
	for _, event := range events {
		if event.Kind == core.KindApprovalDecided {
			var p core.ApprovalDecided
			if err := json.Unmarshal(event.Payload, &p); err != nil {
				t.Fatal(err)
			}
			decided = append(decided, p)
		}
	}
	if len(decided) != 1 || decided[0].Outcome != core.OutcomeAllowedOnce {
		t.Fatalf("decided=%+v, want allowed_once", decided)
	}
	if grants := Grants(events); len(grants) != 0 {
		t.Fatalf("hardline left a session grant: %+v", grants)
	}
}

// TestHardlineWorkflowRejectsWithoutApprover covers AC-HLF-018 and AC-HLF-019:
// a workflow session has no one to confirm, so the call is rejected, the
// approver is never called, and a permission/hardline event is appended.
func TestHardlineWorkflowRejectsWithoutApprover(t *testing.T) {
	store := memstore.New()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "coder", Channel: "workflow", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	config := core.Config{Agents: map[string]core.AgentConfig{"coder": {Permission: "trust"}}}
	approval := &fakeApproval{outcome: core.OutcomeAllowedVerified}
	home := t.TempDir()
	engine := &Engine{Store: store, Config: config, Approval: approval, Home: func() string { return home }}

	got, _ := engine.Handle(context.Background(), callInput(info.ID, "bash", `{"command":"rm -rf ~"}`), nil)
	if got.Allowed || got.Reason != hardlineWant("HL-B-001", reasonB001) {
		t.Fatalf("workflow hardline: %+v", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatalf("workflow hardline called the approver: %d", approval.calls.Load())
	}

	events, err := store.ChainEvents(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Kind != core.KindPermissionHardline {
			continue
		}
		var p core.PermissionHardline
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			t.Fatal(err)
		}
		found = true
		if p.Rule != "HL-B-001" || p.Tier != core.HardlineBlock || p.Outcome != "rejected" || p.CallID != "c" || p.Tool != "bash" || p.Reason != reasonB001 {
			t.Fatalf("permission/hardline payload=%+v", p)
		}
	}
	if !found {
		t.Fatal("no permission/hardline event appended")
	}
}

// TestHardlineDenyWins: a deny match rejects before the hardline step.
func TestHardlineDenyWins(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "trust", Rules: core.AgentRules{Deny: []string{"bash(rm *)"}}}, false)
	approval := &fakeApproval{outcome: core.OutcomeAllowedVerified}
	engine := homeEngine(t, s, approval)
	got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"rm -rf ~"}`), nil)
	if got.Allowed || !strings.Contains(got.Reason, "deny rule") {
		t.Fatalf("deny did not win: %+v", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatalf("approval calls=%d, want 0", approval.calls.Load())
	}
}

// TestHardlineNormalCallUnaffected: a normal call under trust is allowed
// without calling the approver.
func TestHardlineNormalCallUnaffected(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "trust"}, false)
	approval := &fakeApproval{outcome: core.OutcomeRejected}
	engine := homeEngine(t, s, approval)
	got, _ := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"ls"}`), nil)
	if !got.Allowed {
		t.Fatalf("normal call under trust rejected: %+v", got)
	}
	if approval.calls.Load() != 0 {
		t.Fatalf("approval calls=%d, want 0", approval.calls.Load())
	}
}
