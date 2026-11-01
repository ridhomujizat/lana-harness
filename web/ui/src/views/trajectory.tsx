import {
  Fragment,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type {
  KeyboardEvent as ReactKeyboardEvent,
  ReactNode,
} from "react";
import type { SessionEvent } from "../api/sessions";
import {
  outcomeWords,
  prettyJSON,
  stopReasonWords,
} from "../lib/format";
import { Markdown } from "../lib/markdown";
import { formatClock, formatDuration, stepIntent } from "./turn";
import {
  buildTrajectory,
  recordKindLabel,
} from "./trajectory-model";
import type {
  Trajectory,
  TrajectorySpan,
  TrajRecord,
  TrajStep,
  TrajTurn,
  Usage,
} from "./trajectory-model";
import "../styles/trajectory.css";

/* The read-only Trajectory tab of a session (spec 27). It projects the durable
   event log into a turn/step ledger, a timing overview, and a record inspector.
   Nothing on this surface writes: it is an inspection view of what lana did. */

export interface TrajectoryViewProps {
  sessionId: string;
  events: SessionEvent[];
  childEvents: Record<string, SessionEvent[]>;
  onLoadChild?: (childId: string) => void | Promise<void>;
  running?: boolean;
}

type InspectorTab =
  | "summary"
  | "payload"
  | "result"
  | "usage"
  | "timing"
  | "system"
  | "raw";

const TAB_LABELS: Record<InspectorTab, string> = {
  summary: "Summary",
  payload: "Payload",
  result: "Result",
  usage: "Usage",
  timing: "Timing",
  system: "System prompt",
  raw: "Raw",
};

const COLUMN_COUNT = 8;
const TOKEN_FORMAT = new Intl.NumberFormat("en-US");

interface RecordEntry {
  record: TrajRecord;
  turn?: TrajTurn;
  step?: TrajStep;
  parent?: TrajRecord;
}

function tokenText(value: number | undefined): string {
  return value === undefined ? "—" : TOKEN_FORMAT.format(value);
}

function shortModelName(value: string): string {
  const text = String(value || "");
  const parts = text.split(":");
  return parts.length > 1 ? parts.slice(1).join(":") : text;
}

function durationText(record: TrajRecord): string {
  if (record.waitMs !== undefined) {
    return formatDuration(record.waitMs);
  }
  if (record.startMs !== undefined && record.endMs !== undefined) {
    return formatDuration(Math.max(0, record.endMs - record.startMs));
  }
  return "—";
}

function cacheShareText(usage: Usage | undefined): string {
  if (!usage || usage.input <= 0) {
    return "—";
  }
  return Math.round((usage.cached / usage.input) * 100) + "%";
}

function statusText(record: TrajRecord): string {
  switch (record.status) {
    case "running":
      return "running";
    case "error":
      return "error";
    case "failed":
      return "failed";
    case "rejected":
      return "rejected";
    default:
      return "";
  }
}

function recordMatches(record: TrajRecord, query: string): boolean {
  if (!query) {
    return true;
  }
  const haystack = (recordKindLabel(record) + " " + record.title).toLowerCase();
  return haystack.indexOf(query) >= 0;
}

function childrenFor(record: TrajRecord, query: string): TrajRecord[] {
  const children = record.children || [];
  if (!query) {
    return children;
  }
  return children.filter((child) => {
    if (recordMatches(child, query)) {
      return true;
    }
    return (child.children || []).some((entry) => recordMatches(entry, query));
  });
}

function filterRecords(records: TrajRecord[], query: string): TrajRecord[] {
  if (!query) {
    return records;
  }
  return records.filter((record) => {
    if (recordMatches(record, query)) {
      return true;
    }
    return (record.children || []).some((child) => recordMatches(child, query));
  });
}

function indexRecords(trajectory: Trajectory): Map<string, RecordEntry> {
  const index = new Map<string, RecordEntry>();
  const add = (
    record: TrajRecord,
    turn?: TrajTurn,
    step?: TrajStep,
    parent?: TrajRecord
  ) => {
    index.set(record.key, { record, turn, step, parent });
    for (const child of record.children || []) {
      add(child, turn, step, record);
    }
  };
  for (const section of trajectory.sections) {
    if (section.type === "between") {
      for (const record of section.records) {
        add(record);
      }
      continue;
    }
    const turn = section.turn;
    for (const record of turn.loose) {
      add(record, turn);
    }
    for (const step of turn.steps) {
      for (const record of step.records) {
        add(record, turn, step);
      }
    }
  }
  return index;
}

function sectionTurns(trajectory: Trajectory): TrajTurn[] {
  return trajectory.sections
    .filter(
      (section): section is { type: "turn"; turn: TrajTurn } =>
        section.type === "turn"
    )
    .map((section) => section.turn);
}

function charCount(text: string): number {
  return Array.from(String(text || "")).length;
}

function isoText(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms)) {
    return "—";
  }
  const date = new Date(ms);
  return Number.isNaN(date.getTime()) ? "—" : date.toISOString();
}

function clockAndIso(ms: number | undefined): string {
  const iso = isoText(ms);
  if (iso === "—") {
    return "—";
  }
  return (formatClock(Number(ms)) || "—") + " · " + iso;
}

