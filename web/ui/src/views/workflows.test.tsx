import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";

/* Spec #33 web acceptance: AC-WF-019 (approve from Needs attention),
   AC-WF-020 (drop a palette node, connect it, save) and AC-WF-021 (a stream
   change refreshes the run view). The workflow API and the stream are mocked. */
vi.mock("../api/workflows", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/workflows")>();
  return {
    ...actual,
    listWorkflows: vi.fn(),
    getChat: vi.fn(),
    lockPreview: vi.fn(),
    updateWorkflow: vi.fn(),
    listRuns: vi.fn(),
    getRun: vi.fn(),
    answerDecision: vi.fn(),
    openWorkflowStream: vi.fn(),
  };
});
vi.mock("../api/approvals", () => ({ answerApproval: vi.fn() }));
vi.mock("../api/agents", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/agents")>();
  return { ...actual, listAgents: vi.fn(() => Promise.resolve({ agents: [] })) };
});

import { answerApproval } from "../api/approvals";
import {
  getChat,
  getRun,
  listRuns,
  listWorkflows,
  lockPreview,
  openWorkflowStream,
  updateWorkflow,
} from "../api/workflows";
import type { Graph, RunDetail, Workflow, WorkflowChange, WorkflowList } from "../api/workflows";
import { ToastProvider } from "../ui/gr8r";
import { WorkflowsPage } from "./workflows";
import { WorkflowEditor } from "./workflow-editor";
import { WorkflowRuns } from "./workflow-runs";

function wrap(node: ReactNode) {
  return render(<ToastProvider>{node}</ToastProvider>);
}

let streamHandler: ((change: WorkflowChange) => void) | null = null;
vi.mocked(openWorkflowStream).mockImplementation((handler) => {
  streamHandler = handler;
  return { close: () => undefined };
});

afterEach(() => {
  vi.clearAllMocks();
  streamHandler = null;
});

const graph: Graph = {
  nodes: [
    { id: "go", type: "trigger.manual", name: "Run button", position: { x: 0, y: 0 }, params: { enabled: true } },
  ],
  edges: [],
};

function workflow(overrides: Partial<Workflow> = {}): Workflow {
  return {
    id: "wf",
    name: "Test flow",
    description: "",
    guardian: "default",
    status: "draft",
    version: 1,
    version_author: "owner",
    version_summary: "",
    version_at: 0,
    graph,
    lock: null,
    lock_current: false,
    failure_default: {},
    webhook: { path: "/hooks/wf", token: "t" },
    consecutive_failures: 0,
    created_at: 0,
    updated_at: 0,
    ...overrides,
  };
}

describe("workflows list", () => {
  it("AC-WF-019: approves a waiting approval from Needs attention", async () => {
    const list: WorkflowList = {
      workflows: [],
      stats: {
        total: 0, active: 0, paused: 0, error: 0, waiting_on_you: 1, approvals: 1, decisions: 0,
        success_7d: null, success_prev_7d: null, runs_today: 0, runs_today_ok: 0, runs_today_failed: 0, runs_today_running: 0,
      },
      attention: [
        {
          id: "ap1", workflow_id: "wf", workflow_name: "PR gate", run_id: "r1", run_number: 1, node_id: "", node_name: "",
          item_index: -1, kind: "tool_approval", action: "ask", reason: "default wants bash", persistent: false,
          confidence: 0, tokens: 0, status: "pending", choice: "", created_at: 0, expires_at: 0, resolved_at: 0,
        },
      ],
    };
    vi.mocked(listWorkflows)
      .mockResolvedValueOnce(list)
      .mockResolvedValue({ ...list, attention: [] });
    vi.mocked(answerApproval).mockResolvedValue({ outcome: "allowed_once" });
    wrap(<WorkflowsPage />);
    const button = await screen.findByRole("button", { name: "Approve once" });
    fireEvent.click(button);
    await waitFor(() => expect(answerApproval).toHaveBeenCalledWith("ap1", "allowed_once"));
    await waitFor(() => expect(screen.queryByText("Approve once")).toBeNull());
  });
});

