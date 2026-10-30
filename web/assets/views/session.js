import {
  html,
  useState,
  useEffect,
  useRef,
  useMemo,
  useCallback,
} from "/assets/vendor/preact-htm.js";
import { api, openStream, errorMessage, ApiError } from "/assets/api.js";
import { useAnnounce, Dialog, ErrorNotice, Loading } from "/assets/ui.js";
import { holderText, stopReasonWords } from "/assets/format.js";
import {
  buildTurns,
  TurnBlock,
  UserBubble,
} from "/assets/views/turn.js";
import { ApprovalCard, approvalResultWords } from "/assets/views/approval.js";
import { QuestionCard, openQuestionFromEvents } from "/assets/views/question.js";
import { answerQuestion, skipAllQuestions } from "/assets/api/questions.js";
import { isTeam, rosterNames } from "/assets/views/agent-common.js";

/* Chat view of the sessions screen (REQ-WEB-038, REQ-WEB-039, REQ-WEB-040).
   It replaces the rev 1 history list and keeps the turn block and the approval
   card in their own components for the team block and question card later. */

const POLICY_OPTIONS = [
  { value: "ask", label: "Ask", hint: "Ask before risky tools." },
  { value: "trust", label: "Trust", hint: "Run tools without asking." },
  { value: "never", label: "Never", hint: "Never ask; block unapproved tools." },
];

function Icon({ name, className = "icon-20" }) {
  return html`<img
    class=${className}
    src=${"/assets/icons/" + name + ".svg"}
    alt=""
    aria-hidden="true"
  />`;
}

function channelLabel(value) {
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

function policyLabel(value) {
  for (const option of POLICY_OPTIONS) {
    if (option.value === value) {
      return option.label;
    }
  }
  return value ? value : "Ask";
}

function shortModel(value) {
  const text = String(value || "");
  const parts = text.split(":");
  return parts.length > 1 ? parts.slice(1).join(":") : text;
}

function turnCountText(count) {
  const value = Number(count) || 0;
  return value + (value === 1 ? " turn" : " turns");
}

function turnEndAnnouncement(reason, error) {
  if (reason === "cancelled") {
    return "Turn stopped: cancelled.";
  }
  if (reason === "error") {
    const message = error && error.message ? " " + error.message : "";
    return "Turn failed." + message;
  }
  if (reason === "max_steps") {
    return "Turn stopped: maximum steps reached.";
  }
  return "Turn finished: " + stopReasonWords(reason) + ".";
}

function approvalAnnouncement(outcome) {
  if (outcome === "unavailable") {
    return "Approval expired. The tool will not run.";
  }
  if (outcome === "cancelled") {
    return "Approval cancelled.";
  }
  return "Approval decided: " + approvalResultWords(outcome) + ".";
}

function questionClosedWords(payload) {
  if (payload && payload.outcome === "answered") {
    return "Already answered elsewhere";
  }
  if (payload && payload.reason === "timeout") {
    return "Question expired";
  }
  if (payload && (payload.reason === "cancelled" || payload.reason === "interrupted")) {
    return "Question cancelled";
  }
  return "Already answered elsewhere";
}

function menuFocusables(node) {
  if (!node) {
    return [];
  }
  return Array.from(node.querySelectorAll("button:not([disabled]), input:not([disabled])"));
}

/* Small menu popover for the composer. Escape closes it and returns focus to
   the trigger; arrows and Tab move through the items. */
function Popover({ open, onClose, triggerRef, label, align = "left", children }) {
  const ref = useRef(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const node = ref.current;
    const items = menuFocusables(node);
    if (items.length > 0) {
      items[0].focus();
    } else if (node) {
      node.focus();
    }
    function handlePointer(event) {
      const trigger = triggerRef && triggerRef.current;
      if (node && node.contains(event.target)) {
        return;
      }
      if (trigger && trigger.contains(event.target)) {
        return;
      }
      closeRef.current();
    }
    document.addEventListener("mousedown", handlePointer);
    node.addEventListener("focusout", handleFocusOut);
    return () => {
      document.removeEventListener("mousedown", handlePointer);
      node.removeEventListener("focusout", handleFocusOut);
    };
  }, [open, triggerRef]);

  if (!open) {
    return null;
  }

  function handleKeyDown(event) {
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
    if (target && (target.nodeName === "INPUT" || target.nodeName === "TEXTAREA")) {
      return;
    }
    const items = menuFocusables(ref.current);
    if (items.length === 0) {
      return;
    }
    event.preventDefault();
    const index = items.indexOf(document.activeElement);
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
    items[next].focus();
  }

  function handleFocusOut(event) {
    if (!ref.current) {
      return;
    }
    if (event.relatedTarget && ref.current.contains(event.relatedTarget)) {
      return;
    }
    if (!event.relatedTarget) {
      return;
    }
    closeRef.current();
  }

  return html`<div
    ref=${ref}
    class=${"composer-menu" + (align === "right" ? " is-right" : "")}
    role="menu"
    aria-label=${label}
    tabindex="-1"
    onKeyDown=${handleKeyDown}
  >
    ${children}
  </div>`;
}

