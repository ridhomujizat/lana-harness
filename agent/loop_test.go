package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/store/memstore"
	"github.com/ridhomujizat/lana-harness/tools"
)

type testLLM struct {
	providers map[string]core.Provider
}

func (l *testLLM) Provider(name string) (core.Provider, bool) {
	p, ok := l.providers[name]
	return p, ok
}
func (l *testLLM) Names() []string {
	names := make([]string, 0, len(l.providers))
	for name := range l.providers {
		names = append(names, name)
	}
	return names
}

type harness struct {
	reg   *Registry
	store *memstore.Store
	info  core.SessionInfo
	hooks *core.Hooks
	tools *tools.Registry
	llm   *testLLM
	home  string
	ws    string
}

func newHarness(t *testing.T, script mock.Script, agents map[string]core.AgentConfig) *harness {
	t.Helper()
	home := t.TempDir()
	ws := t.TempDir()
	store := memstore.New()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "library", Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	hooks := core.NewHooks()
	toolReg := tools.NewRegistry()
	if err := toolReg.Register(tools.NewReadFile(&tools.Guard{Workspace: ws})); err != nil {
		t.Fatal(err)
	}
	if err := toolReg.Register(tools.NewWriteFile(&tools.Guard{Workspace: ws})); err != nil {
		t.Fatal(err)
	}
	if err := toolReg.Register(tools.NewBash(&tools.Guard{Workspace: ws})); err != nil {
		t.Fatal(err)
	}
	llm := &testLLM{providers: map[string]core.Provider{"mock": mock.New(script)}}
	if agents == nil {
		agents = map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m"}}
	}
	// The tests name the tools an agent offers; the standard toolset stays out
	// unless a test asks for it.
	for name, cfg := range agents {
		if cfg.Toolset == "" {
			cfg.Toolset = core.ToolsetCustom
			agents[name] = cfg
		}
	}
	cfg := core.Config{
		Providers: map[string]core.ProviderConfig{"mock": {Kind: "openai_compat"}},
		Agents:    agents,
	}
	reg := NewRegistry(store, llm, toolReg, hooks, cfg, nil, home, ws)
	tools.NewPipeline(hooks)
	// A mid-stream step retry sleeps for real backoff in production; the tests
	// skip the wait so they stay fast (REQ-LTC-032).
	stepRetryWait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return &harness{reg: reg, store: store, info: info, hooks: hooks, tools: toolReg, llm: llm, home: home, ws: ws}
}

func (h *harness) run(t *testing.T, text string) TurnResult {
	t.Helper()
	turnID, err := h.reg.Start(context.Background(), h.info.ID, text)
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.reg.Wait(context.Background(), turnID)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (h *harness) events(t *testing.T) []core.SessionEvent {
	t.Helper()
	events, err := h.store.ChainEvents(context.Background(), h.info.ID)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func kinds(events []core.SessionEvent) []string {
	out := make([]string, len(events))
	for i, event := range events {
		out[i] = event.Kind
	}
	return out
}

func textResponse(text string) mock.Response {
	return mock.Response{Events: []mock.Event{{Text: &text}, {Stop: strptr("end_turn")}}}
}

func toolResponse(id, name, input string) mock.Response {
	return mock.Response{Events: []mock.Event{
		{ToolUse: &mock.ToolUse{ID: id, Name: name, Input: json.RawMessage(input)}},
		{Stop: strptr("tool_use")},
	}}
}

func strptr(s string) *string { return &s }

func TestTurnPlainAnswer(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("hello")}}, nil)
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "hello" || result.Err != nil {
		t.Fatalf("result=%+v", result)
	}
	got := kinds(h.events(t))
	want := []string{
		core.KindSessionCreated, core.KindTurnStart, core.KindSystemMessage, core.KindUserMessage, core.KindStepStart,
		core.KindAssistantMessage, core.KindStepEnd, core.KindTurnEnd,
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events=%v want %v", got, want)
	}
}

