package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// fakeWorkflowService records the calls of the workflow tool and returns the
// configured results.
type fakeWorkflowService struct {
	overview   workflow.Overview
	detail     workflow.Detail
	runs       []workflow.Run
	runDetail  workflow.RunDetail
	started    workflow.Run
	cancelled  workflow.Run
	err        error
	workflowID map[string]bool

	getID        string
	runsID       string
	runsLimit    int
	runsBefore   int
	detailID     string
	detailRef    string
	startCalled  bool
	startID      string
	startTrigger string
	startInput   json.RawMessage
	cancelCalled bool
	cancelID     string
	cancelRef    string
}

var _ WorkflowService = (*fakeWorkflowService)(nil)

func (f *fakeWorkflowService) Overview(context.Context) (workflow.Overview, error) {
	return f.overview, f.err
}

func (f *fakeWorkflowService) Get(_ context.Context, id string) (workflow.Detail, error) {
	f.getID = id
	return f.detail, f.err
}

func (f *fakeWorkflowService) Runs(_ context.Context, id string, limit, before int) ([]workflow.Run, error) {
	f.runsID = id
	f.runsLimit = limit
	f.runsBefore = before
	return f.runs, f.err
}

func (f *fakeWorkflowService) RunDetail(_ context.Context, id, ref string) (workflow.RunDetail, error) {
	f.detailID = id
	f.detailRef = ref
	return f.runDetail, f.err
}

func (f *fakeWorkflowService) StartRun(_ context.Context, id, trigger string, input json.RawMessage) (workflow.Run, error) {
	f.startCalled = true
	f.startID = id
	f.startTrigger = trigger
	f.startInput = append(json.RawMessage(nil), input...)
	return f.started, f.err
}

func (f *fakeWorkflowService) Cancel(_ context.Context, id, ref string) (workflow.Run, error) {
	f.cancelCalled = true
	f.cancelID = id
	f.cancelRef = ref
	return f.cancelled, f.err
}

func (f *fakeWorkflowService) IsWorkflowSession(sessionID string) bool {
	return f.workflowID[sessionID]
}

func callWorkflowTool(t *testing.T, tool core.Tool, tc core.ToolCtx, input string) core.ToolOutput {
	t.Helper()
	out, err := tool.Call(context.Background(), tc, json.RawMessage(input))
	if err != nil {
		t.Fatalf("call %s: %v", input, err)
	}
	return out
}

func decodeWorkflowOutput[T any](t *testing.T, out core.ToolOutput) T {
	t.Helper()
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status = %s, output = %s", out.Status, out.Output)
	}
	var v T
	if err := json.Unmarshal([]byte(out.Output), &v); err != nil {
		t.Fatalf("decode %q: %v", out.Output, err)
	}
	return v
}

func TestWorkflowToolList(t *testing.T) {
	svc := &fakeWorkflowService{overview: workflow.Overview{
		Workflows: []workflow.Summary{{
			ID:          "daily",
			Name:        "Daily research",
			Description: "Research the news.",
			Status:      workflow.StatusActive,
			Version:     3,
			LastRun: &workflow.Run{
				ID: "run-7", Number: 7, Status: workflow.RunSucceeded,
				Trigger: "schedule", StartedAt: 1_700_000_000_000, EndedAt: 1_700_000_005_000,
			},
		}},
		Attention: []workflow.Attention{{
			Decision: workflow.Decision{
				ID: "dec-1", WorkflowID: "daily", RunID: "run-8", RunNumber: 8,
				NodeID: "approve", Kind: "approval", Action: "ask", Reason: "needs a yes",
				Status: "pending",
			},
			WorkflowName: "Daily research",
			NodeName:     "Approve",
			Tool:         "bash",
			Argument:     "rm -rf build",
		}},
	}}
	out := callWorkflowTool(t, NewWorkflow(svc), core.ToolCtx{}, `{"action":"list"}`)
	got := decodeWorkflowOutput[workflowListView](t, out)
	if len(got.Workflows) != 1 {
		t.Fatalf("workflows = %+v", got.Workflows)
	}
	item := got.Workflows[0]
	if item.ID != "daily" || item.Name != "Daily research" || item.Description != "Research the news." {
		t.Fatalf("workflow row = %+v", item)
	}
	if item.Status != "active" || !item.Active {
		t.Fatalf("status = %q active = %v", item.Status, item.Active)
	}
	if item.LastRun == nil || item.LastRun.Number != 7 || item.LastRun.DurationMS != 5000 || item.LastRun.Status != "succeeded" {
		t.Fatalf("last run = %+v", item.LastRun)
	}
	if len(got.Attention) != 1 {
		t.Fatalf("attention = %+v", got.Attention)
	}
	a := got.Attention[0]
	if a.WorkflowName != "Daily research" || a.Node != "Approve" || a.Kind != "approval" || a.Tool != "bash" {
		t.Fatalf("attention row = %+v", a)
	}
}

