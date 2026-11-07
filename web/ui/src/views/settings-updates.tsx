import { useEffect, useState } from "react";
import type { UpdateChannel } from "../api/update";
import { isJobActive } from "../api/update";
import { Badge, Button, Select, Skeleton, Toggle } from "../ui/gr8r";
import { UPDATE_STEPS } from "./update-card";
import {
  checkNow,
  saveSettings,
  startUpdate,
  useUpdateState,
} from "./update-store";

/* Settings > General > Updates (spec 36, REQ-UPD-007): the running version,
   the channel, the automatic check, the last check, Check now, and the same
   Update flow as the floating card. It shares the card's store, so a job
   started here shows its progress in the card. */

const CHANNEL_OPTIONS: { value: UpdateChannel; label: string }[] = [
  { value: "stable", label: "Stable" },
  { value: "beta", label: "Beta" },
];

export function relativeTime(ms: number | null, now: number): string {
  if (!ms) {
    return "";
  }
  const diff = Math.max(0, now - ms);
  const minutes = Math.floor(diff / 60_000);
  if (minutes < 1) {
    return "just now";
  }
  if (minutes < 60) {
    return minutes + " min ago";
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return hours + " h ago";
  }
  return Math.floor(hours / 24) + " d ago";
}

function useMinuteClock(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const tick = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(tick);
  }, []);
  return now;
}

export function SettingsUpdatesSection() {
  const view = useUpdateState();
  const now = useMinuteClock();
  const [message, setMessage] = useState("");
  const status = view.status;
  const busy = view.busy !== "";

  async function check() {
    setMessage("Checking for updates…");
    const result = await checkNow();
    if (!result) {
      setMessage("");
      return;
    }
    if (result.check_error) {
      setMessage("");
    } else if (result.available) {
      setMessage("lana " + result.available.version + " is available.");
    } else {
      setMessage("lana is up to date.");
    }
  }

  async function changeChannel(value: string) {
    if (!status || value === status.channel) {
      return;
    }
    setMessage("Saving the channel…");
    const result = await saveSettings({ channel: value as UpdateChannel });
    setMessage(result ? "Channel set to " + value + "." : "");
  }

  async function changeCheck(value: boolean) {
    setMessage("Saving…");
    const result = await saveSettings({ check: value });
    setMessage(
      result ? (value ? "Automatic check is on." : "Automatic check is off.") : ""
    );
  }

  let body;
  if (!status) {
    body = view.loadError ? (
      <p className="settings-update-error">{view.loadError}</p>
    ) : (
      <div className="settings-loading-inline" aria-busy="true">
        <span className="sr">Loading the update status…</span>
        <Skeleton width={200} height={12} />
      </div>
    );
  } else if (!status.supported) {
    body = (
      <div className="srow">
        <div>
          <div className="t">lana {status.current || "unknown"}</div>
          <div className="d">
            This build cannot update itself. Development builds and this
            platform need a manual install.
          </div>
        </div>
      </div>
    );
  } else {
    const job = status.job;
    const active = isJobActive(job) || view.restarting;
    const available = status.available;
    const lastCheck = status.checked_at
      ? "Last checked " + relativeTime(status.checked_at, now) + "."
      : "Not checked yet.";
    const upToDate = status.checked_at && !available && !status.check_error;
    const step =
      job && UPDATE_STEPS.find((item) => item.state === job.state);
    body = (
      <>
        <div className="srow">
          <div>
            <div className="t">lana {status.current}</div>
            <div className="d">
              {lastCheck}
              {upToDate ? " lana is up to date." : ""}
            </div>
            {status.check_error ? (
              <p className="settings-update-error" role="alert">
                The last check failed: {status.check_error}
              </p>
            ) : null}
          </div>
          <div className="settings-update-control">
            <Button
              variant="secondary"
              icon="refresh-cw"
              disabled={busy || active}
              loading={view.busy === "check"}
              onClick={() => void check()}
            >
              Check now
            </Button>
          </div>
        </div>

        {available || active ? (
          <div className="srow">
            <div>
              <div className="t">
                {active && job
                  ? "Updating to lana " + job.version
                  : available
                    ? "lana " + available.version + " is available"
                    : ""}
              </div>
              <div className="d">
                {active
                  ? view.restarting || (job && job.state === "restarting")
                    ? "Restarting. The card in the corner shows when lana is back."
                    : (step ? step.label : "Working") +
                      ". The card in the corner shows the progress."
                  : !status.can_apply
                    ? "lana cannot update itself here" +
                      (status.manual_reason ? ": " + status.manual_reason : "") +
                      "."
                    : available && available.upgrade_notes
                      ? "This release has upgrade notes. Read them before you update."
                      : "The update downloads, checks the new version, and restarts lana."}
              </div>
              {!active && !status.can_apply && status.manual_command ? (
                <div className="upd-cmd settings-update-manual">
                  <code className="mono">{status.manual_command}</code>
                </div>
              ) : null}
            </div>
            <div className="settings-update-control">
              {available && status.skipped === available.version && !active ? (
                <Badge tone="gray">Skipped</Badge>
              ) : null}
              {available && available.prerelease ? (
                <Badge tone="amber">Prerelease</Badge>
              ) : null}
              {!active && status.can_apply && available ? (
                <Button
                  variant="primary"
                  icon="circle-arrow-up"
                  disabled={busy}
                  loading={view.busy === "apply"}
                  onClick={() => void startUpdate()}
                >
                  Update
                </Button>
              ) : null}
            </div>
          </div>
        ) : null}

        <div className="srow">
          <div>
            <div className="t">Channel</div>
            <div className="d">
              Stable offers final releases. Beta also offers prereleases.
            </div>
          </div>
          <div>
            <Select
              aria-label="Update channel"
              options={CHANNEL_OPTIONS}
              value={status.channel}
              disabled={busy || active}
              onChange={(value) => void changeChannel(value)}
            />
          </div>
        </div>

        <div className="srow">
          <div>
            <div className="t">Check automatically</div>
            <div className="d">Look for a new release every 6 hours.</div>
          </div>
          <div>
            <Toggle
              aria-label="Check for updates automatically"
              checked={status.check}
              disabled={busy}
              onChange={(value) => void changeCheck(value)}
            />
          </div>
        </div>
      </>
    );
  }

  return (
    <section className="sblock" aria-labelledby="settings-updates-title">
      <h2 id="settings-updates-title">Updates</h2>
      {body}
      {view.actionError ? (
        <p className="settings-update-error" role="alert">
          {view.actionError}
        </p>
      ) : null}
      {message ? (
        <p className="settings-status" role="status" aria-live="polite" aria-atomic="true">
          {message}
        </p>
      ) : null}
    </section>
  );
}
