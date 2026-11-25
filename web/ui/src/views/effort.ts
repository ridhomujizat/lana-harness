import type { RouterChoice, RouterCombo } from "../api/router";

/* Spec 76 (REQ-MES-002, REQ-MES-006): the effort levels of one model are its
   own `efforts` values, in the order the daemon lists them, with no shared
   vocabulary: "minimal", "low", "xhigh", or any provider-specific value. */

const EFFORT_NAMES: Record<string, string> = {
  xhigh: "Extra high",
  max: "Max",
};

/* Claude Code accepts these as --effort (REQ-MES-002); used when a Claude Code
   choice comes from a daemon that does not report its levels yet. */
export const CLAUDE_CODE_EFFORTS = ["low", "medium", "high", "xhigh", "max"];
export const CLAUDE_CODE_DEFAULT_EFFORT = "xhigh";

/* The levels of one model and its default level ("" when unknown). */
export interface EffortLevels {
  efforts: string[];
  default_effort: string;
}

/* The label of one level: "xhigh" -> "Extra high", "max" -> "Max", any
   other value capitalized ("minimal" -> "Minimal"). */
export function effortName(value: string): string {
  const text = String(value || "");
  if (EFFORT_NAMES[text]) {
    return EFFORT_NAMES[text];
  }
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : "";
}

/* The non-empty values of a list, without repeats, in their own order. */
export function cleanEfforts(values: readonly string[] | null | undefined): string[] {
  const out: string[] = [];
  for (const value of values || []) {
    const text = String(value || "");
    if (text && !out.includes(text)) {
      out.push(text);
    }
  }
  return out;
}

/* The levels a router choice carries, or null when it has no effort control.
   A Claude Code choice from an older daemon falls back to the Claude Code
   levels. */
export function choiceLevels(
  choice: Pick<RouterChoice, "efforts" | "default_effort"> | null | undefined,
  claude = false
): EffortLevels | null {
  if (!choice) {
    return null;
  }
  if (Array.isArray(choice.efforts)) {
    const efforts = cleanEfforts(choice.efforts);
    if (efforts.length === 0) {
      return null;
    }
    const preset = String(choice.default_effort || "");
    return { efforts, default_effort: efforts.includes(preset) ? preset : "" };
  }
  if (claude) {
    return { efforts: CLAUDE_CODE_EFFORTS.slice(), default_effort: CLAUDE_CODE_DEFAULT_EFFORT };
  }
  return null;
}

/* REQ-MES-006: the effort kept when the model changes: the previous value
   when the new model lists it, else "" (the new model's default). */
export function keptEffort(previous: string, next: EffortLevels | null | undefined): string {
  const text = String(previous || "");
  return text && next && next.efforts.includes(text) ? text : "";
}

/* The router choice whose levels apply to a model value: the choice itself,
   or the first model of a combo. */
export function effortChoice(
  value: string,
  choices: RouterChoice[],
  combos?: RouterCombo[] | null
): RouterChoice | null {
  const text = String(value || "");
  if (!text) {
    return null;
  }
  const combo = (combos || []).find((entry) => entry.name === text);
  const target = combo ? String(combo.models[0] || "") : text;
  return choices.find((entry) => entry.value === target) || null;
}

/* REQ-MES-005: a full variant address ("antigravity/gemini-4-neon-high")
   whose base model is a choice listing that level. The effort is part of the
   address, so the effort control does not apply to it. */
export function variantOf(
  value: string,
  choices: RouterChoice[]
): { base: RouterChoice; level: string } | null {
  const text = String(value || "");
  if (!text || choices.some((choice) => choice.value === text)) {
    return null;
  }
  const dash = text.lastIndexOf("-");
  if (dash <= 0) {
    return null;
  }
  const level = text.slice(dash + 1);
  const base = choices.find((choice) => choice.value === text.slice(0, dash));
  if (!base || !level) {
    return null;
  }
  const levels = Array.isArray(base.efforts) ? base.efforts : [];
  return levels.includes(level) ? { base, level } : null;
}
