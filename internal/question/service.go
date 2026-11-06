package question

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// DefaultTimeout is the time an open request may wait for an answer.
const DefaultTimeout = 30 * time.Minute

// writeTimeout bounds the closing event write on an already cancelled turn.
const writeTimeout = 5 * time.Second

// ErrBusy is returned when the session already has an open request.
var ErrBusy = errors.New("a question is already open in this session")

// Event outcome "cancelled" is not a tool status; the tool reports it as
// unavailable with reason cancelled (API-ASK-002).
const outcomeCancelled = "cancelled"

// clock supplies the current time and the request timer. It is an interface so
// tests can drive the timeout with a fake clock.
type clock interface {
	Now() time.Time
	NewTimer(time.Duration) questionTimer
}

// questionTimer is the subset of *time.Timer the service needs.
type questionTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) NewTimer(d time.Duration) questionTimer {
	return &systemTimer{timer: time.NewTimer(d)}
}

type systemTimer struct {
	timer *time.Timer
}

func (t *systemTimer) C() <-chan time.Time { return t.timer.C }
func (t *systemTimer) Stop() bool          { return t.timer.Stop() }

type hookResult struct {
	outcome core.QuestionOutcome
	err     error
}

// Service implements core.QuestionService. It writes both question events,
// enforces one open request per session, and runs the 30-minute timeout.
type Service struct {
	Store   core.SessionStore
	Hooks   *core.Hooks
	Timeout time.Duration

	clock         clock
	mu            sync.Mutex
	open          map[string]string // request ID to session ID
	openBySession map[string]string // session ID to request ID
}

// New creates the question service.
func New(store core.SessionStore, hooks *core.Hooks) *Service {
	return &Service{
		Store:         store,
		Hooks:         hooks,
		Timeout:       DefaultTimeout,
		clock:         systemClock{},
		open:          map[string]string{},
		openBySession: map[string]string{},
	}
}

// Ask opens one request, waits for the responder, the timeout, or cancellation,
// writes question/answered in every case, and returns the tool outcome.
func (s *Service) Ask(ctx context.Context, req core.QuestionRequest) (core.QuestionOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := Validate(req.Questions); err != nil {
		return core.QuestionOutcome{}, err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	s.mu.Lock()
	if _, busy := s.openBySession[req.SessionID]; busy {
		s.mu.Unlock()
		return core.QuestionOutcome{}, ErrBusy
	}
	requestID, err := newRequestID()
	if err != nil {
		s.mu.Unlock()
		return core.QuestionOutcome{}, err
	}
	req.RequestID = requestID
	req.ExpiresAt = s.clock.Now().Add(timeout).UnixMilli()
	requestTimer := s.clock.NewTimer(timeout)
	s.open[requestID] = req.SessionID
	s.openBySession[req.SessionID] = requestID
	s.mu.Unlock()
	defer s.release(requestID, req.SessionID)
	defer requestTimer.Stop()

	asked := core.QuestionAsked{
		V:         core.PayloadVersion,
		RequestID: requestID,
		CallID:    req.CallID,
		ExpiresAt: req.ExpiresAt,
	}
	if err := s.append(context.WithoutCancel(ctx), req.SessionID, req.TurnID, core.KindQuestionAsked, asked); err != nil {
		return core.QuestionOutcome{}, err
	}
	slog.Info("question opened", "session", req.SessionID, "request_id", requestID, "questions", len(req.Questions))

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result := make(chan hookResult, 1)
	go func() {
		outcome, err := s.callHook(callCtx, req)
		result <- hookResult{outcome: outcome, err: err}
	}()

	var outcome core.QuestionOutcome
	select {
	case res := <-result:
		outcome = s.mapResult(ctx, callCtx, req.Questions, res)
	case <-requestTimer.C():
		outcome = unavailable(core.QuestionReasonTimeout)
	case <-callCtx.Done():
		if ctx.Err() != nil {
			outcome = unavailable(core.QuestionReasonCancelled)
		} else {
			outcome = unavailable(core.QuestionReasonTimeout)
		}
	}

	s.writeAnswered(ctx, req, outcome)
	return outcome, nil
}

func (s *Service) callHook(ctx context.Context, req core.QuestionRequest) (outcome core.QuestionOutcome, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = core.QuestionOutcome{}
			err = fmt.Errorf("question responder panicked: %v", recovered)
		}
	}()
	if s.Hooks == nil {
		return core.QuestionOutcome{}, core.ErrNoDefault
	}
	return s.Hooks.QuestionAsk.Call(ctx, req)
}

