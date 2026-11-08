import { useEffect, useRef, useState } from "react";
import type {
  ChangeEvent,
  FocusEvent,
  FormEvent,
  KeyboardEvent as ReactKeyboardEvent,
  MouseEvent as ReactMouseEvent,
} from "react";
import type { Agent } from "../api/agents";
import { listAgents } from "../api/agents";
import { uploadAttachments } from "../api/attachments";
import { ApiError, DAEMON_DRAINING_TEXT, errorMessage, isDaemonDraining } from "../api/client";
import { listSessions } from "../api/sessions";
import { createSession } from "../api/sessions";
import { sendMessage } from "../api/turns";
import { ErrorDetails, useAnnounce } from "../ui/ui";
import { Ic, LanaMark } from "../ui/icons";
import { Avatar, Button, SearchInput } from "../ui/gr8r";
import { AgentAvatar, AgentAvatarStack } from "./agent-avatar";
import { gr8rColor } from "./presence";
import { CreateAgentDialog } from "./agents";
import {
  AttachmentChips,
  AttachmentDropOverlay,
  AttachButton,
  pasteImageFiles,
  useAttachmentDraft,
  useFileDrop,
} from "./attachments";

/* REQ-WEB-041: the picker shows the agents of the most recent sessions first.
   A short RECENT list keeps it scannable; every other agent stays in AGENTS. */
const RECENT_LIMIT = 3;
const RECENT_POOL = 20;
const TASK_LIMIT = 32000;
const LISTBOX_ID = "new-session-agent-listbox";

function isTeam(agent: Agent | null): boolean {
  return Boolean(agent) && agent?.kind === "team";
}

function rosterSize(agent: Agent): number {
  const members = Array.isArray(agent.members) ? agent.members : [];
  return (agent.lead ? 1 : 0) + members.length;
}

/* The API returns a team with the lead's policy and no model. The session
   create call still needs a model, so a team uses the model of its lead. */
function qualifiedModel(agent: Agent, byName: Map<string, Agent>): string {
  let source: Agent | null = agent;
  if (isTeam(agent)) {
    source = (agent.lead ? byName.get(agent.lead) : null) || null;
  }
  if (!source || !source.model) {
    return "";
  }
  const model = String(source.model);
  if (model.includes(":")) {
    return model;
  }
  if (source.provider) {
    return source.provider + ":" + model;
  }
  /* A bare model would fail the API-WEB-005 format check; an empty model
     keeps the agent's configured model instead. */
  return "";
}

function modelLabel(agent: Agent, byName: Map<string, Agent>): string {
  return qualifiedModel(agent, byName) || "agent default";
}

