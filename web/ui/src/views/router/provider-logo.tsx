import type { CSSProperties } from "react";
import { familyLabel, logoFor, monogram } from "./presets";
import type { ProviderShape } from "./presets";

/* Provider brand logos from the LobeHub icon set (MIT, see
   src/assets/providers/README.md). Monochrome logos fill with currentColor so
   they follow the theme; the -color files keep their brand colors. Any other
   provider gets a two-letter monogram tile. */

const FILES = import.meta.glob("../../assets/providers/*.svg", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

/* Logo key → file name, without the folder and extension. */
const LOGO_FILES: Record<string, string> = {
  openai: "openai",
  anthropic: "anthropic",
  claude: "claude-color",
  deepseek: "deepseek-color",
  openrouter: "openrouter",
  gemini: "gemini-color",
  grok: "grok",
  xai: "xai",
  groq: "groq",
  mistral: "mistral-color",
  together: "together-color",
  fireworks: "fireworks-color",
  cerebras: "cerebras-color",
};

/* The vendored files keep their upstream markup; the inline copy drops the
   title (the card names the provider), the fixed 1em size, and the style. */
function inlineSvg(raw: string): string {
  return raw
    .replace(/<title>[\s\S]*?<\/title>/g, "")
    .replace(/\s(width|height)="1em"/g, "")
    .replace(/\sstyle="[^"]*"/, "")
    .replace("<svg ", '<svg aria-hidden="true" focusable="false" ');
}

const LOGOS: Record<string, string> = {};
for (const [key, file] of Object.entries(LOGO_FILES)) {
  const raw = FILES["../../assets/providers/" + file + ".svg"];
  if (raw) {
    LOGOS[key] = inlineSvg(raw);
  }
}

export function hasLogo(logo: string | undefined | null): boolean {
  return Boolean(logo && LOGOS[logo]);
}

const MONOGRAM_TONES = ["violet", "blue", "teal", "green", "amber", "rose", "orange"];

function toneFor(name: string): string {
  let hash = 0;
  for (const char of String(name || "")) {
    hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
  }
  return MONOGRAM_TONES[hash % MONOGRAM_TONES.length] || "violet";
}

export interface ProviderLogoProps {
  /* A key of the logo registry; empty or unknown falls back to a monogram. */
  logo?: string | null;
  /* The provider's display name, used for the monogram. */
  name: string;
  /* The tile size in pixels. */
  size?: number;
  className?: string;
}

export function ProviderLogo({ logo, name, size = 36, className }: ProviderLogoProps) {
  const style = { "--pl-size": size + "px" } as CSSProperties;
  const svg = logo ? LOGOS[logo] : undefined;
  const extra = (size < 22 ? " pl-sm" : "") + (className ? " " + className : "");
  if (svg) {
    return (
      <span
        className={"pl" + extra}
        style={style}
        data-logo={logo || undefined}
        aria-hidden="true"
        dangerouslySetInnerHTML={{ __html: svg }}
      />
    );
  }
  return (
    <span
      className={"pl pl-mono" + extra}
      style={style}
      data-tone={toneFor(name)}
      data-monogram=""
      aria-hidden="true"
    >
      {monogram(name)}
    </span>
  );
}

/* The logo of a provider entry (or of a bare provider name such as
   claude-agent), at a size that sits inside a text row. */
export function ProviderMark({
  provider,
  name,
  size = 18,
  className,
}: {
  provider?: ProviderShape | null;
  name?: string;
  size?: number;
  className?: string;
}) {
  return (
    <ProviderLogo
      logo={logoFor(provider, name)}
      name={familyLabel(provider, name)}
      size={size}
      className={className}
    />
  );
}

/* A name → entry index for pages that know only a provider name. */
export function providerIndex<T extends ProviderShape>(providers: T[]): Record<string, T> {
  const index: Record<string, T> = {};
  for (const provider of providers) {
    index[provider.name] = provider;
  }
  return index;
}
