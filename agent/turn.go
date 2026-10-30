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
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

// Runner holds the state of one session.
type Runner struct {
	reg       *Registry
	sessionID string

	mu      sync.Mutex
	inbox   []string
	running bool
	cancel  context.CancelCauseFunc
	turnID  string
}

func (rn *Runner) popInbox() (string, bool) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if len(rn.inbox) == 0 {
		return "", false
	}
	text := rn.inbox[0]
	rn.inbox = rn.inbox[1:]
	return text, true
}

func (rn *Runner) runChain(ctx context.Context, initial, turnID, continueReason string) {
	for {
		result := rn.runTurn(ctx, initial, turnID, continueReason)
		rn.reg.finishTurn(turnID, result)
		// Only the first turn of a chain is a continue; any message that
		// arrives while it runs starts a plain turn.
		continueReason = ""

		rn.reg.mu.Lock()
		if rn.reg.closed {
			rn.mu.Lock()
			rn.running = false
			rn.cancel = nil
			rn.turnID = ""
			rn.mu.Unlock()
			rn.reg.mu.Unlock()
			return
		}
		rn.mu.Lock()
		if len(rn.inbox) == 0 {
			rn.running = false
			rn.cancel = nil
			rn.turnID = ""
			rn.mu.Unlock()
			rn.reg.mu.Unlock()
			return
		}
		next := rn.inbox[0]
		rn.inbox = rn.inbox[1:]
		turnID = id.New()
		nextCtx, cancel := context.WithCancelCause(rn.reg.root)
		rn.cancel = cancel
		rn.turnID = turnID
		rn.running = true
		rn.reg.turns[turnID] = &turnState{done: make(chan struct{})}
		rn.mu.Unlock()
		rn.reg.mu.Unlock()
		ctx = nextCtx
		initial = next
	}
}

type resolvedAgent struct {
	name       string
	team       string
	delegates  []string
	roster     []agenthome.RosterEntry
	provider   string
	model      string
	promptFile string
	channel    string
	workspace  string
	tools      []string
	disabled   []string
	mcp        []string
	skills     []string
	fallback   []core.LLMTarget
	maxSteps   int
	child      bool
}

func (rn *Runner) resolve(ctx context.Context) (resolvedAgent, error) {
	info, err := rn.reg.store.Info(ctx, rn.sessionID)
	if err != nil {
		return resolvedAgent{}, err
	}
	chain, err := rn.reg.store.ChainEvents(ctx, rn.sessionID)
	if err != nil {
		return resolvedAgent{}, err
	}
	agentName := info.Agent
	child := info.ParentSessionID != ""
	var selection core.ModelSelection
	hasSelection := false
	for _, event := range chain {
		if event.Kind != core.KindModelSelection {
			continue
		}
		var payload core.ModelSelection
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return resolvedAgent{}, err
		}
		if payload.Agent != "" {
			agentName = payload.Agent
		}
		selection = payload
		hasSelection = true
	}
	cfgName := agentName
	teamName := ""
	var team core.TeamConfig
	agents, teams := rn.reg.configSnapshot()
	if teamCfg, ok := teams[agentName]; ok {
		team = teamCfg
		teamName = agentName
		cfgName = team.Lead
	}
	cfg, ok := agents[cfgName]
	if !ok {
		return resolvedAgent{}, fmt.Errorf("%w: %s", ErrUnknownAgent, cfgName)
	}
	provider, model := cfg.Provider, cfg.Model
	if hasSelection && teamName == "" {
		if selection.Provider != "" {
			provider = selection.Provider
		}
		if selection.Model != "" {
			model = selection.Model
		}
	}
	if provider == "" || model == "" {
		return resolvedAgent{}, fmt.Errorf("agent %s has no provider or model set", cfgName)
	}
	workspace := info.Workspace
	if workspace == "" {
		workspace = cfg.Workspace
	}
	if workspace == "" {
		workspace = rn.reg.defaultWS
	}
	workspace, err = normalizePath(workspace)
	if err != nil {
		return resolvedAgent{}, err
	}
	maxSteps := cfg.MaxSteps
	if maxSteps == 0 {
		maxSteps = 25
		if child {
			maxSteps = 15
		}
	}
	fallback, err := parseFallback(cfg.Fallback)
	if err != nil {
		return resolvedAgent{}, err
	}
	delegates := append([]string{}, cfg.Subagents...)
	var roster []agenthome.RosterEntry
	if teamName != "" {
		delegates = mergeDelegates(team.Members, cfg.Subagents)
		roster = make([]agenthome.RosterEntry, 0, len(delegates))
		for _, name := range delegates {
			entry := agenthome.RosterEntry{Name: name}
			if delegate, ok := agents[name]; ok {
				entry.Description = delegate.Description
			}
			roster = append(roster, entry)
		}
	}
	mcpServers := cfg.MCP
	if overlay, ok := rn.reg.agentServers(cfgName); ok {
		mcpServers = overlay
	}
	return resolvedAgent{
		name:       cfgName,
		team:       teamName,
		delegates:  delegates,
		roster:     roster,
		provider:   provider,
		model:      model,
		promptFile: cfg.PromptFile,
		channel:    info.Channel,
		workspace:  workspace,
		tools:      cfg.Tools,
		disabled:   cfg.DisabledTools,
		mcp:        mcpServers,
		skills:     cfg.Skills,
		fallback:   fallback,
		maxSteps:   maxSteps,
		child:      info.ParentSessionID != "",
	}, nil
}

