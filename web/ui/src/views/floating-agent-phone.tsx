import { useEffect, useRef, useState } from "react";
import type { CSSProperties, PointerEvent as ReactPointerEvent } from "react";
import type { RoomRow } from "../api/rooms";
import {
  clearNotif,
  closePanel,
  openRoom,
  setBubble,
  setHidden,
  showList,
} from "../app/floating-agent-store";
import type { BubblePos, FaState } from "../app/floating-agent-store";
import { Ic } from "../ui/icons";
import { displayLabel } from "./agent-label";
import { ChatList, ChatRoom, Count, Face, stackRooms, StatusLine } from "./floating-agent-panel";
import { needsYou } from "./room-list-row";

/* Spec 72 phone (REQ-FA-040–044): a bubble that can be dragged anywhere and
   glides to the nearest edge on release, like the iOS AssistiveTouch button.
   Several active rooms show as a stack of heads that fans out on a tap. */

export const BUBBLE = 60;
const EDGE = 8;
const TOP = 64;
const BOTTOM_SAFE = 24;
const DRAG_SLOP = 6;
const IDLE_MS = 3000;
const LABEL_MS = 2000;
const HIDE_RADIUS = 56;

export interface Bounds {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

/* Where the bubble's top-left corner may rest. */
export function boundsFor(vw: number, vh: number, size = BUBBLE): Bounds {
  return {
    left: EDGE,
    top: TOP,
    right: Math.max(EDGE, vw - EDGE - size),
    bottom: Math.max(TOP, vh - EDGE - BOTTOM_SAFE - size),
  };
}

function ratio(value: number, from: number, to: number): number {
  if (to <= from) {
    return 0;
  }
  return Math.min(1, Math.max(0, (value - from) / (to - from)));
}

/* REQ-FA-040: the nearest left or right edge; the top or bottom edge when
   released within 15 % of the screen height from it. */
export function snapFor(x: number, y: number, vw: number, vh: number, size = BUBBLE): BubblePos {
  const b = boundsFor(vw, vh, size);
  const cy = y + size / 2;
  if (cy - b.top < vh * 0.15) {
    return { side: "top", pct: ratio(x, b.left, b.right) };
  }
  if (b.bottom + size - cy < vh * 0.15) {
    return { side: "bottom", pct: ratio(x, b.left, b.right) };
  }
  const side = x + size / 2 < vw / 2 ? "left" : "right";
  return { side, pct: ratio(y, b.top, b.bottom) };
}

export function placeFor(pos: BubblePos, vw: number, vh: number, size = BUBBLE): { x: number; y: number } {
  const b = boundsFor(vw, vh, size);
  const along = (from: number, to: number) => from + pos.pct * (to - from);
  switch (pos.side) {
    case "left":
      return { x: b.left, y: along(b.top, b.bottom) };
    case "top":
      return { x: along(b.left, b.right), y: b.top };
    case "bottom":
      return { x: along(b.left, b.right), y: b.bottom };
    default:
      return { x: b.right, y: along(b.top, b.bottom) };
  }
}

function useViewport(): { vw: number; vh: number } {
  const [size, setSize] = useState(() => ({ vw: window.innerWidth, vh: window.innerHeight }));
  useEffect(() => {
    const onResize = () => setSize({ vw: window.innerWidth, vh: window.innerHeight });
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  return size;
}

interface PhoneProps {
  rows: RoomRow[];
  byName: Record<string, RoomRow>;
  colors: Record<string, string>;
  current: RoomRow;
  fa: FaState;
}

export function FloatingPhone({ rows, colors, current, fa }: PhoneProps) {
  if (fa.hidden) {
    return null;
  }
  if (fa.open) {
    return <PhoneSheet rows={rows} colors={colors} current={current} view={fa.view} />;
  }
  return <Bubble rows={rows} colors={colors} current={current} fa={fa} />;
}

function PhoneSheet({
  rows,
  colors,
  current,
  view,
}: {
  rows: RoomRow[];
  colors: Record<string, string>;
  current: RoomRow;
  view: FaState["view"];
}) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.defaultPrevented) {
        closePanel();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);
  return (
    <div className="fa-sheet-wrap" role="dialog" aria-modal="true" aria-label="Floating agent">
      <div className="fa-scrim" aria-hidden="true" onClick={closePanel} />
      <section className="fa-sheet">
        <span className="fa-sheet-handle" aria-hidden="true" />
        {view === "room" ? (
          <ChatRoom row={current} rows={rows} colors={colors} onClose={closePanel} />
        ) : (
          <ChatList rows={rows} colors={colors} current={current.name} onClose={closePanel} />
        )}
      </section>
    </div>
  );
}

function Bubble({
  rows,
  colors,
  current,
  fa,
}: {
  rows: RoomRow[];
  colors: Record<string, string>;
  current: RoomRow;
  fa: FaState;
}) {
  const { vw, vh } = useViewport();
  const stack = stackRooms(rows);
  const stacked = stack.length >= 2;
  const front: RoomRow = (stacked && stack[0]) || current;
  const [drag, setDrag] = useState<{ x: number; y: number } | null>(null);
  const [snapping, setSnapping] = useState(false);
  const [fanned, setFanned] = useState(false);
  const [idleKey, setIdleKey] = useState("");
  const [label, setLabel] = useState("");
  const startRef = useRef<{ px: number; py: number; x: number; y: number; moved: boolean } | null>(null);
  const rest = placeFor(fa.bubble, vw, vh);
  const pos = drag || rest;

  /* REQ-FA-041: fade after 3 s without a touch or an event. */
  const frontKey = front.name + "|" + (front.presence ? front.presence.state + front.presence.label : "") + "|" + front.unread;
  const activityKey = frontKey + "|" + (drag ? "drag" : "") + "|" + (fanned ? "fan" : "") + "|" + fa.bubble.side + fa.bubble.pct;
  const idle = idleKey === activityKey;
  useEffect(() => {
    const timer = window.setTimeout(() => setIdleKey(activityKey), IDLE_MS);
    return () => window.clearTimeout(timer);
  }, [activityKey]);

  /* A short label next to the bubble when the front room's state changes. */
  const lastStateRef = useRef("");
  useEffect(() => {
    const key = front.name + "|" + (front.presence ? front.presence.state : "");
    const before = lastStateRef.current;
    lastStateRef.current = key;
    if (!before || before === key) {
      return;
    }
    const text = stacked
      ? stack.length + " agents active" + (needsYou(front) ? " · " + displayLabel(front) + " needs you" : "")
      : displayLabel(front) + (front.presence && front.presence.label ? " · " + front.presence.label : "");
    setLabel(text);
    const timer = window.setTimeout(() => setLabel(""), LABEL_MS);
    return () => window.clearTimeout(timer);
    // stack is derived from the same rows as front.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [front.name, front.presence ? front.presence.state : ""]);

  const onDown = (event: ReactPointerEvent<HTMLButtonElement>) => {
    startRef.current = { px: event.clientX, py: event.clientY, x: pos.x, y: pos.y, moved: false };
    try {
      event.currentTarget.setPointerCapture(event.pointerId);
    } catch {
      /* A synthetic or already released pointer: dragging still works while
         the finger stays on the bubble. */
    }
  };
  const onMove = (event: ReactPointerEvent<HTMLButtonElement>) => {
    const start = startRef.current;
    if (!start) {
      return;
    }
    const dx = event.clientX - start.px;
    const dy = event.clientY - start.py;
    if (!start.moved && Math.hypot(dx, dy) < DRAG_SLOP) {
      return;
    }
    start.moved = true;
    setSnapping(false);
    setFanned(false);
    setDrag({ x: start.x + dx, y: start.y + dy });
  };
  const overHide = (x: number, y: number) =>
    Math.hypot(x + BUBBLE / 2 - vw / 2, y + BUBBLE / 2 - (vh - 70)) < HIDE_RADIUS;
  const onUp = () => {
    const start = startRef.current;
    startRef.current = null;
    if (!start) {
      return;
    }
    if (!start.moved) {
      onTap();
      return;
    }
    const at = drag || pos;
    setDrag(null);
    if (overHide(at.x, at.y)) {
      setHidden(true);
      return;
    }
    setSnapping(true);
    setBubble(snapFor(at.x, at.y, vw, vh));
    window.setTimeout(() => setSnapping(false), 360);
  };
  const onTap = () => {
    if (stacked) {
      setFanned((value) => !value);
      return;
    }
    clearNotif();
    openRoom(front.name);
  };

  const onLeft = pos.x + BUBBLE / 2 < vw / 2;
  const fanUp = pos.y + BUBBLE / 2 > vh / 2;
  const style: CSSProperties = { transform: `translate(${pos.x}px, ${pos.y}px)` };
  const behind = stacked ? stack.slice(1, 3) : [];
  return (
    <>
      {drag ? (
        <div className={"fa-hide-target" + (overHide(drag.x, drag.y) ? " on" : "")} aria-hidden="true">
          <span className="fa-hide-x">
            <Ic name="x" size={22} />
          </span>
          <span className="fa-hide-label">Drag here to hide</span>
        </div>
      ) : null}
      {fanned ? <div className="fa-fan-scrim" aria-hidden="true" onClick={() => setFanned(false)} /> : null}
      <div
        className={
          "fa-bubble-wrap" +
          (snapping ? " snapping" : "") +
          (drag ? " dragging" : "") +
          (idle && !drag && !fanned ? " idle" : "") +
          (onLeft ? " on-left" : " on-right") +
          (fanned ? " fanned" : "")
        }
        style={style}
      >
        {fanned ? (
          <div className={"fa-fan" + (fanUp ? " up" : " down")} role="menu" aria-label="Active agents">
            {stack.map((row) => (
              <button
                key={row.name}
                type="button"
                role="menuitem"
                className="fa-fan-item"
                onClick={() => {
                  setFanned(false);
                  openRoom(row.name);
                }}
              >
                <span className="fa-fan-label">
                  <span className="fa-name">{displayLabel(row)}</span>
                  <span className="fa-sub">
                    <StatusLine row={row} />
                  </span>
                </span>
                <span className="fa-orb">
                  <Face row={row} colors={colors} size={BUBBLE - 6} />
                  <Count row={row} />
                </span>
              </button>
            ))}
            <button
              type="button"
              role="menuitem"
              className="fa-fan-item is-all"
              onClick={() => {
                setFanned(false);
                showList();
              }}
            >
              <span className="fa-fan-label is-dark">All chats</span>
              <span className="fa-orb is-all">
                <Ic name="message-square" size={20} />
              </span>
            </button>
          </div>
        ) : null}
        {label && !drag && !fanned ? <span className="fa-bubble-label">{label}</span> : null}
        {behind
          .slice()
          .reverse()
          .map((row, index) => (
            <span key={row.name} className={"fa-orb fa-behind b" + (behind.length - index)} aria-hidden="true">
              <Face row={row} colors={colors} size={BUBBLE - 6} />
            </span>
          ))}
        <button
          type="button"
          className="fa-orb fa-bubble"
          aria-label={
            stacked
              ? stack.length + " agents active, show them"
              : "Open the chat with " + displayLabel(front)
          }
          aria-expanded={stacked ? fanned : undefined}
          onPointerDown={onDown}
          onPointerMove={onMove}
          onPointerUp={onUp}
          onPointerCancel={() => {
            startRef.current = null;
            setDrag(null);
          }}
        >
          <Face row={front} colors={colors} size={BUBBLE - 6} />
          <Count row={front} />
          {stacked ? <span className="fa-stack-count">{stack.length}</span> : null}
        </button>
      </div>
    </>
  );
}
