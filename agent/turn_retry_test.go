package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

func countEvents(t *testing.T, h *harness, kind string) int {
	t.Helper()
	count := 0
	for _, event := range h.events(t) {
		if event.Kind == kind {
			count++
		}
	}
	return count
}

// liveFrameKinds drains n live frames of the session and returns their kinds.
func liveFrameKinds(t *testing.T, sub *core.Subscription[core.LiveFrame], n int) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	kinds := make([]string, 0, n)
	for len(kinds) < n {
		received, err := sub.Recv(ctx)
		if err != nil {
			t.Fatalf("recv frame %d: %v", len(kinds), err)
		}
		if received.Lagged > 0 {
			continue
		}
		kinds = append(kinds, received.Value.Kind)
	}
	return kinds
}

// TestStepRetryAfterMidStreamFailure covers REQ-LTC-032: a stream that fails
// after its first event repeats the step, stores exactly one assistant message,
// and publishes a step_retry frame.
func TestStepRetryAfterMidStreamFailure(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: strptr("partial")}}, Error: &mock.ScriptError{Class: "server", Message: "boom"}},
		textResponse("final"),
	}}, nil)
	sub, err := h.reg.SubscribeLive(h.info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "final" {
		t.Fatalf("result=%+v", result)
	}
	if got := countEvents(t, h, core.KindAssistantMessage); got != 1 {
		t.Fatalf("assistant messages=%d want 1", got)
	}
	if steps := countSteps(t, h); steps != 1 {
		t.Fatalf("steps=%d want 1", steps)
	}
	kinds := liveFrameKinds(t, sub, 3)
	want := []string{"text_delta", "step_retry", "text_delta"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("frames=%v want %v", kinds, want)
	}
}

// The step-retry backoff comes from the injectable delay pair.
func TestStepRetryUsesBackoff(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: strptr("partial")}}, Error: &mock.ScriptError{Class: "rate_limit", Message: "slow down"}},
		textResponse("final"),
	}}, nil)
	oldWait, oldDelays := stepRetryWait, stepRetryDelays
	t.Cleanup(func() { stepRetryWait, stepRetryDelays = oldWait, oldDelays })
	var waits []time.Duration
	stepRetryDelays = []time.Duration{7 * time.Millisecond, 9 * time.Millisecond}
	stepRetryWait = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	if len(waits) != 1 || waits[0] != 7*time.Millisecond {
		t.Fatalf("waits=%v want one 7ms wait", waits)
	}
}

// A non-retryable mid-stream failure is not repeated.
func TestStepRetrySkippedForAuthError(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: strptr("partial")}}, Error: &mock.ScriptError{Class: "auth", Message: "bad key"}},
		textResponse("unused"),
	}}, nil)
	result := h.run(t, "hi")
	if result.StopReason != "error" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 1 {
		t.Fatalf("steps=%d want 1", steps)
	}
}

