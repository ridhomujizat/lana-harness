import type { Graph, WfNode } from "../api/workflows";

/* The node catalog of spec #33 (api-contract "Node types and params"), as the
   web shows it: palette groups, icons, card subtitles and inspector fields.
   The daemon validates the graph; this file only drives the UI. */

export type FieldKind =
  | "text"
  | "textarea"
  | "number"
  | "bool"
  | "select"
  | "agent"
  | "tools"
  | "json"
  | "list"
  /* JSON when the text parses as JSON (arrays, numbers, true), else a string
     such as a {{ }} template. */
  | "value";

export interface FieldSpec {
  name: string;
  label: string;
  kind: FieldKind;
  required?: boolean;
  placeholder?: string;
  help?: string;
  options?: string[];
  mono?: boolean;
}

export interface NodeMeta {
  type: string;
  group: "trigger" | "ai" | "tool" | "flow" | "state";
  label: string;
  icon: string;
  /* The tint of the icon tile: green triggers, violet AI, blue tools, orange
     flow, gray state (design workflows-2). */
  tint: string;
  ports: string[];
  fields: FieldSpec[];
  defaults: Record<string, unknown>;
}

const PROMPT_HELP =
  "Use {{ }} to read the input, the loop item, earlier nodes ({{ nodes.<id>.output }}) or state.";

