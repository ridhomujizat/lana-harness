import { useState } from "react";
import { Ic } from "../ui/icons";
import { jobNoticeIcon } from "../lib/format";
import type { JobNoticePayload } from "../api/sessions";

/* REQ-JBN-020: one compact system line for an ended background job (composer
   redesign 2026-11-25): a tinted status chip, the outcome, the exit code in
   mono, the command in a mono chip, and a Log toggle on the right that shows
   the stored output tail. */

function noticeId(job: JobNoticePayload): string {
  return "job-notice-" + String(job.id || "job").replace(/[^a-zA-Z0-9_-]/g, "-");
}

function outcome(job: JobNoticePayload): { label: string; code: string } {
  if (job.status === "killed") {
    return { label: "Background task stopped", code: "" };
  }
  if (job.exit_code === 0) {
    return { label: "Background task finished", code: "exit 0" };
  }
  return { label: "Background task failed", code: "exit " + job.exit_code };
}

export function JobNoticeLine({ job }: { job: JobNoticePayload }) {
  const [open, setOpen] = useState(false);
  const icon = jobNoticeIcon(job);
  const detailId = noticeId(job);
  const { label, code } = outcome(job);
  return (
    <div className={"job-notice tone-" + icon.tone + (open ? " is-open" : "")}>
      <button
        type="button"
        className="job-notice-row"
        aria-expanded={open}
        aria-controls={detailId}
        onClick={() => setOpen((value) => !value)}
      >
        <span className="job-notice-ico" aria-hidden="true">
          <Ic name={icon.name} size={14} />
        </span>
        <span className="job-notice-label">{label}</span>
        {code ? <code className="job-notice-code">{code}</code> : null}
        {job.command ? (
          <code className="job-notice-cmd" title={job.command}>
            {job.command}
          </code>
        ) : null}
        <span className="job-notice-log">
          Log
          <Ic name="chevron-right" size={12} className="job-notice-chev" />
        </span>
      </button>
      {open ? (
        <pre className="job-notice-tail" id={detailId}>
          {job.tail || "No output."}
        </pre>
      ) : null}
    </div>
  );
}
