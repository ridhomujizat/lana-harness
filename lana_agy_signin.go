package lana

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
)

// Sign-in of named Antigravity accounts from the dashboard (REQ-AGY-010,
// API-AGY-021..023). The Antigravity runtime runs the official
// `agy acp --login` with the account's HOME; lana keeps only the state of the
// last sign-in per account in memory: the Google link while agy waits for
// the code, and a short, URL-stripped error line. The pasted code is written
// once to agy's stdin and never stored or logged.

// Sign-in states of AntigravitySignIn.State (API-AGY-020).
const (
	AntigravitySignInIdle        = "idle"
	AntigravitySignInStarting    = "starting"
	AntigravitySignInWaitingCode = "waiting_code"
	AntigravitySignInRunning     = "running"
	AntigravitySignInSucceeded   = "succeeded"
	AntigravitySignInFailed      = "failed"
	AntigravitySignInCancelled   = "cancelled"
)

var (
	// ErrAntigravitySignInRunning is returned by a sign-in started while one
	// of the same account runs (API-AGY-022, 409 sign_in_running).
	ErrAntigravitySignInRunning = core.ErrRuntimeSignInRunning
	// ErrAntigravityBuiltin is returned for the built-in account, which signs
	// in with agy in a terminal (API-AGY-022, 409 builtin).
	ErrAntigravityBuiltin = errors.New("the built-in Antigravity account signs in with agy in a terminal")
	// ErrAntigravityNotWaiting is returned by a code sent while the account's
	// sign-in does not wait for one (API-AGY-023, 409 not_waiting).
	ErrAntigravityNotWaiting = errors.New("the Antigravity sign-in is not waiting for a code")
	// ErrAntigravityBadCode is returned for a code that is empty, longer
	// than 2048 characters, or not one line (API-AGY-023, 422 bad_code).
	ErrAntigravityBadCode = errors.New("the code must be 1 to 2048 characters on one line")
)

const (
	// antigravitySignInTimeout bounds one sign-in (REQ-AGY-010).
	antigravitySignInTimeout = 15 * time.Minute
	// antigravityCodeMaxRunes is the longest accepted code (API-AGY-023).
	antigravityCodeMaxRunes = 2048
	// antigravitySignInErrorRunes caps the reported error.
	antigravitySignInErrorRunes = 240
	// antigravityCancelWait bounds how long a cancel waits for agy to stop.
	antigravityCancelWait = 15 * time.Second
)

