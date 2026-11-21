package main

import (
	"context"
	"reflect"
	"testing"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/web"
)

// TestWebBackendEventsIncremental checks that the incremental Events path
// returns exactly what the old whole-chain path returned: the chain entries
// with Seq > fromSeq and the chain's largest Seq, for a plain session, a forked
// session, and a cursor at or past the last seq (the short-circuit).
func TestWebBackendEventsIncremental(t *testing.T) {
	text, end := "hello", "end_turn"
	rt, home := webRuntime(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
	}})
	ctx := context.Background()
	created, err := rt.CreateChannelSession(ctx, "web", "owner", "assistant")
	if err != nil {
		t.Fatalf("CreateChannelSession: %v", err)
	}
	if _, err := rt.Run(ctx, lana.Request{Prompt: "hello", SessionID: created.ID}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	backend := newWebBackend(rt, home, nil, ctx)

	plain, err := rt.SessionInfo(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertWebEventsMatchesHistory(t, backend, plain)

	forked, err := rt.ForkSession(ctx, plain.ID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if err := rt.AppendRuntimeNote(ctx, forked.ID, "note"); err != nil {
		t.Fatalf("AppendRuntimeNote: %v", err)
	}
	forked, err = rt.SessionInfo(ctx, forked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if forked.InheritedSeq == 0 {
		t.Fatalf("fork inherited seq = 0, want > 0")
	}
	assertWebEventsMatchesHistory(t, backend, forked)

	// The short-circuit: a cursor at the last seq reads no events.
	page, err := backend.Events(ctx, plain.ID, plain.LastSeq)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if page.Events == nil || len(page.Events) != 0 {
		t.Fatalf("short-circuit events = %#v, want empty non-nil", page.Events)
	}
	if page.LastSeq != plain.LastSeq {
		t.Fatalf("short-circuit last seq = %d, want %d", page.LastSeq, plain.LastSeq)
	}
}

// assertWebEventsMatchesHistory compares webBackend.Events with the old
// semantics at every cursor: all chain entries with Seq > fromSeq, in order,
// with LastSeq the chain's largest seq (the session's LastSeq when the chain is
// empty).
func assertWebEventsMatchesHistory(t *testing.T, backend *webBackend, info core.SessionInfo) {
	t.Helper()
	ctx := context.Background()
	chain, err := backend.rt.History(ctx, info.ID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	for _, fromSeq := range []int64{0, 1, info.InheritedSeq - 1, info.InheritedSeq, info.InheritedSeq + 1, info.LastSeq - 1, info.LastSeq, info.LastSeq + 5} {
		want := web.EventPage{Events: make([]core.SessionEvent, 0, len(chain))}
		for _, event := range chain {
			if event.Seq > want.LastSeq {
				want.LastSeq = event.Seq
			}
			if event.Seq > fromSeq {
				want.Events = append(want.Events, event)
			}
		}
		if want.LastSeq == 0 {
			want.LastSeq = info.LastSeq
		}
		got, err := backend.Events(ctx, info.ID, fromSeq)
		if err != nil {
			t.Fatalf("Events(%d): %v", fromSeq, err)
		}
		if got.LastSeq != want.LastSeq {
			t.Fatalf("fromSeq %d: last seq = %d, want %d", fromSeq, got.LastSeq, want.LastSeq)
		}
		if len(got.Events) != len(want.Events) {
			t.Fatalf("fromSeq %d: %d events, want %d", fromSeq, len(got.Events), len(want.Events))
		}
		for i := range want.Events {
			if !reflect.DeepEqual(got.Events[i], want.Events[i]) {
				t.Fatalf("fromSeq %d: event %d = %+v, want %+v", fromSeq, i, got.Events[i], want.Events[i])
			}
		}
	}
}
