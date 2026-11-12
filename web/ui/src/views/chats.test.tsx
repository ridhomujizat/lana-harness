import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";

/* The room calls and the session stream are mocked so the list, the timeline
   dividers, the older-page load, the live activity row and the composer can be
   observed (AC-ROOM-002, AC-ROOM-003, AC-ROOM-006). */
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
import type { StreamHandlers } from "../api/client";
import { openRoomsStream, roomSegments, sendToRoom } from "../api/rooms";
import type { RoomRow, Segment } from "../api/rooms";
import { listEvents } from "../api/sessions";
import type { SessionEvent } from "../api/sessions";
import { ChatsPage } from "./chats";

/* A room name that matches no row keeps the list rendered without mounting the
   timeline, so the list assertions are unambiguous. */
const NO_ROOM = "__none__";

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
    created_at: 5000,
    updated_at: 6000,
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

describe("Chats list (REQ-ROOM-001)", () => {
  it("renders rows with the presence label and the unread badge", async () => {
    installRows([
      room({
        name: "dana",
        title: "Finance",
        unread: 2,
        presence: {
          state: "waiting",
          label: "Waiting for your answer",
          since: 0,
          session_id: "s1",
        },
      }),
      room({
        name: "cody",
        title: "Coding",
        presence: {
          state: "online",
          label: "Active 2 min ago",
          since: 0,
          session_id: "s2",
        },
        last_message: {
          role: "assistant",
          agent: "cody",
          preview: "All tests green.",
          at: Date.now(),
          session_id: "s2",
          channel: "web",
        },
      }),
    ]);

    render(<ChatsPage roomName={NO_ROOM} />);

    expect(await screen.findByText("Waiting for your answer")).toBeTruthy();
    expect(screen.getByLabelText("2 unread")).toBeTruthy();
    expect(screen.getByText("cody")).toBeTruthy();
    expect(screen.getByText("All tests green.")).toBeTruthy();
  });

  it("narrows the rows when Needs you is picked", async () => {
    installRows([
      room({
        name: "dana",
        presence: { state: "waiting", label: "Waiting for your answer", since: 0, session_id: "s1" },
      }),
      room({
        name: "cody",
        presence: { state: "working", label: "Running bash", since: 0, session_id: "s2" },
      }),
      room({
        name: "opi",
        presence: { state: "problem", label: "provider returned 429", since: 0, session_id: "s3" },
      }),
    ]);

    render(<ChatsPage roomName={NO_ROOM} />);
    expect(await screen.findByText("cody")).toBeTruthy();

    fireEvent.click(screen.getByRole("tab", { name: "Needs you 2" }));

    expect(screen.queryByText("cody")).toBeNull();
    expect(screen.getByText("dana")).toBeTruthy();
    expect(screen.getByText("opi")).toBeTruthy();
  });
});

