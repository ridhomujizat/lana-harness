import {
  html,
  useState,
  useEffect,
  useMemo,
} from "/assets/vendor/preact-htm.js";
import { api, errorMessage, ApiError } from "/assets/api.js";
import { useAnnounce, Dialog, ErrorNotice } from "/assets/ui.js";
import {
  Icon,
  AgentAvatar,
  TeamAvatars,
  isTeam,
  rosterNames,
  splitModel,
  permissionMeta,
  providerLabel,
  agoLabel,
  agentHref,
} from "/assets/views/agent-common.js";

/* REQ-WEB-045: the agents table, the search box, the create dialog, and the
   read-only team summary. Teams are edited in config.toml for now (Q-013). */

const TEMPLATES = [
  { value: "assistant", label: "Assistant", note: "General help and notes." },
  { value: "coder", label: "Coder", note: "Writes and fixes code." },
  { value: "worker", label: "Worker", note: "Runs focused, repeatable work." },
];

const NAME_RULE = /^[a-z0-9-]+$/;

function PermissionBadge({ permission }) {
  const meta = permissionMeta(permission);
  return html`<span class=${meta.className}>
    <span class="badge-ui-dot" aria-hidden="true"></span>
    ${meta.label}
  </span>`;
}function ModelCell({ agent }) {
  if (isTeam(agent)) {
    const count = rosterNames(agent).length;
    return html`<span class="agents-model">
      <span class="agents-model-name">per agent</span>
      <span class="agents-model-provider">${count === 1 ? "1 agent" : count + " agents"}</span>
    </span>`;
  }
  const parts = splitModel(agent);
  return html`<span class="agents-model">
    <span class="agents-model-name">${parts.model || "Agent default"}</span>
    ${parts.provider
      ? html`<span class="agents-model-provider">${providerLabel(parts.provider)}</span>`
      : null}
  </span>`;
}

function AgentNameCell({ agent, onOpenTeam }) {
  const team = isTeam(agent);
  const names = rosterNames(agent);
  return html`<span class="agents-name-cell">
    ${team
      ? html`<${TeamAvatars} agent=${agent} size="24" />`
      : html`<${AgentAvatar} name=${agent.name} size="36" />`}
    <span class="agents-name-text">
      <span class="agents-name-row">
        ${team
          ? html`<button
              type="button"
              class="agents-name-action"
              onClick=${() => onOpenTeam(agent)}
            >${agent.name}</button>`
          : html`<a class="agents-name-action" href=${agentHref(agent.name)}>${agent.name}</a>`}
        ${agent.default
          ? html`<span class="badge-ui badge-ui-brand">
              <span class="badge-ui-dot" aria-hidden="true"></span>
              Default
            </span>`
          : null}
        ${team
          ? html`<span class="badge-ui badge-ui-brand">
              <span class="badge-ui-dot" aria-hidden="true"></span>
              Team
            </span>`
          : null}
      </span>
      <span class="agents-description">
        ${team
          ? "Team · " + names.join(", ")
          : agent.description || ""}
      </span>
    </span>
  </span>`;
}

export function TeamSummary({ team }) {
  const names = rosterNames(team);
  const lead = team.lead ? String(team.lead) : "";
  const members = names.filter((name) => name !== lead);
  return html`<div class="team-summary">
    ${team.description ? html`<p class="team-summary-text">${team.description}</p>` : null}
    <dl class="team-summary-list">
      <div>
        <dt>Lead</dt>
        <dd>
          ${lead
            ? html`<span class="team-summary-person">
                <${AgentAvatar} name=${lead} size="24" />
                <span>${lead}</span>
              </span>`
            : html`<span class="muted">No lead set</span>`}
        </dd>
      </div>
      <div>
        <dt>Members</dt>
        <dd>
          ${members.length === 0
            ? html`<span class="muted">No members</span>`
            : html`<span class="team-summary-people">
                ${members.map((name) => html`<span key=${name} class="team-summary-person">
                  <${AgentAvatar} name=${name} size="24" />
                  <span>${name}</span>
                </span>`)}
              </span>`}
        </dd>
      </div>
      <div>
        <dt>Sessions</dt>
        <dd>${Number(team.sessions_count) || 0}</dd>
      </div>
      <div>
        <dt>Last active</dt>
        <dd>${agoLabel(team.last_active_at) || "No activity yet"}</dd>
      </div>
    </dl>
    <p class="hint">
      Teams are read-only here. Edit the
      <code>[teams.${team.name}]</code> table in the config file for now.
    </p>
  </div>`;
}

