package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
)

// Cooldowns of an account that failed before output (spec 75, REQ-PAF-004).
const (
	// limitCooldown rests an account whose plan window or quota ran out and
	// whose reset time is unknown.
	limitCooldown = 15 * time.Minute
	// authCooldown rests an account that is not signed in.
	authCooldown = 5 * time.Minute
)

// transcriptLimit caps the conversation block of a moved runtime session
// (REQ-PAF-002), in characters.
const transcriptLimit = 12000

// poolFailure reports whether an early failure class moves a pooled turn to
// the next account of its pool (REQ-PAF-001): not signed in, or a plan window
// or quota that ran out.
func poolFailure(class string) bool {
	switch class {
	case "auth", "rate_limit", core.ErrorClassQuota:
		return true
	}
	return false
}

// cooldownUntil is when an account that failed early with err may be tried
// again (REQ-PAF-004): the reset time the runtime reported, else now plus 15
// minutes for a limit and 5 minutes for auth.
func cooldownUntil(err *core.RuntimeEarlyError, now time.Time) time.Time {
	if !err.ResetAt.IsZero() && err.ResetAt.After(now) {
		return err.ResetAt
	}
	if err.Class == "auth" {
		return now.Add(authCooldown)
	}
	return now.Add(limitCooldown)
}

// coolAccount rests account until until. A later until wins.
func (r *Registry) coolAccount(account string, until time.Time) {
	r.cooldownMu.Lock()
	defer r.cooldownMu.Unlock()
	if r.cooldowns == nil {
		r.cooldowns = map[string]time.Time{}
	}
	if until.After(r.cooldowns[account]) {
		r.cooldowns[account] = until
	}
}

// coolingDown reports whether account rests at now.
func (r *Registry) coolingDown(account string, now time.Time) bool {
	r.cooldownMu.Lock()
	defer r.cooldownMu.Unlock()
	until, ok := r.cooldowns[account]
	if !ok {
		return false
	}
	if !until.After(now) {
		delete(r.cooldowns, account)
		return false
	}
	return true
}

// AccountCooldowns returns the external runtime accounts that rest after an
// early failure, with the time each may be tried again (REQ-PAF-004,
// REQ-PAF-006 "Resting until"). Expired entries are left out. The map is a
// copy.
func (r *Registry) AccountCooldowns() map[string]time.Time {
	now := time.Now()
	r.cooldownMu.Lock()
	defer r.cooldownMu.Unlock()
	out := make(map[string]time.Time, len(r.cooldowns))
	for account, until := range r.cooldowns {
		if until.After(now) {
			out[account] = until
		}
	}
	return out
}

// stickyAccount returns the account of the session's newest runtime session
// meta of runtime: the account the last turn on that runtime ran on
// (REQ-PAF-003). An account that failed before output stores no meta.
func stickyAccount(events []core.SessionEvent, runtime string) string {
	account := ""
	for _, event := range events {
		payload, ok := runtimeMeta(event)
		if !ok || payload.Runtime != runtime {
			continue
		}
		account = metaAccountName(payload)
	}
	return account
}

// externalCandidates returns the accounts a turn on head tries, in order
// (REQ-PAF-001): the pool of a pooled address with the session's sticky
// account first (REQ-PAF-003), without the accounts that rest unless every
// one does (REQ-PAF-004); a scheduled run keeps only the accounts that allow
// scheduled runs (REQ-CCT-008, REQ-AGY-008). A pinned address is [head].
func (rn *Runner) externalCandidates(ctx context.Context, runtime string, head core.LLMTarget, cfg core.AgentConfig) []core.LLMTarget {
	pool := llmcall.Pool(rn.reg.llm, head)
	if len(pool) <= 1 {
		return pool
	}
	if events, err := rn.reg.store.ChainEvents(ctx, rn.sessionID); err != nil {
		slog.Warn("sticky account lookup failed", "session_id", rn.sessionID, "error", err)
	} else if sticky := stickyAccount(events, runtime); sticky != "" {
		for i, t := range pool {
			if t.Provider == sticky && i > 0 {
				reordered := make([]core.LLMTarget, 0, len(pool))
				reordered = append(reordered, t)
				reordered = append(reordered, pool[:i]...)
				reordered = append(reordered, pool[i+1:]...)
				pool = reordered
				break
			}
		}
	}
	if rn.reg.isScheduled(rn.sessionID) {
		allowed := make([]core.LLMTarget, 0, len(pool))
		for _, t := range pool {
			if allowScheduled(rn.reg.llm, t, cfg) {
				allowed = append(allowed, t)
			}
		}
		if len(allowed) > 0 {
			pool = allowed
		}
	}
	now := time.Now()
	ready := make([]core.LLMTarget, 0, len(pool))
	for _, t := range pool {
		if !rn.reg.coolingDown(t.Provider, now) {
			ready = append(ready, t)
		}
	}
	if len(ready) == 0 {
		// Every account rests: try them in order anyway.
		return pool
	}
	return ready
}

