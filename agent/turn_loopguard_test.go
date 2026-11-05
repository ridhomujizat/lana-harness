package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

// allowAllTools replaces the harness's fail-closed gate so the loop guard tests
// can run bash.
func allowAllTools(h *harness) {
	h.hooks.ToolsPreExecute.Use(func(ctx context.Context, in core.ToolsPreExecuteInput, next core.Next[core.ToolsPreExecuteInput, core.ToolDecision]) (core.ToolDecision, error) {
		return core.ToolDecision{Allowed: true}, nil
	})
}

// repeatedToolResponses builds n tool responses that call the same tool with
// the same input.
func repeatedToolResponses(n int, name, input string) []mock.Response {
	out := make([]mock.Response, n)
	for i := range out {
		out[i] = toolResponse(fmt.Sprintf("c%d", i), name, input)
	}
	return out
}

// storedBlockedResult reports whether the session stored a tool result whose
// output starts with prefix.
func storedBlockedResult(t *testing.T, h *harness, prefix string) bool {
	t.Helper()
	for _, event := range h.events(t) {
		if event.Kind != core.KindToolResult {
			continue
		}
		var payload core.ToolResultPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(payload.Output, prefix) {
			return true
		}
	}
	return false
}

// assertReportKeepsTools checks REQ-LPG-015 (AC-LPG-016): the last recorded
// request is the report step and it offers the same tool names as the request
// before it.
func assertReportKeepsTools(t *testing.T, rec *requestRecorder) {
	t.Helper()
	if len(rec.requests) < 2 {
		t.Fatalf("recorded %d requests, want at least 2", len(rec.requests))
	}
	report := toolSpecNames(rec.requests[len(rec.requests)-1].Tools)
	previous := toolSpecNames(rec.requests[len(rec.requests)-2].Tools)
	if len(report) == 0 {
		t.Fatal("report step offered no tools, want the same tools as the step before")
	}
	if strings.Join(report, ",") != strings.Join(previous, ",") {
		t.Fatalf("report step tools %v, previous %v", report, previous)
	}
}

// AC-LPG-002, AC-LPG-012: a failing call repeated until the guard blocks it is
// not executed; the next step is the report step, the report text is stored,
// and the turn ends with reason loop.
func TestTurnLoopGuardBlockHaltsTurn(t *testing.T) {
	responses := repeatedToolResponses(loopGuardFailBlock+1, "bash", `{"command":"false"}`)
	responses = append(responses, textResponse("here is the report"))
	h := newHarness(t, mock.Script{Responses: responses}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"bash"}},
	})
	allowAllTools(h)
	rec := &requestRecorder{inner: h.llm.providers["mock"]}
	h.llm.providers["mock"] = rec

	result := h.run(t, "hi")
	if result.StopReason != "loop" {
		t.Fatalf("result=%+v", result)
	}
	if result.Text != "here is the report" {
		t.Fatalf("report text = %q, want the stored report", result.Text)
	}
	assertReportKeepsTools(t, rec)
	if !storedBlockedResult(t, h, "Blocked bash") {
		t.Fatal("the blocked call result was not stored")
	}
	haltNote := false
	for _, event := range h.events(t) {
		if event.Kind != core.KindUserMessage {
			continue
		}
		var payload core.UserMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(userText(payload), "lana stopped this turn") {
			continue
		}
		haltNote = true
		if !payload.Runtime {
			t.Fatal("halt note stored without runtime: true")
		}
	}
	if !haltNote {
		t.Fatal("the halt note was not stored")
	}
	if reason := turnEndReason(t, h, h.info.ID); reason != "loop" {
		t.Fatalf("turn/end reason = %q, want loop", reason)
	}
}

