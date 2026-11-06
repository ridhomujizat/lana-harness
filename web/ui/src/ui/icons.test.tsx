import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Ic, LanaMark } from "./icons";

describe("Ic", () => {
  it("renders a Gr8r icon svg at the default size and stroke width", () => {
    const { container } = render(<Ic name="house" />);
    const svg = container.querySelector("svg.i");
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("width")).toBe("16");
    expect(svg?.getAttribute("height")).toBe("16");
    expect(svg?.getAttribute("stroke-width")).toBe("1.8");
  });

  it("honours the requested size", () => {
    const { container } = render(<Ic name="house" size={20} />);
    const svg = container.querySelector("svg.i");
    expect(svg?.getAttribute("width")).toBe("20");
    expect(svg?.getAttribute("height")).toBe("20");
  });

  it("inlines the icon markup", () => {
    const { container } = render(<Ic name="house" />);
    const svg = container.querySelector("svg.i");
    expect((svg?.innerHTML ?? "").trim()).not.toBe("");
  });

  it("falls back to a plain rect for an unknown name", () => {
    const { container } = render(<Ic name="not-a-real-icon" />);
    expect(container.querySelector("svg.i rect")).not.toBeNull();
  });
});

describe("LanaMark", () => {
  it("renders the Lana brand mark at the requested size", () => {
    const { container } = render(<LanaMark size={24} />);
    const mark = container.querySelector("img.lana-mark");
    expect(mark?.getAttribute("src")).toBe("/assets/icons/logo-mark.svg");
    expect(mark?.getAttribute("width")).toBe("24");
    expect(mark?.getAttribute("aria-hidden")).toBe("true");
  });
});
