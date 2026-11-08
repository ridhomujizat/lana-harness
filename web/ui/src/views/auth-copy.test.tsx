import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { authState } from "../api/auth";
import type { DaemonInfo } from "../api/daemon";
import type { GetConfigResponse } from "../api/settings";
import * as settingsApi from "../api/settings";
import { LoginPage } from "./login";
import { WelcomePage } from "./welcome";
import { SetupWizard } from "./setup";
import { SettingsGeneralPage } from "./settings-general";

/* AC-WLG-014: the built dashboard must not mention the owner token or the
   daemon output. This renders the login view, the welcome view, the setup
   wizard (step 1), and the settings General view, and checks the text of each.
   Mocking the api modules keeps the views off the network. */

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

const mockedAuthState = vi.mocked(authState);

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

/* Case-insensitive check across every rendered node. */
function expectNoLegacyWording(): void {
  const text = (document.body.textContent || "").toLowerCase();
  expect(text).not.toContain("owner token");
  expect(text).not.toContain("daemon output");
}

afterEach(() => {
  vi.clearAllMocks();
});

describe("AC-WLG-014: no owner token or daemon output in the views", () => {
  it("keeps plain wording in login, welcome, setup step 1, and settings", async () => {
    mockedAuthState.mockResolvedValue({
      password_set: true,
      telegram_reset: false,
      locked_until: null,
    });
    const login = render(<LoginPage onLoggedIn={vi.fn()} />);
    await screen.findByLabelText("Password");
    expectNoLegacyWording();
    login.unmount();

    mockedAuthState.mockResolvedValue({
      password_set: false,
      telegram_reset: false,
      locked_until: null,
    });
    const welcome = render(<WelcomePage code="code" onLoggedIn={vi.fn()} />);
    await screen.findByRole("heading", {
      level: 1,
      name: "Create your password",
    });
    expectNoLegacyWording();
    welcome.unmount();

    const setup = render(<SetupWizard step="provider" />);
    expect(screen.getByText("Connect your models")).toBeTruthy();
    expectNoLegacyWording();
    setup.unmount();

    vi.mocked(settingsApi.getDaemon).mockResolvedValue(daemon);
    vi.mocked(settingsApi.getConfig).mockResolvedValue(config);
    vi.mocked(settingsApi.listBrowserSessions).mockResolvedValue({
      sessions: [],
    });
    const settings = render(
      <SettingsGeneralPage theme="system" onThemeChange={vi.fn()} />
    );
    await screen.findByRole("heading", { name: "Change password" });
    expectNoLegacyWording();
    settings.unmount();
  });
});
