package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

func TestToolResultPerCall(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{ToolUse: &mock.ToolUse{ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a.txt"}`)}},
			{ToolUse: &mock.ToolUse{ID: "c2", Name: "write_file", Input: json.RawMessage(`{"path":"b.txt","content":"x"}`)}},
			{ToolUse: &mock.ToolUse{ID: "c3", Name: "read_file", Input: json.RawMessage(`{"path":"missing.txt"}`)}},
			{Stop: strptr("tool_use")},
		}},
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file", "write_file"}}})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	results := map[string]core.ToolResultPayload{}
	for _, event := range h.events(t) {
		if event.Kind != core.KindToolResult {
			continue
		}
		var payload core.ToolResultPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		results[payload.CallID] = payload
	}
	if results["c1"].Status != core.ToolStatusSuccess {
		t.Fatalf("c1=%+v", results["c1"])
	}
	if results["c2"].Status != core.ToolStatusRejected {
		t.Fatalf("c2=%+v", results["c2"])
	}
	if results["c3"].Status != core.ToolStatusError || !strings.Contains(results["c3"].Output, "not found") {
		t.Fatalf("c3=%+v", results["c3"])
	}
}

func TestToolNotEnabled(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{ToolUse: &mock.ToolUse{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"pwd"}`)}}, {Stop: strptr("tool_use")}}},
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	h.run(t, "hi")
	found := false
	for _, event := range h.events(t) {
		if event.Kind == core.KindToolResult {
			var payload core.ToolResultPayload
			_ = json.Unmarshal(event.Payload, &payload)
			if payload.CallID == "c1" && payload.Status == core.ToolStatusError && strings.Contains(payload.Output, "tool is not enabled") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("tool/result not-enabled error not found")
	}
}

type slowTool struct {
	name   string
	risk   core.Risk
	delay  time.Duration
	active *atomic.Int32
	max    *atomic.Int32
}

func (s *slowTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: s.name, InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
}
func (s *slowTool) Risk(json.RawMessage) core.Risk { return s.risk }
func (s *slowTool) Call(ctx context.Context, tc core.ToolCtx, input json.RawMessage) (core.ToolOutput, error) {
	current := s.active.Add(1)
	for {
		old := s.max.Load()
		if current <= old || s.max.CompareAndSwap(old, current) {
			break
		}
	}
	time.Sleep(s.delay)
	s.active.Add(-1)
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "ok"}, nil
}

func TestReadBatchParallel(t *testing.T) {
	var active, max atomic.Int32
	h := newHarness(t, mock.Script{}, nil)
	tool := &slowTool{name: "slow_read", risk: core.RiskRead, delay: 80 * time.Millisecond, active: &active, max: &max}
	if err := h.tools.Register(tool); err != nil {
		t.Fatal(err)
	}
	h.llm.providers["mock"] = mock.New(mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{ToolUse: &mock.ToolUse{ID: "c1", Name: "slow_read", Input: json.RawMessage(`{}`)}},
			{ToolUse: &mock.ToolUse{ID: "c2", Name: "slow_read", Input: json.RawMessage(`{}`)}},
			{ToolUse: &mock.ToolUse{ID: "c3", Name: "slow_read", Input: json.RawMessage(`{}`)}},
			{Stop: strptr("tool_use")},
		}},
		textResponse("done"),
	}})
	h.reg.config.Agents["assistant"] = core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"slow_read"}}
	start := time.Now()
	h.run(t, "hi")
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("Read not parallel: %v", elapsed)
	}
	if max.Load() < 2 {
		t.Fatalf("max concurrency=%d", max.Load())
	}
}

func TestWriteSerial(t *testing.T) {
	var active, max atomic.Int32
	h := newHarness(t, mock.Script{}, nil)
	tool := &slowTool{name: "slow_write", risk: core.RiskWrite, delay: 50 * time.Millisecond, active: &active, max: &max}
	if err := h.tools.Register(tool); err != nil {
		t.Fatal(err)
	}
	h.llm.providers["mock"] = mock.New(mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{ToolUse: &mock.ToolUse{ID: "c1", Name: "slow_write", Input: json.RawMessage(`{}`)}},
			{ToolUse: &mock.ToolUse{ID: "c2", Name: "slow_write", Input: json.RawMessage(`{}`)}},
			{ToolUse: &mock.ToolUse{ID: "c3", Name: "slow_write", Input: json.RawMessage(`{}`)}},
			{Stop: strptr("tool_use")},
		}},
		textResponse("done"),
	}})
	h.reg.config.Agents["assistant"] = core.AgentConfig{Provider: "mock", Model: "m", Tools: []string{"slow_write"}}
	h.hooks.ToolsPreExecute.SetDefault(func(context.Context, core.ToolsPreExecuteInput) (core.ToolDecision, error) {
		return core.ToolDecision{Allowed: true}, nil
	})
	start := time.Now()
	h.run(t, "hi")
	if elapsed := time.Since(start); elapsed < 140*time.Millisecond {
		t.Fatalf("Write ran in parallel: %v", elapsed)
	}
	if max.Load() != 1 {
		t.Fatalf("max concurrency=%d", max.Load())
	}
}

func TestInvalidToolInput(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{ToolUse: &mock.ToolUse{ID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)}}, {Stop: strptr("tool_use")}}},
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	h.run(t, "hi")
	for _, event := range h.events(t) {
		if event.Kind != core.KindToolResult {
			continue
		}
		var payload core.ToolResultPayload
		_ = json.Unmarshal(event.Payload, &payload)
		if payload.CallID == "c1" && payload.Status == core.ToolStatusError && strings.Contains(payload.Output, "invalid input") {
			return
		}
	}
	t.Fatal("tool/result invalid input error not found")
}

func TestSystemMessageOnChange(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("a"), textResponse("b"), textResponse("c")}}, nil)
	prompt := filepath.Join(h.home, "prompt.md")
	if err := os.WriteFile(prompt, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.reg.config.Agents["assistant"] = core.AgentConfig{Provider: "mock", Model: "m", PromptFile: "prompt.md"}
	countSystems := func() int {
		count := 0
		for _, event := range h.events(t) {
			if event.Kind == core.KindSystemMessage {
				count++
			}
		}
		return count
	}
	h.run(t, "1")
	h.run(t, "2")
	if got := countSystems(); got != 1 {
		t.Fatalf("system=%d want 1", got)
	}
	if err := os.WriteFile(prompt, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.run(t, "3")
	// REQ-LTC-002: the head stays stable; a later change is a <system-update>.
	if got := countSystems(); got != 1 {
		t.Fatalf("system=%d want 1", got)
	}
	if update := userText(h.lastUserMessage(t)); !strings.Contains(update, "<system-update>") || !strings.Contains(update, "two") {
		t.Fatalf("prompt change must go out as a <system-update>:\n%s", update)
	}
}

func TestSessionsParallel(t *testing.T) {
	delay := 80
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{DelayMS: &delay}, {Text: strptr("a")}, {Stop: strptr("end_turn")}}},
		{Events: []mock.Event{{DelayMS: &delay}, {Text: strptr("b")}, {Stop: strptr("end_turn")}}},
	}}, nil)
	second, err := h.store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "library", Workspace: h.ws})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	id1, err := h.reg.Start(context.Background(), h.info.ID, "one")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := h.reg.Start(context.Background(), second.ID, "two")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.reg.Wait(context.Background(), id1); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reg.Wait(context.Background(), id2); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("session not parallel: %v", elapsed)
	}
}

func TestSendDuringClose(t *testing.T) {
	delay := 50
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{DelayMS: &delay}, {Text: strptr("a")}, {Stop: strptr("end_turn")}}},
		textResponse("b"),
	}}, nil)
	turnID, err := h.reg.Start(context.Background(), h.info.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := h.reg.Send(context.Background(), h.info.ID, "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reg.Wait(context.Background(), turnID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	var users int
	for _, event := range h.events(t) {
		if event.Kind == core.KindUserMessage {
			users++
		}
	}
	if users != 2 {
		t.Fatalf("users=%d", users)
	}
}