describe("Room timeline (REQ-ROOM-002)", () => {
  it("joins two segments with a via Telegram divider and a compaction divider", async () => {
    vi.mocked(roomSegments).mockResolvedValue({
      segments: [
        segment({
          session_id: "s_tg",
          channel: "telegram",
          address: "12345",
          created_at: 1000,
          updated_at: 2000,
        }),
        segment({
          session_id: "s_web",
          channel: "web",
          address: "owner",
          created_at: 5000,
          updated_at: 6000,
        }),
      ],
      has_more: false,
    });
    vi.mocked(listEvents).mockImplementation(async (id: string) => {
      if (id === "s_tg") {
        return {
          events: [
            ev(1, "user/message", "t1", {
              content: [{ type: "text", text: "hello from telegram" }],
            }),
          ],
          last_seq: 1,
        };
      }
      if (id === "s_web") {
        return {
          events: [
            ev(10, "turn/start", "t2", { turn_id: "t2" }),
            ev(11, "user/message", "t2", {
              content: [{ type: "text", text: "web message" }],
            }),
            ev(12, "compaction/summary", "t2", { summary: "earlier messages" }),
            ev(13, "assistant/message", "t2", {
              content: [{ type: "text", text: "web answer" }],
            }),
            ev(14, "turn/end", "t2", { reason: "end_turn" }),
          ],
          last_seq: 14,
        };
      }
      return { events: [], last_seq: 0 };
    });
    installRows([room({ name: "dana" })]);

    render(<ChatsPage roomName="dana" />);

    expect(await screen.findByText("hello from telegram")).toBeTruthy();
    expect(screen.getByText(/via Telegram/)).toBeTruthy();
    expect(screen.getByText(/summarized earlier messages/)).toBeTruthy();
    expect(screen.getByText("web message")).toBeTruthy();
    expect(screen.getByText("web answer")).toBeTruthy();
  });

  it("skips a segment whose events have no visible item", async () => {
    vi.mocked(roomSegments).mockResolvedValue({
      segments: [
        segment({
          session_id: "s_a",
          channel: "web",
          created_at: 1000,
          updated_at: 2000,
        }),
        segment({
          session_id: "s_empty",
          channel: "cli",
          created_at: 3000,
          updated_at: 4000,
          turn_count: 0,
        }),
        segment({
          session_id: "s_c",
          channel: "telegram",
          created_at: 5000,
          updated_at: 6000,
        }),
      ],
      has_more: false,
    });
    vi.mocked(listEvents).mockImplementation(async (id: string) => {
      if (id === "s_a") {
        return {
          events: [
            ev(1, "user/message", "t1", {
              content: [{ type: "text", text: "first message" }],
            }),
          ],
          last_seq: 1,
        };
      }
      if (id === "s_empty") {
        /* A session with only runtime events makes no visible item. */
        return {
          events: [
            ev(5, "turn/start", "t9", { turn_id: "t9" }),
            ev(6, "turn/end", "t9", { reason: "end_turn" }),
          ],
          last_seq: 6,
        };
      }
      if (id === "s_c") {
        return {
          events: [
            ev(10, "user/message", "t2", {
              content: [{ type: "text", text: "second message" }],
            }),
          ],
          last_seq: 10,
        };
      }
      return { events: [], last_seq: 0 };
    });
    installRows([room({ name: "dana" })]);

    const { container } = render(<ChatsPage roomName="dana" />);

    expect(await screen.findByText("first message")).toBeTruthy();
    expect(screen.getByText("second message")).toBeTruthy();
    /* The empty CLI segment adds no divider, so one divider sits between the
       two visible segments and one opens the first. */
    expect(screen.queryByText(/via CLI/)).toBeNull();
    expect(container.querySelectorAll(".room-divider").length).toBe(2);
  });

  it("shows a lighter New topic divider between same-channel same-day segments", async () => {
    vi.mocked(roomSegments).mockResolvedValue({
      segments: [
        segment({
          session_id: "s_one",
          channel: "web",
          created_at: 1000,
          updated_at: 2000,
        }),
        segment({
          session_id: "s_two",
          channel: "web",
          created_at: 5000,
          updated_at: 6000,
        }),
      ],
      has_more: false,
    });
    vi.mocked(listEvents).mockImplementation(async (id: string) => {
      if (id === "s_one") {
        return {
          events: [
            ev(1, "user/message", "t1", {
              content: [{ type: "text", text: "first topic" }],
            }),
          ],
          last_seq: 1,
        };
      }
      if (id === "s_two") {
        return {
          events: [
            ev(10, "user/message", "t2", {
              content: [{ type: "text", text: "second topic" }],
            }),
          ],
          last_seq: 10,
        };
      }
      return { events: [], last_seq: 0 };
    });
    installRows([room({ name: "dana" })]);

    const { container } = render(<ChatsPage roomName="dana" />);

    expect(await screen.findByText("first topic")).toBeTruthy();
    expect(screen.getByText("second topic")).toBeTruthy();
    /* The second segment keeps the lighter "New topic" divider instead of
       repeating "via Web · <date>". */
    expect(
      screen.getByText("New topic", { selector: ".room-divider-text" })
    ).toBeTruthy();
    expect(container.querySelector(".room-divider-light")).not.toBeNull();
    expect(screen.getAllByText(/via Web/).length).toBe(1);
  });
});

describe("Older segments (AC-ROOM-003)", () => {
  it("requests the previous page with before when the timeline scrolls to the top", async () => {
    vi.mocked(roomSegments).mockImplementation(async (_name, params) => {
      if (params && params.before) {
        return {
          segments: [
            segment({
              session_id: "s_old",
              created_at: 500,
              updated_at: 600,
              turn_count: 0,
            }),
          ],
          has_more: false,
        };
      }
      return {
        segments: [segment({ session_id: "s_new", created_at: 5000, updated_at: 6000 })],
        has_more: true,
      };
    });
    installRows([room({ name: "dana" })]);

    const { container } = render(<ChatsPage roomName="dana" />);
    await waitFor(() => {
      expect(vi.mocked(roomSegments)).toHaveBeenCalledWith("dana", { limit: 5 });
    });

    const scroll = container.querySelector(".room-timeline-scroll") as HTMLElement;
    fireEvent.scroll(scroll, { target: { scrollTop: 0 } });

    await waitFor(() => {
      expect(vi.mocked(roomSegments)).toHaveBeenCalledWith("dana", {
        before: 6000,
        limit: 5,
      });
    });
  });
});

