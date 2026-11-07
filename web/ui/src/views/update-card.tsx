import { useEffect, useId, useState } from "react";
import type { KeyboardEvent, ReactNode } from "react";
import type {
  UpdateJob,
  UpdateJobState,
  UpdateRelease,
  UpdateResult,
} from "../api/update";
import { isJobActive, isJobCancellable } from "../api/update";
import { Markdown } from "../lib/markdown";
import { Ic } from "../ui/icons";
import { Badge, Button, IconButton, Progress } from "../ui/gr8r";
import {
  cancelUpdateJob,
  dismissResult,
  forceUpdate,
  later,
  refreshUpdate,
  skipAvailable,
  startUpdate,
  useUpdateState,
} from "./update-store";
import type { UpdateViewState } from "./update-store";

/* The floating self-update card (spec 36, REQ-UPD-002..005). App mounts it
   once after sign-in, so it sits in a corner of every page. Which face it
   shows comes from cardView(): a running job first, then a failed job, the
   last update's result, and finally the offer of a newer release. */

export type CardView =
  | { kind: "none" }
  | { kind: "restarting"; version: string }
  | { kind: "restart-error"; version: string; reason: string }
  | { kind: "progress"; job: UpdateJob }
  | { kind: "failed"; job: UpdateJob }
  | { kind: "result"; result: UpdateResult }
  | { kind: "offer"; release: UpdateRelease }
  | { kind: "manual"; release: UpdateRelease };

export function cardView(view: UpdateViewState): CardView {
  const status = view.status;
  if (!status || !status.supported) {
    return { kind: "none" };
  }
  const job = status.job;
  const available = status.available;
  if (view.restarting) {
    return {
      kind: "restarting",
      version: view.restartVersion || (job && job.version) || "",
    };
  }
  if (view.restartError) {
    return {
      kind: "restart-error",
      version: view.restartVersion || (job && job.version) || "",
      reason: view.restartError,
    };
  }
  if (job && isJobActive(job)) {
    if (job.state === "restarting") {
      return { kind: "restarting", version: job.version };
    }
    return { kind: "progress", job };
  }
  if (job && job.state === "failed" && view.laterVersion !== job.version) {
    return { kind: "failed", job };
  }
  if (status.last_result) {
    return { kind: "result", result: status.last_result };
  }
  if (
    available &&
    available.version &&
    status.skipped !== available.version &&
    view.laterVersion !== available.version
  ) {
    return status.can_apply
      ? { kind: "offer", release: available }
      : { kind: "manual", release: available };
  }
  return { kind: "none" };
}

/* The steps of REQ-UPD-003, in order. */
export const UPDATE_STEPS: { state: UpdateJobState; label: string }[] = [
  { state: "downloading", label: "Downloading" },
  { state: "verifying", label: "Verifying the download" },
  { state: "probing", label: "Checking the new version" },
  { state: "waiting", label: "Waiting for running turns" },
  { state: "installing", label: "Installing" },
  { state: "restarting", label: "Restarting" },
];

export function formatBytes(bytes: number): string {
  const value = Number(bytes) || 0;
  if (value < 1024) {
    return value + " B";
  }
  if (value < 1024 * 1024) {
    return (value / 1024).toFixed(0) + " KB";
  }
  return (value / (1024 * 1024)).toFixed(1) + " MB";
}

function plural(count: number, word: string): string {
  return count + " " + word + (count === 1 ? "" : "s");
}

function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) {
      return undefined;
    }
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(tick);
  }, [active]);
  return now;
}

function Steps({ current }: { current: UpdateJobState }) {
  const activeIndex = UPDATE_STEPS.findIndex((step) => step.state === current);
  return (
    <ol className="upd-steps" aria-label="Update steps">
      {UPDATE_STEPS.map((step, index) => {
        const done = index < activeIndex;
        const active = index === activeIndex;
        return (
          <li
            key={step.state}
            className={"upd-step" + (done ? " done" : "") + (active ? " on" : "")}
            aria-current={active ? "step" : undefined}
          >
            <span className="upd-step-ic">
              {done ? (
                <Ic name="check" size={13} />
              ) : active ? (
                <Ic name="loader-circle" size={13} className="upd-spin" />
              ) : (
                <Ic name="circle" size={9} />
              )}
            </span>
            <span className="upd-step-t">{step.label}</span>
          </li>
        );
      })}
    </ol>
  );
}