// mergeDelegates joins groups in order and drops duplicates.
func mergeDelegates(groups ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, group := range groups {
		for _, name := range group {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func parseFallback(list []string) ([]core.LLMTarget, error) {
	out := make([]core.LLMTarget, 0, len(list))
	for _, item := range list {
		provider, model, ok := strings.Cut(item, ":")
		if !ok || provider == "" || model == "" {
			return nil, fmt.Errorf("invalid fallback: %s", item)
		}
		out = append(out, core.LLMTarget{Provider: provider, Model: model})
	}
	return out, nil
}

func normalizePath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

func (rn *Runner) promptPath(res resolvedAgent) (string, error) {
	if res.promptFile == "" {
		return "", nil
	}
	path := res.promptFile
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	} else if !filepath.IsAbs(path) {
		base := rn.reg.home
		if base == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			base = cwd
		}
		path = filepath.Join(base, path)
	}
	return path, nil
}

// buildPrompt assembles the system prompt from the persona, the agent home files, the skills, and the runtime context.
func (rn *Runner) buildPrompt(res resolvedAgent, skills []agenthome.Skill, unavailable map[string]string) (string, error) {
	in := agenthome.PromptInput{
		Agent: res.name, Channel: res.channel, Workspace: res.workspace, Now: time.Now(),
		Skills: skills, Roster: res.roster, UnavailableMCP: unavailable,
	}
	var home agenthome.Home
	if rn.reg.home != "" {
		home = agenthome.Paths(rn.reg.home, res.name)
	}
	path, err := rn.promptPath(res)
	if err != nil {
		return "", err
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("prompt_file: %w", err)
		}
		in.Persona = string(data)
	} else if home.Agent != "" {
		data, err := os.ReadFile(home.Agent)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("cannot read persona", "agent", res.name, "error", err)
		}
		in.Persona = string(data)
	}
	if home.Dir != "" {
		if in.User, err = agenthome.ReadEntries(agenthome.SharedUser(rn.reg.home)); err != nil {
			slog.Warn("cannot read user file", "agent", res.name, "error", err)
		}
		if in.Memory, err = agenthome.ReadEntries(home.Memory); err != nil {
			slog.Warn("cannot read memory file", "agent", res.name, "error", err)
		}
	}
	slog.Debug("system prompt", "agent", res.name, "user_chars", agenthome.Size(in.User), "memory_chars", agenthome.Size(in.Memory))
	return agenthome.Build(in), nil
}

