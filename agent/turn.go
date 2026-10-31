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
	"strconv"
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

// pushFrontInbox returns text to the front of the inbox, so a message the step
// cap took but did not use still starts the next turn.
func (rn *Runner) pushFrontInbox(text string) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.inbox = append([]string{text}, rn.inbox...)
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
	// REQ-LTC-040: max_steps is opt-in; 0 means no limit for root and child
	// sessions alike.
	maxSteps := cfg.MaxSteps
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

// promptBuild is the result of building one turn's prompt: the full head text,
// its named sections for diffing, and the <context> block for the first user
// message.
type promptBuild struct {
	text     string
	sections []core.PromptSection
	context  string
}

// buildPrompt assembles the system prompt from the persona, the agent home files, the skills, and the runtime context.
func (rn *Runner) buildPrompt(res resolvedAgent, skills []agenthome.Skill, unavailable map[string]string, memoryCapture bool) (promptBuild, error) {
	in := agenthome.PromptInput{
		Agent: res.name, Channel: res.channel, Workspace: res.workspace, Now: time.Now(),
		Skills: skills, Roster: res.roster, UnavailableMCP: unavailable, MemoryCapture: memoryCapture,
	}
	var home agenthome.Home
	if rn.reg.home != "" {
		home = agenthome.Paths(rn.reg.home, res.name)
	}
	path, err := rn.promptPath(res)
	if err != nil {
		return promptBuild{}, err
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return promptBuild{}, fmt.Errorf("prompt_file: %w", err)
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
	return promptBuild{text: agenthome.Build(in), sections: promptSections(in), context: agenthome.ContextBlock(in)}, nil
}

// promptSections converts the agenthome sections to their core form.
func promptSections(in agenthome.PromptInput) []core.PromptSection {
	built := agenthome.BuildSections(in)
	out := make([]core.PromptSection, 0, len(built))
	for _, section := range built {
		out = append(out, core.PromptSection{Name: section.Name, Text: section.Text})
	}
	return out
}

// promptHead is the stable prefix of a session's requests: the latest
// system/message, its section set, and whether a compaction landed after it
// (REQ-LTC-002).
type promptHead struct {
	exists    bool
	text      string
	compacted bool
	effective []core.PromptSection
}

// promptHead reads the head and the effective sections (the head sections
// overlaid with every PromptSections stored on a later user/message).
func (rn *Runner) promptHead(ctx context.Context) (promptHead, error) {
	chain, err := rn.reg.store.ChainEvents(ctx, rn.sessionID)
	if err != nil {
		return promptHead{}, err
	}
	head := promptHead{}
	for _, event := range chain {
		switch event.Kind {
		case core.KindSystemMessage:
			var payload core.SystemMessage
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return promptHead{}, err
			}
			head.exists = true
			head.text = payload.Text
			head.compacted = false
			head.effective = append([]core.PromptSection(nil), payload.Sections...)
		case core.KindCompactionSummary:
			head.compacted = true
		case core.KindUserMessage:
			var payload core.UserMessage
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return promptHead{}, err
			}
			if len(payload.PromptSections) > 0 {
				head.effective = append([]core.PromptSection(nil), payload.PromptSections...)
			}
		}
	}
	return head, nil
}

// systemUpdate renders the <system-update> block for the sections that changed
// or were removed since the head. It returns "" when nothing changed
// (REQ-LTC-002).
func systemUpdate(next, prev []core.PromptSection) string {
	prevByName := make(map[string]string, len(prev))
	order := make([]string, 0, len(prev))
	for _, section := range prev {
		if _, ok := prevByName[section.Name]; !ok {
			order = append(order, section.Name)
		}
		prevByName[section.Name] = section.Text
	}
	nextSeen := make(map[string]bool, len(next))
	changed := []string{}
	for _, section := range next {
		nextSeen[section.Name] = true
		if prevByName[section.Name] != section.Text {
			changed = append(changed, section.Text)
		}
	}
	for _, name := range order {
		if nextSeen[name] {
			continue
		}
		changed = append(changed, removedHeading(prevByName[name]))
	}
	if len(changed) == 0 {
		return ""
	}
	lines := []string{
		"<system-update>",
		"The following sections of your instructions changed. They replace the earlier versions.",
	}
	lines = append(lines, changed...)
	lines = append(lines, "</system-update>")
	return strings.Join(lines, "\n")
}

