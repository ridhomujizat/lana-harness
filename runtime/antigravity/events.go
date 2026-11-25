package antigravity

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// mapper turns the ACP session/update notifications of one turn into
// RuntimeEvents. The runtime calls it from the turn's handler goroutine only.
type mapper struct {
	emit  func(core.RuntimeEvent)
	log   *slog.Logger
	proc  *procState
	model string
	// text is the agent text gathered since the last assistant message.
	text strings.Builder
	// calls and results remember the tool call IDs already mapped, so a tool
	// call and its result each emit once (REQ-AGY-005).
	calls   map[string]bool
	results map[string]bool
}

func newMapper(emit func(core.RuntimeEvent), log *slog.Logger, proc *procState, model string) *mapper {
	return &mapper{emit: emit, log: log, proc: proc, model: model, calls: map[string]bool{}, results: map[string]bool{}}
}

// updateParams is the params object of a session/update notification.
type updateParams struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

// updateBody is the update object. Content is kept raw: it is an object for a
// message or thought chunk and a list for a tool result.
type updateBody struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content"`
	ToolCallID    string          `json:"toolCallId"`
	Name          string          `json:"name"`
	Kind          string          `json:"kind"`
	Title         string          `json:"title"`
	Status        string          `json:"status"`
	RawInput      json.RawMessage `json:"rawInput"`
	RawOutput     json.RawMessage `json:"rawOutput"`
}

// handleUpdate maps one session/update notification (REQ-AGY-005).
func (m *mapper) handleUpdate(params json.RawMessage) {
	var p updateParams
	if json.Unmarshal(params, &p) != nil || len(p.Update) == 0 {
		return
	}
	var u updateBody
	if json.Unmarshal(p.Update, &u) != nil {
		return
	}
	switch u.SessionUpdate {
	case "agent_message_chunk":
		if text := chunkText(u.Content); text != "" {
			m.text.WriteString(text)
			m.emit(core.RuntimeEvent{Kind: core.RuntimeTextDelta, Text: text})
		}
	case "agent_thought_chunk":
		if text := chunkText(u.Content); text != "" {
			m.emit(core.RuntimeEvent{Kind: core.RuntimeThinkingDelta, Text: text})
		}
	case "tool_call":
		m.toolCall(u)
	case "tool_call_update":
		m.toolResult(u)
	default:
		// usage_update, session_info_update, config_option_update,
		// available_commands_update, user_message_chunk, plan: ignored.
	}
}

// toolCall emits a tool_call once per tool call ID (REQ-AGY-005).
func (m *mapper) toolCall(u updateBody) {
	id := u.ToolCallID
	if id == "" || m.calls[id] {
		return
	}
	m.calls[id] = true
	m.flush()
	input := u.RawInput
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	m.emit(core.RuntimeEvent{Kind: core.RuntimeToolCall, ID: id, Name: u.Name, Input: input, Risk: riskForKind(u.Kind)})
}

// toolResult emits a tool_result once for a completed or failed tool call
// (REQ-AGY-005). A failed call sets the error flag.
func (m *mapper) toolResult(u updateBody) {
	id := u.ToolCallID
	if id == "" || m.results[id] {
		return
	}
	if u.Status != "completed" && u.Status != "failed" {
		return
	}
	m.results[id] = true
	m.flush()
	m.emit(core.RuntimeEvent{Kind: core.RuntimeToolResult, ID: id, Output: toolOutputText(u), IsError: u.Status == "failed"})
}

// flush emits the gathered text as one assistant message and resets it, the
// way a native step stores one assistant/message per text run.
func (m *mapper) flush() {
	if m.text.Len() == 0 {
		return
	}
	text := m.text.String()
	m.text.Reset()
	m.emit(core.RuntimeEvent{Kind: core.RuntimeAssistantMessage, Text: text, Model: m.model})
}

// chunkText reads the text of a message or thought chunk.
func chunkText(raw json.RawMessage) string {
	var c struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &c)
	return c.Text
}

// toolOutputText flattens a tool result: the text of content[].content.text,
// else rawOutput.output (API-AGY-011).
func toolOutputText(u updateBody) string {
	if len(u.Content) > 0 {
		var items []struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(u.Content, &items) == nil {
			parts := make([]string, 0, len(items))
			for _, it := range items {
				if it.Content.Text != "" {
					parts = append(parts, it.Content.Text)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
		}
	}
	var out struct {
		Output string `json:"output"`
	}
	_ = json.Unmarshal(u.RawOutput, &out)
	return out.Output
}

// usageBody is the usage of a session/prompt result.
type usageBody struct {
	InputTokens       int64 `json:"inputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	CachedReadTokens  int64 `json:"cachedReadTokens"`
	CachedWriteTokens int64 `json:"cachedWriteTokens"`
	ThoughtTokens     int64 `json:"thoughtTokens"`
	TotalTokens       int64 `json:"totalTokens"`
}

// promptResult is the part of a session/prompt result lana reads.
type promptResult struct {
	StopReason string     `json:"stopReason"`
	Usage      *usageBody `json:"usage"`
}

// result ends the turn from a session/prompt result: the usage, then the end
// event. interrupted (or a cancelled stop reason) ends it as cancelled; the
// other stop reasons end the turn with that reason.
func (m *mapper) result(raw json.RawMessage, interrupted bool) core.RuntimeEvent {
	var res promptResult
	_ = json.Unmarshal(raw, &res)
	m.flush()
	if res.Usage != nil {
		u := *res.Usage
		m.emit(core.RuntimeEvent{Kind: core.RuntimeUsage, Usage: core.Usage{
			InputTokens:       u.InputTokens,
			OutputTokens:      u.OutputTokens,
			CachedInputTokens: u.CachedReadTokens,
		}, Model: m.model})
	}
	if interrupted {
		return core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: core.RuntimeStopCancelled}
	}
	return core.RuntimeEvent{Kind: core.RuntimeEnd, StopReason: stopReason(res.StopReason)}
}

// stopReason maps an ACP stop reason to a runtime end reason.
func stopReason(reason string) string {
	switch reason {
	case "", "end_turn":
		return core.RuntimeStopEndTurn
	case "cancelled":
		return core.RuntimeStopCancelled
	default:
		// max_tokens, max_turn_requests, refusal, and any other.
		return reason
	}
}

// sessionMeta emits the session_meta event: the ACP session ID and, when
// present, the account's plan tier (API-AGY-020).
func (m *mapper) sessionMeta(sessionID, tier string) {
	m.proc.tier = tier
	m.emit(core.RuntimeEvent{
		Kind: core.RuntimeSessionMeta, ClaudeSessionID: sessionID, Model: m.model, Tier: tier, Version: m.proc.version,
	})
}

// sessionResult is the part of a session/new or session/load result lana reads.
type sessionResult struct {
	SessionID string `json:"sessionId"`
	Meta      struct {
		Antigravity struct {
			Tier struct {
				Name string `json:"name"`
			} `json:"tier"`
		} `json:"antigravity"`
	} `json:"_meta"`
}

// sessionIDOf returns the ACP session ID of a session/new or session/load
// result, or fallback when it named none.
func sessionIDOf(raw json.RawMessage, fallback string) string {
	var r sessionResult
	if json.Unmarshal(raw, &r) == nil && r.SessionID != "" {
		return r.SessionID
	}
	return fallback
}

// tierOf returns the plan tier name of a session/new or session/load result.
func tierOf(raw json.RawMessage) string {
	var r sessionResult
	if json.Unmarshal(raw, &r) == nil {
		return r.Meta.Antigravity.Tier.Name
	}
	return ""
}
