# Contract: Claude Agent runtime

- **Requirements**: [requirements.md](requirements.md) r1 Approved
- **Contract revision**: 1
- **Applies to**: config / HTTP / events
- **Status**: Approved (agent recommendation on the owner's instruction, 2026-11-08)

## Operation index

| Operation ID | Type | Path/key | Requirements | Acceptance criteria |
|---|---|---|---|---|
| `API-CCR-001` | config | `[agents.<name>]` runtime keys | `REQ-CCR-001`, `REQ-CCR-003`, `REQ-CCR-007` | `AC-CCR-001`, `AC-CCR-003`, `AC-CCR-008` |
| `API-CCR-002` | events | session event payloads | `REQ-CCR-002`, `REQ-CCR-004` | `AC-CCR-002`, `AC-CCR-004` |
| `API-CCR-003` | HTTP | `GET /v1/router/claude-agent` | `REQ-CCR-008` | `AC-CCR-009` |
| `API-CCR-004` | HTTP | agent endpoints | `REQ-CCR-009` | `AC-CCR-010` |

## API-CCR-001: config

```toml
[agents.claude]
runtime = "claude-code"     # "" = lana's own loop
auth = "subscription"       # or "api_key"
api_key_env = ""            # required when auth = "api_key"
model = "opus"              # "" = Claude Code default
reasoning_effort = ""       # "", low, medium, high, xhigh, max → --effort
binary = ""                 # "" = `claude` on PATH
allow_scheduled = false
workspace = "~/coding"
permission = "ask"          # ask | never | trust (ADR 0004)
rules = { allow = [], deny = ["bash(rm *)"] }
description = ""
title = ""
color = ""
```

`permission` maps to the answer lana gives `can_use_tool`: `ask` → engine (rules, then owner),
`trust` → allow unless a deny rule matches, `never` → deny unless an allow rule matches.

## API-CCR-002: events

- `session/meta` is not a new kind: the Claude session UUID is stored in a `system/message` event of
  subtype `runtime` with payload `{"runtime":"claude-code","claude_session_id":"<uuid>","model":"…","tools":[…],"version":"2.1.319"}`,
  written on the first `system/init` of each process.
- `tool/call` payload gains `"external": true` (omitted when false).
- `step/start` / `step/end` use `provider: "claude-agent"` and the model from `system/init`.
- `turn/end` reasons as today; `cancelled` on cancel.

## API-CCR-003: `GET /v1/router/claude-agent`

```json
{"configured_agents":["claude"],"binary":"/Users/x/.local/bin/claude","version":"2.1.319",
 "version_ok":true,"logged_in":true,"auth_method":"claude.ai","error":"",
 "running":1,"queued":0,"max_concurrent":2}
```

The same limits appear in `GET /v1/router/limits` as provider `claude-agent` (spec 37 API-RTR-013).
`logged_in` comes from running `claude auth status` (no credential file is read). Results are cached for
60 s.

## API-CCR-004: agent endpoints

The existing agent create/update endpoints accept the keys of API-CCR-001; agent list/detail items gain
`"runtime":"claude-code"` and `"engine_label":"Claude Agent"` (empty runtime → `"lana"`).
