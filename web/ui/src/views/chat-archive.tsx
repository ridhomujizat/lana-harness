import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { PointerEvent as ReactPointerEvent } from "react";
import type { RoomRow } from "../api/rooms";
import {
  Badge,
  Button,
  EmptyState,
  MenuItem,
  MenuSeparator,
  Modal,
  SearchInput,
} from "../ui/gr8r";
import { Ic } from "../ui/icons";
import { useSheetDrag } from "../ui/sheet-drag";
import { displayLabel } from "./agent-label";
import { PresenceLabel } from "./presence";
import { RoomListRow, RowFace } from "./room-list-row";

/* Spec 85: the archive and clear pieces of the Chats UI. Kept apart from
   chats.tsx so the page stays readable: the list toast, the Archived launcher
   and screen, the clear dialog, and the phone row's swipe and bottom sheet. */

/* ---------------------------------------------------------------- */
/* List toast (REQ-ARC-007, REQ-ARC-008, REQ-ARC-011)               */
/* ---------------------------------------------------------------- */

export interface ListToastState {
  message: string;
  undo?: () => void;
}

/* A toast pinned to the bottom of the list column, with an Undo that lasts
   6 s (REQ-ARC-007). */
export function useListToast(): {
  toast: ListToastState | null;
  show: (message: string, undo?: () => void) => void;
  dismiss: () => void;
} {
  const [toast, setToast] = useState<ListToastState | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const clear = useCallback(() => {
    if (timer.current !== null) {
      clearTimeout(timer.current);
      timer.current = null;
    }
  }, []);
  useEffect(() => clear, [clear]);
  const show = useCallback(
    (message: string, undo?: () => void) => {
      clear();
      setToast({ message, undo });
      timer.current = setTimeout(() => {
        timer.current = null;
        setToast(null);
      }, 6000);
    },
    [clear]
  );
  const dismiss = useCallback(() => {
    clear();
    setToast(null);
  }, [clear]);
  return { toast, show, dismiss };
}

export function ListToast({
  toast,
  onClose,
}: {
  toast: ListToastState;
  onClose: () => void;
}) {
  return (
    <div className="list-toast" role="status">
      <span className="list-toast-text">{toast.message}</span>
      {toast.undo ? (
        <button
          type="button"
          className="list-toast-undo"
          onClick={() => {
            toast.undo?.();
            onClose();
          }}
        >
          <Ic name="undo-2" size={14} />
          Undo
        </button>
      ) : null}
    </div>
  );
}

/* ---------------------------------------------------------------- */
/* Row menu (REQ-ARC-007)                                           */
/* ---------------------------------------------------------------- */

/* The ⋯ menu of a live row: Archive (not for lana), Clear, View profile. */
export function ChatMenuItems({
  row,
  onArchive,
  onClear,
}: {
  row: RoomRow;
  onArchive: () => void;
  onClear: () => void;
}) {
  return (
    <>
      {row.name === "lana" ? null : (
        <MenuItem icon="archive" onClick={onArchive}>
          Archive chat
        </MenuItem>
      )}
      <MenuItem icon="eraser" onClick={onClear}>
        Clear chat…
      </MenuItem>
      <MenuSeparator />
      <MenuItem
        icon="user-round"
        onClick={() => {
          window.location.hash = "#/agents/" + encodeURIComponent(row.name);
        }}
      >
        View profile
      </MenuItem>
    </>
  );
}

/* ---------------------------------------------------------------- */
/* Archived launcher row (REQ-ARC-007)                              */
/* ---------------------------------------------------------------- */

/* The rule and "Archived" row at the end of the list; opens #/chats/archived. */
export function ArchivedLauncher({
  count,
  names,
  unread,
}: {
  count: number;
  names: string;
  unread: number;
}) {
  return (
    <div className="archived-block">
      <span className="archived-rule" aria-hidden="true" />
      <a className="archived-row" href="#/chats/archived">
        <span className="archived-tile" aria-hidden="true">
          <Ic name="archive" size={20} />
        </span>
        <span className="rooms-row-body">
          <span className="rooms-row-top">
            <span className="rooms-row-name">Archived</span>
            <span className="rooms-row-time">{count}</span>
          </span>
          <span className="rooms-row-title truncated-names">{names}</span>
        </span>
        {unread > 0 ? (
          <Badge className="rooms-row-badge" tone="accent" aria-label={unread + " unread"}>
            {unread > 99 ? "99+" : unread}
          </Badge>
        ) : null}
        <Ic name="chevron-right" size={16} className="archived-chevron" />
      </a>
    </div>
  );
}

/* ---------------------------------------------------------------- */
/* Archived screen (REQ-ARC-008)                                    */
/* ---------------------------------------------------------------- */