function prettyOutputText(text: string): string {
  try {
    const value: unknown = JSON.parse(text);
    if (value !== null && typeof value === "object") {
      return prettyJSON(value);
    }
  } catch {
    /* Not JSON: the recorded text is shown exactly. */
  }
  return text;
}

function outputBlock(output: string | undefined): ReactNode {
  if (output === undefined || output === "") {
    return <p className="trajectory-empty">No output captured.</p>;
  }
  return <pre>{prettyOutputText(output)}</pre>;
}

function jsonBlock(value: unknown, empty: string): ReactNode {
  if (value === undefined) {
    return <p className="trajectory-empty">{empty}</p>;
  }
  return <pre>{prettyJSON(value)}</pre>;
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);

  function copy() {
    const clipboard =
      typeof navigator === "undefined" ? null : navigator.clipboard;
    if (!clipboard || typeof clipboard.writeText !== "function") {
      return;
    }
    try {
      clipboard
        .writeText(text)
        .then(() => setCopied(true))
        .catch(() => undefined);
    } catch {
      /* An insecure origin can reject the clipboard API. */
    }
  }

  return (
    <button type="button" className="trajectory-copy" onClick={copy}>
      {copied ? "Copied" : "Copy"}
    </button>
  );
}

function TrajectoryToolbar({
  totals,
  usageRecorded,
  allTurnsCollapsed,
  callsOpen,
  query,
  following,
  onToggleTurns,
  onToggleCalls,
  onQuery,
  onFollow,
}: {
  totals: Trajectory["totals"];
  usageRecorded: boolean;
  allTurnsCollapsed: boolean;
  callsOpen: boolean;
  query: string;
  following: boolean;
  onToggleTurns: () => void;
  onToggleCalls: () => void;
  onQuery: (value: string) => void;
  onFollow: () => void;
}) {
  return (
    <div className="trajectory-toolbar" role="toolbar" aria-label="Trajectory toolbar">
      <div className="trajectory-toolbar-group">
        <button
          type="button"
          className="trajectory-button"
          aria-label={allTurnsCollapsed ? "Expand turns" : "Collapse turns"}
          onClick={onToggleTurns}
        >
          {allTurnsCollapsed ? "Expand turns" : "Collapse turns"}
        </button>
        <button
          type="button"
          className="trajectory-button"
          aria-pressed={callsOpen}
          aria-label={callsOpen ? "Collapse calls" : "Expand calls"}
          onClick={onToggleCalls}
        >
          {callsOpen ? "Collapse calls" : "Expand calls"}
        </button>
      </div>
      <span className="trajectory-totals" aria-label="Session totals">
        <strong>{totals.turns}</strong>{" "}
        {totals.turns === 1 ? "turn" : "turns"}
        <span className="trajectory-totals-dot" aria-hidden="true">
          ·
        </span>
        <strong>{totals.requests}</strong>{" "}
        {totals.requests === 1 ? "request" : "requests"}
        <span className="trajectory-totals-dot" aria-hidden="true">
          ·
        </span>
        <strong>{totals.toolCalls}</strong>{" "}
        {totals.toolCalls === 1 ? "tool call" : "tool calls"}
        <span className="trajectory-totals-dot" aria-hidden="true">
          ·
        </span>
        {usageRecorded ? (
          <>
            {tokenText(totals.usage.input)} in
            <span className="trajectory-totals-dot" aria-hidden="true">
              /
            </span>
            {tokenText(totals.usage.cached)} cached
            <span className="trajectory-totals-dot" aria-hidden="true">
              /
            </span>
            {tokenText(totals.usage.output)} out
          </>
        ) : (
          <>tokens —</>
        )}
      </span>
      <div className="trajectory-search">
        <input
          type="search"
          className="trajectory-search-input"
          aria-label="Search trajectory"
          placeholder="Search"
          value={query}
          onChange={(event) => onQuery(event.target.value)}
        />
      </div>
      {!following ? (
        <button
          type="button"
          className="trajectory-button trajectory-follow"
          onClick={onFollow}
        >
          Follow latest
        </button>
      ) : null}
    </div>
  );
}

function spanTitle(span: TrajectorySpan): string {
  const kind = span.kind.toUpperCase();
  const duration =
    span.endMs !== undefined
      ? formatDuration(Math.max(0, span.endMs - span.startMs))
      : "running";
  return kind + " · " + span.label + " · " + formatClock(span.startMs) + " · " + duration;
}

