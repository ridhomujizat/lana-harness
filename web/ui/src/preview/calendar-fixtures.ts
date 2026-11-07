import type {
  CalendarSettings,
  Entry,
  Firing,
  GoogleLink,
  Item,
  ItemInput,
  Weekday,
} from "../api/calendar";
import { workflowEntries } from "../views/calendar-fallback";
import type { SlotOverride } from "../views/calendar-fallback";
import { addDays, dayKey, instantOf, parseDay, startOfDay } from "../views/calendar-model";
import { runsFixture, workflowFixture } from "./workflow-fixtures";

/* Stateful preview fixtures for the Calendar page (spec #35). Every calendar
   endpoint is answered from module state, so creating, moving, ticking and
   deleting work in preview.html. Repeats expand with the contract's rules. */

const MIN = 60000;
const HOUR = 60 * MIN;
const today = startOfDay(new Date());
const at = (days: number, hour: number, minute = 0) =>
  new Date(today.getFullYear(), today.getMonth(), today.getDate() + days, hour, minute).getTime();
const day = (days: number) => dayKey(addDays(today, days));

let seq = 100;
function item(fields: Partial<Item> & Pick<Item, "kind" | "title">): Item {
  seq += 1;
  return {
    id: "ci_" + seq,
    notes: "",
    start: 0,
    end: 0,
    all_day: false,
    date: "",
    end_date: "",
    time: "",
    repeat: { freq: "none", days: [], until: "" },
    assignee: "owner",
    color: "",
    state: "",
    action: null,
    google: null,
    created_by: "owner",
    updated_by: "owner",
    created_at: Date.now() - 3 * 86400000,
    updated_at: Date.now() - 3 * 86400000,
    ...fields,
  };
}

const items: Item[] = [
  item({ kind: "event", title: "Dentist", start: at(0, 14), end: at(0, 15), color: "rose" }),
  item({
    kind: "event",
    title: "Team sync",
    start: at(-14, 10),
    end: at(-14, 10, 30),
    repeat: { freq: "weekly", days: ["mon", "thu"], until: "" },
    google: { calendar_id: "primary", event_id: "g1", sync: "synced" },
  }),
  item({ kind: "event", title: "Bali holiday", all_day: true, date: day(8), end_date: day(10), color: "teal" }),
  item({ kind: "task", title: "Pay electricity", date: day(1), state: "open", created_by: "agent:lana", updated_by: "agent:lana" }),
  item({ kind: "task", title: "Renew domain", date: day(0), state: "done" }),
  item({
    kind: "task",
    title: "Draft Q4 research summary",
    date: day(2),
    time: "11:00",
    state: "open",
    assignee: "researcher",
    created_by: "agent:researcher",
    updated_by: "agent:researcher",
  }),
  item({
    kind: "reminder",
    title: "Take the laundry out",
    start: at(0, 17, 30),
    action: { type: "send_message", target: "telegram", text: "Take the laundry out" },
  }),
  item({
    kind: "reminder",
    title: "Weekly spending recap",
    start: at(-7 + ((5 - today.getDay() + 7) % 7), 16),
    repeat: { freq: "weekly", days: ["fri"], until: "" },
    action: { type: "agent_prompt", agent: "lana", prompt: "Recap this week's spending and post it in your room." },
    created_by: "agent:lana",
    updated_by: "agent:lana",
  }),
  item({
    kind: "reminder",
    title: "Check the job board",
    start: at(-1, 8),
    action: { type: "agent_prompt", agent: "researcher", prompt: "Check the job board and shortlist 3 roles for me." },
  }),
  item({ kind: "note", title: "Jakarta trip ideas", date: day(2), notes: "Kota Tua, Monas, Glodok food." }),
  item({ kind: "habit", title: "Read 20 minutes", date: day(-30), time: "21:00", repeat: { freq: "daily", days: [], until: "" } }),
  item({ kind: "habit", title: "Walk 30 min", date: day(-30), repeat: { freq: "weekly", days: ["mon", "wed", "fri"], until: "" } }),
];

