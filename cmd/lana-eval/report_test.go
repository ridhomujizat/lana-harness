package main

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// testEvent builds one session event for the metric tests.
func testEvent(t *testing.T, kind string, payload any) core.SessionEvent {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", kind, err)
	}
	return core.SessionEvent{Kind: kind, Payload: data}
}

func testUserMessage(text string, runtime bool) core.UserMessage {
	return core.UserMessage{
		V:       core.PayloadVersion,
		Runtime: runtime,
		Content: []core.ContentBlock{{Kind: core.BlockText, Text: text}},
	}
}

func testToolCall(t *testing.T, name, input string) core.SessionEvent {
	t.Helper()
	return testEvent(t, core.KindToolCall, core.ToolCallPayload{
		V:      core.PayloadVersion,
		CallID: name,
		Name:   name,
		Input:  json.RawMessage(input),
	})
}

func TestComputeMetrics(t *testing.T) {
	toolResult := func(callID string, status core.ToolStatus) core.SessionEvent {
		return testEvent(t, core.KindToolResult, core.ToolResultPayload{
			V: core.PayloadVersion, CallID: callID, Status: status, Output: "out",
		})
	}
	events := []core.SessionEvent{
		testEvent(t, core.KindStepEnd, core.StepEnd{
			V: core.PayloadVersion, Step: 1,
			Usage: core.Usage{InputTokens: 100, CachedInputTokens: 60, OutputTokens: 20},
		}),
		testEvent(t, core.KindToolCall, core.ToolCallPayload{
			V: core.PayloadVersion, CallID: "1", Name: "read_file", Input: json.RawMessage(`{"path":"a.txt"}`),
		}),
		testToolCall(t, "bash", `{"command":"cat a.txt"}`),
		testToolCall(t, "bash", `{"command":"grep foo a.txt"}`),
		testToolCall(t, "bash", `{"command":"echo hi > out.txt"}`),
		testToolCall(t, "bash", `{"command":"echo hi"}`),
		testToolCall(t, "bash", `{"command":"sed -i s/a/b/ a.txt"}`),
		testToolCall(t, "bash", `{"command":"python3 - <<'PY'\nprint(1)\nPY"}`),
		testToolCall(t, "bash", `{"command":"find . -name a.txt"}`),
		toolResult("1", core.ToolStatusSuccess),
		toolResult("bash", core.ToolStatusError),
		testEvent(t, core.KindToolResult, core.ToolResultPayload{
			V: core.PayloadVersion, CallID: "x", Status: core.ToolStatusRejected, Output: "no",
		}),
		testEvent(t, core.KindUserMessage, testUserMessage(reminderRepeatPrefix+" (3 times)", true)),
		testEvent(t, core.KindUserMessage, testUserMessage(reminderRepeatedPrefix, true)),
		testEvent(t, core.KindUserMessage, testUserMessage(reminderFailurePrefix+"4 tool calls failed.", true)),
		testEvent(t, core.KindUserMessage, testUserMessage("ordinary runtime note", true)),
		testEvent(t, core.KindUserMessage, testUserMessage(reminderRepeatPrefix, false)),
		testEvent(t, core.KindCompactionSummary, core.CompactionSummary{V: core.PayloadVersion, Summary: "summary"}),
		testEvent(t, core.KindStepEnd, core.StepEnd{
			V: core.PayloadVersion, Step: 2,
			Usage: core.Usage{InputTokens: 50, CachedInputTokens: 25, OutputTokens: 10},
		}),
		testEvent(t, core.KindTurnEnd, core.TurnEnd{V: core.PayloadVersion, Reason: "max_steps"}),
	}

	metrics, err := computeMetrics(events)
	if err != nil {
		t.Fatalf("computeMetrics: %v", err)
	}
	if metrics.Steps != 2 {
		t.Fatalf("steps = %d, want 2", metrics.Steps)
	}
	if metrics.ToolErrors != 1 {
		t.Fatalf("tool errors = %d, want 1", metrics.ToolErrors)
	}
	if metrics.BashFileOps != 6 {
		t.Fatalf("bash file ops = %d, want 6", metrics.BashFileOps)
	}
	if metrics.Reminders != 3 {
		t.Fatalf("reminders = %d, want 3", metrics.Reminders)
	}
	if metrics.Compactions != 1 {
		t.Fatalf("compactions = %d, want 1", metrics.Compactions)
	}
	if metrics.InputTokens != 150 || metrics.CachedTokens != 85 || metrics.OutputTokens != 30 {
		t.Fatalf("tokens = (%d, %d, %d), want (150, 85, 30)",
			metrics.InputTokens, metrics.CachedTokens, metrics.OutputTokens)
	}
	if math.Abs(metrics.CacheHitPct-(85.0*100/150.0)) > 1e-9 {
		t.Fatalf("cache hit = %f, want 56.67", metrics.CacheHitPct)
	}
	if metrics.TurnEnd != "max_steps" {
		t.Fatalf("turn end = %q, want max_steps", metrics.TurnEnd)
	}
	calls := metrics.ToolCalls
	if calls["read_file"] != 1 || calls["bash"] != 7 {
		t.Fatalf("tool calls = %v, want read_file:1 bash:7", calls)
	}
}

