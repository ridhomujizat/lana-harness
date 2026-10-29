# Requirements: Daemon and Telegram channel

- **Issue**: —
- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (`lana daemon`, `[channels.telegram]` config, `[[routes]]` match keys, bot commands and messages)
- **Related ADR**: [ADR Index](../../adr/README.md) — [0003](../../adr/0003-multi-agent-with-channel-routing.md), [0004](../../adr/0004-permission-mode-per-agent.md), [0008](../../adr/0008-cli-channel-http-and-web-dashboard-interfaces.md), [0009](../../adr/0009-session-as-append-only-event-log.md), [0010](../../adr/0010-profile-and-config-patch.md)
- **Related diagram**: [Diagram Index](../../diagram/README.md) — [0001](../../diagram/0001-system-context-and-roadmap.md), [0002](../../diagram/0002-agent-loop-and-approval.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-10-29 | "lana as a worker and assistant agent, like Hermes or OpenClaw"; next step scheduler/cron and Telegram | goal |
| Interview 2026-10-29 | Split: spec #6 daemon + Telegram, spec #7 scheduler | scope |
| Interview 2026-10-29 | One running session per chat; ignore unknown senders silently; Markdown converted to Telegram HTML; one compact line per tool call; private chats and groups (when mentioned) | `REQ-TG-*` |
| Interview 2026-10-29 | Cron jobs ask for approval through Telegram | recorded for spec #7, not part of this spec |
| ADR 0003 | routes from channels, `/agent`, inbox per session | routing, session key |
| ADR 0004 | approval outcomes, 10-minute timeout | approval over Telegram |
| ADR 0008 | Telegram Bot API, long polling, message edits throttled to 1 second, inline keyboard approval, allowlist from `[[routes]]` | channel behavior |
| ADR 0009 | sessions as event log | session lookup and history |
| Spec #5 | agent home, memory, onboarding, runtime context `channel` | `/memory`, onboarding on Telegram |

## Goal

The owner can talk to their lana agents from Telegram on any device, with the same agents, memory, tools,
and approvals as in the terminal, while a single `lana daemon` process keeps running on their machine.

## Actors

- **Owner** — runs `lana daemon`, configures the bot, and is the only one who may approve tools and manage sessions.
- **Allowed chat member** — a person in a group chat listed in `[[routes]]`. May talk to the agent in that group.
- **Stranger** — any other Telegram user. Gets no response.
- **Telegram Bot API** — external service reached over HTTPS with long polling.

## Definitions

- **Daemon** — the long-running `lana daemon` process that runs channels.
- **Chat** — a Telegram private chat or group, identified by `chat_id`.
- **Chat session** — the lana session with channel `telegram` and address `<chat_id>` for one agent.
- **Owner ID** — a Telegram user ID listed in `[channels.telegram] owner_ids`. In a private chat matched by a route, the chat's user counts as owner (`Q-005`).
- **Mention** — a group message that contains `@<bot username>` or replies to a message from the bot.
- **Live message** — the bot message that shows a turn while it runs and is edited as text arrives.

## Scope

### Included

- `lana daemon`: foreground process, single instance, graceful shutdown, logs.
- Telegram channel: long polling, allowlist through `[[routes]]`, private chats and groups (mention only).
- One running chat session per chat and agent; inbox while a turn runs.
- Streaming by editing the live message, Markdown to Telegram HTML, long answers split.
- One compact line per tool call.
- Approval with an inline keyboard.
- Bot commands: `/start`, `/help`, `/new`, `/agent`, `/memory`, `/compact`, `/cancel`, `/status`.
- Text messages only.

### Not included

- Scheduler, cron tools, `lana cron`. Spec #7.
- HTTP API, web dashboard, WhatsApp, ACP.
- `lana chat` attaching to the daemon. The CLI keeps running embedded.
- `lana service install` (launchd/systemd). The owner runs `lana daemon` themselves.
- Photos, voice, files, stickers, and other non-text messages (they get a short reply, see `REQ-TG-012`).
- Webhook mode.

## Assumptions

- The bot token is a secret read from the env or `secrets.env`. — Source: spec #1 contract.
- A session runs one turn at a time; messages during a turn go to the inbox. — Source: ADR 0003.
- Approval waits at most 10 minutes. — Source: ADR 0004.
- The Telegram channel module may only depend on `core` and `internal` and the public `lana` package as the CLI does. — Source: architecture import rules.

## Behavior rules

### Daemon

#### REQ-DMN-001: Start and run channels

`lana daemon` shall load the config, start every enabled channel, and run until it receives SIGINT or SIGTERM.

- `AC-DMN-001`: Given a valid Telegram config, when the owner runs `lana daemon`, then stderr shows `telegram: polling as @<bot>` and the process keeps running.

#### REQ-DMN-002: Single instance

The system shall allow only one daemon per `$LANA_HOME`.

- `AC-DMN-002`: Given a daemon is running, when the owner starts a second `lana daemon` with the same `LANA_HOME`, then it exits with code 1 and the message `daemon already running`.

#### REQ-DMN-003: No channel configured

- `AC-DMN-003`: Given no `[channels.telegram]` and no other channel, when the owner runs `lana daemon`, then it exits with code 2 and names the missing config.

#### REQ-DMN-004: Graceful shutdown

On SIGINT or SIGTERM the system shall stop polling, cancel running turns, answer pending approvals as cancelled, and exit 0 within 10 seconds.

- `AC-DMN-004`: Given a turn is running, when the daemon receives SIGTERM, then the turn ends with stop reason `cancelled` and the process exits 0.

### Telegram access

#### REQ-TG-001: Allowlist

The system shall accept an update only if a `[[routes]]` entry with `channel = "telegram"` matches its `chat_id`. Other updates are ignored without a reply and logged at `info` with the `chat_id`, user ID, and username.

- `AC-TG-001`: Given a route for chat `111`, when user `222` writes from private chat `222`, then the bot sends nothing and the log contains `chat_id=222`.

#### REQ-TG-002: Groups respond only to mentions

In a group chat that matches a route, the system shall respond only to a mention. It shall prefix the text sent to the agent with the sender's name, as `<first name>: <text>`.

- `AC-TG-002`: Given an allowed group, when a member writes without a mention, then the bot does nothing.
- `AC-TG-003`: Given an allowed group, when a member writes `@lana_bot how are you`, then the agent receives `Budi: how are you`.

### Sessions

#### REQ-TG-003: One running session per chat and agent

The chat's active agent is the agent of its most recently updated chat session, or the routed agent if the chat has none. The system shall continue the most recent chat session of the chat for the active agent, and create one if none exists. `/agent` switches the active agent (`REQ-TG-013`), so the choice survives a daemon restart.

- `AC-TG-004`: Given a chat with an existing chat session, when the daemon restarts and the owner writes, then the answer continues the same session ID.

#### REQ-TG-004: Messages during a turn

A message that arrives while the chat session runs a turn shall go to the session inbox, and the bot shall react with `👀` to show it was received (fallback: no reaction if the API refuses).

- `AC-TG-005`: Given a running turn, when the owner sends a second message, then it is claimed at the next step of the same turn.

#### REQ-TG-005: Workspace

A chat session shall use the agent's `workspace` when set, otherwise `$LANA_HOME/workspaces/<agent>/`, created if missing (`Q-007`).

- `AC-TG-006`: Given agent `helper` without `workspace`, when its first chat session is created, then the session workspace is `$LANA_HOME/workspaces/helper`.

### Output

#### REQ-TG-006: Streaming through the live message

The system shall send a live message when the turn starts and edit it with the answer text at most once per second, then a final edit with the full answer when the turn ends. While the turn runs it shall send the `typing` chat action every 5 seconds.

- `AC-TG-007`: Given an answer streamed over 3 seconds, when the turn ends, then the live message holds the full answer and was edited at most 4 times.

#### REQ-TG-007: Telegram HTML formatting

The system shall convert Markdown in the answer to Telegram HTML (bold, italic, inline code, code blocks with language, links; headings as bold; lists as text). If Telegram rejects the HTML, the system shall resend that part as plain text.

- `AC-TG-008`: Given the answer `**ok** and `x``, when it is sent, then the HTML is `<b>ok</b> and <code>x</code>`.

#### REQ-TG-008: Long answers

The system shall split an answer longer than 4,096 characters into several messages at paragraph or line boundaries, never inside a code block unless one block alone is too long.

- `AC-TG-009`: Given an answer of 9,000 characters, when the turn ends, then the chat shows 3 messages in order.

#### REQ-TG-009: Tool call lines

For each tool call the system shall add one line to the live message: `🔧 <tool> <primary argument, max 60 characters>` followed by `✓`, `✗`, or `⊘` when the result arrives.

- `AC-TG-010`: Given the agent runs `bash` with `df -h`, when the result is success, then the live message contains `🔧 bash df -h ✓`.

#### REQ-TG-010: Errors

A failed turn shall end with a message `✗ <error>`. `max_steps` shall end with `⚠ stopped at max_steps`.

- `AC-TG-011`: Given the provider returns an error, when the turn ends, then the chat shows `✗ ` followed by the error.

### Approval

#### REQ-TG-011: Inline keyboard approval

When a tool needs approval, the system shall send a message `Allow <tool>: <argument>?` with buttons `Allow once`, `Allow for session`, `Reject`. Only an owner ID may press them (`Q-005`). After a choice, or after 10 minutes, the system shall edit the message to show the outcome and remove the buttons.

- `AC-TG-012`: Given a `bash` call that needs approval, when the owner presses `Allow once`, then the tool runs and the message reads `✓ allowed once`.
- `AC-TG-013`: Given an approval in a group, when a non-owner presses a button, then nothing changes and Telegram shows the toast `only the owner can approve`.
- `AC-TG-014`: Given no answer for 10 minutes, when the timeout passes, then the tool is rejected and the message reads `⊘ approval expired`.

### Messages and commands

#### REQ-TG-012: Non-text messages

The system shall reply `Only text messages are supported for now.` to photos, voice, files, stickers, and other non-text messages from allowed chats, and ignore service messages.

- `AC-TG-015`: Given an allowed chat, when the owner sends a photo, then the bot replies with that sentence.

#### REQ-TG-013: Bot commands

The system shall register and handle these commands. In groups, `/new`, `/agent`, `/compact`, and `/cancel` require an owner ID.

| Command | Effect |
|---|---|
| `/start`, `/help` | Short introduction: agent name and the command list |
| `/new` | New chat session for the current agent |
| `/agent [name]` | Without a name: inline keyboard of agents; with a name: switch the chat to that agent's running session |
| `/memory` | The agent's user and memory entries with sizes (spec #5 format) |
| `/compact` | Compact the chat session |
| `/cancel` | Cancel the running turn |
| `/status` | Agent, model, policy, session ID, and token usage of the chat session |

- `AC-TG-016`: Given a chat on agent `helper`, when the owner sends `/agent coder`, then the next message is answered by `coder` in its own chat session.
- `AC-TG-017`: Given a running turn, when the owner sends `/cancel`, then the turn ends as cancelled and the live message ends with `⊘ cancelled`.
- `AC-TG-018`: Given the daemon starts, when it registers commands, then `setMyCommands` lists the seven commands above.

### Robustness

#### REQ-TG-014: Network failures

The system shall retry `getUpdates` after a network or 5xx error with exponential backoff from 1 to 30 seconds and keep running. A `401` from Telegram shall stop the daemon with exit code 2 and `telegram: invalid bot token`.

- `AC-TG-019`: Given Telegram is unreachable for 20 seconds, when it comes back, then polling resumes without a restart.
- `AC-TG-020`: Given a revoked token, when the daemon starts, then it exits 2 with `telegram: invalid bot token`.

#### REQ-TG-015: Updates are handled once

The system shall confirm each update through the `getUpdates` offset after handing it to its chat, so an update is not processed twice during one daemon run.

- `AC-TG-021`: Given 3 updates in one poll, when they are processed, then the next poll uses `offset = last update_id + 1`.

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| Second daemon | Exit with `daemon already running` | exit 1 |
| No channel configured | Exit with message naming `[channels.telegram]` | exit 2 |
| Token env not set | Exit with `telegram: TELEGRAM_BOT_TOKEN is not set` (env name from config) | exit 2 |
| Invalid token (401) | Exit with `telegram: invalid bot token` | exit 2 |
| Network or 5xx | Backoff and retry | — |
| Edit rejected by Telegram (rate limit 429) | Wait `retry_after`, keep the latest text, edit again | — |
| Message from stranger | No reply, `info` log | — |
| Session lease held by `lana chat` | Reply `✗ session is in use by another process` | — |

## Authorization and security

- Only chats matched by `[[routes]]` reach an agent. Strangers get no reply.
- Approvals, `/new`, `/agent`, `/compact`, and `/cancel` in groups need an owner ID; in private chats the chat itself is the allowlist.
- The bot token is never logged; logs show the bot username only.
- Agents run with the same tools, permission policy, and rules as in the CLI.

## Data integrity and concurrency

- One goroutine per chat handles that chat's messages in order; different chats run in parallel.
- A chat session is used by one daemon at a time (single instance plus session leases).
- The update offset is advanced only after the update is handed to its chat.

## Accessibility

- Buttons carry text labels, not only emoji.
- Every status (allowed, rejected, cancelled, error) is stated in words.

## Scenarios

### SC-001: Chat from the phone

**Given** `lana daemon` runs with a route for the owner's chat to agent `helper`
**When** the owner writes "check disk usage on this machine"
**Then** the bot shows typing, a live message with `🔧 bash df -h ✓`, asks for approval if the policy needs it, and edits the live message into the final answer.

### SC-002: Group mention

**Given** a family group in `[[routes]]` mapped to agent `assistant`
**When** a member writes "@lana_bot what's the weather in Bandung?"
**Then** the agent answers in the group; messages without a mention are ignored.

## Blocking Open Questions

| ID | Question | Controls | Agent recommendation | Owner | Status | Resolution |
|---|---|---|---|---|---|---|
| `Q-001` | Reply to strangers? | `REQ-TG-001` | Ignore silently, log `chat_id` | Ridho | Resolved | Ignore silently |
| `Q-002` | Answer formatting | `REQ-TG-007` | Telegram HTML with plain-text fallback | Ridho | Resolved | Telegram HTML |
| `Q-003` | Show tool calls? | `REQ-TG-009` | One compact line each | Ridho | Resolved | One compact line each |
| `Q-004` | Private chats only or groups too? | `REQ-TG-002` | Private only | Ridho | Resolved | Private chats and groups; groups only on mention |
| `Q-005` | Who may approve and manage sessions in a group? | `REQ-TG-011`, `REQ-TG-013` | `[channels.telegram] owner_ids = [<user id>, ...]`; in private chats the routed chat counts as owner | Ridho | Resolved | Agent recommendation accepted |
| `Q-006` | Language of bot UI text (buttons, status lines) | contract | English, matching the CLI | Ridho | Resolved | Agent recommendation accepted |
| `Q-007` | Workspace of Telegram sessions | `REQ-TG-005` | Agent `workspace`, else `$LANA_HOME/workspaces/<agent>/` | Ridho | Resolved | Agent recommendation accepted |

All questions are resolved. Blocking Open Questions: None.

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-DMN-001` | `AC-DMN-001` | ADR 0008, owner request |
| `REQ-DMN-002` | `AC-DMN-002` | Data integrity |
| `REQ-DMN-003` | `AC-DMN-003` | Failure handling |
| `REQ-DMN-004` | `AC-DMN-004` | ADR 0004 cancel |
| `REQ-TG-001` | `AC-TG-001` | ADR 0008, `Q-001` |
| `REQ-TG-002` | `AC-TG-002`, `AC-TG-003` | `Q-004` |
| `REQ-TG-003` | `AC-TG-004` | Interview (session per chat) |
| `REQ-TG-004` | `AC-TG-005` | ADR 0003 |
| `REQ-TG-005` | `AC-TG-006` | `Q-007`, ARCHITECTURE data layout |
| `REQ-TG-006` | `AC-TG-007` | ADR 0008 |
| `REQ-TG-007` | `AC-TG-008` | `Q-002` |
| `REQ-TG-008` | `AC-TG-009` | Telegram message limit |
| `REQ-TG-009` | `AC-TG-010` | `Q-003` |
| `REQ-TG-010` | `AC-TG-011` | Spec #1 parity |
| `REQ-TG-011` | `AC-TG-012`, `AC-TG-013`, `AC-TG-014` | ADR 0004, ADR 0008, `Q-005` |
| `REQ-TG-012` | `AC-TG-015` | Scope |
| `REQ-TG-013` | `AC-TG-016`, `AC-TG-017`, `AC-TG-018` | ADR 0003, spec #1 parity |
| `REQ-TG-014` | `AC-TG-019`, `AC-TG-020` | Robustness |
| `REQ-TG-015` | `AC-TG-021` | Telegram API semantics |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit or stated as not applicable.
- [x] State transitions and destructive effects are explicit or not applicable.
- [x] Defaults, ordering, retry, and idempotency are explicit where applicable.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Contract applicability is explicit.
- [x] Blocking Open Questions is `None`.
- **Approved by**: Ridho (interview answers 2026-10-30)
- **Approved on**: 2026-10-30
