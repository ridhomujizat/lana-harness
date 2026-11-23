import type { RoomRow } from "../api/rooms";
import type { AvatarMood } from "../lib/pixel-avatar";
import { Badge } from "../ui/gr8r";
import type { BadgeTone } from "../ui/gr8r";
import { AgentAvatar } from "./agent-avatar";
import { displayLabel } from "./agent-label";
import { TeamAvatar } from "./discussion";
import { gr8rColor, PresenceLabel } from "./presence";
import { relativeShort } from "./room-timeline";

/* One room row of the Chats list (REQ-ROOM-001), shared by the Chats page and
   the floating agent's list (spec 72 REQ-FA-021). */

export function rowState(row: RoomRow): string {
  return row.presence ? row.presence.state : "";
}

/* REQ-ROOM-001: "Needs you" is a room that is waiting or has a problem. */
export function needsYou(row: RoomRow): boolean {
  const state = rowState(row);
  return state === "waiting" || state === "problem";
}

export function unreadTone(row: RoomRow): BadgeTone {
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
export function RowStatus({ row }: { row: RoomRow }) {
  const state = rowState(row);
  if (state === "waiting" || state === "working" || state === "problem") {
    return <PresenceLabel presence={row.presence} />;
  }
  const preview = row.last_message ? row.last_message.preview : "";
  return <span className="rooms-row-preview">{preview || "No messages yet"}</span>;
}

/* The chat list avatar moves like the turn head, only while its room works
   or waits on you; a quiet room stays still. "Running bash" style labels
   name a tool, so they show the tool ring. */
export function rowMood(row: RoomRow): AvatarMood | undefined {
  const presence = row.presence;
  switch (presence ? presence.state : "") {
    case "working": {
      const label = String(presence?.label || "").toLowerCase();
      return label.startsWith("running") || label.startsWith("using") ? "tool" : "thinking";
    }
    case "waiting":
      return "ask";
    default:
      return undefined;
  }
}

export function RowFace({
  row,
  colors,
  size = "lg",
  still = false,
}: {
  row: RoomRow;
  colors: Record<string, string>;
  size?: "md" | "lg";
  /* Skip the working/asking motion (small faces in tight places). */
  still?: boolean;
}) {
  if (row.kind === "team") {
    /* REQ-RDS-056: two faces, three faces, or two faces and +N. */
    const items = [row.lead, ...(row.members || [])]
      .filter(Boolean)
      .map((name) => ({ name: String(name), color: gr8rColor(colors[String(name)]) }));
    return <TeamAvatar items={items} size={size === "md" ? 30 : 44} />;
  }
  const state = rowState(row);
  return (
    <AgentAvatar
      name={row.name}
      color={gr8rColor(row.color)}
      size={size}
      presence={Boolean(row.presence)}
      mood={still ? undefined : rowMood(row)}
      className={"chat-coin" + (state ? " state-" + state : "")}
    />
  );
}

export function rowTime(row: RoomRow): number {
  return row.last_message ? row.last_message.at : row.last_active_at;
}

/* A link (Chats page) or a button (floating agent list). */
export function RoomListRow({
  row,
  colors,
  active = false,
  href,
  onSelect,
}: {
  row: RoomRow;
  colors: Record<string, string>;
  active?: boolean;
  href?: string;
  onSelect?: () => void;
}) {
  const body = (
    <>
      <RowFace row={row} colors={colors} />
      <span className="rooms-row-body">
        <span className="rooms-row-top">
          <span className="rooms-row-name">{displayLabel(row)}</span>
          <span className="rooms-row-time">{relativeShort(rowTime(row))}</span>
        </span>
        <span className="rooms-row-title">{row.title || (row.kind === "team" ? "Team" : "Agent")}</span>
        <span className="rooms-row-line">
          <RowStatus row={row} />
        </span>
      </span>
      {row.unread > 0 ? (
        <Badge className="rooms-row-badge" tone={unreadTone(row)} aria-label={row.unread + " unread"}>
          {row.unread > 99 ? "99+" : row.unread}
        </Badge>
      ) : null}
    </>
  );
  const className = "rooms-row" + (active ? " on" : "");
  if (href !== undefined) {
    return (
      <a className={className} href={href} aria-current={active ? "page" : undefined}>
        {body}
      </a>
    );
  }
  return (
    <button type="button" className={className} onClick={onSelect}>
      {body}
    </button>
  );
}
