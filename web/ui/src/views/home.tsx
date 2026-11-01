import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { errorMessage } from "../api/client";
import { getDashboard } from "../api/dashboard";
import type {
  Dashboard,
  DashboardErrorRow,
  DashboardRange,
} from "../api/dashboard";
import "../styles/dashboard.css";

/* The monitoring dashboard (spec 28 T-002). Every number comes from recorded
   data: token usage, tools, turn outcomes, the error log, and what is running
   now. Plain and dense: numbers, one chart, tables, an error log. */

const RANGES: Array<{ value: DashboardRange; label: string }> = [
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
];

const DEFAULT_RANGE: DashboardRange = "24h";

const REFRESH_MS = 30000;

const DAEMON_AGENT_NOTE =
  "Daemon logs are not per agent, so they are hidden while an agent is selected.";

/* REQ-DSH-011: the range and agent live in the hash query
   (#/home?range=7d&agent=lana). App parses them into route params, but the
   page reads the same hash so a reload keeps the filter. */
function hashQuery(): URLSearchParams {
  if (typeof window === "undefined") {
    return new URLSearchParams();
  }
  const hash = window.location.hash || "";
  const queryIndex = hash.indexOf("?");
  if (queryIndex < 0) {
    return new URLSearchParams();
  }
  try {
    return new URLSearchParams(hash.slice(queryIndex + 1));
  } catch {
    return new URLSearchParams();
  }
}

function rangeFromHash(): DashboardRange {
  const value = hashQuery().get("range");
  const match = RANGES.find((option) => option.value === value);
  return match ? match.value : DEFAULT_RANGE;
}

function agentFromHash(): string {
  return hashQuery().get("agent") || "";
}

/* Write both choices back without adding a history entry. Default values stay
   out of the URL. */
function writeHash(nextRange: DashboardRange, nextAgent: string) {
  if (typeof window === "undefined") {
    return;
  }
  const query = new URLSearchParams();
  if (nextRange !== DEFAULT_RANGE) {
    query.set("range", nextRange);
  }
  if (nextAgent) {
    query.set("agent", nextAgent);
  }
  const suffix = query.toString();
  window.history.replaceState(
    null,
    "",
    "#/home" + (suffix ? "?" + suffix : "")
  );
}

const SOURCE_LABELS: Record<string, string> = {
  turn: "Turn",
  provider: "Provider",
  tool: "Tool",
  daemon: "Daemon",
};

const BREAKDOWN_SOURCE_LABELS: Record<string, string> = {
  turn: "Chat turns",
  compaction: "Compaction",
  memory_capture: "Memory capture",
};

const ERROR_FILTERS: Array<{ value: string; label: string }> = [
  { value: "all", label: "All" },
  { value: "turn", label: "Turn" },
  { value: "provider", label: "Provider" },
  { value: "tool", label: "Tool" },
  { value: "daemon", label: "Daemon" },
];

const MONTHS = [
  "Jan",
  "Feb",
  "Mar",
  "Apr",
  "May",
  "Jun",
  "Jul",
  "Aug",
  "Sep",
  "Oct",
  "Nov",
  "Dec",
];

function trimDecimal(value: number): string {
  const rounded = Math.round(value * 10) / 10;
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
}

/* Compact chart units: 12k, 1.2M. */
function compact(value: number): string {
  const number = Number(value) || 0;
  const abs = Math.abs(number);
  if (abs < 1000) {
    return String(Math.round(number));
  }
  if (abs < 1000000) {
    return trimDecimal(number / 1000) + "k";
  }
  if (abs < 1000000000) {
    return trimDecimal(number / 1000000) + "M";
  }
  return trimDecimal(number / 1000000000) + "B";
}

function count(value: number): string {
  return (Number(value) || 0).toLocaleString("en-US");
}

function share(part: number, whole: number): string {
  if (!whole || whole <= 0) {
    return "0%";
  }
  return Math.round((part / whole) * 100) + "%";
}

function pad2(value: number): string {
  return value < 10 ? "0" + value : String(value);
}

function clockTime(ms: number): string {
  const date = new Date(Number(ms) || 0);
  return pad2(date.getHours()) + ":" + pad2(date.getMinutes());
}

