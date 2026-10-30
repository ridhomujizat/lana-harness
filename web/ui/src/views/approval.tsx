import { useEffect, useId, useRef, useState } from "react";
import type { ApprovalEvent } from "../api/approvals";
import { cut } from "../lib/format";

/* Inline approval card of REQ-WEB-039. It lives in its own slot above the
   composer so spec #11 can add a question card next to it later. */

const OUTCOME_WORDS: Record<string, string> = {
  allowed_once: "Approved once",
  allowed_session: "Approved for this session",
  rejected: "Denied",
  unavailable: "Approval expired",
  cancelled: "Approval cancelled",
  approval_closed: "Approval closed",
};

export function approvalResultWords(outcome: string): string {
  return OUTCOME_WORDS[outcome] || (outcome ? String(outcome) : "");
}

function outcomeTone(outcome: string): string {
  if (outcome === "allowed_once" || outcome === "allowed_session") {
    return " is-allowed";
  }
  if (
    outcome === "rejected" ||
    outcome === "unavailable" ||
    outcome === "cancelled"
  ) {
    return " is-denied";
  }
  return "";
}

function remainingMinutes(expiresAt: number, now: number): number | null {
  const expires = Number(expiresAt) || 0;
  if (!expires) {
    return null;
  }
  return Math.max(0, Math.ceil((expires - now) / 60000));
}

function expiryText(expiresAt: number, now: number): string {
  const minutes = remainingMinutes(expiresAt, now);
  if (minutes === null) {
    return "";
  }
  if (minutes <= 0) {
    return "expired";
  }
  return "expires in " + minutes + " min";
}

function shortenWorkspace(value: unknown): string {
  const text = String(value || "");
  return cut(text || "not set", 80);
}

/* The approval row carries the view-only decision fields the session adds on
   top of the SSE payload. */
export interface ApprovalItem extends ApprovalEvent {
  decided?: string;
  deciding?: boolean;
  error?: string;
}

export interface ApprovalCardProps {
  approval: ApprovalItem;
  workspace?: string;
  busy?: boolean;
  onAnswer?: (approval: ApprovalEvent, outcome: string) => unknown;
  onExpire?: (approval: ApprovalEvent) => void;
  onDismiss?: () => void;
}

export function ApprovalCard({
  approval,
  workspace = "",
  busy = false,
  onAnswer,
  onExpire,
  onDismiss,
}: ApprovalCardProps) {
  const [now, setNow] = useState(() => Date.now());
  const cardRef = useRef<HTMLElement>(null);
  const expiredRef = useRef(false);
  const titleId =
    "approval-title-" + useId().replace(/[^A-Za-z0-9_-]/g, "");

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
      onExpire?.(approval);
    }, delay);
    return () => clearTimeout(timer);
  }, [approval, decided, expiresAt, onExpire]);

  const tool = approval.tool || "tool";
  const risk = approval.risk || "unknown";
  const argument = String(approval.argument || "");
  const isCommand = tool === "bash" || tool === "terminal";
  const command = argument
    ? isCommand
      ? "$ " + argument
      : argument
    : "(no argument)";
  const expiry = expiryText(expiresAt, now);
  const meta = expiry ? risk + " · " + expiry : risk;

  return (
    <section
      ref={cardRef}
      className="approval-card"
      tabIndex={-1}
      role="group"
      aria-labelledby={titleId}
    >
      <div className="approval-head">
        <img
          className="icon-20 approval-lock"
          src="/assets/icons/lock.svg"
          alt=""
          aria-hidden="true"
        />
        <h2 className="approval-title" id={titleId}>
          {"Allow " + tool + "?"}
        </h2>
        <p className="approval-meta">{decided ? risk : meta}</p>
        <span className="approval-spacer"></span>
        {decided ? (
          <>
            <p
              className={"approval-outcome" + outcomeTone(decided)}
              role="status"
            >
              {approvalResultWords(decided)}
            </p>
            <button
              type="button"
              className="button-ui button-ui-tertiary"
              onClick={() => onDismiss?.()}
            >
              Dismiss
            </button>
          </>
        ) : (
          <div className="approval-actions">
            <button
              type="button"
              className="button-ui button-ui-tertiary"
              disabled={busy}
              onClick={() => onAnswer?.(approval, "rejected")}
            >
              Deny
            </button>
            <button
              type="button"
              className="button-ui button-ui-secondary"
              disabled={busy}
              onClick={() => onAnswer?.(approval, "allowed_session")}
            >
              Allow for this session
            </button>
            <button
              type="button"
              className="button-ui button-ui-primary"
              disabled={busy}
              onClick={() => onAnswer?.(approval, "allowed_once")}
            >
              Allow once
            </button>
          </div>
        )}
      </div>
      <hr className="approval-rule" />
      <div className="approval-code">
        <p className="approval-command">{command}</p>
        <p className="approval-cwd">{"cwd  " + shortenWorkspace(workspace)}</p>
      </div>
      {approval.error ? (
        <p className="approval-error">{approval.error}</p>
      ) : null}
    </section>
  );
}
