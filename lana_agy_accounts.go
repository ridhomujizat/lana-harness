package lana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/router"
)

// Antigravity accounts in the root package (spec 74 T-005): the account rows
// of the Router's Antigravity page (API-AGY-020), each account's model list
// (REQ-AGY-009, kept in router_kv), the plan tier agy reports in its session
// meta, and the Router test of an Antigravity address.

const (
	// kvAntigravityModelsPrefix starts the router_kv key of an account's last
	// `agy models` list: "antigravity_models/<account>".
	kvAntigravityModelsPrefix = "antigravity_models/"
	// kvAntigravityTierPrefix starts the router_kv key of an account's last
	// plan tier: "antigravity_tier/<account>".
	kvAntigravityTierPrefix = "antigravity_tier/"
)

// agyTiers keeps the last plan tier per Antigravity account.
type agyTiers struct {
	mu    sync.Mutex
	tiers map[string]string
}

// AntigravityUsage24h is an account's use in the last 24 hours, from the
// router call records.
type AntigravityUsage24h struct {
	Calls        int64
	InputTokens  int64
	OutputTokens int64
}

// AntigravityAccount is one row of the Router's Antigravity page
// (API-AGY-020).
type AntigravityAccount struct {
	Name    string
	Builtin bool
	Label   string
	// Home is the account's HOME; "" for the built-in one (the daemon's).
	Home           string
	Binary         string
	Disabled       bool
	AllowScheduled bool
	// Priority, Pool, and CoolingUntil are the pool settings and the
	// cooldown of spec 75 (REQ-PAF-001, REQ-PAF-004).
	Priority     int
	Pool         bool
	CoolingUntil time.Time
	// Status is the account's `agy models` probe: signed in, its version, or
	// the error.
	Status core.RuntimeAccountStatus
	// Tier is the last plan tier session/new reported, else "".
	Tier        string
	ModelsCount int
	// Windows are the plan windows of the account's limit snapshot, with its
	// time and error.
	Windows          []router.Window
	WindowsUpdatedAt time.Time
	WindowsError     string
	Usage24h         AntigravityUsage24h
	Agents           []string
	Combos           []string
}

// antigravityAccountConfig returns the config of an Antigravity account from
// the router (the built-in one has an empty config with the kind set).
func (rt *Runtime) antigravityAccountConfig(name string) core.ProviderConfig {
	if rt.router != nil {
		if cfg, ok := rt.router.AntigravityAccount(name); ok {
			return cfg
		}
	}
	return core.ProviderConfig{Kind: core.AntigravityKind}
}

// antigravityAccountSpec returns the runtime keys of an Antigravity account:
// the built-in one runs with the daemon's HOME; another account with its own
// home ($LANA_HOME/antigravity/<name> by default).
func (rt *Runtime) antigravityAccountSpec(name string, cfg core.ProviderConfig) core.RuntimeAccount {
	if name == "" || name == AntigravityRuntime {
		return core.RuntimeAccount{Name: AntigravityRuntime, Binary: cfg.Binary}
	}
	return core.RuntimeAccount{Name: name, Binary: cfg.Binary, Home: llmcall.AntigravityHomeDir(rt.home, name, cfg)}
}

// AntigravityHome returns the HOME of the Antigravity account name as the
// runtime sets it: "" for the built-in account.
func (rt *Runtime) AntigravityHome(name string) string {
	return rt.antigravityAccountSpec(name, rt.antigravityAccountConfig(name)).Home
}

// antigravityBinaryFound reports whether an Antigravity binary is found: the
// configured one, else agy on PATH. It runs no process.
func antigravityBinaryFound(bin string) bool {
	if bin == "" {
		_, err := exec.LookPath("agy")
		return err == nil
	}
	if strings.HasPrefix(bin, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			bin = home + bin[1:]
		}
	}
	info, err := os.Stat(bin)
	return err == nil && !info.IsDir()
}

