package workflow

// Tests for the script_parse node handler (spec #44, REQ-SP-002..006,
// REQ-SP-010, REQ-SP-011). They drive the node through the engine with the fake
// Deps and helpers of engine_test.go, or call scriptParseNode directly on a
// runExec built by hand.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// scriptGraph builds a trigger.manual -> script_parse graph.
func scriptGraph(params map[string]any) Graph {
	return Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("parse", "script_parse", "Parse", params),
		},
		Edges: []Edge{wfEdge("start", "out", "parse")},
	}
}

// scriptParams builds the required params of a script_parse node with over
// applied on top.
func scriptParams(over map[string]any) map[string]any {
	m := map[string]any{
		"input":         "the input",
		"goal":          "extract the title",
		"output_schema": map[string]any{"title": "string"},
	}
	for k, v := range over {
		m[k] = v
	}
	return m
}

// runScriptParse runs one engine pass over a script_parse workflow and waits.
func runScriptParse(t *testing.T, st *MemStore, deps *fakeDeps, opts EngineOptions, params map[string]any) Run {
	t.Helper()
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(params))
	run := seedRun(t, st, wf, "manual", nil)
	eng := testEngine(st, deps, opts)
	eng.Launch(context.Background(), run.ID)
	eng.Wait()
	return run
}

// (a) A valid script succeeds with via=script, 0 tokens and no agent call
// (REQ-SP-004).
func TestScriptParseNodeScriptPasses(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	run := runScriptParse(t, st, deps, EngineOptions{}, scriptParams(map[string]any{
		"script": `function parse(input, ctx) { return {title: "hello"}; }`,
	}))

	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v", got)
	}
	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaScript || nr.Tokens != 0 {
		t.Fatalf("node=%+v want succeeded/script/0 tok", nr)
	}
	out := decodeAny(t, nr.Output).(map[string]any)
	if out["title"] != "hello" {
		t.Fatalf("output=%v want title hello", out)
	}
	if deps.agentCount() != 0 {
		t.Fatalf("agent calls=%d want 0", deps.agentCount())
	}
}

// (b) An empty script learns on the first run: fallback and one script decision
// (REQ-SP-005, REQ-SP-010, REQ-SP-011). The same scripted reply has no script, so
// the regeneration attempt is rejected and the node keeps the fallback tokens
// plus the rejected regeneration tokens.
func TestScriptParseNodeEmptyScriptFallsBack(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{{contains: "extract the title", text: `{"title":"hi"}`, tokens: 12}}
	notices := &noticeLog{}
	run := runScriptParse(t, st, deps, EngineOptions{Notify: notices.notify}, scriptParams(map[string]any{"script": ""}))

	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v", got)
	}
	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback || nr.Tokens != 24 {
		t.Fatalf("node=%+v want succeeded/fallback/24 tok", nr)
	}
	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Kind != "script" || decs[0].Action != "script_fallback" {
		t.Fatalf("decisions=%+v want one script/script_fallback", decs)
	}
	if decs[0].Fix["drift"] != scriptNoScriptCode {
		t.Fatalf("decision fix=%v want drift %s", decs[0].Fix, scriptNoScriptCode)
	}
	if got := notices.snapshot(); len(got) != 1 {
		t.Fatalf("notices=%+v want 1", got)
	}
}

// (c) A script returning {repos: []} against a non-empty array field is
// empty_drift and falls back (REQ-SP-003, REQ-SP-005).
func TestScriptParseNodeEmptyDriftFallsBack(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{{contains: "list the repos", text: `{"repos":[{"name":"a"}]}`}}
	params := map[string]any{
		"input":         "page",
		"goal":          "list the repos",
		"output_schema": map[string]any{"repos": "object[]"},
		"item_schema":   map[string]any{"repos": map[string]any{"name": "string"}},
		"script":        `function parse(input, ctx) { return {repos: []}; }`,
	}
	run := runScriptParse(t, st, deps, EngineOptions{}, params)

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback {
		t.Fatalf("node=%+v want succeeded/fallback", nr)
	}
	if !hasLog(nr.Logs, "script drift: "+DriftEmpty+": field \"repos\": empty") {
		t.Fatalf("logs=%v want the empty_drift line", nr.Logs)
	}
	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Fix["drift"] != DriftEmpty {
		t.Fatalf("decisions=%+v want one empty_drift decision", decs)
	}
}

// (d) on_drift=fail with a drifted script fails the node, with no agent call and
// the ladder running (REQ-SP-006, AC-SP-005).
func TestScriptParseNodeDriftFail(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g", FailureDefault: FailurePolicy{Mode: "continue"}},
		scriptGraph(scriptParams(map[string]any{
			"script":   `function parse(input, ctx) { return {title: ""}; }`,
			"on_drift": "fail",
		})))
	run := seedRun(t, st, wf, "manual", nil)
	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeFailed || !strings.Contains(nr.Error, DriftEmpty) {
		t.Fatalf("node=%+v want failed empty_drift", nr)
	}
	if deps.agentCount() != 0 {
		t.Fatalf("agent calls=%d want 0", deps.agentCount())
	}
}

// (e) on_drift=fallback_only runs exactly one agent call and no regeneration
// (REQ-SP-006, AC-SP-005).
func TestScriptParseNodeFallbackOnly(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{{contains: "extract the title", text: `{"title":"ok"}`, tokens: 5}}
	notices := &noticeLog{}
	run := runScriptParse(t, st, deps, EngineOptions{Notify: notices.notify}, scriptParams(map[string]any{
		"script":   `function parse(input, ctx) { return {title: ""}; }`,
		"on_drift": "fallback_only",
	}))

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Via != ViaFallback || nr.Tokens != 5 {
		t.Fatalf("node=%+v want fallback/5 tok", nr)
	}
	if deps.agentCount() != 1 {
		t.Fatalf("agent calls=%d want 1", deps.agentCount())
	}
	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Action != "script_fallback" {
		t.Fatalf("decisions=%+v want one script_fallback", decs)
	}
	if got := notices.snapshot(); len(got) != 1 || !strings.Contains(got[0].Text, "fallback only") {
		t.Fatalf("notices=%+v want one with 'fallback only'", got)
	}
}

