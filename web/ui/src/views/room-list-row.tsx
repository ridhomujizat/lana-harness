import { useRef, useState } from "react";
import type { MouseEvent as ReactMouseEvent, ReactNode } from "react";
import type { RoomRow } from "../api/rooms";
import type { AvatarMood } from "../lib/pixel-avatar";
import { Badge, Menu } from "../ui/gr8r";
import type { BadgeTone } from "../ui/gr8r";
import { Ic } from "../ui/icons";
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

/* Spec 85 REQ-ARC-007: a room is archived when archived_at is set (0 = live). */
export function isArchived(row: RoomRow): boolean {
  return Number(row.archived_at) > 0;
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
   message preview otherwise (REQ-ROOM-001). A cleared room with no newer
   message reads "Chat cleared" (spec 85 REQ-ARC-011). */
export function RowStatus({ row }: { row: RoomRow }) {
  const state = rowState(row);
  if (state === "waiting" || state === "working" || state === "problem") {
    return <PresenceLabel presence={row.presence} />;
  }
  if (!row.last_message && Number(row.cleared_at) > 0) {
    return <span className="rooms-row-preview">Chat cleared</span>;
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

/* A link (Chats page) or a button (floating agent list). Spec 85 adds the ⋯
   menu (row hover, or right-click) and the archived view's Unarchive button. */
export function RoomListRow({
  row,
  colors,
  active = false,
  href,
  onSelect,
  menu,
  onUnarchive,
}: {
  row: RoomRow;
  colors: Record<string, string>;
  active?: boolean;
  href?: string;
  onSelect?: () => void;
  /* REQ-ARC-007: the ⋯ menu items; when set the row shows the ⋯ button on
     hover and opens the same menu on right-click. */
  menu?: ReactNode;
  /* REQ-ARC-008: the archived view shows an Unarchive button on hover. */
  onUnarchive?: () => void;
}) {
  const moreRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const body = (
    <>
      <RowFace row={row} colors={colors} />
      <span className="rooms-row-body">
        <span className="rooms-row-top">
          <span className="rooms-row-name">{displayLabel(row)}</span>
          <span className="rooms-row-time">{relativeShort(rowTime(row))}</span>
          {onUnarchive ? (
            <button
              type="button"
              className="rooms-row-unarchive"
              onClick={(event) => {
                event.preventDefault();
                event.stopPropagation();
                onUnarchive();
              }}
            >
              <Ic name="archive-restore" size={14} />
              Unarchive
            </button>
          ) : menu ? (
            <button
              type="button"
              ref={moreRef}
              className="rooms-row-more"
              aria-label="Chat actions"
              aria-haspopup="menu"
              aria-expanded={open}
              onClick={(event) => {
                event.preventDefault();
                event.stopPropagation();
                setOpen((value) => !value);
              }}
            >
              <Ic name="ellipsis" size={16} />
            </button>
          ) : null}
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
  function onContextMenu(event: ReactMouseEvent) {
    if (!menu) {
      return;
    }
    event.preventDefault();
    setOpen(true);
  }
  const node =
    href !== undefined ? (
      <a
        className={className}
        href={href}
        aria-current={active ? "page" : undefined}
        onContextMenu={onContextMenu}
      >
        {body}
      </a>
    ) : (
      <button type="button" className={className} onClick={onSelect} onContextMenu={onContextMenu}>
        {body}
      </button>
    );
  if (!menu) {
    return node;
  }
  return (
    <>
      {node}
      <Menu
        anchorRef={moreRef}
        open={open}
        onClose={() => setOpen(false)}
        className="rooms-row-menu"
      >
        {menu}
      </Menu>
    </>
  );
}
