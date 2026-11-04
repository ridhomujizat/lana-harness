package workflow

// This file holds the run engine (REQ-WF-004..009, design §"Engine"). One
// goroutine executes one live run and is the only writer of that run's rows.
// The engine walks the version graph in topological order, resolves edge
// readiness, runs each node through the error ladder and persists every step so
// a run survives a daemon restart.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Engine tuning constants (design §"Engine").
const (
	engineDiagnoseTimeout = 3 * time.Minute
	engineDecisionTTL     = 24 * time.Hour
	engineLoopMaxItems    = 500
	engineLogMaxLines     = 20
	engineLogMaxLineLen   = 500
	engineWorkerSteps     = 12
	engineWorkerTokens    = 20000
	engineBashTimeout     = 120 * time.Second
	engineBrowserTimeout  = 30 * time.Second
	engineMinConfidence   = 0.6
)

// errLoopHalt unwinds the ladder when a loop body already ended the run.
var errLoopHalt = errors.New("workflow: loop halted")

// EngineOptions configures an Engine. Every field is optional.
type EngineOptions struct {
	// Now supplies the clock; nil uses time.Now.
	Now func() time.Time
	// Sleep waits for a retry backoff; nil uses a timer that honors ctx.
	Sleep func(ctx context.Context, d time.Duration) error
	// Notify tells the owner; nil drops the notice.
	Notify func(ctx context.Context, n Notice)
	// SendOwner sends a plain owner message (the message node); nil means
	// Telegram is not connected.
	SendOwner func(ctx context.Context, text string) error
	// Changed is called after every saved change to a workflow or run.
	Changed func(workflowID, runID string)
	// DiagnoseTimeout bounds one guardian diagnosis; nil uses three minutes.
	DiagnoseTimeout time.Duration
	// Root bounds every run goroutine; nil uses context.Background(). Launch
	// derives each run's context from it, never from the caller's ctx.
	Root context.Context
}

// liveRun tracks one running goroutine so Launch, Cancel and Live can find it.
type liveRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Engine executes workflow runs. It is safe for concurrent use.
type Engine struct {
	store Store
	deps  Deps
	opts  EngineOptions

	rootMu  sync.RWMutex
	rootCtx context.Context

	mu   sync.Mutex
	live map[string]*liveRun
	wg   sync.WaitGroup
}

// NewEngine returns an engine over st and deps with the given options.
func NewEngine(st Store, deps Deps, opts EngineOptions) *Engine {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepWithContext
	}
	if opts.DiagnoseTimeout <= 0 {
		opts.DiagnoseTimeout = engineDiagnoseTimeout
	}
	if opts.Root == nil {
		opts.Root = context.Background()
	}
	return &Engine{store: st, deps: deps, opts: opts, rootCtx: opts.Root, live: map[string]*liveRun{}}
}

// SetRoot replaces the context that bounds launched runs (REQ-WF-007). It lets
// a service install its own root so a run's goroutine survives the request or
// handler context that launched it. A nil ctx resets to context.Background().
func (e *Engine) SetRoot(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	e.rootMu.Lock()
	e.rootCtx = ctx
	e.rootMu.Unlock()
}

// root returns the context that bounds launched runs.
func (e *Engine) root() context.Context {
	e.rootMu.RLock()
	defer e.rootMu.RUnlock()
	return e.rootCtx
}

// sleepWithContext waits for d or until ctx is done.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// now returns the current time from the engine clock.
func (e *Engine) now() time.Time { return e.opts.Now() }

// changed publishes a change event when a callback is set.
func (e *Engine) changed(workflowID, runID string) {
	if e.opts.Changed != nil {
		e.opts.Changed(workflowID, runID)
	}
}

// notify sends an owner notice when a notifier is set.
func (e *Engine) notify(ctx context.Context, n Notice) {
	if e.opts.Notify != nil {
		e.opts.Notify(ctx, n)
	}
}

// Launch starts a goroutine that executes runID when it is not already live
// (REQ-WF-007). ctx is used only for the store reads taken before the goroutine
// starts; the run's own context derives from the engine root, so cancelling the
// caller's context (an HTTP request, for instance) never stops the run. A
// finished run is ignored, and a waiting run is ignored unless it waits on a
// due timer.
func (e *Engine) Launch(ctx context.Context, runID string) {
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return
	}
	switch run.Status {
	case RunSucceeded, RunFailed, RunCancelled:
		return
	case RunWaiting:
		if run.WaitingReason != "wait" || run.WakeAt > e.now().UnixMilli() {
			return
		}
	}

	e.mu.Lock()
	if _, ok := e.live[runID]; ok {
		e.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(e.root())
	lr := &liveRun{cancel: cancel, done: make(chan struct{})}
	e.live[runID] = lr
	e.wg.Add(1)
	e.mu.Unlock()

	go func() {
		defer e.wg.Done()
		defer func() {
			e.mu.Lock()
			delete(e.live, runID)
			e.mu.Unlock()
			close(lr.done)
		}()
		e.execute(runCtx, runID)
	}()
}

// Live reports whether a goroutine is executing runID.
func (e *Engine) Live(runID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.live[runID]
	return ok
}

// Wait blocks until every launched run goroutine has ended; it is used by tests
// and by Close.
func (e *Engine) Wait() { e.wg.Wait() }

