import { describe, expect, it } from "vitest";

import type { WorkbenchSession } from "../api/workbench";
import {
  addSpace,
  addTab,
  age,
  closeTab,
  ctrlKey,
  emptyLayout,
  folderName,
  isAgent,
  locate,
  mergeRemote,
  parseLayout,
  removeSession,
  renameSpace,
  renameTab,
  spaceLabel,
  splitPane,
  splitSizes,
  statusOf,
  tabSessions,
  terminalKey,
} from "./workbench-model";

function session(over: Partial<WorkbenchSession>): WorkbenchSession {
  return {
    id: "s1",
    title: "",
    command: ["/bin/zsh"],
    dir: "",
    pid: 1,
    created: "2026-11-16T10:00:00Z",
    cols: 120,
    rows: 36,
    program: "zsh",
    state: "idle",
    exit_code: 0,
    needs_you: false,
    last_output: "",
    attached: 0,
    ...over,
  };
}

describe("workbench layout", () => {
  it("adds a space, tabs and splits", () => {
    let l = addSpace(emptyLayout(), "sxored-hub", "~/sxh");
    const sp = l.spaces[0]!;
    expect(l.activeSpace).toBe(sp.id);
    l = addTab(l, sp.id, "a");
    const tab = l.spaces[0]!.tabs[0]!;
    l = splitPane(l, sp.id, tab.id, "a", "b", "row");
    l = splitPane(l, sp.id, tab.id, "b", "c", "row");
    const root = l.spaces[0]!.tabs[0]!.root;
    expect(root.type).toBe("split");
    expect(tabSessions(l.spaces[0]!.tabs[0]!)).toEqual(["a", "b", "c"]);
    /* Same direction adds a sibling, not a nested split. */
    expect(root.type === "split" && root.children.every((c) => c.type === "pane")).toBe(true);
    l = splitPane(l, sp.id, tab.id, "c", "d", "col");
    expect(tabSessions(l.spaces[0]!.tabs[0]!)).toEqual(["a", "b", "c", "d"]);
    expect(locate(l, "d")?.tab.id).toBe(tab.id);
  });

  it("removes sessions and empty tabs", () => {
    let l = addSpace(emptyLayout(), "home", "");
    const sp = l.spaces[0]!;
    l = addTab(l, sp.id, "a");
    l = addTab(l, sp.id, "b");
    const t1 = l.spaces[0]!.tabs[0]!;
    l = splitPane(l, sp.id, t1.id, "a", "c", "col");
    l = removeSession(l, "a");
    expect(l.spaces[0]!.tabs[0]!.root).toEqual({ type: "pane", session: "c" });
    expect(l.spaces[0]!.tabs[0]!.focus).toBe("c");
    l = removeSession(l, "b");
    expect(l.spaces[0]!.tabs).toHaveLength(1);
    l = closeTab(l, sp.id, t1.id);
    expect(l.spaces[0]!.tabs).toHaveLength(0);
  });

  it("keeps this client's selection when another client saves", () => {
    let local = addSpace(emptyLayout(), "a", "");
    const sp = local.spaces[0]!;
    local = addTab(local, sp.id, "s1");
    local = addTab(local, sp.id, "s2");
    const firstTab = local.spaces[0]!.tabs[0]!.id;
    local = { ...local, spaces: local.spaces.map((s) => ({ ...s, activeTab: firstTab })) };
    /* The other client added a tab and selected it. */
    const remote = addTab(local, sp.id, "s3");
    const merged = mergeRemote(local, remote);
    expect(merged.spaces[0]!.tabs).toHaveLength(3);
    expect(merged.spaces[0]!.activeTab).toBe(firstTab);
  });

  it("parses a stored layout and drops malformed parts", () => {
    const l = parseLayout({
      version: 1,
      grouping: "status",
      activeSpace: "nope",
      spaces: [
        { id: "sp1", name: "a", color: "#000", dir: "/x", tabs: [{ id: "t1", root: { type: "pane", session: "s" } }, { id: "bad", root: { type: "x" } }] },
        { name: "missing id" },
      ],
    });
    expect(l.grouping).toBe("status");
    expect(l.spaces).toHaveLength(1);
    expect(l.spaces[0]!.tabs).toHaveLength(1);
    expect(l.activeSpace).toBe("sp1");
    expect(parseLayout(null).spaces).toEqual([]);
  });
});

