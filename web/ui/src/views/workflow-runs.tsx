/* eslint-disable react-hooks/set-state-in-effect -- the view refetches the run on every stream change */
import type { ReactNode } from "react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { errorMessage } from "../api/client";
import { answerDecision, cancelRun, getRun, listRuns } from "../api/workflows";
import type {
  Decision,
  NodeRun,
  Run,
  RunDetail,
  WfNode,
  Workflow,
} from "../api/workflows";
import { Ic } from "../ui/icons";
import { Badge, Button, EmptyState, Tabs, useToast } from "../ui/gr8r";
import { WorkflowCanvas } from "./workflow-canvas";
import type { NodeBadge, NodeTone } from "./workflow-canvas";
import { nodeMeta } from "./workflow-catalog";
import {
  AgentFace,
  RUN_WORDS,
  clock,
  dayClock,
  duration,
  prettyValue,
  tokens,
  useNow,
  useWorkflowStream,
} from "./workflow-common";
import { WorkflowHeader } from "./workflow-page";

/* REQ-WF-011 executions (design workflows-3 and -7): the run list, the graph
   with per-node status, the node panel and the guardian decision card. Live
   through API-WF-019 (AC-WF-021). */

const TRIGGER_WORDS: Record<string, string> = {
  manual: "Run button",
  schedule: "Schedule",
  interval: "Interval",
  webhook: "Webhook",
  event: "Event",
  chat: "Chat command",
};

function runLine(run: Run, now: number): { text: string; tone: string } {
  const trigger = TRIGGER_WORDS[run.trigger] || run.trigger;
  const took = duration((run.ended_at || now) - run.started_at);
  switch (run.status) {
    case "running":
      return { text: trigger + " · running " + took, tone: "blue" };
    case "waiting":
      return {
        text:
          "Paused · " +
          (run.waiting_reason === "wait" ? "waiting until " + clock(run.wake_at) : "waiting for you") +
          " · " +
          took,
        tone: "amber",
      };
    case "failed":
      return { text: run.error || "Failed", tone: "red" };
    case "cancelled":
      return { text: trigger + " · cancelled · " + took, tone: "gray" };
    default:
      return { text: trigger + " · " + took + (run.summary ? " · " + run.summary : ""), tone: "" };
  }
}

function dotTone(status: Run["status"]): string {
  return { running: "blue", waiting: "amber", failed: "red", succeeded: "green", cancelled: "gray", queued: "gray" }[status] || "gray";
}

/* Per-node tones and badges from the node runs (design workflows-3). */
function decorate(detail: RunDetail) {
  const tones: Record<string, NodeTone> = {};
  const badges: Record<string, NodeBadge> = {};
  const byNode = new Map<string, NodeRun[]>();
  for (const row of detail.nodes) {
    const list = byNode.get(row.node_id) || [];
    list.push(row);
    byNode.set(row.node_id, list);
  }
  const done = detail.run.status === "succeeded" || detail.run.status === "failed" || detail.run.status === "cancelled";
  for (const node of detail.graph.nodes) {
    const rows = byNode.get(node.id) || [];
    if (rows.length === 0) {
      tones[node.id] = "pending";
      badges[node.id] = { text: done ? "Not run" : "Pending", tone: "gray" };
      continue;
    }
    const items = rows.filter((row) => row.item_index >= 0);
    const main = rows.find((row) => row.item_index < 0);
    if (items.length > 0 && !main) {
      const ok = items.filter((row) => row.status === "succeeded" || row.status === "fallback").length;
      const skipped = items.filter((row) => row.status === "skipped").length;
      const failed = items.filter((row) => row.status === "failed").length;
      const running = items.some((row) => row.status === "running" || row.status === "waiting");
      const took = items.reduce((sum, row) => sum + Math.max(0, (row.ended_at || 0) - row.started_at), 0);
      if (failed > 0) {
        tones[node.id] = "fail";
        badges[node.id] = { text: failed + " failed · " + ok + " ok", tone: "red" };
      } else if (running) {
        tones[node.id] = "run";
        badges[node.id] = { text: "Running · " + items.length + " items", tone: "blue" };
      } else if (skipped > 0) {
        tones[node.id] = "wait";
        badges[node.id] = { text: ok + " of " + items.length + " ok · " + skipped + " skipped", tone: "amber" };
      } else {
        tones[node.id] = "ok";
        badges[node.id] = { text: (ok > 1 ? ok + " runs · " : "") + duration(took), tone: "green" };
      }
      continue;
    }
    const row = main || rows[0]!;
    const tries = (row.attempts || []).length;
    switch (row.status) {
      case "succeeded":
      case "fallback": {
        tones[node.id] = "ok";
        let text = row.ended_at ? duration(row.ended_at - row.started_at) : "Done";
        if (node.type.startsWith("trigger.")) {
          text = row.started_at ? clock(row.started_at, true) : "Fired";
        } else if (node.type === "loop") {
          const output = row.output as { count?: number } | null;
          text = (output && output.count !== undefined ? output.count : 0) + " items";
        } else if (node.type === "if" || node.type === "switch" || node.type === "approval") {
          text = "took " + (row.port || "out");
        }
        badges[node.id] = { text: row.status === "fallback" ? "Fallback" : text, tone: "green" };
        break;
      }
      case "failed":
        tones[node.id] = "fail";
        badges[node.id] = { text: "Failed" + (tries > 1 ? " · " + tries + " tries" : ""), tone: "red" };
        break;
      case "running":
        tones[node.id] = "run";
        badges[node.id] = { text: "Running", tone: "blue" };
        break;
      case "waiting":
        tones[node.id] = "wait";
        badges[node.id] = { text: "Waiting", tone: "amber" };
        break;
      case "skipped":
        tones[node.id] = "skip";
        badges[node.id] = { text: "Skipped", tone: "gray" };
        break;
      default:
        tones[node.id] = "pending";
        badges[node.id] = { text: "Pending", tone: "gray" };
    }
  }
  return { tones, badges };
}

