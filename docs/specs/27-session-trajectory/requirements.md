# Requirements: Session trajectory view

- **Issue**: —
- **Status**: Approved (on behalf of the owner, who asked to plan and assign at once on 2026-11-01)
- **Revision**: 1
- **Owner**: Ridho
- **Contract**: [api-contract.md](api-contract.md) — Not required
- **Related ADR**: 0009 (session as append-only event log)

## Sources

| Source | Reference | Used for |
|---|---|---|
| Owner request 2026-11-01 | "di tampilan session munculkan tampilan seperti di deepseek harness … agar saya bisa check dan mempelajari apa yang dilakukan lana harness", then "point satu yg saya maksud trajectory /Users/ridho/coding/project/rnd/deepseek-harness" | goal |
| Reference implementation | `deepseek-harness/packages/client/ui-trajectory` (README, `locales.ts` en dictionary, `TrajectoryToolbar.tsx`), test snapshot `apps/web/tests/expected/retained-steering/trajectory.expected.md` | layout, record kinds, columns, inspector tabs |
| Code review 2026-11-01 | `core/event.go` event kinds and payloads; `GET /v1/sessions/{id}/events` returns every event with `created_at` in ms; `web/ui/src/views/session.tsx` `SessionPage` already holds the live `events` state and lazy child events | data source |

## Goal

The owner opens a **Trajectory** tab on any session and sees exactly what lana did, in log order:
the system prompt, injected context, every model request with its tokens, time and model, every
tool call with its input, output, status and duration, approvals, questions, retries, compaction,
pruning, and memory capture. Selecting a row opens an inspector with the raw data. The view is
read-only and updates live while a turn runs.

## Definitions

- **Record** — one ledger row built from one or more events (a tool record joins `tool/call` and
  its `tool/result` by `call_id`).
