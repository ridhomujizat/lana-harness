/* eslint-disable react-hooks/set-state-in-effect -- the old Preact views loaded
   data on mount and synced form state in effects; this port keeps that shape. */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type {
  FormEvent,
  KeyboardEvent,
  MouseEvent,
  ReactNode,
  UIEvent,
} from "react";
import { ApiError, errorMessage } from "../api/client";
import * as settingsApi from "../api/settings";
import type {
  ConfigIssue,
  ConfigValidation,
  GetConfigResponse,
  ListProvidersResponse,
  ProviderRow,
  ProviderTestRequest,
  ProviderWriteRequest,
} from "../api/settings";
import type { ListSecretsResponse, SecretRow } from "../api/secrets";
import type { DaemonInfo } from "../api/daemon";
import type { AuditEntry } from "../api/audit";
import { Dialog, ErrorDetails, ErrorNotice, Icon, Loading } from "../ui/ui";
import { AgentAvatar, agoLabel } from "./agent-common";

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

function readSavedAt(): number {
  try {
    const value = Number(window.localStorage.getItem(SAVED_AT_KEY));
    return Number.isFinite(value) && value > 0 ? value : 0;
  } catch {
    return 0;
  }
}

export function markSettingsSaved(): void {
  try {
    window.localStorage.setItem(SAVED_AT_KEY, String(Date.now()));
  } catch {
    /* The banner still appears; only the "N min ago" hint is lost. */
  }
}

function clearSettingsSaved(): void {
  try {
    window.localStorage.removeItem(SAVED_AT_KEY);
  } catch {
    /* Nothing else depends on the hint. */
  }
}

