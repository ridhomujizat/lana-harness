import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import * as updateApi from "../api/update";
import * as daemonApi from "../api/daemon";
import type { DaemonInfo } from "../api/daemon";
import type { UpdateStatus } from "../api/update";
import { updateFixtures } from "../preview/update-fixtures";
import { UpdateCard, UpdateCardView, cardView, formatBytes } from "./update-card";
import {
  ACTIVE_POLL_MS,
  IDLE_POLL_MS,
  LATER_KEY,
  resetUpdateStore,
} from "./update-store";
import type { UpdateViewState } from "./update-store";

/* The floating update card of spec 36 (REQ-UPD-002..005): which face shows
   for which status, and what each button sends. The api modules are mocked so
   no test reaches the network. */

vi.mock("../api/update", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/update")>();
  return {
    ...actual,
    getUpdate: vi.fn(),
    checkUpdate: vi.fn(),
    applyUpdate: vi.fn(),
    cancelUpdate: vi.fn(),
    skipUpdate: vi.fn(),
    saveUpdateSettings: vi.fn(),
    dismissUpdateResult: vi.fn(),
  };
});

vi.mock("../api/daemon", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/daemon")>();
  return {
    ...actual,
    daemonInfo: vi.fn(),
    waitForDaemonRestart: vi.fn(),
  };
});

const getUpdate = vi.mocked(updateApi.getUpdate);
const applyUpdate = vi.mocked(updateApi.applyUpdate);
const cancelUpdate = vi.mocked(updateApi.cancelUpdate);
const skipUpdate = vi.mocked(updateApi.skipUpdate);
const dismissUpdateResult = vi.mocked(updateApi.dismissUpdateResult);
const daemonInfo = vi.mocked(daemonApi.daemonInfo);
const waitForDaemonRestart = vi.mocked(daemonApi.waitForDaemonRestart);

const daemon = { started_at: 111, version: "0.1.0-beta.1" } as DaemonInfo;

function viewOf(status: UpdateStatus | null, extra: Partial<UpdateViewState> = {}): UpdateViewState {
  return {
    status,
    loadError: "",
    restarting: false,
    restartVersion: "",
    restartError: "",
    busy: "",
    actionError: "",
    laterVersion: "",
    ...extra,
  };
}

function fixture(name: string): UpdateStatus {
  const status = updateFixtures[name];
  if (!status) {
    throw new Error("no fixture " + name);
  }
  return status;
}

beforeEach(() => {
  window.sessionStorage.clear();
  resetUpdateStore();
  daemonInfo.mockResolvedValue(daemon);
});

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
  resetUpdateStore();
});

