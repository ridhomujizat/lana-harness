package workflow

// Tests for the Script Parse service operations (spec #44, REQ-SP-015..019,
// API-SP-002..005, API-SP-009). They use MemStore, the fake Deps of the engine
// tests and a controllable clock. Stored samples and stats are seeded directly
// with PutNodeRun and PutDecision.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// testScriptNode builds a script_parse node with the given params.
func testScriptNode(id string, params map[string]any) Node {
	return Node{ID: id, Type: "script_parse", Name: "Parse", Params: params}
}

// putServiceNodeRun stores one top-level node run.
func putServiceNodeRun(t *testing.T, st *MemStore, run Run, nodeID string, status NodeStatus, via string, tokens int64, input, output any) {
	t.Helper()
	nr := NodeRun{RunID: run.ID, NodeID: nodeID, ItemIndex: -1, Status: status, Via: via, Tokens: tokens}
	if input != nil {
		nr.Input = nodeJSON(input)
	}
	if output != nil {
		nr.Output = nodeJSON(output)
	}
	if err := st.PutNodeRun(context.Background(), nr); err != nil {
		t.Fatalf("put node run: %v", err)
	}
}

// API-SP-002, REQ-SP-015: Test uses the node's stored rendered input of a run as
// the sample when the request sends no input.
func TestServiceTestScriptFromStoredRun(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{t: time.Now()})
	ctx := context.Background()
	params := scriptParams(map[string]any{"script": `function parse(input, ctx) { return {title: input}; }`})
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(params))
	run := seedRun(t, st, wf, "manual", nil)
	putServiceNodeRun(t, st, run, "parse", NodeSucceeded, ViaScript, 0,
		map[string]any{"input": "page", "goal": "extract the title"}, map[string]any{"title": "page"})

	res, err := svc.TestScript(ctx, "w", testScriptNode("parse", params), nil, false, 0)
	if err != nil {
		t.Fatalf("TestScript: %v", err)
	}
	if !res.OK || res.Sample.Source != "run" || res.Sample.Run != run.Number {
		t.Fatalf("result=%+v", res)
	}
	out, _ := res.Output.(map[string]any)
	if out["title"] != "page" {
		t.Fatalf("output=%v", res.Output)
	}
}

// API-SP-002, REQ-SP-015: a request input wins over the stored sample.
func TestServiceTestScriptFromRequestInput(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{t: time.Now()})
	ctx := context.Background()
	params := scriptParams(map[string]any{"script": `function parse(input, ctx) { return {title: input}; }`})
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(params))
	run := seedRun(t, st, wf, "manual", nil)
	putServiceNodeRun(t, st, run, "parse", NodeSucceeded, ViaScript, 0,
		map[string]any{"input": "stored"}, map[string]any{"title": "stored"})

	res, err := svc.TestScript(ctx, "w", testScriptNode("parse", params), "hello", true, 0)
	if err != nil {
		t.Fatalf("TestScript: %v", err)
	}
	if res.Sample.Source != "request" || res.Sample.Run != 0 {
		t.Fatalf("sample=%+v", res.Sample)
	}
	out, _ := res.Output.(map[string]any)
	if out["title"] != "hello" {
		t.Fatalf("output=%v", res.Output)
	}
}

// API-SP-002, REQ-SP-015: when the node never ran, the input template is
// rendered against the newest run's stored node outputs.
func TestServiceTestScriptRendersTemplate(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{t: time.Now()})
	ctx := context.Background()
	params := scriptParams(map[string]any{
		"input":  "{{ nodes.fetch.output.markdown }}",
		"script": `function parse(input, ctx) { return {title: input}; }`,
	})
	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("fetch", "webfetch", "Fetch", map[string]any{"url": "x"}),
			wfNode("parse", "script_parse", "Parse", params),
		},
		Edges: []Edge{wfEdge("start", "out", "fetch"), wfEdge("fetch", "out", "parse")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)
	putServiceNodeRun(t, st, run, "fetch", NodeSucceeded, "", 0, nil, map[string]any{"markdown": "BODY"})

	res, err := svc.TestScript(ctx, "w", testScriptNode("parse", params), nil, false, 0)
	if err != nil {
		t.Fatalf("TestScript: %v", err)
	}
	if !res.OK || res.Sample.Source != "run" {
		t.Fatalf("result=%+v", res)
	}
	out, _ := res.Output.(map[string]any)
	if out["title"] != "BODY" {
		t.Fatalf("output=%v want the rendered input", res.Output)
	}
}

