package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// restartLockFDEnv carries the daemon lock descriptor across exec
	// (design.md, Restart in place).
	restartLockFDEnv = "LANA_RESTART_LOCK_FD"
	// restartResponseDelay lets the 202 response reach the browser before the
	// daemon stops accepting requests.
	restartResponseDelay = 300 * time.Millisecond
)

// errRestartUnsupported is returned when the platform cannot replace the
// process image. Windows hides the restart control (Q-019).
var errRestartUnsupported = errors.New("restart in place is not supported on this platform")

// restartController prepares an in-place restart and asks runDaemon to replace
// the process image after the HTTP response has been written.
type restartController struct {
	lock *daemonLock

	once     sync.Once
	requests chan struct{}
}

func newRestartController(lock *daemonLock) *restartController {
	return &restartController{lock: lock, requests: make(chan struct{})}
}

// supported reports whether the platform can restart in place.
func (c *restartController) supported() bool {
	return runtime.GOOS != "windows"
}

// preflight checks that the daemon can replace its process image: the platform
// supports exec, the lock descriptor is open, and the executable resolves. A
// failure leaves the daemon running (API-WEB-023).
func (c *restartController) preflight() error {
	if c == nil || !c.supported() {
		return errRestartUnsupported
	}
	if c.lock == nil {
		return errors.New("daemon lock is not held")
	}
	if err := c.lock.check(); err != nil {
		return fmt.Errorf("daemon lock descriptor: %w", err)
	}
	if _, err := os.Executable(); err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	return nil
}

// schedule runs the preflight and closes the restart request after a short
// delay, so the handler can write 202 first. A second call is a no-op.
func (c *restartController) schedule() error {
	if err := c.preflight(); err != nil {
		return err
	}
	c.once.Do(func() {
		go func() {
			time.Sleep(restartResponseDelay)
			close(c.requests)
		}()
	})
	return nil
}

// requested is closed when runDaemon should replace the process image.
func (c *restartController) requested() <-chan struct{} {
	if c == nil {
		return nil
	}
	return c.requests
}

// restartInPlace runs the same binary with the same arguments in the same
// process. The lock descriptor is cleared of close-on-exec first, so its flock
// stays held across the exec (Q-019).
func restartInPlace(lock *daemonLock, stderr io.Writer) int {
	if lock == nil {
		_, _ = fmt.Fprintln(stderr, "error: restart: daemon lock is not held")
		return 1
	}
	if err := lock.setCloseOnExec(false); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: restart: %v\n", err)
		return 1
	}
	executable, err := os.Executable()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: restart: %v\n", err)
		return 1
	}
	if err := syscall.Exec(executable, os.Args, restartEnv(os.Environ(), lock.fd())); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: restart: %v\n", err)
		return 1
	}
	return 0
}

// restartEnv replaces LANA_RESTART_LOCK_FD in env so a stale value cannot leak.
func restartEnv(env []string, fd int) []string {
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if strings.HasPrefix(item, restartLockFDEnv+"=") {
			continue
		}
		out = append(out, item)
	}
	return append(out, restartLockFDEnv+"="+strconv.Itoa(fd))
}
