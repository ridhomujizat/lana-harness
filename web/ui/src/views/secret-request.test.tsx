import { afterEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";

/* Spec 53: the secure secret input card and its state hook. The answer API is
   mocked; the value must be posted once and never shown or kept. */
vi.mock("../api/secret-requests", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/secret-requests")>();
  return { ...actual, saveSecretRequest: vi.fn(), cancelSecretRequest: vi.fn() };
});

import { ApiError } from "../api/client";
import { cancelSecretRequest, saveSecretRequest } from "../api/secret-requests";
import type { SessionEvent } from "../api/sessions";
import {
  SecretRequestCard,
  secretRequestsFromEvents,
  useSecretRequest,
} from "./secret-request";
import type { SecretRequestItem } from "./secret-request";

const VALUE = "sk-live-typed-in-card";

function item(overrides: Partial<SecretRequestItem> = {}): SecretRequestItem {
  return {
    request_id: "r1",
    name: "STRIPE_KEY",
    description: "The billing workflow charges cards.",
    link: "https://dashboard.stripe.com/apikeys",
    replaces: false,
    expires_at: Date.now() + 60_000,
    outcome: "",
    ...overrides,
  };
}

function ev(seq: number, kind: string, payload: Record<string, unknown>): SessionEvent {
  return { seq, kind, turn_id: "t1", created_at: seq, payload };
}

function valueInput(): HTMLInputElement {
  return screen.getByLabelText("Value") as HTMLInputElement;
}

afterEach(() => {
  vi.clearAllMocks();
});

