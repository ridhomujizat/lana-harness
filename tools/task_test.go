package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

type fakeSubagentRunner struct {
	calls       int
	spawnReq    core.SpawnRequest
	spawnOut    core.ToolOutput
	spawnErr    error
	continueReq core.ContinueRequest
	continueOut core.ToolOutput
	continueErr error
}

func (f *fakeSubagentRunner) Spawn(_ context.Context, req core.SpawnRequest) (core.ToolOutput, error) {
	f.calls++
	f.spawnReq = req
	return f.spawnOut, f.spawnErr
}

func (f *fakeSubagentRunner) Continue(_ context.Context, req core.ContinueRequest) (core.ToolOutput, error) {
	f.calls++
	f.continueReq = req
	return f.continueOut, f.continueErr
}

func TestTaskSpecAndRisk(t *testing.T) {
	tool := NewTask()
	spec := tool.Spec()
	if spec.Name != "task" {
		t.Fatalf("name=%q", spec.Name)
	}
	var schema struct {
		Type       string `json:"type"`
		Required   []string
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Type != "object" {
		t.Fatalf("type=%q", schema.Type)
	}
	if strings.Join(schema.Required, ",") != "agent,prompt" {
		t.Fatalf("required=%v", schema.Required)
	}
	for _, name := range []string{"agent", "prompt", "title"} {
		if schema.Properties[name].Type != "string" {
			t.Fatalf("property %q=%+v", name, schema.Properties[name])
		}
	}
	if got := tool.Risk(json.RawMessage(`{}`)); got != core.RiskExec {
		t.Fatalf("risk=%q", got)
	}
	parallel, ok := tool.(core.ParallelTool)
	if !ok || !parallel.ParallelSafe() {
		t.Fatal("task is not parallel safe")
	}
}

func TestTaskValidation(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"missing agent", `{"prompt":"do it"}`, "invalid input: agent is required"},
		{"blank agent", `{"agent":"  ","prompt":"do it"}`, "invalid input: agent is required"},
		{"missing prompt", `{"agent":"explorer"}`, "invalid input: prompt is required"},
		{"blank prompt", `{"agent":"explorer","prompt":"   "}`, "invalid input: prompt is required"},
		{"long prompt", fmt.Sprintf(`{"agent":"explorer","prompt":%q}`, strings.Repeat("a", taskPromptLimit+1)), "invalid input: prompt must be at most 8000 characters"},
		{"long title", fmt.Sprintf(`{"agent":"explorer","prompt":"do it","title":%q}`, strings.Repeat("a", taskTitleLimit+1)), "invalid input: title must be at most 80 characters"},
		{"unknown field", `{"agent":"explorer","prompt":"do it","extra":true}`, "invalid input: "},
		{"empty input", ``, "invalid input: "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeSubagentRunner{}
			tool := NewTask()
			out, err := tool.Call(context.Background(), core.ToolCtx{Subagents: runner}, json.RawMessage(test.input))
			if err != nil {
				t.Fatal(err)
			}
			if out.Status != core.ToolStatusError || !strings.HasPrefix(out.Output, test.want) {
				t.Fatalf("output=%+v want prefix %q", out, test.want)
			}
			if runner.calls != 0 {
				t.Fatalf("Spawn called %d times", runner.calls)
			}
		})
	}
}

