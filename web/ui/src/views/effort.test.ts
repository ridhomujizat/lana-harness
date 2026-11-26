import { describe, expect, it } from "vitest";
import type { RouterChoice } from "../api/router";
import {
  CLAUDE_CODE_EFFORTS,
  choiceLevels,
  cleanEfforts,
  effortChoice,
  effortName,
  keptEffort,
  variantOf,
} from "./effort";

/* Spec 76 (REQ-MES-006): a model switch keeps the level when the new model
   lists it, else goes back to "" (the model default). */
describe("keptEffort", () => {
  const cases: [string, string[] | null, string][] = [
    ["", ["low", "high"], ""],
    ["high", ["low", "medium", "high"], "high"],
    ["max", ["low", "medium", "high"], ""],
    ["minimal", ["minimal", "low"], "minimal"],
    ["xhigh", ["medium"], ""],
    ["high", null, ""],
    ["high", [], ""],
  ];
  for (const [value, efforts, want] of cases) {
    it(JSON.stringify(value) + " on " + JSON.stringify(efforts) + " -> " + JSON.stringify(want), () => {
      expect(
        keptEffort(value, efforts ? { efforts, default_effort: "" } : null)
      ).toBe(want);
    });
  }
});

describe("effort helpers", () => {
  it("names the levels by title-casing them", () => {
    expect(["minimal", "low", "medium", "high", "xhigh", "max", "turbo"].map(effortName)).toEqual([
      "Minimal",
      "Low",
      "Medium",
      "High",
      "Extra high",
      "Max",
      "Turbo",
    ]);
  });

  it("keeps the model's own levels in order, without repeats", () => {
    expect(cleanEfforts(["max", "low", "", "low", "minimal"])).toEqual(["max", "low", "minimal"]);
    expect(cleanEfforts(undefined)).toEqual([]);
  });

  it("reads the levels of a choice", () => {
    expect(
      choiceLevels({ efforts: ["minimal", "low", "high"], default_effort: "high" })
    ).toEqual({ efforts: ["minimal", "low", "high"], default_effort: "high" });
    expect(choiceLevels({ efforts: [] })).toBeNull();
    expect(choiceLevels({})).toBeNull();
    expect(choiceLevels(null)).toBeNull();
    /* An older daemon's Claude Code choice falls back to the Claude levels. */
    expect(choiceLevels({}, true)).toEqual({
      efforts: CLAUDE_CODE_EFFORTS,
      default_effort: "xhigh",
    });
    /* A default the model does not list is dropped. */
    expect(choiceLevels({ efforts: ["low"], default_effort: "max" })).toEqual({
      efforts: ["low"],
      default_effort: "",
    });
  });

  const choices: RouterChoice[] = [
    {
      value: "antigravity/gemini-4-neon",
      label: "Gemini 4 Neon",
      group: "Antigravity",
      efforts: ["low", "medium", "high", "xhigh"],
      default_effort: "high",
    },
    { value: "deepseek/deepseek-flash", label: "DeepSeek Flash", group: "deepseek" },
  ];

  it("finds the choice of a model or a combo's first model", () => {
    expect(effortChoice("antigravity/gemini-4-neon", choices)?.label).toBe("Gemini 4 Neon");
    expect(
      effortChoice("fast", choices, [
        { name: "fast", models: ["antigravity/gemini-4-neon"], strategy: "", usage: null } as never,
      ])?.value
    ).toBe("antigravity/gemini-4-neon");
    expect(effortChoice("", choices)).toBeNull();
    expect(effortChoice("other/model", choices)).toBeNull();
  });

  it("recognizes a full Antigravity variant address", () => {
    const found = variantOf("antigravity/gemini-4-neon-high", choices);
    expect(found?.base.value).toBe("antigravity/gemini-4-neon");
    expect(found?.level).toBe("high");
    expect(variantOf("antigravity/gemini-4-neon", choices)).toBeNull();
    expect(variantOf("antigravity/gemini-4-neon-max", choices)).toBeNull();
    expect(variantOf("deepseek/deepseek-flash-high", choices)).toBeNull();
  });
});
