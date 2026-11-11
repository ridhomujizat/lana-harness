import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { errorMessage } from "../api/client";
import {
  getKnowledgeActivity,
  getKnowledgeHealth,
  getKnowledgeReport,
} from "../api/knowledge";
import type {
  KnowledgeActivityEntry,
  KnowledgeHealth,
  KnowledgeReport,
  KnowledgeStatus,
} from "../api/knowledge";
import { Markdown } from "../lib/markdown";
import { Ic } from "../ui/icons";
import { Badge, Button, Drawer, EmptyState, Progress, Skeleton } from "../ui/gr8r";
import { AgentName } from "./agent-avatar";
import { useAgentLabel } from "./agent-label";
import { makeScopeColors, openHref, ScopeDot } from "./knowledge-shared";
import type { ScopeColors } from "./knowledge-shared";

/* Spec 48 REQ-KGR-055: what the nightly run and the agents did, the health of
   the knowledge, and the vault details that used to fill the page. */

type Filter = "" | "run" | "writes" | "review";

const FILTERS: { key: Filter; label: string }[] = [
  { key: "", label: "All" },
  { key: "run", label: "Nightly runs" },
  { key: "writes", label: "Agent writes" },
  { key: "review", label: "Reviews" },
];

function wikiOpen(target: string, text: string) {
  return (
    <a className="kg-wikilink" href={openHref(target)}>
      {text}
    </a>
  );
}

function dayKey(at: string): string {
  const date = new Date(at);
  if (Number.isNaN(date.getTime())) return at.slice(0, 10);
  return (
    date.getFullYear() +
    "-" +
    String(date.getMonth() + 1).padStart(2, "0") +
    "-" +
    String(date.getDate()).padStart(2, "0")
  );
}

function dayLabel(key: string): string {
  const today = dayKey(new Date().toISOString());
  const yesterday = dayKey(new Date(Date.now() - 86400000).toISOString());
  if (key === today) return "Today";
  if (key === yesterday) return "Yesterday";
  const date = new Date(key + "T00:00:00");
  return Number.isNaN(date.getTime())
    ? key
    : date.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "short" });
}

function clock(at: string): string {
  const date = new Date(at);
  if (Number.isNaN(date.getTime())) return "";
  return String(date.getHours()).padStart(2, "0") + ":" + String(date.getMinutes()).padStart(2, "0");
}

function fullTime(at: string): string {
  const date = new Date(at);
  return Number.isNaN(date.getTime())
    ? at
    : date.toLocaleString(undefined, {
        weekday: "short",
        day: "numeric",
        month: "short",
        hour: "2-digit",
        minute: "2-digit",
      });
}

function entryTitle(entry: KnowledgeActivityEntry, label: (name: string) => string): string {
  const who = entry.actor ? label(entry.actor) || entry.actor : "";
  switch (entry.kind) {
    case "run":
      return "Nightly run";
    case "error":
      return "Nightly run failed";
    case "promotion":
      return (who || "An agent") + " updated its knowledge";
    case "write":
      return who ? who + " wrote to its knowledge" : entry.heading;
    case "lint":
      return "Lint report";
    case "review":
      return entry.heading.charAt(0).toUpperCase() + entry.heading.slice(1);
    default:
      return entry.heading;
  }
}

function entryColor(entry: KnowledgeActivityEntry, colors: ScopeColors): string {
  if (entry.kind === "error") return "#E5484D";
  if (entry.actor) return colors(entry.actor);
  return "#8A867E";
}

function TimelineEntry({
  entry,
  colors,
}: {
  entry: KnowledgeActivityEntry;
  colors: ScopeColors;
}) {
  const label = useAgentLabel();
  return (
    <li className={"ka-entry ka-" + entry.kind} data-testid="activity-entry">
      <time className="ka-time" dateTime={entry.at} title={fullTime(entry.at)}>
        {clock(entry.at)}
      </time>
      <span className="ka-dot" aria-hidden="true">
        <ScopeDot color={entryColor(entry, colors)} size={10} />
      </span>
      <div className="ka-body">
        <p className="ka-title">{entryTitle(entry, label)}</p>
        {entry.lines.length ? (
          <div className="ka-lines">
            <Markdown text={entry.lines.map((line) => "- " + line).join("\n")} wikiLink={wikiOpen} />
          </div>
        ) : null}
      </div>
    </li>
  );
}

