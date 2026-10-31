package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

type fakeLLM struct{ provider core.Provider }

func (f fakeLLM) Provider(string) (core.Provider, bool) { return f.provider, true }
func (f fakeLLM) Names() []string                       { return []string{"mock"} }

// recordProvider records every request and returns a short summary text. The
// first failures calls return ErrContextTooLong.
type recordProvider struct {
	mu       sync.Mutex
	requests []core.ChatRequest
	text     string
	usage    core.Usage
	failures int
	calls    int
}

func (p *recordProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.requests = append(p.requests, req)
	text, usage := p.text, p.usage
	p.mu.Unlock()
	if n <= p.failures {
		return nil, core.ErrContextTooLong
	}
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: text}, nil)
		yield(core.ChatEvent{Kind: core.ChatUsage, Usage: usage}, nil)
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

func (p *recordProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *recordProvider) last() core.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[len(p.requests)-1]
}

func newService(t *testing.T, provider core.Provider, cfg core.Config) (*memstore.Store, core.SessionInfo, *Service) {
	t.Helper()
	store := memstore.New()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "coder", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]core.AgentConfig{}
	}
	if _, ok := cfg.Agents["coder"]; !ok {
		cfg.Agents["coder"] = core.AgentConfig{Provider: "mock", Model: "m"}
	}
	return store, info, NewService(store, fakeLLM{provider}, cfg)
}

func appendEvents(t *testing.T, store *memstore.Store, id string, drafts ...core.EventDraft) {
	t.Helper()
	if _, err := store.Append(context.Background(), id, drafts); err != nil {
		t.Fatal(err)
	}
}

func seedTurn(t *testing.T, store *memstore.Store, id string) {
	t.Helper()
	appendEvents(t, store, id,
		core.EventDraft{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		core.EventDraft{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hello"}}}},
		core.EventDraft{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hi"}}}},
		core.EventDraft{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	)
}

// toolStep is one step that answers a tool call with a result of the given
// size.
func toolStep(i, chars int) []core.EventDraft {
	call := fmt.Sprintf("c%d", i)
	return []core.EventDraft{
		{Kind: core.KindStepStart, TurnID: "t", Payload: core.StepStart{V: 1, Step: i, Provider: "mock", Model: "m"}},
		{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: "step"},
			{Kind: core.BlockToolUse, ID: call, Name: "big", Input: json.RawMessage(`{}`)},
		}}},
		{Kind: core.KindToolResult, TurnID: "t", Payload: core.ToolResultPayload{V: 1, CallID: call, Status: core.ToolStatusSuccess, Output: strings.Repeat("x", chars)}},
		{Kind: core.KindStepEnd, TurnID: "t", Payload: core.StepEnd{V: 1, Step: i, Provider: "mock", Model: "m"}},
	}
}

// seedLargeTurn is a two-unit session whose user message is sized so the span
// of an overflow compaction is larger than a short summary.
func seedLargeTurn(t *testing.T, store *memstore.Store, id string, chars int) {
	t.Helper()
	appendEvents(t, store, id,
		core.EventDraft{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		core.EventDraft{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: strings.Repeat("u", chars)}}}},
		core.EventDraft{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hi"}}}},
		core.EventDraft{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	)
}

func seedToolSteps(t *testing.T, store *memstore.Store, id string, steps, chars int) {
	t.Helper()
	appendEvents(t, store, id, core.EventDraft{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}})
	appendEvents(t, store, id, core.EventDraft{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "go"}}}})
	for i := 1; i <= steps; i++ {
		appendEvents(t, store, id, toolStep(i, chars)...)
	}
	appendEvents(t, store, id, core.EventDraft{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}})
}