func TestPreStepReject(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("unused")}}, nil)
	h.hooks.AgentPreStep.Use(func(ctx context.Context, in core.PreStepInput, next core.Next[core.PreStepInput, core.PreStepDecision]) (core.PreStepDecision, error) {
		return core.PreStepDecision{Reject: true, Reason: "test"}, nil
	})
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	got := kinds(h.events(t))
	if containsKind(got, core.KindStepStart) || containsKind(got, core.KindUserMessage) {
		t.Fatalf("events=%v", got)
	}
}

func containsKind(list []string, kind string) bool {
	for _, item := range list {
		if item == kind {
			return true
		}
	}
	return false
}

func TestOneClaimPerStep(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("a"), textResponse("b"), textResponse("c")}}, nil)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	h.hooks.AgentPreStep.Use(func(ctx context.Context, in core.PreStepInput, next core.Next[core.PreStepInput, core.PreStepDecision]) (core.PreStepDecision, error) {
		once.Do(func() {
			entered <- struct{}{}
			<-release
		})
		return next(ctx, in)
	})
	turnID, err := h.reg.Start(context.Background(), h.info.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := h.reg.Send(context.Background(), h.info.ID, "second"); err != nil {
		t.Fatal(err)
	}
	if err := h.reg.Send(context.Background(), h.info.ID, "third"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if _, err := h.reg.Wait(context.Background(), turnID); err != nil {
		t.Fatal(err)
	}
	var users int
	var steps int
	events := h.events(t)
	for i, event := range events {
		if event.Kind == core.KindUserMessage {
			users++
			if i+1 >= len(events) || events[i+1].Kind != core.KindStepStart {
				t.Fatalf("user/message not followed by step/start at %d", i)
			}
		}
		if event.Kind == core.KindStepStart {
			steps++
		}
	}
	if users != 3 || steps != 3 {
		t.Fatalf("users=%d steps=%d events=%v", users, steps, kinds(events))
	}
}

func TestSendWhileRunning(t *testing.T) {
	delay := 100
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{DelayMS: &delay}, {Text: strptr("a")}, {Stop: strptr("end_turn")}}},
		textResponse("b"),
	}}, nil)
	turnID, err := h.reg.Start(context.Background(), h.info.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	if err := h.reg.Send(context.Background(), h.info.ID, "second"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("Send blocked: %v", elapsed)
	}
	if _, err := h.reg.Wait(context.Background(), turnID); err != nil {
		t.Fatal(err)
	}
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

func TestAgentRequestHook(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, nil)
	h.hooks.AgentRequest.Use(func(ctx context.Context, in core.RequestControl, next core.Next[core.RequestControl, core.RequestControl]) (core.RequestControl, error) {
		in.MaxTokens = 42
		temp := 0.5
		in.Temperature = &temp
		return next(ctx, in)
	})
	recording := &recordingProvider{}
	h.llm.providers["mock"] = recording
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	if recording.last.MaxTokens != 42 || recording.last.Temperature == nil || *recording.last.Temperature != 0.5 {
		t.Fatalf("request=%+v", recording.last)
	}
}

type recordingProvider struct {
	mu   sync.Mutex
	last core.ChatRequest
}

func (p *recordingProvider) Stream(ctx context.Context, req core.ChatRequest) (core.ChatStream, error) {
	p.mu.Lock()
	p.last = req
	p.mu.Unlock()
	text := "ok"
	stop := "end_turn"
	return func(yield func(core.ChatEvent, error) bool) {
		yield(core.ChatEvent{Kind: core.ChatTextDelta, Text: text}, nil)
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: stop}, nil)
	}, nil
}

func TestAssistantMessageBlocks(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: strptr("reading")}, {ToolUse: &mock.ToolUse{ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a.txt"}`)}}, {Stop: strptr("tool_use")}}},
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	h.info, _ = h.store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "library", Workspace: ws})
	// the new session needs the same workspace
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "done" {
		t.Fatalf("result=%+v", result)
	}
	var assistant []core.AssistantMessage
	for _, event := range h.events(t) {
		if event.Kind == core.KindAssistantMessage {
			var payload core.AssistantMessage
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			assistant = append(assistant, payload)
		}
	}
	if len(assistant) != 2 || len(assistant[0].Content) != 2 || assistant[0].Content[1].Kind != core.BlockToolUse {
		t.Fatalf("assistant=%+v", assistant)
	}
}

