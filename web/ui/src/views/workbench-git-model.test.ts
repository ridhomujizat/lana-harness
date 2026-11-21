import { describe, expect, it } from "vitest";

import type { GitHunk, GitInfo } from "../api/workbench-git";
import {
  groupByWorktree,
  gutterMarks,
  highlightLine,
  panelWorktree,
  relativeTo,
  sharedAgents,
  shortAge,
  shortBranch,
  splitRows,
  unifiedRows,
} from "./workbench-git-model";

const hunk: GitHunk = {
  header: "@@ -10,6 +10,7 @@ fn",
  old_start: 10,
  new_start: 10,
  lines: [
    { kind: "ctx", old: 10, new: 10, text: "a" },
    { kind: "del", old: 11, text: "b" },
    { kind: "del", old: 12, text: "c" },
    { kind: "add", new: 11, text: "B" },
    { kind: "add", new: 12, text: "C" },
    { kind: "add", new: 13, text: "D" },
    { kind: "ctx", old: 13, new: 14, text: "e" },
    { kind: "del", old: 14, text: "f" },
    { kind: "ctx", old: 15, new: 15, text: "g" },
  ],
};

function info(worktree: string): GitInfo {
  return { repo: "/r/.git", worktree, branch: "feature/x", short: "x", head: "abc", color: "#000", detached: false, main: false };
}

describe("shortBranch", () => {
  it("drops the type prefix", () => {
    expect(shortBranch("feature/router")).toBe("router");
    expect(shortBranch("dev/integration")).toBe("integration");
    expect(shortBranch("main")).toBe("main");
  });
});

describe("diff rows", () => {
  it("puts the hunk header before its lines in the unified view", () => {
    const rows = unifiedRows([hunk]);
    expect(rows[0]).toEqual({ type: "hunk", header: hunk.header, index: 0 });
    expect(rows).toHaveLength(1 + hunk.lines.length);
  });

  it("pairs deletions with the additions after them in the split view", () => {
    const rows = splitRows([hunk]).filter((r) => r.type === "pair");
    expect(rows[0]).toMatchObject({ left: { text: "a" }, right: { text: "a" } });
    expect(rows[1]).toMatchObject({ left: { text: "b" }, right: { text: "B" } });
    expect(rows[2]).toMatchObject({ left: { text: "c" }, right: { text: "C" } });
    expect(rows[3]).toMatchObject({ left: undefined, right: { text: "D" } });
    expect(rows[5]).toMatchObject({ left: { text: "f" }, right: undefined });
  });
});

describe("gutterMarks", () => {
  it("marks replaced lines mod, extra lines add, and lone deletions del", () => {
    const marks = gutterMarks([hunk]);
    expect(marks.get(11)).toBe("mod");
    expect(marks.get(12)).toBe("mod");
    expect(marks.get(13)).toBe("add");
    expect(marks.get(15)).toBe("del");
    expect(marks.has(10)).toBe(false);
    expect(marks.has(14)).toBe(false);
  });
});

describe("highlightLine", () => {
  it("finds keywords, strings, comments and calls", () => {
    const tokens = highlightLine('const x = useState("a"); // hi', "typescript");
    expect(tokens.find((t) => t.text === "const")?.kind).toBe("kw");
    expect(tokens.find((t) => t.text === '"a"')?.kind).toBe("str");
    expect(tokens.find((t) => t.text === "useState")?.kind).toBe("fn");
    expect(tokens.at(-1)).toEqual({ text: "// hi", kind: "com" });
    expect(tokens.map((t) => t.text).join("")).toBe('const x = useState("a"); // hi');
  });

  it("treats # as a comment only in hash-comment languages", () => {
    expect(highlightLine("# note", "shell")[0]?.kind).toBe("com");
    expect(highlightLine("a # b", "go").map((t) => t.text).join("")).toBe("a # b");
  });
});

describe("worktree grouping", () => {
  const git: Record<string, GitInfo> = { s1: info("/w/a"), s2: info("/w/b"), s3: info("/w/a"), s4: info("/w/a") };
  const gitOf = (id: string) => git[id];

  it("groups panes by worktree in pane order", () => {
    const groups = groupByWorktree(["s1", "s2", "s3", "x"], gitOf);
    expect(groups.map((g) => [g.info.worktree, g.sessions])).toEqual([
      ["/w/a", ["s1", "s3"]],
      ["/w/b", ["s2"]],
    ]);
  });

  it("flags agents that share a worktree, ignoring plain shells", () => {
    const shared = sharedAgents(["s1", "s2", "s3", "s4"], gitOf, (id) => id !== "s4");
    expect(shared.get("s1")).toEqual(["s3"]);
    expect(shared.get("s3")).toEqual(["s1"]);
    expect(shared.has("s2")).toBe(false);
    expect(shared.has("s4")).toBe(false);
  });

  it("follows focus unless pinned", () => {
    expect(panelWorktree(null, "/w/b", "/w/a")).toBe("/w/b");
    expect(panelWorktree("/w/a", "/w/b", null)).toBe("/w/a");
    expect(panelWorktree(null, undefined, "/w/a")).toBe("/w/a");
  });
});

describe("paths and ages", () => {
  it("makes paths relative to a worktree", () => {
    expect(relativeTo("/w/a", "/w/a/src/x.ts")).toBe("src/x.ts");
    expect(relativeTo("/w/a/", "/w/a")).toBe("");
    expect(relativeTo("/w/a", "/w/ab/x")).toBeNull();
  });

  it("shortens ages", () => {
    const now = Date.parse("2026-11-21T12:00:00Z");
    expect(shortAge("2026-11-21T11:48:00Z", now)).toBe("12m");
    expect(shortAge("2026-11-21T09:00:00Z", now)).toBe("3h");
    expect(shortAge("2026-11-07T12:00:00Z", now)).toBe("2w");
    expect(shortAge("bad", now)).toBe("—");
  });
});
