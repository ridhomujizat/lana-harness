package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/selfupdate"
	"golang.org/x/sys/unix"
)

// fakeDaemonPID is the daemon pid the decision-loop tests watch. Nothing runs
// with it; the alive fake decides.
const fakeDaemonPID = 4242

// watchHarness is an updateWatcher with a fake clock, fake process control,
// and a recorded start.
type watchHarness struct {
	w       *updateWatcher
	stderr  *bytes.Buffer
	clock   time.Time
	steps   int
	onStep  func(step int)
	alive   bool
	signals []syscall.Signal
	// onSignal runs after a signal is recorded.
	onSignal func(sig syscall.Signal)
	lockFree bool
	starts   []string
}

func newWatchHarness(t *testing.T, home string) *watchHarness {
	t.Helper()
	h := &watchHarness{stderr: &bytes.Buffer{}, clock: time.Unix(1762600000, 0), alive: true, lockFree: true}
	h.w = &updateWatcher{
		paths:      selfupdate.Paths{Home: home},
		pid:        fakeDaemonPID,
		self:       9001,
		daemonArgs: []string{"daemon"},
		stderr:     h.stderr,
		alive: func(pid int) bool {
			if pid != fakeDaemonPID {
				t.Fatalf("alive(%d)", pid)
			}
			return h.alive
		},
		signal: func(pid int, sig syscall.Signal) error {
			h.signals = append(h.signals, sig)
			if h.onSignal != nil {
				h.onSignal(sig)
			}
			return nil
		},
		now: func() time.Time { return h.clock },
		sleep: func(d time.Duration) {
			h.clock = h.clock.Add(d)
			h.steps++
			if h.onStep != nil {
				h.onStep(h.steps)
			}
		},
		lockFree: func() bool { return h.lockFree },
		start: func(exe string, args []string) error {
			h.starts = append(h.starts, exe+" "+strings.Join(args, " "))
			return nil
		},
		timeout:   90 * time.Second,
		killGrace: 10 * time.Second,
		poll:      time.Second,
		lockWait:  5 * time.Second,
	}
	return h
}

