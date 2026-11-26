package router

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Claude Code and Antigravity targets through the router (spec 78, ADR
// 0023). With a RuntimeStreamer set, a router call whose target is a Claude
// Code or Antigravity account runs the account's official CLI headless, like
// an API provider: the proxy endpoint, avatars, and background passes use
// it. Agent turns still run in their runtimes, dispatched before the router.
// Without a streamer the targets fail with not_found, as before.

// RuntimeStreamer runs one router call on a Claude Code or Antigravity
// account (spec 78, REQ-RPR-002). The router keeps no process code: the host
// (lana) wires the external runtimes in.
type RuntimeStreamer interface {
	// StreamRuntime runs call and streams its answer: text deltas, then one
	// usage event and a stop event. A failure is a *core.ProviderError with
	// a router class (REQ-RPR-005); RetryAfter is the time to the limit's
	// reset when the CLI reported one.
	StreamRuntime(ctx context.Context, call RuntimeCall) (core.ChatStream, error)
	// RuntimeInstalled reports whether the CLI of an account is found
	// (REQ-RPR-001). It runs no process.
	RuntimeInstalled(kind, account string, cfg core.ProviderConfig) bool
}

// RuntimeCall is one router call on a Claude Code or Antigravity account.
type RuntimeCall struct {
	// Kind is core.ClaudeCodeKind or core.AntigravityKind.
	Kind string
	// Account is the account (provider) name, and Config its settings.
	Account string
	Config  core.ProviderConfig
	// Model is the target's model.
	Model string
	// Effort is the request's reasoning effort ("" or "off": the CLI's
	// default).
	Effort string
	// System is the system text (Claude Code: --system-prompt); empty for
	// Antigravity, whose Prompt holds it (REQ-RPR-003).
	System string
	// Prompt is the text the CLI reads on stdin (REQ-RPR-003).
	Prompt string
}

// RuntimeInputMessage is the error of a Claude Code or Antigravity target
// asked for tools or non-text input (REQ-RPR-003). Its class is client, but
// the chain moves on to its next target.
const RuntimeInputMessage = "tool calls and non-text input are not supported for claude-code and antigravity models"

// runtimeAnswer ends the transcript prompt of a conversation (REQ-RPR-003).
const runtimeAnswer = "Answer the last User message."

// antigravitySystemHead starts the system block of an Antigravity prompt.
const antigravitySystemHead = "System instructions:"

// WithRuntimeStreamer sets the streamer of Claude Code and Antigravity
// targets (spec 78).
func WithRuntimeStreamer(s RuntimeStreamer) Option {
	return func(o *options) { o.runtime = s }
}

// SetRuntimeStreamer sets or clears the streamer of Claude Code and
// Antigravity targets after New, for a host whose runtimes start after the
// router (spec 78). nil clears it.
func (r *Router) SetRuntimeStreamer(s RuntimeStreamer) {
	r.runtime.Store(&runtimeBox{s: s})
}

type runtimeBox struct{ s RuntimeStreamer }

// streamer returns the runtime streamer, or nil.
func (r *Router) streamer() RuntimeStreamer {
	box := r.runtime.Load()
	if box == nil {
		return nil
	}
	return box.s
}

// RuntimeServes reports whether router calls on the Claude Code or
// Antigravity account name can run (REQ-RPR-001): a streamer is set, the
// account exists and is enabled, and its CLI is found.
func (r *Router) RuntimeServes(name string) bool {
	s := r.streamer()
	if s == nil {
		return false
	}
	snap := r.snap.Load()
	kind, ok := snap.runtimeKind(name)
	if !ok {
		return false
	}
	cfg := snap.runtimeAccounts(kind)[name]
	return !cfg.Disabled && s.RuntimeInstalled(kind, name, cfg)
}

// runtimeKind returns the runtime kind of a Claude Code or Antigravity
// account.
func (s *snapshot) runtimeKind(name string) (string, bool) {
	switch {
	case s.isClaude(name):
		return core.ClaudeCodeKind, true
	case s.isAntigravity(name):
		return core.AntigravityKind, true
	}
	return "", false
}

// attemptRuntime makes one call to a Claude Code or Antigravity target
// through the runtime streamer and waits for its first event (spec 78).
func (r *Router) attemptRuntime(ctx context.Context, snap *snapshot, requested string, target Target, kind string, n int, start time.Time, req core.ChatRequest, meta CallMeta) (core.ChatStream, error) {
	streamer := r.streamer()
	if streamer == nil {
		if kind == core.ClaudeCodeKind {
			// ADR 0020: without a streamer a Claude Code target runs only in
			// the Claude Code runtime, which the agent dispatches before the
			// router.
			return nil, &core.ProviderError{Class: core.ErrorClassNotFound, Message: "claude-code targets run in the Claude Code runtime, not through the router"}
		}
		// ADR 0022, AC-AGY-016: the same for an Antigravity target.
		return nil, &core.ProviderError{Class: core.ErrorClassNotFound, Message: "antigravity targets run in the Antigravity runtime, not through the router"}
	}
	cfg := snap.runtimeAccounts(kind)[target.Provider]
	if cfg.Disabled {
		return nil, &core.ProviderError{Class: core.ErrorClassNotFound, Message: fmt.Sprintf("account %q is disabled", target.Provider)}
	}
	system, prompt, pe := runtimePrompt(kind, req)
	if pe != nil {
		return nil, pe
	}
	stream, err := streamer.StreamRuntime(ctx, RuntimeCall{
		Kind: kind, Account: target.Provider, Config: cfg, Model: target.Model,
		Effort: req.ReasoningEffort, System: system, Prompt: prompt,
	})
	if err != nil {
		return nil, err
	}
	if stream == nil {
		return nil, &core.ProviderError{Class: "server", Message: "the runtime returned no stream"}
	}
	first, pump, err := pumpStream(ctx, stream)
	if err != nil {
		return nil, err
	}
	base := r.baseRecord(requested, target, n, start, meta)
	base.TTFTMs = r.now().Sub(start).Milliseconds()
	return r.recordedStream(snap, first, pump, base, start), nil
}

// runtimePrompt maps a request to the system text and stdin prompt of a
// Claude Code or Antigravity call (REQ-RPR-003). System and developer
// messages are req.System. One user message alone is the prompt as it is;
// otherwise the prompt is the transcript, "User: …" and "Assistant: …"
// entries, ending with the last user message and "Answer the last User
// message.". Antigravity gets the system text as a leading "System
// instructions:" block of the prompt. Tools (unless tool_choice is "none"),
// tool calls and results, and images fail with RuntimeInputMessage.
func runtimePrompt(kind string, req core.ChatRequest) (string, string, *core.ProviderError) {
	inputErr := &core.ProviderError{Class: "client", Message: RuntimeInputMessage}
	switch req.ToolChoice {
	case "required", "function":
		return "", "", inputErr
	}
	if len(req.Tools) > 0 && req.ToolChoice != "none" {
		return "", "", inputErr
	}
	type entry struct {
		user bool
		text string
	}
	entries := make([]entry, 0, len(req.Messages))
	for _, msg := range req.Messages {
		var parts []string
		for _, block := range msg.Content {
			switch block.Kind {
			case core.BlockText:
				if block.Text != "" {
					parts = append(parts, block.Text)
				}
			case core.BlockThinking:
				// Earlier reasoning is not part of the prompt.
			default:
				return "", "", inputErr
			}
		}
		if len(parts) == 0 {
			continue
		}
		entries = append(entries, entry{user: msg.Role == core.RoleUser, text: strings.Join(parts, "\n")})
	}
	if len(entries) == 0 || !entries[len(entries)-1].user {
		return "", "", &core.ProviderError{Class: "client", Message: "the request has no user message to answer"}
	}
	var prompt string
	if len(entries) == 1 {
		prompt = entries[0].text
	} else {
		lines := make([]string, 0, len(entries)+1)
		for _, e := range entries {
			role := "Assistant: "
			if e.user {
				role = "User: "
			}
			lines = append(lines, role+e.text)
		}
		lines = append(lines, runtimeAnswer)
		prompt = strings.Join(lines, "\n\n")
	}
	system := strings.TrimSpace(req.System)
	if kind == core.AntigravityKind {
		if system != "" {
			prompt = antigravitySystemHead + "\n" + system + "\n\n" + prompt
		}
		return "", prompt, nil
	}
	return system, prompt, nil
}

// runtimeInputError reports whether pe is the RuntimeInputMessage error,
// which moves a chain on although its class is client (REQ-RPR-003).
func runtimeInputError(pe *core.ProviderError) bool {
	return pe.Class == "client" && pe.Message == RuntimeInputMessage
}

// poolChain expands the pooled targets of chain into one target per member
// of the pool, in pool order: a pooled Claude Code or Antigravity target
// (the built-in "claude-code" or "antigravity" account) into its accounts
// when a streamer is set (spec 75 REQ-PAF-001, REQ-RPR-005), and a pooled
// API connection address into the connections of its pool, streamer or not
// (spec 80 REQ-ACP-001). A target that an expansion would try a second time
// (same provider and model) is left out. groups[i] is the index in chain of
// the target that out[i] came from.
func (r *Router) poolChain(snap *snapshot, chain []Target) ([]Target, []int) {
	groups := make([]int, 0, len(chain))
	out := make([]Target, 0, len(chain))
	expandRuntime := r.streamer() != nil
	// seen holds every target added; pooled the ones an expansion added.
	seen := map[Target]bool{}
	pooled := map[Target]bool{}
	for i, t := range chain {
		var members []string
		switch t.Provider {
		case ClaudeCode, Antigravity:
			if expandRuntime {
				members = r.PoolAccounts(t.Provider)
			}
		default:
			members = snap.apiPool(t.Provider)
		}
		if len(members) > 0 {
			for _, name := range members {
				member := Target{Provider: name, Model: t.Model}
				if seen[member] {
					continue
				}
				seen[member], pooled[member] = true, true
				out = append(out, member)
				groups = append(groups, i)
			}
			continue
		}
		if pooled[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		groups = append(groups, i)
	}
	return out, groups
}

// coolRuntime cools a Claude Code or Antigravity target that hit its usage
// limit (REQ-RPR-005): until the reset the CLI reported (RetryAfter), else
// for the router cooldown. A router with cooldown off cools nothing.
func (r *Router) coolRuntime(snap *snapshot, target Target, pe *core.ProviderError) {
	if snap.cooldown <= 0 {
		return
	}
	d := snap.cooldown
	if pe.RetryAfter > 0 {
		d = pe.RetryAfter
	}
	r.cool.set(target, r.now().Add(d))
}

// poolMoves reports whether a failure class moves a pooled target to the next
// account of its pool (spec 75 REQ-PAF-001): not signed in, or a limit or
// quota that ran out. Other failures skip the rest of the pool.
func poolMoves(class string) bool {
	switch class {
	case "auth", "rate_limit", core.ErrorClassQuota:
		return true
	}
	return false
}

// runtimeCost is the cost of a Claude Code or Antigravity call: 0, the plan
// pays it (REQ-RPR-006).
func runtimeCost() *float64 {
	zero := 0.0
	return &zero
}
