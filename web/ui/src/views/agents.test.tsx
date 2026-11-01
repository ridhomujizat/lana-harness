import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

/* REQ-ROOM-011: the roster pulse and the cards. The agent list and the live
   rooms store are mocked so the pulse counts and the context action can be
   observed (AC-ROOM-019). */
vi.mock("../api/agents", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/agents")>();
  return { ...actual, listAgents: vi.fn(), createAgent: vi.fn() };
});
vi.mock("../api/rooms", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/rooms")>();
  return { ...actual, listRooms: vi.fn(), openRoomsStream: vi.fn() };
});

import { listAgents } from "../api/agents";
import type { Agent } from "../api/agents";
import { openRoomsStream } from "../api/rooms";
import type { RoomRow } from "../api/rooms";
import { AgentsPage } from "./agents";

function agent(overrides: Partial<Agent> = {}): Agent {
  return {
    name: "dana",
    kind: "agent",
    title: "",
    color: "brand",
    model: null,
    policy: "ask",
    skills: [],
    mcp: [],
    mcp_status: [],
    description: "",
    default: false,
    provider: "deepseek",
    permission: "ask",
    workspace: "",
    disabled_tools: [],
    tools_count: 0,
    sessions_count: 0,
    last_active_at: null,
    ...overrides,
  };
}

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

function installRows(rows: RoomRow[]): void {
  vi.mocked(openRoomsStream).mockImplementation((handlers) => {
    handlers.onRooms(rows);
    handlers.onState?.(true);
    return { close: () => {} };
  });
}

beforeEach(() => {
  vi.mocked(listAgents).mockResolvedValue({ agents: [] });
  installRows([]);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("Agents roster (AC-ROOM-019)", () => {
  it("shows the pulse counts and links the waiting action to its room", async () => {
    vi.mocked(listAgents).mockResolvedValue({
      agents: [
        agent({ name: "dana", title: "Finance", color: "success" }),
        agent({ name: "cody", title: "Coding", color: "blue" }),
        agent({ name: "lana", title: "Assistant", color: "brand", default: true }),
        agent({ name: "opi", title: "Ops", color: "gray" }),
      ],
    });
    installRows([
      room({
        name: "dana",
        presence: {
          state: "waiting",
          label: "Waiting for your answer",
          since: 0,
          session_id: "s1",
        },
        turns_7d: 64,
      }),
      room({
        name: "cody",
        presence: {
          state: "working",
          label: "Running bash",
          since: 0,
          session_id: "s2",
        },
        turns_7d: 23,
      }),
      room({
        name: "lana",
        presence: {
          state: "online",
          label: "Active 2 min ago",
          since: 0,
          session_id: "s3",
        },
        turns_7d: 41,
      }),
      room({
        name: "opi",
        presence: {
          state: "online",
          label: "Active 5 min ago",
          since: 0,
          session_id: "s4",
        },
        turns_7d: 7,
      }),
    ]);

    render(<AgentsPage />);

    expect(await screen.findByText("1 working")).toBeTruthy();
    expect(screen.getByText("1 waiting for you")).toBeTruthy();
    expect(screen.getByText("2 online")).toBeTruthy();
    expect(screen.getByText("Team did 135 tasks this week")).toBeTruthy();

    const answer = screen.getByRole("link", {
      name: "Answer dana",
    }) as HTMLAnchorElement;
    expect(answer.getAttribute("href")).toBe("#/chats/dana");
  });

  it("renders a card per agent and the hire card", async () => {
    vi.mocked(listAgents).mockResolvedValue({
      agents: [agent({ name: "dana", title: "Finance" })],
    });
    installRows([room({ name: "dana" })]);

    render(<AgentsPage />);

    expect(await screen.findByRole("link", { name: "dana" })).toBeTruthy();
    expect(screen.getByText("Finance")).toBeTruthy();
    expect(screen.getByText("Hire a new agent")).toBeTruthy();
  });
});
