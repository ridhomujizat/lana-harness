package compaction

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// REQ-ATT-007: the compaction summary request shows an image as its placeholder
// text and carries no image bytes, at the top level and inside a tool result.
func TestSummaryRequestHasNoImageBytes(t *testing.T) {
	provider := &recordProvider{text: "condensed"}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "m"},
	}})
	const path = "attachments/2026-11-01/a.png"
	userImage := core.ContentBlock{Kind: core.BlockImage, MediaType: "image/png", Path: path, Data: []byte{1, 2, 3}}
	toolImages := []core.ContentBlock{{Kind: core.BlockImage, MediaType: "image/png", Path: path, Data: []byte{4, 5, 6}}}

	appendEvents(t, store, info.ID,
		core.EventDraft{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		core.EventDraft{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: strings.Repeat("u", 60000)}, userImage,
		}}},
		core.EventDraft{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: "step"},
			{Kind: core.BlockToolUse, ID: "c1", Name: "big", Input: json.RawMessage(`{}`)},
		}}},
		core.EventDraft{Kind: core.KindToolResult, TurnID: "t", Payload: core.ToolResultPayload{
			V: 1, CallID: "c1", Status: core.ToolStatusSuccess, Output: strings.Repeat("x", 20000), Images: toolImages,
		}},
		core.EventDraft{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "next"}}}},
		core.EventDraft{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "ok"}}}},
		core.EventDraft{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	)

	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "coder", Provider: "mock", Model: "m", System: "head", Trigger: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	if provider.count() != 1 {
		t.Fatalf("summary calls = %d, want 1", provider.count())
	}

	want := "[image " + path + "]"
	foundPlaceholder := false
	for _, message := range provider.last().Messages {
		for _, block := range message.Content {
			if block.Kind == core.BlockImage {
				t.Fatalf("summary request has an image block: %+v", block)
			}
			if len(block.Data) > 0 {
				t.Fatalf("summary request has image bytes: %+v", block)
			}
			if len(block.Images) > 0 {
				t.Fatalf("summary request has tool-result images: %+v", block)
			}
			if strings.Contains(block.Text, want) || strings.Contains(block.Output, want) {
				foundPlaceholder = true
			}
		}
	}
	if !foundPlaceholder {
		t.Fatalf("summary request has no %q placeholder", want)
	}
}

// REQ-ATT-003: the overflow retry re-derives the messages, which carry the
// stored image bytes again. When the original request had none, the retry must
// replace them with placeholder text instead of resending images to a model
// that cannot see them.
func TestOverflowRetryStripsImages(t *testing.T) {
	provider := &recordProvider{text: "condensed", failures: 1}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "m"},
	}})
	const path = "attachments/2026-11-01/a.png"
	appendEvents(t, store, info.ID,
		core.EventDraft{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		core.EventDraft{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: strings.Repeat("u", 60000)},
		}}},
		core.EventDraft{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "reply"}}}},
		core.EventDraft{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{
			{Kind: core.BlockImage, MediaType: "image/png", Path: path, Data: []byte{1, 2, 3}},
		}}},
	)
	hooks := core.NewHooks()
	hooks.LLMStream.SetDefault(func(ctx context.Context, in core.LLMStreamInput) (core.ChatStream, error) {
		return provider.Stream(ctx, in.Request)
	})
	service.Install(hooks)

	// The agent already replaced the image with its placeholder for this
	// non-vision model, so the original request carries no image bytes.
	req := core.ChatRequest{Model: "m", System: "head", Messages: []core.Message{
		{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "[image " + path + ": the current model cannot see images]"}}},
	}}
	stream, err := hooks.LLMStream.Call(context.Background(), core.LLMStreamInput{
		SessionID: info.ID, TurnID: "t", Step: 1, Targets: []core.LLMTarget{{Provider: "mock", Model: "m"}}, Request: req,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
	}

	if hasImageData(provider.last().Messages) {
		t.Fatalf("retry request carries image bytes: %+v", provider.last().Messages)
	}
	want := "[image " + path + ": the current model cannot see images]"
	found := false
	for _, message := range provider.last().Messages {
		for _, block := range message.Content {
			if strings.Contains(block.Text, want) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("retry request has no %q placeholder: %+v", want, provider.last().Messages)
	}
}
