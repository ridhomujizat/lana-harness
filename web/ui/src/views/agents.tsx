/* eslint-disable react-hooks/set-state-in-effect -- the list resets its load state before each request, as in agents.js */
import { useEffect, useMemo, useState } from "react";
import type { FormEvent, MouseEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import { createAgent, listAgents } from "../api/agents";
import type { Agent, CreateAgentRequest } from "../api/agents";
import { Dialog, ErrorNotice, useAnnounce } from "../ui/ui";
import {
  Icon,
  isTeam,
  rosterNames,
  agentHref,
} from "./agent-common";
import type { AgentLike } from "./agent-common";
import { AgentFace, Skeleton, TeamFace, useRooms } from "./presence";
import type { RoomRow } from "../api/rooms";

/* REQ-ROOM-011: the alive roster. The Agents page shows a team pulse and one
   status card per agent and team (design rooms-3). The create dialog is
   unchanged (REQ-WEB-045). */

const TEMPLATES = [
  { value: "assistant", label: "Assistant", note: "General help and notes." },
  { value: "coder", label: "Coder", note: "Writes and fixes code." },
  { value: "worker", label: "Worker", note: "Runs focused, repeatable work." },
] as const;

const NAME_RULE = /^[a-z0-9-]+$/;

/* The pulse order and wording of REQ-ROOM-011. */
const PULSE_STATES: readonly { state: string; word: string }[] = [
  { state: "working", word: "working" },
  { state: "waiting", word: "waiting for you" },
  { state: "problem", word: "has a problem" },
  { state: "online", word: "online" },
  { state: "resting", word: "resting" },
];

const STATE_FALLBACK: Record<string, string> = {
  working: "Working",
  waiting: "Waiting for you",
  problem: "Has a problem",
  online: "Online",
  resting: "Resting",
};

interface CardAction {
  label: string;
  primary: boolean;
}

/* The one context button of a card: "Answer" when waiting, "Watch live" when
   working, "Read diagnosis" when there is a problem, "Open room" otherwise. */
function cardAction(state: string, name: string): CardAction {
  if (state === "waiting") {
    return { label: "Answer " + name, primary: true };
  }
  if (state === "working") {
    return { label: "Watch live", primary: false };
  }
  if (state === "problem") {
    return { label: "Read diagnosis", primary: false };
  }
  return { label: "Open room", primary: false };
}

function roomState(row: RoomRow | undefined): string {
  return row && row.presence ? row.presence.state : "";
}

function memberCount(row: RoomRow | undefined): number {
  if (!row) {
    return 0;
  }
  const names = [row.lead, ...(row.members || [])].filter(Boolean);
  return names.length;
}

function teamFaces(row: RoomRow | undefined): { name: string; color?: string }[] {
  if (!row) {
    return [];
  }
  return [row.lead, ...(row.members || [])]
    .filter(Boolean)
    .map((name) => ({ name: String(name) }));
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

/* One roster card (design rooms-3). The body opens the profile; the footer
   holds the one context button. */
function AgentCard({
  agent,
  row,
  onOpen,
}: {
  agent: Agent;
  row: RoomRow | undefined;
  onOpen: (event: MouseEvent<HTMLElement>, name: string) => void;
}) {
  const team = isTeam(agent);
  const state = roomState(row);
  const faceColor = (row && row.color) || agent.color;
  const presence = row ? row.presence : null;
  const statusWord =
    row && row.presence && row.presence.label
      ? row.presence.label
      : STATE_FALLBACK[state] || "Resting";
  const action = cardAction(state, agent.name);
  const preview =
    row && row.last_message && row.last_message.preview
      ? row.last_message.preview
      : "No conversations yet";
  const members = memberCount(row);
  const role = team
    ? "Team" + (row && row.lead ? " · lead " + row.lead : "")
    : (agent.title || "Agent") + (agent.default ? " · default" : "");
  const foot = team
    ? members > 0
      ? members + (members === 1 ? " member" : " members")
      : "Team"
    : Number(row ? row.turns_7d : 0) + " tasks · 7d";
  const faces = team ? teamFaces(row) : [];

  const classes = [
    "agent-card",
    state ? "state-" + state : "",
    agent.default ? "is-default" : "",
    team ? "is-team" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <article
      className={classes}
      onClick={(event) => onOpen(event, agent.name)}
    >
      <div className="agent-card-head">
        {team ? (
          faces.length > 0 ? (
            <TeamFace faces={faces} size={52} />
          ) : (
            <AgentFace name={agent.name} color="brand" presence={presence} size={52} />
          )
        ) : (
          <AgentFace
            name={agent.name}
            color={faceColor}
            presence={presence}
            size={52}
          />
        )}
        <div className="agent-card-id">
          <a className="agent-card-name" href={agentHref(agent.name)}>
            {agent.name}
          </a>
          <span className="agent-card-role">{role}</span>
        </div>
      </div>

      <div className={"agent-card-status" + (state ? " state-" + state : "")}>
        <span className="agent-card-status-line">
          <span className="agent-card-status-dot" aria-hidden="true" />
          <span className="agent-card-status-text">{statusWord}</span>
        </span>
        {state === "working" ? (
          <span className="agent-card-progress" aria-hidden="true">
            <span className="agent-card-progress-bar" />
          </span>
        ) : null}
      </div>

      <p className="agent-card-preview">{preview}</p>

      <footer className="agent-card-foot">
        <a
          className={
            "button-ui " +
            (action.primary ? "button-ui-primary" : "button-ui-secondary")
          }
          href={"#/chats/" + encodeURIComponent(agent.name)}
        >
          {action.label}
        </a>
        <span className="agent-card-turns">{foot}</span>
      </footer>
    </article>
  );
}

export function AgentsPage() {
  const announce = useAnnounce();
  const { rows, loading } = useRooms();
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

  const byName = useMemo(() => {
    const map: Record<string, RoomRow> = {};
    for (const row of rows) {
      map[row.name] = row;
    }
    return map;
  }, [rows]);

  const pulse = useMemo(() => {
    const counts: Record<string, number> = {};
    let turns = 0;
    for (const row of rows) {
      const state = roomState(row);
      if (state) {
        counts[state] = (counts[state] || 0) + 1;
      }
      turns += Number(row.turns_7d) || 0;
    }
    return { counts, turns };
  }, [rows]);

  function openCard(event: MouseEvent<HTMLElement>, name: string) {
    const target = event.target;
    if (
      target instanceof Element &&
      target.closest("a, button, input, select, textarea, label")
    ) {
      return;
    }
    window.location.hash = agentHref(name);
  }

  function handleCreated() {
    setReloadToken((current) => current + 1);
  }

  const showSkeleton = status === "loading" || (loading && agents.length === 0);

  return (
    <main id="main" className="page agents-page">
      <header className="agents-header">
        <div className="agents-heading">
          <h1 className="agents-title">Agents</h1>
          <p className="agents-subtitle">
            Your team. Each agent has a job, its own room and its own memory.
          </p>
        </div>
        <button
          type="button"
          className="button-ui button-ui-primary"
          onClick={() => setShowCreate(true)}
        >
          + Hire an agent
        </button>
      </header>

      <div className="agents-pulse">
        <div className="agents-pulse-counts">
          {PULSE_STATES.map((entry) => {
            const count = pulse.counts[entry.state] || 0;
            if (count === 0) {
              return null;
            }
            return (
              <span key={entry.state} className="agents-pulse-item">
                <span
                  className={"agents-pulse-dot state-" + entry.state}
                  aria-hidden="true"
                />
                <span className="agents-pulse-text">
                  {count + " " + entry.word}
                </span>
              </span>
            );
          })}
        </div>
        <p className="agents-pulse-total">
          {"Team did " + pulse.turns + " tasks this week"}
        </p>
      </div>

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
      </div>

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

      {showSkeleton ? (
        <div className="agents-grid">
          {Array.from({ length: 3 }).map((_, index) => (
            <div key={index} className="agent-card is-skeleton">
              <Skeleton height={52} width={52} radius="50%" />
              <Skeleton height={16} width="60%" radius={6} />
              <Skeleton height={34} width="100%" radius={8} />
              <Skeleton height={14} width="80%" radius={6} />
              <Skeleton height={32} width="100%" radius={8} />
            </div>
          ))}
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
            + Hire an agent
          </button>
        </div>
      ) : null}

      {status === "ready" && agents.length > 0 && visible.length === 0 ? (
        <p className="agents-state muted" role="status">
          No agents match “{query.trim()}”.
        </p>
      ) : null}

      {status === "ready" && agents.length > 0 && visible.length > 0 ? (
        <div className="agents-grid">
          {visible.map((agent) => (
            <AgentCard
              key={agent.name}
              agent={agent}
              row={byName[agent.name]}
              onOpen={openCard}
            />
          ))}
          <button
            type="button"
            className="agent-card-add"
            onClick={() => setShowCreate(true)}
          >
            <span className="agent-card-add-icon" aria-hidden="true">
              <Icon name="plus" />
            </span>
            <span className="agent-card-add-title">Hire a new agent</span>
            <span className="agent-card-add-text">
              Describe the job. Lana drafts its name, persona, tools and first
              workflow for you to review.
            </span>
          </button>
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
