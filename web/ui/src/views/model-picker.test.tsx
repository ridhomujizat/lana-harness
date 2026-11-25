import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import {
  COLLAPSE_STORAGE_KEY,
  ModelPicker,
  matchesModel,
} from "./model-picker";
import type { ModelOption } from "./model-picker";
import { Modal } from "../ui/gr8r";

/* A small router list: combos, two API providers, Claude Code, Antigravity. */
const OPTIONS: ModelOption[] = [
  { value: "coding", label: "coding", group: "Combos" },
  { value: "deepseek/deepseek-flash", label: "DeepSeek Flash", group: "deepseek", hint: "2 levels" },
  { value: "deepseek/deepseek-pro", label: "DeepSeek Pro", group: "deepseek" },
  { value: "openai/gpt-5", label: "GPT-5", group: "openai", hint: "4 levels" },
  { value: "claude-code/opus", label: "Opus (latest)", group: "Claude Code" },
  { value: "antigravity/gemini-4-neon", label: "Gemini 4 Neon", group: "Antigravity" },
  { value: "antigravity/gemini-4-neon-high", label: "Gemini 4 Neon High", group: "Antigravity" },
];

function optionValues(): string[] {
  return screen.getAllByRole("option").map((node) => node.getAttribute("data-value") || "");
}

function groupNames(): string[] {
  return Array.from(screen.getByRole("listbox").querySelectorAll('[role="group"]')).map(
    (node) => node.getAttribute("aria-label") || ""
  );
}

function search(): HTMLInputElement {
  return screen.getByRole("combobox", { name: "Search models" }) as HTMLInputElement;
}

function open(value = "openai/gpt-5", options = OPTIONS, onChange = vi.fn()) {
  render(<ModelPicker aria-label="Model" options={options} value={value} onChange={onChange} />);
  fireEvent.click(screen.getByRole("combobox", { name: "Model" }));
  return onChange;
}

beforeEach(() => {
  window.localStorage.removeItem(COLLAPSE_STORAGE_KEY);
});

afterEach(() => {
  window.localStorage.removeItem(COLLAPSE_STORAGE_KEY);
});

describe("matchesModel", () => {
  it("needs every word in the label, address, or group", () => {
    const neonHigh = OPTIONS[6] as ModelOption;
    expect(matchesModel(neonHigh, "neon high")).toBe(true);
    expect(matchesModel(neonHigh, "ANTIGRAVITY neon")).toBe(true);
    expect(matchesModel(neonHigh, "neon low")).toBe(false);
    expect(matchesModel(OPTIONS[4] as ModelOption, "claude opus")).toBe(true);
    expect(matchesModel(OPTIONS[1] as ModelOption, "deepseek flash")).toBe(true);
  });
});

