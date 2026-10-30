import {
  html,
  useState,
  useEffect,
  useCallback,
  useRef,
} from "/assets/vendor/preact-htm.js";
import { api, errorMessage, ApiError } from "/assets/api.js";
import { useAnnounce, Dialog, ErrorNotice } from "/assets/ui.js";
import { formatTime } from "/assets/format.js";
import { SessionPage } from "/assets/views/session.js";
import { NewSessionPage } from "/assets/views/new-session.js";

/* The rev 1 route target stays exported so older callers keep working; the
   view itself now lives in new-session.js. */
export { NewSessionPage };

/* REQ-WEB-037 and Q-023: the filter offers three channels. Library sessions
   stay in the unfiltered list but have no facet chip. */
const CHANNEL_FACETS = [
  { value: "web", label: "Web" },
  { value: "cli", label: "CLI" },
  { value: "telegram", label: "Telegram" },
];

const STATUS_FACETS = [
  { value: "running", label: "Running" },
  { value: "waiting_approval", label: "Waiting approval" },
  { value: "waiting_input", label: "Waiting for you" },
  { value: "idle", label: "Idle" },
  { value: "failed", label: "Failed" },
];

const PAGE_SIZE = 50;
const SEARCH_DELAY_MS = 250;

function Icon({ name }) {
  return html`<img
    class="icon-20"
    src=${"/assets/icons/" + name + ".svg"}
    alt=""
    aria-hidden="true"
  />`;
}

function emptyFilters() {
  return { channel: "", statuses: [], agent: "" };
}

/* Returns a copy with statuses in the canonical facet order so query
   parameters and chips stay stable. */
function copyFilters(filters) {
  const source = filters || emptyFilters();
  return {
    channel: source.channel || "",
    statuses: STATUS_FACETS.map((facet) => facet.value).filter((value) =>
      source.statuses.includes(value)
    ),
    agent: source.agent || "",
  };
}

function channelLabel(value) {
  switch (value) {
    case "web":
      return "Web";
    case "cli":
      return "CLI";
    case "telegram":
      return "Telegram";
    case "library":
      return "Library";
    default:
      return value || "Unknown";
  }
}

function statusLabel(value) {
  for (const facet of STATUS_FACETS) {
    if (facet.value === value) {
      return facet.label;
    }
  }
  return value ? value : "Unknown";
}

function normalizeFacets(raw) {
  const source = raw && typeof raw === "object" ? raw : {};
  const asMap = (value) => (value && typeof value === "object" ? value : {});
  /* API-TEAM-005 lists facets.agent as [{name, kind, count}]. The older shape
     was a name -> count map; accept both so agents and teams always appear. */
  const agent = {};
  const agentKinds = {};
  const rawAgent = source.agent;
  if (Array.isArray(rawAgent)) {
    for (const entry of rawAgent) {
      if (!entry || !entry.name) {
        continue;
      }
      const name = String(entry.name);
      agent[name] = Number(entry.count) || 0;
      agentKinds[name] = entry.kind === "team" ? "team" : "agent";
    }
  } else if (rawAgent && typeof rawAgent === "object") {
    for (const [name, count] of Object.entries(rawAgent)) {
      if (!name) {
        continue;
      }
      agent[name] = Number(count) || 0;
      agentKinds[name] = "agent";
    }
  }
  return {
    channel: asMap(source.channel),
    status: asMap(source.status),
    agent,
    agentKinds,
  };
}

function facetCount(map, key) {
  const count = map ? Number(map[key]) : 0;
  return Number.isFinite(count) ? count : 0;
}

function agentFacetLabel(name, kind, count) {
  return name + (kind === "team" ? " · Team" : "") + " (" + count + ")";
}

function filterChips(filters) {
  const chips = [];
  if (filters.channel) {
    chips.push({
      key: "channel:" + filters.channel,
      kind: "channel",
      value: filters.channel,
      label: "Channel: " + channelLabel(filters.channel),
    });
  }
  for (const facet of STATUS_FACETS) {
    if (filters.statuses.includes(facet.value)) {
      chips.push({
        key: "status:" + facet.value,
        kind: "status",
        value: facet.value,
        label: "Status: " + facet.label,
      });
    }
  }
  if (filters.agent) {
    chips.push({
      key: "agent:" + filters.agent,
      kind: "agent",
      value: filters.agent,
      label: "Agent: " + filters.agent,
    });
  }
  return chips;
}

