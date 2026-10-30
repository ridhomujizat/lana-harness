import {
  html,
  useState,
  useEffect,
  useMemo,
  useCallback,
  useRef,
} from "/assets/vendor/preact-htm.js";
import {
  Dialog,
  ErrorNotice,
  ErrorDetails,
  Loading,
} from "/assets/ui.js";
import { Icon, AgentAvatar, agoLabel } from "/assets/views/agent-common.js";
import * as settingsApi from "/assets/api/settings.js";
import { errorMessage, ApiError } from "/assets/api.js";

/* Settings rev 2 (T-034): the shared shell plus Providers, Secrets, Config file,
   and Audit log. General lives in settings-general.js and imports the shell from
   here, so this file must not import settings-general.js. */

const SETTINGS_TABS = [
  { key: "general", label: "General" },
  { key: "providers", label: "Providers" },
  { key: "secrets", label: "Secrets" },
  { key: "config", label: "Config file" },
  { key: "audit", label: "Audit log" },
];

const PROVIDER_KINDS = [
  { value: "openai_compat", label: "openai_compat" },
  { value: "anthropic", label: "anthropic" },
];

const PROVIDER_NAME_RE = /^[a-z0-9-]+$/;
const SECRET_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const SAVED_AT_KEY = "lana.settings.savedAt";

function readSavedAt() {
  try {
    const value = Number(window.localStorage.getItem(SAVED_AT_KEY));
    return Number.isFinite(value) && value > 0 ? value : 0;
  } catch (cause) {
    return 0;
  }
}

export function markSettingsSaved() {
  try {
    window.localStorage.setItem(SAVED_AT_KEY, String(Date.now()));
  } catch (cause) {
    /* The banner still appears; only the "N min ago" hint is lost. */
  }
}

function clearSettingsSaved() {
  try {
    window.localStorage.removeItem(SAVED_AT_KEY);
  } catch (cause) {
    /* Nothing else depends on the hint. */
  }
}

function savedAgoLabel() {
  const savedAt = readSavedAt();
  if (!savedAt) {
    return "";
  }
  return agoLabel(savedAt);
}

function failureInfo(failure) {
  return {
    message: errorMessage(failure),
    details: failure instanceof ApiError ? failure.details : [],
    code: failure instanceof ApiError ? failure.code : "",
  };
}

function StatusLine({ text }) {
  return html`<p
    class="settings-status"
    role="status"
    aria-live="polite"
    aria-atomic="true"
  >${text}</p>`;
}

function defaultProviderEnv(name) {
  const value = String(name || "")
    .trim()
    .toUpperCase()
    .replace(/[^A-Z0-9_]/g, "_");
  if (!value) {
    return "";
  }
  const validStart = /^[A-Za-z_]/.test(value) ? value : "_" + value;
  return validStart + "_API_KEY";
}

function providerInitial(name) {
  const text = String(name || "").trim();
  return text ? Array.from(text)[0].toUpperCase() : "?";
}

function providerAgents(usedBy) {
  if (!Array.isArray(usedBy)) {
    return [];
  }
  return usedBy
    .map((value) => {
      const text = String(value || "");
      return text.startsWith("agents.") ? text.slice("agents.".length) : text;
    })
    .filter(Boolean);
}

function ProviderUsers({ usedBy }) {
  const names = providerAgents(usedBy);
  if (names.length === 0) {
    return html`<span class="settings-muted">No agents</span>`;
  }
  return html`<span class="settings-used-by">
    <span class="agent-avatar-group" aria-hidden="true">
      ${names.map(
        (name) => html`<${AgentAvatar} key=${name} name=${name} size="24" />`
      )}
    </span>
    <span class="sr-only">${names.join(", ")}</span>
  </span>`;
}

function providerKeyState(provider) {
  if (!provider || !provider.api_key_env) {
    return { label: "No key needed", className: "is-none", note: "" };
  }
  if (provider.key_state === "overridden_by_env") {
    return { label: "Set", className: "is-set", note: "from environment" };
  }
  if (provider.key_state === "set") {
    return { label: "Set", className: "is-set", note: "" };
  }
  return { label: "Missing", className: "is-missing", note: "" };
}

function hasWebScheme(value) {
  try {
    const parsed = new URL(value);
    return (
      (parsed.protocol === "http:" || parsed.protocol === "https:") &&
      Boolean(parsed.host)
    );
  } catch (cause) {
    return false;
  }
}

function matchesProvider(provider, query) {
  if (!query) {
    return true;
  }
  const fields = [
    provider.name,
    provider.kind,
    provider.base_url,
    provider.api_key_env,
  ].concat(providerAgents(provider.used_by));
  return fields
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase()
    .includes(query);
}

function matchesSecret(secret, query) {
  if (!query) {
    return true;
  }
  const fields = [secret.name, secret.state, secret.source].concat(
    Array.isArray(secret.used_by) ? secret.used_by : []
  );
  return fields
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase()
    .includes(query);
}

export function SettingsTabs({ active }) {
  function handleKeyDown(event) {
    if (
      event.key !== "ArrowRight" &&
      event.key !== "ArrowLeft" &&
      event.key !== "Home" &&
      event.key !== "End"
    ) {
      return;
    }
    const tabs = Array.from(
      event.currentTarget.querySelectorAll('[role="tab"]')
    );
    if (tabs.length === 0) {
      return;
    }
    const index = tabs.indexOf(document.activeElement);
    let next = index < 0 ? 0 : index;
    if (event.key === "ArrowRight") {
      next = (next + 1) % tabs.length;
    } else if (event.key === "ArrowLeft") {
      next = (next - 1 + tabs.length) % tabs.length;
    } else if (event.key === "Home") {
      next = 0;
    } else {
      next = tabs.length - 1;
    }
    event.preventDefault();
    tabs[next].focus();
  }

  return html`<div class="settings-head">
    <header class="settings-header">
      <h1 class="settings-title">Settings</h1>
      <p class="settings-subtitle">
        Providers, secrets and the daemon. Agents, skills, MCP and channels have their own pages.
      </p>
    </header>
    <nav
      class="settings-tabs"
      role="tablist"
      aria-label="Settings"
      onKeyDown=${handleKeyDown}
    >
      ${SETTINGS_TABS.map(
        (tab) => html`<a
          key=${tab.key}
          id=${"settings-tab-" + tab.key}
          class=${"settings-tab" + (active === tab.key ? " is-active" : "")}
          role="tab"
          href=${"#/settings/" + tab.key}
          aria-selected=${active === tab.key ? "true" : "false"}
          aria-controls=${"settings-panel-" + tab.key}
          tabIndex=${active === tab.key ? 0 : -1}
        >${tab.label}</a>`
      )}
    </nav>
  </div>`;
}

