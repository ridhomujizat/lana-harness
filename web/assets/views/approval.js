import {
  html,
  useState,
  useEffect,
  useMemo,
  useRef,
} from "/assets/vendor/preact-htm.js";
import { cut } from "/assets/format.js";

/* Inline approval card of REQ-WEB-039. It lives in its own slot above the
   composer so spec #11 can add a question card next to it later. */

const OUTCOME_WORDS = {
  allowed_once: "Approved once",
  allowed_session: "Approved for this session",
  rejected: "Denied",
  unavailable: "Approval expired",
  cancelled: "Approval cancelled",
  approval_closed: "Approval closed",
};

export function approvalResultWords(outcome) {
  return OUTCOME_WORDS[outcome] || (outcome ? String(outcome) : "");
}

function outcomeTone(outcome) {
  if (outcome === "allowed_once" || outcome === "allowed_session") {
    return " is-allowed";
  }
  if (outcome === "rejected" || outcome === "unavailable" || outcome === "cancelled") {
    return " is-denied";
  }
  return "";
}

let cardCounter = 0;

function remainingMinutes(expiresAt, now) {
  const expires = Number(expiresAt) || 0;
  if (!expires) {
    return null;
  }
  return Math.max(0, Math.ceil((expires - now) / 60000));
}

function expiryText(expiresAt, now) {
  const minutes = remainingMinutes(expiresAt, now);
  if (minutes === null) {
    return "";
  }
  if (minutes <= 0) {
    return "expired";
  }
  return "expires in " + minutes + " min";
}

function shortenWorkspace(value) {
  const text = String(value || "");
  return cut(text || "not set", 80);
}

export function ApprovalCard({
  approval,
  workspace = "",
  busy = false,
  onAnswer,
  onExpire,
  onDismiss,
}) {
  const [now, setNow] = useState(() => Date.now());
  const cardRef = useRef(null);
  const expiredRef = useRef(false);
  const titleId = useMemo(() => {
    cardCounter += 1;
    return "approval-title-" + cardCounter;
  }, []);

  const decided = approval.decided || "";
  const expiresAt = Number(approval.expires_at) || 0;

  /* Move focus to the card when it appears (REQ-WEB-039). The card itself is
     the stop; Tab then reaches Deny, Allow for this session, and Allow once. */
  useEffect(() => {
    const node = cardRef.current;
    if (node) {
      node.focus();
    }
  }, []);

  useEffect(() => {
    if (decided) {
      return undefined;
    }
    const timer = setInterval(() => setNow(Date.now()), 15000);
    return () => clearInterval(timer);
  }, [decided]);

  useEffect(() => {
    if (decided || !expiresAt) {
      return undefined;
    }
    const delay = Math.max(0, expiresAt - Date.now());
    const timer = setTimeout(() => {
      if (expiredRef.current) {
        return;
      }
      expiredRef.current = true;
      onExpire(approval);
    }, delay);
    return () => clearTimeout(timer);
  }, [approval, decided, expiresAt, onExpire]);

  const tool = approval.tool || "tool";
  const risk = approval.risk || "unknown";
  const argument = String(approval.argument || "");
  const isCommand = tool === "bash" || tool === "terminal";
  const command = argument ? (isCommand ? "$ " + argument : argument) : "(no argument)";
  const expiry = expiryText(expiresAt, now);
  const meta = expiry ? risk + " · " + expiry : risk;

  return html`<section
    ref=${cardRef}
    class="approval-card"
    tabindex="-1"
    role="group"
    aria-labelledby=${titleId}
  >
    <div class="approval-head">
      <img class="icon-20 approval-lock" src="/assets/icons/lock.svg" alt="" aria-hidden="true" />
      <h2 class="approval-title" id=${titleId}>${"Allow " + tool + "?"}</h2>
      <p class="approval-meta">${decided ? risk : meta}</p>
      <span class="approval-spacer"></span>
      ${decided
        ? html`<p class=${"approval-outcome" + outcomeTone(decided)} role="status">
            ${approvalResultWords(decided)}
          </p>
          <button type="button" class="button-ui button-ui-tertiary" onClick=${() => onDismiss(approval)}>
            Dismiss
          </button>`
        : html`<div class="approval-actions">
            <button
              type="button"
              class="button-ui button-ui-tertiary"
              disabled=${busy}
              onClick=${() => onAnswer(approval, "rejected")}
            >
              Deny
            </button>
            <button
              type="button"
              class="button-ui button-ui-secondary"
              disabled=${busy}
              onClick=${() => onAnswer(approval, "allowed_session")}
            >
              Allow for this session
            </button>
            <button
              type="button"
              class="button-ui button-ui-primary"
              disabled=${busy}
              onClick=${() => onAnswer(approval, "allowed_once")}
            >
              Allow once
            </button>
          </div>`}
    </div>
    <hr class="approval-rule" />
    <div class="approval-code">
      <p class="approval-command">${command}</p>
      <p class="approval-cwd">${"cwd  " + shortenWorkspace(workspace)}</p>
    </div>
    ${approval.error ? html`<p class="approval-error">${approval.error}</p>` : null}
  </section>`;
}