// TestTurnNoLimitRunsPastCap covers REQ-LTC-040: with max_steps unset a root
// turn runs past 200 steps and ends only when the model stops calling tools.
func TestTurnNoLimitRunsPastCap(t *testing.T) {
	const toolSteps = 205
	responses := make([]mock.Response, 0, toolSteps+1)
	for i := 0; i < toolSteps; i++ {
		// Each call reads a distinct line, so its result digest differs and the
		// loop guard's read-only detectors stay quiet; the test is about the
		// missing step cap, not about a looping model.
		responses = append(responses, toolResponse(fmt.Sprintf("c%d", i), "read_file", fmt.Sprintf(`{"path":"a.txt","offset":%d,"limit":1}`, i+1)))
	}
	responses = append(responses, textResponse("done"))
	h := newHarness(t, mock.Script{Responses: responses}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}},
	})
	var content strings.Builder
	for i := 1; i <= toolSteps; i++ {
		fmt.Fprintf(&content, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "done" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps <= 200 {
		t.Fatalf("steps=%d want > 200", steps)
	}
}

// A child turn with no cap also runs past 200 steps (REQ-LTC-040).
func TestChildTurnNoLimitRunsPastCap(t *testing.T) {
	const toolSteps = 205
	responses := make([]mock.Response, 0, toolSteps+2)
	for i := 0; i < toolSteps; i++ {
		// Each call reads a distinct line, so its result digest differs and the
		// loop guard's read-only detectors stay quiet; the test is about the
		// missing step cap, not about a looping model.
		responses = append(responses, toolResponse(fmt.Sprintf("call_%d", i), "read_file", fmt.Sprintf(`{"path":"a.txt","offset":%d,"limit":1}`, i+1)))
	}
	responses = append(responses, textResponse("child done"))
	h := newSubagentHarness(t, mock.Script{Responses: responses}, map[string]core.AgentConfig{
		"coder":  {Provider: "mock", Model: "m", Tools: []string{"task"}, Subagents: []string{"worker"}},
		"worker": {Provider: "mock", Model: "child-model", Tools: []string{"read_file"}},
	})
	var content strings.Builder
	for i := 1; i <= toolSteps; i++ {
		fmt.Fprintf(&content, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := newSubagentParent(t, h, "coder")
	out, err := (&subagentRunner{reg: h.reg, allowed: []string{"worker"}}).Spawn(context.Background(), core.SpawnRequest{
		ParentSessionID: parent.ID, ParentCallID: "call_worker", Agent: "worker", Prompt: "keep working",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := decodeTaskOutput(t, out)
	if result.Status != "end_turn" {
		t.Fatalf("output=%+v want end_turn", result)
	}
	if result.Steps <= 200 {
		t.Fatalf("child steps=%d want > 200", result.Steps)
	}
}

// requestRecorder wraps a provider and records each request so a test can
// inspect what the turn sent.
type requestRecorder struct {
	inner core.Provider

	mu       sync.Mutex
	requests []core.ChatRequest
}

func (p *requestRecorder) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return p.inner.Stream(ctx, req)
}

func (p *requestRecorder) last() core.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return core.ChatRequest{}
	}
	return p.requests[len(p.requests)-1]
}

// queueingProvider queues a message into the session inbox just before its
// after-th stream call, so the message arrives while the turn is running.
type queueingProvider struct {
	inner     core.Provider
	reg       *Registry
	sessionID string
	text      string
	after     int

	mu    sync.Mutex
	calls int
}

func (p *queueingProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	p.calls++
	send := p.calls == p.after
	p.mu.Unlock()
	if send {
		if err := p.reg.Send(ctx, p.sessionID, p.text); err != nil {
			return nil, err
		}
	}
	return p.inner.Stream(ctx, req)
}

// waitForIdle blocks until the session's turn chain has nothing left to run.
func waitForIdle(t *testing.T, h *harness) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for h.reg.TurnActive(h.info.ID) {
		if time.Now().After(deadline) {
			t.Fatal("session did not go idle")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestStepCapReportKeepsTools covers REQ-LTC-040 and REQ-LPG-015: reaching a
// configured cap runs one final report step that keeps the same offered tools
// and asks for a status report.
func TestStepCapReportKeepsTools(t *testing.T) {
	script := mock.Script{Responses: []mock.Response{
		toolResponse("c1", "read_file", `{"path":"a.txt"}`),
		toolResponse("c2", "read_file", `{"path":"a.txt"}`),
		toolResponse("c3", "read_file", `{"path":"a.txt"}`),
		textResponse("here is the report"),
	}}
	h := newHarness(t, script, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}, MaxSteps: 3},
	})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &requestRecorder{inner: h.llm.providers["mock"]}
	h.llm.providers["mock"] = rec
	result := h.run(t, "hi")
	if result.StopReason != "max_steps" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 4 {
		t.Fatalf("steps=%d want 4", steps)
	}
	if got := lastUserText(t, h); got != stepLimitNote {
		t.Fatalf("last user message=%q want %q", got, stepLimitNote)
	}
	assertReportKeepsTools(t, rec)
	if got := countEvents(t, h, core.KindAssistantMessage); got != 4 {
		t.Fatalf("assistant messages=%d want 4", got)
	}
}

// TestStepCapKeepsQueuedMessage covers REQ-LTC-040: a message that arrives while
// a capped turn is running is not lost. The capped turn still ends with reason
// max_steps after its report step, and the queued message starts the next turn
// with its text intact.
func TestStepCapKeepsQueuedMessage(t *testing.T) {
	script := mock.Script{Responses: []mock.Response{
		toolResponse("c1", "read_file", `{"path":"a.txt"}`),
		toolResponse("c2", "read_file", `{"path":"a.txt"}`),
		textResponse("here is the report"),
		textResponse("queued handled"),
	}}
	h := newHarness(t, script, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}, MaxSteps: 2},
	})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The message arrives during the second model call, so the capped turn
	// claims it at the step-cap check instead of feeding it to a step.
	h.llm.providers["mock"] = &queueingProvider{
		inner: h.llm.providers["mock"], reg: h.reg, sessionID: h.info.ID, text: "queued message", after: 2,
	}
	turnID, err := h.reg.Start(context.Background(), h.info.ID, "hi")
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.reg.Wait(context.Background(), turnID)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "max_steps" {
		t.Fatalf("result=%+v", result)
	}
	waitForIdle(t, h)
	// The capped turn ran its two capped steps plus the report step.
	turnSteps := 0
	for _, event := range h.events(t) {
		if event.Kind == core.KindStepStart && event.TurnID == turnID {
			turnSteps++
		}
	}
	if turnSteps != 3 {
		t.Fatalf("turn steps=%d want 3", turnSteps)
	}
	// The queued message starts the next turn with its text intact.
	msg := h.lastUserMessage(t)
	found := false
	for _, block := range msg.Content {
		if block.Text == "queued message" {
			found = true
		}
	}
	if !found {
		t.Fatalf("queued message did not start the next turn: %+v", msg.Content)
	}
}

