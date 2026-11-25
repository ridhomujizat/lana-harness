import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { errorMessage } from "../api/client";
import { listSessionJobs, stopSessionJob } from "../api/jobs";
import type { BackgroundJob } from "../api/jobs";
import { Ic } from "../ui/icons";

/* The composer tray (composer redesign 2026-11-25, FigCraft page "Composer
   redesign"): the background row at the top of the composer shell, styled
   after the Workbench Claude chat tray (REQ-JBN-021). It opens into the job
   list in place: running jobs first, then ended ones, with logs and Stop. The job
   list refreshes every 3 s while a job runs and whenever the session stream
   bumps refreshKey (REQ-JBN-022). */

const JOB_POLL_MS = 3000;

function ageText(fromMs: number, toMs?: number | null): string {
  const seconds = Math.floor(Math.max(0, (toMs || Date.now()) - fromMs) / 1000);
  if (seconds < 60) {
    return seconds + "s";
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return minutes + "m";
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return hours + "h";
  }
  return Math.floor(hours / 24) + "d";
}

function jobIcon(job: BackgroundJob): { name: string; tone: string } {
  if (job.status === "running") {
    return { name: "loader-circle", tone: "running" };
  }
  if (job.status === "killed") {
    return { name: "square", tone: "stopped" };
  }
  return job.exit_code === 0
    ? { name: "circle-check", tone: "ok" }
    : { name: "circle-x", tone: "err" };
}

function jobMeta(job: BackgroundJob): string {
  if (job.status === "running") {
    return "running · " + ageText(job.started_at) + (job.pid ? " · pid " + job.pid : "");
  }
  const ago = job.ended_at ? ageText(job.ended_at) + " ago" : "";
  if (job.status === "killed") {
    return "stopped" + (ago ? " · " + ago : "");
  }
  return "exited " + job.exit_code + (ago ? " · " + ago : "");
}

export interface ComposerTrayProps {
  /* The session whose jobs to show; "" shows nothing. */
  sessionId: string;
  /* REQ-JBN-022: bumped by the session stream on a tool/result or a job
     notice, so a job started mid-visit appears without a reload. */
  refreshKey?: number;
  /* The task panel can be opened from outside (the /tasks command). */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

export function ComposerTray({
  sessionId,
  refreshKey = 0,
  open: openProp,
  onOpenChange,
}: ComposerTrayProps) {
  const [jobs, setJobs] = useState<BackgroundJob[]>([]);
  const [openState, setOpenState] = useState(false);
  const [openLogs, setOpenLogs] = useState<string[]>([]);
  const [stopping, setStopping] = useState<string[]>([]);
  const [stopErrors, setStopErrors] = useState<Record<string, string>>({});
  const [loadError, setLoadError] = useState("");
  const open = openProp === undefined ? openState : openProp;

  function setOpen(next: boolean) {
    if (openProp === undefined) {
      setOpenState(next);
    }
    if (onOpenChange) {
      onOpenChange(next);
    }
  }

  const load = useCallback(async () => {
    if (!sessionId) {
      setJobs([]);
      return;
    }
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
    setOpenState(false);
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

  const runningCount = jobs.filter((job) => job.status === "running").length;
  const hasRunning = runningCount > 0;

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
  const firstEnded = rows.findIndex((job) => job.status !== "running");

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

  async function stopAll() {
    for (const job of rows) {
      if (job.status === "running") {
        await stop(job.id);
      }
    }
  }

  /* The head row, like the Workbench Claude chat tray (REQ-WC-062): what
     runs, how many ended, a peek at the newest running command, and a
     chevron. The open list keeps Logs and Stop per job. */
  const latest = rows.find((job) => job.status === "running") || null;
  const ended = jobs.length - runningCount;
  const title = hasRunning
    ? runningCount + (runningCount === 1 ? " background task" : " background tasks") + " running"
    : "Background";
  const expanded = open && Boolean(sessionId);

  if (!sessionId || (jobs.length === 0 && !expanded)) {
    /* A fetch error still surfaces a small notice so a broken endpoint is
       not silent. */
    return loadError ? (
      <p className="cx-tray-error" role="alert">
        {loadError}
      </p>
    ) : null;
  }

  return (
    <div className={"cx-tray" + (expanded ? " is-open" : "")}>
      <button
        type="button"
        className="cx-tray-head"
        aria-expanded={expanded}
        aria-label={expanded ? "Close background tasks" : title}
        onClick={() => setOpen(!expanded)}
      >
        {hasRunning ? (
          <Ic name="loader-circle" size={13} className="cx-spin cx-run" />
        ) : (
          <Ic name="layers" size={13} />
        )}
        <span className="cx-tray-title">{title}</span>
        {ended ? <span className="cx-tray-sub">{ended + " done"}</span> : null}
        {!expanded && latest ? (
          <span className="cx-tray-peek" title={latest.command}>
            {latest.command}
          </span>
        ) : null}
        <span className="cx-sp" />
        <Ic name={expanded ? "chevron-down" : "chevron-up"} size={13} />
      </button>
      {expanded ? (
        <div className="cx-tray-list" role="list" aria-label="Background tasks">
          {rows.length === 0 ? (
            <p className="cx-tasks-empty">No background tasks in this session.</p>
          ) : null}
          {runningCount > 1 ? (
            <div className="cx-tray-actions">
              <button type="button" className="cx-link" onClick={() => void stopAll()}>
                Stop all
              </button>
            </div>
          ) : null}
          {rows.map((job, index) => {
            const logsId = "job-logs-" + job.id;
            const logsOpen = openLogs.includes(job.id);
            const busy = stopping.includes(job.id);
            const icon = jobIcon(job);
            return (
              <div key={job.id} role="listitem">
                {index === firstEnded && firstEnded > 0 ? <div className="cx-group">Ended</div> : null}
                <div className={"cx-job" + (logsOpen ? " is-open" : "") + (job.status === "running" ? "" : " is-done")}>
                  <div className="cx-job-head">
                    <Ic
                      name={icon.name}
                      size={13}
                      className={"cx-job-ico tone-" + icon.tone + (icon.tone === "running" ? " cx-spin" : "")}
                    />
                    <Ic name="square-terminal" size={13} className="cx-job-kind" />
                    <span className="cx-job-text">
                      <span className="cx-job-cmd" title={job.command}>
                        {job.command}
                      </span>
                      <span className={"cx-job-meta tone-" + icon.tone}>{jobMeta(job)}</span>
                    </span>
                    <button
                      type="button"
                      className={"cx-job-btn" + (logsOpen ? " is-on" : "")}
                      aria-expanded={logsOpen}
                      aria-controls={logsId}
                      onClick={() => toggleLogs(job.id)}
                    >
                      Logs
                    </button>
                    {job.status === "running" ? (
                      <button
                        type="button"
                        className="cx-job-btn cx-job-stop"
                        disabled={busy}
                        onClick={() => void stop(job.id)}
                      >
                        {busy ? "Stopping…" : "Stop"}
                      </button>
                    ) : null}
                  </div>
                  {logsOpen ? (
                    <pre className="cx-job-log" id={logsId}>
                      {job.tail || "No output yet."}
                    </pre>
                  ) : null}
                  {stopErrors[job.id] ? (
                    <p className="cx-job-error" role="alert">
                      {stopErrors[job.id]}
                    </p>
                  ) : null}
                </div>
              </div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}
