import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ApiError } from "../api/client";
import { changePassword } from "../api/auth";
import type { DaemonInfo } from "../api/daemon";
import type { GetConfigResponse } from "../api/settings";
import * as settingsApi from "../api/settings";
import * as workbenchApi from "../api/workbench";
import type { AgentHookState } from "../api/workbench";
import { SettingsGeneralPage } from "./settings-general";

/* Settings > General loads the daemon, the config, and the browser sessions,
   then offers the "Change password" row (REQ-WLG-007, API-WLG-007). Mocking
   the api modules keeps the tests off the network. */

vi.mock("../api/auth", () => ({
  authState: vi.fn(),
  login: vi.fn(),
  claim: vi.fn(),
  changePassword: vi.fn(),
  authSession: vi.fn(),
  getAuthSession: vi.fn(),
  logout: vi.fn(),
  resetTelegram: vi.fn(),
  resetVerify: vi.fn(),
  resetRestart: vi.fn(),
  listBrowserSessions: vi.fn(),
  revokeOtherBrowsers: vi.fn(),
}));

vi.mock("../api/settings", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/settings")>();
  return {
    ...actual,
    getDaemon: vi.fn(),
    getConfig: vi.fn(),
    listBrowserSessions: vi.fn(),
    revokeOtherBrowsers: vi.fn(),
    saveWebAddress: vi.fn(),
  };
});

vi.mock("../api/workbench", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/workbench")>();
  return { ...actual, getAgentHook: vi.fn(), setAgentHook: vi.fn() };
});

const mockedChangePassword = vi.mocked(changePassword);

const daemon: DaemonInfo = {
  pid: 1234,
  started_at: 1,
  uptime_ms: 60000,
  addr: "127.0.0.1:18300",
  home: "/home/lana",
  version: "0.1.0",
  go_version: "go1.23",
  os: "linux",
  arch: "amd64",
  restart_supported: true,
  restart: { state: "idle", deadline: null },
  running_turns: [],
  browsers_signed_in: 1,
};

const config: GetConfigResponse = {
  version: "v1",
  loaded_version: "v1",
  restart_required: false,
  validation: { ok: true, errors: [] },
  raw: "[agents]\n",
  settings: {
    providers: {},
    agents: {},
    routes: [],
    channels: {},
    mcp: {},
    modules: [],
  },
};

function renderGeneral() {
  return render(<SettingsGeneralPage theme="system" onThemeChange={vi.fn()} />);
}

async function fillPasswordForm(
  current: string,
  next: string,
  confirm: string
): Promise<void> {
  fireEvent.change(await screen.findByLabelText("Current password"), {
    target: { value: current },
  });
  fireEvent.change(screen.getByLabelText("New password"), {
    target: { value: next },
  });
  fireEvent.change(screen.getByLabelText("Type it again"), {
    target: { value: confirm },
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(settingsApi.getDaemon).mockResolvedValue(daemon);
  vi.mocked(settingsApi.getConfig).mockResolvedValue(config);
  vi.mocked(settingsApi.listBrowserSessions).mockResolvedValue({ sessions: [] });
  vi.mocked(workbenchApi.getAgentHook).mockResolvedValue(hookState({}));
});

describe("settings general change password", () => {
  it("changes the password, posts the right body, and clears the fields", async () => {
    mockedChangePassword.mockResolvedValue(null);
    renderGeneral();

    await fillPasswordForm("old-password", "new-password-1", "new-password-1");
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(
      await screen.findByText("Password changed. Other browsers were signed out.")
    ).toBeTruthy();
    expect(mockedChangePassword).toHaveBeenCalledWith(
      "old-password",
      "new-password-1"
    );
    expect(
      (screen.getByLabelText("Current password") as HTMLInputElement).value
    ).toBe("");
    expect(
      (screen.getByLabelText("New password") as HTMLInputElement).value
    ).toBe("");
  });

  it("shows the 401 message when the current password is wrong", async () => {
    mockedChangePassword.mockRejectedValue(
      new ApiError(401, "invalid_password", "nope")
    );
    renderGeneral();

    await fillPasswordForm("wrong-password", "new-password-1", "new-password-1");
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByText("That password is not right.")).toBeTruthy();
  });

  it("blocks the API call when the new password is shorter than 8 characters", async () => {
    renderGeneral();

    await fillPasswordForm("old-password", "short", "short");
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByText("Use at least 8 characters.")).toBeTruthy();
    expect(mockedChangePassword).not.toHaveBeenCalled();
  });

  it("blocks the API call when the confirmation does not match", async () => {
    renderGeneral();

    await fillPasswordForm("old-password", "new-password-1", "different-1");
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(
      await screen.findByText("The two passwords do not match.")
    ).toBeTruthy();
    expect(mockedChangePassword).not.toHaveBeenCalled();
  });
});

