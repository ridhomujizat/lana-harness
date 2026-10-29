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
	cancel  context.CancelFunc
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

func (rn *Runner) runChain(ctx context.Context, initial, turnID string) {
	for {
		result := rn.runTurn(ctx, initial, turnID)
		rn.reg.finishTurn(turnID, result)

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
		nextCtx, cancel := context.WithCancel(rn.reg.root)
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
	provider   string
	model      string
	promptFile string
	channel    string
	workspace  string
	tools      []string
	fallback   []core.LLMTarget
	maxSteps   int
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
	cfg, ok := rn.reg.config.Agents[agentName]
	if !ok {
		return resolvedAgent{}, fmt.Errorf("%w: %s", ErrUnknownAgent, agentName)
	}
	provider, model := cfg.Provider, cfg.Model
	if hasSelection {
		if selection.Provider != "" {
			provider = selection.Provider
		}
		if selection.Model != "" {
			model = selection.Model
		}
	}
	if provider == "" || model == "" {
		return resolvedAgent{}, fmt.Errorf("agent %s has no provider or model set", agentName)
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
	}
	fallback, err := parseFallback(cfg.Fallback)
	if err != nil {
		return resolvedAgent{}, err
	}
	return resolvedAgent{
		name:       agentName,
		provider:   provider,
		model:      model,
		promptFile: cfg.PromptFile,
		channel:    info.Channel,
		workspace:  workspace,
		tools:      cfg.Tools,
		fallback:   fallback,
		maxSteps:   maxSteps,
	}, nil
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

// buildPrompt assembles the system prompt from the persona, the agent home files, and the runtime context.
func (rn *Runner) buildPrompt(res resolvedAgent) (string, error) {
	in := agenthome.PromptInput{Agent: res.name, Channel: res.channel, Workspace: res.workspace, Now: time.Now()}
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
		if in.User, err = agenthome.ReadEntries(home.User); err != nil {
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

func (rn *Runner) runTurn(ctx context.Context, initial, turnID string) (result TurnResult) {
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
	prompt := ""
	promptBuilt := false
	var totalUsage core.Usage
	for {
		claimedText := ""
		claimed := false
		switch {
		case hasInitial:
			claimedText = initial
			claimed = true
			hasInitial = false
		case pendingTools:
			claimedText, claimed = rn.popInbox()
		case continueNext:
			continueNext = false
			claimedText, claimed = rn.popInbox()
		default:
			var ok bool
			claimedText, ok = rn.popInbox()
			if !ok {
				goto finished
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
		if steps >= resolved.maxSteps {
			reason = "max_steps"
			goto finished
		}
		if !promptBuilt {
			prompt, err = rn.buildPrompt(resolved)
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
			if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindUserMessage, TurnID: turnID, Payload: core.UserMessage{V: core.PayloadVersion, Content: inputMsg.Content}}); err != nil {
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
		specs, err := rn.reg.tools.Specs(resolved.tools)
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
				rn.writeCancelledAttempt(writeCtx, events, step, resolved, turnID)
				reason = "cancelled"
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
				rn.writeCancelledAttempt(writeCtx, events, step, resolved, turnID)
				reason = "cancelled"
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
			outputs := rn.reg.pipeline.Run(ctx, calls, resolved.tools, core.ToolCtx{
				SessionID: rn.sessionID, TurnID: turnID, AgentName: resolved.name, Workspace: resolved.workspace, Home: rn.reg.home,
			})
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
	if ctx.Err() != nil && reason != "error" {
		reason = "cancelled"
	}
	var payloadErr *core.PayloadError
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
