import { useState } from "react";
import type { CSSProperties, HTMLAttributes, ReactNode } from "react";
import { agentAvatarSvgUrl } from "../api/agents";
import { avatarFor } from "../lib/avatar";
import { agentCssColor, parseAvatarSvg } from "../lib/avatar-svg";
import { useAgentAvatar } from "../lib/avatar-store";
import type { StoredAvatar } from "../lib/avatar-store";
import { Avatar, MoodLayer, moodHostProps } from "../ui/gr8r";
import type { AvatarMood } from "../lib/avatar-motion";
import type { AvatarSize } from "../ui/gr8r";
import { cn } from "../ui/gr8r";

/* The agent avatar of spec 43. Every agent and team has a generated blob
   (a saved one or the default seeded from its name). The avatar comes from,
   in order:
     1. the `svg` prop (the avatar dialog preview);
     2. the shared avatar store, fed by every agents/rooms/overview response;
     3. GET /v1/agents/{name}/avatar.svg as an <img>, for names the store has
        not seen (history, workflows, calendar);
     4. the letter, only when the image fails too (a deleted agent).
   Gr8r sizes (sm, md, lg, xl) render the Gr8r `.av`; the pixel sizes of the
   older views render the `.agent-avatar` markup. */

export type LegacyAvatarSize =
  | "20"
  | "22"
  | "24"
  | "26"
  | "28"
  | "36"
  | "56"
  /* The `.agent-avatar` box sized by the surrounding CSS. */
  | "auto";

export interface AgentAvatarProps
  extends Omit<HTMLAttributes<HTMLSpanElement>, "color"> {
  name: string;
  /* The CSS color of the fallback letter, as the call site maps it. */
  color?: string;
  size?: AvatarSize | LegacyAvatarSize;
  presence?: boolean;
  /* An explicit SVG (the avatar dialog preview); omitted reads the store. */
  svg?: string;
  /* The agent color (token or hex) for the SVG body when `svg` is passed. */
  bodyColor?: string;
  /* The motion mood (lib/avatar-motion); only a generated avatar moves. */
  mood?: AvatarMood;
}

const LEGACY_SIZES: readonly string[] = ["20", "22", "24", "26", "28", "36", "56", "auto"];

function isLegacySize(size: string | undefined): size is LegacyAvatarSize {
  return size !== undefined && LEGACY_SIZES.includes(size);
}

/* The fallback letter color of an agent the call site cannot color: a hash of
   the name onto the Gr8r tokens, so one name keeps one color. */
const FALLBACK_TONES: Record<string, string> = {
  "tone-brand": "var(--violet)",
  "tone-success": "var(--green)",
  "tone-warning": "var(--amber)",
  "tone-error": "var(--red)",
  "tone-gray": "var(--gray)",
};

export function agentFallbackColor(name: string | null | undefined): string {
  return FALLBACK_TONES[avatarFor(name).colorClass] || "var(--violet)";
}

/* The URL of the server-rendered avatar (body color baked in for the theme). */
export function agentAvatarUrl(name: string): string {
  const dark =
    typeof document !== "undefined" &&
    document.documentElement.getAttribute("data-theme") === "dark";
  return agentAvatarSvgUrl(name, dark ? "dark" : "light");
}

/* The image fallback with its failure state. A failed URL falls back to the
   letter; a new name or theme tries again. */
export function useAvatarImage(name: string): {
  src: string;
  failed: boolean;
  onError: () => void;
} {
  const src = name ? agentAvatarUrl(name) : "";
  const [failedSrc, setFailedSrc] = useState("");
  return {
    src,
    failed: !src || failedSrc === src,
    onError: () => setFailedSrc(src),
  };
}

export function AvatarImage({ src, onError }: { src: string; onError: () => void }) {
  return (
    <img
      className="av-img"
      src={src}
      alt=""
      aria-hidden="true"
      draggable={false}
      decoding="async"
      onError={onError}
    />
  );
}

interface ResolvedShape {
  svg: string;
  color: string;
}

/* The inline SVG to show and its body color, or null when there is none. */
export function resolveAvatar(
  stored: StoredAvatar | undefined,
  svg: string | undefined,
  bodyColor: string | undefined,
  color: string | undefined
): ResolvedShape | null {
  const fallback = color || "var(--violet)";
  if (svg !== undefined) {
    if (!svg || !parseAvatarSvg(svg)) {
      return null;
    }
    return { svg, color: bodyColor ? agentCssColor(bodyColor, fallback) : fallback };
  }
  if (!stored || !stored.svg || !parseAvatarSvg(stored.svg)) {
    return null;
  }
  return {
    svg: stored.svg,
    color: stored.color ? agentCssColor(stored.color, fallback) : fallback,
  };
}

