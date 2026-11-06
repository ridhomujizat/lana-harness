import { queryString, request } from "./client";

/* Monitoring dashboard calls of spec 28 (API-DSH-001). The types are written
   by hand from the Go JSON struct; times are Unix milliseconds and token
   fields are integers. */

export type DashboardRange = "24h" | "7d" | "30d";

export interface DashboardNow {
  sessions_running: number;
  sessions_waiting_input: number;
}

export interface DashboardSummary {
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
  turns: number;
  turns_failed: number;
  turns_cancelled: number;
  tool_calls: number;
  tool_errors: number;
  tool_rejected: number;
  provider_retries: number;
}

export interface DashboardSeriesPoint {
  start: number;
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
}

export interface DashboardModelRow {
  provider: string;
  model: string;
  requests: number;
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
}

export interface DashboardAgentRow {
  agent: string;
  turns: number;
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
}

export interface DashboardSourceRow {
  source: string;
  count: number;
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
}

export interface DashboardToolRow {
  name: string;
  calls: number;
  errors: number;
  rejected: number;
  avg_ms: number;
  p95_ms: number;
  last_error: string;
  last_error_at: number;
}

export interface DashboardTurnReasonRow {
  reason: string;
  count: number;
}

export interface DashboardTurnDuration {
  avg_ms: number;
  p95_ms: number;
}

export interface DashboardErrorRow {
  at: number;
  source: string;
  level: string;
  agent: string;
  session_id: string;
  turn_id: string;
  tool: string;
  message: string;
}

export interface Dashboard {
  range: string;
  agent: string;
  agents: string[];
  from: number;
  to: number;
  bucket_ms: number;
  daemon_started_at: number;
  now: DashboardNow;
  summary: DashboardSummary;
  series: DashboardSeriesPoint[];
  by_model: DashboardModelRow[];
  by_agent: DashboardAgentRow[];
  by_source: DashboardSourceRow[];
  tools: DashboardToolRow[];
  turn_reasons: DashboardTurnReasonRow[];
  turn_duration: DashboardTurnDuration;
  errors: DashboardErrorRow[];
}

/* REQ-DSH-011: an empty agent means all agents and stays out of the query. */
export function getDashboard(
  range: DashboardRange,
  agent?: string
): Promise<Dashboard> {
  return request<Dashboard>(
    "GET",
    "/v1/dashboard" + queryString({ range, agent })
  );
}
