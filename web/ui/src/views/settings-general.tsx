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
import { Ic } from "../ui/icons";
import { Badge, Button, Field, IconButton, Input, Seg, Skeleton } from "../ui/gr8r";
import {
  Alert,
  SettingsPanel,
  SettingsRestartBanner,
  SettingsRestartDialog,
  SettingsTabs,
  StatusLine,
  markSettingsSaved,
} from "./settings-pages";
import { SettingsUpdatesSection } from "./settings-updates";

/* Settings > General (REQ-WEB-054). The theme choice of REQ-WEB-031 stays here;
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

interface PasswordInputProps {
  id: string;
  label: string;
  value: string;
  autoComplete: string;
  disabled?: boolean;
  hint?: string;
  onChange: (value: string) => void;
}

/* A Gr8r Field + Input with the eye toggle in the Gr8r icon-button style. */
function PasswordInput({
  id,
  label,
  value,
  autoComplete,
  disabled = false,
  hint,
  onChange,
}: PasswordInputProps) {
  const [shown, setShown] = useState(false);
  return (
    <Field label={label} htmlFor={id} hint={hint}>
      <span className="settings-password">
        <Input
          id={id}
          type={shown ? "text" : "password"}
          autoComplete={autoComplete}
          spellCheck={false}
          value={value}
          disabled={disabled}
          onChange={(event) => onChange(event.target.value)}
        />
        <IconButton
          className="settings-password-eye"
          icon={shown ? "eye-off" : "eye"}
          size="sm"
          on={shown}
          label={shown ? "Hide password" : "Show password"}
          onClick={() => setShown((current) => !current)}
        />
      </span>
    </Field>
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
      <SettingsPanel
        active="general"
        title="General"
        description="The daemon, the dashboard, your password, and this browser."
      >
        <SettingsRestartBanner
          restartRequired={Boolean(config && config.restart_required)}
          onRestarted={handleRestarted}
        />

        {state === "loading" && !daemon && !config ? (
          <div className="settings-loading-inline" aria-busy="true">
            <span className="sr">Loading settings…</span>
            <Skeleton width={180} height={14} />
            <Skeleton width="70%" height={12} />
          </div>
        ) : null}
        <Alert tone="danger" message={error} details={details} />
        {state === "error" ? (
          <div className="settings-actions-bar">
            <Button variant="secondary" disabled={busy} onClick={() => void load(true)}>
              Reload settings
            </Button>
          </div>
        ) : null}
        <StatusLine text={message} />

        <section className="sblock" aria-labelledby="settings-daemon-title">
          <h2 id="settings-daemon-title">Daemon</h2>
          <div className="srow">
            <div>
              <div className="t">{daemonState}</div>
              <div className="d mono">{daemonMeta}</div>
            </div>
            <div className="settings-daemon-control">
              {daemon ? (
                <Badge tone="green" dot>
                  Healthy
                </Badge>
              ) : null}
              <Button
                variant="secondary"
                icon="rotate-cw"
                disabled={busy || !daemon || !daemon.restart_supported}
                onClick={() => setShowRestart(true)}
              >
                Restart daemon
              </Button>
            </div>
          </div>
          {daemon && !daemon.restart_supported ? (
            <p className="settings-warning-text">
              Restarting from the browser is not supported on this platform.
            </p>
          ) : null}
        </section>

        <SettingsUpdatesSection />

        <section className="sblock" aria-labelledby="settings-web-title">
          <h2 id="settings-web-title">Web dashboard</h2>
          <form className="srow" onSubmit={saveAddress}>
            <div>
              <div className="t">Address</div>
              <div className="d">
                Where this dashboard listens. Only owners signed in on this
                browser can use it.
              </div>
            </div>
            <div className="settings-address">
              <Input
                id="settings-web-address"
                className="mono"
                type="text"
                name="web-address"
                aria-label="Address"
                value={address}
                disabled={busy}
                autoComplete="off"
                spellCheck={false}
                placeholder="127.0.0.1:18300"
                onChange={(event) => setAddress(event.target.value)}
              />
              <Button
                type="submit"
                variant="secondary"
                disabled={busy || !config || !config.version}
              >
                Save address
              </Button>
            </div>
          </form>
          <p className="settings-web-note">
            <Ic name="triangle-alert" size={13} />
            Changing the address needs a daemon restart.
          </p>
          {savedUrl ? (
            <p className="settings-web-saved">
              After the restart, open <a href={savedUrl}>{savedUrl}</a>.
            </p>
          ) : null}
          <div className="srow">
            <div>
              <div className="t">Signed-in browsers</div>
              <div className="d">{browserLabel(browserCount)}</div>
            </div>
            <div>
              <Button
                variant="secondary"
                disabled={busy}
                onClick={signOutOtherBrowsers}
              >
                Sign out other browsers
              </Button>
            </div>
          </div>
        </section>

        <section className="sblock" aria-labelledby="settings-password-title">
          <h2 id="settings-password-title">Change password</h2>
          <form
            className="settings-password-form"
            onSubmit={changePasswordSubmit}
          >
            <PasswordInput
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
            <PasswordInput
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
            <PasswordInput
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
              <span className="err" role="alert">
                <Ic name="circle-alert" size={12} />
                {passwordError}
              </span>
            ) : null}
            <div className="settings-password-actions">
              <Button
                type="submit"
                variant="primary"
                disabled={busy}
                loading={busy}
              >
                {busy ? "Changing…" : "Change password"}
              </Button>
            </div>
          </form>
        </section>

        <section className="sblock" aria-labelledby="settings-appearance-title">
          <h2 id="settings-appearance-title">Appearance</h2>
          <div className="srow">
            <div>
              <div className="t">Theme</div>
              <div className="d">Theme for this browser.</div>
            </div>
            <div>
              <Seg
                aria-label="Theme"
                options={THEME_CHOICES.map((choice) => ({
                  value: choice,
                  label: themeLabel(choice),
                }))}
                value={theme}
                onChange={(value) => onThemeChange(value as ThemeChoice)}
              />
            </div>
          </div>
        </section>

        <section className="sblock" aria-labelledby="settings-about-title">
          <h2 id="settings-about-title">About</h2>
          <div className="srow">
            <div>
              <div className="t">Version</div>
              <div className="d">This daemon build.</div>
            </div>
            <div className="settings-about mono">
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
            </div>
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