// Cancel stops a live run or marks a waiting one cancelled (REQ-WF-007). It
// returns ErrRunFinished when the run already ended, and cancels the run's
// pending decisions as "stop".
func (e *Engine) Cancel(ctx context.Context, runID string) (Run, error) {
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	switch run.Status {
	case RunSucceeded, RunFailed, RunCancelled:
		return Run{}, ErrRunFinished
	}

	now := e.now().UnixMilli()
	pending, _ := e.store.ListDecisions(ctx, DecisionFilter{RunID: runID, Status: "pending"})
	for _, d := range pending {
		d.Status = "resolved"
		d.Choice = "stop"
		d.ResolvedAt = now
		_ = e.store.PutDecision(ctx, d)
		e.changed(d.WorkflowID, d.RunID)
	}

	e.mu.Lock()
	lr := e.live[runID]
	e.mu.Unlock()
	if lr != nil {
		lr.cancel()
		<-lr.done
		if updated, gerr := e.store.GetRun(ctx, runID); gerr == nil {
			return updated, nil
		}
		return run, nil
	}

	run.Status = RunCancelled
	run.WaitingReason = ""
	run.Error = "cancelled"
	if run.EndedAt == 0 {
		run.EndedAt = now
	}
	if err := e.store.UpdateRun(ctx, run); err != nil {
		return Run{}, err
	}
	e.changed(run.WorkflowID, run.ID)
	return run, nil
}

// CancelAll cancels every live run without waiting. Service.Close uses it so a
// shutdown stops runs even when the engine root was never installed (REQ-WF-007).
func (e *Engine) CancelAll() {
	e.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(e.live))
	for _, lr := range e.live {
		cancels = append(cancels, lr.cancel)
	}
	e.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// Answer resolves a pending decision and resumes the run (REQ-WF-008). It
// returns ErrNotFound for an unknown decision, ErrDecisionClosed when it is not
// pending, and ErrInvalidChoice for a choice the decision does not accept.
func (e *Engine) Answer(ctx context.Context, decisionID, choice string) (Run, error) {
	d, err := e.store.GetDecision(ctx, decisionID)
	if err != nil {
		return Run{}, err
	}
	if d.Status != "pending" {
		return Run{}, ErrDecisionClosed
	}
	switch d.Kind {
	case "approval":
		if choice != "approve" && choice != "deny" {
			return Run{}, ErrInvalidChoice
		}
	case "guardian":
		switch choice {
		case "apply", "run_only", "skip", "stop":
		default:
			return Run{}, ErrInvalidChoice
		}
	default:
		return Run{}, ErrInvalidChoice
	}

	now := e.now().UnixMilli()
	d.Status = "resolved"
	d.Choice = choice
	d.ResolvedAt = now
	if err := e.store.PutDecision(ctx, d); err != nil {
		return Run{}, err
	}
	e.changed(d.WorkflowID, d.RunID)

	run, err := e.store.GetRun(ctx, d.RunID)
	if err != nil {
		return Run{}, err
	}

	if d.Kind == "approval" {
		row := e.findNodeRun(ctx, run.ID, d.NodeID, d.ItemIndex)
		row.Status = NodeSucceeded
		row.EndedAt = now
		if choice == "approve" {
			row.Port = "approved"
			row.Output = nodeJSON(map[string]any{"approved": true, "by": "owner"})
		} else {
			row.Port = "denied"
			row.Output = nodeJSON(map[string]any{"approved": false})
		}
		normalizeRow(&row, now, false)
		_ = e.store.PutNodeRun(ctx, row)
		e.resumeRun(ctx, run)
		return run, nil
	}

	switch choice {
	case "apply":
		e.saveFixVersion(ctx, run, d)
		e.applyOverride(&run, d)
	case "run_only":
		e.applyOverride(&run, d)
	case "skip":
		row := e.findNodeRun(ctx, run.ID, d.NodeID, d.ItemIndex)
		row.Status = NodeSkipped
		row.Error = d.Reason
		if row.Error == "" {
			row.Error = "skipped by owner"
		}
		row.Port = "out" // the run continues past an owner skip (AC-WF-013)
		row.EndedAt = now
		normalizeRow(&row, now, false)
		_ = e.store.PutNodeRun(ctx, row)
	case "stop":
		run.Status = RunFailed
		run.WaitingReason = ""
		run.Error = "stopped by owner"
		run.EndedAt = now
		_ = e.store.UpdateRun(ctx, run)
		e.changed(run.WorkflowID, run.ID)
		return run, nil
	}

	e.resumeRun(ctx, run)
	return run, nil
}

// saveFixVersion saves the guardian's fix as a new workflow version
// (REQ-WF-008, AC-WF-014).
func (e *Engine) saveFixVersion(ctx context.Context, run Run, d Decision) {
	wf, err := e.store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil {
		return
	}
	cur, err := e.store.GetVersion(ctx, wf.ID, wf.Version)
	if err != nil {
		return
	}
	name := d.NodeID
	g := cur.Graph
	for i := range g.Nodes {
		if g.Nodes[i].ID != d.NodeID {
			continue
		}
		if g.Nodes[i].Name != "" {
			name = g.Nodes[i].Name
		}
		g.Nodes[i].Params = mergeParams(g.Nodes[i].Params, d.Fix)
	}
	if len(d.Fix) > 0 {
		summary := fmt.Sprintf("fix %s: %s", name, strings.Join(sortedKeys(d.Fix), ", "))
		_, _ = e.store.SaveVersion(ctx, wf.ID, wf.Version, g, "guardian", summary)
		e.changed(wf.ID, "")
	}
}