describe("split sizes (spec 58)", () => {
  function tabWith(root: unknown) {
    const l = parseLayout({ version: 1, spaces: [{ id: "sp", name: "a", tabs: [{ id: "t", root }] }] });
    return { l, root: l.spaces[0]!.tabs[0]!.root };
  }

  it("keeps stored sizes on parse and reads them back", () => {
    const { root } = tabWith({
      type: "split",
      dir: "row",
      sizes: [0.25, 0.75],
      children: [
        { type: "pane", session: "a" },
        { type: "pane", session: "b" },
      ],
    });
    expect(root.type === "split" && root.sizes).toEqual([0.25, 0.75]);
    expect(splitSizes(root)).toEqual([0.25, 0.75]);
  });

  it("falls back to equal sizes when sizes are invalid", () => {
    const children = [
      { type: "pane", session: "a" },
      { type: "pane", session: "b" },
    ];
    expect(splitSizes(tabWith({ type: "split", dir: "row", children }).root)).toEqual([0.5, 0.5]);
    expect(splitSizes(tabWith({ type: "split", dir: "row", children, sizes: [1] }).root)).toEqual([0.5, 0.5]);
    expect(splitSizes(tabWith({ type: "split", dir: "row", children, sizes: [0, 1] }).root)).toEqual([0.5, 0.5]);
    expect(splitSizes(tabWith({ type: "split", dir: "row", children, sizes: [1, 3] }).root)).toEqual([0.25, 0.75]);
  });

  it("evens out a split a new sibling joins", () => {
    const { l } = tabWith({
      type: "split",
      dir: "row",
      sizes: [0.4, 0.6],
      children: [
        { type: "pane", session: "a" },
        { type: "pane", session: "b" },
      ],
    });
    const next = splitPane(l, "sp", "t", "b", "c", "row");
    const root = next.spaces[0]!.tabs[0]!.root;
    expect(tabSessions(next.spaces[0]!.tabs[0]!)).toEqual(["a", "b", "c"]);
    expect(root.type === "split" && root.sizes).toBeUndefined();
    expect(splitSizes(root)).toEqual([1 / 3, 1 / 3, 1 / 3]);
  });

  it("leaves equal splits without stored sizes", () => {
    let l = addSpace(emptyLayout(), "a", "");
    const sp = l.spaces[0]!;
    l = addTab(l, sp.id, "a");
    const t = l.spaces[0]!.tabs[0]!;
    l = splitPane(l, sp.id, t.id, "a", "b", "row");
    l = splitPane(l, sp.id, t.id, "a", "c", "row");
    const root = l.spaces[0]!.tabs[0]!.root;
    /* Three equal panes: nothing is stored. */
    expect(root.type === "split" && root.sizes).toBeUndefined();
    expect(tabSessions(l.spaces[0]!.tabs[0]!)).toEqual(["a", "c", "b"]);
    const before = parseLayout({
      version: 1,
      spaces: [{ id: "sp", name: "a", tabs: [{ id: "t", root: { type: "split", dir: "row", children: [{ type: "pane", session: "x" }, { type: "pane", session: "y" }] } }] }],
    });
    const after = removeSession(splitPane(before, "sp", "t", "x", "z", "col"), "z");
    expect(after.spaces[0]!.tabs[0]!.root).toEqual(before.spaces[0]!.tabs[0]!.root);
  });

  it("evens out a split a pane leaves", () => {
    const { l } = tabWith({
      type: "split",
      dir: "row",
      sizes: [0.2, 0.3, 0.5],
      children: [
        { type: "pane", session: "a" },
        { type: "pane", session: "b" },
        { type: "pane", session: "c" },
      ],
    });
    const root = removeSession(l, "c").spaces[0]!.tabs[0]!.root;
    expect(splitSizes(root)).toEqual([0.5, 0.5]);
  });
});

