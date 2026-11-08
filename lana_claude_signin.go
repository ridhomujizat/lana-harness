package lana

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Sign-in and sign-out of Claude Code accounts from the dashboard
// (REQ-CCT-012, ADR 0020 amendment 2026-11-08). The Claude Code runtime runs
// the official `claude auth login` / `claude auth logout` with the account's
// folder; lana keeps only the state of the last sign-in per account and a
// short, URL-stripped error line, in memory.

// Sign-in states of ClaudeSignIn.State.
const (
	ClaudeSignInIdle      = "idle"
	ClaudeSignInRunning   = "running"
	ClaudeSignInSucceeded = "succeeded"
	ClaudeSignInFailed    = "failed"
	ClaudeSignInCancelled = "cancelled"
)

// ErrClaudeSignInRunning is the error of a sign-in or sign-out started while
// one of the same account runs.
var ErrClaudeSignInRunning = core.ErrRuntimeSignInRunning

// claudeSignInErrorRunes caps the reported error.
const claudeSignInErrorRunes = 240

// claudeCancelWait bounds how long a cancel waits for the sign-in to stop.
const claudeCancelWait = 15 * time.Second

// ClaudeSignIn is the state of the last sign-in of one account. Error is set
// for State failed only; it never holds a URL, code, or token.
type ClaudeSignIn struct {
	State      string
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

// claudeSignIns holds the last sign-in per account name.
type claudeSignIns struct {
	mu   sync.Mutex
	runs map[string]*claudeSignInRun
}

type claudeSignInRun struct {
	state     ClaudeSignIn
	cancel    context.CancelFunc
	cancelled bool
	done      chan struct{}
}

// claudeAccountAuth returns the Claude Code runtime's sign-in port.
func (rt *Runtime) claudeAccountAuth() (core.RuntimeAccountAuth, error) {
	if rt.registry == nil {
		return nil, fmt.Errorf("agent runtime %q is not available", ClaudeCodeRuntime)
	}
	ext, err := rt.registry.Runtime(ClaudeCodeRuntime)
	if err != nil {
		return nil, err
	}
	auth, ok := ext.(core.RuntimeAccountAuth)
	if !ok {
		return nil, errors.New("the Claude Code runtime cannot sign accounts in")
	}
	return auth, nil
}

// claudeAuthAccount fills the built-in account: no folder (the owner's own
// ~/.claude) and, without a binary, the first agent's binary setting.
func (rt *Runtime) claudeAuthAccount(account core.RuntimeAccount) core.RuntimeAccount {
	if account.Name == "" || account.Name == ClaudeCodeRuntime {
		account.Name, account.ConfigDir = ClaudeCodeRuntime, ""
		if account.Binary == "" {
			account.Binary = rt.claudeCodeBinary()
		}
	}
	return account
}

// StartClaudeSignIn starts `claude auth login` for the account in the
// background and returns the running state (REQ-CCT-012). The caller passes
// the account's folder and binary as config.toml has them, so an account
// just written works before the config is applied. It returns
// ErrClaudeSignInRunning while a sign-in of the account runs.
func (rt *Runtime) StartClaudeSignIn(account core.RuntimeAccount) (ClaudeSignIn, error) {
	if rt.closed.Load() {
		return ClaudeSignIn{}, errors.New("lana is shutting down")
	}
	auth, err := rt.claudeAccountAuth()
	if err != nil {
		return ClaudeSignIn{}, err
	}
	account = rt.claudeAuthAccount(account)
	s := &rt.claudeSignIns
	s.mu.Lock()
	if run := s.runs[account.Name]; run != nil && run.state.State == ClaudeSignInRunning {
		s.mu.Unlock()
		return run.state, ErrClaudeSignInRunning
	}
	if s.runs == nil {
		s.runs = map[string]*claudeSignInRun{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &claudeSignInRun{
		state:  ClaudeSignIn{State: ClaudeSignInRunning, StartedAt: time.Now().UTC()},
		cancel: cancel, done: make(chan struct{}),
	}
	s.runs[account.Name] = run
	state := run.state
	s.mu.Unlock()

	go func() {
		defer close(run.done)
		defer cancel()
		err := auth.SignIn(ctx, account)
		s.mu.Lock()
		defer s.mu.Unlock()
		run.state.FinishedAt = time.Now().UTC()
		switch {
		case err == nil:
			run.state.State = ClaudeSignInSucceeded
		case run.cancelled || errors.Is(err, context.Canceled):
			run.state.State = ClaudeSignInCancelled
		case errors.Is(err, core.ErrRuntimeSignInRunning):
			run.state.State = ClaudeSignInFailed
			run.state.Error = "A sign-in or sign-out of this account is already running."
		default:
			run.state.State = ClaudeSignInFailed
			run.state.Error = truncateRunes(err.Error(), claudeSignInErrorRunes)
		}
	}()
	return state, nil
}

// ClaudeSignInState returns the last sign-in of the account; idle when there
// was none.
func (rt *Runtime) ClaudeSignInState(name string) ClaudeSignIn {
	if name == "" {
		name = ClaudeCodeRuntime
	}
	s := &rt.claudeSignIns
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.runs[name]; run != nil {
		return run.state
	}
	return ClaudeSignIn{State: ClaudeSignInIdle}
}

// CancelClaudeSignIn cancels the running sign-in of the account and waits
// for it to stop. It returns the state afterwards.
func (rt *Runtime) CancelClaudeSignIn(name string) ClaudeSignIn {
	if name == "" {
		name = ClaudeCodeRuntime
	}
	s := &rt.claudeSignIns
	s.mu.Lock()
	run := s.runs[name]
	if run == nil || run.state.State != ClaudeSignInRunning {
		s.mu.Unlock()
		return rt.ClaudeSignInState(name)
	}
	run.cancelled = true
	run.cancel()
	s.mu.Unlock()
	select {
	case <-run.done:
	case <-time.After(claudeCancelWait):
	}
	return rt.ClaudeSignInState(name)
}

// ForgetClaudeSignIn cancels a running sign-in of the account and drops its
// state, for an account that is removed.
func (rt *Runtime) ForgetClaudeSignIn(name string) {
	rt.CancelClaudeSignIn(name)
	s := &rt.claudeSignIns
	s.mu.Lock()
	if run := s.runs[name]; run != nil && run.state.State != ClaudeSignInRunning {
		delete(s.runs, name)
	}
	s.mu.Unlock()
}

// ClaudeSignOut runs `claude auth logout` for the account (REQ-CCT-012). A
// running sign-in of the account is cancelled first, and the last sign-in
// state is dropped when the sign-out succeeded.
func (rt *Runtime) ClaudeSignOut(ctx context.Context, account core.RuntimeAccount) error {
	auth, err := rt.claudeAccountAuth()
	if err != nil {
		return err
	}
	account = rt.claudeAuthAccount(account)
	rt.CancelClaudeSignIn(account.Name)
	if err := auth.SignOut(ctx, account); err != nil {
		return err
	}
	s := &rt.claudeSignIns
	s.mu.Lock()
	if run := s.runs[account.Name]; run != nil && run.state.State != ClaudeSignInRunning {
		delete(s.runs, account.Name)
	}
	s.mu.Unlock()
	return nil
}

// cancelClaudeSignIns cancels every running sign-in (Close).
func (rt *Runtime) cancelClaudeSignIns() {
	s := &rt.claudeSignIns
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.runs {
		if run.state.State == ClaudeSignInRunning {
			run.cancelled = true
			run.cancel()
		}
	}
}
