import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";

vi.mock("../../api/agents", () => ({
  loadAgentTab: vi.fn(),
  updateAgent: vi.fn(),
  isErrorCode: vi.fn(),
}));

vi.mock("../../api/tools", () => ({
  getToolCatalog: vi.fn(),
}));

import { loadAgentTab, updateAgent } from "../../api/agents";
import { getToolCatalog } from "../../api/tools";
import type { ToolGroup } from "../../api/tools";
import { ToolsTab } from "./tools";

const CATALOG: ToolGroup[] = [
  {
    id: "files",
    label: "Files",
    tools: [
      {
        name: "read_file",
        risk: "read",
        asks_first: false,
        enabled: true,
        source: "listed",
      },
    ],
  },
  {
    id: "chat",
    label: "Chat",
    tools: [
      {
        name: "ask_user",
        risk: "read",
        asks_first: false,
        enabled: false,
        source: "not_listed",
      },
    ],
  },
];

function mockLoad(groups: ToolGroup[]) {
  vi.mocked(loadAgentTab).mockResolvedValue({
    version: "v1",
    agent: { tools: ["read_file"] },
    row: { disabled_tools: [] },
  });
  vi.mocked(getToolCatalog).mockResolvedValue({ agent: "lana", groups });
}

describe("ToolsTab", () => {
  it("shows Always on for an auto-offered tool and no hint for not-listed", async () => {
    mockLoad([
      ...CATALOG,
      {
        id: "mcp-admin",
        label: "MCP",
        tools: [
          {
            name: "mcp_manage",
            risk: "exec",
            asks_first: false,
            enabled: true,
            source: "auto",
          },
        ],
      },
    ]);
    render(<ToolsTab name="lana" />);

    const manageRow = (await screen.findByText("mcp_manage")).closest("tr");
    expect(manageRow).toBeTruthy();
    expect(within(manageRow as HTMLElement).getByText("Always on")).toBeTruthy();

    const askRow = screen.getByText("ask_user").closest("tr");
    expect(askRow).toBeTruthy();
    expect(within(askRow as HTMLElement).queryByText("Always on")).toBeNull();
  });

  it("saves tools, disabled_tools, and mcp together", async () => {
    mockLoad(CATALOG);
    vi.mocked(updateAgent).mockResolvedValue({
      name: "lana",
      version: "v2",
      restart_required: false,
    });
    render(<ToolsTab name="lana" />);

    fireEvent.click(await screen.findByRole("switch", { name: "ask_user" }));

    await waitFor(() => {
      expect(updateAgent).toHaveBeenCalledWith(
        "lana",
        {
          tools: ["read_file", "ask_user"],
          disabled_tools: [],
          mcp: [],
        },
        "v1"
      );
    });
  });
});
