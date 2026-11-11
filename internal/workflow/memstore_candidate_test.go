package workflow

// MemStore parity tests for spec 51: assistant_session, candidate_id, chat
// session_id/turn_id and the candidate methods behave like the SQLite store.

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func memCandidateGraph(extra ...string) Graph {
	g := Graph{Nodes: []Node{{ID: "start", Type: "trigger.manual", Name: "Start", Params: map[string]any{"enabled": true}}}}
	for _, id := range extra {
		g.Nodes = append(g.Nodes, Node{ID: id, Type: "bash", Name: id, Params: map[string]any{"command": "echo " + id}})
		g.Edges = append(g.Edges, Edge{ID: "start->" + id, From: "start", To: id})
	}
	return g
}

func TestMemStoreSpec51Columns(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()
	w, err := st.CreateWorkflow(ctx, Workflow{ID: "a", Name: "A", Guardian: "g", Status: StatusDraft}, memCandidateGraph(), "owner", "first")
	if err != nil {
		t.Fatal(err)
	}
	w.AssistantSession = "sess-a"
	if err := st.UpdateWorkflow(ctx, w); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetWorkflow(ctx, "a"); got.AssistantSession != "sess-a" {
		t.Fatalf("assistant_session=%q", got.AssistantSession)
	}
	if list, _ := st.ListWorkflows(ctx); len(list) != 1 || list[0].AssistantSession != "sess-a" {
		t.Fatalf("list=%+v", list)
	}

	run, err := st.CreateRun(ctx, Run{WorkflowID: "a", Version: 1, CandidateID: 3, Trigger: "test", Status: RunQueued})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetRun(ctx, run.ID); got.CandidateID != 3 {
		t.Fatalf("candidate_id=%d", got.CandidateID)
	}
	run.CandidateID = 4
	if err := st.UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetRunByNumber(ctx, "a", run.Number); got.CandidateID != 4 {
		t.Fatalf("candidate_id after update=%d", got.CandidateID)
	}

	msg, err := st.AppendChat(ctx, ChatMessage{WorkflowID: "a", Role: "guardian", Text: "x", SessionID: "sess-a", TurnID: "t1", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	chat, _ := st.ListChat(ctx, "a", 0)
	if len(chat) != 1 || !reflect.DeepEqual(chat[0], msg) || chat[0].TurnID != "t1" {
		t.Fatalf("chat=%+v", chat)
	}
}

func TestMemStoreCandidates(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()
	if _, err := st.CreateWorkflow(ctx, Workflow{ID: "a", Name: "A", Guardian: "g"}, memCandidateGraph(), "owner", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateWorkflow(ctx, Workflow{ID: "b", Name: "B", Guardian: "g"}, memCandidateGraph(), "owner", "first"); err != nil {
		t.Fatal(err)
	}

	if _, err := st.OpenCandidate(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open on empty err=%v", err)
	}
	if _, err := st.CreateCandidate(ctx, Candidate{WorkflowID: "nope", Graph: memCandidateGraph()}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("create unknown workflow err=%v", err)
	}

	first, err := st.CreateCandidate(ctx, Candidate{WorkflowID: "a", BaseVersion: 1, Graph: memCandidateGraph("x"), Summary: "one", Author: "guardian", Status: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == 0 || first.Status != CandidateOpen || first.CreatedAt == 0 || first.UpdatedAt != first.CreatedAt {
		t.Fatalf("first=%+v", first)
	}
	otherWF, err := st.CreateCandidate(ctx, Candidate{WorkflowID: "b", BaseVersion: 1, Graph: memCandidateGraph(), Author: "guardian"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateCandidate(ctx, Candidate{WorkflowID: "a", BaseVersion: 1, Graph: memCandidateGraph("y"), Summary: "two", Author: "guardian"})
	if err != nil {
		t.Fatal(err)
	}
	if old, _ := st.GetCandidate(ctx, first.ID); old.Status != CandidateSuperseded {
		t.Fatalf("first status=%q want superseded", old.Status)
	}
	if b, _ := st.GetCandidate(ctx, otherWF.ID); b.Status != CandidateOpen {
		t.Fatalf("other workflow candidate status=%q want open", b.Status)
	}
	open, err := st.OpenCandidate(ctx, "a")
	if err != nil || !reflect.DeepEqual(open, second) {
		t.Fatalf("open=%+v err=%v want %+v", open, err, second)
	}

	// Returned values are copies.
	open.Graph.Nodes[0].Params["enabled"] = false
	if again, _ := st.GetCandidate(ctx, second.ID); again.Graph.Nodes[0].Params["enabled"] != true {
		t.Fatal("mutating a returned candidate changed the store")
	}

	patched := second
	patched.Graph = memCandidateGraph("z")
	patched.Summary = "patched"
	patched.Status = CandidatePublished
	patched.PublishedVersion = 2
	patched.UpdatedAt = second.UpdatedAt + 5
	patched.BaseVersion = 99
	patched.Author = "other"
	if err := st.UpdateCandidate(ctx, patched); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetCandidate(ctx, second.ID)
	want := second
	want.Graph = memCandidateGraph("z")
	want.Summary = "patched"
	want.Status = CandidatePublished
	want.PublishedVersion = 2
	want.UpdatedAt = second.UpdatedAt + 5
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after update=%+v want %+v", got, want)
	}
	if _, err := st.OpenCandidate(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open after publish err=%v", err)
	}
	if err := st.UpdateCandidate(ctx, Candidate{ID: 999}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing err=%v", err)
	}

	if err := st.DeleteWorkflow(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetCandidate(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("candidate survived cascade delete: %v", err)
	}
	if _, err := st.GetCandidate(ctx, otherWF.ID); err != nil {
		t.Fatalf("other workflow candidate lost: %v", err)
	}
}