// applyOverride stores the fix in the run's per-node overrides so a restart
// keeps it (REQ-WF-008).
func (e *Engine) applyOverride(run *Run, d Decision) {
	if run.Overrides == nil {
		run.Overrides = map[string]map[string]any{}
	}
	run.Overrides[d.NodeID] = mergeParams(run.Overrides[d.NodeID], d.Fix)
}

// resumeRun marks a waiting run running again and relaunches it.
func (e *Engine) resumeRun(ctx context.Context, run Run) {
	run.Status = RunRunning
	run.WaitingReason = ""
	run.WakeAt = 0
	_ = e.store.UpdateRun(ctx, run)
	e.changed(run.WorkflowID, run.ID)
	e.Launch(ctx, run.ID)
}

// findNodeRun returns the stored node run for (node, item), or a fresh pending
// one when it does not exist.
func (e *Engine) findNodeRun(ctx context.Context, runID, nodeID string, item int) NodeRun {
	list, _ := e.store.ListNodeRuns(ctx, runID)
	for _, nr := range list {
		if nr.NodeID == nodeID && nr.ItemIndex == item {
			return nr
		}
	}
	return NodeRun{RunID: runID, NodeID: nodeID, ItemIndex: item, Status: NodePending}
}

// runExec is the per-run execution state. It is touched by one goroutine only.
type runExec struct {
	e        *Engine
	ctx      context.Context
	run      Run
	wf       Workflow
	graph    Graph
	topLevel []Node
	nodes    map[string]Node
	rows     map[string]*NodeRun
	outputs  map[string]any
	itemOut  map[string]map[int]any
	itemVals map[int]any
	state    map[string]any
	input    any
	runMeta  map[string]any
	wfMeta   map[string]any

	// triggerNodeID is the trigger node that fires for this run.
	triggerNodeID string
}

// execute loads a run and drives it to a final state or a wait (REQ-WF-007).
func (e *Engine) execute(ctx context.Context, runID string) {
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return
	}
	wf, err := e.store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil {
		return
	}
	ver, err := e.store.GetVersion(ctx, run.WorkflowID, run.Version)
	if err != nil {
		return
	}

	ex := &runExec{
		e:        e,
		ctx:      ctx,
		run:      run,
		wf:       wf,
		graph:    ver.Graph,
		nodes:    map[string]Node{},
		rows:     map[string]*NodeRun{},
		outputs:  map[string]any{},
		itemOut:  map[string]map[int]any{},
		itemVals: map[int]any{},
		state:    map[string]any{},
	}
	for _, n := range ver.Graph.Nodes {
		ex.nodes[n.ID] = n
	}
	ex.topLevel = topoOrder(topLevelNodes(ver.Graph), ver.Graph.Edges)

	nrs, _ := e.store.ListNodeRuns(ctx, runID)
	for i := range nrs {
		nr := nrs[i]
		ex.rows[nodeRunKey(nr.NodeID, nr.ItemIndex)] = &nr
	}
	ents, _ := e.store.ListState(ctx, run.WorkflowID)
	for _, en := range ents {
		ex.state[en.Key] = normalize(en.Value)
	}
	ex.input = decodeInput(run.Input)
	ex.runMeta = map[string]any{
		"id": run.ID, "number": run.Number, "trigger": run.Trigger, "started_at": run.StartedAt,
	}
	ex.wfMeta = map[string]any{"id": wf.ID, "name": wf.Name}

	ex.run.Status = RunRunning
	ex.run.WaitingReason = ""
	ex.run.WakeAt = 0
	if ex.run.StartedAt == 0 {
		ex.run.StartedAt = e.now().UnixMilli()
	}
	ex.run.NodesTotal = len(ver.Graph.Nodes)
	ex.saveRun()

	ex.runTopLevel()

	if ex.run.Status == RunWaiting || ex.run.Status == RunCancelled {
		return
	}
	if ex.ctx.Err() != nil {
		ex.cancelRun(nil)
		return
	}
	ex.finish()
}

// runTopLevel walks the top-level nodes in order until the run waits, fails,
// is cancelled or ends (REQ-WF-004).
func (ex *runExec) runTopLevel() {
	ex.triggerNodeID = ex.pickTrigger()
	for _, node := range ex.topLevel {
		if ex.ctx.Err() != nil {
			ex.cancelRun(nil)
			return
		}
		ex.processNode(ex.ctx, node, -1)
		switch ex.run.Status {
		case RunFailed, RunCancelled, RunWaiting:
			return
		}
	}
}

