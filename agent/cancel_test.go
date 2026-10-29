package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

func TestCancelBashProcess(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{ToolUse: &mock.ToolUse{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"sleep 5"}`)}},
			{Stop: strptr("tool_use")},
		}},
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"bash"}}})
	h.hooks.ToolsPreExecute.Use(func(ctx context.Context, in core.ToolsPreExecuteInput, next core.Next[core.ToolsPreExecuteInput, core.ToolDecision]) (core.ToolDecision, error) {
		return core.ToolDecision{Allowed: true}, nil
	})
	turnID, err := h.reg.Start(context.Background(), h.info.ID, "hi")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return hasEvent(h, core.KindToolCall) })
	start := time.Now()
	if err := h.reg.Cancel(context.Background(), h.info.ID); err != nil {
		t.Fatal(err)
	}
	result, err := h.reg.Wait(context.Background(), turnID)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancel took too long: %v", elapsed)
	}
	if result.StopReason != "cancelled" {
		t.Fatalf("result=%+v", result)
	}
	found := false
	for _, event := range h.events(t) {
		if event.Kind == core.KindToolResult {
			var payload core.ToolResultPayload
			_ = json.Unmarshal(event.Payload, &payload)
			if payload.CallID == "c1" && payload.Status == core.ToolStatusError && payload.Output == "cancelled" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("cancelled tool/result not found")
	}
}

func hasEvent(h *harness, kind string) bool {
	events, err := h.store.ChainEvents(context.Background(), h.info.ID)
	if err != nil {
		return false
	}
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestCancelIdleNoop(t *testing.T) {
	h := newHarness(t, mock.Script{}, nil)
	before := len(h.events(t))
	if err := h.reg.Cancel(context.Background(), h.info.ID); err != nil {
		t.Fatal(err)
	}
	if after := len(h.events(t)); after != before {
		t.Fatalf("events increased: %d -> %d", before, after)
	}
}

func TestCancelKeepsInbox(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{ToolUse: &mock.ToolUse{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"sleep 5"}`)}},
			{Stop: strptr("tool_use")},
		}},
		textResponse("after cancel"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"bash"}}})
	h.hooks.ToolsPreExecute.Use(func(ctx context.Context, in core.ToolsPreExecuteInput, next core.Next[core.ToolsPreExecuteInput, core.ToolDecision]) (core.ToolDecision, error) {
		return core.ToolDecision{Allowed: true}, nil
	})
	turnID, err := h.reg.Start(context.Background(), h.info.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return hasEvent(h, core.KindToolCall) })
	if err := h.reg.Send(context.Background(), h.info.ID, "second"); err != nil {
		t.Fatal(err)
	}
	if err := h.reg.Cancel(context.Background(), h.info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reg.Wait(context.Background(), turnID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		count := 0
		for _, event := range h.events(t) {
			if event.Kind == core.KindUserMessage {
				count++
			}
		}
		return count >= 2
	})
}

func TestLiveFrameOrder(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{{Events: []mock.Event{
		{Text: strptr("one")}, {Text: strptr("two")}, {Text: strptr("three")}, {Stop: strptr("end_turn")},
	}}}}, nil)
	sub, err := h.reg.SubscribeLive(h.info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	h.run(t, "hi")
	var frames []core.LiveFrame
	for i := 0; i < 3; i++ {
		received, err := sub.Recv(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, received.Value)
	}
	if len(frames) != 3 || frames[0].Text != "one" || frames[2].Text != "three" {
		t.Fatalf("frames=%+v", frames)
	}
	for _, frame := range frames {
		if frame.Kind != "text_delta" {
			t.Fatalf("kind=%q", frame.Kind)
		}
	}
}

func TestLiveFrameDropWarn(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	hub := NewHub()
	sub := hub.Subscribe("s")
	defer sub.Close()
	for i := 0; i < 1500; i++ {
		hub.Publish("s", core.LiveFrame{Kind: "text_delta", Text: "x"})
	}
	if !strings.Contains(buf.String(), "live frame dropped") {
		t.Fatalf("warning not found: %s", buf.String())
	}
}
