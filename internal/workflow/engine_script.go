package workflow

// This file holds the script_parse node handler of the run engine (spec #44,
// REQ-SP-002..012, design section "Engine (engine_script.go)"). The handler runs
// a guardian-written JavaScript parser in the embedded goja sandbox
// (internal/scriptrt), checks its output against the node contract
// (scriptcheck.go), and, on drift or an empty script, runs one agent fallback.
// When on_drift is regenerate (the default) it then asks the guardian for a
// repaired script, validates it, saves it as a new workflow version and notifies
// the owner; the owner can revert an applied update through Answer.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/scriptrt"
)

// Script parse defaults and limits (REQ-SP-001, REQ-SP-002, REQ-SP-007,
// REQ-SP-009, REQ-SP-011).
const (
	scriptDefaultTimeoutMs = 2000
	scriptMinTimeoutMs     = 100
	scriptMaxTimeoutMs     = 10000
	// scriptNoticeThrottle is how long a script_fallback notice for the same
	// node is silenced (REQ-SP-011); it also bounds the regeneration pause
	// window (REQ-SP-009).
	scriptNoticeThrottle = 24 * time.Hour
	// scriptNoScriptCode and scriptNoScriptMessage describe an empty script,
	// which learns on the first run instead of failing (REQ-SP-005, REQ-SP-006).
	scriptNoScriptCode    = "no_script"
	scriptNoScriptMessage = "script is empty; learning"
	// scriptRegenAttempts is how many guardian calls one regeneration makes
	// (REQ-SP-007): one initial attempt plus one retry carrying the problems of
	// the rejected candidate.
	scriptRegenAttempts = 2
	// scriptAgreementMin and scriptAgreementLeaves gate an accepted candidate:
	// the script output must agree with the fallback output by at least 0.8 when
	// the fallback output has at least 5 leaves (REQ-SP-007).
	scriptAgreementMin    = 0.8
	scriptAgreementLeaves = 5
	// scriptRegenPauseAfter is how many rejected regenerations for a node in
	// 24 h pause further regeneration until the owner saves a change to it
	// (REQ-SP-009).
	scriptRegenPauseAfter = 3
)

// scriptCompile caches one Compile result per script source within a run.
type scriptCompile struct {
	prog *scriptrt.Program
	err  error
}

