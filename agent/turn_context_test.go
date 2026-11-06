package agent

import (
	"context"
	"strings"
	"testing"

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
