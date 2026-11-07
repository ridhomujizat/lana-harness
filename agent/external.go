package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/internal/spill"
)

// ExternalUsage is the usage of one turn of an external agent runtime such as
// Claude Code (ADR 0018, REQ-CCR-008). The root package turns it into one
// router call record.
type ExternalUsage struct {
	// Runtime is the agent's runtime key, for example "claude-code".
	Runtime string
	// Provider is the call record provider, for example "claude-agent".
	Provider  string
	Agent     string
	SessionID string
	TurnID    string
	// Model is the model the runtime reported, else the configured one.
	Model string
	Usage core.Usage
	// CostUSD is the cost the runtime reported for the turn.
	CostUSD float64
	// Started is when the turn reached the runtime.
	Started time.Time
	// DurationMS is the runtime's own turn duration, else the wall time.
	DurationMS int64
	// Outcome is "ok", "cancelled", or the error class ("runtime").
	Outcome string
	// Error is the turn error text; empty when Outcome is "ok".
	Error string
}

// ExternalHooks report what external runtime turns produce to the host, so
// the agent package needs no router import (REQ-CCR-008). Both are called
// from the turn goroutine and must not block.
type ExternalHooks struct {
	// Usage receives one record per turn that reached the runtime.
	Usage func(ExternalUsage)
	// Limits receives the plan windows a runtime reported, with the call
	// record provider ("claude-agent") and the runtime key ("claude-code").
	Limits func(provider, runtime string, windows []core.RuntimeLimitWindow)
}

// SetExternalHooks installs the usage and limits hooks of external runtime
// turns. The root package sets them at boot.
func (r *Registry) SetExternalHooks(h ExternalHooks) {
	r.runtimeMu.Lock()
	r.externalHooks = h
	r.runtimeMu.Unlock()
}

func (r *Registry) hooksExternal() ExternalHooks {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	return r.externalHooks
}

// SetRuntime installs the runtime used for agents whose runtime key is name,
// in place of the one core.LookupRuntime would build. Tests and embedders use
// it; a runtime set here is shut down by Close like a built one.
func (r *Registry) SetRuntime(name string, rt core.AgentRuntime) {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	if r.runtimes == nil {
		r.runtimes = map[string]core.AgentRuntime{}
	}
	r.runtimes[name] = rt
}

// Runtime returns the external agent runtime registered under name (ADR
// 0018), building it once on first use through core.LookupRuntime.
func (r *Registry) Runtime(name string) (core.AgentRuntime, error) {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	if rt, ok := r.runtimes[name]; ok {
		return rt, nil
	}
	if r.runtimesClosed {
		return nil, ErrClosed
	}
	factory, ok := core.LookupRuntime(name)
	if !ok {
		return nil, fmt.Errorf("agent runtime %q is not available", name)
	}
	rt, err := factory(core.RuntimeDeps{Secrets: r.secrets, Logger: slog.Default()})
	if err != nil {
		return nil, fmt.Errorf("agent runtime %q: %w", name, err)
	}
	if r.runtimes == nil {
		r.runtimes = map[string]core.AgentRuntime{}
	}
	r.runtimes[name] = rt
	return rt, nil
}

// shutdownRuntimes stops every external runtime process. Close calls it after
// the turns stopped.
func (r *Registry) shutdownRuntimes(ctx context.Context) {
	r.runtimeMu.Lock()
	r.runtimesClosed = true
	runtimes := make([]core.AgentRuntime, 0, len(r.runtimes))
	for _, rt := range r.runtimes {
		runtimes = append(runtimes, rt)
	}
	r.runtimeMu.Unlock()
	for _, rt := range runtimes {
		if err := rt.Shutdown(ctx); err != nil {
			slog.Warn("agent runtime shutdown", "error", err)
		}
	}
}

// hasRuntimeAgents reports whether any configured agent runs on an external
// runtime, so native turns skip the external lookup otherwise.
func hasRuntimeAgents(agents map[string]core.AgentConfig) bool {
	for _, cfg := range agents {
		if cfg.Runtime != "" {
			return true
		}
	}
	return false
}

