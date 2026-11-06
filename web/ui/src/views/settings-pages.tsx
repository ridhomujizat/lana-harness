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
import { ErrorDetails } from "../ui/ui";
import { avatarFor } from "../lib/avatar";
import { agoLabel } from "./agent-common";
import { Ic } from "../ui/icons";
import {
  Avatar,
  AvatarStack,
  Badge,
  Button,
  EmptyState,
  Field,
  IconButton,
  Input,
  Menu,
  MenuItem,
  Modal,
  SearchInput,
  Select,
  Skeleton,
  Textarea,
} from "../ui/gr8r";
import type { BadgeTone } from "../ui/gr8r";
import { RestartDaemonDialog, useDaemonRestart } from "./restart-dialog";

/* Settings rev 2 (T-034): the shared shell plus Providers, Secrets, Config file,
   and Audit log. General lives in settings-general.js and imports the shell from
   here, so this file must not import settings-general.js. */

const SETTINGS_TABS = [
  { key: "general", label: "General", icon: "sliders-horizontal" },
  { key: "providers", label: "Providers", icon: "plug" },
  { key: "secrets", label: "Secrets", icon: "key-round" },
  { key: "config", label: "Config file", icon: "file-code" },
  { key: "audit", label: "Audit log", icon: "scroll-text" },
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

export function StatusLine({ text }: { text: string }) {
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

/* Gr8r `.alert` in place of the legacy error box: one message plus the detail
   list, with the tone and icon matching Gr8r's state colours. */
export function Alert({
  tone,
  icon = "circle-alert",
  message,
  details,
}: {
  tone: "danger" | "warn" | "ok" | "info";
  icon?: string;
  message?: string;
  details?: unknown[];
}) {
  const hasDetails = Array.isArray(details) && details.length > 0;
  if (!message && !hasDetails) {
    return null;
  }
  return (
    <div
      className={"alert " + tone}
      role={tone === "danger" ? "alert" : undefined}
      aria-live={tone === "danger" ? undefined : "polite"}
    >
      <Ic name={icon} size={15} />
      <div className="grow">
        {message ? <p className="settings-alert-msg">{message}</p> : null}
        <ErrorDetails details={details} />
      </div>
    </div>
  );
}

function LoadingRows({ label }: { label: string }) {
  return (
    <div className="panel settings-loading" aria-busy="true">
      <span className="sr">{label}</span>
      {Array.from({ length: 4 }).map((_, index) => (
        <div key={index} className="mini settings-loading-row">
          <Skeleton width={30} height={30} radius={8} />
          <Skeleton width="40%" height={12} />
          <Skeleton width="18%" height={12} />
        </div>
      ))}
    </div>
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

/* The agent identity colours (lib/avatar.ts) map onto the Gr8r status tokens so
   one agent keeps one colour wherever it appears. */
const AVATAR_COLORS: Record<string, string> = {
  "tone-brand": "var(--violet)",
  "tone-success": "var(--green)",
  "tone-warning": "var(--amber)",
  "tone-error": "var(--red)",
  "tone-gray": "var(--gray)",
};

function agentColor(name: string): string {
  return AVATAR_COLORS[avatarFor(name).colorClass] || "var(--violet)";
}

function ProviderUsers({ usedBy }: { usedBy: string[] }) {
  const names = providerAgents(usedBy);
  if (names.length === 0) {
    return <span className="hint">No agents</span>;
  }
  return (
    <span className="settings-provider-users-inner">
      <AvatarStack
        size="sm"
        max={3}
        items={names.map((name) => ({ name, color: agentColor(name) }))}
      />
      <span className="sr">{names.join(", ")}</span>
    </span>
  );
}

function providerKeyState(provider: ProviderRow): {
  label: string;
  tone: BadgeTone;
  note: string;
} {
  if (!provider || !provider.api_key_env) {
    return { label: "No key needed", tone: "gray", note: "" };
  }
  if (provider.key_state === "overridden_by_env") {
    return { label: "Set", tone: "green", note: "from environment" };
  }
  if (provider.key_state === "set") {
    return { label: "Set", tone: "green", note: "" };
  }
  return { label: "Missing", tone: "amber", note: "" };
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

/* The default model of the connection test, keyed by the base URL host. It is
   only a hint: the test model is never saved. */
function defaultTestModel(baseUrl: string): string {
  const value = String(baseUrl || "").trim();
  let host = value;
  try {
    host = new URL(value).hostname;
  } catch {
    host = value.split("/")[0] || "";
  }
  host = host.toLowerCase();
  if (host === "api.deepseek.com") {
    return "deepseek-flash";
  }
  if (host === "openrouter.ai") {
    return "openai/gpt-5-mini";
  }
  return "gpt-5-mini";
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

/* The Gr8r settings rail: Lana's sections as `.sitem` rows, the active one
   carrying `.on` and aria-current. Arrow keys move focus down the list. */
export function SettingsTabs({ active }: SettingsTabsProps) {
  function handleKeyDown(event: KeyboardEvent<HTMLElement>) {
    if (
      event.key !== "ArrowDown" &&
      event.key !== "ArrowUp" &&
      event.key !== "Home" &&
      event.key !== "End"
    ) {
      return;
    }
    const items = Array.from(
      event.currentTarget.querySelectorAll<HTMLElement>(".sitem")
    );
    if (items.length === 0) {
      return;
    }
    const index = items.findIndex((item) => item === document.activeElement);
    let next = index < 0 ? 0 : index;
    if (event.key === "ArrowDown") {
      next = (next + 1) % items.length;
    } else if (event.key === "ArrowUp") {
      next = (next - 1 + items.length) % items.length;
    } else if (event.key === "Home") {
      next = 0;
    } else {
      next = items.length - 1;
    }
    event.preventDefault();
    const target = items[next];
    if (target) {
      target.focus();
    }
  }

  return (
    <nav className="set-nav" aria-label="Settings" onKeyDown={handleKeyDown}>
      {SETTINGS_TABS.map((tab) => (
        <a
          key={tab.key}
          id={"settings-tab-" + tab.key}
          className={"sitem" + (active === tab.key ? " on" : "")}
          href={"#/settings/" + tab.key}
          aria-current={active === tab.key ? "page" : undefined}
        >
          <Ic name={tab.icon} size={15} />
          <span>{tab.label}</span>
        </a>
      ))}
    </nav>
  );
}

export interface SettingsPanelProps {
  active: string;
  title?: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
}

/* The Gr8r settings body: `.set-body > .set-in` with a `.ph` section header,
   then the section blocks. */
export function SettingsPanel({
  active,
  title,
  description,
  children,
}: SettingsPanelProps) {
  return (
    <div id={"settings-panel-" + active} className="set-body">
      <div className="set-in">
        {title ? (
          <header className="ph">
            <div>
              <h1>{title}</h1>
              {description ? <p>{description}</p> : null}
            </div>
          </header>
        ) : null}
        {children}
      </div>
    </div>
  );
}

export interface SettingsRestartDialogProps {
  onClose: () => void;
  onRestarted?: () => void;
}

/* The restart flow lives in one shared component so Settings > General and the
   Telegram connect step behave the same (REQ-TFB-003, API-TFB-002). */
export function SettingsRestartDialog({
  onClose,
  onRestarted,
}: SettingsRestartDialogProps) {
  const controller = useDaemonRestart({
    onRestarted: () => {
      clearSettingsSaved();
      if (onRestarted) {
        onRestarted();
      }
      onClose();
    },
  });

  return <RestartDaemonDialog controller={controller} onClose={onClose} />;
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
    <div className="alert warn settings-restart-banner">
      <Ic name="triangle-alert" size={15} />
      <p className="settings-restart-text">
        {saved ? "Saved " + saved + ". " : "Saved. "}Restart the daemon for the
        saved changes to take effect.
      </p>
      <Button
        variant="secondary"
        size="sm"
        icon="rotate-cw"
        onClick={() => setOpen(true)}
      >
        Restart daemon
      </Button>
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

  const trimmedName = name.trim();
  const envPreview =
    apiKeyEnv.trim() || (trimmedName ? defaultProviderEnv(trimmedName) : "");

  useEffect(() => {
    if (confirmDelete) {
      const node = document.getElementById("settings-provider-remove-cancel");
      if (node) {
        node.focus();
      }
    }
  }, [confirmDelete]);

  async function testConnection() {
    if (testing) {
      return;
    }
    const base = baseUrl.trim();
    const model = testModel.trim() || defaultTestModel(base);
    if (!base) {
      setTestStatus({ tone: "error", text: "Enter a base URL first." });
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
    <Modal
      title={editing ? "Edit provider" : "Add provider"}
      dismissible={!busy}
      onClose={onClose}
      footer={
        confirmDelete ? (
          <>
            <Button
              id="settings-provider-remove-cancel"
              variant="secondary"
              disabled={busy}
              onClick={() => setConfirmDelete(false)}
            >
              Cancel
            </Button>
            <span className="sp" />
            <Button
              variant="danger"
              disabled={busy}
              loading={busy}
              onClick={removeProvider}
            >
              {busy ? "Removing…" : "Remove provider"}
            </Button>
          </>
        ) : (
          <>
            {editing ? (
              <Button
                variant="danger-ghost"
                disabled={busy}
                onClick={() => setConfirmDelete(true)}
              >
                Remove provider
              </Button>
            ) : null}
            <span className="sp" />
            <Button variant="secondary" disabled={busy} onClick={onClose}>
              Cancel
            </Button>
            <Button
              type="submit"
              form="settings-provider-form"
              variant="primary"
              disabled={busy}
              loading={busy}
            >
              {busy ? "Saving…" : "Save provider"}
            </Button>
          </>
        )
      }
    >
      <form
        id="settings-provider-form"
        className="settings-dialog-form"
        onSubmit={confirmDelete ? (event) => event.preventDefault() : submit}
      >
        <p className="hint">
          {editing
            ? "Edit the provider table. The config keeps every other table and comment."
            : "Add a provider table. Agents can pick it after the daemon restarts."}
        </p>

        <Field
          label="Name"
          hint="Lowercase letters, digits, and dashes. The name cannot change after it is saved."
        >
          <Input
            data-autofocus={editing ? undefined : true}
            type="text"
            name="provider-name"
            value={name}
            disabled={editing || busy}
            autoComplete="off"
            spellCheck={false}
            pattern="[a-z0-9-]+"
            required
            onChange={(event) => setName(event.target.value)}
          />
        </Field>

        <Field label="Kind">
          <Select
            aria-label="Kind"
            options={PROVIDER_KINDS}
            value={kind}
            disabled={busy}
            onChange={setKind}
          />
        </Field>

        <Field
          label="Base URL"
          hint="The API endpoint, starting with the http or https scheme."
        >
          <Input
            data-autofocus={editing ? true : undefined}
            type="url"
            name="provider-base-url"
            value={baseUrl}
            disabled={busy}
            autoComplete="off"
            spellCheck={false}
            placeholder="Provider API endpoint"
            required
            onChange={(event) => setBaseUrl(event.target.value)}
          />
        </Field>

        <Field
          label="API key variable"
          hint={
            <>
              The env name written to config.toml. Defaults to{" "}
              {defaultProviderEnv(trimmedName) || "<NAME>_API_KEY"}.
            </>
          }
        >
          <Input
            type="text"
            name="provider-key-env"
            value={apiKeyEnv}
            disabled={busy}
            autoComplete="off"
            spellCheck={false}
            placeholder={defaultProviderEnv(trimmedName) || "OPENAI_API_KEY"}
            onChange={(event) => setApiKeyEnv(event.target.value)}
          />
        </Field>

        <Field
          label="API key"
          hint={
            editing
              ? "Leave empty to keep the saved key. A new key is tested before it is written."
              : "Stored write-only in secrets.env. Leave empty if this provider does not need a key."
          }
        >
          <Input
            type="password"
            name="provider-api-key"
            value={apiKey}
            disabled={busy}
            autoComplete="new-password"
            spellCheck={false}
            onChange={(event) => setApiKey(event.target.value)}
          />
        </Field>

        <section
          className="settings-dialog-section"
          aria-labelledby="settings-provider-test-title"
        >
          <h3 id="settings-provider-test-title">Connection test</h3>
          <p className="hint">
            The test sends one small request. The model is used for the test only
            and is not saved.
          </p>
          <Field label="Test model">
            <Input
              type="text"
              name="provider-test-model"
              value={testModel}
              disabled={busy || testing}
              autoComplete="off"
              spellCheck={false}
              placeholder={defaultTestModel(baseUrl)}
              onChange={(event) => setTestModel(event.target.value)}
            />
          </Field>
          <div className="settings-dialog-actions">
            <Button
              variant="secondary"
              icon="plug"
              disabled={busy || testing}
              onClick={testConnection}
            >
              {testing ? "Testing…" : "Test connection"}
            </Button>
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

        <Alert tone="danger" message={error} details={details} />

        {confirmDelete ? (
          <div className="settings-confirm">
            <p>
              Remove <strong>{provider ? provider.name : ""}</strong>? This cannot
              be undone. An agent that uses this provider must be changed first.
            </p>
          </div>
        ) : null}
      </form>
    </Modal>
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

  function openRow(event: MouseEvent<HTMLDivElement>, provider: ProviderRow) {
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
      <SettingsPanel
        active="providers"
        title="Providers"
        description="Connections agents use to reach a model."
      >
        <SettingsRestartBanner
          restartRequired={Boolean(data && data.restart_required)}
          onRestarted={reload}
        />

        <div className="settings-toolbar">
          <SearchInput
            wrapClassName="settings-search"
            aria-label="Search providers"
            placeholder="Search providers"
            autoComplete="off"
            spellCheck={false}
            kbd={null}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          <span className="settings-toolbar-spacer" />
          <Button
            variant="primary"
            size="sm"
            icon="plus"
            disabled={!data || !data.version || state === "loading"}
            title={
              data && !data.version
                ? "config.toml is missing. Run lana init first."
                : undefined
            }
            onClick={() => setDialog({ provider: null })}
          >
            Add provider
          </Button>
        </div>

        <StatusLine text={message} />
        <Alert tone="danger" message={error} details={details} />
        {state === "error" ? (
          <div className="settings-actions-bar">
            <Button variant="secondary" onClick={reload}>
              Reload providers
            </Button>
          </div>
        ) : null}
        {state === "loading" ? (
          <LoadingRows label="Loading providers…" />
        ) : state === "ready" && visible.length === 0 ? (
          <div className="panel">
            <EmptyState
              icon={providers.length === 0 ? "plug" : "search-x"}
              size={providers.length === 0 ? undefined : "sm"}
              title={
                providers.length === 0
                  ? "No providers configured"
                  : "No providers match “" + query.trim() + "”"
              }
              text={
                providers.length === 0
                  ? "Add a provider to let agents send requests to a model."
                  : "Try a different name, URL, or kind."
              }
            />
          </div>
        ) : null}

        {state === "ready" && visible.length > 0 ? (
          <section
            className="panel settings-providers"
            aria-label="Providers"
          >
            {visible.map((provider) => {
              const key = providerKeyState(provider);
              return (
                <div
                  key={provider.name}
                  className="mini settings-provider"
                  onClick={(event) => openRow(event, provider)}
                >
                  <Avatar
                    name={provider.name}
                    color={agentColor(provider.name)}
                    size="lg"
                  />
                  <span className="settings-cell">
                    <span className="settings-cell-t">
                      <span className="settings-name mono">
                        {provider.name}
                      </span>
                      <Badge>{provider.kind}</Badge>
                    </span>
                    <span className="settings-cell-s mono">
                      {provider.base_url}
                    </span>
                  </span>
                  <span className="settings-provider-key">
                    {provider.api_key_env ? (
                      <>
                        <code className="mono">{provider.api_key_env}</code>
                        <Badge tone={key.tone} dot>
                          {key.label}
                        </Badge>
                        {key.note ? (
                          <span className="hint">{key.note}</span>
                        ) : null}
                      </>
                    ) : (
                      <Badge tone={key.tone} dot>
                        {key.label}
                      </Badge>
                    )}
                  </span>
                  <span className="settings-provider-users">
                    <ProviderUsers usedBy={provider.used_by} />
                  </span>
                  <span className="settings-row-actions">
                    <IconButton
                      icon="chevron-right"
                      size="sm"
                      label={"Edit " + provider.name}
                      onClick={(event) => {
                        event.stopPropagation();
                        setDialog({ provider });
                      }}
                    />
                  </span>
                </div>
              );
            })}
          </section>
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
    <Modal
      title={editing ? "Replace secret" : "New secret"}
      dismissible={!busy}
      onClose={onClose}
      footer={
        <>
          <span className="sp" />
          <Button variant="secondary" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="settings-secret-form"
            variant="primary"
            disabled={busy}
            loading={busy}
          >
            {busy ? "Saving…" : editing ? "Replace secret" : "Save secret"}
          </Button>
        </>
      }
    >
      <form id="settings-secret-form" className="settings-dialog-form" onSubmit={submit}>
        {editing ? (
          <p className="settings-secret-target">
            Replacing <code className="mono">{secret ? secret.name : ""}</code>.
            The old value is removed.
          </p>
        ) : (
          <Field
            label="Name"
            hint="Letters, digits, and underscores; must not start with a digit."
          >
            <Input
              data-autofocus={true}
              type="text"
              name="secret-name"
              value={name}
              disabled={busy}
              autoComplete="off"
              spellCheck={false}
              pattern="[A-Za-z_][A-Za-z0-9_]*"
              required
              onChange={(event) => setName(event.target.value)}
            />
          </Field>
        )}

        <Field
          label={editing ? "New value" : "Value"}
          hint="Values are write-only. The value is never shown again after saving."
        >
          <Input
            data-autofocus={editing ? true : undefined}
            type="password"
            name="secret-value"
            value={value}
            disabled={busy}
            autoComplete="new-password"
            spellCheck={false}
            required
            onChange={(event) => setValue(event.target.value)}
          />
        </Field>

        <Alert tone="danger" message={error} details={details} />
      </form>
    </Modal>
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
    <Modal
      title={"Remove " + secret.name + "?"}
      dismissible={!busy}
      onClose={onClose}
      footer={
        <>
          <span className="sp" />
          <Button
            data-autofocus={true}
            variant="secondary"
            disabled={busy}
            onClick={onClose}
          >
            Cancel
          </Button>
          <Button
            variant="danger"
            disabled={busy}
            loading={busy}
            onClick={remove}
          >
            {busy ? "Removing…" : "Remove secret"}
          </Button>
        </>
      }
    >
      <div className="settings-dialog-form">
        <p>
          This removes the value from <code className="mono">secrets.env</code>.
          An agent or channel that uses this secret will fail until it is set
          again.
        </p>
        <Alert tone="danger" message={error} details={details} />
      </div>
    </Modal>
  );
}

/* The row action menu: an IconButton that opens the Gr8r popover menu with the
   Replace and Remove items (REQ-WEB-055). */
function RowMenu({
  label,
  onReplace,
  onRemove,
  removeDisabled,
  removeTitle,
}: {
  label: string;
  onReplace: () => void;
  onRemove: () => void;
  removeDisabled?: boolean;
  removeTitle?: string;
}) {
  const [open, setOpen] = useState(false);
  const anchorRef = useRef<HTMLSpanElement>(null);
  return (
    <span className="settings-rowmenu" ref={anchorRef}>
      <IconButton
        icon="ellipsis"
        size="sm"
        label={label}
        onClick={() => setOpen(true)}
      />
      <Menu anchorRef={anchorRef} open={open} onClose={() => setOpen(false)}>
        <MenuItem
          icon="refresh-cw"
          onClick={() => {
            setOpen(false);
            onReplace();
          }}
        >
          Replace
        </MenuItem>
        <MenuItem
          icon="trash-2"
          danger
          disabled={removeDisabled}
          title={removeTitle}
          onClick={() => {
            setOpen(false);
            onRemove();
          }}
        >
          Remove
        </MenuItem>
      </Menu>
    </span>
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
      <SettingsPanel
        active="secrets"
        title="Secrets"
        description="Write-only values providers, channels, and MCP servers read at start."
      >
        <SettingsRestartBanner
          restartRequired={Boolean(data && data.restart_required)}
          onRestarted={reload}
        />

        <div className="alert info settings-write-note">
          <Ic name="lock" size={15} />
          <p>
            Values are write-only. After you save one, nobody can read it here
            again. You can replace or remove it, not read it.
          </p>
        </div>

        <div className="settings-toolbar">
          <SearchInput
            wrapClassName="settings-search"
            aria-label="Search secrets"
            placeholder="Search secrets"
            autoComplete="off"
            spellCheck={false}
            kbd={null}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          <span className="settings-toolbar-spacer" />
          <Button
            variant="primary"
            size="sm"
            icon="plus"
            disabled={!data || state === "loading"}
            onClick={() => setDialog({ secret: null })}
          >
            New secret
          </Button>
        </div>

        <StatusLine text={message} />
        <Alert tone="danger" message={error} details={details} />
        {state === "error" ? (
          <div className="settings-actions-bar">
            <Button variant="secondary" onClick={reload}>
              Reload secrets
            </Button>
          </div>
        ) : null}
        {state === "loading" ? (
          <LoadingRows label="Loading secrets…" />
        ) : state === "ready" && visible.length === 0 ? (
          <div className="panel">
            <EmptyState
              icon={secrets.length === 0 ? "key-round" : "search-x"}
              size={secrets.length === 0 ? undefined : "sm"}
              title={
                secrets.length === 0
                  ? "No secrets yet"
                  : "No secrets match “" + query.trim() + "”"
              }
              text={
                secrets.length === 0
                  ? "Add a secret for a provider, a channel, or an MCP server."
                  : "Try a different name or usage."
              }
            />
          </div>
        ) : null}

        {state === "ready" && visible.length > 0 ? (
          <div className="tbl-wrap">
            <table className="tbl settings-secrets-table">
              <caption className="sr">Secrets</caption>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Used by</th>
                  <th scope="col">Updated</th>
                  <th scope="col" className="settings-col-actions">
                    <span className="sr">Actions</span>
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
                          <Ic name="lock" size={14} />
                          <span className="settings-secret-text">
                            <code className="mono">{secret.name}</code>
                            <span className="settings-secret-value">
                              {isSet ? (
                                <>
                                  <span
                                    className="settings-secret-dots"
                                    aria-hidden="true"
                                  >
                                    ••••••••••••
                                  </span>
                                  <Badge tone="green" dot>
                                    Set
                                  </Badge>
                                </>
                              ) : (
                                <Badge tone="gray" dot>
                                  Not set
                                </Badge>
                              )}
                              {secret.overridden_by_env ? (
                                <Badge tone="amber">From environment</Badge>
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
                                <code className="mono">{path}</code>
                              </span>
                            ))}
                          </span>
                        ) : (
                          <span className="hint">Not used</span>
                        )}
                      </td>
                      <td className="settings-audit-time">
                        {agoLabel(secret.updated_at) || "—"}
                      </td>
                      <td className="settings-row-action">
                        <RowMenu
                          label={"Actions for " + secret.name}
                          onReplace={() => setDialog({ secret })}
                          onRemove={() => setRemoveTarget(secret)}
                          removeDisabled={!isSet}
                          removeTitle={
                            !isSet ? "This secret is not set." : undefined
                          }
                        />
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
            Secrets are stored in <code className="mono">secrets.env</code>.
            Every write uses the version this list loaded.
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
      <SettingsPanel
        active="config"
        title="Config file"
        description="The daemon's config.toml, edited in place."
      >
        <SettingsRestartBanner
          restartRequired={Boolean(config && config.restart_required)}
          onRestarted={() => void load(false)}
        />

        {state === "loading" && !config ? (
          <LoadingRows label="Loading config.toml…" />
        ) : null}
        <Alert tone="danger" message={error} details={details} />
        {state === "error" ? (
          <div className="settings-actions-bar">
            <Button variant="secondary" disabled={busy} onClick={() => void load(true)}>
              Reload config
            </Button>
          </div>
        ) : null}
        <StatusLine text={message} />
        {missing ? (
          <div className="alert warn">
            <Ic name="triangle-alert" size={15} />
            <span>
              config.toml is missing. Run <code className="mono">lana init</code>{" "}
              to create it; the web view does not create the file.
            </span>
          </div>
        ) : null}

        <form className="settings-config-form" onSubmit={save}>
          <div className="settings-config-head">
            <p className="settings-config-path">
              <code className="mono">{path}</code>{" "}
              <span className="settings-config-note">
                · validated on save · comments kept · secret values show as ***
              </span>
            </p>
            <Button
              type="submit"
              variant="primary"
              disabled={busy || missing || !config || !raw.trim()}
              loading={busy}
            >
              {busy ? "Saving…" : "Save config.toml"}
            </Button>
          </div>

          <div className="settings-editor">
            <div className="settings-gutter" aria-hidden="true" ref={gutterRef}>
              {lineNumbers.map((number) => (
                <span key={number} className="settings-gutter-line">
                  {number}
                </span>
              ))}
            </div>
            <Textarea
              id="settings-config-raw"
              className="settings-config-editor mono"
              aria-label="config.toml contents"
              wrap="off"
              spellCheck={false}
              value={raw}
              disabled={busy || missing || !config}
              onChange={(event) => setRaw(event.target.value)}
              onScroll={syncGutter}
            />
          </div>
        </form>

        {validationErrors.length > 0 ? (
          <Alert
            tone="danger"
            message="Validation errors"
            details={validationErrors}
          />
        ) : null}
        {errorCode === "stale_version" ? (
          <div className="settings-actions-bar">
            <Button
              variant="secondary"
              disabled={busy}
              onClick={() => void load(true)}
            >
              Reload from disk
            </Button>
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

function matchesAudit(entry: AuditEntry, query: string): boolean {
  if (!query) {
    return true;
  }
  const keys = Array.isArray(entry.changed_keys) ? entry.changed_keys : [];
  return [entry.kind, entry.target]
    .concat(keys)
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase()
    .includes(query);
}

export function SettingsAuditPage() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [nextBefore, setNextBefore] = useState<number | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [message, setMessage] = useState("");
  const [query, setQuery] = useState("");
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

  const search = query.trim().toLowerCase();
  const visible = useMemo(
    () => entries.filter((entry) => matchesAudit(entry, search)),
    [entries, search]
  );

  return (
    <main id="main" className="page settings-page">
      <SettingsTabs active="audit" />
      <SettingsPanel
        active="audit"
        title="Audit log"
        description="Every settings change the daemon recorded, newest first."
      >
        <SettingsRestartBanner
          restartRequired={restartRequired}
          onRestarted={() => {
            loadRestartState();
            void loadPage(null, false);
          }}
        />

        <div className="settings-toolbar">
          <SearchInput
            wrapClassName="settings-search"
            aria-label="Search audit log"
            placeholder="Search audit log"
            autoComplete="off"
            spellCheck={false}
            kbd={null}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>

        <StatusLine text={message} />
        <Alert tone="danger" message={error} details={details} />
        {state === "error" ? (
          <div className="settings-actions-bar">
            <Button variant="secondary" onClick={() => void loadPage(null, false)}>
              Reload audit log
            </Button>
          </div>
        ) : null}

        {state === "loading" ? (
          <LoadingRows label="Loading the audit log…" />
        ) : state === "ready" && entries.length === 0 ? (
          <div className="panel">
            <EmptyState
              icon="scroll-text"
              title="No settings changes yet"
              text="Saves, restarts, and sign-outs appear here after they happen."
            />
          </div>
        ) : state === "ready" && visible.length === 0 ? (
          <div className="panel">
            <EmptyState
              icon="search-x"
              size="sm"
              title="No entries match"
              text="No audit entries match the current search."
            />
          </div>
        ) : null}

        {state === "ready" && visible.length > 0 ? (
          <div className="tbl-wrap">
            <table className="tbl settings-audit-table">
              <caption className="sr">Settings audit log</caption>
              <thead>
                <tr>
                  <th scope="col">Time</th>
                  <th scope="col">Kind</th>
                  <th scope="col">Target</th>
                  <th scope="col">Changed keys</th>
                </tr>
              </thead>
              <tbody>
                {visible.map((entry, index) => {
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
                        <Badge>{entry.kind}</Badge>
                      </td>
                      <td>
                        <code className="mono">{entry.target || "—"}</code>
                      </td>
                      <td className="settings-audit-keys">
                        {changed.length > 0 ? (
                          changed.map((key, keyIndex) => (
                            <span key={key + ":" + keyIndex}>
                              {keyIndex > 0 ? ", " : ""}
                              <code className="mono">{key}</code>
                            </span>
                          ))
                        ) : (
                          <span className="hint">—</span>
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
              <Button
                variant="secondary"
                loading={loadingMore}
                disabled={loadingMore}
                onClick={() => void loadPage(nextBefore, true)}
              >
                {loadingMore ? "Loading…" : "Load more"}
              </Button>
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
