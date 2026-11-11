package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// assistDeps is fakeDeps with the guardian chat runtime of spec 51: sessions
// are numbered, and a turn runs the test's guardian function, which plays the
// agent calling workflow_builder.
type assistDeps struct {
	*fakeDeps
	env AssistEnv

	mu       sync.Mutex
	sessions map[string]string // id -> agent
	seq      int
	notes    []string
	guardian func(ctx context.Context, sessionID, text string) string
}

func newAssistDeps() *assistDeps {
	return &assistDeps{fakeDeps: newFakeDeps(), sessions: map[string]string{}}
}

func (d *assistDeps) AssistEnv(context.Context, Workflow) AssistEnv { return d.env }

func (d *assistDeps) CreateAssistantSession(_ context.Context, wf Workflow) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	id := fmt.Sprintf("s%d", d.seq)
	d.sessions[id] = wf.Guardian
	return id, nil
}

func (d *assistDeps) AssistantSessionExists(_ context.Context, sessionID, agent string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	owner, ok := d.sessions[sessionID]
	return ok && owner == agent
}

func (d *assistDeps) RunAssistantTurn(ctx context.Context, sessionID, note, text string, _ []string) (AgentResult, error) {
	d.mu.Lock()
	d.notes = append(d.notes, note)
	guardian := d.guardian
	d.mu.Unlock()
	reply := "ok"
	if guardian != nil {
		reply = guardian(ctx, sessionID, text)
	}
	return AgentResult{Text: reply, SessionID: sessionID, Tokens: 42}, nil
}

// waitRunDone polls until the run leaves queued/running.
func waitRunDone(t *testing.T, svc *Service, wfID, runID string) RunDetail {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		detail, err := svc.RunDetail(context.Background(), wfID, runID)
		if err == nil && detail.Run.Status != RunQueued && detail.Run.Status != RunRunning {
			return detail
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s did not finish", runID)
	return RunDetail{}
}

// Spec 51 REQ-WFC-001: one session per workflow, replaced by /new or when the
// guardian changes; REQ-WFC-002: the full guide first, then only changes.
func TestAssistantSessionAndNotes(t *testing.T) {
	st := NewMemStore()
	deps := newAssistDeps()
	deps.env = AssistEnv{MCPTools: []EnvTool{{Name: "mcp__hub__task_list"}}}
	svc := newTestService(t, st, deps, &testClock{})
	ctx := context.Background()

	g := Graph{Nodes: []Node{wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true})}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	sid, msgs, err := svc.AssistantChat(ctx, "w")
	if err != nil || sid != "" || len(msgs) != 0 {
		t.Fatalf("chat before use = %q %v %v", sid, msgs, err)
	}
	s1, err := svc.EnsureAssistantSession(ctx, "w")
	if err != nil || s1 != "s1" {
		t.Fatalf("ensure = %q, %v", s1, err)
	}
	if again, _ := svc.EnsureAssistantSession(ctx, "w"); again != s1 {
		t.Fatalf("ensure again = %q, want %q", again, s1)
	}
	if sid, _, _ := svc.AssistantChat(ctx, "w"); sid != s1 {
		t.Fatalf("chat session = %q", sid)
	}

	note, commit, err := svc.AssistantNote(ctx, "w", s1)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"You are the guardian of workflow", "mcp__hub__task_list", "workflow_builder"} {
		if !strings.Contains(note, want) {
			t.Fatalf("first note misses %q", want)
		}
	}
	commit()
	if note, _, _ := svc.AssistantNote(ctx, "w", s1); note != "" {
		t.Fatalf("second note with nothing new = %q", note)
	}
	g2 := Graph{Nodes: append(g.Nodes, wfNode("say", "bash", "Say", map[string]any{"command": "echo hi"})), Edges: []Edge{wfEdge("start", "out", "say")}}
	if _, err := st.SaveVersion(ctx, "w", 1, g2, "owner", "canvas edit"); err != nil {
		t.Fatal(err)
	}
	if note, _, _ := svc.AssistantNote(ctx, "w", s1); !strings.Contains(note, "current version is now 2") {
		t.Fatalf("delta note = %q", note)
	}

	s2, err := svc.NewAssistantSession(ctx, "w")
	if err != nil || s2 == s1 {
		t.Fatalf("new = %q, %v", s2, err)
	}
	wf, _ := st.GetWorkflow(ctx, "w")
	wf.Guardian = "other"
	if err := st.UpdateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	s3, _ := svc.EnsureAssistantSession(ctx, "w")
	if s3 == s2 {
		t.Fatal("a new guardian should get a new session")
	}
}

