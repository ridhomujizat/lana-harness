package stats

import (
	"encoding/json"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/logring"
)

func event(t *testing.T, kind, session, agent, turnID string, at int64, payload any) core.ScannedEvent {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", kind, err)
	}
	return core.ScannedEvent{
		SessionEvent: core.SessionEvent{SessionID: session, Seq: 1, Kind: kind, Payload: raw, TurnID: turnID, CreatedAt: at},
		Agent:        agent,
	}
}

// TestAggregateCoversACDSH001 covers AC-DSH-001: the totals, buckets,
// by-model, by-agent, by-source, tools (avg and p95), turn reasons, and error
// rows of a fixture event set.
func TestAggregateCoversACDSH001(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r, err := ParseRange("24h", now)
	if err != nil {
		t.Fatal(err)
	}
	from := r.From
	min := int64(time.Minute / time.Millisecond)
	hour := int64(time.Hour / time.Millisecond)

	events := []core.ScannedEvent{
		event(t, core.KindTurnStart, "s1", "assistant", "t1", from+10*min, core.TurnStart{V: 1, TurnID: "t1"}),
		event(t, core.KindStepEnd, "s1", "assistant", "t1", from+20*min, core.StepEnd{V: 1, Provider: "deepseek", Model: "deepseek-chat", Usage: core.Usage{InputTokens: 100, CachedInputTokens: 40, OutputTokens: 10}}),
		event(t, core.KindStepEnd, "s1", "assistant", "t1", from+25*min, core.StepEnd{V: 1, Provider: "deepseek", Model: "deepseek-chat", Usage: core.Usage{InputTokens: 50, OutputTokens: 5}}),
		event(t, core.KindToolCall, "s1", "assistant", "t1", from+30*min, core.ToolCallPayload{V: 1, CallID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)}),
		event(t, core.KindToolResult, "s1", "assistant", "t1", from+30*min+35, core.ToolResultPayload{V: 1, CallID: "c1", Status: core.ToolStatusSuccess, Output: "ok"}),
		event(t, core.KindToolCall, "s1", "assistant", "t1", from+40*min, core.ToolCallPayload{V: 1, CallID: "c2", Name: "bash"}),
		event(t, core.KindToolResult, "s1", "assistant", "t1", from+40*min+100, core.ToolResultPayload{V: 1, CallID: "c2", Status: core.ToolStatusError, Output: "boom: not found"}),
		event(t, core.KindToolCall, "s1", "assistant", "t1", from+50*min, core.ToolCallPayload{V: 1, CallID: "c3", Name: "bash"}),
		event(t, core.KindToolResult, "s1", "assistant", "t1", from+50*min+200, core.ToolResultPayload{V: 1, CallID: "c3", Status: core.ToolStatusError, Output: "second error"}),
		event(t, core.KindToolCall, "s1", "assistant", "t1", from+55*min, core.ToolCallPayload{V: 1, CallID: "c4", Name: "bash"}),
		event(t, core.KindToolResult, "s1", "assistant", "t1", from+55*min+300, core.ToolResultPayload{V: 1, CallID: "c4", Status: core.ToolStatusRejected, Output: "denied"}),
		event(t, core.KindTurnEnd, "s1", "assistant", "t1", from+hour+5*min, core.TurnEnd{V: 1, TurnID: "t1", Reason: "end_turn"}),
		event(t, core.KindTurnStart, "s1", "assistant", "t2", from+hour+10*min, core.TurnStart{V: 1, TurnID: "t2"}),
		event(t, core.KindTurnEnd, "s1", "assistant", "t2", from+hour+20*min, core.TurnEnd{V: 1, TurnID: "t2"}),
		event(t, core.KindTurnStart, "s1", "assistant", "t3", from+2*hour, core.TurnStart{V: 1, TurnID: "t3"}),
		event(t, core.KindTurnEnd, "s1", "assistant", "t3", from+2*hour+10*min, core.TurnEnd{V: 1, TurnID: "t3", Reason: "cancelled"}),
		event(t, core.KindTurnStart, "s1", "assistant", "t4", from+3*hour, core.TurnStart{V: 1, TurnID: "t4"}),
		event(t, core.KindTurnEnd, "s1", "assistant", "t4", from+3*hour+10*min, core.TurnEnd{V: 1, TurnID: "t4", Reason: "max_steps"}),
		event(t, core.KindCompactionSummary, "s1", "assistant", "", from+4*hour, core.CompactionSummary{V: 1, Usage: core.Usage{InputTokens: 10, OutputTokens: 2}}),
		event(t, core.KindMemoryCaptured, "s1", "assistant", "", from+5*hour, core.MemoryCaptured{V: 1, Usage: core.Usage{InputTokens: 20, CachedInputTokens: 5, OutputTokens: 1}}),
		event(t, core.KindAssistantAttempt, "s1", "assistant", "t5", from+6*hour, core.AssistantAttempt{V: 1, Provider: "deepseek", Model: "deepseek-chat", Error: core.PayloadError{Class: "rate_limit", Status: 429, Message: "too many"}}),
	}
	logs := []logring.Record{
		{At: from + 7*hour, Level: slog.LevelError, Message: "boom"},
		{At: from + 6*hour + 30*min, Level: slog.LevelWarn, Message: "mcp server exited", Attrs: "name=github err=exit status 1"},
	}

	d := Aggregate(r, "", events, logs)

	if d.From != r.From || d.To != r.To || d.BucketMS != r.BucketMS {
		t.Fatalf("range = %+v, want %+v", d, r)
	}

	wantSummary := Summary{
		InputTokens: 180, CachedInputTokens: 45, OutputTokens: 18,
		Turns: 4, TurnsFailed: 1, TurnsCancelled: 1,
		ToolCalls: 4, ToolErrors: 2, ToolRejected: 1, ProviderRetries: 1,
	}
	if d.Summary != wantSummary {
		t.Fatalf("summary = %+v, want %+v", d.Summary, wantSummary)
	}

	// 24h with one-hour buckets, aligned in UTC, is 25 zero-filled entries.
	if len(d.Series) != 25 {
		t.Fatalf("series = %d entries, want 25", len(d.Series))
	}
	if d.Series[0].Start != from {
		t.Fatalf("series[0].start = %d, want %d", d.Series[0].Start, from)
	}
	if got := d.Series[0]; got.InputTokens != 150 || got.CachedInputTokens != 40 || got.OutputTokens != 15 {
		t.Fatalf("series[0] = %+v", got)
	}
	if got := d.Series[4]; got.InputTokens != 10 || got.OutputTokens != 2 {
		t.Fatalf("series[4] = %+v", got)
	}
	if got := d.Series[5]; got.InputTokens != 20 || got.CachedInputTokens != 5 || got.OutputTokens != 1 {
		t.Fatalf("series[5] = %+v", got)
	}
	if got := d.Series[1]; got.InputTokens != 0 || got.CachedInputTokens != 0 || got.OutputTokens != 0 {
		t.Fatalf("series[1] = %+v, want zeroed", got)
	}

	wantModels := []ModelRow{{Provider: "deepseek", Model: "deepseek-chat", Requests: 2, InputTokens: 150, CachedInputTokens: 40, OutputTokens: 15}}
	if !reflect.DeepEqual(d.ByModel, wantModels) {
		t.Fatalf("by_model = %+v, want %+v", d.ByModel, wantModels)
	}
	wantAgents := []AgentRow{{Agent: "assistant", Turns: 4, InputTokens: 150, CachedInputTokens: 40, OutputTokens: 15}}
	if !reflect.DeepEqual(d.ByAgent, wantAgents) {
		t.Fatalf("by_agent = %+v, want %+v", d.ByAgent, wantAgents)
	}
	wantSources := []SourceRow{
		{Source: "turn", Count: 2, InputTokens: 150, CachedInputTokens: 40, OutputTokens: 15},
		{Source: "compaction", Count: 1, InputTokens: 10, OutputTokens: 2},
		{Source: "memory_capture", Count: 1, InputTokens: 20, CachedInputTokens: 5, OutputTokens: 1},
	}
	if !reflect.DeepEqual(d.BySource, wantSources) {
		t.Fatalf("by_source = %+v, want %+v", d.BySource, wantSources)
	}

	wantTools := []ToolRow{
		{Name: "bash", Calls: 3, Errors: 2, Rejected: 1, AvgMS: 200, P95MS: 300, LastError: "second error", LastErrorAt: from + 50*min + 200},
		{Name: "read_file", Calls: 1, AvgMS: 35, P95MS: 35},
	}
	if !reflect.DeepEqual(d.Tools, wantTools) {
		t.Fatalf("tools = %+v, want %+v", d.Tools, wantTools)
	}

	wantReasons := []ReasonRow{{Reason: "end_turn", Count: 2}, {Reason: "cancelled", Count: 1}, {Reason: "max_steps", Count: 1}}
	if !reflect.DeepEqual(d.TurnReasons, wantReasons) {
		t.Fatalf("turn_reasons = %+v, want %+v", d.TurnReasons, wantReasons)
	}
	if d.TurnDuration.AvgMS != 1275000 || d.TurnDuration.P95MS != 3300000 {
		t.Fatalf("turn_duration = %+v", d.TurnDuration)
	}

	if len(d.Errors) != 6 {
		t.Fatalf("errors = %d rows, want 6: %+v", len(d.Errors), d.Errors)
	}
	if got := d.Errors[0]; got.Source != "daemon" || got.Level != "error" || got.Message != "boom" {
		t.Fatalf("errors[0] = %+v", got)
	}
	if got := d.Errors[1]; got.Source != "daemon" || got.Level != "warn" || got.Message != "mcp server exited name=github err=exit status 1" {
		t.Fatalf("errors[1] = %+v", got)
	}
	if got := d.Errors[2]; got.Source != "provider" || got.Message != "rate_limit: 429: too many" || got.Agent != "assistant" || got.SessionID != "s1" {
		t.Fatalf("errors[2] = %+v", got)
	}
	if got := d.Errors[3]; got.Source != "turn" || got.Message != "max_steps" || got.TurnID != "t4" {
		t.Fatalf("errors[3] = %+v", got)
	}
	if got := d.Errors[4]; got.Source != "tool" || got.Tool != "bash" || got.Message != "bash: second error" {
		t.Fatalf("errors[4] = %+v", got)
	}
	if !reflect.DeepEqual(d.Agents, []string{"assistant"}) {
		t.Fatalf("agents = %+v, want [assistant]", d.Agents)
	}
}