function ReleaseNotes({ release }: { release: UpdateRelease }) {
  const [open, setOpen] = useState(false);
  const notesId = useId();
  const notes = (release.notes || "").trim();
  const long = notes.split("\n").length > 4 || notes.length > 240;
  if (!notes && !release.url) {
    return null;
  }
  return (
    <div className="upd-notes-wrap">
      {notes ? (
        <div
          id={notesId}
          className={"upd-notes" + (long && !open ? " clamped" : "") + (open ? " open" : "")}
        >
          <Markdown text={notes} />
        </div>
      ) : null}
      <div className="upd-links">
        {notes && long ? (
          <Button
            variant="ghost"
            size="sm"
            className="upd-more"
            iconRight={open ? "chevron-up" : "chevron-down"}
            aria-expanded={open}
            aria-controls={notesId}
            onClick={() => setOpen((value) => !value)}
          >
            {open ? "Hide changes" : "Show changes"}
          </Button>
        ) : null}
        {release.url ? (
          <a className="upd-link" href={release.url} target="_blank" rel="noreferrer">
            Release page
            <Ic name="external-link" size={12} />
          </a>
        ) : null}
      </div>
    </div>
  );
}

function CopyCommand({ command }: { command: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  }
  return (
    <div className="upd-cmd">
      <code className="mono">{command}</code>
      <IconButton
        size="xs"
        icon={copied ? "check" : "copy"}
        label={copied ? "Copied" : "Copy command"}
        onClick={() => void copy()}
      />
    </div>
  );
}

function ActionError({ message }: { message: string }) {
  if (!message) {
    return null;
  }
  return (
    <div className="alert danger upd-alert" role="alert">
      <Ic name="circle-alert" size={14} />
      <div className="grow">{message}</div>
    </div>
  );
}

interface ShellProps {
  icon: string;
  tone: "accent" | "green" | "amber" | "red";
  title: string;
  sub?: ReactNode;
  /* Later: the close button and Escape. */
  onLater?: () => void;
  /* Escape only, for faces without a close button. */
  onEscape?: () => void;
  footer?: ReactNode;
  children?: ReactNode;
  busy?: boolean;
}

function CardShell({
  icon,
  tone,
  title,
  sub,
  onLater,
  onEscape,
  footer,
  children,
  busy,
}: ShellProps) {
  const titleId = useId();
  const escape = onLater || onEscape;
  function onKeyDown(event: KeyboardEvent<HTMLElement>) {
    if (event.key === "Escape" && escape) {
      event.stopPropagation();
      escape();
    }
  }
  return (
    <aside
      className="upd-card fx-rise"
      aria-labelledby={titleId}
      aria-busy={busy ? true : undefined}
      onKeyDown={onKeyDown}
    >
      <div className="upd-h">
        <span className={"upd-ic " + tone}>
          <Ic name={icon} size={15} />
        </span>
        <div className="upd-ht">
          <h2 id={titleId} className="upd-title">
            {title}
          </h2>
          {sub ? <div className="upd-sub">{sub}</div> : null}
        </div>
        {onLater ? (
          <IconButton
            size="sm"
            icon="x"
            label="Close until later"
            onClick={onLater}
          />
        ) : null}
      </div>
      {children ? <div className="upd-b">{children}</div> : null}
      {footer ? <div className="upd-f">{footer}</div> : null}
    </aside>
  );
}

function releaseSub(release: UpdateRelease, current: string): ReactNode {
  return (
    <>
      <span>You run {current || "an older version"}</span>
      {release.prerelease ? (
        <Badge tone="amber" className="upd-badge">
          Prerelease
        </Badge>
      ) : null}
    </>
  );
}

