package workflow

// Tests for the run engine (REQ-WF-004..009). They use MemStore and a fake Deps
// with scripted replies and an instant Sleep. Every test joins the run
// goroutine with Engine.Wait, so the suite is race clean.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- fake Deps ------------------------------------------------------------

// fakeReply scripts one RunAgent answer, matched by agent and prompt substring.
type fakeReply struct {
	agent    string
	contains string
	text     string
	err      error
	tokens   int64
	stop     string
}

type fakeDeps struct {
	mu         sync.Mutex
	replies    []fakeReply
	agentCalls []AgentRequest

	bashSeq   map[string][]BashResult
	bashCalls []BashRequest

	// blockStarted is signaled when the "block" bash command is entered, so a
	// test can cancel a run that is provably inside a node handler.
	blockStarted chan struct{}

	browse    map[string]string
	web       map[string][2]string
	tools     map[string]string
	knowledge []KnowledgeRequest
	toolRisk  map[string]string
}

func newFakeDeps() *fakeDeps {
	return &fakeDeps{
		bashSeq:  map[string][]BashResult{},
		browse:   map[string]string{},
		web:      map[string][2]string{},
		tools:    map[string]string{},
		toolRisk: map[string]string{},
	}
}

func (f *fakeDeps) RunAgent(_ context.Context, req AgentRequest) (AgentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agentCalls = append(f.agentCalls, req)
	for _, r := range f.replies {
		if r.agent != "" && r.agent != req.Agent {
			continue
		}
		if r.contains != "" && !strings.Contains(req.Prompt, r.contains) {
			continue
		}
		return AgentResult{Text: r.text, Tokens: r.tokens, StopReason: r.stop, SessionID: "sess-" + req.Agent}, r.err
	}
	return AgentResult{}, errors.New("fake: no scripted reply")
}

func (f *fakeDeps) CallTool(_ context.Context, req ToolRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tools[req.Name], nil
}

func (f *fakeDeps) Bash(ctx context.Context, req BashRequest) (BashResult, error) {
	if req.Command == "block" {
		if f.blockStarted != nil {
			select {
			case f.blockStarted <- struct{}{}:
			default:
			}
		}
		<-ctx.Done()
		return BashResult{}, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bashCalls = append(f.bashCalls, req)
	if seq := f.bashSeq[req.Command]; len(seq) > 0 {
		res := seq[0]
		if len(seq) > 1 {
			f.bashSeq[req.Command] = seq[1:]
		}
		return res, nil
	}
	if strings.HasPrefix(req.Command, "echo ") {
		return BashResult{ExitCode: 0, Output: strings.TrimPrefix(req.Command, "echo ")}, nil
	}
	return BashResult{ExitCode: 0}, nil
}

func (f *fakeDeps) WebFetch(_ context.Context, url string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if w, ok := f.web[url]; ok {
		return w[0], w[1], nil
	}
	return "", "", nil
}

func (f *fakeDeps) Browse(_ context.Context, req BrowseRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.browse[req.URL], nil
}

func (f *fakeDeps) SaveKnowledge(_ context.Context, req KnowledgeRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.knowledge = append(f.knowledge, req)
	return nil
}

func (f *fakeDeps) AgentExists(string) bool { return true }

func (f *fakeDeps) ToolRisk(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.toolRisk[name]
}

func (f *fakeDeps) agentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.agentCalls)
}

// ---- test helpers ---------------------------------------------------------

// noticeLog captures owner notices and messages under a mutex.
type noticeLog struct {
	mu      sync.Mutex
	notices []Notice
	sent    []string
}

func (n *noticeLog) notify(_ context.Context, nt Notice) {
	n.mu.Lock()
	n.notices = append(n.notices, nt)
	n.mu.Unlock()
}

func (n *noticeLog) sendOwner(_ context.Context, text string) error {
	n.mu.Lock()
	n.sent = append(n.sent, text)
	n.mu.Unlock()
	return nil
}

func (n *noticeLog) snapshot() []Notice {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Notice(nil), n.notices...)
}

func (n *noticeLog) sentTexts() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.sent...)
}

