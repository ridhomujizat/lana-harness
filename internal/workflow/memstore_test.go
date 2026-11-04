package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestMemStoreWorkflowRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()
	g1 := Graph{Nodes: []Node{{ID: "start", Type: "trigger.manual", Name: "Start", Params: map[string]any{"enabled": true}}}}

	w, err := st.CreateWorkflow(ctx, Workflow{ID: "a", Name: "Alpha", Guardian: "g", Status: StatusDraft, CreatedAt: 100}, g1, "owner", "first")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.Version != 1 {
		t.Fatalf("created version=%d want 1", w.Version)
	}

	if _, err := st.CreateWorkflow(ctx, Workflow{ID: "a", Name: "Dup"}, g1, "owner", "dup"); !errors.Is(err, ErrExists) {
		t.Fatalf("second create err=%v want ErrExists", err)
	}

	got, err := st.GetWorkflow(ctx, "a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 1 || got.CreatedAt != 100 || got.Name != "Alpha" {
		t.Fatalf("got=%+v", got)
	}

	v1, err := st.GetVersion(ctx, "a", 1)
	if err != nil || v1.Author != "owner" || v1.Summary != "first" || len(v1.Graph.Nodes) != 1 {
		t.Fatalf("v1=%+v err=%v", v1, err)
	}

	g2 := Graph{Nodes: []Node{
		{ID: "start", Type: "trigger.manual", Name: "Start"},
		{ID: "say", Type: "message", Name: "Say", Params: map[string]any{"text": "hi"}},
	}}
	v2, err := st.SaveVersion(ctx, "a", 1, g2, "guardian", "second")
	if err != nil || v2.Version != 2 {
		t.Fatalf("save v2=%+v err=%v", v2, err)
	}
	if _, err := st.SaveVersion(ctx, "a", 1, g2, "guardian", "stale"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale save err=%v want ErrVersionConflict", err)
	}

	got, _ = st.GetWorkflow(ctx, "a")
	if got.Version != 2 || got.UpdatedAt == 0 {
		t.Fatalf("after save workflow=%+v", got)
	}

	versions, err := st.ListVersions(ctx, "a")
	if err != nil || len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	if _, err := st.GetVersion(ctx, "a", 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing version err=%v want ErrNotFound", err)
	}

	// UpdateWorkflow keeps the stored version even when the caller sends another.
	upd := got
	upd.Name = "Alpha renamed"
	upd.Version = 99
	if err := st.UpdateWorkflow(ctx, upd); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = st.GetWorkflow(ctx, "a")
	if got.Version != 2 || got.Name != "Alpha renamed" {
		t.Fatalf("after update workflow=%+v", got)
	}
	if err := st.UpdateWorkflow(ctx, Workflow{ID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing err=%v want ErrNotFound", err)
	}

	if _, err := st.CreateWorkflow(ctx, Workflow{ID: "b", Name: "Beta"}, Graph{}, "owner", "s"); err != nil {
		t.Fatalf("create b: %v", err)
	}
	list, err := st.ListWorkflows(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "Alpha renamed" || list[1].ID != "b" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
}

func TestMemStoreRunNumberingAndFilters(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()
	if _, err := st.CreateWorkflow(ctx, Workflow{ID: "a", Name: "Alpha"}, Graph{}, "owner", "s"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateWorkflow(ctx, Workflow{ID: "b", Name: "Beta"}, Graph{}, "owner", "s"); err != nil {
		t.Fatal(err)
	}

	var ids []string
	specs := []struct {
		at     int64
		status RunStatus
	}{
		{100, RunSucceeded}, {200, RunFailed}, {300, RunRunning},
	}
	for i, spec := range specs {
		r, err := st.CreateRun(ctx, Run{WorkflowID: "a", StartedAt: spec.at, Status: spec.status})
		if err != nil {
			t.Fatalf("create run %d: %v", i+1, err)
		}
		if r.Number != i+1 {
			t.Fatalf("run %d number=%d want %d", i+1, r.Number, i+1)
		}
		if len(r.ID) != len("r_")+16 || r.ID[:2] != "r_" {
			t.Fatalf("run id=%q", r.ID)
		}
		ids = append(ids, r.ID)
	}

	other, err := st.CreateRun(ctx, Run{WorkflowID: "b", ID: "b1", StartedAt: 250, Status: RunRunning})
	if err != nil {
		t.Fatal(err)
	}
	if other.Number != 1 || other.ID != "b1" {
		t.Fatalf("other run=%+v", other)
	}

	if r, err := st.GetRunByNumber(ctx, "a", 2); err != nil || r.ID != ids[1] {
		t.Fatalf("by number r=%+v err=%v", r, err)
	}
	if _, err := st.GetRun(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing run err=%v", err)
	}
	if _, err := st.GetRunByNumber(ctx, "a", 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing number err=%v", err)
	}

	runs, err := st.ListRuns(ctx, RunFilter{WorkflowID: "a"})
	if err != nil || len(runs) != 3 || runs[0].Number != 3 || runs[1].Number != 2 || runs[2].Number != 1 {
		t.Fatalf("list a=%+v err=%v", runs, err)
	}

	runs, _ = st.ListRuns(ctx, RunFilter{WorkflowID: "a", Statuses: []RunStatus{RunFailed}})
	if len(runs) != 1 || runs[0].Number != 2 {
		t.Fatalf("status filter=%+v", runs)
	}

	runs, _ = st.ListRuns(ctx, RunFilter{WorkflowID: "a", Since: 200})
	if len(runs) != 2 || runs[0].Number != 3 || runs[1].Number != 2 {
		t.Fatalf("since filter=%+v", runs)
	}

	runs, _ = st.ListRuns(ctx, RunFilter{WorkflowID: "a", Before: 3})
	if len(runs) != 2 || runs[0].Number != 2 || runs[1].Number != 1 {
		t.Fatalf("before filter=%+v", runs)
	}

	runs, _ = st.ListRuns(ctx, RunFilter{WorkflowID: "a", Limit: 2})
	if len(runs) != 2 || runs[0].Number != 3 || runs[1].Number != 2 {
		t.Fatalf("limit filter=%+v", runs)
	}

	runs, _ = st.ListRuns(ctx, RunFilter{})
	if len(runs) != 4 || runs[0].Number != 3 || runs[1].ID != "b1" || runs[2].Number != 2 || runs[3].Number != 1 {
		t.Fatalf("cross-workflow list=%+v", runs)
	}

	got, _ := st.GetRun(ctx, ids[0])
	got.Status = RunSucceeded
	if err := st.UpdateRun(ctx, got); err != nil {
		t.Fatalf("update run: %v", err)
	}
	again, _ := st.GetRun(ctx, ids[0])
	if again.Status != RunSucceeded {
		t.Fatalf("updated run=%+v", again)
	}
	if err := st.UpdateRun(ctx, Run{ID: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing run err=%v", err)
	}
}

func TestMemStoreCascadeDelete(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()
	g := Graph{Nodes: []Node{{ID: "n", Type: "state_set", Name: "N", Params: map[string]any{"key": "k", "value": 1}}}}
	if _, err := st.CreateWorkflow(ctx, Workflow{ID: "a", Name: "Alpha"}, g, "owner", "s"); err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateRun(ctx, Run{WorkflowID: "a", Status: RunRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutNodeRun(ctx, NodeRun{RunID: run.ID, NodeID: "n", ItemIndex: -1, Status: NodeSucceeded}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutDecision(ctx, Decision{ID: "d1", WorkflowID: "a", RunID: run.ID, CreatedAt: 5}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(ctx, "a", "k", json.RawMessage(`1`)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendChat(ctx, ChatMessage{WorkflowID: "a", Role: "owner", Text: "hi"}); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteWorkflow(ctx, "a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetWorkflow(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("workflow survived: %v", err)
	}
	if versions, _ := st.ListVersions(ctx, "a"); len(versions) != 0 {
		t.Fatalf("versions survived: %+v", versions)
	}
	if _, err := st.GetRun(ctx, run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("run survived: %v", err)
	}
	if nrs, _ := st.ListNodeRuns(ctx, run.ID); len(nrs) != 0 {
		t.Fatalf("node runs survived: %+v", nrs)
	}
	if _, err := st.GetDecision(ctx, "d1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decision survived: %v", err)
	}
	if entries, _ := st.ListState(ctx, "a"); len(entries) != 0 {
		t.Fatalf("state survived: %+v", entries)
	}
	if msgs, _ := st.ListChat(ctx, "a", 0); len(msgs) != 0 {
		t.Fatalf("chat survived: %+v", msgs)
	}
	if err := st.DeleteWorkflow(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete err=%v want ErrNotFound", err)
	}
}

func TestMemStoreCopiesAreIndependent(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()
	g := Graph{Nodes: []Node{{
		ID: "n", Type: "worker", Name: "W",
		Params: map[string]any{"tools": []any{"a", "b"}, "nested": map[string]any{"k": "v"}},
	}}}
	if _, err := st.CreateWorkflow(ctx, Workflow{
		ID: "a", Name: "Alpha",
		Lock:           &Lock{Version: 1, Tools: []LockTool{{Tool: "browser", UsedBy: []string{"W"}, Risk: RiskRead}}},
		FailureDefault: FailurePolicy{Mode: "guardian", BackoffSecs: []int{10, 30}},
	}, g, "owner", "s"); err != nil {
		t.Fatal(err)
	}

	v, _ := st.GetVersion(ctx, "a", 1)
	v.Graph.Nodes[0].Params["tools"].([]any)[0] = "mutated"
	v.Graph.Nodes[0].Params["nested"].(map[string]any)["k"] = "mutated"
	again, _ := st.GetVersion(ctx, "a", 1)
	if tools := again.Graph.Nodes[0].Params["tools"].([]any); tools[0] != "a" {
		t.Fatalf("graph tools mutated: %v", tools)
	}
	if nested := again.Graph.Nodes[0].Params["nested"].(map[string]any); nested["k"] != "v" {
		t.Fatalf("graph nested mutated: %v", nested)
	}

	got, _ := st.GetWorkflow(ctx, "a")
	got.Lock.Tools[0].UsedBy[0] = "mutated"
	got.FailureDefault.BackoffSecs[0] = 99
	againW, _ := st.GetWorkflow(ctx, "a")
	if againW.Lock.Tools[0].UsedBy[0] != "W" || againW.FailureDefault.BackoffSecs[0] != 10 {
		t.Fatalf("workflow copy is not independent: %+v", againW)
	}

	if err := st.PutNodeRun(ctx, NodeRun{
		RunID: "r1", NodeID: "n", ItemIndex: -1,
		Logs: []string{"a"}, Attempts: []Attempt{{N: 1}}, Output: json.RawMessage(`{"x":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	nrs, _ := st.ListNodeRuns(ctx, "r1")
	nrs[0].Logs[0] = "mutated"
	nrs[0].Attempts[0].N = 9
	nrs[0].Output[1] = '9'
	nrs2, _ := st.ListNodeRuns(ctx, "r1")
	if nrs2[0].Logs[0] != "a" || nrs2[0].Attempts[0].N != 1 || string(nrs2[0].Output) != `{"x":1}` {
		t.Fatalf("node run copy is not independent: %+v", nrs2[0])
	}

	run, _ := st.CreateRun(ctx, Run{WorkflowID: "a", Overrides: map[string]map[string]any{"n": {"p": map[string]any{"q": 1}}}})
	run2, _ := st.GetRun(ctx, run.ID)
	run2.Overrides["n"]["p"].(map[string]any)["q"] = 99
	run3, _ := st.GetRun(ctx, run.ID)
	if run3.Overrides["n"]["p"].(map[string]any)["q"] != 1 {
		t.Fatalf("run overrides mutated: %+v", run3.Overrides)
	}

	if err := st.PutDecision(ctx, Decision{ID: "d1", WorkflowID: "a", Fix: map[string]any{"max_tokens": 4000}}); err != nil {
		t.Fatal(err)
	}
	d, _ := st.GetDecision(ctx, "d1")
	d.Fix["max_tokens"] = 1
	d2, _ := st.GetDecision(ctx, "d1")
	if d2.Fix["max_tokens"] != 4000 {
		t.Fatalf("decision fix mutated: %+v", d2.Fix)
	}

	if _, err := st.AppendChat(ctx, ChatMessage{
		WorkflowID: "a", Role: "owner", Text: "hi",
		Change: &GraphChange{Added: []string{"x"}}, Issues: []Issue{{Code: "c"}},
	}); err != nil {
		t.Fatal(err)
	}
	msgs, _ := st.ListChat(ctx, "a", 0)
	msgs[0].Change.Added[0] = "mutated"
	msgs[0].Issues[0].Code = "mutated"
	msgs2, _ := st.ListChat(ctx, "a", 0)
	if msgs2[0].Change.Added[0] != "x" || msgs2[0].Issues[0].Code != "c" {
		t.Fatalf("chat copy is not independent: %+v", msgs2[0])
	}
}

func TestMemStoreDecisionsStateChat(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()
	if _, err := st.CreateWorkflow(ctx, Workflow{ID: "a", Name: "Alpha"}, Graph{}, "owner", "s"); err != nil {
		t.Fatal(err)
	}

	if err := st.PutDecision(ctx, Decision{ID: "d1", WorkflowID: "a", RunID: "r1", Status: "pending", CreatedAt: 10}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutDecision(ctx, Decision{ID: "d2", WorkflowID: "a", RunID: "r2", Status: "resolved", CreatedAt: 20}); err != nil {
		t.Fatal(err)
	}
	// Upsert by id.
	if err := st.PutDecision(ctx, Decision{ID: "d1", WorkflowID: "a", RunID: "r1", Status: "applied", CreatedAt: 30}); err != nil {
		t.Fatal(err)
	}
	if d, err := st.GetDecision(ctx, "d1"); err != nil || d.Status != "applied" {
		t.Fatalf("get d1=%+v err=%v", d, err)
	}
	if _, err := st.GetDecision(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing decision err=%v", err)
	}

	all, _ := st.ListDecisions(ctx, DecisionFilter{WorkflowID: "a"})
	if len(all) != 2 || all[0].ID != "d1" || all[1].ID != "d2" {
		t.Fatalf("decisions newest first=%+v", all)
	}
	applied, _ := st.ListDecisions(ctx, DecisionFilter{WorkflowID: "a", Status: "applied"})
	if len(applied) != 1 || applied[0].ID != "d1" {
		t.Fatalf("applied=%+v", applied)
	}
	byRun, _ := st.ListDecisions(ctx, DecisionFilter{WorkflowID: "a", RunID: "r1"})
	if len(byRun) != 1 || byRun[0].ID != "d1" {
		t.Fatalf("by run=%+v", byRun)
	}

	if err := st.SetState(ctx, "a", "b", json.RawMessage(`2`)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(ctx, "a", "a", json.RawMessage(`1`)); err != nil {
		t.Fatal(err)
	}
	entries, _ := st.ListState(ctx, "a")
	if len(entries) != 2 || entries[0].Key != "a" || entries[1].Key != "b" || entries[0].UpdatedAt == 0 {
		t.Fatalf("state=%+v", entries)
	}
	if err := st.DeleteState(ctx, "a", "a"); err != nil {
		t.Fatal(err)
	}
	entries, _ = st.ListState(ctx, "a")
	if len(entries) != 1 || entries[0].Key != "b" {
		t.Fatalf("state after delete=%+v", entries)
	}
	if err := st.DeleteState(ctx, "a", ""); err != nil {
		t.Fatal(err)
	}
	if entries, _ = st.ListState(ctx, "a"); len(entries) != 0 {
		t.Fatalf("state after clear=%+v", entries)
	}

	for i := 1; i <= 5; i++ {
		m, err := st.AppendChat(ctx, ChatMessage{WorkflowID: "a", Role: "owner", Text: fmt.Sprintf("m%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if m.ID != int64(i) {
			t.Fatalf("chat id=%d want %d", m.ID, i)
		}
	}
	last, _ := st.ListChat(ctx, "a", 2)
	if len(last) != 2 || last[0].Text != "m4" || last[1].Text != "m5" {
		t.Fatalf("last chat=%+v", last)
	}
	allChat, _ := st.ListChat(ctx, "a", 0)
	if len(allChat) != 5 || allChat[0].Text != "m1" {
		t.Fatalf("all chat=%+v", allChat)
	}
}