// (f) Two fallbacks for the same node within 24 h send one notice; after the
// clock moves past 24 h a second notice is sent (REQ-SP-011).
func TestScriptParseNodeNoticeThrottle(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{{contains: "extract the title", text: `{"title":"ok"}`}}
	clk := &testClock{t: time.Unix(1000, 0)}
	notices := &noticeLog{}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(scriptParams(map[string]any{"script": ""})))
	eng := testEngine(st, deps, EngineOptions{Now: clk.Now, Notify: notices.notify})

	for i := 0; i < 2; i++ {
		run := seedRun(t, st, wf, "manual", nil)
		eng.Launch(context.Background(), run.ID)
		eng.Wait()
	}
	if got := notices.snapshot(); len(got) != 1 {
		t.Fatalf("notices=%d want 1 within 24h", len(got))
	}

	clk.advance(25 * time.Hour)
	run := seedRun(t, st, wf, "manual", nil)
	eng.Launch(context.Background(), run.ID)
	eng.Wait()
	if got := notices.snapshot(); len(got) != 2 {
		t.Fatalf("notices=%d want 2 after 24h", len(got))
	}
}

// (f2) The 24 h window is measured from the last notice actually sent, so a
// suppressed fallback does not reset it: t=0 sends, t=12h is suppressed, and
// t=25h sends again because the last sent notice was at t=0. Decisions are
// never throttled, so all three fallbacks record one (REQ-SP-011).
func TestScriptFallbackNoticeWindowIsFromLastSent(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{{contains: "extract the title", text: `{"title":"ok"}`}}
	clk := &testClock{t: time.Unix(1000, 0)}
	notices := &noticeLog{}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(scriptParams(map[string]any{"script": ""})))
	eng := testEngine(st, deps, EngineOptions{Now: clk.Now, Notify: notices.notify})

	fallback := func() {
		t.Helper()
		run := seedRun(t, st, wf, "manual", nil)
		eng.Launch(context.Background(), run.ID)
		eng.Wait()
	}

	fallback() // t=0: notice sent.
	if got := notices.snapshot(); len(got) != 1 {
		t.Fatalf("notices=%d want 1 after the first fallback", len(got))
	}

	clk.advance(12 * time.Hour)
	fallback() // t=12h: suppressed.
	if got := notices.snapshot(); len(got) != 1 {
		t.Fatalf("notices=%d want 1 while suppressed at t=12h", len(got))
	}

	clk.advance(13 * time.Hour) // t=25h.
	fallback()
	if got := notices.snapshot(); len(got) != 2 {
		t.Fatalf("notices=%d want 2 at t=25h (window from the t=0 send)", len(got))
	}

	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w"})
	if len(decs) != 3 {
		t.Fatalf("decisions=%d want 3 (never throttled)", len(decs))
	}
}

// (g) A fallback answer that misses the schema on both the first and the repair
// attempt fails the node (REQ-SP-005).
func TestScriptParseNodeFallbackSchemaFailure(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{contains: "Your previous answer did not match", text: "still prose"},
		{contains: "extract the title", text: "prose"},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g", FailureDefault: FailurePolicy{Mode: "continue"}},
		scriptGraph(scriptParams(map[string]any{"script": ""})))
	run := seedRun(t, st, wf, "manual", nil)
	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeFailed || !strings.Contains(nr.Error, "schema_error") {
		t.Fatalf("node=%+v want schema_error", nr)
	}
	if deps.agentCount() != 2 {
		t.Fatalf("agent calls=%d want 2 (fallback + repair)", deps.agentCount())
	}
}

// (h) A script with "{{" executes unrendered and returns its output (REQ-SP-001).
func TestScriptParseNodeKeepsScriptUnrendered(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	script := `function parse(input, ctx) { return {title: "{{ literal }}"}; }`
	run := runScriptParse(t, st, deps, EngineOptions{}, scriptParams(map[string]any{"script": script}))

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Via != ViaScript {
		t.Fatalf("via=%q want script", nr.Via)
	}
	out := decodeAny(t, nr.Output).(map[string]any)
	if out["title"] != "{{ literal }}" {
		t.Fatalf("output=%v want the literal braces", out)
	}
}

// (i) ex.scripts[node.ID] overrides the params script (REQ-SP-008).
func TestScriptParseNodeScriptOverride(t *testing.T) {
	eng := NewEngine(NewMemStore(), newFakeDeps(), EngineOptions{})
	ex := &runExec{
		e:        eng,
		ctx:      context.Background(),
		run:      Run{ID: "r", WorkflowID: "w", Number: 1},
		wf:       Workflow{ID: "w", Name: "W", Guardian: "g"},
		itemVals: map[int]any{},
		scripts:  map[string]string{"parse": `function parse(input, ctx) { return {title: "override"}; }`},
	}
	params := scriptParams(map[string]any{
		"script": `function parse(input, ctx) { return {title: "params"}; }`,
	})
	node := wfNode("parse", "script_parse", "Parse", params)

	res, err := ex.scriptParseNode(context.Background(), node, -1, params)
	if err != nil {
		t.Fatalf("scriptParseNode: %v", err)
	}
	out, _ := res.Output.(map[string]any)
	if out["title"] != "override" {
		t.Fatalf("output=%v want the override script output", out)
	}
	if res.Via != ViaScript || res.Tokens != 0 {
		t.Fatalf("result=%+v want script/0 tok", res)
	}
}

// (j) A script with an endless loop times out as script_error and falls back
// (REQ-SP-002, REQ-SP-005, AC-SP-001).
func TestScriptParseNodeTimeoutFallsBack(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{{contains: "extract the title", text: `{"title":"ok"}`, tokens: 3}}
	run := runScriptParse(t, st, deps, EngineOptions{}, scriptParams(map[string]any{
		"script":     `function parse(input, ctx) { while (true) {} }`,
		"timeout_ms": 100,
	}))

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback {
		t.Fatalf("node=%+v want succeeded/fallback", nr)
	}
	if !hasLog(nr.Logs, "script drift: "+DriftScript+": scriptrt: script timed out") {
		t.Fatalf("logs=%v want the script_error drift line", nr.Logs)
	}
}

// ---- regeneration (REQ-SP-007..012, AC-SP-002..007) -----------------------

