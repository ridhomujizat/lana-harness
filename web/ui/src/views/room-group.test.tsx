import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

/* The group room of REQ-ROOM-008 (AC-ROOM-012). The team turn items come from
   teamSteps; the composer mention chips and the "@" listbox live in the room
   view, so the room calls are mocked like the chats test. */
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
import { openRoomsStream, roomSegments } from "../api/rooms";
import type { RoomRow } from "../api/rooms";
import { listEvents } from "../api/sessions";
import type { SessionEvent } from "../api/sessions";
import type { FaceRef } from "./presence";
import { RoomGroup } from "./room-group";
import { RoomView } from "./room-timeline";
import { buildTurns } from "./turn";

function teamRoom(overrides: Partial<RoomRow> = {}): RoomRow {
  return {
    name: "career-plan",
    kind: "team",
    title: "Career plan",
    color: "brand",
    members: ["karin", "dana"],
    lead: "lana",
    presence: { state: "working", label: "Running task", since: 0, session_id: "s1" },
    last_message: null,
    unread: 0,
    turns_7d: 0,
    last_active_at: 0,
    ...overrides,
  };
}

const FACES: Record<string, FaceRef> = {
  lana: { name: "lana", color: "brand" },
  karin: { name: "karin", color: "warning" },
  dana: { name: "dana", color: "success" },
};

function ev(
  seq: number,
  kind: string,
  turnId: string,
  payload: Record<string, unknown>
): SessionEvent {
  return { seq, kind, turn_id: turnId, created_at: seq * 1000, payload };
}

const TASK_RESULT = {
  session_id: "s_karin",
  agent: "karin",
  status: "completed",
  answer: "Gaji pasar untuk posisi ini Rp 24–32 jt.",
  steps: [],
  tool_calls: 3,
  duration_ms: 1200,
};

function delegationEvents(): SessionEvent[] {
  return [
    ev(1, "turn/start", "t1", { turn_id: "t1" }),
    ev(2, "assistant/message", "t1", {
      content: [{ type: "text", text: "Oke, saya bagi tugasku." }],
    }),
    ev(3, "tool/call", "t1", {
      call_id: "c1",
      name: "task",
      input: { agent: "karin", prompt: "cek gaji pasar", title: "Cek gaji" },
    }),
    ev(4, "tool/result", "t1", { call_id: "c1", status: "success", output: TASK_RESULT }),
    ev(5, "assistant/message", "t1", {
      content: [{ type: "text", text: "Kesimpulan: layak." }],
    }),
    ev(6, "turn/end", "t1", { reason: "end_turn" }),
  ];
}

beforeEach(() => {
  vi.mocked(openRoomsStream).mockImplementation((handlers) => {
    handlers.onRooms([]);
    handlers.onState?.(true);
    return { close: () => {} };
  });
  vi.mocked(roomSegments).mockResolvedValue({ segments: [], has_more: false });
  vi.mocked(listEvents).mockResolvedValue({ events: [], last_seq: 0 });
  vi.mocked(openStream).mockImplementation(() => ({ close: () => {} }));
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("Group room items (AC-ROOM-012)", () => {
  it("shows the handoff chip, the member answer, and the lead answer", () => {
    const turn = buildTurns(delegationEvents())[0];
    if (!turn || turn.kind !== "turn") {
      throw new Error("expected a turn");
    }

    render(<RoomGroup turn={turn.turn} room={teamRoom()} faces={FACES} />);

    expect(screen.getByLabelText("karin · Cek gaji · done")).toBeTruthy();
    expect(screen.getAllByText("karin").length).toBeGreaterThan(0);
    expect(screen.getByText(/Gaji pasar untuk posisi ini/)).toBeTruthy();
    expect(screen.getByText(/replying to lana/)).toBeTruthy();
    expect(screen.getByText("Kesimpulan: layak.")).toBeTruthy();
    expect(screen.getAllByText("lead").length).toBeGreaterThan(0);
  });

  it("shows the working chip and the live row while the member works", () => {
    const events = [
      ev(1, "turn/start", "t2", { turn_id: "t2" }),
      ev(2, "assistant/message", "t2", {
        content: [{ type: "text", text: "Saya minta Karin cek." }],
      }),
      ev(3, "tool/call", "t2", {
        call_id: "c2",
        name: "task",
        input: { agent: "karin", prompt: "cek gaji pasar", title: "Cek gaji" },
      }),
    ];
    const turn = buildTurns(events, { running: true, turnId: "t2" })[0];
    if (!turn || turn.kind !== "turn") {
      throw new Error("expected a turn");
    }

    render(<RoomGroup turn={turn.turn} room={teamRoom()} faces={FACES} />);

    expect(screen.getByLabelText("karin · Cek gaji · working")).toBeTruthy();
    expect(screen.getByText(/is working on Cek gaji/)).toBeTruthy();
  });
});

describe("Group composer mentions (REQ-ROOM-008)", () => {
  it("inserts a mention when a chip is clicked", async () => {
    render(<RoomView room={teamRoom()} />);

    const input = (await screen.findByLabelText(
      "Message the team"
    )) as HTMLTextAreaElement;
    fireEvent.click(screen.getByRole("button", { name: "@karin" }));

    expect(input.value).toBe("@karin ");
  });

  it("picks the member with Enter from the @ listbox", async () => {
    render(<RoomView room={teamRoom()} />);

    const input = (await screen.findByLabelText(
      "Message the team"
    )) as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "@ka" } });
    expect(screen.getByRole("option", { name: "@karin" })).toBeTruthy();

    fireEvent.keyDown(input, { key: "Enter" });

    expect(input.value).toBe("@karin ");
  });
});