describe("cardView", () => {
  it("shows nothing without a status, for an unsupported build, or with nothing new", () => {
    expect(cardView(viewOf(null)).kind).toBe("none");
    expect(cardView(viewOf(fixture("unsupported"))).kind).toBe("none");
    expect(cardView(viewOf(fixture("none"))).kind).toBe("none");
    expect(
      cardView(viewOf({ ...fixture("unsupported"), last_result: fixture("updated").last_result })).kind
    ).toBe("none");
  });

  it("offers an available update unless it was skipped or put off", () => {
    expect(cardView(viewOf(fixture("offer"))).kind).toBe("offer");
    expect(cardView(viewOf({ ...fixture("offer"), skipped: "0.1.0" })).kind).toBe("none");
    expect(cardView(viewOf(fixture("offer"), { laterVersion: "0.1.0" })).kind).toBe("none");
    /* AC-UPD-006: a newer release shows again after a skip. */
    expect(
      cardView(
        viewOf({
          ...fixture("offer"),
          skipped: "0.1.0",
          available: { ...fixture("offer").available!, version: "0.1.1" },
        })
      ).kind
    ).toBe("offer");
  });

  it("shows the manual face when the binary cannot be replaced", () => {
    expect(cardView(viewOf(fixture("manual"))).kind).toBe("manual");
  });

  it("shows progress for every active state before restarting", () => {
    for (const name of ["downloading", "verifying", "probing", "waiting", "installing"]) {
      expect(cardView(viewOf(fixture(name))).kind).toBe("progress");
    }
    expect(cardView(viewOf(fixture("restarting"))).kind).toBe("restarting");
    /* A running job ignores Later and a skip. */
    expect(
      cardView(viewOf({ ...fixture("waiting"), skipped: "0.1.0" }, { laterVersion: "0.1.0" })).kind
    ).toBe("progress");
  });

  it("shows the restart wait and its failure", () => {
    expect(cardView(viewOf(fixture("installing"), { restarting: true })).kind).toBe("restarting");
    expect(
      cardView(viewOf(fixture("installing"), { restartError: "did not come back" })).kind
    ).toBe("restart-error");
  });

  it("shows a failed job until Later, then the offer stays hidden too", () => {
    expect(cardView(viewOf(fixture("failed"))).kind).toBe("failed");
    expect(cardView(viewOf(fixture("failed"), { laterVersion: "0.1.0" })).kind).toBe("none");
  });

  it("shows the last result even when nothing is available", () => {
    expect(cardView(viewOf(fixture("updated"))).kind).toBe("result");
    expect(cardView(viewOf(fixture("rolled-back"))).kind).toBe("result");
    expect(cardView(viewOf(fixture("result-failed"), { laterVersion: "0.1.0" })).kind).toBe("result");
  });
});

