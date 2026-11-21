import { afterEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { useRef } from "react";
import { useStickToBottom } from "./scroll-pin";

/* Perf: the observers attach once per scroller node, not on every render, so a
   busy timeline does not rebuild them on each stream update. */
function Host() {
  const ref = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  useStickToBottom(ref, stick);
  return (
    <div ref={ref} data-testid="scroller">
      <div>row</div>
    </div>
  );
}

function observerStub() {
  const calls = { count: 0 };
  class Stub {
    constructor(_callback: unknown) {
      calls.count += 1;
    }
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() {
      return [];
    }
  }
  return { calls, Stub };
}

describe("useStickToBottom", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("attaches the observers once across re-renders", () => {
    const resizes = observerStub();
    const mutations = observerStub();
    vi.stubGlobal("ResizeObserver", resizes.Stub);
    vi.stubGlobal("MutationObserver", mutations.Stub);

    const { rerender } = render(<Host />);
    expect(resizes.calls.count).toBe(1);
    expect(mutations.calls.count).toBe(1);

    rerender(<Host />);
    rerender(<Host />);
    rerender(<Host />);

    expect(resizes.calls.count).toBe(1);
    expect(mutations.calls.count).toBe(1);
  });
});
