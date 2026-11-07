# Tasks: Self-update

- **Status**: In Progress
- **Design**: [design.md](design.md)

| ID | Slice | Covers | Evidence |
|---|---|---|---|
| T-UPD-01 | `internal/selfupdate`: semver, release client, channel filter, download, checksum, eligibility, backup/restore, state files | REQ-UPD-001, 005, 006 | — |
| T-UPD-02 | `config` `[update]` section | API-UPD-001 | — |
| T-UPD-03 | `cmd/lana/update.go`: job state machine, periodic check, pre-exec install hook, trial boot and rollback, `update rollback`, `update-probe` | REQ-UPD-003, 004, 006 | — |
| T-UPD-04 | `web/update.go` handlers and `UpdateStore` wiring | API-UPD-002..009 | — |
| T-UPD-05 | `install.sh` works without auth for the public repo | REQ-UPD-005 | — |
| T-UPD-06 | UI: `api/update.ts`, update card, Settings section, `make web` | REQ-UPD-002, 007 | — |
| T-UPD-07 | End-to-end: update and rollback with a fake GitHub | AC-UPD-007..012 | — |
| T-UPD-08 | Docs: `README.md` Upgrade section, `CHANGELOG.md` Unreleased | — | — |
