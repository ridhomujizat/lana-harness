import { request } from "./client";

/* MCP calls of API-WEB-027 and the agent `mcp` list edits of API-WEB-025.
   The config version travels with every write, like agent-detail.js does. */

export { listAgents } from "./agents";

/* A tool entry from a server row. The daemon reports a count today, so both
   tool fields are optional; older rows carried names and descriptions. */
export type McpToolWire =
  | string
  | { name?: string; id?: string; description?: string };

export interface McpServerRow {
  name: string;
  type: string;
  command: string;
  args: string[];
  url: string;
  env_keys: string[];
  header_keys: string[];
  tools_count: number;
  agents: string[];
  state: string;
  status: string;
  error: string;
  running_for_ms: number;
  tools?: McpToolWire[];
  tool_names?: McpToolWire[];
}

export interface McpDefinition {
  type?: string;
  command?: string;
  args?: string[];
  url?: string;
  env?: Record<string, string>;
  headers?: Record<string, string>;
}

export interface ListMcpResponse {
  version: string;
  servers: McpServerRow[];
}

export interface McpSaveResponse {
  name: string;
  version: string;
  restart_required: boolean;
}

export interface McpTestResult {
  ok: boolean;
  tool_count?: number;
  message?: string;
}

export interface McpRestartState {
  name: string;
  state: string;
  error: string;
}

export interface SaveAgentMcpResponse {
  name: string;
  version: string;
  restart_required: boolean;
}

export function listMcpServers(): Promise<ListMcpResponse> {
  return request<ListMcpResponse>("GET", "/v1/mcp");
}

export function listMcp(): Promise<ListMcpResponse> {
  return listMcpServers();
}

export function saveMcpServer(
  name: string,
  definition: McpDefinition,
  expectedVersion: string
): Promise<McpSaveResponse> {
  return request<McpSaveResponse>(
    "PUT",
    "/v1/mcp/" + encodeURIComponent(name),
    { ...definition, expected_version: expectedVersion }
  );
}

export function deleteMcpServer(
  name: string,
  expectedVersion: string
): Promise<McpSaveResponse> {
  return request<McpSaveResponse>(
    "DELETE",
    "/v1/mcp/" + encodeURIComponent(name),
    { expected_version: expectedVersion }
  );
}

export function testMcpServer(
  definition: McpDefinition
): Promise<McpTestResult> {
  return request<McpTestResult>("POST", "/v1/mcp/test", definition);
}

export function restartMcpServer(name: string): Promise<McpRestartState> {
  return request<McpRestartState>(
    "POST",
    "/v1/mcp/" + encodeURIComponent(name) + "/restart"
  );
}

export function saveAgentMcp(
  name: string,
  mcp: string[],
  expectedVersion: string
): Promise<SaveAgentMcpResponse> {
  return request<SaveAgentMcpResponse>(
    "PUT",
    "/v1/agents/" + encodeURIComponent(name),
    { mcp: Array.isArray(mcp) ? mcp : [], expected_version: expectedVersion }
  );
}
