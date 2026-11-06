import { useCallback, useEffect, useMemo, useState } from "react";
import { answerApproval } from "../api/approvals";
import { errorMessage } from "../api/client";
import { answerDecision, listWorkflows } from "../api/workflows";
import type { Attention, WorkflowList, WorkflowSummary } from "../api/workflows";
import { Ic } from "../ui/icons";
import {
  Badge,
  Button,
  EmptyState,
  ErrorState,
  SearchInput,
  Seg,
  Skeleton,
  useToast,
} from "../ui/gr8r";
import {
  AgentFace,
  WorkflowBadge,
  ago,
  pct,
  useWorkflowStream,
} from "./workflow-common";

/* REQ-WF-011 list (design workflows-1): stats, search, status filter, the
   workflow table and Needs attention. */

type Filter = "all" | "active" | "paused" | "failing";

function matches(row: WorkflowSummary, filter: Filter, query: string): boolean {
  if (filter === "active" && row.status !== "active") {
    return false;
  }
  if (filter === "paused" && row.status !== "paused") {
    return false;
  }
  if (filter === "failing" && row.status !== "error") {
    return false;
  }
  if (!query) {
    return true;
  }
  const text = (row.name + " " + row.description + " " + row.guardian).toLowerCase();
  return text.includes(query.toLowerCase());
}

function lastRunText(row: WorkflowSummary): string {
  if (row.running) {
    return "Running now";
  }
  if (row.waiting) {
    return "Waiting for you";
  }
  if (!row.last_run) {
    return "Never";
  }
  return ago(row.last_run.ended_at || row.last_run.started_at);
}

function Faces({ row }: { row: WorkflowSummary }) {
  const bots = row.bots && row.bots.length > 0 ? row.bots : [row.guardian];
  return (
    <span className="wf-faces">
      {bots.slice(0, 3).map((bot) => (
        <AgentFace key={bot} name={bot} />
      ))}
    </span>
  );
}

const ACTION_WORDS: Record<string, string> = {
  retry_fix: "retried with a fix",
  skip: "skipped a step",
  fallback: "used the fallback",
  ask: "asked you",
  stop: "stopped the run",
};

function AttentionRow({
  item,
  onDone,
}: {
  item: Attention;
  onDone: () => void;
}) {
  const toast = useToast();
  const [busy, setBusy] = useState("");

  async function act(choice: string) {
    setBusy(choice);
    try {
      if (item.kind === "tool_approval") {
        await answerApproval(item.id, choice === "deny" ? "rejected" : "allowed_once");
      } else {
        await answerDecision(item.workflow_id, item.id, choice);
      }
      onDone();
    } catch (error) {
      toast.show({ title: "Could not answer", body: errorMessage(error), tone: "error" });
    } finally {
      setBusy("");
    }
  }

  const runHref =
    "#/workflows/" + encodeURIComponent(item.workflow_id) + "/runs/" + item.run_number;
  if (item.kind === "guardian") {
    return (
      <li className="wf-att">
        <span className="wf-att-ico red">
          <Ic name="triangle-alert" size={16} />
        </span>
        <div className="wf-att-text">
          <b>
            {item.workflow_name}:{" "}
            {item.status === "pending"
              ? (item.node_name || item.node_id) + " needs your call"
              : "the guardian " + (ACTION_WORDS[item.action] || item.action) + " at " + (item.node_name || item.node_id)}
          </b>
          <p>Guardian diagnosis: {item.reason}</p>
        </div>
        <div className="wf-att-acts">
          <Button as="a" href={runHref} variant="secondary">
            Open run
          </Button>
        </div>
      </li>
    );
  }
  const title =
    item.kind === "tool_approval"
      ? (item.workflow_name || "A workflow") + " wants a tool outside its lock"
      : item.workflow_name + " is waiting for your approval";
  return (
    <li className="wf-att">
      <span className="wf-att-ico amber">
        <Ic name="lock" size={16} />
      </span>
      <div className="wf-att-text">
        <b>{title}</b>
        <p>
          {item.node_name ? "Node “" + item.node_name + "”: " : ""}
          {item.reason} Asked {ago(item.created_at)}, also sent to Telegram.
        </p>
      </div>
      <div className="wf-att-acts">
        <Button
          variant="secondary"
          loading={busy === "deny"}
          disabled={busy !== ""}
          onClick={() => void act("deny")}
        >
          Deny
        </Button>
        <Button
          variant="primary"
          loading={busy === "approve"}
          disabled={busy !== ""}
          onClick={() => void act("approve")}
        >
          Approve once
        </Button>
      </div>
    </li>
  );
}

