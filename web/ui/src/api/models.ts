import { queryString, request } from "./client";

/* The model catalog of GET /api/models (REQ-MDL-020). The Go handler answers
   the spec's /api/models path and the dashboard's /v1/models proxy with the
   same body. */

export interface CatalogModel {
  id: string;
  name: string;
  description: string;
  context_window: number;
  legacy: boolean;
  replacement: string;
  thinking: string;
  default_effort: string;
}

export interface CatalogEffort {
  value: string;
  description: string;
}

export interface ModelsResponse {
  models: CatalogModel[];
  efforts: CatalogEffort[];
}

export function getModels(provider: string): Promise<ModelsResponse> {
  return request<ModelsResponse>(
    "GET",
    "/v1/models" + queryString({ provider })
  );
}
