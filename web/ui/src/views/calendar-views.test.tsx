import { afterEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { Entry } from "../api/calendar";
import { DayView } from "./calendar-views";
import { parseDay } from "./calendar-model";

/* Day-view rendering (spec #35): short slots draw as one compact line and a
   burst of workflow runs merges into a single grouped slot. */

afterEach(() => {
  window.location.hash = "";
});

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

const local = (h = 0, min = 0) => new Date(2026, 10, 5, h, min).getTime();
const day = parseDay("2026-11-05");

function wf(id: string, name: string): Entry["workflow"] {
  return { id, name, guardian: "lana", node_id: "", trigger: "schedule", every_minutes: 0, run_number: 1, run_status: "" };
}

function run(key: string, workflow: Entry["workflow"], start: number, status = "succeeded"): Entry {
  return entry({
    key,
    source: "workflow_run",
    kind: "workflow",
    title: workflow!.name,
    start,
    end: start + 10000,
    status,
    created_by: "agent:lana",
    workflow,
  });
}

function renderDay(entries: Entry[]) {
  return render(
    <DayView day={day} entries={entries} now={local(12)} onOpen={() => undefined} fallback={false} onDrop={() => undefined} onAdd={() => undefined} />
  );
}

describe("DayView cards", () => {
  it("draws a 10-second run as one compact line with the full title", () => {
    const { container } = renderDay([run("r1", wf("wf1", "Nightly report"), local(18, 1))]);
    const card = container.querySelector<HTMLElement>(".cday-slot .wcard")!;
    expect(card.classList.contains("compact")).toBe(true);
    expect(card.querySelector(".m")).toBeNull();
    expect(screen.getByText("Nightly report")).toBeTruthy();
  });

  it("draws a 45-minute event as one compact line", () => {
    const { container } = renderDay([
      entry({ kind: "event", title: "Mandi & sarapan", start: local(6, 30), end: local(7, 15), created_by: "agent:lana" }),
    ]);
    const card = container.querySelector<HTMLElement>(".cday-slot .wcard")!;
    expect(card.classList.contains("compact")).toBe(true);
    expect(card.querySelector(".m")).toBeNull();
  });

  it("keeps a 60-minute event as two rows", () => {
    const { container } = renderDay([
      entry({ kind: "event", title: "Deep work", start: local(9), end: local(10), created_by: "agent:lana" }),
    ]);
    const card = container.querySelector<HTMLElement>(".cday-slot .wcard")!;
    expect(card.classList.contains("compact")).toBe(false);
    expect(card.querySelector(".m")).toBeTruthy();
  });

  it("grows the grid beyond 24 fixed hours when a stack overflows its hour", () => {
    const { container } = renderDay([
      entry({ key: "mandi", title: "Mandi", start: local(6, 30), end: local(7, 15), created_by: "agent:lana" }),
      entry({ key: "github", title: "Github", source: "workflow_slot", start: local(7), end: local(7) }),
      entry({ key: "berangkat", title: "Berangkat", start: local(7, 15), end: local(7, 15) }),
    ]);
    const grid = container.querySelector<HTMLElement>(".cday-grid")!;
    expect(Number.parseFloat(grid.style.height)).toBeGreaterThan(24 * 48);
    /* Consecutive short cards keep at least a 4px gap. */
    const slotTop = (key: string) =>
      container.querySelector<HTMLElement>(`.cday-slot [data-key="${key}"]`)!.closest<HTMLElement>(".cday-slot")!;
    const mandi = slotTop("mandi");
    const github = slotTop("github");
    const berangkat = slotTop("berangkat");
    expect(Number.parseFloat(github.style.top) - (Number.parseFloat(mandi.style.top) + Number.parseFloat(mandi.style.height))).toBeGreaterThanOrEqual(4);
    expect(Number.parseFloat(berangkat.style.top) - (Number.parseFloat(github.style.top) + Number.parseFloat(github.style.height))).toBeGreaterThanOrEqual(4);
  });

  it("merges 4 runs of one workflow into a single slot that shows '4 runs'", () => {
    const runs = [0, 2, 4, 19].map((offset) =>
      run("r" + offset, wf("wf1", "Nightly report"), local(18, 1 + offset), offset === 19 ? "failed" : "succeeded")
    );
    const { container } = renderDay(runs);
    const slots = container.querySelectorAll<HTMLElement>(".cday-slot");
    expect(slots).toHaveLength(1);
    expect(slots[0]!.style.left).toContain("0%");
    expect(slots[0]!.style.width).toContain("100%");
    expect(screen.getAllByText(/4 runs/).length).toBeGreaterThan(0);
  });

  it("opens the workflow runs page when a grouped slot is clicked", () => {
    const runs = [0, 2, 4, 19].map((offset) =>
      run("r" + offset, wf("wf1", "Nightly report"), local(18, 1 + offset))
    );
    renderDay(runs);
    fireEvent.click(screen.getByRole("button", { name: /Nightly report/ }));
    expect(window.location.hash).toBe("#/workflows/wf1/runs");
  });

  it("keeps runs of different workflows apart", () => {
    const { container } = renderDay([
      run("a", wf("wf1", "Alpha"), local(18, 1)),
      run("b", wf("wf2", "Beta"), local(18, 1)),
    ]);
    expect(container.querySelectorAll(".cday-slot")).toHaveLength(2);
  });
});
