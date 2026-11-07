import { queryString, request } from "./client";

/* LLM router calls of spec 37 (docs/specs/37-llm-router/api-contract.md,
   API-RTR-006..014) and the Claude Agent status of spec 38 (API-CCR-003).
   Times are RFC 3339 strings, money is float USD, token counts are integers.
   The provider calls of API-RTR-006 live here too; api/settings.ts re-exports
   them for older callers. */

/* ------------------------------------------------------------------ */
/* Providers (API-RTR-006)                                            */
/* ------------------------------------------------------------------ */

export type ProviderKind =
  | "openai"
  | "deepseek"
  | "anthropic"
  | "openrouter"
  | "openai_compat";

export interface ProviderRow {
  name: string;
  kind: string;
  base_url: string;
  api_key_env: string;
  key_state: string;
  used_by: string[];
  /* Spec 37: the preset base URL of the kind, and how many models it lists. */
  default_base_url?: string;
  models_count?: number;
}

export interface ListProvidersResponse {
  version: string;
  providers: ProviderRow[];
  restart_required: boolean;
}

export interface ProviderSaveResponse {
  name: string;
  version: string;
  restart_required: boolean;
}

export interface ProviderWriteRequest {
  kind: string;
  base_url?: string;
  api_key_env?: string;
  api_key?: string;
  headers?: Record<string, string>;
  expected_version: string;
}

export interface ProviderTestRequest {
  /* The provider being edited; with no model, the daemon tests the model an
     agent or combo already uses with it. */
  name?: string;
  kind: string;
  base_url?: string;
  /* Omitted unless the owner typed one: the daemon then picks a model it
     knows for the kind. */
  model?: string;
  api_key?: string;
  api_key_env?: string;
}

export interface ProviderTestResponse {
  ok: boolean;
  latency_ms?: number;
  status?: number;
  message?: string;
}

export function listProviders(): Promise<ListProvidersResponse> {
  return request<ListProvidersResponse>("GET", "/v1/providers");
}

export function saveProvider(
  name: string,
  body: ProviderWriteRequest
): Promise<ProviderSaveResponse> {
  return request<ProviderSaveResponse>(
    "PUT",
    "/v1/providers/" + encodeURIComponent(name),
    body
  );
}

/* A provider in use answers 409 {"error":"in_use","message":"used by …"}. */
export function deleteProvider(
  name: string,
  expectedVersion: string
): Promise<ProviderSaveResponse> {
  return request<ProviderSaveResponse>(
    "DELETE",
    "/v1/providers/" + encodeURIComponent(name),
    { expected_version: expectedVersion }
  );
}

export function testProvider(
  body: ProviderTestRequest
): Promise<ProviderTestResponse> {
  return request<ProviderTestResponse>("POST", "/v1/providers/test", body);
}

/* ------------------------------------------------------------------ */
/* Overview (API-RTR-008)                                             */
/* ------------------------------------------------------------------ */

export interface RouterProvider {
  name: string;
  kind: string;
  base_url: string;
  key_state: string;
  used_by: string[];
}

export interface RouterCombo {
  name: string;
  description: string;
  models: string[];
  used_by: string[];
}

export interface RouterCooling {
  target: string;
  until: string;
}

export interface RouterProxyInfo {
  enabled: boolean;
  base_url: string;
}

/* One entry of the agent model picker: group "Combos" or a provider name. */
export interface RouterChoice {
  value: string;
  label: string;
  group: string;
}

export interface RouterOverview {
  version: string;
  providers: RouterProvider[];
  combos: RouterCombo[];
  cooling: RouterCooling[];
  proxy: RouterProxyInfo;
  choices: RouterChoice[];
}

export const COMBOS_GROUP = "Combos";

/* Missing lists become empty so callers never guard each field. */
export async function getRouter(): Promise<RouterOverview> {
  const data = await request<Partial<RouterOverview> | null>("GET", "/v1/router");
  const value = data || {};
  return {
    version: typeof value.version === "string" ? value.version : "",
    providers: Array.isArray(value.providers) ? value.providers : [],
    combos: Array.isArray(value.combos) ? value.combos : [],
    cooling: Array.isArray(value.cooling) ? value.cooling : [],
    proxy:
      value.proxy && typeof value.proxy === "object"
        ? value.proxy
        : { enabled: false, base_url: "" },
    choices: Array.isArray(value.choices) ? value.choices : [],
  };
}

/* ------------------------------------------------------------------ */
/* Combos (API-RTR-007)                                               */
/* ------------------------------------------------------------------ */

export interface ComboWriteRequest {
  description: string;
  models: string[];
  expected_version: string;
}

export interface RouterWriteResponse {
  version: string;
  restart_required: boolean;
}

export interface ValidationIssue {
  path: string;
  message: string;
}

