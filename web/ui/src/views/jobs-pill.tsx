import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { errorMessage } from "../api/client";
import { listSessionJobs, stopSessionJob } from "../api/jobs";
import type { BackgroundJob } from "../api/jobs";
import { Ic } from "../ui/icons";

/* REQ-JBN-021, REQ-JBN-022: the background pill above the composer. It
   replaces the old strip: a pulsing dot and the running count while at least
   one job runs; clicking it opens a panel listing the jobs (running first,
   then ended newest first). The list refreshes every 3 s while a job runs and
   whenever the session stream bumps refreshKey. */

const JOB_POLL_MS = 3000;

function jobStatusText(job: BackgroundJob): string {
  if (job.status === "running") {
    return "running";
  }
  if (job.status === "killed") {
    return "killed";
  }
  return "exited with code " + job.exit_code;
}

/* The badge tone; a nonzero exit reads as a warning. */
function jobTone(job: BackgroundJob): string {
  if (job.status === "running") {
    return "running";
  }
  if (job.status === "killed") {
    return "killed";
  }
  return job.exit_code === 0 ? "exited" : "warning";
}

function jobAgeText(startedAt: number): string {
  const minutes = Math.floor(Math.max(0, Date.now() - startedAt) / 60000);
  if (minutes < 1) {
    return "started just now";
  }
  if (minutes < 60) {
    return "started " + minutes + "m ago";
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return "started " + hours + "h ago";
  }
  return "started " + Math.floor(hours / 24) + "d ago";
}

export interface JobsPillProps {
  sessionId: string;
  /* REQ-JBN-022: bumped by the session stream on a tool/result or a job
     notice, so a job started mid-visit appears without a reload. */
  refreshKey?: number;
}

export function JobsPill({ sessionId, refreshKey = 0 }: JobsPillProps) {
  const [jobs, setJobs] = useState<BackgroundJob[]>([]);
  const [open, setOpen] = useState(false);
  const [openLogs, setOpenLogs] = useState<string[]>([]);
  const [stopping, setStopping] = useState<string[]>([]);
  const [stopErrors, setStopErrors] = useState<Record<string, string>>({});
  const [loadError, setLoadError] = useState("");

  const load = useCallback(async () => {
    try {
      const list = await listSessionJobs(sessionId);
      setJobs(Array.isArray(list) ? list : []);
      setLoadError("");
    } catch (failure: unknown) {
      setLoadError(errorMessage(failure));
    }
  }, [sessionId]);

  useEffect(() => {
    setJobs([]);
    setOpen(false);
    setOpenLogs([]);
    setStopping([]);
    setStopErrors({});
    setLoadError("");
    void load();
  }, [load]);

  /* A refreshKey change re-reads the list; the session reset above already
     covers the first read, so the initial value is skipped (REQ-JBN-022). */
  const lastRefresh = useRef(refreshKey);
  useEffect(() => {
    if (lastRefresh.current === refreshKey) {
      return;
    }
    lastRefresh.current = refreshKey;
    void load();
  }, [refreshKey, load]);

  const hasRunning = jobs.some((job) => job.status === "running");

  useEffect(() => {
    if (!hasRunning) {
      return undefined;
    }
    const timer = window.setInterval(() => {
      void load();
    }, JOB_POLL_MS);
    return () => window.clearInterval(timer);
  }, [hasRunning, load]);

  /* Running jobs first, then ended, each newest first. */
  const rows = useMemo(
    () =>
      jobs.slice().sort((left, right) => {
        const leftRank = left.status === "running" ? 0 : 1;
        const rightRank = right.status === "running" ? 0 : 1;
        if (leftRank !== rightRank) {
          return leftRank - rightRank;
        }
        return right.started_at - left.started_at;
      }),
    [jobs]
  );

  const runningCount = jobs.filter((job) => job.status === "running").length;

  function toggleLogs(jobId: string) {
    setOpenLogs((current) =>
      current.includes(jobId)
        ? current.filter((id) => id !== jobId)
        : current.concat([jobId])
    );
  }

  async function stop(jobId: string) {
    setStopping((current) =>
      current.includes(jobId) ? current : current.concat([jobId])
    );
    setStopErrors((current) => {
      const next = { ...current };
      delete next[jobId];
      return next;
    });
    try {
      const stopped = await stopSessionJob(sessionId, jobId);
      setJobs((current) =>
        current.map((job) => (job.id === jobId ? stopped : job))
      );
    } catch (failure: unknown) {
      setStopErrors((current) => ({
        ...current,
        [jobId]: errorMessage(failure),
      }));
    } finally {
      setStopping((current) => current.filter((id) => id !== jobId));
    }
  }

  /* REQ-JBN-021: the pill hides when nothing runs. A fetch error still
     surfaces a small notice so a broken endpoint is not silent. */
  if (!hasRunning) {
    if (!loadError) {
      return null;
    }
    return (
      <section className="jobs-pill-wrap" aria-label="Background jobs">
        <p className="jobs-pill-error" role="alert">
          {loadError}
        </p>
      </section>
    );
  }

  return (
    <section className="jobs-pill-wrap" aria-label="Background jobs">
      {open ? (
        <div className="jobs-panel" role="dialog" aria-label="Background jobs">
          {rows.map((job) => {
            const logsId = "job-logs-" + job.id;
            const logsOpen = openLogs.includes(job.id);
            const busy = stopping.includes(job.id);
            return (
              <div className="job-row" key={job.id}>
                <div className="job-main">
                  <span className="job-command" title={job.command}>
                    {job.command}
                  </span>
                  <span className={"job-badge job-badge-" + jobTone(job)}>
                    {jobStatusText(job)}
                  </span>
                  <span className="job-age">{jobAgeText(job.started_at)}</span>
                </div>
                <div className="job-actions">
                  <button
                    type="button"
                    className="job-action"
                    aria-expanded={logsOpen}
                    aria-controls={logsId}
                    onClick={() => toggleLogs(job.id)}
                  >
                    Logs
                  </button>
                  {job.status === "running" ? (
                    <button
                      type="button"
                      className="job-action job-stop"
                      disabled={busy}
                      onClick={() => void stop(job.id)}
                    >
                      {busy ? "Stopping…" : "Stop"}
                    </button>
                  ) : null}
                </div>
                {logsOpen ? (
                  <pre className="job-logs" id={logsId}>
                    {job.tail || "No output yet."}
                  </pre>
                ) : null}
                {stopErrors[job.id] ? (
                  <p className="job-error" role="alert">
                    {stopErrors[job.id]}
                  </p>
                ) : null}
              </div>
            );
          })}
        </div>
      ) : null}
      <button
        type="button"
        className="jobs-pill"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <span className="jobs-pill-dot" aria-hidden="true" />
        <span className="jobs-pill-text">
          {runningCount === 1
            ? "1 background task"
            : runningCount + " background tasks"}
        </span>
        <Ic
          name="chevron-up"
          size={13}
          className={"jobs-pill-chev" + (open ? " is-open" : "")}
        />
      </button>
    </section>
  );
}
