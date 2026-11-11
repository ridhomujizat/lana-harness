/* eslint-disable react-hooks/set-state-in-effect -- the panel follows the live turn, as in team.js */
import { useEffect, useMemo, useRef, useState } from "react";
import type { SessionEvent } from "../api/sessions";
import { Markdown } from "../lib/markdown";
import { contentText, cut, primaryArg } from "../lib/format";
import { AgentAvatar } from "./agent-common";

/* Team message block and Team work panel (REQ-TEAM-019 to REQ-TEAM-021).
   The pure helpers stay free of React and the DOM so they can run in node. */

const LEAD = "lead";
const ARG_LIMIT = 60;
const RETURN_LIMIT = 400;

export interface TeamView {
  name?: string;
  lead: string;
  members: string[];
}

export interface LiveToolEntry {
  name?: string;
  input?: unknown;
  status?: string;
  output?: unknown;
  at?: number;
}

export interface TurnView {
  running?: boolean;
  [key: string]: unknown;
}

export interface TeamChildRequest {
  id: string;
  agent: string;
}

export interface SharedTeamProps {
  team: TeamView;
  turn: TurnView;
  rootEvents?: SessionEvent[];
  childEvents?: Record<string, SessionEvent[]>;
  childLive?: Record<string, TurnView>;
  onLoadChildren?: ((requests: TeamChildRequest[]) => void) | null;
  /* labels maps an agent ID to its display name (falls back to the ID). */
  labels?: Record<string, string>;
}

/* The name shown to people: the display label when known, else the ID. */
function labelOf(labels: Record<string, string> | undefined, name: string): string {
  const label = labels ? labels[name] : "";
  return label || name;
}

/* Child logs carry the agent tag the session view adds (session.js) and the
   live rows carry a string seq, so the local event shape is wider than the
   API's SessionEvent. The group room rebuilds these frames from a turn's
   steps, so the shape is exported (REQ-ROOM-008). */
export interface TeamEvent {
  kind: string;
  created_at?: number;
  seq?: number | string;
  payload?: unknown;
  agent?: string;
}

function asTeamEvents(events: SessionEvent[] | undefined): TeamEvent[] {
  return Array.isArray(events) ? (events as TeamEvent[]) : [];
}

function payloadOf(event: TeamEvent | null | undefined): Record<string, unknown> {
  const payload: unknown = event && event.payload;
  if (typeof payload === "string") {
    return parseJSON(payload) || {};
  }
  return payload && typeof payload === "object"
    ? (payload as Record<string, unknown>)
    : {};
}

