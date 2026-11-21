# Contract: Deferred tool loading

- **Requirements**: [requirements.md](requirements.md) (revision 1, Approved)
- **Contract revision**: 1
- **Applies to**: config, model-facing tool
- **Status**: Draft

## API-DTL-001: `[agents.<name>] defer_tools`

- **Requirements**: REQ-DTL-001
- **Stability**: New, additive

| Location | Name | Type | Required | Meaning |
|---|---|---|---:|---|
| `[agents.<name>]` | `defer_tools` | boolean | No | Absent: defer when the agent has more than 15 deferrable tools. `true`: defer whenever it has one. `false`: never. |

## API-DTL-002: `[mcp.<name>] defer`

- **Requirements**: REQ-DTL-001
- **Stability**: New, additive

| Location | Name | Type | Required | Meaning |
|---|---|---|---:|---|
| `[mcp.<name>]` | `defer` | boolean | No | Absent or `true`: the server's tools are deferrable. `false`: always sent, not counted. |

## API-DTL-003: `tool_search`

- **Requirements**: REQ-DTL-004
- **Risk**: read (no side effect outside the turn)

```json
{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "Keywords, or select:<name>[,<name>...] for exact tools."},
    "limit": {"type": "integer", "minimum": 1, "maximum": 10, "description": "Max results, default 5."}
  },
  "required": ["query"]
}
```

Output, one block per tool:

```text
## mcp__kerjan__create_ticket
<description>
Input schema: {"type":"object",...}

Loaded: mcp__kerjan__create_ticket. Call them directly in your next step.
```

## API-DTL-004: `tools/loaded` event

| Field | Type | Meaning |
|---|---|---|
| `v` | int | payload version |
| `names` | array of strings | tools loaded in this step, in load order |
