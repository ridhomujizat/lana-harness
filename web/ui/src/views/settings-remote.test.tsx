import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import * as auth from "../api/auth";
import * as remote from "../api/remote";
import type { RemoteAccess } from "../api/remote";
import { SettingsRemotePage } from "./settings-remote";
import { RemoteControlBar, useRemoteControl } from "./workbench-remote";

/* Spec 57: Settings > Remote access (REQ-RA-080 to REQ-RA-083) and the
   Workbench remote bar (REQ-RA-085). The api modules are mocked. */

vi.mock("../api/auth", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/auth")>();
  return {
    ...actual,
    getAuthSession: vi.fn(),
    listBrowserSessions: vi.fn(),
  };
});

vi.mock("../api/remote", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/remote")>();
  return {
    ...actual,
    getRemoteAccess: vi.fn(),
    saveRemoteSettings: vi.fn(),
    enableRemoteAccess: vi.fn(),
    disableRemoteAccess: vi.fn(),
    openTailscaleApp: vi.fn(),
    revokeBrowserSession: vi.fn(),
    revokeRemoteSessions: vi.fn(),
    unlockControl: vi.fn(),
    sendControlCode: vi.fn(),
    lockControl: vi.fn(),
  };
});

const readyStatus = {
  installed: true,
  path: "/Applications/Tailscale.app/Contents/MacOS/Tailscale",
  version: "1.90.4",
  running: true,
  dns_name: "mac.tail1.ts.net",
  login: "owner@github",
  tailnet: "owner@github",
  magic_dns: true,
  https_certs: true,
  serving: true,
  serve_other: false,
};

function access(over: Partial<RemoteAccess["config"]> = {}, status = readyStatus): RemoteAccess {
  return {
    config: {
      version: "v1",
      enabled: true,
      host: "mac.tail1.ts.net",
      require_identity: true,
      allowed_logins: ["owner@github"],
      terminal: "confirm",
      confirm_with: "password_or_telegram",
      control_minutes: 15,
      idle_minutes: 60,
      max_age_hours: 168,
      notify_sign_in: true,
      ...over,
    },
    status,
    url: "https://mac.tail1.ts.net",
    checked_at: Date.now(),
  };
}

