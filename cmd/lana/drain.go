package main

import (
	"context"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// daemonDrainTimeout is how long a drain waits for running turns before it
// interrupts them (REQ-TFB-002). Tests shorten it.
var daemonDrainTimeout = 60 * time.Second

// drainPollInterval is how often the controller checks the running count.
const drainPollInterval = 50 * time.Millisecond

// drainState is the phase of a shutdown (REQ-TFB-002).
type drainState int

const (
	drainIdle drainState = iota
	drainDraining
	drainStopping
)

// drainReason is why the daemon is draining.
type drainReason int

const (
	drainStop drainReason = iota
	drainRestart
)

// cause maps the reason to the cancel cause agent/turn.go records
// (REQ-TFB-001, API-TFB-001).
func (r drainReason) cause() error {
	if r == drainRestart {
		return core.ErrInterruptedRestart
	}
	return core.ErrInterruptedStop
}

// drainRuntime is the part of *lana.Runtime the drain controller uses.
type drainRuntime interface {
	SetDraining(bool)
	Draining() bool
	RunningTurnCount() int
	InterruptAll(cause error) int
}

// drainStatus is a snapshot of the controller.
type drainStatus struct {
	State    drainState
	Reason   drainReason
	Deadline time.Time
	Running  int
}

// drainController stops new turns, waits for running turns, and interrupts the
// rest at the deadline (REQ-TFB-002). The states are idle -> draining ->
// stopping; a restart drain may return to idle with cancel.
type drainController struct {
	runtime drainRuntime
	timeout time.Duration
	poll    time.Duration
	now     func() time.Time

	mu         sync.Mutex
	state      drainState
	reason     drainReason
	deadline   time.Time
	waitCancel context.CancelFunc
	stopping   chan struct{}
}

// newDrainController creates a controller with the given wait limit.
func newDrainController(runtime drainRuntime, timeout time.Duration) *drainController {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &drainController{
		runtime:  runtime,
		timeout:  timeout,
		poll:     drainPollInterval,
		now:      time.Now,
		stopping: make(chan struct{}),
	}
}

// begin starts a drain and returns a snapshot. An existing drain is never reset,
// so a repeated web restart keeps the original deadline. New turns are refused
// from the moment the gate is set.
func (c *drainController) begin(reason drainReason) drainStatus {
	c.mu.Lock()
	if c.state != drainIdle {
		status := c.statusLocked()
		c.mu.Unlock()
		return status
	}
	c.state = drainDraining
	c.reason = reason
	c.deadline = c.now().Add(c.timeout)
	deadline := c.deadline
	waitCtx, cancel := context.WithCancel(context.Background())
	c.waitCancel = cancel
	c.runtime.SetDraining(true)
	status := c.statusLocked()
	c.mu.Unlock()
	go c.wait(waitCtx, deadline)
	return status
}

// wait completes the drain when the running count reaches 0 or the deadline
// passes. At the deadline the remaining turns are interrupted with the cause.
func (c *drainController) wait(ctx context.Context, deadline time.Time) {
	ticker := time.NewTicker(c.poll)
	defer ticker.Stop()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		if c.runtime.RunningTurnCount() == 0 {
			c.stop(false)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-timer.C:
			c.stop(true)
			return
		}
	}
}

// stop moves the controller to stopping. interrupt cancels the remaining turns
// with the cause of the drain reason.
func (c *drainController) stop(interrupt bool) {
	c.mu.Lock()
	if c.state != drainDraining {
		c.mu.Unlock()
		return
	}
	cause := c.reason.cause()
	if interrupt {
		c.runtime.InterruptAll(cause)
	}
	c.state = drainStopping
	if c.waitCancel != nil {
		c.waitCancel()
		c.waitCancel = nil
	}
	close(c.stopping)
	c.mu.Unlock()
}

// force skips the wait and interrupts the running turns now.
func (c *drainController) force() {
	c.stop(true)
}

// cancel aborts a pending restart drain and lets sessions accept turns again.
// It only applies while draining a restart, before stopping (API-TFB-002).
func (c *drainController) cancel() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != drainDraining || c.reason != drainRestart {
		return false
	}
	c.state = drainIdle
	c.reason = drainStop
	c.deadline = time.Time{}
	if c.waitCancel != nil {
		c.waitCancel()
		c.waitCancel = nil
	}
	c.runtime.SetDraining(false)
	return true
}

// status returns a snapshot for GET /v1/daemon and the restart handler.
func (c *drainController) status() drainStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *drainController) statusLocked() drainStatus {
	return drainStatus{
		State:    c.state,
		Reason:   c.reason,
		Deadline: c.deadline,
		Running:  c.runtime.RunningTurnCount(),
	}
}

// stoppingSignaled is closed when the controller reaches stopping.
func (c *drainController) stoppingSignaled() <-chan struct{} {
	return c.stopping
}
