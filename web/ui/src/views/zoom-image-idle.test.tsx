import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render } from "@testing-library/react";
import { ZoomImage } from "./zoom-image";

afterEach(() => vi.useRealTimers());

function mouseMove() {
  const e = new Event("pointermove");
  Object.assign(e, { pointerType: "mouse" });
  window.dispatchEvent(e);
}

describe("ZoomImage idle controls", () => {
  it("hides the toolbar after an idle while zoomed and shows it on mouse move", () => {
    vi.useFakeTimers();
    const { container } = render(<ZoomImage src="x.png" alt="x" />);
    const zi = container.querySelector(".zi")!;
    act(() => vi.advanceTimersByTime(5000));
    expect(zi.classList.contains("is-idle")).toBe(false);
    fireEvent.doubleClick(zi);
    act(() => vi.advanceTimersByTime(5000));
    expect(zi.classList.contains("is-idle")).toBe(true);
    act(() => mouseMove());
    expect(zi.classList.contains("is-idle")).toBe(false);
    act(() => vi.advanceTimersByTime(5000));
    expect(zi.classList.contains("is-idle")).toBe(true);
    fireEvent.click(container.querySelector('[aria-label="Fit to view"]')!);
    expect(zi.classList.contains("is-idle")).toBe(false);
  });
});
