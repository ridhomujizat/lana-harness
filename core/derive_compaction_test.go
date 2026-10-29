package core

import (
	"encoding/json"
	"testing"
)

func TestDeriveCompactionCut(t *testing.T) {
	events := []SessionEvent{
		event(t, KindUserMessage, UserMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "lama"}}}),
		event(t, KindAssistantMessage, AssistantMessage{V: 1, Content: []ContentBlock{{Kind: BlockText, Text: "jawaban lama"}}}),
		{Seq: 3, Kind: KindCompactionSummary, Payload: json.RawMessage(`{"v":1,"summary":"ringkasan","upto_seq":2,"tokens_before":10,"tokens_after":2,"model":"m"}`)},
		{Seq: 4, Kind: KindUserMessage, Payload: json.RawMessage(`{"v":1,"content":[{"type":"text","text":"baru"}]}`)},
	}
	messages, err := DeriveMessages(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Content[0].Text != "ringkasan" || messages[1].Content[0].Text != "baru" {
		t.Fatalf("messages=%+v", messages)
	}
}
