package openai

import (
	"encoding/json"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

// ErrUnsupported dikembalikan untuk isi yang tidak bisa dipetakan ke OpenAI.
var ErrUnsupported = fmt.Errorf("tidak didukung")

type wireRequest struct {
	Model         string           `json:"model"`
	Stream        bool             `json:"stream"`
	StreamOptions map[string]bool  `json:"stream_options"`
	Messages      []map[string]any `json:"messages"`
	Tools         []wireTool       `json:"tools,omitempty"`
	MaxTokens     int              `json:"max_tokens,omitempty"`
	Temperature   *float64         `json:"temperature,omitempty"`
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

func buildRequestBody(req core.ChatRequest) ([]byte, error) {
	out := wireRequest{
		Model:         req.Model,
		Stream:        true,
		StreamOptions: map[string]bool{"include_usage": true},
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
	}
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
			var calls []map[string]any
			for _, block := range message.Content {
				switch block.Kind {
				case core.BlockText:
					text += block.Text
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
			if len(calls) > 0 {
				msg["tool_calls"] = calls
			}
			out.Messages = append(out.Messages, msg)
		default:
			return nil, fmt.Errorf("role tidak dikenal: %s", message.Role)
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
