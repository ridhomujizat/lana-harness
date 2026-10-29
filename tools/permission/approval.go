package permission

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

// ErrInvalidPolicy dikembalikan untuk nilai policy yang tidak dikenal.
var ErrInvalidPolicy = errors.New("policy tidak valid")

// ApprovalService adalah implementasi core.ApprovalService.
type ApprovalService struct {
	Store   core.SessionStore
	Hooks   *core.Hooks
	Config  core.Config
	Timeout time.Duration
}

// NewApprovalService membuat service.
func NewApprovalService(store core.SessionStore, hooks *core.Hooks, config core.Config) *ApprovalService {
	return &ApprovalService{Store: store, Hooks: hooks, Config: config, Timeout: 10 * time.Minute}
}

func (s *ApprovalService) append(ctx context.Context, sessionID, kind, turnID string, payload any) error {
	_, err := s.Store.Append(ctx, sessionID, []core.EventDraft{{Kind: kind, TurnID: turnID, Payload: payload}})
	return err
}

// Request meminta approval dan menulis audit.
func (s *ApprovalService) Request(ctx context.Context, req core.ApprovalRequest) (core.ApprovalOutcome, error) {
	requestID := id.New()
	asked := core.ApprovalAsked{V: core.PayloadVersion, RequestID: requestID, CallID: req.CallID, Tool: req.ToolName, Risk: req.Risk, Label: req.Label}
	if err := s.append(context.WithoutCancel(ctx), req.SessionID, core.KindApprovalAsked, req.TurnID, asked); err != nil {
		return core.OutcomeUnavailable, err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	outcome, err := s.ask(callCtx, req)
	cancel()
	reason := string(outcome)
	if err != nil {
		outcome = core.OutcomeUnavailable
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "timeout"
		} else if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			outcome = core.OutcomeCancelled
			reason = "cancelled"
		} else {
			reason = err.Error()
		}
	}
	if !validOutcome(outcome) {
		outcome = core.OutcomeUnavailable
		reason = "outcome tidak valid"
	}
	decided := core.ApprovalDecided{V: core.PayloadVersion, RequestID: requestID, CallID: req.CallID, Outcome: outcome, Reason: reason}
	writeCtx, writeCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer writeCancel()
	if writeErr := s.append(writeCtx, req.SessionID, core.KindApprovalDecided, req.TurnID, decided); writeErr != nil {
		return core.OutcomeUnavailable, writeErr
	}
	return outcome, nil
}

func (s *ApprovalService) ask(ctx context.Context, req core.ApprovalRequest) (outcome core.ApprovalOutcome, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = core.OutcomeUnavailable
			err = fmt.Errorf("penjawab panic: %v", recovered)
		}
	}()
	if s.Hooks == nil {
		return core.OutcomeUnavailable, nil
	}
	return s.Hooks.ApprovalRequest.Call(ctx, req)
}

func validOutcome(outcome core.ApprovalOutcome) bool {
	switch outcome {
	case core.OutcomeAllowedOnce, core.OutcomeAllowedSession, core.OutcomeRejected, core.OutcomeCancelled, core.OutcomeUnavailable:
		return true
	}
	return false
}

// SetPolicy menulis approval/policy.
func (s *ApprovalService) SetPolicy(ctx context.Context, sessionID string, p core.Policy) error {
	if !validPolicy(p) {
		return ErrInvalidPolicy
	}
	if _, err := s.Store.Info(ctx, sessionID); err != nil {
		return err
	}
	return s.append(ctx, sessionID, core.KindApprovalPolicy, "", core.ApprovalPolicy{V: core.PayloadVersion, Policy: p})
}

// EffectivePolicy membaca policy terakhir.
func (s *ApprovalService) EffectivePolicy(ctx context.Context, sessionID string) (core.Policy, error) {
	info, err := s.Store.Info(ctx, sessionID)
	if err != nil {
		return "", err
	}
	fallback := core.PolicyAsk
	if agent, ok := s.Config.Agents[info.Agent]; ok && agent.Permission != "" {
		fallback = core.Policy(agent.Permission)
	}
	events, err := s.Store.ChainEvents(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return EffectivePolicy(events, fallback), nil
}

var _ core.ApprovalService = (*ApprovalService)(nil)
