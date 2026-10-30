import { request } from "./client";

/* Tool catalog calls of API-WEB-026. */

export interface ToolEntry {
  name: string;
  risk: string;
  asks_first: boolean;
  enabled: boolean;
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
