import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ContextMeter, formatTokens } from "./context-meter";

vi.mock("../api/sessions", () => ({
  getSessionUsage: vi.fn(() =>
    Promise.resolve({ input_tokens: 0, output_tokens: 0, cached_input_tokens: 0, context_tokens: 419000, context_window: 1000000, compact_at: 800000 })
  ),
}));
vi.mock("../api/turns", () => ({ compactSession: vi.fn() }));

describe("context meter", () => {
  it("formats tokens like 419k/1m", () => {
    expect(formatTokens(419000)).toBe("419k");
    expect(formatTokens(1000000)).toBe("1m");
    expect(formatTokens(1500000)).toBe("1.5m");
    expect(formatTokens(512)).toBe("512");
  });

  it("shows the ring and opens the details", async () => {
    render(<ContextMeter sessionId="s1" refreshKey="a" busy={false} />);
    const ring = await screen.findByRole("button", { name: /Context window 42% · 419k\/1m/ });
    fireEvent.click(ring);
    expect(screen.getByRole("dialog", { name: "Context window" })).toBeTruthy();
    expect(screen.getByText(/Compacts automatically at 800k \(80%\)/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Compact context" })).toBeTruthy();
  });
});
