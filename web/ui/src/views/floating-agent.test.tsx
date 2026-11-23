import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, within } from "@testing-library/react";

/* Spec 72 floating agent: the store, the room picking rules, the phone snap
   math and the desktop pill, panel and notification. The room calls are
   mocked so the shared rooms store can be fed. */
vi.mock("../api/rooms", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/rooms")>();
  return {
    ...actual,
    listRooms: vi.fn(),
    openRoomsStream: vi.fn(),
    roomSegments: vi.fn(),
    sendToRoom: vi.fn(),
    markRoomRead: vi.fn(),
  };
});
vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return { ...actual, openStream: vi.fn() };
});
vi.mock("../api/sessions", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/sessions")>();
  return { ...actual, listEvents: vi.fn() };
});

import { openStream } from "../api/client";
import { listRooms, markRoomRead, openRoomsStream, roomSegments } from "../api/rooms";
import type { RoomRow, RoomsStreamHandlers } from "../api/rooms";
import { listEvents } from "../api/sessions";
import type { Route, RouteView } from "../app/router";
import {
  clearNotif,
  FA_STORAGE_KEY,
  getFloating,
  notify,
  openRoom,
  resetFloating,
  setBubble,
  showList,
} from "../app/floating-agent-store";
import { FloatingAgent } from "./floating-agent";
import { attentionRooms, othersPending, pickCurrent, stackRooms, stripText } from "./floating-agent-panel";
import { placeFor, snapFor } from "./floating-agent-phone";

function room(overrides: Partial<RoomRow> = {}): RoomRow {
  return {
    name: "dana",
    kind: "agent",
    title: "Finance",
    color: "success",
    members: [],
    lead: "",
    presence: { state: "online", label: "Online", since: 0, session_id: "" },
    last_message: null,
    unread: 0,
    turns_7d: 0,
    last_active_at: 0,
    ...overrides,
  };
}

function presence(state: string, label: string, since = 0) {
  return { state, label, since, session_id: "s1" };
}

function route(view: string, section: string, params: Record<string, string> = {}): Route {
  return { view: view as RouteView, section, params, path: "" };
}

let handlers: RoomsStreamHandlers | null = null;

function installRows(rows: RoomRow[]): void {
  vi.mocked(listRooms).mockResolvedValue(rows);
  vi.mocked(openRoomsStream).mockImplementation((h) => {
    handlers = h;
    h.onRooms(rows);
    h.onState?.(true);
    return { close: () => {} };
  });
}

function setWidth(width: number): void {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: width });
}

beforeEach(() => {
  resetFloating();
  setWidth(1440);
  vi.mocked(roomSegments).mockResolvedValue({ segments: [], has_more: false });
  vi.mocked(listEvents).mockResolvedValue({ events: [], last_seq: 0 });
  vi.mocked(markRoomRead).mockResolvedValue({ read_at: 0 });
  vi.mocked(openStream).mockImplementation(() => ({ close: () => {} }));
});

