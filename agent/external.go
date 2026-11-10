package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	// Provider is the call record provider, "claude-code" (API-CCT-008).
	Provider string
	// Requested is the combo or address the turn's chain asked for.
	Requested string
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
	// record provider and the runtime key (both "claude-code").
	Limits func(provider, runtime string, windows []core.RuntimeLimitWindow)
	// Models receives the model list a runtime learned from its binary.
	Models func(runtime string, models []core.RuntimeModel)
}

// SetToolHost installs the loopback MCP host that serves lana's tools to
// Claude Code turns (ADR 0020, REQ-CCT-006). The root package sets it at
// boot; nil turns lana tools off for those turns.
func (r *Registry) SetToolHost(h core.RuntimeToolHost) {
	r.runtimeMu.Lock()
	r.toolHost = h
	r.runtimeMu.Unlock()
}

func (r *Registry) toolHostFor() core.RuntimeToolHost {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	return r.toolHost
}

// MarkScheduled marks the session's turns as scheduled runs until the
// returned func is called (REQ-CCT-008): a Claude Code target then runs only
// when the agent sets allow_scheduled. Marks nest.
func (r *Registry) MarkScheduled(sessionID string) func() {
	r.runtimeMu.Lock()
	if r.scheduled == nil {
		r.scheduled = map[string]int{}
	}
	r.scheduled[sessionID]++
	r.runtimeMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.runtimeMu.Lock()
			if r.scheduled[sessionID]--; r.scheduled[sessionID] <= 0 {
				delete(r.scheduled, sessionID)
			}
			r.runtimeMu.Unlock()
		})
	}
}

func (r *Registry) isScheduled(sessionID string) bool {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	return r.scheduled[sessionID] > 0
}

// MarkBackground marks the session's turns as lana background work until the
// returned func is called (spec 39 revision 5): they never run in Claude
// Code; they run in lana's loop on the agent's background chain (the chain
// without its Claude Code targets, else [router] background_model,
// REQ-CCT-007). Without one the turn fails with llmcall.ErrNoBackgroundModel.
// Marks nest.
func (r *Registry) MarkBackground(sessionID string) func() {
	r.runtimeMu.Lock()
	if r.background == nil {
		r.background = map[string]int{}
	}
	r.background[sessionID]++
	r.runtimeMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.runtimeMu.Lock()
			if r.background[sessionID]--; r.background[sessionID] <= 0 {
				delete(r.background, sessionID)
			}
			r.runtimeMu.Unlock()
		})
	}
}

