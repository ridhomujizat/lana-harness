package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// questionFixture is one single-select question with three options.
func questionFixture() []core.Question {
	return []core.Question{{
		Text: "Where should I save the weekly digest?",
		Options: []core.QuestionOption{
			{Label: "research/digests/", Description: "Next to the notes, one file per week."},
			{Label: "Inbox/", Description: "Your inbox folder, to sort later."},
			{Label: "Archive/"},
		},
	}}
}

func nextQuestionEvent(t *testing.T, ch <-chan questionEvent) questionEvent {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a question event")
		return questionEvent{}
	}
}

func waitQuestionOutcome(t *testing.T, ch <-chan core.QuestionOutcome) core.QuestionOutcome {
	t.Helper()
	select {
	case outcome := <-ch:
		return outcome
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a question outcome")
		return core.QuestionOutcome{}
	}
}

func decodeQuestionEvent(t *testing.T, block sseBlock) questionEvent {
	t.Helper()
	if block.Name != "question" {
		t.Fatalf("SSE block = %q, want question", block.Name)
	}
	var event questionEvent
	if err := json.Unmarshal([]byte(block.Data), &event); err != nil {
		t.Fatalf("question data %q: %v", block.Data, err)
	}
	return event
}

// askQuestionForTest opens a question on the broker in the background and
// returns its event and outcome channel.
func askQuestionForTest(t *testing.T, broker *Questions, sessionID string) (questionEvent, <-chan core.QuestionOutcome) {
	t.Helper()
	_, events, unsubscribe := broker.subscribe(sessionID)
	t.Cleanup(unsubscribe)
	outcomes := make(chan core.QuestionOutcome, 1)
	go func() {
		outcome, _ := broker.Ask(context.Background(), core.QuestionRequest{
			SessionID: sessionID,
			Questions: questionFixture(),
		})
		outcomes <- outcome
	}()
	return nextQuestionEvent(t, events), outcomes
}

// AC-ASK-019: the first accepted answer wins and a later answer is 409
// question_closed.
func TestQuestionFirstAnswerWins(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	event, outcomes := askQuestionForTest(t, s.Questions(), "session-1")

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/questions/"+event.RequestID, `{"answers":[{"question":0,"selected":[1]}]}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("first answer status = %d, body = %q", rr.Code, rr.Body.String())
	}
	outcome := waitQuestionOutcome(t, outcomes)
	if outcome.Status != core.QuestionStatusAnswered {
		t.Fatalf("first outcome = %+v", outcome)
	}
	if len(outcome.Answers) != 1 {
		t.Fatalf("answers = %+v, want one", outcome.Answers)
	}
	answer := outcome.Answers[0]
	if answer.Question != questionFixture()[0].Text || answer.Skipped || answer.Other != "" {
		t.Fatalf("answer = %+v", answer)
	}
	if len(answer.Selected) != 1 || answer.Selected[0] != "Inbox/" {
		t.Fatalf("selected = %+v, want [Inbox/]", answer.Selected)
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/questions/"+event.RequestID, `{"answers":[{"question":0,"selected":[0]}]}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "question_closed")
}

