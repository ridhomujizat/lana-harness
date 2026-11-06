import { request } from "./client";

/* Tool catalog calls of API-WEB-026. */

/* ToolSource tells the Tools tab how the catalog arrived at the switch:
   built-ins are listed, auto-offered, or not listed; MCP tools are attached to
   the agent or not attached. */
export type ToolSource =
  | "listed"
  | "auto"
  | "not_listed"
  | "attached"
  | "not_attached";

export interface ToolEntry {
  name: string;
  risk: string;
  asks_first: boolean;
  enabled: boolean;
  source: ToolSource;
}

export interface ToolGroup {
  id: string;
  label: string;
  tools: ToolEntry[];
}

export interface ToolCatalogResponse {
  agent: string;
  groups: ToolGroup[];
}

export function getToolCatalog(agent: string): Promise<ToolCatalogResponse> {
  return request<ToolCatalogResponse>(
    "GET",
    "/v1/tools?agent=" + encodeURIComponent(agent)
  );
}
