import { ApiError, request } from "./client";

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
  /* Spec 82: whether the pane's Claude Code conversation can be switched to
     another account in place ("ready"), or why not; the conversation id it
     would resume and the claude config dir it runs under ("" or absent for
     the built-in account). An older core sends none of them. */
  account_switch?: "ready" | "no_conversation" | "not_switchable";
  /* Spec 83: the agent of that conversation; agent_config_dir is then an
     Antigravity pane's account HOME. */
  conversation_agent?: "claude" | "agy";
  conversation_id?: string;
  agent_config_dir?: string;
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
  /* One line typed into the new shell once it has printed (no line breaks,
     at most 4 KiB); the pane stays the shell. */
  input?: string;
  /* The Claude Code account the typed line launches (spec 81 REQ-SMC-005):
     for a named account the daemon first mirrors the built-in account's MCP
     servers into it. */
  claude_account?: string;
  /* The Antigravity account the typed line launches (spec 83 REQ-WAG-004):
     for a named account the daemon first relinks its mcp_config.json. */
  agy_account?: string;
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

/* Spec 82 REQ-WAS-002, spec 83 REQ-WAG-003: stops the pane's agent and
   resumes its conversation on another account: a Claude Code account
   ("claude-code" for the built-in one) or an Antigravity account
   ("antigravity"). A refusal is a 409 ApiError with code no_conversation,
   not_switchable, still_running or core_outdated. */
export function switchWorkbenchAccount(
  id: string,
  account: string,
  agent: "claude" | "agy" = "claude",
): Promise<WorkbenchSession> {
  return request(
    "POST",
    "/v1/workbench/sessions/" + encodeURIComponent(id) + "/switch-account",
    agent === "agy" ? { agy_account: account } : { claude_account: account },
  );
}

/* Spec 84 REQ-WBR-002: the Workbench core process and whether it is older
   than the daemon (a core keeps running across updates). */
export interface WorkbenchCore {
  running: boolean;
  version: string;
  commit: string;
  daemon_version: string;
  daemon_commit: string;
  outdated: boolean;
  sessions: number;
}

export function getWorkbenchCore(): Promise<WorkbenchCore> {
  return request("GET", "/v1/workbench/core");
}

/* Spec 84 REQ-WBR-003: stops the core and starts a new one from the daemon's
   build; the panes come back through the spec 60 restore. 409
   restart_running while another restart runs, 503 when the new core does not
   come up. */
export function restartWorkbenchCore(): Promise<WorkbenchCore> {
  return request("POST", "/v1/workbench/core/restart");
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
   never asked, so the Workbench shows the install notice while needs_install.
   Spec 77 REQ-WRA-009: the same switch manages the Antigravity (agy) hooks.json
   folders; kind tells them apart (missing from an older daemon = "claude"). */
export interface AgentHookFolder {
  dir: string;
  account: string;
  kind?: "claude" | "antigravity";
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

/* Spec 77 REQ-WRA-005: the Router Claude Code account every new Workbench
   terminal runs `claude` on (its CLAUDE_CONFIG_DIR and
   CLAUDE_SECURESTORAGE_CONFIG_DIR). The built-in account "claude-code" has
   config_dir "" and sets neither. */
export interface WorkbenchClaudeAccount {
  account: string;
  config_dir: string;
  /* false when the daemon is older than spec 77 (404): the value is then
     the built-in account and cannot be changed. */
  supported: boolean;
}

const BUILTIN_CLAUDE_ACCOUNT = "claude-code";

function claudeAccountValue(data: Partial<WorkbenchClaudeAccount> | null): WorkbenchClaudeAccount {
  return {
    account: String(data?.account || BUILTIN_CLAUDE_ACCOUNT),
    config_dir: String(data?.config_dir || ""),
    supported: true,
  };
}

export async function getWorkbenchClaudeAccount(): Promise<WorkbenchClaudeAccount> {
  try {
    return claudeAccountValue(await request<Partial<WorkbenchClaudeAccount> | null>("GET", "/v1/workbench/claude-account"));
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return { account: BUILTIN_CLAUDE_ACCOUNT, config_dir: "", supported: false };
    }
    throw err;
  }
}

export async function setWorkbenchClaudeAccount(account: string): Promise<WorkbenchClaudeAccount> {
  return claudeAccountValue(
    await request<Partial<WorkbenchClaudeAccount> | null>("PUT", "/v1/workbench/claude-account", { account }),
  );
}
