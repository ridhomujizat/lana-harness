package workflow

// Tests for the spec 51 candidate operations and the engine rules of candidate
// test runs (REQ-WFC-003, REQ-WFC-005, REQ-WFC-006). They use MemStore, the
// fake Deps of the engine tests and newTestService.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// candNotifier records owner notices for a Service.
type candNotifier struct{ log noticeLog }

func (n *candNotifier) Notify(ctx context.Context, nt Notice) { n.log.notify(ctx, nt) }

func (n *candNotifier) SendOwner(context.Context, string) error { return nil }

// echoGraph builds start -> bash node id running "echo <id>".
func echoGraph(ids ...string) Graph {
	g := Graph{Nodes: []Node{wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true})}}
	prev := "start"
	for _, id := range ids {
		g.Nodes = append(g.Nodes, wfNode(id, "bash", id, map[string]any{"command": "echo " + id}))
		g.Edges = append(g.Edges, wfEdge(prev, "out", id))
		prev = id
	}
	return g
}

func graphHasNode(g Graph, id string) bool {
	for _, n := range g.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

func TestSaveCandidateInvalidGraphStoresNothing(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	ctx := context.Background()
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, echoGraph("a"))

	bad := echoGraph("a")
	bad.Nodes = append(bad.Nodes, wfNode("x", "no.such.type", "X", nil))
	bad.Edges = append(bad.Edges, wfEdge("a", "out", "x"))
	c, change, issues, err := svc.SaveCandidate(ctx, "w", bad, "broken", "guardian")
	if err != nil {
		t.Fatalf("err=%v want nil", err)
	}
	if len(issues) == 0 {
		t.Fatal("no issues for an invalid graph")
	}
	if c.ID != 0 || change.FromVersion != 0 {
		t.Fatalf("candidate=%+v change=%+v want zero", c, change)
	}
	if _, ok, err := svc.OpenCandidate(ctx, "w"); err != nil || ok {
		t.Fatalf("open candidate ok=%v err=%v want none", ok, err)
	}
	if _, _, _, err := svc.SaveCandidate(ctx, "missing", echoGraph(), "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown workflow err=%v want ErrNotFound", err)
	}
	if _, _, err := svc.OpenCandidate(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open unknown workflow err=%v want ErrNotFound", err)
	}
}

func TestSaveCandidateSupersedesAndDiffs(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	ctx := context.Background()
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, echoGraph("a"))

	first, _, issues, err := svc.SaveCandidate(ctx, "w", echoGraph("a", "b"), "add b", "")
	if err != nil || len(issues) > 0 {
		t.Fatalf("save: issues=%v err=%v", issues, err)
	}
	if first.Author != "guardian" || first.BaseVersion != 1 || first.Status != CandidateOpen {
		t.Fatalf("first=%+v", first)
	}
	second, change, issues, err := svc.SaveCandidate(ctx, "w", echoGraph("a", "c"), "add c", "guardian")
	if err != nil || len(issues) > 0 {
		t.Fatalf("save 2: issues=%v err=%v", issues, err)
	}
	if change.FromVersion != 1 || change.ToVersion != 0 || change.Summary != "add c" ||
		len(change.Added) != 1 || change.Added[0] != "c" {
		t.Fatalf("change=%+v want c added from v1", change)
	}
	if old, _ := st.GetCandidate(ctx, first.ID); old.Status != CandidateSuperseded {
		t.Fatalf("first status=%q want superseded", old.Status)
	}
	open, ok, err := svc.OpenCandidate(ctx, "w")
	if err != nil || !ok || open.ID != second.ID {
		t.Fatalf("open=%+v ok=%v err=%v want #%d", open, ok, err, second.ID)
	}
	if wf, _ := st.GetWorkflow(ctx, "w"); wf.Version != 1 {
		t.Fatalf("version=%d want 1: a candidate is not a version", wf.Version)
	}
}

