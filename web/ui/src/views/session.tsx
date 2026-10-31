import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type {
  FormEvent,
  KeyboardEvent as ReactKeyboardEvent,
  ReactNode,
  RefObject,
} from "react";
import { listAgents } from "../api/agents";
import type { Agent } from "../api/agents";
import { answerApproval } from "../api/approvals";
import type { ApprovalEvent } from "../api/approvals";
import { authSession } from "../api/auth";
import {
  ApiError,
  DAEMON_DRAINING_TEXT,
  errorMessage,
  isDaemonDraining,
  openStream,
} from "../api/client";
import type { SSEStream, StreamFrame, StreamState } from "../api/client";
import { answerQuestion, skipAllQuestions } from "../api/questions";
import type { QuestionAnswerRequest, QuestionEvent } from "../api/questions";
import { getSession, listEvents, patchSession } from "../api/sessions";
import type {
  PatchSessionRequest,
  SessionDetail,
  SessionEvent,
  Team,
} from "../api/sessions";
import {
  cancelTurn as cancelTurnRequest,
  compactSession as compactSessionRequest,
  continueSession as continueSessionRequest,
  forkSession as forkSessionRequest,
  sendMessage as sendMessageRequest,
} from "../api/turns";
import { holderText, stopReasonWords } from "../lib/format";
import { Dialog, ErrorNotice, Loading, useAnnounce } from "../ui/ui";
import { ApprovalCard, approvalResultWords } from "./approval";
import type { ApprovalItem } from "./approval";
import { isTeam, rosterNames } from "./agent-common";
import { openQuestionFromEvents, QuestionCard } from "./question";
import type { OpenQuestion } from "./question";
import { buildTurns, TurnBlock, UserBubble } from "./turn";

/* Chat view of the sessions screen (REQ-WEB-038, REQ-WEB-039, REQ-WEB-040).
   It replaces the rev 1 history list and keeps the turn block and the approval
   card in their own components for the team block and question card later. */

const POLICY_OPTIONS = [
  { value: "ask", label: "Ask", hint: "Ask before risky tools." },
  { value: "trust", label: "Trust", hint: "Run tools without asking." },
  { value: "never", label: "Never", hint: "Never ask; block unapproved tools." },
];

const COMPOSER_LINE_HEIGHT = 20;
const COMPOSER_MIN_ROWS = 2;
const COMPOSER_MAX_ROWS = 8;

interface ChildLogRequest {
  id: string;
  agent: string;
}

interface LiveTool {
  callId: string;
  name: string;
  input: unknown;
  status: string;
  output: unknown;
  at: number;
}

interface ChildLiveTurn {
  [key: string]: unknown;
  agent: string;
  turnId: string;
  text: string;
  textAt: number;
  tools: Record<string, LiveTool>;
}

interface SubmitResult {
  ok: boolean;
  message?: string;
}

function stringOf(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/* Count the textarea's rendered lines and clamp the rows attribute to 2..8.
   The composer uses a 20px line height, so the content height gives the count. */
function composerRowCount(node: HTMLTextAreaElement): number {
  const lines = Math.round(node.scrollHeight / COMPOSER_LINE_HEIGHT);
  return Math.min(COMPOSER_MAX_ROWS, Math.max(COMPOSER_MIN_ROWS, lines));
}

function Icon({
  name,
  className = "icon-20",
}: {
  name: string;
  className?: string;
}) {
  return (
    <img
      className={className}
      src={"/assets/icons/" + name + ".svg"}
      alt=""
      aria-hidden="true"
    />
  );
}

function channelLabel(value: string): string {
  switch (value) {
    case "web":
      return "Web";
    case "cli":
      return "CLI";
    case "telegram":
      return "Telegram";
    case "library":
      return "Library";
    default:
      return value || "Unknown";
  }
}

function policyLabel(value: string): string {
  for (const option of POLICY_OPTIONS) {
    if (option.value === value) {
      return option.label;
    }
  }
  return value ? value : "Ask";
}

function shortModel(value: string): string {
  const text = String(value || "");
  const parts = text.split(":");
  return parts.length > 1 ? parts.slice(1).join(":") : text;
}

function turnCountText(count: unknown): string {
  const value = Number(count) || 0;
  return value + (value === 1 ? " turn" : " turns");
}

function turnEndAnnouncement(reason: string, error: unknown): string {
  if (reason === "cancelled") {
    return "Turn stopped: cancelled.";
  }
  if (reason === "error") {
    const record =
      error && typeof error === "object"
        ? (error as Record<string, unknown>)
        : null;
    const message =
      record && record.message ? " " + String(record.message) : "";
    return "Turn failed." + message;
  }
  if (reason === "max_steps") {
    return "Turn stopped: maximum steps reached.";
  }
  return "Turn finished: " + stopReasonWords(reason) + ".";
}

/* API-TFB-003: the refusal text of a failed Continue. */
function continueErrorMessage(failure: unknown): string {
  if (failure instanceof ApiError) {
    if (failure.status === 409 && failure.code === "turn_running") {
      return "A turn is already running.";
    }
    if (failure.status === 409 && failure.code === "nothing_to_continue") {
      return "Nothing to continue. The last turn ended normally.";
    }
  }
  if (isDaemonDraining(failure)) {
    return DAEMON_DRAINING_TEXT;
  }
  return errorMessage(failure);
}

function approvalAnnouncement(outcome: string): string {
  if (outcome === "unavailable") {
    return "Approval expired. The tool will not run.";
  }
  if (outcome === "cancelled") {
    return "Approval cancelled.";
  }
  return "Approval decided: " + approvalResultWords(outcome) + ".";
}

function questionClosedWords(payload: Record<string, unknown>): string {
  if (payload && payload.outcome === "answered") {
    return "Already answered elsewhere";
  }
  if (payload && payload.reason === "timeout") {
    return "Question expired";
  }
  if (
    payload &&
    (payload.reason === "cancelled" || payload.reason === "interrupted")
  ) {
    return "Question cancelled";
  }
  return "Already answered elsewhere";
}

function menuFocusables(node: HTMLElement | null): HTMLElement[] {
  if (!node) {
    return [];
  }
  return Array.from(
    node.querySelectorAll<HTMLElement>(
      "button:not([disabled]), input:not([disabled])"
    )
  );
}

/* Small menu popover for the composer. Escape closes it and returns focus to
   the trigger; arrows and Tab move through the items. */
function Popover({
  open,
  onClose,
  triggerRef,
  label,
  align = "left",
  children,
}: {
  open: boolean;
  onClose: () => void;
  triggerRef: RefObject<HTMLElement | null>;
  label: string;
  align?: "left" | "right";
  children?: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const closeRef = useRef(onClose);
  // eslint-disable-next-line react-hooks/refs -- the latest close handler is kept for the native listeners, as in session.js
  closeRef.current = onClose;

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const node = ref.current;
    const items = menuFocusables(node);
    if (items.length > 0) {
      items[0]?.focus();
    } else if (node) {
      node.focus();
    }
    function handlePointer(event: MouseEvent) {
      const trigger = triggerRef && triggerRef.current;
      if (node && node.contains(event.target as Node)) {
        return;
      }
      if (trigger && trigger.contains(event.target as Node)) {
        return;
      }
      closeRef.current();
    }
    function handleFocusOut(event: FocusEvent) {
      if (!ref.current) {
        return;
      }
      if (
        event.relatedTarget &&
        ref.current.contains(event.relatedTarget as Node)
      ) {
        return;
      }
      if (!event.relatedTarget) {
        return;
      }
      closeRef.current();
    }
    document.addEventListener("mousedown", handlePointer);
    node?.addEventListener("focusout", handleFocusOut);
    return () => {
      document.removeEventListener("mousedown", handlePointer);
      node?.removeEventListener("focusout", handleFocusOut);
    };
  }, [open, triggerRef]);

  if (!open) {
    return null;
  }

  function handleKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      closeRef.current();
      const trigger = triggerRef && triggerRef.current;
      if (trigger) {
        trigger.focus();
      }
      return;
    }
    if (
      event.key !== "ArrowDown" &&
      event.key !== "ArrowUp" &&
      event.key !== "Home" &&
      event.key !== "End"
    ) {
      return;
    }
    const target = event.target;
    if (
      target instanceof HTMLElement &&
      (target.nodeName === "INPUT" || target.nodeName === "TEXTAREA")
    ) {
      return;
    }
    const items = menuFocusables(ref.current);
    if (items.length === 0) {
      return;
    }
    event.preventDefault();
    const active = document.activeElement;
    const index = active ? items.indexOf(active as HTMLElement) : -1;
    let next = index;
    if (event.key === "ArrowDown") {
      next = index < 0 ? 0 : (index + 1) % items.length;
    } else if (event.key === "ArrowUp") {
      next = index < 0 ? items.length - 1 : (index - 1 + items.length) % items.length;
    } else if (event.key === "Home") {
      next = 0;
    } else {
      next = items.length - 1;
    }
    items[next]?.focus();
  }

  return (
    <div
      ref={ref}
      className={"composer-menu" + (align === "right" ? " is-right" : "")}
      role="menu"
      aria-label={label}
      tabIndex={-1}
      onKeyDown={handleKeyDown}
    >
      {children}
    </div>
  );
}

