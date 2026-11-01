package core

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func event(t *testing.T, kind string, payload any) SessionEvent {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return SessionEvent{Kind: kind, Payload: raw}
}

func TestDeriveGolden(t *testing.T) {
	events := []SessionEvent{
		event(t, KindSessionCreated, SessionCreated{V: 1, Agent: "a"}),
		event(t, KindSystemMessage, SystemMessage{V: 1, Text: "sys"}),
		event(t, KindUserMessage, UserMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "hello"}}}),
		event(t, KindAssistantAttempt, AssistantAttempt{V: 1, Error: PayloadError{Class: "rate_limit"}}),
		event(t, KindToolCall, ToolCallPayload{V: 1, CallID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)}),
		event(t, KindAssistantMessage, AssistantMessage{V: 1, Content: []ContentBlock{
			{Kind: BlockText, Text: "ok"},
			{Kind: BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a"}`)},
		}}),
		event(t, KindToolResult, ToolResultPayload{V: 1, CallID: "c1", Status: ToolStatusSuccess, Output: "content"}),
		event(t, KindToolResult, ToolResultPayload{V: 1, CallID: "c2", Status: ToolStatusError, Output: "failed"}),
	}
	messages, err := DeriveMessages(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages=%d %+v", len(messages), messages)
	}
	if messages[0].Role != RoleUser || messages[1].Role != RoleAssistant || messages[2].Role != RoleUser {
		t.Fatalf("roles=%v", messages)
	}
	if len(messages[1].Content) != 2 || messages[1].Content[1].Kind != BlockToolUse {
		t.Fatalf("assistant=%+v", messages[1])
	}
	if len(messages[2].Content) != 2 || messages[2].Content[0].ID != "c1" || !messages[2].Content[1].IsError {
		t.Fatalf("tool results=%+v", messages[2])
	}
}

func TestDeriveUnknownPayloadVersion(t *testing.T) {
	events := []SessionEvent{{Kind: KindUserMessage, Payload: json.RawMessage(`{"v":99,"content":[]}`)}}
	if _, err := DeriveMessages(events); !errors.Is(err, ErrUnknownPayloadVersion) {
		t.Fatalf("err=%v", err)
	}
}

func seqEvent(t *testing.T, seq int64, kind string, payload any) SessionEvent {
	t.Helper()
	event := event(t, kind, payload)
	event.Seq = seq
	return event
}

// REQ-LTC-016: a summary with retained_from_seq frames the summary and keeps a
// tail; a summary without it keeps the older drop-everything behavior.
func TestDeriveSummaryFraming(t *testing.T) {
	newSummary := []SessionEvent{
		seqEvent(t, 1, KindUserMessage, UserMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "old"}}}),
		seqEvent(t, 2, KindAssistantMessage, AssistantMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "old reply"}}}),
		seqEvent(t, 3, KindCompactionSummary, CompactionSummary{V: 1, Summary: "S", FromSeq: 1, UptoSeq: 2, RetainedFromSeq: 4}),
		seqEvent(t, 4, KindUserMessage, UserMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "new"}}}),
		seqEvent(t, 5, KindAssistantMessage, AssistantMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "new reply"}}}),
	}
	messages, err := DeriveMessages(newSummary)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3: %+v", len(messages), messages)
	}
	want := CheckpointPreamble + "\n\n<compacted-summary>\nS\n</compacted-summary>"
	if messages[0].Content[0].Text != want {
		t.Fatalf("framed summary = %q", messages[0].Content[0].Text)
	}
	if messages[1].Content[0].Text != "new" || messages[2].Content[0].Text != "new reply" {
		t.Fatalf("tail = %+v", messages[1:])
	}

	oldSummary := []SessionEvent{
		seqEvent(t, 1, KindUserMessage, UserMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "old"}}}),
		seqEvent(t, 2, KindAssistantMessage, AssistantMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "old reply"}}}),
		seqEvent(t, 3, KindCompactionSummary, CompactionSummary{V: 1, Summary: "S", UptoSeq: 2}),
		seqEvent(t, 4, KindUserMessage, UserMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "new"}}}),
	}
	messages, err = DeriveMessages(oldSummary)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("old summary messages = %d, want 2: %+v", len(messages), messages)
	}
	if messages[0].Content[0].Text != "S" {
		t.Fatalf("old summary is not the raw text: %q", messages[0].Content[0].Text)
	}
}

// REQ-MEM-014: memory/captured is not part of the model history.
func TestDeriveIgnoresMemoryCaptured(t *testing.T) {
	events := []SessionEvent{
		seqEvent(t, 1, KindUserMessage, UserMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "hello"}}}),
		seqEvent(t, 2, KindAssistantMessage, AssistantMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "hi"}}}),
		seqEvent(t, 3, KindMemoryCaptured, MemoryCaptured{V: 1, TurnID: "t", Ops: []MemoryOp{{Target: "user", Action: "add", Content: "x"}}}),
	}
	messages, err := DeriveMessages(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2: %+v", len(messages), messages)
	}
	if messages[0].Content[0].Text != "hello" || messages[1].Content[0].Text != "hi" {
		t.Fatalf("history changed: %+v", messages)
	}
}

