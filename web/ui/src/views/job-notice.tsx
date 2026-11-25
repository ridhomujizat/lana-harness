import { useState } from "react";
import { Ic } from "../ui/icons";
import { jobNoticeIcon, jobNoticeLabel } from "../lib/format";
import type { JobNoticePayload } from "../api/sessions";

/* REQ-JBN-020: one compact system line for an ended background job — a status
   icon, the notice text, then the command in mono (truncated with ellipsis).
   Clicking the line toggles the stored output tail in a <pre>. */

function noticeId(job: JobNoticePayload): string {
  return "job-notice-" + String(job.id || "job").replace(/[^a-zA-Z0-9_-]/g, "-");
}

export function JobNoticeLine({ job }: { job: JobNoticePayload }) {
  const [open, setOpen] = useState(false);
  const icon = jobNoticeIcon(job);
  const detailId = noticeId(job);
  return (
    <div className={"job-notice tone-" + icon.tone}>
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
        <span className="job-notice-label">{jobNoticeLabel(job)}</span>
        {job.command ? (
          <code className="job-notice-cmd" title={job.command}>
            {job.command}
          </code>
        ) : null}
      </button>
      {open ? (
        <pre className="job-notice-tail" id={detailId}>
          {job.tail || "No output."}
        </pre>
      ) : null}
    </div>
  );
}
