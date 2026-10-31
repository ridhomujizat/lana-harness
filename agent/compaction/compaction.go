// Package compaction summarizes session history.
package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
)

// ErrTurnActive and ErrNothingToCompact originate from the store package.
var (
	ErrTurnActive       = errors.New("a turn is still running")
	ErrNothingToCompact = errors.New("nothing to compact")
	ErrSessionChanged   = errors.New("session changed during compaction")
)

// Defaults of the context pressure policy (REQ-LTC-011).
const (
	defaultContextWindow = 128000
	defaultMaxTokens     = 8192
	// thinkingOutput is the output reservation of a thinking model
	// (REQ-MDL-011).
	thinkingOutput   = 32000
	defaultHeadroom  = 16384
	defaultThreshold = 0.8
	defaultRetain    = 0.16
)

// Prune budgets (REQ-LTC-012).
const (
	pruneThresholdChars = 8192
	pruneHeadChars      = 4096
	pruneTailChars      = 1024
)

// summaryMaxTokens caps the summary output.
const summaryMaxTokens = 8192

// pressureRetryEvents is how many message-producing events a session must gain
// before a throttled pressure attempt is retried (REQ-LTC-016).
const pressureRetryEvents = 8

// compactionInstruction is the final user message of a summary request. It is
// the deepseek-harness compaction-basic "Compaction instruction", with "AI
// coding assistant" changed to "AI assistant" (REQ-LTC-015).
const compactionInstruction = `You are now acting as a compaction engine for this AI assistant. Condense the conversation ABOVE into a structured checkpoint that lets another model resume the work with no loss of essential context.

Output EXACTLY the Markdown structure below: keep every section, in order. Use terse bullets, not prose paragraphs. Write "(none)" for an empty section — never drop a section.

## Primary Request and Intent
- [the user's original and evolving goals; quote verbatim where the exact wording matters]

## Key Technical Concepts
- [technologies, frameworks, patterns, and conventions in play]

## Files and Code
- [exact path: why it matters, key changes or snippets]

## Errors and Fixes
- [error: how it was resolved, plus any related user feedback]

## Pending Jobs
- [explicitly requested work not yet completed]

## Current Work
- [precisely what was in progress at this checkpoint]

## Next Step
- [the single next action, directly in line with the most recent request, or "(none)"]

## Critical Context
- [decisions and their rationale, constraints, user preferences, open questions, data needed to continue]

Rules:
- Write concise English engineering prose. Preserve exact file paths, commands, error strings, identifiers, numeric values, function signatures, and syntax fragments.
- Capture user feedback and explicit instructions faithfully, especially corrections.
- Do NOT mention this summarization request or that the context was compacted.
- Output only the checkpoint text: do not call any tool or take any other action.
- If the conversation already contains a <compacted-summary> block, it is a PRIOR checkpoint. Do not copy it forward verbatim: preserve still-true facts, drop stale ones, and merge newer information into a single consolidated summary under the same structure.`

// Service implements core.Compactor.
type Service struct {
	Store  core.SessionStore
	LLM    core.LLMRegistry
	Config core.Config
	// LookupAgent returns the live agent config so context_window and
	// compaction_* edits apply without a restart. Config is the fallback when
	// it is nil (REQ-LTC-011).
	LookupAgent func(name string) (core.AgentConfig, bool)

	mu    sync.Mutex
	locks map[string]*sync.Mutex

	// failMu guards failures, the per-session memory of a failed pressure
	// attempt (REQ-LTC-016).
	failMu   sync.Mutex
	failures map[string]failedAttempt

	// warnMu guards warned, the per-session memory of a non-positive trigger
	// warning (REQ-ECX-030).
	warnMu sync.Mutex
	warned map[string]bool
}

// failedAttempt remembers the last event seq and turn of a failed pressure
// compaction so the following steps do not repeat it too soon (REQ-LTC-016).
type failedAttempt struct {
	seq    int64
	turnID string
}

// NewService creates a Service.
func NewService(s core.SessionStore, llm core.LLMRegistry, config core.Config) *Service {
	return &Service{
		Store: s, LLM: llm, Config: config,
		locks:    map[string]*sync.Mutex{},
		failures: map[string]failedAttempt{},
		warned:   map[string]bool{},
	}
}

// sessionLock returns the per-session mutex that serializes compaction.
func (s *Service) sessionLock(sessionID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	lock, ok := s.locks[sessionID]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[sessionID] = lock
	}
	return lock
}

