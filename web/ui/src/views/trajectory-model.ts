import type { SessionEvent } from "../api/sessions";
import {
  contentText,
  cut,
  outcomeWords,
  primaryArg,
  toolStatusWord,
  userMessageText,
} from "../lib/format";
import { isAttachmentBlock } from "../lib/attachment-blocks";

/* Pure projection of the session event log into the Trajectory ledger
   (REQ-TRJ-002..REQ-TRJ-010). The builder walks the log once, in seq order, and
   returns turns, steps, records, totals, and timing spans. It never estimates a
   value: a token count, duration, or model that the log does not carry is left
   undefined so the view can render "—". */

export type RecordKind =
  | "system"
  | "context"
  | "user"
  | "assistant"
  | "retry"
  | "tool"
  | "subtool"
  | "approval"
  | "question"
  | "compacted"
  | "pruned"
  | "memory"
  | "setting"
  | "other";

export type RecordStatus =
  | "running"
  | "success"
  | "error"
  | "rejected"
  | "failed";

export interface Usage {
  input: number;
  cached: number;
  output: number;
}

export interface PromptSection {
  name: string;
  text: string;
}

export interface MemoryOp {
  target: string;
  action: string;
  content: string;
  oldText: string;
}

export interface TrajRecord {
  key: string;
  kind: RecordKind;
  /* The original kind of a SUBTOOL record (assistant or tool). */
  innerKind?: RecordKind;
  title: string;
  seqs: number[];
  events: SessionEvent[];
  startMs?: number;
  endMs?: number;
  status?: RecordStatus;
  usage?: Usage;
  cumulative?: Usage;
  thinkChars?: number;
  model?: string;
  provider?: string;
  toolCalls?: number;
  requestNo?: number;
  callId?: string;
  toolName?: string;
  input?: unknown;
  output?: string;
  images?: number;
  children?: TrajRecord[];
  childSessionId?: string;
  sections?: PromptSection[];
  text?: string;
  thinking?: string;
  summary?: string;
  ops?: MemoryOp[];
  attachmentCount?: number;
  continued?: boolean;
  errorClass?: string;
  errorStatus?: number;
  errorMessage?: string;
  outcome?: string;
  risk?: string;
  waitMs?: number;
  sourceSeq?: number;
  rawKind?: string;
}

export interface TrajStep {
  no: number;
  provider: string;
  model: string;
  records: TrajRecord[];
}

export interface TrajTurn {
  no: number;
  id: string;
  startMs: number;
  endMs?: number;
  reason?: string;
  error?: string;
  usage: Usage;
  usageRecorded?: boolean;
  steps: TrajStep[];
  loose: TrajRecord[];
}

export type TrajectorySection =
  | { type: "turn"; turn: TrajTurn }
  | { type: "between"; records: TrajRecord[] };

export type SpanKind = "assistant" | "tool" | "approval";

export interface TrajectorySpan {
  key: string;
  recordKey: string;
  kind: SpanKind;
  label: string;
  startMs: number;
  endMs?: number;
  failed?: boolean;
  turnNo: number;
}

export interface Trajectory {
  sections: TrajectorySection[];
  totals: {
    turns: number;
    requests: number;
    toolCalls: number;
    usage: Usage;
  };
  spans: TrajectorySpan[];
}

/* AgentLabel resolves an agent ID to the name a person reads. The builder
   takes one so a record title never shows a raw config ID. */
export type AgentLabel = (name: string) => string;

const KIND_LABELS: Record<RecordKind, string> = {
  system: "SYSTEM",
  context: "CONTEXT",
  user: "USER",
  assistant: "ASSISTANT",
  retry: "RETRY",
  tool: "TOOL",
  subtool: "SUBTOOL",
  approval: "APPROVAL",
  question: "QUESTION",
  compacted: "COMPACTED",
  pruned: "PRUNED",
  memory: "MEMORY",
  setting: "SETTING",
  other: "EVENT",
};

/* The first-column label of a record. An unknown event kind is shown as its
   own name, so nothing in the log is hidden (REQ-TRJ-002). */
export function recordKindLabel(record: TrajRecord): string {
  if (record.kind === "other") {
    return record.rawKind || KIND_LABELS.other;
  }
  return KIND_LABELS[record.kind];
}

export function emptyUsage(): Usage {
  return { input: 0, cached: 0, output: 0 };
}

function cloneUsage(usage: Usage): Usage {
  return { input: usage.input, cached: usage.cached, output: usage.output };
}

function addUsage(target: Usage, usage: Usage): void {
  target.input += usage.input;
  target.cached += usage.cached;
  target.output += usage.output;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object"
    ? (value as Record<string, unknown>)
    : {};
}

