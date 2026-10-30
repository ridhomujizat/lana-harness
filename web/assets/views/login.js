import { html, useEffect, useRef, useState } from "/assets/vendor/preact-htm.js";
import { api, setCSRF, ApiError, errorMessage } from "/assets/api.js";
import { useAnnounce, Loading } from "/assets/ui.js";
import { SetupShell } from "/assets/views/setup.js";

/* Step 1 of the setup wizard (REQ-WEB-032, REQ-WEB-005). The login rules are
   unchanged: the owner token is posted once, the browser session is read back,
   and its CSRF value is handed to /assets/api.js. */

export function LoginPage({ onLoggedIn }) {
  const announce = useAnnounce();
  const tokenRef = useRef(null);
  const [token, setToken] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [checking, setChecking] = useState(false);

  useEffect(() => {
    if (tokenRef.current) {
      tokenRef.current.focus();
    }
  }, []);

  async function submit(event) {
    event.preventDefault();
    if (busy) {
      return;
    }
    setBusy(true);
    setMessage("");
    try {
      await api.login(token);
      const session = await api.authSession();
      setCSRF(session.csrf);
      announce("Signed in.");
      onLoggedIn(session);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        setMessage("The owner token is incorrect. Copy it from the daemon start output and try again.");
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
      const session = await api.authSession();
      setCSRF(session.csrf);
      onLoggedIn(session);
    } catch (error) {
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

  return html`<${SetupShell} step="unlock">
    <div class="setup-heading">
      <span class="setup-eyebrow">Step 1 of 5</span>
      <h1 class="setup-title">Welcome to lana</h1>
      <p class="setup-lede">
        The daemon is running. Paste the owner token it printed in the terminal
        to unlock this dashboard.
      </p>
    </div>

    <form class="setup-form" onSubmit=${submit}>
      <div class="setup-field">
        <label class="setup-label" for="unlock-token">Owner token</label>
        <input
          ref=${tokenRef}
          id="unlock-token"
          type="password"
          name="token"
          autocomplete="current-password"
          spellcheck=${false}
          required
          value=${token}
          aria-required="true"
          aria-invalid=${message ? "true" : undefined}
          aria-describedby=${describedBy}
          onInput=${(event) => setToken(event.target.value)}
        />
        <p id="unlock-token-hint" class="setup-hint">
          The token stays in this browser as a session cookie.
        </p>
        ${message
          ? html`<p id="unlock-token-error" class="setup-field-error" role="alert">
              ${message}
            </p>`
          : null}
      </div>

      <div class="setup-token-help">
        <span class="setup-token-help-title">Where is the token?</span>
        <p class="setup-token-help-text">
          Run lana daemon. The first lines of the log show:
        </p>
        <code>owner token: lana_tok_7Q2k…</code>
      </div>

      <div class="setup-actions">
        <button
          type="submit"
          class="button-ui button-ui-primary"
          disabled=${busy || checking}
        >${busy ? "Unlocking…" : "Unlock dashboard"}</button>
      </div>

      <button
        type="button"
        class="link-button setup-check-session"
        disabled=${busy || checking}
        onClick=${checkExisting}
      >${checking ? "Checking…" : "Check existing session"}</button>
    </form>
  </${SetupShell}>`;
}

export function LoginLoading() {
  return html`<main id="main" class="page page-narrow">
    <h1>lana dashboard</h1>
    <${Loading} label="Checking your browser session." />
  </main>`;
}