// REQ-LTC-040: an unset max_steps is no limit; the turn runs until the model
// stops calling tools, not to a fixed default.
func TestNoStepCapRunsUntilModelStops(t *testing.T) {
	var responses []mock.Response
	for i := 0; i < 30; i++ {
		// Each call reads a distinct line, so its result digest differs and the
		// loop guard's read-only detectors stay quiet; the test is about the
		// missing step cap, not about a looping model.
		responses = append(responses, toolResponse(fmt.Sprintf("c%d", i), "read_file", fmt.Sprintf(`{"path":"a.txt","offset":%d,"limit":1}`, i+1)))
	}
	responses = append(responses, textResponse("done"))
	h := newHarness(t, mock.Script{Responses: responses}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	var content strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&content, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "done" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 31 {
		t.Fatalf("steps=%d want 31", steps)
	}
}

// REQ-LTC-040: a configured cap runs one final report step that keeps the
// offered tools before the reason becomes max_steps (REQ-LPG-015).
func TestMaxStepsConfigured(t *testing.T) {
	var responses []mock.Response
	for i := 0; i < 3; i++ {
		responses = append(responses, toolResponse(fmt.Sprintf("c%d", i), "read_file", `{"path":"a.txt"}`))
	}
	responses = append(responses, textResponse("report"))
	h := newHarness(t, mock.Script{Responses: responses}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}, MaxSteps: 3}})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
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
}

func TestTurnStopNoListener(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, nil)
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
}

func TestTurnStopContinue(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("a"), textResponse("b")}}, nil)
	var count atomic.Int32
	h.hooks.AgentTurnStopping.Use(func(ctx context.Context, in core.TurnStopInput) (core.StopVote, error) {
		if count.Add(1) == 1 {
			return core.StopVote{Continue: true}, nil
		}
		return core.StopVote{}, nil
	})
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	var steps, users int
	for _, event := range h.events(t) {
		switch event.Kind {
		case core.KindStepStart:
			steps++
		case core.KindUserMessage:
			users++
		}
	}
	if steps != 2 || users != 1 {
		t.Fatalf("steps=%d users=%d", steps, users)
	}
}

// countSteps returns the number of step/start events of the session.
func countSteps(t *testing.T, h *harness) int {
	t.Helper()
	steps := 0
	for _, event := range h.events(t) {
		if event.Kind == core.KindStepStart {
			steps++
		}
	}
	return steps
}

// lastUserText returns the text of the session's last user/message.
func lastUserText(t *testing.T, h *harness) string {
	t.Helper()
	text := ""
	for _, event := range h.events(t) {
		if event.Kind != core.KindUserMessage {
			continue
		}
		var payload core.UserMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, 0, len(payload.Content))
		for _, block := range payload.Content {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		text = strings.Join(parts, "")
	}
	return text
}

// AC-TFB-021: a tool call followed by an empty final answer runs exactly one
// extra step with the runtime note (REQ-TFB-008 item 4).
func TestEmptyAnswerGuardRunsOneExtraStep(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "read_file", `{"path":"a.txt"}`),
		textResponse(""),
		textResponse("here is the report"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" || result.Text != "here is the report" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 3 {
		t.Fatalf("steps=%d want 3", steps)
	}
	if got := lastUserText(t, h); got != emptyAnswerNote {
		t.Fatalf("last user message=%q want %q", got, emptyAnswerNote)
	}
}

// The guard runs at most once per turn (REQ-TFB-008 item 4).
func TestEmptyAnswerGuardRunsAtMostOnce(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "read_file", `{"path":"a.txt"}`),
		textResponse(""),
		textResponse(""),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := h.run(t, "hi")
	if result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 3 {
		t.Fatalf("steps=%d want 3", steps)
	}
}

// The guard never runs after an abnormal end (REQ-TFB-008 item 4).
func TestEmptyAnswerGuardSkippedAfterError(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "read_file", `{"path":"a.txt"}`),
		{Error: &mock.ScriptError{Class: "server", Message: "boom"}},
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := h.run(t, "hi")
	if result.StopReason != "error" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 2 {
		t.Fatalf("steps=%d want 2", steps)
	}
}