// guardianCallCount counts the agent calls made to the guardian ("g"). A test
// that sets a separate fallback_agent sees only the regeneration calls here.
func guardianCallCount(deps *fakeDeps) int {
	deps.mu.Lock()
	defer deps.mu.Unlock()
	n := 0
	for _, c := range deps.agentCalls {
		if c.Agent == "g" {
			n++
		}
	}
	return n
}

// hookDeps wraps fakeDeps and runs hook before every agent call, so a test can
// simulate an owner edit while a regeneration is in flight. The engine calls it
// from the run goroutine only.
type hookDeps struct {
	*fakeDeps
	fired bool
	hook  func(AgentRequest)
}

func (h *hookDeps) RunAgent(ctx context.Context, req AgentRequest) (AgentResult, error) {
	if h.hook != nil {
		h.hook(req)
	}
	return h.fakeDeps.RunAgent(ctx, req)
}

// driftScriptParams builds the params of a repos script_parse node with a
// fallback agent and the item contract, so the fallback and regeneration agents
// are easy to tell apart in the fake Deps.
func driftScriptParams(script string) map[string]any {
	return map[string]any{
		"input":          "page",
		"goal":           "list the repos",
		"output_schema":  map[string]any{"repos": "object[]"},
		"item_schema":    map[string]any{"repos": map[string]any{"name": "string"}},
		"script":         script,
		"fallback_agent": "fallback-bot",
	}
}

// TestScriptParseRegenerationLearnsAndSavesScript covers AC-SP-002: an empty
// script learns on run 1 (via=fallback, a script_update decision, version +1
// with script and script_rev set and an always-sent notice) and run 2 runs the
// saved script at 0 tokens.
func TestScriptParseRegenerationLearnsAndSavesScript(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: extract the title", text: `{"title":"hi"}`, tokens: 12},
		{agent: "g", contains: "Goal: extract the title",
			text: `{"feasible":true,"script":"function parse(input, ctx) { return {title: 'hi'}; }"}`, tokens: 20},
	}
	notices := &noticeLog{}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"},
		scriptGraph(scriptParams(map[string]any{"script": "", "fallback_agent": "fallback-bot"})))
	eng := testEngine(st, deps, EngineOptions{Notify: notices.notify})
	ctx := context.Background()

	run1 := seedRun(t, st, wf, "manual", nil)
	eng.Launch(ctx, run1.ID)
	eng.Wait()

	nr := mustNode(t, st, run1.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback || nr.Tokens != 32 {
		t.Fatalf("node=%+v want succeeded/fallback/32 tok", nr)
	}
	decs, _ := st.ListDecisions(ctx, DecisionFilter{WorkflowID: "w", RunID: run1.ID})
	if len(decs) != 1 || decs[0].Kind != "script" || decs[0].Action != "script_update" || decs[0].Status != "applied" {
		t.Fatalf("decisions=%+v want one applied script_update", decs)
	}
	if decs[0].Tokens != 32 {
		t.Fatalf("decision tokens=%d want 32", decs[0].Tokens)
	}
	if got := notices.snapshot(); len(got) != 1 {
		t.Fatalf("notices=%d want 1 (a script_update notice is always sent)", len(got))
	}

	wf2, _ := st.GetWorkflow(ctx, "w")
	if wf2.Version != 2 {
		t.Fatalf("version=%d want 2", wf2.Version)
	}
	v2, err := st.GetVersion(ctx, "w", 2)
	if err != nil {
		t.Fatalf("get version 2: %v", err)
	}
	if v2.Author != "guardian" || !strings.Contains(v2.Summary, "rev 1") {
		t.Fatalf("version 2=%+v", v2)
	}
	var saved string
	for _, n := range v2.Graph.Nodes {
		if n.ID != "parse" {
			continue
		}
		saved, _ = n.Params["script"].(string)
		if got := scriptRev(n.Params); got != 1 {
			t.Fatalf("script_rev=%d want 1", got)
		}
		if _, ok := n.Params["baseline_tokens"]; !ok {
			t.Fatalf("baseline_tokens missing: %v", n.Params)
		}
	}
	if saved != `function parse(input, ctx) { return {title: 'hi'}; }` {
		t.Fatalf("saved script=%q", saved)
	}

	run2 := seedRun(t, st, wf2, "manual", nil)
	eng.Launch(ctx, run2.ID)
	eng.Wait()
	nr2 := mustNode(t, st, run2.ID, "parse", -1)
	if nr2.Status != NodeSucceeded || nr2.Via != ViaScript || nr2.Tokens != 0 {
		t.Fatalf("run 2 node=%+v want succeeded/script/0 tok", nr2)
	}
	if got := guardianCallCount(deps); got != 1 {
		t.Fatalf("guardian calls=%d want 1", got)
	}
}

// TestScriptParseRegenerationOnDrift covers AC-SP-003: a drift fixture is
// empty_drift, the fallback succeeds, the script is regenerated as rev N+1 and
// the next run uses it.
func TestScriptParseRegenerationOnDrift(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: `{"repos":[{"name":"a"}]}`, tokens: 10},
		{agent: "g", contains: "Goal: list the repos",
			text: `{"feasible":true,"script":"function parse(input, ctx) { return {repos: [{name: 'a'}]}; }"}`, tokens: 8},
	}
	const oldScript = `function parse(input, ctx) { return {repos: []}; }`
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(driftScriptParams(oldScript)))
	eng := testEngine(st, deps, EngineOptions{})
	ctx := context.Background()

	run1 := seedRun(t, st, wf, "manual", nil)
	eng.Launch(ctx, run1.ID)
	eng.Wait()

	nr := mustNode(t, st, run1.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback {
		t.Fatalf("node=%+v want succeeded/fallback", nr)
	}
	if !hasLog(nr.Logs, "script drift: "+DriftEmpty+`: field "repos": empty`) {
		t.Fatalf("logs=%v want the empty_drift line", nr.Logs)
	}
	decs, _ := st.ListDecisions(ctx, DecisionFilter{WorkflowID: "w", RunID: run1.ID})
	if len(decs) != 1 || decs[0].Action != "script_update" {
		t.Fatalf("decisions=%+v want one script_update", decs)
	}
	if decs[0].Fix["drift"] != DriftEmpty || decs[0].Fix["previous_script"] != oldScript {
		t.Fatalf("fix=%v", decs[0].Fix)
	}
	if f, _ := asFloat(decs[0].Fix["rev_to"]); f != 1 {
		t.Fatalf("rev_to=%v want 1", decs[0].Fix["rev_to"])
	}

	wf2, _ := st.GetWorkflow(ctx, "w")
	run2 := seedRun(t, st, wf2, "manual", nil)
	eng.Launch(ctx, run2.ID)
	eng.Wait()
	nr2 := mustNode(t, st, run2.ID, "parse", -1)
	if nr2.Status != NodeSucceeded || nr2.Via != ViaScript || nr2.Tokens != 0 {
		t.Fatalf("run 2 node=%+v want succeeded/script/0 tok", nr2)
	}
}