function bucketLabel(ms: number, bucketMs: number): string {
  const date = new Date(Number(ms) || 0);
  if (bucketMs < 86400000) {
    return pad2(date.getHours()) + ":" + pad2(date.getMinutes());
  }
  return MONTHS[date.getMonth()] + " " + date.getDate();
}

function duration(ms: number): string {
  const value = Number(ms) || 0;
  if (value < 1000) {
    return Math.round(value) + " ms";
  }
  if (value < 60000) {
    return trimDecimal(value / 1000) + " s";
  }
  return trimDecimal(value / 60000) + " min";
}

function niceStep(raw: number): number {
  if (!(raw > 0)) {
    return 1;
  }
  const base = Math.pow(10, Math.floor(Math.log10(raw)));
  const fraction = raw / base;
  let nice = 10;
  if (fraction <= 1) {
    nice = 1;
  } else if (fraction <= 2) {
    nice = 2;
  } else if (fraction <= 2.5) {
    nice = 2.5;
  } else if (fraction <= 5) {
    nice = 5;
  }
  return nice * base;
}

/* Four y ticks: 0 and three clean steps that clear the tallest bucket. */
function axisTicks(max: number): number[] {
  const raw = max > 0 ? max : 1;
  const step = niceStep(raw / 3);
  return [0, step, step * 2, step * 3];
}

const CHART = { width: 960, height: 240, left: 56, right: 16, top: 12, bottom: 30 };

interface ChartRow {
  start: number;
  input: number;
  cached: number;
  output: number;
  total: number;
}

/* REQ-DSH-004: a stacked bar chart of input (not cached), cached input, and
   output tokens per bucket, with a tooltip and an accessible table. */
