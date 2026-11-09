package workflow

// This file holds the per-node-type handlers of the engine (REQ-WF-005). Each
// handler turns rendered params into a nodeResult (output, port, tokens) or an
// error that feeds the error ladder. Numbers arrive as float64 from JSON.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// nodeResult is the outcome of one node attempt.
type nodeResult struct {
	Output    any
	Port      string
	Tokens    int64
	SessionID string
	// Via records how a script_parse node resolved: "" for the other node
	// types, script or fallback for a Script Parse node (spec #44, REQ-SP-004,
	// REQ-SP-005).
	Via string
	// Logs are lines the node handler wants appended to the node run (spec #44).
	Logs []string
}

// invoke runs one node handler by type. The waiting nodes (wait, approval) are
// handled by the engine before the ladder, so they never reach here.
func (ex *runExec) invoke(ctx context.Context, node Node, item int, params map[string]any) (nodeResult, error) {
	switch node.Type {
	case "agent":
		return ex.agentNode(ctx, node, item, params, ex.wf.Guardian, paramString(params, "prompt"))
	case "bot":
		agent := paramString(params, "agent")
		if agent == "" {
			agent = ex.wf.Guardian
		}
		return ex.agentNode(ctx, node, item, params, agent, paramString(params, "prompt"))
	case "worker":
		return ex.workerNode(ctx, node, item, params)
	case "script_parse":
		return ex.scriptParseNode(ctx, node, item, params)
	case "browser":
		return ex.browserNode(ctx, node, params)
	case "bash":
		return ex.bashNode(ctx, node, params)
	case "webfetch":
		return ex.webFetchNode(ctx, node, params)
	case "tool":
		return ex.toolNode(ctx, node, params)
	case "message":
		return ex.messageNode(ctx, node, params)
	case "knowledge":
		return ex.knowledgeNode(ctx, node, params)
	case "if":
		return ex.ifNode(node, params)
	case "switch":
		return ex.switchNode(node, params)
	case "merge":
		return ex.mergeNode(node, item)
	case "state_get":
		return ex.stateGetNode(params)
	case "state_set":
		return ex.stateSetNode(ctx, params)
	case "loop":
		return ex.loopNode(ctx, node, params)
	default:
		return nodeResult{}, fmt.Errorf("unknown node type %q", node.Type)
	}
}

// agentNode runs a prompt on one agent (REQ-WF-005). With an output schema the
// answer must be JSON matching it, else the node fails with schema_error.
func (ex *runExec) agentNode(ctx context.Context, node Node, item int, params map[string]any, agent, prompt string) (nodeResult, error) {
	schema, hasSchema := params["output_schema"].(map[string]any)
	if hasSchema {
		prompt += "\n\nAnswer with ONE JSON object only matching: " + compactJSON(schema)
	}
	base := AgentRequest{Agent: agent, Prompt: prompt}
	res, err := ex.callAgent(ctx, node, base)
	if err != nil {
		return nodeResult{Tokens: res.Tokens, SessionID: res.SessionID}, err
	}
	if res.StopReason == "error" {
		return nodeResult{Tokens: res.Tokens, SessionID: res.SessionID}, errors.New("agent stopped with an error")
	}
	if hasSchema {
		return ex.finishSchema(ctx, node, item, base, res, schema)
	}
	return nodeResult{Output: map[string]any{"text": res.Text}, Port: "out", Tokens: res.Tokens, SessionID: res.SessionID}, nil
}

