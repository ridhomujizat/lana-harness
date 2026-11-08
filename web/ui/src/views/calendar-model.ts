import type { Entry } from "../api/calendar";

/* Pure date math for the Calendar page (spec #35, design "Web UI"). Days are
   laid out in the browser's local zone; a day key is YYYY-MM-DD. */

export type ViewMode = "month" | "week" | "day" | "agenda";
export const VIEW_MODES: ViewMode[] = ["month", "week", "day", "agenda"];
export const AGENDA_DAYS = 14;
export const HOUR_PX = 48;
/* The day grid draws a slot at least this tall even when the entry is
   seconds long, so a workflow run stays clickable; the lane math uses the
   same visual extent so what is drawn matches what is measured. */
export const MIN_SLOT_PX = 22;
/* A short (sub-hour) card in the Day view is a fixed-height one-line card;
   the hour it starts in grows to hold the stack. */
export const COMPACT_CARD_PX = 28;
/* Vertical gap between stacked cards, so neighbours never touch. */
export const GAP = 4;

export const WEEKDAY_SHORT = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
export const MONTH_LONG = [
  "January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December",
];
export const MONTH_SHORT = MONTH_LONG.map((name) => name.slice(0, 3));

const pad = (value: number) => String(value).padStart(2, "0");

export function dayKey(date: Date): string {
  return date.getFullYear() + "-" + pad(date.getMonth() + 1) + "-" + pad(date.getDate());
}

/* Local midnight of a YYYY-MM-DD key; an invalid key gives today. */
export function parseDay(key: string | undefined): Date {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(key || "");
  if (!match) {
    return startOfDay(new Date());
  }
  const date = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
  return Number.isNaN(date.getTime()) ? startOfDay(new Date()) : date;
}

export function startOfDay(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate());
}

/* Calendar-day arithmetic (DST-safe: it moves the date, not the instant). */
export function addDays(date: Date, days: number): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate() + days);
}

export function addMonths(date: Date, months: number): Date {
  const target = new Date(date.getFullYear(), date.getMonth() + months, 1);
  const last = new Date(target.getFullYear(), target.getMonth() + 1, 0).getDate();
  return new Date(target.getFullYear(), target.getMonth(), Math.min(date.getDate(), last));
}

export function startOfWeek(date: Date, weekStart: number): Date {
  const day = startOfDay(date);
  const diff = (day.getDay() - weekStart + 7) % 7;
  return addDays(day, -diff);
}

export function sameDay(a: Date, b: Date): boolean {
  return dayKey(a) === dayKey(b);
}

export interface ViewRange {
  from: Date;
  /* Exclusive. */
  to: Date;
  days: Date[];
}

/* The days a view shows. Month follows gr8r: whole weeks from the week of the
   1st, 6 rows trimmed to 5 when the last row is all next month. */
export function viewRange(mode: ViewMode, anchor: Date, weekStart: number): ViewRange {
  let from: Date;
  let count: number;
  switch (mode) {
    case "month": {
      const first = new Date(anchor.getFullYear(), anchor.getMonth(), 1);
      from = startOfWeek(first, weekStart);
      count = 42;
      if (addDays(from, 35).getMonth() !== anchor.getMonth()) {
        count = 35;
      }
      break;
    }
    case "week":
      from = startOfWeek(anchor, weekStart);
      count = 7;
      break;
    case "day":
      from = startOfDay(anchor);
      count = 1;
      break;
    default:
      from = startOfDay(anchor);
      count = AGENDA_DAYS;
  }
  const days = Array.from({ length: count }, (_, index) => addDays(from, index));
  return { from, to: addDays(from, count), days };
}

/* Where ‹ and › go from the anchor. */
export function stepAnchor(mode: ViewMode, anchor: Date, step: number): Date {
  switch (mode) {
    case "month":
      return addMonths(anchor, step);
    case "week":
      return addDays(anchor, 7 * step);
    case "day":
      return addDays(anchor, step);
    default:
      return addDays(anchor, AGENDA_DAYS * step);
  }
}

