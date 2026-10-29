# Contract: Daemon and Telegram channel

- **Issue**: —
- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 1
- **Requirements status**: Approved
- **Contract revision**: 1
- **Applies to**: `lana daemon`, `$LANA_HOME/daemon.lock`, `[channels.telegram]`, `[[routes]]` with `channel = "telegram"`, Telegram Bot API calls, bot messages, buttons, and commands
- **Status**: Approved

Shared conventions (exit codes, stderr `error: ` prefix, `LANA_HOME`, `LANA_LOG`, secrets) follow the
[spec #1 contract](../1-cli-tui/api-contract.md#shared-conventions).

## Operation index

| ID | Operation | Requirements |
|---|---|---|
| `API-DMN-001` | `lana daemon` | `REQ-DMN-001` through `REQ-DMN-004` |
| `API-TG-001` | `[channels.telegram]` and routes | `REQ-TG-001`, `REQ-TG-002`, `REQ-TG-011` |
| `API-TG-002` | Chat sessions | `REQ-TG-003` through `REQ-TG-005` |
| `API-TG-003` | Live message, formatting, splitting, tool lines | `REQ-TG-006` through `REQ-TG-010` |
| `API-TG-004` | Approval keyboard | `REQ-TG-011` |
| `API-TG-005` | Commands and fixed replies | `REQ-TG-012`, `REQ-TG-013` |
| `API-TG-006` | Bot API usage, retries, offsets | `REQ-TG-014`, `REQ-TG-015` |

## API-DMN-001: `lana daemon`

```text
lana daemon [-v|-vv|-vvv]
```

| Topic | Decision |
|---|---|
| Profile | Effective config of the default profile (no `--profile` flag in this spec) |
| Lock | `$LANA_HOME/daemon.lock`, exclusive `flock`, file contains the PID. Released on exit |
| Logs | `log/slog` text to stderr. Default level `info`; `LANA_LOG` and `-v` flags as in spec #1 |
| Start line | `telegram: polling as @<username>` on stderr at `info` |
| Signals | SIGINT, SIGTERM: graceful shutdown, at most 10 seconds, then exit 0 |

| Condition | stderr | Exit |
|---|---|---|
| Lock held | `error: daemon already running (pid <pid>)` | 1 |
| No `[channels.telegram]` | `error: no channel configured; add [channels.telegram] to config.toml` | 2 |
| No route with `channel = "telegram"` | `error: no [[routes]] entry for channel "telegram"` | 2 |
| Token env empty | `error: telegram: <ENV> is not set` | 2 |
| Invalid `[channels.telegram]` key or type | `error: channels.telegram.<key>: <reason>` | 2 |
| `getMe` returns 401 | `error: telegram: invalid bot token` | 2 |
| Config invalid | config errors as in `lana config check` | 2 |

## API-TG-001: Config

```toml
[channels.telegram]
token_env = "TELEGRAM_BOT_TOKEN"   # optional, default TELEGRAM_BOT_TOKEN
owner_ids = [123456789]            # optional, Telegram user IDs
api_base = "https://api.telegram.org"  # optional, for a local Bot API server or tests

[[routes]]
channel = "telegram"
match = { chat_id = 123456789 }    # private chat with the owner
agent = "helper"

[[routes]]
channel = "telegram"
match = { chat_id = -1001234567890 }  # a group
agent = "assistant"
```

| Key | Type | Rule |
|---|---|---|
| `token_env` | string | Env name of the bot token |
| `owner_ids` | array of integers | May be empty |
| `api_base` | string | `http` or `https` URL without trailing `/` |
| `match.chat_id` | integer | Required for `telegram` routes. Routes without it are a config error at daemon start |

Unknown keys under `[channels.telegram]` are an error. The first route whose `chat_id` equals the update's chat wins.

Owner rule: a user is an owner if their ID is in `owner_ids`, or if the update comes from a private chat matched by a route.

## API-TG-002: Chat sessions

| Topic | Decision |
|---|---|
| Session fields | `channel = "telegram"`, `address = "<chat_id>"`, `agent = <active agent>` |
| Active agent | Agent of the most recently updated session with that channel and address; else the route's agent |
| Lookup | Most recently updated session with that channel, address, and agent; created if none |
| Workspace | Agent `workspace`, else `$LANA_HOME/workspaces/<agent>/` (mode `0700`, created on demand) |
| Group text | `<first_name>: <text without the @mention>` |
| Inbox | Messages during a turn are sent to the session inbox; the bot sets reaction `👀` on them (errors ignored) |

## API-TG-003: Live message and output

| Topic | Decision |
|---|---|
| Start | On turn start the bot sends `…` as a reply to the triggering message (the live message) |
| Edits | At most one `editMessageText` per second per chat; the newest text wins |
| Typing | `sendChatAction` `typing` at turn start and every 5 seconds until the turn ends |
| Content | Tool lines (in call order) then a blank line then the answer text so far |
| Tool line | `🔧 <tool> <arg>` then ` ✓` (success), ` ✗` (error), ` ⊘` (rejected or cancelled) when known. `<arg>` is the primary argument (as in the TUI), single line, cut to 60 characters with `…` |
| Final | When the turn ends the live message is edited one last time with the full content. Overflow goes to extra messages |
| Split | Limit 4,096 characters after HTML conversion. Split at the last blank line, else last newline, else hard cut. A code block is closed and reopened across a split |
| Parse mode | `HTML`. On `400 can't parse entities`, the same part is sent again without parse mode |
| End states | `cancelled`: last line `⊘ cancelled`. `max_steps`: `⚠ stopped at max_steps`. Error: `✗ <error>` |
| Empty answer | `(no answer)` |

Markdown to HTML:

| Markdown | HTML |
|---|---|
| `**x**`, `__x__` | `<b>x</b>` |
| `*x*`, `_x_` | `<i>x</i>` |
| `` `x` `` | `<code>x</code>` |
| fenced block with language `go` | `<pre><code class="language-go">…</code></pre>` |
| `[t](url)` with http/https URL | `<a href="url">t</a>` |
| `# Heading` (any level) | `<b>Heading</b>` |
| `- item`, `* item` | `• item` |
| other `<`, `>`, `&` | escaped |

## API-TG-004: Approval keyboard

Message (reply to the live message):

```text
Allow <tool>: <arg>?
```

Buttons in one row: `Allow once` (`a:<id>:once`), `Allow for session` (`a:<id>:session`), `Reject` (`a:<id>:reject`), where `<id>` is a short random ID per request.

| Event | Effect |
|---|---|
| Owner presses a button | Outcome sent to the runtime; message edited to `✓ allowed once`, `✓ allowed for session`, or `⊘ rejected`; buttons removed; callback answered with empty text |
| Non-owner presses | Callback answered with `only the owner can approve`; nothing else changes |
| Button of an unknown or finished request | Callback answered with `this request is no longer active` |
| 10 minutes pass | Outcome `rejected` (timeout); message edited to `⊘ approval expired` |
| Turn cancelled or daemon stops | Outcome `cancelled`; message edited to `⊘ cancelled` |

## API-TG-005: Commands and fixed replies

`setMyCommands` at start with:

| Command | Description shown in Telegram |
|---|---|
| `new` | Start a new session |
| `agent` | Show or switch the agent |
| `memory` | Show this agent's memory |
| `compact` | Summarize the session history |
| `cancel` | Stop the running turn |
| `status` | Agent, model, and session |
| `help` | List commands |

`/start` behaves like `/help`. Commands may carry `@<bot username>`; in groups a command addressed to another bot is ignored.

| Command | Reply |
|---|---|
| `/help` | `I'm <agent>. Commands: /new /agent /memory /compact /cancel /status` |
| `/new` | `· new session <id>` |
| `/agent` | `Current agent: <agent>` plus one button per agent (`g:<name>`); pressing switches. In a group, a non-owner press is answered with the toast `only the owner can switch agents` |
| `/agent <name>` or button | `· agent: <name>`; unknown: `✗ unknown agent "<name>" (available: …)` |
| `/memory` | Same lines as TUI `/memory` (spec #5) |
| `/compact` | `· compacting…`, then `· compaction <before> → <after> token` or `✗ compaction: <error>` |
| `/cancel` | Running turn is cancelled; idle: `· nothing to cancel` |
| `/status` | `agent <a> · model <p:m> · policy <p> · session <id> · <n> turns` |
| Owner-only command from non-owner in a group | `✗ only the owner can use /<cmd>` |
| `/new`, `/agent`, `/compact` during a turn | `· wait for the turn to finish` |
| Unknown command | `· unknown command: /<cmd>` |
| Non-text message | `Only text messages are supported for now.` |

## API-TG-006: Bot API usage

| Call | Use |
|---|---|
| `getMe` | Start: username, token check |
| `setMyCommands` | Start |
| `getUpdates` | `timeout=30`, `allowed_updates=["message","callback_query"]`, `offset` = last handled `update_id + 1` |
| `sendMessage`, `editMessageText`, `editMessageReplyMarkup` | Output, approvals |
| `sendChatAction` | Typing |
| `setMessageReaction` | `👀` on inbox messages |
| `answerCallbackQuery` | Every button press |

| Failure | Handling |
|---|---|
| Network error or HTTP 5xx on `getUpdates` | Wait 1, 2, 4, … up to 30 seconds; reset after a success |
| HTTP 429 | Wait `parameters.retry_after` seconds, then retry the same call |
| HTTP 401 at any time | Stop the daemon with exit 2 and `telegram: invalid bot token` |
| Other 4xx on send/edit | Log at `warn`; `message is not modified` is ignored |

## Compatibility

- Existing configs without `[channels.telegram]` are unaffected; only `lana daemon` needs it.
- `lana chat` and `lana daemon` may run together; a session used by one is refused by the other through the session lease.