func (rn *Runner) lastSystemText(ctx context.Context) (string, error) {
	chain, err := rn.reg.store.ChainEvents(ctx, rn.sessionID)
	if err != nil {
		return "", err
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].Kind != core.KindSystemMessage {
			continue
		}
		var payload core.SystemMessage
		if err := json.Unmarshal(chain[i].Payload, &payload); err != nil {
			return "", err
		}
		return payload.Text, nil
	}
	return "", nil
}

func (rn *Runner) runTurn(ctx context.Context, initial, turnID, continueReason string) (result TurnResult) {
	result = TurnResult{SessionID: rn.sessionID, TurnID: turnID}
	writeCtx := context.WithoutCancel(ctx)
	events := &Events{Store: rn.reg.store, SessionID: rn.sessionID}
	if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindTurnStart, TurnID: turnID, Payload: core.TurnStart{V: core.PayloadVersion, TurnID: turnID}}); err != nil {
		slog.Error("failed to write turn/start", "session_id", rn.sessionID, "turn_id", turnID, "error", err)
		result.StopReason = "error"
		result.Err = &core.ProviderError{Class: "runtime", Message: err.Error()}
		return result
	}
	reason := "end_turn"
	var runErr *core.ProviderError
	steps := 0
	hasInitial := true
	pendingTools := false
	continueNext := false
	lastText := ""
	// The empty-answer guard state (REQ-TFB-008 item 4).
	toolCalls := 0
	lastAssistantText := ""
	guarded := false
	prompt := ""
	promptBuilt := false
	turnTools := []string{}
	var totalUsage core.Usage
	for {
		claimedText := ""
		claimed := false
		// continueMsg marks the claimed message as a continue payload.
		continueMsg := false
		switch {
		case hasInitial:
			claimedText = initial
			claimed = true
			hasInitial = false
			continueMsg = continueReason != ""
		case pendingTools:
			claimedText, claimed = rn.popInbox()
		case continueNext:
			continueNext = false
			claimedText, claimed = rn.popInbox()
		default:
			var ok bool
			claimedText, ok = rn.popInbox()
			if !ok {
				// REQ-TFB-008 item 4: a turn that would end with end_turn
				// after a tool call but with no text in its last assistant
				// message runs exactly one more step asking for a report.
				if !guarded && toolCalls > 0 && lastAssistantText == "" {
					guarded = true
					claimedText = emptyAnswerNote
				} else {
					goto finished
				}
			}
			claimed = true
		}

		inputMsg := core.Message{Role: core.RoleUser}
		if claimed {
			inputMsg.Content = []core.ContentBlock{{Kind: core.BlockText, Text: claimedText}}
			decision, err := rn.reg.hooks.AgentPreStep.Call(ctx, core.PreStepInput{
				SessionID: rn.sessionID, TurnID: turnID, AgentName: "", Step: steps + 1, Input: inputMsg,
			})
			if err != nil {
				runErr = runtimeError(err)
				reason = "error"
				goto finished
			}
			if decision.Reject {
				slog.Info("agent.pre_step rejected message", "session_id", rn.sessionID, "turn_id", turnID, "reason", decision.Reason)
				goto finished
			}
			if len(decision.Input.Content) > 0 {
				inputMsg = decision.Input
			}
		}

		resolved, err := rn.resolve(ctx)
		if err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		// REQ-WS-001: create a missing session workspace before any tool runs.
		// A failure is logged and not fatal; bash, glob, and grep then report a
		// clear workspace error instead of an operating system error.
		if resolved.workspace != "" {
			if _, statErr := os.Stat(resolved.workspace); statErr != nil {
				if mkErr := os.MkdirAll(resolved.workspace, 0o700); mkErr != nil {
					slog.Warn("workspace create failed", "workspace", resolved.workspace, "error", mkErr)
				}
			}
		}
		if steps >= resolved.maxSteps {
			reason = "max_steps"
			goto finished
		}
		if !promptBuilt {
			turnTools = append(turnTools, resolved.tools...)
			var unavailable map[string]string
			if len(resolved.mcp) > 0 {
				if mcp := rn.reg.mcpManager(); mcp != nil {
					names, unavail := mcp.Ensure(ctx, resolved.mcp)
					turnTools = append(turnTools, names...)
					unavailable = unavail
				}
				// REQ-TFB-008 item 2: every server the runtime does not know
				// is listed as "not loaded".
				unavailable = unknownMCP(resolved.mcp, turnTools, unavailable)
			}
			var skills []agenthome.Skill
			if rn.reg.home != "" {
				skills, _ = agenthome.VisibleSkills(rn.reg.home, resolved.name, resolved.skills)
			}
			// REQ-AMCP-001: skill_load, mcp_manage, and the team tools are
			// offered without being listed in tools. The dashboard catalog
			// reads the same helper so the two cannot drift.
			_, mcpManageRegistered := rn.reg.tools.Get("mcp_manage")
			for _, name := range AutoOfferedTools(AutoOfferState{
				Root:                !resolved.child,
				MCPManageRegistered: mcpManageRegistered,
				VisibleSkills:       len(skills) > 0,
				Team:                resolved.team != "",
			}) {
				if !containsString(turnTools, name) {
					turnTools = append(turnTools, name)
				}
			}
			// disabled_tools removes a name from the offered list. It is applied
			// after the built-in, MCP, skill, and team tools are collected so a
			// disabled team or skill tool is removed as well (AC-WEB-049, Q-024).
			turnTools = filterDisabledTools(turnTools, resolved.disabled)
			prompt, err = rn.buildPrompt(resolved, skills, unavailable)
			if err != nil {
				runErr = runtimeError(err)
				reason = "error"
				goto finished
			}
			promptBuilt = true
		}
		previousSystem, err := rn.lastSystemText(ctx)
		if err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		if prompt != previousSystem {
			if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindSystemMessage, TurnID: turnID, Payload: core.SystemMessage{V: core.PayloadVersion, Text: prompt}}); err != nil {
				runErr = runtimeError(err)
				reason = "error"
				goto finished
			}
		}
		if claimed {
			payload := core.UserMessage{V: core.PayloadVersion, Content: inputMsg.Content}
			if continueMsg {
				payload.Continue = true
				payload.ContinueReason = continueReason
			}
			if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindUserMessage, TurnID: turnID, Payload: payload}); err != nil {
				runErr = runtimeError(err)
				reason = "error"
				goto finished
			}
		}
		step := steps + 1
		if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindStepStart, TurnID: turnID, Payload: core.StepStart{V: core.PayloadVersion, Step: step, Provider: resolved.provider, Model: resolved.model}}); err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		steps = step

		history, err := rn.reg.store.DeriveMessages(writeCtx, rn.sessionID)
		if err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		specs, err := rn.reg.tools.Specs(turnTools)
		if err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		req := core.ChatRequest{Model: resolved.model, System: prompt, Messages: history, Tools: specs}
		control, err := rn.reg.hooks.AgentRequest.Call(ctx, core.RequestControl{})
		if err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		req.MaxTokens = control.MaxTokens
		req.Temperature = control.Temperature

		targets := append([]core.LLMTarget{{Provider: resolved.provider, Model: resolved.model}}, resolved.fallback...)
		stream, err := rn.reg.hooks.LLMStream.Call(ctx, core.LLMStreamInput{
			SessionID: rn.sessionID, TurnID: turnID, Step: step, AgentName: resolved.name, Targets: targets, Request: req,
		})
		if err != nil {
			if ctx.Err() != nil {
				reason = rn.writeStoppedAttempt(writeCtx, events, step, resolved, turnID, ctx)
				goto finished
			}
			runErr = providerError(err)
			reason = "error"
			goto finished
		}
		text, toolBlocks, usage, stop, streamErr := rn.consumeStream(ctx, stream, turnID)
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		if streamErr != nil {
			if ctx.Err() != nil {
				reason = rn.writeStoppedAttempt(writeCtx, events, step, resolved, turnID, ctx)
				goto finished
			}
			pe := providerError(streamErr)
			rn.writeAttempt(writeCtx, events, step, resolved, turnID, *pe)
			runErr = pe
			reason = "error"
			goto finished
		}
		content := make([]core.ContentBlock, 0, 1+len(toolBlocks))
		if text != "" {
			content = append(content, core.ContentBlock{Kind: core.BlockText, Text: text})
		}
		content = append(content, toolBlocks...)
		if stop == "" {
			stop = "end_turn"
		}
		if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindAssistantMessage, TurnID: turnID, Payload: core.AssistantMessage{V: core.PayloadVersion, Content: content, StopReason: stop}}); err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		if text != "" {
			lastText = text
		}
		// The empty-answer guard tracks the text of the last assistant message
		// and the tool calls of the whole turn (REQ-TFB-008 item 4).
		lastAssistantText = text
		toolCalls += len(toolBlocks)

		if len(toolBlocks) > 0 {
			calls := make([]core.ToolCall, 0, len(toolBlocks))
			for _, block := range toolBlocks {
				call := core.ToolCall{SessionID: rn.sessionID, TurnID: turnID, CallID: block.ID, Name: block.Name, Input: block.Input}
				calls = append(calls, call)
				if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindToolCall, TurnID: turnID, Payload: core.ToolCallPayload{V: core.PayloadVersion, CallID: block.ID, Name: block.Name, Input: block.Input}}); err != nil {
					runErr = runtimeError(err)
					reason = "error"
					goto finished
				}
				rn.reg.hub.Publish(rn.sessionID, core.LiveFrame{SessionID: rn.sessionID, TurnID: turnID, Kind: "tool_call", CallID: block.ID, Name: block.Name, Input: block.Input})
			}
			var mcpAdmin core.MCPAdmin
			if !resolved.child {
				mcpAdmin = rn.reg.mcpAdminFor()
			}
			outputs := rn.reg.pipeline.Run(ctx, calls, turnTools, core.ToolCtx{
				SessionID: rn.sessionID, TurnID: turnID, AgentName: resolved.name, Workspace: resolved.workspace, Home: rn.reg.home,
				Skills:        resolved.skills,
				Questions:     rn.reg.questions,
				Subagents:     &subagentRunner{reg: rn.reg, allowed: resolved.delegates},
				MCPAdmin:      mcpAdmin,
				MCPStatus:     rn.reg.ServerStatus,
				AllowedAgents: resolved.delegates,
				Depth:         sessionDepth(ctx, rn.reg.store, rn.sessionID),
			})
			// Child usage from task and task_continue belongs to this step's
			// usage (REQ-TEAM-013, AC-TEAM-031). Nil usage is ignored and the
			// child's own step/end keeps the child's model usage.
			for _, output := range outputs {
				if output.Usage == nil {
					continue
				}
				usage.InputTokens += output.Usage.InputTokens
				usage.OutputTokens += output.Usage.OutputTokens
			}
			for i, output := range outputs {
				status := output.Status
				if status == "" {
					status = core.ToolStatusError
				}
				if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindToolResult, TurnID: turnID, Payload: core.ToolResultPayload{V: core.PayloadVersion, CallID: calls[i].CallID, Status: status, Output: output.Output}}); err != nil {
					runErr = runtimeError(err)
					reason = "error"
					goto finished
				}
				rn.reg.hub.Publish(rn.sessionID, core.LiveFrame{SessionID: rn.sessionID, TurnID: turnID, Kind: "tool_result", CallID: calls[i].CallID, Name: calls[i].Name, Status: string(status), Output: output.Output})
			}
		}
		if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindStepEnd, TurnID: turnID, Payload: core.StepEnd{V: core.PayloadVersion, Step: step, Provider: resolved.provider, Model: resolved.model, Usage: usage}}); err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		if ctx.Err() != nil {
			reason = "cancelled"
			goto finished
		}
		pendingTools = len(toolBlocks) > 0
		if pendingTools {
			continue
		}
		votes, err := rn.reg.hooks.AgentTurnStopping.Call(ctx, core.TurnStopInput{
			SessionID: rn.sessionID, TurnID: turnID, AgentName: resolved.name, Steps: steps, StopReason: "end_turn",
		})
		if err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		for _, vote := range votes {
			if vote.Continue {
				continueNext = true
				break
			}
		}
	}

