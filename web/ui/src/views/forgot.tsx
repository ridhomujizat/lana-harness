import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { errorMessage, isErrorCode, setCSRF } from "../api/client";
import { authState, resetRestart, resetTelegram, resetVerify } from "../api/auth";
import { useAnnounce } from "../ui/ui";
import { SetupShell } from "./setup";
import { PasswordField } from "./login";

/* Forgot password (REQ-WLG-004, REQ-WLG-005, mockups 3 and 4). The owner picks
   a Telegram code or a restart sign-in link, then sets a new password. The
   Telegram row only appears while GET /v1/auth/state reports it is available. */

const MIN_PASSWORD = 8;
const SHORT_PASSWORD_TEXT = "Use at least 8 characters.";
const MISMATCH_TEXT = "The two passwords do not match.";
const INVALID_CODE_TEXT = "That code is not right.";
const CODE_EXPIRED_TEXT = "Ask for a new code.";
const TELEGRAM_UNAVAILABLE_TEXT = "Telegram reset is not available.";
const TOO_MANY_ATTEMPTS_TEXT =
  "Too many tries. Wait 5 minutes or reset your password.";

type ForgotMode = "options" | "code" | "restart";

function telegramErrorText(error: unknown): string {
  if (isErrorCode(error, "too_many_attempts")) {
    return TOO_MANY_ATTEMPTS_TEXT;
  }
  if (isErrorCode(error, "telegram_unavailable")) {
    return TELEGRAM_UNAVAILABLE_TEXT;
  }
  return errorMessage(error);
}

export interface ForgotPageProps {
  onLoggedIn: (csrf: string) => void;
}

