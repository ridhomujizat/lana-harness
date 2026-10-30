# Contract: Telegram connect flow for non-technical owners

- **Requirements**: [requirements.md](requirements.md), revision 1, Approved
- **Contract revision**: 1
- **Applies to**: HTTP (`/v1`), web page copy
- **Status**: Approved

## Shared conventions

Spec #9 API conventions apply: owner session cookie, `Origin` and `Host` checks, error body `{ "error": { "code", "message", "details"? } }`, config `version` (sha256) for optimistic writes.

## Operation index

| Operation ID | Method | Path | Requirements |
|---|---|---|---|
| `API-TGC-001` | `PUT` | `/v1/channels/telegram/token` | `REQ-TGC-001` |
| `API-TGC-002` | `POST` | `/v1/channels/telegram/discover` | `REQ-TGC-002` |
| `API-TGC-003` | `POST` | `/v1/channels/telegram/connect` | `REQ-TGC-003` |

## API-TGC-001: Save the bot token

Request: `{ "token": "<bot token>" }`. `token` is trimmed; it must match `^[0-9]{5,}:[A-Za-z0-9_-]{30,}$`.

| Condition | Status | Body |
|---|---:|---|
| Valid, stored | `200` | `{ "bot_username": "lana_bot", "bot_url": "https://t.me/lana_bot", "token_env": "TELEGRAM_BOT_TOKEN", "version": "<config sha256>", "restart_required": true }` |
| Bad shape or body | `400` | `invalid_request` |
| `getMe` 401 | `422` | `telegram_token_invalid`, message "This token was not accepted by Telegram. Copy it again from @BotFather." |
| Network error | `502` | `telegram_unreachable` |

Effects: secret `<token_env>` written through the existing secret store (same file, mode, meta, and audit as `PUT /v1/secrets/{NAME}`); when `[channels.telegram]` is missing or has no `token_env`, the table is created or gets `token_env = "TELEGRAM_BOT_TOKEN"` with `enabled = false` until connect. `version` is the config hash after the write.

## API-TGC-002: Find chats

Request: empty body.

| Condition | Status | Body |
|---|---:|---|
| OK | `200` | `{ "bot_username": "lana_bot", "bot_url": "https://t.me/lana_bot", "chats": [ { "chat_id": 42, "type": "private", "name": "Ridho", "sender_id": 42, "sender_name": "Ridho", "last_message_at": 1793370000000 } ] }` newest first, routed chats excluded, at most 20 |
| No token | `409` | `telegram_token_missing` |
| `getUpdates` 409 | `409` | `telegram_conflict` |
| Network error | `502` | `telegram_unreachable` |

Source: when the channel is polling, the pending list (sender fields from the pending entry; `sender_id` = `chat_id` for private chats); otherwise `getUpdates` with `offset = 0`, `timeout = 0`, `allowed_updates = ["message"]`, never followed by a confirming call. `name` is the chat title, or first + last name, or `@username`.

## API-TGC-003: Connect a chat

Request: `{ "version": "<config sha256>", "chat_id": 42, "sender_id": 42, "agent": "lana" }`. `agent` must exist. `sender_id` must be a positive integer.

| Condition | Status | Body |
|---|---:|---|
| Saved | `200` | `{ "version": "<new sha256>", "restart_required": true }` |
| Stale version | `409` | `stale_version` |
| Unknown agent or bad input | `422` | `validation_failed` with `details[].key` |

Effects in one atomic config write under `webfiles.ConfigWriteMu`: route `{ channel = "telegram", match = { chat_id }, agent }` added or updated; `sender_id` appended to `owner_ids` if missing; `enabled = true`. Audit `channel.telegram.save`. The page then calls the existing `POST /v1/daemon/restart` and polls `GET /v1/channels` until the Telegram card state is not `off` (max 30 s).

## Bot link

The server builds `bot_url` as `https://t.me/<bot_username>` (empty string when the username is unknown) and returns it in `API-TGC-001`, `API-TGC-002`, and the Telegram card of `GET /v1/channels`. Web assets never contain an external URL literal (spec #9 asset rule); the page uses `bot_url` as the link target.

## Page copy

| Place | Text |
|---|---|
| Step 1 title | Paste your bot token |
| Step 1 help | Create a bot with @BotFather in Telegram, then paste the token it gives you. |
| Step 1 done | Connected to @<bot> · Replace token |
| Step 2 title | Send a message to your bot |
| Step 2 help | Open @<bot> in Telegram (link) and send any message, then press Find my chat. |
| Step 2 empty | No messages yet. Send any message to @<bot>, then try again. |
| Step 3 title | Connect the chat |
| Step 3 row action | Connect |
| Connected | Telegram is on. Send a message to @<bot> to talk to <agent>. |
| Advanced token-shaped error | This looks like the bot token itself. Paste it in Bot token above instead. |
| Stale | Settings changed in another tab. Check them and try again. |

## Approval

- [x] Every operation has a stable ID and links to requirements.
- [x] Input, success, failure, and authorization are explicit.
- **Approved by**: Ridho
- **Approved on**: 2026-10-30
