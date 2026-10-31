import { useEffect, useState } from "react";
import type { SessionEvent } from "../api/sessions";
import { contentText, cut, primaryArg, toolStatusWord, userMessageText } from "../lib/format";
import { Markdown } from "../lib/markdown";
import { TeamBlock } from "./team";
import type { TeamChildRequest } from "./team";

/* One assistant turn of the chat view (REQ-WEB-038). The turn block is kept
   separate from the session view so a team block (spec #10) can wrap or
   replace it later without rewriting the chat view. */

const TONES = ["brand", "success", "warning", "error", "gray"];

const READ_TOOLS = new Set(["read_file", "skill_load", "memory_read"]);
const SEARCH_TOOLS = new Set(["glob", "grep"]);
const WRITE_TOOLS = new Set(["write_file", "edit_file", "memory_write"]);
const COMMAND_TOOLS = new Set(["bash", "terminal"]);
const FETCH_TOOLS = new Set(["web_fetch", "web_search"]);
const DELEGATE_TOOLS = new Set(["task", "task_continue"]);

const CATEGORY_ORDER = [
  "read",
  "search",
  "write",
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
}

export type TurnSegment =
  | { kind: "text"; key: string; text: string }
  | { kind: "step"; key: string; step: TurnStep };

export interface TurnGroup {
  kind: "text" | "activity";
  key: string;
  text?: string;
  steps?: TurnStep[];
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
}

export type ThreadItem =
  | { kind: "user"; key: string; text: string; continue?: boolean }
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

/* The avatar color follows the agent name, so one agent keeps one identity
   across sessions and team threads. */
export function agentTone(name: string | null | undefined): string {
  const text = String(name || "agent");
  let hash = 0;
  for (let index = 0; index < text.length; index += 1) {
    hash = (hash * 31 + text.charCodeAt(index)) >>> 0;
  }
  return TONES[hash % TONES.length] || "brand";
}

