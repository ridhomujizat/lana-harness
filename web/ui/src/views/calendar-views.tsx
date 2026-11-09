import { useEffect, useRef } from "react";
import type { CSSProperties, DragEvent, MouseEvent as ReactMouseEvent } from "react";
import type { Entry } from "../api/calendar";
import { Ic } from "../ui/icons";
import { cn } from "../ui/gr8r";
import { AgentFace } from "./workflow-common";
import {
  WEEKDAY_SHORT,
  clockText,
  dayKey,
  dayTitle,
  groupWorkflowRuns,
  isTimed,
  layoutDayAdaptive,
  minuteOfY,
  sameDay,
  startOfDay,
  yOfMinute,
} from "./calendar-model";
import type { ViewRange } from "./calendar-model";

/* The four calendar views of REQ-CAL-010 and the entry chips they share. The
   month grid and week columns follow gr8r-studio's calendar view. */

export const DRAG_TYPE = "application/x-lana-calendar";

/* Matches the phone breakpoint of calendar.css. */
export function isPhone(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(max-width: 760px)").matches;
}

const COLORS: Record<string, string> = {
  gray: "var(--gray)",
  indigo: "var(--accent)",
  blue: "var(--blue)",
  teal: "var(--teal)",
  green: "var(--green)",
  amber: "var(--amber)",
  rose: "var(--rose)",
  violet: "var(--violet)",
  red: "var(--red)",
};

const KIND_COLOR: Record<string, string> = {
  event: "blue",
  task: "violet",
  reminder: "amber",
  note: "gray",
  habit: "green",
  workflow: "indigo",
};

const RUN_COLOR: Record<string, string> = {
  succeeded: "green",
  failed: "red",
  cancelled: "gray",
  running: "blue",
  queued: "blue",
  waiting: "amber",
};

export function entryColor(entry: Entry): string {
  if (entry.source === "workflow_run") {
    return COLORS[RUN_COLOR[entry.status] || "gray"]!;
  }
  if (entry.source === "google" && entry.google?.color) {
    return entry.google.color;
  }
  if (entry.firing && (entry.firing.status === "missed" || entry.firing.status === "failed")) {
    return COLORS.red!;
  }
  return COLORS[entry.color] || COLORS[KIND_COLOR[entry.kind] || "gray"]!;
}

const RUN_ICON: Record<string, string> = {
  succeeded: "circle-check",
  failed: "circle-x",
  cancelled: "circle-slash",
  running: "loader",
  queued: "clock",
  waiting: "circle-pause",
};

export function entryIcon(entry: Entry): string {
  if (entry.firing && (entry.firing.status === "missed" || entry.firing.status === "failed")) {
    return "triangle-alert";
  }
  switch (entry.source) {
    case "workflow_run":
      return RUN_ICON[entry.status] || "circle-dot";
    case "workflow_slot":
      return entry.moved ? "move" : entry.workflow?.every_minutes ? "timer" : "workflow";
    case "google":
      return "calendar";
  }
  switch (entry.kind) {
    case "task":
      return entry.status === "done" ? "square-check" : "square";
    case "reminder":
      return "bell";
    case "note":
      return "sticky-note";
    case "habit":
      return entry.habit?.done ? "circle-check" : "repeat";
    default:
      return "calendar";
  }
}

export function entryWhen(entry: Entry): string {
  if (entry.workflow?.every_minutes) {
    return "Every " + entry.workflow.every_minutes + " min";
  }
  if (!isTimed(entry)) {
    return "All day";
  }
  return clockText(entry.start) + (entry.end > entry.start ? "–" + clockText(entry.end) : "");
}

export function isDone(entry: Entry): boolean {
  return entry.status === "done" || entry.status === "skipped" || Boolean(entry.habit?.done);
}

export function canDrag(entry: Entry, fallback: boolean): boolean {
  if (fallback || entry.readonly) {
    return false;
  }
  /* Google events not linked to an item are shown, not edited, here. */
  return entry.source === "item" || entry.source === "workflow_slot";
}

function agentOf(entry: Entry): string {
  if (entry.assignee && entry.assignee !== "owner") {
    return entry.assignee;
  }
  return entry.created_by.startsWith("agent:") ? entry.created_by.slice(6) : "";
}