function OfferCard({
  release,
  view,
  manual,
}: {
  release: UpdateRelease;
  view: UpdateViewState;
  manual: boolean;
}) {
  const status = view.status;
  const busy = view.busy !== "";
  return (
    <CardShell
      icon="circle-arrow-up"
      tone="accent"
      title={"lana " + release.version + " is available"}
      sub={releaseSub(release, status ? status.current : "")}
      onLater={later}
      busy={busy}
      footer={
        <>
          <Button
            variant="ghost"
            size="sm"
            disabled={busy}
            loading={view.busy === "skip"}
            onClick={() => void skipAvailable()}
          >
            Skip this version
          </Button>
          <span className="sp" />
          <Button variant="secondary" size="sm" disabled={busy} onClick={later}>
            Later
          </Button>
          {manual ? null : (
            <Button
              variant="primary"
              size="sm"
              disabled={busy}
              loading={view.busy === "apply"}
              onClick={() => void startUpdate()}
            >
              Update now
            </Button>
          )}
        </>
      }
    >
      {release.upgrade_notes ? (
        <div className="alert warn upd-alert">
          <Ic name="triangle-alert" size={14} />
          <div className="grow">
            <b>Upgrade notes.</b> This release asks for a step from you. Read the
            notes below before you update.
          </div>
        </div>
      ) : null}
      <ReleaseNotes release={release} />
      {manual && status ? (
        <div className="upd-manual">
          <p className="upd-text">
            lana cannot update itself here
            {status.manual_reason ? ": " + status.manual_reason : ""}. Run this
            command to update:
          </p>
          {status.manual_command ? <CopyCommand command={status.manual_command} /> : null}
        </div>
      ) : null}
      <ActionError message={view.actionError} />
    </CardShell>
  );
}

function ProgressCard({ job, view }: { job: UpdateJob; view: UpdateViewState }) {
  const waiting = job.state === "waiting";
  const now = useNow(waiting);
  const busy = view.busy !== "";
  const progress = job.progress;
  const secondsLeft =
    waiting && job.deadline ? Math.max(0, Math.ceil((job.deadline - now) / 1000)) : 0;
  const step = UPDATE_STEPS.find((item) => item.state === job.state);
  let detail = step ? step.label + "…" : "Updating…";
  if (job.state === "downloading" && progress) {
    detail =
      "Downloading " +
      formatBytes(progress.done) +
      (progress.total > 0 ? " of " + formatBytes(progress.total) : "");
  } else if (waiting) {
    detail =
      "Waiting for " +
      plural(Number(job.running_turns) || 0, "running turn") +
      (job.deadline ? " · " + secondsLeft + " s left" : "");
  }
  return (
    <CardShell
      icon="download"
      tone="accent"
      title={"Updating to lana " + job.version}
      busy={busy}
      footer={
        isJobCancellable(job) || waiting ? (
          <>
            <span className="sp" />
            {isJobCancellable(job) ? (
              <Button
                variant="secondary"
                size="sm"
                disabled={busy}
                loading={view.busy === "cancel"}
                onClick={() => void cancelUpdateJob()}
              >
                Cancel
              </Button>
            ) : null}
            {waiting ? (
              <Button
                variant="primary"
                size="sm"
                disabled={busy}
                loading={view.busy === "force"}
                onClick={() => void forceUpdate()}
              >
                Interrupt and update
              </Button>
            ) : null}
          </>
        ) : null
      }
    >
      <Steps current={job.state} />
      <p className="upd-live" role="status" aria-live="polite" aria-atomic="true">
        {detail}
      </p>
      {job.state === "downloading" && progress && progress.total > 0 ? (
        <Progress
          className="upd-prog"
          value={progress.done / progress.total}
          aria-label="Download progress"
        />
      ) : null}
      {waiting ? (
        <p className="upd-text">
          The update installs when the running turns finish or the time runs out.
          Interrupted turns can be continued after the restart.
        </p>
      ) : null}
      <ActionError message={view.actionError} />
    </CardShell>
  );
}

function RestartingCard({ version }: { version: string }) {
  return (
    <CardShell
      icon="rotate-cw"
      tone="accent"
      title={version ? "Restarting into lana " + version : "Restarting lana"}
      busy
    >
      <Steps current="restarting" />
      <p className="upd-live" role="status" aria-live="polite" aria-atomic="true">
        Waiting for lana to come back. This page reconnects by itself.
      </p>
    </CardShell>
  );
}

