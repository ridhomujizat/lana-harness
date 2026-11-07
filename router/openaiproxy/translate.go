package openaiproxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// chatRequest is the OpenAI Chat Completions request subset of API-RTR-015.
// Unknown fields are ignored; tool_choice is ignored.
type chatRequest struct {
	Model               string         `json:"model"`
	Messages            []chatMessage  `json:"messages"`
	Tools               []chatTool     `json:"tools"`
	MaxTokens           *int           `json:"max_tokens"`
	MaxCompletionTokens *int           `json:"max_completion_tokens"`
	Temperature         *float64       `json:"temperature"`
	ReasoningEffort     string         `json:"reasoning_effort"`
	Stream              bool           `json:"stream"`
	StreamOptions       *streamOptions `json:"stream_options"`
	N                   *int           `json:"n"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role string `json:"role"`
	// Content is a string, an array of content parts, or null.
	Content          json.RawMessage `json:"content"`
	ReasoningContent string          `json:"reasoning_content"`
	ToolCalls        []toolCall      `json:"tool_calls"`
	ToolCallID       string          `json:"tool_call_id"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text"`
	Refusal  string    `json:"refusal"`
	ImageURL *imageURL `json:"image_url"`
}

type imageURL struct {
	URL string `json:"url"`
}

type toolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// emptySchema is the input schema of a function tool without parameters.
var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

// effortMap maps OpenAI reasoning_effort values to core efforts.
var effortMap = map[string]string{
	"none":    "off",
	"minimal": "low",
	"low":     "low",
	"medium":  "high",
	"high":    "high",
	"xhigh":   "max",
}

// toChatRequest translates an OpenAI request into a core.ChatRequest. Model
// is left empty: the router sets it per target.
func toChatRequest(in *chatRequest) (core.ChatRequest, error) {
	var out core.ChatRequest
	if in.N != nil && *in.N != 1 {
		return out, badRequest("invalid_value", "Only n = 1 is supported.")
	}
	if len(in.Messages) == 0 {
		return out, badRequest("invalid_value", "'messages' must contain at least one message.")
	}
	if in.MaxCompletionTokens != nil {
		out.MaxTokens = *in.MaxCompletionTokens
	} else if in.MaxTokens != nil {
		out.MaxTokens = *in.MaxTokens
	}
	if out.MaxTokens < 0 {
		return out, badRequest("invalid_value", "'max_tokens' must not be negative.")
	}
	out.Temperature = in.Temperature
	if in.ReasoningEffort != "" {
		effort, ok := effortMap[in.ReasoningEffort]
		if !ok {
			return out, badRequest("invalid_value", fmt.Sprintf("'reasoning_effort' %q is not supported; use none, minimal, low, medium, high, or xhigh.", in.ReasoningEffort))
		}
		out.ReasoningEffort = effort
	}
	for i, tool := range in.Tools {
		if tool.Type != "function" {
			return out, badRequest("invalid_value", fmt.Sprintf("tools[%d]: type %q is not supported; only function tools are.", i, tool.Type))
		}
		if tool.Function.Name == "" {
			return out, badRequest("invalid_value", fmt.Sprintf("tools[%d]: function.name is required.", i))
		}
		schema := tool.Function.Parameters
		if len(bytes.TrimSpace(schema)) == 0 || string(bytes.TrimSpace(schema)) == "null" {
			schema = emptySchema
		}
		out.Tools = append(out.Tools, core.ToolSpec{Name: tool.Function.Name, Description: tool.Function.Description, InputSchema: schema})
	}

	var system []string
	for i, msg := range in.Messages {
		switch msg.Role {
		case "system", "developer":
			text, err := textContent(msg.Content, i)
			if err != nil {
				return out, err
			}
			if text != "" {
				system = append(system, text)
			}
		case "user":
			blocks, err := userContent(msg.Content, i)
			if err != nil {
				return out, err
			}
			if len(blocks) > 0 {
				out.Messages = append(out.Messages, core.Message{Role: core.RoleUser, Content: blocks})
			}
		case "assistant":
			blocks, err := assistantContent(msg, i)
			if err != nil {
				return out, err
			}
			if len(blocks) > 0 {
				out.Messages = append(out.Messages, core.Message{Role: core.RoleAssistant, Content: blocks})
			}
		case "tool":
			text, err := textContent(msg.Content, i)
			if err != nil {
				return out, err
			}
			block := core.ContentBlock{Kind: core.BlockToolResult, ID: msg.ToolCallID, Output: text}
			// Consecutive tool messages answer one assistant turn: they go
			// into one user message.
			if n := len(out.Messages); n > 0 && i > 0 && in.Messages[i-1].Role == "tool" {
				out.Messages[n-1].Content = append(out.Messages[n-1].Content, block)
				continue
			}
			out.Messages = append(out.Messages, core.Message{Role: core.RoleUser, Content: []core.ContentBlock{block}})
		default:
			return out, badRequest("invalid_value", fmt.Sprintf("messages[%d]: role %q is not supported.", i, msg.Role))
		}
	}
	out.System = strings.Join(system, "\n\n")
	if len(out.Messages) == 0 {
		return out, badRequest("invalid_value", "'messages' must contain a user, assistant, or tool message.")
	}
	return out, nil
}

// parseContent reads content as a string or an array of parts. null and a
// missing field give no parts.
func parseContent(raw json.RawMessage, i int) (string, []contentPart, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return "", nil, false, nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", nil, false, badRequest("invalid_value", fmt.Sprintf("messages[%d].content: %v", i, err))
		}
		return s, nil, true, nil
	}
	var parts []contentPart
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return "", nil, false, badRequest("invalid_value", fmt.Sprintf("messages[%d].content must be a string or an array of content parts.", i))
	}
	return "", parts, false, nil
}

