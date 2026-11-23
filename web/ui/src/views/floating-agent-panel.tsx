import { useMemo, useState } from "react";
import type { RoomRow } from "../api/rooms";
import { openRoom, showList } from "../app/floating-agent-store";
import { Ic } from "../ui/icons";
import { EmptyState, SearchInput, Seg } from "../ui/gr8r";
import { AgentAvatar } from "./agent-avatar";
import { displayLabel } from "./agent-label";
import { TeamAvatar } from "./discussion";
import { gr8rColor, PresenceLabel } from "./presence";
import { needsYou, RoomListRow, rowState, rowTime } from "./room-list-row";
import { RoomView } from "./room-timeline";

/* Spec 72: the parts the desktop panel and the phone sheet share — the chat
   list (REQ-FA-021), the room (REQ-FA-022/023) and the room picking rules. */

export const PHONE_MAX = 760;

export function stateOf(row: RoomRow | undefined): string {
  return row ? rowState(row) : "";
}

export function isWaiting(row: RoomRow | undefined): boolean {
  return stateOf(row) === "waiting";
}

/* A room that is doing something right now. */
export function isActive(row: RoomRow | undefined): boolean {
  const state = stateOf(row);
  return state === "working" || state === "waiting" || state === "problem";
}

function newest(rows: RoomRow[]): RoomRow | undefined {
  let best: RoomRow | undefined;
  for (const row of rows) {
    if (!best || rowTime(row) > rowTime(best)) {
      best = row;
    }
  }
  return best;
}

/* REQ-FA-002: the saved room, else one waiting on the owner, else a working
   one, else the room with the newest message. */
export function pickCurrent(rows: RoomRow[], saved: string): RoomRow | undefined {
  const kept = saved ? rows.find((row) => row.name === saved) : undefined;
  if (kept) {
    return kept;
  }
  return (
    rows.find((row) => isWaiting(row)) ||
    newest(rows.filter((row) => stateOf(row) === "working")) ||
    newest(rows)
  );
}

/* REQ-FA-010: other rooms that want the owner, waiting ones first. */
export function attentionRooms(rows: RoomRow[], exclude: string, max = 2): RoomRow[] {
  const others = rows.filter((row) => row.name !== exclude);
  const waiting = others.filter((row) => needsYou(row));
  const unread = others.filter((row) => !needsYou(row) && row.unread > 0);
  return [...waiting, ...unread].slice(0, max);
}

/* REQ-FA-043: active rooms for the phone stack, waiting first, then the
   newest working. */
export function stackRooms(rows: RoomRow[]): RoomRow[] {
  const active = rows.filter((row) => isActive(row));
  const since = (row: RoomRow) => (row.presence ? Number(row.presence.since) || 0 : 0);
  const waiting = active.filter((row) => needsYou(row));
  const working = active.filter((row) => !needsYou(row)).sort((a, b) => since(b) - since(a));
  return [...waiting, ...working];
}

/* The count on the back button and the tucked edge: rooms other than this
   one that wait on the owner or have unread messages. */
export function othersPending(rows: RoomRow[], exclude: string): number {
  return rows.filter((row) => row.name !== exclude && (needsYou(row) || row.unread > 0)).length;
}

export function colorMap(rows: RoomRow[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const row of rows) {
    out[row.name] = row.color;
  }
  return out;
}

/* A round face with a status ring (working spins, waiting is amber). */
export function Face({
  row,
  colors,
  size = 36,
}: {
  row: RoomRow;
  colors: Record<string, string>;
  size?: number;
}) {
  const state = stateOf(row) || "idle";
  const inner = Math.round(size * 0.78);
  return (
    <span className={"fa-face state-" + state} style={{ width: size, height: size }} aria-hidden="true">
      {row.kind === "team" ? (
        <TeamAvatar
          size={inner}
          items={[row.lead, ...(row.members || [])]
            .filter(Boolean)
            .map((name) => ({ name: String(name), color: gr8rColor(colors[String(name)]) }))}
        />
      ) : (
        <span className="fa-face-av" style={{ width: inner, height: inner }}>
          <AgentAvatar name={row.name} color={gr8rColor(row.color)} size="auto" />
        </span>
      )}
    </span>
  );
}

export function Count({ row }: { row: RoomRow }) {
  if (needsYou(row)) {
    return (
      <span className="fa-badge is-waiting" aria-label="Needs you">
        !
      </span>
    );
  }
  if (row.unread > 0) {
    return (
      <span className="fa-badge" aria-label={row.unread + " unread"}>
        {row.unread > 99 ? "99+" : row.unread}
      </span>
    );
  }
  return null;
}

