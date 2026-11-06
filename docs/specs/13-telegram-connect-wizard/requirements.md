# Requirements: Telegram connect flow for non-technical owners

- **Issue**: —
- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required (three new HTTP operations, Telegram page copy)
- **Related ADR**: —
- **Related diagram**: —

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner test 2026-10-30 | Screenshot: bot token pasted into "Token variable"; error "The token variable must use letters, digits, and underscores"; "Add a chat first before you turn Telegram on" | failure evidence |
| Owner request 2026-10-30 | "ini akan digunakan oleh user awam … susah untuk buka terminal atau file, coba buat lebih friendly" | goal |
| Spec #9 | Telegram page, `PUT /v1/channels/telegram`, `POST /v1/channels/telegram/check`, waiting chats, `PUT /v1/secrets/{NAME}`, `POST /v1/daemon/restart` | existing behavior reused |

## Goal

An owner who never opens a terminal or a file can connect a Telegram bot from the web page: paste the
token from @BotFather, send a message to the bot, pick the chat, and the bot answers — with no env
variable names, chat IDs, or manual restart to type.

## Actors

- **Owner** — logged in to the web view; has a bot token from @BotFather and a Telegram account.

## Definitions

- **Token secret** — the secret named by `channels.telegram.token_env` (default `TELEGRAM_BOT_TOKEN`) in `secrets.env`.
- **Seen chat** — a chat that sent the bot a message, read from Bot API `getUpdates` or from the polling channel's pending list.

## Scope

### Included

- Paste the bot token on the Telegram page; lana checks it and stores it as the token secret.
- Find chats that wrote to the bot, even while the channel is off.
- Connect one seen chat in one click: route, owner, enable, save, restart.
- Plain-language page: a 3-step guide; env variable name and API base move under Advanced.
- A clear message when a token is pasted into the Advanced variable field.

### Not included

- Webhook mode, group admin rights, bot creation through BotFather.
- The web login token flow (separate follow-up).

## Behavior rules

### REQ-TGC-001: Paste the bot token

The page shall offer a "Bot token" password field. Saving it shall check the token with `getMe` and, only when valid, write it to the token secret, set `token_env` if the table had none, and show "Connected to @<bot>". The token is never returned to the browser.

#### Acceptance criteria

- `AC-TGC-001`: Given no token secret, when the owner saves a token that `getMe` accepts, then `secrets.env` has `TELEGRAM_BOT_TOKEN`, the response has `bot_username`, and no response or log contains the token.
- `AC-TGC-002`: Given a token that `getMe` rejects, when the owner saves it, then nothing is written and the page shows "This token was not accepted by Telegram. Copy it again from @BotFather."
- `AC-TGC-003`: Given text that is not shaped like a bot token (`<digits>:<35+ chars of [A-Za-z0-9_-]>`), when the owner saves it, then nothing is written and no network call is made.

### REQ-TGC-002: Find my chat

The page shall show "Open @<bot> in Telegram and send any message, then press Find my chat", with a `https://t.me/<bot>` link. "Find my chat" shall list seen chats with name, type, and the sender, newest first, excluding chats that already have a route. While the channel is off it reads `getUpdates` without confirming updates; while it polls it uses the pending list.

#### Acceptance criteria

- `AC-TGC-004`: Given the channel off and a private message from user 42 ("Ridho") to the bot, when the owner presses Find my chat, then the list has one row: chat 42, type private, name "Ridho", sender 42.
- `AC-TGC-005`: Given no message yet, when the owner presses Find my chat, then the page says "No messages yet. Send any message to @<bot>, then try again." and keeps checking every 3 seconds for up to 2 minutes.
- `AC-TGC-006`: Given `getUpdates` answers 409 (another poller or webhook), when the owner presses Find my chat, then the page says "Telegram is already connected elsewhere. Stop the other bot program or remove its webhook, then try again."

### REQ-TGC-003: Connect a chat in one click