// workerNode runs a task in a fresh session of an agent with a model, tool list
// and step budget, then caps its tokens (REQ-WF-005).
func (ex *runExec) workerNode(ctx context.Context, node Node, item int, params map[string]any) (nodeResult, error) {
	agent := paramString(params, "agent")
	if agent == "" {
		agent = ex.wf.Guardian
	}
	maxSteps := int(paramFloat(params, "max_steps", engineWorkerSteps))
	if maxSteps <= 0 {
		maxSteps = engineWorkerSteps
	}
	maxTokens := paramFloat(params, "max_tokens", engineWorkerTokens)
	tools := stringList(params["tools"]) // empty means agent defaults
	schema, hasSchema := params["output_schema"].(map[string]any)
	prompt := paramString(params, "task")
	if hasSchema {
		prompt += "\n\nAnswer with ONE JSON object only matching: " + compactJSON(schema)
	}

	base := AgentRequest{
		Agent: agent, Prompt: prompt, Model: paramString(params, "model"),
		Tools: tools, MaxSteps: maxSteps,
	}
	res, err := ex.callAgent(ctx, node, base)
	if err != nil {
		return nodeResult{Tokens: res.Tokens, SessionID: res.SessionID}, err
	}
	if res.StopReason == "error" {
		return nodeResult{Tokens: res.Tokens, SessionID: res.SessionID}, errors.New("agent stopped with an error")
	}
	if float64(res.Tokens) > maxTokens {
		return nodeResult{Tokens: res.Tokens, SessionID: res.SessionID},
			fmt.Errorf("budget: used %d tokens of %d", res.Tokens, int64(maxTokens))
	}
	if hasSchema {
		return ex.finishSchema(ctx, node, item, base, res, schema)
	}
	return nodeResult{Output: map[string]any{"text": res.Text}, Port: "out", Tokens: res.Tokens, SessionID: res.SessionID}, nil
}

// browserNode renders a page with the browser engine (REQ-WF-005).
func (ex *runExec) browserNode(ctx context.Context, node Node, params map[string]any) (nodeResult, error) {
	url := paramString(params, "url")
	format := paramString(params, "format")
	if format == "" {
		format = "markdown"
	}
	content, err := ex.e.deps.Browse(ctx, BrowseRequest{
		WorkflowID: ex.wf.ID, URL: url, Format: format,
		Stealth: paramBool(params, "stealth"), Selector: paramString(params, "selector"),
		Timeout: paramSeconds(params, "timeout_secs", engineBrowserTimeout),
	})
	if err != nil {
		return nodeResult{}, err
	}
	return nodeResult{Output: map[string]any{"url": url, "format": format, "content": content}, Port: "out"}, nil
}

// bashNode runs a command in the workflow workspace; a non-zero exit is a
// failure (REQ-WF-005, AC-WF-008).
func (ex *runExec) bashNode(ctx context.Context, node Node, params map[string]any) (nodeResult, error) {
	command := paramString(params, "command")
	res, err := ex.e.deps.Bash(ctx, BashRequest{
		WorkflowID: ex.wf.ID, Command: command,
		Timeout: paramSeconds(params, "timeout_secs", engineBashTimeout),
	})
	if err != nil {
		return nodeResult{}, err
	}
	out := map[string]any{"exit_code": res.ExitCode, "output": res.Output}
	if res.ExitCode != 0 {
		return nodeResult{Output: out}, fmt.Errorf("exit code %d: %s", res.ExitCode, tailString(res.Output, 500))
	}
	return nodeResult{Output: out, Port: "out"}, nil
}

// webFetchNode fetches a URL as Markdown (REQ-WF-005).
func (ex *runExec) webFetchNode(ctx context.Context, node Node, params map[string]any) (nodeResult, error) {
	url := paramString(params, "url")
	title, markdown, err := ex.e.deps.WebFetch(ctx, url)
	if err != nil {
		return nodeResult{}, err
	}
	return nodeResult{Output: map[string]any{"url": url, "title": title, "markdown": markdown}, Port: "out"}, nil
}

// toolNode calls any registered tool by name (REQ-WF-005).
func (ex *runExec) toolNode(ctx context.Context, node Node, params map[string]any) (nodeResult, error) {
	input, err := json.Marshal(params["input"])
	if err != nil {
		return nodeResult{}, fmt.Errorf("tool input is not JSON: %w", err)
	}
	text, err := ex.e.deps.CallTool(ctx, ToolRequest{
		WorkflowID: ex.wf.ID, RunID: ex.run.ID, NodeID: node.ID,
		Name: paramString(params, "name"), Input: input,
	})
	if err != nil {
		return nodeResult{}, err
	}
	return nodeResult{Output: map[string]any{"output": text}, Port: "out"}, nil
}