export function periodLabel(mode: ViewMode, anchor: Date, range: ViewRange): string {
  if (mode === "month") {
    return MONTH_LONG[anchor.getMonth()] + " " + anchor.getFullYear();
  }
  if (mode === "day") {
    return (
      WEEKDAY_SHORT[anchor.getDay()] + ", " + anchor.getDate() + " " + MONTH_LONG[anchor.getMonth()] + " " +
      anchor.getFullYear()
    );
  }
  const first = range.days[0]!;
  const last = range.days[range.days.length - 1]!;
  const year = last.getFullYear();
  if (first.getMonth() === last.getMonth()) {
    return first.getDate() + "–" + last.getDate() + " " + MONTH_SHORT[first.getMonth()] + " " + year;
  }
  const head = first.getDate() + " " + MONTH_SHORT[first.getMonth()];
  const tail = last.getDate() + " " + MONTH_SHORT[last.getMonth()] + " " + year;
  return head + (first.getFullYear() !== year ? " " + first.getFullYear() : "") + " – " + tail;
}

export function clockText(ms: number): string {
  const date = new Date(ms);
  return pad(date.getHours()) + ":" + pad(date.getMinutes());
}

export function dayTitle(date: Date): string {
  return WEEKDAY_SHORT[date.getDay()] + " " + date.getDate() + " " + MONTH_SHORT[date.getMonth()];
}

/* Timed entries sit on the hour grid; dated ones (all-day, notes, tasks
   without a time, short-interval workflow rows) sit in the all-day lane. */
export function isTimed(entry: Entry): boolean {
  if (entry.all_day) {
    return false;
  }
  if (entry.kind === "note") {
    return false;
  }
  if (entry.kind === "task" || entry.kind === "habit") {
    return entry.start > 0 && entry.start !== parseDay(entry.date).getTime();
  }
  return entry.start > 0;
}

/* Day keys an entry covers. Timed entries that cross midnight cover every
   day they touch; dated entries use date..end_date (inclusive). */
export function entryDays(entry: Entry): string[] {
  let first: Date;
  let last: Date;
  if (isTimed(entry)) {
    first = startOfDay(new Date(entry.start));
    const endMs = entry.end > entry.start ? entry.end - 1 : entry.start;
    last = startOfDay(new Date(endMs));
  } else {
    first = entry.date ? parseDay(entry.date) : startOfDay(new Date(entry.start));
    last = entry.end_date ? parseDay(entry.end_date) : first;
  }
  if (last < first) {
    last = first;
  }
  const out: string[] = [];
  for (let day = first; day <= last && out.length < 92; day = addDays(day, 1)) {
    out.push(dayKey(day));
  }
  return out;
}

/* Sort: all-day first, then start time, then title (REQ-CAL-002). */
export function compareEntries(a: Entry, b: Entry): number {
  const at = isTimed(a);
  const bt = isTimed(b);
  if (at !== bt) {
    return at ? 1 : -1;
  }
  if (a.start !== b.start) {
    return a.start - b.start;
  }
  return a.title.localeCompare(b.title);
}

export function groupByDay(entries: Entry[], days: Date[]): Map<string, Entry[]> {
  const map = new Map<string, Entry[]>();
  for (const day of days) {
    map.set(dayKey(day), []);
  }
  for (const entry of entries) {
    for (const key of entryDays(entry)) {
      map.get(key)?.push(entry);
    }
  }
  for (const list of map.values()) {
    list.sort(compareEntries);
  }
  return map;
}

export interface PlacedEntry {
  entry: Entry;
  top: number;
  height: number;
  lane: number;
  lanes: number;
  /* Short (sub-hour) cards render as one compact line. */
  compact: boolean;
}

const MIN = 60000;

/* The number of milliseconds the minimum slot height stands for at a given
   hour height. */
export function minSlotMs(hourPx = HOUR_PX): number {
  return (MIN_SLOT_PX / hourPx) * 3600000;
}

/* Visual end of an entry: its real end, or the minimum extent when it is
   shorter (point entries and runs last seconds). */
function visualEnd(entry: Entry, minMs: number): number {
  const rawEnd = entry.end > entry.start ? entry.end : entry.start + 30 * MIN;
  return Math.max(rawEnd, entry.start + minMs);
}

/* A card is short when its real duration is under an hour: runs, point items,
   short events and grouped run blocks. */
const SHORT_MS = 60 * MIN;

export interface DayLayout {
  slots: PlacedEntry[];
  /* 25 values: the y top of each hour plus the total height in hourTops[24]. */
  hourTops: number[];
}

