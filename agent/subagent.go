package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

const (
	noSubagentsMessage   = "no subagents are configured for this agent"
	internalErrorMessage = "internal error"
	busySubagentMessage  = "subagent is busy"
	depthLimitMessage    = "maximum subagent depth reached"
	maxSubagentDepth     = 2
)

// subagentRunner runs child sessions for one turn of the calling session.
type subagentRunner struct {
	reg     *Registry
	allowed []string
}

var _ core.SubagentRunner = (*subagentRunner)(nil)

// Spawn creates a child session, runs one turn there, and returns its answer.
func (s *subagentRunner) Spawn(ctx context.Context, req core.SpawnRequest) (core.ToolOutput, error) {
	if ctx.Err() != nil {
		return stopToolOutput(ctx), nil
	}
	if len(s.allowed) == 0 {
		return toolError(noSubagentsMessage), nil
	}
	if !containsString(s.allowed, req.Agent) {
		return toolError(fmt.Sprintf("agent '%s' is not a subagent of this agent (allowed: %s)", req.Agent, strings.Join(s.allowed, ", "))), nil
	}
	parent, err := s.reg.store.Info(ctx, req.ParentSessionID)
	if err != nil {
		return toolError(internalErrorMessage), nil
	}
	if sessionDepth(ctx, s.reg.store, parent.ID) >= maxSubagentDepth {
		return toolError(depthLimitMessage), nil
	}
	if count, ok := s.reg.acquireChild(parent.ID); !ok {
		return toolError(fmt.Sprintf("%d subagents are already running", count)), nil
	}
	defer s.reg.releaseChild(parent.ID)
	child, err := s.reg.store.Create(ctx, core.NewSession{
		Agent:           req.Agent,
		Channel:         parent.Channel,
		Address:         parent.Address,
		Workspace:       parent.Workspace,
		ParentSessionID: parent.ID,
		ParentCallID:    req.ParentCallID,
	})
	if err != nil {
		return toolError(internalErrorMessage), nil
	}
	s.reg.hub.Link(parent.ID, child.ID)
	return s.runChildTurn(ctx, child, req.Prompt, time.Now())
}

// Continue sends a follow-up to an existing child session.
func (s *subagentRunner) Continue(ctx context.Context, req core.ContinueRequest) (core.ToolOutput, error) {
	if ctx.Err() != nil {
		return stopToolOutput(ctx), nil
	}
	if len(s.allowed) == 0 {
		return toolError(noSubagentsMessage), nil
	}
	if sessionDepth(ctx, s.reg.store, req.ParentSessionID) >= maxSubagentDepth {
		return toolError(depthLimitMessage), nil
	}
	child, err := s.reg.store.Info(ctx, req.SessionID)
	if err != nil || child.ParentSessionID != req.ParentSessionID {
		return toolError(fmt.Sprintf("session %s is not a subagent of this session", req.SessionID)), nil
	}
	if count, ok := s.reg.acquireChild(req.ParentSessionID); !ok {
		return toolError(fmt.Sprintf("%d subagents are already running", count)), nil
	}
	defer s.reg.releaseChild(req.ParentSessionID)
	return s.runChildTurn(ctx, child, req.Prompt, time.Now())
}

// runChildTurn starts a turn in the child, waits for it, and builds the result.
func (s *subagentRunner) runChildTurn(ctx context.Context, child core.SessionInfo, prompt string, started time.Time) (core.ToolOutput, error) {
	if ctx.Err() != nil {
		return stopToolOutput(ctx), nil
	}
	turnID, err := s.reg.startChildTurn(ctx, child.ID, prompt)
	if err != nil {
		if errors.Is(err, ErrSessionBusy) {
			return toolError(busySubagentMessage), nil
		}
		return toolError(internalErrorMessage), nil
	}
	result, err := s.reg.Wait(ctx, turnID)
	if err != nil {
		if ctx.Err() != nil {
			return stopToolOutput(ctx), nil
		}
		return toolError(internalErrorMessage), nil
	}
	// The parent context ending cancels the child turn; a cancel of this child
	// alone ends its turn as cancelled. A shutdown interrupt surfaces as an
	// interrupted task call (REQ-TFB-001).
	if ctx.Err() != nil || result.StopReason == "cancelled" || result.StopReason == "interrupted" {
		out := stopToolOutput(ctx)
		if result.StopReason == "interrupted" {
			out.Output = "interrupted"
		}
		// A cancelled child still reports the usage of the part of its turn
		// that ran, so the parent step keeps that cost (REQ-TEAM-013).
		out.Usage = &result.Usage
		return out, nil
	}
	return s.resultOutput(ctx, child, result, started), nil
}

// resultOutput builds the JSON output shared by task and task_continue.
func (s *subagentRunner) resultOutput(ctx context.Context, child core.SessionInfo, result TurnResult, started time.Time) core.ToolOutput {
	events, err := s.reg.store.ChainEvents(ctx, child.ID)
	if err != nil {
		return toolError(internalErrorMessage)
	}
	steps := 0
	toolCalls := 0
	for _, event := range events {
		if event.TurnID != result.TurnID {
			continue
		}
		switch event.Kind {
		case core.KindStepStart:
			steps++
		case core.KindToolCall:
			toolCalls++
		}
	}
	payload := taskResult{
		SessionID:  child.ID,
		Agent:      child.Agent,
		Status:     result.StopReason,
		Answer:     result.Text,
		Steps:      steps,
		ToolCalls:  toolCalls,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if result.Err != nil {
		payload.Error = result.Err.Message
	}
	output, err := json.Marshal(payload)
	if err != nil {
		return toolError(internalErrorMessage)
	}
	// Usage is the usage of the turns this call ran: the one child turn of
	// Spawn, or the one child turn of this task_continue call. It is not
	// cumulative across earlier calls. The parent step adds it to its own
	// usage (REQ-TEAM-013, AC-TEAM-031).
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: string(output), Usage: &result.Usage}
}

// taskResult is the JSON output of the task tool.
type taskResult struct {
	SessionID  string `json:"session_id"`
	Agent      string `json:"agent"`
	Status     string `json:"status"`
	Answer     string `json:"answer"`
	Error      string `json:"error,omitempty"`
	Steps      int    `json:"steps"`
	ToolCalls  int    `json:"tool_calls"`
	DurationMS int64  `json:"duration_ms"`
}

func toolError(message string) core.ToolOutput {
	return core.ToolOutput{Status: core.ToolStatusError, Output: message}
}

// startChildTurn starts a turn on an existing session from the parent turn
// context.
func (r *Registry) startChildTurn(parent context.Context, sessionID, text string) (string, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return "", ErrClosed
	}
	runner := r.runnerLocked(sessionID)
	runner.mu.Lock()
	if runner.running {
		runner.mu.Unlock()
		r.mu.Unlock()
		return "", ErrSessionBusy
	}
	turnID, turnCtx := r.startTurnFrom(parent, runner)
	r.wg.Add(1)
	runner.mu.Unlock()
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		runner.runChain(turnCtx, text, turnID)
	}()
	return turnID, nil
}

// sessionDepth returns the number of parent links of a session, up to the
// maximum delegation depth.
func sessionDepth(ctx context.Context, store core.SessionStore, sessionID string) int {
	depth := 0
	for depth < maxSubagentDepth {
		info, err := store.Info(ctx, sessionID)
		if err != nil || info.ParentSessionID == "" {
			break
		}
		depth++
		sessionID = info.ParentSessionID
	}
	return depth
}
