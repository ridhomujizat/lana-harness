# Design: Self-update

- **Status**: Approved (agent recommendation, 2026-11-08)
- **Requirements**: [requirements.md](requirements.md) · **Contract**: [api-contract.md](api-contract.md)

## Components

| Component | Location | Responsibility |
|---|---|---|
| `selfupdate` | `internal/selfupdate/` (new) | semver compare, GitHub release client, channel filter, download with progress, checksum, eligibility, backup and restore, `pending.json` and `state.json` |
| Update service | `cmd/lana/update.go` (new) | the job state machine, the periodic checker, the trial-boot guard, `lana update rollback`, `lana update-probe` |
| Web port | `web/update.go` (new) | `UpdateStore` interface, the API-UPD-003..009 handlers, `Server.SetUpdateStore` |
| Config | `config/` | `[update]` section with defaults and validation (API-UPD-001) |
| Card + Settings | `web/ui/src/api/update.ts`, `web/ui/src/views/update-card.tsx`, Settings section | REQ-UPD-002, REQ-UPD-007 |

`internal/selfupdate` has no dependency on `web`, `lana`, or `cmd`. It takes an `*http.Client`, a clock,
paths, and the running version, so tests drive it with `httptest` and temp dirs.

## Version compare

Semantic Versioning 2.0 precedence, with a leading `v` stripped: `0.1.0-beta.1 < 0.1.0-beta.2 <
0.1.0 < 0.1.1 < 0.2.0-beta.1`. Numeric prerelease identifiers compare as numbers. An unparsable tag is
ignored. The running version `dev` (or any unparsable version) means `supported = false`.

## Check

`GET {api}/repos/{repo}/releases?per_page=30`. Skip `draft`. On `stable`, skip `prerelease`. Keep
releases with an asset named `lana-<GOOS>-<GOARCH>` and an asset named `SHA256SUMS`; take the highest
version above the running one. The asset `browser_download_url` values are used for downloads, so a
fake server in tests can serve them. HTTP timeout 15 s. The result and `checked_at` live in memory;
`skipped` and `last_result` live in `state.json`.

## Eligibility (`can_apply`)

1. `os.Executable()` then `filepath.EvalSymlinks`. This resolved path is `exe_path`.
2. Not supported: `GOOS` not darwin or linux; path under `/nix/store/`; path containing `/Cellar/`.
3. The directory of `exe_path` must accept a new file: create and remove
   `.lana-update-write-test-<pid>` there.

## Job state machine

One job at a time, guarded by a mutex. Each step checks the job context for cancel.

```text
downloading → verifying → probing → waiting → installing → restarting
     │            │          │         │           │
     └────────────┴──────────┴─────────┴→ failed / cancelled (daemon keeps running the old binary)
```

- **downloading**: stream the asset to `updates/lana-<v>.download` with a size cap of 200 MiB and
  progress counters. Also fetch `SHA256SUMS`.
- **verifying**: SHA-256 must equal the entry for `lana-<os>-<arch>` (`<hex>  <name>` or `<hex> *<name>`).
  `chmod 0755`. On darwin remove `com.apple.quarantine` (best effort). Run `<file> --version`
  (timeout 10 s); its output must contain the target version.
- **probing**: make a temp home `updates/probe-<v>/` holding `config.toml`, the secrets and env files
  the config loader reads, the agent folders needed by config load (copy only what the loader
  needs; symlinks are fine for read-only folders), and `VACUUM INTO` copies of every `*.db` directly
  in `$LANA_HOME`. Run `<file> update-probe --home <temp>` with `LANA_HOME=<temp>` (timeout 60 s).
  Nonzero exit → failed with the last 20 lines of stderr. Remove the temp home afterwards.
- **waiting**: call the existing restart path with a hook instead of a plain restart: no running
  turns or `force` → go on at once (force interrupts with `core.ErrInterruptedRestart`); otherwise begin the
  spec 16 drain with reason restart and report `running_turns` and `deadline`. Cancel in this state
  cancels the drain.
- **installing**: runs after the drain completes, before exec, on the daemon goroutine that would
  exec. Create the backup folder: `VACUUM INTO` every `*.db` in `$LANA_HOME` (the store is idle now),
  copy `config.toml` and the current binary, write `manifest.json`. Write `pending.json` with
  `boots = 0`. Rename the verified download over `exe_path` (same filesystem: download lives in
  `$LANA_HOME/updates/`; when that is a different filesystem, copy to `<exe_dir>/.lana-new-<pid>` first,
  then rename). Prune backups to the newest 2.
  If any part fails: put the old binary back if it was replaced, delete `pending.json`, record
  `last_result = failed`, and restart in place into the old binary.
- **restarting**: `restartInPlace` (existing) execs `exe_path`, which is now the new binary.

The drain/restart wiring in `runDaemonWithLogs` gets one pre-exec hook: when the restart was requested
by an update job, the hook runs **installing** and then the normal exec happens. A plain restart
skips the hook.

## Trial boot and rollback

At the top of `runDaemonWithLogs`, after the daemon lock is held:

1. Read `updates/pending.json`. Absent → normal start.
2. Running version ≠ `pending.to` → we are the old binary after a failed install or a rollback: delete
   `pending.json`, normal start.
3. Running version = `pending.to`:
   - `boots >= 1` → a previous trial boot never reported healthy: **rollback**.
   - else write `boots = 1` and start in trial mode.
4. In trial mode, any error return from startup, or a panic (deferred `recover`) before healthy →
   **rollback** with the error text as reason.