// TestScriptParseRegenerationRejectsTwice covers REQ-SP-007: a candidate that
// fails the contract is retried once with its problems, and when it fails again
// the update is rejected with regen_rejected and no version is saved.
func TestScriptParseRegenerationRejectsTwice(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: `{"repos":[{"name":"a"}]}`, tokens: 10},
		{agent: "g", contains: "Goal: list the repos",
			text: `{"feasible":true,"script":"function parse(input, ctx) { return {repos: []}; }"}`, tokens: 5},
	}
	run := runScriptParse(t, st, deps, EngineOptions{}, driftScriptParams(`function parse(input, ctx) { return {repos: []}; }`))

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback {
		t.Fatalf("node=%+v want succeeded/fallback", nr)
	}
	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Action != "script_fallback" {
		t.Fatalf("decisions=%+v want one script_fallback", decs)
	}
	if rejected, _ := decs[0].Fix["regen_rejected"].(bool); !rejected {
		t.Fatalf("fix=%v want regen_rejected", decs[0].Fix)
	}
	problems, _ := decs[0].Fix["problems"].([]string)
	if len(problems) == 0 {
		t.Fatalf("fix=%v want problems", decs[0].Fix)
	}
	if got := guardianCallCount(deps); got != 2 {
		t.Fatalf("guardian calls=%d want 2", got)
	}
	wf, _ := st.GetWorkflow(context.Background(), "w")
	if wf.Version != 1 {
		t.Fatalf("version=%d want 1 (no save)", wf.Version)
	}
}

// TestScriptParseRegenerationInfeasible covers REQ-SP-007: feasible=false stops
// after one guardian call and records the reason as the problem.
func TestScriptParseRegenerationInfeasible(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: `{"repos":[{"name":"a"}]}`, tokens: 10},
		{agent: "g", contains: "Goal: list the repos", text: `{"feasible":false,"reason":"needs judgment"}`, tokens: 4},
	}
	run := runScriptParse(t, st, deps, EngineOptions{}, driftScriptParams(`function parse(input, ctx) { return {repos: []}; }`))

	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Action != "script_fallback" {
		t.Fatalf("decisions=%+v want one script_fallback", decs)
	}
	problems, _ := decs[0].Fix["problems"].([]string)
	if len(problems) != 1 || problems[0] != "needs judgment" {
		t.Fatalf("problems=%v want [needs judgment]", problems)
	}
	if got := guardianCallCount(deps); got != 1 {
		t.Fatalf("guardian calls=%d want 1", got)
	}
}

// TestScriptParseRegenerationAgreementLow covers REQ-SP-007: a candidate whose
// agreement with a 5-leaf fallback output is below 0.8 is rejected.
func TestScriptParseRegenerationAgreementLow(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the items",
			text: `{"items":["aaaa aaaa","bbbb bbbb","cccc cccc","dddd dddd","eeee eeee"]}`, tokens: 10},
		{agent: "g", contains: "Goal: list the items",
			text: `{"feasible":true,"script":"function parse(input, ctx) { return {items: ['vvvv vvvv','wwww wwww','xxxx xxxx','yyyy yyyy','zzzz zzzz']}; }"}`, tokens: 6},
	}
	params := map[string]any{
		"input":          "page",
		"goal":           "list the items",
		"output_schema":  map[string]any{"items": "string[]"},
		"script":         `function parse(input, ctx) { return {items: []}; }`,
		"fallback_agent": "fallback-bot",
	}
	run := runScriptParse(t, st, deps, EngineOptions{}, params)

	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Action != "script_fallback" {
		t.Fatalf("decisions=%+v want one script_fallback", decs)
	}
	problems, _ := decs[0].Fix["problems"].([]string)
	if len(problems) != 1 || !strings.HasPrefix(problems[0], "agreement_low:") {
		t.Fatalf("problems=%v want an agreement_low problem", problems)
	}
	if got := guardianCallCount(deps); got != 2 {
		t.Fatalf("guardian calls=%d want 2", got)
	}
}

// TestScriptParseRegenerationPauseResumesOnReset covers AC-SP-004/REQ-SP-009:
// after three rejected regenerations in 24 h the node is paused, and
// ResetScriptPause resumes it.
func TestScriptParseRegenerationPauseResumesOnReset(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: extract the title", text: `{"title":"hi"}`, tokens: 1},
		{agent: "g", contains: "Goal: extract the title",
			text: `{"feasible":true,"script":"function parse(input, ctx) { return {title: ''}; }"}`, tokens: 2},
	}
	clk := &testClock{t: time.Now().Add(time.Hour)}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"},
		scriptGraph(scriptParams(map[string]any{"script": "", "fallback_agent": "fallback-bot"})))
	eng := testEngine(st, deps, EngineOptions{Now: clk.Now})
	ctx := context.Background()

	runOnce := func() {
		t.Helper()
		run := seedRun(t, st, wf, "manual", nil)
		eng.Launch(ctx, run.ID)
		eng.Wait()
	}

	for i := 0; i < 3; i++ {
		before := guardianCallCount(deps)
		runOnce()
		if got := guardianCallCount(deps) - before; got != 2 {
			t.Fatalf("run %d guardian calls=%d want 2", i+1, got)
		}
	}

	before := guardianCallCount(deps)
	runOnce()
	if got := guardianCallCount(deps) - before; got != 0 {
		t.Fatalf("paused run guardian calls=%d want 0", got)
	}

	eng.ResetScriptPause("w", "parse")
	before = guardianCallCount(deps)
	runOnce()
	if got := guardianCallCount(deps) - before; got != 2 {
		t.Fatalf("run after ResetScriptPause guardian calls=%d want 2", got)
	}
}

