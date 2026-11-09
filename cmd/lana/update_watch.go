package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/selfupdate"
)

// The update watchdog guards the first start of a new binary from the outside
// (design.md, Trial boot and rollback). The old binary starts it from its
// backup copy right before it execs the new one; the watchdog rolls back when
// the daemon process dies or does not report healthy in time, which covers
// failures the trial boot inside the new binary cannot catch (a crash before
// main, a wrong architecture, SIGKILL, a hang).

// updateWatchChildEnv marks the detached watchdog process. Without it,
// `lana update-watch` starts a detached copy of itself and exits, so the
// daemon can reap it before the exec and the watchdog is not its child.
const updateWatchChildEnv = "LANA_UPDATE_WATCH_CHILD"

// Watchdog timings. Tests shorten them.
var (
	// updateWatchTimeout is how long the new version has to report healthy.
	updateWatchTimeout = 90 * time.Second
	// updateWatchKillGrace is the wait between SIGTERM and SIGKILL, and after
	// SIGKILL.
	updateWatchKillGrace = 10 * time.Second
	// updateWatchPoll is the interval between checks.
	updateWatchPoll = 250 * time.Millisecond
	// updateWatchLockWait is how long the watchdog waits for the daemon lock
	// to be free before it starts the old daemon.
	updateWatchLockWait = 5 * time.Second
	// updateWatchSpawnWait limits the detaching step of the spawn.
	updateWatchSpawnWait = 5 * time.Second
)

// spawnUpdateWatch starts the watchdog. Tests replace it so they never spawn.
var spawnUpdateWatch = startUpdateWatch

// updateWatchArgs are the arguments of the watchdog command after the binary.
func updateWatchArgs(home string, pid int, daemonArgs []string) []string {
	args := []string{"update-watch", "--home", home, "--pid", strconv.Itoa(pid), "--"}
	return append(args, daemonArgs...)
}

// startUpdateWatch runs `<backupDir>/lana update-watch --home <home> --pid
// <pid> -- <daemonArgs...>` in its own session with stdin /dev/null and the
// daemon's stdout and stderr. Every other descriptor is marked close-on-exec
// first, so the watchdog holds neither the daemon lock nor a socket. The
// command detaches a copy of itself and exits; this waits for that.
func startUpdateWatch(home, backupDir string, pid int, daemonArgs []string) error {
	markFDsCloseOnExec()
	cmd := exec.Command(filepath.Join(backupDir, "lana"), updateWatchArgs(home, pid, daemonArgs)...)
	cmd.Env = withoutEnv(os.Environ(), restartLockFDEnv, updateWatchChildEnv)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("update-watch: %w", err)
		}
		return nil
	case <-time.After(updateWatchSpawnWait):
		_ = cmd.Process.Kill()
		waitErr := <-done
		return fmt.Errorf("update-watch did not detach within %s (%v)", updateWatchSpawnWait, waitErr)
	}
}

// markFDsCloseOnExec sets FD_CLOEXEC on every open descriptor above 2.
// Descriptors Go opens already have it; this covers the rest.
func markFDsCloseOnExec() {
	// Readdirnames, not ReadDir: a stat of the listing's own entry fails.
	if dir, err := os.Open("/dev/fd"); err == nil {
		names, err := dir.Readdirnames(-1)
		_ = dir.Close()
		if err == nil {
			for _, name := range names {
				if fd, err := strconv.Atoi(name); err == nil && fd >= 3 {
					_ = setCloseOnExec(fd, true)
				}
			}
			return
		}
	}
	// No /dev/fd listing: try every descriptor below the limit.
	limit := uint64(4096)
	var rlim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlim); err == nil && rlim.Cur < limit {
		limit = rlim.Cur
	}
	for fd := 3; uint64(fd) < limit; fd++ {
		_ = setCloseOnExec(fd, true)
	}
}