- **Request** — one model call: `step/start` → `assistant/message` (or the step's failure), with the
  `step/end` usage. Requests are numbered `Request #N` per session.
- **Model time** — `assistant/message.created_at − step/start.created_at`.
- **Tool time** — `tool/result.created_at − tool/call.created_at`.

## Scope

### Included

- A Chat | Trajectory switch in the session header; the choice is in the URL (`?view=trajectory`).
- Toolbar, timing overview, ledger table, record inspector, live updates.
- Subagent (child session) records nested under the tool call that started them.

### Not included

- Time to first token and throughput (no first-token timestamp is recorded today).
- Drag-to-zoom on the overview, virtualized rows, editing, or replay.
- New backend endpoints or event kinds.

## Behavior rules

### REQ-TRJ-001: View switch

The session header shows a two-option switch, **Chat** and **Trajectory**. Choosing one updates the
hash to `#/sessions/{id}` or `#/sessions/{id}?view=trajectory` without a reload. Opening a URL with
`?view=trajectory` opens the Trajectory tab. The composer, notices, and approval/question cards stay
available on both tabs.

### REQ-TRJ-002: Record kinds

The ledger shows these kinds, with the label in the first column:

| Label | Built from |
|---|---|
| SYSTEM | `system/message` (first one: "System prompt" with its section names; later ones: "System prompt changed" with the changed section names) |
| CONTEXT | the first `context_blocks` blocks of a `user/message`, a `user/message` with `runtime: true`, and `prompt_sections` updates |
| USER | the owner part of a `user/message` (text, attachment count, `continue` marker) |
| ASSISTANT | one request: text preview, or "(tool call only)" |
| RETRY | `assistant/attempt` (error class, status, message) |
| TOOL | `tool/call` + `tool/result` (name, one-line input preview, status) |
| SUBTOOL | records of a child session, nested under the parent TOOL row |
| APPROVAL | `approval/asked` + `approval/decided` (tool, risk, outcome, wait time) |
| QUESTION | `question/asked` + `question/answered` |
| COMPACTED | `compaction/summary` (tokens before → after, model) |
| PRUNED | `tool_result_pruned` (call id, source seq) |
| MEMORY | `memory/captured` (operation count, usage) |
| SETTING | `model/selection`, `approval/policy` |

Unknown kinds appear as a generic row with the kind name, so nothing in the log is hidden.

### REQ-TRJ-003: Grouping

Records are grouped by turn (`turn/start` … `turn/end`) and, inside a turn, by step
(`step/start` … `step/end`). A turn header shows `Turn N`, start clock, duration, end reason
(a failed or abnormal end is visibly marked), and the turn's token totals. A step header shows
`Step N · provider/model`. Events outside any turn appear in a **Between turns** section at their
log position. TOOL rows sit under the ASSISTANT row that requested them.

### REQ-TRJ-004: Columns

Columns: **Kind**, **Content**, **Input**, **Output**, **Think**, **Time**, **Model**, **Tools**.
Input shows input tokens with the cached part; Output shows output tokens; Think shows the size of
the thinking text in characters; Time shows model time or tool time; Model shows the short model
name; Tools shows the number of tool calls in the request. A value that is not recorded shows "—",
never an estimate.

### REQ-TRJ-005: Toolbar

A sticky toolbar has: expand/collapse all turns, expand/collapse all tool calls, and a search box.
Search keeps rows whose label or content contains the text (case-insensitive) plus their turn and
step headers. The toolbar also shows session totals: turns, requests, tool calls, input / cached /
output tokens.

### REQ-TRJ-006: Timing overview

Above the ledger, a horizontal overview draws each turn's requests, tool calls, and approval waits
as spans at their real start and duration, with idle gaps between turns compressed to a fixed
small gap. Spans are colored by kind; a failed tool or request is marked. Hover shows kind, name,
start clock, and duration. Clicking a span selects its record and scrolls the ledger to it. A
running span shows only a start marker.

### REQ-TRJ-007: Inspector

Selecting a row (click, or Up/Down + Enter) opens an inspector panel. Its header shows the label,
title, event seq numbers, and clock. Tabs depend on the kind:

| Tab | Kinds | Content |
|---|---|---|
| Summary | all | readable rendering: markdown for assistant text, thinking text, tool input and output |
| Payload | TOOL | tool input as pretty JSON, with copy |
| Result | TOOL | output text (pretty JSON when the output parses as JSON), status, image count |
| Usage | ASSISTANT, COMPACTED, MEMORY | this request's input / cached / output tokens and cache share; session cumulative up to this request |
| Timing | all with time | start (local clock and ISO), end, duration, source "Session timestamps" |
| System prompt | SYSTEM, CONTEXT with sections | each section name, character count, and text, collapsible |
| Raw | all | the JSON of every event that built the record, with seq and kind |

Escape closes the inspector.

### REQ-TRJ-008: Live updates

While a turn runs, new events add or update records without a reload. The ledger follows the tail
until the owner scrolls up; a **Follow latest** button then resumes following. A running request or
tool shows a running state and no duration.

### REQ-TRJ-009: Subagents

A TOOL row whose call started a child session (`session/created.parent_call_id`) can expand to show
the child's records as SUBTOOL rows (one level), loaded on demand, and links to the child session's
Trajectory tab.

### REQ-TRJ-010: Long sessions

When a session has more than 10 turns, only the last 3 turns start expanded. Rows of collapsed turns
are not rendered.

## Acceptance criteria

| ID | Criterion | Evidence owner |
|---|---|---|
| AC-TRJ-001 | Switching tabs changes the hash; loading `?view=trajectory` opens Trajectory | T-001 test |
| AC-TRJ-002 | A fixture session with system prompt, context, user, 2 requests, 3 tool calls (one error), an approval, a retry, a compaction renders each kind with the right label and columns | T-001 test |
| AC-TRJ-003 | Turn and step headers group the rows; between-turn events land in Between turns | T-001 test |
| AC-TRJ-004 | Search filters rows and keeps their headers | T-001 test |
| AC-TRJ-005 | Selecting a TOOL row opens Payload and Result with the recorded input and output; Raw shows both events | T-001 test |
| AC-TRJ-006 | Usage tab shows this request's tokens and the cumulative total up to it | T-001 test |
| AC-TRJ-007 | Appending events to the `events` prop adds rows without remount | T-001 test |
| AC-TRJ-008 | Unrecorded values show "—" | T-001 test |

## Blocking open questions

None.
