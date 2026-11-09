import { request } from "./client";
import { rememberAvatars } from "../lib/avatar-store";
import type { ConfigAgent, GetConfigResponse } from "./settings";

/* Agent calls of API-WEB-012, API-WEB-016, API-WEB-025, and the tab loader of
   api/agent-tabs.js. */

export { errorMessage, isErrorCode } from "./client";

export interface McpStatus {
  name: string;
  state: string;
  error: string;
}

export interface Agent {
  name: string;
  /* display_name is the human name, "" when unset (falls back to name). */
  display_name?: string;
  kind: string;
  title: string;
  /* A color token (brand, success, warning, error, blue, gray) or, since
     spec 43, a "#rrggbb" hex. */
  color: string;
  /* Spec 43: the sanitized avatar of every agent and team: a saved one or
     the default seeded from the name. Missing on an older server. */
  avatar_svg?: string;
  avatar_source?: AvatarSource;
  model: string | null;
  policy: string;
  lead?: string;
  members?: string[];
  skills: string[];
  mcp: string[];
  mcp_status: McpStatus[];
  description: string;
  default: boolean;
  provider: string;
  permission: string;
  workspace: string;
  disabled_tools: string[];
  reasoning_effort?: string;
  /* Spec 39 (API-CCT-006): runtime is "claude-code" only for a legacy file
     (model then holds the mapped claude-code address); the UI never shows an
     engine. claude_code is true when the agent's first target runs in
     Claude Code. */
  runtime?: string;
  claude_code?: boolean;
  tools_count: number;
  sessions_count: number;
  last_active_at: number | null;
}

export interface ListAgentsResponse {
  agents: Agent[];
}

/* AgentWriteFields is the editable part of [agents.<name>]. Every key is
   optional because PUT accepts a subset (API-WEB-025). */
export interface AgentWriteFields {
  /* Spec 39 (API-CCT-002): the dashboard never writes runtime; a Claude Code
     agent is model = "<account>/<model>". provider is only written as "" to
     clear a legacy provider/model pair. */
  provider?: string;
  model?: string;
  display_name?: string;
  title?: string;
  color?: string;
  fallback?: string[];
  prompt_file?: string;
  workspace?: string;
  tools?: string[];
  disabled_tools?: string[];
  subagents?: string[];
  mcp?: string[];
  skills?: string[];
  permission?: string;
  description?: string;
  max_steps?: number;
  context_window?: number;
  memory_capture?: boolean;
  workspace_only?: boolean;
  reasoning_effort?: string;
  rules?: { allow?: string[]; deny?: string[] };
  expected_version?: string;
}

/* CreateAgentRequest is the POST /v1/agents body (API-WEB-025). The template
   picks the starting tools, prompt, and limits. */
export interface CreateAgentRequest {
  name: string;
  display_name?: string;
  /* Every agent is a lana agent and starts from a template (spec 39). */
  template: string;
  workspace?: string;
  /* A router address, combo, or "<account>/<model>" of Claude Code; empty
     starts on the default agent's model. Never a provider or runtime. */
  model?: string;
  reasoning_effort?: string;
}

export interface AgentCreatedResponse {
  name: string;
  restart_required: boolean;
}

export interface PutAgentResponse {
  name: string;
  version: string;
  restart_required: boolean;
}

export interface AgentFileResponse {
  content: string;
  version: string;
  characters: number;
  limit: number | null;
}

export interface PutAgentFileResponse {
  version: string;
  characters: number;
}

export interface AgentTabData {
  version: string;
  agent: ConfigAgent;
  row: Partial<Agent>;
}

export function listAgents(): Promise<ListAgentsResponse> {
  return request<ListAgentsResponse>("GET", "/v1/agents").then((page) => {
    rememberAvatars(page && Array.isArray(page.agents) ? page.agents : []);
    return page;
  });
}

export function createAgent(
  body: CreateAgentRequest
): Promise<AgentCreatedResponse> {
  return request<AgentCreatedResponse>("POST", "/v1/agents", body);
}

