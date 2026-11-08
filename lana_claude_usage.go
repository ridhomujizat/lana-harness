package lana

import (
	"context"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/router"
)

// Usage refresh of Claude Code accounts (spec 39 Revision 4, ADR 0020
// amendment 2026-11-08). The plan windows of an account otherwise move only
// with the rate_limit_event of its turns. A refresh runs `claude -p /usage`
// (a local command, no model call) for every enabled subscription account,
// in parallel, at most once per claudeUsageRefreshEvery per account, and
// merges the windows into the account's limit snapshot with source
// "usage_probe". A failed probe sets the snapshot's error and keeps its old
// values and time.

// claudeUsageRefreshEvery is the shortest gap between two usage probes of
// one account.
const claudeUsageRefreshEvery = 60 * time.Second

// claudeUsageErrorRunes caps the stored probe error.
const claudeUsageErrorRunes = 300

// errNotSubscription is the snapshot error of an account whose /usage shows
// no subscription.
const errNotSubscription = "Not a subscription: Claude Code /usage shows no plan windows for this account."

// claudeUsageThrottle holds the start of the last usage probe per account.
type claudeUsageThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// claim reports whether a probe of name may start at now, and records it.
func (c *claudeUsageThrottle) claim(name string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if last, ok := c.last[name]; ok && now.Sub(last) < claudeUsageRefreshEvery {
		return false
	}
	if c.last == nil {
		c.last = map[string]time.Time{}
	}
	c.last[name] = now
	return true
}

// RefreshClaudeUsage probes the plan usage of every enabled Claude Code
// subscription account and waits for the probes or ctx. The built-in
// account is probed when Claude Code is installed. Probes run under the
// runtime's context, so a client that goes away does not cut them short; an
// account probed less than 60 s ago keeps its snapshot.
func (rt *Runtime) RefreshClaudeUsage(ctx context.Context) {
	if rt.registry == nil || rt.router == nil {
		return
	}
	ext, err := rt.registry.Runtime(ClaudeCodeRuntime)
	if err != nil {
		return
	}
	prober, ok := ext.(core.RuntimeUsageProber)
	if !ok {
		return
	}
	now := time.Now()
	names := append([]string{ClaudeCodeRuntime}, rt.router.ClaudeCodeAccounts()...)
	var wg sync.WaitGroup
	for _, name := range names {
		cfg, _ := rt.router.ClaudeCodeAccount(name)
		if cfg.Disabled || cfg.Auth == core.RuntimeAuthAPIKey {
			continue
		}
		if name == ClaudeCodeRuntime && !rt.ClaudeCodeInstalled(ctx) {
			continue
		}
		if !rt.claudeUsage.claim(name, now) {
			continue
		}
		account := rt.claudeAccountSpec(name, cfg)
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt.probeClaudeUsage(rt.ctx, prober, account)
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// probeClaudeUsage runs one probe and stores its outcome in the account's
// limit snapshot.
func (rt *Runtime) probeClaudeUsage(ctx context.Context, prober core.RuntimeUsageProber, account core.RuntimeAccount) {
	report, err := prober.UsageProbe(ctx, account)
	if ctx.Err() != nil {
		// lana is shutting down: nothing is recorded.
		return
	}
	update := router.LimitUpdate{Provider: account.Name, Kind: llmcall.ClaudeCode}
	switch {
	case err != nil:
		msg := truncateRunes(err.Error(), claudeUsageErrorRunes)
		update.Error, update.KeepTime = &msg, true
	case !report.Subscription:
		msg := errNotSubscription
		update.Error, update.KeepTime = &msg, true
	default:
		noError := ""
		update.Error = &noError
		update.Windows = externalWindows(report.Windows, router.WindowSourceUsageProbe)
		update.MergeWindows = true
	}
	rt.router.UpdateLimits(update)
}
