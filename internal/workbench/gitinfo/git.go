// Package gitinfo answers git questions for the Workbench (spec 66,
// REQ-WG-001, REQ-WG-002, REQ-WG-030 and REQ-WG-061). It runs git without a
// shell, with a fixed environment and a 10 s timeout, and it checks every path
// against the spec 61 path policy (internal/workbench/preview) before it
// touches the file system. It knows nothing of the web server or the Workbench
// core, so its rules are tested on their own against temporary repositories.
package gitinfo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench/preview"
)

// defaultTimeout is how long one git command may run (REQ-WG-001).
const defaultTimeout = 10 * time.Second

// ErrForbiddenPath is returned for a path the spec 61 policy does not allow or
// always refuses (REQ-WG-001).
var ErrForbiddenPath = errors.New("gitinfo: forbidden path")

// ErrNotARepo is returned for a directory outside any git worktree
// (REQ-WG-002).
var ErrNotARepo = errors.New("gitinfo: not a git repository")

// GitError is a git run that failed (REQ-WG-001, api-contract). Code is
// "git_failed" for a non-zero exit, "git_timeout" for a run the timeout killed;
// Message is the first non-empty stderr line, so the caller can put it in the
// daemon envelope unchanged.
type GitError struct {
	Code     string
	Message  string
	ExitCode int
}

// Error renders the failure as code and message.
func (e *GitError) Error() string {
	if e.Message == "" {
		return "gitinfo: " + e.Code
	}
	return "gitinfo: " + e.Code + ": " + e.Message
}

// Runner runs git for the Workbench (REQ-WG-001). It holds the path policy
// every directory is checked against, the git timeout, and the 5 s resolve
// cache of REQ-WG-002.
type Runner struct {
	policy  preview.Policy
	timeout time.Duration

	// now is the time source of the resolve cache. It is a test hook; a nil
	// now means time.Now.
	now   func() time.Time
	mu    sync.Mutex
	cache map[string]cacheEntry
}

// NewRunner builds a Runner with the given path policy and the default 10 s
// git timeout (REQ-WG-001).
func NewRunner(policy preview.Policy) *Runner {
	return &Runner{policy: policy, timeout: defaultTimeout}
}

// Git runs "git -C dir args..." and returns its standard output (REQ-WG-001).
// The command never goes through a shell, runs with the runner's timeout (10 s
// when unset) and with GIT_OPTIONAL_LOCKS=0, GIT_TERMINAL_PROMPT=0 and
// LC_ALL=C added to the environment. A non-zero exit is a *GitError with code
// "git_failed" and the first stderr line; a run the timeout killed is a
// *GitError with code "git_timeout".
func (r *Runner) Git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	timeout := r.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return nil, &GitError{Code: "git_timeout", Message: firstLine(stderr.String())}
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, &GitError{Code: "git_failed", Message: firstLine(stderr.String()), ExitCode: exitErr.ExitCode()}
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// CheckPath reports whether path may be read (REQ-WG-001): the spec 61 policy
// must allow it and must not refuse it. The check runs on the symlink-resolved
// path, like preview, so a link pointing out of an allowed folder is refused.
func (r *Runner) CheckPath(path string) error {
	resolved := preview.Resolve(path)
	if !r.policy.Allowed(resolved) || r.policy.Refused(resolved) {
		return ErrForbiddenPath
	}
	return nil
}

// clock is the time source of the resolve cache: the injected test clock when
// set, else time.Now.
func (r *Runner) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// gitEnv is the environment every git run gets (REQ-WG-001): the daemon's own
// environment plus the three variables that keep git quiet, non-interactive and
// lock-free. os/exec keeps the last value of a repeated key, so these win.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	)
}

// firstLine is the first non-empty line of s, with surrounding whitespace
// dropped, or "" when there is none.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
