import { useSyncExternalStore } from "react";
import { ApiError, errorMessage } from "../api/client";
import { daemonInfo, waitForDaemonRestart } from "../api/daemon";
import {
  applyUpdate,
  cancelUpdate,
  checkUpdate,
  dismissUpdateResult,
  getUpdate,
  isJobActive,
  saveUpdateSettings,
  skipUpdate,
} from "../api/update";
import type { UpdateSettingsPatch, UpdateStatus } from "../api/update";

/* One self-update state for the floating card and Settings > General
   (spec 36, REQ-UPD-002/003/007). The store polls GET /v1/update while at
   least one view subscribes: every 5 minutes when idle and every second while
   a job runs. When the job reaches "restarting", or the daemon goes away
   while a job runs, it waits for the new daemon with the restart wait of
   api/daemon.ts and then reads the status again. */

export const IDLE_POLL_MS = 5 * 60_000;
export const ACTIVE_POLL_MS = 1000;
/* A trial boot that fails rolls back and restarts again, so an update restart
   gets more time than a plain restart. */
export const UPDATE_RESTART_TIMEOUT_MS = 120_000;
export const LATER_KEY = "lana.update.later";

export type UpdateBusy =
  | ""
  | "apply"
  | "force"
  | "cancel"
  | "skip"
  | "dismiss"
  | "check"
  | "settings";

export interface UpdateViewState {
  status: UpdateStatus | null;
  /* The last GET failure; the previous status stays. */
  loadError: string;
  /* Waiting for the daemon to come back after an update restart. */
  restarting: boolean;
  restartVersion: string;
  restartError: string;
  busy: UpdateBusy;
  actionError: string;
  /* The version the owner put off with Later in this browser session. */
  laterVersion: string;
}

function readLater(): string {
  try {
    return window.sessionStorage.getItem(LATER_KEY) || "";
  } catch {
    return "";
  }
}

function writeLater(version: string): void {
  try {
    if (version) {
      window.sessionStorage.setItem(LATER_KEY, version);
    } else {
      window.sessionStorage.removeItem(LATER_KEY);
    }
  } catch {
    /* Storage can be blocked; Later then lasts until the page reloads. */
  }
}

function initialState(): UpdateViewState {
  return {
    status: null,
    loadError: "",
    restarting: false,
    restartVersion: "",
    restartError: "",
    busy: "",
    actionError: "",
    laterVersion: readLater(),
  };
}

let state: UpdateViewState = initialState();
const listeners = new Set<() => void>();
let timer: ReturnType<typeof setTimeout> | null = null;
let inflight: Promise<void> | null = null;
let startedAt = 0;
/* Bumped by resetUpdateStore so a stale async step from before a reset does
   not write into the fresh state (tests). */
let generation = 0;

function set(patch: Partial<UpdateViewState>): void {
  state = { ...state, ...patch };
  for (const listener of listeners) {
    listener();
  }
}

function getSnapshot(): UpdateViewState {
  return state;
}

function clearTimer(): void {
  if (timer !== null) {
    clearTimeout(timer);
    timer = null;
  }
}

function schedule(): void {
  clearTimer();
  if (listeners.size === 0 || state.restarting) {
    return;
  }
  const delay = isJobActive(state.status && state.status.job)
    ? ACTIVE_POLL_MS
    : IDLE_POLL_MS;
  timer = setTimeout(() => {
    timer = null;
    void refreshUpdate();
  }, delay);
}

/* Remember the daemon's started_at while a job runs, so the restart wait can
   tell the new daemon from the old one. */
async function captureStartedAt(): Promise<void> {
  try {
    const info = await daemonInfo();
    startedAt = Number(info && info.started_at) || 0;
  } catch {
    /* The daemon may already be restarting; the wait then waits for it to
       go down and come back. */
  }
}

function applyStatus(status: UpdateStatus): void {
  set({ status, loadError: "" });
  const job = status.job;
  if (isJobActive(job) && startedAt === 0) {
    void captureStartedAt();
  }
  if (job && job.state === "restarting") {
    void waitForRestart(job.version);
  }
}

