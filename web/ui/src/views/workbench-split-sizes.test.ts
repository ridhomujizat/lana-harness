import { describe, expect, it } from "vitest";

import { emptyLayout, normalSizes, parseLayout, removeSession, resizePair, setSplitSizes, splitPane, splitSizes, type PaneNode, type WbLayout } from "./workbench-model";

/* Draggable pane sizes: the split's shares live in the shared layout. */

function layoutWith(root: PaneNode): WbLayout {
  return { ...emptyLayout(), spaces: [{ id: "sp", name: "s", color: "#000", dir: "", tabs: [{ id: "t", root }], activeTab: "t" }], activeSpace: "sp" };
}

function rootOf(l: WbLayout): PaneNode {
  const r = l.spaces[0]?.tabs[0]?.root;
  if (!r) throw new Error("no root");
  return r;
}

const split = (sizes?: number[]): PaneNode => ({
  type: "split",
  dir: "row",
  children: [
    { type: "pane", session: "a" },
    { type: "pane", session: "b" },
  ],
  ...(sizes ? { sizes } : {}),
});

describe("split sizes", () => {
  it("normalises shares and rejects bad ones", () => {
    expect(normalSizes([1, 3], 2)).toEqual([0.25, 0.75]);
    expect(normalSizes([1], 2)).toBeUndefined();
    expect(normalSizes([1, -1], 2)).toBeUndefined();
    expect(normalSizes("x", 2)).toBeUndefined();
  });

  it("defaults to equal shares", () => {
    const node = split();
    if (node.type !== "split") throw new Error();
    expect(splitSizes(node)).toEqual([0.5, 0.5]);
  });

  it("moves one border within the minimum", () => {
    expect(resizePair([0.5, 0.5], 0, 0.2, 0.1)).toEqual([0.7, 0.30000000000000004]);
    expect(resizePair([0.5, 0.5], 0, 0.9, 0.1)[0]).toBeCloseTo(0.9);
    expect(resizePair([0.5, 0.5], 0, -0.9, 0.1)[0]).toBeCloseTo(0.1);
  });

  it("stores sizes at a path and clears them", () => {
    const l = setSplitSizes(layoutWith(split()), "sp", "t", [], [2, 1]);
    const r = rootOf(l);
    expect(r.type === "split" && r.sizes?.map((n) => n.toFixed(3))).toEqual(["0.667", "0.333"]);
    const cleared = rootOf(setSplitSizes(l, "sp", "t", [], undefined));
    expect(cleared.type === "split" && cleared.sizes).toBeUndefined();
  });

  it("gives a new sibling half of the split pane", () => {
    const l = splitPane(layoutWith(split([0.6, 0.4])), "sp", "t", "a", "c", "row");
    const r = rootOf(l);
    expect(r.type === "split" && r.sizes?.map((n) => n.toFixed(2))).toEqual(["0.30", "0.30", "0.40"]);
  });

  it("keeps proportions when a pane closes", () => {
    const three: PaneNode = {
      type: "split",
      dir: "row",
      children: [
        { type: "pane", session: "a" },
        { type: "pane", session: "b" },
        { type: "pane", session: "c" },
      ],
      sizes: [0.5, 0.25, 0.25],
    };
    const r = rootOf(removeSession(layoutWith(three), "b"));
    expect(r.type === "split" && r.sizes?.map((n) => n.toFixed(3))).toEqual(["0.667", "0.333"]);
  });

  it("parses stored sizes and drops malformed ones", () => {
    const good = parseLayout(layoutWith(split([3, 1])));
    const r = rootOf(good);
    expect(r.type === "split" && r.sizes).toEqual([0.75, 0.25]);
    const bad = parseLayout(layoutWith({ ...split(), sizes: [1, 2, 3] } as PaneNode));
    const rb = rootOf(bad);
    expect(rb.type === "split" && rb.sizes).toBeUndefined();
  });
});
