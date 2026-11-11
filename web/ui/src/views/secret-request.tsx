import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import { cancelSecretRequest, saveSecretRequest } from "../api/secret-requests";
import type { SecretRequestEvent } from "../api/secret-requests";
import type { SessionEvent } from "../api/sessions";
import { Button, IconButton, Input } from "../ui/gr8r";
import { Ic } from "../ui/icons";

/* The secure secret input card of spec 53 (REQ-SEC-011 to REQ-SEC-014). An
   agent's secret_request opens it in the approval/question slot above the
   composer. The owner types the value into a masked field; Save posts it once
   to POST /v1/secret-requests/{id}, which writes Settings > Secrets. The value
   lives only in this card's input state and is cleared on save; the agent gets
   the outcome only. Once resolved, the card collapses to "Saved NAME",
   "Cancelled", or "Expired". */

export type SecretOutcome = "" | "saved" | "cancelled" | "expired" | "unavailable";

export interface SecretRequestItem extends SecretRequestEvent {
  outcome: SecretOutcome;
}

export interface SecretSubmitResult {
  ok: boolean;
  message?: string;
}

function stringOf(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function payloadOf(event: SessionEvent): Record<string, unknown> {
  return event && event.payload && typeof event.payload === "object"
    ? (event.payload as Record<string, unknown>)
    : {};
}

function normalizeOutcome(value: string): SecretOutcome {
  if (value === "saved" || value === "cancelled" || value === "expired") {
    return value;
  }
  return value ? "unavailable" : "";
}

function collectEvents(
  events: SessionEvent[] | null | undefined,
  extra?: SessionEvent[] | Record<string, SessionEvent[]> | null
): SessionEvent[] {
  const all: SessionEvent[] = [];
  for (const event of events || []) {
    if (event) {
      all.push(event);
    }
  }
  if (Array.isArray(extra)) {
    for (const event of extra) {
      if (event) {
        all.push(event);
      }
    }
  } else if (extra && typeof extra === "object") {
    for (const key of Object.keys(extra)) {
      for (const event of extra[key] || []) {
        if (event) {
          all.push(event);
        }
      }
    }
  }
  return all;
}

/* Every secret request in the log, oldest first: secret/requested with the
   outcome of its secret/resolved ("" while open). extra may be the child
   events of a team session. */
export function secretRequestsFromEvents(
  events: SessionEvent[] | null | undefined,
  extra?: SessionEvent[] | Record<string, SessionEvent[]> | null
): SecretRequestItem[] {
  const all = collectEvents(events, extra);
  const outcomes = new Map<string, SecretOutcome>();
  for (const event of all) {
    if (event.kind !== "secret/resolved") {
      continue;
    }
    const payload = payloadOf(event);
    const requestId = stringOf(payload.request_id);
    if (requestId) {
      outcomes.set(requestId, normalizeOutcome(stringOf(payload.outcome)) || "unavailable");
    }
  }
  const items: { seq: number; item: SecretRequestItem }[] = [];
  for (const event of all) {
    if (event.kind !== "secret/requested") {
      continue;
    }
    const payload = payloadOf(event);
    const requestId = stringOf(payload.request_id);
    if (!requestId) {
      continue;
    }
    items.push({
      seq: Number(event.seq) || 0,
      item: {
        request_id: requestId,
        call_id: stringOf(payload.call_id),
        turn_id: stringOf(event.turn_id),
        name: stringOf(payload.name),
        description: stringOf(payload.description),
        link: stringOf(payload.link),
        replaces: Boolean(payload.replaces),
        expires_at: Number(payload.expires_at) || 0,
        outcome: outcomes.get(requestId) || "",
      },
    });
  }
  items.sort((left, right) => left.seq - right.seq);
  return items.map((entry) => entry.item);
}

export interface SecretRequestState {
  /* The card to show: the open request, or the last one this view saw open
     until it is dismissed. */
  request: SecretRequestItem | null;
  /* The stream's secret_request handler. Stable across renders. */
  receive: (event: SecretRequestEvent) => void;
  save: (request: SecretRequestEvent, value: string) => Promise<SecretSubmitResult>;
  cancel: (request: SecretRequestEvent) => Promise<SecretSubmitResult>;
  expire: (request: SecretRequestEvent) => void;
  dismiss: () => void;
}

export interface SecretStream {
  live: SecretRequestEvent[];
  receive: (event: SecretRequestEvent) => void;
}

/* useSecretStream holds the secret_request stream events of a view. A view
   whose log is derived after its stream effect declares it first and passes
   it to useSecretRequest. */
export function useSecretStream(): SecretStream {
  const [live, setLive] = useState<SecretRequestEvent[]>([]);
  const receive = useCallback((event: SecretRequestEvent) => {
    if (!event || !event.request_id) {
      return;
    }
    setLive((current) =>
      current.some((item) => item.request_id === event.request_id)
        ? current
        : current.concat([event])
    );
  }, []);
  return useMemo(() => ({ live, receive }), [live, receive]);
}

/* useSecretRequest keeps the secret request card state of one chat view. The
   log is the source of truth (secret/requested, secret/resolved); the
   secret_request stream event shows a card before the log poll has it, and
   the local outcome collapses the card as soon as the owner answers. */
export function useSecretRequest(
  events: SessionEvent[] | null | undefined,
  extra?: SessionEvent[] | Record<string, SessionEvent[]> | null,
  stream?: SecretStream
): SecretRequestState {
  const own = useSecretStream();
  const { live, receive } = stream || own;
  const [local, setLocal] = useState<Record<string, SecretOutcome>>({});
  const [watched, setWatched] = useState<string[]>([]);
  const [dismissed, setDismissed] = useState<string[]>([]);

  const logged = useMemo(() => secretRequestsFromEvents(events, extra), [events, extra]);

  const request = useMemo(() => {
    const merged: SecretRequestItem[] = logged.slice();
    for (const event of live) {
      if (!merged.some((item) => item.request_id === event.request_id)) {
        merged.push({ ...event, outcome: "" });
      }
    }
    const withLocal = merged.map((item): SecretRequestItem => {
      const settled = local[item.request_id];
      return settled && !item.outcome ? { ...item, outcome: settled } : item;
    });
    for (let index = withLocal.length - 1; index >= 0; index--) {
      const item = withLocal[index];
      if (item && !item.outcome) {
        return item;
      }
    }
    for (let index = withLocal.length - 1; index >= 0; index--) {
      const item = withLocal[index];
      if (item && watched.includes(item.request_id) && !dismissed.includes(item.request_id)) {
        return item;
      }
    }
    return null;
  }, [logged, live, local, watched, dismissed]);

  const openId = request && !request.outcome ? request.request_id : "";
  useEffect(() => {
    if (!openId) {
      return;
    }
    // eslint-disable-next-line react-hooks/set-state-in-effect -- a request seen open stays as a collapsed card once it resolves
    setWatched((current) => (current.includes(openId) ? current : current.concat([openId])));
  }, [openId]);

  const settle = useCallback((requestId: string, outcome: SecretOutcome) => {
    setLocal((current) => ({ ...current, [requestId]: outcome }));
  }, []);

  const answer = useCallback(
    async (
      request: SecretRequestEvent,
      send: () => Promise<unknown>,
      outcome: SecretOutcome
    ): Promise<SecretSubmitResult> => {
      try {
        await send();
        settle(request.request_id, outcome);
        return { ok: true };
      } catch (failure) {
        if (failure instanceof ApiError && (failure.status === 409 || failure.status === 404)) {
          settle(request.request_id, "expired");
          return { ok: true };
        }
        if (failure instanceof ApiError && failure.status === 422) {
          const detail = Array.isArray(failure.details) ? failure.details[0] : null;
          const message =
            detail && typeof detail === "object" && "message" in detail
              ? String((detail as { message: unknown }).message)
              : "Check the value.";
          return { ok: false, message: message.charAt(0).toUpperCase() + message.slice(1) + "." };
        }
        return { ok: false, message: "Could not save. " + errorMessage(failure) };
      }
    },
    [settle]
  );

  const save = useCallback(
    (request: SecretRequestEvent, value: string) =>
      answer(request, () => saveSecretRequest(request.request_id, value), "saved"),
    [answer]
  );
  const cancel = useCallback(
    (request: SecretRequestEvent) =>
      answer(request, () => cancelSecretRequest(request.request_id), "cancelled"),
    [answer]
  );
  const expire = useCallback(
    (request: SecretRequestEvent) => settle(request.request_id, "expired"),
    [settle]
  );
  const dismiss = useCallback(() => {
    if (request) {
      const id = request.request_id;
      setDismissed((current) => (current.includes(id) ? current : current.concat([id])));
    }
  }, [request]);

  return { request, receive, save, cancel, expire, dismiss };
}

export interface SecretRequestCardProps {
  request: SecretRequestItem;
  /* The asking agent's name, for the title. */
  agentLabel?: string;
  onSave: (request: SecretRequestEvent, value: string) => Promise<SecretSubmitResult>;
  onCancel: (request: SecretRequestEvent) => Promise<SecretSubmitResult>;
  onExpire?: (request: SecretRequestEvent) => void;
  onDismiss?: () => void;
}

function safeLink(link: string | undefined): string {
  if (!link) {
    return "";
  }
  try {
    const parsed = new URL(link);
    return parsed.protocol === "https:" || parsed.protocol === "http:" ? parsed.href : "";
  } catch {
    return "";
  }
}

const OUTCOME_TEXT: Record<Exclude<SecretOutcome, "">, string> = {
  saved: "Saved",
  cancelled: "Cancelled",
  expired: "Expired",
  unavailable: "Not available",
};

const OUTCOME_ICON: Record<Exclude<SecretOutcome, "">, string> = {
  saved: "check",
  cancelled: "x",
  expired: "clock",
  unavailable: "x",
};

export function SecretRequestCard({
  request,
  agentLabel,
  onSave,
  onCancel,
  onExpire,
  onDismiss,
}: SecretRequestCardProps) {
  const cardId = useId().replace(/[^A-Za-z0-9_-]/g, "");
  const titleId = "secret-title-" + cardId;
  const inputId = "secret-value-" + cardId;
  const warnId = "secret-warn-" + cardId;
  const [value, setValue] = useState("");
  const [shown, setShown] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const sentRef = useRef(false);
  const expiredRef = useRef(false);

  useEffect(() => {
    const expiresAt = Number(request.expires_at) || 0;
    if (!expiresAt || request.outcome || !onExpire) {
      return undefined;
    }
    expiredRef.current = false;
    const fire = () => {
      if (expiredRef.current || sentRef.current) {
        return;
      }
      expiredRef.current = true;
      onExpire(request);
    };
    const timer = setTimeout(fire, Math.max(0, expiresAt - Date.now()));
    return () => clearTimeout(timer);
  }, [request, onExpire]);

  /* The value never outlives the open card. */
  useEffect(() => {
    if (request.outcome) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- clear the typed value as soon as the card closes
      setValue("");
    }
  }, [request.outcome]);

  if (request.outcome) {
    const outcome = request.outcome;
    const label =
      outcome === "saved" ? "Saved " + request.name : OUTCOME_TEXT[outcome];
    return (
      <section
        className={"secret-card secret-card-done is-" + outcome}
        role="status"
        aria-label={label}
      >
        <span className="secret-done-icon" aria-hidden="true">
          <Ic name={OUTCOME_ICON[outcome]} size={14} />
        </span>
        <span className="secret-done-text">
          {outcome === "saved" ? (
            <>
              Saved <code className="secret-name">{request.name}</code>
            </>
          ) : (
            <>
              {label}
              <span className="secret-done-name">{request.name}</span>
            </>
          )}
        </span>
        {onDismiss ? (
          <IconButton
            className="secret-done-close"
            icon="x"
            size="xs"
            label="Dismiss"
            onClick={onDismiss}
          />
        ) : null}
      </section>
    );
  }

  const link = safeLink(request.link);
  const working = pending;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (working || sentRef.current) {
      return;
    }
    if (!value) {
      setError("Type the value first.");
      return;
    }
    setPending(true);
    setError("");
    sentRef.current = true;
    const result = await onSave(request, value);
    if (result.ok) {
      setValue("");
    } else {
      sentRef.current = false;
      setError(result.message || "Could not save. Try again.");
    }
    setPending(false);
  }

  async function cancel() {
    if (working || sentRef.current) {
      return;
    }
    setPending(true);
    setError("");
    sentRef.current = true;
    const result = await onCancel(request);
    if (!result.ok) {
      sentRef.current = false;
      setError(result.message || "Could not cancel. Try again.");
    }
    setPending(false);
  }

  return (
    <section className="secret-card" role="group" aria-labelledby={titleId}>
      <form className="secret-form" onSubmit={(event) => void submit(event)} autoComplete="off">
        <div className="secret-head">
          <span className="secret-icon" aria-hidden="true">
            <Ic name="key-round" size={16} />
          </span>
          <div className="secret-title-wrap">
            <p className="secret-eyebrow">
              {agentLabel ? agentLabel + " needs a secret" : "A secret is needed"}
            </p>
            <h2 className="secret-title" id={titleId}>
              {request.replaces ? "Replace " : "Add "}
              <code className="secret-name">{request.name}</code>
            </h2>
          </div>
        </div>
        {request.description ? (
          <p className="secret-description">{request.description}</p>
        ) : null}
        {link ? (
          <a className="secret-link" href={link} target="_blank" rel="noopener noreferrer">
            <Ic name="external-link" size={13} />
            <span>Where to get it</span>
          </a>
        ) : null}
        {request.replaces ? (
          <div className="alert warn secret-warn" id={warnId} role="note">
            <Ic name="triangle-alert" size={15} />
            <div>
              <b>{request.name}</b> already has a value. Saving replaces the current value.
            </div>
          </div>
        ) : null}
        <label className="label secret-label" htmlFor={inputId}>
          Value
        </label>
        <span className="secret-input">
          <Input
            id={inputId}
            type={shown ? "text" : "password"}
            name={"secret-request-" + cardId}
            autoComplete="new-password"
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            data-1p-ignore="true"
            data-lpignore="true"
            placeholder="Paste the value"
            value={value}
            disabled={working}
            aria-describedby={request.replaces ? warnId : undefined}
            onChange={(event) => setValue(event.target.value)}
          />
          <IconButton
            className="secret-eye"
            icon={shown ? "eye-off" : "eye"}
            size="sm"
            on={shown}
            label={shown ? "Hide value" : "Show value"}
            disabled={working}
            onClick={() => setShown((current) => !current)}
          />
        </span>
        <p className="secret-hint">
          Saved to Settings › Secrets. The agent never sees the value.
        </p>
        {error ? (
          <p className="secret-error" role="alert">
            {error}
          </p>
        ) : null}
        <div className="secret-actions">
          <Button type="button" variant="secondary" size="sm" disabled={working} onClick={() => void cancel()}>
            Cancel
          </Button>
          <Button type="submit" variant={request.replaces ? "danger" : "primary"} size="sm" loading={working} disabled={working}>
            {request.replaces ? "Replace value" : "Save"}
          </Button>
        </div>
      </form>
    </section>
  );
}
