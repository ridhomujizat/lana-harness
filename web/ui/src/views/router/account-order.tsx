import { useId, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent, PointerEvent as ReactPointerEvent, ReactNode } from "react";
import type { PlanWindow } from "../../api/router";
import { Badge, Progress, Toggle } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import { planWindowLabel } from "./limits";
import { failureOf, parseTime } from "./router-common";

/* Spec 75 (REQ-PAF-006): the "Account order" list of a subscription pool
   (Claude Code, Antigravity) and the key list of an API provider. The top row
   is used first. A row moves by dragging its handle (native pointer events,
   a line shows where it lands) or with ↑/↓ on the focused handle, which is
   announced. Every move saves at once; a failed save puts the list back and
   shows the error. Rows with pool = false sit below a "Not used as backup"
   divider and can be dragged back above it. */

export interface OrderItem {
  /* The account or secret name; it is what the save sends. */
  id: string;
  label: string;
  /* A secret name or other code-like label. */
  mono?: boolean;
  /* The plan or tier. */
  detail?: string;
  badges?: ReactNode;
  windows?: PlanWindow[];
  coolingUntil?: string | null;
  /* false = not used by a pooled address (below the divider). */
  pool?: boolean;
}

export interface AccountOrderListProps {
  items: OrderItem[];
  /* The accessible name of the list, for example "Account order". */
  label: string;
  /* "account" rows have the "Use as backup" switch and the divider; "key"
     rows only move. */
  variant?: "account" | "key";
  disabled?: boolean;
  /* order = the used rows top to bottom; unpooled = the rest. Rejects to
     roll the list back. */
  onSave: (order: string[], unpooled: string[]) => Promise<unknown>;
  /* Extra buttons at the end of a row. */
  actions?: (item: OrderItem) => ReactNode;
}

type Zone = "pool" | "rest";

interface Entry {
  id: string;
  pool: boolean;
}

interface DropTarget {
  id: string;
  zone: Zone;
  slot: number;
}

interface Rect {
  id: string;
  pool: boolean;
  top: number;
  bottom: number;
}

export const NOT_USED_AS_BACKUP = "Not used as backup";

/* REQ-PAF-006: the terms note next to a pool of personal accounts. */
export const POOL_TERMS_NOTE =
  "Lana moves to the next account when one runs out. Using several personal accounts to get around plan limits can break the provider's terms.";

/* "Resting until 14:05" while the cooldown runs, else "". */
export function restingText(until: string | null | undefined, now: number = Date.now()): string {
  const at = parseTime(until || "");
  if (!at || at <= now) {
    return "";
  }
  const date = new Date(at);
  const pad = (value: number) => String(value).padStart(2, "0");
  return "Resting until " + pad(date.getHours()) + ":" + pad(date.getMinutes());
}

/* The fullest window of a row: its share used, for the mini bar. */
function fullestWindow(windows: PlanWindow[] | undefined): { window: PlanWindow; used: number } | null {
  let best: { window: PlanWindow; used: number } | null = null;
  for (const window of windows || []) {
    const reported = window.utilization !== null && window.utilization !== undefined;
    const used =
      window.status === "rejected" && !reported ? 1 : Math.max(0, Math.min(1, Number(window.utilization) || 0));
    if (!reported && window.status !== "rejected") {
      continue;
    }
    if (!best || used > best.used) {
      best = { window, used };
    }
  }
  return best;
}

function arrange(items: OrderItem[]): Entry[] {
  const pooled = items.filter((item) => item.pool !== false).map((item) => ({ id: item.id, pool: true }));
  const rest = items.filter((item) => item.pool === false).map((item) => ({ id: item.id, pool: false }));
  return pooled.concat(rest);
}

function signature(entries: Entry[]): string {
  return entries.map((entry) => entry.id + (entry.pool ? "+" : "-")).join("\n");
}

/* The list after moving id into zone at slot (an index among the zone's
   other rows). */
