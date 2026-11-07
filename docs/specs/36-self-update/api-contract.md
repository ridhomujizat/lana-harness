# API contract: Self-update

- **Status**: Approved (agent recommendation, 2026-11-08)
- **Requirements**: [requirements.md](requirements.md)

All HTTP endpoints are registered through `Server.Handle`, so the host, origin, cookie, and CSRF checks
apply. Errors use the existing JSON error shape of the web package (`{"error":{"code","message"}}`).
Times are unix milliseconds.

## API-UPD-001: Config

```toml
[update]
channel = "stable"   # "stable" | "beta"; default "stable"
check = true         # periodic check; default true
```

Validation: any other `channel` value is a config error `update.channel: must be "stable" or "beta"`.

Environment overrides (tests and forks only):

| Variable | Default | Meaning |
|---|---|---|
| `LANA_UPDATE_API` | `https://api.github.com` | GitHub REST base URL |
| `LANA_UPDATE_REPO` | `ridhomujizat/lana-harness` | `owner/name` |
| `GITHUB_TOKEN` | — | sent as `Authorization: Bearer` when set |

## API-UPD-002: UpdateStatus

Returned by every endpoint below unless stated otherwise.

```json
{
  "current": "0.1.0-beta.1",
  "channel": "stable",
  "check": true,
  "supported": true,
  "can_apply": true,
  "manual_reason": "",
  "manual_command": "curl -fsSL https://raw.githubusercontent.com/ridhomujizat/lana-harness/development/install.sh | bash",
  "checked_at": 1762600000000,
  "check_error": "",
  "available": {
    "version": "0.1.0",
    "tag": "v0.1.0",
    "name": "lana 0.1.0",
    "published_at": 1762590000000,
    "notes": "### Fixed\n- ...",
    "url": "https://github.com/ridhomujizat/lana-harness/releases/tag/v0.1.0",
    "size": 31000000,
    "prerelease": false,
    "upgrade_notes": false
  },
  "skipped": "",
  "job": {
    "state": "downloading",
    "version": "0.1.0",
    "progress": { "done": 1200000, "total": 31000000 },
    "running_turns": 0,
    "deadline": null,
    "error": ""
  },
  "last_result": {
    "outcome": "updated",
    "from": "0.1.0-beta.1",
    "to": "0.1.0",
    "reason": "",
    "at": 1762600100000
  }
}
```

- `supported` is false for a `dev` build or an unsupported OS; then `available` and `job` are null.
- `can_apply` is false when the binary cannot be replaced; `manual_reason` says why
  (for example `"lana runs from /nix/store, which is read-only"`).
- `checked_at` is null before the first check. `check_error` is empty after a good check.
- `available` is null when nothing newer exists on the channel.
- `upgrade_notes` is true when `notes` contains a heading `Upgrade notes`.
- `job` is null when no job ran since the daemon started. `job.state` is one of
  `downloading`, `verifying`, `probing`, `waiting`, `installing`, `restarting`, `failed`,
  `cancelled`. `progress` is set only while `downloading` (`total` is 0 when unknown).
  `running_turns` and `deadline` are set only while `waiting`. `error` is set only when `failed`.
- `last_result` is the outcome of the last update across restarts, or null after it was cleared.
  `outcome` is `updated`, `rolled_back`, or `failed`.

## API-UPD-003: `GET /v1/update`

200 UpdateStatus. Never calls GitHub.

## API-UPD-004: `POST /v1/update/check`

Checks GitHub now (timeout 15 s) and returns 200 UpdateStatus. A GitHub failure is still 200, with
`check_error` set. 409 `invalid_state` while a job is between `downloading` and `restarting`.

## API-UPD-005: `POST /v1/update/apply`

Body: `{"version": "0.1.0", "confirm": true, "force": false}`.
`force` interrupts running turns instead of waiting for them.

- 202 UpdateStatus with `job.state` = `downloading`.
- 400 `invalid_request` when `confirm` is not true or the body is invalid.
- While the job for the same `version` is `waiting`, `force: true` interrupts the running turns
  (`core.ErrInterruptedRestart`), lets the drain complete, and answers 202 with the current status.
- 409 `invalid_state` when a job is active (except the `waiting` + `force` case above), or `version`
  is not `available.version`.
- 409 `update_unavailable` when `supported` or `can_apply` is false.

## API-UPD-006: `DELETE /v1/update/apply`

Cancels a job in `downloading`, `verifying`, `probing`, or `waiting` (also cancels the restart drain).
200 `{"cancelled": true}`, or `{"cancelled": false}` when no job could be cancelled.

## API-UPD-007: `POST /v1/update/skip`

Body: `{"version": "0.1.0"}`. Saves the skipped version in `$LANA_HOME/updates/state.json`.
200 UpdateStatus. `{"version": ""}` clears it.

## API-UPD-008: `PATCH /v1/update/settings`

Body: `{"channel": "beta", "check": true}` (both optional). Writes `[update]` in `config.toml`,
applies the change live, and re-checks when the channel changed. 200 UpdateStatus. 400
`invalid_request` for an unknown channel.

## API-UPD-009: `DELETE /v1/update/result`

Clears `last_result`. 204.

## API-UPD-010: CLI

| Command | Behavior | Exit |
|---|---|---|
| `lana update rollback` | Restores the newest backup in `$LANA_HOME/updates/backups/` (binary, databases, config). Refuses while the daemon holds its lock. | 0 restored; 1 no backup or failure; 3 daemon running |
| `lana update-probe --home <dir>` | Hidden. Loads config from `<dir>` and opens and migrates the databases in `<dir>`, then exits. Starts no channels, web server, or turns. | 0 ok; nonzero with the reason on stderr |
| `lana update-watch --home <dir> --pid <pid> -- <daemon args...>` | Hidden. The old binary runs it from `<backup_dir>/lana` right before it execs the new one. It detaches a copy of itself, waits for the update of daemon `<pid>` to end, and rolls back when the pid dies or 90 s pass without healthy, then starts `exe_path` with `<daemon args>` when the daemon lock is free (design.md, Trial boot and rollback). | 0 healthy, handled, or rolled back; 1 rollback failed; 2 usage |

`lana --version` output stays as it is.

## API-UPD-011: Files

```text
$LANA_HOME/updates/
├── state.json              # {"skipped": "...", "last_result": {...}|null}
├── pending.json            # present only during a trial boot
├── pending.claim-<pid>.json # pending.json claimed by <pid> while it records the outcome
├── lana-<version>.download # partial or verified download
└── backups/
    └── <from>-to-<to>-<yyyymmddThhmmssZ>/
        ├── lana            # previous binary
        ├── lana.db         # VACUUM INTO copies of every *.db in $LANA_HOME
        ├── knowledge.db
        ├── config.toml
        └── manifest.json   # {"from","to","exe_path","created_at","files":[...]}
```

`pending.json`: `{"from","to","exe_path","backup_dir","boots": 0,"created_at"}`.
