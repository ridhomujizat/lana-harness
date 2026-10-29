package core

import (
	"encoding/json"
	"errors"
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
		event(t, KindUserMessage, UserMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "halo"}}}),
		event(t, KindAssistantAttempt, AssistantAttempt{V: 1, Error: PayloadError{Class: "rate_limit"}}),
		event(t, KindToolCall, ToolCallPayload{V: 1, CallID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)}),
		event(t, KindAssistantMessage, AssistantMessage{V: 1, Content: []ContentBlock{
			{Kind: BlockText, Text: "ok"},
			{Kind: BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a"}`)},
		}}),
		event(t, KindToolResult, ToolResultPayload{V: 1, CallID: "c1", Status: ToolStatusSuccess, Output: "isi"}),
		event(t, KindToolResult, ToolResultPayload{V: 1, CallID: "c2", Status: ToolStatusError, Output: "gagal"}),
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