describe("workflow editor", () => {
  it("AC-WF-020: a dropped palette node, connected and saved, is in the saved graph", async () => {
    vi.mocked(getChat).mockResolvedValue({ messages: [] });
    vi.mocked(lockPreview).mockResolvedValue({ tools: [] });
    vi.mocked(updateWorkflow).mockImplementation(async (_id, body) =>
      workflow({ version: 2, graph: body.graph || graph })
    );
    const { container } = wrap(<WorkflowEditor workflow={workflow()} onSaved={() => undefined} />);
    const canvas = screen.getByRole("region", { name: "Workflow editor canvas" });
    /* Drop a Bash node from the palette onto the canvas. */
    const data = new Map<string, string>([["application/x-lana-node", "bash"]]);
    const dataTransfer = {
      types: ["application/x-lana-node"],
      getData: (key: string) => data.get(key) || "",
      setData: () => undefined,
      dropEffect: "copy",
    };
    fireEvent.dragOver(canvas, { dataTransfer });
    fireEvent.drop(canvas, { dataTransfer, clientX: 300, clientY: 300 });
    await screen.findByText("Bash · command");
    /* Connect: drag from the trigger's out handle onto the new node. */
    const trigger = container.querySelector('[data-node="go"] .wf-port.out') as HTMLElement;
    const target = container.querySelector('[data-node="bash"]') as HTMLElement;
    expect(trigger).toBeTruthy();
    expect(target).toBeTruthy();
    const left = parseFloat(target.style.left);
    const top = parseFloat(target.style.top);
    /* jsdom has no layout: the canvas origin is 0,0; read pan and zoom from
       the layer transform. */
    const layer = container.querySelector(".wf-layer") as HTMLElement;
    const match = /translate\(([-\d.]+)px, ([-\d.]+)px\) scale\(([-\d.]+)\)/.exec(layer.style.transform);
    const [px, py, zoom] = match ? [Number(match[1]), Number(match[2]), Number(match[3])] : [0, 0, 1];
    const point = { clientX: px + (left + 100) * zoom, clientY: py + (top + 20) * zoom };
    fireEvent.pointerDown(trigger, { clientX: 0, clientY: 0, button: 0 });
    fireEvent.pointerMove(canvas, point);
    fireEvent.pointerUp(canvas, point);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(updateWorkflow).toHaveBeenCalled());
    const body = vi.mocked(updateWorkflow).mock.calls[0]![1];
    expect(body.base_version).toBe(1);
    expect(body.graph!.nodes.map((node) => node.id)).toEqual(["go", "bash"]);
    expect(body.graph!.edges).toEqual([{ id: "e1", from: "go", to: "bash" }]);
  });
});

describe("workflow runs", () => {
  it("AC-WF-021: a stream change refreshes the node statuses", async () => {
    const run = {
      id: "r1", workflow_id: "wf", number: 1, version: 1, trigger: "manual", input: {}, status: "running" as const,
      waiting_reason: "", wake_at: 0, error: "", tokens: 0, nodes_done: 0, nodes_total: 1, summary: "", started_at: 1, ended_at: 0,
    };
    const running: RunDetail = {
      run,
      graph,
      nodes: [
        { node_id: "go", item_index: -1, status: "running", attempts: [], input: {}, output: null, error: "", logs: [], tokens: 0, session_id: "", started_at: 1, ended_at: 0 },
      ],
      decisions: [],
    };
    const done: RunDetail = {
      ...running,
      run: { ...run, status: "succeeded", ended_at: 2 },
      nodes: [{ ...running.nodes[0]!, status: "succeeded", ended_at: 2 }],
    };
    vi.mocked(listRuns).mockResolvedValue({ runs: [run] });
    vi.mocked(getRun).mockResolvedValueOnce(running).mockResolvedValue(done);
    wrap(<WorkflowRuns workflow={workflow()} runRef="1" onWorkflow={() => undefined} />);
    await screen.findByText("Running", { selector: ".wf-badge" });
    expect(streamHandler).toBeTruthy();
    await act(async () => {
      streamHandler!({ workflow_id: "wf", run_id: "r1" });
    });
    await waitFor(() => expect(screen.queryByText("Running", { selector: ".wf-badge" })).toBeNull());
  });
});
