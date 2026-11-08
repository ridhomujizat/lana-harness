package lana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/agent"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/router"
	// ADR 0018: the Claude Code runtime registers "claude-code".
	"github.com/ridhomujizat/lana-harness/runtime/claudecode"
)

// ClaudeCodeRuntime is the runtime key of a Claude Agent (API-CCR-001).
const ClaudeCodeRuntime = claudecode.Name

// RuntimeAgents returns the users of Claude Code for the Router's Claude Code
// card (API-CCT-005): the agents whose chain (model, combo, or fallback)
// holds a target of any Claude Code account, by name, then the combos that
// list one, as "combo:<name>". runtime other than "claude-code" lists
// nothing.
func (rt *Runtime) RuntimeAgents(runtime string) []string {
	names := []string{}
	if runtime != ClaudeCodeRuntime {
		return names
	}
	agents, combos := rt.claudeCodeUsers(func(string) bool { return true })
	for _, combo := range combos {
		agents = append(agents, "combo:"+combo)
	}
	return append(names, agents...)
}

// claudeCodeUsers returns the agents whose chain holds a target of a Claude
// Code account that match accepts, and the combos that list one, each sorted.
func (rt *Runtime) claudeCodeUsers(match func(account string) bool) (agentNames, comboNames []string) {
	rt.cfgMu.RLock()
	agents, combos, providers := rt.result.Config.Agents, rt.result.Config.Combos, rt.result.Config.Providers
	rt.cfgMu.RUnlock()
	uses := func(address string) bool {
		target, ok := llmcall.ParseTarget(address)
		return ok && llmcall.IsClaudeCodeAddressIn(providers, address) && match(target.Provider)
	}
	comboUses := func(name string) bool {
		for _, model := range combos[name].Models {
			if uses(model) {
				return true
			}
		}
		return false
	}
	agentNames, comboNames = []string{}, []string{}
	for name, cfg := range agents {
		cfg = llmcall.NormalizeAgent(cfg)
		used := cfg.Provider == "" && (uses(cfg.Model) || comboUses(cfg.Model))
		for _, fallback := range cfg.Fallback {
			used = used || uses(fallback)
		}
		if used {
			agentNames = append(agentNames, name)
		}
	}
	sort.Strings(agentNames)
	for name := range combos {
		if comboUses(name) {
			comboNames = append(comboNames, name)
		}
	}
	sort.Strings(comboNames)
	return agentNames, comboNames
}

// ClaudeCodeAccount is one Claude Code account of the Router's Claude Code
// page (API-CCT-011).
type ClaudeCodeAccount struct {
	Name    string
	Builtin bool
	// ConfigDir is the account's CLAUDE_CONFIG_DIR; "" for the built-in one.
	ConfigDir      string
	AllowScheduled bool
	Disabled       bool
	Status         core.RuntimeAccountStatus
	// LoginCommand signs the account in from a terminal (REQ-CCT-012):
	// CLAUDE_CONFIG_DIR='<dir>' CLAUDE_SECURESTORAGE_CONFIG_DIR='<dir>'
	// claude auth login (claude auth login for the built-in account).
	LoginCommand string
	// Windows are the plan windows of the account's own limit snapshot.
	Windows []router.Window
	Agents  []string
	Combos  []string
}