function AgentMenuContent({ agents, current, disabled, onPick }) {
  return html`<p class="menu-heading">Switch agent or team</p>
    ${agents.length === 0
      ? html`<p class="menu-empty">No agents configured.</p>`
      : agents.map((entry) => {
          const team = isTeam(entry);
          return html`<button
            key=${entry.name}
            type="button"
            role="menuitemradio"
            class="menu-item"
            aria-checked=${entry.name === current}
            disabled=${disabled}
            onClick=${() => onPick(entry.name)}
          >
            <span class="menu-item-text">
              <span>${entry.name}</span>
              <span class="menu-item-meta">
                ${team
                  ? "Team · " + rosterNames(entry).length + " agents"
                  : entry.model || "agent default"}
              </span>
            </span>
            ${entry.name === current
              ? html`<${Icon} name="check" className="icon-16 menu-check" />`
              : null}
          </button>`;
        })}`;
}

function ModelMenuContent({ current, agentModel, onPick }) {
  const [custom, setCustom] = useState("");
  const options = [];
  if (current) {
    options.push({ value: current, label: shortModel(current), meta: "Current model" });
  }
  if (agentModel && agentModel !== current) {
    options.push({ value: agentModel, label: shortModel(agentModel), meta: "Agent default" });
  }

  function submitCustom(event) {
    event.preventDefault();
    const value = custom.trim();
    if (value) {
      onPick(value);
    }
  }

  return html`<p class="menu-heading">Model</p>
    ${options.length === 0
      ? html`<p class="menu-empty">No model is set yet.</p>`
      : options.map((option) =>
          html`<button
            key=${option.value}
            type="button"
            role="menuitemradio"
            class="menu-item"
            aria-checked=${option.value === current}
            onClick=${() => onPick(option.value)}
          >
            <span class="menu-item-text">
              <span>${option.label}</span>
              <span class="menu-item-meta">${option.meta + " · " + option.value}</span>
            </span>
            ${option.value === current
              ? html`<${Icon} name="check" className="icon-16 menu-check" />`
              : null}
          </button>`
        )}
    <form class="menu-custom" onSubmit=${submitCustom}>
      <input
        type="text"
        class="menu-custom-input"
        aria-label="Custom model as provider:model"
        placeholder="provider:model"
        value=${custom}
        onInput=${(event) => setCustom(event.target.value)}
      />
      <button type="submit" class="menu-set" disabled=${!custom.trim()}>
        Set
      </button>
    </form>`;
}

function PolicyMenuContent({ current, onPick }) {
  return html`<p class="menu-heading">Policy</p>
    ${POLICY_OPTIONS.map((option) =>
      html`<button
        key=${option.value}
        type="button"
        role="menuitemradio"
        class="menu-item"
        aria-checked=${option.value === current}
        onClick=${() => onPick(option.value)}
      >
        <span class="menu-item-text">
          <span>${option.label}</span>
          <span class="menu-item-meta">${option.hint}</span>
        </span>
        ${option.value === current
          ? html`<${Icon} name="check" className="icon-16 menu-check" />`
          : null}
      </button>`
    )}
    <p class="menu-note">Choosing Trust asks for confirmation first.</p>`;
}

