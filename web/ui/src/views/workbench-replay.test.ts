import { describe, expect, it } from "vitest";

import { splitReplay, startsWithSizeMarker, type ReplayPart } from "./workbench-replay";

const enc = new TextEncoder();
const dec = new TextDecoder();
const b = (s: string) => enc.encode(s);
const none = new Uint8Array(0);

function show(parts: ReplayPart[]): string[] {
  return parts.map((p) =>
    p.kind === "data" ? "data:" + dec.decode(p.bytes) : p.kind === "size" ? `size:${p.cols}x${p.rows}` : "end",
  );
}

describe("splitReplay", () => {
  it("splits sizes, data and the end marker in order", () => {
    const { parts, carry } = splitReplay(none, b("\x1b]7701;120;30\x07hello\x1b]7701;48;20\x07world\x1b]7702\x07"));
    expect(show(parts)).toEqual(["size:120x30", "data:hello", "size:48x20", "data:world", "end"]);
    expect(carry.length).toBe(0);
  });

  it("passes other escape sequences through as data", () => {
    const s = "\x1b[31mred\x1b]0;title\x07\x1b]7700;x\x07";
    const { parts } = splitReplay(none, b(s));
    expect(show(parts)).toEqual(["data:" + s]);
  });

  it("joins a marker split across messages", () => {
    const first = splitReplay(none, b("abc\x1b]77"));
    expect(show(first.parts)).toEqual(["data:abc"]);
    const second = splitReplay(first.carry, b("01;80;24\x07def"));
    expect(show(second.parts)).toEqual(["size:80x24", "data:def"]);
  });

  it("keeps a marker body cut before its BEL", () => {
    const first = splitReplay(none, b("x\x1b]7701;80;"));
    expect(show(first.parts)).toEqual(["data:x"]);
    const second = splitReplay(first.carry, b("24\x07"));
    expect(show(second.parts)).toEqual(["size:80x24"]);
  });

  it("does not hold back a lone trailing ESC that is not a marker", () => {
    const first = splitReplay(none, b("abc\x1b"));
    expect(show(first.parts)).toEqual(["data:abc"]);
    const second = splitReplay(first.carry, b("[0m"));
    expect(show(second.parts)).toEqual(["data:\x1b[0m"]);
  });

  it("ignores malformed markers", () => {
    const s = "\x1b]7701;0;24\x07\x1b]7701;abc\x07";
    expect(show(splitReplay(none, b(s)).parts)).toEqual(["data:" + s]);
  });
});

describe("startsWithSizeMarker", () => {
  it("detects a replay from a core that sends markers", () => {
    expect(startsWithSizeMarker(b("\x1b]7701;80;24\x07$ "))).toBe(true);
    expect(startsWithSizeMarker(b("$ ls\r\n"))).toBe(false);
  });
});
