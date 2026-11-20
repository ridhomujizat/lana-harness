import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

vi.mock("../api/agents", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/agents")>();
  return {
    ...actual,
    generateAgentAvatar: vi.fn(),
    getAvatarCatalog: vi.fn(),
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
  getAvatarCatalog,
  putAgentAvatar,
} from "../api/agents";
import type { AvatarCatalog } from "../api/agents";
import { ApiError } from "../api/client";
import { getRouter } from "../api/router";
import type { RouterOverview } from "../api/router";
import { getAgentAvatar, resetAvatarStore } from "../lib/avatar-store";
import type { AvatarDoc } from "../lib/pixel-avatar";
import { SAMPLE_PARTS, sampleAvatar, sampleDrawn } from "../test/pixel-sample";
import { AvatarDialog, MODEL_STORAGE_KEY, defaultDrawModel } from "./avatar-dialog";

/* Spec 62: the pixel avatar dialog. Each test avatar wears its own shirt
   color, so the preview tells them apart by a rect fill. */

const SAVED = sampleAvatar("#aa0000");
const RANDOM = sampleAvatar("#00aa00");
const PICKED: AvatarDoc = {
  ...sampleAvatar("#0000aa"),
  parts: { ...SAMPLE_PARTS, hair: "bob" },
};
const RECOLORED = sampleAvatar("#13b5c8");
const DRAWN = sampleDrawn();
const DEFAULT = sampleAvatar("#777777");

const CATALOG: AvatarCatalog = {
  species: [
    { id: "human", label: "Human" },
    { id: "robot", label: "Robot" },
  ],
  hair: [
    { id: "short", label: "Short" },
    { id: "bob", label: "Bob" },
  ],
  hair_color: [{ id: "dark", label: "Dark" }],
  skin: [{ id: "light", label: "Light" }],
  eyes: [{ id: "dot", label: "Dot" }],
  accessory: [{ id: "none", label: "None" }],
  outfit: [{ id: "plain", label: "Plain" }],
  pants: [{ id: "denim", label: "Denim" }],
};

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

function shows(fill: string): boolean {
  return preview().querySelector('rect[fill="' + fill + '"]') !== null;
}

beforeEach(() => {
  vi.mocked(getRouter).mockResolvedValue(ROUTER);
  vi.mocked(getAvatarCatalog).mockResolvedValue(CATALOG);
  vi.mocked(putAgentAvatar).mockImplementation(async (name, body) => ({
    name,
    avatar: body.avatar,
    avatar_source: "saved",
    color: body.color || "brand",
    restart_required: false,
  }));
  vi.mocked(deleteAgentAvatar).mockResolvedValue({
    name: "lana",
    avatar: DEFAULT,
    avatar_source: "default",
    color: "brand",
  });
});

afterEach(() => {
  window.localStorage.removeItem(MODEL_STORAGE_KEY);
  vi.clearAllMocks();
  vi.mocked(generateAgentAvatar).mockReset();
  resetAvatarStore();
});

describe("AvatarDialog", () => {
  it("randomizes, picks a part, steps through results, and saves", async () => {
    vi.mocked(generateAgentAvatar)
      .mockResolvedValueOnce({ avatar: RANDOM, model: "", seed: 7, source: "local" })
      .mockResolvedValueOnce({ avatar: PICKED, model: "", seed: 0, source: "local" });
    const onClose = vi.fn();
    const onSaved = vi.fn();
    render(
      <AvatarDialog
        name="lana"
        color="brand"
        avatar={SAVED}
        source="saved"
        onClose={onClose}
        onSaved={onSaved}
      />
    );
    expect(shows("#aa0000")).toBe(true);
    expect(preview().querySelectorAll("g.px-f")).toHaveLength(4);
    expect(screen.getByText("The saved avatar.")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Randomize" }));
    await waitFor(() => expect(shows("#00aa00")).toBe(true));
    const randomCall = vi.mocked(generateAgentAvatar).mock.calls[0];
    expect(randomCall?.[0]).toBe("lana");
    expect(randomCall?.[1]).toMatchObject({ mode: "random", color: "brand" });
    expect(screen.getByText("A random character.")).toBeTruthy();

    /* The Hair picker renders the new parts on the server. */
    await waitFor(() => expect(getAvatarCatalog).toHaveBeenCalled());
    const hair = await screen.findByRole("combobox", { name: "Hair" });
    fireEvent.click(hair);
    fireEvent.click(await screen.findByRole("option", { name: "Bob" }));
    await waitFor(() => expect(shows("#0000aa")).toBe(true));
    expect(vi.mocked(generateAgentAvatar).mock.calls[1]?.[1]).toMatchObject({
      mode: "render",
      parts: { hair: "bob", color: "brand" },
      color: "brand",
    });
    expect(screen.getByText("Your pick of parts.")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(shows("#00aa00")).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(shows("#aa0000")).toBe(true);
    expect((screen.getByRole("button", { name: "Undo" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Redo" }));
    expect(shows("#00aa00")).toBe(true);

    /* The strip selects a result too. */
    fireEvent.click(screen.getByRole("button", { name: "Result 3" }));
    expect(shows("#0000aa")).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(putAgentAvatar).toHaveBeenCalledWith("lana", { avatar: PICKED, color: "brand" });
    expect(onSaved).toHaveBeenCalledWith({ avatar: PICKED, color: "brand", restartRequired: false });
    expect(getAgentAvatar("lana")).toEqual({ avatar: PICKED, color: "brand", source: "saved" });
  });

  it("asks the model to draw from a description", async () => {
    vi.mocked(generateAgentAvatar).mockResolvedValueOnce({
      avatar: DRAWN,
      model: "openai/gpt-5-mini",
      seed: 3,
      source: "model",
    });
    render(<AvatarDialog name="lana" color="brand" avatar={SAVED} onClose={vi.fn()} />);
    await waitFor(() => expect(getRouter).toHaveBeenCalled());
    fireEvent.change(screen.getByLabelText("Description"), {
      target: { value: "  a teal wizard  " },
    });
    const picker = document.getElementById("avatar-model") as HTMLElement;
    fireEvent.click(picker);
    fireEvent.click(await screen.findByRole("option", { name: "gpt-5-mini" }));
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    await waitFor(() => expect(shows("#13b5c8")).toBe(true));
    expect(vi.mocked(generateAgentAvatar).mock.calls[0]?.[1]).toMatchObject({
      mode: "ai_draw",
      prompt: "a teal wizard",
      model: "openai/gpt-5-mini",
      color: "brand",
    });
    expect(screen.getByText("Drawn by openai/gpt-5-mini.")).toBeTruthy();
    /* A drawn character has no parts to pick. */
    expect(screen.getByText(/drawn by a model/)).toBeTruthy();
    expect((screen.getByRole("combobox", { name: "Hair" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("always asks for a free drawing and keeps the avatar when the model fails", async () => {
    vi.mocked(generateAgentAvatar).mockResolvedValueOnce({
      avatar: RANDOM,
      model: "",
      seed: 4,
      source: "local",
    });
    render(<AvatarDialog name="lana" color="brand" avatar={SAVED} onClose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Description"), { target: { value: "red cap" } });
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("could not draw a valid character");
    const body = vi.mocked(generateAgentAvatar).mock.calls[0]?.[1];
    expect(body).toMatchObject({ mode: "ai_draw", prompt: "red cap" });
    expect(body).not.toHaveProperty("model");
    /* There is one AI mode: no Parts / Free draw toggle. */
    expect(screen.queryByRole("tab", { name: "Parts" })).toBeNull();
    /* The random stand-in is not shown; the saved avatar stays. */
    expect(shows("#aa0000")).toBe(true);
    expect(shows("#00aa00")).toBe(false);
  });

  it("re-renders a parts avatar in a new color without a new result", async () => {
    vi.mocked(generateAgentAvatar).mockResolvedValueOnce({
      avatar: RECOLORED,
      model: "",
      seed: 0,
      source: "local",
    });
    render(
      <AvatarDialog name="lana" color="brand" avatar={SAVED} source="saved" onClose={vi.fn()} />
    );
    fireEvent.click(screen.getByRole("button", { name: "Teal" }));
    await waitFor(() => expect(shows("#13b5c8")).toBe(true));
    expect(vi.mocked(generateAgentAvatar).mock.calls[0]?.[1]).toMatchObject({
      mode: "render",
      color: "#13b5c8",
      parts: { color: "#13b5c8" },
    });
    expect(preview().style.getPropertyValue("--pxc")).toBe("#13b5c8");
    expect(screen.getByRole("button", { name: "Teal" }).getAttribute("aria-pressed")).toBe("true");
    /* The recolor replaced the shown result: nothing to undo. */
    expect((screen.getByRole("button", { name: "Undo" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("The saved avatar.")).toBeTruthy();
  });

  it("recolors only the tile of a drawn avatar", () => {
    render(<AvatarDialog name="lana" color="brand" avatar={DRAWN} onClose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Custom"), { target: { value: "#ABCDEF" } });
    expect(preview().style.getPropertyValue("--pxc")).toBe("#abcdef");
    expect(generateAgentAvatar).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText("Custom"), { target: { value: "#zz" } });
    expect(screen.getByText("Use a hex color like #ff6a00.")).toBeTruthy();
    expect(preview().style.getPropertyValue("--pxc")).toBe("#abcdef");
  });

  it("shows a color validation error inline", async () => {
    vi.mocked(putAgentAvatar).mockRejectedValueOnce(
      new ApiError(422, "validation_failed", "Invalid config.", [
        { key: "agents.lana.color", message: "must be a token or #rrggbb" },
      ])
    );
    const onClose = vi.fn();
    render(<AvatarDialog name="lana" color="brand" avatar={SAVED} onClose={onClose} />);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Color: must be a token or #rrggbb");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("resets to the default avatar and shows it", async () => {
    const onClose = vi.fn();
    const onSaved = vi.fn();
    render(
      <AvatarDialog
        name="lana"
        color="#ff3d9a"
        avatar={SAVED}
        source="saved"
        onClose={onClose}
        onSaved={onSaved}
      />
    );
    fireEvent.click(screen.getByRole("button", { name: "Reset to default" }));
    await waitFor(() => expect(shows("#777777")).toBe(true));
    expect(deleteAgentAvatar).toHaveBeenCalledWith("lana");
    expect(onClose).not.toHaveBeenCalled();
    expect(onSaved).toHaveBeenCalledWith({ avatar: DEFAULT, color: "brand", restartRequired: false });
    expect(getAgentAvatar("lana")).toEqual({ avatar: DEFAULT, color: "brand", source: "default" });
    expect(screen.getByText("The default avatar, picked from the agent’s name.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Reset to default" })).toBeNull();
    expect(screen.getByRole("button", { name: "Saved avatar" })).toBeTruthy();
  });

  it("hides Reset to default on the default avatar and shows errors inline", async () => {
    vi.mocked(generateAgentAvatar).mockRejectedValue(
      new ApiError(400, "invalid_model", "model offline")
    );
    render(
      <AvatarDialog name="lana" color="brand" avatar={DEFAULT} source="default" onClose={vi.fn()} />
    );
    expect(screen.queryByRole("button", { name: "Reset to default" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("model offline");
  });

  it("lists router models without Claude Code", async () => {
    render(<AvatarDialog name="lana" color="brand" avatar={null} onClose={vi.fn()} />);
    expect(screen.getByText("No avatar yet. Randomize or generate one to start.")).toBeTruthy();
    await waitFor(() => expect(getRouter).toHaveBeenCalled());
    const picker = document.getElementById("avatar-model") as HTMLElement;
    expect(picker.textContent).toContain("Background model");
    fireEvent.click(picker);
    await waitFor(() => expect(screen.getByText("gpt-5-mini")).toBeTruthy());
    expect(screen.queryByText("Sonnet")).toBeNull();
  });

  it("starts drawings on the last pick, else a Claude Sonnet choice", () => {
    const options = [
      { value: "", label: "Background model" },
      { value: "commandcode/deepseek/deepseek-v4.1-flash", label: "flash" },
      { value: "commandcode/claude-sonnet-5-5", label: "Sonnet 5.5" },
    ];
    expect(defaultDrawModel(options, "")).toBe("commandcode/claude-sonnet-5-5");
    expect(defaultDrawModel(options, "commandcode/deepseek/deepseek-v4.1-flash")).toBe(
      "commandcode/deepseek/deepseek-v4.1-flash"
    );
    /* A remembered model that is gone falls back. */
    expect(defaultDrawModel(options, "openai/gone")).toBe("commandcode/claude-sonnet-5-5");
    expect(defaultDrawModel(options.slice(0, 2), "")).toBe("");
  });

  it("remembers the picked model for the next drawing", async () => {
    render(<AvatarDialog name="lana" color="brand" avatar={SAVED} onClose={vi.fn()} />);
    await waitFor(() => expect(getRouter).toHaveBeenCalled());
    fireEvent.click(document.getElementById("avatar-model") as HTMLElement);
    fireEvent.click(await screen.findByRole("option", { name: "gpt-5-mini" }));
    expect(window.localStorage.getItem(MODEL_STORAGE_KEY)).toBe("openai/gpt-5-mini");
  });
});
