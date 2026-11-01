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
	BlockThinking   BlockKind = "thinking"
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
	Path      string          `json:"path,omitempty"`   // workspace-relative path of an image block
	Images    []ContentBlock  `json:"images,omitempty"` // image blocks of a tool_result block
}

// Usage is the token count. InputTokens counts all prompt tokens
// (cache hit + miss); CachedInputTokens is the part served from the
// provider prefix cache.
type Usage struct {
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`
}

// ChatRequest is a request to the provider.
type ChatRequest struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolSpec
	MaxTokens   int
	Temperature *float64
	// ReasoningEffort selects the thinking effort: "", "off", "low", "high",
	// or "max". "" means the provider default (REQ-MDL-001).
	ReasoningEffort string
}

type ChatEventKind string

const (
	ChatTextDelta     ChatEventKind = "text_delta"
	ChatThinkingDelta ChatEventKind = "thinking_delta"
	ChatBlock         ChatEventKind = "block"
	ChatUsage         ChatEventKind = "usage"
	ChatStop          ChatEventKind = "stop"
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
