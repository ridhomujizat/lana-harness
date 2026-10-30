import {
  html,
  useState,
  useEffect,
} from "/assets/vendor/preact-htm.js";
import { contentText, cut, primaryArg, toolStatusWord } from "/assets/format.js";
import { Markdown } from "/assets/markdown.js";
import { TeamBlock } from "/assets/views/team.js";

/* One assistant turn of the chat view (REQ-WEB-038). The turn block is kept
   separate from the session view so a team block (spec #10) can wrap or
   replace it later without rewriting the chat view. */

const TONES = ["brand", "success", "warning", "error", "gray"];

const READ_TOOLS = new Set(["read_file", "skill_load", "memory_read"]);
const SEARCH_TOOLS = new Set(["glob", "grep"]);
const WRITE_TOOLS = new Set(["write_file", "edit_file", "memory_write"]);
const COMMAND_TOOLS = new Set(["bash", "terminal"]);
const FETCH_TOOLS = new Set(["web_fetch", "web_search"]);
const DELEGATE_TOOLS = new Set(["task", "task_continue"]);

const CATEGORY_ORDER = ["read", "search", "write", "command", "fetch", "delegate", "other"];

function payloadOf(event) {
  return event.payload && typeof event.payload === "object" ? event.payload : {};
}

/* The avatar color follows the agent name, so one agent keeps one identity
   across sessions and team threads. */
export function agentTone(name) {
  const text = String(name || "agent");
  let hash = 0;
  for (let index = 0; index < text.length; index += 1) {
    hash = (hash * 31 + text.charCodeAt(index)) >>> 0;
  }
  return TONES[hash % TONES.length];
}

export function agentInitial(name) {
  const text = String(name || "").trim();
  return text ? Array.from(text)[0].toUpperCase() : "?";
}

export function formatDuration(milliseconds) {
  const seconds = Math.max(0, Math.round(Number(milliseconds) / 1000));
  if (seconds < 60) {
    return seconds + "s";
  }
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  if (minutes < 60) {
    return rest ? minutes + "m " + rest + "s" : minutes + "m";
  }
  const hours = Math.floor(minutes / 60);
  const minuteRest = minutes % 60;
  return minuteRest ? hours + "h " + minuteRest + "m" : hours + "h";
}

