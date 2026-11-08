import { describe, expect, it } from "vitest";
import type { Entry } from "../api/calendar";
import type { Run, Workflow } from "../api/workflows";
import { workflowEntries } from "./calendar-fallback";
import {
  dayKey,
  entryDays,
  groupWorkflowRuns,
  layoutDayAdaptive,
  minuteOfY,
  movedStart,
  parseDay,
  periodLabel,
  stepAnchor,
  viewRange,
  yOfMinute,
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

function wf(id: string, name: string): Entry["workflow"] {
  return { id, name, guardian: "lana", node_id: "", trigger: "schedule", every_minutes: 0, run_number: 1, run_status: "" };
}

function run(key: string, workflow: Entry["workflow"], start: number, status = "succeeded"): Entry {
  return entry({ key, source: "workflow_run", kind: "workflow", title: workflow!.name, start, end: start + 10000, status, workflow });
}

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

describe("layoutDayAdaptive", () => {
  const ownerSet = () => [
    entry({ key: "olahraga", title: "Olahraga", start: local(2026, 11, 9, 5, 30), end: local(2026, 11, 9, 6, 30) }),
    entry({ key: "mandi", title: "Mandi & sarapan", start: local(2026, 11, 9, 6, 30), end: local(2026, 11, 9, 7, 15) }),
    entry({ key: "github", title: "Github", source: "workflow_slot", start: local(2026, 11, 9, 7), end: local(2026, 11, 9, 7) }),
    entry({ key: "berangkat", title: "Berangkat ke bengkel", start: local(2026, 11, 9, 7, 15), end: local(2026, 11, 9, 7, 15) }),
    entry({ key: "servis", title: "Servis", start: local(2026, 11, 9, 8), end: local(2026, 11, 9, 9, 30) }),
    entry({ key: "belanja", title: "Belanja", start: local(2026, 11, 9, 9, 30), end: local(2026, 11, 9, 11) }),
  ];

  it("stacks the owner's 9 Nov set full width with no overlap and 4px gaps", () => {
    const { slots, hourTops } = layoutDayAdaptive(ownerSet(), parseDay("2026-11-09"));
    const by = Object.fromEntries(slots.map((slot) => [slot.entry.key, slot]));
    /* Sub-hour cards are compact; hour-or-longer cards are two-row. */
    expect(by.olahraga!.compact).toBe(false);
    expect(by.mandi!.compact).toBe(true);
    expect(by.github!.compact).toBe(true);
    expect(by.berangkat!.compact).toBe(true);
    expect(by.servis!.compact).toBe(false);
    expect(by.belanja!.compact).toBe(false);
    /* Nothing long overlaps the 06:30–07:30 stack, so every card is full width. */
    for (const slot of slots) {
      expect(slot.lanes).toBe(1);
    }
    /* Consecutive cards never overlap and stand at least a GAP apart. */
    const sorted = [...slots].sort((a, b) => a.top - b.top);
    for (let i = 1; i < sorted.length; i++) {
      expect(sorted[i]!.top - (sorted[i - 1]!.top + sorted[i - 1]!.height)).toBeGreaterThanOrEqual(4);
    }
    /* The stack overflows hour 7, which grows past the base height. */
    expect(hourTops[8]! - hourTops[7]!).toBeGreaterThan(48);
    /* Long cards sit inside their time span. */
    expect(by.olahraga!.top).toBeGreaterThanOrEqual(yOfMinute(5 * 60 + 30, hourTops));
    expect(by.olahraga!.top + by.olahraga!.height).toBeLessThanOrEqual(yOfMinute(6 * 60 + 30, hourTops));
    expect(by.servis!.top).toBeGreaterThanOrEqual(yOfMinute(8 * 60, hourTops));
    expect(by.servis!.top + by.servis!.height).toBeLessThanOrEqual(yOfMinute(9 * 60 + 30, hourTops));
    /* The short stack keeps its gaps (the natural time position can widen the
       gap, but never below the GAP). */
    expect(by.github!.top - (by.mandi!.top + by.mandi!.height)).toBeGreaterThanOrEqual(4);
    expect(by.berangkat!.top - (by.github!.top + by.github!.height)).toBeGreaterThanOrEqual(4);
  });

  it("maps minutes to y and back with yOfMinute and minuteOfY", () => {
    const { hourTops } = layoutDayAdaptive(ownerSet(), parseDay("2026-11-09"));
    for (const minute of [0, 60, 330, 390, 420, 435, 540, 700, 1440]) {
      expect(minuteOfY(yOfMinute(minute, hourTops), hourTops)).toBeCloseTo(minute, 6);
    }
    expect(minuteOfY(-10, hourTops)).toBe(0);
    expect(minuteOfY(hourTops[24]! + 10, hourTops)).toBe(1440);
    expect(yOfMinute(-10, hourTops)).toBe(0);
    expect(yOfMinute(9999, hourTops)).toBe(hourTops[24]!);
  });

  it("grows one hour for a burst of runs while the others stay 48px", () => {
    const runs = Array.from({ length: 10 }, (_, index) =>
      entry({ key: "r" + index, title: "run " + index, start: local(2026, 11, 9, 18), end: local(2026, 11, 9, 18) })
    );
    const { hourTops } = layoutDayAdaptive(runs, parseDay("2026-11-09"));
    expect(hourTops[19]! - hourTops[18]!).toBeGreaterThan(48);
    for (let hour = 0; hour < 24; hour++) {
      if (hour === 18) {
        continue;
      }
      expect(hourTops[hour + 1]! - hourTops[hour]!).toBe(48);
    }
  });

  it("splits overlapping long cards into lanes", () => {
    const day = parseDay("2026-11-05");
    const a = entry({ key: "a", start: local(2026, 11, 5, 9), end: local(2026, 11, 5, 10) });
    const b = entry({ key: "b", start: local(2026, 11, 5, 9, 30), end: local(2026, 11, 5, 10, 30) });
    const c = entry({ key: "c", start: local(2026, 11, 5, 11), end: local(2026, 11, 5, 12) });
    const { slots } = layoutDayAdaptive([a, b, c], day);
    const by = Object.fromEntries(slots.map((slot) => [slot.entry.key, slot]));
    expect(by.a!.lanes).toBe(2);
    expect(by.b!.lane).toBe(1);
    expect(by.c!.lanes).toBe(1);
  });

  it("shares the width when a long card overlaps the short stack", () => {
    const day = parseDay("2026-11-05");
    const early = entry({ key: "early", start: local(2026, 11, 5, 6, 30), end: local(2026, 11, 5, 7, 15) });
    const meeting = entry({ key: "meeting", start: local(2026, 11, 5, 6), end: local(2026, 11, 5, 8) });
    const { slots } = layoutDayAdaptive([early, meeting], day);
    const by = Object.fromEntries(slots.map((slot) => [slot.entry.key, slot]));
    expect(by.meeting!.lanes).toBe(2);
    expect(by.early!.lanes).toBe(2);
  });
});

describe("groupWorkflowRuns", () => {
  const day = parseDay("2026-11-05");

  it("merges runs of one workflow that touch into one slot", () => {
    const runs = [0, 2, 4, 19].map((offset) =>
      run("r" + offset, wf("wf1", "Nightly report"), local(2026, 11, 5, 18, 1 + offset), offset === 19 ? "failed" : "succeeded")
    );
    const grouped = groupWorkflowRuns(runs);
    expect(grouped).toHaveLength(1);
    expect(grouped[0]!.title).toBe("Nightly report");
    expect(grouped[0]!.group).toHaveLength(4);
    expect(grouped[0]!.start).toBe(local(2026, 11, 5, 18, 1));
    const placed = layoutDayAdaptive(grouped, day).slots;
    expect(placed).toHaveLength(1);
    expect(placed[0]!.lane).toBe(0);
    expect(placed[0]!.lanes).toBe(1);
  });

  it("merges by the 22px visual extent (27.5 minutes at 48px per hour)", () => {
    /* A slot is at least 22px tall, standing for 27.5 minutes: runs 25 minutes
       apart still touch, 40 minutes apart do not. */
    const near = [
      run("n1", wf("wf1", "Alpha"), local(2026, 11, 5, 18, 0)),
      run("n2", wf("wf1", "Alpha"), local(2026, 11, 5, 18, 25)),
    ];
    expect(groupWorkflowRuns(near)).toHaveLength(1);
    const far = [
      run("f1", wf("wf1", "Alpha"), local(2026, 11, 5, 18, 0)),
      run("f2", wf("wf1", "Alpha"), local(2026, 11, 5, 18, 40)),
    ];
    expect(groupWorkflowRuns(far)).toHaveLength(2);
  });

  it("keeps runs of different workflows separate", () => {
    const runs = [
      run("a", wf("wf1", "Alpha"), local(2026, 11, 5, 18, 1)),
      run("b", wf("wf2", "Beta"), local(2026, 11, 5, 18, 1)),
    ];
    const grouped = groupWorkflowRuns(runs);
    expect(grouped).toHaveLength(2);
    expect(grouped.every((entry) => !entry.group)).toBe(true);
  });

  it("leaves a lone run and other entries untouched", () => {
    const solo = run("solo", wf("wf1", "Alpha"), local(2026, 11, 5, 18, 1));
    const event = entry({ key: "e", start: local(2026, 11, 5, 9), end: local(2026, 11, 5, 10) });
    const grouped = groupWorkflowRuns([solo, event]);
    expect(grouped).toHaveLength(2);
    expect(grouped.some((entry) => entry.key === "e")).toBe(true);
    expect(grouped.find((entry) => entry.key === "solo")!.group).toBeUndefined();
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
