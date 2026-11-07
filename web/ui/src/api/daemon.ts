import { ApiError, errorMessage, request } from "./client";
import { getAuthSession } from "./auth";

/* Daemon calls of API-WEB-022, API-WEB-023, and API-TFB-002. The restart flow
   lives here so the settings dialog and the Telegram connect step share one
   implementation. */

export interface DaemonTurn {
  session_id: string;
  title: string;
  agent: string;
}

/* API-TFB-002: GET /v1/daemon reports the pending restart, if any. */
export interface DaemonRestartInfo {
  state: "idle" | "draining";
  deadline: number | null;
}

export interface DaemonInfo {
  pid: number;
  started_at: number;
  uptime_ms: number;
  addr: string;
  home: string;
  version: string;
  go_version: string;
  os: string;
  arch: string;
  restart_supported: boolean;
  restart: DaemonRestartInfo;
  running_turns: DaemonTurn[];
  browsers_signed_in: number;
}

export interface RestartDaemonResponse {
  restart: string;
  running_turns: number;
  /* API-TFB-002: set only while restart is "draining". */
  deadline?: number;
}

export interface CancelRestartResponse {
  restart: string;
}

export function daemonInfo(): Promise<DaemonInfo> {
  return request<DaemonInfo>("GET", "/v1/daemon");
}

/* POST /v1/daemon/restart. force interrupts running turns now instead of
   draining them (API-TFB-002). */
export function restartDaemon(force = false): Promise<RestartDaemonResponse> {
  return request<RestartDaemonResponse>(
    "POST",
    "/v1/daemon/restart",
    force ? { confirm: true, force: true } : { confirm: true }
  );
}

/* DELETE /v1/daemon/restart aborts a pending drain (API-TFB-002). */
export function cancelDaemonRestart(): Promise<CancelRestartResponse> {
  return request<CancelRestartResponse>("DELETE", "/v1/daemon/restart");
}

const RESTART_POLL_MS = 500;
const RESTART_TIMEOUT_MS = 30000;
const RESTART_WAIT_TEXT =
  "Restart scheduled. Waiting for the daemon to come back.";

function startedAtOf(value: unknown): number {
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
  onProgress?: (message: string) => void,
  timeoutMs: number = RESTART_TIMEOUT_MS
): Promise<DaemonInfo> {
  /* A self-update restart may run a trial boot and a rollback first, so its
     caller passes a longer timeout (spec 36). */
  const deadline = Date.now() + timeoutMs;
  const previous = startedAtOf(previousStartedAt);
  let sawDown = false;
  let lastFailure: unknown = null;

  if (onProgress) {
    onProgress(RESTART_WAIT_TEXT);
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
      const info = await daemonInfo();
      const current = startedAtOf(info && info.started_at);
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
    "The daemon did not come back within " +
      Math.round(timeoutMs / 1000) +
      " seconds." +
      suffix
  );
}