// throttled reports whether a recent failed pressure attempt keeps this one
// from trying again before the session has gained pressureRetryEvents more
// message-producing events or a new turn has started (REQ-LTC-016).
func (s *Service) throttled(sessionID, turnID string, events []core.SessionEvent) bool {
	s.failMu.Lock()
	mem, ok := s.failures[sessionID]
	s.failMu.Unlock()
	if !ok {
		return false
	}
	if turnID != mem.turnID {
		return false
	}
	grown := 0
	for _, event := range events {
		if event.Seq > mem.seq && messageEvent(event.Kind) {
			grown++
		}
	}
	return grown < pressureRetryEvents
}

// rememberFailure records a failed pressure attempt at the given event seq so
// the following steps do not repeat it too soon (REQ-LTC-016). The "overflow"
// and "manual" triggers are not remembered.
func (s *Service) rememberFailure(req core.CompactRequest, seq int64) {
	if req.Trigger != "pressure" {
		return
	}
	s.failMu.Lock()
	s.failures[req.SessionID] = failedAttempt{seq: seq, turnID: req.TurnID}
	s.failMu.Unlock()
}

// messageEvent reports whether an event carries model-visible content.
func messageEvent(kind string) bool {
	switch kind {
	case core.KindUserMessage, core.KindAssistantMessage, core.KindToolResult:
		return true
	}
	return false
}

// CompactNow condenses the session now, without a pressure check.
func (s *Service) CompactNow(ctx context.Context, sessionID string) (core.CompactionResult, error) {
	info, err := s.Store.Info(ctx, sessionID)
	if err != nil {
		return core.CompactionResult{}, err
	}
	if info.Status == "running" {
		return core.CompactionResult{}, ErrTurnActive
	}
	lock := s.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	result, err := s.run(ctx, core.CompactRequest{SessionID: sessionID, AgentName: info.Agent, Trigger: "manual"})
	if err != nil {
		return core.CompactionResult{}, err
	}
	if result.UptoSeq == 0 {
		return core.CompactionResult{}, ErrNothingToCompact
	}
	return result, nil
}

// CompactIfNeeded condenses the session when it is over the pressure threshold,
// or always when the trigger ignores the threshold (REQ-LTC-010, REQ-LTC-016).
func (s *Service) CompactIfNeeded(ctx context.Context, req core.CompactRequest) (core.CompactionResult, error) {
	if req.SessionID == "" {
		return core.CompactionResult{}, nil
	}
	lock := s.sessionLock(req.SessionID)
	lock.Lock()
	defer lock.Unlock()
	return s.run(ctx, req)
}

// policy is the resolved pressure policy of one request.
type policy struct {
	window    int64
	output    int64
	headroom  int64
	threshold float64
	retain    float64
}

// trigger is T = floor(min(W*threshold, W - O - headroom)) (REQ-LTC-011).
func (p policy) trigger() int64 {
	a := int64(float64(p.window) * p.threshold)
	b := p.window - p.output - p.headroom
	if b < a {
		return b
	}
	return a
}

// retained is R = floor(retain * (W - O)) (REQ-LTC-011).
func (p policy) retained() int64 {
	return int64(p.retain * float64(p.window-p.output))
}

// agentConfig returns the live config of one agent, falling back to the boot
// snapshot (REQ-LTC-011).
func (s *Service) agentConfig(name string) (core.AgentConfig, bool) {
	if s.LookupAgent != nil {
		return s.LookupAgent(name)
	}
	agent, ok := s.Config.Agents[name]
	return agent, ok
}