// API-SP-002, REQ-SP-015: no input, no run and no stored node gives no_sample.
func TestServiceTestScriptNoSample(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{t: time.Now()})
	params := scriptParams(map[string]any{"script": `function parse(input, ctx) { return {title: "x"}; }`})
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(params))

	if _, err := svc.TestScript(context.Background(), "w", testScriptNode("parse", params), nil, false, 0); !errors.Is(err, ErrNoSample) {
		t.Fatalf("err=%v want ErrNoSample", err)
	}
}

// API-SP-002: a node that is not script_parse is ErrNotScriptNode.
func TestServiceTestScriptNotScriptNode(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{t: time.Now()})
	node := Node{ID: "m", Type: "message", Params: map[string]any{"text": "hi"}}
	if _, err := svc.TestScript(context.Background(), "w", node, "x", true, 0); !errors.Is(err, ErrNotScriptNode) {
		t.Fatalf("err=%v want ErrNotScriptNode", err)
	}
}

// API-SP-002: an empty script is the problem script_error: script is empty.
func TestServiceTestScriptEmptyScript(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{t: time.Now()})
	res, err := svc.TestScript(context.Background(), "w", testScriptNode("parse", scriptParams(nil)), "x", true, 0)
	if err != nil {
		t.Fatalf("TestScript: %v", err)
	}
	if res.OK || len(res.Problems) != 1 || res.Problems[0].Code != "script_error" || res.Problems[0].Message != "script is empty" {
		t.Fatalf("result=%+v", res)
	}
}

// API-SP-002: a failing script returns its problems and no output.
func TestServiceTestScriptProblems(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{t: time.Now()})
	params := scriptParams(map[string]any{"script": `function parse(input, ctx) { return {title: ""}; }`})
	res, err := svc.TestScript(context.Background(), "w", testScriptNode("parse", params), "x", true, 0)
	if err != nil {
		t.Fatalf("TestScript: %v", err)
	}
	if res.OK || len(res.Problems) != 1 || res.Problems[0].Code != DriftEmpty {
		t.Fatalf("result=%+v", res)
	}
}

// API-SP-003, REQ-SP-016: a successful Generate returns the script and output,
// sets the agreement when an expected output is given, and resets the
// regeneration pause so the next run regenerates again.
func TestServiceGenerateScriptResetsPause(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "fb", contains: "Goal:", text: `{"title":"ok"}`, tokens: 3},
		{agent: "g", contains: "You write a deterministic JavaScript parser",
			text: `{"feasible":true,"script":"function parse(input, ctx) { return {title: 'ok'}; }"}`, tokens: 7},
	}
	params := scriptParams(map[string]any{"script": "", "fallback_agent": "fb"})
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(params))
	clk := &testClock{t: time.Now()}
	svc := newTestService(t, st, deps, clk)
	ctx := context.Background()

	// Three rejected regenerations in the last 24 h pause the node.
	for i := 0; i < 3; i++ {
		if err := st.PutDecision(ctx, Decision{
			ID: decisionID(), WorkflowID: "w", NodeID: "parse", Kind: "script", Action: "script_fallback",
			Status: "applied", CreatedAt: clk.Now().UnixMilli(), Fix: map[string]any{"regen_rejected": true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	clk.advance(time.Minute)

	expected := map[string]any{"title": "ok"}
	res, err := svc.GenerateScript(ctx, "w", testScriptNode("parse", params), "page", true, 0, expected)
	if err != nil {
		t.Fatalf("GenerateScript: %v", err)
	}
	if !res.Feasible || res.Script == "" || res.Output == nil {
		t.Fatalf("result=%+v", res)
	}
	if res.Agreement == nil || *res.Agreement != 1 {
		t.Fatalf("agreement=%v want 1", res.Agreement)
	}
	if res.Tokens != 7 {
		t.Fatalf("tokens=%d want 7", res.Tokens)
	}

	// The pause is reset: the engine now regenerates and saves a new script.
	run := seedRun(t, st, wf, "manual", nil)
	svc.engine.Launch(ctx, run.ID)
	svc.engine.Wait()
	decs, _ := st.ListDecisions(ctx, DecisionFilter{WorkflowID: "w", RunID: run.ID})
	if len(decs) != 1 || decs[0].Action != "script_update" {
		t.Fatalf("decisions=%+v want one script_update", decs)
	}
	wf2, _ := st.GetWorkflow(ctx, "w")
	if wf2.Version != 2 {
		t.Fatalf("version=%d want 2", wf2.Version)
	}
}

// API-SP-003, REQ-SP-007: a rejected first candidate is retried with its
// problems appended; the second attempt is accepted.
func TestServiceGenerateScriptSecondAttempt(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "g", contains: "Problems with the previous attempt",
			text: `{"feasible":true,"script":"function parse(input, ctx) { return {title: 'ok'}; }"}`, tokens: 6},
		{agent: "g", contains: "You write a deterministic JavaScript parser",
			text: `{"feasible":true,"script":"function parse(input, ctx) { return {title: ''}; }"}`, tokens: 5},
	}
	params := scriptParams(nil)
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(params))
	svc := newTestService(t, st, deps, &testClock{t: time.Now()})

	res, err := svc.GenerateScript(context.Background(), "w", testScriptNode("parse", params), "page", true, 0, nil)
	if err != nil {
		t.Fatalf("GenerateScript: %v", err)
	}
	if !res.Feasible || res.Script != `function parse(input, ctx) { return {title: 'ok'}; }` {
		t.Fatalf("result=%+v", res)
	}
	if res.Tokens != 11 {
		t.Fatalf("tokens=%d want 11", res.Tokens)
	}
	if got := guardianCallCount(deps); got != 2 {
		t.Fatalf("guardian calls=%d want 2", got)
	}
}