// externalAgent is the resolved runtime agent of a session.
type externalAgent struct {
	name      string
	cfg       core.AgentConfig
	workspace string
	effort    string
	child     bool
}

// externalAgent resolves the session's agent and reports whether it runs on an
// external runtime. Any lookup error reports false; the native turn then
// reports it.
func (rn *Runner) externalAgent(ctx context.Context) (externalAgent, bool) {
	agents, teams := rn.reg.configSnapshot()
	if !hasRuntimeAgents(agents) {
		return externalAgent{}, false
	}
	info, err := rn.reg.store.Info(ctx, rn.sessionID)
	if err != nil {
		return externalAgent{}, false
	}
	chain, err := rn.reg.store.ChainEvents(ctx, rn.sessionID)
	if err != nil {
		return externalAgent{}, false
	}
	agentName := info.Agent
	effort := ""
	for _, event := range chain {
		if event.Kind != core.KindModelSelection {
			continue
		}
		var payload core.ModelSelection
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		if payload.Agent != "" {
			agentName = payload.Agent
		}
		effort = payload.ReasoningEffort
	}
	cfgName := agentName
	if team, ok := teams[agentName]; ok {
		cfgName = team.Lead
	}
	cfg, ok := agents[cfgName]
	if !ok || cfg.Runtime == "" {
		return externalAgent{}, false
	}
	workspace := info.Workspace
	if workspace == "" {
		workspace = cfg.Workspace
	}
	if workspace == "" {
		workspace = rn.reg.defaultWS
	}
	if normalized, err := normalizePath(workspace); err == nil {
		workspace = normalized
	}
	// The session's reasoning effort wins over the agent's; "off" has no
	// --effort value, so it leaves the agent's (API-CCR-001).
	if effort == "" || effort == "off" {
		effort = cfg.ReasoningEffort
	}
	return externalAgent{name: cfgName, cfg: cfg, workspace: workspace, effort: effort, child: info.ParentSessionID != ""}, true
}

// latestRuntimeMeta returns the newest runtime session meta of the session for
// runtime, the system/message of subtype "runtime" (API-CCR-002).
func latestRuntimeMeta(events []core.SessionEvent, runtime string) (core.SystemMessage, bool) {
	var meta core.SystemMessage
	found := false
	for _, event := range events {
		if event.Kind != core.KindSystemMessage {
			continue
		}
		var payload core.SystemMessage
		if json.Unmarshal(event.Payload, &payload) != nil || payload.Subtype != core.SystemSubtypeRuntime {
			continue
		}
		if payload.Runtime != runtime || payload.ClaudeSessionID == "" {
			continue
		}
		meta, found = payload, true
	}
	return meta, found
}

// externalTurn is the state of one external runtime turn while its events are
// mirrored into the session log.
type externalTurn struct {
	rn       *Runner
	events   *Events
	writeCtx context.Context
	turnID   string
	agent    externalAgent
	provider string
	model    string

	started     time.Time
	stepStarted bool
	// pendingText is the last assistant text not yet stored. A tool call that
	// follows joins it into one assistant/message, as a native step does.
	pendingText string
	thinking    strings.Builder
	lastText    string
	toolNames   map[string]string

	usage      core.Usage
	usageSeen  bool
	costUSD    float64
	durationMS int64
	usageModel string

	end      core.RuntimeEvent
	ended    bool
	writeErr error
}

func (x *externalTurn) append(kind string, payload any) {
	if _, err := x.events.Append(x.writeCtx, core.EventDraft{Kind: kind, TurnID: x.turnID, Payload: payload}); err != nil {
		slog.Error("failed to write external turn event", "session_id", x.rn.sessionID, "turn_id", x.turnID, "kind", kind, "error", err)
		if x.writeErr == nil {
			x.writeErr = err
		}
	}
}

func (x *externalTurn) publish(frame core.LiveFrame) {
	frame.SessionID = x.rn.sessionID
	frame.TurnID = x.turnID
	x.rn.reg.hub.Publish(x.rn.sessionID, frame)
}