export const NODE_CATALOG: NodeMeta[] = [
  {
    type: "trigger.schedule",
    group: "trigger",
    label: "Schedule",
    icon: "clock",
    tint: "green",
    ports: ["out"],
    fields: [
      { name: "cron", label: "Cron", kind: "text", required: true, mono: true, placeholder: "0 7 * * *", help: "minute hour day month weekday" },
      { name: "timezone", label: "Time zone", kind: "text", placeholder: "Asia/Jakarta" },
      { name: "enabled", label: "Enabled", kind: "bool" },
    ],
    defaults: { cron: "0 7 * * *", timezone: "Asia/Jakarta", enabled: true },
  },
  {
    type: "trigger.interval",
    group: "trigger",
    label: "Interval",
    icon: "timer",
    tint: "green",
    ports: ["out"],
    fields: [
      { name: "minutes", label: "Every N minutes", kind: "number", required: true },
      { name: "enabled", label: "Enabled", kind: "bool" },
    ],
    defaults: { minutes: 15, enabled: true },
  },
  {
    type: "trigger.chat",
    group: "trigger",
    label: "Telegram / web chat",
    icon: "send",
    tint: "green",
    ports: ["out"],
    fields: [
      { name: "command", label: "Command", kind: "text", required: true, mono: true, placeholder: "riset", help: "The owner sends /command in Telegram or web chat." },
      { name: "enabled", label: "Enabled", kind: "bool" },
    ],
    defaults: { command: "", enabled: true },
  },
  {
    type: "trigger.webhook",
    group: "trigger",
    label: "Webhook",
    icon: "zap",
    tint: "green",
    ports: ["out"],
    fields: [{ name: "enabled", label: "Enabled", kind: "bool" }],
    defaults: { enabled: true },
  },
  {
    type: "trigger.event",
    group: "trigger",
    label: "Lana event",
    icon: "list",
    tint: "green",
    ports: ["out"],
    fields: [
      { name: "kind", label: "Event", kind: "select", required: true, options: ["turn/end", "session/created", "approval/asked", "question/asked", "knowledge/compiled"] },
      { name: "agent", label: "Only for agent", kind: "agent" },
      { name: "enabled", label: "Enabled", kind: "bool" },
    ],
    defaults: { kind: "turn/end", enabled: true },
  },
  {
    type: "trigger.manual",
    group: "trigger",
    label: "Run button",
    icon: "play",
    tint: "green",
    ports: ["out"],
    fields: [{ name: "enabled", label: "Enabled", kind: "bool" }],
    defaults: { enabled: true },
  },
  {
    type: "agent",
    group: "ai",
    label: "Agent step (guardian)",
    icon: "sparkles",
    tint: "violet",
    ports: ["out"],
    fields: [
      { name: "prompt", label: "Prompt", kind: "textarea", required: true, help: PROMPT_HELP },
      { name: "output_schema", label: "Output schema", kind: "json", mono: true, help: "Optional. The answer must be JSON in this shape; later nodes read it as JSON." },
      { name: "max_tokens", label: "Max tokens", kind: "number" },
    ],
    defaults: { prompt: "" },
  },
  {
    type: "bot",
    group: "ai",
    label: "Call another bot",
    icon: "message-square",
    tint: "violet",
    ports: ["out"],
    fields: [
      { name: "agent", label: "Bot", kind: "agent", required: true },
      { name: "prompt", label: "Prompt", kind: "textarea", required: true, help: PROMPT_HELP },
      { name: "output_schema", label: "Output schema", kind: "json", mono: true },
    ],
    defaults: { agent: "", prompt: "" },
  },
  {
    type: "worker",
    group: "ai",
    label: "Worker agent",
    icon: "bot",
    tint: "violet",
    ports: ["out"],
    fields: [
      { name: "task", label: "Task", kind: "textarea", required: true, help: PROMPT_HELP },
      { name: "agent", label: "Agent", kind: "agent", help: "Empty = the guardian." },
      { name: "model", label: "Model", kind: "text", placeholder: "agent default" },
      { name: "max_steps", label: "Step budget", kind: "number" },
      { name: "max_tokens", label: "Token budget", kind: "number" },
      { name: "tools", label: "Worker tools", kind: "tools" },
      { name: "output_schema", label: "Output schema", kind: "json", mono: true, help: "The worker must answer in this shape. Later nodes read it as JSON." },
    ],
    defaults: { task: "", tools: [], max_steps: 12, max_tokens: 20000 },
  },
  {
    type: "browser",
    group: "tool",
    label: "Browser · Obscura",
    icon: "globe",
    tint: "blue",
    ports: ["out"],
    fields: [
      { name: "url", label: "URL", kind: "text", required: true, mono: true },
      { name: "format", label: "Read as", kind: "select", options: ["markdown", "text", "html"] },
      { name: "stealth", label: "Stealth", kind: "bool" },
      { name: "selector", label: "CSS selector", kind: "text", mono: true },
      { name: "timeout_secs", label: "Timeout (s)", kind: "number" },
    ],
    defaults: { url: "", format: "markdown", stealth: true, timeout_secs: 30 },
  },
  {
    type: "bash",
    group: "tool",
    label: "Bash",
    icon: "terminal",
    tint: "blue",
    ports: ["out"],
    fields: [
      { name: "command", label: "Command", kind: "textarea", required: true, mono: true },
      { name: "timeout_secs", label: "Timeout (s)", kind: "number" },
    ],
    defaults: { command: "", timeout_secs: 120 },
  },
  {
    type: "webfetch",
    group: "tool",
    label: "Web fetch",
    icon: "download",
    tint: "blue",
    ports: ["out"],
    fields: [{ name: "url", label: "URL", kind: "text", required: true, mono: true }],
    defaults: { url: "" },
  },
  {
    type: "tool",
    group: "tool",
    label: "MCP tool",
    icon: "plug",
    tint: "blue",
    ports: ["out"],
    fields: [
      { name: "name", label: "Tool name", kind: "text", required: true, mono: true },
      { name: "input", label: "Input", kind: "json", mono: true },
    ],
    defaults: { name: "", input: {} },
  },
  {
    type: "message",
    group: "tool",
    label: "Send message",
    icon: "send",
    tint: "blue",
    ports: ["out"],
    fields: [{ name: "text", label: "Message", kind: "textarea", required: true, help: "Sent to your Telegram owner chat." }],
    defaults: { text: "" },
  },
  {
    type: "knowledge",
    group: "tool",
    label: "Knowledge",
    icon: "book-open",
    tint: "blue",
    ports: ["out"],
    fields: [
      { name: "title", label: "Title", kind: "text", required: true },
      { name: "folder", label: "Folder", kind: "text", mono: true, placeholder: "research/daily" },
      { name: "text", label: "Text", kind: "textarea", required: true },
    ],
    defaults: { title: "", folder: "", text: "" },
  },
  {
    type: "if",
    group: "flow",
    label: "If",
    icon: "git-branch",
    tint: "orange",
    ports: ["true", "false"],
    fields: [
      { name: "left", label: "Value", kind: "text", required: true, mono: true, placeholder: "{{ nodes.rank.output.score }}" },
      { name: "op", label: "Is", kind: "select", required: true, options: ["==", "!=", ">", ">=", "<", "<=", "contains", "not_contains", "exists", "not_exists", "truthy", "falsy"] },
      { name: "right", label: "Than / to", kind: "value", mono: true },
    ],
    defaults: { left: "", op: ">=", right: "" },
  },
  {
    type: "switch",
    group: "flow",
    label: "Switch",
    icon: "split",
    tint: "orange",
    ports: ["default"],
    fields: [
      { name: "value", label: "Value", kind: "text", required: true, mono: true },
      { name: "cases", label: "Cases", kind: "list", required: true, help: "One branch per case, plus default." },
    ],
    defaults: { value: "", cases: [] },
  },
  {
    type: "loop",
    group: "flow",
    label: "Loop",
    icon: "repeat",
    tint: "orange",
    ports: ["body", "done"],
    fields: [{ name: "items", label: "Items", kind: "value", required: true, mono: true, placeholder: '["a.com", "b.com"] or {{ state.sources }}', help: "A JSON array, or a {{ }} that gives one. Nodes inside the loop box run once per item and read {{ item }}." }],
    defaults: { items: "" },
  },
  {
    type: "wait",
    group: "flow",
    label: "Wait / delay",
    icon: "hourglass",
    tint: "orange",
    ports: ["out"],
    fields: [{ name: "seconds", label: "Seconds", kind: "number", required: true }],
    defaults: { seconds: 60 },
  },
  {
    type: "approval",
    group: "flow",
    label: "Human approval",
    icon: "circle-check",
    tint: "orange",
    ports: ["approved", "denied"],
    fields: [
      { name: "message", label: "Question", kind: "textarea", required: true },
      { name: "timeout_hours", label: "Expires after (h)", kind: "number" },
    ],
    defaults: { message: "", timeout_hours: 24 },
  },
  {
    type: "merge",
    group: "flow",
    label: "Merge",
    icon: "git-merge",
    tint: "orange",
    ports: ["out"],
    fields: [],
    defaults: {},
  },
  {
    type: "state_get",
    group: "state",
    label: "Get state",
    icon: "database",
    tint: "gray",
    ports: ["out"],
    fields: [{ name: "key", label: "Key", kind: "text", required: true, mono: true }],
    defaults: { key: "" },
  },
  {
    type: "state_set",
    group: "state",
    label: "Set state",
    icon: "database",
    tint: "gray",
    ports: ["out"],
    fields: [
      { name: "key", label: "Key", kind: "text", required: true, mono: true },
      { name: "value", label: "Value", kind: "value", required: true, mono: true },
    ],
    defaults: { key: "", value: "" },
  },
];

