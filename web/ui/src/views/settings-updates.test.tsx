import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import * as updateApi from "../api/update";
import * as daemonApi from "../api/daemon";
import type { DaemonInfo } from "../api/daemon";
import { updateFixtures } from "../preview/update-fixtures";
import { SettingsUpdatesSection, relativeTime } from "./settings-updates";
import { resetUpdateStore } from "./update-store";

/* Settings > General > Updates (REQ-UPD-007). */

vi.mock("../api/update", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/update")>();
  return {
    ...actual,
    getUpdate: vi.fn(),
    checkUpdate: vi.fn(),
    applyUpdate: vi.fn(),
    saveUpdateSettings: vi.fn(),
  };
});

vi.mock("../api/daemon", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/daemon")>();
  return { ...actual, daemonInfo: vi.fn() };
});

const getUpdate = vi.mocked(updateApi.getUpdate);
const checkUpdate = vi.mocked(updateApi.checkUpdate);
const applyUpdate = vi.mocked(updateApi.applyUpdate);
const saveUpdateSettings = vi.mocked(updateApi.saveUpdateSettings);

beforeEach(() => {
  window.sessionStorage.clear();
  resetUpdateStore();
  vi.mocked(daemonApi.daemonInfo).mockResolvedValue({ started_at: 1 } as DaemonInfo);
});

afterEach(() => {
  vi.clearAllMocks();
  resetUpdateStore();
});

describe("SettingsUpdatesSection", () => {
  it("shows the version, last check, channel, and automatic check", async () => {
    getUpdate.mockResolvedValue(updateFixtures.none!);
    render(<SettingsUpdatesSection />);
    expect(await screen.findByText("lana 0.1.0-beta.1")).toBeTruthy();
    expect(screen.getByText(/Last checked 12 min ago\. lana is up to date\./)).toBeTruthy();
    expect(screen.getByRole("combobox", { name: "Update channel" }).textContent).toContain("Stable");
    const toggle = screen.getByRole("checkbox", { name: "Check for updates automatically" });
    expect((toggle as HTMLInputElement).checked).toBe(true);
    expect(screen.queryByRole("button", { name: "Update" })).toBeNull();
  });

  it("shows the check error", async () => {
    getUpdate.mockResolvedValue(updateFixtures["check-error"]!);
    render(<SettingsUpdatesSection />);
    expect((await screen.findByRole("alert")).textContent).toContain("i/o timeout");
  });

  it("Check now posts the check and reports an available update", async () => {
    getUpdate.mockResolvedValue(updateFixtures.none!);
    checkUpdate.mockResolvedValue(updateFixtures.offer!);
    render(<SettingsUpdatesSection />);
    fireEvent.click(await screen.findByRole("button", { name: "Check now" }));
    await waitFor(() => expect(checkUpdate).toHaveBeenCalled());
    expect(await screen.findByRole("status")).toBeTruthy();
    expect(screen.getByRole("status").textContent).toBe("lana 0.1.0 is available.");
    expect(screen.getByRole("button", { name: "Update" })).toBeTruthy();
  });

  it("Update starts the same job as the card", async () => {
    getUpdate.mockResolvedValue(updateFixtures.offer!);
    applyUpdate.mockResolvedValue(updateFixtures.downloading!);
    render(<SettingsUpdatesSection />);
    fireEvent.click(await screen.findByRole("button", { name: "Update" }));
    await waitFor(() => expect(applyUpdate).toHaveBeenCalledWith("0.1.0", false));
    expect(await screen.findByText("Updating to lana 0.1.0")).toBeTruthy();
  });

  it("turning off the automatic check patches the settings", async () => {
    getUpdate.mockResolvedValue(updateFixtures.none!);
    saveUpdateSettings.mockResolvedValue({ ...updateFixtures.none!, check: false });
    render(<SettingsUpdatesSection />);
    fireEvent.click(await screen.findByRole("checkbox", { name: "Check for updates automatically" }));
    await waitFor(() => expect(saveUpdateSettings).toHaveBeenCalledWith({ check: false }));
  });

  it("shows the manual command when lana cannot update itself", async () => {
    getUpdate.mockResolvedValue(updateFixtures.manual!);
    render(<SettingsUpdatesSection />);
    expect(await screen.findByText(/\/nix\/store, which is read-only/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Update" })).toBeNull();
  });

  it("says when the build cannot update itself", async () => {
    getUpdate.mockResolvedValue(updateFixtures.unsupported!);
    render(<SettingsUpdatesSection />);
    expect(await screen.findByText(/This build cannot update itself/)).toBeTruthy();
  });

  it("formats relative times", () => {
    const now = 10 * 86_400_000;
    expect(relativeTime(null, now)).toBe("");
    expect(relativeTime(now - 10_000, now)).toBe("just now");
    expect(relativeTime(now - 5 * 60_000, now)).toBe("5 min ago");
    expect(relativeTime(now - 3 * 3_600_000, now)).toBe("3 h ago");
    expect(relativeTime(now - 2 * 86_400_000, now)).toBe("2 d ago");
  });
});