export function WorkflowsPage() {
  const [data, setData] = useState<WorkflowList | null>(null);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [query, setQuery] = useState("");

  const load = useCallback(() => {
    listWorkflows()
      .then((next) => {
        setData(next);
        setError("");
      })
      .catch((err) => setError(errorMessage(err)));
  }, []);

  useEffect(() => {
    load();
  }, [load]);
  useWorkflowStream(() => load());

  const rows = useMemo(
    () => (data ? data.workflows.filter((row) => matches(row, filter, query)) : []),
    [data, filter, query]
  );

  if (error && !data) {
    return (
      <main id="main" className="page wide wf-page">
        <ErrorState title="Could not load workflows" text={error} action={<Button onClick={load}>Try again</Button>} />
      </main>
    );
  }

  const stats = data?.stats;
  const all = data?.workflows || [];
  const count = (status: string) => all.filter((row) => row.status === status).length;
  const delta =
    stats && stats.success_7d !== null && stats.success_prev_7d !== null
      ? (stats.success_7d - stats.success_prev_7d) * 100
      : null;

  return (
    <main id="main" className="page wide wf-page">
      <header className="ph">
        <div>
          <h1>Workflows</h1>
          <p>Automations run and watched by a guardian bot. Every run keeps its steps.</p>
        </div>
        <div className="acts">
          <Button as="a" href="#/workflows/new" variant="primary" icon="plus">
            New workflow
          </Button>
        </div>
      </header>

      <div className="wf-stats">
        {stats ? (
          <>
            <div className="wf-stat">
              <span className="k">Active workflows</span>
              <span className="v num">{stats.active}</span>
              <span className="d">
                of {stats.total} · {stats.paused} paused
              </span>
            </div>
            <div className="wf-stat">
              <span className="k">Waiting on you</span>
              <span className={"v num" + (stats.waiting_on_you > 0 ? " warn" : "")}>
                {stats.waiting_on_you}
              </span>
              <span className="d">
                {stats.approvals} approval · {stats.decisions} guardian report
              </span>
            </div>
            <div className="wf-stat">
              <span className="k">Success rate, 7 days</span>
              <span className="v num">{pct(stats.success_7d, 1)}</span>
              <span className="d">
                {delta === null
                  ? "no runs last week"
                  : (delta >= 0 ? "+" : "") + delta.toFixed(1) + " pts vs last week"}
              </span>
            </div>
            <div className="wf-stat">
              <span className="k">Runs today</span>
              <span className="v num">{stats.runs_today}</span>
              <span className="d">
                {stats.runs_today_ok} ok · {stats.runs_today_failed} failed ·{" "}
                {stats.runs_today_running} running
              </span>
            </div>
          </>
        ) : (
          [0, 1, 2, 3].map((index) => (
            <div className="wf-stat" key={index}>
              <Skeleton width="60%" />
              <Skeleton width="30%" height={26} />
              <Skeleton width="70%" />
            </div>
          ))
        )}
      </div>

      <div className="wf-toolbar">
        <SearchInput
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search workflows"
          kbd={null}
          aria-label="Search workflows"
        />
        <Seg
          value={filter}
          onChange={(value) => setFilter(value as Filter)}
          options={[
            { value: "all", label: "All", count: all.length },
            { value: "active", label: "Active", count: count("active") },
            { value: "paused", label: "Paused", count: count("paused") },
            { value: "failing", label: "Failing", count: count("error") },
          ]}
        />
        <span className="wf-toolbar-meta">
          {all.length} workflows · {count("active")} active
        </span>
      </div>

      <section className="wf-table" aria-label="Workflows">
        <div className="wf-tr wf-th" role="row">
          <span>Workflow</span>
          <span>Trigger</span>
          <span>Status</span>
          <span className="num">Runs 7d</span>
          <span className="num">Success</span>
          <span>Last run</span>
          <span />
        </div>
        {!data
          ? [0, 1, 2].map((index) => (
              <div className="wf-tr" key={index}>
                <Skeleton width="70%" />
              </div>
            ))
          : null}
        {data && rows.length === 0 ? (
          <EmptyState
            icon="workflow"
            title={all.length === 0 ? "No workflows yet" : "Nothing matches"}
            text={
              all.length === 0
                ? "Describe a routine to a guardian bot, or drag nodes onto a canvas."
                : "Try another search or filter."
            }
            action={
              all.length === 0 ? (
                <Button as="a" href="#/workflows/new" variant="primary">
                  New workflow
                </Button>
              ) : undefined
            }
          />
        ) : null}
        {rows.map((row) => {
          const trigger =
            row.triggers.find((item) => item.enabled && item.type !== "trigger.manual") ||
            row.triggers.find((item) => item.enabled) ||
            row.triggers[0];
          return (
            <a
              key={row.id}
              className="wf-tr"
              href={"#/workflows/" + encodeURIComponent(row.id)}
              role="row"
            >
              <span className="wf-name-cell">
                <Faces row={row} />
                <span className="wf-name-text">
                  <span className="wf-name">
                    <b className="trunc">{row.name}</b>
                    {row.running ? (
                      <Badge tone="accent" dot>
                        Running
                      </Badge>
                    ) : null}
                    {row.bots && row.bots.length > 1 ? (
                      <Badge tone="violet" dot>
                        Calls {row.bots.length - 1} bot{row.bots.length > 2 ? "s" : ""}
                      </Badge>
                    ) : null}
                  </span>
                  <span className="wf-sub trunc">
                    Guarded by {row.guardian} · {row.node_count} nodes
                  </span>
                </span>
              </span>
              <span className="wf-trigger">
                <span>{trigger ? trigger.label : "Manual"}</span>
                <small className="trunc">{trigger ? trigger.detail : "Run button"}</small>
              </span>
              <span>
                <WorkflowBadge status={row.status} waiting={row.waiting} />
              </span>
              <span className="num">{row.runs_7d}</span>
              <span className="num">{pct(row.success_7d)}</span>
              <span className="wf-last">{lastRunText(row)}</span>
              <span className="wf-chev">
                <Ic name="chevron-right" size={16} />
              </span>
            </a>
          );
        })}
      </section>

      {data && data.attention.length > 0 ? (
        <section className="wf-attention" aria-label="Needs attention">
          <h2>
            Needs attention
            <Badge tone="amber" dot>
              {data.attention.length}
            </Badge>
          </h2>
          <ul>
            {data.attention.map((item) => (
              <AttentionRow key={item.kind + item.id} item={item} onDone={load} />
            ))}
          </ul>
        </section>
      ) : null}
    </main>
  );
}
