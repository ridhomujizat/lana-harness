import { useCallback, useEffect, useMemo, useState } from "react";
import type { ClipboardEvent, FormEvent, ReactNode } from "react";
import { errorMessage } from "../api/client";
import { getSetup, testProvider } from "../api/setup";
import type { SetupModel, SetupTestProviderResponse } from "../api/setup";
import * as routerApi from "../api/router";
import type { ClaudeAccount, ClaudeSignIn } from "../api/router";
import { useAnnounce } from "../ui/ui";
import { Ic } from "../ui/icons";
import { Badge, Button, Field, IconButton, Input, Select, Toggle, cn } from "../ui/gr8r";
import type { SelectOption } from "../ui/gr8r";
import { useClaudeAccounts } from "./router/claude-code";
import { SIGN_IN_POLL_MS, SignInCard } from "./router/claude-code-page";
import type { SignInFlow } from "./router/claude-code-page";
import { planLabel } from "./router/limits";
import { CLAUDE_CODE_ID, presetById } from "./router/presets";
import type { ProviderPreset } from "./router/presets";
import { ProviderLogo } from "./router/provider-logo";
import { failureOf } from "./router/router-common";
import { SetupShell, StepHeading, go } from "./setup-shell";
import {
  CLAUDE_CODE_MODELS,
  MAIN_PRESETS,
  MORE_PRESETS,
  addressModel,
  addressProvider,
  isClaudeAddress,
  presetModels,
  readDraft,
  reconcileModels,
  updateDraft,
  validAddress,
} from "./setup-draft";
import type { SetupConnection, SetupDraft } from "./setup-draft";

/* The Router onboarding steps (spec 37 Router, spec 39 Claude Code):
   "Connect your models" connects Claude Code and any number of API key
   providers; "Pick a model" picks the default agent's main model, an
   optional fallback (the default combo), and the background model of a
   Claude Code agent (REQ-CCT-007). */

/* The Claude Code docs, where the owner installs the claude program. */
export const CLAUDE_CODE_DOCS = "https://docs.claude.com/en/docs/claude-code/overview";

export const BACKGROUND_NOTE =
  "Background tasks (session titles, memory, and knowledge) never run on Claude Code. They use an API model.";

export const ROUTER_NOTE = "Change it later in Router → Providers or in the agent's settings.";

const CUSTOM_PREFIX = "custom:";

/* ------------------------------------------------------------------ */
/* Labels                                                              */
/* ------------------------------------------------------------------ */

/* The family label and logo of a provider name of this wizard. */
export function providerFace(name: string): { label: string; logo: string } {
  if (name === CLAUDE_CODE_ID) {
    return { label: "Claude Code", logo: "claude" };
  }
  const preset = presetById(name);
  return preset ? { label: preset.label, logo: preset.logo } : { label: name, logo: "" };
}

/* "OpenAI · gpt-5-mini" with its logo. */
export function AddressLabel({ address }: { address: string }) {
  const provider = addressProvider(address);
  const face = providerFace(provider);
  return (
    <span className="setup-addr">
      <ProviderLogo logo={face.logo} name={face.label} size={16} />
      <span className="trunc">
        {face.label} · <span className="mono">{addressModel(address)}</span>
      </span>
    </span>
  );
}

function providerFailure(result: SetupTestProviderResponse | null): string {
  if (result && typeof result.message === "string" && result.message) {
    return result.message;
  }
  const status = Number(result && result.status);
  if (Number.isFinite(status) && status > 0) {
    return "The provider answered with status " + status + ".";
  }
  return "The provider did not answer.";
}

function isHttpUrl(value: string): boolean {
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:";
  } catch {
    return false;
  }
}

/* ------------------------------------------------------------------ */
/* Claude Code card                                                    */
/* ------------------------------------------------------------------ */

