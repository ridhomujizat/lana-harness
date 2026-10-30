package memstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func newSession(t *testing.T) (*Store, core.SessionInfo) {
	t.Helper()
	s := New()
	info, err := s.Create(context.Background(), core.NewSession{Agent: "a", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return s, info
}

func TestMemstoreSeq(t *testing.T) {
	s, info := newSession(t)
	events, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t1", Payload: core.TurnStart{V: 1, TurnID: "t1"}},
		{Kind: core.KindUserMessage, TurnID: "t1", Payload: core.UserMessage{V: 1, Content: []core.ContentBlock{{Kind: core.BlockText, Text: "hello"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Seq != 2 || events[1].Seq != 3 {
		t.Fatalf("seq=%v", events)
	}
	var got []int64
	for event, err := range s.Events(context.Background(), info.ID, 1) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, event.Seq)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("events=%v", got)
	}
	var created core.SessionCreated
	first, err := s.Info(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.LastSeq != 3 {
		t.Fatalf("lastSeq=%d", first.LastSeq)
	}
	chain, err := s.ChainEvents(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(chain[0].Payload, &created); err != nil {
		t.Fatal(err)
	}
	if created.Channel != "library" || created.Workspace == "" {
		t.Fatalf("created=%+v", created)
	}
}

func TestMemstoreQuestionWaitingProjection(t *testing.T) {
	s, info := newSession(t)
	ctx := context.Background()
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindQuestionAsked, TurnID: "t", Payload: core.QuestionAsked{V: 1, RequestID: "q1", CallID: "c1"}},
	}); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.Info(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != "waiting_input" {
		t.Fatalf("status after ask=%q", waiting.Status)
	}
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t2", Payload: core.TurnStart{V: 1, TurnID: "t2"}},
	}); !errors.Is(err, ErrTurnActive) {
		t.Fatalf("turn start while waiting err=%v", err)
	}
	if _, err := s.Append(ctx, info.ID, []core.EventDraft{
		{Kind: core.KindQuestionAnswered, TurnID: "t", Payload: core.QuestionAnswered{V: 1, RequestID: "q1", CallID: "c1", Outcome: core.QuestionStatusAnswered, Responder: "web"}},
	}); err != nil {
		t.Fatal(err)
	}
	running, err := s.Info(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != "running" {
		t.Fatalf("status after answer=%q", running.Status)
	}
}

func TestMemstoreForkUnsupported(t *testing.T) {
	s, info := newSession(t)
	if _, err := s.Fork(context.Background(), info.ID, 1); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("fork err=%v", err)
	}
	release, err := s.TryLease(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestMemstoreNotDurable(t *testing.T) {
	s, info := newSession(t)
	if _, err := s.Info(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	other := New()
	if _, err := other.Info(context.Background(), info.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("old session found in new store: %v", err)
	}
}
