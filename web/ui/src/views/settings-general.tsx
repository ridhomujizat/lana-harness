/* eslint-disable react-hooks/set-state-in-effect -- the old Preact views loaded
   data on mount and synced form state in effects; this port keeps that shape. */
import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import { THEME_CHOICES, themeLabel } from "../app/theme";
import type { ThemeChoice } from "../app/theme";
import { ApiError, errorMessage, isErrorCode } from "../api/client";
import * as settingsApi from "../api/settings";
import { changePassword } from "../api/auth";
import type { BrowserSessionInfo } from "../api/auth";
import type { DaemonInfo } from "../api/daemon";
import type { GetConfigResponse } from "../api/settings";
import { ErrorNotice, Loading } from "../ui/ui";
import { PasswordField } from "./login";
import {
  SettingsPanel,
  SettingsRestartBanner,
  SettingsRestartDialog,
  SettingsTabs,
  markSettingsSaved,
} from "./settings-pages";

/* Settings > General (REQ-WEB-054). The theme tiles of REQ-WEB-031 stay here;
   the daemon, web dashboard, and about sections come from API-WEB-022,
   API-WEB-024, API-WEB-029, and API-WEB-013. "Change password" (REQ-WLG-007,
   API-WLG-007) sets a new password and signs out the other browsers. */

const MIN_PASSWORD = 8;
const SHORT_PASSWORD_TEXT = "Use at least 8 characters.";
const MISMATCH_TEXT = "The two passwords do not match.";
const INVALID_PASSWORD_TEXT = "That password is not right.";
const PASSWORD_CHANGED_TEXT =
  "Password changed. Other browsers were signed out.";

function changePasswordErrorText(error: unknown): string {
  if (isErrorCode(error, "invalid_password")) {
    return INVALID_PASSWORD_TEXT;
  }
  return errorMessage(error);
}

function failureInfo(failure: unknown): {
  message: string;
  details: unknown[];
  code: string;
} {
  return {
    message: errorMessage(failure),
    details: failure instanceof ApiError ? failure.details : [],
    code: failure instanceof ApiError ? failure.code : "",
  };
}

function formatUptime(milliseconds: number): string {
  const value = Number(milliseconds);
  if (!Number.isFinite(value) || value < 0) {
    return "up unknown";
  }
  const totalMinutes = Math.floor(value / 60000);
  if (totalMinutes < 1) {
    return "up just now";
  }
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (hours >= 24) {
    const days = Math.floor(hours / 24);
    return "up " + days + " d " + (hours % 24) + " h";
  }
  if (hours > 0) {
    return "up " + hours + " h " + minutes + " min";
  }
  return "up " + minutes + " min";
}

function browserLabel(count: number): string {
  const value = Number(count) || 0;
  return (
    value +
    (value === 1
      ? " browser signed in as owner"
      : " browsers signed in as owner")
  );
}

export interface SettingsGeneralPageProps {
  theme: ThemeChoice;
  onThemeChange: (choice: ThemeChoice) => void;
}

