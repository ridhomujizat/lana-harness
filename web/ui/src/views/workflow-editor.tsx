/* eslint-disable react-hooks/set-state-in-effect -- the editor resets its draft when the saved workflow changes */
import { useEffect, useMemo, useState } from "react";
import type { DragEvent as ReactDragEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import {
  activateWorkflow,
  askAssistant,
  createWorkflow,
  getChat,
  lockPreview,
  pauseWorkflow,
  restoreVersion,
  resumeWorkflow,
  startRun,
  updateWorkflow,
} from "../api/workflows";
import type {
  ChatMessage,
  Graph,
  Issue,
  LockTool,
  WfNode,
  Workflow,
} from "../api/workflows";
import { Ic } from "../ui/icons";
import { Button, Modal, Seg, Textarea, useToast } from "../ui/gr8r";
import { WorkflowAssistant } from "./workflow-assistant";
import { WorkflowCanvas, addNodeBelow } from "./workflow-canvas";
import type { NodeBadge, NodeTone } from "./workflow-canvas";
import { GROUPS, NODE_CATALOG, newNodeId, nodeMeta } from "./workflow-catalog";
import { ago, useAgentNames } from "./workflow-common";
import { NodeInspector } from "./workflow-inspector";
import { WorkflowHeader } from "./workflow-page";
import { emptyGraph } from "./workflow-templates";
import type { WorkflowTemplate } from "./workflow-templates";

/* REQ-WF-011 editor (design workflows-2, -5, -6) and REQ-WF-010 assistant. The
   canvas edits a local draft; Save writes a new version. */

function sameGraph(a: Graph, b: Graph): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

function Palette({ onAdd }: { onAdd: (type: string) => void }) {
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  return (
    <aside className="wf-palette" aria-label="Add a node">
      <div className="wf-pal-search">
        <Ic name="search" size={14} />
        <input
          value={query}
          placeholder="Add a node…"
          aria-label="Search nodes"
          onChange={(event) => setQuery(event.target.value)}
        />
      </div>
      <div className="wf-pal-list">
        {GROUPS.map((group) => {
          const items = NODE_CATALOG.filter(
            (meta) => meta.group === group.key && (!q || meta.label.toLowerCase().includes(q))
          );
          if (items.length === 0) {
            return null;
          }
          return (
            <div key={group.key} className="wf-pal-group">
              <span className={"wf-pal-h g-" + group.key}>{group.label}</span>
              {items.map((meta) => (
                <button
                  key={meta.type}
                  type="button"
                  className="wf-pal-item"
                  draggable
                  onDragStart={(event: ReactDragEvent<HTMLButtonElement>) => {
                    event.dataTransfer.setData("application/x-lana-node", meta.type);
                    event.dataTransfer.effectAllowed = "copy";
                  }}
                  onClick={() => onAdd(meta.type)}
                  title="Click to add below the selected node, or drag onto the canvas"
                >
                  <Ic name={meta.icon} size={15} />
                  <span>{meta.label}</span>
                </button>
              ))}
            </div>
          );
        })}
      </div>
    </aside>
  );
}

function makeNode(graph: Graph, type: string, at?: { x: number; y: number }): WfNode {
  const meta = nodeMeta(type);
  return {
    id: newNodeId(graph, meta.label.split(" ·")[0] || type),
    type,
    name: meta.label.split(" ·")[0] || meta.label,
    position: at || { x: 240, y: 0 },
    params: JSON.parse(JSON.stringify(meta.defaults)) as Record<string, unknown>,
  };
}

/* API errors carry graph problems as details[0].issues (contract "Errors"). */
function issuesOf(error: unknown): Issue[] {
  if (!(error instanceof ApiError)) {
    return [];
  }
  const first = error.details[0] as { issues?: Issue[] } | undefined;
  return first && Array.isArray(first.issues) ? first.issues : [];
}

export function WorkflowEditor({
  workflow,
  onSaved,
}: {
  workflow: Workflow | null;
  onSaved: (workflow: Workflow) => void;
}) {
  const toast = useToast();
  const agents = useAgentNames();
  const agentNames = agents.map((agent) => agent.name);
  const [draft, setDraft] = useState<Graph>(() => workflow?.graph || emptyGraph());
  const [name, setName] = useState(workflow?.name || "New workflow");
  const [description, setDescription] = useState(workflow?.description || "");
  const [guardian, setGuardian] = useState(workflow?.guardian || "");
  const [selected, setSelected] = useState("");
  const [panel, setPanel] = useState<"chat" | "node">("chat");
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [busy, setBusy] = useState(false);
  const [saving, setSaving] = useState("");
  const [lockTools, setLockTools] = useState<LockTool[] | null>(null);
  const [lockOpen, setLockOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState("");
  const [issues, setIssues] = useState<Issue[]>([]);

  const saved = workflow?.graph;
  const dirty = workflow ? !sameGraph(draft, workflow.graph) : draft.nodes.length > 0;

  /* Reset the draft whenever a new saved version arrives and nothing is
     edited locally. */
  useEffect(() => {
    if (workflow) {
      setDraft((current) => (saved && !sameGraph(current, saved) && current !== saved ? saved : current));
      setName(workflow.name);
      setDescription(workflow.description);
      setGuardian(workflow.guardian);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workflow?.version, workflow?.id]);

  useEffect(() => {
    if (!guardian && agents.length > 0) {
      setGuardian(agents[0]!.name);
    }
  }, [agents, guardian]);

  useEffect(() => {
    if (!workflow) {
      return;
    }
    let live = true;
    getChat(workflow.id)
      .then((response) => live && setMessages(response.messages))
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [workflow?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!workflow) {
      setLockTools(null);
      return;
    }
    let live = true;
    lockPreview(workflow.id)
      .then((response) => live && setLockTools(response.tools))
      .catch(() => live && setLockTools(null));
    return () => {
      live = false;
    };
  }, [workflow?.id, workflow?.version]); // eslint-disable-line react-hooks/exhaustive-deps

  const selectedNode = draft.nodes.find((node) => node.id === selected);

  const changed = useMemo(() => {
    const last = [...messages].reverse().find((message) => message.change);
    const tones: Record<string, NodeTone> = {};
    const badges: Record<string, NodeBadge> = {};
    if (!last || !last.change || !workflow || last.change.to_version !== workflow.version) {
      return { tones, badges };
    }
    const names = new Set([...last.change.added, ...last.change.changed]);
    for (const node of draft.nodes) {
      if (names.has(node.name) || names.has(node.id)) {
        tones[node.id] = "changed";
        badges[node.id] = { text: "Changed by " + workflow.guardian + " · v" + last.change.to_version, tone: "violet" };
      }
    }
    return { tones, badges };
  }, [messages, workflow, draft.nodes]);

  function select(id: string) {
    setSelected(id);
    if (id) {
      setPanel("node");
    }
  }

  function addType(type: string, at?: { x: number; y: number }) {
    const node = makeNode(draft, type, at);
    const next = at
      ? { ...draft, nodes: [...draft.nodes, node] }
      : addNodeBelow(draft, node, selectedNode);
    setDraft(next);
    select(node.id);
  }

  function updateNode(node: WfNode) {
    setDraft({
      ...draft,
      nodes: draft.nodes.map((item) => (item.id === node.id ? node : item)),
    });
  }

  function deleteNode(id: string) {
    setDraft({
      nodes: draft.nodes
        .filter((node) => node.id !== id)
        .map((node) => (node.parent === id ? { ...node, parent: undefined } : node)),
      edges: draft.edges.filter((edge) => edge.from !== id && edge.to !== id),
    });
    setSelected("");
    setPanel("chat");
  }

  async function ensureCreated(graph: Graph, wantName?: string): Promise<Workflow> {
    if (workflow) {
      return workflow;
    }
    let title = (wantName || name).trim() || "New workflow";
    for (let attempt = 0; attempt < 5; attempt += 1) {
      try {
        const created = await createWorkflow({
          name: title,
          description,
          guardian: guardian || agentNames[0] || "default",
          graph: graph.nodes.length > 0 ? graph : undefined,
        });
        onSaved(created);
        window.location.hash = "#/workflows/" + encodeURIComponent(created.id);
        return created;
      } catch (error) {
        if (error instanceof ApiError && error.code === "workflow_exists") {
          title = (wantName || name).trim() + " " + (attempt + 2);
          continue;
        }
        throw error;
      }
    }
    throw new Error("Could not pick a free workflow name.");
  }

  async function save(): Promise<Workflow | null> {
    setSaving("save");
    setIssues([]);
    try {
      if (!workflow) {
        return await ensureCreated(draft);
      }
      const next = await updateWorkflow(workflow.id, {
        base_version: workflow.version,
        graph: draft,
        summary: "edited on the canvas",
      });
      onSaved(next);
      setDraft(next.graph);
      toast.show({ title: "Saved v" + next.version, tone: "ok" });
      return next;
    } catch (error) {
      setIssues(issuesOf(error));
      toast.show({ title: "Could not save", body: errorMessage(error), tone: "error" });
      return null;
    } finally {
      setSaving("");
    }
  }

  async function run() {
    let current = workflow;
    if (dirty || !current) {
      current = await save();
      if (!current) {
        return;
      }
    }
    setSaving("run");
    try {
      const started = await startRun(current.id);
      window.location.hash =
        "#/workflows/" + encodeURIComponent(current.id) + "/runs/" + started.number;
    } catch (error) {
      toast.show({ title: "Could not start a run", body: errorMessage(error), tone: "error" });
    } finally {
      setSaving("");
    }
  }

  async function activate() {
    let current = workflow;
    if (dirty || !current) {
      current = await save();
      if (!current) {
        return;
      }
    }
    setSaving("activate");
    try {
      const next = await activateWorkflow(current.id, current.version);
      onSaved(next);
      setLockOpen(false);
      toast.show({ title: "Locked and active", body: next.lock ? next.lock.tools.length + " tools locked" : undefined, tone: "ok" });
    } catch (error) {
      setIssues(issuesOf(error));
      toast.show({ title: "Could not activate", body: errorMessage(error), tone: "error" });
    } finally {
      setSaving("");
    }
  }

  async function togglePause() {
    if (!workflow) {
      return;
    }
    setSaving("pause");
    try {
      const next =
        workflow.status === "active"
          ? await pauseWorkflow(workflow.id)
          : await resumeWorkflow(workflow.id);
      onSaved(next);
    } catch (error) {
      toast.show({ title: "Could not change the status", body: errorMessage(error), tone: "error" });
    } finally {
      setSaving("");
    }
  }

  async function send(text: string) {
    const owner: ChatMessage = { id: -Date.now(), role: "owner", text, created_at: Date.now() };
    setMessages((list) => [...list, owner]);
    setBusy(true);
    try {
      let current = workflow;
      if (!current) {
        current = await ensureCreated(draft, text.split(/\s+/).slice(0, 4).join(" "));
      } else if (dirty) {
        current = await save();
        if (!current) {
          throw new Error("Save the canvas first.");
        }
      }
      const response = await askAssistant(current.id, text, current.version);
      setMessages((list) => [...list, response.message]);
      onSaved(response.workflow);
      setDraft(response.workflow.graph);
    } catch (error) {
      setMessages((list) => list.filter((message) => message.id !== owner.id));
      throw error;
    } finally {
      setBusy(false);
    }
  }

  async function undo(version: number) {
    if (!workflow) {
      return;
    }
    try {
      const next = await restoreVersion(workflow.id, version);
      onSaved(next);
      setDraft(next.graph);
      setMessages((list) => [
        ...list,
        { id: -Date.now(), role: "guardian", text: "Restored v" + version + " as v" + next.version + ".", created_at: Date.now() },
      ]);
    } catch (error) {
      toast.show({ title: "Could not undo", body: errorMessage(error), tone: "error" });
    }
  }

  async function applyTemplate(template: WorkflowTemplate) {
    setDraft(template.graph);
    setName(template.name);
    setDescription(template.description);
    if (workflow) {
      return;
    }
    try {
      await ensureCreated(template.graph, template.name);
    } catch (error) {
      toast.show({ title: "Could not create the workflow", body: errorMessage(error), tone: "error" });
    }
  }

  function importJSON() {
    try {
      const parsed = JSON.parse(importText) as Graph;
      if (!parsed || !Array.isArray(parsed.nodes) || !Array.isArray(parsed.edges)) {
        throw new Error("The JSON needs nodes and edges arrays.");
      }
      setDraft(parsed);
      setImportOpen(false);
      setImportText("");
    } catch (error) {
      toast.show({ title: "Could not import", body: errorMessage(error), tone: "error" });
    }
  }

  const status = workflow?.status || "draft";
  let meta: string;
  if (!workflow) {
    meta = "Not saved yet";
  } else if (dirty) {
    meta = "Unsaved changes";
  } else {
    const who = workflow.version_author === "guardian" ? workflow.guardian : "you";
    meta =
      (status === "active" ? "Saved" : "Draft") +
      " · v" +
      workflow.version +
      " · " +
      (workflow.version_author === "guardian" ? "changed" : "edited") +
      " by " +
      who +
      ", " +
      ago(workflow.version_at || workflow.updated_at);
  }

  let actions;
  if (!workflow) {
    actions = (
      <>
        <Button as="a" href="#/workflows">
          Cancel
        </Button>
        <Button variant="primary" loading={saving === "save"} onClick={() => void save()}>
          Save draft
        </Button>
      </>
    );
  } else if (dirty) {
    actions = (
      <>
        <Button onClick={() => setDraft(workflow.graph)}>Discard</Button>
        <Button variant="primary" loading={saving === "save"} onClick={() => void save()}>
          Save
        </Button>
      </>
    );
  } else if (status === "active") {
    actions = (
      <>
        <Button loading={saving === "pause"} onClick={() => void togglePause()}>
          Pause
        </Button>
        <Button variant="primary" loading={saving === "run"} onClick={() => void run()}>
          Run now
        </Button>
      </>
    );
  } else {
    actions = (
      <>
        {status === "paused" || status === "error" ? (
          workflow.lock_current ? (
            <Button loading={saving === "pause"} onClick={() => void togglePause()}>
              Resume
            </Button>
          ) : null
        ) : null}
        <Button loading={saving === "run"} onClick={() => void run()}>
          Test run
        </Button>
        {status === "draft" || !workflow.lock_current ? (
          <Button variant="primary" onClick={() => setLockOpen(true)}>
            Activate…
          </Button>
        ) : null}
      </>
    );
  }

  return (
    <>
      <WorkflowHeader
        name={name}
        status={status}
        guardian={guardian || "—"}
        id={workflow?.id || "new"}
        tab="editor"
        isNew={!workflow}
        slots={{ meta, actions }}
      />
      <div className="wf-editor">
        <div className="wf-stage">
          <Palette onAdd={(type) => addType(type)} />
          <WorkflowCanvas
            graph={draft}
            selectedId={selected}
            onSelect={select}
            onChange={setDraft}
            onDropType={(type, at) => addType(type, at)}
            tones={changed.tones}
            badges={changed.badges}
            ariaLabel="Workflow editor canvas"
            insetLeft={230}
            empty={
              <div className="wf-empty-card">
                <span className="wf-ico">
                  <Ic name="workflow" size={18} />
                </span>
                <b>Canvas masih kosong</b>
                <p>Ceritakan ke {guardian || "guardian"} di panel chat, atau tarik node dari palet. Node baru muncul di sini.</p>
              </div>
            }
          />
          {issues.length > 0 ? (
            <div className="wf-issuebar" role="alert">
              <Ic name="circle-alert" size={14} />
              <span>
                {issues
                  .slice(0, 3)
                  .map((issue) => (issue.node_id ? issue.node_id + ": " : "") + issue.message)
                  .join(" · ")}
                {issues.length > 3 ? " · +" + (issues.length - 3) + " more" : ""}
              </span>
              <button type="button" className="wf-x" aria-label="Dismiss" onClick={() => setIssues([])}>
                <Ic name="x" size={14} />
              </button>
            </div>
          ) : null}
        </div>
        <aside className="wf-side-col">
          <div className="wf-side-h">
            {panel === "chat" || !selectedNode ? (
              <div className="wf-side-title">
                <b>Workflow assistant</b>
                <span className="muted">{guardian || "—"} · guardian of this workflow</span>
              </div>
            ) : (
              <div className="wf-side-title">
                <b>Node</b>
                <span className="muted">Edit the selected node</span>
              </div>
            )}
            <Seg
              value={panel === "node" && selectedNode ? "node" : "chat"}
              onChange={(value) => setPanel(value as "chat" | "node")}
              options={[
                { value: "chat", label: "Chat" },
                { value: "node", label: "Node", disabled: !selectedNode },
              ]}
            />
          </div>
          {panel === "node" && selectedNode ? (
            <NodeInspector
              key={selectedNode.id}
              workflowId={workflow?.id || ""}
              graph={draft}
              node={selectedNode}
              agents={agentNames}
              onChange={updateNode}
              onDelete={() => deleteNode(selectedNode.id)}
              onClose={() => {
                setSelected("");
                setPanel("chat");
              }}
            />
          ) : (
            <WorkflowAssistant
              workflow={workflow}
              guardian={guardian || "default"}
              agents={agents}
              messages={messages}
              busy={busy}
              lockTools={lockTools}
              onGuardian={setGuardian}
              onSend={send}
              onUndo={(version) => void undo(version)}
              onActivate={() => void activate()}
              onReviewLock={() => setLockOpen(true)}
              onTemplate={(template) => void applyTemplate(template)}
              onTestRun={() => void run()}
              onImport={() => setImportOpen(true)}
            />
          )}
        </aside>
      </div>
      {lockOpen ? (
        <Modal
          title="Lock permissions and activate"
          subtitle="The workflow may use these tools without asking. Anything else waits in the approval queue."
          onClose={() => setLockOpen(false)}
          footer={
            <>
              <Button onClick={() => setLockOpen(false)}>Cancel</Button>
              <Button variant="primary" loading={saving === "activate"} onClick={() => void activate()}>
                Lock and activate
              </Button>
            </>
          }
        >
          <table className="wf-locktable">
            <thead>
              <tr>
                <th>Tool</th>
                <th>Used by</th>
                <th>Risk</th>
              </tr>
            </thead>
            <tbody>
              {(dirty ? null : lockTools) === null ? (
                <tr>
                  <td colSpan={3} className="muted">
                    Save first; the list is computed from the saved graph.
                  </td>
                </tr>
              ) : null}
              {(dirty ? [] : lockTools || []).map((tool) => (
                <tr key={tool.tool}>
                  <td className="mono">{tool.tool}</td>
                  <td>{tool.used_by.join(", ")}</td>
                  <td>
                    <span className={"risk r-" + tool.risk}>{tool.risk}</span>
                  </td>
                </tr>
              ))}
              {!dirty && lockTools && lockTools.length === 0 ? (
                <tr>
                  <td colSpan={3} className="muted">
                    No tools needed.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </Modal>
      ) : null}
      {importOpen ? (
        <Modal
          title="Import a graph"
          subtitle='Paste JSON with "nodes" and "edges" (the format of this editor).'
          onClose={() => setImportOpen(false)}
          footer={
            <>
              <Button onClick={() => setImportOpen(false)}>Cancel</Button>
              <Button variant="primary" onClick={importJSON}>
                Import
              </Button>
            </>
          }
        >
          <Textarea
            className="mono"
            rows={12}
            value={importText}
            onChange={(event) => setImportText(event.target.value)}
            aria-label="Graph JSON"
          />
        </Modal>
      ) : null}
    </>
  );
}