// AntigravityInstalled reports whether the built-in account's binary is
// found ([providers.antigravity] binary, else agy on PATH). It runs no
// process.
func (rt *Runtime) AntigravityInstalled(context.Context) bool {
	return antigravityBinaryFound(rt.antigravityAccountConfig(AntigravityRuntime).Binary)
}

// agyModelsPort is the per-account model list of the Antigravity runtime.
type agyModelsPort interface {
	Models(account string) ([]core.RuntimeModel, bool)
}

// agyStatusForget drops the runtime's cached status of one account.
type agyStatusForget interface {
	ForgetAccountStatus(account string)
}

// AntigravityAccounts reports every Antigravity account (API-AGY-020) in
// pool order (spec 75 REQ-PAF-006). Each status runs `agy models` with the
// account's HOME (cached by the runtime, never stored); a list it read
// becomes the account's router model list (API-AGY-026).
func (rt *Runtime) AntigravityAccounts(ctx context.Context) ([]AntigravityAccount, error) {
	if rt.registry == nil || rt.router == nil {
		return nil, fmt.Errorf("agent runtime %q is not available", AntigravityRuntime)
	}
	ext, err := rt.registry.Runtime(AntigravityRuntime)
	if err != nil {
		return nil, err
	}
	statuser, _ := ext.(core.RuntimeAccountStatuser)
	modelsPort, _ := ext.(agyModelsPort)
	snapshots := map[string]router.LimitSnapshot{}
	for _, snap := range rt.router.Limits(ctx, false) {
		snapshots[snap.Provider] = snap
	}
	usage := rt.antigravityUsage24h(ctx)
	names := rt.router.AntigravityAccounts()
	configs := make(map[string]core.ProviderConfig, len(names))
	for _, name := range names {
		configs[name] = rt.antigravityAccountConfig(name)
	}
	names = rt.poolOrder(names, configs)
	cooling := rt.accountCooldowns()

	out := make([]AntigravityAccount, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		cfg := configs[name]
		account := rt.antigravityAccountSpec(name, cfg)
		priority, _ := rt.router.AccountPriority(name)
		snap := snapshots[name]
		row := AntigravityAccount{
			Name: name, Builtin: name == AntigravityRuntime, Label: cfg.Label, Home: account.Home,
			Binary: cfg.Binary, Disabled: cfg.Disabled, AllowScheduled: cfg.AllowScheduled,
			Priority: priority, Pool: cfg.InPool(), CoolingUntil: cooling[name],
			Tier:    rt.AntigravityTier(name),
			Windows: snap.Windows, WindowsUpdatedAt: snap.UpdatedAt, WindowsError: snap.Error,
			Usage24h: usage[name],
		}
		row.Agents, row.Combos = rt.externalUsers(AntigravityRuntime, func(account string) bool { return account == name })
		out[i] = row
		if statuser == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i].Status = statuser.AccountStatus(ctx, account)
			if modelsPort != nil && out[i].Status.LoggedIn {
				if models, ok := modelsPort.Models(name); ok {
					rt.syncAntigravityModels(name, models)
				}
			}
		}()
	}
	wg.Wait()
	for i := range out {
		out[i].ModelsCount = len(rt.router.RuntimeModels(out[i].Name))
	}
	return out, nil
}

// antigravityUsage24h sums the call records of the last 24 hours per
// provider. A store without router records gives nothing.
func (rt *Runtime) antigravityUsage24h(ctx context.Context) map[string]AntigravityUsage24h {
	out := map[string]AntigravityUsage24h{}
	st := rt.RouterStore()
	if st == nil {
		return out
	}
	usage, err := st.RouterUsage(ctx, time.Now().Add(-24*time.Hour), time.Hour)
	if err != nil {
		slog.Warn("router: antigravity usage", "error", err)
		return out
	}
	for _, row := range usage.ByProvider {
		out[row.Provider] = AntigravityUsage24h{Calls: row.Calls, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens}
	}
	return out
}