// multiToolStep is one step whose assistant message calls several tools and
// whose results are each chars long. It is shaped like the long single-turn
// eval run: five read_file results of roughly 19 700 characters per step.
func multiToolStep(i, calls, chars int, usage core.Usage) []core.EventDraft {
	assistant := []core.ContentBlock{{Kind: core.BlockText, Text: "step"}}
	for j := 0; j < calls; j++ {
		assistant = append(assistant, core.ContentBlock{
			Kind: core.BlockToolUse, ID: fmt.Sprintf("c%d_%d", i, j), Name: "big", Input: json.RawMessage(`{}`),
		})
	}
	drafts := []core.EventDraft{
		{Kind: core.KindStepStart, TurnID: "t", Payload: core.StepStart{V: 1, Step: i, Provider: "mock", Model: "m"}},
		{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: assistant}},
	}
	for j := 0; j < calls; j++ {
		drafts = append(drafts, core.EventDraft{
			Kind: core.KindToolResult, TurnID: "t",
			Payload: core.ToolResultPayload{V: 1, CallID: fmt.Sprintf("c%d_%d", i, j), Status: core.ToolStatusSuccess, Output: strings.Repeat("x", chars)},
		})
	}
	drafts = append(drafts, core.EventDraft{
		Kind: core.KindStepEnd, TurnID: "t",
		Payload: core.StepEnd{V: 1, Step: i, Provider: "mock", Model: "m", Usage: usage},
	})
	return drafts
}

func summaryCount(t *testing.T, store *memstore.Store, id string) (int, core.CompactionSummary) {
	t.Helper()
	events, err := store.ChainEvents(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var payload core.CompactionSummary
	for _, event := range events {
		if event.Kind != core.KindCompactionSummary {
			continue
		}
		count++
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
	}
	return count, payload
}

func TestCompactNowSummary(t *testing.T) {
	provider := &recordProvider{text: "short"}
	store, info, service := newService(t, provider, core.Config{})
	seedTurn(t, store, info.ID)
	result, err := service.CompactNow(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	count, payload := summaryCount(t, store, info.ID)
	if count != 1 || payload.Summary != "short" {
		t.Fatalf("summaries=%d payload=%+v", count, payload)
	}
	if payload.UptoSeq != result.UptoSeq || payload.FromSeq != 3 || payload.RetainedFromSeq != 4 {
		t.Fatalf("payload=%+v result=%+v", payload, result)
	}
}

// REQ-MDL-011: the compaction window comes from the catalog when the agent sets
// none, and a thinking model reserves the thinking output. REQ-ECX-030: the
// output and headroom are capped relative to the window, and a window below
// 4096 is raised to 4096.
func TestResolvePolicyCatalog(t *testing.T) {
	cases := []struct {
		name     string
		agent    core.AgentConfig
		maxToken int
		effort   string
		window   int64
		output   int64
		trigger  int64
	}{
		{"deepseek-flash window", core.AgentConfig{Model: "deepseek-flash"}, 0, "", 1048576, 32000, 838860},
		{"deepseek-chat window", core.AgentConfig{Model: "deepseek-chat"}, 0, "", 128000, 8192, 102400},
		{"effort off keeps the plain output", core.AgentConfig{Model: "deepseek-flash"}, 0, "off", 1048576, 8192, 838860},
		{"request max_tokens wins", core.AgentConfig{Model: "deepseek-flash"}, 2000, "", 1048576, 2000, 838860},
		{"agent context_window caps the thinking output", core.AgentConfig{Model: "deepseek-flash", ContextWindow: 64000}, 0, "", 64000, 16000, 40000},
		{"unknown model keeps the default", core.AgentConfig{Model: "unknown"}, 0, "", 128000, 8192, 102400},
		{"thinking model on a small window", core.AgentConfig{Model: "deepseek-flash", ContextWindow: 24000}, 0, "", 24000, 6000, 15000},
		{"window below the floor is raised", core.AgentConfig{Model: "deepseek-flash", ContextWindow: 1000}, 0, "", 4096, 1024, 2560},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &recordProvider{text: "x"}
			_, _, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{"coder": tc.agent}})
			pol := service.resolvePolicy("coder", tc.maxToken, tc.effort)
			if pol.window != tc.window || pol.output != tc.output {
				t.Fatalf("window=%d output=%d, want %d/%d", pol.window, pol.output, tc.window, tc.output)
			}
			if got := pol.trigger(); got != tc.trigger {
				t.Fatalf("trigger=%d, want %d", got, tc.trigger)
			}
			if got := pol.retained(); got <= 0 {
				t.Fatalf("retained=%d, want a positive tail", got)
			}
		})
	}
}

