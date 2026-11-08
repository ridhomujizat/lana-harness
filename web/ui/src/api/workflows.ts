import { queryString, request } from "./client";

/* Workflow calls of API-WF-001 to API-WF-019 (spec #33). Types are written by
   hand from the Go JSON structs of internal/workflow. */

export type WorkflowStatus = "draft" | "active" | "paused" | "error";
export type RunStatus =
  | "queued"
  | "running"
  | "waiting"
  | "succeeded"
  | "failed"
  | "cancelled";
export type NodeStatus =
  | "pending"
  | "running"
  | "succeeded"
  | "failed"
  | "skipped"
  | "waiting"
  | "fallback";

export interface Position {
  x: number;
  y: number;
}

export interface FailurePolicy {
  mode?: string;
  retries?: number;
  backoff_secs?: number[];
  fallback?: unknown;
}

export interface WfNode {
  id: string;
  type: string;
  name: string;
  parent?: string;
  disabled?: boolean;
  position: Position;
  params: Record<string, unknown>;
  on_failure?: FailurePolicy;
}

export interface WfEdge {
  id: string;
  from: string;
  from_port?: string;
  to: string;
}

export interface Graph {
  nodes: WfNode[];
  edges: WfEdge[];
}

export interface LockTool {
  tool: string;
  used_by: string[];
  risk: string;
}

export interface Lock {
  version: number;
  locked_at: number;
  tools: LockTool[];
}

export interface Workflow {
  id: string;
  name: string;
  description: string;
  guardian: string;
  status: WorkflowStatus;
  version: number;
  version_author: string;
  version_summary: string;
  version_at: number;
  graph: Graph;
  lock: Lock | null;
  lock_current: boolean;
  failure_default: FailurePolicy;
  webhook: { path: string; token: string };
  consecutive_failures: number;
  created_at: number;
  updated_at: number;
}

export interface TriggerInfo {
  type: string;
  label: string;
  detail: string;
  enabled: boolean;
}

export interface Run {
  id: string;
  workflow_id: string;
  number: number;
  version: number;
  trigger: string;
  input: unknown;
  status: RunStatus;
  waiting_reason: string;
  wake_at: number;
  error: string;
  tokens: number;
  nodes_done: number;
  nodes_total: number;
  summary: string;
  started_at: number;
  ended_at: number;
}

export interface WorkflowSummary {
  id: string;
  name: string;
  description: string;
  guardian: string;
  status: WorkflowStatus;
  version: number;
  node_count: number;
  triggers: TriggerInfo[];
  runs_7d: number;
  success_7d: number | null;
  running: boolean;
  waiting: boolean;
  last_run: Run | null;
  bots: string[];
  updated_at: number;
}

export interface WorkflowStats {
  total: number;
  active: number;
  paused: number;
  error: number;
  waiting_on_you: number;
  approvals: number;
  decisions: number;
  success_7d: number | null;
  success_prev_7d: number | null;
  runs_today: number;
  runs_today_ok: number;
  runs_today_failed: number;
  runs_today_running: number;
}

export interface Decision {
  id: string;
  workflow_id: string;
  run_id: string;
  run_number: number;
  node_id: string;
  item_index: number;
  kind: string;
  action: string;
  reason: string;
  fix?: Record<string, unknown> | null;
  persistent: boolean;
  confidence: number;
  tokens: number;
  status: string;
  choice: string;
  created_at: number;
  expires_at: number;
  resolved_at: number;
}

export interface Attention extends Decision {
  workflow_name: string;
  node_name: string;
  /* tool_approval rows also carry the tool and its argument. */
  tool?: string;
  argument?: string;
}

export interface WorkflowList {
  workflows: WorkflowSummary[];
  stats: WorkflowStats;
  attention: Attention[];
}

export interface Attempt {
  n: number;
  started_at: number;
  ended_at: number;
  error?: string;
}