export function entryLabel(entry: Entry): string {
  const parts = [entry.title, entryWhen(entry)];
  if (entry.source === "workflow_slot") {
    parts.push(entry.status === "skipped" ? "workflow slot, skipped" : entry.moved ? "workflow slot, moved" : "workflow slot");
  } else if (entry.source === "workflow_run") {
    parts.push("workflow run " + entry.status);
  } else if (entry.source === "google") {
    parts.push("Google event");
  } else {
    parts.push(entry.kind + (entry.status ? ", " + entry.status : ""));
  }
  if (entry.firing && entry.firing.status === "missed") {
    parts.push("missed");
  }
  return parts.join(" · ");
}

export interface EntryHandlers {
  onOpen: (entry: Entry, anchor: HTMLElement) => void;
  fallback: boolean;
}

function dragStart(event: DragEvent<HTMLElement>, entry: Entry) {
  event.dataTransfer.setData(DRAG_TYPE, entry.key);
  event.dataTransfer.setData("text/plain", entry.title);
  event.dataTransfer.effectAllowed = "move";
}

export function EntryChip({ entry, onOpen, fallback, showTime = true }: EntryHandlers & { entry: Entry; showTime?: boolean }) {
  const agent = agentOf(entry);
  const draggable = canDrag(entry, fallback);
  return (
    <button
      type="button"
      className={cn(
        "cev",
        entry.source === "workflow_slot" && "event",
        entry.source === "workflow_run" && "run",
        isDone(entry) && "done",
        entry.firing?.status === "missed" && "missed"
      )}
      style={{ "--c": entryColor(entry) } as CSSProperties}
      draggable={draggable}
      onDragStart={draggable ? (event) => dragStart(event, entry) : undefined}
      onClick={(event: ReactMouseEvent<HTMLButtonElement>) => onOpen(entry, event.currentTarget)}
      aria-label={entryLabel(entry)}
      title={entryLabel(entry)}
      data-key={entry.key}
    >
      <Ic name={entryIcon(entry)} size={11} />
      {showTime && isTimed(entry) ? <span className="num faint cev-t">{clockText(entry.start)}</span> : null}
      <span className="trunc">{entry.title}</span>
      {entry.habit && entry.habit.streak > 0 ? <span className="cev-streak num">🔥{entry.habit.streak}</span> : null}
      {agent ? <AgentFace name={agent} /> : null}
    </button>
  );
}

/* The sub line of a card: the runs a grouped entry stands for, or the source. */
function entrySub(entry: Entry): string {
  if (entry.group && entry.group.length > 1) {
    const failed = entry.group.filter((run) => run.status === "failed").length;
    return entry.group.length + " runs" + (failed > 0 ? " · " + failed + " failed" : "");
  }
  if (entry.source === "workflow_run") {
    return "Run #" + (entry.workflow?.run_number || "") + " · " + entry.status;
  }
  if (entry.source === "workflow_slot") {
    return (entry.status === "skipped" ? "Skipped" : entry.moved ? "Moved" : "Workflow") + " · " + (entry.workflow?.guardian || "");
  }
  if (entry.source === "google") {
    return entry.google?.calendar_name || "Google";
  }
  return entry.kind[0]!.toUpperCase() + entry.kind.slice(1);
}

/* The short trailing status a compact card shows next to its time. */
function entryCompactStatus(entry: Entry): string {
  if (entry.group && entry.group.length > 1) {
    return entry.group.length + " runs";
  }
  return entry.source === "workflow_run" ? entry.status : "";
}

export function EntryCard({ entry, onOpen, fallback, compact }: EntryHandlers & { entry: Entry; compact?: boolean }) {
  const agent = agentOf(entry);
  const draggable = canDrag(entry, fallback);
  const colorStyle = { "--c": entryColor(entry) } as CSSProperties;
  const status = entryCompactStatus(entry);
  return (
    <button
      type="button"
      className={cn(
        "wcard",
        compact && "compact",
        entry.source !== "item" && "event",
        entry.source === "workflow_slot" && "slot",
        isDone(entry) && "done",
        entry.firing?.status === "missed" && "missed"
      )}
      style={colorStyle}
      draggable={draggable}
      onDragStart={draggable ? (event) => dragStart(event, entry) : undefined}
      onClick={(event) => {
        /* A grouped slot stands for many runs: open the workflow's runs page
           instead of a single run's drawer. */
        if (entry.group && entry.group.length > 1) {
          window.location.hash = "#/workflows/" + encodeURIComponent(entry.workflow?.id || "") + "/runs";
          return;
        }
        onOpen(entry, event.currentTarget);
      }}
      aria-label={entryLabel(entry)}
      data-key={entry.key}
    >
      {compact ? (
        <span className="t">
          <span className="pdot" style={colorStyle} />
          <span className="trunc grow">{entry.title}</span>
          <span className="num faint wcard-when">{entryWhen(entry)}</span>
          {status ? <span className="wcard-status">{status}</span> : null}
          {agent ? <AgentFace name={agent} /> : null}
        </span>
      ) : (
        <>
          <span className="t">
            <span className="pdot" style={colorStyle} />
            <span className="trunc">{entry.title}</span>
          </span>
          <span className="m">
            <Ic name={entryIcon(entry)} size={11} />
            <span className="num">{entryWhen(entry)}</span>
            <span className="trunc grow">{entrySub(entry)}</span>
            {entry.habit && entry.habit.streak > 0 ? <span className="num">🔥{entry.habit.streak}</span> : null}
            {agent ? <AgentFace name={agent} /> : null}
          </span>
        </>
      )}
    </button>
  );
}