function parseJSON(value: unknown): Record<string, unknown> | null {
  if (!value) {
    return null;
  }
  if (typeof value === "object") {
    return value as Record<string, unknown>;
  }
  if (typeof value !== "string") {
    return null;
  }
  try {
    const parsed: unknown = JSON.parse(value);
    return parsed && typeof parsed === "object"
      ? (parsed as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

function asObject(value: unknown): Record<string, unknown> {
  if (!value) {
    return {};
  }
  if (typeof value === "object") {
    return value as Record<string, unknown>;
  }
  return parseJSON(value) || {};
}

/* Same primary argument rules as format.js, kept local for the node import. */
export { cut };

function contentTextOf(event: TeamEvent): string {
  return contentText(payloadOf(event).content);
}

export { primaryArg };

function toolRowStatus(
  status: unknown
): "done" | "error" | "cancelled" | "running" {
  switch (String(status || "").toLowerCase()) {
    case "success":
    case "ok":
      return "done";
    case "error":
      return "error";
    case "canceled":
    case "cancelled":
    case "rejected":
      return "cancelled";
    default:
      return "running";
  }
}

interface TeamCall {
  callId: string;
  childId: string;
  agent: string;
  result: Record<string, unknown> | null;
  context: string[];
  prompt: string;
  startedAt: number;
  endedAt: number;
}

interface TeamDelegationStep {
  kind: "delegation";
  key: string;
  callId: string;
  childId: string;
  agent: string;
  from: string;
  to: string;
  text: string;
  status: string;
  startedAt: number;
  endedAt: number;
  /* Spec 47 team work: earlier child sessions this task builds on. */
  context: string[];
}

interface TeamReturnStep {
  kind: "return";
  key: string;
  callId: string;
  childId: string;
  agent: string;
  from: string;
  to: string;
  text: string;
  answer: string;
  status: string;
  startedAt: number;
  endedAt: number;
  /* Spec 47 team work: tool calls by name, notes used, files changed. */
  tools: Record<string, number>;
  notes: string[];
  files: string[];
}

export type TeamStep = TeamDelegationStep | TeamReturnStep;

/* Builds the delegation steps of one root turn. Each task call gives an
   outgoing step; each result adds the return step with the child's answer.
   task_continue has no agent input, so its agent comes from the result or
   from the child mapping the caller already has. */
export function teamSteps(
  rootEvents: TeamEvent[] | null | undefined,
  childAgents: Map<string, string> | Record<string, string> | null = null
): { steps: TeamStep[]; count: number; completed: number } {
  const events = Array.isArray(rootEvents) ? rootEvents : [];
  const results = new Map<
    string,
    { payload: Record<string, unknown>; createdAt: number }
  >();
  const sessionAgents = new Map<string, string>();

  for (const event of events) {
    if (!event || event.kind !== "tool/result") {
      continue;
    }
    const payload = payloadOf(event);
    if (!payload.call_id) {
      continue;
    }
    results.set(String(payload.call_id), {
      payload,
      createdAt: Number(event.created_at) || 0,
    });
    const parsed = parseJSON(payload.output);
    if (parsed && parsed.session_id && parsed.agent) {
      sessionAgents.set(String(parsed.session_id), String(parsed.agent));
    }
  }

  const calls: TeamCall[] = [];
  for (const event of events) {
    if (!event || event.kind !== "tool/call") {
      continue;
    }
    const payload = payloadOf(event);
    const name = String(payload.name || "");
    if (name !== "task" && name !== "task_continue") {
      continue;
    }
    const input = asObject(payload.input);
    const callId = String(payload.call_id || "");
    const found = callId ? results.get(callId) : null;
    const result = found ? found.payload : null;
    const parsed = result ? parseJSON(result.output) : null;
    const childId = String(
      (parsed && parsed.session_id) || input.session_id || ""
    );
    const mapped =
      childAgents && childId
        ? childAgents instanceof Map
          ? childAgents.get(childId) || ""
          : childAgents[childId] || ""
        : "";
    const agent = String(
      input.agent ||
        (parsed && parsed.agent) ||
        sessionAgents.get(childId) ||
        mapped ||
        "agent"
    );
    calls.push({
      callId,
      childId,
      agent,
      result,
      context: Array.isArray(input.context) ? input.context.map(String) : [],
      prompt: String(input.prompt || ""),
      startedAt: Number(event.created_at) || 0,
      endedAt: found ? found.createdAt : 0,
    });
  }

  const steps: TeamStep[] = [];
  let completed = 0;
  for (const call of calls) {
    steps.push({
      kind: "delegation",
      key: "out-" + (call.callId || String(steps.length)),
      callId: call.callId,
      childId: call.childId,
      agent: call.agent,
      from: LEAD,
      to: call.agent,
      text: call.prompt,
      status: call.result ? "done" : "running",
      startedAt: call.startedAt,
      endedAt: call.endedAt,
      context: call.context,
    });
    if (!call.result) {
      continue;
    }
    completed += 1;
    const parsed = parseJSON(call.result.output) || {};
    const answer =
      typeof parsed.answer === "string"
        ? parsed.answer
        : String(call.result.output || "");
    steps.push({
      kind: "return",
      key: "ret-" + (call.callId || String(steps.length)),
      callId: call.callId,
      childId: call.childId,
      agent: call.agent,
      from: call.agent,
      to: LEAD,
      text: cut(answer, RETURN_LIMIT),
      answer,
      status: String(parsed.status || call.result.status || ""),
      startedAt: call.startedAt,
      endedAt: call.endedAt,
      tools: toolCounts(parsed.tools),
      notes: stringList(parsed.notes),
      files: stringList(parsed.files),
    });
  }

  return { steps, count: steps.length, completed };
}

export interface PanelStatus {
  tone: string;
  text: string;
}

/* Badge and panel state for the work panel. Live counts every step so far;
   an outgoing step without its return already counts as one. */
export function panelStatus({
  live = false,
  steps = [],
  startedAt = 0,
  endedAt = 0,
  reason = "",
}: {
  live?: boolean;
  steps?: TeamStep[] | { steps?: TeamStep[] };
  startedAt?: number;
  endedAt?: number;
  reason?: unknown;
  error?: unknown;
} = {}): PanelStatus {
  const list: TeamStep[] = Array.isArray(steps)
    ? steps
    : steps && Array.isArray(steps.steps)
      ? steps.steps
      : [];
  if (live) {
    return { tone: "warning", text: "Working · step " + list.length };
  }
  const total = list.length;
  const elapsed = Math.max(0, Number(endedAt) - Number(startedAt));
  const seconds = Number.isFinite(elapsed) ? Math.round(elapsed / 1000) : 0;
  switch (String(reason || "")) {
    case "cancelled":
      return { tone: "gray", text: "Cancelled · " + total + " steps" };
    case "error":
      return { tone: "error", text: "Failed · " + total + " steps" };
    case "max_steps":
      return { tone: "gray", text: "Stopped · " + total + " steps" };
    case "loop":
      return { tone: "gray", text: "Stopped · " + total + " steps" };
    default:
      return {
        tone: "success",
        text: "Done · " + total + " steps · " + seconds + " s",
      };
  }
}

export function badgeText(status: unknown): string {
  if (typeof status === "string") {
    return status;
  }
  return status && typeof status === "object" && "text" in status
    ? String((status as { text?: unknown }).text || "")
    : "";
}

export interface TeamToolRow {
  key: string;
  callId: string;
  name: string;
  argument: string;
  status: string;
  output: string;
}

/* Tool rows of one child log, paired by call_id and kept in call order. */
export function childToolRows(
  childEvents: TeamEvent[] | null | undefined
): TeamToolRow[] {
  const events = Array.isArray(childEvents) ? childEvents : [];
  const results = new Map<string, Record<string, unknown>>();
  for (const event of events) {
    if (!event || event.kind !== "tool/result") {
      continue;
    }
    const payload = payloadOf(event);
    if (payload.call_id) {
      results.set(String(payload.call_id), payload);
    }
  }

  const rows: TeamToolRow[] = [];
  const index = new Map<string, number>();
  for (const event of events) {
    if (!event || event.kind !== "tool/call") {
      continue;
    }
    const payload = payloadOf(event);
    const callId = String(payload.call_id || "");
    const result = callId ? results.get(callId) : null;
    const name = String(payload.name || "tool");
    const row: TeamToolRow = {
      key: callId || "call-" + String(event.seq || rows.length),
      callId,
      name,
      argument: cut(primaryArg(name, payload.input), ARG_LIMIT),
      status: toolRowStatus(result ? result.status : "running"),
      output: result ? String(result.output || "") : "",
    };
    if (callId && index.has(callId)) {
      const existing = rows[index.get(callId) ?? -1];
      if (!existing) {
        continue;
      }
      existing.argument = row.argument || existing.argument;
      existing.status = row.status;
      existing.output = row.output;
      continue;
    }
    if (callId) {
      index.set(callId, rows.length);
    }
    rows.push(row);
  }
  return rows;
}

/* The lead's last assistant message of the turn. */
export function finalAnswer(
  rootEvents: TeamEvent[] | null | undefined
): string {
  const events = Array.isArray(rootEvents) ? rootEvents : [];
  let answer = "";
  for (const event of events) {
    if (!event || event.kind !== "assistant/message") {
      continue;
    }
    const text = contentTextOf(event);
    if (text) {
      answer = text;
    }
  }
  return answer;
}

function turnTimes(events: TeamEvent[] | null | undefined): {
  startedAt: number;
  endedAt: number;
} {
  let startedAt = 0;
  let endedAt = 0;
  for (const event of Array.isArray(events) ? events : []) {
    if (!event) {
      continue;
    }
    if (event.kind === "turn/start" && !startedAt) {
      startedAt = Number(event.created_at) || 0;
    }
    if (event.kind === "turn/end") {
      endedAt = Number(event.created_at) || 0;
    }
  }
  return { startedAt, endedAt };
}

function formatClock(ms: unknown): string {
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

function badgeTone(tone: string): "success" | "error" | "warning" | "neutral" {
  if (tone === "success" || tone === "error" || tone === "warning") {
    return tone;
  }
  return "neutral";
}

function toolTone(status: string): "success" | "error" | "neutral" | "warning" {
  if (status === "done") {
    return "success";
  }
  if (status === "error") {
    return "error";
  }
  if (status === "cancelled") {
    return "neutral";
  }
  return "warning";
}

function toolWord(status: string): string {
  switch (status) {
    case "done":
      return "Done";
    case "error":
      return "Error";
    case "cancelled":
      return "Cancelled";
    default:
      return "Running";
  }
}

/* A child session can serve more than one delegation (task_continue), so
   keep only the events of the child turn that belongs to this call. */
function inCallWindow(
  event: TeamEvent,
  step: TeamDelegationStep | TeamReturnStep
): boolean {
  const at = Number(event && event.created_at) || 0;
  if (!at) {
    return true;
  }
  if (step.startedAt && at < step.startedAt) {
    return false;
  }
  if (step.endedAt && at > step.endedAt) {
    return false;
  }
  return true;
}

function childEventsFor(
  step: TeamDelegationStep | TeamReturnStep,
  childEvents: Record<string, SessionEvent[]>
): TeamEvent[] {
  if (!step || !childEvents) {
    return [];
  }
  if (step.childId && Array.isArray(childEvents[step.childId])) {
    return asTeamEvents(childEvents[step.childId]).filter((event) =>
      inCallWindow(event, step)
    );
  }
  const merged: TeamEvent[] = [];
  for (const key of Object.keys(childEvents)) {
    const list = childEvents[key];
    if (!Array.isArray(list)) {
      continue;
    }
    const tagged = asTeamEvents(list);
    if (tagged.some((event) => event && event.agent === step.agent)) {
      merged.push(...tagged.filter((event) => inCallWindow(event, step)));
    }
  }
  return merged;
}

function liveToolEvents(
  step: TeamDelegationStep | TeamReturnStep,
  childLive: Record<string, TurnView>
): TeamEvent[] {
  const events: TeamEvent[] = [];
  if (!step || !childLive) {
    return events;
  }
  for (const id of Object.keys(childLive)) {
    const entry = childLive[id];
    if (!entry || !entry.tools || typeof entry.tools !== "object") {
      continue;
    }
    if (step.childId && id !== step.childId) {
      continue;
    }
    if (!step.childId && entry.agent !== step.agent) {
      continue;
    }
    const tools = entry.tools as Record<string, unknown>;
    for (const callId of Object.keys(tools)) {
      const tool = asObject(tools[callId]);
      const toolAt = Number(tool.at) || 0;
      if (step.startedAt && toolAt && toolAt < step.startedAt) {
        continue;
      }
      events.push({
        kind: "tool/call",
        seq: "live-call-" + callId,
        payload: { call_id: callId, name: tool.name, input: tool.input },
      });
      if (tool.status && tool.status !== "running") {
        events.push({
          kind: "tool/result",
          seq: "live-result-" + callId,
          payload: {
            call_id: callId,
            status: tool.status,
            output: tool.output,
          },
        });
      }
    }
  }
  return events;
}

function liveTextFor(
  step: TeamDelegationStep | TeamReturnStep,
  childLive: Record<string, TurnView>
): string {
  if (!step || !childLive) {
    return "";
  }
  let text = "";
  for (const id of Object.keys(childLive)) {
    const entry = childLive[id];
    if (!entry) {
      continue;
    }
    if (step.childId && id !== step.childId) {
      continue;
    }
    if (!step.childId && entry.agent !== step.agent) {
      continue;
    }
    const textAt = Number(entry.textAt) || 0;
    if (step.startedAt && textAt && textAt < step.startedAt) {
      continue;
    }
    if (entry.text) {
      text = String(entry.text);
    }
  }
  return text;
}

export interface DelegationStepProps {
  from?: string;
  to?: string;
  /* The human names; from/to stay the IDs for the avatar color. */
  fromLabel?: string;
  toLabel?: string;
  text?: string;
  status?: string;
  avatar?: string;
}

export function DelegationStep({
  from = "",
  to = "",
  fromLabel = "",
  toLabel = "",
  text = "",
  status = "",
  avatar = "",
}: DelegationStepProps) {
  const tone = status === "running" ? "warning" : "";
  return (
    <div className="team-step">
      <span className="team-step-rail">
        <AgentAvatar name={avatar || from || "agent"} size="24" />
      </span>
      <div className="team-step-body">
        <div className="team-step-head">
          <span className="team-step-from">{fromLabel || from || "agent"}</span>
          <span className="team-step-to">→ {toLabel || to || LEAD}</span>
          {status === "running" ? (
            <span className={"badge-ui team-badge-" + tone}>
              <span className="badge-ui-dot" aria-hidden="true"></span>Running
            </span>
          ) : null}
        </div>
        {text ? (
          <div className="team-step-text">
            <Markdown text={text} />
          </div>
        ) : null}
      </div>
    </div>
  );
}

export interface ToolRowProps {
  row: TeamToolRow | null;
}

export function ToolRow({ row }: ToolRowProps) {
  if (!row) {
    return null;
  }
  const tone = toolTone(row.status);
  return (
    <div className="team-tool">
      <span className="team-tool-name">{row.name || "tool"}</span>
      {row.argument ? (
        <span className="team-tool-arg" title={row.argument}>
          {row.argument}
        </span>
      ) : null}
      <span className="team-tool-spacer"></span>
      <span className={"badge-ui team-badge-" + tone}>
        <span className="badge-ui-dot" aria-hidden="true"></span>
        {toolWord(row.status)}
      </span>
    </div>
  );
}

interface TeamGroup {
  key: string;
  out: TeamDelegationStep | null;
  ret: TeamReturnStep | null;
}

export function TeamWorkPanel({
  team,
  turn,
  rootEvents = [],
  childEvents = {},
  childLive = {},
  onLoadChildren = null,
  labels = {},
}: SharedTeamProps) {
  const live = Boolean(turn && turn.running);
  const [open, setOpen] = useState(live);
  const requestedRef = useRef(new Set<string>());
  const childAgents = useMemo(() => {
    const map: Record<string, string> = {};
    for (const id of Object.keys(childEvents || {})) {
      const list = asTeamEvents(childEvents[id]);
      const tagged = list.find((event) => event && event.agent);
      if (tagged && tagged.agent) {
        map[id] = tagged.agent;
      }
    }
    for (const id of Object.keys(childLive || {})) {
      const entry = childLive[id];
      if (entry && typeof entry.agent === "string" && entry.agent) {
        map[id] = entry.agent;
      }
    }
    return map;
  }, [childEvents, childLive]);
  const work = useMemo(
    () => teamSteps(asTeamEvents(rootEvents), childAgents),
    [rootEvents, childAgents]
  );
  const steps = work.steps;
  const times = turnTimes(asTeamEvents(rootEvents));
  const startedAt = times.startedAt || Number(turn && turn.startedAt) || 0;
  const endedAt = times.endedAt || Number(turn && turn.endedAt) || 0;
  const status = panelStatus({
    live,
    steps,
    startedAt,
    endedAt,
    reason: turn && turn.stopReason,
    error: turn && turn.error,
  });
  const answer = useMemo(
    () => finalAnswer(asTeamEvents(rootEvents)),
    [rootEvents]
  );
  const lead = team && team.lead ? String(team.lead) : LEAD;
  const leadLabel = labelOf(labels, lead);
  const threadId =
    "team-work-" +
    String((turn && turn.id) || "turn").replace(/[^a-zA-Z0-9_-]/g, "-");

  /* Open while the turn runs; collapse when it ends (Q-012). */
  useEffect(() => {
    setOpen(live);
  }, [live]);

  /* Older turns load the child logs only when the owner expands the panel
     (Q-011). Live turns get the same rows from the stream. */
  useEffect(() => {
    if (!open || live || !onLoadChildren) {
      return;
    }
    const requests: TeamChildRequest[] = [];
    const seen = new Set<string>();
    for (const step of steps) {
      if (
        step.kind !== "delegation" ||
        !step.childId ||
        seen.has(step.childId)
      ) {
        continue;
      }
      seen.add(step.childId);
      if (requestedRef.current.has(step.childId)) {
        continue;
      }
      requestedRef.current.add(step.childId);
      requests.push({ id: step.childId, agent: step.agent });
    }
    if (requests.length > 0) {
      onLoadChildren(requests);
    }
  }, [open, live, steps, onLoadChildren]);

  const groups = useMemo(() => {
    const list: TeamGroup[] = [];
    for (const step of steps) {
      if (step.kind === "delegation") {
        list.push({ key: step.key, out: step, ret: null });
        continue;
      }
      if (step.kind !== "return") {
        continue;
      }
      const last = list.length > 0 ? list[list.length - 1] ?? null : null;
      if (last && !last.ret && last.out && last.out.agent === step.agent) {
        last.ret = step;
      } else {
        list.push({ key: step.key, out: null, ret: step });
      }
    }
    return list;
  }, [steps]);

  function rowsFor(group: TeamGroup): TeamToolRow[] {
    const step = group.out || group.ret;
    if (!step) {
      return [];
    }
    const stored = childEventsFor(step, childEvents ?? {});
    const liveEvents = liveToolEvents(step, childLive ?? {});
    if (stored.length === 0 && liveEvents.length === 0) {
      return [];
    }
    return childToolRows(stored.concat(liveEvents));
  }

  return (
    <section className="team-work">
      <div className="team-work-head">
        <span className="team-work-title">Team work</span>
        <span
          className={"badge-ui team-badge-" + badgeTone(status.tone)}
          aria-live="polite"
          aria-atomic="true"
        >
          <span className="badge-ui-dot" aria-hidden="true"></span>
          {badgeText(status)}
        </span>
        <span className="team-work-spacer"></span>
        <button
          type="button"
          className="team-work-toggle"
          aria-expanded={open}
          aria-controls={threadId}
          onClick={() => setOpen((value) => !value)}
        >
          {open ? "Hide work ▴" : "Show work ▾"}
        </button>
      </div>
      <div className="team-work-thread" id={threadId} hidden={!open}>
        {groups.length === 0 ? (
          <p className="team-work-empty">
            {live
              ? "Waiting for the first delegation…"
              : "No delegation steps."}
          </p>
        ) : (
          groups.map((group) => {
            const rows = rowsFor(group);
            const liveText =
              live && !group.ret && group.out
                ? liveTextFor(group.out, childLive ?? {})
                : "";
            return (
              <div className="team-call" key={group.key}>
                {group.out ? (
                  <DelegationStep
                    from={group.out.from}
                    to={group.out.to}
                    fromLabel={leadLabel}
                    toLabel={labelOf(labels, group.out.agent)}
                    text={group.out.text}
                    avatar={lead}
                  />
                ) : null}
                {rows.length > 0 ? (
                  <div className="team-tools">
                    {rows.map((row) => (
                      <ToolRow key={row.key} row={row} />
                    ))}
                  </div>
                ) : null}
                {group.ret ? (
                  <DelegationStep
                    from={group.ret.from}
                    to={group.ret.to}
                    fromLabel={labelOf(labels, group.ret.agent)}
                    toLabel={leadLabel}
                    text={group.ret.text}
                    avatar={group.ret.agent}
                  />
                ) : null}
                {live && !group.ret && group.out ? (
                  <DelegationStep
                    from={group.out.agent}
                    to={LEAD}
                    fromLabel={labelOf(labels, group.out.agent)}
                    toLabel={leadLabel}
                    text={cut(liveText || "Working…", RETURN_LIMIT)}
                    status="running"
                    avatar={group.out.agent}
                  />
                ) : null}
              </div>
            );
          })
        )}
      </div>
      {!live && answer ? (
        <div className="team-answer">
          <div className="team-answer-head">
            <AgentAvatar name={lead} size="20" />
            <span className="team-answer-label">{leadLabel + " · answer"}</span>
          </div>
          <div className="team-answer-text">
            <Markdown text={answer} />
          </div>
        </div>
      ) : null}
    </section>
  );
}

export function TeamBlock({
  team,
  turn,
  rootEvents = [],
  childEvents = {},
  childLive = {},
  onLoadChildren = null,
  labels = {},
}: SharedTeamProps) {
  const members = Array.isArray(team && team.members) ? team.members : [];
  const lead = team && team.lead ? String(team.lead) : LEAD;
  const roster = [lead]
    .concat(members.map((name) => String(name || "")))
    .filter(Boolean);
  const shown = roster.slice(0, 3);
  const teamName = team && team.name ? labelOf(labels, String(team.name)) : "Team";
  const startedAt =
    turnTimes(asTeamEvents(rootEvents)).startedAt ||
    Number(turn && turn.startedAt) ||
    0;
  return (
    <article className="team-turn">
      <div className="team-turn-head">
        <span
          className="agent-avatar-group team-avatar-group"
          aria-hidden="true"
        >
          {shown.map((name) => (
            <AgentAvatar key={name} name={name} size="24" />
          ))}
        </span>
        <span className="team-name">{teamName}</span>
        <span className="badge-ui badge-ui-brand team-badge">
          <span className="badge-ui-dot" aria-hidden="true"></span>Team
        </span>
        <span className="team-agents">{1 + members.length} agents</span>
        <span className="turn-spacer"></span>
        <span className="turn-time">{formatClock(startedAt)}</span>
      </div>
      <TeamWorkPanel
        team={team}
        turn={turn}
        rootEvents={rootEvents}
        childEvents={childEvents}
        childLive={childLive}
        onLoadChildren={onLoadChildren}
        labels={labels}
      />
    </article>
  );
}

function toolCounts(value: unknown): Record<string, number> {
  const out: Record<string, number> = {};
  if (value && typeof value === "object") {
    for (const [name, count] of Object.entries(value as Record<string, unknown>)) {
      const n = Number(count);
      if (name && Number.isFinite(n) && n > 0) {
        out[name] = n;
      }
    }
  }
  return out;
}

function stringList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string" && item !== "") : [];
}
