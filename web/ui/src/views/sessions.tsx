import { Ic } from "../ui/icons";
import { useCallback, useEffect, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import { listSessions } from "../api/sessions";
import type { ListSessionsResponse, Session } from "../api/sessions";
import { formatTime } from "../lib/format";
import {
  Badge,
  Button,
  Checkbox,
  Chip,
  IconButton,
  Lbl,
  SearchInput,
  Select,
} from "../ui/gr8r";
import { Dialog, ErrorNotice, useAnnounce } from "../ui/ui";
import { NewSessionPage } from "./new-session";
import { SessionPage } from "./session";

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

interface Filters {
  channel: string;
  statuses: string[];
  agent: string;
}

interface NormalizedFacets {
  channel: Record<string, unknown>;
  status: Record<string, unknown>;
  agent: Record<string, number>;
  agentKinds: Record<string, string>;
}

interface FilterChip {
  key: string;
  kind: string;
  value: string;
  label: string;
}

interface Preview {
  count: number;
  more: boolean;
}

function Icon({ name }: { name: string }) {
  return (
    <img
      className="icon-20"
      src={"/assets/icons/" + name + ".svg"}
      alt=""
      aria-hidden="true"
    />
  );
}

function emptyFilters(): Filters {
  return { channel: "", statuses: [], agent: "" };
}

/* Returns a copy with statuses in the canonical facet order so query
   parameters and chips stay stable. */
function copyFilters(filters: Filters | null | undefined): Filters {
  const source = filters || emptyFilters();
  return {
    channel: source.channel || "",
    statuses: STATUS_FACETS.map((facet) => facet.value).filter((value) =>
      source.statuses.includes(value)
    ),
    agent: source.agent || "",
  };
}

/* The channel chip colour, using Gr8r tokens. */
const CHANNEL_COLORS: Record<string, string> = {
  web: "var(--accent)",
  cli: "var(--teal)",
  telegram: "var(--blue)",
  library: "var(--gray)",
};

function channelLabel(value: string): string {
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

function statusLabel(value: string): string {
  for (const facet of STATUS_FACETS) {
    if (facet.value === value) {
      return facet.label;
    }
  }
  return value ? value : "Unknown";
}

function normalizeFacets(raw: unknown): NormalizedFacets {
  const source =
    raw && typeof raw === "object" ? (raw as Record<string, unknown>) : {};
  const asMap = (value: unknown): Record<string, unknown> =>
    value && typeof value === "object"
      ? (value as Record<string, unknown>)
      : {};
  /* API-TEAM-005 lists facets.agent as [{name, kind, count}]. The older shape
     was a name -> count map; accept both so agents and teams always appear. */
  const agent: Record<string, number> = {};
  const agentKinds: Record<string, string> = {};
  const rawAgent = source.agent;
  if (Array.isArray(rawAgent)) {
    for (const entry of rawAgent) {
      if (!entry || typeof entry !== "object") {
        continue;
      }
      const row = entry as Record<string, unknown>;
      if (!row.name) {
        continue;
      }
      const name = String(row.name);
      agent[name] = Number(row.count) || 0;
      agentKinds[name] = row.kind === "team" ? "team" : "agent";
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

function facetCount(
  map: Record<string, unknown> | null | undefined,
  key: string
): number {
  const count = map ? Number(map[key]) : 0;
  return Number.isFinite(count) ? count : 0;
}

function agentFacetLabel(
  name: string,
  kind: string | undefined,
  count: number
): string {
  return name + (kind === "team" ? " · Team" : "") + " (" + count + ")";
}

function filterChips(filters: Filters): FilterChip[] {
  const chips: FilterChip[] = [];
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
export function relativeTime(ms: unknown): string {
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

function previewButtonLabel(preview: Preview | null): string {
  if (!preview) {
    return "Show sessions";
  }
  return "Show " + preview.count + (preview.more ? "+" : "") + " sessions";
}

function FacetOption({
  label,
  count,
  checked,
  onChange,
}: {
  label: string;
  count: number;
  checked: boolean;
  onChange: () => void;
}) {
  return (
    <label className="facet-option">
      <Checkbox checked={checked} onChange={onChange} />
      <span className="facet-option-label">{label}</span>
      <span className="facet-option-count">{count}</span>
    </label>
  );
}

function SessionItem({
  session,
  selected,
  tabIndex,
}: {
  session: Session;
  selected: boolean;
  tabIndex: number;
}) {
  const status = String(session.status || "idle");
  const channel = String(session.channel || "");
  const title = session.title || "Untitled session";
  const agent = session.agent || "unknown";
  const preview =
    session.preview ||
    (status === "idle" ? "No messages yet." : statusLabel(status));
  const showDot = status !== "idle";
  const holder =
    session.in_use === "other" && session.holder_pid
      ? "PID " + session.holder_pid
      : "";
  return (
    <li className={"session-row" + (selected ? " is-selected" : "")}>
      <a
        className="session-row-link"
        href={"#/sessions/" + encodeURIComponent(session.id)}
        title={formatTime(session.updated_at)}
        aria-current={selected ? "page" : undefined}
        tabIndex={tabIndex}
        data-session-row
      >
        <span className="session-row-head">
          <span className="session-row-title trunc">{title}</span>
          <span className="session-row-time num">
            {relativeTime(session.updated_at)}
          </span>
        </span>
        <span className="session-row-meta">
          {showDot ? (
            <span
              className={"session-row-dot status-" + status}
              aria-hidden="true"
            />
          ) : null}
          <span className="session-row-agent trunc">{agent}</span>
          <Lbl color={CHANNEL_COLORS[channel] || "var(--gray)"}>
            {channelLabel(channel)}
          </Lbl>
          {holder ? (
            <span className="session-row-holder">{holder}</span>
          ) : null}
        </span>
        <span className="session-row-preview trunc">{preview}</span>
      </a>
    </li>
  );
}

function SessionsEmptyPane() {
  return (
    <main id="main" className="sessions-empty-pane">
      <div className="sessions-empty-icon">
        <Icon name="sessions" />
      </div>
      <h2>Pick a session</h2>
      <p>Choose a session from the list to read it, or start a new one.</p>
      <a className="button-ui button-ui-secondary" href="#/sessions/new">
        New session
      </a>
    </main>
  );
}

export interface SessionsWorkspaceProps {
  selectedId?: string;
  newSession?: boolean;
}

/* The three-pane workspace of the sessions screens: sidebar (shell) |
   session list | chat or new session. */
export function SessionsWorkspace({
  selectedId = "",
  newSession = false,
}: SessionsWorkspaceProps) {
  return (
    <div className={"sessions-workspace" + (newSession || selectedId ? " has-detail" : "")}>
      <SessionsPage selectedId={selectedId} />
      <div className="sessions-chat-pane">
        {/* Phones show the list and the session as separate screens. */}
        <a className="sessions-back" href="#/sessions">
          <Ic name="chevron-left" size={16} />
          Sessions
        </a>
        {newSession ? (
          <NewSessionPage />
        ) : selectedId ? (
          <SessionPage key={selectedId} id={selectedId} />
        ) : (
          <SessionsEmptyPane />
        )}
      </div>
    </div>
  );
}

export interface SessionsPageProps {
  selectedId?: string;
}

export function SessionsPage({ selectedId = "" }: SessionsPageProps) {
  const announce = useAnnounce();
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [applied, setApplied] = useState<Filters>(emptyFilters);
  const [draft, setDraft] = useState<Filters>(emptyFilters);
  const [filterOpen, setFilterOpen] = useState(false);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [facets, setFacets] = useState<NormalizedFacets>(() => ({
    channel: {},
    status: {},
    agent: {},
    agentKinds: {},
  }));
  const [nextBefore, setNextBefore] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [reloadToken, setReloadToken] = useState(0);
  const [preview, setPreview] = useState<Preview | null>(null);

  /* Debounce the search box so typing "disk" issues one list request. */
  useEffect(() => {
    const timer = setTimeout(() => setQuery(search.trim()), SEARCH_DELAY_MS);
    return () => clearTimeout(timer);
  }, [search]);

  /* eslint-disable react-hooks/set-state-in-effect -- the list reset belongs to the request cycle, exactly as in sessions.js */
  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    setDetails([]);
    setSessions([]);
    setNextBefore(null);
    listSessions({
      q: query,
      channel: applied.channel,
      status: applied.statuses.join(","),
      agent: applied.agent,
      limit: PAGE_SIZE,
    })
      .then((page: ListSessionsResponse) => {
        if (!alive) {
          return;
        }
        setSessions(page.sessions || []);
        setFacets(normalizeFacets(page.facets));
        setNextBefore(page.next_before ?? null);
      })
      .catch((failure: unknown) => {
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
  /* eslint-enable react-hooks/set-state-in-effect */

  /* The "Show N sessions" label previews the pending filters with one list
     call. Facet counts still come from the q-only list above. */
  useEffect(() => {
    if (!filterOpen) {
      return undefined;
    }
    let alive = true;
    listSessions({
      q: query,
      channel: draft.channel,
      status: draft.statuses.join(","),
      agent: draft.agent,
      limit: PAGE_SIZE,
    })
      .then((page: ListSessionsResponse) => {
        if (!alive) {
          return;
        }
        setPreview({
          count: (page.sessions || []).length,
          more: page.next_before !== null,
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
      const page = await listSessions({
        q: query,
        channel: applied.channel,
        status: applied.statuses.join(","),
        agent: applied.agent,
        limit: PAGE_SIZE,
        before: nextBefore,
      });
      setSessions((current) => current.concat(page.sessions || []));
      setNextBefore(page.next_before ?? null);
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
  const hasMore = nextBefore !== null;
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

  function closeFilter() {
    setFilterOpen(false);
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
  }

  function toggleStatus(value: string) {
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

  function removeFilter(kind: string, value: string) {
    const update = (current: Filters): Filters => {
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

  function handleItemsKeyDown(event: ReactKeyboardEvent<HTMLUListElement>) {
    if (
      event.key !== "ArrowDown" &&
      event.key !== "ArrowUp" &&
      event.key !== "Home" &&
      event.key !== "End"
    ) {
      return;
    }
    const rows = Array.from(
      event.currentTarget.querySelectorAll<HTMLAnchorElement>(
        "[data-session-row]"
      )
    );
    if (rows.length === 0) {
      return;
    }
    event.preventDefault();
    const active = document.activeElement;
    const index = active ? rows.indexOf(active as HTMLAnchorElement) : -1;
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
    rows[next]?.focus();
  }

  return (
    <section className="sessions-pane" aria-labelledby="sessions-title">
      <header className="sessions-header">
        <div className="sessions-title-row">
          <div className="sessions-title-group">
            <h1 className="sessions-title" id="sessions-title">
              Sessions
            </h1>
            <Badge title={"Showing " + sessions.length + " sessions"}>
              {countText}
            </Badge>
          </div>
          <Button
            as="a"
            href="#/sessions/new"
            variant="secondary"
            size="sm"
            icon="plus"
          >
            New
          </Button>
        </div>

        <div className="sessions-toolbar">
          <SearchInput
            wrapClassName="sessions-search"
            kbd={null}
            type="search"
            aria-label="Search sessions"
            placeholder="Search sessions"
            autoComplete="off"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          <div className="sessions-filter">
            <IconButton
              icon="list-filter"
              size="sm"
              label={
                "Filter sessions" +
                (chips.length > 0 ? ", " + chips.length + " active" : "")
              }
              aria-haspopup="dialog"
              aria-expanded={filterOpen}
              on={chips.length > 0}
              onClick={openFilter}
            />
            {chips.length > 0 ? (
              <Badge tone="accent" className="sessions-filter-count">
                {chips.length}
              </Badge>
            ) : null}
          </div>
        </div>

        {chips.length > 0 ? (
          <div className="sessions-chips">
            {chips.map((chip) => (
              <Chip
                key={chip.key}
                onRemove={() => removeFilter(chip.kind, chip.value)}
                removeLabel={"Remove filter " + chip.label}
              >
                {chip.label}
              </Chip>
            ))}
            <Button variant="ghost" size="sm" onClick={clearAll}>
              Clear all
            </Button>
          </div>
        ) : null}
      </header>

      <div className="sessions-items-scroll">
        {error ? (
          <div className="sessions-error">
            <ErrorNotice message={error} details={details} />
            <Button
              variant="secondary"
              onClick={() => setReloadToken((value) => value + 1)}
            >
              Try again
            </Button>
          </div>
        ) : null}

        {loading ? (
          <p className="sessions-state" role="status">
            Loading sessions.
          </p>
        ) : null}

        {!loading && !error && sessions.length === 0 ? (
          <div className="sessions-state sessions-empty">
            <p className="sessions-empty-title">No sessions found</p>
            <p className="sessions-empty-text">
              {query || chips.length > 0
                ? "Try a different search or clear the filters."
                : "Start a session to see it here."}
            </p>
            {chips.length > 0 ? (
              <Button variant="secondary" onClick={clearAll}>
                Clear filters
              </Button>
            ) : (
              <Button as="a" href="#/sessions/new" variant="secondary">
                New session
              </Button>
            )}
          </div>
        ) : null}

        {!loading && sessions.length > 0 ? (
          <ul
            className="sessions-items"
            aria-labelledby="sessions-title"
            onKeyDown={handleItemsKeyDown}
          >
            {sessions.map((session, index) => (
              <SessionItem
                key={session.id}
                session={session}
                selected={session.id === selectedId}
                tabIndex={
                  selectedId
                    ? session.id === selectedId
                      ? 0
                      : -1
                    : index === 0
                      ? 0
                      : -1
                }
              />
            ))}
          </ul>
        ) : null}
      </div>

      {hasMore ? (
        <div className="sessions-load-more">
          <Button
            variant="secondary"
            block
            disabled={loading || loadingMore}
            onClick={() => void loadMore()}
          >
            {loadingMore ? "Loading…" : "Load more"}
          </Button>
        </div>
      ) : null}

      {filterOpen ? (
        <Dialog
          className="dialog-filter"
          title="Filter sessions"
          onClose={closeFilter}
        >
          <div className="filter-sections">
            <section
              className="filter-section"
              aria-labelledby="filter-channel-title"
            >
              <h3 className="filter-section-title" id="filter-channel-title">
                Channel
              </h3>
              {CHANNEL_FACETS.map((facet) => (
                <FacetOption
                  key={facet.value}
                  label={facet.label}
                  count={facetCount(facets.channel, facet.value)}
                  checked={draft.channel === facet.value}
                  onChange={() =>
                    setDraft((current) => ({
                      ...current,
                      channel:
                        current.channel === facet.value ? "" : facet.value,
                    }))
                  }
                />
              ))}
            </section>

            <section
              className="filter-section"
              aria-labelledby="filter-status-title"
            >
              <h3 className="filter-section-title" id="filter-status-title">
                Status
              </h3>
              {STATUS_FACETS.map((facet) => (
                <FacetOption
                  key={facet.value}
                  label={facet.label}
                  count={facetCount(facets.status, facet.value)}
                  checked={draft.statuses.includes(facet.value)}
                  onChange={() => toggleStatus(facet.value)}
                />
              ))}
            </section>

            <section
              className="filter-section"
              aria-labelledby="filter-agent-title"
            >
              <h3 className="filter-section-title" id="filter-agent-title">
                Agent
              </h3>
              <Select
                className="facet-select"
                aria-label="Agent or team"
                placeholder="Any agent or team"
                value={draft.agent}
                options={[
                  { value: "", label: "Any agent or team" },
                  ...agentNames.map((name) => ({
                    value: name,
                    label: agentFacetLabel(
                      name,
                      facets.agentKinds[name],
                      facetCount(facets.agent, name)
                    ),
                  })),
                ]}
                onChange={(next) =>
                  setDraft((current) => ({ ...current, agent: next }))
                }
              />
            </section>
          </div>

          <div className="filter-footer">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setDraft(emptyFilters())}
            >
              Clear all
            </Button>
            <Button variant="primary" size="sm" onClick={applyFilter}>
              {previewButtonLabel(preview)}
            </Button>
          </div>
        </Dialog>
      ) : null}
    </section>
  );
}