// scriptParseNode runs one script_parse node (REQ-SP-002..012). It runs the
// script and checks its output against the contract; on drift, or when the
// script is empty, it runs the fallback agent, records a script decision and
// notifies the owner. With on_drift regenerate (the default) it also asks the
// guardian for a repaired script, validates it and saves it as a new version
// (REQ-SP-007, REQ-SP-008).
func (ex *runExec) scriptParseNode(ctx context.Context, node Node, item int, params map[string]any) (nodeResult, error) {
	script := paramString(params, "script")
	// A per-run override set by regeneration wins over the pinned params
	// (REQ-SP-008).
	if s, ok := ex.scripts[node.ID]; ok {
		script = s
	}
	onDrift := paramString(params, "on_drift")
	if onDrift == "" {
		onDrift = "regenerate"
	}
	timeout := time.Duration(clampScriptTimeoutMs(paramFloat(params, "timeout_ms", scriptDefaultTimeoutMs))) * time.Millisecond
	jsctx := map[string]any{
		"item":        ex.itemVals[item],
		"run_number":  ex.run.Number,
		"workflow_id": ex.wf.ID,
	}

	var drift *DriftError
	if script != "" {
		out, logs, dr := ex.runScript(ctx, script, params, jsctx, timeout)
		if dr == nil {
			return nodeResult{Output: out, Port: "out", Via: ViaScript, Tokens: 0, Logs: logs}, nil
		}
		drift = dr
		// on_drift=fail fails the node with the drift error and lets the spec 33
		// ladder run (REQ-SP-006). An empty script never fails here.
		if onDrift == "fail" {
			return nodeResult{Logs: []string{fmt.Sprintf("script drift: %s: %s", drift.Code, drift.Message)}}, drift
		}
	} else {
		drift = &DriftError{Code: scriptNoScriptCode, Message: scriptNoScriptMessage}
	}

	// Fallback: one agent call that answers the node goal as JSON (REQ-SP-005).
	agent := paramString(params, "fallback_agent")
	if agent == "" {
		agent = ex.wf.Guardian
	}
	outputSchema := asMapParam(params["output_schema"])
	req := AgentRequest{
		Agent:  agent,
		Prompt: BuildFallbackPrompt(paramString(params, "goal"), outputSchema, asMapParam(params["item_schema"]), params["input"]),
		Tools:  []string{},
	}
	res, err := ex.callAgent(ctx, node, req)
	if err != nil {
		return nodeResult{Port: "out", Via: ViaFallback, Tokens: res.Tokens, SessionID: res.SessionID}, err
	}
	if res.StopReason == "error" {
		return nodeResult{Port: "out", Via: ViaFallback, Tokens: res.Tokens, SessionID: res.SessionID}, errors.New("agent stopped with an error")
	}
	finished, ferr := ex.finishSchema(ctx, node, item, req, res, outputSchema)
	if ferr != nil {
		return nodeResult{Port: "out", Via: ViaFallback, Tokens: finished.Tokens, SessionID: finished.SessionID}, ferr
	}
	out, dr := CheckContract(params, finished.Output)
	if dr != nil {
		return nodeResult{Port: "out", Via: ViaFallback, Tokens: finished.Tokens, SessionID: finished.SessionID}, dr
	}

	// Regeneration and the decision bookkeeping share the fallback output
	// (REQ-SP-007..011).
	extra, _ := ex.afterFallback(ctx, node, item, params, script, drift, out, finished.Tokens)
	logs := []string{
		fmt.Sprintf("script drift: %s: %s", drift.Code, drift.Message),
		fmt.Sprintf("fallback via %s: %d tok", agent, finished.Tokens),
	}
	return nodeResult{
		Output:    out,
		Port:      "out",
		Via:       ViaFallback,
		Tokens:    finished.Tokens + extra,
		SessionID: finished.SessionID,
		Logs:      logs,
	}, nil
}

// runScript compiles (through the per-run cache) and runs one script, then
// checks its output against the node contract (REQ-SP-002..004). A compile error,
// a runtime error, a timeout or a size breach is a script_error drift; a contract
// failure keeps the drift code CheckContract reported. On success it returns the
// checked output and the node log lines.
func (ex *runExec) runScript(ctx context.Context, script string, params map[string]any, jsctx map[string]any, timeout time.Duration) (any, []string, *DriftError) {
	prog, err := ex.compileScript(script)
	if err != nil {
		return nil, nil, &DriftError{Code: DriftScript, Message: err.Error()}
	}
	res, err := scriptrt.Run(ctx, prog, params["input"], jsctx, scriptrt.Options{Timeout: timeout})
	if err != nil {
		return nil, nil, &DriftError{Code: DriftScript, Message: err.Error()}
	}
	out, dr := CheckContract(params, res.Value)
	if dr != nil {
		return nil, nil, dr
	}
	logs := make([]string, 0, len(res.Logs)+1)
	for _, line := range res.Logs {
		logs = append(logs, "script: "+line)
	}
	logs = append(logs, fmt.Sprintf("script rev %d ok in %d ms", scriptRev(params), res.Duration.Milliseconds()))
	return out, logs, nil
}

// compileScript compiles a script through the per-run cache, so a run compiles
// each distinct source once (design #44, "Engine (engine_script.go)").
func (ex *runExec) compileScript(src string) (*scriptrt.Program, error) {
	if ex.scriptCache == nil {
		ex.scriptCache = map[string]scriptCompile{}
	}
	if c, ok := ex.scriptCache[src]; ok {
		return c.prog, c.err
	}
	prog, err := scriptrt.Compile(src)
	ex.scriptCache[src] = scriptCompile{prog: prog, err: err}
	return prog, err
}