export interface NodeRun {
  node_id: string;
  item_index: number;
  status: NodeStatus;
  attempts: Attempt[];
  input: unknown;
  output: unknown;
  port?: string;
  error: string;
  logs: string[];
  tokens: number;
  session_id: string;
  started_at: number;
  ended_at: number;
}

export interface RunDetail {
  run: Run;
  graph: Graph;
  nodes: NodeRun[];
  decisions: Decision[];
}

export interface StateEntry {
  key: string;
  value: unknown;
  updated_at: number;
}

export interface GraphChange {
  from_version: number;
  to_version: number;
  summary: string;
  added: string[];
  removed: string[];
  changed: string[];
}

export interface Issue {
  node_id: string;
  code: string;
  message: string;
}

export interface ChatMessage {
  id: number;
  role: "owner" | "guardian";
  text: string;
  change?: GraphChange | null;
  issues?: Issue[] | null;
  created_at: number;
}

export interface VersionInfo {
  version: number;
  author: string;
  summary: string;
  created_at: number;
  node_count?: number;
}

const base = "/v1/workflows";

// normalizeGraph turns a missing or null nodes/edges list into an empty one.
// An older daemon sends "edges": null for a workflow with no edges, and the
// canvas iterates both lists.
export function normalizeGraph(graph: Graph | null | undefined): Graph {
  return {
    ...(graph ?? {}),
    nodes: Array.isArray(graph?.nodes) ? graph.nodes : [],
    edges: Array.isArray(graph?.edges) ? graph.edges : [],
  } as Graph;
}

function requestWorkflow(method: string, path: string, body?: unknown): Promise<Workflow> {
  return request<Workflow>(method, path, body).then((w) => ({ ...w, graph: normalizeGraph(w.graph) }));
}

function wf(id: string): string {
  return base + "/" + encodeURIComponent(id);
}

export function listWorkflows(): Promise<WorkflowList> {
  return request<WorkflowList>("GET", base);
}

export function getWorkflow(id: string): Promise<Workflow> {
  return requestWorkflow("GET", wf(id));
}

export function createWorkflow(body: {
  name: string;
  description?: string;
  guardian: string;
  graph?: Graph;
}): Promise<Workflow> {
  return requestWorkflow("POST", base, body);
}

export interface UpdateWorkflowBody {
  base_version: number;
  name?: string;
  description?: string;
  guardian?: string;
  graph?: Graph;
  failure_default?: FailurePolicy;
  summary?: string;
}

export function updateWorkflow(
  id: string,
  body: UpdateWorkflowBody
): Promise<Workflow> {
  return requestWorkflow("PUT", wf(id), body);
}

export function deleteWorkflow(id: string): Promise<void> {
  return request<void>("DELETE", wf(id));
}

export function listVersions(id: string): Promise<{ versions: VersionInfo[] }> {
  return request<{ versions: VersionInfo[] }>("GET", wf(id) + "/versions");
}

export function restoreVersion(id: string, version: number): Promise<Workflow> {
  return requestWorkflow("POST", wf(id) + "/restore", { version });
}

export function lockPreview(id: string): Promise<{ tools: LockTool[] }> {
  return request<{ tools: LockTool[] }>("GET", wf(id) + "/lock");
}

export function activateWorkflow(id: string, version: number): Promise<Workflow> {
  return requestWorkflow("POST", wf(id) + "/activate", { version });
}

export function pauseWorkflow(id: string): Promise<Workflow> {
  return requestWorkflow("POST", wf(id) + "/pause");
}

export function resumeWorkflow(id: string): Promise<Workflow> {
  return requestWorkflow("POST", wf(id) + "/resume");
}

export function startRun(id: string, input?: unknown): Promise<Run> {
  return request<Run>(
    "POST",
    wf(id) + "/runs",
    input === undefined ? {} : { input }
  );
}

export function listRuns(
  id: string,
  options?: { limit?: number; before?: number }
): Promise<{ runs: Run[] }> {
  return request<{ runs: Run[] }>(
    "GET",
    wf(id) + "/runs" + queryString(options)
  );
}