"Connect" on a seen chat shall, in one config write: add the route for the chat to the chosen agent (default: the first agent), add the sender's user ID to `owner_ids` if missing, and set `enabled = true`. Then the page shall restart the daemon and show the status until it is "On".

#### Acceptance criteria

- `AC-TGC-007`: Given seen chat 42 from sender 42, when the owner connects it to agent `lana`, then `config.toml` has `[[routes]] channel = "telegram"`, `match = { chat_id = 42 }`, `agent = "lana"`, `owner_ids` contains 42, `enabled = true`, and one `channel.telegram.save` audit entry.
- `AC-TGC-008`: Given a successful connect, when the daemon has restarted, then the page shows status On and "Send a message to @<bot> to talk to lana."
- `AC-TGC-009`: Given a stale config version, when the owner connects, then nothing is written and the page reloads the form with "Settings changed in another tab. Check them and try again."

### REQ-TGC-004: Plain page and mistake guard

The main page shall not show env variable names, chat IDs as input fields, or the API base. These stay under "Advanced". If the Advanced token variable field gets a value shaped like a bot token, the page shall block saving with "This looks like the bot token itself. Paste it in Bot token above instead."

#### Acceptance criteria

- `AC-TGC-010`: Given the Telegram page with no token, then the visible steps are 1 "Paste your bot token", 2 "Send a message to your bot", 3 "Connect the chat", and "Token variable" is only inside Advanced.
- `AC-TGC-011`: Given `9094956084:AAEz…` typed into the Advanced token variable field, when the owner saves, then no request is sent and the field shows the mistake message.

## Failure cases

| Condition | Expected behavior | Result code |
|---|---|---|
| Token rejected by `getMe` | nothing written | `422 telegram_token_invalid` |
| Token shape wrong | nothing written, no network call | `400 invalid_request` |
| Telegram unreachable | nothing written | `502 telegram_unreachable` |
| No token secret for discover | prompt step 1 | `409 telegram_token_missing` |
| `getUpdates` 409 | message from `AC-TGC-006` | `409 telegram_conflict` |
| Stale config version on connect | nothing written | `409 stale_version` |
| Unknown agent on connect | nothing written | `422 validation_failed` |

## Authorization and security

- All three operations need an owner browser session (existing auth middleware).
- The token travels only in the save request body; it is never logged, echoed, or put in error text (existing `Client.scrub`).
- Discover never confirms updates, so the polling channel still receives those messages later.

## Data integrity and concurrency

- Token save and connect hold `webfiles.ConfigWriteMu`, like other web saves.
- Connect writes the route, owners, and `enabled` in one atomic config write.

## Accessibility

- Steps are an ordered list; buttons have text labels; status uses text, not only color; the auto-check announces results in an `aria-live="polite"` region.

## Blocking Open Questions

| ID | Question | Controls | Requested reference | Owner | Status | Resolution |
|---|---|---|---|---|---|---|
| `Q-001` | Restart automatically after connect? | `REQ-TGC-003` | owner request "lebih friendly" | Ridho | Resolved | Yes, the page calls the existing restart |
| `Q-002` | Who becomes owner? | `REQ-TGC-003` | — | Ridho | Resolved | The sender of the connected chat's message |

## Requirement coverage

| Requirement | Acceptance criteria | Source |
|---|---|---|
| `REQ-TGC-001` | `AC-TGC-001` through `AC-TGC-003` | owner test |
| `REQ-TGC-002` | `AC-TGC-004` through `AC-TGC-006` | owner request |
| `REQ-TGC-003` | `AC-TGC-007` through `AC-TGC-009` | owner request, `Q-001`, `Q-002` |
| `REQ-TGC-004` | `AC-TGC-010`, `AC-TGC-011` | owner test |

## Approval

- [x] Scope and exclusions are explicit.
- [x] Success and failure behavior is measurable.
- [x] Authorization is explicit.
- [x] Every requirement and acceptance criterion has a stable ID and a source.
- [x] Blocking Open Questions is `None`.
- **Approved by**: Ridho
- **Approved on**: 2026-10-30
