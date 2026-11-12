import { AgentFiles } from "./message-files";
import { Fragment, useCallback, useMemo, useState } from "react";
import { DiscussionThread, NotesTag, SummaryButton } from "./discussion";
import { displayLabel } from "./agent-label";
import type { Dispatch, KeyboardEvent as ReactKeyboardEvent, SetStateAction } from "react";
import type { RoomRow } from "../api/rooms";
import type { SessionEvent } from "../api/sessions";
import { Markdown } from "../lib/markdown";
import { Button } from "../ui/gr8r";
import { AgentAvatar } from "./agent-avatar";
import { agentFaceColors, gr8rColor, TypingDots } from "./presence";
import type { FaceRef } from "./presence";
import { teamSteps } from "./team";
import type { TeamEvent, TeamStep } from "./team";
import { buildTurns, formatClock, TurnBlock } from "./turn";
import type { ChatTurn, TurnStep } from "./turn";

/* The group room of REQ-ROOM-008 (design rooms-4). A team turn renders as a
   group chat: the lead's handoffs as one message with a chip per member, each
   member's answer as that member's own message, and the lead's final answer.
   The delegation rows come from teamSteps (spec #10), so the room and the
   session view share one parse of the `task` handoffs. */

const TITLE_LIMIT = 48;

/* The name shown to people: the face's display label when known, else the ID. */
function faceLabel(faces: Record<string, FaceRef>, name: string): string {
  const face = faces[name];
  return face && face.label ? face.label : name;
}

export interface HandoffChip {
  key: string;
  agent: string;
  title: string;
  state: "working" | "done" | "failed";
  /* Spec 47 team work: the notes the member used, once it answered. */
  notes: number;
}

export type GroupItem =
  | { kind: "lead"; key: string; text: string; chips: HandoffChip[]; at: number }
  | {
      kind: "member";
      key: string;
      agent: string;
      text: string;
      at: number;
      tools: number;
      /* Spec 47 team work: tool calls by name, notes used, files changed. */
      toolsUsed: Record<string, number>;
      notes: string[];
      files: string[];
      /* Group rooms: said to the room, not an answer to the lead. */
      direct?: boolean;
      childSessionId?: string;
      childTurnId?: string;
    }
  | { kind: "passed"; key: string; from: string[]; to: string }
  | { kind: "pass"; key: string; agent: string }
  | { kind: "working"; key: string; agent: string; title: string }
  | { kind: "discussion"; key: string };

type Delegation = Extract<TeamStep, { kind: "delegation" }>;
type Return = Extract<TeamStep, { kind: "return" }>;

function collapsed(text: unknown): string {
  return String(text || "")
    .replace(/\s+/g, " ")
    .trim();
}

/* The chip title: the task title, or the first words of the prompt. */
function firstWords(text: unknown, limit = TITLE_LIMIT): string {
  const value = collapsed(text);
  if (value.length <= limit) {
    return value;
  }
  const head = value.slice(0, limit);
  const at = head.lastIndexOf(" ");
  return (at > 0 ? head.slice(0, at) : head) + "…";
}

function objectOf(value: unknown): Record<string, unknown> {
  if (value && typeof value === "object") {
    return value as Record<string, unknown>;
  }
  if (typeof value !== "string" || !value) {
    return {};
  }
  try {
    const parsed: unknown = JSON.parse(value);
    return parsed && typeof parsed === "object"
      ? (parsed as Record<string, unknown>)
      : {};
  } catch {
    return {};
  }
}

function stepTitle(step: TurnStep | undefined, prompt: string): string {
  const input = objectOf(step ? step.input : null);
  const title = typeof input.title === "string" ? input.title.trim() : "";
  return title || firstWords(prompt);
}

/* AC-ROOM-012: a chip is working until its result arrives, failed when the
   result says so, and done otherwise. */
function chipState(hasResult: boolean, status: string): HandoffChip["state"] {
  if (!hasResult) {
    return "working";
  }
  const value = status.toLowerCase();
  if (value === "failed" || value === "error" || value === "failure") {
    return "failed";
  }
  return "done";
}

