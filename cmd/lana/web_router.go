package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/internal/routerdb"
	"github.com/ridhomujizat/lana-harness/router"
	"github.com/ridhomujizat/lana-harness/router/openaiproxy"
	"github.com/ridhomujizat/lana-harness/web"
)

// The Router page port of spec 37 (T-RTR-06): webBackend implements
// web.RouterBackend over the runtime's router (rt.Router) and its store
// (rt.RouterStore).

var _ web.RouterBackend = (*webBackend)(nil)

const (
	// routerTestTimeout is the 15-second limit of API-RTR-010.
	routerTestTimeout = 15 * time.Second
	// proxyAuthTimeout bounds one proxy key lookup.
	proxyAuthTimeout = 5 * time.Second
	// proxyTouchEvery throttles the last-used update of a proxy key.
	proxyTouchEvery = time.Minute
)

// routerProxyState is the lazily built proxy handler and the last-used
// throttle of the proxy keys.
type routerProxyState struct {
	once    sync.Once
	handler http.Handler

	mu      sync.Mutex
	touched map[string]time.Time
}

func (b *webBackend) llmRouter() (*router.Router, error) {
	if b.rt == nil || b.rt.Router() == nil {
		return nil, web.ErrRouterUnavailable
	}
	return b.rt.Router(), nil
}

func (b *webBackend) routerStore() (routerdb.Store, error) {
	if b.rt == nil {
		return nil, web.ErrRouterUnavailable
	}
	st := b.rt.RouterStore()
	if st == nil {
		return nil, web.ErrRouterUnavailable
	}
	return st, nil
}

// RouterStatus implements web.RouterBackend: the cooling targets, soonest
// end first, and the live proxy switch.
func (b *webBackend) RouterStatus(context.Context) (web.RouterStatus, error) {
	r, err := b.llmRouter()
	if err != nil {
		return web.RouterStatus{}, err
	}
	status := web.RouterStatus{Cooling: []web.RouterCooling{}, ProxyEnabled: b.rt.RouterConfig().Proxy}
	for target, until := range r.Cooling() {
		status.Cooling = append(status.Cooling, web.RouterCooling{Target: router.Address(target), Until: until.UTC()})
	}
	sort.Slice(status.Cooling, func(i, j int) bool {
		if !status.Cooling[i].Until.Equal(status.Cooling[j].Until) {
			return status.Cooling[i].Until.Before(status.Cooling[j].Until)
		}
		return status.Cooling[i].Target < status.Cooling[j].Target
	})
	return status, nil
}

// RouterModels implements web.RouterBackend (API-RTR-009). A failed models
// endpoint keeps the catalog models and reports the error.
func (b *webBackend) RouterModels(ctx context.Context, provider string, refresh bool) (web.RouterModelList, error) {
	r, err := b.llmRouter()
	if err != nil {
		return web.RouterModelList{}, err
	}
	// Spec 39: claude-code is built in, not a configured provider; Kind
	// knows it and every Claude Code account.
	if _, ok := r.Kind(provider); !ok {
		return web.RouterModelList{}, web.ErrRouterUnknownProvider
	}
	infos, fetchErr := r.Models(ctx, provider, refresh)
	if errors.Is(fetchErr, router.ErrUnknownAddress) {
		return web.RouterModelList{}, web.ErrRouterUnknownProvider
	}
	list := web.RouterModelList{Provider: provider, Models: make([]web.RouterModel, 0, len(infos))}
	if fetchErr != nil {
		list.FetchError = fetchErr.Error()
	}
	if at, ok := r.ModelsFetchedAt(provider); ok {
		at = at.UTC()
		list.FetchedAt = &at
	}
	for _, info := range infos {
		list.Models = append(list.Models, web.RouterModel{
			ID: info.ID, Address: info.Address, Name: info.Name,
			ContextWindow: info.ContextWindow, MaxOutput: info.MaxOutput,
			Vision: info.Vision, Thinking: info.Thinking, Price: info.Price, Source: info.Source,
		})
	}
	return list, nil
}