// TestAggregateAgentFilterCoversACDSH007 covers AC-DSH-007 and REQ-DSH-011: an
// agent filter counts only that agent's events, drops daemon rows, and still
// lists every agent in agents, in both modes.
func TestAggregateAgentFilterCoversACDSH007(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r, err := ParseRange("24h", now)
	if err != nil {
		t.Fatal(err)
	}
	from := r.From
	min := int64(time.Minute / time.Millisecond)
	toolErrorAt := from + 20*min + 35
	events := []core.ScannedEvent{
		// A scanned event with no agent must not appear in Agents.
		event(t, core.KindStepStart, "s0", "", "", from, core.StepStart{V: 1}),
		event(t, core.KindStepEnd, "s1", "assistant", "t1", from+10*min, core.StepEnd{V: 1, Provider: "deepseek", Model: "deepseek-chat", Usage: core.Usage{InputTokens: 100, CachedInputTokens: 40, OutputTokens: 10}}),
		event(t, core.KindToolCall, "s1", "assistant", "t1", from+20*min, core.ToolCallPayload{V: 1, CallID: "c1", Name: "read_file"}),
		event(t, core.KindToolResult, "s1", "assistant", "t1", toolErrorAt, core.ToolResultPayload{V: 1, CallID: "c1", Status: core.ToolStatusError, Output: "boom"}),
		event(t, core.KindTurnEnd, "s1", "assistant", "t1", from+30*min, core.TurnEnd{V: 1, TurnID: "t1", Reason: "end_turn"}),
		event(t, core.KindStepEnd, "s2", "lana", "t2", from+70*min, core.StepEnd{V: 1, Provider: "openai", Model: "gpt-5", Usage: core.Usage{InputTokens: 50, OutputTokens: 5}}),
		event(t, core.KindToolCall, "s2", "lana", "t2", from+80*min, core.ToolCallPayload{V: 1, CallID: "c2", Name: "bash"}),
		event(t, core.KindToolResult, "s2", "lana", "t2", from+80*min+100, core.ToolResultPayload{V: 1, CallID: "c2", Status: core.ToolStatusSuccess, Output: "ok"}),
		event(t, core.KindTurnEnd, "s2", "lana", "t2", from+90*min, core.TurnEnd{V: 1, TurnID: "t2", Reason: "max_steps"}),
	}
	logs := []logring.Record{{At: from + 2*int64(time.Hour/time.Millisecond), Level: slog.LevelWarn, Message: "mcp server exited"}}
	wantAgents := []string{"assistant", "lana"}

	all := Aggregate(r, "", events, logs)
	if all.Agent != "" {
		t.Fatalf("agent = %q, want empty", all.Agent)
	}
	if !reflect.DeepEqual(all.Agents, wantAgents) {
		t.Fatalf("agents = %+v, want %+v", all.Agents, wantAgents)
	}
	if all.Summary.Turns != 2 || all.Summary.ToolCalls != 2 || all.Summary.ToolErrors != 1 {
		t.Fatalf("unfiltered summary = %+v", all.Summary)
	}
	if len(all.Errors) != 3 {
		t.Fatalf("unfiltered errors = %d rows, want 3: %+v", len(all.Errors), all.Errors)
	}

	assistant := Aggregate(r, "assistant", events, logs)
	if assistant.Agent != "assistant" {
		t.Fatalf("agent = %q, want assistant", assistant.Agent)
	}
	if !reflect.DeepEqual(assistant.Agents, wantAgents) {
		t.Fatalf("filtered agents = %+v, want %+v", assistant.Agents, wantAgents)
	}
	wantSummary := Summary{InputTokens: 100, CachedInputTokens: 40, OutputTokens: 10, Turns: 1, ToolCalls: 1, ToolErrors: 1}
	if assistant.Summary != wantSummary {
		t.Fatalf("filtered summary = %+v, want %+v", assistant.Summary, wantSummary)
	}
	if len(assistant.Series) != 25 {
		t.Fatalf("filtered series = %d entries, want 25", len(assistant.Series))
	}
	if got := assistant.Series[0]; got.InputTokens != 100 || got.CachedInputTokens != 40 || got.OutputTokens != 10 {
		t.Fatalf("filtered series[0] = %+v", got)
	}
	if got := assistant.Series[1]; got.InputTokens != 0 || got.CachedInputTokens != 0 || got.OutputTokens != 0 {
		t.Fatalf("filtered series[1] = %+v, want zeroed", got)
	}
	wantModels := []ModelRow{{Provider: "deepseek", Model: "deepseek-chat", Requests: 1, InputTokens: 100, CachedInputTokens: 40, OutputTokens: 10}}
	if !reflect.DeepEqual(assistant.ByModel, wantModels) {
		t.Fatalf("filtered by_model = %+v, want %+v", assistant.ByModel, wantModels)
	}
	wantAgentRows := []AgentRow{{Agent: "assistant", Turns: 1, InputTokens: 100, CachedInputTokens: 40, OutputTokens: 10}}
	if !reflect.DeepEqual(assistant.ByAgent, wantAgentRows) {
		t.Fatalf("filtered by_agent = %+v, want %+v", assistant.ByAgent, wantAgentRows)
	}
	wantTools := []ToolRow{{Name: "read_file", Calls: 1, Errors: 1, AvgMS: 35, P95MS: 35, LastError: "boom", LastErrorAt: toolErrorAt}}
	if !reflect.DeepEqual(assistant.Tools, wantTools) {
		t.Fatalf("filtered tools = %+v, want %+v", assistant.Tools, wantTools)
	}
	wantReasons := []ReasonRow{{Reason: "end_turn", Count: 1}}
	if !reflect.DeepEqual(assistant.TurnReasons, wantReasons) {
		t.Fatalf("filtered turn_reasons = %+v, want %+v", assistant.TurnReasons, wantReasons)
	}
	if len(assistant.Errors) != 1 {
		t.Fatalf("filtered errors = %d rows, want 1: %+v", len(assistant.Errors), assistant.Errors)
	}
	if got := assistant.Errors[0]; got.Source != "tool" || got.Agent != "assistant" || got.Tool != "read_file" || got.Message != "read_file: boom" {
		t.Fatalf("filtered errors[0] = %+v", got)
	}

	lana := Aggregate(r, "lana", events, logs)
	if lana.Summary.Turns != 1 || lana.Summary.InputTokens != 50 || lana.Summary.OutputTokens != 5 || lana.Summary.ToolErrors != 0 {
		t.Fatalf("lana summary = %+v", lana.Summary)
	}
	if got := lana.Series[1]; got.InputTokens != 50 || got.OutputTokens != 5 {
		t.Fatalf("lana series[1] = %+v", got)
	}
	if len(lana.Errors) != 1 || lana.Errors[0].Source != "turn" || lana.Errors[0].Message != "max_steps" {
		t.Fatalf("lana errors = %+v", lana.Errors)
	}

	none := Aggregate(r, "ghost", events, logs)
	if !reflect.DeepEqual(none.Agents, wantAgents) {
		t.Fatalf("no-data agents = %+v, want %+v", none.Agents, wantAgents)
	}
	if none.Summary != (Summary{}) || len(none.Errors) != 0 {
		t.Fatalf("no-data dashboard summary = %+v, errors = %+v", none.Summary, none.Errors)
	}
}