// processNode runs one node at the given loop item (-1 at top level): it applies
// the trigger rule, disabled and readiness skips, renders the params and drives
// the error ladder (REQ-WF-004, REQ-WF-008).
func (ex *runExec) processNode(ctx context.Context, node Node, item int) {
	row := ex.ensureRow(node, item)
	if finalNodeStatus(row.Status) {
		return
	}
	if ex.ctx.Err() != nil {
		ex.cancelRun(row)
		return
	}
	if isTriggerType(node.Type) {
		ex.runTrigger(node, item)
		return
	}
	if node.Disabled {
		ex.skipNode(row, "")
		return
	}
	// A waiting approval node wakes only through Answer; a waiting wait node
	// wakes on this pass (REQ-WF-005, REQ-WF-007).
	if row.Status == NodeWaiting && node.Type != "wait" {
		return
	}

	hasIn, live := ex.inputsLive(node, item)
	if hasIn && !live {
		ex.skipNode(row, "")
		return
	}

	rendered, err := Render(node.Params, ex.scopeFor(item))
	if err != nil {
		ex.escalate(ctx, node, item, row, map[string]any{}, ex.policy(node), err)
		return
	}
	params, _ := rendered.(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	if ov, ok := ex.run.Overrides[node.ID]; ok {
		params = mergeParams(params, ov)
	}

	if node.Type == "wait" {
		ex.waitNode(row, node, params)
		return
	}
	if node.Type == "approval" {
		ex.approvalNode(ctx, row, node, params)
		return
	}

	row.Status = NodeRunning
	row.Input = nodeJSON(params)
	if row.StartedAt == 0 {
		row.StartedAt = ex.now()
	}
	ex.putRow(*row)
	ex.saveRun()

	ex.runLadder(ctx, node, item, row, params)
	if row.Status == NodeRunning && ex.run.Status == RunFailed {
		ex.failNode(row, ex.run.Error)
	}
}

// runTrigger marks the firing trigger node succeeded and the others skipped
// (REQ-WF-003, REQ-WF-004).
func (ex *runExec) runTrigger(node Node, item int) {
	row := ex.ensureRow(node, item)
	if finalNodeStatus(row.Status) {
		return
	}
	if node.ID != ex.triggerNodeID {
		ex.skipNode(row, "")
		return
	}
	out := ex.input
	if out == nil {
		out = map[string]any{}
	}
	ex.succeedNode(row, node, out, "out", 0, "")
}

// pickTrigger chooses the trigger node that fires: the one matching the run
// trigger, else the first trigger in order (REQ-WF-003).
func (ex *runExec) pickTrigger() string {
	desired := "trigger." + ex.run.Trigger
	for _, n := range ex.topLevel {
		if n.Type == desired {
			return n.ID
		}
	}
	for _, n := range ex.topLevel {
		if isTriggerType(n.Type) {
			return n.ID
		}
	}
	return ""
}

// scopeFor builds the render scope for the given loop item (REQ-WF-004).
func (ex *runExec) scopeFor(item int) Scope {
	nodes := make(map[string]any, len(ex.outputs)+len(ex.itemOut))
	for id, out := range ex.outputs {
		nodes[id] = out
	}
	sc := Scope{Input: ex.input, Nodes: nodes, State: ex.state, Run: ex.runMeta, Workflow: ex.wfMeta}
	if item >= 0 {
		for id, perItem := range ex.itemOut {
			if v, ok := perItem[item]; ok {
				nodes[id] = v
			}
		}
		sc.Item = ex.itemVals[item]
		sc.ItemIndex = item
		sc.HasItem = true
	}
	return sc
}

// setOutput records a node's output for later templates.
func (ex *runExec) setOutput(nodeID string, item int, out any) {
	if item < 0 {
		ex.outputs[nodeID] = out
		return
	}
	m := ex.itemOut[nodeID]
	if m == nil {
		m = map[int]any{}
		ex.itemOut[nodeID] = m
	}
	m[item] = out
}

// outputOf returns a node's recorded output for the given item.
func (ex *runExec) outputOf(nodeID string, item int) any {
	if item >= 0 {
		if m, ok := ex.itemOut[nodeID]; ok {
			if v, ok := m[item]; ok {
				return v
			}
		}
	}
	return ex.outputs[nodeID]
}

// inputsLive reports whether the node has any incoming edge and whether at
// least one of them is live (REQ-WF-004).
func (ex *runExec) inputsLive(node Node, item int) (has, live bool) {
	for _, e := range ex.graph.Edges {
		if e.To != node.ID {
			continue
		}
		src, ok := ex.nodes[e.From]
		if !ok {
			continue
		}
		if node.Parent != "" {
			if e.From != node.Parent && src.Parent != node.Parent {
				continue
			}
		} else if src.Parent != "" {
			continue
		}
		has = true
		if ex.edgeLive(e, node, item) {
			live = true
		}
	}
	return has, live
}

// edgeLive reports whether one incoming edge of target is live for the item
// (REQ-WF-004).
func (ex *runExec) edgeLive(e Edge, target Node, item int) bool {
	src, ok := ex.nodes[e.From]
	if !ok {
		return false
	}
	if target.Parent != "" && e.From == target.Parent {
		return true // the loop -> body edge is always live
	}
	srcItem := -1
	if src.Parent != "" {
		srcItem = item
	}
	row := ex.rows[nodeRunKey(src.ID, srcItem)]
	if row == nil || !ex.rowLive(*row, src) {
		return false
	}
	port := e.FromPort
	if port == "" {
		port = "out"
	}
	if len(NodePorts(src)) == 1 {
		return true
	}
	if src.Type == "loop" {
		return port == "done" || port == "out"
	}
	return row.Port == port
}

// rowLive reports whether a finished source row lets its edges run on: a
// succeeded or fallback node, a failed node whose policy is continue, or a node
// skipped by a decision (REQ-WF-008, AC-WF-013).
func (ex *runExec) rowLive(row NodeRun, src Node) bool {
	switch row.Status {
	case NodeSucceeded, NodeFallback:
		return true
	case NodeSkipped:
		// A decision "skip" keeps the node's outgoing edges live so the run
		// continues; a skip caused by a dead branch keeps Port "" and stays
		// dead. A skipped source with Port "out" behaves like a succeeded
		// source with output null.
		return row.Port == "out"
	case NodeFailed:
		return ex.policy(src).Mode == "continue"
	default:
		return false
	}
}

// policy merges a node's on_failure over the workflow default (REQ-WF-008).
func (ex *runExec) policy(node Node) FailurePolicy {
	p := ex.wf.FailureDefault
	if p.Mode == "" || p.Mode == "default" {
		p.Mode = "guardian"
	}
	if node.OnFailure != nil {
		if node.OnFailure.Mode != "" && node.OnFailure.Mode != "default" {
			p.Mode = node.OnFailure.Mode
		}
		if node.OnFailure.Retries != nil {
			p.Retries = node.OnFailure.Retries
		}
		if len(node.OnFailure.BackoffSecs) > 0 {
			p.BackoffSecs = node.OnFailure.BackoffSecs
		}
		if node.OnFailure.Fallback != nil {
			p.Fallback = node.OnFailure.Fallback
		}
	}
	if p.Retries == nil {
		r := 2
		p.Retries = &r
	}
	if len(p.BackoffSecs) == 0 {
		p.BackoffSecs = []int{10, 30}
	}
	return p
}

// runLadder runs a node through its retry policy and then its failure mode
// (REQ-WF-008, AC-WF-012).
func (ex *runExec) runLadder(ctx context.Context, node Node, item int, row *NodeRun, params map[string]any) {
	p := ex.policy(node)
	maxAttempts := 1
	if node.Type != "approval" && p.Retries != nil {
		maxAttempts = 1 + *p.Retries
	}
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var tokens int64
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		started := ex.now()
		res, err := ex.invoke(ctx, node, item, params)
		tokens += res.Tokens
		if err == errLoopHalt {
			ex.run.Tokens += tokens
			return
		}
		att := Attempt{N: attempt, StartedAt: started, EndedAt: ex.now()}
		if err == nil {
			row.Attempts = append(row.Attempts, att)
			ex.run.Tokens += tokens
			ex.succeedNode(row, node, res.Output, res.Port, tokens, res.SessionID)
			return
		}
		if ex.ctx.Err() != nil {
			row.Attempts = append(row.Attempts, att)
			ex.run.Tokens += tokens
			ex.cancelRun(row)
			return
		}
		lastErr = err
		att.Error = err.Error()
		row.Attempts = append(row.Attempts, att)
		ex.appendLog(row, fmt.Sprintf("attempt %d: %s", attempt, err.Error()))
		if attempt < maxAttempts && IsTransient(err) {
			if serr := ex.e.opts.Sleep(ctx, backoffFor(p.BackoffSecs, attempt)); serr != nil {
				ex.run.Tokens += tokens
				ex.cancelRun(row)
				return
			}
			continue
		}
		break
	}

	ex.run.Tokens += tokens
	row.Tokens = tokens
	ex.escalate(ctx, node, item, row, params, p, lastErr)
}

