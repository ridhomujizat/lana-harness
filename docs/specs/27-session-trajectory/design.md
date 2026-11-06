# Design: Session trajectory view

- **Requirements**: revision 1 · **Contract**: revision 1 (not required)
- **Status**: Approved

Frontend only. No Go change.

## 1. Files

| File | Role |
|---|---|
| new `web/ui/src/views/trajectory-model.ts` | pure builder: `buildTrajectory(events, childEvents) → Trajectory` (turns, steps, records, totals, spans). No React. |
| new `web/ui/src/views/trajectory.tsx` | `TrajectoryView` component: toolbar, overview, ledger, inspector. Imports `../styles/trajectory.css` itself. |
| new `web/ui/src/styles/trajectory.css` | styles, using only variables from `styles/tokens.css` |
| new `web/ui/src/views/trajectory-model.test.ts`, `trajectory.test.tsx` | tests for AC-TRJ-* |
| `web/ui/src/views/session.tsx` | header switch and render switch only |
| `web/ui/src/app/App.tsx` | pass the `view` route param to `SessionPage`, if needed |

The CSS import lives in `trajectory.tsx`, not `main.tsx`, so the parallel dashboard task does not
touch the same file.

## 2. Model (`trajectory-model.ts`)

```ts
type RecordKind = "system" | "context" | "user" | "assistant" | "retry" | "tool" | "subtool"
  | "approval" | "question" | "compacted" | "pruned" | "memory" | "setting" | "other";

interface TrajRecord {
  key: string;            // stable: kind + first seq (or call_id)
  kind: RecordKind;
  title: string;          // one-line content preview
  seqs: number[];         // every event that built it
  events: SessionEvent[]; // same events, for Raw
  startMs?: number; endMs?: number;  // undefined end = running
  status?: "running" | "success" | "error" | "rejected" | "failed";
  usage?: { input: number; cached: number; output: number };
  cumulative?: { input: number; cached: number; output: number };
  thinkChars?: number; model?: string; provider?: string; toolCalls?: number;
  requestNo?: number;     // ASSISTANT only
  callId?: string; toolName?: string; input?: unknown; output?: string;
  children?: TrajRecord[]; // TOOL under ASSISTANT, SUBTOOL under TOOL
  childSessionId?: string;
}
interface TrajStep { no: number; provider: string; model: string; records: TrajRecord[] }
interface TrajTurn { no: number; id: string; startMs: number; endMs?: number; reason?: string;
  error?: string; usage: Usage; steps: TrajStep[]; loose: TrajRecord[] }
interface Trajectory { sections: Array<{ type: "turn"; turn: TrajTurn } | { type: "between"; records: TrajRecord[] }>;
  totals: { turns: number; requests: number; toolCalls: number; usage: Usage }; spans: Span[] }
```

Rules:

- Walk events in seq order once. `turn/start` opens a turn, `step/start` opens a step, `turn/end`
  closes the turn. Events with no open turn go to the current `between` section.
- `step/start` opens a pending ASSISTANT record (start = its `created_at`). `assistant/message`
  completes it (text preview, thinking chars, tool_use count, end = its `created_at`). `step/end`
  adds usage. The cumulative usage is a running sum over `step/end` in seq order.
- `tool/call` opens a TOOL record under the latest ASSISTANT of the step, keyed by `call_id`;
  `tool/result` with the same `call_id` closes it.
- `user/message`: the first `context_blocks` content blocks become one CONTEXT record; the rest
  becomes a USER record. `runtime: true` → CONTEXT ("lana note"). `prompt_sections` → CONTEXT with
  sections.
- `session/created` of a child session links it to a parent TOOL by `parent_call_id`; the builder
  takes `childEvents: Map<childSessionId, SessionEvent[]>` (the shape `SessionPage` already keeps)
  and nests the child's ASSISTANT and TOOL records as SUBTOOL children.
- Reuse existing helpers where they fit (`stepIntent`, `formatDuration`, `formatClock` from
  `views/turn.tsx`; `lib/format.ts`). Do not duplicate them.

## 3. View (`trajectory.tsx`)

`<TrajectoryView sessionId events childEvents onLoadChild running />`.

- `useMemo(buildTrajectory)` on `events`/`childEvents`.
- Layout: toolbar (sticky) → overview (fixed height ~56 px, SVG) → split area: ledger table (left,
  scrolls) and inspector (right, ~40% width, closable; below the ledger under 900 px width).
- Ledger is a `<table>` with `<thead>` columns of REQ-TRJ-004; turn and step headers are full-width
  rows with a disclosure button (`aria-expanded`). Rows are buttons-in-cells or `tabIndex=0` rows
  with `aria-selected`.
- Overview: lay turns end to end, each turn width proportional to its duration, a fixed 8 px gap
  between turns. Spans are absolutely placed `<rect>` with `<title>` for hover.
- Follow tail: track `scrollTop + clientHeight >= scrollHeight − 24`; when true, scroll to bottom
  after updates; else show **Follow latest**.
- Visual style: dense, monospace numbers (`font-variant-numeric: tabular-nums`), kind label as a
  small uppercase tag with one color per kind, errors in the existing danger color. Match the
  existing session view look; no gradients, no animation beyond a short row highlight.

## 4. Session page (`session.tsx`)

- Read the `view` param; keep `const [tab, setTab]` in sync with the hash.
- In `chat-header`, add a two-button segmented control (`role="tablist"`).
- When `tab === "trajectory"`, render `<TrajectoryView …>` in place of `chat-messages`; keep
  notices, approval/question cards, and the composer.

## 5. Verification

`cd web/ui && npm run typecheck && npm test && npm run lint`. Do not rebuild `web/dist`; the main
session rebuilds it once after both parallel tasks finish.

## Visual architecture check

No new boundary, actor, or flow; a read-only projection of the existing event log. No diagram.
