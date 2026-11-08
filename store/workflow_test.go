package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

func sampleGraph() workflow.Graph {
	return workflow.Graph{
		Nodes: []workflow.Node{
			{ID: "n1", Type: "trigger.manual", Name: "Start", Position: workflow.Position{X: 10, Y: 20}, Params: map[string]any{"enabled": true}},
			{ID: "n2", Type: "bash", Name: "Run", Position: workflow.Position{X: 10, Y: 100}, Params: map[string]any{"command": "echo hi"}},
		},
		Edges: []workflow.Edge{{ID: "e1", From: "n1", To: "n2"}},
	}
}

// createTestWorkflow stores one workflow and asserts CreateWorkflow normalises
// the version to 1.
func createTestWorkflow(t *testing.T, s *Store, id string) workflow.Workflow {
	t.Helper()
	w := workflow.Workflow{
		ID:                  id,
		Name:                "Riset " + id,
		Description:         "daily research",
		Guardian:            "lana",
		Status:              workflow.StatusDraft,
		Version:             99,
		Lock:                &workflow.Lock{Version: 1, LockedAt: 5, Tools: []workflow.LockTool{{Tool: "bash", UsedBy: []string{"n2"}, Risk: "admin"}}},
		FailureDefault:      workflow.FailurePolicy{Mode: "guardian"},
		WebhookToken:        "token-" + id,
		ConsecutiveFailures: 0,
		CreatedAt:           1000,
		UpdatedAt:           1000,
	}
	got, err := s.CreateWorkflow(context.Background(), w, sampleGraph(), "owner", "first")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 {
		t.Fatalf("CreateWorkflow version=%d, want 1", got.Version)
	}
	return got
}

func countRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// REQ-WF-001: every workflow and version method survives a store round trip.
func TestWorkflowRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-1")

	got, err := s.GetWorkflow(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, w) {
		t.Fatalf("GetWorkflow=%+v want=%+v", got, w)
	}

	list, err := s.ListWorkflows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !reflect.DeepEqual(list[0], w) {
		t.Fatalf("ListWorkflows=%+v", list)
	}

	version, err := s.GetVersion(ctx, w.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if version.Version != 1 || version.Author != "owner" || version.Summary != "first" || !reflect.DeepEqual(version.Graph, sampleGraph()) {
		t.Fatalf("GetVersion=%+v", version)
	}

	// A second save appends version 2 and updates the workflow version.
	graph2 := sampleGraph()
	graph2.Nodes = append(graph2.Nodes, workflow.Node{ID: "n3", Type: "message", Name: "Notify"})
	saved, err := s.SaveVersion(ctx, w.ID, 1, graph2, "guardian", "add notify")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 2 || !reflect.DeepEqual(saved.Graph, graph2) {
		t.Fatalf("SaveVersion=%+v", saved)
	}
	after, err := s.GetWorkflow(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != 2 {
		t.Fatalf("workflow version=%d, want 2", after.Version)
	}
	if after.UpdatedAt < w.UpdatedAt {
		t.Fatalf("updated_at did not advance: %d -> %d", w.UpdatedAt, after.UpdatedAt)
	}
	versions, err := s.ListVersions(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("ListVersions=%+v", versions)
	}

	// UpdateWorkflow is metadata only and must never touch the version.
	meta := after
	meta.Name = "Renamed"
	meta.Status = workflow.StatusActive
	meta.Version = 99
	if err := s.UpdateWorkflow(ctx, meta); err != nil {
		t.Fatal(err)
	}
	updated, err := s.GetWorkflow(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Renamed" || updated.Status != workflow.StatusActive {
		t.Fatalf("metadata not updated: %+v", updated)
	}
	if updated.Version != 2 {
		t.Fatalf("UpdateWorkflow changed version to %d", updated.Version)
	}

	if _, err := s.GetWorkflow(ctx, "missing"); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("missing workflow err=%v", err)
	}
	if _, err := s.GetVersion(ctx, w.ID, 99); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("missing version err=%v", err)
	}
	if err := s.UpdateWorkflow(ctx, workflow.Workflow{ID: "missing"}); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("update missing err=%v", err)
	}
}

// REQ-WF-001: a duplicate id is reported as ErrExists, both before and after
// the first version exists.
func TestCreateWorkflowDuplicate(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-dup")
	if _, err := s.CreateWorkflow(ctx, w, sampleGraph(), "owner", "again"); !errors.Is(err, workflow.ErrExists) {
		t.Fatalf("duplicate err=%v", err)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM workflow_versions WHERE workflow_id = ?", w.ID); n != 1 {
		t.Fatalf("versions after duplicate=%d, want 1", n)
	}
}