finished:
	// A setup step can fail only because the turn context ended. That is a
	// stop, not a runtime error, so the context mapping below applies
	// (REQ-TFB-001).
	if runErr != nil && ctx.Err() != nil && runErr.Class == "runtime" && runErr.Message == context.Canceled.Error() {
		runErr = nil
		reason = ""
	}
	var payloadErr *core.PayloadError
	if ctx.Err() != nil && reason != "error" {
		if message, ok := interruptedMessage(ctx); ok {
			// API-TFB-001: a shutdown interrupt is not a user cancel.
			reason = "interrupted"
			payloadErr = &core.PayloadError{Message: message}
		} else {
			reason = "cancelled"
		}
	}
	if runErr != nil {
		reason = "error"
		payloadErr = &core.PayloadError{Class: runErr.Class, Status: runErr.Status, Message: runErr.Message}
	}
	end := core.TurnEnd{V: core.PayloadVersion, TurnID: turnID, Reason: reason, Error: payloadErr}
	if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindTurnEnd, TurnID: turnID, Payload: end}); err != nil {
		slog.Error("failed to write turn/end", "session_id", rn.sessionID, "turn_id", turnID, "error", err)
	}
	result.StopReason = reason
	result.Text = lastText
	result.Usage = totalUsage
	result.Err = runErr
	return result
}