export function updateAgent(
  name: string,
  fields: AgentWriteFields,
  expectedVersion: string
): Promise<PutAgentResponse> {
  return request<PutAgentResponse>(
    "PUT",
    "/v1/agents/" + encodeURIComponent(name),
    { ...fields, expected_version: expectedVersion }
  );
}

export function duplicateAgent(
  name: string,
  newName: string,
  displayName?: string
): Promise<AgentCreatedResponse> {
  const body: { name: string; display_name?: string } = { name: newName };
  if (displayName) {
    body.display_name = displayName;
  }
  return request<AgentCreatedResponse>(
    "POST",
    "/v1/agents/" + encodeURIComponent(name) + "/duplicate",
    body
  );
}

export function getAgentFile(
  agent: string,
  file: string
): Promise<AgentFileResponse> {
  return request<AgentFileResponse>(
    "GET",
    "/v1/agents/" +
      encodeURIComponent(agent) +
      "/files/" +
      encodeURIComponent(file)
  );
}

export function saveAgentFile(
  agent: string,
  file: string,
  content: string,
  expectedVersion: string
): Promise<PutAgentFileResponse> {
  return request<PutAgentFileResponse>(
    "PUT",
    "/v1/agents/" +
      encodeURIComponent(agent) +
      "/files/" +
      encodeURIComponent(file),
    { content, expected_version: expectedVersion }
  );
}

/* The General tab reads the config for its version and settings. The tabs use
   the same read plus the agent row for keys the settings view omits
   (disabled_tools). */
export async function loadAgentTab(name: string): Promise<AgentTabData> {
  const [config, page] = await Promise.all([
    request<GetConfigResponse>("GET", "/v1/config"),
    listAgents(),
  ]);
  const settings = config && config.settings ? config.settings : null;
  const agents = settings ? settings.agents : {};
  const rows = page && Array.isArray(page.agents) ? page.agents : [];
  return {
    version: config && config.version ? String(config.version) : "",
    agent: agents[name] || {},
    row: rows.find((item) => item && item.name === name) || {},
  };
}

/* ------------------------------------------------------------------ */
/* Generated avatars (spec 43)                                        */
/* ------------------------------------------------------------------ */

export interface GenerateAvatarRequest {
  /* A router combo or "provider/model" address; omitted uses the agent's
     background model. Never a Claude Code model. */
  model?: string;
  seed?: number;
}

export interface GenerateAvatarResponse {
  svg: string;
  /* "" when the built-in generator ran. */
  model: string;
  seed: number;
  /* "local" when no model was available or its output was invalid. */
  source: "model" | "local";
}

/* "saved" for a chosen avatar, "default" for the one seeded from the name. */
export type AvatarSource = "saved" | "default";

export interface PutAvatarResponse {
  name: string;
  avatar_svg: string;
  avatar_source?: AvatarSource;
  color: string;
  restart_required: boolean;
}

/* DELETE puts the agent back on its default avatar and returns it. */
export interface DeleteAvatarResponse {
  name: string;
  avatar_svg: string;
  avatar_source?: AvatarSource;
  color: string;
}

/* The server-rendered avatar for an <img>, colors baked in for the theme. */
export function agentAvatarSvgUrl(name: string, theme: "light" | "dark"): string {
  return avatarPath(name) + ".svg?theme=" + theme;
}

function avatarPath(name: string): string {
  return "/v1/agents/" + encodeURIComponent(name) + "/avatar";
}

export function generateAgentAvatar(
  name: string,
  body: GenerateAvatarRequest = {}
): Promise<GenerateAvatarResponse> {
  return request<GenerateAvatarResponse>("POST", avatarPath(name) + "/generate", body);
}

export function putAgentAvatar(
  name: string,
  body: { svg: string; color?: string }
): Promise<PutAvatarResponse> {
  return request<PutAvatarResponse>("PUT", avatarPath(name), body);
}

export function deleteAgentAvatar(name: string): Promise<DeleteAvatarResponse> {
  return request<DeleteAvatarResponse>("DELETE", avatarPath(name));
}
