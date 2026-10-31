package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// wireBody builds and decodes a request body for a format.
func wireBody(t *testing.T, req core.ChatRequest, format string) map[string]any {
	t.Helper()
	body, err := buildRequestBody(req, format)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// assistantMessages returns the assistant messages of a decoded body.
func assistantMessages(t *testing.T, got map[string]any) []map[string]any {
	t.Helper()
	raw, ok := got["messages"].([]any)
	if !ok {
		t.Fatalf("messages=%v", got["messages"])
	}
	var out []map[string]any
	for _, item := range raw {
		message, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("message=%v", item)
		}
		if message["role"] == "assistant" {
			out = append(out, message)
		}
	}
	return out
}

// REQ-MDL-002: effort high on a deepseek provider sends thinking.type enabled
// and reasoning_effort.
func TestWireThinkingHigh(t *testing.T) {
	got := wireBody(t, core.ChatRequest{Model: "deepseek-flash", ReasoningEffort: "high"}, formatDeepSeek)
	thinking, ok := got["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking=%v", got["thinking"])
	}
	if got["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort=%v", got["reasoning_effort"])
	}
}

// REQ-MDL-002, REQ-MDL-004: effort off disables thinking, sends no
// reasoning_effort, and drops reasoning_content from assistant messages.
func TestWireThinkingOff(t *testing.T) {
	req := core.ChatRequest{Model: "deepseek-flash", ReasoningEffort: "off", Messages: []core.Message{
		{Role: core.RoleAssistant, Content: []core.ContentBlock{
			{Kind: core.BlockThinking, Text: "private reasoning"},
			{Kind: core.BlockText, Text: "answer"},
		}},
	}}
	got := wireBody(t, req, formatDeepSeek)
	thinking, ok := got["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("thinking=%v", got["thinking"])
	}
	if _, ok := got["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort present: %v", got["reasoning_effort"])
	}
	messages := assistantMessages(t, got)
	if len(messages) != 1 {
		t.Fatalf("assistant messages=%d", len(messages))
	}
	if _, ok := messages[0]["reasoning_content"]; ok {
		t.Fatalf("reasoning_content present: %v", messages[0]["reasoning_content"])
	}
	if messages[0]["content"] != "answer" {
		t.Fatalf("content=%v", messages[0]["content"])
	}
}

// REQ-MDL-002: a none provider sends neither field, whatever the effort, and
// thinking blocks never reach the wire.
func TestWireFormatNone(t *testing.T) {
	req := core.ChatRequest{Model: "m", ReasoningEffort: "high", Messages: []core.Message{
		{Role: core.RoleAssistant, Content: []core.ContentBlock{
			{Kind: core.BlockThinking, Text: "private"},
			{Kind: core.BlockText, Text: "answer"},
		}},
	}}
	got := wireBody(t, req, formatNone)
	if _, ok := got["thinking"]; ok {
		t.Fatalf("thinking present: %v", got["thinking"])
	}
	if _, ok := got["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort present: %v", got["reasoning_effort"])
	}
	messages := assistantMessages(t, got)
	if len(messages) != 1 {
		t.Fatalf("assistant messages=%d", len(messages))
	}
	if _, ok := messages[0]["reasoning_content"]; ok {
		t.Fatalf("reasoning_content present: %v", messages[0]["reasoning_content"])
	}
	if messages[0]["content"] != "answer" {
		t.Fatalf("content=%v", messages[0]["content"])
	}
}

// REQ-MDL-004: with thinking on, every assistant message carries
// reasoning_content: the joined thinking text, or "" when it has none.
func TestWireReasoningContentRoundTrip(t *testing.T) {
	req := core.ChatRequest{Model: "deepseek-flash", ReasoningEffort: "high", Messages: []core.Message{
		{Role: core.RoleAssistant, Content: []core.ContentBlock{
			{Kind: core.BlockThinking, Text: "reason one"},
			{Kind: core.BlockText, Text: "first"},
		}},
		{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "next"}}},
		{Role: core.RoleAssistant, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "second"}}},
	}}
	got := wireBody(t, req, formatDeepSeek)
	messages := assistantMessages(t, got)
	if len(messages) != 2 {
		t.Fatalf("assistant messages=%d", len(messages))
	}
	if messages[0]["reasoning_content"] != "reason one" {
		t.Fatalf("first reasoning_content=%v", messages[0]["reasoning_content"])
	}
	if messages[1]["reasoning_content"] != "" {
		t.Fatalf("second reasoning_content=%v", messages[1]["reasoning_content"])
	}
}

