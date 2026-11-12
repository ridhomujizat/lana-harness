import { useEffect, useMemo, useRef, useState } from "react";
import type { RoomRow } from "../api/rooms";
import { gr8rColor, PresenceLabel, useRooms } from "./presence";
import { moodFromPresence } from "../lib/avatar-motion";
import { displayLabel } from "./agent-label";
import { relativeShort, RoomView } from "./room-timeline";
import {
  Badge,
  Button,
  EmptyState,
  Menu,
  MenuItem,
  SearchInput,
  Seg,
  Skeleton,
} from "../ui/gr8r";
import { AgentAvatar } from "./agent-avatar";
import { TeamAvatar } from "./discussion";
import { NewGroupRoom } from "./new-group-room";
import type { BadgeTone } from "../ui/gr8r";

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
  /* Spec 48: prefilled composer text for the opened room. */
  draft?: string;
}

function rowState(row: RoomRow): string {
  return row.presence ? row.presence.state : "";
}

/* REQ-ROOM-001: "Needs you" is a room that is waiting or has a problem. */
function needsYou(row: RoomRow): boolean {
  const state = rowState(row);
  return state === "waiting" || state === "problem";
}

function unreadTone(row: RoomRow): BadgeTone {
  const state = rowState(row);
  if (state === "waiting") {
    return "amber";
  }
  if (state === "problem") {
    return "red";
  }
  return "accent";
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

function RowFace({ row, colors }: { row: RoomRow; colors: Record<string, string> }) {
  if (row.kind === "team") {
    /* REQ-RDS-056: two faces, three faces, or two faces and +N. */
    const items = [row.lead, ...(row.members || [])]
      .filter(Boolean)
      .map((name) => ({ name: String(name), color: gr8rColor(colors[String(name)]) }));
    return <TeamAvatar items={items} size={44} />;
  }
  const state = rowState(row);
  return (
    <AgentAvatar
      name={row.name}
      color={gr8rColor(row.color)}
      size="lg"
      presence={Boolean(row.presence)}
      mood={moodFromPresence(state)}
      className={"chat-coin" + (state ? " state-" + state : "")}
    />
  );
}

/* Matches the phone breakpoint in chats.css. */
function isPhoneWidth(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia("(max-width: 760px)").matches;
}

export function ChatsPage({ roomName, draft }: ChatsPageProps) {
  const { rows, loading } = useRooms();
  const newRef = useRef<HTMLSpanElement>(null);
  const [newOpen, setNewOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const colors = useMemo(() => {
    const out: Record<string, string> = {};
    for (const row of rows) {
      out[row.name] = row.color;
    }
    return out;
  }, [rows]);
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
        displayLabel(row).toLowerCase().includes(text) ||
        (row.title || "").toLowerCase().includes(text) ||
        preview.toLowerCase().includes(text)
      );
    });
  }, [rows, query, filter]);

  const selected = roomName
    ? rows.find((row) => row.name === roomName) || null
    : null;

  /* REQ-ROOM-001: with no room in the URL, open the first room. On a phone
     the list is its own screen, so it stays on the list. */
  useEffect(() => {
    const first = rows[0];
    if (roomName || !first || isPhoneWidth()) {
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
    <main id="main" className={"rooms-page" + (roomName ? " has-room" : "")}>
      <aside className="rooms-list" aria-label="Chats">
        <header className="rooms-list-head">
          <h1 className="rooms-list-title">Chats</h1>
          <span ref={newRef} className="rooms-new">
            <Button size="sm" icon="plus" aria-haspopup="menu" aria-expanded={newOpen} onClick={() => setNewOpen((v) => !v)}>
              New
            </Button>
          </span>
          <Menu anchorRef={newRef} open={newOpen} onClose={() => setNewOpen(false)} className="new-menu">
            <MenuItem
              icon="user-plus"
              onClick={() => {
                setNewOpen(false);
                window.location.hash = "#/agents";
              }}
            >
              New agent
            </MenuItem>
            <MenuItem
              icon="users"
              onClick={() => {
                setNewOpen(false);
                setCreating(true);
              }}
            >
              New group room
            </MenuItem>
          </Menu>
          {creating ? <NewGroupRoom rows={rows} defaultLead="lana" onClose={() => setCreating(false)} /> : null}
        </header>
        <SearchInput
          wrapClassName="rooms-search"
          placeholder="Search agents and messages"
          aria-label="Search agents and messages"
          autoComplete="off"
          spellCheck={false}
          kbd={null}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <Seg
          className="rooms-filters"
          aria-label="Room filters"
          value={filter}
          onChange={(value) => setFilter(value as FilterKey)}
          options={FILTERS.map((entry) => ({
            value: entry.key,
            label: entry.label + " ",
            count: countFor(entry.key),
          }))}
        />
        <div className="rooms-list-body">
          {loading && rows.length === 0 ? (
            <div className="rooms-skeleton" aria-hidden="true">
              {Array.from({ length: 5 }).map((_, index) => (
                <div className="rooms-skeleton-row" key={index}>
                  <Skeleton width={36} height={36} radius="50%" />
                  <div className="rooms-skeleton-lines">
                    <Skeleton width="60%" height={10} />
                    <Skeleton width="90%" height={10} />
                  </div>
                </div>
              ))}
            </div>
          ) : rows.length === 0 ? (
            <EmptyState
              size="sm"
              icon="message-square"
              title="No chats yet"
              text="Create an agent to start a chat."
              action={
                <Button as="a" href="#/agents" variant="secondary" icon="plus">
                  New agent
                </Button>
              }
            />
          ) : visible.length === 0 ? (
            <EmptyState
              size="sm"
              icon="search-x"
              title="No chats match"
              text="Try a different search or filter."
            />
          ) : (
            <ul className="rooms-rows">
              {visible.map((row) => {
                const active = row.name === roomName;
                const at = row.last_message ? row.last_message.at : row.last_active_at;
                return (
                  <li key={row.name}>
                    <a
                      className={"rooms-row" + (active ? " on" : "")}
                      href={"#/chats/" + encodeURIComponent(row.name)}
                      aria-current={active ? "page" : undefined}
                    >
                      <RowFace row={row} colors={colors} />
                      <span className="rooms-row-body">
                        <span className="rooms-row-top">
                          <span className="rooms-row-name">{displayLabel(row)}</span>
                          <span className="rooms-row-time">{relativeShort(at)}</span>
                        </span>
                        <span className="rooms-row-title">
                          {row.title || (row.kind === "team" ? "Team" : "Agent")}
                        </span>
                        <span className="rooms-row-line">{rowStatus(row)}</span>
                      </span>
                      {row.unread > 0 ? (
                        <Badge
                          className="rooms-row-badge"
                          tone={unreadTone(row)}
                          aria-label={row.unread + " unread"}
                        >
                          {row.unread > 99 ? "99+" : row.unread}
                        </Badge>
                      ) : null}
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
          <RoomView key={selected.name} room={selected} initialDraft={draft} />
        ) : rows.length === 0 && !loading ? (
          <div className="rooms-placeholder">
            <EmptyState
              icon="message-square"
              title="No chats yet"
              text="Create an agent to start a chat."
              action={
                <Button as="a" href="#/agents" variant="primary" icon="plus">
                  New agent
                </Button>
              }
            />
          </div>
        ) : (
          <div className="rooms-placeholder">
            <div className="rooms-placeholder-skeleton" aria-hidden="true">
              <Skeleton width={220} height={12} />
              <Skeleton width={160} height={12} />
              <Skeleton width={200} height={12} />
            </div>
          </div>
        )}
      </section>
    </main>
  );
}
