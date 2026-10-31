import { useEffect, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { ApiError, errorMessage, setCSRF } from "../api/client";
import { ErrorNotice, useAnnounce } from "../ui/ui";
import { Icon, providerLabel } from "./agent-common";
import {
  getDaemonStartedAt,
  getSetup,
  pollDaemonRestart,
  saveSetup,
  testProvider,
} from "../api/setup";
import type { SetupTestProviderResponse } from "../api/setup";

/* Setup wizard (REQ-WEB-032, REQ-WEB-033, REQ-WEB-034). The shell and the
   progress rail are shared with the Unlock step in views/login.js. Every step
   is a hash route under #/setup/{step}; app.js owns the route table. */

const DRAFT_KEY = "lana.setup";
const SUMMARY_KEY = "lana.setup.summary";
const USER_LIMIT = 2000;

/* The asset scan forbids every protocol-relative URL in shell files, so the
   scheme is assembled instead of written as one literal. */
const SECURE = "https:" + "/".repeat(2);

interface ProviderPreset {
  id: string;
  label: string;
  initials: string;
  description: string;
  kind: string;
  baseUrl: string;
  keyEnv: string;
  model: string;
  suggestions: readonly string[];
}

const PROVIDERS = [
  {
    id: "openai",
    label: "OpenAI",
    initials: "AI",
    description: "GPT models through api.openai.com.",
    kind: "openai_compat",
    baseUrl: SECURE + "api.openai.com/v1",
    keyEnv: "OPENAI_API_KEY",
    model: "gpt-5-mini",
    suggestions: ["gpt-5-mini", "gpt-5", "gpt-4.1"],
  },
  {
    id: "anthropic",
    label: "Anthropic",
    initials: "AN",
    description: "Claude models through api.anthropic.com.",
    /* Q-029: until a native adapter exists, Anthropic uses the
       OpenAI-compatible endpoint through the same openai_compat kind. */
    kind: "openai_compat",
    baseUrl: SECURE + "api.anthropic.com/v1",
    keyEnv: "ANTHROPIC_API_KEY",
    model: "claude-sonnet-4-5",
    suggestions: ["claude-sonnet-4-5", "claude-opus-4-1", "claude-haiku-4-5"],
  },
  {
    id: "openrouter",
    label: "OpenRouter",
    initials: "OR",
    description: "Models from many vendors with one key.",
    kind: "openai_compat",
    baseUrl: SECURE + "openrouter.ai/api/v1",
    keyEnv: "OPENROUTER_API_KEY",
    model: "openai/gpt-5-mini",
    suggestions: [
      "openai/gpt-5-mini",
      "anthropic/claude-sonnet-4.5",
      "google/gemini-2.5-pro",
    ],
  },
] as const satisfies readonly ProviderPreset[];

const PROVIDER_BY_ID: Record<string, ProviderPreset> = {};
for (const preset of PROVIDERS) {
  PROVIDER_BY_ID[preset.id] = preset;
}

interface TemplatePreset {
  id: string;
  label: string;
  note: string;
}

const TEMPLATES = [
  {
    id: "assistant",
    label: "Assistant",
    note: "Everyday help and research.",
  },
  {
    id: "coder",
    label: "Coder",
    note: "Works in a code repository.",
  },
  {
    id: "worker",
    label: "Worker",
    note: "Runs tasks, checks results.",
  },
] as const satisfies readonly TemplatePreset[];

const TEMPLATE_BY_ID: Record<string, TemplatePreset> = {};
for (const template of TEMPLATES) {
  TEMPLATE_BY_ID[template.id] = template;
}

interface PermissionPreset {
  id: string;
  label: string;
  note: string;
  summary: string;
}

const PERMISSIONS = [
  {
    id: "ask",
    label: "Ask",
    note: "Approve risky tool calls.",
    summary: "Ask before risky tools",
  },
  {
    id: "trust",
    label: "Trust",
    note: "Run tools without asking.",
    summary: "Run tools without asking",
  },
  {
    id: "never",
    label: "Never",
    note: "Block risky tool calls.",
    summary: "Block risky tool calls",
  },
] as const satisfies readonly PermissionPreset[];

const PERMISSION_BY_ID: Record<string, PermissionPreset> = {};
for (const permission of PERMISSIONS) {
  PERMISSION_BY_ID[permission.id] = permission;
}

interface RailStep {
  title: string;
  note: string;
}

const RAIL_STEPS: readonly RailStep[] = [
  {
    title: "Create your password",
    note: "Only you can open lana",
  },
  {
    title: "Choose a provider",
    note: "OpenAI, Anthropic, or OpenRouter",
  },
  {
    title: "Connect your model",
    note: "API key, base URL, and model",
  },
  {
    title: "Set up your agent",
    note: "Name, template, and tool access",
  },
  {
    title: "Start chatting",
    note: "Open your first session",
  },
];

const RAIL_INDEX: Record<string, number> = {
  unlock: 0,
  provider: 1,
  connect: 2,
  agent: 3,
  persona: 3,
  ready: 4,
};

const STEP_NUMBER: Record<string, number> = {
  unlock: 1,
  provider: 2,
  connect: 3,
  agent: 4,
  persona: 4,
  ready: 5,
};

const numberFormat = new Intl.NumberFormat("en-US");

interface SetupConnectDraft {
  apiKey: string;
  baseUrl: string;
  model: string;
}

interface SetupAgentDraft {
  name: string;
  template: string;
  workspace: string;
  permission: string;
}

interface SetupPersonaDraft {
  agentMd: string;
  userMd: string;
}

interface SetupDraft {
  provider: string;
  connect: SetupConnectDraft;
  agent: SetupAgentDraft;
  persona: SetupPersonaDraft;
}

interface SetupSummary {
  provider: string;
  providerLabel: string;
  model: string;
  agentName: string;
  template: string;
  templateLabel: string;
  permission: string;
  permissionLabel: string;
  workspace: string;
}

/* sessionStorage holds the draft across routes. A module-level copy keeps the
   wizard working when storage is disabled; it is cleared with the stored copy
   after a successful save. */
let memoryDraft: SetupDraft | null = null;
let memorySummary: SetupSummary | null = null;

function textValue(value: unknown, fallback: string): string {
  return typeof value === "string" ? value : fallback;
}

function providerById(id: string): ProviderPreset {
  return PROVIDER_BY_ID[id] || PROVIDERS[0];
}

function templateById(id: string): TemplatePreset {
  return TEMPLATE_BY_ID[id] || TEMPLATES[0];
}

function permissionById(id: string): PermissionPreset {
  return PERMISSION_BY_ID[id] || PERMISSIONS[0];
}

function defaultConnect(providerId: string): SetupConnectDraft {
  const preset = providerById(providerId);
  return {
    apiKey: "",
    baseUrl: preset.baseUrl,
    model: preset.model,
  };
}

function defaultDraft(): SetupDraft {
  return {
    provider: PROVIDERS[0].id,
    connect: defaultConnect(PROVIDERS[0].id),
    agent: {
      name: "default",
      template: TEMPLATES[0].id,
      workspace: "~/lana/workspace",
      permission: PERMISSIONS[0].id,
    },
    persona: {
      agentMd: "",
      userMd: "",
    },
  };
}

function normalizeDraft(value: unknown): SetupDraft {
  const fallback = defaultDraft();
  if (!value || typeof value !== "object") {
    return fallback;
  }
  const draft = value as Record<string, unknown>;
  const rawProvider = draft.provider;
  const provider =
    typeof rawProvider === "string" && PROVIDER_BY_ID[rawProvider]
      ? rawProvider
      : fallback.provider;
  const preset = providerById(provider);
  const connect =
    draft.connect && typeof draft.connect === "object"
      ? (draft.connect as Record<string, unknown>)
      : {};
  const agent =
    draft.agent && typeof draft.agent === "object"
      ? (draft.agent as Record<string, unknown>)
      : {};
  const persona =
    draft.persona && typeof draft.persona === "object"
      ? (draft.persona as Record<string, unknown>)
      : {};
  return {
    provider,
    connect: {
      apiKey: textValue(connect.apiKey, ""),
      baseUrl: textValue(connect.baseUrl, preset.baseUrl),
      model: textValue(connect.model, preset.model),
    },
    agent: {
      name: textValue(agent.name, fallback.agent.name),
      template:
        typeof agent.template === "string" && TEMPLATE_BY_ID[agent.template]
          ? agent.template
          : fallback.agent.template,
      workspace: textValue(agent.workspace, fallback.agent.workspace),
      permission:
        typeof agent.permission === "string" && PERMISSION_BY_ID[agent.permission]
          ? agent.permission
          : fallback.agent.permission,
    },
    persona: {
      agentMd: textValue(persona.agentMd, ""),
      userMd: textValue(persona.userMd, ""),
    },
  };
}

function readDraft(): SetupDraft {
  if (memoryDraft) {
    return memoryDraft;
  }
  let parsed: unknown = null;
  try {
    const raw = window.sessionStorage.getItem(DRAFT_KEY);
    if (raw) {
      parsed = JSON.parse(raw);
    }
  } catch {
    /* Storage can be disabled; the in-memory copy still carries this page. */
  }
  memoryDraft = normalizeDraft(parsed);
  return memoryDraft;
}

function writeDraft(next: SetupDraft): SetupDraft {
  memoryDraft = normalizeDraft(next);
  try {
    window.sessionStorage.setItem(DRAFT_KEY, JSON.stringify(memoryDraft));
  } catch {
    /* Keep the in-memory copy when storage is unavailable. */
  }
  return memoryDraft;
}

function clearDraft(): void {
  memoryDraft = null;
  try {
    window.sessionStorage.removeItem(DRAFT_KEY);
  } catch {
    /* Nothing else to clear. */
  }
}

function readSummary(): SetupSummary | null {
  if (memorySummary) {
    return memorySummary;
  }
  try {
    const raw = window.sessionStorage.getItem(SUMMARY_KEY);
    if (raw) {
      memorySummary = JSON.parse(raw) as SetupSummary;
      return memorySummary;
    }
  } catch {
    /* Fall through to the draft. */
  }
  return null;
}

function writeSummary(summary: SetupSummary): void {
  memorySummary = summary;
  try {
    window.sessionStorage.setItem(SUMMARY_KEY, JSON.stringify(summary));
  } catch {
    /* The current page still has the summary in memory. */
  }
}

function summaryFor(draft: SetupDraft): SetupSummary {
  const preset = providerById(draft.provider);
  const template = templateById(draft.agent.template);
  const permission = permissionById(draft.agent.permission);
  return {
    provider: draft.provider,
    providerLabel: preset.label,
    model: draft.connect.model.trim(),
    agentName: draft.agent.name.trim(),
    template: draft.agent.template,
    templateLabel: template.label,
    permission: draft.agent.permission,
    permissionLabel: permission.summary,
    workspace: draft.agent.workspace.trim() || "~/lana/workspace",
  };
}

function characterCount(value: unknown): number {
  return Array.from(String(value || "")).length;
}

function go(step: string): void {
  window.location.hash = "#/setup/" + step;
}

function setupAddress(): string {
  return window.location.host || "127.0.0.1:18300";
}

function isHttpUrl(value: string): boolean {
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:";
  } catch {
    return false;
  }
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

function stepEyebrow(step: string): string {
  if (step === "persona") {
    return "Step 4 of 5 · Persona";
  }
  return "Step " + String(STEP_NUMBER[step]) + " of 5";
}

/* The API contract has no template preview endpoint, so the persona fields
   start empty and "Reset to template" stays disabled with a short note. */
const TEMPLATE_PREVIEW_AVAILABLE = false;

interface ProgressStepProps {
  state: string;
  title: string;
  note: string;
  connector: boolean;
}

function ProgressStep({ state, title, note, connector }: ProgressStepProps) {
  const stateLabel =
    state === "complete"
      ? "Complete"
      : state === "current"
        ? "Current step"
        : "Not started";
  return (
    <li
      className={"setup-step is-" + state}
      aria-current={state === "current" ? "step" : undefined}
    >
      <span className="setup-step-marker" aria-hidden="true">
        <span className="setup-step-icon">
          {state === "complete" ? (
            <Icon name="check" className="setup-check-icon" />
          ) : (
            <span className="setup-step-dot"></span>
          )}
        </span>
        {connector ? <span className="setup-step-connector"></span> : null}
      </span>
      <span className="setup-step-copy">
        <span className="sr-only">{stateLabel}: </span>
        <span className="setup-step-title">{title}</span>
        <span className="setup-step-note">{note}</span>
      </span>
    </li>
  );
}

/* The decorative door mark of the setup rail (login, forgot, code, and
   onboarding): the two shapes of the logo Λ drawn large and cut off by the
   rail edges. */
function DoorMark() {
  return (
    <svg className="door-mark" viewBox="0 0 34 32" aria-hidden="true">
      <path
        transform="matrix(1 0 0 1 1.264 4.75)"
        d="M0.39 18.028L9.681 1.528C10.213 0.584 11.212 0 12.295 0L15.843 0C18.138 0 19.583 2.472 18.457 4.472L9.166 20.972C8.635 21.916 7.636 22.5 6.552 22.5L3.004 22.5C0.709 22.5 -0.736 20.028 0.39 18.028Z"
      />
      <path
        className="door-right"
        transform="matrix(1 0 0 1 11.889 4.75)"
        d="M3.004 0L6.552 0C7.636 0 8.635 0.584 9.166 1.528L18.457 18.028C19.583 20.028 18.138 22.5 15.843 22.5L12.295 22.5C11.212 22.5 10.213 21.916 9.681 20.972L0.39 4.472C-0.736 2.472 0.709 0 3.004 0Z"
      />
    </svg>
  );
}

export interface SetupShellProps {
  step: string;
  size?: string;
  variant?: "steps" | "door";
  children?: ReactNode;
}

export function SetupShell({
  step,
  size = "regular",
  variant = "steps",
  children,
}: SetupShellProps) {
  const current = RAIL_INDEX[step] || 0;
  const door = variant === "door";
  const contentClass =
    size === "wide"
      ? " setup-content-wide"
      : size === "agent"
        ? " setup-content-agent"
        : "";
  return (
    <div className="setup-shell">
      <aside className="setup-rail" aria-label={door ? "lana" : "Setup progress"}>
        <DoorMark />
        <div className="setup-rail-top">
          <a
            className="logo-lockup setup-logo"
            href={door ? "#/login" : "#/setup/unlock"}
            aria-label={door ? "lana" : "lana setup"}
          >
            <img
              className="logo-mark"
              src="/assets/icons/logo-mark.svg"
              alt=""
              aria-hidden="true"
            />
            <span className="logo-word">LΛNΛ</span>
          </a>
          {door ? null : (
            <ol className="setup-steps">
              {RAIL_STEPS.map((entry, index) => {
                const state =
                  index < current
                    ? "complete"
                    : index === current
                      ? "current"
                      : "incomplete";
                const note =
                  step === "persona" && index === 3
                    ? "Persona and your profile"
                    : entry.note;
                return (
                  <ProgressStep
                    key={entry.title}
                    state={state}
                    title={entry.title}
                    note={note}
                    connector={index < RAIL_STEPS.length - 1}
                  />
                );
              })}
            </ol>
          )}
        </div>
        {door ? (
          <p className="rail-place">
            <strong>lana on this computer</strong>
            Only this computer can open this page.
          </p>
        ) : (
          <div className="setup-rail-footer">
            <span className="setup-local">Local only · {setupAddress()}</span>
            <span className="setup-help">Help</span>
          </div>
        )}
      </aside>
      <main id="main" className="setup-main">
        <div className={"setup-content" + contentClass}>{children}</div>
      </main>
    </div>
  );
}

interface StepHeadingProps {
  step: string;
  title: string;
  children?: ReactNode;
}

function StepHeading({ step, title, children }: StepHeadingProps) {
  return (
    <div className="setup-heading">
      <span className="setup-eyebrow">{stepEyebrow(step)}</span>
      <h1 className="setup-title">{title}</h1>
      <p className="setup-lede">{children}</p>
    </div>
  );
}

/* Returns the boolean `needed` flag of API-WEB-018. Errors are rethrown, not
   turned into `false`: the caller decides whether to show the wizard, a retry,
   or an error. A silent `false` would hide first-run setup when the daemon
   cannot answer. */
export async function setupNeeded(): Promise<boolean> {
  const status = await getSetup();
  return Boolean(status && status.needed);
}

function ProviderStep() {
  const announce = useAnnounce();
  const [provider, setProvider] = useState(() => readDraft().provider);

  function selectProvider(id: string) {
    if (id === provider) {
      return;
    }
    const current = readDraft();
    const next = {
      ...current,
      provider: id,
      connect: defaultConnect(id),
    };
    writeDraft(next);
    setProvider(id);
    announce(providerById(id).label + " selected.");
  }

  return (
    <SetupShell step="provider">
      <StepHeading step="provider" title="Choose a model provider">
        Pick where lana sends model requests. Each provider has a default base URL
        that you can change in the next step.
      </StepHeading>

      <fieldset className="setup-group">
        <legend className="sr-only">Provider</legend>
        <div className="setup-provider-list">
          {PROVIDERS.map((preset) => {
            const selected = preset.id === provider;
            return (
              <label
                key={preset.id}
                className={
                  "agent-permission-tile setup-provider-card" +
                  (selected ? " is-selected" : "")
                }
              >
                <input
                  className="agent-permission-input"
                  type="radio"
                  name="setup-provider"
                  value={preset.id}
                  checked={selected}
                  onChange={() => selectProvider(preset.id)}
                />
                <span className="setup-provider-logo" aria-hidden="true">
                  {preset.initials}
                </span>
                <span className="agent-permission-text setup-provider-text">
                  <span className="agent-permission-title">{preset.label}</span>
                  <span className="agent-permission-note">
                    {preset.description}
                  </span>
                  <span className="setup-provider-meta">
                    {preset.kind} · {preset.baseUrl}
                  </span>
                </span>
                <span className="agent-permission-radio" aria-hidden="true">
                  <span className="agent-permission-dot"></span>
                </span>
              </label>
            );
          })}
        </div>
      </fieldset>

      <div className="setup-actions">
        <button
          type="button"
          className="button-ui button-ui-secondary"
          onClick={() => go("unlock")}
        >
          Back
        </button>
        <button
          type="button"
          className="button-ui button-ui-primary"
          onClick={() => go("connect")}
        >
          Continue
        </button>
      </div>
    </SetupShell>
  );
}

interface ConnectFieldErrors {
  apiKey?: string;
  baseUrl?: string;
  model?: string;
}

function ConnectStep() {
  const announce = useAnnounce();
  const [draft] = useState(readDraft);
  const preset = providerById(draft.provider);
  const [connect, setConnect] = useState(() => draft.connect);
  const [fieldErrors, setFieldErrors] = useState<ConnectFieldErrors>({});
  const [result, setResult] = useState<SetupTestProviderResponse | null>(null);
  const [requestError, setRequestError] = useState<unknown>(null);
  const [testing, setTesting] = useState(false);
  const [tested, setTested] = useState(false);

  function update(field: keyof SetupConnectDraft, value: string) {
    const next = { ...connect, [field]: value };
    setConnect(next);
    const current = readDraft();
    writeDraft({ ...current, connect: next });
    setFieldErrors((errors) => ({ ...errors, [field]: "" }));
    setResult(null);
    setRequestError(null);
    setTested(false);
  }

  function validate(): boolean {
    const errors: ConnectFieldErrors = {};
    if (!connect.apiKey.trim()) {
      errors.apiKey = "Enter the API key.";
    }
    if (!connect.baseUrl.trim()) {
      errors.baseUrl = "Enter the base URL.";
    } else if (!isHttpUrl(connect.baseUrl.trim())) {
      errors.baseUrl = "Use a complete URL that starts with http or https.";
    }
    if (!connect.model.trim()) {
      errors.model = "Enter the default model.";
    }
    setFieldErrors(errors);
    return Object.keys(errors).length === 0;
  }

  async function runTest() {
    if (testing) {
      return;
    }
    if (!validate()) {
      announce("Check the highlighted fields and try again.");
      return;
    }
    setTesting(true);
    setResult(null);
    setRequestError(null);
    setTested(false);
    announce("Testing the connection.");
    try {
      const response = await testProvider({
        kind: preset.kind,
        baseUrl: connect.baseUrl.trim(),
        model: connect.model.trim(),
        apiKey: connect.apiKey,
      });
      setResult(response);
      if (response && response.ok) {
        const latency = Number(response.latency_ms) || 0;
        const message =
          connect.model.trim() + " answered in " + latency + " ms.";
        setTested(true);
        announce("Connection works. " + message);
      } else {
        const message = providerFailure(response);
        setFieldErrors((errors) => ({ ...errors, apiKey: message }));
        announce("Could not reach the model. " + message);
      }
    } catch (error) {
      const message = errorMessage(error);
      setRequestError(error);
      setFieldErrors((errors) => ({ ...errors, apiKey: message }));
      announce("Could not test the connection. " + message);
    } finally {
      setTesting(false);
    }
  }

  function continueNext(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!tested || testing) {
      return;
    }
    go("agent");
  }

  const apiKeyDescribedBy = [
    "setup-api-key-hint",
    fieldErrors.apiKey ? "setup-api-key-error" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const baseUrlDescribedBy = [
    "setup-base-url-hint",
    fieldErrors.baseUrl ? "setup-base-url-error" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const modelDescribedBy = [
    "setup-model-hint",
    fieldErrors.model ? "setup-model-error" : "",
  ]
    .filter(Boolean)
    .join(" ");

  const latency = Number(result && result.latency_ms) || 0;
  const alertTone = result && result.ok ? "is-success" : "is-error";
  const alertTitle =
    result && result.ok ? "Connection works" : "Could not reach the model";
  const alertBody = result
    ? result.ok
      ? connect.model.trim() + " answered in " + latency + " ms."
      : providerFailure(result)
    : requestError
      ? errorMessage(requestError)
      : "";

  return (
    <SetupShell step="connect">
      <StepHeading step="connect" title={"Connect " + preset.label}>
        Add the API key and pick the default model. lana tests the connection
        before it saves.
      </StepHeading>

      <form className="setup-form" onSubmit={continueNext}>
        <div className="setup-field">
          <label className="setup-label" htmlFor="setup-api-key">
            API key
          </label>
          <input
            id="setup-api-key"
            type="password"
            autoComplete="off"
            spellCheck={false}
            value={connect.apiKey}
            disabled={testing}
            aria-required="true"
            aria-invalid={fieldErrors.apiKey ? "true" : undefined}
            aria-describedby={apiKeyDescribedBy}
            onChange={(event) => update("apiKey", event.target.value)}
          />
          <p id="setup-api-key-hint" className="setup-hint">
            Saved to secrets.env as {preset.keyEnv}. It stays on this machine.
          </p>
          {fieldErrors.apiKey ? (
            <p id="setup-api-key-error" className="setup-field-error" role="alert">
              {fieldErrors.apiKey}
            </p>
          ) : null}
        </div>

        <div className="setup-field">
          <label className="setup-label" htmlFor="setup-base-url">
            Base URL
          </label>
          <input
            id="setup-base-url"
            type="url"
            autoComplete="url"
            spellCheck={false}
            value={connect.baseUrl}
            disabled={testing}
            aria-required="true"
            aria-invalid={fieldErrors.baseUrl ? "true" : undefined}
            aria-describedby={baseUrlDescribedBy}
            onChange={(event) => update("baseUrl", event.target.value)}
          />
          <p id="setup-base-url-hint" className="setup-hint">
            Change it for a proxy or a compatible gateway.
          </p>
          {fieldErrors.baseUrl ? (
            <p id="setup-base-url-error" className="setup-field-error" role="alert">
              {fieldErrors.baseUrl}
            </p>
          ) : null}
        </div>

        <div className="setup-field">
          <label className="setup-label" htmlFor="setup-model">
            Default model
          </label>
          <input
            id="setup-model"
            type="text"
            autoComplete="off"
            spellCheck={false}
            value={connect.model}
            disabled={testing}
            aria-required="true"
            aria-invalid={fieldErrors.model ? "true" : undefined}
            aria-describedby={modelDescribedBy}
            onChange={(event) => update("model", event.target.value)}
          />
          <p id="setup-model-hint" className="setup-hint">
            The default agent uses this model. Change it any time in Settings.
          </p>
          {fieldErrors.model ? (
            <p id="setup-model-error" className="setup-field-error" role="alert">
              {fieldErrors.model}
            </p>
          ) : null}
          <div className="setup-suggestions">
            <span className="setup-suggestions-label">Suggested:</span>
            {preset.suggestions.map((model) => (
              <button
                key={model}
                type="button"
                className={
                  "badge-ui setup-suggestion" +
                  (connect.model === model ? " badge-ui-brand" : "")
                }
                aria-pressed={connect.model === model}
                disabled={testing}
                onClick={() => update("model", model)}
              >
                <span className="badge-ui-dot" aria-hidden="true"></span>{model}
              </button>
            ))}
          </div>
        </div>

        <div
          className="setup-test-region"
          role="status"
          aria-live="polite"
          aria-atomic="true"
        >
          {result || requestError ? (
            <div className={"setup-alert " + alertTone}>
              <span className="setup-alert-icon" aria-hidden="true"></span>
              <span className="setup-alert-copy">
                <span className="setup-alert-title">{alertTitle}</span>
                <p className="setup-alert-body">{alertBody}</p>
              </span>
            </div>
          ) : null}
        </div>

        <div className="setup-actions">
          <button
            type="button"
            className="button-ui button-ui-secondary"
            disabled={testing}
            onClick={runTest}
          >
            {testing
              ? "Testing…"
              : result && !result.ok
                ? "Test again"
                : "Test connection"}
          </button>
          <button
            type="submit"
            className="button-ui button-ui-primary"
            disabled={!tested || testing}
          >
            Save and continue
          </button>
        </div>
        {!tested ? (
          <p className="setup-actions-note">
            Test the connection before you continue.
          </p>
        ) : null}
      </form>
    </SetupShell>
  );
}

interface AgentFieldErrors {
  name?: string;
  workspace?: string;
}

function AgentStep() {
  const announce = useAnnounce();
  const [agent, setAgent] = useState(() => readDraft().agent);
  const [errors, setErrors] = useState<AgentFieldErrors>({});

  function update(field: keyof SetupAgentDraft, value: string) {
    const next = { ...agent, [field]: value };
    setAgent(next);
    const current = readDraft();
    writeDraft({ ...current, agent: next });
    setErrors((currentErrors) => ({ ...currentErrors, [field]: "" }));
  }

  function validate(): boolean {
    const nextErrors: AgentFieldErrors = {};
    if (!/^[a-z0-9-]+$/.test(agent.name)) {
      nextErrors.name = "Use lowercase letters, numbers, and dashes only.";
    }
    if (!agent.workspace.trim()) {
      nextErrors.workspace = "Enter a workspace path.";
    }
    setErrors(nextErrors);
    return Object.keys(nextErrors).length === 0;
  }

  function continueNext() {
    if (!validate()) {
      announce("Check the agent fields and try again.");
      return;
    }
    go("persona");
  }

  const nameDescribedBy = [
    "setup-agent-name-hint",
    errors.name ? "setup-agent-name-error" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const workspaceDescribedBy = [
    "setup-workspace-hint",
    errors.workspace ? "setup-workspace-error" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <SetupShell step="agent" size="agent">
      <StepHeading step="agent" title="Set up your agent">
        This agent answers every new session. You can add more agents and change
        these choices later in Settings.
      </StepHeading>

      <div className="setup-form">
        <div className="setup-field">
          <label className="setup-label" htmlFor="setup-agent-name">
            Agent name
          </label>
          <input
            id="setup-agent-name"
            type="text"
            autoComplete="off"
            spellCheck={false}
            value={agent.name}
            aria-required="true"
            aria-invalid={errors.name ? "true" : undefined}
            aria-describedby={nameDescribedBy}
            onChange={(event) => update("name", event.target.value)}
          />
          <p id="setup-agent-name-hint" className="setup-hint">
            Lowercase letters, numbers, and dashes. Routes and the CLI use this
            name.
          </p>
          {errors.name ? (
            <p id="setup-agent-name-error" className="setup-field-error" role="alert">
              {errors.name}
            </p>
          ) : null}
        </div>

        <fieldset className="setup-group">
          <legend className="setup-label">Template</legend>
          <div className="agent-permission-tiles setup-tiles">
            {TEMPLATES.map((template) => {
              const selected = agent.template === template.id;
              return (
                <label
                  key={template.id}
                  className={
                    "agent-permission-tile" + (selected ? " is-selected" : "")
                  }
                >
                  <input
                    className="agent-permission-input"
                    type="radio"
                    name="setup-template"
                    value={template.id}
                    checked={selected}
                    onChange={() => update("template", template.id)}
                  />
                  <span className="agent-permission-text">
                    <span className="agent-permission-title">
                      {template.label}
                    </span>
                    <span className="agent-permission-note">
                      {template.note}
                    </span>
                  </span>
                  <span className="agent-permission-radio" aria-hidden="true">
                    <span className="agent-permission-dot"></span>
                  </span>
                </label>
              );
            })}
          </div>
          <p className="setup-hint">
            A starting point. You can rewrite the whole persona on the next
            screen.
          </p>
        </fieldset>

        <div className="setup-field">
          <label className="setup-label" htmlFor="setup-workspace">
            Workspace
          </label>
          <input
            id="setup-workspace"
            type="text"
            autoComplete="off"
            spellCheck={false}
            value={agent.workspace}
            aria-required="true"
            aria-invalid={errors.workspace ? "true" : undefined}
            aria-describedby={workspaceDescribedBy}
            onChange={(event) => update("workspace", event.target.value)}
          />
          <p id="setup-workspace-hint" className="setup-hint">
            File and bash tools work inside this folder.
          </p>
          {errors.workspace ? (
            <p id="setup-workspace-error" className="setup-field-error" role="alert">
              {errors.workspace}
            </p>
          ) : null}
        </div>

        <fieldset className="setup-group">
          <legend className="setup-label">Tool permission</legend>
          <div className="agent-permission-tiles setup-tiles">
            {PERMISSIONS.map((permission) => {
              const selected = agent.permission === permission.id;
              return (
                <label
                  key={permission.id}
                  className={
                    "agent-permission-tile" + (selected ? " is-selected" : "")
                  }
                >
                  <input
                    className="agent-permission-input"
                    type="radio"
                    name="setup-permission"
                    value={permission.id}
                    checked={selected}
                    onChange={() => update("permission", permission.id)}
                  />
                  <span className="agent-permission-text">
                    <span className="agent-permission-title">
                      {permission.label}
                    </span>
                    <span className="agent-permission-note">
                      {permission.note}
                    </span>
                  </span>
                  <span className="agent-permission-radio" aria-hidden="true">
                    <span className="agent-permission-dot"></span>
                  </span>
                </label>
              );
            })}
          </div>
          <p className="setup-hint">
            Ask is recommended. Memory tools never ask for approval.
          </p>
        </fieldset>
      </div>

      <div className="setup-actions">
        <button
          type="button"
          className="button-ui button-ui-secondary"
          onClick={() => go("connect")}
        >
          Back
        </button>
        <button
          type="button"
          className="button-ui button-ui-primary"
          onClick={continueNext}
        >
          Continue
        </button>
      </div>
    </SetupShell>
  );
}

interface RestartPanelProps {
  phase: string;
  error: unknown;
  onRetry: () => void;
}

function RestartPanel({ phase, error, onRetry }: RestartPanelProps) {
  if (phase === "restarting") {
    return (
      <div
        className="setup-restart"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        <span className="setup-spinner" aria-hidden="true"></span>
        <h1 className="setup-restart-title">Restarting lana…</h1>
        <p className="setup-restart-text">
          Setup is saved. The daemon restarts in place and this page reconnects
          on its own.
        </p>
      </div>
    );
  }
  return (
    <div className="setup-restart is-error" role="alert">
      <span className="setup-restart-icon" aria-hidden="true">
        !
      </span>
      <h1 className="setup-restart-title">The daemon did not answer</h1>
      <p className="setup-restart-text">{errorMessage(error)}</p>
      <p className="setup-hint">
        If lana is still starting, check again. You can also restart it from the
        terminal.
      </p>
      <button
        type="button"
        className="button-ui button-ui-secondary setup-retry"
        onClick={onRetry}
      >
        Check again
      </button>
    </div>
  );
}

type PersonaPhase = "edit" | "restarting" | "restart-error";

interface PersonaFieldErrors {
  agentMd?: string;
  userMd?: string;
}

function PersonaStep() {
  const announce = useAnnounce();
  const [draft] = useState(readDraft);
  const [persona, setPersona] = useState(() => draft.persona);
  const [errors, setErrors] = useState<PersonaFieldErrors>({});
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [phase, setPhase] = useState<PersonaPhase>("edit");
  const [pollError, setPollError] = useState<unknown>(null);
  const [pollKey, setPollKey] = useState(0);
  const [restartStartedAt, setRestartStartedAt] = useState<number | null>(null);

  const agentCharacters = characterCount(persona.agentMd);
  const userCharacters = characterCount(persona.userMd);

  useEffect(() => {
    if (phase !== "restarting") {
      return undefined;
    }
    let alive = true;
    const controller = new AbortController();
    pollDaemonRestart({
      startedAt: restartStartedAt ?? undefined,
      intervalMs: 1000,
      timeoutMs: 30000,
      signal: controller.signal,
    })
      .then((session) => {
        if (!alive) {
          return;
        }
        if (session && session.csrf) {
          setCSRF(session.csrf);
        }
        announce("The daemon is running again.");
        window.location.hash = "#/setup/ready";
      })
      .catch((error) => {
        if (!alive) {
          return;
        }
        setPollError(error);
        setPhase("restart-error");
        announce("The daemon did not answer. Try again.");
      });
    return () => {
      alive = false;
      controller.abort();
    };
  }, [phase, pollKey, restartStartedAt, announce]);

  function update(field: keyof SetupPersonaDraft, value: string) {
    const next = { ...persona, [field]: value };
    setPersona(next);
    const current = readDraft();
    writeDraft({ ...current, persona: next });
    setErrors((currentErrors) => ({ ...currentErrors, [field]: "" }));
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (saving || phase !== "edit") {
      return;
    }
    const nextErrors: PersonaFieldErrors = {};
    if (!persona.agentMd.trim()) {
      nextErrors.agentMd = "Write the persona before you continue.";
    }
    if (userCharacters > USER_LIMIT) {
      nextErrors.userMd = "Keep USER.md at or under 2,000 characters.";
    }
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) {
      announce("Check the persona fields and try again.");
      return;
    }

    const current = readDraft();
    setSaving(true);
    setSaveError(null);
    try {
      /* Read the restart baseline before the POST, so the wait only resolves
         for the daemon instance that setup starts. */
      const startedAt = await getDaemonStartedAt();
      setRestartStartedAt(startedAt);
      await saveSetup({
        provider: {
          name: current.provider,
          apiKey: current.connect.apiKey,
          baseUrl: current.connect.baseUrl.trim(),
          model: current.connect.model.trim(),
        },
        agent: {
          name: current.agent.name.trim(),
          template: current.agent.template,
          workspace: current.agent.workspace.trim(),
          permission: current.agent.permission,
        },
        persona: {
          agentMd: persona.agentMd,
          userMd: persona.userMd,
        },
      });
      writeSummary(summaryFor(current));
      clearDraft();
      setSaving(false);
      setPhase("restarting");
      announce("Setup saved. The daemon is restarting.");
    } catch (error) {
      setSaving(false);
      setSaveError(error);
      announce("Setup was not saved. " + errorMessage(error));
    }
  }

  function retryPoll() {
    setPollError(null);
    setPhase("restarting");
    setPollKey((value) => value + 1);
  }

  if (phase === "restarting" || phase === "restart-error") {
    return (
      <SetupShell step="persona" size="wide">
        <RestartPanel phase={phase} error={pollError} onRetry={retryPoll} />
      </SetupShell>
    );
  }

  const agentDescribedBy = [
    "setup-agent-md-hint",
    "setup-agent-md-count",
    errors.agentMd ? "setup-agent-md-error" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const userDescribedBy = [
    "setup-user-md-hint",
    "setup-user-md-count",
    errors.userMd ? "setup-user-md-error" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <SetupShell step="persona" size="wide">
      <StepHeading step="persona" title="Make it yours">
        Edit how the agent behaves and tell it about you. Both are plain Markdown
        files in the agent home, so you can change them any time.
      </StepHeading>

      <form className="setup-form setup-persona-form" onSubmit={submit}>
        <div className="setup-field">
          <div className="setup-label-row">
            <label className="setup-label" htmlFor="setup-agent-md">
              Persona · AGENT.md
            </label>
            <button
              type="button"
              className="link-button setup-reset"
              disabled={!TEMPLATE_PREVIEW_AVAILABLE}
              aria-describedby="setup-template-note"
            >
              Reset to template
            </button>
          </div>
          <textarea
            id="setup-agent-md"
            className="setup-textarea"
            rows={8}
            value={persona.agentMd}
            aria-required="true"
            aria-invalid={errors.agentMd ? "true" : undefined}
            aria-describedby={agentDescribedBy}
            onChange={(event) => update("agentMd", event.target.value)}
          ></textarea>
          <div className="setup-hint-row">
            <p id="setup-agent-md-hint" className="setup-hint">
              Write any tone, rules, or role.
            </p>
            <span id="setup-agent-md-count" className="setup-counter">
              {numberFormat.format(agentCharacters)} characters
            </span>
          </div>
          <p id="setup-template-note" className="setup-note">
            The template preview is not available yet. Write the persona
            yourself; you can edit it later in Settings.
          </p>
          {errors.agentMd ? (
            <p id="setup-agent-md-error" className="setup-field-error" role="alert">
              {errors.agentMd}
            </p>
          ) : null}
        </div>

        <div className="setup-field">
          <div className="setup-label-row">
            <label className="setup-label" htmlFor="setup-user-md">
              About you · USER.md
            </label>
            <span className="setup-label-note">Optional</span>
          </div>
          <textarea
            id="setup-user-md"
            className="setup-textarea"
            rows={8}
            value={persona.userMd}
            aria-invalid={errors.userMd ? "true" : undefined}
            aria-describedby={userDescribedBy}
            onChange={(event) => update("userMd", event.target.value)}
          ></textarea>
          <div className="setup-hint-row">
            <p id="setup-user-md-hint" className="setup-hint">
              The agent reads this on every turn. It adds facts it learns later.
            </p>
            <span id="setup-user-md-count" className="setup-counter">
              {numberFormat.format(userCharacters)} / 2,000
            </span>
          </div>
          {errors.userMd ? (
            <p id="setup-user-md-error" className="setup-field-error" role="alert">
              {errors.userMd}
            </p>
          ) : null}
        </div>

        <p className="setup-tip">
          Memory (MEMORY.md) fills itself as you chat. You can review and edit it
          in Settings › Agents.
        </p>

        {saveError ? (
          <div className="setup-save-error" role="alert">
            <ErrorNotice
              message={errorMessage(saveError)}
              details={saveError instanceof ApiError ? saveError.details : []}
            />
          </div>
        ) : null}

        <div className="setup-actions">
          <button
            type="button"
            className="button-ui button-ui-secondary"
            disabled={saving}
            onClick={() => go("agent")}
          >
            Back
          </button>
          <button
            type="submit"
            className="button-ui button-ui-primary"
            disabled={saving}
          >
            {saving ? "Saving…" : "Save and continue"}
          </button>
        </div>
      </form>
    </SetupShell>
  );
}

function ReadyStep() {
  const summary = readSummary() || summaryFor(readDraft());
  const permission =
    summary.permissionLabel || permissionById(summary.permission).summary;
  const rows: { label: string; value: string }[] = [
    {
      label: "Provider",
      value: summary.providerLabel || providerLabel(summary.provider),
    },
    { label: "Model", value: summary.model || "The provider default" },
    {
      label: "Agent",
      value:
        (summary.agentName || "default") +
        " · " +
        (summary.templateLabel || templateById(summary.template).label),
    },
    { label: "Tool permission", value: permission },
    { label: "Workspace", value: summary.workspace || "~/lana/workspace" },
  ];

  return (
    <SetupShell step="ready">
      <StepHeading step="ready" title="lana is ready">
        Your agent is set up. Start a session here or from the CLI. Connect
        Telegram and other channels later in Channels.
      </StepHeading>

      <div className="setup-summary">
        {rows.map((row) => (
          <div key={row.label} className="setup-summary-row">
            <span className="setup-summary-label">{row.label}</span>
            <span className="setup-summary-value">{row.value}</span>
          </div>
        ))}
      </div>

      <div className="setup-ready-actions">
        <a
          className="button-ui button-ui-primary setup-ready-primary"
          href="#/sessions/new"
        >
          Start your first session
        </a>
        <a
          className="button-ui button-ui-tertiary setup-ready-secondary"
          href="#/settings/general"
        >
          Open settings
        </a>
      </div>
    </SetupShell>
  );
}

export interface SetupWizardProps {
  step?: string;
}

export function SetupWizard({ step }: SetupWizardProps) {
  switch (step) {
    case "connect":
      return <ConnectStep />;
    case "agent":
      return <AgentStep />;
    case "persona":
      return <PersonaStep />;
    case "ready":
      return <ReadyStep />;
    case "provider":
    default:
      return <ProviderStep />;
  }
}