afterEach(() => {
  handlers = null;
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("floating agent store", () => {
  it("opens a room, goes to the list and saves the state", () => {
    openRoom("dana");
    expect(getFloating()).toMatchObject({ current: "dana", open: true, view: "room" });
    showList();
    expect(getFloating().view).toBe("list");
    const saved = JSON.parse(window.localStorage.getItem(FA_STORAGE_KEY) || "{}");
    expect(saved).toMatchObject({ current: "dana", open: true, view: "list" });
    expect(saved.notif).toBeUndefined();
  });

  it("keeps a waiting card and shrinks a reply card after 8 s", () => {
    vi.useFakeTimers();
    notify("a", "waiting");
    vi.advanceTimersByTime(9000);
    expect(getFloating().notif?.name).toBe("a");
    notify("b", "reply");
    expect(getFloating().notif?.name).toBe("b");
    vi.advanceTimersByTime(9000);
    expect(getFloating().notif).toBeNull();
  });

  it("drops the card of the room that is opened", () => {
    notify("a", "waiting");
    openRoom("a");
    expect(getFloating().notif).toBeNull();
    notify("b", "waiting");
    clearNotif("a");
    expect(getFloating().notif?.name).toBe("b");
  });

  it("clamps the bubble position", () => {
    setBubble({ side: "left", pct: 3 });
    expect(getFloating().bubble).toEqual({ side: "left", pct: 1 });
  });
});

describe("room picking", () => {
  const rows = [
    room({ name: "a", last_message: { role: "agent", agent: "a", preview: "hi", at: 50, session_id: "", channel: "" } }),
    room({ name: "b", presence: presence("working", "Working", 10) }),
    room({ name: "c", presence: presence("waiting", "Needs you") }),
    room({ name: "d", unread: 2 }),
    room({ name: "e", presence: presence("working", "Working", 30) }),
  ];

  it("keeps the saved room, else waiting, working, newest", () => {
    expect(pickCurrent(rows, "d")?.name).toBe("d");
    expect(pickCurrent(rows, "gone")?.name).toBe("c");
    expect(pickCurrent(rows.filter((row) => row.name !== "c"), "")?.name).toBe("b");
    expect(pickCurrent([rows[0] as RoomRow, rows[3] as RoomRow], "")?.name).toBe("a");
  });

  it("lists rooms that want the owner, waiting first", () => {
    expect(attentionRooms(rows, "b").map((row) => row.name)).toEqual(["c", "d"]);
    expect(othersPending(rows, "c")).toBe(1);
  });

  it("stacks active rooms: waiting, then the newest working", () => {
    expect(stackRooms(rows).map((row) => row.name)).toEqual(["c", "e", "b"]);
  });

  it("names what a waiting room needs without repeating Needs you", () => {
    expect(stripText(room({ name: "x", presence: presence("waiting", "Needs you") }))).toBe("x needs you");
    expect(stripText(room({ name: "x", presence: presence("waiting", "Approval: bash") }))).toBe("x · Approval: bash");
  });
});

describe("phone bubble snap", () => {
  it("glides to the nearest side and keeps the height", () => {
    const left = snapFor(40, 400, 390, 844);
    expect(left.side).toBe("left");
    expect(placeFor(left, 390, 844).x).toBe(8);
    expect(Math.round(placeFor(left, 390, 844).y)).toBe(400);
    expect(snapFor(300, 400, 390, 844).side).toBe("right");
  });

  it("sticks to the top or bottom near those edges", () => {
    expect(snapFor(150, 70, 390, 844).side).toBe("top");
    expect(snapFor(150, 740, 390, 844).side).toBe("bottom");
    expect(placeFor({ side: "bottom", pct: 0 }, 390, 844)).toEqual({ x: 8, y: 844 - 8 - 24 - 60 });
  });
});

describe("FloatingAgent on a desktop", () => {
  const rows = [
    room({ name: "dana", presence: presence("working", "Working") }),
    room({ name: "scored", title: "Coding", presence: presence("waiting", "Needs you") }),
    room({ name: "lana", title: "Main", unread: 1 }),
  ];

  it("shows the pill, opens the panel and goes from the list to a room", async () => {
    installRows(rows);
    resetFloating({ current: "dana", tucked: false });
    render(<FloatingAgent route={route("home", "home")} />);
    await screen.findByRole("button", { name: "Open the chat with dana" });
    expect(screen.getByRole("button", { name: /scored, needs you/ })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Open the chat with dana" }));
    expect(getFloating().open).toBe(true);
    /* The room view: back to the list, which shows every room. */
    fireEvent.click(await screen.findByRole("button", { name: /All chats/ }));
    expect(getFloating().view).toBe("list");
    const list = document.querySelector(".fa-list") as HTMLElement;
    fireEvent.click(within(list).getByText("lana"));
    expect(getFloating()).toMatchObject({ current: "lana", view: "room" });
  });

  it("is hidden on the Chats section and follows the chat opened there", async () => {
    installRows(rows);
    const { container } = render(<FloatingAgent route={route("chat", "chats", { name: "lana" })} />);
    expect(container.querySelector(".fa")).toBeNull();
    expect(getFloating().current).toBe("lana");
  });

  it("starts tucked into the edge on any page and slides out on a click", async () => {
    installRows(rows);
    render(<FloatingAgent route={route("home", "home")} />);
    fireEvent.click(await screen.findByRole("button", { name: /Show the floating agent/ }));
    expect(getFloating().tucked).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Tuck the floating agent into the edge" }));
    expect(getFloating().tucked).toBe(true);
  });

  it("grows a card when a room starts waiting and opens it", async () => {
    const calm = rows.map((row) => (row.name === "scored" ? room({ name: "scored" }) : row));
    installRows(calm);
    resetFloating({ current: "dana", tucked: false });
    render(<FloatingAgent route={route("home", "home")} />);
    await screen.findByRole("button", { name: "Open the chat with dana" });
    act(() => {
      handlers?.onRoom(room({ name: "scored", presence: presence("waiting", "Approval: bash") }));
    });
    expect(getFloating().notif).toMatchObject({ name: "scored", kind: "waiting" });
    expect(await screen.findByText("Approval: bash")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(getFloating()).toMatchObject({ current: "scored", open: true, view: "room", notif: null });
  });
});
