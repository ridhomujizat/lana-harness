import {
  html,
  useState,
  useEffect,
  useMemo,
  useRef,
} from "/assets/vendor/preact-htm.js";
import { api, errorMessage, ApiError } from "/assets/api.js";
import { useAnnounce, Dialog, ErrorNotice } from "/assets/ui.js";
import {
  Icon,
  AgentAvatar,
  TeamAvatars,
  isTeam,
  splitModel,
  splitTarget,
  normalizePermission,
  providerLabel,
  agentHref,
  sessionSummary,
} from "/assets/views/agent-common.js";
import { TeamSummary } from "/assets/views/agents.js";
import { PersonaTab } from "/assets/views/agent-tabs/persona.js";
import { ToolsTab } from "/assets/views/agent-tabs/tools.js";
import { SkillsTab } from "/assets/views/agent-tabs/skills.js";
import { McpTab } from "/assets/views/agent-tabs/mcp.js";
import { RulesTab } from "/assets/views/agent-tabs/rules.js";
import { WorkspaceTab } from "/assets/views/agent-tabs/workspace.js";

/* REQ-WEB-047: the agent detail header, the seven-tab bar, and the General
   tab. The other tabs show a placeholder until their slices land (T-030). */

const TABS = [
  { id: "general", label: "General" },
  { id: "persona", label: "Persona & memory" },
  { id: "tools", label: "Tools" },
  { id: "skills", label: "Skills" },
  { id: "mcp", label: "MCP" },
  { id: "rules", label: "Rules" },
  { id: "workspace", label: "Workspace" },
];

const GENERAL_KEYS = [
  "provider",
  "model",
  "fallback",
  "permission",
  "max_steps",
  "context_window",
];

const PERMISSIONS = [
  {
    value: "ask",
    title: "Ask",
    note: "Ask me before each risky tool.",
  },
  {
    value: "trust",
    title: "Trust",
    note: "Run every tool without asking.",
  },
  {
    value: "never",
    title: "Never",
    note: "Block risky tools. Read-only work only.",
  },
];

function pickText() {
  for (let index = 0; index < arguments.length; index += 1) {
    const value = arguments[index];
    if (typeof value === "string" && value !== "") {
      return value;
    }
  }
  return "";
}

function numberValue(value) {
  const text = String(value === undefined || value === null ? "" : value).trim();
  if (text === "") {
    return 0;
  }
  const parsed = Number(text);
  return Number.isFinite(parsed) ? parsed : 0;
}

/* POST /v1/sessions accepts "" (use the agent's model) or "<provider>:<model>". */
function sessionModel(agent) {
  const value = agent && agent.model ? String(agent.model) : "";
  if (!value) {
    return "";
  }
  if (value.indexOf(":") > 0) {
    return value;
  }
  if (agent && agent.provider) {
    return agent.provider + ":" + value;
  }
  return "";
}

function initialValues(agent, configAgent) {
  const record = configAgent || {};
  const parts = splitModel(agent);
  return {
    provider: pickText(record.provider, agent.provider, parts.provider),
    model: pickText(record.model, parts.model),
    fallback: Array.isArray(record.fallback)
      ? record.fallback.map((target) => String(target))
      : [],
    permission: normalizePermission(
      pickText(record.permission, agent.permission, agent.policy)
    ),
    max_steps: Number(record.max_steps) || 0,
    context_window: Number(record.context_window) || 0,
  };
}

function formValues(initial) {
  return {
    provider: initial.provider || "",
    model: initial.model || "",
    fallback: Array.isArray(initial.fallback) ? initial.fallback.slice() : [],
    permission: normalizePermission(initial.permission),
    max_steps: initial.max_steps ? String(initial.max_steps) : "",
    context_window: initial.context_window ? String(initial.context_window) : "",
  };
}

function canonical(form) {
  return {
    provider: form.provider || "",
    model: form.model || "",
    fallback: Array.isArray(form.fallback) ? form.fallback.slice() : [],
    permission: normalizePermission(form.permission),
    max_steps: numberValue(form.max_steps),
    context_window: numberValue(form.context_window),
  };
}

function sameValue(left, right) {
  if (Array.isArray(left) && Array.isArray(right)) {
    return JSON.stringify(left) === JSON.stringify(right);
  }
  return left === right;
}