// messageNode sends text to the Telegram owner chat (REQ-WF-005).
func (ex *runExec) messageNode(ctx context.Context, node Node, params map[string]any) (nodeResult, error) {
	if ex.e.opts.SendOwner == nil {
		return nodeResult{}, errors.New("telegram is not connected")
	}
	text := paramString(params, "text")
	if err := ex.e.opts.SendOwner(ctx, text); err != nil {
		return nodeResult{}, err
	}
	return nodeResult{Output: map[string]any{"sent": true, "text": text}, Port: "out"}, nil
}

// knowledgeNode saves text as a knowledge note (REQ-WF-005).
func (ex *runExec) knowledgeNode(ctx context.Context, node Node, params map[string]any) (nodeResult, error) {
	title := paramString(params, "title")
	err := ex.e.deps.SaveKnowledge(ctx, KnowledgeRequest{
		WorkflowID: ex.wf.ID, Agent: ex.wf.Guardian, Title: title,
		Folder: paramString(params, "folder"), Text: paramString(params, "text"),
	})
	if err != nil {
		return nodeResult{}, err
	}
	return nodeResult{Output: map[string]any{"saved": true, "title": title}, Port: "out"}, nil
}

// ifNode evaluates a condition and takes the true or false port (REQ-WF-005,
// AC-WF-006).
func (ex *runExec) ifNode(node Node, params map[string]any) (nodeResult, error) {
	left, right := params["left"], params["right"]
	res, err := Compare(left, paramString(params, "op"), right)
	if err != nil {
		return nodeResult{}, err
	}
	port := "false"
	if res {
		port = "true"
	}
	return nodeResult{Output: map[string]any{"result": res, "left": left, "right": right}, Port: port}, nil
}

// switchNode compares a value against its cases and takes the matching port
// (REQ-WF-005).
func (ex *runExec) switchNode(node Node, params map[string]any) (nodeResult, error) {
	value := params["value"]
	sv := stringOf(value)
	port := "default"
	for _, c := range caseNames(params["cases"]) {
		if c == sv {
			port = c
			break
		}
	}
	return nodeResult{Output: map[string]any{"value": value, "port": port}, Port: port}, nil
}

// mergeNode joins its live inputs into an object keyed by source node id
// (REQ-WF-005).
func (ex *runExec) mergeNode(node Node, item int) (nodeResult, error) {
	out := map[string]any{}
	for _, e := range ex.graph.Edges {
		if e.To != node.ID {
			continue
		}
		if !ex.edgeLive(e, node, item) {
			continue
		}
		out[e.From] = ex.outputOf(e.From, item)
	}
	return nodeResult{Output: out, Port: "out"}, nil
}

// stateGetNode reads one workflow state key (REQ-WF-006).
func (ex *runExec) stateGetNode(params map[string]any) (nodeResult, error) {
	key := paramString(params, "key")
	value, ok := ex.state[key]
	return nodeResult{Output: map[string]any{"key": key, "value": value, "exists": ok}, Port: "out"}, nil
}

// stateSetNode writes one workflow state key (REQ-WF-006, AC-WF-010).
func (ex *runExec) stateSetNode(ctx context.Context, params map[string]any) (nodeResult, error) {
	key := paramString(params, "key")
	value := params["value"]
	b, err := json.Marshal(value)
	if err != nil {
		return nodeResult{}, fmt.Errorf("state value is not JSON: %w", err)
	}
	if err := ex.e.store.SetState(ctx, ex.wf.ID, key, b); err != nil {
		return nodeResult{}, err
	}
	ex.state[key] = value
	ex.e.changed(ex.wf.ID, ex.run.ID)
	return nodeResult{Output: map[string]any{"key": key, "value": value}, Port: "out"}, nil
}

