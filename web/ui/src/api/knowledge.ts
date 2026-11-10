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

/* Spec 46 API-DKN-003: one row per scope, "shared" first, then each agent
   domain in name order. */
export interface KnowledgeScopeStat {
  scope: string;
  pages: number;
  journal_days: number;
  updated_at: number;
}

export interface KnowledgeStatus {
  /* Spec 46 additions; older daemons leave them out. */
  scopes?: KnowledgeScopeStat[];
  review_pending?: number;
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

/* Spec 46 API-DKN-004: one page of the vault, shared or in an agent domain. */
export interface KnowledgePageInfo {
  path: string;
  title: string;
  scope: string;
  type: string;
  description: string;
  updated: string;
  mtime: number;
}

export interface KnowledgePageList {
  pages: KnowledgePageInfo[];
  total: number;
}

export interface KnowledgePageDoc {
  path: string;
  title: string;
  scope: string;
  markdown: string;
}

export function getKnowledgePages(
  scope: string,
  q: string,
  limit = 200
): Promise<KnowledgePageList> {
  const params = new URLSearchParams();
  params.set("scope", scope || "all");
  if (q.trim()) {
    params.set("q", q.trim());
  }
  params.set("limit", String(limit));
  return request<KnowledgePageList>(
    "GET",
    "/v1/knowledge/pages?" + params.toString()
  );
}

export function getKnowledgePage(path: string): Promise<KnowledgePageDoc> {
  return request<KnowledgePageDoc>(
    "GET",
    "/v1/knowledge/page?path=" + encodeURIComponent(path)
  );
}

/* Spec 46 API-DKN-006: a pending proposal to an agent domain, or a skill an
   agent proposed. owner_turn is false when the item did not come from a turn
   the owner started; those wait for the owner (REQ-DKN-032). */
export interface ReviewItem {
  id: string;
  kind: "knowledge" | "skill";
  agent: string;
  from: string;
  title: string;
  action: string;
  description?: string;
  body: string;
  owner_turn: boolean;
  session_id: string;
  created_at: number;
}

export interface ReviewDecision {
  id: string;
  status: "approved" | "applied" | "rejected";
}

/* Sent on window after a decision so the sidebar badge refreshes. */
export const REVIEW_CHANGED_EVENT = "lana:review-changed";

export async function getReview(): Promise<ReviewItem[]> {
  const result = await request<{ items: ReviewItem[] | null }>(
    "GET",
    "/v1/review"
  );
  return result && Array.isArray(result.items) ? result.items : [];
}

async function decide(id: string, verb: string): Promise<ReviewDecision> {
  const result = await request<ReviewDecision>(
    "POST",
    "/v1/review/" + encodeURIComponent(id) + "/" + verb
  );
  window.dispatchEvent(new Event(REVIEW_CHANGED_EVENT));
  return result;
}

export function approveReview(id: string): Promise<ReviewDecision> {
  return decide(id, "approve");
}

export function rejectReview(id: string): Promise<ReviewDecision> {
  return decide(id, "reject");
}
