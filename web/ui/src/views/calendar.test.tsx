import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

/* Spec #35 web acceptance: AC-CAL-017 (drag a workflow slot to another day),
   AC-CAL-018 (move from the keyboard), AC-CAL-024 (a stream change reloads),
   and the fallback for daemons without a calendar backend. The network is
   answered by the stateful preview fixtures; the streams are captured. */
vi.mock("../api/calendar", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/calendar")>();
  return { ...actual, openCalendarStream: vi.fn() };
});
vi.mock("../api/workflows", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/workflows")>();
  return { ...actual, openWorkflowStream: vi.fn(() => ({ close: () => undefined })) };
});
/* The calendar resolves agent labels from the shared rooms store, so that store
   is stubbed with no rows and the ID stays the fallback label. */
vi.mock("../api/rooms", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/rooms")>();
  return {
    ...actual,
    listRooms: vi.fn().mockResolvedValue([]),
    openRoomsStream: vi.fn(() => ({ close: () => {} })),
  };
});

import { openCalendarStream } from "../api/calendar";
import type { CalendarChange, Entry } from "../api/calendar";
import { mockApi } from "../preview/fixtures";
import { ToastProvider } from "../ui/gr8r";
import { CalendarPage } from "./calendar";
import { addDays, dayKey, startOfDay } from "./calendar-model";

let streamHandler: ((change: CalendarChange) => void) | null = null;
vi.mocked(openCalendarStream).mockImplementation((handler) => {
  streamHandler = handler;
  return { close: () => undefined };
});

let calendarMissing = false;
const calls: { method: string; path: string; body: unknown }[] = [];

beforeEach(() => {
  calendarMissing = false;
  calls.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      const method = (init?.method || "GET").toUpperCase();
      const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
      calls.push({ method, path: url.pathname + url.search, body });
      if (calendarMissing && url.pathname.startsWith("/v1/calendar")) {
        return new Response(JSON.stringify({ error: { code: "not_found", message: "404 page not found" } }), { status: 404 });
      }
      const result = mockApi(method, url, body);
      if (result.status === 204) {
        return new Response(null, { status: 204 });
      }
      return new Response(JSON.stringify(result.body ?? null), {
        status: result.status,
        headers: { "Content-Type": "application/json" },
      });
    })
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
  streamHandler = null;
});

const today = startOfDay(new Date());
const weekParams = { view: "week", date: dayKey(today) };

function renderPage(params: Record<string, string> = weekParams) {
  return render(
    <ToastProvider>
      <CalendarPage params={params} />
    </ToastProvider>
  );
}

const entriesCalls = () => calls.filter((call) => call.path.startsWith("/v1/calendar/entries"));