// loopNode runs the loop body once per item and collects the last body output
// per item (REQ-WF-005, AC-WF-008).
func (ex *runExec) loopNode(ctx context.Context, node Node, params map[string]any) (nodeResult, error) {
	raw, ok := normalize(params["items"]).([]any)
	if !ok {
		msg := "loop items must be a JSON array, got " + typeName(params["items"])
		if tpl, isStr := node.Params["items"].(string); isStr && strings.Contains(tpl, "{{") {
			msg += " from " + tpl
		}
		return nodeResult{}, errors.New(msg)
	}
	if len(raw) > engineLoopMaxItems {
		return nodeResult{}, fmt.Errorf("loop items has %d entries, the maximum is %d", len(raw), engineLoopMaxItems)
	}

	body := topoOrder(ex.bodyNodes(node.ID), ex.graph.Edges)
	items := make([]any, 0, len(raw))
	okCount, skipCount := 0, 0

	for i := range raw {
		if ex.ctx.Err() != nil {
			return nodeResult{}, errLoopHalt
		}
		ex.itemVals[i] = raw[i]

		stopped := false
		for _, bn := range body {
			if stopped {
				ex.skipNode(ex.ensureRow(bn, i), "")
				continue
			}
			ex.processNode(ctx, bn, i)
			if ex.run.Status == RunFailed || ex.run.Status == RunWaiting || ex.ctx.Err() != nil {
				return nodeResult{}, errLoopHalt
			}
			r := ex.ensureRow(bn, i)
			if r.Status == NodeSkipped && r.Error != "" {
				stopped = true
			}
		}

		var last any
		if !stopped && len(body) > 0 {
			r := ex.ensureRow(body[len(body)-1], i)
			if r.Status == NodeSucceeded || r.Status == NodeFallback {
				last = decodeInput(r.Output)
			}
		}
		switch {
		case stopped:
			skipCount++
		case last != nil:
			okCount++
		}
		items = append(items, last)
	}

	return nodeResult{
		Output: map[string]any{"items": items, "count": len(raw), "ok": okCount, "skipped": skipCount},
		Port:   "done",
	}, nil
}

// waitNode parks the run on a timer the first time and succeeds on the resume
// pass (REQ-WF-005, REQ-WF-007).
func (ex *runExec) waitNode(row *NodeRun, node Node, params map[string]any) {
	seconds := paramFloat(params, "seconds", 0)
	if row.Status == NodeWaiting {
		ex.succeedNode(row, node, map[string]any{"waited": seconds}, "out", 0, "")
		return
	}
	now := ex.now()
	row.Status = NodeWaiting
	row.Input = nodeJSON(params)
	if row.StartedAt == 0 {
		row.StartedAt = now
	}
	ex.appendLog(row, fmt.Sprintf("waiting %v seconds", seconds))
	ex.putRow(*row)
	ex.run.Status = RunWaiting
	ex.run.WaitingReason = "wait"
	ex.run.Summary = ""
	ex.run.WakeAt = now + int64(seconds*1000)
	ex.saveRun()
}

// approvalNode asks the owner and parks the run until Answer (REQ-WF-005,
// REQ-WF-008).
func (ex *runExec) approvalNode(ctx context.Context, row *NodeRun, node Node, params map[string]any) {
	msg := paramString(params, "message")
	hours := paramFloat(params, "timeout_hours", 24)
	now := ex.now()
	dec := Decision{
		ID: decisionID(), WorkflowID: ex.wf.ID, RunID: ex.run.ID, RunNumber: ex.run.Number,
		NodeID: node.ID, ItemIndex: row.ItemIndex, Kind: "approval", Action: "ask", Reason: msg,
		Status: "pending", CreatedAt: now,
		ExpiresAt: now + int64(hours*float64(time.Hour)/float64(time.Millisecond)),
	}
	ex.putDecision(dec)

	row.Status = NodeWaiting
	row.Input = nodeJSON(params)
	if row.StartedAt == 0 {
		row.StartedAt = now
	}
	ex.appendLog(row, "waiting for approval: "+msg)
	ex.putRow(*row)

	ex.run.Status = RunWaiting
	ex.run.WaitingReason = "approval"
	ex.run.Summary = ""
	ex.run.WakeAt = 0
	ex.saveRun()

	ex.notify(ctx, Notice{
		WorkflowID: ex.wf.ID, WorkflowName: ex.wf.Name, RunNumber: ex.run.Number,
		Title: fmt.Sprintf("%s · Run #%d needs your approval", ex.wf.Name, ex.run.Number),
		Text:  msg, DecisionID: dec.ID, Choices: []string{"approve", "deny"},
	})
}

// bodyNodes returns the nodes whose parent is loopID, in graph order.
func (ex *runExec) bodyNodes(loopID string) []Node {
	var out []Node
	for _, n := range ex.graph.Nodes {
		if n.Parent == loopID {
			out = append(out, n)
		}
	}
	return out
}