// RouterTest implements web.RouterBackend (API-RTR-010): one "ping" with
// max_tokens 1 through the router with source "test" and a 15-second limit.
// A single address stops at its first failed attempt instead of retrying; a
// combo falls back like any call.
func (b *webBackend) RouterTest(ctx context.Context, address string) (web.RouterTestResult, error) {
	r, err := b.llmRouter()
	if err != nil {
		return web.RouterTestResult{}, err
	}
	// Spec 39: a Claude Code address runs one tiny Claude Code turn.
	if llmcall.IsClaudeCodeAddress(r, address) {
		if _, err := r.Resolve(address); err != nil {
			return web.RouterTestResult{}, web.ErrRouterUnknownAddress
		}
		latency, err := b.rt.TestClaudeCode(ctx, address)
		if err != nil {
			return routerTestFailure(err), nil
		}
		return web.RouterTestResult{OK: true, LatencyMS: latency}, nil
	}
	chain, err := r.Resolve(address)
	if err != nil {
		return web.RouterTestResult{}, web.ErrRouterUnknownAddress
	}
	ctx, cancel := context.WithTimeout(ctx, routerTestTimeout)
	defer cancel()
	var first *core.ProviderError
	onAttempt := func(attempt router.Attempt) {
		if first == nil {
			first = attempt.Err
		}
		if len(chain) == 1 {
			cancel()
		}
	}
	start := time.Now()
	stream, _, err := r.StreamChain(ctx, chain, core.ChatRequest{
		Messages: []core.Message{{
			Role:    core.RoleUser,
			Content: []core.ContentBlock{{Kind: core.BlockText, Text: providerTestPrompt}},
		}},
		MaxTokens: 1,
	}, router.CallMeta{Source: "test"}, onAttempt)
	if err != nil {
		if first != nil && len(chain) == 1 {
			err = first
		}
		return routerTestFailure(err), nil
	}
	for _, streamErr := range stream {
		if streamErr != nil {
			return routerTestFailure(streamErr), nil
		}
	}
	return web.RouterTestResult{OK: true, LatencyMS: time.Since(start).Milliseconds()}, nil
}

func routerTestFailure(err error) web.RouterTestResult {
	var pe *core.ProviderError
	if errors.As(err, &pe) {
		return web.RouterTestResult{Class: pe.Class, Status: pe.Status, Message: pe.Message}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return web.RouterTestResult{Class: "network", Message: "the test timed out"}
	}
	return web.RouterTestResult{Class: "network", Message: err.Error()}
}

// routerUsageWindow maps a usage range to its start and bucket size
// (API-RTR-011): 24h in 1-hour buckets, 7d and 30d in 1-day buckets.
func routerUsageWindow(rangeName string, now time.Time) (time.Time, time.Duration, bool) {
	switch rangeName {
	case "24h":
		return now.Add(-24 * time.Hour), time.Hour, true
	case "7d":
		return now.Add(-7 * 24 * time.Hour), 24 * time.Hour, true
	case "30d":
		return now.Add(-30 * 24 * time.Hour), 24 * time.Hour, true
	}
	return time.Time{}, 0, false
}

// RouterUsage implements web.RouterBackend (API-RTR-011).
func (b *webBackend) RouterUsage(ctx context.Context, rangeName string) (routerdb.Usage, error) {
	st, err := b.routerStore()
	if err != nil {
		return routerdb.Usage{}, err
	}
	since, bucket, ok := routerUsageWindow(rangeName, time.Now())
	if !ok {
		return routerdb.Usage{}, errors.New("unknown usage range " + rangeName)
	}
	return st.RouterUsage(ctx, since, bucket)
}

// RouterCalls implements web.RouterBackend (API-RTR-012). The page size is
// capped at 200; before is exclusive.
func (b *webBackend) RouterCalls(ctx context.Context, filter routerdb.CallFilter) (routerdb.CallPage, error) {
	st, err := b.routerStore()
	if err != nil {
		return routerdb.CallPage{}, err
	}
	filter.Limit = min(filter.Limit, routerdb.MaxCallsLimit)
	return st.ListRouterCalls(ctx, filter)
}

// RouterLimits implements web.RouterBackend (API-RTR-013). A refresh fetches
// the API providers' balances and, in parallel, probes the plan usage of the
// Claude Code accounts (spec 39 Revision 4); both are throttled per provider.
func (b *webBackend) RouterLimits(ctx context.Context, refresh bool) ([]web.RouterLimit, error) {
	r, err := b.llmRouter()
	if err != nil {
		return nil, err
	}
	if refresh {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.rt.RefreshClaudeUsage(ctx)
		}()
		r.Limits(ctx, true)
		wg.Wait()
	}
	snapshots := r.Limits(ctx, false)
	out := make([]web.RouterLimit, 0, len(snapshots))
	for _, snap := range snapshots {
		out = append(out, webRouterLimit(snap))
	}
	return out, nil
}

