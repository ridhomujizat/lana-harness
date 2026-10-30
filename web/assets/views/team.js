/* Team message block and Team work panel (REQ-TEAM-019 to REQ-TEAM-021).
   The pure helpers stay free of Preact and the DOM so they can run in node. */

const LEAD = "lead";
const ARG_LIMIT = 60;
const RETURN_LIMIT = 400;

let html = null;
let useState = null;
let useEffect = null;
let useMemo = null;
let useRef = null;
let AgentAvatar = null;
let Markdown = null;

/* The browser loads the shared components lazily so importing this module in
   node only evaluates the pure helpers. */
if (typeof window !== "undefined" && typeof document !== "undefined") {
  const preact = await import("/assets/vendor/preact-htm.js");
  html = preact.html;
  useState = preact.useState;
  useEffect = preact.useEffect;
  useMemo = preact.useMemo;
  useRef = preact.useRef;
  const common = await import("/assets/views/agent-common.js");
  AgentAvatar = common.AgentAvatar;
  const markdown = await import("/assets/markdown.js");
  Markdown = markdown.Markdown;
}

function payloadOf(event) {
  const payload = event && event.payload;
  if (typeof payload === "string") {
    return parseJSON(payload) || {};
  }
  return payload && typeof payload === "object" ? payload : {};
}

function parseJSON(value) {
  if (!value) {
    return null;
  }
  if (typeof value === "object") {
    return value;
  }
  if (typeof value !== "string") {
    return null;
  }
  try {
    const parsed = JSON.parse(value);
    return parsed && typeof parsed === "object" ? parsed : null;
  } catch (cause) {
    return null;
  }
}

function asObject(value) {
  if (!value) {
    return {};
  }
  if (typeof value === "object") {
    return value;
  }
  return parseJSON(value) || {};
}

