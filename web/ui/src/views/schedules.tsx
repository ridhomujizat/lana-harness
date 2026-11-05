import { useCallback, useEffect, useMemo, useState } from "react";
import { errorMessage } from "../api/client";
import { getWorkflow, listRuns, listWorkflows, startRun } from "../api/workflows";
import type { Run, WfNode, Workflow } from "../api/workflows";
import { Ic } from "../ui/icons";
import { Badge, Button, EmptyState, ErrorState, Panel, Seg, useToast } from "../ui/gr8r";
import { nextCron } from "./cron-next";
import { describeCron, isTrigger, nodeMeta } from "./workflow-catalog";
import {
  AgentFace,
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

interface TriggerRow {
  workflow: Workflow;
  node: WfNode;
  next: number;
  live: boolean;
}

function triggerText(node: WfNode, workflow: Workflow): { title: string; detail: string } {
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
      return { title: String(p.kind || ""), detail: "Lana event" + (p.agent ? " · " + String(p.agent) : "") };
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

function TriggerTable({
list,
timedRows,
now,
busy,
onRun,
}: {
list: TriggerRow[];
timedRows: boolean;
now: number;
busy: string;
onRun: (workflow: Workflow) => void;
}) {
  return (
    <div className="sch-table" role="table">
      <div className="sch-tr sch-th" role="row">
        <span>{timedRows ? "Next run" : "Trigger"}</span>
        <span>Workflow</span>
        <span>{timedRows ? "Schedule" : "Listens to"}</span>
        <span>Status</span>
        <span />
      </div>
      {list.map((row) => {
        const meta = nodeMeta(row.node.type);
        const text = triggerText(row.node, row.workflow);
        return (
          <div className={"sch-tr" + (row.live ? "" : " off")} role="row" key={row.workflow.id + row.node.id}>
            <span className="sch-when">
              <span className={"wf-ico sm tint-" + (row.live ? "green" : "gray")}>
                <Ic name={meta.icon} size={15} />
              </span>
              {timedRows ? (
                <span>
                  <b>{row.live && row.next ? untilText(row.next, now) : row.live ? "—" : "Not firing"}</b>
                  <small className="num">{row.live && row.next ? dayClock(row.next, now) : row.node.params.enabled === false ? "trigger off" : "workflow " + row.workflow.status}</small>
                </span>
              ) : (
                <span>
                  <b>{meta.label}</b>
                  <small>{row.live ? "listening" : row.node.params.enabled === false ? "trigger off" : "workflow " + row.workflow.status}</small>
                </span>
              )}
            </span>
            <a className="sch-wf" href={"#/workflows/" + encodeURIComponent(row.workflow.id)}>
              <AgentFace name={row.workflow.guardian} />
              <span>
                <b className="trunc">{row.workflow.name}</b>
                <small className="trunc">Guarded by {row.workflow.guardian}</small>
              </span>
            </a>
            <span className="sch-what">
              <span className="mono trunc">{text.title}</span>
              <small className="trunc">{text.detail}</small>
            </span>
            <span>
              <WorkflowBadge status={row.workflow.status} />
            </span>
            <span className="sch-acts">
              <Button size="sm" loading={busy === row.workflow.id} onClick={() => onRun(row.workflow)}>
                Run now
              </Button>
            </span>
          </div>
        );
      })}
    </div>
  );
}

export function SchedulesPage() {
  const toast = useToast();
  const now = useNow(30000);
  const [workflows, setWorkflows] = useState<Workflow[] | null>(null);
  const [runs, setRuns] = useState<Run[]>([]);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState<"live" | "all">("live");
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

  const shown = filter === "live" ? rows.filter((row) => row.live) : rows;
  const timed = shown.filter((row) => row.node.type === "trigger.schedule" || row.node.type === "trigger.interval");
  const others = shown.filter((row) => !(row.node.type === "trigger.schedule" || row.node.type === "trigger.interval"));
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

  return (
    <main id="main" className="page wide sch-page">
      <header className="ph">
        <div>
          <h1>Schedules</h1>
          <p>Everything that can start a workflow on its own, and what ran lately.</p>
        </div>
        <div className="acts">
          <Seg
            value={filter}
            onChange={(value) => setFilter(value as "live" | "all")}
            options={[
              { value: "live", label: "Active", count: rows.filter((row) => row.live).length },
              { value: "all", label: "All", count: rows.length },
            ]}
          />
          <Button as="a" href="#/workflows/new" variant="primary" icon="plus" size="sm">
            New workflow
          </Button>
        </div>
      </header>

      {workflows ? (
        <div className="sch-next">
          <Ic name="alarm-clock" size={16} />
          {nextUp ? (
            <span>
              Next: <b>{nextUp.workflow.name}</b> {untilText(nextUp.next, now)} · {dayClock(nextUp.next, now)}
            </span>
          ) : (
            <span>No schedule will fire. Activate a workflow with a Schedule or Interval trigger.</span>
          )}
        </div>
      ) : null}

      <Panel title="Timed triggers">
        {!workflows ? <p className="muted sch-pad">Loading…</p> : null}
        {workflows && timed.length === 0 ? (
          <EmptyState size="sm" icon="calendar-clock" title="No schedules" text="Add a Schedule or Interval trigger to a workflow and activate it." />
        ) : null}
        {timed.length > 0 ? <TriggerTable list={timed} timedRows now={now} busy={busy} onRun={(wf) => void runNow(wf)} /> : null}
      </Panel>

      <Panel title="Webhooks, commands and events">
        {workflows && others.length === 0 ? (
          <EmptyState size="sm" icon="radio" text="No webhook, chat command or event triggers." />
        ) : null}
        {others.length > 0 ? <TriggerTable list={others} timedRows={false} now={now} busy={busy} onRun={(wf) => void runNow(wf)} /> : null}
      </Panel>

      <Panel title="Recent runs">
        {workflows && runs.length === 0 ? <EmptyState size="sm" icon="play" text="No workflow has run yet." /> : null}
        {runs.length > 0 ? (
          <ul className="sch-runs">
            {runs.slice(0, 15).map((run) => (
              <li key={run.id}>
                <a href={"#/workflows/" + encodeURIComponent(run.workflow_id) + "/runs/" + run.number}>
                  <span>
                    <Badge tone={RUN_TONES[run.status]} dot>
                      {RUN_WORDS[run.status]}
                    </Badge>
                  </span>
                  <b className="trunc">
                    {names.get(run.workflow_id) || run.workflow_id} · Run #{run.number}
                  </b>
                  <span className="muted trunc">
                    {run.trigger} · {run.error || run.summary || duration((run.ended_at || now) - run.started_at)}
                  </span>
                  <span className="muted num">{ago(run.started_at, now)}</span>
                </a>
              </li>
            ))}
          </ul>
        ) : null}
      </Panel>
    </main>
  );
}