function ReportDrawer({ onClose }: { onClose: () => void }) {
  const [report, setReport] = useState<KnowledgeReport | null | undefined>(undefined);
  const [error, setError] = useState("");
  useEffect(() => {
    let alive = true;
    getKnowledgeReport()
      .then((result) => {
        if (alive) setReport(result);
      })
      .catch((failure) => {
        if (alive) {
          setReport(null);
          setError(errorMessage(failure));
        }
      });
    return () => {
      alive = false;
    };
  }, []);
  return (
    <Drawer title="Lint report" onClose={onClose} className="knowledge-viewer">
      {error ? (
        <div className="alert danger" role="alert">
          <Ic name="circle-alert" size={15} />
          <span className="grow">{error}</span>
        </div>
      ) : report === undefined ? (
        <div className="knowledge-loading" aria-busy="true">
          <Skeleton width="60%" height={12} />
          <Skeleton width="90%" height={12} />
        </div>
      ) : report ? (
        <div className="knowledge-report">
          <code className="mono knowledge-path">{report.path}</code>
          <Markdown text={report.markdown} wikiLink={wikiOpen} />
        </div>
      ) : (
        <p className="knowledge-empty">No report yet</p>
      )}
    </Drawer>
  );
}

function HealthCard({
  health,
  onOpenReport,
}: {
  health: KnowledgeHealth | null;
  onOpenReport: () => void;
}) {
  const label = useAgentLabel();
  if (!health) {
    return (
      <aside className="ka-health" aria-busy="true">
        <Skeleton width="50%" height={14} />
        <Skeleton width="100%" height={80} radius={8} />
      </aside>
    );
  }
  const lint = health.lint;
  const rows: { label: string; value: number; tone?: string }[] = lint
    ? [
        { label: "Possible duplicates", value: lint.duplicates, tone: "amber" },
        { label: "Contradictions", value: lint.contradictions + lint.conflicts, tone: "red" },
        { label: "Broken links", value: lint.broken },
        { label: "Pages with no links", value: lint.orphans },
        { label: "Missing details", value: lint.missing },
      ]
    : [];
  return (
    <aside className="ka-health" aria-label="Knowledge health">
      <h3>Knowledge health</h3>
      {lint ? (
        <dl className="ka-rows">
          {rows.map((row) => (
            <div key={row.label}>
              <dt>{row.label}</dt>
              <dd className={row.value && row.tone ? "is-" + row.tone : ""}>{row.value}</dd>
            </div>
          ))}
          <div>
            <dt>Waiting for review</dt>
            <dd>{health.review_pending}</dd>
          </div>
        </dl>
      ) : (
        <p className="kg-none">No lint report yet. The nightly run writes one.</p>
      )}
      {lint ? (
        <Button variant="secondary" size="sm" icon="file-text" onClick={onOpenReport}>
          Open report · {lint.date}
        </Button>
      ) : null}
      {health.memory.length ? (
        <>
          <h4 className="kg-lbl">Agent memory (MEMORY.md)</h4>
          <ul className="ka-mem">
            {health.memory.map((item) => {
              const ratio = item.limit > 0 ? item.characters / item.limit : 0;
              return (
                <li key={item.agent}>
                  <span className="ka-mem-h">
                    <AgentName name={item.agent} label={label(item.agent)} />
                    <span className="ka-mem-n">
                      {(item.characters / 1000).toFixed(1)}k / {(item.limit / 1000).toFixed(0)}k
                    </span>
                  </span>
                  <Progress
                    value={ratio}
                    tone={ratio > 0.9 ? "red" : ratio > 0.75 ? "amber" : "green"}
                    aria-label={"MEMORY.md of " + item.agent}
                  />
                </li>
              );
            })}
          </ul>
          <p className="kg-none">
            Notes move from MEMORY.md into pages each night, so memory stays small.
          </p>
        </>
      ) : null}
    </aside>
  );
}

