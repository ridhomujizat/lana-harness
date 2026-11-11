import { useEffect, useState } from "react";
import { knowledgeSummary } from "../api/discussion";
import type { KnowledgeSummary } from "../api/discussion";
import type { CSSProperties, ReactNode } from "react";
import { Markdown } from "../lib/markdown";
import { discussionStatus } from "../lib/discussion";
import type { DiscussionEntry, DiscussionLog, DiscussionPoint, FaceState } from "../lib/discussion";
import { Button, Modal } from "../ui/gr8r";
import { AgentAvatar } from "./agent-avatar";
import { agentFaceColors, gr8rColor, TypingDots, useRooms } from "./presence";
import type { FaceRef } from "./presence";

/* Spec 47 room discussion views (design page "Room discussion"):
   the discussion as a normal group chat (REQ-RDS-050), the live bar
   (REQ-RDS-051), the Summary button and dialog (REQ-RDS-052), the summary
   card in a member's room (REQ-RDS-053), and the team avatar (REQ-RDS-056). */

type Faces = Record<string, FaceRef>;

function label(faces: Faces, name: string): string {
  const face = faces[name];
  if (face && face.label) {
    return face.label;
  }
  return name ? name.charAt(0).toUpperCase() + name.slice(1) : name;
}

function clock(at: number): string {
  if (!at) {
    return "";
  }
  const date = new Date(at);
  return Number.isNaN(date.getTime()) ? "" : date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function face(faces: Faces, name: string) {
  const ref = faces[name];
  return { color: gr8rColor(ref ? ref.color : undefined), fg: agentFaceColors(ref ? ref.color : undefined).fg };
}

/* ------------------------------------------------------------------ */
/* Team avatar: 2 faces diagonal, 3 in a triangle, 2 + "+N" above 3.   */
/* ------------------------------------------------------------------ */

export interface TeamAvatarItem {
  name: string;
  color?: string;
}

export function TeamAvatar({ items, size = 44, className }: { items: TeamAvatarItem[]; size?: number; className?: string }) {
  const list = items.filter((item) => item.name);
  const three = list.length >= 3;
  const faceSize = Math.round(size * (three ? 0.56 : 0.64));
  const style = { "--box": size + "px", "--face": faceSize + "px" } as CSSProperties;
  const first = list[0];
  const second = list[1];
  const third = list.length === 3 ? list[2] : null;
  const more = list.length > 3 ? list.length - 2 : 0;
  return (
    <span
      className={"team-av " + (three ? "team-av-3" : "team-av-2") + (className ? " " + className : "")}
      style={style}
      role="img"
      aria-label={list.map((item) => item.name).join(", ")}
    >
      {second ? <AgentAvatar className="team-av-face team-av-b1" name={second.name} color={second.color} size="sm" /> : null}
      {third ? <AgentAvatar className="team-av-face team-av-b2" name={third.name} color={third.color} size="sm" /> : null}
      {more > 0 ? <span className="av sm team-av-face team-av-b2 team-av-more">{"+" + more}</span> : null}
      {first ? <AgentAvatar className="team-av-face team-av-top" name={first.name} color={first.color} size="sm" /> : null}
    </span>
  );
}

/* ------------------------------------------------------------------ */
/* Knowledge tags (spec 47 knowledge-aware discussions and team work)  */
/* ------------------------------------------------------------------ */

/* "▤ Used N notes": opens the list of the notes a reply used. */
export function NotesTag({ notes }: { notes: string[] }) {
  const [open, setOpen] = useState(false);
  if (notes.length === 0) {
    return null;
  }
  return (
    <span className="notes-tag-wrap">
      <button type="button" className="notes-tag" aria-expanded={open} onClick={() => setOpen((v) => !v)}>
        {"▤ Used " + notes.length + (notes.length === 1 ? " note" : " notes")}
      </button>
      {open ? (
        <span className="notes-pop" role="note">
          {notes.map((note) => (
            <a key={note} className="notes-pop-item" href={"#/knowledge?q=" + encodeURIComponent(note)}>
              {"▤ " + note}
            </a>
          ))}
        </span>
      ) : null}
    </span>
  );
}

function KnowsChip({
  agent,
  angle,
  notes,
  titles,
  faces,
}: {
  agent: string;
  angle: string;
  notes: number;
  titles: string[];
  faces: Faces;
}) {
  const [open, setOpen] = useState(false);
  const ref = faces[agent];
  const colors = agentFaceColors(ref ? ref.color : undefined);
  return (
    <span className="notes-tag-wrap">
      <button
        type="button"
        className="disc-chip knows-chip"
        style={{ background: colors.bg, color: colors.fg }}
        aria-expanded={open}
        disabled={titles.length === 0}
        onClick={() => setOpen((v) => !v)}
      >
        {label(faces, agent) + (angle ? " · " + angle : "")}
        {notes > 0 ? <span className="knows-n">{"▤ " + notes + (notes === 1 ? " note" : " notes")}</span> : null}
      </button>
      {open ? (
        <span className="notes-pop" role="note">
          <span className="notes-pop-title">{"Why " + label(faces, agent)}</span>
          {titles.map((title) => (
            <a key={title} className="notes-pop-item" href={"#/knowledge?q=" + encodeURIComponent(title)}>
              {"▤ " + title}
            </a>
          ))}
        </span>
      ) : null}
    </span>
  );
}

/* ------------------------------------------------------------------ */
/* The discussion as a group chat                                      */
/* ------------------------------------------------------------------ */

function Msg({
  name,
  faces,
  lead,
  meta,
  at,
  children,
  working,
  tags,
}: {
  name: string;
  faces: Faces;
  lead?: boolean;
  meta?: string;
  at?: number;
  children?: ReactNode;
  working?: boolean;
  tags?: ReactNode;
}) {
  const f = face(faces, name);
  return (
    <div className={"group-msg disc-msg" + (lead ? " group-lead-msg" : " group-member-msg")}>
      <AgentAvatar name={name} color={f.color} size="md" presence={working} />
      <div className="group-msg-main">
        <div className="group-msg-head">
          <span className="group-msg-name" style={{ color: f.fg }}>
            {label(faces, name)}
          </span>
          {lead ? <span className="group-lead-pill">lead</span> : null}
          {at ? <span className="group-msg-time">{clock(at)}</span> : null}
          {meta ? <span className="group-msg-meta">{meta}</span> : null}
          {tags}
        </div>
        {children}
      </div>
    </div>
  );
}

function Entry({ entry, log, faces }: { entry: DiscussionEntry; log: DiscussionLog; faces: Faces }) {
  switch (entry.kind) {
    case "ask":
      return (
        <Msg name={log.lead} faces={faces} lead at={entry.at}>
          <div className="turn-text group-msg-text">
            <Markdown text={entry.text} />
          </div>
        </Msg>
      );
    case "extra":
      return (
        <Msg name={log.lead} faces={faces} lead at={entry.at}>
          <div className="turn-text group-msg-text">
            <Markdown text={entry.text} />
          </div>
          <span className="disc-chip disc-chip-lead">{"Extra round · " + entry.round + "/" + log.rounds}</span>
        </Msg>
      );
    case "stop":
      return (
        <div className="disc-divider" role="note">
          <span>
            {entry.mode === "direct"
              ? "You asked for a direct answer"
              : "You stopped the discussion" + (entry.round ? " in round " + entry.round : "")}
          </span>
        </div>
      );
    case "reply": {
      if (entry.failed) {
        return (
          <div className="disc-failed" role="note">
            <AgentAvatar name={entry.agent} color={face(faces, entry.agent).color} size="sm" />
            <span>{label(faces, entry.agent) + " could not answer: " + (entry.text || "no answer") + ". " + label(faces, log.lead) + " goes on without " + label(faces, entry.agent) + "."}</span>
          </div>
        );
      }
      if (entry.passed) {
        return (
          <div className="disc-pass">
            <AgentAvatar name={entry.agent} color={face(faces, entry.agent).color} size="sm" />
            <span>{label(faces, entry.agent) + " passed · " + entry.text}</span>
          </div>
        );
      }
      return (
        <Msg
          name={entry.agent}
          faces={faces}
          at={entry.at}
          meta={entry.replyTo ? "replying to " + label(faces, entry.replyTo) : undefined}
          tags={
            <>
              <NotesTag notes={entry.notes} />
              {entry.newPoint ? <span className="disc-chip new-point">New point</span> : null}
            </>
          }
        >
          {entry.replyTo && entry.quote ? (
            <div className="disc-quote" style={{ borderColor: face(faces, entry.replyTo).fg }}>
              {label(faces, entry.replyTo) + ": " + entry.quote}
            </div>
          ) : null}
          <div className="turn-text group-msg-text">
            <Markdown text={entry.text} />
          </div>
        </Msg>
      );
    }
    default:
      return null;
  }
}

export interface DiscussionThreadProps {
  log: DiscussionLog;
  faces: Faces;
  running: boolean;
  /* "Answer directly instead" on the opening while the discussion runs. */
  onDirect?: () => void;
}

export function DiscussionThread({ log, faces, running, onDirect }: DiscussionThreadProps) {
  const status = discussionStatus(log);
  return (
    <div className="disc-thread">
      <Msg name={log.lead} faces={faces} lead at={log.startedAt}>
        {log.opening ? (
          <div className="turn-text group-msg-text">
            <Markdown text={log.opening} />
          </div>
        ) : null}
        <div className="disc-setup">
          {log.members.map((member) => (
            <KnowsChip key={member.agent} {...member} faces={faces} />
          ))}
          <span className="disc-chip">{"Up to " + log.rounds + " rounds"}</span>
          {log.skipped.map((skip) => (
            <span key={skip.agent} className="disc-skipped">
              {label(faces, skip.agent) + " not invited · " + skip.reason}
            </span>
          ))}
          {onDirect && status.open && running ? (
            <button type="button" className="disc-link" onClick={onDirect}>
              Answer directly instead
            </button>
          ) : null}
        </div>
      </Msg>
      {log.entries.map((entry) => (
        <Entry key={entry.key} entry={entry} log={log} faces={faces} />
      ))}
      {running && status.open
        ? status.pending.map((agent) => (
            <div className="group-msg group-member-msg group-working" role="status" key={"pending-" + agent}>
              <AgentAvatar name={agent} color={face(faces, agent).color} presence size="md" />
              <div className="group-msg-main">
                <span className="group-working-text">
                  <TypingDots />
                  <span>{label(faces, agent) + " is replying…"}</span>
                </span>
              </div>
            </div>
          ))
        : null}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Live bar                                                            */
/* ------------------------------------------------------------------ */

const FACE_WORDS: Record<FaceState, string> = {
  done: "spoke",
  speaking: "speaking",
  waiting: "waiting",
  passed: "passed",
};

export interface DiscussionBarProps {
  log: DiscussionLog;
  faces: Faces;
  onStop: () => void;
  stopping: boolean;
}

export function DiscussionBar({ log, faces, onStop, stopping }: DiscussionBarProps) {
  const status = discussionStatus(log);
  const lead = label(faces, log.lead);
  const action = stopping
    ? lead + " is summing up"
    : status.pending.length > 0
      ? lead + ": asking " + status.pending.map((name) => label(faces, name)).join(", ")
      : lead + " is thinking";
  return (
    <div className="disc-bar" role="status" aria-label="Discussion">
      <span className="disc-bar-tag">Discussion</span>
      <div className="disc-bar-main">
        <span className="disc-bar-topic">{log.topic}</span>
        <span className="disc-bar-status">{action}</span>
      </div>
      <div className="disc-bar-faces">
        {status.faces.map(({ agent, state }) => (
          <span
            key={agent}
            className={"disc-face is-" + state}
            title={label(faces, agent) + " · " + FACE_WORDS[state]}
          >
            <AgentAvatar name={agent} color={face(faces, agent).color} size="sm" />
          </span>
        ))}
      </div>
      <span className="disc-bar-round" title="Round">
        {Math.max(1, status.round) + "/" + status.limit}
      </span>
      <Button size="sm" disabled={stopping} onClick={onStop}>
        {stopping ? "Stopping…" : "Stop"}
      </Button>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Summary                                                             */
/* ------------------------------------------------------------------ */

function Points({ title, points, faces }: { title: string; points: DiscussionPoint[]; faces: Faces }) {
  if (points.length === 0) {
    return null;
  }
  return (
    <section className="disc-sum-sec">
      <h3 className="disc-sum-label">{title}</h3>
      <ul>
        {points.map((point, index) => {
          const ref = point.agent ? faces[point.agent] : undefined;
          const colors = agentFaceColors(ref ? ref.color : undefined);
          return (
            <li key={index}>
              <span className="disc-sum-text">{point.text}</span>
              {point.agent ? (
                <span className="disc-chip" style={{ background: colors.bg, color: colors.fg }}>
                  {label(faces, point.agent)}
                </span>
              ) : null}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

export function SummaryDialog({
  log,
  faces,
  room,
  onClose,
  onMemory,
}: {
  log: DiscussionLog;
  faces: Faces;
  room: string;
  onClose: () => void;
  onMemory?: () => void;
}) {
  const ended = log.ended;
  if (!ended) {
    return null;
  }
  const members = log.members.map((m) => label(faces, m.agent)).join(", ");
  const sub =
    room +
    " · " +
    clock(log.startedAt) +
    "–" +
    clock(ended.at) +
    " · " +
    ended.rounds +
    (ended.rounds === 1 ? " round" : " rounds") +
    " · " +
    members +
    " · led by " +
    label(faces, log.lead);
  const after: string[] = [];
  if (ended.notesSaved > 0) {
    after.push("Saved " + ended.notesSaved + (ended.notesSaved === 1 ? " note" : " notes") + " to room memory");
  }
  if (ended.sharedWith.length > 0) {
    after.push("Summary sent to " + ended.sharedWith.map((name) => label(faces, name)).join(" and ") + (ended.sharedWith.length === 1 ? "'s room" : "'s rooms"));
  }
  if (ended.learning.length > 0) {
    after.push(ended.learning.map((name) => label(faces, name)).join(" and ") + " will learn from it");
  }
  return (
    <Modal
      title="Discussion summary"
      subtitle={sub}
      onClose={onClose}
      className="disc-summary"
      footer={
        <>
          {onMemory ? <Button onClick={onMemory}>View room memory</Button> : null}
          <Button variant="primary" onClick={onClose} data-autofocus>
            Close
          </Button>
        </>
      }
    >
      <h2 className="disc-sum-title">{ended.summary.conclusion}</h2>
      <Points title="Agreed" points={ended.summary.agreed} faces={faces} />
      <Points title="Still different" points={ended.summary.different} faces={faces} />
      <Points title="Open" points={ended.summary.open} faces={faces} />
      {after.length > 0 ? (
        <ul className="disc-sum-after">
          {after.map((line) => (
            <li key={line}>{line}</li>
          ))}
        </ul>
      ) : null}
    </Modal>
  );
}

export function SummaryButton({
  log,
  faces,
  room,
  onMemory,
}: {
  log: DiscussionLog;
  faces: Faces;
  room: string;
  onMemory?: () => void;
}) {
  const [open, setOpen] = useState(false);
  if (!log.ended) {
    return null;
  }
  return (
    <>
      <Button size="sm" icon="list" className="disc-summary-btn" onClick={() => setOpen(true)}>
        Summary
      </Button>
      {open ? (
        <SummaryDialog
          log={log}
          faces={faces}
          room={room}
          onClose={() => setOpen(false)}
          onMemory={
            onMemory
              ? () => {
                  setOpen(false);
                  onMemory();
                }
              : undefined
          }
        />
      ) : null}
    </>
  );
}

/* ------------------------------------------------------------------ */
/* Member room card                                                    */
/* ------------------------------------------------------------------ */

export function DiscussionCard({ team, text, at }: { team: string; text: string; at: number }) {
  const { byName } = useRooms();
  const row = byName[team];
  const teamLabel = row ? row.display_name || row.name : team;
  const members: TeamAvatarItem[] = row
    ? [row.lead, ...(row.members || [])].filter(Boolean).map((name) => ({
        name,
        color: gr8rColor(byName[name] ? byName[name].color : undefined),
      }))
    : [];
  const lines = text.split("\n");
  const conclusion = lines[0] || "";
  const rest = lines.slice(1).join("\n").trim();
  return (
    <div className="disc-card">
      <div className="disc-card-head">
        {members && members.length > 0 ? <TeamAvatar items={members} size={24} /> : null}
        <span className="disc-card-room">{(teamLabel || team) + " · discussion"}</span>
        <span className="disc-card-time">{clock(at)}</span>
      </div>
      <p className="disc-card-conclusion">{conclusion}</p>
      {rest ? (
        <div className="disc-card-part turn-text">
          <Markdown text={rest} />
        </div>
      ) : null}
      <a className="disc-link" href={"#/chats/" + encodeURIComponent(team)}>
        Open discussion
      </a>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* What an agent knows and can do (Room settings › Members, New room)  */
/* ------------------------------------------------------------------ */

const summaryCache = new Map<string, KnowledgeSummary>();

export function AgentKnows({ name, can }: { name: string; can?: string[] }) {
  const [summary, setSummary] = useState<KnowledgeSummary | null>(() => summaryCache.get(name) || null);
  useEffect(() => {
    if (summaryCache.has(name)) {
      return undefined;
    }
    let live = true;
    knowledgeSummary(name)
      .then((value) => {
        summaryCache.set(name, value);
        if (live) {
          setSummary(value);
        }
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [name]);
  const words = (can || []).slice(0, 4);
  if ((!summary || summary.pages === 0) && words.length === 0) {
    return null;
  }
  return (
    <span className="agent-knows">
      {summary && summary.pages > 0 ? (
        <span className="agent-knows-pages">{"▤ " + summary.pages + (summary.pages === 1 ? " page" : " pages")}</span>
      ) : null}
      {summary && summary.topics.length > 0 ? <span className="agent-knows-topics">{"· " + summary.topics.join(", ")}</span> : null}
      {words.map((word) => (
        <span key={word} className="handoff-tool">
          {word}
        </span>
      ))}
    </span>
  );
}