// afterFallback runs regeneration when on_drift is regenerate (the default,
// also for an empty script) and it is allowed, then records the script decision
// and notifies the owner (REQ-SP-007..011). It returns the extra tokens of the
// regeneration, so scriptParseNode adds them to the node tokens, and the note
// used in the notice.
func (ex *runExec) afterFallback(ctx context.Context, node Node, item int, params map[string]any, oldScript string, drift *DriftError, out any, tokens int64) (int64, string) {
	onDrift := paramString(params, "on_drift")
	if onDrift == "" {
		onDrift = "regenerate"
	}
	fallbackTokens := tokens
	rev := scriptRev(params)

	if onDrift == "regenerate" && ex.regenAllowed(ctx, node) {
		newScript, regenTokens, problems, rerr := ex.regenerate(ctx, node, item, params, oldScript, drift, out)
		tokens += regenTokens

		if rerr == nil && newScript != "" {
			newRev := rev + 1
			dropped, serr := ex.e.saveScriptVersion(ctx, ex.wf.ID, node.ID, oldScript, newScript, newRev, drift.Code, fallbackTokens)
			if serr != nil {
				dropped = serr.Error()
			}
			if dropped == "" {
				// An accepted script applies without asking (REQ-SP-008).
				ex.setRunScript(node.ID, newScript)
				note := fmt.Sprintf("script updated to rev %d", newRev)
				ex.putDecision(Decision{
					ID: decisionID(), WorkflowID: ex.wf.ID, RunID: ex.run.ID, RunNumber: ex.run.Number,
					NodeID: node.ID, ItemIndex: item, Kind: "script", Action: "script_update",
					Status: "applied", Reason: drift.Message, Tokens: tokens, Persistent: true, CreatedAt: ex.now(),
					Fix: map[string]any{
						"drift":           drift.Code,
						"error":           drift.Message,
						"rev_from":        rev,
						"rev_to":          newRev,
						"script":          newScript,
						"previous_script": oldScript,
					},
				})
				ex.scriptNotice(node, drift, note, true)
				return regenTokens, note
			}
			// The node changed under the regeneration: record why the update
			// was dropped (REQ-SP-008).
			note := "script not repaired: " + dropped
			ex.recordScriptFallback(node, item, drift, rev, tokens, map[string]any{"dropped": dropped})
			ex.scriptNotice(node, drift, note, false)
			return regenTokens, note
		}

		reason := regenReason(problems, rerr)
		note := "script not repaired: " + reason
		if problems == nil {
			problems = []string{reason}
		}
		ex.recordScriptFallback(node, item, drift, rev, tokens, map[string]any{
			"regen_rejected": true,
			"problems":       problems,
		})
		ex.scriptNotice(node, drift, note, false)
		return regenTokens, note
	}

	// No regeneration: fallback_only, an already regenerated node, or a node
	// whose regeneration is paused (REQ-SP-006, REQ-SP-009).
	note := scriptFallbackNote(onDrift, oldScript, ex.regenDone[node.ID])
	ex.recordScriptFallback(node, item, drift, rev, tokens, nil)
	ex.scriptNotice(node, drift, note, false)
	return 0, note
}

