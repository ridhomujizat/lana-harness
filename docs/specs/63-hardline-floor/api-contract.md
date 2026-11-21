# Contract: Hardline floor

- **Requirements**: [requirements.md](requirements.md)
- **Requirements revision**: 1
- **Requirements status**: Approved
- **Contract revision**: 1
- **Applies to**: HTTP, events
- **Status**: Draft

## Shared conventions

| Topic | Decision or authority |
|---|---|
| Authentication | The web session cookie, as every `/v1/*` route |
| Error shape | `writeAPIError` (`{"error":{"code","message"}}`), repository convention |
| Serialization | JSON, times as Unix milliseconds, repository convention |
| Versioning | Additive fields only; old clients ignore them |

## Operation index

| Operation ID | Type | Method / topic | Requirements | Acceptance criteria |
|---|---|---|---|---|
| `API-HLF-001` | Event | `approval/asked` payload | REQ-HLF-007, REQ-HLF-008, REQ-HLF-010 | AC-HLF-013, AC-HLF-019 |
| `API-HLF-002` | Event | `approval/decided` outcome `allowed_verified` | REQ-HLF-007 | AC-HLF-015 |
| `API-HLF-003` | Event | `permission/hardline` (new kind) | REQ-HLF-009, REQ-HLF-010 | AC-HLF-018, AC-HLF-019 |
| `API-HLF-004` | SSE | `approval` event data | REQ-HLF-007 | AC-HLF-013 |
| `API-HLF-005` | HTTP | `POST /v1/approvals/{id}` | REQ-HLF-007, REQ-HLF-008 | AC-HLF-013, AC-HLF-017 |
| `API-HLF-006` | HTTP | `POST /v1/approvals/{id}/code` (new) | REQ-HLF-007 | AC-HLF-013 |
| `API-HLF-007` | HTTP | `POST /v1/local/approvals/{id}` | REQ-HLF-007 | AC-HLF-016 |

## API-HLF-001: `approval/asked` payload

- **Stability**: Existing, additive

| Field | Type | Present | Meaning |
|---|---|---|---|
| `hardline` | object | Only for a hardline match | The match that caused the approval |
| `hardline.rule` | string | Yes | Rule ID, for example `HL-B-001` |
| `hardline.tier` | string | Yes | `block` or `escalate` |
| `hardline.reason` | string | Yes | One-line reason of the rule |

## API-HLF-002: `approval/decided` outcome `allowed_verified`

- **Stability**: Existing, additive enum value

`allowed_verified` means the owner allowed one call after a step-up confirmation. It is never a session
grant. For a hardline request, the approval service records `allowed_session` as `allowed_once`, so
no hardline approval ever becomes a grant.

## API-HLF-003: `permission/hardline` event

- **Stability**: New

Appended when a hardline call is rejected without asking anyone (REQ-HLF-009), for example in a
`workflow` session.

| Field | Type | Meaning |
|---|---|---|
| `v` | int | Payload version |
| `call_id` | string | The rejected tool call |
| `tool` | string | Tool name |
| `rule` | string | Rule ID |
| `tier` | string | `block` or `escalate` |
| `reason` | string | One-line reason of the rule |
| `outcome` | string | `rejected` |

## API-HLF-004: SSE `approval` event data

- **Stability**: Existing, additive

Adds `hardline` with the fields of API-HLF-001, plus:

| Field | Type | Meaning |
|---|---|---|
| `hardline.step_up` | string | For tier `block`: `password` or `password_or_telegram`. It is `password_or_telegram` only when `[web.remote] confirm_with` allows a Telegram code and Telegram can deliver one right now (a connected owner messenger and reset port); otherwise `password`. Empty for tier `escalate`. |

## API-HLF-005: `POST /v1/approvals/{id}`

- **Stability**: Existing, compatibility-sensitive

### Input (body, additive)

| Name | Type | Required | Meaning |
|---|---|---:|---|
| `outcome` | string | Yes | Unchanged: `allowed_once`, `allowed_session`, or `rejected` |
| `password` | string | For a `block` allow, one of the two | The Lana web password |
| `code` | string | For a `block` allow, one of the two | A code from API-HLF-006; accepted only when `step_up = password_or_telegram` |

### Rules

- For a `block` request, `allowed_once` needs exactly one of `password` or `code`. A valid one resolves
  the request as `allowed_verified`.
- For any hardline request, `allowed_session` answers 400 `invalid_request`.
- `rejected` never needs a password or code.

### Failure codes

| Condition | Status | Code |
|---|---:|---|
| `block` allow without password or code, or with both | 400 | `step_up_required` |
| `allowed_session` on a hardline request | 400 | `invalid_request` |
| Wrong password (counts toward the login lockout) | 401 | `invalid_password` |
| Login lockout active | 429 | `too_many_attempts` |
| Wrong, expired, or used code | 401 | `invalid_code` |
| `code` while `step_up = password` | 400 | `invalid_request` |

A 401 or 429 leaves the request open.

## API-HLF-006: `POST /v1/approvals/{id}/code`

- **Stability**: New
- **Authorization**: web session; the request must be an open `block` request with
  `step_up = password_or_telegram`

Sends a new 6-digit code to the owner's Telegram private chats. The code is valid for 5 minutes, single
use, and allows at most 5 wrong tries, as REQ-RA-032/033. The message names the rule and the command.

| Condition | Status | Body or code |
|---|---:|---|
| Sent | 202 | `{"sent_to": <int>}` |
| Unknown or closed request | 404 / 409 | `not_found` / `approval_closed` |
| Not a `block` request, or `step_up = password` | 409 | `not_needed` |
| Telegram not connected | 409 | `telegram_unavailable` |
| More than one send per minute for this request | 429 | `too_many_attempts` |

## API-HLF-007: `POST /v1/local/approvals/{id}`

- **Stability**: Existing

For a `block` request, `allowed_once` answers 409 `step_up_required` and the request stays open. The
notification text says to open Lana to confirm.
