import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, errorMessage } from "../api/client";
import {
  cancelDaemonRestart,
  daemonInfo,
  restartDaemon,
  waitForDaemonRestart,
} from "../api/daemon";
import type { DaemonInfo } from "../api/daemon";
import { ErrorDetails } from "../ui/ui";
import { Ic } from "../ui/icons";
import { Button, Modal, Skeleton } from "../ui/gr8r";

/* REQ-TFB-003: one restart flow for Settings > General and the Telegram
   connect step. start() posts the restart (API-TFB-002); a draining answer
   keeps the waiting state with the running-turn count and a live countdown
   while the owner can force the restart or cancel it. */

const DRAIN_POLL_MS = 1500;
const DRAIN_GRACE_MS = 30000;
const DEFAULT_DRAIN_MS = 60000;

export type RestartPhase = "idle" | "restarting" | "waiting";

export interface DaemonRestartOptions {
  onRestarted?: () => void;
  onError?: (error: unknown) => void;
}

export interface DaemonRestartController {
  phase: RestartPhase;
  busy: boolean;
  runningTurns: number;
  secondsLeft: number;
  error: string;
  details: unknown[];
  start: (previousStartedAt: number) => Promise<void>;
  force: () => Promise<void>;
  cancel: () => Promise<void>;
}

export function useDaemonRestart(
  options: DaemonRestartOptions = {}
): DaemonRestartController {
  const [phase, setPhase] = useState<RestartPhase>("idle");
  const [busy, setBusy] = useState(false);
  const [runningTurns, setRunningTurns] = useState(0);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [now, setNow] = useState(() => Date.now());
  /* The deadline is mirrored in state so the countdown can render without
     reading a ref during render. */
  const [deadline, setDeadline] = useState(0);

  const optionsRef = useRef(options);
  useEffect(() => {
    optionsRef.current = options;
  });

  const previousRef = useRef(0);
  const deadlineRef = useRef(0);
  const doneRef = useRef(false);

  const setFailure = useCallback((failure: unknown) => {
    setError(errorMessage(failure));
    setDetails(failure instanceof ApiError ? failure.details : []);
  }, []);

  const reset = useCallback(() => {
    doneRef.current = false;
    deadlineRef.current = 0;
    setDeadline(0);
    setRunningTurns(0);
    setPhase("idle");
  }, []);

  const complete = useCallback(() => {
    if (doneRef.current) {
      return;
    }
    doneRef.current = true;
    deadlineRef.current = 0;
    setDeadline(0);
    setRunningTurns(0);
    setPhase("idle");
    const handler = optionsRef.current.onRestarted;
    if (handler) {
      handler();
    }
  }, []);

  const fail = useCallback(
    (failure: unknown) => {
      doneRef.current = false;
      setFailure(failure);
      setPhase("idle");
      const handler = optionsRef.current.onError;
      if (handler) {
        handler(failure);
      }
    },
    [setFailure]
  );

  /* API-TFB-002: while draining, poll GET /v1/daemon for the running-turn
     count and the deadline. The daemon is back when its started_at changed. */
  const pollDrain = useCallback(async () => {
    if (deadlineRef.current > 0 && Date.now() > deadlineRef.current + DRAIN_GRACE_MS) {
      fail(new ApiError(0, "timeout", "The restart did not finish."));
      return;
    }
    let info: DaemonInfo;
    try {
      info = await daemonInfo();
    } catch {
      /* The daemon is down while it restarts; keep polling. */
      return;
    }
    if (info.restart && typeof info.restart.deadline === "number" && info.restart.deadline > 0) {
      deadlineRef.current = info.restart.deadline;
      setDeadline(info.restart.deadline);
      setNow(Date.now());
    }
    if (Array.isArray(info.running_turns)) {
      setRunningTurns(info.running_turns.length);
    }
    if (info.started_at && info.started_at !== previousRef.current) {
      complete();
      return;
    }
    if (info.restart && info.restart.state === "idle") {
      /* Another tab aborted the drain; nothing is pending here. */
      reset();
    }
  }, [complete, fail, reset]);

  useEffect(() => {
    if (phase !== "waiting") {
      return undefined;
    }
    const tick = setInterval(() => setNow(Date.now()), 1000);
    const poll = setInterval(() => {
      void pollDrain();
    }, DRAIN_POLL_MS);
    return () => {
      clearInterval(tick);
      clearInterval(poll);
    };
  }, [phase, pollDrain]);

  const start = useCallback(
    async (previousStartedAt: number) => {
      doneRef.current = false;
      previousRef.current = Number(previousStartedAt) || 0;
      setBusy(true);
      setError("");
      setDetails([]);
      try {
        const outcome = await restartDaemon(false);
        if (outcome.restart === "draining") {
          const deadline =
            typeof outcome.deadline === "number" && outcome.deadline > 0
              ? outcome.deadline
              : Date.now() + DEFAULT_DRAIN_MS;
          deadlineRef.current = deadline;
          setDeadline(deadline);
          setRunningTurns(Number(outcome.running_turns) || 0);
          setNow(Date.now());
          setPhase("waiting");
          return;
        }
        setPhase("restarting");
        await waitForDaemonRestart(previousRef.current);
        complete();
      } catch (failure) {
        fail(failure);
      } finally {
        setBusy(false);
      }
    },
    [complete, fail]
  );

  const force = useCallback(async () => {
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      await restartDaemon(true);
      setPhase("restarting");
      await waitForDaemonRestart(previousRef.current);
      complete();
    } catch (failure) {
      fail(failure);
    } finally {
      setBusy(false);
    }
  }, [complete, fail]);

  const cancel = useCallback(async () => {
    if (phase !== "waiting") {
      reset();
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      await cancelDaemonRestart();
      reset();
    } catch (failure) {
      /* A 409 invalid_state means the restart already started or is not
         pending; the error text tells the owner. */
      setFailure(failure);
    } finally {
      setBusy(false);
    }
  }, [phase, reset, setFailure]);

  const secondsLeft =
    deadline > 0 ? Math.max(0, Math.ceil((deadline - now) / 1000)) : 0;

  return {
    phase,
    busy,
    runningTurns,
    secondsLeft,
    error,
    details,
    start,
    force,
    cancel,
  };
}