// durationLog captures Sleep durations under a mutex.
type durationLog struct {
	mu sync.Mutex
	d  []time.Duration
}

func (l *durationLog) sleep(_ context.Context, d time.Duration) error {
	l.mu.Lock()
	l.d = append(l.d, d)
	l.mu.Unlock()
	return nil
}

func (l *durationLog) all() []time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]time.Duration(nil), l.d...)
}

// testClock is a manually advanced clock.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func wfNode(id, typ, name string, params map[string]any) Node {
	return Node{ID: id, Type: typ, Name: name, Params: params}
}

func wfEdge(from, port, to string) Edge {
	return Edge{ID: from + ":" + port + "->" + to, From: from, FromPort: port, To: to}
}

func seedWorkflow(t *testing.T, st *MemStore, wf Workflow, g Graph) Workflow {
	t.Helper()
	ctx := context.Background()
	if wf.Status == "" {
		wf.Status = StatusActive
	}
	if wf.CreatedAt == 0 {
		wf.CreatedAt = 1
	}
	created, err := st.CreateWorkflow(ctx, wf, g, "owner", "initial")
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	return created
}

func seedRun(t *testing.T, st *MemStore, wf Workflow, trigger string, input any) Run {
	t.Helper()
	run, err := st.CreateRun(context.Background(), Run{
		WorkflowID: wf.ID, Version: wf.Version, Trigger: trigger, Status: RunQueued, Input: nodeJSON(input),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return run
}

// testEngine builds an engine with an instant Sleep unless the caller set one.
func testEngine(st *MemStore, deps Deps, opts EngineOptions) *Engine {
	if opts.Sleep == nil {
		opts.Sleep = func(context.Context, time.Duration) error { return nil }
	}
	return NewEngine(st, deps, opts)
}

func mustRun(t *testing.T, st *MemStore, id string) Run {
	t.Helper()
	r, err := st.GetRun(context.Background(), id)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	return r
}

func mustNode(t *testing.T, st *MemStore, runID, nodeID string, item int) NodeRun {
	t.Helper()
	nrs, _ := st.ListNodeRuns(context.Background(), runID)
	for _, nr := range nrs {
		if nr.NodeID == nodeID && nr.ItemIndex == item {
			return nr
		}
	}
	t.Fatalf("node run %s#%d not found", nodeID, item)
	return NodeRun{}
}

func pendingDecision(t *testing.T, st *MemStore, runID string) Decision {
	t.Helper()
	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{RunID: runID, Status: "pending"})
	if len(decs) == 0 {
		t.Fatalf("no pending decision for run %s", runID)
	}
	return decs[0]
}

func stateRaw(t *testing.T, st *MemStore, wfID, key string) (json.RawMessage, bool) {
	t.Helper()
	entries, _ := st.ListState(context.Background(), wfID)
	for _, en := range entries {
		if en.Key == key {
			return en.Value, true
		}
	}
	return nil, false
}

func decodeAny(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

// ---- AC-WF-006 ------------------------------------------------------------

func TestEngineIfBranchSkips(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{{contains: "fetch the status", text: `{"status":200}`}}

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true}),
			wfNode("fetch", "agent", "Fetch", map[string]any{"prompt": "fetch the status", "output_schema": map[string]any{"status": "integer"}}),
			wfNode("check", "if", "Check", map[string]any{"left": "{{ nodes.fetch.output.status }}", "op": "==", "right": 200}),
			wfNode("send", "state_set", "Send", map[string]any{"key": "sent", "value": true}),
			wfNode("log", "state_set", "Log", map[string]any{"key": "logged", "value": true}),
		},
		Edges: []Edge{
			wfEdge("start", "out", "fetch"),
			wfEdge("fetch", "out", "check"),
			wfEdge("check", "true", "send"),
			wfEdge("check", "false", "log"),
		},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", map[string]any{})

	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v", got)
	}
	if got := mustNode(t, st, run.ID, "send", -1).Status; got != NodeSucceeded {
		t.Fatalf("send=%s want succeeded", got)
	}
	if got := mustNode(t, st, run.ID, "log", -1).Status; got != NodeSkipped {
		t.Fatalf("log=%s want skipped", got)
	}
	if _, ok := stateRaw(t, st, wf.ID, "sent"); !ok {
		t.Fatal("state sent was not written")
	}
	if _, ok := stateRaw(t, st, wf.ID, "logged"); ok {
		t.Fatal("state logged was written on the dead branch")
	}
}

