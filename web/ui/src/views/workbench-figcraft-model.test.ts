import { describe, expect, it } from "vitest";

import type { FigLayer } from "../api/workbench-figcraft";
import {
  MAX_ZOOM,
  MIN_ZOOM,
  clampZoom,
  detailRequest,
  fitRect,
  hitTest,
  isFigcraftPath,
  layerIcon,
  pageRenderScale,
  toPage,
  toScreen,
  zoomAt,
  zoomLabel,
} from "./workbench-figcraft-model";

function layer(id: number, x: number, y: number, w: number, h: number, visible = true): FigLayer {
  return { id, name: "L" + id, type: "FRAME", bounds: { x, y, width: w, height: h }, child_count: 0, visible };
}

describe("fitRect", () => {
  it("fits and centres a wide page with padding", () => {
    const v = fitRect({ x: 0, y: 0, width: 4970, height: 1625 }, { width: 491, height: 752 });
    expect(v.zoom).toBeCloseTo((491 - 48) / 4970, 6);
    const tl = toScreen(v, 0, 0);
    const br = toScreen(v, 4970, 1625);
    expect(tl.x).toBeCloseTo(24, 6);
    expect(br.x).toBeCloseTo(491 - 24, 6);
    expect((tl.y + br.y) / 2).toBeCloseTo(752 / 2, 6);
  });

  it("respects a page origin away from zero", () => {
    const v = fitRect({ x: -200, y: 100, width: 400, height: 400 }, { width: 448, height: 448 });
    expect(toScreen(v, -200, 100).x).toBeCloseTo(24, 6);
    expect(toScreen(v, 200, 500).y).toBeCloseTo(424, 6);
  });

  it("clamps the zoom for a tiny page", () => {
    expect(fitRect({ x: 0, y: 0, width: 1, height: 1 }, { width: 1000, height: 1000 }).zoom).toBe(MAX_ZOOM);
  });
});

describe("zoom", () => {
  it("keeps the point under the pointer", () => {
    const v = { zoom: 0.5, tx: 10, ty: 20 };
    const before = toPage(v, 300, 200);
    const z = zoomAt(v, 2, 300, 200);
    const after = toPage(z, 300, 200);
    expect(z.zoom).toBe(1);
    expect(after.x).toBeCloseTo(before.x, 9);
    expect(after.y).toBeCloseTo(before.y, 9);
  });

  it("clamps between 2% and 800%", () => {
    expect(clampZoom(0.001)).toBe(MIN_ZOOM);
    expect(clampZoom(50)).toBe(MAX_ZOOM);
    expect(clampZoom(Number.NaN)).toBe(1);
    expect(zoomAt({ zoom: 8, tx: 0, ty: 0 }, 2, 0, 0).zoom).toBe(8);
  });

  it("labels small zooms with one decimal", () => {
    expect(zoomLabel(0.0892)).toBe("8.9%");
    expect(zoomLabel(0.09)).toBe("9%");
    expect(zoomLabel(0.42)).toBe("42%");
  });
});

describe("hitTest", () => {
  const top = [layer(1, 0, 0, 100, 100), layer(2, 50, 50, 100, 100), layer(3, 0, 0, 500, 500, false)];
  const children = new Map<number, FigLayer[]>([[2, [layer(20, 60, 60, 20, 20), layer(21, 60, 60, 10, 10)]]]);

  it("picks the topmost visible layer, then descends into loaded children", () => {
    expect(hitTest(top, children, 65, 65).map((l) => l.id)).toEqual([2, 21]);
    expect(hitTest(top, children, 75, 75).map((l) => l.id)).toEqual([2, 20]);
    expect(hitTest(top, children, 10, 10).map((l) => l.id)).toEqual([1]);
  });

  it("skips hidden layers and empty space", () => {
    expect(hitTest(top, children, 400, 400)).toEqual([]);
  });
});

describe("detailRequest", () => {
  const top = [layer(1, 0, 0, 1440, 1625), layer(2, 1540, 0, 1440, 1024)];
  const vp = { width: 1000, height: 700 };

  it("asks nothing while the page render is sharp enough", () => {
    expect(detailRequest({ zoom: 0.5, tx: 0, ty: 0 }, vp, 1, 2, top)).toBeNull();
  });

  it("asks the frame that fills the view at a sharper scale", () => {
    expect(detailRequest({ zoom: 1.5, tx: -100, ty: -100 }, vp, 1, 2, top)).toEqual({ node: 1, scale: 3 });
    expect(detailRequest({ zoom: 3, tx: -100, ty: -100 }, vp, 1, 2, top)).toEqual({ node: 1, scale: 4 });
  });

  it("asks nothing when no frame covers half the view", () => {
    expect(detailRequest({ zoom: 2, tx: -2850, ty: -2100 }, vp, 1, 1, top)).toBeNull();
  });
});

describe("helpers", () => {
  it("chooses a page render scale for the screen", () => {
    expect(pageRenderScale(0.09, 2)).toBe(0.25);
    expect(pageRenderScale(0.42, 2)).toBe(1);
    expect(pageRenderScale(3, 2)).toBe(2);
  });

  it("maps layer types to icons", () => {
    expect(layerIcon("FRAME")).toBe("frame");
    expect(layerIcon("text")).toBe("type");
    expect(layerIcon("WHATEVER")).toBe("shapes");
  });

  it("recognises .figcraft paths", () => {
    expect(isFigcraftPath("design/lana-design-system.figcraft")).toBe(true);
    expect(isFigcraftPath("design/a.figcraft.json")).toBe(false);
  });
});