// syncAntigravityModels stores an account's model list when it differs from
// the router's.
func (rt *Runtime) syncAntigravityModels(account string, models []core.RuntimeModel) {
	if rt.router == nil || len(models) == 0 {
		return
	}
	current := rt.router.RuntimeModels(account)
	if len(current) == len(models) {
		same := true
		for i := range models {
			if current[i] != models[i] {
				same = false
				break
			}
		}
		if same {
			return
		}
	}
	rt.setAntigravityModels(account, models)
}

// setAntigravityModels gives the router the models an Antigravity account
// reported (`agy models`) and keeps them in router_kv as
// "antigravity_models/<account>", so the pickers list them after a restart
// before agy runs again (API-AGY-026).
func (rt *Runtime) setAntigravityModels(account string, models []core.RuntimeModel) {
	if rt.router == nil || account == "" || models == nil {
		return
	}
	rt.router.SetRuntimeModels(account, models)
	if rt.routerKV == nil {
		return
	}
	data, err := json.Marshal(models)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), routerStoreTimeout)
	defer cancel()
	if err := rt.routerKV.PutRouterKV(ctx, kvAntigravityModelsPrefix+account, string(data)); err != nil {
		slog.Warn("router: save antigravity models", "account", account, "error", err)
	}
}

// loadAntigravityState gives the router the stored model list of every
// Antigravity account and loads their stored plan tiers.
func (rt *Runtime) loadAntigravityState() {
	if rt.router == nil || rt.routerKV == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), routerStoreTimeout)
	defer cancel()
	for _, account := range rt.router.AntigravityAccounts() {
		if value, ok, err := rt.routerKV.GetRouterKV(ctx, kvAntigravityModelsPrefix+account); err == nil && ok && value != "" {
			var models []core.RuntimeModel
			if json.Unmarshal([]byte(value), &models) == nil && models != nil {
				rt.router.SetRuntimeModels(account, models)
			}
		}
		if value, ok, err := rt.routerKV.GetRouterKV(ctx, kvAntigravityTierPrefix+account); err == nil && ok && value != "" {
			rt.agyTiers.mu.Lock()
			if rt.agyTiers.tiers == nil {
				rt.agyTiers.tiers = map[string]string{}
			}
			rt.agyTiers.tiers[account] = value
			rt.agyTiers.mu.Unlock()
		}
	}
}

// updateExternalTier keeps the plan tier an Antigravity turn reported for
// its account (API-AGY-020), in memory and in router_kv as
// "antigravity_tier/<account>".
func (rt *Runtime) updateExternalTier(runtime, account, tier string) {
	tier = strings.TrimSpace(tier)
	if runtime != AntigravityRuntime || account == "" || tier == "" {
		return
	}
	tier = truncateRunes(tier, 80)
	rt.agyTiers.mu.Lock()
	if rt.agyTiers.tiers == nil {
		rt.agyTiers.tiers = map[string]string{}
	}
	unchanged := rt.agyTiers.tiers[account] == tier
	rt.agyTiers.tiers[account] = tier
	rt.agyTiers.mu.Unlock()
	if unchanged || rt.routerKV == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), routerStoreTimeout)
	defer cancel()
	if err := rt.routerKV.PutRouterKV(ctx, kvAntigravityTierPrefix+account, tier); err != nil {
		slog.Warn("router: save antigravity tier", "account", account, "error", err)
	}
}

// AntigravityTier returns the last plan tier the account reported, else "".
func (rt *Runtime) AntigravityTier(account string) string {
	rt.agyTiers.mu.Lock()
	defer rt.agyTiers.mu.Unlock()
	return rt.agyTiers.tiers[account]
}

