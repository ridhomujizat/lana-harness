import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { ApiError, errorMessage, setCSRF } from "../api/client";
import { authSession, login } from "../api/auth";
import { Loading, useAnnounce } from "../ui/ui";
import { SetupShell } from "./setup";
import type { AuthSession } from "../api/auth";

/* Step 1 of the setup wizard (REQ-WEB-032, REQ-WEB-005). The login rules are
   unchanged: the owner token is posted once, the browser session is read back,
   and its CSRF value is handed to the shared API client. */

export interface LoginPageProps {
  onLoggedIn: (session: AuthSession) => void;
}

export function LoginPage({ onLoggedIn }: LoginPageProps) {
  const announce = useAnnounce();
  const tokenRef = useRef<HTMLInputElement>(null);
  const [token, setToken] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [checking, setChecking] = useState(false);

  useEffect(() => {
    if (tokenRef.current) {
      tokenRef.current.focus();
    }
  }, []);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    setBusy(true);
    setMessage("");
    try {
      await login(token);
      const session = await authSession();
      setCSRF(session.csrf);
      announce("Signed in.");
      onLoggedIn(session);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        setMessage(
          "The owner token is incorrect. Copy it from the daemon start output and try again."
        );
        announce("Log in failed: the owner token is incorrect.");
      } else {
        setMessage(errorMessage(error));
        announce("Log in failed. " + errorMessage(error));
      }
      setToken("");
    } finally {
      setBusy(false);
    }
  }

  async function checkExisting() {
    setChecking(true);
    setMessage("");
    try {
      const session = await authSession();
      setCSRF(session.csrf);
      onLoggedIn(session);
    } catch {
      setMessage("No active browser session. Log in with the owner token.");
    } finally {
      setChecking(false);
    }
  }

  const describedBy = [
    "unlock-token-hint",
    message ? "unlock-token-error" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <SetupShell step="unlock">
      <div className="setup-heading">
        <span className="setup-eyebrow">Step 1 of 5</span>
        <h1 className="setup-title">Welcome to lana</h1>
        <p className="setup-lede">
          The daemon is running. Paste the owner token it printed in the terminal
          to unlock this dashboard.
        </p>
      </div>

      <form className="setup-form" onSubmit={submit}>
        <div className="setup-field">
          <label className="setup-label" htmlFor="unlock-token">
            Owner token
          </label>
          <input
            ref={tokenRef}
            id="unlock-token"
            type="password"
            name="token"
            autoComplete="current-password"
            spellCheck={false}
            required
            value={token}
            aria-required="true"
            aria-invalid={message ? "true" : undefined}
            aria-describedby={describedBy}
            onChange={(event) => setToken(event.target.value)}
          />
          <p id="unlock-token-hint" className="setup-hint">
            The token stays in this browser as a session cookie.
          </p>
          {message ? (
            <p id="unlock-token-error" className="setup-field-error" role="alert">
              {message}
            </p>
          ) : null}
        </div>

        <div className="setup-token-help">
          <span className="setup-token-help-title">Where is the token?</span>
          <p className="setup-token-help-text">
            Run lana daemon. The first lines of the log show:
          </p>
          <code>owner token: lana_tok_7Q2k…</code>
        </div>

        <div className="setup-actions">
          <button
            type="submit"
            className="button-ui button-ui-primary"
            disabled={busy || checking}
          >
            {busy ? "Unlocking…" : "Unlock dashboard"}
          </button>
        </div>

        <button
          type="button"
          className="link-button setup-check-session"
          disabled={busy || checking}
          onClick={checkExisting}
        >
          {checking ? "Checking…" : "Check existing session"}
        </button>
      </form>
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