function payloadOf(event: SessionEvent): Record<string, unknown> {
  return asRecord(event.payload);
}

function stringOf(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function numberOrUndefined(value: unknown): number | undefined {
  const number = Number(value);
  return Number.isFinite(number) ? number : undefined;
}

function charCount(text: string): number {
  return Array.from(text).length;
}

function preview(text: string, limit = 120): string {
  return cut(String(text || "").replace(/\s+/g, " ").trim(), limit);
}

function usageOf(value: unknown): Usage | undefined {
  if (!value || typeof value !== "object") {
    return undefined;
  }
  const record = value as Record<string, unknown>;
  if (
    typeof record.input_tokens !== "number" &&
    typeof record.output_tokens !== "number" &&
    typeof record.cached_input_tokens !== "number"
  ) {
    return undefined;
  }
  return {
    input: numberOrUndefined(record.input_tokens) ?? 0,
    cached: numberOrUndefined(record.cached_input_tokens) ?? 0,
    output: numberOrUndefined(record.output_tokens) ?? 0,
  };
}

function blocksOf(value: unknown): Record<string, unknown>[] {
  if (!Array.isArray(value)) {
    return [];
  }
  const blocks: Record<string, unknown>[] = [];
  for (const block of value) {
    if (block && typeof block === "object") {
      blocks.push(block as Record<string, unknown>);
    }
  }
  return blocks;
}

function thinkingText(content: unknown): string {
  const pieces: string[] = [];
  for (const block of blocksOf(content)) {
    if (block.type === "thinking" && typeof block.text === "string" && block.text) {
      pieces.push(block.text);
    }
  }
  return pieces.join("\n\n");
}

function thinkingChars(content: unknown): number {
  let count = 0;
  for (const block of blocksOf(content)) {
    if (block.type === "thinking" && typeof block.text === "string") {
      count += charCount(block.text);
    }
  }
  return count;
}

function toolUseCount(content: unknown): number {
  return blocksOf(content).filter((block) => block.type === "tool_use").length;
}

function imageCount(content: unknown): number {
  /* REQ-CHF-001: file attachments count like images. */
  return blocksOf(content).filter(
    (block) => block.type === "image" || isAttachmentBlock(block)
  ).length;
}

function parseJsonObject(text: string): Record<string, unknown> | null {
  try {
    const value: unknown = JSON.parse(text);
    return value && typeof value === "object"
      ? (value as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

function sectionsOf(value: unknown): PromptSection[] {
  const sections: PromptSection[] = [];
  for (const block of blocksOf(value)) {
    const name = stringOf(block.name);
    const text = stringOf(block.text);
    if (name || text) {
      sections.push({ name, text });
    }
  }
  return sections;
}

/* Sections whose text changed, plus sections that were added or removed. */
function changedSectionNames(
  previous: PromptSection[],
  next: PromptSection[]
): string[] {
  const before = new Map(previous.map((section) => [section.name, section.text]));
  const names: string[] = [];
  for (const section of next) {
    if (before.get(section.name) !== section.text) {
      names.push(section.name);
    }
  }
  for (const section of previous) {
    if (!next.some((entry) => entry.name === section.name)) {
      names.push(section.name);
    }
  }
  return names;
}

function toolState(value: unknown): { status: RecordStatus; word: string } {
  const word = toolStatusWord(value);
  switch (word) {
    case "ok":
      return { status: "success", word: "ok" };
    case "error":
      return { status: "error", word: "error" };
    case "rejected":
      return { status: "rejected", word: "rejected" };
    case "cancelled":
      return { status: "failed", word: "cancelled" };
    case "running":
      return { status: "running", word: "running" };
    default:
      return { status: "error", word };
  }
}

function approvalState(outcome: string): RecordStatus {
  if (outcome === "allowed_once" || outcome === "allowed_session") {
    return "success";
  }
  if (outcome === "rejected") {
    return "rejected";
  }
  if (outcome === "cancelled" || outcome === "unavailable") {
    return "failed";
  }
  return "failed";
}

function questionState(outcome: string): RecordStatus {
  if (outcome === "answered") {
    return "success";
  }
  return outcome ? "failed" : "running";
}

function toolTitle(
  name: string,
  input: unknown,
  statusWord: string,
  hasInput: boolean
): string {
  const argument = hasInput
    ? preview(primaryArg(name, input), 80).replace(/[\s·]+$/, "")
    : "";
  const parts = [name || "tool"];
  if (argument) {
    parts.push(argument);
  }
  parts.push(statusWord || "running");
  return parts.join(" · ");
}

interface StepState {
  step: TrajStep;
  latestAssistant: TrajRecord | null;
  pendingAssistant: TrajRecord | null;
}

interface TurnState {
  turn: TrajTurn;
}

interface BuildState {
  sections: TrajectorySection[];
  turns: TrajTurn[];
  currentTurn: TurnState | null;
  currentStep: StepState | null;
  requestCount: number;
  runningUsage: Usage;
  systemSections: PromptSection[];
  systemSeen: boolean;
  toolsByCallId: Map<string, TrajRecord>;
  approvalsByRequestId: Map<string, TrajRecord>;
  questionsByRequestId: Map<string, TrajRecord>;
  topRecords: TrajRecord[];
}

function newState(): BuildState {
  return {
    sections: [],
    turns: [],
    currentTurn: null,
    currentStep: null,
    requestCount: 0,
    runningUsage: emptyUsage(),
    systemSections: [],
    systemSeen: false,
    toolsByCallId: new Map(),
    approvalsByRequestId: new Map(),
    questionsByRequestId: new Map(),
    topRecords: [],
  };
}

function betweenRecords(state: BuildState): TrajRecord[] {
  const last = state.sections[state.sections.length - 1];
  if (last && last.type === "between") {
    return last.records;
  }
  const section: TrajectorySection = { type: "between", records: [] };
  state.sections.push(section);
  return section.records;
}

function pushRecord(state: BuildState, record: TrajRecord): void {
  if (state.currentStep) {
    state.currentStep.step.records.push(record);
    state.topRecords.push(record);
    return;
  }
  if (state.currentTurn) {
    state.currentTurn.turn.loose.push(record);
    state.topRecords.push(record);
    return;
  }
  betweenRecords(state).push(record);
  state.topRecords.push(record);
}

function startTurn(state: BuildState, event: SessionEvent, id: string): void {
  const turn: TrajTurn = {
    no: state.turns.length + 1,
    id: id || "turn-" + event.seq,
    startMs: Number(event.created_at) || 0,
    usage: emptyUsage(),
    steps: [],
    loose: [],
  };
  state.turns.push(turn);
  state.sections.push({ type: "turn", turn });
  state.currentTurn = { turn };
  state.currentStep = null;
}

function ensureTurn(state: BuildState, event: SessionEvent, id: string): void {
  if (!state.currentTurn) {
    startTurn(state, event, id);
  }
}

function startPendingAssistant(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): TrajRecord {
  state.requestCount += 1;
  const record: TrajRecord = {
    key: "assistant-" + event.seq,
    kind: "assistant",
    title: "(no content)",
    seqs: [event.seq],
    events: [event],
    startMs: Number(event.created_at) || 0,
    status: "running",
    requestNo: state.requestCount,
    provider: stringOf(payload.provider),
    model: stringOf(payload.model),
  };
  return record;
}

function startStep(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  ensureTurn(
    state,
    event,
    stringOf(payload.turn_id) || event.turn_id || "turn-" + event.seq
  );
  const turn = state.currentTurn;
  if (!turn) {
    return;
  }
  const stepNo =
    numberOrUndefined(payload.step) ?? turn.turn.steps.length + 1;
  const step: TrajStep = {
    no: stepNo,
    provider: stringOf(payload.provider),
    model: stringOf(payload.model),
    records: [],
  };
  turn.turn.steps.push(step);
  const assistant = startPendingAssistant(state, event, payload);
  step.records.push(assistant);
  state.topRecords.push(assistant);
  state.currentStep = {
    step,
    latestAssistant: assistant,
    pendingAssistant: assistant,
  };
}

function completeAssistant(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const content = payload.content;
  let target: TrajRecord | null = null;
  if (state.currentStep && state.currentStep.latestAssistant) {
    const candidate = state.currentStep.latestAssistant;
    if (candidate.status === "running" && candidate.endMs === undefined) {
      target = candidate;
    }
  }
  if (!target) {
    const assistant = startPendingAssistant(state, event, {
      provider: state.currentStep ? state.currentStep.step.provider : "",
      model: state.currentStep ? state.currentStep.step.model : "",
    });
    if (state.currentStep) {
      state.currentStep.step.records.push(assistant);
      state.currentStep.latestAssistant = assistant;
      state.currentStep.pendingAssistant = assistant;
    } else {
      /* A message without a step/start has no recorded start time; leaving it
         unset keeps the Time column and the Timing tab from inventing one. */
      assistant.startMs = undefined;
      pushRecord(state, assistant);
    }
    target = assistant;
  }

  const text = contentText(content);
  const thinking = thinkingText(content);
  target.endMs = Number(event.created_at) || 0;
  target.status = "success";
  target.text = text;
  target.thinking = thinking;
  target.thinkChars = thinking ? thinkingChars(content) : undefined;
  target.toolCalls = toolUseCount(content);
  target.title = text
    ? preview(text)
    : toolUseCount(content) > 0
      ? "(tool call only)"
      : "(no content)";
  if (!target.events.some((entry) => entry.seq === event.seq)) {
    target.seqs.push(event.seq);
    target.events.push(event);
  }
  if (state.currentStep) {
    state.currentStep.pendingAssistant = target;
    state.currentStep.latestAssistant = target;
  }
}

function completeStep(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  if (!state.currentTurn) {
    return;
  }
  const stepState = state.currentStep;
  const usage = usageOf(payload.usage);
  if (usage) {
    addUsage(state.currentTurn.turn.usage, usage);
    state.currentTurn.turn.usageRecorded = true;
    addUsage(state.runningUsage, usage);
  }
  if (stepState) {
    const target = stepState.latestAssistant || stepState.pendingAssistant;
    if (target) {
      if (usage) {
        target.usage = cloneUsage(usage);
        target.cumulative = cloneUsage(state.runningUsage);
      }
      if (!stepState.step.provider) {
        stepState.step.provider = stringOf(payload.provider);
      }
      if (!stepState.step.model) {
        stepState.step.model = stringOf(payload.model);
      }
      if (!target.provider) {
        target.provider = stringOf(payload.provider);
      }
      if (!target.model) {
        target.model = stringOf(payload.model);
      }
      if (!target.events.some((entry) => entry.seq === event.seq)) {
        target.seqs.push(event.seq);
        target.events.push(event);
      }
    }
  }
  state.currentStep = null;
}

function finishTurn(state: BuildState): void {
  const turnState = state.currentTurn;
  if (!turnState) {
    return;
  }
  const turn = turnState.turn;
  const finishers: TrajRecord[] = [...turn.loose];
  for (const step of turn.steps) {
    finishers.push(...step.records);
  }
  for (const record of finishers) {
    finishRecord(record, turn);
  }
  state.currentTurn = null;
  state.currentStep = null;
}

function finishRecord(record: TrajRecord, turn: TrajTurn): void {
  if (record.status === "running") {
    record.status = "failed";
    if (record.kind === "assistant" && record.title === "(no content)") {
      record.title = turn.error
        ? "Failed: " + preview(turn.error)
        : "Request failed";
    }
    if (
      (record.kind === "tool" || record.kind === "subtool") &&
      record.toolName
    ) {
      record.title = toolTitle(
        record.toolName,
        record.input,
        "failed",
        record.input !== undefined
      );
    }
  }
  for (const child of record.children || []) {
    finishRecord(child, turn);
  }
}

function setChildSessionFromOutput(record: TrajRecord): void {
  const name = record.toolName;
  if ((name !== "task" && name !== "task_continue") || !record.output) {
    return;
  }
  const parsed = parseJsonObject(record.output);
  const sessionId = parsed ? stringOf(parsed.session_id) : "";
  if (sessionId) {
    record.childSessionId = sessionId;
  }
}

function pushSystem(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const sections = sectionsOf(payload.sections);
  const names = sections.map((section) => section.name).filter(Boolean);
  let title = "System prompt";
  if (state.systemSeen) {
    const changed = changedSectionNames(state.systemSections, sections);
    title = "System prompt changed";
    if (changed.length > 0) {
      title += " · " + changed.join(", ");
    }
  } else if (names.length > 0) {
    title += " · " + names.join(", ");
  }
  state.systemSeen = true;
  state.systemSections = sections;
  const record: TrajRecord = {
    key: "system-" + event.seq,
    kind: "system",
    title,
    seqs: [event.seq],
    events: [event],
    sections,
    text: stringOf(payload.text),
  };
  pushRecord(state, record);
}

/* "Claude Code session · opus · 2.1.319" for the session meta of an external
   runtime (spec 38). It never touches the system prompt state. */
export function runtimeSessionTitle(payload: Record<string, unknown>): string {
  const runtime = stringOf(payload.runtime);
  const name = runtime === "claude-code" || runtime === "" ? "Claude Code" : runtime;
  const parts = [name + " session"];
  const model = stringOf(payload.model);
  if (model) {
    parts.push(model);
  }
  const version = stringOf(payload.version);
  if (version) {
    parts.push(version);
  }
  return parts.join(" · ");
}

function pushRuntimeSession(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  pushRecord(state, {
    key: "setting-" + event.seq,
    kind: "setting",
    title: runtimeSessionTitle(payload),
    seqs: [event.seq],
    events: [event],
    model: stringOf(payload.model) || undefined,
  });
}

function pushUser(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const content = payload.content;
  const contextBlocks = numberOrUndefined(payload.context_blocks) ?? 0;
  const runtime = payload.runtime === true;
  const blocks = Array.isArray(content) ? content : [];
  const contextPart = contextBlocks > 0 ? blocks.slice(0, contextBlocks) : [];
  const ownerPart = contextBlocks > 0 ? blocks.slice(contextBlocks) : blocks;

  if (runtime) {
    const text = userMessageText(payload);
    pushRecord(state, {
      key: "context-" + event.seq + "-runtime",
      kind: "context",
      title: text ? "lana note · " + preview(text, 100) : "lana note",
      seqs: [event.seq],
      events: [event],
      text,
    });
  } else {
    if (contextPart.length > 0) {
      const text = contentText(contextPart);
      const contextPreview = preview(text, 120);
      pushRecord(state, {
        key: "context-" + event.seq,
        kind: "context",
        title: contextPreview
          ? contextPreview
          : "Injected context · " +
            contextPart.length +
            " block" +
            (contextPart.length === 1 ? "" : "s"),
        seqs: [event.seq],
        events: [event],
        text,
      });
    }

    const text = userMessageText(payload);
    const attachments = imageCount(ownerPart);
    const continued = payload.continue === true;
    if (text || attachments > 0 || continued) {
      let title = preview(text);
      if (!title) {
        title = continued
          ? "↻ Continued"
          : attachments + " attachment" + (attachments === 1 ? "" : "s");
      } else if (continued) {
        title += " · ↻ continued";
      }
      if (attachments > 0 && title.indexOf("attachment") < 0) {
        title +=
          " · " + attachments + " attachment" + (attachments === 1 ? "" : "s");
      }
      pushRecord(state, {
        key: "user-" + event.seq,
        kind: "user",
        title,
        seqs: [event.seq],
        events: [event],
        text,
        attachmentCount: attachments,
        continued,
      });
    }
  }

  const promptSections = sectionsOf(payload.prompt_sections);
  if (promptSections.length > 0) {
    const changed = changedSectionNames(state.systemSections, promptSections);
    state.systemSections = promptSections;
    const title = changed.length
      ? "Prompt sections · " + changed.join(", ")
      : "Prompt sections · updated";
    pushRecord(state, {
      key: "context-" + event.seq + "-sections",
      kind: "context",
      title,
      seqs: [event.seq],
      events: [event],
      sections: promptSections,
    });
  }
}

function pushRetry(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const error = asRecord(payload.error);
  const errorClass = stringOf(error.class);
  const status = numberOrUndefined(error.status);
  const message = stringOf(error.message);
  const titleParts = [errorClass || "Retry"];
  if (status !== undefined) {
    titleParts.push("HTTP " + status);
  }
  if (message) {
    titleParts.push(preview(message, 100));
  }
  pushRecord(state, {
    key: "retry-" + event.seq,
    kind: "retry",
    title: titleParts.join(" · "),
    seqs: [event.seq],
    events: [event],
    status: "failed",
    errorClass,
    errorStatus: status,
    errorMessage: message,
    model: stringOf(payload.model),
    provider: stringOf(payload.provider),
  });
}

function pushToolCall(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const callId = stringOf(payload.call_id);
  const name = stringOf(payload.name) || "tool";
  const input = payload.input;
  const key = callId ? "tool-" + callId : "tool-" + event.seq;
  const record: TrajRecord = {
    key,
    kind: "tool",
    title: toolTitle(name, input, "running", input !== undefined),
    seqs: [event.seq],
    events: [event],
    startMs: Number(event.created_at) || 0,
    status: "running",
    callId,
    toolName: name,
    input,
    model: state.currentStep ? state.currentStep.step.model : "",
    provider: state.currentStep ? state.currentStep.step.provider : "",
  };
  if (callId) {
    state.toolsByCallId.set(callId, record);
  }
  const assistant = state.currentStep ? state.currentStep.latestAssistant : null;
  if (assistant) {
    assistant.children = assistant.children || [];
    assistant.children.push(record);
  } else {
    pushRecord(state, record);
  }
}

function pushToolResult(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const callId = stringOf(payload.call_id);
  const stateOfTool = toolState(payload.status);
  const output = stringOf(payload.output);
  const images = blocksOf(payload.images).length;
  const record = callId ? state.toolsByCallId.get(callId) : undefined;
  if (record) {
    record.status = stateOfTool.status;
    record.endMs = Number(event.created_at) || 0;
    record.output = output;
    record.images = images;
    record.title = toolTitle(
      record.toolName || "tool",
      record.input,
      stateOfTool.word,
      record.input !== undefined
    );
    record.seqs.push(event.seq);
    record.events.push(event);
    setChildSessionFromOutput(record);
    return;
  }
  pushRecord(state, {
    key: "tool-result-" + event.seq,
    kind: "tool",
    title: "Tool result · " + stateOfTool.word,
    seqs: [event.seq],
    events: [event],
    status: stateOfTool.status,
    endMs: Number(event.created_at) || 0,
    callId,
    output,
    images,
  });
}

function pushApprovalAsked(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const requestId = stringOf(payload.request_id);
  const tool = stringOf(payload.tool) || "tool";
  const risk = stringOf(payload.risk);
  const record: TrajRecord = {
    key: "approval-" + (requestId || event.seq),
    kind: "approval",
    title: tool + (risk ? " · " + risk : "") + " · waiting",
    seqs: [event.seq],
    events: [event],
    startMs: Number(event.created_at) || 0,
    status: "running",
    callId: stringOf(payload.call_id),
    toolName: tool,
    risk,
  };
  if (requestId) {
    state.approvalsByRequestId.set(requestId, record);
  }
  pushRecord(state, record);
}

function pushApprovalDecided(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const requestId = stringOf(payload.request_id);
  const outcome = stringOf(payload.outcome);
  const risk = stringOf(payload.risk);
  const tool = stringOf(payload.tool) || "approval";
  const record = requestId
    ? state.approvalsByRequestId.get(requestId)
    : undefined;
  if (record) {
    record.outcome = outcome;
    record.status = approvalState(outcome);
    record.endMs = Number(event.created_at) || 0;
    if (record.startMs !== undefined && record.endMs !== undefined) {
      record.waitMs = Math.max(0, record.endMs - record.startMs);
    }
    record.title =
      (record.toolName || tool) +
      (record.risk ? " · " + record.risk : "") +
      " · " +
      outcomeWords(outcome);
    record.seqs.push(event.seq);
    record.events.push(event);
    return;
  }
  pushRecord(state, {
    key: "approval-" + (requestId || event.seq),
    kind: "approval",
    title: tool + (risk ? " · " + risk : "") + " · " + outcomeWords(outcome),
    seqs: [event.seq],
    events: [event],
    status: approvalState(outcome),
    outcome,
    risk,
  });
}

function pushQuestionAsked(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const requestId = stringOf(payload.request_id);
  const record: TrajRecord = {
    key: "question-" + (requestId || event.seq),
    kind: "question",
    title: "Question · waiting",
    seqs: [event.seq],
    events: [event],
    startMs: Number(event.created_at) || 0,
    status: "running",
    callId: stringOf(payload.call_id),
  };
  if (requestId) {
    state.questionsByRequestId.set(requestId, record);
  }
  pushRecord(state, record);
}

function pushQuestionAnswered(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const requestId = stringOf(payload.request_id);
  const outcome = stringOf(payload.outcome);
  const reason = stringOf(payload.reason);
  let record = requestId
    ? state.questionsByRequestId.get(requestId)
    : undefined;
  if (!record) {
    record = {
      key: "question-" + (requestId || event.seq),
      kind: "question",
      title: "Question",
      seqs: [],
      events: [],
      outcome,
    };
    pushRecord(state, record);
  }
  record.outcome = outcome;
  record.status = questionState(outcome);
  record.endMs = Number(event.created_at) || 0;
  if (record.startMs !== undefined && record.endMs !== undefined) {
    record.waitMs = Math.max(0, record.endMs - record.startMs);
  }
  record.title =
    "Question · " +
    (outcome || "closed") +
    (reason ? " · " + reason : "");
  if (!record.events.some((entry) => entry.seq === event.seq)) {
    record.seqs.push(event.seq);
    record.events.push(event);
  }
}

function pushCompaction(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const before = numberOrUndefined(payload.tokens_before);
  const after = numberOrUndefined(payload.tokens_after);
  const usage = usageOf(payload.usage);
  const record: TrajRecord = {
    key: "compacted-" + event.seq,
    kind: "compacted",
    title:
      before !== undefined && after !== undefined
        ? before + " → " + after + " tokens"
        : "Compaction summary",
    seqs: [event.seq],
    events: [event],
    summary: stringOf(payload.summary),
    model: stringOf(payload.model),
  };
  if (usage) {
    record.usage = cloneUsage(usage);
    addUsage(state.runningUsage, usage);
    record.cumulative = cloneUsage(state.runningUsage);
  }
  pushRecord(state, record);
}

function pushPruned(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const callId = stringOf(payload.call_id);
  const sourceSeq = numberOrUndefined(payload.source_seq);
  pushRecord(state, {
    key: "pruned-" + event.seq,
    kind: "pruned",
    title:
      (callId || "tool result") +
      (sourceSeq !== undefined ? " · source seq " + sourceSeq : ""),
    seqs: [event.seq],
    events: [event],
    callId,
    sourceSeq,
    output: stringOf(payload.output),
  });
}

function pushMemory(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  const ops: MemoryOp[] = [];
  for (const op of blocksOf(payload.ops)) {
    ops.push({
      target: stringOf(op.target),
      action: stringOf(op.action),
      content: stringOf(op.content),
      oldText: stringOf(op.old_text),
    });
  }
  const usage = usageOf(payload.usage);
  const record: TrajRecord = {
    key: "memory-" + event.seq,
    kind: "memory",
    title: ops.length + " operation" + (ops.length === 1 ? "" : "s"),
    seqs: [event.seq],
    events: [event],
    ops,
  };
  if (usage) {
    record.usage = cloneUsage(usage);
    addUsage(state.runningUsage, usage);
    record.cumulative = cloneUsage(state.runningUsage);
  }
  pushRecord(state, record);
}

function pushSetting(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>,
  label: AgentLabel
): void {
  if (event.kind === "model/selection") {
    const provider = stringOf(payload.provider);
    const model = stringOf(payload.model);
    const effort = stringOf(payload.reasoning_effort);
    const agent = stringOf(payload.agent);
    let title = "Model · " + (provider ? provider + ":" : "") + (model || "unknown");
    if (effort) {
      title += " · thinking " + effort;
    }
    if (agent) {
      title += " · " + label(agent);
    }
    pushRecord(state, {
      key: "setting-" + event.seq,
      kind: "setting",
      title,
      seqs: [event.seq],
      events: [event],
      provider,
      model,
    });
    return;
  }
  pushRecord(state, {
    key: "setting-" + event.seq,
    kind: "setting",
    title: "Policy · " + (stringOf(payload.policy) || "unknown"),
    seqs: [event.seq],
    events: [event],
  });
}

function pushOther(
  state: BuildState,
  event: SessionEvent,
  payload: Record<string, unknown>
): void {
  pushRecord(state, {
    key: "event-" + event.seq,
    kind: "other",
    title: event.kind,
    seqs: [event.seq],
    events: [event],
    rawKind: event.kind,
    text: preview(JSON.stringify(payload), 200),
  });
}

function walk(events: SessionEvent[], label: AgentLabel): BuildState {
  const state = newState();
  const ordered = events.slice().sort((left, right) => left.seq - right.seq);
  for (const event of ordered) {
    const payload = payloadOf(event);
    switch (event.kind) {
      case "turn/start": {
        startTurn(
          state,
          event,
          stringOf(payload.turn_id) || event.turn_id || "turn-" + event.seq
        );
        break;
      }
      case "turn/end": {
        if (state.currentTurn) {
          const turn = state.currentTurn.turn;
          turn.endMs = Number(event.created_at) || 0;
          turn.reason = stringOf(payload.reason) || turn.reason;
          const error = asRecord(payload.error);
          if (stringOf(error.message)) {
            turn.error = stringOf(error.message);
          }
          finishTurn(state);
        }
        break;
      }
      case "step/start":
        startStep(state, event, payload);
        break;
      case "step/end":
        completeStep(state, event, payload);
        break;
      case "system/message":
        /* API-CCR-002: subtype "runtime" is an external runtime's session
           meta, not a prompt head; it shows as a small setting chip. */
        if (payload.subtype === "runtime") {
          pushRuntimeSession(state, event, payload);
        } else {
          pushSystem(state, event, payload);
        }
        break;
      case "user/message":
        pushUser(state, event, payload);
        break;
      case "assistant/message":
        completeAssistant(state, event, payload);
        break;
      case "assistant/attempt":
        pushRetry(state, event, payload);
        break;
      case "tool/call":
        pushToolCall(state, event, payload);
        break;
      case "tool/result":
        pushToolResult(state, event, payload);
        break;
      case "approval/asked":
        pushApprovalAsked(state, event, payload);
        break;
      case "approval/decided":
        pushApprovalDecided(state, event, payload);
        break;
      case "approval/policy":
        pushSetting(state, event, payload, label);
        break;
      case "question/asked":
        pushQuestionAsked(state, event, payload);
        break;
      case "question/answered":
        pushQuestionAnswered(state, event, payload);
        break;
      case "model/selection":
        pushSetting(state, event, payload, label);
        break;
      case "compaction/summary":
        pushCompaction(state, event, payload);
        break;
      case "tool_result_pruned":
        pushPruned(state, event, payload);
        break;
      case "memory/captured":
        pushMemory(state, event, payload);
        break;
      default:
        pushOther(state, event, payload);
        break;
    }
  }
  return state;
}

/* Tool counts are known only after every tool/call of the request has landed.
   The assistant/message content usually carries the same count, but the log's
   own child records are authoritative. */
function finalizeToolCounts(state: BuildState): void {
  for (const record of state.topRecords) {
    if (record.kind !== "assistant") {
      continue;
    }
    const tools = (record.children || []).filter(
      (child) => child.kind === "tool"
    ).length;
    record.toolCalls = tools || record.toolCalls || 0;
    if (!record.text && tools > 0 && record.title === "(no content)") {
      record.title = "(tool call only)";
    }
  }
}

function asSubtool(record: TrajRecord, prefix: string): TrajRecord {
  return {
    ...record,
    key: prefix + record.key,
    kind: "subtool",
    innerKind: record.kind === "subtool" ? record.innerKind : record.kind,
    childSessionId: undefined,
    children: undefined,
  };
}

/* The child log keeps its own turns and steps, but the parent shows the
   child's assistant and tool records flat, one level deep (REQ-TRJ-009). */
function childRecords(
  events: SessionEvent[],
  prefix: string,
  label: AgentLabel
): TrajRecord[] {
  const child = walk(events, label);
  const records: TrajRecord[] = [];
  for (const record of child.topRecords) {
    if (record.kind === "assistant") {
      records.push(asSubtool(record, prefix));
      for (const tool of record.children || []) {
        if (tool.kind === "tool") {
          records.push(asSubtool(tool, prefix));
        }
      }
      continue;
    }
    if (record.kind === "tool") {
      records.push(asSubtool(record, prefix));
    }
  }
  return records;
}

function attachChildren(
  state: BuildState,
  childEvents: Record<string, SessionEvent[]>,
  label: AgentLabel
): void {
  const childrenById = new Map<string, SessionEvent[]>();
  for (const id of Object.keys(childEvents || {})) {
    const list = childEvents[id];
    if (Array.isArray(list) && list.length > 0) {
      childrenById.set(id, list);
    }
  }
  /* A child session/created event names the parent tool call. It is the
     authoritative link when the tool result did not carry a session id. */
  for (const [id, list] of childrenById) {
    let parentCallId = "";
    for (const event of list) {
      if (event.kind !== "session/created") {
        continue;
      }
      parentCallId = stringOf(payloadOf(event).parent_call_id);
      break;
    }
    if (!parentCallId) {
      continue;
    }
    const record = state.toolsByCallId.get(parentCallId);
    if (record) {
      record.childSessionId = id;
    }
  }
  for (const record of state.toolsByCallId.values()) {
    if (!record.childSessionId) {
      continue;
    }
    const list = childrenById.get(record.childSessionId);
    if (!list) {
      continue;
    }
    record.children = childRecords(
      list,
      "subtool-" + record.childSessionId + "-",
      label
    );
  }
}

function buildSpans(state: BuildState): TrajectorySpan[] {
  const spans: TrajectorySpan[] = [];
  for (const turn of state.turns) {
    const records: TrajRecord[] = [...turn.loose];
    for (const step of turn.steps) {
      records.push(...step.records);
    }
    for (const record of records) {
      collectSpans(record, turn.no, spans);
    }
  }
  return spans;
}

function collectSpans(
  record: TrajRecord,
  turnNo: number,
  spans: TrajectorySpan[]
): void {
  if (
    (record.kind === "assistant" || record.kind === "tool" || record.kind === "approval") &&
    record.startMs !== undefined
  ) {
    spans.push({
      key: "span-" + record.key,
      recordKey: record.key,
      kind: record.kind,
      label: record.title,
      startMs: record.startMs,
      endMs: record.endMs,
      failed:
        record.status === "error" ||
        record.status === "failed" ||
        record.status === "rejected",
      turnNo,
    });
  }
  for (const child of record.children || []) {
    if (child.kind !== "subtool") {
      collectSpans(child, turnNo, spans);
    }
  }
}

function collectRecords(state: BuildState): TrajRecord[] {
  const records: TrajRecord[] = [];
  for (const record of state.topRecords) {
    records.push(record);
    for (const child of record.children || []) {
      records.push(child);
    }
  }
  return records;
}

export function buildTrajectory(
  events: SessionEvent[] | null | undefined,
  childEvents: Record<string, SessionEvent[]> | null | undefined = {},
  agentLabel: AgentLabel = (name) => name
): Trajectory {
  const state = walk(Array.isArray(events) ? events : [], agentLabel);
  attachChildren(state, childEvents || {}, agentLabel);
  finalizeToolCounts(state);
  const records = collectRecords(state);
  const usage = emptyUsage();
  for (const turn of state.turns) {
    addUsage(usage, turn.usage);
  }
  return {
    sections: state.sections,
    totals: {
      turns: state.turns.length,
      requests: records.filter((record) => record.kind === "assistant").length,
      toolCalls: records.filter((record) => record.kind === "tool").length,
      usage,
    },
    spans: buildSpans(state),
  };
}
