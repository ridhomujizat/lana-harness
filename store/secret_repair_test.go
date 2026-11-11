package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Spec 53 REQ-SEC-004: an open secret request waits for input, and a resume
// closes an orphaned one as expired (reason interrupted) before turn/end.
func TestResumeRepairOrphanSecretRequest(t *testing.T) {
	s, _ := openTest(t)
	info := createSession(t, s)
	release, _ := s.TryLease(context.Background(), info.ID)
	defer func() { _ = release() }()
	if _, err := s.Append(context.Background(), info.ID, []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: "t", Payload: core.TurnStart{V: 1, TurnID: "t"}},
		{Kind: core.KindSecretRequested, TurnID: "t", Payload: core.SecretRequested{V: 1, RequestID: "s1", CallID: "c1", Name: "API_KEY", Description: "why", ExpiresAt: time.Now().Add(30 * time.Minute).UnixMilli()}},
	}); err != nil {
		t.Fatal(err)
	}
	open, err := s.Info(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if open.Status != "waiting_input" {
		t.Fatalf("status before open=%q", open.Status)
	}
	if _, err := s.Open(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	events, _ := s.loadEvents(context.Background(), info.ID)
	var resolved *core.SecretResolved
	var resolvedSeq, turnEndSeq int64
	for i := range events {
		switch events[i].Kind {
		case core.KindSecretResolved:
			var p core.SecretResolved
			if err := json.Unmarshal(events[i].Payload, &p); err != nil {
				t.Fatal(err)
			}
			resolved = &p
			resolvedSeq = events[i].Seq
		case core.KindTurnEnd:
			turnEndSeq = events[i].Seq
		case core.KindQuestionAnswered:
			t.Fatal("a secret request was repaired as a question")
		}
	}
	if resolved == nil {
		t.Fatal("orphaned secret request was not closed")
	}
	if resolved.Outcome != core.SecretStatusExpired || resolved.Reason != core.QuestionReasonInterrupted || resolved.Name != "API_KEY" || resolved.CallID != "c1" {
		t.Fatalf("resolved = %+v", resolved)
	}
	if resolvedSeq >= turnEndSeq {
		t.Fatalf("secret/resolved seq=%d is not before turn/end seq=%d", resolvedSeq, turnEndSeq)
	}
}