// REQ-WFC-003 to REQ-WFC-006, REQ-WFC-009: in one chat turn the guardian saves
// a candidate, tests it without touching the current version, publishes it,
// and Assist reports the change card of that turn.
func TestAssistSaveTestPublish(t *testing.T) {
	st := NewMemStore()
	deps := newAssistDeps()
	svc := newTestService(t, st, deps, &testClock{})
	ctx := context.Background()

	g := Graph{Nodes: []Node{wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true})}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	var testRun RunDetail
	deps.guardian = func(ctx context.Context, sid, text string) string {
		if _, err := svc.BuilderGet(ctx, sid); err != nil {
			t.Errorf("get: %v", err)
		}
		bad := Graph{Nodes: []Node{wfNode("a", "bogus", "A", nil)}}
		if _, _, issues, err := svc.BuilderSave(ctx, sid, bad, "bad"); err != nil || len(issues) == 0 {
			t.Errorf("bad save: issues %v, err %v", issues, err)
		}
		next := Graph{
			Nodes: []Node{
				wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true}),
				wfNode("say", "bash", "Say", map[string]any{"command": "echo hi"}),
			},
			Edges: []Edge{wfEdge("start", "out", "say")},
		}
		cand, change, issues, err := svc.BuilderSave(ctx, sid, next, "add Say")
		if err != nil || len(issues) > 0 || cand.ID == 0 || len(change.Added) != 1 {
			t.Errorf("save: %+v %+v %v %v", cand, change, issues, err)
		}
		if cur, _ := st.GetWorkflow(ctx, "w"); cur.Version != 1 {
			t.Errorf("a candidate must not change the current version, got %d", cur.Version)
		}
		run, err := svc.BuilderTest(ctx, sid, nil)
		if err != nil {
			t.Errorf("test: %v", err)
			return "test failed"
		}
		testRun = waitRunDone(t, svc, "w", run.ID)
		if _, err := svc.BuilderPublish(ctx, sid, "turn-9"); err != nil {
			t.Errorf("publish: %v", err)
		}
		return "Sudah ditambah dan dites."
	}

	res, err := svc.Assist(ctx, "w", 1, "tambah langkah Say")
	if err != nil {
		t.Fatalf("assist: %v", err)
	}
	if testRun.Run.Trigger != "test" || testRun.Run.CandidateID == 0 || testRun.Run.Status != RunSucceeded {
		t.Fatalf("test run = %+v", testRun.Run)
	}
	if res.Message.Text != "Sudah ditambah dan dites." || res.Tokens != 42 {
		t.Fatalf("message = %+v tokens %d", res.Message, res.Tokens)
	}
	if res.Message.Change == nil || res.Message.Change.ToVersion != 2 || res.Message.TurnID != "turn-9" {
		t.Fatalf("change = %+v turn %q", res.Message.Change, res.Message.TurnID)
	}
	if res.Workflow.Version != 2 {
		t.Fatalf("workflow version = %d, want 2", res.Workflow.Version)
	}
	_, msgs, _ := svc.AssistantChat(ctx, "w")
	if len(msgs) != 1 || msgs[0].SessionID != "s1" || msgs[0].TurnID != "turn-9" || msgs[0].Change == nil {
		t.Fatalf("chat = %+v", msgs)
	}
	wf, _ := st.GetWorkflow(ctx, "w")
	if wf.ConsecutiveFailures != 0 {
		t.Fatalf("consecutive failures = %d", wf.ConsecutiveFailures)
	}
}

// REQ-WFC-003: builder calls from any other session are refused.
func TestBuilderRefusesOtherSession(t *testing.T) {
	st := NewMemStore()
	deps := newAssistDeps()
	svc := newTestService(t, st, deps, &testClock{})
	ctx := context.Background()
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, Graph{Nodes: []Node{wfNode("start", "trigger.manual", "Start", nil)}})
	if _, err := svc.EnsureAssistantSession(ctx, "w"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BuilderGet(ctx, "elsewhere"); !errors.Is(err, ErrNotAssistantSession) {
		t.Fatalf("err = %v", err)
	}
}

// Without the runtime extension the guardian chat is unavailable.
func TestAssistantUnavailable(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, Graph{Nodes: []Node{wfNode("start", "trigger.manual", "Start", nil)}})
	if _, err := svc.Assist(context.Background(), "w", 1, "hi"); !errors.Is(err, ErrAssistantUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
