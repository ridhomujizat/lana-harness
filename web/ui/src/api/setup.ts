import { ApiError, errorMessage, request } from "./client";
import type { AuthSession } from "./auth";

/* Setup wizard API calls (T-025). The wizard views import only these helpers
   so each request shape stays next to the contract operation it mirrors. */

const POLL_INTERVAL_MS = 1000;
const POLL_TIMEOUT_MS = 30000;

/* One model of the wizard's picker catalog. */
export interface SetupModel {
  id: string;
  name: string;
}

export interface SetupStatus {
  needed: boolean;
  reasons: string[];
  provider_count: number;
  agent_count: number;
  /* Revision 3: the built-in models of each preset kind and the Claude Code
     aliases, so the model picker works before anything is saved. An older
     daemon sends neither. */
  models?: Record<string, SetupModel[]>;
  claude_code_models?: SetupModel[];
}

export interface SetupTestProviderInput {
  kind: string;
  /* Empty for a preset kind's own URL. */
  baseUrl?: string;
  /* Empty: the daemon picks a model it knows for the kind or host. */
  model?: string;
  apiKey: string;
}

export interface SetupTestProviderResponse {
  ok: boolean;
  latency_ms?: number;
  status?: number;
  message?: string;
}

/* One connected provider of the setup save (API-WEB-021 revision 3). */
export interface SetupProviderInput {
  name: string;
  kind: string;
  apiKey: string;
  /* Empty for a preset kind's own URL. */
  baseUrl: string;
  apiKeyEnv: string;
}

export interface SetupInput {
  providers: SetupProviderInput[];
  /* A provider/model address; with a fallback the daemon writes the
     default combo [model, fallback] and the agent uses "default". */
  model: string;
  fallback: string;
  /* [router] background_model; empty leaves it unset. */
  backgroundModel: string;
  agent: {
    name: string;
    display_name?: string;
    template: string;
    workspace: string;
    permission: string;
  };
  persona: {
    agentMd: string;
    userMd: string;
  };
}

export interface SetupSaveResponse {
  restart: string;
  pid: number;
}

export interface SetupPollOptions {
  startedAt?: number;
  intervalMs?: number;
  timeoutMs?: number;
  signal?: AbortSignal;
}

export interface GetConfigVersionResponse {
  version: string;
}

export interface DaemonStartedAtResponse {
  started_at?: number;
}

/* API-WEB-018 returns the setup status read from config.toml at call time. */
export function getSetup(): Promise<SetupStatus> {
  return request<SetupStatus>("GET", "/v1/setup");
}

/* API-WEB-019 tests one provider with an inline key. The key is used for this
   call only; the daemon never writes, logs, or returns it. */
export function testProvider(
  input: SetupTestProviderInput
): Promise<SetupTestProviderResponse> {
  const body: Record<string, string> = { kind: input.kind, api_key: input.apiKey };
  if (input.baseUrl) {
    body.base_url = input.baseUrl;
  }
  if (input.model) {
    body.model = input.model;
  }
  return request<SetupTestProviderResponse>("POST", "/v1/providers/test", body);
}

/* API-WEB-021 requires expected_version, but API-WEB-018 does not return it.
   GET /v1/config returns the sha256 of config.toml ("" when it is missing), so
   the save reads it here instead of keeping a stale copy in the wizard. */
export async function saveSetup(input: SetupInput): Promise<SetupSaveResponse> {
  const config = await request<GetConfigVersionResponse>("GET", "/v1/config");
  const expectedVersion =
    config && typeof config.version === "string" ? config.version : "";
  /* display_name is sent only when the human name differs from the ID, so a
     wizard that kept them equal writes no extra config key. */
  const agent: SetupInput["agent"] = {
    name: input.agent.name,
    template: input.agent.template,
    workspace: input.agent.workspace,
    permission: input.agent.permission,
  };
  const displayName = (input.agent.display_name || "").trim();
  if (displayName && displayName !== input.agent.name.trim()) {
    agent.display_name = displayName;
  }
  const body: Record<string, unknown> = {
    providers: input.providers.map((provider) => {
      const entry: Record<string, string> = {
        name: provider.name,
        kind: provider.kind,
        api_key: provider.apiKey,
        api_key_env: provider.apiKeyEnv,
      };
      if (provider.baseUrl) {
        entry.base_url = provider.baseUrl;
      }
      return entry;
    }),
    model: input.model,
    agent: {
      name: agent.name,
      ...(agent.display_name ? { display_name: agent.display_name } : {}),
      template: agent.template,
      workspace: agent.workspace,
      permission: agent.permission,
    },
    persona: {
      agent_md: input.persona.agentMd,
      user_md: input.persona.userMd,
    },
    expected_version: expectedVersion,
  };
  if (input.fallback) {
    body.fallback = input.fallback;
  }
  if (input.backgroundModel) {
    body.background_model = input.backgroundModel;
  }
  return request<SetupSaveResponse>("POST", "/v1/setup", body);
}