export function formatClock(ms) {
  const value = Number(ms);
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function toolCategory(name) {
  const tool = String(name || "").toLowerCase();
  if (READ_TOOLS.has(tool)) {
    return "read";
  }
  if (SEARCH_TOOLS.has(tool)) {
    return "search";
  }
  if (WRITE_TOOLS.has(tool)) {
    return "write";
  }
  if (COMMAND_TOOLS.has(tool)) {
    return "command";
  }
  if (FETCH_TOOLS.has(tool)) {
    return "fetch";
  }
  if (DELEGATE_TOOLS.has(tool)) {
    return "delegate";
  }
  return "other";
}

function plural(count, word) {
  return count + " " + word + (count === 1 ? "" : "s");
}

function categoryText(category, count, running) {
  switch (category) {
    case "read":
      return (running ? "Reading " : "Read ") + plural(count, "file");
    case "search":
      return (running ? "Searching " : "Searched ") + plural(count, "time");
    case "write":
      return (running ? "Writing " : "Wrote ") + plural(count, "file");
    case "command":
      return (running ? "Running " : "Ran ") + plural(count, "command");
    case "fetch":
      return (running ? "Fetching " : "Fetched ") + plural(count, "page");
    case "delegate":
      return (running ? "Delegating " : "Delegated ") + plural(count, "task");
    default:
      return (running ? "Using " : "Used ") + plural(count, "tool");
  }
}

function stepIsRunning(step) {
  if (step.approval && !step.approval.decided) {
    return true;
  }
  const status = String(step.status || "running").toLowerCase();
  if (status !== "" && status !== "running") {
    return false;
  }
  return !step.output;
}

/* "Read 5 files · Running 1 command" (REQ-WEB-038, AC-WEB-041). A category
   that still has an unfinished step reads with its -ing verb. */
export function turnSummary(steps) {
  if (!Array.isArray(steps) || steps.length === 0) {
    return "";
  }
  const tally = new Map();
  for (const step of steps) {
    const category = toolCategory(step.name);
    const entry = tally.get(category) || { count: 0, running: false };
    entry.count += 1;
    if (stepIsRunning(step)) {
      entry.running = true;
    }
    tally.set(category, entry);
  }
  return CATEGORY_ORDER.filter((category) => tally.has(category))
    .map((category) => categoryText(category, tally.get(category).count, tally.get(category).running))
    .join(" · ");
}

export function stepIntent(name) {
  const tool = String(name || "tool");
  switch (tool) {
    case "read_file":
      return "Read a file";
    case "glob":
      return "Find files";
    case "grep":
      return "Search the text";
    case "write_file":
      return "Write a file";
    case "edit_file":
      return "Edit a file";
    case "bash":
    case "terminal":
      return "Run a command";
    case "web_fetch":
      return "Fetch a page";
    case "web_search":
      return "Search the web";
    case "task":
      return "Hand off a task";
    case "task_continue":
      return "Continue a task";
    case "memory_read":
      return "Read memory";
    case "memory_write":
      return "Write memory";
    case "skill_load":
      return "Load a skill";
    default:
      return "Use " + tool;
  }
}

function stepStatusText(step) {
  if (step.approval && !step.approval.decided) {
    return "Waiting for your approval.";
  }
  const status = toolStatusWord(step.status);
  if (status === "ok") {
    return "Finished.";
  }
  if (status === "error") {
    return "The tool returned an error.";
  }
  if (status === "rejected") {
    return "Rejected. The tool did not run.";
  }
  if (status === "cancelled") {
    return "Cancelled.";
  }
  return "Running.";
}

function Icon({ name, className = "icon-20" }) {
  return html`<img
    class=${className}
    src=${"/assets/icons/" + name + ".svg"}
    alt=""
    aria-hidden="true"
  />`;
}

function StepRow({ step }) {
  const [open, setOpen] = useState(false);
  const waiting = Boolean(step.approval && !step.approval.decided);
  const argument = cut(primaryArg(step.name, step.input), 80);
  const detailId = "step-detail-" + String(step.key).replace(/[^a-zA-Z0-9_-]/g, "-");

  return html`<div class="step">
    <button
      type="button"
      class="step-row"
      aria-expanded=${open}
      aria-controls=${detailId}
      aria-label=${stepIntent(step.name) + ", " + (step.name || "tool") + ", " + (waiting ? "waiting for approval" : toolStatusWord(step.status))}
      onClick=${() => setOpen((value) => !value)}
    >
      <span class="step-intent">${stepIntent(step.name)}</span>
      ${argument
        ? html`<span class="chip chip-arg" title=${argument}>${argument}</span>`
        : null}
      <span class="chip chip-tool">${step.name || "tool"}</span>
      ${waiting
        ? html`<span class="badge-ui badge-ui-warning step-waiting">
            <span class="badge-ui-dot" aria-hidden="true"></span>Waiting
          </span>`
        : null}
      <span class="step-spacer"></span>
      <${Icon} name="chevron-right" className=${"icon-20 chevron" + (open ? " is-open" : "")} />
    </button>
    <div class="step-detail" id=${detailId} hidden=${!open}>
      <p class="step-detail-status">${stepStatusText(step)}</p>
      ${step.output
        ? html`<pre class="step-output">${cut(step.output, 2000)}</pre>`
        : null}
    </div>
  </div>`;
}

function ActivityGroup({ group }) {
  const [open, setOpen] = useState(false);
  const summary = turnSummary(group.steps);
  if (!summary) {
    return null;
  }
  const listId = "activity-" + String(group.key).replace(/[^a-zA-Z0-9_-]/g, "-");
  return html`<div class="activity">
    <button
      type="button"
      class="activity-head"
      aria-expanded=${open}
      aria-controls=${listId}
      onClick=${() => setOpen((value) => !value)}
    >
      <${Icon} name="activity" className="icon-20 activity-icon" />
      <span class="activity-summary">${summary}</span>
      <${Icon} name="chevron-right" className=${"icon-20 chevron" + (open ? " is-open" : "")} />
    </button>
    <div class="activity-steps" id=${listId} hidden=${!open}>
      ${group.steps.map((step) => html`<${StepRow} key=${step.key} step=${step} />`)}
    </div>
  </div>`;
}

export function TurnBlock({
  turn,
  agent,
  streamingText = "",
  waitingForInput = false,
  team = null,
  rootEvents = [],
  childEvents = {},
  childLive = {},
  onLoadChildren = null,
}) {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (!turn.running) {
      return undefined;
    }
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [turn.running]);

  /* A turn of a team session renders the team block; a single-agent turn
     keeps the one-avatar header and has no work panel (REQ-TEAM-019). */
  if (team) {
    return html`<${TeamBlock}
      team=${team}
      turn=${turn}
      rootEvents=${rootEvents}
      childEvents=${childEvents}
      childLive=${childLive}
      onLoadChildren=${onLoadChildren}
    />`;
  }

  const startedAt = Number(turn.startedAt) || Number(turn.lastAt) || 0;
  const finishedAt = Number(turn.endedAt) || 0;
  const elapsedAt = turn.running ? now : finishedAt || Number(turn.lastAt) || startedAt;
  const elapsed = startedAt > 0 && elapsedAt > 0 ? elapsedAt - startedAt : 0;
  const duration = startedAt > 0 ? formatDuration(elapsed) : "";
  /* An open question keeps the turn running, but the header names the reason
     instead of the working time (REQ-ASK-012). */
  let statusText = turn.running ? (waitingForInput ? "waiting for you" : "working") : "worked";
  if (duration && !(turn.running && waitingForInput)) {
    statusText += " for " + duration;
  }
  if (!turn.running && turn.stopReason === "cancelled") {
    statusText = "stopped after " + (duration || "0s");
  }
  if (!turn.running && turn.stopReason === "error") {
    statusText = "failed after " + (duration || "0s");
  }

  return html`<article class="turn">
    <div class="turn-head">
      <span class=${"agent-avatar tone-" + agentTone(agent)} aria-hidden="true">
        ${agentInitial(agent)}
      </span>
      <span class="turn-agent">${agent}</span>
      <span class="turn-status">${statusText}</span>
      <span class="turn-spacer"></span>
      <span class="turn-time">${formatClock(startedAt)}</span>
    </div>
    ${turn.groups.map((segment) =>
      segment.kind === "text"
        ? html`<div class="turn-text" key=${segment.key}>
            <${Markdown} text=${segment.text} />
          </div>`
        : html`<${ActivityGroup} key=${segment.key} group=${segment} />`
    )}
    ${streamingText
      ? html`<div class="turn-text turn-text-streaming">
          <${Markdown} text=${streamingText} />
        </div>`
      : null}
  </article>`;
}

