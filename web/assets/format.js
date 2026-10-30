export function prettyJSON(value) {
  try {
    return JSON.stringify(value, null, 2);
  } catch (cause) {
    return "";
  }
}

function asObject(input) {
  if (!input) {
    return {};
  }
  if (typeof input === "string") {
    try {
      const parsed = JSON.parse(input);
      return parsed && typeof parsed === "object" ? parsed : {};
    } catch (cause) {
      return {};
    }
  }
  if (typeof input === "object") {
    return input;
  }
  return {};
}

function firstString(source, keys) {
  for (const key of keys) {
    const value = source[key];
    if (typeof value === "string" && value !== "") {
      return value;
    }
  }
  return "";
}

function collapse(text) {
  return String(text || "")
    .split(/\s+/)
    .filter(Boolean)
    .join(" ");
}

export function primaryArg(name, input) {
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
        raw = typeof input === "string" ? input : JSON.stringify(input);
      }
      break;
  }
  return collapse(raw);
}

export function cut(text, limit) {
  const runes = Array.from(String(text || ""));
  if (runes.length <= limit) {
    return String(text || "");
  }
  if (limit < 1) {
    return "";
  }
  return runes.slice(0, limit - 1).join("") + "…";
}

export function toolStatusWord(status) {
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

export function toolLine(name, input, status) {
  const parts = [name || "tool"];
  const arg = cut(primaryArg(name, input), 60);
  if (arg) {
    parts.push(arg);
  }
  parts.push("· " + toolStatusWord(status));
  return parts.join(" ");
}

export function contentText(content) {
  if (!Array.isArray(content)) {
    return "";
  }
  const pieces = [];
  for (const block of content) {
    if (block && block.type === "text" && typeof block.text === "string" && block.text !== "") {
      pieces.push(block.text);
    }
  }
  return pieces.join("\n");
}

export function outcomeWords(outcome) {
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

export function stopReasonWords(reason) {
  switch (reason) {
    case "end_turn":
      return "end of turn";
    case "max_steps":
      return "maximum steps reached";
    case "cancelled":
      return "cancelled";
    case "error":
      return "error";
    default:
      return reason ? String(reason) : "unknown";
  }
}

export function formatTime(ms) {
  if (!ms) {
    return "unknown";
  }
  const date = new Date(Number(ms));
  if (Number.isNaN(date.getTime())) {
    return "unknown";
  }
  return date.toLocaleString();
}

export function sessionStatusText(status) {
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

export function inUseText(row) {
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

export function holderText(holder, pid) {
  if (holder === "other") {
    return Number.isInteger(pid) && pid > 0
      ? "another process (PID " + pid + ")"
      : "another process";
  }
  if (holder === "self") {
    return "this daemon";
  }
  return "no process";
}

export function buildHistory(events) {
  const items = [];
  const results = new Map();
  const callIds = new Set();

  for (const event of events || []) {
    const payload = event.payload && typeof event.payload === "object" ? event.payload : {};
    if (event.kind === "tool/call" && payload.call_id) {
      callIds.add(payload.call_id);
    }
    if (event.kind === "tool/result" && payload.call_id) {
      results.set(payload.call_id, payload.status);
    }
  }

  for (const event of events || []) {
    const payload = event.payload && typeof event.payload === "object" ? event.payload : {};
    const key = String(event.seq);
    switch (event.kind) {
      case "session/created":
        items.push({
          key,
          type: "decision",
          label: "Session created",
          text: "Agent: " + (payload.agent || "unknown") + " · Channel: " + (payload.channel || "unknown"),
        });
        break;
      case "user/message": {
        const text = contentText(payload.content);
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
          name: payload.name || "tool",
          input: payload.input,
          status: results.has(payload.call_id) ? results.get(payload.call_id) : "running",
        });
        break;
      case "tool/result":
        if (!payload.call_id || !callIds.has(payload.call_id)) {
          items.push({
            key,
            type: "tool",
            name: "Tool result",
            input: null,
            status: payload.status,
          });
        }
        break;
      case "approval/asked":
        items.push({
          key,
          type: "decision",
          label: "Approval requested",
          text:
            (payload.tool || "tool") +
            " · risk: " +
            (payload.risk || "unknown") +
            (payload.label ? " · " + payload.label : ""),
        });
        break;
      case "approval/decided":
        items.push({
          key,
          type: "decision",
          label: "Approval decided",
          text:
            outcomeWords(payload.outcome) +
            (payload.reason ? " · reason: " + payload.reason : ""),
        });
        break;
      case "approval/policy":
        items.push({
          key,
          type: "decision",
          label: "Policy changed",
          text: "Policy: " + (payload.policy || "unknown"),
        });
        break;
      case "model/selection":
        items.push({
          key,
          type: "decision",
          label: "Model changed",
          text:
            (payload.provider || "unknown") +
            ":" +
            (payload.model || "unknown") +
            (payload.agent ? " · agent: " + payload.agent : ""),
        });
        break;
      case "compaction/summary":
        items.push({
          key,
          type: "summary",
          label: "Compaction",
          text: payload.summary || "Compaction summary",
          detail:
            "Tokens " +
            (payload.tokens_before === undefined ? "?" : payload.tokens_before) +
            " to " +
            (payload.tokens_after === undefined ? "?" : payload.tokens_after),
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
            (payload.error && payload.error.message ? " · " + payload.error.message : ""),
        });
        break;
      case "system/message":
        if (payload.text) {
          items.push({ key, type: "system", label: "System", text: payload.text });
        }
        break;
      default:
        break;
    }
  }
  return items;
}
