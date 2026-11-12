import { request } from "./client";
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
import {
  cancelDaemonRestart,
  daemonInfo,
  restartDaemon,
  waitForDaemonRestart,
} from "./daemon";
import type { DaemonInfo } from "./daemon";
import { listAudit } from "./audit";
import type { AuditEntry, AuditPage } from "./audit";
import { listSecrets, setSecret, deleteSecret } from "./secrets";
import {
  deleteProvider,
  listProviders,
  saveProvider,
  testProvider,
} from "./router";

/* The provider calls moved to api/router.ts with the Router page (spec 37,
   API-RTR-006). They stay importable from here for existing callers. */
export type {
  ListProvidersResponse,
  ProviderRow,
  ProviderSaveResponse,
  ProviderTestRequest,
  ProviderTestResponse,
  ProviderWriteRequest,
} from "./router";
export { deleteProvider, listProviders, saveProvider, testProvider };

/* Settings calls of API-WEB-013, API-WEB-014, API-WEB-020, API-WEB-022,
   API-WEB-023, API-WEB-024, API-WEB-029, and API-WEB-030. Paths and field names
   follow docs/specs/9-web-settings-chat/api-contract.md. The restart polling
   constants live in api/daemon.ts, which owns the restart flow. */

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
  /* Spec 38 (API-CCR-001): "claude-code" runs the agent through Claude Code;
     empty is lana's own loop. */
  runtime?: string;
  auth?: string;
  api_key_env?: string;
  binary?: string;
  allow_scheduled?: boolean;
  reasoning_effort?: string;
  description?: string;
  provider?: string;
  model?: string;
  display_name?: string;
  title?: string;
  color?: string;
  fallback?: string[];
  prompt_file?: string;
  workspace?: string;
  tools?: string[];
  subagents?: string[];
  mcp?: string[];
  skills?: string[];
  permission?: string;
  max_steps?: number;
  max_tokens?: number;
  context_window?: number;
  memory_capture?: boolean;
  workspace_only?: boolean;
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

export type { BrowserSessionInfo, ListBrowserSessionsResponse };
export type { RevokeOtherBrowsersResponse };
export type { AuditEntry, AuditPage };

export function getDaemon(): Promise<DaemonInfo> {
  return daemonInfo();
}

/* The restart flow lives in api/daemon.ts so the settings dialog and the
   Telegram connect step share one implementation (REQ-TFB-003). */
export { cancelDaemonRestart, restartDaemon, waitForDaemonRestart };

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

export { listSecrets, setSecret, deleteSecret };

export { listAudit };