export function ForgotPage({ onLoggedIn }: ForgotPageProps) {
  const announce = useAnnounce();
  const [telegramReset, setTelegramReset] = useState(false);
  const [mode, setMode] = useState<ForgotMode>("options");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [code, setCode] = useState("");
  const [codeError, setCodeError] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [passwordError, setPasswordError] = useState("");
  const [confirmError, setConfirmError] = useState("");

  useEffect(() => {
    let alive = true;
    authState()
      .then((state) => {
        if (alive) {
          setTelegramReset(Boolean(state && state.telegram_reset));
        }
      })
      .catch(() => {
        /* Keep the restart option; Telegram stays hidden. */
      });
    return () => {
      alive = false;
    };
  }, []);

  async function sendCode() {
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      await resetTelegram();
      setMode("code");
      announce("Code sent to Telegram.");
    } catch (failure) {
      const message = telegramErrorText(failure);
      setError(message);
      announce("Could not send the code. " + message);
    } finally {
      setBusy(false);
    }
  }

  async function useRestart() {
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      await resetRestart();
      setMode("restart");
      announce("Restart requested. Start lana again to set a new password.");
    } catch (failure) {
      const message = errorMessage(failure);
      setError(message);
      announce("Could not request a restart. " + message);
    } finally {
      setBusy(false);
    }
  }

  async function submitCode(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const trimmedCode = code.trim();
    const nextCodeError = trimmedCode ? "" : "Enter the code.";
    const nextPasswordError =
      password.length < MIN_PASSWORD ? SHORT_PASSWORD_TEXT : "";
    const nextConfirmError = password !== confirm ? MISMATCH_TEXT : "";
    setCodeError(nextCodeError);
    setPasswordError(nextPasswordError);
    setConfirmError(nextConfirmError);
    if (nextCodeError || nextPasswordError || nextConfirmError) {
      announce("Check the fields and try again.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const session = await resetVerify(trimmedCode, password);
      setCSRF(session.csrf);
      announce("New password set.");
      onLoggedIn(session.csrf);
    } catch (failure) {
      if (isErrorCode(failure, "invalid_code")) {
        setCodeError(INVALID_CODE_TEXT);
      } else if (isErrorCode(failure, "code_expired")) {
        setError(CODE_EXPIRED_TEXT);
      } else {
        setError(errorMessage(failure));
      }
      announce("Could not set the password.");
    } finally {
      setBusy(false);
    }
  }

  if (mode === "restart") {
    return (
      <SetupShell step="unlock" variant="door">
        <div className="setup-heading">
          <h1 className="setup-title">Restart lana</h1>
          <p className="setup-lede">
            Close lana and start it again. A browser window opens where you can
            set a new password.
          </p>
        </div>
        <p className="after-form">
          <a href="#/login">Back to log in</a>
        </p>
      </SetupShell>
    );
  }

  if (mode === "code") {
    return (
      <SetupShell step="unlock" variant="door">
        <div className="setup-heading">
          <h1 className="setup-title">Enter the code</h1>
          <p className="setup-lede">
            We sent it to Telegram. It works for 10 minutes.
          </p>
        </div>

        <form className="setup-form" onSubmit={submitCode}>
          <div className="setup-field code-field">
            <label className="setup-label" htmlFor="reset-code">
              Code
            </label>
            <input
              id="reset-code"
              type="text"
              inputMode="numeric"
              autoComplete="one-time-code"
              spellCheck={false}
              value={code}
              disabled={busy}
              aria-invalid={codeError ? "true" : undefined}
              aria-describedby={codeError ? "reset-code-error" : undefined}
              onChange={(event) => {
                setCode(event.target.value);
                setCodeError("");
              }}
            />
            {codeError ? (
              <p id="reset-code-error" className="setup-field-error" role="alert">
                {codeError}
              </p>
            ) : null}
          </div>
          <PasswordField
            id="reset-password"
            label="New password"
            value={password}
            autoComplete="new-password"
            hint="At least 8 characters."
            error={passwordError}
            disabled={busy}
            onChange={(value) => {
              setPassword(value);
              setPasswordError("");
            }}
          />
          <PasswordField
            id="reset-confirm"
            label="Type it again"
            value={confirm}
            autoComplete="new-password"
            error={confirmError}
            disabled={busy}
            onChange={(value) => {
              setConfirm(value);
              setConfirmError("");
            }}
          />
          {error ? (
            <p className="setup-field-error" role="alert">
              {error}
            </p>
          ) : null}
          <div className="setup-actions">
            <button
              type="submit"
              className="button-ui button-ui-primary"
              disabled={busy}
            >
              {busy ? "Saving…" : "Set new password"}
            </button>
          </div>
        </form>

        <p className="after-form">
          No message?{" "}
          <button
            type="button"
            className="link-button"
            disabled={busy}
            onClick={sendCode}
          >
            Send a new code
          </button>
        </p>
      </SetupShell>
    );
  }

  return (
    <SetupShell step="unlock" variant="door">
      <div className="setup-heading">
        <h1 className="setup-title">Reset your password</h1>
        <p className="setup-lede">Choose how you want to prove it is you.</p>
      </div>

      <ul className="reset-list">
        {telegramReset ? (
          <li>
            <div>
              <h2>Send a code to Telegram</h2>
              <p>We send a 6-digit code to your private chat on Telegram.</p>
            </div>
            <button
              type="button"
              className="button-ui button-ui-primary"
              disabled={busy}
              onClick={sendCode}
            >
              Send code
            </button>
          </li>
        ) : null}
        <li>
          <div>
            <h2>Restart lana to get a sign-in link</h2>
            <p>Close lana and start it again to get a sign-in link.</p>
          </div>
          <button
            type="button"
            className="button-ui button-ui-secondary"
            disabled={busy}
            onClick={useRestart}
          >
            Use restart
          </button>
        </li>
      </ul>

      {error ? (
        <p className="setup-field-error" role="alert">
          {error}
        </p>
      ) : null}

      <p className="after-form">
        <a href="#/login">Back to log in</a>
      </p>
    </SetupShell>
  );
}