func (s *Service) resolvePolicy(agentName string, maxTokens int, effort string) policy {
	p := policy{window: defaultContextWindow, output: defaultMaxTokens, headroom: defaultHeadroom, threshold: defaultThreshold, retain: defaultRetain}
	modelID := ""
	agentWindow := false
	if agent, ok := s.agentConfig(agentName); ok {
		modelID = agent.Model
		if agent.ContextWindow > 0 {
			p.window = int64(agent.ContextWindow)
			agentWindow = true
		}
		if agent.CompactionHeadroom > 0 {
			p.headroom = int64(agent.CompactionHeadroom)
		}
		if agent.CompactionThreshold > 0 {
			p.threshold = agent.CompactionThreshold
		}
		if agent.CompactionRetain > 0 {
			p.retain = agent.CompactionRetain
		}
		if effort == "" {
			effort = agent.ReasoningEffort
		}
	}
	known, ok := models.Lookup(modelID)
	if modelID != "" && ok {
		// REQ-MDL-010, REQ-MDL-011: the catalog window applies when the agent
		// sets none.
		if !agentWindow && known.ContextWindow > 0 {
			p.window = int64(known.ContextWindow)
		}
		if effort == "" {
			effort = known.DefaultEffort
		}
	}
	if maxTokens > 0 {
		// An explicit request max_tokens always wins.
		p.output = int64(maxTokens)
	} else if ok && known.Thinking != "" && effort != "off" {
		// A thinking model reserves the thinking output too; "" counts as not
		// off (REQ-MDL-011).
		p.output = thinkingOutput
	}
	// REQ-ECX-030: keep the reserve proportional to the window so the pressure
	// trigger stays positive even when the output reservation alone exceeds it.
	if p.window < 4096 {
		p.window = 4096
	}
	if p.output > p.window/4 {
		p.output = p.window / 4
	}
	if p.headroom > p.window/8 {
		p.headroom = p.window / 8
	}
	return p
}

// warnTrigger logs a non-positive pressure trigger once per session
// (REQ-ECX-030). resolvePolicy caps the reserves so this should not happen; the
// warning replaces the silent skip if it ever does.
func (s *Service) warnTrigger(sessionID string, pol policy, trigger int64) {
	s.warnMu.Lock()
	defer s.warnMu.Unlock()
	if s.warned == nil {
		s.warned = map[string]bool{}
	}
	if s.warned[sessionID] {
		return
	}
	s.warned[sessionID] = true
	slog.Warn("compaction disabled: non-positive pressure trigger",
		"session_id", sessionID, "window", pol.window, "output", pol.output,
		"headroom", pol.headroom, "trigger", trigger)
}

// logSkip records a compaction that did not summarize for a reason other than
// being below pressure, so a silent skip is visible on the next investigation
// (REQ-LTC-013).
func (s *Service) logSkip(req core.CompactRequest, reason string, attrs ...any) {
	args := []any{"session_id", req.SessionID, "turn_id", req.TurnID, "trigger", req.Trigger, "reason", reason}
	slog.Info("compaction skipped", append(args, attrs...)...)
}

