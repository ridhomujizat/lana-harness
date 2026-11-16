# Spec 55: Workbench — persistent terminals in Lana

- **Status**: Approved (owner, 2026-11-16: "sip lanjut implement ini")
- **Branch**: `feature/workbench`, from `development` (0.5.0-beta.5, 7ab97f0)
- **Design**: figcraft page "Workbench (agent terminal)" in `design/lana-design-system.figcraft`, frames "Lana web · Workbench (sxored-hub)" and "Lana web · mobile – …". The "MyGo ·" frames are for a later native app and are out of scope.

## Problem

Coding agents (Claude Code, Codex) and dev servers run in terminals the owner cannot see from Lana, cannot reach from the phone, and lose when the process that started them restarts. The owner wants a herdr-style workbench inside Lana: terminals that stay alive, grouped in spaces, visible and controllable from the desktop app and the browser, rendered exactly as a real terminal.

## Decisions (owner, 2026-11-16)

1. The feature is called **Workbench** (sidebar label "Workbench", not "Terminal").
2. Web first: the Lana web UI (also the desktop app's WKWebView) renders terminals with xterm.js, so a session looks the same as in a native terminal and works remotely. A MyGo native app may come later on the same core.
3. The terminal core lives in lana-harness for now, but runs as its **own process**, so sessions survive restarts and upgrades of the Lana daemon.
4. Layout follows herdr: a panel with **Spaces** (top, `+` opens a new terminal) and **Agents** (bottom, grouped by space or by status), then tabs, then split panes.
5. Mobile: one pane at a time, a scrollable tab strip, an all-tabs sheet, read-only by default with **Take over** to type, and an extra key bar.

## Architecture

```
lana workbench serve   (separate process, detached; owns every pty)
   │  unix socket $LANA_HOME/workbench/core.sock (0600), protocol v1
lana daemon            (web package: /v1/workbench/* + WebSocket attach)
   │  HTTP + SSE + WebSocket, existing auth
Lana web UI            (Workbench page, xterm.js)
```

## Requirements

### Core process (`internal/workbench`)

- **REQ-WB-001** `lana workbench serve` runs the core in the foreground. It listens on `$LANA_HOME/workbench/core.sock` with mode 0600 and writes `core.pid` and `core.log` in the same directory. A second `serve` while one is running exits with an error and leaves the running one alone.
- **REQ-WB-002** `workbench.Ensure(ctx, home)` returns a connected client. When no core answers on the socket, it starts `<lana executable> workbench serve` detached (new session, stdio to `core.log`, not a child that dies with the caller) and waits up to 3 s for the socket. A stale socket file with no listener is removed first.
- **REQ-WB-003** The core keeps running with no clients attached. Stopping the Lana daemon does not stop the core or its sessions. `lana workbench stop` asks the core to shut down; shutting down kills every session.
- **REQ-WB-004** The protocol has a version. `hello` returns `{version: 1, pid, started}`. A client that gets a different major version reports `workbench_version_mismatch` and does not use the core.

### Sessions

- **REQ-WB-010** `create` starts a program in a new pty. Options: `command` (argv; default the user's login shell `$SHELL -l`, fallback `/bin/zsh` then `/bin/sh`), `dir` (default `$HOME`), `env` (extra entries), `cols`/`rows` (default 120×36), `title`, `labels` (string map). The environment adds `TERM=xterm-256color`, `COLORTERM=truecolor`, and `LANA_WORKBENCH_SESSION=<id>`.
- **REQ-WB-011** A session has: `id` (12 hex chars), `title`, `command`, `dir`, `labels`, `pid`, `created`, `cols`, `rows`, `program` (name of the foreground process of the pty, e.g. `claude`, `lazygit`, `zsh`), `cwd` (Linux only; empty elsewhere), `state`, `exit_code`, `needs_you`, `last_output`, `attached` (count of control and view attachments).
- **REQ-WB-012** `state` is `working` when the session printed output in the last 2 s, `idle` otherwise, and `exited` after the program ended. `needs_you` turns true when the program rings the bell (BEL outside an OSC string) or exits while no control client is attached; it turns false when a control client attaches.
- **REQ-WB-013** The foreground program is read every second from the pty's foreground process group: `tcgetpgrp` on the pty, then the process name (`kern.proc.pid` sysctl `p_comm` on macOS, `/proc/<pid>/comm` on Linux). The working directory comes from `/proc/<pid>/cwd` on Linux.
- **REQ-WB-014** An exited session stays listed (so the UI can show "exited 0" and offer Restart) until it is closed. `kill` on a running session sends SIGHUP to its process group, then SIGKILL after 2 s, and removes it once the process is gone. `kill` on an exited session removes it.
- **REQ-WB-015** `restart` of an exited session starts the same command, dir, env, title and labels again under the same id.
- **REQ-WB-016** `resize` sets the pty window size. The core keeps the last size per session.
- **REQ-WB-017** Each session keeps the last 1 MiB of output in a ring buffer.

### Attach

- **REQ-WB-020** `attach {id, mode}` turns the connection into a raw byte stream after a one-line JSON OK reply. The core first writes the ring buffer, then live output. `mode` is `control` or `view`.
- **REQ-WB-021** Bytes the client writes go to the pty only in `control` mode; the core drops them in `view` mode.
- **REQ-WB-022** When a control client attaches, the core forces a redraw of full-screen programs: it resizes the pty to rows−1 and back to rows (two SIGWINCH), so the client's screen matches the program after the replay.
- **REQ-WB-023** Any number of clients may attach to one session in either mode. A slow client never blocks the pty: each attachment has a bounded queue (256 chunks); when it is full the core closes that attachment.
- **REQ-WB-024** The attach stream ends when the session exits (after its last output) or is killed.

### Events and layout

- **REQ-WB-030** `subscribe` turns the connection into a stream of JSON-line events: `session.created`, `session.updated` (state, program, title, needs_you, exit, size or attached changed; at most 2 per second per session), `session.removed`, `layout.changed`. Each event carries the full session object (or the layout).
- **REQ-WB-031** `layout.get` / `layout.set` read and replace an opaque JSON document (max 256 KiB) stored in `$LANA_HOME/workbench/layout.json` (written atomically). The core does not interpret it; the web UI keeps spaces, tabs and splits there so every client shows the same layout.

### Web API (`web` package, existing auth)

- **REQ-WB-040** Routes, all behind `s.Handle` (Host, Origin, cookie, CSRF):
  - `GET /v1/workbench/sessions` → `{sessions: [...]}`
  - `POST /v1/workbench/sessions` `{command?, dir?, cols?, rows?, title?, labels?}` → session
  - `DELETE /v1/workbench/sessions/{id}`
  - `POST /v1/workbench/sessions/{id}/restart`
  - `POST /v1/workbench/sessions/{id}/resize` `{cols, rows}`
  - `GET /v1/workbench/layout` → the layout JSON (`{}` when none); `PUT /v1/workbench/layout`
  - `GET /v1/workbench/events` → SSE of the core events (`event: <type>`, `data: <json>`), with the existing heartbeat
- **REQ-WB-041** `GET /v1/workbench/sessions/{id}/attach?mode=view|control` upgrades to a WebSocket. Because the upgrade is a GET, the handler checks `Origin` itself: it must equal `http://<web addr>`, else 403 `forbidden_origin`. Server → client: binary frames with terminal output. Client → server: binary frames are input (dropped in view mode); text frames are JSON control messages, `{"type":"resize","cols":N,"rows":N}` (control mode only).
- **REQ-WB-042** When the core cannot be reached or started, the routes return 503 `workbench_unavailable` with the reason.
- **REQ-WB-043** Every control attach and every session create, kill and restart is written to the audit log with the session id and the browser session.

### Web UI

- **REQ-WB-050** The sidebar gets a **Workbench** item (Lucide `terminal` icon) under Workflows, with an orange count of sessions that have `needs_you`.
- **REQ-WB-051** The Workbench page (desktop) has the panel (Spaces + Agents), a tab strip, and split panes, as in the design. `+` (Spaces header, tab strip) opens a new shell in the current space. A space has a name, a colour tile and a directory; new terminals in it start in that directory.
- **REQ-WB-052** Agents lists sessions whose `program` is a known agent (`claude`, `codex`, `jcode`, `opencode`, `gemini`) or that carry a `lana.agent` label. Toggle: group by space, or by status in the order needs you (blocked), working, idle. Each row: agent icon, title, age, status dot, and the Lana agent avatar when `lana.agent` is set.
- **REQ-WB-053** Each pane renders with xterm.js (WebGL renderer, fit, unicode11, web links), the Geist Mono font and the design's dark theme. The pane header shows program icon, title, Lana chip, status, and split right / split down / zoom / close.
- **REQ-WB-054** Size: the pane that has focus and control sends its size. On the desktop every pane attaches in control mode.
- **REQ-WB-055** Mobile (≤ 640 px): Workbench home (Spaces + Agents), a space view with a scrollable tab strip, a tab-count button that opens the all-tabs sheet (cards with status, sorted by status), one pane at a time with a pane switcher, and read-only by default. **Take over** switches to control mode and shows the key bar (`esc`, `tab`, `ctrl` toggle, `⌃C`, arrows, `/`).

## Out of scope

Lana agents starting their runs inside Workbench sessions (next spec), a headless VT emulator for exact snapshots, Windows, remote access beyond the existing loopback/tunnel setup, the MyGo app.
