# Requirements: Self-update from the web dashboard

- **Issue**: — (owner request 2026-11-08)
- **Status**: Approved (owner interview 2026-11-08; remaining details are agent recommendations)
- **Revision**: 1
- **Owner**: ridho
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Related ADR**: —
- **Related diagram**: —

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request | "ada update beta 2 … muncul popup float di web, ketika di klik bisa update … memudahkan dan tidak merusak yang sudah ada" | goal, popup, one-click update, safety |
| Owner interview | 2026-11-08: repo is public; offer stable releases only by default; download only after the click; build on a new branch | channel, download timing, auth |
| Spec 16 | [turn feedback and graceful restart](../16-turn-feedback-graceful-restart/requirements.md) | drain and restart in place |
| Releasing | [docs/releasing.md](../../releasing.md), `.github/workflows/release.yml` | release assets, `SHA256SUMS`, prerelease flag |

## Goal

When a newer lana release is published, the owner sees a small floating card in the web dashboard. One
click installs it: the daemon downloads and verifies the new binary, backs up the data, waits for
running turns, and restarts on the new version. The update never leaves lana broken: when the new
version cannot start, lana goes back to the old binary and the old data on its own and says why.

## Actors

- **Owner** — the signed-in dashboard user. Only the owner can check for, start, cancel, or skip an
  update.
- **Daemon** — `lana daemon`, which checks GitHub Releases, runs the update, and restarts itself.

## Definitions

- **Release** — a published GitHub Release of `ridhomujizat/lana-harness` with the assets
  `lana-<os>-<arch>` and `SHA256SUMS`.
- **Channel** — which releases are offered. `stable` offers releases that GitHub does not mark as
  prerelease. `beta` offers every release, prereleases included.
- **Update job** — one run of the update pipeline for one target version.
- **Trial boot** — the first start of the new binary after an update, before it reports healthy.
- **Rollback** — putting back the previous binary and the backed-up data after a failed trial boot.

## Scope

### Included

- Periodic and manual check for a newer release on the configured channel.
- A floating update card on every dashboard page, and an Updates section in Settings.
- One-click update: download, checksum check, a probe run of the new binary, drain, backup, swap,
  restart in place.
- Automatic rollback when the new binary fails its trial boot.
- `lana update rollback` to restore the previous version by hand while the daemon is stopped.
- `install.sh` works without GitHub auth now that the repository is public.

### Not included

- Windows (restart in place is not supported there).
- Downgrading to an older release from the dashboard.
- Updating a binary that a package manager owns (Nix store, Homebrew Cellar). The card shows the
  manual command instead.
- Delta or background downloads.

## Behavior rules

### REQ-UPD-001: Check for a newer release

The daemon shall check GitHub Releases 60 seconds after start and then every 6 hours while
`update.check` is true, and on demand from the dashboard. It shall pick the highest semantic version on
the channel that is newer than the running version and has both the platform asset and `SHA256SUMS`.
Drafts are ignored. A build whose version is `dev` shall not check and reports updates as unsupported.

- `AC-UPD-001`: Given lana `0.1.0-beta.1` on channel `stable` and releases `v0.1.0-beta.2`
  (prerelease) and `v0.1.0` (stable), when the daemon checks, then `0.1.0` is available.
- `AC-UPD-002`: Given channel `beta` and the same releases plus `v0.2.0-beta.1`, then `0.2.0-beta.1`
  is available.
- `AC-UPD-003`: Given the newest release has no asset for the platform, then it is skipped and the
  next eligible release is offered, or none.
- `AC-UPD-004`: Given GitHub is unreachable, then the status keeps the last result and reports the
  check error; the daemon keeps running.

### REQ-UPD-002: Floating update card

When an update is available, not skipped, and the owner is signed in, the dashboard shall show a
floating card in a corner of every page with the version, the release notes, and the buttons
**Update now** and **Later**. **Later** hides the card until the next browser session. **Skip this
version** hides it for that version until a newer one appears. The card shall call out a release whose
notes contain "Upgrade notes".