function matchesQuery(agent: Agent, query: string): boolean {
  if (!query) {
    return true;
  }
  /* A team matches its name, its lead, its members, and its description, so
     typing a member name such as "research" finds the team too. */
  const fields: (string | undefined)[] = [agent.name, agent.description];
  if (isTeam(agent)) {
    fields.push(agent.lead);
    const members = Array.isArray(agent.members) ? agent.members : [];
    for (const member of members) {
      fields.push(member);
    }
  }
  const haystack = fields
    .filter((value): value is string => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase();
  return haystack.includes(query);
}

interface PickerOptions {
  byName: Map<string, Agent>;
  recent: Agent[];
  others: Agent[];
  all: Agent[];
}

function pickerOptions(
  agents: Agent[],
  recentNames: string[],
  search: string
): PickerOptions {
  const byName = new Map(agents.map((agent) => [agent.name, agent]));
  const query = search.trim().toLowerCase();
  const recent: Agent[] = [];
  const seen = new Set<string>();
  for (const name of recentNames) {
    const agent = byName.get(name);
    if (agent && !seen.has(name) && matchesQuery(agent, query)) {
      recent.push(agent);
      seen.add(name);
    }
  }
  const others = agents.filter(
    (agent) => !seen.has(agent.name) && matchesQuery(agent, query)
  );
  return { byName, recent, others, all: recent.concat(others) };
}

function optionId(index: number): string {
  return "new-session-option-" + index;
}

function TeamAvatars({ agent, byName }: { agent: Agent; byName: Map<string, Agent> }) {
  const names: string[] = [];
  if (agent.lead) {
    names.push(agent.lead);
  }
  const members = Array.isArray(agent.members) ? agent.members : [];
  for (const member of members) {
    if (names.length >= 3) {
      break;
    }
    if (member !== agent.lead && !names.includes(member)) {
      names.push(member);
    }
  }
  if (names.length === 0) {
    names.push(agent.name);
  }
  return (
    <AgentAvatarStack
      size="sm"
      max={3}
      items={names.map((name) => ({
        name,
        color: gr8rColor(byName.get(name)?.color),
      }))}
    />
  );
}

function AgentOption({
  agent,
  id,
  active,
  selected,
  byName,
  onSelect,
  onHover,
}: {
  agent: Agent;
  id: string;
  active: boolean;
  selected: boolean;
  byName: Map<string, Agent>;
  onSelect: (agent: Agent) => void;
  onHover: () => void;
}) {
  const team = isTeam(agent);
  return (
    <button
      type="button"
      role="option"
      id={id}
      className={"agent-option" + (active ? " is-active" : "")}
      aria-selected={selected ? "true" : "false"}
      tabIndex={-1}
      data-active={active ? "true" : undefined}
      onClick={() => onSelect(agent)}
      onMouseEnter={onHover}
    >
      {team ? (
        <TeamAvatars agent={agent} byName={byName} />
      ) : (
        <AgentAvatar name={agent.name} color={gr8rColor(agent.color)} size="md" />
      )}
      <span className="agent-option-text">
        <span className="agent-option-name">{agent.name}</span>
        <span className="agent-option-meta">
          {team
            ? "Team · " + rosterSize(agent) + " agents"
            : modelLabel(agent, byName)}
        </span>
      </span>
      {selected ? (
        <Ic name="check" size={14} className="agent-option-check" />
      ) : null}
    </button>
  );
}

function AgentPicker({
  agents,
  recentNames,
  selectedName,
  loading,
  disabled,
  onSelect,
  onCreateAgent,
}: {
  agents: Agent[];
  recentNames: string[];
  selectedName: string;
  loading: boolean;
  disabled: boolean;
  onSelect: (agent: Agent) => void;
  onCreateAgent: () => void;
}) {
  const announce = useAnnounce();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const chipRef = useRef<HTMLSpanElement>(null);
  const searchBoxRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const selected = agents.find((agent) => agent.name === selectedName) || null;
  const options = pickerOptions(agents, recentNames, search);
  const active = options.all.length
    ? Math.min(activeIndex, options.all.length - 1)
    : -1;

  useEffect(() => {
    if (!open) {
      return;
    }
    searchBoxRef.current
      ?.querySelector<HTMLInputElement>("input")
      ?.focus();
  }, [open]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    function handlePointerDown(event: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
        setOpen(false);
        setSearch("");
      }
    }
    document.addEventListener("pointerdown", handlePointerDown);
    return () => document.removeEventListener("pointerdown", handlePointerDown);
  }, [open]);

  useEffect(() => {
    if (!open || active < 0) {
      return;
    }
    const node =
      listRef.current &&
      listRef.current.querySelector<HTMLElement>('[data-active="true"]');
    if (node && typeof node.scrollIntoView === "function") {
      node.scrollIntoView({ block: "nearest" });
    }
  }, [open, active]);

  function openPicker() {
    if (disabled) {
      return;
    }
    setOpen(true);
    setSearch("");
    const info = pickerOptions(agents, recentNames, "");
    const current = info.all.findIndex((agent) => agent.name === selectedName);
    setActiveIndex(current >= 0 ? current : 0);
    announce("Agent picker opened. Use arrow keys to move and Enter to pick.");
  }

  function closePicker(restoreFocus: boolean) {
    setOpen(false);
    setSearch("");
    if (restoreFocus) {
      chipRef.current?.querySelector<HTMLButtonElement>("button")?.focus();
    }
  }

  function choose(agent: Agent) {
    if (!agent) {
      return;
    }
    onSelect(agent);
    setOpen(false);
    setSearch("");
  }

  function togglePicker() {
    if (open) {
      closePicker(false);
    } else {
      openPicker();
    }
  }

  function handleSearchInput(event: ChangeEvent<HTMLInputElement>) {
    setSearch(event.target.value);
    setActiveIndex(0);
  }

  function handleSearchKeyDown(event: ReactKeyboardEvent<HTMLInputElement>) {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      if (options.all.length === 0) {
        return;
      }
      setActiveIndex((current) =>
        Math.min(Math.max(current, -1) + 1, options.all.length - 1)
      );
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      if (options.all.length === 0) {
        return;
      }
      setActiveIndex((current) => Math.max(Math.max(current, 0) - 1, 0));
    } else if (event.key === "Enter") {
      event.preventDefault();
      if (active >= 0) {
        const agent = options.all[active];
        if (agent) {
          choose(agent);
        }
      }
    }
  }

  function handlePickerKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    if (event.key !== "Escape" || !open) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    closePicker(true);
  }

  function handleFocusOut(event: FocusEvent<HTMLDivElement>) {
    if (!open) {
      return;
    }
    const next = event.relatedTarget;
    if (next && rootRef.current && rootRef.current.contains(next as Node)) {
      return;
    }
    /* The click that picks an option can arrive after focusout. Let it land,
       then close only when focus really is outside the picker. */
    window.setTimeout(() => {
      const focused = document.activeElement;
      if (focused && rootRef.current && rootRef.current.contains(focused)) {
        return;
      }
      setOpen(false);
      setSearch("");
    }, 0);
  }

  function handleCreateAgent(event: ReactMouseEvent<HTMLAnchorElement>) {
    event.preventDefault();
    setOpen(false);
    setSearch("");
    if (onCreateAgent) {
      onCreateAgent();
      return;
    }
    window.location.hash = "#/agents";
  }

  return (
    <div
      className="agent-picker"
      ref={rootRef}
      onBlur={handleFocusOut}
      onKeyDown={handlePickerKeyDown}
    >
      <span className="agent-picker-trigger" ref={chipRef}>
        <Button
          variant="secondary"
          iconRight="chevron-down"
          aria-haspopup="listbox"
          aria-expanded={open}
          aria-controls={open ? LISTBOX_ID : undefined}
          aria-label={
            selected ? "Change agent: " + selected.name : "Pick an agent"
          }
          disabled={disabled}
          onClick={togglePicker}
        >
          {selected ? (
            <AgentAvatar
              name={selected.name}
              color={gr8rColor(selected.color)}
              size="sm"
            />
          ) : (
            <Avatar empty size="sm" />
          )}
          <span className="agent-chip-label">
            {loading
              ? "Loading agents…"
              : selected
                ? selected.name
                : "Pick an agent"}
          </span>
        </Button>
      </span>

      {open ? (
        <div className="agent-picker-menu">
          <div ref={searchBoxRef}>
            <SearchInput
              wrapClassName="agent-picker-search"
              kbd={null}
              size="sm"
              type="search"
              role="combobox"
              aria-label="Search agents and teams"
              aria-autocomplete="list"
              aria-expanded="true"
              aria-controls={LISTBOX_ID}
              aria-activedescendant={active >= 0 ? optionId(active) : undefined}
              placeholder="Search agents and teams"
              autoComplete="off"
              value={search}
              onChange={handleSearchInput}
              onKeyDown={handleSearchKeyDown}
            />
          </div>
          <span className="agent-picker-count" aria-hidden="true">
            {options.all.length}
          </span>

          <div
            className="agent-picker-list"
            id={LISTBOX_ID}
            role="listbox"
            aria-label="Agents and teams"
            ref={listRef}
          >
            {options.recent.length > 0 ? (
              <div className="agent-picker-group" role="group" aria-label="Recent">
                <div className="mh" aria-hidden="true">
                  Recent
                </div>
                {options.recent.map((agent, index) => (
                  <AgentOption
                    key={agent.name}
                    agent={agent}
                    id={optionId(index)}
                    active={active === index}
                    selected={agent.name === selectedName}
                    byName={options.byName}
                    onSelect={choose}
                    onHover={() => setActiveIndex(index)}
                  />
                ))}
              </div>
            ) : null}
            {options.others.length > 0 ? (
              <div className="agent-picker-group" role="group" aria-label="Agents">
                <div className="mh" aria-hidden="true">
                  Agents
                </div>
                {options.others.map((agent, index) => {
                  const optionIndex = options.recent.length + index;
                  return (
                    <AgentOption
                      key={agent.name}
                      agent={agent}
                      id={optionId(optionIndex)}
                      active={active === optionIndex}
                      selected={agent.name === selectedName}
                      byName={options.byName}
                      onSelect={choose}
                      onHover={() => setActiveIndex(optionIndex)}
                    />
                  );
                })}
              </div>
            ) : null}
          </div>

          {options.all.length === 0 ? (
            <p className="agent-picker-empty" role="status">
              No agents or teams found.
            </p>
          ) : null}

          <div className="agent-picker-footer">
            <a
              className="agent-picker-create"
              href="#/agents"
              onClick={handleCreateAgent}
            >
              <Ic name="plus" size={13} />
              <span>Create agent</span>
            </a>
            <span className="agent-picker-hint">↑↓ to move · Enter to pick</span>
          </div>
        </div>
      ) : null}
    </div>
  );
}

