/* Spec 47 room discussion: the discussion/* session events of one turn
   (API-RDS-001..003) folded into one log the room renders as a group chat. */

export interface DiscussionPoint {
  text: string;
  agent: string;
}

export interface DiscussionSummary {
  conclusion: string;
  agreed: DiscussionPoint[];
  different: DiscussionPoint[];
  open: DiscussionPoint[];
}

export type DiscussionEntry =
  | { kind: "ask"; key: string; callId: string; agent: string; text: string; round: number; at: number }
  | {
      kind: "reply";
      key: string;
      callId: string;
      agent: string;
      text: string;
      round: number;
      replyTo: string;
      quote: string;
      passed: boolean;
      failed: boolean;
      at: number;
    }
  | { kind: "extra"; key: string; text: string; round: number; at: number }
  | { kind: "stop"; key: string; mode: string; round: number; at: number };

export interface DiscussionEnd {
  reason: string;
  rounds: number;
  limit: number;
  summary: DiscussionSummary;
  parts: Record<string, string>;
  notesSaved: number;
  sharedWith: string[];
  learning: string[];
  at: number;
}

export interface DiscussionLog {
  id: string;
  topic: string;
  opening: string;
  lead: string;
  members: { agent: string; angle: string }[];
  rounds: number;
  extraRounds: string;
  requested: boolean;
  startedAt: number;
  startCallId: string;
  entries: DiscussionEntry[];
  ended: DiscussionEnd | null;
}

type Payload = Record<string, unknown>;

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function num(value: unknown): number {
  const n = Number(value);
  return Number.isFinite(n) ? n : 0;
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

function points(value: unknown): DiscussionPoint[] {
  if (!Array.isArray(value)) {
    return [];
  }
  const out: DiscussionPoint[] = [];
  for (const item of value) {
    if (item && typeof item === "object") {
      const record = item as Payload;
      const text = str(record.text).trim();
      if (text) {
        out.push({ text, agent: str(record.agent) });
      }
    }
  }
  return out;
}

export function isDiscussionKind(kind: string): boolean {
  return kind === "discussion/started" || kind === "discussion/message" || kind === "discussion/ended";
}

/* Applies one discussion event to the turn's log and returns the log. */
export function applyDiscussionEvent(
  log: DiscussionLog | null,
  kind: string,
  payload: Payload,
  seq: number,
  at: number
): DiscussionLog | null {
  if (kind === "discussion/started") {
    const members = Array.isArray(payload.members)
      ? (payload.members as unknown[]).flatMap((item) => {
          if (!item || typeof item !== "object") {
            return [];
          }
          const record = item as Payload;
          const agent = str(record.agent);
          return agent ? [{ agent, angle: str(record.angle) }] : [];
        })
      : [];
    return {
      id: str(payload.id),
      topic: str(payload.topic),
      opening: str(payload.opening),
      lead: str(payload.lead),
      members,
      rounds: num(payload.rounds) || 3,
      extraRounds: str(payload.extra_rounds) || "once",
      requested: payload.requested === true,
      startedAt: at,
      startCallId: str(payload.call_id),
      entries: [],
      ended: null,
    };
  }
  if (!log) {
    return log;
  }
  if (kind === "discussion/message") {
    const role = str(payload.role);
    const key = "disc-" + seq;
    const round = num(payload.round);
    let entry: DiscussionEntry | null = null;
    if (role === "owner") {
      entry = { kind: "stop", key, mode: str(payload.stop) || "stop", round, at };
    } else if (role === "lead" && payload.extra === true) {
      entry = { kind: "extra", key, text: str(payload.text), round, at };
    } else if (role === "lead") {
      entry = { kind: "ask", key, callId: str(payload.call_id), agent: str(payload.agent), text: str(payload.text), round, at };
    } else if (role === "member") {
      entry = {
        kind: "reply",
        key,
        callId: str(payload.call_id),
        agent: str(payload.agent),
        text: str(payload.text),
        round,
        replyTo: str(payload.reply_to),
        quote: str(payload.quote),
        passed: payload.passed === true,
        failed: payload.failed === true,
        at,
      };
    }
    return entry ? { ...log, entries: log.entries.concat([entry]) } : log;
  }
  if (kind === "discussion/ended") {
    const summary = (payload.summary && typeof payload.summary === "object" ? payload.summary : {}) as Payload;
    const parts: Record<string, string> = {};
    if (payload.parts && typeof payload.parts === "object") {
      for (const [name, value] of Object.entries(payload.parts as Payload)) {
        if (typeof value === "string" && value) {
          parts[name] = value;
        }
      }
    }
    return {
      ...log,
      ended: {
        reason: str(payload.reason),
        rounds: num(payload.rounds),
        limit: num(payload.limit),
        summary: {
          conclusion: str(summary.conclusion),
          agreed: points(summary.agreed),
          different: points(summary.different),
          open: points(summary.open),
        },
        parts,
        notesSaved: num(payload.notes_saved),
        sharedWith: strings(payload.shared_with),
        learning: strings(payload.learning),
        at,
      },
    };
  }
  return log;
}

export type FaceState = "done" | "speaking" | "waiting" | "passed";

export interface DiscussionStatus {
  open: boolean;
  round: number;
  limit: number;
  /* Members asked and not answered yet, in ask order. */
  pending: string[];
  faces: { agent: string; state: FaceState }[];
}

/* The live state the discussion bar shows (REQ-RDS-051). */
export function discussionStatus(log: DiscussionLog): DiscussionStatus {
  const asked = new Map<string, number>();
  const answered = new Map<string, number>();
  const passed = new Set<string>();
  let round = 0;
  let limit = log.rounds;
  let stopped = false;
  for (const entry of log.entries) {
    if (entry.kind === "ask") {
      asked.set(entry.agent, (asked.get(entry.agent) || 0) + 1);
      round = Math.max(round, entry.round);
    } else if (entry.kind === "reply") {
      answered.set(entry.agent, (answered.get(entry.agent) || 0) + 1);
      if (entry.passed) {
        passed.add(entry.agent);
      } else {
        passed.delete(entry.agent);
      }
    } else if (entry.kind === "extra") {
      limit = Math.max(limit, entry.round);
    } else if (entry.kind === "stop") {
      stopped = true;
    }
  }
  const pending: string[] = [];
  for (const entry of log.entries) {
    if (entry.kind === "ask" && (asked.get(entry.agent) || 0) > (answered.get(entry.agent) || 0) && !pending.includes(entry.agent)) {
      pending.push(entry.agent);
    }
  }
  const faces = log.members.map(({ agent }) => {
    let state: FaceState = "waiting";
    if (pending.includes(agent)) {
      state = "speaking";
    } else if (passed.has(agent)) {
      state = "passed";
    } else if ((answered.get(agent) || 0) > 0) {
      state = "done";
    }
    return { agent, state };
  });
  return { open: log.ended === null && !stopped, round, limit, pending, faces };
}
