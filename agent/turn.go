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
	"github.com/ridhomujizat/lana-harness/internal/models"
)

// Runner holds the state of one session.
type Runner struct {
	reg       *Registry
	sessionID string

	mu      sync.Mutex
	inbox   []inboxItem
	running bool
	cancel  context.CancelCauseFunc
	turnID  string
	// memoryShown holds the memory ids already put in a turn <context> block
	// this session (REQ-ECX-024). It is in memory, so a restart forgets it.
	memoryShown map[int]bool
	// knowledgeShown holds the knowledge paths already put in a turn <context>
	// block this session (REQ-KNW-050). It is in memory, so a restart forgets
	// it.
	knowledgeShown map[string]bool
}

// inboxItem is one queued owner message: its text and the workspace-relative
// paths of its attachments (REQ-ATT-002).
type inboxItem struct {
	text        string
	attachments []string
}

func (rn *Runner) popInbox() (inboxItem, bool) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if len(rn.inbox) == 0 {
		return inboxItem{}, false
	}
	item := rn.inbox[0]
	rn.inbox = rn.inbox[1:]
	return item, true
}

// pushFrontInbox returns item to the front of the inbox, so a message the step
// cap took but did not use still starts the next turn.
func (rn *Runner) pushFrontInbox(item inboxItem) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.inbox = append([]inboxItem{item}, rn.inbox...)
}