// regenerate asks the guardian for a repaired script and validates up to
// scriptRegenAttempts candidates (REQ-SP-007). A candidate is accepted when it
// compiles, passes the contract on the full current input and agrees with the
// fallback output; a rejected candidate is retried once with its problems
// appended. It marks the node as regenerated for this run (REQ-SP-009) and
// returns the accepted script with the tokens of every call; on failure it
// returns the problems and an error naming the reason.
func (ex *runExec) regenerate(ctx context.Context, node Node, item int, params map[string]any, oldScript string, drift *DriftError, out any) (string, int64, []string, error) {
	if ex.regenDone == nil {
		ex.regenDone = map[string]bool{}
	}
	ex.regenDone[node.ID] = true
	row := ex.ensureRow(node, item)

	timeout := time.Duration(clampScriptTimeoutMs(paramFloat(params, "timeout_ms", scriptDefaultTimeoutMs))) * time.Millisecond
	driftText := ""
	if drift != nil && drift.Code != scriptNoScriptCode {
		driftText = drift.Error()
	}

	var tokens int64
	var problems []string
	var agreement float64
	for attempt := 0; attempt < scriptRegenAttempts; attempt++ {
		req := AgentRequest{
			Agent: ex.wf.Guardian,
			Prompt: BuildScriptPrompt(ScriptPromptInput{
				Goal:       paramString(params, "goal"),
				Schema:     asMapParam(params["output_schema"]),
				ItemSchema: asMapParam(params["item_schema"]),
				AllowEmpty: stringList(params["allow_empty"]),
				Input:      params["input"],
				Expected:   out,
				OldScript:  oldScript,
				DriftError: driftText,
				Problems:   problems,
			}),
			Tools: []string{},
		}
		res, err := ex.callAgent(ctx, node, req)
		tokens += res.Tokens
		if err != nil {
			ex.appendLog(row, "regeneration rejected: "+err.Error())
			return "", tokens, []string{err.Error()}, err
		}
		if res.StopReason == "error" {
			stopped := errors.New("guardian stopped with an error")
			ex.appendLog(row, "regeneration rejected: "+stopped.Error())
			return "", tokens, []string{stopped.Error()}, stopped
		}
		reply, perr := ParseScriptReply(res.Text)
		if perr != nil {
			ex.appendLog(row, "regeneration rejected: "+perr.Error())
			return "", tokens, []string{perr.Error()}, perr
		}
		if !reply.Feasible {
			reason := reply.Reason
			if reason == "" {
				reason = "the guardian reported the step is not feasible"
			}
			ex.appendLog(row, "regeneration rejected: "+reason)
			return "", tokens, []string{reason}, errors.New(reason)
		}
		problems, agreement = ex.checkScriptCandidate(ctx, params, reply.Script, out, timeout)
		if len(problems) == 0 {
			ex.appendLog(row, fmt.Sprintf("regenerated script rev %d (agreement %d%%)", scriptRev(params)+1, int(agreement*100)))
			return reply.Script, tokens, nil, nil
		}
	}
	if len(problems) == 0 {
		problems = []string{"regeneration failed"}
	}
	ex.appendLog(row, "regeneration rejected: "+problems[0])
	return "", tokens, problems, errors.New(problems[0])
}

// evalScript compiles and runs one script_parse script on input and checks its
// output against the node contract (REQ-SP-003, REQ-SP-007). It is the shared
// building block of the engine's candidate check and of the service Test and
// Generate operations (spec #44, API-SP-002, API-SP-003): it returns the checked
// output, the raw console lines, one ScriptProblem per failure (a compile or run
// failure as script_error, otherwise the drift code CheckContract reported) and
// the run's wall time. The sandbox ctx passed to the script is empty.
func evalScript(ctx context.Context, params map[string]any, script string, input any, timeout time.Duration) (any, []string, []ScriptProblem, time.Duration) {
	prog, err := scriptrt.Compile(script)
	if err != nil {
		return nil, nil, []ScriptProblem{{Code: DriftScript, Message: err.Error()}}, 0
	}
	res, err := scriptrt.Run(ctx, prog, input, nil, scriptrt.Options{Timeout: timeout})
	if err != nil {
		return nil, nil, []ScriptProblem{{Code: DriftScript, Message: err.Error()}}, 0
	}
	out, dr := CheckContract(params, res.Value)
	if dr != nil {
		return nil, res.Logs, []ScriptProblem{{Code: dr.Code, Message: dr.Message}}, res.Duration
	}
	return out, res.Logs, nil, res.Duration
}