func TestTaskValidationBoundaries(t *testing.T) {
	runner := &fakeSubagentRunner{spawnOut: core.ToolOutput{Status: core.ToolStatusSuccess, Output: `{"status":"end_turn"}`}}
	tool := NewTask()
	input := fmt.Sprintf(`{"agent":"explorer","prompt":%q,"title":%q}`, strings.Repeat("a", taskPromptLimit), strings.Repeat("b", taskTitleLimit))
	out, err := tool.Call(context.Background(), core.ToolCtx{Subagents: runner}, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusSuccess || runner.calls != 1 {
		t.Fatalf("output=%+v calls=%d", out, runner.calls)
	}
}

func TestTaskSpawn(t *testing.T) {
	runner := &fakeSubagentRunner{spawnOut: core.ToolOutput{Status: core.ToolStatusSuccess, Output: `{"session_id":"child"}`}}
	tool := NewTask()
	input := json.RawMessage(`{"agent":"explorer","prompt":"Find the notes","title":"Notes"}`)
	out, err := tool.Call(context.Background(), core.ToolCtx{
		SessionID: "parent", CallID: "call_1", Depth: 1, AllowedAgents: []string{"explorer"}, Subagents: runner,
	}, input)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusSuccess || out.Output != `{"session_id":"child"}` {
		t.Fatalf("output=%+v", out)
	}
	want := core.SpawnRequest{ParentSessionID: "parent", ParentCallID: "call_1", Depth: 1, Agent: "explorer", Prompt: "Find the notes", Title: "Notes"}
	if runner.spawnReq != want {
		t.Fatalf("request=%+v want %+v", runner.spawnReq, want)
	}
}

func TestTaskWithoutRunner(t *testing.T) {
	tool := NewTask()
	out, err := tool.Call(context.Background(), core.ToolCtx{}, json.RawMessage(`{"agent":"explorer","prompt":"Find the notes"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusError || out.Output != "no subagents are configured for this agent" {
		t.Fatalf("output=%+v", out)
	}
}

func TestTaskContinueSpecAndRisk(t *testing.T) {
	tool := NewTaskContinue()
	spec := tool.Spec()
	if spec.Name != "task_continue" {
		t.Fatalf("name=%q", spec.Name)
	}
	var schema struct {
		Type       string `json:"type"`
		Required   []string
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Type != "object" {
		t.Fatalf("type=%q", schema.Type)
	}
	if strings.Join(schema.Required, ",") != "session_id,prompt" {
		t.Fatalf("required=%v", schema.Required)
	}
	for _, name := range []string{"session_id", "prompt"} {
		if schema.Properties[name].Type != "string" {
			t.Fatalf("property %q=%+v", name, schema.Properties[name])
		}
	}
	if got := tool.Risk(json.RawMessage(`{}`)); got != core.RiskExec {
		t.Fatalf("risk=%q", got)
	}
	parallel, ok := tool.(core.ParallelTool)
	if !ok || !parallel.ParallelSafe() {
		t.Fatal("task_continue is not parallel safe")
	}
}

func TestTaskContinueValidation(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"missing session", `{"prompt":"do it"}`, "invalid input: session_id is required"},
		{"blank session", `{"session_id":"  ","prompt":"do it"}`, "invalid input: session_id is required"},
		{"missing prompt", `{"session_id":"child"}`, "invalid input: prompt is required"},
		{"blank prompt", `{"session_id":"child","prompt":"   "}`, "invalid input: prompt is required"},
		{"long prompt", fmt.Sprintf(`{"session_id":"child","prompt":%q}`, strings.Repeat("a", taskPromptLimit+1)), "invalid input: prompt must be at most 8000 characters"},
		{"unknown field", `{"session_id":"child","prompt":"do it","extra":true}`, "invalid input: "},
		{"empty input", ``, "invalid input: "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeSubagentRunner{}
			out, err := NewTaskContinue().Call(context.Background(), core.ToolCtx{Subagents: runner}, json.RawMessage(test.input))
			if err != nil {
				t.Fatal(err)
			}
			if out.Status != core.ToolStatusError || !strings.HasPrefix(out.Output, test.want) {
				t.Fatalf("output=%+v want prefix %q", out, test.want)
			}
			if runner.calls != 0 {
				t.Fatalf("Continue called %d times", runner.calls)
			}
		})
	}
}

func TestTaskContinueCallsRunner(t *testing.T) {
	runner := &fakeSubagentRunner{continueOut: core.ToolOutput{Status: core.ToolStatusSuccess, Output: `{"session_id":"child"}`}}
	out, err := NewTaskContinue().Call(context.Background(), core.ToolCtx{
		SessionID: "parent", Subagents: runner,
	}, json.RawMessage(`{"session_id":"child","prompt":"Also check X"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusSuccess || out.Output != `{"session_id":"child"}` {
		t.Fatalf("output=%+v", out)
	}
	want := core.ContinueRequest{ParentSessionID: "parent", SessionID: "child", Prompt: "Also check X"}
	if runner.continueReq != want {
		t.Fatalf("request=%+v want %+v", runner.continueReq, want)
	}
}

func TestTaskContinueWithoutRunner(t *testing.T) {
	out, err := NewTaskContinue().Call(context.Background(), core.ToolCtx{}, json.RawMessage(`{"session_id":"child","prompt":"Also check X"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusError || out.Output != "no subagents are configured for this agent" {
		t.Fatalf("output=%+v", out)
	}
}