// escalate runs the failure mode of a node whose attempts are exhausted
// (REQ-WF-008, AC-WF-013, AC-WF-014).
func (ex *runExec) escalate(ctx context.Context, node Node, item int, row *NodeRun, params map[string]any, p FailurePolicy, lastErr error) {
	msg := ""
	if lastErr != nil {
		msg = lastErr.Error()
	}
	ex.appendLog(row, "failed: "+msg)

	switch p.Mode {
	case "stop":
		ex.failNode(row, msg)
		ex.failRun(node, msg)
		return
	case "continue":
		ex.failNode(row, msg)
		return
	}

	// guardian: one diagnosis per node per item and run.
	if ex.guardianDecisionExists(node.ID, item) {
		ex.failNode(row, msg)
		ex.failRun(node, msg)
		return
	}

	d, tokens, derr := ex.diagnose(ctx, node, row, lastErr)
	ex.run.Tokens += tokens
	ex.saveRun()
	if derr != nil {
		ex.appendLog(row, "guardian failed: "+derr.Error())
		ex.failNode(row, msg)
		ex.run.Status = RunFailed
		if ex.run.Error == "" {
			ex.run.Error = "guardian failed: " + derr.Error()
		}
		ex.saveRun()
		ex.notify(ctx, Notice{
			WorkflowID: ex.wf.ID, WorkflowName: ex.wf.Name, RunNumber: ex.run.Number,
			Title: fmt.Sprintf("%s · Run #%d failed", ex.wf.Name, ex.run.Number),
			Text:  "guardian failed: " + derr.Error(),
		})
		return
	}

	action := d.Action
	if d.Persistent || d.Confidence < engineMinConfidence || fixNeedsLockedTool(d.Fix, ex.wf.Lock) {
		action = "ask"
	}

	now := ex.now()
	dec := Decision{
		ID: decisionID(), WorkflowID: ex.wf.ID, RunID: ex.run.ID, RunNumber: ex.run.Number,
		NodeID: node.ID, ItemIndex: item, Kind: "guardian", Action: action, Reason: d.Reason,
		Fix: d.Fix, Persistent: d.Persistent, Confidence: d.Confidence, Tokens: tokens,
		Status: "applied", CreatedAt: now,
	}

	switch action {
	case "ask":
		dec.Status = "pending"
		dec.ExpiresAt = now + int64(engineDecisionTTL/time.Millisecond)
		ex.putDecision(dec)
		row.Status = NodeWaiting
		row.Error = msg
		row.EndedAt = now
		ex.putRow(*row)
		ex.appendLog(row, "guardian asks the owner: "+d.Reason)
		ex.run.Status = RunWaiting
		ex.run.WaitingReason = "owner"
		ex.saveRun()
		ex.notify(ctx, Notice{
			WorkflowID: ex.wf.ID, WorkflowName: ex.wf.Name, RunNumber: ex.run.Number,
			Title:      fmt.Sprintf("%s · Run #%d is paused", ex.wf.Name, ex.run.Number),
			Text:       askText(d),
			DecisionID: dec.ID, Choices: []string{"apply", "run_only", "skip", "stop"},
		})
	case "retry_fix":
		ex.putDecision(dec)
		ex.e.applyOverride(&ex.run, dec)
		ex.saveRun()
		merged := mergeParams(params, d.Fix)
		started := ex.now()
		res, err := ex.invoke(ctx, node, item, merged)
		att := Attempt{N: len(row.Attempts) + 1, StartedAt: started, EndedAt: ex.now()}
		if err != nil {
			att.Error = err.Error()
		}
		row.Attempts = append(row.Attempts, att)
		ex.run.Tokens += res.Tokens
		row.Tokens += res.Tokens
		if err == nil {
			ex.succeedNode(row, node, res.Output, res.Port, row.Tokens, res.SessionID)
			return
		}
		ex.appendLog(row, "retry_fix failed: "+err.Error())
		ex.failNode(row, err.Error())
		ex.failRun(node, err.Error())
	case "skip":
		ex.putDecision(dec)
		ex.appendLog(row, "guardian skipped: "+d.Reason)
		ex.skipNodeContinue(row, msg)
	case "fallback":
		ex.putDecision(dec)
		out := p.Fallback
		if out == nil {
			out = map[string]any{"error": msg}
		}
		row.Status = NodeFallback
		row.Output = nodeJSON(out)
		row.Port = "out"
		row.Error = msg
		row.EndedAt = now
		ex.putRow(*row)
		ex.setOutput(node.ID, item, out)
		ex.bumpDone(row)
		ex.saveRun()
	default: // stop
		ex.putDecision(dec)
		ex.failNode(row, msg)
		ex.failRun(node, msg)
	}
}

