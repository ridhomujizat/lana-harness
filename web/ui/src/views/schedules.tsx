import { useCallback, useEffect, useMemo, useState } from "react";
import { errorMessage } from "../api/client";
import { getWorkflow, listRuns, listWorkflows, startRun } from "../api/workflows";
import type { Run, WfNode, Workflow } from "../api/workflows";
import { Ic } from "../ui/icons";
import { Badge, Button, EmptyState, ErrorState, Seg, useToast } from "../ui/gr8r";
import { nextCron } from "./cron-next";
import { useAgentLabel } from "./agent-label";
import { describeCron, isTrigger, nodeMeta } from "./workflow-catalog";
import {
  RUN_TONES,
  RUN_WORDS,
  WorkflowBadge,
  ago,
  dayClock,
  duration,
  useNow,
  useWorkflowStream,
} from "./workflow-common";

/* Observe → Schedules (owner request 2026-11-05): every trigger that can start
   a workflow on its own, with the next fire time of schedules and intervals,
   and the latest runs across workflows (spec #33 REQ-WF-003). */

type Filter = "timed" | "listening" | "all";

interface TriggerRow {
  workflow: Workflow;
  node: WfNode;
  next: number;
  live: boolean;
}

function triggerText(
  node: WfNode,
  workflow: Workflow,
  label: (name: string) => string
): { title: string; detail: string } {
  const p = node.params || {};
  switch (node.type) {
    case "trigger.schedule":
      return { title: describeCron(String(p.cron || "")), detail: String(p.cron || "") + " · " + String(p.timezone || "local time") };
    case "trigger.interval":
      return { title: "Every " + String(p.minutes) + " min", detail: "Interval" };
    case "trigger.webhook":
      return { title: "POST " + workflow.webhook.path, detail: "Webhook · X-Lana-Token" };
    case "trigger.chat":
      return { title: "/" + String(p.command || ""), detail: "Web chat and Telegram owner chat" };
    case "trigger.event":
      return { title: String(p.kind || ""), detail: "Lana event" + (p.agent ? " · " + label(String(p.agent)) : "") };
    default:
      return { title: "Run button", detail: "Manual" };
  }
}

function untilText(ms: number, now: number): string {
  const diff = ms - now;
  if (diff <= 60000) {
    return "within a minute";
  }
  const min = Math.round(diff / 60000);
  if (min < 60) {
    return "in " + min + " min";
  }
  const hours = Math.floor(min / 60);
  if (hours < 24) {
    return "in " + hours + " h " + (min % 60) + " m";
  }
  return "in " + Math.round(hours / 24) + " days";
}

function whenText(row: TriggerRow, now: number): { main: string; sub: string } {
  const timed = row.node.type === "trigger.schedule" || row.node.type === "trigger.interval";
  if (!row.live) {
    return {
      main: "Not firing",
      sub: row.node.params.enabled === false ? "trigger is off" : "workflow is " + row.workflow.status,
    };
  }
  if (!timed) {
    return { main: "Listening", sub: "starts on demand" };
  }
  if (!row.next) {
    return { main: "—", sub: "no upcoming time" };
  }
  return { main: dayClock(row.next, now), sub: untilText(row.next, now) };
}

