import type { ApprovalEvent } from "./approvals";
import type { QuestionEvent } from "./questions";
import type { SessionEvent } from "./sessions";

/* The shared fetch client. Paths, headers, the 401 handler, and the error
   envelope follow /assets/api.js exactly. */

const state = {
  csrf: "",
};

type UnauthorizedHandler = ((error: ApiError) => void) | null;

let unauthorizedHandler: UnauthorizedHandler = null;

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: unknown[];

  constructor(
    status: number,
    code: string,
    message: string,
    details?: unknown[]
  ) {
    super(message || "The request failed.");
    this.name = "ApiError";
    this.status = status;
    this.code = code || "internal_error";
    this.details = Array.isArray(details) ? details : [];
  }

  get pid(): number | null {
    for (const detail of this.details) {
      if (isRecord(detail) && typeof detail.pid === "number") {
        return detail.pid;
      }
    }
    return null;
  }
}

export function setCSRF(value: unknown): void {
  state.csrf = typeof value === "string" ? value : "";
}

export function setUnauthorizedHandler(handler: unknown): void {
  unauthorizedHandler = typeof handler === "function" ? (handler as UnauthorizedHandler) : null;
}

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 401) {
      return "Please log in again.";
    }
    return error.message || "The request failed.";
  }
  return "Could not reach the daemon. Check that it is still running.";
}

export function isErrorCode(error: unknown, code: string): boolean {
  return error instanceof ApiError && error.code === code;
}

/* API-TFB-008: the daemon refuses a new turn while it drains. */
export const DAEMON_DRAINING_TEXT = "lana is restarting. Try again in a moment.";

export function isDaemonDraining(error: unknown): boolean {
  return error instanceof ApiError && error.status === 503 && error.code === "daemon_draining";
}

export function queryString(params?: object): string {
  const search = new URLSearchParams();
  const source = (params || {}) as Record<string, unknown>;
  for (const key of Object.keys(source)) {
    const value = source[key];
    if (value === undefined || value === null || value === "") {
      continue;
    }
    search.set(key, String(value));
  }
  const text = search.toString();
  return text ? "?" + text : "";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

export async function request<T>(
  method: string,
  path: string,
  body?: unknown
): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const options: RequestInit = {
    method,
    headers,
    credentials: "same-origin",
  };
  if (body !== undefined) {
    /* API-ATT-001: a FormData body is sent as multipart, so the browser sets
       the boundary and the client must not set a content type. */
    if (typeof FormData !== "undefined" && body instanceof FormData) {
      options.body = body;
    } else {
      headers["Content-Type"] = "application/json";
      options.body = JSON.stringify(body);
    }
  }
  if (method !== "GET" && method !== "HEAD" && state.csrf) {
    headers["X-Lana-CSRF"] = state.csrf;
  }

  let response: Response;
  try {
    response = await fetch(path, options);
  } catch {
    throw new ApiError(0, "network_error", "Could not reach the daemon.", []);
  }

  const text = await response.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }

  if (!response.ok) {
    /* Two error shapes: the envelope {"error":{"code","message","details"}}
       and the flat router shape {"error":"in_use","message","issues"} of
       docs/specs/37-llm-router/api-contract.md (API-RTR-006, API-RTR-007). */
    const flat =
      isRecord(data) && typeof data.error === "string" ? data : null;
    const envelope =
      isRecord(data) && isRecord(data.error) ? data.error : flat;
    const rawCode = flat ? flat.error : envelope ? envelope.code : undefined;
    const code =
      typeof rawCode === "string" && rawCode
        ? rawCode
        : "http_" + response.status;
    const message =
      envelope && typeof envelope.message === "string"
        ? envelope.message
        : text ||
          "The request failed with status " + response.status + ".";
    const details =
      envelope && Array.isArray(envelope.details)
        ? envelope.details
        : envelope && Array.isArray(envelope.issues)
          ? envelope.issues
          : [];
    const error = new ApiError(response.status, code, message, details);
    if (response.status === 401 && unauthorizedHandler) {
      unauthorizedHandler(error);
    }
    throw error;
  }
  return data as T;
}

export type StreamFrame = Record<string, unknown>;

export type StreamState = "connected" | "reconnecting";

export interface StreamHandlers {
  fromSeq?: number;
  onEvent?: (payload: SessionEvent) => void;
  onFrame?: (payload: StreamFrame) => void;
  onStatus?: (payload: StreamFrame) => void;
  onTurn?: (payload: StreamFrame) => void;
  onApproval?: (payload: ApprovalEvent) => void;
  onQuestion?: (payload: QuestionEvent) => void;
  onApprovalClosed?: (payload: StreamFrame) => void;
  onShutdown?: (payload: StreamFrame) => void;
  onState?: (state: StreamState) => void;
  onUnauthorized?: () => void;
  onError?: (status: number) => void;
}

export interface SSEStream {
  close: () => void;
}

interface StreamStateRef {
  lastSeq: number;
}

