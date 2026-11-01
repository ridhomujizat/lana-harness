package memcapture

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// REQ-ATT-007: the capture transcript shows an image as its placeholder text
// and carries no image bytes, at the top level and inside a tool result.
func TestCaptureTranscriptHasNoImageBytes(t *testing.T) {
	provider := &fakeProvider{text: "[]"}
	store, svc, id, home := newTestService(t, provider)
	const path = "attachments/2026-11-01/a.png"
	userImage := core.ContentBlock{Kind: core.BlockImage, MediaType: "image/png", Path: path, Data: []byte{1, 2}}
	toolImages := []core.ContentBlock{{Kind: core.BlockImage, MediaType: "image/png", Path: path, Data: []byte{3, 4}}}

	if _, err := store.Append(context.Background(), id, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "look"}, userImage}}},
		{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: "reading"},
			{Kind: core.BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)},
		}}},
		{Kind: core.KindToolResult, TurnID: "t", Payload: core.ToolResultPayload{
			V: 1, CallID: "c1", Status: core.ToolStatusSuccess, Output: "out", Images: toolImages,
		}},
		{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	}); err != nil {
		t.Fatal(err)
	}

	svc.Capture(core.CaptureRequest{SessionID: id, TurnID: "t", AgentName: "assistant", Home: home, Provider: "mock", Model: "m", System: "head"})
	waitIdle(t, svc, id)

	want := "[image " + path + "]"
	foundPlaceholder := false
	for _, message := range provider.lastRequest().Messages {
		for _, block := range message.Content {
			if block.Kind == core.BlockImage {
				t.Fatalf("capture transcript has an image block: %+v", block)
			}
			if len(block.Data) > 0 {
				t.Fatalf("capture transcript has image bytes: %+v", block)
			}
			if len(block.Images) > 0 {
				t.Fatalf("capture transcript has tool-result images: %+v", block)
			}
			if strings.Contains(block.Text, want) || strings.Contains(block.Output, want) {
				foundPlaceholder = true
			}
		}
	}
	if !foundPlaceholder {
		t.Fatalf("capture transcript has no %q placeholder", want)
	}
}