function timedEntry(title: string, start: number, end: number): Entry {
  return {
    key: title + ":" + start,
    source: "item",
    kind: "event",
    title,
    start,
    end,
    all_day: false,
    date: dayKey(new Date(start)),
    end_date: dayKey(new Date(end)),
    color: "",
    status: "",
    moved: false,
    occurrence: start,
    item_id: "ci_" + title,
    repeating: false,
    assignee: "owner",
    created_by: "owner",
    action: null,
    firing: null,
    habit: null,
    workflow: null,
    google: null,
    readonly: false,
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

/* A gate the test releases by hand, so two loads can be made to finish in the
   order the test wants. */
function gate(): { promise: Promise<void>; release: () => void } {
  let release!: () => void;
  const promise = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { promise, release };
}

describe("CalendarPage", () => {
  it("shows items, workflow slots and runs together", async () => {
    renderPage();
    expect(await screen.findByText("Dentist")).toBeTruthy();
    expect(screen.getAllByText("Riset harian").length).toBeGreaterThan(0);
    expect(screen.getByText(/missed while lana was off/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /^Filter/ }));
    fireEvent.click(await screen.findByRole("menuitemcheckbox", { name: "Workflows" }));
    await waitFor(() => expect(screen.queryAllByText("Riset harian")).toHaveLength(0));
    expect(screen.getByText("1 hidden")).toBeTruthy();
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: "Event" }));
    await waitFor(() => expect(screen.queryByText("Dentist")).toBeNull());
    fireEvent.click(screen.getByRole("menuitem", { name: "Show everything" }));
    expect(await screen.findByText("Dentist")).toBeTruthy();
  });

  it("AC-CAL-017: dropping a workflow slot on another day saves a move override", async () => {
    renderPage({ view: "week", date: dayKey(addDays(today, 7)) });
    await waitFor(() => expect(document.querySelectorAll(".wcard.slot").length).toBeGreaterThan(1));
    const slot = document.querySelector<HTMLElement>(".wcard.slot")!;
    const key = slot.dataset.key!;
    const columns = document.querySelectorAll<HTMLElement>(".wcol-b");
    const target = columns[columns.length - 1]!;
    const store = new Map<string, string>();
    const dataTransfer = {
      setData: (type: string, value: string) => store.set(type, value),
      getData: (type: string) => store.get(type) || "",
      get types() {
        return [...store.keys()];
      },
      effectAllowed: "",
      dropEffect: "",
    };
    fireEvent.dragStart(slot, { dataTransfer });
    fireEvent.dragOver(target, { dataTransfer });
    fireEvent.drop(target, { dataTransfer });
    await waitFor(() => expect(calls.some((call) => call.method === "POST" && call.path === "/v1/calendar/overrides")).toBe(true));
    const saved = calls.find((call) => call.path === "/v1/calendar/overrides")!.body as Record<string, unknown>;
    expect(saved.source).toBe("workflow");
    expect(saved.op).toBe("move");
    expect(key).toContain(String(saved.occurrence));
    await waitFor(() => expect(document.querySelector('.wcard.slot[aria-label*="moved"]')).toBeTruthy());
  });

  it("AC-CAL-018: an item can be moved from the keyboard through Move…", async () => {
    renderPage({ view: "day", date: dayKey(today) });
    const dentist = await screen.findByRole("button", { name: /^Dentist/ });
    fireEvent.click(dentist);
    const drawer = await screen.findByRole("dialog", { name: /Event: Dentist/ });
    fireEvent.click(within(drawer).getByRole("button", { name: "More" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Move/ }));
    const move = await screen.findByRole("dialog", { name: /Move/ });
    fireEvent.change(within(move).getByLabelText("Time"), { target: { value: "16:30" } });
    fireEvent.click(within(move).getByRole("button", { name: "Move" }));
    await waitFor(() => expect(calls.some((call) => call.method === "PATCH")).toBe(true));
    const patch = calls.find((call) => call.method === "PATCH")!.body as { start: number; end: number };
    expect(new Date(patch.start).getHours()).toBe(16);
    expect(new Date(patch.start).getMinutes()).toBe(30);
    expect(patch.end - patch.start).toBe(3600000);
  });

  it("edits an item inline in the side drawer, with no modal", async () => {
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /^Weekly spending recap/ }));
    const drawer = await screen.findByRole("dialog", { name: /Reminder: Weekly spending recap/ });
    expect(within(drawer).getByText("Added by")).toBeTruthy();
    const title = await within(drawer).findByLabelText("Title");
    fireEvent.change(title, { target: { value: "Weekly spending and savings recap" } });
    fireEvent.change(within(drawer).getByLabelText("Notes"), { target: { value: "Include the coffee budget." } });
    expect(within(drawer).getByText("Unsaved changes")).toBeTruthy();
    fireEvent.click(within(drawer).getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls.some((call) => call.method === "PATCH")).toBe(true));
    const body = calls.find((call) => call.method === "PATCH")!.body as Record<string, unknown>;
    expect(body.title).toBe("Weekly spending and savings recap");
    expect(body.notes).toBe("Include the coffee budget.");
    expect(body.kind).toBeUndefined();
    expect((body.action as { type: string }).type).toBe("agent_prompt");
    expect(screen.queryByRole("dialog", { name: /^Edit/ })).toBeNull();
  });

  it("AC-CAL-024: a stream change reloads the visible range", async () => {
    renderPage();
    await screen.findByText("Dentist");
    const before = entriesCalls().length;
    await act(async () => {
      streamHandler?.({ what: "item", item_id: "ci_x" });
      await new Promise((resolve) => setTimeout(resolve, 400));
    });
    expect(entriesCalls().length).toBeGreaterThan(before);
  });

  it("keeps the newer range's entries when two loads resolve out of order", async () => {
    const gates: Array<{ from: number; release: () => void }> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), "http://localhost");
        if (url.pathname.startsWith("/v1/calendar/entries")) {
          const from = Number(url.searchParams.get("from"));
          const held = gate();
          gates.push({ from, release: held.release });
          await held.promise;
          const title = dayKey(new Date(from)) === "2026-11-09" ? "New week" : "Old week";
          return json({
            entries: [timedEntry(title, from + 9 * 3600000, from + 10 * 3600000)],
            timezone: "UTC",
            week_start: 1,
            now: Date.now(),
            google: { state: "off" },
            missed: 0,
          });
        }
        return json({ workflows: [] });
      })
    );

    const view = renderPage({ view: "week", date: "2026-11-02" });
    await waitFor(() => expect(gates).toHaveLength(1));
    view.rerender(
      <ToastProvider>
        <CalendarPage params={{ view: "week", date: "2026-11-09" }} />
      </ToastProvider>
    );
    await waitFor(() => expect(gates).toHaveLength(2));

    /* The newer range lands first… */
    gates[1]!.release();
    expect(await screen.findByText("New week")).toBeTruthy();
    /* …then the older range lands last and must not overwrite it. */
    await act(async () => {
      gates[0]!.release();
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(screen.queryByText("Old week")).toBeNull();
    expect(screen.getByText("New week")).toBeTruthy();
  });

  it("shows the newly included day's entries after switching week start, with no refresh", async () => {
    let serverWeekStart = 1;
    const A_FROM = new Date(2026, 10, 2).getTime();
    const held: Array<() => void> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), "http://localhost");
        const method = (init?.method || "GET").toUpperCase();
        const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
        if (url.pathname === "/v1/calendar/entries") {
          const from = Number(url.searchParams.get("from"));
          const to = Number(url.searchParams.get("to"));
          /* The Mon-start range is held: it stands for the reload that the
             settings save kicks off while the range is still the old one. */
          if (from === A_FROM) {
            const pending = gate();
            held.push(pending.release);
            await pending.promise;
          }
          const nov9 = new Date(2026, 10, 9, 9).getTime();
          const entries = nov9 >= from && nov9 < to ? [timedEntry("Rapat Senin", nov9, nov9 + 3600000)] : [];
          return json({ entries, timezone: "UTC", week_start: serverWeekStart, now: Date.now(), google: { state: "off" }, missed: 0 });
        }
        if (url.pathname === "/v1/calendar/settings" && method === "PUT") {
          serverWeekStart = Number((body as { week_start?: number }).week_start);
        }
        const result = mockApi(method, url, body);
        if (result.status === 204) {
          return new Response(null, { status: 204 });
        }
        return json(result.body ?? null, result.status);
      })
    );

    /* Sunday 8 Nov: Monday-start shows 2–8 Nov, Sunday-start shows 8–14 Nov. */
    renderPage({ view: "week", date: "2026-11-08" });
    await waitFor(() => expect(held).toHaveLength(1));
    expect(screen.queryByText("Rapat Senin")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Calendar settings" }));
    fireEvent.click(await screen.findByRole("tab", { name: "Sunday" }));
    await waitFor(() => expect(held).toHaveLength(2));

    /* The settings reload (old range) lands first: the range moves to Sunday
       and 9 Nov loads. */
    held[1]!();
    expect(await screen.findByText("Rapat Senin")).toBeTruthy();
    /* The initial Mon-start load lands last; it must not wipe 9 Nov. */
    await act(async () => {
      held[0]!();
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(screen.getByText("Rapat Senin")).toBeTruthy();
  });

  it("creates a task from the New menu", async () => {
    renderPage();
    await screen.findByText("Dentist");
    fireEvent.click(screen.getByRole("button", { name: /^New/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Task" }));
    const dialog = await screen.findByRole("dialog", { name: "New task" });
    fireEvent.change(within(dialog).getByLabelText("Title"), { target: { value: "Buy coffee beans" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    await waitFor(() => expect(calls.some((call) => call.method === "POST" && call.path === "/v1/calendar/items")).toBe(true));
    const body = calls.find((call) => call.path === "/v1/calendar/items")!.body as Record<string, unknown>;
    expect(body.kind).toBe("task");
    expect(body.date).toBe(dayKey(today));
  });

  it("falls back to workflow data when the daemon has no calendar backend", async () => {
    calendarMissing = true;
    renderPage();
    expect(await screen.findByText(/calendar service is not running/)).toBeTruthy();
    expect(screen.queryByText("Dentist")).toBeNull();
    expect(screen.getByRole("button", { name: /^New/ })).toHaveProperty("disabled", true);
  });
});