// mapResult turns the responder result into a tool outcome. A missing,
// unknown, or inconsistent responder result is unavailable no_responder.
func (s *Service) mapResult(parent, callCtx context.Context, questions []core.Question, res hookResult) core.QuestionOutcome {
	if parent.Err() != nil {
		return unavailable(core.QuestionReasonCancelled)
	}
	if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		return unavailable(core.QuestionReasonTimeout)
	}
	if res.err != nil {
		if !errors.Is(res.err, core.ErrNoDefault) {
			slog.Warn("question responder failed", "error", res.err)
		}
		return unavailable(core.QuestionReasonNoResponder)
	}
	switch res.outcome.Status {
	case core.QuestionStatusAnswered:
		if !validAnswers(questions, res.outcome.Answers) {
			slog.Warn("question responder returned answers that do not match the request")
			return unavailable(core.QuestionReasonNoResponder)
		}
		return core.QuestionOutcome{Status: core.QuestionStatusAnswered, Answers: res.outcome.Answers}
	case core.QuestionStatusUnavailable:
		return unavailable(validReason(res.outcome.Reason))
	default:
		slog.Warn("question responder returned an unknown status", "status", res.outcome.Status)
		return unavailable(core.QuestionReasonNoResponder)
	}
}

func (s *Service) writeAnswered(ctx context.Context, req core.QuestionRequest, outcome core.QuestionOutcome) {
	responder := s.responder(ctx, req.SessionID, outcome)
	answered := core.QuestionAnswered{
		V:         core.PayloadVersion,
		RequestID: req.RequestID,
		CallID:    req.CallID,
		Outcome:   eventOutcome(outcome),
		Responder: responder,
		Reason:    outcome.Reason,
	}
	if outcome.Status == core.QuestionStatusAnswered {
		answered.Answers = outcome.Answers
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	if err := s.append(writeCtx, req.SessionID, req.TurnID, core.KindQuestionAnswered, answered); err != nil {
		slog.Error("cannot write question/answered", "session", req.SessionID, "request_id", req.RequestID, "error", err)
		return
	}
	slog.Info("question closed", "session", req.SessionID, "request_id", req.RequestID, "outcome", answered.Outcome, "responder", answered.Responder, "reason", answered.Reason)
}

// responder is the session channel that showed the request. A request with no
// responder records none.
func (s *Service) responder(ctx context.Context, sessionID string, outcome core.QuestionOutcome) string {
	if outcome.Status != core.QuestionStatusAnswered && outcome.Reason == core.QuestionReasonNoResponder {
		return "none"
	}
	info, err := s.Store.Info(context.WithoutCancel(ctx), sessionID)
	if err != nil {
		return "none"
	}
	switch info.Channel {
	case "web", "telegram", "cli":
		return info.Channel
	}
	return "none"
}

func (s *Service) append(ctx context.Context, sessionID, turnID, kind string, payload any) error {
	_, err := s.Store.Append(ctx, sessionID, []core.EventDraft{{Kind: kind, TurnID: turnID, Payload: payload}})
	return err
}

func (s *Service) release(requestID, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.open, requestID)
	if s.openBySession[sessionID] == requestID {
		delete(s.openBySession, sessionID)
	}
}

func unavailable(reason string) core.QuestionOutcome {
	return core.QuestionOutcome{Status: core.QuestionStatusUnavailable, Reason: reason}
}

func validReason(reason string) string {
	switch reason {
	case core.QuestionReasonTimeout, core.QuestionReasonNoResponder, core.QuestionReasonCancelled, core.QuestionReasonInterrupted:
		return reason
	}
	return core.QuestionReasonNoResponder
}

func eventOutcome(outcome core.QuestionOutcome) string {
	if outcome.Status == core.QuestionStatusAnswered {
		return core.QuestionStatusAnswered
	}
	if outcome.Reason == core.QuestionReasonCancelled {
		return outcomeCancelled
	}
	return core.QuestionStatusUnavailable
}

func validAnswers(questions []core.Question, answers []core.QuestionAnswer) bool {
	if len(answers) != len(questions) {
		return false
	}
	for i, question := range questions {
		answer := answers[i]
		if answer.Question != question.Text {
			return false
		}
		if answer.Skipped {
			if len(answer.Selected) > 0 || answer.Other != "" {
				return false
			}
			continue
		}
		if !question.MultiSelect && len(answer.Selected) > 1 {
			return false
		}
		for _, label := range answer.Selected {
			if !hasLabel(question.Options, label) {
				return false
			}
		}
		if len(answer.Selected) == 0 && strings.TrimSpace(answer.Other) == "" {
			return false
		}
		if !question.MultiSelect && len(answer.Selected) > 0 && answer.Other != "" {
			return false
		}
	}
	return true
}

func hasLabel(options []core.QuestionOption, label string) bool {
	for _, option := range options {
		if option.Label == label {
			return true
		}
	}
	return false
}

func newRequestID() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

var _ core.QuestionService = (*Service)(nil)
