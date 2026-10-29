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
	cfg := core.Config{
		Providers: map[string]core.ProviderConfig{"mock": {Kind: "openai_compat"}},
		Agents:    agents,
	}
	reg := NewRegistry(store, llm, toolReg, hooks, cfg, nil, home, ws)
	tools.NewPipeline(hooks)
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

func TestMaxStepsDefault(t *testing.T) {
	var responses []mock.Response
	for i := 0; i < 30; i++ {
		responses = append(responses, toolResponse(fmt.Sprintf("c%d", i), "read_file", `{"path":"a.txt"}`))
	}
	h := newHarness(t, mock.Script{Responses: responses}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := h.run(t, "hi")
	if result.StopReason != "max_steps" {
		t.Fatalf("result=%+v", result)
	}
	var steps int
	for _, event := range h.events(t) {
		if event.Kind == core.KindStepStart {
			steps++
		}
	}
	if steps != 25 {
		t.Fatalf("steps=%d", steps)
	}
}

func TestMaxStepsConfigured(t *testing.T) {
	var responses []mock.Response
	for i := 0; i < 5; i++ {
		responses = append(responses, toolResponse(fmt.Sprintf("c%d", i), "read_file", `{"path":"a.txt"}`))
	}
	h := newHarness(t, mock.Script{Responses: responses}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}, MaxSteps: 3}})
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := h.run(t, "hi")
	if result.StopReason != "max_steps" {
		t.Fatalf("result=%+v", result)
	}
	var steps int
	for _, event := range h.events(t) {
		if event.Kind == core.KindStepStart {
			steps++
		}
	}
	if steps != 3 {
		t.Fatalf("steps=%d", steps)
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