export function ActivityTab({
  status,
  details,
}: {
  status: KnowledgeStatus;
  details: ReactNode;
}) {
  const [filter, setFilter] = useState<Filter>("");
  const [entries, setEntries] = useState<KnowledgeActivityEntry[] | null>(null);
  const [nextBefore, setNextBefore] = useState("");
  const [loadingMore, setLoadingMore] = useState(false);
  const [health, setHealth] = useState<KnowledgeHealth | null>(null);
  const [error, setError] = useState("");
  const [reportOpen, setReportOpen] = useState(false);
  const colors = useMemo(
    () => makeScopeColors((status.scopes || []).map((item) => item.scope)),
    [status.scopes]
  );
  const lastRunAt = status.consolidation?.last_at || 0;

  useEffect(() => {
    let alive = true;
    getKnowledgeHealth()
      .then((result) => {
        if (alive) setHealth(result);
      })
      .catch((failure) => {
        if (alive) setError(errorMessage(failure));
      });
    return () => {
      alive = false;
    };
  }, [lastRunAt]);

  useEffect(() => {
    let alive = true;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- reset before the async fetch.
    setEntries(null);
    getKnowledgeActivity({ kind: filter || undefined })
      .then((page) => {
        if (!alive) return;
        setEntries(page.entries);
        setNextBefore(page.next_before);
        setError("");
      })
      .catch((failure) => {
        if (!alive) return;
        setEntries([]);
        setNextBefore("");
        setError(errorMessage(failure));
      });
    return () => {
      alive = false;
    };
  }, [filter, lastRunAt]);

  const loadOlder = useCallback(async () => {
    if (!nextBefore || loadingMore) return;
    setLoadingMore(true);
    try {
      const page = await getKnowledgeActivity({ kind: filter || undefined, before: nextBefore });
      setEntries((current) => [...(current || []), ...page.entries]);
      setNextBefore(page.next_before);
    } catch (failure) {
      setError(errorMessage(failure));
    } finally {
      setLoadingMore(false);
    }
  }, [filter, nextBefore, loadingMore]);

  const days = useMemo(() => {
    const groups: { key: string; items: KnowledgeActivityEntry[] }[] = [];
    for (const entry of entries || []) {
      const key = dayKey(entry.at);
      const last = groups[groups.length - 1];
      if (last && last.key === key) last.items.push(entry);
      else groups.push({ key, items: [entry] });
    }
    return groups;
  }, [entries]);

  const run = health?.last_run || null;
  const lint = health?.lint || null;
  const lintProblems = lint
    ? lint.contradictions + lint.conflicts + lint.duplicates + lint.broken + lint.orphans + lint.missing
    : 0;
  const nextAt = health?.next_at || "";
  const stats = run
    ? [
        { n: run.journaled, l: run.journaled === 1 ? "chat read" : "chats read" },
        { n: run.applied, l: "changes written" },
        { n: run.skipped, l: "skipped" },
        { n: lintProblems, l: lintProblems === 1 ? "lint warning" : "lint warnings" },
        { n: health?.review_pending || 0, l: "waiting for review" },
      ]
    : [];

  return (
    <div className="kg-tab ka-tab">
      <div className="kg-chips" role="group" aria-label="Activity filter">
        {FILTERS.map((item) => (
          <button
            key={item.key || "all"}
            type="button"
            className={"kg-chip" + (filter === item.key ? " on" : "")}
            aria-pressed={filter === item.key}
            onClick={() => setFilter(item.key)}
          >
            {item.label}
          </button>
        ))}
      </div>
      {error ? (
        <div className="alert danger" role="alert">
          <Ic name="circle-alert" size={15} />
          <span className="grow">{error}</span>
        </div>
      ) : null}
      <div className="ka-grid">
        <div className="ka-main">
          <section className="ka-run" aria-label="Last nightly run">
            <div className="ka-run-h">
              <span className="ka-run-title">
                <ScopeDot color={run?.error ? "#E5484D" : "#2E9D6A"} />
                {run ? "Last nightly run · " + fullTime(run.at) : "No nightly run yet"}
              </span>
              {nextAt ? <span className="ka-run-next">Next: {fullTime(nextAt)}</span> : null}
            </div>
            {run?.error ? (
              <p className="ka-run-err">
                <Ic name="circle-alert" size={14} /> {run.error}
              </p>
            ) : null}
            {stats.length ? (
              <div className="ka-stats">
                {stats.map((item) => (
                  <div key={item.l}>
                    <strong>{item.n}</strong>
                    <span>{item.l}</span>
                  </div>
                ))}
              </div>
            ) : null}
          </section>
          {entries === null ? (
            <div className="knowledge-loading" aria-busy="true">
              <span className="sr">Loading activity…</span>
              <Skeleton width="100%" height={40} radius={8} />
              <Skeleton width="100%" height={40} radius={8} />
            </div>
          ) : entries.length === 0 ? (
            <EmptyState
              icon="history"
              title="Nothing here yet"
              text="Agent writes and nightly runs show up here as they happen."
            />
          ) : (
            <div className="ka-days">
              {days.map((day) => (
                <section key={day.key} className="ka-day">
                  <h3 className="kg-lbl">{dayLabel(day.key)}</h3>
                  <ol className="ka-list">
                    {day.items.map((entry, index) => (
                      <TimelineEntry key={entry.at + index} entry={entry} colors={colors} />
                    ))}
                  </ol>
                </section>
              ))}
              {nextBefore ? (
                <Button variant="secondary" size="sm" loading={loadingMore} onClick={loadOlder}>
                  Load older
                </Button>
              ) : null}
            </div>
          )}
        </div>
        <HealthCard health={health} onOpenReport={() => setReportOpen(true)} />
      </div>
      <details className="ka-details">
        <summary>
          <Ic name="settings-2" size={14} /> Details: vault, sources, consolidation
          {status.sources_failed && status.sources_failed.length ? (
            <Badge tone="red">{status.sources_failed.length} failed</Badge>
          ) : null}
        </summary>
        {details}
      </details>
      {reportOpen ? <ReportDrawer onClose={() => setReportOpen(false)} /> : null}
    </div>
  );
}
