# Spec 84: Restart the Workbench core

- **Status**: Approved (owner 2026-11-26: "ga ada tool buat restart workbench kah dari pada restart [laptop]", then "ya")
- **Revision**: 1
- **Branch**: feature/workbench-restart
- **Related**: spec 55 (Workbench core), spec 60 (restore), spec 82 / 83 (Switch here, `core_outdated`)

## Context

The Workbench core (`lana workbench serve`, socket `<LANA_HOME>/workbench/core.sock`) owns every terminal pane
and outlives daemon restarts and updates (REQ-WB-003). After an update the old core keeps running, so features
gated by `Hello.Features` stay off (Switch here answers `core_outdated`) until the owner restarts the laptop or
runs `lana workbench stop`. `stop` sends the `shutdown` op: the core freezes its persister and kills its panes
without marking them exited, so the next core restores every pane (spec 60: shells, claude/agy resumed on their
account). This spec gives the owner a button for that.

## Requirements

### REQ-WBR-001: The core reports its build

The core's Hello carries `app_version` and `commit`, the values `lana version` shows (`version` stays the
protocol number). An older core sends neither.

### REQ-WBR-002: GET /v1/workbench/core

Answers `{running, version, commit, daemon_version, daemon_commit, outdated, sessions}`. It never starts a
core: with none running, `running` is false and `outdated` false (the next start uses the daemon's build).
`outdated` is true when a running core's version or commit differs from the daemon's, or the core sends no
version. `sessions` is the core's pane count.

### REQ-WBR-003: POST /v1/workbench/core/restart

Authenticated, CSRF-checked and refused under locked remote control like the other Workbench writes; audited as
`workbench.core-restart`. The daemon sends `shutdown`, waits up to 10 s for the old core to stop (socket gone,
`core.pid` no longer its pid, process gone), starts a new core the way it starts one on demand, waits for its
Hello, and answers the REQ-WBR-002 body. Other Workbench calls wait for the restart instead of starting a core
of their own. A second restart while one runs is `409 restart_running`; a core that does not stop or come back
is `503 workbench_unavailable` with a short message. Panes come back through spec 60's restore.

### REQ-WBR-004: Clients reconnect

The Workbench event stream ends with the old core and the browser reconnects to the new one; restored panes have
new pids, so their terminals attach again. After a restart the Workbench page also reloads its sessions and
layout, so it recovers without a page reload.

### REQ-WBR-005: Settings

Settings → General → Workbench has a **Workbench core** row: the core's version (or "Not running"), and when
outdated a badge "Outdated" with "Restart to use the new version (<daemon_version>)". **Restart Workbench** asks
first: "All terminal panes close and reopen. Claude Code and Antigravity conversations resume; a running turn
stops." It shows progress, then "Restarted. N panes restored." or the error.

### REQ-WBR-006: Workbench notice and Accounts popup

While the core is outdated the Workbench shows one slim dismissible notice at the top (tint, no colored left
bar): "The Workbench is running an older version. Restart it to use the new features." with **Restart** (the
same confirm). In the Accounts popup, Switch here's tooltip then reads "Restart the Workbench to switch accounts
in place" and the popup offers **Restart Workbench**.

## Out of scope

Restarting the core automatically after an update; restarting a single pane (exists, REQ-WB-040).
