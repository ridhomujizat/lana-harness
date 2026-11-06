package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// TestOpenRepairsOrphanedTurn covers a session whose turn/end was lost (the
// process died mid-turn). Open, called under the lease, closes the orphaned
// tool call with an "interrupted" result, appends an "interrupted" turn/end for
// the orphaned turn, sets the status back to idle, and broadcasts the repair
// events so live views see the turn end.
func TestOpenRepairsOrphanedTurn(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	info := createSession(t, s)
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t1", Payload: core.TurnStart{V: 1, TurnID: "t1"}},
		{Kind: core.KindToolCall, TurnID: "t1", Payload: core.ToolCallPayload{V: 1, CallID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a"}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	stuck, err := s.Info(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stuck.Status != "running" {
		t.Fatalf("status before repair=%q", stuck.Status)
	}
	hooks := core.NewHooks()
	s.Hooks = hooks
	sub := hooks.SessionEvent.Subscribe()
	defer sub.Close()

	release, err := s.TryLease(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	repaired, err := s.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Status != "idle" {
		t.Fatalf("status after repair=%q", repaired.Status)
	}
	events, err := s.loadEvents(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Fatalf("events=%d, want 5 (created, turn/start, tool/call, tool/result, turn/end)", len(events))
	}
	result, end := events[3], events[4]
	if result.Kind != core.KindToolResult || result.TurnID != "t1" {
		t.Fatalf("event 4 = %s turn %q, want tool/result of t1", result.Kind, result.TurnID)
	}
	var resultPayload core.ToolResultPayload
	if err := json.Unmarshal(result.Payload, &resultPayload); err != nil {
		t.Fatal(err)
	}
	if resultPayload.CallID != "c1" || resultPayload.Status != core.ToolStatusError || resultPayload.Output != "interrupted" {
		t.Fatalf("tool/result=%+v", resultPayload)
	}
	if end.Kind != core.KindTurnEnd || end.TurnID != "t1" {
		t.Fatalf("event 5 = %s turn %q, want turn/end of t1", end.Kind, end.TurnID)
	}
	var endPayload core.TurnEnd
	if err := json.Unmarshal(end.Payload, &endPayload); err != nil {
		t.Fatal(err)
	}
	if endPayload.Reason != "interrupted" || endPayload.TurnID != "t1" {
		t.Fatalf("turn/end=%+v", endPayload)
	}
	if repaired.LastSeq != end.Seq {
		t.Fatalf("last_seq=%d, want %d", repaired.LastSeq, end.Seq)
	}

	for _, want := range []string{core.KindToolResult, core.KindTurnEnd} {
		recvCtx, cancel := context.WithTimeout(ctx, time.Second)
		got, err := sub.Recv(recvCtx)
		cancel()
		if err != nil {
			t.Fatalf("broadcast of %s: %v", want, err)
		}
		if got.Value.Kind != want || got.Value.SessionID != info.ID {
			t.Fatalf("broadcast=%s/%s, want %s", got.Value.SessionID, got.Value.Kind, want)
		}
	}

	// A repaired session takes a new turn.
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{{Kind: core.KindTurnStart, TurnID: "t2", Payload: core.TurnStart{V: 1, TurnID: "t2"}}}); err != nil {
		t.Fatalf("new turn after repair: %v", err)
	}
}
