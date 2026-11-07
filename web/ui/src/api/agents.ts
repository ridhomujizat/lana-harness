import { request } from "./client";
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
  kind: string;
  title: string;
  color: string;
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
  /* Spec 38 (API-CCR-004): "claude-code" for a Claude Agent, "lana" or empty
     for lana's own loop; engine_label is the name to show ("Claude Agent"). */
  runtime?: string;
  engine_label?: string;
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
  /* Spec 38 (API-CCR-001): "claude-code" or "" (lana). */
  runtime?: string;
  auth?: string;
  api_key_env?: string;
  binary?: string;
  allow_scheduled?: boolean;
  provider?: string;
  model?: string;
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
  /* lana agents start from a template; a Claude Agent has none. */
  template?: string;
  workspace?: string;
  permission?: string;
  description?: string;
  /* Spec 38 (API-CCR-001): the Claude Agent keys. A Claude Agent never
     sends provider. */
  runtime?: string;
  model?: string;
  reasoning_effort?: string;
  auth?: string;
  api_key_env?: string;
  binary?: string;
  allow_scheduled?: boolean;
  rules?: { allow?: string[]; deny?: string[] };
  title?: string;
  color?: string;
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
  return request<ListAgentsResponse>("GET", "/v1/agents");
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
  newName: string
): Promise<AgentCreatedResponse> {
  return request<AgentCreatedResponse>(
    "POST",
    "/v1/agents/" + encodeURIComponent(name) + "/duplicate",
    { name: newName }
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