// ForgetAntigravityAccount drops what lana keeps about a removed account: a
// running sign-in, its cached runtime status, and its model list.
func (rt *Runtime) ForgetAntigravityAccount(name string) {
	if name == "" || name == AntigravityRuntime {
		return
	}
	rt.ForgetAntigravitySignIn(name)
	if rt.registry != nil {
		if ext, err := rt.registry.Runtime(AntigravityRuntime); err == nil {
			if f, ok := ext.(agyStatusForget); ok {
				f.ForgetAccountStatus(name)
			}
		}
	}
	if rt.router != nil {
		rt.router.SetRuntimeModels(name, []core.RuntimeModel{})
	}
}

// antigravityTestTimeout bounds one Router test of an Antigravity address.
const antigravityTestTimeout = 90 * time.Second

// TestAntigravity runs one tiny Antigravity turn on address
// ("<account>/<model>") for the Router test, like TestClaudeCode: the prompt
// "Reply with OK" in an empty temporary workspace, with no lana tools, no
// approver (tool calls are denied), and a 90-second limit. It records the
// call with source "test" and provider = the account (API-AGY-028), and
// returns the latency, or a *core.ProviderError.
func (rt *Runtime) TestAntigravity(ctx context.Context, address string) (int64, error) {
	target, ok := llmcall.ParseTarget(address)
	if !ok || address[len(target.Provider)] != '/' {
		return 0, &core.ProviderError{Class: "client", Message: "not an antigravity address: " + address}
	}
	if runtime, ok := llmcall.ExternalRuntime(rt.coreCtx.LLM, target.Provider); !ok || runtime != AntigravityRuntime ||
		(target.Model != "default" && !llmcall.ValidAntigravityModel(target.Model)) {
		return 0, &core.ProviderError{Class: "client", Message: "not an antigravity address: " + address}
	}
	cfg := rt.antigravityAccountConfig(target.Provider)
	if cfg.Disabled {
		return 0, &core.ProviderError{Class: core.ErrorClassNotFound, Message: fmt.Sprintf("provider %q is disabled", target.Provider)}
	}
	account := rt.antigravityAccountSpec(target.Provider, cfg)
	if rt.registry == nil {
		return 0, &core.ProviderError{Class: "runtime", Message: "the Antigravity runtime is not available"}
	}
	ext, err := rt.registry.Runtime(AntigravityRuntime)
	if err != nil {
		return 0, &core.ProviderError{Class: "runtime", Message: err.Error()}
	}
	dir, err := os.MkdirTemp("", "lana-agy-test")
	if err != nil {
		return 0, &core.ProviderError{Class: "runtime", Message: err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sessionID := "router-test-" + id.New()
	defer ext.Close(sessionID)
	ctx, cancel := context.WithTimeout(ctx, antigravityTestTimeout)
	defer cancel()
	var usage core.Usage
	turn := core.RuntimeTurn{
		SessionID: sessionID, TurnID: id.New(),
		Config: core.RuntimeConfig{
			Model: target.Model, Binary: account.Binary, Workspace: dir, Account: target.Provider, Home: account.Home,
		},
		Input: []core.ContentBlock{{Kind: core.BlockText, Text: claudeCodeTestPrompt}},
		Emit: func(ev core.RuntimeEvent) {
			switch ev.Kind {
			case core.RuntimeSessionMeta:
				if ev.Tier != "" {
					rt.updateExternalTier(AntigravityRuntime, target.Provider, ev.Tier)
				}
			case core.RuntimeUsage:
				usage = ev.Usage
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
		cost := 0.0
		rt.router.RecordExternal(router.CallRecord{
			At: start.UTC(), Source: "test", Requested: address, Provider: target.Provider, Model: target.Model,
			Attempt: 1, Outcome: outcome, InputTokens: int64(usage.InputTokens), OutputTokens: int64(usage.OutputTokens),
			CachedTokens: int64(usage.CachedInputTokens), CostUSD: &cost, DurationMs: latency, Error: truncateRunes(message, 500),
		})
	}
	if pe != nil {
		return 0, pe
	}
	return latency, nil
}
