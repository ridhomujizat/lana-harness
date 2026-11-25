# Tasks: Antigravity as a router target

Test command: `nix develop -c go vet ./... && nix develop -c go test -count=1 ./...`; the web task also
`cd web/ui && env -u NODE_ENV npx vitest run && npm run build`.

Every task keeps all Claude Code behavior and tests unchanged.

## Wave 1 (parallel)

### T-001 Config and address layer

- Status: Done 2026-11-25 (8781b7c)
- Files: `core/config.go` (`AntigravityKind`, `ProviderConfig.Home` `toml:"home"`), `core/capabilities.go`,
  `config/validate.go` (+ tests), `internal/llmcall/llmcall.go` (+ tests).
- Do: API-AGY-001…004. In llmcall add `AntigravityProvider` (= `core.AntigravityProvider`, declared in
  T-002's `core/runtime.go`; until then use the literal `"antigravity"` in llmcall), `ExternalRuntime`,
  `IsExternal`, `IsExternalAddress(In)`, `WithoutExternal`, `HasExternal`, `AntigravityAccount`,
  `AntigravityHome`, `ValidAntigravityModel`; switch `AgentChain`, `BackgroundChain`,
  `AgentBackgroundChain`, `WarnNoBackground` to the external variants.
- Done when: AC-AGY-001/002 address and account validation cases pass as config/llmcall unit tests; a
  background chain `[antigravity/x, ds/y]` → `[ds/y]`.

### T-002 runtime/antigravity package

- Status: Done 2026-11-25 (98b9ceb runtime, 36d10b5 status/usage/login/archtest)
- Files: `core/runtime.go` (`AntigravityProvider = "antigravity"`, `RuntimeConfig.Home`, doc that
  `ClaudeSessionID` also carries the ACP session ID), new `runtime/antigravity/*.go` + tests,
  `runtime/antigravity/testdata/fakeagy/main.go`, `internal/archtest/arch_test.go` (twin of the
  `runtime/claudecode` rule + the credential-string check of AC-AGY-005).
- Do: design "runtime/antigravity" section and the recorded protocol; fixtures already in
  `runtime/antigravity/testdata/`.
- Done when: AC-AGY-003, 004, 006 (meta + MCP shape from a Claude-shaped `OpenMCP` JSON), 007, 008,
  009, 010, 012, and the login part of 013 pass against `fakeagy`; `go test ./runtime/antigravity/...`
  does not need a real `agy`.

## Wave 2 (parallel, after wave 1)

### T-003 Router

- Status: Done 2026-11-25 (4511373)
- Files: `router/router.go`, `router/claudecode.go` or new `router/antigravity.go`, `router/chain.go`,
  `router/module.go`, `router/models.go`, `router/resolve.go` (+ tests), `router/openaiproxy` test.
- Do: design "router"; `SetRuntimeModels(account, []core.RuntimeModel)` / `RuntimeModels(account)`.
- Done when: AC-AGY-016 (proxy 404, no `agy` run), resolve/combos accept the addresses, Models(account)
  returns the set list.

### T-004 Agent dispatch

- Status: Done 2026-11-25 (34a07da, with spec 75 subscription pools)
- Files: `agent/turn.go`, `agent/external.go`, `agent/registry.go`, `agent/module.go`, background users of
  `WithoutClaudeCode` (`agent/titles`, `memcapture`, `compaction`, `knowledge`) (+ tests with a fake
  `core.AgentRuntime` registered as `antigravity`).
- Do: design "agent".
- Done when: a turn on `antigravity/m` runs the `antigravity` runtime with `Home` set for a named account;
  resume picks only the same runtime and account; AC-AGY-011 fallback; Claude tests unchanged.

## Wave 3

### T-005 Root package

- Status: Done 2026-11-26 (89849cb)
- Files: `lana_runtime.go`, new `lana_agy_signin.go`, `lana_agy_usage.go` (+ tests building `fakeagy`),
  `lana.go`, `lana_router.go`, `lana_workflow.go`, `lana_calendar.go` where they special-case Claude.
- Done when: AC-AGY-002 (record provider), AC-AGY-012 (snapshot), AC-AGY-013/014 at the Go API level.

### T-006 Web API

- Status: Done 2026-11-26 (89849cb)
- Files: new `web/antigravity.go` (+ test), `web/providers.go`, `web/router.go`, new
  `cmd/lana/web_agy.go`, `cmd/lana/web_router.go`.
- Done when: API-AGY-020…027 handler tests pass.

## Wave 4

### T-007 Web UI

- Status: Done 2026-11-26 (67c0e44)
- Files: `web/ui/src/api/router.ts`, new `web/ui/src/views/router/antigravity-page.tsx` (+ test),
  `views/router/presets.ts`, `providers.tsx`, `provider-detail.tsx`, `limits.tsx`, `limits-refresh.tsx`,
  picker grouping, `web/dist` build.
- Done when: API-AGY-030…033; vitest and build pass.

## Wave 5

### T-008 Live check

- Status: In progress 2026-11-26: scratch LANA_HOME ~/.lana-agy :18493; built-in account turn OK (persona, `run_command` approval, lana MCP `mcp_lana_memory_write` → prefix fix 8b96d24, tier "Google AI Pro", call record); deny rule `bash(rm *)` refused agy `rm`. Pending: owner UI check, second Google account sign-in, pool order
- Scratch daemon with its own `LANA_HOME` and port, built from this branch: built-in account turn with a
  tool approval and a lana MCP tool call (record how agy names MCP tools, adjust the tool note); add a
  second account with the owner's second Google account; both accounts' windows on the Router page.