function dispatchBlock(
  block: string,
  handlers: StreamHandlers,
  stateRef: StreamStateRef
): void {
  const lines = block.split("\n");
  let eventName = "message";
  let eventId = "";
  const dataLines: string[] = [];

  for (const rawLine of lines) {
    const line = rawLine.endsWith("\r") ? rawLine.slice(0, -1) : rawLine;
    if (!line || line.startsWith(":")) {
      continue;
    }
    const colon = line.indexOf(":");
    const field = colon >= 0 ? line.slice(0, colon) : line;
    let value = colon >= 0 ? line.slice(colon + 1) : "";
    if (value.startsWith(" ")) {
      value = value.slice(1);
    }
    if (field === "event") {
      eventName = value;
    } else if (field === "id") {
      eventId = value;
    } else if (field === "data") {
      dataLines.push(value);
    }
  }

  if (dataLines.length === 0) {
    return;
  }

  let payload: unknown;
  try {
    payload = JSON.parse(dataLines.join("\n"));
  } catch {
    return;
  }

  if (eventName === "event") {
    if (eventId) {
      const seq = Number(eventId);
      if (Number.isFinite(seq) && seq > stateRef.lastSeq) {
        stateRef.lastSeq = seq;
      }
    }
    if (handlers.onEvent) {
      handlers.onEvent(payload as SessionEvent);
    }
    return;
  }
  if (eventName === "frame") {
    if (handlers.onFrame) {
      handlers.onFrame(payload as StreamFrame);
    }
    return;
  }
  if (eventName === "status") {
    if (handlers.onStatus) {
      handlers.onStatus(payload as StreamFrame);
    }
    return;
  }
  if (eventName === "turn") {
    if (handlers.onTurn) {
      handlers.onTurn(payload as StreamFrame);
    }
    return;
  }
  if (eventName === "approval") {
    if (handlers.onApproval) {
      handlers.onApproval(payload as ApprovalEvent);
    }
    return;
  }
  if (eventName === "question") {
    if (handlers.onQuestion) {
      handlers.onQuestion(payload as QuestionEvent);
    }
    return;
  }
  if (eventName === "approval_closed") {
    if (handlers.onApprovalClosed) {
      handlers.onApprovalClosed(payload as StreamFrame);
    }
    return;
  }
  if (eventName === "shutdown") {
    if (handlers.onShutdown) {
      handlers.onShutdown(payload as StreamFrame);
    }
  }
}

export function openStream(id: string, options?: StreamHandlers): SSEStream {
  const handlers = options || {};
  const stateRef: StreamStateRef = { lastSeq: Number(handlers.fromSeq) || 0 };
  let closed = false;
  let controller: AbortController | null = null;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;

  function scheduleReconnect(): void {
    if (closed) {
      return;
    }
    if (handlers.onState) {
      handlers.onState("reconnecting");
    }
    retryTimer = setTimeout(connect, 1000);
  }

  async function consume(response: Response): Promise<void> {
    if (!response.body) {
      scheduleReconnect();
      return;
    }
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    try {
      while (!closed) {
        const result = await reader.read();
        if (result.done) {
          break;
        }
        buffer += decoder.decode(result.value, { stream: true });
        let index = buffer.indexOf("\n\n");
        while (index >= 0) {
          const block = buffer.slice(0, index);
          buffer = buffer.slice(index + 2);
          dispatchBlock(block, handlers, stateRef);
          index = buffer.indexOf("\n\n");
        }
      }
    } catch {
      if (!closed && handlers.onState) {
        handlers.onState("reconnecting");
      }
    } finally {
      try {
        reader.releaseLock();
      } catch {
        /* The stream is already closed. */
      }
    }
    if (!closed) {
      scheduleReconnect();
    }
  }

  async function connect(): Promise<void> {
    if (closed) {
      return;
    }
    controller = new AbortController();
    const headers: Record<string, string> = {
      Accept: "text/event-stream",
    };
    if (stateRef.lastSeq > 0) {
      headers["Last-Event-ID"] = String(stateRef.lastSeq);
    }
    const path =
      "/v1/sessions/" +
      encodeURIComponent(id) +
      "/stream" +
      (stateRef.lastSeq > 0 ? "?from_seq=" + stateRef.lastSeq : "");

    let response: Response;
    try {
      response = await fetch(path, {
        method: "GET",
        headers,
        credentials: "same-origin",
        signal: controller.signal,
      });
    } catch {
      if (!closed) {
        scheduleReconnect();
      }
      return;
    }

    if (response.status === 401) {
      if (handlers.onUnauthorized) {
        handlers.onUnauthorized();
      }
      return;
    }
    if (!response.ok) {
      if (handlers.onError) {
        handlers.onError(response.status);
      }
      if (response.status !== 404) {
        scheduleReconnect();
      }
      return;
    }
    if (handlers.onState) {
      handlers.onState("connected");
    }
    await consume(response);
  }

  void connect();

  return {
    close() {
      closed = true;
      if (retryTimer !== null) {
        clearTimeout(retryTimer);
        retryTimer = null;
      }
      if (controller) {
        controller.abort();
      }
    },
  };
}