/* A short card sits this far below its exact minute, matching the long cards'
   inset so a card ending at the same minute leaves a full GAP. */
const INSET = 2;

/* Maps a minute of the day to a y offset. Each hour keeps its base HOUR_PX
   span so the grid stays linear; the grown part of an hour is free space
   below its cards. yOfMinute(1440) is hourTops[24]. */
export function yOfMinute(minute: number, hourTops: number[]): number {
  const clamped = Math.max(0, Math.min(1440, minute));
  if (clamped >= 1440) {
    return hourTops[24] ?? 0;
  }
  const hour = Math.floor(clamped / 60);
  const frac = (clamped - hour * 60) / 60;
  return (hourTops[hour] ?? 0) + frac * HOUR_PX;
}

/* Inverse of yOfMinute, clamped to the day; a y in an hour's grown part maps
   to the end of that hour. */
export function minuteOfY(y: number, hourTops: number[]): number {
  const total = hourTops[24] ?? 0;
  const clamped = Math.max(0, Math.min(total, y));
  let hour = 0;
  while (hour < 23 && clamped >= (hourTops[hour + 1] ?? total)) {
    hour += 1;
  }
  const local = clamped - (hourTops[hour] ?? 0);
  if (local >= HOUR_PX) {
    return Math.min(1440, (hour + 1) * 60);
  }
  return hour * 60 + (local / HOUR_PX) * 60;
}

/* Places the timed entries of one day. Short cards pile into a single stack
   (sorted by start, then title) and grow the hour they start in so the stack
   fits; long cards span their time span on the final hour tops. Visual
   extents that overlap share the column width through lane assignment, and a
   GAP keeps stacked cards from touching. */
export function layoutDayAdaptive(entries: Entry[], day: Date): DayLayout {
  const dayStart = startOfDay(day).getTime();
  const dayEnd = addDays(day, 1).getTime();
  const items = entries
    .filter(isTimed)
    .map((entry) => {
      const start = Math.max(entry.start, dayStart);
      const end = Math.min(Math.max(entry.end, start), dayEnd);
      const startMin = (start - dayStart) / MIN;
      return { entry, startMin, endMin: (end - dayStart) / MIN, short: end - start < SHORT_MS };
    })
    .sort((a, b) => a.startMin - b.startMin || a.entry.title.localeCompare(b.entry.title));

  const shorts = items.filter((item) => item.short);
  const longs = items.filter((item) => !item.short);
  const step = COMPACT_CARD_PX + GAP;

  /* Forward pass over the hours: place the stack and grow each hour it
     overflows. Short cards only push downward, so one pass is enough. */
  const hourTops = new Array<number>(25).fill(0);
  const shortTop = new Map<(typeof shorts)[number], number>();
  let prevBottom = -Infinity;
  let top = 0;
  for (let hour = 0; hour < 24; hour++) {
    hourTops[hour] = top;
    const inHour = shorts.filter((item) => Math.floor(item.startMin / 60) === hour);
    if (inHour.length === 0) {
      top += HOUR_PX;
      continue;
    }
    let cardTop = top;
    inHour.forEach((item, index) => {
      const natural = top + (item.startMin / 60 - hour) * HOUR_PX + INSET;
      const floor = index === 0 ? prevBottom + GAP : cardTop + step;
      cardTop = Math.max(natural, floor);
      shortTop.set(item, cardTop);
    });
    prevBottom = cardTop + COMPACT_CARD_PX;
    top += Math.max(HOUR_PX, prevBottom + GAP - top);
  }
  hourTops[24] = top;

  const slots: PlacedEntry[] = [];
  for (const item of shorts) {
    slots.push({
      entry: item.entry,
      top: shortTop.get(item) ?? 0,
      height: COMPACT_CARD_PX,
      lane: 0,
      lanes: 1,
      compact: true,
    });
  }
  for (const item of longs) {
    /* Long cards span their time span, inset 2px top and bottom so they never
       touch the card above or below. */
    const cardTop = yOfMinute(item.startMin, hourTops) + INSET;
    const cardEnd = yOfMinute(item.endMin, hourTops) - INSET;
    slots.push({
      entry: item.entry,
      top: cardTop,
      height: Math.max(cardEnd - cardTop, 0),
      lane: 0,
      lanes: 1,
      compact: false,
    });
  }

  /* Lanes from the visual y extents: a long card over the short stack, or two
     long cards, shares the column width; the rest render full width. */
  const ordered = [...slots].sort((a, b) => a.top - b.top || a.entry.title.localeCompare(b.entry.title));
  let cluster: PlacedEntry[] = [];
  let clusterEnd = 0;
  const flush = () => {
    const lanes = cluster.reduce((max, member) => Math.max(max, member.lane + 1), 1);
    for (const member of cluster) {
      member.lanes = lanes;
    }
    cluster = [];
  };
  for (const slot of ordered) {
    if (cluster.length > 0 && slot.top >= clusterEnd) {
      flush();
    }
    const laneEnds: number[] = [];
    for (const member of cluster) {
      laneEnds[member.lane] = Math.max(laneEnds[member.lane] || 0, member.top + member.height);
    }
    let lane = 0;
    while (laneEnds[lane] !== undefined && laneEnds[lane]! > slot.top) {
      lane += 1;
    }
    slot.lane = lane;
    cluster.push(slot);
    clusterEnd = Math.max(clusterEnd, slot.top + slot.height);
  }
  if (cluster.length > 0) {
    flush();
  }
  return { slots: ordered, hourTops };
}