describe("Live turn (AC-ROOM-006)", () => {
  it("shows the agent's avatar, the activity text and the clock while a turn runs", async () => {
    let handlers: StreamHandlers | null = null;
    vi.mocked(openStream).mockImplementation((_id, options) => {
      handlers = options || null;
      return { close: () => {} };
    });
    vi.mocked(roomSegments).mockResolvedValue({
      segments: [segment({ session_id: "s_web" })],
      has_more: false,
    });
    installRows([room({ name: "dana" })]);

    render(<ChatsPage roomName="dana" />);
    await waitFor(() => {
      expect(vi.mocked(openStream)).toHaveBeenCalled();
    });

    act(() => {
      handlers?.onStatus?.({ turn_running: true });
      handlers?.onFrame?.({ kind: "tool_call", name: "bash", call_id: "c1", turn_id: "t1" });
    });

    expect(await screen.findByText(/running bash/)).toBeTruthy();
    const row = document.querySelector(".room-live.room-live-agent");
    /* An agent room shows the agent's avatar in place of the typing dots. */
    expect(row?.querySelector(".av, .agent-avatar, .agent-face")).not.toBeNull();
    expect(row?.querySelector(".typing-dots")).toBeNull();
    expect(row?.querySelector(".room-live-time")?.textContent).toMatch(/s$/);
  });
});

describe("Empty room (REQ-ROOM-002)", () => {
  /* A fresh room shows the face and an invitation instead of a blank timeline. */
  it("shows the room face and title for an agent room", async () => {
    installRows([room({ name: "nadia", title: "Finance" })]);

    const { container } = render(<ChatsPage roomName="nadia" />);

    expect(
      await screen.findByText("Start a conversation with nadia")
    ).toBeTruthy();
    const empty = container.querySelector(".room-empty");
    expect(empty).not.toBeNull();
    expect(empty?.querySelector(".av")).not.toBeNull();
    expect(empty?.textContent).toContain("Finance");
  });

  it("invites the team and names the lead for a team room", async () => {
    installRows([
      room({
        name: "research-squad",
        kind: "team",
        title: "",
        lead: "lana",
        members: ["rasya", "cody"],
      }),
    ]);

    render(<ChatsPage roomName="research-squad" />);

    expect(
      await screen.findByText(
        "research-squad is ready"
      )
    ).toBeTruthy();
  });
});

describe("Room composer (REQ-ROOM-003)", () => {
  it("starts a new session only with /new, like Hermes", async () => {
    vi.mocked(roomSegments).mockResolvedValue({
      segments: [segment({ session_id: "s_web" })],
      has_more: false,
    });
    installRows([room({ name: "dana" })]);

    render(<ChatsPage roomName="dana" />);
    const input = (await screen.findByLabelText("Message dana")) as HTMLTextAreaElement;
    expect(screen.queryByRole("button", { name: "New topic" })).toBeNull();

    /* Typing "/" opens the command menu; Enter on /new arms the next message. */
    fireEvent.change(input, { target: { value: "/" } });
    expect(screen.getByRole("listbox", { name: "Commands" })).toBeTruthy();
    expect(screen.getByRole("option", { name: /New session/ })).toBeTruthy();
    fireEvent.change(input, { target: { value: "/ne" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(vi.mocked(sendToRoom)).not.toHaveBeenCalled();
    expect(await screen.findByText(/Your next message starts a fresh conversation/)).toBeTruthy();
    expect(input.value).toBe("");
    fireEvent.change(input, { target: { value: "halo" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => {
      expect(vi.mocked(sendToRoom)).toHaveBeenCalledWith("dana", { text: "halo", new_topic: true });
    });

    /* "/new <message>" sends it in a fresh session at once. */
    vi.mocked(sendToRoom).mockClear();
    fireEvent.change(input, { target: { value: "/new rencana minggu depan" } });
    fireEvent.click(screen.getByRole("button", { name: "Send message" }));
    await waitFor(() => {
      expect(vi.mocked(sendToRoom)).toHaveBeenCalledWith("dana", { text: "rencana minggu depan", new_topic: true });
    });
  });
});
