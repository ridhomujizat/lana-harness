package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	kv "github.com/ridhomujizat/lana-harness/internal/knowledge"
	"github.com/ridhomujizat/lana-harness/internal/selfupdate"
	"github.com/ridhomujizat/lana-harness/internal/sqlitex"
	"github.com/ridhomujizat/lana-harness/store"
)

// trialAction is what a daemon start does with pending.json (design.md, Trial
// boot and rollback).
type trialAction int

const (
	// trialNone: no pending.json, a normal start.
	trialNone trialAction = iota
	// trialStale: the running binary is not pending.to (the old binary after a
	// failed install or a rollback); pending.json is deleted.
	trialStale
	// trialStart: the first start of pending.to; boots becomes 1.
	trialStart
	// trialRollback: a previous trial boot never reported healthy.
	trialRollback
	// trialBusy: another live process (the update watchdog) holds a claim on
	// pending.json and is restoring the backup; the daemon does not start.
	trialBusy
)

// trialDecision maps pending.json and the running version to the action.
func trialDecision(pending *selfupdate.Pending, running string) trialAction {
	switch {
	case pending == nil:
		return trialNone
	case !selfupdate.SameVersion(pending.To, running):
		return trialStale
	case pending.Boots >= 1:
		return trialRollback
	default:
		return trialStart
	}
}

// trialBoot is a running trial boot. It ends with markHealthy, or with a
// rollback when startup fails first (REQ-UPD-004).
type trialBoot struct {
	paths   selfupdate.Paths
	pending selfupdate.Pending
	now     func() time.Time
	healthy atomic.Bool
}

// active reports whether the trial boot has not yet reported healthy.
func (t *trialBoot) active() bool { return t != nil && !t.healthy.Load() }

// markHealthy ends the trial boot: the web server serves (or web is off) and
// the channels started. It deletes pending.json and records last_result
// updated (design.md step 5).
func (t *trialBoot) markHealthy() {
	if t == nil || !t.healthy.CompareAndSwap(false, true) {
		return
	}
	// Claim pending.json first: when the update watchdog already claimed it
	// (it timed out), the watchdog records the outcome and rolls back.
	claim, ok, err := selfupdate.ClaimPending(t.paths, os.Getpid())
	if err != nil {
		slog.Warn("update: claim pending.json", "error", err)
		return
	}
	if !ok {
		slog.Warn("update: pending.json was claimed by the update watchdog; not recording healthy", "to", t.pending.To)
		return
	}
	writeUpdateResult(t.paths, selfupdate.Result{
		Outcome: selfupdate.OutcomeUpdated, From: t.pending.From, To: t.pending.To, At: t.now().UnixMilli(),
	})
	if err := claim.Release(); err != nil {
		slog.Warn("update: remove pending.json", "error", err)
	}
	slog.Info("update: healthy", "from", t.pending.From, "to", t.pending.To)
}

// beginTrialBoot reads pending.json at daemon start. It returns the trial boot
// for trialStart, the pending update for trialRollback, and nil otherwise.
func beginTrialBoot(paths selfupdate.Paths, running string, now func() time.Time) (*trialBoot, *selfupdate.Pending, trialAction) {
	if busy := reviveClaims(paths, processAlive); busy != 0 {
		slog.Warn("update: a rollback is in progress", "pid", busy)
		return nil, nil, trialBusy
	}
	pending, err := selfupdate.ReadPending(paths)
	if err != nil {
		slog.Warn("update: unreadable pending.json; ignoring it", "error", err)
		_ = selfupdate.RemovePending(paths)
		return nil, nil, trialNone
	}
	action := trialDecision(pending, running)
	switch action {
	case trialStale:
		_ = selfupdate.RemovePending(paths)
	case trialRollback:
		return nil, pending, action
	case trialStart:
		pending.Boots = 1
		if err := selfupdate.WritePending(paths, *pending); err != nil {
			slog.Warn("update: write pending.json", "error", err)
		}
		return &trialBoot{paths: paths, pending: *pending, now: now}, pending, action
	}
	return nil, nil, action
}

