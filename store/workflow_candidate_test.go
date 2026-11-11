package store

// Tests for the spec 51 workflow columns and the workflow_candidates table:
// assistant_session, candidate_id, chat session_id/turn_id, candidate CRUD and
// the v10 -> v11 migration.

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// Spec 51: assistant_session round-trips through UpdateWorkflow, GetWorkflow
// and ListWorkflows.
func TestWorkflowAssistantSessionRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-assist")
	if w.AssistantSession != "" {
		t.Fatalf("new workflow assistant_session=%q, want empty", w.AssistantSession)
	}
	w.AssistantSession = "sess-guardian"
	w.UpdatedAt = 2000
	if err := s.UpdateWorkflow(ctx, w); err != nil {
		t.Fatal(err)
	}
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
	if len(list) != 1 || list[0].AssistantSession != "sess-guardian" {
		t.Fatalf("ListWorkflows=%+v", list)
	}
}

// Spec 51: candidate_id round-trips through CreateRun, UpdateRun and every run
// getter.
func TestRunCandidateIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-cand-run")
	run, err := s.CreateRun(ctx, workflow.Run{WorkflowID: w.ID, Version: 1, CandidateID: 7, Trigger: "test", Status: workflow.RunQueued, StartedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CandidateID != 7 {
		t.Fatalf("GetRun candidate_id=%d, want 7", got.CandidateID)
	}
	got.CandidateID = 9
	got.Status = workflow.RunSucceeded
	if err := s.UpdateRun(ctx, got); err != nil {
		t.Fatal(err)
	}
	byNumber, err := s.GetRunByNumber(ctx, w.ID, run.Number)
	if err != nil {
		t.Fatal(err)
	}
	if byNumber.CandidateID != 9 {
		t.Fatalf("GetRunByNumber candidate_id=%d, want 9", byNumber.CandidateID)
	}
	plain, err := s.CreateRun(ctx, workflow.Run{WorkflowID: w.ID, Version: 1, Trigger: "manual", Status: workflow.RunQueued, StartedAt: 2})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.ListRuns(ctx, workflow.RunFilter{WorkflowID: w.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != plain.ID || list[0].CandidateID != 0 || list[1].CandidateID != 9 {
		t.Fatalf("ListRuns=%+v", list)
	}
}

// Spec 51: session_id and turn_id round-trip through AppendChat and ListChat,
// and a message without them reads back empty.
func TestChatSessionTurnRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-chat-turn")
	if _, err := s.AppendChat(ctx, workflow.ChatMessage{WorkflowID: w.ID, Role: "owner", Text: "legacy", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	change := &workflow.GraphChange{FromVersion: 1, ToVersion: 2, Summary: "s", Added: []string{"n3"}, Removed: []string{}, Changed: []string{}}
	stored, err := s.AppendChat(ctx, workflow.ChatMessage{WorkflowID: w.ID, Role: "guardian", Text: "published", Change: change, SessionID: "sess-1", TurnID: "turn-9", CreatedAt: 2})
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.ListChat(ctx, w.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("ListChat=%+v", all)
	}
	if all[0].SessionID != "" || all[0].TurnID != "" {
		t.Fatalf("legacy message=%+v, want empty session and turn", all[0])
	}
	if !reflect.DeepEqual(all[1], stored) {
		t.Fatalf("ListChat[1]=%+v want=%+v", all[1], stored)
	}
	last, err := s.ListChat(ctx, w.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(last) != 1 || last[0].SessionID != "sess-1" || last[0].TurnID != "turn-9" {
		t.Fatalf("ListChat limit=%+v", last)
	}
}

// Spec 51 REQ-WFC-003: CreateCandidate sets id, status and times, supersedes
// the previous open candidate, and GetCandidate, OpenCandidate and
// UpdateCandidate round-trip every field.
func TestCandidateCRUD(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-cand")

	if _, err := s.OpenCandidate(ctx, w.ID); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("OpenCandidate on empty err=%v, want ErrNotFound", err)
	}
	if _, err := s.GetCandidate(ctx, 42); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("GetCandidate missing err=%v, want ErrNotFound", err)
	}
	if _, err := s.CreateCandidate(ctx, workflow.Candidate{WorkflowID: "nope", BaseVersion: 1, Graph: sampleGraph(), Author: "guardian"}); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("CreateCandidate unknown workflow err=%v, want ErrNotFound", err)
	}

	first, err := s.CreateCandidate(ctx, workflow.Candidate{WorkflowID: w.ID, BaseVersion: 1, Graph: sampleGraph(), Summary: "first", Author: "guardian", Status: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == 0 || first.Status != workflow.CandidateOpen || first.CreatedAt == 0 || first.UpdatedAt != first.CreatedAt {
		t.Fatalf("CreateCandidate=%+v", first)
	}
	got, err := s.GetCandidate(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, first) {
		t.Fatalf("GetCandidate=%+v want=%+v", got, first)
	}

	g2 := sampleGraph()
	g2.Nodes = append(g2.Nodes, workflow.Node{ID: "n3", Type: "bash", Name: "More", Params: map[string]any{"command": "echo more"}})
	g2.Edges = append(g2.Edges, workflow.Edge{ID: "e2", From: "n2", To: "n3"})
	second, err := s.CreateCandidate(ctx, workflow.Candidate{WorkflowID: w.ID, BaseVersion: 1, Graph: g2, Summary: "second", Author: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatalf("second candidate reused id %d", second.ID)
	}
	old, err := s.GetCandidate(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != workflow.CandidateSuperseded {
		t.Fatalf("first candidate status=%q, want superseded", old.Status)
	}
	open, err := s.OpenCandidate(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(open, second) {
		t.Fatalf("OpenCandidate=%+v want=%+v", open, second)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM workflow_candidates WHERE workflow_id = ? AND status = 'open'", w.ID); n != 1 {
		t.Fatalf("open candidates=%d, want 1", n)
	}

	// UpdateCandidate writes graph, summary, status, published_version and
	// updated_at, and leaves base_version, author and created_at alone.
	patched := second
	patched.Graph = sampleGraph()
	patched.Summary = "patched"
	patched.Status = workflow.CandidatePublished
	patched.PublishedVersion = 2
	patched.UpdatedAt = second.UpdatedAt + 50
	patched.BaseVersion = 99
	patched.Author = "someone"
	if err := s.UpdateCandidate(ctx, patched); err != nil {
		t.Fatal(err)
	}
	reread, err := s.GetCandidate(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := second
	want.Graph = sampleGraph()
	want.Summary = "patched"
	want.Status = workflow.CandidatePublished
	want.PublishedVersion = 2
	want.UpdatedAt = second.UpdatedAt + 50
	if !reflect.DeepEqual(reread, want) {
		t.Fatalf("after UpdateCandidate=%+v want=%+v", reread, want)
	}
	if _, err := s.OpenCandidate(ctx, w.ID); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("OpenCandidate after publish err=%v, want ErrNotFound", err)
	}
	if err := s.UpdateCandidate(ctx, workflow.Candidate{ID: 999, Status: workflow.CandidateDiscarded}); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("UpdateCandidate missing err=%v, want ErrNotFound", err)
	}

	// Candidates of another workflow are independent.
	other := createTestWorkflow(t, s, "wf-cand-other")
	if _, err := s.CreateCandidate(ctx, workflow.Candidate{WorkflowID: other.ID, BaseVersion: 1, Graph: sampleGraph(), Author: "guardian"}); err != nil {
		t.Fatal(err)
	}
	if again, err := s.GetCandidate(ctx, second.ID); err != nil || again.Status != workflow.CandidatePublished {
		t.Fatalf("other workflow's create touched this one: %+v %v", again, err)
	}
}

// Spec 51: deleting a workflow cascades to its candidates.
func TestDeleteWorkflowCascadesCandidates(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := createTestWorkflow(t, s, "wf-cand-cascade")
	if _, err := s.CreateCandidate(ctx, workflow.Candidate{WorkflowID: w.ID, BaseVersion: 1, Graph: sampleGraph(), Author: "guardian"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWorkflow(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM workflow_candidates WHERE workflow_id = ?", w.ID); n != 0 {
		t.Fatalf("workflow_candidates has %d rows after cascade delete", n)
	}
}

// Spec 51: a version-10 database migrates to the current version; existing
// rows read back with empty assistant_session, candidate_id 0 and empty chat
// session and turn ids.
func TestMigrateV10ToV11AddsCandidates(t *testing.T) {
	ctx := context.Background()
	db, path := v9Database(t)
	data, err := migrationFS.ReadFile("migrations/0010_workflow_node_via.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(data)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 10"); err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		"INSERT INTO workflows (id, name, guardian, status, version, created_at, updated_at) VALUES ('old', 'Old', 'lana', 'draft', 1, 1, 1)",
		"INSERT INTO workflow_versions (workflow_id, version, graph_json, author, created_at) VALUES ('old', 1, '{\"nodes\":[],\"edges\":[]}', 'owner', 1)",
		"INSERT INTO workflow_runs (id, workflow_id, number, version, trigger, status, started_at) VALUES ('r_old', 'old', 1, 1, 'manual', 'succeeded', 1)",
		"INSERT INTO workflow_chat (workflow_id, role, text, created_at) VALUES ('old', 'owner', 'hi', 1)",
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
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
	if version != schemaVersion || schemaVersion < 11 {
		t.Fatalf("version=%d schemaVersion=%d, want >= 11", version, schemaVersion)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'workflow_candidates'"); n != 1 {
		t.Fatalf("workflow_candidates table count=%d, want 1", n)
	}

	wf, err := s.GetWorkflow(ctx, "old")
	if err != nil {
		t.Fatal(err)
	}
	if wf.AssistantSession != "" {
		t.Fatalf("migrated assistant_session=%q", wf.AssistantSession)
	}
	run, err := s.GetRun(ctx, "r_old")
	if err != nil {
		t.Fatal(err)
	}
	if run.CandidateID != 0 {
		t.Fatalf("migrated candidate_id=%d", run.CandidateID)
	}
	chat, err := s.ListChat(ctx, "old", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chat) != 1 || chat[0].SessionID != "" || chat[0].TurnID != "" {
		t.Fatalf("migrated chat=%+v", chat)
	}
	c, err := s.CreateCandidate(ctx, workflow.Candidate{WorkflowID: "old", BaseVersion: 1, Graph: workflow.Graph{}, Author: "guardian"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == 0 {
		t.Fatal("CreateCandidate after migrate set no id")
	}
}
