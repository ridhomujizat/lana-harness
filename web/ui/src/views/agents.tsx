/* eslint-disable react-hooks/set-state-in-effect -- the list resets its load state before each request, as in agents.js */
/* eslint-disable react-hooks/immutability -- routing is a window.location hash change, as in agents.js */
import { useEffect, useMemo, useState } from "react";
import type { FormEvent, MouseEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import { createAgent, listAgents } from "../api/agents";
import type { Agent, CreateAgentRequest } from "../api/agents";
import { Dialog, ErrorNotice, useAnnounce } from "../ui/ui";
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
} from "./agent-common";
import type { AgentLike } from "./agent-common";

/* REQ-WEB-045: the agents table, the search box, the create dialog, and the
   read-only team summary. Teams are edited in config.toml for now (Q-013). */

const TEMPLATES = [
  { value: "assistant", label: "Assistant", note: "General help and notes." },
  { value: "coder", label: "Coder", note: "Writes and fixes code." },
  { value: "worker", label: "Worker", note: "Runs focused, repeatable work." },
] as const;

const NAME_RULE = /^[a-z0-9-]+$/;

function PermissionBadge({ permission }: { permission: unknown }) {
  const meta = permissionMeta(permission);
  return (
    <span className={meta.className}>
      <span className="badge-ui-dot" aria-hidden="true"></span>
      {meta.label}
    </span>
  );
}

function ModelCell({ agent }: { agent: Agent }) {
  if (isTeam(agent)) {
    const count = rosterNames(agent).length;
    return (
      <span className="agents-model">
        <span className="agents-model-name">per agent</span>
        <span className="agents-model-provider">
          {count === 1 ? "1 agent" : count + " agents"}
        </span>
      </span>
    );
  }
  const parts = splitModel(agent);
  return (
    <span className="agents-model">
      <span className="agents-model-name">{parts.model || "Agent default"}</span>
      {parts.provider ? (
        <span className="agents-model-provider">
          {providerLabel(parts.provider)}
        </span>
      ) : null}
    </span>
  );
}

function AgentNameCell({
  agent,
  onOpenTeam,
}: {
  agent: Agent;
  onOpenTeam: (agent: Agent) => void;
}) {
  const team = isTeam(agent);
  const names = rosterNames(agent);
  return (
    <span className="agents-name-cell">
      {team ? (
        <TeamAvatars agent={agent} size="24" />
      ) : (
        <AgentAvatar name={agent.name} size="36" />
      )}
      <span className="agents-name-text">
        <span className="agents-name-row">
          {team ? (
            <button
              type="button"
              className="agents-name-action"
              onClick={() => onOpenTeam(agent)}
            >
              {agent.name}
            </button>
          ) : (
            <a className="agents-name-action" href={agentHref(agent.name)}>
              {agent.name}
            </a>
          )}
          {agent.default ? (
            <span className="badge-ui badge-ui-brand">
              <span className="badge-ui-dot" aria-hidden="true"></span>
              Default
            </span>
          ) : null}
          {team ? (
            <span className="badge-ui badge-ui-brand">
              <span className="badge-ui-dot" aria-hidden="true"></span>
              Team
            </span>
          ) : null}
        </span>
        <span className="agents-description">
          {team ? "Team · " + names.join(", ") : agent.description || ""}
        </span>
      </span>
    </span>
  );
}

export interface TeamSummaryProps {
  team: {
    name: string;
    lead: string;
    members: string[];
    description?: string;
    sessions_count?: number;
    last_active_at?: number | null;
  } | null;
}

export function TeamSummary({ team }: TeamSummaryProps) {
  if (!team) {
    return null;
  }
  const names = rosterNames(team);
  const lead = team.lead ? String(team.lead) : "";
  const members = names.filter((name) => name !== lead);
  return (
    <div className="team-summary">
      {team.description ? (
        <p className="team-summary-text">{team.description}</p>
      ) : null}
      <dl className="team-summary-list">
        <div>
          <dt>Lead</dt>
          <dd>
            {lead ? (
              <span className="team-summary-person">
                <AgentAvatar name={lead} size="24" />
                <span>{lead}</span>
              </span>
            ) : (
              <span className="muted">No lead set</span>
            )}
          </dd>
        </div>
        <div>
          <dt>Members</dt>
          <dd>
            {members.length === 0 ? (
              <span className="muted">No members</span>
            ) : (
              <span className="team-summary-people">
                {members.map((name) => (
                  <span key={name} className="team-summary-person">
                    <AgentAvatar name={name} size="24" />
                    <span>{name}</span>
                  </span>
                ))}
              </span>
            )}
          </dd>
        </div>
        <div>
          <dt>Sessions</dt>
          <dd>{Number(team.sessions_count) || 0}</dd>
        </div>
        <div>
          <dt>Last active</dt>
          <dd>{agoLabel(team.last_active_at) || "No activity yet"}</dd>
        </div>
      </dl>
      <p className="hint">
        Teams are read-only here. Edit the{" "}
        <code>[teams.{team.name}]</code> table in the config file for now.
      </p>
    </div>
  );
}