export function SettingsGeneralPage({
  theme,
  onThemeChange,
}: SettingsGeneralPageProps) {
  const [daemon, setDaemon] = useState<DaemonInfo | null>(null);
  const [config, setConfig] = useState<GetConfigResponse | null>(null);
  const [sessions, setSessions] = useState<BrowserSessionInfo[] | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [message, setMessage] = useState("");
  const [address, setAddress] = useState("");
  const [savedUrl, setSavedUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [showRestart, setShowRestart] = useState(false);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [passwordError, setPasswordError] = useState("");

  const load = useCallback(async (announceFailure: boolean) => {
    setState("loading");
    setError("");
    setDetails([]);
    const results = await Promise.allSettled([
      settingsApi.getDaemon(),
      settingsApi.getConfig(),
      settingsApi.listBrowserSessions(),
    ]);

    let failure: unknown = null;
    if (results[0].status === "fulfilled") {
      const value = results[0].value;
      setDaemon(value);
      setAddress(value && value.addr ? value.addr : "");
    } else {
      failure = results[0].reason;
    }
    if (results[1].status === "fulfilled") {
      setConfig(results[1].value);
    } else if (!failure) {
      failure = results[1].reason;
    }
    if (results[2].status === "fulfilled") {
      const value = results[2].value;
      setSessions(value && Array.isArray(value.sessions) ? value.sessions : []);
    } else {
      setSessions(null);
    }

    if (failure) {
      const failureState = failureInfo(failure);
      setError(failureState.message);
      setDetails(failureState.details);
      setState("error");
      if (announceFailure) {
        setMessage("Could not load the settings. " + failureState.message);
      }
    } else {
      setState("ready");
    }
  }, []);

  useEffect(() => {
    void load(false);
  }, [load]);

  const browserCount = sessions
    ? sessions.length
    : daemon
      ? Number(daemon.browsers_signed_in) || 0
      : 0;

  async function saveAddress(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const value = address.trim();
    if (!value) {
      setError("Enter the web address, for example 127.0.0.1:18300.");
      setDetails([]);
      setMessage("The address was not saved.");
      return;
    }
    if (!config || !config.version) {
      setError("config.toml is missing. Run lana init before changing the address.");
      setDetails([]);
      setMessage("The address was not saved.");
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    setMessage("Saving the web address…");
    try {
      const result = await settingsApi.saveWebAddress(value, config.version);
      setConfig((current) =>
        current
          ? {
              ...current,
              version: result.version,
              restart_required: result.restart_required,
            }
          : current
      );
      setAddress(result.addr || value);
      setSavedUrl(result.url || "");
      markSettingsSaved();
      setMessage("Address saved. It applies after a daemon restart.");
    } catch (failure: unknown) {
      const failureState = failureInfo(failure);
      if (failureState.code === "stale_version") {
        setError("config.toml changed since the page loaded. Reload before saving.");
        setMessage("The address was not saved: the config changed on disk.");
      } else {
        setError(failureState.message);
        setMessage("The address was not saved.");
      }
      setDetails(failureState.details);
    } finally {
      setBusy(false);
    }
  }

  async function signOutOtherBrowsers() {
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    setMessage("Signing out the other browsers…");
    try {
      const result = await settingsApi.revokeOtherBrowsers();
      const revoked = Number(result && result.revoked) || 0;
      setMessage(
        revoked === 0
          ? "No other browsers were signed in."
          : "Signed out " +
              revoked +
              " other " +
              (revoked === 1 ? "browser." : "browsers.")
      );
      const results = await Promise.allSettled([
        settingsApi.getDaemon(),
        settingsApi.listBrowserSessions(),
      ]);
      if (results[0].status === "fulfilled") {
        setDaemon(results[0].value);
      }
      if (results[1].status === "fulfilled") {
        const value = results[1].value;
        setSessions(
          value && Array.isArray(value.sessions) ? value.sessions : []
        );
      }
    } catch (failure: unknown) {
      const failureState = failureInfo(failure);
      setError(failureState.message);
      setDetails(failureState.details);
      setMessage("Could not sign out the other browsers.");
    } finally {
      setBusy(false);
    }
  }

  async function changePasswordSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    if (newPassword.length < MIN_PASSWORD) {
      setPasswordError(SHORT_PASSWORD_TEXT);
      return;
    }
    if (newPassword !== confirmPassword) {
      setPasswordError(MISMATCH_TEXT);
      return;
    }
    setPasswordError("");
    setBusy(true);
    setMessage("Changing the password…");
    try {
      await changePassword(currentPassword, newPassword);
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      setMessage(PASSWORD_CHANGED_TEXT);
    } catch (failure: unknown) {
      setPasswordError(changePasswordErrorText(failure));
      setMessage("The password was not changed.");
    } finally {
      setBusy(false);
    }
  }

  function handleRestarted() {
    setShowRestart(false);
    setMessage("Daemon restarted. Config and secret changes are now active.");
    void load(false);
  }

  const daemonState = daemon
    ? "Running"
    : state === "error"
      ? "Unavailable"
      : "Checking";
  const daemonMeta = daemon
    ? "pid " +
      daemon.pid +
      " · " +
      formatUptime(daemon.uptime_ms) +
      " · " +
      (daemon.home || "home unknown")
    : state === "error"
      ? "The daemon status could not be read."
      : "Loading daemon status…";

  return (
    <main id="main" className="page settings-page">
      <SettingsTabs active="general" />
      <SettingsPanel active="general">
        <SettingsRestartBanner
          restartRequired={Boolean(config && config.restart_required)}
          onRestarted={handleRestarted}
        />

        {state === "loading" && !daemon && !config ? (
          <Loading label="Loading settings…" />
        ) : null}
        {error ? <ErrorNotice message={error} details={details} /> : null}
        {state === "error" ? (
          <div className="form-actions">
            <button
              type="button"
              className="button-ui button-ui-secondary"
              disabled={busy}
              onClick={() => void load(true)}
            >
              Reload settings
            </button>
          </div>
        ) : null}
        <p
          className="settings-status"
          role="status"
          aria-live="polite"
          aria-atomic="true"
        >
          {message}
        </p>

        <section className="settings-row" aria-labelledby="settings-daemon-title">
          <div className="settings-row-label">
            <h2 id="settings-daemon-title">Daemon</h2>
            <p>lana daemon runs agents, channels and this dashboard.</p>
          </div>
          <div className="settings-row-controls">
            <div className="settings-daemon-card">
              <div className="settings-daemon-text">
                <div className="settings-daemon-title-row">
                  <h3>{daemonState}</h3>
                  {daemon ? (
                    <span className="badge-ui badge-ui-success">
                      <span
                        className="settings-inline-dot is-success"
                        aria-hidden="true"
                      ></span>
                      Healthy
                    </span>
                  ) : null}
                </div>
                <p className="settings-daemon-meta">{daemonMeta}</p>
                {daemon && !daemon.restart_supported ? (
                  <p className="settings-warning-text">
                    Restarting from the browser is not supported on this platform.
                  </p>
                ) : null}
              </div>
              <button
                type="button"
                className="button-ui button-ui-secondary"
                disabled={busy || !daemon || !daemon.restart_supported}
                onClick={() => setShowRestart(true)}
              >
                Restart daemon
              </button>
            </div>
          </div>
        </section>

        <section className="settings-row" aria-labelledby="settings-web-title">
          <div className="settings-row-label">
            <h2 id="settings-web-title">Web dashboard</h2>
            <p>
              Where this dashboard listens. Only owners signed in on this browser
              can use it.
            </p>
          </div>
          <div className="settings-row-controls">
            <form className="settings-address-form" onSubmit={saveAddress}>
              <div className="field">
                <label
                  className="field-label"
                  htmlFor="settings-web-address"
                >
                  Address
                </label>
                <div className="settings-address-row">
                  <input
                    id="settings-web-address"
                    type="text"
                    name="web-address"
                    value={address}
                    disabled={busy}
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="127.0.0.1:18300"
                    onChange={(event) => setAddress(event.target.value)}
                  />
                  <button
                    type="submit"
                    className="button-ui button-ui-secondary"
                    disabled={busy || !config || !config.version}
                  >
                    Save address
                  </button>
                </div>
              </div>
              <p className="settings-web-note">
                <span
                  className="settings-inline-dot is-warning"
                  aria-hidden="true"
                ></span>
                Changing the address needs a daemon restart.
              </p>
            </form>
            {savedUrl ? (
              <p className="settings-web-saved">
                After the restart, open <a href={savedUrl}>{savedUrl}</a>.
              </p>
            ) : null}
            <div className="settings-browser-row">
              <p>{browserLabel(browserCount)}</p>
              <button
                type="button"
                className="button-ui button-ui-secondary"
                disabled={busy}
                onClick={signOutOtherBrowsers}
              >
                Sign out other browsers
              </button>
            </div>
          </div>
        </section>

        <section
          className="settings-row"
          aria-labelledby="settings-password-title"
        >
          <div className="settings-row-label">
            <h2 id="settings-password-title">Change password</h2>
            <p>Set a new password to open lana on this computer.</p>
          </div>
          <div className="settings-row-controls">
            <form
              className="settings-password-form"
              onSubmit={changePasswordSubmit}
            >
              <PasswordField
                id="settings-current-password"
                label="Current password"
                value={currentPassword}
                autoComplete="current-password"
                disabled={busy}
                onChange={(value) => {
                  setCurrentPassword(value);
                  setPasswordError("");
                }}
              />
              <PasswordField
                id="settings-new-password"
                label="New password"
                value={newPassword}
                autoComplete="new-password"
                disabled={busy}
                hint="At least 8 characters."
                onChange={(value) => {
                  setNewPassword(value);
                  setPasswordError("");
                }}
              />
              <PasswordField
                id="settings-confirm-password"
                label="Type it again"
                value={confirmPassword}
                autoComplete="new-password"
                disabled={busy}
                onChange={(value) => {
                  setConfirmPassword(value);
                  setPasswordError("");
                }}
              />
              {passwordError ? (
                <p className="setup-field-error" role="alert">
                  {passwordError}
                </p>
              ) : null}
              <div className="settings-password-actions">
                <button
                  type="submit"
                  className="button-ui button-ui-primary"
                  disabled={busy}
                >
                  {busy ? "Changing…" : "Change password"}
                </button>
              </div>
            </form>
          </div>
        </section>

        <section
          className="settings-row"
          aria-labelledby="settings-appearance-title"
        >
          <div className="settings-row-label">
            <h2 id="settings-appearance-title">Appearance</h2>
            <p>Theme for this browser.</p>
          </div>
          <div className="settings-row-controls">
            <fieldset className="theme-tiles settings-appearance-tiles">
              <legend className="sr-only">Theme</legend>
              {THEME_CHOICES.map((choice) => (
                <label
                  key={choice}
                  className={
                    "theme-tile" + (theme === choice ? " is-selected" : "")
                  }
                >
                  <input
                    className="theme-radio"
                    type="radio"
                    name="theme"
                    value={choice}
                    checked={theme === choice}
                    onChange={() => onThemeChange(choice)}
                  />
                  <span
                    className={"theme-preview theme-preview-" + choice}
                    aria-hidden="true"
                  ></span>
                  <span className="theme-name">{themeLabel(choice)}</span>
                </label>
              ))}
            </fieldset>
          </div>
        </section>

        <section className="settings-row" aria-labelledby="settings-about-title">
          <div className="settings-row-label">
            <h2 id="settings-about-title">About</h2>
          </div>
          <div className="settings-row-controls">
            <p className="settings-about">
              {daemon
                ? "lana " +
                  (daemon.version || "unknown") +
                  " · " +
                  (daemon.go_version || "Go unknown") +
                  " · " +
                  (daemon.os || "?") +
                  "/" +
                  (daemon.arch || "?")
                : "Loading daemon details…"}
            </p>
          </div>
        </section>

        {showRestart ? (
          <SettingsRestartDialog
            onClose={() => setShowRestart(false)}
            onRestarted={handleRestarted}
          />
        ) : null}
      </SettingsPanel>
    </main>
  );
}