// poolAllowsScheduled reports whether a scheduled run may use head: one of
// the accounts it may run on allows scheduled runs (REQ-CCT-008,
// REQ-AGY-008).
func poolAllowsScheduled(llm core.LLMRegistry, head core.LLMTarget, cfg core.AgentConfig) bool {
	for _, t := range llmcall.Pool(llm, head) {
		if allowScheduled(llm, t, cfg) {
			return true
		}
	}
	return false
}

// movedContext is the block put in front of the owner message when a turn
// starts a new runtime session in a session with earlier turns (REQ-PAF-002):
// "Conversation so far (moved from <previous>):" when the previous runtime
// session belonged to another account, else "Conversation so far:", followed
// by the transcript tail. It is "" when there is nothing earlier.
func movedContext(events []core.SessionEvent, turnID, previous, account string) string {
	tail := transcriptTail(events, turnID, transcriptLimit)
	if tail == "" {
		return ""
	}
	head := "Conversation so far:"
	if previous != "" && previous != account {
		head = "Conversation so far (moved from " + previous + "):"
	}
	return head + "\n" + tail
}

// transcriptTail renders the owner and assistant text of the turns before
// turnID, one "Owner: …" or "Assistant: …" entry per message, newest last, in
// at most limit characters: the oldest whole turns are dropped first, and a
// single turn longer than limit keeps its end. Tool calls, thinking, injected
// blocks, and runtime notes are left out. No model call is made.
func transcriptTail(events []core.SessionEvent, turnID string, limit int) string {
	type turnText struct {
		id    string
		lines []string
	}
	var turns []turnText
	add := func(id, line string) {
		if len(turns) == 0 || turns[len(turns)-1].id != id {
			turns = append(turns, turnText{id: id})
		}
		turns[len(turns)-1].lines = append(turns[len(turns)-1].lines, line)
	}
	for _, event := range events {
		if event.TurnID == turnID && turnID != "" {
			continue
		}
		switch event.Kind {
		case core.KindUserMessage:
			var payload core.UserMessage
			if json.Unmarshal(event.Payload, &payload) != nil || payload.Runtime || payload.Job != nil {
				continue
			}
			blocks := payload.Content
			if payload.ContextBlocks > 0 && payload.ContextBlocks <= len(blocks) {
				blocks = blocks[payload.ContextBlocks:]
			}
			if text := blockText(blocks); text != "" {
				add(event.TurnID, "Owner: "+text)
			}
		case core.KindAssistantMessage:
			var payload core.AssistantMessage
			if json.Unmarshal(event.Payload, &payload) != nil || payload.Passed {
				continue
			}
			if text := blockText(payload.Content); text != "" {
				add(event.TurnID, "Assistant: "+text)
			}
		}
	}
	if len(turns) == 0 {
		return ""
	}
	rendered := make([]string, len(turns))
	total := 0
	for i, t := range turns {
		rendered[i] = strings.Join(t.lines, "\n")
		total += utf8.RuneCountInString(rendered[i])
	}
	total += len(rendered) - 1 // the newlines between turns
	first := 0
	for first < len(rendered)-1 && total > limit {
		total -= utf8.RuneCountInString(rendered[first]) + 1
		first++
	}
	out := strings.Join(rendered[first:], "\n")
	if n := utf8.RuneCountInString(out); n > limit {
		runes := []rune(out)
		out = string(runes[n-limit:])
	}
	return out
}

// blockText joins the text blocks of content.
func blockText(content []core.ContentBlock) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		if block.Kind != core.BlockText {
			continue
		}
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
