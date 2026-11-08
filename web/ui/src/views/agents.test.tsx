import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

/* REQ-ROOM-011: the roster pulse and the cards. The agent list and the live
   rooms store are mocked so the pulse counts and the context action can be
   observed (AC-ROOM-019). */
vi.mock("../api/agents", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/agents")>();
  return { ...actual, listAgents: vi.fn(), createAgent: vi.fn() };
});
vi.mock("../api/router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/router")>();
  return { ...actual, getRouter: vi.fn(), getClaudeAgent: vi.fn() };
});
vi.mock("../api/rooms", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/rooms")>();
  return { ...actual, listRooms: vi.fn(), openRoomsStream: vi.fn() };
});

import { createAgent, listAgents } from "../api/agents";
import type { Agent } from "../api/agents";
import { openRoomsStream } from "../api/rooms";
import { getRouter } from "../api/router";
import type { RoomRow } from "../api/rooms";
import { AgentsPage, CreateAgentDialog } from "./agents";

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

/* Spec 39 (REQ-CCT-009, AC-CCT-011): no engine choice; a Claude Code model
   is picked like any other model. */
describe("Claude Code models", () => {
  beforeEach(() => {
    HTMLDialogElement.prototype.showModal = function (this: HTMLDialogElement) {
      this.open = true;
    };
    HTMLDialogElement.prototype.close = function (this: HTMLDialogElement) {
      this.open = false;
    };
    vi.mocked(getRouter).mockResolvedValue({
      version: "v1",
      providers: [],
      combos: [],
      cooling: [],
      proxy: { enabled: false, base_url: "" },
      choices: [
        { value: "deepseek/deepseek-flash", label: "DeepSeek Flash", group: "deepseek" },
        { value: "claude-code/opus", label: "Claude Code · Opus (latest)", group: "Claude Code" },
      ],
    });
  });

  it("creates a lana agent with a Claude Code model and no runtime or provider", async () => {
    vi.mocked(createAgent).mockResolvedValue({ name: "claude", restart_required: false });
    const onCreated = vi.fn();
    render(<CreateAgentDialog onClose={() => {}} onCreated={onCreated} />);

    fireEvent.change(screen.getByRole("textbox", { name: /Name/ }), {
      target: { value: "claude" },
    });
    expect(screen.queryByRole("radio", { name: "Claude Agent" })).toBeNull();
    expect(screen.queryByText("Runtime")).toBeNull();
    expect(screen.getByRole("combobox", { name: "Template" })).toBeTruthy();
    expect(screen.queryByText(/Runs in Claude Code/)).toBeNull();
    fireEvent.click(screen.getByRole("combobox", { name: "Model" }));
    fireEvent.click(await screen.findByRole("option", { name: "Claude Code · Opus (latest)" }));
    expect(screen.getByText(/Runs in Claude Code with your Claude plan/)).toBeTruthy();
    expect(screen.getByRole("combobox", { name: "Reasoning effort" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Create agent" }));

    await waitFor(() => {
      expect(onCreated).toHaveBeenCalledWith("claude");
    });
    const body = vi.mocked(createAgent).mock.calls[0]?.[0];
    expect(body).toEqual({
      name: "claude",
      template: "assistant",
      workspace: "",
      model: "claude-code/opus",
    });
    expect(screen.queryByText(/Login with Claude/i)).toBeNull();
  });

  it("marks an agent that runs in Claude Code with the Claude logo", async () => {
    vi.mocked(listAgents).mockResolvedValue({
      agents: [agent({ name: "claude", provider: "claude-code", model: "claude-code/opus", claude_code: true })],
    });
    installRows([room({ name: "claude" })]);
    render(<AgentsPage />);
    expect(await screen.findByText("claude-code/opus")).toBeTruthy();
    const badge = document.querySelector(".rt-cc-badge") as HTMLElement;
    expect(badge.textContent).toBe("Claude Code");
    expect(badge.querySelector('[data-logo="claude"]')).toBeTruthy();
    expect(screen.queryByText(/Claude Agent/)).toBeNull();
  });
});