export function SettingsPanel({ active, children }) {
  return html`<div
    id=${"settings-panel-" + active}
    class="settings-panel"
    role="tabpanel"
    aria-labelledby=${"settings-tab-" + active}
    tabIndex="0"
  >${children}</div>`;
}

export function SettingsRestartDialog({ onClose, onRestarted }) {
  const [info, setInfo] = useState(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [message, setMessage] = useState("");

  useEffect(() => {
    let alive = true;
    settingsApi
      .getDaemon()
      .then((value) => {
        if (!alive) {
          return;
        }
        setInfo(value);
        setLoading(false);
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        const failureState = failureInfo(failure);
        setError(failureState.message);
        setDetails(failureState.details);
        setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, []);

  async function confirmRestart() {
    if (busy || !info) {
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    setMessage("Restart scheduled. Waiting for the daemon to come back.");
    try {
      await settingsApi.restartDaemon();
      const updated = await settingsApi.waitForDaemonRestart(
        info.started_at,
        (text) => setMessage(text)
      );
      clearSettingsSaved();
      setMessage("The daemon is back.");
      if (onRestarted) {
        onRestarted(updated);
      }
      onClose();
    } catch (failure) {
      const failureState = failureInfo(failure);
      setError(failureState.message);
      setDetails(failureState.details);
      setMessage("The restart did not finish.");
      setBusy(false);
    }
  }

  const turns = info && Array.isArray(info.running_turns) ? info.running_turns : [];
  const supported = !info || info.restart_supported;

  return html`<${Dialog}
    title="Restart daemon?"
    dismissible=${!busy}
    onClose=${onClose}
  >
    <div class="settings-dialog-form">
      <p class="hint">
        The daemon restarts in place. Every running turn ends and this page reconnects when
        the daemon is back.
      </p>
      ${loading ? html`<${Loading} label="Checking the daemon…" />` : null}
      ${error ? html`<${ErrorNotice} message=${error} details=${details} />` : null}
      ${info
        ? html`<section
            class="settings-running-turns"
            aria-labelledby="settings-running-turns-title"
          >
            <h3 id="settings-running-turns-title">Running turns</h3>
            ${turns.length > 0
              ? html`<ul class="settings-turn-list">
                  ${turns.map(
                    (turn, index) => html`<li
                      key=${(turn && turn.session_id) || index}
                      class="settings-turn-item"
                    >
                      <span class="settings-turn-title">
                        ${(turn && turn.title) || "Untitled session"}
                      </span>
                      <span class="settings-turn-agent">
                        ${(turn && turn.agent) || "unknown agent"}
                      </span>
                    </li>`
                  )}
                </ul>`
              : html`<p class="hint">No turns are running.</p>`}
          </section>`
        : null}
      ${info && !supported
        ? html`<p class="settings-warning-text">
            Restarting from the browser is not supported on this platform.
          </p>`
        : null}
      <p
        class="settings-status"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >${message}</p>
      <div class="dialog-actions">
        <button
          type="button"
          class="button-ui button-ui-secondary"
          disabled=${busy}
          onClick=${onClose}
        >Cancel</button>
        <button
          type="button"
          class="button-ui button-ui-primary"
          disabled=${busy || loading || !info || !supported}
          onClick=${confirmRestart}
        >${busy ? "Restarting…" : "Restart daemon"}</button>
      </div>
    </div>
  </${Dialog}>`;
}

export function SettingsRestartBanner({ restartRequired, onRestarted }) {
  const [open, setOpen] = useState(false);
  if (!restartRequired) {
    return null;
  }
  const saved = savedAgoLabel();
  return html`<div class="settings-restart-banner">
    <span class="settings-inline-dot is-warning" aria-hidden="true"></span>
    <p class="settings-restart-text">
      ${saved ? "Saved " + saved + ". " : "Saved. "}Restart the daemon for the saved changes to take effect.
    </p>
    <button
      type="button"
      class="button-ui button-ui-secondary"
      onClick=${() => setOpen(true)}
    >Restart daemon</button>
    ${open
      ? html`<${SettingsRestartDialog}
          onClose=${() => setOpen(false)}
          onRestarted=${(info) => {
            setOpen(false);
            if (onRestarted) {
              onRestarted(info);
            }
          }}
        />`
      : null}
  </div>`;
}

function ProviderDialog({ provider, version, onClose, onSaved }) {
  const editing = Boolean(provider);
  const [name, setName] = useState(editing ? provider.name : "");
  const [kind, setKind] = useState(editing ? provider.kind : "openai_compat");
  const [baseUrl, setBaseUrl] = useState(editing ? provider.base_url : "");
  const [apiKeyEnv, setApiKeyEnv] = useState(
    editing ? provider.api_key_env || "" : ""
  );
  const [apiKey, setApiKey] = useState("");
  const [testModel, setTestModel] = useState("");
  const [testing, setTesting] = useState(false);
  const [testStatus, setTestStatus] = useState({ tone: "", text: "" });
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const removeButtonRef = useRef(null);
  const wasConfirmingRef = useRef(false);

  const trimmedName = name.trim();
  const envPreview =
    apiKeyEnv.trim() || (trimmedName ? defaultProviderEnv(trimmedName) : "");

  useEffect(() => {
    if (confirmDelete) {
      wasConfirmingRef.current = true;
      const node = document.getElementById("settings-provider-remove-cancel");
      if (node) {
        node.focus();
      }
    } else if (wasConfirmingRef.current && removeButtonRef.current) {
      wasConfirmingRef.current = false;
      removeButtonRef.current.focus();
    }
  }, [confirmDelete]);

  async function testConnection() {
    if (testing) {
      return;
    }
    const base = baseUrl.trim();
    const model = testModel.trim();
    if (!base) {
      setTestStatus({ tone: "error", text: "Enter a base URL first." });
      return;
    }
    if (!model) {
      setTestStatus({
        tone: "error",
        text: "Enter a model for the test. The model is not saved.",
      });
      return;
    }
    if (!apiKey.trim() && !SECRET_NAME_RE.test(envPreview)) {
      setTestStatus({
        tone: "error",
        text: "Enter a valid API key variable, for example OPENAI_API_KEY.",
      });
      return;
    }
    setTesting(true);
    setTestStatus({ tone: "", text: "Testing the connection…" });
    try {
      const body = { kind, base_url: base, model };
      if (apiKey.trim()) {
        body.api_key = apiKey.trim();
      } else {
        body.api_key_env = envPreview;
      }
      const result = await settingsApi.testProvider(body);
      if (result && result.ok) {
        const latency = Number(result.latency_ms) || 0;
        setTestStatus({
          tone: "ok",
          text: "Connected · " + latency + " ms.",
        });
      } else {
        let text = (result && result.message) || "The provider did not answer.";
        if (result && Number(result.status) > 0) {
          text += " (HTTP " + result.status + ")";
        }
        setTestStatus({ tone: "error", text });
      }
    } catch (failure) {
      setTestStatus({ tone: "error", text: errorMessage(failure) });
    } finally {
      setTesting(false);
    }
  }

  async function submit(event) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const value = name.trim();
    if (!editing && !PROVIDER_NAME_RE.test(value)) {
      setError("The name must use lowercase letters, digits, and dashes, for example openai-2.");
      setDetails([]);
      return;
    }
    const base = baseUrl.trim();
    if (!base) {
      setError("Enter the provider base URL.");
      setDetails([]);
      return;
    }
    if (!hasWebScheme(base)) {
      setError("The base URL must start with the http or https scheme.");
      setDetails([]);
      return;
    }
    if (envPreview && !SECRET_NAME_RE.test(envPreview)) {
      setError("The API key variable must match [A-Za-z_][A-Za-z0-9_]*.");
      setDetails([]);
      return;
    }
    if (!version) {
      setError("config.toml is missing. Run lana init before adding providers.");
      setDetails([]);
      return;
    }

    setBusy(true);
    setError("");
    setDetails([]);
    try {
      const body = {
        kind,
        base_url: base,
        expected_version: version,
      };
      if (envPreview) {
        body.api_key_env = envPreview;
      }
      if (apiKey.trim()) {
        body.api_key = apiKey.trim();
      }
      await settingsApi.saveProvider(value, body);
      markSettingsSaved();
      if (onSaved) {
        onSaved(
          "Provider " + value + " saved. Restart the daemon for the saved changes to take effect."
        );
      }
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setError("The config changed since this list loaded. Close the dialog and reload the list before saving.");
        setDetails([]);
      } else if (failure instanceof ApiError && failure.code === "connection_failed") {
        setError(failure.message || "The provider connection test failed. Nothing was saved.");
        setDetails([]);
      } else {
        const failureState = failureInfo(failure);
        setError(failureState.message);
        setDetails(failureState.details);
      }
    } finally {
      setBusy(false);
    }
  }

  async function removeProvider() {
    if (busy || !editing) {
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      await settingsApi.deleteProvider(provider.name, version);
      markSettingsSaved();
      if (onSaved) {
        onSaved(
          "Provider " + provider.name + " removed. Restart the daemon for the saved changes to take effect."
        );
      }
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setError("The config changed since this list loaded. Close the dialog and reload the list before removing.");
        setDetails([]);
      } else {
        const failureState = failureInfo(failure);
        setError(failureState.message);
        setDetails(failureState.details);
      }
    } finally {
      setBusy(false);
    }
  }

  return html`<${Dialog}
    title=${editing ? "Edit provider" : "Add provider"}
    dismissible=${!busy}
    onClose=${onClose}
  >
    <form
      class="settings-dialog-form"
      onSubmit=${confirmDelete ? (event) => event.preventDefault() : submit}
    >
      <p class="hint">
        ${editing
          ? "Edit the provider table. The config keeps every other table and comment."
          : "Add a provider table. Agents can pick it after the daemon restarts."}
      </p>

      <label class="field">
        <span class="field-label">Name</span>
        <input
          data-autofocus=${editing ? undefined : true}
          type="text"
          name="provider-name"
          value=${name}
          disabled=${editing || busy}
          autocomplete="off"
          spellcheck="false"
          pattern="[a-z0-9-]+"
          required
          aria-describedby="settings-provider-name-hint"
          onInput=${(event) => setName(event.target.value)}
        />
        <span id="settings-provider-name-hint" class="hint">
          Lowercase letters, digits, and dashes. The name cannot change after it is saved.
        </span>
      </label>

      <label class="field">
        <span class="field-label">Kind</span>
        <select
          name="provider-kind"
          value=${kind}
          disabled=${busy}
          onChange=${(event) => setKind(event.target.value)}
        >
          ${PROVIDER_KINDS.map(
            (item) => html`<option key=${item.value} value=${item.value}>
              ${item.label}
            </option>`
          )}
        </select>
      </label>

      <label class="field">
        <span class="field-label">Base URL</span>
        <input
          data-autofocus=${editing ? true : undefined}
          type="url"
          name="provider-base-url"
          value=${baseUrl}
          disabled=${busy}
          autocomplete="off"
          spellcheck="false"
          placeholder="Provider API endpoint"
          required
          aria-describedby="settings-provider-base-url-hint"
          onInput=${(event) => setBaseUrl(event.target.value)}
        />
        <span id="settings-provider-base-url-hint" class="hint">
          The API endpoint, starting with the http or https scheme.
        </span>
      </label>

      <label class="field">
        <span class="field-label">API key variable</span>
        <input
          type="text"
          name="provider-key-env"
          value=${apiKeyEnv}
          disabled=${busy}
          autocomplete="off"
          spellcheck="false"
          placeholder=${defaultProviderEnv(trimmedName) || "OPENAI_API_KEY"}
          aria-describedby="settings-provider-key-env-hint"
          onInput=${(event) => setApiKeyEnv(event.target.value)}
        />
        <span id="settings-provider-key-env-hint" class="hint">
          The env name written to config.toml. Defaults to${" "}
          ${defaultProviderEnv(trimmedName) || "<NAME>_API_KEY"}.
        </span>
      </label>

      <label class="field">
        <span class="field-label">API key</span>
        <input
          type="password"
          name="provider-api-key"
          value=${apiKey}
          disabled=${busy}
          autocomplete="new-password"
          spellcheck="false"
          aria-describedby="settings-provider-api-key-hint"
          onInput=${(event) => setApiKey(event.target.value)}
        />
        <span id="settings-provider-api-key-hint" class="hint">
          ${editing
            ? "Leave empty to keep the saved key. A new key is tested before it is written."
            : "Stored write-only in secrets.env. Leave empty if this provider does not need a key."}
        </span>
      </label>

      <section class="settings-dialog-section" aria-labelledby="settings-provider-test-title">
        <h3 id="settings-provider-test-title">Connection test</h3>
        <p class="hint">
          The test sends one small request. The model is used for the test only and is not saved.
        </p>
        <label class="field">
          <span class="field-label">Test model</span>
          <input
            type="text"
            name="provider-test-model"
            value=${testModel}
            disabled=${busy || testing}
            autocomplete="off"
            spellcheck="false"
            placeholder="gpt-5-mini"
            onInput=${(event) => setTestModel(event.target.value)}
          />
        </label>
        <div class="form-actions">
          <button
            type="button"
            class="button-ui button-ui-secondary"
            disabled=${busy || testing}
            onClick=${testConnection}
          >${testing ? "Testing…" : "Test connection"}</button>
        </div>
        <p
          class=${"settings-test-result" + (testStatus.tone ? " is-" + testStatus.tone : "")}
          role="status"
          aria-live="polite"
        >${testStatus.text}</p>
      </section>

      ${error ? html`<${ErrorNotice} message=${error} details=${details} />` : null}

      ${confirmDelete
        ? html`<div class="settings-confirm">
            <p>
              Remove <strong>${provider.name}</strong>? This cannot be undone. An agent that
              uses this provider must be changed first.
            </p>
            <div class="dialog-actions">
              <button
                id="settings-provider-remove-cancel"
                type="button"
                class="button-ui button-ui-secondary"
                disabled=${busy}
                onClick=${() => setConfirmDelete(false)}
              >Cancel</button>
              <button
                type="button"
                class="button-ui settings-danger-button"
                disabled=${busy}
                onClick=${removeProvider}
              >${busy ? "Removing…" : "Remove provider"}</button>
            </div>
          </div>`
        : html`<div class="dialog-actions">
            ${editing
              ? html`<button
                  ref=${removeButtonRef}
                  type="button"
                  class="button-ui button-ui-tertiary"
                  disabled=${busy}
                  onClick=${() => setConfirmDelete(true)}
                >Remove provider</button>`
              : null}
            <span class="settings-dialog-spacer"></span>
            <button
              type="button"
              class="button-ui button-ui-secondary"
              disabled=${busy}
              onClick=${onClose}
            >Cancel</button>
            <button type="submit" class="button-ui button-ui-primary" disabled=${busy}>
              ${busy ? "Saving…" : "Save provider"}
            </button>
          </div>`}
    </form>
  </${Dialog}>`;
}

export function SettingsProvidersPage() {
  const [data, setData] = useState(null);
  const [state, setState] = useState("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [message, setMessage] = useState("");
  const [query, setQuery] = useState("");
  const [dialog, setDialog] = useState(null);
  const [reloadToken, setReloadToken] = useState(0);

  const reload = useCallback(() => {
    setReloadToken((current) => current + 1);
  }, []);

  useEffect(() => {
    let alive = true;
    setState("loading");
    setError("");
    setDetails([]);
    settingsApi
      .listProviders()
      .then((value) => {
        if (!alive) {
          return;
        }
        setData(value);
        setState("ready");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        const failureState = failureInfo(failure);
        setError(failureState.message);
        setDetails(failureState.details);
        setState("error");
      });
    return () => {
      alive = false;
    };
  }, [reloadToken]);

  const providers =
    data && Array.isArray(data.providers) ? data.providers : [];
  const search = query.trim().toLowerCase();
  const visible = useMemo(
    () => providers.filter((provider) => matchesProvider(provider, search)),
    [data, search]
  );

  function handleSaved(text) {
    setDialog(null);
    setMessage(text);
    reload();
  }

  function openRow(event, provider) {
    const target = event.target;
    if (
      target &&
      typeof target.closest === "function" &&
      target.closest("a, button, input, select, textarea, label")
    ) {
      return;
    }
    setDialog({ provider });
  }

  return html`<main id="main" class="page settings-page">
    <${SettingsTabs} active="providers" />
    <${SettingsPanel} active="providers">
      <${SettingsRestartBanner}
        restartRequired=${Boolean(data && data.restart_required)}
        onRestarted=${reload}
      />

      <div class="settings-toolbar">
        <div class="sessions-search settings-search">
          <span class="sessions-search-icon">
            <${Icon} name="search" />
          </span>
          <label class="sr-only" for="settings-providers-search">Search providers</label>
          <input
            id="settings-providers-search"
            class="sessions-search-input"
            type="search"
            placeholder="Search providers"
            autocomplete="off"
            spellcheck="false"
            value=${query}
            onInput=${(event) => setQuery(event.target.value)}
          />
        </div>
        <span class="settings-toolbar-spacer"></span>
        <button
          type="button"
          class="button-ui button-ui-primary"
          disabled=${!data || !data.version || state === "loading"}
          title=${data && !data.version ? "config.toml is missing. Run lana init first." : undefined}
          onClick=${() => setDialog({ provider: null })}
        >+ Add provider</button>
      </div>

      <${StatusLine} text=${message} />
      ${error ? html`<${ErrorNotice} message=${error} details=${details} />` : null}
      ${state === "error"
        ? html`<div class="form-actions">
            <button
              type="button"
              class="button-ui button-ui-secondary"
              onClick=${reload}
            >Reload providers</button>
          </div>`
        : null}
      ${state === "loading"
        ? html`<${Loading} label="Loading providers…" />`
        : state === "ready" && visible.length === 0
          ? html`<div class="settings-empty">
              <p class="settings-empty-title">
                ${providers.length === 0 ? "No providers configured" : "No providers match “" + query.trim() + "”"}
              </p>
              <p class="settings-empty-text">
                ${providers.length === 0
                  ? "Add a provider to let agents send requests to a model."
                  : "Try a different name, URL, or kind."}
              </p>
            </div>`
          : null}

      ${state === "ready" && visible.length > 0
        ? html`<div class="settings-table-wrap">
            <table class="settings-table settings-providers-table">
              <caption class="sr-only">Providers</caption>
              <thead>
                <tr>
                  <th scope="col">Provider</th>
                  <th scope="col">Kind</th>
                  <th scope="col">API key</th>
                  <th scope="col">Used by</th>
                  <th scope="col"><span class="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                ${visible.map((provider) => {
                  const key = providerKeyState(provider);
                  return html`<tr
                    key=${provider.name}
                    class="settings-clickable-row"
                    onClick=${(event) => openRow(event, provider)}
                  >
                    <td>
                      <span class="settings-name-cell">
                        <span class="settings-initial" aria-hidden="true">
                          ${providerInitial(provider.name)}
                        </span>
                        <span class="settings-name-text">
                          <span class="settings-name">${provider.name}</span>
                          <span class="settings-sub">${provider.base_url}</span>
                        </span>
                      </span>
                    </td>
                    <td>
                      <span class="badge-ui">
                        <span class="badge-ui-dot" aria-hidden="true"></span>
                        ${provider.kind}
                      </span>
                    </td>
                    <td>
                      <span class="settings-key-cell">
                        ${provider.api_key_env
                          ? html`<code class="settings-key-env">${provider.api_key_env}</code>
                              <span class=${"settings-key-state " + key.className}>
                                <span class="settings-dot" aria-hidden="true"></span>
                                ${key.label}
                                ${key.note ? html`<span class="settings-key-note">${key.note}</span>` : null}
                              </span>`
                          : html`<span class="settings-key-state is-none">
                              <span class="settings-dot" aria-hidden="true"></span>
                              No key needed
                            </span>`}
                      </span>
                    </td>
                    <td>
                      <${ProviderUsers} usedBy=${provider.used_by} />
                    </td>
                    <td class="settings-row-action">
                      <button
                        type="button"
                        class="icon-button"
                        aria-label=${"Edit " + provider.name}
                        onClick=${() => setDialog({ provider })}
                      >
                        <${Icon} name="chevron-right" />
                      </button>
                    </td>
                  </tr>`;
                })}
              </tbody>
            </table>
          </div>`
        : null}

      <p class="settings-footer-note">
        Agents pick a provider and model on their General tab. Keys live in${" "}
        <a href="#/settings/secrets">Secrets</a>.
      </p>

      ${dialog
        ? html`<${ProviderDialog}
            provider=${dialog.provider}
            version=${data ? data.version : ""}
            onClose=${() => setDialog(null)}
            onSaved=${handleSaved}
          />`
        : null}
    </${SettingsPanel}>
  </main>`;
}

