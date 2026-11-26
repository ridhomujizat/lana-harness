# Spec 80: Connection order for API key providers

- **Status**: Approved (owner 2026-11-26: "bisa di drag, bisa mana dulu yg di gunakan kaya yang lain, terapin
  di api key juga")
- **Revision**: 1
- **Branch**: feature/api-connection-pool
- **Related**: spec 75 (account failover inside a provider), spec 77 revision 4 (one account list)

## Context

The Claude Code and Antigravity pages list their accounts in one list the owner reorders by drag and drop
(spec 75 REQ-PAF-006); the top account is used first and the next one takes over when an account runs out.
An API key provider page (Command Code, DeepSeek, OpenAI, …) lists its **Connections** (`commandcode`,
`commandcode-2`, …) as fixed rows: each connection is its own provider name, and falling back from one to
another needs a combo. Only the keys inside one connection can be reordered (REQ-PAF-005). The owner wants
the connections of an API provider to work like the accounts of the subscription pages.

## Definitions

- **API connection**: a `[providers.<name>]` table whose kind is not `claude-code` or `antigravity`.
- **Connection pool**: the enabled API connections with the same kind and the same effective base URL
  (`base_url`, else the kind's default base URL; compared after trimming a trailing `/` and lowercasing the
  scheme and host), that do not set `pool = false`, ordered by `priority` (lower first; default 100), then
  name. `openai_compat` connections with different base URLs (for example `127.0.0.1:8091` and `:8092`)
  are never in one pool.
- **Pooled connection address**: `<connection>/<model>` of a connection that is in a pool of two or more
  connections.

## Requirements

### REQ-ACP-001: Pooled API addresses

A chain target that is a pooled connection address shall try every connection of its pool, top first, each
with `<model>`, before the chain moves on. The addressed connection itself is not tried first; the pool
order decides. Inside each connection its own keys are tried first (REQ-PAF-005). The router moves to the
next connection only on a failure of class `auth`, `rate_limit`, or `quota` (`poolMoves`); any other
failure skips the rest of the pool and the chain moves to its next target, as for subscription pools
(spec 78 implementation notes). A connection that is cooling down is skipped unless every connection of
the pool is cooling down; then the pool is tried in order anyway.

A connection with `pool = false`, a disabled connection, and a connection alone in its pool are not
expanded: their address uses only that connection, as before this spec. A target that the expansion would
try a second time in the same call (same connection and model, for example a combo `[commandcode/x,
commandcode-2/x]`) is not tried again.

Call records, the call log, and stats name the connection that answered (and its key, as REQ-PAF-005).
This applies to every caller of the router chain: agent turns on API models, the OpenAI-compatible
endpoint, background calls, and combos.

- `AC-ACP-001`: Given `[providers.cc]` and `[providers.cc-2]` of kind `commandcode` with a fake server
  that answers 402 for `cc`'s key, a call on `cc/m` succeeds from `cc-2` and the record names `cc-2`.
- `AC-ACP-002`: Given `cc-2` with `priority = 10` and `cc` with none, a call on `cc/m` is answered by
  `cc-2` without calling `cc`.
- `AC-ACP-003`: Given `cc` answering 400 (class `client`), a call on combo `[cc/m, openai/x]` does not call
  `cc-2`; it answers from `openai/x`.
- `AC-ACP-004`: Given `cc-2` with `pool = false`, a call on `cc/m` that fails with 402 does not call
  `cc-2`; a call on `cc-2/m` uses `cc-2` only.
- `AC-ACP-005`: Given `local` (`openai_compat`, `http://127.0.0.1:8091/v1`) and `local-2` (`openai_compat`,
  `http://127.0.0.1:8092/v1`), a 429 on `local/m` does not call `local-2`.

### REQ-ACP-002: Config

`priority` (integer >= 0) and `pool` (boolean) are valid on API connection tables, with the same checks as
on subscription accounts. Saving a connection from the provider dialog keeps its `priority` and `pool`.

### REQ-ACP-003: Saving the order

`PUT /v1/router/pool-order` accepts `runtime: "api"` with `order` and `unpooled` naming API connections.
Every listed name must be an API connection, all of them must share one kind and effective base URL, and
every enabled or disabled connection of that kind and base URL must appear exactly once; anything else is
422 with one issue per problem. The save writes `priority` 10, 20, 30, … in the new order (order first,
then unpooled), removes `pool` from the rows of `order`, and sets `pool = false` on the rows of
`unpooled`, in one config write guarded by `expected_version` (409 `version_conflict` when stale). The
response is the same shape as for the subscription runtimes. The router picks up the new order on its next
config reload, the same way as a subscription pool order.

### REQ-ACP-004: Provider list fields

`GET /v1/providers` (and the Router provider rows the web reads) report, for each API connection,
`priority` (its own or the default 100), `pool` (false only when `pool = false`), and `pool_group`, a
string equal for two connections exactly when they share kind and effective base URL.

### REQ-ACP-005: Provider page

On an API provider page with two or more connections in one pool group, the **Connections** panel is one
ordered list (the `AccountOrderList` of the Claude Code page, variant `account`): a drag handle, the
"Use as backup" switch, the "Not used as backup" divider for `pool = false` rows, keyboard ↑/↓ on the
handle, "Saved" after a drop, and the revert message when a save fails. Each row keeps what the connection
row shows today (name, key status, enable switch, Test, Edit, Delete, test result, delete confirm).
Dropping a row saves at once through REQ-ACP-003 with `runtime: "api"`. A page with one connection, or a
custom page whose connections are in different groups, shows the rows as today, without handles.

The note under the list replaces "Each connection is its own provider name … Put them in a combo …" with:
"The top connection is used first. Lana moves to the next one when a connection runs out of credit, hits
its rate limit, or its key is rejected. Turn off Use as backup to keep a connection for agents set to it."
With two or more pooled connections the page also shows the pool terms note of the subscription pages
(`POOL_TERMS_NOTE`).

The Keys panel of each connection (key order, Add backup key) stays as it is.

- `AC-ACP-006`: The Command Code page with connections `commandcode` and `commandcode-2` shows both in one
  list with handles; dragging `commandcode-2` to the top calls `savePoolOrder("api", ["commandcode-2",
  "commandcode"], [], version)` and shows "Saved".
- `AC-ACP-007`: A page with one connection shows no handle and no pool terms note.

## Out of scope

Sticky connections per session (API calls hold no runtime session), pooling across kinds or base URLs,
pooling `openai_compat` connections that only share a host, a separate pooled address name.

## Implementation notes (2026-11-26)

- `AC-ACP-003` as written cannot hold: a 400 of class `client` stops the whole chain (spec 37 AC-RTR-008), so
  `openai/x` is not called either. The pool part holds (`cc-2` is not called). The router tests check the
  400 case (chain stops, `cc-2` not called) and the "chain moves on" half with a 404 (`not_found`) and a
  5xx past its retries, which skip the rest of the pool and answer from `openai/x`.
- Cooldowns use the chain-wide rule of REQ-RTR-006, as subscription pools do: cooling connections are
  skipped unless every target of the call is cooling. A pool that is the whole chain (an agent on `cc/m`)
  is then tried in order anyway; a pool inside a combo whose other targets are usable is skipped.
- `missing_secret` and `not_found` do not move to the next connection (only `auth`, `rate_limit`, `quota`
  do); they skip the rest of the pool.
- The dedupe drops a target an expansion would try again in the same call, for subscription pools too.
- The Connections panel uses the ordered list only when every connection of the page shares one
  `pool_group`; a page with connections in different groups keeps the plain rows and shows no note.