/* Relative times follow the list mockup: minutes, hours, Yesterday, days. */
export function relativeTime(ms) {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) {
    return "";
  }
  const now = Date.now();
  const diff = Math.max(0, now - value);
  const minutes = Math.floor(diff / 60000);
  if (minutes < 1) {
    return "Now";
  }
  if (minutes < 60) {
    return minutes + " min";
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return hours + " h";
  }
  const then = new Date(value);
  const today = new Date(now);
  const startToday = new Date(
    today.getFullYear(),
    today.getMonth(),
    today.getDate()
  ).getTime();
  const startThen = new Date(
    then.getFullYear(),
    then.getMonth(),
    then.getDate()
  ).getTime();
  const days = Math.round((startToday - startThen) / 86400000);
  if (days <= 1) {
    return "Yesterday";
  }
  if (days < 7) {
    return days + " d";
  }
  return then.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function previewButtonLabel(preview) {
  if (!preview) {
    return "Show sessions";
  }
  return "Show " + preview.count + (preview.more ? "+" : "") + " sessions";
}

function FacetOption({ label, count, checked, onChange }) {
  return html`<label class="facet-option">
    <input type="checkbox" checked=${checked} onChange=${onChange} />
    <span class="facet-option-label">${label}</span>
    <span class="facet-option-count">${count}</span>
  </label>`;
}

function SessionItem({ session, selected, tabIndex }) {
  const status = String(session.status || "idle");
  const channel = String(session.channel || "");
  const title = session.title || "Untitled session";
  const agent = session.agent || "unknown";
  const preview =
    session.preview || (status === "idle" ? "No messages yet." : statusLabel(status));
  const showDot = status !== "idle";
  const holder =
    session.in_use === "other" && session.holder_pid
      ? "PID " + session.holder_pid
      : "";
  return html`<li class=${"session-item" + (selected ? " is-selected" : "")}>
    <a
      class="session-item-link"
      href=${"#/sessions/" + encodeURIComponent(session.id)}
      title=${formatTime(session.updated_at)}
      aria-current=${selected ? "page" : undefined}
      tabIndex=${tabIndex}
      data-session-row
    >
      <span class="session-item-top">
        ${showDot
          ? html`<span
              class=${"session-dot status-" + status}
              aria-hidden="true"
            ></span>`
          : null}
        <span class="session-item-title">${title}</span>
        <span class="session-item-time">${relativeTime(session.updated_at)}</span>
      </span>
      <span class="session-item-preview">${preview}</span>
      <span class="session-item-meta">
        <span class=${"badge-ui" + (channel === "telegram" ? " badge-ui-brand" : "")}>
          <span class="badge-ui-dot" aria-hidden="true"></span>
          ${channelLabel(channel)}
        </span>
        <span class="session-item-agent">${"· " + agent}</span>
        ${holder
          ? html`<span class="session-item-holder">${holder}</span>`
          : null}
      </span>
    </a>
  </li>`;
}

function SessionsEmptyPane() {
  return html`<main id="main" class="sessions-empty-pane">
    <div class="sessions-empty-icon">
      <${Icon} name="sessions" />
    </div>
    <h2>Pick a session</h2>
    <p>Choose a session from the list to read it, or start a new one.</p>
    <a class="button-ui button-ui-secondary" href="#/sessions/new">New session</a>
  </main>`;
}

/* The three-pane workspace of the sessions screens: sidebar (shell) |
   session list | chat or new session. */
export function SessionsWorkspace({ selectedId = "", newSession = false }) {
  return html`<div class="sessions-workspace">
    <${SessionsPage} selectedId=${selectedId} />
    <div class="sessions-chat-pane">
      ${newSession
        ? html`<${NewSessionPage} />`
        : selectedId
          ? html`<${SessionPage} key=${selectedId} id=${selectedId} />`
          : html`<${SessionsEmptyPane} />`}
    </div>
  </div>`;
}

