import type { CSSProperties } from "react";
import { monogram } from "./router/presets";

/* Channel brand logos: Simple Icons (CC0) for Telegram, Discord and WhatsApp,
   Devicon (MIT) for the multicolor Slack mark; see
   src/assets/channels/README.md. At tile sizes the logo is an app-icon tile
   (white glyph on the brand color, Slack on a neutral tile); below 22 px it is
   the bare glyph in the brand color so it sits inside a text row. Any other
   channel gets a monogram tile. */

const FILES = import.meta.glob("../assets/channels/*.svg", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

interface Brand {
  label: string;
  /* The file name, without the folder and extension. */
  file: string;
  /* The brand color: the tile behind the white glyph, or the bare glyph. */
  color: string;
  /* A multicolor mark keeps its colors and sits on a neutral tile. */
  multicolor?: boolean;
}

const BRANDS: Record<string, Brand> = {
  telegram: { label: "Telegram", file: "telegram", color: "#26A5E4" },
  discord: { label: "Discord", file: "discord", color: "#5865F2" },
  whatsapp: { label: "WhatsApp", file: "whatsapp", color: "#25D366" },
  slack: { label: "Slack", file: "slack-color", color: "#4A154B", multicolor: true },
};

/* The Simple Icons Telegram mark is a circle with the plane cut out. On the
   brand tile only the plane is drawn (the Telegram app icon): the outer circle
   subpath is dropped and the plane's relative move becomes absolute. */
const TELEGRAM_CIRCLE = /d="M11\.944 0A[^z"]*zm4\.962 7\.224/;

/* The vendored files keep their upstream markup; the inline copy drops the
   title (the page names the channel), marks the svg decorative, and fills
   single-color paths with currentColor. */
function inlineSvg(raw: string): string {
  return raw
    .replace(/<title>[\s\S]*?<\/title>/g, "")
    .replace(/\srole="img"/, "")
    .replace(/\sclass="[^"]*"/, "")
    .replace(/\s(width|height)="[^"]*"/g, "")
    .replace("<svg ", '<svg aria-hidden="true" focusable="false" fill="currentColor" ');
}

interface Marks {
  glyph: string;
  tile: string;
}

const MARKS: Record<string, Marks> = {};
for (const [key, brand] of Object.entries(BRANDS)) {
  const raw = FILES["../assets/channels/" + brand.file + ".svg"];
  if (raw) {
    const glyph = inlineSvg(raw);
    const tile =
      key === "telegram" && TELEGRAM_CIRCLE.test(glyph)
        ? glyph.replace(TELEGRAM_CIRCLE, 'd="M16.906 7.224')
        : glyph;
    MARKS[key] = { glyph, tile };
  }
}

function brandKey(channel: string | null | undefined): string {
  return String(channel || "").trim().toLowerCase();
}

/* True for a channel with a brand (a vendored logo or a brand monogram). */
export function isBrandChannel(channel: string | null | undefined): boolean {
  return Boolean(BRANDS[brandKey(channel)]);
}

export interface ChannelLogoProps {
  /* The channel name, e.g. telegram; unknown names get a monogram. */
  channel: string;
  /* The size in pixels. Below 22 the bare glyph is drawn without a tile. */
  size?: number;
  /* The monogram text source for a channel without a logo. */
  label?: string;
  className?: string;
}

export function ChannelLogo({ channel, size = 36, label, className }: ChannelLogoProps) {
  const key = brandKey(channel);
  const brand = BRANDS[key];
  const marks = MARKS[key];
  const small = size < 22;
  const style = {
    "--cl-size": size + "px",
    ...(brand ? { "--cl-brand": brand.color } : {}),
  } as CSSProperties;
  const extra =
    (small ? " cl-sm" : "") +
    (brand && brand.multicolor ? " cl-multi" : "") +
    (className ? " " + className : "");
  if (brand && marks) {
    return (
      <span
        className={"cl" + extra}
        style={style}
        data-channel={key}
        aria-hidden="true"
        dangerouslySetInnerHTML={{ __html: small ? marks.glyph : marks.tile }}
      />
    );
  }
  return (
    <span
      className={"cl cl-mono" + (brand ? " cl-brand-mono" : "") + extra}
      style={style}
      data-channel={key || undefined}
      data-monogram=""
      aria-hidden="true"
    >
      {monogram(label || (brand ? brand.label : channel))}
    </span>
  );
}
