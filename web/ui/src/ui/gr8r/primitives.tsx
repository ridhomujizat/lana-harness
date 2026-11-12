import { createContext, useContext, useId } from "react";
import type {
  AnchorHTMLAttributes,
  ButtonHTMLAttributes,
  CSSProperties,
  HTMLAttributes,
  InputHTMLAttributes,
  ReactNode,
  TextareaHTMLAttributes,
} from "react";
import { Ic } from "../icons";
import { cn, initials } from "./util";
import { parseAvatarSvg } from "../../lib/avatar-svg";
import type { AvatarMood } from "../../lib/avatar-motion";

/* ------------------------------------------------------------------ */
/* Button                                                             */
/* ------------------------------------------------------------------ */

export type ButtonVariant =
  | "primary"
  | "secondary"
  | "ghost"
  | "danger"
  | "danger-ghost";
export type ButtonSize = "sm" | "md" | "lg";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  block?: boolean;
  loading?: boolean;
  icon?: string;
  iconRight?: string;
  as?: "button" | "a";
  href?: string;
}

const BUTTON_ICON_SIZE: Record<ButtonSize, number> = { sm: 13, md: 14, lg: 15 };

export function Button({
  variant = "secondary",
  size = "md",
  block,
  loading,
  icon,
  iconRight,
  as = "button",
  href,
  className,
  children,
  disabled,
  type,
  ...rest
}: ButtonProps) {
  const classes = cn(
    "btn",
    "btn-" + variant,
    size === "sm" && "btn-sm",
    size === "lg" && "btn-lg",
    block && "btn-block",
    loading && "is-loading",
    className
  );
  const iconSize = BUTTON_ICON_SIZE[size];
  const inner = (
    <>
      {icon ? <Ic name={icon} size={iconSize} /> : null}
      {children}
      {iconRight ? <Ic name={iconRight} size={iconSize} /> : null}
    </>
  );
  if (as === "a") {
    return (
      <a
        className={classes}
        href={href}
        aria-disabled={disabled ? true : undefined}
        {...(rest as unknown as AnchorHTMLAttributes<HTMLAnchorElement>)}
      >
        {inner}
      </a>
    );
  }
  return (
    <button
      type={type ?? "button"}
      className={classes}
      disabled={disabled}
      {...rest}
    >
      {inner}
    </button>
  );
}

/* ------------------------------------------------------------------ */
/* IconButton                                                         */
/* ------------------------------------------------------------------ */

export type IconButtonSize = "md" | "sm" | "xs";

export interface IconButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement> {
  icon?: string;
  size?: IconButtonSize;
  tip?: string;
  tipPos?: "bottom" | "right" | "top";
  on?: boolean;
  label?: string;
}

const IBUTTON_ICON_SIZE: Record<IconButtonSize, number> = {
  md: 16,
  sm: 15,
  xs: 13,
};

export function IconButton({
  icon,
  size = "md",
  tip,
  tipPos = "bottom",
  on,
  label,
  className,
  children,
  type,
  ...rest
}: IconButtonProps) {
  return (
    <button
      type={type ?? "button"}
      className={cn(
        "ibtn",
        size === "sm" && "ibtn-sm",
        size === "xs" && "ibtn-xs",
        on && "on",
        className
      )}
      data-tip={tip}
      data-tip-pos={tipPos !== "bottom" ? tipPos : undefined}
      aria-label={label ?? tip}
      aria-pressed={on === undefined ? undefined : on}
      {...rest}
    >
      {icon ? <Ic name={icon} size={IBUTTON_ICON_SIZE[size]} /> : children}
    </button>
  );
}

/* ------------------------------------------------------------------ */
/* Badge / Lbl / Chip                                                 */
/* ------------------------------------------------------------------ */

export type BadgeTone =
  | "green"
  | "amber"
  | "red"
  | "accent"
  | "violet"
  | "gray";

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  tone?: BadgeTone;
  dot?: boolean;
}

export function Badge({ tone, dot, className, children, ...rest }: BadgeProps) {
  return (
    <span className={cn("badge", tone, className)} {...rest}>
      {dot ? <span className="dot" /> : null}
      {children}
    </span>
  );
}

export interface LblProps extends HTMLAttributes<HTMLSpanElement> {
  color?: string;
}

export function Lbl({ color, className, style, children, ...rest }: LblProps) {
  return (
    <span
      className={cn("lbl", className)}
      style={{ ...style, "--c": color } as CSSProperties}
      {...rest}
    >
      <i />
      {children}
    </span>
  );
}

