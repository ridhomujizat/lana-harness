import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { errorMessage, isErrorCode, setCSRF } from "../api/client";
import { authState, login } from "../api/auth";
import { useAnnounce } from "../ui/ui";
import { LanaMark } from "../ui/icons";
import { Button, Field, IconButton, Input } from "../ui/gr8r";
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
  return (
    <Field
      label={label}
      htmlFor={id}
      hint={hint || undefined}
      error={error || undefined}
    >
      <div className="pw-field">
        <Input
          id={id}
          className="input-lg"
          type={shown ? "text" : "password"}
          autoComplete={autoComplete}
          spellCheck={false}
          value={value}
          disabled={disabled}
          onChange={(event) => onChange(event.target.value)}
        />
        <IconButton
          size="sm"
          className="pw-eye"
          icon={shown ? "eye-off" : "eye"}
          label={shown ? "Hide password" : "Show password"}
          on={shown}
          disabled={disabled}
          onClick={() => setShown((current) => !current)}
        />
      </div>
    </Field>
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
      <h1>{noPassword ? "Set up your password" : "Log in"}</h1>
      <p className="sub">
        {noPassword
          ? "lana opened a sign-in link in a new browser tab when it started. Switch to that tab, or restart lana to open a new one."
          : "Enter the password you made when you set up lana."}
      </p>

      {noPassword ? null : (
        <form className="auth-form" onSubmit={submit}>
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
          <Button type="submit" variant="primary" size="lg" block loading={busy} disabled={busy}>
            Log in
          </Button>
        </form>
      )}

      {noPassword ? null : (
        <p className="auth-foot">
          Forgot it?{" "}
          <a className="link" href="#/forgot">
            Reset your password
          </a>
        </p>
      )}
    </SetupShell>
  );
}

export function LoginLoading() {
  return (
    <div className="auth">
      <div className="auth-c">
        <div className="auth-card">
          <span className="auth-mark">
            <LanaMark size={30} />
          </span>
          <h1>lana</h1>
          <p className="sub">Checking your browser session.</p>
          <div className="auth-loading" role="status" aria-live="polite">
            <span className="auth-spinner" aria-hidden="true"></span>
          </div>
        </div>
      </div>
    </div>
  );
}