// ensureStep writes the turn's one step/start before its first content event.
// It waits for the session meta so the step names the model the runtime runs.
func (x *externalTurn) ensureStep() {
	if x.stepStarted {
		return
	}
	x.stepStarted = true
	x.started = time.Now()
	x.append(core.KindStepStart, core.StepStart{V: core.PayloadVersion, Step: 1, Provider: x.provider, Model: x.model})
}

// assistantContent builds the content of one assistant/message: the thinking
// gathered since the last message, the text, then the tool call.
func (x *externalTurn) assistantContent(text string, tool *core.ContentBlock) []core.ContentBlock {
	content := make([]core.ContentBlock, 0, 3)
	if x.thinking.Len() > 0 {
		content = append(content, core.ContentBlock{Kind: core.BlockThinking, Text: x.thinking.String()})
		x.thinking.Reset()
	}
	if text != "" {
		content = append(content, core.ContentBlock{Kind: core.BlockText, Text: text})
	}
	if tool != nil {
		content = append(content, *tool)
	}
	return content
}

// flushText stores the pending assistant text as its own message.
func (x *externalTurn) flushText() {
	if x.pendingText == "" {
		return
	}
	text := x.pendingText
	x.pendingText = ""
	x.append(core.KindAssistantMessage, core.AssistantMessage{V: core.PayloadVersion, Content: x.assistantContent(text, nil), StopReason: "end_turn"})
}

// handle mirrors one runtime event. The runtime calls it from one goroutine
// at a time.
func (x *externalTurn) handle(ev core.RuntimeEvent) {
	switch ev.Kind {
	case core.RuntimeSessionMeta:
		if ev.Model != "" {
			x.model = ev.Model
		}
		if ev.ClaudeSessionID != "" {
			x.append(core.KindSystemMessage, core.SystemMessage{
				V: core.PayloadVersion, Subtype: core.SystemSubtypeRuntime, Runtime: x.agent.cfg.Runtime,
				ClaudeSessionID: ev.ClaudeSessionID, Model: ev.Model, Tools: ev.Tools, Version: ev.Version,
			})
		}
		return
	case core.RuntimeEnd:
		x.flushText()
		x.end, x.ended = ev, true
		return
	case core.RuntimeLimits:
		if h := x.rn.reg.hooksExternal(); h.Limits != nil && len(ev.Windows) > 0 {
			h.Limits(llmcall.RuntimeProvider(x.agent.cfg.Runtime), x.agent.cfg.Runtime, ev.Windows)
		}
		return
	}
	x.ensureStep()
	switch ev.Kind {
	case core.RuntimeTextDelta:
		x.publish(core.LiveFrame{Kind: "text_delta", Text: ev.Text})
	case core.RuntimeThinkingDelta:
		x.thinking.WriteString(ev.Text)
		x.publish(core.LiveFrame{Kind: "thinking_delta", Text: ev.Text})
	case core.RuntimeAssistantMessage:
		x.flushText()
		x.pendingText = ev.Text
		if ev.Text != "" {
			x.lastText = ev.Text
		}
	case core.RuntimeToolCall:
		input := ev.Input
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		block := core.ContentBlock{Kind: core.BlockToolUse, ID: ev.ID, Name: ev.Name, Input: input}
		text := x.pendingText
		x.pendingText = ""
		x.append(core.KindAssistantMessage, core.AssistantMessage{V: core.PayloadVersion, Content: x.assistantContent(text, &block), StopReason: "tool_use"})
		x.append(core.KindToolCall, core.ToolCallPayload{V: core.PayloadVersion, CallID: ev.ID, Name: ev.Name, Input: input, External: true})
		x.toolNames[ev.ID] = ev.Name
		x.publish(core.LiveFrame{Kind: "tool_call", CallID: ev.ID, Name: ev.Name, Input: input})
	case core.RuntimeToolResult:
		x.flushText()
		status := core.ToolStatusSuccess
		if ev.IsError {
			status = core.ToolStatusError
		}
		output := x.storedOutput(ev.ID, ev.Output)
		x.append(core.KindToolResult, core.ToolResultPayload{V: core.PayloadVersion, CallID: ev.ID, Status: status, Output: output})
		x.publish(core.LiveFrame{Kind: "tool_result", CallID: ev.ID, Name: x.toolNames[ev.ID], Status: string(status), Output: output})
	case core.RuntimeUsage:
		x.flushText()
		x.usage, x.usageSeen = ev.Usage, true
		x.costUSD, x.durationMS = ev.CostUSD, ev.DurationMS
		if ev.Model != "" {
			x.usageModel = ev.Model
		}
	}
}