// TestAggregateEmptyListsAreNonNil covers the API-DSH-001 rule that lists are
// never null.
func TestAggregateEmptyListsAreNonNil(t *testing.T) {
	r := Range{From: 0, To: 1000, BucketMS: 1000}
	d := Aggregate(r, "", nil, nil)
	if d.Series == nil || d.ByModel == nil || d.ByAgent == nil || d.BySource == nil || d.Tools == nil || d.TurnReasons == nil || d.Errors == nil || d.Agents == nil {
		t.Fatalf("a list is nil: %+v", d)
	}
	if d.Agent != "" || len(d.Agents) != 0 {
		t.Fatalf("agent = %q, agents = %+v, want empty", d.Agent, d.Agents)
	}
	if len(d.Errors) != 0 || len(d.BySource) != 3 {
		t.Fatalf("errors = %d, by_source = %d", len(d.Errors), len(d.BySource))
	}
}

// TestAggregateSkipsCancelledAttempts covers REQ-DSH-003 and REQ-DSH-008: an
// assistant/attempt whose error class is "cancelled" records a user cancel, so
// it adds no provider retry and no error row, while a real attempt still does.
func TestAggregateSkipsCancelledAttempts(t *testing.T) {
	at := int64(1_000_000)
	events := []core.ScannedEvent{
		event(t, core.KindAssistantAttempt, "s1", "assistant", "t1", at, core.AssistantAttempt{V: 1, Error: core.PayloadError{Class: "cancelled", Message: "cancelled"}}),
		event(t, core.KindAssistantAttempt, "s1", "assistant", "t1", at+1, core.AssistantAttempt{V: 1, Error: core.PayloadError{Class: "rate_limit", Status: 429, Message: "too many"}}),
	}
	d := Aggregate(Range{From: 0, To: 10_000, BucketMS: 1000}, "", events, nil)
	if d.Summary.ProviderRetries != 1 {
		t.Fatalf("provider_retries = %d, want 1", d.Summary.ProviderRetries)
	}
	var providerRows int
	for _, row := range d.Errors {
		if row.Source == "provider" {
			providerRows++
		}
	}
	if providerRows != 1 {
		t.Fatalf("provider error rows = %d, want 1: %+v", providerRows, d.Errors)
	}
}