function savedAgoLabel(): string {
  const savedAt = readSavedAt();
  if (!savedAt) {
    return "";
  }
  return agoLabel(savedAt);
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

function StatusLine({ text }: { text: string }) {
  return (
    <p
      className="settings-status"
      role="status"
      aria-live="polite"
      aria-atomic="true"
    >
      {text}
    </p>
  );
}

function defaultProviderEnv(name: string): string {
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

function providerInitial(name: string): string {
  const text = String(name || "").trim();
  return text ? (Array.from(text)[0] || "").toUpperCase() : "?";
}

function providerAgents(usedBy: string[] | undefined): string[] {
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

function ProviderUsers({ usedBy }: { usedBy: string[] }) {
  const names = providerAgents(usedBy);
  if (names.length === 0) {
    return <span className="settings-muted">No agents</span>;
  }
  return (
    <span className="settings-used-by">
      <span className="agent-avatar-group" aria-hidden="true">
        {names.map((name) => (
          <AgentAvatar key={name} name={name} size="24" />
        ))}
      </span>
      <span className="sr-only">{names.join(", ")}</span>
    </span>
  );
}

function providerKeyState(provider: ProviderRow): {
  label: string;
  className: string;
  note: string;
} {
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

function hasWebScheme(value: string): boolean {
  try {
    const parsed = new URL(value);
    return (
      (parsed.protocol === "http:" || parsed.protocol === "https:") &&
      Boolean(parsed.host)
    );
  } catch {
    return false;
  }
}

function matchesProvider(provider: ProviderRow, query: string): boolean {
  if (!query) {
    return true;
  }
  const fields: Array<string | null | undefined> = [
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

function matchesSecret(secret: SecretRow, query: string): boolean {
  if (!query) {
    return true;
  }
  const fields: Array<string | null | undefined> = [
    secret.name,
    secret.state,
    secret.source,
  ].concat(Array.isArray(secret.used_by) ? secret.used_by : []);
  return fields
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase()
    .includes(query);
}

export interface SettingsTabsProps {
  active: string;
}

export function SettingsTabs({ active }: SettingsTabsProps) {
  function handleKeyDown(event: KeyboardEvent<HTMLElement>) {
    if (
      event.key !== "ArrowRight" &&
      event.key !== "ArrowLeft" &&
      event.key !== "Home" &&
      event.key !== "End"
    ) {
      return;
    }
    const tabs = Array.from(
      event.currentTarget.querySelectorAll<HTMLElement>('[role="tab"]')
    );
    if (tabs.length === 0) {
      return;
    }
    const index = tabs.findIndex((tab) => tab === document.activeElement);
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
    const target = tabs[next];
    if (target) {
      target.focus();
    }
  }

  return (
    <div className="settings-head">
      <header className="settings-header">
        <h1 className="settings-title">Settings</h1>
        <p className="settings-subtitle">
          Providers, secrets and the daemon. Agents, skills, MCP and channels have
          their own pages.
        </p>
      </header>
      <nav
        className="settings-tabs"
        role="tablist"
        aria-label="Settings"
        onKeyDown={handleKeyDown}
      >
        {SETTINGS_TABS.map((tab) => (
          <a
            key={tab.key}
            id={"settings-tab-" + tab.key}
            className={"settings-tab" + (active === tab.key ? " is-active" : "")}
            role="tab"
            href={"#/settings/" + tab.key}
            aria-selected={active === tab.key ? "true" : "false"}
            aria-controls={"settings-panel-" + tab.key}
            tabIndex={active === tab.key ? 0 : -1}
          >
            {tab.label}
          </a>
        ))}
      </nav>
    </div>
  );
}

export interface SettingsPanelProps {
  active: string;
  children?: ReactNode;
}

export function SettingsPanel({ active, children }: SettingsPanelProps) {
  return (
    <div
      id={"settings-panel-" + active}
      className="settings-panel"
      role="tabpanel"
      aria-labelledby={"settings-tab-" + active}
      tabIndex={0}
    >
      {children}
    </div>
  );
}

export interface SettingsRestartDialogProps {
  onClose: () => void;
  onRestarted?: () => void;
}

export function SettingsRestartDialog({
  onClose,
  onRestarted,
}: SettingsRestartDialogProps) {
  const [info, setInfo] = useState<DaemonInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
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
      .catch((failure: unknown) => {
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
      await settingsApi.waitForDaemonRestart(info.started_at, (text) =>
        setMessage(text)
      );
      clearSettingsSaved();
      setMessage("The daemon is back.");
      if (onRestarted) {
        onRestarted();
      }
      onClose();
    } catch (failure: unknown) {
      const failureState = failureInfo(failure);
      setError(failureState.message);
      setDetails(failureState.details);
      setMessage("The restart did not finish.");
      setBusy(false);
    }
  }

  const turns =
    info && Array.isArray(info.running_turns) ? info.running_turns : [];
  const supported = !info || info.restart_supported;

  return (
    <Dialog title="Restart daemon?" dismissible={!busy} onClose={onClose}>
      <div className="settings-dialog-form">
        <p className="hint">
          The daemon restarts in place. Every running turn ends and this page
          reconnects when the daemon is back.
        </p>
        {loading ? <Loading label="Checking the daemon…" /> : null}
        {error ? <ErrorNotice message={error} details={details} /> : null}
        {info ? (
          <section
            className="settings-running-turns"
            aria-labelledby="settings-running-turns-title"
          >
            <h3 id="settings-running-turns-title">Running turns</h3>
            {turns.length > 0 ? (
              <ul className="settings-turn-list">
                {turns.map((turn, index) => (
                  <li
                    key={(turn && turn.session_id) || index}
                    className="settings-turn-item"
                  >
                    <span className="settings-turn-title">
                      {(turn && turn.title) || "Untitled session"}
                    </span>
                    <span className="settings-turn-agent">
                      {(turn && turn.agent) || "unknown agent"}
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="hint">No turns are running.</p>
            )}
          </section>
        ) : null}
        {info && !supported ? (
          <p className="settings-warning-text">
            Restarting from the browser is not supported on this platform.
          </p>
        ) : null}
        <p
          className="settings-status"
          role="status"
          aria-live="polite"
          aria-atomic="true"
        >
          {message}
        </p>
        <div className="dialog-actions">
          <button
            type="button"
            className="button-ui button-ui-secondary"
            disabled={busy}
            onClick={onClose}
          >
            Cancel
          </button>
          <button
            type="button"
            className="button-ui button-ui-primary"
            disabled={busy || loading || !info || !supported}
            onClick={confirmRestart}
          >
            {busy ? "Restarting…" : "Restart daemon"}
          </button>
        </div>
      </div>
    </Dialog>
  );
}

export interface SettingsRestartBannerProps {
  restartRequired: boolean;
  onRestarted?: () => void;
}

export function SettingsRestartBanner({
  restartRequired,
  onRestarted,
}: SettingsRestartBannerProps) {
  const [open, setOpen] = useState(false);
  if (!restartRequired) {
    return null;
  }
  const saved = savedAgoLabel();
  return (
    <div className="settings-restart-banner">
      <span className="settings-inline-dot is-warning" aria-hidden="true"></span>
      <p className="settings-restart-text">
        {saved ? "Saved " + saved + ". " : "Saved. "}Restart the daemon for the
        saved changes to take effect.
      </p>
      <button
        type="button"
        className="button-ui button-ui-secondary"
        onClick={() => setOpen(true)}
      >
        Restart daemon
      </button>
      {open ? (
        <SettingsRestartDialog
          onClose={() => setOpen(false)}
          onRestarted={() => {
            setOpen(false);
            if (onRestarted) {
              onRestarted();
            }
          }}
        />
      ) : null}
    </div>
  );
}

interface ProviderDialogProps {
  provider: ProviderRow | null;
  version: string;
  onClose: () => void;
  onSaved?: (text: string) => void;
}

function ProviderDialog({
  provider,
  version,
  onClose,
  onSaved,
}: ProviderDialogProps) {
  const editing = Boolean(provider);
  const [name, setName] = useState(provider ? provider.name : "");
  const [kind, setKind] = useState(provider ? provider.kind : "openai_compat");
  const [baseUrl, setBaseUrl] = useState(provider ? provider.base_url : "");
  const [apiKeyEnv, setApiKeyEnv] = useState(
    provider ? provider.api_key_env || "" : ""
  );
  const [apiKey, setApiKey] = useState("");
  const [testModel, setTestModel] = useState("");
  const [testing, setTesting] = useState(false);
  const [testStatus, setTestStatus] = useState({ tone: "", text: "" });
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const removeButtonRef = useRef<HTMLButtonElement>(null);
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
      const body: ProviderTestRequest = { kind, base_url: base, model };
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
    } catch (failure: unknown) {
      setTestStatus({ tone: "error", text: errorMessage(failure) });
    } finally {
      setTesting(false);
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const value = name.trim();
    if (!editing && !PROVIDER_NAME_RE.test(value)) {
      setError(
        "The name must use lowercase letters, digits, and dashes, for example openai-2."
      );
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
      const body: ProviderWriteRequest = {
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
          "Provider " +
            value +
            " saved. Restart the daemon for the saved changes to take effect."
        );
      }
    } catch (failure: unknown) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setError(
          "The config changed since this list loaded. Close the dialog and reload the list before saving."
        );
        setDetails([]);
      } else if (
        failure instanceof ApiError &&
        failure.code === "connection_failed"
      ) {
        setError(
          failure.message ||
            "The provider connection test failed. Nothing was saved."
        );
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
    if (busy || !provider) {
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
          "Provider " +
            provider.name +
            " removed. Restart the daemon for the saved changes to take effect."
        );
      }
    } catch (failure: unknown) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setError(
          "The config changed since this list loaded. Close the dialog and reload the list before removing."
        );
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

  return (
    <Dialog
      title={editing ? "Edit provider" : "Add provider"}
      dismissible={!busy}
      onClose={onClose}
    >
      <form
        className="settings-dialog-form"
        onSubmit={confirmDelete ? (event) => event.preventDefault() : submit}
      >
        <p className="hint">
          {editing
            ? "Edit the provider table. The config keeps every other table and comment."
            : "Add a provider table. Agents can pick it after the daemon restarts."}
        </p>

        <label className="field">
          <span className="field-label">Name</span>
          <input
            data-autofocus={editing ? undefined : true}
            type="text"
            name="provider-name"
            value={name}
            disabled={editing || busy}
            autoComplete="off"
            spellCheck={false}
            pattern="[a-z0-9-]+"
            required
            aria-describedby="settings-provider-name-hint"
            onChange={(event) => setName(event.target.value)}
          />
          <span id="settings-provider-name-hint" className="hint">
            Lowercase letters, digits, and dashes. The name cannot change after it
            is saved.
          </span>
        </label>

        <label className="field">
          <span className="field-label">Kind</span>
          <select
            name="provider-kind"
            value={kind}
            disabled={busy}
            onChange={(event) => setKind(event.target.value)}
          >
            {PROVIDER_KINDS.map((item) => (
              <option key={item.value} value={item.value}>
                {item.label}
              </option>
            ))}
          </select>
        </label>

        <label className="field">
          <span className="field-label">Base URL</span>
          <input
            data-autofocus={editing ? true : undefined}
            type="url"
            name="provider-base-url"
            value={baseUrl}
            disabled={busy}
            autoComplete="off"
            spellCheck={false}
            placeholder="Provider API endpoint"
            required
            aria-describedby="settings-provider-base-url-hint"
            onChange={(event) => setBaseUrl(event.target.value)}
          />
          <span id="settings-provider-base-url-hint" className="hint">
            The API endpoint, starting with the http or https scheme.
          </span>
        </label>

        <label className="field">
          <span className="field-label">API key variable</span>
          <input
            type="text"
            name="provider-key-env"
            value={apiKeyEnv}
            disabled={busy}
            autoComplete="off"
            spellCheck={false}
            placeholder={defaultProviderEnv(trimmedName) || "OPENAI_API_KEY"}
            aria-describedby="settings-provider-key-env-hint"
            onChange={(event) => setApiKeyEnv(event.target.value)}
          />
          <span id="settings-provider-key-env-hint" className="hint">
            The env name written to config.toml. Defaults to{" "}
            {defaultProviderEnv(trimmedName) || "<NAME>_API_KEY"}.
          </span>
        </label>

        <label className="field">
          <span className="field-label">API key</span>
          <input
            type="password"
            name="provider-api-key"
            value={apiKey}
            disabled={busy}
            autoComplete="new-password"
            spellCheck={false}
            aria-describedby="settings-provider-api-key-hint"
            onChange={(event) => setApiKey(event.target.value)}
          />
          <span id="settings-provider-api-key-hint" className="hint">
            {editing
              ? "Leave empty to keep the saved key. A new key is tested before it is written."
              : "Stored write-only in secrets.env. Leave empty if this provider does not need a key."}
          </span>
        </label>

        <section
          className="settings-dialog-section"
          aria-labelledby="settings-provider-test-title"
        >
          <h3 id="settings-provider-test-title">Connection test</h3>
          <p className="hint">
            The test sends one small request. The model is used for the test only
            and is not saved.
          </p>
          <label className="field">
            <span className="field-label">Test model</span>
            <input
              type="text"
              name="provider-test-model"
              value={testModel}
              disabled={busy || testing}
              autoComplete="off"
              spellCheck={false}
              placeholder="gpt-5-mini"
              onChange={(event) => setTestModel(event.target.value)}
            />
          </label>
          <div className="form-actions">
            <button
              type="button"
              className="button-ui button-ui-secondary"
              disabled={busy || testing}
              onClick={testConnection}
            >
              {testing ? "Testing…" : "Test connection"}
            </button>
          </div>
          <p
            className={
              "settings-test-result" +
              (testStatus.tone ? " is-" + testStatus.tone : "")
            }
            role="status"
            aria-live="polite"
          >
            {testStatus.text}
          </p>
        </section>

        {error ? <ErrorNotice message={error} details={details} /> : null}

        {confirmDelete ? (
          <div className="settings-confirm">
            <p>
              Remove <strong>{provider ? provider.name : ""}</strong>? This cannot
              be undone. An agent that uses this provider must be changed first.
            </p>
            <div className="dialog-actions">
              <button
                id="settings-provider-remove-cancel"
                type="button"
                className="button-ui button-ui-secondary"
                disabled={busy}
                onClick={() => setConfirmDelete(false)}
              >
                Cancel
              </button>
              <button
                type="button"
                className="button-ui settings-danger-button"
                disabled={busy}
                onClick={removeProvider}
              >
                {busy ? "Removing…" : "Remove provider"}
              </button>
            </div>
          </div>
        ) : (
          <div className="dialog-actions">
            {editing ? (
              <button
                ref={removeButtonRef}
                type="button"
                className="button-ui button-ui-tertiary"
                disabled={busy}
                onClick={() => setConfirmDelete(true)}
              >
                Remove provider
              </button>
            ) : null}
            <span className="settings-dialog-spacer"></span>
            <button
              type="button"
              className="button-ui button-ui-secondary"
              disabled={busy}
              onClick={onClose}
            >
              Cancel
            </button>
            <button
              type="submit"
              className="button-ui button-ui-primary"
              disabled={busy}
            >
              {busy ? "Saving…" : "Save provider"}
            </button>
          </div>
        )}
      </form>
    </Dialog>
  );
}

export function SettingsProvidersPage() {
  const [data, setData] = useState<ListProvidersResponse | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [message, setMessage] = useState("");
  const [query, setQuery] = useState("");
  const [dialog, setDialog] = useState<{ provider: ProviderRow | null } | null>(
    null
  );
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
      .catch((failure: unknown) => {
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

  const providers = useMemo(
    () => (data && Array.isArray(data.providers) ? data.providers : []),
    [data]
  );
  const search = query.trim().toLowerCase();
  const visible = useMemo(
    () => providers.filter((provider) => matchesProvider(provider, search)),
    [providers, search]
  );

  function handleSaved(text: string) {
    setDialog(null);
    setMessage(text);
    reload();
  }

  function openRow(
    event: MouseEvent<HTMLTableRowElement>,
    provider: ProviderRow
  ) {
    const target = event.target;
    if (
      target instanceof Element &&
      target.closest("a, button, input, select, textarea, label")
    ) {
      return;
    }
    setDialog({ provider });
  }

  return (
    <main id="main" className="page settings-page">
      <SettingsTabs active="providers" />
      <SettingsPanel active="providers">
        <SettingsRestartBanner
          restartRequired={Boolean(data && data.restart_required)}
          onRestarted={reload}
        />

        <div className="settings-toolbar">
          <div className="sessions-search settings-search">
            <span className="sessions-search-icon">
              <Icon name="search" />
            </span>
            <label className="sr-only" htmlFor="settings-providers-search">
              Search providers
            </label>
            <input
              id="settings-providers-search"
              className="sessions-search-input"
              type="search"
              placeholder="Search providers"
              autoComplete="off"
              spellCheck={false}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </div>
          <span className="settings-toolbar-spacer"></span>
          <button
            type="button"
            className="button-ui button-ui-primary"
            disabled={!data || !data.version || state === "loading"}
            title={
              data && !data.version
                ? "config.toml is missing. Run lana init first."
                : undefined
            }
            onClick={() => setDialog({ provider: null })}
          >
            + Add provider
          </button>
        </div>

        <StatusLine text={message} />
        {error ? <ErrorNotice message={error} details={details} /> : null}
        {state === "error" ? (
          <div className="form-actions">
            <button
              type="button"
              className="button-ui button-ui-secondary"
              onClick={reload}
            >
              Reload providers
            </button>
          </div>
        ) : null}
        {state === "loading" ? (
          <Loading label="Loading providers…" />
        ) : state === "ready" && visible.length === 0 ? (
          <div className="settings-empty">
            <p className="settings-empty-title">
              {providers.length === 0
                ? "No providers configured"
                : "No providers match “" + query.trim() + "”"}
            </p>
            <p className="settings-empty-text">
              {providers.length === 0
                ? "Add a provider to let agents send requests to a model."
                : "Try a different name, URL, or kind."}
            </p>
          </div>
        ) : null}

        {state === "ready" && visible.length > 0 ? (
          <div className="settings-table-wrap">
            <table className="settings-table settings-providers-table">
              <caption className="sr-only">Providers</caption>
              <thead>
                <tr>
                  <th scope="col">Provider</th>
                  <th scope="col">Kind</th>
                  <th scope="col">API key</th>
                  <th scope="col">Used by</th>
                  <th scope="col">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {visible.map((provider) => {
                  const key = providerKeyState(provider);
                  return (
                    <tr
                      key={provider.name}
                      className="settings-clickable-row"
                      onClick={(event) => openRow(event, provider)}
                    >
                      <td>
                        <span className="settings-name-cell">
                          <span className="settings-initial" aria-hidden="true">
                            {providerInitial(provider.name)}
                          </span>
                          <span className="settings-name-text">
                            <span className="settings-name">
                              {provider.name}
                            </span>
                            <span className="settings-sub">
                              {provider.base_url}
                            </span>
                          </span>
                        </span>
                      </td>
                      <td>
                        <span className="badge-ui">
                          <span className="badge-ui-dot" aria-hidden="true"></span>
                          {provider.kind}
                        </span>
                      </td>
                      <td>
                        <span className="settings-key-cell">
                          {provider.api_key_env ? (
                            <>
                              <code className="settings-key-env">
                                {provider.api_key_env}
                              </code>
                              <span
                                className={
                                  "settings-key-state " + key.className
                                }
                              >
                                <span
                                  className="settings-dot"
                                  aria-hidden="true"
                                ></span>
                                {key.label}
                                {key.note ? (
                                  <span className="settings-key-note">
                                    {key.note}
                                  </span>
                                ) : null}
                              </span>
                            </>
                          ) : (
                            <span className="settings-key-state is-none">
                              <span
                                className="settings-dot"
                                aria-hidden="true"
                              ></span>
                              No key needed
                            </span>
                          )}
                        </span>
                      </td>
                      <td>
                        <ProviderUsers usedBy={provider.used_by} />
                      </td>
                      <td className="settings-row-action">
                        <button
                          type="button"
                          className="icon-button"
                          aria-label={"Edit " + provider.name}
                          onClick={() => setDialog({ provider })}
                        >
                          <Icon name="chevron-right" />
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}

        <p className="settings-footer-note">
          Agents pick a provider and model on their General tab. Keys live in{" "}
          <a href="#/settings/secrets">Secrets</a>.
        </p>

        {dialog ? (
          <ProviderDialog
            provider={dialog.provider}
            version={data ? data.version : ""}
            onClose={() => setDialog(null)}
            onSaved={handleSaved}
          />
        ) : null}
      </SettingsPanel>
    </main>
  );
}

interface SecretDialogProps {
  secret: SecretRow | null;
  version: string;
  onClose: () => void;
  onSaved?: (text: string) => void;
}

function SecretDialog({ secret, version, onClose, onSaved }: SecretDialogProps) {
  const editing = Boolean(secret);
  const [name, setName] = useState(secret ? secret.name : "");
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const secretName = secret ? secret.name : name.trim();
    if (!SECRET_NAME_RE.test(secretName)) {
      setError(
        "The name must match [A-Za-z_][A-Za-z0-9_]*, for example OPENAI_API_KEY."
      );
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
          "Secret " +
            secretName +
            " saved. Restart the daemon for the saved changes to take effect."
        );
      }
    } catch (failure: unknown) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setError(
          "The secrets file changed since this list loaded. Close the dialog and reload the list before saving."
        );
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

  return (
    <Dialog
      title={editing ? "Replace secret" : "New secret"}
      dismissible={!busy}
      onClose={onClose}
    >
      <form className="settings-dialog-form" onSubmit={submit}>
        {editing ? (
          <p className="settings-secret-target">
            Replacing <code>{secret ? secret.name : ""}</code>. The old value is
            removed.
          </p>
        ) : (
          <label className="field">
            <span className="field-label">Name</span>
            <input
              data-autofocus={true}
              type="text"
              name="secret-name"
              value={name}
              disabled={busy}
              autoComplete="off"
              spellCheck={false}
              pattern="[A-Za-z_][A-Za-z0-9_]*"
              required
              aria-describedby="settings-secret-name-hint"
              onChange={(event) => setName(event.target.value)}
            />
            <span id="settings-secret-name-hint" className="hint">
              Letters, digits, and underscores; must not start with a digit.
            </span>
          </label>
        )}

        <label className="field">
          <span className="field-label">{editing ? "New value" : "Value"}</span>
          <input
            data-autofocus={editing ? true : undefined}
            type="password"
            name="secret-value"
            value={value}
            disabled={busy}
            autoComplete="new-password"
            spellCheck={false}
            required
            aria-describedby="settings-secret-value-hint"
            onChange={(event) => setValue(event.target.value)}
          />
          <span id="settings-secret-value-hint" className="hint">
            Values are write-only. The value is never shown again after saving.
          </span>
        </label>

        {error ? <ErrorNotice message={error} details={details} /> : null}

        <div className="dialog-actions">
          <button
            type="button"
            className="button-ui button-ui-secondary"
            disabled={busy}
            onClick={onClose}
          >
            Cancel
          </button>
          <button
            type="submit"
            className="button-ui button-ui-primary"
            disabled={busy}
          >
            {busy ? "Saving…" : editing ? "Replace secret" : "Save secret"}
          </button>
        </div>
      </form>
    </Dialog>
  );
}

interface RemoveSecretDialogProps {
  secret: SecretRow;
  version: string;
  onClose: () => void;
  onRemoved?: (text: string) => void;
}

function RemoveSecretDialog({
  secret,
  version,
  onClose,
  onRemoved,
}: RemoveSecretDialogProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

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
          "Secret " +
            secret.name +
            " removed. Restart the daemon for the saved changes to take effect."
        );
      }
    } catch (failure: unknown) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setError(
          "The secrets file changed since this list loaded. Close the dialog and reload the list before removing."
        );
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

  return (
    <Dialog
      title={"Remove " + secret.name + "?"}
      dismissible={!busy}
      onClose={onClose}
    >
      <div className="settings-dialog-form">
        <p>
          This removes the value from <code>secrets.env</code>. An agent or
          channel that uses this secret will fail until it is set again.
        </p>
        {error ? <ErrorNotice message={error} details={details} /> : null}
        <div className="dialog-actions">
          <button
            data-autofocus={true}
            type="button"
            className="button-ui button-ui-secondary"
            disabled={busy}
            onClick={onClose}
          >
            Cancel
          </button>
          <button
            type="button"
            className="button-ui settings-danger-button"
            disabled={busy}
            onClick={remove}
          >
            {busy ? "Removing…" : "Remove secret"}
          </button>
        </div>
      </div>
    </Dialog>
  );
}

export function SettingsSecretsPage() {
  const [data, setData] = useState<ListSecretsResponse | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [message, setMessage] = useState("");
  const [query, setQuery] = useState("");
  const [dialog, setDialog] = useState<{ secret: SecretRow | null } | null>(null);
  const [removeTarget, setRemoveTarget] = useState<SecretRow | null>(null);
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
      .catch((failure: unknown) => {
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

  const secrets = useMemo(
    () => (data && Array.isArray(data.secrets) ? data.secrets : []),
    [data]
  );
  const search = query.trim().toLowerCase();
  const visible = useMemo(
    () => secrets.filter((secret) => matchesSecret(secret, search)),
    [secrets, search]
  );

  function handleSaved(text: string) {
    setDialog(null);
    setMessage(text);
    reload();
  }

  function handleRemoved(text: string) {
    setRemoveTarget(null);
    setMessage(text);
    reload();
  }

  return (
    <main id="main" className="page settings-page">
      <SettingsTabs active="secrets" />
      <SettingsPanel active="secrets">
        <SettingsRestartBanner
          restartRequired={Boolean(data && data.restart_required)}
          onRestarted={reload}
        />

        <div className="settings-write-note">
          <Icon name="lock" />
          <p>
            Values are write-only. After you save one, nobody can read it here
            again. You can replace or remove it, not read it.
          </p>
        </div>

        <div className="settings-toolbar">
          <div className="sessions-search settings-search">
            <span className="sessions-search-icon">
              <Icon name="search" />
            </span>
            <label className="sr-only" htmlFor="settings-secrets-search">
              Search secrets
            </label>
            <input
              id="settings-secrets-search"
              className="sessions-search-input"
              type="search"
              placeholder="Search secrets"
              autoComplete="off"
              spellCheck={false}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </div>
          <span className="settings-toolbar-spacer"></span>
          <button
            type="button"
            className="button-ui button-ui-primary"
            disabled={!data || state === "loading"}
            onClick={() => setDialog({ secret: null })}
          >
            + New secret
          </button>
        </div>

        <StatusLine text={message} />
        {error ? <ErrorNotice message={error} details={details} /> : null}
        {state === "error" ? (
          <div className="form-actions">
            <button
              type="button"
              className="button-ui button-ui-secondary"
              onClick={reload}
            >
              Reload secrets
            </button>
          </div>
        ) : null}
        {state === "loading" ? (
          <Loading label="Loading secrets…" />
        ) : state === "ready" && visible.length === 0 ? (
          <div className="settings-empty">
            <p className="settings-empty-title">
              {secrets.length === 0
                ? "No secrets yet"
                : "No secrets match “" + query.trim() + "”"}
            </p>
            <p className="settings-empty-text">
              {secrets.length === 0
                ? "Add a secret for a provider, a channel, or an MCP server."
                : "Try a different name or usage."}
            </p>
          </div>
        ) : null}

        {state === "ready" && visible.length > 0 ? (
          <div className="settings-table-wrap">
            <table className="settings-table settings-secrets-table">
              <caption className="sr-only">Secrets</caption>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Used by</th>
                  <th scope="col">Updated</th>
                  <th scope="col">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {visible.map((secret) => {
                  const isSet = secret.state === "set";
                  const usedBy = Array.isArray(secret.used_by)
                    ? secret.used_by
                    : [];
                  return (
                    <tr key={secret.name}>
                      <td>
                        <span className="settings-secret-name">
                          <Icon name="lock" />
                          <span className="settings-secret-text">
                            <code>{secret.name}</code>
                            <span className="settings-secret-value">
                              {isSet ? (
                                <>
                                  <span
                                    className="settings-secret-dots"
                                    aria-hidden="true"
                                  >
                                    ••••••••••••
                                  </span>
                                  <span className="settings-secret-state">
                                    Set
                                  </span>
                                </>
                              ) : (
                                <span className="settings-secret-state">
                                  Not set
                                </span>
                              )}
                              {secret.overridden_by_env ? (
                                <span className="badge-ui badge-ui-warning">
                                  From environment
                                </span>
                              ) : null}
                            </span>
                          </span>
                        </span>
                      </td>
                      <td>
                        {usedBy.length > 0 ? (
                          <span className="settings-used-paths">
                            {usedBy.map((path, index) => (
                              <span key={path}>
                                {index > 0 ? ", " : ""}
                                <code>{path}</code>
                              </span>
                            ))}
                          </span>
                        ) : (
                          <span className="settings-muted">Not used</span>
                        )}
                      </td>
                      <td>{agoLabel(secret.updated_at) || "—"}</td>
                      <td className="settings-row-action">
                        <span className="settings-actions">
                          <button
                            type="button"
                            className="button-ui button-ui-tertiary"
                            onClick={() => setDialog({ secret })}
                          >
                            Replace
                          </button>
                          <button
                            type="button"
                            className="button-ui button-ui-tertiary"
                            disabled={!isSet}
                            title={!isSet ? "This secret is not set." : undefined}
                            onClick={() => setRemoveTarget(secret)}
                          >
                            Remove
                          </button>
                        </span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}

        {data && data.version ? (
          <p className="settings-footer-note">
            Secrets are stored in <code>secrets.env</code>. Every write uses the
            version this list loaded.
          </p>
        ) : null}

        {dialog ? (
          <SecretDialog
            secret={dialog.secret}
            version={data ? data.version : ""}
            onClose={() => setDialog(null)}
            onSaved={handleSaved}
          />
        ) : null}
        {removeTarget ? (
          <RemoveSecretDialog
            secret={removeTarget}
            version={data ? data.version : ""}
            onClose={() => setRemoveTarget(null)}
            onRemoved={handleRemoved}
          />
        ) : null}
      </SettingsPanel>
    </main>
  );
}

function configLineNumbers(raw: string): number[] {
  const count = String(raw || "").split("\n").length;
  const lines: number[] = [];
  for (let number = 1; number <= count; number += 1) {
    lines.push(number);
  }
  return lines;
}

export function SettingsConfigPage() {
  const [config, setConfig] = useState<GetConfigResponse | null>(null);
  const [daemon, setDaemon] = useState<DaemonInfo | null>(null);
  const [raw, setRaw] = useState("");
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [errorCode, setErrorCode] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const gutterRef = useRef<HTMLDivElement>(null);

  const load = useCallback(async (announceFailure: boolean) => {
    setState("loading");
    setError("");
    setDetails([]);
    setErrorCode("");
    const results = await Promise.allSettled([
      settingsApi.getConfig(),
      settingsApi.getDaemon(),
    ]);
    let failure: unknown = null;
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
    void load(false);
  }, [load]);

  const lineNumbers = useMemo(() => configLineNumbers(raw), [raw]);
  const missing = Boolean(config && !config.version);
  const validation: ConfigValidation =
    config && config.validation ? config.validation : { ok: true, errors: [] };
  const validationErrors: ConfigIssue[] = Array.isArray(validation.errors)
    ? validation.errors
    : [];

  function syncGutter(event: UIEvent<HTMLTextAreaElement>) {
    const gutter = gutterRef.current;
    if (gutter) {
      gutter.scrollTop = event.currentTarget.scrollTop;
    }
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    if (!config) {
      return;
    }
    if (!config.version) {
      setError(
        "config.toml is missing. Run lana init to create it; the web view does not create the file."
      );
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
    } catch (failure: unknown) {
      const failureState = failureInfo(failure);
      if (failureState.code === "validation_failed") {
        setError("The config was not saved because validation failed.");
        setMessage("Config not saved. Validation failed.");
      } else if (failureState.code === "stale_version") {
        setError(
          "The file changed on disk since it was loaded. Reload before saving."
        );
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

  const path =
    daemon && daemon.home ? daemon.home + "/config.toml" : "config.toml";

  return (
    <main id="main" className="page settings-page">
      <SettingsTabs active="config" />
      <SettingsPanel active="config">
        <SettingsRestartBanner
          restartRequired={Boolean(config && config.restart_required)}
          onRestarted={() => void load(false)}
        />

        {state === "loading" && !config ? (
          <Loading label="Loading config.toml…" />
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
              Reload config
            </button>
          </div>
        ) : null}
        <StatusLine text={message} />
        {missing ? (
          <p className="notice notice-warn">
            config.toml is missing. Run <code>lana init</code> to create it; the
            web view does not create the file.
          </p>
        ) : null}

        <form className="settings-config-form" onSubmit={save}>
          <div className="settings-config-head">
            <p className="settings-config-path">
              <code>{path}</code>{" "}
              <span className="settings-config-note">
                · validated on save · comments kept · secret values show as ***
              </span>
            </p>
            <button
              type="submit"
              className="button-ui button-ui-primary"
              disabled={busy || missing || !config || !raw.trim()}
            >
              {busy ? "Saving…" : "Save config.toml"}
            </button>
          </div>

          <div className="settings-editor">
            <div className="settings-gutter" aria-hidden="true" ref={gutterRef}>
              {lineNumbers.map((number) => (
                <span key={number} className="settings-gutter-line">
                  {number}
                </span>
              ))}
            </div>
            <label className="sr-only" htmlFor="settings-config-raw">
              config.toml contents
            </label>
            <textarea
              id="settings-config-raw"
              className="settings-editor-input"
              wrap="off"
              spellCheck={false}
              value={raw}
              disabled={busy || missing || !config}
              onChange={(event) => setRaw(event.target.value)}
              onScroll={syncGutter}
            ></textarea>
          </div>
        </form>

        {validationErrors.length > 0 ? (
          <div className="error-box settings-validation">
            <p className="error-message">Validation errors</p>
            <ErrorDetails details={validationErrors} />
          </div>
        ) : null}
        {errorCode === "stale_version" ? (
          <div className="form-actions">
            <button
              type="button"
              className="button-ui button-ui-secondary"
              disabled={busy}
              onClick={() => void load(true)}
            >
              Reload from disk
            </button>
          </div>
        ) : null}
        {config && config.version ? (
          <p className="settings-footer-note">
            Optimistic concurrency: saving sends this file's current version.
          </p>
        ) : null}
      </SettingsPanel>
    </main>
  );
}

function auditTimeLabel(value: unknown): string {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) {
    return "—";
  }
  return new Date(number).toLocaleString();
}

function auditTimeISO(value: unknown): string | undefined {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) {
    return undefined;
  }
  return new Date(number).toISOString();
}

export function SettingsAuditPage() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [nextBefore, setNextBefore] = useState<number | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [message, setMessage] = useState("");
  const [restartRequired, setRestartRequired] = useState(false);

  const loadRestartState = useCallback(() => {
    settingsApi
      .getConfig()
      .then((value) => {
        setRestartRequired(Boolean(value && value.restart_required));
      })
      .catch(() => {
        /* The banner is optional; the audit list still works. */
      });
  }, []);

  const loadPage = useCallback(async (before: number | null, append: boolean) => {
    if (append) {
      setLoadingMore(true);
    } else {
      setState("loading");
    }
    setError("");
    setDetails([]);
    setMessage(append ? "Loading more entries…" : "Loading the audit log…");
    try {
      const page = await settingsApi.listAudit(before ?? undefined, 50);
      const rows = page && Array.isArray(page.entries) ? page.entries : [];
      setEntries((current) => (append ? current.concat(rows) : rows));
      const cursor = page ? Number(page.next_before) : 0;
      setNextBefore(Number.isFinite(cursor) && cursor > 0 ? cursor : null);
      setState("ready");
      setMessage(
        append
          ? "Loaded " +
              rows.length +
              " more " +
              (rows.length === 1 ? "entry." : "entries.")
          : "Loaded " +
              rows.length +
              " " +
              (rows.length === 1 ? "entry." : "entries.")
      );
    } catch (failure: unknown) {
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
    void loadPage(null, false);
  }, [loadPage, loadRestartState]);

  return (
    <main id="main" className="page settings-page">
      <SettingsTabs active="audit" />
      <SettingsPanel active="audit">
        <SettingsRestartBanner
          restartRequired={restartRequired}
          onRestarted={() => {
            loadRestartState();
            void loadPage(null, false);
          }}
        />

        <StatusLine text={message} />
        {error ? <ErrorNotice message={error} details={details} /> : null}
        {state === "error" ? (
          <div className="form-actions">
            <button
              type="button"
              className="button-ui button-ui-secondary"
              onClick={() => void loadPage(null, false)}
            >
              Reload audit log
            </button>
          </div>
        ) : null}

        {state === "loading" ? (
          <Loading label="Loading the audit log…" />
        ) : state === "ready" && entries.length === 0 ? (
          <div className="settings-empty">
            <p className="settings-empty-title">No settings changes yet</p>
            <p className="settings-empty-text">
              Saves, restarts, and sign-outs appear here after they happen.
            </p>
          </div>
        ) : null}

        {state === "ready" && entries.length > 0 ? (
          <div className="settings-table-wrap">
            <table className="settings-table settings-audit-table">
              <caption className="sr-only">Settings audit log</caption>
              <thead>
                <tr>
                  <th scope="col">Time</th>
                  <th scope="col">Kind</th>
                  <th scope="col">Target</th>
                  <th scope="col">Changed keys</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((entry, index) => {
                  const changed = Array.isArray(entry.changed_keys)
                    ? entry.changed_keys
                    : [];
                  const iso = auditTimeISO(entry.time);
                  return (
                    <tr key={(entry.time || 0) + ":" + index}>
                      <td className="settings-audit-time">
                        <time dateTime={iso || undefined}>
                          {auditTimeLabel(entry.time)}
                        </time>
                      </td>
                      <td>
                        <span className="badge-ui">{entry.kind}</span>
                      </td>
                      <td>
                        <code>{entry.target || "—"}</code>
                      </td>
                      <td className="settings-audit-keys">
                        {changed.length > 0 ? (
                          changed.map((key, keyIndex) => (
                            <span key={key + ":" + keyIndex}>
                              {keyIndex > 0 ? ", " : ""}
                              <code>{key}</code>
                            </span>
                          ))
                        ) : (
                          <span className="settings-muted">—</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}

        {state === "ready" && entries.length > 0 ? (
          <div className="settings-load-more">
            {nextBefore !== null ? (
              <button
                type="button"
                className="button-ui button-ui-secondary"
                disabled={loadingMore}
                onClick={() => void loadPage(nextBefore, true)}
              >
                {loadingMore ? "Loading…" : "Load more"}
              </button>
            ) : (
              <p className="settings-footer-note">
                That is every entry in the audit file.
              </p>
            )}
          </div>
        ) : null}

        <p className="settings-footer-note">
          Entries are newest first. Each line names the target and the changed
          keys, never a value.
        </p>
      </SettingsPanel>
    </main>
  );
}