// run is the compaction engine. It prunes oversized tool results first (no
// model call), then summarizes the balanced span before the retained tail.
func (s *Service) run(ctx context.Context, req core.CompactRequest) (core.CompactionResult, error) {
	var zero core.CompactionResult
	info, err := s.Store.Info(ctx, req.SessionID)
	if err != nil {
		return zero, err
	}
	events, err := s.Store.ChainEvents(ctx, req.SessionID)
	if err != nil {
		return zero, err
	}
	baseline := info.LastSeq
	providerName, model := s.resolveTarget(req, info.Agent, events)
	if providerName == "" || model == "" {
		return zero, fmt.Errorf("no model found for compaction")
	}
	provider, ok := s.LLM.Provider(providerName)
	if !ok {
		return zero, fmt.Errorf("unknown provider: %s", providerName)
	}

	pol := s.resolvePolicy(req.AgentName, req.MaxTokens, req.ReasoningEffort)
	trigger := pol.trigger()
	if trigger <= 0 {
		s.warnTrigger(req.SessionID, pol, trigger)
		return zero, nil
	}
	force := req.Trigger == "overflow" || req.Trigger == "manual"
	newestOnly := force
	// A recent failed pressure attempt keeps the following steps from asking
	// the summarizer again until the session grows or a new turn starts
	// (REQ-LTC-016). "overflow" and "manual" always try.
	if !force && s.throttled(req.SessionID, req.TurnID, events) {
		s.logSkip(req, "throttled after a recent failed attempt")
		return zero, nil
	}
	record := func() {
		if len(events) > 0 {
			s.rememberFailure(req, events[len(events)-1].Seq)
		}
	}

	prunes, err := pruneIndex(events)
	if err != nil {
		return zero, err
	}
	derived, err := core.DeriveMessages(events)
	if err != nil {
		return zero, err
	}
	estimate := estimateTokens(events, req, derived, prunes)
	if !force && estimate <= trigger {
		return zero, nil // below pressure
	}

	units, err := buildUnits(events, prunes)
	if err != nil {
		return zero, err
	}
	if len(units) == 0 {
		s.logSkip(req, "no units in the derived history", "estimate_tokens", estimate, "trigger_tokens", trigger)
		return zero, nil
	}

	// Step 1: prune oversized tool results outside the retained tail.
	tailStart := tailIndex(units, pol.retained(), newestOnly)
	pruned, err := s.prune(ctx, req.SessionID, units[:tailStart])
	if err != nil {
		return zero, err
	}
	if pruned > 0 {
		if events, err = s.Store.ChainEvents(ctx, req.SessionID); err != nil {
			return zero, err
		}
		if prunes, err = pruneIndex(events); err != nil {
			return zero, err
		}
		if derived, err = core.DeriveMessages(events); err != nil {
			return zero, err
		}
		estimate = estimateTokens(events, req, derived, prunes)
		if units, err = buildUnits(events, prunes); err != nil {
			return zero, err
		}
		tailStart = tailIndex(units, pol.retained(), newestOnly)
		// Our own prune events are now part of the log; only a later change
		// makes the summary stale.
		baseline = events[len(events)-1].Seq
		if !force && estimate <= trigger {
			return zero, nil // pruning relieved the pressure
		}
	}

	// Step 2: summarize the span before the tail.
	span := units[:tailStart]
	if len(span) == 0 {
		s.logSkip(req, "no span outside the retained tail", "estimate_tokens", estimate, "trigger_tokens", trigger, "units", len(units), "tail_start", tailStart)
		return zero, nil
	}
	// A span that holds nothing but a prior summary cannot be condensed: a
	// fresh summary of the checkpoint alone would not shrink it (REQ-LTC-016).
	if onlySummary(span) {
		s.logSkip(req, "span is only the prior summary", "estimate_tokens", estimate, "trigger_tokens", trigger, "units", len(units), "tail_start", tailStart)
		return zero, nil
	}
	spanMessages := flatten(span)
	spanChars := messagesChars(spanMessages)
	if spanChars == 0 {
		s.logSkip(req, "span has no text", "estimate_tokens", estimate, "trigger_tokens", trigger, "units", len(units), "tail_start", tailStart)
		return zero, nil
	}

	if req.System == "" {
		req.System = headText(events)
	}
	messages := append(append([]core.Message(nil), spanMessages...), core.Message{
		Role:    core.RoleUser,
		Content: []core.ContentBlock{{Kind: core.BlockText, Text: compactionInstruction}},
	})
	stream, err := provider.Stream(ctx, core.ChatRequest{
		Model: model, System: req.System, Messages: messages, Tools: req.Tools, MaxTokens: summaryMaxTokens,
		ReasoningEffort: req.ReasoningEffort,
	})
	if err != nil {
		record()
		return zero, err
	}
	text, usage, err := collectSummary(stream)
	if err != nil {
		record()
		return zero, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		record()
		return zero, errors.New("summary is empty")
	}
	// A summary that does not shrink its source is rejected (REQ-LTC-015). A
	// manual compaction is exempt: /compact on a short session still condenses.
	if req.Trigger != "manual" && len(text) >= spanChars {
		record()
		return zero, errors.New("summary is not smaller than its source")
	}

	current, err := s.Store.Info(ctx, req.SessionID)
	if err != nil {
		return zero, err
	}
	if current.LastSeq != baseline {
		return zero, ErrSessionChanged
	}

	tokensBefore := estimate
	tokensAfter := int64(len(text)) / 4
	if usage.InputTokens > 0 {
		tokensBefore = usage.InputTokens
	}
	if usage.OutputTokens > 0 {
		tokensAfter = usage.OutputTokens
	}
	payload := core.CompactionSummary{
		V:               core.PayloadVersion,
		Summary:         text,
		FromSeq:         span[0].startSeq,
		UptoSeq:         span[len(span)-1].endSeq,
		RetainedFromSeq: units[tailStart].startSeq,
		TokensBefore:    tokensBefore,
		TokensAfter:     tokensAfter,
		Model:           model,
		Usage:           usage,
	}
	if _, err := s.Store.Append(ctx, req.SessionID, []core.EventDraft{{Kind: core.KindCompactionSummary, Payload: payload}}); err != nil {
		return zero, err
	}
	return core.CompactionResult{UptoSeq: payload.UptoSeq, TokensBefore: tokensBefore, TokensAfter: tokensAfter}, nil
}

