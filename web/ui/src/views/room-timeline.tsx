import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import { answerApproval } from "../api/approvals";
import type { ApprovalEvent, ApprovalProof } from "../api/approvals";
import { uploadAttachments } from "../api/attachments";
import { ApiError, errorMessage, openStream } from "../api/client";
import type { StreamState } from "../api/client";
import { answerQuestion, skipAllQuestions } from "../api/questions";
import type { QuestionAnswerRequest, QuestionEvent } from "../api/questions";
import { markRoomRead, roomSegments, sendToRoom } from "../api/rooms";
import { stopDiscussion } from "../api/discussion";
import { discussionStatus } from "../lib/discussion";
import { DiscussionBar, TeamAvatar } from "./discussion";
import { RoomSettings } from "./room-settings";
import type { RoomSettingsTab } from "./room-settings";
import type { RoomRow, Segment, SendRoomRequest } from "../api/rooms";
import { listEvents } from "../api/sessions";
import { cancelTurn, compactSession } from "../api/turns";
import { NewSessionBanner, SlashMenu, useSlashMenu } from "./slash-menu";
import { ContextMeter } from "./context-meter";
import type { SlashCommand } from "./slash-menu";
import type { SessionEvent, JobNoticePayload } from "../api/sessions";
import { knowledgeCompiledLine, jobNoticeOf, memoryOpLine } from "../lib/format";
import { useSessionPresence } from "../lib/session-presence";
import { ErrorDetails } from "../ui/ui";
import { Ic } from "../ui/icons";
import { pinToBottom, useStickToBottom } from "./scroll-pin";
import { Badge, Button, IconButton, Menu, MenuItem, MenuSeparator, Skeleton } from "../ui/gr8r";
import { AgentAvatar } from "./agent-avatar";
import { JobNoticeLine } from "./job-notice";
import { ComposerTray } from "./composer-tray";
import { ApprovalCard } from "./approval";
import type { ApprovalItem } from "./approval";
import {
  AttachButton,
  AttachmentChips,
  AttachmentDropOverlay,
  pasteImageFiles,
  useAttachmentDraft,
  useFileDrop,
} from "./attachments";
import { gr8rColor, PresenceLabel, TypingDots, useRooms } from "./presence";
import type { FaceRef } from "./presence";
import { agentLabel, displayLabel } from "./agent-label";
import { openQuestionFromEvents, QuestionCard } from "./question";
import { SecretRequestCard, useSecretRequest, useSecretStream } from "./secret-request";
import type { OpenQuestion } from "./question";
import {
  MentionChips,
  MentionList,
  RoomGroup,
  useMentions,
} from "./room-group";
import { storedAttachments } from "./session";
import { buildTurns, ReplyChip, TurnBlock, turnMood, UserBubble } from "./turn";
import type { ChatTurn, LiveTurnState, ReplyTarget, ThreadItem } from "./turn";

/* The room timeline of REQ-ROOM-002 to REQ-ROOM-005. It joins the room's
   segments into one bottom-anchored thread, streams the newest segment like
   the session view, and sends messages through API-ROOM-003. */

const LIMIT = 5;
const STICK_THRESHOLD = 96;

const CHANNEL_NAMES: Record<string, string> = {
  web: "Web",
  telegram: "Telegram",
  cli: "CLI",
  library: "Library",
};

const MONTHS = [
  "Jan",
  "Feb",
  "Mar",
  "Apr",
  "May",
  "Jun",
  "Jul",
  "Aug",
  "Sep",
  "Oct",
  "Nov",
  "Dec",
];

/* REQ-ROOM-002: the segment channel of the divider. */
export function channelName(value: string): string {
  return CHANNEL_NAMES[value] || (value ? value : "Unknown");
}

export function shortDate(ms: number): string {
  const date = new Date(ms);
  if (!ms || Number.isNaN(date.getTime())) {
    return "";
  }
  return date.getDate() + " " + MONTHS[date.getMonth()];
}

/* REQ-ROOM-001: the relative time of a room row (now, 2 min, 1 h, Yesterday). */
export function relativeShort(ms: number): string {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) {
    return "";
  }
  const minutes = Math.floor(Math.max(0, Date.now() - value) / 60000);
  if (minutes < 1) {
    return "now";
  }
  if (minutes < 60) {
    return minutes + " min";
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return hours + " h";
  }
  const then = new Date(value);
  const now = new Date();
  const startToday = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate()
  ).getTime();
  const startThen = new Date(
    then.getFullYear(),
    then.getMonth(),
    then.getDate()
  ).getTime();
  const days = Math.round((startToday - startThen) / 86400000);
  if (days <= 1) {
    return "Yesterday";
  }
  return then.toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

function dayKey(ms: number): string {
  const date = new Date(ms);
  if (!ms || Number.isNaN(date.getTime())) {
    return "";
  }
  return date.getFullYear() + "-" + date.getMonth() + "-" + date.getDate();
}

function dayLabel(ms: number): string {
  const date = new Date(ms);
  if (!ms || Number.isNaN(date.getTime())) {
    return "";
  }
  const now = new Date();
  const startToday = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate()
  ).getTime();
  const startThen = new Date(
    date.getFullYear(),
    date.getMonth(),
    date.getDate()
  ).getTime();
  const days = Math.round((startToday - startThen) / 86400000);
  if (days <= 0) {
    return "Today";
  }
  if (days === 1) {
    return "Yesterday";
  }
  return date.toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

/* REQ-ROOM-004: the activity line maps the latest tool frame to a short verb. */
const ACTIVITY_PHRASES: Record<string, string> = {
  bash: "running bash",
  terminal: "running a command",
  read_file: "reading files",
  glob: "searching files",
  grep: "searching the text",
  write_file: "writing files",
  edit_file: "editing files",
  web_fetch: "fetching a page",
  web_search: "searching the web",
  task: "delegating a task",
  task_continue: "continuing a task",
  memory_read: "reading memory",
  memory_write: "writing memory",
  skill_load: "loading a skill",
};

export function activityPhrase(name: string): string {
  const key = String(name || "").toLowerCase();
  return ACTIVITY_PHRASES[key] || (key ? "using " + key : "thinking");
}

/* REQ-ROOM-008: the room header names the working members. */
function workingLabel(names: string[]): string {
  if (names.length === 1) {
    return names[0] + " is working";
  }
  if (names.length === 2) {
    return names[0] + " and " + names[1] + " are working";
  }
  return names.length + " members are working";
}

