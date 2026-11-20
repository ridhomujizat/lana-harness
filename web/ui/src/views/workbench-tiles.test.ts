import { describe, expect, it } from "vitest";

import { TILE_COLORS, hashColor, monogram, tileFor, tileWidth } from "./workbench-tiles";

/* Spec 61 REQ-WP-032, REQ-WP-051: the program tile table, shared with the
   native Workbench app. */

describe("program tile table", () => {
  it("gives a plain shell the green prompt tile", () => {
    const tile = tileFor("zsh");
    expect(tile.bg).toBe("#2E2D2B");
    expect(tile.fg).toBe("#5CBA86");
    expect(tile.glyph).toBe("prompt");
    expect(tile.border).toBe("rgba(255, 255, 255, 0.08)");
  });

  it("reads a program's base name", () => {
    expect(tileFor("/bin/bash").glyph).toBe("prompt");
    expect(tileFor("-fish").glyph).toBe("prompt");
    expect(tileFor("pwsh").glyph).toBe("prompt");
  });

  it("gives claude its coral tile and mono logo", () => {
    const tile = tileFor("claude");
    expect(tile.bg).toBe("#D97757");
    expect(tile.fg).toBe("#FFFFFF");
    expect(tile.glyph).toBe("mono");
    expect(tile.logo).toBe("claude");
  });

  it("gives codex a near-white tile with a dark mono logo", () => {
    const tile = tileFor("codex");
    expect(tile.bg).toBe("#F7F7F7");
    expect(tile.fg).toBe("#1A1A1A");
    expect(tile.glyph).toBe("mono");
    expect(tile.logo).toBe("codex");
    expect(tile.border).toBe("rgba(26, 26, 26, 0.16)");
  });

  it("gives a mono agent the near-black tile", () => {
    const cursor = tileFor("cursor");
    expect(cursor.bg).toBe("#1A1A1A");
    expect(cursor.fg).toBe("#FFFFFF");
    expect(cursor.glyph).toBe("mono");
    expect(cursor.logo).toBe("cursor");
    expect(tileFor("grok").glyph).toBe("mono");
    expect(tileFor("mastracode").logo).toBe("mastra");
    expect(tileFor("hermes").logo).toBe("hermesagent");
    expect(tileFor("copilot").bg).toBe("#24292F");
  });

  it("gives a colour-logo agent the neutral tile", () => {
    const gemini = tileFor("gemini");
    expect(gemini.bg).toBe("#2E2D2B");
    expect(gemini.fg).toBe("#FFFFFF");
    expect(gemini.glyph).toBe("color");
    expect(gemini.logo).toBe("geminicli-color");
    expect(tileFor("agy").logo).toBe("antigravity-color");
    expect(tileFor("kilo").glyph).toBe("mono");
    expect(tileFor("kilo").logo).toBe("kilocode");
  });

  it("shares the pi glyph and tells omp apart by its accent", () => {
    expect(tileFor("pi")).toMatchObject({ bg: "#1A1A1A", fg: "#FFFFFF", glyph: "text", text: "π" });
    expect(tileFor("pi").accent).toBeUndefined();
    expect(tileFor("omp").text).toBe("π");
    expect(tileFor("omp").accent).toBe("#7652C2");
  });

  it("gives an unknown program a monogram on a hashed colour", () => {
    const droid = tileFor("droid");
    expect(droid.glyph).toBe("text");
    expect(droid.text).toBe("D");
    expect(droid.fg).toBe("#FFFFFF");
    expect(droid.bg).toBe(hashColor("droid"));
    expect(TILE_COLORS).toContain(droid.bg);
    expect(droid.bg).toBe("#7652C2");
  });

  it("keeps the landscape width of a tile", () => {
    expect(tileWidth(16)).toBe(21);
    expect(tileWidth(18)).toBe(24);
  });

  it("has a stable monogram", () => {
    expect(monogram("lana")).toBe("L");
    expect(monogram("")).toBe("?");
    expect(hashColor("droid")).toBe(hashColor("droid"));
  });
});