function dirtyKeys(form, baseline) {
  if (!form || !baseline) {
    return [];
  }
  const current = canonical(form);
  return GENERAL_KEYS.filter((key) => !sameValue(current[key], baseline[key]));
}

function validateForm(form, baseline) {
  const stepsText = String(form.max_steps || "").trim();
  if (stepsText === "") {
    if (baseline.max_steps > 0) {
      return "Enter max steps per turn, between 1 and 200.";
    }
  } else {
    const steps = Number(stepsText);
    if (!Number.isInteger(steps) || steps < 1 || steps > 200) {
      return "Max steps per turn must be a whole number from 1 to 200.";
    }
  }

  const contextText = String(form.context_window || "").trim();
  if (contextText === "") {
    if (baseline.context_window > 0) {
      return "Enter a context window greater than 0.";
    }
  } else {
    const context = Number(contextText);
    if (!Number.isInteger(context) || context <= 0) {
      return "Context window must be a whole number greater than 0.";
    }
  }

  for (const target of form.fallback) {
    const parts = splitTarget(target);
    if (!parts.provider || !parts.model) {
      return "Each fallback must be a provider and a model.";
    }
  }
  return "";
}

function nextDuplicateName(source, agents) {
  const names = new Set(agents.map((agent) => agent.name));
  let index = 2;
  while (names.has(source + "-" + index)) {
    index += 1;
  }
  return source + "-" + index;
}

function DuplicateAgentDialog({ source, agents, onClose, onDuplicated }) {
  const announce = useAnnounce();
  const [name, setName] = useState(() => nextDuplicateName(source, agents));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);

  async function submit(event) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const value = name.trim();
    if (!/^[a-z0-9-]+$/.test(value)) {
      setError("The name must use lowercase letters, digits, and dashes.");
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      const created = await api.duplicateAgent(source, value);
      const createdName = created && created.name ? created.name : value;
      announce("Agent " + createdName + " created from " + source + ".");
      if (onDuplicated) {
        onDuplicated(createdName);
      }
      onClose();
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 409) {
        setError("An agent named " + value + " already exists. Pick another name.");
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
      }
      announce("Could not duplicate the agent. " + errorMessage(failure));
    } finally {
      setBusy(false);
    }
  }

  return html`<${Dialog}
    title="Duplicate agent"
    dismissible=${!busy}
    onClose=${onClose}
  >
    <form class="agent-dialog-form" onSubmit=${submit}>
      <p class="hint">
        Copy <strong>${source}</strong> to a new agent. The config table and
        AGENT.md are copied; MEMORY.md is not.
      </p>
      <label class="field">
        <span class="field-label">New name</span>
        <input
          data-autofocus
          type="text"
          value=${name}
          autocomplete="off"
          spellcheck="false"
          pattern="[a-z0-9-]+"
          required
          onInput=${(event) => setName(event.target.value)}
        />
        <span class="hint">Lowercase letters, digits, and dashes.</span>
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
          ${busy ? "Duplicating…" : "Duplicate"}
        </button>
      </div>
    </form>
  </${Dialog}>`;
}