// diagnose sends the failure to the guardian and parses one decision
// (REQ-WF-008).
func (ex *runExec) diagnose(ctx context.Context, node Node, row *NodeRun, lastErr error) (Diagnosis, int64, error) {
	recent, _ := ex.e.store.ListRuns(ctx, RunFilter{WorkflowID: ex.wf.ID, Limit: diagnoseRecentRuns})
	errText := ""
	if lastErr != nil {
		errText = lastErr.Error()
	}
	prompt := BuildDiagnosisPrompt(DiagnosisInput{
		Workflow: ex.wf,
		Node:     node,
		Input:    decodeInput(row.Input),
		Error:    errText,
		Logs:     row.Logs,
		Recent:   recent,
		Lock:     lockToolNames(ex.wf.Lock),
	})
	dctx, cancel := context.WithTimeout(ctx, ex.e.opts.DiagnoseTimeout)
	defer cancel()
	res, err := ex.e.deps.RunAgent(dctx, AgentRequest{
		WorkflowID: ex.wf.ID, RunID: ex.run.ID, NodeID: node.ID,
		Agent: ex.wf.Guardian, Prompt: prompt,
	})
	if err != nil {
		return Diagnosis{}, res.Tokens, err
	}
	if res.StopReason == "error" {
		return Diagnosis{}, res.Tokens, errors.New("guardian stopped with an error")
	}
	d, perr := ParseDiagnosis(res.Text)
	return d, res.Tokens, perr
}

// guardianDecisionExists reports whether a guardian decision already exists for
// (node, item) in this run.
func (ex *runExec) guardianDecisionExists(nodeID string, item int) bool {
	list, err := ex.e.store.ListDecisions(ex.ctx, DecisionFilter{WorkflowID: ex.wf.ID, RunID: ex.run.ID})
	if err != nil {
		return false
	}
	for _, d := range list {
		if d.Kind == "guardian" && d.NodeID == nodeID && d.ItemIndex == item {
			return true
		}
	}
	return false
}

// finish settles a run: succeeded when nothing failed, else failed, and it
// updates the workflow failure counter (REQ-WF-007, REQ-WF-008, AC-WF-015).
func (ex *runExec) finish() {
	failed := ex.run.Error
	succeeded, skipped, done := 0, 0, 0
	for _, node := range ex.topLevel {
		row := ex.rows[nodeRunKey(node.ID, -1)]
		if row == nil {
			continue
		}
		if finalNodeStatus(row.Status) {
			done++
		}
		switch row.Status {
		case NodeSucceeded, NodeFallback:
			succeeded++
		case NodeSkipped:
			skipped++
		}
	}
	ex.run.NodesDone = done
	if failed == "" {
		ex.run.Status = RunSucceeded
		ex.run.Summary = fmt.Sprintf("%d nodes ok", succeeded)
		if skipped > 0 {
			ex.run.Summary += fmt.Sprintf(", %d skipped", skipped)
		}
	} else {
		ex.run.Status = RunFailed
		ex.run.Error = failed
	}
	ex.run.WaitingReason = ""
	ex.run.EndedAt = ex.now()
	ex.saveRun()

	wf, err := ex.e.store.GetWorkflow(ex.ctx, ex.wf.ID)
	if err == nil {
		if failed == "" {
			wf.ConsecutiveFailures = 0
		} else {
			wf.ConsecutiveFailures++
		}
		if wf.ConsecutiveFailures >= 3 && wf.Status != StatusError {
			wf.Status = StatusError
			ex.notify(ex.ctx, Notice{
				WorkflowID: wf.ID, WorkflowName: wf.Name, RunNumber: ex.run.Number,
				Title: wf.Name + " is paused",
				Text:  fmt.Sprintf("%s is paused after 3 failed runs", wf.Name),
			})
		}
		_ = ex.e.store.UpdateWorkflow(ex.ctx, wf)
		ex.e.changed(wf.ID, "")
	}
	if failed != "" {
		ex.notify(ex.ctx, Notice{
			WorkflowID: ex.wf.ID, WorkflowName: ex.wf.Name, RunNumber: ex.run.Number,
			Title: fmt.Sprintf("%s · Run #%d failed", ex.wf.Name, ex.run.Number),
			Text:  failed,
		})
	}
}