export function getRun(id: string, run: string | number): Promise<RunDetail> {
  return request<RunDetail>(
    "GET",
    wf(id) + "/runs/" + encodeURIComponent(String(run))
  ).then((detail) => ({ ...detail, graph: normalizeGraph(detail.graph) }));
}

export function cancelRun(id: string, run: string | number): Promise<Run> {
  return request<Run>(
    "POST",
    wf(id) + "/runs/" + encodeURIComponent(String(run)) + "/cancel"
  );
}

export function answerDecision(
  id: string,
  decision: string,
  choice: string
): Promise<Run> {
  return request<Run>(
    "POST",
    wf(id) + "/decisions/" + encodeURIComponent(decision),
    { choice }
  );
}

export function getState(id: string): Promise<{ entries: StateEntry[] }> {
  return request<{ entries: StateEntry[] }>("GET", wf(id) + "/state");
}

export function setState(
  id: string,
  key: string,
  value: unknown
): Promise<StateEntry> {
  return request<StateEntry>(
    "PUT",
    wf(id) + "/state/" + encodeURIComponent(key),
    { value }
  );
}

export function deleteState(id: string, key?: string): Promise<void> {
  return request<void>(
    "DELETE",
    wf(id) + "/state" + (key ? "/" + encodeURIComponent(key) : "")
  );
}

export function getChat(id: string): Promise<{ messages: ChatMessage[] }> {
  return request<{ messages: ChatMessage[] }>("GET", wf(id) + "/assistant");
}

export interface AssistResponse {
  message: ChatMessage;
  workflow: Workflow;
  tokens: number;
}

export function askAssistant(
  id: string,
  message: string,
  baseVersion: number
): Promise<AssistResponse> {
  return request<AssistResponse>("POST", wf(id) + "/assistant", {
    message,
    base_version: baseVersion,
  });
}

export function rotateWebhook(id: string): Promise<{ token: string }> {
  return request<{ token: string }>("POST", wf(id) + "/webhook/rotate");
}

export interface WorkflowChange {
  workflow_id: string;
  run_id?: string;
}

export interface WorkflowStream {
  close: () => void;
}

/* API-WF-019: GET /v1/workflows/stream. Every `changed` event names the
   workflow (and run) that changed; callers refetch what they show. The reader
   reconnects after 1 s and ignores `: ping` comments. */
export function openWorkflowStream(
  onChange: (change: WorkflowChange) => void
): WorkflowStream {
  let closed = false;
  let controller: AbortController | null = null;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;

  function scheduleReconnect(): void {
    if (!closed) {
      retryTimer = setTimeout(connect, 1000);
    }
  }

  function dispatchBlock(block: string): void {
    let eventName = "message";
    const dataLines: string[] = [];
    for (const rawLine of block.split("\n")) {
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
      } else if (field === "data") {
        dataLines.push(value);
      }
    }
    if (eventName !== "changed" || dataLines.length === 0) {
      return;
    }
    try {
      const payload = JSON.parse(dataLines.join("\n")) as WorkflowChange;
      if (payload && typeof payload.workflow_id === "string") {
        onChange(payload);
      }
    } catch {
      /* A broken frame is skipped; the next change refetches anyway. */
    }
  }

  async function connect(): Promise<void> {
    if (closed) {
      return;
    }
    controller = new AbortController();
    let response: Response;
    try {
      response = await fetch(base + "/stream", {
        method: "GET",
        headers: { Accept: "text/event-stream" },
        credentials: "same-origin",
        signal: controller.signal,
      });
    } catch {
      scheduleReconnect();
      return;
    }
    if (!response.ok || !response.body) {
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
          dispatchBlock(buffer.slice(0, index));
          buffer = buffer.slice(index + 2);
          index = buffer.indexOf("\n\n");
        }
      }
    } catch {
      /* Reconnect below. */
    }
    scheduleReconnect();
  }

  void connect();

  return {
    close() {
      closed = true;
      if (retryTimer !== null) {
        clearTimeout(retryTimer);
      }
      if (controller) {
        controller.abort();
      }
    },
  };
}