export function saveCombo(
  name: string,
  body: ComboWriteRequest
): Promise<RouterWriteResponse> {
  return request<RouterWriteResponse>(
    "PUT",
    "/v1/router/combos/" + encodeURIComponent(name),
    body
  );
}

export function deleteCombo(
  name: string,
  expectedVersion: string
): Promise<RouterWriteResponse> {
  return request<RouterWriteResponse>(
    "DELETE",
    "/v1/router/combos/" + encodeURIComponent(name),
    { expected_version: expectedVersion }
  );
}

/* ------------------------------------------------------------------ */
/* Models (API-RTR-009, API-RTR-010)                                  */
/* ------------------------------------------------------------------ */

export interface ModelPrice {
  input: number;
  cached_input: number;
  output: number;
}

export interface RouterModel {
  id: string;
  address: string;
  name: string;
  context_window: number;
  max_output: number;
  vision: boolean;
  thinking: boolean;
  price: ModelPrice | null;
  source: string;
  used_by?: { agents?: string[]; combos?: string[] };
}

export interface ProviderModels {
  provider: string;
  fetched_at: string;
  fetch_error: string;
  models: RouterModel[];
}

function normalizeProviderModels(value: Partial<ProviderModels>): ProviderModels {
  return {
    provider: String(value.provider || ""),
    fetched_at: String(value.fetched_at || ""),
    fetch_error: String(value.fetch_error || ""),
    models: Array.isArray(value.models) ? value.models : [],
  };
}

/* One provider answers {provider, models}; no provider answers
   {providers: [...]}. Both come back as a list. */
export async function listRouterModels(
  provider?: string,
  refresh?: boolean
): Promise<ProviderModels[]> {
  const data = await request<
    (Partial<ProviderModels> & { providers?: Partial<ProviderModels>[] }) | null
  >(
    "GET",
    "/v1/router/models" +
      queryString({ provider: provider || undefined, refresh: refresh ? 1 : undefined })
  );
  if (!data) {
    return [];
  }
  if (Array.isArray(data.providers)) {
    return data.providers.map(normalizeProviderModels);
  }
  return [normalizeProviderModels(data)];
}

export interface ModelTestResponse {
  ok: boolean;
  latency_ms?: number;
  class?: string;
  status?: number;
  message?: string;
}

export function testRouterModel(address: string): Promise<ModelTestResponse> {
  return request<ModelTestResponse>("POST", "/v1/router/test", { address });
}

/* ------------------------------------------------------------------ */
/* Usage (API-RTR-011)                                                */
/* ------------------------------------------------------------------ */

export type RouterRange = "24h" | "7d" | "30d";

export interface UsageStats {
  calls: number;
  errors: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  /* null or a partial total when a model had no known price. */
  cost_usd: number | null;
  cost_partial: boolean;
  ttft_p50_ms: number;
  ttft_p95_ms?: number;
}

export interface UsagePoint {
  t: string;
  calls: number;
  errors: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens?: number;
  cost_usd: number | null;
}

export interface UsageProviderRow extends UsageStats {
  provider: string;
}

export interface UsageModelRow extends UsageStats {
  address: string;
  provider?: string;
  model?: string;
}

export interface UsageSourceRow {
  source: string;
  calls: number;
  errors?: number;
  cost_usd: number | null;
  cost_partial?: boolean;
  input_tokens?: number;
  output_tokens?: number;
}

export interface RouterUsage {
  range: RouterRange;
  totals: UsageStats;
  series: UsagePoint[];
  by_provider: UsageProviderRow[];
  by_model: UsageModelRow[];
  by_source: UsageSourceRow[];
}

const EMPTY_STATS: UsageStats = {
  calls: 0,
  errors: 0,
  input_tokens: 0,
  output_tokens: 0,
  cached_tokens: 0,
  cost_usd: 0,
  cost_partial: false,
  ttft_p50_ms: 0,
  ttft_p95_ms: 0,
};

export async function getRouterUsage(range: RouterRange): Promise<RouterUsage> {
  const data = await request<Partial<RouterUsage> | null>(
    "GET",
    "/v1/router/usage" + queryString({ range })
  );
  const value = data || {};
  return {
    range,
    totals: { ...EMPTY_STATS, ...(value.totals || {}) },
    series: Array.isArray(value.series) ? value.series : [],
    by_provider: Array.isArray(value.by_provider) ? value.by_provider : [],
    by_model: Array.isArray(value.by_model) ? value.by_model : [],
    by_source: Array.isArray(value.by_source) ? value.by_source : [],
  };
}

/* ------------------------------------------------------------------ */
/* Calls (API-RTR-012)                                                */
/* ------------------------------------------------------------------ */

export interface RouterCall {
  id: number;
  at: string;
  source: string;
  agent: string;
  session_id: string;
  turn_id?: string;
  requested: string;
  provider: string;
  model: string;
  attempt: number;
  /* "ok" or the error class. */
  outcome: string;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  cost_usd: number | null;
  ttft_ms: number;
  duration_ms: number;
  error: string;
}

