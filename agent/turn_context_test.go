package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// REQ-LTC-001: the first user message of a turn records the injected block
// count, while the model history keeps the <context> block.
func TestUserMessageContextBlocks(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, nil)
	h.run(t, "hi")

	msg := h.firstUserMessage(t)
	if msg.ContextBlocks != 1 {
		t.Fatalf("ContextBlocks=%d want 1", msg.ContextBlocks)
	}
	if len(msg.Content) < 2 || !strings.HasPrefix(msg.Content[0].Text, "<context>") {
		t.Fatalf("first block is not the <context> block: %+v", msg.Content)
	}
	if got := userText(msg); !strings.Contains(got, "hi") {
		t.Fatalf("user text lost: %q", got)
	}

	history, err := h.store.DeriveMessages(context.Background(), h.info.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range history {
		for _, block := range message.Content {
			if strings.HasPrefix(block.Text, "<context>") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("model history lost the <context> block: %+v", history)
	}
}

// REQ-LTC-001: a turn whose first user message also carries a <system-update>
// records a count of 2.
func TestUserMessageContextBlocksWithSystemUpdate(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("a"), textResponse("b")}}, nil)
	writeHomeFile(t, h.home, "assistant", "MEMORY.md", "Server kenari")
	h.run(t, "one")
	writeHomeFile(t, h.home, "assistant", "MEMORY.md", "Server bandung")
	h.run(t, "two")

	msg := h.lastUserMessage(t)
	if msg.ContextBlocks != 2 {
		t.Fatalf("ContextBlocks=%d want 2", msg.ContextBlocks)
	}
}

// A reply to an automation message injects the quoted message as an extra
// context block before the owner's text, records ReplyTo, and keeps the quote
// in the derived history for later turns.
func TestUserMessageReplyQuote(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, nil)
	reply := &core.ReplyRef{Seq: 7, Source: "workflow", SourceName: "Daily digest", Text: "Here is your report."}
	turnID, err := h.reg.StartReply(context.Background(), h.info.ID, "explain item 2", reply)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.reg.Wait(context.Background(), turnID); err != nil {
		t.Fatal(err)
	}

	msg := h.firstUserMessage(t)
	if msg.ContextBlocks != 2 {
		t.Fatalf("ContextBlocks=%d want 2: %+v", msg.ContextBlocks, msg.Content)
	}
	if msg.ReplyTo == nil || msg.ReplyTo.Seq != 7 || msg.ReplyTo.SourceName != "Daily digest" {
		t.Fatalf("ReplyTo=%+v", msg.ReplyTo)
	}
	wantQuote := "[The owner is replying to this message you posted via workflow Daily digest]\nHere is your report."
	if msg.Content[1].Text != wantQuote {
		t.Fatalf("quote block = %q, want %q", msg.Content[1].Text, wantQuote)
	}
	if got := core.TextContent(msg.Content[msg.ContextBlocks:]); got != "explain item 2" {
		t.Fatalf("owner text = %q", got)
	}

	// The quote is a normal user-message block, so it stays in the history.
	history, err := h.store.DeriveMessages(context.Background(), h.info.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range history {
		for _, block := range message.Content {
			if strings.Contains(block.Text, "Here is your report.") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("history lost the reply quote: %+v", history)
	}
}
