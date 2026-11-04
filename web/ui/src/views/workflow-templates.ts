import type { Graph } from "../api/workflows";

/* Starter graphs of the new-workflow screen (design workflows-6). They are
   plain drafts: the owner edits them or asks the guardian to change them. */

export interface WorkflowTemplate {
  key: string;
  name: string;
  description: string;
  line: string;
  icon: string;
  tint: string;
  graph: Graph;
}

const X = 240;

export const TEMPLATES: WorkflowTemplate[] = [
  {
    key: "riset-harian",
    name: "Riset harian",
    description: "Scrape tech sources every morning, summarize them, send me what matters.",
    line: "Jadwal → browser → rangkum → Telegram",
    icon: "clock",
    tint: "green",
    graph: {
      nodes: [
        { id: "every-day", type: "trigger.schedule", name: "Every day 07:00", position: { x: X, y: 0 }, params: { cron: "0 7 * * *", timezone: "Asia/Jakarta", enabled: true } },
        { id: "run-button", type: "trigger.manual", name: "Run button", position: { x: X + 300, y: 0 }, params: { enabled: true } },
        { id: "each", type: "loop", name: "For each source", position: { x: X, y: 104 }, params: { items: "{{ state.sources }}" } },
        { id: "scrape", type: "browser", name: "Scrape page", parent: "each", position: { x: X, y: 232 }, params: { url: "https://{{ item }}", format: "markdown", stealth: true, timeout_secs: 30 }, on_failure: { mode: "guardian", retries: 2, backoff_secs: [10, 30] } },
        { id: "summarize", type: "worker", name: "Summarize source", parent: "each", position: { x: X, y: 336 }, params: { task: "Summarize {{ nodes.scrape.output.content }} in 5 bullets for an Indonesian tech reader. Score importance from 1 to 10. Keep the link {{ nodes.scrape.output.url }}.", tools: [], max_steps: 6, max_tokens: 20000, output_schema: { summary: "string", score: "integer 1-10", links: "string[]" } } },
        { id: "rank", type: "agent", name: "Rank by importance", position: { x: X, y: 472 }, params: { prompt: "Rank these summaries by importance and pick the top one: {{ nodes.each.output.items }}", output_schema: { top: "object", digest: "string" } } },
        { id: "important", type: "if", name: "Anything important?", position: { x: X, y: 576 }, params: { left: "{{ nodes.rank.output.top.score }}", op: ">=", right: 7 } },
        { id: "send", type: "message", name: "Send digest", position: { x: X - 140, y: 696 }, params: { text: "{{ nodes.rank.output.digest }}" } },
        { id: "remember", type: "state_set", name: "Remember last run", position: { x: X + 140, y: 696 }, params: { key: "last_run_at", value: "{{ run.started_at }}" } },
        { id: "save", type: "knowledge", name: "Save to knowledge", position: { x: X - 140, y: 800 }, params: { title: "Daily research {{ run.number }}", folder: "research/daily", text: "{{ nodes.rank.output.digest }}" } },
      ],
      edges: [
        { id: "e1", from: "every-day", to: "each" },
        { id: "e2", from: "run-button", to: "each" },
        { id: "e3", from: "each", from_port: "body", to: "scrape" },
        { id: "e4", from: "scrape", to: "summarize" },
        { id: "e5", from: "each", from_port: "done", to: "rank" },
        { id: "e6", from: "rank", to: "important" },
        { id: "e7", from: "important", from_port: "true", to: "send" },
        { id: "e8", from: "important", from_port: "false", to: "remember" },
        { id: "e9", from: "send", to: "save" },
      ],
    },
  },
  {
    key: "review-pr",
    name: "Review PR dengan approval",
    description: "A GitHub webhook asks a bot to review the PR, then waits for my approval before deploying.",
    line: "Webhook GitHub → agent review → approval",
    icon: "zap",
    tint: "orange",
    graph: {
      nodes: [
        { id: "hook", type: "trigger.webhook", name: "PR opened", position: { x: X, y: 0 }, params: { enabled: true } },
        { id: "review", type: "bot", name: "Review the PR", position: { x: X, y: 104 }, params: { agent: "", prompt: "Review this pull request and list risks: {{ input }}", output_schema: { summary: "string", risk: "string" } } },
        { id: "ok", type: "approval", name: "Deploy to staging?", position: { x: X, y: 208 }, params: { message: "{{ nodes.review.output.summary }}", timeout_hours: 24 } },
        { id: "deploy", type: "bash", name: "Deploy to staging", position: { x: X - 140, y: 328 }, params: { command: "./scripts/deploy.sh staging", timeout_secs: 300 } },
        { id: "tell", type: "message", name: "Tell me", position: { x: X + 140, y: 328 }, params: { text: "Deploy skipped: {{ nodes.review.output.risk }}" } },
      ],
      edges: [
        { id: "e1", from: "hook", to: "review" },
        { id: "e2", from: "review", to: "ok" },
        { id: "e3", from: "ok", from_port: "approved", to: "deploy" },
        { id: "e4", from: "ok", from_port: "denied", to: "tell" },
      ],
    },
  },
  {
    key: "triage",
    name: "Triage pesan Telegram",
    description: "A /triage command classifies the message and hands it to the right bot.",
    line: "Pesan masuk → klasifikasi → panggil bot lain",
    icon: "send",
    tint: "blue",
    graph: {
      nodes: [
        { id: "cmd", type: "trigger.chat", name: "/triage", position: { x: X, y: 0 }, params: { command: "triage", enabled: true } },
        { id: "classify", type: "agent", name: "Classify", position: { x: X, y: 104 }, params: { prompt: "Classify this message as bug, question or idea: {{ input.text }}", output_schema: { kind: "string" } } },
        { id: "route", type: "switch", name: "Route", position: { x: X, y: 208 }, params: { value: "{{ nodes.classify.output.kind }}", cases: ["bug", "question"] } },
        { id: "bug", type: "bot", name: "Ask the coder", position: { x: X - 280, y: 328 }, params: { agent: "", prompt: "Investigate this bug report: {{ input.text }}" } },
        { id: "answer", type: "agent", name: "Answer", position: { x: X, y: 328 }, params: { prompt: "Answer: {{ input.text }}" } },
        { id: "note", type: "knowledge", name: "Save idea", position: { x: X + 280, y: 328 }, params: { title: "Idea", folder: "ideas", text: "{{ input.text }}" } },
      ],
      edges: [
        { id: "e1", from: "cmd", to: "classify" },
        { id: "e2", from: "classify", to: "route" },
        { id: "e3", from: "route", from_port: "bug", to: "bug" },
        { id: "e4", from: "route", from_port: "question", to: "answer" },
        { id: "e5", from: "route", from_port: "default", to: "note" },
      ],
    },
  },
];

export function emptyGraph(): Graph {
  return { nodes: [], edges: [] };
}