// TestScriptFallbackOnlySkipsRegeneration covers AC-SP-005: fallback_only never
// calls the guardian for regeneration.
func TestScriptFallbackOnlySkipsRegeneration(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: extract the title", text: `{"title":"hi"}`, tokens: 3},
	}
	run := runScriptParse(t, st, deps, EngineOptions{}, scriptParams(map[string]any{
		"script":         "",
		"fallback_agent": "fallback-bot",
		"on_drift":       "fallback_only",
	}))

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback {
		t.Fatalf("node=%+v want succeeded/fallback", nr)
	}
	if got := guardianCallCount(deps); got != 0 {
		t.Fatalf("guardian calls=%d want 0 (no regeneration)", got)
	}
}

// TestScriptParseRegenerationOncePerRunInLoop covers AC-SP-006: a loop of five
// items whose second drifts makes exactly one regeneration; items 3-5 run on the
// new script.
func TestScriptParseRegenerationOncePerRunInLoop(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: extract the title", text: `{"title":"b-title"}`, tokens: 3},
		{agent: "g", contains: "Goal: extract the title",
			text: `{"feasible":true,"script":"function parse(input, ctx) { return {title: 'reg ' + input}; }"}`, tokens: 4},
	}
	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("each", "loop", "Each", map[string]any{"items": []any{"a", "b", "c", "d", "e"}}),
			{ID: "parse", Type: "script_parse", Name: "Parse", Parent: "each", Params: map[string]any{
				"input":          "{{ item }}",
				"goal":           "extract the title",
				"output_schema":  map[string]any{"title": "string"},
				"script":         `function parse(input, ctx) { if (input === 'b') return {title: ''}; return {title: 'ok ' + input}; }`,
				"fallback_agent": "fallback-bot",
			}},
		},
		Edges: []Edge{
			wfEdge("start", "out", "each"),
			{ID: "body", From: "each", FromPort: "body", To: "parse"},
		},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)
	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	if got := mustRun(t, st, run.ID); got.Status != RunSucceeded {
		t.Fatalf("run=%+v want succeeded", got)
	}
	if got := guardianCallCount(deps); got != 1 {
		t.Fatalf("guardian calls=%d want 1", got)
	}
	if got := mustNode(t, st, run.ID, "parse", 1); got.Via != ViaFallback {
		t.Fatalf("item 1 via=%q want fallback", got.Via)
	}
	for _, i := range []int{0, 2, 3, 4} {
		if got := mustNode(t, st, run.ID, "parse", i); got.Via != ViaScript {
			t.Fatalf("item %d via=%q want script", i, got.Via)
		}
	}
}

// TestScriptDecisionRevertRestoresScript covers AC-SP-007/REQ-SP-012: revert
// restores the previous script as a new version with script_rev+1, closes the
// decision with choice "revert" and status "applied", and a second revert is
// ErrDecisionClosed.
func TestScriptDecisionRevertRestoresScript(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: `{"repos":[{"name":"a"}]}`, tokens: 10},
		{agent: "g", contains: "Goal: list the repos",
			text: `{"feasible":true,"script":"function parse(input, ctx) { return {repos: [{name: 'a'}]}; }"}`, tokens: 8},
	}
	const oldScript = `function parse(input, ctx) { return {repos: []}; }`
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(driftScriptParams(oldScript)))
	eng := testEngine(st, deps, EngineOptions{})
	ctx := context.Background()

	run := seedRun(t, st, wf, "manual", nil)
	eng.Launch(ctx, run.ID)
	eng.Wait()

	decs, _ := st.ListDecisions(ctx, DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Action != "script_update" {
		t.Fatalf("decisions=%+v want one script_update", decs)
	}
	dec := decs[0]

	got, err := eng.Answer(ctx, dec.ID, "revert")
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got.ID != run.ID {
		t.Fatalf("run=%q want %q", got.ID, run.ID)
	}

	d2, _ := st.GetDecision(ctx, dec.ID)
	if d2.Choice != "revert" || d2.Status != "applied" {
		t.Fatalf("decision=%+v want choice revert / status applied", d2)
	}
	wf2, _ := st.GetWorkflow(ctx, "w")
	if wf2.Version != 3 {
		t.Fatalf("version=%d want 3", wf2.Version)
	}
	v3, err := st.GetVersion(ctx, "w", 3)
	if err != nil {
		t.Fatalf("get version 3: %v", err)
	}
	if v3.Author != "owner" || !strings.Contains(v3.Summary, "revert script Parse to rev 0") {
		t.Fatalf("version 3=%+v", v3)
	}
	for _, n := range v3.Graph.Nodes {
		if n.ID != "parse" {
			continue
		}
		if s, _ := n.Params["script"].(string); s != oldScript {
			t.Fatalf("restored script=%q want the previous script", s)
		}
		if got := scriptRev(n.Params); got != 2 {
			t.Fatalf("script_rev=%d want 2", got)
		}
	}

	if _, err := eng.Answer(ctx, dec.ID, "revert"); !errors.Is(err, ErrDecisionClosed) {
		t.Fatalf("second revert err=%v want ErrDecisionClosed", err)
	}
}

// TestScriptDecisionRevertChoiceErrors covers API-SP-006: "revert" on a
// script_fallback decision and on a non-script decision is ErrInvalidChoice.
func TestScriptDecisionRevertChoiceErrors(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: `{"repos":[{"name":"a"}]}`, tokens: 10},
		{agent: "g", contains: "Goal: list the repos", text: `{"feasible":false,"reason":"nope"}`, tokens: 2},
	}
	run := runScriptParse(t, st, deps, EngineOptions{}, driftScriptParams(`function parse(input, ctx) { return {repos: []}; }`))
	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Action != "script_fallback" {
		t.Fatalf("decisions=%+v want one script_fallback", decs)
	}
	eng := testEngine(st, deps, EngineOptions{})
	if _, err := eng.Answer(context.Background(), decs[0].ID, "revert"); !errors.Is(err, ErrInvalidChoice) {
		t.Fatalf("revert on script_fallback err=%v want ErrInvalidChoice", err)
	}

	appr := Decision{ID: "d_appr", WorkflowID: "w", Kind: "approval", Action: "ask", Status: "pending"}
	if err := st.PutDecision(context.Background(), appr); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Answer(context.Background(), appr.ID, "revert"); !errors.Is(err, ErrInvalidChoice) {
		t.Fatalf("revert on approval err=%v want ErrInvalidChoice", err)
	}
}