/* The taskResult carries the tool_calls count and fallback steps (spec #10). */
function toolCount(step: TurnStep | undefined): number {
  const parsed = objectOf(step ? step.output : null);
  const calls = Number(parsed.tool_calls);
  if (Number.isFinite(calls) && calls > 0) {
    return calls;
  }
  return Array.isArray(parsed.steps) ? parsed.steps.length : 0;
}

function isDelegation(name: unknown): boolean {
  return name === "task" || name === "task_continue";
}

/* Rebuilds the root tool frames of a turn from its steps so teamSteps can pair
   each handoff with its result. */
function rootFrames(turn: ChatTurn): TeamEvent[] {
  const events: TeamEvent[] = [];
  for (const step of turn.steps) {
    if (!step.callId || !isDelegation(step.name)) {
      continue;
    }
    events.push({
      kind: "tool/call",
      payload: { call_id: step.callId, name: step.name, input: step.input },
    });
    if (
      step.status &&
      step.status !== "running" &&
      step.output !== "" &&
      step.output != null
    ) {
      events.push({
        kind: "tool/result",
        payload: { call_id: step.callId, status: step.status, output: step.output },
      });
    }
  }
  return events;
}

/* The ordered group-chat items of one team turn (AC-ROOM-012). The owner
   messages render above the turn, as in the session view. */
export function groupItems(turn: ChatTurn): GroupItem[] {
  const delegations = new Map<string, Delegation>();
  const returns = new Map<string, Return>();
  const agentByChild = new Map<string, string>();
  for (const step of teamSteps(rootFrames(turn)).steps) {
    if (!step.callId) {
      continue;
    }
    if (step.kind === "delegation") {
      delegations.set(step.callId, step);
    } else if (step.kind === "return") {
      returns.set(step.callId, step);
      if (step.childId) {
        agentByChild.set(step.childId, step.agent);
      }
    }
  }

  const stepByCall = new Map<string, TurnStep>();
  for (const step of turn.steps) {
    if (step.callId) {
      stepByCall.set(step.callId, step);
    }
  }

  const items: GroupItem[] = [];
  let counter = 0;
  let lead: Extract<GroupItem, { kind: "lead" }> | null = null;

  for (const group of turn.groups) {
    if (group.kind === "text" && group.source === "agent" && group.agentMeta) {
      /* Group rooms: each agent's reply is that agent's own message. */
      const agent = group.sourceName || "agent";
      if (group.agentMeta.passed) {
        items.push({ kind: "pass", key: "pass-" + group.key, agent });
      } else {
        items.push({
          kind: "member",
          key: "member-" + group.key,
          agent,
          text: group.text || "",
          at: turn.lastAt || turn.startedAt,
          tools: Object.values(group.agentMeta.tools).reduce((a, b) => a + b, 0),
          toolsUsed: group.agentMeta.tools,
          notes: group.agentMeta.notes,
          files: group.agentMeta.files,
          direct: true,
          childSessionId: group.agentMeta.childSessionId,
          childTurnId: group.agentMeta.childTurnId,
        });
      }
      lead = null;
      continue;
    }
    if (group.kind === "text") {
      if (!group.text) {
        continue;
      }
      /* Text after a discussion is the lead's answer: it carries the turn's
         end time, not its start. */
      const afterDiscussion = items.some((item) => item.kind === "discussion");
      lead = {
        kind: "lead",
        key: "lead-" + turn.key + "-" + counter,
        text: group.text,
        chips: [],
        at: afterDiscussion ? turn.endedAt || turn.lastAt || turn.startedAt : turn.startedAt,
      };
      counter += 1;
      items.push(lead);
      continue;
    }
    if (group.kind !== "activity" || !group.steps) {
      continue;
    }
    for (const step of group.steps) {
      /* Spec 47: the discussion renders as one block where the lead opened
         it; later lead text follows it. */
      if (turn.discussion && String(step.name || "").startsWith("discuss_")) {
        if (!items.some((item) => item.kind === "discussion")) {
          items.push({ kind: "discussion", key: "discussion-" + turn.key });
          lead = null;
        }
        continue;
      }
      if (!step.callId || !isDelegation(step.name)) {
        continue;
      }
      const out = delegations.get(step.callId);
      if (!out) {
        continue;
      }
      const ret = returns.get(step.callId);
      const title = stepTitle(stepByCall.get(step.callId), out.text);
      const state = chipState(Boolean(ret), ret ? ret.status : "");
      if (!lead) {
        lead = {
          kind: "lead",
          key: "lead-" + turn.key + "-" + counter,
          text: "",
          chips: [],
          at: out.startedAt,
        };
        counter += 1;
        items.push(lead);
      }
      lead.chips.push({ key: "chip-" + out.key, agent: out.agent, title, state, notes: ret ? ret.notes.length : 0 });
      if (!lead.at) {
        lead.at = out.startedAt;
      }
      /* Spec 47: a task that builds on earlier answers says whose. */
      if (out.context.length > 0) {
        const from = out.context
          .map((id) => agentByChild.get(id) || "")
          .filter((name, index, list) => name && list.indexOf(name) === index);
        if (from.length > 0) {
          items.push({ kind: "passed", key: "passed-" + out.key, from, to: out.agent });
        }
      }
      if (ret) {
        items.push({
          kind: "member",
          key: "member-" + out.key,
          agent: out.agent,
          text: ret.answer,
          at: ret.endedAt || turn.lastAt,
          tools: toolCount(stepByCall.get(step.callId)),
          toolsUsed: ret.tools,
          notes: ret.notes,
          files: ret.files,
        });
      } else if (turn.running) {
        items.push({ kind: "working", key: "working-" + out.key, agent: out.agent, title });
      }
    }
  }

  return items;
}

