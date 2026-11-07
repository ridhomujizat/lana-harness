import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import {
  SettingsAuditPage,
  SettingsConfigPage,
  SettingsPanel,
  SettingsRestartDialog,
  SettingsSecretsPage,
  SettingsTabs,
  markSettingsSaved,
} from "./settings-pages";
import * as settingsApi from "../api/settings";
import * as daemonApi from "../api/daemon";
import type { GetConfigResponse } from "../api/settings";
import type { SecretRow } from "../api/secrets";
import type { AuditEntry } from "../api/audit";
import type { DaemonInfo } from "../api/daemon";

vi.mock("../api/settings", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/settings")>();
  return {
    ...actual,
    getConfig: vi.fn(),
    getDaemon: vi.fn(),
    listProviders: vi.fn(),
    listSecrets: vi.fn(),
    listAudit: vi.fn(),
    saveConfig: vi.fn(),
    saveProvider: vi.fn(),
    deleteProvider: vi.fn(),
    testProvider: vi.fn(),
    setSecret: vi.fn(),
    deleteSecret: vi.fn(),
    restartDaemon: vi.fn(),
    waitForDaemonRestart: vi.fn(),
  };
});

/* The restart dialog reads the daemon through api/daemon directly, so that
   module is mocked too (REQ-TFB-003, API-TFB-002). */
vi.mock("../api/daemon", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/daemon")>();
  return {
    ...actual,
    daemonInfo: vi.fn(),
    restartDaemon: vi.fn(),
    cancelDaemonRestart: vi.fn(),
    waitForDaemonRestart: vi.fn(),
  };
});

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

const secret: SecretRow = {
  name: "OPENAI_API_KEY",
  state: "set",
  source: "file",
  file: "secrets.env",
  overridden_by_env: false,
  updated_at: Date.now() - 60000,
  used_by: ["providers.openai"],
};

const auditEntry: AuditEntry = {
  time: Date.now(),
  kind: "settings.save",
  target: "config.toml",
  changed_keys: ["agents.coder.model"],
};

describe("settings shell", () => {
  it("marks the active section and moves focus with the arrow keys", () => {
    render(
      <>
        <SettingsTabs active="secrets" />
        <SettingsPanel active="secrets">Panel body</SettingsPanel>
      </>
    );

    /* Spec 37: Providers moved to the Router page. */
    expect(screen.queryByRole("link", { name: "Providers" })).toBeNull();
    const active = screen.getByRole("link", { name: "Secrets" });
    expect(active.getAttribute("aria-current")).toBe("page");
    expect(active.className).toContain("on");
    expect(screen.getByText("Panel body")).toBeTruthy();

    active.focus();
    fireEvent.keyDown(active, { key: "ArrowDown" });
    expect(document.activeElement).toBe(
      screen.getByRole("link", { name: "Config file" })
    );
  });

  it("records when settings were saved for the restart banner", () => {
    markSettingsSaved();
    const savedAt = Number(
      window.localStorage.getItem("lana.settings.savedAt")
    );
    expect(savedAt).toBeGreaterThan(0);
  });
});

describe("settings pages", () => {
  beforeEach(() => {
    /* jsdom has no modal dialog implementation. */
    HTMLDialogElement.prototype.showModal = function (this: HTMLDialogElement) {
      this.open = true;
    };
    HTMLDialogElement.prototype.close = function (this: HTMLDialogElement) {
      this.open = false;
    };
    vi.clearAllMocks();
    vi.mocked(settingsApi.getConfig).mockResolvedValue(config);
    vi.mocked(settingsApi.getDaemon).mockResolvedValue(daemon);
    vi.mocked(daemonApi.daemonInfo).mockResolvedValue(daemon);
    vi.mocked(settingsApi.listSecrets).mockResolvedValue({
      version: "v1",
      secrets: [secret],
      restart_required: false,
    });
    vi.mocked(settingsApi.listAudit).mockResolvedValue({
      entries: [auditEntry],
      next_before: null,
    });
  });

  it("renders the secret row and its usage", async () => {
    render(<SettingsSecretsPage />);
    expect(await screen.findByText("OPENAI_API_KEY")).toBeTruthy();
    expect(screen.getByText("providers.openai")).toBeTruthy();
  });

  it("opens the secret row menu with replace and remove", async () => {
    render(<SettingsSecretsPage />);
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Actions for OPENAI_API_KEY",
      })
    );
    expect(
      await screen.findByRole("menuitem", { name: "Replace" })
    ).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Remove" })).toBeTruthy();
  });

  it("loads config.toml into the editor", async () => {
    render(<SettingsConfigPage />);
    const editor = (await screen.findByLabelText(
      "config.toml contents"
    )) as HTMLTextAreaElement;
    expect(editor.value).toBe("[agents]\n");
    expect(screen.getByText("/home/lana/config.toml")).toBeTruthy();
  });

  it("renders audit entries and the end of the log", async () => {
    render(<SettingsAuditPage />);
    expect(await screen.findByText("settings.save")).toBeTruthy();
    expect(screen.getByText("agents.coder.model")).toBeTruthy();
    expect(
      await screen.findByText("That is every entry in the audit file.")
    ).toBeTruthy();
  });

  it("reports a finished restart and closes the dialog", async () => {
    const onClose = vi.fn();
    const onRestarted = vi.fn();
    vi.mocked(daemonApi.daemonInfo).mockResolvedValue(daemon);
    vi.mocked(daemonApi.restartDaemon).mockResolvedValue({
      restart: "scheduled",
      running_turns: 0,
    });
    vi.mocked(daemonApi.waitForDaemonRestart).mockResolvedValue(daemon);

    render(
      <SettingsRestartDialog onClose={onClose} onRestarted={onRestarted} />
    );
    const restart = screen.getByRole("button", { name: "Restart daemon" });
    await waitFor(() => {
      expect((restart as HTMLButtonElement).disabled).toBe(false);
    });
    fireEvent.click(restart);

    await waitFor(() => {
      expect(onRestarted).toHaveBeenCalledTimes(1);
    });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(vi.mocked(daemonApi.waitForDaemonRestart)).toHaveBeenCalledWith(
      daemon.started_at
    );
  });
});
