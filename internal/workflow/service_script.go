package workflow

// This file holds the Script Parse service operations of spec #44
// (REQ-SP-015..019, API-SP-002..005, API-SP-009): Test, Generate, Convert,
// Suggestions and the per-node stats of the workflow detail. They reuse the
// engine's script evaluation (evalScript) and the guardian prompts of
// script_prompts.go. Test, Convert and Suggestions never write; Generate only
// resets the regeneration pause in memory (REQ-SP-016).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Script Parse service errors.
var (
	// ErrNoSample means a node has no usable sample input (REQ-SP-015,
	// API-SP-004).
	ErrNoSample = errors.New("workflow: no sample input")
	// ErrNotScriptNode means Test was asked for a node that is not script_parse
	// (API-SP-002).
	ErrNotScriptNode = errors.New("workflow: node is not script_parse")
	// ErrNotConvertible means Convert was asked for a node that is not an agent,
	// bot or worker (REQ-SP-017, API-SP-004).
	ErrNotConvertible = errors.New("workflow: node type cannot be converted")
)

// Script Parse service limits and windows (REQ-SP-015, REQ-SP-017, REQ-SP-018,
// REQ-SP-019).
const (
	// scriptSampleScanRuns bounds the runs scanned for a stored sample
	// (REQ-SP-015).
	scriptSampleScanRuns = 20
	// scriptConvertScanRuns bounds the runs scanned for a convertible node's
	// succeeded runs (REQ-SP-017).
	scriptConvertScanRuns = 50
	// scriptConvertRuns is how many succeeded runs Convert checks against
	// (REQ-SP-017).
	scriptConvertRuns = 3
	// scriptSuggestionRuns bounds the runs Suggestions scans (REQ-SP-018).
	scriptSuggestionRuns = 200
	// scriptSuggestionWindow is the look-back window of Suggestions
	// (REQ-SP-018).
	scriptSuggestionWindow = 30 * 24 * time.Hour
	// scriptSuggestionMinRuns and scriptSuggestionMinTokens gate a suggestion
	// row (REQ-SP-018).
	scriptSuggestionMinRuns   = 3
	scriptSuggestionMinTokens = 500
	// scriptStatsRuns bounds the runs the detail stats scan (REQ-SP-019).
	scriptStatsRuns = 1000
	// scriptStatsEvents is how many newest script decisions one node's stats
	// carry (API-SP-009).
	scriptStatsEvents = 20
)

// ScriptProblem is one problem of a script evaluation (API-SP-002, API-SP-003):
// a drift code (script_error, schema_drift, empty_drift, item_drift) or
// "agreement", with a human message.
type ScriptProblem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Sample names where a Test sample came from (API-SP-002): the request or one
// run of the workflow.
type Sample struct {
	Source string `json:"source"` // request | run
	Run    int    `json:"run,omitempty"`
}

// TestResult is the response of API-SP-002.
type TestResult struct {
	OK         bool            `json:"ok"`
	Output     any             `json:"output"`
	Problems   []ScriptProblem `json:"problems"`
	Logs       []string        `json:"logs"`
	DurationMS int64           `json:"duration_ms"`
	Sample     Sample          `json:"sample"`
}

// GenerateResult is the response of API-SP-003. Agreement is set, and non-nil,
// only when an expected output was available.
type GenerateResult struct {
	Feasible  bool            `json:"feasible"`
	Reason    string          `json:"reason"`
	Script    string          `json:"script"`
	Output    any             `json:"output"`
	Problems  []ScriptProblem `json:"problems"`
	Agreement *float64        `json:"agreement,omitempty"`
	Tokens    int64           `json:"tokens"`
}

// ConvertCheck is one per-run check of a converted script (API-SP-004): Run is
// the run number, OK is true when the script passed the contract and agreed with
// that run's output.
type ConvertCheck struct {
	Run       int             `json:"run"`
	OK        bool            `json:"ok"`
	Agreement float64         `json:"agreement"`
	Problems  []ScriptProblem `json:"problems"`
}

// ConvertResult is the response of API-SP-004. Node is set only when the
// guardian found the step feasible.
type ConvertResult struct {
	Feasible bool           `json:"feasible"`
	Reason   string         `json:"reason"`
	Node     *Node          `json:"node,omitempty"`
	Checks   []ConvertCheck `json:"checks"`
	Tokens   int64          `json:"tokens"`
}

