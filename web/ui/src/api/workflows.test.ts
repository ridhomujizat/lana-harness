import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  askAssistant,
  getChat,
  getRun,
  getWorkflow,
  listWorkflows,
  lockPreview,
  normalizeGraph,
} from "./workflows";

/* Older servers sent nil Go slices as null (a new workflow had
   "edges": null). The client turns every list a view iterates into an
   array on read. */

function response(body: unknown): Response {
  return { ok: true, status: 200, text: async () => JSON.stringify(body) } as Response;
}

const nullGraph = {
  nodes: [{ id: "run-button", type: "trigger.manual", name: "Run", position: { x: 0, y: 0 }, params: null }],
  edges: null,
};

const wireWorkflow = {
  id: "hi",
  name: "hi",
  description: "",
  guardian: "default",
  status: "draft",
  version: 1,
  version_author: "owner",
  version_summary: "created",
  version_at: 0,
  graph: nullGraph,
  lock: { version: 1, locked_at: 0, tools: null },
  lock_current: false,
  failure_default: { mode: "guardian" },
  webhook: { path: "/hooks/hi", token: "t" },
  consecutive_failures: 0,
  created_at: 0,
  updated_at: 0,
};

describe("api/workflows normalization", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it("normalizeGraph turns null or missing lists into arrays", () => {
    expect(normalizeGraph(null)).toEqual({ nodes: [], edges: [] });
    expect(normalizeGraph({ nodes: null, edges: null })).toEqual({ nodes: [], edges: [] });
    const graph = normalizeGraph(nullGraph as never);
    expect(graph.edges).toEqual([]);
    expect(graph.nodes).toHaveLength(1);
    expect(graph.nodes[0]!.params).toEqual({});
  });

  it("getWorkflow normalizes a graph with edges: null", async () => {
    fetchMock.mockResolvedValueOnce(response(wireWorkflow));
    const workflow = await getWorkflow("hi");
    expect(fetchMock.mock.calls[0]![0]).toBe("/v1/workflows/hi");
    expect(workflow.graph.edges).toEqual([]);
    expect(workflow.graph.nodes.map((node) => node.id)).toEqual(["run-button"]);
    expect(workflow.lock!.tools).toEqual([]);
  });

  it("getRun normalizes the graph, node rows and decisions", async () => {
    fetchMock.mockResolvedValueOnce(
      response({
        run: { id: "r1", number: 1 },
        graph: { nodes: null, edges: null },
        nodes: [{ node_id: "a", status: "succeeded", attempts: null, logs: null }],
        decisions: null,
      })
    );
    const detail = await getRun("hi", 1);
    expect(detail.graph).toEqual({ nodes: [], edges: [] });
    expect(detail.nodes[0]!.attempts).toEqual([]);
    expect(detail.nodes[0]!.logs).toEqual([]);
    expect(detail.decisions).toEqual([]);
  });

  it("list, lock and chat responses never carry null lists", async () => {
    fetchMock
      .mockResolvedValueOnce(response({ workflows: [{ id: "hi", triggers: null, bots: null }], stats: {}, attention: null }))
      .mockResolvedValueOnce(response({ tools: [{ tool: "bash", used_by: null, risk: "admin" }] }))
      .mockResolvedValueOnce(
        response({ messages: [{ id: 1, role: "guardian", text: "ok", change: { added: null, removed: null, changed: null } }] })
      )
      .mockResolvedValueOnce(
        response({ message: { id: 2, role: "guardian", text: "ok" }, workflow: wireWorkflow, tokens: 1 })
      );
    const list = await listWorkflows();
    expect(list.workflows[0]!.triggers).toEqual([]);
    expect(list.workflows[0]!.bots).toEqual([]);
    expect(list.attention).toEqual([]);
    const lock = await lockPreview("hi");
    expect(lock.tools[0]!.used_by).toEqual([]);
    const chat = await getChat("hi");
    expect(chat.messages[0]!.change).toMatchObject({ added: [], removed: [], changed: [] });
    const assist = await askAssistant("hi", "add a step", 1);
    expect(assist.workflow.graph.edges).toEqual([]);
  });
});
