import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { answerApproval as answerApprovalRequest } from "../api/approvals";
import type { ApprovalEvent } from "../api/approvals";
import { ApiError, errorMessage, openStream } from "../api/client";
import type { StreamFrame, StreamState } from "../api/client";
import {
  answerQuestion as answerQuestionRequest,
  skipAllQuestions as skipAllQuestionsRequest,
} from "../api/questions";
import type { QuestionAnswerRequest, QuestionEvent } from "../api/questions";
import { getSession, listEvents } from "../api/sessions";
import type { SessionEvent } from "../api/sessions";
import { cancelTurn as cancelTurnRequest, continueSession } from "../api/turns";
import type { ApprovalItem } from "./approval";
import { revokeAttachmentURLs } from "./attachments";
import type { DraftAttachment } from "./attachments";
import { openQuestionFromEvents } from "./question";
import type { OpenQuestion } from "./question";
import type { TeamChildRequest } from "./team";
import { buildTurns } from "./turn";
import type { ThreadItem } from "./turn";

/* useLiveSession is the live chat state of one agent session, for views that
   embed a session's timeline (spec 51: the workflow guardian chat). It loads
   the event log, follows the session stream (API-WEB-008) and keeps what the
   main chat keeps: the turn thread, the streaming text of the running turn,
   the open approval and ask_user question, and Stop. The view renders the
   thread with the main chat components (TurnBlock, UserBubble, ApprovalCard,
   QuestionCard). An empty id is "no session yet": nothing loads. */

export interface LiveSessionOptions {
  /* Called once per ended turn (turn/end event or turn frame). */
  onTurnEnd?: (turnId: string, reason: string) => void;
}

/* The owner's message that was sent but is not in the log yet. */
export interface PendingMessage {
  text: string;
  files: DraftAttachment[];
}

export interface LiveSession {
  loading: boolean;
  loadError: string;
  events: SessionEvent[];
  thread: ThreadItem[];
  /* Root events by turn id, for TurnBlock's rootEvents. */
  turnEvents: Map<string, SessionEvent[]>;
  childEvents: Record<string, SessionEvent[]>;
  loadChildren: (requests: TeamChildRequest[]) => void;
  streamState: StreamState | "connecting";
  running: boolean;
  liveTurnId: string;
  streamText: string;
  thinking: boolean;
  queued: boolean;
  pending: PendingMessage | null;
  /* Shows the owner's message before the log has it. */
  setPending: (pending: PendingMessage | null) => void;
  /* Marks the turn a send started (or queued). */
  started: (result: { turn: string; turn_id?: string }) => void;
  cancelling: boolean;
  cancel: () => Promise<void>;
  /* REQ-TFB-005: Continue resumes an abnormally ended last turn. */
  continueTurn: () => Promise<void>;
  continueBusy: boolean;
  continueError: string;
  actionError: string;
  clearError: () => void;
  approval: ApprovalItem | null;
  answerApproval: (approval: ApprovalEvent, outcome: string) => Promise<void>;
  expireApproval: (approval: ApprovalEvent) => void;
  dismissApproval: () => void;
  question: QuestionEvent | null;
  questionBusy: boolean;
  submitQuestion: (request: QuestionEvent, body: QuestionAnswerRequest) => Promise<unknown>;
  skipQuestion: (request: QuestionEvent) => Promise<unknown>;
  expireQuestion: (request: QuestionEvent) => void;
}

function stringOf(value: unknown): string {
  return typeof value === "string" ? value : value == null ? "" : String(value);
}

function bySeq(left: SessionEvent, right: SessionEvent): number {
  return (Number(left.seq) || 0) - (Number(right.seq) || 0);
}