const checks = new Set<string>();
for (let back = 1; back <= 4; back += 1) {
  checks.add(items[10]!.id + "@" + day(-back));
}

const firings = new Map<string, Firing>();
firings.set(items[8]!.id + "@" + at(-1, 8), {
  status: "missed",
  at: at(-1, 8),
  started_at: 0,
  ended_at: 0,
  session_id: "",
  run: null,
  error: "",
});

const itemOverrides = new Map<string, SlotOverride>();
const wfOverrides = new Map<string, SlotOverride>();

const settings: CalendarSettings = {
  timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
  week_start: 1,
  agenda: { enabled: true, time: "07:00" },
  ics: { url: "http://127.0.0.1:18300/cal/3qV9xL2mT8kZ.ics" },
};

const google: GoogleLink = {
  client_configured: true,
  state: "linked",
  account: "owner@example.com",
  calendars: [
    { id: "primary", name: "Owner", color: "#4285f4", selected: true, writable: true },
    { id: "holidays", name: "Holidays in Indonesia", color: "#0b8043", selected: true, writable: false },
  ],
  target: "primary",
  last_sync: Date.now() - 4 * MIN,
  error: "",
};

const WD: Weekday[] = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];

/* The base instant and day of an item, for expansion. */
function baseOf(it: Item): { start: number; date: Date } {
  if (it.start) {
    return { start: it.start, date: startOfDay(new Date(it.start)) };
  }
  const date = parseDay(it.date);
  return { start: it.time ? instantOf(it.date, it.time) : date.getTime(), date };
}

function matches(it: Item, base: Date, d: Date): boolean {
  if (d < base) {
    return false;
  }
  if (it.repeat.until && dayKey(d) > it.repeat.until) {
    return false;
  }
  switch (it.repeat.freq) {
    case "daily":
      return true;
    case "weekdays":
      return d.getDay() >= 1 && d.getDay() <= 5;
    case "weekly":
      return it.repeat.days.includes(WD[d.getDay()]!);
    case "monthly":
      return d.getDate() === base.getDate();
    case "yearly":
      return d.getDate() === base.getDate() && d.getMonth() === base.getMonth();
    default:
      return dayKey(d) === dayKey(base);
  }
}

function streak(it: Item, upto: Date): number {
  const base = baseOf(it).date;
  let count = 0;
  let d = startOfDay(upto);
  if (!checks.has(it.id + "@" + dayKey(d))) {
    d = addDays(d, -1);
  }
  for (let guard = 0; guard < 400 && d >= base; guard += 1, d = addDays(d, -1)) {
    if (!matches(it, base, d)) {
      continue;
    }
    if (!checks.has(it.id + "@" + dayKey(d))) {
      break;
    }
    count += 1;
  }
  return count;
}

function itemEntries(from: number, to: number): Entry[] {
  const out: Entry[] = [];
  for (const it of items) {
    const base = baseOf(it);
    const duration = it.end > it.start ? it.end - it.start : 0;
    const spanDays = it.all_day && it.end_date ? Math.round((parseDay(it.end_date).getTime() - parseDay(it.date).getTime()) / 86400000) : 0;
    const timed = Boolean(it.start) || Boolean(it.time);
    for (let d = addDays(startOfDay(new Date(from)), -spanDays); d.getTime() < to; d = addDays(d, 1)) {
      if (!matches(it, base.date, d)) {
        continue;
      }
      const occurrence = timed
        ? new Date(d.getFullYear(), d.getMonth(), d.getDate(), new Date(base.start).getHours(), new Date(base.start).getMinutes()).getTime()
        : d.getTime();
      const override = itemOverrides.get(it.id + "@" + occurrence);
      if (override && override.op === "skip" && it.kind !== "habit") {
        continue;
      }
      const start = override && override.op === "move" ? override.to : occurrence;
      const date = dayKey(new Date(start));
      const firing = firings.get(it.id + "@" + occurrence) || null;
      out.push({
        key: "item:" + it.id + ":" + occurrence,
        source: "item",
        kind: it.kind,
        title: it.title,
        start,
        end: duration ? start + duration : 0,
        all_day: it.all_day,
        date,
        end_date: it.all_day ? dayKey(addDays(parseDay(date), spanDays)) : date,
        color: it.color,
        status: it.kind === "task" ? it.state || "open" : override?.op === "skip" ? "skipped" : firing ? firing.status : "",
        moved: Boolean(override && override.op === "move"),
        occurrence,
        item_id: it.id,
        repeating: it.repeat.freq !== "none",
        assignee: it.assignee,
        created_by: it.created_by,
        action: it.action,
        firing,
        habit: it.kind === "habit" ? { done: checks.has(it.id + "@" + dayKey(d)), streak: streak(it, d < today ? d : today) } : null,
        workflow: null,
        google: it.google,
        readonly: false,
      });
    }
  }
  return out;
}