// ---- AC-WF-007 ------------------------------------------------------------

func TestEngineArrayParamType(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{{contains: "list them", text: `{"list":[1,2,3]}`}}

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("a", "agent", "A", map[string]any{"prompt": "list them", "output_schema": map[string]any{"list": "array"}}),
			wfNode("b", "state_set", "B", map[string]any{"key": "x", "value": "{{ nodes.a.output.list }}"}),
		},
		Edges: []Edge{wfEdge("start", "out", "a"), wfEdge("a", "out", "b")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)

	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	val, ok := stateRaw(t, st, wf.ID, "x")
	if !ok {
		t.Fatal("state x not written")
	}
	if string(val) != "[1,2,3]" {
		t.Fatalf("state x=%s want the JSON array", val)
	}
}

// ---- AC-WF-008 ------------------------------------------------------------

func TestEngineLoopBashEcho(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("each", "loop", "Each", map[string]any{"items": []any{"a", "b", "c"}}),
			{ID: "run", Type: "bash", Name: "Run", Parent: "each", Params: map[string]any{"command": "echo {{ item }}"}},
		},
		Edges: []Edge{
			wfEdge("start", "out", "each"),
			{ID: "body", From: "each", FromPort: "body", To: "run"},
		},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)

	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v", got)
	}
	out := decodeAny(t, mustNode(t, st, run.ID, "each", -1).Output).(map[string]any)
	items, _ := out["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("loop items=%v want 3", items)
	}
	for i, want := range []string{"a", "b", "c"} {
		m, ok := items[i].(map[string]any)
		if !ok || m["output"] != want {
			t.Fatalf("item %d=%v want output %q", i, items[i], want)
		}
	}
	if out["count"] != float64(3) || out["ok"] != float64(3) || out["skipped"] != float64(0) {
		t.Fatalf("loop summary=%v", out)
	}
}

// ---- AC-WF-009 ------------------------------------------------------------

func TestEngineSchemaOk(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{{contains: "score it", text: `{"score":8}`}}

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("a", "agent", "A", map[string]any{"prompt": "score it", "output_schema": map[string]any{"score": "integer"}}),
		},
		Edges: []Edge{wfEdge("start", "out", "a")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)

	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v", got)
	}
	out := decodeAny(t, mustNode(t, st, run.ID, "a", -1).Output).(map[string]any)
	if out["score"] != float64(8) {
		t.Fatalf("output=%v want score 8", out)
	}
}

func TestEngineSchemaError(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{{contains: "score it", text: "it is probably fine"}}

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			{ID: "a", Type: "agent", Name: "A",
				Params:    map[string]any{"prompt": "score it", "output_schema": map[string]any{"score": "integer"}},
				OnFailure: &FailurePolicy{Mode: "stop"}},
		},
		Edges: []Edge{wfEdge("start", "out", "a")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)

	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	if got := mustRun(t, st, run.ID); got.Status != RunFailed {
		t.Fatalf("run=%+v want failed", got)
	}
	nr := mustNode(t, st, run.ID, "a", -1)
	if nr.Status != NodeFailed || !strings.Contains(nr.Error, "schema_error") {
		t.Fatalf("node=%+v want schema_error", nr)
	}
	if deps.agentCount() != 1 {
		t.Fatalf("agent calls=%d want 1", deps.agentCount())
	}
}

// ---- AC-WF-010 ------------------------------------------------------------