// REQ-MDL-002: an empty effort sends neither field; reasoning_content is added
// only when the history already carries a thinking block.
func TestWireEmptyEffort(t *testing.T) {
	withoutThinking := core.ChatRequest{Model: "deepseek-flash", Messages: []core.Message{
		{Role: core.RoleAssistant, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "a"}}},
	}}
	got := wireBody(t, withoutThinking, formatDeepSeek)
	if _, ok := got["thinking"]; ok {
		t.Fatalf("thinking present: %v", got["thinking"])
	}
	if _, ok := got["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort present: %v", got["reasoning_effort"])
	}
	if _, ok := assistantMessages(t, got)[0]["reasoning_content"]; ok {
		t.Fatalf("reasoning_content present without history thinking")
	}

	withThinking := core.ChatRequest{Model: "deepseek-flash", Messages: []core.Message{
		{Role: core.RoleAssistant, Content: []core.ContentBlock{
			{Kind: core.BlockThinking, Text: "prior"},
			{Kind: core.BlockText, Text: "a"},
		}},
	}}
	got = wireBody(t, withThinking, formatDeepSeek)
	if _, ok := got["thinking"]; ok {
		t.Fatalf("thinking present: %v", got["thinking"])
	}
	if _, ok := got["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort present: %v", got["reasoning_effort"])
	}
	if assistantMessages(t, got)[0]["reasoning_content"] != "prior" {
		t.Fatalf("reasoning_content=%v", assistantMessages(t, got)[0]["reasoning_content"])
	}
}

// REQ-MDL-002: the format comes from thinking_format when set, else the base
// URL host.
func TestResolveThinkingFormat(t *testing.T) {
	cases := []struct {
		name string
		cfg  core.ProviderConfig
		want string
	}{
		{"configured deepseek", core.ProviderConfig{ThinkingFormat: "deepseek", BaseURL: "https://example.com"}, formatDeepSeek},
		{"configured none", core.ProviderConfig{ThinkingFormat: "none", BaseURL: "https://api.deepseek.com"}, formatNone},
		{"deepseek host", core.ProviderConfig{BaseURL: "https://api.deepseek.com/v1"}, formatDeepSeek},
		{"other host", core.ProviderConfig{BaseURL: "https://api.openai.com/v1"}, formatNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveThinkingFormat(tc.cfg); got != tc.want {
				t.Fatalf("format=%q want %q", got, tc.want)
			}
		})
	}
}

// REQ-MDL-003: reasoning_content deltas yield thinking deltas, then a thinking
// block; text follows and usage is still parsed.
func TestSSEReasoningContent(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think \"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"more\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4}}\n\n" +
		"data: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	adapter, err := NewAdapter("p", Config{BaseURL: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Stream(context.Background(), core.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, stream)

	var reasoning strings.Builder
	var text strings.Builder
	var block *core.ContentBlock
	var usage *core.Usage
	for _, ev := range events {
		switch ev.Kind {
		case core.ChatThinkingDelta:
			if block != nil {
				t.Fatalf("thinking delta after the thinking block")
			}
			reasoning.WriteString(ev.Text)
		case core.ChatTextDelta:
			text.WriteString(ev.Text)
		case core.ChatBlock:
			if ev.Block.Kind != core.BlockThinking {
				t.Fatalf("unexpected block=%+v", ev.Block)
			}
			b := ev.Block
			block = &b
		case core.ChatUsage:
			u := ev.Usage
			usage = &u
		}
	}
	if reasoning.String() != "think more" {
		t.Fatalf("reasoning=%q", reasoning.String())
	}
	if text.String() != "answer" {
		t.Fatalf("text=%q", text.String())
	}
	if block == nil || block.Kind != core.BlockThinking || block.Text != "think more" {
		t.Fatalf("block=%+v", block)
	}
	if usage == nil || usage.InputTokens != 10 || usage.OutputTokens != 4 {
		t.Fatalf("usage=%+v", usage)
	}
}