// ClaudeCodeAccounts reports every Claude Code account (API-CCT-011): the
// built-in "claude-code" first, then the providers of kind "claude-code" in
// name order. Each status runs `claude auth status` with the account's
// CLAUDE_CONFIG_DIR (cached 60 s by the runtime, never stored).
func (rt *Runtime) ClaudeCodeAccounts(ctx context.Context) ([]ClaudeCodeAccount, error) {
	if rt.registry == nil || rt.router == nil {
		return nil, fmt.Errorf("agent runtime %q is not available", ClaudeCodeRuntime)
	}
	ext, err := rt.registry.Runtime(ClaudeCodeRuntime)
	if err != nil {
		return nil, err
	}
	statuser, _ := ext.(core.RuntimeAccountStatuser)
	windows := map[string][]router.Window{}
	for _, snap := range rt.router.Limits(ctx, false) {
		windows[snap.Provider] = snap.Windows
	}
	names := append([]string{ClaudeCodeRuntime}, rt.router.ClaudeCodeAccounts()...)
	out := make([]ClaudeCodeAccount, 0, len(names))
	for _, name := range names {
		cfg, _ := rt.router.ClaudeCodeAccount(name)
		account := rt.claudeAccountSpec(name, cfg)
		row := ClaudeCodeAccount{
			Name: name, Builtin: name == ClaudeCodeRuntime, ConfigDir: account.ConfigDir,
			AllowScheduled: cfg.AllowScheduled, Disabled: cfg.Disabled,
			LoginCommand: llmcall.ClaudeLoginCommand(account.ConfigDir),
			Windows:      windows[name],
		}
		row.Agents, row.Combos = rt.claudeCodeUsers(func(account string) bool { return account == name })
		if statuser != nil {
			row.Status = statuser.AccountStatus(ctx, account)
		}
		out = append(out, row)
	}
	return out, nil
}

// claudeAccountSpec returns the runtime keys of a Claude Code account: the
// built-in one uses the first agent's binary and the daemon's
// CLAUDE_CONFIG_DIR; another account its own keys and folder.
func (rt *Runtime) claudeAccountSpec(name string, cfg core.ProviderConfig) core.RuntimeAccount {
	if name == ClaudeCodeRuntime {
		return core.RuntimeAccount{Name: name, Binary: rt.claudeCodeBinary()}
	}
	return core.RuntimeAccount{
		Name: name, ConfigDir: llmcall.ClaudeConfigDir(rt.home, name, cfg),
		Binary: cfg.Binary, Auth: cfg.Auth, APIKeyEnv: cfg.APIKeyEnv,
	}
}

// AgentRuntimeStatus reports the health of the external runtime registered
// under name (API-CCR-003). The runtime probes its binary (for Claude Code,
// `claude --version` and `claude auth status`, cached) and never reads a
// credential. ConfiguredAgents is left for the caller.
func (rt *Runtime) AgentRuntimeStatus(ctx context.Context, name string) (core.RuntimeStatus, error) {
	if rt.registry == nil {
		return core.RuntimeStatus{}, fmt.Errorf("agent runtime %q is not available", name)
	}
	ext, err := rt.registry.Runtime(name)
	if err != nil {
		return core.RuntimeStatus{}, err
	}
	return ext.Status(ctx), nil
}

// SetAgentRuntime installs ext as the external runtime used for agents whose
// runtime key is name, in place of the registered one. Tests and embedders
// use it; the runtime is shut down with the Runtime.
func (rt *Runtime) SetAgentRuntime(name string, ext core.AgentRuntime) {
	if rt.registry != nil {
		rt.registry.SetRuntime(name, ext)
	}
}

// externalHooks routes the usage, plan windows, and model list of Claude
// Code turns into the router: one call record per turn, the "claude-code"
// limit snapshot (API-CCT-008), and the Claude Code model list.
func (rt *Runtime) externalHooks() agent.ExternalHooks {
	return agent.ExternalHooks{Usage: rt.recordExternalUsage, Limits: rt.updateExternalLimits, Models: rt.updateClaudeCodeModels}
}

// updateClaudeCodeModels gives the router the models Claude Code listed in
// its initialize response and keeps them in router_kv, so the pickers list
// them after a restart before Claude Code runs again.
func (rt *Runtime) updateClaudeCodeModels(runtime string, models []core.RuntimeModel) {
	if runtime != ClaudeCodeRuntime || rt.router == nil || models == nil {
		return
	}
	rt.router.SetClaudeCodeModels(models)
	if rt.routerKV == nil {
		return
	}
	data, err := json.Marshal(models)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), routerStoreTimeout)
	defer cancel()
	if err := rt.routerKV.PutRouterKV(ctx, kvClaudeCodeModels, string(data)); err != nil {
		slog.Warn("router: save claude code models", "error", err)
	}
}

// kvClaudeCodeModels is the router_kv key of the last Claude Code model list.
const kvClaudeCodeModels = "claude_code_models"