func TestEngineStateAcrossRuns(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	var g Graph
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("set", "state_set", "Set", map[string]any{"key": "last_run_at", "value": "T1"}),
		},
		Edges: []Edge{wfEdge("start", "out", "set")},
	})
	g = Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("get", "state_get", "Get", map[string]any{"key": "last_run_at"}),
		},
		Edges: []Edge{wfEdge("start", "out", "get")},
	}
	// Save the second graph as version 2 so the get run reads it.
	if _, err := st.SaveVersion(context.Background(), wf.ID, wf.Version, g, "owner", "read state"); err != nil {
		t.Fatalf("save version: %v", err)
	}
	wf, _ = st.GetWorkflow(context.Background(), wf.ID)

	eng := testEngine(st, deps, EngineOptions{})

	run1 := seedRun(t, st, wf, "manual", nil)
	run1.Version = 1
	if err := st.UpdateRun(context.Background(), run1); err != nil {
		t.Fatalf("update run1: %v", err)
	}
	eng.Launch(context.Background(), run1.ID)
	eng.Wait()

	run2 := seedRun(t, st, wf, "manual", nil)
	eng.Launch(context.Background(), run2.ID)
	eng.Wait()

	if got := mustRun(t, st, run2.ID); got.Status != RunSucceeded {
		t.Fatalf("run2=%+v", got)
	}
	out := decodeAny(t, mustNode(t, st, run2.ID, "get", -1).Output).(map[string]any)
	if out["value"] != "T1" || out["exists"] != true {
		t.Fatalf("get output=%v want value T1", out)
	}
}

// ---- AC-WF-011 ------------------------------------------------------------

func TestEngineResumeSkipsFinishedNodes(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	ctx := context.Background()

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("a", "message", "A", map[string]any{"text": "a"}),
			wfNode("b", "message", "B", map[string]any{"text": "b"}),
			wfNode("c", "message", "C", map[string]any{"text": "c"}),
		},
		Edges: []Edge{wfEdge("start", "out", "a"), wfEdge("a", "out", "b"), wfEdge("b", "out", "c")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)

	// Hand-build the rows of the first two nodes as already done.
	if err := st.PutNodeRun(ctx, NodeRun{RunID: run.ID, NodeID: "start", ItemIndex: -1, Status: NodeSucceeded}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutNodeRun(ctx, NodeRun{RunID: run.ID, NodeID: "a", ItemIndex: -1, Status: NodeSucceeded}); err != nil {
		t.Fatal(err)
	}
	run.Status = RunRunning
	if err := st.UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	notices := &noticeLog{}
	eng := testEngine(st, deps, EngineOptions{SendOwner: notices.sendOwner, Notify: notices.notify})
	eng.Launch(ctx, run.ID)
	eng.Wait()

	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v", got)
	}
	sent := notices.sentTexts()
	if len(sent) != 2 || sent[0] != "b" || sent[1] != "c" {
		t.Fatalf("sent=%v want [b c]", sent)
	}
}

// ---- AC-WF-012 ------------------------------------------------------------

func TestEngineRetriesTransient(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.bashSeq["flaky"] = []BashResult{{ExitCode: 1, Output: "timeout"}, {ExitCode: 1, Output: "timeout"}, {ExitCode: 0, Output: "ok"}}

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("run", "bash", "Run", map[string]any{"command": "flaky"}),
		},
		Edges: []Edge{wfEdge("start", "out", "run")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)

	sleeps := &durationLog{}
	eng := testEngine(st, deps, EngineOptions{Sleep: sleeps.sleep})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v", got)
	}
	nr := mustNode(t, st, run.ID, "run", -1)
	if len(nr.Attempts) != 3 {
		t.Fatalf("attempts=%d want 3", len(nr.Attempts))
	}
	if deps.agentCount() != 0 {
		t.Fatalf("guardian was called %d times want 0", deps.agentCount())
	}
	got := sleeps.all()
	if len(got) != 2 || got[0] != 10*time.Second || got[1] != 30*time.Second {
		t.Fatalf("sleeps=%v want [10s 30s]", got)
	}
}

// ---- AC-WF-013 ------------------------------------------------------------