func (r *Registry) isBackground(sessionID string) bool {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	return r.background[sessionID] > 0
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
	onModels := func(models []core.RuntimeModel) {
		if h := r.hooksExternal(); h.Models != nil {
			h.Models(name, models)
		}
	}
	rt, err := factory(core.RuntimeDeps{Secrets: r.secrets, Logger: slog.Default(), OnModels: onModels})
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

// isClaudeProvider reports whether a step's provider is a Claude Code account
// of llm (REQ-CCT-010); spec 38 steps used "claude-agent".
func isClaudeProvider(llm core.LLMRegistry, provider string) bool {
	if provider == "claude-agent" {
		return true
	}
	_, ok := llmcall.ClaudeCodeAccount(llm, provider)
	return ok
}

// claudeResume returns the Claude session of account to resume for the next
// Claude Code turn: the newest runtime session meta (API-CCR-002), unless a
// turn ran in lana's loop after it (REQ-CCT-003: an engine switch starts a
// new Claude session) or the meta belongs to another account (REQ-CCT-010:
// sessions never mix accounts). switched reports that a stored Claude session
// was dropped. The model is the one the meta names. isClaude tells Claude
// Code step providers from lana's.
func claudeResume(events []core.SessionEvent, account string, isClaude func(string) bool) (resumeID, model string, switched bool) {
	metaAccount := ""
	for _, event := range events {
		switch event.Kind {
		case core.KindSystemMessage:
			var payload core.SystemMessage
			if json.Unmarshal(event.Payload, &payload) != nil || payload.Subtype != core.SystemSubtypeRuntime {
				continue
			}
			if payload.Runtime != llmcall.ClaudeCode || payload.ClaudeSessionID == "" {
				continue
			}
			resumeID, model, switched = payload.ClaudeSessionID, payload.Model, false
			metaAccount = payload.Account
			if metaAccount == "" {
				metaAccount = llmcall.ClaudeCode
			}
		case core.KindStepStart:
			var payload core.StepStart
			if json.Unmarshal(event.Payload, &payload) != nil || isClaude(payload.Provider) {
				continue
			}
			if resumeID != "" {
				resumeID, model, switched = "", "", true
			}
		}
	}
	if resumeID != "" && metaAccount != account {
		return "", "", true
	}
	return resumeID, model, switched
}

// claudeToolNote tells Claude how lana's tools are named over MCP. tools are
// the MCP tool names actually served (the result of claudeMCPTools), listed
// sorted so the prompt is stable and only names the tools this agent has. An
// empty list returns "" and the caller appends no note.
func claudeToolNote(tools []string) string {
	if len(tools) == 0 {
		return ""
	}
	names := append([]string(nil), tools...)
	sort.Strings(names)
	return "## lana tools\nlana's own tools are available as MCP tools named mcp__lana__<tool>: " +
		strings.Join(names, ", ") + ". The tool notes above use the plain names. Use your own tools for files and shell commands."
}

// claudeOwnTools are the lana tools Claude Code has itself: they are not
// served over MCP (REQ-CCT-006).
var claudeOwnTools = []string{"bash", "edit_file", "glob", "grep", "read_file", "write_file"}

// claudeMCPTools returns the offered lana tools served to Claude Code: all of
// them except the file and shell tools and the job tools that come with bash.
func claudeMCPTools(offered []string) []string {
	out := make([]string, 0, len(offered))
	for _, name := range offered {
		if containsString(claudeOwnTools, name) || containsString(companionTools, name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// mcpToolName returns the lana tool name of a Claude tool call to the lana MCP
// server ("mcp__lana__memory_write" -> "memory_write").
func mcpToolName(name string) (string, bool) {
	const prefix = "mcp__lana__"
	if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
		return name[len(prefix):], true
	}
	return "", false
}

// externalTurn is the state of one external runtime turn while its events are
// mirrored into the session log.
type externalTurn struct {
	rn       *Runner
	events   *Events
	writeCtx context.Context
	turnID   string
	agent    resolvedAgent
	provider string
	model    string
	// requested is the chain's requested value (API-CCT-008).
	requested string
	// content is set by the first text, thinking, or tool event.
	content  bool
	recorded bool

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

// metaAccount is the account of the runtime session meta: "" for the
// built-in one, so its events read as before accounts existed.
func (x *externalTurn) metaAccount() string {
	if x.provider == llmcall.ClaudeCode {
		return ""
	}
	return x.provider
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
				V: core.PayloadVersion, Subtype: core.SystemSubtypeRuntime, Runtime: llmcall.ClaudeCode,
				ClaudeSessionID: ev.ClaudeSessionID, Model: ev.Model, Tools: ev.Tools, Version: ev.Version,
				Account: x.metaAccount(),
			})
		}
		return
	case core.RuntimeEnd:
		x.flushText()
		x.end, x.ended = ev, true
		return
	case core.RuntimeLimits:
		// API-CCT-012: one limit snapshot per account.
		if h := x.rn.reg.hooksExternal(); h.Limits != nil && len(ev.Windows) > 0 {
			h.Limits(x.provider, llmcall.ClaudeCode, ev.Windows)
		}
		return
	case core.RuntimeUsage:
		x.flushText()
		x.usage, x.usageSeen = ev.Usage, true
		x.costUSD, x.durationMS = ev.CostUSD, ev.DurationMS
		if ev.Model != "" {
			x.usageModel = ev.Model
		}
		return
	}
	x.content = true
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
		// REQ-CCT-006: a call to lana's MCP server is a lana tool call: lana
		// ran it through its tool runner, so the log shows the plain name.
		name, external := ev.Name, true
		if lanaName, ok := mcpToolName(ev.Name); ok {
			name, external = lanaName, false
		}
		block := core.ContentBlock{Kind: core.BlockToolUse, ID: ev.ID, Name: name, Input: input}
		text := x.pendingText
		x.pendingText = ""
		x.append(core.KindAssistantMessage, core.AssistantMessage{V: core.PayloadVersion, Content: x.assistantContent(text, &block), StopReason: "tool_use"})
		x.append(core.KindToolCall, core.ToolCallPayload{V: core.PayloadVersion, CallID: ev.ID, Name: name, Input: input, External: external})
		x.toolNames[ev.ID] = name
		x.publish(core.LiveFrame{Kind: "tool_call", CallID: ev.ID, Name: name, Input: input})
	case core.RuntimeToolResult:
		x.flushText()
		status := core.ToolStatusSuccess
		if ev.IsError {
			status = core.ToolStatusError
		}
		output := x.storedOutput(ev.ID, ev.Output)
		x.append(core.KindToolResult, core.ToolResultPayload{V: core.PayloadVersion, CallID: ev.ID, Status: status, Output: output})
		x.publish(core.LiveFrame{Kind: "tool_result", CallID: ev.ID, Name: x.toolNames[ev.ID], Status: string(status), Output: output})
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
		// REQ-CCT-006: a lana MCP tool is gated when lana runs it, by the
		// same permission engine, so Claude's own ask is answered allow here.
		if _, ok := mcpToolName(req.ToolName); ok {
			return true, ""
		}
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

// runExternalTurn runs one turn in the Claude Code runtime (ADR 0018, ADR
// 0020). It writes the same events as a native turn: turn/start,
// user/message, one step/start, assistant/message, tool/call, tool/result,
// step/end with the usage, and turn/end. Claude gets lana's prompt head and
// tools (REQ-CCT-005, REQ-CCT-006); lana's compaction, loop guard, and step cap
// do not run. When Claude Code fails before any output and the chain has a
// target outside Claude Code, it writes the attempt and reports fallback
// without ending the turn; runTurn then continues it in lana's loop
// (REQ-CCT-004).
func (rn *Runner) runExternalTurn(ctx context.Context, initial inboxItem, turnID, continueReason string, res resolvedAgent) (result TurnResult, fallback bool) {
	result = TurnResult{SessionID: rn.sessionID, TurnID: turnID}
	writeCtx := context.WithoutCancel(ctx)
	events := &Events{Store: rn.reg.store, SessionID: rn.sessionID}
	if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindTurnStart, TurnID: turnID, Payload: core.TurnStart{V: core.PayloadVersion, TurnID: turnID}}); err != nil {
		slog.Error("failed to write turn/start", "session_id", rn.sessionID, "turn_id", turnID, "error", err)
		result.StopReason = "error"
		result.Err = &core.ProviderError{Class: "runtime", Message: err.Error()}
		return result, false
	}
	target := res.fullChain[0]
	// API-CCT-012: the record provider is the account name.
	x := &externalTurn{
		rn: rn, events: events, writeCtx: writeCtx, turnID: turnID, agent: res,
		provider: target.Provider, model: target.Model, requested: res.requested,
		toolNames: map[string]string{},
	}
	reason := "end_turn"
	run := rn.externalRun(ctx, x, initial, continueReason, target)
	runErr := run.err
	if run.stop != "" {
		reason = run.stop
	}
	if early := run.early; early != nil {
		pe := core.ProviderError{Class: early.Class, Message: early.Message}
		rn.writeStepAttempt(writeCtx, events, 1, target, turnID, 1, pe)
		rn.recordExternalUsage(x, "error", &pe)
		if len(res.chain) > 0 && ctx.Err() == nil {
			slog.Info("claude code failed before output; the turn continues in lana's loop",
				"session_id", rn.sessionID, "turn_id", turnID, "target", llmcall.Address(target), "class", early.Class, "next", llmcall.Address(res.chain[0]))
			return result, true
		}
		runErr = &core.ProviderError{Class: "runtime", Message: early.Message}
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
	// REQ-MEM-010, REQ-CCT-007: memory capture runs after a Claude Code turn
	// too, over the mirrored transcript, with the agent's background chain.
	if captureEligible(reason, res.child, run.owner, run.specs, res.cfg) {
		if c := rn.reg.memoryCapturer(); c != nil {
			c.Capture(core.CaptureRequest{
				SessionID: rn.sessionID, TurnID: turnID, AgentName: res.name,
				Home: rn.reg.home, Targets: res.fullChain, Requested: res.requested,
				System: run.system, Tools: run.specs, ReasoningEffort: res.reasoningEffort,
			})
		}
	}
	// Spec 40, REQ-TTL-001: the title pass uses the background chain, never
	// Claude Code.
	rn.reg.titleTurnEnd(reason, res.child, run.owner, core.TitleRequest{
		SessionID: rn.sessionID, AgentName: res.name,
		Targets: res.fullChain, Requested: res.requested,
	})
	result.StopReason = reason
	result.Text = x.lastText
	result.Usage = x.usage
	result.Err = runErr
	return result, false
}