describe("UpdateCardView faces", () => {
  it("renders the offer with notes, upgrade notes, and the three buttons", () => {
    render(<UpdateCardView view={viewOf(fixture("offer-notes"))} />);
    expect(screen.getByRole("heading", { name: "lana 0.2.0-beta.1 is available" })).toBeTruthy();
    expect(screen.getByText("Prerelease")).toBeTruthy();
    expect(screen.getByText("Upgrade notes.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Update now" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Later" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Skip this version" })).toBeTruthy();
    const more = screen.getByRole("button", { name: "Show changes" });
    expect(more.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(more);
    expect(screen.getByRole("button", { name: "Hide changes" }).getAttribute("aria-expanded")).toBe(
      "true"
    );
  });

  it("renders the download with bytes and a progress bar", () => {
    render(<UpdateCardView view={viewOf(fixture("downloading"))} />);
    expect(screen.getByRole("status").textContent).toBe("Downloading 11.8 MB of 29.6 MB");
    expect(screen.getByRole("progressbar").getAttribute("aria-valuenow")).toBe("40");
    expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
    const current = document.querySelector('[aria-current="step"]');
    expect(current && current.textContent).toBe("Downloading");
  });

  it("renders the waiting step with the turn count and countdown", () => {
    const status = fixture("waiting");
    const job = { ...status.job!, deadline: Date.now() + 30_000 };
    render(<UpdateCardView view={viewOf({ ...status, job })} />);
    expect(screen.getByRole("status").textContent).toMatch(/^Waiting for 2 running turns · (30|29) s left$/);
    expect(screen.getByRole("button", { name: "Interrupt and update" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
  });

  it("does not offer Cancel once installing", () => {
    render(<UpdateCardView view={viewOf(fixture("installing"))} />);
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
    const current = document.querySelector('[aria-current="step"]');
    expect(current && current.textContent).toBe("Installing");
  });

  it("renders the restart wait", () => {
    render(<UpdateCardView view={viewOf(fixture("installing"), { restarting: true, restartVersion: "0.1.0" })} />);
    expect(screen.getByRole("heading", { name: "Restarting into lana 0.1.0" })).toBeTruthy();
    expect(screen.getByRole("status").textContent).toContain("Waiting for lana to come back");
  });

  it("renders the results", () => {
    const { unmount } = render(<UpdateCardView view={viewOf(fixture("updated"))} />);
    expect(screen.getByRole("heading", { name: "Updated to lana 0.1.0" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Dismiss" })).toBeTruthy();
    unmount();

    const rolled = render(<UpdateCardView view={viewOf(fixture("rolled-back"))} />);
    expect(
      screen.getByRole("heading", { name: "Update to lana 0.1.0 failed and was rolled back" })
    ).toBeTruthy();
    expect(screen.getByText(/database disk image is malformed/)).toBeTruthy();
    rolled.unmount();

    render(<UpdateCardView view={viewOf(fixture("result-failed"))} />);
    expect(screen.getByRole("heading", { name: "Update to lana 0.1.0 failed" })).toBeTruthy();
    expect(screen.getByText(/does not match SHA256SUMS/)).toBeTruthy();
  });

  it("renders a failed job with the reason and Try again", () => {
    render(<UpdateCardView view={viewOf(fixture("failed"))} />);
    expect(screen.getByRole("alert").textContent).toContain("update-probe exited 1");
    expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
  });

  it("renders the manual face with the reason and a copyable command", () => {
    render(<UpdateCardView view={viewOf(fixture("manual"))} />);
    expect(screen.queryByRole("button", { name: "Update now" })).toBeNull();
    expect(screen.getByText(/\/nix\/store, which is read-only/)).toBeTruthy();
    expect(screen.getByText(/install\.sh \| bash/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Copy command" })).toBeTruthy();
  });

  it("formats bytes", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(2048)).toBe("2 KB");
    expect(formatBytes(31_000_000)).toBe("29.6 MB");
  });
});

describe("UpdateCard with the store", () => {
  it("loads the status and shows the offer", async () => {
    getUpdate.mockResolvedValue(fixture("offer"));
    render(<UpdateCard />);
    expect(await screen.findByRole("heading", { name: "lana 0.1.0 is available" })).toBeTruthy();
    expect(getUpdate).toHaveBeenCalledTimes(1);
  });

  it("Later hides the card for this browser session", async () => {
    getUpdate.mockResolvedValue(fixture("offer"));
    render(<UpdateCard />);
    fireEvent.click(await screen.findByRole("button", { name: "Later" }));
    expect(screen.queryByRole("heading", { name: /is available/ })).toBeNull();
    expect(window.sessionStorage.getItem(LATER_KEY)).toBe("0.1.0");
  });

  it("Escape works as Later", async () => {
    getUpdate.mockResolvedValue(fixture("offer"));
    render(<UpdateCard />);
    const button = await screen.findByRole("button", { name: "Update now" });
    fireEvent.keyDown(button, { key: "Escape" });
    expect(screen.queryByRole("button", { name: "Update now" })).toBeNull();
  });

  it("stays hidden when Later was clicked earlier in the session", async () => {
    window.sessionStorage.setItem(LATER_KEY, "0.1.0");
    resetUpdateStore();
    getUpdate.mockResolvedValue(fixture("offer"));
    render(<UpdateCard />);
    await waitFor(() => expect(getUpdate).toHaveBeenCalled());
    expect(screen.queryByRole("heading", { name: /is available/ })).toBeNull();
  });

  it("Skip this version posts the skip", async () => {
    getUpdate.mockResolvedValue(fixture("offer"));
    skipUpdate.mockResolvedValue({ ...fixture("offer"), skipped: "0.1.0" });
    render(<UpdateCard />);
    fireEvent.click(await screen.findByRole("button", { name: "Skip this version" }));
    await waitFor(() => expect(skipUpdate).toHaveBeenCalledWith("0.1.0"));
    await waitFor(() => expect(screen.queryByRole("heading", { name: /is available/ })).toBeNull());
  });

  it("Update now reads started_at, applies, and shows the progress", async () => {
    getUpdate.mockResolvedValue(fixture("offer"));
    applyUpdate.mockResolvedValue(fixture("downloading"));
    render(<UpdateCard />);
    fireEvent.click(await screen.findByRole("button", { name: "Update now" }));
    await waitFor(() => expect(applyUpdate).toHaveBeenCalledWith("0.1.0", false));
    expect(daemonInfo).toHaveBeenCalled();
    expect(await screen.findByRole("heading", { name: "Updating to lana 0.1.0" })).toBeTruthy();
  });

  it("shows the apply error in the card", async () => {
    getUpdate.mockResolvedValue(fixture("offer"));
    applyUpdate.mockRejectedValue(new Error("offline"));
    render(<UpdateCard />);
    fireEvent.click(await screen.findByRole("button", { name: "Update now" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
  });

  it("Interrupt and update posts apply with force", async () => {
    getUpdate.mockResolvedValue(fixture("waiting"));
    applyUpdate.mockResolvedValue(fixture("installing"));
    render(<UpdateCard />);
    fireEvent.click(await screen.findByRole("button", { name: "Interrupt and update" }));
    await waitFor(() => expect(applyUpdate).toHaveBeenCalledWith("0.1.0", true));
  });

  it("Cancel deletes the job and reads the status again", async () => {
    getUpdate.mockResolvedValueOnce(fixture("verifying")).mockResolvedValue({
      ...fixture("offer"),
      job: { ...fixture("verifying").job!, state: "cancelled" },
    });
    cancelUpdate.mockResolvedValue({ cancelled: true });
    render(<UpdateCard />);
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(cancelUpdate).toHaveBeenCalled());
    expect(await screen.findByRole("heading", { name: "lana 0.1.0 is available" })).toBeTruthy();
  });

  it("Dismiss clears the result", async () => {
    getUpdate.mockResolvedValue(fixture("updated"));
    dismissUpdateResult.mockResolvedValue(undefined);
    render(<UpdateCard />);
    fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
    await waitFor(() => expect(dismissUpdateResult).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByRole("heading", { name: /Updated to/ })).toBeNull());
  });

  it("waits for the restart and then shows Updated to (AC-UPD-010)", async () => {
    getUpdate
      .mockResolvedValueOnce(fixture("restarting"))
      .mockResolvedValue(fixture("updated"));
    let finish: (info: DaemonInfo) => void = () => {};
    waitForDaemonRestart.mockImplementation(
      () => new Promise<DaemonInfo>((resolve) => (finish = resolve))
    );
    render(<UpdateCard />);
    expect(await screen.findByRole("heading", { name: "Restarting into lana 0.1.0" })).toBeTruthy();
    await waitFor(() => expect(waitForDaemonRestart).toHaveBeenCalled());
    await act(async () => {
      finish({ ...daemon, started_at: 222 });
    });
    expect(await screen.findByRole("heading", { name: "Updated to lana 0.1.0" })).toBeTruthy();
  });

  it("treats a lost daemon during installing as the restart", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    getUpdate
      .mockResolvedValueOnce(fixture("installing"))
      .mockRejectedValueOnce(new Error("down"))
      .mockResolvedValue(fixture("updated"));
    waitForDaemonRestart.mockResolvedValue({ ...daemon, started_at: 222 });
    render(<UpdateCard />);
    expect(await screen.findByRole("heading", { name: "Updating to lana 0.1.0" })).toBeTruthy();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(ACTIVE_POLL_MS);
    });
    await waitFor(() => expect(waitForDaemonRestart).toHaveBeenCalled());
    expect(await screen.findByRole("heading", { name: "Updated to lana 0.1.0" })).toBeTruthy();
  });

  it("polls every second during a job and every 5 minutes when idle", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    getUpdate.mockResolvedValue(fixture("downloading"));
    render(<UpdateCard />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(getUpdate).toHaveBeenCalledTimes(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(ACTIVE_POLL_MS);
    });
    expect(getUpdate).toHaveBeenCalledTimes(2);

    getUpdate.mockResolvedValue(fixture("offer"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(ACTIVE_POLL_MS);
    });
    expect(getUpdate).toHaveBeenCalledTimes(3);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(IDLE_POLL_MS - 1000);
    });
    expect(getUpdate).toHaveBeenCalledTimes(3);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(getUpdate).toHaveBeenCalledTimes(4);
  });
});
