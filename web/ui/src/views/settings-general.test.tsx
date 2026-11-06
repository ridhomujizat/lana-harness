import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ApiError } from "../api/client";
import { changePassword } from "../api/auth";
import type { DaemonInfo } from "../api/daemon";
import type { GetConfigResponse } from "../api/settings";
import * as settingsApi from "../api/settings";
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
