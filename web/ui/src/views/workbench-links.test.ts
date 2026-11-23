import { describe, expect, it } from "vitest";

import { activationAllowed, matchLinksInLine, wrappedLink, type TermLine } from "./workbench-links";

/* Spec 61 REQ-WP-010, REQ-WP-051: the file paths in a line of terminal text. */

function texts(line: string): string[] {
  return matchLinksInLine(line).map((match) => match.text);
}

describe("matchLinksInLine path rules", () => {
  it("matches an absolute path", () => {
    expect(texts("/Users/me/code/lana/main.go")).toEqual(["/Users/me/code/lana/main.go"]);
  });

  it("matches ~/, ./ and ../", () => {
    expect(texts("~/code/x.go")).toEqual(["~/code/x.go"]);
    expect(texts("./build.sh")).toEqual(["./build.sh"]);
    expect(texts("../src/main.go")).toEqual(["../src/main.go"]);
  });

  it("matches any path with a slash, with or without an extension", () => {
    expect(texts("src/views/workbench.tsx")).toEqual(["src/views/workbench.tsx"]);
    expect(texts("cmd/lana")).toEqual(["cmd/lana"]);
  });

  it("matches a bare file name only when it has an extension", () => {
    expect(texts("README.md")).toEqual(["README.md"]);
    expect(texts("main.go")).toEqual(["main.go"]);
    expect(texts("hello")).toEqual([]);
    expect(texts("world")).toEqual([]);
  });

  it("does not match a trailing-space list of bare words", () => {
    expect(texts("build test deploy")).toEqual([]);
  });

  it("keeps the offsets of each match", () => {
    const line = "open src/a.go now";
    const [match] = matchLinksInLine(line);
    expect(match).toBeDefined();
    expect(line.slice(match!.start, match!.end)).toBe("src/a.go");
  });

  it("finds several paths on one line", () => {
    expect(texts("/etc/hosts and ~/notes.txt")).toEqual(["/etc/hosts", "~/notes.txt"]);
  });

  it("trims quotes, brackets and sentence punctuation", () => {
    expect(texts('"src/a.go", (see src/b.ts).')).toEqual(["src/a.go", "src/b.ts"]);
  });
});

describe("matchLinksInLine line and column suffix", () => {
  it("reads a :line suffix", () => {
    const [match] = matchLinksInLine("src/a.go:42");
    expect(match?.text).toBe("src/a.go:42");
    expect(match?.line).toBe(42);
    expect(match?.col).toBeUndefined();
  });

  it("reads a :line:col suffix", () => {
    const [match] = matchLinksInLine("/tmp/report.md:12:7");
    expect(match?.text).toBe("/tmp/report.md:12:7");
    expect(match?.line).toBe(12);
    expect(match?.col).toBe(7);
  });

  it("applies the suffix to a bare file name too", () => {
    const [match] = matchLinksInLine("main.go:3");
    expect(match?.text).toBe("main.go:3");
    expect(match?.line).toBe(3);
  });
});

describe("matchLinksInLine URLs", () => {
  it("does not match http or https URLs", () => {
    expect(texts("http://example.com/a.ts")).toEqual([]);
    expect(texts("https://example.com/group/repo")).toEqual([]);
    expect(texts("see https://example.com/a.ts:12")).toEqual([]);
    expect(texts("https://example.com/a.ts.")).toEqual([]);
  });

  it("does not match file:// or other schemes", () => {
    expect(texts("file:///etc/passwd")).toEqual([]);
    expect(texts("file:///Users/me/code/x.go:5")).toEqual([]);
    expect(texts("ftp://host/path")).toEqual([]);
    expect(texts("javascript:alert(1)")).toEqual([]);
  });

  it("leaves a path that follows a URL alone", () => {
    expect(texts("https://example.com then src/a.go")).toEqual(["src/a.go"]);
  });
});

describe("activationAllowed", () => {
  it("uses ⌘ on macOS and Ctrl elsewhere", () => {
    expect(activationAllowed({ metaKey: true, ctrlKey: false }, true, false)).toBe(true);
    expect(activationAllowed({ metaKey: false, ctrlKey: true }, true, false)).toBe(false);
    expect(activationAllowed({ metaKey: false, ctrlKey: true }, false, false)).toBe(true);
    expect(activationAllowed({ metaKey: true, ctrlKey: false }, false, false)).toBe(false);
  });

  it("accepts a plain click on a touch device", () => {
    expect(activationAllowed({ metaKey: false, ctrlKey: false }, true, true)).toBe(true);
    expect(activationAllowed({ metaKey: false, ctrlKey: false }, false, true)).toBe(true);
  });
});