// TestScriptVersionConflictDropsUpdate covers AC-SP-007: when the owner edits the
// node's script during the regeneration, the update is dropped and the reason is
// recorded in the decision.
func TestScriptVersionConflictDropsUpdate(t *testing.T) {
	st := NewMemStore()
	base := newFakeDeps()
	base.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: `{"repos":[{"name":"a"}]}`, tokens: 10},
		{agent: "g", contains: "Goal: list the repos",
			text: `{"feasible":true,"script":"function parse(input, ctx) { return {repos: [{name: 'a'}]}; }"}`, tokens: 8},
	}
	const ownerScript = `function parse(input, ctx) { return {repos: [{name: 'owner'}]}; }`
	ctx := context.Background()
	deps := &hookDeps{fakeDeps: base}
	deps.hook = func(req AgentRequest) {
		if deps.fired || req.Agent != "g" {
			return
		}
		deps.fired = true
		_, _ = st.SaveVersion(ctx, "w", 1, scriptGraph(driftScriptParams(ownerScript)), "owner", "owner edit")
	}

	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"},
		scriptGraph(driftScriptParams(`function parse(input, ctx) { return {repos: []}; }`)))
	eng := testEngine(st, deps, EngineOptions{})

	run := seedRun(t, st, wf, "manual", nil)
	eng.Launch(ctx, run.ID)
	eng.Wait()

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback {
		t.Fatalf("node=%+v want succeeded/fallback", nr)
	}
	decs, _ := st.ListDecisions(ctx, DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Action != "script_fallback" {
		t.Fatalf("decisions=%+v want one script_fallback", decs)
	}
	if dropped, _ := decs[0].Fix["dropped"].(string); dropped == "" {
		t.Fatalf("fix=%v want a dropped reason", decs[0].Fix)
	}
	wf2, _ := st.GetWorkflow(ctx, "w")
	if wf2.Version != 2 {
		t.Fatalf("version=%d want 2 (only the owner edit)", wf2.Version)
	}
}

// ---- drift guard (spec #45, REQ-DG-001..007) ------------------------------

// trendParams builds the params of a repos-array script_parse node shaped like
// testdata/trending_profile.json, with over applied on top.
func trendParams(over map[string]any) map[string]any {
	m := map[string]any{
		"input":          "page",
		"goal":           "list the repos",
		"output_schema":  map[string]any{"repos": "object[]"},
		"item_schema":    map[string]any{"repos": map[string]any{"name": "string", "stars_today": "integer"}},
		"fallback_agent": "fallback-bot",
	}
	for k, v := range over {
		m[k] = v
	}
	return m
}

// trendOutput builds a repos output with the given stars per item.
func trendOutput(stars ...int) map[string]any {
	arr := make([]any, len(stars))
	for i, s := range stars {
		arr[i] = map[string]any{"name": fmt.Sprintf("owner/repo%d", i), "stars_today": s}
	}
	return map[string]any{"repos": arr}
}

// trendScript builds a parse script that returns trendOutput(stars...).
func trendScript(stars ...int) string {
	parts := make([]string, len(stars))
	for i, s := range stars {
		parts[i] = fmt.Sprintf("{name:'owner/repo%d',stars_today:%d}", i, s)
	}
	return "function parse(input, ctx) { return {repos: [" + strings.Join(parts, ",") + "]}; }"
}

// trendJSON renders trendOutput(stars...) as the fallback answer text.
func trendJSON(stars ...int) string {
	b, _ := json.Marshal(trendOutput(stars...))
	return string(b)
}

// nodeLogsContain reports whether any log line contains sub.
func nodeLogsContain(logs []string, sub string) bool {
	for _, l := range logs {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// versionNodeSpec returns the schema spec of a versioned node's item field.
func versionNodeSpec(t *testing.T, st *MemStore, wfID string, version int, nodeID, field, key string) string {
	t.Helper()
	v, err := st.GetVersion(context.Background(), wfID, version)
	if err != nil {
		t.Fatalf("get version %d: %v", version, err)
	}
	for _, n := range v.Graph.Nodes {
		if n.ID != nodeID {
			continue
		}
		entry := asMapParam(asMapParam(n.Params["item_schema"])[field])
		s, _ := entry[key].(string)
		return s
	}
	return ""
}

// TestScriptParseProfileDriftRegenerates covers REQ-DG-005 without confirmation:
// the script drifts, the fallback disagrees, so regeneration runs.
func TestScriptParseProfileDriftRegenerates(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: trendJSON(5, 5, 5), tokens: 10},
		{agent: "g", contains: "Goal: list the repos",
			text: `{"feasible":true,"script":"` + trendScript(5, 5, 5) + `"}`, tokens: 8},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"},
		scriptGraph(trendParams(map[string]any{"script": trendScript(0, 0, 0)})))
	seedBaselineRun(t, st, wf, trendOutput(5, 3, 7), NodeSucceeded, ViaScript)
	seedBaselineRun(t, st, wf, trendOutput(2, 9, 4), NodeSucceeded, ViaScript)
	run := seedRun(t, st, wf, "manual", nil)
	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback {
		t.Fatalf("node=%+v want succeeded/fallback", nr)
	}
	if !nodeLogsContain(nr.Logs, "script drift: "+DriftProfile+":") {
		t.Fatalf("logs=%v want a profile_drift line", nr.Logs)
	}
	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Action != "script_update" {
		t.Fatalf("decisions=%+v want one script_update", decs)
	}
	wf2, _ := st.GetWorkflow(context.Background(), "w")
	if wf2.Version != 2 {
		t.Fatalf("version=%d want 2", wf2.Version)
	}
}