// REQ-MDL-005: the compaction summary request carries the request's reasoning
// effort.
func TestCompactSummaryCarriesEffort(t *testing.T) {
	provider := &recordProvider{text: "condensed"}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "deepseek-flash"},
	}})
	seedTurn(t, store, info.ID)
	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "coder", Provider: "mock", Model: "deepseek-flash",
		System: "head", ReasoningEffort: "high", Trigger: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	if provider.count() != 1 {
		t.Fatalf("summary calls = %d, want 1", provider.count())
	}
	if got := provider.last().ReasoningEffort; got != "high" {
		t.Fatalf("summary reasoning_effort = %q, want high", got)
	}
}

func TestCompactNowRejects(t *testing.T) {
	provider := &recordProvider{text: "x"}
	store, info, service := newService(t, provider, core.Config{})
	if _, err := service.CompactNow(context.Background(), info.ID); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err=%v", err)
	}
	seedTurn(t, store, info.ID)
	appendEvents(t, store, info.ID, core.EventDraft{Kind: core.KindTurnStart, TurnID: "t2", Payload: core.TurnStart{V: 1, TurnID: "t2"}})
	if _, err := service.CompactNow(context.Background(), info.ID); !errors.Is(err, ErrTurnActive) {
		t.Fatalf("err=%v", err)
	}
}

// (a) sixty tool steps of 6000 characters each under a 32000 window compact
// into a framed summary plus a retained tail that starts at a unit boundary.
func TestCompactUnderPressureKeepsTail(t *testing.T) {
	provider := &recordProvider{text: "condensed"}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "m", ContextWindow: 32000},
	}})
	seedToolSteps(t, store, info.ID, 60, 6000)

	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "coder", Provider: "mock", Model: "m",
		System: "head", Trigger: "pressure",
	}); err != nil {
		t.Fatal(err)
	}
	count, payload := summaryCount(t, store, info.ID)
	if count != 1 || payload.Summary != "condensed" {
		t.Fatalf("summaries=%d payload=%+v", count, payload)
	}
	if payload.RetainedFromSeq != 0 && payload.RetainedFromSeq <= payload.UptoSeq {
		t.Fatalf("retained_from_seq %d must follow upto_seq %d", payload.RetainedFromSeq, payload.UptoSeq)
	}
	messages, err := store.DeriveMessages(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) < 3 {
		t.Fatalf("messages=%d", len(messages))
	}
	if messages[0].Role != core.RoleUser || !strings.Contains(messages[0].Content[0].Text, "<compacted-summary>") {
		t.Fatalf("first message = %+v", messages[0])
	}
	// The retained tail is a whole unit: an assistant message (its tool result
	// follows in the next message). It never starts with a bare tool result.
	if messages[1].Role != core.RoleAssistant {
		t.Fatalf("tail starts with %s, want assistant (a unit boundary)", messages[1].Role)
	}
}

// (b) pruning alone relieves pressure when large tool results dominate; no
// summary call is made.
func TestPruneRelievesPressure(t *testing.T) {
	provider := &recordProvider{text: "condensed"}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "m", ContextWindow: 40000},
	}})
	// The trigger of this window is 26808 tokens after the REQ-ECX-030 caps, so
	// the seeded session must exceed that.
	seedToolSteps(t, store, info.ID, 5, 22000)

	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "coder", Provider: "mock", Model: "m",
		System: "head", Trigger: "pressure",
	}); err != nil {
		t.Fatal(err)
	}
	if provider.count() != 0 {
		t.Fatalf("summary calls = %d, want 0", provider.count())
	}
	count, _ := summaryCount(t, store, info.ID)
	if count != 0 {
		t.Fatalf("summaries = %d, want 0", count)
	}
	events, _ := store.ChainEvents(context.Background(), info.ID)
	pruned := 0
	for _, event := range events {
		if event.Kind == core.KindToolResultPruned {
			pruned++
		}
	}
	if pruned == 0 {
		t.Fatal("no tool_result_pruned event")
	}
	messages, _ := store.DeriveMessages(context.Background(), info.ID)
	prunedOutput := false
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Kind == core.BlockToolResult && strings.Contains(block.Output, "middle pruned") {
				prunedOutput = true
			}
		}
	}
	if !prunedOutput {
		t.Fatal("no derived tool result was pruned")
	}
}