function RestartError({ message, details }: { message: string; details: unknown[] }) {
  return (
    <div className="alert danger" role="alert">
      <Ic name="circle-alert" size={15} />
      <div className="grow">
        <p className="settings-alert-msg">{message}</p>
        <ErrorDetails details={details} />
      </div>
    </div>
  );
}

export interface RestartDaemonDialogProps {
  controller: DaemonRestartController;
  onClose: () => void;
  /* The Telegram connect step starts the restart itself, so it shows the
     dialog only while a restart is waiting. */
  confirmFirst?: boolean;
}

export function RestartDaemonDialog({
  controller,
  onClose,
  confirmFirst = true,
}: RestartDaemonDialogProps) {
  const [info, setInfo] = useState<DaemonInfo | null>(null);
  const [loading, setLoading] = useState(confirmFirst);
  const [loadError, setLoadError] = useState("");
  const [loadDetails, setLoadDetails] = useState<unknown[]>([]);

  useEffect(() => {
    if (!confirmFirst) {
      return undefined;
    }
    let alive = true;
    daemonInfo()
      .then((value) => {
        if (!alive) {
          return;
        }
        setInfo(value);
        setLoading(false);
      })
      .catch((failure: unknown) => {
        if (!alive) {
          return;
        }
        setLoadError(errorMessage(failure));
        setLoadDetails(failure instanceof ApiError ? failure.details : []);
        setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [confirmFirst]);

  if (!confirmFirst && controller.phase === "idle") {
    return null;
  }

  if (controller.phase !== "idle") {
    const waiting = controller.phase === "waiting";
    return (
      <Modal
        title="Restart daemon?"
        dismissible={false}
        onClose={onClose}
        footer={
          <>
            <Button
              variant="secondary"
              disabled={controller.busy || !waiting}
              onClick={() => {
                void controller.cancel();
              }}
            >
              Cancel
            </Button>
            <span className="sp" />
            <Button
              variant="primary"
              disabled={controller.busy || !waiting}
              loading={controller.busy}
              onClick={() => {
                void controller.force();
              }}
            >
              Restart now
            </Button>
          </>
        }
      >
        <div className="settings-dialog-form">
          <p className="hint">
            {waiting
              ? "lana is waiting for running turns to finish before it restarts."
              : "Restarting. Waiting for the daemon to come back."}
          </p>
          <p
            className="settings-status"
            role="status"
            aria-live="polite"
            aria-atomic="true"
          >
            {waiting
              ? "Waiting for " +
                controller.runningTurns +
                " running turn" +
                (controller.runningTurns === 1 ? "" : "s") +
                " (" +
                controller.secondsLeft +
                " s)"
              : "Restarting…"}
          </p>
          {controller.error ? (
            <RestartError
              message={controller.error}
              details={controller.details}
            />
          ) : null}
        </div>
      </Modal>
    );
  }

  const turns = info && Array.isArray(info.running_turns) ? info.running_turns : [];
  const supported = !info || info.restart_supported;

  return (
    <Modal
      title="Restart daemon?"
      dismissible={!controller.busy}
      onClose={onClose}
      footer={
        <>
          <span className="sp" />
          <Button
            variant="secondary"
            disabled={controller.busy}
            onClick={onClose}
          >
            Cancel
          </Button>
          <Button
            variant="primary"
            disabled={controller.busy || loading || !info || !supported}
            loading={controller.busy}
            onClick={() => {
              void controller.start(info ? info.started_at : 0);
            }}
          >
            {controller.busy ? "Restarting…" : "Restart daemon"}
          </Button>
        </>
      }
    >
      <div className="settings-dialog-form">
        <p className="hint">
          The daemon restarts in place. Every running turn ends and this page
          reconnects when the daemon is back.
        </p>
        {loading ? (
          <div className="settings-dialog-loading" aria-busy="true">
            <span className="sr">Checking the daemon…</span>
            <Skeleton width="60%" height={12} />
          </div>
        ) : null}
        {loadError ? (
          <RestartError message={loadError} details={loadDetails} />
        ) : null}
        {info ? (
          <section
            className="settings-running-turns"
            aria-labelledby="settings-running-turns-title"
          >
            <h3 id="settings-running-turns-title">Running turns</h3>
            {turns.length > 0 ? (
              <ul className="settings-turn-list">
                {turns.map((turn, index) => (
                  <li
                    key={(turn && turn.session_id) || index}
                    className="settings-turn-item"
                  >
                    <span className="settings-turn-title">
                      {(turn && turn.title) || "Untitled session"}
                    </span>
                    <span className="settings-turn-agent">
                      {(turn && turn.agent) || "unknown agent"}
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="hint">No turns are running.</p>
            )}
          </section>
        ) : null}
        {info && !supported ? (
          <p className="settings-warning-text">
            Restarting from the browser is not supported on this platform.
          </p>
        ) : null}
        {controller.error ? (
          <RestartError
            message={controller.error}
            details={controller.details}
          />
        ) : null}
      </div>
    </Modal>
  );
}
