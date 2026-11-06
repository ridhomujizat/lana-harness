import type {
  Decision,
  NodeRun,
  Run,
  RunDetail,
  Workflow,
  WorkflowList,
  WorkflowSummary,
} from "../api/workflows";
import { TEMPLATES } from "../views/workflow-templates";

/* Preview fixtures for the Workflows pages (spec #33), shaped like the design
   screens workflows-1..7. */

const NOW = Date.now();
const MIN = 60000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;
const research = TEMPLATES[0]!.graph;

function summary(
  id: string,
  name: string,
  guardian: string,
  status: WorkflowSummary["status"],
  nodes: number,
  trigger: [string, string, string],
  runs: number,
  success: number | null,
  last: number,
  extra: Partial<WorkflowSummary> = {}
): WorkflowSummary {
  return {
    id,
    name,
    description: "",
    guardian,
    status,
    version: 3,
    node_count: nodes,
    triggers: [{ type: trigger[0], label: trigger[1], detail: trigger[2], enabled: true }],
    runs_7d: runs,
    success_7d: success,
    running: false,
    waiting: false,
    last_run: {
      id: "r_" + id,
      workflow_id: id,
      number: runs,
      version: 3,
      trigger: "schedule",
      input: {},
      status: "succeeded",
      waiting_reason: "",
      wake_at: 0,
      error: "",
      tokens: 1200,
      nodes_done: nodes,
      nodes_total: nodes,
      summary: "",
      started_at: NOW - last,
      ended_at: NOW - last + 2 * MIN,
    },
    bots: [guardian],
    updated_at: NOW - last,
    ...extra,
  };
}

const attention = [
  {
    id: "d_pr",
    workflow_id: "pr-review-gate",
    workflow_name: "PR review gate",
    run_id: "r_pr12",
    run_number: 12,
    node_id: "ok",
    node_name: "Deploy to staging",
    item_index: -1,
    kind: "approval",
    action: "ask",
    reason: "wants bash: ./scripts/deploy.sh staging — not in the locked tool list.",
    persistent: false,
    confidence: 0,
    tokens: 0,
    status: "pending",
    choice: "",
    created_at: NOW - 18 * MIN,
    expires_at: NOW + 23 * HOUR,
    resolved_at: 0,
  },
  {
    id: "d_up",
    workflow_id: "uptime-watcher",
    workflow_name: "Uptime watcher",
    run_id: "r_up",
    run_number: 672,
    node_id: "restart",
    node_name: "Restart service",
    item_index: -1,
    kind: "guardian",
    action: "stop",
    reason:
      "api.example.com returns 502 since 06:48. Restart skipped because systemctl is not in the locked tools. Report sent to Telegram.",
    persistent: false,
    confidence: 0.9,
    tokens: 2100,
    status: "applied",
    choice: "",
    created_at: NOW - 12 * MIN,
    expires_at: 0,
    resolved_at: 0,
  },
];

export const workflowListFixture: WorkflowList = {
  workflows: [
    summary("riset-harian", "Riset harian", "default", "active", 10, ["trigger.schedule", "Schedule", "Every day 07:00"], 7, 1, 0, {
      running: true,
    }),
    summary("pr-review-gate", "PR review gate", "coder", "active", 6, ["trigger.webhook", "Webhook", "POST /hooks/pr-review"], 12, 0.92, 18 * MIN, {
      waiting: true,
    }),
    summary("weekly-metrics", "Weekly metrics report", "analyst", "paused", 5, ["trigger.schedule", "Schedule", "Mondays 08:00"], 1, 1, 6 * DAY),
    summary("inbox-triage", "Inbox triage", "default", "active", 7, ["trigger.chat", "Telegram", "/triage in the owner chat"], 64, 0.98, 5 * MIN),
    summary("uptime-watcher", "Uptime watcher", "ops", "error", 6, ["trigger.interval", "Interval", "Every 15 min"], 672, 0.71, 12 * MIN),
    summary("session-recap", "Session recap", "lead", "active", 8, ["trigger.event", "Event", "turn/end"], 23, 1, HOUR, {
      bots: ["lead", "researcher", "writer"],
    }),
  ],
  stats: {
    total: 6,
    active: 4,
    paused: 1,
    error: 1,
    waiting_on_you: 2,
    approvals: 1,
    decisions: 1,
    success_7d: 0.942,
    success_prev_7d: 0.924,
    runs_today: 31,
    runs_today_ok: 28,
    runs_today_failed: 2,
    runs_today_running: 1,
  },
  attention: attention as WorkflowList["attention"],
};

