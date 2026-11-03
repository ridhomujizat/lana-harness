import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { errorMessage } from "../api/client";
import { getDashboard } from "../api/dashboard";
import type {
  Dashboard,
  DashboardErrorRow,
  DashboardRange,
} from "../api/dashboard";
import {
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Panel,
  Seg,
  Select,
  Stackbar,
  Stat,
} from "../ui/gr8r";
import type { BadgeTone } from "../ui/gr8r";
import { Ic } from "../ui/icons";

/* The monitoring dashboard (spec 28 T-002). Every number comes from recorded
   data: token usage, tools, turn outcomes, the error log, and what is running
   now. Rendered with the Gr8r home markup: a greeting header, a KPI strip, and
   a two-column panel grid. */

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

const SOURCE_TONES: Record<string, BadgeTone> = {
  turn: "accent",
  provider: "amber",
  tool: "red",
  daemon: "gray",
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

const WEEKDAYS = [
  "Sunday",
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
];

function greeting(): string {
  const hour = new Date().getHours();
  if (hour < 12) {
    return "Good morning";
  }
  if (hour < 18) {
    return "Good afternoon";
  }
  return "Good evening";
}

function todayLabel(): string {
  const date = new Date();
  return (
    WEEKDAYS[date.getDay()] + ", " + MONTHS[date.getMonth()] + " " + date.getDate()
  );
}

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

interface ChartRow {
  start: number;
  input: number;
  cached: number;
  output: number;
  total: number;
}

/* REQ-DSH-004: token usage per bucket as a stacked Gr8r bar (input that was not
   cached, cached input, and output), with a legend and an accessible table of
   the same values. */
function TokenChart({ data }: { data: Dashboard }) {
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
    return (
      <EmptyState
        size="sm"
        icon="chart-column"
        text="No token usage in this range."
      />
    );
  }

  const max = rows.reduce((peak, row) => Math.max(peak, row.total), 0) || 1;

  return (
    <div className="home-chart">
      <div
        className="home-chart-plot"
        role="img"
        aria-label={
          "Token usage over time, " +
          count(totalTokens) +
          " tokens across " +
          rows.length +
          " buckets. A table of the same values follows."
        }
      >
        {rows.map((row) => (
          <div className="home-chart-row" key={row.start}>
            <span className="home-chart-label num">
              {bucketLabel(row.start, bucketMs)}
            </span>
            <span className="home-chart-bar">
              <Stackbar
                height={10}
                parts={[
                  {
                    value: row.input,
                    color: "var(--accent)",
                    label: "Input not cached " + count(row.input),
                  },
                  {
                    value: row.cached,
                    color: "var(--amber)",
                    label: "Cached input " + count(row.cached),
                  },
                  {
                    value: row.output,
                    color: "var(--green)",
                    label: "Output " + count(row.output),
                  },
                  { value: max - row.total, color: "transparent" },
                ]}
              />
            </span>
            <span className="home-chart-total num">{compact(row.total)}</span>
          </div>
        ))}
      </div>

      <div className="home-chart-legend">
        <span className="home-legend-item">
          <span className="home-swatch home-swatch-input" aria-hidden="true" />
          Input (not cached)
        </span>
        <span className="home-legend-item">
          <span className="home-swatch home-swatch-cached" aria-hidden="true" />
          Cached input
        </span>
        <span className="home-legend-item">
          <span className="home-swatch home-swatch-output" aria-hidden="true" />
          Output
        </span>
      </div>

      <details className="home-chart-table">
        <summary>Show as table</summary>
        <div className="home-table-wrap">
          <table className="perm-t home-table">
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
                  <td className="is-num num">{count(row.input)}</td>
                  <td className="is-num num">{count(row.cached)}</td>
                  <td className="is-num num">{count(row.output)}</td>
                  <td className="is-num num">{count(row.total)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
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
  const source = row.source || "";
  return (
    <li className="home-error-row">
      <div className="home-error-head">
        <span className="home-error-time num">{clockTime(row.at)}</span>
        <Badge tone={SOURCE_TONES[source] || "gray"} dot>
          {SOURCE_LABELS[source] || source || "Other"}
        </Badge>
        <span className="home-error-agent trunc">{row.agent || "—"}</span>
        <button
          type="button"
          className={"home-error-message" + (open ? " is-open" : "")}
          aria-expanded={open}
          onClick={onToggle}
        >
          {row.message || "(no message)"}
        </button>
        {href ? (
          <a className="home-error-link" href={href}>
            Trajectory
          </a>
        ) : null}
      </div>
      {open ? (
        <dl className="home-error-detail">
          <dt>Level</dt>
          <dd>{row.level || "error"}</dd>
          <dt>Source</dt>
          <dd>{SOURCE_LABELS[source] || source || "Other"}</dd>
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
  const cachedDetail =
    "Cached " +
    share(summary?.cached_input_tokens || 0, summary?.input_tokens || 0) +
    " of input";
  const turnsDetail =
    "Failed " +
    count(summary?.turns_failed || 0) +
    " · " +
    share(summary?.turns_failed || 0, summary?.turns || 0);
  const toolsDetail =
    "Errors " +
    count(summary?.tool_errors || 0) +
    " · " +
    share(summary?.tool_errors || 0, summary?.tool_calls || 0);

  return (
    <main id="main" className="page home-page">
      <header className="ph">
        <div>
          <h1>
            {greeting()}, Lana
          </h1>
          <p>
            <span className="num">{todayLabel()}</span> · Here's what's
            happening across your workspace.
          </p>
        </div>
        <div className="acts">
          <Seg
            className="home-range"
            aria-label="Time range"
            options={RANGES.map((option) => ({
              value: option.value,
              label: option.label,
            }))}
            value={range}
            onChange={(next) => {
              if (next === range) {
                return;
              }
              writeHash(next as DashboardRange, agent);
              setLoading(true);
              setRange(next as DashboardRange);
            }}
          />
          <div className="home-agent">
            <label className="home-agent-label" htmlFor="home-agent-select">
              Agent
            </label>
            <Select
              id="home-agent-select"
              placeholder="All agents"
              options={[
                { value: "", label: "All agents" },
                ...agentOptions.map((name) => ({ value: name, label: name })),
              ]}
              value={agent}
              onChange={changeAgent}
            />
          </div>
          <span className="home-updated num" aria-live="polite">
            {updatedAt ? "Updated " + clockTime(updatedAt) : "Loading"}
          </span>
          <Button
            as="a"
            href="#/sessions/new"
            variant="primary"
            icon="plus"
            size="sm"
          >
            New chat
          </Button>
          <Button
            variant="secondary"
            icon="refresh-cw"
            size="sm"
            onClick={reload}
            disabled={loading}
          >
            Refresh
          </Button>
        </div>
      </header>

      {error ? (
        <div className="home-alert" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not refresh"
            text={error}
            action={
              <Button variant="secondary" icon="refresh-cw" onClick={reload}>
                Retry
              </Button>
            }
          />
        </div>
      ) : null}

      {!data ? (
        <div className="home-empty">
          {error ? "No data yet." : "Loading the dashboard…"}
        </div>
      ) : (
        <>
          <div className="stats home-stats">
            <Stat
              icon="coins"
              label="Tokens"
              value={count(tokensTotal)}
              detail={cachedDetail}
            />
            <Stat
              icon="activity"
              label="Turns"
              value={count(summary?.turns || 0)}
              detail={turnsDetail}
              tone={summary?.turns_failed ? "bad" : "up"}
            />
            <Stat
              icon="wrench"
              label="Tool calls"
              value={count(summary?.tool_calls || 0)}
              detail={toolsDetail}
              tone={summary?.tool_errors ? "bad" : "up"}
            />
            <Stat
              icon="repeat"
              label="Provider retries"
              value={count(summary?.provider_retries || 0)}
              detail="Retried model attempts"
            />
          </div>

          <div className="grid2 home-grid">
            <div className="stack">
              <Panel title="Token usage">
                <TokenChart data={data} />
              </Panel>

              <Panel title="Tools">
                {toolRows.length === 0 ? (
                  <EmptyState
                    size="sm"
                    icon="wrench"
                    text="No tool calls in this range."
                  />
                ) : (
                  <div className="home-table-wrap">
                    <table className="perm-t home-table home-tools-table">
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
                          const marked =
                            row.calls >= 5 && row.errors / row.calls > 0.2;
                          return (
                            <tr
                              key={row.name}
                              className={marked ? "is-danger" : undefined}
                            >
                              <td>{row.name}</td>
                              <td className="is-num num">{count(row.calls)}</td>
                              <td className="is-num num">{count(row.errors)}</td>
                              <td className="is-num num">
                                {count(row.rejected)}
                              </td>
                              <td className="is-num num">
                                {share(row.errors, row.calls)}
                              </td>
                              <td className="is-num num">
                                {duration(row.avg_ms)}
                              </td>
                              <td className="is-num num">
                                {duration(row.p95_ms)}
                              </td>
                              <td className="home-last-error">
                                {row.last_error ? (
                                  <>
                                    <span className="home-last-error-text">
                                      {row.last_error}
                                    </span>
                                    <span className="home-last-error-time num">
                                      {clockTime(row.last_error_at)}
                                    </span>
                                  </>
                                ) : (
                                  <span className="faint">—</span>
                                )}
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
              </Panel>

              <Panel title="Turn outcomes">
                {reasonRows.length === 0 ? (
                  <EmptyState
                    size="sm"
                    icon="circle-dot"
                    text="No turns in this range."
                  />
                ) : (
                  <>
                    <p className="home-note">
                      Average turn time{" "}
                      {duration(data.turn_duration?.avg_ms || 0)} · p95{" "}
                      {duration(data.turn_duration?.p95_ms || 0)}
                    </p>
                    <div className="home-table-wrap">
                      <table className="perm-t home-table">
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
                              <td className="is-num num">{count(row.count)}</td>
                              <td className="is-num num">
                                {share(row.count, summary?.turns || 0)}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </>
                )}
              </Panel>

              <Panel
                title="Error log"
                actions={
                  <Seg
                    className="home-err-filter"
                    aria-label="Error source"
                    options={ERROR_FILTERS.map((filter) => {
                      const daemonHidden =
                        filter.value === "daemon" && agent !== "";
                      return {
                        value: filter.value,
                        label: filter.label,
                        disabled: daemonHidden,
                        title: daemonHidden ? DAEMON_AGENT_NOTE : undefined,
                      };
                    })}
                    value={errorFilter}
                    onChange={(next) => setErrorFilter(next)}
                  />
                }
              >
                <p className="home-note">
                  {agent ? (
                    DAEMON_AGENT_NOTE
                  ) : (
                    <>
                      Daemon rows cover the time since the daemon last started
                      {data.daemon_started_at
                        ? " (" + clockTime(data.daemon_started_at) + ")"
                        : ""}
                      .
                    </>
                  )}
                </p>
                {visibleErrors.length === 0 ? (
                  <EmptyState
                    size="sm"
                    icon="circle-check"
                    text="No errors in this range."
                  />
                ) : (
                  <ul className="home-errors" aria-label="Error log">
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
              </Panel>
            </div>

            <div className="stack">
              <Panel title="Right now">
                <div className="home-rows">
                  <a className="home-row" href="#/sessions">
                    <span className="home-row-icon" aria-hidden="true">
                      <Ic name="activity" size={15} />
                    </span>
                    <span className="home-row-label grow">Running</span>
                    <span className="home-row-value num">
                      {count(now?.sessions_running || 0)}
                    </span>
                  </a>
                  <a className="home-row" href="#/sessions">
                    <span className="home-row-icon" aria-hidden="true">
                      <Ic name="clock" size={15} />
                    </span>
                    <span className="home-row-label grow">
                      Waiting for answer
                    </span>
                    <span className="home-row-value num">
                      {count(now?.sessions_waiting_input || 0)}
                    </span>
                  </a>
                </div>
              </Panel>

              <Panel title="By model">
                {modelRows.length === 0 ? (
                  <EmptyState
                    size="sm"
                    icon="cpu"
                    text="No model usage in this range."
                  />
                ) : (
                  <div className="home-rows">
                    {modelRows.map((row) => (
                      <div
                        className="home-row"
                        key={row.provider + "/" + row.model}
                      >
                        <span className="home-row-main">
                          <span className="home-row-title trunc">
                            {row.model}
                          </span>
                          <span className="home-row-sub">
                            {row.provider} · {count(row.requests)} requests
                          </span>
                        </span>
                        <span className="home-row-value num">
                          {compact(
                            (row.input_tokens || 0) + (row.output_tokens || 0)
                          )}{" "}
                          tok
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </Panel>

              <Panel title="By agent">
                {agentRows.length === 0 ? (
                  <EmptyState
                    size="sm"
                    icon="users"
                    text="No agent activity in this range."
                  />
                ) : (
                  <div className="home-rows">
                    {agentRows.map((row) => (
                      <div className="home-row" key={row.agent}>
                        <span className="home-row-main">
                          <span className="home-row-title trunc">
                            {row.agent}
                          </span>
                          <span className="home-row-sub">
                            {count(row.turns)} turns
                          </span>
                        </span>
                        <span className="home-row-value num">
                          {compact(
                            (row.input_tokens || 0) + (row.output_tokens || 0)
                          )}{" "}
                          tok
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </Panel>

              <Panel title="By source">
                {sourceRows.length === 0 ||
                sourceRows.every((row) => !row.count) ? (
                  <EmptyState
                    size="sm"
                    icon="layers"
                    text="No token usage by source in this range."
                  />
                ) : (
                  <div className="home-rows">
                    {sourceRows.map((row) => (
                      <div className="home-row" key={row.source}>
                        <span className="home-row-main">
                          <span className="home-row-title trunc">
                            {BREAKDOWN_SOURCE_LABELS[row.source] || row.source}
                          </span>
                          <span className="home-row-sub">
                            {count(row.count)} calls
                          </span>
                        </span>
                        <span className="home-row-value num">
                          {compact(
                            (row.input_tokens || 0) + (row.output_tokens || 0)
                          )}{" "}
                          tok
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </Panel>
            </div>
          </div>
        </>
      )}
    </main>
  );
}
