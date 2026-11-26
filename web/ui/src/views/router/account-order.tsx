import { useId, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent, PointerEvent as ReactPointerEvent, ReactNode } from "react";
import type { PlanWindow } from "../../api/router";
import { Badge, IconButton, Progress, Toggle } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import { planWindowLabel } from "./limits";
import { failureOf, parseTime, resetsIn } from "./router-common";

/* Spec 75 (REQ-PAF-006): the "Account order" list of a subscription pool
   (Claude Code, Antigravity) and the key list of an API provider. The top row
   is used first. A row moves by dragging its handle (native pointer events,
   a line shows where it lands) or with ↑/↓ on the focused handle, which is
   announced. Every move saves at once; a failed save puts the list back and
   shows the error. Rows with pool = false sit below a "Not used as backup"
   divider and can be dragged back above it.

   Spec 77 revision 4: on the Claude Code and Antigravity pages this is the
   one accounts list. A row then also carries the account's logo, name line,
   sign-in progress (always shown), and its settings, which open under the
   row with the chevron; there is no separate account card. */

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
  /* Rich rows (spec 77 revision 4). The row's accessible name (default
     label). */
  rowLabel?: string;
  /* Extra classes of the row, for example "is-off" or "is-new". */
  rowClass?: string;
  /* Before the name, for example the provider logo. */
  lead?: ReactNode;
  /* In place of the label text: the name with its rename button or form. */
  name?: ReactNode;
  /* In place of detail: the line under the name. */
  sub?: ReactNode;
  /* Buttons of this row before the details chevron (Sign in). */
  rowActions?: ReactNode;
  /* Always shown under the row line: sign-in progress and errors. */
  below?: ReactNode;
  /* The row's settings, shown while the row is open. */
  details?: ReactNode;
  /* Open until the owner toggles it. */
  open?: boolean;
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
  /* false: no handle, no "Use as backup" switch and no hint (a list of one
     account has no order). */
  sortable?: boolean;
  /* What a row is in the hint, default "key" or "account" by variant (an
     API provider's connections say "connection", spec 80). */
  noun?: string;
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

/* The reported windows of a row with their share used (a rejected window
   without a figure counts as full), for the usage bars under the name. */
export function usageRows(windows: PlanWindow[] | undefined): { window: PlanWindow; used: number }[] {
  const rows: { window: PlanWindow; used: number }[] = [];
  for (const window of windows || []) {
    const reported = window.utilization !== null && window.utilization !== undefined;
    if (!reported && window.status !== "rejected") {
      continue;
    }
    const used =
      window.status === "rejected" && !reported ? 1 : Math.max(0, Math.min(1, Number(window.utilization) || 0));
    rows.push({ window, used });
  }
  return rows;
}

/* "5h" and "7d" for the two plan windows, else the window's own name. */
function usageLabel(window: PlanWindow): string {
  if (window.type === "five_hour") return "5h";
  if (window.type === "seven_day") return "7d";
  return planWindowLabel(window);
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
  sortable = true,
  noun,
}: AccountOrderListProps) {
  const hintId = useId();
  const moreId = useId();
  const listRef = useRef<HTMLOListElement | null>(null);
  const targetRef = useRef<DropTarget | null>(null);
  const poolSwitch = variant === "account" && sortable;
  /* Rows the owner opened or closed; the rest follow item.open. */
  const [opened, setOpened] = useState<Record<string, boolean>>({});

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
    const usage = usageRows(item.windows);
    const resting = restingText(item.coolingUntil);
    const first = sortable && entry.pool && pos === 0;
    const hasDetails = item.details !== undefined && item.details !== null && item.details !== false;
    const open = hasDetails && (opened[entry.id] ?? Boolean(item.open));
    const panelId = moreId + "-" + pos;
    return (
      <li
        key={entry.id}
        className={
          "rt-order-row" +
          (sortable ? " is-sortable" : "") +
          (item.lead ? " has-lead" : "") +
          (open ? " is-open" : "") +
          (entry.pool ? "" : " is-unpooled") +
          (drop && drop.id === entry.id ? " is-dragging" : "") +
          dropClass(entry) +
          (item.rowClass ? " " + item.rowClass : "")
        }
        data-order-id={entry.id}
        data-order-pool={entry.pool ? "1" : "0"}
        data-order-pos={pos}
        aria-label={item.rowLabel || item.label}
      >
        <div className="rt-order-line">
          {sortable ? (
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
          ) : null}
          {item.lead ? <span className="rt-order-lead">{item.lead}</span> : null}
          <span className="rt-order-id">
            <span className="rt-order-name">
              {item.name !== undefined ? (
                item.name
              ) : (
                <span className={"trunc" + (item.mono ? " mono" : "")} title={item.label}>
                  {item.label}
                </span>
              )}
              {first ? <Badge tone="accent">Used first</Badge> : null}
            </span>
            <span className="rt-order-sub">
              {item.sub !== undefined ? item.sub : item.detail ? <span className="trunc">{item.detail}</span> : null}
              {item.badges}
              {resting ? (
                <span className="rt-order-rest">
                  <Ic name="moon" size={12} /> {resting}
                </span>
              ) : null}
            </span>
            {usage.length ? (
              <span className="rt-order-usage">
                {usage.map(({ window, used }, index) => (
                  <span key={window.type + ":" + (window.label || "") + ":" + index} className="rt-order-use" title={planWindowLabel(window)}>
                    <span className="rt-order-use-l">{usageLabel(window)}</span>
                    <Progress
                      value={used}
                      tone={used >= 0.9 ? "red" : used >= 0.7 ? "amber" : undefined}
                      aria-label={item.label + " · " + planWindowLabel(window)}
                    />
                    <span className="rt-order-use-p num">{Math.round(used * 100)}%</span>
                    <span className="rt-order-use-r">{resetsIn(window.resets_at).replace(/^resets in /, "")}</span>
                  </span>
                ))}
              </span>
            ) : null}
          </span>
          <span className="rt-order-acts">
            {item.rowActions}
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
            {hasDetails ? (
              <IconButton
                icon="chevron-down"
                size="sm"
                className={"rt-order-more-btn" + (open ? " is-open" : "")}
                label={"Details of " + item.id}
                aria-expanded={open}
                aria-controls={open ? panelId : undefined}
                onClick={() => setOpened((current) => ({ ...current, [entry.id]: !open }))}
              />
            ) : null}
          </span>
        </div>
        {item.below ? <div className="rt-order-below">{item.below}</div> : null}
        {open ? (
          <div className="rt-order-more" id={panelId} role="region" aria-label={"Details of " + item.id}>
            {item.details}
          </div>
        ) : null}
      </li>
    );
  };

  return (
    <div className="rt-order">
      {sortable ? (
        <p id={hintId} className="hint rt-order-hint">
          The top {noun || (variant === "key" ? "key" : "account")} is used first. Drag a row, or focus its handle and press ↑ or ↓.
        </p>
      ) : null}
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