export const workflowFixture: Workflow = {
  id: "riset-harian",
  name: "Riset harian",
  description: "Scrape 5 tech sources every morning, summarize them, send me what matters.",
  guardian: "default",
  status: "active",
  version: 14,
  version_author: "guardian",
  version_summary: "on failure → guardian",
  version_at: NOW - 2 * MIN,
  graph: research,
  lock: {
    version: 14,
    locked_at: NOW - 4 * DAY,
    tools: [
      { tool: "browser", used_by: ["Scrape page"], risk: "read" },
      { tool: "knowledge.save", used_by: ["Save to knowledge"], risk: "write" },
      { tool: "state.set", used_by: ["Remember last run"], risk: "write" },
      { tool: "telegram.send", used_by: ["Send digest"], risk: "write" },
    ],
  },
  lock_current: true,
  failure_default: { mode: "guardian", retries: 2, backoff_secs: [10, 30] },
  webhook: { path: "/hooks/riset-harian", token: "8c1f0e7a9b2d" },
  consecutive_failures: 0,
  created_at: NOW - 9 * DAY,
  updated_at: NOW - 2 * MIN,
};

function run(number: number, status: Run["status"], ago: number, extra: Partial<Run> = {}): Run {
  return {
    id: "r_" + number,
    workflow_id: "riset-harian",
    number,
    version: 14,
    trigger: "schedule",
    input: {},
    status,
    waiting_reason: "",
    wake_at: 0,
    error: "",
    tokens: 18200,
    nodes_done: 9,
    nodes_total: 10,
    summary: "digest sent",
    started_at: NOW - ago,
    ended_at: status === "running" || status === "waiting" ? 0 : NOW - ago + 125000,
    ...extra,
  };
}

export const runsFixture: Run[] = [
  run(8, "waiting", 3 * MIN, { waiting_reason: "owner", summary: "" }),
  run(7, "running", 102000, { summary: "" }),
  run(6, "succeeded", DAY),
  run(5, "succeeded", 2 * DAY, { summary: "nothing important" }),
  run(4, "succeeded", 3 * DAY, { trigger: "chat" }),
  run(3, "failed", 4 * DAY, { error: "Guardian stopped it: Obscura is not running" }),
  run(2, "succeeded", 5 * DAY),
  run(1, "succeeded", 6 * DAY, { trigger: "manual" }),
];

const SITES = ["techcrunch.com", "dailysocial.id", "theverge.com", "news.ycombinator.com", "katadata.co.id"];

function nodeRow(nodeId: string, status: NodeRun["status"], start: number, took: number, extra: Partial<NodeRun> = {}): NodeRun {
  return {
    node_id: nodeId,
    item_index: -1,
    status,
    attempts: [{ n: 1, started_at: start, ended_at: start + took }],
    input: {},
    output: {},
    error: "",
    logs: [],
    tokens: 0,
    session_id: "",
    started_at: start,
    ended_at: status === "running" ? 0 : start + took,
    ...extra,
  };
}

