import {
  html,
  useState,
  useEffect,
  useCallback,
} from "/assets/vendor/preact-htm.js";
import { THEME_CHOICES, themeLabel } from "/assets/theme.js";
import { ErrorNotice, Loading } from "/assets/ui.js";
import {
  SettingsTabs,
  SettingsPanel,
  SettingsRestartBanner,
  SettingsRestartDialog,
  markSettingsSaved,
} from "/assets/views/settings-pages.js";
import * as settingsApi from "/assets/api/settings.js";
import { errorMessage, ApiError } from "/assets/api.js";

/* Settings > General (REQ-WEB-054). The theme tiles of REQ-WEB-031 stay here;
   the daemon, web dashboard, and about sections come from API-WEB-022,
   API-WEB-024, API-WEB-029, and API-WEB-013. */

function failureInfo(failure) {
  return {
    message: errorMessage(failure),
    details: failure instanceof ApiError ? failure.details : [],
    code: failure instanceof ApiError ? failure.code : "",
  };
}

function formatUptime(milliseconds) {
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

function browserLabel(count) {
  const value = Number(count) || 0;
  return value + (value === 1 ? " browser signed in as owner" : " browsers signed in as owner");
}

export function SettingsGeneralPage({ theme, onThemeChange }) {
  const [daemon, setDaemon] = useState(null);
  const [config, setConfig] = useState(null);
  const [sessions, setSessions] = useState(null);
  const [state, setState] = useState("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [message, setMessage] = useState("");
  const [address, setAddress] = useState("");
  const [savedUrl, setSavedUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [showRestart, setShowRestart] = useState(false);

  const load = useCallback(async (announceFailure) => {
    setState("loading");
    setError("");
    setDetails([]);
    const results = await Promise.allSettled([
      settingsApi.getDaemon(),
      settingsApi.getConfig(),
      settingsApi.listBrowserSessions(),
    ]);

    let failure = null;
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
    load(false);
  }, [load]);

  const browserCount = sessions
    ? sessions.length
    : daemon
      ? Number(daemon.browsers_signed_in) || 0
      : 0;

  async function saveAddress(event) {
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
    } catch (failure) {
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
          : "Signed out " + revoked + " other " + (revoked === 1 ? "browser." : "browsers.")
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
        setSessions(value && Array.isArray(value.sessions) ? value.sessions : []);
      }
    } catch (failure) {
      const failureState = failureInfo(failure);
      setError(failureState.message);
      setDetails(failureState.details);
      setMessage("Could not sign out the other browsers.");
    } finally {
      setBusy(false);
    }
  }

  function handleRestarted() {
    setShowRestart(false);
    setMessage("Daemon restarted. Config and secret changes are now active.");
    load(false);
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

  return html`<main id="main" class="page settings-page">
    <${SettingsTabs} active="general" />
    <${SettingsPanel} active="general">
      <${SettingsRestartBanner}
        restartRequired=${Boolean(config && config.restart_required)}
        onRestarted=${handleRestarted}
      />

      ${state === "loading" && !daemon && !config
        ? html`<${Loading} label="Loading settings…" />`
        : null}
      ${error ? html`<${ErrorNotice} message=${error} details=${details} />` : null}
      ${state === "error"
        ? html`<div class="form-actions">
            <button
              type="button"
              class="button-ui button-ui-secondary"
              disabled=${busy}
              onClick=${() => load(true)}
            >Reload settings</button>
          </div>`
        : null}
      <p
        class="settings-status"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >${message}</p>

      <section class="settings-row" aria-labelledby="settings-daemon-title">
        <div class="settings-row-label">
          <h2 id="settings-daemon-title">Daemon</h2>
          <p>lana daemon runs agents, channels and this dashboard.</p>
        </div>
        <div class="settings-row-controls">
          <div class="settings-daemon-card">
            <div class="settings-daemon-text">
              <div class="settings-daemon-title-row">
                <h3>${daemonState}</h3>
                ${daemon
                  ? html`<span class="badge-ui badge-ui-success">
                      <span class="settings-inline-dot is-success" aria-hidden="true"></span>
                      Healthy
                    </span>`
                  : null}
              </div>
              <p class="settings-daemon-meta">${daemonMeta}</p>
              ${daemon && !daemon.restart_supported
                ? html`<p class="settings-warning-text">
                    Restarting from the browser is not supported on this platform.
                  </p>`
                : null}
            </div>
            <button
              type="button"
              class="button-ui button-ui-secondary"
              disabled=${busy || !daemon || !daemon.restart_supported}
              onClick=${() => setShowRestart(true)}
            >Restart daemon</button>
          </div>
        </div>
      </section>

      <section class="settings-row" aria-labelledby="settings-web-title">
        <div class="settings-row-label">
          <h2 id="settings-web-title">Web dashboard</h2>
          <p>Where this dashboard listens. Only owners signed in on this browser can use it.</p>
        </div>
        <div class="settings-row-controls">
          <form class="settings-address-form" onSubmit=${saveAddress}>
            <div class="field">
              <label class="field-label" for="settings-web-address">Address</label>
              <div class="settings-address-row">
                <input
                  id="settings-web-address"
                  type="text"
                  name="web-address"
                  value=${address}
                  disabled=${busy}
                  autocomplete="off"
                  spellcheck="false"
                  placeholder="127.0.0.1:18300"
                  onInput=${(event) => setAddress(event.target.value)}
                />
                <button
                  type="submit"
                  class="button-ui button-ui-secondary"
                  disabled=${busy || !config || !config.version}
                >Save address</button>
              </div>
            </div>
            <p class="settings-web-note">
              <span class="settings-inline-dot is-warning" aria-hidden="true"></span>
              Changing the address needs a daemon restart.
            </p>
          </form>
          ${savedUrl
            ? html`<p class="settings-web-saved">
                After the restart, open <a href=${savedUrl}>${savedUrl}</a>.
              </p>`
            : null}
          <div class="settings-browser-row">
            <p>${browserLabel(browserCount)}</p>
            <button
              type="button"
              class="button-ui button-ui-secondary"
              disabled=${busy}
              onClick=${signOutOtherBrowsers}
            >Sign out other browsers</button>
          </div>
        </div>
      </section>

      <section class="settings-row" aria-labelledby="settings-appearance-title">
        <div class="settings-row-label">
          <h2 id="settings-appearance-title">Appearance</h2>
          <p>Theme for this browser.</p>
        </div>
        <div class="settings-row-controls">
          <fieldset class="theme-tiles settings-appearance-tiles">
            <legend class="sr-only">Theme</legend>
            ${THEME_CHOICES.map(
              (choice) => html`<label
                key=${choice}
                class=${"theme-tile" + (theme === choice ? " is-selected" : "")}
              >
                <input
                  class="theme-radio"
                  type="radio"
                  name="theme"
                  value=${choice}
                  checked=${theme === choice}
                  onChange=${() => onThemeChange(choice)}
                />
                <span
                  class=${"theme-preview theme-preview-" + choice}
                  aria-hidden="true"
                ></span>
                <span class="theme-name">${themeLabel(choice)}</span>
              </label>`
            )}
          </fieldset>
        </div>
      </section>

      <section class="settings-row" aria-labelledby="settings-about-title">
        <div class="settings-row-label">
          <h2 id="settings-about-title">About</h2>
        </div>
        <div class="settings-row-controls">
          <p class="settings-about">
            ${daemon
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

      ${showRestart
        ? html`<${SettingsRestartDialog}
            onClose=${() => setShowRestart(false)}
            onRestarted=${handleRestarted}
          />`
        : null}
    </${SettingsPanel}>
  </main>`;
}
