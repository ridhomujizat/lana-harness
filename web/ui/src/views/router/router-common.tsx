import { useState } from "react";
import { ApiError, errorMessage } from "../../api/client";
import type { BadgeTone } from "../../ui/gr8r";
import { IconButton } from "../../ui/gr8r";
import { agoLabel } from "../agent-common";

/* Shared formatting for the Router page (spec 37). Money is float USD; an
   unknown cost renders as "—" (AC-RTR-014). */

export const DASH = "—";

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/* $7.50, $0.0042, $12,340. Small amounts keep two significant digits so a
   cheap call never reads as $0.00. */
export function usd(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(Number(value))) {
    return DASH;
  }
  const number = Number(value);
  const abs = Math.abs(number);
  if (abs === 0) {
    return "$0.00";
  }
  if (abs < 0.01) {
    return "$" + number.toPrecision(2).replace(/0+$/, "").replace(/\.$/, "");
  }
  if (abs >= 1000) {
    return "$" + Math.round(number).toLocaleString("en-US");
  }
  return "$" + number.toFixed(2);
}

/* The cost of a usage row: "—" when no price was known for any of it, the
   amount otherwise. `partial` is shown next to it by the caller. */
export function costText(cost: number | null | undefined, partial?: boolean): string {
  if (cost === null || cost === undefined) {
    return DASH;
  }
  if (partial && Number(cost) === 0) {
    return DASH;
  }
  return usd(cost);
}

/* The cost of an aggregate row: "—" when none of its calls answered with a
   known price (every call failed, or the ok ones had no price), so a row of
   failures never reads as a free "$0.00". */
export function statsCostText(stats: {
  calls: number;
  errors?: number;
  cost_usd: number | null | undefined;
  cost_partial?: boolean;
}): string {
  const cost = stats.cost_usd;
  const failedOnly = Number(stats.calls) > 0 && Number(stats.errors) >= Number(stats.calls);
  if (failedOnly && !Number(cost)) {
    return DASH;
  }
  return costText(cost, stats.cost_partial);
}

export function count(value: number | null | undefined): string {
  return (Number(value) || 0).toLocaleString("en-US");
}

function trimDecimal(value: number): string {
  const rounded = Math.round(value * 10) / 10;
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
}

export function compact(value: number | null | undefined): string {
  const number = Number(value) || 0;
  const abs = Math.abs(number);
  if (abs < 1000) {
    return String(Math.round(number));
  }
  if (abs < 1000000) {
    return trimDecimal(number / 1000) + "k";
  }
  if (abs < 1000000000) {
    return trimDecimal(number / 1000000) + "M";
  }
  return trimDecimal(number / 1000000000) + "B";
}

export function ms(value: number | null | undefined): string {
  const number = Number(value) || 0;
  if (number <= 0) {
    return DASH;
  }
  if (number < 1000) {
    return Math.round(number) + " ms";
  }
  if (number < 60000) {
    return trimDecimal(number / 1000) + " s";
  }
  return trimDecimal(number / 60000) + " min";
}

export function percent(part: number, whole: number): string {
  if (!whole || whole <= 0) {
    return "0%";
  }
  const value = (part / whole) * 100;
  if (value > 0 && value < 1) {
    return "<1%";
  }
  return Math.round(value) + "%";
}

function pad2(value: number): string {
  return value < 10 ? "0" + value : String(value);
}