export function agentInitial(name: string | null | undefined): string {
  const text = String(name || "").trim();
  return text ? (Array.from(text)[0] || "").toUpperCase() : "?";
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

function Icon({ name, className = "icon-20" }: { name: string; className?: string }) {
  return (
    <img
      className={className}
      src={"/assets/icons/" + name + ".svg"}
      alt=""
      aria-hidden="true"
    />
  );
}

function StepRow({ step }: { step: TurnStep }) {
  const [open, setOpen] = useState(false);
  const waiting = Boolean(step.approval && !step.approval.decided);
  const argument = cut(primaryArg(step.name, step.input), 80);
  const detailId =
    "step-detail-" + String(step.key).replace(/[^a-zA-Z0-9_-]/g, "-");

  return (
    <div className="step">
      <button
        type="button"
        className="step-row"
        aria-expanded={open}
        aria-controls={detailId}
        aria-label={
          stepIntent(step.name) +
          ", " +
          (step.name || "tool") +
          ", " +
          (waiting ? "waiting for approval" : toolStatusWord(step.status))
        }
        onClick={() => setOpen((value) => !value)}
      >
        <span className="step-intent">{stepIntent(step.name)}</span>
        {argument ? (
          <span className="chip chip-arg" title={argument}>
            {argument}
          </span>
        ) : null}
        <span className="chip chip-tool">{step.name || "tool"}</span>
        {waiting ? (
          <span className="badge-ui badge-ui-warning step-waiting">
            <span className="badge-ui-dot" aria-hidden="true"></span>Waiting
          </span>
        ) : null}
        <span className="step-spacer"></span>
        <Icon
          name="chevron-right"
          className={"icon-20 chevron" + (open ? " is-open" : "")}
        />
      </button>
      <div className="step-detail" id={detailId} hidden={!open}>
        <p className="step-detail-status">{stepStatusText(step)}</p>
        {step.output ? (
          <pre className="step-output">{cut(step.output, 2000)}</pre>
        ) : null}
      </div>
    </div>
  );
}

function ActivityGroup({ group }: { group: TurnGroup }) {
  const [open, setOpen] = useState(false);
  const summary = turnSummary(group.steps);
  if (!summary) {
    return null;
  }
  const listId =
    "activity-" + String(group.key).replace(/[^a-zA-Z0-9_-]/g, "-");
  return (
    <div className="activity">
      <button
        type="button"
        className="activity-head"
        aria-expanded={open}
        aria-controls={listId}
        onClick={() => setOpen((value) => !value)}
      >
        <Icon name="activity" className="icon-20 activity-icon" />
        <span className="activity-summary">{summary}</span>
        <Icon
          name="chevron-right"
          className={"icon-20 chevron" + (open ? " is-open" : "")}
        />
      </button>
      <div className="activity-steps" id={listId} hidden={!open}>
        {(group.steps || []).map((step) => (
          <StepRow key={step.key} step={step} />
        ))}
      </div>
    </div>
  );
}

export interface TurnBlockProps {
  turn: Turn;
  agent: string;
  streamingText?: string;
  waitingForInput?: boolean;
  team?: { lead: string; members: string[] } | null;
  rootEvents?: SessionEvent[];
  childEvents?: Record<string, SessionEvent[]>;
  childLive?: Record<string, Turn>;
  onLoadChildren?: ((requests: TeamChildRequest[]) => void) | null;
  /* REQ-TFB-004: the status card shows under the session's last turn only. */
  showStatusCard?: boolean;
  onContinue?: (() => void) | null;
  continueBusy?: boolean;
  /* API-TFB-003: the refusal text of a failed Continue, shown in the card. */
  continueError?: string;
}

export function TurnBlock({
  turn,
  agent,
  streamingText = "",
  waitingForInput = false,
  team = null,
  rootEvents = [],
  childEvents = {},
  childLive = {},
  onLoadChildren = null,
  showStatusCard = false,
  onContinue = null,
  continueBusy = false,
  continueError = "",
}: TurnBlockProps) {
  const [now, setNow] = useState(() => Date.now());
  const [cardDismissed, setCardDismissed] = useState(false);

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

  return (
    <article className="turn">
      <div className="turn-head">
        <span
          className={"agent-avatar tone-" + agentTone(agent)}
          aria-hidden="true"
        >
          {agentInitial(agent)}
        </span>
        <span className="turn-agent">{agent}</span>
        <span className="turn-status">{statusText}</span>
        <span className="turn-spacer"></span>
        <span className="turn-time">{formatClock(startedAt)}</span>
      </div>
      {(turn.groups || []).map((segment) =>
        segment.kind === "text" ? (
          <div className="turn-text" key={segment.key}>
            <Markdown text={segment.text} />
          </div>
        ) : (
          <ActivityGroup key={segment.key} group={segment} />
        )
      )}
      {streamingText ? (
        <div className="turn-text turn-text-streaming">
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
}

export function UserBubble({
  text,
  continue: isContinue = false,
}: UserBubbleProps) {
  if (isContinue) {
    /* API-TFB-004: a continue request is a chip, not owner text. */
    return (
      <div className="chat-user-row">
        <span className="chip">↻ Continued</span>
      </div>
    );
  }
  return (
    <div className="chat-user-row">
      <div className="chat-bubble">{text}</div>
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
  };
}

function pushStep(turn: ChatTurn, step: TurnStep): void {
  turn.segments.push({ kind: "step", key: "step-" + step.key, step });
  turn.steps.push(step);
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
        const text = userMessageText(payload);
        if (!text) {
          break;
        }
        const turn = turnFor(event, payload);
        turn.userTexts.push({
          key: "user-" + event.seq,
          text,
          continue: payload.continue === true,
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
        const text = contentText(payload.content);
        if (text) {
          turn.segments.push({
            kind: "text",
            key: "text-" + event.seq,
            text,
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
      case "turn/end": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        turn.endedAt = createdAt;
        turn.stopReason = stringOf(payload.reason);
        turn.error = payload.error || null;
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
      });
    }
    items.push({ kind: "turn", key: turn.key, turn });
  }
  return items;
}
