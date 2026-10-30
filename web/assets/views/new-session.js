import {
  html,
  useState,
  useEffect,
  useRef,
} from "/assets/vendor/preact-htm.js";
import { api, errorMessage, ApiError } from "/assets/api.js";
import { useAnnounce, ErrorDetails } from "/assets/ui.js";
import { avatarFor } from "/assets/avatar.js";
import { CreateAgentDialog } from "/assets/views/agents.js";

/* REQ-WEB-041: the picker shows the agents of the most recent sessions first.
   A short RECENT list keeps it scannable; every other agent stays in AGENTS. */
const RECENT_LIMIT = 3;
const RECENT_POOL = 20;
const TASK_LIMIT = 32000;
const LISTBOX_ID = "new-session-agent-listbox";

function Icon({ name, className = "icon-20" }) {
  return html`<img
    class=${className}
    src=${"/assets/icons/" + name + ".svg"}
    alt=""
    aria-hidden="true"
  />`;
}

function AgentAvatar({ name, size = "28" }) {
  const avatar = avatarFor(name);
  return html`<span
    class=${"agent-avatar avatar-" + size + " " + avatar.colorClass}
    aria-hidden="true"
  >${avatar.initial}</span>`;
}

function isTeam(agent) {
  return Boolean(agent) && agent.kind === "team";
}

function rosterSize(agent) {
  const members = Array.isArray(agent.members) ? agent.members : [];
  return (agent.lead ? 1 : 0) + members.length;
}

/* The API returns a team with the lead's policy and no model. The session
   create call still needs a model, so a team uses the model of its lead. */
