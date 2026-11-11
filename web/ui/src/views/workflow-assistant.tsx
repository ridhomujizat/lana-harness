import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { uploadAttachments } from "../api/attachments";
import { errorMessage } from "../api/client";
import type { Agent } from "../api/agents";
import type { ChatMessage, LockTool, SendAssistantResponse, Workflow } from "../api/workflows";
import { Ic } from "../ui/icons";
import { Button, Select } from "../ui/gr8r";
import { AgentFace } from "./workflow-common";
import { displayLabel, useAgentLabel } from "./agent-label";
import { TEMPLATES } from "./workflow-templates";
import type { WorkflowTemplate } from "./workflow-templates";
import { agentOptionIcon } from "./agent-avatar";
import { ApprovalCard } from "./approval";
import {
  AttachButton,
  AttachmentChips,
  AttachmentDropOverlay,
  draftFileMeta,
  pasteImageFiles,
  useAttachmentDraft,
  useFileDrop,
} from "./attachments";
import type { DraftAttachment } from "./attachments";
import { ContextMeter } from "./context-meter";
import { useLiveSession } from "./live-session";
import { QuestionCard } from "./question";
import { SecretRequestCard } from "./secret-request";
import { pinToBottom, useStickToBottom } from "./scroll-pin";
import { storedAttachments } from "./session";
import { SlashMenu, useSlashMenu } from "./slash-menu";
import type { SlashCommand } from "./slash-menu";
import { TurnBlock, UserBubble } from "./turn";
import type { ThreadItem, UserAttachment } from "./turn";

/* The workflow assistant column of REQ-WF-010 (design workflows-5 and -6) and
   spec 51 REQ-WFC-007/008. On a new workflow it shows the guardian picker and
   the idea cards. Once the workflow exists it shows the guardian's chat
   session like the main chat: owner messages with their files, turns with
   tool steps, the reply, the ask_user and approval cards, and Stop. Change
   cards follow the turn that published them, and the activation card locks
   permissions. Messages from before spec 51 (no session) render first. */

/* A files-only send carries this text; UserBubble hides it. */
const ATTACHMENTS_TEXT = "(attachments)";