// rollbackUpdate restores the backup of a failed trial boot (databases,
// config.toml, previous binary), deletes pending.json, records last_result
// rolled_back, and execs the previous binary with the lock held (design.md,
// Rollback). It returns only when the restore or the exec failed.
//
// It claims pending.json first. When the claim fails, the update watchdog
// already took it and restores the backup itself, so this returns 1 without
// touching any file.
func rollbackUpdate(lock *daemonLock, stderr io.Writer, paths selfupdate.Paths, pending selfupdate.Pending, reason string) int {
	claim, ok, err := selfupdate.ClaimPending(paths, os.Getpid())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: update rollback: claim pending.json: %v\n", err)
		return 1
	}
	if !ok {
		_, _ = fmt.Fprintf(stderr, "update: %s failed its trial boot: %s; the update watchdog is rolling back\n", pending.To, reason)
		return 1
	}
	pending = claim.Pending
	_, _ = fmt.Fprintf(stderr, "update: %s failed its trial boot: %s; rolling back to %s\n", pending.To, reason, pending.From)
	if err := restoreClaimed(paths, claim, reason, time.Now); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: update rollback: %v\n", err)
		return 1
	}
	return execRestart(lock, stderr, pending.ExePath)
}

// restoreClaimed restores the backup of a claimed pending.json, records
// last_result rolled_back (or failed when the restore failed), skips the
// failed version, and releases the claim (design.md, Rollback).
func restoreClaimed(paths selfupdate.Paths, claim selfupdate.Claim, reason string, now func() time.Time) error {
	pending := claim.Pending
	defer func() { _ = claim.Release() }()
	if err := selfupdate.Restore(pending.BackupDir, paths.Home, pending.ExePath); err != nil {
		writeRollbackResult(paths, selfupdate.Result{
			Outcome: selfupdate.OutcomeFailed, From: pending.From, To: pending.To,
			Reason: reason + "; rollback failed: " + err.Error(), At: now().UnixMilli(),
		})
		return err
	}
	writeRollbackResult(paths, selfupdate.Result{
		Outcome: selfupdate.OutcomeRolledBack, From: pending.From, To: pending.To, Reason: reason, At: now().UnixMilli(),
	})
	return nil
}

// writeRollbackResult records the result of a rollback and skips the version
// that failed, so the dashboard offers only a newer release (REQ-UPD-004).
func writeRollbackResult(paths selfupdate.Paths, result selfupdate.Result) {
	if _, err := selfupdate.UpdateState(paths, func(state *selfupdate.State) {
		state.LastResult = &result
		if to := strings.TrimPrefix(strings.TrimSpace(result.To), "v"); to != "" {
			state.Skipped = to
		}
	}); err != nil {
		slog.Warn("update: write state.json", "error", err)
	}
}

// reviveClaims handles claim files left in updates/ at daemon start. A claim
// of a live process other than this one means a rollback is running: its pid
// is returned and the daemon does not start. A claim of a dead process (or of
// this pid, from an image before an exec) is turned back into pending.json
// with boots 1, so the trial boot logic rolls it back.
func reviveClaims(paths selfupdate.Paths, alive func(int) bool) int {
	pids, err := selfupdate.ClaimPIDs(paths)
	if err != nil {
		slog.Warn("update: list claims", "error", err)
		return 0
	}
	self := os.Getpid()
	for _, pid := range pids {
		if pid != self && alive(pid) {
			return pid
		}
	}
	for _, pid := range pids {
		claim := selfupdate.Claim{Path: paths.Claim(pid), PID: pid}
		pending, err := selfupdate.ReadClaim(claim.Path)
		if err != nil {
			slog.Warn("update: unreadable claim; removing it", "path", claim.Path, "error", err)
			_ = claim.Release()
			continue
		}
		if current, _ := selfupdate.ReadPending(paths); current == nil {
			if pending.Boots < 1 {
				pending.Boots = 1
			}
			if err := selfupdate.WritePending(paths, pending); err != nil {
				slog.Warn("update: revive claim", "error", err)
				continue
			}
		}
		_ = claim.Release()
	}
	return 0
}

