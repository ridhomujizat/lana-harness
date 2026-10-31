package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// ErrUnsupported is returned for content that cannot be mapped to OpenAI.
var ErrUnsupported = fmt.Errorf("unsupported")

// Thinking formats of a provider (REQ-MDL-002).
const (
	formatDeepSeek = "deepseek"
	formatNone     = "none"
)

type wireRequest struct {
	Model           string            `json:"model"`
	Stream          bool              `json:"stream"`
	StreamOptions   map[string]bool   `json:"stream_options"`
	Messages        []map[string]any  `json:"messages"`
	Tools           []wireTool        `json:"tools,omitempty"`
	MaxTokens       int               `json:"max_tokens,omitempty"`
	Temperature     *float64          `json:"temperature,omitempty"`
	Thinking        map[string]string `json:"thinking,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// buildRequestBody renders a ChatRequest for the OpenAI-compatible endpoint.
// format is the provider's thinking format: deepseek adds the thinking and
// reasoning_effort fields and round-trips reasoning_content; none sends
// neither and drops thinking blocks (REQ-MDL-002, REQ-MDL-004).
func buildRequestBody(req core.ChatRequest, format string) ([]byte, error) {
	out := wireRequest{
		Model:         req.Model,
		Stream:        true,
		StreamOptions: map[string]bool{"include_usage": true},
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
	}
	thinkingOn := applyThinking(req, format, &out)
	if req.System != "" {
		out.Messages = append(out.Messages, map[string]any{"role": "system", "content": req.System})
	}
	for _, message := range req.Messages {
		switch message.Role {
		case core.RoleUser:
			var text string
			var results []map[string]any
			for _, block := range message.Content {
				switch block.Kind {
				case core.BlockText:
					text += block.Text
				case core.BlockImage:
					return nil, ErrUnsupported
				case core.BlockToolResult:
					results = append(results, map[string]any{
						"role":         "tool",
						"tool_call_id": block.ID,
						"content":      block.Output,
					})
				}
			}
			if text != "" {
				out.Messages = append(out.Messages, map[string]any{"role": "user", "content": text})
			}
			out.Messages = append(out.Messages, results...)
		case core.RoleAssistant:
			var text string
			var reasoning strings.Builder
			var calls []map[string]any
			for _, block := range message.Content {
				switch block.Kind {
				case core.BlockText:
					text += block.Text
				case core.BlockThinking:
					reasoning.WriteString(block.Text)
				case core.BlockImage:
					return nil, ErrUnsupported
				case core.BlockToolUse:
					args := string(block.Input)
					if args == "" {
						args = "{}"
					}
					calls = append(calls, map[string]any{
						"id":   block.ID,
						"type": "function",
						"function": map[string]any{
							"name":      block.Name,
							"arguments": args,
						},
					})
				}
			}
			msg := map[string]any{"role": "assistant"}
			if text != "" {
				msg["content"] = text
			} else {
				msg["content"] = nil
			}
			if thinkingOn {
				msg["reasoning_content"] = reasoning.String()
			}
			if len(calls) > 0 {
				msg["tool_calls"] = calls
			}
			out.Messages = append(out.Messages, msg)
		default:
			return nil, fmt.Errorf("unknown role: %s", message.Role)
		}
	}
	for _, spec := range req.Tools {
		parameters := spec.InputSchema
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object"}`)
		}
		out.Tools = append(out.Tools, wireTool{
			Type: "function",
			Function: wireFunction{
				Name:        spec.Name,
				Description: spec.Description,
				Parameters:  parameters,
			},
		})
	}
	return json.Marshal(out)
}

// applyThinking sets the thinking and reasoning_effort fields on a deepseek
// request and reports whether every assistant message carries reasoning_content.
// An empty effort sends neither field: the server default applies, and
// reasoning_content is added only when the history already has a thinking block
// (REQ-MDL-002, REQ-MDL-004).
func applyThinking(req core.ChatRequest, format string, out *wireRequest) bool {
	if format != formatDeepSeek {
		return false
	}
	switch req.ReasoningEffort {
	case "off":
		out.Thinking = map[string]string{"type": "disabled"}
		return false
	case "low", "high", "max":
		out.Thinking = map[string]string{"type": "enabled"}
		out.ReasoningEffort = req.ReasoningEffort
		return true
	default:
		return historyHasThinking(req.Messages)
	}
}

// historyHasThinking reports whether any message carries a thinking block.
func historyHasThinking(messages []core.Message) bool {
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Kind == core.BlockThinking {
				return true
			}
		}
	}
	return false
}