/* Shared drop handling: a day (keep time) or a minute of a day. */
export interface DropHandlers {
  onDrop: (key: string, day: Date, minute?: number) => void;
  onAdd: (day: Date, minute?: number) => void;
}

function dropProps(day: Date, onDrop: DropHandlers["onDrop"], minuteAt?: (event: DragEvent<HTMLElement>) => number) {
  return {
    onDragOver(event: DragEvent<HTMLElement>) {
      if (event.dataTransfer.types.includes(DRAG_TYPE)) {
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
        event.currentTarget.classList.add("over");
      }
    },
    onDragLeave(event: DragEvent<HTMLElement>) {
      event.currentTarget.classList.remove("over");
    },
    onDrop(event: DragEvent<HTMLElement>) {
      event.currentTarget.classList.remove("over");
      const key = event.dataTransfer.getData(DRAG_TYPE);
      if (key) {
        event.preventDefault();
        onDrop(key, day, minuteAt ? minuteAt(event) : undefined);
      }
    },
  };
}

const MONTH_CHIPS = 3;

export function MonthView({
  range,
  anchor,
  byDay,
  today,
  weekStart,
  onMore,
  picked,
  onPick,
  ...handlers
}: EntryHandlers &
  DropHandlers & {
    range: ViewRange;
    anchor: Date;
    byDay: Map<string, Entry[]>;
    today: Date;
    weekStart: number;
    onMore: (day: Date) => void;
    picked: Date;
    onPick: (day: Date) => void;
  }) {
  const names = Array.from({ length: 7 }, (_, index) => WEEKDAY_SHORT[(index + weekStart) % 7]!);
  const pickedKey = dayKey(picked);
  const pickedList = byDay.get(pickedKey) || [];
  return (
    <>
    <div className="cal" role="grid" aria-label="Month">
      <div className="cal-h" role="row">
        {names.map((name) => (
          <div key={name} role="columnheader">
            {name}
          </div>
        ))}
      </div>
      <div className="cal-g">
        {range.days.map((day) => {
          const key = dayKey(day);
          const list = byDay.get(key) || [];
          const shown = list.slice(0, MONTH_CHIPS);
          const extra = list.length - shown.length;
          return (
            <div
              key={key}
              role="gridcell"
              className={cn(
                "cday",
                day.getMonth() !== anchor.getMonth() && "out",
                sameDay(day, today) && "today",
                key === pickedKey && "picked"
              )}
              data-day={key}
              {...dropProps(day, handlers.onDrop)}
              onClick={(event) => {
                /* Phones: chips are colour bars, so a tap on the cell picks
                   the day and its list shows under the grid. */
                if (isPhone() && !(event.target as HTMLElement).closest(".add")) {
                  event.preventDefault();
                  onPick(day);
                }
              }}
            >
              <button
                type="button"
                className="dn"
                onClick={(event) => {
                  if (isPhone()) {
                    return;
                  }
                  event.stopPropagation();
                  onMore(day);
                }}
                aria-label={"Open " + dayTitle(day)}
              >
                {day.getDate()}
              </button>
              <button
                type="button"
                className="ibtn ibtn-xs add"
                aria-label={"Add on " + dayTitle(day)}
                onClick={() => handlers.onAdd(day)}
              >
                <Ic name="plus" size={13} />
              </button>
              {shown.map((entry) => (
                <EntryChip key={entry.key} entry={entry} onOpen={handlers.onOpen} fallback={handlers.fallback} />
              ))}
              {extra > 0 ? (
                <button type="button" className="cmore" onClick={() => onMore(day)}>
                  +{extra} more
                </button>
              ) : null}
            </div>
          );
        })}
      </div>
    </div>
    <section className="cal-mday" aria-label={dayTitle(picked)}>
      <h3 className={cn("cag-h", sameDay(picked, today) && "today")}>
        {sameDay(picked, today) ? "Today · " : ""}
        {dayTitle(picked)}
        <span className="sp" />
        <button type="button" className="ibtn ibtn-xs" aria-label={"Add on " + dayTitle(picked)} onClick={() => handlers.onAdd(picked)}>
          <Ic name="plus" size={13} />
        </button>
      </h3>
      {pickedList.length === 0 ? <p className="muted cag-empty">Nothing on this day.</p> : null}
      {pickedList.map((entry) => (
        <AgendaRow key={entry.key} entry={entry} onOpen={handlers.onOpen} fallback={handlers.fallback} />
      ))}
    </section>
    </>
  );
}