function SecretDialog({ secret, version, onClose, onSaved }) {
  const editing = Boolean(secret);
  const [name, setName] = useState(editing ? secret.name : "");
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);

  async function submit(event) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const secretName = editing ? secret.name : name.trim();
    if (!SECRET_NAME_RE.test(secretName)) {
      setError("The name must match [A-Za-z_][A-Za-z0-9_]*, for example OPENAI_API_KEY.");
      setDetails([]);
      return;
    }
    if (!value) {
      setError("Enter a value. The value is never shown again after saving.");
      setDetails([]);
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      await settingsApi.setSecret(secretName, value, version);
      setValue("");
      markSettingsSaved();
      if (onSaved) {
        onSaved(
          "Secret " + secretName + " saved. Restart the daemon for the saved changes to take effect."
        );
      }
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setError("The secrets file changed since this list loaded. Close the dialog and reload the list before saving.");
        setDetails([]);
      } else {
        const failureState = failureInfo(failure);
        setError(failureState.message);
        setDetails(failureState.details);
      }
    } finally {
      setBusy(false);
    }
  }

  return html`<${Dialog}
    title=${editing ? "Replace secret" : "New secret"}
    dismissible=${!busy}
    onClose=${onClose}
  >
    <form class="settings-dialog-form" onSubmit=${submit}>
      ${editing
        ? html`<p class="settings-secret-target">
            Replacing <code>${secret.name}</code>. The old value is removed.
          </p>`
        : html`<label class="field">
            <span class="field-label">Name</span>
            <input
              data-autofocus
              type="text"
              name="secret-name"
              value=${name}
              disabled=${busy}
              autocomplete="off"
              spellcheck="false"
              pattern="[A-Za-z_][A-Za-z0-9_]*"
              required
              aria-describedby="settings-secret-name-hint"
              onInput=${(event) => setName(event.target.value)}
            />
            <span id="settings-secret-name-hint" class="hint">
              Letters, digits, and underscores; must not start with a digit.
            </span>
          </label>`}

      <label class="field">
        <span class="field-label">${editing ? "New value" : "Value"}</span>
        <input
          data-autofocus=${editing ? true : undefined}
          type="password"
          name="secret-value"
          value=${value}
          disabled=${busy}
          autocomplete="new-password"
          spellcheck="false"
          required
          aria-describedby="settings-secret-value-hint"
          onInput=${(event) => setValue(event.target.value)}
        />
        <span id="settings-secret-value-hint" class="hint">
          Values are write-only. The value is never shown again after saving.
        </span>
      </label>

      ${error ? html`<${ErrorNotice} message=${error} details=${details} />` : null}

      <div class="dialog-actions">
        <button
          type="button"
          class="button-ui button-ui-secondary"
          disabled=${busy}
          onClick=${onClose}
        >Cancel</button>
        <button type="submit" class="button-ui button-ui-primary" disabled=${busy}>
          ${busy ? "Saving…" : editing ? "Replace secret" : "Save secret"}
        </button>
      </div>
    </form>
  </${Dialog}>`;
}