export interface RoomGroupProps {
  turn: ChatTurn;
  room: RoomRow;
  faces: Record<string, FaceRef>;
  /* REQ-AGF-005: the session of the turn, for the URLs of sent files. */
  sessionId?: string;
  /* Spec 47: "Answer directly instead" and "View room memory". */
  onDirect?: () => void;
  onMemory?: () => void;
  /* Spec 47 team work: what each agent can do ("web", "files"…). */
  can?: Record<string, string[]>;
  /* Group rooms: the events of each agent's own session, so a reply shows
     its steps like a chat, and the session of the agent working now. */
  childEvents?: Record<string, SessionEvent[]>;
  liveChild?: string;
}

function HandoffChipView({
  chip,
  faces,
  can,
}: {
  chip: HandoffChip;
  faces: Record<string, FaceRef>;
  can?: string[];
}) {
  const face = faces[chip.agent];
  const colors = agentFaceColors(face ? face.color : undefined);
  const label = faceLabel(faces, chip.agent);
  const text = label + " · " + chip.title + " · " + chip.state;
  return (
    <span className={"handoff-chip handoff-" + chip.state} aria-label={text}>
      <AgentAvatar
        name={chip.agent}
        color={gr8rColor(face ? face.color : undefined)}
        size="sm"
      />
      <span className="handoff-name" style={{ color: colors.fg }}>
        {label}
      </span>
      <span className="handoff-text">{chip.title}</span>
      {(can || []).slice(0, 3).map((word) => (
        <span key={word} className="handoff-tool">
          {word}
        </span>
      ))}
      {chip.notes > 0 ? <span className="handoff-notes">{"▤ " + chip.notes + (chip.notes === 1 ? " note" : " notes")}</span> : null}
      <span className="handoff-state">{chip.state}</span>
    </span>
  );
}