const claudeDir = { dir: "/Users/me/.claude", account: "claude-code", kind: "claude" as const, installed: true };
const agyDir = { dir: "/Users/me/.gemini/config", account: "antigravity", kind: "antigravity" as const, installed: true };

function hookState(over: Partial<AgentHookState>): AgentHookState {
  return { enabled: true, state: "on", needs_install: false, folders: [claudeDir, agyDir], ...over };
}

function workbenchSection(): HTMLElement {
  return screen.getByRole("heading", { name: "Workbench" }).closest("section") as HTMLElement;
}

/* Spec 77 REQ-WRA-009: the Workbench hook switch lives in Settings > General. */
describe("settings general workbench hooks", () => {
  it("shows the switch with the folder count and no Install when every folder has the hooks", async () => {
    renderGeneral();
    const toggle = (await screen.findByRole("checkbox", {
      name: "Connect Claude Code and Antigravity to the Workbench",
    })) as HTMLInputElement;
    expect(toggle.checked).toBe(true);
    const section = workbenchSection();
    expect(section.textContent).toContain("Installed in 2 of 2 folders (Claude 1, Antigravity 1).");
    expect(within(section).queryByRole("button", { name: "Install" })).toBeNull();
  });

  it("turns the hooks off from the switch", async () => {
    vi.mocked(workbenchApi.setAgentHook).mockResolvedValue(hookState({ enabled: false, state: "off" }));
    renderGeneral();
    const toggle = (await screen.findByRole("checkbox", {
      name: "Connect Claude Code and Antigravity to the Workbench",
    })) as HTMLInputElement;
    fireEvent.click(toggle);
    expect(workbenchApi.setAgentHook).toHaveBeenCalledWith(false);
    await waitFor(() => expect(toggle.checked).toBe(false));
    expect(workbenchSection().textContent).not.toContain("Installed in");
  });

  it("offers Install while a folder lacks the hooks, even after the notice was dismissed", async () => {
    vi.mocked(workbenchApi.getAgentHook).mockResolvedValue(
      hookState({ enabled: false, state: "off", needs_install: true, folders: [{ ...claudeDir, installed: false }] }),
    );
    vi.mocked(workbenchApi.setAgentHook).mockResolvedValue(
      hookState({ needs_install: true, folders: [{ ...claudeDir, installed: false, error: "permission denied" }] }),
    );
    renderGeneral();
    await screen.findByRole("heading", { name: "Workbench" });
    fireEvent.click(await within(workbenchSection()).findByRole("button", { name: "Install" }));
    expect(workbenchApi.setAgentHook).toHaveBeenCalledWith(true);
    expect(await screen.findByText("Some agent folders could not be updated")).toBeTruthy();
    expect(screen.getByText("/Users/me/.claude: permission denied")).toBeTruthy();
  });

  it("shows why the hooks could not be read", async () => {
    vi.mocked(workbenchApi.getAgentHook).mockRejectedValue(new ApiError(503, "unavailable", "hooks unavailable"));
    renderGeneral();
    expect(await screen.findByText("hooks unavailable")).toBeTruthy();
  });
});