func (rn *Runner) consumeStream(ctx context.Context, stream core.ChatStream, turnID string) (string, []core.ContentBlock, core.Usage, string, error) {
	var text strings.Builder
	var blocks []core.ContentBlock
	var usage core.Usage
	stop := "end_turn"
	for event, err := range stream {
		if err != nil {
			return text.String(), blocks, usage, stop, err
		}
		switch event.Kind {
		case core.ChatTextDelta:
			text.WriteString(event.Text)
			rn.reg.hub.Publish(rn.sessionID, core.LiveFrame{SessionID: rn.sessionID, TurnID: turnID, Kind: "text_delta", Text: event.Text})
		case core.ChatBlock:
			if event.Block.Kind == core.BlockText {
				text.WriteString(event.Block.Text)
			} else {
				blocks = append(blocks, event.Block)
			}
		case core.ChatUsage:
			usage = event.Usage
		case core.ChatStop:
			stop = event.StopReason
		}
	}
	if err := ctx.Err(); err != nil {
		return text.String(), blocks, usage, stop, err
	}
	return text.String(), blocks, usage, stop, nil
}

func (rn *Runner) writeAttempt(ctx context.Context, events *Events, step int, resolved resolvedAgent, turnID string, pe core.ProviderError) {
	payload := core.AssistantAttempt{V: core.PayloadVersion, Step: step, Attempt: 1, Provider: resolved.provider, Model: resolved.model, Error: core.PayloadError(pe)}
	_, _ = events.Append(ctx, core.EventDraft{Kind: core.KindAssistantAttempt, TurnID: turnID, Payload: payload})
}

