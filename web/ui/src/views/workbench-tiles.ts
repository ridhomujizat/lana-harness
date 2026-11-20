/* Spec 61 REQ-WP-030..REQ-WP-033: the program tile table, shared with the
   native Workbench app (apps/desktop/internal/app/logos.go, format.go). The
   values match that table exactly so both clients show the same tiles.

   Pure: no DOM, React or asset imports, so it can be unit tested on its own. */

/* How a tile draws its mark. */
export type TileGlyph = "mono" | "color" | "prompt" | "text";

export interface TileSpec {
  /* The tile's background. */
  bg: string;
  /* The mark's colour. */
  fg: string;
  /* A 1 px border, when present. */
  border?: string;
  glyph: TileGlyph;
  /* A logo file in src/assets/agents, without folder and extension. */
  logo?: string;
  /* The text of a glyphText mark (pi, a monogram). */
  text?: string;
  /* A small dot at the tile's bottom right (pi and omp). */
  accent?: string;
}

/* A plain shell at its prompt: a dark tile with a faint light border and a
   green ">_" prompt glyph (REQ-WP-032). */
export const SHELL_TILE: TileSpec = {
  bg: "#2E2D2B",
  fg: "#5CBA86",
  border: "rgba(255, 255, 255, 0.08)",
  glyph: "prompt",
};

/* The program tile table (REQ-WP-032). A name that is not here is a plain
   shell or, failing that, a monogram. */
export const PROGRAM_TILES: Record<string, TileSpec> = {
  claude: { bg: "#D97757", fg: "#FFFFFF", glyph: "mono", logo: "claude" },
  codex: { bg: "#F7F7F7", fg: "#1A1A1A", border: "rgba(26, 26, 26, 0.16)", glyph: "mono", logo: "codex" },
  cursor: { bg: "#1A1A1A", fg: "#FFFFFF", glyph: "mono", logo: "cursor" },
  grok: { bg: "#1A1A1A", fg: "#FFFFFF", glyph: "mono", logo: "grok" },
  opencode: { bg: "#1A1A1A", fg: "#FFFFFF", glyph: "mono", logo: "opencode" },
  cline: { bg: "#1A1A1A", fg: "#FFFFFF", glyph: "mono", logo: "cline" },
  mastracode: { bg: "#1A1A1A", fg: "#FFFFFF", glyph: "mono", logo: "mastra" },
  hermes: { bg: "#1A1A1A", fg: "#FFFFFF", glyph: "mono", logo: "hermesagent" },
  copilot: { bg: "#24292F", fg: "#FFFFFF", glyph: "mono", logo: "githubcopilot" },
  gemini: { bg: "#2E2D2B", fg: "#FFFFFF", glyph: "color", logo: "geminicli-color" },
  agy: { bg: "#2E2D2B", fg: "#FFFFFF", glyph: "color", logo: "antigravity-color" },
  devin: { bg: "#2E2D2B", fg: "#FFFFFF", glyph: "color", logo: "devin-color" },
  kimi: { bg: "#2E2D2B", fg: "#FFFFFF", glyph: "color", logo: "kimi-color" },
  kiro: { bg: "#2E2D2B", fg: "#FFFFFF", glyph: "color", logo: "kiro-color" },
  amp: { bg: "#2E2D2B", fg: "#FFFFFF", glyph: "color", logo: "amp-color" },
  qwen: { bg: "#2E2D2B", fg: "#FFFFFF", glyph: "color", logo: "qwen-color" },
  qodercli: { bg: "#2E2D2B", fg: "#FFFFFF", glyph: "color", logo: "qoder-color" },
  muse: { bg: "#2E2D2B", fg: "#FFFFFF", glyph: "color", logo: "muse-color" },
  kilo: { bg: "#2E2D2B", fg: "#FFFFFF", glyph: "mono", logo: "kilocode" },
  pi: { bg: "#1A1A1A", fg: "#FFFFFF", glyph: "text", text: "π" },
  omp: { bg: "#1A1A1A", fg: "#FFFFFF", glyph: "text", text: "π", accent: "#7652C2" },
};

/* The programs the app treats as a plain shell at its prompt (REQ-WP-032). */
export const SHELLS = new Set([
  "zsh", "bash", "fish", "sh", "dash", "nu", "pwsh", "elvish", "xonsh", "tcsh", "csh", "ksh", "cmd", "powershell",
]);

/* The palette a monogram tile's colour is hashed from, the Lana agent avatar
   colours (the same list the daemon's hashColor uses). */
export const TILE_COLORS = ["#B21FB8", "#3B6FD8", "#7652C2", "#2F9E5B", "#D9824B", "#3E9C8F"];

/* A program or command as it shows on a tile: the base name, without a leading
   dash. */
export function commandBase(program: string): string {
  const trimmed = program.trim();
  if (!trimmed) return "";
  const base = trimmed.split("/").pop() || trimmed;
  return base.startsWith("-") ? base.slice(1) : base;
}

/* A program's avatar colour, the same every time. */
export function hashColor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return TILE_COLORS[h % TILE_COLORS.length] ?? "#7652C2";
}

/* The first letter a program without a logo shows. */
export function monogram(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return "?";
  return (trimmed[0] ?? "?").toUpperCase();
}

/* The tile of a program's name: its row in the table, else a shell's, else a
   monogram on a colour hashed from the name (REQ-WP-032). */
export function tileFor(program: string): TileSpec {
  const name = commandBase(program).toLowerCase();
  const spec = PROGRAM_TILES[name];
  if (spec) return spec;
  if (name === "" || SHELLS.has(name)) return SHELL_TILE;
  return { bg: hashColor(name), fg: "#FFFFFF", glyph: "text", text: monogram(name) };
}

/* The landscape width of a program tile of the given height: a little wider
   than it is tall (REQ-WP-030). */
export function tileWidth(size: number): number {
  return Math.round(size * 1.32);
}