// externalOutcome is what externalRun hands back to runExternalTurn.
type externalOutcome struct {
	err   *core.ProviderError
	stop  string // "" = end_turn
	early *core.RuntimeEarlyError
	// owner reports that the owner message was stored; specs and system are
	// the lana tools and prompt head of the turn, for memory capture.
	owner  bool
	specs  []core.ToolSpec
	system string
}

// externalRun stores the owner message with its <context> block, builds
// Claude's prompt head and lana tool set, and runs the turn on the runtime.
func (rn *Runner) externalRun(ctx context.Context, x *externalTurn, initial inboxItem, continueReason string, target core.LLMTarget) externalOutcome {
	res := x.agent
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
			return externalOutcome{stop: core.RuntimeStopCancelled}
		}
		return externalOutcome{err: runtimeError(err)}
	}
	if decision.Reject {
		slog.Info("agent.pre_step rejected message", "session_id", rn.sessionID, "turn_id", x.turnID, "reason", decision.Reason)
		return externalOutcome{}
	}
	if len(decision.Input.Content) > 0 {
		input = decision.Input
	}
	if ws := res.workspace; ws != "" {
		if _, statErr := os.Stat(ws); statErr != nil {
			if mkErr := os.MkdirAll(ws, 0o700); mkErr != nil {
				slog.Warn("workspace create failed", "workspace", ws, "error", mkErr)
			}
		}
	}
	// REQ-ATT-002: one block per attachment after the text; images reach the
	// runtime as image blocks.
	if len(initial.attachments) > 0 {
		input.Content = append(input.Content, ownerAttachmentBlocks(res.workspace, initial.attachments)...)
	}

	// REQ-CCT-005, REQ-CCT-006: the prompt head and the lana tools of a
	// native turn of this agent, without the tools Claude Code has itself.
	offered, unavailable, skills := rn.offeredTools(ctx, res)
	tools := claudeMCPTools(offered)
	specs, err := rn.reg.tools.Specs(tools)
	if err != nil {
		slog.Warn("lana tools for claude code", "session_id", rn.sessionID, "error", err)
		specs, tools = nil, nil
	}
	built, err := rn.buildPrompt(res, skills, unavailable, rn.toolNotes(tools), len(res.mcp) > 0, text, tools)
	if err != nil {
		return externalOutcome{err: runtimeError(err)}
	}
	system := built.text
	if note := claudeToolNote(tools); note != "" {
		system += "\n\n" + note
	}
	out := externalOutcome{specs: specs, system: system}

	// REQ-CCT-005: the <context> block (time, relevant memory, knowledge
	// recall, jobs) goes in front of the owner message, as in native turns.
	// A reply adds the quoted automation message as one more injected block.
	prefix := make([]core.ContentBlock, 0, 2)
	if built.context != "" {
		prefix = append(prefix, core.ContentBlock{Kind: core.BlockText, Text: built.context})
	}
	if initial.replyTo != nil {
		prefix = append(prefix, core.ContentBlock{Kind: core.BlockText, Text: replyQuoteBlock(initial.replyTo)})
	}
	input.Content = append(prefix, input.Content...)
	payload := core.UserMessage{V: core.PayloadVersion, Content: input.Content, ContextBlocks: len(prefix)}
	if initial.replyTo != nil {
		payload.ReplyTo = initial.replyTo
	}
	if continueReason != "" {
		payload.Continue = true
		payload.ContinueReason = continueReason
	}
	if _, err := x.events.Append(x.writeCtx, core.EventDraft{Kind: core.KindUserMessage, TurnID: x.turnID, Payload: payload}); err != nil {
		out.err = runtimeError(err)
		return out
	}
	out.owner = true

	account, ok := llmcall.ClaudeCodeAccount(rn.reg.llm, target.Provider)
	switch {
	case !ok:
		out.early = &core.RuntimeEarlyError{Class: core.ErrorClassNotFound, Message: fmt.Sprintf("provider %q is not configured", target.Provider)}
		return out
	case account.Disabled:
		// A disabled account is skipped like a disabled provider
		// (API-RTR-001 revision 2).
		out.early = &core.RuntimeEarlyError{Class: core.ErrorClassNotFound, Message: fmt.Sprintf("provider %q is disabled", target.Provider)}
		return out
	}

	rt, err := rn.reg.Runtime(llmcall.ClaudeCode)
	if err != nil {
		out.early = &core.RuntimeEarlyError{Class: "runtime", Message: err.Error()}
		return out
	}
	chain, err := rn.reg.store.ChainEvents(x.writeCtx, rn.sessionID)
	if err != nil {
		out.err = runtimeError(err)
		return out
	}
	isClaude := func(provider string) bool { return isClaudeProvider(rn.reg.llm, provider) }
	resumeID, metaModel, switched := claudeResume(chain, target.Provider, isClaude)
	if switched {
		// REQ-CCT-003, REQ-CCT-010: a turn ran in lana's loop or on another
		// account since the stored Claude session, so this turn starts a new
		// one.
		rt.Close(rn.sessionID)
	}
	if resumeID != "" && metaModel != "" {
		// A live process sends no new session meta, so the stored one names
		// the model; a new process sends its own before the step starts.
		x.model = metaModel
	}
	if x.model == "" {
		x.model = "default"
	}

	// REQ-CCT-006: lana's tools over the loopback MCP host. A host that
	// cannot serve leaves Claude without lana's tools, with a note.
	var openMCP func() (string, func(), error)
	mcpKey := ""
	if host := rn.reg.toolHostFor(); host != nil && len(specs) > 0 {
		// REQ-DKN-021, REQ-DKN-032: a tool sees an owner turn only for a root
		// turn that stored an owner message, the same value captureEligible
		// uses (REQ-MEM-010).
		ownerTurn := !res.child && out.owner
		if err := host.Bind(rn.sessionID, specs, rn.claudeToolCall(ctx, x.turnID, res, tools, ownerTurn)); err != nil {
			slog.Warn("lana tools are not available to claude code", "session_id", rn.sessionID, "error", err)
			x.append(core.KindSystemMessage, core.SystemMessage{V: core.PayloadVersion, Subtype: core.SystemSubtypeNote,
				Text: "lana's tools are not available to Claude Code in this turn: " + err.Error()})
		} else {
			defer host.Unbind(rn.sessionID)
			sessionID := rn.sessionID
			openMCP = func() (string, func(), error) { return host.Open(sessionID) }
			mcpKey = strings.Join(tools, ",")
		}
	}

	model := target.Model
	if model == "default" {
		model = ""
	}
	turn := core.RuntimeTurn{
		SessionID: rn.sessionID,
		TurnID:    x.turnID,
		AgentName: res.name,
		Config: core.RuntimeConfig{
			Model: model, Effort: res.claudeEffort, Workspace: res.workspace,
			AppendSystemPrompt: system, MCPKey: mcpKey,
		},
		ResumeID: resumeID,
		Input:    input.Content,
		Emit:     x.handle,
		Approve:  rn.externalApprove(x.turnID, res.name),
		OpenMCP:  openMCP,
	}
	applyAccount(&turn.Config, rn.reg.home, target.Provider, account, res.cfg)
	rn.setExternal(rt)
	defer rn.setExternal(nil)
	runErr := rt.RunTurn(ctx, turn)
	var early *core.RuntimeEarlyError
	if errors.As(runErr, &early) && !x.content && ctx.Err() == nil {
		out.early = early
		return out
	}
	if !x.ended {
		// The contract promises an end event; a runtime that broke it still
		// ends the turn.
		x.flushText()
		if runErr == nil && ctx.Err() == nil {
			return out
		}
		if ctx.Err() != nil && runErr == nil {
			out.stop = core.RuntimeStopCancelled
			return out
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
		out.err = &core.ProviderError{Class: "runtime", Message: message}
	case x.end.StopReason == core.RuntimeStopCancelled:
		out.stop = core.RuntimeStopCancelled
	}
	return out
}