// ensureRow returns the node run for (node, item), creating it when missing.
func (ex *runExec) ensureRow(node Node, item int) *NodeRun {
	key := nodeRunKey(node.ID, item)
	if r, ok := ex.rows[key]; ok {
		return r
	}
	r := &NodeRun{RunID: ex.run.ID, NodeID: node.ID, ItemIndex: item, Status: NodePending}
	ex.rows[key] = r
	return r
}

// putRow persists a node run with its durable defaults filled in.
func (ex *runExec) putRow(nr NodeRun) {
	normalizeRow(&nr, ex.now(), isTriggerType(ex.nodes[nr.NodeID].Type))
	_ = ex.e.store.PutNodeRun(ex.ctx, nr)
}

// normalizeRow fills the durable defaults of a node run before it is saved:
// Attempts and Logs are never nil so their JSON is never null, and a trigger or
// skipped row always carries start and end times.
func normalizeRow(row *NodeRun, now int64, trigger bool) {
	if row.Attempts == nil {
		row.Attempts = []Attempt{}
	}
	if row.Logs == nil {
		row.Logs = []string{}
	}
	if trigger || row.Status == NodeSkipped {
		if row.StartedAt == 0 {
			row.StartedAt = now
		}
		if row.EndedAt == 0 {
			row.EndedAt = now
		}
	}
}

// skipNodeContinue marks a node skipped by a decision and keeps its outgoing
// edges live, so a guardian or owner "skip" lets the run continue
// (REQ-WF-008, AC-WF-013).
func (ex *runExec) skipNodeContinue(row *NodeRun, msg string) {
	ex.skipNode(row, msg)
	if row.Status == NodeSkipped {
		row.Port = "out"
		ex.putRow(*row)
	}
}

// putDecision persists a decision and publishes the change.
func (ex *runExec) putDecision(d Decision) {
	_ = ex.e.store.PutDecision(ex.ctx, d)
	ex.e.changed(ex.wf.ID, ex.run.ID)
}

// saveRun persists the run and publishes the change.
func (ex *runExec) saveRun() {
	_ = ex.e.store.UpdateRun(ex.ctx, ex.run)
	ex.e.changed(ex.run.WorkflowID, ex.run.ID)
}

// now returns the current time in Unix milliseconds.
func (ex *runExec) now() int64 { return ex.e.now().UnixMilli() }

// notify sends an owner notice through the engine notifier.
func (ex *runExec) notify(ctx context.Context, n Notice) { ex.e.notify(ctx, n) }

// succeedNode marks a node row succeeded and records its output.
func (ex *runExec) succeedNode(row *NodeRun, node Node, out any, port string, tokens int64, session string) {
	if port == "" {
		port = "out"
	}
	row.Status = NodeSucceeded
	row.Output = nodeJSON(out)
	row.Port = port
	row.Tokens = tokens
	row.SessionID = session
	row.Error = ""
	row.EndedAt = ex.now()
	ex.putRow(*row)
	ex.setOutput(node.ID, row.ItemIndex, out)
	ex.bumpDone(row)
	ex.saveRun()
}

// skipNode marks a node row skipped (REQ-WF-004, REQ-WF-008).
func (ex *runExec) skipNode(row *NodeRun, msg string) {
	if finalNodeStatus(row.Status) {
		return
	}
	row.Status = NodeSkipped
	row.Error = msg
	row.EndedAt = ex.now()
	ex.putRow(*row)
	ex.bumpDone(row)
	ex.saveRun()
}

// failNode marks a node row failed (REQ-WF-008).
func (ex *runExec) failNode(row *NodeRun, msg string) {
	row.Status = NodeFailed
	row.Error = msg
	row.EndedAt = ex.now()
	ex.putRow(*row)
	ex.bumpDone(row)
	ex.saveRun()
}

// failRun records the first node failure and marks the run failed.
func (ex *runExec) failRun(node Node, msg string) {
	if ex.run.Error == "" {
		name := node.Name
		if name == "" {
			name = node.ID
		}
		ex.run.Error = name + ": " + msg
	}
	ex.run.Status = RunFailed
	ex.saveRun()
}

// cancelRun marks the running node failed "cancelled" and the run cancelled
// (REQ-WF-007).
func (ex *runExec) cancelRun(row *NodeRun) {
	if row != nil && !finalNodeStatus(row.Status) {
		row.Status = NodeFailed
		row.Error = "cancelled"
		row.EndedAt = ex.now()
		ex.putRow(*row)
		ex.bumpDone(row)
	}
	ex.run.Status = RunCancelled
	ex.run.WaitingReason = ""
	ex.run.Error = "cancelled"
	if ex.run.EndedAt == 0 {
		ex.run.EndedAt = ex.now()
	}
	ex.saveRun()
}