describe("SecretRequestCard", () => {
  it("renders the name, the reason, the link, and a masked field with a show/hide toggle", () => {
    render(
      <SecretRequestCard request={item()} agentLabel="Dana" onSave={vi.fn()} onCancel={vi.fn()} />
    );
    expect(screen.getByText("Dana needs a secret")).toBeTruthy();
    expect(screen.getByRole("heading").textContent).toBe("Add STRIPE_KEY");
    expect(screen.getByText("The billing workflow charges cards.")).toBeTruthy();
    const link = screen.getByRole("link", { name: "Where to get it" }) as HTMLAnchorElement;
    expect(link.href).toBe("https://dashboard.stripe.com/apikeys");
    expect(link.rel).toContain("noopener");
    expect(valueInput().type).toBe("password");
    expect(valueInput().autocomplete).toBe("new-password");
    fireEvent.click(screen.getByRole("button", { name: "Show value" }));
    expect(valueInput().type).toBe("text");
    fireEvent.click(screen.getByRole("button", { name: "Hide value" }));
    expect(valueInput().type).toBe("password");
    expect(screen.queryByText(/replaces the current value/)).toBeNull();
    expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
  });

  it("warns clearly that saving replaces an existing value", () => {
    render(<SecretRequestCard request={item({ replaces: true })} onSave={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByRole("heading").textContent).toBe("Replace STRIPE_KEY");
    expect(screen.getByRole("note").textContent).toContain("already has a value. Saving replaces the current value.");
    expect(screen.getByRole("button", { name: "Replace value" })).toBeTruthy();
  });

  it("does not render an unsafe link", () => {
    render(
      <SecretRequestCard request={item({ link: "javascript:alert(1)" })} onSave={vi.fn()} onCancel={vi.fn()} />
    );
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("posts the value once, clears it, and shows a save error without losing the card", async () => {
    let resolveSave: (value: { ok: boolean; message?: string }) => void = () => undefined;
    const onSave = vi.fn(
      () => new Promise<{ ok: boolean; message?: string }>((resolve) => (resolveSave = resolve))
    );
    render(<SecretRequestCard request={item()} onSave={onSave} onCancel={vi.fn()} />);
    fireEvent.change(valueInput(), { target: { value: VALUE } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    fireEvent.submit(valueInput().form!);
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ request_id: "r1" }), VALUE);
    await act(async () => resolveSave({ ok: false, message: "Value must not contain a newline." }));
    expect(screen.getByRole("alert").textContent).toBe("Value must not contain a newline.");
    expect(valueInput().value).toBe(VALUE);

    onSave.mockResolvedValueOnce({ ok: true });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(valueInput().value).toBe(""));
    expect(onSave).toHaveBeenCalledTimes(2);
  });

  it("asks for a value before saving", () => {
    const onSave = vi.fn();
    render(<SecretRequestCard request={item()} onSave={onSave} onCancel={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toBe("Type the value first.");
  });

  it("collapses to the outcome without the value", () => {
    const { rerender, container } = render(
      <SecretRequestCard request={item({ outcome: "saved" })} onSave={vi.fn()} onCancel={vi.fn()} />
    );
    expect(screen.getByRole("status").textContent).toContain("Saved STRIPE_KEY");
    expect(container.querySelector("input")).toBeNull();
    rerender(<SecretRequestCard request={item({ outcome: "cancelled" })} onSave={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByRole("status").textContent).toContain("Cancelled");
    rerender(<SecretRequestCard request={item({ outcome: "expired" })} onSave={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByRole("status").textContent).toContain("Expired");
    expect(container.textContent).not.toContain(VALUE);
  });

  it("expires when expires_at passes", () => {
    vi.useFakeTimers();
    try {
      const onExpire = vi.fn();
      render(
        <SecretRequestCard
          request={item({ expires_at: Date.now() + 1000 })}
          onSave={vi.fn()}
          onCancel={vi.fn()}
          onExpire={onExpire}
        />
      );
      act(() => {
        vi.advanceTimersByTime(1500);
      });
      expect(onExpire).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("useSecretRequest", () => {
  it("derives the open request from the log and keeps the outcome once resolved", () => {
    const requested = ev(1, "secret/requested", {
      request_id: "r1",
      call_id: "c1",
      name: "STRIPE_KEY",
      description: "why",
      replaces: true,
      expires_at: Date.now() + 60_000,
    });
    const { result, rerender } = renderHook(({ events }) => useSecretRequest(events), {
      initialProps: { events: [requested] },
    });
    expect(result.current.request).toMatchObject({ request_id: "r1", name: "STRIPE_KEY", replaces: true, outcome: "" });

    rerender({ events: [requested, ev(2, "secret/resolved", { request_id: "r1", outcome: "saved" })] });
    expect(result.current.request).toMatchObject({ request_id: "r1", outcome: "saved" });
    act(() => result.current.dismiss());
    expect(result.current.request).toBeNull();
  });

  it("does not show an old resolved request on load", () => {
    const events = [
      ev(1, "secret/requested", { request_id: "r1", name: "A", description: "x", expires_at: 1 }),
      ev(2, "secret/resolved", { request_id: "r1", outcome: "cancelled" }),
    ];
    expect(secretRequestsFromEvents(events)[0]).toMatchObject({ outcome: "cancelled" });
    const { result } = renderHook(() => useSecretRequest(events));
    expect(result.current.request).toBeNull();
  });

  it("shows a stream request, saves through the API, and collapses to saved", async () => {
    vi.mocked(saveSecretRequest).mockResolvedValue({ outcome: "saved", name: "API_KEY" });
    const { result } = renderHook(() => useSecretRequest([]));
    act(() =>
      result.current.receive({ request_id: "r9", name: "API_KEY", description: "x", replaces: false, expires_at: Date.now() + 60_000 })
    );
    expect(result.current.request).toMatchObject({ request_id: "r9", outcome: "" });
    let outcome: { ok: boolean } = { ok: false };
    await act(async () => {
      outcome = await result.current.save(result.current.request!, VALUE);
    });
    expect(outcome.ok).toBe(true);
    expect(saveSecretRequest).toHaveBeenCalledWith("r9", VALUE);
    expect(result.current.request).toMatchObject({ request_id: "r9", outcome: "saved" });
    expect(JSON.stringify(result.current.request)).not.toContain(VALUE);
  });

  it("maps cancel, a closed request, and a validation error", async () => {
    vi.mocked(cancelSecretRequest).mockResolvedValue({ outcome: "cancelled", name: "A" });
    const { result } = renderHook(() => useSecretRequest([]));
    act(() => result.current.receive({ request_id: "r1", name: "A", description: "x", replaces: false, expires_at: Date.now() + 60_000 }));
    await act(async () => {
      await result.current.cancel(result.current.request!);
    });
    expect(result.current.request).toMatchObject({ outcome: "cancelled" });

    act(() => result.current.receive({ request_id: "r2", name: "B", description: "x", replaces: false, expires_at: Date.now() + 60_000 }));
    vi.mocked(saveSecretRequest).mockRejectedValueOnce(
      new ApiError(422, "validation_failed", "secret is not valid", [{ field: "value", message: "value must not contain a newline" }])
    );
    let failed: { ok: boolean; message?: string } = { ok: true };
    await act(async () => {
      failed = await result.current.save(result.current.request!, "a\nb");
    });
    expect(failed).toEqual({ ok: false, message: "Value must not contain a newline." });
    expect(result.current.request).toMatchObject({ request_id: "r2", outcome: "" });

    vi.mocked(saveSecretRequest).mockRejectedValueOnce(new ApiError(409, "secret_request_closed", "closed", []));
    await act(async () => {
      await result.current.save(result.current.request!, "x");
    });
    expect(result.current.request).toMatchObject({ request_id: "r2", outcome: "expired" });
  });
});