// applyAccount sets the account keys of a Claude Code turn (REQ-CCT-010): the
// built-in account takes the binary, auth, and api_key_env of its optional
// [providers.claude-code] table (API-CCT-027), else the agent's (the keys of
// spec 38 agents), and keeps the daemon's CLAUDE_CONFIG_DIR; another account
// takes its own keys and folder.
func applyAccount(cfg *core.RuntimeConfig, home, name string, account core.ProviderConfig, agent core.AgentConfig) {
	cfg.Account = name
	if name == llmcall.ClaudeCode {
		cfg.Binary = firstSet(account.Binary, agent.Binary)
		cfg.Auth = firstSet(account.Auth, agent.Auth)
		cfg.APIKeyEnv = firstSet(account.APIKeyEnv, agent.APIKeyEnv)
		return
	}
	cfg.Binary, cfg.Auth, cfg.APIKeyEnv = account.Binary, account.Auth, account.APIKeyEnv
	cfg.ConfigDir = llmcall.ClaudeConfigDir(home, name, account)
}

// claudeToolCall runs one lana tool for a Claude Code turn through the tool
// runner, so the permission engine, approvals, input checks, and spill apply
// (REQ-CCT-006). The call ends when the turn ends or Claude drops the request.
// The tool/call and tool/result events come from Claude's own stream.
func (rn *Runner) claudeToolCall(turnCtx context.Context, turnID string, res resolvedAgent, allowed []string, ownerTurn bool) core.RuntimeToolFunc {
	return func(reqCtx context.Context, callID, name string, input json.RawMessage) core.ToolOutput {
		ctx, cancel := context.WithCancel(turnCtx)
		defer cancel()
		stop := context.AfterFunc(reqCtx, cancel)
		defer stop()
		if !containsString(allowed, name) {
			return core.ToolOutput{Status: core.ToolStatusError, Output: "tool " + name + " is not offered to this agent"}
		}
		call := core.ToolCall{SessionID: rn.sessionID, TurnID: turnID, CallID: callID, Name: name, Input: input}
		toolCtx := rn.toolCtx(ctx, res, turnID)
		// REQ-DKN-021, REQ-DKN-032: a tool sees an owner turn only for a root
		// turn that stored an owner message (REQ-MEM-010).
		toolCtx.OwnerTurn = ownerTurn
		outs := rn.reg.pipeline.Run(ctx, []core.ToolCall{call}, allowed, toolCtx)
		if len(outs) == 0 {
			return core.ToolOutput{Status: core.ToolStatusError, Output: "tool produced no output"}
		}
		return outs[0]
	}
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
	if x.recorded {
		return
	}
	x.recorded = true
	h.Usage(ExternalUsage{
		Runtime: llmcall.ClaudeCode, Provider: x.provider, Requested: x.requested, Agent: x.agent.name,
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

// firstSet returns a when it is not empty, else b.
func firstSet(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
