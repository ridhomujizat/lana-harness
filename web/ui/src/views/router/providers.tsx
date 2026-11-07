/* eslint-disable react-hooks/set-state-in-effect -- the list resets its load state before each request, as the settings pages do. */
import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent, MouseEvent } from "react";
import * as routerApi from "../../api/router";
import type {
  ListProvidersResponse,
  ProviderRow,
  ProviderTestRequest,
  ProviderWriteRequest,
} from "../../api/router";
import { avatarFor } from "../../lib/avatar";
import {
  AvatarStack,
  Avatar,
  Badge,
  Button,
  EmptyState,
  Field,
  IconButton,
  Input,
  Modal,
  SearchInput,
  Select,
  Skeleton,
} from "../../ui/gr8r";
import type { BadgeTone } from "../../ui/gr8r";
import { Alert, SettingsRestartBanner, StatusLine, markSettingsSaved } from "../settings-pages";
import {
  KIND_PRESETS,
  failureOf,
  inUseText,
  isStale,
  kindLabel,
  kindPreset,
} from "./router-common";

/* Router → Providers (REQ-RTR-014, API-RTR-006). Moved from Settings with the
   kind presets of spec 37. Saves apply live; the restart banner only shows
   when the API says a restart is pending. */

const PROVIDER_NAME_RE = /^[a-z0-9-]+$/;
const SECRET_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

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

/* used_by holds "agents.<name>", "combos.<name>", or bare agent names. */
export function splitUsers(usedBy: string[] | undefined): { agents: string[]; others: string[] } {
  const agents: string[] = [];
  const others: string[] = [];
  for (const raw of Array.isArray(usedBy) ? usedBy : []) {
    const text = String(raw || "");
    if (!text) {
      continue;
    }
    if (text.startsWith("agents.")) {
      agents.push(text.slice("agents.".length));
    } else if (text.includes(".")) {
      const [kind, ...rest] = text.split(".");
      const name = rest.join(".");
      others.push((kind === "combos" ? "combo " : kind + " ") + name);
    } else {
      agents.push(text);
    }
  }
  return { agents, others };
}

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

export function UsedBy({ usedBy }: { usedBy: string[] | undefined }) {
  const { agents, others } = splitUsers(usedBy);
  if (agents.length === 0 && others.length === 0) {
    return <span className="hint">Not used</span>;
  }
  return (
    <span className="rt-used-by" title={agents.concat(others).join(", ")}>
      {agents.length > 0 ? (
        <AvatarStack
          size="sm"
          max={3}
          items={agents.map((name) => ({ name, color: agentColor(name) }))}
        />
      ) : null}
      <span className="sr">{agents.concat(others).join(", ")}</span>
      {others.length > 0 ? (
        <span className="hint trunc" aria-hidden="true">
          {others.join(", ")}
        </span>
      ) : null}
    </span>
  );
}

function keyState(provider: ProviderRow): { label: string; tone: BadgeTone; note: string } {
  if (!provider.api_key_env) {
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
    return (parsed.protocol === "http:" || parsed.protocol === "https:") && Boolean(parsed.host);
  } catch {
    return false;
  }
}

function matchesProvider(provider: ProviderRow, query: string): boolean {
  if (!query) {
    return true;
  }
  return [provider.name, provider.kind, kindLabel(provider.kind), provider.base_url, provider.api_key_env]
    .concat(provider.used_by || [])
    .filter(Boolean)
    .join(" ")
    .toLowerCase()
    .includes(query);
}

interface ProviderDialogProps {
  provider: ProviderRow | null;
  version: string;
  takenNames: string[];
  onClose: () => void;
  onSaved: (text: string, restartRequired: boolean) => void;
}

