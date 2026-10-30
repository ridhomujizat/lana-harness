import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { errorMessage, isErrorCode, setCSRF } from "../api/client";
import { authState, login } from "../api/auth";
import { Icon, Loading, useAnnounce } from "../ui/ui";
import { SetupShell } from "./setup";

/* Daily login (REQ-WLG-003, mockup 2). The owner types the password they set
   during setup; a wrong one answers invalid_password, too many tries answer
   too_many_attempts. The legacy token field is gone (REQ-WLG-008). */

const INVALID_PASSWORD_TEXT = "That password is not right.";
const TOO_MANY_ATTEMPTS_TEXT =
  "Too many tries. Wait 5 minutes or reset your password.";

function loginErrorText(error: unknown): string {
  if (isErrorCode(error, "too_many_attempts")) {
    return TOO_MANY_ATTEMPTS_TEXT;
  }
  if (isErrorCode(error, "invalid_password")) {
    return INVALID_PASSWORD_TEXT;
  }
  return errorMessage(error);
}

export interface PasswordFieldProps {
  id: string;
  label: string;
  value: string;
  autoComplete: string;
  disabled?: boolean;
  hint?: string;
  error?: string;
  onChange: (value: string) => void;
}

/* A labelled password input with the inline eye icon toggle. The forgot,
   welcome, and code screens share it. */
export function PasswordField({
  id,
  label,
  value,
  autoComplete,
  disabled = false,
  hint = "",
  error = "",
  onChange,
}: PasswordFieldProps) {
  const [shown, setShown] = useState(false);
  const hintId = hint ? id + "-hint" : "";
  const errorId = error ? id + "-error" : "";
  const describedBy = [hintId, errorId].filter(Boolean).join(" ") || undefined;
  return (
    <div className="setup-field">
      <label className="setup-label" htmlFor={id}>
        {label}
      </label>
      <div className="pw-wrap">
        <input
          id={id}
          type={shown ? "text" : "password"}
          autoComplete={autoComplete}
          spellCheck={false}
          value={value}
          disabled={disabled}
          aria-invalid={error ? "true" : undefined}
          aria-describedby={describedBy}
          onChange={(event) => onChange(event.target.value)}
        />
        <button
          type="button"
          className="pw-toggle"
          aria-label={shown ? "Hide password" : "Show password"}
          aria-pressed={shown}
          disabled={disabled}
          onClick={() => setShown((current) => !current)}
        >
          <Icon name={shown ? "eye-off" : "eye"} />
        </button>
      </div>
      {hint ? (
        <p id={hintId} className="setup-hint">
          {hint}
        </p>
      ) : null}
      {error ? (
        <p id={errorId} className="setup-field-error" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}

export interface LoginPageProps {
  onLoggedIn: (csrf: string) => void;
}

export function LoginPage({ onLoggedIn }: LoginPageProps) {
  const announce = useAnnounce();
  const [passwordSet, setPasswordSet] = useState<boolean | null>(null);
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let alive = true;
    authState()
      .then((state) => {
        if (alive) {
          setPasswordSet(Boolean(state && state.password_set));
        }
      })
      .catch(() => {
        /* Keep the form: the login call itself reports the real failure. */
        if (alive) {
          setPasswordSet(true);
        }
      });
    return () => {
      alive = false;
    };
  }, []);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      const session = await login({ password });
      setCSRF(session.csrf);
      announce("Signed in.");
      onLoggedIn(session.csrf);
    } catch (failure) {
      const message = loginErrorText(failure);
      setError(message);
      setPassword("");
      announce("Log in failed. " + message);
    } finally {
      setBusy(false);
    }
  }

  const noPassword = passwordSet === false;

  return (
    <SetupShell step="unlock" variant="door">
      <div className="setup-heading">
        <h1 className="setup-title">
          {noPassword ? "Set up your password" : "Log in"}
        </h1>
        <p className="setup-lede">
          {noPassword
            ? "lana opened a sign-in link in a new browser tab when it started. Switch to that tab, or restart lana to open a new one."
            : "Enter the password you made when you set up lana."}
        </p>
      </div>

      {noPassword ? null : (
        <form className="setup-form" onSubmit={submit}>
          <PasswordField
            id="login-password"
            label="Password"
            value={password}
            autoComplete="current-password"
            error={error}
            disabled={busy}
            onChange={(value) => {
              setPassword(value);
              setError("");
            }}
          />
          <div className="setup-actions">
            <button
              type="submit"
              className="button-ui button-ui-primary"
              disabled={busy}
            >
              {busy ? "Logging in…" : "Log in"}
            </button>
          </div>
        </form>
      )}

      {noPassword ? null : (
        <p className="after-form">
          Forgot it? <a href="#/forgot">Reset your password</a>
        </p>
      )}
    </SetupShell>
  );
}

export function LoginLoading() {
  return (
    <main id="main" className="page page-narrow">
      <h1>lana dashboard</h1>
      <Loading label="Checking your browser session." />
    </main>
  );
}
