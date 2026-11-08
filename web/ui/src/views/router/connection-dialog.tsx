import { useState } from "react";
import type { FormEvent } from "react";
import * as routerApi from "../../api/router";
import type { ProviderRow, ProviderWriteRequest } from "../../api/router";
import { Button, Field, Input, Modal } from "../../ui/gr8r";
import { Alert, markSettingsSaved } from "../settings-pages";
import { defaultKeyEnv, suggestName } from "./presets";
import type { ProviderPreset } from "./presets";
import { ProviderLogo } from "./provider-logo";
import { failureOf, isStale } from "./router-common";

/* One [providers.<name>] entry, added or edited from a provider's page
   (API-RTR-006). Adding to a preset is one short form: paste the key and
   save; the daemon tests a new key before it writes anything. The name, base
   URL, and key variable sit under Advanced with working defaults. */

const NAME_RE = /^[a-z0-9-]+$/;
const ENV_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

function hasWebScheme(value: string): boolean {
  try {
    const parsed = new URL(value);
    return (parsed.protocol === "http:" || parsed.protocol === "https:") && Boolean(parsed.host);
  } catch {
    return false;
  }
}

export interface ConnectionDialogProps {
  /* The card the connection belongs to; absent for a custom compatible
     provider. */
  preset?: ProviderPreset | null;
  /* The entry being edited; absent when adding. */
  connection?: ProviderRow | null;
  version: string;
  takenNames: string[];
  onClose: () => void;
  onSaved: (text: string, restartRequired: boolean, name: string) => void;
}

