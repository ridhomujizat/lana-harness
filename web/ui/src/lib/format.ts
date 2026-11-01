import type { SessionEvent } from "../api/sessions";

export interface HistoryItem {
  key: string;
  type: string;
  label?: string;
  text?: string;
  name?: string;
  input?: unknown;
  status?: string;
  detail?: string;
}

function asRecord(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object") {
    return {};
  }
  return value as Record<string, unknown>;
}

function stringOf(value: unknown): string {
  return typeof value === "string" ? value : "";
}

export function prettyJSON(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2) ?? "";
  } catch {
    return "";
  }
}

function asObject(input: unknown): Record<string, unknown> {
  if (!input) {
    return {};
  }
  if (typeof input === "string") {
    try {
      const parsed: unknown = JSON.parse(input);
      return parsed && typeof parsed === "object"
        ? (parsed as Record<string, unknown>)
        : {};
    } catch {
      return {};
    }
  }
  if (typeof input === "object") {
    return input as Record<string, unknown>;
  }
  return {};
}

function firstString(
  source: Record<string, unknown>,
  keys: string[]
): string {
  for (const key of keys) {
    const value = source[key];
    if (typeof value === "string" && value !== "") {
      return value;
    }
  }
  return "";
}

function collapse(text: string): string {
  return String(text || "")
    .split(/\s+/)
    .filter(Boolean)
    .join(" ");
}

export function primaryArg(name: string, input: unknown): string {
  const value = asObject(input);
  let raw = "";
  switch (name) {
    case "bash":
      raw = firstString(value, ["command"]);
      break;
    case "read_file":
    case "write_file":
    case "edit_file":
      raw = firstString(value, ["path", "file_path"]);
      break;
    case "glob":
    case "grep":
      raw = firstString(value, ["pattern"]);
      break;
    case "web_fetch":
      raw = firstString(value, ["url"]);
      break;
    case "task": {
      const agent = firstString(value, ["agent"]);
      const title = firstString(value, ["title"]);
      raw = (agent + " · " + title).trim();
      break;
    }
    case "memory_read":
      raw = firstString(value, ["target"]);
      break;
    case "memory_write": {
      const action = firstString(value, ["action"]);
      const target = firstString(value, ["target"]);
      raw = (action + " " + target).trim();
      break;
    }
    default:
      if (input) {
        raw = typeof input === "string" ? input : JSON.stringify(input) || "";
      }
      break;
  }
  return collapse(raw);
}

export function cut(text: unknown, limit: number): string {
  const runes = Array.from(String(text || ""));
  if (runes.length <= limit) {
    return String(text || "");
  }
  if (limit < 1) {
    return "";
  }
  return runes.slice(0, limit - 1).join("") + "…";
}

export function toolStatusWord(status: unknown): string {
  const value = String(status || "").toLowerCase();
  switch (value) {
    case "":
      return "running";
    case "success":
    case "ok":
      return "ok";
    case "canceled":
    case "cancelled":
      return "cancelled";
    case "error":
      return "error";
    case "rejected":
      return "rejected";
    case "running":
      return "running";
    default:
      return value;
  }
}

export function toolLine(
  name: string,
  input: unknown,
  status: unknown
): string {
  const parts = [name || "tool"];
  const arg = cut(primaryArg(name, input), 60);
  if (arg) {
    parts.push(arg);
  }
  parts.push("· " + toolStatusWord(status));
  return parts.join(" ");
}

export function contentText(content: unknown): string {
  if (!Array.isArray(content)) {
    return "";
  }
  const pieces: string[] = [];
  for (const block of content) {
    if (!block || typeof block !== "object") {
      continue;
    }
    const value = block as Record<string, unknown>;
    if (
      value.type === "text" &&
      typeof value.text === "string" &&
      value.text !== ""
    ) {
      pieces.push(value.text);
    }
  }
  return pieces.join("\n");
}

function isInjectedContextBlock(block: unknown): boolean {
  if (!block || typeof block !== "object") {
    return false;
  }
  const value = block as Record<string, unknown>;
  if (value.type !== "text" || typeof value.text !== "string") {
    return false;
  }
  const text = value.text.trim();
  return text.startsWith("<context>") || text.startsWith("<system-update>");
}

/* REQ-LTC-001: the turn loop prepends <context> and, when sections changed,
   <system-update> blocks to the first user message of a turn. Display surfaces
   hide them. context_blocks counts the injected leading blocks; events written
   before the field existed fall back to matching the tags. */
