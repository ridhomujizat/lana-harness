# Requirements: Web view: settings and chat sessions

- **Issue**: —
- **Status**: Draft
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (`[web]` config, browser login, `/v1` session, settings, secrets, agent-file and skill endpoints, SSE stream)
- **Related ADR**: [ADR Index](../../adr/README.md) — [0003](../../adr/0003-multi-agent-with-channel-routing.md), [0004](../../adr/0004-permission-mode-per-agent.md), [0008](../../adr/0008-cli-channel-http-and-web-dashboard-interfaces.md), [0009](../../adr/0009-session-as-append-only-event-log.md), [0010](../../adr/0010-profile-and-config-patch.md), [0012](../../adr/0012-acp-and-sdk-json-rpc.md), [0014](../../adr/0014-integration-as-agentic-tool.md), [0015](../../adr/0015-embeddable-runtime-for-services.md). A new ADR for the browser and HTTP auth model is required before design approval (see `Q-002`, `Q-003`).
- **Related diagram**: [Diagram Index](../../diagram/README.md) — [0001](../../diagram/0001-system-context-and-roadmap.md) (M5/M6 roadmap: REST + SSE, `embed.FS` dashboard), [0002](../../diagram/0002-agent-loop-and-approval.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-30 | Draft spec for a web view served by `lana daemon`: settings and chat sessions | goal and scope |
| ADR 0008 | Web dashboard pages (chat, sessions, approvals, settings-like pages), loopback `127.0.0.1:18300`, SSE, modal approvals, Preact + `htm` with no build step, `embed` assets | interface, port, assets |
| ADR 0014 | REST + SSE endpoints under `/v1`, `POST /v1/approvals/{id}`, `GET /v1/agents`; bearer tokens per client (`[[api.tokens]]`) | endpoint shape; auth conflict (`Q-002`, `Q-003`) |
| ADR 0004 | Policy `trust`, `ask`, `never`; outcomes `allowed_once`, `allowed_session`, `rejected`, `cancelled`, `unavailable`; fail-closed; 10-minute timeout | approvals, policy |
| ADR 0009 | Session = append-only event log; history derived from events; fork only at turn boundary; resume | history, fork |
| ADR 0003 | Routes pick the agent; one inbox per session | new session, inbox |
| ADR 0010 | `daemon` profile; layered config; `lana config check` | config validation, `[web]` section |
| ADR 0015 | Library packages do not import `channels/`, `web/`, `protocol/` | package boundary |
| Spec #1 | Shared conventions (exit codes, `LANA_HOME`, secrets in `secrets.env`, `"***"`, ULID session IDs, snake_case JSON, ms times); `REQ-SESS-002` session lock; `REQ-CONF-001` config commands | conventions, leases |
| Spec #2 | `secrets.env` lookup order (process env wins); `core.Secret` | secret handling |
| Spec #5 | Agent home `$LANA_HOME/agents/<name>/` with `AGENT.md`, `USER.md`, `MEMORY.md`; limits 2,000 / 4,000 characters; files read every turn (`REQ-PROMPT-002`) | agent files |
| Spec #6 | Daemon (`REQ-DMN-*`), Telegram session rules (`REQ-TG-*`), approval keyboard, `/status`, `/compact`, `/cancel` behavior, Telegram session workspace (`Q-007`) | daemon, shared lease, command parity |
| Spec #7 | Skill files, frontmatter, `lana skill` validation | skills |
| Code (read 2026-10-30) | `lana.go`, `lana_channel.go`, `cmd/lana/daemon.go`, `cmd/lana/lease.go`, `channels/telegram/channel.go`, `core/config.go`, `config/secrets.go`, `internal/agenthome` | feasibility; gaps listed in `design.md` |

Anything not supported by a source or an explicit approval goes into Blocking Open Questions. Rules that
depend on an open question are marked `(pending Q-0NN)` and must not be implemented before the answer.

## Goal

The owner runs `lana daemon` on their own machine and opens lana in a browser on that machine. From the browser
the owner can see every chat session (terminal, Telegram, library, web), continue a session with streamed
answers, tool lines, and approvals, and change lana's settings (providers, agents, routes, channels, MCP servers,
secrets, agent files, and skills) without editing files by hand. The daemon keeps running while the owner works.

## Actors

- **Owner** — the only human user. Logs in with the owner token. May read and change every setting shown in the
  web view and may answer every approval of a web session.
- **Browser** — the owner's tab on the loopback address. Has no privileges beyond the owner's logged-in browser
  session. Cannot act without the CSRF token and a valid cookie.
- **Other local process** — `lana chat`, `lana run`, or another `lana` process using the same `LANA_HOME`. It may
  hold a session lease or edit files on disk. The web view must respect both.
- **Telegram users** — not web actors. Their chats appear in the web session list as `telegram` sessions and follow
  spec #6 rules.

## Definitions

