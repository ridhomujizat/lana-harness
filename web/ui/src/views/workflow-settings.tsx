import type { ReactNode } from "react";
import { useCallback, useEffect, useState } from "react";
import { errorMessage } from "../api/client";
import {
  deleteState,
  deleteWorkflow,
  getState,
  pauseWorkflow,
  resumeWorkflow,
  rotateWebhook,
  setState,
  updateWorkflow,
} from "../api/workflows";
import type { FailurePolicy, StateEntry, WfNode, Workflow } from "../api/workflows";
import { Ic } from "../ui/icons";
import {
  Button,
  Input,
  Modal,
  Radio,
  Select,
  Textarea,
  Toggle,
  useToast,
} from "../ui/gr8r";
import { displayLabel, useAgentLabel } from "./agent-label";
import { describeCron, isTrigger, nodeMeta } from "./workflow-catalog";
import {
  AgentFace,
  ago,
  shortValue,
  useAgentNames,
  useWorkflowStream,
} from "./workflow-common";
import { WorkflowHeader } from "./workflow-page";
import { agentOptionIcon } from "./agent-avatar";

/* REQ-WF-011 settings (design workflows-4): general, guardian, triggers,
   locked permissions, state (REQ-WF-006), failure default and delete. */

function Section({
  title,
  text,
  children,
}: {
  title: string;
  text: string;
  children: ReactNode;
}) {
  return (
    <section className="wf-set">
      <div className="wf-set-l">
        <h2>{title}</h2>
        <p>{text}</p>
      </div>
      <div className="wf-set-r">{children}</div>
    </section>
  );
}

function triggerLine(
  node: WfNode,
  workflow: Workflow,
  label: (name: string) => string
): string {
  const p = node.params;
  switch (node.type) {
    case "trigger.schedule":
      return describeCron(String(p.cron || "")) + ", " + String(p.timezone || "local time");
    case "trigger.interval":
      return "Every " + String(p.minutes) + " min";
    case "trigger.webhook":
      return "POST " + workflow.webhook.path + " · header X-Lana-Token";
    case "trigger.chat":
      return "/" + String(p.command) + " in web chat or the Telegram owner chat";
    case "trigger.event":
      return String(p.kind) + (p.agent ? " · " + label(String(p.agent)) : "");
    default:
      return "From the Editor or the Runs tab, with optional JSON input";
  }
}