func TestTestCandidateRunsCandidateGraph(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	svc := newTestService(t, st, deps, &testClock{})
	ctx := context.Background()
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, echoGraph("current"))

	// Without a candidate the current version runs, candidate_id 0.
	plain, err := svc.TestCandidate(ctx, "w", nil)
	if err != nil {
		t.Fatalf("test current: %v", err)
	}
	svc.engine.Wait()
	if plain.Trigger != "test" || plain.CandidateID != 0 || plain.Version != 1 {
		t.Fatalf("plain run=%+v", plain)
	}
	if got := mustRun(t, st, plain.ID); got.Status != RunSucceeded {
		t.Fatalf("plain run=%+v want succeeded", got)
	}
	mustNode(t, st, plain.ID, "current", -1)

	c, _, issues, err := svc.SaveCandidate(ctx, "w", echoGraph("cand", "more"), "try", "guardian")
	if err != nil || len(issues) > 0 {
		t.Fatalf("save: issues=%v err=%v", issues, err)
	}
	run, err := svc.TestCandidate(ctx, "w", []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("test candidate: %v", err)
	}
	svc.engine.Wait()
	if run.Trigger != "test" || run.CandidateID != c.ID || run.Version != c.BaseVersion || run.NodesTotal != 3 {
		t.Fatalf("run=%+v want trigger test, candidate %d, version %d, 3 nodes", run, c.ID, c.BaseVersion)
	}
	got := mustRun(t, st, run.ID)
	if got.Status != RunSucceeded || got.CandidateID != c.ID {
		t.Fatalf("run=%+v want succeeded candidate run", got)
	}
	if nr := mustNode(t, st, run.ID, "more", -1); nr.Status != NodeSucceeded {
		t.Fatalf("candidate node=%+v", nr)
	}

	// RunDetail shows the graph the run ran, even after the candidate moved on.
	if err := svc.DiscardCandidate(ctx, "w"); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.RunDetail(ctx, "w", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !graphHasNode(detail.Graph, "cand") || graphHasNode(detail.Graph, "current") {
		t.Fatalf("detail graph=%+v want the candidate graph", detail.Graph)
	}
	plainDetail, err := svc.RunDetail(ctx, "w", plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !graphHasNode(plainDetail.Graph, "current") {
		t.Fatalf("plain detail graph=%+v want the version graph", plainDetail.Graph)
	}
}

func TestTestCandidateRefusesWhileRunning(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.blockStarted = make(chan struct{}, 1)
	svc := newTestService(t, st, deps, &testClock{})
	ctx := context.Background()
	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true}),
			wfNode("b", "bash", "Block", map[string]any{"command": "block"}),
		},
		Edges: []Edge{wfEdge("start", "out", "b")},
	}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	if _, err := svc.StartRun(ctx, "w", "manual", nil); err != nil {
		t.Fatal(err)
	}
	<-deps.blockStarted
	if _, err := svc.TestCandidate(ctx, "w", nil); !errors.Is(err, ErrRunActive) {
		t.Fatalf("err=%v want ErrRunActive", err)
	}
}