export function CreateAgentDialog({ onClose, onCreated }) {
  const announce = useAnnounce();
  const [name, setName] = useState("");
  const [template, setTemplate] = useState("assistant");
  const [workspace, setWorkspace] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);

  const templateInfo = TEMPLATES.find((item) => item.value === template) || TEMPLATES[0];

  async function submit(event) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const value = name.trim();
    if (!NAME_RULE.test(value)) {
      setError("The name must use lowercase letters, digits, and dashes, for example reviewer-2.");
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      const created = await api.createAgent({
        name: value,
        template,
        workspace: workspace.trim(),
      });
      const createdName = created && created.name ? created.name : value;
      announce("Agent " + createdName + " created. Restart the daemon for it to take effect.");
      if (onCreated) {
        onCreated(createdName);
      }
      onClose();
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 409) {
        setError("An agent named " + value + " already exists. Pick another name.");
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
      }
      announce("Could not create the agent. " + errorMessage(failure));
    } finally {
      setBusy(false);
    }
  }

  return html`<${Dialog}
    title="Create agent"
    dismissible=${!busy}
    onClose=${onClose}
  >
    <form class="agent-dialog-form" onSubmit=${submit}>
      <p class="hint">
        Create an agent from a template. You can change the model and the
        other settings after it appears.
      </p>

      <label class="field">
        <span class="field-label">Name</span>
        <input
          data-autofocus
          type="text"
          name="name"
          value=${name}
          autocomplete="off"
          spellcheck="false"
          pattern="[a-z0-9-]+"
          required
          aria-describedby="create-agent-name-hint"
          onInput=${(event) => setName(event.target.value)}
        />
        <span id="create-agent-name-hint" class="hint">
          Lowercase letters, digits, and dashes.
        </span>
      </label>

      <label class="field">
        <span class="field-label">Template</span>
        <select
          name="template"
          value=${template}
          onChange=${(event) => setTemplate(event.target.value)}
        >
          ${TEMPLATES.map(
            (item) => html`<option key=${item.value} value=${item.value}>
              ${item.label}
            </option>`
          )}
        </select>
        <span class="hint">${templateInfo.note}</span>
      </label>

      <label class="field">
        <span class="field-label">Workspace</span>
        <input
          type="text"
          name="workspace"
          value=${workspace}
          autocomplete="off"
          spellcheck="false"
          aria-describedby="create-agent-workspace-hint"
          onInput=${(event) => setWorkspace(event.target.value)}
        />
        <span id="create-agent-workspace-hint" class="hint">
          Optional. Leave empty to use the default workspace.
        </span>
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
          ${busy ? "Creating…" : "Create agent"}
        </button>
      </div>
    </form>
  </${Dialog}>`;
}

function sortAgents(rows) {
  return rows.slice().sort((left, right) => {
    if (Boolean(left.default) !== Boolean(right.default)) {
      return left.default ? -1 : 1;
    }
    const leftTeam = isTeam(left);
    const rightTeam = isTeam(right);
    if (leftTeam !== rightTeam) {
      return leftTeam ? 1 : -1;
    }
    return String(left.name || "").localeCompare(String(right.name || ""));
  });
}

function matchesQuery(agent, query) {
  if (!query) {
    return true;
  }
  const fields = [
    agent.name,
    agent.description,
    agent.provider,
    agent.model,
    agent.permission,
    agent.policy,
  ].concat(rosterNames(agent));
  const haystack = fields
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase();
  return haystack.includes(query);
}