// TestScriptParseProfileDriftConfirmed covers AC-DG-003: a profile drift the
// fallback confirms records profile_accept, sends no notice, saves no version,
// and the node output is the fallback output.
func TestScriptParseProfileDriftConfirmed(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: trendJSON(0, 0, 0), tokens: 9},
	}
	notices := &noticeLog{}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"},
		scriptGraph(trendParams(map[string]any{"script": trendScript(0, 0, 0)})))
	seedBaselineRun(t, st, wf, trendOutput(5, 3, 7), NodeSucceeded, ViaScript)
	seedBaselineRun(t, st, wf, trendOutput(2, 9, 4), NodeSucceeded, ViaScript)
	run := seedRun(t, st, wf, "manual", nil)
	eng := testEngine(st, deps, EngineOptions{Notify: notices.notify})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback || nr.Tokens != 9 {
		t.Fatalf("node=%+v want succeeded/fallback/9 tok", nr)
	}
	if !nodeLogsContain(nr.Logs, "profile change confirmed by fallback (agreement 1.00)") {
		t.Fatalf("logs=%v want the confirmation line", nr.Logs)
	}
	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Kind != "script" || decs[0].Action != "profile_accept" || decs[0].Status != "applied" {
		t.Fatalf("decisions=%+v want one applied profile_accept", decs)
	}
	if decs[0].Fix["drift"] != DriftProfile {
		t.Fatalf("fix=%v want drift profile_drift", decs[0].Fix)
	}
	if ag, _ := asFloat(decs[0].Fix["agreement"]); ag != 1 {
		t.Fatalf("agreement=%v want 1", decs[0].Fix["agreement"])
	}
	if got := notices.snapshot(); len(got) != 0 {
		t.Fatalf("notices=%+v want none on a confirmed profile drift", got)
	}
	if wf2, _ := st.GetWorkflow(context.Background(), "w"); wf2.Version != 1 {
		t.Fatalf("version=%d want 1 (a confirmed profile drift changes no schema)", wf2.Version)
	}
}

// TestScriptParseProfileAcceptRestartsBaseline covers the profile_accept cut of
// REQ-DG-001: after a confirmed profile drift at run 3, the next run's baseline
// starts there, so the next run with the accepted data is via=script.
func TestScriptParseProfileAcceptRestartsBaseline(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: trendJSON(0, 0, 0), tokens: 4},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"},
		scriptGraph(trendParams(map[string]any{"script": trendScript(0, 0, 0)})))
	seedBaselineRun(t, st, wf, trendOutput(5, 3, 7), NodeSucceeded, ViaScript)
	seedBaselineRun(t, st, wf, trendOutput(2, 9, 4), NodeSucceeded, ViaScript)
	eng := testEngine(st, deps, EngineOptions{})
	ctx := context.Background()

	run := seedRun(t, st, wf, "manual", nil)
	eng.Launch(ctx, run.ID)
	eng.Wait()
	if decs, _ := st.ListDecisions(ctx, DecisionFilter{WorkflowID: "w", RunID: run.ID}); len(decs) != 1 {
		t.Fatalf("decisions=%+v want one profile_accept", decs)
	}

	// The baseline now cuts at the profile_accept run, so only that run's output
	// remains (fewer than two), and the next run with the same data is via=script.
	base, _ := loadScriptBaseline(ctx, st, "w", "parse", "")
	if len(base) != 1 {
		t.Fatalf("baseline=%d want 1 (the cut)", len(base))
	}

	run2 := seedRun(t, st, wf, "manual", nil)
	eng.Launch(ctx, run2.ID)
	eng.Wait()
	nr2 := mustNode(t, st, run2.ID, "parse", -1)
	if nr2.Status != NodeSucceeded || nr2.Via != ViaScript || nr2.Tokens != 0 {
		t.Fatalf("run 2 node=%+v want succeeded/script/0 tok", nr2)
	}
}

// TestScriptParseRuleDriftConfirmed covers AC-DG-004: a rule drift the fallback
// confirms drops the violated token in a new version, records rule_drop and
// notifies the owner.
func TestScriptParseRuleDriftConfirmed(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: trendJSON(0), tokens: 7},
	}
	notices := &noticeLog{}
	params := trendParams(map[string]any{
		"script":      trendScript(0),
		"item_schema": map[string]any{"repos": map[string]any{"name": "string", "stars_today": "integer >0"}},
	})
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(params))
	run := seedRun(t, st, wf, "manual", nil)
	eng := testEngine(st, deps, EngineOptions{Notify: notices.notify})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback || nr.Tokens != 7 {
		t.Fatalf("node=%+v want succeeded/fallback/7 tok", nr)
	}
	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Kind != "script" || decs[0].Action != "rule_drop" || decs[0].Status != "applied" {
		t.Fatalf("decisions=%+v want one applied rule_drop", decs)
	}
	rules, _ := decs[0].Fix["rules"].([]map[string]any)
	if len(rules) != 1 || rules[0]["path"] != "repos.stars_today" || rules[0]["token"] != ">0" {
		t.Fatalf("rules=%v want one [repos.stars_today >0] entry", decs[0].Fix["rules"])
	}
	if got := notices.snapshot(); len(got) != 1 || !strings.Contains(got[0].Text, "rule dropped: repos.stars_today >0") {
		t.Fatalf("notices=%+v want one 'rule dropped'", got)
	}
	wf2, _ := st.GetWorkflow(context.Background(), "w")
	if wf2.Version != 2 {
		t.Fatalf("version=%d want 2", wf2.Version)
	}
	if got := versionNodeSpec(t, st, "w", 2, "parse", "repos", "stars_today"); got != "integer" {
		t.Fatalf("stars_today spec=%q want %q (token dropped)", got, "integer")
	}
}