5. Healthy = the web server is serving (or web is disabled) and channels started. Then claim
   `pending.json`, write `last_result = {updated, from, to}`, and delete the claim. When the claim
   fails, the watchdog already took the update and records nothing here.

**Rollback**: claim `pending.json` (see Claim below; back off when the claim fails), copy each
backed-up `*.db` over the live one (remove its `-wal` and `-shm` first), copy the backed-up
`config.toml` back, copy the previous binary over `exe_path` (temp file + rename),
write `last_result = {rolled_back, from, to, reason}`, delete the claim file, and exec `exe_path`
with the same args, keeping the lock FD as `restartInPlace` does. The rolled-back old binary starts
normally (step 2).

Every rollback (this one, the watchdog's, and `lana update rollback`) also sets `skipped` to the
failed version, so the dashboard offers only a newer release.

**Watchdog.** The trial boot runs inside the new binary, so it cannot catch a binary that dies before
`main`, has the wrong architecture, is killed, or hangs. Right before the install hook execs the new
binary, the old binary starts `<backup_dir>/lana update-watch --home <home> --pid <daemon pid> --
<daemon args>` in its own session (stdin `/dev/null`, the daemon's stdout and stderr, every other
descriptor close-on-exec, no `LANA_RESTART_LOCK_FD`). That command starts a detached copy of itself
and exits, so the daemon reaps it before the exec. The watchdog polls every 250 ms until
`pending.json` and every claim file are gone (the trial boot recorded `updated`, `rolled_back` or
`failed`: exit). When the daemon pid is gone (`kill(pid, 0)` fails or it is a zombie), or 90 s pass
without healthy, it claims `pending.json`, stops the daemon on timeout (SIGTERM, SIGKILL after 10 s),
runs the same restore, records `rolled_back` with the reason ("<to> stopped during its first start
before it reported healthy" or "<to> did not report healthy within 90s"; the exit code is not known,
since the watchdog is not the parent), and then starts `exe_path` with the daemon args, detached,
once the daemon lock is free within 5 s. When another process (launchd KeepAlive) holds the lock, it
starts nothing. A failed spawn is a warning; the trial boot still covers Go-level failures.

**Claim.** Whoever records the outcome first renames `pending.json` to
`pending.claim-<pid>.json`; a failed rename (the file is gone) means another process owns the
outcome and backs off. The trial rollback, `markHealthy`, and the watchdog all claim, so they never
both restore or record `updated` after a rollback started. The watchdog also takes over the claim of
the dead daemon pid. At daemon start, a claim of another live process stops the start ("an update
rollback is in progress"); a claim of a dead process turns back into `pending.json` with `boots = 1`.

`lana update rollback` runs the same restore from the newest backup without exec, after taking the
daemon lock (exit 3 when it is held).

## Web

`web.UpdateStore`:

```go
type UpdateStore interface {
    UpdateStatus(ctx context.Context) UpdateStatus
    CheckUpdate(ctx context.Context) (UpdateStatus, error)          // ErrUpdateInvalidState
    ApplyUpdate(ctx context.Context, version string, force bool) (UpdateStatus, error) // ErrUpdateInvalidState, ErrUpdateUnavailable
    CancelUpdate(ctx context.Context) (bool, error)
    SkipUpdate(ctx context.Context, version string) (UpdateStatus, error)
    UpdateSettings(ctx context.Context, channel *string, check *bool) (UpdateStatus, error)
    ClearUpdateResult(ctx context.Context) error
}
```

The handlers mirror `web/daemon.go`. `UpdateStatus` and its parts are JSON structs in `web/update.go`
matching API-UPD-002. Settings changes write `config.toml` through the same config file writer the
existing settings endpoints use.

## UI

- `api/update.ts`: typed calls for API-UPD-003..009.
- `update-card.tsx`: mounted once in `App.tsx` after sign-in. Polls `GET /v1/update` every 5 min
  when idle and every 1 s while a job runs. Hidden when `available` is null, `skipped` equals it, or
  **Later** was clicked (stored in `sessionStorage`, wrapped in try/catch). States: offer (version,
  notes rendered with the existing markdown renderer, upgrade-notes callout, **Update now**,
  **Later**, **Skip this version**), progress (step list with the active step, bytes during
  download, waiting turns with **Interrupt and update**, **Cancel**), restarting (reuse the restart
  wait in `api/daemon.ts` until `started_at` changes), result (`last_result`: success, rollback, or
  failure, with **Dismiss** calling `DELETE /v1/update/result`), manual (`can_apply` false: reason and
  copyable `manual_command`).
- Desktop: fixed bottom-right, width 360 px. Mobile: bottom sheet with 16 px gutters. Gr8r tokens
  and components only; light and dark themes.
- Settings: an **Updates** section next to the daemon/restart controls with version, channel select,
  automatic check switch, last check time, **Check now**, and the same Update button.

## Verification

- `internal/selfupdate`: table tests for semver, channel filter, asset selection, checksum parse,
  eligibility, backup/restore with temp dirs, a fake GitHub with `httptest`.
- `cmd/lana`: job state machine with a fake runner; trial-boot decisions; rollback restore; the
  pre-exec hook runs only for update restarts.
- `web`: handler tests like `web/daemon_test.go`.
- UI: vitest for the card states.
- End to end: build `0.1.0-beta.1` and `0.1.0` binaries, serve them from a fake GitHub
  (`LANA_UPDATE_API`), click **Update now** in the dashboard, confirm the new version runs and the
  sessions are still there; repeat with a broken new binary and confirm the rollback.
