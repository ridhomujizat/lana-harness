import { createContext, useCallback, useContext, useEffect, useState } from "react";
import type { FormEvent } from "react";
import { errorMessage, isErrorCode } from "../api/client";
import { getAuthSession } from "../api/auth";
import type { AuthSession } from "../api/auth";
import {
  controlUnlocked,
  lockControl,
  sendControlCode,
  unlockControl,
} from "../api/remote";
import { Ic } from "../ui/icons";
import { Button, Field, Input, Modal } from "../ui/gr8r";

/* Workbench in a remote browser (spec 57, REQ-RA-085): panes attach in view
   mode until the owner unlocks control with the password or a Telegram code.
   Local browsers are not affected. */

/* The attach mode of every pane on the page, with the 4403 callback. Local
   browsers get control. */
export interface PaneAccess {
  mode: "control" | "view";
  onControlLost?: () => void;
}

export const PaneAccessContext = createContext<PaneAccess>({ mode: "control" });

export function usePaneAccess(): PaneAccess {
  return useContext(PaneAccessContext);
}

export interface RemoteControl {
  remote: boolean;
  /* canControl: panes may attach in control mode. */
  canControl: boolean;
  session: AuthSession | null;
  now: number;
  refresh: () => void;
  setControlUntil: (until: number) => void;
}

