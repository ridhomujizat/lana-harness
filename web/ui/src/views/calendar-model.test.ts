import { describe, expect, it } from "vitest";
import type { Entry } from "../api/calendar";
import type { Run, Workflow } from "../api/workflows";
import { workflowEntries } from "./calendar-fallback";
import {
  dayKey,
  entryDays,
  layoutDay,
  movedStart,
  parseDay,
  periodLabel,
  stepAnchor,
  viewRange,
} from "./calendar-model";

/* Spec #35 web: date math of the calendar views and the workflow fallback
   (REQ-CAL-008, AC-CAL-015). */

function entry(fields: Partial<Entry>): Entry {
  return {
    key: "k" + Math.random(),
    source: "item",
    kind: "event",
    title: "x",
    start: 0,
    end: 0,
    all_day: false,
    date: "",
    end_date: "",
    color: "",
    status: "",
    moved: false,
    occurrence: 0,
    item_id: "i",
    repeating: false,
    assignee: "owner",
    created_by: "owner",
    action: null,
    firing: null,
    habit: null,
    workflow: null,
    google: null,
    readonly: false,
    ...fields,
  };
}

const local = (y: number, m: number, d: number, h = 0, min = 0) => new Date(y, m - 1, d, h, min).getTime();

describe("viewRange", () => {
  it("lays out a Monday-start week", () => {
    const range = viewRange("week", parseDay("2026-11-05"), 1);
    expect(dayKey(range.from)).toBe("2026-11-02");
    expect(range.days).toHaveLength(7);
    expect(dayKey(range.to)).toBe("2026-11-09");
  });

  it("lays out a Sunday-start week", () => {
    expect(dayKey(viewRange("week", parseDay("2026-11-05"), 0).from)).toBe("2026-11-01");
  });

  it("trims the month grid to five weeks when the sixth is all next month", () => {
    /* February 2027 starts on a Monday: exactly 4 weeks, so 5 rows remain. */
    const feb = viewRange("month", parseDay("2027-02-10"), 1);
    expect(feb.days).toHaveLength(35);
    const nov = viewRange("month", parseDay("2026-11-05"), 1);
    expect(dayKey(nov.from)).toBe("2026-10-26");
    expect(nov.days).toHaveLength(42);
  });

  it("steps months without overflowing short months", () => {
    expect(dayKey(stepAnchor("month", parseDay("2026-01-31"), 1))).toBe("2026-02-28");
    expect(dayKey(stepAnchor("week", parseDay("2026-11-05"), -1))).toBe("2026-10-29");
  });

  it("labels periods", () => {
    const anchor = parseDay("2026-11-05");
    expect(periodLabel("month", anchor, viewRange("month", anchor, 1))).toBe("November 2026");
    expect(periodLabel("week", anchor, viewRange("week", anchor, 1))).toBe("2–8 Nov 2026");
    const edge = parseDay("2026-12-30");
    expect(periodLabel("week", edge, viewRange("week", edge, 1))).toBe("28 Dec 2026 – 3 Jan 2027");
  });
});

describe("entryDays", () => {
  it("covers an all-day range inclusively", () => {
    expect(entryDays(entry({ all_day: true, date: "2026-11-13", end_date: "2026-11-15" }))).toEqual([
      "2026-11-13",
      "2026-11-14",
      "2026-11-15",
    ]);
  });

  it("puts a timed event that ends at midnight on one day", () => {
    const e = entry({ start: local(2026, 11, 5, 22), end: local(2026, 11, 6, 0) });
    expect(entryDays(e)).toEqual(["2026-11-05"]);
  });

  it("puts a task without a time on its due date", () => {
    expect(entryDays(entry({ kind: "task", date: "2026-11-06", start: local(2026, 11, 6) }))).toEqual(["2026-11-06"]);
  });
});

