import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";

/* The room timeline orders segments by last activity (API-ROOM-002 returns them
   ascending by updated_at). These cases cover the ordering, the send target and
   the last_message reload of BUG 2. The API client is mocked like the other
   view tests. */
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
vi.mock("../api/attachments", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/attachments")>();
  return { ...actual, uploadAttachments: vi.fn() };
});

import { openStream } from "../api/client";
import { openRoomsStream, roomSegments, sendToRoom } from "../api/rooms";
import type { RoomRow, RoomsStreamHandlers, Segment } from "../api/rooms";
import { listEvents } from "../api/sessions";
import type { SessionEvent } from "../api/sessions";
import { ChatsPage } from "./chats";

function room(overrides: Partial<RoomRow> = {}): RoomRow {
  return {
    name: "dana",
    kind: "agent",
    title: "Finance",
    color: "success",
    members: [],
    lead: "",
    presence: {
      state: "online",
      label: "Active 2 min ago",
      since: 0,
      session_id: "",
    },
    last_message: null,
    unread: 0,
    turns_7d: 0,
    last_active_at: 0,
    ...overrides,
  };
}

function segment(overrides: Partial<Segment> = {}): Segment {
  return {
    session_id: "s_web",
    channel: "web",
    address: "owner",
    created_at: 1000,
    updated_at: 1000,
    turn_count: 1,
    status: "idle",
    ...overrides,
  };
}

function ev(
  seq: number,
  kind: string,
  turnId: string,
  payload: Record<string, unknown>
): SessionEvent {
  return { seq, kind, turn_id: turnId, created_at: seq, payload };
}

function userText(seq: number, turnId: string, text: string): SessionEvent {
  return ev(seq, "user/message", turnId, {
    content: [{ type: "text", text }],
  });
}

function installRows(rows: RoomRow[]): void {
  vi.mocked(openRoomsStream).mockImplementation((handlers) => {
    handlers.onRooms(rows);
    handlers.onState?.(true);
    return { close: () => {} };
  });
}

beforeEach(() => {
  vi.mocked(roomSegments).mockResolvedValue({ segments: [], has_more: false });
  vi.mocked(listEvents).mockResolvedValue({ events: [], last_seq: 0 });
  vi.mocked(sendToRoom).mockResolvedValue({
    session_id: "s_web",
    created: false,
    turn: "started",
    turn_id: "t1",
  });
  vi.mocked(openStream).mockImplementation(() => ({ close: () => {} }));
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("Segment order (BUG 2)", () => {
  it("keeps the segments ascending by updated_at, not created_at", async () => {
    /* The page arrives in the wrong order on purpose: the telegram segment was
       created later but updated earlier, so it must render first. */
    vi.mocked(roomSegments).mockResolvedValue({
      segments: [
        segment({
          session_id: "s_cli",
          channel: "cli",
          created_at: 1000,
          updated_at: 5000,
        }),
        segment({
          session_id: "s_tg",
          channel: "telegram",
          created_at: 5000,
          updated_at: 1000,
        }),
      ],
      has_more: false,
    });
    vi.mocked(listEvents).mockImplementation(async (id: string) => {
      if (id === "s_tg") {
        return { events: [userText(1, "t1", "alpha")], last_seq: 1 };
      }
      if (id === "s_cli") {
        return { events: [userText(2, "t2", "beta")], last_seq: 2 };
      }
      return { events: [], last_seq: 0 };
    });
    installRows([room({ name: "dana" })]);

    const { container } = render(<ChatsPage roomName="dana" />);

    await screen.findByText("alpha");
    const text = container.querySelector(".room-timeline")?.textContent || "";
    expect(text.indexOf("alpha")).toBeGreaterThan(-1);
    expect(text.indexOf("alpha")).toBeLessThan(text.indexOf("beta"));

    const dividers = Array.from(
      container.querySelectorAll(".room-divider-text")
    ).map((node) => node.textContent || "");
    expect(dividers[0]).toContain("Telegram");
    expect(dividers[1]).toContain("CLI");
  });
});

describe("Send target (BUG 2)", () => {
  it("moves an older-created session to the end and streams it after send", async () => {
    const streamIds: string[] = [];
    vi.mocked(openStream).mockImplementation((id) => {
      streamIds.push(String(id));
      return { close: () => {} };
    });
    /* The most recent web session is the older-created s_old; the telegram
       segment is newer-created and currently has the latest activity. */
    vi.mocked(roomSegments).mockResolvedValue({
      segments: [
        segment({
          session_id: "s_old",
          channel: "web",
          created_at: 1000,
          updated_at: 2000,
        }),
        segment({
          session_id: "s_new",
          channel: "telegram",
          created_at: 5000,
          updated_at: 3000,
        }),
      ],
      has_more: false,
    });
    vi.mocked(listEvents).mockImplementation(async (id: string) => {
      if (id === "s_old") {
        return { events: [userText(1, "t1", "from old")], last_seq: 1 };
      }
      if (id === "s_new") {
        return { events: [userText(2, "t2", "from new")], last_seq: 2 };
      }
      return { events: [], last_seq: 0 };
    });
    vi.mocked(sendToRoom).mockResolvedValue({
      session_id: "s_old",
      created: false,
      turn: "started",
      turn_id: "t9",
    });
    installRows([room({ name: "dana" })]);

    const { container } = render(<ChatsPage roomName="dana" />);
    await screen.findByText("from old");

    const input = (await screen.findByLabelText(
      "Message dana"
    )) as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "halo" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));

    await waitFor(() => {
      expect(vi.mocked(sendToRoom)).toHaveBeenCalled();
    });
    /* The stream subscription follows the session that just got the message. */
    await waitFor(() => {
      expect(streamIds[streamIds.length - 1]).toBe("s_old");
    });

    const text = container.querySelector(".room-timeline")?.textContent || "";
    expect(text.indexOf("from old")).toBeGreaterThan(text.indexOf("from new"));
  });
});

