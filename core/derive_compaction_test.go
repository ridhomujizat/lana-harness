package core

import (
	"encoding/json"
	"testing"
)

func TestDeriveCompactionCut(t *testing.T) {
	events := []SessionEvent{
		event(t, KindUserMessage, UserMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "old"}}}),
		event(t, KindAssistantMessage, AssistantMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "old answer"}}}),
		{Seq: 3, Kind: KindCompactionSummary, Payload: json.RawMessage(`{"v":1,"summary":"summary","upto_seq":2,"tokens_before":10,"tokens_after":2,"model":"m"}`)},
		{Seq: 4, Kind: KindUserMessage, Payload: json.RawMessage(`{"v":1,"content":[{"type":"text","text":"new"}]}`)},
	}
	messages, err := DeriveMessages(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Content[0].Text != "summary" || messages[1].Content[0].Text != "new" {
		t.Fatalf("messages=%+v", messages)
	}
}