/* A restart in place drops the listener for a moment, so a failed fetch and a
   gateway status are expected. A 401 is not transient: the browser session is
   gone and the caller has to show the unlock page again. */
function isTransientRestartError(error: unknown): boolean {
  if (!(error instanceof ApiError)) {
    return false;
  }
  return (
    error.status === 0 ||
    error.status === 502 ||
    error.status === 503 ||
    error.status === 504
  );
}

function delay(milliseconds: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    let timer: ReturnType<typeof setTimeout> | null = null;

    function cleanup() {
      if (signal) {
        signal.removeEventListener("abort", onAbort);
      }
    }

    function onAbort() {
      cleanup();
      reject(new ApiError(0, "poll_aborted", "The restart wait was cancelled."));
    }

    timer = setTimeout(() => {
      cleanup();
      resolve();
    }, milliseconds);

    if (signal) {
      if (signal.aborted) {
        clearTimeout(timer);
        onAbort();
        return;
      }
      signal.addEventListener("abort", onAbort);
    }
  });
}

/* getDaemonStartedAt reads the restart baseline before POST /v1/setup. A
   response without started_at is not a usable baseline. */
export async function getDaemonStartedAt(): Promise<number> {
  const info = await request<DaemonStartedAtResponse>("GET", "/v1/daemon");
  const startedAt = info ? info.started_at : undefined;
  if (startedAt === undefined || startedAt === null) {
    throw new ApiError(
      0,
      "invalid_response",
      "The daemon did not report when it started."
    );
  }
  return startedAt;
}

/* Task T-025 and design.md poll GET /v1/daemon until started_at changes. The
   restart drops the listener for a moment, so network errors are ignored until
   the timeout. Once the daemon answers with a new started_at, GET
   /v1/auth/session refreshes the CSRF value before the caller moves on. */
export async function pollDaemonRestart(
  options?: SetupPollOptions
): Promise<AuthSession> {
  const settings = options || {};
  const intervalMs =
    Number(settings.intervalMs) > 0
      ? Number(settings.intervalMs)
      : POLL_INTERVAL_MS;
  const timeoutMs =
    Number(settings.timeoutMs) > 0
      ? Number(settings.timeoutMs)
      : POLL_TIMEOUT_MS;
  const signal = settings.signal;
  const baseline = settings.startedAt;
  const deadline = Date.now() + timeoutMs;

  while (Date.now() < deadline) {
    if (signal && signal.aborted) {
      throw new ApiError(0, "poll_aborted", "The restart wait was cancelled.");
    }
    try {
      const info = await request<DaemonStartedAtResponse>(
        "GET",
        "/v1/daemon"
      );
      const current = info ? info.started_at : undefined;
      if (current !== undefined && current !== null && current !== baseline) {
        return await request<AuthSession>("GET", "/v1/auth/session");
      }
    } catch (error) {
      if (signal && signal.aborted) {
        throw new ApiError(0, "poll_aborted", "The restart wait was cancelled.");
      }
      if (!isTransientRestartError(error)) {
        throw error;
      }
    }
    const remaining = deadline - Date.now();
    if (remaining <= 0) {
      break;
    }
    await delay(Math.min(intervalMs, remaining), signal);
  }

  const seconds = Math.round(timeoutMs / 1000);
  const timeout = new ApiError(
    0,
    "restart_timeout",
    "The daemon did not answer within " + seconds + " seconds. It may still be restarting."
  );
  /* Keep the timeout text in the same shape as the errors the views show. */
  throw new ApiError(timeout.status, timeout.code, errorMessage(timeout));
}