export function ArchivedChats({
  rows,
  colors,
  onUnarchive,
}: {
  rows: RoomRow[];
  colors: Record<string, string>;
  onUnarchive: (row: RoomRow) => void;
}) {
  const [query, setQuery] = useState("");
  const visible = useMemo(() => {
    const text = query.trim().toLowerCase();
    if (!text) {
      return rows;
    }
    return rows.filter(
      (row) =>
        row.name.toLowerCase().includes(text) ||
        displayLabel(row).toLowerCase().includes(text) ||
        (row.title || "").toLowerCase().includes(text)
    );
  }, [rows, query]);
  return (
    <div className="archived-view">
      <header className="rooms-list-head archived-head">
        <a className="ibtn ibtn-sm archived-back" href="#/chats" aria-label="All chats">
          <Ic name="chevron-left" size={18} />
        </a>
        <h1 className="rooms-list-title">Archived</h1>
      </header>
      <SearchInput
        wrapClassName="rooms-search"
        placeholder="Search archived chats"
        aria-label="Search archived chats"
        autoComplete="off"
        spellCheck={false}
        kbd={null}
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />
      <div className="rooms-list-body">
        {visible.length === 0 ? (
          <EmptyState size="sm" icon="archive" title="No archived chats" />
        ) : (
          <ul className="rooms-rows">
            {visible.map((row) => (
              <li key={row.name}>
                <RoomListRow
                  row={row}
                  colors={colors}
                  href={"#/chats/" + encodeURIComponent(row.name)}
                  onUnarchive={() => onUnarchive(row)}
                />
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

/* ---------------------------------------------------------------- */
/* Clear dialog (REQ-ARC-010)                                       */
/* ---------------------------------------------------------------- */

export function ClearChatDialog({
  label,
  working,
  busy,
  onCancel,
  onConfirm,
}: {
  label: string;
  working: boolean;
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <Modal
      title={
        <span className="clear-chat-title">
          <span className="clear-chat-tile" aria-hidden="true">
            <Ic name="eraser" size={18} />
          </span>
          {"Clear chat with " + label + "?"}
        </span>
      }
      onClose={onCancel}
      className="clear-chat-dialog"
      footer={
        <>
          <span className="sp" />
          <Button variant="secondary" onClick={onCancel}>
            Cancel
          </Button>
          <Button variant="danger" loading={busy} onClick={onConfirm}>
            Clear chat
          </Button>
        </>
      }
    >
      <p className="clear-chat-text">
        {"Every message in this chat leaves the timeline. " +
          label +
          " answers the next message with an empty context."}
      </p>
      <div className="clear-chat-stays">
        <span className="clear-chat-stays-label">Stays</span>
        <ul className="clear-chat-list">
          <li>
            <Ic name="check" size={14} />
            {label + "'s memory, knowledge and files"}
          </li>
          <li>
            <Ic name="check" size={14} />
            Schedules, workflows and channels
          </li>
          <li>
            <Ic name="check" size={14} />
            Old sessions, readable in History
          </li>
        </ul>
      </div>
      {working ? (
        <p className="clear-chat-working">
          <Ic name="info" size={14} />
          {label + " is working. Clearing stops the turn first."}
        </p>
      ) : null}
    </Modal>
  );
}

/* ---------------------------------------------------------------- */
/* Phone: swipe actions and bottom sheet (REQ-ARC-013)              */
/* ---------------------------------------------------------------- */

function useIsPhone(): boolean {
  const [phone, setPhone] = useState(
    () => typeof window.matchMedia === "function" && window.matchMedia("(max-width: 760px)").matches
  );
  useEffect(() => {
    if (typeof window.matchMedia !== "function") {
      return undefined;
    }
    const query = window.matchMedia("(max-width: 760px)");
    const update = () => setPhone(query.matches);
    update();
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);
  return phone;
}

export { useIsPhone };

/* Long-press (~500 ms) opens the sheet; swiping left reveals More and Archive
   (or Unarchive in the archived view). Lana's row shows only More. */
export function PhoneChatRow({
  row,
  colors,
  active,
  href,
  archivedView,
  onArchive,
  onClear,
  onUnarchive,
}: {
  row: RoomRow;
  colors: Record<string, string>;
  active?: boolean;
  href: string;
  archivedView?: boolean;
  onArchive: () => void;
  onClear: () => void;
  onUnarchive?: () => void;
}) {
  const [reveal, setReveal] = useState(false);
  const [sheet, setSheet] = useState(false);
  const start = useRef<{ x: number; y: number; time: number } | null>(null);
  const longPress = useRef<ReturnType<typeof setTimeout> | null>(null);
  const suppress = useRef(false);
  const archiveLabel = archivedView ? "Unarchive" : "Archive";
  const showArchive = archivedView || row.name !== "lana";

  const cancelLongPress = useCallback(() => {
    if (longPress.current !== null) {
      clearTimeout(longPress.current);
      longPress.current = null;
    }
  }, []);
  useEffect(() => cancelLongPress, [cancelLongPress]);

  function onDown(event: ReactPointerEvent<HTMLDivElement>) {
    start.current = { x: event.clientX, y: event.clientY, time: Date.now() };
    suppress.current = false;
    cancelLongPress();
    longPress.current = setTimeout(() => {
      longPress.current = null;
      suppress.current = true;
      setSheet(true);
    }, 500);
  }
  function onMove(event: ReactPointerEvent<HTMLDivElement>) {
    const from = start.current;
    if (!from) {
      return;
    }
    const dx = event.clientX - from.x;
    const dy = event.clientY - from.y;
    if (Math.hypot(dx, dy) > 6) {
      cancelLongPress();
    }
    if (dx < -30 && Math.abs(dx) > Math.abs(dy)) {
      setReveal(true);
    } else if (dx > 30) {
      setReveal(false);
    }
  }
  function onUp() {
    cancelLongPress();
    start.current = null;
  }

  return (
    <div className={"phone-row" + (reveal ? " is-revealed" : "")}>
      <div
        className="phone-row-track"
        onPointerDown={onDown}
        onPointerMove={onMove}
        onPointerUp={onUp}
        onPointerCancel={onUp}
        onClickCapture={(event) => {
          if (suppress.current) {
            suppress.current = false;
            event.preventDefault();
            event.stopPropagation();
          }
        }}
      >
        <RoomListRow row={row} colors={colors} active={active} href={href} />
      </div>
      <div className="phone-row-actions" aria-hidden={!reveal}>
        <button type="button" className="phone-act" onClick={() => setSheet(true)}>
          More
        </button>
        {showArchive ? (
          <button
            type="button"
            className="phone-act is-accent"
            onClick={() => {
              if (archivedView) {
                onUnarchive?.();
              } else {
                onArchive();
              }
            }}
          >
            {archiveLabel}
          </button>
        ) : null}
      </div>
      {sheet ? (
        <ChatSheet
          row={row}
          colors={colors}
          archivedView={Boolean(archivedView)}
          onArchive={onArchive}
          onClear={onClear}
          onUnarchive={onUnarchive}
          onClose={() => setSheet(false)}
        />
      ) : null}
    </div>
  );
}

function ChatSheet({
  row,
  colors,
  archivedView,
  onArchive,
  onClear,
  onUnarchive,
  onClose,
}: {
  row: RoomRow;
  colors: Record<string, string>;
  archivedView: boolean;
  onArchive: () => void;
  onClear: () => void;
  onUnarchive?: () => void;
  onClose: () => void;
}) {
  const { attach, full, gripProps } = useSheetDrag(onClose);
  const label = displayLabel(row);
  return (
    <div className="chat-sheet-wrap" role="dialog" aria-modal="true" aria-label={label}>
      <div className="chat-sheet-scrim" aria-hidden="true" onClick={onClose} />
      <section className={"chat-sheet" + (full ? " is-full" : "")} ref={attach}>
        <span className="chat-sheet-grip sheet-grip" {...gripProps} />
        <div className="chat-sheet-head">
          <RowFace row={row} colors={colors} still />
          <span className="chat-sheet-who">
            <span className="chat-sheet-name">{label}</span>
            {row.presence ? (
              <PresenceLabel presence={row.presence} />
            ) : (
              <span className="muted">{row.title || "Agent"}</span>
            )}
          </span>
        </div>
        <div className="chat-sheet-items">
          {archivedView ? (
            <button
              type="button"
              className="chat-sheet-item"
              onClick={() => {
                onUnarchive?.();
                onClose();
              }}
            >
              <Ic name="archive-restore" size={17} />
              <span className="chat-sheet-item-text">
                <span className="chat-sheet-item-title">Unarchive chat</span>
                <span className="chat-sheet-item-sub">Show it in Chats again</span>
              </span>
            </button>
          ) : row.name === "lana" ? null : (
            <button
              type="button"
              className="chat-sheet-item"
              onClick={() => {
                onArchive();
                onClose();
              }}
            >
              <Ic name="archive" size={17} />
              <span className="chat-sheet-item-text">
                <span className="chat-sheet-item-title">Archive chat</span>
                <span className="chat-sheet-item-sub">Hide it from Chats</span>
              </span>
            </button>
          )}
          <button
            type="button"
            className="chat-sheet-item"
            onClick={() => {
              onClear();
              onClose();
            }}
          >
            <Ic name="eraser" size={17} />
            <span className="chat-sheet-item-text">
              <span className="chat-sheet-item-title">Clear chat…</span>
              <span className="chat-sheet-item-sub">Remove every message</span>
            </span>
          </button>
          <button
            type="button"
            className="chat-sheet-item"
            onClick={() => {
              onClose();
              window.location.hash = "#/agents/" + encodeURIComponent(row.name);
            }}
          >
            <Ic name="user-round" size={17} />
            <span className="chat-sheet-item-text">
              <span className="chat-sheet-item-title">View profile</span>
            </span>
          </button>
        </div>
      </section>
    </div>
  );
}
