/* Settings API calls (T-034). Every fetch for Settings lives here so the views
   stay presentational. Paths and field names follow docs/specs/9-web-settings-chat/
   api-contract.md and the handlers under web/. */

import { request, errorMessage, ApiError } from "/assets/api.js";

const RESTART_POLL_MS = 500;
const RESTART_TIMEOUT_MS = 30000;

function query(params) {
  const search = new URLSearchParams();
  for (const key of Object.keys(params || {})) {
    const value = params[key];
    if (value === undefined || value === null || value === "") {
      continue;
    }
    search.set(key, String(value));
  }
  const text = search.toString();
  return text ? "?" + text : "";
}

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

function startedAt(value) {
  const number = Number(value);
  return Number.isFinite(number) ? number : 0;
}

export function getDaemon() {
  return request("GET", "/v1/daemon");
}

export function restartDaemon() {
  return request("POST", "/v1/daemon/restart", { confirm: true });
}

export function getAuthSession() {
  return request("GET", "/v1/auth/session");
}

export function listBrowserSessions() {
  return request("GET", "/v1/auth/sessions");
}

export function revokeOtherBrowsers() {
  return request("POST", "/v1/auth/sessions/revoke-others");
}

export function saveWebAddress(addr, expectedVersion) {
  return request("PUT", "/v1/web", {
    addr,
    expected_version: expectedVersion,
  });
}

export function getConfig() {
  return request("GET", "/v1/config");
}

export function saveConfig(raw, expectedVersion) {
  return request("PUT", "/v1/config", {
    raw,
    expected_version: expectedVersion,
  });
}

export function listProviders() {
  return request("GET", "/v1/providers");
}

export function saveProvider(name, body) {
  return request("PUT", "/v1/providers/" + encodeURIComponent(name), body);
}

export function deleteProvider(name, expectedVersion) {
  return request("DELETE", "/v1/providers/" + encodeURIComponent(name), {
    expected_version: expectedVersion,
  });
}

export function testProvider(body) {
  return request("POST", "/v1/providers/test", body);
}

export function listSecrets() {
  return request("GET", "/v1/secrets");
}

export function setSecret(name, value, expectedVersion) {
  return request("PUT", "/v1/secrets/" + encodeURIComponent(name), {
    value,
    expected_version: expectedVersion,
  });
}

export function deleteSecret(name, expectedVersion) {
  return request("DELETE", "/v1/secrets/" + encodeURIComponent(name), {
    expected_version: expectedVersion,
  });
}

export function listAudit(before, limit) {
  return request("GET", "/v1/audit" + query({ before, limit: limit || 50 }));
}

/* After a confirmed restart, API-WEB-023 says the page waits for the daemon.
   Poll the owner session first, then confirm the new started_at with
   GET /v1/daemon. The timeout keeps a failed restart from polling forever. */
export async function waitForDaemonRestart(previousStartedAt, onProgress) {
  const deadline = Date.now() + RESTART_TIMEOUT_MS;
  const previous = startedAt(previousStartedAt);
  let sawDown = false;
  let lastFailure = null;

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