// signInURLRe matches a URL in an error line; each becomes "sign-in link".
var signInURLRe = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S+`)

// AntigravitySignIn is the state of the last sign-in of one account. URL is
// set only while State is waiting_code; Error only for failed, and it never
// holds a URL or a code.
type AntigravitySignIn struct {
	State      string
	URL        string
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

// agySignIns holds the last sign-in per account name.
type agySignIns struct {
	mu   sync.Mutex
	runs map[string]*agySignInRun
}

type agySignInRun struct {
	state     AntigravitySignIn
	cancel    context.CancelFunc
	cancelled bool
	code      chan string
	done      chan struct{}
}

// active reports whether the sign-in still runs.
func (r *agySignInRun) active() bool {
	switch r.state.State {
	case AntigravitySignInStarting, AntigravitySignInWaitingCode, AntigravitySignInRunning:
		return true
	}
	return false
}

// agySignInPort is the sign-in of the Antigravity runtime (runtime/antigravity
// Runtime.SignIn).
type agySignInPort interface {
	SignIn(ctx context.Context, account core.RuntimeAccount, onLink func(url string), code <-chan string) error
}

// antigravitySignInPort returns the Antigravity runtime's sign-in.
func (rt *Runtime) antigravitySignInPort() (agySignInPort, error) {
	if rt.registry == nil {
		return nil, fmt.Errorf("agent runtime %q is not available", AntigravityRuntime)
	}
	ext, err := rt.registry.Runtime(AntigravityRuntime)
	if err != nil {
		return nil, err
	}
	port, ok := ext.(agySignInPort)
	if !ok {
		return nil, errors.New("the Antigravity runtime cannot sign accounts in")
	}
	return port, nil
}

// StartAntigravitySignIn starts `agy acp --login` for a named account in the
// background and returns the starting state (REQ-AGY-010). The caller passes
// the account's home and binary as config.toml has them, so an account just
// written works before the config is applied; an empty home is filled from
// the running config, else $LANA_HOME/antigravity/<name>. It returns
// ErrAntigravityBuiltin for the built-in account and
// ErrAntigravitySignInRunning while a sign-in of the account runs.
func (rt *Runtime) StartAntigravitySignIn(account core.RuntimeAccount) (AntigravitySignIn, error) {
	if rt.closed.Load() {
		return AntigravitySignIn{}, errors.New("lana is shutting down")
	}
	if account.Name == "" || account.Name == AntigravityRuntime {
		return AntigravitySignIn{}, ErrAntigravityBuiltin
	}
	if account.Home == "" {
		spec := rt.antigravityAccountSpec(account.Name, rt.antigravityAccountConfig(account.Name))
		account.Home = spec.Home
		if account.Binary == "" {
			account.Binary = spec.Binary
		}
	}
	port, err := rt.antigravitySignInPort()
	if err != nil {
		return AntigravitySignIn{}, err
	}
	s := &rt.agySignIns
	s.mu.Lock()
	if run := s.runs[account.Name]; run != nil && run.active() {
		state := run.state
		s.mu.Unlock()
		return state, ErrAntigravitySignInRunning
	}
	if s.runs == nil {
		s.runs = map[string]*agySignInRun{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), antigravitySignInTimeout)
	run := &agySignInRun{
		state:  AntigravitySignIn{State: AntigravitySignInStarting, StartedAt: time.Now().UTC()},
		cancel: cancel, code: make(chan string, 1), done: make(chan struct{}),
	}
	s.runs[account.Name] = run
	state := run.state
	s.mu.Unlock()

	onLink := func(url string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if run.state.State == AntigravitySignInStarting {
			run.state.State = AntigravitySignInWaitingCode
			run.state.URL = url
		}
	}
	go func() {
		defer close(run.done)
		defer cancel()
		err := port.SignIn(ctx, account, onLink, run.code)
		deadline := errors.Is(ctx.Err(), context.DeadlineExceeded)
		s.mu.Lock()
		defer s.mu.Unlock()
		run.state.FinishedAt = time.Now().UTC()
		run.state.URL = ""
		switch {
		case err == nil:
			run.state.State = AntigravitySignInSucceeded
		case run.cancelled:
			run.state.State = AntigravitySignInCancelled
		case deadline:
			run.state.State = AntigravitySignInFailed
			run.state.Error = "Antigravity sign-in timed out. Please try again."
		case errors.Is(err, context.Canceled):
			run.state.State = AntigravitySignInCancelled
		default:
			run.state.State = AntigravitySignInFailed
			run.state.Error = antigravitySignInError(err.Error())
		}
	}()
	return state, nil
}

// antigravitySignInError redacts a sign-in error: URLs become "sign-in
// link", control characters spaces, and it keeps at most 240 runes.
func antigravitySignInError(text string) string {
	text = signInURLRe.ReplaceAllString(text, "sign-in link")
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		text = "Antigravity sign-in failed"
	}
	return truncateRunes(text, antigravitySignInErrorRunes)
}

// AntigravitySignInState returns the last sign-in of the account; idle when
// there was none.
func (rt *Runtime) AntigravitySignInState(name string) AntigravitySignIn {
	s := &rt.agySignIns
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.runs[name]; run != nil {
		return run.state
	}
	return AntigravitySignIn{State: AntigravitySignInIdle}
}

// ValidAntigravityCode trims a pasted code and checks it (API-AGY-023): 1 to
// 2048 characters on one line with no control characters.
func ValidAntigravityCode(code string) (string, bool) {
	code = strings.TrimSpace(code)
	n := utf8.RuneCountInString(code)
	if n == 0 || n > antigravityCodeMaxRunes || !utf8.ValidString(code) {
		return "", false
	}
	if strings.ContainsFunc(code, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", false
	}
	return code, true
}

// SubmitAntigravityCode hands the code Google showed to the account's running
// sign-in (API-AGY-023): it is written once to agy's stdin and dropped. It
// returns ErrAntigravityBadCode for a code that fails ValidAntigravityCode
// and ErrAntigravityNotWaiting unless the sign-in waits for a code; the
// answer state is running.
func (rt *Runtime) SubmitAntigravityCode(name, code string) (AntigravitySignIn, error) {
	code, ok := ValidAntigravityCode(code)
	if !ok {
		return rt.AntigravitySignInState(name), ErrAntigravityBadCode
	}
	s := &rt.agySignIns
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[name]
	if run == nil || run.state.State != AntigravitySignInWaitingCode {
		if run == nil {
			return AntigravitySignIn{State: AntigravitySignInIdle}, ErrAntigravityNotWaiting
		}
		return run.state, ErrAntigravityNotWaiting
	}
	select {
	case run.code <- code:
	default:
		return run.state, ErrAntigravityNotWaiting
	}
	run.state.State = AntigravitySignInRunning
	run.state.URL = ""
	return run.state, nil
}

// CancelAntigravitySignIn cancels the running sign-in of the account (its
// process group is stopped) and waits for it to end. It returns the state
// afterwards.
func (rt *Runtime) CancelAntigravitySignIn(name string) AntigravitySignIn {
	s := &rt.agySignIns
	s.mu.Lock()
	run := s.runs[name]
	if run == nil || !run.active() {
		s.mu.Unlock()
		return rt.AntigravitySignInState(name)
	}
	run.cancelled = true
	run.cancel()
	s.mu.Unlock()
	select {
	case <-run.done:
	case <-time.After(antigravityCancelWait):
	}
	return rt.AntigravitySignInState(name)
}

// ForgetAntigravitySignIn cancels a running sign-in of the account and drops
// its state, for an account that is removed.
func (rt *Runtime) ForgetAntigravitySignIn(name string) {
	rt.CancelAntigravitySignIn(name)
	s := &rt.agySignIns
	s.mu.Lock()
	if run := s.runs[name]; run != nil && !run.active() {
		delete(s.runs, name)
	}
	s.mu.Unlock()
}

// cancelAntigravitySignIns cancels every running sign-in (Close).
func (rt *Runtime) cancelAntigravitySignIns() {
	s := &rt.agySignIns
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.runs {
		if run.active() {
			run.cancelled = true
			run.cancel()
		}
	}
}
