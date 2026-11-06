# Design: Telegram connect flow for non-technical owners

- **Requirements**: [requirements.md](requirements.md), revision 1, Approved
- **Contract**: [api-contract.md](api-contract.md), revision 1, Approved
- **Status**: Approved
- **Design revision**: 1

## Repository fit

- HTTP handlers go in `web/channels.go` next to `handleCheckTelegram` and `handleTelegramWaiting`, registered in `registerChannelRoutes`.
- Telegram network calls stay behind a port, like `TelegramChecker`. The adapter in `cmd/lana/web_channels.go` uses `channels/telegram.Client`.
- Secret writes reuse `webfiles.SetSecret`, `webfiles.SetSecretMeta`, the `secret` audit, and `setSecretsChanged`, exactly as `handlePutSecret` does. Extract the shared part into one helper; do not copy it.
- Config writes reuse `webfiles.ConfigWriteMu`, the telegram table renderer, and the route writer already used by `handlePutTelegram` and `handlePutTelegramRoute`.
- The page is `web/assets/views/channels.js` (Telegram detail) plus `web/assets/api.js`. Match the existing view style and `app.css` tokens.

## Components

- **Port** — extend the Telegram port in package `web`:

  ```go
  type TelegramSetup interface {
      // TelegramTokenCheck calls getMe with token (not the stored one).
      TelegramTokenCheck(ctx context.Context, token string) (TelegramCheckResult, error)
      // TelegramDiscover lists chats that wrote to the bot with the stored token.
      TelegramDiscover(ctx context.Context) ([]TelegramSeenChat, error)
  }
  ```

  Errors: `ErrTelegramTokenMissing`, `ErrTelegramConflict` (Bot API 409), `ErrTelegramUnreachable`. `TelegramCheckResult{OK:false}` for 401.
- **`channels/telegram.Client.GetUpdatesPeek(ctx)`** — `getUpdates` with `offset=0`, `timeout=0`, `allowed_updates=["message"]`; maps HTTP 409 to `ErrConflict`. If the existing `Update` type lacks `from` or chat name fields, add them.
- **Adapter** — `cmd/lana/web_channels.go`: `TelegramTokenCheck` builds a client from the given token; `TelegramDiscover` reads the stored token like `telegramCheckSettings`, and when the running channel is polling returns its pending chats instead of calling `getUpdates` (two pollers would get 409).
- **Handlers** — `handlePutTelegramToken`, `handleDiscoverTelegram`, `handleConnectTelegram` per the contract. The connect handler builds one candidate: telegram table with `owner_ids` + sender and `enabled = true`, plus the route; checks it with the existing channel checker; writes atomically; audits `channel.telegram.save`.
- **Page** — Telegram detail view becomes three numbered steps with live state:
  1. Token: password input + "Save token" → `PUT …/token`; done state shows `@bot` and "Replace token".
  2. Find: `t.me` link + "Find my chat" → `POST …/discover`; empty result starts a 3 s poll for 2 min with an `aria-live` message.
  3. Connect: table of seen chats (name, type, sender) with an agent select and a "Connect" button → `POST …/connect` → `POST /v1/daemon/restart` → poll `GET /v1/channels` until state ≠ `off` → success text.
  The current status toggle, owners table, chats table, and Disconnect stay below for later edits. "Token variable" and "API base" move into a collapsed "Advanced" section; its token variable input rejects token-shaped values client-side (`/^[0-9]{5,}:[A-Za-z0-9_-]{30,}$/`).

## Failure handling

| Failure | Observable result |
|---|---|
| 401 from `getMe` | `422 telegram_token_invalid`; nothing written |
| Network | `502 telegram_unreachable` |
| 409 from `getUpdates` | `409 telegram_conflict` |
| Restart unsupported (`POST /v1/daemon/restart` fails) | page shows "Restart lana to finish" with the existing restart button |

## Security

- The token is only in the `PUT …/token` body. The handler never logs the body. Client errors pass through `Client.scrub`.
- Discover is read-only on Telegram (no offset confirmation).

## Verification

| Acceptance criterion | Method | Evidence |
|---|---|---|
| `AC-TGC-001`–`003` | unit (`web`, fake `TelegramSetup`) + adapter test with `httptest` Bot API | `web/channels_connect_test.go`, `cmd/lana/web_channels_test.go` |
| `AC-TGC-004`–`006` | unit + adapter `httptest` | same files, `channels/telegram` client test for 409 |
| `AC-TGC-007`, `009` | unit | `web/channels_connect_test.go` |
| `AC-TGC-008`, `010`, `011` | manual in the browser | runtime check by the owner |