describe("ModelPicker", () => {
  it("shows the selected label and group on the trigger", () => {
    render(<ModelPicker aria-label="Model" options={OPTIONS} value="openai/gpt-5" onChange={() => {}} />);
    const trigger = screen.getByRole("combobox", { name: "Model" });
    expect(trigger.textContent).toContain("GPT-5");
    expect(trigger.querySelector(".mp-trigger-group")?.textContent).toBe("openai");
  });

  it("orders Combos first, then the selected model's group, then the rest", () => {
    open("openai/gpt-5");
    expect(groupNames()).toEqual(["Combos", "openai", "deepseek", "Claude Code", "Antigravity"]);
    expect(document.activeElement).toBe(search());
    const selected = screen.getByRole("option", { name: "GPT-5" });
    expect(selected.getAttribute("aria-selected")).toBe("true");
    expect(search().getAttribute("aria-activedescendant")).toBe(selected.id);
    expect(screen.getByText("4 levels")).toBeTruthy();
  });

  it("filters across groups with every word matching", () => {
    open();
    fireEvent.change(search(), { target: { value: "neon high" } });
    expect(optionValues()).toEqual(["antigravity/gemini-4-neon-high"]);
    expect(groupNames()).toEqual(["Antigravity"]);

    fireEvent.change(search(), { target: { value: "deepseek" } });
    expect(optionValues()).toEqual(["deepseek/deepseek-flash", "deepseek/deepseek-pro"]);

    fireEvent.change(search(), { target: { value: "claude opus" } });
    expect(optionValues()).toEqual(["claude-code/opus"]);
  });

  it("says when nothing matches", () => {
    open();
    fireEvent.change(search(), { target: { value: "zzz" } });
    expect(screen.queryAllByRole("option")).toHaveLength(0);
    expect(screen.getByRole("status").textContent).toBe("No models match “zzz”");
  });

  it("collapses a group, remembers it, and expands it for a search", () => {
    open();
    const header = within(screen.getByRole("group", { name: "deepseek" })).getByRole("button");
    expect(header.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(header);
    expect(header.getAttribute("aria-expanded")).toBe("false");
    expect(optionValues()).not.toContain("deepseek/deepseek-flash");
    expect(JSON.parse(window.localStorage.getItem(COLLAPSE_STORAGE_KEY) || "{}")).toEqual({
      deepseek: true,
    });

    fireEvent.change(search(), { target: { value: "flash" } });
    expect(optionValues()).toEqual(["deepseek/deepseek-flash"]);

    fireEvent.change(search(), { target: { value: "" } });
    expect(optionValues()).not.toContain("deepseek/deepseek-flash");
  });

  it("starts the other groups collapsed when the list is long", () => {
    const many: ModelOption[] = [{ value: "coding", label: "coding", group: "Combos" }];
    for (let index = 0; index < 45; index += 1) {
      many.push({
        value: "p" + (index % 3) + "/m" + index,
        label: "Model " + index,
        group: "p" + (index % 3),
      });
    }
    open("p1/m1", many);
    expect(groupNames()).toEqual(["Combos", "p1", "p0", "p2"]);
    const shown = optionValues();
    expect(shown).toContain("coding");
    expect(shown).toContain("p1/m4");
    expect(shown).not.toContain("p0/m0");
    expect(shown).not.toContain("p2/m2");
  });

  it("moves with the arrow keys, skipping headings, and picks with Enter", () => {
    const onChange = open("coding");
    const input = search();
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(input.getAttribute("aria-activedescendant")).toBe(
      screen.getByRole("option", { name: "DeepSeek Flash" }).id
    );
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(input.getAttribute("aria-activedescendant")).toBe(
      screen.getByRole("option", { name: "DeepSeek Pro" }).id
    );
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith("deepseek/deepseek-pro");
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("picks the first match with Enter after a search", () => {
    const onChange = open();
    fireEvent.change(search(), { target: { value: "neon" } });
    fireEvent.keyDown(search(), { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith("antigravity/gemini-4-neon");
  });

  it("closes on Escape without a change", () => {
    const onChange = open();
    fireEvent.keyDown(search(), { key: "Escape" });
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("closes only itself on Escape inside a modal", () => {
    const onClose = vi.fn();
    render(
      <Modal title="New agent" onClose={onClose}>
        <ModelPicker aria-label="Model" options={OPTIONS} value="coding" onChange={() => {}} />
      </Modal>
    );
    fireEvent.click(screen.getByRole("combobox", { name: "Model" }));
    fireEvent.keyDown(search(), { key: "Escape" });
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("sends a typed key from a heading to the search box", () => {
    open();
    const header = within(screen.getByRole("group", { name: "deepseek" })).getByRole("button");
    header.focus();
    fireEvent.keyDown(header, { key: "g" });
    expect(search().value).toBe("g");
    expect(document.activeElement).toBe(search());
  });

  it("calls onChange with the clicked address", () => {
    const onChange = open();
    fireEvent.click(screen.getByRole("option", { name: "Opus (latest)" }));
    expect(onChange).toHaveBeenCalledWith("claude-code/opus");
  });

  it("lists a legacy saved value under Current, and keeps a sticky Other entry in a search", () => {
    const options: ModelOption[] = [
      {
        value: "antigravity/gemini-4-neon-high-legacy",
        label: "antigravity/gemini-4-neon-high-legacy",
        group: "Current",
      },
      ...OPTIONS,
      { value: "__other__", label: "Other…", sticky: true },
    ];
    const onChange = open("antigravity/gemini-4-neon-high-legacy", options);
    expect(groupNames()[0]).toBe("Current");
    expect(
      screen.getByRole("option", { name: "antigravity/gemini-4-neon-high-legacy" }).getAttribute("aria-selected")
    ).toBe("true");
    fireEvent.change(search(), { target: { value: "zzz" } });
    fireEvent.click(screen.getByRole("option", { name: "Other…" }));
    expect(onChange).toHaveBeenCalledWith("__other__");
  });
});
