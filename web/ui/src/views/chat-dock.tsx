import { useEffect, useRef, useState } from "react";
import type { RoomRow } from "../api/rooms";
import type { Route } from "../app/router";
import {
  clearPeek,
  dockAdd,
  dockClose,
  dockMinimize,
  dockOpen,
  isRecent,
  noteRoomVisit,
  showPeek,
  useChatDock,
} from "../app/chat-dock-store";
import type { DockItem } from "../app/chat-dock-store";
import { Ic } from "../ui/icons";
import { AgentAvatar } from "./agent-avatar";
import { displayLabel } from "./agent-label";
import { TeamAvatar } from "./discussion";
import { gr8rColor, PresenceLabel, TypingDots, useRooms } from "./presence";
import { RoomView } from "./room-timeline";

/* The chat dock: chats with agents stay on screen on every page except the
   Chats page, like the old Facebook chat bar. On a desktop the chats sit at
   the bottom right as windows (the full room timeline and composer, so
   questions and approvals can be answered there) and minimized tabs; on a
   phone they are floating chat heads that open a full-height sheet.

   A room joins the dock when the owner leaves the Chats page while it is
   active (working, waiting on them, or a message in the last 30 minutes), or,
   for a room they chatted with in the last two hours, when it starts waiting
   on them or a new message arrives. Closing a chat only removes it from the
   dock; the agent keeps working. */

const ACTIVE_MESSAGE_MS = 30 * 60 * 1000;
const WINDOW_WIDTH = 336;
const TAB_WIDTH = 196;
const OVERFLOW_WIDTH = 56;
const GAP = 12;
const SIDEBAR_WIDTH = 280;
const PHONE_MAX = 760;
const MAX_HEADS = 3;

function stateOf(row: RoomRow | undefined): string {
  return row && row.presence ? row.presence.state : "";
}

/* A room worth keeping on screen when the owner leaves its chat. */
export function isActiveRoom(row: RoomRow | undefined, now = Date.now()): boolean {
  if (!row) {
    return false;
  }
  const state = stateOf(row);
  if (state === "working" || state === "waiting" || state === "problem") {
    return true;
  }
  const at = row.last_message ? Number(row.last_message.at) || 0 : 0;
  return at > 0 && now - at < ACTIVE_MESSAGE_MS;
}

/* How many windows fit side by side. */
export function maxOpenFor(width: number): number {
  if (width >= 1900) {
    return 3;
  }
  return width >= 1200 ? 2 : 1;
}

export interface DockLayout {
  windows: RoomRow[];
  tabs: RoomRow[];
  overflow: RoomRow[];
}

/* The docked rooms the rows still know, with their open flag. */
function knownRooms(
  items: DockItem[],
  byName: Record<string, RoomRow>
): { row: RoomRow; open: boolean }[] {
  const out: { row: RoomRow; open: boolean }[] = [];
  for (const item of items) {
    const row = byName[item.name];
    if (row) {
      out.push({ row, open: item.open });
    }
  }
  return out;
}

/* Splits the docked rooms into windows, visible tabs, and the "+N" overflow.
   Tabs of rooms waiting on the owner come first so they are never hidden. */
export function dockLayout(
  items: DockItem[],
  byName: Record<string, RoomRow>,
  width: number
): DockLayout {
  const known = knownRooms(items, byName);
  const windows = known
    .filter((entry) => entry.open)
    .slice(0, maxOpenFor(width))
    .map((entry) => entry.row);
  const rest = known.filter((entry) => !windows.includes(entry.row)).map((entry) => entry.row);
  const waiting = rest.filter((row) => stateOf(row) === "waiting");
  const others = rest.filter((row) => stateOf(row) !== "waiting");
  const ordered = [...waiting, ...others];
  const free =
    width - SIDEBAR_WIDTH - 2 * GAP - windows.length * (WINDOW_WIDTH + GAP);
  const fitAll = Math.max(0, Math.floor((free + GAP) / (TAB_WIDTH + GAP)));
  if (ordered.length <= fitAll) {
    return { windows, tabs: ordered, overflow: [] };
  }
  const fit = Math.max(
    0,
    Math.floor((free - OVERFLOW_WIDTH) / (TAB_WIDTH + GAP))
  );
  return { windows, tabs: ordered.slice(0, fit), overflow: ordered.slice(fit) };
}