function RemoveSecretDialog({ secret, version, onClose, onRemoved }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);

  async function remove() {
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      await settingsApi.deleteSecret(secret.name, version);
      markSettingsSaved();
      if (onRemoved) {
        onRemoved(
          "Secret " + secret.name + " removed. Restart the daemon for the saved changes to take effect."
        );
      }
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setError("The secrets file changed since this list loaded. Close the dialog and reload the list before removing.");
        setDetails([]);
      } else {
        const failureState = failureInfo(failure);
        setError(failureState.message);
        setDetails(failureState.details);
      }
    } finally {
      setBusy(false);
    }
  }

  return html`<${Dialog}
    title=${"Remove " + secret.name + "?"}
    dismissible=${!busy}
    onClose=${onClose}
  >
    <div class="settings-dialog-form">
      <p>
        This removes the value from <code>secrets.env</code>. An agent or channel that uses
        this secret will fail until it is set again.
      </p>
      ${error ? html`<${ErrorNotice} message=${error} details=${details} />` : null}
      <div class="dialog-actions">
        <button
          data-autofocus
          type="button"
          class="button-ui button-ui-secondary"
          disabled=${busy}
          onClick=${onClose}
        >Cancel</button>
        <button
          type="button"
          class="button-ui settings-danger-button"
          disabled=${busy}
          onClick=${remove}
        >${busy ? "Removing…" : "Remove secret"}</button>
      </div>
    </div>
  </${Dialog}>`;
}