func TestEngineGuardianSkip(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.bashSeq["bad"] = []BashResult{{ExitCode: 1, Output: "boom no good"}}
	deps.replies = []fakeReply{{
		agent: "g", contains: "You are the guardian",
		text:   `{"action":"skip","reason":"optional step","confidence":0.9}`,
		tokens: 42,
	}}

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("bad", "bash", "Bad", map[string]any{"command": "bad"}),
		},
		Edges: []Edge{wfEdge("start", "out", "bad")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)

	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v want succeeded", got)
	}
	nr := mustNode(t, st, run.ID, "bad", -1)
	if nr.Status != NodeSkipped {
		t.Fatalf("node=%s want skipped", nr.Status)
	}
	if nr.Error == "" {
		t.Fatal("skipped node lost its error")
	}
	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: wf.ID, RunID: run.ID})
	if len(decs) != 1 {
		t.Fatalf("decisions=%d want 1", len(decs))
	}
	d := decs[0]
	if d.Kind != "guardian" || d.Action != "skip" || d.Tokens != 42 || d.Status != "applied" {
		t.Fatalf("decision=%+v", d)
	}
}

// ---- AC-WF-014 ------------------------------------------------------------

// guardianAskWorkflow builds a workflow whose only node always fails and whose
// guardian answers "ask" with a fix that renames the command.
func guardianAskWorkflow(t *testing.T) (*MemStore, *fakeDeps, Workflow, Run, *noticeLog) {
	t.Helper()
	st := NewMemStore()
	deps := newFakeDeps()
	deps.bashSeq["needsfix"] = []BashResult{{ExitCode: 1, Output: "boom"}}
	deps.bashSeq["needsfix2"] = []BashResult{{ExitCode: 0, Output: "ok"}}
	deps.replies = []fakeReply{{
		agent: "g", contains: "You are the guardian",
		text: `{"action":"retry_fix","reason":"rename the command","fix":{"command":"needsfix2"},"persistent":true,"confidence":0.9}`,
	}}

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("bad", "bash", "Bad", map[string]any{"command": "needsfix"}),
		},
		Edges: []Edge{wfEdge("start", "out", "bad")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)
	notices := &noticeLog{}
	eng := testEngine(st, deps, EngineOptions{Notify: notices.notify})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()
	if got := mustRun(t, st, run.ID); got.Status != RunWaiting || got.WaitingReason != "owner" {
		t.Fatalf("run=%+v want waiting/owner", got)
	}
	return st, deps, wf, run, notices
}

func TestEngineGuardianAskApply(t *testing.T) {
	st, deps, wf, run, notices := guardianAskWorkflow(t)
	ctx := context.Background()
	dec := pendingDecision(t, st, run.ID)
	if dec.Action != "ask" || dec.Status != "pending" {
		t.Fatalf("decision=%+v", dec)
	}
	if dec.Fix["command"] != "needsfix2" {
		t.Fatalf("fix=%v", dec.Fix)
	}
	if len(notices.snapshot()) == 0 {
		t.Fatal("owner was not notified of the ask")
	}

	eng := testEngine(st, deps, EngineOptions{Notify: notices.notify})
	if _, err := eng.Answer(ctx, dec.ID, "apply"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	eng.Wait()

	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v want succeeded", got)
	}
	wf2, _ := st.GetWorkflow(ctx, wf.ID)
	if wf2.Version != 2 {
		t.Fatalf("version=%d want 2", wf2.Version)
	}
	v2, err := st.GetVersion(ctx, wf.ID, 2)
	if err != nil {
		t.Fatalf("get version 2: %v", err)
	}
	if v2.Author != "guardian" || !strings.Contains(v2.Summary, "command") {
		t.Fatalf("version 2=%+v", v2)
	}
	for _, n := range v2.Graph.Nodes {
		if n.ID == "bad" && n.Params["command"] != "needsfix2" {
			t.Fatalf("saved node params=%v", n.Params)
		}
	}
	got := mustRun(t, st, run.ID)
	if got.Overrides["bad"]["command"] != "needsfix2" {
		t.Fatalf("overrides=%v", got.Overrides)
	}
}