/* Merges workflow runs of the same workflow whose visual extents overlap or
   touch into one entry (a burst of runs close together draws as one slot
   instead of a staircase). The grouped entry keeps its runs on `group`; a
   lone run passes through unchanged. */
export function groupWorkflowRuns(entries: Entry[], hourPx = HOUR_PX): Entry[] {
  const minMs = minSlotMs(hourPx);
  const byWorkflow = new Map<string, Entry[]>();
  const out: Entry[] = [];
  for (const entry of entries) {
    if (entry.source === "workflow_run" && entry.workflow) {
      const list = byWorkflow.get(entry.workflow.id) || [];
      list.push(entry);
      byWorkflow.set(entry.workflow.id, list);
    } else {
      out.push(entry);
    }
  }
  for (const list of byWorkflow.values()) {
    const sorted = [...list].sort((a, b) => a.start - b.start || a.title.localeCompare(b.title));
    let cluster: Entry[] = [];
    let clusterEnd = 0;
    const flush = () => {
      if (cluster.length === 1) {
        out.push(cluster[0]!);
      } else if (cluster.length > 1) {
        const first = cluster[0]!;
        const failed = cluster.filter((run) => run.status === "failed").length;
        out.push({
          ...first,
          key: "group:" + first.workflow!.id + ":" + first.start,
          title: first.workflow!.name,
          start: first.start,
          end: Math.max(...cluster.map((run) => visualEnd(run, minMs))),
          occurrence: first.start,
          status: failed > 0 ? "failed" : first.status,
          group: cluster,
        });
      }
      cluster = [];
    };
    for (const run of sorted) {
      const end = visualEnd(run, minMs);
      if (cluster.length > 0 && run.start > clusterEnd) {
        flush();
      }
      cluster.push(run);
      clusterEnd = Math.max(clusterEnd, end);
    }
    flush();
  }
  return out;
}

/* Moving keeps the time of day when dropped on a day, or takes the slot time
   when dropped on the hour grid. Returns the new start instant. */
export function movedStart(entry: Entry, targetDay: Date, minuteOfDay?: number): number {
  const base = startOfDay(targetDay);
  if (minuteOfDay !== undefined) {
    return new Date(base.getFullYear(), base.getMonth(), base.getDate(), 0, minuteOfDay).getTime();
  }
  if (!entry.start) {
    return base.getTime();
  }
  const old = new Date(entry.start);
  return new Date(base.getFullYear(), base.getMonth(), base.getDate(), old.getHours(), old.getMinutes()).getTime();
}

/* HH:MM of an instant, for the item forms. */
export function timeOf(ms: number): string {
  return ms ? clockText(ms) : "";
}

/* Combines a day key and HH:MM into a local instant (0 when either is bad). */
export function instantOf(date: string, time: string): number {
  const match = /^(\d{1,2}):(\d{2})$/.exec(time || "");
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date || "") || !match) {
    return 0;
  }
  const day = parseDay(date);
  return new Date(day.getFullYear(), day.getMonth(), day.getDate(), Number(match[1]), Number(match[2])).getTime();
}
