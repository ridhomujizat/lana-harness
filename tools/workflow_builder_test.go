package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// fakeBuilderService is a WorkflowBuilderService that records the calls and
// serves canned results.
type fakeBuilderService struct {
	session    string
	saved      *workflow.Graph
	issues     []workflow.Issue
	runStatus  []workflow.RunStatus
	polls      int
	published  string
	publishErr error
	discarded  bool
}

func (f *fakeBuilderService) check(sessionID string) error {
	if sessionID != f.session {
		return workflow.ErrNotAssistantSession
	}
	return nil
}

func (f *fakeBuilderService) BuilderGet(_ context.Context, sessionID string) (workflow.BuilderView, error) {
	if err := f.check(sessionID); err != nil {
		return workflow.BuilderView{}, err
	}
	return workflow.BuilderView{
		Workflow:  workflow.Workflow{ID: "daily", Name: "Daily", Status: workflow.StatusDraft, Version: 2},
		Current:   workflow.Version{Version: 2, Graph: workflow.Graph{Nodes: []workflow.Node{{ID: "start", Type: "trigger.manual"}}}},
		Candidate: &workflow.Candidate{ID: 4, BaseVersion: 2, Summary: "add send"},
	}, nil
}

func (f *fakeBuilderService) BuilderSave(_ context.Context, sessionID string, g workflow.Graph, summary string) (workflow.Candidate, workflow.GraphChange, []workflow.Issue, error) {
	if err := f.check(sessionID); err != nil {
		return workflow.Candidate{}, workflow.GraphChange{}, nil, err
	}
	if len(f.issues) > 0 {
		return workflow.Candidate{}, workflow.GraphChange{}, f.issues, nil
	}
	f.saved = &g
	return workflow.Candidate{ID: 5, BaseVersion: 2, Summary: summary}, workflow.GraphChange{Added: []string{"send"}, Summary: summary}, nil, nil
}

func (f *fakeBuilderService) BuilderTest(_ context.Context, sessionID string, _ json.RawMessage) (workflow.Run, error) {
	if err := f.check(sessionID); err != nil {
		return workflow.Run{}, err
	}
	return workflow.Run{ID: "r_9", Number: 9, CandidateID: 5, Status: workflow.RunQueued}, nil
}

func (f *fakeBuilderService) BuilderRun(_ context.Context, _ string, runID string) (workflow.RunDetail, error) {
	status := f.runStatus[min(f.polls, len(f.runStatus)-1)]
	f.polls++
	detail := workflow.RunDetail{Run: workflow.Run{ID: runID, Number: 9, Trigger: "test", Status: status}}
	if status == workflow.RunFailed {
		detail.Nodes = []workflow.NodeRun{{NodeID: "send", Status: workflow.NodeFailed, Error: "chat not found"}}
	}
	return detail, nil
}

func (f *fakeBuilderService) BuilderPublish(_ context.Context, sessionID, turnID string) (workflow.ChatMessage, error) {
	if err := f.check(sessionID); err != nil {
		return workflow.ChatMessage{}, err
	}
	if f.publishErr != nil {
		return workflow.ChatMessage{}, f.publishErr
	}
	f.published = turnID
	return workflow.ChatMessage{Change: &workflow.GraphChange{FromVersion: 2, ToVersion: 3}}, nil
}

func (f *fakeBuilderService) BuilderDiscard(_ context.Context, sessionID string) error {
	if err := f.check(sessionID); err != nil {
		return err
	}
	f.discarded = true
	return nil
}

func callBuilder(t *testing.T, svc WorkflowBuilderService, sessionID, input string) core.ToolOutput {
	t.Helper()
	out, err := NewWorkflowBuilder(svc).Call(context.Background(), core.ToolCtx{SessionID: sessionID, TurnID: "turn-1"}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("call %s: %v", input, err)
	}
	return out
}