export function UserBubble({ text }) {
  return html`<div class="chat-user-row">
    <div class="chat-bubble">${text}</div>
  </div>`;
}

function newTurn(id, startedAt) {
  return {
    id,
    key: id,
    startedAt: Number(startedAt) || 0,
    lastAt: Number(startedAt) || 0,
    endedAt: 0,
    stopReason: "",
    error: null,
    userTexts: [],
    segments: [],
    steps: [],
    approvals: new Map(),
    groups: [],
    running: false,
  };
}

function pushStep(turn, step) {
  turn.segments.push({ kind: "step", key: "step-" + step.key, step });
  turn.steps.push(step);
}

/* Builds the ordered chat thread from the durable event log plus the live
   frame text of the running turn. User messages become bubbles; each turn
   becomes one turn block. Events without a turn_id (rev 1 logs) are grouped
   into a synthetic turn so older sessions still render. */
export function buildTurns(events, live = {}) {
  const results = new Map();
  for (const event of events || []) {
    if (event.kind !== "tool/result") {
      continue;
    }
    const payload = payloadOf(event);
    if (payload.call_id) {
      results.set(payload.call_id, { status: payload.status, output: payload.output });
    }
  }

  const turns = [];
  const turnIndex = new Map();
  const stepIndex = new Map();
  let legacyTurn = null;
  let legacyCount = 0;

  function startTurn(id, startedAt) {
    const turn = newTurn(id, startedAt);
    turns.push(turn);
    turnIndex.set(id, turn);
    return turn;
  }

  function turnFor(event, payload) {
    const id = event.turn_id || payload.turn_id || "";
    if (id) {
      return turnIndex.get(id) || startTurn(id, event.created_at);
    }
    if (event.kind === "user/message") {
      legacyCount += 1;
      legacyTurn = startTurn("legacy-" + legacyCount, event.created_at);
      return legacyTurn;
    }
    if (!legacyTurn) {
      legacyCount += 1;
      legacyTurn = startTurn("legacy-" + legacyCount, event.created_at);
    }
    return legacyTurn;
  }

  function stepFor(turn, callId) {
    if (!callId) {
      return null;
    }
    return stepIndex.get(turn.id + "|" + callId) || null;
  }

  for (const event of events || []) {
    const payload = payloadOf(event);
    const createdAt = Number(event.created_at) || 0;

    switch (event.kind) {
      case "turn/start": {
        const id = event.turn_id || payload.turn_id || "turn-" + event.seq;
        if (!turnIndex.has(id)) {
          startTurn(id, createdAt);
        }
        break;
      }
      case "user/message": {
        const text = contentText(payload.content);
        if (!text) {
          break;
        }
        const turn = turnFor(event, payload);
        turn.userTexts.push({ key: "user-" + event.seq, text });
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        break;
      }
      case "assistant/message": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const text = contentText(payload.content);
        if (text) {
          turn.segments.push({ kind: "text", key: "text-" + event.seq, text });
        }
        break;
      }
      case "tool/call": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const result = payload.call_id ? results.get(payload.call_id) : null;
        const step = {
          key: payload.call_id || String(event.seq),
          callId: payload.call_id || "",
          name: payload.name || "tool",
          input: payload.input,
          status: result ? result.status : "running",
          output: result ? result.output : "",
          approval: null,
        };
        pushStep(turn, step);
        if (step.callId) {
          stepIndex.set(turn.id + "|" + step.callId, step);
          for (const record of turn.approvals.values()) {
            if (record.call_id && record.call_id === step.callId) {
              step.approval = record;
            }
          }
        }
        break;
      }
      case "tool/result": {
        if (!payload.call_id) {
          break;
        }
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const step = stepFor(turn, payload.call_id);
        if (step) {
          step.status = payload.status || step.status;
          step.output = payload.output || "";
        }
        break;
      }
      case "approval/asked": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const record = {
          request_id: payload.request_id || "",
          call_id: payload.call_id || "",
          tool: payload.tool || "tool",
          risk: payload.risk || "",
          label: payload.label || "",
          decided: "",
        };
        turn.approvals.set(record.request_id, record);
        const step = stepFor(turn, record.call_id);
        if (step) {
          step.approval = record;
          if (record.tool) {
            step.name = step.name || record.tool;
          }
        }
        break;
      }
      case "approval/decided": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        const record = turn.approvals.get(payload.request_id);
        if (record) {
          record.decided = payload.outcome || "unavailable";
          const step = stepFor(turn, record.call_id);
          if (step && step.approval) {
            step.approval.decided = record.decided;
          }
        }
        break;
      }
      case "turn/end": {
        const turn = turnFor(event, payload);
        turn.lastAt = Math.max(turn.lastAt, createdAt);
        turn.endedAt = createdAt;
        turn.stopReason = payload.reason || "";
        turn.error = payload.error || null;
        break;
      }
      default:
        break;
    }
  }

  /* Consecutive tool steps become one collapsible activity group; assistant
     text between them stays a paragraph of its own. */
  for (const turn of turns) {
    const groups = [];
    let steps = [];
    for (const segment of turn.segments) {
      if (segment.kind === "step") {
        steps.push(segment.step);
        continue;
      }
      if (steps.length > 0) {
        groups.push({
          kind: "activity",
          key: "activity-" + turn.key + "-" + groups.length,
          steps,
        });
        steps = [];
      }
      groups.push(segment);
    }
    if (steps.length > 0) {
      groups.push({
        kind: "activity",
        key: "activity-" + turn.key + "-" + groups.length,
        steps,
      });
    }
    turn.groups = groups;
  }

  const lastTurn = turns.length > 0 ? turns[turns.length - 1] : null;
  for (const turn of turns) {
    const liveTurn = live.turnId && turn.id === live.turnId;
    const openApproval = Array.from(turn.approvals.values()).some(
      (approval) => !approval.decided
    );
    turn.running = Boolean(
      !turn.endedAt &&
        ((live.running && turn === lastTurn) ||
          liveTurn ||
          (openApproval && turn === lastTurn))
    );
  }

  if (live.running && live.turnId && !turnIndex.has(live.turnId)) {
    const turn = newTurn(live.turnId, live.startedAt || Date.now());
    turn.running = true;
    turns.push(turn);
    turnIndex.set(live.turnId, turn);
  }

  const items = [];
  for (const turn of turns) {
    const empty = turn.userTexts.length === 0 && turn.groups.length === 0;
    if (empty && !turn.running) {
      continue;
    }
    for (const message of turn.userTexts) {
      items.push({ kind: "user", key: message.key, text: message.text });
    }
    items.push({ kind: "turn", key: turn.key, turn });
  }
  return items;
}
