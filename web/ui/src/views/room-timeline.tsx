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
import type { ApprovalEvent } from "../api/approvals";
import { uploadAttachments } from "../api/attachments";
import { ApiError, errorMessage, openStream } from "../api/client";
import type { StreamState } from "../api/client";
import { answerQuestion, skipAllQuestions } from "../api/questions";
import type { QuestionAnswerRequest, QuestionEvent } from "../api/questions";
import { markRoomRead, roomSegments, sendToRoom } from "../api/rooms";
import type { RoomRow, Segment, SendRoomRequest } from "../api/rooms";
import { listEvents } from "../api/sessions";
import type { SessionEvent } from "../api/sessions";
import { knowledgeCompiledLine, memoryOpLine } from "../lib/format";
import { ErrorNotice, Icon } from "../ui/ui";
import { ApprovalCard } from "./approval";
import type { ApprovalItem } from "./approval";
import {
  AttachButton,
  AttachmentChips,
  pasteImageFiles,
  useAttachmentDraft,
} from "./attachments";
import { AgentFace, PresenceLabel, TypingDots } from "./presence";
import { openQuestionFromEvents, QuestionCard } from "./question";
import type { OpenQuestion } from "./question";
import { storedAttachments } from "./session";
import { buildTurns, TurnBlock, UserBubble } from "./turn";
import type { ChatTurn, ThreadItem } from "./turn";

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

