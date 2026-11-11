package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// The workflow_builder tool (spec 51 REQ-WFC-003): the guardian edits and
// tests its workflow from the workflow editor chat. Changes go to a candidate
// that is tested before it is published as the current version. The tool
// works only in a workflow's guardian chat session.

// WorkflowBuilderTool is the tool name; the agent runner offers it on the
// workflow channel (spec 51).
const WorkflowBuilderTool = "workflow_builder"

// WorkflowBuilderService is the part of *workflow.Service the tool calls.
type WorkflowBuilderService interface {
	BuilderGet(ctx context.Context, sessionID string) (workflow.BuilderView, error)
	BuilderSave(ctx context.Context, sessionID string, g workflow.Graph, summary string) (workflow.Candidate, workflow.GraphChange, []workflow.Issue, error)
	BuilderTest(ctx context.Context, sessionID string, input json.RawMessage) (workflow.Run, error)
	BuilderRun(ctx context.Context, sessionID, runID string) (workflow.RunDetail, error)
	BuilderPublish(ctx context.Context, sessionID, turnID string) (workflow.ChatMessage, error)
	BuilderDiscard(ctx context.Context, sessionID string) error
}

const workflowBuilderDescription = "Edit and test the workflow you guard, from its editor chat. " +
	"get returns the current graph (the live version) and the open candidate. " +
	"save stores a full new graph as the candidate (not live yet) and returns the change or the validation issues to fix. " +
	"test runs the candidate (or the current version when there is none) now and waits for the result with every node's status, output and error. " +
	"publish makes the tested candidate the current version; discard drops it. " +
	"Always save, test, read the result and fix before you publish."

var workflowBuilderSpec = json.RawMessage(`{"type":"object","properties":{` +
	`"action":{"type":"string","enum":["get","save","test","publish","discard"],"description":"Operation."},` +
	`"graph":{"type":"object","description":"save: the full new graph {nodes, edges}, same shape as get returns."},` +
	`"summary":{"type":"string","description":"save: one line of what changed."},` +
	`"input":{"type":"object","description":"test: the run input object, when the trigger needs one."}` +
	`},"required":["action"],"additionalProperties":false}`)

type workflowBuilderTool struct{ svc WorkflowBuilderService }

// NewWorkflowBuilder creates the workflow_builder tool (spec 51).
func NewWorkflowBuilder(svc WorkflowBuilderService) core.Tool {
	return &workflowBuilderTool{svc: svc}
}

func (t *workflowBuilderTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: WorkflowBuilderTool, Description: workflowBuilderDescription, InputSchema: workflowBuilderSpec}
}

// Risk is read for get and write otherwise: a candidate does nothing until it
// is published, a test run is what the assistant always could start, and a
// published change of an active workflow outside its lock sends it back to
// draft.
func (t *workflowBuilderTool) Risk(raw json.RawMessage) core.Risk {
	var in struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(raw, &in) == nil && in.Action == "get" {
		return core.RiskRead
	}
	return core.RiskWrite
}

type workflowBuilderInput struct {
	Action  string          `json:"action"`
	Graph   json.RawMessage `json:"graph"`
	Summary string          `json:"summary"`
	Input   json.RawMessage `json:"input"`
}

// workflowBuilderTestWait is how long test waits for the run to finish.
var workflowBuilderTestWait = 2 * time.Minute

// workflowBuilderPoll is the test poll interval.
var workflowBuilderPoll = time.Second

func (t *workflowBuilderTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in workflowBuilderInput
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if t.svc == nil {
		return errorOutput("workflows are not available")
	}
	switch in.Action {
	case "get":
		return t.get(ctx, tc)
	case "save":
		return t.save(ctx, tc, in)
	case "test":
		return t.test(ctx, tc, in)
	case "publish":
		return t.publish(ctx, tc)
	case "discard":
		if err := t.svc.BuilderDiscard(ctx, tc.SessionID); err != nil {
			return builderError(err)
		}
		return successOutput("Candidate discarded; the current version is unchanged.")
	case "":
		return inputErrorOutput(errors.New("action is required"))
	default:
		return inputErrorOutput(fmt.Errorf("unknown action %q", in.Action))
	}
}

// builderError turns a service error into a tool error with a next step.
func builderError(err error) (core.ToolOutput, error) {
	switch {
	case errors.Is(err, workflow.ErrNotAssistantSession):
		return errorOutput("workflow_builder works only in a workflow's editor chat; use the workflow tool here")
	case errors.Is(err, workflow.ErrVersionConflict):
		return errorOutput("version conflict: the current version changed after you saved the candidate (the owner edited the canvas). Run get, apply your change to the current graph, save and test again")
	case errors.Is(err, workflow.ErrRunActive):
		return errorOutput("another run of this workflow is running; wait for it to finish, then test again")
	case errors.Is(err, workflow.ErrNotFound):
		return errorOutput("no open candidate: save a graph first")
	}
	return errorOutput(err.Error())
}

// workflowBuilderGetView is the get output.
type workflowBuilderGetView struct {
	ID        string                    `json:"id"`
	Name      string                    `json:"name"`
	Status    string                    `json:"status"`
	Version   int                       `json:"version"`
	Graph     workflow.Graph            `json:"graph"`
	Candidate *workflowBuilderCandidate `json:"candidate,omitempty"`
}