export interface ChipProps extends HTMLAttributes<HTMLSpanElement> {
  onRemove?: () => void;
  removeLabel?: string;
}

export function Chip({
  onRemove,
  removeLabel = "Remove",
  className,
  children,
  ...rest
}: ChipProps) {
  return (
    <span className={cn("chip", className)} {...rest}>
      {children}
      {onRemove ? (
        <button
          type="button"
          className="ibtn"
          aria-label={removeLabel}
          onClick={onRemove}
        >
          <Ic name="x" size={12} />
        </button>
      ) : null}
    </span>
  );
}

/* ------------------------------------------------------------------ */
/* Avatar                                                             */
/* ------------------------------------------------------------------ */

export type AvatarSize = "sm" | "md" | "lg" | "xl";

/* The pixel side of each Gr8r avatar size (components.css .av). */
export const AVATAR_PX: Record<AvatarSize | "default", number> = {
  default: 22,
  sm: 20,
  md: 26,
  lg: 36,
  xl: 64,
};

const SPARK_ANGLES = [0, 72, 144, 216, 288];

/* The extra marks of an avatar mood (styles/pages-gr8r/avatar-motion.css):
   the tool ring, the waiting halo, the error badge, the done sparks, and on
   avatars of 44 px and up the thought bubble and the sleeping z's. The host
   carries data-mood and --avpx (see moodHostProps). */
export function MoodLayer({ mood, px }: { mood?: AvatarMood; px?: number }) {
  const big = (px || 0) >= 44;
  switch (mood) {
    case "tool":
      return (
        <span className="av-ring" aria-hidden="true">
          <svg viewBox="0 0 100 100">
            <circle cx="50" cy="50" r="46" />
          </svg>
        </span>
      );
    case "ask":
      return <span className="av-halo" aria-hidden="true" />;
    case "error":
      return <span className="av-badge" aria-hidden="true" />;
    case "done":
      return (
        <span className="av-spark" aria-hidden="true">
          {SPARK_ANGLES.map((angle) => (
            <span key={angle} style={{ "--a": angle + "deg" } as CSSProperties}>
              <i />
            </span>
          ))}
        </span>
      );
    case "thinking":
      return big ? (
        <span className="av-bubble" aria-hidden="true">
          <i />
          <i />
          <i />
        </span>
      ) : null;
    case "sleep":
      return big ? (
        <span className="av-zzz" aria-hidden="true">
          <i>z</i>
          <i>z</i>
          <i>z</i>
        </span>
      ) : null;
    default:
      return null;
  }
}

/* The host attributes of a moving avatar: its mood and its pixel size for
   the mood marks. A mood of undefined leaves the avatar still apart from the
   gaze, blinks and hover of lib/avatar-motion. */
export function moodHostProps(
  mood: AvatarMood | undefined,
  px: number | undefined
): { "data-mood"?: AvatarMood; style?: CSSProperties } {
  if (!mood) {
    return {};
  }
  return {
    "data-mood": mood,
    style: px ? ({ "--avpx": px + "px" } as CSSProperties) : undefined,
  };
}

export interface AvatarProps extends HTMLAttributes<HTMLSpanElement> {
  name?: string | null;
  color?: string;
  size?: AvatarSize;
  presence?: boolean;
  empty?: boolean;
  /* Spec 43: a sanitized generated avatar. It replaces the letter when it
     passes the allowlist of lib/avatar-svg; the body takes `color`. */
  svg?: string;
  /* The motion mood of a generated avatar (lib/avatar-motion). */
  mood?: AvatarMood;
}

export function Avatar({
  name,
  color = "var(--gray)",
  size,
  presence,
  empty,
  svg,
  mood,
  className,
  style,
  children,
  ...rest
}: AvatarProps) {
  const label = (name ?? "").trim();
  const shape = svg && !children ? parseAvatarSvg(svg) : null;
  if (empty || !label) {
    return (
      <span
        className={cn("av", "empty", size, className)}
        data-tip="Unassigned"
        {...rest}
      >
        {children ?? <Ic name="user" size={11} />}
      </span>
    );
  }
  const px = AVATAR_PX[size || "default"];
  const moving = shape ? moodHostProps(mood, px) : {};
  return (
    <span
      className={cn("av", shape && "has-svg", size, presence && "presence", className)}
      style={{ ...style, ...moving.style, "--c": color } as CSSProperties}
      data-tip={label}
      aria-label={label}
      data-mood={moving["data-mood"]}
      {...rest}
    >
      {children ?? shape ?? initials(label)}
      {shape ? <MoodLayer mood={mood} px={px} /> : null}
    </span>
  );
}

