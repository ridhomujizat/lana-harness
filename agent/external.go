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
	"github.com/ridhomujizat/lana-harness/internal/models"
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
	// Tier receives the plan tier a runtime reported in its session meta
	// (Antigravity, spec 74), with the runtime key and the account name.
	Tier func(runtime, account, tier string)
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

// metaAccountName returns the account of a runtime session meta: its
// Account, else the built-in account of its runtime (spec 38 metas named the
// runtime "claude-agent").
func metaAccountName(p core.SystemMessage) string {
	if p.Account != "" {
		return p.Account
	}
	if p.Runtime == "claude-agent" {
		return llmcall.ClaudeCode
	}
	return p.Runtime
}

// stepAccount returns the account a step ran on: its provider, with the spec
// 38 provider "claude-agent" read as the built-in Claude Code account.
func stepAccount(provider string) string {
	if provider == "claude-agent" {
		return llmcall.ClaudeCode
	}
	return provider
}

// runtimeMeta decodes a runtime session meta event that names a runtime
// session.
func runtimeMeta(event core.SessionEvent) (core.SystemMessage, bool) {
	if event.Kind != core.KindSystemMessage {
		return core.SystemMessage{}, false
	}
	var payload core.SystemMessage
	if json.Unmarshal(event.Payload, &payload) != nil || payload.Subtype != core.SystemSubtypeRuntime || payload.ClaudeSessionID == "" {
		return core.SystemMessage{}, false
	}
	return payload, true
}

// resumeState is what externalResume found for the next external turn.
type resumeState struct {
	// resumeID and model name the runtime session to resume; empty starts a
	// new one.
	resumeID, model string
	// switched reports that a stored runtime session of this runtime was
	// dropped, so the runtime's live process must not be reused.
	switched bool
	// previous is the account of the newest runtime session meta of any
	// runtime, "" when the session has none.
	previous string
}