/* A long path broken over terminal lines opens as one path. */
describe("wrappedLink", () => {
  const COLS = 40;
  function lines(...rows: Array<string | [string, boolean]>): (row: number) => TermLine | undefined {
    const list = rows.map((r) => (typeof r === "string" ? { text: r, wrapped: false } : { text: r[0], wrapped: r[1] }));
    return (row) => list[row];
  }
  function at(getLine: (row: number) => TermLine | undefined, row: number, index = 0) {
    const [match] = matchLinksInLine(getLine(row)!.text).slice(index);
    return wrappedLink(getLine, row, match!, COLS);
  }

  /* Claude Code breaks the line itself and indents the rest. */
  const hard = lines(
    "  - Light: /private/tmp/scratch/border-l", // 40 cells: runs to the edge
    "    ight.png",
    "  - Dark: /private/tmp/x.png"
  );

  it("joins a hard-wrapped, indented path from its first piece", () => {
    expect(hard(0)!.text.length).toBe(COLS);
    const link = at(hard, 0);
    expect(link?.text).toBe("/private/tmp/scratch/border-light.png");
    expect(link).toMatchObject({ startRow: 0, startCol: 11, endRow: 1, endCol: 12 });
  });

  it("joins the same path from its second piece", () => {
    expect(at(hard, 1)?.text).toBe("/private/tmp/scratch/border-light.png");
  });

  it("leaves a path alone when its line stops short of the edge", () => {
    expect(at(hard, 2)).toBeNull();
  });

  it("joins xterm soft wraps over more than two lines", () => {
    const soft = lines(
      "open /aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      ["bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", true],
      ["ccc/main.go now", true]
    );
    expect(at(soft, 0)?.text).toBe("/" + "a".repeat(35) + "b".repeat(40) + "ccc/main.go");
    expect(at(soft, 2)?.text).toBe("/" + "a".repeat(35) + "b".repeat(40) + "ccc/main.go");
  });

  it("does not join across a blank line", () => {
    const gap = lines("see /private/tmp/aaaaaaaaaaaaaaaaaaaaaaaa", "", "x.png");
    expect(at(gap, 0)).toBeNull();
  });

  it("keeps the trailing punctuation out of the joined path", () => {
    const punct = lines("file /Users/me/code/project/src/views/wo", "  rkbench.tsx).");
    expect(at(punct, 0)?.text).toBe("/Users/me/code/project/src/views/workbench.tsx");
  });
});

describe("paths with spaces", () => {
  it("runs an absolute path on to its file name", () => {
    const [m] = matchLinksInLine("  /Users/r/Documents/ridho-note/00 Inbox/Task Infomedia Imporve VA.md");
    expect(m?.text).toBe("/Users/r/Documents/ridho-note/00 Inbox/Task Infomedia Imporve VA.md");
    expect(m?.short).toBe("/Users/r/Documents/ridho-note/00");
  });

  it("stops at the first file name and keeps what follows", () => {
    const links = matchLinksInLine("/Users/r/Documents/screenshot/Screenshot 2026-10-10 at 20.12.00.png (folder in /tmp/x.md)");
    expect(links.map((l) => l.text)).toEqual(["/Users/r/Documents/screenshot/Screenshot 2026-10-10 at 20.12.00.png", "/tmp/x.md"]);
  });

  it("reads a quoted path with spaces whole", () => {
    const [m] = matchLinksInLine("open '/Users/r/Downloads/WhatsApp Image 2026-11-20 at 00.06.33.jpeg' now");
    expect(m?.text).toBe("/Users/r/Downloads/WhatsApp Image 2026-11-20 at 00.06.33.jpeg");
  });

  it("leaves a path with an extension and plain words alone", () => {
    expect(matchLinksInLine("/a/b.md and c.md").map((l) => l.text)).toEqual(["/a/b.md", "c.md"]);
    expect(matchLinksInLine("/usr/bin is a folder").map((l) => l.text)).toEqual(["/usr/bin"]);
  });
});
