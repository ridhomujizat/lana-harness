# Tasks: Deferred tool loading

- **Requirements**: [requirements.md](requirements.md) (revision 1)
- **Contract**: [api-contract.md](api-contract.md) (revision 1)
- **Design**: [design.md](design.md) (revision 1)
- **Status**: Draft

## Coverage

| Acceptance criterion | Evidence owner | Verification |
|---|---|---|
| AC-DTL-007, 009, 010 | T-001 | `go test ./tools/ ./internal/agenthome/ ./core/ ./config/` |
| AC-DTL-001..006, 008, 011..015 | T-002 | `go test ./agent/ -race` |
| AC-DTL-016 | T-002 | code check of the web event renderer |
| AC-DTL-017 | T-003 | live test on a scratch daemon |

## Tasks

- [x] `T-001` Config, core types, `tool_search`, and the prompt section
  - Requirements: REQ-DTL-001 (config only), REQ-DTL-003, REQ-DTL-004; API-DTL-001..004
  - Scope: `core/config.go`, `config/` validation, `core/` event kind and payload, `core.ToolOutput.Loaded`,
    `core.ToolCtx.DeferredTools`, `tools/tool_search.go` (registered with the other built-ins),
    `internal/agenthome` `deferred_tools` section, and tests
- [x] `T-002` Agent wiring
  - Requirements: REQ-DTL-001, 002, 005, 006
  - Scope: `agent/deferred.go` (`planDeferral`, `loadedFromEvents`), `agent/turn.go`, `agent/toolrunner.go`
    if needed, web event filter if needed, and tests
- [x] `T-003` Live test on a scratch daemon and token measurement (main session)

## Live test (T-003, 2026-11-21)

Scratch daemon on 127.0.0.1:18460, model `commandcode/deepseek/deepseek-v4.1-flash`, MCP server `dbx`
(25 tools). Agent `lana` (no `defer_tools`, so deferral on) against agent `nodefer` (`defer_tools = false`),
same prompt.

| Check | Result |
|---|---|
| AC-DTL-017 first-step input tokens | 8,728 deferred vs 13,282 not deferred (-34%). The 50% target assumes the `lana` production set (about 150 MCP tools); with 25 tools the fixed prompt dominates. |
| AC-DTL-017 with the production `lana` MCP set (sxored-hub 77, kerjan 29, dbx 25 tools) | 11,461 deferred vs 36,967 not deferred (-69%) |
| kerjan read-only task "show the 3 newest tickets" | `tool_search select:` loaded 3 kerjan tools, called natively, correct answer |
| `tool_search select:` then call | tool loaded (`tools/loaded`), called natively in the next step |
| direct call of a deferred tool | ran, then loaded |
| invalid input on a deferred tool | `invalid input: field 'table' is required` followed by description and schema |
| keyword `execute query` | `mcp__dbx__dbx_execute_query` first, 5 tools loaded |
| next turn | first step 9,216 of 9,936 input tokens cached |
| daemon restart | first step 14,080 of 14,380 cached, model sees the same loaded tools |