export const GROUPS: { key: NodeMeta["group"]; label: string }[] = [
  { key: "trigger", label: "Triggers" },
  { key: "ai", label: "AI" },
  { key: "tool", label: "Tools" },
  { key: "flow", label: "Flow" },
  { key: "state", label: "State" },
];

const BY_TYPE = new Map(NODE_CATALOG.map((meta) => [meta.type, meta]));

export function nodeMeta(type: string): NodeMeta {
  return (
    BY_TYPE.get(type) || {
      type,
      group: "tool",
      label: type,
      icon: "box",
      tint: "gray",
      ports: ["out"],
      fields: [],
      defaults: {},
    }
  );
}

export function isTrigger(type: string): boolean {
  return type.startsWith("trigger.");
}

/* The output ports of a node: switch cases are dynamic. */
export function nodePorts(node: WfNode): string[] {
  if (node.type === "switch") {
    const cases = Array.isArray(node.params.cases)
      ? node.params.cases.map((value) => String(value))
      : [];
    return [...cases, "default"];
  }
  return nodeMeta(node.type).ports;
}

function str(value: unknown): string {
  if (value === undefined || value === null) {
    return "";
  }
  return typeof value === "string" ? value : JSON.stringify(value);
}

/* Strip {{ }} so card subtitles read "If · top.score ≥ 7". */
function bare(value: unknown): string {
  return str(value)
    .replace(/\{\{\s*/g, "")
    .replace(/\s*\}\}/g, "")
    .replace(/^nodes\.([^.]+)\.output\./, "$1.");
}

const OPS: Record<string, string> = {
  ">=": "≥",
  "<=": "≤",
  "!=": "≠",
  "==": "=",
};

