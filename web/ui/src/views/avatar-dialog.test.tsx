import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

vi.mock("../api/agents", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/agents")>();
  return {
    ...actual,
    generateAgentAvatar: vi.fn(),
    putAgentAvatar: vi.fn(),
    deleteAgentAvatar: vi.fn(),
  };
});
vi.mock("../api/router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/router")>();
  return { ...actual, getRouter: vi.fn() };
});

import {
  deleteAgentAvatar,
  generateAgentAvatar,
  putAgentAvatar,
} from "../api/agents";
import { ApiError } from "../api/client";
import { getRouter } from "../api/router";
import type { RouterOverview } from "../api/router";
import { getAgentAvatar, resetAvatarStore } from "../lib/avatar-store";
import { AvatarDialog } from "./avatar-dialog";

/* Spec 43: the Generate avatar dialog. */

function blob(shape: string): string {
  return (
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">' +
    shape +
    '<ellipse cx="40" cy="46" rx="4" ry="7" fill="#ffffff"/>' +
    '<ellipse cx="60" cy="46" rx="4" ry="7" fill="#ffffff"/></svg>'
  );
}

const FIRST = blob('<circle cx="50" cy="50" r="38" fill="currentColor"/>');
const DEFAULT = blob('<ellipse cx="50" cy="54" rx="40" ry="32" fill="currentColor"/>');
const SECOND = blob('<rect x="14" y="14" width="72" height="72" rx="24" fill="currentColor"/>');

const ROUTER: RouterOverview = {
  version: "v1",
  providers: [],
  combos: [],
  cooling: [],
  proxy: { enabled: false, base_url: "" },
  choices: [
    { value: "openai/gpt-5-mini", label: "gpt-5-mini", group: "openai" },
    { value: "claude-code/sonnet", label: "Sonnet", group: "Claude Code" },
  ],
};

function preview(): HTMLElement {
  return screen.getByTestId("avatar-preview");
}

beforeEach(() => {
  vi.mocked(getRouter).mockResolvedValue(ROUTER);
  vi.mocked(generateAgentAvatar)
    .mockResolvedValueOnce({ svg: FIRST, model: "openai/gpt-5-mini", seed: 1, source: "model" })
    .mockResolvedValueOnce({ svg: SECOND, model: "", seed: 2, source: "local" });
  vi.mocked(putAgentAvatar).mockImplementation(async (name, body) => ({
    name,
    avatar_svg: body.svg,
    color: body.color || "brand",
    restart_required: false,
  }));
  vi.mocked(deleteAgentAvatar).mockResolvedValue({
    name: "lana",
    avatar_svg: DEFAULT,
    avatar_source: "default",
    color: "brand",
  });
});

afterEach(() => {
  vi.clearAllMocks();
  vi.mocked(generateAgentAvatar).mockReset();
  resetAvatarStore();
});