// checkScriptCandidate validates one regenerated script (REQ-SP-007): it must
// compile, run on the full current input within the node timeout, pass the
// contract and agree with the fallback output. It returns the problems as
// "<code>: <message>" strings and the agreement score; an empty problems slice
// accepts the candidate.
func (ex *runExec) checkScriptCandidate(ctx context.Context, params map[string]any, script string, expected any, timeout time.Duration) ([]string, float64) {
	out, _, problems, _ := evalScript(ctx, params, script, params["input"], timeout)
	if len(problems) > 0 {
		return []string{problems[0].Code + ": " + problems[0].Message}, 0
	}
	agreement, leaves := Agreement(expected, out)
	if leaves >= scriptAgreementLeaves && agreement < scriptAgreementMin {
		return []string{fmt.Sprintf("agreement_low: agreement %.2f is below %.2f", agreement, scriptAgreementMin)}, agreement
	}
	return nil, agreement
}

// regenAllowed reports whether the node may regenerate now (REQ-SP-009). It is
// false while the node has already regenerated once in this run, and after
// scriptRegenPauseAfter rejected regenerations for the node in the last 24 h,
// unless the owner saved a change to the workflow after the newest rejection.
func (ex *runExec) regenAllowed(ctx context.Context, node Node) bool {
	if ex.regenDone[node.ID] {
		return false
	}

	key := ex.wf.ID + "/" + node.ID
	ex.e.mu.Lock()
	reset := ex.e.scriptPauseReset[key]
	ex.e.mu.Unlock()

	decs, err := ex.e.store.ListDecisions(ctx, DecisionFilter{WorkflowID: ex.wf.ID})
	if err != nil {
		return true
	}
	now := ex.e.now()
	count := 0
	var newest int64
	for _, d := range decs {
		if d.Kind != "script" || d.NodeID != node.ID {
			continue
		}
		if rejected, _ := d.Fix["regen_rejected"].(bool); !rejected {
			continue
		}
		if now.Sub(time.UnixMilli(d.CreatedAt)) >= scriptNoticeThrottle {
			continue
		}
		// A ResetScriptPause clears every rejection older than it (REQ-SP-009).
		if !reset.IsZero() && d.CreatedAt <= reset.UnixMilli() {
			continue
		}
		count++
		if d.CreatedAt > newest {
			newest = d.CreatedAt
		}
	}
	if count < scriptRegenPauseAfter {
		return true
	}

	// Paused: only an owner save newer than the newest rejection lifts it.
	wf, err := ex.e.store.GetWorkflow(ctx, ex.wf.ID)
	if err != nil {
		return false
	}
	cur, err := ex.e.store.GetVersion(ctx, ex.wf.ID, wf.Version)
	if err != nil {
		return false
	}
	return cur.Author == "owner" && cur.CreatedAt > newest
}

// setRunScript records the per-run script override so later items of this run
// use the new script (REQ-SP-008). It lazily initializes the map.
func (ex *runExec) setRunScript(nodeID, script string) {
	if ex.scripts == nil {
		ex.scripts = map[string]string{}
	}
	ex.scripts[nodeID] = script
}

// recordScriptFallback records a kind "script" script_fallback decision with the
// drift code, the error and the script revision (REQ-SP-010). extra carries the
// rejection, drop or pause detail.
func (ex *runExec) recordScriptFallback(node Node, item int, drift *DriftError, rev, tokens int64, extra map[string]any) {
	fix := map[string]any{
		"drift":    drift.Code,
		"error":    drift.Message,
		"rev_from": rev,
		"rev_to":   rev,
	}
	for k, v := range extra {
		fix[k] = v
	}
	ex.putDecision(Decision{
		ID: decisionID(), WorkflowID: ex.wf.ID, RunID: ex.run.ID, RunNumber: ex.run.Number,
		NodeID: node.ID, ItemIndex: item, Kind: "script", Action: "script_fallback",
		Status: "applied", Reason: drift.Message, Tokens: tokens, CreatedAt: ex.now(),
		Fix: fix,
	})
}