// loadClaudeCodeModels gives the router the stored Claude Code model list.
func (rt *Runtime) loadClaudeCodeModels() {
	if rt.router == nil || rt.routerKV == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), routerStoreTimeout)
	defer cancel()
	value, ok, err := rt.routerKV.GetRouterKV(ctx, kvClaudeCodeModels)
	if err != nil || !ok || value == "" {
		return
	}
	var models []core.RuntimeModel
	if json.Unmarshal([]byte(value), &models) == nil && models != nil {
		rt.router.SetClaudeCodeModels(models)
	}
}

// recordExternalUsage writes the call record of one external runtime turn
// with source "turn" (REQ-CCR-008).
func (rt *Runtime) recordExternalUsage(u agent.ExternalUsage) {
	if rt.router == nil {
		return
	}
	cost := u.CostUSD
	// API-CCT-008: requested is the chain's requested value.
	requested := u.Requested
	if requested == "" {
		requested = u.Provider + "/" + u.Model
	}
	rec := router.CallRecord{
		At:           u.Started.UTC(),
		Source:       llmcall.SourceTurn,
		Agent:        u.Agent,
		SessionID:    u.SessionID,
		TurnID:       u.TurnID,
		Requested:    requested,
		Provider:     u.Provider,
		Model:        u.Model,
		Attempt:      1,
		Outcome:      u.Outcome,
		InputTokens:  int64(u.Usage.InputTokens),
		OutputTokens: int64(u.Usage.OutputTokens),
		CachedTokens: int64(u.Usage.CachedInputTokens),
		CostUSD:      &cost,
		DurationMs:   u.DurationMS,
		Error:        truncateRunes(u.Error, 500),
	}
	rt.router.RecordExternal(rec)
}

// updateExternalLimits stores the plan windows an external runtime reported
// as the provider's limit snapshot (REQ-CCR-008, API-RTR-013).
func (rt *Runtime) updateExternalLimits(provider, runtime string, windows []core.RuntimeLimitWindow) {
	if rt.router == nil || len(windows) == 0 {
		return
	}
	rt.router.UpdateLimits(router.LimitUpdate{Provider: provider, Kind: runtime, Windows: externalWindows(windows)})
}

// externalWindows converts runtime plan windows to router windows. A window
// without a reported utilization keeps a nil Utilization (JSON null, "usage
// not reported"), never 0.
func externalWindows(windows []core.RuntimeLimitWindow) []router.Window {
	out := make([]router.Window, 0, len(windows))
	for _, w := range windows {
		win := router.Window{Type: w.Type, Status: w.Status}
		if w.Utilization != nil {
			// Copy: the window must not alias the runtime's event. Nil stays
			// nil (not reported).
			util := *w.Utilization
			win.Utilization = &util
		}
		if !w.ResetsAt.IsZero() {
			resets := w.ResetsAt.UTC()
			win.ResetsAt = &resets
		}
		out = append(out, win)
	}
	return out
}

// scheduledRunAllowed checks a calendar or workflow run before it starts
// (REQ-CCT-008, AC-CCR-008 amended): when the chain the run would use (the
// agent's, or model when set) has only Claude Code targets and the agent does
// not set allow_scheduled, the run fails. A chain with another target runs;
// the turn skips its Claude Code targets (agent.Registry.MarkScheduled). A
// team is checked by its lead.
func (rt *Runtime) scheduledRunAllowed(agentName, model string) error {
	cfgName := agentName
	if team, ok := rt.teams()[agentName]; ok {
		cfgName = team.Lead
	}
	cfg, ok := rt.agentConfig(agentName)
	if !ok || cfg.AllowScheduled {
		return nil
	}
	llm := rt.coreCtx.LLM
	cfg = llmcall.NormalizeAgent(cfg)
	if model != "" {
		cfg.Provider, cfg.Model = "", model
		if target, ok := llmcall.ParseTarget(model); ok && model[len(target.Provider)] == ':' {
			cfg.Provider, cfg.Model = target.Provider, target.Model
		}
	}
	chain, _, err := llmcall.AgentChain(llm, cfg)
	if err != nil || len(chain) == 0 || !llmcall.IsClaudeCode(llm, chain[0]) {
		return nil
	}
	// REQ-CCT-010: the account's allow_scheduled lets it run too.
	if account, _ := llmcall.ClaudeCodeAccount(llm, chain[0].Provider); account.AllowScheduled {
		return nil
	}
	if len(llmcall.WithoutClaudeCode(llm, chain)) == 0 {
		return fmt.Errorf("claude-code is not allowed for scheduled runs of agent '%s'", cfgName)
	}
	return nil
}