function AgentMenuContent({
  agents,
  current,
  disabled,
  onPick,
}: {
  agents: Agent[];
  current: string;
  disabled: boolean;
  onPick: (name: string) => void;
}) {
  return (
    <>
      <p className="menu-heading">Switch agent or team</p>
      {agents.length === 0 ? (
        <p className="menu-empty">No agents configured.</p>
      ) : (
        agents.map((entry) => {
          const team = isTeam(entry);
          return (
            <button
              key={entry.name}
              type="button"
              role="menuitemradio"
              className="menu-item"
              aria-checked={entry.name === current}
              disabled={disabled}
              onClick={() => onPick(entry.name)}
            >
              <span className="menu-item-text">
                <span>{entry.name}</span>
                <span className="menu-item-meta">
                  {team
                    ? "Team · " + rosterNames(entry).length + " agents"
                    : entry.model || "agent default"}
                </span>
              </span>
              {entry.name === current ? (
                <Icon name="check" className="icon-16 menu-check" />
              ) : null}
            </button>
          );
        })
      )}
    </>
  );
}

function ModelMenuContent({
  current,
  agentModel,
  onPick,
}: {
  current: string;
  agentModel: string;
  onPick: (value: string) => void;
}) {
  const [custom, setCustom] = useState("");
  const options: { value: string; label: string; meta: string }[] = [];
  if (current) {
    options.push({
      value: current,
      label: shortModel(current),
      meta: "Current model",
    });
  }
  if (agentModel && agentModel !== current) {
    options.push({
      value: agentModel,
      label: shortModel(agentModel),
      meta: "Agent default",
    });
  }

  function submitCustom(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = custom.trim();
    if (value) {
      onPick(value);
    }
  }

  return (
    <>
      <p className="menu-heading">Model</p>
      {options.length === 0 ? (
        <p className="menu-empty">No model is set yet.</p>
      ) : (
        options.map((option) => (
          <button
            key={option.value}
            type="button"
            role="menuitemradio"
            className="menu-item"
            aria-checked={option.value === current}
            onClick={() => onPick(option.value)}
          >
            <span className="menu-item-text">
              <span>{option.label}</span>
              <span className="menu-item-meta">
                {option.meta + " · " + option.value}
              </span>
            </span>
            {option.value === current ? (
              <Icon name="check" className="icon-16 menu-check" />
            ) : null}
          </button>
        ))
      )}
      <form className="menu-custom" onSubmit={submitCustom}>
        <input
          type="text"
          className="menu-custom-input"
          aria-label="Custom model as provider:model"
          placeholder="provider:model"
          value={custom}
          onChange={(event) => setCustom(event.target.value)}
        />
        <button type="submit" className="menu-set" disabled={!custom.trim()}>
          Set
        </button>
      </form>
    </>
  );
}

function PolicyMenuContent({
  current,
  onPick,
}: {
  current: string;
  onPick: (value: string) => void;
}) {
  return (
    <>
      <p className="menu-heading">Policy</p>
      {POLICY_OPTIONS.map((option) => (
        <button
          key={option.value}
          type="button"
          role="menuitemradio"
          className="menu-item"
          aria-checked={option.value === current}
          onClick={() => onPick(option.value)}
        >
          <span className="menu-item-text">
            <span>{option.label}</span>
            <span className="menu-item-meta">{option.hint}</span>
          </span>
          {option.value === current ? (
            <Icon name="check" className="icon-16 menu-check" />
          ) : null}
        </button>
      ))}
      <p className="menu-note">Choosing Trust asks for confirmation first.</p>
    </>
  );
}

function TrustDialog({
  busy,
  onCancel,
  onConfirm,
}: {
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <Dialog title="Set policy to trust?" onClose={onCancel}>
      <p>
        Policy <strong>trust</strong> lets tools in this session run without
        asking for approval. Only choose it for an agent and session you trust.
      </p>
      <div className="dialog-actions">
        <button type="button" data-autofocus disabled={busy} onClick={onCancel}>
          Cancel
        </button>
        <button type="button" className="danger" disabled={busy} onClick={onConfirm}>
          {busy ? "Saving…" : "Set policy to trust"}
        </button>
      </div>
    </Dialog>
  );
}

interface ComposerProps {
  agent: string;
  team: Team | null;
  model: string;
  policy: string;
  agents: Agent[];
  draft: string;
  readOnly: boolean;
  questionOpen: boolean;
  turnRunning: boolean;
  cancelling: boolean;
  sending: boolean;
  onDraft: (value: string) => void;
  onSend: () => void;
  onStop: () => void;
  onAgent: (name: string) => void;
  onModel: (value: string) => void;
  onPolicy: (value: string, confirmTrust: boolean) => Promise<boolean>;
}