// prune replaces oversized tool results of the given units with a bounded
// head, marker, and tail, and records a tool_result_pruned event for each
// (REQ-LTC-012). It returns the number of replacements written.
func (s *Service) prune(ctx context.Context, sessionID string, units []unit) (int, error) {
	var drafts []core.EventDraft
	for _, u := range units {
		for _, tool := range u.tools {
			if runeCount(tool.output) <= pruneThresholdChars {
				continue
			}
			drafts = append(drafts, core.EventDraft{
				Kind: core.KindToolResultPruned,
				Payload: core.ToolResultPruned{
					V:         core.PayloadVersion,
					CallID:    tool.callID,
					SourceSeq: tool.seq,
					Output:    pruneOutput(tool.output),
				},
			})
		}
	}
	if len(drafts) == 0 {
		return 0, nil
	}
	if _, err := s.Store.Append(ctx, sessionID, drafts); err != nil {
		return 0, err
	}
	return len(drafts), nil
}

// collectSummary reads text and usage from a summary stream, ignoring tool
// calls and reasoning.
func collectSummary(stream core.ChatStream) (string, core.Usage, error) {
	var text strings.Builder
	var usage core.Usage
	for event, err := range stream {
		if err != nil {
			return "", usage, err
		}
		switch event.Kind {
		case core.ChatTextDelta:
			text.WriteString(event.Text)
		case core.ChatBlock:
			if event.Block.Kind == core.BlockText {
				text.WriteString(event.Block.Text)
			}
		case core.ChatUsage:
			usage = event.Usage
		}
	}
	return text.String(), usage, nil
}

// pruneOutput keeps the first pruneHeadChars and last pruneTailChars runes of
// text joined by a middle marker.
func pruneOutput(text string) string {
	runes := []rune(text)
	if len(runes) <= pruneHeadChars+pruneTailChars {
		return text
	}
	head := string(runes[:pruneHeadChars])
	tail := string(runes[len(runes)-pruneTailChars:])
	middle := len(runes) - pruneHeadChars - pruneTailChars
	return head + "\n…[middle pruned: " + strconv.Itoa(middle) + " characters]…\n" + tail
}

// resolveTarget returns the provider and model of the summary request. It uses
// the request target when present, else the session's selection.
func (s *Service) resolveTarget(req core.CompactRequest, agentName string, events []core.SessionEvent) (string, string) {
	provider, model := s.resolveModel(agentName, events)
	if req.Provider != "" {
		provider = req.Provider
	}
	if req.Model != "" {
		model = req.Model
	}
	return provider, model
}

func (s *Service) resolveModel(agentName string, events []core.SessionEvent) (string, string) {
	provider, model := "", ""
	if agent, ok := s.agentConfig(agentName); ok {
		provider, model = agent.Provider, agent.Model
	}
	for _, event := range events {
		if event.Kind != core.KindModelSelection {
			continue
		}
		var payload core.ModelSelection
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		if payload.Agent != "" {
			if agent, ok := s.agentConfig(payload.Agent); ok {
				provider, model = agent.Provider, agent.Model
			}
		}
		if payload.Provider != "" {
			provider = payload.Provider
		}
		if payload.Model != "" {
			model = payload.Model
		}
	}
	return provider, model
}

// Install installs the llm.stream overflow listener. The turn loop calls
// CompactIfNeeded directly; only the context-length recovery stays on a hook.
func (s *Service) Install(hooks *core.Hooks) {
	hooks.LLMStream.Use(s.stream, core.WithPrepend())
}

func (s *Service) stream(ctx context.Context, in core.LLMStreamInput, next core.Next[core.LLMStreamInput, core.ChatStream]) (core.ChatStream, error) {
	stream, err := next(ctx, in)
	if err == nil || !errors.Is(err, core.ErrContextTooLong) {
		return stream, err
	}
	providerName := ""
	if len(in.Targets) > 0 {
		providerName = in.Targets[0].Provider
	}
	if _, compactErr := s.CompactIfNeeded(ctx, core.CompactRequest{
		SessionID: in.SessionID, TurnID: in.TurnID, AgentName: in.AgentName,
		Provider: providerName, Model: in.Request.Model, System: in.Request.System,
		Tools: in.Request.Tools, MaxTokens: in.Request.MaxTokens,
		ReasoningEffort: in.Request.ReasoningEffort, Trigger: "overflow",
	}); compactErr != nil {
		slog.Warn("compaction retry failed", "session_id", in.SessionID, "error", compactErr)
		return nil, err
	}
	events, chainErr := s.Store.ChainEvents(ctx, in.SessionID)
	if chainErr != nil {
		return nil, err
	}
	messages, deriveErr := core.DeriveMessages(events)
	if deriveErr != nil {
		return nil, err
	}
	in.Request.Messages = messages
	return next(ctx, in)
}
