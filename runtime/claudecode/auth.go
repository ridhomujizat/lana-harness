package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Sign-in and sign-out of a Claude Code account (REQ-CCT-012, ADR 0020
// amendment 2026-11-08). lana starts the official `claude auth login` or
// `claude auth logout` with the account's environment, stdin closed and
// stdout discarded. The binary opens the browser and writes the credentials
// itself. lana keeps only the last non-empty stderr line, with every URL
// replaced by "sign-in link", and only to report a failure: URLs, OAuth codes,
// and tokens never reach a log, an API answer, or storage.

// Defaults of the sign-in and sign-out deadlines.
const (
	DefaultSignInTimeout  = 10 * time.Minute
	DefaultSignOutTimeout = 30 * time.Second
)

const (
	// signInTailBytes caps the stderr kept while the command runs; the pipe
	// is drained to the end either way.
	signInTailBytes = 64 << 10
	// signInDetailRunes caps the reported error line.
	signInDetailRunes = 240
	// authWaitDelay bounds the wait for the stderr pipe after the command
	// exited: a browser the command started may keep it open.
	authWaitDelay = 2 * time.Second
)

var (
	signInURLRe  = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S+`)
	signInANSIRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
)

// signInDetail returns the reportable part of the command's stderr: its last
// non-empty line with every URL replaced by "sign-in link", ANSI escapes and
// control characters removed, whitespace collapsed, secrets redacted, and at
// most 240 characters.
func signInDetail(stderr string) string {
	last := ""
	lines := strings.Split(strings.ReplaceAll(stderr, "\r", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := signInANSIRe.ReplaceAllString(lines[i], ""); strings.TrimSpace(line) != "" {
			last = line
			break
		}
	}
	last = signInURLRe.ReplaceAllString(last, "sign-in link")
	last = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, last)
	last = strings.Join(strings.Fields(last), " ")
	return truncate(redact(last, ""), signInDetailRunes)
}

// tailWriter keeps the last max bytes written to it.
type tailWriter struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailWriter) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// SignIn runs `claude auth login` for the account and waits for it, at most
// SignInTimeout (REQ-CCT-012). It succeeds when the command exits 0. One
// sign-in or sign-out runs per account at a time; another returns
// core.ErrRuntimeSignInRunning. Cancelling ctx stops the command's process
// group and returns an error wrapping context.Canceled. The account's status
// cache is dropped afterwards, so the next status shows the new login.
func (r *Runtime) SignIn(ctx context.Context, account core.RuntimeAccount) error {
	return r.runAuth(ctx, account, "login", r.opts.SignInTimeout)
}

// SignOut runs `claude auth logout` for the account and waits for it, at most
// SignOutTimeout. The rules of SignIn apply.
func (r *Runtime) SignOut(ctx context.Context, account core.RuntimeAccount) error {
	return r.runAuth(ctx, account, "logout", r.opts.SignOutTimeout)
}

var _ core.RuntimeAccountAuth = (*Runtime)(nil)

func (r *Runtime) runAuth(ctx context.Context, account core.RuntimeAccount, action string, timeout time.Duration) error {
	name := account.Name
	if name == "" {
		name = core.ClaudeCodeProvider
	}
	what := "sign-in"
	if action == "logout" {
		what = "sign-out"
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return userError("Claude Code runtime is shut down")
	}
	if _, busy := r.authRuns[name]; busy {
		r.mu.Unlock()
		return core.ErrRuntimeSignInRunning
	}
	r.authRuns[name] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.authRuns, name)
		r.mu.Unlock()
		r.invalidateAccountStatus(name)
	}()

	bin, err := r.accountBinary(account)
	if err != nil {
		return err
	}
	// Subscription auth: the token variables are removed, so the command
	// signs in to the account's own login.
	env, _, err := buildEnv(r.opts.Environ(), core.RuntimeConfig{ConfigDir: account.ConfigDir}, nil)
	if err != nil {
		return err
	}
	if account.ConfigDir != "" {
		if err := os.MkdirAll(account.ConfigDir, 0o700); err != nil {
			return userError("cannot create the account folder: %v", err)
		}
	}
	cmd := exec.Command(bin, "auth", action)
	cmd.Env = env
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	stderr := &tailWriter{max: signInTailBytes}
	cmd.Stderr = stderr
	cmd.WaitDelay = authWaitDelay
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return userError("Could not start Claude Code %s: %v", what, err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		if err := waitErr; err == nil || errors.Is(err, exec.ErrWaitDelay) {
			return nil
		}
		if detail := signInDetail(stderr.String()); detail != "" {
			return &TurnError{Message: detail}
		}
		return userError("Claude Code %s exited unexpectedly", what)
	case <-runCtx.Done():
		r.stopAuth(cmd, done)
		return fmt.Errorf("claude code %s cancelled: %w", what, context.Canceled)
	case <-timer.C:
		r.stopAuth(cmd, done)
		return userError("Claude Code %s timed out. Please try again.", what)
	}
}

// stopAuth ends an auth command's process group: SIGTERM, then SIGKILL after
// KillGrace, and waits for the command.
func (r *Runtime) stopAuth(cmd *exec.Cmd, done <-chan struct{}) {
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-done:
		return
	case <-time.After(r.opts.KillGrace):
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-done:
	case <-time.After(r.opts.KillGrace + authWaitDelay):
	}
}

// cancelAuthRuns cancels every running sign-in and sign-out (Shutdown).
func (r *Runtime) cancelAuthRuns() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cancel := range r.authRuns {
		cancel()
	}
}

// accountBinary returns the account's claude: its binary key (a leading ~/
// expanded), else claude on PATH.
func (r *Runtime) accountBinary(account core.RuntimeAccount) (string, error) {
	bin := account.Binary
	if bin == "" {
		path, err := r.opts.LookPath("claude")
		if err != nil {
			return "", userError(errNotInstalled)
		}
		return path, nil
	}
	if strings.HasPrefix(bin, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			bin = home + bin[1:]
		}
	}
	return bin, nil
}

// invalidateAccountStatus drops the cached status of the account, so the
// next AccountStatus (and, for the built-in account, Status) probes again.
func (r *Runtime) invalidateAccountStatus(name string) {
	r.accountStatusMu.Lock()
	for key := range r.accountStatus {
		if strings.HasPrefix(key, name+"\x00") {
			delete(r.accountStatus, key)
		}
	}
	r.accountStatusMu.Unlock()
	if name == core.ClaudeCodeProvider {
		r.statusMu.Lock()
		r.statusAt = time.Time{}
		r.statusMu.Unlock()
	}
}
