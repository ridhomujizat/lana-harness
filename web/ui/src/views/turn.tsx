import { applyDiscussionEvent } from "../lib/discussion";
import { DiscussionCard } from "./discussion";
import type { DiscussionLog } from "../lib/discussion";
import { parseAttachmentBlock, type MessageFile } from "../lib/attachment-blocks";
import { AgentFiles, FoldText, MessageFiles, type UserAttachment } from "./message-files";
import { useEffect, useRef, useState } from "react";
import type { AvatarMood } from "../lib/avatar-motion";
import type { SessionContentBlock, SessionEvent, UserMessagePayload } from "../api/sessions";
import { contentText, cut, knowledgeCompiledLine, memoryOpLine, primaryArg, toolStatusWord, userMessageText } from "../lib/format";
import { Markdown } from "../lib/markdown";
import { Ic } from "../ui/icons";
import { Button } from "../ui/gr8r";
import { AgentFace } from "./presence";
import { useAgentAvatar } from "../lib/avatar-store";
import { AgentAvatar } from "./agent-avatar";
import { TeamBlock } from "./team";
import type { TeamChildRequest } from "./team";

/* One assistant turn of the chat view (REQ-WEB-038). The turn block is kept
   separate from the session view so a team block (spec #10) can wrap or
   replace it later without rewriting the chat view. */


const READ_TOOLS = new Set(["read_file", "skill_load", "memory_read"]);
const SEARCH_TOOLS = new Set(["glob", "grep"]);
const WRITE_TOOLS = new Set(["write_file", "edit_file", "memory_write"]);
const COMMAND_TOOLS = new Set(["bash", "terminal"]);
const FETCH_TOOLS = new Set(["web_fetch", "web_search"]);
const DELEGATE_TOOLS = new Set(["task", "task_continue"]);
/* REQ-AGF-006: send_file counts as its own category ("Sent 2 files"). */
const SEND_TOOLS = new Set(["send_file"]);

const CATEGORY_ORDER = [
  "read",
  "search",
  "write",
  "send",
  "command",
  "fetch",
  "delegate",
  "other",
];

export interface TurnApproval {
  request_id: string;
  call_id: string;
  tool: string;
  risk: string;
  label: string;
  decided: string;
}

export interface TurnStep {
  key: string;
  callId: string;
  name: string;
  input: unknown;
  status: string;
  output: unknown;
  approval: TurnApproval | null;
  /* The model step the call ran in, from the turn's step/start events. */
  step?: number;
}

export interface TurnUserText {
  key: string;
  text: string;
  /* API-TFB-004: a continue message renders as a chip, not an owner bubble. */
  continue?: boolean;
  /* API-ATT-004: the image attachments of an owner message, in order. */
  attachments?: UserAttachmentRef[];
  /* API reply: set when the owner message replies to an automation message. */
  replyTo?: ReplyTarget;
}

/* ReplyTarget is a reply the owner is writing or that an owner message already
   carries: the seq of the automation message, its source and name, and the
   quoted text. */
export interface ReplyTarget {
  seq: number;
  source: string;
  sourceName: string;
  text: string;
}

/* The label of a reply target, e.g. "Replying to workflow Daily brief". */
export function replyTargetLabel(source: string, sourceName: string): string {
  const name = sourceName ? source + " " + sourceName : source;
  return "Replying to " + (name || "message");
}

/* The first non-empty line of a quoted message, trimmed and cut short. */
export function quoteLine(text: string, max = 90): string {
  const first = String(text || "")
    .split("\n")
    .find((line) => line.trim().length > 0);
  return cut((first || "").trim(), max);
}

/* UserAttachmentRef is a stored attachment of an owner message: an image block,
   or (REQ-CHF-001) the text block of another file. The reader turns it into a
   URL (API-ATT-003). */
export interface UserAttachmentRef {
  path: string;
  mediaType: string;
  file?: MessageFile;
}

export type { UserAttachment };

export type TurnSegment =
  | {
      kind: "text";
      key: string;
      text: string;
      /* API reply: the seq of the assistant/message event, so a sourced message
         can be replied to. */
      seq?: number;
      /* API-WF-005: a message the runtime delivered out of band carries its
         source ("workflow") and the source's name. */
      source?: string;
      sourceName?: string;
      /* Group rooms: an agent's reply and what it used. */
      agentMeta?: AgentReplyMeta;
    }
  | { kind: "thinking"; key: string; text: string }
  | { kind: "step"; key: string; step: TurnStep };

/* Group rooms: a reply's pass/fail state and the tools, notes and files the
   agent used (assistant/message with source "agent"). */
export interface AgentReplyMeta {
  /* The agent's own turn behind the reply, to show its steps like a chat. */
  childSessionId: string;
  childTurnId: string;
  passed: boolean;
  failed: boolean;
  tools: Record<string, number>;
  notes: string[];
  files: string[];
}

export interface TurnGroup {
  agentMeta?: AgentReplyMeta;
  kind: "text" | "thinking" | "activity";
  key: string;
  text?: string;
  steps?: TurnStep[];
  seq?: number;
  source?: string;
  sourceName?: string;
}

/* The public turn shape keeps the stub's optional fields and index signature so
   the team panel can accept a live child turn; buildTurns returns the fuller
   ChatTurn below. */
export interface Turn {
  id?: string;
  key?: string;
  startedAt?: number;
  lastAt?: number;
  endedAt?: number;
  running?: boolean;
  stopReason?: string;
  error?: unknown;
  userTexts?: TurnUserText[];
  segments?: TurnSegment[];
  steps?: TurnStep[];
  approvals?: Map<string, TurnApproval>;
  groups?: TurnGroup[];
  stepCount?: number;
  currentStep?: number;
  /* REQ-MEM-030: muted memory-capture lines, one per applied operation. */
  notes?: string[];
  sentFiles?: SentFileRef[];
  [key: string]: unknown;
}

