import { useEffect, useId, useRef, useState } from "react";
import type { ApprovalEvent, ApprovalProof } from "../api/approvals";
import { sendApprovalCode } from "../api/approvals";
import { ApiError, errorMessage } from "../api/client";
import { cut } from "../lib/format";

/* Inline approval card of REQ-WEB-039. It lives in its own slot above the
   composer so spec #11 can add a question card next to it later.

   Spec 63 REQ-HLF-007: a hardline request shows the rule and reason in a danger
   strip, never offers "allow for session", and, at the block tier, runs only
   after the owner types the Lana password or a Telegram code. */

const OUTCOME_WORDS: Record<string, string> = {
  allowed_once: "Approved once",
  allowed_verified: "Approved with confirmation",
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
  if (
    outcome === "allowed_once" ||
    outcome === "allowed_session" ||
    outcome === "allowed_verified"
  ) {
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

/* API-HLF-005: the step-up error the card shows under the inputs. */
function stepUpErrorText(code: string, message: string): string {
  switch (code) {
    case "invalid_password":
      return "Wrong password.";
    case "invalid_code":
      return "Wrong or expired code.";
    case "too_many_attempts":
      return "Too many tries. Wait a few minutes.";
    case "telegram_unavailable":
      return "Telegram is not connected.";
    case "step_up_required":
      return "Enter your password or a Telegram code.";
    default:
      return message || "Could not confirm. Try again.";
  }
}

/* The answer handler of a surface may return an ApprovalAnswerResult; a
   handler that returns nothing leaves the error to the parent. */
function answerFailure(
  value: unknown
): { code: string; message: string } | null {
  if (!value || typeof value !== "object") {
    return null;
  }
  const record = value as { ok?: unknown; code?: unknown; message?: unknown };
  if (record.ok !== false) {
    return null;
  }
  return {
    code: typeof record.code === "string" ? record.code : "",
    message: typeof record.message === "string" ? record.message : "",
  };
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
  onAnswer?: (
    approval: ApprovalEvent,
    outcome: string,
    proof?: ApprovalProof
  ) => unknown;
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
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [localError, setLocalError] = useState("");
  const [sendingCode, setSendingCode] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const cardRef = useRef<HTMLElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const expiredRef = useRef(false);
  const cardId = useId().replace(/[^A-Za-z0-9_-]/g, "");
  const titleId = "approval-title-" + cardId;
  const passwordId = "approval-password-" + cardId;
  const codeId = "approval-code-" + cardId;

  const decided = approval.decided || "";
  const expiresAt = Number(approval.expires_at) || 0;

  const hardline = approval.hardline;
  const isBlock = Boolean(hardline && hardline.tier === "block");
  const stepUp =
    hardline && hardline.step_up ? hardline.step_up : "password";
  const showCode = isBlock && stepUp === "password_or_telegram";
  const allowDisabled = busy || (isBlock && !password && !code);

  /* Move focus into the card when it appears (REQ-WEB-039); a block card opens
     on its password field (REQ-HLF-007). */
  useEffect(() => {
    if (isBlock) {
      passwordRef.current?.focus();
      return;
    }
    cardRef.current?.focus();
  }, [isBlock]);

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

  /* A sent code disables the button for 60 seconds (REQ-HLF-007). */
  useEffect(() => {
    if (cooldown <= 0) {
      return undefined;
    }
    const timer = setInterval(
      () => setCooldown((current) => Math.max(0, current - 1)),
      1000
    );
    return () => clearInterval(timer);
  }, [cooldown]);

  function proofFor(outcome: string): ApprovalProof | undefined {
    if (outcome !== "allowed_once" || !isBlock) {
      return undefined;
    }
    if (password) {
      return { password };
    }
    if (stepUp === "password_or_telegram" && code) {
      return { code };
    }
    return undefined;
  }

  async function answer(outcome: string) {
    if (busy) {
      return;
    }
    const proof = proofFor(outcome);
    setLocalError("");
    let result: unknown;
    try {
      result = await onAnswer?.(approval, outcome, proof);
    } catch (failure) {
      result = {
        ok: false,
        code: failure instanceof ApiError ? failure.code : "",
        message: errorMessage(failure),
      };
    }
    const failure = isBlock ? answerFailure(result) : null;
    if (failure) {
      setLocalError(stepUpErrorText(failure.code, failure.message));
      setPassword("");
      setCode("");
    }
  }

  async function sendCode() {
    if (busy || sendingCode || cooldown > 0) {
      return;
    }
    setSendingCode(true);
    setLocalError("");
    try {
      await sendApprovalCode(approval.request_id);
      setCooldown(60);
    } catch (failure) {
      setLocalError(
        stepUpErrorText(
          failure instanceof ApiError ? failure.code : "",
          errorMessage(failure)
        )
      );
    } finally {
      setSendingCode(false);
    }
  }

  function onFieldKeyDown(event: {
    key: string;
    preventDefault: () => void;
  }): void {
    if (event.key !== "Enter") {
      return;
    }
    event.preventDefault();
    if (!allowDisabled) {
      void answer("allowed_once");
    }
  }

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
        ) : isBlock ? null : (
          <div className="approval-actions">
            <button
              type="button"
              className="button-ui button-ui-tertiary"
              disabled={busy}
              onClick={() => void answer("rejected")}
            >
              Deny
            </button>
            {hardline ? null : (
              <button
                type="button"
                className="button-ui button-ui-secondary"
                disabled={busy}
                onClick={() => void answer("allowed_session")}
              >
                Allow for this session
              </button>
            )}
            <button
              type="button"
              className="button-ui button-ui-primary"
              disabled={allowDisabled}
              onClick={() => void answer("allowed_once")}
            >
              Allow once
            </button>
          </div>
        )}
      </div>
      {hardline ? (
        <p className="approval-hardline">
          <span className="approval-hardline-rule-group">
            <span className="approval-hardline-rule">{hardline.rule}</span>
            <span className="approval-hardline-sep" aria-hidden="true">
              ·
            </span>
          </span>
          <span className="approval-hardline-reason">{hardline.reason}</span>
        </p>
      ) : null}
      <hr className="approval-rule" />
      <div className="approval-code">
        <p className="approval-command">{command}</p>
        <p className="approval-cwd">{"cwd  " + shortenWorkspace(workspace)}</p>
      </div>
      {isBlock ? (
        <div className="approval-stepup">
          <p className="approval-stepup-hint">
            {showCode
              ? "This command needs your Lana password or a Telegram code to run."
              : "This command needs your Lana password to run."}
          </p>
          <div className="approval-stepup-field">
            <label className="label" htmlFor={passwordId}>
              Lana password
            </label>
            <input
              ref={passwordRef}
              id={passwordId}
              className="input"
              type="password"
              autoComplete="current-password"
              value={password}
              disabled={busy}
              onChange={(event) => setPassword(event.target.value)}
              onKeyDown={onFieldKeyDown}
            />
          </div>
          {showCode ? (
            <>
              <div
                className="approval-stepup-or"
                data-testid="stepup-or"
                aria-hidden="true"
              >
                <span className="approval-stepup-or-line" />
                <span className="approval-stepup-or-text">or</span>
                <span className="approval-stepup-or-line" />
              </div>
              <div className="approval-stepup-code-row">
                <div className="approval-stepup-field">
                  <label className="label" htmlFor={codeId}>
                    Telegram code
                  </label>
                  <input
                    id={codeId}
                    className="input approval-stepup-code"
                    type="text"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    maxLength={6}
                    value={code}
                    disabled={busy}
                    onChange={(event) =>
                      setCode(
                        event.target.value.replace(/\D/g, "").slice(0, 6)
                      )
                    }
                    onKeyDown={onFieldKeyDown}
                  />
                </div>
                <button
                  type="button"
                  className="button-ui button-ui-secondary"
                  disabled={busy || sendingCode || cooldown > 0}
                  onClick={() => void sendCode()}
                >
                  {cooldown > 0 ? "Send code (" + cooldown + "s)" : "Send code"}
                </button>
              </div>
            </>
          ) : null}
          {cooldown > 0 ? (
            <p className="approval-stepup-note" role="status">
              {"Code sent to Telegram."}
            </p>
          ) : null}
          {localError ? (
            <p className="approval-error" role="alert">
              {localError}
            </p>
          ) : null}
          {decided ? null : (
            <div className="approval-stepup-actions">
              <button
                type="button"
                className="button-ui button-ui-tertiary"
                disabled={busy}
                onClick={() => void answer("rejected")}
              >
                Deny
              </button>
              <button
                type="button"
                className="button-ui button-ui-primary"
                disabled={allowDisabled}
                onClick={() => void answer("allowed_once")}
              >
                Allow once
              </button>
            </div>
          )}
        </div>
      ) : null}
      {approval.error ? (
        <p className="approval-error" role="alert">
          {approval.error}
        </p>
      ) : null}
    </section>
  );
}
