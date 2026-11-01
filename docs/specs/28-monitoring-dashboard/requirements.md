# Requirements: Monitoring dashboard

- **Issue**: —
- **Status**: Approved (on behalf of the owner, who asked to plan and assign at once on 2026-11-01)
- **Revision**: 2
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Required
- **Related ADR**: 0005 (SQLite as single storage), 0009 (session as append-only event log)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-11-01 | "implement dashboard, munculkan penggunaan token, tool, log error dll yang penting untuk di monitoring (tidak gimik ramai saja)" | goal, tone |
| Code review 2026-11-01 | `web/ui/src/views/home.tsx` is a "coming soon" placeholder at `#/home`; `step/end`, `compaction/summary`, `memory/captured` carry `core.Usage`; `turn/end` carries `reason` and `error`; `assistant/attempt` carries provider errors; `tool/result.status` is `success`, `error`, or `rejected`; index `idx_session_events_kind_created`; daemon logs go to stderr through `slog` (`cmd/lana/daemon.go:48`); the model catalog has no prices | data source, limits |
| Owner request 2026-11-01 (r2) | "dashboardnya ada filter by agent" | REQ-DSH-011 |
| Reference | `deepseek-harness/packages/experimental/client-ui-stats/README.md` (per-turn context, cache hit rate, model and tool time) | metric vocabulary |

## Goal

The owner opens **Dashboard** and sees, for a chosen time range, how many tokens lana used and
where, how tools behave, what failed, and what is running now. Every number comes from recorded
data. The page is plain and dense: numbers, one chart, tables, and an error log.

## Scope

### Included

- `#/home` becomes the dashboard; the sidebar item reads **Dashboard**.
- Ranges: last 24 hours, 7 days, 30 days.
- One read endpoint, `GET /v1/dashboard` (API-DSH-001).
- An in-memory buffer of the daemon's recent warning and error log lines.

### Not included

- Money cost (the catalog has no prices; no estimate is shown).
- Alerts, notifications, export, custom ranges, per-session drill-down beyond a link.
- Persisting daemon log lines across restarts.

## Behavior rules

### REQ-DSH-001: Range

The page has a range switch (24 hours, 7 days, 30 days; default 24 hours). All sections use the
range, except **Right now**. The page refreshes every 30 seconds while the browser tab is visible,
and on a **Refresh** button; it shows the time of the last update.

### REQ-DSH-002: Right now

Counts of sessions with status `running` and `waiting_input`, each linking to the sessions list.

### REQ-DSH-003: Summary numbers

Four numbers: **Tokens** (input + output, with cached share of input), **Turns** (count, with
failed count and share), **Tool calls** (count, with error share), **Provider retries** (count of
`assistant/attempt`).

A turn is failed when its `turn/end` has an `error` or its reason is not `end_turn`
(`error`, `max_steps`, `loop`, `interrupted`; `cancelled` is counted separately and is not failed).

### REQ-DSH-004: Token usage over time

A stacked bar chart of input (not cached), cached input, and output tokens per bucket: 1 hour for
24 hours, 1 day for 7 and 30 days. Empty buckets are zero. Hover shows the exact numbers. The same
data is available as an accessible table.

### REQ-DSH-005: Token usage breakdown

Three tables: by model (provider, model, requests, input, cached, output), by agent (agent, turns,
input, cached, output), and by source: **Chat turns** (`step/end`), **Compaction**
(`compaction/summary`), **Memory capture** (`memory/captured`).

### REQ-DSH-006: Tools

A table with one row per tool name: calls, errors, rejected, error share, average and p95 tool time
(`tool/result.created_at − tool/call.created_at`), and the last error message (first 200
characters) with its time. Sorted by calls, descending. A tool with an error share above 20% and at
least 5 calls is marked.

### REQ-DSH-007: Turn outcomes

A table of `turn/end` reasons with count and share, plus average and p95 turn duration.

### REQ-DSH-008: Error log

A list of the newest 100 errors in the range, newest first. Sources:

| Source | From | Message |
|---|---|---|
| Turn | `turn/end` with `error`, or reason `error`, `max_steps`, `loop`, `interrupted` | error class and message, or the reason |
| Provider | `assistant/attempt` | error class, status, message |
| Tool | `tool/result` with status `error` | tool name and the first 300 characters of the output |
| Daemon | daemon log records at level WARN or ERROR | message and attributes as `key=value` |

Each row shows time, source, agent, message, and for session sources a link to
`#/sessions/{id}?view=trajectory`. A source filter (All, Turn, Provider, Tool, Daemon) narrows the
list. Daemon rows cover only the time since the daemon started; the page says so.

### REQ-DSH-009: Daemon log buffer

The daemon keeps the last 500 log records at level WARN or ERROR in memory, in addition to writing
them to stderr as today. The buffer does not change what is written to stderr.

### REQ-DSH-010: Empty and error states

With no data in the range, each section shows a short empty text. A failed request keeps the last
data on screen and shows an error notice with a retry button.

### REQ-DSH-011: Agent filter (revision 2)

Next to the range switch, an **Agent** select lists "All agents" (default), every configured agent
(even one with no data in the range), and any other agent that has events in the range (for example
an agent removed from the config). Choosing an agent narrows every section to sessions of that agent: Right now,
summary numbers, token chart and tables, tools, turn outcomes, and the error log. Daemon log rows
have no agent, so they are hidden while an agent is selected, and the Daemon filter chip says so.
The range and agent choices are kept in the URL (`#/home?range=7d&agent=lana`), so a reload keeps
them. An agent with no data in the range shows the empty states, not an error.

## Acceptance criteria

| ID | Criterion | Evidence owner |
|---|---|---|
| AC-DSH-001 | Aggregation of a fixture event set gives the expected totals, buckets, by-model, by-agent, by-source, tools (avg, p95), turn reasons, and error rows | T-001 test (`internal/stats`) |
| AC-DSH-002 | `GET /v1/dashboard?range=7d` returns the API-DSH-001 shape; an unknown range returns 400 `invalid_request`; the route is behind the auth middleware | T-001 test (`web`) |
| AC-DSH-003 | The log buffer keeps the newest 500 WARN+ records, drops INFO, and still passes every record to the wrapped handler | T-001 test (`internal/logring`) |
| AC-DSH-004 | The page renders every section from a mocked response, switches range, and shows empty states | T-002 test |
| AC-DSH-005 | The error log filter narrows rows by source; session rows link to the trajectory URL | T-002 test |
| AC-DSH-006 | The sidebar shows **Dashboard** for `#/home` | T-002 test |
| AC-DSH-007 | `Aggregate` with an agent filter counts only that agent's events, drops daemon rows, and still lists every agent in `agents`; the handler passes `agent` through | T-004 test |
| AC-DSH-008 | The Agent select lists `agents`, calls `getDashboard` with the chosen agent, writes `range` and `agent` to the hash, and restores them on load | T-005 test |

## Blocking open questions

None.
