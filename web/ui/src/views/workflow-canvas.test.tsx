import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { Graph, WfEdge } from "../api/workflows";
import { WorkflowCanvas, edgeKey } from "./workflow-canvas";

/* The edge controls live on the edge itself (hover to reveal), not in the old
   floating `.wf-edgebar` corner toolbar. */

function twoNodes(): Graph {
  return {
    nodes: [
      { id: "a", type: "bot", name: "Start", position: { x: 0, y: 0 }, params: {} },
      { id: "b", type: "bot", name: "Left", position: { x: 0, y: 200 }, params: {} },
    ],
    edges: [{ id: "e1", from: "a", to: "b" }],
  };
}

function actsFor(container: HTMLElement, index = 0): HTMLElement {
  const acts = container.querySelectorAll(".wf-edgeacts");
  return acts[index] as HTMLElement;
}

describe("workflow canvas edges", () => {
  it("has no corner Delete edge toolbar", () => {
    const { container } = render(
      <WorkflowCanvas graph={twoNodes()} onChange={() => undefined} />
    );
    expect(screen.queryByText("Delete edge")).toBeNull();
    expect(container.querySelector(".wf-edgebar")).toBeNull();
  });

  it("shows an edge's delete button while the pointer is over the edge", () => {
    const { container } = render(
      <WorkflowCanvas graph={twoNodes()} onChange={() => undefined} />
    );
    const hit = container.querySelector(".wf-hit") as SVGPathElement;
    expect(actsFor(container).classList.contains("on")).toBe(false);
    fireEvent.pointerOver(hit);
    expect(actsFor(container).classList.contains("on")).toBe(true);
    expect(
      screen.getByRole("button", { name: "Delete edge from Start to Left" })
    ).toBeTruthy();
  });

  it("deletes only the edge whose button was clicked", () => {
    const onChange = vi.fn();
    const graph: Graph = {
      nodes: [
        { id: "a", type: "bot", name: "Start", position: { x: 0, y: 0 }, params: {} },
        { id: "b", type: "bot", name: "Left", position: { x: 0, y: 200 }, params: {} },
        { id: "c", type: "bot", name: "Right", position: { x: 320, y: 200 }, params: {} },
      ],
      edges: [
        { id: "e1", from: "a", to: "b" },
        { id: "e2", from: "a", to: "c" },
      ],
    };
    const { container } = render(<WorkflowCanvas graph={graph} onChange={onChange} />);
    fireEvent.pointerOver(container.querySelector(".wf-hit") as SVGPathElement);
    fireEvent.click(screen.getByRole("button", { name: "Delete edge from Start to Left" }));
    expect(onChange).toHaveBeenCalledTimes(1);
    const next = onChange.mock.calls[0]![0] as Graph;
    expect(next.edges.map((edge) => edge.id)).toEqual(["e2"]);
  });

  it("shows the port pills for a selected multi-port edge", () => {
    const onChange = vi.fn();
    const graph: Graph = {
      nodes: [
        { id: "s", type: "switch", name: "Route", position: { x: 0, y: 0 }, params: { cases: ["high", "low"] } },
        { id: "t", type: "bot", name: "Bot", position: { x: 0, y: 200 }, params: {} },
      ],
      edges: [{ id: "e1", from: "s", to: "t", from_port: "high" }],
    };
    const { container } = render(<WorkflowCanvas graph={graph} onChange={onChange} />);
    expect(screen.queryByRole("button", { name: "low" })).toBeNull();
    fireEvent.pointerDown(container.querySelector(".wf-hit") as SVGPathElement);
    expect(screen.getByRole("button", { name: "high" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "low" }));
    const next = onChange.mock.calls[0]![0] as Graph;
    expect(next.edges[0]!.from_port).toBe("low");
  });

  it("delete/backspace removes the selected edge", () => {
    const onChange = vi.fn();
    const { container } = render(
      <WorkflowCanvas graph={twoNodes()} onChange={onChange} />
    );
    fireEvent.pointerDown(container.querySelector(".wf-hit") as SVGPathElement);
    fireEvent.keyDown(window, { key: "Delete" });
    expect(onChange).toHaveBeenCalledTimes(1);
    expect((onChange.mock.calls[0]![0] as Graph).edges).toEqual([]);
  });

  it("shows no delete buttons on a read-only canvas", () => {
    const { container } = render(<WorkflowCanvas graph={twoNodes()} />);
    expect(container.querySelector(".wf-edgeacts")).toBeNull();
    expect(screen.queryByRole("button", { name: /Delete edge/ })).toBeNull();
  });
});

