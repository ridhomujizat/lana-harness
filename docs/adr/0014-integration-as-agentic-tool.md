# 0014. Provide lana as an agentic tool for other services

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: Ridho
- **Related ADRs**: [0004](0004-permission-mode-per-agent.md), [0008](0008-cli-channel-http-and-web-dashboard-interfaces.md), [0009](0009-session-as-append-only-event-log.md), [0012](0012-acp-and-sdk-json-rpc.md), [0013](0013-use-go-as-single-binary.md)

## Context

The owner wants `lana` to be used not only directly, but also to be called by other services as an
**agentic tool**: a service sends a task, `lana` runs an agent with its tools and permissions,
and returns the result. The users envisioned:

- The owner's Go services that want an agent inside their own process.
- Other agent harnesses (Claude Code, Cursor, custom agents) that call tools through MCP.
- Services in any language over HTTP.
- Other agents that speak the Agent2Agent (A2A) protocol.

ADR 0012 already provides ACP (for editors) and the SDK JSON-RPC (stdio and unix socket). Neither reaches
the users above.

## Decision

`lana` provides **four integration surfaces** on top of the same `Context`. There is no separate execution path;
all surfaces use the same agent loop, event log, and permissions.

| Order | Surface | Form | Profile |
|---|---|---|---|
| 1 | **Go library** | Public packages `lana` and `core` | — (used inside the service process) |
| 2 | **MCP server** | `lana mcp`, stdio and streamable HTTP transports | `mcp` |
| 3 | **REST + SSE API** | Part of the daemon's HTTP server (ADR 0008) | `daemon` |
| 4 | **A2A** | Agent card and A2A endpoints | `daemon` + build tag `a2a` |

**1. Go library.**

```go
rt, err := lana.New(ctx, lana.Options{
    Home:    "/var/lib/myservice/lana",   // or Config directly, without a file
    Profile: "headless",
    Tools:   []core.Tool{myservice.LookupOrderTool()},  // the service's own tools
    Approval: lana.ApproveWith(myApprover),            // or lana.PolicyNever
})
defer rt.Close()

res, err := rt.Run(ctx, lana.Request{Agent: "ops", Prompt: "check order 123"})
// res.Text, res.SessionID, res.Usage, res.StopReason

stream, err := rt.Stream(ctx, lana.Request{...})  // event iterator
```

- Public API: package `lana` (facade) and `core` (types `Tool`, `Module`, hooks, events). Other packages
  may be imported but are not guaranteed to be stable before v1.0.0.
- A service can register its own tools and modules; those tools go through the same permission engine.
- Approval in the library is the service's responder function (`approval.request`). Without a responder, the result is `unavailable` (fail-closed).
- Versions follow semver through Git tags. Before `v1.0.0`, changes to the public API are recorded in the CHANGELOG.

**2. MCP server.** Uses `modelcontextprotocol/go-sdk`.

| MCP tool | Input | Output |
|---|---|---|
| `lana_run` | `agent?`, `prompt`, `session_id?` | Final answer text, `session_id`, `stop_reason` |
| `lana_agents` | — | List of agents and their descriptions |
| `lana_sessions` | `limit?` | List of sessions belonging to this MCP client |

- Progress is sent as MCP progress notifications while a turn is running.
- Approval: if the client supports **elicitation**, approval is sent as an elicitation. If not, the session policy is `never`.
- The agents that may be called through MCP are restricted by `[protocol.mcpserver] agents = [...]`.
- The streamable HTTP transport requires a bearer token, the same as the HTTP API.

**3. REST + SSE API.** Replaces and extends the HTTP table in ADR 0008:

| Method | Path | Function |
|---|---|---|
| `POST` | `/v1/runs` | Run one turn; `?wait=true` waits for the result, by default returns `session_id` |
| `POST` | `/v1/sessions` | Create a session |
| `POST` | `/v1/sessions/{id}/messages` | Send a message |
| `GET` | `/v1/sessions/{id}/events?from_seq=` | SSE events of a session |
| `POST` | `/v1/sessions/{id}/cancel` | Cancel a turn |
| `POST` | `/v1/approvals/{id}` | Answer an approval |
| `GET` | `/v1/agents` | List agents |

- The OpenAPI 3.1 specification is written by hand in `docs/specs/` and serves as the contract. Handlers are tested against it.
- Bearer token authentication per client (`[[api.tokens]]` with a name and the list of agents it may use).
- SDK JSON-RPC methods (ADR 0012) map to the same handlers.

**4. A2A.** `lana` as an A2A server: agent card at `/.well-known/agent-card.json` (one skill per
exposed agent), tasks mapped to sessions, task status mapped to `turn/*`, and artifacts mapped to the final answer.
Approval maps to the `input-required` status. `lana` as an A2A client (calling other agents) is added afterward
as the tool `a2a_call`.

**Shared rules:**

- Every session created from an integration surface records the `channel` (`library`, `mcp`, `api`, `a2a`) and the
  client identity in `session/created`.
- An agent that is not in a surface's allowlist cannot be called through that surface.
- The workspace for network surfaces is taken from the agent's `workspace`, not from the client.
- No surface can raise a policy above the agent's own policy.

## Consequences

**Positive**
- One agent can be used from the terminal, chat, editors, Go services, other harnesses, and other agents.
- MCP lets Claude Code and other MCP-capable harnesses use `lana` directly.
- The event log makes it easy to audit actions triggered by other services.

**Negative / tradeoffs**
- Four surfaces mean four contracts that must be maintained.
- A public library limits how freely the `lana` and `core` packages can be refactored.
- A2A and MCP elicitation are still evolving; upgrading the libraries may force changes.

**Neutral**
- ACP and the SDK JSON-RPC from ADR 0012 remain for editors and local processes.

## Alternatives considered

- **ConnectRPC/gRPC** — one definition for both gRPC and HTTP+JSON. Rejected by the owner; REST + SSE is sufficient and simpler.
- **MCP only** — fastest. Rejected because Go services want to embed, and other services need HTTP.
- **HTTP API only** — universal. Rejected because harnesses that support MCP and A2A would need their own adapters.
