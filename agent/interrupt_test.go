package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// TestInterruptReasons covers AC-TFB-001: a turn cut by the shutdown interrupt
// cause ends "interrupted" with the cause message and its tool result reports
// "interrupted", while a user cancel stays "cancelled".
func TestInterruptReasons(t *testing.T) {
	for _, test := range []struct {
		name    string
		cause   error
		message string
	}{
		{name: "restart", cause: core.ErrInterruptedRestart, message: "lana restarted while this turn was running"},
		{name: "stop", cause: core.ErrInterruptedStop, message: "lana stopped while this turn was running"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t, mock.Script{Responses: []mock.Response{
				{Events: []mock.Event{
					{ToolUse: &mock.ToolUse{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"sleep 5"}`)}},
					{Stop: strptr("tool_use")},
				}},
			}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"bash"}}})
			h.hooks.ToolsPreExecute.Use(func(ctx context.Context, in core.ToolsPreExecuteInput, next core.Next[core.ToolsPreExecuteInput, core.ToolDecision]) (core.ToolDecision, error) {
				return core.ToolDecision{Allowed: true}, nil
			})
			turnID, err := h.reg.Start(context.Background(), h.info.ID, "hi")
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return hasEvent(h, core.KindToolCall) })
			if err := h.reg.Interrupt(context.Background(), h.info.ID, test.cause); err != nil {
				t.Fatal(err)
			}
			result, err := h.reg.Wait(context.Background(), turnID)
			if err != nil {
				t.Fatal(err)
			}
			if result.StopReason != "interrupted" {
				t.Fatalf("stop reason = %q, want interrupted", result.StopReason)
			}
			if result.Err != nil {
				t.Fatalf("turn error = %v, want nil", result.Err)
			}

			var end *core.TurnEnd
			var toolOutput string
			for _, event := range h.events(t) {
				if event.TurnID != turnID {
					continue
				}
				switch event.Kind {
				case core.KindTurnEnd:
					var payload core.TurnEnd
					if err := json.Unmarshal(event.Payload, &payload); err != nil {
						t.Fatal(err)
					}
					end = &payload
				case core.KindToolResult:
					var payload core.ToolResultPayload
					if err := json.Unmarshal(event.Payload, &payload); err != nil {
						t.Fatal(err)
					}
					if payload.CallID == "c1" {
						if payload.Status != core.ToolStatusError {
							t.Fatalf("tool status = %q, want error", payload.Status)
						}
						toolOutput = payload.Output
					}
				}
			}
			if end == nil {
				t.Fatal("no turn/end")
			}
			if end.Reason != "interrupted" {
				t.Fatalf("turn/end reason = %q, want interrupted", end.Reason)
			}
			if end.Error == nil || end.Error.Message != test.message {
				t.Fatalf("turn/end error = %+v, want message %q", end.Error, test.message)
			}
			if toolOutput != "interrupted" {
				t.Fatalf("tool output = %q, want interrupted", toolOutput)
			}
		})
	}
}