function TrajectoryOverview({
  trajectory,
  selectedKey,
  onSelect,
}: {
  trajectory: Trajectory;
  selectedKey: string;
  onSelect: (recordKey: string) => void;
}) {
  const turns = sectionTurns(trajectory);
  if (turns.length === 0) {
    return null;
  }

  const width = 1000;
  const gap = 8;
  const height = 56;
  const lanes: Record<TrajectorySpan["kind"], number> = {
    assistant: 4,
    tool: 20,
    approval: 36,
  };
  const laneHeight = 12;

  const spansByTurn = new Map<number, TrajectorySpan[]>();
  for (const span of trajectory.spans) {
    const list = spansByTurn.get(span.turnNo) || [];
    list.push(span);
    spansByTurn.set(span.turnNo, list);
  }

  const durations = turns.map((turn) => {
    if (turn.endMs !== undefined) {
      return Math.max(0, turn.endMs - turn.startMs);
    }
    let last = turn.startMs;
    for (const span of spansByTurn.get(turn.no) || []) {
      last = Math.max(last, span.endMs ?? span.startMs);
    }
    return Math.max(0, last - turn.startMs);
  });
  const totalDuration = durations.reduce((sum, value) => sum + value, 0);
  let fractions = durations.map((value) =>
    totalDuration > 0 ? value / totalDuration : 1 / turns.length
  );
  const minimum = 0.02;
  fractions = fractions.map((value) => Math.max(minimum, value));
  const fractionTotal = fractions.reduce((sum, value) => sum + value, 0);
  fractions = fractions.map((value) => value / fractionTotal);
  const available = Math.max(1, width - gap * (turns.length - 1));
  const layouts: { turn: TrajTurn; x: number; width: number }[] = [];
  let offset = 0;
  turns.forEach((turn, index) => {
    const turnWidth = Math.max(6, (fractions[index] || 0) * available);
    layouts.push({ turn, x: offset, width: turnWidth });
    offset += turnWidth + gap;
  });

  return (
    <div className="trajectory-overview">
      <div className="trajectory-overview-head">
        <span>Timing</span>
        <span>Idle gaps compressed</span>
      </div>
      <svg
        className="trajectory-overview-svg"
        viewBox={"0 0 " + width + " " + height}
        preserveAspectRatio="none"
        role="group"
        aria-label="Trajectory timing overview"
      >
        <rect
          className="trajectory-overview-lane"
          x={0}
          y={lanes.assistant}
          width={width}
          height={laneHeight}
          rx={2}
        />
        <rect
          className="trajectory-overview-lane"
          x={0}
          y={lanes.tool}
          width={width}
          height={laneHeight}
          rx={2}
        />
        <rect
          className="trajectory-overview-lane"
          x={0}
          y={lanes.approval}
          width={width}
          height={laneHeight}
          rx={2}
        />
        {layouts.map((layout, index) => (
          <Fragment key={"overview-turn-" + layout.turn.id}>
            {index < layouts.length - 1 ? (
              <rect
                className="trajectory-overview-gap"
                x={layout.x + layout.width}
                y={0}
                width={gap}
                height={height}
              />
            ) : null}
            {(spansByTurn.get(layout.turn.no) || []).map((span) => {
              const turnDuration = Math.max(1, durations[index] || 1);
              const ratio = Math.min(
                1,
                Math.max(0, (span.startMs - layout.turn.startMs) / turnDuration)
              );
              const x = layout.x + ratio * layout.width;
              const y = lanes[span.kind];
              const title = spanTitle(span);
              if (span.endMs === undefined) {
                return (
                  <line
                    key={span.key}
                    className={
                      "trajectory-overview-marker" +
                      (span.failed ? " is-failed" : "")
                    }
                    x1={x}
                    x2={x}
                    y1={y}
                    y2={y + laneHeight}
                    role="button"
                    tabIndex={0}
                    aria-label={title}
                    onClick={() => onSelect(span.recordKey)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        onSelect(span.recordKey);
                      }
                    }}
                  >
                    <title>{title}</title>
                  </line>
                );
              }
              const spanWidth = Math.max(
                1.5,
                Math.min(
                  layout.width - (x - layout.x),
                  ((span.endMs - span.startMs) / turnDuration) * layout.width
                )
              );
              return (
                <rect
                  key={span.key}
                  className={
                    "trajectory-overview-span is-" +
                    span.kind +
                    (span.failed ? " is-failed" : "") +
                    (selectedKey === span.recordKey ? " is-selected" : "")
                  }
                  x={x}
                  y={y}
                  width={spanWidth}
                  height={laneHeight}
                  rx={1.5}
                  role="button"
                  tabIndex={0}
                  aria-label={title}
                  onClick={() => onSelect(span.recordKey)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ") {
                      event.preventDefault();
                      onSelect(span.recordKey);
                    }
                  }}
                >
                  <title>{title}</title>
                </rect>
              );
            })}
          </Fragment>
        ))}
      </svg>
    </div>
  );
}