// REQ-WF-001: SaveVersion rejects a stale base and leaves the stored version
// untouched.
func TestSaveVersionConflict(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-conflict")
	if _, err := s.SaveVersion(ctx, w.ID, 1, sampleGraph(), "owner", "v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveVersion(ctx, w.ID, 1, sampleGraph(), "owner", "stale"); !errors.Is(err, workflow.ErrVersionConflict) {
		t.Fatalf("stale err=%v", err)
	}
	stored, err := s.GetWorkflow(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version != 2 {
		t.Fatalf("version after conflict=%d, want 2", stored.Version)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM workflow_versions WHERE workflow_id = ?", w.ID); n != 2 {
		t.Fatalf("versions after conflict=%d, want 2", n)
	}
	if _, err := s.SaveVersion(ctx, "missing", 1, sampleGraph(), "owner", "x"); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("missing workflow err=%v", err)
	}
}

// REQ-WF-007: run numbering is 1..N per workflow and independent between
// workflows; an empty id becomes "r_" plus 16 hex characters.
func TestCreateRunNumbering(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	a := createTestWorkflow(t, s, "wf-a")
	b := createTestWorkflow(t, s, "wf-b")
	seen := map[string]bool{}
	for _, wf := range []workflow.Workflow{a, b} {
		for i := 1; i <= 3; i++ {
			r, err := s.CreateRun(ctx, workflow.Run{WorkflowID: wf.ID, Version: 1, Trigger: "manual", Status: workflow.RunQueued, StartedAt: int64(i)})
			if err != nil {
				t.Fatal(err)
			}
			if r.Number != i {
				t.Fatalf("workflow %s run %d has number %d", wf.ID, i, r.Number)
			}
			if len(r.ID) != 18 || r.ID[:2] != "r_" {
				t.Fatalf("run id=%q, want r_ + 16 hex", r.ID)
			}
			if seen[r.ID] {
				t.Fatalf("duplicate run id %q", r.ID)
			}
			seen[r.ID] = true
		}
	}
}

