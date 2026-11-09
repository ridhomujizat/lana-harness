package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
)

const (
	// maxTokensCap caps the catalog output limit used when the request sets
	// no max_tokens.
	maxTokensCap = 32000
	// maxTokensUnknown is max_tokens for a model outside the catalog.
	maxTokensUnknown = 8192
)

// block is one Messages API content block. A map keeps fields such as an
// empty "thinking" string that a struct with omitempty would drop.
type block = map[string]any

type wireMessage struct {
	Role    string  `json:"role"`
	Content []block `json:"content"`
}

type wireTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

type wireRequest struct {
	Model        string          `json:"model"`
	MaxTokens    int             `json:"max_tokens"`
	System       []block         `json:"system,omitempty"`
	Messages     []wireMessage   `json:"messages"`
	Tools        []wireTool      `json:"tools,omitempty"`
	Thinking     *thinkingConfig `json:"thinking,omitempty"`
	OutputConfig *outputConfig   `json:"output_config,omitempty"`
	Temperature  *float64        `json:"temperature,omitempty"`
	Stream       bool            `json:"stream"`
}

func ephemeral() *cacheControl { return &cacheControl{Type: "ephemeral"} }

// buildRequestBody maps a core request to a streaming Messages request.
// Prompt caching: the system block and the last tool carry cache_control,
// and so does the last block of a trailing user message, so each agent step
// reads the previous step's prefix from the cache.
func buildRequestBody(req core.ChatRequest) ([]byte, error) {
	maxOutput := 0
	if m, ok := models.Lookup(req.Model); ok {
		maxOutput = m.MaxOutput
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		switch {
		case maxOutput <= 0:
			maxTokens = maxTokensUnknown
		case maxOutput > maxTokensCap:
			maxTokens = maxTokensCap
		default:
			maxTokens = maxOutput
		}
	}
	plan := planThinking(req.Model, req.ReasoningEffort, maxTokens, maxOutput)
	out := wireRequest{
		Model:        req.Model,
		MaxTokens:    plan.maxTokens,
		Messages:     buildMessages(req.Messages),
		Thinking:     plan.thinking,
		OutputConfig: plan.output,
		Stream:       true,
	}
	if plan.sampling && req.Temperature != nil {
		t := *req.Temperature
		out.Temperature = &t
	}
	if req.System != "" {
		out.System = []block{{"type": "text", "text": req.System, "cache_control": ephemeral()}}
	}
	for _, tool := range req.Tools {
		schema := tool.InputSchema
		if len(schema) == 0 || !json.Valid(schema) {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out.Tools = append(out.Tools, wireTool{Name: tool.Name, Description: tool.Description, InputSchema: schema})
	}
	if n := len(out.Tools); n > 0 {
		out.Tools[n-1].CacheControl = ephemeral()
	}
	if n := len(out.Messages); n > 0 && out.Messages[n-1].Role == string(core.RoleUser) {
		content := out.Messages[n-1].Content
		content[len(content)-1]["cache_control"] = ephemeral()
	}
	return json.Marshal(out)
}

// buildMessages maps core messages to API messages. Empty messages are
// dropped, consecutive messages of one role are merged (the API wants the
// roles to alternate), and tool_result blocks lead their user message.
func buildMessages(messages []core.Message) []wireMessage {
	var out []wireMessage
	for _, message := range messages {
		var content []block
		role := string(message.Role)
		switch message.Role {
		case core.RoleUser:
			content = userBlocks(message.Content)
		case core.RoleAssistant:
			content = assistantBlocks(message.Content)
		default:
			continue
		}
		if len(content) == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, content...)
			continue
		}
		out = append(out, wireMessage{Role: role, Content: content})
	}
	for i := range out {
		if out[i].Role == string(core.RoleUser) {
			out[i].Content = toolResultsFirst(out[i].Content)
		}
	}
	return out
}

func toolResultsFirst(content []block) []block {
	results := make([]block, 0, len(content))
	var rest []block
	for _, b := range content {
		if b["type"] == "tool_result" {
			results = append(results, b)
		} else {
			rest = append(rest, b)
		}
	}
	return append(results, rest...)
}

func userBlocks(blocks []core.ContentBlock) []block {
	var out []block
	for _, b := range blocks {
		switch b.Kind {
		case core.BlockText:
			if b.Text != "" {
				out = append(out, block{"type": "text", "text": b.Text})
			}
		case core.BlockImage:
			if image := imageBlock(b); image != nil {
				out = append(out, image)
			}
		case core.BlockToolResult:
			out = append(out, toolResultBlock(b))
		}
	}
	return out
}

func assistantBlocks(blocks []core.ContentBlock) []block {
	var out []block
	for _, b := range blocks {
		switch b.Kind {
		case core.BlockText:
			if b.Text != "" {
				out = append(out, block{"type": "text", "text": b.Text})
			}
		case core.BlockThinking:
			if thinking := thinkingBlock(b); thinking != nil {
				out = append(out, thinking)
			}
		case core.BlockToolUse:
			input := b.Input
			if len(input) == 0 || !json.Valid(input) {
				input = json.RawMessage(`{}`)
			}
			out = append(out, block{"type": "tool_use", "id": toolID(b.ID), "name": b.Name, "input": input})
		}
	}
	return out
}

// thinkingBlock returns the API block of a signed thinking block, or nil for
// a thinking block without a signature: it came from another provider and
// the API would reject it.
func thinkingBlock(b core.ContentBlock) block {
	if data, ok := strings.CutPrefix(b.Signature, core.RedactedThinkingPrefix); ok {
		if data == "" {
			return nil
		}
		return block{"type": "redacted_thinking", "data": data}
	}
	if b.Signature == "" {
		return nil
	}
	return block{"type": "thinking", "thinking": b.Text, "signature": b.Signature}
}

func imageBlock(b core.ContentBlock) block {
	if len(b.Data) == 0 || b.MediaType == "" {
		return nil
	}
	return block{"type": "image", "source": map[string]any{
		"type":       "base64",
		"media_type": b.MediaType,
		"data":       base64.StdEncoding.EncodeToString(b.Data),
	}}
}

// toolResultBlock renders a tool_result: string content, or a content array
// of the output text and the images (REQ-ATT-004).
func toolResultBlock(b core.ContentBlock) block {
	out := block{"type": "tool_result", "tool_use_id": toolID(b.ID)}
	var images []block
	for _, image := range b.Images {
		if part := imageBlock(image); part != nil {
			images = append(images, part)
		}
	}
	switch {
	case len(images) > 0:
		parts := make([]block, 0, len(images)+1)
		if b.Output != "" {
			parts = append(parts, block{"type": "text", "text": b.Output})
		}
		out["content"] = append(parts, images...)
	case b.Output != "":
		out["content"] = b.Output
	}
	if b.IsError {
		out["is_error"] = true
	}
	return out
}

// toolID makes a tool call ID match the API pattern ^[a-zA-Z0-9_-]+$: IDs
// from other providers may carry other characters. The same mapping is used
// for tool_use and tool_result, so the pair still matches.
func toolID(id string) string {
	if id == "" {
		return "toolu_empty"
	}
	ok := true
	for _, r := range id {
		if !isIDRune(r) {
			ok = false
			break
		}
	}
	if ok {
		return id
	}
	var b strings.Builder
	for _, r := range id {
		if isIDRune(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func isIDRune(r rune) bool {
	return r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
