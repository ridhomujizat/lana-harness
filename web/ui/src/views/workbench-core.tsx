import { useCallback, useEffect, useRef, useState } from "react";

import { errorMessage } from "../api/client";
import { getWorkbenchCore, restartWorkbenchCore, type WorkbenchCore } from "../api/workbench";
import { Badge, Button, Modal } from "../ui/gr8r";
import { Ic } from "../ui/icons";

/* Spec 84: restarting the Workbench core. The core owns every terminal pane
   and keeps running across daemon updates, so after an update it can be
   older than the daemon (REQ-WBR-002). Restarting it closes and reopens the
   panes; the spec 60 restore resumes Claude Code and Antigravity
   conversations (REQ-WBR-003). The same confirm dialog serves Settings >
   General, the Workbench notice, and the Accounts popup. */

export const RESTART_TITLE = "Restart Workbench";
export const RESTART_CONFIRM =
  "All terminal panes close and reopen. Claude Code and Antigravity conversations resume; a running turn stops.";
export const CORE_OUTDATED_NOTICE = "The Workbench is running an older version. Restart it to use the new features.";
export const SWITCH_CORE_OUTDATED = "Restart the Workbench to switch accounts in place";

/* Sent on window after a restart, so an open Workbench reloads its panes and
   every reader of the core state updates. detail is the new WorkbenchCore. */
export const WORKBENCH_RESTARTED_EVENT = "lana:workbench-restarted";

export function restartedText(panes: number): string {
  return "Restarted. " + panes + (panes === 1 ? " pane" : " panes") + " restored.";
}

export function outdatedText(core: WorkbenchCore): string {
  return "Restart to use the new version (" + core.daemon_version + ")";
}

/* "0.6.0-beta.13 (abc1234)", "Not running", or the older core's note. */
export function coreVersionText(core: WorkbenchCore): string {
  if (!core.running) return "Not running";
  if (!core.version) return "An older version that does not report its version";
  return core.commit ? core.version + " (" + core.commit + ")" : core.version;
}

/* Reads GET /v1/workbench/core once and follows restarts. A failed read
   leaves core null, so nothing outdated shows. */
export function useWorkbenchCore(): { core: WorkbenchCore | null; error: string; setCore: (core: WorkbenchCore) => void } {
  const [core, setCore] = useState<WorkbenchCore | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let alive = true;
    getWorkbenchCore()
      .then((value) => alive && setCore(value))
      .catch((err) => alive && setError(errorMessage(err)));
    const onRestarted = (event: Event) => {
      const detail = (event as CustomEvent<WorkbenchCore>).detail;
      if (detail) setCore(detail);
    };
    window.addEventListener(WORKBENCH_RESTARTED_EVENT, onRestarted);
    return () => {
      alive = false;
      window.removeEventListener(WORKBENCH_RESTARTED_EVENT, onRestarted);
    };
  }, []);
  return { core, error, setCore };
}

type Phase = "confirm" | "busy" | "done" | "error";

/* Asks first (REQ-WBR-005), then restarts and shows the result. It cannot be
   dismissed while the restart runs. */
export function RestartWorkbenchDialog({
  onClose,
  onRestarted,
}: {
  onClose: () => void;
  onRestarted?: (core: WorkbenchCore) => void;
}) {
  const [phase, setPhase] = useState<Phase>("confirm");
  const [message, setMessage] = useState("");
  const busy = phase === "busy";

  const restart = () => {
    setPhase("busy");
    setMessage("");
    restartWorkbenchCore()
      .then((core) => {
        setMessage(restartedText(core.sessions));
        setPhase("done");
        onRestarted?.(core);
        window.dispatchEvent(new CustomEvent(WORKBENCH_RESTARTED_EVENT, { detail: core }));
      })
      .catch((err) => {
        setMessage(errorMessage(err));
        setPhase("error");
      });
  };

  let body;
  if (phase === "busy") {
    body = (
      <p className="wb-core-dialog-text" role="status">
        Restarting the Workbench…
      </p>
    );
  } else if (phase === "done") {
    body = (
      <p className="wb-core-dialog-text" role="status">
        {message}
      </p>
    );
  } else if (phase === "error") {
    body = (
      <p className="wb-core-dialog-text settings-update-error" role="alert">
        {message}
      </p>
    );
  } else {
    body = <p className="wb-core-dialog-text">{RESTART_CONFIRM}</p>;
  }

  return (
    <Modal
      title={RESTART_TITLE}
      size="sm"
      dismissible={!busy}
      onClose={onClose}
      footer={
        <>
          <span className="sp" />
          {phase === "done" ? (
            <Button variant="primary" onClick={onClose} data-autofocus>
              Close
            </Button>
          ) : (
            <>
              <Button variant="secondary" disabled={busy} onClick={onClose}>
                {phase === "error" ? "Close" : "Cancel"}
              </Button>
              <Button variant="primary" loading={busy} disabled={busy} onClick={restart}>
                {busy ? "Restarting…" : phase === "error" ? "Try again" : "Restart"}
              </Button>
            </>
          )}
        </>
      }
    >
      {body}
    </Modal>
  );
}