export function moveEntry(entries: Entry[], id: string, zone: Zone, slot: number): Entry[] {
  const others = entries.filter((entry) => entry.id !== id);
  const pooled = others.filter((entry) => entry.pool);
  const rest = others.filter((entry) => !entry.pool);
  const moved = { id, pool: zone === "pool" };
  const target = zone === "pool" ? pooled : rest;
  target.splice(Math.max(0, Math.min(slot, target.length)), 0, moved);
  return pooled.concat(rest);
}

export function AccountOrderList({
  items,
  label,
  variant = "account",
  disabled,
  onSave,
  actions,
}: AccountOrderListProps) {
  const hintId = useId();
  const listRef = useRef<HTMLOListElement | null>(null);
  const targetRef = useRef<DropTarget | null>(null);
  const poolSwitch = variant === "account";

  /* The order is local so a move shows at once; new props (a reload) reset
     it. */
  const incoming = arrange(items);
  const incomingSig = signature(incoming);
  const [synced, setSynced] = useState(incomingSig);
  const [entries, setEntries] = useState<Entry[]>(incoming);
  if (incomingSig !== synced) {
    setSynced(incomingSig);
    setEntries(incoming);
  }

  const [saving, setSaving] = useState(false);
  const [note, setNote] = useState<{ tone: "ok" | "error"; text: string } | null>(null);
  const [announce, setAnnounce] = useState("");
  const [drop, setDrop] = useState<DropTarget | null>(null);

  const byId: Record<string, OrderItem> = {};
  for (const item of items) {
    byId[item.id] = item;
  }
  const visible = entries.filter((entry) => byId[entry.id]);
  const pooled = visible.filter((entry) => entry.pool);
  const rest = visible.filter((entry) => !entry.pool);
  const nameOf = (id: string) => (byId[id] ? byId[id].label : id);
  const locked = Boolean(disabled) || saving;

  async function commit(next: Entry[], text: string) {
    if (signature(next) === signature(entries)) {
      return;
    }
    const previous = entries;
    setEntries(next);
    setAnnounce(text);
    setNote(null);
    setSaving(true);
    try {
      await onSave(
        next.filter((entry) => entry.pool).map((entry) => entry.id),
        next.filter((entry) => !entry.pool).map((entry) => entry.id)
      );
      setNote({ tone: "ok", text: "Saved" });
    } catch (failure) {
      setEntries(previous);
      const message = failureOf(failure).message;
      setNote({ tone: "error", text: "Could not save the order: " + message });
      setAnnounce("Could not save the order. It is back as it was.");
    } finally {
      setSaving(false);
    }
  }

  function moveText(id: string, zone: Zone, slot: number, next: Entry[]): string {
    if (zone === "rest") {
      return nameOf(id) + " is not used as backup";
    }
    const total = next.filter((entry) => entry.pool).length;
    return nameOf(id) + " moved to position " + (slot + 1) + " of " + total;
  }

  function move(id: string, zone: Zone, slot: number) {
    const next = moveEntry(entries, id, zone, slot);
    void commit(next, moveText(id, zone, slot, next));
  }

  function setPool(id: string, on: boolean) {
    const others = entries.filter((entry) => entry.id !== id);
    if (on) {
      const slot = others.filter((entry) => entry.pool).length;
      const next = moveEntry(entries, id, "pool", slot);
      const total = next.filter((entry) => entry.pool).length;
      void commit(next, nameOf(id) + " is used as backup, position " + total + " of " + total);
    } else {
      void commit(moveEntry(entries, id, "rest", 0), nameOf(id) + " is not used as backup");
    }
  }

  function onKeyDown(event: ReactKeyboardEvent<HTMLButtonElement>, entry: Entry) {
    if (event.key !== "ArrowUp" && event.key !== "ArrowDown") {
      return;
    }
    event.preventDefault();
    if (locked) {
      return;
    }
    const zone: Zone = entry.pool ? "pool" : "rest";
    const group = entry.pool ? pooled : rest;
    const index = group.findIndex((item) => item.id === entry.id);
    const slot = index + (event.key === "ArrowUp" ? -1 : 1);
    if (index < 0 || slot < 0 || slot >= group.length) {
      return;
    }
    move(entry.id, zone, slot);
  }

  /* Pointer drag: the rows' boxes are read once at the start; the drop slot
     follows the pointer's height against the rows' middles. */
  function onPointerDown(event: ReactPointerEvent<HTMLButtonElement>, entry: Entry) {
    if (locked || event.button !== 0) {
      return;
    }
    const list = listRef.current;
    if (!list) {
      return;
    }
    event.preventDefault();
    const handle = event.currentTarget;
    try {
      handle.setPointerCapture(event.pointerId);
    } catch {
      /* Synthetic events have no active pointer. */
    }
    const rects: Rect[] = [];
    list.querySelectorAll<HTMLElement>("[data-order-id]").forEach((row) => {
      const id = row.dataset.orderId || "";
      if (id === entry.id) {
        return;
      }
      const box = row.getBoundingClientRect();
      rects.push({ id, pool: row.dataset.orderPool === "1", top: box.top, bottom: box.bottom });
    });
    const divider = list.querySelector<HTMLElement>("[data-order-divider]");
    const dividerBox = divider ? divider.getBoundingClientRect() : null;
    const dividerMid = dividerBox ? (dividerBox.top + dividerBox.bottom) / 2 : Infinity;

    const zone0: Zone = entry.pool ? "pool" : "rest";
    const start = (entry.pool ? pooled : rest).findIndex((item) => item.id === entry.id);
    const first: DropTarget = { id: entry.id, zone: zone0, slot: start };
    targetRef.current = first;
    setDrop(first);

    const moveTo = (y: number) => {
      const zone: Zone = poolSwitch && y > dividerMid ? "rest" : "pool";
      const zoneRows = rects.filter((rect) => (zone === "pool" ? rect.pool : !rect.pool) || !poolSwitch);
      const slot = zoneRows.filter((rect) => (rect.top + rect.bottom) / 2 < y).length;
      const target = { id: entry.id, zone, slot };
      const current = targetRef.current;
      if (!current || current.zone !== zone || current.slot !== slot) {
        targetRef.current = target;
        setDrop(target);
      }
    };
    const onMove = (ev: PointerEvent) => moveTo(ev.clientY);
    const finish = (apply: boolean) => {
      handle.removeEventListener("pointermove", onMove);
      handle.removeEventListener("pointerup", onUp);
      handle.removeEventListener("pointercancel", onCancel);
      const target = targetRef.current;
      targetRef.current = null;
      setDrop(null);
      if (apply && target) {
        move(target.id, target.zone, target.slot);
      }
    };
    const onUp = () => finish(true);
    const onCancel = () => finish(false);
    handle.addEventListener("pointermove", onMove);
    handle.addEventListener("pointerup", onUp);
    handle.addEventListener("pointercancel", onCancel);
  }

  /* Where the drop line shows: before a row of the target zone, after its
     last row, or at the divider when the zone has no other row. */
  function dropClass(entry: Entry): string {
    if (!drop || entry.id === drop.id) {
      return "";
    }
    const zone: Zone = entry.pool || !poolSwitch ? "pool" : "rest";
    if (zone !== drop.zone) {
      return "";
    }
    const zoneRows = (zone === "pool" ? pooled : rest).filter((item) => item.id !== drop.id);
    const index = zoneRows.findIndex((item) => item.id === entry.id);
    if (index === drop.slot) {
      return " is-drop-before";
    }
    if (index === zoneRows.length - 1 && drop.slot === zoneRows.length) {
      return " is-drop-after";
    }
    return "";
  }

  function dividerClass(): string {
    if (!drop) {
      return "";
    }
    const others = (drop.zone === "pool" ? pooled : rest).filter((item) => item.id !== drop.id);
    if (others.length > 0) {
      return "";
    }
    return drop.zone === "pool" ? " is-drop-before" : " is-drop-after";
  }

  /* pos is the row's place on screen, the divider counted. */
  const renderRow = (entry: Entry, pos: number) => {
    const item = byId[entry.id] as OrderItem;
    const fullest = fullestWindow(item.windows);
    const resting = restingText(item.coolingUntil);
    const first = entry.pool && pos === 0;
    return (
      <li
        key={entry.id}
        className={
          "rt-order-row" +
          (entry.pool ? "" : " is-unpooled") +
          (drop && drop.id === entry.id ? " is-dragging" : "") +
          dropClass(entry)
        }
        data-order-id={entry.id}
        data-order-pool={entry.pool ? "1" : "0"}
        data-order-pos={pos}
        aria-label={item.label}
      >
        <button
          type="button"
          className="rt-order-grip"
          aria-label={"Move " + item.label}
          aria-describedby={hintId}
          disabled={locked}
          onPointerDown={(event) => onPointerDown(event, entry)}
          onKeyDown={(event) => onKeyDown(event, entry)}
        >
          <Ic name="grip-vertical" size={16} />
        </button>
        <span className="rt-order-id">
          <span className="rt-order-name">
            <span className={"trunc" + (item.mono ? " mono" : "")} title={item.label}>
              {item.label}
            </span>
            {first ? <Badge tone="accent">Used first</Badge> : null}
          </span>
          <span className="rt-order-sub">
            {item.detail ? <span className="trunc">{item.detail}</span> : null}
            {item.badges}
            {resting ? (
              <span className="rt-order-rest">
                <Ic name="moon" size={12} /> {resting}
              </span>
            ) : null}
          </span>
        </span>
        {fullest ? (
          <span className="rt-order-bar" title={planWindowLabel(fullest.window)}>
            <Progress
              value={fullest.used}
              tone={fullest.used >= 0.9 ? "red" : fullest.used >= 0.7 ? "amber" : undefined}
              aria-label={item.label + " · " + planWindowLabel(fullest.window)}
            />
            <span className="rt-order-bar-t num">{Math.round(fullest.used * 100)} %</span>
          </span>
        ) : null}
        <span className="rt-order-acts">
          {poolSwitch ? (
            <label className="rt-order-pool">
              <Toggle
                aria-label={"Use " + item.label + " as backup"}
                checked={entry.pool}
                disabled={locked}
                onChange={(on) => setPool(entry.id, on)}
              />
              <span className="hint">Use as backup</span>
            </label>
          ) : null}
          {actions ? actions(item) : null}
        </span>
      </li>
    );
  };

  return (
    <div className="rt-order">
      <p id={hintId} className="hint rt-order-hint">
        The top {variant === "key" ? "key" : "account"} is used first. Drag a row, or focus its handle and press ↑ or ↓.
      </p>
      <ol className="rt-order-list" ref={listRef} aria-label={label}>
        {pooled.map((entry, index) => renderRow(entry, index))}
        {poolSwitch && rest.length > 0 ? (
          <li className={"rt-order-divider" + dividerClass()} data-order-divider="" data-order-pos={pooled.length} role="presentation">
            <span>{NOT_USED_AS_BACKUP}</span>
          </li>
        ) : null}
        {rest.map((entry, index) => renderRow(entry, pooled.length + index + (poolSwitch ? 1 : 0)))}
      </ol>
      <p className="sr" aria-live="polite">
        {announce}
      </p>
      {saving ? (
        <p className="hint rt-order-note" role="status">
          Saving…
        </p>
      ) : note ? (
        <p className={"rt-order-note" + (note.tone === "error" ? " is-error" : "")} role={note.tone === "error" ? "alert" : "status"}>
          <Ic name={note.tone === "error" ? "circle-alert" : "circle-check"} size={12} /> {note.text}
        </p>
      ) : null}
    </div>
  );
}