describe("rename", () => {
  it("renames spaces and tabs, empty restores the automatic label", () => {
    let l = addSpace(emptyLayout(), "~", "");
    const sp = l.spaces[0]!;
    l = addTab(l, sp.id, "s1");
    const tab = l.spaces[0]!.tabs[0]!;
    l = renameSpace(l, sp.id, " infra ");
    expect(l.spaces[0]!.name).toBe("infra");
    expect(l.spaces[0]!.auto).toBe(false);
    l = renameSpace(l, sp.id, "");
    expect(l.spaces[0]!.auto).toBe(true);
    l = renameTab(l, sp.id, tab.id, "dev server");
    expect(l.spaces[0]!.tabs[0]!.title).toBe("dev server");
    l = renameTab(l, sp.id, tab.id, "  ");
    expect(l.spaces[0]!.tabs[0]!.title).toBeUndefined();
  });
});

describe("herdr-style space names", () => {
  it("names a space after its active terminal's folder", () => {
    expect(folderName("/Users/me")).toBe("~");
    expect(folderName("/home/me/")).toBe("~");
    expect(folderName("/Users/me/coding/lana-harness")).toBe("lana-harness");
    expect(folderName("")).toBe("~");
    let l = addSpace(emptyLayout(), "~", "");
    const sp = l.spaces[0]!;
    expect(spaceLabel(sp, {})).toBe("~");
    l = addTab(l, sp.id, "s1");
    const sessions = { s1: session({ id: "s1", dir: "/Users/me", cwd: "/Users/me/sxh/sxored" }) };
    expect(spaceLabel(l.spaces[0]!, sessions)).toBe("sxored");
    expect(spaceLabel({ ...l.spaces[0]!, auto: false, name: "infra" }, sessions)).toBe("infra");
  });
});

describe("workbench sessions", () => {
  it("knows agents and statuses", () => {
    expect(isAgent(session({ program: "claude" }))).toBe(true);
    expect(isAgent(session({ program: "zsh", labels: { "lana.agent": "scored-hub" } }))).toBe(true);
    expect(isAgent(session({ program: "lazygit" }))).toBe(false);
    expect(statusOf(session({ needs_you: true, state: "working" }))).toBe("blocked");
    expect(statusOf(session({ state: "exited" }))).toBe("exited");
    expect(statusOf(session({ state: "working" }))).toBe("working");
  });

  it("formats ages and ctrl keys", () => {
    const now = Date.parse("2026-11-16T10:00:00Z");
    expect(age("2026-11-16T09:59:30Z", now)).toBe("<1m");
    expect(age("2026-11-16T09:56:00Z", now)).toBe("4m");
    expect(age("2026-11-16T08:00:00Z", now)).toBe("2h");
    expect(ctrlKey("c")).toBe("\x03");
    expect(ctrlKey("C")).toBe("\x03");
    expect(ctrlKey("[")).toBe("\x1b");
    expect(ctrlKey("ab")).toBe("ab");
  });
});

describe("terminalKey", () => {
  const k = (key: string, mods: Partial<{ shiftKey: boolean; altKey: boolean; ctrlKey: boolean; metaKey: boolean }> = {}) =>
    terminalKey({ key, shiftKey: false, altKey: false, ctrlKey: false, metaKey: false, ...mods });

  it("maps Shift+Enter to ESC CR", () => {
    expect(k("Enter", { shiftKey: true })).toBe("\x1b\r");
    expect(k("Enter")).toBeNull();
  });

  it("maps macOS editing keys", () => {
    expect(k("ArrowLeft", { metaKey: true })).toBe("\x01");
    expect(k("ArrowRight", { metaKey: true })).toBe("\x05");
    expect(k("Backspace", { metaKey: true })).toBe("\x15");
    expect(k("ArrowLeft", { altKey: true })).toBe("\x1bb");
    expect(k("ArrowRight", { altKey: true })).toBe("\x1bf");
    expect(k("Backspace", { altKey: true })).toBe("\x1b\x7f");
  });

  it("leaves other keys to xterm", () => {
    expect(k("c", { metaKey: true })).toBeNull();
    expect(k("v", { metaKey: true })).toBeNull();
    expect(k("c", { ctrlKey: true })).toBeNull();
    expect(k("a")).toBeNull();
  });
});