// REQ-WFC-005: a failed candidate run leaves ConsecutiveFailures and the status
// alone, asks no guardian, records no decision and notifies nobody; the same
// failure in a normal run does all of that.
func TestCandidateRunFailureLeavesWorkflowAlone(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.bashSeq["bad"] = []BashResult{{ExitCode: 1, Output: "boom"}}
	notifier := &candNotifier{}
	svc := newTestService(t, st, deps, &testClock{})
	svc.SetNotifier(notifier)
	ctx := context.Background()

	failing := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true}),
			wfNode("bad", "bash", "Bad", map[string]any{"command": "bad"}),
		},
		Edges: []Edge{wfEdge("start", "out", "bad")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g", Status: StatusActive}, echoGraph("ok"))
	wf.ConsecutiveFailures = 2
	if err := st.UpdateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}

	if _, _, issues, err := svc.SaveCandidate(ctx, "w", failing, "fails", "guardian"); err != nil || len(issues) > 0 {
		t.Fatalf("save: issues=%v err=%v", issues, err)
	}
	run, err := svc.TestCandidate(ctx, "w", nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.engine.Wait()

	got := mustRun(t, st, run.ID)
	if got.Status != RunFailed || !strings.Contains(got.Error, "Bad") {
		t.Fatalf("run=%+v want failed with the node error", got)
	}
	if nr := mustNode(t, st, run.ID, "bad", -1); nr.Status != NodeFailed {
		t.Fatalf("node=%+v want failed", nr)
	}
	after, _ := st.GetWorkflow(ctx, "w")
	if after.ConsecutiveFailures != 2 || after.Status != StatusActive {
		t.Fatalf("workflow=%+v want 2 failures, active", after)
	}
	if decs, _ := st.ListDecisions(ctx, DecisionFilter{RunID: run.ID}); len(decs) != 0 {
		t.Fatalf("decisions=%+v want none", decs)
	}
	if n := deps.agentCount(); n != 0 {
		t.Fatalf("agent calls=%d want 0 (no guardian diagnosis)", n)
	}
	if got := notifier.log.snapshot(); len(got) != 0 {
		t.Fatalf("notices=%+v want none", got)
	}

	// Contrast: publish and run the same graph normally; it counts and notifies.
	if _, _, err := svc.PublishCandidate(ctx, "w"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	normal, err := svc.StartRun(ctx, "w", "manual", nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.engine.Wait()
	if got := mustRun(t, st, normal.ID); got.Status != RunFailed {
		t.Fatalf("normal run=%+v want failed", got)
	}
	final, _ := st.GetWorkflow(ctx, "w")
	if final.ConsecutiveFailures != 3 {
		t.Fatalf("normal run failures=%d want 3", final.ConsecutiveFailures)
	}
	if deps.agentCount() == 0 {
		t.Fatal("normal run did not call the guardian")
	}
	if len(notifier.log.snapshot()) == 0 {
		t.Fatal("normal run sent no notice")
	}
}

// REQ-WFC-005: a Script Parse script learned during a candidate run patches the
// candidate and saves no version.
func TestCandidateRunScriptLearnPatchesCandidate(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	learned := "function parse(input, ctx) { return {title: 'hi'}; }"
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: extract the title", text: `{"title":"hi"}`, tokens: 12},
		{agent: "g", contains: "Goal: extract the title", text: `{"feasible":true,"script":"` + learned + `"}`, tokens: 20},
	}
	notifier := &candNotifier{}
	svc := newTestService(t, st, deps, &testClock{})
	svc.SetNotifier(notifier)
	ctx := context.Background()
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, echoGraph("a"))

	g := scriptGraph(scriptParams(map[string]any{"script": "", "fallback_agent": "fallback-bot"}))
	g.Nodes[0].Params = map[string]any{"enabled": true}
	c, _, issues, err := svc.SaveCandidate(ctx, "w", g, "parse", "guardian")
	if err != nil || len(issues) > 0 {
		t.Fatalf("save: issues=%v err=%v", issues, err)
	}
	run, err := svc.TestCandidate(ctx, "w", nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.engine.Wait()

	if nr := mustNode(t, st, run.ID, "parse", -1); nr.Status != NodeSucceeded || nr.Via != ViaFallback {
		t.Fatalf("node=%+v want succeeded via fallback", nr)
	}
	decs, _ := st.ListDecisions(ctx, DecisionFilter{RunID: run.ID})
	if len(decs) != 1 || decs[0].Action != "script_update" {
		t.Fatalf("decisions=%+v want one script_update", decs)
	}
	wf, _ := st.GetWorkflow(ctx, "w")
	versions, _ := st.ListVersions(ctx, "w")
	if wf.Version != 1 || len(versions) != 1 {
		t.Fatalf("version=%d versions=%d want 1/1: no version may be saved", wf.Version, len(versions))
	}
	patched, err := st.GetCandidate(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var script string
	var rev int64
	for _, n := range patched.Graph.Nodes {
		if n.ID == "parse" {
			script, _ = n.Params["script"].(string)
			rev = scriptRev(n.Params)
		}
	}
	if script != learned || rev != 1 || patched.Status != CandidateOpen {
		t.Fatalf("candidate script=%q rev=%d status=%q want learned rev 1 open", script, rev, patched.Status)
	}
	if got := notifier.log.snapshot(); len(got) != 0 {
		t.Fatalf("notices=%+v want none for a candidate run", got)
	}

	// Reverting that script_update patches the candidate as well.
	if _, err := svc.Answer(ctx, "w", decs[0].ID, "revert"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	reverted, _ := st.GetCandidate(ctx, c.ID)
	for _, n := range reverted.Graph.Nodes {
		if n.ID == "parse" {
			if s, _ := n.Params["script"].(string); s != "" || scriptRev(n.Params) != 2 {
				t.Fatalf("reverted params=%v want empty script rev 2", n.Params)
			}
		}
	}
	if versions, _ := st.ListVersions(ctx, "w"); len(versions) != 1 {
		t.Fatalf("versions=%d after revert want 1", len(versions))
	}
}

// REQ-WFC-006: publishing saves the candidate as a guardian version, marks it
// published, returns the change and drops an active workflow whose lock no
// longer covers the graph to draft.
func TestPublishCandidate(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	ctx := context.Background()
	start := Graph{Nodes: []Node{wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true})}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g", Status: StatusActive, Lock: &Lock{Version: 1}}, start)

	if _, _, err := svc.PublishCandidate(ctx, "w"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("publish without candidate err=%v want ErrNotFound", err)
	}
	c, _, _, err := svc.SaveCandidate(ctx, "w", echoGraph("a"), "add a", "guardian")
	if err != nil {
		t.Fatal(err)
	}
	change, ver, err := svc.PublishCandidate(ctx, "w")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if ver.Version != 2 || ver.Author != "guardian" || ver.Summary != "add a" || !graphHasNode(ver.Graph, "a") {
		t.Fatalf("version=%+v", ver)
	}
	if change.FromVersion != 1 || change.ToVersion != 2 || change.Summary != "add a" ||
		len(change.Added) != 1 || change.Added[0] != "a" {
		t.Fatalf("change=%+v", change)
	}
	got, _ := st.GetCandidate(ctx, c.ID)
	if got.Status != CandidatePublished || got.PublishedVersion != 2 {
		t.Fatalf("candidate=%+v want published v2", got)
	}
	if _, ok, _ := svc.OpenCandidate(ctx, "w"); ok {
		t.Fatal("a published candidate is still open")
	}
	wf, _ := st.GetWorkflow(ctx, "w")
	if wf.Version != 2 || wf.Status != StatusDraft {
		t.Fatalf("workflow=%+v want v2 draft (bash is not locked)", wf)
	}
}

// REQ-WFC-006: publishing refuses with a version conflict when the current
// version moved; the candidate stays open.
func TestPublishCandidateConflict(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	ctx := context.Background()
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g", Status: StatusDraft}, echoGraph("a"))

	c, _, _, err := svc.SaveCandidate(ctx, "w", echoGraph("a", "b"), "add b", "guardian")
	if err != nil {
		t.Fatal(err)
	}
	edited := echoGraph("a", "owner")
	if _, err := svc.Update(ctx, "w", UpdateInput{Graph: &edited, BaseVersion: 1}); err != nil {
		t.Fatalf("owner edit: %v", err)
	}
	if _, _, err := svc.PublishCandidate(ctx, "w"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("publish err=%v want ErrVersionConflict", err)
	}
	open, ok, _ := svc.OpenCandidate(ctx, "w")
	if !ok || open.ID != c.ID {
		t.Fatalf("open=%+v ok=%v want candidate %d still open", open, ok, c.ID)
	}
	if wf, _ := st.GetWorkflow(ctx, "w"); wf.Version != 2 {
		t.Fatalf("version=%d want 2 (the owner edit)", wf.Version)
	}
}

func TestDiscardCandidate(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	ctx := context.Background()
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, echoGraph("a"))

	if err := svc.DiscardCandidate(ctx, "w"); err != nil {
		t.Fatalf("discard without candidate: %v", err)
	}
	c, _, _, err := svc.SaveCandidate(ctx, "w", echoGraph("a", "b"), "add b", "guardian")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DiscardCandidate(ctx, "w"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := svc.OpenCandidate(ctx, "w"); ok {
		t.Fatal("discarded candidate is still open")
	}
	if got, _ := st.GetCandidate(ctx, c.ID); got.Status != CandidateDiscarded {
		t.Fatalf("status=%q want discarded", got.Status)
	}
	if err := svc.DiscardCandidate(ctx, "w"); err != nil {
		t.Fatalf("second discard: %v", err)
	}
	if wf, _ := st.GetWorkflow(ctx, "w"); wf.Version != 1 {
		t.Fatalf("version=%d want 1", wf.Version)
	}
}

func TestAssistantSessionAndAppendChat(t *testing.T) {
	st := NewMemStore()
	clk := &testClock{}
	svc := newTestService(t, st, newFakeDeps(), clk)
	ctx := context.Background()
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, echoGraph("a"))
	seedWorkflow(t, st, Workflow{ID: "v", Name: "V", Guardian: "g"}, echoGraph("a"))

	if _, ok := svc.AssistantWorkflow(ctx, ""); ok {
		t.Fatal("empty session matched a workflow")
	}
	if _, ok := svc.AssistantWorkflow(ctx, "sess-1"); ok {
		t.Fatal("unknown session matched a workflow")
	}
	if err := svc.SetAssistantSession(ctx, "w", "sess-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetAssistantSession(ctx, "missing", "sess-x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown workflow err=%v", err)
	}
	wf, ok := svc.AssistantWorkflow(ctx, "sess-1")
	if !ok || wf.ID != "w" || wf.AssistantSession != "sess-1" {
		t.Fatalf("assistant workflow=%+v ok=%v", wf, ok)
	}

	msg, err := svc.AppendChat(ctx, ChatMessage{
		WorkflowID: "w", Role: "guardian", Text: "published",
		Change: &GraphChange{FromVersion: 1, ToVersion: 2}, SessionID: "sess-1", TurnID: "turn-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.ID == 0 || msg.CreatedAt != clk.Now().UnixMilli() {
		t.Fatalf("msg=%+v", msg)
	}
	chat, _ := svc.Chat(ctx, "w")
	if len(chat) != 1 || chat[0].SessionID != "sess-1" || chat[0].TurnID != "turn-1" || chat[0].Change == nil {
		t.Fatalf("chat=%+v", chat)
	}
}
