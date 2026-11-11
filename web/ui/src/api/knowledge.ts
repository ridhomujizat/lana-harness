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
  /* Spec 48: the replaced text of a replace or supersede op. */
  old_text?: string;
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

/* Spec 48 API-KGR-001: the knowledge graph. Hub edges are not sent; every
   node joins the hub of its scope. */
export interface KnowledgeGraphNode {
  id: string;
  title: string;
  scope: string;
  kind: "page" | "journal" | "source";
  type: string;
  updated: string;
  degree: number;
}

export interface KnowledgeGraphEdge {
  source: string;
  target: string;
}

export interface KnowledgeGraphHub {
  scope: string;
  pages: number;
}

export interface KnowledgeGraph {
  nodes: KnowledgeGraphNode[];
  hubs: KnowledgeGraphHub[];
  edges: KnowledgeGraphEdge[];
  counts: { pages: number; links: number; broken: number };
  truncated: boolean;
}

export async function getKnowledgeGraph(
  scope: string,
  journals = false,
  sources = false
): Promise<KnowledgeGraph> {
  const params = new URLSearchParams();
  params.set("scope", scope || "all");
  if (journals) params.set("journals", "1");
  if (sources) params.set("sources", "1");
  const g = await request<KnowledgeGraph>(
    "GET",
    "/v1/knowledge/graph?" + params.toString()
  );
  return {
    nodes: g?.nodes ?? [],
    hubs: g?.hubs ?? [],
    edges: g?.edges ?? [],
    counts: g?.counts ?? { pages: 0, links: 0, broken: 0 },
    truncated: Boolean(g?.truncated),
  };
}

/* Spec 48 API-KGR-002: page detail fields added to KnowledgePageDoc. Older
   daemons leave them out, so every field is optional. */
export interface KnowledgeLinkRef {
  path: string;
  title: string;
  scope: string;
}

export interface KnowledgeBacklink extends KnowledgeLinkRef {
  context: string;
}

export interface KnowledgePageDetail extends KnowledgePageDoc {
  type?: string;
  description?: string;
  updated?: string;
  sources?: string[];
  links_out?: KnowledgeLinkRef[];
  unresolved?: string[];
  links_in?: KnowledgeBacklink[];
}

export function getKnowledgePageDetail(
  path: string
): Promise<KnowledgePageDetail> {
  return request<KnowledgePageDetail>(
    "GET",
    "/v1/knowledge/page?path=" + encodeURIComponent(path)
  );
}

/* Spec 48 API-KGR-003: decided knowledge proposals. */
export interface DecidedReviewItem extends ReviewItem {
  status?: string;
  result?: string;
  decided_at?: number;
}

export async function getDecidedReview(): Promise<DecidedReviewItem[]> {
  const result = await request<{ items: DecidedReviewItem[] | null }>(
    "GET",
    "/v1/review?status=decided"
  );
  return result && Array.isArray(result.items) ? result.items : [];
}

/* Spec 48 API-KGR-004: log.md entries, newest first. */
export type KnowledgeActivityKind =
  | "run"
  | "promotion"
  | "write"
  | "review"
  | "lint"
  | "error"
  | "other";

export interface KnowledgeActivityEntry {
  at: string;
  heading: string;
  kind: KnowledgeActivityKind;
  actor: string;
  lines: string[];
}

export interface KnowledgeActivityPage {
  entries: KnowledgeActivityEntry[];
  next_before: string;
}

export async function getKnowledgeActivity(
  opts: { limit?: number; before?: string; kind?: string } = {}
): Promise<KnowledgeActivityPage> {
  const params = new URLSearchParams();
  params.set("limit", String(opts.limit ?? 50));
  if (opts.before) params.set("before", opts.before);
  if (opts.kind) params.set("kind", opts.kind);
  const page = await request<KnowledgeActivityPage>(
    "GET",
    "/v1/knowledge/activity?" + params.toString()
  );
  return {
    entries: page?.entries ?? [],
    next_before: page?.next_before ?? "",
  };
}

/* Spec 48 API-KGR-005. */
export interface KnowledgeLintCounts {
  date: string;
  path: string;
  pages: number;
  contradictions: number;
  conflicts: number;
  duplicates: number;
  broken: number;
  orphans: number;
  missing: number;
}

export interface KnowledgeHealth {
  last_run: {
    at: string;
    journaled: number;
    applied: number;
    skipped: number;
    error: string;
  } | null;
  next_at: string;
  lint: KnowledgeLintCounts | null;
  review_pending: number;
  memory: { agent: string; characters: number; limit: number }[];
}

export async function getKnowledgeHealth(): Promise<KnowledgeHealth> {
  const h = await request<KnowledgeHealth>("GET", "/v1/knowledge/health");
  return {
    last_run: h?.last_run ?? null,
    next_at: h?.next_at ?? "",
    lint: h?.lint ?? null,
    review_pending: h?.review_pending ?? 0,
    memory: h?.memory ?? [],
  };
}
