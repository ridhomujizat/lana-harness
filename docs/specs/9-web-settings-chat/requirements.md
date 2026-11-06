# Requirements: Web view: settings and chat sessions

- **Issue**: —
- **Status**: Approved
- **Revision**: 2 (2026-10-30, owner brief: align with the dashboard mockup in `design/`; see Revision history)
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (`[web]` config, browser login and browser session records, `/v1` session, setup, providers, settings, agents, skills, MCP, channels, secrets, agent files, daemon restart, audit, SSE stream)
- **Related specs**: [#5](../5-agent-home/requirements.md) (agent home, shared `USER.md`), [#6](../6-daemon-telegram/requirements.md) (daemon, Telegram channel), [#7](../7-mcp-skills/requirements.md) (skills, MCP), [#10](../10-agent-teams/requirements.md) (agent teams, team UI), [#11](../11-ask-user-question/requirements.md) (ask-user questions, question card)
- **Related ADR**: [ADR Index](../../adr/README.md) — [0003](../../adr/0003-multi-agent-with-channel-routing.md), [0004](../../adr/0004-permission-mode-per-agent.md), [0008](../../adr/0008-cli-channel-http-and-web-dashboard-interfaces.md), [0009](../../adr/0009-session-as-append-only-event-log.md), [0010](../../adr/0010-profile-and-config-patch.md), [0012](../../adr/0012-acp-and-sdk-json-rpc.md), [0014](../../adr/0014-integration-as-agentic-tool.md), [0015](../../adr/0015-embeddable-runtime-for-services.md). [0016](../../adr/0016-owner-auth-for-browser.md) (owner auth for the browser, `Q-002`, `Q-003`).
- **Related diagram**: [Diagram Index](../../diagram/README.md) — [0001](../../diagram/0001-system-context-and-roadmap.md) (M5/M6 roadmap: REST + SSE, `embed.FS` dashboard), [0002](../../diagram/0002-agent-loop-and-approval.md)

## Revision history

| Revision | Date | Change |
|---|---|---|
| 1 | 2026-10-30 | First approved version: login page, modal approval dialog, read-only settings with a raw `config.toml` editor, skills without delete, no restart from the browser |
| 2 | 2026-10-30 | Owner brief: align with the dashboard mockup in `design/`. Adds the sidebar IA and hash routes, the setup wizard, inline approval cards, sessions filters and status, structured settings forms, restart from the browser, browser logins that survive a restart, agents, skills delete, MCP, Telegram channel page, secrets with update time, and an audit file. Supersedes the modal approval dialog, the raw-only settings rule (`Q-009`), the skills part of `Q-011`, and the login page as a separate screen |

Revision 2 keeps every rev 1 ID. A rev 1 requirement that changed keeps its text and carries a line
**Superseded by ... (rev 2)** or **Extended by ... (rev 2)**. The table "Rev 2 changes" in
[Requirement coverage](#requirement-coverage) lists every change. New IDs start after the last rev 1 ID:
`REQ-WEB-030` to `REQ-WEB-056`, `AC-WEB-033` to `AC-WEB-059`, and `Q-016` to `Q-028`.

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-30 | Spec for a web view served by `lana daemon`: settings and chat sessions | goal and scope |
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
| Owner brief 2026-10-30 (rev 2) | Decisions 1 to 13 for the dashboard, specs #5, #6, #10, #11, and ADRs 0008, 0016 | rev 2 scope and every rev 2 default |
| Dashboard mockup `design/` (2026-10-30) | `design/lana-design-system.figcraft`, `design/export/screens/*.layout.txt`, `*.png`, `tokens.css`, `icons/` | screen list, layout, UI copy (`REQ-WEB-030` to `REQ-WEB-056`) |
| Spec #5 rev 4 | `$LANA_HOME/USER.md` shared by every agent (`REQ-HOME-003`, `REQ-HOME-004`) | `REQ-WEB-047`, `REQ-WEB-034` |
| Spec #6 rev 3 | Waiting chats (`REQ-TG-016`), `enabled` switch (`REQ-DMN-005`), token-missing rule (`REQ-DMN-006`), bot identity (`REQ-TG-017`), channel status (`REQ-TG-018`), disconnect (`REQ-TG-019`) | `REQ-WEB-052`, `REQ-WEB-053` |
| ADR 0016 (rev 2) | Browser session records in `web-sessions.json`; revoke others | `REQ-WEB-036` |
| Spec #10, spec #11 | Agent teams and ask-user questions. Written by other owners in their own folders | references only (`REQ-WEB-040`, `REQ-WEB-041`, `REQ-WEB-048`) |

Anything not supported by a source or an explicit approval goes into Blocking Open Questions. Every rev 1 open question was answered on 2026-10-30 (the owner accepted every agent recommendation). Rev 2 questions `Q-016` to `Q-028` carry the agent recommendation (`Rekomendasi agent:`). The owner lifted the approval gate for rev 2 on 2026-10-30 (Ridho, lifted gate 2026-10-30).

## Goal

The owner runs `lana daemon` on their own machine and opens lana in a browser on that machine. A first-time owner is
guided through setup: a provider, an agent, and its persona. After setup, the owner can see every chat session
(terminal, Telegram, library, web), continue a session with streamed answers, tool lines, and inline approvals, and
manage agents, skills, MCP servers, channels, providers, secrets, and settings with forms instead of by hand. The
owner restarts the daemon from the browser when a saved config needs it. The daemon keeps running while the owner works.

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
- **Setup needed** — the state reported by `GET /v1/setup` as `needed: true`. It holds when `config.toml` is missing,
  when no `[providers.*]` table exists, or when no `[agents.*]` table exists (rev 2).
- **Browser session record** — the stored record of one browser login: hashed session ID, hashed CSRF value, created
  time, last used time, and a user-agent label. It lives in `$LANA_HOME/web-sessions.json`. It is not a chat session
  (rev 2).
- **Restart in place** — the daemon ends its runtime and runs its own binary again with the same arguments, in the same
  process, so the PID does not change and the daemon lock stays held (rev 2).
- **Waiting chat** — a Telegram chat that wrote to the bot and has no route. The Telegram channel keeps it in memory
  for 24 hours (spec #6 rev 3, rev 2 of this spec).
- **Mockup** — the dashboard design in `design/`. It is the source for layout, screen list, and UI copy.

## Scope

### Included

- `lana daemon` serves the web view on loopback when `[web]` is enabled (`API-WEB-001`).
- Owner login with a token, origin and host checks, and CSRF protection (`API-WEB-002`).
- Chat sessions across all channels: list, history, create, send, stream over SSE, tool lines, cancel.
- Approvals in the browser with `Allow once`, `Allow for this session`, and `Deny`, shown as an inline card (rev 2;
  rev 1 used a dialog with `Reject`).
- Session actions: compact, fork, change agent, change model, change policy, stop.
- Sessions list with search, filters, facets, and status values (rev 2).
- Structured forms for providers, agents, MCP servers, the Telegram channel and its chats, and `[web] addr` (rev 2,
  `Q-018`). A validated raw `config.toml` editor for every other key (`Q-009`).
- Setup wizard for a first-time owner, when setup is needed (rev 2, `Q-017`).
- Restart of the daemon from the browser, with a confirmation (rev 2, `Q-019`).
- Browser login that survives a restart, and "sign out other browsers" (rev 2, `Q-020`).
- Secrets: set, unset, and list state without values, with update time and "Used by" (`Q-010`, `Q-022`).
- Agent files `AGENT.md`, `MEMORY.md` per agent; `USER.md` shared by every agent (spec #5 rev 4): read and replace.
- Agents: list, create from a template, duplicate, and the detail tabs General, Persona and memory, Tools, Skills,
  MCP, Rules, and Workspace (rev 2).
- Skills: list, read, create, edit, and delete (rev 2; `Q-011` skill part superseded).
- MCP servers: list, detail, add with a test, restart of one client (rev 2).
- Channels page: Telegram detail (token check, owners, chats, waiting chats), and status cards for Web, CLI, and
  coming-soon channels (rev 2).
- Audit file for each settings save, and the Audit log page (rev 2, `Q-021`). The stderr line stays.
- Light, Dark, and System themes (rev 2).
- Keyboard and screen-reader support for every flow in the web view.

### Not included

- Multi-user accounts, roles, or sharing with other people.
- Access from another machine, LAN, or internet; TLS certificates; reverse proxies (`Q-001`).
- Bearer tokens for API clients and per-client `[[api.tokens]]` (ADR 0014). Only the browser login is in this spec (`Q-003`).
- An OpenAPI document. ADR 0014 requires one for the full REST API; it is a follow-up.
- Hot reload of config while the daemon runs (`Q-008`).
- Structured forms for every config key. Forms cover the entities listed above; other keys use the raw editor
  (`Q-009`, rev 2 `Q-018`).
- Deleting agent files or sessions from the web view (`Q-011`). Agents cannot be deleted in rev 2. Skills can be
  deleted (rev 2).
- The team UI (agent teams, spec #10) and the ask-user question card (spec #11). This spec links to them and does not
  define them. Team rows in the agents list and the team picker follow spec #10.
- A file upload control. "Attach file" is shown disabled and labelled "coming soon".
- Log rotation and export of the audit file (`Q-021`).
- Windows support for restart in place (`Q-019`).
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
- The mockup in `design/` is the source for layout and UI copy. Where this spec and the mockup differ, this spec wins and the mockup is updated later. Known differences are listed in `design.md` under "UI reference".
- A config change needs a daemon restart to take effect. An agent file change (AGENT.md, USER.md, MEMORY.md) applies at the next turn. The UI says so in these words; the mockup's "Changes apply to new turns" is not used. — Source: `Q-008`, spec #5 `REQ-PROMPT-002`.

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

#### REQ-WEB-004: Web view without Telegram

When the web view is enabled and no `[channels.telegram]` is configured, the daemon shall run with the web view only.
It shall not exit with code 2 as spec #6 `REQ-DMN-003` requires today. This changes spec #6 and needs its revision.

- `AC-WEB-005`: Given `[web]` is enabled and there is no `[channels.telegram]`, when the owner runs `lana daemon`, then the daemon keeps running, serves the web view, and makes no Telegram API call.

### Access

#### REQ-WEB-005: Owner login

**Extended by REQ-WEB-030 and REQ-WEB-032 (rev 2)**: the login page is now the Unlock step of the setup wizard. The login rules below are unchanged. Browser sessions survive a daemon restart (`REQ-WEB-036`).

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

**Superseded by REQ-WEB-039 (rev 2)** for the dialog. The approval is an inline card above the composer, with the buttons `Allow once`, `Allow for this session`, and `Deny`. The first-answer rule below stays.

When a tool in a web session needs approval, the system shall open an approval dialog with `Allow once`, `Allow for
session`, and `Reject`. The first answer from any tab wins. A later answer shall receive `409 approval_closed`. The
outcomes are the ADR 0004 outcomes.

- `AC-WEB-016`: Given a `bash` call that needs approval, when the owner clicks `Allow once` in one tab and `Reject` in a second tab, then the tool runs once, the log has `approval/decided` with `allowed_once`, and the second answer receives 409 `approval_closed`.

#### REQ-WEB-015: Approval timeout

**Extended by REQ-WEB-039 (rev 2)**: the card, not the dialog, closes and states the outcome in words (`Approval expired`).

An approval that gets no answer within 10 minutes shall end with the timeout outcome of ADR 0004. ADR 0004 names
`unavailable`; spec #6 uses `rejected` for Telegram (`Q-014`). The dialog shall close and state the outcome in words.

- `AC-WEB-017`: Given no answer for 10 minutes, when the timeout passes, then the outcome is recorded as `unavailable`, the tool does not run, the model receives a tool error, and the dialog reads `Approval expired`.

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

**Superseded by REQ-WEB-042, REQ-WEB-043, and REQ-WEB-044 (rev 2)**: the settings pages edit the entities with forms; the raw editor stays. The masking rule below is kept in `REQ-WEB-042`.

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

#### REQ-WEB-023: When saved config takes effect

A saved config shall not change running sessions or the running daemon. The save response and the settings page shall
state `restart_required: true` until the daemon restarts. This is the agent recommendation for `Q-008`.

- `AC-WEB-025`: Given a saved change of `agents.helper.model`, when the owner views a running session of `helper`, then the session keeps the model it had, and the settings page shows `restart required` until the daemon restarts.

#### REQ-WEB-024: Secrets are write-only

The owner may set or unset a named secret in `$LANA_HOME/secrets.env`. The web view shall never return a secret value.
For each name it shall show whether the secret is set and whether the process environment overrides it. A changed
secret takes effect at the next daemon start, because the runtime reads secrets once.

- `AC-WEB-026`: Given the owner sets `OPENAI_API_KEY` through the web view, when the owner lists secrets, then the entry shows `set` and no value; `secrets.env` has mode 0600 and contains the value; given a name that is not a valid env name, then the status is 422 `validation_failed`.

#### REQ-WEB-025: Agent files

**Extended by REQ-WEB-047 (rev 2)**: `USER.md` is one file shared by every agent, at `$LANA_HOME/USER.md` (spec #5 rev 4). The per-agent `USER.md` is no longer read. `AGENT.md` and `MEMORY.md` stay per agent.

The owner may read and replace `AGENT.md`, `USER.md`, and `MEMORY.md` of any agent. A save of `USER.md` or `MEMORY.md`
above its limit (2,000 and 4,000 characters, spec #5 `REQ-PROMPT-003`) shall be refused with `422 over_limit` and
nothing is written. A saved file is used from the next turn (spec #5 `REQ-PROMPT-002`).

- `AC-WEB-027`: Given `USER.md` would be 2,001 characters, when the owner saves it, then the status is 422 `over_limit` and the file is unchanged; given a valid `AGENT.md` edit, when the next turn of that agent starts, then its prompt contains the new text.

#### REQ-WEB-026: Skills

The owner may list skills (global and agent-owned), read a `SKILL.md`, create a skill, and edit it. A saved skill shall
pass the same frontmatter validation as `lana skill` (spec #7). ~~Skills cannot be deleted in the web view in v1.~~
**Superseded by REQ-WEB-048 (rev 2)** for that sentence: a skill can be deleted with a confirmation. Agent files still
cannot be deleted.

- `AC-WEB-028`: Given the owner creates skill `git-workflow` with valid frontmatter and lists it for agent `coder`, then `coder` sees the skill in its visible skills; given a `SKILL.md` without a `description`, then the status is 422 `validation_failed`.

#### REQ-WEB-027: Audit of changes

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

### Rev 2: navigation, theme, and setup

#### REQ-WEB-030: Information architecture and routes

The dashboard shall show a left sidebar (240 px wide; the mockup shows 280 px, narrowed at the owner's request after testing on 2026-10-30) with the items Home, Sessions, Agents, Skills, MCP, Channels, and
Settings. The bottom of the sidebar shows a daemon card (state, address, uptime) and an owner row with a logout icon.
Every page has a hash route:

| Route | Page |
|---|---|
| `#/home` | Home (empty state) |
| `#/sessions`, `#/sessions/new`, `#/sessions/{id}` | Sessions list, new session, chat |
| `#/agents`, `#/agents/{name}/{tab}` | Agents list; agent detail with tab `general`, `persona`, `tools`, `skills`, `mcp`, `rules`, or `workspace` |
| `#/skills`, `#/skills/{scope}/{name}`, `#/skills/new` | Skills list, skill editor, new skill |
| `#/mcp`, `#/mcp/{name}` | MCP list, MCP detail |
| `#/channels`, `#/channels/telegram` | Channels, Telegram detail |
| `#/settings/{general\|providers\|secrets\|config\|audit}` | Settings tabs |
| `#/setup/{step}` | Setup wizard (`unlock`, `provider`, `connect`, `agent`, `persona`, `ready`) |

After login the page opens `#/sessions`. Home shows the text "Home is coming soon" and a "Go to sessions" button. An
unknown route shows a not-found state inside the layout. Team routes and question routes belong to specs #10 and #11.

- `AC-WEB-033`: Given a logged-in browser, when it opens each route in the table, then the matching page renders inside the sidebar layout; after login the page opens `#/sessions`; when it opens `#/unknown`, then a not-found state shows and the sidebar stays.

#### REQ-WEB-031: Theme and sidebar state

The dashboard shall offer three themes: Light, Dark, and System. The choice is in Settings › General › Appearance. The
theme colors come from `tokens.css` (design `design/export/tokens.css`). The sidebar may collapse to icons. The theme
choice and the sidebar state are stored in the browser's `localStorage`, not on the daemon.

- `AC-WEB-034`: Given the owner picks Dark, when the page reloads, then Dark is still applied; given the sidebar is collapsed, when the page reloads, then it is still collapsed; given System, then the page follows the operating system's color scheme.

#### REQ-WEB-032: Setup wizard

When `GET /v1/setup` reports `needed: true`, the dashboard shall show the setup wizard instead of the normal pages. The
wizard has five steps shown in a left progress rail:

1. **Unlock** — the owner token (`REQ-WEB-005`). This replaces the rev 1 login page.
2. **Provider** — OpenAI, Anthropic, or OpenRouter cards.
3. **Connect model** — API key, base URL, default model with suggested model badges, and "Test connection".
4. **Set up agent** — agent name, template, workspace, and permission. Step 4b **Persona** follows, with `AGENT.md` and
   `USER.md` text areas.
5. **Ready** — summary, "Start your first session", and "Open settings".

Steps 2 to 5 appear only while setup is needed. The Unlock step is the only step shown to a logged-out browser. Setup
is needed when `config.toml` is missing, when it has no `[providers.*]` table, or when it has no `[agents.*]` table. The
owner can return to an earlier step with "Back". When setup is no longer needed, `#/setup/*` routes redirect to
`#/sessions`.

- `AC-WEB-035`: Given `config.toml` has one provider and one agent, when the owner logs in, then `GET /v1/setup` returns `needed: false` and the wizard is not shown; given `config.toml` is missing, then `needed` is `true`, `reasons` contains `config_missing`, and the owner sees step 2 after Unlock.

#### REQ-WEB-033: Provider connection test

The system shall test a provider with one minimal chat request: one output token, a 15-second timeout, and the given kind,
base URL, and model. The key comes from an inline value or from a named environment variable. The inline key is never
stored, logged, or returned. The test result is shown inline: `ok` with `latency_ms`, or the HTTP status and a message.
Setup and the provider form both use this test. A save in setup runs the test first and refuses when the test fails.

- `AC-WEB-036`: Given an OpenAI key that the provider rejects with 401, when the owner presses "Test connection", then the response is `ok: false` with `status: 401` and the message `401 Unauthorized from api.openai.com`, and the key field shows the error; when the owner presses "Save and continue", then nothing is written and the response is 422 `connection_failed`. Given a valid key, then the test returns `ok: true` and `latency_ms`, and the key value appears in `secrets.env` after a successful save and in no other file or log line.

#### REQ-WEB-034: Setup save and restart

When the owner presses "Continue" on step 4b, the system shall save the setup in this order, and stop at the first failure:

1. Run the provider test again (`REQ-WEB-033`).
2. Create `config.toml` with `version = 1` if it is missing, then add `[providers.<name>]` and `[agents.<name>]`. The provider names are `openai` (kind `openai_compat`), `anthropic` (kind `anthropic`), and `openrouter` (kind `openai_compat`), with their default base URLs.
3. Write the key to `$LANA_HOME/secrets.env` as `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, or `OPENROUTER_API_KEY` (mode `0600`).
4. Create the agent home from the chosen template (spec #5). Write `AGENT.md` from the persona text. Write `USER.md` at `$LANA_HOME/USER.md` only when that file is missing (spec #5 rev 4).
5. Restart the daemon in place (`REQ-WEB-035`).

The page then waits for `GET /v1/daemon` to answer with a new `started_at` and continues to step 5. A setup save is refused when setup is not needed (`409 setup_not_needed`). The agent name must match `[a-z0-9-]` and must not exist.

- `AC-WEB-037`: Given a new `LANA_HOME` with no `config.toml`, when the owner completes setup with provider OpenAI, agent `coder`, template Coder, and workspace `~/lana/workspace`, then `config.toml` has `version = 1`, `[providers.openai]`, and `[agents.coder]`; `secrets.env` has mode `0600` and `OPENAI_API_KEY`; `agents/coder/AGENT.md` exists; the daemon PID is the same after the restart; and step 5 shows after the daemon answers again.

### Rev 2: chat sessions

#### REQ-WEB-036: Browser logins survive a restart

**Extends REQ-WEB-005 and Q-002 (rev 2).** Browser session records shall be kept in `$LANA_HOME/web-sessions.json` (mode `0600`). A record holds the SHA-256 of the browser's session ID, the SHA-256 of its CSRF value, the created time, the last used time, and a user-agent label. A record never holds a cookie value or a CSRF value in plain text. Records survive a restart in place (`REQ-WEB-035`). The 12-hour idle limit still applies. The owner can list the records and sign out every other browser with one action (`POST /v1/auth/sessions/revoke-others`). The current browser stays signed in. If `web-sessions.json` cannot be parsed, the daemon refuses to start with exit 2 and names the file, as it does for `web.json`.

- `AC-WEB-039`: Given two browsers are signed in, when the owner restarts the daemon from browser A, then A stays signed in and its next call succeeds without a login; when the owner presses "Sign out other browsers" in A, then B's next call returns `401 unauthenticated`, A stays signed in, the revoke response has the count of revoked records, and a scan of `web-sessions.json` finds no plain session or CSRF value and shows mode `0600`.

#### REQ-WEB-037: Session list with search, filters, and status

**Extends REQ-WEB-008 (rev 2).** The sessions list shall show a header "Sessions" with a count badge and "+ New", and a search box that matches the title and the preview (`q`). A filter popover offers three facets with counts: Channel (Web, CLI, Telegram), Status (Running, Waiting approval, Waiting for you, Idle, Failed), and Agent (agents and teams, spec #10). Active filters appear as removable chips, with "Clear all". The button "Show N sessions" applies the filters. Facet counts reflect the search text only. The list pages 50 rows at a time, as in rev 1.

Each row shows a status dot, the title, the relative time, a preview line, a channel badge, and "· agent". The preview line shows the first of these that applies: "Waiting for your approval: bash go test ./..." for a pending approval; "Running: edit_file store/store.go" for a running tool; the last answer; or the error. The status values are `running`, `waiting_approval`, `waiting_input`, `idle`, and `failed`. A session held by another process shows the PID (`REQ-WEB-012`). The channel filter still accepts `library` through the API. The facet has no library entry (`Q-023`).

- `AC-WEB-040`: Given sessions with status running, waiting approval, failed, and idle, when the owner selects Status "Waiting approval", then only the waiting session shows; the facet counts match the unfiltered rows for the same search text; when the owner types "disk", then rows whose title or preview contains "disk" show; "Clear all" removes every chip.

#### REQ-WEB-038: Chat view

**Extends REQ-WEB-009 and REQ-WEB-013 (rev 2).** The chat view shall show a header with the session title, a channel badge, "N turns", and the actions "Compact" and "Fork" (`REQ-WEB-017`). User messages are right-aligned bubbles. Each assistant turn is one block with: the agent avatar (the first letter of the agent name on a color derived from the name), the agent name, "worked for 9s" when the turn is done or "working for 48s" while it runs, and the time. An activity summary row (for example "Read 5 files · Running 1 command") collapses and expands with a chevron. Expanded, it lists each step with its intent text, its primary argument as a chip, and its tool name as a chip. Interim assistant text appears between steps. The view is built from the log (`REQ-WEB-009`) and from live frames when the turn runs in this daemon (`REQ-WEB-011`).

- `AC-WEB-041`: Given a turn with five reads and one shell command, when the owner opens the session, then the summary row counts five files read and one command, the row is collapsed, expanding it lists six steps with tool name and primary argument, and each user message is a right-aligned bubble.

#### REQ-WEB-039: Inline approval card

**Supersedes the dialog wording of REQ-WEB-014 and REQ-WEB-015 (rev 2).** When a tool in a web session needs approval, the system shall show an inline card above the composer. The card shows a lock icon, the title "Allow `<tool>`?", the risk and "expires in N min", the command preview and the working directory, and three buttons:

| Button | Outcome (ADR 0004) |
|---|---|
| Deny | `rejected` |
| Allow for this session | `allowed_session` |
| Allow once | `allowed_once` |

The pending tool row in the chat shows a "Waiting" badge. The first answer from any tab wins (`REQ-WEB-014`). When the request closes, the card shows the outcome in words ("Approved once", "Denied", "Approval expired", "Cancelled") and then leaves the view. The card is reachable by keyboard and its outcome is announced in the live region.

- `AC-WEB-042`: Given a pending `bash` approval, when the card shows, then it sits above the composer, has the three buttons with those labels, and the pending row shows "Waiting"; given the owner presses "Allow once" with the keyboard only, then the log has `approval/decided` with `allowed_once`, the card says "Approved once", and the tool runs.

#### REQ-WEB-040: Composer, model and policy, and stop

The composer shall have a textarea with the placeholder "Message `<agent>`…", a plus button, a model picker, a policy picker (Ask, Trust, Never), and a send button. The plus button opens a menu with "Switch agent or team" (team entries follow spec #10) and "Attach file". "Attach file" is disabled and labelled "coming soon". While a turn runs, the send button becomes a stop button that cancels the turn (`API-WEB-009`). Choosing policy Trust opens a confirmation first (`Q-012`). While a question from spec #11 is open, the composer is disabled and reads "Answer the question above".

- `AC-WEB-043`: Given a running turn, when the owner presses the stop button, then the request is `POST /v1/sessions/{id}/cancel` and the turn ends `cancelled`; given the owner picks policy Trust, then nothing changes until the confirmation is accepted; "Attach file" cannot be activated.

#### REQ-WEB-041: New session and agent picker

The new session view shall show the logo, the title "New session", the text "Pick who works on it, then describe the task.", a textarea "What should we work on?", and an agent picker. The picker has a search box, a RECENT section, an AGENTS section (each entry shows its model under the name), team entries (spec #10), a "Create agent" link that opens `REQ-WEB-045`, and the keyboard hint "↑↓ to move · Enter to pick". "Start session" creates the session (`API-WEB-005`) and sends the first message (`API-WEB-007`).

- `AC-WEB-044`: Given agents `coder` and `helper`, when the owner types "cod" and presses Enter on `coder`, then the picker selects `coder`; "Start session" creates a session with agent `coder`; the first message starts a turn; the arrow keys move the highlight.

### Rev 2: settings, agents, skills, and channels

#### REQ-WEB-042: Structured forms for entities

**Supersedes REQ-WEB-020 and the raw-only rule of `Q-009` (rev 2)** for the entities below. The settings and entity pages shall edit these with forms, through one endpoint per entity: providers (`[providers.<name>]`), agents (`[agents.<name>]`), MCP servers (`[mcp.<name>]`), the Telegram channel (`[channels.telegram]` and its `[[routes]]` entries for channel `telegram`), and the web address (`[web] addr`). Each save changes only the table it owns (`REQ-WEB-043`). Values under `mcp.*.env` and `mcp.*.headers` show as `***` in the forms and in the raw view. A `***` value that is saved unchanged keeps the stored value (`REQ-WEB-020` masking rule, kept).

- `AC-WEB-045`: Given the owner changes `base_url` in the form for `[providers.openai]`, when the save succeeds, then only lines inside `[providers.openai]` change, every other byte of `config.toml` stays the same, and the response has `restart_required: true`.

#### REQ-WEB-043: Section rewrite keeps the rest of the file

A form save shall replace only the TOML table it owns, from its header line to the line before the next table header (`[...]` or `[[...]]`). A `[[routes]]` entry is replaced as one entry. Comments and all other tables stay byte for byte. A new table is appended to the end of its section group. The candidate file shall pass `config.Check` before the atomic write (`REQ-WEB-021`), shall use `expected_version` (`REQ-WEB-022`), and shall write one audit line (`REQ-WEB-056`). An invalid candidate is not written, and the response lists each error with file and key.

- `AC-WEB-046`: Given a `config.toml` with a comment above `[agents.coder]` and an unrelated `[providers.openai]`, when the owner saves `model` of `coder` through the form, then the comment and the providers table are byte-identical; given a candidate with `agents.coder.provider = "nope"`, then the response is 422 `validation_failed` naming `agents.coder.provider` and the file is unchanged; given an old `expected_version`, then 409 `stale_version`.

#### REQ-WEB-044: Raw config editor and restart banner

**Extends REQ-WEB-020 and REQ-WEB-023 (rev 2).** Settings › Config file keeps the raw editor with line numbers (`API-WEB-013`, `API-WEB-014`). While `restart_required` is true, every settings page shows the banner "Saved N min ago. Restart the daemon for the saved changes to take effect." with a "Restart daemon" button. The button starts the restart flow (`REQ-WEB-035`).

- `AC-WEB-047`: Given a form save three minutes ago, when the owner opens Settings › Config file, then the banner reads "Saved 3 min ago. Restart the daemon for the saved changes to take effect." and "Restart daemon" opens the restart confirmation.

#### REQ-WEB-045: Agents list, create, and duplicate

The agents page shall list agents in a table with: the name, an avatar, and a description; a "Default" badge; the model and provider; a permission badge; the tools count; the sessions count; and the last active time. It offers a search box and "+ Create agent". The default badge marks the agent that the new session view preselects (`Q-016`). The create dialog takes a name (`[a-z0-9-]`), a template (Assistant, Coder, or Worker, the existing `agenthome` templates), and a workspace. It writes `[agents.<name>]` and creates the agent home (spec #5). The agent detail header offers "Duplicate" and "Start session". Duplicate asks for a new name and copies the agent's table and `AGENT.md` (`Q-025`). No agent can be deleted in rev 2.

- `AC-WEB-048`: Given no agent `reviewer`, when the owner creates `reviewer` from the Worker template, then `[agents.reviewer]` and `agents/reviewer/AGENT.md` exist and the list shows `reviewer`; given the name `Reviewer!`, then 422 `validation_failed`; when the owner duplicates `coder` as `coder-2`, then `coder-2` has the same table and the same `AGENT.md`, and no `MEMORY.md`.

#### REQ-WEB-046: Agent keys `description` and `disabled_tools`

The agent config shall accept two new keys. `description` is a single-line string of at most 120 characters. `disabled_tools` is a list of names, each `<tool>` or `<mcp_server>/<tool>`, that are turned off for the agent. Both keys are checked by `config.Check` and by the loader. Unknown names in `disabled_tools` are kept and have no effect (`Q-024`). A config without the keys behaves as before. Both keys take effect after a restart (`Q-008`).

- `AC-WEB-049`: Given a `description` of 121 characters, when the owner saves it, then the response is 422 `validation_failed` naming `agents.<name>.description`; given `disabled_tools = ["bash", "github/create_issue"]` and a restart, then `bash` is not offered to the model for that agent, and `config.Check` passes for the file.

#### REQ-WEB-047: Agent detail tabs and the shared USER.md

**Extends REQ-WEB-025 and supersedes its USER.md part (rev 2).** The agent detail page shall have the tabs General, Persona and memory, Tools, Skills, MCP, Rules, and Workspace.

- **General**: provider and model selects, a fallback list with "+ Add fallback", permission tiles (Ask, Trust, Never), and limits `max_steps` and `context_window`. Saving writes `[agents.<name>]` and sets `restart_required`.
- **Persona and memory**: a switcher for `AGENT.md`, `USER.md`, and `MEMORY.md`, with a character counter and the limit, an editor, and "Revert" and "Save". `USER.md` is the shared file at `$LANA_HOME/USER.md`, labelled "About you. Shared by every agent." (spec #5 rev 4). `AGENT.md` and `MEMORY.md` belong to the agent. Saves follow `REQ-WEB-025`. Spec #5 IDs: `REQ-HOME-003` (shared file), `REQ-HOME-004` (migration).
- **Tools**: groups Files, Shell, Web, Memory, Skills, and Chat, then one group per MCP server. Each tool has an On switch. An "Asks first" badge marks a tool that needs approval under policy Ask. "Turn all off" applies to a group. A switch writes `disabled_tools`.
- **Skills**: the skills visible to this agent. "+ Add skill" opens a dialog that lists global skills. Removing a skill removes its name from the agent's `skills` list; the skill files stay.
- **MCP**: the servers with tool count and status, and an On switch that edits the agent's `mcp` list. "+ Add server" opens `REQ-WEB-051`.
- **Rules**: the allow and deny lists (`rules.allow`, `rules.deny`).
- **Workspace**: the workspace path (`workspace`).

- `AC-WEB-050`: Given agent `coder`, when the owner turns off `bash` in Tools and saves, then `disabled_tools` contains `bash` and `restart_required` is true; when the owner saves a new `AGENT.md` text, then the next turn's prompt of `coder` contains it without a restart; when the owner saves `USER.md`, then `$LANA_HOME/USER.md` changes and the next prompt of any agent contains it; removing a skill in the Skills tab changes only the agent's `skills` list.

#### REQ-WEB-048: Skills list and delete

**Supersedes the "no delete" rule of REQ-WEB-026 and the skills part of `Q-011` (rev 2).** The skills page shall list skills in a table with: the skill name and description; a scope badge (`Global` or `agent:<name>`); the size in characters; and the updated time. It offers a search box, a scope filter, and the summary "N skills · M can't load", where N counts valid skills and M counts invalid ones. Each invalid skill shows a "Can't load" badge with the reason. The table shows 8 rows per page. A skill can be deleted after a confirmation. Deleting removes the skill's folder and nothing else. Agent files still cannot be deleted. A skill deleted while an agent lists it keeps the name in that agent's `skills` list; the runtime skips a listed skill that does not exist, and the Skills page does not show it (`Q-026`).

- `AC-WEB-051`: Given ten valid skills and one invalid skill, when the page opens, then page 1 shows eight rows, the summary reads "10 skills · 1 can't load", and the invalid row shows its reason; given the owner deletes `git-workflow` after confirmation, then its folder is gone, and an agent that listed it still starts turns without an error.

#### REQ-WEB-049: Skill editor

The skill editor shall have: a name (read-only after creation); a scope (Global or one agent); a description of at most 300 characters with a counter; the agents list; the folder path; instructions in Markdown with a counter of the form `N / 20,000`; "Cancel"; and "Save". For a global skill, the agents list shows the agents whose `skills` list contains the skill, and "Add agent" adds the skill to that agent's list. A team added with "Add agent" means every member of the team (spec #10). Save runs the frontmatter validation of `REQ-WEB-026` and the version check of `REQ-WEB-022`.

- `AC-WEB-052`: Given a description of 301 characters, when the owner saves, then the response is 422 `validation_failed` naming `description` and nothing is written; given the owner adds agent `coder` to global skill `git-workflow`, then the `skills` list of `coder` contains `git-workflow` after the save.

#### REQ-WEB-050: MCP list and detail

The MCP page shall list servers in a table with: the name; the command or the URL; the type (Command or URL); the tools count; the agents that use the server (avatars, or "No agents"); and the status. It offers a search box, a status filter, and paging. The status comes from the MCP client state: `running` shows Connected, `unavailable` shows Unreachable, and `stopped` shows Idle. The label "Needs sign-in" is part of the UI copy but is not produced in v1, because no client state reports an authentication failure (see `design.md`).

The detail page shows the status, a line such as "Running for 2 h · 26 tools · used by 2 agents", and these controls: "Restart", which calls `POST /v1/mcp/{name}/restart`; and "Save changes". The sections are Connection (the definition), Agents (edits the `mcp` list of each agent), and Tools (the list from the running client, with "Show all N tools").

- `AC-WEB-053`: Given server `docs` in state `unavailable`, when the owner presses "Restart", then the client is stopped and started from the loaded definition, the response is `200` with `state` and `error`, and the list shows the new status; given a server name that is not configured, then `404 not_found`.

#### REQ-WEB-051: Add an MCP server with a test

The add server dialog shall take: a name; a type, Command or URL; the command and its arguments, or the URL; environment rows (for Command, the `env` table) or header rows (for URL, the `headers` table) as key and value pairs, with values masked; and a "Test" action. "Test" calls `POST /v1/mcp/test`, which starts a client from the given definition, lists its tools, stops the client, and returns `ok` and `tool_count`, or the error. The result line reads "Connected · found 18 tools", or the error, with "Test again". The dialog says "You pick agents after adding it." Saving does not need a passing test.

- `AC-WEB-054`: Given a valid command server, when the owner presses "Test", then the result line reads "Connected · found N tools" with the real count; given a command that does not exist, then the test returns `ok: false` with the error, and "Save" still saves the server.

#### REQ-WEB-052: Channels page and the Telegram detail

**Extends spec #6 rev 3 (rev 2): `REQ-DMN-005` (`enabled`), `REQ-DMN-006` (token missing keeps the web view running), `REQ-TG-017` (bot identity), `REQ-TG-018` (status), `REQ-TG-019` (disconnect).** The channels page shall show cards:

- **Telegram**: status Connected, Token missing, or Off; the line "@bot · N chats · N owner"; "Manage".
- **Web**: status On, this dashboard address, and a link to Settings.
- **CLI**: "Built in" and "lana chat in your terminal".
- **Discord, Slack, WhatsApp**: "Coming soon". These cards are not interactive.

The Telegram detail page shall show: the token variable (the secret name, `REQ-WEB-055`) and a check that calls Bot API `getMe`, which shows "Token found. Bot is @x" (spec #6 rev 3); the owners, each with its ID and a display name if the bot has seen it since start, and "Add owner"; a chats table with chat name, Private or Group, chat ID, agent, and "Remove", where each row is one `[[routes]]` entry; "+ Add chat" (`REQ-WEB-053`); and an Advanced section with the API base. "Disconnect" saves `[channels.telegram] enabled = false`. The key `enabled` is new and defaults to `true` (spec #6 rev 3). The header shows "polling for N h · last message N min ago".

- `AC-WEB-055`: Given a token variable whose value Bot API `getMe` accepts, when the owner runs the check, then the page shows "Token found. Bot is @x"; given the token variable is unset, then the card status is "Token missing" and the check returns `ok: false` with a message; when the owner presses "Disconnect", then `enabled = false` is saved, the card shows Off, `restart_required` is true, and after a restart the daemon does not poll Telegram.

#### REQ-WEB-053: Chats waiting for access

**New (rev 2, spec #6 `REQ-TG-016`, spec #6 `Q-009`: at most 50 records).** The Telegram channel shall keep in memory each chat that wrote to the bot and has no route, for 24 hours from its last message. The "+ Add chat" dialog shall list these chats under "Chats waiting for access", with the chat name, type, and time of the last message. The dialog also has a manual chat ID field. Choosing a waiting chat and an agent writes a `[[routes]]` entry (`REQ-WEB-042`). The list is not written to disk. It is empty after a restart until the chats write again (`Q-027`).

- `AC-WEB-056`: Given a group chat writes to the bot without a route, when the owner opens "+ Add chat", then the chat is listed under "Chats waiting for access"; after 24 hours, or after a route for that chat is saved, the chat is not listed; after a restart, the chat is not listed until it writes again.

#### REQ-WEB-054: Settings › General

**New (rev 2).** Settings › General shall show: **Daemon** (state Running or Healthy, PID, uptime, `LANA_HOME`, and "Restart daemon" as in `REQ-WEB-035`); **Web dashboard** (the address, editable and saved to `[web] addr`, with the note "Changing the address needs a daemon restart", the number of signed-in browsers, and "Sign out other browsers" as in `REQ-WEB-036`); **Appearance** (Light, Dark, System, as in `REQ-WEB-031`); and **About** (lana version, Go version, operating system, and architecture).

- `AC-WEB-057`: Given the owner saves the address `127.0.0.1:18301`, then `[web] addr` changes, `restart_required` is true, and the page says the address applies after a restart; given the owner saves `0.0.0.0:18300`, then the response is 422 `validation_failed` naming `web.addr`, as `REQ-WEB-003` requires.

#### REQ-WEB-055: Secrets with update time and usage

**Extends REQ-WEB-024 (rev 2).** Settings › Secrets shall list each name with a masked value (`set` or `unset`, never a value), "Used by" (the config tables that reference the name), the time it was last set, and the buttons "Replace" and "Remove". The time comes from `$LANA_HOME/secrets.meta.json` (mode `0600`). That file holds names and times only (`Q-022`). The page shows the notice "Values are write-only. You can replace or remove them, not read them." Write-only rules of `REQ-WEB-024` still apply.

- `AC-WEB-058`: Given `OPENAI_API_KEY` was set through the web view at time `T`, when the owner lists secrets, then the entry shows `set`, the time `T`, and "Used by" with `providers.openai`; no value appears in the response; and `secrets.meta.json` holds the name and the time only.

#### REQ-WEB-056: Audit file and Audit log page

**Extends REQ-WEB-027 (rev 2).** Each successful save or action listed below shall append one JSON line to `$LANA_HOME/logs/audit.jsonl` (mode `0600`) with the fields `time` (Unix ms), `kind`, `target`, and `changed_keys` (key paths or secret names only). The actions are: config table saves, raw config saves, secret set and remove, agent file saves, skill saves and deletes, agent creates and duplicates, provider, MCP, and Telegram changes, web address changes, setup saves, and restarts. The line never holds a value. The daemon keeps the stderr `info` line of `REQ-WEB-027`. Settings › Audit log lists the entries newest first through `GET /v1/audit`, with paging. The file is not rotated in v1 (`Q-021`).

- `AC-WEB-059`: Given the owner saves `model` and `permission` of `coder`, when the owner opens Settings › Audit log, then one entry shows `kind` `config.save`, `target` `config.toml`, and `changed_keys` `agents.coder.model` and `agents.coder.permission`, with no value; `audit.jsonl` has one new line for the save and mode `0600`.

#### REQ-WEB-057: Markdown in assistant text

**Added after owner testing (2026-10-30).** Assistant text in the session view (final and interim answers, team member replies, and the lead answer) shall render Markdown: paragraphs, headings, bold, italic, strikethrough, inline code, fenced code blocks with a copy action, ordered and unordered lists (one nested level), block quotes, horizontal rules, tables, and links. Only `http`, `https`, and `mailto` links become links; they open in a new tab. The renderer shall not insert raw HTML. Text that is still streaming and has unclosed markup shall show as plain text until it is closed. User messages stay plain text.

- `AC-WEB-060`: Given an assistant answer with `**Web**`, a `-` list, `` `go test` ``, a fenced block, and `<script>alert(1)</script>`, when the session view renders it, then "Web" is bold, the list is a list, the code shows in a monospace style, the block has a Copy action, and the script text shows as literal text and does not run.

### Rev 2: daemon, browsers, and scenarios

#### REQ-WEB-035: Restart the daemon from the browser

**New (rev 2, `Q-019`).** The owner may restart the daemon from the browser: from Settings › General, from the restart banner (`REQ-WEB-044`), and as the last step of setup (`REQ-WEB-034`). The page shows a confirmation dialog that lists the running turns (session title and agent). A confirmed restart shall:

1. End each running turn with stop reason `cancelled`.
2. Send `event: shutdown` on every SSE stream (`REQ-WEB-029`).
3. Stop the listener and close the store.
4. Run the same binary with the same arguments in the same process, so the PID does not change, and keep the daemon lock held (`design.md`, Restart in place).

Config and secret changes take effect after the restart. This is a restart, not a hot reload (`Q-008`). The control is hidden on Windows (`Q-019`).

- `AC-WEB-038`: Given one running turn in session `S` and an open SSE stream, when the owner confirms the restart, then the turn ends `cancelled`, the stream receives `event: shutdown`, the PID is the same after the restart, a second `lana daemon` started during the restart refuses to start because the lock is still held, and `GET /v1/daemon` returns a new `started_at`.

#### SC-005: First run in the browser

**Given** a new `LANA_HOME` with no `config.toml`
**When** the owner opens the dashboard, unlocks with the owner token, picks OpenAI, pastes a key, runs "Test connection", names the agent `coder` with the Coder template, edits the persona, and presses "Continue"
**Then** the test passes, the files are written, the daemon restarts in place, and step 5 offers "Start your first session" (`AC-WEB-035`, `AC-WEB-037`)

#### SC-006: Change a model and restart from the browser

**Given** a running turn of `coder` and an open stream
**When** the owner changes the model of `coder` in the form and presses "Restart daemon" in the banner, then confirms
**Then** the turn ends `cancelled`, the browser reconnects after the restart, and new turns use the new model (`AC-WEB-038`, `AC-WEB-047`)

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
| `REQ-WEB-030`, `REQ-WEB-031` | Routes, theme, sidebar state (client only) | — (UI) |
| `REQ-WEB-032`, `REQ-WEB-033`, `REQ-WEB-034` | Setup status, provider test, setup save | `API-WEB-018`, `API-WEB-019`, `API-WEB-021` |
| `REQ-WEB-035`, `REQ-WEB-054` | Daemon status and restart in place | `API-WEB-022`, `API-WEB-023` |
| `REQ-WEB-036` | Browser session records and revoke others | `API-WEB-002` (extended), `API-WEB-024` |
| `REQ-WEB-037` | Session list filters, facets, preview, status | `API-WEB-003` (extended) |
| `REQ-WEB-038`, `REQ-WEB-040`, `REQ-WEB-041` | Chat view, composer, new session (rendering and client calls) | `API-WEB-004`, `API-WEB-005`, `API-WEB-007`, `API-WEB-009` |
| `REQ-WEB-039` | Approval answer with the three outcome buttons | `API-WEB-011` |
| `REQ-WEB-042`, `REQ-WEB-043`, `REQ-WEB-044` | Providers, agents, and Telegram forms; section rewrite; raw editor | `API-WEB-013`, `API-WEB-014`, `API-WEB-020`, `API-WEB-025`, `API-WEB-028`, `API-WEB-029` |
| `REQ-WEB-045`, `REQ-WEB-046`, `REQ-WEB-047` | Agents list, create, duplicate, detail tabs, shared `USER.md` | `API-WEB-012` (extended), `API-WEB-016` (extended), `API-WEB-025`, `API-WEB-026` |
| `REQ-WEB-048`, `REQ-WEB-049` | Skills list, editor, delete | `API-WEB-017` (extended) |
| `REQ-WEB-050`, `REQ-WEB-051` | MCP list, detail, restart, add with test | `API-WEB-027` |
| `REQ-WEB-052`, `REQ-WEB-053` | Channels, Telegram detail, waiting chats | `API-WEB-028` |
| `REQ-WEB-055` | Secrets with update time and usage | `API-WEB-015` (extended) |
| `REQ-WEB-056` | Audit file and audit page | `API-WEB-030` |
| `REQ-WEB-057` | Markdown in assistant text | — (UI) |

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
| Running (own session) | Press stop (send button, rev 2) | Same as Cancel: turn ends `cancelled` | Yes |
| Any state, daemon running | Restart daemon, confirmed (rev 2) | Running turns end `cancelled`; SSE `shutdown`; same PID | Yes |
| Any state, daemon running | Restart daemon, not confirmed (rev 2) | Nothing | No |
| Setup needed | Any page except Unlock (rev 2) | Setup wizard shown | — |

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
| Provider test fails during setup or a provider save | Nothing written; error shown inline | 422 `connection_failed` (save); `ok: false` (test) |
| Setup save when setup is not needed | Nothing written | 409 `setup_not_needed` |
| Restart without confirmation | Nothing restarted | 400 `invalid_request` |
| Restart in place fails after the listener closed | Daemon exits with the error; the owner starts it again from a terminal | 500 `restart_failed` before exec; exit 1 after exec |
| `web-sessions.json` cannot be parsed | Daemon refuses to start; the owner is told which file to fix | exit 2, names the file (`Q-020`) |
| Secret name has no `secrets.meta.json` entry | Shown as "—" in the Updated column | — |
| Agent `description` over 120 characters | Nothing written | 422 `validation_failed` |
| Skill description over 300 characters, or body over 20,000 | Nothing written | 422 `validation_failed` |
| Telegram token variable unset when the check runs | Check reports the state; nothing changes | 200 with `ok: false` |
| MCP restart for a server that is not configured | Nothing changed | 404 `not_found` |
| Skill delete with a stale `expected_version` | Nothing deleted | 409 `stale_version` |
| `[web] addr` not loopback (saved from the page) | Nothing written | 422 `validation_failed` naming `web.addr` |

## Authorization and security

- Only the owner, after login, can call the API. Login is the only unauthenticated endpoint, plus the static dashboard
  assets. Restart, sign-out of other browsers, and every write need the cookie and the CSRF header (rev 2).
- Browser session records hold hashes only (`REQ-WEB-036`). A file that holds a plain session or CSRF value fails the
  scan in `AC-WEB-039`.
- Provider keys entered in setup or in a provider form go to `secrets.env` and are never returned, logged, or shown
  again. The inline key of a connection test is never stored (`REQ-WEB-033`).
- The restart control restarts the daemon, which is a privileged action on the owner's machine. It needs the same
  cookie and CSRF header as every write, and a confirmation in the page (`REQ-WEB-035`).
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
- Rev 2 files written by the daemon: `web-sessions.json` (browser records, `REQ-WEB-036`), `secrets.meta.json` (names
  and times, `REQ-WEB-055`), `logs/audit.jsonl` (`REQ-WEB-056`), and the existing `config.toml`, `secrets.env`, agent
  files, and skills. Each write is atomic (temporary file and rename) and keeps the mode.
- A form save rewrites one TOML table. Two saves that touch different tables still take the one write lock, so they
  never interleave (`REQ-WEB-043`).
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
**Then** an inline approval card opens above the composer with `Allow once`, `Allow for this session`, and `Deny` (rev 2, `REQ-WEB-039`); the owner picks `Allow once`; the tool runs and the card shows the outcome in words (`AC-WEB-016`, `AC-WEB-042`)

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
| `Q-001` | May the web view listen on a non-loopback address in v1? | `REQ-WEB-003` | ADR 0008 (`0.0.0.0` requires a token) | No. Loopback only. Refuse other addresses with exit 2. Remote access, TLS, and a reverse proxy are a later spec | Ridho | Answered | Accepted agent recommendation (owner, 2026-10-30) |
| `Q-002` | How is the owner token created, stored, shown, and how long does a browser login last? | `REQ-WEB-005`, `REQ-WEB-006`, Failure cases | ADR 0008 says `lana init` creates the token and stores a hash; `lana init` does not create one today | Daemon creates `$LANA_HOME/web.json` (mode 0600) on first start if it is missing, with a random 32-byte token; stores only its SHA-256; prints the token once to stderr at creation; cookie lasts 12 hours idle; a new token is made by deleting `web.json` and restarting. This deviates from ADR 0008 where it says `lana init`, because `lana init` runs before the web view exists | Ridho | Answered; the "lost on stop" rule of sessions is superseded by `Q-020` (rev 2) | Accepted agent recommendation (owner, 2026-10-30) |
| `Q-003` | Which HTTP namespace and auth model does the browser API use? `/v1` as in ADR 0014 with bearer tokens, or a private browser namespace? | `API-WEB-*` paths, `REQ-WEB-005` | ADR 0014 (`/v1`, `[[api.tokens]]`); ADR 0008 (login token) | Use the `/v1` paths in this contract, with cookie auth for the browser. Bearer tokens for API clients stay out of this spec. Write a new ADR (0016) for the owner-auth model before design approval | Ridho | Answered | Accepted agent recommendation (owner, 2026-10-30) |
| `Q-004` | Which front-end stack? | `design.md` assets, build and Nix | ADR 0008 (Preact + `htm`, no build step, under 200 KB) | Follow ADR 0008: pinned Preact and `htm` files vendored under `web/assets/vendor/`, plain ES modules, no Node in `flake.nix`. Alternative: plain JS without a framework | Ridho | Answered | Accepted agent recommendation (owner, 2026-10-30) |
| `Q-005` | May `lana daemon` run with the web view only, without `[channels.telegram]`, and is the web view on by default? | `REQ-WEB-004`, `REQ-WEB-001` | Spec #6 `REQ-DMN-003` and API-DMN-001 (exit 2 today) | Yes. Web view on by default when `[web]` is absent. Revise spec #6 to r2 for the new no-channel rule | Ridho | Answered | Accepted agent recommendation (owner, 2026-10-30) |
| `Q-006` | Which sessions are listed, and what channel and address do web sessions use? | `REQ-WEB-008`, `REQ-WEB-010` | Spec #6 `Q-007` (workspace precedent) | List every channel with a channel badge. Web sessions use channel `web` and address `owner`; workspace from the agent or `$LANA_HOME/workspaces/<agent>/` | Ridho | Answered | Accepted agent recommendation (owner, 2026-10-30) |
| `Q-007` | For a session that runs in another process, is polling of the event log (no token stream) acceptable? | `REQ-WEB-012` | `lana chat` runs its own runtime; live frames are in-process only | Yes. Poll the log about once per second and show the PID. Token streaming for other processes needs a cross-process frame feed, which is a later ADR | Ridho | Answered | Accepted agent recommendation (owner, 2026-10-30) |
| `Q-008` | When does a saved config take effect? | `REQ-WEB-023`, `API-WEB-014` | Runtime is built once at start (`lana.New`); no reload path | Save and validate now; no hot reload in v1; show `restart required`; the owner restarts `lana daemon`. Hot reload is a later change: rebuild the runtime when no turn runs, and update the Telegram backend too | Ridho | Answered; restart in place added by `Q-019` (rev 2) | Accepted agent recommendation (owner, 2026-10-30) |
| `Q-009` | What is the editing surface for `config.toml`? | `REQ-WEB-020`, `REQ-WEB-021`, `API-WEB-013`, `API-WEB-014` | `lana config edit` opens `$EDITOR` today | v1: read-only structured view plus a raw TOML editor validated on save (comments kept). Values under `mcp.*.env` and `mcp.*.headers` are masked in the editor and kept on save. Structured forms can come later; a TOML writer for them drops comments | Ridho | Answered; raw-only part superseded by `Q-018` (rev 2) | Accepted agent recommendation (owner, 2026-10-30). Rev 2 keeps the raw editor and adds forms for the entities in `REQ-WEB-042` |
| `Q-010` | May the browser write secrets at all, and which names? | `REQ-WEB-024`, `API-WEB-015` | Spec #1 secrets rule (process env wins, never printed) | Yes. Set and unset named secrets in `secrets.env` (mode 0600). Never reveal. Show `set` or `unset` and `overridden by process env`. Provider keys and the Telegram token are included, and take effect at restart | Ridho | Answered | Accepted agent recommendation (owner, 2026-10-30) |
| `Q-011` | Which agent-file and skill edits are allowed in v1? | `REQ-WEB-025`, `REQ-WEB-026` | Spec #5 and spec #7 | Edit `AGENT.md`, `USER.md`, `MEMORY.md` (limits enforced by refusing the save), and create or edit skills (global and agent-owned). No delete in v1 | Ridho | Answered; skills part superseded by `Q-026` (rev 2) | Accepted agent recommendation (owner, 2026-10-30). Agent files still have no delete |
| `Q-012` | May the owner set policy `trust` from the browser? | `REQ-WEB-017` | ADR 0004 (`trust` runs without approval) | Yes, behind a confirmation dialog; recorded as `approval/policy` like `/permission` | Ridho | Answered | Accepted agent recommendation (owner, 2026-10-30) |
| `Q-013` | Where and how are settings changes audited? | `REQ-WEB-027` | Spec #1 logs (`$LANA_HOME/logs/lana.log`) and the daemon stderr log | One `info` line per save on the daemon stderr log with key names and no values. No audit table | Ridho | Answered; the no-table part superseded by `Q-021` (rev 2) | Accepted agent recommendation (owner, 2026-10-30) |
| `Q-014` | Which outcome does an approval timeout produce? | `REQ-WEB-015` | ADR 0004 says `unavailable`; spec #6 (approved) uses `rejected` for Telegram | Use `unavailable` for the web view, as ADR 0004 says, and revise spec #6 to match (or record an ADR that changes it) | Ridho | Answered | Accepted agent recommendation (owner, 2026-10-30) |
| `Q-015` | Default address and config key for the web view | `REQ-WEB-001` | ADR 0008 (`127.0.0.1:18300`, configurable) | `[web] addr = "127.0.0.1:18300"` and `[web] enabled = true` | Ridho | Answered | Accepted agent recommendation (owner, 2026-10-30) |

Rev 2 questions (owner lifted the approval gate on 2026-10-30; each resolution is the agent recommendation, recorded with the prefix `Rekomendasi agent:`):

| ID | Question | Controls | Requested reference | Agent recommendation | Owner | Status | Resolution |
|---|---|---|---|---|---|---|---|
| `Q-016` | Which routes, default landing page, and sidebar behavior does the dashboard have? | `REQ-WEB-030`, `REQ-WEB-031` | Dashboard mockup `design/export/screens/` (2026-10-30) | Use the routes in `REQ-WEB-030`. Land on `#/sessions` after login. Home is an empty state in v1. The sidebar is 280 px and collapses to icons. Theme and sidebar state live in `localStorage`. There is no default-agent key in v1, so the "Default" badge and the preselected agent use the first agent in name order | Ridho | Answered | Rekomendasi agent: accept the routes and sidebar as in the mockup. Ridho, lifted gate 2026-10-30. |
| `Q-017` | When does the setup wizard appear, and what does it cover? | `REQ-WEB-032`, `REQ-WEB-034`, `API-WEB-018`, `API-WEB-021` | Mockup screens 01 to 05; owner brief decision 2 | Show the wizard only when `GET /v1/setup` returns `needed: true` (no `config.toml`, no provider, or no agent). Steps run in order: Unlock, Provider, Connect, Agent, Persona, Ready. The wizard does not add a second provider or agent; later changes use the settings forms. Ridho, lifted gate 2026-10-30. | Ridho | Answered | Rekomendasi agent: accept. Ridho, lifted gate 2026-10-30. |
| `Q-018` | Which pages write `config.toml` through forms, and how is the rest of the file kept? | `REQ-WEB-042`, `REQ-WEB-043`, `API-WEB-020`, `API-WEB-025` | Owner brief decision 3 | Forms for providers, agents, MCP servers, the Telegram channel and its routes, and `[web] addr`. Each save rewrites only the table it owns (from its header to the next header), keeps comments and other tables, runs `config.Check`, uses `expected_version`, and writes one audit line. The raw editor stays for every other key. Supersedes the raw-only part of `Q-009`. | Ridho | Answered | Rekomendasi agent: accept. Ridho, lifted gate 2026-10-30. |
| `Q-019` | How does the daemon restart from the browser without a new PID and without losing the daemon lock? | `REQ-WEB-035`, `API-WEB-023`, `design.md` (Restart in place) | Owner brief decision 4; `cmd/lana/daemon.go` takes `daemon.lock` with `flock` | Restart in place. End running turns as `cancelled`, send `shutdown`, stop the listener, close the store, then run the same binary with the same arguments. The lock file descriptor keeps its place across the exec, so the lock stays held. The confirmation lists running turns. macOS and Linux only; the control is hidden on Windows. No hot reload. | Ridho | Answered | Rekomendasi agent: accept. Ridho, lifted gate 2026-10-30. |
| `Q-020` | How do browser logins survive a restart? | `REQ-WEB-036`, `API-WEB-002`, `API-WEB-024`, ADR 0016 | Owner brief decision 4; `Q-002` said sessions are lost on stop | Keep browser session records in `$LANA_HOME/web-sessions.json` (mode `0600`): SHA-256 of the session ID and of the CSRF value, created and last-used times, a user-agent label. The 12-hour idle limit stays. A file that cannot be parsed stops the daemon with exit 2, as `web.json` does. ADR 0016 is updated. | Ridho | Answered | Rekomendasi agent: accept. Ridho, lifted gate 2026-10-30. |
| `Q-021` | Where are settings changes audited, and is the audit log paged in the UI? | `REQ-WEB-056`, `API-WEB-030` | Owner brief decision 13. Supersedes the no-table part of `Q-013` | Append one JSON line per save or action to `$LANA_HOME/logs/audit.jsonl` (mode `0600`): time, kind, target, and changed key names, never a value. Keep the stderr line. Read it through `GET /v1/audit` with paging. No rotation or export in v1. | Ridho | Answered | Rekomendasi agent: accept. Ridho, lifted gate 2026-10-30. |
| `Q-022` | Where does the "Updated" time of each secret come from? | `REQ-WEB-055`, `API-WEB-015` | Owner brief decision 13 asks the agent to pick one: a new file, or the file modification time | Use `$LANA_HOME/secrets.meta.json` (mode `0600`): one set time in milliseconds per name, written on set and on remove. It holds no values. The modification time of `secrets.env` is one time for all names, so it is not used. A name with no entry shows "—". | Ridho | Answered | Rekomendasi agent: `secrets.meta.json`. Ridho, lifted gate 2026-10-30. |
| `Q-023` | Should the Channel facet include library sessions? | `REQ-WEB-037`, `API-WEB-003` | Owner brief decision 6 lists Web, CLI, and Telegram; `REQ-WEB-008` lists four channels | Keep the facet to Web, CLI, and Telegram, as the brief says. Library sessions appear in the unfiltered list, and `channel=library` works in the API, but there is no facet chip. The owner can ask for a fourth chip. | Ridho | Answered | Rekomendasi agent: follow the brief (three chips). Ridho, lifted gate 2026-10-30. |
| `Q-024` | What do `description` and `disabled_tools` accept, and what happens to unknown tool names? | `REQ-WEB-046`, `API-WEB-025` | Owner brief decision 8 | `description`: one line, at most 120 characters. `disabled_tools`: a list of `<tool>` or `<mcp_server>/<tool>`. The syntax is checked. A name that matches no tool is kept and has no effect. Both take effect after a restart. | Ridho | Answered | Rekomendasi agent: accept. Ridho, lifted gate 2026-10-30. |
| `Q-025` | What does "Duplicate" copy? | `REQ-WEB-045`, `API-WEB-025` | Owner brief decision 8 ("copies the config table and AGENT.md") | Copy `[agents.<name>]` and `AGENT.md` only. Do not copy `MEMORY.md` (the new agent starts with no memory), `USER.md` (shared), or the agent's skill folders. The new name must be new and valid. | Ridho | Answered | Rekomendasi agent: accept. Ridho, lifted gate 2026-10-30. |
| `Q-026` | What happens to an agent that lists a global skill that is later deleted? | `REQ-WEB-048`, `REQ-WEB-049`, `API-WEB-017` | Owner brief decision 10 (skills may be deleted); `Q-011` | Delete removes the skill folder only. Agent `skills` lists are not edited. The runtime skips a listed name that has no skill folder, and the Skills page does not show it. The owner removes the name in the agent's Skills tab. | Ridho | Answered | Rekomendasi agent: accept. Ridho, lifted gate 2026-10-30. |
| `Q-027` | Where are chats waiting for access kept, and for how long? | `REQ-WEB-053`, spec #6 `REQ-TG-016` | Owner brief decision 12 | In memory in the Telegram channel, for 24 hours after the last message, at most 50 chats (spec #6 `Q-009`; the oldest last message is dropped first). Lost on restart. Not written to disk. A chat leaves the list when a route is saved for it. | Ridho | Answered | Rekomendasi agent: accept (spec #6 rev 3). Ridho, lifted gate 2026-10-30. |
| `Q-028` | What happens when the owner changes `[web] addr`? | `REQ-WEB-054`, `REQ-WEB-003`, `API-WEB-029` | Owner brief decision 13; the loopback rule of `REQ-WEB-003` | Save and validate the address (the loopback rule still applies). It takes effect after a restart. The page shows the new URL after the restart and asks the owner to open it, because the browser cannot follow the change by itself. | Ridho | Answered | Rekomendasi agent: accept. Ridho, lifted gate 2026-10-30. |
| `Q-029` | The repo has only the `openai_compat` provider adapter. How does setup connect Anthropic? | `REQ-WEB-032`, `API-WEB-019` | `providers/openai` is the only adapter; `kind = "anthropic"` validates but has no runtime adapter | Rekomendasi agent: until a native Anthropic adapter exists, setup writes `kind = "openai_compat"`, `base_url = "https://api.anthropic.com/v1"` (Anthropic's OpenAI-compatible endpoint), `api_key_env = "ANTHROPIC_API_KEY"`. The provider test also uses this adapter | Ridho (lifted gate) | Answered | Added during implementation, 2026-10-30 |

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
| `REQ-WEB-030` | `AC-WEB-033` | Owner brief decision 1, mockup `dashboard-home`, `Q-016` |
| `REQ-WEB-031` | `AC-WEB-034` | Owner brief decision 1, mockup `settings-general`, `Q-016` |
| `REQ-WEB-032` | `AC-WEB-035` | Owner brief decision 2, mockup `01` to `05`, `Q-017` |
| `REQ-WEB-033` | `AC-WEB-036` | Owner brief decision 5, mockup `03b-connect-model-error` |
| `REQ-WEB-034` | `AC-WEB-037` | Owner brief decision 2, spec #5 rev 4, `Q-019` |
| `REQ-WEB-035` | `AC-WEB-038` | Owner brief decision 4, `Q-019`, `Q-008` |
| `REQ-WEB-036` | `AC-WEB-039` | Owner brief decision 4, ADR 0016 rev 2, `Q-020`, `Q-002` |
| `REQ-WEB-037` | `AC-WEB-040` | Owner brief decision 6, mockup `sessions-filter`, `Q-023` |
| `REQ-WEB-038` | `AC-WEB-041` | Owner brief decision 7, mockup `sessions-chat`, `team-message-states` (team part: spec #10) |
| `REQ-WEB-039` | `AC-WEB-042` | Owner brief decision 7, `REQ-WEB-014`, `REQ-WEB-015` (superseded) |
| `REQ-WEB-040` | `AC-WEB-043` | Owner brief decision 7, `Q-012`, spec #11 (question lock) |
| `REQ-WEB-041` | `AC-WEB-044` | Owner brief decision 7, mockup `sessions-new-session-agent-picker` |
| `REQ-WEB-042` | `AC-WEB-045` | Owner brief decision 3, `Q-018` |
| `REQ-WEB-043` | `AC-WEB-046` | Owner brief decision 3, `REQ-WEB-021`, `REQ-WEB-022` |
| `REQ-WEB-044` | `AC-WEB-047` | Owner brief decision 3 and 4, `Q-009` (raw editor kept) |
| `REQ-WEB-045` | `AC-WEB-048` | Owner brief decision 8, mockup `agents-list`, `Q-025` |
| `REQ-WEB-046` | `AC-WEB-049` | Owner brief decision 8, `Q-024` |
| `REQ-WEB-047` | `AC-WEB-050` | Owner brief decisions 8 and 9, spec #5 rev 4 |
| `REQ-WEB-048` | `AC-WEB-051` | Owner brief decision 10, `Q-011`, `Q-026` |
| `REQ-WEB-049` | `AC-WEB-052` | Owner brief decision 10, mockup `skills-editor` |
| `REQ-WEB-050` | `AC-WEB-053` | Owner brief decision 11, mockup `mcp-list`, `mcp-detail` |
| `REQ-WEB-051` | `AC-WEB-054` | Owner brief decisions 5 and 11, mockup `mcp-add-server` |
| `REQ-WEB-052` | `AC-WEB-055` | Owner brief decision 12, spec #6 rev 3, mockup `channels-*` |
| `REQ-WEB-053` | `AC-WEB-056` | Owner brief decision 12, spec #6 rev 3, `Q-027` |
| `REQ-WEB-054` | `AC-WEB-057` | Owner brief decision 13, `Q-028` |
| `REQ-WEB-055` | `AC-WEB-058` | Owner brief decision 13, `Q-022` |
| `REQ-WEB-056` | `AC-WEB-059` | Owner brief decision 13, `Q-021` |
| `REQ-WEB-057` | `AC-WEB-060` | Owner testing feedback 2026-10-30 |

### Rev 2 changes

| Rev 1 item | Rev 2 change | Replaced or extended by |
|---|---|---|
| `REQ-WEB-005` login page | The login page is the Unlock step of the setup wizard | `REQ-WEB-030`, `REQ-WEB-032` |
| `REQ-WEB-014`, `REQ-WEB-015` approval dialog | Inline approval card with three buttons | `REQ-WEB-039` |
| `AC-WEB-017` "dialog reads Approval expired" | The card reads `Approval expired` | `REQ-WEB-039` |
| `AC-WEB-030` approval dialog announces its outcome | The card announces its outcome | `REQ-WEB-039` |
| `REQ-WEB-020` read-only settings | Forms per entity; raw editor kept | `REQ-WEB-042`, `REQ-WEB-043`, `REQ-WEB-044` |
| `REQ-WEB-025` `USER.md` per agent | One shared `USER.md` | `REQ-WEB-047`, spec #5 rev 4 |
| `REQ-WEB-026` no skill delete | Skills can be deleted | `REQ-WEB-048` |
| `Q-002` sessions lost on stop | Sessions survive a restart in place | `REQ-WEB-036`, `Q-020` |
| `Q-008` no restart from the browser | Restart in place; still no hot reload | `REQ-WEB-035`, `Q-019` |
| `Q-009` raw editor only | Forms for entities; raw editor kept | `Q-018` |
| `Q-011` skills not deleted | Skills can be deleted; agent files cannot | `Q-026` |
| `Q-013` no audit table | Audit file added; stderr line kept | `Q-021` |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit or stated as not applicable.
- [x] State transitions and destructive effects are explicit or not applicable.
- [x] Defaults, ordering, retry, and idempotency are explicit where applicable.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Contract applicability is explicit.
- [x] Blocking Open Questions is `None`.
- [x] Rev 2 superseded and extended items are marked in place and listed in "Rev 2 changes".
- **Approved by**: Ridho, lifted gate 2026-10-30 (rev 2; each `Q-016` to `Q-028` resolution is the agent recommendation)
- **Approved on**: 2026-10-30 (rev 1: 2026-10-30; rev 2: 2026-10-30)