// REQ-WF-007: the run methods round trip and ListRuns applies its filters.
func TestRunRoundTripAndFilters(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-runs")

	first, err := s.CreateRun(ctx, workflow.Run{
		WorkflowID: w.ID, Version: 1, Trigger: "manual", Status: workflow.RunSucceeded,
		Input:     json.RawMessage(`{"text":"hi"}`),
		Overrides: map[string]map[string]any{"n2": {"command": "echo hi"}},
		StartedAt: 100, EndedAt: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateRun(ctx, workflow.Run{WorkflowID: w.ID, Version: 1, Trigger: "schedule", Status: workflow.RunFailed, StartedAt: 300})
	if err != nil {
		t.Fatal(err)
	}
	third, err := s.CreateRun(ctx, workflow.Run{WorkflowID: w.ID, Version: 2, Trigger: "webhook", Status: workflow.RunRunning, StartedAt: 400})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.GetRun(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Input, first.Input) || !reflect.DeepEqual(got.Overrides, first.Overrides) {
		t.Fatalf("GetRun=%+v", got)
	}
	byNumber, err := s.GetRunByNumber(ctx, w.ID, second.Number)
	if err != nil {
		t.Fatal(err)
	}
	if byNumber.ID != second.ID {
		t.Fatalf("GetRunByNumber=%s want %s", byNumber.ID, second.ID)
	}

	got.Status = workflow.RunSucceeded
	got.Summary = "done"
	got.Tokens = 42
	got.Input = json.RawMessage(`{"text":"bye"}`)
	if err := s.UpdateRun(ctx, got); err != nil {
		t.Fatal(err)
	}
	reread, err := s.GetRun(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reread.Status != workflow.RunSucceeded || reread.Summary != "done" || reread.Tokens != 42 || !bytes.Equal(reread.Input, json.RawMessage(`{"text":"bye"}`)) {
		t.Fatalf("UpdateRun=%+v", reread)
	}

	all, err := s.ListRuns(ctx, workflow.RunFilter{WorkflowID: w.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != third.ID || all[2].ID != first.ID {
		t.Fatalf("ListRuns order=%+v", all)
	}

	succeeded, err := s.ListRuns(ctx, workflow.RunFilter{WorkflowID: w.ID, Statuses: []workflow.RunStatus{workflow.RunSucceeded, workflow.RunRunning}})
	if err != nil {
		t.Fatal(err)
	}
	if len(succeeded) != 2 || succeeded[0].ID != third.ID || succeeded[1].ID != first.ID {
		t.Fatalf("ListRuns statuses=%+v", succeeded)
	}

	since, err := s.ListRuns(ctx, workflow.RunFilter{WorkflowID: w.ID, Since: 300})
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 2 {
		t.Fatalf("ListRuns since=%+v", since)
	}

	before, err := s.ListRuns(ctx, workflow.RunFilter{WorkflowID: w.ID, Before: second.Number})
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].ID != first.ID {
		t.Fatalf("ListRuns before=%+v", before)
	}

	limited, err := s.ListRuns(ctx, workflow.RunFilter{WorkflowID: w.ID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].ID != third.ID {
		t.Fatalf("ListRuns limit=%+v", limited)
	}

	if _, err := s.GetRun(ctx, "missing"); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("missing run err=%v", err)
	}
	if err := s.UpdateRun(ctx, workflow.Run{ID: "missing"}); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("update missing run err=%v", err)
	}
}

// REQ-WF-007: node runs upsert by (run, node, item) and read back in order.
func TestNodeRunRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-nodes")
	run, err := s.CreateRun(ctx, workflow.Run{WorkflowID: w.ID, Version: 1, Trigger: "manual", Status: workflow.RunRunning, StartedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	node := workflow.NodeRun{
		RunID: run.ID, NodeID: "n2", ItemIndex: 0, Status: workflow.NodeSucceeded,
		Attempts: []workflow.Attempt{{N: 1, StartedAt: 10, EndedAt: 20}},
		Input:    json.RawMessage(`{"command":"echo hi"}`),
		Output:   json.RawMessage(`{"exit_code":0}`),
		Port:     "out", Error: "", Logs: []string{"line 1"}, Tokens: 7, SessionID: "s1",
		StartedAt: 10, EndedAt: 20,
	}
	if err := s.PutNodeRun(ctx, node); err != nil {
		t.Fatal(err)
	}
	node.Status = workflow.NodeFailed
	node.Error = "boom"
	node.Logs = []string{"line 1", "line 2"}
	if err := s.PutNodeRun(ctx, node); err != nil {
		t.Fatal(err)
	}
	other := node
	other.NodeID = "n3"
	other.ItemIndex = 1
	if err := s.PutNodeRun(ctx, other); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListNodeRuns(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("ListNodeRuns=%+v", list)
	}
	got := list[0]
	if got.NodeID != "n2" || got.Status != workflow.NodeFailed || got.Error != "boom" {
		t.Fatalf("upsert did not replace: %+v", got)
	}
	if !reflect.DeepEqual(got.Attempts, node.Attempts) || !bytes.Equal(got.Output, node.Output) || !reflect.DeepEqual(got.Logs, node.Logs) {
		t.Fatalf("node run round trip=%+v", got)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM workflow_node_runs WHERE run_id = ?", run.ID); n != 2 {
		t.Fatalf("node run rows=%d, want 2", n)
	}
}

// REQ-WF-008: a decision upserts by id and ListDecisions filters and orders by
// created_at.
func TestDecisionRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-dec")
	d := workflow.Decision{
		ID: "d1", WorkflowID: w.ID, RunID: "r1", RunNumber: 1, NodeID: "n2", ItemIndex: 0,
		Kind: "guardian", Action: "ask", Reason: "needs review", Fix: map[string]any{"command": "echo x"},
		Persistent: true, Confidence: 0.8, Tokens: 12, Status: "pending", Choice: "", CreatedAt: 100,
	}
	if err := s.PutDecision(ctx, d); err != nil {
		t.Fatal(err)
	}
	d.Status = "resolved"
	d.Choice = "apply"
	d.ResolvedAt = 200
	if err := s.PutDecision(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDecision(ctx, workflow.Decision{ID: "d2", WorkflowID: w.ID, RunID: "r1", Kind: "approval", Action: "ask", Status: "pending", CreatedAt: 300}); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetDecision(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "resolved" || got.Choice != "apply" || got.ResolvedAt != 200 || !got.Persistent || !reflect.DeepEqual(got.Fix, d.Fix) {
		t.Fatalf("GetDecision=%+v", got)
	}

	all, err := s.ListDecisions(ctx, workflow.DecisionFilter{WorkflowID: w.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != "d2" || all[1].ID != "d1" {
		t.Fatalf("ListDecisions=%+v", all)
	}
	pending, err := s.ListDecisions(ctx, workflow.DecisionFilter{WorkflowID: w.ID, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "d2" {
		t.Fatalf("ListDecisions status=%+v", pending)
	}
	byRun, err := s.ListDecisions(ctx, workflow.DecisionFilter{WorkflowID: w.ID, RunID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byRun) != 2 {
		t.Fatalf("ListDecisions run=%+v", byRun)
	}
	if _, err := s.GetDecision(ctx, "missing"); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("missing decision err=%v", err)
	}
}

// REQ-WF-006: state entries list by key, upsert, delete one, and clear all with
// an empty key.
func TestStateRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-state")
	if err := s.SetState(ctx, w.ID, "b", json.RawMessage(`2`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(ctx, w.ID, "a", json.RawMessage(`{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListState(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Key != "a" || list[1].Key != "b" {
		t.Fatalf("ListState=%+v", list)
	}
	if !bytes.Equal(list[0].Value, json.RawMessage(`{"x":1}`)) || list[0].UpdatedAt == 0 {
		t.Fatalf("state value=%+v", list[0])
	}

	if err := s.SetState(ctx, w.ID, "a", json.RawMessage(`{"x":2}`)); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListState(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !bytes.Equal(list[0].Value, json.RawMessage(`{"x":2}`)) {
		t.Fatalf("upsert state=%+v", list)
	}

	if err := s.DeleteState(ctx, w.ID, "a"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM workflow_state WHERE workflow_id = ?", w.ID); n != 1 {
		t.Fatalf("after delete one=%d, want 1", n)
	}
	if err := s.DeleteState(ctx, w.ID, ""); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM workflow_state WHERE workflow_id = ?", w.ID); n != 0 {
		t.Fatalf("after clear all=%d, want 0", n)
	}
}

// REQ-WF-001: chat appends return the new id and ListChat returns the last
// limit messages oldest first.
func TestChatRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-chat")
	first, err := s.AppendChat(ctx, workflow.ChatMessage{WorkflowID: w.ID, Role: "owner", Text: "hello", CreatedAt: 10})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == 0 {
		t.Fatalf("AppendChat did not set an id")
	}
	// DiffGraphs never leaves a list nil, and the store writes nil lists as [].
	change := &workflow.GraphChange{FromVersion: 1, ToVersion: 2, Summary: "added", Added: []string{"n3"}, Removed: []string{}, Changed: []string{}}
	second, err := s.AppendChat(ctx, workflow.ChatMessage{
		WorkflowID: w.ID, Role: "guardian", Text: "did it", CreatedAt: 20,
		Change: change, Issues: []workflow.Issue{{NodeID: "n1", Code: "missing_param", Message: "need x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendChat(ctx, workflow.ChatMessage{WorkflowID: w.ID, Role: "owner", Text: "thanks", CreatedAt: 30}); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListChat(ctx, w.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != first.ID || all[1].ID != second.ID {
		t.Fatalf("ListChat all=%+v", all)
	}
	if !reflect.DeepEqual(all[1].Change, change) || len(all[1].Issues) != 1 || all[1].Issues[0].Code != "missing_param" {
		t.Fatalf("chat detail=%+v", all[1])
	}

	last, err := s.ListChat(ctx, w.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(last) != 2 || last[0].ID != second.ID {
		t.Fatalf("ListChat limit=%+v", last)
	}
}

// REQ-WF-001: deleting a workflow removes every child row through the foreign
// key cascade.
func TestDeleteWorkflowCascades(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-cascade")
	run, err := s.CreateRun(ctx, workflow.Run{WorkflowID: w.ID, Version: 1, Trigger: "manual", Status: workflow.RunRunning, StartedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutNodeRun(ctx, workflow.NodeRun{RunID: run.ID, NodeID: "n1", ItemIndex: 0, Status: workflow.NodeSucceeded}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDecision(ctx, workflow.Decision{ID: "d1", WorkflowID: w.ID, RunID: run.ID, Kind: "guardian", Action: "ask", Status: "pending", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(ctx, w.ID, "k", json.RawMessage(`1`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendChat(ctx, workflow.ChatMessage{WorkflowID: w.ID, Role: "owner", Text: "hi", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteWorkflow(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWorkflow(ctx, w.ID); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("second delete err=%v", err)
	}

	checks := []struct {
		name  string
		query string
		args  []any
	}{
		{"workflows", "SELECT COUNT(*) FROM workflows WHERE id = ?", []any{w.ID}},
		{"workflow_versions", "SELECT COUNT(*) FROM workflow_versions WHERE workflow_id = ?", []any{w.ID}},
		{"workflow_runs", "SELECT COUNT(*) FROM workflow_runs WHERE workflow_id = ?", []any{w.ID}},
		{"workflow_node_runs", "SELECT COUNT(*) FROM workflow_node_runs WHERE run_id = ?", []any{run.ID}},
		{"workflow_decisions", "SELECT COUNT(*) FROM workflow_decisions WHERE workflow_id = ?", []any{w.ID}},
		{"workflow_state", "SELECT COUNT(*) FROM workflow_state WHERE workflow_id = ?", []any{w.ID}},
		{"workflow_chat", "SELECT COUNT(*) FROM workflow_chat WHERE workflow_id = ?", []any{w.ID}},
	}
	for _, check := range checks {
		if n := countRows(t, s, check.query, check.args...); n != 0 {
			t.Fatalf("%s has %d rows after cascade delete", check.name, n)
		}
	}
}

// REQ-WF-007: a fresh database has the workflow tables and foreign keys on.
func TestMigrateFreshAddsWorkflows(t *testing.T) {
	s, _ := openTest(t)
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("version=%d, want %d", version, schemaVersion)
	}
	for _, table := range []string{"workflows", "workflow_versions", "workflow_runs", "workflow_node_runs", "workflow_decisions", "workflow_state", "workflow_chat"} {
		if n := countRows(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table); n != 1 {
			t.Fatalf("table %s count=%d, want 1", table, n)
		}
	}
	var fk int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatalf("PRAGMA foreign_keys=%d, want 1", fk)
	}
}

// v5Database builds a schema-version-5 database the way the previous binary
// left it: migrations 0001-0005 applied and user_version set to 5.
func v5Database(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lana.db")
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"migrations/0001_session_log.sql", "migrations/0002_session_leases.sql", "migrations/0003_session_status_waiting_input.sql", "migrations/0004_session_text.sql", "migrations/0005_room_reads.sql"} {
		data, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 5"); err != nil {
		t.Fatal(err)
	}
	return db, path
}

// REQ-WF-007: a version-5 database migrates to version 6 and its workflow
// tables work afterwards.
func TestMigrateV5ToV6Workflows(t *testing.T) {
	ctx := context.Background()
	db, path := v5Database(t)
	var before int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'workflows'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("v5 fixture already has a workflows table")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("version=%d, want %d", version, schemaVersion)
	}

	// The migrated database still persists room reads and accepts workflows.
	if stored, err := s.SetRoomRead(ctx, "dana", 7000); err != nil || stored != 7000 {
		t.Fatalf("SetRoomRead after migrate stored=%d err=%v", stored, err)
	}
	w := createTestWorkflow(t, s, "wf-migrated")
	if _, err := s.GetWorkflow(ctx, w.ID); err != nil {
		t.Fatalf("get workflow after migrate: %v", err)
	}
}

// A single-node graph is stored with "edges":[], and a version stored before
// that fix with "edges":null still loads.
func TestWorkflowGraphEdgesNeverNull(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	g := workflow.Graph{Nodes: []workflow.Node{{ID: "run-button", Type: "trigger.manual", Name: "Run"}}}
	w := workflow.Workflow{ID: "hi", Name: "hi", Guardian: "lana", Status: workflow.StatusDraft, WebhookToken: "tok", CreatedAt: 1, UpdatedAt: 1}
	if _, err := s.CreateWorkflow(ctx, w, g, "owner", "created"); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.QueryRow("SELECT graph_json FROM workflow_versions WHERE workflow_id = ? AND version = 1", "hi").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(stored), []byte(`"edges":[]`)) {
		t.Fatalf("stored graph_json=%s, want \"edges\":[]", stored)
	}

	legacy := `{"nodes":[{"id":"run-button","type":"trigger.manual","name":"Run","position":{"x":0,"y":0},"params":{}}],"edges":null}`
	if _, err := s.db.Exec("INSERT INTO workflow_versions (workflow_id, version, graph_json, author, summary, created_at) VALUES (?, ?, ?, ?, ?, ?)", "hi", 2, legacy, "owner", "legacy", 2); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetVersion(ctx, "hi", 2)
	if err != nil {
		t.Fatalf("GetVersion legacy: %v", err)
	}
	if len(v.Graph.Nodes) != 1 {
		t.Fatalf("legacy graph=%+v", v.Graph)
	}
	if _, err := s.ListVersions(ctx, "hi"); err != nil {
		t.Fatalf("ListVersions legacy: %v", err)
	}
	b, err := json.Marshal(v.Graph)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"edges":[]`)) {
		t.Fatalf("legacy graph JSON=%s", b)
	}
}
