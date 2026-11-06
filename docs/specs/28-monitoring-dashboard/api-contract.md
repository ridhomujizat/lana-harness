# API contract: Monitoring dashboard

- **Status**: Approved
- **Revision**: 2

## API-DSH-001: `GET /v1/dashboard`

Registered through `Server.Handle`, so the Host, Origin, cookie, and CSRF checks apply.

Query:

- `range` = `24h` (default) | `7d` | `30d`. Any other value → `400` with the existing error body,
  code `invalid_request`.
- `agent` (revision 2, optional) = an agent name. Empty or absent means all agents. A name with no
  data is not an error: the response has zero counts and empty lists. At most 128 bytes, else `400`
  `invalid_request`.

All times are Unix milliseconds. All token fields are integers. Lists are never `null` (empty `[]`).

```json
{
  "range": "24h",
  "agent": "",
  "agents": ["default", "lana"],
  "from": 1761900000000,
  "to": 1761986400000,
  "bucket_ms": 3600000,
  "daemon_started_at": 1761980000000,
  "now": { "sessions_running": 1, "sessions_waiting_input": 0 },
  "summary": {
    "input_tokens": 120000, "cached_input_tokens": 80000, "output_tokens": 9000,
    "turns": 42, "turns_failed": 3, "turns_cancelled": 1,
    "tool_calls": 310, "tool_errors": 12, "tool_rejected": 2,
    "provider_retries": 4
  },
  "series": [
    { "start": 1761900000000, "input_tokens": 0, "cached_input_tokens": 0, "output_tokens": 0 }
  ],
  "by_model": [
    { "provider": "deepseek", "model": "deepseek-chat", "requests": 120,
      "input_tokens": 0, "cached_input_tokens": 0, "output_tokens": 0 }
  ],
  "by_agent": [
    { "agent": "assistant", "turns": 40, "input_tokens": 0, "cached_input_tokens": 0, "output_tokens": 0 }
  ],
  "by_source": [
    { "source": "turn", "count": 120, "input_tokens": 0, "cached_input_tokens": 0, "output_tokens": 0 },
    { "source": "compaction", "count": 2, "input_tokens": 0, "cached_input_tokens": 0, "output_tokens": 0 },
    { "source": "memory_capture", "count": 10, "input_tokens": 0, "cached_input_tokens": 0, "output_tokens": 0 }
  ],
  "tools": [
    { "name": "read_file", "calls": 80, "errors": 2, "rejected": 0,
      "avg_ms": 35, "p95_ms": 120, "last_error": "file not found: a.txt", "last_error_at": 1761985000000 }
  ],
  "turn_reasons": [ { "reason": "end_turn", "count": 38 } ],
  "turn_duration": { "avg_ms": 12000, "p95_ms": 45000 },
  "errors": [
    { "at": 1761985000000, "source": "tool", "level": "error", "agent": "assistant",
      "session_id": "s_abc", "turn_id": "t_1", "tool": "read_file",
      "message": "read_file: file not found: a.txt" },
    { "at": 1761984000000, "source": "daemon", "level": "warn", "agent": "", "session_id": "",
      "turn_id": "", "tool": "", "message": "mcp server exited name=github err=exit status 1" }
  ]
}
```

Field rules:

- `agent` echoes the filter (`""` for all agents). `agents` (revision 2) lists every configured agent
  plus every other agent with at least one scanned event in the range, ignoring the filter, unique,
  sorted by name; never `null`. A configured agent with no data in the range is still listed.
- With an `agent` filter, every count, series, table, and `now` covers only sessions whose agent
  equals the filter, and `errors` has no `daemon` rows.

- `series` has one entry per bucket from `from` to `to`, oldest first, zero-filled. `bucket_ms` is
  `3600000` for `24h` and `86400000` for `7d` and `30d`. Buckets align to the bucket size in UTC.
- `by_model`, `by_agent`, `tools` are sorted by their main count, descending, then name.
- `by_source` always has the three sources in the order shown.
- `turn_reasons` lists every reason seen; an empty reason is reported as `end_turn`.
- `p95_ms` is the nearest-rank 95th percentile; with no samples, `avg_ms` and `p95_ms` are `0`.
- `last_error` is at most 200 characters; `message` in `errors` at most 300 characters (tool output)
  or the full one-line text for other sources. `last_error`/`last_error_at` are `""`/`0` when none.
- `errors` holds at most 100 rows, newest first. `source` is `turn`, `provider`, `tool`, or
  `daemon`; `level` is `error` or `warn`.
- `agent` comes from the session's agent; empty for daemon rows.
- `daemon_started_at` is the start of the current daemon process image.

Covers REQ-DSH-001–009, AC-DSH-002.
