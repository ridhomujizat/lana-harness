package permission

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func approvalEvents(t *testing.T, s *testSetup, sessionID string) ([]core.ApprovalAsked, []core.ApprovalDecided) {
	t.Helper()
	events, err := s.store.ChainEvents(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var asked []core.ApprovalAsked
	var decided []core.ApprovalDecided
	for _, event := range events {
		switch event.Kind {
		case core.KindApprovalAsked:
			var payload core.ApprovalAsked
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			asked = append(asked, payload)
		case core.KindApprovalDecided:
			var payload core.ApprovalDecided
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			decided = append(decided, payload)
		}
	}
	return asked, decided
}

// TestChildApprovalLabel covers AC-TEAM-025 (log part): a child approval is
// written to the child's log with the child's agent name as its label.
func TestChildApprovalLabel(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "trust"}, false)
	s.config.Agents["researcher"] = core.AgentConfig{Permission: "ask"}
	child := newChildSession(t, s, "researcher")
	s.hooks.ApprovalRequest.SetDefault(func(context.Context, core.ApprovalRequest) (core.ApprovalOutcome, error) {
		return core.OutcomeAllowedOnce, nil
	})
	service := NewApprovalService(s.store, s.hooks, s.config)
	engine := &Engine{Store: s.store, Config: s.config, Approval: service}

	got, err := engine.Handle(context.Background(), callInputFor(child.ID, "researcher", "bash", `{"command":"echo hi"}`), nil)
	if err != nil || !got.Allowed || got.Outcome != core.OutcomeAllowedOnce {
		t.Fatalf("decision=%+v err=%v", got, err)
	}

	asked, decided := approvalEvents(t, s, child.ID)
	if len(asked) != 1 || len(decided) != 1 {
		t.Fatalf("child asked=%d decided=%d", len(asked), len(decided))
	}
	if asked[0].Label != "researcher" {
		t.Fatalf("child approval label=%q want %q", asked[0].Label, "researcher")
	}
	if asked[0].RequestID != decided[0].RequestID || decided[0].Outcome != core.OutcomeAllowedOnce {
		t.Fatalf("child audit pair asked=%+v decided=%+v", asked[0], decided[0])
	}

	// The answer is written to the child's log only (AC-TEAM-026 log part).
	parentAsked, parentDecided := approvalEvents(t, s, s.session.ID)
	if len(parentAsked) != 0 || len(parentDecided) != 0 {
		t.Fatalf("parent asked=%+v decided=%+v", parentAsked, parentDecided)
	}
}

// TestRootApprovalLabelEmpty covers the root half of AC-TEAM-025: root
// sessions keep an empty label.
func TestRootApprovalLabelEmpty(t *testing.T) {
	s := setup(t, core.AgentConfig{Permission: "ask"}, false)
	s.hooks.ApprovalRequest.SetDefault(func(context.Context, core.ApprovalRequest) (core.ApprovalOutcome, error) {
		return core.OutcomeAllowedOnce, nil
	})
	service := NewApprovalService(s.store, s.hooks, s.config)
	engine := &Engine{Store: s.store, Config: s.config, Approval: service}

	got, err := engine.Handle(context.Background(), callInput(s.session.ID, "bash", `{"command":"echo hi"}`), nil)
	if err != nil || !got.Allowed {
		t.Fatalf("decision=%+v err=%v", got, err)
	}

	asked, decided := approvalEvents(t, s, s.session.ID)
	if len(asked) != 1 || len(decided) != 1 {
		t.Fatalf("asked=%d decided=%d", len(asked), len(decided))
	}
	if asked[0].Label != "" {
		t.Fatalf("root approval label=%q want empty", asked[0].Label)
	}
	events, err := s.store.ChainEvents(context.Background(), s.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == core.KindApprovalAsked && strings.Contains(string(event.Payload), `"label"`) {
			t.Fatalf("root approval/asked carries a label: %s", event.Payload)
		}
	}
}