function googleEntries(from: number, to: number): Entry[] {
  const flight = at(9, 6, 40);
  if (flight < from || flight >= to) {
    return [];
  }
  return [
    {
      key: "google:holidays:flight",
      source: "google",
      kind: "event",
      title: "Flight CGK → DPS",
      start: flight,
      end: flight + 2 * HOUR,
      all_day: false,
      date: dayKey(new Date(flight)),
      end_date: dayKey(new Date(flight)),
      color: "",
      status: "",
      moved: false,
      occurrence: flight,
      item_id: "",
      repeating: false,
      assignee: "",
      created_by: "google",
      action: null,
      firing: null,
      habit: null,
      workflow: null,
      google: { calendar_id: "primary", event_id: "g9", sync: "synced", color: "#4285f4", calendar_name: "Owner" },
      readonly: true,
    },
  ];
}

function entries(from: number, to: number): Entry[] {
  const wf = workflowEntries([workflowFixture], runsFixture, from, to, Date.now(), wfOverrides);
  return [...itemEntries(from, to), ...wf, ...googleEntries(from, to)].sort(
    (a, b) => a.start - b.start || a.title.localeCompare(b.title)
  );
}

function missedCount(): number {
  return [...firings.values()].filter((firing) => firing.status === "missed").length;
}

function err(status: number, code: string, message: string) {
  return { status, body: { error: { code, message, details: [] } } };
}

function touch(it: Item) {
  it.updated_at = Date.now();
  it.updated_by = "owner";
  if (it.google) {
    it.google = { ...it.google, sync: "pending" };
  }
}

