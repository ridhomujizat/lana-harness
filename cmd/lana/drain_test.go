package main

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// fakeDrainRuntime records the calls of the drain controller.
type fakeDrainRuntime struct {
	running  atomic.Int32
	draining atomic.Bool
	mu       sync.Mutex
	causes   []error
}

func (f *fakeDrainRuntime) SetDraining(value bool) { f.draining.Store(value) }
func (f *fakeDrainRuntime) Draining() bool         { return f.draining.Load() }
func (f *fakeDrainRuntime) RunningTurnCount() int  { return int(f.running.Load()) }
func (f *fakeDrainRuntime) InterruptAll(cause error) int {
	f.mu.Lock()
	f.causes = append(f.causes, cause)
	f.mu.Unlock()
	return int(f.running.Load())
}

func (f *fakeDrainRuntime) interrupted() []error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]error(nil), f.causes...)
}

func waitStopping(t *testing.T, c *drainController, timeout time.Duration) {
	t.Helper()
	select {
	case <-c.stoppingSignaled():
	case <-time.After(timeout):
		t.Fatal("drain did not reach stopping")
	}
}

// TestDrainWaitsForRunningTurn covers AC-TFB-002: a turn that finishes before
// the deadline is not interrupted.
func TestDrainWaitsForRunningTurn(t *testing.T) {
	rt := &fakeDrainRuntime{}
	rt.running.Store(1)
	drain := newDrainController(rt, 500*time.Millisecond)

	status := drain.begin(drainStop)
	if status.State != drainDraining || !rt.Draining() {
		t.Fatalf("status = %+v, draining = %v", status, rt.Draining())
	}
	if remaining := time.Until(status.Deadline); remaining <= 0 || remaining > 500*time.Millisecond {
		t.Fatalf("deadline = %v, want about 500ms from now", remaining)
	}

	time.AfterFunc(30*time.Millisecond, func() { rt.running.Store(0) })
	waitStopping(t, drain, 2*time.Second)
	if got := rt.interrupted(); len(got) != 0 {
		t.Fatalf("interrupts = %v, want none", got)
	}
	if status := drain.status(); status.State != drainStopping {
		t.Fatalf("state = %v, want stopping", status.State)
	}
}

// TestDrainDeadlineInterrupts covers AC-TFB-003: a turn that outlives the
// deadline ends interrupted with the stop cause.
func TestDrainDeadlineInterrupts(t *testing.T) {
	rt := &fakeDrainRuntime{}
	rt.running.Store(1)
	drain := newDrainController(rt, 80*time.Millisecond)

	started := time.Now()
	drain.begin(drainStop)
	waitStopping(t, drain, 2*time.Second)
	if elapsed := time.Since(started); elapsed < 60*time.Millisecond {
		t.Fatalf("drain completed after %v, want at least the deadline", elapsed)
	}
	got := rt.interrupted()
	if len(got) != 1 || !errors.Is(got[0], core.ErrInterruptedStop) {
		t.Fatalf("interrupts = %v, want the stop cause", got)
	}
}

// TestDrainCancelRestart covers API-TFB-002: cancelling a pending restart
// drain lifts the gate and leaves the running turn alone.
func TestDrainCancelRestart(t *testing.T) {
	rt := &fakeDrainRuntime{}
	rt.running.Store(1)
	drain := newDrainController(rt, time.Minute)

	status := drain.begin(drainRestart)
	if status.State != drainDraining || status.Reason != drainRestart {
		t.Fatalf("status = %+v", status)
	}
	if !drain.cancel() {
		t.Fatal("cancel of a pending restart drain = false")
	}
	if drain.cancel() {
		t.Fatal("second cancel = true, want false")
	}
	if rt.Draining() {
		t.Fatal("gate is still set after cancel")
	}
	if status := drain.status(); status.State != drainIdle {
		t.Fatalf("state = %v, want idle", status.State)
	}
	if got := rt.interrupted(); len(got) != 0 {
		t.Fatalf("interrupts = %v, want none", got)
	}
}

// TestDrainCancelStopRejected covers the documented choice that only a restart
// drain can be cancelled.
func TestDrainCancelStopRejected(t *testing.T) {
	rt := &fakeDrainRuntime{}
	rt.running.Store(1)
	drain := newDrainController(rt, time.Minute)
	drain.begin(drainStop)
	if drain.cancel() {
		t.Fatal("cancel of a stop drain = true")
	}
	drain.force()
	waitStopping(t, drain, time.Second)
}

// TestDrainForce covers "Restart now": force interrupts at once with the cause
// of the drain reason.
func TestDrainForce(t *testing.T) {
	for _, test := range []struct {
		reason drainReason
		cause  error
	}{
		{reason: drainStop, cause: core.ErrInterruptedStop},
		{reason: drainRestart, cause: core.ErrInterruptedRestart},
	} {
		rt := &fakeDrainRuntime{}
		rt.running.Store(1)
		drain := newDrainController(rt, time.Minute)
		drain.begin(test.reason)
		drain.force()
		waitStopping(t, drain, time.Second)
		got := rt.interrupted()
		if len(got) != 1 || !errors.Is(got[0], test.cause) {
			t.Fatalf("reason %v interrupts = %v, want %v", test.reason, got, test.cause)
		}
	}
}

// TestDrainBeginKeepsDeadline covers the repeated restart POST: the original
// deadline is returned and the drain is not restarted.
func TestDrainBeginKeepsDeadline(t *testing.T) {
	rt := &fakeDrainRuntime{}
	rt.running.Store(1)
	drain := newDrainController(rt, time.Minute)

	first := drain.begin(drainRestart)
	second := drain.begin(drainRestart)
	if !first.Deadline.Equal(second.Deadline) {
		t.Fatalf("deadlines = %v and %v, want equal", first.Deadline, second.Deadline)
	}
	drain.cancel()
}