// Suggestion is one row of API-SP-005: an agent, bot or worker node that looks
// like a good Script Parse candidate.
type Suggestion struct {
	NodeID       string `json:"node_id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Runs         int    `json:"runs"`
	AvgTokens    int64  `json:"avg_tokens"`
	EstTokens30d int64  `json:"est_tokens_30d"`
}

// ScriptStat is the per-node stats object of API-SP-009.
type ScriptStat struct {
	ScriptRuns   int        `json:"script_runs"`
	FallbackRuns int        `json:"fallback_runs"`
	TokensSaved  int64      `json:"tokens_saved"`
	LastUpdate   int64      `json:"last_update"`
	Events       []Decision `json:"events"`
}

// TestScript runs a node's script (saved or draft) on a sample and returns the
// output or the problems, without tokens or storage (REQ-SP-015, API-SP-002). The
// node must be a script_parse node; an empty script is the problem
// script_error: script is empty. The sample comes from the request input, else
// from the node's stored rendered input in a run, else from the node's input
// template rendered against the newest run's stored outputs.
func (s *Service) TestScript(ctx context.Context, wfID string, node Node, input any, hasInput bool, run int) (TestResult, error) {
	if node.Type != "script_parse" {
		return TestResult{}, ErrNotScriptNode
	}
	sample, smp, _, err := s.scriptSample(ctx, wfID, node, input, hasInput, run)
	if err != nil {
		return TestResult{}, err
	}
	res := TestResult{Problems: []ScriptProblem{}, Logs: []string{}, Sample: smp}
	script := paramString(node.Params, "script")
	if strings.TrimSpace(script) == "" {
		res.Problems = []ScriptProblem{{Code: DriftScript, Message: "script is empty"}}
		return res, nil
	}
	out, logs, problems, dur := evalScript(ctx, node.Params, script, sample, scriptTimeout(node.Params))
	res.OK = len(problems) == 0
	res.Output = out
	if problems != nil {
		res.Problems = problems
	}
	if logs != nil {
		res.Logs = logs
	}
	res.DurationMS = dur.Milliseconds()
	return res, nil
}

// GenerateScript asks the guardian for a script for a node (saved or draft) and
// a sample and validates it without saving (REQ-SP-016, API-SP-003). It takes the
// expected output from the request, else from the node's output in the sample
// run when that output came from the fallback or the node is not a script_parse
// node. It makes up to two guardian calls (the second carries the problems of
// the first attempt, REQ-SP-007). A successful Generate resets the regeneration
// pause (REQ-SP-016).
func (s *Service) GenerateScript(ctx context.Context, wfID string, node Node, input any, hasInput bool, run int, expected map[string]any) (GenerateResult, error) {
	wf, err := s.store.GetWorkflow(ctx, wfID)
	if err != nil {
		return GenerateResult{}, err
	}
	if !s.deps.AgentExists(wf.Guardian) {
		return GenerateResult{}, ErrGuardianUnavailable
	}
	sample, _, sampleRun, err := s.scriptSample(ctx, wfID, node, input, hasInput, run)
	if err != nil {
		return GenerateResult{}, err
	}
	expectedVal, hasExpected := s.scriptExpected(ctx, node, sampleRun, expected)

	params := node.Params
	timeout := scriptTimeout(params)
	empty := []ScriptProblem{}
	var tokens int64
	var problems []ScriptProblem
	var script string
	var agreement float64

	for attempt := 0; attempt < scriptRegenAttempts; attempt++ {
		call, cerr := s.deps.RunAgent(ctx, AgentRequest{
			WorkflowID: wfID,
			NodeID:     node.ID,
			Agent:      wf.Guardian,
			Prompt: BuildScriptPrompt(ScriptPromptInput{
				Goal:       paramString(params, "goal"),
				Schema:     asMapParam(params["output_schema"]),
				ItemSchema: asMapParam(params["item_schema"]),
				AllowEmpty: stringList(params["allow_empty"]),
				Input:      sample,
				Expected:   expectedVal,
				Problems:   problemStrings(problems),
			}),
			Tools: []string{},
		})
		tokens += call.Tokens
		if cerr != nil {
			return GenerateResult{}, fmt.Errorf("%w: %v", ErrGuardianUnavailable, cerr)
		}
		if call.StopReason == "error" {
			return GenerateResult{}, fmt.Errorf("%w: the guardian stopped with an error", ErrGuardianUnavailable)
		}
		reply, perr := ParseScriptReply(call.Text)
		if perr != nil {
			return GenerateResult{}, fmt.Errorf("%w: %v", ErrGuardianUnavailable, perr)
		}
		if !reply.Feasible {
			reason := reply.Reason
			if reason == "" {
				reason = "the guardian reported the step is not feasible"
			}
			return GenerateResult{Feasible: false, Reason: reason, Problems: empty, Tokens: tokens}, nil
		}
		script = reply.Script
		out, _, probs, _ := evalScript(ctx, params, script, sample, timeout)
		if hasExpected {
			agreement, _ = Agreement(expectedVal, out)
			if len(probs) == 0 {
				if ap := scriptAgreementProblem(expectedVal, out); ap != nil {
					probs = []ScriptProblem{*ap}
				}
			}
		}
		problems = probs
		if len(problems) == 0 {
			res := GenerateResult{Feasible: true, Script: script, Output: out, Problems: empty, Tokens: tokens}
			if hasExpected {
				a := agreement
				res.Agreement = &a
			}
			s.engine.ResetScriptPause(wfID, node.ID)
			return res, nil
		}
	}

	reason := ""
	if len(problems) > 0 {
		reason = problems[0].Code + ": " + problems[0].Message
	}
	res := GenerateResult{Feasible: false, Reason: reason, Script: script, Problems: problems, Tokens: tokens}
	if hasExpected {
		a := agreement
		res.Agreement = &a
	}
	return res, nil
}

// ConvertNode proposes a script_parse node that replaces an agent, bot or worker
// node and keeps its id (REQ-SP-017, API-SP-004). The guardian derives the goal
// and the input template from the node's prompt and writes the script; up to
// three of the node's newest succeeded runs are used as expected outputs for
// per-run checks. A feasible=false answer returns the reason and no node.
func (s *Service) ConvertNode(ctx context.Context, wfID, nodeID string) (ConvertResult, error) {
	wf, err := s.store.GetWorkflow(ctx, wfID)
	if err != nil {
		return ConvertResult{}, err
	}
	ver, err := s.store.GetVersion(ctx, wfID, wf.Version)
	if err != nil {
		return ConvertResult{}, err
	}
	var node Node
	found := false
	for _, n := range ver.Graph.Nodes {
		if n.ID == nodeID {
			node = n
			found = true
			break
		}
	}
	if !found {
		return ConvertResult{}, ErrNotFound
	}
	switch node.Type {
	case "agent", "bot", "worker":
	default:
		return ConvertResult{}, ErrNotConvertible
	}
	if !s.deps.AgentExists(wf.Guardian) {
		return ConvertResult{}, ErrGuardianUnavailable
	}

	samples := s.convertSamples(ctx, wfID, nodeID)
	if len(samples) == 0 {
		return ConvertResult{}, ErrNoSample
	}
	newest := samples[0]

	// The agent's data is embedded in its prompt, so the raw prompt template is
	// the conversion source and the newest run's rendered prompt is the sample
	// (REQ-SP-017).
	rawPrompt := paramString(node.Params, "prompt")
	if node.Type == "worker" {
		rawPrompt = paramString(node.Params, "task")
	}
	call, cerr := s.deps.RunAgent(ctx, AgentRequest{
		WorkflowID: wfID,
		NodeID:     node.ID,
		Agent:      wf.Guardian,
		Prompt: BuildScriptPrompt(ScriptPromptInput{
			AgentPrompt: rawPrompt,
			Schema:      asMapParam(node.Params["output_schema"]),
			ItemSchema:  asMapParam(node.Params["item_schema"]),
			Input:       renderedPromptText(newest.nodeRun.Input),
			Expected:    decodeInput(newest.nodeRun.Output),
		}),
		Tools: []string{},
	})
	tokens := call.Tokens
	if cerr != nil {
		return ConvertResult{}, fmt.Errorf("%w: %v", ErrGuardianUnavailable, cerr)
	}
	if call.StopReason == "error" {
		return ConvertResult{}, fmt.Errorf("%w: the guardian stopped with an error", ErrGuardianUnavailable)
	}
	reply, perr := ParseScriptReply(call.Text)
	if perr != nil {
		return ConvertResult{}, fmt.Errorf("%w: %v", ErrGuardianUnavailable, perr)
	}
	if !reply.Feasible {
		reason := reply.Reason
		if reason == "" {
			reason = "the guardian reported the step is not feasible"
		}
		return ConvertResult{Feasible: false, Reason: reason, Checks: []ConvertCheck{}, Tokens: tokens}, nil
	}
	reply.Input = normalizeConvertInput(reply.Input, rawPrompt)

	schema := reply.OutputSchema
	if schema == nil {
		schema = asMapParam(node.Params["output_schema"])
	}
	if schema == nil {
		schema = map[string]any{"text": "string"}
	}
	var sum int64
	for _, smp := range samples {
		sum += smp.nodeRun.Tokens
	}
	proposed := Node{
		ID: node.ID, Type: "script_parse", Name: node.Name, Parent: node.Parent,
		Position: node.Position, OnFailure: node.OnFailure,
		Params: map[string]any{
			"input":           reply.Input,
			"goal":            reply.Goal,
			"output_schema":   schema,
			"script":          reply.Script,
			"script_rev":      1,
			"baseline_tokens": sum / int64(len(samples)),
			"on_drift":        "regenerate",
		},
	}

	// Check the proposal against each collected run (REQ-SP-017): render the
	// input template against that run's stored outputs, else use its rendered
	// prompt, then compare the script output with the run's output.
	timeout := scriptTimeout(proposed.Params)
	checks := make([]ConvertCheck, 0, len(samples))
	for _, smp := range samples {
		fallback := renderedPromptText(smp.nodeRun.Input)
		var in any = fallback
		if scope, serr := s.storedScope(ctx, wfID, smp.run); serr == nil {
			in = convertCheckInput(reply.Input, scope, fallback)
		}
		out, _, probs, _ := evalScript(ctx, proposed.Params, reply.Script, in, timeout)
		agreement, _ := Agreement(decodeInput(smp.nodeRun.Output), out)
		check := ConvertCheck{Run: smp.run.Number, OK: len(probs) == 0 && agreement >= scriptAgreementMin, Agreement: agreement, Problems: []ScriptProblem{}}
		if probs != nil {
			check.Problems = probs
		}
		checks = append(checks, check)
	}
	return ConvertResult{Feasible: true, Node: &proposed, Checks: checks, Tokens: tokens}, nil
}

// convertSample is one succeeded run of a node that Convert uses as an expected
// output (REQ-SP-017).
type convertSample struct {
	run     Run
	nodeRun NodeRun
}

// convertSamples collects up to scriptConvertRuns of a node's newest succeeded
// runs, scanning at most scriptConvertScanRuns runs (REQ-SP-017).
func (s *Service) convertSamples(ctx context.Context, wfID, nodeID string) []convertSample {
	runs, err := s.store.ListRuns(ctx, RunFilter{WorkflowID: wfID, Limit: scriptConvertScanRuns})
	if err != nil {
		return nil
	}
	var out []convertSample
	for _, run := range runs {
		if len(out) >= scriptConvertRuns {
			break
		}
		nrs, err := s.store.ListNodeRuns(ctx, run.ID)
		if err != nil {
			continue
		}
		for _, nr := range nrs {
			if nr.NodeID != nodeID || nr.Status != NodeSucceeded || len(nr.Output) == 0 {
				continue
			}
			out = append(out, convertSample{run: run, nodeRun: nr})
			break
		}
	}
	return out
}

// ScriptSuggestions lists the agent, bot and worker nodes of the current version
// that are enabled, ran at least 3 times with a succeeded node run in the last
// 30 days and used at least 500 tokens per run on average (REQ-SP-018,
// API-SP-005). It uses no LLM and scans at most the newest 200 runs.
func (s *Service) ScriptSuggestions(ctx context.Context, wfID string) ([]Suggestion, error) {
	wf, err := s.store.GetWorkflow(ctx, wfID)
	if err != nil {
		return nil, err
	}
	ver, err := s.store.GetVersion(ctx, wfID, wf.Version)
	if err != nil {
		return nil, err
	}
	types := map[string]string{}
	names := map[string]string{}
	var order []string
	for _, n := range ver.Graph.Nodes {
		switch n.Type {
		case "agent", "bot", "worker":
			if n.Disabled {
				continue
			}
			types[n.ID] = n.Type
			names[n.ID] = nodeName(n)
			order = append(order, n.ID)
		}
	}
	suggestions := []Suggestion{}
	if len(types) == 0 {
		return suggestions, nil
	}

	since := s.now().Add(-scriptSuggestionWindow).UnixMilli()
	runs, _ := s.store.ListRuns(ctx, RunFilter{WorkflowID: wfID, Since: since, Limit: scriptSuggestionRuns})
	type acc struct {
		runs   int
		tokens int64
	}
	totals := map[string]*acc{}
	for _, run := range runs {
		nrs, err := s.store.ListNodeRuns(ctx, run.ID)
		if err != nil {
			continue
		}
		for _, nr := range nrs {
			if _, ok := types[nr.NodeID]; !ok || nr.Status != NodeSucceeded {
				continue
			}
			a := totals[nr.NodeID]
			if a == nil {
				a = &acc{}
				totals[nr.NodeID] = a
			}
			a.runs++
			a.tokens += nr.Tokens
		}
	}
	for _, id := range order {
		a := totals[id]
		if a == nil || a.runs < scriptSuggestionMinRuns {
			continue
		}
		avg := a.tokens / int64(a.runs)
		if avg < scriptSuggestionMinTokens {
			continue
		}
		suggestions = append(suggestions, Suggestion{
			NodeID: id, Name: names[id], Type: types[id],
			Runs: a.runs, AvgTokens: avg, EstTokens30d: avg * int64(a.runs),
		})
	}
	return suggestions, nil
}

// ScriptStats builds the per-node stats of every script_parse node of the graph
// (REQ-SP-019, API-SP-009): the script and fallback run counts over the newest
// runs, tokens saved = script runs × baseline_tokens, the last script_update
// time and the newest 20 kind-script decisions for the node. It returns nil when
// the graph has no script_parse node.
func (s *Service) ScriptStats(ctx context.Context, wfID string, g Graph) map[string]ScriptStat {
	params := map[string]map[string]any{}
	for _, n := range g.Nodes {
		if n.Type == "script_parse" {
			params[n.ID] = n.Params
		}
	}
	if len(params) == 0 {
		return nil
	}
	stats := make(map[string]ScriptStat, len(params))
	for id := range params {
		stats[id] = ScriptStat{Events: []Decision{}}
	}

	runs, _ := s.store.ListRuns(ctx, RunFilter{WorkflowID: wfID, Limit: scriptStatsRuns})
	counts := map[string]*[2]int{}
	for _, run := range runs {
		nrs, err := s.store.ListNodeRuns(ctx, run.ID)
		if err != nil {
			continue
		}
		for _, nr := range nrs {
			if _, ok := params[nr.NodeID]; !ok {
				continue
			}
			c := counts[nr.NodeID]
			if c == nil {
				c = &[2]int{}
				counts[nr.NodeID] = c
			}
			switch nr.Via {
			case ViaScript:
				c[0]++
			case ViaFallback:
				c[1]++
			}
		}
	}

	// ListDecisions returns newest first, so the first 20 per node are the
	// newest (API-SP-009).
	decs, _ := s.store.ListDecisions(ctx, DecisionFilter{WorkflowID: wfID})
	events := map[string]int{}
	for _, d := range decs {
		if d.Kind != "script" {
			continue
		}
		st, ok := stats[d.NodeID]
		if !ok {
			continue
		}
		if n := events[d.NodeID]; n < scriptStatsEvents {
			st.Events = append(st.Events, d)
			events[d.NodeID] = n + 1
		}
		if d.Action == "script_update" && d.CreatedAt > st.LastUpdate {
			st.LastUpdate = d.CreatedAt
		}
		stats[d.NodeID] = st
	}

	for id, p := range params {
		st := stats[id]
		if c := counts[id]; c != nil {
			st.ScriptRuns = c[0]
			st.FallbackRuns = c[1]
		}
		st.TokensSaved = int64(st.ScriptRuns) * int64(paramFloat(p, "baseline_tokens", 0))
		stats[id] = st
	}
	return stats
}

// graphHasScriptParse reports whether a graph holds a script_parse node, so
// Service.Get only computes the detail stats when there is something to report
// (API-SP-009).
func graphHasScriptParse(g Graph) bool {
	for _, n := range g.Nodes {
		if n.Type == "script_parse" {
			return true
		}
	}
	return false
}

// scriptSample resolves the sample input of Test and Generate (REQ-SP-015): the
// request input, else the node's stored rendered input in the requested run or
// the newest runs, else the node's input template rendered against the newest
// run's stored outputs. It returns the value, the sample source and the run the
// value came from.
func (s *Service) scriptSample(ctx context.Context, wfID string, node Node, input any, hasInput bool, run int) (any, Sample, Run, error) {
	if hasInput {
		return input, Sample{Source: "request"}, Run{}, nil
	}
	var newest []Run
	if runs, err := s.store.ListRuns(ctx, RunFilter{WorkflowID: wfID, Limit: scriptSampleScanRuns}); err == nil {
		newest = runs
	}
	var ordered []Run
	if run > 0 {
		r, err := s.store.GetRunByNumber(ctx, wfID, run)
		if err != nil {
			return nil, Sample{}, Run{}, err
		}
		ordered = append(ordered, r)
	}
	ordered = append(ordered, newest...)
	for _, r := range ordered {
		if v, ok := s.storedNodeInput(ctx, r.ID, node.ID); ok {
			return v, Sample{Source: "run", Run: r.Number}, r, nil
		}
	}
	// The node never ran: render its input template against the newest run's
	// stored outputs (REQ-SP-015).
	if len(newest) == 0 {
		return nil, Sample{}, Run{}, ErrNoSample
	}
	newestRun := newest[0]
	scope, serr := s.storedScope(ctx, wfID, newestRun)
	if serr != nil {
		return nil, Sample{}, Run{}, ErrNoSample
	}
	raw, ok := TemplateParams(node)["input"]
	if !ok {
		return nil, Sample{}, Run{}, ErrNoSample
	}
	rendered, rerr := Render(raw, scope)
	if rerr != nil {
		return nil, Sample{}, Run{}, ErrNoSample
	}
	return rendered, Sample{Source: "run", Run: newestRun.Number}, newestRun, nil
}

// scriptExpected resolves the expected output of Generate (REQ-SP-016): the
// request value, else the node's stored output in the sample run when that row
// came from the fallback or the node is not a script_parse node.
func (s *Service) scriptExpected(ctx context.Context, node Node, sampleRun Run, expected map[string]any) (any, bool) {
	if expected != nil {
		return expected, true
	}
	if sampleRun.ID == "" {
		return nil, false
	}
	nr, ok := s.nodeRunFor(ctx, sampleRun.ID, node.ID)
	if !ok || len(nr.Output) == 0 {
		return nil, false
	}
	if node.Type != "script_parse" || nr.Via == ViaFallback {
		return decodeInput(nr.Output), true
	}
	return nil, false
}

// storedNodeInput returns the rendered "input" param of a node in a run, and
// whether that run ran the node (REQ-SP-015). A node run's Input is the rendered
// params JSON.
func (s *Service) storedNodeInput(ctx context.Context, runID, nodeID string) (any, bool) {
	nr, ok := s.nodeRunFor(ctx, runID, nodeID)
	if !ok || len(nr.Input) == 0 {
		return nil, false
	}
	var m map[string]any
	if json.Unmarshal(nr.Input, &m) != nil {
		return nil, false
	}
	v, ok := m["input"]
	return v, ok
}

// nodeRunFor returns the stored node run of (run, node), preferring the
// top-level item.
func (s *Service) nodeRunFor(ctx context.Context, runID, nodeID string) (NodeRun, bool) {
	nrs, err := s.store.ListNodeRuns(ctx, runID)
	if err != nil {
		return NodeRun{}, false
	}
	var chosen NodeRun
	found := false
	for _, nr := range nrs {
		if nr.NodeID != nodeID {
			continue
		}
		if nr.ItemIndex == -1 {
			return nr, true
		}
		if !found {
			chosen = nr
			found = true
		}
	}
	return chosen, found
}

// storedScope rebuilds the render scope of a stored run the way the engine
// builds it (design #44, "Sample resolution"): the succeeded and fallback node
// outputs, the run input, the workflow state and the run and workflow metadata.
func (s *Service) storedScope(ctx context.Context, wfID string, run Run) (Scope, error) {
	nrs, err := s.store.ListNodeRuns(ctx, run.ID)
	if err != nil {
		return Scope{}, err
	}
	nodes := map[string]any{}
	for _, nr := range nrs {
		if nr.ItemIndex != -1 {
			continue
		}
		switch nr.Status {
		case NodeSucceeded, NodeFallback:
			nodes[nr.NodeID] = decodeInput(nr.Output)
		}
	}
	wf, err := s.store.GetWorkflow(ctx, wfID)
	if err != nil {
		return Scope{}, err
	}
	state := map[string]any{}
	if ents, err := s.store.ListState(ctx, wfID); err == nil {
		for _, en := range ents {
			state[en.Key] = normalize(en.Value)
		}
	}
	return Scope{
		Input:    decodeInput(run.Input),
		Nodes:    nodes,
		State:    state,
		Run:      map[string]any{"id": run.ID, "number": run.Number, "trigger": run.Trigger, "started_at": run.StartedAt},
		Workflow: map[string]any{"id": wf.ID, "name": wf.Name},
	}, nil
}

// renderedPromptText reads the rendered prompt (agent, bot) or task (worker) of
// a node run's stored params (REQ-SP-017).
func renderedPromptText(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for _, key := range []string{"prompt", "task"} {
		if s, ok := m[key].(string); ok {
			return s
		}
	}
	return ""
}

// convertCheckInput renders a conversion's input template against a run's scope;
// when the template is empty or does not resolve to a usable value it returns
// the run's rendered prompt instead (REQ-SP-017).
func convertCheckInput(template string, scope Scope, fallback string) any {
	if strings.TrimSpace(template) != "" {
		if rendered, err := Render(template, scope); err == nil && rendered != nil {
			if s, ok := rendered.(string); !ok || strings.TrimSpace(s) != "" {
				return rendered
			}
		}
	}
	return fallback
}

// normalizeConvertInput cleans the input template a conversion proposed
// (REQ-SP-017). A model that echoes its instructions returns the whole prompt
// instead of the data reference, so when the value carries text beside exactly
// one {{ }} expression only that expression is kept. An empty value falls back to
// the {{ }} expressions of the agent prompt joined with "\n\n"; anything else,
// including an already clean single reference, is returned unchanged. The checks
// then run with the normalized input, so a script that relied on the instruction
// prefix fails visibly.
func normalizeConvertInput(input, agentPrompt string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		toks, err := parseTemplate(agentPrompt)
		if err != nil {
			return input
		}
		var refs []string
		for _, tok := range toks {
			if tok.isExpr {
				refs = append(refs, "{{ "+tok.expr+" }}")
			}
		}
		if len(refs) == 0 {
			return input
		}
		return strings.Join(refs, "\n\n")
	}
	toks, err := parseTemplate(trimmed)
	if err != nil {
		return input
	}
	var expr string
	count := 0
	text := false
	for _, tok := range toks {
		switch {
		case tok.isExpr:
			count++
			expr = tok.expr
		case strings.TrimSpace(tok.lit) != "":
			text = true
		}
	}
	if count == 1 && text {
		return "{{ " + expr + " }}"
	}
	return input
}

// scriptAgreementProblem reports an "agreement" problem when the script output
// disagrees with the expected output (REQ-SP-007): at least
// scriptAgreementLeaves expected leaves and a score below scriptAgreementMin.
func scriptAgreementProblem(expected, got any) *ScriptProblem {
	agreement, leaves := Agreement(expected, got)
	if leaves >= scriptAgreementLeaves && agreement < scriptAgreementMin {
		return &ScriptProblem{
			Code:    "agreement",
			Message: fmt.Sprintf("agreement %.2f is below %.2f", agreement, scriptAgreementMin),
		}
	}
	return nil
}

// problemStrings renders problems as "<code>: <message>" lines for a prompt
// (REQ-SP-007).
func problemStrings(problems []ScriptProblem) []string {
	if len(problems) == 0 {
		return nil
	}
	out := make([]string, len(problems))
	for i, p := range problems {
		out[i] = p.Code + ": " + p.Message
	}
	return out
}

// scriptTimeout reads a node's timeout_ms param as a duration, clamped to the
// 100..10000 millisecond range of REQ-SP-002.
func scriptTimeout(params map[string]any) time.Duration {
	return time.Duration(clampScriptTimeoutMs(paramFloat(params, "timeout_ms", scriptDefaultTimeoutMs))) * time.Millisecond
}