function TriggerList({
  list,
  now,
  busy,
  label,
  onRun,
}: {
  list: TriggerRow[];
  now: number;
  busy: string;
  label: (name: string) => string;
  onRun: (workflow: Workflow) => void;
}) {
  return (
    <section className="sch-table" aria-label="Triggers">
      <div className="sch-tr sch-th" role="row">
        <span>When</span>
        <span>Trigger</span>
        <span>Workflow</span>
        <span>Status</span>
        <span />
      </div>
      {list.map((row) => {
        const meta = nodeMeta(row.node.type);
        const text = triggerText(row.node, row.workflow, label);
        const when = whenText(row, now);
        return (
          <div className={"sch-tr" + (row.live ? "" : " off")} role="row" key={row.workflow.id + row.node.id}>
            <span className="sch-cell">
              <b className="num">{when.main}</b>
              <small>{when.sub}</small>
            </span>
            <span className="sch-trigger">
              <span className={"wf-ico sm tint-" + (row.live ? "green" : "gray")}>
                <Ic name={meta.icon} size={14} />
              </span>
              <span className="sch-cell">
                <span className="trunc">{text.title}</span>
                <small className="trunc mono">{text.detail}</small>
              </span>
            </span>
            <a className="sch-cell sch-wf" href={"#/workflows/" + encodeURIComponent(row.workflow.id)}>
              <span className="trunc">{row.workflow.name}</span>
              <small className="trunc">
                Guarded by {label(row.workflow.guardian)} · v{row.workflow.version}
              </small>
            </a>
            <span>
              <WorkflowBadge status={row.workflow.status} />
            </span>
            <span className="sch-acts">
              <Button
                size="sm"
                variant="ghost"
                icon="play"
                loading={busy === row.workflow.id}
                onClick={() => onRun(row.workflow)}
                aria-label={"Run " + row.workflow.name + " now"}
              >
                Run
              </Button>
            </span>
          </div>
        );
      })}
    </section>
  );
}

