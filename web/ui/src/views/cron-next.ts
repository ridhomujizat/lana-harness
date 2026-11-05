/* The next fire time of a 5-field cron expression in an IANA time zone, for
   the Schedules page. Same grammar as internal/workflow/cron.go: `*`, numbers,
   lists, ranges, steps, month/day names and @hourly/@daily/@weekly/@monthly.
   When day-of-month and day-of-week are both restricted, either may match. */

const MACROS: Record<string, string> = {
  "@hourly": "0 * * * *",
  "@daily": "0 0 * * *",
  "@weekly": "0 0 * * 0",
  "@monthly": "0 0 1 * *",
};

const NAMES: Record<string, number> = {
  jan: 1, feb: 2, mar: 3, apr: 4, may: 5, jun: 6, jul: 7, aug: 8, sep: 9, oct: 10, nov: 11, dec: 12,
  sun: 0, mon: 1, tue: 2, wed: 3, thu: 4, fri: 5, sat: 6,
};

interface Field {
  set: Set<number>;
  star: boolean;
}

function parseField(text: string, min: number, max: number): Field | null {
  const set = new Set<number>();
  const star = text === "*" || text.startsWith("*/");
  for (const part of text.split(",")) {
    const [rangePart, stepPart] = part.split("/");
    const step = stepPart ? Number(stepPart) : 1;
    if (!Number.isInteger(step) || step < 1) {
      return null;
    }
    let lo = min;
    let hi = max;
    if (rangePart && rangePart !== "*") {
      const [a, b] = rangePart.split("-");
      const num = (v: string | undefined) => (v === undefined ? NaN : v.toLowerCase() in NAMES ? NAMES[v.toLowerCase()]! : Number(v));
      lo = num(a);
      hi = b === undefined ? (stepPart ? max : lo) : num(b);
    }
    if (!Number.isInteger(lo) || !Number.isInteger(hi) || lo < min || hi > max || lo > hi) {
      return null;
    }
    for (let v = lo; v <= hi; v += step) {
      set.add(v);
    }
  }
  return { set, star };
}

export interface Cron {
  minute: Field;
  hour: Field;
  dom: Field;
  month: Field;
  dow: Field;
}

export function parseCron(expr: string): Cron | null {
  const text = MACROS[expr.trim().toLowerCase()] || expr.trim();
  const parts = text.split(/\s+/);
  if (parts.length !== 5) {
    return null;
  }
  const minute = parseField(parts[0]!, 0, 59);
  const hour = parseField(parts[1]!, 0, 23);
  const dom = parseField(parts[2]!, 1, 31);
  const month = parseField(parts[3]!, 1, 12);
  const dow = parseField(parts[4]!, 0, 7);
  if (!minute || !hour || !dom || !month || !dow) {
    return null;
  }
  if (dow.set.has(7)) {
    dow.set.add(0);
  }
  return { minute, hour, dom, month, dow };
}

const WEEKDAY: Record<string, number> = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 };

function partsIn(ms: number, zone: string) {
  const format = new Intl.DateTimeFormat("en-US", {
    timeZone: zone,
    hourCycle: "h23",
    year: "numeric",
    month: "numeric",
    day: "numeric",
    hour: "numeric",
    minute: "numeric",
    weekday: "short",
  });
  const out: Record<string, string> = {};
  for (const part of format.formatToParts(new Date(ms))) {
    out[part.type] = part.value;
  }
  return {
    month: Number(out.month),
    day: Number(out.day),
    hour: Number(out.hour) % 24,
    minute: Number(out.minute),
    dow: WEEKDAY[out.weekday || "Sun"] ?? 0,
  };
}

function dayMatches(cron: Cron, p: ReturnType<typeof partsIn>): boolean {
  if (!cron.month.set.has(p.month)) {
    return false;
  }
  const domOk = cron.dom.set.has(p.day);
  const dowOk = cron.dow.set.has(p.dow);
  if (!cron.dom.star && !cron.dow.star) {
    return domOk || dowOk;
  }
  return domOk && dowOk;
}

/* The first matching minute strictly after `after` (Unix ms), or 0 when none
   is found within about 13 months. */
export function nextCron(expr: string, zone: string, after: number): number {
  const cron = parseCron(expr);
  if (!cron) {
    return 0;
  }
  let tz = zone || Intl.DateTimeFormat().resolvedOptions().timeZone;
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: tz });
  } catch {
    tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
  }
  const MIN = 60000;
  let t = Math.floor(after / MIN) * MIN + MIN;
  const limit = after + 400 * 86400000;
  while (t < limit) {
    const p = partsIn(t, tz);
    if (!dayMatches(cron, p)) {
      /* Jump to the next local midnight (approximately; DST shifts are
         absorbed by re-reading the parts). */
      t += ((23 - p.hour) * 60 + (60 - p.minute)) * MIN;
      continue;
    }
    if (!cron.hour.set.has(p.hour)) {
      t += (60 - p.minute) * MIN;
      continue;
    }
    if (!cron.minute.set.has(p.minute)) {
      t += MIN;
      continue;
    }
    return t;
  }
  return 0;
}