// TestAggregateCapsAndTruncates covers the 100-row error cap and the 300/200
// character message cuts.
func TestAggregateCapsAndTruncates(t *testing.T) {
	long := make([]rune, 400)
	for i := range long {
		long[i] = 'x'
	}
	output := string(long)
	events := []core.ScannedEvent{
		event(t, core.KindToolCall, "s1", "a", "t", 1, core.ToolCallPayload{V: 1, CallID: "c", Name: "read_file"}),
		event(t, core.KindToolResult, "s1", "a", "t", 2, core.ToolResultPayload{V: 1, CallID: "c", Status: core.ToolStatusError, Output: output}),
	}
	for i := 0; i < 150; i++ {
		events = append(events, event(t, core.KindAssistantAttempt, "s1", "a", "t", int64(1000+i), core.AssistantAttempt{V: 1, Error: core.PayloadError{Class: "rate_limit", Message: "retry"}}))
	}
	d := Aggregate(Range{From: 0, To: 10_000, BucketMS: 1000}, "", events, nil)
	if len(d.Errors) != errorRowLimit {
		t.Fatalf("errors = %d, want %d", len(d.Errors), errorRowLimit)
	}
	if got := d.Tools[0].LastError; len([]rune(got)) != lastErrorMax {
		t.Fatalf("last_error runes = %d, want %d", len([]rune(got)), lastErrorMax)
	}
	// The newest error is an assistant/attempt (highest timestamp).
	for _, row := range d.Errors {
		if len([]rune(row.Message)) > toolMessageMax {
			t.Fatalf("error message = %d runes, want <= %d", len([]rune(row.Message)), toolMessageMax)
		}
	}
}

// TestParseRangeCoversAPIDSH001 covers the three ranges and the refusal.
func TestParseRangeCoversAPIDSH001(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		bucketMS     int64
		span         time.Duration
		wantValidErr bool
	}{
		{name: "24h", bucketMS: 3_600_000, span: 24 * time.Hour},
		{name: "7d", bucketMS: 86_400_000, span: 7 * 24 * time.Hour},
		{name: "30d", bucketMS: 86_400_000, span: 30 * 24 * time.Hour},
		{name: "1h", wantValidErr: true},
	}
	for _, tt := range tests {
		r, err := ParseRange(tt.name, now)
		if tt.wantValidErr {
			if err == nil {
				t.Fatalf("ParseRange(%q) succeeded", tt.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", tt.name, err)
		}
		if r.BucketMS != tt.bucketMS || r.To-r.From != tt.span.Milliseconds() {
			t.Fatalf("ParseRange(%q) = %+v", tt.name, r)
		}
		if r.To != now.UnixMilli() {
			t.Fatalf("ParseRange(%q).To = %d, want %d", tt.name, r.To, now.UnixMilli())
		}
	}
}