export interface AvatarStackProps extends HTMLAttributes<HTMLSpanElement> {
  items: Array<{ name?: string | null; color?: string; svg?: string }>;
  max?: number;
  size?: AvatarSize;
}

export function AvatarStack({
  items,
  max = 4,
  size = "sm",
  className,
  ...rest
}: AvatarStackProps) {
  const shown = items.slice(0, max);
  const extra = items.length - shown.length;
  return (
    <span className={cn("avs", className)} {...rest}>
      {shown.map((item, index) => (
        <Avatar
          key={index}
          name={item.name}
          color={item.color}
          svg={item.svg}
          size={size}
        />
      ))}
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

/* ------------------------------------------------------------------ */
/* Seg / Tabs                                                         */
/* ------------------------------------------------------------------ */

export interface SegOption {
  value: string;
  label?: ReactNode;
  /* A Lucide icon name, or a node such as a brand logo. */
  icon?: ReactNode;
  count?: number;
  disabled?: boolean;
  title?: string;
}

export interface SegProps
  extends Omit<HTMLAttributes<HTMLDivElement>, "onChange"> {
  options: SegOption[];
  value: string;
  onChange: (value: string) => void;
}

export function Seg({ options, value, onChange, className, ...rest }: SegProps) {
  return (
    <div className={cn("seg", className)} role="tablist" {...rest}>
      {options.map((option) => {
        const on = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            role="tab"
            aria-selected={on}
            className={on ? "on" : undefined}
            disabled={option.disabled}
            title={option.title}
            onClick={() => onChange(option.value)}
          >
            {typeof option.icon === "string" ? (
              <Ic name={option.icon} size={13} />
            ) : (
              option.icon || null
            )}
            {option.label}
            {option.count !== undefined ? (
              <span className="cnt">{option.count}</span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

export interface TabItem {
  value: string;
  label: ReactNode;
  count?: number;
  icon?: string;
}

export interface TabsProps
  extends Omit<HTMLAttributes<HTMLDivElement>, "onChange"> {
  items: TabItem[];
  value: string;
  onChange: (value: string) => void;
}

export function Tabs({ items, value, onChange, className, ...rest }: TabsProps) {
  return (
    <div className={cn("tabs", className)} role="tablist" {...rest}>
      {items.map((item) => {
        const on = item.value === value;
        return (
          <button
            key={item.value}
            type="button"
            role="tab"
            aria-selected={on}
            className={cn("tab", on && "on")}
            onClick={() => onChange(item.value)}
          >
            {item.icon ? <Ic name={item.icon} size={14} /> : null}
            {item.label}
            {item.count !== undefined ? (
              <span className="cnt">{item.count}</span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Progress / Stackbar                                                */
/* ------------------------------------------------------------------ */

export type ProgressTone = "green" | "amber" | "red";

export interface ProgressProps extends HTMLAttributes<HTMLSpanElement> {
  value: number;
  tone?: ProgressTone;
}

export function Progress({ value, tone, className, ...rest }: ProgressProps) {
  const clamped = Math.max(0, Math.min(1, value));
  const percent = Math.round(clamped * 100);
  return (
    <span
      className={cn("prog", tone, className)}
      role="progressbar"
      aria-valuenow={percent}
      aria-valuemin={0}
      aria-valuemax={100}
      {...rest}
    >
      <i style={{ "--p": String(clamped) } as CSSProperties} />
    </span>
  );
}

export interface StackbarPart {
  value: number;
  color?: string;
  label?: string;
}

export interface StackbarProps extends HTMLAttributes<HTMLDivElement> {
  parts: StackbarPart[];
  height?: number;
}

export function Stackbar({
  parts,
  height,
  className,
  style,
  ...rest
}: StackbarProps) {
  const total = parts.reduce((sum, part) => sum + Math.max(0, part.value), 0) || 1;
  return (
    <div
      className={cn("stackbar", className)}
      style={{ ...style, height }}
      {...rest}
    >
      {parts.map((part, index) => (
        <i
          key={index}
          style={{
            width: `${(Math.max(0, part.value) / total) * 100}%`,
            background: part.color,
          }}
          title={part.label}
        />
      ))}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Toggle / Checkbox / Radio                                          */
/* ------------------------------------------------------------------ */

export interface ToggleProps
  extends Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "onChange"> {
  checked: boolean;
  onChange: (checked: boolean) => void;
}

export function Toggle({ checked, onChange, className, ...rest }: ToggleProps) {
  return (
    <input
      type="checkbox"
      className={cn("toggle", className)}
      checked={checked}
      onChange={(event) => onChange(event.target.checked)}
      {...rest}
    />
  );
}

export interface CheckboxProps
  extends Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "onChange"> {
  checked: boolean;
  onChange: (checked: boolean) => void;
  round?: boolean;
}

export function Checkbox({
  checked,
  onChange,
  round,
  className,
  ...rest
}: CheckboxProps) {
  return (
    <input
      type="checkbox"
      className={cn("check", round && "round", className)}
      checked={checked}
      onChange={(event) => onChange(event.target.checked)}
      {...rest}
    />
  );
}

export interface RadioProps
  extends Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "onChange"> {
  checked: boolean;
  onChange: (checked: boolean) => void;
}

export function Radio({ checked, onChange, className, ...rest }: RadioProps) {
  return (
    <input
      type="radio"
      className={cn("radio", className)}
      checked={checked}
      onChange={(event) => onChange(event.target.checked)}
      {...rest}
    />
  );
}

/* ------------------------------------------------------------------ */
/* Field + Input / Textarea                                           */
/* ------------------------------------------------------------------ */

export interface FieldContextValue {
  id: string;
  describedBy?: string;
  invalid: boolean;
}

export const FieldContext = createContext<FieldContextValue | null>(null);

export interface FieldProps extends HTMLAttributes<HTMLDivElement> {
  label?: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  htmlFor?: string;
}

export function Field({
  label,
  hint,
  error,
  htmlFor,
  id,
  className,
  children,
  ...rest
}: FieldProps) {
  const generated = "field-" + useId().replace(/[^A-Za-z0-9_-]/g, "");
  const fieldId = id ?? htmlFor ?? generated;
  const hintId = hint ? fieldId + "-hint" : undefined;
  const errorId = error ? fieldId + "-err" : undefined;
  const describedBy = [hintId, errorId].filter(Boolean).join(" ") || undefined;
  return (
    <div className={cn("field", className)} {...rest}>
      {label ? (
        <label className="label" htmlFor={fieldId}>
          {label}
        </label>
      ) : null}
      <FieldContext.Provider
        value={{ id: fieldId, describedBy, invalid: Boolean(error) }}
      >
        {children}
      </FieldContext.Provider>
      {hint ? (
        <span className="hint" id={hintId}>
          {hint}
        </span>
      ) : null}
      {error ? (
        <span className="err" id={errorId}>
          <Ic name="circle-alert" size={12} />
          {error}
        </span>
      ) : null}
    </div>
  );
}

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  invalid?: boolean;
}

export function Input({ invalid, className, id, ...rest }: InputProps) {
  const field = useContext(FieldContext);
  const isInvalid = invalid ?? field?.invalid ?? false;
  return (
    <input
      id={id ?? field?.id}
      className={cn("input", isInvalid && "is-error", className)}
      aria-invalid={isInvalid || undefined}
      aria-describedby={field?.describedBy}
      {...rest}
    />
  );
}

export interface TextareaProps
  extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  invalid?: boolean;
}

export function Textarea({ invalid, className, id, ...rest }: TextareaProps) {
  const field = useContext(FieldContext);
  const isInvalid = invalid ?? field?.invalid ?? false;
  return (
    <textarea
      id={id ?? field?.id}
      className={cn("textarea", isInvalid && "is-error", className)}
      aria-invalid={isInvalid || undefined}
      aria-describedby={field?.describedBy}
      {...rest}
    />
  );
}

/* ------------------------------------------------------------------ */
/* Kbd / SearchInput                                                  */
/* ------------------------------------------------------------------ */

export function Kbd({ className, children, ...rest }: HTMLAttributes<HTMLElement>) {
  return (
    <kbd className={className} {...rest}>
      {children}
    </kbd>
  );
}

export interface SearchInputProps
  extends Omit<InputHTMLAttributes<HTMLInputElement>, "size"> {
  kbd?: ReactNode;
  wrapClassName?: string;
  size?: "md" | "sm";
}

export function SearchInput({
  kbd = "/",
  size = "md",
  className,
  wrapClassName,
  ...rest
}: SearchInputProps) {
  return (
    <div className={cn("inwrap", wrapClassName)}>
      <Ic name="search" size={13} />
      <input
        className={cn("input", size === "sm" && "search-sm", className)}
        {...rest}
      />
      {kbd !== null && kbd !== undefined ? (
        <span className="kbd">{kbd}</span>
      ) : null}
    </div>
  );
}
