package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/web"
)

// Antigravity accounts for the dashboard (spec 74 API-AGY-020..027):
// webBackend implements web.AntigravityBackend with the runtime.

var (
	_ web.AntigravityBackend        = (*webBackend)(nil)
	_ web.ProviderKeyCoolingBackend = (*webBackend)(nil)
)

// errAntigravityUnavailable answers an Antigravity call without a runtime.
var errAntigravityUnavailable = errors.New("the lana runtime is not available")

func agySignInJSON(state lana.AntigravitySignIn) web.AntigravitySignInStatus {
	out := web.AntigravitySignInStatus{State: state.State, Error: state.Error}
	if out.State == "" {
		out.State = lana.AntigravitySignInIdle
	}
	if out.State == lana.AntigravitySignInWaitingCode {
		out.URL = state.URL
	}
	if !state.StartedAt.IsZero() {
		at := state.StartedAt.UTC().Truncate(time.Millisecond)
		out.StartedAt = &at
	}
	if !state.FinishedAt.IsZero() {
		at := state.FinishedAt.UTC().Truncate(time.Millisecond)
		out.FinishedAt = &at
	}
	return out
}

func agySignInError(err error) error {
	switch {
	case errors.Is(err, lana.ErrAntigravitySignInRunning):
		return web.ErrAntigravitySignInRunning
	case errors.Is(err, lana.ErrAntigravityBuiltin):
		return web.ErrAntigravityBuiltin
	case errors.Is(err, lana.ErrAntigravityNotWaiting):
		return web.ErrAntigravityNotWaiting
	case errors.Is(err, lana.ErrAntigravityBadCode):
		return web.ErrAntigravityBadCode
	}
	return err
}

// AntigravityInstalled implements web.AntigravityBackend.
func (b *webBackend) AntigravityInstalled(ctx context.Context) bool {
	return b.rt != nil && b.rt.AntigravityInstalled(ctx)
}

// StartAntigravitySignIn implements web.AntigravityBackend.
func (b *webBackend) StartAntigravitySignIn(ref web.AntigravityAccountRef) (web.AntigravitySignInStatus, error) {
	if b.rt == nil {
		return web.AntigravitySignInStatus{State: lana.AntigravitySignInIdle}, errAntigravityUnavailable
	}
	state, err := b.rt.StartAntigravitySignIn(core.RuntimeAccount{Name: ref.Name, Home: ref.Home, Binary: ref.Binary})
	return agySignInJSON(state), agySignInError(err)
}

// AntigravitySignInState implements web.AntigravityBackend.
func (b *webBackend) AntigravitySignInState(name string) web.AntigravitySignInStatus {
	if b.rt == nil {
		return web.AntigravitySignInStatus{State: lana.AntigravitySignInIdle}
	}
	return agySignInJSON(b.rt.AntigravitySignInState(name))
}

// SubmitAntigravityCode implements web.AntigravityBackend.
func (b *webBackend) SubmitAntigravityCode(name, code string) (web.AntigravitySignInStatus, error) {
	if b.rt == nil {
		return web.AntigravitySignInStatus{State: lana.AntigravitySignInIdle}, errAntigravityUnavailable
	}
	state, err := b.rt.SubmitAntigravityCode(name, code)
	return agySignInJSON(state), agySignInError(err)
}

// CancelAntigravitySignIn implements web.AntigravityBackend.
func (b *webBackend) CancelAntigravitySignIn(name string) web.AntigravitySignInStatus {
	if b.rt == nil {
		return web.AntigravitySignInStatus{State: lana.AntigravitySignInIdle}
	}
	return agySignInJSON(b.rt.CancelAntigravitySignIn(name))
}

// ForgetAntigravityAccount implements web.AntigravityBackend.
func (b *webBackend) ForgetAntigravityAccount(name string) {
	if b.rt != nil {
		b.rt.ForgetAntigravityAccount(name)
	}
}

// AntigravityStatus implements web.AntigravityBackend (API-AGY-020): the
// runtime's binary and version, and every account in pool order with its
// sign-in, tier, windows, 24-hour usage, and users. The probes run `agy
// --version` and `agy models` (cached) and never read a credential.
func (b *webBackend) AntigravityStatus(ctx context.Context) (web.AntigravityStatus, error) {
	out := web.AntigravityStatus{MinVersion: web.AntigravityMinVersion, Accounts: []web.AntigravityAccountStatus{}}
	if b.rt == nil {
		return out, nil
	}
	out.Installed = b.rt.AntigravityInstalled(ctx)
	if out.Installed {
		status, err := b.rt.AgentRuntimeStatus(ctx, lana.AntigravityRuntime)
		if err != nil {
			out.Error = err.Error()
			return out, nil
		}
		out.Binary, out.Version, out.VersionOK, out.Error = status.Binary, status.Version, status.VersionOK, status.Error
		out.Running, out.Queued, out.MaxConcurrent = status.Running, status.Queued, status.MaxConcurrent
	} else {
		out.Error = "Install the Antigravity CLI (agy) to use Antigravity models."
	}
	out.PoolWarning = b.rt.PoolWarning(lana.AntigravityRuntime)
	accounts, err := b.rt.AntigravityAccounts(ctx)
	if err != nil {
		slog.Warn("web: antigravity accounts", "error", err)
		return out, nil
	}
	for _, account := range accounts {
		row := web.AntigravityAccountStatus{
			Name: account.Name, Builtin: account.Builtin, Label: account.Label, Home: account.Home,
			Binary: account.Status.Binary, Disabled: account.Disabled, AllowScheduled: account.AllowScheduled,
			SignedIn: account.Status.LoggedIn, Error: account.Status.Error, Tier: account.Tier,
			ModelsCount: account.ModelsCount, Running: account.Status.Running, Queued: account.Status.Queued,
			Windows:          agyWindows(account),
			WindowsUpdatedAt: optionalTime(account.WindowsUpdatedAt), WindowsError: account.WindowsError,
			Usage24h: web.AntigravityUsage{
				Calls: account.Usage24h.Calls, InputTokens: account.Usage24h.InputTokens, OutputTokens: account.Usage24h.OutputTokens,
			},
			Agents: account.Agents, Combos: account.Combos,
			Priority: account.Priority, Pool: account.Pool, CoolingUntil: optionalTime(account.CoolingUntil),
		}
		if row.Binary == "" {
			row.Binary = account.Binary
		}
		out.Accounts = append(out.Accounts, row)
	}
	return out, nil
}

// agyWindows converts an account's limit windows ("<group>/5h",
// "<group>/weekly", utilization) to the page's windows (group, type,
// remaining).
func agyWindows(account lana.AntigravityAccount) []web.AntigravityWindow {
	out := make([]web.AntigravityWindow, 0, len(account.Windows))
	for _, w := range account.Windows {
		group, typ := w.Type, ""
		if i := strings.LastIndex(w.Type, "/"); i >= 0 {
			group, typ = w.Type[:i], w.Type[i+1:]
		}
		window := web.AntigravityWindow{Group: group, Type: typ, Source: w.Source}
		if w.Utilization != nil {
			remaining := 1 - *w.Utilization
			if remaining < 0 {
				remaining = 0
			}
			window.Remaining = &remaining
		}
		if w.ResetsAt != nil {
			at := w.ResetsAt.UTC()
			window.ResetsAt = &at
		}
		out = append(out, window)
	}
	return out
}