func (rn *Runner) writeCancelledAttempt(ctx context.Context, events *Events, step int, resolved resolvedAgent, turnID string) {
	payload := core.AssistantAttempt{V: core.PayloadVersion, Step: step, Attempt: 1, Provider: resolved.provider, Model: resolved.model, Error: core.PayloadError{Class: "cancelled", Message: "cancelled"}}
	_, _ = events.Append(ctx, core.EventDraft{Kind: core.KindAssistantAttempt, TurnID: turnID, Payload: payload})
}

// writeStoppedAttempt records the attempt of a turn cut by its context and
// returns the turn/end reason. An interrupt cause yields "interrupted"; a user
// cancel yields "cancelled" (REQ-TFB-001, API-TFB-001).
func (rn *Runner) writeStoppedAttempt(ctx context.Context, events *Events, step int, resolved resolvedAgent, turnID string, turnCtx context.Context) string {
	if message, ok := interruptedMessage(turnCtx); ok {
		payload := core.AssistantAttempt{V: core.PayloadVersion, Step: step, Attempt: 1, Provider: resolved.provider, Model: resolved.model, Error: core.PayloadError{Class: "interrupted", Message: message}}
		_, _ = events.Append(ctx, core.EventDraft{Kind: core.KindAssistantAttempt, TurnID: turnID, Payload: payload})
		return "interrupted"
	}
	rn.writeCancelledAttempt(ctx, events, step, resolved, turnID)
	return "cancelled"
}