function LeadMessage({
  item,
  lead,
  faces,
  can,
}: {
  item: Extract<GroupItem, { kind: "lead" }>;
  lead: string;
  faces: Record<string, FaceRef>;
  can?: Record<string, string[]>;
}) {
  const face = faces[lead];
  const color = agentFaceColors(face ? face.color : undefined).fg;
  const leadLabel = faceLabel(faces, lead);
  return (
    <div className="group-msg group-lead-msg">
      <AgentAvatar
        name={lead}
        color={gr8rColor(face ? face.color : undefined)}
        size="md"
      />
      <div className="group-msg-main">
        <div className="group-msg-head">
          <span className="group-msg-name" style={{ color }}>
            {leadLabel}
          </span>
          <span className="group-lead-pill">lead</span>
          <span className="group-msg-time">{formatClock(item.at)}</span>
        </div>
        {item.text ? (
          <div className="turn-text group-msg-text">
            <Markdown text={item.text} />
          </div>
        ) : null}
        {item.chips.length > 0 ? (
          <ol className="group-plan">
            {item.chips.map((chip, index) => (
              <li key={chip.key} className="group-plan-step">
                {item.chips.length > 1 ? <span className="group-plan-n">{index + 1}</span> : null}
                <HandoffChipView chip={chip} faces={faces} can={can ? can[chip.agent] : undefined} />
              </li>
            ))}
          </ol>
        ) : null}
      </div>
    </div>
  );
}

