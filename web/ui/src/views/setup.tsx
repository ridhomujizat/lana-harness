import { useEffect, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { ApiError, errorMessage, setCSRF } from "../api/client";
import { ErrorNotice, useAnnounce } from "../ui/ui";
import { Ic } from "../ui/icons";
import { Button, Field, Input, Textarea, cn } from "../ui/gr8r";
import {
  getDaemonStartedAt,
  getSetup,
  pollDaemonRestart,
  saveSetup,
} from "../api/setup";
import { CLAUDE_CODE_ID, presetById } from "./router/presets";
import { ProviderLogo } from "./router/provider-logo";
import { planLabel } from "./router/limits";
import { SetupShell, StepHeading, go } from "./setup-shell";
import { AddressLabel, ConnectStep, ModelStep, ROUTER_NOTE, providerFace } from "./setup-models";
import {
  clearDraft,
  isClaudeAddress,
  readDraft,
  readSummary,
  summaryFor,
  writeDraft,
  writeSummary,
} from "./setup-draft";
import type { SetupAgentDraft, SetupDraft, SetupPersonaDraft } from "./setup-draft";

export { SetupShell } from "./setup-shell";
export type { SetupShellProps } from "./setup-shell";

/* Setup wizard (REQ-WEB-032, REQ-WEB-033, REQ-WEB-034), Router onboarding:
   unlock → connect your models → pick a model → agent + persona → ready.
   The shell and the progress are shared with the Unlock step in
   views/login.tsx; the model steps live in views/setup-models.tsx. */

const USER_LIMIT = 2000;

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

const numberFormat = new Intl.NumberFormat("en-US");

function templateById(id: string): TemplatePreset {
  return TEMPLATE_BY_ID[id] || TEMPLATES[0];
}

function permissionById(id: string): PermissionPreset {
  return PERMISSION_BY_ID[id] || PERMISSIONS[0];
}

function characterCount(value: unknown): number {
  return Array.from(String(value || "")).length;
}

/* The API contract has no template preview endpoint, so the persona fields
   start empty and "Reset to template" stays disabled with a short note. */
const TEMPLATE_PREVIEW_AVAILABLE = false;

/* Returns the boolean `needed` flag of API-WEB-018. Errors are rethrown, not
   turned into `false`: the caller decides whether to show the wizard, a retry,
   or an error. A silent `false` would hide first-run setup when the daemon
   cannot answer. */
export async function setupNeeded(): Promise<boolean> {
  const status = await getSetup();
  return Boolean(status && status.needed);
}

/* The POST /v1/setup body of a draft (API-WEB-021 revision 3). */
function setupBody(draft: SetupDraft, persona: SetupPersonaDraft) {
  return {
    providers: draft.connections.map((connection) => {
      const preset = presetById(connection.id);
      return {
        name: connection.id,
        kind: preset ? preset.kind : "openai_compat",
        apiKey: connection.apiKey,
        baseUrl: connection.baseUrl,
        apiKeyEnv: preset ? preset.keyEnv : "",
      };
    }),
    model: draft.model,
    fallback: draft.fallback,
    backgroundModel: isClaudeAddress(draft.model) ? draft.background : "",
    agent: {
      name: draft.agent.name.trim(),
      template: draft.agent.template,
      workspace: draft.agent.workspace.trim(),
      permission: draft.agent.permission,
    },
    persona: {
      agentMd: persona.agentMd,
      userMd: persona.userMd,
    },
  };
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
        <Button variant="ghost" onClick={() => go("model")}>
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
      await saveSetup(setupBody(current, persona));
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

/* The ready summary (mockup 05, Router onboarding): what was connected,
   the models, the agent, and where to change them. */
function ReadyStep() {
  const summary = readSummary() || summaryFor(readDraft());
  const permission = permissionById(summary.permission).summary;
  const template = templateById(summary.template).label;
  const agentName = summary.agentName || "default";
  const names = (summary.claudeCode ? [CLAUDE_CODE_ID] : []).concat(summary.providers);
  const account = summary.claudeAccount;
  const rows: { icon: string; label: string; value: ReactNode }[] = [
    {
      icon: "server",
      label: "Connected",
      value: names.length ? (
        <span className="setup-logos">
          {names.map((name) => {
            const face = providerFace(name);
            return (
              <span key={name} className="setup-logo-chip">
                <ProviderLogo logo={face.logo} name={face.label} size={16} />
                {face.label}
              </span>
            );
          })}
        </span>
      ) : (
        "Nothing yet"
      ),
    },
    {
      icon: "cpu",
      label: "Main model",
      value: summary.model ? <AddressLabel address={summary.model} /> : "Not set",
    },
  ];
  if (summary.fallback) {
    rows.push({
      icon: "layers",
      label: "Fallback · combo default",
      value: (
        <span className="setup-chain">
          <AddressLabel address={summary.model} />
          <Ic name="arrow-right" size={12} />
          <AddressLabel address={summary.fallback} />
        </span>
      ),
    });
  }
  if (isClaudeAddress(summary.model)) {
    rows.push({
      icon: "clock",
      label: "Background model",
      value: summary.background ? (
        <AddressLabel address={summary.background} />
      ) : (
        "Skipped until you connect an API key"
      ),
    });
  }
  if (summary.claudeCode) {
    rows.push({
      icon: "user",
      label: "Claude Code account",
      value: ["Signed in", account && account.email, account && account.plan ? planLabel(account.plan) : ""]
        .filter(Boolean)
        .join(" · "),
    });
  }
  rows.push(
    { icon: "bot", label: "Agent", value: agentName + " · " + template },
    { icon: "shield-check", label: "Tool permission", value: permission },
    { icon: "folder", label: "Workspace", value: summary.workspace || "~/lana/workspace" }
  );

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
      <p className="hint setup-ready-note">{ROUTER_NOTE}</p>
      <div className="setup-ready-links">
        <a className="link" href="#/router/providers">
          Open Router
        </a>
        <a className="link" href={"#/agents/" + encodeURIComponent(agentName)}>
          Open agent {agentName}
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
    case "model":
      return <ModelStep />;
    case "agent":
      return <AgentStep />;
    case "persona":
      return <PersonaStep />;
    case "ready":
      return <ReadyStep />;
    case "connect":
    case "provider":
    default:
      return <ConnectStep />;
  }
}