export interface CreateAgentDialogProps {
  onClose: () => void;
  onCreated?: (name: string) => void;
}

export function CreateAgentDialog({
  onClose,
  onCreated,
}: CreateAgentDialogProps) {
  const announce = useAnnounce();
  const [name, setName] = useState("");
  const [template, setTemplate] = useState<string>(TEMPLATES[0].value);
  const [workspace, setWorkspace] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  const templateInfo =
    TEMPLATES.find((item) => item.value === template) || TEMPLATES[0];

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const value = name.trim();
    if (!NAME_RULE.test(value)) {
      setError(
        "The name must use lowercase letters, digits, and dashes, for example reviewer-2."
      );
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      const body: CreateAgentRequest = {
        name: value,
        template,
        workspace: workspace.trim(),
      };
      const created = await createAgent(body);
      const createdName = created && created.name ? created.name : value;
      announce(
        "Agent " +
          createdName +
          " created. Restart the daemon for it to take effect."
      );
      if (onCreated) {
        onCreated(createdName);
      }
      onClose();
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 409) {
        setError(
          "An agent named " + value + " already exists. Pick another name."
        );
      } else {
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
      }
      announce("Could not create the agent. " + errorMessage(failure));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog title="Create agent" dismissible={!busy} onClose={onClose}>
      <form className="agent-dialog-form" onSubmit={submit}>
        <p className="hint">
          Create an agent from a template. You can change the model and the
          other settings after it appears.
        </p>

        <label className="field">
          <span className="field-label">Name</span>
          <input
            data-autofocus
            type="text"
            name="name"
            value={name}
            autoComplete="off"
            spellCheck={false}
            pattern="[a-z0-9-]+"
            required
            aria-describedby="create-agent-name-hint"
            onChange={(event) => setName(event.target.value)}
          />
          <span id="create-agent-name-hint" className="hint">
            Lowercase letters, digits, and dashes.
          </span>
        </label>

        <label className="field">
          <span className="field-label">Template</span>
          <select
            name="template"
            value={template}
            onChange={(event) => setTemplate(event.target.value)}
          >
            {TEMPLATES.map((item) => (
              <option key={item.value} value={item.value}>
                {item.label}
              </option>
            ))}
          </select>
          <span className="hint">{templateInfo.note}</span>
        </label>

        <label className="field">
          <span className="field-label">Workspace</span>
          <input
            type="text"
            name="workspace"
            value={workspace}
            autoComplete="off"
            spellCheck={false}
            aria-describedby="create-agent-workspace-hint"
            onChange={(event) => setWorkspace(event.target.value)}
          />
          <span id="create-agent-workspace-hint" className="hint">
            Optional. Leave empty to use the default workspace.
          </span>
        </label>

        {error ? <ErrorNotice message={error} details={details} /> : null}

        <div className="dialog-actions">
          <button
            type="button"
            className="button-ui button-ui-secondary"
            disabled={busy}
            onClick={onClose}
          >
            Cancel
          </button>
          <button
            type="submit"
            className="button-ui button-ui-primary"
            disabled={busy}
          >
            {busy ? "Creating…" : "Create agent"}
          </button>
        </div>
      </form>
    </Dialog>
  );
}

