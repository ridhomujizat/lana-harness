import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { errorMessage, isErrorCode, setCSRF } from "../api/client";
import { authState, claim } from "../api/auth";
import { useAnnounce } from "../ui/ui";
import { SetupShell } from "./setup";
import { PasswordField } from "./login";

/* Create the password from the sign-in link (REQ-WLG-002, mockup 1). The link
   puts the one-time code in the hash (#/welcome?code=…) so it never reaches
   the server log. An unknown, used, or old code shows the expired screen. */

const MIN_PASSWORD = 8;
const SHORT_PASSWORD_TEXT = "Use at least 8 characters.";
const MISMATCH_TEXT = "The two passwords do not match.";

export interface WelcomePageProps {
  code?: string;
  onLoggedIn: (csrf: string) => void;
}

export function WelcomePage({ code, onLoggedIn }: WelcomePageProps) {
  const announce = useAnnounce();
  const [expired, setExpired] = useState(!code);
  const [passwordSet, setPasswordSet] = useState(false);
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [passwordError, setPasswordError] = useState("");
  const [confirmError, setConfirmError] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!code) {
      return undefined;
    }
    let alive = true;
    authState()
      .then((state) => {
        if (alive) {
          setPasswordSet(Boolean(state && state.password_set));
        }
      })
      .catch(() => {
        /* Stay on the create screen; the claim call reports real failures. */
      });
    return () => {
      alive = false;
    };
  }, [code]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy || !code) {
      return;
    }
    const nextPasswordError =
      password.length < MIN_PASSWORD ? SHORT_PASSWORD_TEXT : "";
    const nextConfirmError = password !== confirm ? MISMATCH_TEXT : "";
    setPasswordError(nextPasswordError);
    setConfirmError(nextConfirmError);
    if (nextPasswordError || nextConfirmError) {
      announce("Check the password fields and try again.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const session = await claim(code, password);
      setCSRF(session.csrf);
      announce(passwordSet ? "New password set." : "Password created.");
      onLoggedIn(session.csrf);
    } catch (failure) {
      if (isErrorCode(failure, "link_expired")) {
        setExpired(true);
        announce("This link has expired.");
        return;
      }
      const message = isErrorCode(failure, "invalid_request")
        ? SHORT_PASSWORD_TEXT
        : errorMessage(failure);
      setError(message);
      announce("Could not set the password. " + message);
    } finally {
      setBusy(false);
    }
  }

  if (expired) {
    return (
      <SetupShell step="unlock" variant="door">
        <div className="setup-heading">
          <h1 className="setup-title">This link has expired</h1>
          <p className="setup-lede">Restart lana to get a new one.</p>
        </div>
      </SetupShell>
    );
  }

  const title = passwordSet ? "Set a new password" : "Create your password";
  const action = passwordSet ? "Set new password" : "Create password";

  return (
    <SetupShell step="unlock" variant={passwordSet ? "door" : "steps"}>
      <div className="setup-heading">
        <h1 className="setup-title">{title}</h1>
        <p className="setup-lede">
          You will use it to open lana on this computer.
        </p>
      </div>

      <form className="setup-form" onSubmit={submit}>
        <PasswordField
          id="welcome-password"
          label="Password"
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
          id="welcome-confirm"
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
            {busy ? "Saving…" : action}
          </button>
        </div>
      </form>
    </SetupShell>
  );
}
