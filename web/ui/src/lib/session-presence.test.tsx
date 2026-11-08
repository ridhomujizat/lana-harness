import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { PRESENCE_INTERVAL_MS, useSessionPresence } from "./session-presence";

let visibility: DocumentVisibilityState = "visible";
let focused = true;

beforeEach(() => {
  vi.useFakeTimers();
  visibility = "visible";
  focused = true;
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => visibility,
  });
  vi.spyOn(document, "hasFocus").mockImplementation(() => focused);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("useSessionPresence", () => {
  it("posts visible, repeats it, and posts hidden on hide, blur and unmount", () => {
    const report = vi.fn(() => Promise.resolve());
    const { unmount } = renderHook(() => useSessionPresence("s-1", report));
    expect(report).toHaveBeenLastCalledWith("s-1", true);
    expect(report).toHaveBeenCalledTimes(1);

    act(() => {
      vi.advanceTimersByTime(PRESENCE_INTERVAL_MS);
    });
    expect(report).toHaveBeenCalledTimes(2);
    expect(report).toHaveBeenLastCalledWith("s-1", true);

    visibility = "hidden";
    act(() => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(report).toHaveBeenLastCalledWith("s-1", false);
    const calls = report.mock.calls.length;
    act(() => {
      vi.advanceTimersByTime(PRESENCE_INTERVAL_MS);
    });
    expect(report.mock.calls.length).toBe(calls);

    visibility = "visible";
    act(() => {
      window.dispatchEvent(new Event("focus"));
    });
    expect(report).toHaveBeenLastCalledWith("s-1", true);

    focused = false;
    act(() => {
      window.dispatchEvent(new Event("blur"));
    });
    expect(report).toHaveBeenLastCalledWith("s-1", false);

    focused = true;
    act(() => {
      window.dispatchEvent(new Event("focus"));
    });
    expect(report).toHaveBeenLastCalledWith("s-1", true);
    unmount();
    expect(report).toHaveBeenLastCalledWith("s-1", false);
  });

  it("does nothing without a session and moves to a new session", () => {
    const report = vi.fn(() => Promise.resolve());
    const { rerender } = renderHook(({ id }) => useSessionPresence(id, report), {
      initialProps: { id: "" as string },
    });
    expect(report).not.toHaveBeenCalled();
    rerender({ id: "a" });
    expect(report).toHaveBeenLastCalledWith("a", true);
    rerender({ id: "b" });
    expect(report.mock.calls).toEqual([
      ["a", true],
      ["a", false],
      ["b", true],
    ]);
  });

  it("does not report a hidden tab", () => {
    visibility = "hidden";
    const report = vi.fn(() => Promise.resolve());
    renderHook(() => useSessionPresence("s-2", report));
    expect(report).not.toHaveBeenCalled();
  });
});