function SummaryPanel({ record }: { record: TrajRecord }) {
  const inner = record.innerKind || record.kind;
  switch (inner) {
    case "assistant":
      return (
        <>
          {record.thinking ? (
            <details className="trajectory-section">
              <summary>Thinking</summary>
              <div className="trajectory-section-body">
                <Markdown text={record.thinking} />
              </div>
            </details>
          ) : null}
          {record.text ? (
            <Markdown text={record.text} />
          ) : (
            <p className="trajectory-empty">No text recorded.</p>
          )}
        </>
      );
    case "tool":
      return (
        <>
          <h3>{stepIntent(record.toolName)}</h3>
          <h3>Input</h3>
          {jsonBlock(record.input, "No input captured.")}
          <h3>Output</h3>
          {outputBlock(record.output)}
          {record.images ? (
            <p>
              {record.images} image{record.images === 1 ? "" : "s"} recorded.
            </p>
          ) : null}
        </>
      );
    case "user":
      return record.text ? (
        <p>{record.text}</p>
      ) : (
        <p className="trajectory-empty">No text recorded.</p>
      );
    case "system":
    case "context":
      return record.text ? (
        <Markdown text={record.text} />
      ) : (
        <p className="trajectory-empty">No sections recorded.</p>
      );
    case "retry":
      return (
        <dl className="trajectory-timing">
          <dt>Error class</dt>
          <dd>{record.errorClass || "—"}</dd>
          <dt>Status</dt>
          <dd>{record.errorStatus !== undefined ? record.errorStatus : "—"}</dd>
          <dt>Message</dt>
          <dd>{record.errorMessage || "—"}</dd>
        </dl>
      );
    case "approval":
      return (
        <dl className="trajectory-timing">
          <dt>Tool</dt>
          <dd>{record.toolName || "—"}</dd>
          <dt>Risk</dt>
          <dd>{record.risk || "—"}</dd>
          <dt>Outcome</dt>
          <dd>{record.outcome ? outcomeWords(record.outcome) : "—"}</dd>
          <dt>Wait</dt>
          <dd>{record.waitMs !== undefined ? formatDuration(record.waitMs) : "—"}</dd>
        </dl>
      );
    case "question":
      return (
        <dl className="trajectory-timing">
          <dt>Outcome</dt>
          <dd>{record.outcome || "—"}</dd>
          <dt>Wait</dt>
          <dd>{record.waitMs !== undefined ? formatDuration(record.waitMs) : "—"}</dd>
        </dl>
      );
    case "compacted":
      return (
        <>
          {record.summary ? (
            <Markdown text={record.summary} />
          ) : (
            <p className="trajectory-empty">No summary recorded.</p>
          )}
          <p className="trajectory-muted">{record.title}</p>
        </>
      );
    case "pruned":
      return (
        <>
          <dl className="trajectory-timing">
            <dt>Call</dt>
            <dd>{record.callId || "—"}</dd>
            <dt>Source seq</dt>
            <dd>{record.sourceSeq !== undefined ? record.sourceSeq : "—"}</dd>
          </dl>
          {outputBlock(record.output)}
        </>
      );
    case "memory":
      if (!record.ops || record.ops.length === 0) {
        return <p className="trajectory-empty">No operations recorded.</p>;
      }
      return (
        <ul className="trajectory-list">
          {record.ops.map((op, index) => (
            <li key={index}>
              {op.action || "change"} {op.target || "memory"}
              {op.content ? ": " + op.content : ""}
              {op.oldText ? " (was: " + op.oldText + ")" : ""}
            </li>
          ))}
        </ul>
      );
    default:
      return (
        <>
          {record.title ? <p>{record.title}</p> : null}
          {record.text ? <pre>{record.text}</pre> : null}
        </>
      );
  }
}

function UsagePanel({ record }: { record: TrajRecord }) {
  const usage = record.usage;
  const cumulative = record.cumulative;
  return (
    <>
      <table className="trajectory-usage">
        <thead>
          <tr>
            <th scope="col"></th>
            <th scope="col">Input</th>
            <th scope="col">Cached</th>
            <th scope="col">Output</th>
            <th scope="col">Cache share</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <th scope="row">This request</th>
            <td>{tokenText(usage?.input)}</td>
            <td>{tokenText(usage?.cached)}</td>
            <td>{tokenText(usage?.output)}</td>
            <td>{cacheShareText(usage)}</td>
          </tr>
          <tr>
            <th scope="row">Session cumulative</th>
            <td>{tokenText(cumulative?.input)}</td>
            <td>{tokenText(cumulative?.cached)}</td>
            <td>{tokenText(cumulative?.output)}</td>
            <td>{cacheShareText(cumulative)}</td>
          </tr>
        </tbody>
      </table>
      {!usage ? (
        <p className="trajectory-empty">Usage not recorded.</p>
      ) : null}
    </>
  );
}

function TimingPanel({ record }: { record: TrajRecord }) {
  return (
    <dl className="trajectory-timing">
      <dt>Start</dt>
      <dd>{clockAndIso(record.startMs)}</dd>
      <dt>End</dt>
      <dd>{clockAndIso(record.endMs)}</dd>
      <dt>Duration</dt>
      <dd>{durationText(record)}</dd>
      <dt>Source</dt>
      <dd>Session timestamps</dd>
    </dl>
  );
}

function SystemPanel({ record }: { record: TrajRecord }) {
  const sections = record.sections || [];
  if (sections.length === 0) {
    return <p className="trajectory-empty">No sections recorded.</p>;
  }
  return (
    <>
      {sections.map((section, index) => (
        <details
          className="trajectory-section"
          key={section.name + "-" + index}
          open={index === 0}
        >
          <summary>
            {section.name || "Section"}
            <span className="trajectory-muted">
              {" "}
              · {charCount(section.text)} chars
            </span>
          </summary>
          <div className="trajectory-section-body">
            <pre>{section.text}</pre>
          </div>
        </details>
      ))}
    </>
  );
}