export function useRemoteControl(): RemoteControl {
  const [session, setSession] = useState<AuthSession | null>(null);
  const [now, setNow] = useState(() => Date.now());

  const refresh = useCallback(() => {
    getAuthSession()
      .then((value) => {
        setSession(value);
        setNow(Date.now());
      })
      .catch(() => {
        /* Keep the last known state; the app's auth handler covers 401. */
      });
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const remote = Boolean(session && session.remote);
  const until = session ? session.control_until || 0 : 0;

  /* Tick while control is unlocked so the countdown moves and the panes
     switch back to view mode when it ends. */
  useEffect(() => {
    if (!remote || !until) {
      return;
    }
    const timer = window.setInterval(() => {
      const current = Date.now();
      setNow(current);
      if (current >= until) {
        refresh();
      }
    }, 15000);
    const end = window.setTimeout(
      () => setNow(Date.now()),
      Math.max(0, until - Date.now()) + 50
    );
    return () => {
      window.clearInterval(timer);
      window.clearTimeout(end);
    };
  }, [remote, until, refresh]);

  const setControlUntil = useCallback((value: number) => {
    setSession((current) =>
      current ? { ...current, control_until: value } : current
    );
    setNow(Date.now());
  }, []);

  return {
    remote,
    canControl:
      !remote ||
      controlUnlocked(session ? session.terminal : undefined, until, now),
    session,
    now,
    refresh,
    setControlUntil,
  };
}

function minutesLeft(until: number, now: number): number {
  return Math.max(1, Math.ceil((until - now) / 60000));
}

export function RemoteControlBar({ control }: { control: RemoteControl }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  if (!control.remote || !control.session) {
    return null;
  }
  const session = control.session;
  if (session.terminal === "full") {
    return null;
  }
  const until = session.control_until || 0;
  const unlocked = control.canControl;

  async function lock() {
    setBusy(true);
    try {
      await lockControl();
      control.setControlUntil(0);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className={"wb-remote-bar" + (unlocked ? " on" : "")} role="status">
        <span className="wb-remote-ic" aria-hidden="true">
          <Ic name={unlocked ? "lock-open" : "lock"} size={15} />
        </span>
        <span className="grow">
          <span className="t">
            {unlocked
              ? "Control · " + minutesLeft(until, control.now) + " min left"
              : "Remote · view only"}
          </span>
          <span className="d">
            {unlocked
              ? "Typing is on for this phone. It locks again by itself."
              : session.terminal === "view"
                ? "Remote access is set to view only on the Mac."
                : "Typing is locked on this phone"}
          </span>
        </span>
        {session.terminal === "confirm" ? (
          unlocked ? (
            <Button variant="secondary" size="sm" disabled={busy} onClick={() => void lock()}>
              Lock
            </Button>
          ) : (
            <Button variant="primary" size="sm" onClick={() => setOpen(true)}>
              Unlock control
            </Button>
          )
        ) : null}
      </div>
      {open ? (
        <UnlockSheet
          control={control}
          onClose={() => setOpen(false)}
        />
      ) : null}
    </>
  );
}

function UnlockSheet({
  control,
  onClose,
}: {
  control: RemoteControl;
  onClose: () => void;
}) {
  const session = control.session;
  const [method, setMethod] = useState<"password" | "code">("password");
  const [value, setValue] = useState("");
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy || !value.trim()) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await unlockControl(
        method === "password" ? { password: value } : { code: value.trim() }
      );
      control.setControlUntil(result.control_until);
      onClose();
    } catch (failure: unknown) {
      setValue("");
      if (isErrorCode(failure, "invalid_password")) {
        setError("That password is not right.");
      } else if (isErrorCode(failure, "invalid_code")) {
        setError("That code is not right or has expired.");
      } else if (isErrorCode(failure, "too_many_attempts")) {
        setError("Too many tries. Wait a few minutes.");
      } else {
        setError(errorMessage(failure));
      }
    } finally {
      setBusy(false);
    }
  }

  async function sendCode() {
    setBusy(true);
    setError("");
    try {
      const result = await sendControlCode();
      setMethod("code");
      setValue("");
      setNote(
        "Code sent to Telegram" +
          (result.sent_to > 1 ? " (" + result.sent_to + " chats)" : "") +
          ". It works for 5 minutes."
      );
    } catch (failure: unknown) {
      setError(
        isErrorCode(failure, "telegram_unavailable")
          ? "Telegram is not connected."
          : isErrorCode(failure, "too_many_attempts")
            ? "A code was sent less than a minute ago."
            : errorMessage(failure)
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title="Unlock control"
      subtitle="Type in Workbench terminals from this phone. Control locks again by itself."
      size="sm"
      onClose={onClose}
      className="wb-unlock"
    >
      <form className="wb-unlock-form" onSubmit={submit}>
        {method === "password" ? (
          <Field label="Lana password" htmlFor="wb-unlock-password" error={error || undefined}>
            <Input
              id="wb-unlock-password"
              type="password"
              autoComplete="current-password"
              data-autofocus
              value={value}
              disabled={busy}
              onChange={(event) => {
                setValue(event.target.value);
                setError("");
              }}
            />
          </Field>
        ) : (
          <Field
            label="Code from Telegram"
            htmlFor="wb-unlock-code"
            hint={note || undefined}
            error={error || undefined}
          >
            <Input
              id="wb-unlock-code"
              className="mono"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              data-autofocus
              value={value}
              disabled={busy}
              onChange={(event) => {
                setValue(event.target.value.replace(/\D/g, ""));
                setError("");
              }}
            />
          </Field>
        )}
        <Button type="submit" variant="primary" block loading={busy} disabled={busy || !value}>
          Unlock control
        </Button>
        {session && session.telegram_code ? (
          method === "password" ? (
            <Button variant="secondary" block disabled={busy} onClick={() => void sendCode()}>
              Send code to Telegram
            </Button>
          ) : (
            <Button
              variant="ghost"
              block
              disabled={busy}
              onClick={() => {
                setMethod("password");
                setValue("");
                setError("");
              }}
            >
              Use the password instead
            </Button>
          )
        ) : null}
        {session ? (
          <p className="wb-unlock-meta">
            <Ic name="globe" size={13} />
            Signed in as{" "}
            {[session.login, session.device].filter(Boolean).join(" on ") ||
              "a remote browser"}
          </p>
        ) : null}
      </form>
    </Modal>
  );
}
