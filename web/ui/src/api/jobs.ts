import { request } from "./client";

/* Background-job calls of REQ-JOB-020. Types are written by hand from the Go
   JSON struct. */

export interface BackgroundJob {
  id: string;
  command: string;
  status: string;
  exit_code: number;
  pid: number;
  started_at: number;
  ended_at: number | null;
  tail: string;
}

export function listSessionJobs(sessionId: string): Promise<BackgroundJob[]> {
  return request<BackgroundJob[]>(
    "GET",
    "/v1/sessions/" + encodeURIComponent(sessionId) + "/jobs"
  );
}

export function stopSessionJob(
  sessionId: string,
  jobId: string
): Promise<BackgroundJob> {
  return request<BackgroundJob>(
    "POST",
    "/v1/sessions/" +
      encodeURIComponent(sessionId) +
      "/jobs/" +
      encodeURIComponent(jobId) +
      "/stop"
  );
}