// Spec 51 REQ-WFC-003: the tool works only in the guardian chat session.
func TestWorkflowBuilderRefusesOtherSessions(t *testing.T) {
	svc := &fakeBuilderService{session: "chat"}
	out := callBuilder(t, svc, "other", `{"action":"get"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "editor chat") {
		t.Fatalf("out = %+v", out)
	}
}

func TestWorkflowBuilderGetAndSave(t *testing.T) {
	svc := &fakeBuilderService{session: "chat"}
	out := callBuilder(t, svc, "chat", `{"action":"get"}`)
	if (out.Status == core.ToolStatusError) || !strings.Contains(out.Output, `"trigger.manual"`) || !strings.Contains(out.Output, `"add send"`) {
		t.Fatalf("get = %+v", out)
	}
	out = callBuilder(t, svc, "chat", `{"action":"save","summary":"add send","graph":{"nodes":[{"id":"start","type":"trigger.manual"}],"edges":[]}}`)
	if (out.Status == core.ToolStatusError) || svc.saved == nil || !strings.Contains(out.Output, "Test it now") {
		t.Fatalf("save = %+v", out)
	}
	svc.issues = []workflow.Issue{{Code: "cycle", Message: "a cycle"}}
	out = callBuilder(t, svc, "chat", `{"action":"save","graph":{"nodes":[]}}`)
	if !strings.Contains(out.Output, "Nothing was saved") || !strings.Contains(out.Output, "a cycle") {
		t.Fatalf("save with issues = %+v", out)
	}
	if out := callBuilder(t, svc, "chat", `{"action":"save"}`); out.Status != core.ToolStatusError {
		t.Fatalf("save without graph should fail: %+v", out)
	}
}

// REQ-WFC-004: test waits for the run and reports node errors with a next step.
func TestWorkflowBuilderTestWaits(t *testing.T) {
	prev := workflowBuilderPoll
	workflowBuilderPoll = time.Millisecond
	t.Cleanup(func() { workflowBuilderPoll = prev })

	svc := &fakeBuilderService{session: "chat", runStatus: []workflow.RunStatus{workflow.RunRunning, workflow.RunRunning, workflow.RunFailed}}
	out := callBuilder(t, svc, "chat", `{"action":"test"}`)
	if (out.Status == core.ToolStatusError) || svc.polls != 3 {
		t.Fatalf("test = %+v, polls %d", out, svc.polls)
	}
	for _, want := range []string{`"status": "failed"`, "chat not found", "fix the graph", `"candidate": 5`} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("test output misses %q:\n%s", want, out.Output)
		}
	}
}

func TestWorkflowBuilderPublishAndDiscard(t *testing.T) {
	svc := &fakeBuilderService{session: "chat"}
	out := callBuilder(t, svc, "chat", `{"action":"publish"}`)
	if (out.Status == core.ToolStatusError) || svc.published != "turn-1" || !strings.Contains(out.Output, `"version": 3`) {
		t.Fatalf("publish = %+v (turn %q)", out, svc.published)
	}
	svc.publishErr = workflow.ErrVersionConflict
	out = callBuilder(t, svc, "chat", `{"action":"publish"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "version conflict") {
		t.Fatalf("conflict = %+v", out)
	}
	out = callBuilder(t, svc, "chat", `{"action":"discard"}`)
	if (out.Status == core.ToolStatusError) || !svc.discarded {
		t.Fatalf("discard = %+v", out)
	}
}

func TestWorkflowBuilderRisk(t *testing.T) {
	tool := NewWorkflowBuilder(&fakeBuilderService{})
	if tool.Risk(json.RawMessage(`{"action":"get"}`)) != core.RiskRead {
		t.Fatal("get should be read")
	}
	for _, action := range []string{"save", "test", "publish", "discard"} {
		if tool.Risk(json.RawMessage(`{"action":"`+action+`"}`)) != core.RiskWrite {
			t.Fatalf("%s should be write", action)
		}
	}
}
