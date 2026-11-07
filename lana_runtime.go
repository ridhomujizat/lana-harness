package lana

import (
	"context"
	"fmt"
	"sort"

	"github.com/ridhomujizat/lana-harness/agent"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/router"
	// ADR 0018: the Claude Agent runtime registers "claude-code".
	"github.com/ridhomujizat/lana-harness/runtime/claudecode"
)

// ClaudeCodeRuntime is the runtime key of a Claude Agent (API-CCR-001).
const ClaudeCodeRuntime = claudecode.Name

// RuntimeAgents returns the configured agents whose runtime key is runtime,
// sorted.
func (rt *Runtime) RuntimeAgents(runtime string) []string {
	names := []string{}
	if runtime == "" {
		return names
	}
	for name, cfg := range rt.agents() {
		if cfg.Runtime == runtime {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// AgentRuntimeStatus reports the health of the external runtime registered
// under name (API-CCR-003). The runtime probes its binary (for Claude Code,
// `claude --version` and `claude auth status`, cached) and never reads a
// credential. ConfiguredAgents is left for the caller.
func (rt *Runtime) AgentRuntimeStatus(ctx context.Context, name string) (core.RuntimeStatus, error) {
	if rt.registry == nil {
		return core.RuntimeStatus{}, fmt.Errorf("agent runtime %q is not available", name)
	}
	ext, err := rt.registry.Runtime(name)
	if err != nil {
		return core.RuntimeStatus{}, err
	}
	return ext.Status(ctx), nil
}

// SetAgentRuntime installs ext as the external runtime used for agents whose
// runtime key is name, in place of the registered one. Tests and embedders
// use it; the runtime is shut down with the Runtime.
func (rt *Runtime) SetAgentRuntime(name string, ext core.AgentRuntime) {
	if rt.registry != nil {
		rt.registry.SetRuntime(name, ext)
	}
}

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
	rt.router.UpdateLimits(router.LimitUpdate{Provider: provider, Kind: runtime, Windows: externalWindows(windows)})
}

// externalWindows converts runtime plan windows to router windows. A window
// without a reported utilization keeps a nil Utilization (JSON null, "usage
// not reported"), never 0.
func externalWindows(windows []core.RuntimeLimitWindow) []router.Window {
	out := make([]router.Window, 0, len(windows))
	for _, w := range windows {
		win := router.Window{Type: w.Type, Status: w.Status}
		if w.Utilization != nil {
			// Copy: the window must not alias the runtime's event. Nil stays
			// nil (not reported).
			util := *w.Utilization
			win.Utilization = &util
		}
		if !w.ResetsAt.IsZero() {
			resets := w.ResetsAt.UTC()
			win.ResetsAt = &resets
		}
		out = append(out, win)
	}
	return out
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
