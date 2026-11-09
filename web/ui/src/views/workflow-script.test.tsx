import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";

/* Spec #44 web acceptance (REQ-SP-021): the Script tab tests and generates on
   a sample, shows stats and history with Revert, and the convert dialog
   replaces an agent step only when the guardian says it is feasible. */
vi.mock("../api/workflows", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/workflows")>();
  return {
    ...actual,
    getWorkflow: vi.fn(),
    testScript: vi.fn(),
    generateScript: vi.fn(),
    convertNode: vi.fn(),
    scriptSuggestions: vi.fn(),
    answerDecision: vi.fn(),
    openWorkflowStream: vi.fn(() => ({ close: () => undefined })),
  };
});

import {
  answerDecision,
  convertNode,
  generateScript,
  getWorkflow,
  testScript,
} from "../api/workflows";
import type { Decision, WfNode, Workflow } from "../api/workflows";
import { ToastProvider } from "../ui/gr8r";
import { ConvertBanner, ConvertDialog, ScriptTab } from "./workflow-script";

function wrap(node: ReactNode) {
  return render(<ToastProvider>{node}</ToastProvider>);
}

afterEach(() => {
  vi.clearAllMocks();
});

const node: WfNode = {
  id: "parse",
  type: "script_parse",
  name: "Parse trending",
  position: { x: 0, y: 0 },
  params: {
    input: "{{ nodes.fetch.output.markdown }}",
    goal: "list repos",
    output_schema: { repos: "object[]" },
    script: "function parse(input) { return { repos: [] }; }",
    script_rev: 3,
  },
};

const update: Decision = {
  id: "d1",
  workflow_id: "wf",
  run_id: "r1",
  run_number: 7,
  node_id: "parse",
  item_index: -1,
  kind: "script",
  action: "script_update",
  reason: "field repos: empty",
  fix: { drift: "empty_drift", rev_from: 2, rev_to: 3 },
  persistent: true,
  confidence: 0,
  tokens: 2100,
  status: "applied",
  choice: "",
  created_at: Date.now() - 60_000,
  expires_at: 0,
  resolved_at: 0,
};

function workflowWithStats(): Workflow {
  return {
    script_stats: {
      parse: { script_runs: 12, fallback_runs: 1, tokens_saved: 25200, last_update: Date.now(), events: [update] },
    },
  } as unknown as Workflow;
}

describe("ScriptTab", () => {
  it("shows stats, tests the script and reverts an update", async () => {
    vi.mocked(getWorkflow).mockResolvedValue(workflowWithStats());
    vi.mocked(testScript).mockResolvedValue({
      ok: false,
      output: { repos: [] },
      problems: [{ code: "empty_drift", message: 'field "repos": empty' }],
      logs: ["seen 0 rows"],
      duration_ms: 4,
      sample: { source: "run", run: 7 },
    });
    vi.mocked(answerDecision).mockResolvedValue({} as never);
    wrap(<ScriptTab workflowId="wf" node={node} onChange={() => undefined} />);

    expect(await screen.findByText("25k")).toBeTruthy();
    expect(screen.getByText("Script updated to rev 3 · empty_drift")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Test" }));
    expect(await screen.findByText("Fails the contract")).toBeTruthy();
    expect(screen.getByText("empty_drift")).toBeTruthy();
    expect(vi.mocked(testScript)).toHaveBeenCalledWith("wf", node, {});

    fireEvent.click(screen.getByRole("button", { name: "Revert" }));
    await waitFor(() => expect(vi.mocked(answerDecision)).toHaveBeenCalledWith("wf", "d1", "revert"));
  });

  it("uses a generated script and bumps the revision", async () => {
    vi.mocked(getWorkflow).mockResolvedValue(workflowWithStats());
    vi.mocked(generateScript).mockResolvedValue({
      feasible: true,
      reason: "",
      script: "function parse(input) { return { repos: [{ name: 'a' }] }; }",
      output: { repos: [{ name: "a" }] },
      problems: [],
      agreement: 0.92,
      tokens: 1800,
    });
    const onChange = vi.fn();
    wrap(<ScriptTab workflowId="wf" node={node} onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    expect(await screen.findByText("Script ready")).toBeTruthy();
    expect(screen.getByText(/92% agreement/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Use this script" }));
    const next = onChange.mock.calls[0]![0] as WfNode;
    expect(next.params.script).toContain("name: 'a'");
    expect(next.params.script_rev).toBe(4);
  });
});

describe("Convert", () => {
  const agentNode: WfNode = {
    id: "summarize",
    type: "agent",
    name: "Summarize",
    position: { x: 10, y: 20 },
    params: { prompt: "List {{ nodes.fetch.output.markdown }}" },
  };

  it("replaces the node with the proposal, keeping id and position", async () => {
    vi.mocked(convertNode).mockResolvedValue({
      feasible: true,
      reason: "",
      node: { ...node, id: "summarize", position: { x: 0, y: 0 } },
      checks: [{ run: 5, ok: true, agreement: 1, problems: [] }],
      tokens: 2500,
    });
    const onReplace = vi.fn();
    wrap(<ConvertDialog workflowId="wf" node={agentNode} onReplace={onReplace} onClose={() => undefined} />);
    expect(await screen.findByText("Run #5")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Replace node" }));
    const next = onReplace.mock.calls[0]![0] as WfNode;
    expect(next.id).toBe("summarize");
    expect(next.type).toBe("script_parse");
    expect(next.position).toEqual({ x: 10, y: 20 });
  });

  it("keeps the agent step when the guardian says it needs judgment", async () => {
    vi.mocked(convertNode).mockResolvedValue({
      feasible: false,
      reason: "Summaries need judgment.",
      checks: [],
      tokens: 900,
    });
    wrap(<ConvertDialog workflowId="wf" node={agentNode} onReplace={() => undefined} onClose={() => undefined} />);
    expect(await screen.findByText("Summaries need judgment.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Replace node" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("banner offers convert and dismiss", () => {
    const onConvert = vi.fn();
    const onDismiss = vi.fn();
    wrap(
      <ConvertBanner
        suggestion={{ node_id: "summarize", name: "Summarize", type: "agent", runs: 7, avg_tokens: 3200, est_tokens_30d: 96000 }}
        onConvert={onConvert}
        onDismiss={onDismiss}
      />
    );
    fireEvent.click(screen.getByRole("button", { name: "Convert" }));
    fireEvent.click(screen.getByRole("button", { name: "Dismiss suggestion" }));
    expect(onConvert).toHaveBeenCalled();
    expect(onDismiss).toHaveBeenCalled();
  });
});