function Composer({
  agent,
  team,
  model,
  policy,
  agents,
  draft,
  readOnly,
  questionOpen,
  turnRunning,
  cancelling,
  sending,
  onDraft,
  onSend,
  onStop,
  onAgent,
  onModel,
  onPolicy,
}: ComposerProps) {
  const [menu, setMenu] = useState("");
  const [trustOpen, setTrustOpen] = useState(false);
  const [trustBusy, setTrustBusy] = useState(false);
  const [rows, setRows] = useState(COMPOSER_MIN_ROWS);
  const plusRef = useRef<HTMLButtonElement>(null);
  const modelRef = useRef<HTMLButtonElement>(null);
  const policyRef = useRef<HTMLButtonElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  useLayoutEffect(() => {
    const node = inputRef.current;
    if (!node) {
      return;
    }
    /* Collapse to one row first so scrollHeight reports the content height even
       when the previous draft filled eight rows. */
    node.rows = 1;
    const next = composerRowCount(node);
    node.rows = next;
    setRows(next);
  }, [draft]);

  const agentEntry = agents.find((entry) => entry.name === agent) || null;
  const agentModel = agentEntry && agentEntry.model ? agentEntry.model : "";
  const modelValue = model || agentModel;
  const teamSession = Boolean(team) || isTeam(agentEntry);
  const messageTarget =
    teamSession && team && team.name ? String(team.name) : agent || "agent";
  const busySession = readOnly || turnRunning;

  function closeMenu(kind: "plus" | "model" | "policy") {
    setMenu("");
    const target =
      kind === "plus" ? plusRef : kind === "model" ? modelRef : policyRef;
    if (target && target.current) {
      window.setTimeout(() => {
        if (target.current) {
          target.current.focus();
        }
      }, 0);
    }
  }

  function pickAgent(name: string) {
    setMenu("");
    if (plusRef.current) {
      window.setTimeout(() => plusRef.current && plusRef.current.focus(), 0);
    }
    onAgent(name);
  }

  function pickModel(value: string) {
    setMenu("");
    if (modelRef.current) {
      window.setTimeout(() => modelRef.current && modelRef.current.focus(), 0);
    }
    onModel(value);
  }

  function pickPolicy(value: string) {
    setMenu("");
    if (value === "trust") {
      setTrustOpen(true);
      return;
    }
    if (policyRef.current) {
      window.setTimeout(
        () => policyRef.current && policyRef.current.focus(),
        0
      );
    }
    onPolicy(value, false);
  }

  function cancelTrust() {
    setTrustOpen(false);
    if (policyRef.current) {
      window.setTimeout(
        () => policyRef.current && policyRef.current.focus(),
        0
      );
    }
  }

  async function confirmTrust() {
    setTrustBusy(true);
    await onPolicy("trust", true);
    setTrustBusy(false);
    setTrustOpen(false);
    if (policyRef.current) {
      window.setTimeout(
        () => policyRef.current && policyRef.current.focus(),
        0
      );
    }
  }

  function composerKeyDown(event: ReactKeyboardEvent<HTMLTextAreaElement>) {
    if (readOnly || questionOpen || turnRunning) {
      return;
    }
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault();
      onSend();
    }
  }

  return (
    <div className="composer">
      {questionOpen ? (
        <p className="sr-only" id="composer-question-note">
          Answer the question above
        </p>
      ) : null}
      <div className="composer-box">
        <textarea
          ref={inputRef}
          className="composer-input"
          rows={rows}
          aria-label={"Message " + messageTarget}
          aria-describedby={questionOpen ? "composer-question-note" : undefined}
          placeholder={
            questionOpen
              ? "Answer the question above"
              : "Message " + messageTarget + "…"
          }
          value={draft}
          disabled={readOnly || questionOpen}
          onChange={(event) => onDraft(event.target.value)}
          onKeyDown={composerKeyDown}
        ></textarea>
        <div className="composer-bar">
          <span className="composer-control">
            <button
              ref={plusRef}
              type="button"
              className="composer-plus"
              aria-label="Composer actions"
              aria-haspopup="menu"
              aria-expanded={menu === "plus" || menu === "agent"}
              disabled={readOnly}
              onClick={() =>
                setMenu(menu === "plus" || menu === "agent" ? "" : "plus")
              }
            >
              <Icon name="plus" />
            </button>
            <Popover
              open={menu === "plus"}
              onClose={() => closeMenu("plus")}
              triggerRef={plusRef}
              label="Composer actions"
            >
              <p className="menu-heading">Composer</p>
              <button
                type="button"
                role="menuitem"
                className="menu-item"
                disabled={busySession}
                onClick={() => setMenu("agent")}
              >
                <span className="menu-item-text">
                  <span>Switch agent or team</span>
                  <span className="menu-item-meta">{agent || "no agent"}</span>
                </span>
              </button>
              <button type="button" role="menuitem" className="menu-item" disabled>
                <span className="menu-item-text">
                  <span>Attach file</span>
                  <span className="menu-item-meta">coming soon</span>
                </span>
              </button>
            </Popover>
            <Popover
              open={menu === "agent"}
              onClose={() => closeMenu("plus")}
              triggerRef={plusRef}
              label="Switch agent or team"
            >
              <AgentMenuContent
                agents={agents}
                current={agent}
                disabled={busySession}
                onPick={pickAgent}
              />
            </Popover>
          </span>

          <span className="composer-control">
            {teamSession ? (
              <button
                ref={modelRef}
                type="button"
                className="composer-pill is-team-model"
                aria-label="Each agent in the team uses its own model."
                title="Each agent in the team uses its own model."
                disabled
              >
                <span className="composer-pill-label">per agent</span>
                <Icon name="chevron-down" />
              </button>
            ) : (
              <>
                <button
                  ref={modelRef}
                  type="button"
                  className="composer-pill"
                  aria-label={"Model: " + (modelValue || "not set")}
                  aria-haspopup="menu"
                  aria-expanded={menu === "model"}
                  title={modelValue || "No model set"}
                  disabled={busySession}
                  onClick={() => setMenu(menu === "model" ? "" : "model")}
                >
                  <span className="composer-pill-label">
                    {shortModel(modelValue) || "Model"}
                  </span>
                  <Icon name="chevron-down" />
                </button>
                <Popover
                  open={menu === "model"}
                  onClose={() => closeMenu("model")}
                  triggerRef={modelRef}
                  label="Model"
                >
                  <ModelMenuContent
                    current={modelValue}
                    agentModel={agentModel}
                    onPick={pickModel}
                  />
                </Popover>
              </>
            )}
          </span>

          <span className="composer-control">
            <button
              ref={policyRef}
              type="button"
              className="composer-pill"
              aria-label={"Policy: " + policyLabel(policy)}
              aria-haspopup="menu"
              aria-expanded={menu === "policy"}
              disabled={busySession}
              onClick={() => setMenu(menu === "policy" ? "" : "policy")}
            >
              <Icon name="lock" />
              <span className="composer-pill-label">{policyLabel(policy)}</span>
              <Icon name="chevron-down" />
            </button>
            <Popover
              open={menu === "policy"}
              onClose={() => closeMenu("policy")}
              triggerRef={policyRef}
              label="Policy"
            >
              <PolicyMenuContent current={policy} onPick={pickPolicy} />
            </Popover>
          </span>

          <span className="composer-spacer"></span>

          {turnRunning && !readOnly ? (
            <button
              type="button"
              className="composer-send composer-stop"
              aria-label="Stop the running turn"
              title="Stop the running turn"
              disabled={cancelling}
              onClick={onStop}
            >
              <span className="stop-square" aria-hidden="true"></span>
            </button>
          ) : (
            <button
              type="button"
              className="composer-send"
              aria-label="Send message"
              title="Send message"
              disabled={readOnly || questionOpen || sending || !draft.trim()}
              onClick={onSend}
            >
              <Icon name="arrow-up" className="icon-20 send-icon" />
            </button>
          )}
        </div>
      </div>
      {trustOpen ? (
        <TrustDialog
          busy={trustBusy}
          onCancel={cancelTrust}
          onConfirm={confirmTrust}
        />
      ) : null}
    </div>
  );
}

