import type { Entry } from "../api/calendar";

/* Pure date math for the Calendar page (spec #35, design "Web UI"). Days are
   laid out in the browser's local zone; a day key is YYYY-MM-DD. */

export type ViewMode = "month" | "week" | "day" | "agenda";
export const VIEW_MODES: ViewMode[] = ["month", "week", "day", "agenda"];
export const AGENDA_DAYS = 14;
export const HOUR_PX = 48;

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
}

/* Places timed entries of one day on the hour grid. Overlapping entries split
   the width into lanes; a cluster shares one lane count. Point entries (no
   end) take 30 minutes of height. */
export function layoutDay(entries: Entry[], day: Date, hourPx = HOUR_PX): PlacedEntry[] {
  const dayStart = startOfDay(day).getTime();
  const dayEnd = addDays(day, 1).getTime();
  const MIN = 60000;
  const items = entries
    .filter(isTimed)
    .map((entry) => {
      const start = Math.max(entry.start, dayStart);
      const rawEnd = entry.end > entry.start ? entry.end : entry.start + 30 * MIN;
      const end = Math.min(Math.max(rawEnd, start + 20 * MIN), dayEnd);
      return { entry, start, end };
    })
    .sort((a, b) => a.start - b.start || b.end - a.end);

  const placed: PlacedEntry[] = [];
  let cluster: { item: (typeof items)[number]; lane: number }[] = [];
  let clusterEnd = 0;
  const flush = () => {
    const lanes = cluster.reduce((max, member) => Math.max(max, member.lane + 1), 1);
    for (const member of cluster) {
      placed.push({
        entry: member.item.entry,
        top: ((member.item.start - dayStart) / (60 * MIN)) * hourPx,
        height: Math.max(((member.item.end - member.item.start) / (60 * MIN)) * hourPx, hourPx / 3),
        lane: member.lane,
        lanes,
      });
    }
    cluster = [];
  };
  for (const item of items) {
    if (cluster.length > 0 && item.start >= clusterEnd) {
      flush();
    }
    const laneEnds: number[] = [];
    for (const member of cluster) {
      laneEnds[member.lane] = Math.max(laneEnds[member.lane] || 0, member.item.end);
    }
    let lane = 0;
    while (laneEnds[lane] !== undefined && laneEnds[lane]! > item.start) {
      lane += 1;
    }
    cluster.push({ item, lane });
    clusterEnd = Math.max(clusterEnd, item.end);
  }
  if (cluster.length > 0) {
    flush();
  }
  return placed;
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
