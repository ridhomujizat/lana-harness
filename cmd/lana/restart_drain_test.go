package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/web"
)

// TestRestartAdapterDrain covers the backend side of AC-TFB-005/006: the
// restart waits with the original deadline, cancel lifts the gate with the turn
// still running, and force interrupts it (API-TFB-002).
func TestRestartAdapterDrain(t *testing.T) {
	delay := 5000
	rt, home := webRuntime(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{DelayMS: &delay}}},
	}})
	leases := newLeaseSet(rt)
	t.Cleanup(leases.releaseAll)
	lock, err := acquireDaemonLock(t.TempDir())
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	t.Cleanup(lock.release)

	backend := newWebBackend(rt, home, leases, context.Background())
	backend.SetRestartController(newRestartController(lock))
	drain := newDrainController(rt, time.Minute)
	backend.SetDrainController(drain)

	ctx := context.Background()
	info, err := rt.CreateChannelSession(ctx, "web", "owner", "assistant")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan lana.Result, 1)
	go func() {
		result, _ := rt.Run(ctx, lana.Request{Prompt: "slow", SessionID: info.ID})
		done <- result
	}()
	waitForRunningTurns(t, rt, 1)

	outcome, err := backend.Restart(ctx, false)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if outcome.State != web.DaemonRestartDraining || outcome.RunningTurns != 1 || outcome.Deadline == 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if !rt.Draining() {
		t.Fatal("gate is not set during the drain")
	}
	again, err := backend.Restart(ctx, false)
	if err != nil {
		t.Fatalf("second Restart: %v", err)
	}
	if again.Deadline != outcome.Deadline {
		t.Fatalf("deadline reset: %d -> %d", outcome.Deadline, again.Deadline)
	}
	daemonInfo := backend.DaemonInfo()
	if daemonInfo.Restart.State != web.DaemonRestartDraining || daemonInfo.Restart.Deadline == nil || *daemonInfo.Restart.Deadline != outcome.Deadline {
		t.Fatalf("daemon info restart = %+v", daemonInfo.Restart)
	}

	cancelled, err := backend.CancelRestart(ctx)
	if err != nil || !cancelled {
		t.Fatalf("CancelRestart = %v, %v", cancelled, err)
	}
	if rt.Draining() {
		t.Fatal("gate is still set after cancel")
	}
	if rt.RunningTurnCount() != 1 {
		t.Fatalf("running turns after cancel = %d, want 1", rt.RunningTurnCount())
	}
	if cancelled, _ := backend.CancelRestart(ctx); cancelled {
		t.Fatal("second CancelRestart = true")
	}
	if info := backend.DaemonInfo(); info.Restart.State != "" && info.Restart.State != web.DaemonRestartIdle {
		t.Fatalf("restart state after cancel = %q", info.Restart.State)
	}

	forced, err := backend.Restart(ctx, true)
	if err != nil {
		t.Fatalf("forced Restart: %v", err)
	}
	if forced.State != web.DaemonRestartScheduled {
		t.Fatalf("forced outcome = %+v", forced)
	}
	select {
	case result := <-done:
		if result.StopReason != "interrupted" {
			t.Fatalf("stop reason = %q, want interrupted", result.StopReason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("forced turn did not finish")
	}
}

// TestRestartAdapterStopDrainInvalid covers the documented choice: a stop drain
// is a 409 invalid_state for the web restart.
func TestRestartAdapterStopDrainInvalid(t *testing.T) {
	rt, home := webRuntime(t, mock.Script{})
	leases := newLeaseSet(rt)
	t.Cleanup(leases.releaseAll)
	lock, err := acquireDaemonLock(t.TempDir())
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	t.Cleanup(lock.release)

	backend := newWebBackend(rt, home, leases, context.Background())
	backend.SetRestartController(newRestartController(lock))
	drain := newDrainController(rt, time.Minute)
	backend.SetDrainController(drain)
	drain.begin(drainStop)
	t.Cleanup(drain.force)

	if _, err := backend.Restart(context.Background(), false); !errors.Is(err, web.ErrRestartInvalidState) {
		t.Fatalf("Restart during a stop drain = %v, want ErrRestartInvalidState", err)
	}
}

func waitForRunningTurns(t *testing.T, rt interface{ RunningTurnCount() int }, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for rt.RunningTurnCount() != want {
		if !time.Now().Before(deadline) {
			t.Fatalf("running turns = %d, want %d", rt.RunningTurnCount(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
