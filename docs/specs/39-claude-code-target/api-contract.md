# Contract: Claude Code as a router target

- **Requirements**: [requirements.md](requirements.md) r1 · **Revision**: 1 · **Status**: Approved (2026-11-08)
- **Applies to**: config / HTTP / UI

| Operation ID | Change | Requirements |
|---|---|---|
| `API-CCT-001` | Address `claude-code/<model>` accepted in `[agents.*] model`, `fallback`, `[combos.*] models`, session model switch; `[providers.claude-code]` is rejected ("claude-code is built in") | REQ-CCT-001 |
| `API-CCT-002` | `[agents.*] runtime` deprecated: `"claude-code"` + `model = "M"` ≡ `model = "claude-code/M"`; dashboard writes drop `runtime` | REQ-CCT-002 |
| `API-CCT-003` | `[router] background_model` (string, address or combo without `claude-code/*`, default "") | REQ-CCT-007 |
| `API-CCT-004` | `GET /v1/router` `choices` gains group `"Claude Code"` with `claude-code/opus`, `claude-code/sonnet`, `claude-code/haiku` (labels "Claude Code · Opus" …) when the binary is found; `GET /router/v1/models` does **not** list them (the proxy never routes to Claude Code) | REQ-CCT-009 |
| `API-CCT-005` | `GET /v1/router/claude-agent` keeps its shape; `configured_agents` lists agents and combos whose chains contain `claude-code/*` (agents as `name`, combos as `combo:<name>`) | REQ-CCT-009 |
| `API-CCT-006` | Agent list/detail: `engine_label` removed from the UI's use; items keep `runtime` only for legacy files (always `""` after a dashboard save) and gain `claude_code: true` when the agent's first target is `claude-code/*` | REQ-CCT-009 |
| `API-CCT-007` | Loopback MCP (internal, not public API): `POST http://127.0.0.1:<port>/mcp` streamable HTTP, header `Authorization: Bearer <token>`; server name `lana`; Claude sees tools as `mcp__lana__<tool>` | REQ-CCT-006 |
| `API-CCT-008` | Router call records for Claude Code turns: provider `claude-code`, `requested` = the chain's requested value, model from `system/init` (renamed from `claude-agent`; limits snapshot provider renamed to `claude-code`; old rows keep `claude-agent`) | REQ-CCT-004 |
| `API-CCT-009` | UI: no Engine radio; Claude Code models in pickers; Router Subscriptions card "Claude Code" | REQ-CCT-009 |