export interface CallsPage {
  calls: RouterCall[];
  /* The id of the last returned row; 0 when there are no older rows. The
     next page passes it as `before`, which is exclusive. */
  next_before: number;
}

export interface CallsQuery {
  limit?: number;
  before?: number;
  provider?: string;
  status?: "" | "ok" | "error";
  source?: string;
}

export async function listRouterCalls(query: CallsQuery = {}): Promise<CallsPage> {
  const data = await request<Partial<CallsPage> | null>(
    "GET",
    "/v1/router/calls" +
      queryString({
        limit: query.limit,
        before: query.before && query.before > 0 ? query.before : undefined,
        provider: query.provider,
        status: query.status,
        source: query.source,
      })
  );
  return {
    calls: data && Array.isArray(data.calls) ? data.calls : [],
    next_before: data && Number(data.next_before) > 0 ? Number(data.next_before) : 0,
  };
}

/* ------------------------------------------------------------------ */
/* Limits (API-RTR-013)                                               */
/* ------------------------------------------------------------------ */

export interface RateLimitValue {
  limit: number;
  remaining: number;
  reset_at?: string;
}

export interface RateLimits {
  requests?: RateLimitValue | null;
  tokens?: RateLimitValue | null;
  input_tokens?: RateLimitValue | null;
  output_tokens?: RateLimitValue | null;
}

export interface CreditLimit {
  limit_usd: number | null;
  used_usd: number;
  remaining_usd: number | null;
}

export interface BalanceLimit {
  currency: string;
  total: number;
  granted: number;
  topped_up: number;
}

export interface PlanWindow {
  /* five_hour, seven_day, … */
  type: string;
  status: string;
  /* A fraction: 0.42 is 42 %. */
  utilization?: number | null;
  resets_at?: string;
}

export interface LimitSnapshot {
  provider: string;
  kind?: string;
  updated_at?: string;
  error?: string;
  rate?: RateLimits | null;
  credit?: CreditLimit | null;
  balance?: BalanceLimit | null;
  windows?: PlanWindow[] | null;
}

export const CLAUDE_AGENT_PROVIDER = "claude-agent";

export async function getRouterLimits(refresh?: boolean): Promise<LimitSnapshot[]> {
  const data = await request<{ limits?: LimitSnapshot[] } | null>(
    "GET",
    "/v1/router/limits" + queryString({ refresh: refresh ? 1 : undefined })
  );
  return data && Array.isArray(data.limits) ? data.limits : [];
}

/* ------------------------------------------------------------------ */
/* Claude Agent (spec 38, API-CCR-003)                                */
/* ------------------------------------------------------------------ */

export interface ClaudeAgentStatus {
  configured_agents: string[];
  binary: string;
  version: string;
  version_ok: boolean;
  logged_in: boolean;
  auth_method: string;
  error: string;
  running: number;
  queued: number;
  max_concurrent: number;
}

export async function getClaudeAgent(): Promise<ClaudeAgentStatus> {
  const data = await request<Partial<ClaudeAgentStatus> | null>(
    "GET",
    "/v1/router/claude-agent"
  );
  const value = data || {};
  return {
    configured_agents: Array.isArray(value.configured_agents)
      ? value.configured_agents
      : [],
    binary: String(value.binary || ""),
    version: String(value.version || ""),
    version_ok: value.version_ok !== false,
    logged_in: value.logged_in === true,
    auth_method: String(value.auth_method || ""),
    error: String(value.error || ""),
    running: Number(value.running) || 0,
    queued: Number(value.queued) || 0,
    max_concurrent: Number(value.max_concurrent) || 0,
  };
}

/* ------------------------------------------------------------------ */
/* Proxy switch and keys (API-RTR-014)                                */
/* ------------------------------------------------------------------ */

export function setProxyEnabled(
  enabled: boolean,
  expectedVersion: string
): Promise<RouterWriteResponse> {
  return request<RouterWriteResponse>("PUT", "/v1/router/proxy", {
    enabled,
    expected_version: expectedVersion,
  });
}

export interface ProxyKey {
  id: string;
  name: string;
  prefix: string;
  created_at: string;
  last_used_at: string | null;
}

export interface CreatedProxyKey {
  id: string;
  name: string;
  prefix: string;
  /* The full key. It appears only in this response. */
  key: string;
}

export async function listProxyKeys(): Promise<ProxyKey[]> {
  const data = await request<{ keys?: ProxyKey[] } | null>("GET", "/v1/router/keys");
  return data && Array.isArray(data.keys) ? data.keys : [];
}

export function createProxyKey(name: string): Promise<CreatedProxyKey> {
  return request<CreatedProxyKey>("POST", "/v1/router/keys", { name });
}

export async function revokeProxyKey(id: string): Promise<void> {
  await request<unknown>("DELETE", "/v1/router/keys/" + encodeURIComponent(id));
}