function RawPanel({ record }: { record: TrajRecord }) {
  if (record.events.length === 0) {
    return <p className="trajectory-empty">No events recorded.</p>;
  }
  return (
    <>
      {record.events.map((event) => (
        <div key={event.seq}>
          <div className="trajectory-json-head">
            <span>
              #{event.seq} · {event.kind}
            </span>
          </div>
          <pre>{prettyJSON(event)}</pre>
        </div>
      ))}
    </>
  );
}

function PayloadPanel({ record }: { record: TrajRecord }) {
  if (record.input === undefined) {
    return <p className="trajectory-empty">No payload captured.</p>;
  }
  const text = prettyJSON(record.input);
  return (
    <>
      <div className="trajectory-json-head">
        <span>Input</span>
        <CopyButton text={text} />
      </div>
      <pre>{text}</pre>
    </>
  );
}

function ResultPanel({ record }: { record: TrajRecord }) {
  const output = record.output;
  const status = record.status || "";
  return (
    <>
      <dl className="trajectory-timing">
        <dt>Status</dt>
        <dd>{status || "—"}</dd>
        <dt>Images</dt>
        <dd>{record.images !== undefined ? record.images : "—"}</dd>
      </dl>
      {output ? (
        <>
          <div className="trajectory-json-head">
            <span>Output</span>
            <CopyButton text={output} />
          </div>
          <pre>{prettyOutputText(output)}</pre>
        </>
      ) : (
        <p className="trajectory-empty">No result captured.</p>
      )}
    </>
  );
}

function tabsFor(record: TrajRecord): InspectorTab[] {
  const inner = record.innerKind || record.kind;
  const tabs: InspectorTab[] = ["summary"];
  if (inner === "tool") {
    tabs.push("payload", "result");
  }
  if (inner === "assistant" || inner === "compacted" || inner === "memory") {
    tabs.push("usage");
  }
  if (record.startMs !== undefined || record.endMs !== undefined) {
    tabs.push("timing");
  }
  if (
    inner === "system" ||
    (inner === "context" && (record.sections?.length || 0) > 0)
  ) {
    tabs.push("system");
  }
  tabs.push("raw");
  return tabs;
}

function RecordInspector({
  record,
  onClose,
}: {
  record: TrajRecord;
  onClose: () => void;
}) {
  const tabs = useMemo(() => tabsFor(record), [record]);
  const [tab, setTab] = useState<InspectorTab>("summary");
  const active = tabs.indexOf(tab) >= 0 ? tab : "summary";
  const first = record.events[0];
  const clockMs = record.startMs ?? (first ? Number(first.created_at) : 0);

  let panel: ReactNode;
  switch (active) {
    case "payload":
      panel = <PayloadPanel record={record} />;
      break;
    case "result":
      panel = <ResultPanel record={record} />;
      break;
    case "usage":
      panel = <UsagePanel record={record} />;
      break;
    case "timing":
      panel = <TimingPanel record={record} />;
      break;
    case "system":
      panel = <SystemPanel record={record} />;
      break;
    case "raw":
      panel = <RawPanel record={record} />;
      break;
    default:
      panel = <SummaryPanel record={record} />;
      break;
  }

  return (
    <aside className="trajectory-inspector" aria-label="Record inspector">
      <header className="trajectory-inspector-head">
        <div className="trajectory-inspector-heading">
          <p className="trajectory-inspector-meta">
            <span
              className={
                "trajectory-kind trajectory-kind-" + record.kind
              }
            >
              {recordKindLabel(record)}
            </span>
          </p>
          <h2 className="trajectory-inspector-title">{record.title}</h2>
          <p className="trajectory-inspector-meta">
            {record.seqs.length > 0
              ? "Events #" + record.seqs.join(", #")
              : "No events"}
            {clockMs ? " · " + formatClock(clockMs) : ""}
          </p>
        </div>
        <button
          type="button"
          className="trajectory-close"
          aria-label="Close inspector"
          onClick={onClose}
        >
          Close
        </button>
      </header>
      <div
        className="trajectory-tabs"
        role="tablist"
        aria-label="Inspector tabs"
      >
        {tabs.map((entry) => (
          <button
            key={entry}
            type="button"
            role="tab"
            className="trajectory-tab"
            aria-selected={entry === active}
            onClick={() => setTab(entry)}
          >
            {TAB_LABELS[entry]}
          </button>
        ))}
      </div>
      <div className="trajectory-panel" role="tabpanel" aria-label={TAB_LABELS[active]}>
        {panel}
      </div>
    </aside>
  );
}

function turnReason(turn: TrajTurn): ReactNode {
  const reason = turn.reason || "";
  if (!reason) {
    return <span className="trajectory-muted">—</span>;
  }
  if (reason === "error") {
    return <span className="trajectory-turn-error">failed</span>;
  }
  if (reason === "end_turn") {
    return <span>end of turn</span>;
  }
  return <span className="trajectory-turn-warn">{stopReasonWords(reason)}</span>;
}

function turnUsageText(turn: TrajTurn): string {
  if (!turn.usageRecorded) {
    return "usage —";
  }
  return (
    tokenText(turn.usage.input) +
    " in · " +
    tokenText(turn.usage.cached) +
    " cached · " +
    tokenText(turn.usage.output) +
    " out"
  );
}