// assertRolledBack checks the files after a watchdog rollback of trialHome.
func assertRolledBack(t *testing.T, home, exe, reason string) {
	t.Helper()
	if data, _ := os.ReadFile(exe); string(data) != "old-binary" {
		t.Fatalf("exe = %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(home, "config.toml")); string(data) != "[web]\nenabled = false\n" {
		t.Fatalf("config = %q", data)
	}
	paths := selfupdate.Paths{Home: home}
	if pending, _ := selfupdate.ReadPending(paths); pending != nil {
		t.Fatalf("pending left: %+v", pending)
	}
	if pids, _ := selfupdate.ClaimPIDs(paths); len(pids) != 0 {
		t.Fatalf("claims left: %v", pids)
	}
	state, _ := selfupdate.ReadState(paths)
	result := state.LastResult
	if result == nil || result.Outcome != selfupdate.OutcomeRolledBack || result.From != "0.1.0-beta.1" || result.To != "0.1.0" || !strings.Contains(result.Reason, reason) {
		t.Fatalf("last_result = %+v, want reason with %q", result, reason)
	}
	if state.Skipped != "0.1.0" {
		t.Fatalf("skipped = %q", state.Skipped)
	}
}

// TestUpdateWatchHealthy: the trial boot reports healthy; the watchdog exits
// without touching anything.
func TestUpdateWatchHealthy(t *testing.T) {
	home, exe, pending := trialHome(t, 0, "# new\n")
	h := newWatchHarness(t, home)
	h.onStep = func(step int) {
		if step == 3 {
			pending.Boots = 1
			(&trialBoot{paths: h.w.paths, pending: pending, now: time.Now}).markHealthy()
		}
	}
	if got := h.w.watch(); got != watchHealthy {
		t.Fatalf("result = %d, log = %s", got, h.stderr.String())
	}
	if data, _ := os.ReadFile(exe); string(data) != "new-binary" {
		t.Fatalf("exe = %q", data)
	}
	if len(h.signals) != 0 || len(h.starts) != 0 {
		t.Fatalf("signals = %v, starts = %v", h.signals, h.starts)
	}
}

// TestUpdateWatchHandledByTrialBoot: the new binary recorded rolled_back or
// failed itself; the watchdog does nothing.
func TestUpdateWatchHandledByTrialBoot(t *testing.T) {
	for _, outcome := range []string{selfupdate.OutcomeRolledBack, selfupdate.OutcomeFailed} {
		t.Run(outcome, func(t *testing.T) {
			home, exe, _ := trialHome(t, 0, "# new\n")
			h := newWatchHarness(t, home)
			h.onStep = func(step int) {
				switch step {
				case 2:
					// The trial boot claims pending.json; the watchdog waits
					// even while the daemon looks dead.
					if _, ok, err := selfupdate.ClaimPending(h.w.paths, fakeDaemonPID); !ok || err != nil {
						t.Fatalf("claim = %v, %v", ok, err)
					}
				case 4:
					writeRollbackResult(h.w.paths, selfupdate.Result{Outcome: outcome, From: "0.1.0-beta.1", To: "0.1.0"})
					_ = os.Remove(h.w.paths.Claim(fakeDaemonPID))
				}
			}
			if got := h.w.watch(); got != watchHandled {
				t.Fatalf("result = %d, log = %s", got, h.stderr.String())
			}
			if data, _ := os.ReadFile(exe); string(data) != "new-binary" {
				t.Fatalf("exe = %q (the watchdog restored)", data)
			}
			if len(h.signals) != 0 || len(h.starts) != 0 {
				t.Fatalf("signals = %v, starts = %v", h.signals, h.starts)
			}
		})
	}
}

// TestUpdateWatchDaemonDied: the daemon pid disappears before healthy; the
// watchdog restores the backup, records rolled_back, and starts the old
// daemon.
func TestUpdateWatchDaemonDied(t *testing.T) {
	home, exe, _ := trialHome(t, 0, "# new\n")
	h := newWatchHarness(t, home)
	h.onStep = func(step int) {
		if step == 2 {
			h.alive = false
		}
	}
	if got := h.w.watch(); got != watchRolledBack {
		t.Fatalf("result = %d, log = %s", got, h.stderr.String())
	}
	assertRolledBack(t, home, exe, "0.1.0 stopped during its first start")
	if len(h.signals) != 0 {
		t.Fatalf("signals = %v", h.signals)
	}
	if len(h.starts) != 1 || h.starts[0] != exe+" daemon" {
		t.Fatalf("starts = %v", h.starts)
	}
}

// TestUpdateWatchOrphanClaim: the trial boot claimed pending.json and died
// before it released the claim; the watchdog takes it over.
func TestUpdateWatchOrphanClaim(t *testing.T) {
	home, exe, _ := trialHome(t, 0, "# new\n")
	h := newWatchHarness(t, home)
	if _, ok, err := selfupdate.ClaimPending(h.w.paths, fakeDaemonPID); !ok || err != nil {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	h.alive = false
	if got := h.w.watch(); got != watchRolledBack {
		t.Fatalf("result = %d, log = %s", got, h.stderr.String())
	}
	assertRolledBack(t, home, exe, "stopped during its first start")
}

// TestUpdateWatchTimeout: no healthy report within 90 s. The watchdog claims
// first, sends SIGTERM, then SIGKILL after 10 s, and rolls back.
func TestUpdateWatchTimeout(t *testing.T) {
	t.Run("sigkill", func(t *testing.T) {
		home, exe, _ := trialHome(t, 0, "# new\n")
		h := newWatchHarness(t, home)
		start := h.clock
		var termAt, killAt time.Time
		h.onSignal = func(sig syscall.Signal) {
			// The claim happens before the daemon is stopped, so it can no
			// longer report healthy.
			if _, err := os.Stat(h.w.paths.Claim(9001)); err != nil {
				t.Fatalf("signal %v before the claim: %v", sig, err)
			}
			switch sig {
			case syscall.SIGTERM:
				termAt = h.clock
			case syscall.SIGKILL:
				killAt = h.clock
				h.alive = false
			}
		}
		if got := h.w.watch(); got != watchRolledBack {
			t.Fatalf("result = %d, log = %s", got, h.stderr.String())
		}
		if len(h.signals) != 2 || h.signals[0] != syscall.SIGTERM || h.signals[1] != syscall.SIGKILL {
			t.Fatalf("signals = %v", h.signals)
		}
		if termAt.Sub(start) != 90*time.Second || killAt.Sub(termAt) != 10*time.Second {
			t.Fatalf("term after %s, kill after %s", termAt.Sub(start), killAt.Sub(termAt))
		}
		assertRolledBack(t, home, exe, "0.1.0 did not report healthy within 90s")
		if len(h.starts) != 1 {
			t.Fatalf("starts = %v", h.starts)
		}
	})
	t.Run("sigterm", func(t *testing.T) {
		home, exe, _ := trialHome(t, 0, "# new\n")
		h := newWatchHarness(t, home)
		h.onSignal = func(sig syscall.Signal) { h.alive = false }
		if got := h.w.watch(); got != watchRolledBack {
			t.Fatalf("result = %d, log = %s", got, h.stderr.String())
		}
		if len(h.signals) != 1 || h.signals[0] != syscall.SIGTERM {
			t.Fatalf("signals = %v", h.signals)
		}
		assertRolledBack(t, home, exe, "within 90s")
	})
}

// TestUpdateWatchLockTaken: another process (launchd KeepAlive) holds the
// daemon lock after the rollback; the watchdog starts nothing.
func TestUpdateWatchLockTaken(t *testing.T) {
	home, exe, _ := trialHome(t, 0, "# new\n")
	h := newWatchHarness(t, home)
	h.alive = false
	h.lockFree = false
	if got := h.w.watch(); got != watchRolledBack {
		t.Fatalf("result = %d, log = %s", got, h.stderr.String())
	}
	assertRolledBack(t, home, exe, "stopped")
	if len(h.starts) != 0 || !strings.Contains(h.stderr.String(), "another process holds the daemon lock") {
		t.Fatalf("starts = %v, log = %s", h.starts, h.stderr.String())
	}
}

// TestUpdateWatchClaimRace: the watchdog and the trial boot never both
// restore. While the watchdog holds the claim, the in-binary rollback backs
// off and markHealthy records nothing.
func TestUpdateWatchClaimRace(t *testing.T) {
	setVersion(t, "0.1.0")
	home, exe, pending := trialHome(t, 1, "# new\n")
	paths := selfupdate.Paths{Home: home}
	// The test's parent process stands in for a live watchdog.
	watchdog := os.Getppid()
	claim, ok, err := selfupdate.ClaimPending(paths, watchdog)
	if !ok || err != nil {
		t.Fatalf("watchdog claim = %v, %v", ok, err)
	}
	calls := stubExec(t)
	var stderr bytes.Buffer
	if code := rollbackUpdate(nil, &stderr, paths, pending, "boom"); code != 1 || len(*calls) != 0 {
		t.Fatalf("code = %d, exec calls = %v, stderr = %s", code, *calls, stderr.String())
	}
	if data, _ := os.ReadFile(exe); string(data) != "new-binary" {
		t.Fatalf("exe = %q (the trial rollback restored)", data)
	}
	trial := &trialBoot{paths: paths, pending: pending, now: time.Now}
	trial.markHealthy()
	if state, _ := selfupdate.ReadState(paths); state.LastResult != nil {
		t.Fatalf("last_result = %+v", state.LastResult)
	}
	if _, err := os.Stat(claim.Path); err != nil {
		t.Fatalf("the watchdog claim was touched: %v", err)
	}

	// The daemon does not start while a live watchdog holds a claim.
	if _, _, action := beginTrialBoot(paths, "0.1.0", time.Now); action != trialBusy {
		t.Fatalf("action = %d", action)
	}
}

// TestReviveClaims: a claim left by a dead process becomes pending.json with
// boots 1 again, so the next start rolls back; a live claimer blocks the
// start.
func TestReviveClaims(t *testing.T) {
	_, _, _ = trialHome(t, 0, "# new\n")
	paths := selfupdate.Paths{Home: os.Getenv("LANA_HOME")}
	if _, ok, err := selfupdate.ClaimPending(paths, 777); !ok || err != nil {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	if busy := reviveClaims(paths, func(pid int) bool { return pid == 777 }); busy != 777 {
		t.Fatalf("busy = %d", busy)
	}
	if busy := reviveClaims(paths, func(int) bool { return false }); busy != 0 {
		t.Fatalf("busy = %d", busy)
	}
	pending, err := selfupdate.ReadPending(paths)
	if err != nil || pending == nil || pending.Boots != 1 || pending.To != "0.1.0" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if pids, _ := selfupdate.ClaimPIDs(paths); len(pids) != 0 {
		t.Fatalf("claims left: %v", pids)
	}
	if got := trialDecision(pending, "0.1.0"); got != trialRollback {
		t.Fatalf("decision = %d", got)
	}
}

// writeScript writes an executable shell script.
func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateWatchRestoresAfterCrash is the end-to-end case of the bug: the
// new binary is a process that exits 2 before it could run any trial boot
// code. The real watcher (real pid checks, real lock, real restore) rolls
// back; only the daemon start is recorded instead of run.
func TestUpdateWatchRestoresAfterCrash(t *testing.T) {
	home, exe, _ := trialHome(t, 0, "# new\n")
	script := filepath.Join(t.TempDir(), "new-lana")
	writeScript(t, script, "sleep 0.3\necho 'error: simulated startup failure in 0.1.0' >&2\nexit 2\n")
	daemon := exec.Command(script)
	daemon.Stderr = io.Discard
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- daemon.Wait() }()

	var log bytes.Buffer
	w := newUpdateWatcher(home, daemon.Process.Pid, []string{"daemon", "--flag"}, &log)
	w.poll = 20 * time.Millisecond
	w.timeout = 20 * time.Second
	var starts []string
	w.start = func(exe string, args []string) error {
		starts = append(starts, exe+" "+strings.Join(args, " "))
		return nil
	}
	if got := w.watch(); got != watchRolledBack {
		t.Fatalf("result = %d, log = %s", got, log.String())
	}
	if err := <-exited; err == nil || !strings.Contains(err.Error(), "exit status 2") {
		t.Fatalf("helper exit = %v", err)
	}
	assertRolledBack(t, home, exe, "0.1.0 stopped during its first start")
	if len(starts) != 1 || starts[0] != exe+" daemon --flag" {
		t.Fatalf("starts = %v", starts)
	}
	// The lock was only probed, not kept.
	if !daemonLockFree(home) {
		t.Fatal("daemon lock held after the watch")
	}
}

// TestStartUpdateWatch: the watchdog runs from <backup>/lana with the
// documented arguments, in its own process group, without the daemon lock
// descriptor or LANA_RESTART_LOCK_FD.
func TestStartUpdateWatch(t *testing.T) {
	home := t.TempDir()
	backup := t.TempDir()
	out := t.TempDir()
	lock, err := acquireDaemonLock(home)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	// A descriptor without close-on-exec, as the lock is right before the
	// exec of an update.
	const leakFD = 57
	if err := unix.Dup2(lock.fd(), leakFD); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Close(leakFD) }()
	if err := setCloseOnExec(leakFD, false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEAK_FD", strconv.Itoa(leakFD))
	t.Setenv("OUT", out)
	t.Setenv(restartLockFDEnv, "57")
	writeScript(t, filepath.Join(backup, "lana"), `printf '%s\n' "$@" > "$OUT/args"
if [ -e /dev/fd/$LEAK_FD ]; then echo open > "$OUT/fd"; else echo closed > "$OUT/fd"; fi
echo "${LANA_RESTART_LOCK_FD:-unset}" > "$OUT/env"
ps -o pgid= -p $$ | tr -d ' ' > "$OUT/pgid"
`)
	if err := startUpdateWatch(home, backup, 1234, []string{"daemon", "-x"}); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(data))
	}
	want := strings.Join([]string{"update-watch", "--home", home, "--pid", "1234", "--", "daemon", "-x"}, "\n")
	if got := read("args"); got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
	if got := read("fd"); got != "closed" {
		t.Fatal("the watchdog inherited the lock descriptor")
	}
	if got := read("env"); got != "unset" {
		t.Fatalf("LANA_RESTART_LOCK_FD = %q", got)
	}
	if got := read("pgid"); got == strconv.Itoa(syscall.Getpgrp()) || got == "" {
		t.Fatalf("pgid = %q, same group as the daemon", got)
	}

	// A watchdog that cannot start is an error the install only warns about.
	if err := startUpdateWatch(home, t.TempDir(), 1234, nil); err == nil {
		t.Fatal("missing binary started")
	}
}