// callAgent fills the run identity and the lock into an agent request.
func (ex *runExec) callAgent(ctx context.Context, node Node, req AgentRequest) (AgentResult, error) {
	req.WorkflowID = ex.wf.ID
	req.RunID = ex.run.ID
	req.NodeID = node.ID
	if req.Lock == nil {
		req.Lock = lockToolNames(ex.wf.Lock)
	}
	return ex.e.deps.RunAgent(ctx, req)
}

// parseSchema extracts an agent answer and coerces it to a schema, so a
// near-miss answer (a list where a string is wanted, a numeric string where a
// number is wanted) still validates (REQ-WF-005, REQ-WF-008, AC-WF-009).
func (ex *runExec) parseSchema(schema map[string]any, text string) (any, error) {
	v, err := ExtractJSON(text)
	if err != nil {
		return nil, fmt.Errorf("schema_error: %w", err)
	}
	v, err = CoerceToSchema(schema, v)
	if err != nil {
		return nil, fmt.Errorf("schema_error: %w", err)
	}
	return v, nil
}

// finishSchema turns a schema-bearing answer into a node result: it validates
// the answer, and when the answer does not match it makes one repair call before
// failing (REQ-WF-005, REQ-WF-008). base is the request that produced res and is
// replayed for the repair with only the prompt replaced. When even the repair
// answer is invalid the node still fails with its schema_error, which is not
// transient.
func (ex *runExec) finishSchema(ctx context.Context, node Node, item int, base AgentRequest, res AgentResult, schema map[string]any) (nodeResult, error) {
	v, verr := ex.parseSchema(schema, res.Text)
	if verr == nil {
		return nodeResult{Output: v, Port: "out", Tokens: res.Tokens, SessionID: res.SessionID}, nil
	}
	ex.appendLog(ex.ensureRow(node, item), "repair: asked for JSON again")
	rep, rerr := ex.repairSchema(ctx, node, base, schema, res.Text, verr)
	tokens := res.Tokens + rep.Tokens
	if rerr == nil {
		if rv, rverr := ex.parseSchema(schema, rep.Text); rverr == nil {
			return nodeResult{Output: rv, Port: "out", Tokens: tokens, SessionID: rep.SessionID}, nil
		}
	}
	return nodeResult{Tokens: tokens, SessionID: res.SessionID}, verr
}

// repairSchema makes the one repair RunAgent call after a schema error: the same
// agent, model, tools and lock as the original call, with a prompt that repeats
// the error, the previous answer and the schema (REQ-WF-005, REQ-WF-008).
func (ex *runExec) repairSchema(ctx context.Context, node Node, base AgentRequest, schema map[string]any, prevText string, prevErr error) (AgentResult, error) {
	prompt := fmt.Sprintf(
		"Your previous answer did not match the required JSON shape: %s.\nPrevious answer:\n%s\n\nReply with ONE JSON object only, no prose, matching: %s",
		prevErr.Error(), firstChars(prevText, engineRepairChars), compactJSON(schema))
	req := AgentRequest{
		Agent: base.Agent, Prompt: prompt, Model: base.Model,
		Tools: base.Tools, MaxSteps: base.MaxSteps,
	}
	rep, err := ex.callAgent(ctx, node, req)
	if err != nil {
		return rep, err
	}
	if rep.StopReason == "error" {
		return rep, errors.New("agent stopped with an error")
	}
	return rep, nil
}

// firstChars returns the first n characters of s.
func firstChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// paramString reads a string param.
func paramString(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return s
}

// paramBool reads a bool param.
func paramBool(params map[string]any, key string) bool {
	b, _ := params[key].(bool)
	return b
}

// paramFloat reads a numeric param with a default.
func paramFloat(params map[string]any, key string, def float64) float64 {
	if f, ok := asFloat(params[key]); ok {
		return f
	}
	return def
}

// paramSeconds reads a duration in seconds with a default.
func paramSeconds(params map[string]any, key string, def time.Duration) time.Duration {
	if f, ok := asFloat(params[key]); ok && f > 0 {
		return time.Duration(f * float64(time.Second))
	}
	return def
}

// tailString returns the last n bytes of s.
func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