// AC-ASK-020: an out-of-range option is 422 invalid_answers and the request
// stays open.
func TestQuestionInvalidOptionKeepsRequestOpen(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	event, outcomes := askQuestionForTest(t, s.Questions(), "session-1")

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/questions/"+event.RequestID, `{"answers":[{"question":0,"selected":[9]}]}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusUnprocessableEntity, "invalid_answers")
	if !s.Questions().hasOpenQuestion("session-1") {
		t.Fatal("request closed after an invalid answer")
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/questions/"+event.RequestID, `{"answers":[{"question":0,"selected":[2]}]}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("valid answer status = %d, body = %q", rr.Code, rr.Body.String())
	}
	outcome := waitQuestionOutcome(t, outcomes)
	if outcome.Status != core.QuestionStatusAnswered || len(outcome.Answers) != 1 || outcome.Answers[0].Selected[0] != "Archive/" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// AC-ASK-033: a missing CSRF header is refused by the middleware and the
// request stays open.
func TestQuestionAnswerRequiresCSRF(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	event, outcomes := askQuestionForTest(t, s.Questions(), "session-1")

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/questions/"+event.RequestID, `{"answers":[{"question":0,"selected":[0]}]}`, cookie, ""))
	assertAuthError(t, rr, http.StatusForbidden, "csrf_invalid")
	if !s.Questions().hasOpenQuestion("session-1") {
		t.Fatal("request closed after a CSRF refusal")
	}

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/questions/"+event.RequestID, `{"answers":[{"question":0,"selected":[0]}]}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("answer status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if outcome := waitQuestionOutcome(t, outcomes); outcome.Status != core.QuestionStatusAnswered {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestQuestionMalformedBody(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	event, _ := askQuestionForTest(t, s.Questions(), "session-1")

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/questions/"+event.RequestID, `{"answers":`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")

	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/questions/"+event.RequestID, `{"answers":[{"question":0,"selected":[0]}],"unknown":true}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")

	if !s.Questions().hasOpenQuestion("session-1") {
		t.Fatal("request closed after a malformed body")
	}
}

func TestQuestionUnknownID(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/questions/never-issued", `{"answers":[{"question":0,"selected":[0]}]}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

// AC-ASK-019 (validation part): skip_all is a separate valid case and marks
// every question skipped.
func TestQuestionSkipAll(t *testing.T) {
	s, cookie, csrf := newLoggedInSessionServer(t, newFakeWebBackend())
	event, outcomes := askQuestionForTest(t, s.Questions(), "session-1")

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/questions/"+event.RequestID, `{"skip_all":true}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("skip_all status = %d, body = %q", rr.Code, rr.Body.String())
	}
	outcome := waitQuestionOutcome(t, outcomes)
	if outcome.Status != core.QuestionStatusAnswered || len(outcome.Answers) != 1 || !outcome.Answers[0].Skipped {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// A closed ID is remembered so a late answer gets 409, and the fake clock
// drives the 30-minute timeout.
func TestQuestionTimeoutUnavailable(t *testing.T) {
	broker := newQuestions()
	clock := newFakeApprovalClock()
	broker.setClock(clock)
	event, outcomes := askQuestionForTest(t, broker, "session-1")
	if want := clock.Now().Add(questionTimeout).UnixMilli(); event.ExpiresAt != want {
		t.Fatalf("expires_at = %d, want %d", event.ExpiresAt, want)
	}

	clock.Advance(questionTimeout)
	outcome := waitQuestionOutcome(t, outcomes)
	if outcome.Status != core.QuestionStatusUnavailable || outcome.Reason != core.QuestionReasonTimeout {
		t.Fatalf("timeout outcome = %+v", outcome)
	}
	_, err := broker.answer(event.RequestID, questionAnswerRequest{Answers: []questionAnswerEntry{{Question: 0, Selected: []int{0}}}})
	if !errors.Is(err, errQuestionClosed) {
		t.Fatalf("late answer error = %v, want question closed", err)
	}
}

// The SSE payload follows API-ASK-004: question, empty header, multi_select,
// and options with an omitted empty description.
func TestQuestionEventShape(t *testing.T) {
	broker := newQuestions()
	multi := []core.Question{{
		Text:        "Which files?",
		Header:      "Files",
		MultiSelect: true,
		Options: []core.QuestionOption{
			{Label: "one"},
			{Label: "two", Description: "the second"},
		},
	}}
	_, events, unsubscribe := broker.subscribe("session-1")
	defer unsubscribe()
	go func() {
		_, _ = broker.Ask(context.Background(), core.QuestionRequest{SessionID: "session-1", Questions: multi})
	}()
	event := nextQuestionEvent(t, events)
	if event.RequestID == "" {
		t.Fatal("event has no request_id")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"request_id":"` + event.RequestID + `","questions":[{"question":"Which files?","header":"Files","multi_select":true,"options":[{"label":"one"},{"label":"two","description":"the second"}]}],"expires_at":` + jsonNumber(event.ExpiresAt) + `}`
	if string(raw) != want {
		t.Fatalf("event = %s, want %s", raw, want)
	}
}

func jsonNumber(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