async function waitForRestart(version: string): Promise<void> {
  if (state.restarting) {
    return;
  }
  const mine = generation;
  clearTimer();
  set({
    restarting: true,
    restartVersion: version,
    restartError: "",
    actionError: "",
  });
  try {
    await waitForDaemonRestart(startedAt, undefined, UPDATE_RESTART_TIMEOUT_MS);
    if (mine !== generation) {
      return;
    }
    startedAt = 0;
    set({ restarting: false });
    await refreshUpdate();
  } catch (failure) {
    if (mine !== generation) {
      return;
    }
    set({ restarting: false, restartError: errorMessage(failure) });
    schedule();
  }
}

/* GET /v1/update, then schedule the next poll. A failure while a job runs
   means the daemon went down to restart. */
export function refreshUpdate(): Promise<void> {
  if (inflight) {
    return inflight;
  }
  const mine = generation;
  inflight = (async () => {
    try {
      const status = await getUpdate();
      if (mine !== generation) {
        return;
      }
      if (state.restartError) {
        set({ restartError: "" });
      }
      applyStatus(status);
    } catch (failure) {
      if (mine !== generation) {
        return;
      }
      const job = state.status && state.status.job;
      const unauthorized = failure instanceof ApiError && failure.status === 401;
      if (!unauthorized && isJobActive(job) && job && job.state !== "downloading") {
        void waitForRestart(job.version);
        return;
      }
      set({ loadError: errorMessage(failure) });
    } finally {
      if (mine === generation) {
        inflight = null;
        schedule();
      }
    }
  })();
  return inflight;
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) {
    void refreshUpdate();
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      clearTimer();
    }
  };
}

export function useUpdateState(): UpdateViewState {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

async function run(
  busy: UpdateBusy,
  action: () => Promise<UpdateStatus | null>
): Promise<UpdateStatus | null> {
  if (state.busy) {
    return null;
  }
  set({ busy, actionError: "" });
  try {
    const status = await action();
    set({ busy: "" });
    if (status) {
      applyStatus(status);
    }
    schedule();
    return status;
  } catch (failure) {
    set({ busy: "", actionError: errorMessage(failure) });
    return null;
  }
}

/* Update now / Try again (API-UPD-005). The daemon's started_at is read
   first, so the restart wait recognises the new daemon. */
export function startUpdate(): Promise<UpdateStatus | null> {
  const available = state.status && state.status.available;
  if (!available) {
    return Promise.resolve(null);
  }
  return run("apply", async () => {
    await captureStartedAt();
    const status = await applyUpdate(available.version, false);
    writeLater("");
    set({ laterVersion: "" });
    return status;
  });
}

/* Interrupt and update: the same job, with running turns interrupted. */
export function forceUpdate(): Promise<UpdateStatus | null> {
  const status = state.status;
  const version =
    (status && status.job && status.job.version) ||
    (status && status.available && status.available.version) ||
    "";
  if (!version) {
    return Promise.resolve(null);
  }
  return run("force", () => applyUpdate(version, true));
}

export function cancelUpdateJob(): Promise<UpdateStatus | null> {
  return run("cancel", async () => {
    await cancelUpdate();
    return getUpdate();
  });
}

export function skipAvailable(): Promise<UpdateStatus | null> {
  const available = state.status && state.status.available;
  if (!available) {
    return Promise.resolve(null);
  }
  return run("skip", () => skipUpdate(available.version));
}

export function dismissResult(): Promise<UpdateStatus | null> {
  return run("dismiss", async () => {
    await dismissUpdateResult();
    const current = state.status;
    return current ? { ...current, last_result: null } : null;
  });
}

export function checkNow(): Promise<UpdateStatus | null> {
  return run("check", () => checkUpdate());
}

export function saveSettings(patch: UpdateSettingsPatch): Promise<UpdateStatus | null> {
  return run("settings", () => saveUpdateSettings(patch));
}

/* Later hides the card for this version until the next browser session. */
export function later(): void {
  const status = state.status;
  const version =
    (status && status.available && status.available.version) ||
    (status && status.job && status.job.version) ||
    "";
  writeLater(version);
  set({ laterVersion: version, actionError: "" });
}

/* Test and preview helpers. */
export function resetUpdateStore(): void {
  generation += 1;
  clearTimer();
  inflight = null;
  startedAt = 0;
  listeners.clear();
  state = initialState();
}

export function setUpdateStateForTest(patch: Partial<UpdateViewState>): void {
  set(patch);
}
