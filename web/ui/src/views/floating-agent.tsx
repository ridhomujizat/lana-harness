import { useEffect, useRef, useState } from "react";
import type { RoomRow } from "../api/rooms";
import type { Route } from "../app/router";
import {
  clearNotif,
  closePanel,
  getFloating,
  notify,
  openPanel,
  openRoom,
  setCurrent,
  setTucked,
  showList,
  togglePanel,
  useFloatingAgent,
} from "../app/floating-agent-store";
import type { FaState } from "../app/floating-agent-store";
import { Ic } from "../ui/icons";
import { displayLabel } from "./agent-label";
import {
  attentionRooms,
  ChatList,
  ChatRoom,
  colorMap,
  Count,
  Face,
  isWaiting,
  PHONE_MAX,
  pickCurrent,
  stateOf,
  StatusLine,
} from "./floating-agent-panel";
import { FloatingPhone } from "./floating-agent-phone";
import { useRooms } from "./presence";
import { needsYou } from "./room-list-row";

/* Spec 72: one floating agent at the bottom right of every page but the
   Chats section. On a desktop it is a pill stuck to the right edge that opens
   a panel with the chat list and the room, and tucks into the edge (owner,
   2026-11-23: the Workbench look on every page). A notification grows the
   pill into a card. On a phone it is a draggable
   bubble (floating-agent-phone.tsx). */

