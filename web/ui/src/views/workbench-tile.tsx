import type { CSSProperties } from "react";

import type { WorkbenchSession } from "../api/workbench";
import { agentLogoSvg } from "./agent-logo";
import { monogram, tileFor, tileWidth, type TileSpec } from "./workbench-tiles";

/* Spec 61 REQ-WP-030..REQ-WP-033: the program and agent tiles. One tile is a
   landscape face (a little wider than tall, a top sheen and a soft shadow)
   with the program's mark centred on it. The table and its values come from
   workbench-tiles.ts, so both clients match. */

/* The shell's ">_" mark, drawn in the shell tile's green (REQ-WP-032). */
const PROMPT_SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="m6 8 4 4-4 4"/><path d="M12.5 16h5.5"/></svg>';

const TAB_SIZE = 16;
const PANE_SIZE = 18;
/* How far each tile behind peeks to the right, and how much smaller it is at
   the top and bottom per step (REQ-WP-031). */
const TILE_STEP = 4;

function tileHeight(size: "tab" | "pane"): number {
  return size === "tab" ? TAB_SIZE : PANE_SIZE;
}

/* The program a session runs: the agent the core detected, else its program. */
export function programKey(session?: WorkbenchSession): string {
  const agent = (session?.agent || "").trim();
  if (agent) return agent;
  return session?.program || session?.command?.[0] || "";
}

function TileGlyph({ spec, height, name }: { spec: TileSpec; height: number; name: string }) {
  if (spec.glyph === "prompt") {
    const d = height * 0.62;
    return <span className="wb-ptile-glyph" style={{ width: d, height: d }} dangerouslySetInnerHTML={{ __html: PROMPT_SVG }} />;
  }
  if (spec.glyph === "text") {
    return <span className="wb-ptile-text" style={{ fontSize: height * 0.5 }}>{spec.text ?? monogram(name)}</span>;
  }
  const svg = spec.logo ? agentLogoSvg(spec.logo) : undefined;
  const d = height * 0.66;
  if (!svg) {
    return <span className="wb-ptile-text" style={{ fontSize: height * 0.5 }}>{monogram(name)}</span>;
  }
  /* A mono logo draws in the tile's colour; a colour logo keeps its own. The
     wrapper's `color` (spec.fg) is what an svg's currentColor resolves to. */
  return <span className="wb-ptile-glyph" style={{ width: d, height: d }} dangerouslySetInnerHTML={{ __html: svg }} />;
}

export interface ProgramTileProps {
  session?: WorkbenchSession;
  /* A program name, when there is no session (a test). */
  program?: string;
  size?: "tab" | "pane";
  /* A tile drawn behind another: no glyph, inset from the top and bottom. */
  back?: boolean;
  inset?: number;
  className?: string;
  style?: CSSProperties;
  title?: string;
}

export function ProgramTile({ session, program, size = "pane", back = false, inset = 0, className, style, title }: ProgramTileProps) {
  const name = program ?? programKey(session);
  const spec = tileFor(name);
  const height = tileHeight(size);
  const face: CSSProperties = {
    width: tileWidth(height),
    height: height - 2 * inset,
    borderRadius: height * 0.28,
    background: spec.bg,
    boxShadow: "0 0.5px 1.5px rgba(0, 0, 0, 0.18)",
    color: spec.fg,
    ...(spec.border ? { border: "1px solid " + spec.border } : {}),
    ...style,
  };
  return (
    <span
      className={"wb-ptile" + (className ? " " + className : "")}
      style={face}
      title={title}
      aria-hidden={back ? true : undefined}
    >
      <span className="wb-ptile-sheen" aria-hidden="true" />
      {back ? null : <TileGlyph spec={spec} height={height} name={name} />}
      {!back && spec.accent ? <i className="wb-ptile-dot" style={{ background: spec.accent }} /> : null}
    </span>
  );
}

/* A tab's stacked tiles: the focused pane's tile in front with its glyph, and
   up to two other panes' tiles peeking out behind it, each further right and
   smaller (REQ-WP-031). */
export function TabTiles({
  ids,
  focus,
  sessions,
  size = "tab",
}: {
  ids: string[];
  focus?: string;
  sessions: Record<string, WorkbenchSession>;
  size?: "tab" | "pane";
}) {
  if (ids.length === 0) return null;
  const front = focus && ids.includes(focus) ? focus : ids[0]!;
  const back = ids.filter((id) => id !== front).slice(0, 2);
  const height = tileHeight(size);
  const width = tileWidth(height) + TILE_STEP * back.length + 2;
  return (
    <span className="wb-ttiles" style={{ width, height }}>
      {back.map((id, i) => (
        <ProgramTile
          key={id}
          session={sessions[id]}
          size={size}
          back
          inset={0.8 * (i + 1)}
          style={{ position: "absolute", left: 1 + TILE_STEP * (i + 1), top: 0.8 * (i + 1), zIndex: back.length - i }}
        />
      ))}
      <ProgramTile session={sessions[front]} size={size} style={{ position: "absolute", left: 1, top: 0, zIndex: back.length + 1 }} />
    </span>
  );
}
