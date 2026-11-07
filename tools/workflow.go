package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// The workflow agent tool (spec #33). It inspects workflows and their runs and
// starts or cancels runs. Creating and editing workflows stays in the web UI.

// WorkflowService is the part of *workflow.Service the workflow tool calls.
type WorkflowService interface {
	Overview(ctx context.Context) (workflow.Overview, error)
	Get(ctx context.Context, id string) (workflow.Detail, error)
	Runs(ctx context.Context, id string, limit, before int) ([]workflow.Run, error)
	RunDetail(ctx context.Context, id, ref string) (workflow.RunDetail, error)
	StartRun(ctx context.Context, id, trigger string, input json.RawMessage) (workflow.Run, error)
	Cancel(ctx context.Context, id, ref string) (workflow.Run, error)
	IsWorkflowSession(sessionID string) bool
}

const (
	// workflowRunLimitDefault is the runs action's default page size.
	workflowRunLimitDefault = 10
	// workflowRunLimitMax is the runs action's page size cap.
	workflowRunLimitMax = 50
	// workflowOutputLimit is how many characters of a node output the run
	// action keeps.
	workflowOutputLimit = 2000
	// workflowTrigger is the run trigger of an agent-started workflow.
	workflowTrigger = "agent"
)

const workflowDescription = "Inspect and trigger workflows. " +
	"list shows every workflow with its status and last run, plus pending attention items. " +
	"get shows one workflow's current version, triggers and node names/types. " +
	"runs lists recent runs; run shows one run with its steps. " +
	"start starts a run with trigger \"agent\" and optional input; cancel stops a run. " +
	"A workflow session cannot start another workflow."

var workflowSpec = json.RawMessage(`{"type":"object","properties":{` +
	`"action":{"type":"string","enum":["list","get","runs","run","start","cancel"],"description":"Operation."},` +
	`"id":{"type":"string","description":"Workflow id. Required for every action except list."},` +
	`"ref":{"type":"string","description":"Run id or run number, for run and cancel."},` +
	`"limit":{"type":"integer","minimum":1,"maximum":50,"description":"runs: how many runs to return. Defaults to 10, at most 50."},` +
	`"input":{"type":"object","description":"start: the run input object. Optional."}` +
	`},"required":["action"],"additionalProperties":false}`)

type workflowTool struct{ svc WorkflowService }

// NewWorkflow creates the workflow tool (spec #33).
func NewWorkflow(svc WorkflowService) core.Tool { return &workflowTool{svc: svc} }

func (t *workflowTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "workflow", Description: workflowDescription, InputSchema: workflowSpec}
}

// Risk is read for the inspection actions and exec for starts and cancels.
func (t *workflowTool) Risk(raw json.RawMessage) core.Risk {
	var in struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(raw, &in) == nil {
		switch in.Action {
		case "list", "get", "runs", "run":
			return core.RiskRead
		}
	}
	return core.RiskExec
}

// workflowInput is the tool input.
type workflowInput struct {
	Action string          `json:"action"`
	ID     string          `json:"id"`
	Ref    string          `json:"ref"`
	Limit  *int            `json:"limit"`
	Input  json.RawMessage `json:"input"`
}

func (t *workflowTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in workflowInput
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if t.svc == nil {
		return errorOutput("workflows are not available")
	}
	switch in.Action {
	case "list":
		return t.list(ctx)
	case "get":
		if in.ID == "" {
			return inputErrorOutput(errors.New("id is required"))
		}
		return t.get(ctx, in.ID)
	case "runs":
		if in.ID == "" {
			return inputErrorOutput(errors.New("id is required"))
		}
		return t.runs(ctx, in.ID, in.Limit)
	case "run":
		if in.ID == "" {
			return inputErrorOutput(errors.New("id is required"))
		}
		if in.Ref == "" {
			return inputErrorOutput(errors.New("ref is required"))
		}
		return t.run(ctx, in.ID, in.Ref)
	case "start":
		if in.ID == "" {
			return inputErrorOutput(errors.New("id is required"))
		}
		return t.start(ctx, tc, in)
	case "cancel":
		if in.ID == "" {
			return inputErrorOutput(errors.New("id is required"))
		}
		if in.Ref == "" {
			return inputErrorOutput(errors.New("ref is required"))
		}
		return t.cancel(ctx, in.ID, in.Ref)
	case "":
		return inputErrorOutput(errors.New("action is required"))
	default:
		return inputErrorOutput(fmt.Errorf("unknown action %q", in.Action))
	}
}

