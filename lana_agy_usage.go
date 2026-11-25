package lana

import (
	"context"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/router"
)

// Usage refresh of Antigravity accounts (REQ-AGY-009, API-AGY-027). A refresh
// runs `agy -p /usage --output-format json` (a local command, no model call)
// for every enabled, signed-in account, in parallel, at most once per 60 s
// per account, and merges each group's 5-hour and weekly windows into the
// account's limit snapshot with kind "antigravity" and source
// "usage_probe". A failed probe sets the snapshot's error and keeps its old
// values and time. The Router page asks for it, and every Antigravity turn
// asks for its own account when it ends.

// RefreshAntigravityUsage probes the plan usage of every enabled, signed-in
// Antigravity account whose binary is found, and waits for the probes or
// ctx. Probes run under the runtime's context, so a client that goes away
// does not cut them short; an account probed less than 60 s ago keeps its
// snapshot.
func (rt *Runtime) RefreshAntigravityUsage(ctx context.Context) {
	if rt.registry == nil || rt.router == nil {
		return
	}
	ext, err := rt.registry.Runtime(AntigravityRuntime)
	if err != nil {
		return
	}
	prober, ok := ext.(core.RuntimeUsageProber)
	if !ok {
		return
	}
	statuser, _ := ext.(core.RuntimeAccountStatuser)
	now := time.Now()
	var wg sync.WaitGroup
	for _, name := range rt.router.AntigravityAccounts() {
		cfg := rt.antigravityAccountConfig(name)
		if cfg.Disabled || !antigravityBinaryFound(cfg.Binary) {
			continue
		}
		if !rt.agyUsage.claim(name, now) {
			continue
		}
		account := rt.antigravityAccountSpec(name, cfg)
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Only a signed-in account is probed (the status is cached).
			if statuser != nil && !statuser.AccountStatus(rt.ctx, account).LoggedIn {
				return
			}
			rt.probeAntigravityUsage(rt.ctx, prober, account)
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

// refreshAntigravityAccountUsage probes one account after its turn ended,
// unless it was probed less than 60 s ago.
func (rt *Runtime) refreshAntigravityAccountUsage(name string) {
	if rt.registry == nil || rt.router == nil || rt.closed.Load() {
		return
	}
	cfg, ok := rt.router.AntigravityAccount(name)
	if !ok || cfg.Disabled {
		return
	}
	ext, err := rt.registry.Runtime(AntigravityRuntime)
	if err != nil {
		return
	}
	prober, ok := ext.(core.RuntimeUsageProber)
	if !ok || !rt.agyUsage.claim(name, time.Now()) {
		return
	}
	rt.probeAntigravityUsage(rt.ctx, prober, rt.antigravityAccountSpec(name, cfg))
}

// probeAntigravityUsage runs one probe and stores its outcome in the
// account's limit snapshot.
func (rt *Runtime) probeAntigravityUsage(ctx context.Context, prober core.RuntimeUsageProber, account core.RuntimeAccount) {
	report, err := prober.UsageProbe(ctx, account)
	if ctx.Err() != nil {
		// lana is shutting down: nothing is recorded.
		return
	}
	update := router.LimitUpdate{Provider: account.Name, Kind: llmcall.Antigravity}
	if err != nil {
		msg := truncateRunes(err.Error(), claudeUsageErrorRunes)
		update.Error, update.KeepTime = &msg, true
	} else {
		noError := ""
		update.Error = &noError
		update.Windows = externalWindows(report.Windows, router.WindowSourceUsageProbe)
		update.MergeWindows = true
	}
	rt.router.UpdateLimits(update)
}