function useViewportWidth(): number {
  const [width, setWidth] = useState(() => window.innerWidth);
  useEffect(() => {
    const onResize = () => setWidth(window.innerWidth);
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  return width;
}

/* ⌥Space toggles the panel, except while typing in a field or a terminal. */
function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  if (target.closest(".xterm")) {
    return true;
  }
  const tag = target.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || target.isContentEditable;
}

export function FloatingAgent({ route }: { route: Route }) {
  const onChats = route.section === "chats";
  const chatName = route.view === "chat" ? route.params.name || "" : "";

  /* A chat opened on the Chats page becomes the pill's agent. */
  useEffect(() => {
    if (chatName) {
      setCurrent(chatName);
    }
  }, [chatName]);

  if (onChats) {
    return null;
  }
  return <FloatingLive />;
}

function FloatingLive() {
  const fa = useFloatingAgent();
  const { rows, byName } = useRooms();
  const width = useViewportWidth();
  const phone = width <= PHONE_MAX;
  useNotifications(rows, byName);
  const current = pickCurrent(rows, fa.current);
  if (!current) {
    return null;
  }
  const colors = colorMap(rows);
  if (phone) {
    return <FloatingPhone rows={rows} byName={byName} colors={colors} current={current} fa={fa} />;
  }
  return (
    <FloatingDesktop rows={rows} byName={byName} colors={colors} current={current} fa={fa} />
  );
}

/* REQ-FA-030/031: a room that starts waiting, or gets a new message, while
   the owner is not looking at it raises the notification card. */
function useNotifications(rows: RoomRow[], byName: Record<string, RoomRow>) {
  const seenRef = useRef<Record<string, { unread: number; state: string }> | null>(null);
  useEffect(() => {
    const seen = seenRef.current;
    const next: Record<string, { unread: number; state: string }> = {};
    const fa = getFloating();
    for (const row of rows) {
      const state = stateOf(row);
      const unread = Number(row.unread) || 0;
      next[row.name] = { unread, state };
      const before = seen ? seen[row.name] : undefined;
      if (!before) {
        continue;
      }
      const looking = fa.open && fa.view === "room" && fa.current === row.name;
      if (looking) {
        continue;
      }
      if (state === "waiting" && before.state !== "waiting") {
        notify(row.name, "waiting");
      } else if (unread > before.unread) {
        notify(row.name, "reply");
      }
    }
    const notif = getFloating().notif;
    if (notif && notif.kind === "waiting" && !isWaiting(byName[notif.name])) {
      clearNotif(notif.name);
    }
    seenRef.current = next;
  }, [rows, byName]);
}

interface DesktopProps {
  rows: RoomRow[];
  byName: Record<string, RoomRow>;
  colors: Record<string, string>;
  current: RoomRow;
  fa: FaState;
}

function FloatingDesktop({ rows, byName, colors, current, fa }: DesktopProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  const notifRow = fa.notif && !fa.open ? byName[fa.notif.name] : undefined;
  const tucked = fa.tucked && !fa.open && !notifRow;

  /* REQ-FA-032: a notification slides the tucked pill out. */
  useEffect(() => {
    if (notifRow && getFloating().tucked) {
      setTucked(false);
    }
  }, [notifRow]);

  /* REQ-FA-011: ⌥Space; REQ-FA-024: Esc steps back from the room to the
     list, then closes. */
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.altKey && event.code === "Space" && !event.metaKey && !event.ctrlKey) {
        if (isTypingTarget(event.target)) {
          return;
        }
        event.preventDefault();
        setTucked(false);
        togglePanel();
        return;
      }
      if (event.key !== "Escape" || event.defaultPrevented || !getFloating().open) {
        return;
      }
      const inside = panelRef.current && event.target instanceof Node && panelRef.current.contains(event.target);
      if (!inside && event.target !== document.body) {
        return;
      }
      if (getFloating().view === "room") {
        showList();
      } else {
        closePanel();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  /* Reply: put the caret in the room's composer. */
  useEffect(() => {
    if (!fa.focusTick || !fa.open) {
      return;
    }
    const timer = window.setTimeout(() => {
      const field = panelRef.current?.querySelector("textarea");
      if (field) {
        field.focus();
      }
    }, 60);
    return () => window.clearTimeout(timer);
  }, [fa.focusTick, fa.open]);

  const roomRow = fa.view === "room" ? current : undefined;
  return (
    <div className="fa is-edge" role="region" aria-label="Floating agent">
      {fa.open ? (
        <div className="fa-panel" ref={panelRef}>
          {roomRow ? (
            <ChatRoom row={roomRow} rows={rows} colors={colors} onClose={closePanel} />
          ) : (
            <ChatList rows={rows} colors={colors} current={current.name} onClose={closePanel} />
          )}
        </div>
      ) : null}
      {tucked ? (
        <TuckedTab rows={rows} colors={colors} current={current} />
      ) : (
        <Pill
          rows={rows}
          colors={colors}
          current={current}
          open={fa.open}
          edge
          notifRow={notifRow}
          notifKind={fa.notif ? fa.notif.kind : "reply"}
        />
      )}
    </div>
  );
}

/* REQ-FA-012: half a pill at the right edge. */
function TuckedTab({ rows, colors, current }: { rows: RoomRow[]; colors: Record<string, string>; current: RoomRow }) {
  const waiting = rows.filter((row) => needsYou(row)).length;
  const unread = rows.reduce((sum, row) => sum + (Number(row.unread) || 0), 0);
  const label = displayLabel(current);
  return (
    <button
      type="button"
      className="fa-tuck"
      aria-label={"Show the floating agent, " + label}
      title={label}
      onClick={() => setTucked(false)}
    >
      <span className="fa-tuck-face">
        <Face row={current} colors={colors} size={38} />
        {waiting > 0 ? (
          <span className="fa-badge is-waiting">{waiting}</span>
        ) : unread > 0 ? (
          <span className="fa-badge">{unread > 99 ? "99+" : unread}</span>
        ) : null}
      </span>
      <Ic name="chevron-left" size={16} />
    </button>
  );
}

function Pill({
  rows,
  colors,
  current,
  open,
  edge,
  notifRow,
  notifKind,
}: {
  rows: RoomRow[];
  colors: Record<string, string>;
  current: RoomRow;
  open: boolean;
  edge: boolean;
  notifRow: RoomRow | undefined;
  notifKind: "waiting" | "reply";
}) {
  const others = attentionRooms(rows, current.name);
  const label = displayLabel(current);
  const toggle = () => {
    if (open) {
      closePanel();
    } else {
      openPanel();
    }
  };
  /* The round button: at the edge it tucks the pill while the
     panel is closed; otherwise it opens and closes the panel. */
  const tuckButton = edge && !open;
  return (
    <div className={"fa-pill" + (notifRow ? " has-card" : "")}>
      {notifRow ? <NotifCard row={notifRow} kind={notifKind} colors={colors} /> : null}
      <div className="fa-pill-row">
        <button
          type="button"
          className="fa-who"
          aria-expanded={open}
          aria-label={(open ? "Close the chat with " : "Open the chat with ") + label}
          onClick={toggle}
        >
          <Face row={current} colors={colors} size={38} />
          <span className="fa-who-text">
            <span className="fa-name">{label}</span>
            <span className="fa-sub">
              <StatusLine row={current} />
            </span>
          </span>
        </button>
        {others.length > 0 ? (
          <>
            <span className="fa-divider" aria-hidden="true" />
            <span className="fa-others">
              {others.map((row) => (
                <button
                  key={row.name}
                  type="button"
                  className="fa-mini"
                  aria-label={"Open the chat with " + displayLabel(row) + (needsYou(row) ? ", needs you" : "")}
                  title={displayLabel(row)}
                  onClick={() => openRoom(row.name)}
                >
                  <Face row={row} colors={colors} size={30} />
                  <Count row={row} />
                </button>
              ))}
            </span>
          </>
        ) : null}
        {tuckButton ? (
          <button
            type="button"
            className="fa-round"
            aria-label="Tuck the floating agent into the edge"
            title="Tuck into the edge"
            onClick={() => setTucked(true)}
          >
            <Ic name="chevron-right" size={16} />
          </button>
        ) : (
          <button
            type="button"
            className="fa-round"
            aria-label={open ? "Close the panel" : "Open the panel"}
            title={open ? "Close" : "Open"}
            onClick={toggle}
          >
            <Ic name={open ? "chevron-down" : "chevron-up"} size={16} />
          </button>
        )}
      </div>
    </div>
  );
}

/* REQ-FA-030: the pill grown upward; Open / Reply open the panel there. */
function NotifCard({ row, kind, colors }: { row: RoomRow; kind: "waiting" | "reply"; colors: Record<string, string> }) {
  const label = displayLabel(row);
  const waiting = kind === "waiting";
  const preview = row.last_message ? row.last_message.preview : "";
  const presenceLabel = row.presence ? String(row.presence.label || "").trim() : "";
  /* A waiting room's label ("Waiting on approval: bash") says what it needs;
     a bare "Needs you" repeats the kind line, so the last message shows. */
  const detail = waiting && presenceLabel && presenceLabel.toLowerCase() !== "needs you";
  const text = detail ? presenceLabel : preview;
  return (
    <div className="fa-card" role="status">
      <div className="fa-card-head">
        <Face row={row} colors={colors} size={30} />
        <span className="fa-who-text">
          <span className="fa-name">{label}</span>
          <span className={"fa-card-kind" + (waiting ? " is-waiting" : "")}>
            {waiting ? "Needs you · now" : "New reply · now"}
          </span>
        </span>
        <button
          type="button"
          className="fa-round fa-round-sm"
          aria-label="Shrink the notification"
          title="Shrink"
          onClick={() => clearNotif()}
        >
          <Ic name="minus" size={14} />
        </button>
      </div>
      {text ? <p className={"fa-card-text" + (detail ? " is-mono" : "")}>{text}</p> : null}
      <div className="fa-card-actions">
        {waiting ? (
          <button type="button" className="btn btn-primary btn-sm" onClick={() => openRoom(row.name)}>
            Open
          </button>
        ) : (
          <>
            <button type="button" className="btn btn-primary btn-sm" onClick={() => openRoom(row.name, true)}>
              Reply
            </button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => openRoom(row.name)}>
              Open
            </button>
          </>
        )}
      </div>
    </div>
  );
}

