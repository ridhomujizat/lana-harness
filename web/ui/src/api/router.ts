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
  | "commandcode"
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
  /* REQ-CCT-012: the display label of an added Claude Code account. */
  label?: string;
  /* Spec 75 (REQ-PAF-005): the keys of an API provider in use order:
     api_key_env first, then api_key_envs. Secret names only. */
  keys?: ProviderKey[];
  /* Spec 80 (REQ-ACP-004): an API connection's place in its connection
     pool: its priority (default 100), false only with pool = false, and a
     group equal for two connections exactly when they share kind and base
     URL. An older daemon sends none of them. */
  priority?: number;
  pool?: boolean;
  pool_group?: string;
}

/* One key of an API provider (spec 75): its secret name, whether the
   secret holds a value, and the end of its cooldown. */
export interface ProviderKey {
  name: string;
  present: boolean;
  cooling_until: string | null;
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
     that signs it in from a terminal. */
  login_command?: string;
  /* A deleted Claude Code account keeps its folder unless remove_folder was
     asked (REQ-CCT-012); the answer says where. */
  config_dir?: string;
  message?: string;
  folder_removed?: boolean;
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

/* A provider in use answers 409 {"error":"in_use","message":"used by …"}.
   removeFolder (REQ-CCT-012) signs a Claude Code account out and deletes its
   folder when it lies in lana's claude folder. */
export function deleteProvider(
  name: string,
  expectedVersion: string,
  options?: { removeFolder?: boolean }
): Promise<ProviderSaveResponse> {
  return request<ProviderSaveResponse>(
    "DELETE",
    "/v1/providers/" + encodeURIComponent(name) + (options && options.removeFolder ? "?remove_folder=1" : ""),
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
  /* Spec 76 (REQ-MES-002): the model's own effort levels, in the order to
     show them (any strings, such as minimal, low, xhigh, max), and its
     default level ("" when unknown). No efforts: no effort control. */
  efforts?: string[];
  default_effort?: string;
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
  /* Spec 76 (REQ-MES-002): as on RouterChoice. */
  efforts?: string[];
  default_effort?: string;
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
  /* A name for a window the UI has no fixed name for, such as
     "Weekly · Fable" (spec 39 Revision 4). */
  label?: string;
  /* "stream" (a turn's rate_limit_event) or "usage_probe" (claude /usage). */
  source?: string;
}

/* A provider's subscription plan, such as Command Code's (spec 79). */
export interface LimitPlan {
  /* The plan's display name ("Pro"), else its raw id. */
  name: string;
  /* "active", "trialing", "past_due", … */
  status?: string;
  /* The end of the current billing period. */
  renews_at?: string;
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
  plan?: LimitPlan | null;
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

/* Spec 39 Revision 4: refresh the providers' balances and probe the plan
   usage of every Claude Code account (claude /usage, no model call). The
   daemon probes an account at most once a minute and answers with the
   snapshots either way. */
export async function refreshRouterLimits(): Promise<LimitSnapshot[]> {
  const data = await request<{ limits?: LimitSnapshot[] } | null>("POST", "/v1/router/limits/refresh");
  return data && Array.isArray(data.limits) ? data.limits : [];
}

/* ------------------------------------------------------------------ */
/* Claude Code (spec 38 API-CCR-003, spec 39 API-CCT-005/011)         */
/* ------------------------------------------------------------------ */

/* The built-in Claude Code account and address prefix (API-CCT-001). */
export const CLAUDE_CODE_PROVIDER = "claude-code";

/* The last sign-in of one account (REQ-CCT-012). error is set for failed
   only and never holds a URL or token. */
export type ClaudeSignInState = "idle" | "running" | "succeeded" | "failed" | "cancelled";

export interface ClaudeSignIn {
  state: ClaudeSignInState;
  error: string;
  started_at: string | null;
  finished_at: string | null;
}

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
  /* REQ-CCT-012: the stored label, the name to show (label, else email,
     else name), and the last sign-in. */
  label: string;
  display_name: string;
  sign_in: ClaudeSignIn;
  /* Spec 75 (REQ-PAF-006): pool order (lower first), whether a pooled
     address may use the account, and the end of its cooldown. Optional for
     older daemons; the client reads a missing pool as true. */
  priority?: number;
  pool?: boolean;
  cooling_until?: string | null;
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
  /* Spec 75: more than one pooled personal account. */
  pool_warning?: boolean;
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.map((item) => String(item || "")).filter(Boolean) : [];
}

const SIGN_IN_STATES: ClaudeSignInState[] = ["idle", "running", "succeeded", "failed", "cancelled"];

export function normalizeSignIn(value: Partial<ClaudeSignIn> | null | undefined): ClaudeSignIn {
  const entry = value || {};
  const state = SIGN_IN_STATES.includes(entry.state as ClaudeSignInState) ? (entry.state as ClaudeSignInState) : "idle";
  return {
    state,
    error: String(entry.error || ""),
    started_at: entry.started_at ? String(entry.started_at) : null,
    finished_at: entry.finished_at ? String(entry.finished_at) : null,
  };
}

function normalizeAccount(value: Partial<ClaudeAccount> | null | undefined): ClaudeAccount {
  const entry = value || {};
  const name = String(entry.name || CLAUDE_CODE_PROVIDER);
  const email = String(entry.email || "");
  const label = String(entry.label || "");
  return {
    name,
    builtin: entry.builtin === true,
    config_dir: String(entry.config_dir || ""),
    binary: String(entry.binary || ""),
    version: String(entry.version || ""),
    version_ok: entry.version_ok !== false,
    logged_in: entry.logged_in === true,
    email,
    plan: String(entry.plan || ""),
    auth_method: String(entry.auth_method || ""),
    error: String(entry.error || ""),
    running: Number(entry.running) || 0,
    queued: Number(entry.queued) || 0,
    login_command: String(entry.login_command || ""),
    windows: Array.isArray(entry.windows) ? entry.windows : [],
    agents: strings(entry.agents),
    combos: strings(entry.combos),
    label,
    display_name: String(entry.display_name || "") || label || email || name,
    sign_in: normalizeSignIn(entry.sign_in),
    priority: Number(entry.priority) || 0,
    pool: entry.pool !== false,
    cooling_until: entry.cooling_until ? String(entry.cooling_until) : null,
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
    pool_warning: value.pool_warning === true,
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

/* REQ-CCT-012: one-click sign-in. The daemon starts the official
   `claude auth login` for the account; Claude Code opens its own sign-in page
   in the browser. lana never sees the password or tokens. */
export interface ClaudeAccountCreated {
  name: string;
  config_dir: string;
  version: string;
  restart_required: boolean;
  login_command: string;
  sign_in: ClaudeSignIn;
}

const ACCOUNTS_PATH = "/v1/router/claude-code/accounts";

function accountPath(name: string, rest: string): string {
  return ACCOUNTS_PATH + "/" + encodeURIComponent(name) + rest;
}

/* Adds an account (name default claude-2, claude-3, …) and starts its
   sign-in. */
export async function createClaudeAccount(name?: string): Promise<ClaudeAccountCreated> {
  const data = await request<Partial<ClaudeAccountCreated> | null>("POST", ACCOUNTS_PATH, name ? { name } : {});
  const value = data || {};
  return {
    name: String(value.name || ""),
    config_dir: String(value.config_dir || ""),
    version: String(value.version || ""),
    restart_required: value.restart_required === true,
    login_command: String(value.login_command || ""),
    sign_in: normalizeSignIn(value.sign_in),
  };
}

/* Starts a sign-in; 409 sign_in_running while one runs. */
export async function startClaudeSignIn(name: string): Promise<ClaudeSignIn> {
  return normalizeSignIn(await request<Partial<ClaudeSignIn> | null>("POST", accountPath(name, "/sign-in"), {}));
}

export async function getClaudeSignIn(name: string): Promise<ClaudeSignIn> {
  return normalizeSignIn(await request<Partial<ClaudeSignIn> | null>("GET", accountPath(name, "/sign-in")));
}

export async function cancelClaudeSignIn(name: string): Promise<ClaudeSignIn> {
  return normalizeSignIn(await request<Partial<ClaudeSignIn> | null>("DELETE", accountPath(name, "/sign-in")));
}

/* Signs the account out and answers its refreshed row. The built-in account
   needs confirm, since it signs the owner out of their own Claude Code. */
export async function signOutClaudeAccount(name: string, confirm?: boolean): Promise<ClaudeAccount> {
  const data = await request<Partial<ClaudeAccount> | null>(
    "POST",
    accountPath(name, "/sign-out"),
    confirm ? { confirm: true } : {}
  );
  return normalizeAccount(data);
}

export interface ClaudeLabelResponse {
  name: string;
  label: string;
  version: string;
  restart_required: boolean;
}

/* Sets the display label of an added account; "" removes it. */
export function setClaudeAccountLabel(name: string, label: string, expectedVersion?: string): Promise<ClaudeLabelResponse> {
  const body: { label: string; expected_version?: string } = { label };
  if (expectedVersion) {
    body.expected_version = expectedVersion;
  }
  return request<ClaudeLabelResponse>("PUT", accountPath(name, "/label"), body);
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
/* Antigravity (spec 74 API-AGY-020..025)                             */
/* ------------------------------------------------------------------ */

/* The built-in Antigravity account and address prefix (API-AGY-003). */
export const ANTIGRAVITY_PROVIDER = "antigravity";

/* The oldest agy the router runs (REQ-AGY-002). */
export const ANTIGRAVITY_MIN_VERSION = "1.5.17";

/* A sign-in of one account (API-AGY-022): starting = agy started, no link
   yet; waiting_code = the Google link is out, lana waits for the code;
   running = the code went to agy, which has not exited yet. url is set at
   waiting_code only; error at failed only, without URLs. */
export type AntigravitySignInState =
  | "idle"
  | "starting"
  | "waiting_code"
  | "running"
  | "succeeded"
  | "failed"
  | "cancelled";

export interface AntigravitySignIn {
  state: AntigravitySignInState;
  url: string;
  error: string;
  started_at: string | null;
  finished_at: string | null;
}

/* One quota window of an Antigravity model group (REQ-AGY-009). remaining
   is a fraction: 0.25 is a quarter left. */
export interface AntigravityWindow {
  group: string;
  type: "5h" | "weekly" | string;
  remaining: number | null;
  resets_at: string;
  source?: string;
}

export interface AntigravityUsage {
  calls: number;
  input_tokens: number;
  output_tokens: number;
}

/* One Antigravity account (API-AGY-020, spec 75 additions). */
export interface AntigravityAccount {
  name: string;
  builtin: boolean;
  label: string;
  display_name: string;
  /* The Google account's email; "" when agy has not reported it (spec 77
     revision 4). */
  email: string;
  home: string;
  binary: string;
  disabled: boolean;
  allow_scheduled: boolean;
  signed_in: boolean;
  error: string;
  tier: string;
  models_count: number;
  running: number;
  queued: number;
  windows: AntigravityWindow[];
  windows_updated_at: string | null;
  windows_error: string;
  usage_24h: AntigravityUsage;
  agents: string[];
  combos: string[];
  sign_in: AntigravitySignIn;
  priority: number;
  pool: boolean;
  cooling_until: string | null;
}

export interface AntigravityStatus {
  installed: boolean;
  binary: string;
  version: string;
  version_ok: boolean;
  min_version: string;
  /* In pool order (spec 75). */
  accounts: AntigravityAccount[];
  pool_warning: boolean;
}

const AGY_SIGN_IN_STATES: AntigravitySignInState[] = [
  "idle",
  "starting",
  "waiting_code",
  "running",
  "succeeded",
  "failed",
  "cancelled",
];

export function normalizeAntigravitySignIn(
  value: Partial<AntigravitySignIn> | null | undefined
): AntigravitySignIn {
  const entry = value || {};
  const state = AGY_SIGN_IN_STATES.includes(entry.state as AntigravitySignInState)
    ? (entry.state as AntigravitySignInState)
    : "idle";
  return {
    state,
    url: state === "waiting_code" ? String(entry.url || "") : "",
    error: String(entry.error || ""),
    started_at: entry.started_at ? String(entry.started_at) : null,
    finished_at: entry.finished_at ? String(entry.finished_at) : null,
  };
}

function normalizeAgyWindow(value: Partial<AntigravityWindow> | null | undefined): AntigravityWindow {
  const entry = value || {};
  const remaining = entry.remaining === null || entry.remaining === undefined ? null : Number(entry.remaining);
  return {
    group: String(entry.group || ""),
    type: String(entry.type || ""),
    remaining: remaining === null || !Number.isFinite(remaining) ? null : remaining,
    resets_at: String(entry.resets_at || ""),
    source: entry.source ? String(entry.source) : undefined,
  };
}

function normalizeAgyAccount(value: Partial<AntigravityAccount> | null | undefined): AntigravityAccount {
  const entry = value || {};
  const name = String(entry.name || ANTIGRAVITY_PROVIDER);
  const label = String(entry.label || "");
  const email = String(entry.email || "");
  const usage = (entry.usage_24h || {}) as Partial<AntigravityUsage>;
  return {
    name,
    builtin: entry.builtin === true,
    label,
    display_name: String(entry.display_name || "") || label || email || name,
    email,
    home: String(entry.home || ""),
    binary: String(entry.binary || ""),
    disabled: entry.disabled === true,
    allow_scheduled: entry.allow_scheduled === true,
    signed_in: entry.signed_in === true,
    error: String(entry.error || ""),
    tier: String(entry.tier || ""),
    models_count: Number(entry.models_count) || 0,
    running: Number(entry.running) || 0,
    queued: Number(entry.queued) || 0,
    windows: Array.isArray(entry.windows) ? entry.windows.map((window) => normalizeAgyWindow(window)) : [],
    windows_updated_at: entry.windows_updated_at ? String(entry.windows_updated_at) : null,
    windows_error: String(entry.windows_error || ""),
    usage_24h: {
      calls: Number(usage.calls) || 0,
      input_tokens: Number(usage.input_tokens) || 0,
      output_tokens: Number(usage.output_tokens) || 0,
    },
    agents: strings(entry.agents),
    combos: strings(entry.combos),
    sign_in: normalizeAntigravitySignIn(entry.sign_in),
    priority: Number(entry.priority) || 0,
    pool: entry.pool !== false,
    cooling_until: entry.cooling_until ? String(entry.cooling_until) : null,
  };
}

export async function getAntigravity(): Promise<AntigravityStatus> {
  const data = await request<Partial<AntigravityStatus> | null>("GET", "/v1/router/antigravity");
  const value = data || {};
  return {
    installed: value.installed === true,
    binary: String(value.binary || ""),
    version: String(value.version || ""),
    version_ok: value.version_ok === true,
    min_version: String(value.min_version || "") || ANTIGRAVITY_MIN_VERSION,
    accounts: Array.isArray(value.accounts) ? value.accounts.map((entry) => normalizeAgyAccount(entry)) : [],
    pool_warning: value.pool_warning === true,
  };
}

export interface AntigravityAccountCreated {
  name: string;
  home: string;
  version: string;
  restart_required: boolean;
  sign_in: AntigravitySignIn;
}

const AGY_ACCOUNTS_PATH = "/v1/router/antigravity/accounts";

function agyAccountPath(name: string, rest: string): string {
  return AGY_ACCOUNTS_PATH + "/" + encodeURIComponent(name) + rest;
}

/* Adds an account (name default agy-2, agy-3, …) and starts its sign-in;
   the daemon answers 202. */
export async function createAntigravityAccount(
  options: { name?: string; label?: string } = {}
): Promise<AntigravityAccountCreated> {
  const body: { name?: string; label?: string } = {};
  if (options.name) {
    body.name = options.name;
  }
  if (options.label) {
    body.label = options.label;
  }
  const data = await request<Partial<AntigravityAccountCreated> | null>("POST", AGY_ACCOUNTS_PATH, body);
  const value = data || {};
  return {
    name: String(value.name || ""),
    home: String(value.home || ""),
    version: String(value.version || ""),
    restart_required: value.restart_required === true,
    sign_in: normalizeAntigravitySignIn(value.sign_in),
  };
}

/* Starts a sign-in; 409 sign_in_running while one runs, 409 builtin for
   the built-in account. */
export async function startAntigravitySignIn(name: string): Promise<AntigravitySignIn> {
  return normalizeAntigravitySignIn(
    await request<Partial<AntigravitySignIn> | null>("POST", agyAccountPath(name, "/sign-in"), {})
  );
}

export async function getAntigravitySignIn(name: string): Promise<AntigravitySignIn> {
  return normalizeAntigravitySignIn(
    await request<Partial<AntigravitySignIn> | null>("GET", agyAccountPath(name, "/sign-in"))
  );
}

export async function cancelAntigravitySignIn(name: string): Promise<AntigravitySignIn> {
  return normalizeAntigravitySignIn(
    await request<Partial<AntigravitySignIn> | null>("DELETE", agyAccountPath(name, "/sign-in"))
  );
}

/* API-AGY-023: the code Google shows after the owner allows access. It goes
   to agy once; neither the daemon nor the page keeps it. 409 not_waiting,
   422 bad_code. */
export async function sendAntigravitySignInCode(name: string, code: string): Promise<AntigravitySignIn> {
  return normalizeAntigravitySignIn(
    await request<Partial<AntigravitySignIn> | null>("POST", agyAccountPath(name, "/sign-in/code"), {
      code: code.trim(),
    })
  );
}

/* Sets the display label of an added account; "" removes it. */
export function setAntigravityAccountLabel(
  name: string,
  label: string,
  expectedVersion?: string
): Promise<ClaudeLabelResponse> {
  const body: { label: string; expected_version?: string } = { label };
  if (expectedVersion) {
    body.expected_version = expectedVersion;
  }
  return request<ClaudeLabelResponse>("PUT", agyAccountPath(name, "/label"), body);
}

/* ------------------------------------------------------------------ */
/* Account order and key pools (spec 75)                              */
/* ------------------------------------------------------------------ */

/* "api" orders the connections of one API connection pool (spec 80). */
export type PoolRuntime = "claude-code" | "antigravity" | "api";

export interface PoolOrderResponse {
  version: string;
  restart_required: boolean;
  pool: string[];
}

/* Rewrites the pool order of a subscription runtime, or of one API
   connection pool (runtime "api"): order holds the pooled accounts top to
   bottom, unpooled the rest. Every account appears once. */
export async function savePoolOrder(
  runtime: PoolRuntime,
  order: string[],
  unpooled: string[],
  expectedVersion?: string
): Promise<PoolOrderResponse> {
  const body: { runtime: PoolRuntime; order: string[]; unpooled: string[]; expected_version?: string } = {
    runtime,
    order,
    unpooled,
  };
  if (expectedVersion) {
    body.expected_version = expectedVersion;
  }
  const data = await request<Partial<PoolOrderResponse> | null>("PUT", "/v1/router/pool-order", body);
  const value = data || {};
  return {
    version: String(value.version || ""),
    restart_required: value.restart_required === true,
    pool: strings(value.pool),
  };
}

export interface ProviderKeysResponse {
  version: string;
  keys: ProviderKey[];
}

/* Sets an API provider's keys in use order: the first becomes api_key_env,
   the rest api_key_envs. */
export async function saveProviderKeys(
  name: string,
  keys: string[],
  expectedVersion?: string
): Promise<ProviderKeysResponse> {
  const body: { keys: string[]; expected_version?: string } = { keys };
  if (expectedVersion) {
    body.expected_version = expectedVersion;
  }
  const data = await request<Partial<ProviderKeysResponse> | null>(
    "PUT",
    "/v1/providers/" + encodeURIComponent(name) + "/keys",
    body
  );
  const value = data || {};
  return {
    version: String(value.version || ""),
    keys: Array.isArray(value.keys)
      ? value.keys.map((key) => ({
          name: String((key && key.name) || ""),
          present: Boolean(key && key.present),
          cooling_until: key && key.cooling_until ? String(key.cooling_until) : null,
        }))
      : [],
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