export function mockCalendar(method: string, parts: string[], url: URL, body: unknown): { status: number; body?: unknown; stream?: string } {
  const at2 = (index: number) => parts[index] || "";
  const input = (body || {}) as Record<string, unknown>;
  const route = at2(2);

  if (route === "stream") {
    return { status: 200, stream: ": connected\n\n" };
  }
  if (route === "entries" && method === "GET") {
    const from = Number(url.searchParams.get("from"));
    const to = Number(url.searchParams.get("to"));
    if (!(to > from)) {
      return err(400, "invalid_range", "from must be before to");
    }
    if (to - from > 92 * 86400000) {
      return err(400, "range_too_large", "At most 92 days.");
    }
    return {
      status: 200,
      body: {
        entries: entries(from, to),
        timezone: settings.timezone,
        week_start: settings.week_start,
        now: Date.now(),
        google: { state: google.state },
        missed: missedCount(),
      },
    };
  }
  if (route === "items") {
    const id = at2(3);
    const found = items.find((it) => it.id === id);
    if (!id && method === "POST") {
      const fields = input as ItemInput;
      if (!fields.title || !fields.kind) {
        return { status: 400, body: { error: { code: "invalid_item", message: "Not valid.", details: [{ field: "title", message: "Required." }] } } };
      }
      const created = item({ ...(fields as Partial<Item>), kind: fields.kind, title: fields.title });
      items.push(created);
      return { status: 201, body: { item: created } };
    }
    if (!found) {
      return err(404, "not_found", "No such item.");
    }
    if (at2(4) === "complete" && method === "POST") {
      const occurrence = Number(input.occurrence);
      const done = Boolean(input.done);
      if (found.kind === "task") {
        found.state = done ? "done" : "open";
        touch(found);
        return { status: 200, body: { item: found } };
      }
      if (found.kind === "habit") {
        const key = found.id + "@" + dayKey(new Date(occurrence));
        if (done) {
          checks.add(key);
        } else {
          checks.delete(key);
        }
        return { status: 200, body: { item: found, habit: { done, streak: streak(found, new Date(occurrence)) } } };
      }
      return err(400, "not_completable", "Only tasks and habits.");
    }
    if (method === "GET") {
      return {
        status: 200,
        body: {
          item: found,
          firings: [...firings.entries()].filter(([key]) => key.startsWith(found.id + "@")).map(([, firing]) => firing),
          history: [{ at: found.updated_at, by: found.updated_by, summary: "Created" }],
        },
      };
    }
    if (method === "PATCH") {
      Object.assign(found, input);
      touch(found);
      return { status: 200, body: { item: found } };
    }
    if (method === "DELETE") {
      items.splice(items.indexOf(found), 1);
      return { status: 204 };
    }
  }
  if (route === "overrides") {
    const source = method === "DELETE" ? url.searchParams.get("source") : String(input.source);
    const ref = method === "DELETE" ? url.searchParams.get("ref") : String(input.ref);
    const occurrence = Number(method === "DELETE" ? url.searchParams.get("occurrence") : input.occurrence);
    const map = source === "workflow" ? wfOverrides : itemOverrides;
    const key = ref + "@" + occurrence;
    if (method === "DELETE") {
      map.delete(key);
      return { status: 204 };
    }
    map.set(key, { op: input.op === "skip" ? "skip" : "move", to: Number(input.to) || 0 });
    return { status: 200, body: { override: { ...input, by: "owner", at: Date.now() } } };
  }
  if (route === "firings") {
    const key = String(input.item_id) + "@" + Number(input.occurrence);
    const firing = firings.get(key);
    if (!firing) {
      return err(404, "not_found", "No such firing.");
    }
    if (at2(3) === "run") {
      firing.status = "done";
      firing.started_at = Date.now();
      firing.ended_at = Date.now() + 4000;
      firing.session_id = "s_preview";
      return { status: 202, body: { firing } };
    }
    firing.status = "dismissed";
    return { status: 200, body: { firing } };
  }
  if (route === "settings") {
    if (method === "PUT") {
      Object.assign(settings, input);
    }
    return { status: 200, body: settings };
  }
  if (route === "ics") {
    settings.ics.url = "http://127.0.0.1:18300/cal/" + Math.random().toString(36).slice(2, 14) + ".ics";
    return { status: 200, body: { url: settings.ics.url } };
  }
  if (route === "google") {
    const sub = at2(3);
    if (!sub && method === "PUT") {
      const list = (input.calendars || []) as { id: string; selected: boolean }[];
      for (const cal of google.calendars) {
        const next = list.find((c) => c.id === cal.id);
        if (next) {
          cal.selected = next.selected;
        }
      }
      google.target = String(input.target || google.target);
    }
    if (sub === "sync") {
      google.last_sync = Date.now();
      return { status: 202 };
    }
    if (sub === "disconnect") {
      google.state = "off";
      return { status: 204 };
    }
    if (sub === "connect") {
      return { status: 200, body: { auth_url: "#/calendar?google=linked" } };
    }
    if (sub === "client") {
      google.client_configured = true;
      return { status: 204 };
    }
    return { status: 200, body: google };
  }
  return err(404, "not_found", "Unmocked calendar route " + method + " " + url.pathname);
}