// storedOutput keeps a large tool result out of the log like a native one: the
// full text is saved under <home>/spill and the log keeps a preview. The
// runtime keeps the full result in its own context either way.
func (x *externalTurn) storedOutput(callID, output string) string {
	home := x.rn.reg.home
	if home == "" || utf8.RuneCountInString(output) <= inlineRunes {
		return output
	}
	tool := strings.ToLower(x.toolNames[callID])
	if tool == "" {
		tool = "tool"
	}
	path, err := spill.Store{Root: filepath.Join(home, "spill")}.Save(x.rn.sessionID, tool, callID, output)
	if err != nil {
		slog.Warn("tool output spill failed", "tool", tool, "session_id", x.rn.sessionID, "error", err)
		return output
	}
	return spillPreview(output, path)
}

// externalApprove routes a runtime permission request through lana's tool
// gate: the workflow gate, then the permission engine with the agent's rules,
// policy, and owner approval (ADR 0004, REQ-CCR-005). The request is matched
// as the equivalent lana tool call, so bash(rm *) or read_file(/etc/*) rules
// apply to Claude's Bash and Read. Every failure denies (fail-closed).
func (rn *Runner) externalApprove(turnID, agentName string) func(context.Context, core.RuntimeApproval) (bool, string) {
	return func(ctx context.Context, req core.RuntimeApproval) (allow bool, message string) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("external approval panicked", "session_id", rn.sessionID, "panic", v)
				allow, message = false, "lana could not decide this tool call"
			}
		}()
		name := req.PermTool
		if name == "" {
			name = strings.ToLower(req.ToolName)
		}
		input := req.PermInput
		if len(input) == 0 {
			input = req.Input
		}
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		call := core.ToolCall{SessionID: rn.sessionID, TurnID: turnID, CallID: req.CallID, Name: name, Input: input, Risk: req.Risk}
		decision, err := rn.reg.hooks.ToolsPreExecute.Call(ctx, core.ToolsPreExecuteInput{AgentName: agentName, Call: call})
		if err != nil {
			return false, "lana could not decide this tool call: " + err.Error()
		}
		if ctx.Err() != nil {
			return false, "the request was cancelled"
		}
		if !decision.Allowed {
			reason := decision.Reason
			if reason == "" {
				reason = "rejected"
			}
			return false, "lana denied this tool call: " + reason
		}
		return true, ""
	}
}

