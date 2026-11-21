# Spec 65: Deferred tool loading

- **Status**: Approved (owner, 2026-11-21: "lanjut ke point 1", accepting the plan given in chat: MCP
  tools of an agent with many tools are deferred, the prompt lists their names, `tool_search` loads a
  tool's schema, loaded tools are appended after the stable list, a deferred tool called directly is
  loaded, per-server opt-out)
- **Revision**: 1
- **Branch**: `feature/deferred-tools`, from `development` (9afd3d8)
- **Contract**: [api-contract.md](api-contract.md) (config and tool)
- **Design**: [design.md](design.md)
- **Tasks**: [tasks.md](tasks.md)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Paper | Barbaste et al., "Harness Engineering", arXiv 2609.00006, Recommendation 4 | defer tool schemas above about 15 tools; list names, load on demand through a search tool |
| evonic `7fda3b5` | `llm_loop.py` lazy skill tools | loaded tools are appended at the end of the list and kept per session; never unload (prefix cache) |
| Owner decision | chat, 2026-11-21 | scope listed in Status |

## Problem

`offeredTools` (`agent/turn.go`) offers every tool of every MCP server the agent lists, and each step
sends all their schemas. The `lana` agent lists `sxored-hub` (about 77 tools), `kerjan` (29), and
`dbx` (25), so each request carries about 150 tool schemas. `router_calls` shows an average input of
about 53k tokens per `lana` call. Most turns use none of the MCP tools. Weak models (DeepSeek Flash)
also pick tools worse from a long list.

## Goal

An agent with many MCP tools sends only the names of those tools until it needs one. It finds and
loads a tool in one step, and the provider prefix cache stays valid across turns.

## Terms

- **Deferrable tool**: an MCP tool (`mcp__<server>__<tool>`) of a server whose `defer` setting is not
  `false`. Built-in tools are never deferrable.
- **Deferred tool**: a deferrable tool of a native turn where deferral is on, that is not loaded.
- **Loaded tool**: a deferred tool whose schema the turn sends because the model searched for it or
  called it.

## Requirements

### REQ-DTL-001: When deferral is on

Deferral applies to native turns only. The rules:

- `[agents.<name>] defer_tools = false` turns deferral off for the agent.
- `defer_tools = true` turns it on whenever the agent has at least one deferrable tool.
- When `defer_tools` is absent, deferral is on when the agent has more than 15 deferrable tools.
- `[mcp.<name>] defer = false` makes the server's tools not deferrable. They are always sent and do
  not count toward the 15.
- Claude Code turns (`runtime/claudecode`, `agent/external.go`) keep the full tool list; Claude Code
  defers tools on its own.

#### Acceptance criteria

- `AC-DTL-001`: An agent with `kerjan` (29 tools) and no `defer_tools` sends no `mcp__kerjan__*`
  schema on its first step.
- `AC-DTL-002`: An agent with one MCP server of 5 tools and no `defer_tools` sends all 5 schemas.
- `AC-DTL-003`: With `[mcp.kerjan] defer = false`, the `kerjan` tools are sent in full and the
  remaining servers decide deferral by their own count.
- `AC-DTL-004`: A Claude Code turn of the same agent still receives every MCP tool.

### REQ-DTL-002: Stable tool order

The tools a step sends shall be the non-deferred tools, sorted by name as today (REQ-LTC-003),
followed by the loaded tools in the order they were first loaded. Loading a tool appends it; nothing
ever removes or reorders a loaded tool within a compaction period.

#### Acceptance criteria

- `AC-DTL-005`: After `tool_search` loads `b` and then a direct call loads `a`, the next step's tool
  list ends with `b`, `a`.
- `AC-DTL-006`: Two turns in a row with no new load send identical tool lists.

### REQ-DTL-003: The deferred tools prompt section

When a turn has deferred tools, the system prompt shall carry a section `deferred_tools`. It says
that the listed MCP tools are deferred, that `tool_search` returns a tool's description and
parameters and loads it, and that a deferred tool may also be called directly by name. It then lists
every deferred and loaded deferrable tool name, grouped by server, servers and names sorted. The
section does not change when a tool is loaded, so loading never rewrites the prompt.

#### Acceptance criteria

- `AC-DTL-007`: The section lists `mcp__kerjan__*` names under `kerjan` and contains no schema.
- `AC-DTL-008`: Loading a tool mid-session produces no `<system-update>`.

### REQ-DTL-004: The `tool_search` tool

A turn with deferred tools shall be offered `tool_search` without listing it in `tools`, unless
`disabled_tools` lists it. Its input is `query` (required) and `limit` (optional, default 5, at most
10).

- `select:<name>[,<name>…]` returns exactly those tools. A name may be the full name or the
  `<tool>` part when that part is unique among the deferrable tools.
- Any other query ranks the deferrable tools by keyword match over the tool name, the server name,
  and the description, and returns the best `limit` with a score above zero.
- Each result shows the full name, the description, and the JSON input schema.
- Every returned tool becomes loaded. The output ends with
  `Loaded: <names>. Call them directly in your next step.`
- No match returns a success result that says so and suggests other words; nothing is loaded.

#### Acceptance criteria

- `AC-DTL-009`: `tool_search {"query": "create ticket"}` on `kerjan` returns its ticket-creating tool
  first, and the next step sends that tool's schema.
- `AC-DTL-010`: `tool_search {"query": "select:mcp__dbx__dbx_list_tables"}` loads exactly that tool.
- `AC-DTL-011`: An agent with no deferred tools is not offered `tool_search`.

### REQ-DTL-005: Calling a deferred tool directly

A call to a deferred tool by name shall run like a call to an offered tool: name repair, input
validation, permission, and hardline checks all apply. The tool becomes loaded whether or not the
call succeeds. When input validation fails, the error also carries the tool's description and JSON
input schema. Name repair and the "Did you mean" suggestion (REQ-LPG-020, REQ-LPG-021) consider
deferred names too.

#### Acceptance criteria

- `AC-DTL-012`: A call to `mcp__kerjan__<tool>` with valid input runs and returns its result before
  any `tool_search`, and the next step sends the tool's schema.
- `AC-DTL-013`: The same call with a missing required field returns `invalid input: …` followed by the
  tool's schema, and the tool is loaded.

### REQ-DTL-006: Loaded tools persist until compaction

Each step that loads tools shall append one `tools/loaded` event with the new names. A turn starts
with the loaded tools named by `tools/loaded` events after the latest compaction summary in the
session chain, in event order, kept only if still deferrable. After a compaction the set is empty.

#### Acceptance criteria

- `AC-DTL-014`: After a daemon restart, a tool loaded in an earlier turn is still sent, in the same
  position.
- `AC-DTL-015`: After a compaction, no earlier loaded tool is sent until it is loaded again.
- `AC-DTL-016`: The web chat does not render `tools/loaded` events.

### REQ-DTL-007: Measured effect

The first step of a `lana` turn with deferral on shall send at least 50% fewer input tokens than the
same step with `defer_tools = false`, measured on a scratch daemon with the same MCP servers.

#### Acceptance criteria

- `AC-DTL-017`: The live test reports both input token counts.

## Out of scope

- Unloading tools.
- Embedding or BM25 ranking; keyword match is enough for v1.
- Deferring built-in tools.
- Provider-native deferral (Anthropic `defer_loading`), which only some providers support.

## Known cost

Loading a tool changes the tool list, so the step after a load misses the provider prefix cache once.
Turns without a new load keep the cache (AC-DTL-006).