function runSeven(): RunDetail {
  const start = NOW - 102000;
  const nodes: NodeRun[] = [
    nodeRow("every-day", "succeeded", start, 10),
    nodeRow("run-button", "skipped", start, 0),
    nodeRow("each", "succeeded", start + 100, 40000, { output: { items: [], count: 5, ok: 4, skipped: 1 }, port: "done" }),
  ];
  SITES.forEach((site, index) => {
    const at = start + 200 + index * 8000;
    const skipped = index === 2;
    nodes.push(
      nodeRow("scrape", skipped ? "skipped" : "succeeded", at, [2100, 1800, 9000, 900, 2400][index]!, {
        item_index: index,
        input: { url: "https://" + site, format: "markdown", stealth: true },
        output: skipped
          ? { url: "https://" + site + "/tech", status: "skipped", error: "HTTP 403", attempts: 3, decided_by: "guardian" }
          : { url: "https://" + site, format: "markdown", content: "# " + site + "\n…" },
        error: skipped ? "HTTP 403" : "",
        attempts: skipped
          ? [1, 2, 3].map((n) => ({ n, started_at: at + n * 1000, ended_at: at + n * 1000 + 800, error: "HTTP 403" }))
          : [{ n: 1, started_at: at, ended_at: at + 2000 }],
      })
    );
    if (!skipped) {
      nodes.push(nodeRow("summarize", "succeeded", at + 2500, 9500, { item_index: index, tokens: 2400, output: { summary: "…", score: 6 + index, links: [] } }));
    }
  });
  nodes.push(nodeRow("rank", "succeeded", start + 42000, 6100, { tokens: 3100, output: { top: { score: 9 }, digest: "…" } }));
  nodes.push(nodeRow("important", "succeeded", start + 48200, 10, { port: "true", output: { result: true, left: 9, right: 7 } }));
  nodes.push(nodeRow("send", "running", start + 48300, 0));
  const decision: Decision = {
    id: "d_skip",
    workflow_id: "riset-harian",
    run_id: "r_7",
    run_number: 7,
    node_id: "scrape",
    item_index: 2,
    kind: "guardian",
    action: "skip",
    reason:
      "theverge.com blocked the browser 3 times (HTTP 403). Four sources are enough for today's digest, so the run goes on without it. I will try this source again tomorrow.",
    persistent: false,
    confidence: 0.86,
    tokens: 2300,
    status: "applied",
    choice: "",
    created_at: start + 22000,
    expires_at: 0,
    resolved_at: 0,
  };
  return { run: runsFixture[1]!, graph: research, nodes, decisions: [decision] };
}

function runEight(): RunDetail {
  const start = NOW - 3 * MIN;
  const nodes: NodeRun[] = [
    nodeRow("every-day", "succeeded", start, 10),
    nodeRow("run-button", "skipped", start, 0),
    nodeRow("each", "succeeded", start + 100, 38000, { output: { count: 5, ok: 5, skipped: 0 }, port: "done" }),
  ];
  SITES.forEach((site, index) => {
    nodes.push(nodeRow("scrape", "succeeded", start + index * 7000, 1900, { item_index: index, input: { url: "https://" + site } }));
    nodes.push(nodeRow("summarize", "succeeded", start + index * 7000 + 2000, 7000, { item_index: index, tokens: 2100 }));
  });
  nodes.push(
    nodeRow("rank", "waiting", start + 40000, 0, {
      error:
        'SchemaError: output does not match\n  items[3].score   required\n  items[4]         missing\nfinish_reason: "length"\nmax_tokens: 2000 · used 2000',
      attempts: [
        { n: 1, started_at: start + 40000, ended_at: start + 56000, error: "cut off at 2,000 tokens" },
        { n: 2, started_at: start + 66000, ended_at: start + 82000, error: "same, after 10 s" },
        { n: 3, started_at: start + 112000, ended_at: start + 127000, error: "same, after 30 s" },
      ],
      status: "failed",
    })
  );
  const decision: Decision = {
    id: "d_ask",
    workflow_id: "riset-harian",
    run_id: "r_8",
    run_number: 8,
    node_id: "rank",
    item_index: -1,
    kind: "guardian",
    action: "ask",
    reason:
      "The ranking reply is cut off at 2,000 tokens. Today has 5 long sources, so the JSON ends before item 4. Retrying the same way will fail again.",
    fix: { max_tokens: 4000, reply_format: "compact JSON" },
    persistent: true,
    confidence: 0.82,
    tokens: 3100,
    status: "pending",
    choice: "",
    created_at: start + 130000,
    expires_at: start + 130000 + DAY,
    resolved_at: 0,
  };
  return { run: { ...runsFixture[0]!, tokens: 14100 }, graph: research, nodes, decisions: [decision] };
}

