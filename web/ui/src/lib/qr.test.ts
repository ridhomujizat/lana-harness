import { describe, expect, it } from "vitest";
import { encodeQr, qrPath } from "./qr";

/* The encoder output was checked with zbarimg when it was written (spec 57):
   these tests pin the structure so a change cannot silently break scanning. */

function finderAt(grid: boolean[][], x: number, y: number): boolean {
  for (let dy = 0; dy < 7; dy++) {
    for (let dx = 0; dx < 7; dx++) {
      const ring = Math.max(Math.abs(dx - 3), Math.abs(dy - 3));
      const want = ring !== 2;
      if ((grid[y + dy]?.[x + dx] ?? false) !== want) {
        return false;
      }
    }
  }
  return true;
}

describe("encodeQr", () => {
  it("picks version 1 for a short text and draws the three finders", () => {
    const grid = encodeQr("hello");
    expect(grid.length).toBe(21);
    expect(finderAt(grid, 0, 0)).toBe(true);
    expect(finderAt(grid, 14, 0)).toBe(true);
    expect(finderAt(grid, 0, 14)).toBe(true);
  });

  it("fits a ts.net address in version 3", () => {
    expect(encodeQr("https://ridho-mbp.tail3f2a1.ts.net").length).toBe(29);
  });

  it("grows the version for long text and keeps the timing pattern", () => {
    const grid = encodeQr("x".repeat(300));
    expect(grid.length).toBeGreaterThan(45);
    for (let i = 8; i < grid.length - 8; i++) {
      expect(grid[6]?.[i]).toBe(i % 2 === 0);
    }
  });

  it("is deterministic", () => {
    expect(encodeQr("lana")).toEqual(encodeQr("lana"));
  });

  it("throws when the text cannot fit", () => {
    expect(() => encodeQr("x".repeat(3000))).toThrow();
  });
});

describe("qrPath", () => {
  it("draws one rectangle per run of dark modules, offset by the quiet zone", () => {
    expect(qrPath([[true, true, false, true]], 2)).toBe("M2 2h2v1h-2zM5 2h1v1h-1z");
  });
});