func TestEngineGuardianAskRunOnly(t *testing.T) {
	st, deps, wf, run, notices := guardianAskWorkflow(t)
	ctx := context.Background()
	dec := pendingDecision(t, st, run.ID)

	eng := testEngine(st, deps, EngineOptions{Notify: notices.notify})
	if _, err := eng.Answer(ctx, dec.ID, "run_only"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	eng.Wait()

	got := mustRun(t, st, run.ID)
	if got.Status != RunSucceeded {
		t.Fatalf("run=%+v want succeeded", got)
	}
	if got.Overrides["bad"]["command"] != "needsfix2" {
		t.Fatalf("overrides=%v", got.Overrides)
	}
	wf2, _ := st.GetWorkflow(ctx, wf.ID)
	if wf2.Version != 1 {
		t.Fatalf("version=%d want 1 (no new version)", wf2.Version)
	}
}

func TestEngineGuardianAskStop(t *testing.T) {
	st, deps, _, run, notices := guardianAskWorkflow(t)
	ctx := context.Background()
	dec := pendingDecision(t, st, run.ID)

	eng := testEngine(st, deps, EngineOptions{Notify: notices.notify})
	got, err := eng.Answer(ctx, dec.ID, "stop")
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	eng.Wait()

	if got.Status != RunFailed || got.Error != "stopped by owner" {
		t.Fatalf("run=%+v", got)
	}
}

// ---- AC-WF-015 ------------------------------------------------------------

func TestEngineWorkflowErrorAfterThreeFailures(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.bashSeq["always"] = []BashResult{{ExitCode: 1, Output: "nope"}}

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			{ID: "bad", Type: "bash", Name: "Bad", Params: map[string]any{"command": "always"}, OnFailure: &FailurePolicy{Mode: "stop"}},
		},
		Edges: []Edge{wfEdge("start", "out", "bad")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	notices := &noticeLog{}
	eng := testEngine(st, deps, EngineOptions{Notify: notices.notify})

	for i := 0; i < 3; i++ {
		run := seedRun(t, st, wf, "manual", nil)
		eng.Launch(context.Background(), run.ID)
		eng.Wait()
		if got := mustRun(t, st, run.ID); got.Status != RunFailed {
			t.Fatalf("run %d=%+v want failed", i+1, got)
		}
	}

	wf2, _ := st.GetWorkflow(context.Background(), wf.ID)
	if wf2.Status != StatusError || wf2.ConsecutiveFailures != 3 {
		t.Fatalf("workflow=%+v want error/3", wf2)
	}
	paused := false
	for _, n := range notices.snapshot() {
		if strings.Contains(n.Text, "paused after 3 failed runs") {
			paused = true
		}
	}
	if !paused {
		t.Fatal("no paused notice was sent")
	}
}

// ---- approval -------------------------------------------------------------

func approvalGraph() Graph {
	return Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("appr", "approval", "Approve", map[string]any{"message": "go?"}),
			wfNode("yes", "state_set", "Yes", map[string]any{"key": "approved", "value": true}),
			wfNode("no", "state_set", "No", map[string]any{"key": "denied", "value": true}),
		},
		Edges: []Edge{
			wfEdge("start", "out", "appr"),
			wfEdge("appr", "approved", "yes"),
			wfEdge("appr", "denied", "no"),
		},
	}
}

func TestEngineApprovalApprove(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	g := approvalGraph()
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)
	notices := &noticeLog{}
	eng := testEngine(st, deps, EngineOptions{Notify: notices.notify})

	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	if got := mustRun(t, st, run.ID); got.Status != RunWaiting || got.WaitingReason != "approval" {
		t.Fatalf("run=%+v want waiting/approval", got)
	}
	dec := pendingDecision(t, st, run.ID)
	if dec.Kind != "approval" || dec.Action != "ask" {
		t.Fatalf("decision=%+v", dec)
	}
	if len(notices.snapshot()) == 0 || len(notices.snapshot()[0].Choices) != 2 {
		t.Fatalf("approval notice=%+v", notices.snapshot())
	}
	if _, err := eng.Answer(context.Background(), dec.ID, "approve"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	eng.Wait()

	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v want succeeded", got)
	}
	if got := mustNode(t, st, run.ID, "yes", -1).Status; got != NodeSucceeded {
		t.Fatalf("yes=%s", got)
	}
	if got := mustNode(t, st, run.ID, "no", -1).Status; got != NodeSkipped {
		t.Fatalf("no=%s want skipped", got)
	}
	if _, ok := stateRaw(t, st, wf.ID, "approved"); !ok {
		t.Fatal("approved branch did not run")
	}
}