beforeEach(() => {
  vi.mocked(auth.getAuthSession).mockResolvedValue({ csrf: "c", expires_at: 0, remote: false });
  vi.mocked(auth.listBrowserSessions).mockResolvedValue({
    sessions: [
      {
        id: "aaa",
        user_agent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605.1.15",
        created_at: 1,
        last_used_at: Date.now(),
        current: true,
      },
      {
        id: "bbb",
        user_agent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/604.1",
        created_at: 2,
        last_used_at: Date.now() - 3 * 60000,
        current: false,
        remote: true,
        login: "owner@github",
        device: "owner-iphone",
        ip: "100.88.14.2",
      },
    ],
  });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("SettingsRemotePage", () => {
  it("shows the address, a QR code, and the devices when remote access is on", async () => {
    vi.mocked(remote.getRemoteAccess).mockResolvedValue(access());
    render(<SettingsRemotePage />);
    expect(await screen.findByDisplayValue("https://mac.tail1.ts.net")).toBeTruthy();
    expect(screen.getByRole("img", { name: /QR code/ })).toBeTruthy();
    expect(screen.getByText("Reachable")).toBeTruthy();
    expect(screen.getByText("Safari · iPhone")).toBeTruthy();
    expect(screen.getByText(/owner-iphone · owner@github · 100.88.14.2/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Sign out all remote devices" })).toBeTruthy();
  });

  it("shows the setup checklist while Tailscale is not ready", async () => {
    vi.mocked(remote.getRemoteAccess).mockResolvedValue(
      access({ enabled: false, host: "" }, { ...readyStatus, running: false, https_certs: false, serving: false })
    );
    render(<SettingsRemotePage />);
    expect(await screen.findByText("Tailscale is stopped")).toBeTruthy();
    expect(screen.getByText("HTTPS certificates are off for your tailnet")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Open Tailscale" })).toBeTruthy();
    expect(screen.getByRole("checkbox", { name: "Remote access" })).toHaveProperty("disabled", true);
    expect(screen.queryByText("Who can sign in")).toBeNull();
  });

  it("turns remote access on with the config version", async () => {
    vi.mocked(remote.getRemoteAccess).mockResolvedValue(access({ enabled: false }, { ...readyStatus, serving: false }));
    vi.mocked(remote.enableRemoteAccess).mockResolvedValue(access());
    render(<SettingsRemotePage />);
    const toggle = await screen.findByRole("checkbox", { name: "Remote access" });
    fireEvent.click(toggle);
    await waitFor(() => expect(remote.enableRemoteAccess).toHaveBeenCalledWith("v1"));
    expect(await screen.findByDisplayValue("https://mac.tail1.ts.net")).toBeTruthy();
  });

  it("saves the terminal choice", async () => {
    vi.mocked(remote.getRemoteAccess).mockResolvedValue(access());
    vi.mocked(remote.saveRemoteSettings).mockResolvedValue(access({ terminal: "view" }));
    render(<SettingsRemotePage />);
    await screen.findByText("Control after confirming");
    fireEvent.click(screen.getAllByRole("radio")[0] as HTMLElement);
    await waitFor(() =>
      expect(remote.saveRemoteSettings).toHaveBeenCalledWith(
        expect.objectContaining({ expected_version: "v1", terminal: "view" })
      )
    );
  });

  it("is read-only in a remote browser", async () => {
    vi.mocked(auth.getAuthSession).mockResolvedValue({ csrf: "c", expires_at: 0, remote: true });
    vi.mocked(remote.getRemoteAccess).mockResolvedValue(access());
    render(<SettingsRemotePage />);
    expect(await screen.findByText(/Change these settings on the Mac/)).toBeTruthy();
    expect(screen.getByRole("checkbox", { name: "Remote access" })).toHaveProperty("disabled", true);
  });
});

function Bar() {
  const control = useRemoteControl();
  return (
    <>
      <span data-testid="mode">{control.canControl ? "control" : "view"}</span>
      <RemoteControlBar control={control} />
    </>
  );
}

describe("RemoteControlBar", () => {
  it("renders nothing in a local browser", async () => {
    render(<Bar />);
    await waitFor(() => expect(auth.getAuthSession).toHaveBeenCalled());
    expect(screen.getByTestId("mode").textContent).toBe("control");
    expect(screen.queryByText("Remote · view only")).toBeNull();
  });

  it("unlocks control with the password", async () => {
    vi.mocked(auth.getAuthSession).mockResolvedValue({
      csrf: "c",
      expires_at: 0,
      remote: true,
      terminal: "confirm",
      control_until: 0,
      telegram_code: true,
      login: "owner@github",
      device: "owner-iphone",
    });
    vi.mocked(remote.unlockControl).mockResolvedValue({ control_until: Date.now() + 15 * 60000 });
    render(<Bar />);
    expect(await screen.findByText("Remote · view only")).toBeTruthy();
    expect(screen.getByTestId("mode").textContent).toBe("view");
    fireEvent.click(screen.getByRole("button", { name: "Unlock control" }));
    expect(screen.getByRole("button", { name: "Send code to Telegram" })).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Lana password"), { target: { value: "secret-pass" } });
    await act(async () => {
      fireEvent.submit(screen.getByLabelText("Lana password").closest("form") as HTMLFormElement);
    });
    expect(remote.unlockControl).toHaveBeenCalledWith({ password: "secret-pass" });
    expect(await screen.findByText(/Control · 15 min left/)).toBeTruthy();
    expect(screen.getByTestId("mode").textContent).toBe("control");
  });
});