func TestWorkflowToolGet(t *testing.T) {
	svc := &fakeWorkflowService{detail: workflow.Detail{
		Workflow: workflow.Workflow{
			ID: "daily", Name: "Daily research", Description: "Research the news.",
			Status: workflow.StatusActive, Version: 4, Guardian: "researcher",
		},
		Graph: workflow.Graph{Nodes: []workflow.Node{
			{
				ID: "schedule", Type: "trigger.schedule", Name: "Every morning",
				Params: map[string]any{"cron": "0 9 * * *", "secret": strings.Repeat("s", 3000)},
			},
			{
				ID: "research", Type: "agent", Name: "Research",
				Params: map[string]any{"prompt": strings.Repeat("p", 3000)},
			},
		}},
	}}
	out := callWorkflowTool(t, NewWorkflow(svc), core.ToolCtx{}, `{"action":"get","id":"daily"}`)
	got := decodeWorkflowOutput[workflowDetailView](t, out)
	if svc.getID != "daily" {
		t.Fatalf("Get id = %q", svc.getID)
	}
	if got.ID != "daily" || got.Name != "Daily research" || got.Description != "Research the news." {
		t.Fatalf("detail = %+v", got)
	}
	if got.Status != "active" || got.Version != 4 || got.Guardian != "researcher" {
		t.Fatalf("status/version/guardian = %+v", got)
	}
	if len(got.Triggers) != 1 || got.Triggers[0].Type != "trigger.schedule" || got.Triggers[0].Name != "Every morning" || !got.Triggers[0].Enabled {
		t.Fatalf("triggers = %+v", got.Triggers)
	}
	if got.Triggers[0].Detail != workflow.DescribeCron("0 9 * * *") {
		t.Fatalf("trigger detail = %q", got.Triggers[0].Detail)
	}
	if len(got.Nodes) != 2 || got.Nodes[0].ID != "schedule" || got.Nodes[1].Type != "agent" || got.Nodes[1].Name != "Research" {
		t.Fatalf("nodes = %+v", got.Nodes)
	}
	if strings.Contains(out.Output, "secret") || strings.Contains(out.Output, "prompt") || strings.Contains(out.Output, strings.Repeat("p", 20)) {
		t.Fatalf("get dumped node params: %s", out.Output)
	}
}

func TestWorkflowToolRuns(t *testing.T) {
	svc := &fakeWorkflowService{runs: []workflow.Run{
		{ID: "run-7", Number: 7, Status: workflow.RunSucceeded, Trigger: "schedule", StartedAt: 1000, EndedAt: 4000},
		{ID: "run-8", Number: 8, Status: workflow.RunFailed, Trigger: "agent", StartedAt: 5000, EndedAt: 9000, Error: "boom"},
	}}
	tool := NewWorkflow(svc)
	out := callWorkflowTool(t, tool, core.ToolCtx{}, `{"action":"runs","id":"daily"}`)
	got := decodeWorkflowOutput[[]workflowRunView](t, out)
	if svc.runsID != "daily" || svc.runsLimit != workflowRunLimitDefault || svc.runsBefore != 0 {
		t.Fatalf("Runs args = %q %d %d", svc.runsID, svc.runsLimit, svc.runsBefore)
	}
	if len(got) != 2 {
		t.Fatalf("runs = %+v", got)
	}
	if got[0].Number != 7 || got[0].Status != "succeeded" || got[0].Trigger != "schedule" || got[0].DurationMS != 3000 {
		t.Fatalf("run 7 = %+v", got[0])
	}
	if got[1].ID != "run-8" || got[1].Error != "boom" {
		t.Fatalf("run 8 = %+v", got[1])
	}

	out = callWorkflowTool(t, tool, core.ToolCtx{}, `{"action":"runs","id":"daily","limit":100}`)
	decodeWorkflowOutput[[]workflowRunView](t, out)
	if svc.runsLimit != workflowRunLimitMax {
		t.Fatalf("limit 100 -> %d, want %d", svc.runsLimit, workflowRunLimitMax)
	}

	out = callWorkflowTool(t, tool, core.ToolCtx{}, `{"action":"runs","id":"daily","limit":2}`)
	decodeWorkflowOutput[[]workflowRunView](t, out)
	if svc.runsLimit != 2 {
		t.Fatalf("limit 2 -> %d", svc.runsLimit)
	}
}

