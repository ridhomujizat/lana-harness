import type {
  ButtonHTMLAttributes,
  CSSProperties,
  HTMLAttributes,
  ReactNode,
} from "react";
import { Ic } from "../icons";
import { cn, toSize } from "./util";

/* ------------------------------------------------------------------ */
/* Panel                                                              */
/* ------------------------------------------------------------------ */

export interface PanelProps extends Omit<HTMLAttributes<HTMLElement>, "title"> {
  title?: ReactNode;
  actions?: ReactNode;
  bodyClassName?: string;
}

export function Panel({
  title,
  actions,
  bodyClassName,
  className,
  children,
  ...rest
}: PanelProps) {
  return (
    <section className={cn("panel", className)} {...rest}>
      {title || actions ? (
        <div className="panel-h">
          {title ? <h2>{title}</h2> : null}
          {actions ? <div className="acts">{actions}</div> : null}
        </div>
      ) : null}
      <div className={cn("panel-b", bodyClassName)}>{children}</div>
    </section>
  );
}

/* ------------------------------------------------------------------ */
/* Stat                                                               */
/* ------------------------------------------------------------------ */

export interface StatProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "value"> {
  label: ReactNode;
  value: ReactNode;
  detail?: ReactNode;
  icon?: string;
  tone?: "up" | "bad";
}

export function Stat({
  label,
  value,
  detail,
  icon,
  tone,
  className,
  type,
  ...rest
}: StatProps) {
  return (
    <button type={type ?? "button"} className={cn("stat", className)} {...rest}>
      <span className="k">
        {icon ? <Ic name={icon} size={14} /> : null}
        {label}
      </span>
      <span className="v">{value}</span>
      {detail !== undefined ? (
        <span className={cn("d", tone)}>{detail}</span>
      ) : null}
    </button>
  );
}

/* ------------------------------------------------------------------ */
/* EmptyState / ErrorState                                            */
/* ------------------------------------------------------------------ */

export interface EmptyStateProps
  extends Omit<HTMLAttributes<HTMLDivElement>, "title"> {
  icon?: string;
  title?: ReactNode;
  text?: ReactNode;
  action?: ReactNode;
  size?: "sm";
  level?: 2 | 3;
  tone?: "err";
}

export function EmptyState({
  icon,
  title,
  text,
  action,
  size,
  level = 2,
  tone,
  className,
  children,
  ...rest
}: EmptyStateProps) {
  const Heading = (level === 3 ? "h3" : "h2") as "h2" | "h3";
  return (
    <div
      className={cn(
        "empty-state",
        size === "sm" && "sm",
        tone === "err" && "err",
        className
      )}
      {...rest}
    >
      {icon ? (
        <div className="glyph">
          <Ic name={icon} size={20} />
        </div>
      ) : null}
      {title ? <Heading className="es-h">{title}</Heading> : null}
      {text ? <p>{text}</p> : null}
      {children}
      {action}
    </div>
  );
}

export interface ErrorStateProps extends Omit<EmptyStateProps, "tone"> {
  code?: ReactNode;
  full?: boolean;
}

export function ErrorState({
  code,
  full,
  icon = "cloud-alert",
  ...rest
}: ErrorStateProps) {
  const inner = <EmptyState {...rest} icon={icon} tone="err" />;
  if (!full) {
    return inner;
  }
  return (
    <div className="fullstate">
      <div className="box">
        {code ? <span className="code">{code}</span> : null}
        {inner}
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Skeleton                                                           */
/* ------------------------------------------------------------------ */

export interface SkeletonProps extends HTMLAttributes<HTMLSpanElement> {
  width?: number | string;
  height?: number | string;
  radius?: number | string;
}

export function Skeleton({
  width,
  height = 10,
  radius,
  className,
  style,
  ...rest
}: SkeletonProps) {
  const merged: CSSProperties = {
    width: toSize(width),
    height: toSize(height),
    borderRadius: toSize(radius),
    ...style,
  };
  return <span className={cn("sk", className)} style={merged} {...rest} />;
}
