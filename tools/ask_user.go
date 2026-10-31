package tools

import (
	"context"
	"encoding/json"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/question"
)

var askUserSpec = json.RawMessage(`{"type":"object","properties":{"questions":{"type":"array","minItems":1,"maxItems":3,"items":{"type":"object","properties":{"question":{"type":"string","minLength":1,"maxLength":300},"header":{"type":"string","maxLength":30},"multi_select":{"type":"boolean"},"options":{"type":"array","minItems":2,"maxItems":6,"items":{"type":"object","properties":{"label":{"type":"string","minLength":1,"maxLength":80},"description":{"type":"string","maxLength":200}},"required":["label"],"additionalProperties":false}}},"required":["question","options"],"additionalProperties":false}}},"required":["questions"],"additionalProperties":false}`)

type askUserInput struct {
	Questions []askUserQuestion `json:"questions"`
}

type askUserQuestion struct {
	Question    string          `json:"question"`
	Header      string          `json:"header"`
	MultiSelect bool            `json:"multi_select"`
	Options     []askUserOption `json:"options"`
}

type askUserOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// askUserOutput keeps the contract key order: status, then answers or reason.
type askUserOutput struct {
	Status  string                `json:"status"`
	Answers []core.QuestionAnswer `json:"answers,omitempty"`
	Reason  string                `json:"reason,omitempty"`
}

type askUserTool struct{}

// askUserGuidance is the head-prompt note shown when ask_user is offered
// (REQ-ECX-011).
const askUserGuidance = "Ask the owner when a decision belongs to them or the task is blocked on an answer they hold, because those choices are not yours to make. " +
	"Do not ask for something the tools can find out (files, search results, command output); check first."

// NewAskUser creates the ask_user tool.
func NewAskUser() core.Tool { return &askUserTool{} }

func (t *askUserTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        "ask_user",
		Description: "Asks the owner one to three multiple-choice questions and returns the answers.",
		InputSchema: askUserSpec,
	}
}

func (t *askUserTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *askUserTool) PromptGuidance() string { return askUserGuidance }

func (t *askUserTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in askUserInput
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	questions := make([]core.Question, 0, len(in.Questions))
	for _, item := range in.Questions {
		options := make([]core.QuestionOption, 0, len(item.Options))
		for _, option := range item.Options {
			options = append(options, core.QuestionOption{Label: option.Label, Description: option.Description})
		}
		questions = append(questions, core.Question{
			Text:        item.Question,
			Header:      item.Header,
			MultiSelect: item.MultiSelect,
			Options:     options,
		})
	}
	if err := question.Validate(questions); err != nil {
		return inputErrorOutput(err)
	}
	if tc.Questions == nil {
		return questionOutput(core.QuestionOutcome{Status: core.QuestionStatusUnavailable, Reason: core.QuestionReasonNoResponder})
	}
	outcome, err := tc.Questions.Ask(ctx, core.QuestionRequest{
		SessionID: tc.SessionID,
		TurnID:    tc.TurnID,
		CallID:    tc.CallID,
		AgentName: tc.AgentName,
		Questions: questions,
	})
	if err != nil {
		return errorOutput(err.Error())
	}
	return questionOutput(outcome)
}

func questionOutput(outcome core.QuestionOutcome) (core.ToolOutput, error) {
	status := outcome.Status
	if status != core.QuestionStatusAnswered {
		status = core.QuestionStatusUnavailable
	}
	out := askUserOutput{Status: status, Reason: outcome.Reason}
	if status == core.QuestionStatusAnswered {
		out.Answers = outcome.Answers
		out.Reason = ""
	}
	data, err := json.Marshal(out)
	if err != nil {
		return core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()}, nil
	}
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: string(data)}, nil
}