export function AgentsPage() {
  const announce = useAnnounce();
  const [status, setStatus] = useState("loading");
  const [agents, setAgents] = useState([]);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [query, setQuery] = useState("");
  const [reloadToken, setReloadToken] = useState(0);
  const [showCreate, setShowCreate] = useState(false);

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setError("");
    setDetails([]);
    api
      .listAgents()
      .then((page) => {
        if (!alive) {
          return;
        }
        setAgents(sortAgents(page && Array.isArray(page.agents) ? page.agents : []));
        setStatus("ready");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        const message = errorMessage(failure);
        setError(message);
        setDetails(failure instanceof ApiError ? failure.details : []);
        setStatus("error");
        announce("Could not load agents. " + message);
      });
    return () => {
      alive = false;
    };
  }, [reloadToken, announce]);

  const search = query.trim().toLowerCase();
  const visible = useMemo(
    () => agents.filter((agent) => matchesQuery(agent, search)),
    [agents, search]
  );

  const teamCount = agents.filter(isTeam).length;
  const agentCount = agents.length - teamCount;
  const summary =
    agentCount + " agent" + (agentCount === 1 ? "" : "s") +
    " · " +
    teamCount + " team" + (teamCount === 1 ? "" : "s");

  /* Q-013: a team row opens the config file. The shell has no anchor for one
     table, so this lands on Settings › Config file; the owner edits the
     [teams.<name>] table there. */
  function openTeam() {
    window.location.hash = "#/settings/config";
  }

  function openRow(event, agent) {
    const target = event.target;
    if (
      target &&
      typeof target.closest === "function" &&
      target.closest("a, button, input, select, textarea, label")
    ) {
      return;
    }
    if (isTeam(agent)) {
      openTeam(agent);
      return;
    }
    window.location.hash = agentHref(agent.name);
  }

  function handleCreated() {
    setReloadToken((current) => current + 1);
  }

  return html`<main id="main" class="page agents-page">
    <header class="agents-header">
      <div class="agents-heading">
        <h1 class="agents-title">Agents</h1>
        <p class="agents-subtitle">
          Each agent has its own model, tools, persona and memory.
        </p>
      </div>
      <button
        type="button"
        class="button-ui button-ui-primary"
        onClick=${() => setShowCreate(true)}
      >+ Create agent</button>
    </header>

    <div class="agents-toolbar">
      <div class="sessions-search agents-search">
        <span class="sessions-search-icon">
          <${Icon} name="search" />
        </span>
        <label class="sr-only" for="agents-search-input">Search agents</label>
        <input
          id="agents-search-input"
          class="sessions-search-input"
          type="search"
          placeholder="Search agents"
          autocomplete="off"
          spellcheck="false"
          value=${query}
          onInput=${(event) => setQuery(event.target.value)}
        />
      </div>
      <span class="agents-toolbar-spacer"></span>
      ${status === "loading"
        ? null
        : html`<p class="agents-summary" aria-live="polite">${summary}</p>`}
    </div>

    ${status === "loading"
      ? html`<p class="agents-state muted" role="status">Loading agents…</p>`
      : null}

    ${status === "error"
      ? html`<div class="agents-state" role="alert">
          <${ErrorNotice} message=${error} details=${details} />
          <button
            type="button"
            class="button-ui button-ui-secondary"
            onClick=${() => setReloadToken((current) => current + 1)}
          >Try again</button>
        </div>`
      : null}

    ${status === "ready" && agents.length === 0
      ? html`<div class="agents-empty">
          <h2 class="agents-empty-title">No agents yet</h2>
          <p class="agents-empty-text">
            Create the first agent to give it a model, tools, and a persona.
          </p>
          <button
            type="button"
            class="button-ui button-ui-primary"
            onClick=${() => setShowCreate(true)}
          >+ Create agent</button>
        </div>`
      : null}

    ${status === "ready" && agents.length > 0 && visible.length === 0
      ? html`<p class="agents-state muted" role="status">
          No agents match “${query.trim()}”.
        </p>`
      : null}

    ${status === "ready" && visible.length > 0
      ? html`<div class="agents-table-wrap">
          <table class="agents-table">
            <caption class="sr-only">Agents and teams</caption>
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">Model</th>
                <th scope="col">Permission</th>
                <th scope="col">Tools</th>
                <th scope="col">Sessions</th>
                <th scope="col">Last active</th>
                <th scope="col"><span class="sr-only">Open</span></th>
              </tr>
            </thead>
            <tbody>
              ${visible.map((agent) => {
                const team = isTeam(agent);
                return html`<tr
                  key=${agent.name}
                  class=${"agents-row" + (team ? " is-team" : "")}
                  onClick=${(event) => openRow(event, agent)}
                >
                  <td>
                    <${AgentNameCell} agent=${agent} onOpenTeam=${openTeam} />
                  </td>
                  <td>
                    <${ModelCell} agent=${agent} />
                  </td>
                  <td>
                    <${PermissionBadge} permission=${agent.permission || agent.policy} />
                  </td>
                  <td class="agents-number">${team ? "–" : Number(agent.tools_count) || 0}</td>
                  <td class="agents-number">${Number(agent.sessions_count) || 0}</td>
                  <td class="agents-last-active">
                    ${agoLabel(agent.last_active_at) || "—"}
                  </td>
                  <td class="agents-chevron">
                    <${Icon} name="chevron-right" />
                  </td>
                </tr>`;
              })}
            </tbody>
          </table>
        </div>`
      : null}

    ${showCreate
      ? html`<${CreateAgentDialog}
          onClose=${() => setShowCreate(false)}
          onCreated=${handleCreated}
        />`
      : null}
  </main>`;
}