function TokenChart({ data }: { data: Dashboard }) {
  const [hover, setHover] = useState<number | null>(null);
  const bucketMs = data.bucket_ms || 3600000;

  const rows = useMemo<ChartRow[]>(
    () =>
      (data.series || []).map((point) => {
        const cached = Math.max(0, point.cached_input_tokens || 0);
        const input = Math.max(0, (point.input_tokens || 0) - cached);
        const output = Math.max(0, point.output_tokens || 0);
        return {
          start: point.start,
          input,
          cached,
          output,
          total: input + cached + output,
        };
      }),
    [data.series]
  );

  const totalTokens = rows.reduce((sum, row) => sum + row.total, 0);

  if (rows.length === 0 || totalTokens === 0) {
    return <p className="dash-empty">No token usage in this range.</p>;
  }

  const ticks = axisTicks(rows.reduce((max, row) => Math.max(max, row.total), 0));
  const top = ticks[ticks.length - 1] || 1;
  const plotW = CHART.width - CHART.left - CHART.right;
  const plotH = CHART.height - CHART.top - CHART.bottom;
  const slot = plotW / rows.length;
  const barW = Math.max(1, slot * 0.68);
  const labelEvery = Math.max(1, Math.ceil(rows.length / 6));
  const hovered = hover !== null ? rows[hover] : null;

  function yOf(value: number): number {
    return CHART.top + plotH - (value / top) * plotH;
  }

  return (
    <div className="dash-chart">
      <div className="dash-chart-plot">
        <svg
          className="dash-chart-svg"
          viewBox={"0 0 " + CHART.width + " " + CHART.height}
          role="img"
          aria-label={
            "Token usage over time, " +
            count(totalTokens) +
            " tokens across " +
            rows.length +
            " buckets. A table of the same values follows."
          }
        >
          {ticks.map((tick) => (
            <g key={tick}>
              <line
                className="dash-chart-grid"
                x1={CHART.left}
                x2={CHART.width - CHART.right}
                y1={yOf(tick)}
                y2={yOf(tick)}
              />
              <text
                className="dash-chart-ylabel"
                x={CHART.left - 8}
                y={yOf(tick) + 4}
                textAnchor="end"
              >
                {compact(tick)}
              </text>
            </g>
          ))}

          {rows.map((row, index) => {
            const x = CHART.left + slot * index + (slot - barW) / 2;
            const base = CHART.top + plotH;
            const inputH = (row.input / top) * plotH;
            const cachedH = (row.cached / top) * plotH;
            const outputH = (row.output / top) * plotH;
            return (
              <g key={row.start}>
                <rect
                  className="dash-bar dash-bar-input"
                  x={x}
                  y={base - inputH}
                  width={barW}
                  height={inputH}
                />
                <rect
                  className="dash-bar dash-bar-cached"
                  x={x}
                  y={base - inputH - cachedH}
                  width={barW}
                  height={cachedH}
                />
                <rect
                  className="dash-bar dash-bar-output"
                  x={x}
                  y={base - inputH - cachedH - outputH}
                  width={barW}
                  height={outputH}
                />
                <rect
                  className="dash-bar-hit"
                  x={CHART.left + slot * index}
                  y={CHART.top}
                  width={slot}
                  height={plotH}
                  onMouseEnter={() => setHover(index)}
                  onMouseLeave={() => setHover(null)}
                />
                {index % labelEvery === 0 ? (
                  <text
                    className="dash-chart-xlabel"
                    x={CHART.left + slot * index + slot / 2}
                    y={CHART.height - 10}
                    textAnchor="middle"
                  >
                    {bucketLabel(row.start, bucketMs)}
                  </text>
                ) : null}
              </g>
            );
          })}
        </svg>

        {hovered && hover !== null ? (
          <div
            className="dash-chart-tooltip"
            style={{
              left:
                ((CHART.left + slot * hover + slot / 2) / CHART.width) * 100 +
                "%",
            }}
          >
            <div className="dash-chart-tooltip-time">
              {bucketLabel(hovered.start, bucketMs)}
            </div>
            <dl className="dash-chart-tooltip-list">
              <dt>Input</dt>
              <dd>{count(hovered.input)}</dd>
              <dt>Cached</dt>
              <dd>{count(hovered.cached)}</dd>
              <dt>Output</dt>
              <dd>{count(hovered.output)}</dd>
              <dt>Total</dt>
              <dd>{count(hovered.total)}</dd>
            </dl>
          </div>
        ) : null}
      </div>

      <div className="dash-chart-legend">
        <span className="dash-legend-item">
          <span className="dash-swatch dash-swatch-input" aria-hidden="true" />
          Input (not cached)
        </span>
        <span className="dash-legend-item">
          <span className="dash-swatch dash-swatch-cached" aria-hidden="true" />
          Cached input
        </span>
        <span className="dash-legend-item">
          <span className="dash-swatch dash-swatch-output" aria-hidden="true" />
          Output
        </span>
      </div>

      <details className="dash-chart-table">
        <summary>Show as table</summary>
        <div className="dash-table-wrap">
          <table className="dash-table">
            <caption className="sr-only">
              Token usage per bucket in this range
            </caption>
            <thead>
              <tr>
                <th scope="col">Bucket</th>
                <th scope="col" className="is-num">
                  Input
                </th>
                <th scope="col" className="is-num">
                  Cached
                </th>
                <th scope="col" className="is-num">
                  Output
                </th>
                <th scope="col" className="is-num">
                  Total
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.start}>
                  <td>{bucketLabel(row.start, bucketMs)}</td>
                  <td className="is-num">{count(row.input)}</td>
                  <td className="is-num">{count(row.cached)}</td>
                  <td className="is-num">{count(row.output)}</td>
                  <td className="is-num">{count(row.total)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}

function StatTile({
  label,
  value,
  sub,
}: {
  label: string;
  value: string;
  sub: string;
}) {
  return (
    <div className="dash-tile">
      <div className="dash-tile-label">{label}</div>
      <div className="dash-tile-value">{value}</div>
      <div className="dash-tile-sub">{sub}</div>
    </div>
  );
}

function Section({
  title,
  note,
  children,
}: {
  title: string;
  note?: string;
  children: ReactNode;
}) {
  return (
    <section className="dash-section">
      <div className="dash-section-head">
        <h2 className="dash-section-title">{title}</h2>
        {note ? <p className="dash-note">{note}</p> : null}
      </div>
      {children}
    </section>
  );
}

function ErrorLogRow({
  row,
  open,
  onToggle,
}: {
  row: DashboardErrorRow;
  open: boolean;
  onToggle: () => void;
}) {
  const sessionId = row.session_id || "";
  const href = sessionId
    ? "#/sessions/" + encodeURIComponent(sessionId) + "?view=trajectory"
    : "";
  return (
    <li className="dash-error-row">
      <div className="dash-error-head">
        <span className="dash-error-time">{clockTime(row.at)}</span>
        <span className={"dash-error-source is-" + (row.source || "unknown")}>
          {SOURCE_LABELS[row.source] || row.source || "Other"}
        </span>
        <span className="dash-error-agent">{row.agent || "—"}</span>
        <button
          type="button"
          className={"dash-error-message" + (open ? " is-open" : "")}
          aria-expanded={open}
          onClick={onToggle}
        >
          {row.message || "(no message)"}
        </button>
        {href ? (
          <a className="dash-error-link" href={href}>
            Trajectory
          </a>
        ) : null}
      </div>
      {open ? (
        <dl className="dash-error-detail">
          <dt>Level</dt>
          <dd>{row.level || "error"}</dd>
          <dt>Source</dt>
          <dd>{SOURCE_LABELS[row.source] || row.source || "Other"}</dd>
          {row.agent ? (
            <>
              <dt>Agent</dt>
              <dd>{row.agent}</dd>
            </>
          ) : null}
          {row.turn_id ? (
            <>
              <dt>Turn</dt>
              <dd>{row.turn_id}</dd>
            </>
          ) : null}
          {row.tool ? (
            <>
              <dt>Tool</dt>
              <dd>{row.tool}</dd>
            </>
          ) : null}
          {sessionId ? (
            <>
              <dt>Session</dt>
              <dd>{sessionId}</dd>
            </>
          ) : null}
        </dl>
      ) : null}
    </li>
  );
}

/* Stable identity for an error row across refreshes: its time, source, session,
   and its position among rows sharing the same millisecond. A plain index would
   point at a different row after a refresh and expand the wrong one. */
function errorKey(row: DashboardErrorRow, indexInSameMs: number): string {
  return (
    String(row.at) +
    "|" +
    (row.source || "") +
    "|" +
    (row.session_id || "") +
    "|" +
    (row.tool || "") +
    "|" +
    (row.turn_id || "") +
    "|" +
    String(indexInSameMs)
  );
}

export function HomePage() {
  const [range, setRange] = useState<DashboardRange>(rangeFromHash);
  const [agent, setAgent] = useState<string>(agentFromHash);
  const [data, setData] = useState<Dashboard | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [updatedAt, setUpdatedAt] = useState(0);
  const [errorFilter, setErrorFilter] = useState("all");
  const [openErrors, setOpenErrors] = useState<string[]>([]);

  /* Only the latest request may apply its result or error: a range or agent
     switch, or the refresh timer, can put two requests in flight, and the
     older one can arrive last (REQ-DSH-001). */
  const requestId = useRef(0);

  /* The first state update happens after the await, so the effect below does
     not set state synchronously while it renders. */
  const load = useCallback(
    async (nextRange: DashboardRange, nextAgent: string) => {
      const id = ++requestId.current;
      try {
        /* An absent agent keeps the one-argument call and query. */
        const result = nextAgent
          ? await getDashboard(nextRange, nextAgent)
          : await getDashboard(nextRange);
        if (id !== requestId.current) {
          return;
        }
        setData(result);
        setError("");
        setUpdatedAt(Date.now());
      } catch (failure) {
        if (id !== requestId.current) {
          return;
        }
        /* REQ-DSH-010: keep the last data and show the notice. */
        setError(errorMessage(failure));
      } finally {
        if (id === requestId.current) {
          setLoading(false);
        }
      }
    },
    []
  );

  function reload() {
    setLoading(true);
    void load(range, agent);
  }

  useEffect(() => {
    /* Fetching on mount and on a range or agent change matches the other
       views. */
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load(range, agent);
  }, [load, range, agent]);

  /* REQ-DSH-001: refresh every 30 s, but only while the tab is visible. The
     selected agent is kept. */
  useEffect(() => {
    const timer = setInterval(() => {
      if (document.visibilityState === "visible") {
        void load(range, agent);
      }
    }, REFRESH_MS);
    return () => clearInterval(timer);
  }, [load, range, agent]);

  function toggleError(key: string) {
    setOpenErrors((current) =>
      current.includes(key)
        ? current.filter((value) => value !== key)
        : current.concat(key)
    );
  }

  /* Keep the chosen agent as an option even when the latest response no
     longer lists it, so the select never jumps to another value. */
  const agentOptions = useMemo(() => {
    const names = new Set(data?.agents || []);
    if (agent) {
      names.add(agent);
    }
    return Array.from(names).sort((a, b) => a.localeCompare(b));
  }, [data, agent]);

  function changeAgent(next: string) {
    if (next === agent) {
      return;
    }
    writeHash(range, next);
    setLoading(true);
    setAgent(next);
    /* Daemon rows are hidden with a filter, so that chip cannot stay active. */
    if (next && errorFilter === "daemon") {
      setErrorFilter("all");
    }
  }

  const summary = data?.summary;
  const now = data?.now;

  const modelRows = data?.by_model || [];
  const agentRows = data?.by_agent || [];
  const sourceRows = data?.by_source || [];
  const toolRows = useMemo(
    () =>
      [...(data?.tools || [])].sort(
        (a, b) => b.calls - a.calls || a.name.localeCompare(b.name)
      ),
    [data]
  );
  const reasonRows = data?.turn_reasons || [];
  const visibleErrors = useMemo(() => {
    const seen = new Map<number, number>();
    return (data?.errors || [])
      .map((row) => {
        const indexInSameMs = seen.get(row.at) || 0;
        seen.set(row.at, indexInSameMs + 1);
        return { row, key: errorKey(row, indexInSameMs) };
      })
      .filter(({ row }) => errorFilter === "all" || row.source === errorFilter);
  }, [data, errorFilter]);

  const tokensTotal = summary
    ? (summary.input_tokens || 0) + (summary.output_tokens || 0)
    : 0;

  return (
    <main id="main" className="page dash-page">
      <header className="dash-head">
        <h1 className="dash-title">Dashboard</h1>
        <div className="dash-controls">
          <div className="dash-range" role="group" aria-label="Time range">
            {RANGES.map((option) => (
              <button
                key={option.value}
                type="button"
                className={
                  "dash-range-button" + (range === option.value ? " is-active" : "")
                }
                aria-pressed={range === option.value}
                onClick={() => {
                  if (option.value === range) {
                    return;
                  }
                  writeHash(option.value, agent);
                  setLoading(true);
                  setRange(option.value);
                }}
              >
                {option.label}
              </button>
            ))}
          </div>
          <div className="dash-agent">
            <label className="dash-agent-label" htmlFor="dash-agent-select">
              Agent
            </label>
            <select
              id="dash-agent-select"
              className="dash-agent-select"
              value={agent}
              onChange={(event) => changeAgent(event.target.value)}
            >
              <option value="">All agents</option>
              {agentOptions.map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))}
            </select>
          </div>
          <span className="dash-updated" aria-live="polite">
            {updatedAt ? "Updated " + clockTime(updatedAt) : "Loading"}
          </span>
          <button
            type="button"
            className="button-ui button-ui-secondary dash-refresh"
            onClick={reload}
            disabled={loading}
          >
            Refresh
          </button>
        </div>
      </header>

      {error ? (
        <div className="error-box dash-error-notice" role="alert">
          <p className="error-message">{error}</p>
          <button
            type="button"
            className="button-ui button-ui-secondary"
            onClick={reload}
          >
            Retry
          </button>
        </div>
      ) : null}

      {!data ? (
        <p className="dash-empty">
          {error ? "No data yet." : "Loading the dashboard…"}
        </p>
      ) : (
        <>
          <section className="dash-section">
            <div className="dash-section-head">
              <h2 className="dash-section-title">Right now</h2>
            </div>
            <div className="dash-now">
              <a className="dash-now-item" href="#/sessions">
                <span className="dash-now-count">
                  {count(now?.sessions_running || 0)}
                </span>
                Running
              </a>
              <a className="dash-now-item" href="#/sessions">
                <span className="dash-now-count">
                  {count(now?.sessions_waiting_input || 0)}
                </span>
                Waiting for answer
              </a>
            </div>
          </section>

          <div className="dash-tiles">
            <StatTile
              label="Tokens"
              value={count(tokensTotal)}
              sub={
                "Cached " +
                share(summary?.cached_input_tokens || 0, summary?.input_tokens || 0) +
                " of input"
              }
            />
            <StatTile
              label="Turns"
              value={count(summary?.turns || 0)}
              sub={
                "Failed " +
                count(summary?.turns_failed || 0) +
                " · " +
                share(summary?.turns_failed || 0, summary?.turns || 0)
              }
            />
            <StatTile
              label="Tool calls"
              value={count(summary?.tool_calls || 0)}
              sub={
                "Errors " +
                count(summary?.tool_errors || 0) +
                " · " +
                share(summary?.tool_errors || 0, summary?.tool_calls || 0)
              }
            />
            <StatTile
              label="Provider retries"
              value={count(summary?.provider_retries || 0)}
              sub="Retried model attempts"
            />
          </div>

          <Section title="Token usage">
            <TokenChart data={data} />
          </Section>

          <div className="dash-columns">
            <Section title="By model">
              {modelRows.length === 0 ? (
                <p className="dash-empty">No model usage in this range.</p>
              ) : (
                <div className="dash-table-wrap">
                  <table className="dash-table">
                    <thead>
                      <tr>
                        <th scope="col">Provider</th>
                        <th scope="col">Model</th>
                        <th scope="col" className="is-num">
                          Requests
                        </th>
                        <th scope="col" className="is-num">
                          Input
                        </th>
                        <th scope="col" className="is-num">
                          Cached
                        </th>
                        <th scope="col" className="is-num">
                          Output
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {modelRows.map((row) => (
                        <tr key={row.provider + "/" + row.model}>
                          <td>{row.provider}</td>
                          <td>{row.model}</td>
                          <td className="is-num">{count(row.requests)}</td>
                          <td className="is-num">{count(row.input_tokens)}</td>
                          <td className="is-num">
                            {count(row.cached_input_tokens)}
                          </td>
                          <td className="is-num">{count(row.output_tokens)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </Section>

            <Section title="By agent">
              {agentRows.length === 0 ? (
                <p className="dash-empty">No agent activity in this range.</p>
              ) : (
                <div className="dash-table-wrap">
                  <table className="dash-table">
                    <thead>
                      <tr>
                        <th scope="col">Agent</th>
                        <th scope="col" className="is-num">
                          Turns
                        </th>
                        <th scope="col" className="is-num">
                          Input
                        </th>
                        <th scope="col" className="is-num">
                          Cached
                        </th>
                        <th scope="col" className="is-num">
                          Output
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {agentRows.map((row) => (
                        <tr key={row.agent}>
                          <td>{row.agent}</td>
                          <td className="is-num">{count(row.turns)}</td>
                          <td className="is-num">{count(row.input_tokens)}</td>
                          <td className="is-num">
                            {count(row.cached_input_tokens)}
                          </td>
                          <td className="is-num">{count(row.output_tokens)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </Section>
          </div>

          <Section title="By source">
            {sourceRows.length === 0 ||
            sourceRows.every((row) => !row.count) ? (
              <p className="dash-empty">No token usage by source in this range.</p>
            ) : (
              <div className="dash-table-wrap">
                <table className="dash-table">
                  <thead>
                    <tr>
                      <th scope="col">Source</th>
                      <th scope="col" className="is-num">
                        Count
                      </th>
                      <th scope="col" className="is-num">
                        Input
                      </th>
                      <th scope="col" className="is-num">
                        Cached
                      </th>
                      <th scope="col" className="is-num">
                        Output
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {sourceRows.map((row) => (
                      <tr key={row.source}>
                        <td>
                          {BREAKDOWN_SOURCE_LABELS[row.source] || row.source}
                        </td>
                        <td className="is-num">{count(row.count)}</td>
                        <td className="is-num">{count(row.input_tokens)}</td>
                        <td className="is-num">
                          {count(row.cached_input_tokens)}
                        </td>
                        <td className="is-num">{count(row.output_tokens)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Section>

          <Section title="Tools">
            {toolRows.length === 0 ? (
              <p className="dash-empty">No tool calls in this range.</p>
            ) : (
              <div className="dash-table-wrap">
                <table className="dash-table dash-tools-table">
                  <thead>
                    <tr>
                      <th scope="col">Tool</th>
                      <th scope="col" className="is-num">
                        Calls
                      </th>
                      <th scope="col" className="is-num">
                        Errors
                      </th>
                      <th scope="col" className="is-num">
                        Rejected
                      </th>
                      <th scope="col" className="is-num">
                        Error share
                      </th>
                      <th scope="col" className="is-num">
                        Avg time
                      </th>
                      <th scope="col" className="is-num">
                        p95 time
                      </th>
                      <th scope="col">Last error</th>
                    </tr>
                  </thead>
                  <tbody>
                    {toolRows.map((row) => {
                      const marked = row.calls >= 5 && row.errors / row.calls > 0.2;
                      return (
                        <tr
                          key={row.name}
                          className={marked ? "is-danger" : undefined}
                        >
                          <td>{row.name}</td>
                          <td className="is-num">{count(row.calls)}</td>
                          <td className="is-num">{count(row.errors)}</td>
                          <td className="is-num">{count(row.rejected)}</td>
                          <td className="is-num">
                            {share(row.errors, row.calls)}
                          </td>
                          <td className="is-num">{duration(row.avg_ms)}</td>
                          <td className="is-num">{duration(row.p95_ms)}</td>
                          <td className="dash-last-error">
                            {row.last_error ? (
                              <>
                                <span className="dash-last-error-text">
                                  {row.last_error}
                                </span>
                                <span className="dash-last-error-time">
                                  {clockTime(row.last_error_at)}
                                </span>
                              </>
                            ) : (
                              <span className="dash-muted">—</span>
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </Section>

          <Section title="Turn outcomes">
            {reasonRows.length === 0 ? (
              <p className="dash-empty">No turns in this range.</p>
            ) : (
              <>
                <p className="dash-note">
                  Average turn time {duration(data.turn_duration?.avg_ms || 0)} ·
                  p95 {duration(data.turn_duration?.p95_ms || 0)}
                </p>
                <div className="dash-table-wrap">
                  <table className="dash-table">
                    <thead>
                      <tr>
                        <th scope="col">Outcome</th>
                        <th scope="col" className="is-num">
                          Count
                        </th>
                        <th scope="col" className="is-num">
                          Share
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {reasonRows.map((row) => (
                        <tr key={row.reason}>
                          <td>{row.reason || "end_turn"}</td>
                          <td className="is-num">{count(row.count)}</td>
                          <td className="is-num">
                            {share(row.count, summary?.turns || 0)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </>
            )}
          </Section>

          <Section title="Error log">
            <div className="dash-chips" role="group" aria-label="Error source">
              {ERROR_FILTERS.map((filter) => {
                const daemonHidden = filter.value === "daemon" && agent !== "";
                return (
                  <button
                    key={filter.value}
                    type="button"
                    className={
                      "dash-chip" +
                      (errorFilter === filter.value ? " is-active" : "")
                    }
                    aria-pressed={errorFilter === filter.value}
                    disabled={daemonHidden}
                    title={daemonHidden ? DAEMON_AGENT_NOTE : undefined}
                    onClick={() => setErrorFilter(filter.value)}
                  >
                    {filter.label}
                  </button>
                );
              })}
            </div>
            {agent ? (
              <p className="dash-note">{DAEMON_AGENT_NOTE}</p>
            ) : (
              <p className="dash-note">
                Daemon rows cover the time since the daemon last started
                {data.daemon_started_at
                  ? " (" + clockTime(data.daemon_started_at) + ")"
                  : ""}
                .
              </p>
            )}
            {visibleErrors.length === 0 ? (
              <p className="dash-empty">No errors in this range.</p>
            ) : (
              <ul className="dash-errors" aria-label="Error log">
                {visibleErrors.map(({ row, key }) => (
                  <ErrorLogRow
                    key={key}
                    row={row}
                    open={openErrors.includes(key)}
                    onToggle={() => toggleError(key)}
                  />
                ))}
              </ul>
            )}
          </Section>
        </>
      )}
    </main>
  );
}
