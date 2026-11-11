// Package secretreq implements the service behind the secret_request tool
// (spec 53): it checks the request, writes secret/requested and
// secret/resolved, and waits for the channel responder that shows the secure
// input card. The secret value never passes through this package: the web
// responder writes it to the secret store and returns only the outcome.
package secretreq

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// DefaultTimeout is the time an open request waits for the owner, the same as
// an ask_user question.
const DefaultTimeout = 30 * time.Minute

// Input limits of the secret_request tool (spec 53 API-SEC-001).
const (
	MaxNameRunes        = 128
	MaxDescriptionRunes = 500
	MaxLinkRunes        = 500
)

// writeTimeout bounds the closing event write on an already cancelled turn.
const writeTimeout = 5 * time.Second

// Responder shows the secure input card in the session's channel and returns
// the outcome. It never returns the value.
type Responder func(ctx context.Context, req core.SecretRequest) core.SecretOutcome

// Service implements core.SecretRequester.
type Service struct {
	Store   core.SessionStore
	Timeout time.Duration

	// exists reports whether a secret name already has a value.
	exists func(name string) bool

	mu            sync.Mutex
	responder     Responder
	channels      map[string]bool
	openBySession map[string]string
}

// New creates the service. exists reports whether a name already has a
// value; nil means no name exists.
func New(store core.SessionStore, exists func(name string) bool) *Service {
	return &Service{
		Store:         store,
		Timeout:       DefaultTimeout,
		exists:        exists,
		openBySession: map[string]string{},
	}
}

// SetResponder installs the responder and the session channels it answers.
// A session on another channel gets an error before any event is written.
func (s *Service) SetResponder(fn Responder, channels ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responder = fn
	s.channels = map[string]bool{}
	for _, channel := range channels {
		s.channels[channel] = true
	}
}

// Validate checks the tool input of spec 53 API-SEC-001. The name rule is the
// Settings > Secrets rule.
func Validate(name, description, link string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("name is required")
	}
	if utf8.RuneCountInString(name) > MaxNameRunes {
		return fmt.Errorf("name must be at most %d characters", MaxNameRunes)
	}
	if err := webfiles.ValidateSecretName(name); err != nil {
		var validation *webfiles.SecretValidationError
		if errors.As(err, &validation) {
			return fmt.Errorf("%q is not a valid secret name: %s (letters, digits and _, not starting with a digit, for example GITHUB_TOKEN)", name, validation.Message)
		}
		return err
	}
	if strings.TrimSpace(description) == "" {
		return errors.New("description is required: say why the secret is needed and where the owner gets it")
	}
	if utf8.RuneCountInString(description) > MaxDescriptionRunes {
		return fmt.Errorf("description must be at most %d characters", MaxDescriptionRunes)
	}
	if link != "" {
		if utf8.RuneCountInString(link) > MaxLinkRunes {
			return fmt.Errorf("link must be at most %d characters", MaxLinkRunes)
		}
		parsed, err := url.Parse(link)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return errors.New("link must be an http or https URL")
		}
	}
	return nil
}

