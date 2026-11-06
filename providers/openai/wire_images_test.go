package openai

import (
	"encoding/base64"
	"reflect"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// wireMessages returns every message of a decoded body in order.
func wireMessages(t *testing.T, got map[string]any) []map[string]any {
	t.Helper()
	raw, ok := got["messages"].([]any)
	if !ok {
		t.Fatalf("messages=%v", got["messages"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		message, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("message=%v", item)
		}
		out = append(out, message)
	}
	return out
}

func dataURL(mediaType string, data []byte) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// REQ-ATT-002: a user message with images sends content as an array of parts in
// block order, each image as a data URL.
func TestWireUserMessageWithImages(t *testing.T) {
	first := core.ContentBlock{Kind: core.BlockImage, MediaType: "image/png", Data: []byte("first-png")}
	second := core.ContentBlock{Kind: core.BlockImage, MediaType: "image/png", Data: []byte("second-png")}
	req := core.ChatRequest{Model: "m", Messages: []core.Message{
		{Role: core.RoleUser, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: "look at these"},
			first,
			second,
		}},
	}}
	got := wireBody(t, req, formatNone)
	messages := wireMessages(t, got)
	if len(messages) != 1 || messages[0]["role"] != "user" {
		t.Fatalf("messages=%v", messages)
	}
	content, ok := messages[0]["content"].([]any)
	if !ok {
		t.Fatalf("content is not an array: %v", messages[0]["content"])
	}
	if len(content) != 3 {
		t.Fatalf("parts=%d, want 3", len(content))
	}
	if !reflect.DeepEqual(content[0], map[string]any{"type": "text", "text": "look at these"}) {
		t.Fatalf("part 0=%v", content[0])
	}
	for i, block := range []core.ContentBlock{first, second} {
		part, ok := content[i+1].(map[string]any)
		if !ok || part["type"] != "image_url" {
			t.Fatalf("part %d=%v", i+1, content[i+1])
		}
		imageURL, ok := part["image_url"].(map[string]any)
		if !ok || imageURL["url"] != dataURL(block.MediaType, block.Data) {
			t.Fatalf("part %d image_url=%v", i+1, part["image_url"])
		}
	}
}

// REQ-ATT-002: a user message without an image keeps the plain string content.
func TestWireUserMessageTextOnlyUnchanged(t *testing.T) {
	req := core.ChatRequest{Model: "m", Messages: []core.Message{
		{Role: core.RoleUser, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: "hello "},
			{Kind: core.BlockText, Text: "world"},
		}},
	}}
	got := wireBody(t, req, formatNone)
	messages := wireMessages(t, got)
	if len(messages) != 1 {
		t.Fatalf("messages=%d", len(messages))
	}
	if messages[0]["content"] != "hello world" {
		t.Fatalf("content=%v, want the concatenated string", messages[0]["content"])
	}
}

// REQ-ATT-004: a tool result with an image sends the output text then the image.
func TestWireToolResultWithImages(t *testing.T) {
	image := core.ContentBlock{Kind: core.BlockImage, MediaType: "image/jpeg", Data: []byte("jpeg-bytes")}
	req := core.ChatRequest{Model: "m", Messages: []core.Message{
		{Role: core.RoleUser, Content: []core.ContentBlock{
			{Kind: core.BlockToolResult, ID: "c1", Output: "Image a.png (2x2).", Images: []core.ContentBlock{image}},
		}},
	}}
	got := wireBody(t, req, formatNone)
	messages := wireMessages(t, got)
	if len(messages) != 1 || messages[0]["role"] != "tool" {
		t.Fatalf("messages=%v", messages)
	}
	if messages[0]["tool_call_id"] != "c1" {
		t.Fatalf("tool_call_id=%v", messages[0]["tool_call_id"])
	}
	content, ok := messages[0]["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("content=%v", messages[0]["content"])
	}
	if !reflect.DeepEqual(content[0], map[string]any{"type": "text", "text": "Image a.png (2x2)."}) {
		t.Fatalf("part 0=%v", content[0])
	}
	part, ok := content[1].(map[string]any)
	if !ok || part["type"] != "image_url" {
		t.Fatalf("part 1=%v", content[1])
	}
	imageURL, ok := part["image_url"].(map[string]any)
	if !ok || imageURL["url"] != dataURL(image.MediaType, image.Data) {
		t.Fatalf("part 1 image_url=%v", part["image_url"])
	}
}