function qualifiedModel(agent, byName) {
  let source = agent;
  if (isTeam(agent)) {
    source = byName.get(agent.lead) || null;
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

function modelLabel(agent, byName) {
  return qualifiedModel(agent, byName) || "agent default";
}

function matchesQuery(agent, query) {
  if (!query) {
    return true;
  }
  /* A team matches its name, its lead, its members, and its description, so
     typing a member name such as "research" finds the team too. */
  const fields = [agent.name, agent.description];
  if (isTeam(agent)) {
    fields.push(agent.lead);
    const members = Array.isArray(agent.members) ? agent.members : [];
    for (const member of members) {
      fields.push(member);
    }
  }
  const haystack = fields
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase();
  return haystack.includes(query);
}

function pickerOptions(agents, recentNames, search) {
  const byName = new Map(agents.map((agent) => [agent.name, agent]));
  const query = search.trim().toLowerCase();
  const recent = [];
  const seen = new Set();
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

function optionId(index) {
  return "new-session-option-" + index;
}

function TeamAvatars({ agent }) {
  const names = [];
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
  return html`<span class="agent-avatar-group" aria-hidden="true">
    ${names.map((name) => html`<${AgentAvatar} key=${name} name=${name} size="26" />`)}
  </span>`;
}

function AgentOption({
  agent,
  id,
  active,
  selected,
  byName,
  onSelect,
  onHover,
}) {
  const team = isTeam(agent);
  return html`<button
    type="button"
    role="option"
    id=${id}
    class=${"agent-option" + (team ? " agent-option-team" : "") + (active ? " is-active" : "")}
    aria-selected=${selected ? "true" : "false"}
    tabIndex=${-1}
    data-active=${active ? "true" : undefined}
    onClick=${() => onSelect(agent)}
    onMouseEnter=${onHover}
  >
    ${team
      ? html`<${TeamAvatars} agent=${agent} />`
      : html`<${AgentAvatar} name=${agent.name} size="28" />`}
    <span class="agent-option-text">
      <span class="agent-option-name">${agent.name}</span>
      <span class="agent-option-meta">
        ${team ? "Team · " + rosterSize(agent) + " agents" : modelLabel(agent, byName)}
      </span>
    </span>
    ${selected
      ? html`<${Icon} name="check" className="icon-16 agent-option-check" />`
      : null}
  </button>`;
}

function AgentPicker({
  agents,
  recentNames,
  selectedName,
  loading,
  disabled,
  onSelect,
  onCreateAgent,
}) {
  const announce = useAnnounce();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const rootRef = useRef(null);
  const chipRef = useRef(null);
  const searchRef = useRef(null);
  const listRef = useRef(null);

  const selected = agents.find((agent) => agent.name === selectedName) || null;
  const options = pickerOptions(agents, recentNames, search);
  const active = options.all.length
    ? Math.min(activeIndex, options.all.length - 1)
    : -1;

  useEffect(() => {
    if (!open) {
      return;
    }
    if (searchRef.current) {
      searchRef.current.focus();
    }
  }, [open]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    function handlePointerDown(event) {
      if (rootRef.current && !rootRef.current.contains(event.target)) {
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
      listRef.current.querySelector('[data-active="true"]');
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

  function closePicker(restoreFocus) {
    setOpen(false);
    setSearch("");
    if (restoreFocus && chipRef.current) {
      chipRef.current.focus();
    }
  }

  function choose(agent) {
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

  function handleSearchInput(event) {
    setSearch(event.target.value);
    setActiveIndex(0);
  }

  function handleSearchKeyDown(event) {
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
        choose(options.all[active]);
      }
    }
  }

  function handlePickerKeyDown(event) {
    if (event.key !== "Escape" || !open) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    closePicker(true);
  }

  function handleFocusOut(event) {
    if (!open) {
      return;
    }
    const next = event.relatedTarget;
    if (next && rootRef.current && rootRef.current.contains(next)) {
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

  function handleCreateAgent(event) {
    event.preventDefault();
    setOpen(false);
    setSearch("");
    if (onCreateAgent) {
      onCreateAgent();
      return;
    }
    window.location.hash = "#/agents";
  }

  return html`<div
    class="agent-picker"
    ref=${rootRef}
    onFocusOut=${handleFocusOut}
    onKeyDown=${handlePickerKeyDown}
  >
    <button
      ref=${chipRef}
      type="button"
      class=${"agent-chip" + (open ? " is-open" : "")}
      aria-haspopup="listbox"
      aria-expanded=${open}
      aria-controls=${open ? LISTBOX_ID : undefined}
      aria-label=${selected ? "Change agent: " + selected.name : "Pick an agent"}
      disabled=${disabled}
      onClick=${togglePicker}
    >
      ${selected
        ? html`<${AgentAvatar} name=${selected.name} size="22" />`
        : html`<span class="agent-avatar avatar-22 tone-gray" aria-hidden="true">?</span>`}
      <span class="agent-chip-label">
        ${loading ? "Loading agents…" : selected ? selected.name : "Pick an agent"}
      </span>
      <${Icon} name="chevron-down" />
    </button>

    ${open
      ? html`<div class="agent-picker-menu">
          <div class="agent-picker-search">
            <${Icon} name="search" />
            <input
              ref=${searchRef}
              class="agent-picker-search-input"
              type="search"
              role="combobox"
              aria-label="Search agents and teams"
              aria-autocomplete="list"
              aria-expanded="true"
              aria-controls=${LISTBOX_ID}
              aria-activedescendant=${active >= 0 ? optionId(active) : undefined}
              placeholder="Search agents and teams"
              autocomplete="off"
              value=${search}
              onInput=${handleSearchInput}
              onKeyDown=${handleSearchKeyDown}
            />
            <span class="agent-picker-count" aria-hidden="true">${options.all.length}</span>
          </div>

          <div
            class="agent-picker-list"
            id=${LISTBOX_ID}
            role="listbox"
            aria-label="Agents and teams"
            ref=${listRef}
          >
            ${options.recent.length > 0
              ? html`<div class="agent-picker-group" role="group" aria-label="Recent">
                  <div class="agent-picker-heading" aria-hidden="true">RECENT</div>
                  ${options.recent.map((agent, index) =>
                    html`<${AgentOption}
                      key=${agent.name}
                      agent=${agent}
                      id=${optionId(index)}
                      active=${active === index}
                      selected=${agent.name === selectedName}
                      byName=${options.byName}
                      onSelect=${choose}
                      onHover=${() => setActiveIndex(index)}
                    />`
                  )}
                </div>`
              : null}
            ${options.others.length > 0
              ? html`<div class="agent-picker-group" role="group" aria-label="Agents">
                  <div class="agent-picker-heading" aria-hidden="true">AGENTS</div>
                  ${options.others.map((agent, index) => {
                    const optionIndex = options.recent.length + index;
                    return html`<${AgentOption}
                      key=${agent.name}
                      agent=${agent}
                      id=${optionId(optionIndex)}
                      active=${active === optionIndex}
                      selected=${agent.name === selectedName}
                      byName=${options.byName}
                      onSelect=${choose}
                      onHover=${() => setActiveIndex(optionIndex)}
                    />`;
                  })}
                </div>`
              : null}
          </div>

          ${options.all.length === 0
            ? html`<p class="agent-picker-empty" role="status">
                No agents or teams found.
              </p>`
            : null}

          <div class="agent-picker-footer">
            <a class="agent-picker-create" href="#/agents" onClick=${handleCreateAgent}>
              <${Icon} name="plus" />
              <span>Create agent</span>
            </a>
            <span class="agent-picker-hint">↑↓ to move · Enter to pick</span>
          </div>
        </div>`
      : null}
  </div>`;
}

export function NewSessionPage() {
  const announce = useAnnounce();
  const [agents, setAgents] = useState([]);
  const [recentNames, setRecentNames] = useState([]);
  const [selectedName, setSelectedName] = useState("");
  const [text, setText] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [createdId, setCreatedId] = useState("");
  const [showCreateAgent, setShowCreateAgent] = useState(false);
  const taskRef = useRef(null);

  useEffect(() => {
    let alive = true;
    setLoading(true);

    /* A failed recent list only empties RECENT; the picker still works. */
    const sessions = api
      .listSessions({ limit: RECENT_POOL })
      .catch(() => ({ sessions: [] }));

    Promise.all([api.listAgents(), sessions])
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
        const recent = [];
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
      .catch((failure) => {
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

  const byName = new Map(agents.map((agent) => [agent.name, agent]));
  const selected = agents.find((agent) => agent.name === selectedName) || null;

  function selectAgent(agent) {
    setSelectedName(agent.name);
    setCreatedId("");
    setError("");
    setDetails([]);
    if (taskRef.current) {
      taskRef.current.focus();
    }
    announce(agent.name + " selected.");
  }

  function handleAgentCreated(createdName) {
    setShowCreateAgent(false);
    setCreatedId("");
    api
      .listAgents()
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
      .catch((failure) => {
        setError(
          "The agent was created, but the list could not refresh. " +
            errorMessage(failure)
        );
      });
  }

  async function submit(event) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const task = text.trim();
    if (!selected) {
      setError("Pick an agent or team first.");
      return;
    }
    if (!task) {
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
        const created = await api.createSession(
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

    try {
      await api.sendMessage(sessionId, task);
    } catch (failure) {
      const message =
        "The session was created, but the first message did not send. " +
        errorMessage(failure);
      setError(message);
      setDetails(failure instanceof ApiError ? failure.details : []);
      announce(message);
      setBusy(false);
      return;
    }

    announce("Session started with " + selected.name + ".");
    setBusy(false);
    window.location.hash = "#/sessions/" + encodeURIComponent(sessionId);
  }

  return html`<main id="main" class="new-session-view">
    <div class="new-session-content">
      <header class="new-session-heading">
        <span class="new-session-mark">
          <img
            class="new-session-logo"
            src="/assets/icons/logo-mark.svg"
            alt=""
            aria-hidden="true"
          />
        </span>
        <h1 class="new-session-title">New session</h1>
        <p class="new-session-subtitle">
          Pick who works on it, then describe the task.
        </p>
      </header>

      <form class="new-session-form" onSubmit=${submit}>
        <div class="new-session-composer">
          <label class="sr-only" for="new-session-task">
            What should we work on?
          </label>
          <textarea
            id="new-session-task"
            ref=${taskRef}
            class="new-session-input"
            placeholder="What should we work on?"
            rows=${3}
            maxLength=${TASK_LIMIT}
            value=${text}
            disabled=${busy}
            onInput=${(event) => setText(event.target.value)}
          ></textarea>

          <div class="new-session-bar">
            <${AgentPicker}
              agents=${agents}
              recentNames=${recentNames}
              selectedName=${selectedName}
              loading=${loading}
              disabled=${loading || busy || agents.length === 0}
              onSelect=${selectAgent}
              onCreateAgent=${() => setShowCreateAgent(true)}
            />
            <span class="new-session-spacer"></span>
            <button
              type="submit"
              class="button-ui button-ui-primary new-session-start"
              disabled=${loading || busy || !selected}
            >
              ${busy ? "Starting…" : "Start session"}
            </button>
          </div>
        </div>

        ${!loading && !error && agents.length === 0
          ? html`<p class="new-session-empty">
              No agents are configured yet.
              <a href="#/agents">Create an agent</a> to start a session.
            </p>`
          : null}

        ${error
          ? html`<div class="new-session-error" role="alert">
              <p class="new-session-error-message">${error}</p>
              <${ErrorDetails} details=${details} />
              ${createdId
                ? html`<p class="new-session-error-link">
                    <a href=${"#/sessions/" + encodeURIComponent(createdId)}>
                      Open the created session
                    </a>
                  </p>`
                : null}
            </div>`
          : null}
      </form>
    </div>

    ${showCreateAgent
      ? html`<${CreateAgentDialog}
          onClose=${() => setShowCreateAgent(false)}
          onCreated=${handleAgentCreated}
        />`
      : null}
  </main>`;
}