describe("layoutDay", () => {
  it("splits overlapping entries into lanes", () => {
    const day = parseDay("2026-11-05");
    const a = entry({ key: "a", start: local(2026, 11, 5, 9), end: local(2026, 11, 5, 10) });
    const b = entry({ key: "b", start: local(2026, 11, 5, 9, 30), end: local(2026, 11, 5, 10, 30) });
    const c = entry({ key: "c", start: local(2026, 11, 5, 11), end: local(2026, 11, 5, 12) });
    const placed = layoutDay([a, b, c], day, 48);
    const by = Object.fromEntries(placed.map((p) => [p.entry.key, p]));
    expect(by.a!.top).toBe(9 * 48);
    expect(by.a!.lanes).toBe(2);
    expect(by.b!.lane).toBe(1);
    expect(by.c!.lanes).toBe(1);
    expect(by.c!.height).toBe(48);
  });
});

describe("movedStart", () => {
  it("keeps the time of day on a day drop and takes the slot time on a grid drop", () => {
    const e = entry({ start: local(2026, 11, 3, 7) });
    expect(movedStart(e, parseDay("2026-11-04"))).toBe(local(2026, 11, 4, 7));
    expect(movedStart(e, parseDay("2026-11-04"), 9 * 60 + 15)).toBe(local(2026, 11, 4, 9, 15));
  });
});

describe("workflowEntries (fallback)", () => {
  const wf = (nodes: Workflow["graph"]["nodes"]): Workflow =>
    ({
      id: "wf",
      name: "Daily research",
      guardian: "lana",
      status: "active",
      graph: { nodes, edges: [] },
    }) as unknown as Workflow;

  it("shows schedule slots only in the future part of the range", () => {
    const now = local(2026, 11, 5, 12);
    const list = workflowEntries(
      [wf([{ id: "t", type: "trigger.schedule", name: "", position: { x: 0, y: 0 }, params: { cron: "0 7 * * *", timezone: Intl.DateTimeFormat().resolvedOptions().timeZone } }])],
      [],
      local(2026, 11, 2),
      local(2026, 11, 9),
      now
    );
    expect(list.map((e) => dayKey(new Date(e.start)))).toEqual(["2026-11-06", "2026-11-07", "2026-11-08"]);
    expect(list.every((e) => e.source === "workflow_slot")).toBe(true);
  });

  it("AC-CAL-015: collapses a 5-minute interval to one row per day", () => {
    const now = local(2026, 11, 5, 0, 1);
    const list = workflowEntries(
      [wf([{ id: "t", type: "trigger.interval", name: "", position: { x: 0, y: 0 }, params: { minutes: 5 } }])],
      [],
      local(2026, 11, 5),
      local(2026, 11, 6),
      now
    );
    expect(list).toHaveLength(1);
    expect(list[0]!.all_day).toBe(true);
    expect(list[0]!.workflow?.every_minutes).toBe(5);
  });

  it("applies a moved override and lists runs in the range", () => {
    const now = local(2026, 11, 5, 12);
    const at = local(2026, 11, 6, 7);
    const run = { id: "r1", workflow_id: "wf", number: 3, status: "failed", started_at: local(2026, 11, 4, 7), ended_at: local(2026, 11, 4, 7, 2), trigger: "schedule" } as Run;
    const list = workflowEntries(
      [wf([{ id: "t", type: "trigger.schedule", name: "", position: { x: 0, y: 0 }, params: { cron: "0 7 * * *", timezone: Intl.DateTimeFormat().resolvedOptions().timeZone } }])],
      [run],
      local(2026, 11, 2),
      local(2026, 11, 7),
      now,
      new Map([["wf/t@" + at, { op: "move" as const, to: local(2026, 11, 6, 9, 30) }]])
    );
    const slot = list.find((e) => e.source === "workflow_slot")!;
    expect(slot.moved).toBe(true);
    expect(slot.start).toBe(local(2026, 11, 6, 9, 30));
    expect(slot.occurrence).toBe(at);
    const runEntry = list.find((e) => e.source === "workflow_run")!;
    expect(runEntry.status).toBe("failed");
    expect(runEntry.readonly).toBe(true);
  });
});