func (rn *Runner) runChain(ctx context.Context, initial inboxItem, turnID, continueReason string) {
	for {
		// turnCancel is the cancel func of the turn about to run. Releasing it
		// once the turn's work is done keeps the per-turn context tree of a
		// long session bounded; the next chained turn gets its own context.
		rn.mu.Lock()
		turnCancel := rn.cancel
		rn.mu.Unlock()
		result := rn.runTurn(ctx, initial, turnID, continueReason)
		if turnCancel != nil {
			turnCancel(context.Canceled)
		}
		// finishedTurnID is the turn whose done channel finishTurn closes. Its
		// signal must wait until the session's next state is set under the
		// locks, so a Wait that returns never sees the session still running
		// (and a Start right after it does not get ErrSessionBusy).
		finishedTurnID := turnID
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
			rn.reg.finishTurn(finishedTurnID, result)
			return
		}
		rn.mu.Lock()
		if len(rn.inbox) == 0 {
			rn.running = false
			rn.cancel = nil
			rn.turnID = ""
			rn.mu.Unlock()
			rn.reg.mu.Unlock()
			rn.reg.finishTurn(finishedTurnID, result)
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
		// The next turn is already marked running, so closing the finished
		// turn's done channel now cannot make a waiter see an idle session that
		// really has a queued message.
		rn.reg.finishTurn(finishedTurnID, result)
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
	// vision reports whether the model can see images. The agent key wins over
	// the catalog (REQ-ATT-003).
	vision bool
	// reasoningEffort is the thinking effort of every step request; "" means
	// the provider default (REQ-MDL-001).
	reasoningEffort string
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
	// REQ-MDL-001: the session's reasoning_effort wins, else the agent's, else
	// the catalog default for the resolved model, else the provider default ("").
	effort := cfg.ReasoningEffort
	if hasSelection && selection.ReasoningEffort != "" {
		effort = selection.ReasoningEffort
	}
	if effort == "" {
		if known, ok := models.Lookup(model); ok {
			effort = known.DefaultEffort
		}
	}
	// REQ-ATT-003: the agent key wins over the catalog flag; an unknown model
	// is not a vision model unless the agent sets vision = true.
	vision := false
	if cfg.Vision != nil {
		vision = *cfg.Vision
	} else if known, ok := models.Lookup(model); ok {
		vision = known.Vision
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
		name:            cfgName,
		team:            teamName,
		delegates:       delegates,
		roster:          roster,
		provider:        provider,
		model:           model,
		promptFile:      cfg.PromptFile,
		channel:         info.Channel,
		workspace:       workspace,
		tools:           cfg.Tools,
		disabled:        cfg.DisabledTools,
		mcp:             mcpServers,
		skills:          cfg.Skills,
		fallback:        fallback,
		maxSteps:        maxSteps,
		child:           info.ParentSessionID != "",
		vision:          vision,
		reasoningEffort: effort,
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

// buildPrompt assembles the system prompt from the persona, the agent home
// files, the skills, the tool guidance, and the runtime context. ownerText is
// the first user message of the turn: when the memory is indexed it picks the
// relevant entries for the <context> block (REQ-ECX-024). toolNames are the
// tools offered in this turn; they gate per-turn knowledge recall
// (REQ-KNW-050).
func (rn *Runner) buildPrompt(res resolvedAgent, skills []agenthome.Skill, unavailable map[string]string, notes []agenthome.ToolNote, hasMCP bool, ownerText string, toolNames []string) (promptBuild, error) {
	in := agenthome.PromptInput{
		Agent: res.name, Channel: res.channel, Workspace: res.workspace, Now: time.Now(),
		Skills: skills, Roster: res.roster, UnavailableMCP: unavailable, ToolNotes: notes, HasMCP: hasMCP,
	}
	// REQ-JOB-017: the session's jobs go into the turn <context> block. Ended
	// jobs are marked announced so they appear in one turn only.
	if rn.reg.jobs != nil {
		in.Jobs = rn.reg.jobs.ContextLine(rn.sessionID)
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
		in.RelevantMemory = rn.relevantMemory(ownerText, in.Memory)
	}
	in.RelevantKnowledge = rn.relevantKnowledge(ownerText, toolNames)
	slog.Debug("system prompt", "agent", res.name, "user_chars", agenthome.Size(in.User), "memory_chars", agenthome.Size(in.Memory))
	return promptBuild{text: agenthome.Build(in), sections: promptSections(in), context: agenthome.ContextBlock(in)}, nil
}

// memoryRelevantMax is the most entries put in one turn <context> block
// (REQ-ECX-024).
const memoryRelevantMax = 3

// relevantMemory ranks the indexed memory against the owner's message and
// returns up to memoryRelevantMax entries not shown earlier in this session,
// as formatted "m<n>: <entry>" lines. It records the ids it returns; ids are
// 1-based file positions (REQ-ECX-024).
func (rn *Runner) relevantMemory(ownerText string, entries []string) []string {
	if strings.TrimSpace(ownerText) == "" || len(entries) == 0 {
		return nil
	}
	if _, indexed := agenthome.MemoryView(entries); !indexed {
		return nil
	}
	ranked := agenthome.Rank(ownerText, entries)
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if rn.memoryShown == nil {
		rn.memoryShown = make(map[int]bool, len(ranked))
	}
	out := make([]string, 0, memoryRelevantMax)
	for _, item := range ranked {
		if len(out) == memoryRelevantMax {
			break
		}
		if rn.memoryShown[item.ID] {
			continue
		}
		rn.memoryShown[item.ID] = true
		out = append(out, fmt.Sprintf("m%d: %s", item.ID, entries[item.ID-1]))
	}
	return out
}

// The knowledge recall bounds and line cut (REQ-KNW-050).
const (
	knowledgeRecallMax     = 3
	knowledgeRecallLineMax = 200
	knowledgeRecallTimeout = 2 * time.Second
)

// relevantKnowledge returns up to knowledgeRecallMax knowledge pages relevant
// to ownerText as "<title> (<path>): <description or snippet>" lines. Recall
// runs only for an owner message, when knowledge_search is offered in this
// turn, when auto_recall is not false, and when the knowledge service is
// installed. A page already shown in this session is skipped, so each appears
// once per session (REQ-KNW-050).
func (rn *Runner) relevantKnowledge(ownerText string, toolNames []string) []string {
	if strings.TrimSpace(ownerText) == "" || !containsString(toolNames, "knowledge_search") {
		return nil
	}
	if auto := rn.reg.config.Knowledge.AutoRecall; auto != nil && !*auto {
		return nil
	}
	service := rn.reg.knowledgeService()
	if service == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), knowledgeRecallTimeout)
	defer cancel()
	skip := func(path string) bool {
		rn.mu.Lock()
		defer rn.mu.Unlock()
		return rn.knowledgeShown[path]
	}
	hits, err := service.Recall(ctx, ownerText, knowledgeRecallMax, skip)
	if err != nil {
		slog.Debug("knowledge recall", "session_id", rn.sessionID, "error", err)
		return nil
	}
	if len(hits) == 0 {
		return nil
	}
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if rn.knowledgeShown == nil {
		rn.knowledgeShown = make(map[string]bool, len(hits))
	}
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		if rn.knowledgeShown[hit.Path] {
			continue
		}
		rn.knowledgeShown[hit.Path] = true
		out = append(out, knowledgeRecallLine(hit))
	}
	return out
}