export function parseTime(value: string | null | undefined): number {
  if (!value) {
    return 0;
  }
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

/* "14:05" today, "Nov 9, 14:05" on another day. */
export function timeLabel(value: string | null | undefined): string {
  const at = parseTime(value);
  if (!at) {
    return DASH;
  }
  const date = new Date(at);
  const now = new Date();
  const clock = pad2(date.getHours()) + ":" + pad2(date.getMinutes());
  if (
    date.getFullYear() === now.getFullYear() &&
    date.getMonth() === now.getMonth() &&
    date.getDate() === now.getDate()
  ) {
    return clock;
  }
  return MONTHS[date.getMonth()] + " " + date.getDate() + ", " + clock;
}

export function dateLabel(value: string | null | undefined): string {
  const at = parseTime(value);
  if (!at) {
    return DASH;
  }
  const date = new Date(at);
  return MONTHS[date.getMonth()] + " " + date.getDate() + ", " + date.getFullYear();
}

export function agoText(value: string | null | undefined): string {
  return agoLabel(parseTime(value)) || DASH;
}

/* The provider kinds of API-RTR-001. The four presets know their base URL;
   the custom kind needs one. A connection test without a typed model lets the
   daemon pick one it knows for the kind (API-RTR-006). */
export interface KindPreset {
  value: string;
  label: string;
  baseUrl: string;
}

export const KIND_PRESETS: KindPreset[] = [
  { value: "openai", label: "OpenAI", baseUrl: "https://api.openai.com/v1" },
  { value: "deepseek", label: "DeepSeek", baseUrl: "https://api.deepseek.com/v1" },
  { value: "anthropic", label: "Anthropic", baseUrl: "https://api.anthropic.com" },
  { value: "openrouter", label: "OpenRouter", baseUrl: "https://openrouter.ai/api/v1" },
  { value: "openai_compat", label: "Custom (OpenAI-compatible)", baseUrl: "" },
];

export function kindLabel(kind: string): string {
  if (kind === "claude-code") {
    return "Claude Agent";
  }
  const preset = KIND_PRESETS.find((entry) => entry.value === kind);
  return preset ? preset.label : kind || "Unknown";
}

export function kindPreset(kind: string): KindPreset | undefined {
  return KIND_PRESETS.find((entry) => entry.value === kind);
}

/* Plan windows of the Claude Agent (spec 38): known types get a name, others
   are humanized ("seven_day_opus" → "Seven day opus window"). */
const WINDOW_LABELS: Record<string, string> = {
  five_hour: "5-hour window",
  seven_day: "Weekly window",
};

export function windowLabel(type: string): string {
  const known = WINDOW_LABELS[type];
  if (known) {
    return known;
  }
  const words = String(type || "window").replace(/[_-]+/g, " ").trim();
  const text = words.charAt(0).toUpperCase() + words.slice(1);
  return /window$/i.test(text) ? text : text + " window";
}

/* The call outcome badge: "ok" or the error class, always with text. */
export function outcomeTone(outcome: string): BadgeTone {
  if (outcome === "ok") {
    return "green";
  }
  if (outcome === "rate_limit" || outcome === "overloaded" || outcome === "timeout") {
    return "amber";
  }
  return "red";
}

export function outcomeLabel(outcome: string): string {
  if (outcome === "ok") {
    return "OK";
  }
  const text = String(outcome || "error").replace(/_/g, " ");
  return text.charAt(0).toUpperCase() + text.slice(1);
}

const SOURCE_LABELS: Record<string, string> = {
  turn: "Chat turn",
  compaction: "Compaction",
  memory_capture: "Memory capture",
  knowledge: "Knowledge",
  embed: "Embedding",
  workflow: "Workflow",
  proxy: "Proxy",
  test: "Test",
};

export function sourceLabel(source: string): string {
  return SOURCE_LABELS[source] || (source ? source.replace(/_/g, " ") : "Other");
}

export const KNOWN_SOURCES = Object.keys(SOURCE_LABELS);

export interface Failure {
  message: string;
  details: unknown[];
  code: string;
  status: number;
}

export function failureOf(failure: unknown): Failure {
  return {
    message: errorMessage(failure),
    details: failure instanceof ApiError ? failure.details : [],
    code: failure instanceof ApiError ? failure.code : "",
    status: failure instanceof ApiError ? failure.status : 0,
  };
}

export function isStale(failure: unknown): boolean {
  return (
    failure instanceof ApiError &&
    (failure.code === "stale_version" || failure.code === "version_conflict")
  );
}

/* AC-RTR-019: the in-use refusal names who uses the item. */
export function inUseText(failure: unknown, what: string): string {
  if (failure instanceof ApiError && failure.code === "in_use") {
    const reason = failure.message || "it is in use";
    return "Cannot delete " + what + ": " + reason + ".";
  }
  return "";
}

/* A copy button with a short "Copied" confirmation. */
export function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard?.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      setCopied(false);
    }
  }
  return (
    <IconButton
      icon={copied ? "check" : "copy"}
      size="sm"
      label={copied ? "Copied" : label}
      tip={copied ? "Copied" : label}
      onClick={() => void copy()}
    />
  );
}