export function describeCron(expr: string): string {
  const parts = expr.trim().split(/\s+/);
  if (parts.length !== 5) {
    return expr;
  }
  const [min, hour, dom, mon, dow] = parts;
  const pad = (value: string) => value.padStart(2, "0");
  const timeOk = /^\d+$/.test(min || "") && /^\d+$/.test(hour || "");
  const time = timeOk ? pad(hour || "") + ":" + pad(min || "") : "";
  if (dom === "*" && mon === "*") {
    if (timeOk && dow === "*") {
      return "Every day " + time;
    }
    if (timeOk && dow === "1-5") {
      return "Weekdays " + time;
    }
    const days = ["Sundays", "Mondays", "Tuesdays", "Wednesdays", "Thursdays", "Fridays", "Saturdays", "Sundays"];
    if (timeOk && /^[0-7]$/.test(dow || "")) {
      return (days[Number(dow)] || "") + " " + time;
    }
    const step = /^\*\/(\d+)$/.exec(min || "");
    if (step && hour === "*" && dow === "*") {
      return "Every " + step[1] + " min";
    }
    if (min === "0" && hour === "*" && dow === "*") {
      return "Every hour";
    }
  }
  return expr;
}

/* The second line of a node card, e.g. "Schedule · Asia/Jakarta". */
export function nodeSubtitle(node: WfNode): string {
  const p = node.params || {};
  const meta = nodeMeta(node.type);
  switch (node.type) {
    case "trigger.schedule":
      return "Schedule · " + (str(p.timezone) || "local time");
    case "trigger.interval":
      return "Interval · every " + str(p.minutes) + " min";
    case "trigger.chat":
      return "Chat command · /" + str(p.command);
    case "trigger.webhook":
      return "Webhook · POST /hooks/…";
    case "trigger.event":
      return "Event · " + str(p.kind);
    case "trigger.manual":
      return "Run button · optional JSON input";
    case "agent":
      return "Agent step · guardian";
    case "bot":
      return "Call bot · " + (str(p.agent) || "pick a bot");
    case "worker":
      return "Worker agent" + (str(p.model) ? " · " + str(p.model) : "");
    case "browser":
      return "Browser · Obscura" + (p.stealth ? " · stealth" : "");
    case "bash":
      return "Bash · " + (str(p.command).split("\n")[0] || "command");
    case "webfetch":
      return "Web fetch · " + (bare(p.url) || "url");
    case "tool":
      return "Tool · " + (str(p.name) || "pick a tool");
    case "message":
      return "Telegram · owner chat";
    case "knowledge":
      return "Knowledge · " + (str(p.folder) || "notes");
    case "if":
      return "If · " + bare(p.left) + " " + (OPS[str(p.op)] || str(p.op)) + " " + bare(p.right);
    case "switch":
      return "Switch · " + bare(p.value);
    case "loop": {
      if (Array.isArray(p.items)) {
        return "Loop · " + p.items.length + " items";
      }
      return "Loop · " + (bare(p.items) || "items");
    }
    case "wait":
      return "Wait · " + str(p.seconds) + " s";
    case "approval":
      return "Human approval";
    case "merge":
      return "Merge branches";
    case "state_get":
      return "Get state · " + str(p.key);
    case "state_set":
      return "Set state · " + str(p.key);
    default:
      return meta.label;
  }
}

/* The trigger shown in the list and run headers: the first enabled trigger. */
export function primaryTrigger(graph: Graph): WfNode | undefined {
  return (
    graph.nodes.find((node) => isTrigger(node.type) && node.params.enabled !== false) ||
    graph.nodes.find((node) => isTrigger(node.type))
  );
}

/* A node id that is unique in the graph, from its label. */
export function newNodeId(graph: Graph, label: string): string {
  const base =
    label
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 40) || "node";
  const taken = new Set(graph.nodes.map((node) => node.id));
  if (!taken.has(base)) {
    return base;
  }
  let index = 2;
  while (taken.has(base + "-" + index)) {
    index += 1;
  }
  return base + "-" + index;
}

export function newEdgeId(graph: Graph): string {
  const taken = new Set(graph.edges.map((edge) => edge.id));
  let index = graph.edges.length + 1;
  while (taken.has("e" + index)) {
    index += 1;
  }
  return "e" + index;
}