// RequestSecret opens one request, waits for the responder, the timeout, or
// cancellation, writes secret/resolved in every case, and returns the outcome.
// A Telegram or other non-web session gets an error and no event.
func (s *Service) RequestSecret(ctx context.Context, req core.SecretRequest) (core.SecretOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := Validate(req.Name, req.Description, req.Link); err != nil {
		return core.SecretOutcome{}, err
	}
	info, err := s.Store.Info(ctx, req.SessionID)
	if err != nil {
		return core.SecretOutcome{}, err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	s.mu.Lock()
	responder := s.responder
	accepts := s.channels[info.Channel]
	if info.Channel == "telegram" {
		s.mu.Unlock()
		return core.SecretOutcome{}, core.ErrSecretTelegram
	}
	if responder == nil || !accepts {
		s.mu.Unlock()
		return core.SecretOutcome{}, core.ErrSecretNoWeb
	}
	if _, busy := s.openBySession[req.SessionID]; busy {
		s.mu.Unlock()
		return core.SecretOutcome{}, core.ErrSecretBusy
	}
	requestID, err := newRequestID()
	if err != nil {
		s.mu.Unlock()
		return core.SecretOutcome{}, err
	}
	s.openBySession[req.SessionID] = requestID
	s.mu.Unlock()
	defer s.release(requestID, req.SessionID)

	req.RequestID = requestID
	req.ExpiresAt = time.Now().Add(timeout).UnixMilli()
	req.Replaces = s.exists != nil && s.exists(req.Name)

	requested := core.SecretRequested{
		V:           core.PayloadVersion,
		RequestID:   requestID,
		CallID:      req.CallID,
		Name:        req.Name,
		Description: req.Description,
		Link:        req.Link,
		Replaces:    req.Replaces,
		ExpiresAt:   req.ExpiresAt,
	}
	if err := s.append(context.WithoutCancel(ctx), req.SessionID, req.TurnID, core.KindSecretRequested, requested); err != nil {
		return core.SecretOutcome{}, err
	}
	slog.Info("secret request opened", "session", req.SessionID, "request_id", requestID, "name", req.Name, "replaces", req.Replaces)

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result := make(chan core.SecretOutcome, 1)
	go func() {
		result <- callResponder(callCtx, responder, req)
	}()

	var res core.SecretOutcome
	select {
	case res = <-result:
	case <-callCtx.Done():
		// The responder sees the same deadline. Wait a moment for it, so a
		// value saved at the last moment is still reported as saved.
		select {
		case res = <-result:
		case <-time.After(writeTimeout):
		}
	}
	outcome := mapResult(ctx, callCtx, res)
	s.writeResolved(ctx, req, outcome)
	return outcome, nil
}

func callResponder(ctx context.Context, fn Responder, req core.SecretRequest) (outcome core.SecretOutcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Warn("secret responder panicked", "panic", fmt.Sprint(recovered))
			outcome = core.SecretOutcome{Status: core.SecretStatusUnavailable, Reason: "no_responder"}
		}
	}()
	return fn(ctx, req)
}

// mapResult keeps the known statuses. A cancelled turn is cancelled and a
// timed-out wait is expired, whatever the responder said.
func mapResult(parent, callCtx context.Context, res core.SecretOutcome) core.SecretOutcome {
	switch res.Status {
	case core.SecretStatusSaved:
		// A value written to the store is saved even when the wait ended at
		// the same moment.
		return core.SecretOutcome{Status: core.SecretStatusSaved}
	case core.SecretStatusCancelled, core.SecretStatusExpired:
		return core.SecretOutcome{Status: res.Status, Reason: res.Reason}
	}
	if parent.Err() != nil || callCtx.Err() != nil {
		return contextOutcome(parent)
	}
	reason := res.Reason
	if reason == "" {
		reason = "no_responder"
	}
	return core.SecretOutcome{Status: core.SecretStatusUnavailable, Reason: reason}
}

func contextOutcome(parent context.Context) core.SecretOutcome {
	if parent.Err() != nil && !errors.Is(parent.Err(), context.DeadlineExceeded) {
		return core.SecretOutcome{Status: core.SecretStatusCancelled, Reason: "turn_cancelled"}
	}
	return core.SecretOutcome{Status: core.SecretStatusExpired, Reason: "timeout"}
}

func (s *Service) writeResolved(ctx context.Context, req core.SecretRequest, outcome core.SecretOutcome) {
	resolved := core.SecretResolved{
		V:         core.PayloadVersion,
		RequestID: req.RequestID,
		CallID:    req.CallID,
		Name:      req.Name,
		Outcome:   outcome.Status,
		Reason:    outcome.Reason,
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	if err := s.append(writeCtx, req.SessionID, req.TurnID, core.KindSecretResolved, resolved); err != nil {
		slog.Error("cannot write secret/resolved", "session", req.SessionID, "request_id", req.RequestID, "error", err)
		return
	}
	slog.Info("secret request closed", "session", req.SessionID, "request_id", req.RequestID, "name", req.Name, "outcome", outcome.Status, "reason", outcome.Reason)
}

func (s *Service) append(ctx context.Context, sessionID, turnID, kind string, payload any) error {
	_, err := s.Store.Append(ctx, sessionID, []core.EventDraft{{Kind: kind, TurnID: turnID, Payload: payload}})
	return err
}

func (s *Service) release(requestID, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openBySession[sessionID] == requestID {
		delete(s.openBySession, sessionID)
	}
}

func newRequestID() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

var _ core.SecretRequester = (*Service)(nil)
