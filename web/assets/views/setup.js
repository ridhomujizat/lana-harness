import { html, useEffect, useState } from "/assets/vendor/preact-htm.js";
import { ApiError, errorMessage, setCSRF } from "/assets/api.js";
import { ErrorNotice, useAnnounce } from "/assets/ui.js";
import { Icon, providerLabel } from "/assets/views/agent-common.js";
import {
  getDaemonStartedAt,
  getSetup,
  pollDaemonRestart,
  saveSetup,
  testProvider,
} from "/assets/api/setup.js";

/* Setup wizard (REQ-WEB-032, REQ-WEB-033, REQ-WEB-034). The shell and the
   progress rail are shared with the Unlock step in views/login.js. Every step
   is a hash route under #/setup/{step}; app.js owns the route table. */

const DRAFT_KEY = "lana.setup";
const SUMMARY_KEY = "lana.setup.summary";
const USER_LIMIT = 2000;

/* The asset scan forbids every protocol-relative URL in shell files, so the
   scheme is assembled instead of written as one literal. */
const SECURE = "https:" + "/".repeat(2);

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
];

const PROVIDER_BY_ID = {};
for (const preset of PROVIDERS) {
  PROVIDER_BY_ID[preset.id] = preset;
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
];

const TEMPLATE_BY_ID = {};
for (const template of TEMPLATES) {
  TEMPLATE_BY_ID[template.id] = template;
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
];

const PERMISSION_BY_ID = {};
for (const permission of PERMISSIONS) {
  PERMISSION_BY_ID[permission.id] = permission;
}

