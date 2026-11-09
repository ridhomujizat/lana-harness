import { useMemo, useState } from "react";
import type { DashboardErrorRow } from "../api/dashboard";
import { Badge, Button, EmptyState, ErrorState, Panel, Seg } from "../ui/gr8r";
import type { BadgeTone } from "../ui/gr8r";
import { useAgentLabel } from "./agent-label";
import {
  DashboardFilters,
  clockTime,
  count,
  duration,
  share,
  useDashboard,
} from "./dashboard-common";

/* Observe → Logs (moved out of Home on owner request 2026-11-05): the error
   log of spec 28 (REQ-DSH-005) and the tool health table, with the same range
   and agent filter as Home. */

const DAEMON_AGENT_NOTE =
  "Daemon logs are not per agent, so they are hidden while an agent is selected.";

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

const ERROR_FILTERS: Array<{ value: string; label: string }> = [
  { value: "all", label: "All" },
  { value: "turn", label: "Turn" },
  { value: "provider", label: "Provider" },
  { value: "tool", label: "Tool" },
  { value: "daemon", label: "Daemon" },
];

function ErrorLogRow({
  row,
  open,
  onToggle,
  label,
}: {
  row: DashboardErrorRow;
  open: boolean;
  onToggle: () => void;
  label: (name: string) => string;
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
        <span className="home-error-agent trunc">{row.agent ? label(row.agent) : "—"}</span>
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
              <dd>{label(row.agent)}</dd>
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
   and its position among rows sharing the same millisecond. */
function errorKey(row: DashboardErrorRow, indexInSameMs: number): string {
  return [row.at, row.source || "", row.session_id || "", row.tool || "", row.turn_id || "", indexInSameMs].join("|");
}

export function LogsPage() {
  const state = useDashboard("#/logs");
  const label = useAgentLabel();
  const { data, agent, error } = state;
  const [errorFilter, setErrorFilter] = useState("all");
  const [openErrors, setOpenErrors] = useState<string[]>([]);

  /* Daemon rows are hidden with an agent filter, so that chip cannot stay on. */
  const filter = agent && errorFilter === "daemon" ? "all" : errorFilter;

  const visibleErrors = useMemo(() => {
    const seen = new Map<number, number>();
    return (data?.errors || [])
      .map((row) => {
        const indexInSameMs = seen.get(row.at) || 0;
        seen.set(row.at, indexInSameMs + 1);
        return { row, key: errorKey(row, indexInSameMs) };
      })
      .filter(({ row }) => filter === "all" || row.source === filter);
  }, [data, filter]);

  const toolRows = useMemo(
    () => [...(data?.tools || [])].sort((a, b) => b.calls - a.calls || a.name.localeCompare(b.name)),
    [data]
  );

  function toggleError(key: string) {
    setOpenErrors((current) =>
      current.includes(key) ? current.filter((value) => value !== key) : current.concat(key)
    );
  }

  return (
    <main id="main" className="page home-page logs-page">
      <header className="ph">
        <div>
          <h1>Logs</h1>
          <p>Errors from turns, providers, tools and the daemon, and how each tool behaves.</p>
        </div>
        <div className="acts">
          <DashboardFilters state={state} />
        </div>
      </header>

      {error ? (
        <div className="home-alert" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not refresh"
            text={error}
            action={
              <Button variant="secondary" icon="refresh-cw" onClick={state.reload}>
                Retry
              </Button>
            }
          />
        </div>
      ) : null}

      {!data ? (
        <div className="home-empty">{error ? "No data yet." : "Loading logs…"}</div>
      ) : (
        <div className="stack">
          <Panel
            title="Error log"
            actions={
              <Seg
                className="home-err-filter"
                aria-label="Error source"
                options={ERROR_FILTERS.map((option) => {
                  const daemonHidden = option.value === "daemon" && agent !== "";
                  return {
                    value: option.value,
                    label: option.label,
                    disabled: daemonHidden,
                    title: daemonHidden ? DAEMON_AGENT_NOTE : undefined,
                  };
                })}
                value={filter}
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
                  {data.daemon_started_at ? " (" + clockTime(data.daemon_started_at) + ")" : ""}.
                </>
              )}
            </p>
            {visibleErrors.length === 0 ? (
              <EmptyState size="sm" icon="circle-check" text="No errors in this range." />
            ) : (
              <ul className="home-errors" aria-label="Error log">
                {visibleErrors.map(({ row, key }) => (
                  <ErrorLogRow
                    key={key}
                    row={row}
                    open={openErrors.includes(key)}
                    onToggle={() => toggleError(key)}
                    label={label}
                  />
                ))}
              </ul>
            )}
          </Panel>

          <Panel title="Tool health">
            {toolRows.length === 0 ? (
              <EmptyState size="sm" icon="wrench" text="No tool calls in this range." />
            ) : (
              <div className="home-table-wrap">
                <table className="perm-t home-table home-tools-table">
                  <thead>
                    <tr>
                      <th scope="col">Tool</th>
                      <th scope="col" className="is-num">Calls</th>
                      <th scope="col" className="is-num">Errors</th>
                      <th scope="col" className="is-num">Rejected</th>
                      <th scope="col" className="is-num">Error share</th>
                      <th scope="col" className="is-num">Avg time</th>
                      <th scope="col" className="is-num">p95 time</th>
                      <th scope="col">Last error</th>
                    </tr>
                  </thead>
                  <tbody>
                    {toolRows.map((row) => {
                      const marked = row.calls >= 5 && row.errors / row.calls > 0.2;
                      return (
                        <tr key={row.name} className={marked ? "is-danger" : undefined}>
                          <td>{row.name}</td>
                          <td className="is-num num">{count(row.calls)}</td>
                          <td className="is-num num">{count(row.errors)}</td>
                          <td className="is-num num">{count(row.rejected)}</td>
                          <td className="is-num num">{share(row.errors, row.calls)}</td>
                          <td className="is-num num">{duration(row.avg_ms)}</td>
                          <td className="is-num num">{duration(row.p95_ms)}</td>
                          <td className="home-last-error">
                            {row.last_error ? (
                              <>
                                <span className="home-last-error-text">{row.last_error}</span>
                                <span className="home-last-error-time num">{clockTime(row.last_error_at)}</span>
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
        </div>
      )}
    </main>
  );
}
