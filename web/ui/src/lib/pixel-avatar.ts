/* Pixel-art agent avatars (spec 62). An avatar document carries a sprite: a
   palette of one-character keys and two 16x20 layers (body, then head over
   it). This module checks documents from the server, composes the four
   animation frames, adds the outline, and turns a frame into run-length rects.
   It never renders markup from strings: callers draw the rects with React. */

/* The motion mood of an avatar host. Only the working moods animate
   (REQ-PXA-011); the rest show frame 0. */
export type AvatarMood =
  | "rest"
  | "idle"
  | "thinking"
  | "tool"
  | "talk"
  | "ask"
  | "error"
  | "done"
  | "sleep";

export const WORKING_MOODS: readonly AvatarMood[] = ["thinking", "tool", "talk"];

export function isWorkingMood(mood: AvatarMood | undefined | null): boolean {
  return Boolean(mood) && WORKING_MOODS.includes(mood as AvatarMood);
}

export interface Sprite {
  palette: Record<string, string>;
  head: string[];
  body: string[];
}

export interface AvatarParts {
  species: string;
  hair: string;
  hair_color: string;
  skin: string;
  eyes: string;
  accessory: string;
  outfit: string;
  pants: string;
  blush: boolean;
  color: string;
  accessory_color: string;
}

export interface AvatarDoc {
  kind: "parts" | "drawn";
  parts?: AvatarParts;
  sprite: Sprite;
  prompt?: string;
}

export const GRID_W = 16;
export const GRID_H = 20;
export const OUTLINE = "#1b1820";

const KEY = /^[a-zA-Z0-9]$/;
const HEX = /^#[0-9a-fA-F]{6}$/;

function isLayer(rows: unknown, palette: Record<string, string>): rows is string[] {
  if (!Array.isArray(rows) || rows.length !== GRID_H) {
    return false;
  }
  for (const row of rows) {
    if (typeof row !== "string" || row.length !== GRID_W) {
      return false;
    }
    for (const char of row) {
      if (char !== "." && !Object.prototype.hasOwnProperty.call(palette, char)) {
        return false;
      }
    }
  }
  return true;
}

/* isSprite checks the shape the renderer relies on: palette keys and hex
   values, 20 rows of 16 cells per layer, cells from the palette. The server
   enforces the template rules (REQ-PXA-003). */
export function isSprite(value: unknown): value is Sprite {
  if (!value || typeof value !== "object") {
    return false;
  }
  const sprite = value as Partial<Sprite>;
  const palette = sprite.palette;
  if (!palette || typeof palette !== "object" || Array.isArray(palette)) {
    return false;
  }
  const keys = Object.keys(palette);
  if (keys.length === 0 || keys.length > 24) {
    return false;
  }
  for (const key of keys) {
    const color = (palette as Record<string, unknown>)[key];
    if (!KEY.test(key) || typeof color !== "string" || !HEX.test(color)) {
      return false;
    }
  }
  return isLayer(sprite.head, palette) && isLayer(sprite.body, palette);
}

export function isAvatarDoc(value: unknown): value is AvatarDoc {
  if (!value || typeof value !== "object") {
    return false;
  }
  const doc = value as Partial<AvatarDoc>;
  return (doc.kind === "parts" || doc.kind === "drawn") && isSprite(doc.sprite);
}

/* ------------------------------------------------------------------ */
/* Frames                                                             */
/* ------------------------------------------------------------------ */

/* The working cycle: frame index per 180 ms step (spec 62 Definitions). */
export const FRAME_PATTERN = [0, 1, 0, 3, 0, 1, 2, 3] as const;
export const FRAME_MS = 180;

interface FrameSpec {
  head: number;
  leftHand: number;
  rightHand: number;
  blink: boolean;
}

const REST: FrameSpec = { head: 0, leftHand: 0, rightHand: 0, blink: false };

const FRAMES: readonly FrameSpec[] = [
  REST,
  { head: 1, leftHand: -1, rightHand: 0, blink: false },
  { head: 0, leftHand: 0, rightHand: 0, blink: true },
  { head: 1, leftHand: 0, rightHand: -1, blink: false },
];

type Cells = Map<string, string>;

const at = (x: number, y: number) => x + "," + y;

function cellOf(rows: string[], x: number, y: number): string {
  const row = rows[y];
  if (!row || x < 0 || x >= GRID_W) {
    return ".";
  }
  return row[x] || ".";
}

/* composeFrame draws a frame into a cell map of colors: the body layer with
   the hands moved, the head layer over it (shifted down on a nod, eyes closed
   on a blink), then the outline on every empty cell next to a filled one. */