// interruptedMessage reports the message of an interrupt cancel cause.
func interruptedMessage(ctx context.Context) (string, bool) {
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, core.ErrInterruptedRestart), errors.Is(cause, core.ErrInterruptedStop):
		return cause.Error(), true
	default:
		return "", false
	}
}

// stopToolOutput is the result of a tool call cut by ctx ending. An interrupt
// cause reports "interrupted"; a user cancel keeps "cancelled" (REQ-TFB-001).
func stopToolOutput(ctx context.Context) core.ToolOutput {
	if _, ok := interruptedMessage(ctx); ok {
		return core.ToolOutput{Status: core.ToolStatusError, Output: "interrupted"}
	}
	return core.ToolOutput{Status: core.ToolStatusError, Output: "cancelled"}
}

// containsString reports whether list holds name.
func containsString(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

// emptyAnswerNote is the runtime note of the empty-answer guard (REQ-TFB-008
// item 4). It is the user-role message of the one extra step.
const emptyAnswerNote = "Tell the owner what you did, what failed, and what is still needed."

// The prompt reasons of the unavailable MCP list (REQ-TFB-008 item 2).
const (
	notLoadedReason     = "not loaded"
	notConfiguredReason = "not configured"
)

// unknownMCP returns the prompt's unavailable MCP list. A listed server the
// runtime does not know is "not loaded"; a listed server that offered no tool
// and has no reason is unknown as well (REQ-TFB-008 item 2, AC-TFB-019).
func unknownMCP(servers, tools []string, unavailable map[string]string) map[string]string {
	out := make(map[string]string, len(servers))
	for _, name := range servers {
		reason, ok := unavailable[name]
		if ok && reason == notConfiguredReason {
			reason = notLoadedReason
		}
		if !ok {
			if mcpServerOffered(tools, name) {
				continue
			}
			reason = notLoadedReason
		}
		out[name] = reason
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mcpServerOffered reports whether the offered tools include one of an MCP
// server.
func mcpServerOffered(tools []string, server string) bool {
	prefix := "mcp__" + server + "__"
	for _, name := range tools {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// filterDisabledTools drops every name in disabled from tools. An entry is a
// plain tool name ("bash") or an MCP reference ("github/create_issue"). The
// MCP reference matches the model-facing "mcp__github__create_issue" name.
// Unknown names are ignored and the input order is kept (Q-024).
func filterDisabledTools(tools, disabled []string) []string {
	if len(disabled) == 0 {
		return tools
	}
	blocked := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		if name != "" {
			blocked[name] = true
		}
	}
	out := make([]string, 0, len(tools))
	for _, name := range tools {
		if blocked[name] {
			continue
		}
		if ref, ok := mcpToolRef(name); ok && blocked[ref] {
			continue
		}
		out = append(out, name)
	}
	return out
}

// mcpToolRef turns a model-facing MCP tool name into the config-facing
// "<server>/<tool>" reference. Names that are not MCP tools return false.
func mcpToolRef(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return "", false
	}
	i := strings.Index(rest, "__")
	if i <= 0 || i+2 >= len(rest) {
		return "", false
	}
	return rest[:i] + "/" + rest[i+2:], true
}

func runtimeError(err error) *core.ProviderError {
	return &core.ProviderError{Class: "runtime", Message: err.Error()}
}

func providerError(err error) *core.ProviderError {
	var pe *core.ProviderError
	if errors.As(err, &pe) {
		return pe
	}
	return &core.ProviderError{Class: "runtime", Message: err.Error()}
}