// TestProcessAlive covers the pid check: a running and a reaped process, and
// a zombie that was not reaped yet.
func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("self not alive")
	}
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	// Not reaped yet: a zombie once it exits.
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Fatal("exited child still alive")
	}
	_ = cmd.Wait()
	if processAlive(pid) || processAlive(0) {
		t.Fatal("reaped child alive")
	}
}

// TestUpdateWatchArgs pins the watchdog command line (API-UPD-010).
func TestUpdateWatchArgs(t *testing.T) {
	got := fmt.Sprint(updateWatchArgs("/h", 12, []string{"daemon"}))
	if got != "[update-watch --home /h --pid 12 -- daemon]" {
		t.Fatalf("args = %s", got)
	}
}

// TestClaimRaceInWatch runs the watchdog claim and the trial rollback claim
// at once many times; exactly one wins each time.
func TestClaimRaceInWatch(t *testing.T) {
	for i := 0; i < 20; i++ {
		paths := selfupdate.Paths{Home: t.TempDir()}
		if err := selfupdate.WritePending(paths, selfupdate.Pending{To: "0.1.0"}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wins := make(chan int, 2)
		for _, pid := range []int{100, 200} {
			wg.Add(1)
			go func(pid int) {
				defer wg.Done()
				if _, ok, err := selfupdate.ClaimPending(paths, pid); ok && err == nil {
					wins <- pid
				}
			}(pid)
		}
		wg.Wait()
		close(wins)
		if n := len(wins); n != 1 {
			t.Fatalf("round %d: %d winners", i, n)
		}
	}
}