/* The workflow assistant used to write edges with an empty id; every edge then
   shared one key. The canvas must key edges apart so hover, deletion and port
   changes touch exactly one edge. */
function blankIdGraph(): Graph {
  return {
    nodes: [
      { id: "a", type: "bot", name: "Start", position: { x: 0, y: 0 }, params: {} },
      { id: "b", type: "bot", name: "Left", position: { x: 0, y: 200 }, params: {} },
      { id: "c", type: "bot", name: "Right", position: { x: 320, y: 200 }, params: {} },
    ],
    edges: [
      { id: "", from: "a", to: "b" },
      { id: "", from: "a", to: "c" },
    ],
  };
}

describe("edgeKey", () => {
  const edge = (id: string, from: string, to: string): WfEdge => ({ id, from, to });

  it("uses a non-empty unique id", () => {
    expect(edgeKey(edge("e1", "a", "b"), 0, new Set(["e1"]))).toBe("e1");
  });

  it("derives a key for an empty id", () => {
    expect(edgeKey(edge("", "a", "b"), 0, new Set())).not.toBe("");
  });

  it("does not trust an id repeated in the graph and keys such edges apart", () => {
    const first = edgeKey(edge("x", "a", "b"), 0, new Set());
    const second = edgeKey(edge("x", "a", "c"), 1, new Set());
    expect(first).not.toBe("x");
    expect(first).not.toBe(second);
  });
});

describe("workflow canvas edges with empty ids", () => {
  it("marks no edge active until one is hovered", () => {
    const { container } = render(
      <WorkflowCanvas graph={blankIdGraph()} onChange={() => undefined} />
    );
    expect(container.querySelectorAll(".wf-edgeacts").length).toBe(2);
    expect(container.querySelectorAll(".wf-edgeacts.on").length).toBe(0);
  });

  it("shows only the hovered edge's button", () => {
    const { container } = render(
      <WorkflowCanvas graph={blankIdGraph()} onChange={() => undefined} />
    );
    fireEvent.pointerOver(container.querySelectorAll(".wf-hit")[0]!);
    const active = container.querySelectorAll(".wf-edgeacts.on");
    expect(active.length).toBe(1);
    expect(active[0]).toBe(actsFor(container, 0));
  });

  it("deletes only the hovered edge", () => {
    const onChange = vi.fn();
    const { container } = render(
      <WorkflowCanvas graph={blankIdGraph()} onChange={onChange} />
    );
    fireEvent.pointerOver(container.querySelectorAll(".wf-hit")[0]!);
    fireEvent.click(container.querySelector(".wf-edgeacts.on .wf-edgeacts-del")!);
    expect(onChange).toHaveBeenCalledTimes(1);
    const next = onChange.mock.calls[0]![0] as Graph;
    expect(next.edges.map((edge) => edge.to)).toEqual(["c"]);
  });

  it("changes only the selected edge's port", () => {
    const onChange = vi.fn();
    const graph: Graph = {
      nodes: [
        { id: "s", type: "switch", name: "Route", position: { x: 0, y: 0 }, params: { cases: ["high", "low"] } },
        { id: "t1", type: "bot", name: "One", position: { x: 0, y: 200 }, params: {} },
        { id: "t2", type: "bot", name: "Two", position: { x: 320, y: 200 }, params: {} },
      ],
      edges: [
        { id: "", from: "s", from_port: "high", to: "t1" },
        { id: "", from: "s", from_port: "low", to: "t2" },
      ],
    };
    const { container } = render(<WorkflowCanvas graph={graph} onChange={onChange} />);
    fireEvent.pointerDown(container.querySelectorAll(".wf-hit")[0]!);
    // Only the selected edge reveals its ports.
    fireEvent.click(screen.getByRole("button", { name: "default" }));
    expect(onChange).toHaveBeenCalledTimes(1);
    const next = onChange.mock.calls[0]![0] as Graph;
    expect(next.edges.length).toBe(2);
    expect(next.edges[0]!.from_port).toBe("default");
    expect(next.edges[1]!.from_port).toBe("low");
  });
});