export function ProviderDialog({
  provider,
  version,
  takenNames,
  onClose,
  onSaved,
}: ProviderDialogProps) {
  const editing = Boolean(provider);
  const [name, setName] = useState(() =>
    provider ? provider.name : takenNames.includes("openai") ? "" : "openai"
  );
  const [kind, setKind] = useState(provider ? provider.kind : "openai");
  /* A preset provider saved with its default base URL shows an empty field
     with the default as the placeholder. */
  const savedDefault = provider ? provider.default_base_url || kindPreset(provider.kind)?.baseUrl || "" : "";
  const initialBase =
    provider && provider.base_url && provider.base_url !== savedDefault ? provider.base_url : "";
  const [baseUrl, setBaseUrl] = useState(initialBase);
  const [apiKeyEnv, setApiKeyEnv] = useState(provider ? provider.api_key_env || "" : "");
  const [apiKey, setApiKey] = useState("");
  const [testModel, setTestModel] = useState("");
  const [testing, setTesting] = useState(false);
  const [testStatus, setTestStatus] = useState({ tone: "", text: "" });
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  const preset = kindPreset(kind);
  const custom = kind === "openai_compat";
  const defaultBase =
    (provider && provider.kind === kind && provider.default_base_url) || (preset ? preset.baseUrl : "");
  const trimmedName = name.trim();
  const envPreview = apiKeyEnv.trim() || (trimmedName ? defaultProviderEnv(trimmedName) : "");
  const effectiveBase = baseUrl.trim() || defaultBase;

  function pickKind(next: string) {
    setKind(next);
    /* A new provider is named after its preset until the owner types a name. */
    if (!editing && (!name || KIND_PRESETS.some((entry) => entry.value === name))) {
      const suggested = next === "openai_compat" ? "" : next;
      setName(suggested && !takenNames.includes(suggested) ? suggested : "");
    }
  }

  async function testConnection() {
    if (testing) {
      return;
    }
    if (!effectiveBase) {
      setTestStatus({ tone: "error", text: "Enter a base URL first." });
      return;
    }
    if (!apiKey.trim() && !SECRET_NAME_RE.test(envPreview)) {
      setTestStatus({ tone: "error", text: "Enter a valid API key variable, for example OPENAI_API_KEY." });
      return;
    }
    setTesting(true);
    setTestStatus({ tone: "", text: "Testing the connection…" });
    try {
      const body: ProviderTestRequest = {
        kind,
        base_url: effectiveBase,
        model: testModel.trim() || (preset ? preset.testModel : "gpt-5-mini"),
      };
      if (apiKey.trim()) {
        body.api_key = apiKey.trim();
      } else {
        body.api_key_env = envPreview;
      }
      const result = await routerApi.testProvider(body);
      if (result && result.ok) {
        setTestStatus({ tone: "ok", text: "Connected · " + (Number(result.latency_ms) || 0) + " ms." });
      } else {
        let text = (result && result.message) || "The provider did not answer.";
        if (result && Number(result.status) > 0) {
          text += " (HTTP " + result.status + ")";
        }
        setTestStatus({ tone: "error", text });
      }
    } catch (failure) {
      setTestStatus({ tone: "error", text: failureOf(failure).message });
    } finally {
      setTesting(false);
    }
  }

  function fail(text: string) {
    setError(text);
    setDetails([]);
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const value = name.trim();
    if (!editing && !PROVIDER_NAME_RE.test(value)) {
      fail("The name must use lowercase letters, digits, and dashes, for example openai-2.");
      return;
    }
    const base = baseUrl.trim();
    if (custom && !base) {
      fail("Enter the base URL. A custom provider has no default.");
      return;
    }
    if (base && !hasWebScheme(base)) {
      fail("The base URL must start with the http or https scheme.");
      return;
    }
    if (envPreview && !SECRET_NAME_RE.test(envPreview)) {
      fail("The API key variable must match [A-Za-z_][A-Za-z0-9_]*.");
      return;
    }
    if (!version) {
      fail("config.toml is missing. Run lana init before adding providers.");
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      const body: ProviderWriteRequest = { kind, expected_version: version };
      if (base) {
        body.base_url = base;
      }
      if (envPreview) {
        body.api_key_env = envPreview;
      }
      if (apiKey.trim()) {
        body.api_key = apiKey.trim();
      }
      const result = await routerApi.saveProvider(value, body);
      markSettingsSaved();
      onSaved("Provider " + value + " saved.", Boolean(result && result.restart_required));
    } catch (failure) {
      if (isStale(failure)) {
        fail("The config changed since this list loaded. Close the dialog and reload the list before saving.");
      } else {
        const info = failureOf(failure);
        setError(info.code === "connection_failed" ? info.message || "The provider connection test failed. Nothing was saved." : info.message);
        setDetails(info.code === "connection_failed" ? [] : info.details);
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
      const result = await routerApi.deleteProvider(provider.name, version);
      markSettingsSaved();
      onSaved("Provider " + provider.name + " deleted.", Boolean(result && result.restart_required));
    } catch (failure) {
      const inUse = inUseText(failure, provider.name);
      if (inUse) {
        setConfirmDelete(false);
        fail(inUse);
      } else if (isStale(failure)) {
        fail("The config changed since this list loaded. Close the dialog and reload the list before deleting.");
      } else {
        const info = failureOf(failure);
        setError(info.message);
        setDetails(info.details);
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
            <Button variant="secondary" disabled={busy} onClick={() => setConfirmDelete(false)}>
              Cancel
            </Button>
            <span className="sp" />
            <Button variant="danger" disabled={busy} loading={busy} onClick={removeProvider}>
              {busy ? "Deleting…" : "Delete provider"}
            </Button>
          </>
        ) : (
          <>
            {editing ? (
              <Button variant="danger-ghost" disabled={busy} onClick={() => setConfirmDelete(true)}>
                Delete provider
              </Button>
            ) : null}
            <span className="sp" />
            <Button variant="secondary" disabled={busy} onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" form="router-provider-form" variant="primary" disabled={busy} loading={busy}>
              {busy ? "Saving…" : "Save provider"}
            </Button>
          </>
        )
      }
    >
      <form
        id="router-provider-form"
        className="settings-dialog-form"
        onSubmit={confirmDelete ? (event) => event.preventDefault() : submit}
      >
        <Field label="Kind" hint={custom ? "Any server that speaks the OpenAI chat API." : undefined}>
          <Select
            aria-label="Kind"
            options={KIND_PRESETS.map((entry) => ({ value: entry.value, label: entry.label }))}
            value={kind}
            disabled={busy}
            onChange={pickKind}
          />
        </Field>

        <Field label="Name" hint="Lowercase letters, digits, and dashes. Agents and combos refer to models as name/model.">
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

        <Field
          label={custom ? "Base URL" : "Base URL (optional)"}
          hint={custom ? "The API endpoint, starting with http or https." : "Leave empty to use the default shown."}
        >
          <Input
            type="url"
            name="provider-base-url"
            value={baseUrl}
            disabled={busy}
            autoComplete="off"
            spellCheck={false}
            placeholder={defaultBase || "https://example.com/v1"}
            required={custom}
            onChange={(event) => setBaseUrl(event.target.value)}
          />
        </Field>

        <Field
          label="API key variable"
          hint={<>The env name written to config.toml. Defaults to {defaultProviderEnv(trimmedName) || "<NAME>_API_KEY"}.</>}
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
              ? "Write-only. Leave empty to keep the saved key."
              : "Write-only: stored in secrets.env and never shown again."
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

        <section className="settings-dialog-section" aria-labelledby="router-provider-test-title">
          <h3 id="router-provider-test-title">Connection test</h3>
          <p className="hint">Sends one small request. The test model is not saved.</p>
          <Field label="Test model">
            <Input
              type="text"
              name="provider-test-model"
              value={testModel}
              disabled={busy || testing}
              autoComplete="off"
              spellCheck={false}
              placeholder={preset ? preset.testModel : "gpt-5-mini"}
              onChange={(event) => setTestModel(event.target.value)}
            />
          </Field>
          <div className="settings-dialog-actions">
            <Button variant="secondary" icon="plug" disabled={busy || testing} onClick={testConnection}>
              {testing ? "Testing…" : "Test connection"}
            </Button>
          </div>
          <p
            className={"settings-test-result" + (testStatus.tone ? " is-" + testStatus.tone : "")}
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
              Delete <strong>{provider ? provider.name : ""}</strong>? This cannot be undone. A
              provider that an agent or a combo uses cannot be deleted.
            </p>
          </div>
        ) : null}
      </form>
    </Modal>
  );
}

