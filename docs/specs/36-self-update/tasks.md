# Tasks: Self-update

- **Status**: Completed 2026-11-08 · branch `feature/self-update`
- **Design**: [design.md](design.md)

| ID | Slice | Covers | Evidence |
|---|---|---|---|
| T-UPD-01 | `internal/selfupdate`: semver, release client, channel filter, download, checksum, eligibility, backup/restore, state files | REQ-UPD-001, 005, 006 | `go test ./internal/selfupdate` ok (incl. claim race, -race) |
| T-UPD-02 | `config` `[update]` section | API-UPD-001 | `go test ./config` ok |
| T-UPD-03 | `cmd/lana/update.go`: job state machine, periodic check, pre-exec install hook, trial boot and rollback, `update rollback`, `update-probe` | REQ-UPD-003, 004, 006 | `go test ./cmd/lana` ok; watchdog `update-watch` added after e2e found a dead daemon when the new binary could not run its own rollback |
| T-UPD-04 | `web/update.go` handlers and `UpdateStore` wiring | API-UPD-002..009 | `go test ./web` ok |
| T-UPD-05 | `install.sh` works without auth for the public repo | REQ-UPD-005 | installed v0.1.0-beta.1 from the public repo without auth; `bash -n install.sh` |
| T-UPD-06 | UI: `api/update.ts`, update card, Settings section, `make web` | REQ-UPD-002, 007 | vitest 324/324, `tsc --noEmit`, `make web-check`; screenshots of the card on the real daemon |
| T-UPD-07 | End-to-end: update and rollback with a fake GitHub | AC-UPD-007..012 | fake GitHub e2e: 0.1.0-beta.1 → 0.1.0 updated in place (same pid, backup made, browser session kept); stable channel skipped 0.1.0-beta.2; broken 0.1.1 (exits 2) rolled back by the watchdog in ~1 s, old daemon restarted, 0.1.1 skipped; `lana update rollback` restored by hand |
| T-UPD-08 | Docs: `README.md` Upgrade section, `CHANGELOG.md` Unreleased | — | README Upgrade section, CHANGELOG Unreleased |