export function AgentDetailPage({ name, tab }) {
  const announce = useAnnounce();
  const [status, setStatus] = useState("loading");
  const [page, setPage] = useState(null);
  const [version, setVersion] = useState("");
  const [form, setForm] = useState(null);
  const [baseline, setBaseline] = useState(null);
  const [reloadToken, setReloadToken] = useState(0);
  const [saving, setSaving] = useState(false);
  const [saveNotice, setSaveNotice] = useState(null);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [stale, setStale] = useState(false);
  const [fallbackDraft, setFallbackDraft] = useState(null);
  const [customModel, setCustomModel] = useState(false);
  const [headerError, setHeaderError] = useState("");
  const [startBusy, setStartBusy] = useState(false);
  const [duplicateOpen, setDuplicateOpen] = useState(false);
  const tabRefs = useRef([]);
  const pendingTabFocus = useRef(false);

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setPage(null);
    setForm(null);
    setBaseline(null);
    setError("");
    setDetails([]);
    setSaveNotice(null);
    setStale(false);
    setFallbackDraft(null);
    setCustomModel(false);
    setHeaderError("");

    Promise.all([
      api.listAgents(),
      api.getConfig().catch(() => null),
      api.listProviders().catch(() => ({ providers: [] })),
    ])
      .then(([agentPage, config, providerPage]) => {
        if (!alive) {
          return;
        }
        const rows = agentPage && Array.isArray(agentPage.agents) ? agentPage.agents : [];
        const agent = rows.find((row) => row.name === name) || null;
        const settings = config && config.settings ? config.settings : null;
        const configAgent =
          settings && settings.agents && settings.agents[name]
            ? settings.agents[name]
            : {};
        const providers = ((providerPage && providerPage.providers) || [])
          .map((provider) => provider && provider.name)
          .filter(Boolean)
          .sort();
        const initial = agent ? initialValues(agent, configAgent) : null;
        setPage({ agent, agents: rows, providers });
        setVersion(config && config.version ? String(config.version) : "");
        if (initial) {
          setForm(formValues(initial));
          setBaseline(canonical(formValues(initial)));
        }
        setStatus("ready");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
        setStatus("error");
        announce("Could not load the agent. " + errorMessage(failure));
      });

    return () => {
      alive = false;
    };
  }, [name, reloadToken, announce]);

  const activeTab = TABS.find((item) => item.id === tab) ? tab : "general";

  useEffect(() => {
    if (!pendingTabFocus.current) {
      return;
    }
    pendingTabFocus.current = false;
    const index = TABS.findIndex((item) => item.id === activeTab);
    const node = tabRefs.current[index];
    if (node) {
      node.focus();
    }
  }, [activeTab]);

  const dirty = useMemo(() => dirtyKeys(form, baseline), [form, baseline]);

  const providerOptions = useMemo(() => {
    const names = new Set(page && page.providers ? page.providers : []);
    if (baseline && baseline.provider) {
      names.add(baseline.provider);
    }
    return Array.from(names).sort();
  }, [page, baseline]);

  const modelOptions = useMemo(() => {
    const names = new Set();
    if (baseline && baseline.model) {
      names.add(baseline.model);
    }
    for (const agent of (page && page.agents) || []) {
      const parts = splitModel(agent);
      if (parts.model) {
        names.add(parts.model);
      }
    }
    if (baseline) {
      for (const target of baseline.fallback) {
        const parts = splitTarget(target);
        if (parts.model) {
          names.add(parts.model);
        }
      }
    }
    return Array.from(names).sort((left, right) => left.localeCompare(right));
  }, [page, baseline]);

  function updateField(key, value) {
    setForm((current) => {
      if (!current) {
        return current;
      }
      const next = Object.assign({}, current);
      next[key] = value;
      return next;
    });
    setSaveNotice(null);
    if (!stale) {
      setError("");
      setDetails([]);
    }
  }

  function reload() {
    setReloadToken((current) => current + 1);
  }

  function resetForm() {
    if (!baseline) {
      return;
    }
    setForm(formValues(baseline));
    setCustomModel(false);
    setFallbackDraft(null);
    setSaveNotice(null);
    setError("");
    setDetails([]);
    setStale(false);
  }

  function activateTab(index, focus) {
    const next = TABS[index];
    if (!next) {
      return;
    }
    if (next.id === activeTab) {
      if (focus) {
        const node = tabRefs.current[index];
        if (node) {
          node.focus();
        }
      }
      return;
    }
    pendingTabFocus.current = Boolean(focus);
    window.location.hash = agentHref(name, next.id);
  }

  function handleTabKeyDown(event, index) {
    const last = TABS.length - 1;
    let next = null;
    if (event.key === "ArrowRight") {
      next = index === last ? 0 : index + 1;
    } else if (event.key === "ArrowLeft") {
      next = index === 0 ? last : index - 1;
    } else if (event.key === "Home") {
      next = 0;
    } else if (event.key === "End") {
      next = last;
    }
    if (next === null) {
      return;
    }
    event.preventDefault();
    activateTab(next, true);
  }

  function startFallback() {
    setFallbackDraft({ provider: "", model: "" });
  }

  function updateFallbackDraft(key, value) {
    setFallbackDraft((current) => {
      if (!current) {
        return current;
      }
      const next = Object.assign({}, current);
      next[key] = value;
      return next;
    });
  }

  function addFallback() {
    if (!fallbackDraft || !form) {
      return;
    }
    const provider = fallbackDraft.provider.trim();
    const model = fallbackDraft.model.trim();
    if (!provider || !model) {
      return;
    }
    updateField("fallback", form.fallback.concat([provider + ":" + model]));
    setFallbackDraft(null);
  }

  function removeFallback(index) {
    if (!form) {
      return;
    }
    updateField(
      "fallback",
      form.fallback.filter((target, position) => position !== index)
    );
  }

  async function save(event) {
    event.preventDefault();
    if (!form || !baseline || saving) {
      return;
    }
    setSaveNotice(null);
    setError("");
    setDetails([]);
    setStale(false);

    const problem = validateForm(form, baseline);
    if (problem) {
      setError(problem);
      return;
    }
    if (!version) {
      setError("The config version is unavailable. Reload the page and try again.");
      return;
    }
    if (dirty.length === 0) {
      announce("No changes to save.");
      return;
    }

    const current = canonical(form);
    const patch = {};
    for (const key of dirty) {
      patch[key] = current[key];
    }

    setSaving(true);
    try {
      const result = await api.updateAgent(name, patch, version);
      if (result && result.version) {
        setVersion(String(result.version));
      }
      setBaseline(current);
      const restart = Boolean(result && result.restart_required);
      setSaveNotice(
        restart
          ? "Changes saved. Restart the daemon for them to take effect."
          : "Changes saved."
      );
      announce("Saved changes to " + name + ".");
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setStale(true);
        setError(
          "This agent changed since you opened it. Reload to get the latest version, then save again."
        );
        announce("The save did not apply because the config changed.");
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
        announce("Could not save changes. " + errorMessage(failure));
      }
    } finally {
      setSaving(false);
    }
  }

  async function startSession() {
    if (startBusy || !page || !page.agent) {
      return;
    }
    setStartBusy(true);
    setHeaderError("");
    try {
      const model = sessionModel(page.agent);
      const created = await api.createSession(name, model);
      if (!created || !created.id) {
        throw new ApiError(
          0,
          "invalid_response",
          "The daemon did not return a session id.",
          []
        );
      }
      announce("Session started with " + name + ".");
      window.location.hash = "#/sessions/" + encodeURIComponent(created.id);
    } catch (failure) {
      setHeaderError(errorMessage(failure));
      announce("Could not start a session. " + errorMessage(failure));
    } finally {
      setStartBusy(false);
    }
  }

  if (status === "loading") {
    return html`<main id="main" class="page agent-detail-page">
      <p class="muted" role="status">Loading agent…</p>
    </main>`;
  }

  if (status === "error") {
    return html`<main id="main" class="page agent-detail-page">
      <nav class="agent-breadcrumb" aria-label="Breadcrumb">
        <a href="#/agents">Agents</a>
        <span aria-hidden="true">/</span>
        <span>${name}</span>
      </nav>
      <div role="alert">
        <${ErrorNotice} message=${error} details=${details} />
      </div>
      <div class="agent-state-actions">
        <button type="button" class="button-ui button-ui-secondary" onClick=${reload}>
          Try again
        </button>
        <a class="button-ui button-ui-secondary" href="#/agents">Back to agents</a>
      </div>
    </main>`;
  }

  const agent = page ? page.agent : null;
  if (!agent) {
    return html`<main id="main" class="page agent-detail-page">
      <nav class="agent-breadcrumb" aria-label="Breadcrumb">
        <a href="#/agents">Agents</a>
        <span aria-hidden="true">/</span>
        <span>${name}</span>
      </nav>
      <h1>Agent not found</h1>
      <p class="muted">
        No configured agent or team is named <code>${name}</code>.
      </p>
      <a class="button-ui button-ui-secondary" href="#/agents">Back to agents</a>
    </main>`;
  }

  if (isTeam(agent)) {
    return html`<main id="main" class="page agent-detail-page">
      <nav class="agent-breadcrumb" aria-label="Breadcrumb">
        <a href="#/agents">Agents</a>
        <span aria-hidden="true">/</span>
        <span aria-current="page">${agent.name}</span>
      </nav>
      <header class="agent-detail-header">
        <div class="agent-detail-identity">
          <${TeamAvatars} agent=${agent} size="36" />
          <div class="agent-detail-heading">
            <div class="agent-detail-name-row">
              <h1 class="agent-detail-name">${agent.name}</h1>
              <span class="badge-ui badge-ui-brand">
                <span class="badge-ui-dot" aria-hidden="true"></span>
                Team
              </span>
            </div>
            <p class="agent-detail-meta">${sessionSummary(agent)}.</p>
          </div>
        </div>
      </header>
      <div class="agent-team-card">
        <h2>Team summary</h2>
        <p class="hint">Teams are read-only in this build.</p>
        <${TeamSummary} team=${agent} />
        <a class="button-ui button-ui-secondary" href="#/settings/config">
          Open the config file
        </a>
      </div>
    </main>`;
  }

  const canSave = dirty.length > 0 && !saving;
  const activeLabel =
    (TABS.find((item) => item.id === activeTab) || TABS[0]).label;

  return html`<main id="main" class="page agent-detail-page">
    <nav class="agent-breadcrumb" aria-label="Breadcrumb">
      <a href="#/agents">Agents</a>
      <span aria-hidden="true">/</span>
      <span aria-current="page">${agent.name}</span>
    </nav>

    <header class="agent-detail-header">
      <div class="agent-detail-identity">
        <${AgentAvatar} name=${agent.name} size="56" />
        <div class="agent-detail-heading">
          <div class="agent-detail-name-row">
            <h1 class="agent-detail-name">${agent.name}</h1>
            ${agent.default
              ? html`<span class="badge-ui badge-ui-brand">
                  <span class="badge-ui-dot" aria-hidden="true"></span>
                  Default
                </span>`
              : null}
          </div>
          <p class="agent-detail-description">
            ${agent.description || "No description yet."}
          </p>
          <p class="agent-detail-meta">${sessionSummary(agent)}.</p>
        </div>
      </div>
      <div class="agent-detail-actions">
        <button
          type="button"
          class="button-ui button-ui-secondary"
          onClick=${() => setDuplicateOpen(true)}
        >Duplicate</button>
        <button
          type="button"
          class="button-ui button-ui-primary"
          disabled=${startBusy}
          onClick=${startSession}
        >${startBusy ? "Starting…" : "Start session"}</button>
      </div>
    </header>

    ${headerError
      ? html`<div class="agent-header-error" role="alert">
          <${ErrorNotice} message=${headerError} />
        </div>`
      : null}

    <div class="agent-tabs" role="tablist" aria-label="Agent sections">
      ${TABS.map((item, index) => {
        const selected = item.id === activeTab;
        return html`<button
          key=${item.id}
          type="button"
          role="tab"
          id=${"agent-tab-" + item.id}
          class="agent-tab"
          aria-selected=${selected ? "true" : "false"}
          aria-controls="agent-tabpanel"
          tabIndex=${selected ? 0 : -1}
          ref=${(node) => {
            tabRefs.current[index] = node;
          }}
          onClick=${() => activateTab(index, false)}
          onKeyDown=${(event) => handleTabKeyDown(event, index)}
        >${item.label}</button>`;
      })}
    </div>

    <section
      class="agent-tab-panel"
      id="agent-tabpanel"
      role="tabpanel"
      aria-labelledby=${"agent-tab-" + activeTab}
      tabIndex=${0}
    >
      ${activeTab === "general" && form
        ? html`<form class="agent-general-form" onSubmit=${save}>
            <section class="agent-form-section" aria-labelledby="agent-model-title">
              <div class="agent-form-copy">
                <h2 id="agent-model-title">Model</h2>
                <p>
                  The model this agent uses. Fallbacks run when the main model
                  fails.
                </p>
              </div>
              <div class="agent-form-controls">
                <div class="agent-form-row">
                  <div class="agent-form-field">
                    <label class="agent-form-field-label" for="agent-provider">
                      Provider
                    </label>
                    <select
                      id="agent-provider"
                      value=${form.provider}
                      disabled=${saving}
                      onChange=${(event) => updateField("provider", event.target.value)}
                    >
                      <option value="">Not set</option>
                      ${providerOptions.map(
                        (provider) => html`<option key=${provider} value=${provider}>
                          ${providerLabel(provider)}
                        </option>`
                      )}
                    </select>
                  </div>
                  <div class="agent-form-field">
                    <label class="agent-form-field-label" for="agent-model-select">
                      Model
                    </label>
                    <select
                      id="agent-model-select"
                      value=${customModel ? "__custom__" : form.model}
                      disabled=${saving}
                      onChange=${(event) => {
                        const value = event.target.value;
                        if (value === "__custom__") {
                          setCustomModel(true);
                          return;
                        }
                        setCustomModel(false);
                        updateField("model", value);
                      }}
                    >
                      <option value="">Provider default</option>
                      ${modelOptions.map(
                        (model) => html`<option key=${model} value=${model}>
                          ${model}
                        </option>`
                      )}
                      <option value="__custom__">Custom model…</option>
                    </select>
                    ${customModel
                      ? html`<input
                          id="agent-model-custom"
                          class="agent-custom-model"
                          type="text"
                          aria-label="Custom model id"
                          placeholder="model id"
                          autocomplete="off"
                          spellcheck="false"
                          value=${form.model}
                          disabled=${saving}
                          onInput=${(event) => updateField("model", event.target.value)}
                        />`
                      : null}
                  </div>
                </div>

                <div class="agent-form-field agent-form-field-wide">
                  <span class="agent-form-field-label" id="agent-fallback-label">
                    Fallback
                  </span>
                  <div
                    class="agent-fallbacks"
                    role="group"
                    aria-labelledby="agent-fallback-label"
                  >
                    ${form.fallback.length === 0
                      ? html`<span class="agent-fallback-empty muted">No fallbacks.</span>`
                      : null}
                    ${form.fallback.map((target, index) => {
                      const parts = splitTarget(target);
                      return html`<span
                        key=${target + ":" + index}
                        class="agent-fallback-tag"
                      >
                        <span>
                          ${(parts.provider ? parts.provider + " / " : "") +
                          parts.model}
                        </span>
                        <button
                          type="button"
                          class="agent-fallback-remove"
                          aria-label=${"Remove fallback " + target}
                          disabled=${saving}
                          onClick=${() => removeFallback(index)}
                        >
                          <${Icon} name="close" className="icon-16" />
                        </button>
                      </span>`;
                    })}
                    <button
                      type="button"
                      class="agent-fallback-add"
                      disabled=${saving}
                      onClick=${startFallback}
                    >+ Add fallback</button>
                  </div>

                  ${fallbackDraft
                    ? html`<div class="agent-fallback-draft">
                        <label class="agent-form-field">
                          <span class="agent-form-field-label">Fallback provider</span>
                          <input
                            type="text"
                            placeholder="provider"
                            autocomplete="off"
                            spellcheck="false"
                            value=${fallbackDraft.provider}
                            disabled=${saving}
                            onInput=${(event) =>
                              updateFallbackDraft("provider", event.target.value)}
                          />
                        </label>
                        <label class="agent-form-field">
                          <span class="agent-form-field-label">Fallback model</span>
                          <input
                            type="text"
                            placeholder="model"
                            autocomplete="off"
                            spellcheck="false"
                            value=${fallbackDraft.model}
                            disabled=${saving}
                            onInput=${(event) =>
                              updateFallbackDraft("model", event.target.value)}
                          />
                        </label>
                        <div class="agent-fallback-draft-actions">
                          <button
                            type="button"
                            class="button-ui button-ui-secondary"
                            disabled=${saving}
                            onClick=${() => setFallbackDraft(null)}
                          >Cancel</button>
                          <button
                            type="button"
                            class="button-ui button-ui-secondary"
                            disabled=${saving ||
                            !fallbackDraft.provider.trim() ||
                            !fallbackDraft.model.trim()}
                            onClick=${addFallback}
                          >Add</button>
                        </div>
                      </div>`
                    : null}
                </div>
              </div>
            </section>

            <section class="agent-form-section" aria-labelledby="agent-permission-title">
              <div class="agent-form-copy">
                <h2 id="agent-permission-title">Permission</h2>
                <p>
                  What happens when the agent wants to run a risky tool, such as
                  bash or write_file.
                </p>
              </div>
              <div class="agent-form-controls">
                <fieldset class="agent-permission-tiles">
                  <legend class="sr-only">Permission</legend>
                  ${PERMISSIONS.map((permission) => {
                    const selected = form.permission === permission.value;
                    return html`<label
                      key=${permission.value}
                      class=${"agent-permission-tile" + (selected ? " is-selected" : "")}
                    >
                      <input
                        class="agent-permission-input"
                        type="radio"
                        name="agent-permission"
                        value=${permission.value}
                        checked=${selected}
                        disabled=${saving}
                        onChange=${() => updateField("permission", permission.value)}
                      />
                      <span class="agent-permission-text">
                        <span class="agent-permission-title">${permission.title}</span>
                        <span class="agent-permission-note">${permission.note}</span>
                      </span>
                      <span class="agent-permission-radio" aria-hidden="true">
                        <span class="agent-permission-dot"></span>
                      </span>
                    </label>`;
                  })}
                </fieldset>
              </div>
            </section>

            <section class="agent-form-section" aria-labelledby="agent-limits-title">
              <div class="agent-form-copy">
                <h2 id="agent-limits-title">Limits</h2>
                <p>How long one turn can run.</p>
              </div>
              <div class="agent-form-controls">
                <div class="agent-form-row">
                  <div class="agent-form-field">
                    <label class="agent-form-field-label" for="agent-max-steps">
                      Max steps per turn
                    </label>
                    <input
                      id="agent-max-steps"
                      class="agent-number-input"
                      type="number"
                      min="1"
                      max="200"
                      step="1"
                      inputMode="numeric"
                      value=${form.max_steps}
                      placeholder="Default"
                      disabled=${saving}
                      onInput=${(event) => updateField("max_steps", event.target.value)}
                    />
                  </div>
                  <div class="agent-form-field">
                    <label class="agent-form-field-label" for="agent-context-window">
                      Context window
                    </label>
                    <input
                      id="agent-context-window"
                      class="agent-number-input"
                      type="number"
                      min="1"
                      step="1"
                      inputMode="numeric"
                      value=${form.context_window}
                      placeholder="Provider default"
                      disabled=${saving}
                      onInput=${(event) =>
                        updateField("context_window", event.target.value)}
                    />
                  </div>
                </div>
              </div>
            </section>

            ${saveNotice
              ? html`<p class="notice notice-ok agent-save-notice" role="status">
                  ${saveNotice}
                </p>`
              : null}
            ${error
              ? html`<div class="agent-save-error" role="alert">
                  <${ErrorNotice} message=${error} details=${details} />
                  ${stale
                    ? html`<button
                        type="button"
                        class="button-ui button-ui-secondary"
                        onClick=${reload}
                      >Reload settings</button>`
                    : null}
                </div>`
              : null}

            <div class="agent-save-bar">
              <p class="agent-save-note">
                Config changes apply after a daemon restart. Agent file changes
                apply at the next turn.${" "}
                <a href="#/settings/general">
                  Restart the daemon in Settings › General
                </a>.
              </p>
              <div class="agent-save-actions">
                <button
                  type="button"
                  class="button-ui button-ui-secondary"
                  disabled=${!dirty.length || saving}
                  onClick=${resetForm}
                >Cancel</button>
                <button
                  type="submit"
                  class="button-ui button-ui-primary"
                  disabled=${!canSave}
                >${saving ? "Saving…" : "Save changes"}</button>
              </div>
            </div>
          </form>`
        : activeTab === "persona"
        ? html`<${PersonaTab} name=${name} />`
        : activeTab === "tools"
        ? html`<${ToolsTab} name=${name} />`
        : activeTab === "skills"
        ? html`<${SkillsTab} name=${name} />`
        : activeTab === "mcp"
        ? html`<${McpTab} name=${name} />`
        : activeTab === "rules"
        ? html`<${RulesTab} name=${name} />`
        : activeTab === "workspace"
        ? html`<${WorkspaceTab} name=${name} />`
        : null}
    </section>

    ${duplicateOpen
      ? html`<${DuplicateAgentDialog}
          source=${agent.name}
          agents=${page.agents}
          onClose=${() => setDuplicateOpen(false)}
          onDuplicated=${(createdName) => {
            window.location.hash = agentHref(createdName);
          }}
        />`
      : null}
  </main>`;
}
