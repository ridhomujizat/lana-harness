package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/stats"
)

// recordingUsageStatsSource records the dashboard calls and returns canned data.
type recordingUsageStatsSource struct {
	dashboard stats.Dashboard
	err       error
	calls     []recordedUsageStatsCall
}

type recordedUsageStatsCall struct {
	rangeName string
	agent     string
}

func (f *recordingUsageStatsSource) Dashboard(_ context.Context, rangeName, agent string) (stats.Dashboard, error) {
	f.calls = append(f.calls, recordedUsageStatsCall{rangeName: rangeName, agent: agent})
	return f.dashboard, f.err
}

func invokeUsageStatsTool(t *testing.T, src UsageStatsSource, tc core.ToolCtx, input string) core.ToolOutput {
	t.Helper()
	out, err := NewUsageStats(src).Call(context.Background(), tc, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The default range is 24h and the default agent is the caller.
func TestUsageStatsToolDefaults(t *testing.T) {
	src := &recordingUsageStatsSource{dashboard: stats.Dashboard{Summary: stats.Summary{Turns: 3}}}
	out := invokeUsageStatsTool(t, src, core.ToolCtx{AgentName: "lana"}, `{}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if len(src.calls) != 1 {
		t.Fatalf("calls=%d want 1", len(src.calls))
	}
	if got := src.calls[0]; got.rangeName != "24h" || got.agent != "lana" {
		t.Fatalf("call=%+v want range 24h and agent lana", got)
	}
	for _, want := range []string{"range=24h", "agent=lana", "turns=3"} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("output lacks %q:\n%s", want, out.Output)
		}
	}
}

// "all" asks the source for every agent, i.e. an empty filter.
func TestUsageStatsToolAllAgents(t *testing.T) {
	src := &recordingUsageStatsSource{}
	out := invokeUsageStatsTool(t, src, core.ToolCtx{AgentName: "lana"}, `{"range":"7d","agent":"all"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if len(src.calls) != 1 {
		t.Fatalf("calls=%d want 1", len(src.calls))
	}
	if got := src.calls[0]; got.rangeName != "7d" || got.agent != "" {
		t.Fatalf("call=%+v want range 7d and an empty agent", got)
	}
	if !strings.Contains(out.Output, "agent=all") {
		t.Fatalf("output=%q want agent=all", out.Output)
	}
}

// An explicit agent overrides the caller.
func TestUsageStatsToolExplicitAgent(t *testing.T) {
	src := &recordingUsageStatsSource{}
	invokeUsageStatsTool(t, src, core.ToolCtx{AgentName: "lana"}, `{"agent":"coder"}`)
	if len(src.calls) != 1 || src.calls[0].agent != "coder" {
		t.Fatalf("calls=%+v want agent coder", src.calls)
	}
}

// An invalid range is rejected before the source is called.
func TestUsageStatsToolInvalidRange(t *testing.T) {
	src := &recordingUsageStatsSource{}
	out := invokeUsageStatsTool(t, src, core.ToolCtx{AgentName: "lana"}, `{"range":"1h"}`)
	if out.Status != core.ToolStatusError {
		t.Fatalf("out=%+v want an error", out)
	}
	if !strings.Contains(out.Output, "24h, 7d, 30d") {
		t.Fatalf("output=%q lacks the valid ranges", out.Output)
	}
	if len(src.calls) != 0 {
		t.Fatalf("source called %d times, want 0", len(src.calls))
	}
}

// The output carries the summary, model rows, top tools, and recent errors,
// and a long message is cut to 300 characters.
func TestUsageStatsToolOutput(t *testing.T) {
	at := time.Date(2026, 1, 2, 15, 4, 0, 0, time.Local).UnixMilli()
	long := strings.Repeat("x", 400)
	src := &recordingUsageStatsSource{dashboard: stats.Dashboard{
		Summary: stats.Summary{
			InputTokens:       1000,
			CachedInputTokens: 200,
			OutputTokens:      300,
			Turns:             5,
			TurnsFailed:       2,
			TurnsCancelled:    1,
			ToolCalls:         9,
			ToolErrors:        2,
			ToolRejected:      1,
			ProviderRetries:   3,
		},
		ByModel: []stats.ModelRow{{
			Provider: "openai", Model: "gpt-5", Requests: 5,
			InputTokens: 900, CachedInputTokens: 180, OutputTokens: 270,
		}},
		Tools: []stats.ToolRow{
			{Name: "read_file", Calls: 2},
			{Name: "bash", Calls: 4, Errors: 1},
		},
		Errors: []stats.ErrorRow{{
			At: at, Source: "tool", Level: "error", Tool: "bash", Message: long,
		}},
	}}
	out := invokeUsageStatsTool(t, src, core.ToolCtx{AgentName: "lana"}, `{}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	wantTime := time.UnixMilli(at).Local().Format("2006-01-02T15:04Z07:00")
	for _, want := range []string{
		"turns=5", "turns_failed=2", "turns_cancelled=1",
		"input_tokens=1000", "cached_input_tokens=200", "output_tokens=300",
		"tool_calls=9", "tool_errors=2", "tool_rejected=1", "provider_retries=3",
		"openai/gpt-5", "read_file", "bash", "calls=4", "errors=1",
		wantTime, "[tool/error]", strings.Repeat("x", 300),
	} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("output lacks %q:\n%s", want, out.Output)
		}
	}
	if strings.Contains(out.Output, long) {
		t.Fatalf("output carries the untruncated message:\n%s", out.Output)
	}
	if strings.Contains(out.Output, strings.Repeat("x", 301)) {
		t.Fatalf("output carries more than 300 message characters:\n%s", out.Output)
	}
}

// The tool table keeps the ten busiest tools and the error list the ten newest.
func TestUsageStatsToolLimits(t *testing.T) {
	toolRows := make([]stats.ToolRow, 0, 12)
	for i := 0; i < 12; i++ {
		toolRows = append(toolRows, stats.ToolRow{Name: string(rune('a' + i)), Calls: int64(i)})
	}
	errorRows := make([]stats.ErrorRow, 0, 12)
	for i := 0; i < 12; i++ {
		errorRows = append(errorRows, stats.ErrorRow{At: int64(i), Message: string(rune('a' + i))})
	}
	src := &recordingUsageStatsSource{dashboard: stats.Dashboard{Tools: toolRows, Errors: errorRows}}
	out := invokeUsageStatsTool(t, src, core.ToolCtx{}, `{}`)
	for _, want := range []string{"l: calls=11", "c: calls=2"} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("output lacks %q:\n%s", want, out.Output)
		}
	}
	if strings.Contains(out.Output, "b: calls=1") {
		t.Fatalf("output has an eleventh tool:\n%s", out.Output)
	}
	for _, want := range []string{"[unknown/error] l", "[unknown/error] c"} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("output lacks %q:\n%s", want, out.Output)
		}
	}
	if strings.Contains(out.Output, "[unknown/error] b") {
		t.Fatalf("output has an eleventh error:\n%s", out.Output)
	}
}

// A dashboard error becomes a tool error.
func TestUsageStatsToolSourceError(t *testing.T) {
	src := &recordingUsageStatsSource{err: errors.New("scan failed")}
	out := invokeUsageStatsTool(t, src, core.ToolCtx{}, `{}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "scan failed") {
		t.Fatalf("out=%+v", out)
	}
}
