import { request, errorMessage, ApiError } from "/assets/api.js";

/* Setup wizard API calls (T-025). The wizard views import only these helpers
   so each request shape stays next to the contract operation it mirrors. */

const POLL_INTERVAL_MS = 1000;
const POLL_TIMEOUT_MS = 30000;

/* API-WEB-018 returns the setup status read from config.toml at call time. */
export function getSetup() {
  return request("GET", "/v1/setup");
}

/* API-WEB-019 tests one provider with an inline key. The key is used for this
   call only; the daemon never writes, logs, or returns it. */
export function testProvider(input) {
  return request("POST", "/v1/providers/test", {
    kind: input.kind,
    base_url: input.baseUrl,
    model: input.model,
    api_key: input.apiKey,
  });
}

/* API-WEB-021 requires expected_version, but API-WEB-018 does not return it.
   GET /v1/config returns the sha256 of config.toml ("" when it is missing), so
   the save reads it here instead of keeping a stale copy in the wizard. */
export async function saveSetup(input) {
  const config = await request("GET", "/v1/config");
  const expectedVersion =
    config && typeof config.version === "string" ? config.version : "";
  return request("POST", "/v1/setup", {
    provider: {
      name: input.provider.name,
      api_key: input.provider.apiKey,
      base_url: input.provider.baseUrl,
      model: input.provider.model,
    },
    agent: {
      name: input.agent.name,
      template: input.agent.template,
      workspace: input.agent.workspace,
      permission: input.agent.permission,
    },
    persona: {
      agent_md: input.persona.agentMd,
      user_md: input.persona.userMd,
    },
    expected_version: expectedVersion,
  });
}

/* A restart in place drops the listener for a moment, so a failed fetch and a
   gateway status are expected. A 401 is not transient: the browser session is
   gone and the caller has to show the unlock page again. */
function isTransientRestartError(error) {
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

function delay(milliseconds, signal) {
  return new Promise((resolve, reject) => {
    let timer = null;

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
export async function getDaemonStartedAt() {
  const info = await request("GET", "/v1/daemon");
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
export async function pollDaemonRestart(options) {
  const settings = options || {};
  const intervalMs =
    Number(settings.intervalMs) > 0
      ? Number(settings.intervalMs)
      : POLL_INTERVAL_MS;
  const timeoutMs =
    Number(settings.timeoutMs) > 0 ? Number(settings.timeoutMs) : POLL_TIMEOUT_MS;
  const signal = settings.signal;
  const baseline = settings.startedAt;
  const deadline = Date.now() + timeoutMs;

  while (Date.now() < deadline) {
    if (signal && signal.aborted) {
      throw new ApiError(0, "poll_aborted", "The restart wait was cancelled.");
    }
    try {
      const info = await request("GET", "/v1/daemon");
      const current = info ? info.started_at : undefined;
      if (current !== undefined && current !== null && current !== baseline) {
        return await request("GET", "/v1/auth/session");
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
