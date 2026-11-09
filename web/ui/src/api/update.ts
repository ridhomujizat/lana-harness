import { request } from "./client";

/* Self-update calls of spec 36 (API-UPD-002..009). Every call except cancel
   and dismiss answers with the full UpdateStatus. Times are unix
   milliseconds. */

export type UpdateChannel = "stable" | "beta";

export type UpdateJobState =
  | "downloading"
  | "verifying"
  | "probing"
  | "waiting"
  | "installing"
  | "restarting"
  | "failed"
  | "cancelled";

export type UpdateOutcome = "updated" | "rolled_back" | "failed";

/* API-UPD-002: the newest eligible release on the channel. */
export interface UpdateRelease {
  version: string;
  tag: string;
  name: string;
  published_at: number;
  notes: string;
  url: string;
  size: number;
  prerelease: boolean;
  /* True when notes contain an "Upgrade notes" heading. */
  upgrade_notes: boolean;
}

export interface UpdateProgress {
  done: number;
  /* 0 when the size is unknown. */
  total: number;
}

export interface UpdateJob {
  state: UpdateJobState;
  version: string;
  /* Set only while downloading. */
  progress: UpdateProgress | null;
  /* Set only while waiting. */
  running_turns: number;
  deadline: number | null;
  /* Set only when failed. */
  error: string;
}

export interface UpdateResult {
  outcome: UpdateOutcome;
  from: string;
  to: string;
  reason: string;
  at: number;
}

export interface UpdateStatus {
  current: string;
  channel: UpdateChannel;
  check: boolean;
  /* False for a dev build or an unsupported OS; then available and job are
     null. */
  supported: boolean;
  /* False when the binary cannot be replaced; manual_reason says why. */
  can_apply: boolean;
  manual_reason: string;
  manual_command: string;
  /* Null before the first check. */
  checked_at: number | null;
  check_error: string;
  available: UpdateRelease | null;
  skipped: string;
  job: UpdateJob | null;
  last_result: UpdateResult | null;
}

export interface CancelUpdateResponse {
  cancelled: boolean;
}

export interface UpdateSettingsPatch {
  channel?: UpdateChannel;
  check?: boolean;
}

/* The job states between downloading and restarting (API-UPD-004). */
export const ACTIVE_JOB_STATES: readonly UpdateJobState[] = [
  "downloading",
  "verifying",
  "probing",
  "waiting",
  "installing",
  "restarting",
];

/* DELETE /v1/update/apply can cancel these (API-UPD-006). */
export const CANCELLABLE_JOB_STATES: readonly UpdateJobState[] = [
  "downloading",
  "verifying",
  "probing",
  "waiting",
];

export function isJobActive(job: UpdateJob | null | undefined): boolean {
  return Boolean(job && ACTIVE_JOB_STATES.includes(job.state));
}

export function isJobCancellable(job: UpdateJob | null | undefined): boolean {
  return Boolean(job && CANCELLABLE_JOB_STATES.includes(job.state));
}

/* API-UPD-003: GET /v1/update never calls GitHub. */
export function getUpdate(): Promise<UpdateStatus> {
  return request<UpdateStatus>("GET", "/v1/update");
}

/* API-UPD-004: POST /v1/update/check asks GitHub now. A GitHub failure is
   still 200 with check_error set. */
export function checkUpdate(): Promise<UpdateStatus> {
  return request<UpdateStatus>("POST", "/v1/update/check");
}

/* API-UPD-005: POST /v1/update/apply starts the job. force interrupts
   running turns instead of waiting for them. */
export function applyUpdate(version: string, force = false): Promise<UpdateStatus> {
  return request<UpdateStatus>("POST", "/v1/update/apply", {
    version,
    confirm: true,
    force,
  });
}

/* API-UPD-006: DELETE /v1/update/apply cancels a job up to waiting. */
export function cancelUpdate(): Promise<CancelUpdateResponse> {
  return request<CancelUpdateResponse>("DELETE", "/v1/update/apply");
}

/* API-UPD-007: POST /v1/update/skip. An empty version clears the skip. */
export function skipUpdate(version: string): Promise<UpdateStatus> {
  return request<UpdateStatus>("POST", "/v1/update/skip", { version });
}

/* API-UPD-008: PATCH /v1/update/settings writes [update] in config.toml. */
export function saveUpdateSettings(patch: UpdateSettingsPatch): Promise<UpdateStatus> {
  return request<UpdateStatus>("PATCH", "/v1/update/settings", patch);
}

/* API-UPD-009: DELETE /v1/update/result clears last_result; 204. */
export async function dismissUpdateResult(): Promise<void> {
  await request<null>("DELETE", "/v1/update/result");
}