// claudeCodeTestTimeout bounds one Router test of a Claude Code address.
const claudeCodeTestTimeout = 60 * time.Second

// claudeCodeTestPrompt is the prompt of a Router test of a Claude Code
// address. The turn has no approver, so any tool call is denied.
const claudeCodeTestPrompt = "Reply with OK"

// TestClaudeCode runs one tiny Claude Code turn on address
// ("<account>/<model>") for the Router test (spec 39 scope addition): the
// prompt "Reply with OK" in an empty temporary workspace, with no lana tools,
// no approver (tool calls are denied), and a 60-second limit. It records the
// call with source "test" and provider = the account, and returns the
// latency, or a *core.ProviderError. The built-in account uses the first
// agent's binary setting, else claude on PATH; another account its own keys
// and CLAUDE_CONFIG_DIR (REQ-CCT-010).
func (rt *Runtime) TestClaudeCode(ctx context.Context, address string) (int64, error) {
	target, ok := llmcall.ParseTarget(address)
	if !ok || !llmcall.IsClaudeCodeAddress(rt.coreCtx.LLM, address) || !llmcall.ValidClaudeCodeModel(target.Model) {
		return 0, &core.ProviderError{Class: "client", Message: "not a claude-code address: " + address}
	}
	accountCfg, _ := llmcall.ClaudeCodeAccount(rt.coreCtx.LLM, target.Provider)
	if accountCfg.Disabled {
		return 0, &core.ProviderError{Class: core.ErrorClassNotFound, Message: fmt.Sprintf("provider %q is disabled", target.Provider)}
	}
	account := rt.claudeAccountSpec(target.Provider, accountCfg)
	if rt.registry == nil {
		return 0, &core.ProviderError{Class: "runtime", Message: "the Claude Code runtime is not available"}
	}
	ext, err := rt.registry.Runtime(ClaudeCodeRuntime)
	if err != nil {
		return 0, &core.ProviderError{Class: "runtime", Message: err.Error()}
	}
	dir, err := os.MkdirTemp("", "lana-claude-test")
	if err != nil {
		return 0, &core.ProviderError{Class: "runtime", Message: err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sessionID := "router-test-" + id.New()
	defer ext.Close(sessionID)
	ctx, cancel := context.WithTimeout(ctx, claudeCodeTestTimeout)
	defer cancel()
	model := target.Model
	if model == "default" {
		model = ""
	}
	served := target.Model
	var usage core.Usage
	var cost float64
	turn := core.RuntimeTurn{
		SessionID: sessionID, TurnID: id.New(), AgentName: "",
		Config: core.RuntimeConfig{
			Model: model, Binary: account.Binary, Workspace: dir, Account: target.Provider,
			ConfigDir: account.ConfigDir, Auth: account.Auth, APIKeyEnv: account.APIKeyEnv,
		},
		Input: []core.ContentBlock{{Kind: core.BlockText, Text: claudeCodeTestPrompt}},
		Emit: func(ev core.RuntimeEvent) {
			switch ev.Kind {
			case core.RuntimeSessionMeta:
				if ev.Model != "" {
					served = ev.Model
				}
			case core.RuntimeUsage:
				usage, cost = ev.Usage, ev.CostUSD
				if ev.Model != "" {
					served = ev.Model
				}
			}
		},
	}
	start := time.Now()
	runErr := ext.RunTurn(ctx, turn)
	latency := time.Since(start).Milliseconds()
	outcome, message := "ok", ""
	var pe *core.ProviderError
	if runErr != nil {
		class := "runtime"
		var early *core.RuntimeEarlyError
		if errors.As(runErr, &early) && early.Class != "" {
			class = early.Class
		}
		if ctx.Err() != nil {
			class, message = "network", "the test timed out"
		} else {
			message = runErr.Error()
		}
		outcome = class
		pe = &core.ProviderError{Class: class, Message: message}
	} else if ctx.Err() != nil {
		outcome, message = "network", "the test timed out"
		pe = &core.ProviderError{Class: outcome, Message: message}
	}
	if rt.router != nil {
		rt.router.RecordExternal(router.CallRecord{
			At: start.UTC(), Source: "test", Requested: address, Provider: target.Provider, Model: served,
			Attempt: 1, Outcome: outcome, InputTokens: int64(usage.InputTokens), OutputTokens: int64(usage.OutputTokens),
			CachedTokens: int64(usage.CachedInputTokens), CostUSD: &cost, DurationMs: latency, Error: truncateRunes(message, 500),
		})
	}
	if pe != nil {
		return 0, pe
	}
	return latency, nil
}

// claudeCodeBinary returns the binary setting of the first agent (in name
// order) that sets one, else "" (claude on PATH).
func (rt *Runtime) claudeCodeBinary() string {
	agents := rt.agents()
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if bin := agents[name].Binary; bin != "" {
			return bin
		}
	}
	return ""
}

// ClaudeCodeInstalled reports whether Claude Code's binary is found: the
// first agent's binary setting when one is set, else claude on PATH. It runs
// no process.
func (rt *Runtime) ClaudeCodeInstalled(context.Context) bool {
	if bin := rt.claudeCodeBinary(); bin != "" {
		if strings.HasPrefix(bin, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				bin = home + bin[1:]
			}
		}
		info, err := os.Stat(bin)
		return err == nil && !info.IsDir()
	}
	_, err := exec.LookPath("claude")
	return err == nil
}

