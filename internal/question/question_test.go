package question

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/store/memstore"
)

// fakeQuestionClock drives the 30-minute timeout without waiting.
type fakeQuestionClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeQuestionTimer
}

type fakeQuestionTimer struct {
	clock    *fakeQuestionClock
	ch       chan time.Time
	deadline time.Time
	fired    bool
	stopped  bool
}

func newFakeQuestionClock() *fakeQuestionClock {
	return &fakeQuestionClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeQuestionClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeQuestionClock) NewTimer(d time.Duration) questionTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeQuestionTimer{clock: c, ch: make(chan time.Time, 1), deadline: c.now.Add(d)}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *fakeQuestionClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, timer := range c.timers {
		if timer.stopped || timer.fired || timer.deadline.After(c.now) {
			continue
		}
		timer.fired = true
		timer.ch <- c.now
	}
}

func (t *fakeQuestionTimer) C() <-chan time.Time { return t.ch }

func (t *fakeQuestionTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	active := !t.fired && !t.stopped
	t.stopped = true
	return active
}

func newTestService(t *testing.T, channel string) (*Service, *memstore.Store, core.SessionInfo) {
	t.Helper()
	store := memstore.New()
	info, err := store.Create(context.Background(), core.NewSession{Agent: "assistant", Channel: channel, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return New(store, core.NewHooks()), store, info
}

func testQuestions() []core.Question {
	return []core.Question{
		{Text: "Where should I save the digest?", Options: []core.QuestionOption{{Label: "research/"}, {Label: "inbox/"}}},
		{Text: "Which sections?", MultiSelect: true, Options: []core.QuestionOption{{Label: "Findings"}, {Label: "Actions"}}},
		{Text: "Anything else?", Options: []core.QuestionOption{{Label: "No"}, {Label: "Yes"}}},
	}
}

func testAnswers(questions []core.Question) []core.QuestionAnswer {
	return []core.QuestionAnswer{
		{Question: questions[0].Text, Selected: []string{"research/"}},
		{Question: questions[1].Text, Selected: []string{"Findings", "Actions"}, Other: "Trend chart"},
		{Question: questions[2].Text, Skipped: true},
	}
}

func requestFor(info core.SessionInfo, questions []core.Question) core.QuestionRequest {
	return core.QuestionRequest{SessionID: info.ID, TurnID: "turn-1", CallID: "call-1", AgentName: "assistant", Questions: questions}
}

func chain(t *testing.T, store *memstore.Store, sessionID string) []core.SessionEvent {
	t.Helper()
	events, err := store.ChainEvents(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func payloads[T any](t *testing.T, events []core.SessionEvent, kind string) []T {
	t.Helper()
	var out []T
	for _, event := range events {
		if event.Kind != kind {
			continue
		}
		var value T
		if err := json.Unmarshal(event.Payload, &value); err != nil {
			t.Fatal(err)
		}
		out = append(out, value)
	}
	return out
}

func waitResult(t *testing.T, ch <-chan result) result {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(3 * time.Second):
		t.Fatal("Ask did not return")
		return result{}
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("responder was not called")
	}
}

type result struct {
	outcome core.QuestionOutcome
	err     error
}

// AC-ASK-005, AC-ASK-009, AC-ASK-018: a valid request opens, the responder
// answers three questions in order, and the log has one event pair without
// question text.
func TestServiceAnsweredWritesEventPair(t *testing.T) {
	service, store, info := newTestService(t, "web")
	questions := testQuestions()
	answers := testAnswers(questions)
	service.Hooks.QuestionAsk.Use(func(context.Context, core.QuestionRequest, core.Next[core.QuestionRequest, core.QuestionOutcome]) (core.QuestionOutcome, error) {
		return core.QuestionOutcome{Status: core.QuestionStatusAnswered, Answers: answers}, nil
	})

	outcome, err := service.Ask(context.Background(), requestFor(info, questions))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != core.QuestionStatusAnswered || len(outcome.Answers) != 3 {
		t.Fatalf("outcome=%+v", outcome)
	}
	for i, answer := range outcome.Answers {
		if answer.Question != questions[i].Text {
			t.Fatalf("answer %d question=%q, want %q", i, answer.Question, questions[i].Text)
		}
	}
	if outcome.Answers[2].Skipped != true || len(outcome.Answers[2].Selected) != 0 {
		t.Fatalf("skipped answer=%+v", outcome.Answers[2])
	}

	events := chain(t, store, info.ID)
	asked := payloads[core.QuestionAsked](t, events, core.KindQuestionAsked)
	answered := payloads[core.QuestionAnswered](t, events, core.KindQuestionAnswered)
	if len(asked) != 1 || len(answered) != 1 {
		t.Fatalf("asked=%d answered=%d events=%v", len(asked), len(answered), kinds(events))
	}
	if asked[0].RequestID == "" || asked[0].CallID != "call-1" || asked[0].ExpiresAt <= 0 {
		t.Fatalf("asked=%+v", asked[0])
	}
	for _, event := range events {
		if event.Kind != core.KindQuestionAsked {
			continue
		}
		for _, question := range questions {
			if strings.Contains(string(event.Payload), question.Text) {
				t.Fatalf("question/asked payload contains question text: %s", event.Payload)
			}
		}
	}
	if answered[0].RequestID != asked[0].RequestID || answered[0].Outcome != core.QuestionStatusAnswered {
		t.Fatalf("answered=%+v asked=%+v", answered[0], asked[0])
	}
	if answered[0].Responder != "web" {
		t.Fatalf("responder=%q", answered[0].Responder)
	}
	if len(answered[0].Answers) != 3 {
		t.Fatalf("answers=%+v", answered[0].Answers)
	}
	infoAfter, err := store.Info(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if infoAfter.Status != "running" {
		t.Fatalf("status=%q", infoAfter.Status)
	}
}

func kinds(events []core.SessionEvent) []string {
	out := make([]string, len(events))
	for i, event := range events {
		out[i] = event.Kind
	}
	return out
}

// AC-ASK-008: a second request in the same session is busy at once and writes
// no event of its own.
func TestServiceSecondRequestIsBusy(t *testing.T) {
	service, store, info := newTestService(t, "web")
	questions := testQuestions()
	entered := make(chan struct{})
	release := make(chan struct{})
	service.Hooks.QuestionAsk.Use(func(_ context.Context, req core.QuestionRequest, _ core.Next[core.QuestionRequest, core.QuestionOutcome]) (core.QuestionOutcome, error) {
		close(entered)
		<-release
		return core.QuestionOutcome{Status: core.QuestionStatusAnswered, Answers: testAnswers(req.Questions)}, nil
	})

	first := make(chan result, 1)
	go func() {
		outcome, err := service.Ask(context.Background(), requestFor(info, questions))
		first <- result{outcome: outcome, err: err}
	}()
	waitSignal(t, entered)
	waiting, err := store.Info(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != "waiting_input" {
		t.Fatalf("status while open=%q", waiting.Status)
	}

	outcome, err := service.Ask(context.Background(), requestFor(info, questions))
	if !errors.Is(err, ErrBusy) || outcome.Status != "" {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
	if got := payloads[core.QuestionAsked](t, chain(t, store, info.ID), core.KindQuestionAsked); len(got) != 1 {
		t.Fatalf("asked=%d", len(got))
	}

	close(release)
	got := waitResult(t, first)
	if got.err != nil || got.outcome.Status != core.QuestionStatusAnswered {
		t.Fatalf("first=%+v", got)
	}
	events := chain(t, store, info.ID)
	if len(payloads[core.QuestionAsked](t, events, core.KindQuestionAsked)) != 1 ||
		len(payloads[core.QuestionAnswered](t, events, core.KindQuestionAnswered)) != 1 {
		t.Fatalf("events=%v", kinds(events))
	}
}

// AC-ASK-013: the fake clock fires at 30 minutes and partial card answers are
// dropped.
func TestServiceTimeoutDropsPartialAnswers(t *testing.T) {
	service, store, info := newTestService(t, "web")
	clock := newFakeQuestionClock()
	service.clock = clock
	questions := testQuestions()
	entered := make(chan struct{})
	release := make(chan struct{})
	var partial []core.QuestionAnswer
	service.Hooks.QuestionAsk.Use(func(_ context.Context, _ core.QuestionRequest, _ core.Next[core.QuestionRequest, core.QuestionOutcome]) (core.QuestionOutcome, error) {
		partial = append(partial, core.QuestionAnswer{Question: questions[0].Text, Selected: []string{"research/"}})
		partial = append(partial, core.QuestionAnswer{Question: questions[1].Text, Skipped: true})
		close(entered)
		<-release
		return core.QuestionOutcome{Status: core.QuestionStatusAnswered, Answers: partial}, nil
	})

	done := make(chan result, 1)
	go func() {
		outcome, err := service.Ask(context.Background(), requestFor(info, questions))
		done <- result{outcome: outcome, err: err}
	}()
	waitSignal(t, entered)
	clock.Advance(DefaultTimeout)
	got := waitResult(t, done)
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.outcome.Status != core.QuestionStatusUnavailable || got.outcome.Reason != core.QuestionReasonTimeout {
		t.Fatalf("outcome=%+v", got.outcome)
	}
	if len(got.outcome.Answers) != 0 {
		t.Fatalf("partial answers returned: %+v", got.outcome.Answers)
	}
	answered := payloads[core.QuestionAnswered](t, chain(t, store, info.ID), core.KindQuestionAnswered)
	if len(answered) != 1 || answered[0].Outcome != core.QuestionStatusUnavailable ||
		answered[0].Reason != core.QuestionReasonTimeout || len(answered[0].Answers) != 0 {
		t.Fatalf("answered=%+v", answered)
	}
	close(release)
}

// AC-ASK-014: cancelling the turn closes the request with outcome cancelled.
func TestServiceCancelWritesCancelled(t *testing.T) {
	service, store, info := newTestService(t, "telegram")
	questions := testQuestions()
	entered := make(chan struct{})
	release := make(chan struct{})
	service.Hooks.QuestionAsk.Use(func(context.Context, core.QuestionRequest, core.Next[core.QuestionRequest, core.QuestionOutcome]) (core.QuestionOutcome, error) {
		close(entered)
		<-release
		return core.QuestionOutcome{Status: core.QuestionStatusUnavailable, Reason: core.QuestionReasonCancelled}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan result, 1)
	go func() {
		outcome, err := service.Ask(ctx, requestFor(info, questions))
		done <- result{outcome: outcome, err: err}
	}()
	waitSignal(t, entered)
	cancel()
	got := waitResult(t, done)
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.outcome.Status != core.QuestionStatusUnavailable || got.outcome.Reason != core.QuestionReasonCancelled {
		t.Fatalf("outcome=%+v", got.outcome)
	}
	answered := payloads[core.QuestionAnswered](t, chain(t, store, info.ID), core.KindQuestionAnswered)
	if len(answered) != 1 || answered[0].Outcome != outcomeCancelled || answered[0].Reason != core.QuestionReasonCancelled {
		t.Fatalf("answered=%+v", answered)
	}
	if answered[0].Responder != "telegram" {
		t.Fatalf("responder=%q", answered[0].Responder)
	}
	close(release)
}

// AC-ASK-012: a missing responder writes both events and returns
// unavailable no_responder without waiting.
func TestServiceNoResponder(t *testing.T) {
	service, store, info := newTestService(t, "web")
	done := make(chan result, 1)
	go func() {
		outcome, err := service.Ask(context.Background(), requestFor(info, testQuestions()))
		done <- result{outcome: outcome, err: err}
	}()
	got := waitResult(t, done)
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.outcome.Status != core.QuestionStatusUnavailable || got.outcome.Reason != core.QuestionReasonNoResponder {
		t.Fatalf("outcome=%+v", got.outcome)
	}
	events := chain(t, store, info.ID)
	if len(payloads[core.QuestionAsked](t, events, core.KindQuestionAsked)) != 1 ||
		len(payloads[core.QuestionAnswered](t, events, core.KindQuestionAnswered)) != 1 {
		t.Fatalf("events=%v", kinds(events))
	}
	answered := payloads[core.QuestionAnswered](t, events, core.KindQuestionAnswered)
	if answered[0].Responder != "none" || answered[0].Reason != core.QuestionReasonNoResponder {
		t.Fatalf("answered=%+v", answered[0])
	}
}

// AC-ASK-003, AC-ASK-004: invalid input never calls the responder and writes no
// event.
func TestServiceRejectsInvalidInput(t *testing.T) {
	four := append(testQuestions(), core.Question{Text: "Four?", Options: []core.QuestionOption{{Label: "A"}, {Label: "B"}}})
	duplicate := []core.Question{{
		Text: "Pick",
		Options: []core.QuestionOption{
			{Label: "same"}, {Label: "same"},
		},
	}}
	for name, questions := range map[string][]core.Question{"four questions": four, "duplicate labels": duplicate} {
		t.Run(name, func(t *testing.T) {
			service, store, info := newTestService(t, "web")
			calls := 0
			service.Hooks.QuestionAsk.Use(func(context.Context, core.QuestionRequest, core.Next[core.QuestionRequest, core.QuestionOutcome]) (core.QuestionOutcome, error) {
				calls++
				return core.QuestionOutcome{Status: core.QuestionStatusAnswered}, nil
			})
			if _, err := service.Ask(context.Background(), requestFor(info, questions)); err == nil {
				t.Fatal("invalid request accepted")
			}
			if calls != 0 {
				t.Fatalf("responder called %d times", calls)
			}
			events := chain(t, store, info.ID)
			if len(payloads[core.QuestionAsked](t, events, core.KindQuestionAsked)) != 0 ||
				len(payloads[core.QuestionAnswered](t, events, core.KindQuestionAnswered)) != 0 {
				t.Fatalf("events=%v", kinds(events))
			}
		})
	}
}

// A responder that panics is unavailable no_responder and still yields the
// audit pair.
func TestServiceResponderPanic(t *testing.T) {
	service, store, info := newTestService(t, "web")
	service.Hooks.QuestionAsk.Use(func(context.Context, core.QuestionRequest, core.Next[core.QuestionRequest, core.QuestionOutcome]) (core.QuestionOutcome, error) {
		panic("boom")
	})
	outcome, err := service.Ask(context.Background(), requestFor(info, testQuestions()))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != core.QuestionStatusUnavailable || outcome.Reason != core.QuestionReasonNoResponder {
		t.Fatalf("outcome=%+v", outcome)
	}
	events := chain(t, store, info.ID)
	if len(payloads[core.QuestionAsked](t, events, core.KindQuestionAsked)) != 1 ||
		len(payloads[core.QuestionAnswered](t, events, core.KindQuestionAnswered)) != 1 {
		t.Fatalf("events=%v", kinds(events))
	}
}
