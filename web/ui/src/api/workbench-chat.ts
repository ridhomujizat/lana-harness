import { request } from "./client";

/* Spec 71: the Claude Code chat view of a Workbench pane. The daemon reads the
   Claude Code transcript and a mirror of the pane's screen, and turns chat
   actions into terminal input (api-contract.md). */

export interface ChatImageRef {
  ref: string;
  media_type: string;
}

export interface ChatToolSummary {
  verb: string;
  target: string;
  meta?: string;
}

export interface ChatToolResult {
  text: string;
  lines: number;
  truncated: boolean;
  is_error: boolean;
}

export interface ChatQuestionOption {
  label: string;
  description?: string;
}

export interface ChatQuestion {
  question: string;
  header?: string;
  multi_select?: boolean;
  options: ChatQuestionOption[];
}

export type ChatItemKind =
  | "user"
  | "command"
  | "output"
  | "assistant"
  | "thinking"
  | "tool"
  | "question"
  | "plan"
  | "divider"
  | "turn_end";

export interface ChatAgentRef {
  agent_id: string;
  agent_type?: string;
  background?: boolean;
}

/* Spec 71 REQ-WC-016: a background shell or a subagent. */
export interface ChatTask {
  id: string;
  kind: "shell" | "agent" | string;
  tool_use_id?: string;
  title: string;
  agent_type?: string;
  status: "running" | "completed" | "failed" | "killed" | string;
  summary?: string;
  started?: string;
  ended?: string;
}

export interface ChatAgentDetail {
  agent_id: string;
  agent_type?: string;
  description?: string;
  model?: string;
  finished: boolean;
  steps: number;
  items: ChatItem[];
}

export interface ChatItem {
  id: string;
  kind: ChatItemKind;
  at?: string;
  text?: string;
  images?: ChatImageRef[];
  name?: string;
  args?: string;
  model?: string;
  tool_use_id?: string;
  input?: Record<string, unknown>;
  summary?: ChatToolSummary;
  diff?: { old: string; new: string };
  status?: "running" | "done" | "error";
  result?: ChatToolResult;
  questions?: ChatQuestion[];
  answered?: boolean;
  answer?: string;
  plan?: string;
  state?: "pending" | "approved" | "rejected";
  duration_ms?: number;
  output_tokens?: number;
  agent?: ChatAgentRef;
}

export interface ChatContext {
  used: number;
  window: number;
  cache_read: number;
  input: number;
  output: number;
}

export type ChatMode = "default" | "acceptEdits" | "plan" | "auto" | "bypassPermissions";

export interface ChatMeta {
  model: string;
  effort: string;
  mode: string;
  context: ChatContext;
  unknown_lines: number;
  version: string;
  tasks?: ChatTask[];
}

export interface ChatDialogOption {
  key: string;
  label: string;
  selected: boolean;
  description?: string;
  checked?: boolean;
}

export interface ChatDialogTab {
  label: string;
  done: boolean;
  /* The chip of the question on screen (its highlight on the terminal). */
  active?: boolean;
}

export interface ChatDialog {
  kind: "permission" | "question" | "plan" | "other";
  title: string;
  lines: string[];
  options: ChatDialogOption[];
  tabs?: ChatDialogTab[];
  /* Text between the title and the options, e.g. the answers on the
     review step of a multi-question dialog. */
  body?: string[];
  /* The preview Claude Code shows next to the option under the cursor. */
  preview?: string;
  /* Checkbox options (multi-select). */
  multi?: boolean;
}

export interface ChatLive {
  state: "idle" | "working" | "blocked" | "unknown" | "";
  status: string;
  mode: string;
  effort?: string;
  input_empty: boolean;
  dialog: ChatDialog | null;
}

export interface ChatConversation {
  id: string;
  path: string;
  started?: string;
}

export interface ChatUpdate {
  available: boolean;
  reason?: "no_agent" | "unsupported_agent" | "waiting" | "";
  agent?: string;
  conversation?: ChatConversation | null;
  reset?: boolean;
  truncated_before?: boolean;
  items?: ChatItem[];
  meta?: ChatMeta;
  live?: ChatLive | null;
}

export interface ChatCommand {
  name: string;
  description: string;
  source: string;
}

function base(id: string): string {
  return "/v1/workbench/sessions/" + encodeURIComponent(id) + "/chat";
}

export function getChat(id: string, before?: string): Promise<ChatUpdate> {
  return request("GET", base(id) + (before ? "?before=" + encodeURIComponent(before) : ""));
}

export function chatSay(id: string, text: string, paths?: string[], force?: boolean): Promise<void> {
  return request("POST", base(id) + "/say", { text, paths: paths && paths.length ? paths : undefined, force: force || undefined });
}

export function chatKeys(id: string, keys: string[]): Promise<void> {
  return request("POST", base(id) + "/keys", { keys });
}

export function chatMode(id: string, mode: ChatMode): Promise<{ mode: string }> {
  return request("POST", base(id) + "/mode", { mode });
}

export function chatModel(id: string, input: { model?: string; effort?: string }): Promise<void> {
  return request("POST", base(id) + "/model", input);
}

export function chatCommands(id: string): Promise<{ commands: ChatCommand[] }> {
  return request("GET", base(id) + "/commands");
}

/* Files for the `@` menu: any folder, git or not. */
export function chatFiles(id: string, q: string): Promise<{ files: string[]; root?: string }> {
  return request("GET", base(id) + "/files?q=" + encodeURIComponent(q));
}

export function getChatAgent(id: string, agentId: string): Promise<ChatAgentDetail> {
  return request("GET", base(id) + "/agents/" + encodeURIComponent(agentId));
}

export function chatImageURL(id: string, item: string, index: number): string {
  return base(id) + "/image?item=" + encodeURIComponent(item) + "&i=" + index;
}

/* Opens the chat stream (REQ-WC-041). The browser's EventSource reconnects by
   itself; every (re)connect starts with a snapshot (reset: true). */
export function subscribeChat(id: string, onUpdate: (update: ChatUpdate) => void, onError?: () => void): () => void {
  if (typeof EventSource === "undefined") {
    return () => {};
  }
  const source = new EventSource(base(id) + "/stream", { withCredentials: true });
  const handler = (message: MessageEvent) => {
    try {
      onUpdate(JSON.parse(String(message.data)) as ChatUpdate);
    } catch {
      /* A malformed event is skipped. */
    }
  };
  source.addEventListener("chat", handler as EventListener);
  if (onError) source.addEventListener("error", onError);
  return () => {
    source.removeEventListener("chat", handler as EventListener);
    if (onError) source.removeEventListener("error", onError);
    source.close();
  };
}

/* Merges a stream update into the item list: reset replaces it, otherwise
   items are upserted by id in transcript order. */
export function mergeItems(current: ChatItem[], update: ChatUpdate): ChatItem[] {
  const incoming = update.items ?? [];
  if (update.reset) return incoming.slice();
  if (!incoming.length) return current;
  const index = new Map(current.map((item, i) => [item.id, i] as const));
  const next = current.slice();
  for (const item of incoming) {
    const at = index.get(item.id);
    if (at === undefined) {
      index.set(item.id, next.length);
      next.push(item);
    } else {
      next[at] = item;
    }
  }
  return next;
}