func TestWorkflowToolRun(t *testing.T) {
	svc := &fakeWorkflowService{runDetail: workflow.RunDetail{
		Run: workflow.Run{
			ID: "run-7", Number: 7, Status: workflow.RunSucceeded,
			Trigger: "schedule", StartedAt: 1000, EndedAt: 4000,
		},
		Graph: workflow.Graph{Nodes: []workflow.Node{
			{ID: "research", Type: "agent", Name: "Research"},
			{ID: "publish", Type: "message", Name: "Publish"},
		}},
		Nodes: []workflow.NodeRun{
			{
				NodeID: "research", Status: workflow.NodeSucceeded,
				StartedAt: 1000, EndedAt: 2500, Output: json.RawMessage(`"short"`),
			},
			{
				NodeID: "publish", Status: workflow.NodeFailed,
				StartedAt: 2600, EndedAt: 3900, Error: "no chat",
				Output: json.RawMessage(`{"ok":false}`),
			},
		},
	}}
	out := callWorkflowTool(t, NewWorkflow(svc), core.ToolCtx{}, `{"action":"run","id":"daily","ref":"7"}`)
	got := decodeWorkflowOutput[workflowRunDetailView](t, out)
	if svc.detailID != "daily" || svc.detailRef != "7" {
		t.Fatalf("RunDetail args = %q %q", svc.detailID, svc.detailRef)
	}
	if got.Number != 7 || got.Status != "succeeded" || got.Trigger != "schedule" || got.DurationMS != 3000 {
		t.Fatalf("run = %+v", got)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes = %+v", got.Nodes)
	}
	if got.Nodes[0].Node != "Research" || got.Nodes[0].ID != "research" || got.Nodes[0].Status != "succeeded" || got.Nodes[0].DurationMS != 1500 || got.Nodes[0].Output != "short" {
		t.Fatalf("step 0 = %+v", got.Nodes[0])
	}
	if got.Nodes[1].Node != "Publish" || got.Nodes[1].Status != "failed" || got.Nodes[1].Error != "no chat" || got.Nodes[1].Output != `{"ok":false}` {
		t.Fatalf("step 1 = %+v", got.Nodes[1])
	}
}

func TestWorkflowToolStart(t *testing.T) {
	svc := &fakeWorkflowService{started: workflow.Run{
		ID: "run-9", Number: 9, Status: workflow.RunQueued, Trigger: "agent", StartedAt: 1000,
	}}
	out := callWorkflowTool(t, NewWorkflow(svc), core.ToolCtx{SessionID: "s1"}, `{"action":"start","id":"daily","input":{"topic":"news"}}`)
	got := decodeWorkflowOutput[workflowRunView](t, out)
	if !svc.startCalled || svc.startID != "daily" {
		t.Fatalf("StartRun called = %v id = %q", svc.startCalled, svc.startID)
	}
	if svc.startTrigger != "agent" {
		t.Fatalf("trigger = %q", svc.startTrigger)
	}
	if string(svc.startInput) != `{"topic":"news"}` {
		t.Fatalf("input = %s", svc.startInput)
	}
	if got.Number != 9 || got.Status != "queued" {
		t.Fatalf("started run = %+v", got)
	}
}