export function runDetailFixture(ref: string): RunDetail {
  if (ref === "8" || ref === "r_8") {
    return runEight();
  }
  const detail = runSeven();
  if (ref !== "7" && ref !== "r_7") {
    const match = runsFixture.find((item) => String(item.number) === ref || item.id === ref);
    if (match) {
      return { ...detail, run: match, nodes: detail.nodes.map((row) => ({ ...row, status: row.status === "running" ? "succeeded" : row.status, ended_at: row.ended_at || row.started_at + 900 })), decisions: [] };
    }
  }
  return detail;
}

export const stateFixture = {
  entries: [
    { key: "sources", value: SITES, updated_at: NOW - 3 * DAY },
    { key: "seen_urls", value: Array.from({ length: 214 }, (_, i) => "u" + i), updated_at: NOW - 2 * HOUR },
    { key: "last_run_at", value: "2026-11-05T07:01:44+07:00", updated_at: NOW - 2 * HOUR },
    { key: "last_digest_id", value: 8841, updated_at: NOW - DAY },
  ],
};

export const chatFixture = {
  messages: [
    { id: 1, role: "owner", text: "Tiap jam 7 pagi cek 5 situs tech, rangkum, kirim yang penting ke Telegram dan simpan ke knowledge.", created_at: NOW - 3 * DAY },
    {
      id: 2,
      role: "guardian",
      text: "Draft-nya sudah ada di canvas. Skor ≥ 7 dikirim ke Telegram dan disimpan ke knowledge.",
      change: { from_version: 0, to_version: 1, summary: "10 nodes", added: ["Every day 07:00", "For each source", "Scrape page", "Summarize source"], removed: [], changed: [] },
      created_at: NOW - 3 * DAY,
    },
    { id: 3, role: "owner", text: "Kalau ada situs yang gagal, skip aja jangan stop. Kirimnya ke chat saya saja.", created_at: NOW - 4 * MIN },
    {
      id: 4,
      role: "guardian",
      text: "Sudah saya atur. Node yang berubah saya tandai di canvas.",
      change: { from_version: 13, to_version: 14, summary: "2 settings", added: [], removed: [], changed: ["Scrape page", "Send digest"] },
      created_at: NOW - 2 * MIN,
    },
  ],
};

/* Routes /v1/workflows*. */
export function mockWorkflows(method: string, parts: string[]): { status: number; body: unknown; stream?: string } {
  const at = (index: number): string => parts[index] || "";
  const id = at(2);
  if (!id) {
    if (method === "POST") return { status: 201, body: { ...workflowFixture, id: "new-workflow", name: "New workflow", status: "draft", version: 1, lock: null, lock_current: false } };
    return { status: 200, body: workflowListFixture };
  }
  if (id === "stream") {
    return { status: 200, body: null, stream: ": ping\n\n" };
  }
  const wf = { ...workflowFixture, id, name: workflowListFixture.workflows.find((row) => row.id === id)?.name || workflowFixture.name };
  switch (at(3)) {
    case "":
      return { status: method === "DELETE" ? 204 : 200, body: wf };
    case "runs":
      if (at(4)) {
        if (at(5) === "cancel") return { status: 200, body: { ...runsFixture[0], status: "cancelled" } };
        return { status: 200, body: runDetailFixture(at(4)) };
      }
      if (method === "POST") return { status: 202, body: runsFixture[1] };
      return { status: 200, body: { runs: runsFixture } };
    case "state":
      return { status: 200, body: stateFixture };
    case "assistant":
      if (method === "POST") {
        return {
          status: 200,
          body: {
            message: { id: 9, role: "guardian", text: "Siap, sudah saya ubah.", change: { from_version: 14, to_version: 15, summary: "1 node", added: ["Remember sources"], removed: [], changed: [] }, created_at: Date.now() },
            workflow: { ...wf, version: 15 },
            tokens: 2100,
          },
        };
      }
      return { status: 200, body: chatFixture };
    case "lock":
      return { status: 200, body: { tools: workflowFixture.lock!.tools } };
    case "versions":
      return { status: 200, body: { versions: [{ version: 14, author: "guardian", summary: "on failure → guardian", created_at: NOW - 2 * MIN }] } };
    case "decisions":
      return { status: 200, body: runsFixture[0] };
    case "webhook":
      return { status: 200, body: { token: "rotated" } };
    default:
      return { status: 200, body: wf };
  }
}