// knowledgeRecallLine renders one recall line: the title, the vault path, and
// the description or, when it is empty, the snippet cut to 200 runes
// (REQ-KNW-050).
func knowledgeRecallLine(hit core.KnowledgeHit) string {
	text := strings.TrimSpace(hit.Description)
	if text == "" {
		text = strings.TrimSpace(hit.Snippet)
	}
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > knowledgeRecallLineMax {
		text = string(runes[:knowledgeRecallLineMax]) + "…"
	}
	if text == "" {
		return fmt.Sprintf("%s (%s)", hit.Title, hit.Path)
	}
	return fmt.Sprintf("%s (%s): %s", hit.Title, hit.Path, text)
}

// toolNotes collects the prompt guidance of the offered tools in name order.
// A tool that does not implement core.ToolGuidance, or returns an empty text,
// adds no note (REQ-ECX-010).
func (rn *Runner) toolNotes(names []string) []agenthome.ToolNote {
	notes := make([]agenthome.ToolNote, 0, len(names))
	for _, name := range names {
		tool, ok := rn.reg.tools.Get(name)
		if !ok {
			continue
		}
		guidance, ok := tool.(core.ToolGuidance)
		if !ok {
			continue
		}
		if text := strings.TrimSpace(guidance.PromptGuidance()); text != "" {
			notes = append(notes, agenthome.ToolNote{Name: name, Text: text})
		}
	}
	return notes
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

func (rn *Runner) runTurn(ctx context.Context, initial inboxItem, turnID, continueReason string) (result TurnResult) {
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
	// budgetReport marks the final report step a configured step cap adds
	// before the turn ends (REQ-LTC-040). It sends the same tools as the other
	// steps, and a tool call it returns is not run (REQ-LPG-015).
	budgetReport := false
	// halted marks a loop guard halt (REQ-LPG-009) whose final report step is
	// still to run; pendingHaltNote is that step's runtime note and loopReport
	// marks that the report step ran, so a normal end is reason loop rather
	// than max_steps or end_turn. The report step keeps the offered tools and
	// does not run a tool call it returns (REQ-LPG-015).
	halted := false
	pendingHaltNote := ""
	loopReport := false
	prompt := ""
	promptBuilt := false
	turnTools := []string{}
	var totalUsage core.Usage
	// ownerMessage marks that this turn stored at least one message from the
	// owner, not a runtime note. A capture pass needs one (REQ-MEM-010).
	ownerMessage := false
	// lastResolved and lastSpecs carry the last step's resolution and tool
	// declarations to the finished label for the capture pass (REQ-MEM-011).
	var lastResolved resolvedAgent
	var lastSpecs []core.ToolSpec
	// pendingReminder is the loop guard's reminder for the next request; it is
	// set after a step's tool results and claimed as the next step input
	// (REQ-TRL-010..014).
	pendingReminder := ""
	// guard tracks this turn's exact-repeat chain and failure streak.
	var guard loopGuard
	// pendingClaim holds the inbox message popInbox took for this step until
	// its user/message event is stored. Every error exit before that event
	// returns it to the inbox at the finished label, so a claimed owner message
	// is never lost; the step cap and halt paths return it explicitly.
	var pendingClaim inboxItem
	claimedPending := false
	for {
		claimedText := ""
		claimed := false
		// claimedItem carries the claimed message and its attachments. Runtime
		// notes leave it zero, so they never add attachment blocks
		// (REQ-ATT-002).
		var claimedItem inboxItem
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
			claimedItem = initial
			claimedText = initial.text
			claimed = true
			hasInitial = false
			continueMsg = continueReason != ""
		case pendingTools:
			var item inboxItem
			item, claimed = rn.popInbox()
			if claimed {
				// An owner message replaces the guard's reminder and resets both
				// chains: repetition across an interjection is not a loop
				// (REQ-TRL-013, REQ-TRL-014).
				claimedItem = item
				claimedText = item.text
				inboxClaim = true
				pendingClaim = item
				claimedPending = true
				guard.reset()
				pendingReminder = ""
			} else if pendingReminder != "" {
				claimedText = pendingReminder
				claimed = true
				runtimeNote = true
				pendingReminder = ""
			}
		case continueNext:
			continueNext = false
			var item inboxItem
			item, claimed = rn.popInbox()
			if claimed {
				claimedItem = item
				claimedText = item.text
				inboxClaim = true
				pendingClaim = item
				claimedPending = true
				guard.reset()
			}
		default:
			var item inboxItem
			var ok bool
			item, ok = rn.popInbox()
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
				claimedItem = item
				claimedText = item.text
				inboxClaim = true
				pendingClaim = item
				claimedPending = true
				// A mid-turn owner message resets both guard chains
				// (REQ-TRL-014).
				guard.reset()
			}
			claimed = true
		}

		inputMsg := core.Message{Role: core.RoleUser}
		if claimed {
			text := claimedText
			// REQ-ATT-002: an owner message with attachments and no text is
			// stored with the text "(attachments)".
			if !runtimeNote && text == "" && len(claimedItem.attachments) > 0 {
				text = "(attachments)"
			}
			inputMsg.Content = []core.ContentBlock{{Kind: core.BlockText, Text: text}}
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
				// A rejected message is dropped on purpose, not re-queued.
				claimedPending = false
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
		lastResolved = resolved
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
		// REQ-LTC-040: a positive cap runs one final step that asks for a
		// status report, then the turn ends with reason max_steps. The report
		// step sends the same tools as the earlier steps and does not run a
		// tool call it returns (REQ-LPG-015).
		if resolved.maxSteps > 0 && steps >= resolved.maxSteps {
			// A message popInbox already claimed stays for the next turn; the
			// cap replaces the model input, not the owner's queued message.
			if inboxClaim {
				rn.pushFrontInbox(claimedItem)
				claimedPending = false
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
		// REQ-LPG-009: a loop guard halt runs one final step, like the step
		// cap. The halt note is delivered as a runtime note and a message
		// popInbox already claimed stays for the next turn. When the cap is
		// reached as well, the cap wins (first condition reached). The report
		// step keeps the offered tools; a tool call it returns is not run
		// (REQ-LPG-015).
		if halted && !budgetReport && !loopReport {
			if inboxClaim {
				rn.pushFrontInbox(claimedItem)
				claimedPending = false
			}
			budgetReport = true
			loopReport = true
			claimed = true
			runtimeNote = true
			claimedText = pendingHaltNote
			pendingReminder = ""
			inputMsg = core.Message{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: pendingHaltNote}}}
		}
		// REQ-LPG-009: after the halt report step the turn ends, and an owner
		// message that arrived meanwhile stays for the next turn.
		if loopReport && !pendingTools {
			if inboxClaim {
				rn.pushFrontInbox(claimedItem)
				claimedPending = false
			}
			goto finished
		}
		// REQ-ATT-002: the owner message carries one block per attachment, in
		// order, after the text block and before the <context> prefix the
		// !promptBuilt branch adds. The step-cap and halt notes replaced
		// inputMsg above, so they carry no attachments; the item was pushed
		// back instead.
		if claimed && !runtimeNote && len(claimedItem.attachments) > 0 {
			inputMsg.Content = append(inputMsg.Content, ownerAttachmentBlocks(resolved.workspace, claimedItem.attachments)...)
		}
		var storedSections []core.PromptSection
		// contextBlocks is the number of leading content blocks lana injects
		// into the turn's first user message; it is recorded on the stored
		// payload so display surfaces can hide them (REQ-LTC-001).
		contextBlocks := 0
		if !promptBuilt {
			// REQ-TB-004, REQ-TB-008: a tool an agent lists but nothing has
			// registered (a command of a package that is not installed) is not
			// offered. A later registration (the toolbox sync) makes it
			// available on the next turn without a restart.
			turnTools = append(turnTools, rn.reg.registeredTools(resolved.tools)...)
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
			// REQ-JOB-015: an agent offered bash also gets the job tools without
			// listing them. It runs before disabled_tools so a disabled job tool
			// is removed like any other.
			turnTools = addCompanionTools(turnTools, resolved.disabled, func(name string) bool {
				_, ok := rn.reg.tools.Get(name)
				return ok
			})
			// disabled_tools removes a name from the offered list. It is applied
			// after the built-in, MCP, skill, and team tools are collected so a
			// disabled team or skill tool is removed as well (AC-WEB-049, Q-024).
			turnTools = filterDisabledTools(turnTools, resolved.disabled)
			// REQ-LTC-003: a deterministic tool order keeps the prefix stable.
			sort.Strings(turnTools)
			// REQ-ECX-024: only an owner message can pick relevant memory, and
			// only the first user message of the turn carries it.
			ownerText := ""
			if claimed && !runtimeNote {
				ownerText = claimedText
			}
			built, err := rn.buildPrompt(resolved, skills, unavailable, rn.toolNotes(turnTools), len(resolved.mcp) > 0, ownerText, turnTools)
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
			// The claimed message is stored now, so it is no longer re-queued.
			claimedPending = false
			if !runtimeNote {
				ownerMessage = true
			}
		}
		step := steps + 1
		if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindStepStart, TurnID: turnID, Payload: core.StepStart{V: core.PayloadVersion, Step: step, Provider: resolved.provider, Model: resolved.model}}); err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		steps = step

		// REQ-LPG-015: the report step sends the same tools as the earlier steps
		// so the provider prefix cache stays valid; only the runtime note asks
		// the model for text.
		specs, err := rn.reg.tools.Specs(turnTools)
		if err != nil {
			runErr = runtimeError(err)
			reason = "error"
			goto finished
		}
		lastSpecs = specs
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
				Tools: specs, MaxTokens: control.MaxTokens,
				ReasoningEffort: resolved.reasoningEffort, Trigger: "pressure",
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
		// REQ-ATT-003: a model that cannot see images gets the placeholder text
		// instead of image bytes. The stored events are unchanged.
		if !resolved.vision {
			history = core.TextForImages(history)
		}
		req := core.ChatRequest{Model: resolved.model, System: prompt, Messages: history, Tools: specs}
		req.MaxTokens = control.MaxTokens
		req.Temperature = control.Temperature
		req.ReasoningEffort = resolved.reasoningEffort

		targets := append([]core.LLMTarget{{Provider: resolved.provider, Model: resolved.model}}, resolved.fallback...)
		var (
			text       string
			thinking   string
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
			text, thinking, toolBlocks, usage, stop, seen, streamErr = rn.consumeStream(ctx, stream, turnID)
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
		// REQ-MDL-003: the thinking block is stored first, then the answer text,
		// then the tool calls.
		content := make([]core.ContentBlock, 0, 2+len(toolBlocks))
		if thinking != "" {
			content = append(content, core.ContentBlock{Kind: core.BlockThinking, Text: thinking})
		}
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
			outputs := make([]core.ToolOutput, len(calls))
			if budgetReport {
				// REQ-LPG-015: the report step keeps the offered tools for the
				// provider prefix cache, but a tool call it returns is not run.
				// Each gets an error result and the turn ends after this step.
				for i := range outputs {
					outputs[i] = core.ToolOutput{Status: core.ToolStatusError, Output: reportToolsDisabledText}
				}
			} else {
				var mcpAdmin core.MCPAdmin
				if !resolved.child {
					mcpAdmin = rn.reg.mcpAdminFor()
				}
				// REQ-SSR-010: session_search reads the conversation index when
				// the store provides one.
				var sessions core.SessionSearcher
				if searcher, ok := rn.reg.store.(core.SessionSearcher); ok {
					sessions = searcher
				}
				toolCtx := core.ToolCtx{
					SessionID: rn.sessionID, TurnID: turnID, AgentName: resolved.name, Workspace: resolved.workspace, Home: rn.reg.home,
					Skills:        resolved.skills,
					Questions:     rn.reg.questions,
					Subagents:     &subagentRunner{reg: rn.reg, allowed: resolved.delegates},
					MCPAdmin:      mcpAdmin,
					MCPStatus:     rn.reg.ServerStatus,
					AllowedAgents: resolved.delegates,
					Depth:         sessionDepth(ctx, rn.reg.store, rn.sessionID),
					Jobs:          rn.reg.jobs,
					Sessions:      sessions,
					Knowledge:     rn.reg.knowledgeService(),
				}
				// REQ-LPG-008: a call that matches a block rule is not run; its
				// synthetic error result takes its place. The other calls run as
				// usual and every result is stored in call order.
				var runnable []core.ToolCall
				var runnableIndex []int
				// stepKeys counts, per call identity, the identical calls seen
				// earlier in this step. guard.observe only advances the repeat
				// chain after the whole step, so without this count one response
				// with many identical calls would run all of them past the block
				// threshold (REQ-LPG-008).
				stepKeys := map[string]int{}
				for i, call := range calls {
					out, blocked := guard.before(call)
					if !blocked && !loopGuardUntracked[call.Name] {
						key := call.Name + "\x00" + canonicalArgs(call.Input)
						prior := 0
						if key == guard.lastKey {
							prior = guard.repeat
						}
						if prior+stepKeys[key] >= loopGuardStreakLen {
							out = guard.block(call, loopGuardKindIdenticalStreak, prior+stepKeys[key], loopGuardBlockedIdenticalText)
							blocked = true
						}
						stepKeys[key]++
					}
					if blocked {
						outputs[i] = out
						slog.Info("loop guard block", "session_id", rn.sessionID, "turn_id", turnID,
							"kind", guard.lastBlock.kind, "tool", guard.lastBlock.tool, "count", guard.lastBlock.count)
						continue
					}
					runnable = append(runnable, call)
					runnableIndex = append(runnableIndex, i)
				}
				if len(runnable) > 0 {
					ran := rn.reg.pipeline.Run(ctx, runnable, turnTools, toolCtx)
					for j, out := range ran {
						outputs[runnableIndex[j]] = out
					}
				}
			}
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
				if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindToolResult, TurnID: turnID, Payload: core.ToolResultPayload{V: core.PayloadVersion, CallID: calls[i].CallID, Status: status, Output: output.Output, Images: output.Images}}); err != nil {
					runErr = runtimeError(err)
					reason = "error"
					goto finished
				}
				rn.reg.hub.Publish(rn.sessionID, core.LiveFrame{SessionID: rn.sessionID, TurnID: turnID, Kind: "tool_result", CallID: calls[i].CallID, Name: calls[i].Name, Status: string(status), Output: output.Output})
			}
			if !budgetReport {
				// REQ-TRL-010..014, REQ-LPG-002..007: advance the loop guard
				// over this step's calls and results. The reminder rides the
				// next request, which is claimed in the pendingTools branch or
				// dropped for an owner message. The report step is skipped: its
				// calls were not run (REQ-LPG-015).
				pendingReminder = guard.observe(calls, outputs)
				for _, hit := range guard.hits {
					slog.Info("loop guard reminder", "session_id", rn.sessionID, "turn_id", turnID, "kind", hit.kind, "count", hit.count)
				}
				guard.strike(allUnknownNames(outputs))
				if guard.halt != nil {
					// REQ-LPG-009: a block or a detected loop halts the turn. The
					// halt note replaces the reminder and the next step is the
					// report step.
					slog.Info("loop guard halt", "session_id", rn.sessionID, "turn_id", turnID,
						"kind", guard.halt.kind, "tool", guard.halt.tool, "count", guard.halt.count)
					pendingReminder = loopGuardHaltNote(*guard.halt)
					pendingHaltNote = pendingReminder
					halted = true
				}
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
		if budgetReport && len(toolBlocks) > 0 {
			// REQ-LPG-015: a tool call in the final report step is not run; the
			// step's text is kept and the turn ends with its usual reason (loop
			// or max_steps).
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
	// A message popInbox claimed but never stored is owed to the inbox: an
	// early error exit (pre-step, resolve, prompt, or event write) must not
	// swallow an owner message.
	if claimedPending {
		rn.pushFrontInbox(pendingClaim)
	}
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
		// A loop-guard halt ends as loop instead (REQ-LPG-009).
		if loopReport {
			reason = "loop"
		} else {
			reason = "max_steps"
		}
	}
	end := core.TurnEnd{V: core.PayloadVersion, TurnID: turnID, Reason: reason, Error: payloadErr}
	if err := writeTurnEnd(writeCtx, events, core.EventDraft{Kind: core.KindTurnEnd, TurnID: turnID, Payload: end}); err != nil {
		slog.Error("failed to write turn/end", "session_id", rn.sessionID, "turn_id", turnID, "error", err)
	}
	// REQ-MEM-010: after a root turn that stored an owner message and had
	// memory_write, run one background capture pass. Capture returns at once,
	// so the turn result is never delayed.
	if cfg, ok := rn.reg.AgentConfig(lastResolved.name); ok && captureEligible(reason, lastResolved.child, ownerMessage, lastSpecs, cfg) {
		if c := rn.reg.memoryCapturer(); c != nil {
			c.Capture(core.CaptureRequest{
				SessionID: rn.sessionID, TurnID: turnID, AgentName: lastResolved.name,
				Home: rn.reg.home, Provider: lastResolved.provider, Model: lastResolved.model,
				System: prompt, Tools: lastSpecs, ReasoningEffort: lastResolved.reasoningEffort,
			})
		}
	}
	result.StopReason = reason
	result.Text = lastText
	result.Usage = totalUsage
	result.Err = runErr
	return result
}