// When pruning does not relieve the pressure, the summary is written over the
// pruned span and the session-changed guard does not trip on our own writes.
func TestPruneThenSummarize(t *testing.T) {
	provider := &recordProvider{text: "condensed"}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "m", ContextWindow: 40000},
	}})
	// The trigger of this window is 26808 tokens after the REQ-ECX-030 caps, so
	// the pruned session must still exceed it for the summary to run.
	seedToolSteps(t, store, info.ID, 4, 100000)

	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "coder", Provider: "mock", Model: "m", System: "head", Trigger: "pressure",
	}); err != nil {
		t.Fatal(err)
	}
	count, _ := summaryCount(t, store, info.ID)
	if count != 1 {
		t.Fatalf("summaries = %d, want 1", count)
	}
	if provider.count() != 1 {
		t.Fatalf("summary calls = %d, want 1", provider.count())
	}
	// The summary request reads the pruned surface.
	replayed := provider.last().Messages
	pruned := false
	for _, message := range replayed {
		for _, block := range message.Content {
			if block.Kind == core.BlockToolResult && strings.Contains(block.Output, "middle pruned") {
				pruned = true
			}
		}
	}
	if !pruned {
		t.Fatal("summary did not read the pruned surface")
	}
}

// (c) the summary request reuses the System, Tools, and leading messages of the
// conversation, plus one final user message with the 8-section instruction.
func TestSummaryRequestShape(t *testing.T) {
	provider := &recordProvider{text: "condensed summary"}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "m", ContextWindow: 32000},
	}})
	seedToolSteps(t, store, info.ID, 20, 4000)
	before, err := store.DeriveMessages(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}

	tools := []core.ToolSpec{{Name: "big", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "coder", Provider: "mock", Model: "m",
		System: "the head", Tools: tools, Trigger: "pressure",
	}); err != nil {
		t.Fatal(err)
	}
	if provider.count() != 1 {
		t.Fatalf("summary calls = %d, want 1", provider.count())
	}
	req := provider.last()
	if req.System != "the head" || !reflect.DeepEqual(req.Tools, tools) {
		t.Fatalf("summary request prefix changed: system=%q tools=%v", req.System, req.Tools)
	}
	if req.MaxTokens != summaryMaxTokens {
		t.Fatalf("summary MaxTokens = %d", req.MaxTokens)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != core.RoleUser || !strings.HasPrefix(last.Content[0].Text, "You are now acting as a compaction engine for this AI assistant") {
		t.Fatalf("final message = %+v", last)
	}
	if !strings.Contains(last.Content[0].Text, "## Primary Request and Intent") {
		t.Fatalf("instruction is missing the 8-section structure")
	}
	if strings.Contains(last.Content[0].Text, "AI coding assistant") {
		t.Fatal("instruction still says \"AI coding assistant\"")
	}
	// The replayed messages are a prefix of the conversation, without the
	// trailing instruction and without the retained tail.
	leading := req.Messages[:len(req.Messages)-1]
	if len(leading) >= len(before) {
		t.Fatalf("summary replayed the tail: leading=%d conversation=%d", len(leading), len(before))
	}
	if !reflect.DeepEqual(leading, before[:len(leading)]) {
		t.Fatalf("summary messages are not the conversation prefix")
	}
}

