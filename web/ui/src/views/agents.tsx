/* eslint-disable react-hooks/set-state-in-effect -- the list resets its load state before each request, as in agents.js */
import { useEffect, useMemo, useState } from "react";
import type { FormEvent, MouseEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import { createAgent, listAgents } from "../api/agents";
import type { Agent, CreateAgentRequest } from "../api/agents";
import { getRouter } from "../api/router";
import type { RouterOverview } from "../api/router";
import { Dialog, ErrorNotice, useAnnounce } from "../ui/ui";
import { Ic } from "../ui/icons";
import {
  isTeam,
  rosterNames,
  agentHref,
  usesClaudeCode,
  splitModel,
  providerLabel,
} from "./agent-common";
import { CLAUDE_EFFORTS, ClaudeCodeNote, choiceOptions } from "./agent-runtime";
import {
  ClaudeCodeBadge,
  claudeAccountNames,
  claudeTargetAccount,
  useClaudeAccounts,
} from "./router/claude-code";
import type { AgentLike } from "./agent-common";
import { useRooms } from "./presence";
import {
  AGENT_ID_HINT,
  AGENT_ID_RULE,
  MAX_DISPLAY_NAME,
  displayLabel,
  slugifyAgentName,
} from "./agent-label";
import type { RoomRow } from "../api/rooms";
import {
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Lbl,
  SearchInput,
  Select,
  Skeleton,
} from "../ui/gr8r";
import { TeamAvatar } from "./discussion";
import { AgentAvatar, agentFallbackColor } from "./agent-avatar";
import type { BadgeTone } from "../ui/gr8r";

/* REQ-ROOM-011: the alive roster. The Agents page shows a team pulse and one
   status card per agent and team (design rooms-3). The create dialog is
   unchanged (REQ-WEB-045). */

const TEMPLATES = [
  { value: "assistant", label: "Assistant", note: "General help and notes." },
  { value: "coder", label: "Coder", note: "Writes and fixes code." },
  { value: "worker", label: "Worker", note: "Runs focused, repeatable work." },
] as const;


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
  resting: "Idle",
};

function agentColor(name: string): string {
  return agentFallbackColor(name);
}

/* green active / gray idle / amber waiting / red error (page mapping). */
function stateTone(state: string): BadgeTone {
  if (state === "waiting") {
    return "amber";
  }
  if (state === "problem") {
    return "red";
  }
  if (state === "working" || state === "online") {
    return "green";
  }
  return "gray";
}

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

export interface CreateAgentDialogProps {
  onClose: () => void;
  onCreated?: (name: string) => void;
  /* The model picked when the dialog opens, for example a Claude Code model
     from the Router's Claude Code page. */
  initialModel?: string;
}

/* REQ-WEB-045 and spec 39 (REQ-CCT-009): every agent is a lana agent made
   from a template. The model is optional; a Claude Code model shows its note
   and the Claude reasoning effort. */
