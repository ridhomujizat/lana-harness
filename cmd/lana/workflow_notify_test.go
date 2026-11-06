package main

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/channels/telegram"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// REQ-WF-009: a channel "workflow" approval goes to the web responder and, in
// parallel, fires the Telegram heads-up off the approval path.
func TestApprovalRouterWorkflowChannel(t *testing.T) {
	var noticed atomic.Int32
	noticedCh := make(chan struct{}, 1)
	router := &ApprovalRouter{
		sessionInfo: func(_ context.Context, sessionID string) (core.SessionInfo, error) {
			return core.SessionInfo{ID: sessionID, Channel: "workflow", Address: "wf1"}, nil
		},
		web: func(_ context.Context, req core.ApprovalRequest) core.ApprovalOutcome {
			if req.SessionID != "wf-session" {
				t.Errorf("web route got session %q", req.SessionID)
			}
			return core.OutcomeAllowedOnce
		},
		workflowNotice: func(_ context.Context, _ core.ApprovalRequest, _ core.SessionInfo) {
			noticed.Add(1)
			noticedCh <- struct{}{}
		},
	}

	got := router.Approve(context.Background(), core.ApprovalRequest{SessionID: "wf-session"})
	if got != core.OutcomeAllowedOnce {
		t.Fatalf("outcome = %q, want allowed_once from the web route", got)
	}
	select {
	case <-noticedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("workflowNotice was not called")
	}
	if noticed.Load() != 1 {
		t.Fatalf("workflowNotice calls = %d, want 1", noticed.Load())
	}
}

// REQ-WF-009: a workflow approval without a web responder fails closed even
// though the heads-up still fires.
func TestApprovalRouterWorkflowWithoutWeb(t *testing.T) {
	router := &ApprovalRouter{
		sessionInfo: func(_ context.Context, sessionID string) (core.SessionInfo, error) {
			return core.SessionInfo{ID: sessionID, Channel: "workflow"}, nil
		},
	}
	if got := router.Approve(context.Background(), core.ApprovalRequest{SessionID: "s"}); got != core.OutcomeUnavailable {
		t.Fatalf("outcome = %q, want unavailable", got)
	}
}

// REQ-WF-005, REQ-WF-008: the decision buttons carry the labels and callback
// data the channel expects, two per row.
func TestWorkflowButtons(t *testing.T) {
	rows := workflowButtons(workflow.Notice{
		DecisionID: "d1",
		Choices:    []string{"apply", "run_only", "skip", "stop"},
	})
	want := [][]telegram.Button{
		{{Text: "Apply and resume", Data: "w:d1:apply"}, {Text: "This run only", Data: "w:d1:run_only"}},
		{{Text: "Skip", Data: "w:d1:skip"}, {Text: "Stop run", Data: "w:d1:stop"}},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("buttons =\n%v\nwant\n%v", rows, want)
	}

	approveRows := workflowButtons(workflow.Notice{DecisionID: "d2", Choices: []string{"approve", "deny"}})
	wantApprove := [][]telegram.Button{
		{{Text: "Approve", Data: "w:d2:approve"}, {Text: "Deny", Data: "w:d2:deny"}},
	}
	if !reflect.DeepEqual(approveRows, wantApprove) {
		t.Fatalf("approve buttons = %v, want %v", approveRows, wantApprove)
	}
}

// REQ-WF-005: a notice without a decision has no keyboard, and an unknown
// choice yields no buttons.
func TestWorkflowButtonsEmpty(t *testing.T) {
	if rows := workflowButtons(workflow.Notice{}); rows != nil {
		t.Fatalf("buttons = %v, want nil without a decision", rows)
	}
	if rows := workflowButtons(workflow.Notice{DecisionID: "d1"}); rows != nil {
		t.Fatalf("buttons = %v, want nil without choices", rows)
	}
	if rows := workflowButtons(workflow.Notice{DecisionID: "d1", Choices: []string{"bogus"}}); rows != nil {
		t.Fatalf("buttons = %v, want nil for an unknown choice", rows)
	}
}

// REQ-WF-005: the notice text joins the non-empty parts, and the heads-up
// argument is capped.
func TestWorkflowNoticeHelpers(t *testing.T) {
	if got := noticeText(workflow.Notice{Title: "T", Text: "B"}); got != "T\n\nB" {
		t.Fatalf("noticeText = %q", got)
	}
	if got := noticeText(workflow.Notice{Text: "B"}); got != "B" {
		t.Fatalf("noticeText = %q", got)
	}

	long := make([]rune, workflowNoticeArgumentMax+50)
	for i := range long {
		long[i] = 'x'
	}
	arg := shortApprovalArgument("bash", []byte(`{"command":"`+string(long)+`"}`))
	if len([]rune(arg)) != workflowNoticeArgumentMax {
		t.Fatalf("argument length = %d, want %d", len([]rune(arg)), workflowNoticeArgumentMax)
	}
}