// (d) a context-length error triggers one forced compaction and one retry.
func TestContextTooLongRetryOnce(t *testing.T) {
	provider := &recordProvider{text: "condensed", failures: 1}
	store, info, service := newService(t, provider, core.Config{})
	seedLargeTurn(t, store, info.ID, 200)
	hooks := core.NewHooks()
	hooks.LLMStream.SetDefault(func(ctx context.Context, in core.LLMStreamInput) (core.ChatStream, error) {
		return provider.Stream(ctx, in.Request)
	})
	service.Install(hooks)

	req := core.ChatRequest{Model: "m", System: "head", Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hello"}}}}}
	stream, err := hooks.LLMStream.Call(context.Background(), core.LLMStreamInput{SessionID: info.ID, TurnID: "t", Step: 1, Targets: []core.LLMTarget{{Provider: "mock", Model: "m"}}, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for event, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == core.ChatTextDelta {
			text.WriteString(event.Text)
		}
	}
	if text.String() != "condensed" || provider.count() != 3 {
		t.Fatalf("text=%q calls=%d, want condensed/3", text.String(), provider.count())
	}
	if count, _ := summaryCount(t, store, info.ID); count != 1 {
		t.Fatalf("summaries = %d, want 1", count)
	}
}

func TestContextTooLongTwiceEndsTurn(t *testing.T) {
	provider := &recordProvider{text: "condensed", failures: 2}
	store, info, service := newService(t, provider, core.Config{})
	seedTurn(t, store, info.ID)
	hooks := core.NewHooks()
	hooks.LLMStream.SetDefault(func(ctx context.Context, in core.LLMStreamInput) (core.ChatStream, error) {
		return provider.Stream(ctx, in.Request)
	})
	service.Install(hooks)
	_, err := hooks.LLMStream.Call(context.Background(), core.LLMStreamInput{SessionID: info.ID, TurnID: "t", Step: 1, Targets: []core.LLMTarget{{Provider: "mock", Model: "m"}}})
	if !errors.Is(err, core.ErrContextTooLong) {
		t.Fatalf("err=%v", err)
	}
}

// (f) the agent's context_window reaches the engine: a small window compacts,
// a large one does not.
func TestAgentContextWindowHonored(t *testing.T) {
	provider := &recordProvider{text: "condensed"}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"small": {Provider: "mock", Model: "m", ContextWindow: 32000},
		"big":   {Provider: "mock", Model: "m", ContextWindow: 4000000},
	}})
	seedToolSteps(t, store, info.ID, 60, 6000)

	// The large window is under threshold: nothing happens and no model call.
	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "big", Provider: "mock", Model: "m", System: "head", Trigger: "pressure",
	}); err != nil {
		t.Fatal(err)
	}
	if provider.count() != 0 {
		t.Fatalf("big window compacted (calls=%d)", provider.count())
	}
	if count, _ := summaryCount(t, store, info.ID); count != 0 {
		t.Fatalf("big window wrote %d summaries", count)
	}

	// The small window is over threshold: it compacts.
	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "small", Provider: "mock", Model: "m", System: "head", Trigger: "pressure",
	}); err != nil {
		t.Fatal(err)
	}
	if count, _ := summaryCount(t, store, info.ID); count != 1 {
		t.Fatalf("small window wrote %d summaries, want 1", count)
	}
}

// An overflow trigger ignores the threshold and keeps only the newest unit.
func TestOverflowCompactsBelowThreshold(t *testing.T) {
	provider := &recordProvider{text: "condensed"}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "m", ContextWindow: 4000000},
	}})
	seedLargeTurn(t, store, info.ID, 200)
	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "coder", Provider: "mock", Model: "m", System: "head", Trigger: "overflow",
	}); err != nil {
		t.Fatal(err)
	}
	if count, _ := summaryCount(t, store, info.ID); count != 1 {
		t.Fatalf("summaries = %d, want 1", count)
	}
}

// A step/end with real usage counts the tool outputs as they were sent. Pruning
// that content must relieve the pressure on its own: the estimate drops and no
// summary request is made.
func TestPruneRelievesUsagePressure(t *testing.T) {
	provider := &recordProvider{text: "condensed"}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "m", ContextWindow: 40000},
	}})
	seedToolSteps(t, store, info.ID, 5, 12000)
	appendEvents(t, store, info.ID, core.EventDraft{Kind: core.KindStepEnd, TurnID: "t", Payload: core.StepEnd{
		V: 1, Step: 99, Provider: "mock", Model: "m", Usage: core.Usage{InputTokens: 27000, OutputTokens: 500},
	}})

	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "coder", Provider: "mock", Model: "m", System: "head", Trigger: "pressure",
	}); err != nil {
		t.Fatal(err)
	}
	if provider.count() != 0 {
		t.Fatalf("summary calls = %d, want 0", provider.count())
	}
	if count, _ := summaryCount(t, store, info.ID); count != 0 {
		t.Fatalf("summaries = %d, want 0", count)
	}
	events, _ := store.ChainEvents(context.Background(), info.ID)
	pruned := 0
	for _, event := range events {
		if event.Kind == core.KindToolResultPruned {
			pruned++
		}
	}
	if pruned == 0 {
		t.Fatal("no tool_result_pruned event")
	}
}