export function ConnectionDialog({
  preset,
  connection,
  version,
  takenNames,
  onClose,
  onSaved,
}: ConnectionDialogProps) {
  const editing = Boolean(connection);
  const custom = !preset;
  const kind = connection ? connection.kind : preset ? preset.kind : "openai_compat";
  const presetBase = preset ? preset.baseUrl : "";
  /* A preset entry saved on its default base URL shows an empty field. */
  const defaultBase = connection
    ? connection.default_base_url || (preset && preset.kind === connection.kind ? presetBase : "")
    : presetBase;
  const suggested = preset && !editing ? suggestName(preset.id, takenNames) : "";

  const [name, setName] = useState(connection ? connection.name : "");
  const [apiKey, setApiKey] = useState("");
  const [baseUrl, setBaseUrl] = useState(() => {
    if (!connection) {
      return "";
    }
    return connection.base_url && connection.base_url !== defaultBase ? connection.base_url : "";
  });
  const [keyEnv, setKeyEnv] = useState(connection ? connection.api_key_env || "" : "");
  const [testModel, setTestModel] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  const effectiveName = editing ? name : name.trim() || suggested;
  const envPlaceholder = defaultKeyEnv(effectiveName) || "MY_PROVIDER_API_KEY";
  /* Custom entries and compat presets always carry their base URL. */
  const needsBase = kind === "openai_compat";
  const advancedOpen = Boolean(baseUrl || (editing && keyEnv && keyEnv !== defaultKeyEnv(name)));

  function fail(text: string) {
    setError(text);
    setDetails([]);
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const value = effectiveName.trim();
    if (!editing && !NAME_RE.test(value)) {
      fail(
        custom
          ? "Name the provider with lowercase letters, digits, and dashes, for example local-llm."
          : "The connection name must use lowercase letters, digits, and dashes, for example openai-2."
      );
      return;
    }
    if (!editing && takenNames.includes(value)) {
      fail("A provider named " + value + " already exists. Pick another name.");
      return;
    }
    const base = baseUrl.trim() || (needsBase ? defaultBase : "");
    if (needsBase && !base) {
      fail("Enter the base URL, for example http://127.0.0.1:11434/v1.");
      return;
    }
    if (base && !hasWebScheme(base)) {
      fail("The base URL must start with http:// or https://.");
      return;
    }
    const env = keyEnv.trim() || defaultKeyEnv(value);
    if (env && !ENV_RE.test(env)) {
      fail("The key variable must match [A-Za-z_][A-Za-z0-9_]*, for example GROQ_API_KEY.");
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
      /* A custom server is checked with the model the owner named before
         the save, since the daemon cannot know one for it. */
      if (custom && !editing && testModel.trim() && apiKey.trim()) {
        const result = await routerApi.testProvider({
          name: value,
          kind,
          base_url: base,
          model: testModel.trim(),
          api_key: apiKey.trim(),
        });
        if (!result || !result.ok) {
          let text = (result && result.message) || "The server did not answer.";
          if (result && Number(result.status) > 0) {
            text += " (HTTP " + result.status + ")";
          }
          fail("The connection test failed: " + text + " Nothing was saved.");
          return;
        }
      }
      const body: ProviderWriteRequest = { kind, expected_version: version };
      if (base) {
        body.base_url = base;
      }
      if (env) {
        body.api_key_env = env;
      }
      if (apiKey.trim()) {
        body.api_key = apiKey.trim();
      }
      const result = await routerApi.saveProvider(value, body);
      markSettingsSaved();
      onSaved(
        (editing ? "Connection " : custom ? "Provider " : "Connection ") + value + " saved.",
        Boolean(result && result.restart_required),
        value
      );
    } catch (failure) {
      if (isStale(failure)) {
        fail("The config changed since this page loaded. Close the dialog and reload the page before saving.");
      } else {
        const info = failureOf(failure);
        if (info.code === "connection_failed") {
          fail((info.message || "The connection test failed.") + " Check the key. Nothing was saved.");
        } else {
          setError(info.message);
          setDetails(info.details);
        }
      }
    } finally {
      setBusy(false);
    }
  }

  const title = editing
    ? "Edit connection " + name
    : custom
      ? "Add OpenAI-compatible provider"
      : "Connect " + (preset ? preset.label : "");

  return (
    <Modal
      title={
        <span className="rt-dialog-title">
          {preset ? <ProviderLogo logo={preset.logo} name={preset.label} size={24} /> : null}
          <span>{title}</span>
        </span>
      }
      subtitle={
        custom && !editing
          ? "Any server that speaks the OpenAI chat API: Ollama, vLLM, LM Studio, a company gateway."
          : undefined
      }
      dismissible={!busy}
      onClose={onClose}
      footer={
        <>
          <span className="sp" />
          <Button variant="secondary" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="rt-connection-form" variant="primary" disabled={busy} loading={busy}>
            {busy ? (apiKey.trim() ? "Testing the key…" : "Saving…") : "Save"}
          </Button>
        </>
      }
    >
      <form id="rt-connection-form" className="settings-dialog-form" onSubmit={submit}>
        {custom && !editing ? (
          <>
            <Field label="Name" hint="Agents and combos call its models as name/model.">
              <Input
                data-autofocus
                name="connection-name"
                value={name}
                disabled={busy}
                autoComplete="off"
                spellCheck={false}
                placeholder="local-llm"
                onChange={(event) => setName(event.target.value)}
              />
            </Field>
            <Field label="Base URL" hint="The API root that ends before /chat/completions.">
              <Input
                type="url"
                name="connection-base-url"
                value={baseUrl}
                disabled={busy}
                autoComplete="off"
                spellCheck={false}
                placeholder="http://127.0.0.1:11434/v1"
                onChange={(event) => setBaseUrl(event.target.value)}
              />
            </Field>
          </>
        ) : null}

        <Field
          label="API key"
          hint={
            editing ? (
              "Write-only. Leave empty to keep the saved key."
            ) : preset ? (
              <>
                Stored in secrets.env and never shown again.{" "}
                <a href={preset.keysUrl} target="_blank" rel="noreferrer">
                  Get a key from {preset.label}
                </a>
              </>
            ) : (
              "Optional for a local server. Stored in secrets.env and never shown again."
            )
          }
        >
          <Input
            data-autofocus={custom && !editing ? undefined : true}
            type="password"
            name="connection-api-key"
            value={apiKey}
            disabled={busy}
            autoComplete="new-password"
            spellCheck={false}
            placeholder={editing ? "Unchanged" : "Paste the key"}
            onChange={(event) => setApiKey(event.target.value)}
          />
        </Field>

        {custom && !editing ? (
          <Field label="Test model (optional)" hint="Checks the key with this model before saving.">
            <Input
              name="connection-test-model"
              className="mono"
              value={testModel}
              disabled={busy}
              autoComplete="off"
              spellCheck={false}
              placeholder="llama3.2"
              onChange={(event) => setTestModel(event.target.value)}
            />
          </Field>
        ) : null}

        <details className="rt-advanced" open={advancedOpen || undefined}>
          <summary>Advanced</summary>
          <div className="rt-advanced-b">
            {!custom && !editing ? (
              <Field label="Connection name" hint="Agents and combos call its models as name/model.">
                <Input
                  name="connection-name"
                  value={name}
                  disabled={busy}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder={suggested}
                  onChange={(event) => setName(event.target.value)}
                />
              </Field>
            ) : null}
            {!(custom && !editing) ? (
              <Field
                label="Base URL"
                hint={defaultBase ? "Leave empty to use " + defaultBase + "." : "The API root, starting with http or https."}
              >
                <Input
                  type="url"
                  name="connection-base-url"
                  value={baseUrl}
                  disabled={busy}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder={defaultBase || "https://example.com/v1"}
                  onChange={(event) => setBaseUrl(event.target.value)}
                />
              </Field>
            ) : null}
            <Field label="Key variable" hint={"The env name written to config.toml. Defaults to " + envPlaceholder + "."}>
              <Input
                name="connection-key-env"
                className="mono"
                value={keyEnv}
                disabled={busy}
                autoComplete="off"
                spellCheck={false}
                placeholder={envPlaceholder}
                onChange={(event) => setKeyEnv(event.target.value)}
              />
            </Field>
          </div>
        </details>

        <Alert tone="danger" message={error} details={details} />
      </form>
    </Modal>
  );
}
