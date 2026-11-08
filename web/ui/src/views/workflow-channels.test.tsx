import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

/* Spec #33: the Message node's channel checklist. The channel list comes from
   GET /v1/workflows/channels; these tests mock it and drive the inspector. */
vi.mock("../api/workflows", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/workflows")>();
  return {
    ...actual,
    getWorkflowChannels: vi.fn(),
    listRuns: vi.fn(),
    getRun: vi.fn(),
  };
});

import { getWorkflowChannels } from "../api/workflows";
import type { Graph, MessageChannel, WfNode } from "../api/workflows";
import { NodeInspector } from "./workflow-inspector";

function messageNode(params: Record<string, unknown>): WfNode {
  return {
    id: "msg",
    type: "message",
    name: "Send",
    position: { x: 0, y: 0 },
    params,
  };
}

function renderInspector(node: WfNode) {
  const onChange = vi.fn();
  const graph: Graph = { nodes: [node], edges: [] };
  render(
    <NodeInspector
      workflowId="wf"
      graph={graph}
      node={node}
      agents={["lana", "scout"]}
      onChange={onChange}
      onDelete={() => undefined}
      onClose={() => undefined}
    />
  );
  return { onChange };
}

/* web needs an agent; discord is registered but cannot deliver right now. */
const CHANNELS: MessageChannel[] = [
  { id: "web", label: "Web chat", needs_agent: true, available: true },
  { id: "telegram", label: "Telegram", needs_agent: false, available: true },
  {
    id: "discord",
    label: "Discord",
    needs_agent: false,
    available: false,
    reason: "Discord is not connected",
  },
];

afterEach(() => {
  vi.clearAllMocks();
});

describe("message node channels", () => {
  it("renders the channels the endpoint returns", async () => {
    vi.mocked(getWorkflowChannels).mockResolvedValue(CHANNELS);
    renderInspector(messageNode({ text: "hi" }));
    expect(await screen.findByText("Web chat")).toBeTruthy();
    expect(screen.getByText("Telegram")).toBeTruthy();
    expect(screen.getByText("Discord")).toBeTruthy();
  });

  it("defaults an old node without channels to telegram", async () => {
    vi.mocked(getWorkflowChannels).mockResolvedValue(CHANNELS);
    renderInspector(messageNode({ text: "hi" }));
    const telegram = (await screen.findByLabelText("Telegram")) as HTMLInputElement;
    const web = screen.getByLabelText("Web chat") as HTMLInputElement;
    expect(telegram.checked).toBe(true);
    expect(web.checked).toBe(false);
  });

  it("writes an explicit channels list on the first change", async () => {
    vi.mocked(getWorkflowChannels).mockResolvedValue(CHANNELS);
    const { onChange } = renderInspector(messageNode({ text: "hi" }));
    fireEvent.click(await screen.findByLabelText("Web chat"));
    expect(onChange).toHaveBeenCalledTimes(1);
    const next = onChange.mock.calls[0]![0] as WfNode;
    /* Ordered like the endpoint: web before telegram. */
    expect(next.params.channels).toEqual(["web", "telegram"]);
  });

  it("shows the agent picker and a hint when a needs-agent channel is picked", async () => {
    vi.mocked(getWorkflowChannels).mockResolvedValue(CHANNELS);
    renderInspector(messageNode({ text: "hi", channels: ["web"] }));
    expect(await screen.findByText("Agent")).toBeTruthy();
    expect(screen.getByText("Pick a bot")).toBeTruthy();
    expect(screen.getByText("Pick an agent for Web chat.")).toBeTruthy();
  });

  it("hides the agent picker when no selected channel needs one", async () => {
    vi.mocked(getWorkflowChannels).mockResolvedValue(CHANNELS);
    renderInspector(messageNode({ text: "hi" }));
    await screen.findByText("Telegram");
    expect(screen.queryByText("Agent")).toBeNull();
  });

  it("disables unchecking the last selected channel", async () => {
    vi.mocked(getWorkflowChannels).mockResolvedValue(CHANNELS);
    renderInspector(messageNode({ text: "hi" }));
    const telegram = (await screen.findByLabelText("Telegram")) as HTMLInputElement;
    expect(telegram.disabled).toBe(true);
  });

  it("disables an unavailable channel and shows the reason", async () => {
    vi.mocked(getWorkflowChannels).mockResolvedValue(CHANNELS);
    renderInspector(messageNode({ text: "hi" }));
    const discord = (await screen.findByLabelText("Discord")) as HTMLInputElement;
    expect(discord.disabled).toBe(true);
    expect(screen.getByText("Discord is not connected")).toBeTruthy();
  });

  it("shows an inline error and keeps the value when the endpoint fails", async () => {
    vi.mocked(getWorkflowChannels).mockRejectedValue(new Error("boom"));
    const { onChange } = renderInspector(
      messageNode({ text: "hi", channels: ["web"], agent: "lana" })
    );
    expect(
      await screen.findByText("Could not reach the daemon. Check that it is still running.")
    ).toBeTruthy();
    expect(onChange).not.toHaveBeenCalled();
  });
});