// externalResume returns the runtime session of runtime and account to resume
// for the next external turn: the newest runtime session meta (API-CCR-002)
// of the same runtime and account, unless a step ran on another provider
// after it (REQ-CCT-003: an engine switch starts a new runtime session) or a
// newer meta of another account of the runtime followed it (REQ-CCT-010,
// REQ-AGY-004: sessions never mix accounts). The model is the one the meta
// names.
func externalResume(events []core.SessionEvent, runtime, account string) resumeState {
	var st resumeState
	stored := false
	for _, event := range events {
		switch event.Kind {
		case core.KindSystemMessage:
			payload, ok := runtimeMeta(event)
			if !ok {
				continue
			}
			metaAccount := metaAccountName(payload)
			st.previous = metaAccount
			if payload.Runtime != runtime {
				continue
			}
			stored = true
			if metaAccount == account {
				st.resumeID, st.model = payload.ClaudeSessionID, payload.Model
			} else {
				st.resumeID, st.model = "", ""
			}
		case core.KindStepStart:
			var payload core.StepStart
			if json.Unmarshal(event.Payload, &payload) != nil || stepAccount(payload.Provider) == account {
				continue
			}
			st.resumeID, st.model = "", ""
		}
	}
	st.switched = stored && st.resumeID == ""
	return st
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

// antigravityToolNote tells Antigravity which of lana's tools its lana MCP
// server serves (spec 74). agy has its own shell, file, and search tools.
func antigravityToolNote(tools []string) string {
	if len(tools) == 0 {
		return ""
	}
	names := append([]string(nil), tools...)
	sort.Strings(names)
	return "## lana tools\nlana's own tools are available as MCP tools named mcp_lana_<tool>: " +
		strings.Join(names, ", ") + ". The tool notes above use the plain names. Use your own tools for files, shell commands, and search."
}

// externalToolNote is the prompt-head tool note of runtime.
func externalToolNote(runtime string, tools []string) string {
	if runtime == llmcall.Antigravity {
		return antigravityToolNote(tools)
	}
	return claudeToolNote(tools)
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

// mcpToolName returns the lana tool name of a runtime tool call to the lana
// MCP server: Claude Code names it "mcp__lana__memory_write", agy 1.5.17
// "mcp_lana_memory_write" (recorded live 2026-11-26) -> "memory_write".
func mcpToolName(name string) (string, bool) {
	for _, prefix := range []string{"mcp__lana__", "mcp_lana_"} {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			return name[len(prefix):], true
		}
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
	// runtime is the runtime key of the turn: "claude-code" or "antigravity".
	runtime  string
	provider string
	model    string
	// effort is the --effort of a Claude Code turn; "" when none is sent.
	effort string
	// pendingMeta is the runtime session meta not yet stored. It is stored
	// with the first content event or when the attempt did not fail early, so
	// an account that failed before output leaves no session to resume or
	// stick to (REQ-PAF-003).
	pendingMeta *core.SystemMessage
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
	if x.provider == x.runtime {
		return ""
	}
	return x.provider
}

// flushMeta stores the pending runtime session meta.
func (x *externalTurn) flushMeta() {
	if x.pendingMeta == nil {
		return
	}
	meta := *x.pendingMeta
	x.pendingMeta = nil
	x.append(core.KindSystemMessage, meta)
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
	x.append(core.KindStepStart, core.StepStart{V: core.PayloadVersion, Step: 1, Provider: x.provider, Model: x.model, Effort: x.stepEffort(x.model)})
}

// stepEffort is the effort a runtime step runs with (spec 76 REQ-MES-003):
// Claude Code's --effort, or the level suffix of the Antigravity variant the
// runtime picked ("gemini-4-neon-high" runs at high).
func (x *externalTurn) stepEffort(model string) string {
	if x.runtime == llmcall.Antigravity {
		if _, level, ok := models.SplitEffortVariant(model); ok {
			return level
		}
		return ""
	}
	return x.effort
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
			x.pendingMeta = &core.SystemMessage{
				V: core.PayloadVersion, Subtype: core.SystemSubtypeRuntime, Runtime: x.runtime,
				ClaudeSessionID: ev.ClaudeSessionID, Model: ev.Model, Tools: ev.Tools, Version: ev.Version,
				Account: x.metaAccount(),
			}
		}
		if h := x.rn.reg.hooksExternal(); h.Tier != nil && ev.Tier != "" {
			h.Tier(x.runtime, x.provider, ev.Tier)
		}
		return
	case core.RuntimeEnd:
		x.flushText()
		x.end, x.ended = ev, true
		return
	case core.RuntimeLimits:
		// API-CCT-012: one limit snapshot per account.
		if h := x.rn.reg.hooksExternal(); h.Limits != nil && len(ev.Windows) > 0 {
			h.Limits(x.provider, x.runtime, ev.Windows)
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
	x.flushMeta()
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

// runExternalTurn runs one turn in an external runtime: Claude Code or
// Antigravity (ADR 0018, ADR 0020, ADR 0022). It writes the same events as a
// native turn: turn/start, user/message, one step/start, assistant/message,
// tool/call, tool/result, step/end with the usage, and turn/end. The runtime
// gets lana's prompt head and tools (REQ-CCT-005, REQ-CCT-006); lana's
// compaction, loop guard, and step cap do not run. A pooled address tries the
// accounts of its pool in order (spec 75, REQ-PAF-001): an account that fails
// before any output with auth, rate_limit, or quota is cooled down and the
// next one runs. When the runtime fails before any output and no account is
// left, and the chain has a target outside the external runtimes, it reports
// fallback without ending the turn; runTurn then continues it in lana's loop
// (REQ-CCT-004, REQ-AGY-008).
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
	head := res.fullChain[0]
	runtime, ok := llmcall.ExternalRuntime(rn.reg.llm, head.Provider)
	if !ok {
		runtime = llmcall.ClaudeCode
	}
	// API-CCT-012: the record provider is the account name.
	newTurn := func(target core.LLMTarget) *externalTurn {
		return &externalTurn{
			rn: rn, events: events, writeCtx: writeCtx, turnID: turnID, agent: res, runtime: runtime,
			provider: target.Provider, model: target.Model, requested: res.requested,
			toolNames: map[string]string{},
		}
	}
	x := newTurn(head)
	reason := "end_turn"
	prep, run, ok := rn.externalPrepare(ctx, x, initial, continueReason, runtime)
	if ok {
		candidates := rn.externalCandidates(writeCtx, runtime, head, res.cfg)
		for i, target := range candidates {
			x = newTurn(target)
			run = rn.externalAttempt(ctx, x, prep, target)
			early := run.early
			if early == nil {
				break
			}
			pe := core.ProviderError{Class: early.Class, Message: early.Message}
			rn.writeStepAttempt(writeCtx, events, 1, target, turnID, i+1, pe)
			rn.recordExternalUsage(x, "error", &pe)
			pooled := poolFailure(early.Class)
			if pooled {
				// REQ-PAF-004: the account rests until its window resets.
				rn.reg.coolAccount(target.Provider, cooldownUntil(early, time.Now()))
			}
			if !pooled || ctx.Err() != nil || i == len(candidates)-1 {
				break
			}
			slog.Info("external account failed before output; the turn moves to the next account",
				"session_id", rn.sessionID, "turn_id", turnID, "account", target.Provider, "class", early.Class, "next", candidates[i+1].Provider)
		}
	}
	runErr := run.err
	if run.stop != "" {
		reason = run.stop
	}
	if early := run.early; early != nil {
		if len(res.chain) > 0 && ctx.Err() == nil {
			slog.Info("external runtime failed before output; the turn continues in lana's loop",
				"session_id", rn.sessionID, "turn_id", turnID, "runtime", runtime, "target", llmcall.Address(head), "class", early.Class, "next", llmcall.Address(res.chain[0]))
			return result, true
		}
		runErr = &core.ProviderError{Class: "runtime", Message: early.Message}
	}

	if x.stepStarted {
		model := x.model
		if x.usageModel != "" {
			model = x.usageModel
		}
		x.append(core.KindStepEnd, core.StepEnd{V: core.PayloadVersion, Step: 1, Provider: x.provider, Model: model, Usage: x.usage, Effort: x.stepEffort(model)})
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
	// REQ-MEM-010, REQ-CCT-007: memory capture runs after an external turn
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
	// an external runtime.
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

// externalOutcome is what an external attempt hands back to runExternalTurn.
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

// externalPrep is the part of an external turn every account of a pool
// shares: the stored owner message as the runtime input, and the prompt head
// and lana tools.
type externalPrep struct {
	input  []core.ContentBlock
	tools  []string
	specs  []core.ToolSpec
	system string
}

// externalPrepare stores the owner message with its <context> block and
// builds the runtime's prompt head and lana tool set. ok is false when the
// turn ends without running the runtime (a rejected message, a write error);
// out then says how.
func (rn *Runner) externalPrepare(ctx context.Context, x *externalTurn, initial inboxItem, continueReason, runtime string) (prep externalPrep, out externalOutcome, ok bool) {
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
			return prep, externalOutcome{stop: core.RuntimeStopCancelled}, false
		}
		return prep, externalOutcome{err: runtimeError(err)}, false
	}
	if decision.Reject {
		slog.Info("agent.pre_step rejected message", "session_id", rn.sessionID, "turn_id", x.turnID, "reason", decision.Reason)
		return prep, externalOutcome{}, false
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
	// native turn of this agent, without the tools the runtime has itself
	// (Claude Code and agy both have shell, file, and search tools).
	offered, unavailable, skills := rn.offeredTools(ctx, res)
	tools := claudeMCPTools(offered)
	specs, err := rn.reg.tools.Specs(tools)
	if err != nil {
		slog.Warn("lana tools for the external runtime", "session_id", rn.sessionID, "runtime", runtime, "error", err)
		specs, tools = nil, nil
	}
	// Spec 64 scope: the external runtime reads its own project files
	// (CLAUDE.md), so its prompt gets no project section.
	res.contextFiles = nil
	built, err := rn.buildPrompt(res, skills, unavailable, rn.toolNotes(tools), len(res.mcp) > 0, text, tools)
	if err != nil {
		return prep, externalOutcome{err: runtimeError(err)}, false
	}
	system := built.text
	if note := externalToolNote(runtime, tools); note != "" {
		system += "\n\n" + note
	}
	out = externalOutcome{specs: specs, system: system}

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
		return prep, out, false
	}
	out.owner = true
	return externalPrep{input: input.Content, tools: tools, specs: specs, system: system}, out, true
}

// externalAccount returns the config of the account of target in runtime.
func externalAccount(llm core.LLMRegistry, runtime, provider string) (core.ProviderConfig, bool) {
	if runtime == llmcall.Antigravity {
		return llmcall.AntigravityAccount(llm, provider)
	}
	return llmcall.ClaudeCodeAccount(llm, provider)
}

// externalAttempt runs the turn on one account of the runtime.
func (rn *Runner) externalAttempt(ctx context.Context, x *externalTurn, prep externalPrep, target core.LLMTarget) externalOutcome {
	res := x.agent
	runtime := x.runtime
	out := externalOutcome{owner: true, specs: prep.specs, system: prep.system}
	account, ok := externalAccount(rn.reg.llm, runtime, target.Provider)
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

	rt, err := rn.reg.Runtime(runtime)
	if err != nil {
		out.early = &core.RuntimeEarlyError{Class: "runtime", Message: err.Error()}
		return out
	}
	chain, err := rn.reg.store.ChainEvents(x.writeCtx, rn.sessionID)
	if err != nil {
		out.err = runtimeError(err)
		return out
	}
	st := externalResume(chain, runtime, target.Provider)
	if st.switched {
		// REQ-CCT-003, REQ-CCT-010: a turn ran in lana's loop or on another
		// account since the stored runtime session, so this turn starts a new
		// one.
		rt.Close(rn.sessionID)
	}
	if st.resumeID != "" && st.model != "" {
		// A live process sends no new session meta, so the stored one names
		// the model; a new process sends its own before the step starts.
		x.model = st.model
	}
	if x.model == "" {
		x.model = "default"
	}
	input := prep.input
	if st.resumeID == "" {
		// REQ-PAF-002: a new runtime session gets the conversation so far from
		// lana's own log.
		if block := movedContext(chain, x.turnID, st.previous, target.Provider); block != "" {
			input = append([]core.ContentBlock{{Kind: core.BlockText, Text: block}}, input...)
		}
	}

	// REQ-CCT-006: lana's tools over the loopback MCP host. A host that
	// cannot serve leaves the runtime without lana's tools, with a note.
	var openMCP func() (string, func(), error)
	mcpKey := ""
	if host := rn.reg.toolHostFor(); host != nil && len(prep.specs) > 0 {
		// REQ-DKN-021, REQ-DKN-032: a tool sees an owner turn only for a root
		// turn that stored an owner message, the same value captureEligible
		// uses (REQ-MEM-010).
		ownerTurn := !res.child
		if err := host.Bind(rn.sessionID, prep.specs, rn.claudeToolCall(ctx, x.turnID, res, prep.tools, ownerTurn)); err != nil {
			slog.Warn("lana tools are not available to the external runtime", "session_id", rn.sessionID, "runtime", runtime, "error", err)
			x.append(core.KindSystemMessage, core.SystemMessage{V: core.PayloadVersion, Subtype: core.SystemSubtypeNote,
				Text: "lana's tools are not available to " + runtimeTitle(runtime) + " in this turn: " + err.Error()})
		} else {
			defer host.Unbind(rn.sessionID)
			sessionID := rn.sessionID
			openMCP = func() (string, func(), error) { return host.Open(sessionID) }
			mcpKey = strings.Join(prep.tools, ",")
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
			Model: model, Workspace: res.workspace,
			AppendSystemPrompt: prep.system, MCPKey: mcpKey,
		},
		ResumeID: st.resumeID,
		Input:    input,
		Emit:     x.handle,
		Approve:  rn.externalApprove(x.turnID, res.name),
		OpenMCP:  openMCP,
	}
	// Spec 76 REQ-MES-003: Claude Code gets --effort only for a level it
	// lists ("off" and other providers' levels use its default); Antigravity
	// picks the variant of the base model for the effort (REQ-MES-005).
	switch runtime {
	case llmcall.ClaudeCode:
		turn.Config.Effort = models.PickEffort(res.runtimeEffort, models.ClaudeCodeEfforts(), "")
		x.effort = turn.Config.Effort
	case llmcall.Antigravity:
		turn.Config.Effort = res.runtimeEffort
	}
	applyAccount(&turn.Config, rn.reg.home, runtime, target.Provider, account, res.cfg)
	rn.setExternal(rt)
	defer rn.setExternal(nil)
	runErr := rt.RunTurn(ctx, turn)
	var early *core.RuntimeEarlyError
	if errors.As(runErr, &early) && !x.content && ctx.Err() == nil {
		out.early = early
		return out
	}
	x.flushMeta()
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

// runtimeTitle is the product name of an external runtime in notes.
func runtimeTitle(runtime string) string {
	if runtime == llmcall.Antigravity {
		return "Antigravity"
	}
	return "Claude Code"
}

// applyAccount sets the account keys of an external turn. Claude Code
// (REQ-CCT-010): the built-in account takes the binary, auth, and api_key_env
// of its optional [providers.claude-code] table (API-CCT-027), else the
// agent's (the keys of spec 38 agents), and keeps the daemon's
// CLAUDE_CONFIG_DIR; another account takes its own keys and folder.
// Antigravity (REQ-AGY-001): the account's binary, and its HOME ("" for the
// built-in account, which runs with the daemon's HOME).
func applyAccount(cfg *core.RuntimeConfig, home, runtime, name string, account core.ProviderConfig, agent core.AgentConfig) {
	cfg.Account = name
	if runtime == llmcall.Antigravity {
		cfg.Binary = account.Binary
		cfg.Home = llmcall.AntigravityHomeDir(home, name, account)
		return
	}
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
		Runtime: x.runtime, Provider: x.provider, Requested: x.requested, Agent: x.agent.name,
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
