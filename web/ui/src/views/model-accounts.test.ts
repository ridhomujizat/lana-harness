import { describe, expect, it } from "vitest";
import type { AntigravityAccount, ClaudeAccount, RouterChoice } from "../api/router";
import { accountOptions, mergeAccountChoices, modelAccounts, withAccount } from "./model-accounts";

const CHOICES: RouterChoice[] = [
  { value: "fast", label: "fast", group: "Combos" },
  { value: "claude-code/opus", label: "Claude Code · Opus", group: "Claude Code", efforts: ["low", "high"] },
  { value: "claude-code/sonnet", label: "Claude Code · Sonnet", group: "Claude Code" },
  { value: "claude-work/opus", label: "Claude Code (claude-work) · Opus", group: "Claude Code", efforts: ["low", "high"] },
  { value: "antigravity/gemini-4", label: "Gemini 4", group: "Antigravity · antigravity" },
  { value: "agy-2/gemini-4", label: "Gemini 4", group: "Antigravity · Work" },
  { value: "openai/gpt-5", label: "GPT-5", group: "openai" },
];

const CLAUDE = [
  { name: "claude-code", label: "", email: "me@home.dev", display_name: "" },
  { name: "claude-work", label: "", email: "me@work.dev", display_name: "" },
] as unknown as ClaudeAccount[];

const AGY = [
  { name: "antigravity", label: "", email: "" },
  { name: "agy-2", label: "Work", email: "me@g.dev" },
] as unknown as AntigravityAccount[];

describe("mergeAccountChoices", () => {
  it("lists each Claude Code and Antigravity model once in one group", () => {
    const merged = mergeAccountChoices(CHOICES, "", null, CLAUDE);
    expect(merged.map((choice) => [choice.value, choice.label, choice.group])).toEqual([
      ["fast", "fast", "Combos"],
      ["claude-code/opus", "Claude Code · Opus", "Claude Code"],
      ["claude-code/sonnet", "Claude Code · Sonnet", "Claude Code"],
      ["antigravity/gemini-4", "Gemini 4", "Antigravity"],
      ["openai/gpt-5", "GPT-5", "openai"],
    ]);
  });

  it("keeps the current model's account for the models it offers", () => {
    const merged = mergeAccountChoices(CHOICES, "claude-work/opus", null, CLAUDE);
    expect(merged.map((choice) => choice.value)).toContain("claude-work/opus");
    /* claude-work has no Sonnet: the first account that does. */
    expect(merged.map((choice) => choice.value)).toContain("claude-code/sonnet");
    const agy = mergeAccountChoices(CHOICES, "agy-2/gemini-4", null, CLAUDE);
    expect(agy.map((choice) => choice.value)).toContain("agy-2/gemini-4");
  });
});

describe("modelAccounts", () => {
  it("names the accounts that offer the model", () => {
    expect(modelAccounts("claude-work/opus", CHOICES, null, CLAUDE)).toEqual({
      family: "claude",
      model: "opus",
      accounts: ["claude-code", "claude-work"],
    });
    expect(modelAccounts("claude-code/sonnet", CHOICES, null, CLAUDE)?.accounts).toEqual(["claude-code"]);
    expect(modelAccounts("agy-2/gemini-4", CHOICES, null, CLAUDE)?.accounts).toEqual(["antigravity", "agy-2"]);
    expect(modelAccounts("openai/gpt-5", CHOICES, null, CLAUDE)).toBeNull();
    expect(modelAccounts("fast", CHOICES, null, CLAUDE)).toBeNull();
  });
});

describe("accountOptions", () => {
  it("shows the label, else the email, else the name", () => {
    const claude = modelAccounts("claude-code/opus", CHOICES, null, CLAUDE)!;
    expect(accountOptions(claude, CLAUDE, AGY)).toEqual([
      { value: "claude-code", label: "me@home.dev — claude-code" },
      { value: "claude-work", label: "me@work.dev — claude-work" },
    ]);
    const agy = modelAccounts("agy-2/gemini-4", CHOICES, null, CLAUDE)!;
    expect(accountOptions(agy, CLAUDE, AGY)).toEqual([
      { value: "antigravity", label: "antigravity" },
      { value: "agy-2", label: "Work — me@g.dev · agy-2" },
    ]);
  });

  it("moves an address to another account", () => {
    expect(withAccount("claude-code/opus", "claude-work")).toBe("claude-work/opus");
  });
});