// cmdUpdate runs `lana update rollback` (API-UPD-010).
func cmdUpdate(args []string) int {
	if len(args) != 1 || args[0] != "rollback" {
		fmt.Fprintln(os.Stderr, "error: use lana update rollback")
		return 2
	}
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	return runUpdateRollback(home, os.Stdout, os.Stderr)
}

// runUpdateRollback restores the newest backup while the daemon is stopped
// (REQ-UPD-006). Exit 0 restored, 1 no backup or failure, 3 daemon running.
func runUpdateRollback(home string, stdout, stderr io.Writer) int {
	lock, err := acquireDaemonLock(home)
	if err != nil {
		if strings.Contains(err.Error(), "daemon already running") {
			_, _ = fmt.Fprintf(stderr, "error: %v; stop the daemon first\n", err)
			return 3
		}
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer lock.release()
	paths := selfupdate.Paths{Home: home}
	backup, ok, err := selfupdate.NewestBackup(paths)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if !ok {
		_, _ = fmt.Fprintf(stderr, "error: no update backup in %s\n", paths.Backups())
		return 1
	}
	if err := selfupdate.Restore(backup.Dir, home, ""); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: rollback: %v\n", err)
		return 1
	}
	_ = selfupdate.RemovePending(paths)
	manifest := backup.Manifest
	writeRollbackResult(paths, selfupdate.Result{
		Outcome: selfupdate.OutcomeRolledBack, From: manifest.From, To: manifest.To,
		Reason: "rolled back with lana update rollback", At: time.Now().UnixMilli(),
	})
	_, _ = fmt.Fprintf(stdout, "✓ restored lana %s from %s\n", manifest.From, backup.Dir)
	return 0
}

// cmdUpdateProbe runs the hidden `lana update-probe --home <dir>`
// (API-UPD-010).
func cmdUpdateProbe(args []string) int {
	fs := flag.NewFlagSet("update-probe", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	home := fs.String("home", "", "home folder to probe")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *home == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "error: use lana update-probe --home <dir>")
		return 2
	}
	return runUpdateProbe(context.Background(), *home, os.Stderr)
}

// runUpdateProbe loads the config from home and opens and migrates the
// databases in home, then returns. It starts no channels, web server, or
// turns (REQ-UPD-003 step 3).
func runUpdateProbe(ctx context.Context, home string, stderr io.Writer) int {
	result, err := config.Load(ctx, config.Options{Home: home})
	if err != nil {
		var errs config.Errors
		if errors.As(err, &errs) {
			for _, item := range errs {
				_, _ = fmt.Fprintf(stderr, "error: %s\n", item.Error())
			}
			return 2
		}
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	names, err := selfupdate.ListDatabases(home)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	for _, name := range names {
		path := filepath.Join(home, name)
		var openErr error
		switch name {
		case "lana.db":
			var st *store.Store
			st, openErr = store.Open(ctx, path)
			if openErr == nil {
				openErr = st.Close()
			}
		case "knowledge.db":
			var idx *kv.Index
			idx, openErr = kv.OpenIndex(path, kv.ResolvePath(home, result.Config.Knowledge.Path))
			if openErr == nil {
				openErr = idx.Close()
			}
		default:
			openErr = quickCheckDatabase(ctx, path)
		}
		if openErr != nil {
			_, _ = fmt.Fprintf(stderr, "error: %s: %v\n", name, openErr)
			return 1
		}
	}
	return 0
}

// quickCheckDatabase opens a database the probe does not migrate and runs
// PRAGMA quick_check.
func quickCheckDatabase(ctx context.Context, path string) error {
	db, err := sqlitex.Open("file:" + path + "?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("quick_check: %s", result)
	}
	return nil
}
