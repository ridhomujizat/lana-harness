package agent

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/tools"
)

// fakeCapturer records the capture requests a turn makes.
type fakeCapturer struct {
	mu   sync.Mutex
	reqs []core.CaptureRequest
}

func (f *fakeCapturer) Capture(req core.CaptureRequest) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
}

func (f *fakeCapturer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func (f *fakeCapturer) last() core.CaptureRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		return core.CaptureRequest{}
	}
	return f.reqs[len(f.reqs)-1]
}

// memoryAgent is an agent whose only listed tool is memory_write.
func memoryAgent() map[string]core.AgentConfig {
	return map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"memory_write"}}}
}

// REQ-MEM-010/011: a root turn that stored an owner message and had
// memory_write calls Capture once with the turn's prefix and specs.
func TestTurnCapturesOwnerMessage(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("hello")}}, memoryAgent())
	if err := h.tools.Register(tools.NewMemoryWrite()); err != nil {
		t.Fatal(err)
	}
	rec := &requestRecorder{inner: h.llm.providers["mock"]}
	h.llm.providers["mock"] = rec
	fake := &fakeCapturer{}
	h.reg.SetMemoryCapturer(fake)

	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	if fake.count() != 1 {
		t.Fatalf("capture calls = %d, want 1", fake.count())
	}
	req := fake.last()
	if req.SessionID != h.info.ID || req.AgentName != "assistant" || req.TurnID == "" || req.Home != h.home {
		t.Fatalf("capture request = %+v", req)
	}
	last := rec.last()
	if req.System != last.System || !reflect.DeepEqual(req.Tools, last.Tools) {
		t.Fatalf("capture prefix differs from the turn: got system=%q tools=%v want system=%q tools=%v",
			req.System, req.Tools, last.System, last.Tools)
	}
}

// REQ-MEM-010: a child session does not run a capture pass.
func TestTurnCaptureSkippedForChildSession(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("hi")}}, memoryAgent())
	if err := h.tools.Register(tools.NewMemoryWrite()); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCapturer{}
	h.reg.SetMemoryCapturer(fake)
	child, err := h.store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: "library", Workspace: h.ws, ParentSessionID: h.info.ID})
	if err != nil {
		t.Fatal(err)
	}
	h.info = child
	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	if fake.count() != 0 {
		t.Fatalf("child capture calls = %d, want 0", fake.count())
	}
}

// REQ-MEM-010: memory_capture = false skips the capture pass.
func TestTurnCaptureDisabledByConfig(t *testing.T) {
	off := false
	agents := map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"memory_write"}, MemoryCapture: &off}}
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("hi")}}, agents)
	if err := h.tools.Register(tools.NewMemoryWrite()); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCapturer{}
	h.reg.SetMemoryCapturer(fake)
	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	if fake.count() != 0 {
		t.Fatalf("capture calls = %d, want 0", fake.count())
	}
}

// REQ-MEM-010: the eligibility rules of a finished turn.
func TestCaptureEligibility(t *testing.T) {
	specs := []core.ToolSpec{{Name: "memory_write"}}
	enabled := core.AgentConfig{}
	off := false
	disabled := core.AgentConfig{MemoryCapture: &off}
	cases := []struct {
		name   string
		reason string
		child  bool
		owner  bool
		specs  []core.ToolSpec
		cfg    core.AgentConfig
		want   bool
	}{
		{"end_turn with owner and tool", "end_turn", false, true, specs, enabled, true},
		{"max_steps qualifies", "max_steps", false, true, specs, enabled, true},
		{"runtime note only is not an owner message", "end_turn", false, false, specs, enabled, false},
		{"child session skipped", "end_turn", true, true, specs, enabled, false},
		{"cancelled skipped", "cancelled", false, true, specs, enabled, false},
		{"memory_capture=false skipped", "end_turn", false, true, specs, disabled, false},
		{"without memory_write skipped", "end_turn", false, true, nil, enabled, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := captureEligible(tc.reason, tc.child, tc.owner, tc.specs, tc.cfg); got != tc.want {
				t.Fatalf("captureEligible = %v, want %v", got, tc.want)
			}
		})
	}
}
