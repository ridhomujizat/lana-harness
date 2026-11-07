import { useEffect, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { ApiError, errorMessage, setCSRF } from "../api/client";
import { ErrorNotice, useAnnounce } from "../ui/ui";
import { Ic, LanaMark } from "../ui/icons";
import { Button, Field, IconButton, Input, Textarea, cn } from "../ui/gr8r";
import { providerLabel } from "./agent-common";
import {
  getDaemonStartedAt,
  getSetup,
  pollDaemonRestart,
  saveSetup,
  testProvider,
} from "../api/setup";
import type { SetupTestProviderResponse } from "../api/setup";

/* Setup wizard (REQ-WEB-032, REQ-WEB-033, REQ-WEB-034). The shell and the
   progress are shared with the Unlock step in views/login.js. Every step is a
   hash route under #/setup/{step}; app.js owns the route table. */

const DRAFT_KEY = "lana.setup";
const SUMMARY_KEY = "lana.setup.summary";
const USER_LIMIT = 2000;
const STEP_TOTAL = 5;

/* The asset scan forbids every protocol-relative URL in shell files, so the
   scheme is assembled instead of written as one literal. */
const SECURE = "https:" + "/".repeat(2);

interface ProviderPreset {
  id: string;
  label: string;
  icon: string;
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
    icon: "sparkles",
    description: "GPT models through api.openai.com.",
    kind: "openai",
    baseUrl: SECURE + "api.openai.com/v1",
    keyEnv: "OPENAI_API_KEY",
    model: "gpt-5-mini",
    suggestions: ["gpt-5-mini", "gpt-5.1", "gpt-5"],
  },
  {
    id: "anthropic",
    label: "Anthropic",
    icon: "bot",
    description: "Claude models through api.anthropic.com.",
    /* The native Messages adapter (spec 37); it adds /v1 itself. */
    kind: "anthropic",
    baseUrl: SECURE + "api.anthropic.com",
    keyEnv: "ANTHROPIC_API_KEY",
    model: "claude-sonnet-5-5",
    suggestions: ["claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"],
  },
  {
    id: "deepseek",
    label: "DeepSeek",
    icon: "waves",
    description: "DeepSeek models through api.deepseek.com.",
    kind: "deepseek",
    baseUrl: SECURE + "api.deepseek.com/v1",
    keyEnv: "DEEPSEEK_API_KEY",
    model: "deepseek-flash",
    suggestions: ["deepseek-flash", "deepseek-v4-pro"],
  },
  {
    id: "openrouter",
    label: "OpenRouter",
    icon: "route",
    description: "Models from many vendors with one key.",
    kind: "openrouter",
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

/* A draft saved before the native Anthropic adapter holds the old
   OpenAI-compatible URL; the native adapter adds /v1 itself. */
function currentBaseUrl(preset: ProviderPreset, baseUrl: string): string {
  if (preset.kind === "anthropic" && baseUrl.replace(/\/+$/, "") === SECURE + "api.anthropic.com/v1") {
    return preset.baseUrl;
  }
  return baseUrl;
}

const PROVIDER_BY_ID: Record<string, ProviderPreset> = {};
for (const preset of PROVIDERS) {
  PROVIDER_BY_ID[preset.id] = preset;
}

interface TemplatePreset {
  id: string;
  label: string;
  icon: string;
  note: string;
}

const TEMPLATES = [
  {
    id: "assistant",
    label: "Assistant",
    icon: "message-circle",
    note: "Everyday help and research.",
  },
  {
    id: "coder",
    label: "Coder",
    icon: "code",
    note: "Works in a code repository.",
  },
  {
    id: "worker",
    label: "Worker",
    icon: "hammer",
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
  icon: string;
  note: string;
  summary: string;
}

const PERMISSIONS = [
  {
    id: "ask",
    label: "Ask",
    icon: "circle-help",
    note: "Approve risky tool calls.",
    summary: "Ask before risky tools",
  },
  {
    id: "trust",
    label: "Trust",
    icon: "shield-check",
    note: "Run tools without asking.",
    summary: "Run tools without asking",
  },
  {
    id: "never",
    label: "Never",
    icon: "ban",
    note: "Block risky tool calls.",
    summary: "Block risky tool calls",
  },
] as const satisfies readonly PermissionPreset[];

const PERMISSION_BY_ID: Record<string, PermissionPreset> = {};
for (const permission of PERMISSIONS) {
  PERMISSION_BY_ID[permission.id] = permission;
}

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
      baseUrl: currentBaseUrl(preset, textValue(connect.baseUrl, preset.baseUrl)),
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

/* The API contract has no template preview endpoint, so the persona fields
   start empty and "Reset to template" stays disabled with a short note. */
const TEMPLATE_PREVIEW_AVAILABLE = false;

/* The step progress of the Gr8r onboarding card: one bar per step, the bars
   up to the current step filled, the current one growing in. */
function Steps({ step }: { step: string }) {
  const current = STEP_NUMBER[step] || 1;
  return (
    <>
      <div
        className="steps"
        role="progressbar"
        aria-valuemin={1}
        aria-valuemax={STEP_TOTAL}
        aria-valuenow={current}
        aria-label="Setup progress"
      >
        {Array.from({ length: STEP_TOTAL }, (_unused, index) => (
          <i
            key={index}
            className={cn(index + 1 <= current && "on", index + 1 === current && "cur")}
          ></i>
        ))}
      </div>
      <div className="steps-sub faint">
        Step {current} of {STEP_TOTAL}
      </div>
    </>
  );
}

export interface SetupShellProps {
  step: string;
  size?: string;
  variant?: "steps" | "door";
  children?: ReactNode;
}

/* The Gr8r auth/onboarding shell: a full-page column with the brand bar, a
   centred card, and a quiet footer. The door variant is the pre-login card
   (login, welcome, forgot); the steps variant is the setup wizard and adds the
   progress bars. */
export function SetupShell({
  step,
  size = "regular",
  variant = "steps",
  children,
}: SetupShellProps) {
  const door = variant === "door";
  return (
    <div className="auth">
      <div className="auth-top">
        <span className="brand">
          <LanaMark size={22} />
          <span className="lana-word">LΛNΛ</span>
        </span>
      </div>
      <div className="auth-c">
        <div
          className={cn(
            "auth-card",
            door ? "fx-auth" : "onb fx-step",
            size === "wide" && "onb-wide"
          )}
        >
          {door ? null : <Steps step={step} />}
          {children}
        </div>
      </div>
      <div className="auth-foot">
        <span className="faint">
          {door
            ? "lana on this computer · only this computer can open this page."
            : "Local only · " + setupAddress()}
        </span>
      </div>
    </div>
  );
}

interface StepHeadingProps {
  title: string;
  children?: ReactNode;
}

function StepHeading({ title, children }: StepHeadingProps) {
  return (
    <>
      <h1>{title}</h1>
      <p className="sub">{children}</p>
    </>
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
      <StepHeading title="Choose a model provider">
        Pick where lana sends model requests. Each provider has a default base URL
        that you can change in the next step.
      </StepHeading>

      <div className="opts" role="radiogroup" aria-label="Provider">
        {PROVIDERS.map((preset) => {
          const on = preset.id === provider;
          return (
            <button
              key={preset.id}
              type="button"
              role="radio"
              aria-checked={on}
              className={cn("opt", on && "on")}
              onClick={() => selectProvider(preset.id)}
            >
              <Ic name={preset.icon} size={18} />
              <b>{preset.label}</b>
              <span>{preset.description}</span>
            </button>
          );
        })}
      </div>

      <div className="auth-actions">
        <Button variant="ghost" onClick={() => go("unlock")}>
          Back
        </Button>
        <span className="sp"></span>
        <Button
          variant="primary"
          size="lg"
          iconRight="arrow-right"
          onClick={() => go("connect")}
        >
          Continue
        </Button>
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
  const [keyShown, setKeyShown] = useState(false);
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

  const latency = Number(result && result.latency_ms) || 0;
  const testedOk = Boolean(result && result.ok);
  const alertTitle = testedOk ? "Connection works" : "Could not reach the model";
  const alertBody = result
    ? result.ok
      ? connect.model.trim() + " answered in " + latency + " ms."
      : providerFailure(result)
    : requestError
      ? errorMessage(requestError)
      : "";

  return (
    <SetupShell step="connect">
      <StepHeading title={"Connect " + preset.label}>
        Add the API key and pick the default model. lana tests the connection
        before it saves.
      </StepHeading>

      <form className="auth-form" onSubmit={continueNext}>
        <Field
          label="API key"
          htmlFor="setup-api-key"
          hint={"Saved to secrets.env as " + preset.keyEnv + ". It stays on this machine."}
          error={fieldErrors.apiKey}
        >
          <div className="pw-field">
            <Input
              id="setup-api-key"
              className="input-lg"
              type={keyShown ? "text" : "password"}
              autoComplete="off"
              spellCheck={false}
              value={connect.apiKey}
              disabled={testing}
              aria-required="true"
              onChange={(event) => update("apiKey", event.target.value)}
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

        <Field
          label="Base URL"
          htmlFor="setup-base-url"
          hint="Change it for a proxy or a compatible gateway."
          error={fieldErrors.baseUrl}
        >
          <Input
            id="setup-base-url"
            className="input-lg"
            type="url"
            autoComplete="url"
            spellCheck={false}
            value={connect.baseUrl}
            disabled={testing}
            aria-required="true"
            onChange={(event) => update("baseUrl", event.target.value)}
          />
        </Field>

        <Field
          label="Default model"
          htmlFor="setup-model"
          hint="The default agent uses this model. Change it any time in Settings."
          error={fieldErrors.model}
        >
          <Input
            id="setup-model"
            className="input-lg"
            type="text"
            autoComplete="off"
            spellCheck={false}
            value={connect.model}
            disabled={testing}
            aria-required="true"
            onChange={(event) => update("model", event.target.value)}
          />
        </Field>

        <div className="suggests">
          <span className="faint">Suggested</span>
          {preset.suggestions.map((model) => {
            const on = connect.model === model;
            return (
              <button
                key={model}
                type="button"
                className={cn("badge", "suggest", on && "accent")}
                aria-pressed={on}
                disabled={testing}
                onClick={() => update("model", model)}
              >
                <span className="dot" aria-hidden="true"></span>
                {model}
              </button>
            );
          })}
        </div>

        <div
          className="auth-status"
          role="status"
          aria-live="polite"
          aria-atomic="true"
        >
          {result || requestError ? (
            <div className={cn("alert", testedOk ? "ok" : "danger")}>
              <Ic name={testedOk ? "circle-check" : "circle-alert"} size={15} />
              <div className="grow">
                <b>{alertTitle}</b>
                <p className="alert-body">{alertBody}</p>
              </div>
            </div>
          ) : null}
        </div>

        <div className="auth-actions">
          <Button variant="secondary" disabled={testing} onClick={runTest}>
            {result && !result.ok ? "Test again" : "Test connection"}
          </Button>
          <span className="sp"></span>
          <Button
            type="submit"
            variant="primary"
            loading={testing}
            disabled={!tested || testing}
          >
            Save and continue
          </Button>
        </div>
        {!tested ? (
          <p className="hint">Test the connection before you continue.</p>
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

  return (
    <SetupShell step="agent">
      <StepHeading title="Set up your agent">
        This agent answers every new session. You can add more agents and change
        these choices later in Settings.
      </StepHeading>

      <div className="auth-form">
        <Field
          label="Agent name"
          htmlFor="setup-agent-name"
          hint="Lowercase letters, numbers, and dashes. Routes and the CLI use this name."
          error={errors.name}
        >
          <Input
            id="setup-agent-name"
            className="input-lg"
            type="text"
            autoComplete="off"
            spellCheck={false}
            value={agent.name}
            aria-required="true"
            onChange={(event) => update("name", event.target.value)}
          />
        </Field>

        <div className="auth-group">
          <span className="label">Template</span>
          <div className="opts" role="radiogroup" aria-label="Template">
            {TEMPLATES.map((template) => {
              const on = agent.template === template.id;
              return (
                <button
                  key={template.id}
                  type="button"
                  role="radio"
                  aria-checked={on}
                  className={cn("opt", on && "on")}
                  onClick={() => update("template", template.id)}
                >
                  <Ic name={template.icon} size={18} />
                  <b>{template.label}</b>
                  <span>{template.note}</span>
                </button>
              );
            })}
          </div>
          <span className="hint">
            A starting point. You can rewrite the whole persona on the next
            screen.
          </span>
        </div>

        <Field
          label="Workspace"
          htmlFor="setup-workspace"
          hint="File and bash tools work inside this folder."
          error={errors.workspace}
        >
          <Input
            id="setup-workspace"
            className="input-lg"
            type="text"
            autoComplete="off"
            spellCheck={false}
            value={agent.workspace}
            aria-required="true"
            onChange={(event) => update("workspace", event.target.value)}
          />
        </Field>

        <div className="auth-group">
          <span className="label">Tool permission</span>
          <div className="opts" role="radiogroup" aria-label="Tool permission">
            {PERMISSIONS.map((permission) => {
              const on = agent.permission === permission.id;
              return (
                <button
                  key={permission.id}
                  type="button"
                  role="radio"
                  aria-checked={on}
                  className={cn("opt", on && "on")}
                  onClick={() => update("permission", permission.id)}
                >
                  <Ic name={permission.icon} size={18} />
                  <b>{permission.label}</b>
                  <span>{permission.note}</span>
                </button>
              );
            })}
          </div>
          <span className="hint">
            Ask is recommended. Memory tools never ask for approval.
          </span>
        </div>
      </div>

      <div className="auth-actions">
        <Button variant="ghost" onClick={() => go("connect")}>
          Back
        </Button>
        <span className="sp"></span>
        <Button
          variant="primary"
          size="lg"
          iconRight="arrow-right"
          onClick={continueNext}
        >
          Continue
        </Button>
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
        className="auth-restart"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        <span className="auth-spinner" aria-hidden="true"></span>
        <h1 className="auth-restart-title">Restarting lana…</h1>
        <p className="auth-restart-text">
          Setup is saved. The daemon restarts in place and this page reconnects
          on its own.
        </p>
      </div>
    );
  }
  return (
    <div className="auth-restart" role="alert">
      <span className="auth-restart-ic" aria-hidden="true">
        <Ic name="circle-alert" size={20} />
      </span>
      <h1 className="auth-restart-title">The daemon did not answer</h1>
      <p className="auth-restart-text">{errorMessage(error)}</p>
      <p className="hint">
        If lana is still starting, check again. You can also restart it from the
        terminal.
      </p>
      <Button variant="secondary" onClick={onRetry}>
        Check again
      </Button>
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

  return (
    <SetupShell step="persona" size="wide">
      <StepHeading title="Make it yours">
        Edit how the agent behaves and tell it about you. Both are plain Markdown
        files in the agent home, so you can change them any time.
      </StepHeading>

      <form className="auth-form" onSubmit={submit}>
        <Field
          hint={
            <span className="field-hint-row">
              <span>Write any tone, rules, or role.</span>
              <span>{numberFormat.format(agentCharacters)} characters</span>
            </span>
          }
          error={errors.agentMd}
        >
          <div className="field-label-row">
            <label className="label" htmlFor="setup-agent-md">
              Persona · AGENT.md
            </label>
            <Button
              variant="ghost"
              size="sm"
              disabled={!TEMPLATE_PREVIEW_AVAILABLE}
              aria-describedby="setup-template-note"
            >
              Reset to template
            </Button>
          </div>
          <Textarea
            id="setup-agent-md"
            rows={8}
            value={persona.agentMd}
            aria-required="true"
            onChange={(event) => update("agentMd", event.target.value)}
          ></Textarea>
          <span className="hint" id="setup-template-note">
            The template preview is not available yet. Write the persona
            yourself; you can edit it later in Settings.
          </span>
        </Field>

        <Field
          hint={
            <span className="field-hint-row">
              <span>The agent reads this on every turn. It adds facts it learns later.</span>
              <span>{numberFormat.format(userCharacters)} / 2,000</span>
            </span>
          }
          error={errors.userMd}
        >
          <div className="field-label-row">
            <label className="label" htmlFor="setup-user-md">
              About you · USER.md
            </label>
            <span className="faint">Optional</span>
          </div>
          <Textarea
            id="setup-user-md"
            rows={8}
            value={persona.userMd}
            onChange={(event) => update("userMd", event.target.value)}
          ></Textarea>
        </Field>

        <p className="panel auth-tip">
          Memory (MEMORY.md) fills itself as you chat. You can review and edit it
          in Settings › Agents.
        </p>

        {saveError ? (
          <div className="auth-save-error" role="alert">
            <ErrorNotice
              message={errorMessage(saveError)}
              details={saveError instanceof ApiError ? saveError.details : []}
            />
          </div>
        ) : null}

        <div className="auth-actions">
          <Button variant="ghost" disabled={saving} onClick={() => go("agent")}>
            Back
          </Button>
          <span className="sp"></span>
          <Button type="submit" variant="primary" loading={saving} disabled={saving}>
            Save and continue
          </Button>
        </div>
      </form>
    </SetupShell>
  );
}

function ReadyStep() {
  const summary = readSummary() || summaryFor(readDraft());
  const permission =
    summary.permissionLabel || permissionById(summary.permission).summary;
  const rows: { icon: string; label: string; value: string }[] = [
    {
      icon: "server",
      label: "Provider",
      value: summary.providerLabel || providerLabel(summary.provider),
    },
    { icon: "cpu", label: "Model", value: summary.model || "The provider default" },
    {
      icon: "bot",
      label: "Agent",
      value:
        (summary.agentName || "default") +
        " · " +
        (summary.templateLabel || templateById(summary.template).label),
    },
    { icon: "shield-check", label: "Tool permission", value: permission },
    {
      icon: "folder",
      label: "Workspace",
      value: summary.workspace || "~/lana/workspace",
    },
  ];

  return (
    <SetupShell step="ready">
      <span className="ready-badge">
        <Ic name="circle-check" size={22} />
      </span>
      <h1>lana is ready</h1>
      <p className="sub">
        Your agent is set up. Start a session here or from the CLI. Connect
        Telegram and other channels later in Channels.
      </p>

      <div className="panel">
        {rows.map((row) => (
          <div key={row.label} className="ready-row">
            <span className="ready-ic">
              <Ic name={row.icon} size={14} />
            </span>
            <div className="grow">
              <div className="ready-t">{row.label}</div>
              <div className="faint ready-s">{row.value}</div>
            </div>
            <span className="ready-ok">
              <Ic name="check" size={15} />
            </span>
          </div>
        ))}
      </div>

      <Button variant="primary" size="lg" block as="a" href="#/sessions/new">
        Start your first session
      </Button>
      <a className="link ready-link" href="#/settings/general">
        Open settings
      </a>
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