function sortAgents(rows: Agent[]): Agent[] {
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

function matchesQuery(agent: AgentLike, query: string): boolean {
  if (!query) {
    return true;
  }
  const fields: unknown[] = [
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
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [agents, setAgents] = useState<Agent[]>([]);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [query, setQuery] = useState("");
  const [reloadToken, setReloadToken] = useState(0);
  const [showCreate, setShowCreate] = useState(false);

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setError("");
    setDetails([]);
    listAgents()
      .then((page) => {
        if (!alive) {
          return;
        }
        setAgents(
          sortAgents(page && Array.isArray(page.agents) ? page.agents : [])
        );
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
    agentCount +
    " agent" +
    (agentCount === 1 ? "" : "s") +
    " · " +
    teamCount +
    " team" +
    (teamCount === 1 ? "" : "s");

  /* Q-013: a team row opens the config file. The shell has no anchor for one
     table, so this lands on Settings › Config file; the owner edits the
     [teams.<name>] table there. */
  function openTeam() {
    window.location.hash = "#/settings/config";
  }

  function openRow(event: MouseEvent<HTMLTableRowElement>, agent: Agent) {
    const target = event.target;
    if (
      target instanceof Element &&
      target.closest("a, button, input, select, textarea, label")
    ) {
      return;
    }
    if (isTeam(agent)) {
      openTeam();
      return;
    }
    window.location.hash = agentHref(agent.name);
  }

  function handleCreated() {
    setReloadToken((current) => current + 1);
  }

  return (
    <main id="main" className="page agents-page">
      <header className="agents-header">
        <div className="agents-heading">
          <h1 className="agents-title">Agents</h1>
          <p className="agents-subtitle">
            Each agent has its own model, tools, persona and memory.
          </p>
        </div>
        <button
          type="button"
          className="button-ui button-ui-primary"
          onClick={() => setShowCreate(true)}
        >
          + Create agent
        </button>
      </header>

      <div className="agents-toolbar">
        <div className="sessions-search agents-search">
          <span className="sessions-search-icon">
            <Icon name="search" />
          </span>
          <label className="sr-only" htmlFor="agents-search-input">
            Search agents
          </label>
          <input
            id="agents-search-input"
            className="sessions-search-input"
            type="search"
            placeholder="Search agents"
            autoComplete="off"
            spellCheck={false}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
        <span className="agents-toolbar-spacer"></span>
        {status === "loading" ? null : (
          <p className="agents-summary" aria-live="polite">
            {summary}
          </p>
        )}
      </div>

      {status === "loading" ? (
        <p className="agents-state muted" role="status">
          Loading agents…
        </p>
      ) : null}

      {status === "error" ? (
        <div className="agents-state" role="alert">
          <ErrorNotice message={error} details={details} />
          <button
            type="button"
            className="button-ui button-ui-secondary"
            onClick={() => setReloadToken((current) => current + 1)}
          >
            Try again
          </button>
        </div>
      ) : null}

      {status === "ready" && agents.length === 0 ? (
        <div className="agents-empty">
          <h2 className="agents-empty-title">No agents yet</h2>
          <p className="agents-empty-text">
            Create the first agent to give it a model, tools, and a persona.
          </p>
          <button
            type="button"
            className="button-ui button-ui-primary"
            onClick={() => setShowCreate(true)}
          >
            + Create agent
          </button>
        </div>
      ) : null}

      {status === "ready" && agents.length > 0 && visible.length === 0 ? (
        <p className="agents-state muted" role="status">
          No agents match “{query.trim()}”.
        </p>
      ) : null}

      {status === "ready" && visible.length > 0 ? (
        <div className="agents-table-wrap">
          <table className="agents-table">
            <caption className="sr-only">Agents and teams</caption>
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">Model</th>
                <th scope="col">Permission</th>
                <th scope="col">Tools</th>
                <th scope="col">Sessions</th>
                <th scope="col">Last active</th>
                <th scope="col">
                  <span className="sr-only">Open</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {visible.map((agent) => {
                const team = isTeam(agent);
                return (
                  <tr
                    key={agent.name}
                    className={"agents-row" + (team ? " is-team" : "")}
                    onClick={(event) => openRow(event, agent)}
                  >
                    <td>
                      <AgentNameCell agent={agent} onOpenTeam={openTeam} />
                    </td>
                    <td>
                      <ModelCell agent={agent} />
                    </td>
                    <td>
                      <PermissionBadge
                        permission={agent.permission || agent.policy}
                      />
                    </td>
                    <td className="agents-number">
                      {team ? "–" : Number(agent.tools_count) || 0}
                    </td>
                    <td className="agents-number">
                      {Number(agent.sessions_count) || 0}
                    </td>
                    <td className="agents-last-active">
                      {agoLabel(agent.last_active_at) || "—"}
                    </td>
                    <td className="agents-chevron">
                      <Icon name="chevron-right" />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : null}

      {showCreate ? (
        <CreateAgentDialog
          onClose={() => setShowCreate(false)}
          onCreated={handleCreated}
        />
      ) : null}
    </main>
  );
}
