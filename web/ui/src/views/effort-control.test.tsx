import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { EffortControl } from "./effort-control";

/* Spec 76 (REQ-MES-006): the effort control beside a model picker. */

const NEON = { efforts: ["low", "medium", "high", "xhigh"], default_effort: "high" };

function radios(): string[] {
  return screen.getAllByRole("radio").map((node) => node.textContent || "");
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("EffortControl", () => {
  it("is hidden when the model has no levels", () => {
    const { container, rerender } = render(
      <EffortControl levels={null} value="" onChange={() => {}} />
    );
    expect(container.innerHTML).toBe("");
    rerender(
      <EffortControl levels={{ efforts: [], default_effort: "" }} value="high" onChange={() => {}} />
    );
    expect(container.innerHTML).toBe("");
  });

  it("lists Default and the model's own levels as a radio group", () => {
    render(<EffortControl levels={NEON} value="" onChange={() => {}} />);
    expect(screen.getByRole("radiogroup", { name: "Reasoning effort" })).toBeTruthy();
    expect(radios()).toEqual(["Default (High)", "Low", "Medium", "High", "Extra high"]);
    expect(screen.getByRole("radio", { name: "Default (High)" }).getAttribute("aria-checked")).toBe(
      "true"
    );
  });

  it("labels provider-specific levels and a plain Default", () => {
    render(
      <EffortControl
        levels={{ efforts: ["minimal", "low", "max"], default_effort: "" }}
        value="max"
        onChange={() => {}}
      />
    );
    expect(radios()).toEqual(["Default", "Minimal", "Low", "Max"]);
    expect(screen.getByRole("radio", { name: "Max" }).getAttribute("aria-checked")).toBe("true");
  });

  it("shows a saved level the model does not list as its own option", () => {
    render(<EffortControl levels={NEON} value="max" onChange={() => {}} />);
    const stale = screen.getByRole("radio", { name: "Max (not offered by this model)" });
    expect(stale.getAttribute("aria-checked")).toBe("true");
    expect(screen.getAllByRole("radio", { checked: true })).toHaveLength(1);
  });

  it("calls onChange on click and with the arrow keys", () => {
    const onChange = vi.fn();
    render(<EffortControl levels={NEON} value="medium" onChange={onChange} />);
    fireEvent.click(screen.getByRole("radio", { name: "Extra high" }));
    expect(onChange).toHaveBeenLastCalledWith("xhigh");
    fireEvent.click(screen.getByRole("radio", { name: "Default (High)" }));
    expect(onChange).toHaveBeenLastCalledWith("");
    const group = screen.getByRole("radiogroup");
    fireEvent.keyDown(group, { key: "ArrowRight" });
    expect(onChange).toHaveBeenLastCalledWith("high");
    fireEvent.keyDown(group, { key: "ArrowLeft" });
    expect(onChange).toHaveBeenLastCalledWith("low");
    fireEvent.keyDown(group, { key: "Home" });
    expect(onChange).toHaveBeenLastCalledWith("");
    expect(screen.getByRole("radio", { name: "Medium" }).getAttribute("tabindex")).toBe("0");
  });

  it("is a select on a phone", () => {
    vi.stubGlobal(
      "matchMedia",
      (query: string) =>
        ({
          matches: query.includes("max-width"),
          media: query,
          addEventListener: () => {},
          removeEventListener: () => {},
        }) as unknown as MediaQueryList
    );
    const onChange = vi.fn();
    render(<EffortControl levels={NEON} value="max" onChange={onChange} />);
    expect(screen.queryByRole("radiogroup")).toBeNull();
    const select = screen.getByRole("combobox", { name: "Reasoning effort" });
    expect(select.textContent).toContain("Max (not offered by this model)");
    fireEvent.click(select);
    fireEvent.click(screen.getByRole("option", { name: "Medium" }));
    expect(onChange).toHaveBeenCalledWith("medium");
  });
});
