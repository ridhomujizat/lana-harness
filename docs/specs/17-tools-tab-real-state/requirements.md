# Requirements: Tools tab shows what the agent can really use

- **Status**: Approved
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: changes `API-WEB-026` (spec #9) — one field added; the agent save already accepts `tools`, `disabled_tools`, `mcp`

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner report 2026-10-31 | "si lana ask_user on di dashboard kenapa ga bisa gunakan ya?" | problem |
| Diagnosis 2026-10-31 | `agents.lana.tools` has no `ask_user`; the catalog sets `enabled = !disabled` (`lana.go:936`), ignoring `tools`; the switch only writes `disabled_tools` (spec #9 REQ-WEB-047) | cause |
| Owner decision 2026-10-31 | Fix the Tools tab; do not auto-offer `ask_user` ("saya mau nambahin sendiri") | scope |

## Goal

A switch that shows On in Agents → Tools means the model is offered that tool on the next turn, and turning
a switch On makes the tool available, so the owner can enable `ask_user` (or any tool) from the dashboard.

## Behavior rules

### REQ-TTS-001: Real state in the catalog

`GET /v1/tools?agent=<name>` shall set `enabled` to whether the tool is offered to the agent's root turns:

- built-in tool: (listed in `tools` **or** auto-offered) **and** not in `disabled_tools`. Auto-offered: `mcp_manage` always; `skill_load` when the agent has visible skills; `task` and `task_continue` when the agent is in a team.
- MCP tool `<server>/<tool>`: the server is in the agent's `mcp` list **and** neither `<server>/<tool>` nor the registry name is in `disabled_tools`.

Each entry gains `"source": "listed" | "auto" | "not_listed"` (built-ins) or `"attached" | "not_attached"` (MCP).

#### Acceptance criteria

- `AC-TTS-001`: Given `tools` without `ask_user` and no `disabled_tools`, then `ask_user` has `enabled: false`, `source: "not_listed"`.
- `AC-TTS-002`: Given `mcp_manage` not listed, then it has `enabled: true`, `source: "auto"`.
- `AC-TTS-003`: Given server `github` not in the agent's `mcp`, then its tools have `enabled: false`, `source: "not_attached"`.

### REQ-TTS-002: Switches change what is offered

Saving the Tools tab shall send `tools`, `disabled_tools`, and `mcp` together with the config version:

- On for a `not_listed` built-in: add it to `tools`, remove it from `disabled_tools`.
- On for a `listed` or `auto` built-in: remove it from `disabled_tools`.
- Off for a `listed` or `auto` built-in: add it to `disabled_tools` (do not remove it from `tools`). Off for a `not_listed` built-in writes nothing (it is off already).
- On for an MCP tool of a `not_attached` server: add the server to `mcp`, remove the tool from `disabled_tools`.
- On/Off for an attached MCP tool: remove from / add `<server>/<tool>` to `disabled_tools`. Off for a tool of a `not_attached` server writes nothing.
- "Turn all off" applies Off to each tool of the group.

The change applies from the next turn (spec #16 live config).

#### Acceptance criteria

- `AC-TTS-004`: Given `ask_user` `not_listed`, when the owner turns it On and saves, then `agents.<name>.tools` contains `ask_user`, the next turn offers it, and reloading the tab shows it On with `source: "listed"`.
- `AC-TTS-005`: Given `bash` listed, when the owner turns it Off and saves, then `disabled_tools` contains `bash` and `tools` still contains `bash`.
- `AC-TTS-006`: Given `github` not attached, when the owner turns `github/create_issue` On, then `mcp` contains `github`.

### REQ-TTS-003: Clear labels

A tool with `source: "auto"` shows a small "Always on" hint; a `not_listed` or `not_attached` tool that is Off shows no warning. No other visual change.

#### Acceptance criteria

- `AC-TTS-007`: Given `mcp_manage`, the row shows "Always on" next to the switch.

## Approval

- **Approved by**: Ridho
- **Approved on**: 2026-10-31