// truncateRunes cuts s to at most n runes.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// turnModel returns the model that served a turn as `lana run` shows it: a
// legacy agent as "provider:model", a router agent as the "provider/model"
// target that answered (else the combo or address it asked for). A Claude
// Code turn answers as "claude-code/<model>" with the model Claude Code
// reported, for example "claude-code/claude-haiku-5-5". An empty turnID or a
// turn with no step reports the requested model.
func (rt *Runtime) turnModel(ctx context.Context, sessionID, turnID, agentName string) string {
	var chain []core.SessionEvent
	if sessionID != "" {
		chain, _ = rt.store.ChainEvents(ctx, sessionID)
	}
	var selection core.ModelSelection
	hasSelection := false
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
		selection, hasSelection = payload, true
	}
	cfg, ok := rt.agentConfig(agentName)
	if !ok {
		return ""
	}
	cfg = llmcall.NormalizeAgent(cfg)
	_, isAgent := rt.agents()[agentName]

	// The served target is the last step/end of the turn, else its last
	// step/start; runtime meta names the model an external runtime runs.
	var served core.StepEnd
	stepEnded := false
	for _, event := range chain {
		switch {
		case turnID == "" || event.TurnID != turnID:
		case event.Kind == core.KindStepEnd:
			var end core.StepEnd
			if json.Unmarshal(event.Payload, &end) == nil && end.Model != "" {
				served, stepEnded = end, true
			}
		case event.Kind == core.KindStepStart && !stepEnded:
			var start core.StepStart
			if json.Unmarshal(event.Payload, &start) == nil && start.Model != "" {
				served = core.StepEnd{Provider: start.Provider, Model: start.Model}
			}
		}
	}

	provider, model := cfg.Provider, cfg.Model
	if hasSelection && isAgent {
		provider, model = llmcall.Select(provider, model, selection)
	}
	if served.Model == "" {
		return llmcall.Display(provider, model)
	}
	if provider != "" {
		return llmcall.Display(served.Provider, served.Model)
	}
	if served.Provider == "" {
		return served.Model
	}
	return served.Provider + "/" + served.Model
}