describe("Last message change (BUG 2)", () => {
  it("moves a loaded non-last session to the end and reloads its events", async () => {
    const stream = { handlers: null as RoomsStreamHandlers | null };
    vi.mocked(openRoomsStream).mockImplementation((handlers) => {
      stream.handlers = handlers;
      handlers.onRooms([room({ name: "dana", last_message: null })]);
      handlers.onState?.(true);
      return { close: () => {} };
    });
    let page = 0;
    vi.mocked(roomSegments).mockImplementation(async () => {
      page += 1;
      if (page === 1) {
        return {
          segments: [
            segment({
              session_id: "s_a",
              channel: "web",
              created_at: 1000,
              updated_at: 1000,
            }),
            segment({
              session_id: "s_b",
              channel: "telegram",
              created_at: 5000,
              updated_at: 5000,
            }),
          ],
          has_more: false,
        };
      }
      /* After the new activity, s_a is the most recently updated. */
      return {
        segments: [
          segment({
            session_id: "s_b",
            channel: "telegram",
            created_at: 5000,
            updated_at: 5000,
          }),
          segment({
            session_id: "s_a",
            channel: "web",
            created_at: 1000,
            updated_at: 9000,
          }),
        ],
        has_more: false,
      };
    });
    vi.mocked(listEvents).mockImplementation(async (id: string) => {
      if (id === "s_a") {
        return { events: [userText(1, "t1", "alpha")], last_seq: 1 };
      }
      if (id === "s_b") {
        return { events: [userText(2, "t2", "beta")], last_seq: 2 };
      }
      return { events: [], last_seq: 0 };
    });
    const streamIds: string[] = [];
    vi.mocked(openStream).mockImplementation((id) => {
      streamIds.push(String(id));
      return { close: () => {} };
    });

    const { container } = render(<ChatsPage roomName="dana" />);
    await screen.findByText("alpha");
    const before = container.querySelector(".room-timeline")?.textContent || "";
    expect(before.indexOf("alpha")).toBeLessThan(before.indexOf("beta"));

    act(() => {
      stream.handlers?.onRooms([
        room({
          name: "dana",
          last_message: {
            role: "user",
            agent: "dana",
            preview: "alpha again",
            at: Date.now(),
            session_id: "s_a",
            channel: "web",
          },
        }),
      ]);
    });

    await waitFor(() => {
      const text = container.querySelector(".room-timeline")?.textContent || "";
      expect(text.indexOf("alpha")).toBeGreaterThan(text.indexOf("beta"));
    });

    /* The new last session was reloaded and the stream follows it. */
    const reloads = vi
      .mocked(listEvents)
      .mock.calls.filter((call) => call[0] === "s_a").length;
    expect(reloads).toBeGreaterThan(1);
    await waitFor(() => {
      expect(streamIds[streamIds.length - 1]).toBe("s_a");
    });
  });
});