func TestBashFileOpHeuristic(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{command: "cat a.txt", want: true},
		{command: "  head -n 5 a.txt", want: true},
		{command: "tail -f log", want: true},
		{command: "sed -i s/a/b/ a.txt", want: true},
		{command: "awk '{print}' a.txt", want: true},
		{command: "grep foo a.txt", want: true},
		{command: "find . -name '*.go'", want: true},
		{command: "printf 'x' > a.txt", want: true},
		{command: "echo hi > a.txt", want: true},
		{command: "echo hi", want: false},
		{command: "python3 -c 'print(1)'", want: false},
		{command: "python3 - <<'PY'\nprint(1)\nPY", want: true},
		{command: "ls -la", want: false},
		{command: "", want: false},
	}
	for _, tt := range tests {
		if got := isBashFileOp(tt.command); got != tt.want {
			t.Errorf("isBashFileOp(%q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

func TestCompareDeltaFormatting(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "int up", got: formatIntDelta(10, 8, true), want: "10 (+2)"},
		{name: "int down", got: formatIntDelta(10, 12, true), want: "10 (-2)"},
		{name: "int no compare", got: formatIntDelta(10, 0, false), want: "10"},
		{name: "int64 up", got: formatInt64Delta(123456, 120000, true), want: "123456 (+3456)"},
		{name: "duration up", got: formatDurationDelta(1500, 1200, true), want: "1.5s (+300ms)"},
		{name: "duration zero", got: formatDurationDelta(0, 0, true), want: "0s (+0s)"},
		{name: "percent up", got: formatPctDelta(56.66, 50, true), want: "56.7% (+6.7pp)"},
		{name: "percent no compare", got: formatPctDelta(50, 0, false), want: "50.0%"},
		{name: "cost up", got: formatCostDelta(0.0012, 0.0010, true), want: "$0.001200 (+$0.000200)"},
		{name: "cost down", got: formatCostDelta(0.0010, 0.0012, true), want: "$0.001000 (-$0.000200)"},
		{name: "tool calls", got: formatToolCalls(map[string]int{"read_file": 1, "bash": 2}), want: "bash:2 read_file:1"},
		{name: "no tool calls", got: formatToolCalls(nil), want: "-"},
		{name: "tail", got: tailText("0123456789", 4), want: "6789"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestBuildTotal(t *testing.T) {
	prices := priceTable{in: 2, cached: 0.5, out: 4, set: true}
	tasks := []taskResult{
		{
			Name: "one", Status: statusPass, DurationMS: 1000, Steps: 2,
			ToolCalls: map[string]int{"read_file": 1}, InputTokens: 1000,
			CachedTokens: 400, OutputTokens: 100,
		},
		{
			Name: "two", Status: statusFail, DurationMS: 2000, Steps: 3,
			ToolCalls: map[string]int{"bash": 2}, ToolErrors: 1,
			InputTokens: 2000, CachedTokens: 0, OutputTokens: 200,
		},
	}
	total := buildTotal(tasks, prices)
	if total.Steps != 5 || total.DurationMS != 3000 || total.ToolErrors != 1 {
		t.Fatalf("total = %+v", total)
	}
	if total.ToolCalls["read_file"] != 1 || total.ToolCalls["bash"] != 2 {
		t.Fatalf("total tool calls = %v", total.ToolCalls)
	}
	if total.CacheHitPct < 13.3 || total.CacheHitPct > 13.4 {
		t.Fatalf("total cache hit = %f, want 13.33", total.CacheHitPct)
	}
	// 2600 uncached input at $2/M + 400 cached at $0.5/M + 300 output at $4/M.
	wantCost := (2600*2.0 + 400*0.5 + 300*4.0) / 1e6
	if total.CostUSD == nil || math.Abs(*total.CostUSD-wantCost) > 1e-12 {
		t.Fatalf("total cost = %v, want %f", total.CostUSD, wantCost)
	}
	if total.Status != "1 pass, 1 fail, 0 error" {
		t.Fatalf("total status = %q", total.Status)
	}
}

func TestPrintTableCompare(t *testing.T) {
	cost, previousCost := 0.001, 0.0005
	tasks := []taskResult{{
		Name: "task", Status: statusPass, DurationMS: 1500, Steps: 2,
		ToolCalls: map[string]int{"bash": 1}, InputTokens: 100,
		CachedTokens: 50, CacheHitPct: 50, OutputTokens: 10,
		TurnEnd: "end_turn", CostUSD: &cost,
	}}
	prices := priceTable{in: 1, cached: 1, out: 1, set: true}
	total := buildTotal(tasks, prices)
	previous := &report{
		Tasks: []taskResult{{
			Name: "task", Status: statusPass, DurationMS: 1000, Steps: 1,
			ToolCalls: map[string]int{"bash": 1}, InputTokens: 50,
			CachedTokens: 30, CacheHitPct: 30, OutputTokens: 5,
			TurnEnd: "end_turn", CostUSD: &previousCost,
		}},
	}
	var buffer bytes.Buffer
	printTable(&buffer, tasks, total, previous, true)
	output := buffer.String()
	for _, want := range []string{
		"1.5s (+500ms)",
		"2 (+1)",
		"100 (+50)",
		"50 (+20)",
		"50.0% (+20.0pp)",
		"10 (+5)",
		"$0.001000 (+$0.000500)",
		"1 pass, 0 fail, 0 error",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("table does not contain %q:\n%s", want, output)
		}
	}
}