export function WeekView({
  range,
  byDay,
  today,
  ...handlers
}: EntryHandlers &
  DropHandlers & {
    range: ViewRange;
    byDay: Map<string, Entry[]>;
    today: Date;
  }) {
  return (
    <div className="week" role="grid" aria-label="Week">
      {range.days.map((day) => {
        const key = dayKey(day);
        const list = byDay.get(key) || [];
        const allDay = list.filter((entry) => !isTimed(entry));
        const timed = list.filter(isTimed);
        return (
          <div className="wcol" key={key} role="gridcell" data-day={key}>
            <div className={cn("wcol-h", sameDay(day, today) && "today")}>
              <span className="n">{day.getDate()}</span>
              <span className="muted wcol-d">{WEEKDAY_SHORT[day.getDay()]}</span>
              <span className="sp" />
              <button
                type="button"
                className="ibtn ibtn-xs"
                aria-label={"Add on " + dayTitle(day)}
                onClick={() => handlers.onAdd(day)}
              >
                <Ic name="plus" size={13} />
              </button>
            </div>
            <div className="wcol-b" {...dropProps(day, handlers.onDrop)}>
              {allDay.length > 0 ? (
                <div className="wcol-all">
                  {allDay.map((entry) => (
                    <EntryChip key={entry.key} entry={entry} onOpen={handlers.onOpen} fallback={handlers.fallback} />
                  ))}
                </div>
              ) : null}
              {timed.map((entry) => (
                <EntryCard key={entry.key} entry={entry} onOpen={handlers.onOpen} fallback={handlers.fallback} />
              ))}
              {list.length === 0 ? <span className="wcol-empty">Nothing</span> : null}
            </div>
          </div>
        );
      })}
    </div>
  );
}

const HOURS = Array.from({ length: 24 }, (_, hour) => hour);

