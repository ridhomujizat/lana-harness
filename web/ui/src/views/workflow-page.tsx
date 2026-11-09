/* eslint-disable react-hooks/set-state-in-effect -- the page resets its load state before each request */
import { useCallback, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { errorMessage } from "../api/client";
import { getWorkflow } from "../api/workflows";
import type { Workflow } from "../api/workflows";
import { Ic } from "../ui/icons";
import { Button, ErrorState, Skeleton } from "../ui/gr8r";
import { WorkflowEditor } from "./workflow-editor";
import { WorkflowRuns } from "./workflow-runs";
import { WorkflowSettings } from "./workflow-settings";
import { AgentFace, WorkflowBadge, useWorkflowStream } from "./workflow-common";
import { useAgentLabel } from "./agent-label";

/* The workflow shell of REQ-WF-011: breadcrumb, status, guardian chip and the
   Editor / Executions / Settings tabs. Each tab renders its own action buttons
   through the header slot. */

export type WorkflowTab = "editor" | "runs" | "settings";

export interface HeaderSlots {
  meta?: ReactNode;
  actions?: ReactNode;
}

export function WorkflowHeader({
  name,
  status,
  guardian,
  id,
  tab,
  runCount,
  slots,
  isNew,
}: {
  name: string;
  status: Workflow["status"];
  guardian: string;
  id: string;
  tab: WorkflowTab;
  runCount?: number;
  slots: HeaderSlots;
  isNew?: boolean;
}) {
  const label = useAgentLabel();
  const base = "#/workflows/" + encodeURIComponent(id);
  const tabs: { key: WorkflowTab; label: string; href: string }[] = [
    { key: "editor", label: "Editor", href: isNew ? "#/workflows/new" : base },
    { key: "runs", label: "Executions", href: base + "/runs" },
    { key: "settings", label: "Settings", href: base + "/settings" },
  ];
  return (
    <header className="wf-head">
      <div className="wf-head-row">
        <div className="wf-crumb">
          <h1 className="trunc">{name}</h1>
        </div>
        <WorkflowBadge status={status} />
        <span className="wf-guard-chip">
          <AgentFace name={guardian} />
          Guardian: {label(guardian)}
        </span>
        <span className="wf-head-meta">{slots.meta}</span>
        <div className="wf-head-acts">{slots.actions}</div>
      </div>
      <nav className="wf-tabs" aria-label="Workflow">
        {tabs.map((item) =>
          isNew && item.key !== "editor" ? (
            <span key={item.key} className="wf-tab off" aria-disabled="true">
              {item.label}
            </span>
          ) : (
            <a
              key={item.key}
              href={item.href}
              className={"wf-tab" + (tab === item.key ? " on" : "")}
              aria-current={tab === item.key ? "page" : undefined}
            >
              {item.label}
              {item.key === "runs" && runCount ? <span className="cnt">{runCount}</span> : null}
            </a>
          )
        )}
      </nav>
    </header>
  );
}

export function WorkflowPage({
  id,
  tab,
  run,
}: {
  id?: string;
  tab: WorkflowTab;
  run?: string;
}) {
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [error, setError] = useState("");
  const isNew = !id;

  const load = useCallback(() => {
    if (!id) {
      return;
    }
    getWorkflow(id)
      .then((next) => {
        setWorkflow(next);
        setError("");
      })
      .catch((err) => setError(errorMessage(err)));
  }, [id]);

  useEffect(() => {
    setWorkflow(null);
    load();
  }, [load]);

  useWorkflowStream((change) => {
    if (!id) {
      return;
    }
    /* A refresh arrives when the stream reconnects after a hidden pause: the
       changes made while the tab was gone were missed. */
    if (change.refresh || (change.workflow_id === id && !change.run_id)) {
      load();
    }
  });

  if (isNew) {
    return (
      <main id="main" className="page flush wf-shell">
        <WorkflowEditor workflow={null} onSaved={setWorkflow} />
      </main>
    );
  }
  if (error && !workflow) {
    return (
      <main id="main" className="page wf-page">
        <ErrorState
          title="Could not open this workflow"
          text={error}
          action={
            <Button as="a" href="#/workflows" icon="arrow-left">
              All workflows
            </Button>
          }
        />
      </main>
    );
  }
  if (!workflow) {
    return (
      <main id="main" className="page flush wf-shell" aria-busy="true">
        <div className="wf-head">
          <Skeleton width={280} height={20} />
        </div>
        <div className="wf-loading">
          <Ic name="workflow" size={20} />
        </div>
      </main>
    );
  }
  return (
    <main id="main" className="page flush wf-shell">
      {tab === "editor" ? (
        <WorkflowEditor workflow={workflow} onSaved={setWorkflow} />
      ) : null}
      {tab === "runs" ? (
        <WorkflowRuns workflow={workflow} runRef={run} onWorkflow={setWorkflow} />
      ) : null}
      {tab === "settings" ? (
        <WorkflowSettings workflow={workflow} onSaved={setWorkflow} />
      ) : null}
    </main>
  );
}
