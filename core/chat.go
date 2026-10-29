package core

import (
	"context"
	"encoding/json"
	"iter"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one message. Tool results are sent as tool_result blocks in a user message.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

type BlockKind string

const (
	BlockText       BlockKind = "text"
	BlockToolUse    BlockKind = "tool_use"
	BlockToolResult BlockKind = "tool_result"
	BlockImage      BlockKind = "image"
)

// ContentBlock is one content block.
type ContentBlock struct {
	Kind      BlockKind       `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    string          `json:"output,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	MediaType string          `json:"media_type,omitempty"`
	Data      []byte          `json:"data,omitempty"`
}

// Usage is the token count.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// ChatRequest is a request to the provider.
type ChatRequest struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolSpec
	MaxTokens   int
	Temperature *float64
}

type ChatEventKind string

const (
	ChatTextDelta ChatEventKind = "text_delta"
	ChatBlock     ChatEventKind = "block"
	ChatUsage     ChatEventKind = "usage"
	ChatStop      ChatEventKind = "stop"
)

// ChatEvent is one chunk of a stream.
type ChatEvent struct {
	Kind       ChatEventKind
	Text       string
	Block      ContentBlock
	Usage      Usage
	StopReason string
}

// ChatStream is the stream returned by a provider.
type ChatStream = iter.Seq2[ChatEvent, error]

// Provider is a model adapter.
type Provider interface {
	Stream(ctx context.Context, req ChatRequest) (ChatStream, error)
}

// LLMRegistry stores providers by name from [providers.<name>].
type LLMRegistry interface {
	Provider(name string) (Provider, bool)
	Names() []string
}
