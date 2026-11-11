# Spec 53: Agents add secrets through a secure input card

- **Status**: Approved (owner decision 2026-11-12, "agent bisa tambahkan Secrets")
- **Branch**: `feature/agent-secrets`, from `feature/workflow-chat` (spec 51)

## Problem

When a workflow, an MCP server or a tool needs a credential that the owner has not set, the agent can only ask the owner to paste it in chat. The value then reaches the model, the session log, the SSE stream and possibly the logs. The owner must also leave the chat and go to Settings > Secrets.

## Decisions (owner, 2026-11-12)

1. **Secure input card.** The new agent tool `secret_request` opens a card in the chat. The card has a masked field with a show/hide toggle, Save and Cancel. On Save, the value goes straight into the owner's secret store, the same store and validation that Settings > Secrets uses.
2. **The value never reaches the agent.** The tool result is only `{"status":"saved"|"cancelled"|"expired","name":"X"}`.
3. **Overwrite rule.** A new name shows the card normally. If the name already has a value, the card clearly says that saving REPLACES the current value. The tool description tells the agent to prefer existing names and to ask to replace a name only to rotate it.
4. **No deletion.** Deleting a secret stays in Settings only. The tool has no delete action.
5. **Web only.** Web and workflow (guardian chat, spec 51) sessions show the card. A Telegram session gets a tool error that sends the owner to the web; secrets are never collected over Telegram chat. A CLI session gets an "unavailable" error.
6. **Every agent.** `secret_request` is in the standard toolset and in the auto permission list. It needs no approval because the owner types the value.

## Requirements

- **REQ-SEC-001** The tool `secret_request` takes `name` (required), `description` (required: why it is needed and where to get it) and `link` (optional http(s) URL). It blocks until the owner answers, like `ask_user`.
- **REQ-SEC-002** The name follows the Settings rule `[A-Za-z_][A-Za-z0-9_]*`, at most 128 characters. The description is 1 to 500 characters and the link at most 500. Invalid input gets a clear tool error and no card.
- **REQ-SEC-003** The service writes `secret/requested` (request_id, call_id, name, description, link, replaces, expires_at) and, in every case, `secret/resolved` (request_id, call_id, name, outcome, reason). The session status is `waiting_input` while a request is open.
- **REQ-SEC-004** A request expires after 30 minutes (outcome `expired`). A cancelled turn closes it as `cancelled`. A daemon restart closes an orphaned request as `expired` (reason `interrupted`). One request per session is open at a time.
- **REQ-SEC-005** `replaces` is true when the name has a non-empty value in `secrets.env` or in the process environment.
- **REQ-SEC-006** The value never reaches the model or provider, the session events, the SSE frames, slog, the tool call input or output, notifications, or the audit log. The audit log records the name only, as for a Settings save.
- **REQ-SEC-007** The tool description tells the agent that the value is never returned, to prefer existing names, that saving over an existing name replaces it, and that deleting is not possible.
- **REQ-SEC-008** Prompt guidance: when a workflow, MCP server or tool needs a credential that is not among the owner's secret names, call `secret_request` instead of asking the owner to paste it in chat. Never ask for a secret value in plain chat.
- **REQ-SEC-009** Routing by session channel: `web` and `workflow` go to the web broker. `telegram` gets an error that names the web. Every other channel gets the "needs the web chat" error. No event is written for a refused channel.
- **REQ-SEC-010** The workflow guardian's environment note lists the secret names. A later note names the secrets again when the set changed, so a secret saved through the card is known on the next turn.
- **REQ-SEC-011** One shared `SecretRequestCard` renders in the main chat (session view), the room timeline and the workflow guardian chat, in the slot where the question card renders.
- **REQ-SEC-012** The card shows the asking agent, the name, the description, the link ("Where to get it", opened with `noopener`), a masked field with a show/hide toggle, Cancel and Save. A replace shows "Replace NAME", a warning that saving replaces the current value, and a "Replace value" button.
- **REQ-SEC-013** Save posts the value once. The field is cleared after a save. A validation error (newline, double quote, empty) keeps the card open with the message.
- **REQ-SEC-014** After the answer, the card collapses to "Saved NAME", "Cancelled" or "Expired", with no value. A resolved request is not shown on a later page load.

## API

- **API-SEC-001** Tool result: `{"status":"saved","name":"X"}`, or `{"status":"cancelled"|"expired"|"unavailable","name":"X","reason":"..."}`. Never the value.
- **API-SEC-002** Events: `secret/requested` and `secret/resolved` with the payloads of REQ-SEC-003.
- **API-SEC-003** SSE event `secret_request` on `GET /v1/sessions/{id}/stream`: `{"request_id","call_id","turn_id"?,"name","description","link"?,"replaces","expires_at"}`. Open requests are replayed on connect. A child session's request reaches the root stream.
- **API-SEC-004** `POST /v1/secret-requests/{request_id}` with `{"value":"..."}` or `{"cancel":true}`, behind the same auth, Host, Origin and CSRF checks as the other `/v1` routes. Responses:
  - `200 {"outcome":"saved"|"cancelled","name":"X"}`
  - `400 invalid_request`: not exactly one of value or cancel, unknown field, or bad JSON
  - `422 validation_failed`: the Settings value rule
  - `404 not_found`: unknown id
  - `409 secret_request_closed`: already answered or expired
  - The body is never logged. A refused answer leaves the request open.
- **API-SEC-005** A save writes `secrets.env` and `secrets.meta.json` with the Settings code. It marks the restart flag and appends the audit line with the name only. A running daemon reads the new value on the next lookup, so no restart is needed (REQ-RTR-008).

## Acceptance

- **AC-SEC-001** A save writes the store and the response, outcome, logs and audit contain no value (`web/secret_requests_test.go`).
- **AC-SEC-002** The replace flag reaches the card and saving overwrites the old value.
- **AC-SEC-003** The route needs CSRF and a session cookie; cancel writes nothing.
- **AC-SEC-004** An invalid body or value is refused and the request stays open; an unknown id is 404.
- **AC-SEC-005** The fake clock expires a request; a late answer is 409.
- **AC-SEC-006** Channel routing per REQ-SEC-009 (`internal/secretreq`, `cmd/lana/daemon_secret_test.go`).
- **AC-SEC-007** The SSE event is sent live and replayed.
- **AC-SEC-008** End to end with a real runtime: after a save, the value is in `secrets.env` and absent from the session events, the tool result, every model request, the SSE stream, slog and the audit file (`TestSecretRequestValueNeverLeaks`).
- **AC-SEC-009** vitest: the card renders, masks, toggles, posts once, warns on replace, shows the outcome states, and shows in the workflow guardian chat and the room timeline.