// API-SP-003: an infeasible guardian answer returns the reason and no script.
func TestServiceGenerateScriptInfeasible(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "g", contains: "You write a deterministic JavaScript parser", text: `{"feasible":false,"reason":"needs judgment"}`, tokens: 4},
	}
	params := scriptParams(nil)
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(params))
	svc := newTestService(t, st, deps, &testClock{t: time.Now()})

	res, err := svc.GenerateScript(context.Background(), "w", testScriptNode("parse", params), "page", true, 0, nil)
	if err != nil {
		t.Fatalf("GenerateScript: %v", err)
	}
	if res.Feasible || res.Reason != "needs judgment" || res.Script != "" {
		t.Fatalf("result=%+v", res)
	}
}

// API-SP-003: a missing guardian agent is ErrGuardianUnavailable.
func TestServiceGenerateScriptGuardianUnavailable(t *testing.T) {
	st := NewMemStore()
	deps := &agentDeps{fakeDeps: newFakeDeps(), known: map[string]bool{}}
	params := scriptParams(nil)
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(params))
	svc := newTestService(t, st, deps, &testClock{t: time.Now()})

	if _, err := svc.GenerateScript(context.Background(), "w", testScriptNode("parse", params), "page", true, 0, nil); !errors.Is(err, ErrGuardianUnavailable) {
		t.Fatalf("err=%v want ErrGuardianUnavailable", err)
	}
}

// convertGraph builds a trigger.manual -> agent node graph.
func convertGraph(params map[string]any) Graph {
	return Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", nil),
			wfNode("write", "agent", "Write", params),
		},
		Edges: []Edge{wfEdge("start", "out", "write")},
	}
}

// API-SP-004, REQ-SP-017: Convert proposes a script_parse node that keeps the id
// and position, derives the goal and input, and passes the stored run's check.
func TestServiceConvertNodeFeasible(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "g", contains: "This replaces an agent step",
			text: `{"feasible":true,"reason":"","script":"function parse(input, ctx) { return {title: 'hello'}; }","goal":"extract the title","input":"{{ input.text }}","output_schema":{"title":"string"}}`, tokens: 9},
	}
	params := map[string]any{"prompt": "Summarize {{ input.text }}", "output_schema": map[string]any{"title": "string"}}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, convertGraph(params))
	svc := newTestService(t, st, deps, &testClock{t: time.Now()})
	ctx := context.Background()

	run := seedRunAt(t, st, wf, "manual", RunSucceeded, svc.now().UnixMilli(), map[string]any{"text": "hi"})
	putServiceNodeRun(t, st, run, "write", NodeSucceeded, "", 1000,
		map[string]any{"prompt": "Summarize hi"}, map[string]any{"title": "hello"})

	res, err := svc.ConvertNode(ctx, "w", "write")
	if err != nil {
		t.Fatalf("ConvertNode: %v", err)
	}
	if !res.Feasible || res.Node == nil {
		t.Fatalf("result=%+v", res)
	}
	if res.Node.ID != "write" || res.Node.Type != "script_parse" || res.Node.Name != "Write" {
		t.Fatalf("node=%+v", res.Node)
	}
	if got := paramString(res.Node.Params, "input"); got != "{{ input.text }}" {
		t.Fatalf("input=%q", got)
	}
	if got := paramString(res.Node.Params, "goal"); got != "extract the title" {
		t.Fatalf("goal=%q", got)
	}
	if scriptRev(res.Node.Params) != 1 {
		t.Fatalf("script_rev=%v want 1", res.Node.Params["script_rev"])
	}
	if got := paramFloat(res.Node.Params, "baseline_tokens", 0); got != 1000 {
		t.Fatalf("baseline_tokens=%v want 1000", got)
	}
	if len(res.Checks) != 1 || !res.Checks[0].OK || res.Checks[0].Run != run.Number {
		t.Fatalf("checks=%+v", res.Checks)
	}
	if res.Tokens != 9 {
		t.Fatalf("tokens=%d want 9", res.Tokens)
	}
}

