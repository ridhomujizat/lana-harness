package tools

import (
	"context"
	"encoding/json"

	"github.com/ridhomujizat/lana-harness/core"
)

// The room discussion tools of spec 47 (API-RDS-020). A team lead's root turn
// gets them; they call ToolCtx.Discussions.

var discussStartSpec = json.RawMessage(`{"type":"object","required":["topic","opening","members"],"properties":{` +
	`"topic":{"type":"string","description":"The question being discussed, in one line"},` +
	`"opening":{"type":"string","description":"One short message to the owner, in their language, saying who you bring in and why. Shown in the room as your message"},` +
	`"members":{"type":"array","items":{"type":"object","required":["agent"],"properties":{"agent":{"type":"string","description":"Member name from the roster"},"angle":{"type":"string","description":"What this member looks at, a few words"}}}}}}`)

var discussAskSpec = json.RawMessage(`{"type":"object","required":["agent","message"],"properties":{` +
	`"agent":{"type":"string","description":"The member to ask"},` +
	`"message":{"type":"string","description":"Your short message to that member, starting with @Name, in the owner's language. Shown in the room"},` +
	`"reply_to":{"type":"string","description":"The member whose point this member should answer, if any"},` +
	`"quote":{"type":"string","description":"The short part of that member's reply being answered"}}}`)

var discussMoreSpec = json.RawMessage(`{"type":"object","required":["message","reason"],"properties":{` +
	`"message":{"type":"string","description":"What you tell the room (and the owner, if they are asked): why one more round and what is still missing"},` +
	`"reason":{"type":"string","description":"The one concrete open question the extra round answers"}}}`)

var pointSchema = `{"type":"array","items":{"type":"object","required":["text"],"properties":{"text":{"type":"string"},"agent":{"type":"string","description":"The member the point came from"}}}}`

var discussEndSpec = json.RawMessage(`{"type":"object","required":["reason","conclusion"],"properties":{` +
	`"reason":{"type":"string","enum":["agreed","no_progress","limit","priorities","stopped","direct","done"]},` +
	`"conclusion":{"type":"string","description":"The conclusion in one sentence, in the owner's language"},` +
	`"agreed":` + pointSchema + `,"different":` + pointSchema + `,"open":` + pointSchema + `,` +
	`"parts":{"type":"object","additionalProperties":{"type":"string"},"description":"Per member: the part it played, one sentence"},` +
	`"notes":{"type":"array","description":"Facts, decisions and open questions worth keeping in room memory","items":{"type":"object","required":["kind","text"],"properties":{"kind":{"type":"string","enum":["decision","fact","open"]},"text":{"type":"string"},"replaces":{"type":"string","description":"Id or exact text of a room memory note this one replaces"}}}}}}`)

type discussTool struct {
	name, desc string
	spec       json.RawMessage
	parallel   bool
	call       func(ctx context.Context, d core.DiscussionService, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error)
}

func (t *discussTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: t.name, Description: t.desc, InputSchema: t.spec}
}
func (t *discussTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }
func (t *discussTool) ParallelSafe() bool             { return t.parallel }
func (t *discussTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	if tc.Discussions == nil {
		return errorOutput("discussions are only available to the lead of a team room")
	}
	return t.call(ctx, tc.Discussions, tc, raw)
}

func decodeDiscuss[T any](raw json.RawMessage) (T, error) {
	var in T
	err := decodeInput(raw, &in)
	return in, err
}

// NewDiscussTools creates discuss_start, discuss_ask, discuss_more and
// discuss_end.
func NewDiscussTools() []core.Tool {
	return []core.Tool{
		&discussTool{name: "discuss_start", spec: discussStartSpec,
			desc: "Opens a discussion among team members on a question that needs more than one view.",
			call: func(ctx context.Context, d core.DiscussionService, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
				in, err := decodeDiscuss[core.DiscussStart](raw)
				if err != nil {
					return inputErrorOutput(err)
				}
				return d.Start(ctx, tc, in)
			}},
		&discussTool{name: "discuss_ask", spec: discussAskSpec, parallel: true,
			desc: "Asks one member in the open discussion and returns its reply. Several calls in one step run in parallel.",
			call: func(ctx context.Context, d core.DiscussionService, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
				in, err := decodeDiscuss[core.DiscussAsk](raw)
				if err != nil {
					return inputErrorOutput(err)
				}
				return d.Ask(ctx, tc, in)
			}},
		&discussTool{name: "discuss_more", spec: discussMoreSpec,
			desc: "Adds one round to the open discussion when the last round brought something new and one question is still open.",
			call: func(ctx context.Context, d core.DiscussionService, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
				in, err := decodeDiscuss[core.DiscussMore](raw)
				if err != nil {
					return inputErrorOutput(err)
				}
				return d.More(ctx, tc, in)
			}},
		&discussTool{name: "discuss_end", spec: discussEndSpec,
			desc: "Ends the open discussion with a summary and room memory notes.",
			call: func(ctx context.Context, d core.DiscussionService, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
				in, err := decodeDiscuss[core.DiscussEnd](raw)
				if err != nil {
					return inputErrorOutput(err)
				}
				return d.End(ctx, tc, in)
			}},
	}
}