export function composeFrame(sprite: Sprite, frame: number): Cells {
  const spec: FrameSpec = FRAMES[frame] ?? REST;
  const cells: Cells = new Map();
  const palette = sprite.palette;
  for (let y = 0; y < GRID_H; y++) {
    for (let x = 0; x < GRID_W; x++) {
      const key = cellOf(sprite.body, x, y);
      if (key === ".") {
        continue;
      }
      let dy = 0;
      if (y === 14 && x === 4) {
        dy = spec.leftHand;
      } else if (y === 14 && x === 11) {
        dy = spec.rightHand;
      }
      const color = palette[key];
      if (color) {
        cells.set(at(x, y + dy), color);
      }
    }
  }
  for (let y = 0; y < GRID_H; y++) {
    for (let x = 0; x < GRID_W; x++) {
      let key = cellOf(sprite.head, x, y);
      if (key === ".") {
        continue;
      }
      if (spec.blink && key === "e" && cellOf(sprite.head, x, y + 1) === "e") {
        /* The top cell of an open eye takes the color to its left (the skin,
           or a robot's visor). */
        const left = cellOf(sprite.head, x - 1, y);
        key = left !== "." && left !== "e" ? left : "s";
      }
      const color = palette[key];
      if (color) {
        cells.set(at(x, y + spec.head), color);
      }
    }
  }
  const filled = new Set(cells.keys());
  for (let y = -1; y <= GRID_H - 1; y++) {
    for (let x = -1; x <= GRID_W; x++) {
      const key = at(x, y);
      if (filled.has(key)) {
        continue;
      }
      if (
        filled.has(at(x + 1, y)) ||
        filled.has(at(x - 1, y)) ||
        filled.has(at(x, y + 1)) ||
        filled.has(at(x, y - 1))
      ) {
        cells.set(key, OUTLINE);
      }
    }
  }
  return cells;
}

export interface PixelRun {
  x: number;
  y: number;
  w: number;
  fill: string;
}

/* runsOf merges horizontal neighbours of one color into one rect. */
export function runsOf(cells: Cells): PixelRun[] {
  const rows = new Map<number, Array<[number, string]>>();
  for (const [key, fill] of cells) {
    const comma = key.indexOf(",");
    const x = Number(key.slice(0, comma));
    const y = Number(key.slice(comma + 1));
    let row = rows.get(y);
    if (!row) {
      row = [];
      rows.set(y, row);
    }
    row.push([x, fill]);
  }
  const runs: PixelRun[] = [];
  for (const y of [...rows.keys()].sort((a, b) => a - b)) {
    const row = rows.get(y) || [];
    row.sort((a, b) => a[0] - b[0]);
    let i = 0;
    while (i < row.length) {
      const [startX, fill] = row[i]!;
      let j = i;
      let lastX = startX;
      for (;;) {
        const next = row[j + 1];
        if (!next || next[0] !== lastX + 1 || next[1] !== fill) {
          break;
        }
        j++;
        lastX = next[0];
      }
      runs.push({ x: startX, y, w: j - i + 1, fill });
      i = j + 1;
    }
  }
  return runs;
}

/* A small cache: the same sprite shows in many rows of a page. */
const frameCache = new Map<string, PixelRun[]>();
const CACHE_LIMIT = 400;

export function spriteKey(sprite: Sprite): string {
  return JSON.stringify([sprite.palette, sprite.head, sprite.body]);
}

export function frameRuns(sprite: Sprite, frame: number, key = spriteKey(sprite)): PixelRun[] {
  const cacheKey = key + "#" + frame;
  const hit = frameCache.get(cacheKey);
  if (hit) {
    return hit;
  }
  const runs = runsOf(composeFrame(sprite, frame));
  if (frameCache.size >= CACHE_LIMIT) {
    const oldest = frameCache.keys().next().value;
    if (oldest !== undefined) {
      frameCache.delete(oldest);
    }
  }
  frameCache.set(cacheKey, runs);
  return runs;
}

/* ------------------------------------------------------------------ */
/* Agent colors (spec 43 REQ-AVT-010, kept)                           */
/* ------------------------------------------------------------------ */

/* The six color tokens of an agent and the Gr8r variables they name. */
export const AGENT_COLOR_TOKENS: Record<string, string> = {
  brand: "var(--violet)",
  success: "var(--green)",
  warning: "var(--amber)",
  error: "var(--red)",
  blue: "var(--blue)",
  gray: "var(--gray)",
};

const AGENT_HEX = /^#([0-9a-f]{6}|[0-9a-f]{3})$/i;

export function isHexColor(value: unknown): value is string {
  return typeof value === "string" && AGENT_HEX.test(value.trim());
}

export function isColorToken(value: unknown): value is string {
  return typeof value === "string" && Object.prototype.hasOwnProperty.call(AGENT_COLOR_TOKENS, value);
}

/* The CSS color of an agent color: a token maps onto its Gr8r variable, a hex
   passes through, anything else uses the fallback. */
export function agentCssColor(color: string | null | undefined, fallback = "var(--violet)"): string {
  const text = String(color || "").trim();
  if (isHexColor(text)) {
    return text.toLowerCase();
  }
  if (isColorToken(text)) {
    return AGENT_COLOR_TOKENS[text] || fallback;
  }
  return fallback;
}
