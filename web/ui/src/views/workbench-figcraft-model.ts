import type { FigLayer, FigRect } from "../api/workbench-figcraft";

/* Spec 67 REQ-FV-064…066, 102: the pure model of the FigCraft viewer canvas.
   A view maps page units to screen pixels: screen = page * zoom + t. */

export interface View {
  zoom: number;
  tx: number;
  ty: number;
}

export interface Viewport {
  width: number;
  height: number;
}

export const MIN_ZOOM = 0.02;
export const MAX_ZOOM = 8;
export const FIT_PAD = 24;

export function clampZoom(zoom: number): number {
  if (!Number.isFinite(zoom) || zoom <= 0) return 1;
  return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoom));
}

/* Fit a page rectangle in the viewport with padding, centred (REQ-FV-064). */
export function fitRect(rect: FigRect, vp: Viewport, pad = FIT_PAD): View {
  if (rect.width <= 0 || rect.height <= 0 || vp.width <= 0 || vp.height <= 0) {
    return { zoom: 1, tx: pad - rect.x, ty: pad - rect.y };
  }
  const zoom = clampZoom(Math.min((vp.width - 2 * pad) / rect.width, (vp.height - 2 * pad) / rect.height));
  return {
    zoom,
    tx: (vp.width - rect.width * zoom) / 2 - rect.x * zoom,
    ty: (vp.height - rect.height * zoom) / 2 - rect.y * zoom,
  };
}

/* Zoom by factor keeping the page point under (sx, sy) in place. */
export function zoomAt(view: View, factor: number, sx: number, sy: number): View {
  const zoom = clampZoom(view.zoom * factor);
  const k = zoom / view.zoom;
  return { zoom, tx: sx - (sx - view.tx) * k, ty: sy - (sy - view.ty) * k };
}

/* Set an absolute zoom around the viewport centre. */
export function zoomTo(view: View, zoom: number, vp: Viewport): View {
  return zoomAt(view, clampZoom(zoom) / view.zoom, vp.width / 2, vp.height / 2);
}

export function toScreen(view: View, x: number, y: number): { x: number; y: number } {
  return { x: x * view.zoom + view.tx, y: y * view.zoom + view.ty };
}

export function toPage(view: View, sx: number, sy: number): { x: number; y: number } {
  return { x: (sx - view.tx) / view.zoom, y: (sy - view.ty) / view.zoom };
}

export function rectToScreen(view: View, r: FigRect): FigRect {
  const p = toScreen(view, r.x, r.y);
  return { x: p.x, y: p.y, width: r.width * view.zoom, height: r.height * view.zoom };
}

/* The page rectangle that the viewport shows. */
export function visibleRect(view: View, vp: Viewport): FigRect {
  const a = toPage(view, 0, 0);
  return { x: a.x, y: a.y, width: vp.width / view.zoom, height: vp.height / view.zoom };
}

function contains(r: FigRect, x: number, y: number): boolean {
  return x >= r.x && y >= r.y && x <= r.x + r.width && y <= r.y + r.height;
}

function overlap(a: FigRect, b: FigRect): number {
  const w = Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x);
  const h = Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y);
  return w > 0 && h > 0 ? w * h : 0;
}

/* The deepest loaded visible layer under a page point (REQ-FV-065). FigCraft
   lists children bottom first, so the last match in a list is on top. Children
   not loaded yet stop the descent at their parent. */
export function hitTest(
  top: FigLayer[],
  children: ReadonlyMap<number, FigLayer[]>,
  x: number,
  y: number
): FigLayer[] {
  const path: FigLayer[] = [];
  let list: FigLayer[] | undefined = top;
  while (list) {
    let hit: FigLayer | undefined;
    for (let i = list.length - 1; i >= 0; i--) {
      const l = list[i];
      if (l && l.visible && contains(l.bounds, x, y)) {
        hit = l;
        break;
      }
    }
    if (!hit) break;
    path.push(hit);
    list = children.get(hit.id);
  }
  return path;
}

/* When the page render is too coarse for the zoom, ask for the top-level layer
   that fills most of the view at a sharper scale (REQ-FV-066). */
export function detailRequest(
  view: View,
  vp: Viewport,
  renderScale: number,
  dpr: number,
  top: FigLayer[]
): { node: number; scale: number } | null {
  const want = view.zoom * dpr;
  if (want <= renderScale * 1.25) return null;
  const vis = visibleRect(view, vp);
  const area = vis.width * vis.height;
  if (area <= 0) return null;
  let best: FigLayer | null = null;
  let bestArea = 0;
  for (const l of top) {
    if (!l.visible) continue;
    const a = overlap(vis, l.bounds);
    if (a > bestArea) {
      best = l;
      bestArea = a;
    }
  }
  if (!best || bestArea < area * 0.5) return null;
  const scale = Math.min(4, Math.ceil(want * 2) / 2);
  if (scale <= renderScale) return null;
  return { node: best.id, scale };
}

/* The scale to ask a page render at: sharp enough for the fitted view on this
   screen, at most 2 (the daemon also caps the longest side). */
export function pageRenderScale(fitZoom: number, dpr: number): number {
  return Math.min(2, Math.max(0.25, Math.ceil(fitZoom * dpr * 4) / 4));
}

const TYPE_ICONS: Record<string, string> = {
  FRAME: "frame",
  SECTION: "square-dashed",
  COMPONENT: "component",
  COMPONENT_SET: "component",
  INSTANCE: "diamond",
  TEXT: "type",
  RECTANGLE: "square",
  ELLIPSE: "circle",
  POLYGON: "triangle",
  STAR: "star",
  LINE: "minus",
  ARROW: "move-up-right",
  VECTOR: "pen-tool",
  BOOLEAN_OPERATION: "squares-unite",
  GROUP: "group",
  IMAGE: "image",
  SLICE: "slice",
};

export function layerIcon(type: string): string {
  return TYPE_ICONS[type.toUpperCase()] ?? "shapes";
}

export function zoomLabel(zoom: number): string {
  const pct = zoom * 100;
  return (pct < 10 ? pct.toFixed(1).replace(/\.0$/, "") : Math.round(pct).toString()) + "%";
}

export function sizeLabel(r: FigRect): string {
  return `${Math.round(r.width)} × ${Math.round(r.height)}`;
}

export function isFigcraftPath(path: string): boolean {
  return /\.figcraft$/i.test(path);
}
