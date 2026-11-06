import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

/* REQ-ROOM-010: the alive agent profile. The overview call and the live rooms
   store are mocked so the steps, the week, the activity filters, the job empty
   state and the team variant can be observed (AC-ROOM-017, AC-ROOM-018). */
vi.mock("../api/rooms", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/rooms")>();
  return {
    ...actual,
    getAgentOverview: vi.fn(),
    listRooms: vi.fn(),
    openRoomsStream: vi.fn(),
  };
});

import { getAgentOverview, openRoomsStream } from "../api/rooms";
import type { AgentOverview as AgentOverviewData, RoomRow } from "../api/rooms";
import type { Agent } from "../api/agents";
import { AgentOverview } from "./agent-overview";

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

function agent(overrides: Partial<Agent> = {}): Agent {
  return {
    name: "dana",
    kind: "agent",
    title: "Finance",
    color: "success",
    model: "deepseek-chat",
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

const NOW = Date.now();

function overview(overrides: Partial<AgentOverviewData> = {}): AgentOverviewData {
  return {
    name: "dana",
    kind: "agent",
    title: "Finance",
    color: "success",
    persona_line: "Saya mencatat setiap rupiah.",
    created_at: NOW - 21 * 86400000,
    presence: {
      state: "working",
      label: "Running gmail.search",
      since: NOW,
      session_id: "s1",
    },
    now: {
      session_id: "s1",
      turn_id: "t1",
      started_at: NOW,
      steps: [
        { name: "gmail.search", status: "ok", at: NOW },
        { name: "read_file", status: "running", at: NOW },
      ],
    },
    activity: [
      { kind: "answered", title: "Answered: food spending", detail: "queried postgres", at: NOW, session_id: "s1" },
      { kind: "learned", title: "Learned: Indomaret", detail: "saved to knowledge", at: NOW, session_id: "s1" },
      { kind: "failed", title: "Failed a Gmail call", detail: "429", at: NOW, session_id: "s1" },
    ],
    week: {
      days: Array.from({ length: 7 }).map((_, index) => ({
        day: "2026-10-" + (26 + index),
        turns: index === 6 ? 9 : index,
      })),
      turns: 64,
      questions: 5,
      notes: 12,
      tokens: 412000,
    },
    memory: { entries: ["Gaji masuk tiap tanggal 25"], total: 312 },
    job: "",
    looks_after: { channels: [], teams: [] },
    ...overrides,
  };
}

beforeEach(() => {
  vi.mocked(openRoomsStream).mockImplementation((handlers) => {
    handlers.onRooms([]);
    handlers.onState?.(true);
    return { close: () => {} };
  });
  vi.mocked(getAgentOverview).mockResolvedValue(overview());
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("Agent overview steps (AC-ROOM-017)", () => {
  it("renders the finished and running steps of now", async () => {
    const { container } = render(<AgentOverview name="dana" agent={agent()} />);

    expect(await screen.findByText("gmail.search")).toBeTruthy();
    expect(screen.getByText("read_file")).toBeTruthy();
    expect(container.querySelector(".ov-step.status-ok")).toBeTruthy();
    expect(container.querySelector(".ov-step.status-running")).toBeTruthy();
    expect(screen.getByText("Right now")).toBeTruthy();
  });
});

describe("Agent overview week (AC-ROOM-018)", () => {
  it("shows seven bars and the four stats", async () => {
    const { container } = render(<AgentOverview name="dana" agent={agent()} />);

    await screen.findByText("This week");
    expect(container.querySelectorAll(".ov-bar")).toHaveLength(7);
    expect(screen.getByText("tasks done")).toBeTruthy();
    expect(screen.getByText("times asked you")).toBeTruthy();
    expect(screen.getByText("new notes learned")).toBeTruthy();
    expect(screen.getByText("tokens")).toBeTruthy();
    expect(screen.getByText("412k")).toBeTruthy();
  });
});

describe("Agent overview activity filters (REQ-ROOM-010)", () => {
  it("narrows the activity to the picked kind", async () => {
    render(<AgentOverview name="dana" agent={agent()} />);

    expect(await screen.findByText("Answered: food spending")).toBeTruthy();

    fireEvent.click(screen.getByRole("tab", { name: "Learning" }));

    expect(screen.queryByText("Answered: food spending")).toBeNull();
    expect(screen.getByText("Learned: Indomaret")).toBeTruthy();
  });

  it("shows the job empty state and the memory chips", async () => {
    render(<AgentOverview name="dana" agent={agent()} />);

    expect(
      await screen.findByText(/No job yet — describe what dana is responsible for/)
    ).toBeTruthy();
    expect(screen.getByText("Gaji masuk tiap tanggal 25")).toBeTruthy();
    expect(screen.getByText("+ 311 more")).toBeTruthy();
  });
});

describe("Agent overview team variant (REQ-ROOM-010)", () => {
  it("shows the members instead of the memory", async () => {
    vi.mocked(getAgentOverview).mockResolvedValue(
      overview({
        name: "career-plan",
        kind: "team",
        title: "Career plan",
        presence: {
          state: "resting",
          label: "Resting",
          since: 0,
          session_id: "",
        },
        now: null,
        activity: [],
        memory: { entries: [], total: 0 },
        job: "",
      })
    );
    vi.mocked(openRoomsStream).mockImplementation((handlers) => {
      handlers.onRooms([
        room({ name: "karin", presence: { state: "online", label: "Active 1 min ago", since: 0, session_id: "" } }),
        room({ name: "dana", presence: { state: "working", label: "Running task", since: 0, session_id: "" } }),
      ]);
      handlers.onState?.(true);
      return { close: () => {} };
    });

    const team = agent({
      name: "career-plan",
      kind: "team",
      lead: "lana",
      members: ["karin", "dana"],
      title: "Career plan",
    });

    render(<AgentOverview name="career-plan" agent={team} />);

    expect(await screen.findByText("Members")).toBeTruthy();
    expect(screen.getByText("karin")).toBeTruthy();
    expect(screen.getByText("dana")).toBeTruthy();
    expect(screen.getByText("lana")).toBeTruthy();
    expect(screen.queryByText(/remembers about you/)).toBeNull();
  });
});
