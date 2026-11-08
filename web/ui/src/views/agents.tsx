/* eslint-disable react-hooks/set-state-in-effect -- the list resets its load state before each request, as in agents.js */
import { useEffect, useMemo, useState } from "react";
import type { FormEvent, MouseEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import { createAgent, listAgents } from "../api/agents";
import type { Agent, CreateAgentRequest } from "../api/agents";
import { Dialog, ErrorNotice, useAnnounce } from "../ui/ui";
import { Ic } from "../ui/icons";
import { avatarFor } from "../lib/avatar";
import {
  CLAUDE_RUNTIME,
  isTeam,
  rosterNames,
  agentHref,
  engineLabel,
  splitModel,
  providerLabel,
} from "./agent-common";
import {
  CLAUDE_AUTH,
  CLAUDE_EFFORTS,
  ClaudeModelControl,
  DEFAULT_CLAUDE,
  RuntimeChoice,
} from "./agent-runtime";
import type { ClaudeValues, RuntimeValue } from "./agent-runtime";
import type { AgentLike } from "./agent-common";
import { useRooms } from "./presence";
import type { RoomRow } from "../api/rooms";
import {
  Avatar,
  AvatarStack,
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Lbl,
  SearchInput,
  Select,
  Skeleton,
  Toggle,
} from "../ui/gr8r";
import type { BadgeTone } from "../ui/gr8r";

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
  resting: "Idle",
};

/* The agent identity colors of lib/avatar.ts map onto the Gr8r status tokens,
   so one agent keeps one color in the picker, the chat, and the roster. */
const AVATAR_COLORS: Record<string, string> = {
  "tone-brand": "var(--violet)",
  "tone-success": "var(--green)",
  "tone-warning": "var(--amber)",
  "tone-error": "var(--red)",
  "tone-gray": "var(--gray)",
};

function agentColor(name: string): string {
  return AVATAR_COLORS[avatarFor(name).colorClass] || "var(--violet)";
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
  /* The runtime picked when the dialog opens, for example Claude Agent from
     the Router's Claude Code page. */
  initialRuntime?: RuntimeValue;
}

const PERMISSION_OPTIONS = [
  { value: "ask", label: "Ask before risky tools" },
  { value: "trust", label: "Trust: run every tool" },
  { value: "never", label: "Never: read-only work" },
];

const COLOR_OPTIONS = [
  { value: "", label: "Automatic" },
  { value: "brand", label: "Brand" },
  { value: "success", label: "Success" },
  { value: "warning", label: "Warning" },
  { value: "error", label: "Error" },
  { value: "blue", label: "Blue" },
  { value: "gray", label: "Gray" },
];

const ENV_RULE = /^[A-Za-z_][A-Za-z0-9_]*$/;

function lines(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
}