// AC-LPG-006: eight identical successful calls with the same output are
// followed by a blocked ninth call; the turn halts with reason loop.
func TestTurnLoopGuardIdenticalStreakHalts(t *testing.T) {
	responses := repeatedToolResponses(loopGuardStreakLen+1, "bash", `{"command":"true"}`)
	responses = append(responses, textResponse("report"))
	h := newHarness(t, mock.Script{Responses: responses}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"bash"}},
	})
	allowAllTools(h)
	rec := &requestRecorder{inner: h.llm.providers["mock"]}
	h.llm.providers["mock"] = rec

	result := h.run(t, "hi")
	if result.StopReason != "loop" {
		t.Fatalf("result=%+v", result)
	}
	assertReportKeepsTools(t, rec)
	if !storedBlockedResult(t, h, "Blocked bash") {
		t.Fatal("the blocked call result was not stored")
	}
	if reason := turnEndReason(t, h, h.info.ID); reason != "loop" {
		t.Fatalf("turn/end reason = %q, want loop", reason)
	}
}

// AC-LPG-017: a tool call returned by the halt report step is not run; its
// result is the REQ-LPG-015 error, the turn ends with reason loop, and no
// further step runs.
func TestTurnLoopGuardReportDoesNotRunTools(t *testing.T) {
	counted := &countingTool{}
	responses := repeatedToolResponses(loopGuardFailBlock+1, "bash", `{"command":"false"}`)
	responses = append(responses, toolResponse("call_report", "count_call", `{}`))
	h := newHarness(t, mock.Script{Responses: responses}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"bash", "count_call"}},
	})
	allowAllTools(h)
	if err := h.tools.Register(counted); err != nil {
		t.Fatal(err)
	}
	rec := &requestRecorder{inner: h.llm.providers["mock"]}
	h.llm.providers["mock"] = rec

	result := h.run(t, "hi")
	if result.StopReason != "loop" {
		t.Fatalf("result=%+v", result)
	}
	if got := counted.calls.Load(); got != 0 {
		t.Fatalf("report tool ran %d times, want 0", got)
	}
	payload := toolResultPayload(t, h, "call_report")
	if payload.Status != core.ToolStatusError || payload.Output != reportToolsDisabledText {
		t.Fatalf("report tool result=%+v", payload)
	}
	if reason := turnEndReason(t, h, h.info.ID); reason != "loop" {
		t.Fatalf("turn/end reason = %q, want loop", reason)
	}
	// The halt step (5) and the one report step: no request follows the report.
	if got, want := len(rec.requests), loopGuardFailBlock+2; got != want {
		t.Fatalf("requests=%d want %d", got, want)
	}
}

// TestTurnLoopGuardSameStepIdenticalCallsBlocked covers that one response with
// many identical calls runs at most the guard's repeat block threshold of
// them; the rest get the synthetic block result.
func TestTurnLoopGuardSameStepIdenticalCallsBlocked(t *testing.T) {
	const calls = 20
	events := make([]mock.Event, 0, calls+1)
	for i := 0; i < calls; i++ {
		events = append(events, mock.Event{ToolUse: &mock.ToolUse{
			ID: fmt.Sprintf("c%d", i), Name: "bash", Input: json.RawMessage(`{"command":"true"}`),
		}})
	}
	events = append(events, mock.Event{Stop: strptr("tool_use")})
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: events},
		textResponse("report"),
	}}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"bash"}},
	})
	allowAllTools(h)

	if result := h.run(t, "hi"); result.StopReason != "loop" {
		t.Fatalf("result=%+v", result)
	}
	executed, blocked := 0, 0
	for _, event := range h.events(t) {
		if event.Kind != core.KindToolResult {
			continue
		}
		var payload core.ToolResultPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Status == core.ToolStatusError && strings.HasPrefix(payload.Output, "Blocked bash") {
			blocked++
			continue
		}
		executed++
	}
	if executed > loopGuardStreakLen {
		t.Fatalf("executed %d identical calls, want at most %d", executed, loopGuardStreakLen)
	}
	if blocked != calls-executed {
		t.Fatalf("executed=%d blocked=%d want %d blocked", executed, blocked, calls-executed)
	}
}