function useViewportWidth(): number {
  const [width, setWidth] = useState(() => window.innerWidth);
  useEffect(() => {
    const onResize = () => setWidth(window.innerWidth);
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  return width;
}

export function ChatDock({ route }: { route: Route }) {
  const dock = useChatDock();
  const onChats = route.section === "chats";
  const chatName = route.view === "chat" ? route.params.name || "" : "";

  /* The owner has this chat open now: it counts as recent. */
  useEffect(() => {
    if (chatName) {
      noteRoomVisit(chatName);
    }
  }, [chatName]);

  const watching =
    dock.items.length > 0 ||
    Object.keys(dock.recent).length > 0 ||
    Boolean(chatName);
  if (!watching) {
    return null;
  }
  return <DockLive onChats={onChats} chatName={chatName} />;
}

/* Mounted only while there is something to watch, so a browser that never
   chats does not hold the rooms stream open. */
function DockLive({ onChats, chatName }: { onChats: boolean; chatName: string }) {
  const dock = useChatDock();
  const { rows, byName } = useRooms();
  const width = useViewportWidth();
  const phone = width <= PHONE_MAX;
  const maxOpen = maxOpenFor(width);

  /* Leaving the Chats page docks the chat the owner had open, when active. */
  const lastChatRef = useRef("");
  useEffect(() => {
    if (onChats) {
      if (chatName) {
        lastChatRef.current = chatName;
      }
      return;
    }
    const left = lastChatRef.current;
    lastChatRef.current = "";
    if (left && isActiveRoom(byName[left])) {
      dockOpen(left, phone ? 1 : maxOpen);
    }
    // Only the route matters here; byName is read at the moment of leaving.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onChats, chatName]);

  /* A room the owner chatted with lately joins the dock as a tab when it
     starts waiting on them or a new message arrives, with a peek card. */
  const seenRef = useRef<Record<string, { unread: number; state: string }>>({});
  useEffect(() => {
    const seen = seenRef.current;
    const next: Record<string, { unread: number; state: string }> = {};
    for (const row of rows) {
      const state = stateOf(row);
      const unread = Number(row.unread) || 0;
      next[row.name] = { unread, state };
      const before = seen[row.name];
      if (!before || onChats || row.name === chatName || !isRecent(row.name)) {
        continue;
      }
      const startedWaiting = state === "waiting" && before.state !== "waiting";
      const newMessage = unread > before.unread;
      if (!startedWaiting && !newMessage) {
        continue;
      }
      dockAdd(row.name);
      const item = dock.items.find((entry) => entry.name === row.name);
      if (newMessage && !(item && item.open)) {
        showPeek(row.name);
      }
    }
    seenRef.current = next;
  }, [rows, onChats, chatName, dock.items]);

  if (onChats || dock.items.length === 0) {
    return null;
  }
  if (phone) {
    return <DockHeads items={dock.items} byName={byName} />;
  }
  const layout = dockLayout(dock.items, byName, width);
  if (layout.windows.length + layout.tabs.length + layout.overflow.length === 0) {
    return null;
  }
  return (
    <div className="dock" role="region" aria-label="Chat dock">
      {layout.windows.map((row) => (
        <DockWindow key={row.name} row={row} />
      ))}
      {layout.tabs.map((row) => (
        <DockTab
          key={row.name}
          row={row}
          maxOpen={maxOpen}
          peek={dock.peek !== null && dock.peek.name === row.name}
        />
      ))}
      {layout.overflow.length > 0 ? (
        <DockOverflow rows={layout.overflow} maxOpen={maxOpen} />
      ) : null}
    </div>
  );
}

function RoomFace({ row, size }: { row: RoomRow; size: "md" | "lg" | "56" }) {
  if (row.kind === "team") {
    const px = size === "md" ? 26 : size === "lg" ? 36 : 46;
    return (
      <TeamAvatar
        size={px}
        items={[row.lead, ...(row.members || [])]
          .filter(Boolean)
          .map((name) => ({ name: String(name), color: "var(--violet)" }))}
      />
    );
  }
  const state = stateOf(row);
  return (
    <AgentAvatar
      name={row.name}
      color={gr8rColor(row.color)}
      size={size}
      presence={Boolean(row.presence)}
      /* Heads skip the motion: a big working avatar adds a speech bubble that
         would spill out of the round head. */
      mood={
        size === "56" ? undefined : state === "working" ? "thinking" : state === "waiting" ? "ask" : undefined
      }
    />
  );
}

/* The short status under a tab or head name. */
function TabLine({ row }: { row: RoomRow }) {
  const state = stateOf(row);
  if (state === "working") {
    return (
      <span className="dock-line is-working">
        <TypingDots />
        <span className="dock-line-text">{row.presence.label || "Working"}</span>
      </span>
    );
  }
  if (state === "waiting") {
    return <span className="dock-line is-waiting">Needs you</span>;
  }
  if (state === "problem") {
    return <span className="dock-line is-problem">{row.presence.label || "Problem"}</span>;
  }
  if (row.unread > 0) {
    return (
      <span className="dock-line is-unread">
        {row.unread === 1 ? "1 new reply" : row.unread + " new replies"}
      </span>
    );
  }
  return <span className="dock-line">{row.title || (row.kind === "team" ? "Team" : "Agent")}</span>;
}

function TabBadge({ row }: { row: RoomRow }) {
  if (stateOf(row) === "waiting") {
    return (
      <span className="dock-badge is-waiting" aria-label="Needs you">
        !
      </span>
    );
  }
  if (row.unread > 0) {
    return (
      <span className="dock-badge" aria-label={row.unread + " unread"}>
        {row.unread > 99 ? "99+" : row.unread}
      </span>
    );
  }
  return null;
}

function DockWindow({ row }: { row: RoomRow }) {
  const label = displayLabel(row);
  return (
    <section className="dock-win" aria-label={"Chat with " + label}>
      <header className="dock-win-head">
        <button
          type="button"
          className="dock-win-who"
          title="Minimize"
          onClick={() => dockMinimize(row.name)}
        >
          <RoomFace row={row} size="lg" />
          <span className="dock-win-title">
            <span className="dock-win-name">{label}</span>
            <span className="dock-win-sub">
              {row.presence ? <PresenceLabel presence={row.presence} /> : row.title}
            </span>
          </span>
        </button>
        <a
          className="ibtn ibtn-sm"
          href={"#/chats/" + encodeURIComponent(row.name)}
          aria-label={"Open the full chat with " + label}
          title="Open full chat"
        >
          <Ic name="maximize-2" size={15} />
        </a>
        <button
          type="button"
          className="ibtn ibtn-sm"
          aria-label={"Minimize the chat with " + label}
          title="Minimize"
          onClick={() => dockMinimize(row.name)}
        >
          <Ic name="minus" size={15} />
        </button>
        <button
          type="button"
          className="ibtn ibtn-sm"
          aria-label={"Close the chat with " + label}
          title="Close"
          onClick={() => dockClose(row.name)}
        >
          <Ic name="x" size={15} />
        </button>
      </header>
      <div className="dock-win-body">
        <RoomView room={row} variant="dock" />
      </div>
    </section>
  );
}

function DockTab({ row, maxOpen, peek }: { row: RoomRow; maxOpen: number; peek: boolean }) {
  const label = displayLabel(row);
  return (
    <div className={"dock-tab state-" + (stateOf(row) || "idle")}>
      {peek ? <DockPeek row={row} maxOpen={maxOpen} /> : null}
      <button
        type="button"
        className="dock-tab-main"
        aria-label={"Open the chat with " + label}
        onClick={() => dockOpen(row.name, maxOpen)}
      >
        <RoomFace row={row} size="md" />
        <span className="dock-tab-text">
          <span className="dock-tab-name">{label}</span>
          <TabLine row={row} />
        </span>
        <TabBadge row={row} />
      </button>
      <button
        type="button"
        className="ibtn ibtn-sm dock-tab-x"
        aria-label={"Close the chat with " + label}
        title="Close"
        onClick={() => dockClose(row.name)}
      >
        <Ic name="x" size={13} />
      </button>
    </div>
  );
}

function DockPeek({ row, maxOpen }: { row: RoomRow; maxOpen: number }) {
  const label = displayLabel(row);
  const preview = row.last_message ? row.last_message.preview : "";
  return (
    <div className="dock-peek" role="status">
      <div className="dock-peek-head">
        <RoomFace row={row} size="md" />
        <span className="dock-peek-who">
          <span className="dock-peek-name">{label}</span>
          <span className="dock-peek-sub">New reply · now</span>
        </span>
        <button
          type="button"
          className="ibtn ibtn-sm"
          aria-label="Dismiss"
          onClick={() => clearPeek()}
        >
          <Ic name="x" size={13} />
        </button>
      </div>
      {preview ? <p className="dock-peek-text">{preview}</p> : null}
      <div className="dock-peek-actions">
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          onClick={() => dockOpen(row.name, maxOpen)}
        >
          Reply
        </button>
        <a className="btn btn-ghost btn-sm" href={"#/chats/" + encodeURIComponent(row.name)}>
          Open chat
        </a>
      </div>
    </div>
  );
}

function DockOverflow({ rows, maxOpen }: { rows: RoomRow[]; maxOpen: number }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) {
      return;
    }
    const onDown = (event: MouseEvent) => {
      if (ref.current && !ref.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);
  return (
    <div className="dock-more" ref={ref}>
      {open ? (
        <div className="dock-more-menu" role="menu" aria-label="More chats">
          {rows.map((row) => {
            return (
              <button
                key={row.name}
                type="button"
                role="menuitem"
                className="dock-more-item"
                onClick={() => {
                  setOpen(false);
                  dockOpen(row.name, maxOpen);
                }}
              >
                <RoomFace row={row} size="md" />
                <span className="dock-tab-text">
                  <span className="dock-tab-name">{displayLabel(row)}</span>
                  <TabLine row={row} />
                </span>
                <TabBadge row={row} />
              </button>
            );
          })}
        </div>
      ) : null}
      <button
        type="button"
        className="dock-more-btn"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={rows.length + " more chats"}
        onClick={() => setOpen((value) => !value)}
      >
        {"+" + rows.length}
      </button>
    </div>
  );
}

/* Phone: floating chat heads; a tap opens the chat as a full-height sheet with
   the heads in a row above it to switch chats. */
function DockHeads({ items, byName }: { items: DockItem[]; byName: Record<string, RoomRow> }) {
  const [sheet, setSheet] = useState("");
  const heads = knownRooms(items, byName)
    .slice(0, MAX_HEADS)
    .map((entry) => entry.row);
  const sheetRow = sheet ? heads.find((row) => row.name === sheet) : undefined;

  /* A sheet whose room left the dock just falls back to the heads. */
  const front = heads[0];
  if (!front) {
    return null;
  }
  if (!sheetRow) {
    return (
      <div className="dock-heads" role="region" aria-label="Chat dock">
        {stateOf(front) === "working" ? (
          <span className="dock-head-label" aria-hidden="true">
            <TypingDots />
            {displayLabel(front) + " is working…"}
          </span>
        ) : null}
        <div className="dock-head-stack">
          {heads.map((row) => (
            <DockHead key={row.name} row={row} onOpen={() => setSheet(row.name)} />
          ))}
        </div>
      </div>
    );
  }
  const label = displayLabel(sheetRow);
  return (
    <div className="dock-sheet-wrap" role="dialog" aria-modal="true" aria-label={"Chat with " + label}>
      <div className="dock-scrim" aria-hidden="true" onClick={() => setSheet("")} />
      <div className="dock-sheet-heads">
        {heads.map((row) => (
          <DockHead
            key={row.name}
            row={row}
            selected={row.name === sheet}
            onOpen={() => setSheet(row.name)}
          />
        ))}
      </div>
      <section className="dock-sheet">
        <span className="dock-sheet-handle" aria-hidden="true" />
        <header className="dock-win-head">
          <span className="dock-win-who">
            <RoomFace row={sheetRow} size="lg" />
            <span className="dock-win-title">
              <span className="dock-win-name">{label}</span>
              <span className="dock-win-sub">
                {sheetRow.presence ? <PresenceLabel presence={sheetRow.presence} /> : sheetRow.title}
              </span>
            </span>
          </span>
          <a
            className="ibtn ibtn-sm"
            href={"#/chats/" + encodeURIComponent(sheetRow.name)}
            aria-label={"Open the full chat with " + label}
            title="Open full chat"
          >
            <Ic name="square-arrow-out-up-right" size={16} />
          </a>
          <button
            type="button"
            className="ibtn ibtn-sm"
            aria-label={"Close the chat with " + label}
            title="Close"
            onClick={() => {
              setSheet("");
              dockClose(sheetRow.name);
            }}
          >
            <Ic name="x" size={16} />
          </button>
          <button
            type="button"
            className="ibtn ibtn-sm"
            aria-label="Collapse"
            title="Collapse"
            onClick={() => setSheet("")}
          >
            <Ic name="chevron-down" size={16} />
          </button>
        </header>
        <div className="dock-win-body">
          <RoomView key={sheetRow.name} room={sheetRow} variant="dock" />
        </div>
      </section>
    </div>
  );
}

function DockHead({
  row,
  selected,
  onOpen,
}: {
  row: RoomRow;
  selected?: boolean;
  onOpen: () => void;
}) {
  const label = displayLabel(row);
  return (
    <button
      type="button"
      className={"dock-head state-" + (stateOf(row) || "idle") + (selected ? " on" : "")}
      aria-label={"Open the chat with " + label}
      aria-pressed={selected}
      onClick={onOpen}
    >
      <RoomFace row={row} size="56" />
      <TabBadge row={row} />
    </button>
  );
}
