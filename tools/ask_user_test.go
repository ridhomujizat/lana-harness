package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/question"
	"github.com/ridhomujizat/lana-harness/store/memstore"
	"github.com/ridhomujizat/lana-harness/tools"
	"github.com/ridhomujizat/lana-harness/tools/permission"
)

const validAskUserInput = `{"questions":[{"question":"Where should I save it?","header":"Save","options":[{"label":"a","description":"first"},{"label":"b"}]}]}`

type fakeQuestionService struct {
	calls   int
	req     core.QuestionRequest
	outcome core.QuestionOutcome
	err     error
}

func (f *fakeQuestionService) Ask(_ context.Context, req core.QuestionRequest) (core.QuestionOutcome, error) {
	f.calls++
	f.req = req
	return f.outcome, f.err
}

func toolCall(t *testing.T, service core.QuestionService, raw string) core.ToolOutput {
	t.Helper()
	tool := tools.NewAskUser()
	ctx := core.ToolCtx{SessionID: "s1", TurnID: "t1", CallID: "c1", AgentName: "assistant", Questions: service}
	out, err := tool.Call(context.Background(), ctx, json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if out.Status == "" {
		t.Fatal("tool returned no status")
	}
	return out
}

func TestAskUserSpecAndRisk(t *testing.T) {
	tool := tools.NewAskUser()
	spec := tool.Spec()
	if spec.Name != "ask_user" {
		t.Fatalf("name=%q", spec.Name)
	}
	var schema struct {
		Type       string `json:"type"`
		Required   []string
		Properties struct {
			Questions struct {
				Type     string `json:"type"`
				MinItems int    `json:"minItems"`
				MaxItems int    `json:"maxItems"`
				Items    struct {
					Required             []string `json:"required"`
					AdditionalProperties bool     `json:"additionalProperties"`
					Properties           struct {
						MultiSelect map[string]any `json:"multi_select"`
						Options     struct {
							MinItems int `json:"minItems"`
							MaxItems int `json:"maxItems"`
						} `json:"options"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"questions"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Type != "object" || strings.Join(schema.Required, ",") != "questions" {
		t.Fatalf("schema=%+v", schema)
	}
	if schema.Properties.Questions.MinItems != 1 || schema.Properties.Questions.MaxItems != 3 {
		t.Fatalf("question limits=%d..%d", schema.Properties.Questions.MinItems, schema.Properties.Questions.MaxItems)
	}
	if schema.Properties.Questions.Items.Required[0] != "question" ||
		schema.Properties.Questions.Items.Required[1] != "options" ||
		schema.Properties.Questions.Items.AdditionalProperties {
		t.Fatalf("question item=%+v", schema.Properties.Questions.Items)
	}
	if schema.Properties.Questions.Items.Properties.Options.MinItems != 2 ||
		schema.Properties.Questions.Items.Properties.Options.MaxItems != 6 {
		t.Fatalf("option limits=%d..%d", schema.Properties.Questions.Items.Properties.Options.MinItems, schema.Properties.Questions.Items.Properties.Options.MaxItems)
	}
	for _, input := range []string{`{}`, `{"questions":[]}`, `not json`} {
		if got := tool.Risk(json.RawMessage(input)); got != core.RiskRead {
			t.Fatalf("Risk(%s)=%q", input, got)
		}
	}
}

// AC-ASK-003 and AC-ASK-004: invalid input is an error and never reaches the
// responder.
func TestAskUserValidation(t *testing.T) {
	longQuestion := strings.Repeat("q", question.MaxQuestionRunes+1)
	longLabel := strings.Repeat("l", question.MaxLabelRunes+1)
	longDescription := strings.Repeat("d", question.MaxDescRunes+1)
	longHeader := strings.Repeat("h", question.MaxHeaderRunes+1)
	cases := map[string]string{
		"four questions":   `{"questions":[` + questionJSON("a") + `,` + questionJSON("b") + `,` + questionJSON("c") + `,` + questionJSON("d") + `]}`,
		"duplicate labels": `{"questions":[{"question":"Pick","options":[{"label":"same"},{"label":"same"}]}]}`,
		"empty title":      `{"questions":[{"question":"  ","options":[{"label":"a"},{"label":"b"}]}]}`,
		"empty label":      `{"questions":[{"question":"Pick","options":[{"label":" "},{"label":"b"}]}]}`,
		"one option":       `{"questions":[{"question":"Pick","options":[{"label":"a"}]}]}`,
		"seven options":    `{"questions":[{"question":"Pick","options":[{"label":"a"},{"label":"b"},{"label":"c"},{"label":"d"},{"label":"e"},{"label":"f"},{"label":"g"}]}]}`,
		"long question":    `{"questions":[{"question":"` + longQuestion + `","options":[{"label":"a"},{"label":"b"}]}]}`,
		"long header":      `{"questions":[{"question":"Pick","header":"` + longHeader + `","options":[{"label":"a"},{"label":"b"}]}]}`,
		"long label":       `{"questions":[{"question":"Pick","options":[{"label":"` + longLabel + `"},{"label":"b"}]}]}`,
		"long description": `{"questions":[{"question":"Pick","options":[{"label":"a","description":"` + longDescription + `"},{"label":"b"}]}]}`,
		"unknown field":    `{"questions":[{"question":"Pick","extra":1,"options":[{"label":"a"},{"label":"b"}]}]}`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			service := &fakeQuestionService{}
			out := toolCall(t, service, input)
			if out.Status != core.ToolStatusError || !strings.HasPrefix(out.Output, "invalid input: ") {
				t.Fatalf("out=%+v", out)
			}
			if service.calls != 0 {
				t.Fatalf("responder called %d times", service.calls)
			}
		})
	}
}

func questionJSON(label string) string {
	return `{"question":"` + label + `?","options":[{"label":"a"},{"label":"b"}]}`
}

// AC-ASK-011: the output keeps the contract key order and shape.
func TestAskUserAnsweredOutput(t *testing.T) {
	service := &fakeQuestionService{outcome: core.QuestionOutcome{
		Status: core.QuestionStatusAnswered,
		Answers: []core.QuestionAnswer{
			{Question: "Where?", Selected: []string{"a"}},
			{Question: "Which?", Selected: []string{"x", "y"}, Other: "typed"},
			{Question: "Skip?", Skipped: true},
		},
	}}
	input := `{"questions":[{"question":"Where?","header":"H","multi_select":false,"options":[{"label":"a","description":"first"},{"label":"b"}]}]}`
	out := toolCall(t, service, input)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status=%q", out.Status)
	}
	want := `{"status":"answered","answers":[{"question":"Where?","selected":["a"]},{"question":"Which?","selected":["x","y"],"other":"typed"},{"question":"Skip?","skipped":true}]}`
	if out.Output != want {
		t.Fatalf("output=%s\nwant  =%s", out.Output, want)
	}
	if service.req.SessionID != "s1" || service.req.TurnID != "t1" || service.req.CallID != "c1" || service.req.AgentName != "assistant" {
		t.Fatalf("request=%+v", service.req)
	}
	if len(service.req.Questions) != 1 || service.req.Questions[0].Text != "Where?" ||
		service.req.Questions[0].Header != "H" || service.req.Questions[0].MultiSelect ||
		len(service.req.Questions[0].Options) != 2 ||
		service.req.Questions[0].Options[0].Description != "first" {
		t.Fatalf("questions=%+v", service.req.Questions)
	}
}

// AC-ASK-012: every unavailable reason returns status success with the contract
// JSON, and a missing service returns no_responder at once.
func TestAskUserUnavailableReasons(t *testing.T) {
	for _, reason := range []string{
		core.QuestionReasonNoResponder,
		core.QuestionReasonTimeout,
		core.QuestionReasonCancelled,
		core.QuestionReasonInterrupted,
	} {
		t.Run(reason, func(t *testing.T) {
			service := &fakeQuestionService{outcome: core.QuestionOutcome{Status: core.QuestionStatusUnavailable, Reason: reason}}
			out := toolCall(t, service, validAskUserInput)
			if out.Status != core.ToolStatusSuccess {
				t.Fatalf("status=%q", out.Status)
			}
			want := `{"status":"unavailable","reason":"` + reason + `"}`
			if out.Output != want {
				t.Fatalf("output=%s want=%s", out.Output, want)
			}
		})
	}
	out := toolCall(t, nil, validAskUserInput)
	if out.Status != core.ToolStatusSuccess || out.Output != `{"status":"unavailable","reason":"no_responder"}` {
		t.Fatalf("nil service out=%+v", out)
	}
}

func TestAskUserBusy(t *testing.T) {
	service := &fakeQuestionService{err: question.ErrBusy}
	out := toolCall(t, service, validAskUserInput)
	if out.Status != core.ToolStatusError || out.Output != "a question is already open in this session" {
		t.Fatalf("out=%+v", out)
	}
}

// AC-ASK-001: risk read is allowed under trust, ask, and never, and the tool
// never reaches the approval responder.
func TestAskUserAllowedUnderEveryPolicy(t *testing.T) {
	for _, policy := range []core.Policy{core.PolicyTrust, core.PolicyAsk, core.PolicyNever} {
		t.Run(string(policy), func(t *testing.T) {
			store := memstore.New()
			info, err := store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "web", Workspace: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			hooks := core.NewHooks()
			pipeline := tools.NewPipeline(hooks)
			config := core.Config{Agents: map[string]core.AgentConfig{"assistant": {Permission: string(policy)}}}
			engine := &permission.Engine{Store: store, Config: config}
			hooks.ToolsPreExecute.Use(engine.Handle)

			tool := tools.NewAskUser()
			call := core.ToolCall{
				SessionID: info.ID, TurnID: "t1", CallID: "c1", Name: "ask_user",
				Input: json.RawMessage(validAskUserInput), Risk: tool.Risk(json.RawMessage(validAskUserInput)),
			}
			decision, err := pipeline.PreExecute(context.Background(), core.ToolsPreExecuteInput{AgentName: "assistant", Call: call})
			if err != nil {
				t.Fatal(err)
			}
			if !decision.Allowed {
				t.Fatalf("decision=%+v", decision)
			}
			service := &fakeQuestionService{outcome: core.QuestionOutcome{Status: core.QuestionStatusAnswered, Answers: []core.QuestionAnswer{{Question: "Where should I save it?", Selected: []string{"a"}}}}}
			out, err := pipeline.Execute(context.Background(), core.ToolsExecuteInput{
				Ctx:  core.ToolCtx{SessionID: info.ID, TurnID: "t1", CallID: "c1", AgentName: "assistant", Questions: service},
				Call: call,
				Tool: tool,
			})
			if err != nil {
				t.Fatal(err)
			}
			if out.Status != core.ToolStatusSuccess || service.calls != 1 {
				t.Fatalf("out=%+v calls=%d", out, service.calls)
			}
			events, err := store.ChainEvents(context.Background(), info.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range events {
				if event.Kind == core.KindApprovalAsked {
					t.Fatalf("approval/asked written under %s", policy)
				}
			}
		})
	}
}