// API-SP-004, REQ-SP-017: a guardian infeasible answer returns the reason and no
// node.
func TestServiceConvertNodeInfeasible(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	deps.replies = []fakeReply{
		{agent: "g", contains: "This replaces an agent step", text: `{"feasible":false,"reason":"real summarization"}`, tokens: 3},
	}
	params := map[string]any{"prompt": "Summarize {{ input.text }}"}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, convertGraph(params))
	svc := newTestService(t, st, deps, &testClock{t: time.Now()})
	ctx := context.Background()
	run := seedRunAt(t, st, wf, "manual", RunSucceeded, svc.now().UnixMilli(), map[string]any{"text": "hi"})
	putServiceNodeRun(t, st, run, "write", NodeSucceeded, "", 100, map[string]any{"prompt": "Summarize hi"}, map[string]any{"text": "hi"})

	res, err := svc.ConvertNode(ctx, "w", "write")
	if err != nil {
		t.Fatalf("ConvertNode: %v", err)
	}
	if res.Feasible || res.Node != nil || res.Reason != "real summarization" {
		t.Fatalf("result=%+v", res)
	}
}

// API-SP-004: a node that is not agent, bot or worker is ErrNotConvertible.
func TestServiceConvertNodeNotConvertible(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{t: time.Now()})
	g := Graph{Nodes: []Node{
		wfNode("start", "trigger.manual", "Start", nil),
		wfNode("msg", "message", "Message", map[string]any{"text": "hi"}),
	}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	if _, err := svc.ConvertNode(context.Background(), "w", "msg"); !errors.Is(err, ErrNotConvertible) {
		t.Fatalf("err=%v want ErrNotConvertible", err)
	}
}

// API-SP-004: an unknown node id is ErrNotFound and a node with no succeeded
// runs is ErrNoSample.
func TestServiceConvertNodeNoSample(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{t: time.Now()})
	params := map[string]any{"prompt": "Summarize"}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, convertGraph(params))
	ctx := context.Background()

	if _, err := svc.ConvertNode(ctx, "w", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
	if _, err := svc.ConvertNode(ctx, "w", "write"); !errors.Is(err, ErrNoSample) {
		t.Fatalf("err=%v want ErrNoSample", err)
	}
}