// bumpDone counts a finished top-level node.
func (ex *runExec) bumpDone(row *NodeRun) {
	if row.ItemIndex < 0 {
		ex.run.NodesDone++
	}
}

// appendLog adds one bounded log line to a node run.
func (ex *runExec) appendLog(row *NodeRun, line string) {
	if len(line) > engineLogMaxLineLen {
		line = line[:engineLogMaxLineLen]
	}
	row.Logs = append(row.Logs, line)
	if len(row.Logs) > engineLogMaxLines {
		row.Logs = row.Logs[len(row.Logs)-engineLogMaxLines:]
	}
}

// askText renders the owner-facing ask text with the proposed fix.
func askText(d Diagnosis) string {
	text := d.Reason
	if len(d.Fix) > 0 {
		text += "\nFix: " + fixString(d.Fix)
	}
	return text
}

// fixString renders a fix map as "k → v" pairs.
func fixString(fix map[string]any) string {
	keys := sortedKeys(fix)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s → %s", k, compactJSON(fix[k])))
	}
	return strings.Join(parts, ", ")
}

// fixNeedsLockedTool reports whether a fix adds a tool the lock does not cover
// (REQ-WF-008).
func fixNeedsLockedTool(fix map[string]any, lock *Lock) bool {
	tools := stringList(fix["tools"])
	if len(tools) == 0 {
		return false
	}
	set := LockSet(lock)
	for _, t := range tools {
		if !set[t] {
			return true
		}
	}
	return false
}

// lockToolNames returns the locked tool names sorted.
func lockToolNames(lock *Lock) []string {
	set := LockSet(lock)
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// sortedKeys returns the keys of m sorted.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mergeParams returns a copy of base with over's keys applied on top.
func mergeParams(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// backoffFor returns the delay before the retry after a failed attempt: the
// attempt-th value of backoffs, the last one repeating (REQ-WF-008).
func backoffFor(backoffs []int, attempt int) time.Duration {
	if len(backoffs) == 0 {
		return 0
	}
	idx := attempt - 1
	if idx >= len(backoffs) {
		idx = len(backoffs) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return time.Duration(backoffs[idx]) * time.Second
}

// decisionID returns a fresh "d_" + 16 hex characters decision id.
func decisionID() string { return randomHexID("d_", 16) }

// finalNodeStatus reports whether a node row will not run again.
func finalNodeStatus(s NodeStatus) bool {
	switch s {
	case NodeSucceeded, NodeSkipped, NodeFallback, NodeFailed:
		return true
	default:
		return false
	}
}

// isTriggerType reports whether a node type is a trigger.
func isTriggerType(t string) bool {
	return strings.HasPrefix(t, "trigger.")
}

// nodeRunKey builds the in-memory row key of (node, item).
func nodeRunKey(nodeID string, item int) string {
	return nodeID + "\x00" + strconv.Itoa(item)
}

// decodeInput decodes a raw JSON value, mapping an empty or invalid blob to nil.
func decodeInput(msg json.RawMessage) any {
	if len(msg) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(msg, &v) != nil {
		return nil
	}
	return v
}

// nodeJSON marshals a value for storage, falling back to null.
func nodeJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

// topLevelNodes returns the nodes without a parent.
func topLevelNodes(g Graph) []Node {
	var out []Node
	for _, n := range g.Nodes {
		if n.Parent == "" {
			out = append(out, n)
		}
	}
	return out
}

// topoOrder sorts nodes topologically using the edges between them, breaking
// ties by (y, x, id); a cycle falls back to a sorted tail (REQ-WF-004).
func topoOrder(nodes []Node, edges []Edge) []Node {
	set := make(map[string]Node, len(nodes))
	indeg := make(map[string]int, len(nodes))
	adj := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		set[n.ID] = n
		indeg[n.ID] = 0
	}
	for _, e := range edges {
		if _, ok := set[e.From]; !ok {
			continue
		}
		if _, ok := set[e.To]; !ok {
			continue
		}
		if e.From == e.To {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
		indeg[e.To]++
	}

	var avail []Node
	for _, n := range nodes {
		if indeg[n.ID] == 0 {
			avail = append(avail, n)
		}
	}
	sortNodesByPosition(avail)

	done := map[string]bool{}
	out := make([]Node, 0, len(nodes))
	for len(avail) > 0 {
		n := avail[0]
		avail = avail[1:]
		if done[n.ID] {
			continue
		}
		done[n.ID] = true
		out = append(out, n)
		for _, to := range adj[n.ID] {
			indeg[to]--
			if indeg[to] == 0 {
				avail = append(avail, set[to])
				sortNodesByPosition(avail)
			}
		}
	}
	if len(out) < len(nodes) {
		var rest []Node
		for _, n := range nodes {
			if !done[n.ID] {
				rest = append(rest, n)
			}
		}
		sortNodesByPosition(rest)
		out = append(out, rest...)
	}
	return out
}

// sortNodesByPosition sorts nodes by (y, x, id).
func sortNodesByPosition(nodes []Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if a.Position.Y != b.Position.Y {
			return a.Position.Y < b.Position.Y
		}
		if a.Position.X != b.Position.X {
			return a.Position.X < b.Position.X
		}
		return a.ID < b.ID
	})
}