// A span that is only the prior summary unit is not summarized again.
func TestOnlySummarySpanIsSkipped(t *testing.T) {
	provider := &recordProvider{text: "condensed"}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "m", ContextWindow: 32000},
	}})
	appendEvents(t, store, info.ID,
		core.EventDraft{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		core.EventDraft{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "old"}}}},
		core.EventDraft{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "old reply"}}}},
	)
	events, err := store.ChainEvents(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	upto := events[len(events)-1].Seq
	appendEvents(t, store, info.ID,
		core.EventDraft{Kind: core.KindCompactionSummary, TurnID: "t", Payload: core.CompactionSummary{
			V: 1, Summary: strings.Repeat("s", 100000), FromSeq: 1, UptoSeq: upto, RetainedFromSeq: upto + 1,
		}},
		core.EventDraft{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "new"}}}},
		core.EventDraft{Kind: core.KindAssistantMessage, TurnID: "t", Payload: core.AssistantMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "new reply"}}}},
		core.EventDraft{Kind: core.KindTurnEnd, TurnID: "t", Payload: core.TurnEnd{V: 1, TurnID: "t", Reason: "end_turn"}},
	)

	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "coder", Provider: "mock", Model: "m", System: "head", Trigger: "pressure",
	}); err != nil {
		t.Fatal(err)
	}
	if provider.count() != 0 {
		t.Fatalf("summary calls = %d, want 0", provider.count())
	}
	if count, _ := summaryCount(t, store, info.ID); count != 1 {
		t.Fatalf("summaries = %d, want the seeded 1", count)
	}
}

// A failed pressure attempt is not repeated until the session gains enough
// message-producing events or a new turn starts. "overflow" and "manual" always
// try.
func TestPressureAttemptThrottled(t *testing.T) {
	provider := &recordProvider{text: strings.Repeat("z", 20000)}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "m", ContextWindow: 32000},
	}})
	seedToolSteps(t, store, info.ID, 5, 3000)
	appendEvents(t, store, info.ID, core.EventDraft{Kind: core.KindStepEnd, TurnID: "t", Payload: core.StepEnd{
		V: 1, Step: 99, Provider: "mock", Model: "m", Usage: core.Usage{InputTokens: 100000, OutputTokens: 1000},
	}})

	attempt := func(trigger, turn string) {
		t.Helper()
		if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
			SessionID: info.ID, TurnID: turn, AgentName: "coder", Provider: "mock", Model: "m", System: "head", Trigger: trigger,
		}); err != nil && trigger == "manual" {
			t.Fatal(err)
		}
	}

	// The first pressure attempt runs the summarizer and is rejected.
	attempt("pressure", "t")
	if provider.count() != 1 {
		t.Fatalf("calls = %d, want 1", provider.count())
	}
	// Without growth the next step is throttled: no second call.
	attempt("pressure", "t")
	if provider.count() != 1 {
		t.Fatalf("throttled call = %d, want 1", provider.count())
	}
	// Eight more message-producing events lift the throttle.
	for i := 0; i < pressureRetryEvents; i++ {
		appendEvents(t, store, info.ID, core.EventDraft{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "g"}}}})
	}
	attempt("pressure", "t")
	if provider.count() != 2 {
		t.Fatalf("retry call = %d, want 2", provider.count())
	}
	// The retry failed too, so the throttle is back.
	attempt("pressure", "t")
	if provider.count() != 2 {
		t.Fatalf("re-throttled call = %d, want 2", provider.count())
	}
	// A new turn tries regardless of growth.
	attempt("pressure", "t2")
	if provider.count() != 3 {
		t.Fatalf("new-turn call = %d, want 3", provider.count())
	}
	// "overflow" and "manual" ignore the throttle.
	attempt("overflow", "t2")
	if provider.count() != 4 {
		t.Fatalf("overflow call = %d, want 4", provider.count())
	}
	attempt("manual", "t2")
	if provider.count() != 5 {
		t.Fatalf("manual call = %d, want 5", provider.count())
	}
}