export function SettingsSecretsPage() {
  const [data, setData] = useState(null);
  const [state, setState] = useState("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [message, setMessage] = useState("");
  const [query, setQuery] = useState("");
  const [dialog, setDialog] = useState(null);
  const [removeTarget, setRemoveTarget] = useState(null);
  const [reloadToken, setReloadToken] = useState(0);

  const reload = useCallback(() => {
    setReloadToken((current) => current + 1);
  }, []);

  useEffect(() => {
    let alive = true;
    setState("loading");
    setError("");
    setDetails([]);
    settingsApi
      .listSecrets()
      .then((value) => {
        if (!alive) {
          return;
        }
        setData(value);
        setState("ready");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        const failureState = failureInfo(failure);
        setError(failureState.message);
        setDetails(failureState.details);
        setState("error");
      });
    return () => {
      alive = false;
    };
  }, [reloadToken]);

  const secrets = data && Array.isArray(data.secrets) ? data.secrets : [];
  const search = query.trim().toLowerCase();
  const visible = useMemo(
    () => secrets.filter((secret) => matchesSecret(secret, search)),
    [data, search]
  );

  function handleSaved(text) {
    setDialog(null);
    setMessage(text);
    reload();
  }

  function handleRemoved(text) {
    setRemoveTarget(null);
    setMessage(text);
    reload();
  }

  return html`<main id="main" class="page settings-page">
    <${SettingsTabs} active="secrets" />
    <${SettingsPanel} active="secrets">
      <${SettingsRestartBanner}
        restartRequired=${Boolean(data && data.restart_required)}
        onRestarted=${reload}
      />

      <div class="settings-write-note">
        <${Icon} name="lock" />
        <p>
          Values are write-only. After you save one, nobody can read it here again. You can
          replace or remove it, not read it.
        </p>
      </div>

      <div class="settings-toolbar">
        <div class="sessions-search settings-search">
          <span class="sessions-search-icon">
            <${Icon} name="search" />
          </span>
          <label class="sr-only" for="settings-secrets-search">Search secrets</label>
          <input
            id="settings-secrets-search"
            class="sessions-search-input"
            type="search"
            placeholder="Search secrets"
            autocomplete="off"
            spellcheck="false"
            value=${query}
            onInput=${(event) => setQuery(event.target.value)}
          />
        </div>
        <span class="settings-toolbar-spacer"></span>
        <button
          type="button"
          class="button-ui button-ui-primary"
          disabled=${!data || state === "loading"}
          onClick=${() => setDialog({ secret: null })}
        >+ New secret</button>
      </div>

      <${StatusLine} text=${message} />
      ${error ? html`<${ErrorNotice} message=${error} details=${details} />` : null}
      ${state === "error"
        ? html`<div class="form-actions">
            <button
              type="button"
              class="button-ui button-ui-secondary"
              onClick=${reload}
            >Reload secrets</button>
          </div>`
        : null}
      ${state === "loading"
        ? html`<${Loading} label="Loading secrets…" />`
        : state === "ready" && visible.length === 0
          ? html`<div class="settings-empty">
              <p class="settings-empty-title">
                ${secrets.length === 0 ? "No secrets yet" : "No secrets match “" + query.trim() + "”"}
              </p>
              <p class="settings-empty-text">
                ${secrets.length === 0
                  ? "Add a secret for a provider, a channel, or an MCP server."
                  : "Try a different name or usage."}
              </p>
            </div>`
          : null}

      ${state === "ready" && visible.length > 0
        ? html`<div class="settings-table-wrap">
            <table class="settings-table settings-secrets-table">
              <caption class="sr-only">Secrets</caption>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Used by</th>
                  <th scope="col">Updated</th>
                  <th scope="col"><span class="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                ${visible.map((secret) => {
                  const isSet = secret.state === "set";
                  const usedBy = Array.isArray(secret.used_by) ? secret.used_by : [];
                  return html`<tr key=${secret.name}>
                    <td>
                      <span class="settings-secret-name">
                        <${Icon} name="lock" />
                        <span class="settings-secret-text">
                          <code>${secret.name}</code>
                          <span class="settings-secret-value">
                            ${isSet
                              ? html`<span class="settings-secret-dots" aria-hidden="true">••••••••••••</span>
                                  <span class="settings-secret-state">Set</span>`
                              : html`<span class="settings-secret-state">Not set</span>`}
                            ${secret.overridden_by_env
                              ? html`<span class="badge-ui badge-ui-warning">From environment</span>`
                              : null}
                          </span>
                        </span>
                      </span>
                    </td>
                    <td>
                      ${usedBy.length > 0
                        ? html`<span class="settings-used-paths">
                            ${usedBy.map(
                              (path, index) => html`<span key=${path}>
                                ${index > 0 ? ", " : ""}<code>${path}</code>
                              </span>`
                            )}
                          </span>`
                        : html`<span class="settings-muted">Not used</span>`}
                    </td>
                    <td>${agoLabel(secret.updated_at) || "—"}</td>
                    <td class="settings-row-action">
                      <span class="settings-actions">
                        <button
                          type="button"
                          class="button-ui button-ui-tertiary"
                          onClick=${() => setDialog({ secret })}
                        >Replace</button>
                        <button
                          type="button"
                          class="button-ui button-ui-tertiary"
                          disabled=${!isSet}
                          title=${!isSet ? "This secret is not set." : undefined}
                          onClick=${() => setRemoveTarget(secret)}
                        >Remove</button>
                      </span>
                    </td>
                  </tr>`;
                })}
              </tbody>
            </table>
          </div>`
        : null}

      ${data && data.version
        ? html`<p class="settings-footer-note">
            Secrets are stored in <code>secrets.env</code>. Every write uses the version this
            list loaded.
          </p>`
        : null}

      ${dialog
        ? html`<${SecretDialog}
            secret=${dialog.secret}
            version=${data ? data.version : ""}
            onClose=${() => setDialog(null)}
            onSaved=${handleSaved}
          />`
        : null}
      ${removeTarget
        ? html`<${RemoveSecretDialog}
            secret=${removeTarget}
            version=${data ? data.version : ""}
            onClose=${() => setRemoveTarget(null)}
            onRemoved=${handleRemoved}
          />`
        : null}
    </${SettingsPanel}>
  </main>`;
}