export function CreateAgentDialog({
  onClose,
  onCreated,
  initialRuntime,
}: CreateAgentDialogProps) {
  const announce = useAnnounce();
  const [name, setName] = useState("");
  const [runtime, setRuntime] = useState<RuntimeValue>(initialRuntime || "");
  const [template, setTemplate] = useState<string>(TEMPLATES[0].value);
  const [workspace, setWorkspace] = useState("");
  const [claude, setClaude] = useState<ClaudeValues>(DEFAULT_CLAUDE);
  const [permission, setPermission] = useState("ask");
  const [title, setTitle] = useState("");
  const [color, setColor] = useState("");
  const [description, setDescription] = useState("");
  const [allowRules, setAllowRules] = useState("");
  const [denyRules, setDenyRules] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  const templateInfo =
    TEMPLATES.find((item) => item.value === template) || TEMPLATES[0];
  const isClaude = runtime === CLAUDE_RUNTIME;

  function updateClaude<K extends keyof ClaudeValues>(key: K, value: ClaudeValues[K]) {
    setClaude((current) => ({ ...current, [key]: value }));
  }

  /* REQ-CCR-001: a Claude Agent sends runtime and its own keys, never a
     provider or a template. */
  function claudeBody(value: string): CreateAgentRequest {
    const body: CreateAgentRequest = {
      name: value,
      runtime: CLAUDE_RUNTIME,
      auth: claude.auth,
      permission,
      allow_scheduled: claude.allow_scheduled,
    };
    if (claude.model.trim()) {
      body.model = claude.model.trim();
    }
    if (claude.reasoning_effort) {
      body.reasoning_effort = claude.reasoning_effort;
    }
    if (claude.auth === "api_key") {
      body.api_key_env = claude.api_key_env.trim();
    }
    if (claude.binary.trim()) {
      body.binary = claude.binary.trim();
    }
    if (workspace.trim()) {
      body.workspace = workspace.trim();
    }
    if (title.trim()) {
      body.title = title.trim();
    }
    if (color) {
      body.color = color;
    }
    if (description.trim()) {
      body.description = description.trim();
    }
    const allow = lines(allowRules);
    const deny = lines(denyRules);
    if (allow.length || deny.length) {
      body.rules = { allow, deny };
    }
    return body;
  }

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
    if (isClaude && claude.auth === "api_key" && !ENV_RULE.test(claude.api_key_env.trim())) {
      setError("Enter the env variable that holds the API key, for example ANTHROPIC_API_KEY.");
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      const body: CreateAgentRequest = isClaude
        ? claudeBody(value)
        : { name: value, template, workspace: workspace.trim() };
      const created = await createAgent(body);
      const createdName = created && created.name ? created.name : value;
      announce(
        "Agent " +
          createdName +
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
          {isClaude
            ? "A Claude Agent runs Claude Code on this machine. Log in once with claude in a terminal."
            : "Create an agent from a template. You can change the model and the other settings after it appears."}
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

        <div className="field">
          <span className="field-label" id="create-agent-runtime-label">
            Runtime
          </span>
          <RuntimeChoice
            name="create-agent-runtime"
            labelledBy="create-agent-runtime-label"
            value={runtime}
            disabled={busy}
            onChange={setRuntime}
          />
        </div>

        {isClaude ? (
          <>
            <div className="field">
              <label className="field-label" htmlFor="create-agent-claude-model">
                Model
              </label>
              <ClaudeModelControl
                id="create-agent-claude-model"
                value={claude.model}
                disabled={busy}
                onChange={(value) => updateClaude("model", value)}
              />
            </div>
            <div className="field">
              <label className="field-label" htmlFor="create-agent-effort">
                Reasoning effort
              </label>
              <Select
                id="create-agent-effort"
                value={claude.reasoning_effort}
                options={CLAUDE_EFFORTS}
                disabled={busy}
                onChange={(value) => updateClaude("reasoning_effort", value)}
              />
            </div>
            <div className="field">
              <label className="field-label" htmlFor="create-agent-auth">
                Sign-in
              </label>
              <Select
                id="create-agent-auth"
                value={claude.auth}
                options={CLAUDE_AUTH.map((entry) => ({ value: entry.value, label: entry.title }))}
                disabled={busy}
                onChange={(value) => updateClaude("auth", value)}
              />
              <span className="hint">
                {CLAUDE_AUTH.find((entry) => entry.value === claude.auth)?.note}
              </span>
            </div>
            {claude.auth === "api_key" ? (
              <label className="field">
                <span className="field-label">API key variable</span>
                <input
                  type="text"
                  name="api_key_env"
                  value={claude.api_key_env}
                  placeholder="ANTHROPIC_API_KEY"
                  autoComplete="off"
                  spellCheck={false}
                  onChange={(event) => updateClaude("api_key_env", event.target.value)}
                />
                <span className="hint">The env variable or secret that holds the key.</span>
              </label>
            ) : null}
            <label className="field">
              <span className="field-label">Claude Code binary</span>
              <input
                type="text"
                name="binary"
                value={claude.binary}
                placeholder="claude on PATH"
                autoComplete="off"
                spellCheck={false}
                onChange={(event) => updateClaude("binary", event.target.value)}
              />
              <span className="hint">Optional. Leave empty to use claude from PATH.</span>
            </label>
          </>
        ) : (
          <div className="field">
            <label className="field-label" htmlFor="create-agent-template">
              Template
            </label>
            <Select
              id="create-agent-template"
              name="template"
              value={template}
              onChange={setTemplate}
              options={TEMPLATES.map((item) => ({
                value: item.value,
                label: item.label,
              }))}
            />
            <span className="hint">{templateInfo.note}</span>
          </div>
        )}

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
            {isClaude
              ? "The folder Claude Code works in. Leave empty to use the default workspace."
              : "Optional. Leave empty to use the default workspace."}
          </span>
        </label>

        {isClaude ? (
          <>
            <div className="field">
              <label className="field-label" htmlFor="create-agent-permission">
                Permission
              </label>
              <Select
                id="create-agent-permission"
                value={permission}
                options={PERMISSION_OPTIONS}
                disabled={busy}
                onChange={setPermission}
              />
            </div>
            <label className="field">
              <span className="field-label">Deny rules</span>
              <textarea
                name="deny"
                rows={2}
                value={denyRules}
                placeholder="bash(rm *)"
                spellCheck={false}
                onChange={(event) => setDenyRules(event.target.value)}
              />
              <span className="hint">One pattern per line. Optional.</span>
            </label>
            <label className="field">
              <span className="field-label">Allow rules</span>
              <textarea
                name="allow"
                rows={2}
                value={allowRules}
                spellCheck={false}
                onChange={(event) => setAllowRules(event.target.value)}
              />
              <span className="hint">One pattern per line. Optional.</span>
            </label>
            <div className="field agent-dialog-switch">
              <span className="field-label" id="create-agent-scheduled-label">
                Allow scheduled runs
              </span>
              <Toggle
                aria-labelledby="create-agent-scheduled-label"
                checked={claude.allow_scheduled}
                disabled={busy}
                onChange={(checked) => updateClaude("allow_scheduled", checked)}
              />
              <span className="hint">
                Off by default: schedules and workflows cannot start this agent unattended.
              </span>
            </div>
            <label className="field">
              <span className="field-label">Title</span>
              <input
                type="text"
                name="title"
                value={title}
                maxLength={60}
                autoComplete="off"
                onChange={(event) => setTitle(event.target.value)}
              />
            </label>
            <div className="field">
              <label className="field-label" htmlFor="create-agent-color">
                Color
              </label>
              <Select
                id="create-agent-color"
                value={color}
                options={COLOR_OPTIONS}
                disabled={busy}
                onChange={setColor}
              />
            </div>
            <label className="field">
              <span className="field-label">Description</span>
              <textarea
                name="description"
                rows={2}
                value={description}
                onChange={(event) => setDescription(event.target.value)}
              />
            </label>
          </>
        ) : null}

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
    engineLabel(agent),
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
  const presence = row ? row.presence : null;
  const statusWord =
    presence && presence.label
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
    ? "Team" + (row && row.lead ? " · lead " + row.lead : "")
    : (agent.title || "Agent") + (agent.default ? " · default" : "");
  const foot = team
    ? members > 0
      ? members + (members === 1 ? " member" : " members")
      : "Team"
    : Number(row ? row.turns_7d : 0) + " tasks · 7d";
  const model = splitModel(agent);
  const engine = engineLabel(agent);
  const modelText = model.model
    ? model.provider
      ? providerLabel(model.provider) + " · " + model.model
      : model.model
    : "";
  /* Spec 38: a Claude Agent shows its engine before the model. */
  const modelLabel = engine
    ? engine + (model.model ? " · " + model.model : "")
    : modelText;

  return (
    <article className="pcard" onClick={(event) => onOpen(event, agent.name)}>
      <div className="agent-card-head">
        {team ? (
          <AvatarStack
            size="md"
            max={3}
            items={rosterNames(agent).map((name) => ({
              name,
              color: agentColor(name),
            }))}
          />
        ) : (
          <Avatar
            name={agent.name}
            color={agentColor(agent.name)}
            size="md"
            presence={state === "online" || state === "working"}
          />
        )}
        <div className="agent-card-id">
          <a className="agent-card-name" href={agentHref(agent.name)}>
            {agent.name}
          </a>
          <span className="agent-card-role">{role}</span>
        </div>
        <Badge tone={stateTone(state)} dot>
          {statusWord}
        </Badge>
      </div>

      <p className="agent-card-preview">{description}</p>

      {modelLabel ? (
        <div className="agent-card-meta">
          <Lbl color="var(--text-4)">{modelLabel}</Lbl>
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