// The live agent lookup replaces the boot snapshot: a smaller context_window
// taken from the lookup makes the threshold trigger where it did not before.
func TestLiveAgentConfigLookup(t *testing.T) {
	provider := &recordProvider{text: "condensed"}
	big := core.AgentConfig{Provider: "mock", Model: "m", ContextWindow: 4000000}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{"coder": big}})
	seedToolSteps(t, store, info.ID, 60, 6000)
	live := map[string]core.AgentConfig{"coder": big}
	service.LookupAgent = func(name string) (core.AgentConfig, bool) {
		agent, ok := live[name]
		return agent, ok
	}

	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "coder", Provider: "mock", Model: "m", System: "head", Trigger: "pressure",
	}); err != nil {
		t.Fatal(err)
	}
	if count, _ := summaryCount(t, store, info.ID); count != 0 {
		t.Fatalf("big window wrote %d summaries", count)
	}

	live["coder"] = core.AgentConfig{Provider: "mock", Model: "m", ContextWindow: 32000}
	if _, err := service.CompactIfNeeded(context.Background(), core.CompactRequest{
		SessionID: info.ID, AgentName: "coder", Provider: "mock", Model: "m", System: "head", Trigger: "pressure",
	}); err != nil {
		t.Fatal(err)
	}
	if count, _ := summaryCount(t, store, info.ID); count != 1 {
		t.Fatalf("small live window wrote %d summaries, want 1", count)
	}
}

// REQ-LTC-012/013 regression: a long single turn whose steps each carry five
// ~19 700-character read_file results. The provider reports real usage, pruning
// shrinks the oldest results, and the estimate must not treat that relief as
// including the newest step's not-yet-counted results: pressure remains, so one
// summary runs. The newest unit alone is larger than the trigger, so the
// estimate can stay above T after the summary; a repeat check must not loop.
func TestPressureSummaryRunsAfterPruningReportedUsage(t *testing.T) {
	provider := &recordProvider{text: "condensed"}
	store, info, service := newService(t, provider, core.Config{Agents: map[string]core.AgentConfig{
		"coder": {Provider: "mock", Model: "deepseek-flash", ContextWindow: 24000},
	}})
	appendEvents(t, store, info.ID,
		core.EventDraft{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		core.EventDraft{Kind: core.KindUserMessage, TurnID: "t", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "read all files"}}}},
	)
	// Step 1's usage was tiny; step 2 reports the five full results step 1
	// returned, exactly like the eval run that motivated this test.
	appendEvents(t, store, info.ID, multiToolStep(1, 5, 19700, core.Usage{InputTokens: 2544, OutputTokens: 177})...)
	appendEvents(t, store, info.ID, multiToolStep(2, 5, 19700, core.Usage{InputTokens: 25803, OutputTokens: 496})...)

	req := core.CompactRequest{
		SessionID: info.ID, TurnID: "t", AgentName: "coder", Provider: "mock",
		Model: "deepseek-flash", System: "head", Trigger: "pressure",
	}
	result, err := service.CompactIfNeeded(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if provider.count() != 1 {
		t.Fatalf("summary calls = %d, want 1: pruning hid pressure the provider still reports", provider.count())
	}
	count, payload := summaryCount(t, store, info.ID)
	if count != 1 || payload.Summary != "condensed" {
		t.Fatalf("summaries=%d payload=%+v", count, payload)
	}
	if payload.UptoSeq == 0 || payload.RetainedFromSeq <= payload.UptoSeq {
		t.Fatalf("payload=%+v: retained tail must follow the summarized span", payload)
	}
	if result.TokensAfter >= result.TokensBefore {
		t.Fatalf("summary did not shrink the request: before=%d after=%d", result.TokensBefore, result.TokensAfter)
	}
	messages, err := store.DeriveMessages(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) < 3 || !strings.Contains(messages[0].Content[0].Text, "<compacted-summary>") {
		t.Fatalf("derived history does not start with a framed summary: %+v", messages)
	}

	// The newest unit alone is over T, so a second pressure check sees pressure
	// again. It must skip the summary-only span instead of looping.
	if _, err := service.CompactIfNeeded(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if provider.count() != 1 {
		t.Fatalf("repeat check made %d summary calls, want 1", provider.count())
	}
	if count, _ := summaryCount(t, store, info.ID); count != 1 {
		t.Fatalf("repeat check wrote %d summaries, want 1", count)
	}
}