export function userMessageText(payload: Record<string, unknown>): string {
  const content = payload.content;
  if (!Array.isArray(content)) {
    return "";
  }
  const count = payload.context_blocks;
  if (typeof count === "number" && count > 0) {
    return contentText(content.slice(count));
  }
  let index = 0;
  while (index < content.length && isInjectedContextBlock(content[index])) {
    index += 1;
  }
  return contentText(content.slice(index));
}

/* REQ-MEM-030: one line per captured operation. add and replace name the saved
   content; remove names the entry that was dropped. */
export function memoryOpLine(op: unknown): string {
  const value = asRecord(op);
  switch (stringOf(value.action)) {
    case "add":
    case "replace": {
      const content = stringOf(value.content);
      return content ? "Saved to memory: " + content : "";
    }
    case "remove": {
      const oldText = stringOf(value.old_text);
      return oldText ? "Removed from memory: " + oldText : "";
    }
    default:
      return "";
  }
}

/* REQ-KNW-060: one muted line for a knowledge compile pass. It names the source
   file and counts the created and updated pages; a failed pass names the error.
   The source name is the last path segment of the payload source. */
export function knowledgeCompiledLine(payload: unknown): string {
  const value = asRecord(payload);
  const source = stringOf(value.source);
  const segments = source.split("/").filter((segment) => segment !== "");
  const name = segments.length > 0 ? segments[segments.length - 1] : "";
  const error = stringOf(value.error);
  if (error) {
    return 'Knowledge compile failed for "' + name + '": ' + error;
  }
  const ops = Array.isArray(value.ops) ? value.ops : [];
  let created = 0;
  let updated = 0;
  for (const op of ops) {
    switch (stringOf(asRecord(op).op)) {
      case "create":
        created += 1;
        break;
      case "append":
      case "replace":
      case "meta":
        updated += 1;
        break;
      default:
        break;
    }
  }
  const parts: string[] = [];
  if (created > 0) {
    parts.push("created " + created);
  }
  if (updated > 0) {
    parts.push("updated " + updated);
  }
  if (parts.length === 0) {
    return "";
  }
  return 'Knowledge updated from "' + name + '": ' + parts.join(", ");
}

export function outcomeWords(outcome: unknown): string {
  switch (outcome) {
    case "allowed_once":
      return "Allowed once";
    case "allowed_session":
      return "Allowed for session";
    case "rejected":
      return "Rejected";
    case "cancelled":
      return "Cancelled";
    case "unavailable":
      return "Unavailable";
    default:
      return outcome ? String(outcome) : "Unknown";
  }
}

export function stopReasonWords(reason: unknown): string {
  switch (reason) {
    case "end_turn":
      return "end of turn";
    case "max_steps":
      return "maximum steps reached";
    case "loop":
      return "stopped: no progress";
    case "cancelled":
      return "cancelled";
    case "error":
      return "error";
    default:
      return reason ? String(reason) : "unknown";
  }
}

export function formatTime(ms: unknown): string {
  if (!ms) {
    return "unknown";
  }
  const date = new Date(Number(ms));
  if (Number.isNaN(date.getTime())) {
    return "unknown";
  }
  return date.toLocaleString();
}

export function sessionStatusText(status: unknown): string {
  const value = String(status || "");
  if (value === "idle") {
    return "Idle";
  }
  if (value === "running") {
    return "Running";
  }
  if (!value) {
    return "Unknown";
  }
  return value.charAt(0).toUpperCase() + value.slice(1);
}

export function inUseText(row: {
  in_use?: string;
  holder_pid?: number | null;
} | null): string {
  if (!row) {
    return "Not in use";
  }
  if (row.in_use === "other") {
    const pid = Number(row.holder_pid);
    return Number.isInteger(pid) && pid > 0
      ? "In use by another process (PID " + pid + ")"
      : "In use by another process";
  }
  if (row.in_use === "self") {
    return "In use by this daemon";
  }
  return "Not in use";
}

export function holderText(holder: unknown, pid: unknown): string {
  if (holder === "other") {
    return Number.isInteger(pid) && Number(pid) > 0
      ? "another process (PID " + pid + ")"
      : "another process";
  }
  if (holder === "self") {
    return "this daemon";
  }
  return "no process";
}