// regenReason names why regeneration was rejected: the first problem, else the
// terminal error.
func regenReason(problems []string, err error) string {
	switch {
	case len(problems) > 0:
		return problems[0]
	case err != nil:
		return err.Error()
	default:
		return "regeneration failed"
	}
}

// scriptFallbackNote is the notice note for a fallback without a new script
// (REQ-SP-006, REQ-SP-009).
func scriptFallbackNote(onDrift, script string, regenDone bool) string {
	switch {
	case onDrift == "fallback_only":
		return "fallback only"
	case regenDone:
		return "regeneration already ran for this node"
	case onDrift == "regenerate":
		// Reaching here with on_drift regenerate means regenAllowed was false:
		// the node is paused after three failed repairs (REQ-SP-009).
		return "regeneration paused after 3 failed repairs"
	case script == "":
		return "learning on first run"
	default:
		return "regeneration not run"
	}
}

// saveScriptVersion saves a regenerated script as a new workflow version
// (REQ-SP-008). It loads the current version, requires the node to still hold
// oldScript, and merges script, script_rev and baseline_tokens into a copy of
// the node params. It returns a drop reason when the node is gone or its script
// changed; on ErrVersionConflict it reloads once and retries once.
func (e *Engine) saveScriptVersion(ctx context.Context, wfID, nodeID, oldScript, newScript string, newRev int64, driftCode string, baseline int64) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		wf, err := e.store.GetWorkflow(ctx, wfID)
		if err != nil {
			return "", err
		}
		cur, err := e.store.GetVersion(ctx, wfID, wf.Version)
		if err != nil {
			return "", err
		}
		g := cur.Graph
		idx := -1
		for i := range g.Nodes {
			if g.Nodes[i].ID == nodeID {
				idx = i
				break
			}
		}
		if idx < 0 || paramString(g.Nodes[idx].Params, "script") != oldScript {
			return "owner changed the node; update dropped", nil
		}
		n := g.Nodes[idx]
		g.Nodes[idx].Params = mergeParams(n.Params, map[string]any{
			"script":          newScript,
			"script_rev":      newRev,
			"baseline_tokens": baseline,
		})
		summary := fmt.Sprintf("script %s: rev %d (%s)", nodeName(n), newRev, driftCode)
		if _, err := e.store.SaveVersion(ctx, wfID, wf.Version, g, "guardian", summary); err != nil {
			if errors.Is(err, ErrVersionConflict) {
				continue
			}
			return "", err
		}
		e.changed(wfID, "")
		return "", nil
	}
	return "owner changed the node; update dropped", nil
}

// ResetScriptPause clears the regeneration pause of a node (REQ-SP-009): every
// rejection recorded before now is ignored by regenAllowed. Generate calls it
// after a successful generation.
func (e *Engine) ResetScriptPause(wfID, nodeID string) {
	key := wfID + "/" + nodeID
	e.mu.Lock()
	if e.scriptPauseReset == nil {
		e.scriptPauseReset = map[string]time.Time{}
	}
	e.scriptPauseReset[key] = e.now()
	e.mu.Unlock()
}