// consumeStream drains one step's stream. thinking is the reasoning text:
// the explicit thinking block when the provider sends one, else the joined
// thinking deltas. seen is the number of events read before a failure, which
// tells a pre-first-event failure from a mid-stream one (REQ-LTC-032).
func (rn *Runner) consumeStream(ctx context.Context, stream core.ChatStream, turnID string) (string, string, []core.ContentBlock, core.Usage, string, int, error) {
	var text strings.Builder
	var reasoning strings.Builder
	thinkingBlock := ""
	var blocks []core.ContentBlock
	var usage core.Usage
	stop := "end_turn"
	seen := 0
	thinking := func() string {
		if thinkingBlock != "" {
			return thinkingBlock
		}
		return reasoning.String()
	}
	for event, err := range stream {
		if err != nil {
			return text.String(), thinking(), blocks, usage, stop, seen, err
		}
		seen++
		switch event.Kind {
		case core.ChatTextDelta:
			text.WriteString(event.Text)
			rn.reg.hub.Publish(rn.sessionID, core.LiveFrame{SessionID: rn.sessionID, TurnID: turnID, Kind: "text_delta", Text: event.Text})
		case core.ChatThinkingDelta:
			reasoning.WriteString(event.Text)
			rn.reg.hub.Publish(rn.sessionID, core.LiveFrame{SessionID: rn.sessionID, TurnID: turnID, Kind: "thinking_delta", Text: event.Text})
		case core.ChatBlock:
			switch event.Block.Kind {
			case core.BlockText:
				text.WriteString(event.Block.Text)
			case core.BlockThinking:
				thinkingBlock = event.Block.Text
			default:
				blocks = append(blocks, event.Block)
			}
		case core.ChatUsage:
			usage = event.Usage
		case core.ChatStop:
			stop = event.StopReason
		}
	}
	if err := ctx.Err(); err != nil {
		return text.String(), thinking(), blocks, usage, stop, seen, err
	}
	return text.String(), thinking(), blocks, usage, stop, seen, nil
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

// captureEligible reports whether a finished turn qualifies for a background
// memory-capture pass (REQ-MEM-010).
func captureEligible(reason string, child, ownerMessage bool, specs []core.ToolSpec, cfg core.AgentConfig) bool {
	if reason != "end_turn" && reason != "max_steps" {
		return false
	}
	if child || !ownerMessage || !hasToolSpec(specs, "memory_write") {
		return false
	}
	return cfg.MemoryCapture == nil || *cfg.MemoryCapture
}

// hasToolSpec reports whether specs holds a tool with the given name.
func hasToolSpec(specs []core.ToolSpec, name string) bool {
	for _, spec := range specs {
		if spec.Name == name {
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

// stepLimitNote is the user text of the final report step a configured step cap
// runs before the turn ends (REQ-LTC-040). The step keeps the offered tools and
// does not run a tool call it returns (REQ-LPG-015).
const stepLimitNote = "You reached the step limit for this turn. Stop working and report: what is done, what remains, and the next step."

// reportToolsDisabledText is the error result of a call made in the final
// report step, which keeps the offered tools but does not run them
// (REQ-LPG-015).
const reportToolsDisabledText = "Tools are disabled for this final report; answer in text."

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

// companionTools are the background-job tools offered with bash (REQ-JOB-015).
var companionTools = []string{"job_kill", "job_list", "job_output"}

// addCompanionTools adds the background-job tools when bash is offered and
// drops them when it is not. A disabled bash or job tool is never added, and a
// tool the registry does not have is skipped (REQ-JOB-015).
func addCompanionTools(tools, disabled []string, registered func(string) bool) []string {
	hasBash := containsString(tools, "bash") && !containsString(disabled, "bash")
	out := make([]string, 0, len(tools)+len(companionTools))
	for _, name := range tools {
		if isCompanionTool(name) && (!hasBash || !registered(name)) {
			continue
		}
		out = append(out, name)
	}
	if !hasBash {
		return out
	}
	for _, name := range companionTools {
		if containsString(out, name) || containsString(disabled, name) || !registered(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// isCompanionTool reports whether name is one of the background-job tools.
func isCompanionTool(name string) bool {
	return containsString(companionTools, name)
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

// registeredTools keeps the names the tool registry knows. A name an agent
// lists but nothing registered yet (for example the command of a package that
// is not installed) is dropped: the turn does not offer it, and a later
// registration makes it available on the next turn without a restart
// (REQ-TB-004, REQ-TB-008).
func (r *Registry) registeredTools(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := r.tools.Get(name); !ok {
			continue
		}
		out = append(out, name)
	}
	return out
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
