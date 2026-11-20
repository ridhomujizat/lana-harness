import { describe, expect, it } from "vitest";

import { activationAllowed, matchLinksInLine } from "./workbench-links";

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