// answerScript resolves a kind "script" decision (REQ-SP-012, API-SP-006). Only
// a script_update decision with no choice yet accepts "revert", which restores
// the previous script as a new version and closes the decision. A revert of an
// already reverted decision is ErrDecisionClosed; anything else is
// ErrInvalidChoice. It never resumes or relaunches the run.
func (e *Engine) answerScript(ctx context.Context, d Decision, choice string) (Run, error) {
	if d.Action != "script_update" || choice != "revert" {
		return Run{}, ErrInvalidChoice
	}
	if d.Choice == "revert" {
		return Run{}, ErrDecisionClosed
	}
	if d.Choice != "" {
		return Run{}, ErrInvalidChoice
	}
	if err := e.revertScript(ctx, d); err != nil {
		return Run{}, err
	}
	d.Choice = "revert"
	d.ResolvedAt = e.now().UnixMilli()
	if err := e.store.PutDecision(ctx, d); err != nil {
		return Run{}, err
	}
	e.changed(d.WorkflowID, d.RunID)
	return e.store.GetRun(ctx, d.RunID)
}

// revertScript saves a new workflow version that restores the previous script of
// a script_update decision (REQ-SP-012). The node's script_rev advances by one,
// the author is the owner and the old and new revisions are named.
func (e *Engine) revertScript(ctx context.Context, d Decision) error {
	wf, err := e.store.GetWorkflow(ctx, d.WorkflowID)
	if err != nil {
		return err
	}
	cur, err := e.store.GetVersion(ctx, d.WorkflowID, wf.Version)
	if err != nil {
		return err
	}
	g := cur.Graph
	idx := -1
	for i := range g.Nodes {
		if g.Nodes[i].ID == d.NodeID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("node no longer exists")
	}
	n := g.Nodes[idx]
	prev, _ := d.Fix["previous_script"].(string)
	g.Nodes[idx].Params = mergeParams(n.Params, map[string]any{
		"script":     prev,
		"script_rev": scriptRev(n.Params) + 1,
	})
	summary := fmt.Sprintf("revert script %s to rev %v", nodeName(n), d.Fix["rev_from"])
	_, err = e.store.SaveVersion(ctx, d.WorkflowID, wf.Version, g, "owner", summary)
	return err
}

// scriptNotice tells the owner that a script_parse node used the agent
// (REQ-SP-011). It sends no buttons and no decision id. A script_fallback notice
// for the same node is throttled to one per 24 h unless always is true; a
// script_update notice passes always = true. The throttle lives in memory.
func (ex *runExec) scriptNotice(node Node, drift *DriftError, note string, always bool) {
	e := ex.e
	key := ex.wf.ID + "/" + node.ID

	e.mu.Lock()
	if e.scriptNoticeAt == nil {
		e.scriptNoticeAt = map[string]time.Time{}
	}
	last, seen := e.scriptNoticeAt[key]
	now := e.now()
	// The window runs from the last notice actually sent: only a notice that
	// gets past the throttle (or passes always) advances the timestamp, so a
	// suppressed fallback never resets the 24 h window (REQ-SP-011).
	throttled := !always && seen && now.Sub(last) < scriptNoticeThrottle
	if !throttled {
		e.scriptNoticeAt[key] = now
	}
	e.mu.Unlock()
	if throttled {
		return
	}

	name := node.Name
	if name == "" {
		name = node.ID
	}
	ex.notify(ex.ctx, Notice{
		WorkflowID:   ex.wf.ID,
		WorkflowName: ex.wf.Name,
		RunNumber:    ex.run.Number,
		Title:        fmt.Sprintf("%s · Run #%d: Script Parse used the agent", ex.wf.Name, ex.run.Number),
		Text:         fmt.Sprintf("%s: %s. %s", name, drift.Code, note),
	})
}

// scriptRev reads the node's script_rev system param (REQ-SP-001).
func scriptRev(params map[string]any) int64 {
	return int64(paramFloat(params, "script_rev", 0))
}

// clampScriptTimeoutMs clamps a timeout in milliseconds to the 100..10000 range
// of REQ-SP-002.
func clampScriptTimeoutMs(ms float64) int64 {
	switch {
	case ms < scriptMinTimeoutMs:
		return scriptMinTimeoutMs
	case ms > scriptMaxTimeoutMs:
		return scriptMaxTimeoutMs
	default:
		return int64(ms)
	}
}