export function WorkflowSettings({
  workflow,
  onSaved,
}: {
  workflow: Workflow;
  onSaved: (workflow: Workflow) => void;
}) {
  const toast = useToast();
  const agents = useAgentNames();
  const label = useAgentLabel();
  const [name, setName] = useState(workflow.name);
  const [description, setDescription] = useState(workflow.description);
  const [entries, setEntries] = useState<StateEntry[]>([]);
  const [busy, setBusy] = useState("");
  const [guardianOpen, setGuardianOpen] = useState(false);
  const [guardian, setGuardian] = useState(workflow.guardian);
  const [editKey, setEditKey] = useState<string | null>(null);
  const [editValue, setEditValue] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [showToken, setShowToken] = useState(false);
  const policy: FailurePolicy = workflow.failure_default || {};
  const [retries, setRetries] = useState(String(policy.retries ?? 2));
  const [backoff, setBackoff] = useState((policy.backoff_secs || [10, 30]).join(", "));

  const loadState = useCallback(() => {
    getState(workflow.id)
      .then((response) => setEntries(response.entries))
      .catch(() => setEntries([]));
  }, [workflow.id]);
  useEffect(() => {
    loadState();
  }, [loadState]);
  useWorkflowStream((change) => {
    /* A refresh arrives when the stream reconnects after a hidden pause: the
       changes made while the tab was gone were missed. */
    if (change.refresh || (change.workflow_id === workflow.id && change.run_id)) {
      loadState();
    }
  });

  async function patch(body: Parameters<typeof updateWorkflow>[1], label: string) {
    setBusy(label);
    try {
      const next = await updateWorkflow(workflow.id, body);
      onSaved(next);
      toast.show({ title: "Saved", tone: "ok" });
    } catch (error) {
      toast.show({ title: "Could not save", body: errorMessage(error), tone: "error" });
    } finally {
      setBusy("");
    }
  }

  async function toggleActive(on: boolean) {
    setBusy("active");
    try {
      const next = on ? await resumeWorkflow(workflow.id) : await pauseWorkflow(workflow.id);
      onSaved(next);
    } catch (error) {
      toast.show({ title: "Could not change the status", body: errorMessage(error), tone: "error" });
    } finally {
      setBusy("");
    }
  }

  async function toggleTrigger(node: WfNode, on: boolean) {
    const graph = {
      ...workflow.graph,
      nodes: workflow.graph.nodes.map((item) =>
        item.id === node.id ? { ...item, params: { ...item.params, enabled: on } } : item
      ),
    };
    await patch(
      { base_version: workflow.version, graph, summary: (on ? "enable " : "disable ") + node.name },
      "trigger-" + node.id
    );
  }

  async function saveFailure(mode: string) {
    const parsedBackoff = backoff
      .split(",")
      .map((part) => Number(part.trim()))
      .filter((value) => Number.isFinite(value) && value > 0);
    await patch(
      {
        base_version: workflow.version,
        failure_default: {
          mode,
          retries: Math.max(0, Math.min(5, Number(retries) || 0)),
          backoff_secs: parsedBackoff.length > 0 ? parsedBackoff : [10, 30],
        },
      },
      "failure"
    );
  }

  async function saveStateValue() {
    if (editKey === null) {
      return;
    }
    let value: unknown;
    try {
      value = JSON.parse(editValue);
    } catch {
      value = editValue;
    }
    try {
      await setState(workflow.id, editKey, value);
      setEditKey(null);
      loadState();
    } catch (error) {
      toast.show({ title: "Could not save state", body: errorMessage(error), tone: "error" });
    }
  }

  async function remove() {
    setBusy("delete");
    try {
      await deleteWorkflow(workflow.id);
      window.location.hash = "#/workflows";
    } catch (error) {
      toast.show({ title: "Could not delete", body: errorMessage(error), tone: "error" });
      setBusy("");
    }
  }

  const triggers = workflow.graph.nodes.filter((node) => isTrigger(node.type));
  const guarding = 0;
  const guardianAgent = agents.find((agent) => agent.name === workflow.guardian);
  const lock = workflow.lock;
  const allTools = new Set((lock ? lock.tools : []).map((tool) => tool.tool));
  const mode = policy.mode || "guardian";

  return (
    <>
      <WorkflowHeader
        name={workflow.name}
        status={workflow.status}
        guardian={workflow.guardian}
        id={workflow.id}
        tab="settings"
        slots={{
          meta: "Saved · v" + workflow.version,
        }}
      />
      <div className="wf-settings">
        <Section title="General" text="Name and description show in lists, run reports and Telegram.">
          <label className="wf-lbl" htmlFor="wf-set-name">
            Name
          </label>
          <Input id="wf-set-name" value={name} onChange={(event) => setName(event.target.value)} />
          <span className="wf-hint">
            Id: <code>{workflow.id}</code>. Used in webhook URLs.
          </span>
          <label className="wf-lbl" htmlFor="wf-set-desc">
            Description
          </label>
          <Textarea
            id="wf-set-desc"
            rows={2}
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
          <span className="wf-hint">The guardian reads this as the goal of the workflow.</span>
          {name !== workflow.name || description !== workflow.description ? (
            <div className="wf-row-acts">
              <Button
                variant="primary"
                loading={busy === "general"}
                onClick={() =>
                  void patch({ base_version: workflow.version, name, description }, "general")
                }
              >
                Save
              </Button>
            </div>
          ) : null}
          <div className="wf-card wf-toggle-card">
            <Toggle
              checked={workflow.status === "active"}
              disabled={busy === "active" || (!workflow.lock_current && workflow.status !== "active")}
              onChange={(on) => void toggleActive(on)}
              aria-label="Active"
            />
            <div>
              <b>Active</b>
              <p>
                {workflow.lock_current || workflow.status === "active"
                  ? "Runs on its triggers. Pausing keeps the state, the lock and the run history."
                  : "Lock the permissions in the Editor (Activate…) before it can run on its own."}
              </p>
            </div>
          </div>
        </Section>

        <Section
          title="Guardian bot"
          text="One bot owns this workflow. It runs AI steps, decides what to do when a node fails, and edits the workflow through chat."
        >
          <div className="wf-card wf-guardian">
            <AgentFace name={workflow.guardian} size="md" />
            <div>
              <b>{label(workflow.guardian)}</b>
              <p className="muted">
                {guardianAgent && guardianAgent.model ? guardianAgent.model.split("/").pop() + " · " : ""}
                edits via the web assistant{guarding ? " · guarding " + guarding + " workflows" : ""}
              </p>
            </div>
            <Button onClick={() => setGuardianOpen(true)}>Change guardian</Button>
          </div>
          <span className="wf-hint">
            Other bots can still run as nodes (“Call another bot”), but only the guardian can change the
            graph through chat. A new guardian has to accept the permission lock again.
          </span>
        </Section>

        <Section
          title="Triggers"
          text="What starts a run. A workflow can have several; each run records which one fired."
        >
          <div className="wf-card wf-list">
            {triggers.length === 0 ? <p className="muted">No triggers. Add one in the Editor.</p> : null}
            {triggers.map((node) => {
              const meta = nodeMeta(node.type);
              const on = node.params.enabled !== false;
              return (
                <div className="wf-list-row" key={node.id}>
                  <span className={"wf-ico sm tint-" + (on ? "green" : "gray")}>
                    <Ic name={meta.icon} size={16} />
                  </span>
                  <div>
                    <b>{node.name || meta.label}</b>
                    <p className="muted">
                      {on ? "" : "Off · "}
                      {triggerLine(node, workflow, label)}
                    </p>
                  </div>
                  <Toggle
                    checked={on}
                    disabled={busy === "trigger-" + node.id}
                    onChange={(next) => void toggleTrigger(node, next)}
                    aria-label={"Enable " + node.name}
                  />
                </div>
              );
            })}
          </div>
          {triggers.some((node) => node.type === "trigger.webhook") ? (
            <div className="wf-webhook">
              <span className="wf-lbl">Webhook secret</span>
              <code className="mono">{showToken ? workflow.webhook.token : "••••••••••••••••"}</code>
              <Button size="sm" variant="ghost" onClick={() => setShowToken(!showToken)}>
                {showToken ? "Hide" : "Show"}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() =>
                  void rotateWebhook(workflow.id).then(() => {
                    toast.show({ title: "New webhook secret", tone: "ok" });
                  })
                }
              >
                Rotate
              </Button>
            </div>
          ) : null}
          <a className="wf-link" href={"#/workflows/" + encodeURIComponent(workflow.id)}>
            + Add trigger in the Editor
          </a>
        </Section>

        <Section
          title="Locked permissions"
          text="Tools this workflow may use without asking. You approved this list when you activated it. Anything else waits in the approval queue."
        >
          {lock ? (
            <>
              <div className="wf-lockhead">
                <Ic name="lock" size={14} />
                <b>
                  Locked by you on {new Date(lock.locked_at).toLocaleString()} · version {lock.version}
                </b>
                {!workflow.lock_current ? (
                  <Button as="a" href={"#/workflows/" + encodeURIComponent(workflow.id)} size="sm">
                    Review and re-lock
                  </Button>
                ) : null}
              </div>
              <table className="wf-locktable">
                <thead>
                  <tr>
                    <th>Tool</th>
                    <th>Used by</th>
                    <th>Risk</th>
                  </tr>
                </thead>
                <tbody>
                  {lock.tools.map((tool) => (
                    <tr key={tool.tool}>
                      <td className="mono">{tool.tool}</td>
                      <td>{tool.used_by.join(", ")}</td>
                      <td>
                        <span className={"risk r-" + tool.risk}>{tool.risk}</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <span className="wf-hint">
                Not allowed: {["bash", "MCP tools", "other channels"].filter((item) => !allTools.has(item)).join(", ")}. If the
                guardian needs one of them during a run, you get an approval request on Telegram and on
                this page.
              </span>
            </>
          ) : (
            <p className="muted">Not locked yet. Activate the workflow in the Editor to lock its tools.</p>
          )}
        </Section>

        <Section
          title="State"
          text="Kept between runs. Nodes read it as {{ state.key }} and write it with Set state."
        >
          <div className="wf-card wf-statelist">
            {entries.length === 0 ? <p className="muted">No state yet.</p> : null}
            {entries.map((entry) => (
              <button
                type="button"
                className="wf-state-row"
                key={entry.key}
                onClick={() => {
                  setEditKey(entry.key);
                  setEditValue(JSON.stringify(entry.value, null, 2));
                }}
              >
                <span className="mono">{entry.key}</span>
                <span className="mono trunc">{shortValue(entry.value)}</span>
                <span className="muted">{ago(entry.updated_at)}</span>
              </button>
            ))}
          </div>
          <div className="wf-row-acts">
            <Button
              onClick={() => {
                setEditKey("");
                setEditValue("");
              }}
            >
              Edit state
            </Button>
            <Button
              variant="ghost"
              disabled={entries.length === 0}
              onClick={() => void deleteState(workflow.id).then(loadState)}
            >
              Clear state
            </Button>
          </div>
        </Section>

        <Section
          title="When a node fails"
          text="Default for every node. A node can override it in its On failure tab."
        >
          <div className="wf-choice">
            <label className={"wf-card wf-radio" + (mode === "guardian" ? " on" : "")}>
              <div>
                <b>Retry, then guardian decides</b>
                <p>{label(workflow.guardian)} reads the error and skips, continues or stops the run.</p>
              </div>
              <Radio checked={mode === "guardian"} onChange={() => void saveFailure("guardian")} name="wf-mode" />
            </label>
            <label className={"wf-card wf-radio" + (mode === "stop" ? " on" : "")}>
              <div>
                <b>Retry, then stop</b>
                <p>No AI. The run stops with an error and you get an alert.</p>
              </div>
              <Radio checked={mode === "stop"} onChange={() => void saveFailure("stop")} name="wf-mode" />
            </label>
          </div>
          <div className="wf-grid2">
            <div>
              <label className="wf-lbl" htmlFor="wf-retries">
                Default retries
              </label>
              <Input id="wf-retries" type="number" min={0} max={5} value={retries} onChange={(event) => setRetries(event.target.value)} />
            </div>
            <div>
              <label className="wf-lbl" htmlFor="wf-backoff">
                Backoff (seconds)
              </label>
              <Input id="wf-backoff" value={backoff} onChange={(event) => setBackoff(event.target.value)} />
            </div>
          </div>
          <span className="wf-hint">Applies to tool, browser and bot nodes. Approval nodes never retry.</span>
          <div className="wf-row-acts">
            <Button loading={busy === "failure"} onClick={() => void saveFailure(mode)}>
              Save retry policy
            </Button>
          </div>
          <span className="wf-lbl">Alerts</span>
          <div className="wf-card wf-static">Telegram · owner chat</div>
          <span className="wf-hint">Failures, guardian decisions and approval requests go here.</span>
        </Section>

        <Section
          title="Delete workflow"
          text="Removes the graph, its state and all run history. Knowledge notes it saved stay."
        >
          <div>
            <Button variant="danger" onClick={() => setConfirmDelete(true)}>
              Delete workflow
            </Button>
          </div>
        </Section>
      </div>

      {guardianOpen ? (
        <Modal
          title="Change guardian"
          subtitle="The workflow goes back to draft until the new guardian's lock is accepted."
          onClose={() => setGuardianOpen(false)}
          footer={
            <>
              <Button onClick={() => setGuardianOpen(false)}>Cancel</Button>
              <Button
                variant="primary"
                disabled={guardian === workflow.guardian}
                loading={busy === "guardian"}
                onClick={() =>
                  void patch({ base_version: workflow.version, guardian }, "guardian").then(() =>
                    setGuardianOpen(false)
                  )
                }
              >
                Change
              </Button>
            </>
          }
        >
          <Select
            value={guardian}
            onChange={setGuardian}
            options={agents.map((agent) => ({
              value: agent.name,
              label: displayLabel(agent),
              icon: agentOptionIcon(agent.name),
            }))}
            aria-label="Guardian"
          />
        </Modal>
      ) : null}
      {editKey !== null ? (
        <Modal
          title={editKey ? "Edit " + editKey : "Add a state key"}
          subtitle="Values are JSON. Plain text is saved as a string."
          onClose={() => setEditKey(null)}
          footer={
            <>
              {editKey ? (
                <Button
                  variant="danger-ghost"
                  onClick={() =>
                    void deleteState(workflow.id, editKey).then(() => {
                      setEditKey(null);
                      loadState();
                    })
                  }
                >
                  Delete key
                </Button>
              ) : null}
              <Button onClick={() => setEditKey(null)}>Cancel</Button>
              <Button variant="primary" onClick={() => void saveStateValue()} disabled={!editKey}>
                Save
              </Button>
            </>
          }
        >
          {entries.some((entry) => entry.key === editKey) ? null : (
            <Input
              className="mono"
              placeholder="key"
              value={editKey}
              onChange={(event) => setEditKey(event.target.value)}
              aria-label="Key"
            />
          )}
          <Textarea
            className="mono"
            rows={8}
            value={editValue}
            onChange={(event) => setEditValue(event.target.value)}
            aria-label="Value"
          />
        </Modal>
      ) : null}
      {confirmDelete ? (
        <Modal
          title={"Delete " + workflow.name + "?"}
          subtitle="The graph, its state and all run history are removed. This cannot be undone."
          size="sm"
          onClose={() => setConfirmDelete(false)}
          footer={
            <>
              <Button onClick={() => setConfirmDelete(false)}>Cancel</Button>
              <Button variant="danger" loading={busy === "delete"} onClick={() => void remove()}>
                Delete workflow
              </Button>
            </>
          }
        >
          <p className="muted">Knowledge notes it saved stay.</p>
        </Modal>
      ) : null}
    </>
  );
}
