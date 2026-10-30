package lana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// TestQuestionWithoutResponder covers AC-ASK-007: a runtime without AskWith
// answers no_responder at once instead of waiting for the 30-minute timeout.
func TestQuestionWithoutResponder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	rt := newTestRuntime(t, srv.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	info, err := rt.coreCtx.Sessions.Create(ctx, core.NewSession{Agent: "assistant", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	outcome, err := rt.coreCtx.Questions.Ask(ctx, core.QuestionRequest{
		SessionID: info.ID,
		Questions: []core.Question{{
			Text:    "Where should I save it?",
			Options: []core.QuestionOption{{Label: "research/"}, {Label: "inbox/"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != core.QuestionStatusUnavailable || outcome.Reason != core.QuestionReasonNoResponder {
		t.Fatalf("outcome = %+v, want unavailable no_responder", outcome)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("no_responder took %v, want at once", elapsed)
	}
}
