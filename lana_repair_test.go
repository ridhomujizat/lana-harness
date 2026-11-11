package lana

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// newRepairRuntime builds a runtime on the SQLite store (Home is set) whose
// mock provider answers every turn with "hello" after delayMS.
func newRepairRuntime(t *testing.T, turns, delayMS int) *Runtime {
	t.Helper()
	text, stop := "hello", "end_turn"
	responses := make([]mock.Response, 0, turns)
	for range turns {
		delay := delayMS
		responses = append(responses, mock.Response{Events: []mock.Event{{DelayMS: &delay}, {Text: &text}, {Stop: &stop}}})
	}
	rt, err := New(context.Background(), Options{
		Home: t.TempDir(),
		Layers: []config.Layer{{Name: "test", TOML: []byte(`
[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.assistant]
toolset = "custom"
provider = "mock"
model = "m"
`)}},
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{Responses: responses})},
	})
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// turnEnds returns the turn/end payloads of the session in log order.
func turnEnds(t *testing.T, rt *Runtime, sessionID string) []core.TurnEnd {
	t.Helper()
	var out []core.TurnEnd
	for event, err := range rt.store.Events(context.Background(), sessionID, 1) {
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind != core.KindTurnEnd {
			continue
		}
		var end core.TurnEnd
		if err := json.Unmarshal(event.Payload, &end); err != nil {
			t.Fatal(err)
		}
		out = append(out, end)
	}
	return out
}

// TestLeaseSessionRepairsOrphanedTurn covers a session left "running" by a
// turn whose turn/end was lost (the daemon died mid-turn). Taking the lease in
// a process with no live turn for the session closes the orphaned turn, so the
// next message runs a turn instead of queueing forever.
func TestLeaseSessionRepairsOrphanedTurn(t *testing.T) {
	ctx := context.Background()
	rt := newRepairRuntime(t, 1, 0)
	info, err := rt.CreateChannelSession(ctx, "web", "owner", "assistant")
	if err != nil {
		t.Fatal(err)
	}
	// A turn that started, called a tool and never ended.
	if _, err := rt.store.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "dead", Payload: core.TurnStart{V: 1, TurnID: "dead"}},
		{Kind: core.KindToolCall, TurnID: "dead", Payload: core.ToolCallPayload{V: 1, CallID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a"}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	if stuck, _ := rt.SessionInfo(ctx, info.ID); stuck.Status != "running" {
		t.Fatalf("status before lease=%q, want running", stuck.Status)
	}

	release, err := rt.LeaseSession(ctx, info.ID)
	if err != nil {
		t.Fatalf("LeaseSession: %v", err)
	}
	defer func() { _ = release() }()

	repaired, err := rt.SessionInfo(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Status != "idle" {
		t.Fatalf("status after lease=%q, want idle", repaired.Status)
	}
	ends := turnEnds(t, rt, info.ID)
	if len(ends) != 1 || ends[0].Reason != "interrupted" || ends[0].TurnID != "dead" {
		t.Fatalf("turn/end after repair=%+v, want one interrupted end of turn dead", ends)
	}

	// The session is usable again: a new message runs a turn.
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := rt.Run(runCtx, Request{Prompt: "hi", SessionID: info.ID})
	if err != nil {
		t.Fatalf("Run after repair: %v", err)
	}
	if result.Text != "hello" || result.StopReason != StopEndTurn {
		t.Fatalf("result=%q stop=%q", result.Text, result.StopReason)
	}
}

// TestLeaseSessionKeepsLiveTurn covers the guard: a turn running in this
// process (a workflow, CLI or channel turn that holds no lease) is never
// interrupted when the web takes the session lease.
func TestLeaseSessionKeepsLiveTurn(t *testing.T) {
	ctx := context.Background()
	rt := newRepairRuntime(t, 1, 300)
	info, err := rt.CreateChannelSession(ctx, "web", "owner", "assistant")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan Result, 1)
	errs := make(chan error, 1)
	go func() {
		result, err := rt.Run(ctx, Request{Prompt: "hi", SessionID: info.ID})
		errs <- err
		done <- result
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		live, err := rt.SessionInfo(ctx, info.ID)
		if err != nil {
			t.Fatal(err)
		}
		if rt.TurnActive(info.ID) && live.Status == "running" {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("turn never reached running")
		}
		time.Sleep(5 * time.Millisecond)
	}

	release, err := rt.LeaseSession(ctx, info.ID)
	if err != nil {
		t.Fatalf("LeaseSession: %v", err)
	}
	defer func() { _ = release() }()
	if live, _ := rt.SessionInfo(ctx, info.ID); live.Status != "running" {
		t.Fatalf("status after lease=%q, want running (live turn interrupted)", live.Status)
	}
	if ends := turnEnds(t, rt, info.ID); len(ends) != 0 {
		t.Fatalf("live turn got a turn/end at lease time: %+v", ends)
	}

	if err := <-errs; err != nil {
		t.Fatalf("Run: %v", err)
	}
	result := <-done
	if result.Text != "hello" || result.StopReason != StopEndTurn {
		t.Fatalf("result=%q stop=%q", result.Text, result.StopReason)
	}
	ends := turnEnds(t, rt, info.ID)
	if len(ends) != 1 || ends[0].Reason == "interrupted" {
		t.Fatalf("turn/end=%+v, want one normal end", ends)
	}
}