func TestWorkflowToolStartRefusedFromWorkflowSession(t *testing.T) {
	svc := &fakeWorkflowService{workflowID: map[string]bool{"wf-session": true}}
	out := callWorkflowTool(t, NewWorkflow(svc), core.ToolCtx{SessionID: "wf-session"}, `{"action":"start","id":"daily"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "cannot start another workflow") {
		t.Fatalf("start from workflow session = %+v", out)
	}
	if svc.startCalled {
		t.Fatal("StartRun was called from a workflow session")
	}
}

func TestWorkflowToolCancel(t *testing.T) {
	svc := &fakeWorkflowService{cancelled: workflow.Run{
		ID: "run-9", Number: 9, Status: workflow.RunCancelled,
		Trigger: "agent", StartedAt: 1000, EndedAt: 2000,
	}}
	out := callWorkflowTool(t, NewWorkflow(svc), core.ToolCtx{}, `{"action":"cancel","id":"daily","ref":"9"}`)
	got := decodeWorkflowOutput[workflowRunView](t, out)
	if !svc.cancelCalled || svc.cancelID != "daily" || svc.cancelRef != "9" {
		t.Fatalf("Cancel args = %v %q %q", svc.cancelCalled, svc.cancelID, svc.cancelRef)
	}
	if got.Status != "cancelled" || got.DurationMS != 1000 {
		t.Fatalf("cancelled run = %+v", got)
	}
}

func TestWorkflowToolMissingIDAndUnknownAction(t *testing.T) {
	svc := &fakeWorkflowService{}
	tool := NewWorkflow(svc)
	for _, input := range []string{
		`{"action":"get"}`,
		`{"action":"runs"}`,
		`{"action":"run","ref":"1"}`,
		`{"action":"start"}`,
		`{"action":"cancel","ref":"1"}`,
	} {
		out := callWorkflowTool(t, tool, core.ToolCtx{}, input)
		if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "id is required") {
			t.Fatalf("%s = %+v", input, out)
		}
	}
	out := callWorkflowTool(t, tool, core.ToolCtx{}, `{"action":"run","id":"daily"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "ref is required") {
		t.Fatalf("run without ref = %+v", out)
	}
	out = callWorkflowTool(t, tool, core.ToolCtx{}, `{"action":"delete","id":"daily"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, `unknown action "delete"`) {
		t.Fatalf("unknown action = %+v", out)
	}
	out = callWorkflowTool(t, tool, core.ToolCtx{}, `{}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "action is required") {
		t.Fatalf("missing action = %+v", out)
	}
}

func TestWorkflowToolServiceError(t *testing.T) {
	svc := &fakeWorkflowService{err: errors.New("workflow: not found")}
	out := callWorkflowTool(t, NewWorkflow(svc), core.ToolCtx{}, `{"action":"get","id":"missing"}`)
	if out.Status != core.ToolStatusError || out.Output != "workflow: not found" {
		t.Fatalf("service error = %+v", out)
	}
}

func TestWorkflowToolRunTruncatesOutput(t *testing.T) {
	raw, err := json.Marshal(strings.Repeat("a", workflowOutputLimit+500))
	if err != nil {
		t.Fatal(err)
	}
	svc := &fakeWorkflowService{runDetail: workflow.RunDetail{
		Run:   workflow.Run{ID: "run-1", Number: 1, Status: workflow.RunSucceeded},
		Graph: workflow.Graph{Nodes: []workflow.Node{{ID: "node", Type: "bash", Name: "Node"}}},
		Nodes: []workflow.NodeRun{{
			NodeID: "node", Status: workflow.NodeSucceeded, Output: raw,
		}},
	}}
	out := callWorkflowTool(t, NewWorkflow(svc), core.ToolCtx{}, `{"action":"run","id":"daily","ref":"1"}`)
	got := decodeWorkflowOutput[workflowRunDetailView](t, out)
	if len(got.Nodes) != 1 {
		t.Fatalf("nodes = %+v", got.Nodes)
	}
	text := got.Nodes[0].Output
	if utf8.RuneCountInString(text) != workflowOutputLimit {
		t.Fatalf("output has %d characters, want %d", utf8.RuneCountInString(text), workflowOutputLimit)
	}
	if !strings.Contains(text, "[truncated]") || !strings.HasPrefix(text, strings.Repeat("a", 100)) {
		t.Fatalf("truncated output = %q", text[:80])
	}
}

// editorWorkflowService is the fake plus create and assist.
type editorWorkflowService struct {
	*fakeWorkflowService
	createIn    workflow.CreateInput
	assistID    string
	assistMsg   string
	assistBase  int
	assistReply workflow.AssistResult
}

func (f *editorWorkflowService) Create(_ context.Context, in workflow.CreateInput) (workflow.Detail, error) {
	f.createIn = in
	return workflow.Detail{Workflow: workflow.Workflow{ID: "daily-tickets", Name: in.Name, Version: 1, Status: workflow.StatusDraft}}, nil
}

func (f *editorWorkflowService) Assist(_ context.Context, id string, base int, msg string) (workflow.AssistResult, error) {
	f.assistID, f.assistBase, f.assistMsg = id, base, msg
	return f.assistReply, nil
}

func TestWorkflowToolCreateAsksTheGuardian(t *testing.T) {
	svc := &editorWorkflowService{fakeWorkflowService: &fakeWorkflowService{}}
	svc.assistReply = workflow.AssistResult{
		Message:  workflow.ChatMessage{Text: "Sudah kubuat", Change: &workflow.GraphChange{Added: []string{"Setiap jam 8"}}},
		Workflow: workflow.Detail{Workflow: workflow.Workflow{ID: "daily-tickets", Name: "Daily tickets", Version: 2, Status: workflow.StatusDraft}},
	}
	tool := NewWorkflow(svc)
	out, err := tool.Call(context.Background(), core.ToolCtx{AgentName: "karin"}, json.RawMessage(`{"action":"create","name":"Daily tickets","request":"tiap jam 8 kirim ticket terbuka"}`))
	if err != nil || out.Status != core.ToolStatusSuccess {
		t.Fatalf("create: %v %+v", err, out)
	}
	if svc.createIn.Guardian != "karin" || svc.assistID != "daily-tickets" || svc.assistBase != 1 || svc.assistMsg != "tiap jam 8 kirim ticket terbuka" {
		t.Fatalf("create %+v assist %s/%d/%q", svc.createIn, svc.assistID, svc.assistBase, svc.assistMsg)
	}
	for _, want := range []string{`"link": "#/workflows/daily-tickets"`, `"guardian_reply": "Sudah kubuat"`, "Draft:"} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("output misses %s: %s", want, out.Output)
		}
	}
	if risk := tool.Risk(json.RawMessage(`{"action":"create"}`)); risk != core.RiskWrite {
		t.Fatalf("create risk = %s, want write", risk)
	}
	if risk := tool.Risk(json.RawMessage(`{"action":"test"}`)); risk != core.RiskWrite {
		t.Fatalf("test risk = %s, want write", risk)
	}
	if risk := tool.Risk(json.RawMessage(`{"action":"start"}`)); risk != core.RiskExec {
		t.Fatalf("start risk = %s, want exec", risk)
	}
}

