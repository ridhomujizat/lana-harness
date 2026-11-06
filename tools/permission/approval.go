package permission

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

// ErrInvalidPolicy is returned for unknown policy values.
var ErrInvalidPolicy = errors.New("invalid policy")

// ApprovalService is the implementation of core.ApprovalService.
type ApprovalService struct {
	Store   core.SessionStore
	Hooks   *core.Hooks
	Config  core.Config
	Timeout time.Duration

	// cfgMu guards Config once the service is shared. ApplyConfig swaps it
	// while tool calls read it through CurrentConfig.
	cfgMu sync.RWMutex
}

// NewApprovalService creates the service.
func NewApprovalService(store core.SessionStore, hooks *core.Hooks, config core.Config) *ApprovalService {
	return &ApprovalService{Store: store, Hooks: hooks, Config: config, Timeout: 10 * time.Minute}
}

// ApplyConfig replaces the config the service and the permission engine read.
// The runtime calls it when a config save is applied live, so a changed agent
// permission or rule takes effect on the next tool call.
func (s *ApprovalService) ApplyConfig(cfg core.Config) {
	s.cfgMu.Lock()
	s.Config = cfg
	s.cfgMu.Unlock()
}

// CurrentConfig returns the config last set by the constructor or ApplyConfig.
func (s *ApprovalService) CurrentConfig() core.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.Config
}

func (s *ApprovalService) append(ctx context.Context, sessionID, kind, turnID string, payload any) error {
	_, err := s.Store.Append(ctx, sessionID, []core.EventDraft{{Kind: kind, TurnID: turnID, Payload: payload}})
	return err
}

// Request asks for approval and writes an audit event.
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
		reason = "invalid outcome"
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
			err = fmt.Errorf("approver panicked: %v", recovered)
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

// SetPolicy writes an approval/policy event.
func (s *ApprovalService) SetPolicy(ctx context.Context, sessionID string, p core.Policy) error {
	if !validPolicy(p) {
		return ErrInvalidPolicy
	}
	if _, err := s.Store.Info(ctx, sessionID); err != nil {
		return err
	}
	return s.append(ctx, sessionID, core.KindApprovalPolicy, "", core.ApprovalPolicy{V: core.PayloadVersion, Policy: p})
}

// EffectivePolicy reads the latest policy.
func (s *ApprovalService) EffectivePolicy(ctx context.Context, sessionID string) (core.Policy, error) {
	info, err := s.Store.Info(ctx, sessionID)
	if err != nil {
		return "", err
	}
	fallback := core.PolicyAsk
	if agent, ok := s.CurrentConfig().Agents[info.Agent]; ok && agent.Permission != "" {
		fallback = core.Policy(agent.Permission)
	}
	events, err := s.Store.ChainEvents(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return EffectivePolicy(events, fallback), nil
}

var _ core.ApprovalService = (*ApprovalService)(nil)
