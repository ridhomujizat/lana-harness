import { describe, expect, it } from "vitest";
import { clampView, fitScale, zoomAt } from "./zoom-image";

const box = { w: 400, h: 300 };
const img = { w: 800, h: 300 };

describe("zoom image view", () => {
  it("fits a large image and never upscales a small one", () => {
    expect(fitScale(box, img)).toBe(0.5);
    expect(fitScale(box, { w: 100, h: 50 })).toBe(1);
  });

  it("centres an image smaller than the box", () => {
    const v = clampView({ scale: 0.5, x: 99, y: 99, fit: true }, box, img);
    expect(v.x).toBe(0);
    expect(v.y).toBe(75);
  });

  it("keeps a zoomed image from leaving a gap at the edges", () => {
    const v = clampView({ scale: 1, x: 50, y: 0, fit: false }, box, img);
    expect(v.x).toBe(0);
    expect(clampView({ scale: 1, x: -900, y: 0, fit: false }, box, img).x).toBe(-400);
  });

  it("zooms around the pointer and leaves fit", () => {
    const start = { scale: 1, x: -200, y: 0, fit: false };
    const v = zoomAt(start, 2, 200, 150, box, img);
    expect(v.scale).toBe(2);
    expect(v.fit).toBe(false);
    /* The image point under the pointer stays under it. */
    expect((200 - v.x) / v.scale).toBeCloseTo((200 - start.x) / start.scale);
  });

  it("caps the zoom", () => {
    expect(zoomAt({ scale: 1, x: 0, y: 0, fit: false }, 100, 0, 0, box, img).scale).toBe(16);
    expect(zoomAt({ scale: 1, x: 0, y: 0, fit: false }, 0.001, 0, 0, box, img).scale).toBe(0.125);
  });
});
