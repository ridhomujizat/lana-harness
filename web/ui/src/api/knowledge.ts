import { ApiError, request } from "./client";

/* Knowledge upkeep calls of spec 31 (API-KNU-003 to API-KNU-006). The types are
   written by hand from the Go JSON struct; times are Unix milliseconds and 0
   means "never" (API-KNU-003). */

export interface KnowledgeCounts {
  pages: number;
  sources: number;
  journal_days: number;
}

export interface KnowledgeFailedSource {
  path: string;
  error: string;
}

export interface KnowledgeEmbedding {
  provider: string;
  model: string;
  chunks: number;
  vectors: number;
}

export interface KnowledgeConsolidation {
  running: boolean;
  last_at: number;
  last_error: string;
  next_at: number;
  report: string;
}

export interface KnowledgeLogEntry {
  heading: string;
  lines: string[];
}

export interface KnowledgeStatus {
  available: boolean;
  path: string;
  exists: boolean;
  counts: KnowledgeCounts;
  sources_pending: string[];
  sources_failed: KnowledgeFailedSource[];
  inbox_unsupported: string[];
  embedding: KnowledgeEmbedding;
  agent: string;
  agent_error: string;
  consolidation: KnowledgeConsolidation;
  log: KnowledgeLogEntry[];
}

export interface KnowledgeReport {
  path: string;
  markdown: string;
}

export interface KnowledgeUploadResponse {
  saved: string[];
}

export function getKnowledge(): Promise<KnowledgeStatus> {
  return request<KnowledgeStatus>("GET", "/v1/knowledge");
}

/* API-KNU-004: a missing report is a normal state, not an error. Any other
   failure is rethrown so the page can show it. */
export async function getKnowledgeReport(): Promise<KnowledgeReport | null> {
  try {
    return await request<KnowledgeReport>("GET", "/v1/knowledge/report");
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) {
      return null;
    }
    throw error;
  }
}

/* API-KNU-005: one "file" field per File; request() sends FormData as
   multipart and lets the browser set the boundary. */
export function uploadKnowledge(
  files: File[]
): Promise<KnowledgeUploadResponse> {
  const body = new FormData();
  for (const file of files) {
    body.append("file", file);
  }
  return request<KnowledgeUploadResponse>("POST", "/v1/knowledge/upload", body);
}

export function consolidateKnowledge(): Promise<{ started: boolean }> {
  return request<{ started: boolean }>("POST", "/v1/knowledge/consolidate");
}

/* API-KNU-006: a run in progress is reported with 409 busy and a stopped
   daemon with 503 unavailable. The page tells them apart by these helpers. */
export function isKnowledgeBusy(error: unknown): boolean {
  return (
    error instanceof ApiError && error.status === 409 && error.code === "busy"
  );
}

export function isKnowledgeUnavailable(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.status === 503 &&
    error.code === "unavailable"
  );
}