export function TrajectoryView({
  sessionId,
  events,
  childEvents,
  onLoadChild,
  running = false,
}: TrajectoryViewProps) {
  const trajectory = useMemo(
    () => buildTrajectory(events, childEvents),
    [events, childEvents]
  );
  const index = useMemo(() => indexRecords(trajectory), [trajectory]);
  const turns = useMemo(() => sectionTurns(trajectory), [trajectory]);
  const turnPositions = useMemo(() => {
    const positions = new Map<string, number>();
    turns.forEach((turn, position) => positions.set(turn.id, position));
    return positions;
  }, [turns]);
  const [query, setQuery] = useState("");
  const [selectedKey, setSelectedKey] = useState("");
  const [inspectorKey, setInspectorKey] = useState("");
  const [turnOverrides, setTurnOverrides] = useState<Record<string, boolean>>(
    {}
  );
  const [stepOverrides, setStepOverrides] = useState<Record<string, boolean>>(
    {}
  );
  const [callsOpen, setCallsOpen] = useState(true);
  const [openSubagents, setOpenSubagents] = useState<Record<string, boolean>>(
    {}
  );
  const [loadedSubagents, setLoadedSubagents] = useState<
    Record<string, boolean>
  >({});
  const [following, setFollowing] = useState(true);
  const ledgerRef = useRef<HTMLDivElement>(null);
  const rowRefs = useRef(new Map<string, HTMLTableRowElement>());
  const requestedChildrenRef = useRef(new Set<string>());
  const followingRef = useRef(true);
  const pendingScrollRef = useRef("");
  const searchText = query.trim().toLowerCase();

  useEffect(() => {
    followingRef.current = following;
  }, [following]);

  useEffect(() => {
    const node = ledgerRef.current;
    if (!node || !followingRef.current) {
      return;
    }
    node.scrollTop = node.scrollHeight;
  }, [trajectory]);

  useEffect(() => {
    if (!inspectorKey) {
      return undefined;
    }
    function handleKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        setInspectorKey("");
      }
    }
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  }, [inspectorKey]);

  useEffect(() => {
    const key = pendingScrollRef.current;
    if (!key) {
      return;
    }
    const node = rowRefs.current.get(key);
    if (!node) {
      return;
    }
    pendingScrollRef.current = "";
    if (typeof node.scrollIntoView === "function") {
      node.scrollIntoView({ block: "nearest" });
    }
  });

  const usageRecorded = turns.some((turn) => turn.usageRecorded);

  function isTurnExpanded(turn: TrajTurn, position: number): boolean {
    const override = turnOverrides[turn.id];
    if (override !== undefined) {
      return override;
    }
    return turns.length <= 10 || position >= turns.length - 3;
  }

  const allTurnsCollapsed =
    turns.length > 0 &&
    turns.every((turn, position) => !isTurnExpanded(turn, position));

  function toggleAllTurns() {
    const next: Record<string, boolean> = {};
    const expand = allTurnsCollapsed;
    for (const turn of turns) {
      next[turn.id] = expand;
    }
    setTurnOverrides(next);
  }

  function isStepExpanded(turn: TrajTurn, step: TrajStep): boolean {
    return stepOverrides[turn.id + "|" + step.no] ?? true;
  }

  function toggleStep(turn: TrajTurn, step: TrajStep) {
    const key = turn.id + "|" + step.no;
    setStepOverrides((current) => ({
      ...current,
      [key]: !(current[key] ?? true),
    }));
  }

  function openRecord(key: string) {
    setSelectedKey(key);
    setInspectorKey(key);
  }

  function closeInspector() {
    setInspectorKey("");
  }

  function moveSelection(key: string, delta: number) {
    const nodes = ledgerRef.current
      ? Array.from(
          ledgerRef.current.querySelectorAll<HTMLTableRowElement>(
            "tr[data-record-key]"
          )
        )
      : [];
    if (nodes.length === 0) {
      return;
    }
    const current = nodes.findIndex(
      (node) => node.getAttribute("data-record-key") === key
    );
    let target: number;
    if (current < 0) {
      target = delta > 0 ? 0 : nodes.length - 1;
    } else {
      target = Math.min(nodes.length - 1, Math.max(0, current + delta));
    }
    const next = nodes[target];
    if (!next) {
      return;
    }
    const nextKey = next.getAttribute("data-record-key") || "";
    setSelectedKey(nextKey);
    next.focus();
  }

  function onRowKeyDown(
    event: ReactKeyboardEvent<HTMLTableRowElement>,
    key: string
  ) {
    if (event.key === "Enter") {
      event.preventDefault();
      openRecord(key);
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      moveSelection(key, event.key === "ArrowDown" ? 1 : -1);
    }
  }

  function selectSpan(key: string) {
    const entry = index.get(key);
    const turn = entry?.turn;
    const step = entry?.step;
    const parent = entry?.parent;
    if (turn) {
      setTurnOverrides((current) => ({ ...current, [turn.id]: true }));
      if (step) {
        setStepOverrides((current) => ({
          ...current,
          [turn.id + "|" + step.no]: true,
        }));
      }
    }
    if (parent) {
      setCallsOpen(true);
      setOpenSubagents((current) => ({ ...current, [parent.key]: true }));
    }
    pendingScrollRef.current = key;
    openRecord(key);
  }

  function toggleSubagent(record: TrajRecord) {
    const next = !openSubagents[record.key];
    setOpenSubagents((current) => ({ ...current, [record.key]: next }));
    const childId = record.childSessionId;
    if (
      !next ||
      !childId ||
      (record.children && record.children.length > 0) ||
      requestedChildrenRef.current.has(childId)
    ) {
      return;
    }
    requestedChildrenRef.current.add(childId);
    if (onLoadChild) {
      Promise.resolve(onLoadChild(childId)).then(
        () => {
          setLoadedSubagents((current) => ({ ...current, [childId]: true }));
        },
        () => {
          setLoadedSubagents((current) => ({ ...current, [childId]: true }));
        }
      );
    } else {
      setLoadedSubagents((current) => ({ ...current, [childId]: true }));
    }
  }

  function handleLedgerScroll() {
    const node = ledgerRef.current;
    if (!node) {
      return;
    }
    setFollowing(
      node.scrollHeight - node.scrollTop - node.clientHeight < 24
    );
  }

  function followLatest() {
    setFollowing(true);
    const node = ledgerRef.current;
    if (node) {
      node.scrollTop = node.scrollHeight;
    }
  }

  function renderRecord(record: TrajRecord): ReactNode {
    const children = childrenFor(record, searchText);
    const isError =
      record.status === "error" ||
      record.status === "failed" ||
      record.status === "rejected";
    const selected = selectedKey === record.key;
    const status = statusText(record);
    const childSessionId = record.childSessionId;
    const hasChildren = (record.children?.length || 0) > 0;
    const subagentOpen =
      Boolean(openSubagents[record.key]) ||
      (searchText !== "" && hasChildren);
    const toolChildren =
      record.kind === "assistant" && (callsOpen || searchText !== "")
        ? children
        : [];
    const subtoolChildren =
      (record.kind === "tool" || record.kind === "subtool") && subagentOpen
        ? children
        : [];
    return (
      <Fragment key={record.key}>
        <tr
          ref={(node) => {
            if (node) {
              rowRefs.current.set(record.key, node);
            } else {
              rowRefs.current.delete(record.key);
            }
          }}
          className={
            "traj-row" +
            (selected ? " is-selected" : "") +
            (isError ? " is-error" : "") +
            (record.kind === "subtool" ? " is-subtool" : "")
          }
          data-record-key={record.key}
          tabIndex={0}
          aria-selected={selected}
          onClick={() => openRecord(record.key)}
          onKeyDown={(event) => onRowKeyDown(event, record.key)}
        >
          <td>
            {record.kind === "assistant" && record.requestNo !== undefined ? (
              <span className="trajectory-request">
                Request #{record.requestNo}
              </span>
            ) : null}
            <span className={"trajectory-kind trajectory-kind-" + record.kind}>
              {recordKindLabel(record)}
            </span>
          </td>
          <td className="traj-content">
            <span className="trajectory-title">{record.title}</span>
            {status ? (
              <span
                className={
                  "trajectory-status" +
                  (isError ? " is-error" : "") +
                  (record.status === "running" ? " is-running" : "")
                }
              >
                {status}
              </span>
            ) : null}
            {childSessionId ? (
              <span className="trajectory-subagent">
                <button
                  type="button"
                  className="trajectory-subagent-toggle"
                  aria-expanded={subagentOpen}
                  onClick={(event) => {
                    event.stopPropagation();
                    toggleSubagent(record);
                  }}
                >
                  {subagentOpen ? "Hide subtools" : "Show subtools"}
                </button>
                <a
                  className="trajectory-subagent-link"
                  href={
                    "#/sessions/" +
                    encodeURIComponent(childSessionId) +
                    "?view=trajectory"
                  }
                  onClick={(event) => event.stopPropagation()}
                >
                  Open child trajectory
                </a>
              </span>
            ) : null}
            {childSessionId && subagentOpen && children.length === 0 ? (
              <p className="trajectory-muted">
                {loadedSubagents[childSessionId]
                  ? "No subtool records."
                  : "Loading subtools…"}
              </p>
            ) : null}
          </td>
          <td className="traj-num">
            {record.usage ? (
              <>
                {tokenText(record.usage.input)}
                {record.usage.cached > 0 ? (
                  <span className="trajectory-muted">
                    {" "}
                    ({tokenText(record.usage.cached)} cached)
                  </span>
                ) : null}
              </>
            ) : (
              "—"
            )}
          </td>
          <td className="traj-num">
            {record.usage ? tokenText(record.usage.output) : "—"}
          </td>
          <td className="traj-num">
            {record.thinkChars !== undefined
              ? tokenText(record.thinkChars)
              : "—"}
          </td>
          <td className="traj-num">{durationText(record)}</td>
          <td>{record.model ? shortModelName(record.model) : "—"}</td>
          <td className="traj-num">
            {record.toolCalls !== undefined ? record.toolCalls : "—"}
          </td>
        </tr>
        {toolChildren.map((child) => renderRecord(child))}
        {subtoolChildren.map((child) => renderRecord(child))}
      </Fragment>
    );
  }

  function renderStep(turn: TrajTurn, step: TrajStep, records: TrajRecord[]) {
    const expanded = searchText !== "" ? true : isStepExpanded(turn, step);
    const provider = step.provider || "—";
    const model = step.model || "—";
    return (
      <Fragment key={turn.id + "|step|" + step.no}>
        <tr className="trajectory-step-row">
          <td colSpan={COLUMN_COUNT}>
            <button
              type="button"
              className="trajectory-disclosure trajectory-disclosure-step"
              aria-expanded={expanded}
              onClick={() => toggleStep(turn, step)}
            >
              <span className="trajectory-chevron">
                {expanded ? "▾" : "▸"}
              </span>
              <span>
                Step {step.no} · {provider}/{model}
              </span>
            </button>
          </td>
        </tr>
        {expanded ? records.map((record) => renderRecord(record)) : null}
      </Fragment>
    );
  }

  function renderTurn(turn: TrajTurn, position: number) {
    const loose = filterRecords(turn.loose, searchText);
    const steps = turn.steps
      .map((step) => ({
        step,
        records: filterRecords(step.records, searchText),
      }))
      .filter((entry) => entry.records.length > 0);
    if (searchText && loose.length === 0 && steps.length === 0) {
      return null;
    }
    const expanded = searchText !== "" ? true : isTurnExpanded(turn, position);
    const duration =
      turn.endMs !== undefined
        ? formatDuration(Math.max(0, turn.endMs - turn.startMs))
        : "running";
    return (
      <Fragment key={"turn-" + turn.id}>
        <tr className="trajectory-turn-row">
          <td colSpan={COLUMN_COUNT}>
            <button
              type="button"
              className="trajectory-disclosure"
              aria-expanded={expanded}
              onClick={() =>
                setTurnOverrides((current) => ({
                  ...current,
                  [turn.id]: !expanded,
                }))
              }
            >
              <span className="trajectory-chevron">
                {expanded ? "▾" : "▸"}
              </span>
              <span>Turn {turn.no}</span>
              <span className="trajectory-turn-meta">
                {formatClock(turn.startMs) || "—"} · {duration} ·{" "}
                {turnReason(turn)} · {turnUsageText(turn)}
              </span>
            </button>
          </td>
        </tr>
        {expanded
          ? loose.map((record) => renderRecord(record))
          : null}
        {expanded
          ? steps.map((entry) =>
              renderStep(turn, entry.step, entry.records)
            )
          : null}
      </Fragment>
    );
  }

  const selected = inspectorKey ? index.get(inspectorKey) : undefined;
  const selectedRecord = selected ? selected.record : null;

  return (
    <section
      className="trajectory"
      aria-label="Session trajectory"
      aria-busy={running}
      data-session-id={sessionId}
    >
      <TrajectoryToolbar
        totals={trajectory.totals}
        usageRecorded={usageRecorded}
        allTurnsCollapsed={allTurnsCollapsed}
        callsOpen={callsOpen}
        query={query}
        following={following}
        onToggleTurns={toggleAllTurns}
        onToggleCalls={() => setCallsOpen((current) => !current)}
        onQuery={setQuery}
        onFollow={followLatest}
      />
      <TrajectoryOverview
        trajectory={trajectory}
        selectedKey={selectedKey}
        onSelect={selectSpan}
      />
      <div className="trajectory-body">
        <div
          className="trajectory-ledger"
          ref={ledgerRef}
          onScroll={handleLedgerScroll}
          tabIndex={-1}
        >
          <table className="trajectory-table">
            <thead>
              <tr>
                <th scope="col">Kind</th>
                <th scope="col">Content</th>
                <th scope="col" className="traj-num">
                  Input
                </th>
                <th scope="col" className="traj-num">
                  Output
                </th>
                <th scope="col" className="traj-num">
                  Think
                </th>
                <th scope="col" className="traj-num">
                  Time
                </th>
                <th scope="col">Model</th>
                <th scope="col" className="traj-num">
                  Tools
                </th>
              </tr>
            </thead>
            <tbody>
              {trajectory.sections.map((section, position) => {
                if (section.type === "between") {
                  const records = filterRecords(section.records, searchText);
                  if (searchText && records.length === 0) {
                    return null;
                  }
                  return (
                    <Fragment key={"between-" + position}>
                      <tr className="trajectory-between-row">
                        <td colSpan={COLUMN_COUNT}>
                          <div className="trajectory-disclosure trajectory-disclosure-between">
                            Between turns
                          </div>
                        </td>
                      </tr>
                      {records.map((record) => renderRecord(record))}
                    </Fragment>
                  );
                }
                return renderTurn(
                  section.turn,
                  turnPositions.get(section.turn.id) ?? 0
                );
              })}
            </tbody>
          </table>
        </div>
        {selectedRecord ? (
          <RecordInspector
            key={selectedRecord.key}
            record={selectedRecord}
            onClose={closeInspector}
          />
        ) : null}
      </div>
    </section>
  );
}