export function RouterProviders() {
  const [data, setData] = useState<ListProvidersResponse | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [message, setMessage] = useState("");
  const [query, setQuery] = useState("");
  const [dialog, setDialog] = useState<{ provider: ProviderRow | null } | null>(null);
  const [reloadToken, setReloadToken] = useState(0);

  const reload = useCallback(() => setReloadToken((current) => current + 1), []);

  useEffect(() => {
    let alive = true;
    setState("loading");
    setError("");
    setDetails([]);
    routerApi
      .listProviders()
      .then((value) => {
        if (alive) {
          setData(value);
          setState("ready");
        }
      })
      .catch((failure: unknown) => {
        if (alive) {
          const info = failureOf(failure);
          setError(info.message);
          setDetails(info.details);
          setState("error");
        }
      });
    return () => {
      alive = false;
    };
  }, [reloadToken]);

  const providers = useMemo(() => (data && Array.isArray(data.providers) ? data.providers : []), [data]);
  const search = query.trim().toLowerCase();
  const visible = useMemo(() => providers.filter((provider) => matchesProvider(provider, search)), [providers, search]);

  function handleSaved(text: string, restartRequired: boolean) {
    setDialog(null);
    setMessage(restartRequired ? text + " Restart the daemon for it to take effect." : text + " It applies now.");
    reload();
  }

  function openRow(event: MouseEvent<HTMLDivElement>, provider: ProviderRow) {
    const target = event.target;
    if (target instanceof Element && target.closest("a, button, input, select, textarea, label")) {
      return;
    }
    setDialog({ provider });
  }

  return (
    <div className="rt-providers">
      <SettingsRestartBanner restartRequired={Boolean(data && data.restart_required)} onRestarted={reload} />
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
          title={data && !data.version ? "config.toml is missing. Run lana init first." : undefined}
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
        <div className="panel settings-loading" aria-busy="true">
          <span className="sr">Loading providers…</span>
          {[0, 1, 2].map((index) => (
            <div key={index} className="mini settings-loading-row">
              <Skeleton width={30} height={30} radius={8} />
              <Skeleton width="40%" height={12} />
              <Skeleton width="18%" height={12} />
            </div>
          ))}
        </div>
      ) : state === "ready" && visible.length === 0 ? (
        <div className="panel">
          <EmptyState
            icon={providers.length === 0 ? "plug" : "search-x"}
            size={providers.length === 0 ? undefined : "sm"}
            title={providers.length === 0 ? "No providers yet" : "No providers match “" + query.trim() + "”"}
            text={
              providers.length === 0
                ? "Add OpenAI, DeepSeek, Anthropic, OpenRouter, or any OpenAI-compatible server so agents can reach a model."
                : "Try a different name, URL, or kind."
            }
          />
        </div>
      ) : null}

      {state === "ready" && visible.length > 0 ? (
        <section className="panel settings-providers" aria-label="Providers">
          {visible.map((provider) => {
            const key = keyState(provider);
            const base = provider.base_url || provider.default_base_url || "";
            return (
              <div key={provider.name} className="mini settings-provider" onClick={(event) => openRow(event, provider)}>
                <Avatar name={provider.name} color={agentColor(provider.name)} size="lg" />
                <span className="settings-cell">
                  <span className="settings-cell-t">
                    <span className="settings-name mono">{provider.name}</span>
                    <Badge>{kindLabel(provider.kind)}</Badge>
                  </span>
                  <span className="settings-cell-s mono trunc">
                    {base}
                    {typeof provider.models_count === "number" ? " · " + provider.models_count + " models" : ""}
                  </span>
                </span>
                <span className="settings-provider-key">
                  {provider.api_key_env ? <code className="mono">{provider.api_key_env}</code> : null}
                  <Badge tone={key.tone} dot>
                    {key.label}
                  </Badge>
                  {key.note ? <span className="hint">{key.note}</span> : null}
                </span>
                <span className="settings-provider-users">
                  <UsedBy usedBy={provider.used_by} />
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
        Agents pick a model on their General tab. Keys live in <a href="#/settings/secrets">Secrets</a>.
      </p>

      {dialog ? (
        <ProviderDialog
          provider={dialog.provider}
          version={data ? data.version : ""}
          takenNames={providers.map((provider) => provider.name)}
          onClose={() => setDialog(null)}
          onSaved={handleSaved}
        />
      ) : null}
    </div>
  );
}