// workflowListItem is one workflow row of the list action.
type workflowListItem struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Status      string           `json:"status"`
	Active      bool             `json:"active"`
	LastRun     *workflowRunView `json:"last_run,omitempty"`
}

// workflowAttentionView is one Needs attention row of the list action.
type workflowAttentionView struct {
	ID           string `json:"id"`
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
	RunID        string `json:"run_id,omitempty"`
	RunNumber    int    `json:"run_number,omitempty"`
	Node         string `json:"node,omitempty"`
	Kind         string `json:"kind"`
	Action       string `json:"action,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Argument     string `json:"argument,omitempty"`
}

// workflowListView is the list action output.
type workflowListView struct {
	Workflows []workflowListItem      `json:"workflows"`
	Attention []workflowAttentionView `json:"attention"`
}

func (t *workflowTool) list(ctx context.Context) (core.ToolOutput, error) {
	overview, err := t.svc.Overview(ctx)
	if err != nil {
		return errorOutput(err.Error())
	}
	out := workflowListView{
		Workflows: make([]workflowListItem, 0, len(overview.Workflows)),
		Attention: make([]workflowAttentionView, 0, len(overview.Attention)),
	}
	for _, sum := range overview.Workflows {
		out.Workflows = append(out.Workflows, workflowListItem{
			ID:          sum.ID,
			Name:        sum.Name,
			Description: sum.Description,
			Status:      string(sum.Status),
			Active:      sum.Status == workflow.StatusActive,
			LastRun:     workflowRunViewPtr(sum.LastRun),
		})
	}
	for _, a := range overview.Attention {
		out.Attention = append(out.Attention, workflowAttentionView{
			ID:           a.ID,
			WorkflowID:   a.WorkflowID,
			WorkflowName: a.WorkflowName,
			RunID:        a.RunID,
			RunNumber:    a.RunNumber,
			Node:         a.NodeName,
			Kind:         a.Kind,
			Action:       a.Action,
			Reason:       a.Reason,
			Tool:         a.Tool,
			Argument:     a.Argument,
		})
	}
	return workflowJSON(out)
}

// workflowTriggerView is one trigger of the get action. Detail is the short
// human-readable form of the trigger params (the cron expression, the interval,
// and so on), never the raw params object.
type workflowTriggerView struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Detail  string `json:"detail,omitempty"`
	Enabled bool   `json:"enabled"`
}

// workflowNodeView is one node of the get action: its name and type only, so a
// large params object is never dumped.
type workflowNodeView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// workflowDetailView is the get action output.
type workflowDetailView struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Status      string                `json:"status"`
	Version     int                   `json:"version"`
	Guardian    string                `json:"guardian,omitempty"`
	Triggers    []workflowTriggerView `json:"triggers"`
	Nodes       []workflowNodeView    `json:"nodes"`
}

func (t *workflowTool) get(ctx context.Context, id string) (core.ToolOutput, error) {
	detail, err := t.svc.Get(ctx, id)
	if err != nil {
		return errorOutput(err.Error())
	}
	out := workflowDetailView{
		ID:          detail.ID,
		Name:        detail.Name,
		Description: detail.Description,
		Status:      string(detail.Status),
		Version:     detail.Version,
		Guardian:    detail.Guardian,
		Triggers:    make([]workflowTriggerView, 0),
		Nodes:       make([]workflowNodeView, 0, len(detail.Graph.Nodes)),
	}
	for _, n := range detail.Graph.Nodes {
		if spec, ok := workflow.Spec(n.Type); ok && spec.Trigger {
			out.Triggers = append(out.Triggers, workflowTriggerView{
				Type:    n.Type,
				Name:    workflowNodeNameOf(n),
				Detail:  workflowTriggerDetail(detail.ID, n),
				Enabled: workflowNodeEnabled(n),
			})
		}
		out.Nodes = append(out.Nodes, workflowNodeView{
			ID:   n.ID,
			Name: workflowNodeNameOf(n),
			Type: n.Type,
		})
	}
	return workflowJSON(out)
}

// workflowRunView is the compact run row of the runs, run, start and cancel
// actions.
type workflowRunView struct {
	Number     int    `json:"number"`
	ID         string `json:"id"`
	Status     string `json:"status"`
	Trigger    string `json:"trigger"`
	StartedAt  string `json:"started_at,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

func (t *workflowTool) runs(ctx context.Context, id string, requested *int) (core.ToolOutput, error) {
	runs, err := t.svc.Runs(ctx, id, workflowRunLimit(requested), 0)
	if err != nil {
		return errorOutput(err.Error())
	}
	out := make([]workflowRunView, 0, len(runs))
	for _, r := range runs {
		out = append(out, workflowRunViewOf(r))
	}
	return workflowJSON(out)
}

// workflowStepView is one node run of the run action.
type workflowStepView struct {
	Node       string `json:"node"`
	ID         string `json:"id"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Output     string `json:"output,omitempty"`
}

// workflowRunDetailView is the run action output.
type workflowRunDetailView struct {
	Number     int                `json:"number"`
	ID         string             `json:"id"`
	Status     string             `json:"status"`
	Trigger    string             `json:"trigger"`
	StartedAt  string             `json:"started_at,omitempty"`
	DurationMS int64              `json:"duration_ms"`
	Error      string             `json:"error,omitempty"`
	Nodes      []workflowStepView `json:"nodes"`
}

func (t *workflowTool) run(ctx context.Context, id, ref string) (core.ToolOutput, error) {
	detail, err := t.svc.RunDetail(ctx, id, ref)
	if err != nil {
		return errorOutput(err.Error())
	}
	out := workflowRunDetailView{
		Number:     detail.Run.Number,
		ID:         detail.Run.ID,
		Status:     string(detail.Run.Status),
		Trigger:    detail.Run.Trigger,
		StartedAt:  workflowTime(detail.Run.StartedAt),
		DurationMS: workflowDurationMS(detail.Run.StartedAt, detail.Run.EndedAt),
		Error:      detail.Run.Error,
		Nodes:      make([]workflowStepView, 0, len(detail.Nodes)),
	}
	for _, nr := range detail.Nodes {
		out.Nodes = append(out.Nodes, workflowStepView{
			Node:       workflowGraphNodeName(detail.Graph, nr.NodeID),
			ID:         nr.NodeID,
			Status:     string(nr.Status),
			Error:      nr.Error,
			DurationMS: workflowDurationMS(nr.StartedAt, nr.EndedAt),
			Output:     workflowNodeOutput(nr.Output),
		})
	}
	return workflowJSON(out)
}

func (t *workflowTool) start(ctx context.Context, tc core.ToolCtx, in workflowInput) (core.ToolOutput, error) {
	// A run started from a workflow session would nest one workflow inside
	// another, so an agent node cannot start runs.
	if t.svc.IsWorkflowSession(tc.SessionID) {
		return errorOutput("a workflow session cannot start another workflow")
	}
	input, err := workflowInputObject(in.Input)
	if err != nil {
		return inputErrorOutput(err)
	}
	run, err := t.svc.StartRun(ctx, in.ID, workflowTrigger, input)
	if err != nil {
		return errorOutput(err.Error())
	}
	return workflowJSON(workflowRunViewOf(run))
}

func (t *workflowTool) cancel(ctx context.Context, id, ref string) (core.ToolOutput, error) {
	run, err := t.svc.Cancel(ctx, id, ref)
	if err != nil {
		return errorOutput(err.Error())
	}
	return workflowJSON(workflowRunViewOf(run))
}

// workflowRunLimit resolves the runs page size: default 10, capped at 50.
func workflowRunLimit(requested *int) int {
	if requested == nil {
		return workflowRunLimitDefault
	}
	limit := *requested
	if limit <= 0 {
		return workflowRunLimitDefault
	}
	if limit > workflowRunLimitMax {
		return workflowRunLimitMax
	}
	return limit
}

// workflowRunViewOf renders one run row.
func workflowRunViewOf(r workflow.Run) workflowRunView {
	return workflowRunView{
		Number:     r.Number,
		ID:         r.ID,
		Status:     string(r.Status),
		Trigger:    r.Trigger,
		StartedAt:  workflowTime(r.StartedAt),
		DurationMS: workflowDurationMS(r.StartedAt, r.EndedAt),
		Error:      r.Error,
	}
}

// workflowRunViewPtr renders an optional run row.
func workflowRunViewPtr(r *workflow.Run) *workflowRunView {
	if r == nil {
		return nil
	}
	view := workflowRunViewOf(*r)
	return &view
}

// workflowTime renders Unix ms as UTC RFC 3339; zero is empty.
func workflowTime(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// workflowDurationMS returns the elapsed time between two Unix ms stamps, or
// zero while a run or step is still going.
func workflowDurationMS(started, ended int64) int64 {
	if started <= 0 || ended < started {
		return 0
	}
	return ended - started
}

// workflowNodeNameOf names a node from its own name, falling back to its id.
func workflowNodeNameOf(n workflow.Node) string {
	if name := strings.TrimSpace(n.Name); name != "" {
		return name
	}
	return n.ID
}

// workflowGraphNodeName resolves a node id in a graph, falling back to the id.
func workflowGraphNodeName(g workflow.Graph, id string) string {
	for _, n := range g.Nodes {
		if n.ID == id {
			return workflowNodeNameOf(n)
		}
	}
	return id
}

// workflowNodeEnabled reports whether a trigger node fires; a missing enabled
// param means it does.
func workflowNodeEnabled(n workflow.Node) bool {
	if enabled, ok := n.Params["enabled"].(bool); ok {
		return enabled
	}
	return true
}

// workflowTriggerDetail renders the useful params of one trigger, following the
// trigger rows of the overview (API-WF-001).
func workflowTriggerDetail(workflowID string, n workflow.Node) string {
	switch n.Type {
	case "trigger.schedule":
		cron, _ := n.Params["cron"].(string)
		return workflow.DescribeCron(cron)
	case "trigger.interval":
		switch minutes := n.Params["minutes"].(type) {
		case float64:
			return fmt.Sprintf("Every %d min", int(minutes))
		case int:
			return fmt.Sprintf("Every %d min", minutes)
		}
	case "trigger.webhook":
		return "POST /hooks/" + workflowID
	case "trigger.event":
		kind, _ := n.Params["kind"].(string)
		if agent, _ := n.Params["agent"].(string); agent != "" {
			kind += " · " + agent
		}
		return kind
	case "trigger.chat":
		command, _ := n.Params["command"].(string)
		return "/" + strings.TrimPrefix(strings.TrimSpace(command), "/")
	case "trigger.manual":
		return "From the Editor"
	}
	return ""
}

// workflowInputObject validates the start input as a JSON object. Empty and
// null mean no input, which StartRun replaces with {}.
func workflowInputObject(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, errors.New("input must be a JSON object")
	}
	return raw, nil
}

// workflowNodeOutput renders one node output, truncating it to
// workflowOutputLimit characters.
func workflowNodeOutput(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	text := string(raw)
	var decoded string
	if json.Unmarshal(raw, &decoded) == nil {
		text = decoded
	}
	return truncateWorkflowOutput(text)
}

// truncateWorkflowOutput keeps the first characters of s and replaces the rest
// with a marker, so the result stays within workflowOutputLimit runes.
func truncateWorkflowOutput(s string) string {
	runes := []rune(s)
	if len(runes) <= workflowOutputLimit {
		return s
	}
	marker := "\n...[truncated]"
	keep := workflowOutputLimit - utf8.RuneCountInString(marker)
	if keep < 0 {
		keep = 0
	}
	return string(runes[:keep]) + marker
}

// workflowJSON renders v as indented JSON.
func workflowJSON(v any) (core.ToolOutput, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorOutput(err.Error())
	}
	return successOutput(string(data))
}