func webRouterLimit(snap router.LimitSnapshot) web.RouterLimit {
	out := web.RouterLimit{
		Provider: snap.Provider,
		Kind:     snap.Kind,
		Error:    snap.Error,
		Windows:  make([]web.RouterPlanWindow, 0, len(snap.Windows)),
	}
	if !snap.UpdatedAt.IsZero() {
		at := snap.UpdatedAt.UTC()
		out.UpdatedAt = &at
	}
	if snap.Rate != nil {
		out.Rate = &web.RouterRateLimits{
			Requests:     webRateLimit(snap.Rate.Requests),
			Tokens:       webRateLimit(snap.Rate.Tokens),
			InputTokens:  webRateLimit(snap.Rate.InputTokens),
			OutputTokens: webRateLimit(snap.Rate.OutputTokens),
		}
	}
	if snap.Credit != nil {
		out.Credit = &web.RouterCredit{LimitUSD: snap.Credit.LimitUSD, UsedUSD: snap.Credit.UsedUSD, RemainingUSD: snap.Credit.RemainingUSD}
	}
	if snap.Balance != nil {
		out.Balance = &web.RouterBalance{
			Currency: snap.Balance.Currency, Total: snap.Balance.Total,
			Granted: snap.Balance.Granted, ToppedUp: snap.Balance.ToppedUp,
		}
	}
	for _, window := range snap.Windows {
		out.Windows = append(out.Windows, web.RouterPlanWindow{
			Type: window.Type, Status: window.Status, Utilization: window.Utilization, ResetsAt: window.ResetsAt,
			Label: window.Label, Source: window.Source,
		})
	}
	return out
}

func webRateLimit(limit *router.RateLimit) *web.RouterRateLimit {
	if limit == nil {
		return nil
	}
	return &web.RouterRateLimit{Limit: limit.Limit, Remaining: limit.Remaining, ResetAt: limit.ResetAt}
}

// RouterKeys implements web.RouterBackend (API-RTR-014).
func (b *webBackend) RouterKeys(ctx context.Context) ([]routerdb.Key, error) {
	st, err := b.routerStore()
	if err != nil {
		return nil, err
	}
	return st.ListRouterKeys(ctx)
}

// CreateRouterKey implements web.RouterBackend (API-RTR-014, REQ-RTR-016).
// Only the SHA-256 of the key is stored; the key is returned once.
func (b *webBackend) CreateRouterKey(ctx context.Context, name string) (web.RouterKeyCreated, error) {
	st, err := b.routerStore()
	if err != nil {
		return web.RouterKeyCreated{}, err
	}
	key, prefix, err := lana.GenerateProxyKey()
	if err != nil {
		return web.RouterKeyCreated{}, err
	}
	var idBytes [8]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return web.RouterKeyCreated{}, err
	}
	id := "k_" + hex.EncodeToString(idBytes[:])
	if err := st.CreateRouterKey(ctx, routerdb.Key{
		ID: id, Name: name, Prefix: prefix, Hash: lana.HashProxyKey(key), CreatedAt: time.Now().UTC(),
	}); err != nil {
		return web.RouterKeyCreated{}, err
	}
	return web.RouterKeyCreated{ID: id, Name: name, Prefix: prefix, Key: key}, nil
}

// RevokeRouterKey implements web.RouterBackend (API-RTR-014).
func (b *webBackend) RevokeRouterKey(ctx context.Context, id string) error {
	st, err := b.routerStore()
	if err != nil {
		return err
	}
	return st.RevokeRouterKey(ctx, id)
}

// RouterProxyHandler implements web.RouterBackend (REQ-RTR-015). It is nil
// while [router] proxy is off; the web server reads it per request, so the
// switch applies live.
func (b *webBackend) RouterProxyHandler() http.Handler {
	r, err := b.llmRouter()
	if err != nil || !b.rt.RouterConfig().Proxy {
		return nil
	}
	if _, err := b.routerStore(); err != nil {
		return nil
	}
	b.proxy.once.Do(func() {
		b.proxy.handler = openaiproxy.NewHandler(r, b.proxyAuth, openaiproxy.WithOnRequest(b.touchProxyKey))
	})
	return b.proxy.handler
}