// runExternalTurn runs one turn on an external agent runtime (ADR 0018). It
// writes the same events as a native turn: turn/start, user/message, one
// step/start, assistant/message, tool/call (external), tool/result, step/end
// with the usage, and turn/end. lana's tools, compaction, loop guard, step cap,
// and memory capture do not run (REQ-CCR-004).
func (rn *Runner) runExternalTurn(ctx context.Context, initial inboxItem, turnID, continueReason string, ext externalAgent) (result TurnResult) {
	result = TurnResult{SessionID: rn.sessionID, TurnID: turnID}
	writeCtx := context.WithoutCancel(ctx)
	events := &Events{Store: rn.reg.store, SessionID: rn.sessionID}
	if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindTurnStart, TurnID: turnID, Payload: core.TurnStart{V: core.PayloadVersion, TurnID: turnID}}); err != nil {
		slog.Error("failed to write turn/start", "session_id", rn.sessionID, "turn_id", turnID, "error", err)
		result.StopReason = "error"
		result.Err = &core.ProviderError{Class: "runtime", Message: err.Error()}
		return result
	}
	x := &externalTurn{
		rn: rn, events: events, writeCtx: writeCtx, turnID: turnID, agent: ext,
		provider: llmcall.RuntimeProvider(ext.cfg.Runtime), model: ext.cfg.Model,
		toolNames: map[string]string{},
	}
	reason := "end_turn"
	runErr, stop := rn.externalRun(ctx, x, initial, continueReason)
	if stop != "" {
		reason = stop
	}

	if x.stepStarted {
		model := x.model
		if x.usageModel != "" {
			model = x.usageModel
		}
		x.append(core.KindStepEnd, core.StepEnd{V: core.PayloadVersion, Step: 1, Provider: x.provider, Model: model, Usage: x.usage})
		slog.Info("step finished", "session_id", rn.sessionID, "turn_id", turnID, "step", 1,
			"provider", x.provider, "model", model,
			"input_tokens", x.usage.InputTokens, "output_tokens", x.usage.OutputTokens,
			"cached_input_tokens", x.usage.CachedInputTokens)
	}
	if runErr == nil && x.writeErr != nil {
		runErr = runtimeError(x.writeErr)
	}
	var payloadErr *core.PayloadError
	switch {
	case runErr != nil:
		reason = "error"
		payloadErr = &core.PayloadError{Class: runErr.Class, Status: runErr.Status, Message: runErr.Message}
	case ctx.Err() != nil || reason == core.RuntimeStopCancelled:
		if message, ok := interruptedMessage(ctx); ok {
			// API-TFB-001: a shutdown interrupt is not a user cancel.
			reason = "interrupted"
			payloadErr = &core.PayloadError{Message: message}
		} else {
			reason = "cancelled"
		}
	}
	if x.stepStarted || x.usageSeen {
		rn.recordExternalUsage(x, reason, runErr)
	}
	end := core.TurnEnd{V: core.PayloadVersion, TurnID: turnID, Reason: reason, Error: payloadErr}
	if err := writeTurnEnd(writeCtx, events, core.EventDraft{Kind: core.KindTurnEnd, TurnID: turnID, Payload: end}); err != nil {
		slog.Error("failed to write turn/end", "session_id", rn.sessionID, "turn_id", turnID, "error", err)
	}
	result.StopReason = reason
	result.Text = x.lastText
	result.Usage = x.usage
	result.Err = runErr
	return result
}