- `AC-UPD-005`: Given an available update, when any page loads, then the card shows within 5 seconds
  without a page reload.
- `AC-UPD-006`: Given the owner skipped `0.1.0`, then the card does not show for `0.1.0` and shows
  again for `0.1.1`.

### REQ-UPD-003: One-click update

**Update now** shall run the update job and show its progress in the card: downloading (with bytes),
verifying, checking the new version, waiting for running turns, installing, restarting. The job
shall:

1. download the asset into `$LANA_HOME/updates/`,
2. verify its SHA-256 against `SHA256SUMS`, and run `<new> --version`, which must print the target
   version,
3. run the new binary in probe mode against a copy of the data, which loads the config and opens and
   migrates the copied databases; the live data is not touched,
4. wait for running turns with the existing restart drain (spec 16), or interrupt them when the owner
   chose **Update and interrupt**,
5. back up the databases, `config.toml`, and the current binary into a backup folder,
6. replace the executable atomically and restart in place.

A failure in steps 1–3 shall leave the running daemon and its data unchanged and show the reason.
The owner can cancel during steps 1–4.

- `AC-UPD-007`: Given a corrupted download, then the job fails at verifying and the daemon keeps
  running the old version.
- `AC-UPD-008`: Given a new binary whose probe exits nonzero, then the job fails at checking with
  the last lines of its stderr, and nothing in `$LANA_HOME` except `updates/` changed.
- `AC-UPD-009`: Given two running turns, when the owner clicks **Update now**, then the card shows
  that it waits for 2 turns, and the restart happens after they finish or at the drain deadline.
- `AC-UPD-010`: Given a successful update, then after the restart the dashboard reconnects by
  itself and the card says "Updated to <version>".

### REQ-UPD-004: Never leave lana broken

The new binary shall treat its first start after an update as a trial boot. If it fails to start
before the web server serves and channels run, or a previous trial boot of the same version never
reported healthy, it shall restore the backed-up databases and the previous binary and restart into
it. The old version then shows "Update to <version> failed and was rolled back" with the reason.
The failed version is skipped; a newer release is offered again.

- `AC-UPD-011`: Given a new binary that exits with a config error at start, then the old binary is
  running again within 30 seconds, the databases match the backup, and the card reports the rollback.
- `AC-UPD-012`: Given a trial boot that was killed before it reported healthy, when the owner starts
  the daemon again, then it rolls back instead of trying the same version again.

### REQ-UPD-005: Manual install fallback

When the daemon cannot replace its own binary (path not writable, inside `/nix/store` or a Homebrew
Cellar, unsupported platform), the card shall show the reason and the manual install command instead
of **Update now**.

### REQ-UPD-006: Backups and rollback by hand

The daemon shall keep the newest 2 update backups in `$LANA_HOME/updates/backups/`.
`lana update rollback` shall restore the newest backup (binary, databases, config) while the daemon is
stopped, and refuse while it runs.

### REQ-UPD-007: Settings

Settings shall show the running version, the channel (`stable`/`beta`), the automatic check switch,
the last check time, and **Check now**. Changing the channel takes effect without a restart and is
saved in `config.toml` under `[update]`.

## Resolved questions

| Question | Resolution |
|---|---|
| GitHub auth | Not needed: the repository is public (owner, 2026-11-08). `GITHUB_TOKEN` is sent when set, for rate limits only. |
| Channel | `stable` by default (owner). `beta` is a setting. |
| Download timing | Only after the click (owner). |
| Check interval | Rekomendasi agent: 60 s after start, then every 6 h. |
| Running turns | Rekomendasi agent: reuse the spec 16 drain (wait, deadline, interrupt; turns can be continued). |
| Backups kept | Rekomendasi agent: newest 2. |