// proxyAuth checks a proxy bearer key: its SHA-256 must name a live key
// (REQ-RTR-016). The key itself is never logged.
func (b *webBackend) proxyAuth(bearer string) (string, bool) {
	if bearer == "" {
		return "", false
	}
	st, err := b.routerStore()
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), proxyAuthTimeout)
	defer cancel()
	key, ok, err := st.LookupRouterKeyByHash(ctx, lana.HashProxyKey(bearer))
	if err != nil {
		slog.Warn("router proxy: key lookup failed", "error", err)
		return "", false
	}
	if !ok {
		return "", false
	}
	return key.ID, true
}

// touchProxyKey records a key's last use at most once a minute per key, off
// the request path.
func (b *webBackend) touchProxyKey(id string) {
	now := time.Now().UTC()
	b.proxy.mu.Lock()
	if b.proxy.touched == nil {
		b.proxy.touched = map[string]time.Time{}
	}
	if last, ok := b.proxy.touched[id]; ok && now.Sub(last) < proxyTouchEvery {
		b.proxy.mu.Unlock()
		return
	}
	b.proxy.touched[id] = now
	b.proxy.mu.Unlock()
	st, err := b.routerStore()
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), proxyAuthTimeout)
		defer cancel()
		if err := st.TouchRouterKey(ctx, id, now); err != nil {
			slog.Warn("router proxy: update key last use", "error", err)
		}
	}()
}

// ClaudeCodeInstalled implements web.ClaudeCodeBackend: the claude binary is
// found (spec 39, API-CCT-004).
func (b *webBackend) ClaudeCodeInstalled(ctx context.Context) bool {
	return b.rt != nil && b.rt.ClaudeCodeInstalled(ctx)
}

// ClaudeAgentStatus implements web.RouterBackend (API-CCR-003). Without a
// configured Claude Agent it reports none and starts nothing; otherwise the
// claude-code runtime probes `claude --version` and `claude auth status`
// (cached 60 s) and never reads a credential.
func (b *webBackend) ClaudeAgentStatus(ctx context.Context) (web.ClaudeAgentStatus, error) {
	if b.rt == nil {
		return web.ClaudeAgentStatus{ConfiguredAgents: []string{}}, nil
	}
	// API-CCT-005: the agents and combos whose chains use claude-code. The
	// status is probed even with no users, for the Router's Claude Code card.
	agents := b.rt.RuntimeAgents(lana.ClaudeCodeRuntime)
	status, err := b.rt.AgentRuntimeStatus(ctx, lana.ClaudeCodeRuntime)
	if err != nil {
		return web.ClaudeAgentStatus{ConfiguredAgents: agents, Error: err.Error()}, nil
	}
	out := web.ClaudeAgentStatus{
		ConfiguredAgents: agents,
		Binary:           status.Binary,
		Version:          status.Version,
		VersionOK:        status.VersionOK,
		LoggedIn:         status.LoggedIn,
		AuthMethod:       status.AuthMethod,
		Error:            status.Error,
		Running:          status.Running,
		Queued:           status.Queued,
		MaxConcurrent:    status.MaxConcurrent,
		Accounts:         []web.ClaudeCodeAccountStatus{},
	}
	// API-CCT-011: one row per account.
	accounts, err := b.rt.ClaudeCodeAccounts(ctx)
	if err != nil {
		slog.Warn("web: claude code accounts", "error", err)
		return out, nil
	}
	for _, account := range accounts {
		row := web.ClaudeCodeAccountStatus{
			Name: account.Name, Builtin: account.Builtin, ConfigDir: account.ConfigDir,
			AllowScheduled: account.AllowScheduled, Disabled: account.Disabled,
			Binary: account.Status.Binary, Version: account.Status.Version, VersionOK: account.Status.VersionOK,
			LoggedIn: account.Status.LoggedIn, Email: account.Status.Email, Plan: account.Status.Plan,
			AuthMethod: account.Status.AuthMethod, Error: account.Status.Error,
			Running: account.Status.Running, Queued: account.Status.Queued,
			LoginCommand: account.LoginCommand,
			Windows:      make([]web.RouterPlanWindow, 0, len(account.Windows)),
			Agents:       account.Agents, Combos: account.Combos,
		}
		for _, w := range account.Windows {
			window := web.RouterPlanWindow{
				Type: w.Type, Status: w.Status, Utilization: w.Utilization, Label: w.Label, Source: w.Source,
			}
			if w.ResetsAt != nil {
				at := w.ResetsAt.UTC()
				window.ResetsAt = &at
			}
			row.Windows = append(row.Windows, window)
		}
		out.Accounts = append(out.Accounts, row)
	}
	return out, nil
}