/* Settings > General > Workbench: the core's version, Outdated with the
   daemon's version, and Restart Workbench (REQ-WBR-005). */
export function WorkbenchCoreRow() {
  const { core, error, setCore } = useWorkbenchCore();
  const [open, setOpen] = useState(false);
  const [result, setResult] = useState("");

  let detail: string;
  if (core) detail = coreVersionText(core);
  else detail = error || "Reading the Workbench core…";

  return (
    <>
      <div className="srow">
        <div>
          <div className="t">
            Workbench core
            {core?.outdated ? (
              <>
                {" "}
                <Badge tone="amber">Outdated</Badge>
              </>
            ) : null}
          </div>
          <div className="d">
            {detail}
            {core?.outdated ? ". " + outdatedText(core) + "." : ""}
          </div>
          {result ? (
            <div className="d wb-core-result" role="status">
              {result}
            </div>
          ) : null}
        </div>
        <div className="settings-update-control">
          <Button
            variant="secondary"
            onClick={() => {
              setResult("");
              setOpen(true);
            }}
          >
            {RESTART_TITLE}
          </Button>
        </div>
      </div>
      {open ? (
        <RestartWorkbenchDialog
          onClose={() => setOpen(false)}
          onRestarted={(next) => {
            setCore(next);
            setResult(restartedText(next.sessions));
          }}
        />
      ) : null}
    </>
  );
}

const DISMISS_KEY = "lana.workbench.coreNotice.dismissed";

function readDismissed(): string {
  try {
    return window.localStorage.getItem(DISMISS_KEY) || "";
  } catch {
    return "";
  }
}

function writeDismissed(value: string): void {
  try {
    window.localStorage.setItem(DISMISS_KEY, value);
  } catch {
    /* Without storage the notice hides until the page reloads. */
  }
}

/* The build a dismissal applies to: a later update shows the notice again. */
function buildKey(core: WorkbenchCore): string {
  return core.daemon_version + "+" + core.daemon_commit;
}

/* The Workbench's slim notice while the core is outdated (REQ-WBR-006). */
export function WorkbenchCoreNotice({ core, onRestart }: { core: WorkbenchCore | null; onRestart: () => void }) {
  const [dismissed, setDismissed] = useState(readDismissed);
  if (!core?.outdated || dismissed === buildKey(core)) return null;
  return (
    <div className="wb-core-notice" role="status">
      <Ic name="refresh-cw" size={14} className="wb-core-notice-icon" />
      <span className="wb-core-notice-text">{CORE_OUTDATED_NOTICE}</span>
      <button type="button" className="btn btn-primary btn-sm" onClick={onRestart}>
        Restart
      </button>
      <button
        type="button"
        className="wb-icon-btn"
        aria-label="Dismiss"
        onClick={() => {
          const key = buildKey(core);
          writeDismissed(key);
          setDismissed(key);
        }}
      >
        <Ic name="x" size={14} />
      </button>
    </div>
  );
}

/* The Workbench page's restart: the core state, the notice, and the dialog
   the notice and the Accounts popup open. onRestarted runs after any restart,
   this page's or Settings', so the page reloads its panes. */
export function useWorkbenchRestart(onRestarted: () => void) {
  const { core } = useWorkbenchCore();
  const [open, setOpen] = useState(false);
  const callback = useRef(onRestarted);
  useEffect(() => {
    callback.current = onRestarted;
  }, [onRestarted]);
  useEffect(() => {
    const handler = () => callback.current();
    window.addEventListener(WORKBENCH_RESTARTED_EVENT, handler);
    return () => window.removeEventListener(WORKBENCH_RESTARTED_EVENT, handler);
  }, []);
  const show = useCallback(() => setOpen(true), []);
  return {
    core,
    outdated: Boolean(core?.outdated),
    open: show,
    notice: <WorkbenchCoreNotice core={core} onRestart={show} />,
    dialog: open ? <RestartWorkbenchDialog onClose={() => setOpen(false)} /> : null,
  };
}
