package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
)

// ErrUnsupported is returned for content that cannot be mapped to OpenAI.
var ErrUnsupported = fmt.Errorf("unsupported")

// Thinking formats of a provider (REQ-MDL-002).
const (
	formatDeepSeek = "deepseek"
	formatNone     = "none"
)

type wireRequest struct {
	Model         string           `json:"model"`
	Stream        bool             `json:"stream"`
	StreamOptions map[string]bool  `json:"stream_options"`
	Messages      []map[string]any `json:"messages"`
	Tools         []wireTool       `json:"tools,omitempty"`
	MaxTokens     int              `json:"max_tokens,omitempty"`
	// MaxCompletionTokens replaces max_tokens on kind openai: OpenAI rejects
	// max_tokens on reasoning models.
	MaxCompletionTokens int               `json:"max_completion_tokens,omitempty"`
	Temperature         *float64          `json:"temperature,omitempty"`
	Thinking            map[string]string `json:"thinking,omitempty"`
	ReasoningEffort     string            `json:"reasoning_effort,omitempty"`
	// Reasoning is OpenRouter's unified reasoning object.
	Reasoning map[string]any `json:"reasoning,omitempty"`
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

// buildRequestBody renders a ChatRequest for an openai_compat provider.
func buildRequestBody(req core.ChatRequest, format string) ([]byte, error) {
	return buildKindRequestBody(req, kindCompat, format)
}

// buildKindRequestBody renders a ChatRequest for the OpenAI-compatible
// endpoint of a provider kind. format is the provider's thinking format:
// deepseek adds the thinking and reasoning_effort fields and round-trips
// reasoning_content; none sends neither and drops thinking blocks
// (REQ-MDL-002, REQ-MDL-004). Without the deepseek format, kind openai maps
// the effort to reasoning_effort and kind openrouter to its reasoning object.
func buildKindRequestBody(req core.ChatRequest, kind, format string) ([]byte, error) {
	out := wireRequest{
		Model:         req.Model,
		Stream:        true,
		StreamOptions: map[string]bool{"include_usage": true},
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
	}
	if kind == kindOpenAI {
		out.MaxTokens = 0
		out.MaxCompletionTokens = req.MaxTokens
	}
	thinkingOn := applyThinking(req, format, &out)
	if format != formatDeepSeek {
		switch kind {
		case kindOpenAI:
			applyOpenAIEffort(req, &out)
		case kindOpenRouter:
			applyOpenRouterReasoning(req, &out)
		}
	}
	if req.System != "" {
		out.Messages = append(out.Messages, map[string]any{"role": "system", "content": req.System})
	}
	for _, message := range req.Messages {
		switch message.Role {
		case core.RoleUser:
			var text string
			var parts []map[string]any
			var results []map[string]any
			hasImage := false
			for _, block := range message.Content {
				switch block.Kind {
				case core.BlockText:
					text += block.Text
					if block.Text != "" {
						parts = append(parts, textPart(block.Text))
					}
				case core.BlockImage:
					hasImage = true
					parts = append(parts, imagePart(block))
				case core.BlockToolResult:
					results = append(results, toolResultMessage(block))
				}
			}
			if hasImage {
				out.Messages = append(out.Messages, map[string]any{"role": "user", "content": parts})
			} else if text != "" {
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

// textPart renders one text part of an OpenAI content array.
func textPart(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

// imagePart renders an image content block as an OpenAI image_url part. The
// bytes travel inline as a data URL (REQ-ATT-002).
func imagePart(block core.ContentBlock) map[string]any {
	return map[string]any{
		"type": "image_url",
		"image_url": map[string]any{
			"url": "data:" + block.MediaType + ";base64," + base64.StdEncoding.EncodeToString(block.Data),
		},
	}
}

// toolResultMessage renders a tool_result block as a role "tool" message. A
// block with images carries a content array: the output text, then one image
// part per image (REQ-ATT-004).
func toolResultMessage(block core.ContentBlock) map[string]any {
	var content any = block.Output
	if len(block.Images) > 0 {
		parts := []map[string]any{textPart(block.Output)}
		for _, image := range block.Images {
			parts = append(parts, imagePart(image))
		}
		content = parts
	}
	return map[string]any{
		"role":         "tool",
		"tool_call_id": block.ID,
		"content":      content,
	}
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

// openAIEfforts maps lana efforts to OpenAI reasoning_effort values.
var openAIEfforts = map[string]string{"off": "minimal", "low": "low", "high": "high", "max": "xhigh"}

// applyOpenAIEffort sets reasoning_effort on a kind openai request. An empty
// effort sends nothing (the model default applies). A catalog model without
// thinking never gets it, since OpenAI rejects the field there; a catalog
// thinking model and an unknown model get it when the agent sets an effort.
// Catalog thinking models also drop temperature, which OpenAI reasoning
// models reject.
func applyOpenAIEffort(req core.ChatRequest, out *wireRequest) {
	entry, known := models.Lookup(req.Model)
	if known && entry.Thinking != "" {
		out.Temperature = nil
	}
	effort, ok := openAIEfforts[req.ReasoningEffort]
	if !ok || (known && entry.Thinking == "") {
		return
	}
	out.ReasoningEffort = effort
}

// applyOpenRouterReasoning sets OpenRouter's reasoning object: off disables
// reasoning, low and high pass through, and max maps to high. An empty
// effort sends nothing.
func applyOpenRouterReasoning(req core.ChatRequest, out *wireRequest) {
	switch req.ReasoningEffort {
	case "off":
		out.Reasoning = map[string]any{"enabled": false}
	case "low":
		out.Reasoning = map[string]any{"effort": "low"}
	case "high", "max":
		out.Reasoning = map[string]any{"effort": "high"}
	}
}