function TrustDialog({ busy, onCancel, onConfirm }) {
  return html`<${Dialog} title="Set policy to trust?" onClose=${onCancel} children=${html`
    <p>
      Policy <strong>trust</strong> lets tools in this session run without asking
      for approval. Only choose it for an agent and session you trust.
    </p>
    <div class="dialog-actions">
      <button type="button" data-autofocus disabled=${busy} onClick=${onCancel}>Cancel</button>
      <button type="button" class="danger" disabled=${busy} onClick=${onConfirm}>
        ${busy ? "Saving…" : "Set policy to trust"}
      </button>
    </div>
  `} />`;
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
}) {
  const [menu, setMenu] = useState("");
  const [trustOpen, setTrustOpen] = useState(false);
  const [trustBusy, setTrustBusy] = useState(false);
  const plusRef = useRef(null);
  const modelRef = useRef(null);
  const policyRef = useRef(null);

  const agentEntry = agents.find((entry) => entry.name === agent) || null;
  const agentModel = agentEntry ? agentEntry.model : "";
  const modelValue = model || agentModel;
  const teamSession = Boolean(team) || isTeam(agentEntry);
  const messageTarget =
    teamSession && team && team.name ? String(team.name) : agent || "agent";
  const busySession = readOnly || turnRunning;

  function closeMenu(kind) {
    setMenu("");
    const refs = { plus: plusRef, model: modelRef, policy: policyRef };
    const target = refs[kind];
    if (target && target.current) {
      window.setTimeout(() => {
        if (target.current) {
          target.current.focus();
        }
      }, 0);
    }
  }

  function pickAgent(name) {
    setMenu("");
    if (plusRef.current) {
      window.setTimeout(() => plusRef.current && plusRef.current.focus(), 0);
    }
    onAgent(name);
  }

  function pickModel(value) {
    setMenu("");
    if (modelRef.current) {
      window.setTimeout(() => modelRef.current && modelRef.current.focus(), 0);
    }
    onModel(value);
  }

  function pickPolicy(value) {
    setMenu("");
    if (value === "trust") {
      setTrustOpen(true);
      return;
    }
    if (policyRef.current) {
      window.setTimeout(() => policyRef.current && policyRef.current.focus(), 0);
    }
    onPolicy(value, false);
  }

  function cancelTrust() {
    setTrustOpen(false);
    if (policyRef.current) {
      window.setTimeout(() => policyRef.current && policyRef.current.focus(), 0);
    }
  }

  async function confirmTrust() {
    setTrustBusy(true);
    await onPolicy("trust", true);
    setTrustBusy(false);
    setTrustOpen(false);
    if (policyRef.current) {
      window.setTimeout(() => policyRef.current && policyRef.current.focus(), 0);
    }
  }

  function composerKeyDown(event) {
    if (readOnly || questionOpen || turnRunning) {
      return;
    }
    if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
      event.preventDefault();
      onSend();
    }
  }

  return html`<div class="composer">
    ${questionOpen
      ? html`<p class="sr-only" id="composer-question-note">Answer the question above</p>`
      : null}
    <div class="composer-box">
      <textarea
        class="composer-input"
        rows="1"
        aria-label=${"Message " + messageTarget}
        aria-describedby=${questionOpen ? "composer-question-note" : undefined}
        placeholder=${questionOpen ? "Answer the question above" : "Message " + messageTarget + "…"}
        value=${draft}
        disabled=${readOnly || questionOpen}
        onInput=${(event) => onDraft(event.target.value)}
        onKeyDown=${composerKeyDown}
      ></textarea>
      <div class="composer-bar">
        <span class="composer-control">
          <button
            ref=${plusRef}
            type="button"
            class="composer-plus"
            aria-label="Composer actions"
            aria-haspopup="menu"
            aria-expanded=${menu === "plus" || menu === "agent"}
            disabled=${readOnly}
            onClick=${() => setMenu(menu === "plus" || menu === "agent" ? "" : "plus")}
          >
            <${Icon} name="plus" />
          </button>
          <${Popover}
            open=${menu === "plus"}
            onClose=${() => closeMenu("plus")}
            triggerRef=${plusRef}
            label="Composer actions"
          >
            <p class="menu-heading">Composer</p>
            <button
              type="button"
              role="menuitem"
              class="menu-item"
              disabled=${busySession}
              onClick=${() => setMenu("agent")}
            >
              <span class="menu-item-text">
                <span>Switch agent or team</span>
                <span class="menu-item-meta">${agent || "no agent"}</span>
              </span>
            </button>
            <button type="button" role="menuitem" class="menu-item" disabled>
              <span class="menu-item-text">
                <span>Attach file</span>
                <span class="menu-item-meta">coming soon</span>
              </span>
            </button>
          </${Popover}>
          <${Popover}
            open=${menu === "agent"}
            onClose=${() => closeMenu("plus")}
            triggerRef=${plusRef}
            label="Switch agent or team"
          >
            <${AgentMenuContent}
              agents=${agents}
              current=${agent}
              disabled=${busySession}
              onPick=${pickAgent}
            />
          </${Popover}>
        </span>

        <span class="composer-control">
          ${teamSession
            ? html`<button
                ref=${modelRef}
                type="button"
                class="composer-pill is-team-model"
                aria-label="Each agent in the team uses its own model."
                title="Each agent in the team uses its own model."
                disabled=${true}
              >
                <span class="composer-pill-label">per agent</span>
                <${Icon} name="chevron-down" />
              </button>`
            : html`<button
                ref=${modelRef}
                type="button"
                class="composer-pill"
                aria-label=${"Model: " + (modelValue || "not set")}
                aria-haspopup="menu"
                aria-expanded=${menu === "model"}
                title=${modelValue || "No model set"}
                disabled=${busySession}
                onClick=${() => setMenu(menu === "model" ? "" : "model")}
              >
                <span class="composer-pill-label">${shortModel(modelValue) || "Model"}</span>
                <${Icon} name="chevron-down" />
              </button>
              <${Popover}
                open=${menu === "model"}
                onClose=${() => closeMenu("model")}
                triggerRef=${modelRef}
                label="Model"
              >
                <${ModelMenuContent}
                  current=${modelValue}
                  agentModel=${agentModel}
                  onPick=${pickModel}
                />
              </${Popover}>`}
        </span>

        <span class="composer-control">
          <button
            ref=${policyRef}
            type="button"
            class="composer-pill"
            aria-label=${"Policy: " + policyLabel(policy)}
            aria-haspopup="menu"
            aria-expanded=${menu === "policy"}
            disabled=${busySession}
            onClick=${() => setMenu(menu === "policy" ? "" : "policy")}
          >
            <${Icon} name="lock" />
            <span class="composer-pill-label">${policyLabel(policy)}</span>
            <${Icon} name="chevron-down" />
          </button>
          <${Popover}
            open=${menu === "policy"}
            onClose=${() => closeMenu("policy")}
            triggerRef=${policyRef}
            label="Policy"
          >
            <${PolicyMenuContent} current=${policy} onPick=${pickPolicy} />
          </${Popover}>
        </span>

        <span class="composer-spacer"></span>

        ${turnRunning && !readOnly
          ? html`<button
              type="button"
              class="composer-send composer-stop"
              aria-label="Stop the running turn"
              title="Stop the running turn"
              disabled=${cancelling}
              onClick=${onStop}
            >
              <span class="stop-square" aria-hidden="true"></span>
            </button>`
          : html`<button
              type="button"
              class="composer-send"
              aria-label="Send message"
              title="Send message"
              disabled=${readOnly || questionOpen || sending || !draft.trim()}
              onClick=${onSend}
            >
              <${Icon} name="arrow-up" className="icon-20 send-icon" />
            </button>`}
      </div>
    </div>
    ${trustOpen
      ? html`<${TrustDialog}
          busy=${trustBusy}
          onCancel=${cancelTrust}
          onConfirm=${confirmTrust}
        />`
      : null}
  </div>`;
}

