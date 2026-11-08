package main

import (
	"context"
	"errors"
	"time"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/web"
)

// Claude Code account sign-in and sign-out for the dashboard (REQ-CCT-012):
// webBackend implements web.ClaudeAccountAuthBackend with the runtime.

var _ web.ClaudeAccountAuthBackend = (*webBackend)(nil)

// errClaudeSignInUnavailable answers a sign-in call without a runtime.
var errClaudeSignInUnavailable = errors.New("the lana runtime is not available")

func claudeRuntimeAccount(ref web.ClaudeAccountRef) core.RuntimeAccount {
	return core.RuntimeAccount{Name: ref.Name, ConfigDir: ref.ConfigDir, Binary: ref.Binary}
}

func claudeSignInJSON(state lana.ClaudeSignIn) web.ClaudeSignInStatus {
	out := web.ClaudeSignInStatus{State: state.State, Error: state.Error}
	if out.State == "" {
		out.State = lana.ClaudeSignInIdle
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

func claudeSignInError(err error) error {
	if errors.Is(err, lana.ErrClaudeSignInRunning) {
		return web.ErrClaudeSignInRunning
	}
	return err
}

// StartClaudeSignIn implements web.ClaudeAccountAuthBackend.
func (b *webBackend) StartClaudeSignIn(ref web.ClaudeAccountRef) (web.ClaudeSignInStatus, error) {
	if b.rt == nil {
		return web.ClaudeSignInStatus{State: lana.ClaudeSignInIdle}, errClaudeSignInUnavailable
	}
	state, err := b.rt.StartClaudeSignIn(claudeRuntimeAccount(ref))
	return claudeSignInJSON(state), claudeSignInError(err)
}

// CancelClaudeSignIn implements web.ClaudeAccountAuthBackend.
func (b *webBackend) CancelClaudeSignIn(name string) web.ClaudeSignInStatus {
	if b.rt == nil {
		return web.ClaudeSignInStatus{State: lana.ClaudeSignInIdle}
	}
	return claudeSignInJSON(b.rt.CancelClaudeSignIn(name))
}

// ClaudeSignInState implements web.ClaudeAccountAuthBackend.
func (b *webBackend) ClaudeSignInState(name string) web.ClaudeSignInStatus {
	if b.rt == nil {
		return web.ClaudeSignInStatus{State: lana.ClaudeSignInIdle}
	}
	return claudeSignInJSON(b.rt.ClaudeSignInState(name))
}

// ForgetClaudeSignIn implements web.ClaudeAccountAuthBackend.
func (b *webBackend) ForgetClaudeSignIn(name string) {
	if b.rt != nil {
		b.rt.ForgetClaudeSignIn(name)
	}
}

// ClaudeSignOut implements web.ClaudeAccountAuthBackend.
func (b *webBackend) ClaudeSignOut(ctx context.Context, ref web.ClaudeAccountRef) error {
	if b.rt == nil {
		return errClaudeSignInUnavailable
	}
	return claudeSignInError(b.rt.ClaudeSignOut(ctx, claudeRuntimeAccount(ref)))
}
