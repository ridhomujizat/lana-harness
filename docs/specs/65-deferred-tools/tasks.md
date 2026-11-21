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
- [ ] `T-003` Live test on a scratch daemon and token measurement (main session)