func TestEngineApprovalDeny(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	g := approvalGraph()
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)
	notices := &noticeLog{}
	eng := testEngine(st, deps, EngineOptions{Notify: notices.notify})

	eng.Launch(context.Background(), run.ID)
	eng.Wait()
	dec := pendingDecision(t, st, run.ID)
	if _, err := eng.Answer(context.Background(), dec.ID, "deny"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	eng.Wait()

	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v", got)
	}
	if got := mustNode(t, st, run.ID, "yes", -1).Status; got != NodeSkipped {
		t.Fatalf("yes=%s want skipped", got)
	}
	if got := mustNode(t, st, run.ID, "no", -1).Status; got != NodeSucceeded {
		t.Fatalf("no=%s want succeeded", got)
	}
	if _, ok := stateRaw(t, st, wf.ID, "denied"); !ok {
		t.Fatal("denied branch did not run")
	}
}

// ---- wait -----------------------------------------------------------------

func TestEngineWaitNodeResumes(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	clk := &testClock{t: time.Unix(1000, 0)}

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("w", "wait", "Wait", map[string]any{"seconds": 60}),
		},
		Edges: []Edge{wfEdge("start", "out", "w")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)
	ctx := context.Background()

	eng := testEngine(st, deps, EngineOptions{Now: clk.Now})
	eng.Launch(ctx, run.ID)
	eng.Wait()

	got := mustRun(t, st, run.ID)
	if got.Status != RunWaiting || got.WaitingReason != "wait" {
		t.Fatalf("run=%+v want waiting/wait", got)
	}
	if got.WakeAt != 1000*1000+60*1000 {
		t.Fatalf("wake_at=%d", got.WakeAt)
	}

	// Not due yet: Launch is a no-op.
	eng.Launch(ctx, run.ID)
	eng.Wait()
	if got := mustRun(t, st, run.ID); got.Status != RunWaiting {
		t.Fatalf("run resumed early: %+v", got)
	}

	clk.advance(61 * time.Second)
	eng.Launch(ctx, run.ID)
	eng.Wait()
	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v want succeeded", got)
	}
	out := decodeAny(t, mustNode(t, st, run.ID, "w", -1).Output).(map[string]any)
	if out["waited"] != float64(60) {
		t.Fatalf("wait output=%v", out)
	}
}

// ---- cancel ---------------------------------------------------------------

func TestEngineCancelLiveRun(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.blockStarted = make(chan struct{}, 1)
	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("b", "bash", "Block", map[string]any{"command": "block"}),
		},
		Edges: []Edge{wfEdge("start", "out", "b")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)
	ctx := context.Background()

	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(ctx, run.ID)
	<-deps.blockStarted // the run is now inside the bash node
	if !eng.Live(run.ID) {
		t.Fatal("run is not live after Launch")
	}
	got, err := eng.Cancel(ctx, run.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got.Status != RunCancelled {
		t.Fatalf("run=%+v want cancelled", got)
	}
	nr := mustNode(t, st, run.ID, "b", -1)
	if nr.Status != NodeFailed || nr.Error != "cancelled" {
		t.Fatalf("node=%+v want cancelled", nr)
	}
}

// ---- guardian failure -----------------------------------------------------

func TestEngineGuardianParseFailure(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.bashSeq["bad"] = []BashResult{{ExitCode: 1, Output: "boom"}}
	deps.replies = []fakeReply{{agent: "g", contains: "You are the guardian", text: "I cannot help, sorry."}}

	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("bad", "bash", "Bad", map[string]any{"command": "bad"}),
		},
		Edges: []Edge{wfEdge("start", "out", "bad")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)
	notices := &noticeLog{}
	eng := testEngine(st, deps, EngineOptions{Notify: notices.notify})

	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	got := mustRun(t, st, run.ID)
	if got.Status != RunFailed || !strings.Contains(got.Error, "guardian failed") {
		t.Fatalf("run=%+v want guardian failed", got)
	}
	alerted := false
	for _, n := range notices.snapshot() {
		if strings.Contains(n.Text, "guardian failed") {
			alerted = true
		}
	}
	if !alerted {
		t.Fatal("no plain alert was sent")
	}
}