const RAIL_STEPS = [
  {
    title: "Unlock the dashboard",
    note: "Paste the owner token from the daemon",
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

const RAIL_INDEX = {
  unlock: 0,
  provider: 1,
  connect: 2,
  agent: 3,
  persona: 3,
  ready: 4,
};

const STEP_NUMBER = {
  unlock: 1,
  provider: 2,
  connect: 3,
  agent: 4,
  persona: 4,
  ready: 5,
};

const numberFormat = new Intl.NumberFormat("en-US");

/* sessionStorage holds the draft across routes. A module-level copy keeps the
   wizard working when storage is disabled; it is cleared with the stored copy
   after a successful save. */
let memoryDraft = null;
let memorySummary = null;

function textValue(value, fallback) {
  return typeof value === "string" ? value : fallback;
}

function providerById(id) {
  return PROVIDER_BY_ID[id] || PROVIDERS[0];
}

function templateById(id) {
  return TEMPLATE_BY_ID[id] || TEMPLATES[0];
}

function permissionById(id) {
  return PERMISSION_BY_ID[id] || PERMISSIONS[0];
}

function defaultConnect(providerId) {
  const preset = providerById(providerId);
  return {
    apiKey: "",
    baseUrl: preset.baseUrl,
    model: preset.model,
  };
}

function defaultDraft() {
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

function normalizeDraft(value) {
  const fallback = defaultDraft();
  if (!value || typeof value !== "object") {
    return fallback;
  }
  const provider = PROVIDER_BY_ID[value.provider]
    ? value.provider
    : fallback.provider;
  const preset = providerById(provider);
  const connect = value.connect && typeof value.connect === "object" ? value.connect : {};
  const agent = value.agent && typeof value.agent === "object" ? value.agent : {};
  const persona = value.persona && typeof value.persona === "object" ? value.persona : {};
  return {
    provider,
    connect: {
      apiKey: textValue(connect.apiKey, ""),
      baseUrl: textValue(connect.baseUrl, preset.baseUrl),
      model: textValue(connect.model, preset.model),
    },
    agent: {
      name: textValue(agent.name, fallback.agent.name),
      template: TEMPLATE_BY_ID[agent.template]
        ? agent.template
        : fallback.agent.template,
      workspace: textValue(agent.workspace, fallback.agent.workspace),
      permission: PERMISSION_BY_ID[agent.permission]
        ? agent.permission
        : fallback.agent.permission,
    },
    persona: {
      agentMd: textValue(persona.agentMd, ""),
      userMd: textValue(persona.userMd, ""),
    },
  };
}

function readDraft() {
  if (memoryDraft) {
    return memoryDraft;
  }
  let parsed = null;
  try {
    const raw = window.sessionStorage.getItem(DRAFT_KEY);
    if (raw) {
      parsed = JSON.parse(raw);
    }
  } catch (cause) {
    /* Storage can be disabled; the in-memory copy still carries this page. */
  }
  memoryDraft = normalizeDraft(parsed);
  return memoryDraft;
}

function writeDraft(next) {
  memoryDraft = normalizeDraft(next);
  try {
    window.sessionStorage.setItem(DRAFT_KEY, JSON.stringify(memoryDraft));
  } catch (cause) {
    /* Keep the in-memory copy when storage is unavailable. */
  }
  return memoryDraft;
}

function clearDraft() {
  memoryDraft = null;
  try {
    window.sessionStorage.removeItem(DRAFT_KEY);
  } catch (cause) {
    /* Nothing else to clear. */
  }
}

function readSummary() {
  if (memorySummary) {
    return memorySummary;
  }
  try {
    const raw = window.sessionStorage.getItem(SUMMARY_KEY);
    if (raw) {
      memorySummary = JSON.parse(raw);
      return memorySummary;
    }
  } catch (cause) {
    /* Fall through to the draft. */
  }
  return null;
}

function writeSummary(summary) {
  memorySummary = summary;
  try {
    window.sessionStorage.setItem(SUMMARY_KEY, JSON.stringify(summary));
  } catch (cause) {
    /* The current page still has the summary in memory. */
  }
}

function summaryFor(draft) {
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

function characterCount(value) {
  return Array.from(String(value || "")).length;
}

function go(step) {
  window.location.hash = "#/setup/" + step;
}

function setupAddress() {
  return window.location.host || "127.0.0.1:18300";
}

function isHttpUrl(value) {
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:";
  } catch (cause) {
    return false;
  }
}

function providerFailure(result) {
  if (result && typeof result.message === "string" && result.message) {
    return result.message;
  }
  const status = Number(result && result.status);
  if (Number.isFinite(status) && status > 0) {
    return "The provider answered with status " + status + ".";
  }
  return "The provider did not answer.";
}

function stepEyebrow(step) {
  if (step === "persona") {
    return "Step 4 of 5 · Persona";
  }
  return "Step " + STEP_NUMBER[step] + " of 5";
}

/* The API contract has no template preview endpoint, so the persona fields
   start empty and "Reset to template" stays disabled with a short note. */
const TEMPLATE_PREVIEW_AVAILABLE = false;

function ProgressStep({ state, title, note, connector }) {
  const stateLabel =
    state === "complete"
      ? "Complete"
      : state === "current"
        ? "Current step"
        : "Not started";
  return html`<li
    class=${"setup-step is-" + state}
    aria-current=${state === "current" ? "step" : undefined}
  >
    <span class="setup-step-marker" aria-hidden="true">
      <span class="setup-step-icon">
        ${state === "complete"
          ? html`<${Icon} name="check" className="setup-check-icon" />`
          : html`<span class="setup-step-dot"></span>`}
      </span>
      ${connector ? html`<span class="setup-step-connector"></span>` : null}
    </span>
    <span class="setup-step-copy">
      <span class="sr-only">${stateLabel}: </span>
      <span class="setup-step-title">${title}</span>
      <span class="setup-step-note">${note}</span>
    </span>
  </li>`;
}

export function SetupShell({ step, size = "regular", children }) {
  const current = RAIL_INDEX[step] || 0;
  const contentClass =
    size === "wide"
      ? " setup-content-wide"
      : size === "agent"
        ? " setup-content-agent"
        : "";
  return html`<div class="setup-shell">
    <aside class="setup-rail" aria-label="Setup progress">
      <div class="setup-rail-top">
        <a class="logo-lockup setup-logo" href="#/setup/unlock" aria-label="lana setup">
          <img
            class="logo-mark"
            src="/assets/icons/logo-mark.svg"
            alt=""
            aria-hidden="true"
          />
          <span class="logo-word">LΛNΛ</span>
        </a>
        <ol class="setup-steps">
          ${RAIL_STEPS.map((entry, index) => {
            const state =
              index < current ? "complete" : index === current ? "current" : "incomplete";
            const note =
              step === "persona" && index === 3
                ? "Persona and your profile"
                : entry.note;
            return html`<${ProgressStep}
              key=${entry.title}
              state=${state}
              title=${entry.title}
              note=${note}
              connector=${index < RAIL_STEPS.length - 1}
            />`;
          })}
        </ol>
      </div>
      <div class="setup-rail-footer">
        <span class="setup-local">Local only · ${setupAddress()}</span>
        <span class="setup-help">Help</span>
      </div>
    </aside>
    <main id="main" class="setup-main">
      <div class=${"setup-content" + contentClass}>${children}</div>
    </main>
  </div>`;
}

function StepHeading({ step, title, children }) {
  return html`<div class="setup-heading">
    <span class="setup-eyebrow">${stepEyebrow(step)}</span>
    <h1 class="setup-title">${title}</h1>
    <p class="setup-lede">${children}</p>
  </div>`;
}

/* Returns the boolean `needed` flag of API-WEB-018. Errors are rethrown, not
   turned into `false`: the caller decides whether to show the wizard, a retry,
   or an error. A silent `false` would hide first-run setup when the daemon
   cannot answer. */
export async function setupNeeded() {
  const status = await getSetup();
  return Boolean(status && status.needed);
}

function ProviderStep() {
  const announce = useAnnounce();
  const [provider, setProvider] = useState(() => readDraft().provider);

  function selectProvider(id) {
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

  return html`<${SetupShell} step="provider">
    <${StepHeading} step="provider" title="Choose a model provider">
      Pick where lana sends model requests. Each provider has a default base URL
      that you can change in the next step.
    </${StepHeading}>

    <fieldset class="setup-group">
      <legend class="sr-only">Provider</legend>
      <div class="setup-provider-list">
        ${PROVIDERS.map((preset) => {
          const selected = preset.id === provider;
          return html`<label
            key=${preset.id}
            class=${"agent-permission-tile setup-provider-card" + (selected ? " is-selected" : "")}
          >
            <input
              class="agent-permission-input"
              type="radio"
              name="setup-provider"
              value=${preset.id}
              checked=${selected}
              onChange=${() => selectProvider(preset.id)}
            />
            <span class="setup-provider-logo" aria-hidden="true">${preset.initials}</span>
            <span class="agent-permission-text setup-provider-text">
              <span class="agent-permission-title">${preset.label}</span>
              <span class="agent-permission-note">${preset.description}</span>
              <span class="setup-provider-meta">${preset.kind} · ${preset.baseUrl}</span>
            </span>
            <span class="agent-permission-radio" aria-hidden="true">
              <span class="agent-permission-dot"></span>
            </span>
          </label>`;
        })}
      </div>
    </fieldset>

    <div class="setup-actions">
      <button
        type="button"
        class="button-ui button-ui-secondary"
        onClick=${() => go("unlock")}
      >Back</button>
      <button
        type="button"
        class="button-ui button-ui-primary"
        onClick=${() => go("connect")}
      >Continue</button>
    </div>
  </${SetupShell}>`;
}

function ConnectStep() {
  const announce = useAnnounce();
  const [draft] = useState(readDraft);
  const preset = providerById(draft.provider);
  const [connect, setConnect] = useState(() => draft.connect);
  const [fieldErrors, setFieldErrors] = useState({});
  const [result, setResult] = useState(null);
  const [requestError, setRequestError] = useState(null);
  const [testing, setTesting] = useState(false);
  const [tested, setTested] = useState(false);

  function update(field, value) {
    const next = { ...connect, [field]: value };
    setConnect(next);
    const current = readDraft();
    writeDraft({ ...current, connect: next });
    setFieldErrors((errors) => ({ ...errors, [field]: "" }));
    setResult(null);
    setRequestError(null);
    setTested(false);
  }

  function validate() {
    const errors = {};
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

  function continueNext(event) {
    event.preventDefault();
    if (!tested || testing) {
      return;
    }
    go("agent");
  }

  const apiKeyDescribedBy = ["setup-api-key-hint", fieldErrors.apiKey ? "setup-api-key-error" : ""]
    .filter(Boolean)
    .join(" ");
  const baseUrlDescribedBy = ["setup-base-url-hint", fieldErrors.baseUrl ? "setup-base-url-error" : ""]
    .filter(Boolean)
    .join(" ");
  const modelDescribedBy = ["setup-model-hint", fieldErrors.model ? "setup-model-error" : ""]
    .filter(Boolean)
    .join(" ");

  const latency = Number(result && result.latency_ms) || 0;
  const alertTone = result && result.ok ? "is-success" : "is-error";
  const alertTitle = result && result.ok ? "Connection works" : "Could not reach the model";
  const alertBody = result
    ? result.ok
      ? connect.model.trim() + " answered in " + latency + " ms."
      : providerFailure(result)
    : requestError
      ? errorMessage(requestError)
      : "";

  return html`<${SetupShell} step="connect">
    <${StepHeading} step="connect" title=${"Connect " + preset.label}>
      Add the API key and pick the default model. lana tests the connection
      before it saves.
    </${StepHeading}>

    <form class="setup-form" onSubmit=${continueNext}>
      <div class="setup-field">
        <label class="setup-label" for="setup-api-key">API key</label>
        <input
          id="setup-api-key"
          type="password"
          autocomplete="off"
          spellcheck=${false}
          value=${connect.apiKey}
          disabled=${testing}
          aria-required="true"
          aria-invalid=${fieldErrors.apiKey ? "true" : undefined}
          aria-describedby=${apiKeyDescribedBy}
          onInput=${(event) => update("apiKey", event.target.value)}
        />
        <p id="setup-api-key-hint" class="setup-hint">
          Saved to secrets.env as ${preset.keyEnv}. It stays on this machine.
        </p>
        ${fieldErrors.apiKey
          ? html`<p id="setup-api-key-error" class="setup-field-error" role="alert">
              ${fieldErrors.apiKey}
            </p>`
          : null}
      </div>

      <div class="setup-field">
        <label class="setup-label" for="setup-base-url">Base URL</label>
        <input
          id="setup-base-url"
          type="url"
          autocomplete="url"
          spellcheck=${false}
          value=${connect.baseUrl}
          disabled=${testing}
          aria-required="true"
          aria-invalid=${fieldErrors.baseUrl ? "true" : undefined}
          aria-describedby=${baseUrlDescribedBy}
          onInput=${(event) => update("baseUrl", event.target.value)}
        />
        <p id="setup-base-url-hint" class="setup-hint">
          Change it for a proxy or a compatible gateway.
        </p>
        ${fieldErrors.baseUrl
          ? html`<p id="setup-base-url-error" class="setup-field-error" role="alert">
              ${fieldErrors.baseUrl}
            </p>`
          : null}
      </div>

      <div class="setup-field">
        <label class="setup-label" for="setup-model">Default model</label>
        <input
          id="setup-model"
          type="text"
          autocomplete="off"
          spellcheck=${false}
          value=${connect.model}
          disabled=${testing}
          aria-required="true"
          aria-invalid=${fieldErrors.model ? "true" : undefined}
          aria-describedby=${modelDescribedBy}
          onInput=${(event) => update("model", event.target.value)}
        />
        <p id="setup-model-hint" class="setup-hint">
          The default agent uses this model. Change it any time in Settings.
        </p>
        ${fieldErrors.model
          ? html`<p id="setup-model-error" class="setup-field-error" role="alert">
              ${fieldErrors.model}
            </p>`
          : null}
        <div class="setup-suggestions">
          <span class="setup-suggestions-label">Suggested:</span>
          ${preset.suggestions.map(
            (model) => html`<button
              key=${model}
              type="button"
              class=${"badge-ui setup-suggestion" + (connect.model === model ? " badge-ui-brand" : "")}
              aria-pressed=${connect.model === model}
              disabled=${testing}
              onClick=${() => update("model", model)}
            >
              <span class="badge-ui-dot" aria-hidden="true"></span>${model}
            </button>`
          )}
        </div>
      </div>

      <div class="setup-test-region" role="status" aria-live="polite" aria-atomic="true">
        ${result || requestError
          ? html`<div class=${"setup-alert " + alertTone}>
              <span class="setup-alert-icon" aria-hidden="true"></span>
              <span class="setup-alert-copy">
                <span class="setup-alert-title">${alertTitle}</span>
                <p class="setup-alert-body">${alertBody}</p>
              </span>
            </div>`
          : null}
      </div>

      <div class="setup-actions">
        <button
          type="button"
          class="button-ui button-ui-secondary"
          disabled=${testing}
          onClick=${runTest}
        >${testing ? "Testing…" : result && !result.ok ? "Test again" : "Test connection"}</button>
        <button
          type="submit"
          class="button-ui button-ui-primary"
          disabled=${!tested || testing}
        >Save and continue</button>
      </div>
      ${!tested
        ? html`<p class="setup-actions-note">Test the connection before you continue.</p>`
        : null}
    </form>
  </${SetupShell}>`;
}

function AgentStep() {
  const announce = useAnnounce();
  const [agent, setAgent] = useState(() => readDraft().agent);
  const [errors, setErrors] = useState({});

  function update(field, value) {
    const next = { ...agent, [field]: value };
    setAgent(next);
    const current = readDraft();
    writeDraft({ ...current, agent: next });
    setErrors((currentErrors) => ({ ...currentErrors, [field]: "" }));
  }

  function validate() {
    const nextErrors = {};
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

  const nameDescribedBy = ["setup-agent-name-hint", errors.name ? "setup-agent-name-error" : ""]
    .filter(Boolean)
    .join(" ");
  const workspaceDescribedBy = [
    "setup-workspace-hint",
    errors.workspace ? "setup-workspace-error" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return html`<${SetupShell} step="agent" size="agent">
    <${StepHeading} step="agent" title="Set up your agent">
      This agent answers every new session. You can add more agents and change
      these choices later in Settings.
    </${StepHeading}>

    <div class="setup-form">
      <div class="setup-field">
        <label class="setup-label" for="setup-agent-name">Agent name</label>
        <input
          id="setup-agent-name"
          type="text"
          autocomplete="off"
          spellcheck=${false}
          value=${agent.name}
          aria-required="true"
          aria-invalid=${errors.name ? "true" : undefined}
          aria-describedby=${nameDescribedBy}
          onInput=${(event) => update("name", event.target.value)}
        />
        <p id="setup-agent-name-hint" class="setup-hint">
          Lowercase letters, numbers, and dashes. Routes and the CLI use this name.
        </p>
        ${errors.name
          ? html`<p id="setup-agent-name-error" class="setup-field-error" role="alert">
              ${errors.name}
            </p>`
          : null}
      </div>

      <fieldset class="setup-group">
        <legend class="setup-label">Template</legend>
        <div class="agent-permission-tiles setup-tiles">
          ${TEMPLATES.map((template) => {
            const selected = agent.template === template.id;
            return html`<label
              key=${template.id}
              class=${"agent-permission-tile" + (selected ? " is-selected" : "")}
            >
              <input
                class="agent-permission-input"
                type="radio"
                name="setup-template"
                value=${template.id}
                checked=${selected}
                onChange=${() => update("template", template.id)}
              />
              <span class="agent-permission-text">
                <span class="agent-permission-title">${template.label}</span>
                <span class="agent-permission-note">${template.note}</span>
              </span>
              <span class="agent-permission-radio" aria-hidden="true">
                <span class="agent-permission-dot"></span>
              </span>
            </label>`;
          })}
        </div>
        <p class="setup-hint">
          A starting point. You can rewrite the whole persona on the next screen.
        </p>
      </fieldset>

      <div class="setup-field">
        <label class="setup-label" for="setup-workspace">Workspace</label>
        <input
          id="setup-workspace"
          type="text"
          autocomplete="off"
          spellcheck=${false}
          value=${agent.workspace}
          aria-required="true"
          aria-invalid=${errors.workspace ? "true" : undefined}
          aria-describedby=${workspaceDescribedBy}
          onInput=${(event) => update("workspace", event.target.value)}
        />
        <p id="setup-workspace-hint" class="setup-hint">
          File and bash tools work inside this folder.
        </p>
        ${errors.workspace
          ? html`<p id="setup-workspace-error" class="setup-field-error" role="alert">
              ${errors.workspace}
            </p>`
          : null}
      </div>

      <fieldset class="setup-group">
        <legend class="setup-label">Tool permission</legend>
        <div class="agent-permission-tiles setup-tiles">
          ${PERMISSIONS.map((permission) => {
            const selected = agent.permission === permission.id;
            return html`<label
              key=${permission.id}
              class=${"agent-permission-tile" + (selected ? " is-selected" : "")}
            >
              <input
                class="agent-permission-input"
                type="radio"
                name="setup-permission"
                value=${permission.id}
                checked=${selected}
                onChange=${() => update("permission", permission.id)}
              />
              <span class="agent-permission-text">
                <span class="agent-permission-title">${permission.label}</span>
                <span class="agent-permission-note">${permission.note}</span>
              </span>
              <span class="agent-permission-radio" aria-hidden="true">
                <span class="agent-permission-dot"></span>
              </span>
            </label>`;
          })}
        </div>
        <p class="setup-hint">
          Ask is recommended. Memory tools never ask for approval.
        </p>
      </fieldset>
    </div>

    <div class="setup-actions">
      <button
        type="button"
        class="button-ui button-ui-secondary"
        onClick=${() => go("connect")}
      >Back</button>
      <button
        type="button"
        class="button-ui button-ui-primary"
        onClick=${continueNext}
      >Continue</button>
    </div>
  </${SetupShell}>`;
}

function RestartPanel({ phase, error, onRetry }) {
  if (phase === "restarting") {
    return html`<div
      class="setup-restart"
      role="status"
      aria-live="polite"
      aria-atomic="true"
    >
      <span class="setup-spinner" aria-hidden="true"></span>
      <h1 class="setup-restart-title">Restarting lana…</h1>
      <p class="setup-restart-text">
        Setup is saved. The daemon restarts in place and this page reconnects on
        its own.
      </p>
    </div>`;
  }
  return html`<div class="setup-restart is-error" role="alert">
    <span class="setup-restart-icon" aria-hidden="true">!</span>
    <h1 class="setup-restart-title">The daemon did not answer</h1>
    <p class="setup-restart-text">${errorMessage(error)}</p>
    <p class="setup-hint">
      If lana is still starting, check again. You can also restart it from the
      terminal.
    </p>
    <button
      type="button"
      class="button-ui button-ui-secondary setup-retry"
      onClick=${onRetry}
    >Check again</button>
  </div>`;
}

function PersonaStep() {
  const announce = useAnnounce();
  const [draft] = useState(readDraft);
  const [persona, setPersona] = useState(() => draft.persona);
  const [errors, setErrors] = useState({});
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState(null);
  const [phase, setPhase] = useState("edit");
  const [pollError, setPollError] = useState(null);
  const [pollKey, setPollKey] = useState(0);
  const [restartStartedAt, setRestartStartedAt] = useState(null);

  const agentCharacters = characterCount(persona.agentMd);
  const userCharacters = characterCount(persona.userMd);

  useEffect(() => {
    if (phase !== "restarting") {
      return undefined;
    }
    let alive = true;
    const controller = new AbortController();
    pollDaemonRestart({
      startedAt: restartStartedAt,
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

  function update(field, value) {
    const next = { ...persona, [field]: value };
    setPersona(next);
    const current = readDraft();
    writeDraft({ ...current, persona: next });
    setErrors((currentErrors) => ({ ...currentErrors, [field]: "" }));
  }

  async function submit(event) {
    event.preventDefault();
    if (saving || phase !== "edit") {
      return;
    }
    const nextErrors = {};
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
    return html`<${SetupShell} step="persona" size="wide">
      <${RestartPanel}
        phase=${phase}
        error=${pollError}
        onRetry=${retryPoll}
      />
    </${SetupShell}>`;
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

  return html`<${SetupShell} step="persona" size="wide">
    <${StepHeading} step="persona" title="Make it yours">
      Edit how the agent behaves and tell it about you. Both are plain Markdown
      files in the agent home, so you can change them any time.
    </${StepHeading}>

    <form class="setup-form setup-persona-form" onSubmit=${submit}>
      <div class="setup-field">
        <div class="setup-label-row">
          <label class="setup-label" for="setup-agent-md">Persona · AGENT.md</label>
          <button
            type="button"
            class="link-button setup-reset"
            disabled=${!TEMPLATE_PREVIEW_AVAILABLE}
            aria-describedby="setup-template-note"
          >Reset to template</button>
        </div>
        <textarea
          id="setup-agent-md"
          class="setup-textarea"
          rows="8"
          value=${persona.agentMd}
          aria-required="true"
          aria-invalid=${errors.agentMd ? "true" : undefined}
          aria-describedby=${agentDescribedBy}
          onInput=${(event) => update("agentMd", event.target.value)}
        ></textarea>
        <div class="setup-hint-row">
          <p id="setup-agent-md-hint" class="setup-hint">
            Write any tone, rules, or role.
          </p>
          <span id="setup-agent-md-count" class="setup-counter">
            ${numberFormat.format(agentCharacters)} characters
          </span>
        </div>
        <p id="setup-template-note" class="setup-note">
          The template preview is not available yet. Write the persona yourself;
          you can edit it later in Settings.
        </p>
        ${errors.agentMd
          ? html`<p id="setup-agent-md-error" class="setup-field-error" role="alert">
              ${errors.agentMd}
            </p>`
          : null}
      </div>

      <div class="setup-field">
        <div class="setup-label-row">
          <label class="setup-label" for="setup-user-md">About you · USER.md</label>
          <span class="setup-label-note">Optional</span>
        </div>
        <textarea
          id="setup-user-md"
          class="setup-textarea"
          rows="8"
          value=${persona.userMd}
          aria-invalid=${errors.userMd ? "true" : undefined}
          aria-describedby=${userDescribedBy}
          onInput=${(event) => update("userMd", event.target.value)}
        ></textarea>
        <div class="setup-hint-row">
          <p id="setup-user-md-hint" class="setup-hint">
            The agent reads this on every turn. It adds facts it learns later.
          </p>
          <span id="setup-user-md-count" class="setup-counter">
            ${numberFormat.format(userCharacters)} / 2,000
          </span>
        </div>
        ${errors.userMd
          ? html`<p id="setup-user-md-error" class="setup-field-error" role="alert">
              ${errors.userMd}
            </p>`
          : null}
      </div>

      <p class="setup-tip">
        Memory (MEMORY.md) fills itself as you chat. You can review and edit it
        in Settings › Agents.
      </p>

      ${saveError
        ? html`<div class="setup-save-error" role="alert">
            <${ErrorNotice}
              message=${errorMessage(saveError)}
              details=${saveError instanceof ApiError ? saveError.details : []}
            />
          </div>`
        : null}

      <div class="setup-actions">
        <button
          type="button"
          class="button-ui button-ui-secondary"
          disabled=${saving}
          onClick=${() => go("agent")}
        >Back</button>
        <button
          type="submit"
          class="button-ui button-ui-primary"
          disabled=${saving}
        >${saving ? "Saving…" : "Save and continue"}</button>
      </div>
    </form>
  </${SetupShell}>`;
}

function ReadyStep() {
  const summary = readSummary() || summaryFor(readDraft());
  const permission =
    summary.permissionLabel ||
    permissionById(summary.permission).summary;
  const rows = [
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

  return html`<${SetupShell} step="ready">
    <${StepHeading} step="ready" title="lana is ready">
      Your agent is set up. Start a session here or from the CLI. Connect
      Telegram and other channels later in Channels.
    </${StepHeading}>

    <div class="setup-summary">
      ${rows.map(
        (row) => html`<div key=${row.label} class="setup-summary-row">
          <span class="setup-summary-label">${row.label}</span>
          <span class="setup-summary-value">${row.value}</span>
        </div>`
      )}
    </div>

    <div class="setup-ready-actions">
      <a class="button-ui button-ui-primary setup-ready-primary" href="#/sessions/new">
        Start your first session
      </a>
      <a class="button-ui button-ui-tertiary setup-ready-secondary" href="#/settings/general">
        Open settings
      </a>
    </div>
  </${SetupShell}>`;
}

export function SetupWizard({ step }) {
  switch (step) {
    case "connect":
      return html`<${ConnectStep} />`;
    case "agent":
      return html`<${AgentStep} />`;
    case "persona":
      return html`<${PersonaStep} />`;
    case "ready":
      return html`<${ReadyStep} />`;
    case "provider":
    default:
      return html`<${ProviderStep} />`;
  }
}