// removedHeading names a section that is no longer present: its heading when it
// has one, else its first line.
func removedHeading(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return text
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
	// toolCallTextNudges counts the malformed tool-call nudges done this turn
	// (REQ-MEM-020); at most maxToolCallTextNudges per turn.
	toolCallTextNudges := 0
	// budgetReport marks the final, tool-free report step that a configured
	// step cap adds before the turn ends (REQ-LTC-040).
	budgetReport := false
	prompt := ""
	promptBuilt := false
	turnTools := []string{}
	var totalUsage core.Usage
	for {
		claimedText := ""
		claimed := false
		// runtimeNote marks the claimed text as a note lana wrote itself, not
		// an owner message (REQ-MEM-021). It is reset every step.
		runtimeNote := false
		// inboxClaim marks the claimed text as a message popInbox took from the
		// inbox, so the step cap can hand it to the next turn instead of losing
		// it (REQ-LTC-040).
		inboxClaim := false
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
			inboxClaim = claimed
		case continueNext:
			continueNext = false
			claimedText, claimed = rn.popInbox()
			inboxClaim = claimed
		default:
			var ok bool
			claimedText, ok = rn.popInbox()
			if !ok {
				// REQ-MEM-020: a reply that wrote a tool call as plain text
				// ran nothing. Nudge it to use the native interface, at most
				// maxToolCallTextNudges times per turn.
				if toolCallTextNudges < maxToolCallTextNudges && looksLikeToolCallText(lastAssistantText) {
					toolCallTextNudges++
					claimedText = toolCallTextNote
					runtimeNote = true
				} else if !guarded && toolCalls > 0 && lastAssistantText == "" {
					// REQ-TFB-008 item 4: a turn that would end with end_turn
					// after a tool call but with no text in its last assistant
					// message runs exactly one more step asking for a report.
					guarded = true
					claimedText = emptyAnswerNote
					runtimeNote = true
				} else {
					goto finished
				}
			} else {
				inboxClaim = true
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
		// REQ-LTC-040: a positive cap runs one final step without tools that
		// asks for a status report, then the turn ends with reason max_steps.
		if resolved.maxSteps > 0 && steps >= resolved.maxSteps {
			// A message popInbox already claimed stays for the next turn; the
			// cap replaces the model input, not the owner's queued message.
			if inboxClaim {
				rn.pushFrontInbox(claimedText)
			}
			if budgetReport {
				reason = "max_steps"
				goto finished
			}
			budgetReport = true
			claimed = true
			runtimeNote = true
			claimedText = stepLimitNote
			inputMsg = core.Message{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: stepLimitNote}}}
		}
		var storedSections []core.PromptSection
		// contextBlocks is the number of leading content blocks lana injects
		// into the turn's first user message; it is recorded on the stored
		// payload so display surfaces can hide them (REQ-LTC-001).
		contextBlocks := 0
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
			// REQ-LTC-003: a deterministic tool order keeps the prefix stable.
			sort.Strings(turnTools)
			built, err := rn.buildPrompt(resolved, skills, unavailable, containsString(turnTools, "memory_write"))
			if err != nil {
				runErr = runtimeError(err)
				reason = "error"
				goto finished
			}
			head, err := rn.promptHead(ctx)
			if err != nil {
				runErr = runtimeError(err)
				reason = "error"
				goto finished
			}
			// REQ-LTC-002: rewrite the head only without a head or after a
			// compaction; every other change goes out as a <system-update>.
			prompt = head.text
			update := ""
			if !head.exists || head.compacted || len(head.effective) == 0 { // no sections: a head from before REQ-LTC-002
				if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindSystemMessage, TurnID: turnID, Payload: core.SystemMessage{V: core.PayloadVersion, Text: built.text, Sections: built.sections}}); err != nil {
					runErr = runtimeError(err)
					reason = "error"
					goto finished
				}
				prompt = built.text
			} else if update = systemUpdate(built.sections, head.effective); update != "" {
				storedSections = built.sections
			}
			// REQ-LTC-001: the first user message of the turn carries the
			// <context> block, and a <system-update> when sections changed.
			if claimed {
				prefix := []core.ContentBlock{{Kind: core.BlockText, Text: built.context}}
				if update != "" {
					prefix = append(prefix, core.ContentBlock{Kind: core.BlockText, Text: update})
				}
				inputMsg.Content = append(prefix, inputMsg.Content...)
				contextBlocks = len(prefix)
			}
			promptBuilt = true
		}
		if claimed {
			payload := core.UserMessage{V: core.PayloadVersion, Content: inputMsg.Content, PromptSections: storedSections, ContextBlocks: contextBlocks, Runtime: runtimeNote}
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

		// The report step of REQ-LTC-040 offers no tools.
		var specs []core.ToolSpec
		if !budgetReport {
			specs, err = rn.reg.tools.Specs(turnTools)
			if err != nil {
				runErr = runtimeError(err)
				reason = "error"
				goto finished
			}
		}
		control, err := rn.reg.hooks.AgentRequest.Call(ctx, core.RequestControl{})
		if err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		// REQ-LTC-010: check for context pressure before every model call,
		// including steps that only carry tool results. A failure is logged
		// and the step continues.
		if c := rn.reg.compactor(); c != nil {
			if _, cerr := c.CompactIfNeeded(ctx, core.CompactRequest{
				SessionID: rn.sessionID, TurnID: turnID, AgentName: resolved.name,
				Provider: resolved.provider, Model: resolved.model, System: prompt,
				Tools: specs, MaxTokens: control.MaxTokens, Trigger: "pressure",
			}); cerr != nil {
				slog.Warn("compaction failed", "session_id", rn.sessionID, "turn_id", turnID, "error", cerr)
			}
		}
		history, err := rn.reg.store.DeriveMessages(writeCtx, rn.sessionID)
		if err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		req := core.ChatRequest{Model: resolved.model, System: prompt, Messages: history, Tools: specs}
		req.MaxTokens = control.MaxTokens
		req.Temperature = control.Temperature

		targets := append([]core.LLMTarget{{Provider: resolved.provider, Model: resolved.model}}, resolved.fallback...)
		var (
			text       string
			toolBlocks []core.ContentBlock
			usage      core.Usage
			stop       string
		)
		// REQ-LTC-032: a retryable failure after the first stream event, but
		// before the assistant message is stored, repeats the step. The partial
		// stream is dropped: consumers reset on the step_retry frame.
		for attempt := 1; ; attempt++ {
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
			var (
				seen      int
				streamErr error
			)
			text, toolBlocks, usage, stop, seen, streamErr = rn.consumeStream(ctx, stream, turnID)
			if streamErr == nil {
				break
			}
			if ctx.Err() != nil {
				reason = rn.writeStoppedAttempt(writeCtx, events, step, resolved, turnID, ctx)
				goto finished
			}
			pe := providerError(streamErr)
			rn.writeStepAttempt(writeCtx, events, step, resolved, turnID, attempt, *pe)
			// Only a mid-stream failure is repeated here; a failure before the
			// first event is the llm.stream retry hook's job (REQ-LTC-031).
			if seen == 0 || !retryable(pe.Class) || attempt > maxStepRetries {
				runErr = pe
				reason = "error"
				goto finished
			}
			// LiveFrame has no step field, so the step number rides in Text for
			// diagnostics; consumers only act on the kind.
			rn.reg.hub.Publish(rn.sessionID, core.LiveFrame{SessionID: rn.sessionID, TurnID: turnID, Kind: "step_retry", Text: strconv.Itoa(step)})
			if werr := stepRetryWait(ctx, stepRetryDelay(attempt)); werr != nil {
				reason = rn.writeStoppedAttempt(writeCtx, events, step, resolved, turnID, ctx)
				goto finished
			}
		}
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		totalUsage.CachedInputTokens += usage.CachedInputTokens
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
				usage.CachedInputTokens += output.Usage.CachedInputTokens
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
		// REQ-LTC-005: the step log line reports the cache hit share.
		slog.Info("step finished", "session_id", rn.sessionID, "turn_id", turnID, "step", step,
			"provider", resolved.provider, "model", resolved.model,
			"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens,
			"cached_input_tokens", usage.CachedInputTokens,
			"cache_hit_pct", cacheHitPct(usage.InputTokens, usage.CachedInputTokens))
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
	} else if budgetReport && reason == "end_turn" {
		// A turn that ran its configured step cap ends as max_steps, not
		// end_turn, even though the report step ended normally (REQ-LTC-040).
		reason = "max_steps"
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

// consumeStream drains one step's stream. seen is the number of events read
// before a failure, which tells a pre-first-event failure from a mid-stream one
// (REQ-LTC-032).
func (rn *Runner) consumeStream(ctx context.Context, stream core.ChatStream, turnID string) (string, []core.ContentBlock, core.Usage, string, int, error) {
	var text strings.Builder
	var blocks []core.ContentBlock
	var usage core.Usage
	stop := "end_turn"
	seen := 0
	for event, err := range stream {
		if err != nil {
			return text.String(), blocks, usage, stop, seen, err
		}
		seen++
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
		return text.String(), blocks, usage, stop, seen, err
	}
	return text.String(), blocks, usage, stop, seen, nil
}

// writeStepAttempt records one failed stream attempt of a step. attempt counts
// from 1 (REQ-LTC-032).
func (rn *Runner) writeStepAttempt(ctx context.Context, events *Events, step int, resolved resolvedAgent, turnID string, attempt int, pe core.ProviderError) {
	payload := core.AssistantAttempt{V: core.PayloadVersion, Step: step, Attempt: attempt, Provider: resolved.provider, Model: resolved.model, Error: core.PayloadError{Class: pe.Class, Status: pe.Status, Message: pe.Message}}
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

// toolCallTextNote is the runtime note of the malformed tool-call nudge
// (REQ-MEM-020). It is the user-role message of one extra step.
const toolCallTextNote = "Your last reply wrote a tool call as plain text, so nothing ran. Call the tool again through the tool-calling interface, or answer without tools."

// maxToolCallTextNudges caps the malformed tool-call nudges per turn
// (REQ-MEM-020).
const maxToolCallTextNudges = 2

// stepLimitNote is the user text of the final, tool-free report step a
// configured step cap runs before the turn ends (REQ-LTC-040).
const stepLimitNote = "You reached the step limit for this turn. Stop working and report: what is done, what remains, and the next step."

// maxStepRetries is the number of extra attempts after a mid-stream failure
// (REQ-LTC-032); the first failed stream plus two retries is three calls.
const maxStepRetries = 2

// stepRetryDelays is the backoff between mid-stream step retries. It mirrors
// the first delays of NewRetry (agent/retry.go) and is a variable so tests can
// replace it and run fast.
var stepRetryDelays = []time.Duration{2 * time.Second, 4 * time.Second}

// stepRetryWait performs one step-retry backoff wait with the retry jitter. It
// is a variable so tests can replace it and avoid wall-clock time.
var stepRetryWait = func(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(jitteredDelay(delay, randomJitter))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// stepRetryDelay returns the backoff before retry attempt (1-based).
func stepRetryDelay(attempt int) time.Duration {
	if len(stepRetryDelays) == 0 {
		return 0
	}
	index := min(len(stepRetryDelays), attempt) - 1
	return stepRetryDelays[index]
}

// cacheHitPct is the percentage of prompt tokens served from the provider cache
// (REQ-LTC-005). It is 0 when the request counted no input tokens.
func cacheHitPct(input, cached int64) int64 {
	if input <= 0 {
		return 0
	}
	return cached * 100 / input
}

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