function MemberMessage({
  item,
  lead,
  face,
}: {
  item: Extract<GroupItem, { kind: "member" }>;
  lead: string;
  face: FaceRef | undefined;
}) {
  const color = agentFaceColors(face ? face.color : undefined).fg;
  const memberLabel = face && face.label ? face.label : item.agent;
  const named = Object.entries(item.toolsUsed || {})
    .filter(([name]) => !name.startsWith("knowledge_"))
    .sort((a, b) => b[1] - a[1]);
  const tools = named.length > 0 ? "" : item.tools === 1 ? "1 tool" : item.tools > 0 ? item.tools + " tools" : "";
  const meta = [item.direct ? "" : "replying to " + lead, formatClock(item.at), tools]
    .filter(Boolean)
    .join(" · ");
  return (
    <div className="group-msg group-member-msg">
      <AgentAvatar
        name={item.agent}
        color={gr8rColor(face ? face.color : undefined)}
        size="md"
      />
      <div className="group-msg-main">
        <div className="group-msg-head">
          <span className="group-msg-name" style={{ color }}>
            {memberLabel}
          </span>
          <span className="group-msg-meta">{meta}</span>
          {named.slice(0, 2).map(([name, count]) => (
            <span key={name} className="disc-chip">
              {name + (count > 1 ? " ×" + count : "")}
            </span>
          ))}
          <NotesTag notes={item.notes || []} />
        </div>
        <div className="turn-text group-msg-text">
          <Markdown text={item.text} />
        </div>
        {(item.files || []).length > 0 ? (
          <div className="group-files-changed">
            {(item.files || []).slice(0, 6).map((file) => (
              <span key={file} className="disc-chip">
                {file.split("/").pop()}
              </span>
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function WorkingRow({
  item,
  face,
}: {
  item: Extract<GroupItem, { kind: "working" }>;
  face: FaceRef | undefined;
}) {
  const memberLabel = face && face.label ? face.label : item.agent;
  return (
    <div className="group-msg group-member-msg group-working" role="status">
      <AgentAvatar
        name={item.agent}
        color={gr8rColor(face ? face.color : undefined)}
        presence
        size="md"
        mood="thinking"
      />
      <div className="group-msg-main">
        <span className="group-working-text">
          <TypingDots />
          <span>{memberLabel + " is working on " + item.title + "…"}</span>
        </span>
      </div>
    </div>
  );
}

export function RoomGroup({ turn, room, faces, sessionId = "", onDirect, onMemory, can, childEvents, liveChild }: RoomGroupProps) {
  const lead = room.lead || "lead";
  const items = useMemo(() => {
    const list = groupItems(turn);
    /* A discussion whose steps are not in the turn yet (live) still shows. */
    if (turn.discussion && !list.some((item) => item.kind === "discussion")) {
      list.unshift({ kind: "discussion", key: "discussion-" + turn.key });
    }
    return list;
  }, [turn]);
  if (items.length === 0) {
    return null;
  }
  const discussion = turn.discussion;
  /* REQ-RDS-052: the Summary button sits under the lead's last message after
     the discussion, or under the discussion when the lead wrote nothing. */
  const discussionIndex = items.findIndex((item) => item.kind === "discussion");
  let summaryAfter = -1;
  if (discussion && discussion.ended) {
    summaryAfter = discussionIndex;
    items.forEach((item, index) => {
      if (index > discussionIndex && item.kind === "lead") {
        summaryAfter = index;
      }
    });
  }
  const summary =
    discussion && discussion.ended ? (
      <div className="disc-after" key="disc-summary">
        <SummaryButton log={discussion} faces={faces} room={displayLabel(room)} onMemory={onMemory} />
      </div>
    ) : null;
  return (
    <div className="room-group">
      {items.map((item, index) => {
        const tail = index === summaryAfter ? summary : null;
        if (item.kind === "discussion" && discussion) {
          return (
            <Fragment key={item.key}>
              <DiscussionThread log={discussion} faces={faces} running={turn.running} onDirect={onDirect} />
              {tail}
            </Fragment>
          );
        }
        if (item.kind === "discussion") {
          return null;
        }
        if (item.kind === "lead") {
          return (
            <Fragment key={item.key}>
              <LeadMessage item={item} lead={lead} faces={faces} can={can} />
              {tail}
            </Fragment>
          );
        }
        if (item.kind === "member" && item.direct && item.childSessionId && childEvents && childEvents[item.childSessionId]) {
          const childTurn = childTurnOf(childEvents[item.childSessionId] || [], item.childTurnId || "");
          if (childTurn) {
            const face = faces[item.agent];
            return (
              <div key={item.key} className="group-turn">
                <TurnBlock
                  turn={childTurn}
                  agent={item.agent}
                  agentLabel={faceLabel(faces, item.agent)}
                  agentColor={face ? face.color : undefined}
                  sessionId={item.childSessionId}
                />
              </div>
            );
          }
        }
        if (item.kind === "pass") {
          return (
            <div key={item.key} className="disc-pass">
              <span>{faceLabel(faces, item.agent) + " passed"}</span>
            </div>
          );
        }
        if (item.kind === "passed") {
          return (
            <div key={item.key} className="disc-pass group-passed">
              <span>
                {"↳ " + faceLabel(faces, lead) + " passed " + item.from.map((name) => faceLabel(faces, name) + "'s").join(" and ") + " findings to " + faceLabel(faces, item.to)}
              </span>
            </div>
          );
        }
        if (item.kind === "working") {
          return <WorkingRow key={item.key} item={item} face={faces[item.agent]} />;
        }
        return (
          <MemberMessage
            key={item.key}
            item={item}
            lead={faceLabel(faces, lead)}
            face={faces[item.agent]}
          />
        );
      })}
      {turn.running && liveChild && childEvents && childEvents[liveChild] ? (
        <LiveChildTurn events={childEvents[liveChild] || []} faces={faces} childId={liveChild} />
      ) : null}
      {sessionId && turn.sentFiles.length > 0 ? (
        <div className="group-files">
          <AgentFiles sessionId={sessionId} files={turn.sentFiles} sender={faceLabel(faces, lead)} />
        </div>
      ) : null}
    </div>
  );
}

/* REQ-ROOM-008: the composer offers one mention chip per agent. The chips and
   the "@" listbox live here; the room composer owns the textarea. */
export interface MentionChipsProps {
  names: string[];
  faces: Record<string, FaceRef>;
  onPick: (name: string) => void;
}

export function MentionChips({ names, faces, onPick }: MentionChipsProps) {
  return (
    <div className="room-mentions">
      {names.map((name) => (
        <Button
          key={name}
          size="sm"
          variant="ghost"
          className="room-mention-chip"
          style={{ color: agentFaceColors(faces[name] ? faces[name].color : undefined).fg }}
          onClick={() => onPick(name)}
        >
          {"@" + name}
        </Button>
      ))}
    </div>
  );
}

export interface MentionListProps {
  names: string[];
  active: number;
  onPick: (name: string) => void;
}

export function MentionList({ names, active, onPick }: MentionListProps) {
  return (
    <div className="room-mention-list" role="listbox" aria-label="Mention an agent">
      {names.map((name, index) => (
        <button
          key={name}
          type="button"
          role="option"
          aria-selected={index === active}
          tabIndex={-1}
          className={"room-mention-option" + (index === active ? " is-active" : "")}
          onMouseDown={(event) => {
            event.preventDefault();
            onPick(name);
          }}
        >
          {"@" + name}
        </button>
      ))}
    </div>
  );
}

export interface Mentions {
  open: boolean;
  matches: string[];
  index: number;
  insert: (name: string) => void;
  reset: () => void;
  onKeyDown: (event: ReactKeyboardEvent<HTMLTextAreaElement>) => boolean;
}

/* REQ-ROOM-008: typing "@" at the start of the draft opens the member listbox
   and the arrow keys, Enter and Escape pick or close it. A pick replaces an
   existing leading mention. */
export function useMentions(
  draft: string,
  setDraft: Dispatch<SetStateAction<string>>,
  names: string[]
): Mentions {
  const [index, setIndex] = useState(0);
  const [dismissed, setDismissed] = useState(false);
  const match = /^@(\S*)$/.exec(draft);
  const prefix = match ? match[1] || "" : null;
  const matches = useMemo(
    () =>
      prefix === null
        ? []
        : names.filter((name) =>
            name.toLowerCase().startsWith(prefix.toLowerCase())
          ),
    [names, prefix]
  );
  const open = matches.length > 0 && !dismissed;
  const active = matches.length > 0 ? Math.min(index, matches.length - 1) : 0;

  const insert = useCallback(
    (name: string) => {
      setDraft((current) => "@" + name + " " + current.replace(/^@\S*\s?/, ""));
      setDismissed(true);
      setIndex(0);
    },
    [setDraft]
  );

  const reset = useCallback(() => {
    setDismissed(false);
    setIndex(0);
  }, []);

  const onKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLTextAreaElement>) => {
      if (!open) {
        return false;
      }
      if (event.key === "ArrowDown") {
        event.preventDefault();
        setIndex((current) => (current + 1) % matches.length);
        return true;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        setIndex((current) => (current - 1 + matches.length) % matches.length);
        return true;
      }
      if (event.key === "Enter") {
        event.preventDefault();
        const pick = matches[active] || matches[0];
        if (pick) {
          insert(pick);
        }
        return true;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        setDismissed(true);
        return true;
      }
      return false;
    },
    [open, matches, active, insert]
  );

  return { open, matches, index: active, insert, reset, onKeyDown };
}

/* Group rooms: the agent's own turn that made a reply, built from its
   session's events. */
function childTurnOf(events: SessionEvent[], turnId: string): ChatTurn | null {
  if (!turnId) {
    return null;
  }
  for (const item of buildTurns(events)) {
    if (item.kind === "turn" && item.turn.id === turnId) {
      return item.turn;
    }
  }
  return null;
}

/* The turn an agent is running now in a group room, shown live like a chat. */
function LiveChildTurn({ events, faces, childId }: { events: SessionEvent[]; faces: Record<string, FaceRef>; childId: string }) {
  let turnId = "";
  let startedAt = 0;
  let agent = "";
  for (const event of events) {
    if (event.kind === "turn/start") {
      turnId = event.turn_id || "";
      startedAt = event.created_at;
    }
    if (event.kind === "turn/end" && event.turn_id === turnId) {
      turnId = "";
    }
    const tagged = (event as { agent?: unknown }).agent;
    if (typeof tagged === "string" && tagged) {
      agent = tagged;
    }
  }
  if (!turnId) {
    return null;
  }
  const items = buildTurns(events, { running: true, turnId, startedAt });
  const live = items.find((item) => item.kind === "turn" && item.turn.id === turnId);
  if (!live || live.kind !== "turn") {
    return null;
  }
  const face = faces[agent];
  return (
    <div className="group-turn">
      <TurnBlock turn={live.turn} agent={agent || "agent"} agentLabel={faceLabel(faces, agent)} agentColor={face ? face.color : undefined} sessionId={childId} />
    </div>
  );
}