// cmdUpdateWatch runs the hidden `lana update-watch --home <dir> --pid <pid>
// -- <daemon args...>` (API-UPD-010).
func cmdUpdateWatch(args []string) int {
	fs := flag.NewFlagSet("update-watch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	home := fs.String("home", "", "home folder of the daemon")
	pid := fs.Int("pid", 0, "daemon process id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *home == "" || *pid <= 0 {
		fmt.Fprintln(os.Stderr, "error: use lana update-watch --home <dir> --pid <pid> -- <daemon args...>")
		return 2
	}
	if os.Getenv(updateWatchChildEnv) == "" {
		if err := detachUpdateWatch(args); err != nil {
			fmt.Fprintf(os.Stderr, "error: update-watch: %v\n", err)
			return 1
		}
		return 0
	}
	_ = os.Unsetenv(updateWatchChildEnv)
	switch newUpdateWatcher(*home, *pid, fs.Args(), os.Stderr).watch() {
	case watchRollbackFailed, watchGaveUp:
		return 1
	}
	return 0
}

// detachUpdateWatch starts this binary again as the watchdog in its own
// session and returns without waiting for it.
func detachUpdateWatch(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, append([]string{"update-watch"}, args...)...)
	cmd.Env = append(withoutEnv(os.Environ(), updateWatchChildEnv, restartLockFDEnv), updateWatchChildEnv+"=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// watchResult is how a watch ended.
type watchResult int

const (
	// watchHealthy: the new version reported healthy.
	watchHealthy watchResult = iota
	// watchHandled: the update ended another way (the trial boot rolled back
	// or failed, or the install gave up), so there was nothing to do.
	watchHandled
	// watchRolledBack: the watchdog restored the backup.
	watchRolledBack
	// watchRollbackFailed: the watchdog claimed the update but the restore
	// failed.
	watchRollbackFailed
	// watchGaveUp: another process kept a claim past the hard deadline.
	watchGaveUp
)

// updateWatcher decides when the watchdog rolls back. Its process and clock
// functions are fakes in tests.
type updateWatcher struct {
	paths selfupdate.Paths
	// pid is the daemon process; it keeps its pid across the exec.
	pid int
	// self is the watchdog pid, used for its claim file.
	self       int
	daemonArgs []string
	stderr     io.Writer

	alive    func(pid int) bool
	signal   func(pid int, sig syscall.Signal) error
	now      func() time.Time
	sleep    func(time.Duration)
	lockFree func() bool
	start    func(exe string, args []string) error

	timeout, killGrace, poll, lockWait time.Duration
}

func newUpdateWatcher(home string, pid int, daemonArgs []string, stderr io.Writer) *updateWatcher {
	return &updateWatcher{
		paths:      selfupdate.Paths{Home: home},
		pid:        pid,
		self:       os.Getpid(),
		daemonArgs: daemonArgs,
		stderr:     stderr,
		alive:      processAlive,
		signal:     syscall.Kill,
		now:        time.Now,
		sleep:      time.Sleep,
		lockFree:   func() bool { return daemonLockFree(home) },
		start:      startDetachedDaemon,
		timeout:    updateWatchTimeout,
		killGrace:  updateWatchKillGrace,
		poll:       updateWatchPoll,
		lockWait:   updateWatchLockWait,
	}
}

func (w *updateWatcher) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(w.stderr, "update-watch: "+format+"\n", args...)
}

// watch waits until the update ends and rolls back when the daemon died or
// did not report healthy in time:
//
//   - pending.json and every claim gone: the update ended; healthy when
//     last_result is updated for this version.
//   - the daemon pid is gone: claim pending.json (or the claim the dead daemon
//     left) and roll back.
//   - the timeout passed: claim pending.json, stop the daemon (SIGTERM, then
//     SIGKILL), and roll back.
//
// A claim held by another process is waited for.
func (w *updateWatcher) watch() watchResult {
	to := ""
	if pending, err := selfupdate.ReadPending(w.paths); err == nil && pending != nil {
		to = pending.To
	} else if claim, err := selfupdate.ReadClaim(w.paths.Claim(w.pid)); err == nil {
		to = claim.To
	}
	w.logf("watching pid %d for the first start of %s", w.pid, displayVersion(to))
	deadline := w.now().Add(w.timeout)
	hardDeadline := deadline.Add(w.timeout)
	for {
		pendingThere := fileExists(w.paths.Pending())
		claims, _ := selfupdate.ClaimPIDs(w.paths)
		if !pendingThere && len(claims) == 0 {
			return w.finished(to)
		}
		switch {
		case !w.alive(w.pid):
			claim, ok := w.claim(true)
			if ok {
				return w.rollback(claim, fmt.Sprintf("%s stopped during its first start before it reported healthy", displayVersion(claim.Pending.To)))
			}
		case !w.now().Before(deadline) && pendingThere:
			claim, ok := w.claim(false)
			if ok {
				w.stopDaemon()
				return w.rollback(claim, fmt.Sprintf("%s did not report healthy within %ds", displayVersion(claim.Pending.To), int(w.timeout/time.Second)))
			}
		}
		if !w.now().Before(hardDeadline) {
			w.logf("gave up: the update of %s did not end (claims %v)", displayVersion(to), claims)
			return watchGaveUp
		}
		w.sleep(w.poll)
	}
}

// claim takes pending.json; with orphan it also takes over a claim the dead
// daemon left behind.
func (w *updateWatcher) claim(orphan bool) (selfupdate.Claim, bool) {
	claim, ok, err := selfupdate.ClaimPending(w.paths, w.self)
	if err != nil {
		w.logf("claim pending.json: %v", err)
		return selfupdate.Claim{}, false
	}
	if ok || !orphan {
		return claim, ok
	}
	claim, ok, err = selfupdate.TakeOverClaim(w.paths, w.pid, w.self)
	if err != nil {
		w.logf("take over the claim of pid %d: %v", w.pid, err)
		return selfupdate.Claim{}, false
	}
	return claim, ok
}

// finished reports the outcome another process recorded.
func (w *updateWatcher) finished(to string) watchResult {
	state, _ := selfupdate.ReadState(w.paths)
	result := state.LastResult
	if result != nil && result.Outcome == selfupdate.OutcomeUpdated && to != "" && selfupdate.SameVersion(result.To, to) {
		w.logf("%s is healthy", displayVersion(to))
		return watchHealthy
	}
	if result != nil {
		w.logf("the update to %s ended with %s", displayVersion(result.To), result.Outcome)
	} else {
		w.logf("the update to %s ended", displayVersion(to))
	}
	return watchHandled
}

// stopDaemon sends SIGTERM, waits killGrace, then sends SIGKILL and waits
// killGrace again.
func (w *updateWatcher) stopDaemon() {
	w.logf("stopping pid %d", w.pid)
	_ = w.signal(w.pid, syscall.SIGTERM)
	if w.waitExit(w.killGrace) {
		return
	}
	w.logf("pid %d did not stop; killing it", w.pid)
	_ = w.signal(w.pid, syscall.SIGKILL)
	if !w.waitExit(w.killGrace) {
		w.logf("pid %d is still running", w.pid)
	}
}

// waitExit polls until the daemon pid is gone or d passed.
func (w *updateWatcher) waitExit(d time.Duration) bool {
	end := w.now().Add(d)
	for {
		if !w.alive(w.pid) {
			return true
		}
		if !w.now().Before(end) {
			return false
		}
		w.sleep(w.poll)
	}
}

// rollback restores the claimed update and starts the old daemon.
func (w *updateWatcher) rollback(claim selfupdate.Claim, reason string) watchResult {
	pending := claim.Pending
	w.logf("%s; rolling back to %s", reason, displayVersion(pending.From))
	if err := restoreClaimed(w.paths, claim, reason, w.now); err != nil {
		w.logf("rollback failed: %v", err)
		return watchRollbackFailed
	}
	w.logf("restored %s from %s", displayVersion(pending.From), pending.BackupDir)
	w.startOld(pending.ExePath)
	return watchRolledBack
}

// startOld starts the restored binary with the daemon's arguments once the
// daemon lock is free. A process that takes the lock first (launchd
// KeepAlive) wins and nothing more is started.
func (w *updateWatcher) startOld(exe string) {
	if len(w.daemonArgs) == 0 {
		w.logf("no daemon arguments; not starting %s", exe)
		return
	}
	end := w.now().Add(w.lockWait)
	for !w.lockFree() {
		if !w.now().Before(end) {
			w.logf("another process holds the daemon lock; not starting %s", exe)
			return
		}
		w.sleep(w.poll)
	}
	if err := w.start(exe, w.daemonArgs); err != nil {
		w.logf("start %s: %v", exe, err)
		return
	}
	w.logf("started %s", exe)
}

// daemonLockFree takes and releases the daemon lock of home.
func daemonLockFree(home string) bool {
	lock, err := acquireDaemonLock(home)
	if err != nil {
		return false
	}
	lock.release()
	return true
}

// startDetachedDaemon starts exe with args in its own session, with the
// watchdog's stdout and stderr, and does not wait for it.
func startDetachedDaemon(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Env = withoutEnv(os.Environ(), restartLockFDEnv, updateWatchChildEnv)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// processAlive reports whether pid runs. A zombie counts as gone; EPERM means
// the process exists.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	return !processZombie(pid)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// displayVersion names a version in watchdog messages.
func displayVersion(v string) string {
	if v == "" {
		return "the new version"
	}
	return v
}