export interface SessionPageProps {
  id: string;
}

export function SessionPage({ id }: SessionPageProps) {
  const announce = useAnnounce();
  const [detail, setDetail] = useState<SessionDetail | null>(null);
  const [events, setEvents] = useState<SessionEvent[]>([]);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [loadDetails, setLoadDetails] = useState<unknown[]>([]);
  const [streamState, setStreamState] = useState<
    StreamState | "connecting" | "closed"
  >("connecting");
  const [streamReady, setStreamReady] = useState(false);
  const [turnRunning, setTurnRunning] = useState(false);
  const [liveTurnId, setLiveTurnId] = useState("");
  const [liveStartedAt, setLiveStartedAt] = useState(0);
  const [streamText, setStreamText] = useState("");
  const [liveThinking, setLiveThinking] = useState(false);
  const [holder, setHolder] = useState("none");
  const [holderPid, setHolderPid] = useState<number | null>(null);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [continueBusy, setContinueBusy] = useState(false);
  const [continueError, setContinueError] = useState("");
  const [pendingText, setPendingText] = useState("");
  const [queued, setQueued] = useState(false);
  const [notice, setNotice] = useState<{ text: string; tone: string } | null>(
    null
  );
  const [actionError, setActionError] = useState("");
  const [actionDetails, setActionDetails] = useState<unknown[]>([]);
  const [approvals, setApprovals] = useState<ApprovalItem[]>([]);
  const [questionBusy, setQuestionBusy] = useState(false);
  const [closedQuestionIds, setClosedQuestionIds] = useState<string[]>([]);
  const [liveQuestion, setLiveQuestion] = useState<OpenQuestion | null>(null);
  const [childEvents, setChildEvents] = useState<
    Record<string, SessionEvent[]>
  >({});
  const [childLive, setChildLive] = useState<Record<string, ChildLiveTurn>>({});

  const lastSeqRef = useRef(0);
  const childSeenRef = useRef(new Set<string>());
  const sessionIdRef = useRef(id);
  sessionIdRef.current = id;
  const pendingRef = useRef("");
  const decidedRef = useRef(new Set<string>());
  const openApprovalIdsRef = useRef(new Set<string>());
  const questionNotifiedRef = useRef(new Set<string>());
  const closedQuestionIdsRef = useRef(new Set<string>());
  const questionSubmittingRef = useRef("");
  const openQuestionIdRef = useRef("");
  const endedRef = useRef(new Set<string>());
  const refreshRef = useRef<(() => Promise<SessionDetail | null>) | null>(null);
  const streamRef = useRef<SSEStream | null>(null);
  const lastHolderRef = useRef<string | null>(null);
  const lastStreamStateRef = useRef("connecting");
  const lastRunningRef = useRef(false);
  const messagesRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);

  const readOnly = holder === "other";
  const busySession = readOnly || turnRunning;

  /* The open request comes from the session log: question/asked without a
     question/answered. It follows the same replay path as the rest of the
     thread, so a page load or reconnect shows the card without a second
     question-only stream. */
  const openQuestion = useMemo(() => {
    const found = openQuestionFromEvents(events, childEvents);
    if (!found) {
      return null;
    }
    if (closedQuestionIds.includes(found.request_id)) {
      return null;
    }
    return found;
  }, [events, childEvents, closedQuestionIds]);
  const activeQuestion: QuestionEvent | null =
    openQuestion ||
    (liveQuestion && !closedQuestionIds.includes(liveQuestion.request_id)
      ? liveQuestion
      : null);
  openQuestionIdRef.current = activeQuestion ? activeQuestion.request_id : "";

  const clearError = useCallback(() => {
    setActionError("");
    setActionDetails([]);
  }, []);

  const showNotice = useCallback((text: string, tone = "info") => {
    setNotice({ text, tone });
  }, []);

  const reportError = useCallback(
    (failure: unknown, prefix: string) => {
      let message = errorMessage(failure);
      if (failure instanceof ApiError) {
        if (failure.code === "session_in_use") {
          const pid = failure.pid;
          message = pid
            ? "Session is in use by another process (PID " + pid + ")."
            : "Session is in use by another process.";
          setHolder("other");
          if (pid) {
            setHolderPid(pid);
          }
        }
        if (failure.code === "invalid_state") {
          message = "The operation is not allowed while a turn is running.";
        }
        setActionDetails(failure.details || []);
      } else {
        setActionDetails([]);
      }
      setActionError(message);
      announce(prefix + " " + message);
    },
    [announce]
  );

  const refreshDetail = useCallback(async (): Promise<SessionDetail | null> => {
    try {
      const next = await getSession(id);
      setDetail(next);
      setHolder(next.in_use || "none");
      setHolderPid(next.holder_pid || null);
      setTurnRunning(Boolean(next.turn_running));
      lastRunningRef.current = Boolean(next.turn_running);
      return next;
    } catch {
      return null;
    }
  }, [id]);

  refreshRef.current = refreshDetail;

  const markApprovalDecided = useCallback(
    (requestId: string, outcome: string, announcement?: string) => {
      if (!requestId) {
        return;
      }
      const first = !decidedRef.current.has(requestId);
      decidedRef.current.add(requestId);
      openApprovalIdsRef.current.delete(requestId);
      setApprovals((current) =>
        current.map((item) =>
          item.request_id === requestId
            ? { ...item, decided: outcome, deciding: false, error: "" }
            : item
        )
      );
      if (first && announcement) {
        announce(announcement);
      }
    },
    [announce]
  );

  const closeQuestion = useCallback((requestId: string) => {
    if (!requestId) {
      return;
    }
    openQuestionIdRef.current = "";
    closedQuestionIdsRef.current.add(requestId);
    setLiveQuestion((current) =>
      current && current.request_id === requestId ? null : current
    );
    setClosedQuestionIds((current) =>
      current.includes(requestId) ? current : current.concat([requestId])
    );
    /* The status projection moves from waiting_input back to running (or
       idle) once question/answered is written. Refresh it for the header. */
    if (refreshRef.current) {
      refreshRef.current();
    }
  }, []);

  const announceQuestionResult = useCallback(
    (requestId: string, words: string) => {
      if (!requestId || questionNotifiedRef.current.has(requestId)) {
        return;
      }
      questionNotifiedRef.current.add(requestId);
      announce(words);
    },
    [announce]
  );

  const submitQuestion = useCallback(
    async (
      request: QuestionEvent,
      send: (requestId: string) => Promise<unknown>
    ): Promise<SubmitResult> => {
      const requestId = request && request.request_id;
      if (!requestId) {
        return { ok: false, message: "Could not send. Try again." };
      }
      setQuestionBusy(true);
      questionSubmittingRef.current = requestId;
      try {
        await send(requestId);
        closeQuestion(requestId);
        announceQuestionResult(requestId, "Answer sent");
        return { ok: true };
      } catch (failure) {
        if (failure instanceof ApiError) {
          if (failure.status === 422) {
            return { ok: false, message: "Check your answers" };
          }
          if (failure.status === 409) {
            closeQuestion(requestId);
            announceQuestionResult(requestId, "Already answered elsewhere");
            return { ok: true };
          }
          if (failure.status === 404) {
            closeQuestion(requestId);
            announceQuestionResult(requestId, "This question is no longer open");
            return { ok: true };
          }
        }
        return { ok: false, message: "Could not send. Try again." };
      } finally {
        if (questionSubmittingRef.current === requestId) {
          questionSubmittingRef.current = "";
        }
        setQuestionBusy(false);
      }
    },
    [announceQuestionResult, closeQuestion]
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

  const handleQuestionExpired = useCallback(
    (request: QuestionEvent) => {
      const requestId = request && request.request_id;
      closeQuestion(requestId);
      announceQuestionResult(requestId, "Question expired");
    },
    [announceQuestionResult, closeQuestion]
  );

  const finishTurn = useCallback(
    (turnId: string, reason: string, error: unknown) => {
      if (turnId && endedRef.current.has(turnId)) {
        return;
      }
      if (turnId) {
        endedRef.current.add(turnId);
      }
      lastRunningRef.current = false;
      setTurnRunning(false);
      setCancelling(false);
      setStreamText("");
      setLiveThinking(false);
      setQueued(false);
      setLiveTurnId("");
      setLiveStartedAt(0);
      openApprovalIdsRef.current.clear();
      setApprovals((current) =>
        current.map((item) =>
          item.decided
            ? item
            : {
                ...item,
                decided:
                  reason === "cancelled" ? "cancelled" : "unavailable",
                deciding: false,
              }
        )
      );
      pendingRef.current = "";
      setPendingText("");
      const text = turnEndAnnouncement(reason, error);
      if (reason !== "end_turn") {
        showNotice(text, reason === "error" ? "warn" : "info");
      }
      announce(text);
      if (refreshRef.current) {
        refreshRef.current();
      }
    },
    [announce, showNotice]
  );

  /* Older turns load their child logs only when the owner expands the work
     panel (Q-011). The fetched events are tagged with the child agent so the
     panel can group its tool rows. */
  const loadChildEvents = useCallback(
    async (requests: ChildLogRequest[]) => {
      if (!Array.isArray(requests) || requests.length === 0) {
        return;
      }
      const list = requests;
      const loadId = sessionIdRef.current;
      const loaded: Record<string, SessionEvent[]> = {};
      await Promise.all(
        list.map(async (request) => {
          const childId = request && request.id ? String(request.id) : "";
          if (!childId) {
            return;
          }
          try {
            const page = await listEvents(childId, 0);
            loaded[childId] = (page.events || []).map((event) => ({
              ...event,
              child_session_id: childId,
              agent: (request && request.agent) || "",
            }));
          } catch {
            loaded[childId] = [];
          }
        })
      );
      if (sessionIdRef.current !== loadId) {
        return;
      }
      setChildEvents((current) => {
        let changed = false;
        const next = { ...current };
        for (const childId of Object.keys(loaded)) {
          const incoming = loaded[childId];
          if (!incoming || incoming.length === 0) {
            continue;
          }
          const bySeq = new Map<string, SessionEvent>();
          const existing = Array.isArray(next[childId]) ? next[childId] : [];
          for (const event of existing) {
            if (event && event.seq !== undefined) {
              bySeq.set(String(event.seq), event);
            }
          }
          for (const event of incoming) {
            if (event && event.seq !== undefined) {
              bySeq.set(String(event.seq), event);
            }
          }
          next[childId] = Array.from(bySeq.values()).sort(
            (left, right) => (Number(left.seq) || 0) - (Number(right.seq) || 0)
          );
          changed = true;
        }
        return changed ? next : current;
      });
    },
    []
  );

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setLoadError("");
    setLoadDetails([]);
    setStreamReady(false);
    setApprovals([]);
    setStreamText("");
    setLiveThinking(false);
    setQueued(false);
    setPendingText("");
    setNotice(null);
    setActionError("");
    setActionDetails([]);
    setCancelling(false);
    setContinueBusy(false);
    setContinueError("");
    setChildEvents({});
    setChildLive({});
    setQuestionBusy(false);
    setClosedQuestionIds([]);
    setLiveQuestion(null);
    pendingRef.current = "";
    closedQuestionIdsRef.current.clear();
    decidedRef.current.clear();
    openApprovalIdsRef.current.clear();
    questionNotifiedRef.current.clear();
    questionSubmittingRef.current = "";
    openQuestionIdRef.current = "";
    childSeenRef.current.clear();
    endedRef.current.clear();
    lastRunningRef.current = false;
    lastHolderRef.current = null;
    stickRef.current = true;

    Promise.all([getSession(id), listEvents(id, 0)])
      .then(([session, page]) => {
        if (!alive) {
          return;
        }
        setDetail(session);
        setEvents(page.events || []);
        lastSeqRef.current = page.last_seq || 0;
        setHolder(session.in_use || "none");
        setHolderPid(session.holder_pid || null);
        setTurnRunning(Boolean(session.turn_running));
        lastRunningRef.current = Boolean(session.turn_running);
        setDraft("");
        setLoading(false);
        setStreamReady(true);
      })
      .catch((failure: unknown) => {
        if (!alive) {
          return;
        }
        setLoadError(errorMessage(failure));
        setLoadDetails(failure instanceof ApiError ? failure.details : []);
        announce("Could not load the session. " + errorMessage(failure));
        setLoading(false);
      });

    listAgents()
      .then((page) => {
        if (alive) {
          setAgents(page.agents || []);
        }
      })
      .catch(() => {
        if (alive) {
          setAgents([]);
        }
      });

    return () => {
      alive = false;
    };
  }, [id, announce]);

  useEffect(() => {
    if (!streamReady) {
      return undefined;
    }
    setStreamState("connecting");
    const stream = openStream(id, {
      fromSeq: lastSeqRef.current,
      onState: (value) => {
        const previous = lastStreamStateRef.current;
        lastStreamStateRef.current = value;
        setStreamState(value);
        if (value === "reconnecting" && previous !== "reconnecting") {
          announce("The live stream is reconnecting.");
        } else if (value === "connected" && previous === "reconnecting") {
          announce("The live stream is connected.");
        }
      },
      onEvent: (event) => {
        const childId =
          typeof event.child_session_id === "string"
            ? event.child_session_id
            : "";
        if (childId) {
          /* Child items never touch the root history or the root turn state.
             A reconnect replays them, so drop a seen (child, seq) pair. */
          const seenKey = childId + "|" + String(event.seq);
          if (childSeenRef.current.has(seenKey)) {
            return;
          }
          childSeenRef.current.add(seenKey);
          setChildEvents((current) => {
            const list = Array.isArray(current[childId])
              ? current[childId]
              : [];
            if (list.some((entry) => entry.seq === event.seq)) {
              return current;
            }
            return {
              ...current,
              [childId]: list
                .concat([event])
                .sort((left, right) => left.seq - right.seq),
            };
          });
          return;
        }
        setEvents((current) => {
          if (current.some((entry) => entry.seq === event.seq)) {
            return current;
          }
          return current
            .concat([event])
            .sort((left, right) => left.seq - right.seq);
        });
        lastSeqRef.current = Math.max(lastSeqRef.current, event.seq);
        const payload =
          event.payload && typeof event.payload === "object"
            ? event.payload
            : {};

        if (event.kind === "user/message" && pendingRef.current) {
          pendingRef.current = "";
          setPendingText("");
          setQueued(false);
        }
        if (event.kind === "assistant/message" || event.kind === "tool/call") {
          setStreamText("");
          setLiveThinking(false);
        }
        if (event.kind === "approval/decided") {
          markApprovalDecided(
            stringOf(payload.request_id),
            stringOf(payload.outcome) || "unavailable",
            approvalAnnouncement(stringOf(payload.outcome) || "unavailable")
          );
        }
        if (event.kind === "question/answered") {
          const requestId = stringOf(payload.request_id);
          if (requestId) {
            closedQuestionIdsRef.current.add(requestId);
            setLiveQuestion((current) =>
              current && current.request_id === requestId ? null : current
            );
          }
          if (requestId && openQuestionIdRef.current === requestId) {
            /* A POST that is still in flight announces its own result. The
               stream event closes the card either way. */
            const inFlight = questionSubmittingRef.current === requestId;
            closeQuestion(requestId);
            if (!inFlight) {
              announceQuestionResult(requestId, questionClosedWords(payload));
            }
          }
        }
        if (event.kind === "turn/end") {
          finishTurn(
            stringOf(payload.turn_id),
            stringOf(payload.reason),
            payload.error
          );
        }
      },
      onFrame: (frame: StreamFrame) => {
        const childId = stringOf(frame.child_session_id);
        if (childId) {
          /* A child frame updates only that child's live thread. The root's
             turn id and streaming text stay untouched (API-TEAM-011). */
          setChildLive((current) => {
            const previous =
              current[childId] || {
                agent: stringOf(frame.agent),
                turnId: "",
                text: "",
                textAt: 0,
                tools: {},
              };
            const turnId = stringOf(frame.turn_id) || previous.turnId || "";
            const fresh = Boolean(
              turnId && previous.turnId && turnId !== previous.turnId
            );
            const entry: ChildLiveTurn = {
              agent: stringOf(frame.agent) || previous.agent || "",
              turnId,
              text: fresh ? "" : previous.text || "",
              textAt: fresh ? 0 : previous.textAt || 0,
              tools: fresh ? {} : previous.tools ? { ...previous.tools } : {},
            };
            if (frame.kind === "text_delta" && frame.text) {
              entry.text += String(frame.text);
              entry.textAt = Date.now();
            } else if (
              frame.kind === "tool_call" ||
              frame.kind === "tool_result"
            ) {
              const callId = stringOf(frame.call_id);
              if (!callId) {
                return current;
              }
              const tool = entry.tools[callId] || {
                callId,
                name: "",
                input: null,
                status: "running",
                output: "",
                at: 0,
              };
              if (frame.kind === "tool_call") {
                entry.tools[callId] = {
                  ...tool,
                  name: stringOf(frame.name) || tool.name,
                  input: frame.input === undefined ? tool.input : frame.input,
                  status: tool.status || "running",
                  at: tool.at || Date.now(),
                };
              } else {
                entry.tools[callId] = {
                  ...tool,
                  status: stringOf(frame.status) || "success",
                  output:
                    frame.output === undefined ? tool.output : frame.output,
                  at: tool.at || Date.now(),
                };
              }
            }
            return { ...current, [childId]: entry };
          });
          return;
        }
        const turnId = stringOf(frame.turn_id);
        if (turnId) {
          setLiveTurnId(turnId);
          if (!lastRunningRef.current) {
            setLiveStartedAt(Date.now());
          }
        }
        if (frame.kind === "text_delta" && frame.text) {
          setLiveThinking(false);
          setStreamText((current) => current + String(frame.text));
        }
        /* REQ-MDL-006: a thinking delta shows a muted note with no thinking
           text; the answer text or the next step clears it. */
        if (frame.kind === "thinking_delta") {
          setLiveThinking(true);
        }
        if (frame.kind === "tool_call" || frame.kind === "tool_result") {
          setLiveThinking(false);
          setStreamText("");
        }
        /* REQ-LTC-032: a step_retry drops the partial text of the failed step;
           streamText only ever holds the current step's streamed answer. */
        if (frame.kind === "step_retry") {
          setLiveThinking(false);
          setStreamText("");
        }
      },
      onStatus: (status: StreamFrame) => {
        const next = stringOf(status.holder) || "none";
        const previous = lastHolderRef.current;
        lastHolderRef.current = next;
        setHolder(next);
        setHolderPid(typeof status.pid === "number" ? status.pid : null);
        const running =
          Boolean(status.turn_running) || openApprovalIdsRef.current.size > 0;
        if (running && !lastRunningRef.current) {
          announce("Turn running.");
        }
        lastRunningRef.current = running;
        setTurnRunning(running);
        if (next === "other" && previous !== "other") {
          announce(
            typeof status.pid === "number"
              ? "This session is now read-only: another process holds it (PID " +
                  status.pid +
                  ")."
              : "This session is now read-only: another process holds it."
          );
        } else if (previous === "other" && next !== "other") {
          announce("The session is no longer held by another process.");
        }
      },
      onTurn: (turn: StreamFrame) => {
        finishTurn(
          stringOf(turn.turn_id),
          stringOf(turn.stop_reason),
          null
        );
      },
      onApproval: (approval) => {
        if (approval.request_id) {
          openApprovalIdsRef.current.add(approval.request_id);
        }
        setApprovals((current) => {
          if (current.some((item) => item.request_id === approval.request_id)) {
            return current;
          }
          return current.concat([
            {
              ...approval,
              decided: "",
              deciding: false,
              error: "",
            },
          ]);
        });
        lastRunningRef.current = true;
        setTurnRunning(true);
        announce("Approval requested for " + (approval.tool || "a tool") + ".");
      },
      onQuestion: (question) => {
        const requestId = question && question.request_id;
        if (!requestId) {
          return;
        }
        if (
          openQuestionIdRef.current === requestId ||
          closedQuestionIdsRef.current.has(requestId)
        ) {
          return;
        }
        setLiveQuestion((current) => {
          if (current && current.request_id === requestId) {
            return current;
          }
          return {
            request_id: requestId,
            call_id: stringOf(question.call_id),
            expires_at: Number(question.expires_at) || 0,
            turn_id: stringOf(question.turn_id),
            questions: Array.isArray(question.questions) ? question.questions : [],
          };
        });
      },
      onApprovalClosed: (payload: StreamFrame) => {
        const requestId = stringOf(payload.request_id);
        if (requestId) {
          markApprovalDecided(
            requestId,
            "approval_closed",
            "Approval closed. Another tab answered first or the approval expired."
          );
        }
      },
      onShutdown: () => {
        setStreamState("closed");
        announce("The daemon is shutting down.");
        if (streamRef.current) {
          streamRef.current.close();
        }
      },
      onError: () => {
        setStreamState("reconnecting");
      },
      onUnauthorized: () => {
        authSession().catch(() => undefined);
      },
    });
    streamRef.current = stream;
    return () => {
      streamRef.current = null;
      stream.close();
    };
  }, [
    id,
    streamReady,
    announce,
    finishTurn,
    markApprovalDecided,
    closeQuestion,
    announceQuestionResult,
  ]);

  const thread = useMemo(
    () =>
      buildTurns(events, {
        running: turnRunning,
        turnId: liveTurnId,
        startedAt: liveStartedAt,
      }),
    [events, turnRunning, liveTurnId, liveStartedAt]
  );

  /* Root events grouped by turn id so the team block can build its steps
   from the turn's own log. */
  const turnEvents = useMemo(() => {
    const map = new Map<string, SessionEvent[]>();
    for (const event of events) {
      const turnId =
        event.turn_id ||
        (event.payload && stringOf(event.payload.turn_id)) ||
        "";
      if (!turnId) {
        continue;
      }
      const list = map.get(turnId) || [];
      list.push(event);
      map.set(turnId, list);
    }
    return map;
  }, [events]);

  useEffect(() => {
    const node = messagesRef.current;
    if (node && stickRef.current) {
      node.scrollTop = node.scrollHeight;
    }
  }, [thread, pendingText, streamText]);

  function handleScroll() {
    const node = messagesRef.current;
    if (!node) {
      return;
    }
    stickRef.current = node.scrollHeight - node.scrollTop - node.clientHeight < 96;
  }

  async function sendMessage() {
    const text = draft.trim();
    if (!text || readOnly || sending || turnRunning) {
      return;
    }
    setSending(true);
    clearError();
    /* Show the bubble before the request so a fast turn that logs the user
       message before the response cannot leave a duplicate pending bubble. */
    setDraft("");
    pendingRef.current = text;
    setPendingText(text);
    setQueued(false);
    try {
      const result = await sendMessageRequest(id, text);
      if (result.turn === "queued") {
        setQueued(true);
        announce("Message queued. It will be claimed at the next step.");
      } else {
        if (!lastRunningRef.current) {
          announce("Turn running.");
        }
        if (result.turn_id) {
          setLiveTurnId(result.turn_id);
          setLiveStartedAt(Date.now());
        }
        setStreamText("");
        setLiveThinking(false);
      }
      lastRunningRef.current = true;
      setTurnRunning(true);
    } catch (failure) {
      pendingRef.current = "";
      setPendingText("");
      setDraft((current) => current || text);
      /* API-TFB-008: the daemon refuses new turns while it drains. Keep the
         typed text so the owner can send it again. */
      if (isDaemonDraining(failure)) {
        showNotice(DAEMON_DRAINING_TEXT, "warn");
        announce(DAEMON_DRAINING_TEXT);
      } else {
        reportError(failure, "Could not send the message.");
      }
    } finally {
      setSending(false);
    }
  }

  async function cancelTurn() {
    if (cancelling) {
      return;
    }
    setCancelling(true);
    clearError();
    try {
      const result = await cancelTurnRequest(id);
      if (result.turn === "cancelling") {
        showNotice("Stopping the turn…", "info");
        announce("Stopping the turn.");
      } else {
        lastRunningRef.current = false;
        setTurnRunning(false);
        setCancelling(false);
        showNotice("No turn is running.", "info");
        announce("No turn is running.");
      }
    } catch (failure) {
      setCancelling(false);
      reportError(failure, "Could not stop the turn.");
    }
  }

  async function compactSession() {
    clearError();
    try {
      const result = await compactSessionRequest(id);
      const text =
        "Compacted from " +
        result.tokens_before +
        " to " +
        result.tokens_after +
        " tokens.";
      showNotice(text, "ok");
      announce(text);
      if (refreshRef.current) {
        refreshRef.current();
      }
    } catch (failure) {
      reportError(failure, "Could not compact the session.");
    }
  }

  async function forkSession() {
    clearError();
    try {
      const created = await forkSessionRequest(id);
      announce("Session forked.");
      window.location.hash = "#/sessions/" + encodeURIComponent(created.id);
    } catch (failure) {
      reportError(failure, "Could not fork the session.");
    }
  }

  /* REQ-TFB-005: Continue resumes the abnormally ended last turn. It follows
     the same live path as a sent message (API-TFB-003). */
  async function continueLastTurn() {
    if (continueBusy || readOnly || turnRunning) {
      return;
    }
    setContinueBusy(true);
    clearError();
    setContinueError("");
    try {
      const result = await continueSessionRequest(id);
      if (result.turn === "queued") {
        setQueued(true);
        announce("Message queued. It will be claimed at the next step.");
      } else {
        if (!lastRunningRef.current) {
          announce("Turn running.");
        }
        if (result.turn_id) {
          setLiveTurnId(result.turn_id);
          setLiveStartedAt(Date.now());
        }
        setStreamText("");
        setLiveThinking(false);
      }
      lastRunningRef.current = true;
      setTurnRunning(true);
    } catch (failure) {
      /* API-TFB-003: a refused Continue shows its reason in the status card. */
      const message = continueErrorMessage(failure);
      setContinueError(message);
      announce(message);
      if (refreshRef.current) {
        refreshRef.current();
      }
    } finally {
      setContinueBusy(false);
    }
  }

  const patch = useCallback(
    async (update: PatchSessionRequest, successText: string) => {
      clearError();
      try {
        const updated = await patchSession(id, update);
        setDetail(updated);
        setHolder(updated.in_use || "none");
        setHolderPid(updated.holder_pid || null);
        setTurnRunning(Boolean(updated.turn_running));
        lastRunningRef.current = Boolean(updated.turn_running);
        showNotice(successText, "ok");
        announce(successText);
        return true;
      } catch (failure) {
        reportError(failure, "Could not change the session.");
        return false;
      }
    },
    [announce, clearError, id, reportError, showNotice]
  );

  const changeAgent = useCallback(
    (name: string) => patch({ agent: name }, "Agent changed to " + name + "."),
    [patch]
  );

  const changeModel = useCallback(
    (value: string) => {
      /* A team session never changes one model: each agent uses its own. */
      const currentAgent = agents.find(
        (entry) => entry.name === (detail && detail.agent)
      );
      if ((detail && detail.team) || isTeam(currentAgent || null)) {
        return Promise.resolve(false);
      }
      return patch({ model: value }, "Model changed to " + value + ".");
    },
    [agents, detail, patch]
  );

  const changePolicy = useCallback(
    (value: string, confirmTrust: boolean) =>
      patch(
        confirmTrust ? { policy: value, confirm_trust: true } : { policy: value },
        "Policy changed to " + value + "."
      ),
    [patch]
  );

  const handleApprovalAnswer = useCallback(
    async (approval: ApprovalEvent, outcome: string) => {
      clearError();
      setApprovals((current) =>
        current.map((item) =>
          item.request_id === approval.request_id
            ? { ...item, deciding: true, error: "" }
            : item
        )
      );
      try {
        await answerApproval(approval.request_id, outcome);
        markApprovalDecided(
          approval.request_id,
          outcome,
          "Approval decided: " + approvalResultWords(outcome) + "."
        );
      } catch (failure) {
        if (failure instanceof ApiError && failure.code === "approval_closed") {
          markApprovalDecided(
            approval.request_id,
            "approval_closed",
            "Approval closed. Another tab answered first or the approval expired."
          );
          return;
        }
        setApprovals((current) =>
          current.map((item) =>
            item.request_id === approval.request_id
              ? { ...item, deciding: false, error: errorMessage(failure) }
              : item
          )
        );
        reportError(failure, "Could not answer the approval.");
      }
    },
    [clearError, markApprovalDecided, reportError]
  );

  const handleApprovalExpired = useCallback(
    (approval: ApprovalEvent) => {
      markApprovalDecided(
        approval.request_id,
        "unavailable",
        "Approval expired. The tool will not run."
      );
    },
    [markApprovalDecided]
  );

  const handleApprovalDismiss = useCallback(() => {
    setApprovals((current) => current.filter((item) => !item.decided));
    window.setTimeout(() => {
      const input = document.querySelector<HTMLTextAreaElement>(
        ".composer-input"
      );
      if (input) {
        input.focus();
      }
    }, 0);
  }, []);

  if (loading) {
    return (
      <main id="main" className="chat-view chat-state">
        <Loading label="Loading the session." />
      </main>
    );
  }

  if (loadError) {
    return (
      <main id="main" className="chat-view chat-state">
        <h1 className="chat-title">Session</h1>
        <ErrorNotice message={loadError} details={loadDetails} />
        <p>
          <a href="#/sessions">Back to sessions</a>
        </p>
      </main>
    );
  }

  if (!detail) {
    return (
      <main id="main" className="chat-view chat-state">
        <p className="muted">Session not found.</p>
        <p>
          <a href="#/sessions">Back to sessions</a>
        </p>
      </main>
    );
  }

  const agent = detail.agent || "agent";
  const team = detail.team || null;
  const activeApproval =
    approvals.find((approval) => !approval.decided) ||
    (approvals.length > 0 ? approvals[approvals.length - 1] || null : null);
  const channel = String(detail.channel || "");
  /* The responder of a question is the session's channel (REQ-ASK-015). The
     named question SSE event is web-only, so a web session gets the
     interactive card and every other channel gets the read-only one. */
  const questionResponder = channel || "web";
  const questionReadOnly = questionResponder !== "web";
  const questionResponderPid = questionResponder === "cli" ? holderPid : null;
  const waitingForInput =
    Boolean(activeQuestion) || detail.status === "waiting_input";
  const turnCount = Number(detail.turn_count) || 0;
  const hasThread = thread.length > 0 || pendingText !== "";
  /* REQ-TFB-004: the end card shows under the session's last turn only. */
  const lastTurnKey = thread.reduce(
    (found: string, item) => (item.kind === "turn" ? item.key : found),
    ""
  );

  return (
    <main id="main" className="chat-view">
      <header className="chat-header">
        <div className="chat-header-title">
          <h1 className="chat-title">{detail.title || "Untitled session"}</h1>
          <div className="chat-meta">
            <span
              className={
                "badge-ui" + (channel === "telegram" ? " badge-ui-brand" : "")
              }
            >
              <span className="badge-ui-dot" aria-hidden="true"></span>
              {channelLabel(channel)}
            </span>
            <span className="chat-turns">{turnCountText(turnCount)}</span>
            {readOnly ? (
              <span className="chat-holder">
                Read-only · {holderText(holder, holderPid)}
              </span>
            ) : null}
          </div>
        </div>
        <div className="chat-actions">
          <button
            type="button"
            className="button-ui button-ui-tertiary"
            disabled={busySession}
            title={
              busySession
                ? "Compact is available on an idle session."
                : "Compact this session"
            }
            onClick={compactSession}
          >
            Compact
          </button>
          <button
            type="button"
            className="button-ui button-ui-secondary"
            disabled={busySession}
            title={
              busySession
                ? "Fork is available on an idle session."
                : "Fork at the last completed turn"
            }
            onClick={forkSession}
          >
            Fork
          </button>
        </div>
      </header>

      <div className="chat-notices">
        {readOnly ? (
          <p className="notice notice-warn">
            Read-only: {holderText(holder, holderPid)} holds the session lease.
            Sending, stopping, and settings are disabled.
          </p>
        ) : null}
        {streamState === "reconnecting" ? (
          <p className="notice notice-warn">
            The live stream is reconnecting. History still updates from the event
            log.
          </p>
        ) : null}
        {notice ? (
          <p className={"notice notice-" + notice.tone}>{notice.text}</p>
        ) : null}
        {queued ? (
          <p className="notice notice-warn">
            Message queued. It will be claimed at the next step.
          </p>
        ) : null}
        {actionError ? (
          <ErrorNotice message={actionError} details={actionDetails} />
        ) : null}
      </div>

      <div className="chat-messages" ref={messagesRef} onScroll={handleScroll}>
        <div className="chat-thread">
          {thread.map((item) =>
            item.kind === "user" ? (
              <UserBubble
                key={item.key}
                text={item.text}
                continue={item.continue === true}
              />
            ) : (
              <TurnBlock
                key={item.key}
                turn={item.turn}
                agent={agent}
                team={team}
                rootEvents={turnEvents.get(item.turn.id) || []}
                childEvents={childEvents}
                childLive={childLive}
                onLoadChildren={loadChildEvents}
                waitingForInput={waitingForInput}
                showStatusCard={item.key === lastTurnKey && !turnRunning}
                onContinue={continueLastTurn}
                continueBusy={continueBusy}
                continueError={continueError}
                streamingText={
                  item.turn.running &&
                  (liveTurnId ? item.turn.id === liveTurnId : true)
                    ? streamText
                    : ""
                }
                thinking={
                  item.turn.running &&
                  (liveTurnId ? item.turn.id === liveTurnId : true) &&
                  liveThinking
                }
              />
            )
          )}
          {pendingText ? (
            <UserBubble key="pending" text={pendingText} />
          ) : null}
          {!hasThread ? (
            <div className="chat-empty">
              <p className="chat-empty-title">No messages yet</p>
              <p className="chat-empty-text">
                Send the first message to {agent} with the composer below.
              </p>
            </div>
          ) : null}
        </div>
      </div>

      <div className="chat-composer">
        <div className="chat-composer-inner">
          <div className="chat-slot">
            {activeApproval ? (
              <ApprovalCard
                key={activeApproval.request_id}
                approval={activeApproval}
                workspace={detail.workspace || ""}
                busy={Boolean(activeApproval.deciding)}
                onAnswer={handleApprovalAnswer}
                onExpire={handleApprovalExpired}
                onDismiss={handleApprovalDismiss}
              />
            ) : null}
            {activeQuestion ? (
              <QuestionCard
                key={"question-" + activeQuestion.request_id}
                question={activeQuestion}
                responder={questionResponder}
                responderPid={questionResponderPid}
                readOnly={questionReadOnly}
                busy={questionBusy}
                onSubmit={handleQuestionSubmit}
                onSkipAll={handleQuestionSkipAll}
                onExpire={handleQuestionExpired}
              />
            ) : null}
          </div>
          <Composer
            agent={agent}
            team={team}
            model={detail.model || ""}
            policy={detail.policy || "ask"}
            agents={agents}
            draft={draft}
            readOnly={readOnly}
            questionOpen={Boolean(activeQuestion)}
            turnRunning={turnRunning}
            cancelling={cancelling}
            sending={sending}
            onDraft={setDraft}
            onSend={sendMessage}
            onStop={cancelTurn}
            onAgent={changeAgent}
            onModel={changeModel}
            onPolicy={changePolicy}
          />
        </div>
      </div>
    </main>
  );
}