export interface ChatTurn extends Turn {
  id: string;
  key: string;
  startedAt: number;
  lastAt: number;
  endedAt: number;
  running: boolean;
  stopReason: string;
  error: unknown;
  userTexts: TurnUserText[];
  segments: TurnSegment[];
  steps: TurnStep[];
  approvals: Map<string, TurnApproval>;
  groups: TurnGroup[];
  stepCount: number;
  currentStep: number;
  notes: string[];
  /* REQ-AGF-005: the files the agent sent in this turn (file/sent events). */
  sentFiles: SentFileRef[];
  /* Spec 47: the room discussion of this turn, if the lead opened one. */
  discussion: DiscussionLog | null;
}

/* SentFileRef is one file/sent event (API-AGF-001). */
export interface SentFileRef {
  key: string;
  path: string;
  name: string;
  mediaType: string;
  size: number;
  caption: string;
  at: number;
}

export type ThreadItem =
  | {
      kind: "user";
      key: string;
      text: string;
      continue?: boolean;
      attachments?: UserAttachmentRef[];
      replyTo?: ReplyTarget;
    }
  | { kind: "turn"; key: string; turn: ChatTurn };

export interface LiveTurnState {
  running?: boolean;
  turnId?: string;
  startedAt?: number;
}

function payloadOf(event: SessionEvent): Record<string, unknown> {
  return event.payload && typeof event.payload === "object"
    ? event.payload
    : {};
}