interface AssistantProps {
  workflow: Workflow | null;
  guardian: string;
  agents: Agent[];
  /* Stored chat messages: legacy messages and change cards. */
  messages: ChatMessage[];
  /* The guardian chat session, "" until first use. */
  sessionId: string;
  /* The editor is creating or saving the workflow before a send. */
  busy: boolean;
  lockTools: LockTool[] | null;
  onGuardian?: (name: string) => void;
  /* Sends an owner message with uploaded attachment paths. It resolves to
     null when the send moved to another view (a new workflow). */
  onSend: (text: string, attachments: string[]) => Promise<SendAssistantResponse | null>;
  /* Ensures the session, so files can be uploaded before the first message. */
  onEnsureSession: () => Promise<string>;
  /* The /new command. */
  onNewSession: () => Promise<void>;
  /* A guardian turn ended: the graph may have changed. */
  onTurnEnd?: () => void;
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

function Issues({ message }: { message: ChatMessage }) {
  if (!message.issues || message.issues.length === 0) {
    return null;
  }
  return (
    <ul className="wf-issues">
      {message.issues.map((issue, index) => (
        <li key={index}>
          <Ic name="circle-alert" size={12} />
          {issue.node_id ? <code>{issue.node_id}</code> : null} {issue.message}
        </li>
      ))}
    </ul>
  );
}

/* One stored chat message in the bubble style: legacy messages, local notes
   (Undo) and change cards whose turn is not in the timeline. */
function StoredMessage({
  message,
  guardian,
  latestChange,
  onUndo,
}: {
  message: ChatMessage;
  guardian: string;
  latestChange: number | undefined;
  onUndo: (version: number) => void;
}) {
  return (
    <div className={"wf-msg " + message.role}>
      {message.role === "guardian" ? <AgentFace name={guardian} /> : null}
      <div className="wf-msg-b">
        <p>{message.text}</p>
        <ChangeCard message={message} onUndo={onUndo} latest={message.id === latestChange} />
        <Issues message={message} />
      </div>
    </div>
  );
}

function pendingAttachments(files: DraftAttachment[]): UserAttachment[] {
  return files.map((file) => ({
    key: file.id,
    name: file.file.name,
    src: file.url,
    alt: file.file.name,
    isImage: file.isImage,
    meta: draftFileMeta(file.file),
  }));
}

/* REQ-TFB-004: the end card shows under the session's last turn only. */
function lastTurnKey(thread: ThreadItem[]): string {
  return thread.reduce((found, item) => (item.kind === "turn" ? item.key : found), "");
}

export function WorkflowAssistant({
  workflow,
  guardian,
  agents,
  messages,
  sessionId,
  busy,
  lockTools,
  onGuardian,
  onSend,
  onEnsureSession,
  onNewSession,
  onTurnEnd,
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
  const [sending, setSending] = useState(false);
  const [uploading, setUploading] = useState(false);
  const isNew = workflow === null;
  const listRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);
  const attachmentDraft = useAttachmentDraft();
  const fileDrop = useFileDrop(attachmentDraft.addFiles);
  const handleTurnEnd = useCallback(() => onTurnEnd?.(), [onTurnEnd]);
  const live = useLiveSession(isNew ? "" : sessionId, { onTurnEnd: handleTurnEnd });
  const running = live.running;

  async function startNewSession() {
    setError("");
    try {
      await onNewSession();
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  const slashCommands: SlashCommand[] = isNew
    ? []
    : [
        {
          name: "new",
          title: "New session",
          description: "Start a fresh chat with the guardian. The old one stays in History.",
          icon: "square-pen",
          run: () => void startNewSession(),
        },
      ];
  const slash = useSlashMenu(text, setText, slashCommands);

  useStickToBottom(listRef, stickRef);
  useEffect(() => {
    const list = listRef.current;
    if (list && stickRef.current) {
      list.scrollTop = list.scrollHeight;
    }
  }, [messages.length, live.thread, live.pending, live.streamText, busy]);

  function onScroll() {
    const list = listRef.current;
    if (list) {
      stickRef.current = list.scrollHeight - list.scrollTop - list.clientHeight < 96;
    }
  }

  async function send(value?: string) {
    const body = (value ?? text).trim();
    const files = value === undefined ? attachmentDraft.items.slice() : [];
    if ((!body && files.length === 0) || busy || sending || running) {
      return;
    }
    setError("");
    live.clearError();
    if (value === undefined) {
      setText("");
    }
    pinToBottom(listRef, stickRef);
    live.setPending({ text: body, files });
    setSending(true);
    try {
      let paths: string[] = [];
      if (files.length > 0) {
        /* Uploads go to the guardian session, so it must exist first. */
        setUploading(true);
        const target = sessionId || (await onEnsureSession());
        const uploaded = await uploadAttachments(
          target,
          files.map((item) => item.file)
        );
        paths = uploaded.map((item) => item.path);
        setUploading(false);
      }
      const result = await onSend(body || ATTACHMENTS_TEXT, paths);
      if (result) {
        live.started(result);
      }
      /* The pending bubble owns the object URLs until the log has the
         message, so the chips are dropped without revoking them. */
      attachmentDraft.clear();
    } catch (err) {
      live.setPending(null);
      if (value === undefined) {
        setText((current) => current || body);
        attachmentDraft.restore(files);
      }
      setError(errorMessage(err));
    } finally {
      setUploading(false);
      setSending(false);
    }
  }

  function onKey(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (slash.onKeyDown(event)) {
      return;
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void send();
    }
  }

  /* Session change cards go after their turn. The rest render as stored
     messages: legacy ones above the timeline, and local notes and change
     cards whose turn is not shown below it. */
  const turnIds = useMemo(() => {
    const ids = new Set<string>();
    for (const item of live.thread) {
      if (item.kind === "turn") {
        ids.add(item.turn.id);
      }
    }
    return ids;
  }, [live.thread]);
  const byTurn = new Map<string, ChatMessage[]>();
  const before: ChatMessage[] = [];
  const after: ChatMessage[] = [];
  for (const message of messages) {
    const inSession = Boolean(sessionId) && message.session_id === sessionId;
    if (inSession && message.turn_id && turnIds.has(message.turn_id)) {
      byTurn.set(message.turn_id, (byTurn.get(message.turn_id) || []).concat([message]));
    } else if (inSession || message.id < 0) {
      after.push(message);
    } else {
      before.push(message);
    }
  }

  const lastChangeId = [...messages].reverse().find((message) => message.change)?.id;
  const needsLock =
    workflow !== null &&
    (workflow.status === "draft" || !workflow.lock_current) &&
    workflow.graph.nodes.length > 1 &&
    lockTools !== null;
  const pending = live.pending;
  const hasTimeline = live.thread.length > 0 || pending !== null;
  const showGreeting = isNew || (messages.length === 0 && !hasTimeline);
  const agentName = label(guardian);
  const endKey = lastTurnKey(live.thread);
  /* Dots while the workflow is created or saved, or the sent message waits
     for its turn to show up. */
  const waiting = !running && (busy || sending || pending !== null);

  return (
    <div className="wf-asst" {...(isNew ? {} : fileDrop.handlers)}>
      {isNew ? null : <AttachmentDropOverlay active={fileDrop.active} />}
      <div className="wf-asst-list" ref={listRef} onScroll={onScroll} aria-live="polite">
        {showGreeting ? (
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
        {before.map((message) => (
          <StoredMessage
            key={message.id}
            message={message}
            guardian={guardian}
            latestChange={lastChangeId}
            onUndo={onUndo}
          />
        ))}
        {hasTimeline ? (
          <div className="wf-thread">
            {live.thread.map((item) =>
              item.kind === "user" ? (
                <UserBubble
                  key={item.key}
                  text={item.text}
                  continue={item.continue === true}
                  attachments={storedAttachments(sessionId, item.attachments)}
                />
              ) : (
                <div className="wf-turn" key={item.key}>
                  <TurnBlock
                    turn={item.turn}
                    sessionId={sessionId}
                    agent={guardian}
                    agentLabel={agentName}
                    rootEvents={live.turnEvents.get(item.turn.id) || []}
                    childEvents={live.childEvents}
                    onLoadChildren={live.loadChildren}
                    waitingForInput={Boolean(live.question) || Boolean(live.secrets.request && !live.secrets.request.outcome)}
                    showStatusCard={item.key === endKey && !running}
                    onContinue={() => void live.continueTurn()}
                    continueBusy={live.continueBusy}
                    continueError={live.continueError}
                    streamingText={
                      item.turn.running && (!live.liveTurnId || item.turn.id === live.liveTurnId)
                        ? live.streamText
                        : ""
                    }
                    thinking={
                      item.turn.running &&
                      (!live.liveTurnId || item.turn.id === live.liveTurnId) &&
                      live.thinking
                    }
                  />
                  {(byTurn.get(item.turn.id) || []).map((message) => (
                    <div className="wf-turn-change" key={message.id}>
                      <ChangeCard message={message} onUndo={onUndo} latest={message.id === lastChangeId} />
                      <Issues message={message} />
                    </div>
                  ))}
                </div>
              )
            )}
            {pending ? (
              <UserBubble
                key="pending"
                text={pending.text}
                attachments={pendingAttachments(pending.files)}
              />
            ) : null}
          </div>
        ) : null}
        {after.map((message) => (
          <StoredMessage
            key={message.id}
            message={message}
            guardian={guardian}
            latestChange={lastChangeId}
            onUndo={onUndo}
          />
        ))}
        {waiting ? (
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
        {live.approval || live.question || live.secrets.request ? (
          <div className="wf-asst-slot">
            {live.approval ? (
              <ApprovalCard
                key={live.approval.request_id}
                approval={live.approval}
                busy={Boolean(live.approval.deciding)}
                onAnswer={live.answerApproval}
                onExpire={live.expireApproval}
                onDismiss={live.dismissApproval}
              />
            ) : null}
            {live.question ? (
              <QuestionCard
                key={"question-" + live.question.request_id}
                question={live.question}
                responder="web"
                readOnly={false}
                busy={live.questionBusy}
                onSubmit={live.submitQuestion}
                onSkipAll={live.skipQuestion}
                onExpire={live.expireQuestion}
              />
            ) : null}
            {live.secrets.request ? (
              <SecretRequestCard
                key={"secret-" + live.secrets.request.request_id}
                request={live.secrets.request}
                agentLabel={agentName}
                onSave={live.secrets.save}
                onCancel={live.secrets.cancel}
                onExpire={live.secrets.expire}
                onDismiss={live.secrets.dismiss}
              />
            ) : null}
          </div>
        ) : null}
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
                disabled={busy || sending || running}
                onClick={() => void send("Jelaskan workflow ini secara singkat.")}
              >
                Jelaskan workflow ini
              </button>
            </>
          )}
        </div>
        {live.streamState === "reconnecting" && sessionId && !isNew ? (
          <p className="wf-hint" role="status">
            The live stream is reconnecting.
          </p>
        ) : null}
        {live.queued ? (
          <p className="wf-hint" role="status">
            Message queued. It will be claimed at the next step.
          </p>
        ) : null}
        {error || live.actionError || live.loadError ? (
          <p className="wf-field-err" role="alert">
            {error || live.actionError || live.loadError}
          </p>
        ) : null}
        <div className="wf-composer">
          <SlashMenu state={slash} />
          <AttachmentChips items={attachmentDraft.items} onRemove={attachmentDraft.remove} />
          {attachmentDraft.notice ? (
            <p className="wf-hint" role="status">
              {attachmentDraft.notice}
            </p>
          ) : null}
          {uploading ? (
            <p className="wf-hint" role="status">
              Uploading…
            </p>
          ) : null}
          <textarea
            value={text}
            rows={2}
            placeholder={"Minta " + agentName + " membuat atau mengubah sesuatu…"}
            aria-label="Message the guardian"
            onChange={(event) => {
              setText(event.target.value);
              slash.reset();
            }}
            onKeyDown={onKey}
            onPaste={isNew ? undefined : (event) => pasteImageFiles(event, attachmentDraft.addFiles)}
          />
          <div className="wf-composer-bar">
            {isNew ? null : <AttachButton disabled={sending} onFiles={attachmentDraft.addFiles} />}
            <span className="wf-composer-spacer" />
            {sessionId && !isNew ? (
              <ContextMeter
                sessionId={sessionId}
                refreshKey={(running ? "run-" : "idle-") + live.liveTurnId}
                busy={running || sending}
              />
            ) : null}
            {running ? (
              <button
                type="button"
                className="wf-send stop"
                aria-label="Stop"
                title="Stop the turn"
                disabled={live.cancelling}
                onClick={() => void live.cancel()}
              >
                <Ic name="square" size={14} />
              </button>
            ) : (
              <button
                type="button"
                className="wf-send"
                aria-label="Send"
                disabled={busy || sending || (!text.trim() && attachmentDraft.items.length === 0)}
                onClick={() => void send()}
              >
                <Ic name="arrow-up" size={16} />
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
