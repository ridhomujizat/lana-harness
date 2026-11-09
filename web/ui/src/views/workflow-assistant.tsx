import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { errorMessage } from "../api/client";
import type { Agent } from "../api/agents";
import type { ChatMessage, LockTool, Workflow } from "../api/workflows";
import { Ic } from "../ui/icons";
import { Button, Select } from "../ui/gr8r";
import { AgentFace } from "./workflow-common";
import { displayLabel, useAgentLabel } from "./agent-label";
import { TEMPLATES } from "./workflow-templates";
import type { WorkflowTemplate } from "./workflow-templates";
import { agentOptionIcon } from "./agent-avatar";

/* The workflow assistant column of REQ-WF-010 (design workflows-5 and -6):
   chat with the guardian, change cards with a diff and Undo, the activation
   card that locks permissions, and on a new workflow the guardian picker and
   idea cards. */

interface AssistantProps {
  workflow: Workflow | null;
  guardian: string;
  agents: Agent[];
  messages: ChatMessage[];
  busy: boolean;
  lockTools: LockTool[] | null;
  onGuardian?: (name: string) => void;
  onSend: (text: string) => Promise<void>;
  onUndo: (version: number) => void;
  onActivate: () => void;
  onReviewLock: () => void;
  onTemplate?: (template: WorkflowTemplate) => void;
  onTestRun?: () => void;
  onImport?: () => void;
}

function ChangeCard({
  message,
  onUndo,
  latest,
}: {
  message: ChatMessage;
  onUndo: (version: number) => void;
  latest: boolean;
}) {
  const change = message.change;
  if (!change) {
    return null;
  }
  const first = change.from_version === 0;
  return (
    <div className="wf-change">
      <div className="wf-change-h">
        <b>
          {first
            ? "Draft v" + change.to_version
            : "v" + change.from_version + " → v" + change.to_version}
          {change.summary ? " · " + change.summary : ""}
        </b>
        {!first && latest ? (
          <button type="button" className="wf-link" onClick={() => onUndo(change.from_version)}>
            Undo
          </button>
        ) : null}
      </div>
      <div className="wf-diff mono">
        {change.added.map((name) => (
          <div className="add" key={"a" + name}>
            + {name}
          </div>
        ))}
        {change.changed.map((name) => (
          <div className="chg" key={"c" + name}>
            ~ {name}
          </div>
        ))}
        {change.removed.map((name) => (
          <div className="del" key={"r" + name}>
            − {name}
          </div>
        ))}
        {change.added.length + change.changed.length + change.removed.length === 0 ? (
          <div className="muted">layout only</div>
        ) : null}
      </div>
    </div>
  );
}