export function useLiveSession(id: string, options: LiveSessionOptions = {}): LiveSession {
  const [loading, setLoading] = useState(Boolean(id));
  const [loadError, setLoadError] = useState("");
  const [events, setEvents] = useState<SessionEvent[]>([]);
  const [childEvents, setChildEvents] = useState<Record<string, SessionEvent[]>>({});
  const [streamReady, setStreamReady] = useState(false);
  const [streamState, setStreamState] = useState<StreamState | "connecting">("connecting");
  const [running, setRunning] = useState(false);
  const [liveTurnId, setLiveTurnId] = useState("");
  const [liveStartedAt, setLiveStartedAt] = useState(0);
  const [streamText, setStreamText] = useState("");
  const [thinking, setThinking] = useState(false);
  const [queued, setQueued] = useState(false);
  const [pending, setPendingState] = useState<PendingMessage | null>(null);
  const [cancelling, setCancelling] = useState(false);
  const [actionError, setActionError] = useState("");
  const [approvals, setApprovals] = useState<ApprovalItem[]>([]);
  const [liveQuestion, setLiveQuestion] = useState<OpenQuestion | null>(null);
  const [closedQuestionIds, setClosedQuestionIds] = useState<string[]>([]);
  const [questionBusy, setQuestionBusy] = useState(false);
  const [continueBusy, setContinueBusy] = useState(false);
  const [continueError, setContinueError] = useState("");

  const lastSeqRef = useRef(0);
  const runningRef = useRef(false);
  const endedRef = useRef(new Set<string>());
  const pendingRef = useRef<PendingMessage | null>(null);
  /* The number of owner messages in the thread when the pending one was set;
     one more means the log has it. */
  const pendingBaseRef = useRef(0);
  const userCountRef = useRef(0);
  const sessionRef = useRef(id);
  const onTurnEndRef = useRef(options.onTurnEnd);
  useEffect(() => {
    onTurnEndRef.current = options.onTurnEnd;
  }, [options.onTurnEnd]);
  useEffect(() => {
    sessionRef.current = id;
  }, [id]);

  const setPending = useCallback((next: PendingMessage | null) => {
    pendingRef.current = next;
    pendingBaseRef.current = userCountRef.current;
    setPendingState(next);
  }, []);

  /* The stored owner message now shows its own files, so the pending object
     URLs are freed. */
  const settlePending = useCallback(() => {
    const current = pendingRef.current;
    if (current) {
      revokeAttachmentURLs(current.files);
    }
    pendingRef.current = null;
    setPendingState(null);
  }, []);

  const closeQuestion = useCallback((requestId: string) => {
    if (!requestId) {
      return;
    }
    setLiveQuestion((current) => (current && current.request_id === requestId ? null : current));
    setClosedQuestionIds((current) =>
      current.includes(requestId) ? current : current.concat([requestId])
    );
  }, []);

  const markDecided = useCallback((requestId: string, outcome: string) => {
    if (!requestId) {
      return;
    }
    setApprovals((current) =>
      current.map((item) =>
        item.request_id === requestId ? { ...item, decided: outcome, deciding: false, error: "" } : item
      )
    );
  }, []);

  const finishTurn = useCallback(
    (turnId: string, reason: string) => {
      if (turnId && endedRef.current.has(turnId)) {
        return;
      }
      if (turnId) {
        endedRef.current.add(turnId);
      }
      runningRef.current = false;
      setRunning(false);
      setCancelling(false);
      setStreamText("");
      setThinking(false);
      setQueued(false);
      setLiveTurnId("");
      setLiveStartedAt(0);
      setApprovals((current) =>
        current.map((item) =>
          item.decided
            ? item
            : { ...item, decided: reason === "cancelled" ? "cancelled" : "unavailable", deciding: false }
        )
      );
      if (onTurnEndRef.current) {
        onTurnEndRef.current(turnId, reason);
      }
    },
    []
  );

  /* Load the log and the running state whenever the session changes. */
  useEffect(() => {
    let alive = true;
    setEvents([]);
    setChildEvents({});
    setStreamReady(false);
    setStreamState("connecting");
    setRunning(false);
    setLiveTurnId("");
    setLiveStartedAt(0);
    setStreamText("");
    setThinking(false);
    setQueued(false);
    setCancelling(false);
    setActionError("");
    setContinueError("");
    setApprovals([]);
    setLiveQuestion(null);
    setClosedQuestionIds([]);
    setLoadError("");
    runningRef.current = false;
    endedRef.current.clear();
    lastSeqRef.current = 0;
    if (!id) {
      setLoading(false);
      return undefined;
    }
    setLoading(true);
    Promise.all([getSession(id), listEvents(id, 0)])
      .then(([session, page]) => {
        if (!alive) {
          return;
        }
        setEvents((page.events || []).slice().sort(bySeq));
        lastSeqRef.current = page.last_seq || 0;
        runningRef.current = Boolean(session.turn_running);
        setRunning(Boolean(session.turn_running));
        setLoading(false);
        setStreamReady(true);
      })
      .catch((failure: unknown) => {
        if (!alive) {
          return;
        }
        setLoadError(errorMessage(failure));
        setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [id]);

  useEffect(() => {
    if (!id || !streamReady) {
      return undefined;
    }
    const stream = openStream(id, {
      fromSeq: lastSeqRef.current,
      onState: (value) => setStreamState(value),
      onEvent: (event) => {
        const childId = stringOf(event.child_session_id);
        if (childId) {
          setChildEvents((current) => {
            const list = current[childId] || [];
            if (list.some((entry) => entry.seq === event.seq)) {
              return current;
            }
            return { ...current, [childId]: list.concat([event]).sort(bySeq) };
          });
          return;
        }
        setEvents((current) =>
          current.some((entry) => entry.seq === event.seq)
            ? current
            : current.concat([event]).sort(bySeq)
        );
        lastSeqRef.current = Math.max(lastSeqRef.current, Number(event.seq) || 0);
        const payload =
          event.payload && typeof event.payload === "object" ? event.payload : {};
        if (event.kind === "user/message") {
          setQueued(false);
        }
        if (event.kind === "assistant/message" || event.kind === "tool/call") {
          setStreamText("");
          setThinking(false);
        }
        if (event.kind === "approval/decided") {
          markDecided(stringOf(payload.request_id), stringOf(payload.outcome) || "unavailable");
        }
        if (event.kind === "question/answered") {
          closeQuestion(stringOf(payload.request_id));
        }
        if (event.kind === "turn/end") {
          finishTurn(stringOf(payload.turn_id) || stringOf(event.turn_id), stringOf(payload.reason));
        }
      },
      onFrame: (frame: StreamFrame) => {
        if (stringOf(frame.child_session_id)) {
          return;
        }
        const turnId = stringOf(frame.turn_id);
        if (turnId) {
          setLiveTurnId(turnId);
          if (!runningRef.current) {
            runningRef.current = true;
            setRunning(true);
            setLiveStartedAt(Date.now());
          }
        }
        if (frame.kind === "text_delta" && frame.text) {
          setThinking(false);
          setStreamText((current) => current + String(frame.text));
        }
        if (frame.kind === "thinking_delta") {
          setThinking(true);
        }
        if (frame.kind === "tool_call" || frame.kind === "tool_result" || frame.kind === "step_retry") {
          setThinking(false);
          setStreamText("");
        }
      },
      onStatus: (status: StreamFrame) => {
        const next = Boolean(status.turn_running);
        runningRef.current = next;
        setRunning(next);
      },
      onTurn: (turn: StreamFrame) => {
        finishTurn(stringOf(turn.turn_id), stringOf(turn.stop_reason));
      },
      onApproval: (approval: ApprovalEvent) => {
        if (!approval.request_id) {
          return;
        }
        setApprovals((current) =>
          current.some((item) => item.request_id === approval.request_id)
            ? current
            : current.concat([{ ...approval, decided: "", deciding: false, error: "" }])
        );
        runningRef.current = true;
        setRunning(true);
      },
      onApprovalClosed: (payload: StreamFrame) => {
        markDecided(stringOf(payload.request_id), "approval_closed");
      },
      onQuestion: (incoming: QuestionEvent) => {
        if (!incoming.request_id) {
          return;
        }
        setLiveQuestion((current) =>
          current && current.request_id === incoming.request_id
            ? current
            : {
                request_id: incoming.request_id,
                call_id: stringOf(incoming.call_id),
                expires_at: Number(incoming.expires_at) || 0,
                turn_id: stringOf(incoming.turn_id),
                questions: Array.isArray(incoming.questions) ? incoming.questions : [],
              }
        );
      },
      onShutdown: () => setStreamState("reconnecting"),
      onError: () => setStreamState("reconnecting"),
    });
    return () => {
      stream.close();
    };
  }, [id, streamReady, finishTurn, markDecided, closeQuestion]);

  const thread = useMemo(
    () => buildTurns(events, { running, turnId: liveTurnId, startedAt: liveStartedAt }),
    [events, running, liveTurnId, liveStartedAt]
  );

  /* The pending bubble goes once the log holds one more owner message than
     when it was sent: from the stream, or from the load of a session the
     send just created. */
  const userCount = useMemo(
    () => thread.filter((item) => item.kind === "user").length,
    [thread]
  );
  useEffect(() => {
    if (id && !loading) {
      userCountRef.current = userCount;
    }
    if (pending && id && !loading && userCount > pendingBaseRef.current) {
      settlePending();
    }
  }, [id, loading, pending, userCount, settlePending]);

  const turnEvents = useMemo(() => {
    const map = new Map<string, SessionEvent[]>();
    for (const event of events) {
      const turnId = event.turn_id || (event.payload && stringOf(event.payload.turn_id)) || "";
      if (!turnId) {
        continue;
      }
      const list = map.get(turnId) || [];
      list.push(event);
      map.set(turnId, list);
    }
    return map;
  }, [events]);

  /* Older delegated turns load their child logs when the owner opens them. */
  const loadChildren = useCallback((requests: TeamChildRequest[]) => {
    const loadId = sessionRef.current;
    void Promise.all(
      requests
        .filter((request) => request && request.id)
        .map((request) =>
          listEvents(request.id, 0)
            .then((page) => ({
              id: request.id,
              events: (page.events || []).map((event) => ({
                ...event,
                child_session_id: request.id,
                agent: request.agent || "",
              })),
            }))
            .catch(() => ({ id: request.id, events: [] as SessionEvent[] }))
        )
    ).then((loaded) => {
      if (sessionRef.current !== loadId) {
        return;
      }
      setChildEvents((current) => {
        const next = { ...current };
        for (const entry of loaded) {
          if (entry.events.length > 0) {
            next[entry.id] = entry.events.slice().sort(bySeq);
          }
        }
        return next;
      });
    });
  }, []);

  const started = useCallback((result: { turn: string; turn_id?: string }) => {
    if (result.turn === "queued") {
      setQueued(true);
    } else {
      if (result.turn_id) {
        setLiveTurnId(result.turn_id);
        setLiveStartedAt(Date.now());
      }
      setStreamText("");
      setThinking(false);
    }
    runningRef.current = true;
    setRunning(true);
  }, []);

  const cancel = useCallback(async () => {
    if (!id || cancelling) {
      return;
    }
    setCancelling(true);
    setActionError("");
    try {
      const result = await cancelTurnRequest(id);
      if (result.turn !== "cancelling") {
        runningRef.current = false;
        setRunning(false);
        setCancelling(false);
      }
    } catch (failure) {
      setCancelling(false);
      setActionError("Could not stop the turn. " + errorMessage(failure));
    }
  }, [id, cancelling]);

  const continueTurn = useCallback(async () => {
    if (!id || continueBusy || runningRef.current) {
      return;
    }
    setContinueBusy(true);
    setContinueError("");
    try {
      started(await continueSession(id));
    } catch (failure) {
      setContinueError(errorMessage(failure));
    } finally {
      setContinueBusy(false);
    }
  }, [id, continueBusy, started]);

  const answerApproval = useCallback(
    async (approval: ApprovalEvent, outcome: string) => {
      setActionError("");
      setApprovals((current) =>
        current.map((item) =>
          item.request_id === approval.request_id ? { ...item, deciding: true, error: "" } : item
        )
      );
      try {
        await answerApprovalRequest(approval.request_id, outcome);
        markDecided(approval.request_id, outcome);
      } catch (failure) {
        if (failure instanceof ApiError && failure.code === "approval_closed") {
          markDecided(approval.request_id, "approval_closed");
          return;
        }
        setApprovals((current) =>
          current.map((item) =>
            item.request_id === approval.request_id
              ? { ...item, deciding: false, error: errorMessage(failure) }
              : item
          )
        );
      }
    },
    [markDecided]
  );

  const expireApproval = useCallback(
    (approval: ApprovalEvent) => markDecided(approval.request_id, "unavailable"),
    [markDecided]
  );

  const dismissApproval = useCallback(() => {
    setApprovals((current) => current.filter((item) => !item.decided));
  }, []);

  const openQuestion = useMemo(() => {
    const found = openQuestionFromEvents(events, childEvents);
    return found && !closedQuestionIds.includes(found.request_id) ? found : null;
  }, [events, childEvents, closedQuestionIds]);
  const question: QuestionEvent | null =
    openQuestion ||
    (liveQuestion && !closedQuestionIds.includes(liveQuestion.request_id) ? liveQuestion : null);

  const sendAnswer = useCallback(
    async (request: QuestionEvent, send: (requestId: string) => Promise<unknown>) => {
      const requestId = request && request.request_id;
      if (!requestId) {
        return { ok: false, message: "Could not send. Try again." };
      }
      setQuestionBusy(true);
      try {
        await send(requestId);
        closeQuestion(requestId);
        return { ok: true };
      } catch (failure) {
        if (failure instanceof ApiError) {
          if (failure.status === 422) {
            return { ok: false, message: "Check your answers" };
          }
          if (failure.status === 409 || failure.status === 404) {
            closeQuestion(requestId);
            return { ok: true };
          }
        }
        return { ok: false, message: "Could not send. Try again." };
      } finally {
        setQuestionBusy(false);
      }
    },
    [closeQuestion]
  );

  const submitQuestion = useCallback(
    (request: QuestionEvent, body: QuestionAnswerRequest) =>
      sendAnswer(request, (requestId) => answerQuestionRequest(requestId, body)),
    [sendAnswer]
  );

  const skipQuestion = useCallback(
    (request: QuestionEvent) => sendAnswer(request, (requestId) => skipAllQuestionsRequest(requestId)),
    [sendAnswer]
  );

  const expireQuestion = useCallback(
    (request: QuestionEvent) => closeQuestion(request && request.request_id),
    [closeQuestion]
  );

  const approval =
    approvals.find((item) => !item.decided) ||
    (approvals.length > 0 ? approvals[approvals.length - 1] || null : null);

  return {
    loading,
    loadError,
    events,
    thread,
    turnEvents,
    childEvents,
    loadChildren,
    streamState,
    running,
    liveTurnId,
    streamText,
    thinking,
    queued,
    pending,
    setPending,
    started,
    cancelling,
    cancel,
    continueTurn,
    continueBusy,
    continueError,
    actionError,
    clearError: () => setActionError(""),
    approval,
    answerApproval,
    expireApproval,
    dismissApproval,
    question,
    questionBusy,
    submitQuestion,
    skipQuestion,
    expireQuestion,
  };
}
