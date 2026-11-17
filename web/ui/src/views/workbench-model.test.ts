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
  statusOf,
  tabSessions,
  terminalKey,
  shellEscapePath,
  dropText,
  takeNativeDragPaths,
  workbenchShortcut,
  tabAtShortcut,
  paneInDirection,
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

describe("drop paths", () => {
  it("escapes spaces and shell characters with backslashes", () => {
    expect(shellEscapePath("/Users/me/Desktop/Screenshot 2026-11-17 at 08.50.12.png")).toBe(
      "/Users/me/Desktop/Screenshot\\ 2026-11-17\\ at\\ 08.50.12.png",
    );
    expect(shellEscapePath("/tmp/a'b(c)&$d.txt")).toBe("/tmp/a\\'b\\(c\\)\\&\\$d.txt");
    expect(shellEscapePath("/tmp/caf\u00e9.png")).toBe("/tmp/caf\u00e9.png");
  });

  it("joins several paths with spaces", () => {
    expect(dropText(["/a b", "", "/c"])).toBe("/a\\ b /c");
  });

  it("takes fresh native paths once", () => {
    const holder: { lanaNativeDrag?: { paths?: unknown; at?: unknown } | null } = {
      lanaNativeDrag: { paths: ["/x.png", 3, ""], at: 1000 },
    };
    expect(takeNativeDragPaths(holder, 2000)).toEqual(["/x.png"]);
    expect(takeNativeDragPaths(holder, 2000)).toBeNull();
  });

  it("ignores stale or empty native paths", () => {
    expect(takeNativeDragPaths({ lanaNativeDrag: { paths: ["/x"], at: 0 } }, 11 * 60 * 1000)).toBeNull();
    expect(takeNativeDragPaths({ lanaNativeDrag: { paths: [], at: 5 } }, 6)).toBeNull();
    expect(takeNativeDragPaths({}, 6)).toBeNull();
  });
});

describe("workbenchShortcut", () => {
  const k = (key: string, mods: Partial<{ shiftKey: boolean; altKey: boolean; ctrlKey: boolean; metaKey: boolean; code: string }> = {}) =>
    workbenchShortcut({ key, shiftKey: false, altKey: false, ctrlKey: false, metaKey: true, ...mods });

  it("maps GoRex shortcuts", () => {
    expect(k("t", { code: "KeyT" })).toEqual({ kind: "new-tab" });
    expect(k("T", { code: "KeyT", shiftKey: true })).toEqual({ kind: "new-space" });
    expect(k("d", { code: "KeyD" })).toEqual({ kind: "split", dir: "row" });
    expect(k("D", { code: "KeyD", shiftKey: true })).toEqual({ kind: "split", dir: "col" });
    expect(k("w", { code: "KeyW" })).toEqual({ kind: "close-pane" });
    expect(k("W", { code: "KeyW", shiftKey: true })).toEqual({ kind: "close-tab" });
    expect(k("Enter", { shiftKey: true })).toEqual({ kind: "zoom" });
    expect(k("ArrowLeft", { altKey: true })).toEqual({ kind: "focus", dir: "left" });
    expect(k("3", { code: "Digit3" })).toEqual({ kind: "tab", index: 2 });
    expect(k("{", { code: "BracketLeft", shiftKey: true })).toEqual({ kind: "tab-step", step: -1 });
    expect(k("}", { code: "BracketRight", shiftKey: true })).toEqual({ kind: "tab-step", step: 1 });
    expect(k("Tab", { metaKey: false, ctrlKey: true })).toEqual({ kind: "tab-step", step: 1 });
    expect(k("Tab", { metaKey: false, ctrlKey: true, shiftKey: true })).toEqual({ kind: "tab-step", step: -1 });
    expect(k("R", { code: "KeyR", shiftKey: true })).toEqual({ kind: "rename-tab" });
    expect(k("k", { code: "KeyK" })).toEqual({ kind: "clear" });
    expect(k("=", { code: "Equal" })).toEqual({ kind: "font", step: 1 });
    expect(k("-", { code: "Minus" })).toEqual({ kind: "font", step: -1 });
    expect(k("0", { code: "Digit0" })).toEqual({ kind: "font", step: 0 });
  });

  it("leaves other keys to the terminal and the app", () => {
    expect(k("c", { code: "KeyC" })).toBeNull();
    expect(k("v", { code: "KeyV" })).toBeNull();
    expect(k("ArrowLeft")).toBeNull();
    expect(k("t", { metaKey: false })).toBeNull();
    expect(k("t", { code: "KeyT", ctrlKey: true })).toBeNull();
  });

  it("uses the physical key on other layouts", () => {
    expect(k("е", { code: "KeyT" })).toEqual({ kind: "new-tab" });
  });
});

describe("tabAtShortcut", () => {
  it("picks by position and ⌘9 is the last tab", () => {
    expect(tabAtShortcut(3, 0)).toBe(0);
    expect(tabAtShortcut(3, 4)).toBe(-1);
    expect(tabAtShortcut(3, 8)).toBe(2);
    expect(tabAtShortcut(0, 0)).toBe(-1);
  });
});

describe("paneInDirection", () => {
  /* a | b
     --+--
     c (full width) */
  const rects = [
    { id: "a", left: 0, top: 0, right: 100, bottom: 50 },
    { id: "b", left: 100, top: 0, right: 200, bottom: 50 },
    { id: "c", left: 0, top: 50, right: 200, bottom: 100 },
  ];
  it("finds the neighbour", () => {
    expect(paneInDirection(rects, "a", "right")).toBe("b");
    expect(paneInDirection(rects, "b", "left")).toBe("a");
    expect(paneInDirection(rects, "b", "down")).toBe("c");
    expect(paneInDirection(rects, "c", "up")).toBe("a");
    expect(paneInDirection(rects, "a", "left")).toBeNull();
  });
});