export function SessionPage({ id }) {
  const announce = useAnnounce();
  const [detail, setDetail] = useState(null);
  const [events, setEvents] = useState([]);
  const [agents, setAgents] = useState([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [loadDetails, setLoadDetails] = useState([]);
  const [streamState, setStreamState] = useState("connecting");
  const [streamReady, setStreamReady] = useState(false);
  const [turnRunning, setTurnRunning] = useState(false);
  const [liveTurnId, setLiveTurnId] = useState("");
  const [liveStartedAt, setLiveStartedAt] = useState(0);
  const [streamText, setStreamText] = useState("");
  const [holder, setHolder] = useState("none");
  const [holderPid, setHolderPid] = useState(null);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [pendingText, setPendingText] = useState("");
  const [queued, setQueued] = useState(false);
  const [notice, setNotice] = useState(null);
  const [actionError, setActionError] = useState("");
  const [actionDetails, setActionDetails] = useState([]);
  const [approvals, setApprovals] = useState([]);
  const [questionBusy, setQuestionBusy] = useState(false);
  const [closedQuestionIds, setClosedQuestionIds] = useState([]);
  const [liveQuestion, setLiveQuestion] = useState(null);
  const [childEvents, setChildEvents] = useState({});
  const [childLive, setChildLive] = useState({});

  const lastSeqRef = useRef(0);
  const childSeenRef = useRef(new Set());
  const sessionIdRef = useRef(id);
  sessionIdRef.current = id;
  const pendingRef = useRef("");
  const decidedRef = useRef(new Set());
  const openApprovalIdsRef = useRef(new Set());
  const questionNotifiedRef = useRef(new Set());
  const closedQuestionIdsRef = useRef(new Set());
  const questionSubmittingRef = useRef("");
  const openQuestionIdRef = useRef("");
  const endedRef = useRef(new Set());
  const refreshRef = useRef(null);
  const streamRef = useRef(null);
  const lastHolderRef = useRef(null);
  const lastStreamStateRef = useRef("connecting");
  const lastRunningRef = useRef(false);
  const messagesRef = useRef(null);
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
  const activeQuestion =
    openQuestion ||
    (liveQuestion && !closedQuestionIds.includes(liveQuestion.request_id)
      ? liveQuestion
      : null);
  openQuestionIdRef.current = activeQuestion ? activeQuestion.request_id : "";

  const clearError = useCallback(() => {
    setActionError("");
    setActionDetails([]);
  }, []);

  const showNotice = useCallback((text, tone = "info") => {
    setNotice({ text, tone });
  }, []);

  const reportError = useCallback(
    (failure, prefix) => {
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

  const refreshDetail = useCallback(async () => {
    try {
      const next = await api.getSession(id);
      setDetail(next);
      setHolder(next.in_use || "none");
      setHolderPid(next.holder_pid || null);
      setTurnRunning(Boolean(next.turn_running));
      lastRunningRef.current = Boolean(next.turn_running);
      return next;
    } catch (failure) {
      return null;
    }
  }, [id]);

  refreshRef.current = refreshDetail;

  const markApprovalDecided = useCallback(
    (requestId, outcome, announcement) => {
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

  const closeQuestion = useCallback((requestId) => {
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
    (requestId, words) => {
      if (!requestId || questionNotifiedRef.current.has(requestId)) {
        return;
      }
      questionNotifiedRef.current.add(requestId);
      announce(words);
    },
    [announce]
  );

  const submitQuestion = useCallback(
    async (request, send) => {
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
    (request, body) =>
      submitQuestion(request, (requestId) => answerQuestion(requestId, body)),
    [submitQuestion]
  );

  const handleQuestionSkipAll = useCallback(
    (request) => submitQuestion(request, (requestId) => skipAllQuestions(requestId)),
    [submitQuestion]
  );

  const handleQuestionExpired = useCallback(
    (request) => {
      const requestId = request && request.request_id;
      closeQuestion(requestId);
      announceQuestionResult(requestId, "Question expired");
    },
    [announceQuestionResult, closeQuestion]
  );

  const finishTurn = useCallback(
    (turnId, reason, error) => {
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
      setQueued(false);
      setLiveTurnId("");
      setLiveStartedAt(0);
      openApprovalIdsRef.current.clear();
      setApprovals((current) =>
        current.map((item) =>
          item.decided
            ? item
            : { ...item, decided: reason === "cancelled" ? "cancelled" : "unavailable", deciding: false }
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
  const loadChildEvents = useCallback(async (requests) => {
    if (!Array.isArray(requests) || requests.length === 0) {
      return;
    }
    const loadId = sessionIdRef.current;
    const loaded = {};
    await Promise.all(
      requests.map(async (request) => {
        const childId = request && request.id ? String(request.id) : "";
        if (!childId) {
          return;
        }
        try {
          const page = await api.listEvents(childId, 0);
          loaded[childId] = (page.events || []).map((event) => ({
            ...event,
            child_session_id: childId,
            agent: (request && request.agent) || "",
          }));
        } catch (failure) {
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
        const bySeq = new Map();
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
  }, []);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setLoadError("");
    setLoadDetails([]);
    setStreamReady(false);
    setApprovals([]);
    setStreamText("");
    setQueued(false);
    setPendingText("");
    setNotice(null);
    setActionError("");
    setActionDetails([]);
    setCancelling(false);
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

    Promise.all([api.getSession(id), api.listEvents(id, 0)])
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
      .catch((failure) => {
        if (!alive) {
          return;
        }
        setLoadError(errorMessage(failure));
        setLoadDetails(failure instanceof ApiError ? failure.details : []);
        announce("Could not load the session. " + errorMessage(failure));
        setLoading(false);
      });

    api
      .listAgents()
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
          typeof event.child_session_id === "string" ? event.child_session_id : "";
        if (childId) {
          /* Child items never touch the root history or the root turn state.
             A reconnect replays them, so drop a seen (child, seq) pair. */
          const seenKey = childId + "|" + String(event.seq);
          if (childSeenRef.current.has(seenKey)) {
            return;
          }
          childSeenRef.current.add(seenKey);
          setChildEvents((current) => {
            const list = Array.isArray(current[childId]) ? current[childId] : [];
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
          return current.concat([event]).sort((left, right) => left.seq - right.seq);
        });
        lastSeqRef.current = Math.max(lastSeqRef.current, event.seq);
        const payload =
          event.payload && typeof event.payload === "object" ? event.payload : {};

        if (event.kind === "user/message" && pendingRef.current) {
          pendingRef.current = "";
          setPendingText("");
          setQueued(false);
        }
        if (event.kind === "assistant/message" || event.kind === "tool/call") {
          setStreamText("");
        }
        if (event.kind === "approval/decided") {
          markApprovalDecided(
            payload.request_id,
            payload.outcome || "unavailable",
            approvalAnnouncement(payload.outcome || "unavailable")
          );
        }
        if (event.kind === "question/answered") {
          const requestId = payload.request_id || "";
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
          finishTurn(payload.turn_id, payload.reason, payload.error);
        }
      },
      onFrame: (frame) => {
        const childId =
          typeof frame.child_session_id === "string" ? frame.child_session_id : "";
        if (childId) {
          /* A child frame updates only that child's live thread. The root's
             turn id and streaming text stay untouched (API-TEAM-011). */
          setChildLive((current) => {
            const previous =
              current[childId] || {
                agent: frame.agent || "",
                turnId: "",
                text: "",
                textAt: 0,
                tools: {},
              };
            const turnId = frame.turn_id || previous.turnId || "";
            const fresh = Boolean(
              turnId && previous.turnId && turnId !== previous.turnId
            );
            const entry = {
              agent: frame.agent || previous.agent || "",
              turnId,
              text: fresh ? "" : previous.text || "",
              textAt: fresh ? 0 : previous.textAt || 0,
              tools: fresh ? {} : previous.tools ? { ...previous.tools } : {},
            };
            if (frame.kind === "text_delta" && frame.text) {
              entry.text += frame.text;
              entry.textAt = Date.now();
            } else if (frame.kind === "tool_call" || frame.kind === "tool_result") {
              const callId = String(frame.call_id || "");
              if (!callId) {
                return current;
              }
              const tool =
                entry.tools[callId] || {
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
                  name: frame.name || tool.name,
                  input: frame.input === undefined ? tool.input : frame.input,
                  status: tool.status || "running",
                  at: tool.at || Date.now(),
                };
              } else {
                entry.tools[callId] = {
                  ...tool,
                  status: frame.status || "success",
                  output: frame.output === undefined ? tool.output : frame.output,
                  at: tool.at || Date.now(),
                };
              }
            }
            return { ...current, [childId]: entry };
          });
          return;
        }
        if (frame.turn_id) {
          setLiveTurnId(frame.turn_id);
          if (!lastRunningRef.current) {
            setLiveStartedAt(Date.now());
          }
        }
        if (frame.kind === "text_delta" && frame.text) {
          setStreamText((current) => current + frame.text);
        }
        if (frame.kind === "tool_call" || frame.kind === "tool_result") {
          setStreamText("");
        }
      },
      onStatus: (status) => {
        const next = status.holder || "none";
        const previous = lastHolderRef.current;
        lastHolderRef.current = next;
        setHolder(next);
        setHolderPid(status.pid || null);
        const running = Boolean(status.turn_running) || openApprovalIdsRef.current.size > 0;
        if (running && !lastRunningRef.current) {
          announce("Turn running.");
        }
        lastRunningRef.current = running;
        setTurnRunning(running);
        if (next === "other" && previous !== "other") {
          announce(
            status.pid
              ? "This session is now read-only: another process holds it (PID " + status.pid + ")."
              : "This session is now read-only: another process holds it."
          );
        } else if (previous === "other" && next !== "other") {
          announce("The session is no longer held by another process.");
        }
      },
      onTurn: (turn) => {
        finishTurn(turn.turn_id, turn.stop_reason, null);
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
            call_id: question.call_id || "",
            expires_at: Number(question.expires_at) || 0,
            turn_id: question.turn_id || "",
            questions: Array.isArray(question.questions) ? question.questions : [],
          };
        });
      },
      onApprovalClosed: (payload) => {
        const requestId = payload && payload.request_id;
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
        api.authSession().catch(() => {});
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
    const map = new Map();
    for (const event of events) {
      const turnId =
        event.turn_id || (event.payload && event.payload.turn_id) || "";
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
      const result = await api.sendMessage(id, text);
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
      }
      lastRunningRef.current = true;
      setTurnRunning(true);
    } catch (failure) {
      pendingRef.current = "";
      setPendingText("");
      setDraft((current) => current || text);
      reportError(failure, "Could not send the message.");
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
      const result = await api.cancelTurn(id);
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
      const result = await api.compactSession(id);
      const text =
        "Compacted from " + result.tokens_before + " to " + result.tokens_after + " tokens.";
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
      const created = await api.forkSession(id);
      announce("Session forked.");
      window.location.hash = "#/sessions/" + encodeURIComponent(created.id);
    } catch (failure) {
      reportError(failure, "Could not fork the session.");
    }
  }

  const patch = useCallback(
    async (update, successText) => {
      clearError();
      try {
        const updated = await api.patchSession(id, update);
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
    (name) => patch({ agent: name }, "Agent changed to " + name + "."),
    [patch]
  );

  const changeModel = useCallback(
    (value) => {
      /* A team session never changes one model: each agent uses its own. */
      const currentAgent = agents.find(
        (entry) => entry.name === (detail && detail.agent)
      );
      if ((detail && detail.team) || isTeam(currentAgent)) {
        return Promise.resolve(false);
      }
      return patch({ model: value }, "Model changed to " + value + ".");
    },
    [agents, detail, patch]
  );

  const changePolicy = useCallback(
    (value, confirmTrust) =>
      patch(
        confirmTrust ? { policy: value, confirm_trust: true } : { policy: value },
        "Policy changed to " + value + "."
      ),
    [patch]
  );

  const handleApprovalAnswer = useCallback(
    async (approval, outcome) => {
      clearError();
      setApprovals((current) =>
        current.map((item) =>
          item.request_id === approval.request_id
            ? { ...item, deciding: true, error: "" }
            : item
        )
      );
      try {
        await api.answerApproval(approval.request_id, outcome);
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
    (approval) => {
      markApprovalDecided(
        approval.request_id,
        "unavailable",
        "Approval expired. The tool will not run."
      );
    },
    [markApprovalDecided]
  );

  const handleApprovalDismiss = useCallback((approval) => {
    setApprovals((current) => current.filter((item) => !item.decided));
    window.setTimeout(() => {
      const input = document.querySelector(".composer-input");
      if (input) {
        input.focus();
      }
    }, 0);
  }, []);

  if (loading) {
    return html`<main id="main" class="chat-view chat-state">
      <${Loading} label="Loading the session." />
    </main>`;
  }

  if (loadError) {
    return html`<main id="main" class="chat-view chat-state">
      <h1 class="chat-title">Session</h1>
      <${ErrorNotice} message=${loadError} details=${loadDetails} />
      <p><a href="#/sessions">Back to sessions</a></p>
    </main>`;
  }

  if (!detail) {
    return html`<main id="main" class="chat-view chat-state">
      <p class="muted">Session not found.</p>
      <p><a href="#/sessions">Back to sessions</a></p>
    </main>`;
  }

  const agent = detail.agent || "agent";
  const team = detail.team || null;
  const activeApproval =
    approvals.find((approval) => !approval.decided) ||
    (approvals.length > 0 ? approvals[approvals.length - 1] : null);
  const channel = String(detail.channel || "");
  /* The responder of a question is the session's channel (REQ-ASK-015). The
     named question SSE event is web-only, so a web session gets the
     interactive card and every other channel gets the read-only one. */
  const questionResponder = channel || "web";
  const questionReadOnly = questionResponder !== "web";
  const questionResponderPid = questionResponder === "cli" ? holderPid : null;
  const waitingForInput = Boolean(activeQuestion) || detail.status === "waiting_input";
  const turnCount = Number(detail.turn_count) || 0;
  const hasThread = thread.length > 0 || pendingText !== "";

  return html`<main id="main" class="chat-view">
    <header class="chat-header">
      <div class="chat-header-title">
        <h1 class="chat-title">${detail.title || "Untitled session"}</h1>
        <div class="chat-meta">
          <span class=${"badge-ui" + (channel === "telegram" ? " badge-ui-brand" : "")}>
            <span class="badge-ui-dot" aria-hidden="true"></span>
            ${channelLabel(channel)}
          </span>
          <span class="chat-turns">${turnCountText(turnCount)}</span>
          ${readOnly
            ? html`<span class="chat-holder">Read-only · ${holderText(holder, holderPid)}</span>`
            : null}
        </div>
      </div>
      <div class="chat-actions">
        <button
          type="button"
          class="button-ui button-ui-tertiary"
          disabled=${busySession}
          title=${busySession ? "Compact is available on an idle session." : "Compact this session"}
          onClick=${compactSession}
        >
          Compact
        </button>
        <button
          type="button"
          class="button-ui button-ui-secondary"
          disabled=${busySession}
          title=${busySession ? "Fork is available on an idle session." : "Fork at the last completed turn"}
          onClick=${forkSession}
        >
          Fork
        </button>
      </div>
    </header>

    <div class="chat-notices">
      ${readOnly
        ? html`<p class="notice notice-warn">
            Read-only: ${holderText(holder, holderPid)} holds the session lease. Sending,
            stopping, and settings are disabled.
          </p>`
        : null}
      ${streamState === "reconnecting"
        ? html`<p class="notice notice-warn">
            The live stream is reconnecting. History still updates from the event log.
          </p>`
        : null}
      ${notice ? html`<p class=${"notice notice-" + notice.tone}>${notice.text}</p>` : null}
      ${queued
        ? html`<p class="notice notice-warn">
            Message queued. It will be claimed at the next step.
          </p>`
        : null}
      ${actionError
        ? html`<${ErrorNotice} message=${actionError} details=${actionDetails} />`
        : null}
    </div>

    <div class="chat-messages" ref=${messagesRef} onScroll=${handleScroll}>
      <div class="chat-thread">
        ${thread.map((item) =>
          item.kind === "user"
            ? html`<${UserBubble} key=${item.key} text=${item.text} />`
            : html`<${TurnBlock}
                key=${item.key}
                turn=${item.turn}
                agent=${agent}
                team=${team}
                rootEvents=${turnEvents.get(item.turn.id) || []}
                childEvents=${childEvents}
                childLive=${childLive}
                onLoadChildren=${loadChildEvents}
                waitingForInput=${waitingForInput}
                streamingText=${item.turn.running &&
                (liveTurnId ? item.turn.id === liveTurnId : true)
                  ? streamText
                  : ""}
              />`
        )}
        ${pendingText
          ? html`<${UserBubble} key="pending" text=${pendingText} />`
          : null}
        ${!hasThread
          ? html`<div class="chat-empty">
              <p class="chat-empty-title">No messages yet</p>
              <p class="chat-empty-text">
                Send the first message to ${agent} with the composer below.
              </p>
            </div>`
          : null}
      </div>
    </div>

    <div class="chat-composer">
      <div class="chat-composer-inner">
        <div class="chat-slot">
          ${activeApproval
            ? html`<${ApprovalCard}
                key=${activeApproval.request_id}
                approval=${activeApproval}
                workspace=${detail.workspace || ""}
                busy=${Boolean(activeApproval.deciding)}
                onAnswer=${handleApprovalAnswer}
                onExpire=${handleApprovalExpired}
                onDismiss=${handleApprovalDismiss}
              />`
            : null}
          ${activeQuestion
            ? html`<${QuestionCard}
                key=${"question-" + activeQuestion.request_id}
                question=${activeQuestion}
                responder=${questionResponder}
                responderPid=${questionResponderPid}
                readOnly=${questionReadOnly}
                busy=${questionBusy}
                onSubmit=${handleQuestionSubmit}
                onSkipAll=${handleQuestionSkipAll}
                onExpire=${handleQuestionExpired}
              />`
            : null}
        </div>
        <${Composer}
          agent=${agent}
          team=${team}
          model=${detail.model || ""}
          policy=${detail.policy || "ask"}
          agents=${agents}
          draft=${draft}
          readOnly=${readOnly}
          questionOpen=${Boolean(activeQuestion)}
          turnRunning=${turnRunning}
          cancelling=${cancelling}
          sending=${sending}
          onDraft=${setDraft}
          onSend=${sendMessage}
          onStop=${cancelTurn}
          onAgent=${changeAgent}
          onModel=${changeModel}
          onPolicy=${changePolicy}
        />
      </div>
    </div>
  </main>`;
}