function stringOf(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/* API reply: reads the reply_to of a user/message payload into a ReplyTarget.
   A payload without one, or with a non-positive seq, returns null. */
function replyTargetOf(payload: Record<string, unknown>): ReplyTarget | null {
  const raw = (payload as UserMessagePayload).reply_to;
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const seq = Number(raw.seq);
  if (!Number.isFinite(seq) || seq <= 0) {
    return null;
  }
  return {
    seq,
    source: stringOf(raw.source),
    sourceName: stringOf(raw.source_name),
    text: stringOf(raw.text),
  };
}

/* REQ-MDL-006: an assistant message's thinking blocks are kept separate from
   the answer text. They render in a collapsed section, not as the reply. */
function thinkingText(content: unknown): string {
  if (!Array.isArray(content)) {
    return "";
  }
  const pieces: string[] = [];
  for (const block of content) {
    if (!block || typeof block !== "object") {
      continue;
    }
    const value = block as Record<string, unknown>;
    if (
      value.type === "thinking" &&
      typeof value.text === "string" &&
      value.text !== ""
    ) {
      pieces.push(value.text);
    }
  }
  return pieces.join("\n\n");
}

export function formatDuration(milliseconds: number): string {
  const seconds = Math.max(0, Math.round(Number(milliseconds) / 1000));
  if (seconds < 60) {
    return seconds + "s";
  }
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  if (minutes < 60) {
    return rest ? minutes + "m " + rest + "s" : minutes + "m";
  }
  const hours = Math.floor(minutes / 60);
  const minuteRest = minutes % 60;
  return minuteRest ? hours + "h " + minuteRest + "m" : hours + "h";
}

export function formatClock(ms: number): string {
  const value = Number(ms);
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

/* API-WF-005: the muted line above an out-of-band assistant message, e.g.
   "via workflow Daily brief" or "via workflow" when the source has no name. A
   message without a source returns "". */
export function messageSourceLabel(
  source: unknown,
  sourceName: unknown
): string {
  const from = typeof source === "string" ? source : "";
  if (!from) {
    return "";
  }
  const name = typeof sourceName === "string" ? sourceName : "";
  return name ? "via " + from + " " + name : "via " + from;
}

function errorTextOf(error: unknown): string {
  if (typeof error === "string") {
    return error;
  }
  if (error && typeof error === "object") {
    const record = error as Record<string, unknown>;
    if (typeof record.message === "string") {
      return record.message;
    }
  }
  return "";
}

/* The plain reason line and the last step of an abnormally ended turn
   (REQ-TFB-004, AC-TFB-007..009). A normal end_turn returns null: a tool
   failure inside a turn is the agent's story, not a card. */
export interface TurnEndCard {
  text: string;
  lastStep: string;
}

export function turnEndCard(turn: Turn): TurnEndCard | null {
  const reason = String(turn.stopReason || "");
  let text = "";
  if (reason === "cancelled") {
    text = "Stopped by you.";
  } else if (reason === "interrupted") {
    text = "lana restarted while working.";
  } else if (reason === "error") {
    const message = errorTextOf(turn.error);
    text = "Stopped by an error:" + (message ? " " + message : "");
  } else if (reason === "max_steps") {
    const count = Number(turn.stepCount) || 0;
    text = "Stopped after the step limit (" + count + " steps).";
  } else if (reason === "loop") {
    text = "Stopped: lana kept repeating a step without progress.";
  } else if (reason === "max_tokens") {
    /* REQ-TFB-008 item 5: the final step hit the output-token cap. */
    text = "Reply cut off by the model's output limit.";
  } else {
    return null;
  }

  const steps = Array.isArray(turn.steps) ? turn.steps : [];
  const last = steps.length > 0 ? steps[steps.length - 1] : null;
  const stepNumber = last ? Number(last.step) || 0 : 0;
  const lastStep =
    last && last.name && stepNumber > 0
      ? "Last step: " + last.name + " (step " + stepNumber + ")"
      : "";
  return { text, lastStep };
}

function toolCategory(name: string | null | undefined): string {
  const tool = String(name || "").toLowerCase();
  if (READ_TOOLS.has(tool)) {
    return "read";
  }
  if (SEARCH_TOOLS.has(tool)) {
    return "search";
  }
  if (WRITE_TOOLS.has(tool)) {
    return "write";
  }
  if (SEND_TOOLS.has(tool)) {
    return "send";
  }
  if (COMMAND_TOOLS.has(tool)) {
    return "command";
  }
  if (FETCH_TOOLS.has(tool)) {
    return "fetch";
  }
  if (DELEGATE_TOOLS.has(tool)) {
    return "delegate";
  }
  return "other";
}

function plural(count: number, word: string): string {
  return count + " " + word + (count === 1 ? "" : "s");
}

function categoryText(
  category: string,
  count: number,
  running: boolean
): string {
  switch (category) {
    case "read":
      return (running ? "Reading " : "Read ") + plural(count, "file");
    case "search":
      return (running ? "Searching " : "Searched ") + plural(count, "time");
    case "write":
      return (running ? "Writing " : "Wrote ") + plural(count, "file");
    case "send":
      return (running ? "Sending " : "Sent ") + plural(count, "file");
    case "command":
      return (running ? "Running " : "Ran ") + plural(count, "command");
    case "fetch":
      return (running ? "Fetching " : "Fetched ") + plural(count, "page");
    case "delegate":
      return (running ? "Delegating " : "Delegated ") + plural(count, "task");
    default:
      return (running ? "Using " : "Used ") + plural(count, "tool");
  }
}

function stepIsRunning(step: TurnStep): boolean {
  if (step.approval && !step.approval.decided) {
    return true;
  }
  const status = String(step.status || "running").toLowerCase();
  if (status !== "" && status !== "running") {
    return false;
  }
  return !step.output;
}

/* "Read 5 files · Running 1 command" (REQ-WEB-038, AC-WEB-041). A category
   that still has an unfinished step reads with its -ing verb. */
export function turnSummary(steps: TurnStep[] | null | undefined): string {
  if (!Array.isArray(steps) || steps.length === 0) {
    return "";
  }
  const tally = new Map<string, { count: number; running: boolean }>();
  for (const step of steps) {
    const category = toolCategory(step.name);
    const entry = tally.get(category) || { count: 0, running: false };
    entry.count += 1;
    if (stepIsRunning(step)) {
      entry.running = true;
    }
    tally.set(category, entry);
  }
  return CATEGORY_ORDER.map((category) => {
    const entry = tally.get(category);
    if (!entry) {
      return "";
    }
    return categoryText(category, entry.count, entry.running);
  })
    .filter(Boolean)
    .join(" · ");
}

export function stepIntent(name: string | null | undefined): string {
  const tool = String(name || "tool");
  switch (tool) {
    case "read_file":
      return "Read a file";
    case "glob":
      return "Find files";
    case "grep":
      return "Search the text";
    case "write_file":
      return "Write a file";
    case "edit_file":
      return "Edit a file";
    case "send_file":
      return "Send a file";
    case "bash":
    case "terminal":
      return "Run a command";
    case "web_fetch":
      return "Fetch a page";
    case "web_search":
      return "Search the web";
    case "task":
      return "Hand off a task";
    case "task_continue":
      return "Continue a task";
    case "memory_read":
      return "Read memory";
    case "memory_write":
      return "Write memory";
    case "skill_load":
      return "Load a skill";
    default:
      return "Use " + tool;
  }
}

function stepStatusText(step: TurnStep): string {
  if (step.approval && !step.approval.decided) {
    return "Waiting for your approval.";
  }
  const status = toolStatusWord(step.status);
  if (status === "ok") {
    return "Finished.";
  }
  if (status === "error") {
    return "The tool returned an error.";
  }
  if (status === "rejected") {
    return "Rejected. The tool did not run.";
  }
  if (status === "cancelled") {
    return "Cancelled.";
  }
  return "Running.";
}

const CATEGORY_ICONS: Record<string, string> = {
  read: "file-text",
  search: "search",
  write: "pencil",
  send: "send",
  command: "terminal",
  fetch: "globe",
  delegate: "users",
  other: "wrench",
};

function stepStatusIcon(step: TurnStep): { icon: string; tone: string; word: string } {
  if (step.approval && !step.approval.decided) {
    return { icon: "hand", tone: "warn", word: "waiting for approval" };
  }
  const status = toolStatusWord(step.status);
  if (status === "ok") {
    return { icon: "check", tone: "ok", word: "done" };
  }
  if (status === "error") {
    return { icon: "x", tone: "err", word: "error" };
  }
  if (status === "rejected" || status === "cancelled") {
    return { icon: "ban", tone: "muted", word: status };
  }
  return stepIsRunning(step)
    ? { icon: "loader", tone: "run", word: "running" }
    : { icon: "check", tone: "ok", word: "done" };
}

function inputText(step: TurnStep): string {
  const input = step.input;
  if (input === undefined || input === null || input === "") {
    return "";
  }
  if (typeof input === "string") {
    return input;
  }
  if (typeof input === "object") {
    const record = input as Record<string, unknown>;
    if (typeof record.command === "string" && Object.keys(record).length <= 2) {
      return record.command;
    }
  }
  try {
    return JSON.stringify(input, null, 2);
  } catch {
    return String(input);
  }
}

/* One tool call in the process timeline: icon, what it did, the main argument
   on one line, and its status. Opening it shows the full input and output. */
function StepRow({ step }: { step: TurnStep }) {
  const [open, setOpen] = useState(false);
  const status = stepStatusIcon(step);
  const argument = cut(primaryArg(step.name, step.input), 120);
  const detailId =
    "step-detail-" + String(step.key).replace(/[^a-zA-Z0-9_-]/g, "-");
  const full = inputText(step);
  return (
    <li className={"tp-step tone-" + status.tone}>
      <button
        type="button"
        className="tp-row"
        aria-expanded={open}
        aria-controls={detailId}
        aria-label={stepIntent(step.name) + ", " + (step.name || "tool") + ", " + status.word}
        onClick={() => setOpen((value) => !value)}
      >
        <span className="tp-ico" aria-hidden="true">
          <Ic name={CATEGORY_ICONS[toolCategory(step.name)] || "wrench"} size={14} />
        </span>
        <span className="tp-label">{stepIntent(step.name)}</span>
        {argument ? (
          <code className="tp-arg" title={argument}>
            {argument}
          </code>
        ) : null}
        <span className="tp-status" title={status.word}>
          <Ic name={status.icon} size={13} className={status.icon === "loader" ? "spin" : ""} />
        </span>
      </button>
      {open ? (
        <div className="tp-detail" id={detailId}>
          <p className="tp-detail-status">
            <span className="tp-tool">{step.name || "tool"}</span> {stepStatusText(step)}
          </p>
          {full ? (
            <>
              <span className="tp-detail-k">Input</span>
              <pre className="tp-pre">{cut(full, 4000)}</pre>
            </>
          ) : null}
          {step.output ? (
            <>
              <span className="tp-detail-k">Output</span>
              <pre className="tp-pre">{cut(step.output, 4000)}</pre>
            </>
          ) : null}
        </div>
      ) : null}
    </li>
  );
}

/* A thinking block in the timeline: one quiet line, the reasoning on open. */
function ThoughtRow({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const first = text.replace(/\s+/g, " ").trim();
  return (
    <li className="tp-step tp-thought">
      <button type="button" className="tp-row" aria-expanded={open} onClick={() => setOpen((value) => !value)}>
        <span className="tp-ico" aria-hidden="true">
          <Ic name="brain" size={14} />
        </span>
        <span className="tp-label">Thought</span>
        <span className="tp-arg tp-arg-text">{cut(first, 120)}</span>
      </button>
      {open ? (
        <div className="tp-detail turn-text">
          <Markdown text={text} />
        </div>
      ) : null}
    </li>
  );
}

/* Text the agent wrote between tool calls ("Let me check the config…"). It is
   part of the process, not the answer. */
function NoteRow({
  text,
  source,
  sourceName,
}: {
  text: string;
  source?: string;
  sourceName?: string;
}) {
  const via = messageSourceLabel(source, sourceName);
  return (
    <li className="tp-step tp-note">
      <span className="tp-row tp-row-static">
        <span className="tp-ico" aria-hidden="true">
          <Ic name="message-square" size={14} />
        </span>
        <span className="tp-note-text">
          {via ? <span className="turn-via muted">{via}</span> : null}
          <Markdown text={text} />
        </span>
      </span>
    </li>
  );
}

/* Splits a turn's groups into the process (thinking, tool calls and the text
   between them) and the answer: the text after the last process group. */
export function splitTurn(groups: TurnGroup[] | undefined): { process: TurnGroup[]; answer: TurnGroup[] } {
  const list = groups || [];
  let last = -1;
  list.forEach((group, index) => {
    if (group.kind !== "text") {
      last = index;
    }
  });
  return { process: list.slice(0, last + 1), answer: list.slice(last + 1) };
}

/* The live line while a turn runs: what is happening right now. */
function liveText(steps: TurnStep[], thinking: boolean, waitingForInput: boolean): string {
  if (waitingForInput) {
    return "Waiting for your answer";
  }
  const running = [...steps].reverse().find(stepIsRunning);
  if (running) {
    if (running.approval && !running.approval.decided) {
      return "Waiting for your approval: " + (running.name || "tool");
    }
    const arg = cut(primaryArg(running.name, running.input), 80);
    return stepIntent(running.name) + (arg ? ": " + arg : "");
  }
  return thinking ? "Thinking…" : "Working…";
}

/* The whole process of a turn as one line (owner decision 2026-11-05): the
   answer stays the main thing; the steps open on demand as a timeline. */
function TurnProcess({
  groups,
  running,
  thinking,
  streaming,
  waitingForInput,
  turnKey,
}: {
  groups: TurnGroup[];
  running: boolean;
  thinking: boolean;
  streaming: boolean;
  waitingForInput: boolean;
  turnKey: string;
}) {
  const [open, setOpen] = useState(false);
  const steps = groups.flatMap((group) => (group.kind === "activity" ? group.steps || [] : []));
  const thoughts = groups.filter((group) => group.kind === "thinking").length;
  const waitingApproval = steps.some((step) => step.approval && !step.approval.decided);
  const live = (running || thinking) && !streaming;
  if (steps.length === 0 && thoughts === 0 && !live) {
    return null;
  }
  const parts: string[] = [];
  const summary = turnSummary(steps);
  if (summary) {
    parts.push(summary);
  }
  if (thoughts > 0) {
    parts.push("thought " + thoughts + "×");
  }
  const done = steps.filter((step) => !stepIsRunning(step)).length;
  const listId = "process-" + turnKey.replace(/[^a-zA-Z0-9_-]/g, "-");
  return (
    <div className={"turn-process" + (live ? " is-live" : "") + (open ? " is-open" : "") + (waitingApproval ? " is-waiting" : "")}>
      <button
        type="button"
        className="tp-head"
        aria-expanded={open}
        aria-controls={listId}
        onClick={() => setOpen((value) => !value)}
      >
        <Ic name="chevron-right" size={14} className={"tp-chev" + (open ? " is-open" : "")} />
        {live ? (
          <>
            <span className="tp-live-dot" aria-hidden="true" />
            <span className="tp-head-text tp-head-live" role="status">
              {liveText(steps, thinking, waitingForInput)}
            </span>
            {done > 0 ? <span className="tp-head-meta">{plural(done, "step")} done</span> : null}
          </>
        ) : (
          <span className="tp-head-text">{parts.join(" · ")}</span>
        )}
      </button>
      {open ? (
        <ol className="tp-list" id={listId}>
          {groups.map((group) =>
            group.kind === "thinking" ? (
              <ThoughtRow key={group.key} text={group.text || ""} />
            ) : group.kind === "activity" ? (
              (group.steps || []).map((step) => <StepRow key={step.key} step={step} />)
            ) : (
              <NoteRow
                key={group.key}
                text={group.text || ""}
                source={group.source}
                sourceName={group.sourceName}
              />
            )
          )}
        </ol>
      ) : null}
    </div>
  );
}

export interface TurnBlockProps {
  turn: Turn;
  /* agent is the identity: the avatar color hash and the fallback label. */
  agent: string;
  /* agentLabel is the human name to show, when it differs from the ID. */
  agentLabel?: string;
  /* The resolved agent color of the room timeline (REQ-ROOM-001): when it is
     known the header shows the colored face; otherwise the legacy hashed
     avatar of the session view stays. */
  agentColor?: string;
  streamingText?: string;
  /* REQ-MDL-006: a live "Thinking…" note while only thinking deltas arrived. */
  thinking?: boolean;
  waitingForInput?: boolean;
  team?: { lead: string; members: string[] } | null;
  /* teamLabels maps an agent ID to its display name for the team block. */
  teamLabels?: Record<string, string>;
  rootEvents?: SessionEvent[];
  childEvents?: Record<string, SessionEvent[]>;
  childLive?: Record<string, Turn>;
  onLoadChildren?: ((requests: TeamChildRequest[]) => void) | null;
  /* REQ-TFB-004: the status card shows under the session's last turn only. */
  showStatusCard?: boolean;
  /* The newest agent turn of the chat: its avatar idles (breathes) instead of
     resting, so the conversation's current face stays alive. */
  latest?: boolean;
  /* REQ-AGF-005: the session the turn belongs to, for sent-file URLs. */
  sessionId?: string;
  onContinue?: (() => void) | null;
  continueBusy?: boolean;
  /* API-TFB-003: the refusal text of a failed Continue, shown in the card. */
  continueError?: string;
  /* API reply: when set, a sourced automation message shows a Reply action that
     calls this with the message's target. */
  onReply?: ((target: ReplyTarget) => void) | null;
}

/* The avatar mood of a turn (lib/avatar-motion): a running turn waits for you,
   runs a tool, writes its answer, or thinks; a finished turn rests. */
export function turnMood(
  turn: Turn,
  live: { waitingForInput?: boolean; thinking?: boolean; streaming?: boolean } = {}
): AvatarMood {
  if (!turn.running) {
    return "rest";
  }
  const groups = turn.groups || [];
  const steps = groups.flatMap((group) => group.steps || []);
  if (live.waitingForInput || steps.some((step) => step.approval && !step.approval.decided)) {
    return "ask";
  }
  if (steps.some(stepIsRunning)) {
    return "tool";
  }
  if (live.thinking) {
    return "thinking";
  }
  if (live.streaming || groups.some((group) => group.kind === "text" && group.text)) {
    return "talk";
  }
  return "thinking";
}

/* turnMood plus the end of the turn as it is watched: a turn that finishes
   while on screen hops once ("done") or shakes ("error"), then rests. A turn
   loaded already finished just rests. */
export function useTurnMood(
  turn: Turn,
  live: { waitingForInput?: boolean; thinking?: boolean; streaming?: boolean }
): AvatarMood {
  const running = Boolean(turn.running);
  const wasRunning = useRef(running);
  const [flash, setFlash] = useState<AvatarMood | null>(null);
  const stopReason = String(turn.stopReason || "");
  useEffect(() => {
    const was = wasRunning.current;
    wasRunning.current = running;
    if (running || !was || stopReason === "cancelled") {
      return undefined;
    }
    const end: AvatarMood = stopReason === "error" ? "error" : "done";
    setFlash(end);
    const timer = setTimeout(() => setFlash(null), end === "error" ? 4000 : 2600);
    return () => clearTimeout(timer);
  }, [running, stopReason]);
  if (running) {
    return turnMood(turn, live);
  }
  return flash || "rest";
}

export function TurnBlock({
  turn,
  agent,
  agentLabel = "",
  agentColor = "",
  streamingText = "",
  thinking = false,
  waitingForInput = false,
  team = null,
  teamLabels = {},
  rootEvents = [],
  childEvents = {},
  childLive = {},
  onLoadChildren = null,
  showStatusCard = false,
  latest = false,
  onContinue = null,
  continueBusy = false,
  continueError = "",
  onReply = null,
  sessionId = "",
}: TurnBlockProps) {
  /* Spec 43: an agent with a generated avatar shows it through AgentFace. */
  const storedAvatar = useAgentAvatar(agent);
  const [now, setNow] = useState(() => Date.now());
  const [cardDismissed, setCardDismissed] = useState(false);
  const turnMoodNow = useTurnMood(turn, {
    waitingForInput,
    thinking,
    streaming: Boolean(streamingText),
  });
  const mood = turnMoodNow === "rest" && latest ? "idle" : turnMoodNow;

  useEffect(() => {
    if (!turn.running) {
      return undefined;
    }
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the running clock starts from the current time, as in turn.js
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [turn.running]);

  /* A turn of a team session renders the team block; a single-agent turn
     keeps the one-avatar header and has no work panel (REQ-TEAM-019). */
  if (team) {
    return (
      <TeamBlock
        team={team}
        turn={turn}
        rootEvents={rootEvents}
        childEvents={childEvents}
        childLive={childLive}
        onLoadChildren={onLoadChildren}
        labels={teamLabels}
      />
    );
  }

  const startedAt = Number(turn.startedAt) || Number(turn.lastAt) || 0;
  const finishedAt = Number(turn.endedAt) || 0;
  const elapsedAt = turn.running
    ? now
    : finishedAt || Number(turn.lastAt) || startedAt;
  const elapsed = startedAt > 0 && elapsedAt > 0 ? elapsedAt - startedAt : 0;
  const duration = startedAt > 0 ? formatDuration(elapsed) : "";
  /* An open question keeps the turn running, but the header names the reason
     instead of the working time (REQ-ASK-012). */
  let statusText = turn.running
    ? waitingForInput
      ? "waiting for you"
      : "working"
    : "worked";
  if (duration && !(turn.running && waitingForInput)) {
    statusText += " for " + duration;
  }
  if (!turn.running && turn.stopReason === "cancelled") {
    statusText = "stopped after " + (duration || "0s");
  }
  if (!turn.running && turn.stopReason === "error") {
    statusText = "failed after " + (duration || "0s");
  }

  const statusCard = showStatusCard ? turnEndCard(turn) : null;
  const parts = splitTurn(turn.groups);
  const shownAgent = agentLabel || agent;

  return (
    <article className="turn">
      <div className="turn-head">
        {agentColor || (storedAvatar && storedAvatar.svg) ? (
          <AgentFace name={agent} color={agentColor || storedAvatar?.color} size={32} mood={mood} />
        ) : (
          <AgentAvatar name={agent || "agent"} size="auto" mood={mood} />
        )}
        <span className="turn-agent">{shownAgent}</span>
        <span className="turn-status">{statusText}</span>
        <span className="turn-spacer"></span>
        <span className="turn-time">{formatClock(startedAt)}</span>
      </div>
      <TurnProcess
        groups={parts.process}
        running={Boolean(turn.running)}
        thinking={thinking}
        streaming={Boolean(streamingText) || (Boolean(turn.running) && parts.answer.length > 0)}
        waitingForInput={waitingForInput}
        turnKey={String(turn.key || turn.id || "turn")}
      />
      {parts.answer.map((segment) =>
        segment.source === "discussion" ? (
          /* Spec 47 REQ-RDS-053: a discussion summary shared into a member's
             room shows as a card. */
          <DiscussionCard
            key={segment.key}
            team={segment.sourceName || ""}
            text={segment.text || ""}
            at={Number(turn.startedAt) || 0}
          />
        ) : (
        <div className="turn-text turn-answer" key={segment.key}>
          {segment.source ? (
            <div className="turn-via muted">
              <span className="turn-via-label">
                {messageSourceLabel(segment.source, segment.sourceName)}
              </span>
              {onReply && segment.seq ? (
                <Button
                  size="sm"
                  variant="ghost"
                  icon="reply"
                  className="turn-reply"
                  aria-label={
                    "Reply to " + (segment.sourceName || segment.source)
                  }
                  onClick={() =>
                    onReply({
                      seq: Number(segment.seq),
                      source: segment.source || "",
                      sourceName: segment.sourceName || "",
                      text: segment.text || "",
                    })
                  }
                >
                  Reply
                </Button>
              ) : null}
            </div>
          ) : null}
          <Markdown text={segment.text || ""} />
        </div>
        )
      )}
      {sessionId && (turn.sentFiles || []).length > 0 ? (
        <AgentFiles
          sessionId={sessionId}
          files={turn.sentFiles || []}
          sender={agentLabel || agent}
        />
      ) : null}
      {(turn.notes || []).length > 0 ? (
        <div>
          {(turn.notes || []).map((note, index) => (
            <p className="step-detail-status" key={"note-" + index}>
              {note}
            </p>
          ))}
        </div>
      ) : null}
      {streamingText ? (
        <div className="turn-text turn-answer turn-text-streaming">
          <Markdown text={streamingText} />
        </div>
      ) : null}
      {statusCard && !cardDismissed ? (
        <div className="card" role="status" aria-live="polite">
          <p>{statusCard.text}</p>
          {statusCard.lastStep ? <p>{statusCard.lastStep}</p> : null}
          {continueError ? (
            <p className="card-error">{continueError}</p>
          ) : null}
          <div className="dialog-actions">
            <button
              type="button"
              className="button-ui button-ui-secondary"
              disabled={continueBusy}
              onClick={() => {
                if (onContinue) {
                  onContinue();
                }
              }}
            >
              {continueBusy ? "Continuing…" : "Continue"}
            </button>
            <button
              type="button"
              className="button-ui button-ui-tertiary"
              onClick={() => setCardDismissed(true)}
            >
              Dismiss
            </button>
          </div>
        </div>
      ) : null}
    </article>
  );
}

export interface UserBubbleProps {
  text: string;
  continue?: boolean;
  /* API-ATT-004: the image thumbnails and file chips of an owner message. */
  attachments?: UserAttachment[];
  /* API reply: the automation message this owner message replies to, shown as a
     muted quote line above the text. */
  replyTo?: ReplyTarget | null;
}

/* The quote line of an owner reply: "↪ workflow Daily brief: first line". */
export function replyQuoteLabel(reply: ReplyTarget): string {
  const name = reply.sourceName
    ? reply.source + " " + reply.sourceName
    : reply.source;
  const line = quoteLine(reply.text, 80);
  return "↪ " + (name || "message") + (line ? ": " + line : "");
}

/* The cancelable chip above a composer while a reply target is set. */
export function ReplyChip({
  target,
  onCancel,
}: {
  target: ReplyTarget;
  onCancel: () => void;
}) {
  return (
    <div className="reply-chip" role="status">
      <Ic name="reply" size={14} className="reply-chip-icon" />
      <span className="reply-chip-body">
        <span className="reply-chip-title">
          {replyTargetLabel(target.source, target.sourceName)}
        </span>
        {quoteLine(target.text, 80) ? (
          <span className="reply-chip-quote">{quoteLine(target.text, 80)}</span>
        ) : null}
      </span>
      <button
        type="button"
        className="ibtn ibtn-xs reply-chip-cancel"
        aria-label="Cancel reply"
        onClick={onCancel}
      >
        <Ic name="x" size={12} />
      </button>
    </div>
  );
}

const ATTACHMENTS_PLACEHOLDER = "(attachments)";

export function UserBubble({
  text,
  continue: isContinue = false,
  attachments = [],
  replyTo = null,
}: UserBubbleProps) {
  if (isContinue) {
    /* API-TFB-004: a continue request is a chip, not owner text. */
    return (
      <div className="chat-user-row">
        <span className="chip">↻ Continued</span>
      </div>
    );
  }
  /* REQ-CHF-002: files sit above the bubble as cards; the bubble holds only
     the owner's text and is left out when there is none. A files-only send is
     stored with the placeholder text "(attachments)", which is not shown. */
  if (attachments.length > 0 && text.trim() === ATTACHMENTS_PLACEHOLDER) {
    text = "";
  }
  return (
    <div className="chat-user-row">
      <div className="chat-user-stack">
        {attachments.length > 0 ? <MessageFiles attachments={attachments} /> : null}
        {text || replyTo ? (
          <div className="chat-bubble">
            {replyTo ? (
              <span className="chat-bubble-quote" title={replyQuoteLabel(replyTo)}>
                {replyQuoteLabel(replyTo)}
              </span>
            ) : null}
            {text ? <FoldText text={text} /> : null}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function newTurn(id: string, startedAt: unknown): ChatTurn {
  return {
    id,
    key: id,
    startedAt: Number(startedAt) || 0,
    lastAt: Number(startedAt) || 0,
    endedAt: 0,
    stopReason: "",
    error: null,
    userTexts: [],
    segments: [],
    steps: [],
    approvals: new Map(),
    groups: [],
    running: false,
    stepCount: 0,
    currentStep: 0,
    notes: [],
    sentFiles: [],
    discussion: null,
  };
}

function pushStep(turn: ChatTurn, step: TurnStep): void {
  turn.segments.push({ kind: "step", key: "step-" + step.key, step });
  turn.steps.push(step);
}

/* userMessageAttachments collects the image blocks and file blocks of an owner
   message in order. The web API keeps an image's path and media type and drops
   the bytes (API-ATT-004). */
function userMessageAttachments(
  payload: Record<string, unknown>
): UserAttachmentRef[] {
  const content = payload.content;
  if (!Array.isArray(content)) {
    return [];
  }
  const refs: UserAttachmentRef[] = [];
  for (const block of content) {
    if (!block || typeof block !== "object") {
      continue;
    }
    const value = block as SessionContentBlock;
    if (value.type === "image" && typeof value.path === "string" && value.path) {
      refs.push({
        path: value.path,
        mediaType:
          typeof value.media_type === "string" ? value.media_type : "",
      });
      continue;
    }
    const file =
      value.type === "text" && typeof value.text === "string"
        ? parseAttachmentBlock(value.text)
        : null;
    if (file) {
      refs.push({ path: file.path, mediaType: file.mediaType, file });
    }
  }
  return refs;
}

/* Builds the ordered chat thread from the durable event log plus the live
   frame text of the running turn. User messages become bubbles; each turn
   becomes one turn block. Events without a turn_id (rev 1 logs) are grouped
   into a synthetic turn so older sessions still render. */
export function buildTurns(
  events: SessionEvent[] | null | undefined,
  live: LiveTurnState = {}
): ThreadItem[] {
  const results = new Map<string, { status: string; output: unknown }>();
  for (const event of events || []) {
    if (event.kind !== "tool/result") {
      continue;
    }
    const payload = payloadOf(event);
    const callId = stringOf(payload.call_id);
    if (callId) {
      results.set(callId, {
        status: stringOf(payload.status),
        output: payload.output,
      });
    }
  }

  const turns: ChatTurn[] = [];
  const turnIndex = new Map<string, ChatTurn>();
  const stepIndex = new Map<string, TurnStep>();
  let legacyTurn: ChatTurn | null = null;
  let legacyCount = 0;

  function startTurn(id: string, startedAt: unknown): ChatTurn {
    const turn = newTurn(id, startedAt);
    turns.push(turn);
    turnIndex.set(id, turn);
    return turn;
  }

  function turnFor(
    event: SessionEvent,
    payload: Record<string, unknown>
  ): ChatTurn {
    const id = event.turn_id || stringOf(payload.turn_id) || "";
    if (id) {
      return turnIndex.get(id) || startTurn(id, event.created_at);
    }
    if (event.kind === "user/message") {
      legacyCount += 1;
      legacyTurn = startTurn("legacy-" + legacyCount, event.created_at);
      return legacyTurn;
    }
    if (!legacyTurn) {
      legacyCount += 1;
      legacyTurn = startTurn("legacy-" + legacyCount, event.created_at);
    }
    return legacyTurn;
  }

  function stepFor(turn: ChatTurn, callId: string): TurnStep | null {
    if (!callId) {
      return null;
    }
    return stepIndex.get(turn.id + "|" + callId) || null;
  }

  for (const event of events || []) {
    const payload = payloadOf(event);
    const createdAt = Number(event.created_at) || 0;

    switch (event.kind) {
      case "turn/start": {
        const id =
          event.turn_id || stringOf(payload.turn_id) || "turn-" + event.seq;
        if (!turnIndex.has(id)) {
          startTurn(id, createdAt);
        }
        break;
      }
      case "user/message": {
        // REQ-MEM-021: skip a runtime note so it makes no owner bubble.
        if (payload.runtime === true) {
          break;
        }
        const text = userMessageText(payload);
        const attachments = userMessageAttachments(payload);
        if (!text && attachments.length === 0) {
          break;
        }
        const turn = turnFor(event, payload);
        const replyTo = replyTargetOf(payload);
        turn.userTexts.push({
          key: "user-" + event.seq,
          text,
          continue: payload.continue === true,
          ...(attachments.length > 0 ? { attachments } : {}),
          ...(replyTo ? { replyTo } : {}),
        });
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        break;
      }
      case "step/start": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        turn.stepCount += 1;
        const step = Number(payload.step);
        if (Number.isFinite(step) && step > 0) {
          turn.currentStep = step;
        }
        break;
      }
      case "assistant/message": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const thinking = thinkingText(payload.content);
        if (thinking) {
          turn.segments.push({
            kind: "thinking",
            key: "thinking-" + event.seq,
            text: thinking,
          });
        }
        const text = contentText(payload.content);
        const source = stringOf(payload.source);
        const agentReply = source === "agent";
        if (text || (agentReply && payload.passed === true)) {
          /* API-WF-005: an out-of-band message names its source, shown as a
             muted "via …" line above the answer. */
          const sourceName = stringOf(payload.source_name);
          turn.segments.push({
            kind: "text",
            key: "text-" + event.seq,
            text,
            seq: event.seq,
            ...(source ? { source, sourceName } : {}),
            ...(agentReply ? { agentMeta: agentMetaOf(payload) } : {}),
          });
        }
        break;
      }
      case "tool/call": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const callId = stringOf(payload.call_id);
        const result = callId ? results.get(callId) : null;
        const step: TurnStep = {
          key: stringOf(payload.call_id) || String(event.seq),
          callId,
          name: stringOf(payload.name) || "tool",
          input: payload.input,
          status: result ? result.status : "running",
          output: result ? result.output : "",
          approval: null,
          step: turn.currentStep,
        };
        pushStep(turn, step);
        if (step.callId) {
          stepIndex.set(turn.id + "|" + step.callId, step);
          for (const record of turn.approvals.values()) {
            if (record.call_id && record.call_id === step.callId) {
              step.approval = record;
            }
          }
        }
        break;
      }
      case "tool/result": {
        const callId = stringOf(payload.call_id);
        if (!callId) {
          break;
        }
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const step = stepFor(turn, callId);
        if (step) {
          step.status = stringOf(payload.status) || step.status;
          step.output = payload.output || "";
        }
        break;
      }
      case "approval/asked": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const record: TurnApproval = {
          request_id: stringOf(payload.request_id),
          call_id: stringOf(payload.call_id),
          tool: stringOf(payload.tool) || "tool",
          risk: stringOf(payload.risk),
          label: stringOf(payload.label),
          decided: "",
        };
        turn.approvals.set(record.request_id, record);
        const step = stepFor(turn, record.call_id);
        if (step) {
          step.approval = record;
          if (record.tool) {
            step.name = step.name || record.tool;
          }
        }
        break;
      }
      case "approval/decided": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const record = turn.approvals.get(stringOf(payload.request_id));
        if (record) {
          record.decided = stringOf(payload.outcome) || "unavailable";
          const step = stepFor(turn, record.call_id);
          if (step && step.approval) {
            step.approval.decided = record.decided;
          }
        }
        break;
      }
      case "file/sent": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const path = stringOf(payload.path);
        if (path) {
          turn.sentFiles.push({
            key: "sent-" + event.seq,
            path,
            name: stringOf(payload.name) || path.split("/").pop() || path,
            mediaType: stringOf(payload.media_type),
            size: Number(payload.size) || 0,
            caption: stringOf(payload.caption),
            at: createdAt,
          });
        }
        break;
      }
      case "discussion/started":
      case "discussion/message":
      case "discussion/ended": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        turn.discussion = applyDiscussionEvent(turn.discussion, event.kind, payload, event.seq, createdAt);
        break;
      }
      case "turn/end": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        turn.endedAt = createdAt;
        turn.stopReason = stringOf(payload.reason);
        turn.error = payload.error || null;
        break;
      }
      case "memory/captured": {
        // REQ-MEM-030: one muted note per applied operation.
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const ops = Array.isArray(payload.ops) ? payload.ops : [];
        for (const op of ops) {
          const note = memoryOpLine(op);
          if (note) {
            turn.notes.push(note);
          }
        }
        break;
      }
      case "knowledge/compiled": {
        // REQ-KNW-060: one muted note for a knowledge compile pass.
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const note = knowledgeCompiledLine(payload);
        if (note) {
          turn.notes.push(note);
        }
        break;
      }
      default:
        break;
    }
  }

  /* Consecutive tool steps become one collapsible activity group; assistant
     text between them stays a paragraph of its own. */
  for (const turn of turns) {
    const groups: TurnGroup[] = [];
    let steps: TurnStep[] = [];
    for (const segment of turn.segments) {
      if (segment.kind === "step") {
        if (segment.step) {
          steps.push(segment.step);
        }
        continue;
      }
      if (steps.length > 0) {
        groups.push({
          kind: "activity",
          key: "activity-" + turn.key + "-" + groups.length,
          steps,
        });
        steps = [];
      }
      groups.push(segment);
    }
    if (steps.length > 0) {
      groups.push({
        kind: "activity",
        key: "activity-" + turn.key + "-" + groups.length,
        steps,
      });
    }
    turn.groups = groups;
  }

  const lastTurn = turns.length > 0 ? turns[turns.length - 1] || null : null;
  for (const turn of turns) {
    const liveTurn = live.turnId && turn.id === live.turnId;
    const openApproval = Array.from(turn.approvals.values()).some(
      (approval) => !approval.decided
    );
    turn.running = Boolean(
      !turn.endedAt &&
        ((live.running && turn === lastTurn) ||
          liveTurn ||
          (openApproval && turn === lastTurn))
    );
  }

  if (live.running && live.turnId && !turnIndex.has(live.turnId)) {
    const turn = newTurn(live.turnId, live.startedAt || Date.now());
    turn.running = true;
    turns.push(turn);
    turnIndex.set(live.turnId, turn);
  }

  const items: ThreadItem[] = [];
  for (const turn of turns) {
    const empty = turn.userTexts.length === 0 && turn.groups.length === 0;
    if (empty && !turn.running) {
      continue;
    }
    for (const message of turn.userTexts) {
      items.push({
        kind: "user",
        key: message.key,
        text: message.text,
        continue: message.continue === true,
        ...(message.attachments && message.attachments.length > 0
          ? { attachments: message.attachments }
          : {}),
        ...(message.replyTo ? { replyTo: message.replyTo } : {}),
      });
    }
    items.push({ kind: "turn", key: turn.key, turn });
  }
  return items;
}

function agentMetaOf(payload: Record<string, unknown>): AgentReplyMeta {
  const tools: Record<string, number> = {};
  if (payload.tools && typeof payload.tools === "object") {
    for (const [name, count] of Object.entries(payload.tools as Record<string, unknown>)) {
      const n = Number(count);
      if (Number.isFinite(n) && n > 0) {
        tools[name] = n;
      }
    }
  }
  const list = (value: unknown) =>
    Array.isArray(value) ? value.filter((item): item is string => typeof item === "string" && item !== "") : [];
  return {
    childSessionId: typeof payload.child_session_id === "string" ? payload.child_session_id : "",
    childTurnId: typeof payload.child_turn_id === "string" ? payload.child_turn_id : "",
    passed: payload.passed === true,
    failed: payload.failed === true,
    tools,
    notes: list(payload.notes),
    files: list(payload.files),
  };
}
