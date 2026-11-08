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
  /* Revision 2: a disabled entry is skipped by the router. */
  disabled?: boolean;
  /* Spec 39 (API-CCT-010): a Claude Code account has kind "claude-code";
     the built-in one (~/.claude, no table) has builtin true. */
  builtin?: boolean;
  config_dir?: string;
  allow_scheduled?: boolean;
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
  /* Spec 39 (API-CCT-010): a saved Claude Code account answers the command
     that logs it in once: CLAUDE_CONFIG_DIR='<dir>' claude, then /login. */
  login_command?: string;
  /* A deleted Claude Code account keeps its folder; the answer says where. */
  config_dir?: string;
  message?: string;
}

export interface ProviderWriteRequest {
  kind: string;
  base_url?: string;
  api_key_env?: string;
  api_key?: string;
  headers?: Record<string, string>;
  /* Revision 2: absent keeps the stored value. */
  disabled?: boolean;
  /* Spec 39 (API-CCT-010): the keys of a kind "claude-code" account. */
  config_dir?: string;
  binary?: string;
  auth?: string;
  allow_scheduled?: boolean;
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
  disabled?: boolean;
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

/* Router records and limits of Claude Code turns before spec 39 used the
   provider name claude-agent; old rows keep it (API-CCT-008). */
export const CLAUDE_AGENT_PROVIDER = "claude-agent";

export async function getRouterLimits(refresh?: boolean): Promise<LimitSnapshot[]> {
  const data = await request<{ limits?: LimitSnapshot[] } | null>(
    "GET",
    "/v1/router/limits" + queryString({ refresh: refresh ? 1 : undefined })
  );
  return data && Array.isArray(data.limits) ? data.limits : [];
}

/* ------------------------------------------------------------------ */
/* Claude Code (spec 38 API-CCR-003, spec 39 API-CCT-005/011)         */
/* ------------------------------------------------------------------ */

/* The built-in Claude Code account and address prefix (API-CCT-001). */
export const CLAUDE_CODE_PROVIDER = "claude-code";

/* One Claude Code account (API-CCT-011). email and plan come from that
   account's `claude auth status`; they are never stored. */
export interface ClaudeAccount {
  name: string;
  builtin: boolean;
  config_dir: string;
  binary: string;
  version: string;
  version_ok: boolean;
  logged_in: boolean;
  email: string;
  plan: string;
  auth_method: string;
  error: string;
  running: number;
  queued: number;
  login_command: string;
  windows: PlanWindow[];
  agents: string[];
  combos: string[];
}

export interface ClaudeAgentStatus {
  /* Agents as name, combos as combo:<name> (API-CCT-005). */
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
  /* Revision 2 (API-CCT-011): one entry per account. An older daemon sends
     none; the client then describes the built-in account from the top-level
     fields. */
  accounts: ClaudeAccount[];
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.map((item) => String(item || "")).filter(Boolean) : [];
}

function normalizeAccount(value: Partial<ClaudeAccount> | null | undefined): ClaudeAccount {
  const entry = value || {};
  return {
    name: String(entry.name || CLAUDE_CODE_PROVIDER),
    builtin: entry.builtin === true,
    config_dir: String(entry.config_dir || ""),
    binary: String(entry.binary || ""),
    version: String(entry.version || ""),
    version_ok: entry.version_ok !== false,
    logged_in: entry.logged_in === true,
    email: String(entry.email || ""),
    plan: String(entry.plan || ""),
    auth_method: String(entry.auth_method || ""),
    error: String(entry.error || ""),
    running: Number(entry.running) || 0,
    queued: Number(entry.queued) || 0,
    login_command: String(entry.login_command || ""),
    windows: Array.isArray(entry.windows) ? entry.windows : [],
    agents: strings(entry.agents),
    combos: strings(entry.combos),
  };
}

export async function getClaudeAgent(): Promise<ClaudeAgentStatus> {
  const data = await request<Partial<ClaudeAgentStatus> | null>(
    "GET",
    "/v1/router/claude-agent"
  );
  const value = data || {};
  const status: ClaudeAgentStatus = {
    configured_agents: strings(value.configured_agents),
    binary: String(value.binary || ""),
    version: String(value.version || ""),
    version_ok: value.version_ok !== false,
    logged_in: value.logged_in === true,
    auth_method: String(value.auth_method || ""),
    error: String(value.error || ""),
    running: Number(value.running) || 0,
    queued: Number(value.queued) || 0,
    max_concurrent: Number(value.max_concurrent) || 0,
    accounts: [],
  };
  if (Array.isArray(value.accounts) && value.accounts.length > 0) {
    status.accounts = value.accounts.map((entry) => normalizeAccount(entry));
  } else if (data) {
    const users = status.configured_agents;
    status.accounts = [
      normalizeAccount({
        name: CLAUDE_CODE_PROVIDER,
        builtin: true,
        binary: status.binary,
        version: status.version,
        version_ok: status.version_ok,
        logged_in: status.logged_in,
        auth_method: status.auth_method,
        error: status.error,
        running: status.running,
        queued: status.queued,
        login_command: "claude",
        agents: users.filter((name) => !name.startsWith("combo:")),
        combos: users.filter((name) => name.startsWith("combo:")).map((name) => name.slice(6)),
      }),
    ];
  }
  return status;
}

/* API-CCT-013: the custom Claude Code model IDs ([router] claude_code_models).
   The body is the whole list. */
export interface ClaudeCodeModelsResponse {
  models: string[];
  version: string;
  restart_required: boolean;
}

export function saveClaudeCodeModels(
  models: string[],
  expectedVersion: string
): Promise<ClaudeCodeModelsResponse> {
  return request<ClaudeCodeModelsResponse>("PUT", "/v1/router/claude-code/models", {
    models,
    expected_version: expectedVersion,
  });
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
