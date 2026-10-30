import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("../api/mcp", () => ({
  listMcpServers: vi.fn(),
  saveMcpServer: vi.fn(),
  deleteMcpServer: vi.fn(),
  testMcpServer: vi.fn(),
  restartMcpServer: vi.fn(),
  listAgents: vi.fn(),
  saveAgentMcp: vi.fn(),
}));

import { listMcpServers, listAgents } from "../api/mcp";
import type { Agent } from "../api/agents";
import { McpPage, McpDetailPage } from "./mcp";

const AGENT: Agent = {
  name: "scout",
  kind: "agent",
  model: "openai:gpt-4o",
  policy: "ask",
  skills: [],
  mcp: ["docs"],
  mcp_status: [],
  description: "Scout",
  default: false,
  provider: "openai",
  permission: "ask",
  workspace: "",
  disabled_tools: [],
  tools_count: 0,
  sessions_count: 0,
  last_active_at: null,
};

describe("mcp list", () => {
  it("shows the empty state when no servers are configured", async () => {
    vi.mocked(listMcpServers).mockResolvedValue({ version: "v1", servers: [] });
    render(<McpPage />);
    expect(await screen.findByText("No MCP servers yet.")).toBeTruthy();
    expect(
      screen.getByText("Add a server once, then pick which agents use it.")
    ).toBeTruthy();
  });

  it("shows a server row with its status and agents", async () => {
    vi.mocked(listMcpServers).mockResolvedValue({
      version: "v1",
      servers: [
        {
          name: "docs",
          type: "command",
          command: "npx",
          args: ["-y", "docs-server"],
          url: "",
          env_keys: [],
          header_keys: [],
          tools_count: 2,
          agents: ["scout"],
          state: "running",
          status: "connected",
          error: "",
          running_for_ms: 65000,
        },
      ],
    });
    render(<McpPage />);
    expect(await screen.findByText("docs")).toBeTruthy();
    expect(screen.getByText("npx -y docs-server")).toBeTruthy();
    expect(screen.getAllByText("Connected").length).toBeGreaterThan(0);
    expect(screen.getByText("1 server · 0 unreachable")).toBeTruthy();
    expect(screen.getByText("Used by scout")).toBeTruthy();
  });
});

describe("mcp detail", () => {
  it("loads the connection form, tools, and the agent list", async () => {
    vi.mocked(listMcpServers).mockResolvedValue({
      version: "v2",
      servers: [
        {
          name: "docs",
          type: "command",
          command: "npx",
          args: ["-y", "docs-server"],
          url: "",
          env_keys: ["TOKEN"],
          header_keys: [],
          tools_count: 2,
          agents: ["scout"],
          state: "running",
          status: "connected",
          error: "",
          running_for_ms: 65000,
          tools: [
            { name: "search", description: "Search docs" },
            "fetch",
          ],
        },
      ],
    });
    vi.mocked(listAgents).mockResolvedValue({ agents: [AGENT] });

    render(<McpDetailPage name="docs" />);
    expect(
      await screen.findByRole("heading", { name: "docs" })
    ).toBeTruthy();
    expect(screen.getByDisplayValue("npx")).toBeTruthy();
    expect(screen.getByDisplayValue(/docs-server/)).toBeTruthy();
    expect(screen.getByDisplayValue("TOKEN")).toBeTruthy();
    expect(screen.getByText("Search docs")).toBeTruthy();
    expect(screen.getByText("fetch")).toBeTruthy();
    expect(screen.getByText("scout")).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Save changes" })
    ).toBeTruthy();
  });
});