// TestStepUsageIncludesCachedTokens covers REQ-LTC-005: the step usage and the
// turn usage carry the cached input tokens.
func TestStepUsageIncludesCachedTokens(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{Text: strptr("ok")},
			{Usage: &core.Usage{InputTokens: 100, OutputTokens: 5, CachedInputTokens: 80}},
			{Stop: strptr("end_turn")},
		}},
	}}, nil)
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	if got := stepUsageFor(t, h, h.info.ID, 1); got.CachedInputTokens != 80 {
		t.Fatalf("step cached=%d want 80", got.CachedInputTokens)
	}
	if result.Usage.CachedInputTokens != 80 {
		t.Fatalf("turn cached=%d want 80", result.Usage.CachedInputTokens)
	}
}

// TestStepLogReportsCacheMetrics covers REQ-LTC-005: the per-step log line
// includes cached_input_tokens and cache_hit_pct.
func TestStepLogReportsCacheMetrics(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{
			{Text: strptr("ok")},
			{Usage: &core.Usage{InputTokens: 100, OutputTokens: 5, CachedInputTokens: 80}},
			{Stop: strptr("end_turn")},
		}},
	}}, nil)
	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	out := buf.String()
	if !strings.Contains(out, "cached_input_tokens=80") || !strings.Contains(out, "cache_hit_pct=80") {
		t.Fatalf("step log missing cache metrics:\n%s", out)
	}
}

func TestCacheHitPct(t *testing.T) {
	cases := []struct {
		input, cached, want int64
	}{
		{100, 80, 80},
		{100, 0, 0},
		{0, 50, 0},
		{3, 1, 33},
	}
	for _, tc := range cases {
		if got := cacheHitPct(tc.input, tc.cached); got != tc.want {
			t.Fatalf("cacheHitPct(%d,%d)=%d want %d", tc.input, tc.cached, got, tc.want)
		}
	}
}
