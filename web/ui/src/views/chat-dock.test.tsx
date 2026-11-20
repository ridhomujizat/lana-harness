import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";

/* The chat dock: the store, the window/tab layout, and when a room joins the
   dock. The room calls are mocked so the shared rooms store can be fed. */
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
import {
  listRooms,
  markRoomRead,
  openRoomsStream,
  roomSegments,
} from "../api/rooms";
import type { RoomRow, RoomsStreamHandlers } from "../api/rooms";
import { listEvents } from "../api/sessions";
import type { Route, RouteView } from "../app/router";
import {
  DOCK_STORAGE_KEY,
  dockAdd,
  dockClose,
  dockOpen,
  getDock,
  noteRoomVisit,
  resetDock,
} from "../app/chat-dock-store";
import { ChatDock, dockLayout, isActiveRoom, maxOpenFor } from "./chat-dock";

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

function presence(state: string, label: string) {
  return { state, label, since: 0, session_id: "s1" };
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
  resetDock();
  setWidth(1440);
  vi.mocked(roomSegments).mockResolvedValue({ segments: [], has_more: false });
  vi.mocked(listEvents).mockResolvedValue({ events: [], last_seq: 0 });
  vi.mocked(markRoomRead).mockResolvedValue({ read_at: 0 });
  vi.mocked(openStream).mockImplementation(() => ({ close: () => {} }));
});

afterEach(() => {
  handlers = null;
  vi.clearAllMocks();
});

describe("chat dock store", () => {
  it("opens a room at the front and minimizes windows beyond the limit", () => {
    dockOpen("a", 2);
    dockOpen("b", 2);
    dockOpen("c", 2);
    expect(getDock().items).toEqual([
      { name: "c", open: true },
      { name: "b", open: true },
      { name: "a", open: false },
    ]);
  });

  it("adds a tab without moving a docked room, closes, and saves", () => {
    dockOpen("a", 2);
    dockAdd("a");
    dockAdd("b");
    expect(getDock().items).toEqual([
      { name: "b", open: false },
      { name: "a", open: true },
    ]);
    dockClose("a");
    expect(getDock().items).toEqual([{ name: "b", open: false }]);
    const saved = JSON.parse(window.localStorage.getItem(DOCK_STORAGE_KEY) || "{}");
    expect(saved.items).toEqual([{ name: "b", open: false }]);
  });
});

describe("chat dock layout", () => {
  const byName: Record<string, RoomRow> = {
    a: room({ name: "a" }),
    b: room({ name: "b" }),
    c: room({ name: "c" }),
    d: room({ name: "d" }),
    e: room({ name: "e", presence: presence("waiting", "Needs you") }),
  };

  it("fits windows to the screen width", () => {
    expect(maxOpenFor(1000)).toBe(1);
    expect(maxOpenFor(1440)).toBe(2);
    expect(maxOpenFor(1920)).toBe(3);
  });

  it("puts waiting tabs first and the rest in the overflow", () => {
    const items = ["a", "b", "c", "d", "e"].map((name, index) => ({ name, open: index < 2 }));
    const layout = dockLayout(items, byName, 1440);
    expect(layout.windows.map((row) => row.name)).toEqual(["a", "b"]);
    expect(layout.tabs[0]?.name).toBe("e");
    expect(layout.tabs.length + layout.overflow.length).toBe(3);
    expect(layout.overflow.length).toBeGreaterThan(0);
  });

  it("skips rooms the rows no longer know", () => {
    const layout = dockLayout([{ name: "gone", open: true }], byName, 1440);
    expect(layout.windows).toEqual([]);
  });

  it("counts working, waiting and fresh messages as active", () => {
    const now = 10_000_000;
    expect(isActiveRoom(room({ presence: presence("working", "Working") }), now)).toBe(true);
    expect(isActiveRoom(room(), now)).toBe(false);
    const fresh = room({
      last_message: { role: "assistant", agent: "dana", preview: "hi", at: now - 60_000, session_id: "s", channel: "web" },
    });
    expect(isActiveRoom(fresh, now)).toBe(true);
  });
});

describe("ChatDock", () => {
  it("is hidden on the Chats page and shows windows elsewhere", async () => {
    installRows([room({ name: "dana", presence: presence("working", "Working") })]);
    dockOpen("dana", 2);
    const { rerender } = render(<ChatDock route={route("chat", "chats", { name: "lana" })} />);
    expect(screen.queryByRole("region", { name: "Chat dock" })).toBeNull();
    rerender(<ChatDock route={route("home", "home")} />);
    expect(await screen.findByRole("region", { name: "Chat with dana" })).toBeTruthy();
  });

  it("docks the chat the owner leaves while the agent works", async () => {
    installRows([room({ name: "dana", presence: presence("working", "Working") })]);
    const { rerender } = render(<ChatDock route={route("chat", "chats", { name: "dana" })} />);
    await act(async () => {});
    rerender(<ChatDock route={route("workflows", "workflows")} />);
    expect(await screen.findByRole("region", { name: "Chat with dana" })).toBeTruthy();
    expect(getDock().items).toEqual([{ name: "dana", open: true }]);
  });

  it("does not dock a quiet chat on leave", async () => {
    installRows([room({ name: "dana" })]);
    const { rerender } = render(<ChatDock route={route("chat", "chats", { name: "dana" })} />);
    await act(async () => {});
    rerender(<ChatDock route={route("home", "home")} />);
    await act(async () => {});
    expect(getDock().items).toEqual([]);
  });

  it("adds a recent room as a tab with a peek when a reply arrives", async () => {
    installRows([room({ name: "dana" })]);
    noteRoomVisit("dana");
    render(<ChatDock route={route("home", "home")} />);
    await act(async () => {});
    expect(handlers).not.toBeNull();
    act(() => {
      handlers?.onRoom(
        room({
          name: "dana",
          unread: 1,
          last_message: { role: "assistant", agent: "dana", preview: "Sudah dibayar.", at: Date.now(), session_id: "s", channel: "web" },
        })
      );
    });
    expect(await screen.findByText("Sudah dibayar.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Open the chat with dana" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Reply" }));
    expect(await screen.findByRole("region", { name: "Chat with dana" })).toBeTruthy();
  });

  it("ignores replies from rooms the owner has not chatted with lately", async () => {
    installRows([room({ name: "dana" })]);
    dockAdd("lana");
    render(<ChatDock route={route("home", "home")} />);
    await act(async () => {});
    act(() => {
      handlers?.onRoom(room({ name: "dana", unread: 3 }));
    });
    expect(getDock().items.map((item) => item.name)).toEqual(["lana"]);
  });
});
