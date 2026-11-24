import { request } from "./client";

/* Spec 55: the Workbench API. Sessions live in the workbench core process;
   the daemon proxies them under /v1/workbench (REQ-WB-040, REQ-WB-041). */

export type SessionState = "working" | "idle" | "exited";

export interface WorkbenchSession {
  id: string;
  title: string;
  command: string[];
  dir: string;
  labels?: Record<string, string>;
  pid: number;
  created: string;
  cols: number;
  rows: number;
  program: string;
  cwd?: string;
  state: SessionState;
  exit_code: number;
  needs_you: boolean;
  last_output: string;
  attached: number;
  /* The coding agent running in the session (herdr labels: claude, codex,
     cursor, omp, opencode, …) and its screen-derived state. */
  agent?: string;
  agent_state?: "idle" | "working" | "blocked" | "unknown" | "";
  /* Spec 60: set when the core brought the session back after a restart,
     until its program exits or it is restarted; resumed_agent names the
     agent whose resume command was typed into it. */
  restored?: string;
  resumed_agent?: string;
  /* Spec 69: the worktree the agent in this pane reported with
     `lana workbench use`, and when. The daemon adds them; the core never
     sends them. */
  work_dir?: string;
  work_dir_at?: string;
  /* Client only, never sent by the core: the agent finished while its pane
     was not in view (herdr's "done"). */
  done?: boolean;
}

export interface CreateSessionInput {
  command?: string[];
  dir?: string;
  cols?: number;
  rows?: number;
  title?: string;
  labels?: Record<string, string>;
}

export type AttachMode = "control" | "view";

export function listWorkbenchSessions(): Promise<{ sessions: WorkbenchSession[] }> {
  return request("GET", "/v1/workbench/sessions");
}

export function createWorkbenchSession(input: CreateSessionInput): Promise<WorkbenchSession> {
  return request("POST", "/v1/workbench/sessions", input);
}

export function killWorkbenchSession(id: string): Promise<unknown> {
  return request("DELETE", "/v1/workbench/sessions/" + encodeURIComponent(id));
}

export function restartWorkbenchSession(id: string): Promise<WorkbenchSession> {
  return request("POST", "/v1/workbench/sessions/" + encodeURIComponent(id) + "/restart");
}

export function resizeWorkbenchSession(id: string, cols: number, rows: number): Promise<unknown> {
  return request("POST", "/v1/workbench/sessions/" + encodeURIComponent(id) + "/resize", { cols, rows });
}

export function getWorkbenchLayout(): Promise<unknown> {
  return request("GET", "/v1/workbench/layout");
}

export function putWorkbenchLayout(layout: unknown): Promise<unknown> {
  return request("PUT", "/v1/workbench/layout", layout);
}

/* Spec 61 REQ-WP-001, REQ-WP-002: the daemon reads a file a terminal pointed
   at (a path with an optional :line[:col]) and reports how to show it. */
/* "dir": the link named a folder; url and raw_url are empty and root is its
   git root, when it has one. */
export type WorkbenchPreviewKind = "html" | "markdown" | "image" | "pdf" | "text" | "binary" | "dir";

export interface WorkbenchPreview {
  path: string;
  name: string;
  dir: string;
  kind: WorkbenchPreviewKind;
  size: number;
  line: number;
  col: number;
  url: string;
  raw_url: string;
  /* The git root of a folder link (kind "dir"), absent outside a repo. */
  root?: string;
}

export function previewWorkbenchFile(session: string, link: string, dir?: string): Promise<WorkbenchPreview> {
  return request("POST", "/v1/workbench/preview", { session, link, dir: dir || undefined });
}

/* The WebSocket URL of one attachment (REQ-WB-041). Same origin, so the
   session cookie goes with it. */
export function attachURL(id: string, mode: AttachMode): string {
  const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
  return (
    scheme + "//" + window.location.host + "/v1/workbench/sessions/" +
    encodeURIComponent(id) + "/attach?mode=" + mode
  );
}

export type WorkbenchEvent =
  | { type: "session.created" | "session.updated"; session: WorkbenchSession }
  | { type: "session.removed"; session: WorkbenchSession }
  | { type: "layout.changed"; layout: unknown }
  /* Spec 70 REQ-WM-083: a subscribed root's summary changed. */
  | { type: "git.root"; root: string; etag: string; repos: Record<string, string> };

const EVENT_TYPES = ["session.created", "session.updated", "session.removed", "layout.changed", "git.root"] as const;

/* Subscribes to the core events over SSE (REQ-WB-030, REQ-WB-040). The
   browser's EventSource reconnects by itself; onOpen fires on every
   (re)connect so the caller can reload the full list. */
export function subscribeWorkbench(
  onEvent: (event: WorkbenchEvent) => void,
  onOpen?: () => void
): () => void {
  if (typeof EventSource === "undefined") {
    return () => {};
  }
  const source = new EventSource("/v1/workbench/events", { withCredentials: true });
  const handlers = EVENT_TYPES.map((type) => {
    const handler = (message: MessageEvent) => {
      try {
        const data = JSON.parse(String(message.data)) as Record<string, unknown>;
        if (type === "layout.changed") {
          onEvent({ type, layout: data.layout ?? data });
        } else if (type === "git.root") {
          onEvent({ type, root: String(data.root ?? ""), etag: String(data.etag ?? ""), repos: (data.repos ?? {}) as Record<string, string> });
        } else {
          const session = (data.session ?? data) as WorkbenchSession;
          onEvent({ type, session } as WorkbenchEvent);
        }
      } catch {
        /* A malformed event is skipped. */
      }
    };
    source.addEventListener(type, handler as EventListener);
    return [type, handler] as const;
  });
  if (onOpen) {
    source.addEventListener("open", onOpen);
  }
  return () => {
    for (const [type, handler] of handlers) {
      source.removeEventListener(type, handler as EventListener);
    }
    if (onOpen) {
      source.removeEventListener("open", onOpen);
    }
    source.close();
  };
}

/* Files dropped on a terminal in a browser: the daemon saves them and returns
   their absolute paths, which the page types into the session. The macOS app
   sends real paths instead (LanaWebView). */
export function uploadWorkbenchDrops(files: File[]): Promise<string[]> {
  const form = new FormData();
  for (const file of files) form.append("files", file, file.name);
  return request<{ paths?: string[] }>("POST", "/v1/workbench/drops", form).then(
    (response) => (response && response.paths) || [],
  );
}

/* Spec 69 REQ-WA-021: drop the worktree an agent reported for a session, so
   the pane follows its process directory again. */
export function clearWorktreeReport(id: string): Promise<void> {
  return request("DELETE", "/v1/workbench/sessions/" + encodeURIComponent(id) + "/worktree");
}

/* Spec 69 REQ-WA-014: the Claude Code hooks switch (SessionStart + PreToolUse
   pre-edit). The hooks are installed only by the owner's action: "unset" means
   never asked, so the Workbench shows the install notice while needs_install. */
export interface AgentHookFolder {
  dir: string;
  account: string;
  installed: boolean;
  error?: string;
}

export interface AgentHookState {
  enabled: boolean;
  state?: "unset" | "on" | "off";
  needs_install?: boolean;
  folders: AgentHookFolder[];
}

export function getAgentHook(): Promise<AgentHookState> {
  return request("GET", "/v1/workbench/agent-hook");
}

export function setAgentHook(enabled: boolean): Promise<AgentHookState> {
  return request("PUT", "/v1/workbench/agent-hook", { enabled });
}