function stringOf(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/* API-ROOM-002 orders segments by last activity: the list stays ascending by
   updated_at everywhere, so the last segment is the one with the most recent
   activity. */
function byUpdatedAt(left: Segment, right: Segment): number {
  return left.updated_at - right.updated_at;
}

/* Merge a freshly loaded page into the loaded segments by session id, keeping
   the ascending updated_at order. */
function mergeSegments(current: Segment[], incoming: Segment[]): Segment[] {
  const byId = new Map(current.map((segment) => [segment.session_id, segment]));
  for (const segment of incoming) {
    byId.set(segment.session_id, segment);
  }
  return Array.from(byId.values()).sort(byUpdatedAt);
}

/* Append a streamed event to a loaded list. The list stays ascending by seq,
   so a newer seq appends in place without a sort; an out-of-order or duplicate
   seq falls back to the dedupe and sort. This keeps a stream replay O(n)
   instead of O(n^2). */
function mergeEvent(list: SessionEvent[], event: SessionEvent): SessionEvent[] {
  const last = list.length > 0 ? list[list.length - 1] : null;
  if (!last || event.seq > last.seq) {
    return list.concat([event]);
  }
  if (list.some((entry) => entry.seq === event.seq)) {
    return list;
  }
  return list.concat([event]).sort((left, right) => left.seq - right.seq);
}

/* The visible items of one segment: its turns (built from the event log plus
   the live inputs of the newest segment) and its summary and learned notices.
   The room memo caches this per segment (perf: chat load). */
function segmentEntries(
  segment: Segment,
  rawEvents: SessionEvent[],
  live: LiveTurnState,
  /* Spec 85 REQ-ARC-005/011: events at or before the room's clear are hidden. */
  clearedAt: number
): TimelineNode[] {
  const segEvents =
    clearedAt > 0
      ? rawEvents.filter((event) => Number(event.created_at) > clearedAt)
      : rawEvents;
  const threadEvents = segEvents.filter(
    (event) =>
      event.kind !== "knowledge/compiled" && event.kind !== "memory/captured"
  );
  const thread = buildTurns(threadEvents, live);
  /* One seq index per segment: a user item looks its event up by seq. */
  const bySeq = new Map<number, SessionEvent>();
  for (const event of segEvents) {
    bySeq.set(event.seq, event);
  }
  const entries: TimelineNode[] = [];
  for (const item of thread) {
    if (item.kind === "user") {
      /* REQ-JBN-020: a job notice is a system line, placed at its event time. */
      if (item.job) {
        const event = bySeq.get(Number(item.seq) || 0);
        entries.push({
          key: item.key,
          at: event ? event.created_at : segment.created_at,
          kind: "job",
          sessionId: segment.session_id,
          job: item.job,
        });
        continue;
      }
      const seq = Number(String(item.key).replace("user-", ""));
      const event = bySeq.get(seq);
      entries.push({
        key: item.key,
        at: event ? event.created_at : segment.created_at,
        kind: "user",
        sessionId: segment.session_id,
        item,
      });
    } else {
      entries.push({
        key: item.key,
        at: item.turn.lastAt || item.turn.startedAt || segment.created_at,
        kind: "turn",
        sessionId: segment.session_id,
        turn: item.turn,
      });
    }
  }
  for (const event of segEvents) {
    if (event.kind === "compaction/summary") {
      entries.push({
        key: "sum-" + event.seq,
        at: event.created_at,
        kind: "summary",
        sessionId: segment.session_id,
      });
    } else if (
      event.kind === "knowledge/compiled" ||
      event.kind === "memory/captured"
    ) {
      const text = learnedLine(event);
      if (text) {
        entries.push({
          key: "learn-" + event.seq,
          at: event.created_at,
          kind: "learned",
          learnedText: text,
        });
      }
    }
  }
  entries.sort((left, right) => left.at - right.at);
  return entries;
}

/* A segment still expects a live turn while it is running or waiting on the
   owner; anything else is settled, so the working state clears. */
function isActiveStatus(status: string): boolean {
  return (
    status === "running" ||
    status === "waiting_approval" ||
    status === "waiting_input"
  );
}

/* The muted "Learned" line of a memory/captured or knowledge/compiled event
   (Q-004). The payload title or summary wins; otherwise the event is named. */
function learnedLine(event: SessionEvent): string {
  const payload =
    event.payload && typeof event.payload === "object"
      ? (event.payload as Record<string, unknown>)
      : {};
  const title = stringOf(payload.title) || stringOf(payload.summary);
  if (title) {
    return title;
  }
  if (event.kind === "knowledge/compiled") {
    return knowledgeCompiledLine(payload);
  }
  const ops = Array.isArray(payload.ops) ? payload.ops : [];
  return ops
    .map((op) => memoryOpLine(op))
    .filter(Boolean)
    .join(" ");
}

/* One muted line per learned note: clipped to a single line so a long
   memory does not take over the chat; a click shows the whole note. */
function LearnedLine({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  return (
    <button
      type="button"
      className={"room-learned" + (open ? " is-open" : "")}
      aria-expanded={open}
      title={open ? undefined : text}
      onClick={() => setOpen((v) => !v)}
    >
      <BookIcon />
      <span className="room-learned-text">
        <span className="room-learned-label">Learned</span> {text}
      </span>
    </button>
  );
}

function BookIcon() {
  return (
    <svg
      className="room-book-icon"
      viewBox="0 0 16 16"
      width="14"
      height="14"
      aria-hidden="true"
      focusable="false"
    >
      <path
        d="M8 3.6C7 2.8 5.5 2.5 3.5 2.5v9.4c2 0 3.5.3 4.5 1.1 1-.8 2.5-1.1 4.5-1.1V2.5c-2 0-3.5.3-4.5 1.1Zm0 0v9.4"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinejoin="round"
      />
    </svg>
  );
}

type UserThreadItem = Extract<ThreadItem, { kind: "user" }>;

type TimelineNode =
  | {
      key: string;
      at: number;
      kind: "divider";
      channel: string;
      date: string;
      /* REQ-ROOM-002: a lighter divider when the channel and day did not change. */
      topic: boolean;
    }
  | { key: string; at: number; kind: "day"; dayLabel: string }
  | { key: string; at: number; kind: "summary"; sessionId: string }
  | { key: string; at: number; kind: "learned"; learnedText: string }
  | { key: string; at: number; kind: "user"; sessionId: string; item: UserThreadItem }
  | { key: string; at: number; kind: "job"; sessionId: string; job: JobNoticePayload }
  | { key: string; at: number; kind: "turn"; sessionId: string; turn: ChatTurn };

export interface RoomViewProps {
  room: RoomRow;
  /* Spec 48: composer text prefilled from the route (?draft=). */
  initialDraft?: string;
  /* "dock": the compact chat dock window, which draws its own header. */
  variant?: "page" | "dock";
  /* Spec 85 REQ-ARC-009: the room header's ⋯ menu actions. */
  onArchive?: (archived: boolean) => void;
  onClear?: () => void;
}

export function RoomView({ room, initialDraft, variant = "page", onArchive, onClear }: RoomViewProps) {
  /* API-WF-021: the reply of a workflow chat command shown above the composer. */
  const [commandReply, setCommandReply] = useState("");
  const [segments, setSegments] = useState<Segment[]>([]);
  const [events, setEvents] = useState<Record<string, SessionEvent[]>>({});
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [actionError, setActionError] = useState("");
  const [actionDetails, setActionDetails] = useState<unknown[]>([]);
  const [turnRunning, setTurnRunning] = useState(false);
  const [liveTurnId, setLiveTurnId] = useState("");
  const [liveStartedAt, setLiveStartedAt] = useState(0);
  const [liveText, setLiveText] = useState("");
  const [liveThinking, setLiveThinking] = useState(false);
  const [liveTool, setLiveTool] = useState("");
  /* Group rooms: the agent whose turn it is, its session, and the events of
     every agent session behind the room's replies. */
  const [liveSpeaker, setLiveSpeaker] = useState("");
  const [liveChild, setLiveChild] = useState("");
  const [childEvents, setChildEvents] = useState<Record<string, SessionEvent[]>>({});
  const [streamState, setStreamState] = useState<StreamState>("connected");
  const [approvals, setApprovals] = useState<ApprovalItem[]>([]);
  const [question, setQuestion] = useState<OpenQuestion | null>(null);
  const [questionBusy, setQuestionBusy] = useState(false);
  const [closedQuestionIds, setClosedQuestionIds] = useState<string[]>([]);
  /* Spec 53: secret_request stream events, read by useSecretRequest below. */
  const secretStream = useSecretStream();
  const receiveSecret = secretStream.receive;
  const [draft, setDraft] = useState(initialDraft || "");
  /* API reply: the automation message the next send replies to. */
  const [replyTarget, setReplyTarget] = useState<ReplyTarget | null>(null);
  const [newTopic, setNewTopic] = useState(false);
  /* Spec 47 REQ-RDS-020: Discuss on for the next message. */
  const [discuss, setDiscuss] = useState(false);
  const [settingsTab, setSettingsTab] = useState<RoomSettingsTab | null>(null);
  const [stoppingId, setStoppingId] = useState("");
  const [sending, setSending] = useState(false);
  const [uploading, setUploading] = useState(false);
  /* REQ-JBN-022: bumped on a tool/result or job notice so the tray re-reads. */
  const [jobRefresh, setJobRefresh] = useState(0);
  /* The composer tray's background task panel; /tasks opens it. */
  const [tasksOpen, setTasksOpen] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  /* Spec 85 REQ-ARC-009: the header ⋯ menu. */
  const [menuOpen, setMenuOpen] = useState(false);
  const roomMenuRef = useRef<HTMLSpanElement>(null);
  const clearedAt = Number(room.cleared_at) || 0;
  const archived = Number(room.archived_at) > 0;
  const lana = room.name === "lana";

  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const stickRef = useRef(true);
  const prependRef = useRef<number | null>(null);
  const startedRef = useRef(false);
  /* REQ-ROOM-004: the last session the hub named. Seeded with the room's
     current last message so the reload effect does not fire on mount (the
     mount effect below already loads the newest page). */
  const handledSessionRef = useRef(
    room.last_message ? room.last_message.session_id : ""
  );
  /* The last event seq loaded per session, so the live stream opens from what
     was already read instead of replaying the whole log (perf: chat load). */
  const loadedSeqRef = useRef<Record<string, number>>({});
  /* Perf: the visible items of each segment, cached by its events array (and,
     for the newest segment, by the live-turn inputs it uses), so an unrelated
     event or live-state change does not rebuild every loaded segment. */
  const segmentCacheRef = useRef<
    Map<string, { events: SessionEvent[]; liveKey: string; items: TimelineNode[] }>
  >(new Map());
  /* Perf: the node keys already on screen when the chat opened (or when older
     messages were prepended). Only a row that arrives later animates in. */
  const heldKeysRef = useRef<Set<string>>(new Set());
  const firstLoadRef = useRef(false);
  const prependHoldRef = useRef(false);
  const attachmentDraft = useAttachmentDraft();
  const { byName, rows: roomRows } = useRooms();

  /* REQ-ROOM-008: the faces of the room, its lead and its members, with each
     agent's configured color for the group chat. */
  const faces = useMemo(() => {
    const map: Record<string, FaceRef> = {};
    for (const name of [room.name, room.lead, ...(room.members || [])]) {
      if (!name || map[name]) {
        continue;
      }
      const row = byName[name];
      map[name] = {
        name,
        label: agentLabel(name, byName),
        color: row ? row.color : undefined,
        presence: row ? row.presence : undefined,
      };
    }
    return map;
  }, [byName, room.name, room.lead, room.members]);

  /* The mention roster: the lead first, then the members, without repeats. */
  const mentionNames = useMemo(() => {
    const names = [room.lead, ...(room.members || [])].filter(Boolean);
    return Array.from(new Set(names));
  }, [room.lead, room.members]);

  const team = room.kind === "team";
  const mentions = useMentions(draft, setDraft, team ? mentionNames : []);
  /* The slash command menu (owner 2026-11-11). */
  const slashCommands: SlashCommand[] = [
    {
      name: "new",
      title: "New session",
      description: "Start a fresh conversation; the messages above stay but are not sent again.",
      icon: "square-pen",
      group: "Session",
      run: () => setNewTopic(true),
    },
    {
      name: "compact",
      title: "Compact",
      description: "Summarize this session so far to save tokens, then keep going.",
      icon: "minimize-2",
      group: "Session",
      run: () => void compactNow(),
    },
  ];
  if (team) {
    slashCommands.push({
      name: "discuss",
      title: "Discuss",
      description: "Ask the lead to open a discussion on your next message.",
      icon: "messages-square",
      group: "Work",
      run: () => setDiscuss(true),
    });
  } else {
    /* Composer redesign: /tasks opens the background task panel. */
    slashCommands.push({
      name: "tasks",
      title: "Background tasks",
      description: "Show this session's background tasks, their logs, and Stop.",
      icon: "square-terminal",
      group: "Work",
      run: () => setTasksOpen(true),
    });
  }
  const slash = useSlashMenu(draft, setDraft, slashCommands);

  const loadEvents = useCallback(async (ids: string[]) => {
    if (ids.length === 0) {
      return;
    }
    const loaded: Record<string, SessionEvent[]> = {};
    await Promise.all(
      ids.map(async (id) => {
        try {
          const page = await listEvents(id, 0, { view: "chat" });
          loaded[id] = page.events || [];
          /* Remember the page's last seq so the live stream can open from it. */
          loadedSeqRef.current[id] = Number(page.last_seq) || 0;
        } catch {
          loaded[id] = [];
        }
      })
    );
    setEvents((current) => ({ ...current, ...loaded }));
  }, []);

  /* REQ-ROOM-002: the newest page of segments loads first, ascending by
     updated_at so the last segment is the most recently active. Spec 85
     REQ-ARC-011: the page also reloads when the room's cleared_at changes, so
     the server's now-filtered segments replace the cleared ones at once. */
  useEffect(() => {
    let alive = true;
    roomSegments(room.name, { limit: LIMIT })
      .then((page) => {
        if (!alive) {
          return undefined;
        }
        const list = (page.segments || []).slice().sort(byUpdatedAt);
        setSegments(list);
        setHasMore(Boolean(page.has_more));
        return loadEvents(list.map((segment) => segment.session_id));
      })
      .then(() => {
        if (alive) {
          setLoading(false);
        }
      })
      .catch((failure: unknown) => {
        if (alive) {
          setLoadError(errorMessage(failure));
          setLoading(false);
        }
      });
    return () => {
      alive = false;
    };
  }, [room.name, loadEvents, clearedAt]);

  const lastSegment =
    segments.length > 0 ? segments[segments.length - 1] : null;
  const newestId = lastSegment ? lastSegment.session_id : "";
  /* The newest session's events are loaded (an empty page also counts, so the
     stream is not held back by a session with no events). */
  const newestLoaded = newestId ? events[newestId] !== undefined : false;
  /* Spec #42: the newest segment is the session this room shows. */
  useSessionPresence(newestId);

  const finishLive = useCallback(() => {
    startedRef.current = false;
    setTurnRunning(false);
    setLiveText("");
    setLiveThinking(false);
    setLiveTool("");
    setLiveSpeaker("");
    setLiveChild("");
    setLiveTurnId("");
    setLiveStartedAt(0);
  }, []);

  /* REQ-ROOM-004: a turn started elsewhere shows up in the timeline. When the
     hub names a new last session, reload the newest page, merge it in ascending
     updated_at order (so the active segment moves to the end) and clear the
     working state when that segment is already settled. */
  const lastSession = room.last_message ? room.last_message.session_id : "";
  useEffect(() => {
    if (!lastSession || handledSessionRef.current === lastSession) {
      return;
    }
    handledSessionRef.current = lastSession;
    let alive = true;
    roomSegments(room.name, { limit: LIMIT })
      .then((page) => {
        if (!alive) {
          return undefined;
        }
        const list = page.segments || [];
        setSegments((current) => mergeSegments(current, list));
        const target = list.find(
          (segment) => segment.session_id === lastSession
        );
        if (target && !isActiveStatus(target.status)) {
          finishLive();
        }
        return loadEvents(list.map((segment) => segment.session_id));
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [lastSession, room.name, loadEvents, finishLive]);

  /* The live tail streams the newest segment exactly like the session view. It
     opens only once that segment's events have loaded and resumes from the
     loaded last seq, so the server replays just what is new. A turn started
     here replays its own log over the stream. The connect frames (status,
     approval, question, secret_request) are sent regardless of from_seq, so
     the running state and the open cards still arrive (see web/sse.go). */
  useEffect(() => {
    if (!newestId || !newestLoaded) {
      return undefined;
    }
    const id = newestId;
    const stream = openStream(id, {
      fromSeq: loadedSeqRef.current[id] || 0,
      view: "chat",
      onState: (state) => setStreamState(state),
      onEvent: (event) => {
        /* Child sessions (a member's own session in a team room) stream their
           events too; the room shows only its own conversation. */
        const childId = (event as { child_session_id?: unknown }).child_session_id;
        if (typeof childId === "string" && childId) {
          setChildEvents((current) => {
            const list = current[childId] || [];
            const next = mergeEvent(list, event);
            if (next === list) {
              return current;
            }
            return { ...current, [childId]: next };
          });
          if (event.kind === "turn/start") {
            setLiveChild(childId);
          }
          return;
        }
        setEvents((current) => {
          const list = current[id] || [];
          const next = mergeEvent(list, event);
          if (next === list) {
            return current;
          }
          return { ...current, [id]: next };
        });
        const payload =
          event.payload && typeof event.payload === "object"
            ? (event.payload as Record<string, unknown>)
            : {};
        if (event.kind === "assistant/message" || event.kind === "tool/call") {
          setLiveText("");
          setLiveThinking(false);
        }
        /* REQ-JBN-022: refresh the background pill on a tool result or a job
           notice, so a job started mid-visit appears without a reload. */
        if (
          event.kind === "tool/result" ||
          (event.kind === "user/message" && jobNoticeOf(payload) !== null)
        ) {
          setJobRefresh((value) => value + 1);
        }
        if (event.kind === "turn/end") {
          finishLive();
        }
        if (event.kind === "question/answered") {
          const requestId = stringOf(payload.request_id);
          if (requestId) {
            setClosedQuestionIds((current) =>
              current.includes(requestId) ? current : current.concat([requestId])
            );
          }
        }
        if (event.kind === "approval/decided") {
          const requestId = stringOf(payload.request_id);
          const outcome = stringOf(payload.outcome) || "unavailable";
          setApprovals((current) =>
            current.map((item) =>
              item.request_id === requestId
                ? { ...item, decided: outcome, deciding: false }
                : item
            )
          );
        }
      },
      onFrame: (frame) => {
        /* A team handoff streams child frames tagged with their session id and
           agent (API-TEAM-011). The group room builds the member row from the
           delegation, so a child frame leaves the lead's live state alone. */
        if (stringOf(frame.child_session_id)) {
          /* Group rooms: a teammate's tool calls show who works on what. */
          if (frame.kind === "tool_call") {
            const agent = stringOf(frame.agent);
            if (agent) {
              setLiveSpeaker(agent);
            }
            setLiveTool(stringOf(frame.name));
          }
          return;
        }
        if (frame.kind === "speaker") {
          setLiveSpeaker(stringOf(frame.name));
          setLiveTool("");
          setLiveText("");
          return;
        }
        const turnId = stringOf(frame.turn_id);
        if (turnId) {
          setLiveTurnId(turnId);
        }
        if (!startedRef.current) {
          startedRef.current = true;
          setLiveStartedAt(Date.now());
        }
        if (frame.kind === "text_delta" && frame.text) {
          setLiveThinking(false);
          setLiveText((current) => current + String(frame.text));
          return;
        }
        if (frame.kind === "thinking_delta") {
          setLiveThinking(true);
          return;
        }
        if (frame.kind === "tool_call") {
          setLiveThinking(false);
          setLiveText("");
          setLiveTool(stringOf(frame.name));
          return;
        }
        if (frame.kind === "tool_result" || frame.kind === "step_retry") {
          setLiveThinking(false);
          setLiveText("");
        }
      },
      onStatus: (status) => {
        setTurnRunning(Boolean(status.turn_running));
      },
      onTurn: () => {
        finishLive();
      },
      onApproval: (approval: ApprovalEvent) => {
        if (!approval.request_id) {
          return;
        }
        setApprovals((current) =>
          current.some((item) => item.request_id === approval.request_id)
            ? current
            : current.concat([
                { ...approval, decided: "", deciding: false, error: "" },
              ])
        );
        setTurnRunning(true);
      },
      onQuestion: (incoming: QuestionEvent) => {
        if (!incoming.request_id) {
          return;
        }
        setQuestion((current) =>
          current && current.request_id === incoming.request_id
            ? current
            : {
                request_id: incoming.request_id,
                call_id: incoming.call_id || "",
                expires_at: Number(incoming.expires_at) || 0,
                turn_id: incoming.turn_id || "",
                questions: Array.isArray(incoming.questions)
                  ? incoming.questions
                  : [],
              }
        );
      },
      onSecretRequest: (incoming) => {
        receiveSecret(incoming);
      },
      onShutdown: () => {
        setStreamState("reconnecting");
      },
    });
    return () => {
      stream.close();
    };
  }, [newestId, newestLoaded, finishLive, receiveSecret]);

  /* REQ-ROOM-005: the room is marked read one second after open or a new
     message, so a burst of rows does not spam the endpoint. */
  const lastMessageAt = room.last_message ? room.last_message.at : 0;
  const lastMessageSession = room.last_message ? room.last_message.session_id : "";
  useEffect(() => {
    const timer = window.setTimeout(() => {
      void markRoomRead(room.name).catch(() => undefined);
    }, 1000);
    return () => window.clearTimeout(timer);
  }, [room.name, lastMessageAt, lastMessageSession]);

  const nodes = useMemo<TimelineNode[]>(() => {
    /* REQ-ROOM-002: build the visible items of each segment first, so a segment
       with no visible turn or notice is left out along with its divider. Each
       segment's items are cached by its events array (and, for the newest
       segment, by the live inputs it uses), so an unrelated event or live-state
       change does not rebuild every loaded segment (perf: chat load). */
    const cache = segmentCacheRef.current;
    const liveNow: LiveTurnState = {
      running: turnRunning,
      turnId: liveTurnId,
      startedAt: liveStartedAt,
    };
    const liveKey = turnRunning + "|" + liveTurnId + "|" + liveStartedAt;
    const blocks: { segment: Segment; items: TimelineNode[] }[] = [];
    const liveIds = new Set<string>();
    /* Spec 85 REQ-ARC-005: a segment last updated at or before the clear is
       gone from the timeline. */
    const shownSegments =
      clearedAt > 0
        ? segments.filter((segment) => Number(segment.updated_at) > clearedAt)
        : segments;
    for (let index = 0; index < shownSegments.length; index += 1) {
      const segment = shownSegments[index];
      if (!segment) {
        continue;
      }
      const id = segment.session_id;
      const segEvents = events[id] || [];
      const isNewest = id === newestId;
      const key = clearedAt + (isNewest ? "|" + liveKey : "");
      liveIds.add(id);
      let cached = cache.get(id);
      if (!cached || cached.events !== segEvents || cached.liveKey !== key) {
        cached = {
          events: segEvents,
          liveKey: key,
          items: segmentEntries(segment, segEvents, isNewest ? liveNow : {}, clearedAt),
        };
        cache.set(id, cached);
      }
      if (cached.items.length === 0) {
        continue;
      }
      blocks.push({ segment, items: cached.items });
    }
    for (const id of Array.from(cache.keys())) {
      if (!liveIds.has(id)) {
        cache.delete(id);
      }
    }

    /* A divider opens each visible segment: the full channel and date, or a
       lighter "New topic" when the channel and day did not change. The first
       visible segment always takes the full divider. */
    const list: TimelineNode[] = [];
    let first = true;
    let previousChannel = "";
    let previousDay = "";
    for (const block of blocks) {
      const channel = block.segment.channel;
      const day = dayKey(block.segment.created_at);
      const topic = !first && channel === previousChannel && day === previousDay;
      list.push({
        key: "div-" + block.segment.session_id,
        at: block.segment.created_at,
        kind: "divider",
        channel,
        date: shortDate(block.segment.created_at),
        topic,
      });
      for (const item of block.items) {
        list.push(item);
      }
      first = false;
      previousChannel = channel;
      previousDay = day;
    }

    /* REQ-ROOM-002: one day divider per day. A full segment divider already
       names its day, so the first message under it does not repeat it. */
    const withDays: TimelineNode[] = [];
    let lastDay = "";
    for (const node of list) {
      if (node.kind === "divider") {
        if (!node.topic) {
          lastDay = dayKey(node.at);
        }
        withDays.push(node);
        continue;
      }
      const key = dayKey(node.at);
      if (key && key !== lastDay) {
        withDays.push({
          key: "day-" + key + "-" + node.key,
          at: node.at,
          kind: "day",
          dayLabel: dayLabel(node.at),
        });
        lastDay = key;
      }
      withDays.push(node);
    }

    /* Perf: the rows present at the first load (and at each loadOlder prepend)
       mount without the entrance animation; only a row that arrives later (a
       streamed message) gets .msg-enter. */
    if (prependHoldRef.current) {
      prependHoldRef.current = false;
      for (const node of withDays) {
        heldKeysRef.current.add(node.key);
      }
    } else if (!firstLoadRef.current && withDays.length > 0) {
      firstLoadRef.current = true;
      for (const node of withDays) {
        heldKeysRef.current.add(node.key);
      }
    }
    return withDays;
  }, [segments, events, newestId, turnRunning, liveTurnId, liveStartedAt, clearedAt]);

  useStickToBottom(scrollRef, stickRef);

  /* API reply: clicking Reply sets a target and focuses the composer input. */
  useEffect(() => {
    if (replyTarget) {
      inputRef.current?.focus();
    }
  }, [replyTarget]);

  /* The timeline is bottom anchored: new content scrolls into view unless the
     owner has scrolled up (AC-ROOM-002, AC-ROOM-003). */
  useLayoutEffect(() => {
    const node = scrollRef.current;
    if (!node) {
      return;
    }
    if (prependRef.current !== null) {
      node.scrollTop = node.scrollHeight - prependRef.current;
      prependRef.current = null;
      return;
    }
    if (stickRef.current) {
      node.scrollTop = node.scrollHeight;
    }
  }, [nodes, liveText]);

  const currentWebSessionId = useMemo(() => {
    for (let index = segments.length - 1; index >= 0; index -= 1) {
      const current = segments[index];
      if (current && current.channel === "web") {
        return current.session_id;
      }
    }
    return "";
  }, [segments]);

  /* REQ-CHF-009: files dropped anywhere on the room become draft files, like
     the paperclip. Uploads go to the room's web session, so a room without one
     asks for a first message instead. */
  const addAttachmentFiles = attachmentDraft.addFiles;
  const dropFiles = useCallback(
    (files: File[]) => {
      if (!currentWebSessionId) {
        setActionError("Send a message first to attach files.");
        return;
      }
      addAttachmentFiles(files);
    },
    [currentWebSessionId, addAttachmentFiles]
  );
  const fileDrop = useFileDrop(dropFiles);

  const allEvents = useMemo(() => {
    const out: SessionEvent[] = [];
    for (const segment of segments) {
      const list = events[segment.session_id] || [];
      for (const event of list) {
        out.push(event);
      }
    }
    return out;
  }, [segments, events]);

  const activeQuestion = useMemo(() => {
    const found = openQuestionFromEvents(allEvents);
    if (found && !closedQuestionIds.includes(found.request_id)) {
      return found;
    }
    if (question && !closedQuestionIds.includes(question.request_id)) {
      return question;
    }
    return null;
  }, [allEvents, question, closedQuestionIds]);

  const activeApproval = approvals.find((item) => !item.decided) || null;
  const secrets = useSecretRequest(allEvents, childEvents, secretStream);

  /* AC-ROOM-003: scrolling to the top loads the previous page of segments and
     holds the visible position. */
  async function loadOlder() {
    if (loadingOlder || !hasMore || segments.length === 0) {
      return;
    }
    setLoadingOlder(true);
    /* The prepended rows are part of history, so they mount without animating. */
    prependHoldRef.current = true;
    const node = scrollRef.current;
    prependRef.current = node ? node.scrollHeight : 0;
    const oldest = segments[0];
    try {
      const page = await roomSegments(room.name, {
        before: oldest ? oldest.updated_at : 0,
        limit: LIMIT,
      });
      const older = page.segments || [];
      setSegments((current) => mergeSegments(current, older));
      setHasMore(Boolean(page.has_more));
      await loadEvents(older.map((segment) => segment.session_id));
    } catch {
      /* Keep the current page; a later scroll retries. */
    } finally {
      setLoadingOlder(false);
    }
  }

  function onScroll() {
    const node = scrollRef.current;
    if (!node) {
      return;
    }
    stickRef.current =
      node.scrollHeight - node.scrollTop - node.clientHeight < STICK_THRESHOLD;
    if (node.scrollTop <= 0 && hasMore && !loadingOlder) {
      void loadOlder();
    }
  }

  const submitQuestion = useCallback(
    async (
      request: QuestionEvent,
      send: (requestId: string) => Promise<unknown>
    ) => {
      const requestId = request && request.request_id;
      if (!requestId) {
        return { ok: false, message: "Could not send. Try again." };
      }
      setQuestionBusy(true);
      try {
        await send(requestId);
        setClosedQuestionIds((current) =>
          current.includes(requestId) ? current : current.concat([requestId])
        );
        setQuestion((current) =>
          current && current.request_id === requestId ? null : current
        );
        return { ok: true };
      } catch (failure) {
        if (failure instanceof ApiError && failure.status === 409) {
          setClosedQuestionIds((current) =>
            current.includes(requestId) ? current : current.concat([requestId])
          );
          return { ok: true };
        }
        return { ok: false, message: "Could not send. Try again." };
      } finally {
        setQuestionBusy(false);
      }
    },
    []
  );

  const handleQuestionSubmit = useCallback(
    (request: QuestionEvent, body: QuestionAnswerRequest) =>
      submitQuestion(request, (requestId) => answerQuestion(requestId, body)),
    [submitQuestion]
  );

  const handleQuestionSkipAll = useCallback(
    (request: QuestionEvent) =>
      submitQuestion(request, (requestId) => skipAllQuestions(requestId)),
    [submitQuestion]
  );

  const handleQuestionExpire = useCallback((request: QuestionEvent) => {
    const requestId = request && request.request_id;
    if (requestId) {
      setClosedQuestionIds((current) =>
        current.includes(requestId) ? current : current.concat([requestId])
      );
    }
  }, []);

  const handleApprovalAnswer = useCallback(
    async (approval: ApprovalEvent, outcome: string, proof?: ApprovalProof) => {
      setApprovals((current) =>
        current.map((item) =>
          item.request_id === approval.request_id
            ? { ...item, deciding: true, error: "" }
            : item
        )
      );
      try {
        await answerApproval(approval.request_id, outcome, undefined, proof);
        setApprovals((current) =>
          current.map((item) =>
            item.request_id === approval.request_id
              ? { ...item, decided: outcome, deciding: false }
              : item
          )
        );
      } catch (failure) {
        setApprovals((current) =>
          current.map((item) =>
            item.request_id === approval.request_id
              ? { ...item, deciding: false, error: errorMessage(failure) }
              : item
          )
        );
      }
    },
    []
  );

  const handleApprovalExpire = useCallback((approval: ApprovalEvent) => {
    setApprovals((current) =>
      current.map((item) =>
        item.request_id === approval.request_id
          ? { ...item, decided: item.decided || "unavailable", deciding: false }
          : item
      )
    );
  }, []);

  const handleApprovalDismiss = useCallback(() => {
    setApprovals((current) => current.filter((item) => !item.decided));
  }, []);

  /* REQ-ROOM-003: send to the room's current web session; a new topic asks
     the server to create a new one first (AC-ROOM-004, AC-ROOM-005). A new
     session starts only from the /new command, like Hermes and Telegram
     (owner 2026-11-11): "/new" alone arms it for the next message, and
     "/new <message>" sends that message in a fresh session. */
  async function sendMessage() {
    let text = draft.trim();
    const files = attachmentDraft.items.slice();
    if ((!text && files.length === 0) || sending) {
      return;
    }
    let startNew = newTopic;
    const command = /^\/new(?:\s+([\s\S]*))?$/i.exec(text);
    if (command) {
      text = (command[1] || "").trim();
      startNew = true;
      if (!text && files.length === 0) {
        setNewTopic(true);
        setDraft("");
        return;
      }
    }
    setSending(true);
    setActionError("");
    setActionDetails([]);
    setCommandReply("");
    setDraft("");
    pinToBottom(scrollRef, stickRef);
    const topic = startNew;
    try {
      let paths: string[] = [];
      if (files.length > 0 && currentWebSessionId) {
        setUploading(true);
        const uploaded = await uploadAttachments(
          currentWebSessionId,
          files.map((item) => item.file)
        );
        paths = uploaded.map((item) => item.path);
        setUploading(false);
      }
      const body: SendRoomRequest = { text: text || "(attachments)" };
      if (paths.length > 0) {
        body.attachments = paths;
      }
      if (topic) {
        body.new_topic = true;
      }
      if (discuss && team) {
        body.discuss = true;
      }
      if (replyTarget) {
        body.reply_to = replyTarget.seq;
      }
      const result = await sendToRoom(room.name, body);
      /* API reply: a successful send clears the target; the stored message now
         carries its own reply_to. */
      setReplyTarget(null);
      setNewTopic(false);
      setDiscuss(false);
      attachmentDraft.clear();
      if (result.command) {
        /* API-WF-021: a workflow chat command started a run, not a turn. */
        setCommandReply(result.command.reply);
        return;
      }
      /* The sent session now has the most recent activity: move it to the end
         and bump its updated_at so the live stream follows the session that
         just received the message (API-ROOM-002, API-ROOM-003). */
      if (result.session_id) {
        const now = Date.now();
        setSegments((current) => {
          const exists = current.some(
            (segment) => segment.session_id === result.session_id
          );
          const next = exists
            ? current.map((segment) =>
                segment.session_id === result.session_id
                  ? { ...segment, updated_at: now, status: "running" }
                  : segment
              )
            : current.concat([
                {
                  session_id: result.session_id,
                  channel: "web",
                  address: "owner",
                  created_at: now,
                  updated_at: now,
                  turn_count: 0,
                  status: "running",
                },
              ]);
          return next.slice().sort(byUpdatedAt);
        });
        await loadEvents([result.session_id]);
      }
      setTurnRunning(true);
      setLiveTool("");
      setLiveText("");
      setLiveThinking(false);
      if (result.turn_id) {
        startedRef.current = true;
        setLiveTurnId(result.turn_id);
        setLiveStartedAt(Date.now());
      }
    } catch (failure) {
      setDraft((current) => current || text);
      setActionError(errorMessage(failure));
      setActionDetails(failure instanceof ApiError ? failure.details : []);
    } finally {
      setUploading(false);
      setSending(false);
    }
  }

  const facts = team
    ? (room.members.length + 1) + " agents · " + room.turns_7d + " turns this week"
    : room.turns_7d + " turns this week";
  const placeholder = team
    ? room.mode === "lead"
      ? "Message the team…   " + (room.lead ? agentLabel(room.lead, byName) : "The lead") + " answers, or type @ to ask one agent"
      : "Message the team…   everyone can answer, or @ someone"
    : "Message " + displayLabel(room) + "…   / commands · @ agents";
  const attachDisabled = !currentWebSessionId || sending || uploading;
  const activity = liveTool ? activityPhrase(liveTool) : "thinking";

  /* REQ-ROOM-008: the group header stacks the member faces and names the
     working members under the lead. */
  const teamFaces = team
    ? [room.lead, ...(room.members || [])]
        .filter(Boolean)
        .map((name) => faces[name] || { name })
    : [];
  const teamAgents = 1 + (room.members ? room.members.length : 0);
  const workingMembers = team
    ? mentionNames.filter((name) => {
        const row = byName[name];
        return (
          name !== room.lead &&
          row != null &&
          row.presence != null &&
          row.presence.state === "working"
        );
      })
    : [];
  const teamSubtitle =
    "Lead: " +
    (room.lead ? agentLabel(room.lead, byName) : "none") +
    (workingMembers.length > 0
      ? " · " +
        workingLabel(workingMembers.map((name) => agentLabel(name, byName)))
      : "");

  /* Group rooms: load the agent sessions behind replies in loaded history. */
  const childIds = useMemo(() => {
    const ids = new Set<string>();
    for (const list of Object.values(events)) {
      for (const event of list) {
        if (event.kind !== "assistant/message") {
          continue;
        }
        const payload = (event.payload || {}) as Record<string, unknown>;
        if (payload.source === "agent" && typeof payload.child_session_id === "string" && payload.child_session_id) {
          ids.add(payload.child_session_id);
        }
      }
    }
    return Array.from(ids);
  }, [events]);
  useEffect(() => {
    const missing = childIds.filter((id) => !childEvents[id]);
    if (missing.length === 0) {
      return undefined;
    }
    let live = true;
    void Promise.all(
      missing.map((id) =>
        listEvents(id, 0, { view: "chat" })
          .then((page) => [id, page && Array.isArray(page.events) ? page.events : []] as const)
          .catch(() => [id, [] as SessionEvent[]] as const)
      )
    ).then((pairs) => {
      if (!live) {
        return;
      }
      setChildEvents((current) => {
        const next = { ...current };
        for (const [id, list] of pairs) {
          if (!next[id] || next[id].length < list.length) {
            next[id] = list;
          }
        }
        return next;
      });
    });
    return () => {
      live = false;
    };
  }, [childIds, childEvents]);

  /* Spec 47 team work: what each agent can do, for the handoff rows. */
  const canByName = useMemo(() => {
    const out: Record<string, string[]> = {};
    for (const row of roomRows) {
      if (row.can && row.can.length > 0) {
        out[row.name] = row.can;
      }
    }
    return out;
  }, [roomRows]);

  /* REQ-RDS-051: the newest running turn with an open discussion. */
  const openDiscussion = useMemo(() => {
    if (!team) {
      return null;
    }
    for (let index = nodes.length - 1; index >= 0; index -= 1) {
      const node = nodes[index];
      if (node && node.kind === "turn" && node.turn.discussion) {
        const log = node.turn.discussion;
        return node.turn.running && discussionStatus(log).open ? { log, sessionId: node.sessionId } : null;
      }
    }
    return null;
  }, [nodes, team]);

  async function stopRoomDiscussion(sessionId: string, mode: "stop" | "direct") {
    if (openDiscussion) {
      setStoppingId(openDiscussion.log.id);
    }
    try {
      await stopDiscussion(sessionId, mode);
    } catch (error) {
      setActionError(errorMessage(error));
    }
  }

  async function compactNow() {
    if (!currentWebSessionId) {
      setActionError("Nothing to compact yet.");
      return;
    }
    setActionError("");
    setCommandReply("Compacting this session…");
    try {
      const result = await compactSession(currentWebSessionId);
      setCommandReply(
        "Session compacted: " + result.tokens_before.toLocaleString() + " → " + result.tokens_after.toLocaleString() + " tokens."
      );
    } catch (failure) {
      setCommandReply("");
      setActionError(errorMessage(failure));
    }
  }

  function composerKeyDown(event: ReactKeyboardEvent<HTMLTextAreaElement>) {
    if (slash.onKeyDown(event)) {
      return;
    }
    if (mentions.onKeyDown(event)) {
      return;
    }
    if (event.key === "Escape" && replyTarget) {
      event.preventDefault();
      setReplyTarget(null);
      return;
    }
    /* Composer redesign: esc stops the running turn, like the Stop button. */
    if (event.key === "Escape" && canStop) {
      event.preventDefault();
      void stopTurn();
      return;
    }
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault();
      void sendMessage();
    }
  }

  /* A one-agent room can stop its running turn from the composer. */
  const canStop = !team && turnRunning && Boolean(currentWebSessionId);
  async function stopTurn() {
    if (!currentWebSessionId || cancelling) {
      return;
    }
    setCancelling(true);
    try {
      await cancelTurn(currentWebSessionId);
    } catch (failure: unknown) {
      setActionError(errorMessage(failure));
    } finally {
      setCancelling(false);
    }
  }

  /* The live row's clock starts with the running turn, even when the page
     opened mid-turn. */
  const liveSince = nodes.reduce(
    (at, node) => (node.kind === "turn" && node.turn.running ? Number(node.turn.startedAt) || at : at),
    0
  );

  /* Perf: an older segment's rows skip rendering and layout while off screen
     (`is-old`), and only a row that arrived after the initial load animates in
     (`msg-enter`). */
  function rowClass(key: string, sessionId: string): string {
    const enter = heldKeysRef.current.has(key) ? "" : "msg-enter";
    const old = sessionId !== newestId ? "is-old" : "";
    return [enter, old].filter(Boolean).join(" ");
  }

  function renderNode(node: TimelineNode) {
    switch (node.kind) {
      case "divider":
        return (
          <div
            className={"room-divider" + (node.topic ? " room-divider-light" : "")}
            key={node.key}
          >
            <span className="room-divider-line" aria-hidden="true" />
            <span className="room-divider-text">
              {node.topic
                ? "New topic"
                : "via " +
                  channelName(node.channel) +
                  (node.date ? " · " + node.date : "")}
            </span>
            <span className="room-divider-line" aria-hidden="true" />
          </div>
        );
      case "day":
        return (
          <div className="room-day" key={node.key}>
            <span className="room-day-text">{node.dayLabel}</span>
          </div>
        );
      case "summary":
        return (
          <div
            className={"room-summary" + (node.sessionId !== newestId ? " is-old" : "")}
            key={node.key}
          >
            <BookIcon />
            <span className="room-summary-text">
              {displayLabel(room) + " summarized earlier messages · full log in "}
            </span>
            <a
              className="room-summary-link"
              href={"#/sessions/" + encodeURIComponent(node.sessionId)}
            >
              History
            </a>
          </div>
        );
      case "learned":
        return (
          <LearnedLine key={node.key} text={node.learnedText} />
        );
      case "user":
        return (
          <div className={rowClass(node.key, node.sessionId)} key={node.key}>
            <UserBubble
              text={node.item.text}
              continue={node.item.continue === true}
              attachments={storedAttachments(node.sessionId, node.item.attachments)}
              replyTo={node.item.replyTo || null}
              at={node.at}
            />
          </div>
        );
      case "job":
        return (
          <div className={rowClass(node.key, node.sessionId)} key={node.key}>
            <JobNoticeLine job={node.job} />
          </div>
        );
      case "turn":
        return (
          <div className={rowClass(node.key, node.sessionId)} key={node.key}>
            {team ? (
              <RoomGroup
                turn={node.turn}
                room={room}
                faces={faces}
                sessionId={node.sessionId}
                onDirect={() => void stopRoomDiscussion(node.sessionId, "direct")}
                onMemory={() => setSettingsTab("memory")}
                can={canByName}
                childEvents={childEvents}
                liveChild={node.sessionId === newestId && node.turn.running ? liveChild : ""}
              />
            ) : (
              <TurnBlock
                turn={node.turn}
                agent={room.name}
                agentLabel={displayLabel(room)}
                agentColor={room.color}
                sessionId={node.sessionId}
                onReply={setReplyTarget}
                roomAgent
                liveActivity={node.sessionId === newestId && node.turn.running ? activity : ""}
                liveTool={node.sessionId === newestId && node.turn.running ? liveTool : ""}
                waitingForInput={Boolean(activeQuestion)}
                streamingText={
                  node.sessionId === newestId && node.turn.running ? liveText : ""
                }
                thinking={
                  node.sessionId === newestId && node.turn.running && liveThinking
                }
              />
            )}
          </div>
        );
      default:
        return null;
    }
  }

  return (
    <div className={variant === "dock" ? "room-view is-dock" : "room-view"} {...fileDrop.handlers}>
      <AttachmentDropOverlay active={fileDrop.active} />
      {variant === "dock" ? null : (
      <header className="room-head">
        <a className="ibtn ibtn-sm room-back" href="#/chats" aria-label="All chats">
          <Ic name="chevron-left" size={18} />
        </a>
        {team ? (
          <TeamAvatar
            size={44}
            items={teamFaces.map((face) => ({
              name: face.name,
              color: gr8rColor(face.color),
            }))}
          />
        ) : (
          <AgentAvatar
            name={room.name}
            color={gr8rColor(room.color)}
            size="lg"
            presence={Boolean(room.presence)}
          />
        )}
        <div className="room-head-main">
          <div className="room-head-title">
            <span className="room-head-name">{displayLabel(room)}</span>
            {team ? (
              <Badge tone="violet">{"Team · " + teamAgents + " agents"}</Badge>
            ) : room.title ? (
              <Badge>{room.title}</Badge>
            ) : null}
          </div>
          <div className="room-head-sub">
            {team ? (
              <span className="room-head-facts">{teamSubtitle}</span>
            ) : (
              <>
                <PresenceLabel presence={room.presence} />
                <span className="room-head-facts">{facts}</span>
              </>
            )}
          </div>
        </div>
        <div className="room-head-actions">
          {team ? (
            <Button size="sm" onClick={() => setSettingsTab("members")}>
              Members
            </Button>
          ) : (
            <Button
              as="a"
              size="sm"
              href={"#/agents/" + encodeURIComponent(room.name)}
            >
              View profile
            </Button>
          )}
          <span ref={roomMenuRef} className="room-head-more">
            <IconButton
              icon="ellipsis"
              size="sm"
              label="Chat actions"
              aria-haspopup="menu"
              aria-expanded={menuOpen}
              onClick={() => setMenuOpen((value) => !value)}
            />
          </span>
          {/* Spec 85 REQ-ARC-009: Clear, Archive/Unarchive (lana disabled), then
              the room settings action (teams) or View profile (agents). */}
          <Menu
            anchorRef={roomMenuRef}
            open={menuOpen}
            onClose={() => setMenuOpen(false)}
            className="room-head-menu"
          >
            {onClear ? (
              <MenuItem
                icon="eraser"
                onClick={() => {
                  setMenuOpen(false);
                  onClear();
                }}
              >
                <span className="menu-2l">
                  <span className="menu-2l-title">Clear chat…</span>
                  <span className="menu-2l-sub">Remove every message in this chat</span>
                </span>
              </MenuItem>
            ) : null}
            {onArchive ? (
              <MenuItem
                icon={archived ? "archive-restore" : "archive"}
                disabled={lana}
                onClick={() => {
                  if (lana) {
                    return;
                  }
                  setMenuOpen(false);
                  onArchive(!archived);
                }}
              >
                <span className="menu-2l">
                  <span className="menu-2l-title">
                    {archived ? "Unarchive chat" : "Archive chat"}
                  </span>
                  {lana ? (
                    <span className="menu-2l-sub">Main agent, always in Chats</span>
                  ) : null}
                </span>
              </MenuItem>
            ) : null}
            {onClear || onArchive ? <MenuSeparator /> : null}
            {team ? (
              <MenuItem
                icon="settings"
                onClick={() => {
                  setMenuOpen(false);
                  setSettingsTab("members");
                }}
              >
                Room settings
              </MenuItem>
            ) : (
              <MenuItem
                icon="user-round"
                onClick={() => {
                  setMenuOpen(false);
                  window.location.hash = "#/agents/" + encodeURIComponent(room.name);
                }}
              >
                View profile
              </MenuItem>
            )}
          </Menu>
        </div>
      </header>
      )}
      {settingsTab && team ? (
        <RoomSettings room={room} rows={roomRows} initialTab={settingsTab} onClose={() => setSettingsTab(null)} />
      ) : null}
      {openDiscussion ? (
        <DiscussionBar
          log={openDiscussion.log}
          faces={faces}
          stopping={stoppingId === openDiscussion.log.id}
          onStop={() => void stopRoomDiscussion(openDiscussion.sessionId, "stop")}
        />
      ) : null}

      {/* Spec 85 REQ-ARC-008: an archived room keeps its banner over the
          timeline with a one-tap way back into Chats. */}
      {archived ? (
        <div className="room-archived" role="status">
          <Ic name="archive" size={16} />
          <span className="room-archived-text">
            Archived — hidden from Chats. Messages here keep it archived.
          </span>
          {onArchive ? (
            <Button size="sm" variant="secondary" icon="archive-restore" onClick={() => onArchive(false)}>
              Unarchive
            </Button>
          ) : null}
        </div>
      ) : null}

      {loadError ? (
        <div className="alert danger room-notices" role="alert">
          <Ic name="triangle-alert" size={15} />
          <div>{loadError}</div>
        </div>
      ) : null}
      {streamState === "reconnecting" ? (
        <div className="alert warn room-notices" role="status">
          <Ic name="wifi-off" size={15} />
          <div>The live stream is reconnecting.</div>
        </div>
      ) : null}

      <div className="room-timeline-scroll" ref={scrollRef} onScroll={onScroll}>
        {!loading && nodes.length === 0 ? (
          clearedAt > 0 ? (
            <RoomCleared room={room} team={team} teamFaces={teamFaces} />
          ) : (
            <RoomEmpty room={room} team={team} teamFaces={teamFaces} />
          )
        ) : (
          <div className="room-timeline">
            {loading && segments.length === 0 ? <SkeletonHint /> : null}
            {nodes.map(renderNode)}
            {loadingOlder ? (
              <p className="room-older muted">Loading earlier messages…</p>
            ) : null}
            {/* An agent room shows its working state in the turn's foot; this
                row is for group rooms, and for an agent room only until the
                running turn appears. */}
            {turnRunning && !(team && liveChild) && (team || !liveSince) ? (
              <div className={"room-live" + (team ? "" : " room-live-agent")} role="status">
                {team ? null : (
                  <AgentAvatar
                    name={room.name}
                    color={gr8rColor(room.color)}
                    size="md"
                    aria-hidden="true"
                    mood={turnMood(
                      { running: true },
                      {
                        waitingForInput: Boolean(activeQuestion),
                        thinking: liveThinking,
                        streaming: Boolean(liveText),
                        tool: Boolean(liveTool),
                      }
                    )}
                  />
                )}
                <TypingDots />
                <span className="room-live-text">
                  {(liveSpeaker ? agentLabel(liveSpeaker, byName) : displayLabel(room)) + " is " + activity + "…"}
                </span>
              </div>
            ) : null}
          </div>
        )}
      </div>

      <div className="room-composer">
        {commandReply ? (
          <div className="alert info" role="status">
            <Ic name="workflow" size={15} />
            <div>{commandReply}</div>
          </div>
        ) : null}
        {actionError ? (
          <div className="alert danger" role="alert">
            <Ic name="triangle-alert" size={15} />
            <div>
              {actionError}
              <ErrorDetails details={actionDetails} />
            </div>
          </div>
        ) : null}
        <div className="room-slot">
          {activeApproval ? (
            <ApprovalCard
              key={activeApproval.request_id}
              approval={activeApproval}
              busy={Boolean(activeApproval.deciding)}
              onAnswer={handleApprovalAnswer}
              onExpire={handleApprovalExpire}
              onDismiss={handleApprovalDismiss}
            />
          ) : null}
          {activeQuestion ? (
            <QuestionCard
              key={"question-" + activeQuestion.request_id}
              question={activeQuestion}
              responder="web"
              readOnly={false}
              busy={questionBusy}
              onSubmit={handleQuestionSubmit}
              onSkipAll={handleQuestionSkipAll}
              onExpire={handleQuestionExpire}
            />
          ) : null}
          {secrets.request ? (
            <SecretRequestCard
              key={"secret-" + secrets.request.request_id}
              request={secrets.request}
              agentLabel={displayLabel(room)}
              onSave={secrets.save}
              onCancel={secrets.cancel}
              onExpire={secrets.expire}
              onDismiss={secrets.dismiss}
            />
          ) : null}
        </div>

        {/* Composer redesign (FigCraft "Composer redesign"): the tray tab and the
            input card each sit in a thick padded frame, the tab docked on the
            card. */}
        <div className={"cx" + (turnRunning ? " is-live" : "") + (variant === "dock" ? " is-dock" : "")}>
        {/* Composer redesign (owner 2026-11-25): one framed shell holds the
            background row on top, the input field with send inside it, and
            the toolbar under the field. */}
        {/* The Background tab docks on the shell's top edge: one stepped
            outline (owner 2026-11-25). */}
        {!team ? (
          <ComposerTray
            sessionId={currentWebSessionId || ""}
            refreshKey={jobRefresh}
            open={tasksOpen}
            onOpenChange={setTasksOpen}
          />
        ) : null}
        <div className="cx-shell">
        <div className="cbox cx-field">
          {replyTarget ? (
            <ReplyChip target={replyTarget} onCancel={() => setReplyTarget(null)} />
          ) : null}
          <AttachmentChips
            items={attachmentDraft.items}
            onRemove={attachmentDraft.remove}
          />
          {attachmentDraft.notice ? (
            <p className="room-composer-status" role="status">
              {attachmentDraft.notice}
            </p>
          ) : null}
          {uploading ? (
            <p className="room-composer-status" role="status">
              Uploading…
            </p>
          ) : null}
          <SlashMenu state={slash} />
          {newTopic ? (
            <NewSessionBanner name={team ? "The team" : displayLabel(room)} onCancel={() => setNewTopic(false)} />
          ) : null}
          {mentions.open ? (
            <MentionList
              names={mentions.matches}
              active={mentions.index}
              onPick={mentions.insert}
            />
          ) : null}
          <div className="cx-field-row">
            <textarea
              ref={inputRef}
              rows={variant === "dock" ? 1 : 2}
              aria-label={"Message " + (team ? "the team" : displayLabel(room))}
              placeholder={placeholder}
              value={draft}
              disabled={sending}
              onChange={(event) => {
                setDraft(event.target.value);
                mentions.reset();
                slash.reset();
              }}
              onKeyDown={composerKeyDown}
              onPaste={(event) => pasteImageFiles(event, attachmentDraft.addFiles)}
            ></textarea>
            <span className="cx-send">
              {canStop && !draft.trim() && attachmentDraft.items.length === 0 ? (
                <button
                  type="button"
                  className="cx-send-btn is-stop"
                  aria-label="Stop the running turn"
                  title="Stop · Esc"
                  disabled={cancelling}
                  onClick={() => void stopTurn()}
                >
                  <i />
                </button>
              ) : (
                <button
                  type="button"
                  className="cx-send-btn"
                  aria-label="Send message"
                  title="Send message"
                  disabled={sending || (!draft.trim() && attachmentDraft.items.length === 0)}
                  onClick={() => void sendMessage()}
                >
                  <Ic name="arrow-up" size={16} />
                </button>
              )}
            </span>
          </div>
          {team && mentionNames.length > 0 ? (
            <MentionChips
              names={mentionNames}
              faces={faces}
              onPick={mentions.insert}
            />
          ) : null}
        </div>
          <div className="room-composer-bar cx-bar">
            <span
              className="room-attach"
              title={
                currentWebSessionId
                  ? undefined
                  : "Send a message first to attach files."
              }
            >
              <AttachButton
                disabled={attachDisabled}
                onFiles={attachmentDraft.addFiles}
              />
            </span>
            {team ? (
              <Button
                size="sm"
                variant="ghost"
                icon="messages-square"
                className={"room-discuss" + (discuss ? " on" : "")}
                aria-pressed={discuss}
                title="Ask the lead to open a discussion"
                disabled={sending}
                onClick={() => setDiscuss((value) => !value)}
              >
                Discuss
              </Button>
            ) : null}
            <span className="room-composer-spacer"></span>
            <ContextMeter
              sessionId={currentWebSessionId || ""}
              refreshKey={(turnRunning ? "run-" : "idle-") + (liveTurnId || "")}
              busy={turnRunning || sending}
            />
          </div>
        </div>
        {variant !== "dock" ? (
          <p className="cx-hint">
            {canStop
              ? "esc stops the turn · Enter queues your message"
              : "Enter to send · Shift+Enter for a new line"}
          </p>
        ) : null}
        </div>
      </div>
    </div>
  );
}

/* REQ-ROOM-002: a fresh room with no segments yet shows the room face and an
   invitation instead of a blank timeline. */
function RoomEmpty({
  room,
  team,
  teamFaces,
}: {
  room: RoomRow;
  team: boolean;
  teamFaces: FaceRef[];
}) {
  const leadFace = teamFaces.find((face) => face.name === room.lead);
  const leadLabel = room.lead
    ? leadFace && leadFace.label
      ? leadFace.label
      : room.lead
    : "the lead";
  return (
    <div className="room-empty">
      {team && teamFaces.length > 0 ? (
        <TeamAvatar
          size={64}
          items={teamFaces.map((face) => ({
            name: face.name,
            color: gr8rColor(face.color),
          }))}
        />
      ) : (
        <AgentAvatar
          name={room.name}
          color={gr8rColor(room.color)}
          size="xl"
          presence={Boolean(room.presence)}
        />
      )}
      <h2 className="room-empty-title">
        {team ? displayLabel(room) + " is ready" : "Start a conversation with " + displayLabel(room)}
      </h2>
      {team ? (
        <>
          <p className="room-empty-text muted">
            {"Ask anything here. " +
              leadLabel +
              " answers, brings in a member when needed, and opens a discussion when a question needs more than one view."}
          </p>
          <div className="room-empty-members">
            {teamFaces.map((face) => (
              <span key={face.name} className="room-empty-member">
                <AgentAvatar name={face.name} color={gr8rColor(face.color)} size="sm" />
                <span>{face.label || face.name}</span>
                {face.name === room.lead ? <span className="muted">lead</span> : null}
              </span>
            ))}
          </div>
          {room.title ? <p className="room-empty-text muted">{room.title}</p> : null}
        </>
      ) : room.title ? (
        <p className="room-empty-text muted">{room.title}</p>
      ) : null}
    </div>
  );
}

/* Spec 85 REQ-ARC-011: an empty timeline of a cleared room shows the clear
   divider, the room face, and a way into the older sessions in History. */
function RoomCleared({
  room,
  team,
  teamFaces,
}: {
  room: RoomRow;
  team: boolean;
  teamFaces: FaceRef[];
}) {
  return (
    <div className="room-empty room-cleared">
      <div className="room-cleared-divider">
        <span className="room-divider-line" aria-hidden="true" />
        <span className="room-cleared-text">
          {"Chat cleared · " + relativeShort(Number(room.cleared_at) || 0)}
        </span>
        <span className="room-divider-line" aria-hidden="true" />
      </div>
      {team && teamFaces.length > 0 ? (
        <TeamAvatar
          size={64}
          items={teamFaces.map((face) => ({
            name: face.name,
            color: gr8rColor(face.color),
          }))}
        />
      ) : (
        <AgentAvatar
          name={room.name}
          color={gr8rColor(room.color)}
          size="xl"
          presence={Boolean(room.presence)}
        />
      )}
      <h2 className="room-empty-title">{"Fresh start with " + displayLabel(room)}</h2>
      <p className="room-empty-text muted">Earlier messages are in History.</p>
      <Button as="a" href="#/history" variant="secondary" icon="history">
        Open in History
      </Button>
    </div>
  );
}

/* The loading placeholders of REQ-ROOM-013, matching the room list. */
function SkeletonHint() {
  return (
    <div className="room-skeleton" aria-hidden="true">
      <Skeleton width="100%" height={32} />
      <Skeleton width="100%" height={56} />
      <Skeleton width="100%" height={32} />
    </div>
  );
}