export function CreateAgentDialog({
  onClose,
  onCreated,
  initialModel,
}: CreateAgentDialogProps) {
  const announce = useAnnounce();
  const [name, setName] = useState("");
  const [agentId, setAgentId] = useState("");
  const [idTouched, setIdTouched] = useState(false);
  const [template, setTemplate] = useState<string>(TEMPLATES[0].value);
  const [workspace, setWorkspace] = useState("");
  const [model, setModel] = useState(initialModel || "");
  const [effort, setEffort] = useState("");
  const [router, setRouter] = useState<RouterOverview | null>(null);
  const { accounts } = useClaudeAccounts();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  useEffect(() => {
    let alive = true;
    Promise.resolve()
      .then(() => getRouter())
      .then((value) => {
        if (alive && value) {
          setRouter(value);
        }
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);

  const templateInfo =
    TEMPLATES.find((item) => item.value === template) || TEMPLATES[0];
  const choices = useMemo(() => (router ? router.choices : []), [router]);
  const names = useMemo(
    () => claudeAccountNames(choices, router ? router.providers : null, accounts),
    [choices, router, accounts]
  );
  const claudeAccount = claudeTargetAccount(model, choices, router ? router.combos : null, names);
  const modelOptions = useMemo(() => {
    const options = choiceOptions(choices, router ? router.providers : undefined, accounts);
    const known = !model || options.some((option) => option.value === model);
    return [
      { value: "", label: "Same as the default agent" },
      ...(known ? [] : [{ value: model, label: model }]),
      ...options,
    ];
  }, [choices, router, accounts, model]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const displayName = name.trim();
    const value = (idTouched ? agentId : slugifyAgentName(displayName)).trim();
    if (!displayName) {
      setError("Enter a name for the agent, for example Research Assistant.");
      return;
    }
    if (Array.from(displayName).length > MAX_DISPLAY_NAME) {
      setError(
        "The name must be at most " + MAX_DISPLAY_NAME + " characters."
      );
      return;
    }
    if (!AGENT_ID_RULE.test(value)) {
      setError("The ID is not valid. " + AGENT_ID_HINT);
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      /* API-CCT-002: the model is an address; never a provider or runtime. */
      const body: CreateAgentRequest = { name: value, template, workspace: workspace.trim() };
      if (displayName !== value) {
        body.display_name = displayName;
      }
      if (model) {
        body.model = model;
      }
      if (claudeAccount && effort) {
        body.reasoning_effort = effort;
      }
      const created = await createAgent(body);
      const createdName = created && created.name ? created.name : value;
      announce(
        "Agent " +
          displayName +
          " created." +
          (created && created.restart_required
            ? " Restart the daemon for it to take effect."
            : "")
      );
      if (onCreated) {
        onCreated(createdName);
      }
      onClose();
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 409) {
        setError(
          "An agent with the ID " + value + " already exists. Change the ID."
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
          Create an agent from a template. You can change the other settings after it appears.
        </p>

        <label className="field">
          <span className="field-label">Name</span>
          <input
            data-autofocus
            type="text"
            name="name"
            value={name}
            autoComplete="off"
            placeholder="Research Assistant"
            maxLength={MAX_DISPLAY_NAME}
            required
            aria-describedby="create-agent-name-hint"
            onChange={(event) => {
              setName(event.target.value);
              if (!idTouched) {
                setAgentId(slugifyAgentName(event.target.value));
              }
            }}
          />
          <span id="create-agent-name-hint" className="hint">
            The name people see, for example Research Assistant.
          </span>
        </label>

        <label className="field">
          <span className="field-label">ID</span>
          <input
            type="text"
            name="id"
            value={agentId}
            autoComplete="off"
            spellCheck={false}
            placeholder="research-assistant"
            maxLength={32}
            required
            aria-describedby="create-agent-id-hint"
            onChange={(event) => {
              setIdTouched(true);
              setAgentId(event.target.value);
            }}
          />
          <span id="create-agent-id-hint" className="hint">
            Used in links, the config file, and @mentions. Filled in from the
            name. You cannot change it later.
          </span>
        </label>

        <div className="field">
          <label className="field-label" htmlFor="create-agent-template">
            Template
          </label>
          <Select
            id="create-agent-template"
            name="template"
            aria-label="Template"
            value={template}
            onChange={setTemplate}
            options={TEMPLATES.map((item) => ({
              value: item.value,
              label: item.label,
            }))}
          />
          <span className="hint">{templateInfo.note}</span>
        </div>

        <div className="field">
          <label className="field-label" htmlFor="create-agent-model">
            Model
          </label>
          <Select
            id="create-agent-model"
            aria-label="Model"
            value={model}
            options={modelOptions}
            disabled={busy}
            onChange={(value) => {
              setModel(value);
              setEffort("");
            }}
          />
          {claudeAccount ? <ClaudeCodeNote account={claudeAccount} /> : null}
        </div>

        {claudeAccount ? (
          <div className="field">
            <label className="field-label" htmlFor="create-agent-effort">
              Reasoning effort
            </label>
            <Select
              id="create-agent-effort"
              aria-label="Reasoning effort"
              value={effort}
              options={CLAUDE_EFFORTS}
              disabled={busy}
              onChange={setEffort}
            />
            <span className="hint">Passed to Claude Code as --effort.</span>
          </div>
        ) : null}

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
    agent.display_name,
    agent.description,
    agent.provider,
    agent.model,
    agent.permission,
    agent.policy,
    usesClaudeCode(agent) ? "Claude Code" : "",
  ].concat(rosterNames(agent));
  const haystack = fields
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase();
  return haystack.includes(query);
}

/* The model of an address like "commandcode/deepseek/deepseek-v4-flash"
   without its provider part, which the label already names. */
function withoutProvider(model: string, provider: string): string {
  return provider && model.startsWith(provider + "/") ? model.slice(provider.length + 1) : model;
}

/* One roster card (design rooms-3). The body opens the profile; the footer
   holds the one context button. */
function AgentCard({
  agent,
  row,
  rows,
  onOpen,
}: {
  agent: Agent;
  row: RoomRow | undefined;
  /* Every room row, for the lead's display name on a team card. */
  rows?: Record<string, RoomRow>;
  onOpen: (event: MouseEvent<HTMLElement>, name: string) => void;
}) {
  const team = isTeam(agent);
  const state = roomState(row);
  const presence = row ? row.presence : null;
  /* A problem's detail (often a long error) goes under the name in place of
     the preview; the badge keeps a short word so the card does not overflow. */
  const problemText = state === "problem" && presence && presence.label ? presence.label : "";
  const statusWord =
    presence && presence.label && !problemText
      ? presence.label
      : STATE_FALLBACK[state] || "Idle";
  const action = cardAction(state, agent.name);
  const preview =
    row && row.last_message && row.last_message.preview
      ? row.last_message.preview
      : "No conversations yet";
  const description =
    agent.description && agent.description.trim()
      ? agent.description
      : preview;
  const members = memberCount(row);
  const role = team
    ? "Team" + (row && row.lead ? " · lead " + displayLabel(rows && rows[row.lead] ? rows[row.lead] : { name: row.lead }) : "")
    : (agent.title || "Agent") + (agent.default ? " · default" : "");
  const foot = team
    ? members > 0
      ? members + (members === 1 ? " member" : " members")
      : "Team"
    : (() => {
        const turns = Number(row ? row.turns_7d : 0);
        return turns + (turns === 1 ? " task" : " tasks") + " · 7d";
      })();
  const model = splitModel(agent);
  /* Spec 39 (API-CCT-006): an agent on Claude Code shows the Claude mark;
     its model is the address, for example claude-code/opus. */
  const claude = usesClaudeCode(agent);
  const modelLabel = claude
    ? String(agent.model || "")
    : model.model
      ? model.provider
        ? providerLabel(model.provider) + " · " + withoutProvider(model.model, model.provider)
        : model.model
      : "";

  return (
    <article className="pcard" onClick={(event) => onOpen(event, agent.name)}>
      <div className="agent-card-head">
        {team ? (
          <TeamAvatar
            size={40}
            items={rosterNames(agent).map((name) => ({
              name,
              color: agentColor(name),
            }))}
          />
        ) : (
          <AgentAvatar
            name={agent.name}
            color={agentColor(agent.name)}
            size="md"
            presence={state === "online" || state === "working"}
          />
        )}
        <div className="agent-card-id">
          <a className="agent-card-name" href={agentHref(agent.name)}>
            {displayLabel(agent.display_name ? agent : row && row.display_name ? row : agent)}
          </a>
          <span className="agent-card-role">{role}</span>
        </div>
        <Badge tone={stateTone(state)} dot className="agent-card-state" title={statusWord}>
          <span className="agent-card-clip">{statusWord}</span>
        </Badge>
      </div>

      {problemText ? (
        <p className="agent-card-preview agent-card-problem" title={problemText}>
          {problemText}
        </p>
      ) : (
        <p className="agent-card-preview">{description}</p>
      )}

      {modelLabel || claude ? (
        <div className="agent-card-meta">
          {claude ? <ClaudeCodeBadge /> : null}
          {modelLabel ? (
            <Lbl color="var(--text-4)" className="agent-card-model" title={modelLabel}>
              <span className="agent-card-clip">{modelLabel}</span>
            </Lbl>
          ) : null}
        </div>
      ) : null}

      <footer className="agent-card-foot">
        <Button
          as="a"
          size="sm"
          variant={action.primary ? "primary" : "secondary"}
          href={"#/chats/" + encodeURIComponent(agent.name)}
        >
          {action.label}
        </Button>
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
  const [query, setQuery] = useState("");
  const [reloadToken, setReloadToken] = useState(0);
  const [showCreate, setShowCreate] = useState(false);

  useEffect(() => {
    let alive = true;
    setStatus("loading");
    setError("");
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

  const showSkeleton =
    status !== "error" && (status === "loading" || (loading && agents.length === 0));

  return (
    <main id="main" className="page agents-page">
      <header className="ph">
        <div>
          <h1>Agents</h1>
          <p>Your team. Each agent has a job, its own room and its own memory.</p>
        </div>
        <div className="acts">
          <Button
            variant="primary"
            icon="plus"
            onClick={() => setShowCreate(true)}
          >
            Hire an agent
          </Button>
        </div>
      </header>

      <div className="agents-pulse">
        <div className="agents-pulse-counts">
          {PULSE_STATES.map((entry) => {
            const count = pulse.counts[entry.state] || 0;
            if (count === 0) {
              return null;
            }
            return (
              <Badge key={entry.state} tone={stateTone(entry.state)} dot>
                {count + " " + entry.word}
              </Badge>
            );
          })}
        </div>
        <p className="agents-pulse-total">
          {"Team did " + pulse.turns + " tasks this week"}
        </p>
      </div>

      <div className="agents-toolbar">
        <SearchInput
          wrapClassName="agents-search"
          aria-label="Search agents"
          placeholder="Search agents"
          autoComplete="off"
          spellCheck={false}
          kbd={null}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        {status === "ready" ? (
          <p className="agents-total">
            {visible.length + (visible.length === 1 ? " agent" : " agents")}
          </p>
        ) : null}
      </div>

      {status === "error" ? (
        <div className="panel" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not load agents"
            text={error}
            action={
              <Button
                variant="secondary"
                onClick={() => setReloadToken((current) => current + 1)}
              >
                Try again
              </Button>
            }
          />
        </div>
      ) : null}

      {showSkeleton ? (
        <div className="pgrid">
          {Array.from({ length: 3 }).map((_, index) => (
            <div key={index} className="pcard agent-skeleton">
              <div className="agent-card-head">
                <Skeleton width={26} height={26} radius="50%" />
                <div className="agent-card-id">
                  <Skeleton width="55%" height={13} />
                  <Skeleton width="35%" height={11} />
                </div>
              </div>
              <Skeleton width="100%" height={12} />
              <Skeleton width="85%" height={12} />
              <Skeleton width="100%" height={26} radius={6} />
            </div>
          ))}
        </div>
      ) : null}

      {status === "ready" && agents.length === 0 ? (
        <div className="panel">
          <EmptyState
            icon="bot"
            title="No agents yet"
            text="Create the first agent to give it a model, tools, and a persona."
            action={
              <Button
                variant="primary"
                icon="plus"
                onClick={() => setShowCreate(true)}
              >
                Hire an agent
              </Button>
            }
          />
        </div>
      ) : null}

      {status === "ready" && agents.length > 0 && visible.length === 0 ? (
        <div className="panel">
          <EmptyState
            icon="search-x"
            size="sm"
            title="No agents match"
            text={"No agents match “" + query.trim() + "”."}
            action={
              <Button variant="secondary" onClick={() => setQuery("")}>
                Clear search
              </Button>
            }
          />
        </div>
      ) : null}

      {status === "ready" && visible.length > 0 ? (
        <div className="pgrid">
          {visible.map((agent) => (
            <AgentCard
              key={agent.name}
              agent={agent}
              row={byName[agent.name]}
              rows={byName}
              onOpen={openCard}
            />
          ))}
          <button
            type="button"
            className="pcard agent-add"
            onClick={() => setShowCreate(true)}
          >
            <span className="agent-add-icon" aria-hidden="true">
              <Ic name="plus" size={18} />
            </span>
            <span className="agent-add-title">Hire a new agent</span>
            <span className="agent-add-text">
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
