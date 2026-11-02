import { useEffect, useMemo, useState } from "react";
import type { RoomRow } from "../api/rooms";
import {
  AgentFace,
  PresenceLabel,
  SkeletonRows,
  TeamFace,
  UnreadBadge,
  useRooms,
} from "./presence";
import { relativeShort, RoomView } from "./room-timeline";

/* The Chats page of REQ-ROOM-001 to REQ-ROOM-005: the room list column and the
   room timeline. The list rows come from the live useRooms() store; the
   timeline lives in room-timeline.tsx. */

const FILTERS = [
  { key: "all", label: "All" },
  { key: "working", label: "Working" },
  { key: "needs", label: "Needs you" },
] as const;

type FilterKey = (typeof FILTERS)[number]["key"];

export interface ChatsPageProps {
  roomName?: string;
}

function rowState(row: RoomRow): string {
  return row.presence ? row.presence.state : "";
}

/* REQ-ROOM-001: "Needs you" is a room that is waiting or has a problem. */
function needsYou(row: RoomRow): boolean {
  const state = rowState(row);
  return state === "waiting" || state === "problem";
}

function unreadTone(row: RoomRow): string {
  const state = rowState(row);
  if (state === "waiting") {
    return "warning";
  }
  if (state === "problem") {
    return "error";
  }
  return "brand";
}

/* The status line: the presence label while busy, waiting or broken; the last
   message preview otherwise (REQ-ROOM-001). */
function rowStatus(row: RoomRow) {
  const state = rowState(row);
  if (state === "waiting" || state === "working" || state === "problem") {
    return <PresenceLabel presence={row.presence} />;
  }
  const preview = row.last_message ? row.last_message.preview : "";
  return <span className="rooms-row-preview">{preview || "No messages yet"}</span>;
}

function RowFace({ row }: { row: RoomRow }) {
  if (row.kind === "team") {
    const faces = [row.lead, ...(row.members || [])]
      .filter(Boolean)
      .map((name) => ({ name: String(name) }));
    return <TeamFace faces={faces} size={32} />;
  }
  return (
    <AgentFace
      name={row.name}
      color={row.color}
      presence={row.presence}
      size={44}
    />
  );
}

export function ChatsPage({ roomName }: ChatsPageProps) {
  const { rows, loading } = useRooms();
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<FilterKey>("all");

  const counts = useMemo(() => {
    let working = 0;
    let needs = 0;
    for (const row of rows) {
      if (rowState(row) === "working") {
        working += 1;
      }
      if (needsYou(row)) {
        needs += 1;
      }
    }
    return { all: rows.length, working, needs };
  }, [rows]);

  const visible = useMemo(() => {
    const text = query.trim().toLowerCase();
    return rows.filter((row) => {
      if (filter === "working" && rowState(row) !== "working") {
        return false;
      }
      if (filter === "needs" && !needsYou(row)) {
        return false;
      }
      if (!text) {
        return true;
      }
      const preview = row.last_message ? row.last_message.preview : "";
      return (
        row.name.toLowerCase().includes(text) ||
        (row.title || "").toLowerCase().includes(text) ||
        preview.toLowerCase().includes(text)
      );
    });
  }, [rows, query, filter]);

  const selected = roomName
    ? rows.find((row) => row.name === roomName) || null
    : null;

  /* REQ-ROOM-001: with no room in the URL, open the first room. */
  useEffect(() => {
    const first = rows[0];
    if (roomName || !first) {
      return;
    }
    window.location.hash = "#/chats/" + encodeURIComponent(first.name);
  }, [roomName, rows]);

  function countFor(key: FilterKey): number {
    if (key === "all") {
      return counts.all;
    }
    return key === "working" ? counts.working : counts.needs;
  }

  return (
    <main id="main" className="rooms-page">
      <aside className="rooms-list" aria-label="Chats">
        <header className="rooms-list-head">
          <h1 className="rooms-list-title">Chats</h1>
          <a className="button-ui button-ui-secondary" href="#/agents">
            + New agent
          </a>
        </header>
        <div className="rooms-search">
          <img
            className="icon-16 rooms-search-icon"
            src="/assets/icons/search.svg"
            alt=""
            aria-hidden="true"
          />
          <input
            type="search"
            className="rooms-search-input"
            placeholder="Search agents and messages"
            aria-label="Search agents and messages"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
        <div className="rooms-filters" role="tablist" aria-label="Room filters">
          {FILTERS.map((entry) => (
            <button
              key={entry.key}
              type="button"
              role="tab"
              aria-selected={filter === entry.key}
              className={"rooms-filter" + (filter === entry.key ? " is-active" : "")}
              onClick={() => setFilter(entry.key)}
            >
              {entry.label + " " + countFor(entry.key)}
            </button>
          ))}
        </div>
        <div className="rooms-list-body">
          {loading && rows.length === 0 ? (
            <SkeletonRows count={5} />
          ) : rows.length === 0 ? (
            <div className="rooms-empty">
              <p className="rooms-empty-title">No chats yet</p>
              <p className="muted">Create an agent to start a chat.</p>
              <a className="button-ui button-ui-secondary" href="#/agents">
                + New agent
              </a>
            </div>
          ) : visible.length === 0 ? (
            <p className="muted rooms-list-note">No chats match.</p>
          ) : (
            <ul className="rooms-rows">
              {visible.map((row) => {
                const active = row.name === roomName;
                const at = row.last_message ? row.last_message.at : row.last_active_at;
                return (
                  <li key={row.name}>
                    <a
                      className={"rooms-row" + (active ? " is-selected" : "")}
                      href={"#/chats/" + encodeURIComponent(row.name)}
                      aria-current={active ? "page" : undefined}
                    >
                      <RowFace row={row} />
                      <span className="rooms-row-body">
                        <span className="rooms-row-top">
                          <span className="rooms-row-name">{row.name}</span>
                          <span className="rooms-row-time">{relativeShort(at)}</span>
                        </span>
                        <span className="rooms-row-title">
                          {row.title || (row.kind === "team" ? "Team" : "Agent")}
                        </span>
                        <span className="rooms-row-line">{rowStatus(row)}</span>
                      </span>
                      <span className="rooms-row-badge">
                        <UnreadBadge count={row.unread} tone={unreadTone(row)} />
                      </span>
                    </a>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </aside>
      <section className="rooms-panel">
        {selected ? (
          <RoomView key={selected.name} room={selected} />
        ) : rows.length === 0 && !loading ? (
          <div className="rooms-placeholder">
            <p className="muted">Create an agent to start a chat.</p>
            <a className="button-ui button-ui-secondary" href="#/agents">
              + New agent
            </a>
          </div>
        ) : (
          <div className="rooms-placeholder">
            <SkeletonRows count={3} />
          </div>
        )}
      </section>
    </main>
  );
}