export function NewSessionPage() {
  const announce = useAnnounce();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [recentNames, setRecentNames] = useState<string[]>([]);
  const [selectedName, setSelectedName] = useState("");
  const [text, setText] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [createdId, setCreatedId] = useState("");
  const [showCreateAgent, setShowCreateAgent] = useState(false);
  const taskRef = useRef<HTMLTextAreaElement>(null);
  const attachmentDraft = useAttachmentDraft();
  const fileDrop = useFileDrop(attachmentDraft.addFiles);

  /* eslint-disable react-hooks/set-state-in-effect -- the picker load starts from a clean state, as in new-session.js */
  useEffect(() => {
    let alive = true;
    setLoading(true);

    /* A failed recent list only empties RECENT; the picker still works. */
    const sessions = listSessions({ limit: RECENT_POOL }).catch(() => ({
      sessions: [] as Awaited<ReturnType<typeof listSessions>>["sessions"],
    }));

    Promise.all([listAgents(), sessions])
      .then(([page, sessionPage]) => {
        if (!alive) {
          return;
        }
        const list = (page.agents || [])
          .filter((agent) => agent && agent.name)
          .sort((left, right) => {
            if (Boolean(left.default) !== Boolean(right.default)) {
              return left.default ? -1 : 1;
            }
            return left.name.localeCompare(right.name);
          });
        setAgents(list);
        const preferred = list.find((agent) => agent.default) || list[0];
        setSelectedName(preferred ? preferred.name : "");

        const known = new Set(list.map((agent) => agent.name));
        const recent: string[] = [];
        for (const session of sessionPage.sessions || []) {
          const name = session && session.agent;
          if (name && known.has(name) && !recent.includes(name)) {
            recent.push(name);
          }
          if (recent.length >= RECENT_LIMIT) {
            break;
          }
        }
        setRecentNames(recent);
      })
      .catch((failure: unknown) => {
        if (!alive) {
          return;
        }
        const message = "Could not load agents. " + errorMessage(failure);
        setError(message);
        setDetails(failure instanceof ApiError ? failure.details : []);
        announce(message);
      })
      .finally(() => {
        if (alive) {
          setLoading(false);
        }
      });

    return () => {
      alive = false;
    };
  }, [announce]);
  /* eslint-enable react-hooks/set-state-in-effect */

  const byName = new Map(agents.map((agent) => [agent.name, agent]));
  const selected = agents.find((agent) => agent.name === selectedName) || null;

  function selectAgent(agent: Agent) {
    setSelectedName(agent.name);
    setCreatedId("");
    setError("");
    setDetails([]);
    if (taskRef.current) {
      taskRef.current.focus();
    }
    announce(agent.name + " selected.");
  }

  function handleAgentCreated(createdName: string) {
    setShowCreateAgent(false);
    setCreatedId("");
    listAgents()
      .then((page) => {
        const list = (page.agents || [])
          .filter((agent) => agent && agent.name)
          .sort((left, right) => {
            if (Boolean(left.default) !== Boolean(right.default)) {
              return left.default ? -1 : 1;
            }
            return left.name.localeCompare(right.name);
          });
        setAgents(list);
        if (list.some((agent) => agent.name === createdName)) {
          setSelectedName(createdName);
        }
      })
      .catch((failure: unknown) => {
        setError(
          "The agent was created, but the list could not refresh. " +
            errorMessage(failure)
        );
      });
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const task = text.trim();
    const files = attachmentDraft.items.slice();
    if (!selected) {
      setError("Pick an agent or team first.");
      return;
    }
    if (!task && files.length === 0) {
      setError("Describe what to work on first.");
      if (taskRef.current) {
        taskRef.current.focus();
      }
      return;
    }

    setBusy(true);
    setError("");
    setDetails([]);

    let sessionId = createdId;
    if (!sessionId) {
      try {
        const created = await createSession(
          selected.name,
          qualifiedModel(selected, byName)
        );
        if (!created || !created.id) {
          throw new ApiError(
            0,
            "invalid_response",
            "The daemon did not return a session id.",
            []
          );
        }
        sessionId = String(created.id);
        setCreatedId(sessionId);
      } catch (failure) {
        const message = errorMessage(failure);
        setError(message);
        setDetails(failure instanceof ApiError ? failure.details : []);
        announce("Could not create the session. " + message);
        setBusy(false);
        return;
      }
    }

    let paths: string[] = [];
    if (files.length > 0) {
      try {
        /* API-ATT-001: the session already exists, so a failed upload keeps
           it and the chips so the owner can retry. */
        const uploaded = await uploadAttachments(
          sessionId,
          files.map((item) => item.file)
        );
        paths = uploaded.map((item) => item.path);
      } catch (failure) {
        const message = isDaemonDraining(failure)
          ? DAEMON_DRAINING_TEXT
          : "The session was created, but the attachments did not upload. " +
            errorMessage(failure);
        setError(message);
        setDetails(failure instanceof ApiError ? failure.details : []);
        announce(message);
        setBusy(false);
        return;
      }
    }

    try {
      if (paths.length > 0) {
        await sendMessage(sessionId, task, paths);
      } else {
        await sendMessage(sessionId, task);
      }
    } catch (failure) {
      /* API-TFB-008: the daemon refuses new turns while it drains. Keep the
         typed task so the owner can start it again after the restart. */
      const message = isDaemonDraining(failure)
        ? DAEMON_DRAINING_TEXT
        : "The session was created, but the first message did not send. " +
          errorMessage(failure);
      setError(message);
      setDetails(failure instanceof ApiError ? failure.details : []);
      announce(message);
      setBusy(false);
      return;
    }

    /* The sent message owns its attachments now, so the draft is cleared. */
    attachmentDraft.clear();
    announce("Session started with " + selected.name + ".");
    setBusy(false);
    window.location.hash = "#/sessions/" + encodeURIComponent(sessionId);
  }

  return (
    <main id="main" className="page new-session-page">
      <div className="new-session-content">
        <header className="new-session-head">
          <span className="new-session-mark">
            <LanaMark size={28} />
          </span>
          <div className="new-session-head-text">
            <h1 className="new-session-title">New session</h1>
            <p className="new-session-subtitle">
              Pick who works on it, then describe the task.
            </p>
          </div>
        </header>

        <form
          className="new-session-form"
          onSubmit={submit}
          {...fileDrop.handlers}
        >
          <div className="cbox new-session-composer">
            <label className="sr" htmlFor="new-session-task">
              What should we work on?
            </label>
            <AttachmentChips
              items={attachmentDraft.items}
              onRemove={attachmentDraft.remove}
            />
            {attachmentDraft.notice ? (
              <p className="composer-status" role="status">
                {attachmentDraft.notice}
              </p>
            ) : null}
            <textarea
              id="new-session-task"
              ref={taskRef}
              className="new-session-input"
              placeholder="What should we work on?"
              rows={3}
              maxLength={TASK_LIMIT}
              value={text}
              disabled={busy}
              onChange={(event) => setText(event.target.value)}
              onPaste={(event) => pasteImageFiles(event, attachmentDraft.addFiles)}
            ></textarea>

            <div className="new-session-bar">
              <AttachButton
                disabled={busy}
                onFiles={attachmentDraft.addFiles}
              />
              <AgentPicker
                agents={agents}
                recentNames={recentNames}
                selectedName={selectedName}
                loading={loading}
                disabled={loading || busy || agents.length === 0}
                onSelect={selectAgent}
                onCreateAgent={() => setShowCreateAgent(true)}
              />
              <span className="sp"></span>
              <Button
                type="submit"
                variant="primary"
                disabled={loading || busy || !selected}
              >
                {busy ? "Starting…" : "Start session"}
              </Button>
            </div>
          </div>

          {!loading && !error && agents.length === 0 ? (
            <p className="new-session-empty">
              No agents are configured yet.{" "}
              <a href="#/agents">Create an agent</a> to start a session.
            </p>
          ) : null}

          {error ? (
            <div className="alert danger new-session-error" role="alert">
              <Ic name="triangle-alert" size={15} />
              <div>
                <p className="new-session-error-message">{error}</p>
                <ErrorDetails details={details} />
                {createdId ? (
                  <p className="new-session-error-link">
                    <a href={"#/sessions/" + encodeURIComponent(createdId)}>
                      Open the created session
                    </a>
                  </p>
                ) : null}
              </div>
            </div>
          ) : null}

          <AttachmentDropOverlay active={fileDrop.active} />
        </form>
      </div>

      {showCreateAgent ? (
        <CreateAgentDialog
          onClose={() => setShowCreateAgent(false)}
          onCreated={handleAgentCreated}
        />
      ) : null}
    </main>
  );
}