export function SchedulesPage() {
  const toast = useToast();
  const label = useAgentLabel();
  const now = useNow(30000);
  const [workflows, setWorkflows] = useState<Workflow[] | null>(null);
  const [runs, setRuns] = useState<Run[]>([]);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState<Filter>("timed");
  const [busy, setBusy] = useState("");

  const load = useCallback(async () => {
    try {
      const list = await listWorkflows();
      const details = await Promise.all(list.workflows.map((row) => getWorkflow(row.id)));
      const recent = await Promise.all(list.workflows.map((row) => listRuns(row.id, { limit: 10 })));
      setWorkflows(details);
      setRuns(
        [...new Map(recent.flatMap((response) => response.runs).map((run) => [run.id, run])).values()].sort(
          (a, b) => b.started_at - a.started_at
        )
      );
      setError("");
    } catch (failure) {
      setError(errorMessage(failure));
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the first update happens after the await
    void load();
  }, [load]);
  useWorkflowStream(() => void load());

  const rows = useMemo<TriggerRow[]>(() => {
    const out: TriggerRow[] = [];
    for (const workflow of workflows || []) {
      for (const node of workflow.graph.nodes) {
        if (!isTrigger(node.type) || node.type === "trigger.manual") {
          continue;
        }
        const enabled = node.params.enabled !== false;
        const live = enabled && workflow.status === "active";
        let next = 0;
        if (live && node.type === "trigger.schedule") {
          next = nextCron(String(node.params.cron || ""), String(node.params.timezone || ""), now);
        }
        if (live && node.type === "trigger.interval") {
          const minutes = Math.max(1, Number(node.params.minutes) || 1);
          const last = runs.find((run) => run.workflow_id === workflow.id && run.trigger === "interval");
          next = last ? Math.max(now, last.started_at + minutes * 60000) : now;
        }
        out.push({ workflow, node, next, live });
      }
    }
    return out.sort((a, b) => {
      if (a.live !== b.live) {
        return a.live ? -1 : 1;
      }
      if (a.next && b.next) {
        return a.next - b.next;
      }
      return a.next ? -1 : b.next ? 1 : a.workflow.name.localeCompare(b.workflow.name);
    });
  }, [workflows, runs, now]);

  const nextUp = rows.find((row) => row.live && row.next > 0);
  const names = new Map((workflows || []).map((workflow) => [workflow.id, workflow.name]));

  async function runNow(workflow: Workflow) {
    setBusy(workflow.id);
    try {
      const run = await startRun(workflow.id);
      window.location.hash = "#/workflows/" + encodeURIComponent(workflow.id) + "/runs/" + run.number;
    } catch (failure) {
      toast.show({ title: "Could not start a run", body: errorMessage(failure), tone: "error" });
    } finally {
      setBusy("");
    }
  }

  if (error && !workflows) {
    return (
      <main id="main" className="page wide sch-page">
        <ErrorState title="Could not load schedules" text={error} action={<Button onClick={() => void load()}>Try again</Button>} />
      </main>
    );
  }

  const isTimed = (row: TriggerRow) => row.node.type === "trigger.schedule" || row.node.type === "trigger.interval";
  const liveRows = rows.filter((row) => row.live);
  const shownRows =
    filter === "timed" ? liveRows.filter(isTimed) : filter === "listening" ? liveRows.filter((row) => !isTimed(row)) : rows;
  const startOfDay = new Date(new Date(now).setHours(0, 0, 0, 0)).getTime();
  const runsToday = runs.filter((run) => run.started_at >= startOfDay).length;

  return (
    <main id="main" className="page wide sch-page">
      <header className="ph">
        <div>
          <h1>Schedules</h1>
          <p>Everything that can start a workflow on its own, and what ran lately.</p>
        </div>
        <div className="acts">
          <Button as="a" href="#/workflows/new" variant="primary" icon="plus" size="sm">
            New workflow
          </Button>
        </div>
      </header>

      <div className="home-now sch-summary">
        <span className="home-now-k">Next</span>
        {nextUp ? (
          <a className="home-now-item" href={"#/workflows/" + encodeURIComponent(nextUp.workflow.id)}>
            <b>{nextUp.workflow.name}</b> · {dayClock(nextUp.next, now)} · {untilText(nextUp.next, now)}
          </a>
        ) : (
          <span className="home-now-item">No schedule will fire</span>
        )}
        <span className="home-now-item">
          <b className="num">{liveRows.length}</b> active triggers
        </span>
        <span className="home-now-item">
          <b className="num">{runsToday}</b> runs today
        </span>
      </div>

      <div className="sch-toolbar">
        <Seg
          value={filter}
          onChange={(value) => setFilter(value as Filter)}
          options={[
            { value: "timed", label: "Upcoming", count: liveRows.filter(isTimed).length },
            { value: "listening", label: "Listening", count: liveRows.filter((row) => !isTimed(row)).length },
            { value: "all", label: "All", count: rows.length },
          ]}
        />
      </div>

      {!workflows ? <p className="muted sch-pad">Loading…</p> : null}
      {workflows && shownRows.length === 0 ? (
        <div className="panel">
          <EmptyState
            icon="calendar-clock"
            title={filter === "listening" ? "Nothing is listening" : "Nothing scheduled"}
            text={
              filter === "listening"
                ? "Add a Webhook, Chat command or Lana event trigger to an active workflow."
                : "Add a Schedule or Interval trigger to a workflow and activate it."
            }
          />
        </div>
      ) : null}
      {shownRows.length > 0 ? <TriggerList list={shownRows} now={now} busy={busy} label={label} onRun={(wf) => void runNow(wf)} /> : null}

      <h2 className="sch-h2">Recent runs</h2>
      {workflows && runs.length === 0 ? (
        <div className="panel">
          <EmptyState size="sm" icon="play" text="No workflow has run yet." />
        </div>
      ) : null}
      {runs.length > 0 ? (
        <section className="sch-table" aria-label="Recent runs">
          {runs.slice(0, 12).map((run) => (
            <a
              className="sch-run"
              key={run.id}
              href={"#/workflows/" + encodeURIComponent(run.workflow_id) + "/runs/" + run.number}
            >
              <span>
                <Badge tone={RUN_TONES[run.status]} dot>
                  {RUN_WORDS[run.status]}
                </Badge>
              </span>
              <span className="trunc">
                {names.get(run.workflow_id) || run.workflow_id} <span className="muted">#{run.number}</span>
              </span>
              <span className="muted trunc">
                {run.trigger} · {run.error || run.summary || duration((run.ended_at || now) - run.started_at)}
              </span>
              <span className="muted num">{ago(run.started_at, now)}</span>
            </a>
          ))}
        </section>
      ) : null}
    </main>
  );
}