/* The status line under a name: the presence label while busy, else the
   title. */
export function StatusLine({ row }: { row: RoomRow }) {
  if (row.presence && isActive(row)) {
    return <PresenceLabel presence={row.presence} />;
  }
  return <span className="fa-sub-text">{row.title || (row.kind === "team" ? "Team" : "Agent")}</span>;
}

type FilterKey = "all" | "working" | "needs";

export function ChatList({
  rows,
  colors,
  current,
  onClose,
}: {
  rows: RoomRow[];
  colors: Record<string, string>;
  current: string;
  onClose: () => void;
}) {
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
  return (
    <div className="fa-view fa-list">
      <header className="fa-head">
        <h2 className="fa-list-title">Chats</h2>
        <span className="sp" />
        <a className="ibtn ibtn-sm" href="#/chats" aria-label="Open the Chats page" title="Open the Chats page">
          <Ic name="square-arrow-out-up-right" size={15} />
        </a>
        <button type="button" className="ibtn ibtn-sm" aria-label="Close" title="Close" onClick={onClose}>
          <Ic name="minus" size={15} />
        </button>
      </header>
      <div className="fa-list-tools">
        <SearchInput
          wrapClassName="fa-search"
          placeholder="Search agents and messages"
          aria-label="Search agents and messages"
          autoComplete="off"
          spellCheck={false}
          kbd={null}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <Seg
          className="fa-filters"
          aria-label="Chat filters"
          value={filter}
          onChange={(value) => setFilter(value as FilterKey)}
          options={[
            { value: "all", label: "All ", count: counts.all },
            { value: "working", label: "Working ", count: counts.working },
            { value: "needs", label: "Needs you ", count: counts.needs },
          ]}
        />
      </div>
      <div className="fa-list-body">
        {visible.length === 0 ? (
          <EmptyState size="sm" icon="search-x" title="No chats match" text="Try a different search or filter." />
        ) : (
          <ul className="rooms-rows">
            {visible.map((row) => (
              <li key={row.name}>
                <RoomListRow
                  row={row}
                  colors={colors}
                  active={row.name === current}
                  onSelect={() => openRoom(row.name)}
                />
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

/* "researcher needs you", or "researcher · Waiting on approval: bash" when
   the presence label says more than "Needs you". */
export function stripText(row: RoomRow): string {
  const label = row.presence ? String(row.presence.label || "").trim() : "";
  if (!label || label.toLowerCase() === "needs you") {
    return displayLabel(row) + " needs you";
  }
  return displayLabel(row) + " · " + label;
}

export function ChatRoom({
  row,
  rows,
  colors,
  onClose,
}: {
  row: RoomRow;
  rows: RoomRow[];
  colors: Record<string, string>;
  onClose: () => void;
}) {
  const label = displayLabel(row);
  const pending = othersPending(rows, row.name);
  const asking = rows.find((other) => other.name !== row.name && isWaiting(other));
  return (
    <div className="fa-view fa-room">
      <header className="fa-head">
        <button
          type="button"
          className="fa-back"
          aria-label={pending > 0 ? "All chats, " + pending + " need you or have news" : "All chats"}
          title="All chats"
          onClick={() => showList()}
        >
          <Ic name="chevron-left" size={18} />
          {pending > 0 ? <span className="fa-back-count">{pending}</span> : null}
        </button>
        <Face row={row} colors={colors} size={34} />
        <span className="fa-who-text">
          <span className="fa-name">{label}</span>
          <span className="fa-sub">
            <StatusLine row={row} />
          </span>
        </span>
        <span className="sp" />
        <a
          className="ibtn ibtn-sm"
          href={"#/chats/" + encodeURIComponent(row.name)}
          aria-label={"Open the full chat with " + label}
          title="Open full chat"
        >
          <Ic name="square-arrow-out-up-right" size={15} />
        </a>
        <button type="button" className="ibtn ibtn-sm" aria-label="Close" title="Close" onClick={onClose}>
          <Ic name="minus" size={15} />
        </button>
      </header>
      {asking ? (
        <button type="button" className="fa-strip" onClick={() => openRoom(asking.name)}>
          <Face row={asking} colors={colors} size={22} />
          <span className="fa-strip-text">{stripText(asking)}</span>
          <span className="fa-strip-go">
            View
            <Ic name="chevron-right" size={14} />
          </span>
        </button>
      ) : null}
      <div className="fa-room-body">
        <RoomView key={row.name} room={row} variant="dock" />
      </div>
    </div>
  );
}