export function DayView({
  day,
  entries,
  now,
  ...handlers
}: EntryHandlers &
  DropHandlers & {
    day: Date;
    entries: Entry[];
    now: number;
  }) {
  const scroller = useRef<HTMLDivElement>(null);
  const allDay = entries.filter((entry) => !isTimed(entry));
  const { slots: placed, hourTops } = layoutDayAdaptive(groupWorkflowRuns(entries), day);
  const isToday = sameDay(day, new Date(now));
  const nowTop = yOfMinute((now - startOfDay(day).getTime()) / 60000, hourTops);
  const dayKeyText = dayKey(day);

  useEffect(() => {
    const el = scroller.current;
    if (!el) {
      return;
    }
    const firstTimed = placed[0]?.top;
    const target = isToday ? nowTop - 120 : firstTimed !== undefined ? firstTimed - 48 : yOfMinute(7 * 60, hourTops);
    el.scrollTop = Math.max(0, target);
    // Scroll once per day shown, not on every entry change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dayKeyText]);

  const minuteAt = (event: DragEvent<HTMLElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    const y = event.clientY - rect.top;
    const minute = Math.round(minuteOfY(y, hourTops) / 15) * 15;
    return Math.max(0, Math.min(23 * 60 + 45, minute));
  };

  return (
    <div className="cday-view" aria-label={"Day " + dayTitle(day)}>
      <div className="cday-all">
        <span className="cday-all-k">All day</span>
        <div className="cday-all-b" {...dropProps(day, handlers.onDrop)}>
          {allDay.length === 0 ? <span className="wcol-empty">Nothing</span> : null}
          {allDay.map((entry) => (
            <EntryChip key={entry.key} entry={entry} onOpen={handlers.onOpen} fallback={handlers.fallback} />
          ))}
        </div>
      </div>
      <div className="cday-scroll" ref={scroller}>
        <div className="cday-grid" style={{ height: hourTops[24]! }}>
          <div className="cday-hours" aria-hidden="true">
            {HOURS.map((hour) => (
              <span key={hour} style={{ top: hourTops[hour]! }}>
                {String(hour).padStart(2, "0")}:00
              </span>
            ))}
          </div>
          <div
            className="cday-lane"
            {...dropProps(day, handlers.onDrop, minuteAt)}
            onDoubleClick={(event) => {
              const rect = event.currentTarget.getBoundingClientRect();
              const minute = Math.floor(minuteOfY(event.clientY - rect.top, hourTops) / 30) * 30;
              handlers.onAdd(day, minute);
            }}
          >
            {HOURS.map((hour) => (
              <i key={hour} className="cday-line" style={{ top: hourTops[hour]! }} />
            ))}
            {isToday ? <i className="cday-now" style={{ top: nowTop }} aria-hidden="true" /> : null}
            {placed.map((slot) => (
              <div
                key={slot.entry.key}
                className="cday-slot"
                style={{
                  top: slot.top,
                  height: slot.height,
                  left: "calc(" + (slot.lane / slot.lanes) * 100 + "% + 2px)",
                  width: "calc(" + 100 / slot.lanes + "% - 4px)",
                }}
              >
                <EntryCard entry={slot.entry} compact={slot.compact} onOpen={handlers.onOpen} fallback={handlers.fallback} />
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

export function AgendaView({
  range,
  byDay,
  today,
  onOpen,
  fallback,
  missedFirst,
}: EntryHandlers & {
  range: ViewRange;
  byDay: Map<string, Entry[]>;
  today: Date;
  missedFirst: Entry[];
}) {
  const days = range.days.filter((day) => (byDay.get(dayKey(day)) || []).length > 0);
  return (
    <div className="cagenda">
      {missedFirst.length > 0 ? (
        <section className="cag-day missed" aria-label="Missed">
          <h3 className="cag-h">
            <Ic name="triangle-alert" size={14} /> Missed while lana was off
          </h3>
          {missedFirst.map((entry) => (
            <AgendaRow key={"m" + entry.key} entry={entry} onOpen={onOpen} fallback={fallback} showDate />
          ))}
        </section>
      ) : null}
      {days.length === 0 && missedFirst.length === 0 ? (
        <p className="muted cag-empty">Nothing in the next {range.days.length} days.</p>
      ) : null}
      {days.map((day) => (
        <section className="cag-day" key={dayKey(day)} aria-label={dayTitle(day)}>
          <h3 className={cn("cag-h", sameDay(day, today) && "today")}>
            {sameDay(day, today) ? "Today · " : ""}
            {dayTitle(day)}
          </h3>
          {(byDay.get(dayKey(day)) || []).map((entry) => (
            <AgendaRow key={entry.key} entry={entry} onOpen={onOpen} fallback={fallback} />
          ))}
        </section>
      ))}
    </div>
  );
}

function AgendaRow({ entry, onOpen, showDate }: EntryHandlers & { entry: Entry; showDate?: boolean }) {
  const agent = agentOf(entry);
  const source =
    entry.source === "workflow_run"
      ? "Run #" + (entry.workflow?.run_number || "")
      : entry.source === "workflow_slot"
        ? "Workflow" + (entry.moved ? " · moved" : "") + (entry.status === "skipped" ? " · skipped" : "")
        : entry.source === "google"
          ? "Google"
          : entry.kind[0]!.toUpperCase() + entry.kind.slice(1);
  return (
    <button
      type="button"
      className={cn("cag-row", isDone(entry) && "done", entry.firing?.status === "missed" && "missed")}
      style={{ "--c": entryColor(entry) } as CSSProperties}
      onClick={(event) => onOpen(entry, event.currentTarget)}
      aria-label={entryLabel(entry)}
      data-key={entry.key}
    >
      <span className="cag-when num">
        {showDate ? dayTitle(new Date(entry.occurrence || entry.start)) + " " : ""}
        {entryWhen(entry)}
      </span>
      <span className="pdot" style={{ "--c": entryColor(entry) } as CSSProperties} />
      <Ic name={entryIcon(entry)} size={14} />
      <span className="trunc grow cag-title">{entry.title}</span>
      {entry.habit && entry.habit.streak > 0 ? <span className="num muted">🔥 {entry.habit.streak}</span> : null}
      <span className="muted cag-src">{source}</span>
      {agent ? <AgentFace name={agent} /> : null}
    </button>
  );
}
