package lana

import (
	"fmt"

	"github.com/ridhomujizat/lana-harness/agent"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/router"
	// ADR 0018: the Claude Agent runtime registers "claude-code".
	_ "github.com/ridhomujizat/lana-harness/runtime/claudecode"
)

// externalHooks routes the usage and plan windows of external runtime turns
// (Claude Agent, spec 38) into the router: one call record per turn and the
// "claude-agent" limit snapshot (REQ-CCR-008).
func (rt *Runtime) externalHooks() agent.ExternalHooks {
	return agent.ExternalHooks{Usage: rt.recordExternalUsage, Limits: rt.updateExternalLimits}
}

// recordExternalUsage writes the call record of one external runtime turn
// with source "turn" (REQ-CCR-008).
func (rt *Runtime) recordExternalUsage(u agent.ExternalUsage) {
	if rt.router == nil {
		return
	}
	cost := u.CostUSD
	rec := router.CallRecord{
		At:           u.Started.UTC(),
		Source:       llmcall.SourceTurn,
		Agent:        u.Agent,
		SessionID:    u.SessionID,
		TurnID:       u.TurnID,
		Requested:    u.Provider + "/" + u.Model,
		Provider:     u.Provider,
		Model:        u.Model,
		Attempt:      1,
		Outcome:      u.Outcome,
		InputTokens:  int64(u.Usage.InputTokens),
		OutputTokens: int64(u.Usage.OutputTokens),
		CachedTokens: int64(u.Usage.CachedInputTokens),
		CostUSD:      &cost,
		DurationMs:   u.DurationMS,
		Error:        truncateRunes(u.Error, 500),
	}
	rt.router.RecordExternal(rec)
}

// updateExternalLimits stores the plan windows an external runtime reported
// as the provider's limit snapshot (REQ-CCR-008, API-RTR-013).
func (rt *Runtime) updateExternalLimits(provider, runtime string, windows []core.RuntimeLimitWindow) {
	if rt.router == nil || len(windows) == 0 {
		return
	}
	out := make([]router.Window, 0, len(windows))
	for _, w := range windows {
		win := router.Window{Type: w.Type, Status: w.Status}
		if w.Utilization != nil {
			win.Utilization = *w.Utilization
		}
		if !w.ResetsAt.IsZero() {
			resets := w.ResetsAt.UTC()
			win.ResetsAt = &resets
		}
		out = append(out, win)
	}
	rt.router.UpdateLimits(router.LimitUpdate{Provider: provider, Kind: runtime, Windows: out})
}

// scheduledRunAllowed refuses a calendar or workflow run on a runtime agent
// that does not set allow_scheduled (REQ-CCR-007, AC-CCR-008). A team is
// checked by its lead.
func (rt *Runtime) scheduledRunAllowed(agentName string) error {
	cfg, ok := rt.agentConfig(agentName)
	if !ok || cfg.Runtime == "" || cfg.AllowScheduled {
		return nil
	}
	return fmt.Errorf("claude agent '%s' does not allow scheduled runs", agentName)
}

// truncateRunes cuts s to at most n runes.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