// REQ-KNW-024: knowledge/compiled is not part of the model history.
func TestDeriveIgnoresKnowledgeCompiled(t *testing.T) {
	events := []SessionEvent{
		seqEvent(t, 1, KindUserMessage, UserMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "hello"}}}),
		seqEvent(t, 2, KindAssistantMessage, AssistantMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "hi"}}}),
		seqEvent(t, 3, KindKnowledgeCompiled, KnowledgeCompiled{V: 1, Source: "sources/a.txt", Ops: []KnowledgeCompiledOp{{Op: "create", Title: "A"}}}),
	}
	messages, err := DeriveMessages(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2: %+v", len(messages), messages)
	}
	if messages[0].Content[0].Text != "hello" || messages[1].Content[0].Text != "hi" {
		t.Fatalf("history changed: %+v", messages)
	}
}

// REQ-LTC-012: DeriveMessages uses the latest pruned output for a tool call.
func TestDeriveAppliesPrune(t *testing.T) {
	events := []SessionEvent{
		seqEvent(t, 1, KindAssistantMessage, AssistantMessage{V: 1, Content: []ContentBlock{
			{Kind: BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)},
		}}),
		seqEvent(t, 2, KindToolResult, ToolResultPayload{V: 1, CallID: "c1", Status: ToolStatusSuccess, Output: "full output"}),
		seqEvent(t, 3, KindToolResultPruned, ToolResultPruned{V: 1, CallID: "c1", SourceSeq: 2, Output: "pruned output"}),
	}
	messages, err := DeriveMessages(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Content[0].Output != "pruned output" {
		t.Fatalf("pruned output not applied: %+v", messages)
	}
}

// REQ-ATT-004: a tool result payload keeps its images on the derived block, and
// an image block in user/message passes through unchanged.
func TestDeriveToolResultImages(t *testing.T) {
	userImage := ContentBlock{Kind: BlockImage, MediaType: "image/jpeg", Data: []byte("jpeg-bytes"), Path: "attachments/b.jpg"}
	resultImage := ContentBlock{Kind: BlockImage, MediaType: "image/png", Data: []byte("png-bytes"), Path: "attachments/a.png"}
	events := []SessionEvent{
		event(t, KindUserMessage, UserMessage{V: 1, Content: []ContentBlock{
			{Kind: BlockText, Text: "look"},
			userImage,
		}}),
		event(t, KindAssistantMessage, AssistantMessage{V: 1, Content: []ContentBlock{
			{Kind: BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)},
		}}),
		event(t, KindToolResult, ToolResultPayload{V: 1, CallID: "c1", Status: ToolStatusSuccess, Output: "Image a.png (2x2).", Images: []ContentBlock{resultImage}}),
	}
	messages, err := DeriveMessages(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages=%d: %+v", len(messages), messages)
	}
	if !reflect.DeepEqual(messages[0].Content[1], userImage) {
		t.Fatalf("user image changed: %+v", messages[0].Content[1])
	}
	block := messages[2].Content[0]
	if block.Kind != BlockToolResult || len(block.Images) != 1 || !reflect.DeepEqual(block.Images[0], resultImage) {
		t.Fatalf("tool result block=%+v", block)
	}
}

// REQ-ATT-007: a pruned tool result keeps only the pruned output, whose image
// placeholder lines compaction already wrote, while an unpruned result keeps
// its images.
func TestDeriveToolResultImagesPruned(t *testing.T) {
	image := ContentBlock{Kind: BlockImage, MediaType: "image/png", Data: []byte("png-bytes"), Path: "attachments/a.png"}
	base := []SessionEvent{
		event(t, KindAssistantMessage, AssistantMessage{V: 1, Content: []ContentBlock{
			{Kind: BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)},
		}}),
		event(t, KindToolResult, ToolResultPayload{V: 1, CallID: "c1", Status: ToolStatusSuccess, Output: "Image a.png (2x2).", Images: []ContentBlock{image}}),
	}

	t.Run("pruned", func(t *testing.T) {
		events := append(append([]SessionEvent(nil), base...),
			seqEvent(t, 3, KindToolResultPruned, ToolResultPruned{V: 1, CallID: "c1", SourceSeq: 2, Output: "pruned output\n[image attachments/a.png]"}))
		messages, err := DeriveMessages(events)
		if err != nil {
			t.Fatal(err)
		}
		block := messages[1].Content[0]
		if block.Output != "pruned output\n[image attachments/a.png]" {
			t.Fatalf("output = %q", block.Output)
		}
		if len(block.Images) != 0 {
			t.Fatalf("pruned result kept images: %+v", block.Images)
		}
	})

	t.Run("kept", func(t *testing.T) {
		messages, err := DeriveMessages(base)
		if err != nil {
			t.Fatal(err)
		}
		block := messages[1].Content[0]
		if block.Output != "Image a.png (2x2)." {
			t.Fatalf("output = %q", block.Output)
		}
		if len(block.Images) != 1 || !reflect.DeepEqual(block.Images[0], image) {
			t.Fatalf("images = %+v", block.Images)
		}
	})
}
