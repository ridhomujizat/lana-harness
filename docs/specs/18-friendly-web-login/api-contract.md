# Contract: Friendly web login

- **Requirements**: [requirements.md](requirements.md), revision 1
- **Contract revision**: 1
- **Applies to**: HTTP `/v1/auth/*`, `$LANA_HOME/web.json`, `[web] open_browser`, CLI `lana web reset-login`, Telegram message

Spec #9 conventions apply (error envelope, Host/Origin checks, CSRF on state-changing calls with a session).

## `web.json`

```json
{ "version": 2, "password_hash": "$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>", "password_set_at": 1793400000000, "reset_requested": false, "token_sha256": "<hex, kept until a password is set>" }
```

Mode `0600`. `version: 1` files (token only) are read and upgraded on the first password set.

## Config

`[web] open_browser = true` (default) — boolean.

## Operations

| ID | Method and path | Auth | Body | Success | Failures |
|---|---|---|---|---|---|
| `API-WLG-001` | `GET /v1/auth/state` | none | — | `200 { "password_set": bool, "telegram_reset": bool, "locked_until": ms \| null }` | — |
| `API-WLG-002` | `POST /v1/auth/claim` | none | `{ "code", "password" }` | `200 { "csrf": "…" }` + session cookie | `400 invalid_request` (password < 8), `410 link_expired` (unknown, used, or old code) |
| `API-WLG-003` | `POST /v1/auth/login` (changed) | none | `{ "password" }` (or `{ "token" }` while no password is set) | `200 { "csrf" }` + cookie | `401 invalid_password`, `429 too_many_attempts` |
| `API-WLG-004` | `POST /v1/auth/reset/telegram` | none | `{}` | `202 { "sent_to": <n> }` | `409 telegram_unavailable`, `429 too_many_attempts` (1 per minute) |
| `API-WLG-005` | `POST /v1/auth/reset/verify` | none | `{ "code", "password" }` | `200 { "csrf" }` + cookie | `401 invalid_code`, `410 code_expired` (also after 5 wrong tries) |
| `API-WLG-006` | `POST /v1/auth/reset/restart` | none | `{}` | `202 {}` — sets `reset_requested` | — |
| `API-WLG-007` | `POST /v1/auth/password` | session + CSRF | `{ "current", "password" }` | `200 {}` — other sessions end | `401 invalid_password`, `400 invalid_request` |
| `API-WLG-008` | `POST /v1/auth/link` | local control only (daemon socket/lock owner, not HTTP from the browser) | — | the sign-in link | — |

Error messages for the UI: `invalid_password` → "That password is not right."; `too_many_attempts` → "Too many tries. Wait 5 minutes or reset your password."; `link_expired` → "This link has expired. Restart lana to get a new one."; `code_expired` → "Ask for a new code.".

## CLI

`lana web reset-login`: with a running daemon, prints `Open this link to sign in: http://127.0.0.1:18300/#/welcome?code=…` and opens the browser; without one, sets `reset_requested` and prints `lana is not running. Start it and a browser window will open to set a new password.` Exit 0.

## Telegram

Reset message text as in REQ-WLG-004, sent with plain text to each owner's private chat route.

## Approval

- **Approved by**: Ridho
- **Approved on**: 2026-10-31