function configLineNumbers(raw) {
  const count = String(raw || "").split("\n").length;
  const lines = [];
  for (let number = 1; number <= count; number += 1) {
    lines.push(number);
  }
  return lines;
}

export function SettingsConfigPage() {
  const [config, setConfig] = useState(null);
  const [daemon, setDaemon] = useState(null);
  const [raw, setRaw] = useState("");
  const [state, setState] = useState("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [errorCode, setErrorCode] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const gutterRef = useRef(null);

  const load = useCallback(async (announceFailure) => {
    setState("loading");
    setError("");
    setDetails([]);
    setErrorCode("");
    const results = await Promise.allSettled([
      settingsApi.getConfig(),
      settingsApi.getDaemon(),
    ]);
    let failure = null;
    if (results[0].status === "fulfilled") {
      const value = results[0].value;
      setConfig(value);
      setRaw(value && typeof value.raw === "string" ? value.raw : "");
    } else {
      failure = results[0].reason;
    }
    if (results[1].status === "fulfilled") {
      setDaemon(results[1].value);
    }
    if (failure) {
      const failureState = failureInfo(failure);
      setError(failureState.message);
      setDetails(failureState.details);
      setState("error");
      if (announceFailure) {
        setMessage("Could not load the config. " + failureState.message);
      }
    } else {
      setState("ready");
    }
  }, []);

  useEffect(() => {
    load(false);
  }, [load]);

  const lineNumbers = useMemo(() => configLineNumbers(raw), [raw]);
  const missing = Boolean(config && !config.version);
  const validation =
    config && config.validation ? config.validation : { ok: true, errors: [] };
  const validationErrors = Array.isArray(validation.errors)
    ? validation.errors
    : [];

  function syncGutter(event) {
    const gutter = gutterRef.current;
    if (gutter) {
      gutter.scrollTop = event.currentTarget.scrollTop;
    }
  }

  async function save(event) {
    event.preventDefault();
    if (busy) {
      return;
    }
    if (!config) {
      return;
    }
    if (!config.version) {
      setError("config.toml is missing. Run lana init to create it; the web view does not create the file.");
      setDetails([]);
      setMessage("Config not saved: config.toml is missing.");
      return;
    }
    if (!raw.trim()) {
      setError("The config is empty. Nothing was saved.");
      setDetails([]);
      setMessage("Config not saved: the editor is empty.");
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    setErrorCode("");
    setMessage("Saving config.toml…");
    try {
      const result = await settingsApi.saveConfig(raw, config.version);
      markSettingsSaved();
      setConfig({
        ...config,
        version: result.version,
        restart_required: result.restart_required,
        validation: { ok: true, errors: [] },
      });
      setMessage(
        "Config saved. Restart the daemon for the saved changes to take effect."
      );
    } catch (failure) {
      const failureState = failureInfo(failure);
      if (failureState.code === "validation_failed") {
        setError("The config was not saved because validation failed.");
        setMessage("Config not saved. Validation failed.");
      } else if (failureState.code === "stale_version") {
        setError("The file changed on disk since it was loaded. Reload before saving.");
        setMessage("Config not saved: the file changed on disk.");
      } else if (failureState.code === "config_missing") {
        setError("config.toml does not exist. Run lana init to create it.");
        setMessage("Config not saved: config.toml does not exist.");
      } else {
        setError(failureState.message);
        setMessage("Could not save the config.");
      }
      setDetails(failureState.details);
      setErrorCode(failureState.code);
    } finally {
      setBusy(false);
    }
  }

  const path = daemon && daemon.home ? daemon.home + "/config.toml" : "config.toml";

  return html`<main id="main" class="page settings-page">
    <${SettingsTabs} active="config" />
    <${SettingsPanel} active="config">
      <${SettingsRestartBanner}
        restartRequired=${Boolean(config && config.restart_required)}
        onRestarted=${() => load(false)}
      />

      ${state === "loading" && !config
        ? html`<${Loading} label="Loading config.toml…" />`
        : null}
      ${error ? html`<${ErrorNotice} message=${error} details=${details} />` : null}
      ${state === "error"
        ? html`<div class="form-actions">
            <button
              type="button"
              class="button-ui button-ui-secondary"
              disabled=${busy}
              onClick=${() => load(true)}
            >Reload config</button>
          </div>`
        : null}
      <${StatusLine} text=${message} />
      ${missing
        ? html`<p class="notice notice-warn">
            config.toml is missing. Run <code>lana init</code> to create it; the web view does
            not create the file.
          </p>`
        : null}

      <form class="settings-config-form" onSubmit=${save}>
        <div class="settings-config-head">
          <p class="settings-config-path">
            <code>${path}</code>${" "}
            <span class="settings-config-note">
              · validated on save · comments kept · secret values show as ***
            </span>
          </p>
          <button
            type="submit"
            class="button-ui button-ui-primary"
            disabled=${busy || missing || !config || !raw.trim()}
          >${busy ? "Saving…" : "Save config.toml"}</button>
        </div>

        <div class="settings-editor">
          <div class="settings-gutter" aria-hidden="true" ref=${gutterRef}>
            ${lineNumbers.map(
              (number) => html`<span key=${number} class="settings-gutter-line">${number}</span>`
            )}
          </div>
          <label class="sr-only" for="settings-config-raw">config.toml contents</label>
          <textarea
            id="settings-config-raw"
            class="settings-editor-input"
            wrap="off"
            spellcheck="false"
            value=${raw}
            disabled=${busy || missing || !config}
            onInput=${(event) => setRaw(event.target.value)}
            onScroll=${syncGutter}
          ></textarea>
        </div>
      </form>

      ${validationErrors.length > 0
        ? html`<div class="error-box settings-validation">
            <p class="error-message">Validation errors</p>
            <${ErrorDetails} details=${validationErrors} />
          </div>`
        : null}
      ${errorCode === "stale_version"
        ? html`<div class="form-actions">
            <button
              type="button"
              class="button-ui button-ui-secondary"
              disabled=${busy}
              onClick=${() => load(true)}
            >Reload from disk</button>
          </div>`
        : null}
      ${config && config.version
        ? html`<p class="settings-footer-note">
            Optimistic concurrency: saving sends this file's current version.
          </p>`
        : null}
    </${SettingsPanel}>
  </main>`;
}

function auditTimeLabel(value) {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) {
    return "—";
  }
  return new Date(number).toLocaleString();
}

function auditTimeISO(value) {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) {
    return undefined;
  }
  return new Date(number).toISOString();
}

