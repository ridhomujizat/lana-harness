import { describe, expect, it } from "vitest";

import type { GitHunk, GitInfo } from "../api/workbench-git";
import { blockCommentStates, groupByWorktree, gutterMarks, highlightLine, panelWorktree, relativeTo, sharedAgents, shortAge, shortBranch, splitRows, unifiedRows, sessionDir, repoDisplay, isUnder, orderRootRepos, defaultOpenGroups, rootRows, ROOT_GROUP_FILES } from "./workbench-git-model";

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

describe("block comments", () => {
  it("tracks /* */ across lines and colours them", () => {
    const lines = ["a /* start", "middle", "end */ b", "// x /* not", "c"];
    expect(blockCommentStates(lines, "typescript")).toEqual([false, true, true, false, false]);
    expect(highlightLine("middle", "typescript", true)).toEqual([{ text: "middle", kind: "com" }]);
    expect(highlightLine("end */ b", "typescript", true)[0]).toEqual({ text: "end */", kind: "com" });
    expect(blockCommentStates(["/* x"], "python")).toEqual([false]);
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

describe("spec 69: session worktree order", () => {
  it("prefers the reported work_dir, then cwd, then dir", () => {
    expect(sessionDir({ work_dir: "/r/wt", cwd: "/r", dir: "/" })).toBe("/r/wt");
    expect(sessionDir({ cwd: "/r", dir: "/" })).toBe("/r");
    expect(sessionDir({ dir: "/d" })).toBe("/d");
  });
});

describe("spec 70: root folders", () => {
  const repo = (name: string, changed: number) => ({ name, worktree: "/root/" + name, changed });

  it("drops the root prefix from repo names", () => {
    expect(repoDisplay("sxored", "sxored-be-ocrengine")).toBe("be-ocrengine");
    expect(repoDisplay("sxored", "sxored-")).toBe("sxored-");
    expect(repoDisplay("rdms", "be-rdms")).toBe("be-rdms");
  });

  it("checks paths under a folder", () => {
    expect(isUnder("/a/b", "/a")).toBe(true);
    expect(isUnder("/a", "/a/")).toBe(true);
    expect(isUnder("/ab", "/a")).toBe(false);
  });

  it("orders changed repos: reported first, then count, then name", () => {
    const list = [repo("b", 2), repo("a", 2), repo("c", 9), repo("d", 0), repo("e", 5)];
    expect(orderRootRepos(list).map((r) => r.name)).toEqual(["c", "e", "a", "b"]);
    expect(orderRootRepos(list, "/root/e").map((r) => r.name)).toEqual(["e", "c", "a", "b"]);
    /* A clean repo the agent reported still shows. */
    expect(orderRootRepos(list, "/root/d").map((r) => r.name)[0]).toBe("d");
  });

  it("opens groups by default only for at most 3 changed repos", () => {
    expect(defaultOpenGroups([repo("a", 1), repo("b", 1), repo("c", 1)]).size).toBe(3);
    expect(defaultOpenGroups([repo("a", 1), repo("b", 1), repo("c", 1), repo("d", 1)]).size).toBe(0);
  });

  it("flattens groups, files, the more row and the clean row", () => {
    const a = repo("a", 12);
    const b = repo("b", 1);
    const files = Array.from({ length: 12 }, (_, i) => "f" + i);
    const rows = rootRows({
      changed: [a, b],
      clean: [repo("c", 0), repo("d", 0)],
      open: new Set([a.worktree]),
      files: { [a.worktree]: files },
      showAll: new Set(),
      cleanOpen: false,
    });
    expect(rows.map((r) => r.kind)).toEqual(["group", ...Array(ROOT_GROUP_FILES).fill("file"), "more", "group", "clean"]);
    const more = rows.find((r) => r.kind === "more");
    expect(more && more.kind === "more" ? more.hidden : 0).toBe(2);
    const reported = rootRows({ changed: [b], clean: [], open: new Set(), files: {}, showAll: new Set(), reported: b.worktree, cleanOpen: false });
    expect(reported.map((r) => r.kind)).toEqual(["group", "loading"]);
    expect(reported[0]?.kind === "group" && reported[0].here).toBe(true);
  });
});
