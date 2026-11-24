import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

import { getAgentHook, setAgentHook, type AgentHookState } from "../api/workbench";
import { AgentHookNotice } from "./workbench-accounts";

/* Spec 69: the Claude Code hooks are installed only by the owner's action. The
   notice shows while the owner was never asked and a folder lacks the hooks. */
vi.mock("../api/workbench", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/workbench")>();
  return { ...actual, getAgentHook: vi.fn(), setAgentHook: vi.fn() };
});

const folder = { dir: "/Users/me/.claude", account: "claude-code", installed: false };

function hookState(over: Partial<AgentHookState>): AgentHookState {
  return { enabled: false, state: "unset", needs_install: true, folders: [folder], ...over };
}

afterEach(() => {
  cleanup();
  vi.mocked(getAgentHook).mockReset();
  vi.mocked(setAgentHook).mockReset();
});

describe("AgentHookNotice", () => {
  it("asks when the hooks are not installed and the owner was never asked", async () => {
    vi.mocked(getAgentHook).mockResolvedValue(hookState({}));
    render(<AgentHookNotice />);
    expect(await screen.findByText("Connect Claude Code to the Workbench")).toBeTruthy();
  });

  it("stays hidden once installed, declined, or with nothing to install", async () => {
    for (const s of [
      hookState({ state: "on", enabled: true, needs_install: false, folders: [{ ...folder, installed: true }] }),
      hookState({ state: "off" }),
      hookState({ state: "unset", needs_install: false }),
    ]) {
      vi.mocked(getAgentHook).mockResolvedValue(s);
      const { container } = render(<AgentHookNotice />);
      await waitFor(() => expect(getAgentHook).toHaveBeenCalled());
      expect(container.querySelector(".wb-hook-notice")).toBeNull();
      cleanup();
      vi.mocked(getAgentHook).mockReset();
    }
  });

  it("installs on Install and then disappears", async () => {
    vi.mocked(getAgentHook).mockResolvedValue(hookState({}));
    vi.mocked(setAgentHook).mockResolvedValue(
      hookState({ state: "on", enabled: true, needs_install: false, folders: [{ ...folder, installed: true }] }),
    );
    const { container } = render(<AgentHookNotice />);
    fireEvent.click(await screen.findByRole("button", { name: "Install" }));
    expect(setAgentHook).toHaveBeenCalledWith(true);
    await waitFor(() => expect(container.querySelector(".wb-hook-notice")).toBeNull());
  });

  it("Not now hides it without writing; Don't ask again stores off", async () => {
    vi.mocked(getAgentHook).mockResolvedValue(hookState({}));
    const first = render(<AgentHookNotice />);
    fireEvent.click(await screen.findByRole("button", { name: "Not now" }));
    expect(setAgentHook).not.toHaveBeenCalled();
    expect(first.container.querySelector(".wb-hook-notice")).toBeNull();
    cleanup();

    vi.mocked(setAgentHook).mockResolvedValue(hookState({ state: "off" }));
    const second = render(<AgentHookNotice />);
    fireEvent.click(await screen.findByRole("button", { name: "Don't ask again" }));
    expect(setAgentHook).toHaveBeenCalledWith(false);
    await waitFor(() => expect(second.container.querySelector(".wb-hook-notice")).toBeNull());
  });

  it("shows a folder that failed after Install", async () => {
    vi.mocked(getAgentHook).mockResolvedValue(hookState({}));
    vi.mocked(setAgentHook).mockResolvedValue(
      hookState({
        state: "on",
        enabled: true,
        needs_install: true,
        folders: [{ ...folder, error: "settings.json is not valid JSON" }],
      }),
    );
    render(<AgentHookNotice />);
    fireEvent.click(await screen.findByRole("button", { name: "Install" }));
    expect(await screen.findByText(/settings.json is not valid JSON/)).toBeTruthy();
  });
});
