# 0012. Provide an ACP server and an SDK JSON-RPC server

- **Status**: Accepted (Rust implementation details replaced by [0013](0013-use-go-as-single-binary.md); the architectural decision stands)
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0008](0008-cli-channel-http-and-web-dashboard-interfaces.md), [0010](0010-profile-and-config-patch.md)

## Context

The owner wants agents to be controllable from editors and from other programs. `dsh` has two profiles
for this: `acp` (Agent Client Protocol server for automation) and `sdk` (JSON-RPC server used by the
TypeScript and Python SDKs). Both use the same runtime; only the entry module differs.

**ACP (Agent Client Protocol)** is a JSON-RPC protocol over stdio between an editor (Zed and other editors)
and a coding agent. The editor acts as the client, the agent acts as the server.

## Decision

`lana-protocol` provides two server modules. Both are selected through profiles
(see [0010](0010-profile-and-config-patch.md)).

**ACP — `lana acp`.** JSON-RPC 2.0 over stdio using the `agent-client-protocol` crate.

| Direction | Method | Mapping to `lana` |
|---|---|---|
| client to agent | `initialize` | Send capabilities: `loadSession`, prompts with images |
| client to agent | `session/new` | Create a session with the editor's `cwd` as the workspace |
| client to agent | `session/load` | Resume a session from the event log |
| client to agent | `session/prompt` | Run one turn |
| client to agent | `session/cancel` | Cancel the active turn |
| agent to client | `session/update` | Stream text, `tool_call`, and tool status from the `session.event` hook |
| agent to client | `session/request_permission` | Responder in the `approval.request` chain |
| agent to client | `fs/read_text_file`, `fs/write_text_file` | Optional: read files through the editor's buffer if the client supports it |

The agent used by ACP is selected with `lana acp --agent coder`.

**SDK — `lana sdk`.** `lana`'s own JSON-RPC 2.0 over stdio (default) or a unix socket
`~/.lana/lana.sock`. Methods follow the HTTP API so there is one mental model for all entry points:

| Method | Function |
|---|---|
| `session.create { agent }` | Create a session |
| `session.send { session_id, content }` | Send a message, start or queue a turn |
| `session.subscribe { session_id, from_seq }` | Notifications of `session.event` starting from a given `seq` |
| `session.fork { session_id, at_seq }` | Fork at a turn boundary |
| `session.cancel { session_id }` | Cancel a turn |
| `approval.respond { request_id, outcome }` | Answer an approval |
| `agents.list`, `sessions.list` | Listings |

Rules:

- The ACP and SDK servers use the same `Context` as the daemon. There is no separate execution path.
- The stdio protocols write logs to stderr or a file, **never** to stdout.
- The SDK on a unix socket uses `0600` file permissions. There is no TCP port for the SDK in v1.
- Client libraries for the SDK (TypeScript, Python) are not part of v1. The protocol is documented first in `docs/specs/`.

## Consequences

**Positive**
- `lana` can be used directly as an agent in Zed and other editors that support ACP.
- Scripts and other programs can use `lana` without an HTTP server.
- `session.subscribe` with `from_seq` uses the event log to reconnect without losing events.

**Negative / tradeoffs**
- The ACP specification is still evolving. Upgrading the crate may force changes.
- Two protocols plus the HTTP API mean three surfaces that must be kept consistent.

**Neutral**
- SDK methods and HTTP endpoints are built from a single handler definition so they do not drift apart.

## Alternatives considered

- **HTTP API only** — one surface. Rejected because ACP editors use stdio and local programs do not need a port.
- **MCP server mode (lana as an MCP server)** — possible later. MCP suits exposing tools, not agents with sessions and approvals.