// textContent reads a content that may hold only text.
func textContent(raw json.RawMessage, i int) (string, error) {
	s, parts, isString, err := parseContent(raw, i)
	if err != nil || isString {
		return s, err
	}
	var texts []string
	for j, part := range parts {
		switch part.Type {
		case "text":
			texts = append(texts, part.Text)
		default:
			return "", badRequest("invalid_value", fmt.Sprintf("messages[%d].content[%d]: type %q is not supported here; only text is.", i, j, part.Type))
		}
	}
	return strings.Join(texts, "\n"), nil
}

// userContent reads a user content: text and image_url data URLs.
func userContent(raw json.RawMessage, i int) ([]core.ContentBlock, error) {
	s, parts, isString, err := parseContent(raw, i)
	if err != nil {
		return nil, err
	}
	if isString {
		if s == "" {
			return nil, nil
		}
		return []core.ContentBlock{{Kind: core.BlockText, Text: s}}, nil
	}
	var blocks []core.ContentBlock
	for j, part := range parts {
		switch part.Type {
		case "text":
			if part.Text != "" {
				blocks = append(blocks, core.ContentBlock{Kind: core.BlockText, Text: part.Text})
			}
		case "image_url":
			if part.ImageURL == nil {
				return nil, badRequest("invalid_value", fmt.Sprintf("messages[%d].content[%d]: image_url.url is required.", i, j))
			}
			block, err := imageBlock(part.ImageURL.URL, i, j)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
		default:
			return nil, badRequest("invalid_value", fmt.Sprintf("messages[%d].content[%d]: type %q is not supported; use text or image_url.", i, j, part.Type))
		}
	}
	return blocks, nil
}

// imageBlock decodes a "data:<media type>;base64,<data>" URL. Remote URLs are
// not fetched.
func imageBlock(url string, i, j int) (core.ContentBlock, error) {
	where := fmt.Sprintf("messages[%d].content[%d].image_url", i, j)
	rest, ok := strings.CutPrefix(url, "data:")
	if !ok {
		if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
			return core.ContentBlock{}, badRequest("unsupported_value", where+": remote image URLs are not supported; send the image as a base64 data URL.")
		}
		return core.ContentBlock{}, badRequest("invalid_value", where+": the URL must be a base64 data URL.")
	}
	meta, data, ok := strings.Cut(rest, ",")
	if !ok {
		return core.ContentBlock{}, badRequest("invalid_value", where+": the data URL has no data.")
	}
	params := strings.Split(meta, ";")
	mediaType := strings.ToLower(strings.TrimSpace(params[0]))
	base64Encoded := false
	for _, p := range params[1:] {
		if strings.EqualFold(strings.TrimSpace(p), "base64") {
			base64Encoded = true
		}
	}
	if !base64Encoded {
		return core.ContentBlock{}, badRequest("invalid_value", where+": the data URL must be base64 encoded.")
	}
	if !strings.HasPrefix(mediaType, "image/") {
		return core.ContentBlock{}, badRequest("invalid_value", where+": the media type must be an image type.")
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(data, "="))
	}
	if err != nil || len(decoded) == 0 {
		return core.ContentBlock{}, badRequest("invalid_value", where+": the image data is not valid base64.")
	}
	return core.ContentBlock{Kind: core.BlockImage, MediaType: mediaType, Data: decoded}, nil
}

// assistantContent reads an assistant message: reasoning_content becomes a
// thinking block, text content a text block, tool_calls tool_use blocks.
func assistantContent(msg chatMessage, i int) ([]core.ContentBlock, error) {
	var blocks []core.ContentBlock
	if msg.ReasoningContent != "" {
		blocks = append(blocks, core.ContentBlock{Kind: core.BlockThinking, Text: msg.ReasoningContent})
	}
	s, parts, isString, err := parseContent(msg.Content, i)
	if err != nil {
		return nil, err
	}
	text := s
	if !isString {
		var texts []string
		for j, part := range parts {
			switch part.Type {
			case "text":
				texts = append(texts, part.Text)
			case "refusal":
				texts = append(texts, part.Refusal)
			default:
				return nil, badRequest("invalid_value", fmt.Sprintf("messages[%d].content[%d]: type %q is not supported in an assistant message.", i, j, part.Type))
			}
		}
		text = strings.Join(texts, "\n")
	}
	if text != "" {
		blocks = append(blocks, core.ContentBlock{Kind: core.BlockText, Text: text})
	}
	for j, call := range msg.ToolCalls {
		if call.Type != "" && call.Type != "function" {
			return nil, badRequest("invalid_value", fmt.Sprintf("messages[%d].tool_calls[%d]: type %q is not supported.", i, j, call.Type))
		}
		if call.Function.Name == "" {
			return nil, badRequest("invalid_value", fmt.Sprintf("messages[%d].tool_calls[%d]: function.name is required.", i, j))
		}
		args := strings.TrimSpace(call.Function.Arguments)
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			return nil, badRequest("invalid_value", fmt.Sprintf("messages[%d].tool_calls[%d]: function.arguments is not valid JSON.", i, j))
		}
		blocks = append(blocks, core.ContentBlock{Kind: core.BlockToolUse, ID: call.ID, Name: call.Function.Name, Input: json.RawMessage(args)})
	}
	return blocks, nil
}