export function SettingsAuditPage() {
  const [entries, setEntries] = useState([]);
  const [nextBefore, setNextBefore] = useState(null);
  const [state, setState] = useState("loading");
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [message, setMessage] = useState("");
  const [restartRequired, setRestartRequired] = useState(false);

  const loadRestartState = useCallback(() => {
    settingsApi
      .getConfig()
      .then((value) => {
        setRestartRequired(Boolean(value && value.restart_required));
      })
      .catch((cause) => {
        /* The banner is optional; the audit list still works. */
      });
  }, []);

  const loadPage = useCallback(async (before, append) => {
    if (append) {
      setLoadingMore(true);
    } else {
      setState("loading");
    }
    setError("");
    setDetails([]);
    setMessage(append ? "Loading more entries…" : "Loading the audit log…");
    try {
      const page = await settingsApi.listAudit(before, 50);
      const rows = page && Array.isArray(page.entries) ? page.entries : [];
      setEntries((current) => (append ? current.concat(rows) : rows));
      const cursor = page ? Number(page.next_before) : 0;
      setNextBefore(Number.isFinite(cursor) && cursor > 0 ? cursor : null);
      setState("ready");
      setMessage(
        append
          ? "Loaded " + rows.length + " more " + (rows.length === 1 ? "entry." : "entries.")
          : "Loaded " + rows.length + " " + (rows.length === 1 ? "entry." : "entries.")
      );
    } catch (failure) {
      const failureState = failureInfo(failure);
      setError(failureState.message);
      setDetails(failureState.details);
      setState(append ? "ready" : "error");
      setMessage("Could not load the audit log.");
    } finally {
      setLoadingMore(false);
    }
  }, []);

  useEffect(() => {
    loadRestartState();
    loadPage(null, false);
  }, [loadPage, loadRestartState]);

  return html`<main id="main" class="page settings-page">
    <${SettingsTabs} active="audit" />
    <${SettingsPanel} active="audit">
      <${SettingsRestartBanner}
        restartRequired=${restartRequired}
        onRestarted=${() => {
          loadRestartState();
          loadPage(null, false);
        }}
      />

      <${StatusLine} text=${message} />
      ${error ? html`<${ErrorNotice} message=${error} details=${details} />` : null}
      ${state === "error"
        ? html`<div class="form-actions">
            <button
              type="button"
              class="button-ui button-ui-secondary"
              onClick=${() => loadPage(null, false)}
            >Reload audit log</button>
          </div>`
        : null}

      ${state === "loading"
        ? html`<${Loading} label="Loading the audit log…" />`
        : state === "ready" && entries.length === 0
          ? html`<div class="settings-empty">
              <p class="settings-empty-title">No settings changes yet</p>
              <p class="settings-empty-text">
                Saves, restarts, and sign-outs appear here after they happen.
              </p>
            </div>`
          : null}

      ${state === "ready" && entries.length > 0
        ? html`<div class="settings-table-wrap">
            <table class="settings-table settings-audit-table">
              <caption class="sr-only">Settings audit log</caption>
              <thead>
                <tr>
                  <th scope="col">Time</th>
                  <th scope="col">Kind</th>
                  <th scope="col">Target</th>
                  <th scope="col">Changed keys</th>
                </tr>
              </thead>
              <tbody>
                ${entries.map((entry, index) => {
                  const changed = Array.isArray(entry.changed_keys)
                    ? entry.changed_keys
                    : [];
                  const iso = auditTimeISO(entry.time);
                  return html`<tr key=${(entry.time || 0) + ":" + index}>
                    <td class="settings-audit-time">
                      <time dateTime=${iso || undefined}>${auditTimeLabel(entry.time)}</time>
                    </td>
                    <td><span class="badge-ui">${entry.kind}</span></td>
                    <td><code>${entry.target || "—"}</code></td>
                    <td class="settings-audit-keys">
                      ${changed.length > 0
                        ? changed.map(
                            (key, keyIndex) => html`<span key=${key + ":" + keyIndex}>
                              ${keyIndex > 0 ? ", " : ""}<code>${key}</code>
                            </span>`
                          )
                        : html`<span class="settings-muted">—</span>`}
                    </td>
                  </tr>`;
                })}
              </tbody>
            </table>
          </div>`
        : null}

      ${state === "ready" && entries.length > 0
        ? html`<div class="settings-load-more">
            ${nextBefore !== null
              ? html`<button
                  type="button"
                  class="button-ui button-ui-secondary"
                  disabled=${loadingMore}
                  onClick=${() => loadPage(nextBefore, true)}
                >${loadingMore ? "Loading…" : "Load more"}</button>`
              : html`<p class="settings-footer-note">That is every entry in the audit file.</p>`}
          </div>`
        : null}

      <p class="settings-footer-note">
        Entries are newest first. Each line names the target and the changed keys, never a value.
      </p>
    </${SettingsPanel}>
  </main>`;
}
