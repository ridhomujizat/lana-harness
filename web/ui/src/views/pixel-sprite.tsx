import { memo, useMemo } from "react";
import { FRAME_PATTERN, frameRuns, spriteKey } from "../lib/pixel-avatar";
import type { Sprite } from "../lib/pixel-avatar";

/* A pixel-art avatar sprite (spec 62) as SVG rects. The bust view crops the
   head and shoulders (rows -1..14) for avatar tiles; the full view shows the
   whole character on a ground bar. An animated sprite carries all four frames
   and styles/pages-gr8r/pixel-avatar.css shows one at a time with CSS steps
   (REQ-PXA-011); a still one carries frame 0 only. */

export type SpriteView = "bust" | "full";

const VIEW_BOX: Record<SpriteView, string> = {
  bust: "0 -1 16 16",
  full: "-1 -1 18 21",
};

const ALL_FRAMES = [...new Set(FRAME_PATTERN)].sort();

export interface PixelSpriteProps {
  sprite: Sprite;
  view?: SpriteView;
  animate?: boolean;
  className?: string;
}

export const PixelSprite = memo(function PixelSprite({
  sprite,
  view = "bust",
  animate = false,
  className,
}: PixelSpriteProps) {
  const key = useMemo(() => spriteKey(sprite), [sprite]);
  const frames = animate ? ALL_FRAMES : [0];
  return (
    <svg
      className={"px-sprite" + (animate ? " is-animated" : "") + (className ? " " + className : "")}
      viewBox={VIEW_BOX[view]}
      shapeRendering="crispEdges"
      aria-hidden="true"
      focusable="false"
    >
      {view === "full" ? <rect className="px-ground" x={2} y={19} width={12} height={1} /> : null}
      {frames.map((frame) => (
        <g key={frame} className={"px-f px-f" + frame}>
          {frameRuns(sprite, frame, key).map((run) => (
            <rect
              key={run.x + "," + run.y}
              x={run.x}
              y={run.y}
              width={run.w}
              height={1}
              fill={run.fill}
            />
          ))}
        </g>
      ))}
    </svg>
  );
});

/* The pixel thought bubble of a large working avatar: a white box with three
   dots that light up in turn. */
const BUBBLE = [
  ".kkkkkkkkk.",
  "kwwwwwwwwwk",
  "kw1w2w3wwwk",
  "kwwwwwwwwwk",
  ".kkkkkkkkk.",
  "..kwk......",
  "...k.......",
];

export function PixelBubble({ className }: { className?: string }) {
  const cells: { x: number; y: number; kind: string }[] = [];
  BUBBLE.forEach((row, y) => {
    [...row].forEach((kind, x) => {
      if (kind !== ".") {
        cells.push({ x, y, kind });
      }
    });
  });
  return (
    <span className={"px-bubble" + (className ? " " + className : "")} aria-hidden="true">
      <svg viewBox="0 0 11 7" shapeRendering="crispEdges" focusable="false">
        {cells.map(({ x, y, kind }) =>
          kind === "k" ? (
            <rect key={x + "," + y} x={x} y={y} width={1} height={1} fill="#1b1820" />
          ) : kind === "w" ? (
            <rect key={x + "," + y} x={x} y={y} width={1} height={1} fill="#ffffff" />
          ) : (
            <g key={x + "," + y}>
              <rect x={x} y={y} width={1} height={1} fill="#ffffff" />
              <rect className={"px-dot px-dot" + kind} x={x} y={y} width={1} height={1} fill="#1b1820" />
            </g>
          )
        )}
      </svg>
    </span>
  );
}