export function buildHistory(
  events: SessionEvent[] | null | undefined
): HistoryItem[] {
  const items: HistoryItem[] = [];
  const results = new Map<string, unknown>();
  const callIds = new Set<string>();

  for (const event of events || []) {
    const payload = asRecord(event.payload);
    const callId = stringOf(payload.call_id);
    if (event.kind === "tool/call" && callId) {
      callIds.add(callId);
    }
    if (event.kind === "tool/result" && callId) {
      results.set(callId, payload.status);
    }
  }

  for (const event of events || []) {
    const payload = asRecord(event.payload);
    const key = String(event.seq);
    switch (event.kind) {
      case "session/created":
        items.push({
          key,
          type: "decision",
          label: "Session created",
          text:
            "Agent: " +
            (stringOf(payload.agent) || "unknown") +
            " · Channel: " +
            (stringOf(payload.channel) || "unknown"),
        });
        break;
      case "user/message": {
        // REQ-MEM-021: a runtime note is lana's own text, not an owner message.
        if (payload.runtime === true) {
          break;
        }
        const text = userMessageText(payload);
        if (text) {
          items.push({ key, type: "user", label: "You", text });
        }
        break;
      }
      case "assistant/message": {
        const text = contentText(payload.content);
        if (text) {
          items.push({ key, type: "assistant", label: "Assistant", text });
        }
        break;
      }
      case "tool/call":
        items.push({
          key,
          type: "tool",
          name: stringOf(payload.name) || "tool",
          input: payload.input,
          status: results.has(stringOf(payload.call_id))
            ? String(results.get(stringOf(payload.call_id)))
            : "running",
        });
        break;
      case "tool/result":
        if (!payload.call_id || !callIds.has(stringOf(payload.call_id))) {
          items.push({
            key,
            type: "tool",
            name: "Tool result",
            input: null,
            status: stringOf(payload.status),
          });
        }
        break;
      case "approval/asked":
        items.push({
          key,
          type: "decision",
          label: "Approval requested",
          text:
            (stringOf(payload.tool) || "tool") +
            " · risk: " +
            (stringOf(payload.risk) || "unknown") +
            (payload.label ? " · " + stringOf(payload.label) : ""),
        });
        break;
      case "approval/decided":
        items.push({
          key,
          type: "decision",
          label: "Approval decided",
          text:
            outcomeWords(payload.outcome) +
            (payload.reason ? " · reason: " + stringOf(payload.reason) : ""),
        });
        break;
      case "approval/policy":
        items.push({
          key,
          type: "decision",
          label: "Policy changed",
          text: "Policy: " + (stringOf(payload.policy) || "unknown"),
        });
        break;
      case "model/selection":
        items.push({
          key,
          type: "decision",
          label: "Model changed",
          text:
            (stringOf(payload.provider) || "unknown") +
            ":" +
            (stringOf(payload.model) || "unknown") +
            (payload.agent ? " · agent: " + stringOf(payload.agent) : ""),
        });
        break;
      case "compaction/summary":
        items.push({
          key,
          type: "summary",
          label: "Compaction",
          text: stringOf(payload.summary) || "Compaction summary",
          detail:
            "Tokens " +
            (payload.tokens_before === undefined
              ? "?"
              : String(payload.tokens_before)) +
            " to " +
            (payload.tokens_after === undefined
              ? "?"
              : String(payload.tokens_after)),
        });
        break;
      case "turn/end":
        items.push({
          key,
          type: "turn",
          label: "Turn",
          text:
            "Ended: " +
            stopReasonWords(payload.reason) +
            (asRecord(payload.error).message
              ? " · " + stringOf(asRecord(payload.error).message)
              : ""),
        });
        break;
      case "system/message":
        if (payload.text) {
          items.push({
            key,
            type: "system",
            label: "System",
            text: stringOf(payload.text),
          });
        }
        break;
      case "memory/captured": {
        // REQ-MEM-030: one decision line per applied operation.
        const ops = Array.isArray(payload.ops) ? payload.ops : [];
        ops.forEach((op, index) => {
          const text = memoryOpLine(op);
          if (text) {
            items.push({
              key: key + "-" + index,
              type: "decision",
              label: "Memory",
              text,
            });
          }
        });
        break;
      }
      case "knowledge/compiled": {
        // REQ-KNW-060: one decision line for a knowledge compile pass.
        const text = knowledgeCompiledLine(payload);
        if (text) {
          items.push({ key, type: "decision", label: "Knowledge", text });
        }
        break;
      }
      default:
        break;
    }
  }
  return items;
}