function DecisionCard({
  decision,
  workflow,
  onAnswer,
  now,
}: {
  decision: Decision;
  workflow: Workflow;
  now: number;
  onAnswer: (choice: string) => Promise<void>;
}) {
  const [busy, setBusy] = useState("");
  const act = async (choice: string) => {
    setBusy(choice);
    try {
      await onAnswer(choice);
    } finally {
      setBusy("");
    }
  };
  const pending = decision.status === "pending";
  const fix = decision.fix && Object.keys(decision.fix).length > 0 ? decision.fix : null;
  if (decision.kind === "approval") {
    return (
      <div className={"wf-decision" + (pending ? " ask" : "")}>
        <div className="wf-decision-h">
          <Ic name="circle-check" size={14} />
          <b>{pending ? "Waiting for your approval" : "Approval " + (decision.choice || decision.status)}</b>
        </div>
        <p>{decision.reason}</p>
        {pending ? (
          <div className="wf-decision-acts">
            <Button variant="primary" loading={busy === "approve"} disabled={busy !== ""} onClick={() => void act("approve")}>
              Approve
            </Button>
            <Button loading={busy === "deny"} disabled={busy !== ""} onClick={() => void act("deny")}>
              Deny
            </Button>
          </div>
        ) : null}
      </div>
    );
  }
  const title = pending
    ? "Guardian needs your call"
    : "Guardian decided: " + ({ retry_fix: "retry with a fix", skip: "skip and continue", fallback: "use the fallback", ask: "asked you", stop: "stop the run" }[decision.action] || decision.action);
  return (
    <div className={"wf-decision" + (pending ? " ask" : "")}>
      <div className="wf-decision-h">
        <AgentFace name={workflow.guardian} />
        <b>{title}</b>
      </div>
      <p>{decision.reason}</p>
      {fix ? (
        <div className="wf-fix">
          <b>Proposed fix</b>
          <div className="mono">
            {Object.entries(fix).map(([key, value]) => (
              <div key={key}>
                <span>{key}</span>
                <span>→ {typeof value === "string" ? value : JSON.stringify(value)}</span>
              </div>
            ))}
          </div>
        </div>
      ) : null}
      {pending && decision.persistent ? (
        <p className="muted">I am asking because this changes the saved workflow. I can also apply it to this run only.</p>
      ) : null}
      <span className="wf-decision-meta">
        {workflow.guardian} · {clock(decision.created_at, true)} · {tokens(decision.tokens)} tokens
        {pending && decision.expires_at
          ? " · auto-stops in " + duration(decision.expires_at - now).replace(/ \d+ s$/, "")
          : ""}
        {!pending && decision.choice ? " · you chose " + decision.choice.replace("_", " ") : ""}
      </span>
      {pending ? (
        <div className="wf-decision-acts">
          {fix ? (
            <Button variant="primary" loading={busy === "apply"} disabled={busy !== ""} onClick={() => void act("apply")}>
              Apply and resume
            </Button>
          ) : null}
          {fix ? (
            <Button loading={busy === "run_only"} disabled={busy !== ""} onClick={() => void act("run_only")}>
              This run only
            </Button>
          ) : null}
          <Button loading={busy === "skip"} disabled={busy !== ""} onClick={() => void act("skip")}>
            Skip this node
          </Button>
          <Button variant="danger-ghost" loading={busy === "stop"} disabled={busy !== ""} onClick={() => void act("stop")}>
            Stop run
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function NodePanel({
  node,
  rows,
  decisions,
  workflow,
  onAnswer,
  onClose,
  now,
}: {
  now: number;
  node: WfNode;
  rows: NodeRun[];
  decisions: Decision[];
  workflow: Workflow;
  onAnswer: (decision: Decision, choice: string) => Promise<void>;
  onClose: () => void;
}) {
  const failed = rows.find((row) => row.status === "failed");
  const [tab, setTab] = useState(failed ? "error" : "output");
  const [item, setItem] = useState(0);
  const meta = nodeMeta(node.type);
  const items = rows.filter((row) => row.item_index >= 0).sort((a, b) => a.item_index - b.item_index);
  const main = rows.find((row) => row.item_index < 0);
  const current = items.length > 0 ? items[Math.min(item, items.length - 1)] : main;
  const sub =
    meta.label +
    (items.length > 0 ? " · ran " + items.length + " times" : "") +
    (failed && (failed.attempts || []).length > 1 ? " · failed after " + (failed.attempts || []).length + " tries" : "");
  const tabItems = [
    ...(failed ? [{ value: "error", label: "Error" }] : [{ value: "output", label: "Output" }]),
    { value: "input", label: "Input" },
    { value: "logs", label: "Logs" },
  ];
  return (
    <div className="wf-insp">
      <header className="wf-insp-h">
        <span className={"wf-ico tint-" + meta.tint}>
          <Ic name={meta.icon} size={18} />
        </span>
        <div className="wf-insp-title">
          <b className="trunc">{node.name}</b>
          <span className="muted trunc">{sub}</span>
        </div>
        <button type="button" className="wf-x" aria-label="Close" onClick={onClose}>
          <Ic name="x" size={16} />
        </button>
      </header>
      <Tabs className="wf-insp-tabs" value={tab} onChange={setTab} items={tabItems} />
      <div className="wf-insp-b">
        {rows.length === 0 ? <p className="muted">This node has not run in this run.</p> : null}
        {items.length > 0 ? (
          <>
            <h4 className="wf-h4">Loop items</h4>
            <div className="wf-items">
              {items.map((row, index) => {
                const label =
                  row.input && typeof row.input === "object"
                    ? String(
                        (row.input as Record<string, unknown>).url ||
                          (row.input as Record<string, unknown>).command ||
                          "Item " + (row.item_index + 1)
                      )
                    : "Item " + (row.item_index + 1);
                return (
                  <button
                    type="button"
                    key={row.item_index}
                    className={"wf-item s-" + row.status + (index === item ? " on" : "")}
                    onClick={() => setItem(index)}
                  >
                    <i />
                    <span className="mono trunc">{label.replace(/^https?:\/\//, "")}</span>
                    <span className="num">
                      {row.status === "skipped" || row.status === "failed"
                        ? (row.error ? row.error.slice(0, 24) + " · " : "") + row.status
                        : duration((row.ended_at || now) - row.started_at)}
                    </span>
                  </button>
                );
              })}
            </div>
          </>
        ) : null}
        {decisions.map((decision) => (
          <DecisionCard
            key={decision.id}
            decision={decision}
            workflow={workflow}
            now={now}
            onAnswer={(choice) => onAnswer(decision, choice)}
          />
        ))}
        {current && tab === "error" ? (
          <>
            <h4 className="wf-h4">What failed</h4>
            <pre className="wf-json err">{current.error || failed?.error}</pre>
            {current.attempts && current.attempts.length > 0 ? (
              <>
                <h4 className="wf-h4">Retries · {current.attempts.length} tries</h4>
                <div className="wf-items">
                  {current.attempts.map((attempt) => (
                    <div className="wf-item s-failed" key={attempt.n}>
                      <i />
                      <span className="trunc">
                        Try {attempt.n}
                        {attempt.error ? " · " + attempt.error : ""}
                      </span>
                      <span className="num">{clock(attempt.started_at, true)}</span>
                    </div>
                  ))}
                </div>
              </>
            ) : null}
          </>
        ) : null}
        {current && tab === "output" ? (
          <>
            <h4 className="wf-h4">Output{items.length > 0 ? " · item " + (current.item_index + 1) : ""}</h4>
            <pre className="wf-json">{prettyValue(current.output)}</pre>
          </>
        ) : null}
        {current && tab === "input" ? (
          <>
            <h4 className="wf-h4">Input{items.length > 0 ? " · item " + (current.item_index + 1) : ""}</h4>
            <pre className="wf-json">{prettyValue(current.input)}</pre>
          </>
        ) : null}
        {current && tab === "logs" ? (
          <>
            <h4 className="wf-h4">Logs</h4>
            <pre className="wf-json">{(current.logs || []).join("\n") || "No logs."}</pre>
            {current.session_id ? (
              <a className="wf-link" href={"#/sessions/" + encodeURIComponent(current.session_id)}>
                Open the agent session
              </a>
            ) : null}
          </>
        ) : null}
      </div>
      <footer className="wf-insp-f">
        <Button
          variant="ghost"
          icon="copy"
          onClick={() => {
            void navigator.clipboard?.writeText(prettyValue(current?.output ?? current?.error ?? null));
          }}
        >
          Copy JSON
        </Button>
      </footer>
    </div>
  );
}

export function WorkflowRuns({
  workflow,
  runRef,
}: {
  workflow: Workflow;
  runRef?: string;
  onWorkflow: (workflow: Workflow) => void;
}) {
  const toast = useToast();
  const now = useNow(1000);
  const [runs, setRuns] = useState<Run[] | null>(null);
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [selected, setSelected] = useState("");
  const [busy, setBusy] = useState(false);
  const active = runRef || (runs && runs[0] ? String(runs[0].number) : "");

  const loadRuns = useCallback(() => {
    listRuns(workflow.id, { limit: 50 })
      .then((response) => setRuns(response.runs))
      .catch(() => setRuns((current) => current || []));
  }, [workflow.id]);

  const loadDetail = useCallback(() => {
    if (!active) {
      setDetail(null);
      return;
    }
    getRun(workflow.id, active)
      .then((next) => {
        setDetail(next);
        setSelected((current) => {
          if (current) {
            return current;
          }
          const failed = next.nodes.find((row) => row.status === "failed" || row.status === "waiting");
          return failed ? failed.node_id : "";
        });
      })
      .catch(() => setDetail(null));
  }, [workflow.id, active]);

  useEffect(() => {
    loadRuns();
  }, [loadRuns]);
  useEffect(() => {
    setSelected("");
    loadDetail();
  }, [loadDetail]);

  useWorkflowStream((change) => {
    if (change.workflow_id !== workflow.id) {
      return;
    }
    loadRuns();
    if (!change.run_id || !detail || change.run_id === detail.run.id) {
      loadDetail();
    }
  });

  const deco = useMemo(() => (detail ? decorate(detail) : { tones: {}, badges: {} }), [detail]);

  async function answer(decision: Decision, choice: string) {
    try {
      await answerDecision(workflow.id, decision.id, choice);
      loadDetail();
      loadRuns();
    } catch (error) {
      toast.show({ title: "Could not answer", body: errorMessage(error), tone: "error" });
    }
  }

  async function cancel() {
    if (!detail) {
      return;
    }
    setBusy(true);
    try {
      await cancelRun(workflow.id, detail.run.id);
      loadDetail();
      loadRuns();
    } catch (error) {
      toast.show({ title: "Could not stop the run", body: errorMessage(error), tone: "error" });
    } finally {
      setBusy(false);
    }
  }

  const run = detail?.run;
  const live = run && (run.status === "running" || run.status === "waiting" || run.status === "queued");
  const pendingDecision = detail?.decisions.find((decision) => decision.status === "pending");
  let meta = "";
  if (run) {
    const by = TRIGGER_WORDS[run.trigger] || run.trigger;
    if (run.status === "waiting" && pendingDecision) {
      const node = detail.graph.nodes.find((item) => item.id === pendingDecision.node_id);
      meta = "Run #" + run.number + " · paused at " + (node ? node.name : pendingDecision.node_id) + " · " + clock(pendingDecision.created_at, true);
    } else {
      meta = "Run #" + run.number + " · started " + clock(run.started_at, true) + " by " + by.toLowerCase();
    }
  }

  const selNode = detail?.graph.nodes.find((node) => node.id === selected);
  const sidePills: Record<string, ReactNode> = {};
  if (pendingDecision && pendingDecision.kind === "guardian") {
    sidePills[pendingDecision.node_id] = (
      <span className="wf-pill-ask">
        <AgentFace name={workflow.guardian} />
        Asking you
      </span>
    );
  }
  const guardianCount = detail ? detail.decisions.filter((d) => d.kind === "guardian").length : 0;
  const finalNodes = detail
    ? new Set(
        detail.nodes
          .filter((row) => row.item_index < 0 && ["succeeded", "skipped", "fallback", "failed"].includes(row.status))
          .map((row) => row.node_id)
      ).size
    : 0;

  return (
    <>
      <WorkflowHeader
        name={workflow.name}
        status={workflow.status}
        guardian={workflow.guardian}
        id={workflow.id}
        tab="runs"
        runCount={runs ? runs.length : undefined}
        slots={{
          meta,
          actions: (
            <>
              {live ? (
                <Button loading={busy} onClick={() => void cancel()}>
                  {run && run.status === "waiting" ? "Stop run" : "Cancel run"}
                </Button>
              ) : null}
              <Button as="a" href={"#/workflows/" + encodeURIComponent(workflow.id)} variant="primary">
                Open in editor
              </Button>
            </>
          ),
        }}
      />
      <div className="wf-runs">
        <aside className="wf-runlist" aria-label="Runs">
          <div className="wf-runlist-h">
            <b>Runs</b>
            <Badge tone="gray" dot>
              {runs ? runs.length : 0} · recent
            </Badge>
          </div>
          {runs && runs.length === 0 ? (
            <EmptyState size="sm" icon="play" title="No runs yet" text="Press Run now in the editor, or wait for a trigger." />
          ) : null}
          <ul>
            {(runs || []).map((item) => {
              const line = runLine(item, now);
              return (
                <li key={item.id}>
                  <a
                    href={"#/workflows/" + encodeURIComponent(workflow.id) + "/runs/" + item.number}
                    className={"wf-runrow" + (String(item.number) === active ? " on" : "")}
                    aria-current={String(item.number) === active ? "true" : undefined}
                  >
                    <span className="wf-runrow-top">
                      <span className={"wf-dot " + dotTone(item.status)} aria-label={RUN_WORDS[item.status]} />
                      <b>Run #{item.number}</b>
                      <span className="muted">{dayClock(item.started_at)}</span>
                    </span>
                    <span className={"wf-runrow-sub " + line.tone}>{line.text}</span>
                  </a>
                </li>
              );
            })}
          </ul>
        </aside>
        <div className="wf-stage">
          {detail ? (
            <WorkflowCanvas
              graph={detail.graph}
              selectedId={selected}
              onSelect={setSelected}
              tones={deco.tones}
              badges={deco.badges}
              sidePills={sidePills}
              ariaLabel={"Run " + detail.run.number}
              footer={
                <div className="wf-runfoot">
                  <b>
                    {finalNodes} of {detail.graph.nodes.length} nodes done
                  </b>
                  <span>
                    {run && run.status === "waiting" ? "paused " : ""}
                    {duration(((run && run.ended_at) || now) - (run ? run.started_at : 0))}
                  </span>
                  <span>{tokens(run ? run.tokens : 0)} tokens</span>
                  {pendingDecision ? (
                    <span className="amber">Waiting for your call</span>
                  ) : guardianCount > 0 ? (
                    <span className="violet">
                      {guardianCount} guardian decision{guardianCount > 1 ? "s" : ""}
                    </span>
                  ) : null}
                </div>
              }
            />
          ) : (
            <div className="wf-loading">
              {runs && runs.length === 0 ? null : <Ic name="workflow" size={20} />}
            </div>
          )}
        </div>
        <aside className="wf-side-col">
          {detail && selNode ? (
            <NodePanel
              key={detail.run.id + selNode.id}
              node={selNode}
              rows={detail.nodes.filter((row) => row.node_id === selNode.id)}
              decisions={detail.decisions.filter((decision) => decision.node_id === selNode.id)}
              workflow={workflow}
              now={now}
              onAnswer={answer}
              onClose={() => setSelected("")}
            />
          ) : (
            <div className="wf-side-empty">
              <Ic name="mouse-pointer-click" size={18} />
              <p>Pick a node to see its output, input and logs.</p>
              {detail && detail.decisions.length > 0 ? (
                <div className="wf-side-decisions">
                  {detail.decisions.map((decision) => (
                    <DecisionCard
                      key={decision.id}
                      decision={decision}
                      workflow={workflow}
                      now={now}
                      onAnswer={(choice) => answer(decision, choice)}
                    />
                  ))}
                </div>
              ) : null}
            </div>
          )}
        </aside>
      </div>
    </>
  );
}