function stringOf(value: unknown): string {
  return typeof value === "string" ? value : "";
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
  | { key: string; at: number; kind: "divider"; channel: string; date: string }
  | { key: string; at: number; kind: "day"; dayLabel: string }
  | { key: string; at: number; kind: "summary"; sessionId: string }
  | { key: string; at: number; kind: "learned"; learnedText: string }
  | { key: string; at: number; kind: "user"; sessionId: string; item: UserThreadItem }
  | { key: string; at: number; kind: "turn"; sessionId: string; turn: ChatTurn };

export interface RoomViewProps {
  room: RoomRow;
}

export function RoomView({ room }: RoomViewProps) {
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
  const [streamState, setStreamState] = useState<StreamState>("connected");
  const [approvals, setApprovals] = useState<ApprovalItem[]>([]);
  const [question, setQuestion] = useState<OpenQuestion | null>(null);
  const [questionBusy, setQuestionBusy] = useState(false);
  const [closedQuestionIds, setClosedQuestionIds] = useState<string[]>([]);
  const [draft, setDraft] = useState("");
  const [newTopic, setNewTopic] = useState(false);
  const [sending, setSending] = useState(false);
  const [uploading, setUploading] = useState(false);

  const scrollRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);
  const prependRef = useRef<number | null>(null);
  const startedRef = useRef(false);
  const handledSessionRef = useRef("");
  const attachmentDraft = useAttachmentDraft();

  const loadEvents = useCallback(async (ids: string[]) => {
    if (ids.length === 0) {
      return;
    }
    const loaded: Record<string, SessionEvent[]> = {};
    await Promise.all(
      ids.map(async (id) => {
        try {
          const page = await listEvents(id, 0);
          loaded[id] = page.events || [];
        } catch {
          loaded[id] = [];
        }
      })
    );
    setEvents((current) => ({ ...current, ...loaded }));
  }, []);

  /* REQ-ROOM-002: the newest page of segments loads first. */
  useEffect(() => {
    let alive = true;
    roomSegments(room.name, { limit: LIMIT })
      .then((page) => {
        if (!alive) {
          return undefined;
        }
        const list = page.segments || [];
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
  }, [room.name, loadEvents]);

  const lastSegment =
    segments.length > 0 ? segments[segments.length - 1] : null;
  const newestId = lastSegment ? lastSegment.session_id : "";

  /* REQ-ROOM-004: a Telegram turn started elsewhere shows up in the timeline. */
  const lastSession = room.last_message ? room.last_message.session_id : "";
  useEffect(() => {
    if (!lastSession || handledSessionRef.current === lastSession) {
      return;
    }
    handledSessionRef.current = lastSession;
    if (segments.some((segment) => segment.session_id === lastSession)) {
      return;
    }
    let alive = true;
    roomSegments(room.name, { limit: LIMIT })
      .then((page) => {
        if (!alive) {
          return undefined;
        }
        const list = page.segments || [];
        setSegments((current) => {
          const byId = new Map(current.map((segment) => [segment.session_id, segment]));
          for (const segment of list) {
            byId.set(segment.session_id, segment);
          }
          return Array.from(byId.values()).sort(
            (left, right) => left.created_at - right.created_at
          );
        });
        return loadEvents(list.map((segment) => segment.session_id));
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [lastSession, segments, room.name, loadEvents]);

  const finishLive = useCallback(() => {
    startedRef.current = false;
    setTurnRunning(false);
    setLiveText("");
    setLiveThinking(false);
    setLiveTool("");
    setLiveTurnId("");
    setLiveStartedAt(0);
  }, []);

  /* The live tail streams the newest segment exactly like the session view. */
  useEffect(() => {
    if (!newestId) {
      return undefined;
    }
    const id = newestId;
    const stream = openStream(id, {
      fromSeq: 0,
      onState: (state) => setStreamState(state),
      onEvent: (event) => {
        setEvents((current) => {
          const list = current[id] || [];
          if (list.some((entry) => entry.seq === event.seq)) {
            return current;
          }
          return {
            ...current,
            [id]: list.concat([event]).sort((left, right) => left.seq - right.seq),
          };
        });
        const payload =
          event.payload && typeof event.payload === "object"
            ? (event.payload as Record<string, unknown>)
            : {};
        if (event.kind === "assistant/message" || event.kind === "tool/call") {
          setLiveText("");
          setLiveThinking(false);
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
      onShutdown: () => {
        setStreamState("reconnecting");
      },
    });
    return () => {
      stream.close();
    };
  }, [newestId, finishLive]);

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
    const list: TimelineNode[] = [];
    for (let index = 0; index < segments.length; index += 1) {
      const segment = segments[index];
      if (!segment) {
        continue;
      }
      const segEvents = events[segment.session_id] || [];
      list.push({
        key: "div-" + segment.session_id,
        at: segment.created_at,
        kind: "divider",
        channel: segment.channel,
        date: shortDate(segment.created_at),
      });
      const isNewest = segment.session_id === newestId;
      const threadEvents = segEvents.filter(
        (event) =>
          event.kind !== "knowledge/compiled" && event.kind !== "memory/captured"
      );
      const thread = buildTurns(
        threadEvents,
        isNewest
          ? { running: turnRunning, turnId: liveTurnId, startedAt: liveStartedAt }
          : {}
      );
      const entries: TimelineNode[] = [];
      for (const item of thread) {
        if (item.kind === "user") {
          const seq = Number(String(item.key).replace("user-", ""));
          const event = segEvents.find((entry) => entry.seq === seq);
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
      for (const entry of entries) {
        list.push(entry);
      }
    }

    const withDays: TimelineNode[] = [];
    let lastDay = "";
    for (const node of list) {
      if (node.kind === "divider") {
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
    return withDays;
  }, [segments, events, newestId, turnRunning, liveTurnId, liveStartedAt]);

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

  /* AC-ROOM-003: scrolling to the top loads the previous page of segments and
     holds the visible position. */
  async function loadOlder() {
    if (loadingOlder || !hasMore || segments.length === 0) {
      return;
    }
    setLoadingOlder(true);
    const node = scrollRef.current;
    prependRef.current = node ? node.scrollHeight : 0;
    const oldest = segments[0];
    try {
      const page = await roomSegments(room.name, {
        before: oldest ? oldest.created_at : 0,
        limit: LIMIT,
      });
      const older = page.segments || [];
      const known = new Set(segments.map((segment) => segment.session_id));
      const fresh = older.filter((segment) => !known.has(segment.session_id));
      setSegments((current) =>
        [...fresh, ...current].sort((left, right) => left.created_at - right.created_at)
      );
      setHasMore(Boolean(page.has_more));
      await loadEvents(fresh.map((segment) => segment.session_id));
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
    async (approval: ApprovalEvent, outcome: string) => {
      setApprovals((current) =>
        current.map((item) =>
          item.request_id === approval.request_id
            ? { ...item, deciding: true, error: "" }
            : item
        )
      );
      try {
        await answerApproval(approval.request_id, outcome);
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

  /* REQ-ROOM-003: send to the room's current web session; "New topic" asks the
     server to create a new one first (AC-ROOM-004, AC-ROOM-005). */
  async function sendMessage() {
    const text = draft.trim();
    const files = attachmentDraft.items.slice();
    if ((!text && files.length === 0) || sending) {
      return;
    }
    setSending(true);
    setActionError("");
    setActionDetails([]);
    setDraft("");
    const topic = newTopic;
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
      const result = await sendToRoom(room.name, body);
      setNewTopic(false);
      attachmentDraft.clear();
      const isNew =
        Boolean(result.session_id) &&
        !segments.some((segment) => segment.session_id === result.session_id);
      if (isNew) {
        const now = Date.now();
        const segment: Segment = {
          session_id: result.session_id,
          channel: "web",
          address: "owner",
          created_at: now,
          updated_at: now,
          turn_count: 0,
          status: "running",
        };
        setSegments((current) =>
          [...current, segment].sort((left, right) => left.created_at - right.created_at)
        );
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

  const team = room.kind === "team";
  const facts = team
    ? (room.members.length + 1) + " agents · " + room.turns_7d + " turns this week"
    : room.turns_7d + " turns this week";
  const placeholder = team
    ? "Message the team…   " +
      (room.lead || "The lead") +
      " answers, or type @ to ask one agent"
    : "Message " + room.name + "…   Type @ to bring in another agent";
  const attachDisabled = !currentWebSessionId || sending || uploading;
  const activity = liveTool ? activityPhrase(liveTool) : "thinking";

  function composerKeyDown(event: ReactKeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault();
      void sendMessage();
    }
  }

  function renderNode(node: TimelineNode) {
    switch (node.kind) {
      case "divider":
        return (
          <div className="room-divider" key={node.key}>
            <span className="room-divider-line" aria-hidden="true" />
            <span className="room-divider-text">
              {"via " + channelName(node.channel) + (node.date ? " · " + node.date : "")}
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
          <div className="room-summary" key={node.key}>
            <BookIcon />
            <span className="room-summary-text">
              {room.name + " summarized earlier messages · full log in "}
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
          <div className="room-learned" key={node.key}>
            <BookIcon />
            <span className="room-learned-text">{"Learned: " + node.learnedText}</span>
          </div>
        );
      case "user":
        return (
          <div className="msg-enter" key={node.key}>
            <UserBubble
              text={node.item.text}
              continue={node.item.continue === true}
              attachments={storedAttachments(node.sessionId, node.item.attachments)}
            />
          </div>
        );
      case "turn":
        return (
          <div className="msg-enter" key={node.key}>
            <TurnBlock
              turn={node.turn}
              agent={room.name}
              waitingForInput={Boolean(activeQuestion)}
              streamingText={
                node.sessionId === newestId && node.turn.running ? liveText : ""
              }
              thinking={
                node.sessionId === newestId && node.turn.running && liveThinking
              }
            />
          </div>
        );
      default:
        return null;
    }
  }

  return (
    <div className="room-view">
      <header className="room-head">
        <AgentFace
          name={room.name}
          color={room.color}
          presence={room.presence}
          size={44}
        />
        <div className="room-head-main">
          <div className="room-head-title">
            <span className="room-head-name">{room.name}</span>
            {room.title ? (
              <span className="badge-ui room-head-badge">{room.title}</span>
            ) : null}
          </div>
          <div className="room-head-sub">
            <PresenceLabel presence={room.presence} />
            <span className="room-head-facts">{facts}</span>
          </div>
        </div>
        <div className="room-head-actions">
          <a
            className="button-ui button-ui-secondary"
            href={"#/agents/" + encodeURIComponent(room.name)}
          >
            View profile
          </a>
        </div>
      </header>

      {loadError ? (
        <div className="room-notices">
          <ErrorNotice message={loadError} />
        </div>
      ) : null}
      {streamState === "reconnecting" ? (
        <p className="notice notice-warn room-notices">
          The live stream is reconnecting.
        </p>
      ) : null}

      <div className="room-timeline-scroll" ref={scrollRef} onScroll={onScroll}>
        <div className="room-timeline">
          {loading && segments.length === 0 ? <SkeletonHint /> : null}
          {nodes.map(renderNode)}
          {loadingOlder ? (
            <p className="room-older muted">Loading earlier messages…</p>
          ) : null}
          {turnRunning ? (
            <div className="room-live" role="status">
              <TypingDots />
              <span className="room-live-text">
                {room.name + " is " + activity + "…"}
              </span>
            </div>
          ) : null}
        </div>
      </div>

      <div className="room-composer">
        {actionError ? (
          <ErrorNotice message={actionError} details={actionDetails} />
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
        </div>
        {newTopic ? <div className="room-newtopic">New topic</div> : null}
        <div className="room-composer-box">
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
          <textarea
            className="room-composer-input"
            rows={2}
            aria-label={"Message " + (team ? "the team" : room.name)}
            placeholder={placeholder}
            value={draft}
            disabled={sending}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={composerKeyDown}
            onPaste={(event) => pasteImageFiles(event, attachmentDraft.addFiles)}
          ></textarea>
          <div className="room-composer-bar">
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
            <button
              type="button"
              className="button-ui button-ui-tertiary room-newtopic-btn"
              disabled={sending}
              onClick={() => setNewTopic((value) => !value)}
            >
              New topic
            </button>
            <span className="room-composer-spacer"></span>
            <button
              type="button"
              className="room-send"
              aria-label="Send message"
              title="Send message"
              disabled={sending || (!draft.trim() && attachmentDraft.items.length === 0)}
              onClick={() => void sendMessage()}
            >
              <Icon name="arrow-up" className="icon-20" />
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

/* The loading placeholders of REQ-ROOM-013, matching the room list. */
function SkeletonHint() {
  return (
    <div className="room-skeleton">
      <span className="skeleton skeleton-row" style={{ height: 32 }} aria-hidden="true" />
      <span className="skeleton skeleton-row" style={{ height: 56 }} aria-hidden="true" />
      <span className="skeleton skeleton-row" style={{ height: 32 }} aria-hidden="true" />
    </div>
  );
}
