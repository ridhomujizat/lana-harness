# 0016. Authenticate the browser with an owner token and a session cookie

- **Status**: Accepted
- **Date**: 2026-10-30
- **Deciders**: Ridho
- **Related ADRs**: [0008](0008-cli-channel-http-and-web-dashboard-interfaces.md), [0014](0014-integration-as-agentic-tool.md)

## Context

Spec #9 adds a web view to `lana daemon` for settings and chat sessions. Two ADRs describe the HTTP surface with
different auth models:

- ADR 0008: `lana init` creates a dashboard token and stores a hash; the browser logs in with it.
- ADR 0014: `/v1` REST and SSE endpoints with bearer tokens from `[[api.tokens]]`.

`lana init` creates no token today, and it runs before the web view exists. The web view needs one owner and no API
clients in v1. It listens on loopback only (spec #9 `Q-001`). Spec #9 rev 2 adds a restart of the daemon from the browser, so browser logins must survive it.

## Decision

The browser API uses the `/v1` paths of ADR 0014 with cookie auth for one owner:

- On first start with the web view enabled, the daemon creates `$LANA_HOME/web.json` (mode `0600`) with the SHA-256
  of a random 32-byte token and prints the token once to stderr. Only the hash is stored.
- `POST /v1/auth/login` compares the token in constant time and sets an `HttpOnly`, `SameSite=Strict` session cookie.
  A browser session expires after 12 hours without use. State-changing calls also need the `X-Lana-CSRF` header and
  pass `Host` and `Origin` checks.
- Each browser session is a record in `$LANA_HOME/web-sessions.json` (mode `0600`). A record holds the SHA-256 of the session cookie, the SHA-256 of the CSRF token, the creation time, the last-used time, and a short user agent label. The raw cookie and CSRF token are never stored.
- Records survive a daemon restart. The in-place restart of `POST /v1/daemon/restart` (spec #9 rev 2) keeps the file, so the owner stays signed in. Records past the 12-hour idle limit are dropped when the file is read.
- `POST /v1/auth/sessions/revoke-others` deletes every record except the caller's own, which signs out the other browsers. It does not change the owner token.
- A new token is made by deleting `web.json` and restarting the daemon.
- Bearer tokens (`[[api.tokens]]`) for API clients are not part of this decision; a later spec adds them.

## Consequences

**Positive**
- One small auth model for a loopback, single-owner web view.
- Browsers stay signed in across a daemon restart.
- The owner can sign out other browsers with one call.
- The token never appears in config, logs, or responses after creation.

**Negative / tradeoffs**
- One token opens every setting; rotation needs a restart.
- Browser sessions are written to `web-sessions.json`. The file is rewritten on login, logout, revoke, and when a session's last-used time moves by more than one minute.

**Neutral**
- Replaces the `lana init` token step of ADR 0008. The rest of ADR 0008 (pages, port, `embed.FS`, Preact and `htm`) stays.

## Alternatives considered

- **Sessions in memory only** — every restart, including the restart from the browser, would sign the owner out; rejected.
- **Bearer tokens only (ADR 0014)** — a browser would keep the token in script-readable storage; rejected for the web view.
- **Token created by `lana init`** — `lana init` runs before the web view is configured and existing homes have no token.