// TestScriptParseRuleDriftRegenerates covers REQ-DG-005 without confirmation: a
// rule drift the fallback does not confirm takes the spec 44 regeneration path.
func TestScriptParseRuleDriftRegenerates(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: trendJSON(5), tokens: 10},
		{agent: "g", contains: "Goal: list the repos",
			text: `{"feasible":true,"script":"` + trendScript(5) + `"}`, tokens: 8},
	}
	params := trendParams(map[string]any{
		"script":      trendScript(0),
		"item_schema": map[string]any{"repos": map[string]any{"name": "string", "stars_today": "integer >0"}},
	})
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(params))
	run := seedRun(t, st, wf, "manual", nil)
	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback {
		t.Fatalf("node=%+v want succeeded/fallback", nr)
	}
	if !nodeLogsContain(nr.Logs, "script drift: "+DriftRule+":") {
		t.Fatalf("logs=%v want a rule_drift line", nr.Logs)
	}
	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Action != "script_update" {
		t.Fatalf("decisions=%+v want one script_update", decs)
	}
	wf2, _ := st.GetWorkflow(context.Background(), "w")
	if wf2.Version != 2 {
		t.Fatalf("version=%d want 2", wf2.Version)
	}
	// The dropped rule is unchanged by regeneration (REQ-DG-006): the new
	// version keeps it, because only a confirmed drift drops a rule.
	if got := versionNodeSpec(t, st, "w", 2, "parse", "repos", "stars_today"); got != "integer >0" {
		t.Fatalf("stars_today spec=%q want %q", got, "integer >0")
	}
}

// TestScriptParseAuditEvery covers AC-DG-005: audit_every 3 makes the third
// script run an audit that keeps the script output, sets via=audit and adds the
// fallback tokens; the count then restarts, so the fourth run is via=script.
func TestScriptParseAuditEvery(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: trendJSON(5, 5, 5), tokens: 11},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"},
		scriptGraph(trendParams(map[string]any{"script": trendScript(5, 5, 5), "audit_every": 3})))
	seedBaselineRun(t, st, wf, trendOutput(5, 3, 7), NodeSucceeded, ViaScript)
	seedBaselineRun(t, st, wf, trendOutput(5, 3, 7), NodeSucceeded, ViaScript)
	eng := testEngine(st, deps, EngineOptions{})
	ctx := context.Background()

	run3 := seedRun(t, st, wf, "manual", nil)
	eng.Launch(ctx, run3.ID)
	eng.Wait()
	nr3 := mustNode(t, st, run3.ID, "parse", -1)
	if nr3.Status != NodeSucceeded || nr3.Via != ViaAudit || nr3.Tokens != 11 {
		t.Fatalf("run 3 node=%+v want succeeded/audit/11 tok", nr3)
	}
	if !nodeLogsContain(nr3.Logs, "audit ok, agreement 1.00") {
		t.Fatalf("logs=%v want the audit line", nr3.Logs)
	}
	if decs, _ := st.ListDecisions(ctx, DecisionFilter{WorkflowID: "w", RunID: run3.ID}); len(decs) != 0 {
		t.Fatalf("decisions=%+v want none on a confirmed audit", decs)
	}

	run4 := seedRun(t, st, wf, "manual", nil)
	eng.Launch(ctx, run4.ID)
	eng.Wait()
	nr4 := mustNode(t, st, run4.ID, "parse", -1)
	if nr4.Status != NodeSucceeded || nr4.Via != ViaScript || nr4.Tokens != 0 {
		t.Fatalf("run 4 node=%+v want succeeded/script/0 tok (count restarts)", nr4)
	}
}

// TestScriptParseAuditDrift covers REQ-DG-007: an audit the fallback does not
// confirm is audit_drift and takes the spec 44 drift path with the fallback
// output.
func TestScriptParseAuditDrift(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", text: trendJSON(9, 9, 9), tokens: 6},
		{agent: "g", contains: "Goal: list the repos",
			text: `{"feasible":true,"script":"` + trendScript(9, 9, 9) + `"}`, tokens: 5},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"},
		scriptGraph(trendParams(map[string]any{"script": trendScript(5, 5, 5), "audit_every": 1})))
	seedBaselineRun(t, st, wf, trendOutput(5, 3, 7), NodeSucceeded, ViaScript)
	run := seedRun(t, st, wf, "manual", nil)
	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaFallback {
		t.Fatalf("node=%+v want succeeded/fallback", nr)
	}
	if !nodeLogsContain(nr.Logs, "script drift: "+DriftAudit+":") {
		t.Fatalf("logs=%v want an audit_drift line", nr.Logs)
	}
	decs, _ := st.ListDecisions(context.Background(), DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Action != "script_update" {
		t.Fatalf("decisions=%+v want one script_update", decs)
	}
}

// TestScriptParseAuditFallbackErrorKeepsScript covers REQ-DG-007: an audit whose
// fallback call fails (with a live context) is skipped, the script output stands
// and via stays script.
func TestScriptParseAuditFallbackErrorKeepsScript(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fallback-bot", contains: "Goal: list the repos", err: errors.New("audit boom")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"},
		scriptGraph(trendParams(map[string]any{"script": trendScript(5, 5, 5), "audit_every": 1})))
	run := seedRun(t, st, wf, "manual", nil)
	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaScript {
		t.Fatalf("node=%+v want succeeded/script (audit skipped)", nr)
	}
	if got, want := string(nodeJSON(decodeAny(t, nr.Output))), string(nodeJSON(trendOutput(5, 5, 5))); got != want {
		t.Fatalf("output=%s want the script output %s", got, want)
	}
	skipped := false
	for _, l := range nr.Logs {
		if strings.HasPrefix(l, "audit skipped:") {
			skipped = true
			break
		}
	}
	if !skipped {
		t.Fatalf("logs=%v want an 'audit skipped:' line", nr.Logs)
	}
	if got := deps.agentCount(); got != 1 {
		t.Fatalf("agent calls=%d want 1 (the audit fallback)", got)
	}
}

// TestScriptParseNoBaselineSkipsProfile covers AC-DG-006: without a baseline the
// profile check is skipped, so a zero output runs via script as in spec 44.
func TestScriptParseNoBaselineSkipsProfile(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"},
		scriptGraph(trendParams(map[string]any{"script": trendScript(0, 0, 0)})))
	run := seedRun(t, st, wf, "manual", nil)
	eng := testEngine(st, deps, EngineOptions{})
	eng.Launch(context.Background(), run.ID)
	eng.Wait()

	nr := mustNode(t, st, run.ID, "parse", -1)
	if nr.Status != NodeSucceeded || nr.Via != ViaScript || nr.Tokens != 0 {
		t.Fatalf("node=%+v want succeeded/script/0 tok", nr)
	}
	if deps.agentCount() != 0 {
		t.Fatalf("agent calls=%d want 0", deps.agentCount())
	}
}