describe("AvatarDialog", () => {
  it("generates, recolors without a request, steps through results, and saves", async () => {
    const onClose = vi.fn();
    const onSaved = vi.fn();
    render(
      <AvatarDialog name="lana" color="brand" svg="" onClose={onClose} onSaved={onSaved} />
    );

    expect(preview().querySelector("svg")).toBeNull();
    expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    await waitFor(() => expect(preview().querySelector("circle")).not.toBeNull());
    expect(generateAgentAvatar).toHaveBeenCalledTimes(1);
    expect(vi.mocked(generateAgentAvatar).mock.calls[0]?.[0]).toBe("lana");
    expect(vi.mocked(generateAgentAvatar).mock.calls[0]?.[1]).not.toHaveProperty("model");
    expect(screen.getByText("Drawn by openai/gpt-5-mini.")).toBeTruthy();

    /* A color change recolors at once and sends nothing. */
    fireEvent.click(screen.getByRole("button", { name: "Teal" }));
    expect(preview().style.getPropertyValue("--c")).toBe("#12b5a6");
    expect(screen.getByRole("button", { name: "Teal" }).getAttribute("aria-pressed")).toBe("true");
    expect(generateAgentAvatar).toHaveBeenCalledTimes(1);
    expect(putAgentAvatar).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Regenerate" }));
    await waitFor(() => expect(preview().querySelector("rect")).not.toBeNull());
    expect(screen.getByText(/Built-in generator/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(preview().querySelector("circle")).not.toBeNull();
    expect((screen.getByRole("button", { name: "Undo" }) as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Redo" }));
    expect(preview().querySelector("rect")).not.toBeNull();
    expect((screen.getByRole("button", { name: "Redo" }) as HTMLButtonElement).disabled).toBe(true);

    /* The strip selects a result too. */
    fireEvent.click(screen.getByRole("button", { name: "Result 1" }));
    expect(preview().querySelector("circle")).not.toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(putAgentAvatar).toHaveBeenCalledWith("lana", { svg: FIRST, color: "#12b5a6" });
    expect(onSaved).toHaveBeenCalledWith({
      svg: FIRST,
      color: "#12b5a6",
      restartRequired: false,
    });
    expect(getAgentAvatar("lana")).toEqual({ svg: FIRST, color: "#12b5a6", source: "saved" });
  });

  it("takes a custom hex color", () => {
    render(<AvatarDialog name="lana" color="brand" svg={FIRST} onClose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Custom"), { target: { value: "#ABCDEF" } });
    expect(preview().style.getPropertyValue("--c")).toBe("#abcdef");
    fireEvent.change(screen.getByLabelText("Custom"), { target: { value: "#zz" } });
    expect(screen.getByText("Use a hex color like #ff6a00.")).toBeTruthy();
    expect(preview().style.getPropertyValue("--c")).toBe("#abcdef");
  });

  it("shows a color validation error inline", async () => {
    vi.mocked(putAgentAvatar).mockRejectedValueOnce(
      new ApiError(422, "validation_failed", "Invalid config.", [
        { key: "agents.lana.color", message: "must be a token or #rrggbb" },
      ])
    );
    const onClose = vi.fn();
    render(<AvatarDialog name="lana" color="brand" svg={FIRST} onClose={onClose} />);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Color: must be a token or #rrggbb");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("saves a color change of the saved avatar", async () => {
    const onClose = vi.fn();
    render(<AvatarDialog name="lana" color="brand" svg={FIRST} onClose={onClose} />);
    expect(preview().querySelector("circle")).not.toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Pink" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(putAgentAvatar).toHaveBeenCalledWith("lana", { svg: FIRST, color: "#ff3d9a" });
  });

  it("resets to the default avatar and shows it", async () => {
    const onClose = vi.fn();
    const onSaved = vi.fn();
    render(
      <AvatarDialog
        name="lana"
        color="#ff3d9a"
        svg={FIRST}
        source="saved"
        onClose={onClose}
        onSaved={onSaved}
      />
    );
    fireEvent.click(screen.getByRole("button", { name: "Reset to default" }));
    await waitFor(() => expect(preview().querySelector("ellipse[rx='40']")).not.toBeNull());
    expect(deleteAgentAvatar).toHaveBeenCalledWith("lana");
    expect(onClose).not.toHaveBeenCalled();
    expect(onSaved).toHaveBeenCalledWith({ svg: DEFAULT, color: "brand", restartRequired: false });
    expect(getAgentAvatar("lana")).toEqual({ svg: DEFAULT, color: "brand", source: "default" });
    expect(screen.getByText("The default avatar, drawn from the agent\u2019s name.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Reset to default" })).toBeNull();
    /* The strip keeps the saved avatar to step back to. */
    expect(screen.getByRole("button", { name: "Saved avatar" })).toBeTruthy();
  });

  it("hides Reset to default on the default avatar and shows errors inline", async () => {
    vi.mocked(generateAgentAvatar).mockReset();
    vi.mocked(generateAgentAvatar).mockRejectedValue(
      new ApiError(400, "invalid_model", "model offline")
    );
    render(
      <AvatarDialog name="lana" color="brand" svg={DEFAULT} source="default" onClose={vi.fn()} />
    );
    expect(screen.queryByRole("button", { name: "Reset to default" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("model offline");
  });

  it("lists router models without Claude Code", async () => {
    render(<AvatarDialog name="lana" color="brand" svg="" onClose={vi.fn()} />);
    await waitFor(() => expect(getRouter).toHaveBeenCalled());
    const picker = document.getElementById("avatar-model") as HTMLElement;
    expect(picker.textContent).toContain("Background model");
    fireEvent.click(picker);
    await waitFor(() => expect(screen.getByText("gpt-5-mini")).toBeTruthy());
    expect(screen.queryByText("Sonnet")).toBeNull();
  });
});