export function WorkflowAssistant({
  workflow,
  guardian,
  agents,
  messages,
  busy,
  lockTools,
  onGuardian,
  onSend,
  onUndo,
  onActivate,
  onReviewLock,
  onTemplate,
  onTestRun,
  onImport,
}: AssistantProps) {
  const label = useAgentLabel();
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const listRef = useRef<HTMLDivElement>(null);
  const isNew = workflow === null;

  useEffect(() => {
    const list = listRef.current;
    if (list) {
      list.scrollTop = list.scrollHeight;
    }
  }, [messages.length, busy]);

  async function send(value?: string) {
    const body = (value ?? text).trim();
    if (!body || busy) {
      return;
    }
    setError("");
    setText("");
    try {
      await onSend(body);
    } catch (err) {
      setError(errorMessage(err));
      setText(body);
    }
  }

  function onKey(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void send();
    }
  }

  const lastChangeId = [...messages].reverse().find((message) => message.change)?.id;
  const needsLock =
    workflow !== null &&
    (workflow.status === "draft" || !workflow.lock_current) &&
    workflow.graph.nodes.length > 1 &&
    lockTools !== null;

  return (
    <div className="wf-asst">
      <div className="wf-asst-list" ref={listRef} aria-live="polite">
        {isNew || messages.length === 0 ? (
          <div className="wf-msg guardian">
            <AgentFace name={guardian} />
            <div className="wf-msg-b">
              <p>
                Halo! Ceritakan apa yang mau diotomasi. Saya susun node-nya di canvas, lalu kita atur
                trigger, izin, dan notifikasinya bareng.
              </p>
              {isNew ? (
                <>
                  <span className="wf-lbl">Bot penjaga</span>
                  <Select
                    value={guardian}
                    onChange={(name) => onGuardian?.(name)}
                    options={agents.map((agent) => ({
                      value: agent.name,
                      icon: agentOptionIcon(agent.name),
                      label: displayLabel(agent) + (agent.model ? " · " + agent.model.split("/").pop() : ""),
                    }))}
                    aria-label="Guardian bot"
                  />
                  <span className="wf-hint">
                    Bot ini yang menjalankan, memperbaiki, dan mengedit workflow. Bisa diganti nanti di
                    Settings.
                  </span>
                  <span className="wf-lbl">Atau mulai dari ide ini:</span>
                  <div className="wf-ideas">
                    {TEMPLATES.map((template) => (
                      <button
                        type="button"
                        key={template.key}
                        className="wf-idea"
                        onClick={() => onTemplate?.(template)}
                      >
                        <span className={"wf-ico sm tint-" + template.tint}>
                          <Ic name={template.icon} size={16} />
                        </span>
                        <span>
                          <b>{template.name}</b>
                          <small>{template.line}</small>
                        </span>
                      </button>
                    ))}
                  </div>
                </>
              ) : null}
            </div>
          </div>
        ) : null}
        {messages.map((message) => (
          <div className={"wf-msg " + message.role} key={message.id}>
            {message.role === "guardian" ? <AgentFace name={guardian} /> : null}
            <div className="wf-msg-b">
              <p>{message.text}</p>
              <ChangeCard
                message={message}
                onUndo={onUndo}
                latest={message.id === lastChangeId}
              />
              {message.issues && message.issues.length > 0 ? (
                <ul className="wf-issues">
                  {message.issues.map((issue, index) => (
                    <li key={index}>
                      <Ic name="circle-alert" size={12} />
                      {issue.node_id ? <code>{issue.node_id}</code> : null} {issue.message}
                    </li>
                  ))}
                </ul>
              ) : null}
            </div>
          </div>
        ))}
        {busy ? (
          <div className="wf-msg guardian">
            <AgentFace name={guardian} />
            <div className="wf-msg-b">
              <span className="wf-typing" aria-label="Guardian is thinking">
                <i />
                <i />
                <i />
              </span>
            </div>
          </div>
        ) : null}
        {needsLock && lockTools ? (
          <div className="wf-lockcard">
            <p>
              Supaya bisa jalan sendiri, workflow ini butuh izin berikut. Saya kunci daftarnya saat
              diaktifkan.
            </p>
            <div className="wf-locklist mono">
              {lockTools.length === 0 ? <div className="muted">No tools needed.</div> : null}
              {lockTools.map((tool) => (
                <div key={tool.tool}>
                  <span>{tool.tool}</span>
                  <span className={"risk r-" + tool.risk}>{tool.risk}</span>
                </div>
              ))}
            </div>
            <div className="wf-lockacts">
              <Button variant="primary" onClick={onActivate}>
                Lock and activate
              </Button>
              <Button variant="secondary" onClick={onReviewLock}>
                Review first
              </Button>
            </div>
          </div>
        ) : null}
      </div>
      <div className="wf-asst-foot">
        <div className="wf-chips">
          {isNew ? (
            <>
              <button type="button" className="wf-chip" onClick={() => onTemplate?.(TEMPLATES[0]!)}>
                Mulai dari template
              </button>
              <button type="button" className="wf-chip" onClick={onImport}>
                Impor dari JSON
              </button>
            </>
          ) : (
            <>
              <button type="button" className="wf-chip" onClick={onTestRun}>
                Test run
              </button>
              <button
                type="button"
                className="wf-chip"
                disabled={busy}
                onClick={() => void send("Jelaskan workflow ini secara singkat.")}
              >
                Jelaskan workflow ini
              </button>
            </>
          )}
        </div>
        {error ? <p className="wf-field-err">{error}</p> : null}
        <div className="wf-composer">
          <textarea
            value={text}
            rows={2}
            placeholder={"Minta " + label(guardian) + " membuat atau mengubah sesuatu…"}
            aria-label="Message the guardian"
            onChange={(event) => setText(event.target.value)}
            onKeyDown={onKey}
          />
          <button
            type="button"
            className="wf-send"
            aria-label="Send"
            disabled={busy || !text.trim()}
            onClick={() => void send()}
          >
            <Ic name="arrow-up" size={16} />
          </button>
        </div>
      </div>
    </div>
  );
}