// externalRun stores the owner message and runs the turn on the runtime. It
// returns the turn error, or the stop reason ("" = end_turn).
func (rn *Runner) externalRun(ctx context.Context, x *externalTurn, initial inboxItem, continueReason string) (*core.ProviderError, string) {
	text := initial.text
	// REQ-ATT-002: an owner message with attachments and no text is stored
	// with the text "(attachments)".
	if text == "" && len(initial.attachments) > 0 {
		text = "(attachments)"
	}
	input := core.Message{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: text}}}
	decision, err := rn.reg.hooks.AgentPreStep.Call(ctx, core.PreStepInput{
		SessionID: rn.sessionID, TurnID: x.turnID, AgentName: "", Step: 1, Input: input,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, core.RuntimeStopCancelled
		}
		return runtimeError(err), ""
	}
	if decision.Reject {
		slog.Info("agent.pre_step rejected message", "session_id", rn.sessionID, "turn_id", x.turnID, "reason", decision.Reason)
		return nil, ""
	}
	if len(decision.Input.Content) > 0 {
		input = decision.Input
	}
	if ws := x.agent.workspace; ws != "" {
		if _, statErr := os.Stat(ws); statErr != nil {
			if mkErr := os.MkdirAll(ws, 0o700); mkErr != nil {
				slog.Warn("workspace create failed", "workspace", ws, "error", mkErr)
			}
		}
	}
	// REQ-ATT-002: one block per attachment after the text; images reach the
	// runtime as image blocks.
	if len(initial.attachments) > 0 {
		input.Content = append(input.Content, ownerAttachmentBlocks(x.agent.workspace, initial.attachments)...)
	}
	payload := core.UserMessage{V: core.PayloadVersion, Content: input.Content}
	if continueReason != "" {
		payload.Continue = true
		payload.ContinueReason = continueReason
	}
	if _, err := x.events.Append(x.writeCtx, core.EventDraft{Kind: core.KindUserMessage, TurnID: x.turnID, Payload: payload}); err != nil {
		return runtimeError(err), ""
	}

	rt, err := rn.reg.Runtime(x.agent.cfg.Runtime)
	if err != nil {
		return runtimeError(err), ""
	}
	chain, err := rn.reg.store.ChainEvents(x.writeCtx, rn.sessionID)
	if err != nil {
		return runtimeError(err), ""
	}
	resumeID := ""
	if meta, ok := latestRuntimeMeta(chain, x.agent.cfg.Runtime); ok {
		resumeID = meta.ClaudeSessionID
		// A live process sends no new session meta, so the stored one names
		// the model; a new process sends its own before the step starts.
		if meta.Model != "" {
			x.model = meta.Model
		}
	}
	if x.model == "" {
		x.model = "default"
	}
	cfg := x.agent.cfg
	turn := core.RuntimeTurn{
		SessionID: rn.sessionID,
		TurnID:    x.turnID,
		AgentName: x.agent.name,
		Config: core.RuntimeConfig{
			Auth: cfg.Auth, APIKeyEnv: cfg.APIKeyEnv, Model: cfg.Model, Effort: x.agent.effort,
			Binary: cfg.Binary, Workspace: x.agent.workspace,
		},
		ResumeID: resumeID,
		Input:    input.Content,
		Emit:     x.handle,
		Approve:  rn.externalApprove(x.turnID, x.agent.name),
	}
	rn.setExternal(rt)
	defer rn.setExternal(nil)
	runErr := rt.RunTurn(ctx, turn)
	if !x.ended {
		// The contract promises an end event; a runtime that broke it still
		// ends the turn.
		x.flushText()
		if runErr == nil && ctx.Err() == nil {
			return nil, ""
		}
		if ctx.Err() != nil && runErr == nil {
			return nil, core.RuntimeStopCancelled
		}
	}
	switch {
	case x.end.IsError || (runErr != nil && !x.ended):
		message := x.end.ErrorText
		if message == "" && runErr != nil {
			message = runErr.Error()
		}
		if message == "" {
			message = "the agent runtime failed"
		}
		return &core.ProviderError{Class: "runtime", Message: message}, ""
	case x.end.StopReason == core.RuntimeStopCancelled:
		return nil, core.RuntimeStopCancelled
	}
	return nil, ""
}

// recordExternalUsage hands the turn's one call record to the host
// (REQ-CCR-008).
func (rn *Runner) recordExternalUsage(x *externalTurn, reason string, runErr *core.ProviderError) {
	h := rn.reg.hooksExternal()
	if h.Usage == nil {
		return
	}
	model := x.model
	if x.usageModel != "" {
		model = x.usageModel
	}
	started := x.started
	if started.IsZero() {
		started = time.Now()
	}
	duration := x.durationMS
	if duration <= 0 {
		duration = time.Since(started).Milliseconds()
	}
	outcome, message := "ok", ""
	switch {
	case runErr != nil:
		outcome, message = runErr.Class, runErr.Message
	case reason == "cancelled" || reason == "interrupted":
		outcome = "cancelled"
	}
	h.Usage(ExternalUsage{
		Runtime: x.agent.cfg.Runtime, Provider: x.provider, Agent: x.agent.name,
		SessionID: rn.sessionID, TurnID: x.turnID, Model: model,
		Usage: x.usage, CostUSD: x.costUSD, Started: started, DurationMS: duration,
		Outcome: outcome, Error: message,
	})
}

// setExternal records the runtime of the session's running external turn, so
// Cancel can interrupt it directly.
func (rn *Runner) setExternal(rt core.AgentRuntime) {
	rn.mu.Lock()
	rn.externalRT = rt
	rn.mu.Unlock()
}

// interruptExternal asks the runtime of the session's running external turn
// to stop (REQ-CCR-006). Cancelling the turn context has the same effect; the
// direct call does not wait for the turn goroutine to notice.
func (rn *Runner) interruptExternal() {
	rn.mu.Lock()
	rt := rn.externalRT
	rn.mu.Unlock()
	if rt == nil {
		return
	}
	if err := rt.Interrupt(rn.sessionID); err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("agent runtime interrupt", "session_id", rn.sessionID, "error", err)
	}
}