function RestartErrorCard({ version, reason }: { version: string; reason: string }) {
  return (
    <CardShell
      icon="circle-alert"
      tone="red"
      title="lana did not come back"
      sub={version ? "Update to " + version : undefined}
      footer={
        <>
          <span className="sp" />
          <Button variant="secondary" size="sm" icon="refresh-cw" onClick={() => void refreshUpdate()}>
            Check again
          </Button>
        </>
      }
    >
      <p className="upd-reason" role="alert">
        {reason}
      </p>
      <p className="upd-text">
        If it stays down, run <code className="mono">lana update rollback</code> and start
        the daemon again.
      </p>
    </CardShell>
  );
}

function FailedJobCard({ job, view }: { job: UpdateJob; view: UpdateViewState }) {
  const busy = view.busy !== "";
  const available = view.status && view.status.available;
  const canRetry = Boolean(available && view.status && view.status.can_apply);
  return (
    <CardShell
      icon="circle-x"
      tone="red"
      title={"Update to lana " + job.version + " failed"}
      sub="Nothing changed. lana keeps running the current version."
      onLater={later}
      busy={busy}
      footer={
        <>
          <span className="sp" />
          <Button variant="secondary" size="sm" disabled={busy} onClick={later}>
            Later
          </Button>
          {canRetry ? (
            <Button
              variant="primary"
              size="sm"
              icon="rotate-ccw"
              disabled={busy}
              loading={view.busy === "apply"}
              onClick={() => void startUpdate()}
            >
              Try again
            </Button>
          ) : null}
        </>
      }
    >
      {job.error ? (
        <p className="upd-reason" role="alert">
          {job.error}
        </p>
      ) : null}
      <ActionError message={view.actionError} />
    </CardShell>
  );
}

function ResultCard({ result, view }: { result: UpdateResult; view: UpdateViewState }) {
  const busy = view.busy !== "";
  const footer = (
    <>
      <span className="sp" />
      <Button
        variant="secondary"
        size="sm"
        disabled={busy}
        loading={view.busy === "dismiss"}
        onClick={() => void dismissResult()}
      >
        Dismiss
      </Button>
    </>
  );
  if (result.outcome === "updated") {
    return (
      <CardShell
        icon="circle-check"
        tone="green"
        title={"Updated to lana " + result.to}
        sub={result.from ? "From " + result.from : undefined}
        footer={footer}
        onEscape={() => void dismissResult()}
      >
        <ActionError message={view.actionError} />
      </CardShell>
    );
  }
  const rolledBack = result.outcome === "rolled_back";
  return (
    <CardShell
      icon={rolledBack ? "undo-2" : "circle-x"}
      tone={rolledBack ? "amber" : "red"}
      title={
        rolledBack
          ? "Update to lana " + result.to + " failed and was rolled back"
          : "Update to lana " + result.to + " failed"
      }
      sub={
        rolledBack && result.from
          ? "lana " + result.from + " is running again with your data from before the update."
          : undefined
      }
      footer={footer}
      onEscape={() => void dismissResult()}
    >
      {result.reason ? <p className="upd-reason">{result.reason}</p> : null}
      <ActionError message={view.actionError} />
    </CardShell>
  );
}

export function UpdateCardView({ view }: { view: UpdateViewState }) {
  const face = cardView(view);
  switch (face.kind) {
    case "offer":
      return <OfferCard release={face.release} view={view} manual={false} />;
    case "manual":
      return <OfferCard release={face.release} view={view} manual />;
    case "progress":
      return <ProgressCard job={face.job} view={view} />;
    case "restarting":
      return <RestartingCard version={face.version} />;
    case "restart-error":
      return <RestartErrorCard version={face.version} reason={face.reason} />;
    case "failed":
      return <FailedJobCard job={face.job} view={view} />;
    case "result":
      return <ResultCard result={face.result} view={view} />;
    default:
      return null;
  }
}

export function UpdateCard() {
  const view = useUpdateState();
  return <UpdateCardView view={view} />;
}