// REQ-SP-017: normalizeConvertInput keeps only the {{ }} data reference of a
// proposed input, keeps a clean value, and falls back to the agent prompt's
// references when the value is empty.
func TestNormalizeConvertInput(t *testing.T) {
	hnPrompt := "From this Hacker News front page, list the first 10 stories.\n\n{{ nodes.fetch.output.markdown }}"
	cases := []struct {
		name        string
		input       string
		agentPrompt string
		want        string
	}{
		{
			name:        "prompt with instruction keeps the single ref",
			input:       hnPrompt,
			agentPrompt: hnPrompt,
			want:        "{{ nodes.fetch.output.markdown }}",
		},
		{
			name:  "clean single ref unchanged",
			input: "{{ nodes.fetch.output.markdown }}",
			want:  "{{ nodes.fetch.output.markdown }}",
		},
		{
			name:  "two clean refs kept",
			input: "{{ nodes.a.output.markdown }}\n\n{{ nodes.b.output.output }}",
			want:  "{{ nodes.a.output.markdown }}\n\n{{ nodes.b.output.output }}",
		},
		{
			name:        "empty input takes the single ref of the prompt",
			input:       "",
			agentPrompt: hnPrompt,
			want:        "{{ nodes.fetch.output.markdown }}",
		},
		{
			name:        "empty input joins the prompt refs with a blank line",
			input:       "   ",
			agentPrompt: "Compare {{ nodes.a.output.markdown }} with {{ nodes.b.output.output }}.",
			want:        "{{ nodes.a.output.markdown }}\n\n{{ nodes.b.output.output }}",
		},
		{
			name:        "empty input with no prompt ref stays empty",
			input:       "",
			agentPrompt: "Summarize the page.",
			want:        "",
		},
		{
			name:  "two refs with text are left alone",
			input: "Merge {{ nodes.a.output.markdown }} and {{ nodes.b.output.output }}",
			want:  "Merge {{ nodes.a.output.markdown }} and {{ nodes.b.output.output }}",
		},
	}
	for _, tc := range cases {
		if got := normalizeConvertInput(tc.input, tc.agentPrompt); got != tc.want {
			t.Fatalf("%s: normalizeConvertInput = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// API-SP-004, REQ-SP-017: a guardian that echoes the whole agent prompt as the
// input keeps only the {{ }} reference in the proposed node.
func TestServiceConvertNodeNormalizesPromptInput(t *testing.T) {
	st := NewMemStore()
	deps := newFakeDeps()
	prompt := "From this Hacker News front page, list the first 10 stories with title, url and points.\n\n{{ nodes.fetch.output.markdown }}"
	deps.replies = []fakeReply{
		{agent: "g", contains: "This replaces an agent step",
			text: compactJSON(map[string]any{
				"feasible":      true,
				"reason":        "",
				"script":        "function parse(input, ctx) { return {title: 'hello'}; }",
				"goal":          "list the first 10 stories",
				"input":         prompt,
				"output_schema": map[string]any{"title": "string"},
			}), tokens: 9},
	}
	params := map[string]any{"prompt": prompt, "output_schema": map[string]any{"title": "string"}}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, convertGraph(params))
	svc := newTestService(t, st, deps, &testClock{t: time.Now()})
	ctx := context.Background()

	run := seedRunAt(t, st, wf, "manual", RunSucceeded, svc.now().UnixMilli(), map[string]any{"markdown": "hi"})
	putServiceNodeRun(t, st, run, "fetch", NodeSucceeded, "", 0, nil, map[string]any{"markdown": "BODY"})
	putServiceNodeRun(t, st, run, "write", NodeSucceeded, "", 1000,
		map[string]any{"prompt": prompt}, map[string]any{"title": "hello"})

	res, err := svc.ConvertNode(ctx, "w", "write")
	if err != nil {
		t.Fatalf("ConvertNode: %v", err)
	}
	if !res.Feasible || res.Node == nil {
		t.Fatalf("result=%+v", res)
	}
	if got := paramString(res.Node.Params, "input"); got != "{{ nodes.fetch.output.markdown }}" {
		t.Fatalf("input=%q want only the reference", got)
	}
	if len(res.Checks) != 1 || !res.Checks[0].OK {
		t.Fatalf("checks=%+v", res.Checks)
	}
}

// API-SP-005, REQ-SP-018: Suggestions list exactly the enabled agent, bot and
// worker nodes with at least 3 succeeded runs in 30 days and 500 tokens per run
// on average.
func TestServiceScriptSuggestionsThresholds(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{t: time.Now()})
	ctx := context.Background()
	g := Graph{Nodes: []Node{
		wfNode("start", "trigger.manual", "Start", nil),
		{ID: "a", Type: "agent", Name: "A", Params: map[string]any{"prompt": "p"}},
		{ID: "b", Type: "bot", Name: "B", Params: map[string]any{"prompt": "p"}},
		{ID: "c", Type: "worker", Name: "C", Params: map[string]any{"task": "t"}},
		{ID: "d", Type: "agent", Name: "D", Disabled: true, Params: map[string]any{"prompt": "p"}},
		wfNode("msg", "message", "Message", map[string]any{"text": "hi"}),
	}}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	now := svc.now().UnixMilli()
	runs := []Run{
		seedRunAt(t, st, wf, "manual", RunSucceeded, now-3_000, nil),
		seedRunAt(t, st, wf, "manual", RunSucceeded, now-2_000, nil),
		seedRunAt(t, st, wf, "manual", RunSucceeded, now-1_000, nil),
	}
	for i, run := range runs {
		putServiceNodeRun(t, st, run, "a", NodeSucceeded, "", 600, nil, map[string]any{"text": "x"})
		putServiceNodeRun(t, st, run, "c", NodeSucceeded, "", 100, nil, map[string]any{"text": "x"})
		putServiceNodeRun(t, st, run, "d", NodeSucceeded, "", 600, nil, map[string]any{"text": "x"})
		if i < 2 {
			putServiceNodeRun(t, st, run, "b", NodeSucceeded, "", 600, nil, map[string]any{"text": "x"})
		}
	}

	got, err := svc.ScriptSuggestions(ctx, "w")
	if err != nil {
		t.Fatalf("ScriptSuggestions: %v", err)
	}
	if len(got) != 1 || got[0].NodeID != "a" {
		t.Fatalf("suggestions=%+v want only a", got)
	}
	if got[0].Type != "agent" || got[0].Runs != 3 || got[0].AvgTokens != 600 || got[0].EstTokens30d != 1800 {
		t.Fatalf("suggestion=%+v", got[0])
	}
}

// API-SP-009, REQ-SP-019: the stats count the script and fallback node runs,
// scale tokens saved by baseline_tokens and carry the newest script decisions.
func TestServiceScriptStats(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{t: time.Now()})
	ctx := context.Background()
	params := scriptParams(map[string]any{"script": "", "baseline_tokens": 100})
	g := scriptGraph(params)
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	now := svc.now().UnixMilli()

	run1 := seedRunAt(t, st, wf, "manual", RunSucceeded, now-3_000, nil)
	run2 := seedRunAt(t, st, wf, "manual", RunSucceeded, now-2_000, nil)
	run3 := seedRunAt(t, st, wf, "manual", RunSucceeded, now-1_000, nil)
	putServiceNodeRun(t, st, run1, "parse", NodeSucceeded, ViaScript, 0, nil, map[string]any{"title": "a"})
	putServiceNodeRun(t, st, run2, "parse", NodeSucceeded, ViaScript, 0, nil, map[string]any{"title": "b"})
	putServiceNodeRun(t, st, run3, "parse", NodeSucceeded, ViaFallback, 50, nil, map[string]any{"title": "c"})

	for _, d := range []Decision{
		{ID: "d_upd2", WorkflowID: "w", NodeID: "parse", Kind: "script", Action: "script_update", Status: "applied", CreatedAt: 500},
		{ID: "d_fb", WorkflowID: "w", NodeID: "parse", Kind: "script", Action: "script_fallback", Status: "applied", CreatedAt: 400},
		{ID: "d_upd1", WorkflowID: "w", NodeID: "parse", Kind: "script", Action: "script_update", Status: "applied", CreatedAt: 300},
		{ID: "d_other", WorkflowID: "w", NodeID: "parse", Kind: "guardian", Status: "applied", CreatedAt: 900},
	} {
		if err := st.PutDecision(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	stats := svc.ScriptStats(ctx, "w", g)
	stat, ok := stats["parse"]
	if !ok {
		t.Fatalf("stats=%+v want the parse node", stats)
	}
	if stat.ScriptRuns != 2 || stat.FallbackRuns != 1 || stat.TokensSaved != 200 {
		t.Fatalf("stat=%+v", stat)
	}
	if stat.LastUpdate != 500 {
		t.Fatalf("last_update=%d want 500", stat.LastUpdate)
	}
	if len(stat.Events) != 3 || stat.Events[0].CreatedAt != 500 {
		t.Fatalf("events=%+v", stat.Events)
	}

	// Service.Get fills the detail stats.
	detail, err := svc.Get(ctx, "w")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got, ok := detail.ScriptStats["parse"]; !ok || got.TokensSaved != 200 {
		t.Fatalf("detail script_stats=%+v", detail.ScriptStats)
	}
}

// API-SP-009: the detail omits script_stats when the graph has no script_parse
// node.
func TestServiceGetOmitsScriptStats(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{t: time.Now()})
	g := Graph{Nodes: []Node{wfNode("start", "trigger.manual", "Start", nil)}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)

	detail, err := svc.Get(context.Background(), "w")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if detail.ScriptStats != nil {
		t.Fatalf("script_stats=%+v want nil", detail.ScriptStats)
	}
}