- **Web view** — the HTTP server, API, and dashboard that `lana daemon` serves when `[web]` is enabled.
- **Browser session** — the login state of one browser, held in an HTTP-only cookie. Not the same as a chat session.
- **Chat session** — a lana session (ADR 0009) with channel `cli`, `telegram`, `library`, or `web`.
- **Session in use** — a session whose lease is held by another process (spec #1 `REQ-SESS-002`).
- **Own session** — a session whose lease is held by this daemon's shared lease set (Telegram chats and web sessions).
- **Live frames** — text and tool frames from `Runtime.SubscribeLive`. They exist only for turns running in the same
  process.
- **Loaded config** — the `config.toml` content the daemon read at start. Its SHA-256 is `loaded_sha256`.
- **Config version** — the SHA-256 of the `config.toml` file currently on disk. The web view sends the version it
  loaded in every save.
- **Secret** — a value in `$LANA_HOME/secrets.env` or in the process environment, named by its env name.
- **Restart required** — the state where `config.toml` on disk differs from the loaded config, or `secrets.env`
  changed after the daemon started.

## Scope

### Included

- `lana daemon` serves the web view on loopback when `[web]` is enabled (`API-WEB-001`).
- Owner login with a token, origin and host checks, and CSRF protection (`API-WEB-002`).
- Chat sessions across all channels: list, history, create, send, stream over SSE, tool lines, cancel.
- Approvals in the browser with `Allow once`, `Allow for session`, and `Reject`.
- Session actions: compact, fork, change agent, change model, change policy.
- Read-only view of effective settings, and a validated raw `config.toml` editor (`Q-009`).
- Secrets: set, unset, and list state without values (`Q-010`).
- Agent files `AGENT.md`, `USER.md`, `MEMORY.md`: read and replace.
- Skills: list, read, create, and edit.
- An audit line in the daemon log for each settings save.
- Keyboard and screen-reader support for every flow in the web view.

### Not included

- Multi-user accounts, roles, or sharing with other people.
- Access from another machine, LAN, or internet; TLS certificates; reverse proxies (`Q-001`).
- Bearer tokens for API clients and per-client `[[api.tokens]]` (ADR 0014). Only the browser login is in this spec (`Q-003`).
- An OpenAPI document. ADR 0014 requires one for the full REST API; it is a follow-up.
- Hot reload of config while the daemon runs (`Q-008`).
- Structured forms for every config key (`Q-009`).
- Deleting skills, agent files, or sessions from the web view (`Q-011`).
- The cron/scheduler page. The scheduler is spec #8 and was not written yet.
- WhatsApp, ACP, SDK, MCP server, and A2A pages.
- Editing profiles (`~/.lana/profiles/*.toml`) and `--patch` layers.
- Live token streaming for turns that run in another process (`Q-007`).
- Upload of files or images in chat.
- A mobile app and a separate native client.

## Assumptions

- One daemon process runs per `LANA_HOME`. — Source: spec #6 `REQ-DMN-002`.
- Agent files are read at the start of every turn. — Source: spec #5 `REQ-PROMPT-002`.
- Approvals are fail-closed; a tool runs only for `allowed_once` and `allowed_session`. — Source: ADR 0004.
- History is derived from the append-only event log and is the only source of truth. — Source: ADR 0009.
- A session is used by one process at a time, enforced by a lease. — Source: spec #1 `REQ-SESS-002`; spec #6 `API-TG-002`.
- Web sessions use the same agents, tools, permission policy, and rules as the CLI and Telegram. — Source: ADR 0004; spec #6 Authorization.
- The UI text is English, as in the CLI and spec #6 `Q-006`. — Source: spec #6 `Q-006` precedent.
- Config validation covers all layers and secret references as `lana config check` does. — Source: spec #1 `REQ-CONF-001`; spec #2.

## Behavior rules

### Server and assets

#### REQ-WEB-001: Web view in the daemon

When `[web]` is enabled, the system shall serve the web view and its API on `[web] addr` for as long as `lana daemon`
runs. The default address is `127.0.0.1:18300` (`Q-015`). When `[web] enabled = false`, the daemon shall not open
any web port.

- `AC-WEB-001`: Given `[web]` is enabled with the default address, when the owner runs `lana daemon`, then stderr shows `web: listening on http://127.0.0.1:18300` and `GET /` returns the dashboard with status 200.
- `AC-WEB-002`: Given `[web] enabled = false`, when the owner runs `lana daemon`, then no port is opened and stderr has no `web:` line.

#### REQ-WEB-002: Self-contained assets

The system shall embed every HTML, CSS, JavaScript, and font file of the web view in the binary, and the pages shall
load no resource from another origin. The web view shall not require Node.js at build time or at run time.

- `AC-WEB-003`: Given the built binary and no network access, when the owner opens the dashboard, then every resource loads from the loopback origin and the response sets `Content-Security-Policy` with `default-src 'self'`.

#### REQ-WEB-003: Loopback only

The system shall refuse to start the web view when `[web] addr` is not a loopback address (`127.0.0.1`, `::1`, or
`localhost`). This is `Q-001`; the agent recommendation is loopback only in v1.

- `AC-WEB-004`: Given `[web] addr = "0.0.0.0:18300"`, when the owner runs `lana daemon`, then it exits with code 2 and stderr reads `error: web: addr must be a loopback address`.

#### REQ-WEB-004: Web view without Telegram (pending Q-005)

When the web view is enabled and no `[channels.telegram]` is configured, the daemon shall run with the web view only.
It shall not exit with code 2 as spec #6 `REQ-DMN-003` requires today. This changes spec #6 and needs its revision.

- `AC-WEB-005`: Given `[web]` is enabled and there is no `[channels.telegram]`, when the owner runs `lana daemon`, then the daemon keeps running, serves the web view, and makes no Telegram API call.

### Access

#### REQ-WEB-005: Owner login

The system shall answer API calls and serve the dashboard pages that show data only to a browser that has logged in
with the owner token. Login shall set an HTTP-only cookie with `SameSite=Strict`. The cookie shall expire after 12
hours without use (`Q-002`). A request without a valid cookie shall receive `401 unauthenticated`.

- `AC-WEB-006`: Given no cookie, when a client calls `GET /v1/sessions`, then the status is 401 with code `unauthenticated`; given the correct token at login, then the status is 204 and a cookie is set; given a wrong token, then the status is 401 and no cookie is set.

#### REQ-WEB-006: Token secrecy

The owner token shall be stored on disk only as a hash. It shall never be written to logs, to `config.toml`, to a URL
after the login step, or to any response other than the one-time creation output (`Q-002`).

- `AC-WEB-007`: Given a daemon that created the token and served a login, when the owner scans the daemon log, `config.toml`, and every response body, then the plain token appears only in the one-time creation output.

#### REQ-WEB-007: Origin and host checks

The system shall reject a request whose `Host` header is not the configured loopback host and port, which blocks
DNS rebinding. It shall reject a state-changing request (`POST`, `PUT`, `PATCH`, `DELETE`) whose `Origin` header is
present and is not the dashboard origin, or that lacks the CSRF token issued at login in the `X-Lana-CSRF` header.

- `AC-WEB-008`: Given a `POST` with `Origin: https://evil.example`, when it is sent, then the status is 403 with code `forbidden_origin` and nothing changes; given a `POST` from the dashboard origin without `X-Lana-CSRF`, then the status is 403 with code `csrf_invalid`; given a `Host` header of `evil.example`, then the status is 403 with code `forbidden_host`.

### Chat sessions

#### REQ-WEB-008: Session list across channels

The system shall list chat sessions of every channel (`cli`, `telegram`, `library`, `web`), newest updated first, 50
per page. Each row shows channel, address, agent, title, status, turn count, last update time, and whether the session
is in use (`Q-006`).

- `AC-WEB-009`: Given sessions on `cli`, `telegram`, and `web` exist, when the owner opens the list, then all of them appear sorted by last update, each with its channel name, and a `before` cursor returns the next page.

#### REQ-WEB-009: History from the event log

The system shall show a session's history as turns derived from its event log (ADR 0009): user messages, assistant
answers, tool calls with their status, approval decisions, and compaction summaries. It shall not keep a second copy
of the history.

- `AC-WEB-010`: Given a Telegram session with one turn that ran a tool, when the owner opens it, then the page shows the user message, the tool call with its status, and the answer, and the events endpoint returns the same events in `seq` order.

#### REQ-WEB-010: New web session

The system shall create a web session when the owner picks an agent from the agent list and optionally a model
`<provider>:<model>`. The session shall use channel `web`, address `owner` (`Q-006`), and the workspace rule of spec
#6 `REQ-TG-005`.

- `AC-WEB-011`: Given agents `helper` and `coder`, when the owner creates a session with `coder` and model `openai:gpt-5-mini`, then a session with channel `web`, address `owner`, and agent `coder` exists and the model is set through the same runtime call the CLI uses.

#### REQ-WEB-011: Send, queue, and stream

Sending a message to an idle session shall start a turn. Sending while a turn runs shall put the message into the
session inbox (ADR 0003, spec #6 `REQ-TG-004`). The browser shall receive the turn's live frames over Server-Sent
Events in order. The final answer shall equal the last `assistant/message` event in the log.

- `AC-WEB-012`: Given an idle session of this daemon, when the owner sends `hello`, then the SSE stream yields text frames in order, ends with a turn-end event, and the final text equals the last `assistant/message` event.
- `AC-WEB-013`: Given a turn is running, when the owner sends a second message, then the request returns 202, the message is claimed at the next step of the same turn, and the page shows it as queued until it is claimed.
- `AC-WEB-032`: Given a turn is running, when the owner closes the browser tab, then the turn continues and its answer is written to the session log.

#### REQ-WEB-012: Sessions running in another process

When a session's turn runs in another process (`lana chat`, `lana run`), the browser shall show new events from the
event log by polling about once per second. It shall not show token-level text for that turn, and it shall show the
PID of the holding process (`Q-007`).

- `AC-WEB-014`: Given `lana chat` runs a turn in session `S` and the web view has `S` open, when the turn writes events, then the page shows them within 3 seconds without text deltas, and labels `S` as in use by PID `<pid>`.

#### REQ-WEB-013: Tool lines

The system shall show each tool call as one line with the tool name, the primary argument cut to 60 characters (spec
#6 `REQ-TG-009`), and a status: `running`, `ok`, `error`, `rejected`, or `cancelled`. The status shall be written in
words, not only as an icon.

- `AC-WEB-015`: Given a turn runs `bash` with `df -h` and the call succeeds, then the page shows the line `bash df -h` with status `ok`.

#### REQ-WEB-014: Approvals in the browser

When a tool in a web session needs approval, the system shall open an approval dialog with `Allow once`, `Allow for
session`, and `Reject`. The first answer from any tab wins. A later answer shall receive `409 approval_closed`. The
outcomes are the ADR 0004 outcomes.

- `AC-WEB-016`: Given a `bash` call that needs approval, when the owner clicks `Allow once` in one tab and `Reject` in a second tab, then the tool runs once, the log has `approval/decided` with `allowed_once`, and the second answer receives 409 `approval_closed`.

#### REQ-WEB-015: Approval timeout

An approval that gets no answer within 10 minutes shall end with the timeout outcome of ADR 0004. ADR 0004 names
`unavailable`; spec #6 uses `rejected` for Telegram (`Q-014`). The dialog shall close and state the outcome in words.

- `AC-WEB-017`: Given no answer for 10 minutes, when the timeout passes, then the outcome is recorded as `unavailable` (pending `Q-014`), the tool does not run, the model receives a tool error, and the dialog reads `Approval expired`.

#### REQ-WEB-016: Cancel

Cancel shall stop the running turn of the session with stop reason `cancelled` (ADR 0004; spec #6 `REQ-DMN-004`).
A pending approval of that turn shall end as `cancelled`. Cancel on an idle session shall change nothing.

- `AC-WEB-018`: Given a running turn with a pending approval, when the owner presses Cancel, then the turn ends with stop reason `cancelled`, the approval is recorded as `cancelled`, and the page shows `cancelled` in words.

#### REQ-WEB-017: Session actions

The owner shall compact an idle session, fork it at its last completed turn (ADR 0009), and change its agent, model,
and policy. These use the same runtime operations as the CLI and Telegram. Policy `ask` and `never` can be chosen
freely; `trust` needs a confirmation step (`Q-012`).

- `AC-WEB-019`: Given an idle session with at least one completed turn, when the owner compacts it, then a `compaction/summary` event is written; when the owner forks it, then a new session with `parent_session_id` set is created; when the owner sets policy `never`, then an `approval/policy` event is written.

#### REQ-WEB-018: Session in use by another process

A session in use by another process shall be read-only in the web view. Send, cancel, compact, fork, and settings
changes shall be refused with `409 session_in_use` and the PID. Reading history shall always work.

- `AC-WEB-020`: Given `lana chat` holds the lease of `S`, when the owner sends a message to `S` in the web view, then the response is 409 `session_in_use` naming the PID, and the history of `S` is still shown.

#### REQ-WEB-019: One lease set for the daemon

Every session the daemon uses (Telegram chats and web sessions) shall be leased through the daemon's single lease set.
The daemon shall not refuse its own sessions.

- `AC-WEB-021`: Given the Telegram chat of the owner has used session `T` in this daemon, when the owner sends a message to `T` in the web view, then the message is accepted, the turn is written to the log of `T`, and the owner's next Telegram message continues `T`.

### Settings

#### REQ-WEB-020: Read-only settings view

The system shall show the effective settings: providers (without key values), agents (model, tools, subagents, MCP
servers, skills, permission, rules, workspace), routes, channels (Telegram without the token value), MCP servers, and
modules. Values under `mcp.*.env` and `mcp.*.headers` shall show as `***`. The page shall state whether the file on
disk validates.

- `AC-WEB-022`: Given an MCP server with `headers.Authorization = "Bearer x"`, when the owner opens settings, then the value shows as `***` in the structured view and in the raw `config.toml` view, and the validation status is shown.

#### REQ-WEB-021: Validated saves

Before writing, a save of `config.toml` shall run the same validation as `lana config check` on the candidate text. An
invalid candidate shall not be written and the response shall list each error with its file and key. A valid
candidate shall be written atomically (temporary file and rename) and keep the file mode.

- `AC-WEB-023`: Given a candidate with `agents.coder.provider = "nope"`, when the owner saves it, then the status is 422 `validation_failed` naming `agents.coder.provider`, and `config.toml` is byte-for-byte unchanged; given a valid candidate, then the status is 200 and the file mode is unchanged.

#### REQ-WEB-022: Stale writes

Every save of `config.toml`, agent files, skills, and secrets shall carry the config version or file hash the owner
loaded. If the file on disk changed since then, the save shall be refused with `409 stale_version` and nothing shall
be written.

- `AC-WEB-024`: Given two tabs loaded the same `config.toml`, when tab A saves, and then tab B saves with the old version, then tab B receives 409 `stale_version` and `config.toml` contains tab A's text.

#### REQ-WEB-023: When saved config takes effect (pending Q-008)

A saved config shall not change running sessions or the running daemon. The save response and the settings page shall
state `restart_required: true` until the daemon restarts. This is the agent recommendation for `Q-008`.

- `AC-WEB-025`: Given a saved change of `agents.helper.model`, when the owner views a running session of `helper`, then the session keeps the model it had, and the settings page shows `restart required` until the daemon restarts.

#### REQ-WEB-024: Secrets are write-only (pending Q-010)

The owner may set or unset a named secret in `$LANA_HOME/secrets.env`. The web view shall never return a secret value.
For each name it shall show whether the secret is set and whether the process environment overrides it. A changed
secret takes effect at the next daemon start, because the runtime reads secrets once.

- `AC-WEB-026`: Given the owner sets `OPENAI_API_KEY` through the web view, when the owner lists secrets, then the entry shows `set` and no value; `secrets.env` has mode 0600 and contains the value; given a name that is not a valid env name, then the status is 422 `validation_failed`.

#### REQ-WEB-025: Agent files (pending Q-011)

The owner may read and replace `AGENT.md`, `USER.md`, and `MEMORY.md` of any agent. A save of `USER.md` or `MEMORY.md`
above its limit (2,000 and 4,000 characters, spec #5 `REQ-PROMPT-003`) shall be refused with `422 over_limit` and
nothing is written. A saved file is used from the next turn (spec #5 `REQ-PROMPT-002`).

- `AC-WEB-027`: Given `USER.md` would be 2,001 characters, when the owner saves it, then the status is 422 `over_limit` and the file is unchanged; given a valid `AGENT.md` edit, when the next turn of that agent starts, then its prompt contains the new text.

#### REQ-WEB-026: Skills (pending Q-011)

The owner may list skills (global and agent-owned), read a `SKILL.md`, create a skill, and edit it. A saved skill shall
pass the same frontmatter validation as `lana skill` (spec #7). Skills cannot be deleted in the web view in v1.

- `AC-WEB-028`: Given the owner creates skill `git-workflow` with valid frontmatter and lists it for agent `coder`, then `coder` sees the skill in its visible skills; given a `SKILL.md` without a `description`, then the status is 422 `validation_failed`.

#### REQ-WEB-027: Audit of changes (pending Q-013)

Each successful save of `config.toml`, secrets, agent files, or skills shall write one `info` line to the daemon's
stderr log with the kind of change, the file or secret name, and the changed key names. The line shall never contain
a value.

- `AC-WEB-029`: Given a config save that changes two keys and a secret that is set through the web view, when the owner reads the log, then there is one line per save naming the file and the changed keys or the secret name, and no value appears in any line.

### Accessibility and lifecycle

#### REQ-WEB-028: Accessibility

Every flow of the web view shall work with the keyboard alone and with a screen reader. Controls shall be focusable and
show a visible focus ring. Dialogs shall trap focus and return it to the trigger on close. Status changes shall be
announced through a live region. Status shall never be shown by color alone.

- `AC-WEB-030`: Given keyboard only, when the owner creates a session, sends a message, answers an approval, and saves a setting, then each step completes without a pointer, and the approval dialog announces its outcome in text.

#### REQ-WEB-029: Shutdown

On SIGINT or SIGTERM the web view shall stop accepting requests, end every open SSE stream with `event: shutdown`, and
cancel running turns through the daemon shutdown of spec #6 `REQ-DMN-004`, all within the 10-second grace period.

- `AC-WEB-031`: Given an open SSE stream and a running turn, when the daemon receives SIGTERM, then the stream receives `event: shutdown`, the turn ends with stop reason `cancelled`, and the process exits 0 within 10 seconds.

## Contract commitments

| Requirement | Observable commitment | Contract operation |
|---|---|---|
| `REQ-WEB-001`, `REQ-WEB-003`, `REQ-WEB-004` | Port, loopback check, start line, exit codes | `API-WEB-001` |
| `REQ-WEB-005`, `REQ-WEB-006`, `REQ-WEB-007` | Login, cookie, CSRF, origin and host checks | `API-WEB-002` |
| `REQ-WEB-008` | Session list paging and fields | `API-WEB-003` |
| `REQ-WEB-009`, `REQ-WEB-012` | Session detail and events from the log | `API-WEB-004` |
| `REQ-WEB-010` | Create a web session | `API-WEB-005`, `API-WEB-012` |
| `REQ-WEB-017`, `REQ-WEB-018` | Change agent, model, policy; lease refusal | `API-WEB-006` |
| `REQ-WEB-011`, `REQ-WEB-018`, `REQ-WEB-019` | Send message, inbox, lease refusal | `API-WEB-007` |
| `REQ-WEB-011`, `REQ-WEB-012`, `REQ-WEB-029` | SSE event names and shutdown event | `API-WEB-008` |
| `REQ-WEB-016` | Cancel | `API-WEB-009` |
| `REQ-WEB-017` | Compact and fork | `API-WEB-010` |
| `REQ-WEB-014`, `REQ-WEB-015` | Approval answer and closed-approval error | `API-WEB-011` |
| `REQ-WEB-020`, `REQ-WEB-021`, `REQ-WEB-022`, `REQ-WEB-023`, `REQ-WEB-027` | Settings read, validated save, stale check, restart flag, audit | `API-WEB-013`, `API-WEB-014` |
| `REQ-WEB-024`, `REQ-WEB-027` | Secret state and write-only set and unset | `API-WEB-015` |
| `REQ-WEB-025` | Agent file read and replace | `API-WEB-016` |
| `REQ-WEB-026` | Skill list, read, create, edit | `API-WEB-017` |

## State and operation matrix

| Current state | Operation | Result | Allowed |
|---|---|---|---:|
| Idle (own session) | View history | History shown | Yes |
| Idle (own session) | Send | Turn starts | Yes |
| Idle (own session) | Cancel | No change | Yes |
| Idle (own session) | Compact, fork | Runs; event written | Yes |
| Idle (own session) | Change agent, model, policy | Event written | Yes |
| Running (own session) | Send | Message queued in inbox, `202` | Yes |
| Running (own session) | Cancel | Turn ends `cancelled` | Yes |
| Running (own session) | Compact, fork, change agent, model, or policy | `409 invalid_state` (same rule as spec #6 `/compact`: wait for the turn) | No |
| Waiting approval (own session) | Answer approval | Approval closed, turn continues | Yes |
| Waiting approval (own session) | Cancel | Approval `cancelled`, turn `cancelled` | Yes |
| Held by another process | View history | History shown, PID shown | Yes |
| Held by another process | Any write or send | `409 session_in_use` | No |
| Running in another process | Any write or send | `409 session_in_use`; events polled | No |

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| `[web] addr` is not loopback | Daemon does not start | exit 2, `error: web: addr must be a loopback address` |
| `[web] addr` port already in use | Daemon does not start | exit 1, `error: web: address already in use` |
| `[web]` has an unknown key or wrong type | Config error as in `lana config check` | exit 2, names `web.<key>` |
| Token file exists but cannot be parsed | Daemon does not start; the owner is told which file to fix | exit 2, names the file (`Q-002`) |
| Wrong login token | No cookie; no retry limit beyond the token's entropy | 401 `unauthenticated` |
| Session ID unknown | Not found | 404 `not_found` |
| Missing or invalid body | Request rejected; nothing written | 400 `invalid_request` |
| Config candidate invalid | Nothing written; all errors listed | 422 `validation_failed` |
| Config file changed since load | Nothing written | 409 `stale_version` |
| `config.toml` missing on disk | Settings page shows `run lana init`; no file is created by the web view | 409 `config_missing` on save |
| Agent file or skill over limit or invalid | Nothing written | 422 `over_limit` or `validation_failed` |
| Session in use by another process | Read-only; PID shown | 409 `session_in_use` |
| Compact, fork, or settings change during a turn | Refused | 409 `invalid_state` |
| Approval answered twice or after timeout | Second answer refused | 409 `approval_closed` |
| Provider error during a turn | Error line in the stream and in the page | `✗ <error>` line, as spec #6 `REQ-TG-010` |
| Browser disconnects | Turn continues | No error |
| Daemon stops | Streams end with `event: shutdown`; turns cancelled | exit 0 within 10 seconds |

## Authorization and security

- Only the owner, after login, can call the API. Login is the only unauthenticated endpoint, plus the static dashboard
  assets.
- The web view binds to loopback only (`REQ-WEB-003`). The `Host` header must match the bound loopback host and port
  (`REQ-WEB-007`).
- The owner token is at least 32 random bytes. It is stored as a hash. Comparison is constant-time.
- The browser cookie is HTTP-only, `SameSite=Strict`, and has no `Max-Age` beyond 12 hours of idle use (`Q-002`).
- State-changing requests need the `Origin` check and the `X-Lana-CSRF` header.
- Responses for the API send `Cache-Control: no-store`. The dashboard sends a `Content-Security-Policy` with
  `default-src 'self'`, no inline scripts, and `frame-ancestors 'none'`.
- Secrets are never returned: the read model returns `set` or `unset` only (`REQ-WEB-024`). Config values under
  `mcp.*.env` and `mcp.*.headers` are masked (`REQ-WEB-020`).
- Approvals show the tool name and the primary argument from the `tool/call` event (ADR 0004: requests do not copy
  arguments). Nothing new is trusted from the browser: the approval outcome is accepted only for an open request.
- The web view can run any tool that the agent may run, after the same permission rules. It is not a sandbox
  (ADR 0004: no OS sandbox in v1). A browser user with the token can therefore approve shell commands; the token
  must be treated like a password.
- MCP server output is data, never an instruction to lana (spec #7 `REQ-MCP-*`); the UI shows it as plain text.

## Data integrity and concurrency

- Session history is append-only. The web view never edits or deletes events (ADR 0009).
- Writes to one session go through the runtime; one turn runs at a time per session (ADR 0003).
- A save of `config.toml`, agent files, skills, or secrets is refused when the file changed since the owner loaded it
  (`REQ-WEB-022`). The check is best-effort against a concurrent `lana config edit`; the window between the check and
  the rename is small and is documented as a known limit.
- Two saves in one daemon are serialized by a single write lock for files under `$LANA_HOME`.
- Each daemon writes config and secrets only through the atomic write described in `design.md`.
- Approval requests have one answer. The first answer closes the request for all tabs (`REQ-WEB-014`).
- Event order in the SSE stream is the `seq` order of the log; a client that reconnects sends `from_seq` and
  receives the missing events once.

## Accessibility

- Every action is a native button or link. Dialogs use the native `dialog` element or equivalent focus trap.
- Focus order follows the visual order. Focus returns to the trigger after a dialog closes.
- Session status, approval outcome, save result, and errors are announced through an `aria-live` region.
- Tool lines show status words (`ok`, `error`, `rejected`, `cancelled`) and not only icons.
- Text meets WCAG 2.2 AA contrast in light and dark color schemes.
- Keyboard shortcuts are optional and never the only way to do an action.

## Scenarios

### SC-001: Continue a Telegram chat from the browser

**Given** the owner's Telegram chat `T` used agent `helper` today, and the daemon runs with the web view
**When** the owner opens `T` in the web view and sends "check disk usage"
**Then** the answer streams into the page, the `bash` tool line shows `ok`, the Telegram chat's next message continues the same session (`AC-WEB-021`)

### SC-002: Approve a shell command in the browser

**Given** agent `coder` has policy `ask` and the owner has a web session with `coder`
**When** the agent calls `bash` with `git status`, which is not in `rules.allow`
**Then** an approval dialog opens with `Allow once`, `Allow for session`, and `Reject`; the owner picks `Allow once`; the tool runs and the dialog closes with the outcome in words (`AC-WEB-016`)

### SC-003: Session held by lana chat

**Given** the owner runs `lana chat --resume S` in a terminal
**When** the owner opens `S` in the web view and tries to send a message
**Then** the history is shown with the PID of the terminal process, the send is refused with `session_in_use`, and the page updates while the terminal turn runs (`AC-WEB-014`, `AC-WEB-020`)

### SC-004: Change a model and restart

**Given** the owner changes `agents.helper.model` in the raw `config.toml` editor and saves it
**When** the save succeeds
**Then** the page shows `restart required`; running sessions keep the old model; after the owner restarts
`lana daemon`, new turns use the new model (`AC-WEB-023`, `AC-WEB-025`)

## Blocking Open Questions

| ID | Question | Controls | Requested reference | Agent recommendation | Owner | Status | Resolution |
|---|---|---|---|---|---|---|---|
| `Q-001` | May the web view listen on a non-loopback address in v1? | `REQ-WEB-003` | ADR 0008 (`0.0.0.0` requires a token) | No. Loopback only. Refuse other addresses with exit 2. Remote access, TLS, and a reverse proxy are a later spec | Ridho | Open | — |
| `Q-002` | How is the owner token created, stored, shown, and how long does a browser login last? | `REQ-WEB-005`, `REQ-WEB-006`, Failure cases | ADR 0008 says `lana init` creates the token and stores a hash; `lana init` does not create one today | Daemon creates `$LANA_HOME/web.json` (mode 0600) on first start if it is missing, with a random 32-byte token; stores only its SHA-256; prints the token once to stderr at creation; cookie lasts 12 hours idle; a new token is made by deleting `web.json` and restarting. This deviates from ADR 0008 where it says `lana init`, because `lana init` runs before the web view exists | Ridho | Open | — |
| `Q-003` | Which HTTP namespace and auth model does the browser API use? `/v1` as in ADR 0014 with bearer tokens, or a private browser namespace? | `API-WEB-*` paths, `REQ-WEB-005` | ADR 0014 (`/v1`, `[[api.tokens]]`); ADR 0008 (login token) | Use the `/v1` paths in this contract, with cookie auth for the browser. Bearer tokens for API clients stay out of this spec. Write a new ADR (0016) for the owner-auth model before design approval | Ridho | Open | — |
| `Q-004` | Which front-end stack? | `design.md` assets, build and Nix | ADR 0008 (Preact + `htm`, no build step, under 200 KB) | Follow ADR 0008: pinned Preact and `htm` files vendored under `web/assets/vendor/`, plain ES modules, no Node in `flake.nix`. Alternative: plain JS without a framework | Ridho | Open | — |
| `Q-005` | May `lana daemon` run with the web view only, without `[channels.telegram]`, and is the web view on by default? | `REQ-WEB-004`, `REQ-WEB-001` | Spec #6 `REQ-DMN-003` and API-DMN-001 (exit 2 today) | Yes. Web view on by default when `[web]` is absent. Revise spec #6 to r2 for the new no-channel rule | Ridho | Open | — |
| `Q-006` | Which sessions are listed, and what channel and address do web sessions use? | `REQ-WEB-008`, `REQ-WEB-010` | Spec #6 `Q-007` (workspace precedent) | List every channel with a channel badge. Web sessions use channel `web` and address `owner`; workspace from the agent or `$LANA_HOME/workspaces/<agent>/` | Ridho | Open | — |
| `Q-007` | For a session that runs in another process, is polling of the event log (no token stream) acceptable? | `REQ-WEB-012` | `lana chat` runs its own runtime; live frames are in-process only | Yes. Poll the log about once per second and show the PID. Token streaming for other processes needs a cross-process frame feed, which is a later ADR | Ridho | Open | — |
| `Q-008` | When does a saved config take effect? | `REQ-WEB-023`, `API-WEB-014` | Runtime is built once at start (`lana.New`); no reload path | Save and validate now; no hot reload in v1; show `restart required`; the owner restarts `lana daemon`. Hot reload is a later change: rebuild the runtime when no turn runs, and update the Telegram backend too | Ridho | Open | — |
| `Q-009` | What is the editing surface for `config.toml`? | `REQ-WEB-020`, `REQ-WEB-021`, `API-WEB-013`, `API-WEB-014` | `lana config edit` opens `$EDITOR` today | v1: read-only structured view plus a raw TOML editor validated on save (comments kept). Values under `mcp.*.env` and `mcp.*.headers` are masked in the editor and kept on save. Structured forms can come later; a TOML writer for them drops comments | Ridho | Open | — |
| `Q-010` | May the browser write secrets at all, and which names? | `REQ-WEB-024`, `API-WEB-015` | Spec #1 secrets rule (process env wins, never printed) | Yes. Set and unset named secrets in `secrets.env` (mode 0600). Never reveal. Show `set` or `unset` and `overridden by process env`. Provider keys and the Telegram token are included, and take effect at restart | Ridho | Open | — |
| `Q-011` | Which agent-file and skill edits are allowed in v1? | `REQ-WEB-025`, `REQ-WEB-026` | Spec #5 and spec #7 | Edit `AGENT.md`, `USER.md`, `MEMORY.md` (limits enforced by refusing the save), and create or edit skills (global and agent-owned). No delete in v1 | Ridho | Open | — |
| `Q-012` | May the owner set policy `trust` from the browser? | `REQ-WEB-017` | ADR 0004 (`trust` runs without approval) | Yes, behind a confirmation dialog; recorded as `approval/policy` like `/permission` | Ridho | Open | — |
| `Q-013` | Where and how are settings changes audited? | `REQ-WEB-027` | Spec #1 logs (`$LANA_HOME/logs/lana.log`) and the daemon stderr log | One `info` line per save on the daemon stderr log with key names and no values. No audit table | Ridho | Open | — |
| `Q-014` | Which outcome does an approval timeout produce? | `REQ-WEB-015` | ADR 0004 says `unavailable`; spec #6 (approved) uses `rejected` for Telegram | Use `unavailable` for the web view, as ADR 0004 says, and revise spec #6 to match (or record an ADR that changes it) | Ridho | Open | — |
| `Q-015` | Default address and config key for the web view | `REQ-WEB-001` | ADR 0008 (`127.0.0.1:18300`, configurable) | `[web] addr = "127.0.0.1:18300"` and `[web] enabled = true` | Ridho | Open | — |

The agent must ask every Open question and wait for the answers before approval or design.

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-WEB-001` | `AC-WEB-001`, `AC-WEB-002` | Owner request, ADR 0008, `Q-015` |
| `REQ-WEB-002` | `AC-WEB-003` | ADR 0008 (`embed`, no build step), ADR 0001 roadmap |
| `REQ-WEB-003` | `AC-WEB-004` | ADR 0008, `Q-001` |
| `REQ-WEB-004` | `AC-WEB-005` | `Q-005`, spec #6 `REQ-DMN-003` |
| `REQ-WEB-005` | `AC-WEB-006` | ADR 0008, ADR 0014, `Q-002` |
| `REQ-WEB-006` | `AC-WEB-007` | ADR 0008, `Q-002` |
| `REQ-WEB-007` | `AC-WEB-008` | Security baseline, `Q-003` |
| `REQ-WEB-008` | `AC-WEB-009` | Owner request, `Q-006` |
| `REQ-WEB-009` | `AC-WEB-010` | ADR 0009 |
| `REQ-WEB-010` | `AC-WEB-011` | ADR 0003, spec #6 `REQ-TG-005`, `Q-006` |
| `REQ-WEB-011` | `AC-WEB-012`, `AC-WEB-013`, `AC-WEB-032` | ADR 0003, spec #6 `REQ-TG-004`, ADR 0008 (SSE) |
| `REQ-WEB-012` | `AC-WEB-014` | `Q-007`, spec #1 `REQ-SESS-002` |
| `REQ-WEB-013` | `AC-WEB-015` | Spec #6 `REQ-TG-009` |
| `REQ-WEB-014` | `AC-WEB-016` | ADR 0004, ADR 0008 (modal approval) |
| `REQ-WEB-015` | `AC-WEB-017` | ADR 0004, spec #6 `REQ-TG-011`, `Q-014` |
| `REQ-WEB-016` | `AC-WEB-018` | ADR 0004, spec #6 `REQ-DMN-004` |
| `REQ-WEB-017` | `AC-WEB-019` | ADR 0009, spec #6 `/compact`, `Q-012` |
| `REQ-WEB-018` | `AC-WEB-020` | Spec #1 `REQ-SESS-002` |
| `REQ-WEB-019` | `AC-WEB-021` | Spec #6 `API-TG-002`, daemon lease set |
| `REQ-WEB-020` | `AC-WEB-022` | Spec #1 secrets rule, spec #2 `core.Secret` |
| `REQ-WEB-021` | `AC-WEB-023` | Spec #1 `REQ-CONF-001`, ADR 0010 |
| `REQ-WEB-022` | `AC-WEB-024` | Owner request (two tabs), `Q-008` |
| `REQ-WEB-023` | `AC-WEB-025` | `Q-008` |
| `REQ-WEB-024` | `AC-WEB-026` | Spec #1 secrets rule, `Q-010` |
| `REQ-WEB-025` | `AC-WEB-027` | Spec #5 `REQ-PROMPT-002`, `REQ-PROMPT-003`, `Q-011` |
| `REQ-WEB-026` | `AC-WEB-028` | Spec #7 skills, `Q-011` |
| `REQ-WEB-027` | `AC-WEB-029` | `Q-013` |
| `REQ-WEB-028` | `AC-WEB-030` | Owner request (accessibility as a quality bar) |
| `REQ-WEB-029` | `AC-WEB-031` | Spec #6 `REQ-DMN-004` |

## Approval

- [ ] Scope and exclusions are explicit.
- [ ] Success and failure behavior is measurable.
- [ ] Authorization is explicit or stated as not applicable.
- [ ] State transitions and destructive effects are explicit or not applicable.
- [ ] Defaults, ordering, retry, and idempotency are explicit where applicable.
- [ ] Every requirement and acceptance criterion has a stable ID and a source.
- [ ] Contract applicability is explicit.
- [ ] Blocking Open Questions is `None`.
- **Approved by**: —
- **Approved on**: —
