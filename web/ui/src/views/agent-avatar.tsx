import { useState } from "react";
import type { CSSProperties, HTMLAttributes, ReactNode } from "react";
import { agentAvatarSvgUrl } from "../api/agents";
import { avatarFor } from "../lib/avatar";
import { agentCssColor, isAvatarDoc, isWorkingMood } from "../lib/pixel-avatar";
import type { AvatarDoc, AvatarMood } from "../lib/pixel-avatar";
import { useAgentAvatar } from "../lib/avatar-store";
import type { StoredAvatar } from "../lib/avatar-store";
import { Avatar, AVATAR_PX } from "../ui/gr8r";
import type { AvatarSize } from "../ui/gr8r";
import { cn } from "../ui/gr8r";
import { PixelBubble, PixelSprite } from "./pixel-sprite";
import type { SpriteView } from "./pixel-sprite";

/* The agent avatar (spec 43, pixel art since spec 62). Every agent and team
   has a pixel character (a saved one or the default seeded from its name).
   The avatar comes from, in order:
     1. the `avatar` prop (the avatar dialog preview);
     2. the shared avatar store, fed by every agents/rooms/overview response;
     3. GET /v1/agents/{name}/avatar.svg as an <img>, for names the store has
        not seen (history, workflows, calendar);
     4. the letter, only when the image fails too (a deleted agent).
   Small avatars show the bust in a tile tinted with the agent color; the
   "full" view shows the whole character. Only a working mood animates
   (REQ-PXA-011). Gr8r sizes (sm, md, lg, xl) render the Gr8r `.av`; the
   pixel sizes of the older views render the `.agent-avatar` markup. */

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
  /* An explicit avatar (the avatar dialog preview); omitted reads the store. */
  avatar?: AvatarDoc | null;
  /* The agent color (token or hex) for the tile when `avatar` is passed. */
  bodyColor?: string;
  /* The motion mood; only the working moods animate. */
  mood?: AvatarMood;
  /* "bust" (default) or "full" (the whole character, profile headers). */
  view?: SpriteView;
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

/* The URL of the server-rendered avatar (frame 0 of the sprite). */
export function agentAvatarUrl(name: string, view: SpriteView = "bust"): string {
  const dark =
    typeof document !== "undefined" &&
    document.documentElement.getAttribute("data-theme") === "dark";
  return agentAvatarSvgUrl(name, dark ? "dark" : "light", view);
}

/* The image fallback with its failure state. A failed URL falls back to the
   letter; a new name or view tries again. */
export function useAvatarImage(
  name: string,
  view: SpriteView = "bust"
): {
  src: string;
  failed: boolean;
  onError: () => void;
} {
  const src = name ? agentAvatarUrl(name, view) : "";
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

export interface ResolvedAvatar {
  avatar: AvatarDoc;
  /* The CSS color of the tile. */
  color: string;
}

/* The avatar to show and its tile color, or null when there is none. */
export function resolveAvatar(
  stored: StoredAvatar | undefined,
  avatar: AvatarDoc | null | undefined,
  bodyColor: string | undefined,
  color: string | undefined
): ResolvedAvatar | null {
  const fallback = color || "var(--violet)";
  if (avatar !== undefined) {
    if (!isAvatarDoc(avatar)) {
      return null;
    }
    return { avatar, color: bodyColor ? agentCssColor(bodyColor, fallback) : fallback };
  }
  if (!stored || !isAvatarDoc(stored.avatar)) {
    return null;
  }
  return {
    avatar: stored.avatar,
    color: stored.color ? agentCssColor(stored.color, fallback) : fallback,
  };
}

/* The sprite, plus the thought bubble on a large working avatar. */
function SpriteFace({
  avatar,
  view,
  animate,
  px,
}: {
  avatar: AvatarDoc;
  view: SpriteView;
  animate: boolean;
  px: number | undefined;
}) {
  return (
    <>
      <PixelSprite sprite={avatar.sprite} view={view} animate={animate} />
      {animate && (px || 0) >= 44 ? <PixelBubble /> : null}
    </>
  );
}

export function AgentAvatar({
  name,
  color,
  size,
  presence,
  avatar,
  bodyColor,
  mood,
  view = "bust",
  className,
  style,
  ...rest
}: AgentAvatarProps) {
  const stored = useAgentAvatar(name);
  const shape = resolveAvatar(stored, avatar, bodyColor, color);
  /* An explicit avatar prop never reaches for the image: the dialog shows the
     letter until something is generated. */
  const image = useAvatarImage(shape || avatar !== undefined ? "" : name, view);
  const letterColor = color || agentFallbackColor(name);
  const animate = Boolean(shape) && isWorkingMood(mood);
  const pixelClass = cn("has-pixel", view === "full" && "is-full", animate && "is-working");

  if (isLegacySize(size)) {
    const sizeClass = size === "auto" ? "" : "avatar-" + size;
    const px = size === "auto" ? undefined : Number(size);
    let inner: ReactNode;
    let look: CSSProperties | undefined = style;
    let tone = "";
    if (shape) {
      inner = <SpriteFace avatar={shape.avatar} view={view} animate={animate} px={px} />;
      look = { ...style, "--pxc": shape.color } as CSSProperties;
    } else if (!image.failed) {
      inner = <AvatarImage src={image.src} onError={image.onError} />;
      look = { ...style, "--pxc": letterColor } as CSSProperties;
    } else {
      const letter = avatarFor(name);
      inner = letter.initial;
      tone = letter.colorClass;
    }
    return (
      <span
        className={cn("agent-avatar", sizeClass, tone || pixelClass, className)}
        aria-hidden="true"
        style={look}
        {...rest}
      >
        {inner}
      </span>
    );
  }

  const px = AVATAR_PX[size || "default"];
  if (shape) {
    return (
      <Avatar
        name={name}
        color={shape.color}
        size={size}
        presence={presence}
        className={cn(pixelClass, className)}
        style={{ ...style, "--pxc": shape.color } as CSSProperties}
        {...rest}
      >
        <SpriteFace avatar={shape.avatar} view={view} animate={animate} px={px} />
      </Avatar>
    );
  }
  return (
    <Avatar
      name={name}
      color={letterColor}
      size={size}
      presence={presence}
      className={cn(!image.failed && pixelClass, className)}
      style={!image.failed ? ({ ...style, "--pxc": letterColor } as CSSProperties) : style}
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