type workflowBuilderCandidate struct {
	ID          int64          `json:"id"`
	BaseVersion int            `json:"base_version"`
	Summary     string         `json:"summary"`
	Graph       workflow.Graph `json:"graph"`
}

func (t *workflowBuilderTool) get(ctx context.Context, tc core.ToolCtx) (core.ToolOutput, error) {
	view, err := t.svc.BuilderGet(ctx, tc.SessionID)
	if err != nil {
		return builderError(err)
	}
	out := workflowBuilderGetView{
		ID: view.Workflow.ID, Name: view.Workflow.Name, Status: string(view.Workflow.Status),
		Version: view.Current.Version, Graph: view.Current.Graph,
	}
	if c := view.Candidate; c != nil {
		out.Candidate = &workflowBuilderCandidate{ID: c.ID, BaseVersion: c.BaseVersion, Summary: c.Summary, Graph: c.Graph}
	}
	return workflowJSON(out)
}

// workflowBuilderSaveView is the save output.
type workflowBuilderSaveView struct {
	Candidate   int64                 `json:"candidate,omitempty"`
	BaseVersion int                   `json:"base_version,omitempty"`
	Change      *workflow.GraphChange `json:"change,omitempty"`
	Issues      []workflow.Issue      `json:"issues,omitempty"`
	Next        string                `json:"next"`
}

func (t *workflowBuilderTool) save(ctx context.Context, tc core.ToolCtx, in workflowBuilderInput) (core.ToolOutput, error) {
	if len(in.Graph) == 0 || string(in.Graph) == "null" {
		return inputErrorOutput(errors.New("graph is required"))
	}
	var g workflow.Graph
	if err := json.Unmarshal(in.Graph, &g); err != nil {
		return inputErrorOutput(fmt.Errorf("graph: %w", err))
	}
	cand, change, issues, err := t.svc.BuilderSave(ctx, tc.SessionID, g, strings.TrimSpace(in.Summary))
	if err != nil {
		return builderError(err)
	}
	if len(issues) > 0 {
		return workflowJSON(workflowBuilderSaveView{Issues: issues, Next: "Nothing was saved. Fix these issues and save again."})
	}
	return workflowJSON(workflowBuilderSaveView{
		Candidate: cand.ID, BaseVersion: cand.BaseVersion, Change: &change,
		Next: "Saved as a candidate, not live yet. Test it now.",
	})
}

// workflowBuilderTestView is the test output.
type workflowBuilderTestView struct {
	Candidate int64 `json:"candidate,omitempty"`
	workflowRunDetailView
	Next string `json:"next"`
}

func (t *workflowBuilderTool) test(ctx context.Context, tc core.ToolCtx, in workflowBuilderInput) (core.ToolOutput, error) {
	input, err := workflowInputObject(in.Input)
	if err != nil {
		return inputErrorOutput(err)
	}
	run, err := t.svc.BuilderTest(ctx, tc.SessionID, input)
	if err != nil {
		return builderError(err)
	}
	deadline := time.Now().Add(workflowBuilderTestWait)
	var detail workflow.RunDetail
	for {
		detail, err = t.svc.BuilderRun(ctx, tc.SessionID, run.ID)
		if err == nil && runSettled(detail.Run.Status) {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return errorOutput(ctx.Err().Error())
		case <-time.After(workflowBuilderPoll):
		}
	}
	if err != nil {
		return errorOutput(err.Error())
	}
	out := workflowBuilderTestView{Candidate: run.CandidateID, workflowRunDetailView: workflowRunDetailOf(detail)}
	switch detail.Run.Status {
	case workflow.RunSucceeded:
		out.Next = "The test passed. Check the outputs match what the owner wants, then publish."
	case workflow.RunFailed:
		out.Next = "The test failed. Read the failed node's error and output, fix the graph, save and test again."
	case workflow.RunWaiting:
		out.Next = "The run is waiting (an approval or a wait node). Tell the owner, or change the graph so the test can finish."
	case workflow.RunCancelled:
		out.Next = "The run was cancelled."
	default:
		out.Next = "Still running after 2 minutes; check it again with test later, or tell the owner."
	}
	return workflowJSON(out)
}

// runSettled reports whether a run status will not change without outside help.
func runSettled(status workflow.RunStatus) bool {
	switch status {
	case workflow.RunSucceeded, workflow.RunFailed, workflow.RunCancelled, workflow.RunWaiting:
		return true
	}
	return false
}

func (t *workflowBuilderTool) publish(ctx context.Context, tc core.ToolCtx) (core.ToolOutput, error) {
	msg, err := t.svc.BuilderPublish(ctx, tc.SessionID, tc.TurnID)
	if err != nil {
		return builderError(err)
	}
	version := 0
	if msg.Change != nil {
		version = msg.Change.ToVersion
	}
	return workflowJSON(struct {
		Version int                   `json:"version"`
		Change  *workflow.GraphChange `json:"change,omitempty"`
		Next    string                `json:"next"`
	}{Version: version, Change: msg.Change, Next: "Published: this is the current version now. Tell the owner what changed and what the test showed."})
}