/* The built-in account (~/.claude): the setup signs in only that one. */
function builtinAccount(accounts: ClaudeAccount[]): ClaudeAccount | undefined {
  return accounts.find((account) => account.builtin) || accounts.find((account) => account.name === CLAUDE_CODE_ID);
}

type ClaudeState = "loading" | "unavailable" | "missing" | "signed-out" | "signed-in";

function ClaudeCodeCard({
  selected,
  onSelect,
}: {
  selected: boolean;
  onSelect: (use: boolean, account: ClaudeAccount | undefined) => void;
}) {
  const announce = useAnnounce();
  const { status, accounts, failed, reload } = useClaudeAccounts();
  const [flow, setFlow] = useState<SignInFlow | null>(null);
  const [pollTick, setPollTick] = useState(0);
  const account = builtinAccount(accounts);

  let state: ClaudeState = "loading";
  if (failed) {
    state = "unavailable";
  } else if (status) {
    const installed = Boolean(status.binary || (account && account.binary && account.version));
    state = !installed ? "missing" : account && account.logged_in ? "signed-in" : "signed-out";
  }

  const finish = useCallback(
    async (result: ClaudeSignIn) => {
      if (result.state === "succeeded") {
        setFlow(null);
        const next = await reload();
        const signedIn = next ? builtinAccount(next.accounts) : undefined;
        if (signedIn && signedIn.logged_in) {
          onSelect(true, signedIn);
          announce("Signed in to Claude Code. Claude Code is selected.");
        }
        return;
      }
      setFlow((current) =>
        current
          ? {
              ...current,
              phase: result.state === "cancelled" ? "cancelled" : "failed",
              error: result.state === "cancelled" ? "" : result.error || "The sign-in stopped. Try again.",
            }
          : current
      );
    },
    [announce, onSelect, reload]
  );

  /* Poll the running sign-in each SIGN_IN_POLL_MS. */
  const running = Boolean(flow && flow.phase === "running");
  useEffect(() => {
    if (!running) {
      return undefined;
    }
    let alive = true;
    const timer = window.setTimeout(() => {
      void (async () => {
        try {
          const result = await routerApi.getClaudeSignIn(CLAUDE_CODE_ID);
          if (!alive) {
            return;
          }
          if (result.state === "running") {
            setPollTick((tick) => tick + 1);
          } else {
            await finish(result);
          }
        } catch (failure) {
          if (alive) {
            await finish({ state: "failed", error: failureOf(failure).message, started_at: null, finished_at: null });
          }
        }
      })();
    }, SIGN_IN_POLL_MS);
    return () => {
      alive = false;
      window.clearTimeout(timer);
    };
  }, [running, pollTick, finish]);

  async function signIn() {
    setFlow({
      name: CLAUDE_CODE_ID,
      phase: "running",
      error: "",
      command: (account && account.login_command) || "claude auth login",
    });
    announce("Opening Claude Code sign-in in your browser.");
    try {
      const result = await routerApi.startClaudeSignIn(CLAUDE_CODE_ID);
      if (result.state !== "running") {
        await finish(result);
      }
    } catch (failure) {
      const info = failureOf(failure);
      if (info.code === "sign_in_running") {
        return;
      }
      setFlow((current) => (current ? { ...current, phase: "failed", error: info.message } : current));
    }
  }

  async function cancel() {
    try {
      const result = await routerApi.cancelClaudeSignIn(CLAUDE_CODE_ID);
      if (result.state === "succeeded") {
        await finish(result);
        return;
      }
      setFlow((current) => (current ? { ...current, phase: "cancelled", error: "" } : current));
    } catch (failure) {
      setFlow((current) => (current ? { ...current, phase: "failed", error: failureOf(failure).message } : current));
    }
  }

  let detail: ReactNode;
  let action: ReactNode = null;
  switch (state) {
    case "loading":
      detail = <span className="hint">Checking Claude Code…</span>;
      break;
    case "unavailable":
      detail = <span className="hint">Claude Code status is not available. You can connect it later in Router → Providers.</span>;
      break;
    case "missing":
      detail = (
        <span className="hint">
          Not installed.{" "}
          <a className="link" href={CLAUDE_CODE_DOCS} target="_blank" rel="noreferrer">
            Get Claude Code
            <Ic name="arrow-up-right" size={12} />
          </a>
        </span>
      );
      break;
    case "signed-in": {
      const parts = ["Signed in"];
      if (account && account.email) {
        parts.push(account.email);
      }
      if (account && account.plan) {
        parts.push(planLabel(account.plan));
      }
      detail = (
        <Badge tone="green" dot>
          {parts.join(" · ")}
        </Badge>
      );
      action = (
        <label className="setup-cc-use">
          <span>Use Claude Code</span>
          <Toggle
            checked={selected}
            aria-label="Use Claude Code"
            onChange={(checked) => {
              onSelect(checked, account);
              announce(checked ? "Claude Code selected." : "Claude Code not selected.");
            }}
          />
        </label>
      );
      break;
    }
    default:
      detail = (
        <span className="hint">
          {account && account.error && !account.version ? account.error : "Use your Claude plan. Sign in once; Claude Code opens its own sign-in page."}
        </span>
      );
      action = running ? null : (
        <Button size="sm" variant="primary" icon="log-in" onClick={() => void signIn()}>
          Sign in with Claude Code
        </Button>
      );
  }

  return (
    <div className={cn("setup-pc setup-cc", selected && state === "signed-in" && "is-on")} aria-label="Claude Code">
      <div className="setup-pc-head is-static">
        <ProviderLogo logo="claude" name="Claude Code" size={36} />
        <span className="setup-pc-text">
          <span className="setup-pc-name">Claude Code</span>
          <span className="setup-pc-status">{detail}</span>
        </span>
        {action ? <span className="setup-pc-act">{action}</span> : null}
      </div>
      {flow ? (
        <div className="setup-pc-body">
          <SignInCard flow={flow} onCancel={() => void cancel()} onRetry={() => void signIn()} onDismiss={() => setFlow(null)} />
        </div>
      ) : null}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* API key card                                                        */
/* ------------------------------------------------------------------ */

interface ProviderCardProps {
  preset: ProviderPreset;
  connection: SetupConnection | undefined;
  open: boolean;
  onToggle: () => void;
  onConnected: (connection: SetupConnection) => void;
  onRemove: () => void;
}

function ProviderCard({ preset, connection, open, onToggle, onConnected, onRemove }: ProviderCardProps) {
  const announce = useAnnounce();
  const compat = preset.kind === "openai_compat";
  const [apiKey, setApiKey] = useState(() => (connection ? connection.apiKey : ""));
  const [baseUrl, setBaseUrl] = useState(() => (connection && connection.baseUrl) || preset.baseUrl);
  const [keyShown, setKeyShown] = useState(false);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState("");
  const [urlError, setUrlError] = useState("");
  const bodyId = "setup-pc-" + preset.id;
  const keyId = "setup-key-" + preset.id;

  useEffect(() => {
    if (open && !connection) {
      document.getElementById(keyId)?.focus({ preventScroll: true });
    }
  }, [open, connection, keyId]);

  /* The base_url the save writes: empty for a native preset's own URL. */
  function storedBaseUrl(value: string): string {
    const typed = value.trim().replace(/\/+$/, "");
    if (compat) {
      return typed;
    }
    return typed === preset.baseUrl.replace(/\/+$/, "") ? "" : typed;
  }

  async function runTest(key: string, url: string) {
    const trimmed = key.trim();
    if (testing) {
      return;
    }
    if (!trimmed) {
      setError("Paste the API key.");
      return;
    }
    if (!isHttpUrl(url.trim())) {
      setUrlError("Use a complete URL that starts with http or https.");
      return;
    }
    setTesting(true);
    setError("");
    setUrlError("");
    announce("Testing the " + preset.label + " key.");
    try {
      const result = await testProvider({ kind: preset.kind, baseUrl: storedBaseUrl(url), apiKey: trimmed });
      if (result && result.ok) {
        const latency = Number(result.latency_ms) || 0;
        onConnected({ id: preset.id, apiKey: trimmed, baseUrl: storedBaseUrl(url), latencyMs: latency });
        announce(preset.label + " is connected.");
      } else {
        const message = providerFailure(result);
        setError(message);
        announce("Could not connect " + preset.label + ". " + message);
      }
    } catch (failure) {
      const message = errorMessage(failure);
      setError(message);
      announce("Could not test the key. " + message);
    } finally {
      setTesting(false);
    }
  }

  function onPaste(event: ClipboardEvent<HTMLInputElement>) {
    const pasted = event.clipboardData ? event.clipboardData.getData("text") : "";
    if (!pasted.trim()) {
      return;
    }
    event.preventDefault();
    setApiKey(pasted.trim());
    setError("");
    void runTest(pasted, baseUrl);
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void runTest(apiKey, baseUrl);
  }

  const status = connection ? (
    <Badge tone="green" dot>
      Connected
    </Badge>
  ) : (
    <span className="setup-pc-none">Not connected</span>
  );

  return (
    <div className={cn("setup-pc", open && "is-open", connection && "is-on")}>
      <button
        type="button"
        className="setup-pc-head"
        aria-expanded={open}
        aria-controls={bodyId}
        onClick={onToggle}
      >
        <ProviderLogo logo={preset.logo} name={preset.label} size={36} />
        <span className="setup-pc-text">
          <span className="setup-pc-name">{preset.label}</span>
          <span className="setup-pc-status">{status}</span>
        </span>
        <Ic name={open ? "chevron-up" : "chevron-down"} size={15} className="setup-pc-go" />
      </button>
      {open ? (
        <div className="setup-pc-body" id={bodyId}>
          {connection ? (
            <div className="setup-pc-ok" role="status">
              <Ic name="circle-check" size={15} />
              <span className="grow">
                Connected{connection.latencyMs ? " · answered in " + connection.latencyMs + " ms" : ""}. The key is saved to
                secrets.env as {preset.keyEnv} when you finish.
              </span>
              <Button size="sm" variant="ghost" onClick={onRemove}>
                Remove
              </Button>
            </div>
          ) : (
            <form className="auth-form" onSubmit={submit}>
              <Field
                label="API key"
                htmlFor={keyId}
                hint={
                  <>
                    Paste it and lana tests it. It stays on this machine.{" "}
                    <a className="link" href={preset.keysUrl} target="_blank" rel="noreferrer">
                      Get a key
                      <Ic name="arrow-up-right" size={12} />
                    </a>
                  </>
                }
                error={error}
              >
                <div className="pw-field">
                  <Input
                    id={keyId}
                    type={keyShown ? "text" : "password"}
                    autoComplete="off"
                    spellCheck={false}
                    value={apiKey}
                    disabled={testing}
                    onPaste={onPaste}
                    onChange={(event) => {
                      setApiKey(event.target.value);
                      setError("");
                    }}
                  />
                  <IconButton
                    size="sm"
                    className="pw-eye"
                    icon={keyShown ? "eye-off" : "eye"}
                    label={keyShown ? "Hide API key" : "Show API key"}
                    on={keyShown}
                    disabled={testing}
                    onClick={() => setKeyShown((current) => !current)}
                  />
                </div>
              </Field>
              <details className="setup-adv" open={urlError ? true : undefined}>
                <summary>Advanced</summary>
                <Field
                  label="Base URL"
                  htmlFor={"setup-url-" + preset.id}
                  hint="Change it only for a proxy or a gateway."
                  error={urlError}
                >
                  <Input
                    id={"setup-url-" + preset.id}
                    type="url"
                    spellCheck={false}
                    value={baseUrl}
                    disabled={testing}
                    onChange={(event) => {
                      setBaseUrl(event.target.value);
                      setUrlError("");
                    }}
                  />
                </Field>
              </details>
              <div className="setup-pc-acts">
                <span className="hint grow">{testing ? "Testing the key…" : "Saved to secrets.env as " + preset.keyEnv + "."}</span>
                <Button type="submit" size="sm" variant="primary" loading={testing} disabled={testing}>
                  Connect
                </Button>
              </div>
            </form>
          )}
        </div>
      ) : null}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Step: Connect your models                                           */
/* ------------------------------------------------------------------ */

export function ConnectStep() {
  const announce = useAnnounce();
  const [draft, setDraft] = useState<SetupDraft>(readDraft);
  const [openId, setOpenId] = useState("");
  const [more, setMore] = useState(() =>
    readDraft().connections.some((connection) => MORE_PRESETS.some((preset) => preset.id === connection.id))
  );

  const save = useCallback((change: Partial<SetupDraft>) => {
    setDraft(updateDraft(change));
  }, []);

  const selectClaude = useCallback(
    (use: boolean, account: ClaudeAccount | undefined) => {
      save({
        claudeCode: use,
        claudeAccount: use ? { email: (account && account.email) || "", plan: (account && account.plan) || "" } : null,
      });
    },
    [save]
  );

  function connected(connection: SetupConnection) {
    const others = readDraft().connections.filter((entry) => entry.id !== connection.id);
    save({ connections: others.concat(connection) });
  }

  function remove(id: string) {
    save({ connections: readDraft().connections.filter((entry) => entry.id !== id) });
    announce(providerFace(id).label + " removed.");
  }

  const ready = draft.connections.length > 0 || draft.claudeCode;

  function card(preset: ProviderPreset) {
    const connection = draft.connections.find((entry) => entry.id === preset.id);
    return (
      <ProviderCard
        key={preset.id}
        preset={preset}
        connection={connection}
        open={openId === preset.id}
        onToggle={() => setOpenId((current) => (current === preset.id ? "" : preset.id))}
        onConnected={connected}
        onRemove={() => remove(preset.id)}
      />
    );
  }

  return (
    <SetupShell step="connect" size="wide">
      <StepHeading title="Connect your models">
        Use your Claude plan, paste API keys, or both. Agents can use every model you connect, and the
        Router falls back between them. You can add more later in Router → Providers.
      </StepHeading>

      <section className="setup-sec" aria-labelledby="setup-sec-sub">
        <h2 id="setup-sec-sub" className="setup-sec-t">
          Subscription
        </h2>
        <ClaudeCodeCard selected={draft.claudeCode} onSelect={selectClaude} />
      </section>

      <section className="setup-sec" aria-labelledby="setup-sec-api">
        <h2 id="setup-sec-api" className="setup-sec-t">
          API keys
        </h2>
        <div className="setup-pgrid">
          {MAIN_PRESETS.map(card)}
          {more ? MORE_PRESETS.map(card) : null}
        </div>
        <button
          type="button"
          className="setup-more"
          aria-expanded={more}
          onClick={() => setMore((current) => !current)}
        >
          <Ic name={more ? "chevron-up" : "chevron-down"} size={14} />
          {more ? "Fewer providers" : "More providers"}
          {more ? null : (
            <span className="faint">
              {MORE_PRESETS.map((preset) => preset.label.replace(/^(Google|xAI) /, "")).join(", ")}
            </span>
          )}
        </button>
      </section>

      <div className="auth-actions">
        <Button variant="ghost" onClick={() => go("unlock")}>
          Back
        </Button>
        <span className="sp"></span>
        <Button variant="primary" size="lg" iconRight="arrow-right" disabled={!ready} onClick={() => go("model")}>
          Continue
        </Button>
      </div>
      {ready ? null : <p className="hint">Connect a provider or use Claude Code to continue.</p>}
    </SetupShell>
  );
}

/* ------------------------------------------------------------------ */
/* Step: Pick a model                                                  */
/* ------------------------------------------------------------------ */

interface Catalog {
  models: Record<string, SetupModel[]> | null;
  claude: SetupModel[];
}

/* The grouped picker options: Claude Code first, then each connected
   provider with its logo, each group ending with "Other model ID…". */
export function modelOptions(
  draft: SetupDraft,
  catalog: Catalog,
  include: (provider: string) => boolean
): SelectOption[] {
  const out: SelectOption[] = [];
  const group = (provider: string, models: SetupModel[]) => {
    if (!include(provider)) {
      return;
    }
    const face = providerFace(provider);
    const icon = <ProviderLogo logo={face.logo} name={face.label} size={16} />;
    /* Claude Code models read "Claude Code · Sonnet", as in the Router's
       agent picker (API-CCT-004). */
    const prefix = provider === CLAUDE_CODE_ID ? face.label + " · " : "";
    for (const model of models) {
      out.push({ value: provider + "/" + model.id, label: prefix + (model.name || model.id), group: face.label, icon });
    }
    out.push({ value: CUSTOM_PREFIX + provider, label: "Other model ID…", group: face.label, icon });
  };
  if (draft.claudeCode) {
    group(CLAUDE_CODE_ID, catalog.claude.length > 0 ? catalog.claude : CLAUDE_CODE_MODELS);
  }
  for (const connection of draft.connections) {
    const preset = presetById(connection.id);
    if (preset) {
      group(preset.id, presetModels(preset, catalog.models));
    }
  }
  return out;
}

/* The picker value of an address: the option itself, else the provider's
   "Other model ID…" entry. */
function pickerValue(address: string, options: SelectOption[]): string {
  if (!address) {
    return "";
  }
  if (options.some((option) => option.value === address)) {
    return address;
  }
  const provider = addressProvider(address);
  return provider ? CUSTOM_PREFIX + provider : "";
}

interface ModelPickerProps {
  id: string;
  label: string;
  hint: ReactNode;
  value: string;
  options: SelectOption[];
  placeholder: string;
  error?: string;
  onChange: (address: string) => void;
}

/* A grouped model Select with a model ID field for "Other model ID…". */
function ModelPicker({ id, label, hint, value, options, placeholder, error, onChange }: ModelPickerProps) {
  const selected = pickerValue(value, options);
  const custom = selected.startsWith(CUSTOM_PREFIX);
  const provider = custom ? selected.slice(CUSTOM_PREFIX.length) : "";
  return (
    <div className="auth-group">
      <Field label={label} htmlFor={id} hint={custom ? undefined : hint} error={custom ? undefined : error}>
        <Select
          id={id}
          options={options}
          value={selected}
          placeholder={placeholder}
          onChange={(next) => onChange(next.startsWith(CUSTOM_PREFIX) ? next.slice(CUSTOM_PREFIX.length) + "/" : next)}
        />
      </Field>
      {custom ? (
        <Field
          label={"Model ID on " + providerFace(provider).label}
          htmlFor={id + "-custom"}
          hint={hint}
          error={error}
        >
          <Input
            id={id + "-custom"}
            spellCheck={false}
            autoComplete="off"
            value={addressModel(value)}
            onChange={(event) => onChange(provider + "/" + event.target.value)}
          />
        </Field>
      ) : null}
    </div>
  );
}

const NO_FALLBACK = "";

export function ModelStep() {
  const [draft, setDraft] = useState<SetupDraft>(() => {
    const current = readDraft();
    return updateDraft(reconcileModels(current));
  });
  const [catalog, setCatalog] = useState<Catalog>({ models: null, claude: [] });
  const [tried, setTried] = useState(false);

  useEffect(() => {
    let alive = true;
    getSetup()
      .then((status) => {
        if (alive && status) {
          setCatalog({
            models: status.models || null,
            claude: Array.isArray(status.claude_code_models) ? status.claude_code_models : [],
          });
        }
      })
      .catch(() => {
        /* The picker falls back to the built-in lists. */
      });
    return () => {
      alive = false;
    };
  }, []);

  const mainProvider = addressProvider(draft.model);
  const mainOptions = useMemo(() => modelOptions(draft, catalog, () => true), [draft, catalog]);
  const fallbackOptions = useMemo(
    () =>
      [{ value: NO_FALLBACK, label: "No fallback" } as SelectOption].concat(
        modelOptions(draft, catalog, (provider) => provider !== mainProvider)
      ),
    [draft, catalog, mainProvider]
  );
  const backgroundOptions = useMemo(
    () => modelOptions(draft, catalog, (provider) => provider !== CLAUDE_CODE_ID),
    [draft, catalog]
  );
  const claudeMain = isClaudeAddress(draft.model);
  const apiConnected = draft.connections.length > 0;

  function change(next: Partial<SetupDraft>) {
    const merged = { ...readDraft(), ...next };
    /* A new main model drops a fallback on the same provider and gives a
       Claude Code main model its background model. */
    setDraft(updateDraft({ ...next, ...reconcileModels(merged) }));
  }

  const mainError = tried && !validAddress(draft.model) ? "Pick a model or enter its ID." : "";
  const fallbackError =
    tried && draft.fallback && !validAddress(draft.fallback) ? "Enter the model ID or pick No fallback." : "";
  const backgroundError =
    tried && claudeMain && apiConnected && !validAddress(draft.background) ? "Pick a model or enter its ID." : "";

  function continueNext() {
    setTried(true);
    if (!validAddress(draft.model) || (draft.fallback && !validAddress(draft.fallback))) {
      return;
    }
    if (claudeMain && apiConnected && !validAddress(draft.background)) {
      return;
    }
    go("agent");
  }

  return (
    <SetupShell step="model" size="wide">
      <StepHeading title="Pick a model">
        Your first agent uses this model. {ROUTER_NOTE}
      </StepHeading>

      <div className="auth-form">
        <ModelPicker
          id="setup-main-model"
          label="Main model"
          hint="Every turn of the agent starts on this model."
          value={draft.model}
          options={mainOptions}
          placeholder="Pick a model"
          error={mainError}
          onChange={(model) => change({ model })}
        />

        {claudeMain ? (
          <div className="setup-note" role="note">
            <ProviderLogo logo="claude" name="Claude Code" size={18} />
            <p>
              Runs in Claude Code with your Claude plan. {BACKGROUND_NOTE}{" "}
              {apiConnected
                ? "Pick the one they use below."
                : "Connect an API key to run them; until then they are skipped."}
            </p>
          </div>
        ) : null}

        {fallbackOptions.length > 1 ? (
          <ModelPicker
            id="setup-fallback-model"
            label="Fallback when it fails"
            hint="Optional. When the main model fails or hits a limit, the Router tries this one. lana saves the pair as the combo default."
            value={draft.fallback}
            options={fallbackOptions}
            placeholder="No fallback"
            error={fallbackError}
            onChange={(fallback) => change({ fallback })}
          />
        ) : (
          <p className="hint">Connect a second provider to add a fallback model.</p>
        )}

        {claudeMain && apiConnected ? (
          <ModelPicker
            id="setup-background-model"
            label="Background model"
            hint="Titles, memory, and knowledge run on this model. A cheap, fast one is enough."
            value={draft.background}
            options={backgroundOptions}
            placeholder="Pick a model"
            error={backgroundError}
            onChange={(background) => change({ background })}
          />
        ) : null}
      </div>

      <div className="auth-actions">
        <Button variant="ghost" onClick={() => go("connect")}>
          Back
        </Button>
        <span className="sp"></span>
        <Button variant="primary" size="lg" iconRight="arrow-right" onClick={continueNext}>
          Continue
        </Button>
      </div>
    </SetupShell>
  );
}