export function SessionsPage({ selectedId = "" }) {
  const announce = useAnnounce();
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [applied, setApplied] = useState(emptyFilters);
  const [draft, setDraft] = useState(emptyFilters);
  const [filterOpen, setFilterOpen] = useState(false);
  const [sessions, setSessions] = useState([]);
  const [facets, setFacets] = useState(() => ({
    channel: {},
    status: {},
    agent: {},
    agentKinds: {},
  }));
  const [nextBefore, setNextBefore] = useState(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState([]);
  const [reloadToken, setReloadToken] = useState(0);
  const [preview, setPreview] = useState(null);
  const filterButtonRef = useRef(null);

  /* Debounce the search box so typing "disk" issues one list request. */
  useEffect(() => {
    const timer = setTimeout(() => setQuery(search.trim()), SEARCH_DELAY_MS);
    return () => clearTimeout(timer);
  }, [search]);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    setDetails([]);
    setSessions([]);
    setNextBefore(null);
    api
      .listSessions({
        q: query,
        channel: applied.channel,
        status: applied.statuses.join(","),
        agent: applied.agent,
        limit: PAGE_SIZE,
      })
      .then((page) => {
        if (!alive) {
          return;
        }
        setSessions(page.sessions || []);
        setFacets(normalizeFacets(page.facets));
        setNextBefore(
          page.next_before === undefined ? null : page.next_before
        );
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        setError(errorMessage(failure));
        setDetails(failure instanceof ApiError ? failure.details : []);
        announce("Could not load sessions. " + errorMessage(failure));
      })
      .finally(() => {
        if (alive) {
          setLoading(false);
        }
      });
    return () => {
      alive = false;
    };
  }, [query, applied, reloadToken, announce]);

  /* The "Show N sessions" label previews the pending filters with one list
     call. Facet counts still come from the q-only list above. */
  useEffect(() => {
    if (!filterOpen) {
      return undefined;
    }
    let alive = true;
    api
      .listSessions({
        q: query,
        channel: draft.channel,
        status: draft.statuses.join(","),
        agent: draft.agent,
        limit: PAGE_SIZE,
      })
      .then((page) => {
        if (!alive) {
          return;
        }
        setPreview({
          count: (page.sessions || []).length,
          more:
            page.next_before !== undefined && page.next_before !== null,
        });
      })
      .catch(() => {
        if (alive) {
          setPreview(null);
        }
      });
    return () => {
      alive = false;
    };
  }, [filterOpen, query, draft]);

  const loadMore = useCallback(async () => {
    if (loadingMore || nextBefore === null || nextBefore === undefined) {
      return;
    }
    setLoadingMore(true);
    setError("");
    setDetails([]);
    try {
      const page = await api.listSessions({
        q: query,
        channel: applied.channel,
        status: applied.statuses.join(","),
        agent: applied.agent,
        limit: PAGE_SIZE,
        before: nextBefore,
      });
      setSessions((current) => current.concat(page.sessions || []));
      setNextBefore(page.next_before === undefined ? null : page.next_before);
    } catch (failure) {
      setError(errorMessage(failure));
      setDetails(failure instanceof ApiError ? failure.details : []);
      announce("Could not load the next page. " + errorMessage(failure));
    } finally {
      setLoadingMore(false);
    }
  }, [announce, applied, loadingMore, nextBefore, query]);

  const visibleFilters = filterOpen ? draft : applied;
  const chips = filterChips(visibleFilters);
  const hasMore = nextBefore !== null && nextBefore !== undefined;
  const countText = String(sessions.length) + (hasMore ? "+" : "");
  const agentNames = Object.keys(facets.agent || {}).sort((left, right) =>
    left.localeCompare(right)
  );
  if (draft.agent && !agentNames.includes(draft.agent)) {
    agentNames.unshift(draft.agent);
  }

  function openFilter() {
    setDraft(copyFilters(applied));
    setPreview({ count: sessions.length, more: hasMore });
    setFilterOpen(true);
  }

  function refocusFilterButton() {
    window.setTimeout(() => {
      if (filterButtonRef.current) {
        filterButtonRef.current.focus();
      }
    }, 0);
  }

  function closeFilter() {
    setFilterOpen(false);
    refocusFilterButton();
  }

  function applyFilter() {
    setApplied(copyFilters(draft));
    setFilterOpen(false);
    const count = filterChips(copyFilters(draft)).length;
    announce(
      count === 0
        ? "Filters cleared."
        : "Filters applied. " + count + (count === 1 ? " filter." : " filters.")
    );
    refocusFilterButton();
  }

  function toggleStatus(value) {
    setDraft((current) => {
      const selected = current.statuses.includes(value);
      return {
        ...current,
        statuses: selected
          ? current.statuses.filter((status) => status !== value)
          : current.statuses.concat([value]),
      };
    });
  }

  function removeFilter(kind, value) {
    const update = (current) => {
      if (kind === "channel") {
        return { ...current, channel: "" };
      }
      if (kind === "agent") {
        return { ...current, agent: "" };
      }
      return {
        ...current,
        statuses: current.statuses.filter((status) => status !== value),
      };
    };
    if (filterOpen) {
      setDraft(update);
    } else {
      setApplied(update);
    }
  }

  function clearAll() {
    if (filterOpen) {
      setDraft(emptyFilters());
      return;
    }
    setApplied(emptyFilters());
    announce("Filters cleared.");
  }

  function handleItemsKeyDown(event) {
    if (
      event.key !== "ArrowDown" &&
      event.key !== "ArrowUp" &&
      event.key !== "Home" &&
      event.key !== "End"
    ) {
      return;
    }
    const rows = Array.from(
      event.currentTarget.querySelectorAll("[data-session-row]")
    );
    if (rows.length === 0) {
      return;
    }
    event.preventDefault();
    const index = rows.indexOf(document.activeElement);
    let next = index < 0 ? 0 : index;
    if (event.key === "ArrowDown") {
      next = index < 0 ? 0 : Math.min(index + 1, rows.length - 1);
    } else if (event.key === "ArrowUp") {
      next = index < 0 ? 0 : Math.max(index - 1, 0);
    } else if (event.key === "Home") {
      next = 0;
    } else if (event.key === "End") {
      next = rows.length - 1;
    }
    rows[next].focus();
  }

  return html`<section class="sessions-pane" aria-labelledby="sessions-title">
    <header class="sessions-header">
      <div class="sessions-title-row">
        <div class="sessions-title-group">
          <h1 class="sessions-title" id="sessions-title">Sessions</h1>
          <span
            class="badge-ui badge-ui-count"
            title=${"Showing " + sessions.length + " sessions"}
          >
            <span class="badge-ui-dot" aria-hidden="true"></span>
            ${countText}
          </span>
        </div>
        <a class="button-ui button-ui-secondary" href="#/sessions/new">+ New</a>
      </div>

      <div class="sessions-search">
        <span class="sessions-search-icon"><${Icon} name="search" /></span>
        <input
          type="search"
          class="sessions-search-input"
          aria-label="Search sessions"
          placeholder="Search sessions"
          autocomplete="off"
          value=${search}
          onInput=${(event) => setSearch(event.target.value)}
        />
        <button
          ref=${filterButtonRef}
          type="button"
          class=${"sessions-filter-button" + (chips.length > 0 ? " has-filters" : "")}
          aria-label=${"Filter sessions" + (chips.length > 0 ? ", " + chips.length + " active" : "")}
          aria-haspopup="dialog"
          aria-expanded=${filterOpen}
          onClick=${openFilter}
        >
          <${Icon} name="filter" />
          ${chips.length > 0
            ? html`<span class="sessions-filter-count">${chips.length}</span>`
            : null}
        </button>
      </div>

      ${chips.length > 0
        ? html`<div class="sessions-active-filters">
            ${chips.map(
              (chip) => html`<button
                type="button"
                class="filter-chip"
                key=${chip.key}
                aria-label=${"Remove filter " + chip.label}
                onClick=${() => removeFilter(chip.kind, chip.value)}
              >
                <span>${chip.label}</span>
                <span class="filter-chip-x" aria-hidden="true">×</span>
              </button>`
            )}
            <button type="button" class="filter-clear" onClick=${clearAll}>
              Clear all
            </button>
          </div>`
        : null}
    </header>

    <div class="sessions-items-scroll">
      ${error
        ? html`<div class="sessions-error">
            <${ErrorNotice} message=${error} details=${details} />
            <button
              type="button"
              class="button-ui button-ui-secondary"
              onClick=${() => setReloadToken((value) => value + 1)}
            >
              Try again
            </button>
          </div>`
        : null}

      ${loading
        ? html`<p class="sessions-state" role="status">Loading sessions.</p>`
        : null}

      ${!loading && !error && sessions.length === 0
        ? html`<div class="sessions-state sessions-empty">
            <p class="sessions-empty-title">No sessions found</p>
            <p class="sessions-empty-text">
              ${query || chips.length > 0
                ? "Try a different search or clear the filters."
                : "Start a session to see it here."}
            </p>
            ${chips.length > 0
              ? html`<button
                  type="button"
                  class="button-ui button-ui-secondary"
                  onClick=${clearAll}
                >
                  Clear filters
                </button>`
              : html`<a class="button-ui button-ui-secondary" href="#/sessions/new">
                  New session
                </a>`}
          </div>`
        : null}

      ${!loading && sessions.length > 0
        ? html`<ul
            class="sessions-items"
            aria-labelledby="sessions-title"
            onKeyDown=${handleItemsKeyDown}
          >
            ${sessions.map((session, index) =>
              html`<${SessionItem}
                key=${session.id}
                session=${session}
                selected=${session.id === selectedId}
                tabIndex=${selectedId
                  ? session.id === selectedId
                    ? 0
                    : -1
                  : index === 0
                    ? 0
                    : -1}
              />`
            )}
          </ul>`
        : null}
    </div>

    ${hasMore
      ? html`<div class="sessions-load-more">
          <button
            type="button"
            class="button-ui button-ui-secondary"
            disabled=${loading || loadingMore}
            onClick=${loadMore}
          >
            ${loadingMore ? "Loading…" : "Load more"}
          </button>
        </div>`
      : null}

    ${filterOpen
      ? html`<${Dialog}
          className="dialog-filter"
          title="Filter sessions"
          onClose=${closeFilter}
          children=${html`
            <div class="filter-sections">
              <section class="filter-section" aria-labelledby="filter-channel-title">
                <h3 class="filter-section-title" id="filter-channel-title">CHANNEL</h3>
                ${CHANNEL_FACETS.map(
                  (facet) => html`<${FacetOption}
                    key=${facet.value}
                    label=${facet.label}
                    count=${facetCount(facets.channel, facet.value)}
                    checked=${draft.channel === facet.value}
                    onChange=${() =>
                      setDraft((current) => ({
                        ...current,
                        channel:
                          current.channel === facet.value ? "" : facet.value,
                      }))}
                  />`
                )}
              </section>

              <section class="filter-section" aria-labelledby="filter-status-title">
                <h3 class="filter-section-title" id="filter-status-title">STATUS</h3>
                ${STATUS_FACETS.map(
                  (facet) => html`<${FacetOption}
                    key=${facet.value}
                    label=${facet.label}
                    count=${facetCount(facets.status, facet.value)}
                    checked=${draft.statuses.includes(facet.value)}
                    onChange=${() => toggleStatus(facet.value)}
                  />`
                )}
              </section>

              <section class="filter-section" aria-labelledby="filter-agent-title">
                <h3 class="filter-section-title" id="filter-agent-title">AGENT</h3>
                <select
                  class="facet-select"
                  aria-label="Agent or team"
                  value=${draft.agent}
                  onChange=${(event) =>
                    setDraft((current) => ({
                      ...current,
                      agent: event.target.value,
                    }))}
                >
                  <option value="">Any agent or team</option>
                  ${agentNames.map(
                    (name) => html`<option key=${name} value=${name}>
                      ${agentFacetLabel(
                        name,
                        facets.agentKinds[name],
                        facetCount(facets.agent, name)
                      )}
                    </option>`
                  )}
                </select>
              </section>
            </div>

            <div class="filter-footer">
              <button
                type="button"
                class="filter-clear"
                onClick=${() => setDraft(emptyFilters())}
              >
                Clear all
              </button>
              <button
                type="button"
                class="button-ui button-ui-primary"
                onClick=${applyFilter}
              >
                ${previewButtonLabel(preview)}
              </button>
            </div>
          `}
        />`
      : null}
  </section>`;
}

