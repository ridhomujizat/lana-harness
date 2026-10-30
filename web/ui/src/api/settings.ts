import { ApiError, errorMessage, request } from "./client";
import {
  getAuthSession,
  listBrowserSessions,
  revokeOtherBrowsers,
} from "./auth";
import type {
  BrowserSessionInfo,
  ListBrowserSessionsResponse,
  RevokeOtherBrowsersResponse,
} from "./auth";
import { daemonInfo } from "./daemon";
import type { DaemonInfo } from "./daemon";
import { listAudit } from "./audit";
import type { AuditEntry, AuditPage } from "./audit";
import { listSecrets, setSecret, deleteSecret } from "./secrets";

/* Settings calls of API-WEB-013, API-WEB-014, API-WEB-020, API-WEB-022,
   API-WEB-023, API-WEB-024, API-WEB-029, and API-WEB-030. Paths and field names
   follow docs/specs/9-web-settings-chat/api-contract.md. */

const RESTART_POLL_MS = 500;
const RESTART_TIMEOUT_MS = 30000;

export interface ConfigProvider {
  kind: string;
  base_url?: string;
  api_key_env?: string;
  headers?: Record<string, string>;
  timeout?: string;
}

export interface ConfigRules {
  allow?: string[];
  deny?: string[];
}

export interface ConfigAgent {
  provider?: string;
  model?: string;
  fallback?: string[];
  prompt_file?: string;
  workspace?: string;
  tools?: string[];
  subagents?: string[];
  mcp?: string[];
  skills?: string[];
  permission?: string;
  max_steps?: number;
  context_window?: number;
  rules?: ConfigRules;
}

export interface ConfigRoute {
  channel?: string;
  agent?: string;
  match?: Record<string, unknown>;
}

export interface ConfigMCPServer {
  command?: string;
  args?: string[];
  url?: string;
  env?: Record<string, string>;
  headers?: Record<string, string>;
}

export interface ConfigModule {
  id?: string;
  module?: string;
  disabled?: boolean;
  config?: Record<string, unknown>;
}

export interface ConfigSettings {
  providers: Record<string, ConfigProvider>;
  agents: Record<string, ConfigAgent>;
  routes: ConfigRoute[];
  channels: Record<string, Record<string, unknown>>;
  mcp: Record<string, ConfigMCPServer>;
  modules: ConfigModule[];
}

export interface ConfigIssue {
  file: string;
  key: string;
  message: string;
}

export interface ConfigValidation {
  ok: boolean;
  errors: ConfigIssue[];
}

export interface GetConfigResponse {
  version: string;
  loaded_version: string;
  restart_required: boolean;
  validation: ConfigValidation;
  raw: string;
  settings: ConfigSettings;
}

export interface PutConfigResponse {
  version: string;
  restart_required: boolean;
}

export interface ProviderRow {
  name: string;
  kind: string;
  base_url: string;
  api_key_env: string;
  key_state: string;
  used_by: string[];
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
  kind: string;
  base_url?: string;
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

export interface SaveWebRequest {
  addr: string;
  expected_version: string;
}

export interface SaveWebResponse {
  addr: string;
  url: string;
  version: string;
  restart_required: boolean;
}

export interface RestartDaemonResponse {
  restart: string;
  running_turns: number;
}

export type { BrowserSessionInfo, ListBrowserSessionsResponse };
export type { RevokeOtherBrowsersResponse };
export type { AuditEntry, AuditPage };

export function getDaemon(): Promise<DaemonInfo> {
  return daemonInfo();
}

export function restartDaemon(): Promise<RestartDaemonResponse> {
  return request<RestartDaemonResponse>("POST", "/v1/daemon/restart", {
    confirm: true,
  });
}

export { getAuthSession, listBrowserSessions, revokeOtherBrowsers };export function saveWebAddress(
  addr: string,
  expectedVersion: string
): Promise<SaveWebResponse> {
  return request<SaveWebResponse>("PUT", "/v1/web", {
    addr,
    expected_version: expectedVersion,
  });
}

export function getConfig(): Promise<GetConfigResponse> {
  return request<GetConfigResponse>("GET", "/v1/config");
}

export function saveConfig(
  raw: string,
  expectedVersion: string
): Promise<PutConfigResponse> {
  return request<PutConfigResponse>("PUT", "/v1/config", {
    raw,
    expected_version: expectedVersion,
  });
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

export { listSecrets, setSecret, deleteSecret };

export { listAudit };

function startedAt(value: unknown): number {
  const number = Number(value);
  return Number.isFinite(number) ? number : 0;
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

/* After a confirmed restart, API-WEB-023 says the page waits for the daemon.
   Poll the owner session first, then confirm the new started_at with
   GET /v1/daemon. The timeout keeps a failed restart from polling forever. */
export async function waitForDaemonRestart(
  previousStartedAt: number,
  onProgress?: (message: string) => void
): Promise<DaemonInfo> {
  const deadline = Date.now() + RESTART_TIMEOUT_MS;
  const previous = startedAt(previousStartedAt);
  let sawDown = false;
  let lastFailure: unknown = null;

  if (onProgress) {
    onProgress("Restart scheduled. Waiting for the daemon to come back.");
  }

  while (Date.now() < deadline) {
    await delay(RESTART_POLL_MS);

    let sessionOK = true;
    try {
      await getAuthSession();
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 401) {
        throw failure;
      }
      sessionOK = false;
      sawDown = true;
      lastFailure = failure;
      if (onProgress) {
        onProgress("The daemon is restarting. Waiting for it to come back.");
      }
    }

    if (!sessionOK) {
      continue;
    }

    try {
      const info = await getDaemon();
      const current = startedAt(info && info.started_at);
      const changed = previous > 0 ? current !== previous : true;
      if (sawDown && changed) {
        return info;
      }
      if (!sawDown && previous > 0 && current > 0 && changed) {
        return info;
      }
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 401) {
        throw failure;
      }
      sawDown = true;
      lastFailure = failure;
    }
  }

  const suffix = lastFailure ? " " + errorMessage(lastFailure) : "";
  throw new ApiError(
    0,
    "timeout",
    "The daemon did not come back within 30 seconds." + suffix
  );
}