func TestWorkflowToolTestWaitsForTheRun(t *testing.T) {
	svc := &fakeWorkflowService{
		started:   workflow.Run{ID: "r1", Number: 3, Status: workflow.RunQueued},
		runDetail: workflow.RunDetail{Run: workflow.Run{ID: "r1", Number: 3, Status: workflow.RunSucceeded}, Nodes: []workflow.NodeRun{{NodeID: "fetch", Status: "succeeded", Output: json.RawMessage(`{"text":"3 tickets"}`)}}},
	}
	out, err := NewWorkflow(svc).Call(context.Background(), core.ToolCtx{}, json.RawMessage(`{"action":"test","id":"daily-tickets"}`))
	if err != nil || out.Status != core.ToolStatusSuccess {
		t.Fatalf("test: %v %+v", err, out)
	}
	if !svc.startCalled || !strings.Contains(out.Output, `"status": "succeeded"`) || !strings.Contains(out.Output, "3 tickets") {
		t.Fatalf("output = %s", out.Output)
	}
}

// guardianChatService is the editor fake whose session chat-1 is the
// guardian chat of daily-tickets.
type guardianChatService struct{ *editorWorkflowService }

func (f *guardianChatService) AssistantWorkflow(_ context.Context, sessionID string) (workflow.Workflow, bool) {
	if sessionID == "chat-1" {
		return workflow.Workflow{ID: "daily-tickets"}, true
	}
	return workflow.Workflow{}, false
}

// Spec 51: edit from a workflow's own guardian chat would start a second
// turn on the calling session and fail as busy, so it points the guardian at
// workflow_builder instead.
func TestWorkflowToolEditFromOwnGuardianChatPointsToBuilder(t *testing.T) {
	svc := &guardianChatService{&editorWorkflowService{fakeWorkflowService: &fakeWorkflowService{
		detail: workflow.Detail{Workflow: workflow.Workflow{ID: "daily-tickets", Version: 7}},
	}}}
	tool := NewWorkflow(svc)
	in := json.RawMessage(`{"action":"edit","id":"daily-tickets","request":"fix grouping"}`)
	out, err := tool.Call(context.Background(), core.ToolCtx{SessionID: "chat-1"}, in)
	if err != nil || out.Status != core.ToolStatusError || !strings.Contains(out.Output, "workflow_builder") {
		t.Fatalf("own chat edit: %v %+v", err, out)
	}
	if svc.assistID != "" {
		t.Fatalf("assist ran from the guardian chat: %q", svc.assistID)
	}
	out, err = tool.Call(context.Background(), core.ToolCtx{SessionID: "web-1"}, in)
	if err != nil || out.Status != core.ToolStatusSuccess || svc.assistID != "daily-tickets" {
		t.Fatalf("main chat edit: %v %+v assist=%q", err, out, svc.assistID)
	}
}
