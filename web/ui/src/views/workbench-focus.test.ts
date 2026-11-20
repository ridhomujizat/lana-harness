import { describe, expect, it } from "vitest";

import { focusAfterClose, forgetFocus, noteFocus } from "./workbench-model";

/* Spec 61 REQ-WP-022, REQ-WP-051: which pane a tab's focus goes to when one of
   its panes is closed, and the per-tab focus history behind it. */

describe("focusAfterClose", () => {
  it("moves the focus to the pane the user used last", () => {
    expect(focusAfterClose(["a", "b", "c"], ["a", "b", "c"], "c", "c")).toBe("b");
  });

  it("with no history, takes the previous pane in tree order, else the next", () => {
    expect(focusAfterClose([], ["a", "b", "c"], "b", "b")).toBe("a");
    expect(focusAfterClose([], ["a", "b", "c"], "a", "a")).toBe("b");
    expect(focusAfterClose([], ["a", "b", "c"], "c", "c")).toBe("b");
  });

  it("skips panes that are closing, then panes that are gone", () => {
    expect(focusAfterClose(["a", "b", "c"], ["a", "b", "c"], "c", "c", new Set(["b"]))).toBe("a");
    expect(focusAfterClose(["gone", "a", "b"], ["a", "b", "c"], "b", "b")).toBe("a");
  });

  it("leaves the focus where it is when the closed pane is not the focused one", () => {
    expect(focusAfterClose(["a", "b"], ["a", "b"], "a", "b")).toBe("a");
    expect(focusAfterClose([], ["a", "b"], "a", "b")).toBe("a");
  });

  it("returns nothing when the tab has no other pane", () => {
    expect(focusAfterClose([], ["a"], "a", "a")).toBeUndefined();
    expect(focusAfterClose(["a"], ["a"], "a", "a")).toBeUndefined();
  });
});

describe("focus history", () => {
  it("moves a pane to the end, dropping an earlier entry", () => {
    expect(noteFocus(["a", "b"], "a")).toEqual(["b", "a"]);
    expect(noteFocus([], "a")).toEqual(["a"]);
    expect(noteFocus(["a"], "")).toEqual(["a"]);
  });

  it("keeps the most recent entries only", () => {
    let recent: string[] = [];
    for (let i = 0; i < 40; i++) recent = noteFocus(recent, "s" + i);
    expect(recent).toHaveLength(32);
    expect(recent[0]).toBe("s8");
    expect(recent[recent.length - 1]).toBe("s39");
  });

  it("forgets a pane", () => {
    expect(forgetFocus(["a", "b", "a"], "a")).toEqual(["b"]);
    expect(forgetFocus(["a"], "")).toEqual(["a"]);
  });
});