function collapse(text) {
  return String(text || "")
    .split(/\s+/)
    .filter(Boolean)
    .join(" ");
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

/* Same primary argument rules as format.js, kept local for the node import. */
function primaryArg(name, input) {
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

function contentText(content) {
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

/* At most `max` characters, ending with an ellipsis when the text is cut.
   This mirrors internal/toolfmt.Cut and format.js. */
export function cut(text, max) {
  const value = text === undefined || text === null ? "" : String(text);
  const runes = Array.from(value);
  const limit = Number(max);
  if (!Number.isFinite(limit) || limit < 1) {
    return "";
  }
  if (runes.length <= limit) {
    return value;
  }
  return runes.slice(0, limit - 1).join("") + "…";
}

function toolRowStatus(status) {
  switch (String(status || "").toLowerCase()) {
    case "success":
    case "ok":
      return "done";
    case "error":
      return "error";
    case "canceled":
    case "cancelled":
    case "rejected":
      return "cancelled";
    default:
      return "running";
  }
}

/* Builds the delegation steps of one root turn. Each task call gives an
   outgoing step; each result adds the return step with the child's answer.
   task_continue has no agent input, so its agent comes from the result or
   from the child mapping the caller already has. */
export function teamSteps(rootEvents, childAgents = null) {
  const events = Array.isArray(rootEvents) ? rootEvents : [];
  const results = new Map();
  const sessionAgents = new Map();

  for (const event of events) {
    if (!event || event.kind !== "tool/result") {
      continue;
    }
    const payload = payloadOf(event);
    if (!payload.call_id) {
      continue;
    }
    results.set(String(payload.call_id), {
      payload,
      createdAt: Number(event.created_at) || 0,
    });
    const parsed = parseJSON(payload.output);
    if (parsed && parsed.session_id && parsed.agent) {
      sessionAgents.set(String(parsed.session_id), String(parsed.agent));
    }
  }

  const calls = [];
  for (const event of events) {
    if (!event || event.kind !== "tool/call") {
      continue;
    }
    const payload = payloadOf(event);
    const name = String(payload.name || "");
    if (name !== "task" && name !== "task_continue") {
      continue;
    }
    const input = asObject(payload.input);
    const callId = String(payload.call_id || "");
    const found = callId ? results.get(callId) : null;
    const result = found ? found.payload : null;
    const parsed = result ? parseJSON(result.output) : null;
    const childId = String((parsed && parsed.session_id) || input.session_id || "");
    const mapped =
      childAgents && childId
        ? typeof childAgents.get === "function"
          ? childAgents.get(childId) || ""
          : childAgents[childId] || ""
        : "";
    const agent = String(
      input.agent ||
        (parsed && parsed.agent) ||
        sessionAgents.get(childId) ||
        mapped ||
        "agent"
    );
    calls.push({
      callId,
      childId,
      agent,
      result,
      prompt: String(input.prompt || ""),
      startedAt: Number(event.created_at) || 0,
      endedAt: found ? found.createdAt : 0,
    });
  }

  const steps = [];
  let completed = 0;
  for (const call of calls) {
    steps.push({
      kind: "delegation",
      key: "out-" + (call.callId || String(steps.length)),
      callId: call.callId,
      childId: call.childId,
      agent: call.agent,
      from: LEAD,
      to: call.agent,
      text: call.prompt,
      status: call.result ? "done" : "running",
      startedAt: call.startedAt,
      endedAt: call.endedAt,
    });
    if (!call.result) {
      continue;
    }
    completed += 1;
    const parsed = parseJSON(call.result.output) || {};
    const answer =
      typeof parsed.answer === "string" ? parsed.answer : String(call.result.output || "");
    steps.push({
      kind: "return",
      key: "ret-" + (call.callId || String(steps.length)),
      callId: call.callId,
      childId: call.childId,
      agent: call.agent,
      from: call.agent,
      to: LEAD,
      text: cut(answer, RETURN_LIMIT),
      answer,
      status: String(parsed.status || call.result.status || ""),
      startedAt: call.startedAt,
      endedAt: call.endedAt,
    });
  }

  return { steps, count: steps.length, completed };
}

/* Badge and panel state for the work panel. Live counts every step so far;
   an outgoing step without its return already counts as one. */
export function panelStatus({
  live = false,
  steps = [],
  startedAt = 0,
  endedAt = 0,
  reason = "",
  error = null,
} = {}) {
  const list = Array.isArray(steps)
    ? steps
    : steps && Array.isArray(steps.steps)
      ? steps.steps
      : [];
  if (live) {
    return { tone: "warning", text: "Working · step " + list.length };
  }
  const total = list.length;
  const elapsed = Math.max(0, Number(endedAt) - Number(startedAt));
  const seconds = Number.isFinite(elapsed) ? Math.round(elapsed / 1000) : 0;
  switch (String(reason || "")) {
    case "cancelled":
      return { tone: "gray", text: "Cancelled · " + total + " steps" };
    case "error":
      return { tone: "error", text: "Failed · " + total + " steps" };
    case "max_steps":
      return { tone: "gray", text: "Stopped · " + total + " steps" };
    default:
      return {
        tone: "success",
        text: "Done · " + total + " steps · " + seconds + " s",
      };
  }
}

export function badgeText(status) {
  if (typeof status === "string") {
    return status;
  }
  return status && status.text ? String(status.text) : "";
}

/* Tool rows of one child log, paired by call_id and kept in call order. */
export function childToolRows(childEvents) {
  const events = Array.isArray(childEvents) ? childEvents : [];
  const results = new Map();
  for (const event of events) {
    if (!event || event.kind !== "tool/result") {
      continue;
    }
    const payload = payloadOf(event);
    if (payload.call_id) {
      results.set(String(payload.call_id), payload);
    }
  }

  const rows = [];
  const index = new Map();
  for (const event of events) {
    if (!event || event.kind !== "tool/call") {
      continue;
    }
    const payload = payloadOf(event);
    const callId = String(payload.call_id || "");
    const result = callId ? results.get(callId) : null;
    const name = String(payload.name || "tool");
    const row = {
      key: callId || "call-" + String(event.seq || rows.length),
      callId,
      name,
      argument: cut(primaryArg(name, payload.input), ARG_LIMIT),
      status: toolRowStatus(result ? result.status : "running"),
      output: result ? String(result.output || "") : "",
    };
    if (callId && index.has(callId)) {
      const existing = rows[index.get(callId)];
      existing.argument = row.argument || existing.argument;
      existing.status = row.status;
      existing.output = row.output;
      continue;
    }
    if (callId) {
      index.set(callId, rows.length);
    }
    rows.push(row);
  }
  return rows;
}

/* The lead's last assistant message of the turn. */
export function finalAnswer(rootEvents) {
  const events = Array.isArray(rootEvents) ? rootEvents : [];
  let answer = "";
  for (const event of events) {
    if (!event || event.kind !== "assistant/message") {
      continue;
    }
    const text = contentText(payloadOf(event).content);
    if (text) {
      answer = text;
    }
  }
  return answer;
}

function turnTimes(events) {
  let startedAt = 0;
  let endedAt = 0;
  for (const event of Array.isArray(events) ? events : []) {
    if (!event) {
      continue;
    }
    if (event.kind === "turn/start" && !startedAt) {
      startedAt = Number(event.created_at) || 0;
    }
    if (event.kind === "turn/end") {
      endedAt = Number(event.created_at) || 0;
    }
  }
  return { startedAt, endedAt };
}

function formatClock(ms) {
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

function badgeTone(tone) {
  if (tone === "success" || tone === "error" || tone === "warning") {
    return tone;
  }
  return "neutral";
}

function toolTone(status) {
  if (status === "done") {
    return "success";
  }
  if (status === "error") {
    return "error";
  }
  if (status === "cancelled") {
    return "neutral";
  }
  return "warning";
}

function toolWord(status) {
  switch (status) {
    case "done":
      return "Done";
    case "error":
      return "Error";
    case "cancelled":
      return "Cancelled";
    default:
      return "Running";
  }
}

/* A child session can serve more than one delegation (task_continue), so
   keep only the events of the child turn that belongs to this call. */
function inCallWindow(event, step) {
  const at = Number(event && event.created_at) || 0;
  if (!at) {
    return true;
  }
  if (step.startedAt && at < step.startedAt) {
    return false;
  }
  if (step.endedAt && at > step.endedAt) {
    return false;
  }
  return true;
}

function childEventsFor(step, childEvents) {
  if (!step || !childEvents) {
    return [];
  }
  if (step.childId && Array.isArray(childEvents[step.childId])) {
    return childEvents[step.childId].filter((event) => inCallWindow(event, step));
  }
  const merged = [];
  for (const key of Object.keys(childEvents)) {
    const list = childEvents[key];
    if (!Array.isArray(list)) {
      continue;
    }
    if (list.some((event) => event && event.agent === step.agent)) {
      merged.push(...list.filter((event) => inCallWindow(event, step)));
    }
  }
  return merged;
}

function liveToolEvents(step, childLive) {
  const events = [];
  if (!step || !childLive) {
    return events;
  }
  for (const id of Object.keys(childLive)) {
    const entry = childLive[id];
    if (!entry || !entry.tools) {
      continue;
    }
    if (step.childId && id !== step.childId) {
      continue;
    }
    if (!step.childId && entry.agent !== step.agent) {
      continue;
    }
    for (const callId of Object.keys(entry.tools)) {
      const tool = entry.tools[callId] || {};
      if (step.startedAt && tool.at && tool.at < step.startedAt) {
        continue;
      }
      events.push({
        kind: "tool/call",
        seq: "live-call-" + callId,
        payload: { call_id: callId, name: tool.name, input: tool.input },
      });
      if (tool.status && tool.status !== "running") {
        events.push({
          kind: "tool/result",
          seq: "live-result-" + callId,
          payload: { call_id: callId, status: tool.status, output: tool.output },
        });
      }
    }
  }
  return events;
}

function liveTextFor(step, childLive) {
  if (!step || !childLive) {
    return "";
  }
  let text = "";
  for (const id of Object.keys(childLive)) {
    const entry = childLive[id];
    if (!entry) {
      continue;
    }
    if (step.childId && id !== step.childId) {
      continue;
    }
    if (!step.childId && entry.agent !== step.agent) {
      continue;
    }
    if (step.startedAt && entry.textAt && entry.textAt < step.startedAt) {
      continue;
    }
    if (entry.text) {
      text = entry.text;
    }
  }
  return text;
}

export function DelegationStep({ from, to, text, status = "", avatar = "" }) {
  const tone = status === "running" ? "warning" : "";
  return html`<div class="team-step">
    <span class="team-step-rail">
      <${AgentAvatar} name=${avatar || from || "agent"} size="24" />
    </span>
    <div class="team-step-body">
      <div class="team-step-head">
        <span class="team-step-from">${from || "agent"}</span>
        <span class="team-step-to">→ ${to || LEAD}</span>
        ${status === "running"
          ? html`<span class=${"badge-ui team-badge-" + tone}>
              <span class="badge-ui-dot" aria-hidden="true"></span>Running
            </span>`
          : null}
      </div>
      ${text ? html`<div class="team-step-text"><${Markdown} text=${text} /></div>` : null}
    </div>
  </div>`;
}

export function ToolRow({ row }) {
  if (!row) {
    return null;
  }
  const tone = toolTone(row.status);
  return html`<div class="team-tool">
    <span class="team-tool-name">${row.name || "tool"}</span>
    ${row.argument
      ? html`<span class="team-tool-arg" title=${row.argument}>${row.argument}</span>`
      : null}
    <span class="team-tool-spacer"></span>
    <span class=${"badge-ui team-badge-" + tone}>
      <span class="badge-ui-dot" aria-hidden="true"></span>${toolWord(row.status)}
    </span>
  </div>`;
}

export function TeamWorkPanel({
  team,
  turn,
  rootEvents = [],
  childEvents = {},
  childLive = {},
  onLoadChildren = null,
}) {
  const live = Boolean(turn && turn.running);
  const [open, setOpen] = useState(live);
  const requestedRef = useRef(new Set());
  const childAgents = useMemo(() => {
    const map = {};
    for (const id of Object.keys(childEvents || {})) {
      const list = Array.isArray(childEvents[id]) ? childEvents[id] : [];
      const tagged = list.find((event) => event && event.agent);
      if (tagged) {
        map[id] = tagged.agent;
      }
    }
    for (const id of Object.keys(childLive || {})) {
      if (childLive[id] && childLive[id].agent) {
        map[id] = childLive[id].agent;
      }
    }
    return map;
  }, [childEvents, childLive]);
  const work = useMemo(
    () => teamSteps(rootEvents, childAgents),
    [rootEvents, childAgents]
  );
  const steps = work.steps;
  const times = turnTimes(rootEvents);
  const startedAt = times.startedAt || Number(turn && turn.startedAt) || 0;
  const endedAt = times.endedAt || Number(turn && turn.endedAt) || 0;
  const status = panelStatus({
    live,
    steps,
    startedAt,
    endedAt,
    reason: turn && turn.stopReason,
    error: turn && turn.error,
  });
  const answer = useMemo(() => finalAnswer(rootEvents), [rootEvents]);
  const lead = team && team.lead ? String(team.lead) : LEAD;
  const threadId =
    "team-work-" + String((turn && turn.id) || "turn").replace(/[^a-zA-Z0-9_-]/g, "-");

  /* Open while the turn runs; collapse when it ends (Q-012). */
  useEffect(() => {
    setOpen(live);
  }, [live]);

  /* Older turns load the child logs only when the owner expands the panel
     (Q-011). Live turns get the same rows from the stream. */
  useEffect(() => {
    if (!open || live || !onLoadChildren) {
      return;
    }
    const requests = [];
    const seen = new Set();
    for (const step of steps) {
      if (step.kind !== "delegation" || !step.childId || seen.has(step.childId)) {
        continue;
      }
      seen.add(step.childId);
      if (requestedRef.current.has(step.childId)) {
        continue;
      }
      requestedRef.current.add(step.childId);
      requests.push({ id: step.childId, agent: step.agent });
    }
    if (requests.length > 0) {
      onLoadChildren(requests);
    }
  }, [open, live, steps, onLoadChildren]);

  const groups = useMemo(() => {
    const list = [];
    for (const step of steps) {
      if (step.kind === "delegation") {
        list.push({ key: step.key, out: step, ret: null });
        continue;
      }
      if (step.kind !== "return") {
        continue;
      }
      const last = list.length > 0 ? list[list.length - 1] : null;
      if (last && !last.ret && last.out && last.out.agent === step.agent) {
        last.ret = step;
      } else {
        list.push({ key: step.key, out: null, ret: step });
      }
    }
    return list;
  }, [steps]);

  function rowsFor(group) {
    const step = group.out || group.ret;
    if (!step) {
      return [];
    }
    const stored = childEventsFor(step, childEvents);
    const liveEvents = liveToolEvents(step, childLive);
    if (stored.length === 0 && liveEvents.length === 0) {
      return [];
    }
    return childToolRows(stored.concat(liveEvents));
  }

  return html`<section class="team-work">
    <div class="team-work-head">
      <span class="team-work-title">Team work</span>
      <span
        class=${"badge-ui team-badge-" + badgeTone(status.tone)}
        aria-live="polite"
        aria-atomic="true"
      >
        <span class="badge-ui-dot" aria-hidden="true"></span>${badgeText(status)}
      </span>
      <span class="team-work-spacer"></span>
      <button
        type="button"
        class="team-work-toggle"
        aria-expanded=${open}
        aria-controls=${threadId}
        onClick=${() => setOpen((value) => !value)}
      >${open ? "Hide work ▴" : "Show work ▾"}</button>
    </div>
    <div class="team-work-thread" id=${threadId} hidden=${!open}>
      ${groups.length === 0
        ? html`<p class="team-work-empty">
            ${live ? "Waiting for the first delegation…" : "No delegation steps."}
          </p>`
        : groups.map((group) => {
            const rows = rowsFor(group);
            const liveText = live && !group.ret && group.out
              ? liveTextFor(group.out, childLive)
              : "";
            return html`<div class="team-call" key=${group.key}>
              ${group.out
                ? html`<${DelegationStep}
                    from=${group.out.from}
                    to=${group.out.to}
                    text=${group.out.text}
                    avatar=${lead}
                  />`
                : null}
              ${rows.length > 0
                ? html`<div class="team-tools">
                    ${rows.map((row) =>
                      html`<${ToolRow} key=${row.key} row=${row} />`
                    )}
                  </div>`
                : null}
              ${group.ret
                ? html`<${DelegationStep}
                    from=${group.ret.from}
                    to=${group.ret.to}
                    text=${group.ret.text}
                    avatar=${group.ret.agent}
                  />`
                : null}
              ${live && !group.ret && group.out
                ? html`<${DelegationStep}
                    from=${group.out.agent}
                    to=${LEAD}
                    text=${cut(liveText || "Working…", RETURN_LIMIT)}
                    status="running"
                    avatar=${group.out.agent}
                  />`
                : null}
            </div>`;
          })}
    </div>
    ${!live && answer
      ? html`<div class="team-answer">
          <div class="team-answer-head">
            <${AgentAvatar} name=${lead} size="20" />
            <span class="team-answer-label">lead · answer</span>
          </div>
          <div class="team-answer-text"><${Markdown} text=${answer} /></div>
        </div>`
      : null}
  </section>`;
}

export function TeamBlock({
  team,
  turn,
  rootEvents = [],
  childEvents = {},
  childLive = {},
  onLoadChildren = null,
}) {
  const members = Array.isArray(team && team.members) ? team.members : [];
  const lead = team && team.lead ? String(team.lead) : LEAD;
  const roster = [lead].concat(members.map((name) => String(name || ""))).filter(Boolean);
  const shown = roster.slice(0, 3);
  const startedAt =
    turnTimes(rootEvents).startedAt || Number(turn && turn.startedAt) || 0;
  return html`<article class="team-turn">
    <div class="team-turn-head">
      <span class="agent-avatar-group team-avatar-group" aria-hidden="true">
        ${shown.map((name) =>
          html`<${AgentAvatar} key=${name} name=${name} size="24" />`
        )}
      </span>
      <span class="team-name">${(team && team.name) || "Team"}</span>
      <span class="badge-ui badge-ui-brand team-badge">
        <span class="badge-ui-dot" aria-hidden="true"></span>Team
      </span>
      <span class="team-agents">${1 + members.length} agents</span>
      <span class="turn-spacer"></span>
      <span class="turn-time">${formatClock(startedAt)}</span>
    </div>
    <${TeamWorkPanel}
      team=${team}
      turn=${turn}
      rootEvents=${rootEvents}
      childEvents=${childEvents}
      childLive=${childLive}
      onLoadChildren=${onLoadChildren}
    />
  </article>`;
}