export function AgentAvatar({
  name,
  color,
  size,
  presence,
  svg,
  bodyColor,
  mood,
  className,
  style,
  ...rest
}: AgentAvatarProps) {
  const stored = useAgentAvatar(name);
  const shape = resolveAvatar(stored, svg, bodyColor, color);
  /* An explicit svg prop never reaches for the image: the dialog shows the
     letter until something is generated. */
  const image = useAvatarImage(shape || svg !== undefined ? "" : name);
  const letterColor = color || agentFallbackColor(name);

  if (isLegacySize(size)) {
    const sizeClass = size === "auto" ? "" : "avatar-" + size;
    let inner: ReactNode;
    let look: CSSProperties | undefined = style;
    let tone = "";
    const px = size === "auto" ? undefined : Number(size);
    const moving = shape ? moodHostProps(mood, px) : {};
    if (shape) {
      inner = (
        <>
          {parseAvatarSvg(shape.svg)}
          <MoodLayer mood={mood} px={px} />
        </>
      );
      look = { ...style, ...moving.style, color: shape.color };
    } else if (!image.failed) {
      inner = <AvatarImage src={image.src} onError={image.onError} />;
    } else {
      const letter = avatarFor(name);
      inner = letter.initial;
      tone = letter.colorClass;
    }
    return (
      <span
        className={cn("agent-avatar", sizeClass, tone || "has-svg", className)}
        aria-hidden="true"
        style={look}
        data-mood={moving["data-mood"]}
        {...rest}
      >
        {inner}
      </span>
    );
  }

  if (shape) {
    return (
      <Avatar
        name={name}
        color={shape.color}
        svg={shape.svg}
        size={size}
        presence={presence}
        mood={mood}
        className={className}
        style={style}
        {...rest}
      />
    );
  }
  return (
    <Avatar
      name={name}
      color={letterColor}
      size={size}
      presence={presence}
      className={cn(!image.failed && "has-svg", className)}
      style={style}
      {...rest}
    >
      {!image.failed ? <AvatarImage src={image.src} onError={image.onError} /> : undefined}
    </Avatar>
  );
}

export interface AgentAvatarStackItem {
  name?: string | null;
  color?: string;
}

export interface AgentAvatarStackProps extends HTMLAttributes<HTMLSpanElement> {
  items: AgentAvatarStackItem[];
  max?: number;
  size?: AvatarSize;
}

/* The Gr8r AvatarStack markup with an AgentAvatar per member. */
export function AgentAvatarStack({
  items,
  max = 4,
  size = "sm",
  className,
  ...rest
}: AgentAvatarStackProps) {
  const shown = items.slice(0, max);
  const extra = items.length - shown.length;
  return (
    <span className={cn("avs", className)} {...rest}>
      {shown.map((item, index) =>
        item.name ? (
          <AgentAvatar key={index} name={String(item.name)} color={item.color} size={size} />
        ) : (
          <Avatar key={index} empty size={size} />
        )
      )}
      {extra > 0 ? (
        <span
          className={cn("av", size, "more")}
          style={{ "--c": "var(--gray)" } as CSSProperties}
        >
          +{extra}
        </span>
      ) : null}
    </span>
  );
}

/* An agent name with its avatar in front, for table cells, detail lists and
   one-line labels. */
export function AgentName({
  name,
  label,
  size = "sm",
  className,
}: {
  /* name is the agent ID: it seeds the avatar. */
  name: string;
  /* label is the display name to show; it falls back to name. */
  label?: string;
  size?: AvatarSize;
  className?: string;
}) {
  return (
    <span className={cn("agent-name-chip", className)}>
      <AgentAvatar name={name} size={size} aria-hidden="true" data-tip={undefined} />
      <span className="trunc">{label || name}</span>
    </span>
  );
}

/* The icon of an agent option in a Gr8r Select or a menu. */
export function agentOptionIcon(name: string): ReactNode {
  return name ? <AgentAvatar name={name} size="sm" aria-hidden="true" /> : undefined;
}
