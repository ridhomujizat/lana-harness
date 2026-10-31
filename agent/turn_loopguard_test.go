package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// loopGuardRequestText returns the joined text blocks of the last message of a
// request.
func loopGuardRequestText(t *testing.T, req core.ChatRequest) string {
	t.Helper()
	if len(req.Messages) == 0 {
		t.Fatal("request has no messages")
	}
	msg := req.Messages[len(req.Messages)-1]
	if msg.Role != core.RoleUser {
		t.Fatalf("last request message role = %s, want user", msg.Role)
	}
	parts := make([]string, 0, len(msg.Content))
	for _, block := range msg.Content {
		parts = append(parts, block.Text)
	}
	return strings.Join(parts, "\n")
}

// REQ-TRL-010/013: a repeated failing call sends the gentle reminder as the
// next request's trailing runtime note, stored after the tool results.
func TestTurnLoopGuardReminder(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "read_file", `{"path":"missing.txt"}`),
		toolResponse("c2", "read_file", `{"path":"missing.txt"}`),
		toolResponse("c3", "read_file", `{"path":"missing.txt"}`),
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	rec := &requestRecorder{inner: h.llm.providers["mock"]}
	h.llm.providers["mock"] = rec

	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	if got := loopGuardRequestText(t, rec.last()); got != loopGuardGentleReminder {
		t.Fatalf("last request message = %q, want the loop guard reminder", got)
	}

	reminderIndex, lastToolResult, toolResults := -1, -1, 0
	for i, event := range h.events(t) {
		switch event.Kind {
		case core.KindToolResult:
			toolResults++
			lastToolResult = i
		case core.KindUserMessage:
			var payload core.UserMessage
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if userText(payload) != loopGuardGentleReminder {
				continue
			}
			if !payload.Runtime {
				t.Fatal("loop guard reminder stored without runtime: true")
			}
			reminderIndex = i
		}
	}
	if toolResults != 3 {
		t.Fatalf("tool results = %d, want 3", toolResults)
	}
	if reminderIndex < 0 {
		t.Fatal("loop guard reminder was not stored")
	}
	if reminderIndex < lastToolResult {
		t.Fatal("loop guard reminder was stored before the tool results")
	}
}

// REQ-TRL-013/014: an owner message waiting in the inbox is sent instead of
// the reminder, and the reminder is dropped.
func TestTurnLoopGuardDroppedForOwnerMessage(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "read_file", `{"path":"missing.txt"}`),
		toolResponse("c2", "read_file", `{"path":"missing.txt"}`),
		toolResponse("c3", "read_file", `{"path":"missing.txt"}`),
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	// Queue the owner message just before the third stream call, while the
	// turn is running.
	h.llm.providers["mock"] = &queueingProvider{
		inner: h.llm.providers["mock"], reg: h.reg, sessionID: h.info.ID,
		text: "owner interjection", after: 3,
	}

	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	waitForIdle(t, h)

	for _, event := range h.events(t) {
		if event.Kind != core.KindUserMessage {
			continue
		}
		var payload core.UserMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if userText(payload) == loopGuardGentleReminder {
			t.Fatal("loop guard reminder sent despite a queued owner message")
		}
	}
	if got := userText(h.lastUserMessage(t)); got != "owner interjection" {
		t.Fatalf("last user message = %q, want the owner interjection", got)
	}
}
